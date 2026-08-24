from __future__ import annotations

import unittest
from datetime import UTC, datetime
from typing import Any, Mapping

from channel_quota_controller.newapi_v813 import NewApiV813Backend


class FakeTransport:
    def __init__(self, responses: list[Mapping[str, Any]]) -> None:
        self.responses = list(responses)
        self.calls: list[tuple[str, str, Mapping[str, Any] | None, Mapping[str, Any] | None]] = []

    def request_json(self, method, path, *, query=None, body=None):
        self.calls.append((method, path, query, body))
        return self.responses.pop(0)


class NewApiV813BackendTestCase(unittest.TestCase):
    def test_channel_snapshot_maps_csv_models_and_single_tag(self) -> None:
        transport = FakeTransport(
            [
                {
                    "success": True,
                    "data": {
                        "id": 68,
                        "name": "daily-free",
                        "status": 1,
                        "tag": "auto-quota",
                        "models": "v4p, v4p-search",
                    },
                },
                {
                    "success": True,
                    "data": [
                        {
                            "channel_id": 68,
                            "group": "default",
                            "model": "v4p",
                            "enabled": False,
                        },
                        {
                            "channel_id": 68,
                            "group": "vip",
                            "model": "v4p-search",
                            "enabled": True,
                        },
                    ],
                },
            ]
        )
        snapshot = NewApiV813Backend(transport).fetch_channel(68)
        self.assertIsNotNone(snapshot)
        self.assertEqual(snapshot.models, ("v4p", "v4p-search"))
        self.assertEqual(snapshot.tags, ("auto-quota",))
        self.assertTrue(snapshot.enabled)
        self.assertEqual(snapshot.disabled_routes, ("default::v4p",))

    def test_disable_uses_auto_disabled_status_only(self) -> None:
        transport = FakeTransport([{"success": True, "data": {}}])
        NewApiV813Backend(transport).set_channel_enabled(68, False)
        self.assertEqual(
            transport.calls[0],
            ("PUT", "/api/channel/", None, {"id": 68, "status": 3}),
        )

    def test_specific_probe_passes_model(self) -> None:
        transport = FakeTransport(
            [
                {
                    "success": False,
                    "message": "daily quota exceeded",
                    "error_code": "bad_response_status_code",
                    "status_code": 429,
                }
            ]
        )
        result = NewApiV813Backend(transport).probe_channel_model_result(68, "v4p")
        self.assertFalse(result.success)
        self.assertEqual(result.status_code, 429)
        self.assertEqual(transport.calls[0][2], {"model": "v4p"})

    def test_model_mutation_uses_exact_group_and_expected_state(self) -> None:
        transport = FakeTransport([{"success": True, "data": {}}])
        NewApiV813Backend(transport).set_channel_model_enabled(
            68, "default", "v4p", False
        )
        self.assertEqual(
            transport.calls[0],
            (
                "PUT",
                "/api/channel/route",
                None,
                {
                    "channel_id": 68,
                    "group": "default",
                    "model": "v4p",
                    "enabled": False,
                    "expected_enabled": True,
                },
            ),
        )

    def test_error_log_mapping_uses_other_status_code(self) -> None:
        now = datetime(2026, 8, 24, 0, 0, tzinfo=UTC)
        transport = FakeTransport(
            [
                {
                    "success": True,
                    "data": {
                        "total": 1,
                        "items": [
                            {
                                "created_at": int(now.timestamp()),
                                "channel": 68,
                                "model_name": "v4p",
                                "content": "daily quota exceeded",
                                "other": '{"status_code":429}',
                            }
                        ],
                    },
                }
            ]
        )
        events = NewApiV813Backend(transport).query_usage_events(68, "v4p", now, now)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0].status_code, 429)


if __name__ == "__main__":
    unittest.main()
