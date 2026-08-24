# AI failure recovery and rollback

Use this runbook whenever canary or deployment reports an error, the public site returns 5xx, the database check fails, channel state changes unexpectedly, or the new container repeatedly restarts.

## 1. Immediate rules

- Stop further deployment attempts.
- Do not run bulk channel enable/disable, channel merge, database vacuum, schema repair, or recursive deletion.
- Do not restore SQLite files while NewAPI is running.
- Preserve the failed binary, failed data, logs, and backup directory for diagnosis.
- Prefer binary-only rollback when the database is healthy and state is unchanged. Use full rollback after a failed migration, integrity error, or state mutation.

## 2. Locate the backup

Production backups are under `/var/backups/new-api-production/<UTC timestamp>`.

```bash
sudo cat /var/backups/new-api-production/last_backup
sudo cat /var/backups/new-api-production/original_backup
sudo find /var/backups/new-api-production -maxdepth 2 -type f -name new-api -print
```

Each backup must contain `new-api`, `data/one-api.db`, `docker-compose.yml`, `container-inspect.json`, `image-id`, and `pre-state.json`.

## 3. Normal rollback

Preserve current user data when the database remains healthy:

```bash
sudo operations/scripts/rollback-production.sh --binary-only /var/backups/new-api-production/<timestamp>
```

Restore both binary and the stopped-service database snapshot after migration/integrity/state damage:

```bash
sudo operations/scripts/rollback-production.sh --full /var/backups/new-api-production/<timestamp>
```

To return to the state before the first managed deployment:

```bash
BACKUP="$(sudo cat /var/backups/new-api-production/original_backup)"
sudo operations/scripts/rollback-production.sh --full "$BACKUP"
```

The rollback script first creates a pre-rollback rescue snapshot. If the restored version fails health checks, it puts the pre-rollback state back automatically.

## 4. Diagnose by symptom

### Local health fails, container is restarting

```bash
sudo docker inspect -f '{{.State.Status}} {{.State.ExitCode}} {{.State.Error}}' new-api
sudo docker logs --tail 200 new-api
sudo docker inspect -f '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}' new-api
```

If `/opt/new-api/new-api.bin -> /new-api` is absent or the binary hash differs from the uploaded candidate, rollback immediately.

### Local health is 200 but public site is 502/504

Do not restore the database. Check nginx and the loopback target first:

```bash
curl -v --max-time 5 http://127.0.0.1:3000/api/status
sudo nginx -t
sudo systemctl status nginx --no-pager
sudo journalctl -u nginx --since '-10 minutes' --no-pager
```

Reload nginx only after `nginx -t` succeeds. A reverse-proxy failure is not evidence of database damage.

### SQLite integrity or lock failure

Stop NewAPI before copying/restoring database files:

```bash
sudo docker stop -t 25 new-api
sudo python3 - <<'PY'
import sqlite3
p = '/opt/new-api/data/one-api.db'
db = sqlite3.connect('file:' + p + '?mode=ro', uri=True)
print(db.execute('pragma quick_check').fetchone()[0])
db.close()
PY
```

If the result is not `ok`, use full rollback. Never delete `-wal`/`-shm` files from a running database.

### Channel state changed unexpectedly

Stop the new container to prevent further writes and compare live state with the backup `pre-state.json`. Use full rollback. Do not try to reverse hundreds of rows manually.

### Permissions prevent startup

The host binary must be executable and the data directory must remain writable by the container's configured user. Restore ownership/mode from the timestamped backup rather than guessing.

### SSH is unavailable

Use the VPS provider console. Verify the exact hostname and filesystem paths before acting. Start with `docker ps -a`, local health, disk space, and backup listing. Do not reinstall the OS or delete `/opt/new-api`.

## 5. Controller recovery

The Python controller is secondary. Disable it without touching NewAPI:

```bash
sudo systemctl disable --now channel-quota-controller.service
sudo journalctl -u channel-quota-controller.service --since '-30 minutes' --no-pager
```

If it ever ran live, preserve `/var/lib/channel-quota-controller/state.json` and `/var/log/channel-quota-controller/audit.jsonl`. Restore only routes whose state records prove `owned_by_controller=true`; never enable a route that an administrator or another system disabled.

## 6. Completion criteria

Recovery is complete only when:

- container is running without a restart loop;
- local and public status endpoints return 200;
- SQLite `quick_check` is `ok`;
- user/channel/ability counts and channel/ability state match the chosen backup;
- no new panic/fatal log appears during a 15-minute observation;
- the exact backup used and resulting binary hash are recorded outside the server.
