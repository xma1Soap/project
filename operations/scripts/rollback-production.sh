#!/bin/sh
set -eu

APP_DIR=${NEW_API_APP_DIR:-/opt/new-api}
DATA_DIR=${NEW_API_DATA_DIR:-/opt/new-api/data}
CONTAINER=${NEW_API_CONTAINER:-new-api}
BACKUP_ROOT=${NEW_API_BACKUP_ROOT:-/var/backups/new-api-production}
MODE=${1:---binary-only}
BACKUP_ARG=${2:-}

fail() { echo "ERROR: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || fail "run as root"
[ "$CONTAINER" = "new-api" ] || fail "unexpected container"
[ "$(realpath -e "$APP_DIR")" = "/opt/new-api" ] || fail "unexpected app directory"
[ "$(realpath -e "$DATA_DIR")" = "/opt/new-api/data" ] || fail "unexpected data directory"
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
docker stop -t 25 "$CONTAINER" >/dev/null
cp -a "$APP_DIR/new-api.bin" "$RESCUE/new-api"
if [ "$MODE" = "--full" ]; then
  mv "$DATA_DIR" "$RESCUE/data"
  cp -a "$BACKUP/data" "$DATA_DIR"
fi
install -m 0755 "$BACKUP/new-api" "$APP_DIR/new-api.bin"
docker start "$CONTAINER" >/dev/null

HEALTHY=0
i=0
while [ "$i" -lt 30 ]; do
  if curl -fsS --max-time 2 http://127.0.0.1:3000/api/status >/dev/null; then HEALTHY=1; break; fi
  i=$((i + 1)); sleep 1
done
if [ "$HEALTHY" -ne 1 ]; then
  echo "Rollback failed health check; restoring pre-rollback state." >&2
  docker stop -t 25 "$CONTAINER" >/dev/null 2>&1 || true
  install -m 0755 "$RESCUE/new-api" "$APP_DIR/new-api.bin"
  if [ "$MODE" = "--full" ]; then
    mv "$DATA_DIR" "$RESCUE/failed-restored-data"
    mv "$RESCUE/data" "$DATA_DIR"
  fi
  docker start "$CONTAINER" >/dev/null 2>&1 || true
  exit 1
fi
python3 - "$DATA_DIR/one-api.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True)
assert db.execute('pragma quick_check').fetchone()[0] == 'ok'
db.close()
print('database_quick_check=ok')
PY
echo "Rollback succeeded: $MODE from $BACKUP"
echo "Pre-rollback rescue: $RESCUE"
