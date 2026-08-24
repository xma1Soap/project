from __future__ import annotations

import unittest
from datetime import UTC, datetime

from channel_quota_controller.models import ChannelSnapshot, UsageEvent
from channel_quota_controller.newapi_adapter import (
    NewApiEventSource,
    NewApiGatewayAdapter,
    NewApiIntegrationContract,
    UnsupportedIntegrationCapability,
)


class FakeBackend:
    def __init__(self) -> None:
        self.channel_enabled_calls: list[tuple[int, bool]] = []
        self.model_enabled_calls: list[tuple[int, str, str, bool]] = []
        self.events: list[UsageEvent] = []

    def fetch_channel(self, channel_id: int) -> ChannelSnapshot | None:
        return ChannelSnapshot(
            channel_id=channel_id,
            name="fake",
            enabled=True,
            tags=("auto-quota",),
            models=("v4p",),
        )

    def set_channel_enabled(self, channel_id: int, enabled: bool) -> None:
        self.channel_enabled_calls.append((channel_id, enabled))

    def set_channel_model_enabled(
        self, channel_id: int, group: str, model: str, enabled: bool
    ) -> None:
        self.model_enabled_calls.append((channel_id, group, model, enabled))

    def probe_channel_model(self, channel_id: int, model: str) -> bool:
        return True

    def query_usage_events(
        self, channel_id: int, model: str, since: datetime, until: datetime
    ) -> list[UsageEvent]:
        return list(self.events)


class NewApiAdapterTestCase(unittest.TestCase):
    def test_model_mutation_never_falls_back_to_whole_channel(self) -> None:
        backend = FakeBackend()
        adapter = NewApiGatewayAdapter(
            backend,
            NewApiIntegrationContract(channel_model_mutation=False),
        )
        with self.assertRaises(UnsupportedIntegrationCapability):
            adapter.disable_route(68, "default", "v4p", "channel_model")
        self.assertEqual(backend.channel_enabled_calls, [])
        self.assertEqual(backend.model_enabled_calls, [])

    def test_verified_model_mutation_is_exact(self) -> None:
        backend = FakeBackend()
        adapter = NewApiGatewayAdapter(
            backend,
            NewApiIntegrationContract(channel_model_mutation=True),
        )
        adapter.disable_route(68, "default", "v4p", "channel_model")
        self.assertEqual(
            backend.model_enabled_calls, [(68, "default", "v4p", False)]
        )
        self.assertEqual(backend.channel_enabled_calls, [])

    def test_event_source_rejects_cross_route_leakage(self) -> None:
        backend = FakeBackend()
        now = datetime(2026, 8, 24, 0, 0, tzinfo=UTC)
        backend.events = [
            UsageEvent(now, channel_id=999, model="v4p", status_code=429)
        ]
        source = NewApiEventSource(
            backend,
            NewApiIntegrationContract(filtered_event_query=True),
        )
        with self.assertRaises(RuntimeError):
            source.recent(68, "v4p", now, now)


if __name__ == "__main__":
    unittest.main()
