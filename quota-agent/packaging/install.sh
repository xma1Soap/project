#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BUNDLE_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
MODE=agent-only
WIZARD=0
CONFIG_SOURCE=
START_SERVICE=1
BACKUP_ROOT=/var/backups/gensoukyou-quota-agent
AGENT_BIN=/usr/local/bin/gensoukyou-quota-agent
CONFIG_DIR=/etc/gensoukyou-quota-agent
STATE_DIR=/var/lib/gensoukyou-quota-agent
UNIT=/etc/systemd/system/gensoukyou-quota-agent.service
LIB_DIR=/usr/local/lib/gensoukyou-quota-agent

fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
usage() {
  printf '%s\n' 'usage: install.sh [--full|--agent-only] [--wizard] [--config FILE] [--no-start]'
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --full) MODE=full ;;
    --agent-only) MODE=agent-only ;;
    --wizard) WIZARD=1 ;;
    --config) shift; [ "$#" -gt 0 ] || fail '--config requires a file'; CONFIG_SOURCE=$1 ;;
    --no-start) START_SERVICE=0 ;;
    --help|-h) usage; exit 0 ;;
    *) fail "unknown option: $1" ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || fail 'run as root'
command -v systemctl >/dev/null 2>&1 || fail 'systemd is required'
command -v sha256sum >/dev/null 2>&1 || fail 'sha256sum is required'
command -v runuser >/dev/null 2>&1 || fail 'runuser is required'
[ -x "$BUNDLE_ROOT/bin/gensoukyou-quota-agent" ] || fail 'agent binary missing from release bundle'
[ -f "$SCRIPT_DIR/gensoukyou-quota-agent.service" ] || fail 'systemd unit missing'
[ -f "$BUNDLE_ROOT/examples/config.json" ] || fail 'example config missing'
if [ -f "$BUNDLE_ROOT/SHA256SUMS" ]; then
  (cd "$BUNDLE_ROOT" && sha256sum -c SHA256SUMS) || fail 'release checksum verification failed'
fi
if [ "$MODE" = full ]; then
  [ -x "$BUNDLE_ROOT/bin/new-api" ] || fail 'custom NewAPI binary missing from full bundle'
  [ -x "$BUNDLE_ROOT/operations/canary-production.sh" ] || fail 'NewAPI canary script missing'
  [ -x "$BUNDLE_ROOT/operations/deploy-production.sh" ] || fail 'NewAPI deployment script missing'
  [ -x "$BUNDLE_ROOT/operations/rollback-production.sh" ] || fail 'NewAPI rollback script missing'
fi
if [ -n "$CONFIG_SOURCE" ]; then
  CONFIG_SOURCE=$(realpath -e "$CONFIG_SOURCE")
  [ -f "$CONFIG_SOURCE" ] || fail 'config source is not a file'
fi

install -d -m 0700 "$BACKUP_ROOT"
BACKUP_ROOT=$(realpath -e "$BACKUP_ROOT")
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
BACKUP="$BACKUP_ROOT/$STAMP"
install -d -m 0700 "$BACKUP"
if systemctl is-active --quiet gensoukyou-quota-agent.service 2>/dev/null; then
  systemctl stop gensoukyou-quota-agent.service
  : > "$BACKUP/was-active"
fi
if systemctl is-enabled --quiet gensoukyou-quota-agent.service 2>/dev/null; then : > "$BACKUP/was-enabled"; fi
if [ -f "$AGENT_BIN" ]; then cp -a "$AGENT_BIN" "$BACKUP/agent-binary"; fi
if [ -d "$CONFIG_DIR" ]; then cp -a "$CONFIG_DIR" "$BACKUP/etc"; fi
if [ -d "$STATE_DIR" ]; then cp -a "$STATE_DIR" "$BACKUP/var-lib"; fi
if [ -f "$UNIT" ]; then cp -a "$UNIT" "$BACKUP/service"; fi
if [ -d "$LIB_DIR" ]; then cp -a "$LIB_DIR" "$BACKUP/lib"; fi
if getent passwd gensoukyou-quota >/dev/null 2>&1; then : > "$BACKUP/user-existed"; fi
if getent group gensoukyou-quota >/dev/null 2>&1; then : > "$BACKUP/group-existed"; fi
printf '%s\n' "$BACKUP" > "$BACKUP_ROOT/last_backup"
chmod 0600 "$BACKUP_ROOT/last_backup"

