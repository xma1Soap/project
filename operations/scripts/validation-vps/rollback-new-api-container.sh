#!/bin/sh
set -eu

CONTAINER=${NEW_API_CONTAINER:-new-api}
DATA_DIR=${NEW_API_DATA_DIR:-/data/new-api}
BACKUP_ROOT=${NEW_API_BACKUP_ROOT:-/var/backups/new-api}
MODE=${1:---binary-only}
BACKUP_ARG=${2:-}

fail() { echo "ERROR: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || fail "run as root"
[ "$CONTAINER" = "new-api" ] || fail "unexpected container"
[ "$(realpath -e "$DATA_DIR")" = "/data/new-api" ] || fail "unexpected data directory"
case "$MODE" in --binary-only|--full) ;; *) fail "invalid mode" ;; esac
BACKUP_ROOT=$(realpath -e "$BACKUP_ROOT")
if [ -z "$BACKUP_ARG" ]; then IFS= read -r BACKUP_ARG < "$BACKUP_ROOT/last_backup"; fi
BACKUP=$(realpath -e "$BACKUP_ARG")
case "$BACKUP" in "$BACKUP_ROOT"/*) ;; *) fail "backup outside approved root" ;; esac
[ -f "$BACKUP/new-api" ] || fail "backup binary missing"
[ "$MODE" = "--binary-only" ] || [ -f "$BACKUP/data/one-api.db" ] || fail "backup database missing"

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
RESCUE="$BACKUP_ROOT/pre-rollback-$STAMP"
install -d -m 0700 "$RESCUE"

DONE=0
BINARY_SAVED=0
DATA_SWAPPED=0
recover() {
  status=$?
  trap - EXIT HUP INT TERM
  set +e
  if [ "$DONE" -eq 0 ]; then
    echo "Rollback interrupted; restoring the pre-rollback state." >&2
    docker stop -t 25 "$CONTAINER" >/dev/null 2>&1 || true
    if [ "$BINARY_SAVED" -eq 1 ]; then
      docker cp "$RESCUE/new-api" "$CONTAINER:/new-api" >/dev/null 2>&1 || true
    fi
    if [ "$DATA_SWAPPED" -eq 1 ] && [ -d "$RESCUE/data" ]; then
      if [ -d "$DATA_DIR" ]; then
        mv "$DATA_DIR" "$RESCUE/failed-restored-data" || true
      fi
      mv "$RESCUE/data" "$DATA_DIR"
    fi
    docker start "$CONTAINER" >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap recover EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

docker stop -t 25 "$CONTAINER" >/dev/null
docker cp "$CONTAINER:/new-api" "$RESCUE/new-api"
BINARY_SAVED=1
if [ "$MODE" = "--full" ]; then
  mv "$DATA_DIR" "$RESCUE/data"
  DATA_SWAPPED=1
  cp -a "$BACKUP/data" "$DATA_DIR"
fi
docker cp "$BACKUP/new-api" "$CONTAINER:/new-api"
docker start "$CONTAINER" >/dev/null

HEALTHY=0
i=0
while [ "$i" -lt 30 ]; do
  if curl -fsS --max-time 2 http://127.0.0.1:3000/api/status >/dev/null; then HEALTHY=1; break; fi
  i=$((i + 1)); sleep 1
done
[ "$HEALTHY" -eq 1 ] || fail "rollback health check failed"
DONE=1
trap - EXIT HUP INT TERM
echo "Rollback succeeded: $MODE from $BACKUP"
echo "Pre-rollback rescue: $RESCUE"
