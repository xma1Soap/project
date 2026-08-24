package wizard

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/xma1Soap/project/quota-agent/internal/config"
	"github.com/xma1Soap/project/quota-agent/internal/newapi"
)

//go:embed page.html
var assets embed.FS

type Server struct {
	Listen        string
	Output        string
	Out           io.Writer
	ExitAfterSave bool
}

func (s Server) Serve(ctx context.Context) error {
	if s.Out == nil {
		s.Out = os.Stdout
	}
	host, _, err := net.SplitHostPort(s.Listen)
	if err != nil {
		return fmt.Errorf("invalid wizard listen address: %w", err)
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errors.New("wizard must listen on loopback")
	}
	if strings.TrimSpace(s.Output) == "" {
		return errors.New("wizard output path is required")
	}
	token, err := setupToken()
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	saved := make(chan struct{}, 1)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token {
			http.Error(w, "invalid setup token", http.StatusForbidden)
			return
		}
		page, readErr := assets.ReadFile("page.html")
		if readErr != nil {
			http.Error(w, "setup page unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /api/current", s.authorize(token, func(w http.ResponseWriter, _ *http.Request) {
		cfg, loadErr := config.Load(s.Output)
		if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": loadErr.Error()})
			return
		}
		if errors.Is(loadErr, os.ErrNotExist) {
			cfg = defaultConfig()
			cfg.ApplyDefaults()
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": cfg})
	}))
	mux.HandleFunc("POST /api/save", s.authorize(token, func(w http.ResponseWriter, r *http.Request) {
		cfg, decodeErr := decodeConfig(w, r)
		if decodeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": decodeErr.Error()})
			return
		}
		if err := config.Write(s.Output, cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "配置已安全写入；服务仍默认 dry-run"})
		if s.ExitAfterSave {
			select {
			case saved <- struct{}{}:
			default:
			}
		}
	}))
	mux.HandleFunc("POST /api/test", s.authorize(token, func(w http.ResponseWriter, r *http.Request) {
		cfg, decodeErr := decodeConfig(w, r)
		if decodeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": decodeErr.Error()})
			return
		}
		accessToken := strings.TrimSpace(os.Getenv(cfg.NewAPI.AccessTokenEnv))
		if accessToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "当前进程未设置令牌环境变量"})
			return
		}
		client, clientErr := newapi.NewClient(cfg.NewAPI.BaseURL, cfg.NewAPI.UserID, accessToken, time.Duration(cfg.NewAPI.TimeoutSeconds)*time.Second)
		if clientErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": clientErr.Error()})
			return
		}
		ids := monitoredIDs(cfg)
		snapshot, snapshotErr := client.Snapshot(r.Context(), ids)
		if snapshotErr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "message": snapshotErr.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": fmt.Sprintf("连接成功，读取到 %d 个渠道快照", len(snapshot.Channels))})
	}))

	listener, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	actual := listener.Addr().String()
	if strings.HasPrefix(actual, "[::1]") {
		actual = "127.0.0.1" + strings.TrimPrefix(actual, "[::1]")
	}
	_, _ = fmt.Fprintf(s.Out, "setup_url=http://%s/?token=%s\n", actual, token)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-saved:
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s Server) authorize(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Setup-Token") != token {
			http.Error(w, "invalid setup token", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

func decodeConfig(w http.ResponseWriter, r *http.Request) (config.Config, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var cfg config.Config
	if err := decoder.Decode(&cfg); err != nil {
		return config.Config{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return config.Config{}, errors.New("request body must contain exactly one JSON object")
		}
		return config.Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func setupToken() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func monitoredIDs(cfg config.Config) []int {
	seen := map[int]struct{}{}
	for _, policy := range cfg.Channels {
		if policy.QuotaMode != config.QuotaIgnore {
			seen[policy.ChannelID] = struct{}{}
		}
	}
	result := make([]int, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	return result
}

func defaultConfig() config.Config {
	return config.Config{
		Version: 1,
		DryRun:  true,
		NewAPI:  config.NewAPIConfig{UserID: 1},
		Channels: []config.ChannelPolicy{{
			ChannelID: 1, Group: "default", Model: "example-model", RequiredTag: "quota-agent",
			RoutePool: "example-route-pool", QuotaPool: "example-quota-pool", QuotaMode: config.QuotaEstimate,
			Action: config.ActionObserve, HardErrorThreshold: 3, MinIndependentRoutesAfter: 1,
			Reset: config.ResetPolicy{Mode: "after_days", AfterDays: 1},
		}},
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
