from __future__ import annotations

from dataclasses import asdict, dataclass, field
from datetime import datetime
from enum import StrEnum
from typing import Any


class Phase(StrEnum):
    NORMAL = "normal"
    QUOTA_DISABLED = "quota_disabled"


@dataclass(frozen=True)
class AdapterCapabilities:
    channel_status_mutation: bool
    channel_model_mutation: bool
    specific_route_probe: bool
    filtered_event_query: bool


@dataclass(frozen=True)
class UsageEvent:
    timestamp: datetime
    channel_id: int
    model: str
    status_code: int
    message: str = ""

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "UsageEvent":
        raw_timestamp = str(value["timestamp"])
        if raw_timestamp.endswith("Z"):
            raw_timestamp = raw_timestamp[:-1] + "+00:00"
        timestamp = datetime.fromisoformat(raw_timestamp)
        if timestamp.tzinfo is None:
            raise ValueError("usage event timestamp must include a timezone")
        return cls(
            timestamp=timestamp,
            channel_id=int(value["channel_id"]),
            model=str(value["model"]),
            status_code=int(value["status_code"]),
            message=str(value.get("message", "")),
        )


@dataclass(frozen=True)
class ChannelSnapshot:
    channel_id: int
    name: str
    enabled: bool
    tags: tuple[str, ...]
    models: tuple[str, ...]
    disabled_models: tuple[str, ...] = ()
    disabled_routes: tuple[str, ...] = ()

    def has_tag(self, required_tag: str) -> bool:
        return required_tag in self.tags

    def route_enabled(
        self, model: str, action_scope: str, group: str = "default"
    ) -> bool:
        if not self.enabled:
            return False
        if action_scope == "channel":
            return True
        route_key = f"{group}::{model}"
        return (
            model in self.models
            and model not in self.disabled_models
            and route_key not in self.disabled_routes
        )


@dataclass
class RouteRuntimeState:
    phase: Phase = Phase.NORMAL
    owned_by_controller: bool = False
    disabled_at: str | None = None
    reenable_after: str | None = None
    last_probe_at: str | None = None
    probe_successes: int = 0
    reason: str | None = None

    @classmethod
    def from_dict(cls, value: dict[str, Any] | None) -> "RouteRuntimeState":
        if not value:
            return cls()
        return cls(
            phase=Phase(value.get("phase", Phase.NORMAL)),
            owned_by_controller=bool(value.get("owned_by_controller", False)),
            disabled_at=value.get("disabled_at"),
            reenable_after=value.get("reenable_after"),
            last_probe_at=value.get("last_probe_at"),
            probe_successes=int(value.get("probe_successes", 0)),
            reason=value.get("reason"),
        )

    def to_dict(self) -> dict[str, Any]:
        result = asdict(self)
        result["phase"] = self.phase.value
        return result


@dataclass(frozen=True)
class Decision:
    timestamp: str
    route_key: str
    channel_id: int
    model: str
    action: str
    reason: str
    details: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)
