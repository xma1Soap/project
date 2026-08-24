from __future__ import annotations

import json
import tempfile
import unittest
from datetime import UTC, datetime, timedelta
from pathlib import Path

from channel_quota_controller.adapter import JsonFileGatewayAdapter
from channel_quota_controller.config import AppConfig
from channel_quota_controller.controller import QuotaController
from channel_quota_controller.newapi_v813 import ProbeResult
from channel_quota_controller.probe_events import ProbeHistoryEventSource
from channel_quota_controller.storage import AuditLogger, StateStore


class FakeStructuredProbeBackend:
    def __init__(self, results: list[ProbeResult]) -> None:
        self.results = list(results)
        self.calls = 0

    def probe_channel_model_result(self, channel_id: int, model: str) -> ProbeResult:
        self.calls += 1
        return self.results.pop(0)


class ProbeHistoryEventSourceTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.path = Path(self.temp.name) / "probe-history.json"
        self.now = datetime(2026, 8, 24, 0, 0, tzinfo=UTC)

    def tearDown(self) -> None:
        self.temp.cleanup()

    def test_probe_interval_and_sanitized_history(self) -> None:
        backend = FakeStructuredProbeBackend(
            [
                ProbeResult(
                    success=False,
                    status_code=429,
                    error_code="bad_response_status_code",
                    message="daily limit: quota exceeded; secret-key=do-not-store",
                ),
                ProbeResult(
                    success=True,
                    status_code=200,
                    error_code="",
                    message="",
                ),
            ]
        )
        source = ProbeHistoryEventSource(
            backend, self.path, min_probe_interval_seconds=60
        )
        first = source.recent(
            68, "v4p", self.now - timedelta(minutes=5), self.now
        )
        second = source.recent(
            68,
            "v4p",
            self.now - timedelta(minutes=5),
            self.now + timedelta(seconds=30),
        )
        third = source.recent(
            68,
            "v4p",
            self.now - timedelta(minutes=5),
            self.now + timedelta(seconds=60),
        )

        self.assertEqual(backend.calls, 2)
        self.assertEqual(len(first), 1)
        self.assertEqual(len(second), 1)
        self.assertEqual(len(third), 2)
        raw = self.path.read_text(encoding="utf-8")
        self.assertNotIn("secret-key", raw)
        stored = json.loads(raw)["68::v4p"]
        self.assertEqual(stored[0]["message"], "quota daily limit")
        self.assertEqual(stored[1]["status_code"], 200)

    def test_three_structured_quota_probes_drive_dry_run_decision(self) -> None:
        backend = FakeStructuredProbeBackend(
            [
                ProbeResult(False, 429, "bad_response_status_code", "daily quota")
                for _ in range(3)
            ]
        )
        events = ProbeHistoryEventSource(
            backend, self.path, min_probe_interval_seconds=60
        )
        channels_path = Path(self.temp.name) / "channels.json"
        channels_path.write_text(
            json.dumps(
                [
                    {
                        "id": 68,
                        "name": "managed",
                        "enabled": True,
                        "tags": ["auto-quota"],
                        "models": ["v4p"],
                    }
                ]
            ),
            encoding="utf-8",
        )
        config = AppConfig.from_dict(
            {
                "dry_run": True,
                "routes": [
                    {
                        "channel_id": 68,
                        "model": "v4p",
                        "reset_time": "00:00",
                        "timezone": "Asia/Shanghai",
                        "quota_status_codes": [429],
                        "quota_message_patterns": ["quota"],
                        "consecutive_errors": 3,
                        "window_seconds": 300,
                        "min_active_routes_after_disable": 0,
                    }
                ],
            }
        )
        controller = QuotaController(
            config,
            JsonFileGatewayAdapter(channels_path),
            events,
            StateStore(Path(self.temp.name) / "state.json"),
            AuditLogger(Path(self.temp.name) / "audit.jsonl"),
        )

        first = controller.run_once(self.now)[0]
        second = controller.run_once(self.now + timedelta(seconds=60))[0]
        third = controller.run_once(self.now + timedelta(seconds=120))[0]

        self.assertEqual(first.action, "observe")
        self.assertEqual(second.action, "observe")
        self.assertEqual(third.action, "would_disable")


if __name__ == "__main__":
    unittest.main()
