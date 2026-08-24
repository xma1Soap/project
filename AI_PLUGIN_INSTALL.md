# AI installation contract: Gensoukyou quota plugin

This document is written for an AI operator installing the station-specific plugin. It is an execution contract, not a generic introduction. Stop at the first failed invariant. Never print or commit credentials, channel keys, upstream URLs, request data, or the production database.

## 1. What is installed

The release bundle contains two Linux static executables:

- `bin/new-api`: the Gensoukyou NewAPI build with bounded request failover, a root-only route API, and sanitized in-memory quota telemetry;
- `bin/gensoukyou-quota-agent`: the low-overhead quota-cycle estimator and route owner.

The agent monitors only explicitly configured channels. `quota_mode=ignore` is not queried. `quota_mode=known` uses the configured capacity without learning a replacement value. `quota_mode=estimate` records the delta in NewAPI `used_quota` between a confirmed cycle start and a high-confidence hard-quota exhaustion event.

If the agent is installed in the middle of an existing quota cycle, that first sample is marked incomplete and excluded. A learned estimate starts only after recovery probes confirm a reset and establish a new baseline.

The agent never writes the NewAPI database directly. Route changes use the root-only compare-and-swap API. A route disabled by an operator or another tool is never claimed by the agent.

## 2. Mandatory preflight

Verify the repository/release checksum and CPU architecture. On the target, verify all production invariants from `AI_DEPLOYMENT.md`, including the exact `/opt/new-api` layout, Docker mounts, local health endpoint, public domain, SQLite `quick_check`, table counts, and channel/ability state snapshot.

Do not infer production from a reachable host or an IP address. Record and verify the SSH host key outside this repository. Use a short-lived SSH key where possible.

The release bundle must have this layout:

```text
bin/gensoukyou-quota-agent
bin/new-api
packaging/install.sh
packaging/rollback.sh
packaging/uninstall.sh
packaging/gensoukyou-quota-agent.service
operations/canary-production.sh
operations/deploy-production.sh
operations/rollback-production.sh
examples/config.json
docs/AI_PLUGIN_INSTALL.md
docs/AI_PLUGIN_RECOVERY.md
docs/AI_DEPLOYMENT.md
docs/AI_FAILURE_RECOVERY.md
SHA256SUMS
```

Run checksum verification before any installation:

```bash
cd /path/to/extracted/gensoukyou-plugin-linux-ARCH
sha256sum -c SHA256SUMS
```

## 3. Installation choices

Use full mode only when installing the bundled telemetry-capable NewAPI binary. It runs the existing isolated canary and production deployment script before installing the agent:

```bash
sudo ./packaging/install.sh --full --wizard
```

Use agent-only mode only after proving that the currently running NewAPI exposes the root-only `GET /api/channel/quota-snapshot?ids=...` endpoint and `PUT /api/channel/route` contract from this repository:

```bash
sudo ./packaging/install.sh --agent-only --wizard
```

For non-interactive installation, prepare a validated config outside Git and pass it explicitly:

```bash
sudo ./packaging/install.sh --full --config /root/private-config.json --no-start
```

The installer creates a timestamped restorable backup under `/var/backups/gensoukyou-quota-agent`, and full mode also creates the NewAPI backup described in `AI_DEPLOYMENT.md`. A failed install automatically restores the previous agent and NewAPI binary.

## 4. Visual setup over SSH

The wizard binds only to `127.0.0.1` and prints a one-time tokenized URL. From the operator computer, create a tunnel:

```bash
ssh -L 8765:127.0.0.1:8765 <verified-production-host>
```

Open the exact `setup_url` printed by the installer. The page can configure monitored channels, known/unknown quota behavior, route and quota pools, dry-run/live intent, error threshold, backup guard, and reset modes:

- `manual`: never automatically starts recovery;
- `after_days`: schedule recovery X days after confirmed exhaustion;
- `fixed_at`: schedule an exact RFC3339 date and timezone;
- `daily`: next configured local time;
- `annual`: next configured month/day/time.

Saving exits the installation wizard. The page does not accept or persist the NewAPI access token.

## 5. Secret provisioning

The installer can read the token from an interactive hidden terminal prompt. Otherwise edit this root-only file using a secure editor or secret manager:

```text
/etc/gensoukyou-quota-agent/agent.env
```

It must contain `GENSOUKYOU_NEW_API_ACCESS_TOKEN=...` and remain mode `0600`. Never place the value in JSON, shell history, Git, issue text, chat output, or systemd command arguments.

Validate without exposing the secret:

```bash
sudo /usr/local/bin/gensoukyou-quota-agent check-config \
  --config /etc/gensoukyou-quota-agent/config.json
sudo systemctl enable --now gensoukyou-quota-agent.service
sudo systemctl is-active gensoukyou-quota-agent.service
```

## 6. Dry-run acceptance gate

The shipped unit deliberately omits live confirmation flags, so it cannot mutate routes even if `dry_run` is accidentally changed. Keep it this way until all of these are proven:

1. at least one complete reset-to-exhaustion cycle has been observed;
2. every managed route has the exact configured opt-in tag;
3. `route_pool` identifies interchangeable model routes;
4. `quota_pool` identifies a genuinely shared upstream quota/account;
5. independent fallback quota pools satisfy every configured minimum;
6. transient RPM/TPM 429 errors are not classified as hard quota exhaustion;
7. a manual agent rollback and NewAPI binary rollback have succeeded;
8. SQLite and channel/ability state remain unchanged during dry-run.

Inspect sanitized state and service events:

```bash
sudo /usr/local/bin/gensoukyou-quota-agent status \
  --state /var/lib/gensoukyou-quota-agent/state.json
sudo journalctl -u gensoukyou-quota-agent.service --since '-30 minutes' --no-pager
```

No prompts, response bodies, user identifiers, channel keys, or upstream URLs should appear.

## 7. Enabling live route ownership

Live mode requires all three conditions simultaneously:

- config has `"dry_run": false`;
- command includes `--confirm-live-actions`;
- command includes `--confirm-production-host gensoukyou.xyz`.

After the acceptance gate, create a systemd override rather than editing the shipped unit:

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/gensoukyou-quota-agent run --config /etc/gensoukyou-quota-agent/config.json --confirm-live-actions --confirm-production-host gensoukyou.xyz
```

Then run `systemctl daemon-reload` and restart once. Re-check local/public health, database integrity, channel/ability state, agent ownership, 429/5xx rate, latency, memory, and restarts. Stop and roll back on the first mismatch.

For a manually scheduled quota reset, do not enable routes directly. Mark the exhausted pool due and let the agent perform its configured recovery probes:

```bash
sudo systemctl stop gensoukyou-quota-agent.service
sudo /usr/local/bin/gensoukyou-quota-agent reset-pool \
  --config /etc/gensoukyou-quota-agent/config.json --pool '<configured-pool>'
sudo systemctl start gensoukyou-quota-agent.service
```

`reset-pool` refuses to write while the running service owns the state lock. If the command fails, restart the service immediately and investigate; never edit `state.json` by hand.

## 8. Required handoff evidence

An AI installer must leave the user with: release version and checksums, selected install mode, backup paths, dry-run/live state, config validation result, service state, local/public health, SQLite `quick_check`, pre/post counts and route-state comparison, and the exact rollback command. Redact all credentials and production channel details.

Do not claim installation complete from a green unit status alone.
