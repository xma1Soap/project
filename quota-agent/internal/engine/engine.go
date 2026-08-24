package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xma1Soap/project/quota-agent/internal/config"
	"github.com/xma1Soap/project/quota-agent/internal/newapi"
	"github.com/xma1Soap/project/quota-agent/internal/schedule"
	"github.com/xma1Soap/project/quota-agent/internal/state"
)

const channelStatusEnabled = 1

type Gateway interface {
	Snapshot(context.Context, []int) (newapi.Snapshot, error)
	RouteEnabled(context.Context, int, string, string) (bool, error)
	SetRouteEnabled(context.Context, int, string, string, bool, bool) error
	Probe(context.Context, int, string) (bool, error)
}

type Persist func(state.State) error

type Engine struct {
	Config     config.Config
	Gateway    Gateway
	State      state.State
	Live       bool
	Persist    Persist
	routeCache map[string]bool
}

type Event struct {
	Time       string `json:"time"`
	Pool       string `json:"pool,omitempty"`
	Route      string `json:"route,omitempty"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	UsedQuota  int64  `json:"used_quota,omitempty"`
	Estimate   int64  `json:"estimate,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

func (e *Engine) RunOnce(ctx context.Context, now time.Time) ([]Event, error) {
	if e.Gateway == nil || e.Persist == nil {
		return nil, errors.New("engine gateway and persistence are required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	e.routeCache = make(map[string]bool)
	policies := e.managedPolicies()
	if len(policies) == 0 {
		return nil, errors.New("no monitored policies")
	}
	snapshot, err := e.Gateway.Snapshot(ctx, uniqueChannelIDs(policies))
	if err != nil {
		return nil, err
	}
	channels := make(map[int]newapi.ChannelSnapshot, len(snapshot.Channels))
	for _, channel := range snapshot.Channels {
		channels[channel.ChannelID] = channel
	}
	telemetry := make(map[string]newapi.TelemetrySnapshot, len(snapshot.Telemetry))
	for _, item := range snapshot.Telemetry {
		telemetry[telemetryKey(item.ChannelID, item.Model)] = item
	}
	if err := e.reconcilePending(ctx, policies); err != nil {
		return nil, err
	}
	groups := groupByQuotaPool(policies)
	poolNames := make([]string, 0, len(groups))
	for pool := range groups {
		poolNames = append(poolNames, pool)
	}
	sort.Strings(poolNames)
	events := make([]Event, 0, len(poolNames))
	for _, poolName := range poolNames {
		poolEvents, err := e.evaluatePool(ctx, now, poolName, groups[poolName], channels, telemetry)
		if err != nil {
			return events, fmt.Errorf("quota pool %s: %w", poolName, err)
		}
		events = append(events, poolEvents...)
	}
	if err := e.Persist(e.State); err != nil {
		return events, err
	}
	return events, nil
}

func (e *Engine) evaluatePool(ctx context.Context, now time.Time, poolName string, policies []config.ChannelPolicy, channels map[int]newapi.ChannelSnapshot, telemetry map[string]newapi.TelemetrySnapshot) ([]Event, error) {
	pool := e.State.Pools[poolName]
	if pool == nil {
		pool = &state.PoolState{Phase: "normal", BaselineUsedQuota: map[int]int64{}}
		e.State.Pools[poolName] = pool
	}
	if pool.BaselineUsedQuota == nil {
		pool.BaselineUsedQuota = map[int]int64{}
	}
	valid, reason := validatePoolInputs(policies, channels)
	if !valid {
		return []Event{event(now, poolName, "", "hold", reason)}, nil
	}
	if pool.CycleStartedAt == "" {
		startCycle(pool, policies, channels, now, false)
	}
	if pool.Phase == "exhausted" {
		return e.handleExhaustedPool(ctx, now, poolName, policies, channels, pool)
	}
	if !hardQuotaReached(policies, telemetry) {
		return []Event{event(now, poolName, "", "observe", "quota_not_exhausted")}, nil
	}
	used, complete := cycleUsage(pool, policies, channels)
	if policies[0].QuotaMode == config.QuotaEstimate {
		pool.Samples = append(pool.Samples, state.CapacitySample{
			StartedAt: pool.CycleStartedAt,
			EndedAt:   state.NowString(now),
			UsedQuota: used,
			Complete:  complete,
		})
		pool.Samples = state.TrimSamples(pool.Samples, 14)
		pool.EstimatedCapacity, pool.Confidence = state.Estimate(pool.Samples)
	} else if policies[0].QuotaMode == config.QuotaKnown {
		pool.EstimatedCapacity = policies[0].KnownCapacity
		pool.Confidence = "known"
	}
	pool.Phase = "exhausted"
	pool.ExhaustedAt = state.NowString(now)
	next, scheduled, err := schedule.Next(policies[0].Reset, now)
	if err != nil {
		return nil, err
	}
	if scheduled {
		pool.ReenableAt = state.NowString(next)
	} else {
		pool.ReenableAt = ""
	}
	if err := e.Persist(e.State); err != nil {
		return nil, err
	}
	events := []Event{{Time: state.NowString(now), Pool: poolName, Action: "exhausted", Reason: "hard_quota_threshold_reached", UsedQuota: used, Estimate: pool.EstimatedCapacity, Confidence: pool.Confidence}}
	routeEvents, err := e.enforceExhaustedRoutes(ctx, now, policies, channels)
	if err != nil {
		return events, err
	}
	events = append(events, routeEvents...)
	return events, nil
}

func (e *Engine) enforceExhaustedRoutes(ctx context.Context, now time.Time, policies []config.ChannelPolicy, channels map[int]newapi.ChannelSnapshot) ([]Event, error) {
	events := make([]Event, 0)
	for _, policy := range policies {
		if policy.Action != config.ActionRoute {
			continue
		}
		routeEvents, err := e.disableManagedRoute(ctx, now, policy, channels)
		if err != nil {
			return events, err
		}
		events = append(events, routeEvents...)
	}
	return events, nil
}

func (e *Engine) disableManagedRoute(ctx context.Context, now time.Time, policy config.ChannelPolicy, channels map[int]newapi.ChannelSnapshot) ([]Event, error) {
	key := policy.RouteKey()
	routeState := e.State.Routes[key]
	if routeState == nil {
		routeState = &state.RouteState{}
		e.State.Routes[key] = routeState
	}
	if routeState.OwnedByAgent {
		return []Event{event(now, policy.QuotaPool, key, "observe", "route_already_owned")}, nil
	}
	enabled, err := e.routeEnabled(ctx, policy)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return []Event{event(now, policy.QuotaPool, key, "preserve", "manual_or_external_disable")}, nil
	}
	backups, err := e.independentBackups(ctx, policy, channels)
	if err != nil {
		return nil, err
	}
	if backups < policy.MinIndependentRoutesAfter {
		return []Event{event(now, policy.QuotaPool, key, "hold", "independent_route_guard")}, nil
	}
	if !e.Live {
		return []Event{event(now, policy.QuotaPool, key, "would_disable", "dry_run")}, nil
	}
	routeState.PendingAction = "disable"
	if err := e.Persist(e.State); err != nil {
		return nil, err
	}
	if err := e.Gateway.SetRouteEnabled(ctx, policy.ChannelID, policy.Group, policy.Model, false, true); err != nil {
		routeState.PendingAction = ""
		_ = e.Persist(e.State)
		return nil, err
	}
	e.routeCache[key] = false
	routeState.PendingAction = ""
	routeState.OwnedByAgent = true
	routeState.DisabledAt = state.NowString(now)
	if err := e.Persist(e.State); err != nil {
		return nil, err
	}
	return []Event{event(now, policy.QuotaPool, key, "disable", "hard_quota_exhausted")}, nil
}

func (e *Engine) handleExhaustedPool(ctx context.Context, now time.Time, poolName string, policies []config.ChannelPolicy, channels map[int]newapi.ChannelSnapshot, pool *state.PoolState) ([]Event, error) {
	if pool.ReenableAt == "" {
		events, err := e.enforceExhaustedRoutes(ctx, now, policies, channels)
		if err != nil {
			return events, err
		}
		return append(events, event(now, poolName, "", "hold", "manual_reset_required")), nil
	}
	reenableAt, err := time.Parse(time.RFC3339, pool.ReenableAt)
	if err != nil {
		return nil, err
	}
	if now.Before(reenableAt) {
		events, err := e.enforceExhaustedRoutes(ctx, now, policies, channels)
		if err != nil {
			return events, err
		}
		return append(events, event(now, poolName, "", "observe", "reset_schedule_pending")), nil
	}
	confirmedByOwnedProbe := false
	events := make([]Event, 0)
	for _, policy := range policies {
		routeState := e.State.Routes[policy.RouteKey()]
		if routeState == nil || !routeState.OwnedByAgent {
			continue
		}
		if !e.Live {
			events = append(events, event(now, poolName, policy.RouteKey(), "would_probe", "reset_schedule_reached"))
			continue
		}
		enabled, err := e.routeEnabled(ctx, policy)
		if err != nil {
			return events, err
		}
		if enabled {
			routeState.OwnedByAgent = false
			routeState.DisabledAt = ""
			routeState.LastProbeAt = ""
			routeState.ProbeSuccesses = 0
			events = append(events, event(now, poolName, policy.RouteKey(), "release", "operator_enabled_route"))
			continue
		}
		if routeState.LastProbeAt != "" {
			lastProbe, parseErr := time.Parse(time.RFC3339, routeState.LastProbeAt)
			if parseErr != nil {
				return events, parseErr
			}
			if now.Before(lastProbe.Add(time.Duration(policy.Reset.ProbeIntervalSeconds) * time.Second)) {
				continue
			}
		}
		ok, err := e.Gateway.Probe(ctx, policy.ChannelID, policy.Model)
		if err != nil {
			return events, err
		}
		routeState.LastProbeAt = state.NowString(now)
		if !ok {
			routeState.ProbeSuccesses = 0
			events = append(events, event(now, poolName, policy.RouteKey(), "probe_failed", "upstream_not_recovered"))
			continue
		}
		routeState.ProbeSuccesses++
		if routeState.ProbeSuccesses < policy.Reset.ProbeSuccesses {
			events = append(events, event(now, poolName, policy.RouteKey(), "probe_succeeded", "additional_probe_required"))
			continue
		}
		routeState.PendingAction = "enable"
		if err := e.Persist(e.State); err != nil {
			return events, err
		}
		if err := e.Gateway.SetRouteEnabled(ctx, policy.ChannelID, policy.Group, policy.Model, true, false); err != nil {
			routeState.PendingAction = ""
			_ = e.Persist(e.State)
			return events, err
		}
		e.routeCache[policy.RouteKey()] = true
		routeState.PendingAction = ""
		routeState.OwnedByAgent = false
		routeState.DisabledAt = ""
		routeState.LastProbeAt = ""
		routeState.ProbeSuccesses = 0
		confirmedByOwnedProbe = true
		events = append(events, event(now, poolName, policy.RouteKey(), "enable", "reset_probe_succeeded"))
	}
	ownedRemaining := false
	for _, policy := range policies {
		if routeState := e.State.Routes[policy.RouteKey()]; routeState != nil && routeState.OwnedByAgent {
			ownedRemaining = true
			break
		}
	}
	if !ownedRemaining {
		if confirmedByOwnedProbe {
			startCycle(pool, policies, channels, now, true)
			events = append(events, event(now, poolName, "", "cycle_start", "quota_reset_confirmed"))
		} else {
			confirmed, probeEvents, err := e.confirmUnownedPoolReset(ctx, now, poolName, policies, pool)
			if err != nil {
				return events, err
			}
			events = append(events, probeEvents...)
			if confirmed {
				startCycle(pool, policies, channels, now, true)
				events = append(events, event(now, poolName, "", "cycle_start", "quota_reset_probe_succeeded"))
			}
		}
	}
	if err := e.Persist(e.State); err != nil {
		return events, err
	}
	if len(events) == 0 {
		events = append(events, event(now, poolName, "", "observe", "recovery_probe_pending"))
	}
	return events, nil
}

func (e *Engine) confirmUnownedPoolReset(ctx context.Context, now time.Time, poolName string, policies []config.ChannelPolicy, pool *state.PoolState) (bool, []Event, error) {
	policy := policies[0]
	if pool.LastPoolProbeAt != "" {
		lastProbe, err := time.Parse(time.RFC3339, pool.LastPoolProbeAt)
		if err != nil {
			return false, nil, err
		}
		if now.Before(lastProbe.Add(time.Duration(policy.Reset.ProbeIntervalSeconds) * time.Second)) {
			return false, []Event{event(now, poolName, "", "observe", "pool_probe_interval_active")}, nil
		}
	}
	ok, err := e.Gateway.Probe(ctx, policy.ChannelID, policy.Model)
	if err != nil {
		return false, nil, err
	}
	pool.LastPoolProbeAt = state.NowString(now)
	if !ok {
		pool.PoolProbeSuccesses = 0
		return false, []Event{event(now, poolName, policy.RouteKey(), "probe_failed", "upstream_not_recovered")}, nil
	}
	pool.PoolProbeSuccesses++
	if pool.PoolProbeSuccesses < policy.Reset.ProbeSuccesses {
		return false, []Event{event(now, poolName, policy.RouteKey(), "probe_succeeded", "additional_probe_required")}, nil
	}
	return true, nil, nil
}

func (e *Engine) independentBackups(ctx context.Context, target config.ChannelPolicy, channels map[int]newapi.ChannelSnapshot) (int, error) {
	independentPools := map[string]struct{}{}
	for _, candidate := range e.Config.Channels {
		if candidate.RoutePool != target.RoutePool || candidate.QuotaPool == target.QuotaPool || candidate.QuotaMode == config.QuotaIgnore {
			continue
		}
		channel, ok := channels[candidate.ChannelID]
		if !ok || channel.Status != channelStatusEnabled || strings.TrimSpace(pointerValue(channel.Tag)) != candidate.RequiredTag {
			continue
		}
		if candidateState := e.State.Pools[candidate.QuotaPool]; candidateState != nil && candidateState.Phase == "exhausted" {
			continue
		}
		enabled, err := e.routeEnabled(ctx, candidate)
		if err != nil {
			return 0, err
		}
		if enabled {
			independentPools[candidate.QuotaPool] = struct{}{}
		}
	}
	return len(independentPools), nil
}

func (e *Engine) reconcilePending(ctx context.Context, policies []config.ChannelPolicy) error {
	byKey := make(map[string]config.ChannelPolicy, len(policies))
	for _, policy := range policies {
		byKey[policy.RouteKey()] = policy
	}
	changed := false
	for key, routeState := range e.State.Routes {
		if routeState.PendingAction == "" {
			continue
		}
		policy, ok := byKey[key]
		if !ok {
			return fmt.Errorf("pending action references unmanaged route %s", key)
		}
		enabled, err := e.routeEnabled(ctx, policy)
		if err != nil {
			return err
		}
		if routeState.PendingAction == "disable" {
			routeState.OwnedByAgent = !enabled
		} else if routeState.PendingAction == "enable" && enabled {
			routeState.OwnedByAgent = false
			routeState.DisabledAt = ""
		}
		routeState.PendingAction = ""
		changed = true
	}
	if changed {
		return e.Persist(e.State)
	}
	return nil
}

func (e *Engine) routeEnabled(ctx context.Context, policy config.ChannelPolicy) (bool, error) {
	key := policy.RouteKey()
	if enabled, ok := e.routeCache[key]; ok {
		return enabled, nil
	}
	enabled, err := e.Gateway.RouteEnabled(ctx, policy.ChannelID, policy.Group, policy.Model)
	if err != nil {
		return false, err
	}
	e.routeCache[key] = enabled
	return enabled, nil
}

func (e *Engine) managedPolicies() []config.ChannelPolicy {
	result := make([]config.ChannelPolicy, 0, len(e.Config.Channels))
	for _, policy := range e.Config.Channels {
		if policy.QuotaMode != config.QuotaIgnore {
			result = append(result, policy)
		}
	}
	return result
}

func validatePoolInputs(policies []config.ChannelPolicy, channels map[int]newapi.ChannelSnapshot) (bool, string) {
	for _, policy := range policies {
		channel, ok := channels[policy.ChannelID]
		if !ok {
			return false, "channel_snapshot_missing"
		}
		if channel.Status != channelStatusEnabled {
			return false, "channel_not_enabled"
		}
		if strings.TrimSpace(pointerValue(channel.Tag)) != policy.RequiredTag {
			return false, "required_tag_missing"
		}
	}
	return true, ""
}

func hardQuotaReached(policies []config.ChannelPolicy, telemetry map[string]newapi.TelemetrySnapshot) bool {
	for _, policy := range policies {
		item, ok := telemetry[telemetryKey(policy.ChannelID, policy.Model)]
		if ok && item.ConsecutiveHard >= policy.HardErrorThreshold {
			return true
		}
	}
	return false
}

func startCycle(pool *state.PoolState, policies []config.ChannelPolicy, channels map[int]newapi.ChannelSnapshot, now time.Time, confirmed bool) {
	pool.Phase = "normal"
	pool.CycleStartedAt = state.NowString(now)
	pool.CycleConfirmed = confirmed
	pool.BaselineUsedQuota = map[int]int64{}
	for _, policy := range policies {
		pool.BaselineUsedQuota[policy.ChannelID] = channels[policy.ChannelID].UsedQuota
	}
	pool.ExhaustedAt = ""
	pool.ReenableAt = ""
	pool.LastPoolProbeAt = ""
	pool.PoolProbeSuccesses = 0
}

func cycleUsage(pool *state.PoolState, policies []config.ChannelPolicy, channels map[int]newapi.ChannelSnapshot) (int64, bool) {
	seen := map[int]struct{}{}
	var total int64
	complete := pool.CycleConfirmed
	for _, policy := range policies {
		if _, ok := seen[policy.ChannelID]; ok {
			continue
		}
		seen[policy.ChannelID] = struct{}{}
		baseline, ok := pool.BaselineUsedQuota[policy.ChannelID]
		if !ok || channels[policy.ChannelID].UsedQuota < baseline {
			complete = false
			continue
		}
		total += channels[policy.ChannelID].UsedQuota - baseline
	}
	return total, complete
}

func groupByQuotaPool(policies []config.ChannelPolicy) map[string][]config.ChannelPolicy {
	result := make(map[string][]config.ChannelPolicy)
	for _, policy := range policies {
		result[policy.QuotaPool] = append(result[policy.QuotaPool], policy)
	}
	return result
}

func uniqueChannelIDs(policies []config.ChannelPolicy) []int {
	seen := map[int]struct{}{}
	for _, policy := range policies {
		seen[policy.ChannelID] = struct{}{}
	}
	result := make([]int, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Ints(result)
	return result
}

func telemetryKey(channelID int, model string) string {
	return fmt.Sprintf("%d::%s", channelID, strings.TrimSpace(model))
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func event(now time.Time, pool, route, action, reason string) Event {
	return Event{Time: state.NowString(now), Pool: pool, Route: route, Action: action, Reason: reason}
}
