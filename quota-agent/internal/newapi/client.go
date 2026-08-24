package newapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

type Client struct {
	baseURL string
	userID  int
	token   string
	http    *http.Client
}

type Snapshot struct {
	GeneratedAt int64               `json:"generated_at"`
	Channels    []ChannelSnapshot   `json:"channels"`
	Telemetry   []TelemetrySnapshot `json:"telemetry"`
}

type ChannelSnapshot struct {
	ChannelID int     `json:"channel_id"`
	Status    int     `json:"status"`
	UsedQuota int64   `json:"used_quota"`
	Tag       *string `json:"tag"`
}

type TelemetrySnapshot struct {
	ChannelID       int    `json:"channel_id"`
	Model           string `json:"model"`
	Successes       uint64 `json:"successes"`
	HardQuotaErrors uint64 `json:"hard_quota_errors"`
	RateLimitErrors uint64 `json:"rate_limit_errors"`
	OtherErrors     uint64 `json:"other_errors"`
	ConsecutiveHard uint64 `json:"consecutive_hard"`
	LastEventUnix   int64  `json:"last_event_unix"`
	LastKind        string `json:"last_kind"`
}

type routeSnapshot struct {
	ChannelID int    `json:"channel_id"`
	Group     string `json:"group"`
	Model     string `json:"model"`
	Enabled   bool   `json:"enabled"`
}

type probeResponse struct {
	Success bool `json:"success"`
}

type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func NewClient(baseURL string, userID int, token string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid NewAPI base URL")
	}
	if parsed.Scheme == "http" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1" {
		return nil, errors.New("plain HTTP is allowed only on loopback")
	}
	if userID <= 0 || strings.TrimSpace(token) == "" {
		return nil, errors.New("NewAPI user id and access token are required")
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, errors.New("invalid NewAPI timeout")
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		userID:  userID,
		token:   token,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) Snapshot(ctx context.Context, channelIDs []int) (Snapshot, error) {
	if len(channelIDs) == 0 || len(channelIDs) > 500 {
		return Snapshot{}, errors.New("snapshot requires 1 to 500 channel ids")
	}
	ids := append([]int(nil), channelIDs...)
	sort.Ints(ids)
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return Snapshot{}, errors.New("snapshot channel ids must be positive")
		}
		values = append(values, strconv.Itoa(id))
	}
	var snapshot Snapshot
	if err := c.requestData(ctx, http.MethodGet, "/api/channel/quota-snapshot?ids="+url.QueryEscape(strings.Join(values, ",")), nil, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (c *Client) RouteEnabled(ctx context.Context, channelID int, group, model string) (bool, error) {
	var routes []routeSnapshot
	if err := c.requestData(ctx, http.MethodGet, "/api/channel/routes/"+strconv.Itoa(channelID), nil, &routes); err != nil {
		return false, err
	}
	for _, route := range routes {
		if route.ChannelID == channelID && route.Group == group && route.Model == model {
			return route.Enabled, nil
		}
	}
	return false, errors.New("managed route not found")
}

func (c *Client) SetRouteEnabled(ctx context.Context, channelID int, group, model string, enabled, expected bool) error {
	body := map[string]any{
		"channel_id":       channelID,
		"group":            group,
		"model":            model,
		"enabled":          enabled,
		"expected_enabled": expected,
	}
	var response routeSnapshot
	return c.requestData(ctx, http.MethodPut, "/api/channel/route", body, &response)
}

func (c *Client) Probe(ctx context.Context, channelID int, model string) (bool, error) {
	path := "/api/channel/test/" + strconv.Itoa(channelID) + "?model=" + url.QueryEscape(model)
	var response probeResponse
	if err := c.requestRaw(ctx, http.MethodGet, path, nil, &response); err != nil {
		return false, err
	}
	return response.Success, nil
}

func (c *Client) requestData(ctx context.Context, method, path string, body any, target any) error {
	var response envelope
	if err := c.requestRaw(ctx, method, path, body, &response); err != nil {
		return err
	}
	if !response.Success {
		return errors.New("NewAPI operation reported failure")
	}
	if target == nil || len(response.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Data, target); err != nil {
		return fmt.Errorf("decode NewAPI data: %w", err)
	}
	return nil
}

func (c *Client) requestRaw(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", c.token)
	request.Header.Set("New-Api-User", strconv.Itoa(c.userID))
	request.Header.Set("User-Agent", "gensoukyou-quota-agent/1")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("NewAPI request failed: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return errors.New("NewAPI response exceeded size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("NewAPI returned HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode NewAPI response: %w", err)
	}
	return nil
}
