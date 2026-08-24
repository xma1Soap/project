# AI deployment procedure

This is an execution runbook for an AI operator. Follow it in order and stop on the first failed invariant.

## 1. Repository scope

- `new-api/`: station-specific NewAPI source with request-time channel/model failover.
- `channel-quota-controller/`: optional long-window quota controller; default and recommended initial mode is dry-run.
- `operations/scripts/`: canary, production deployment, and rollback scripts.
- `operations/templates/`: production Compose template without secrets.

The damaged-machine Vertex project is deliberately not included.

## 2. Required behavior

The NewAPI build has these station-specific defaults:

- two retries after the first attempt, for at most three bounded attempts;
- a recognized upstream 429/quota/rate-limit error cools only the failing `(channel, model)` route for 300 seconds;
- retry selection does not reuse the just-failed route;
- `channels.status` and `abilities.enabled` are not modified by request-time failover;
- transparent retry occurs only before response bytes reach the client. An active stream is never replayed or spliced.

Database options can override process defaults. Before and after deployment, inspect `RetryTimes`, `ChannelCooldownEnabled`, `ChannelCooldownSeconds`, `AutomaticDisableChannelEnabled`, `AutomaticEnableChannelEnabled`, and the automatic-disable keyword/status-code rules in the `options` table without changing them silently. Request-time quota failover bypasses the legacy whole-channel auto-ban path, but the legacy settings still affect non-quota failures.

## 3. Identify the actual production host

Do not deploy based only on an IP supplied in chat. The intended production layout must have all of the following:

```text
/opt/new-api/docker-compose.yml
/opt/new-api/new-api.bin
/opt/new-api/data/one-api.db
Docker container: new-api
Host bind: /opt/new-api/new-api.bin -> /new-api
Host bind: /opt/new-api/data -> /data
Local health: http://127.0.0.1:3000/api/status
Public health: https://gensoukyou.xyz/api/status
```

Record and verify the SSH host-key fingerprint before password/key authentication. Prefer a short-lived SSH key over a password. If any expected path or mount is absent, treat the host as non-production and stop.

## 4. Local verification and build

Use Go 1.26.1 or the version pinned by `new-api/Dockerfile`, Bun 1.x, and Python 3.11+.

```bash
cd new-api
cd web
bun install --frozen-lockfile
cd default
DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION="$(cat ../../VERSION)" bun run build
cd ../classic
VITE_REACT_APP_VERSION="$(cat ../../VERSION)" bun run build
cd ../..

go test -count=1 ./...

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOEXPERIMENT=greenteagc \
  go build -trimpath \
  -ldflags "-s -w -X github.com/QuantumNous/new-api/common.Version=$(cat VERSION)" \
  -o ../new-api.candidate .
sha256sum ../new-api.candidate

cd ../channel-quota-controller
PYTHONPATH=src python -m pytest -q
```

Do not commit `new-api.candidate`.

## 5. Read-only production audit

Run only read operations first:

```bash
sudo docker inspect new-api
sudo docker ps --filter name='^new-api$'
sudo sha256sum /opt/new-api/new-api.bin
curl -fsS http://127.0.0.1:3000/api/status >/dev/null
sudo python3 - <<'PY'
import sqlite3
p = '/opt/new-api/data/one-api.db'
db = sqlite3.connect('file:' + p + '?mode=ro', uri=True)
print('quick_check', db.execute('pragma quick_check').fetchone()[0])
for table in ('users', 'channels', 'abilities'):
    print(table, db.execute('select count(*) from ' + table).fetchone()[0])
print('channels_by_status', list(db.execute('select status,count(*) from channels group by status order by status')))
db.close()
PY
```

Save the counts and channel-status distribution. They must be unchanged after canary and deployment.

## 6. Upload and canary

Upload the candidate outside the live path:

```bash
sudo install -d -m 0700 /root/new-api-release
sudo install -m 0755 /path/to/uploaded/new-api.candidate /root/new-api-release/new-api.candidate
sudo sha256sum /root/new-api-release/new-api.candidate
sudo operations/scripts/canary-production.sh /root/new-api-release/new-api.candidate
```

The canary script:

- validates the real production layout;
- creates an online SQLite backup in an isolated directory;
- starts a temporary Docker container bound only to `127.0.0.1:3001`;
- verifies root page, status API, binary hash, database integrity, and table counts;
- removes the canary container on exit;
- never writes to production data or changes channel state.

