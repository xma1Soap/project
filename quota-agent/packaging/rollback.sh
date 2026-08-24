#!/bin/sh
set -eu

MODE=${1:---agent-only}
BACKUP_ARG=${2:-}
BACKUP_ROOT=/var/backups/gensoukyou-quota-agent
AGENT_BIN=/usr/local/bin/gensoukyou-quota-agent
CONFIG_DIR=/etc/gensoukyou-quota-agent
STATE_DIR=/var/lib/gensoukyou-quota-agent
UNIT=/etc/systemd/system/gensoukyou-quota-agent.service
LIB_DIR=/usr/local/lib/gensoukyou-quota-agent

fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || fail 'run as root'
case "$MODE" in --agent-only|--full) ;; *) fail 'mode must be --agent-only or --full' ;; esac
BACKUP_ROOT=$(realpath -e "$BACKUP_ROOT")
if [ -z "$BACKUP_ARG" ]; then IFS= read -r BACKUP_ARG < "$BACKUP_ROOT/last_backup"; fi
BACKUP=$(realpath -e "$BACKUP_ARG")
case "$BACKUP" in "$BACKUP_ROOT"/*) ;; *) fail 'backup is outside the approved root' ;; esac

systemctl disable --now gensoukyou-quota-agent.service >/dev/null 2>&1 || true
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
RESCUE="$BACKUP_ROOT/pre-rollback-$STAMP"
install -d -m 0700 "$RESCUE"
if [ -f "$AGENT_BIN" ]; then cp -a "$AGENT_BIN" "$RESCUE/agent-binary"; fi
if [ -d "$CONFIG_DIR" ]; then cp -a "$CONFIG_DIR" "$RESCUE/etc"; fi
if [ -d "$STATE_DIR" ]; then cp -a "$STATE_DIR" "$RESCUE/var-lib"; fi
if [ -f "$UNIT" ]; then cp -a "$UNIT" "$RESCUE/service"; fi
if [ -d "$LIB_DIR" ]; then cp -a "$LIB_DIR" "$RESCUE/lib"; fi

if [ "$MODE" = --full ]; then
  [ -f "$BACKUP/new-api-backup-path" ] || fail 'backup does not contain a NewAPI rollback pointer'
  IFS= read -r NEWAPI_BACKUP < "$BACKUP/new-api-backup-path"
  [ -x "$LIB_DIR/rollback-production.sh" ] || fail 'installed NewAPI rollback helper is missing'
  "$LIB_DIR/rollback-production.sh" --binary-only "$NEWAPI_BACKUP"
fi

if [ -f "$BACKUP/agent-binary" ]; then install -m 0755 "$BACKUP/agent-binary" "$AGENT_BIN"; else rm -f "$AGENT_BIN"; fi
if [ -d "$BACKUP/etc" ]; then rm -rf "$CONFIG_DIR"; cp -a "$BACKUP/etc" "$CONFIG_DIR"; else rm -rf "$CONFIG_DIR"; fi
if [ -d "$BACKUP/var-lib" ]; then rm -rf "$STATE_DIR"; cp -a "$BACKUP/var-lib" "$STATE_DIR"; else rm -rf "$STATE_DIR"; fi
if [ -f "$BACKUP/service" ]; then install -m 0644 "$BACKUP/service" "$UNIT"; else rm -f "$UNIT"; fi
if [ -d "$BACKUP/lib" ]; then rm -rf "$LIB_DIR"; cp -a "$BACKUP/lib" "$LIB_DIR"; else rm -rf "$LIB_DIR"; fi
if [ ! -f "$BACKUP/user-existed" ] && getent passwd gensoukyou-quota >/dev/null 2>&1; then userdel gensoukyou-quota; fi
if [ ! -f "$BACKUP/group-existed" ] && getent group gensoukyou-quota >/dev/null 2>&1; then groupdel gensoukyou-quota; fi
systemctl daemon-reload
if [ -f "$BACKUP/was-enabled" ]; then systemctl enable gensoukyou-quota-agent.service >/dev/null; fi
if [ -f "$BACKUP/was-active" ]; then systemctl start gensoukyou-quota-agent.service; fi
printf 'Rollback succeeded. Pre-rollback rescue: %s\n' "$RESCUE"
