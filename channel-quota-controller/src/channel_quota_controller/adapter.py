from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path
from typing import Protocol

from .models import AdapterCapabilities, ChannelSnapshot, UsageEvent
from .storage import atomic_write_json


class GatewayAdapter(Protocol):
    def capabilities(self) -> AdapterCapabilities: ...

    def snapshot(self, channel_id: int) -> ChannelSnapshot | None: ...

    def disable_route(
        self, channel_id: int, group: str, model: str, action_scope: str
    ) -> None: ...

    def enable_route(
        self, channel_id: int, group: str, model: str, action_scope: str
    ) -> None: ...

    def probe_route(self, channel_id: int, group: str, model: str) -> bool: ...


class EventSource(Protocol):
    def recent(
        self, channel_id: int, model: str, since: datetime, until: datetime
    ) -> list[UsageEvent]: ...


class JsonFileGatewayAdapter:
    """Local simulation adapter.

    This adapter never talks to a production gateway. Live actions only update the
    supplied JSON fixture, making it suitable for tests and dry-run rehearsals.
    """

    def __init__(self, path: str | Path):
        self.path = Path(path)

    def capabilities(self) -> AdapterCapabilities:
        return AdapterCapabilities(
            channel_status_mutation=True,
            channel_model_mutation=True,
            specific_route_probe=True,
            filtered_event_query=True,
        )

    def _load(self) -> list[dict]:
        with self.path.open("r", encoding="utf-8") as handle:
            return json.load(handle)

    def _find(self, values: list[dict], channel_id: int) -> dict | None:
        return next((item for item in values if int(item["id"]) == channel_id), None)

    def snapshot(self, channel_id: int) -> ChannelSnapshot | None:
        item = self._find(self._load(), channel_id)
        if item is None:
            return None
        raw_tags = item.get("tags", [])
        if isinstance(raw_tags, str):
            raw_tags = [raw_tags]
        return ChannelSnapshot(
            channel_id=int(item["id"]),
            name=str(item.get("name", "")),
            enabled=bool(item.get("enabled", False)),
            tags=tuple(str(tag) for tag in raw_tags),
            models=tuple(str(model) for model in item.get("models", [])),
            disabled_models=tuple(str(model) for model in item.get("disabled_models", [])),
            disabled_routes=tuple(str(route) for route in item.get("disabled_routes", [])),
        )

    def disable_route(
        self, channel_id: int, group: str, model: str, action_scope: str
    ) -> None:
        values = self._load()
        item = self._find(values, channel_id)
        if item is None:
            raise KeyError(f"channel {channel_id} not found")
        if action_scope == "channel":
            item["enabled"] = False
        else:
            disabled_routes = set(
                str(value) for value in item.get("disabled_routes", [])
            )
            disabled_routes.add(f"{group}::{model}")
            item["disabled_routes"] = sorted(disabled_routes)
            disabled = set(str(value) for value in item.get("disabled_models", []))
            if group == "default":
                disabled.add(model)
            item["disabled_models"] = sorted(disabled)
        atomic_write_json(self.path, values)

    def enable_route(
        self, channel_id: int, group: str, model: str, action_scope: str
    ) -> None:
        values = self._load()
        item = self._find(values, channel_id)
        if item is None:
            raise KeyError(f"channel {channel_id} not found")
        if action_scope == "channel":
            item["enabled"] = True
        else:
            disabled_routes = set(
                str(value) for value in item.get("disabled_routes", [])
            )
            disabled_routes.discard(f"{group}::{model}")
            item["disabled_routes"] = sorted(disabled_routes)
            disabled = set(str(value) for value in item.get("disabled_models", []))
            if group == "default":
                disabled.discard(model)
            item["disabled_models"] = sorted(disabled)
        atomic_write_json(self.path, values)

    def probe_route(self, channel_id: int, group: str, model: str) -> bool:
        item = self._find(self._load(), channel_id)
        if item is None:
            return False
        route_results = item.get("probe_results", {})
        if model in route_results:
            return bool(route_results[model])
        return bool(item.get("probe_ok", False))


class JsonLinesEventSource:
    def __init__(self, path: str | Path):
        self.path = Path(path)

    def recent(
        self, channel_id: int, model: str, since: datetime, until: datetime
    ) -> list[UsageEvent]:
        if not self.path.exists():
            return []
        result: list[UsageEvent] = []
        with self.path.open("r", encoding="utf-8") as handle:
            for line_number, line in enumerate(handle, 1):
                if not line.strip():
                    continue
                try:
                    event = UsageEvent.from_dict(json.loads(line))
                except (KeyError, TypeError, ValueError, json.JSONDecodeError) as exc:
                    raise ValueError(f"invalid event at line {line_number}") from exc
                if (
                    event.channel_id == channel_id
                    and event.model == model
                    and since <= event.timestamp <= until
                ):
                    result.append(event)
        return sorted(result, key=lambda event: event.timestamp)
