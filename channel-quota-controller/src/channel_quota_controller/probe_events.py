from __future__ import annotations

import json
from datetime import datetime, timedelta
from pathlib import Path
from typing import Protocol

from .adapter import EventSource
from .models import UsageEvent
from .newapi_v813 import ProbeResult
from .storage import atomic_write_json


class StructuredProbeBackend(Protocol):
    def probe_channel_model_result(
        self, channel_id: int, model: str
    ) -> ProbeResult: ...


class ProbeHistoryEventSource(EventSource):
    """Low-frequency active probes backed by sanitized local history.

    Raw upstream error text is never persisted. Only explicitly configured
    marker strings that were present in the response are retained.
    """

    def __init__(
        self,
        backend: StructuredProbeBackend,
        history_path: str | Path,
        *,
        min_probe_interval_seconds: int = 300,
        retained_markers: tuple[str, ...] = (
            "quota",
            "daily limit",
            "rate limit",
            "额度",
            "限额",
        ),
        max_events_per_route: int = 200,
    ) -> None:
        if min_probe_interval_seconds < 10:
            raise ValueError("min_probe_interval_seconds must be at least 10")
        if max_events_per_route < 10:
            raise ValueError("max_events_per_route must be at least 10")
        normalized = tuple(
            marker.strip().lower() for marker in retained_markers if marker.strip()
        )
        if not normalized:
            raise ValueError("at least one retained marker is required")
        self.backend = backend
        self.history_path = Path(history_path)
        self.min_probe_interval = timedelta(seconds=min_probe_interval_seconds)
        self.retained_markers = normalized
        self.max_events_per_route = max_events_per_route

    def recent(
        self, channel_id: int, model: str, since: datetime, until: datetime
    ) -> list[UsageEvent]:
        if since.tzinfo is None or until.tzinfo is None:
            raise ValueError("probe query times must include a timezone")
        route_key = f"{channel_id}::{model}"
        history = self._load()
        rows = history.setdefault(route_key, [])
        parsed = [UsageEvent.from_dict(row) for row in rows]
        latest = parsed[-1].timestamp if parsed else None
        should_probe = latest is None or until - latest >= self.min_probe_interval
        if should_probe:
            result = self.backend.probe_channel_model_result(channel_id, model)
            event = self._to_event(result, channel_id, model, until)
            parsed.append(event)
            history[route_key] = [
                self._event_to_dict(item)
                for item in parsed[-self.max_events_per_route :]
            ]
            atomic_write_json(self.history_path, history)
        return [item for item in parsed if since <= item.timestamp <= until]

    def _to_event(
        self,
        result: ProbeResult,
        channel_id: int,
        model: str,
        timestamp: datetime,
    ) -> UsageEvent:
        if result.success:
            status_code = result.status_code or 200
            safe_message = ""
        else:
            status_code = result.status_code
            lower_message = result.message.lower()
            matched = [
                marker for marker in self.retained_markers if marker in lower_message
            ]
            safe_message = " ".join(matched) or result.error_code[:100]
        return UsageEvent(
            timestamp=timestamp,
            channel_id=channel_id,
            model=model,
            status_code=status_code,
            message=safe_message,
        )

    def _load(self) -> dict[str, list[dict]]:
        if not self.history_path.exists():
            return {}
        with self.history_path.open("r", encoding="utf-8") as handle:
            value = json.load(handle)
        if not isinstance(value, dict):
            raise ValueError("probe history must be a JSON object")
        return value

    @staticmethod
    def _event_to_dict(event: UsageEvent) -> dict:
        return {
            "timestamp": event.timestamp.isoformat(),
            "channel_id": event.channel_id,
            "model": event.model,
            "status_code": event.status_code,
            "message": event.message,
        }
