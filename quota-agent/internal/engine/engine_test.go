package engine

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/xma1Soap/project/quota-agent/internal/config"
	"github.com/xma1Soap/project/quota-agent/internal/newapi"
	"github.com/xma1Soap/project/quota-agent/internal/state"
)

type fakeGateway struct {
	snapshot  newapi.Snapshot
	routes    map[string]bool
	probes    map[string]bool
	mutations []string
}

func (f *fakeGateway) Snapshot(context.Context, []int) (newapi.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeGateway) RouteEnabled(_ context.Context, channelID int, group, model string) (bool, error) {
	value, ok := f.routes[fmt.Sprintf("%d::%s::%s", channelID, group, model)]
	if !ok {
		return false, fmt.Errorf("route not found")
	}
	return value, nil
}

func (f *fakeGateway) SetRouteEnabled(_ context.Context, channelID int, group, model string, enabled, expected bool) error {
	key := fmt.Sprintf("%d::%s::%s", channelID, group, model)
	if f.routes[key] != expected {
		return fmt.Errorf("CAS conflict")
	}
	f.routes[key] = enabled
	f.mutations = append(f.mutations, fmt.Sprintf("%s=%t", key, enabled))
	return nil
}

func (f *fakeGateway) Probe(_ context.Context, channelID int, model string) (bool, error) {
	return f.probes[fmt.Sprintf("%d::%s", channelID, model)], nil
}

func TestHardQuotaDisablesOnlyOwnedRouteWithIndependentBackup(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	managedTag := "managed"
	gateway := &fakeGateway{
		snapshot: newapi.Snapshot{
			Channels: []newapi.ChannelSnapshot{
				{ChannelID: 1, Status: 1, UsedQuota: 150, Tag: &managedTag},
				{ChannelID: 2, Status: 1, UsedQuota: 20, Tag: &managedTag},
			},
			Telemetry: []newapi.TelemetrySnapshot{{ChannelID: 1, Model: "model-a", ConsecutiveHard: 3}},
		},
		routes: map[string]bool{
			"1::default::model-a": true,
			"2::default::model-a": true,
		},
		probes: map[string]bool{},
	}
	runtime := state.New()
	runtime.Pools["quota-a"] = &state.PoolState{
		Phase: "normal", CycleStartedAt: now.Add(-time.Hour).Format(time.RFC3339),
		CycleConfirmed: true, BaselineUsedQuota: map[int]int64{1: 50},
	}
	persistCalls := 0
	engine := Engine{Config: cfg, Gateway: gateway, State: runtime, Live: true, Persist: func(state.State) error { persistCalls++; return nil }}
	events, err := engine.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || !slices.Equal(gateway.mutations, []string{"1::default::model-a=false"}) {
		t.Fatalf("unexpected events or mutations: %+v %+v", events, gateway.mutations)
	}
	if !engine.State.Routes["1::default::model-a"].OwnedByAgent || engine.State.Pools["quota-a"].EstimatedCapacity != 100 || engine.State.Pools["quota-a"].Confidence != "low" || persistCalls < 3 {
		t.Fatalf("unexpected resulting state: %+v", engine.State)
	}
}

func TestDryRunEstimatesWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	managedTag := "managed"
	gateway := &fakeGateway{
		snapshot: newapi.Snapshot{
			Channels: []newapi.ChannelSnapshot{
				{ChannelID: 1, Status: 1, UsedQuota: 10, Tag: &managedTag},
				{ChannelID: 2, Status: 1, UsedQuota: 20, Tag: &managedTag},
			},
			Telemetry: []newapi.TelemetrySnapshot{{ChannelID: 1, Model: "model-a", ConsecutiveHard: 3}},
		},
		routes: map[string]bool{"1::default::model-a": true, "2::default::model-a": true},
		probes: map[string]bool{"1::model-a": true},
	}
	engine := Engine{Config: cfg, Gateway: gateway, State: state.New(), Live: false, Persist: func(state.State) error { return nil }}
	events, err := engine.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.mutations) != 0 || !slices.Contains(eventActions(events), "would_disable") {
		t.Fatalf("dry-run mutated or omitted decision: %+v %+v", gateway.mutations, events)
	}
	if engine.State.Pools["quota-a"].EstimatedCapacity != 0 || engine.State.Pools["quota-a"].Confidence != "none" {
		t.Fatalf("an initial mid-cycle sample must not become an estimate: %+v", engine.State.Pools["quota-a"])
	}
}

func TestScheduledRecoveryProbesAndEnablesOwnedRoute(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	managedTag := "managed"
	gateway := &fakeGateway{
		snapshot: newapi.Snapshot{Channels: []newapi.ChannelSnapshot{
			{ChannelID: 1, Status: 1, UsedQuota: 160, Tag: &managedTag},
			{ChannelID: 2, Status: 1, UsedQuota: 30, Tag: &managedTag},
		}},
		routes: map[string]bool{"1::default::model-a": false, "2::default::model-a": true},
		probes: map[string]bool{"1::model-a": true},
	}
	runtime := state.New()
	runtime.Pools["quota-a"] = &state.PoolState{
		Phase: "exhausted", CycleStartedAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
		BaselineUsedQuota: map[int]int64{1: 50}, ReenableAt: now.Add(-time.Minute).Format(time.RFC3339),
	}
	runtime.Routes["1::default::model-a"] = &state.RouteState{OwnedByAgent: true, DisabledAt: now.Add(-24 * time.Hour).Format(time.RFC3339)}
	engine := Engine{Config: cfg, Gateway: gateway, State: runtime, Live: true, Persist: func(state.State) error { return nil }}
	events, err := engine.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(eventActions(events), "enable") || !slices.Equal(gateway.mutations, []string{"1::default::model-a=true"}) || engine.State.Routes["1::default::model-a"].OwnedByAgent || engine.State.Pools["quota-a"].Phase != "normal" {
		t.Fatalf("unexpected recovery: %+v %+v %+v", events, gateway.mutations, engine.State)
	}
}

