from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Protocol

from .adapter import EventSource, GatewayAdapter
from .models import AdapterCapabilities, ChannelSnapshot, UsageEvent


class UnsupportedIntegrationCapability(RuntimeError):
    """Raised instead of silently falling back to a broader mutation."""


@dataclass(frozen=True)
class NewApiIntegrationContract:
    """Capabilities verified against the station's exact New API build.

    Keep every flag false until the corresponding endpoint, schema, cache update,
    and authorization behavior have been confirmed from source and a test instance.
    """

    deployment_version: str = "unverified"
    channel_status_mutation: bool = False
    channel_model_mutation: bool = False
    specific_route_probe: bool = False
    filtered_event_query: bool = False

    @classmethod
    def source_reviewed_v813(cls) -> "NewApiIntegrationContract":
        return cls(
            deployment_version="custom-v8.13-source-archive-20260824",
            channel_status_mutation=True,
            channel_model_mutation=False,
            specific_route_probe=True,
            filtered_event_query=False,
        )

    @classmethod
    def gensoukyou_route_api_source_reviewed(cls) -> "NewApiIntegrationContract":
        return cls(
            deployment_version="gensoukyou-custom-v8.13-route-api-20260824",
            channel_status_mutation=True,
            channel_model_mutation=True,
            specific_route_probe=True,
            filtered_event_query=False,
        )

    def capabilities(self) -> AdapterCapabilities:
        return AdapterCapabilities(
            channel_status_mutation=self.channel_status_mutation,
            channel_model_mutation=self.channel_model_mutation,
            specific_route_probe=self.specific_route_probe,
            filtered_event_query=self.filtered_event_query,
        )


class NewApiBackend(Protocol):
    """Version-specific boundary to implement after source inspection.

    This interface deliberately uses domain objects. HTTP paths, response field
    mapping, authentication, pagination, and cache synchronization stay inside the
    concrete backend for the station's deployed New API version.
    """

    def fetch_channel(self, channel_id: int) -> ChannelSnapshot | None: ...

    def set_channel_enabled(self, channel_id: int, enabled: bool) -> None: ...

    def set_channel_model_enabled(
        self, channel_id: int, group: str, model: str, enabled: bool
    ) -> None: ...

    def probe_channel_model(self, channel_id: int, model: str) -> bool: ...

    def query_usage_events(
        self, channel_id: int, model: str, since: datetime, until: datetime
    ) -> list[UsageEvent]: ...


class NewApiGatewayAdapter(GatewayAdapter):
    def __init__(
        self, backend: NewApiBackend, contract: NewApiIntegrationContract
    ) -> None:
        self.backend = backend
        self.contract = contract

    def capabilities(self) -> AdapterCapabilities:
        return self.contract.capabilities()

    def snapshot(self, channel_id: int) -> ChannelSnapshot | None:
        return self.backend.fetch_channel(channel_id)

    def disable_route(
        self, channel_id: int, group: str, model: str, action_scope: str
    ) -> None:
        self._set_route_enabled(channel_id, group, model, action_scope, False)

    def enable_route(
        self, channel_id: int, group: str, model: str, action_scope: str
    ) -> None:
        self._set_route_enabled(channel_id, group, model, action_scope, True)

    def _set_route_enabled(
        self,
        channel_id: int,
        group: str,
        model: str,
        action_scope: str,
        enabled: bool,
    ) -> None:
        capabilities = self.capabilities()
        if action_scope == "channel_model":
            if not capabilities.channel_model_mutation:
                raise UnsupportedIntegrationCapability(
                    "the verified New API contract does not support channel-model mutation"
                )
            self.backend.set_channel_model_enabled(channel_id, group, model, enabled)
            return
        if action_scope == "channel":
            if not capabilities.channel_status_mutation:
                raise UnsupportedIntegrationCapability(
                    "the verified New API contract does not support channel status mutation"
                )
            self.backend.set_channel_enabled(channel_id, enabled)
            return
        raise ValueError(f"unsupported action scope: {action_scope}")

    def probe_route(self, channel_id: int, group: str, model: str) -> bool:
        if not self.capabilities().specific_route_probe:
            raise UnsupportedIntegrationCapability(
                "the verified New API contract does not support a specific route probe"
            )
        return self.backend.probe_channel_model(channel_id, model)


class NewApiEventSource(EventSource):
    def __init__(
        self, backend: NewApiBackend, contract: NewApiIntegrationContract
    ) -> None:
        self.backend = backend
        self.contract = contract

    def recent(
        self, channel_id: int, model: str, since: datetime, until: datetime
    ) -> list[UsageEvent]:
        if not self.contract.filtered_event_query:
            raise UnsupportedIntegrationCapability(
                "the verified New API contract does not support filtered event queries"
            )
        events = self.backend.query_usage_events(channel_id, model, since, until)
        for event in events:
            if (
                event.channel_id != channel_id
                or event.model != model
                or event.timestamp < since
                or event.timestamp > until
            ):
                raise RuntimeError(
                    "New API backend returned an event outside the requested route/time window"
                )
        return sorted(events, key=lambda event: event.timestamp)
