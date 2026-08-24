#!/bin/sh
set -eu

CONTAINER=${NEW_API_CONTAINER:-new-api}
DATA_DIR=${NEW_API_DATA_DIR:-/data/new-api}
BACKUP_ROOT=${NEW_API_BACKUP_ROOT:-/var/backups/new-api}
ARTIFACT=${1:-}

fail() { echo "ERROR: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || fail "run as root"
[ -f "$ARTIFACT" ] || fail "artifact not found"
[ "$CONTAINER" = "new-api" ] || fail "unexpected container"
[ "$(realpath -e "$DATA_DIR")" = "/data/new-api" ] || fail "unexpected data directory"
docker inspect "$CONTAINER" >/dev/null

install -d -m 0700 "$BACKUP_ROOT"
BACKUP_ROOT=$(realpath -e "$BACKUP_ROOT")
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
BACKUP="$BACKUP_ROOT/$STAMP"
install -d -m 0700 "$BACKUP"
install -m 0755 "$ARTIFACT" "$BACKUP/candidate-new-api"
docker inspect "$CONTAINER" > "$BACKUP/container-inspect.json"
docker inspect -f '{{.Image}}' "$CONTAINER" > "$BACKUP/image-id"

READY=0
DONE=0
recover() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$DONE" -eq 0 ]; then
    echo "Deployment failed; restoring binary and data." >&2
    docker stop -t 25 "$CONTAINER" >/dev/null 2>&1 || true
    if [ "$READY" -eq 1 ]; then
      mv "$DATA_DIR" "$BACKUP/failed-deploy-data"
      cp -a "$BACKUP/data" "$DATA_DIR"
      docker cp "$BACKUP/new-api" "$CONTAINER:/new-api" >/dev/null
    fi
    docker start "$CONTAINER" >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap recover EXIT HUP INT TERM

docker stop -t 25 "$CONTAINER" >/dev/null
docker cp "$CONTAINER:/new-api" "$BACKUP/new-api"
cp -a "$DATA_DIR" "$BACKUP/data"
chmod -R go-rwx "$BACKUP"
printf '%s\n' "$BACKUP" > "$BACKUP_ROOT/last_backup"
READY=1

docker cp "$BACKUP/candidate-new-api" "$CONTAINER:/new-api"
docker start "$CONTAINER" >/dev/null

HEALTHY=0
i=0
while [ "$i" -lt 30 ]; do
  if curl -fsS --max-time 2 http://127.0.0.1:3000/api/status >/dev/null; then HEALTHY=1; break; fi
  i=$((i + 1)); sleep 1
done
[ "$HEALTHY" -eq 1 ] || fail "health check failed"

EXPECTED=$(sha256sum "$ARTIFACT" | awk '{print $1}')
ACTUAL=$(docker exec "$CONTAINER" sha256sum /new-api | awk '{print $1}')
[ "$EXPECTED" = "$ACTUAL" ] || fail "container binary hash mismatch"
python3 - "$DATA_DIR/one-api.db" "$BACKUP/data/one-api.db" <<'PY'
import sqlite3, sys
states = []
for path in sys.argv[1:]:
    db = sqlite3.connect('file:' + path + '?mode=ro', uri=True)
    assert db.execute('pragma quick_check').fetchone()[0] == 'ok'
    states.append(tuple(db.execute('select count(*) from ' + table).fetchone()[0] for table in ('users', 'channels', 'abilities')))
    db.close()
assert states[0] == states[1], (states[0], states[1])
print('database_counts=' + ','.join(map(str, states[0])))
PY

DONE=1
trap - EXIT HUP INT TERM
echo "Deployment succeeded."
echo "Backup: $BACKUP"
echo "Rollback: $(dirname "$0")/rollback-new-api-container.sh --full '$BACKUP'"