func TestScheduledRecoveryRequiresProbeForUnownedPool(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	cfg.Channels = cfg.Channels[:1]
	managedTag := "managed"
	gateway := &fakeGateway{
		snapshot: newapi.Snapshot{Channels: []newapi.ChannelSnapshot{
			{ChannelID: 1, Status: 1, UsedQuota: 160, Tag: &managedTag},
		}},
		routes: map[string]bool{"1::default::model-a": true},
		probes: map[string]bool{"1::model-a": false},
	}
	runtime := state.New()
	runtime.Pools["quota-a"] = &state.PoolState{
		Phase: "exhausted", CycleStartedAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
		BaselineUsedQuota: map[int]int64{1: 50}, ReenableAt: now.Add(-time.Minute).Format(time.RFC3339),
	}
	engine := Engine{Config: cfg, Gateway: gateway, State: runtime, Live: true, Persist: func(state.State) error { return nil }}
	events, err := engine.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(eventActions(events), "probe_failed") || engine.State.Pools["quota-a"].Phase != "exhausted" || engine.State.Pools["quota-a"].CycleConfirmed {
		t.Fatalf("an unowned pool must remain exhausted until its recovery probe succeeds: %+v %+v", events, engine.State.Pools["quota-a"])
	}
}

func TestExhaustedPoolRetriesRouteDisableBeforeReset(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	managedTag := "managed"
	gateway := &fakeGateway{
		snapshot: newapi.Snapshot{Channels: []newapi.ChannelSnapshot{
			{ChannelID: 1, Status: 1, UsedQuota: 150, Tag: &managedTag},
			{ChannelID: 2, Status: 1, UsedQuota: 20, Tag: &managedTag},
		}},
		routes: map[string]bool{
			"1::default::model-a": true,
			"2::default::model-a": true,
		},
		probes: map[string]bool{},
	}
	runtime := state.New()
	runtime.Pools["quota-a"] = &state.PoolState{
		Phase: "exhausted", CycleStartedAt: now.Add(-time.Hour).Format(time.RFC3339),
		CycleConfirmed: true, BaselineUsedQuota: map[int]int64{1: 50},
		ReenableAt: now.Add(time.Hour).Format(time.RFC3339),
	}
	engine := Engine{Config: cfg, Gateway: gateway, State: runtime, Live: true, Persist: func(state.State) error { return nil }}
	events, err := engine.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(eventActions(events), "disable") || !slices.Equal(gateway.mutations, []string{"1::default::model-a=false"}) {
		t.Fatalf("an exhausted pool must retry an unfinished route disable: %+v %+v", events, gateway.mutations)
	}
}

func TestOperatorEnabledRouteStillRequiresRecoveryProbe(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	cfg.Channels = cfg.Channels[:1]
	managedTag := "managed"
	gateway := &fakeGateway{
		snapshot: newapi.Snapshot{Channels: []newapi.ChannelSnapshot{
			{ChannelID: 1, Status: 1, UsedQuota: 160, Tag: &managedTag},
		}},
		routes: map[string]bool{"1::default::model-a": true},
		probes: map[string]bool{"1::model-a": false},
	}
	runtime := state.New()
	runtime.Pools["quota-a"] = &state.PoolState{
		Phase: "exhausted", CycleStartedAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
		CycleConfirmed: true, BaselineUsedQuota: map[int]int64{1: 50},
		ReenableAt: now.Add(-time.Minute).Format(time.RFC3339),
	}
	runtime.Routes["1::default::model-a"] = &state.RouteState{OwnedByAgent: true, DisabledAt: now.Add(-24 * time.Hour).Format(time.RFC3339)}
	engine := Engine{Config: cfg, Gateway: gateway, State: runtime, Live: true, Persist: func(state.State) error { return nil }}
	events, err := engine.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(eventActions(events), "release") || !slices.Contains(eventActions(events), "probe_failed") || engine.State.Pools["quota-a"].Phase != "exhausted" {
		t.Fatalf("operator enablement must release ownership without claiming quota recovery: %+v %+v", events, engine.State.Pools["quota-a"])
	}
}

func testConfig() config.Config {
	reset := config.ResetPolicy{Mode: "after_days", AfterDays: 1, ProbeSuccesses: 1, ProbeIntervalSeconds: 30, Timezone: "Asia/Shanghai", Time: "00:00"}
	return config.Config{
		DryRun: true,
		Channels: []config.ChannelPolicy{
			{ChannelID: 1, Group: "default", Model: "model-a", RequiredTag: "managed", RoutePool: "chat", QuotaPool: "quota-a", QuotaMode: config.QuotaEstimate, Action: config.ActionRoute, HardErrorThreshold: 3, MinIndependentRoutesAfter: 1, Reset: reset},
			{ChannelID: 2, Group: "default", Model: "model-a", RequiredTag: "managed", RoutePool: "chat", QuotaPool: "quota-b", QuotaMode: config.QuotaKnown, KnownCapacity: 1000, Action: config.ActionObserve, HardErrorThreshold: 3, Reset: reset},
		},
	}
}

func eventActions(events []Event) []string {
	result := make([]string, 0, len(events))
	for _, item := range events {
		result = append(result, item.Action)
	}
	return result
}
