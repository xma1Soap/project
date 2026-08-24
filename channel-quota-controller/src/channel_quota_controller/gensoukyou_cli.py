from __future__ import annotations

import argparse
import json
import os
import sys
import time
from datetime import UTC, datetime
from urllib.parse import urlsplit

from .cli import parse_datetime
from .config import load_config
from .controller import QuotaController
from .newapi_adapter import NewApiGatewayAdapter, NewApiIntegrationContract
from .newapi_v813 import (
    NewApiAuth,
    NewApiHttpError,
    NewApiV813Backend,
    UrllibJsonTransport,
)
from .probe_events import ProbeHistoryEventSource
from .storage import AuditLogger, SingleInstanceLock, StateStore


ACCESS_TOKEN_ENV = "GENSOUKYOU_ADMIN_ACCESS_TOKEN"
BASE_URL_ENV = "GENSOUKYOU_NEW_API_BASE_URL"
USER_ID_ENV = "GENSOUKYOU_NEW_API_USER_ID"
PRODUCTION_CONFIRMATION = "gensoukyou.xyz"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="幻想乡公益站专用渠道额度控制器"
    )
    parser.add_argument("--config", required=True)
    parser.add_argument("--base-url", default=os.environ.get(BASE_URL_ENV))
    parser.add_argument(
        "--user-id", type=int, default=int(os.environ.get(USER_ID_ENV, "1"))
    )
    parser.add_argument(
        "--environment", choices=("test", "production"), default="test"
    )
    parser.add_argument("--state", default="./var/gensoukyou-state.json")
    parser.add_argument("--audit", default="./var/gensoukyou-audit.jsonl")
    parser.add_argument(
        "--probe-history", default="./var/gensoukyou-probe-history.json"
    )
    parser.add_argument("--lock", default="./var/gensoukyou-controller.lock")
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--now", type=parse_datetime)
    parser.add_argument("--confirm-live-actions", action="store_true")
    parser.add_argument(
        "--confirm-production-host",
        help=f"Production live gate; must equal {PRODUCTION_CONFIRMATION}",
    )
    return parser


def _is_loopback_url(base_url: str) -> bool:
    return urlsplit(base_url).hostname in {"127.0.0.1", "localhost", "::1"}


def _effective_dry_run(config_dry_run: bool, args: argparse.Namespace) -> bool:
    live_confirmed = (not config_dry_run) and args.confirm_live_actions
    if args.environment == "production":
        live_confirmed = live_confirmed and (
            args.confirm_production_host == PRODUCTION_CONFIRMATION
        )
    return not live_confirmed


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.now is not None and not args.once:
        parser.error("--now requires --once")
    if not args.base_url:
        parser.error(f"--base-url or {BASE_URL_ENV} is required")

    parsed_url = urlsplit(args.base_url)
    if parsed_url.scheme not in {"http", "https"} or not parsed_url.netloc:
        parser.error("base URL must be an absolute HTTP(S) URL")
    loopback = _is_loopback_url(args.base_url)
    if parsed_url.scheme != "https" and not loopback:
        parser.error("plain HTTP is allowed only for a loopback test/sidecar URL")
    if args.environment == "test" and not loopback:
        parser.error("test environment must use a loopback URL or SSH tunnel")

    access_token = os.environ.get(ACCESS_TOKEN_ENV, "").strip()
    if not access_token:
        parser.error(f"{ACCESS_TOKEN_ENV} is required and is never accepted on CLI")

    config = load_config(args.config)
    effective_dry_run = _effective_dry_run(config.dry_run, args)
    if not config.dry_run and effective_dry_run:
        print(
            "warning: live mode was requested but all confirmation gates were not "
            "satisfied; forcing dry-run",
            file=sys.stderr,
        )

    transport = UrllibJsonTransport(
        args.base_url,
        NewApiAuth(user_id=args.user_id, access_token=access_token),
        allow_insecure_http=loopback,
    )
    backend = NewApiV813Backend(transport)
    contract = NewApiIntegrationContract.gensoukyou_route_api_source_reviewed()
    adapter = NewApiGatewayAdapter(backend, contract)
    retained_markers = tuple(
        sorted(
            {
                marker
                for route in config.routes
                for marker in route.quota_message_patterns
            }
        )
    )
    events = ProbeHistoryEventSource(
        backend,
        args.probe_history,
        min_probe_interval_seconds=max(
            10, min(route.probe_interval_seconds for route in config.routes)
        ),
        retained_markers=retained_markers,
    )
    controller = QuotaController(
        config,
        adapter,
        events,
        StateStore(args.state),
        AuditLogger(args.audit),
        effective_dry_run=effective_dry_run,
    )

    try:
        with SingleInstanceLock(args.lock):
            while True:
                run_at = args.now or datetime.now(UTC)
                decisions = controller.run_once(run_at)
                print(
                    json.dumps(
                        {
                            "timestamp": run_at.isoformat(),
                            "environment": args.environment,
                            "dry_run": effective_dry_run,
                            "decisions": [item.to_dict() for item in decisions],
                        },
                        ensure_ascii=False,
                        sort_keys=True,
                    ),
                    flush=True,
                )
                if args.once:
                    break
                time.sleep(config.poll_interval_seconds)
    except (NewApiHttpError, OSError, RuntimeError, ValueError) as exc:
        print(f"controller stopped safely: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
