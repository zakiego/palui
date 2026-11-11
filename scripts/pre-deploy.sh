#!/bin/bash
# Run by the zaki-selfhost deploy script from the checkout dir, after `git pull`
# and before `docker compose up`. A non-zero exit stops the deploy.
#
#   1. If the data volume exists, snapshot the database with the bundled CLI to
#      /data/backups/pre-deploy-<UTC timestamp>.db.gz (inside the volume), so
#      a migration in the new build can always be undone. Keeps the newest 3.
#      A running app does it with `exec`. A stopped or crash-looping app does
#      it in a one-off container (`run --rm --no-deps`) from palui:latest,
#      which is still the old image at this point. Any failure stops the
#      deploy. Only a missing volume (first deploy) skips the backup.
#   2. If scripts/backup.sh changed, reinstall the nightly backup agent so the
#      installed copy in ~/selfhost/bin doesn't drift from the repo.
set -euo pipefail

cd "$(dirname "$0")/.."
KEEP=3
# Both overridable so tests never touch the real launchd agent.
INSTALLED_BACKUP="${PALUI_INSTALLED_BACKUP:-$HOME/selfhost/bin/palui-backup.sh}"
INSTALL_BACKUP="${PALUI_INSTALL_BACKUP:-./ops/install-backup.sh}"

log() { echo "[pre-deploy] $*"; }

# The data volume: compose project `palui` (compose.yaml `name:`), volume `data`.
DATA_VOLUME="${PALUI_DATA_VOLUME:-palui_data}"

# Docker errors are not hidden: if docker can't answer, the deploy stops here
# instead of migrating without a backup.
volume=$(docker volume ls --quiet --filter "name=^${DATA_VOLUME}\$") || {
  log "docker didn't answer (volume ls), not deploying"
  exit 1
}
running=$(docker compose ps --status running --quiet app) || {
  log "docker didn't answer (compose ps), not deploying"
  exit 1
}

# Runs a command in the app container: `exec` when it's up, otherwise a one-off
# container on the same volume.
if [ -n "$running" ]; then
  in_app() { docker compose exec -T app "$@"; }
else
  in_app() { docker compose run --rm --no-deps -T app "$@"; }
fi

# A volume without a database (the app never got far enough to create it) has
# nothing to lose. Anything other than a clear yes or no is an error.
db_in_volume() {
  local answer
  answer=$(in_app sh -c 'if [ -f /data/palui.db ]; then echo yes; else echo no; fi') || return 2
  case "$answer" in
    *yes) return 0 ;;
    *no) return 1 ;;
    *) return 2 ;;
  esac
}

backup() {
  local dest
  dest="/data/backups/pre-deploy-$(date -u +%Y%m%dT%H%M%SZ).db.gz"
  log "backing up to $dest"
  if ! in_app node .output/server/cli.mjs backup "$dest"; then
    log "backup FAILED, not deploying"
    exit 1
  fi
  # Timestamps sort by name, newest first; drop everything after the newest $KEEP.
  # shellcheck disable=SC2016 # $1 expands inside the container's shell
  in_app sh -c 'ls -1 /data/backups/pre-deploy-*.db.gz 2>/dev/null | sort -r | tail -n +$(($1 + 1)) | xargs -r rm -f --' _ "$KEEP" ||
    log "couldn't prune old pre-deploy backups (the new one is fine)"
}

if [ -z "$volume" ]; then
  log "no $DATA_VOLUME volume (first deploy), no backup needed"
else
  [ -n "$running" ] || log "app isn't running, using a one-off app container on the current image"
  rc=0
  db_in_volume || rc=$?
  case "$rc" in
    0) backup ;;
    1) log "$DATA_VOLUME has no palui.db yet, no backup needed" ;;
    *)
      log "couldn't check for /data/palui.db in $DATA_VOLUME, not deploying"
      exit 1
      ;;
  esac
fi

if command -v launchctl >/dev/null && ! cmp -s scripts/backup.sh "$INSTALLED_BACKUP"; then
  log "scripts/backup.sh differs from $INSTALLED_BACKUP, reinstalling the nightly backup"
  # A launchd hiccup shouldn't block the deploy, but it must be loud.
  "$INSTALL_BACKUP" || log "WARNING: install-backup.sh failed; the nightly backup still runs the old script"
fi
