from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo


@dataclass(frozen=True)
class RoutePolicy:
    channel_id: int
    model: str
    required_tag: str
    reset_time: str
    timezone: str
    quota_status_codes: frozenset[int]
    quota_message_patterns: tuple[str, ...]
    group: str = "default"
    consecutive_errors: int = 3
    window_seconds: int = 300
    reset_grace_seconds: int = 120
    probe_interval_seconds: int = 300
    probe_successes_required: int = 2
    action_scope: str = "channel_model"
    pool_id: str = ""
    min_active_routes_after_disable: int = 1

    @property
    def route_key(self) -> str:
        suffix = (
            "*"
            if self.action_scope == "channel"
            else f"{self.group}::{self.model}"
        )
        return f"{self.channel_id}::{suffix}"

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "RoutePolicy":
        model = str(value["model"])
        policy = cls(
            channel_id=int(value["channel_id"]),
            model=model,
            required_tag=str(value.get("required_tag", "auto-quota")),
            reset_time=str(value["reset_time"]),
            timezone=str(value.get("timezone", "Asia/Shanghai")),
            quota_status_codes=frozenset(
                int(code) for code in value.get("quota_status_codes", [402, 429])
            ),
            quota_message_patterns=tuple(
                str(pattern).lower()
                for pattern in value.get(
                    "quota_message_patterns",
                    ["quota", "rate limit", "额度", "限额"],
                )
            ),
            group=str(value.get("group", "default")).strip(),
            consecutive_errors=int(value.get("consecutive_errors", 3)),
            window_seconds=int(value.get("window_seconds", 300)),
            reset_grace_seconds=int(value.get("reset_grace_seconds", 120)),
            probe_interval_seconds=int(value.get("probe_interval_seconds", 300)),
            probe_successes_required=int(value.get("probe_successes_required", 2)),
            action_scope=str(value.get("action_scope", "channel_model")),
            pool_id=str(value.get("pool_id", f"model:{model}")),
            min_active_routes_after_disable=int(
                value.get("min_active_routes_after_disable", 1)
            ),
        )
        policy.validate()
        return policy

    def validate(self) -> None:
        if self.channel_id <= 0:
            raise ValueError("channel_id must be positive")
        if not self.model:
            raise ValueError("model cannot be empty")
        if not self.required_tag:
            raise ValueError("required_tag cannot be empty")
        if not self.group:
            raise ValueError("group cannot be empty")
        if not self.pool_id:
            raise ValueError("pool_id cannot be empty")
        if self.action_scope not in {"channel", "channel_model"}:
            raise ValueError("action_scope must be channel or channel_model")
        if self.action_scope == "channel" and self.model != "*":
            raise ValueError("channel scope requires model='*'")
        if self.action_scope == "channel_model" and self.model == "*":
            raise ValueError("channel_model scope requires a concrete model")
        hour_text, minute_text = self.reset_time.split(":", 1)
        hour, minute = int(hour_text), int(minute_text)
        if not (0 <= hour <= 23 and 0 <= minute <= 59):
            raise ValueError("reset_time must be HH:MM")
        ZoneInfo(self.timezone)
        if not self.quota_status_codes:
            raise ValueError("quota_status_codes cannot be empty")
        if not self.quota_message_patterns:
            raise ValueError("quota_message_patterns cannot be empty")
        if self.consecutive_errors < 2:
            raise ValueError("consecutive_errors must be at least 2")
        if self.window_seconds < 30:
            raise ValueError("window_seconds must be at least 30")
        if self.probe_successes_required < 1:
            raise ValueError("probe_successes_required must be positive")
        if self.min_active_routes_after_disable < 0:
            raise ValueError("min_active_routes_after_disable cannot be negative")


@dataclass(frozen=True)
class AppConfig:
    dry_run: bool
    poll_interval_seconds: int
    routes: tuple[RoutePolicy, ...]

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "AppConfig":
        routes = tuple(RoutePolicy.from_dict(item) for item in value.get("routes", []))
        if not routes:
            raise ValueError("at least one managed route is required")
        keys = [route.route_key for route in routes]
        if len(keys) != len(set(keys)):
            raise ValueError("managed route keys must be unique")
        interval = int(value.get("poll_interval_seconds", 60))
        if interval < 10:
            raise ValueError("poll_interval_seconds must be at least 10")
        return cls(
            dry_run=bool(value.get("dry_run", True)),
            poll_interval_seconds=interval,
            routes=routes,
        )


def load_config(path: str | Path) -> AppConfig:
    with Path(path).open("r", encoding="utf-8") as handle:
        return AppConfig.from_dict(json.load(handle))
