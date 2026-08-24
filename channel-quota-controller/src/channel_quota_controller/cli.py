from __future__ import annotations

import argparse
import json
import sys
import time
from datetime import UTC, datetime

from .adapter import JsonFileGatewayAdapter, JsonLinesEventSource
from .config import load_config
from .controller import QuotaController
from .storage import AuditLogger, SingleInstanceLock, StateStore


def parse_datetime(value: str) -> datetime:
    normalized = value[:-1] + "+00:00" if value.endswith("Z") else value
    try:
        parsed = datetime.fromisoformat(normalized)
    except ValueError as exc:
        raise argparse.ArgumentTypeError(
            "must be an ISO-8601 timestamp with a timezone"
        ) from exc
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise argparse.ArgumentTypeError("timestamp must include a timezone")
    return parsed.astimezone(UTC)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Quota-aware channel route controller")
    parser.add_argument("--config", required=True)
    parser.add_argument("--channels", required=True, help="Local simulation channel JSON")
    parser.add_argument("--events", required=True, help="Usage-event JSONL")
    parser.add_argument("--state", default="./var/state.json")
    parser.add_argument("--audit", default="./var/audit.jsonl")
    parser.add_argument("--lock", default="./var/controller.lock")
    parser.add_argument("--once", action="store_true")
    parser.add_argument(
        "--now",
        type=parse_datetime,
        help="Fixed ISO-8601 time for a deterministic one-shot rehearsal",
    )
    parser.add_argument(
        "--confirm-live-actions",
        action="store_true",
        help="Second safety gate; config dry_run must also be false",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.now is not None and not args.once:
        parser.error("--now requires --once")
    config = load_config(args.config)
    live_confirmed = (not config.dry_run) and args.confirm_live_actions
    effective_dry_run = not live_confirmed
    if not config.dry_run and not args.confirm_live_actions:
        print(
            "warning: config requests live mode, but --confirm-live-actions is absent; "
            "forcing dry-run",
            file=sys.stderr,
        )
    controller = QuotaController(
        config,
        JsonFileGatewayAdapter(args.channels),
        JsonLinesEventSource(args.events),
        StateStore(args.state),
        AuditLogger(args.audit),
        effective_dry_run=effective_dry_run,
    )
    with SingleInstanceLock(args.lock):
        while True:
            run_at = args.now or datetime.now(UTC)
            decisions = controller.run_once(run_at)
            print(
                json.dumps(
                    {
                        "timestamp": run_at.isoformat(),
                        "dry_run": effective_dry_run,
                        "decisions": [decision.to_dict() for decision in decisions],
                    },
                    ensure_ascii=False,
                    sort_keys=True,
                ),
                flush=True,
            )
            if args.once:
                break
            time.sleep(config.poll_interval_seconds)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
