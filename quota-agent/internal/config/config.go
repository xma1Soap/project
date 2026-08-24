package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const CurrentVersion = 1
const AccessTokenEnvName = "GENSOUKYOU_NEW_API_ACCESS_TOKEN"

const (
	QuotaEstimate = "estimate"
	QuotaKnown    = "known"
	QuotaIgnore   = "ignore"

	ActionObserve = "observe"
	ActionRoute   = "route"
)

type Config struct {
	Version             int             `json:"version"`
	DryRun              bool            `json:"dry_run"`
	PollIntervalSeconds int             `json:"poll_interval_seconds"`
	StatePath           string          `json:"state_path"`
	LockPath            string          `json:"lock_path"`
	NewAPI              NewAPIConfig    `json:"new_api"`
	Channels            []ChannelPolicy `json:"channels"`
}

type NewAPIConfig struct {
	BaseURL        string `json:"base_url"`
	UserID         int    `json:"user_id"`
	AccessTokenEnv string `json:"access_token_env"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type ChannelPolicy struct {
	ChannelID                 int         `json:"channel_id"`
	Group                     string      `json:"group"`
	Model                     string      `json:"model"`
	RequiredTag               string      `json:"required_tag"`
	RoutePool                 string      `json:"route_pool"`
	QuotaPool                 string      `json:"quota_pool"`
	QuotaMode                 string      `json:"quota_mode"`
	KnownCapacity             int64       `json:"known_capacity,omitempty"`
	Action                    string      `json:"action"`
	HardErrorThreshold        uint64      `json:"hard_error_threshold"`
	MinIndependentRoutesAfter int         `json:"min_independent_routes_after_disable"`
	Reset                     ResetPolicy `json:"reset"`
}

type ResetPolicy struct {
	Mode                 string `json:"mode"`
	AfterDays            int    `json:"after_days,omitempty"`
	FixedAt              string `json:"fixed_at,omitempty"`
	Time                 string `json:"time,omitempty"`
	Timezone             string `json:"timezone,omitempty"`
	Month                int    `json:"month,omitempty"`
	Day                  int    `json:"day,omitempty"`
	ProbeSuccesses       int    `json:"probe_successes"`
	ProbeIntervalSeconds int    `json:"probe_interval_seconds"`
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) ApplyDefaults() {
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.PollIntervalSeconds == 0 {
		c.PollIntervalSeconds = 60
	}
	if c.StatePath == "" {
		c.StatePath = "/var/lib/gensoukyou-quota-agent/state.json"
	}
	if c.LockPath == "" {
		c.LockPath = "/run/gensoukyou-quota-agent/agent.lock"
	}
	if c.NewAPI.BaseURL == "" {
		c.NewAPI.BaseURL = "http://127.0.0.1:3000"
	}
	if c.NewAPI.AccessTokenEnv == "" {
		c.NewAPI.AccessTokenEnv = AccessTokenEnvName
	}
	if c.NewAPI.TimeoutSeconds == 0 {
		c.NewAPI.TimeoutSeconds = 10
	}
	for i := range c.Channels {
		policy := &c.Channels[i]
		policy.Group = strings.TrimSpace(policy.Group)
		policy.Model = strings.TrimSpace(policy.Model)
		policy.RequiredTag = strings.TrimSpace(policy.RequiredTag)
		policy.RoutePool = strings.TrimSpace(policy.RoutePool)
		policy.QuotaPool = strings.TrimSpace(policy.QuotaPool)
		if policy.Group == "" {
			policy.Group = "default"
		}
		if policy.QuotaMode == "" {
			policy.QuotaMode = QuotaEstimate
		}
		if policy.Action == "" {
			policy.Action = ActionObserve
		}
		if policy.HardErrorThreshold == 0 {
			policy.HardErrorThreshold = 3
		}
		if policy.Reset.Mode == "" {
			policy.Reset.Mode = "manual"
		}
		if policy.Reset.Timezone == "" {
			policy.Reset.Timezone = "Asia/Shanghai"
		}
		if policy.Reset.Time == "" {
			policy.Reset.Time = "00:00"
		}
		if policy.Reset.ProbeSuccesses == 0 {
			policy.Reset.ProbeSuccesses = 2
		}
		if policy.Reset.ProbeIntervalSeconds == 0 {
			policy.Reset.ProbeIntervalSeconds = 300
		}
	}
}

func (c *Config) Validate() error {
	c.ApplyDefaults()
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.PollIntervalSeconds < 10 || c.PollIntervalSeconds > 3600 {
		return errors.New("poll_interval_seconds must be between 10 and 3600")
	}
	if strings.TrimSpace(c.StatePath) == "" {
		return errors.New("state_path is required")
	}
	if strings.TrimSpace(c.LockPath) == "" {
		return errors.New("lock_path is required")
	}
	parsed, err := url.Parse(c.NewAPI.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return errors.New("new_api.base_url must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme == "http" && !isLoopback(parsed.Hostname()) {
		return errors.New("plain HTTP is allowed only for a loopback NewAPI URL")
	}
	if c.NewAPI.UserID <= 0 {
		return errors.New("new_api.user_id must be positive")
	}
	if c.NewAPI.AccessTokenEnv != AccessTokenEnvName {
		return fmt.Errorf("new_api.access_token_env must be %s for the packaged service", AccessTokenEnvName)
	}
	if c.NewAPI.TimeoutSeconds < 1 || c.NewAPI.TimeoutSeconds > 60 {
		return errors.New("new_api.timeout_seconds must be between 1 and 60")
	}
	if len(c.Channels) == 0 {
		return errors.New("at least one channel policy is required")
	}
	routes := make(map[string]struct{}, len(c.Channels))
	poolReset := make(map[string]ResetPolicy)
	poolMode := make(map[string]string)
	poolCapacity := make(map[string]int64)
	monitoredChannels := make(map[int]struct{})
	for i := range c.Channels {
		policy := &c.Channels[i]
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("channels[%d]: %w", i, err)
		}
		key := policy.RouteKey()
		if _, exists := routes[key]; exists {
			return fmt.Errorf("duplicate managed route %s", key)
		}
		routes[key] = struct{}{}
		if prior, exists := poolReset[policy.QuotaPool]; exists && prior.ScheduleIdentity() != policy.Reset.ScheduleIdentity() {
			return fmt.Errorf("quota_pool %q has inconsistent reset schedules", policy.QuotaPool)
		}
		poolReset[policy.QuotaPool] = policy.Reset
		if prior, exists := poolMode[policy.QuotaPool]; exists && prior != policy.QuotaMode {
			return fmt.Errorf("quota_pool %q has inconsistent quota modes", policy.QuotaPool)
		}
		poolMode[policy.QuotaPool] = policy.QuotaMode
		if prior, exists := poolCapacity[policy.QuotaPool]; exists && prior != policy.KnownCapacity {
			return fmt.Errorf("quota_pool %q has inconsistent known capacities", policy.QuotaPool)
		}
		poolCapacity[policy.QuotaPool] = policy.KnownCapacity
		if policy.QuotaMode != QuotaIgnore {
			monitoredChannels[policy.ChannelID] = struct{}{}
		}
	}
	if len(monitoredChannels) == 0 {
		return errors.New("at least one channel must use estimate or known quota mode")
	}
	if len(monitoredChannels) > 500 {
		return errors.New("at most 500 distinct channels can be monitored")
	}
	return nil
}

func Write(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (p ChannelPolicy) Validate() error {
	if p.ChannelID <= 0 {
		return errors.New("channel_id must be positive")
	}
	if p.Group == "" || p.Model == "" || p.RequiredTag == "" || p.RoutePool == "" || p.QuotaPool == "" {
		return errors.New("group, model, required_tag, route_pool and quota_pool are required")
	}
	if p.QuotaMode != QuotaEstimate && p.QuotaMode != QuotaKnown && p.QuotaMode != QuotaIgnore {
		return errors.New("quota_mode must be estimate, known or ignore")
	}
	if p.QuotaMode == QuotaKnown && p.KnownCapacity <= 0 {
		return errors.New("known quota mode requires known_capacity > 0")
	}
	if p.Action != ActionObserve && p.Action != ActionRoute {
		return errors.New("action must be observe or route")
	}
	if p.HardErrorThreshold < 2 || p.HardErrorThreshold > 20 {
		return errors.New("hard_error_threshold must be between 2 and 20")
	}
	if p.MinIndependentRoutesAfter < 0 {
		return errors.New("min_independent_routes_after_disable cannot be negative")
	}
	return p.Reset.Validate()
}

func (p ChannelPolicy) RouteKey() string {
	return strconv.Itoa(p.ChannelID) + "::" + p.Group + "::" + p.Model
}

func (r ResetPolicy) Validate() error {
	if r.ProbeSuccesses < 1 || r.ProbeSuccesses > 10 {
		return errors.New("reset.probe_successes must be between 1 and 10")
	}
	if r.ProbeIntervalSeconds < 30 || r.ProbeIntervalSeconds > 86400 {
		return errors.New("reset.probe_interval_seconds must be between 30 and 86400")
	}
	switch r.Mode {
	case "manual":
		return nil
	case "after_days":
		if r.AfterDays < 1 || r.AfterDays > 3660 {
			return errors.New("reset.after_days must be between 1 and 3660")
		}
	case "fixed_at":
		if _, err := time.Parse(time.RFC3339, r.FixedAt); err != nil {
			return errors.New("reset.fixed_at must be RFC3339 with timezone")
		}
	case "daily":
		if err := validateClock(r.Time); err != nil {
			return err
		}
	case "annual":
		if r.Month < 1 || r.Month > 12 || r.Day < 1 || r.Day > 31 {
			return errors.New("annual reset requires valid month and day")
		}
		candidate := time.Date(2000, time.Month(r.Month), r.Day, 0, 0, 0, 0, time.UTC)
		if int(candidate.Month()) != r.Month || candidate.Day() != r.Day {
			return errors.New("annual reset calendar date does not exist")
		}
		if err := validateClock(r.Time); err != nil {
			return err
		}
	default:
		return errors.New("reset.mode must be manual, after_days, fixed_at, daily or annual")
	}
	if r.Mode == "daily" || r.Mode == "annual" {
		if _, err := time.LoadLocation(r.Timezone); err != nil {
			return fmt.Errorf("invalid reset.timezone: %w", err)
		}
	}
	return nil
}

func (r ResetPolicy) ScheduleIdentity() string {
	return strings.Join([]string{r.Mode, strconv.Itoa(r.AfterDays), r.FixedAt, r.Time, r.Timezone, strconv.Itoa(r.Month), strconv.Itoa(r.Day)}, "|")
}

func validateClock(value string) error {
	if _, err := time.Parse("15:04", value); err != nil {
		return errors.New("reset.time must be HH:MM")
	}
	return nil
}

func isLoopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
