#!/bin/sh
set -eu

APP_DIR=${NEW_API_APP_DIR:-/opt/new-api}
CONTAINER=${NEW_API_CONTAINER:-new-api}
CANARY_ROOT=${NEW_API_CANARY_ROOT:-/opt/new-api-canary}
ARTIFACT=${1:-}

fail() { echo "ERROR: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || fail "run as root"
[ -f "$ARTIFACT" ] || fail "artifact not found"
[ "$CONTAINER" = "new-api" ] || fail "unexpected container"
[ "$(realpath -e "$APP_DIR")" = "/opt/new-api" ] || fail "unexpected app directory"
[ -f "$APP_DIR/docker-compose.yml" ] || fail "production compose missing"
[ -f "$APP_DIR/new-api.bin" ] || fail "production binary missing"
[ -f "$APP_DIR/data/one-api.db" ] || fail "production database missing"

MOUNTS=$(docker inspect -f '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}' "$CONTAINER")
printf '%s\n' "$MOUNTS" | grep -F '/opt/new-api/new-api.bin -> /new-api' >/dev/null || fail "binary mount mismatch"
printf '%s\n' "$MOUNTS" | grep -F '/opt/new-api/data -> /data' >/dev/null || fail "data mount mismatch"

install -d -m 0700 "$CANARY_ROOT/data" "$CANARY_ROOT/logs"
[ "$(realpath -e "$CANARY_ROOT")" = "/opt/new-api-canary" ] || fail "unexpected canary directory"
rm -f "$CANARY_ROOT/data/one-api.db" "$CANARY_ROOT/data/one-api.db-wal" "$CANARY_ROOT/data/one-api.db-shm"
python3 - "$APP_DIR/data/one-api.db" "$CANARY_ROOT/data/one-api.db" <<'PY'
import sqlite3, sys
source = sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True, timeout=10)
target = sqlite3.connect(sys.argv[2])
source.backup(target)
target.close()
source.close()
PY
install -m 0755 "$ARTIFACT" "$CANARY_ROOT/new-api.candidate"

IMAGE=$(docker inspect -f '{{.Config.Image}}' "$CONTAINER")
docker rm -f new-api-canary >/dev/null 2>&1 || true
cleanup() { docker rm -f new-api-canary >/dev/null 2>&1 || true; }
trap cleanup EXIT HUP INT TERM
docker run -d --name new-api-canary --restart no \
  -p 127.0.0.1:3001:3000 \
  -v "$CANARY_ROOT/data:/data" \
  -v "$CANARY_ROOT/logs:/app/logs" \
  -v "$CANARY_ROOT/new-api.candidate:/new-api:ro" \
  -e TZ=Asia/Shanghai -e VERSION=v8.13 -e NODE_NAME=new-api-canary \
  -e ERROR_LOG_ENABLED=true -e BATCH_UPDATE_ENABLED=false \
  "$IMAGE" --log-dir /app/logs >/dev/null

HEALTHY=0
i=0
while [ "$i" -lt 30 ]; do
  if curl -fsS --max-time 2 http://127.0.0.1:3001/api/status >/dev/null; then HEALTHY=1; break; fi
  i=$((i + 1)); sleep 1
done
[ "$HEALTHY" -eq 1 ] || { docker logs --tail 100 new-api-canary; fail "canary health failed"; }
curl -fsS --max-time 5 http://127.0.0.1:3001/ >/dev/null
EXPECTED=$(sha256sum "$ARTIFACT" | awk '{print $1}')
ACTUAL=$(docker exec new-api-canary sha256sum /new-api | awk '{print $1}')
[ "$EXPECTED" = "$ACTUAL" ] || fail "canary binary hash mismatch"
python3 - "$APP_DIR/data/one-api.db" "$CANARY_ROOT/data/one-api.db" <<'PY'
import sqlite3, sys
states = []
for path in sys.argv[1:]:
    db = sqlite3.connect('file:' + path + '?mode=ro', uri=True)
    assert db.execute('pragma quick_check').fetchone()[0] == 'ok'
    counts = tuple(db.execute('select count(*) from ' + t).fetchone()[0] for t in ('users', 'channels', 'abilities'))
    route_state = tuple(db.execute('select id,status from channels order by id'))
    ability_state = tuple(db.execute('select channel_id,`group`,model,enabled from abilities order by channel_id,`group`,model'))
    states.append((counts, route_state, ability_state))
    db.close()
assert states[0] == states[1]
print('canary_database_state=unchanged')
PY
echo "Canary succeeded."
