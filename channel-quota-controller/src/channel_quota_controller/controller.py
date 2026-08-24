from __future__ import annotations

from datetime import UTC, datetime, timedelta
from zoneinfo import ZoneInfo

from .adapter import EventSource, GatewayAdapter
from .config import AppConfig, RoutePolicy
from .models import Decision, Phase, RouteRuntimeState, UsageEvent
from .storage import AuditLogger, StateStore


def next_daily_reset(now: datetime, reset_time: str, timezone: str) -> datetime:
    if now.tzinfo is None:
        raise ValueError("now must include a timezone")
    hour_text, minute_text = reset_time.split(":", 1)
    local_now = now.astimezone(ZoneInfo(timezone))
    candidate = local_now.replace(
        hour=int(hour_text), minute=int(minute_text), second=0, microsecond=0
    )
    if candidate <= local_now:
        candidate += timedelta(days=1)
    return candidate.astimezone(UTC)


class QuotaController:
    def __init__(
        self,
        config: AppConfig,
        adapter: GatewayAdapter,
        events: EventSource,
        state_store: StateStore,
        audit: AuditLogger,
        *,
        effective_dry_run: bool | None = None,
    ):
        self.config = config
        self.adapter = adapter
        self.events = events
        self.state_store = state_store
        self.audit = audit
        self.dry_run = config.dry_run if effective_dry_run is None else effective_dry_run

    def run_once(self, now: datetime | None = None) -> list[Decision]:
        current_time = now or datetime.now(UTC)
        if current_time.tzinfo is None:
            raise ValueError("now must include a timezone")
        current_time = current_time.astimezone(UTC)
        state = self.state_store.load()
        decisions: list[Decision] = []
        state_changed = False
        for policy in self.config.routes:
            route_state = state.setdefault(policy.route_key, RouteRuntimeState())
            decision, changed = self._evaluate(policy, route_state, current_time)
            decisions.append(decision)
            self.audit.write(decision)
            state_changed = state_changed or changed
        if state_changed and not self.dry_run:
            self.state_store.save(state)
        return decisions

    def _decision(
        self,
        now: datetime,
        policy: RoutePolicy,
        action: str,
        reason: str,
        **details,
    ) -> Decision:
        return Decision(
            timestamp=now.isoformat(),
            route_key=policy.route_key,
            channel_id=policy.channel_id,
            model=policy.model,
            action=action,
            reason=reason,
            details=details,
        )

    def _evaluate(
        self, policy: RoutePolicy, state: RouteRuntimeState, now: datetime
    ) -> tuple[Decision, bool]:
        channel = self.adapter.snapshot(policy.channel_id)
        if channel is None:
            return self._decision(now, policy, "skip", "channel_not_found"), False
        if not channel.has_tag(policy.required_tag):
            return self._decision(
                now,
                policy,
                "skip",
                "required_tag_missing",
                required_tag=policy.required_tag,
            ), False

        route_enabled = channel.route_enabled(
            policy.model, policy.action_scope, policy.group
        )
        if state.owned_by_controller and route_enabled:
            if self.dry_run:
                return self._decision(
                    now, policy, "observe", "manual_override_enabled_detected"
                ), False
            self._clear_state(state)
            return self._decision(
                now, policy, "release", "manual_override_enabled_detected"
            ), True

        if not route_enabled and not state.owned_by_controller:
            return self._decision(
                now, policy, "skip", "manual_or_external_disable_preserved"
            ), False

        if state.phase == Phase.QUOTA_DISABLED and state.owned_by_controller:
            return self._handle_disabled_route(policy, state, channel, now)

        since = now - timedelta(seconds=policy.window_seconds)
        events = self.events.recent(policy.channel_id, policy.model, since, now)
        consecutive = self._consecutive_quota_errors(events, policy)
        if consecutive < policy.consecutive_errors:
            return self._decision(
                now,
                policy,
                "observe",
                "healthy_or_insufficient_quota_evidence",
                events_in_window=len(events),
                consecutive_quota_errors=consecutive,
                threshold=policy.consecutive_errors,
            ), False

        active_backups = self._active_pool_backups(policy)
        if len(active_backups) < policy.min_active_routes_after_disable:
            return self._decision(
                now,
                policy,
                "hold",
                "pool_capacity_guard",
                pool_id=policy.pool_id,
                active_backups=len(active_backups),
                required_backups=policy.min_active_routes_after_disable,
            ), False

        reset_at = next_daily_reset(now, policy.reset_time, policy.timezone) + timedelta(
            seconds=policy.reset_grace_seconds
        )
        mutation_available = self._mutation_capability_available(policy)
        if self.dry_run:
            return self._decision(
                now,
                policy,
                "would_disable",
                "quota_threshold_reached",
                consecutive_quota_errors=consecutive,
                reenable_after=reset_at.isoformat(),
                scope=policy.action_scope,
                pool_id=policy.pool_id,
                active_backups=len(active_backups),
                mutation_capability_available=mutation_available,
            ), False

        if not mutation_available:
            return self._decision(
                now,
                policy,
                "hold",
                "adapter_missing_required_mutation",
                scope=policy.action_scope,
            ), False

        fresh = self.adapter.snapshot(policy.channel_id)
        if (
            fresh is None
            or not fresh.has_tag(policy.required_tag)
            or not fresh.route_enabled(policy.model, policy.action_scope, policy.group)
        ):
            return self._decision(
                now, policy, "skip", "pre_change_state_mismatch"
            ), False
        self.adapter.disable_route(
            policy.channel_id, policy.group, policy.model, policy.action_scope
        )
        verified = self.adapter.snapshot(policy.channel_id)
        if verified is None or verified.route_enabled(
            policy.model, policy.action_scope, policy.group
        ):
            raise RuntimeError(f"failed to verify route disable: {policy.route_key}")
        state.phase = Phase.QUOTA_DISABLED
        state.owned_by_controller = True
        state.disabled_at = now.isoformat()
        state.reenable_after = reset_at.isoformat()
        state.last_probe_at = None
        state.probe_successes = 0
        state.reason = "quota_threshold_reached"
        return self._decision(
            now,
            policy,
            "disable",
            "quota_threshold_reached",
            consecutive_quota_errors=consecutive,
            reenable_after=reset_at.isoformat(),
            scope=policy.action_scope,
            pool_id=policy.pool_id,
            active_backups=len(active_backups),
        ), True

    def _handle_disabled_route(
        self, policy: RoutePolicy, state: RouteRuntimeState, channel, now: datetime
    ) -> tuple[Decision, bool]:
        if not state.reenable_after:
            return self._decision(
                now, policy, "skip", "owned_state_missing_reenable_time"
            ), False
        reenable_after = datetime.fromisoformat(state.reenable_after)
        if reenable_after.tzinfo is None:
            raise ValueError("reenable_after must include a timezone")
        if now < reenable_after:
            return self._decision(
                now,
                policy,
                "observe",
                "quota_cooldown_active",
                reenable_after=reenable_after.isoformat(),
            ), False
        if self.dry_run:
            return self._decision(
                now, policy, "would_probe", "reset_time_reached"
            ), False

        if not self.adapter.capabilities().specific_route_probe:
            return self._decision(
                now, policy, "hold", "adapter_missing_specific_route_probe"
            ), False

        if state.last_probe_at:
            last_probe = datetime.fromisoformat(state.last_probe_at)
            next_probe = last_probe + timedelta(seconds=policy.probe_interval_seconds)
            if now < next_probe:
                return self._decision(
                    now,
                    policy,
                    "observe",
                    "probe_interval_active",
                    next_probe=next_probe.isoformat(),
                ), False

        probe_ok = self.adapter.probe_route(
            policy.channel_id, policy.group, policy.model
        )
        state.last_probe_at = now.isoformat()
        if not probe_ok:
            state.probe_successes = 0
            return self._decision(
                now, policy, "probe_failed", "upstream_probe_failed"
            ), True

        state.probe_successes += 1
        if state.probe_successes < policy.probe_successes_required:
            return self._decision(
                now,
                policy,
                "probe_succeeded",
                "additional_probe_required",
                successes=state.probe_successes,
                required=policy.probe_successes_required,
            ), True

        fresh = self.adapter.snapshot(policy.channel_id)
        if fresh is None or not fresh.has_tag(policy.required_tag):
            return self._decision(
                now, policy, "skip", "pre_enable_tag_or_channel_mismatch"
            ), True
        if fresh.route_enabled(policy.model, policy.action_scope, policy.group):
            self._clear_state(state)
            return self._decision(
                now, policy, "release", "route_already_enabled_by_operator"
            ), True
        if not self._mutation_capability_available(policy):
            return self._decision(
                now,
                policy,
                "hold",
                "adapter_missing_required_mutation",
                scope=policy.action_scope,
            ), True
        self.adapter.enable_route(
            policy.channel_id, policy.group, policy.model, policy.action_scope
        )
        verified = self.adapter.snapshot(policy.channel_id)
        if verified is None or not verified.route_enabled(
            policy.model, policy.action_scope, policy.group
        ):
            raise RuntimeError(f"failed to verify route enable: {policy.route_key}")
        self._clear_state(state)
        return self._decision(
            now, policy, "enable", "quota_reset_probe_succeeded"
        ), True

    def _active_pool_backups(self, policy: RoutePolicy) -> list[str]:
        backups: list[str] = []
        for candidate in self.config.routes:
            if candidate.pool_id != policy.pool_id or candidate.route_key == policy.route_key:
                continue
            channel = self.adapter.snapshot(candidate.channel_id)
            if channel is None or not channel.has_tag(candidate.required_tag):
                continue
            if channel.route_enabled(
                candidate.model, candidate.action_scope, candidate.group
            ):
                backups.append(candidate.route_key)
        return backups

    def _mutation_capability_available(self, policy: RoutePolicy) -> bool:
        capabilities = self.adapter.capabilities()
        if policy.action_scope == "channel_model":
            return capabilities.channel_model_mutation
        return capabilities.channel_status_mutation

    @staticmethod
    def _consecutive_quota_errors(
        events: list[UsageEvent], policy: RoutePolicy
    ) -> int:
        count = 0
        for event in reversed(events):
            message = event.message.lower()
            matches = (
                event.status_code in policy.quota_status_codes
                and any(pattern in message for pattern in policy.quota_message_patterns)
            )
            if not matches:
                break
            count += 1
        return count

    @staticmethod
    def _clear_state(state: RouteRuntimeState) -> None:
        state.phase = Phase.NORMAL
        state.owned_by_controller = False
        state.disabled_at = None
        state.reenable_after = None
        state.last_probe_at = None
        state.probe_successes = 0
        state.reason = None
