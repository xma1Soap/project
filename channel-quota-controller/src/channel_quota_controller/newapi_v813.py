from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any, Mapping, Protocol
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener

from .models import ChannelSnapshot, UsageEvent


class NewApiHttpError(RuntimeError):
    pass


@dataclass(frozen=True)
class ProbeResult:
    success: bool
    status_code: int
    error_code: str
    message: str


class JsonTransport(Protocol):
    def request_json(
        self,
        method: str,
        path: str,
        *,
        query: Mapping[str, Any] | None = None,
        body: Mapping[str, Any] | None = None,
    ) -> Mapping[str, Any]: ...


class _RejectRedirects(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise NewApiHttpError(
            "redirect refused; configure the final HTTPS New API base URL"
        )


@dataclass(frozen=True)
class NewApiAuth:
    user_id: int
    access_token: str | None = None
    session_cookie: str | None = None

    def validate(self) -> None:
        if self.user_id <= 0:
            raise ValueError("New API user_id must be positive")
        if not self.access_token and not self.session_cookie:
            raise ValueError("an access token or session cookie is required")


class UrllibJsonTransport:
    def __init__(
        self,
        base_url: str,
        auth: NewApiAuth,
        *,
        timeout_seconds: float = 10.0,
        max_response_bytes: int = 2_000_000,
        allow_insecure_http: bool = False,
    ) -> None:
        auth.validate()
        parsed = urlsplit(base_url.rstrip("/"))
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise ValueError("base_url must be an absolute HTTP(S) URL")
        if parsed.scheme != "https" and not allow_insecure_http:
            raise ValueError("base_url must use HTTPS")
        self.base_url = base_url.rstrip("/")
        self.auth = auth
        self.timeout_seconds = timeout_seconds
        self.max_response_bytes = max_response_bytes
        self.opener = build_opener(_RejectRedirects())

    def request_json(
        self,
        method: str,
        path: str,
        *,
        query: Mapping[str, Any] | None = None,
        body: Mapping[str, Any] | None = None,
    ) -> Mapping[str, Any]:
        if not path.startswith("/"):
            raise ValueError("API path must start with /")
        url = self.base_url + path
        if query:
            encoded = urlencode(
                {key: value for key, value in query.items() if value is not None}
            )
            if encoded:
                url += "?" + encoded
        payload = None
        headers = {
            "Accept": "application/json",
            "New-Api-User": str(self.auth.user_id),
            "User-Agent": "channel-quota-controller/0.1",
        }
        if body is not None:
            payload = json.dumps(body, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if self.auth.access_token:
            headers["Authorization"] = self.auth.access_token
        if self.auth.session_cookie:
            headers["Cookie"] = self.auth.session_cookie
        request = Request(url, data=payload, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=self.timeout_seconds) as response:
                raw = response.read(self.max_response_bytes + 1)
        except (HTTPError, URLError, TimeoutError, NewApiHttpError) as exc:
            raise NewApiHttpError(f"New API request failed: {method} {path}") from exc
        if len(raw) > self.max_response_bytes:
            raise NewApiHttpError("New API response exceeded the size limit")
        try:
            value = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise NewApiHttpError("New API returned invalid JSON") from exc
        if not isinstance(value, dict):
            raise NewApiHttpError("New API returned a non-object JSON response")
        return value


class NewApiV813Backend:
    """Source-reviewed adapter for the supplied customized v8.13 backend.

    It implements the narrow NewApiBackend domain boundary. Capability gating is
    still owned by NewApiIntegrationContract; constructing this class alone does
    not authorize writes.
    """

    STATUS_ENABLED = 1
    STATUS_AUTO_DISABLED = 3
    ERROR_LOG_TYPE = 5

    def __init__(self, transport: JsonTransport, *, max_log_pages: int = 20) -> None:
        if max_log_pages < 1:
            raise ValueError("max_log_pages must be positive")
        self.transport = transport
        self.max_log_pages = max_log_pages

    @staticmethod
    def _data(response: Mapping[str, Any]) -> Any:
        if response.get("success") is not True:
            message = str(response.get("message", "New API operation failed"))
            raise NewApiHttpError(message[:300])
        return response.get("data")

    def fetch_channel(self, channel_id: int) -> ChannelSnapshot | None:
        response = self.transport.request_json("GET", f"/api/channel/{channel_id}")
        raw = self._data(response)
        if raw is None:
            return None
        if not isinstance(raw, Mapping):
            raise NewApiHttpError("channel response data is not an object")
        raw_models = str(raw.get("models", ""))
        models = tuple(
            model.strip() for model in raw_models.split(",") if model.strip()
        )
        raw_tag = str(raw.get("tag") or "").strip()
        tags = (raw_tag,) if raw_tag else ()
        routes_response = self.transport.request_json(
            "GET", f"/api/channel/routes/{channel_id}"
        )
        raw_routes = self._data(routes_response)
        if not isinstance(raw_routes, list):
            raise NewApiHttpError("channel routes response data is not a list")
        disabled_routes: list[str] = []
        for route in raw_routes:
            if not isinstance(route, Mapping):
                raise NewApiHttpError("channel route response item is not an object")
            try:
                route_channel_id = int(route.get("channel_id", 0))
            except (TypeError, ValueError) as exc:
                raise NewApiHttpError("channel route id is invalid") from exc
            if route_channel_id != channel_id:
                raise NewApiHttpError("channel route response crossed channel boundary")
            if route.get("enabled") is not True:
                group = str(route.get("group", "")).strip()
                route_model = str(route.get("model", "")).strip()
                if not group or not route_model:
                    raise NewApiHttpError("disabled channel route is missing group/model")
                disabled_routes.append(f"{group}::{route_model}")
        return ChannelSnapshot(
            channel_id=int(raw["id"]),
            name=str(raw.get("name", "")),
            enabled=int(raw.get("status", 0)) == self.STATUS_ENABLED,
            tags=tags,
            models=models,
            disabled_models=(),
            disabled_routes=tuple(sorted(disabled_routes)),
        )

    def set_channel_enabled(self, channel_id: int, enabled: bool) -> None:
        status = self.STATUS_ENABLED if enabled else self.STATUS_AUTO_DISABLED
        response = self.transport.request_json(
            "PUT", "/api/channel/", body={"id": channel_id, "status": status}
        )
        self._data(response)

    def set_channel_model_enabled(
        self, channel_id: int, group: str, model: str, enabled: bool
    ) -> None:
        response = self.transport.request_json(
            "PUT",
            "/api/channel/route",
            body={
                "channel_id": channel_id,
                "group": group,
                "model": model,
                "enabled": enabled,
                "expected_enabled": not enabled,
            },
        )
        self._data(response)

    def probe_channel_model(self, channel_id: int, model: str) -> bool:
        return self.probe_channel_model_result(channel_id, model).success

    def probe_channel_model_result(self, channel_id: int, model: str) -> ProbeResult:
        response = self.transport.request_json(
            "GET",
            f"/api/channel/test/{channel_id}",
            query={"model": model},
        )
        try:
            status_code = int(response.get("status_code", 0))
        except (TypeError, ValueError):
            status_code = 0
        return ProbeResult(
            success=response.get("success") is True,
            status_code=status_code,
            error_code=str(response.get("error_code", "")),
            message=str(response.get("message", "")),
        )

    def query_usage_events(
        self, channel_id: int, model: str, since: datetime, until: datetime
    ) -> list[UsageEvent]:
        if since.tzinfo is None or until.tzinfo is None:
            raise ValueError("event query times must include a timezone")
        events: list[UsageEvent] = []
        page = 1
        while page <= self.max_log_pages:
            response = self.transport.request_json(
                "GET",
                "/api/log/",
                query={
                    "type": self.ERROR_LOG_TYPE,
                    "start_timestamp": int(since.timestamp()),
                    "end_timestamp": int(until.timestamp()),
                    "model_name": model,
                    "channel": channel_id,
                    "p": page,
                    "page_size": 100,
                },
            )
            data = self._data(response)
            if not isinstance(data, Mapping):
                raise NewApiHttpError("log response data is not an object")
            items = data.get("items", [])
            if not isinstance(items, list):
                raise NewApiHttpError("log response items is not a list")
            for item in items:
                event = self._parse_log_event(item)
                if (
                    event is not None
                    and event.channel_id == channel_id
                    and event.model == model
                    and since <= event.timestamp <= until
                ):
                    events.append(event)
            total = int(data.get("total", len(items)))
            if not items or page * 100 >= total:
                break
            page += 1
        return sorted(events, key=lambda event: event.timestamp)

    def _parse_log_event(self, item: Any) -> UsageEvent | None:
        if not isinstance(item, Mapping):
            return None
        try:
            channel_id = int(item.get("channel", 0))
            model = str(item.get("model_name", ""))
            created_at = int(item["created_at"])
        except (KeyError, TypeError, ValueError):
            return None
        status_code = 0
        raw_other = item.get("other")
        if isinstance(raw_other, str) and raw_other.strip():
            try:
                raw_other = json.loads(raw_other)
            except json.JSONDecodeError:
                raw_other = None
        if isinstance(raw_other, Mapping):
            try:
                status_code = int(raw_other.get("status_code", 0))
            except (TypeError, ValueError):
                status_code = 0
        return UsageEvent(
            timestamp=datetime.fromtimestamp(created_at, UTC),
            channel_id=channel_id,
            model=model,
            status_code=status_code,
            message=str(item.get("content", "")),
        )
