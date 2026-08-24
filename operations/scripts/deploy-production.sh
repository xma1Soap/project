#!/bin/sh
set -eu

APP_DIR=${NEW_API_APP_DIR:-/opt/new-api}
DATA_DIR=${NEW_API_DATA_DIR:-/opt/new-api/data}
CONTAINER=${NEW_API_CONTAINER:-new-api}
BACKUP_ROOT=${NEW_API_BACKUP_ROOT:-/var/backups/new-api-production}
ARTIFACT=${1:-}

fail() { echo "ERROR: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || fail "run as root"
[ -f "$ARTIFACT" ] || fail "artifact not found"
[ "$CONTAINER" = "new-api" ] || fail "unexpected container"
[ "$(realpath -e "$APP_DIR")" = "/opt/new-api" ] || fail "unexpected app directory"
[ "$(realpath -e "$DATA_DIR")" = "/opt/new-api/data" ] || fail "unexpected data directory"
[ -f "$APP_DIR/docker-compose.yml" ] || fail "compose missing"
[ -f "$APP_DIR/new-api.bin" ] || fail "live binary missing"
[ -f "$DATA_DIR/one-api.db" ] || fail "database missing"

MOUNTS=$(docker inspect -f '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}' "$CONTAINER")
printf '%s\n' "$MOUNTS" | grep -F '/opt/new-api/new-api.bin -> /new-api' >/dev/null || fail "binary mount mismatch"
printf '%s\n' "$MOUNTS" | grep -F '/opt/new-api/data -> /data' >/dev/null || fail "data mount mismatch"

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
    echo "Deployment failed; restoring original binary and data." >&2
    docker stop -t 25 "$CONTAINER" >/dev/null 2>&1 || true
    if [ "$READY" -eq 1 ]; then
      mv "$DATA_DIR" "$BACKUP/failed-deploy-data"
      cp -a "$BACKUP/data" "$DATA_DIR"
      install -m 0755 "$BACKUP/new-api" "$APP_DIR/new-api.bin"
    fi
    docker start "$CONTAINER" >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap recover EXIT HUP INT TERM

docker stop -t 25 "$CONTAINER" >/dev/null
cp -a "$APP_DIR/new-api.bin" "$BACKUP/new-api"
cp -a "$APP_DIR/docker-compose.yml" "$BACKUP/docker-compose.yml"
cp -a "$DATA_DIR" "$BACKUP/data"
python3 - "$BACKUP/data/one-api.db" "$BACKUP/pre-state.json" <<'PY'
import json, sqlite3, sys
db = sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True)
assert db.execute('pragma quick_check').fetchone()[0] == 'ok'
state = {
    'counts': {t: db.execute('select count(*) from ' + t).fetchone()[0] for t in ('users', 'channels', 'abilities')},
    'channels': list(db.execute('select id,status from channels order by id')),
    'abilities': list(db.execute('select channel_id,`group`,model,enabled from abilities order by channel_id,`group`,model')),
}
with open(sys.argv[2], 'w', encoding='utf-8') as handle:
    json.dump(state, handle, ensure_ascii=False, separators=(',', ':'))
db.close()
PY
chmod -R go-rwx "$BACKUP"
printf '%s\n' "$BACKUP" > "$BACKUP_ROOT/last_backup"
if [ ! -f "$BACKUP_ROOT/original_backup" ]; then printf '%s\n' "$BACKUP" > "$BACKUP_ROOT/original_backup"; fi
chmod 0600 "$BACKUP_ROOT/last_backup" "$BACKUP_ROOT/original_backup"
READY=1

STAGED="$APP_DIR/.new-api.bin.$STAMP"
install -m 0755 "$ARTIFACT" "$STAGED"
chown --reference="$APP_DIR/new-api.bin" "$STAGED"
mv -f "$STAGED" "$APP_DIR/new-api.bin"
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
python3 - "$DATA_DIR/one-api.db" "$BACKUP/pre-state.json" <<'PY'
import json, sqlite3, sys
with open(sys.argv[2], encoding='utf-8') as handle:
    before = json.load(handle)
db = sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True)
assert db.execute('pragma quick_check').fetchone()[0] == 'ok'
after = {
    'counts': {t: db.execute('select count(*) from ' + t).fetchone()[0] for t in ('users', 'channels', 'abilities')},
    'channels': [list(row) for row in db.execute('select id,status from channels order by id')],
    'abilities': [list(row) for row in db.execute('select channel_id,`group`,model,enabled from abilities order by channel_id,`group`,model')],
}
db.close()
assert before == after, 'channel or ability state changed during deployment'
print('production_database_state=unchanged')
PY

DONE=1
trap - EXIT HUP INT TERM
echo "Deployment succeeded."
echo "Backup: $BACKUP"
echo "Rollback: $(dirname "$0")/rollback-production.sh --full '$BACKUP'"
