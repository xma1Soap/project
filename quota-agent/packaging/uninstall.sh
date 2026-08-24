#!/bin/sh
set -eu

BACKUP_ROOT=/var/backups/gensoukyou-quota-agent
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
BACKUP="$BACKUP_ROOT/uninstall-$STAMP"
[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'ERROR: run as root' >&2; exit 1; }
install -d -m 0700 "$BACKUP"
systemctl disable --now gensoukyou-quota-agent.service >/dev/null 2>&1 || true
[ ! -f /usr/local/bin/gensoukyou-quota-agent ] || cp -a /usr/local/bin/gensoukyou-quota-agent "$BACKUP/agent-binary"
[ ! -d /etc/gensoukyou-quota-agent ] || cp -a /etc/gensoukyou-quota-agent "$BACKUP/etc"
[ ! -d /var/lib/gensoukyou-quota-agent ] || cp -a /var/lib/gensoukyou-quota-agent "$BACKUP/var-lib"
[ ! -f /etc/systemd/system/gensoukyou-quota-agent.service ] || cp -a /etc/systemd/system/gensoukyou-quota-agent.service "$BACKUP/service"
[ ! -d /usr/local/lib/gensoukyou-quota-agent ] || cp -a /usr/local/lib/gensoukyou-quota-agent "$BACKUP/lib"
rm -f /usr/local/bin/gensoukyou-quota-agent /etc/systemd/system/gensoukyou-quota-agent.service
rm -rf /etc/gensoukyou-quota-agent /var/lib/gensoukyou-quota-agent /run/gensoukyou-quota-agent /usr/local/lib/gensoukyou-quota-agent
systemctl daemon-reload
printf 'Uninstalled. Recoverable backup: %s\n' "$BACKUP"
