package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const CurrentVersion = 1

type State struct {
	Version int                    `json:"version"`
	Pools   map[string]*PoolState  `json:"pools"`
	Routes  map[string]*RouteState `json:"routes"`
}

type PoolState struct {
	Phase              string           `json:"phase"`
	CycleStartedAt     string           `json:"cycle_started_at,omitempty"`
	CycleConfirmed     bool             `json:"cycle_confirmed"`
	BaselineUsedQuota  map[int]int64    `json:"baseline_used_quota,omitempty"`
	ExhaustedAt        string           `json:"exhausted_at,omitempty"`
	ReenableAt         string           `json:"reenable_at,omitempty"`
	Samples            []CapacitySample `json:"samples,omitempty"`
	EstimatedCapacity  int64            `json:"estimated_capacity,omitempty"`
	Confidence         string           `json:"confidence,omitempty"`
	LastPoolProbeAt    string           `json:"last_pool_probe_at,omitempty"`
	PoolProbeSuccesses int              `json:"pool_probe_successes,omitempty"`
}

type RouteState struct {
	OwnedByAgent   bool   `json:"owned_by_agent"`
	PendingAction  string `json:"pending_action,omitempty"`
	DisabledAt     string `json:"disabled_at,omitempty"`
	LastProbeAt    string `json:"last_probe_at,omitempty"`
	ProbeSuccesses int    `json:"probe_successes,omitempty"`
}

type CapacitySample struct {
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	UsedQuota int64  `json:"used_quota"`
	Complete  bool   `json:"complete"`
}

func New() State {
	return State{Version: CurrentVersion, Pools: map[string]*PoolState{}, Routes: map[string]*RouteState{}}
}

func Load(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return State{}, err
	}
	var value State
	if err := json.Unmarshal(raw, &value); err != nil {
		return State{}, fmt.Errorf("decode state: %w", err)
	}
	if value.Version != CurrentVersion {
		return State{}, fmt.Errorf("unsupported state version %d", value.Version)
	}
	if value.Pools == nil {
		value.Pools = map[string]*PoolState{}
	}
	if value.Routes == nil {
		value.Routes = map[string]*RouteState{}
	}
	return value, nil
}

func Save(path string, value State) error {
	value.Version = CurrentVersion
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
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
	return os.Rename(temporaryName, path)
}

func Estimate(samples []CapacitySample) (int64, string) {
	values := make([]int64, 0, len(samples))
	for _, sample := range samples {
		if sample.Complete && sample.UsedQuota > 0 {
			values = append(values, sample.UsedQuota)
		}
	}
	if len(values) == 0 {
		return 0, "none"
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	median := values[len(values)/2]
	if len(values)%2 == 0 {
		median = (values[len(values)/2-1] + values[len(values)/2]) / 2
	}
	confidence := "low"
	if len(values) >= 3 {
		confidence = "medium"
	}
	if len(values) >= 5 && stable(values, median) {
		confidence = "high"
	}
	return median, confidence
}

func stable(values []int64, median int64) bool {
	if median <= 0 {
		return false
	}
	low, high := values[len(values)/4], values[(len(values)*3)/4]
	return float64(high-low)/float64(median) <= 0.25
}

func TrimSamples(samples []CapacitySample, limit int) []CapacitySample {
	if limit < 1 || len(samples) <= limit {
		return samples
	}
	copyOfTail := append([]CapacitySample(nil), samples[len(samples)-limit:]...)
	return copyOfTail
}

func NowString(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}