Do not perform live upstream inference unless the station owner supplies a dedicated test token and explicitly authorizes the traffic. Keep such a token only in a root-readable environment file.

## 7. Production deployment

Only after canary succeeds:

```bash
sudo operations/scripts/deploy-production.sh /root/new-api-release/new-api.candidate
```

The script stops the container, captures the old binary, full SQLite data directory, Compose file, image ID, container inspection, and pre-deployment route state, then atomically replaces the host-mounted binary. It automatically restores the old binary and data if startup, health, hash, database integrity, or state comparison fails.

Expected interruption is one controlled container stop/start, normally tens of seconds. Do not run channel tests, bulk channel actions, or schema maintenance during this window.

## 8. Post-deployment verification

```bash
curl -fsS http://127.0.0.1:3000/api/status >/dev/null
curl -fsS https://gensoukyou.xyz/api/status >/dev/null
sudo docker inspect -f '{{.State.Status}}' new-api
sudo docker exec new-api sha256sum /new-api
sudo docker logs --since 10m new-api 2>&1 | grep -Ei 'panic|fatal' || true
```

Repeat the read-only SQLite audit from step 5. Counts and `channels.status` / `abilities.enabled` state must match the pre-deployment snapshot.

Observe health, HTTP 5xx, 429 rate, latency, memory, and container restarts for at least 15 minutes. The request-time cooldown is in memory, so process restart intentionally clears it.

## 9. Legacy quota-controller prototype

`channel-quota-controller/` remains as the tested policy prototype and migration reference. Do not install it for a new deployment and never run it alongside the Go agent. Existing installations may keep it in dry-run while planning migration:

```bash
sudo useradd --system --home /opt/channel-quota-controller --shell /usr/sbin/nologin channel-controller || true
sudo install -d -o channel-controller -g channel-controller -m 0750 /opt/channel-quota-controller
sudo python3 -m venv /opt/channel-quota-controller/venv
sudo /opt/channel-quota-controller/venv/bin/pip install /path/to/channel-quota-controller
sudo install -d -m 0750 /etc/channel-quota-controller
sudo install -m 0640 channel-quota-controller/examples/gensoukyou.glm.production-dry-run.json /etc/channel-quota-controller/config.json
sudo test -f /etc/channel-quota-controller/gensoukyou.env || sudo install -o root -g root -m 0600 /dev/null /etc/channel-quota-controller/gensoukyou.env
# Populate gensoukyou.env through a root-only editor or secret manager. It must contain:
# GENSOUKYOU_ADMIN_ACCESS_TOKEN=...
# GENSOUKYOU_NEW_API_BASE_URL=https://gensoukyou.xyz
# GENSOUKYOU_NEW_API_USER_ID=<root user id>
sudo install -d -o channel-controller -g channel-controller -m 0750 /var/lib/channel-quota-controller /var/log/channel-quota-controller /run/channel-quota-controller
sudo install -m 0644 channel-quota-controller/systemd/channel-quota-controller.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now channel-quota-controller.service
```

The installed unit now invokes the station-specific HTTP adapter rather than the local JSON simulator. Keep `dry_run=true`. The unit deliberately omits `--confirm-live-actions` and `--confirm-production-host gensoukyou.xyz`, so it remains forced into dry-run even if the JSON file is edited incorrectly. Do not add both flags until a full quota cycle has been observed, every managed route has explicit opt-in tagging, at least two fallback routes remain, reset times are confirmed, and a manual rollback drill has succeeded.

## 10. Static quota plugin

New deployments use `quota-agent/`, a dependency-free static Go executable. It consumes a single root-only bulk snapshot per polling interval, stores bounded local state, distinguishes transient rate limits from hard quota exhaustion, estimates capacity only from confirmed complete cycles, and can own only explicitly configured channel/model routes.

Build and test locally:

```bash
cd quota-agent
go test -count=1 ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o ../gensoukyou-quota-agent ./cmd/gensoukyou-quota-agent
```

Use the checksummed release installer and the complete AI execution contract in `AI_PLUGIN_INSTALL.md`. The visual wizard binds only to loopback and is reached through an SSH tunnel. The shipped systemd unit omits both live confirmation flags, sets `MemoryMax=64M` and `CPUQuota=10%`, and starts in dry-run.

The first cycle observed after installing mid-cycle is deliberately marked incomplete and excluded from automatic capacity estimation. A complete estimate begins only after a scheduled or manually requested reset has been confirmed by successful recovery probes.
