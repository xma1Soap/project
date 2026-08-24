# AI recovery contract: Gensoukyou quota plugin

Use this procedure after an installer error, agent restart loop, unexpected route change, failed health check, or NewAPI regression.

## Immediate action

Stop the agent first; do not bulk-edit channels:

```bash
sudo systemctl disable --now gensoukyou-quota-agent.service
```

Record the latest agent backup without printing its secret environment file:

```bash
sudo cat /var/backups/gensoukyou-quota-agent/last_backup
```

## Agent-only rollback

Restore the previous agent binary, unit, config, and state. This does not touch NewAPI:

```bash
sudo /usr/local/lib/gensoukyou-quota-agent/rollback-agent.sh --agent-only \
  /var/backups/gensoukyou-quota-agent/<timestamp>
```

The rollback first creates a `pre-rollback-*` rescue snapshot.

## Full plugin rollback

Use full rollback only when the bundled NewAPI binary caused the regression. It restores the agent and then performs a binary-only NewAPI rollback using the exact backup pointer captured by installation:

```bash
sudo /usr/local/lib/gensoukyou-quota-agent/rollback-agent.sh --full \
  /var/backups/gensoukyou-quota-agent/<timestamp>
```

If SQLite integrity or channel/ability state changed, stop and follow `AI_FAILURE_RECOVERY.md`; binary-only rollback is not sufficient evidence of database recovery.

## Ownership recovery

Only routes with `owned_by_agent=true` in the preserved state may be considered for agent recovery. Never enable a route that was manually disabled, has no ownership record, has a pending action that was not reconciled, or lacks the exact configured tag. If ownership is uncertain, leave the route disabled and ask the station owner.

Recovery is complete only after local/public health, SQLite `quick_check`, counts, channel and ability state, service restarts, and a 15-minute observation all pass.