DONE=0
NEWAPI_CHANGED=0
TTY_HIDDEN=0
recover() {
  status=$?
  trap - EXIT HUP INT TERM
  set +e
  if [ "$TTY_HIDDEN" -eq 1 ]; then stty echo; printf '\n' >&2; fi
  if [ "$DONE" -eq 0 ]; then
    printf '%s\n' 'Installation failed; restoring the previous agent state.' >&2
    if [ "$NEWAPI_CHANGED" -eq 1 ] && [ -f "$BACKUP/new-api-backup-path" ]; then
      IFS= read -r newapi_backup < "$BACKUP/new-api-backup-path"
      "$BUNDLE_ROOT/operations/rollback-production.sh" --binary-only "$newapi_backup" >/dev/null 2>&1
    fi
    "$SCRIPT_DIR/rollback.sh" --agent-only "$BACKUP" >/dev/null 2>&1
  fi
  exit "$status"
}
trap recover EXIT HUP INT TERM

if [ "$MODE" = full ]; then
  "$BUNDLE_ROOT/operations/canary-production.sh" "$BUNDLE_ROOT/bin/new-api"
  "$BUNDLE_ROOT/operations/deploy-production.sh" "$BUNDLE_ROOT/bin/new-api"
  IFS= read -r NEWAPI_BACKUP < /var/backups/new-api-production/last_backup
  printf '%s\n' "$NEWAPI_BACKUP" > "$BACKUP/new-api-backup-path"
  NEWAPI_CHANGED=1
fi

if ! getent group gensoukyou-quota >/dev/null 2>&1; then groupadd --system gensoukyou-quota; fi
if ! getent passwd gensoukyou-quota >/dev/null 2>&1; then
  useradd --system --gid gensoukyou-quota --home-dir /var/lib/gensoukyou-quota-agent --shell /usr/sbin/nologin gensoukyou-quota
fi
GROUP_RECORD=$(getent group gensoukyou-quota) || fail 'service group lookup failed'
USER_RECORD=$(getent passwd gensoukyou-quota) || fail 'service user lookup failed'
IFS=: read -r GROUP_NAME GROUP_PASSWORD GROUP_GID GROUP_MEMBERS <<EOF
$GROUP_RECORD
EOF
IFS=: read -r USER_NAME USER_PASSWORD USER_UID USER_GID USER_GECOS USER_HOME USER_SHELL <<EOF
$USER_RECORD
EOF
[ "$GROUP_NAME" = gensoukyou-quota ] || fail 'unexpected service group record'
[ "$USER_NAME" = gensoukyou-quota ] || fail 'unexpected service user record'
[ "$USER_GID" = "$GROUP_GID" ] || fail 'existing service user has the wrong primary group'
[ "$USER_HOME" = /var/lib/gensoukyou-quota-agent ] || fail 'existing service user has the wrong home directory'
[ "$USER_SHELL" = /usr/sbin/nologin ] || fail 'existing service user has an interactive or unexpected shell'
install -d -o root -g gensoukyou-quota -m 0750 "$CONFIG_DIR"
install -d -o gensoukyou-quota -g gensoukyou-quota -m 0750 "$STATE_DIR" /run/gensoukyou-quota-agent
install -d -o root -g root -m 0755 "$LIB_DIR"
install -m 0755 "$BUNDLE_ROOT/bin/gensoukyou-quota-agent" "$AGENT_BIN"
install -m 0755 "$SCRIPT_DIR/rollback.sh" "$LIB_DIR/rollback-agent.sh"
install -m 0755 "$SCRIPT_DIR/uninstall.sh" "$LIB_DIR/uninstall.sh"
if [ "$MODE" = full ]; then
  install -m 0755 "$BUNDLE_ROOT/operations/rollback-production.sh" "$LIB_DIR/rollback-production.sh"
