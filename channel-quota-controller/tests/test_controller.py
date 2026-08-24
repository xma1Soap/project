from __future__ import annotations

import json
import tempfile
import unittest
from datetime import UTC, datetime, timedelta
from pathlib import Path

from channel_quota_controller.adapter import JsonFileGatewayAdapter, JsonLinesEventSource
from channel_quota_controller.config import AppConfig
from channel_quota_controller.controller import QuotaController, next_daily_reset
from channel_quota_controller.storage import AuditLogger, StateStore


class ControllerTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.channels_path = self.root / "channels.json"
        self.events_path = self.root / "events.jsonl"
        self.state_path = self.root / "state.json"
        self.audit_path = self.root / "audit.jsonl"
        self.now = datetime(2026, 8, 24, 2, 0, tzinfo=UTC)
        self.write_channels()
        self.events_path.write_text("", encoding="utf-8")

    def tearDown(self) -> None:
        self.temp.cleanup()

    def write_channels(
        self,
        *,
        enabled: bool = True,
        tags: list[str] | None = None,
        disabled_models: list[str] | None = None,
        probe_ok: bool = True,
    ) -> None:
        channels = [
            {
                "id": 1001,
                "name": "test",
                "enabled": enabled,
                "tags": ["auto-quota"] if tags is None else tags,
                "models": ["v4p"],
                "disabled_models": disabled_models or [],
                "probe_ok": probe_ok,
            }
        ]
        self.channels_path.write_text(
            json.dumps(channels, ensure_ascii=False), encoding="utf-8"
        )

    def config(self, *, dry_run: bool) -> AppConfig:
        return AppConfig.from_dict(
            {
                "dry_run": dry_run,
                "poll_interval_seconds": 60,
                "routes": [
                    {
                        "channel_id": 1001,
                        "model": "v4p",
                        "required_tag": "auto-quota",
                        "reset_time": "00:00",
                        "timezone": "Asia/Shanghai",
                        "quota_status_codes": [402, 429],
                        "quota_message_patterns": ["quota", "额度"],
                        "consecutive_errors": 3,
                        "window_seconds": 300,
                        "reset_grace_seconds": 0,
                        "probe_interval_seconds": 10,
                        "probe_successes_required": 2,
                        "action_scope": "channel_model",
                        "pool_id": "test-v4p",
                        "min_active_routes_after_disable": 0,
                    }
                ],
            }
        )

    def controller(self, *, dry_run: bool) -> QuotaController:
        return QuotaController(
            self.config(dry_run=dry_run),
            JsonFileGatewayAdapter(self.channels_path),
            JsonLinesEventSource(self.events_path),
            StateStore(self.state_path),
            AuditLogger(self.audit_path),
        )

    def write_events(self, rows: list[dict]) -> None:
        self.events_path.write_text(
            "".join(json.dumps(row, ensure_ascii=False) + "\n" for row in rows),
            encoding="utf-8",
        )

    def quota_events(self) -> list[dict]:
        return [
            {
                "timestamp": (self.now - timedelta(seconds=offset)).isoformat(),
                "channel_id": 1001,
                "model": "v4p",
                "status_code": 429,
                "message": "daily quota exceeded",
            }
            for offset in (3, 2, 1)
        ]

    def test_next_daily_reset_uses_configured_timezone(self) -> None:
        reset = next_daily_reset(self.now, "12:00", "Asia/Shanghai")
        self.assertEqual(reset, datetime(2026, 8, 24, 4, 0, tzinfo=UTC))

    def test_dry_run_reports_without_mutating_route_or_state(self) -> None:
        self.write_events(self.quota_events())
        decision = self.controller(dry_run=True).run_once(self.now)[0]
        self.assertEqual(decision.action, "would_disable")
        self.assertTrue(JsonFileGatewayAdapter(self.channels_path).snapshot(1001).route_enabled("v4p", "channel_model"))
        self.assertFalse(self.state_path.exists())

    def test_generic_429_without_quota_message_does_not_trip(self) -> None:
        rows = self.quota_events()
        for row in rows:
            row["message"] = "temporary upstream overload"
        self.write_events(rows)
        decision = self.controller(dry_run=False).run_once(self.now)[0]
        self.assertEqual(decision.action, "observe")
        self.assertTrue(JsonFileGatewayAdapter(self.channels_path).snapshot(1001).route_enabled("v4p", "channel_model"))

    def test_live_mode_disables_only_managed_model_and_records_ownership(self) -> None:
        self.write_events(self.quota_events())
        decision = self.controller(dry_run=False).run_once(self.now)[0]
        self.assertEqual(decision.action, "disable")
        snapshot = JsonFileGatewayAdapter(self.channels_path).snapshot(1001)
        self.assertTrue(snapshot.enabled)
        self.assertFalse(snapshot.route_enabled("v4p", "channel_model"))
        state = StateStore(self.state_path).load()["1001::default::v4p"]
        self.assertTrue(state.owned_by_controller)

    def test_manual_disable_is_never_reenabled(self) -> None:
        self.write_channels(disabled_models=["v4p"])
        decision = self.controller(dry_run=False).run_once(self.now)[0]
        self.assertEqual(decision.reason, "manual_or_external_disable_preserved")
        self.assertFalse(JsonFileGatewayAdapter(self.channels_path).snapshot(1001).route_enabled("v4p", "channel_model"))

    def test_missing_management_tag_fails_closed(self) -> None:
        self.write_channels(tags=["quota-daily"])
        self.write_events(self.quota_events())
        decision = self.controller(dry_run=False).run_once(self.now)[0]
        self.assertEqual(decision.reason, "required_tag_missing")
        self.assertTrue(JsonFileGatewayAdapter(self.channels_path).snapshot(1001).route_enabled("v4p", "channel_model"))

    def test_pool_guard_preserves_last_managed_route(self) -> None:
        config = AppConfig.from_dict(
            {
                "dry_run": False,
                "routes": [
                    {
                        "channel_id": 1001,
                        "model": "v4p",
                        "required_tag": "auto-quota",
                        "reset_time": "00:00",
                        "timezone": "Asia/Shanghai",
                        "quota_message_patterns": ["quota"],
                        "consecutive_errors": 3,
                        "pool_id": "only-route",
                        "min_active_routes_after_disable": 1,
                    }
                ],
            }
        )
        self.write_events(self.quota_events())
        controller = QuotaController(
            config,
            JsonFileGatewayAdapter(self.channels_path),
            JsonLinesEventSource(self.events_path),
            StateStore(self.state_path),
            AuditLogger(self.audit_path),
        )
        decision = controller.run_once(self.now)[0]
        self.assertEqual(decision.action, "hold")
        self.assertEqual(decision.reason, "pool_capacity_guard")
        self.assertTrue(
            JsonFileGatewayAdapter(self.channels_path).snapshot(1001).route_enabled(
                "v4p", "channel_model"
            )
        )

    def test_owned_route_requires_two_successful_probes_before_enable(self) -> None:
        self.write_events(self.quota_events())
        controller = self.controller(dry_run=False)
        disabled = controller.run_once(self.now)[0]
        self.assertEqual(disabled.action, "disable")
        state = StateStore(self.state_path).load()["1001::default::v4p"]
        due = datetime.fromisoformat(state.reenable_after)
        first = controller.run_once(due)[0]
        self.assertEqual(first.action, "probe_succeeded")
        self.assertFalse(JsonFileGatewayAdapter(self.channels_path).snapshot(1001).route_enabled("v4p", "channel_model"))
        second = controller.run_once(due + timedelta(seconds=10))[0]
        self.assertEqual(second.action, "enable")
        self.assertTrue(JsonFileGatewayAdapter(self.channels_path).snapshot(1001).route_enabled("v4p", "channel_model"))
        final_state = StateStore(self.state_path).load()["1001::default::v4p"]
        self.assertFalse(final_state.owned_by_controller)


if __name__ == "__main__":
    unittest.main()
