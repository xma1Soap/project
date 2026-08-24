#!/bin/sh
set -eu
exec /root/new-api-release-20260824/rollback-new-api-container.sh \
  --full /var/backups/new-api/20260823T225940Z