fi
if [ -n "$CONFIG_SOURCE" ]; then
  install -o root -g gensoukyou-quota -m 0640 "$CONFIG_SOURCE" "$CONFIG_DIR/config.json"
elif [ ! -f "$CONFIG_DIR/config.json" ]; then
  install -o root -g gensoukyou-quota -m 0640 "$BUNDLE_ROOT/examples/config.json" "$CONFIG_DIR/config.json"
fi
if [ ! -f "$CONFIG_DIR/agent.env" ]; then
  install -o root -g root -m 0600 "$SCRIPT_DIR/agent.env.example" "$CONFIG_DIR/agent.env"
fi
install -m 0644 "$SCRIPT_DIR/gensoukyou-quota-agent.service" "$UNIT"

AGENT_TOKEN=
while IFS= read -r ENV_LINE; do
  case "$ENV_LINE" in
    GENSOUKYOU_NEW_API_ACCESS_TOKEN=*) AGENT_TOKEN=${ENV_LINE#GENSOUKYOU_NEW_API_ACCESS_TOKEN=} ;;
  esac
done < "$CONFIG_DIR/agent.env"
if [ -z "$AGENT_TOKEN" ] && [ -t 0 ]; then
  printf '%s' 'NewAPI root access token (input hidden; leave blank to keep service stopped): '
  stty -echo
  TTY_HIDDEN=1
  IFS= read -r AGENT_TOKEN || AGENT_TOKEN=
  stty echo
  TTY_HIDDEN=0
  printf '\n'
  if [ -n "$AGENT_TOKEN" ]; then
    umask 077
    printf 'GENSOUKYOU_NEW_API_ACCESS_TOKEN=%s\n' "$AGENT_TOKEN" > "$CONFIG_DIR/agent.env"
  fi
fi

if [ "$WIZARD" -eq 1 ]; then
  printf '%s\n' 'Open an SSH tunnel from your computer: ssh -L 8765:127.0.0.1:8765 <server>'
  printf '%s\n' 'Then open the one-time setup_url printed below. The wizard exits after Save.'
  GENSOUKYOU_NEW_API_ACCESS_TOKEN=$AGENT_TOKEN "$AGENT_BIN" wizard --listen 127.0.0.1:8765 --output "$CONFIG_DIR/config.json" --exit-after-save
  chown root:gensoukyou-quota "$CONFIG_DIR/config.json"
  chmod 0640 "$CONFIG_DIR/config.json"
fi

"$AGENT_BIN" check-config --config "$CONFIG_DIR/config.json"
systemctl daemon-reload
HAS_TOKEN=0
if grep -Eq '^GENSOUKYOU_NEW_API_ACCESS_TOKEN=.+$' "$CONFIG_DIR/agent.env"; then HAS_TOKEN=1; fi
if [ "$START_SERVICE" -eq 1 ] && [ "$HAS_TOKEN" -eq 1 ]; then
	GENSOUKYOU_NEW_API_ACCESS_TOKEN=$AGENT_TOKEN
	export GENSOUKYOU_NEW_API_ACCESS_TOKEN
	runuser -u gensoukyou-quota -- "$AGENT_BIN" once --config "$CONFIG_DIR/config.json" >/dev/null
	unset GENSOUKYOU_NEW_API_ACCESS_TOKEN
  systemctl enable --now gensoukyou-quota-agent.service
  systemctl is-active --quiet gensoukyou-quota-agent.service || fail 'agent service did not become active'
else
  systemctl disable gensoukyou-quota-agent.service >/dev/null 2>&1 || true
  printf '%s\n' 'Agent installed but left stopped; populate agent.env and enable it when ready.'
fi
unset AGENT_TOKEN ENV_LINE

DONE=1
trap - EXIT HUP INT TERM
printf 'Installation succeeded. Backup: %s\n' "$BACKUP"
printf '%s\n' 'The installed service has no live-action confirmation flags and therefore starts in dry-run.'
