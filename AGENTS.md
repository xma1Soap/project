# AI operation contract

Before changing, testing, building, or deploying anything in this repository, read `AI_DEPLOYMENT.md` and `AI_FAILURE_RECOVERY.md` completely.

Mandatory constraints:

- This repository contains only the Gensoukyou NewAPI project and its optional quota controller. The Vertex project from the damaged VPS is intentionally out of scope.
- Never store credentials, API keys, cookies, SSH passwords, private keys, production databases, logs, prompts, or user data in Git.
- Never infer that a reachable VPS is production. Production must pass the path, Docker mount, database, and domain checks in the deployment runbook.
- Do not bulk-enable, bulk-disable, merge, delete, or rewrite channels. Request-time failover is a transient `(channel, model)` overlay and must not mutate administrator-controlled switches.
- Run canary first. Create a stopped-service, restorable backup before replacing a production binary.
- On any failed health check, state mismatch, database integrity error, or unexpected mount, stop and use the rollback runbook. Do not improvise destructive filesystem commands.
- Treat `channel-quota-controller/` as the legacy policy prototype. New deployments use the static Go agent in `quota-agent/`; never run both controllers at the same time.
- Keep the Go quota agent in dry-run unless config, command-line live confirmation, production-host confirmation, a full observed quota cycle, and the production prerequisites in the runbook are all satisfied.
- Preserve upstream NewAPI names, licenses, attribution, and repository policy files.
