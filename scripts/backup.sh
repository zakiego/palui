#!/bin/bash
# Nightly encrypted backup of Palui to Cloudflare R2 (Zakiego Lab).
#
#   1. snapshot /data/palui.db inside the running app container with the bundled
#      admin CLI (`palui backup`: VACUUM INTO, gzipped), copy it out, check it,
#      encrypt it
#   2. archive the WhatsApp sessions, whichever layout exists:
#      - /data/wa/ (the Go server): one SQLite store per connector, <id>.db in
#        WAL mode. Each is snapshotted with `palui snapshot` (VACUUM INTO, so
#        the WAL is included and a write mid-copy can't tear it) into a staging
#        dir in the volume, and that dir is tarred. Lock files are skipped.
#      - /data/wa-auth/ (the old Node runtime's Baileys files), tarred as is
#      Both stream from the container straight into age, so no plaintext
#      touches the host disk
#   3. upload everything to r2://palui-backups/palui/YYYY-MM-DD/ and check each size
#   4. delete the snapshot and the staging dir in the container and every
#      local temp file
#
# Every file is encrypted with age to the public key in
# ~/.config/palui/age-recipient. The matching identity (private key) lives only
# in the Keychain, never on disk and never in this repo; restore-drill.sh reads
# it from there.
#
# Why the WhatsApp archives are encrypted too: a session alone is enough to
# take over the bot's WhatsApp number as a linked device. Treat it like a password.
#
# Restoring a WhatsApp store: decrypt, `tar -xf`, then, with the server stopped,
# `gunzip -c wa/<id>.db.gz > /data/wa/<id>.db` (no -wal or -shm next to it,
# owned by 1000:1000).
#
# R2 credentials come from the Keychain through the cf-zakiego-lab wrapper.
# Run by launchd (ops/com.zakiego.palui-backup.plist), or by hand.
set -euo pipefail

PALUI_DIR="${PALUI_DIR:-$HOME/selfhost/palui}"
LOG="${BACKUP_LOG:-$HOME/selfhost/palui-backup.log}"
BUCKET="${BACKUP_BUCKET:-palui-backups}"
CF="${CF_WRAPPER:-$HOME/.agents/skills/cf-zakiego-lab/scripts/cf-zakiego-lab}"
RECIPIENT_FILE="${AGE_RECIPIENT_FILE:-$HOME/.config/palui/age-recipient}"
MIN_FREE_MB=500
SNAPSHOT_ATTEMPTS=3
SNAPSHOT_RETRY_DELAY="${SNAPSHOT_RETRY_DELAY:-60}"

# launchd starts with a bare PATH. docker lives in /usr/local/bin, age in
# Homebrew, cf and its node in an nvm version dir.
export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
for d in "$HOME"/.nvm/versions/node/*/bin; do
  [ -x "$d/cf" ] && CF_NODE_BIN="$d"
done
[ -n "${CF_NODE_BIN:-}" ] && export PATH="$CF_NODE_BIN:$PATH"

# Keep the log small: trim to the last 2000 lines once it passes 1 MB.
mkdir -p "$(dirname "$LOG")"
if [ -f "$LOG" ] && [ "$(stat -f %z "$LOG")" -gt 1048576 ]; then
  tail -n 2000 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
fi
if [ -t 1 ]; then
  exec > >(tee -a "$LOG") 2>&1
else
  exec >>"$LOG" 2>&1
fi

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*"; }

day=$(date +%Y-%m-%d)
ts=$(date +%Y%m%d-%H%M%S)
db_name="palui-$ts.db.gz"
db_key="palui/$day/$db_name.age"
wa_key="palui/$day/wa-$ts.tar.age"
wa_auth_key="palui/$day/wa-auth-$ts.tar.gz.age"
remote="/data/backups/$db_name"
# Staging dir for the WhatsApp snapshots, inside the volume. The leading dot
# keeps it out of the dashboard's "latest backup" listing.
stage="/data/backups/.wa-$ts"
tmpdir=""

cleanup() {
  status=$?
  [ -n "$tmpdir" ] && rm -rf "$tmpdir"
  # shellcheck disable=SC2016 # $1 and $2 expand inside the container's shell
  (cd "$PALUI_DIR" && docker compose exec -T app sh -c 'rm -f "$1" "$1".tmp-*; rm -rf "$2"' _ "$remote" "$stage") >/dev/null 2>&1 || true
  if [ "$status" -eq 0 ]; then log "done"; else log "FAILED (exit $status)"; fi
}
trap cleanup EXIT

log "start $day $ts"
command -v docker >/dev/null || { log "docker not found"; exit 1; }
command -v age >/dev/null || { log "age not found (brew install age)"; exit 1; }
[ -x "$CF" ] || { log "cf wrapper not found at $CF"; exit 1; }
command -v cf >/dev/null || { log "cf CLI not found (looked in ~/.nvm/versions/node/*/bin)"; exit 1; }
[ -s "$RECIPIENT_FILE" ] || { log "no age recipient at $RECIPIENT_FILE (one age1... public key per line)"; exit 1; }
free_mb=$(df -m / | awk 'NR==2 {print $4}')
[ "$free_mb" -ge "$MIN_FREE_MB" ] || { log "only ${free_mb} MB free, need ${MIN_FREE_MB}"; exit 1; }

cd "$PALUI_DIR"

# `palui` is on the PATH in the Go image and in the old Node image alike.
snapshot() {
  docker compose exec -T app palui backup "$remote"
}

# The container can be mid-restart (a deploy, a Docker update) at 03:30.
# Give it a few minutes before calling the night a failure.
attempt=1
until snapshot; do
  if [ "$attempt" -ge "$SNAPSHOT_ATTEMPTS" ]; then
    log "snapshot failed $attempt times, giving up"
    exit 1
  fi
  log "snapshot attempt $attempt failed, retrying in ${SNAPSHOT_RETRY_DELAY}s"
  sleep "$SNAPSHOT_RETRY_DELAY"
  attempt=$((attempt + 1))
done

tmpdir=$(mktemp -d /tmp/palui-backup.XXXXXX)
chmod 700 "$tmpdir"

# --- database ---------------------------------------------------------------
plain="$tmpdir/$db_name"
db_file="$tmpdir/$db_name.age"
docker compose cp "app:$remote" "$plain" >/dev/null
gzip -t "$plain"
log "db snapshot $(stat -f %z "$plain") bytes, sha256 $(shasum -a 256 "$plain" | cut -d' ' -f1)"
age -R "$RECIPIENT_FILE" -o "$db_file" "$plain"
rm -f "$plain"

# --- WhatsApp sessions ------------------------------------------------------
# Read through the app container: it's up (the snapshot just worked) and
# mounts the volume. Not paired yet (or WhatsApp removed): nothing to keep,
# and that's fine. Any answer other than a clear yes or no fails the night.
wa_file=""
wa_auth_file=""
wa_dirs=$(docker compose exec -T app sh -c 'for d in wa wa-auth; do if [ -d "/data/$d" ]; then echo "$d=yes"; else echo "$d=no"; fi; done') || {
  log "couldn't check /data/wa and /data/wa-auth through the app container"
  exit 1
}
dir_answer() { printf '%s\n' "$wa_dirs" | tr -d '\r' | grep -E "^$1=(yes|no)\$" | cut -d= -f2 || true; }
has_wa=$(dir_answer wa)
has_wa_auth=$(dir_answer wa-auth)
if [ -z "$has_wa" ] || [ -z "$has_wa_auth" ]; then
  log "unexpected answer checking the WhatsApp dirs: $wa_dirs"
  exit 1
fi

if [ "$has_wa" = yes ]; then
  # `palui snapshot` is the Go image's. An older image (after a rollback to
  # the Node image) never writes wa/, so there its files are at rest and a
  # plain copy of each store with its -wal and -shm is consistent.
  # shellcheck disable=SC2016 # $1 expands inside the container's shell
  stores=$(docker compose exec -T app sh -c '
    set -e
    mkdir -p "$1/wa"
    if palui snapshot 2>&1 | grep -q "usage: palui snapshot"; then
      for f in /data/wa/*.db; do
        [ -f "$f" ] || continue
        palui snapshot "$f" "$1/wa/$(basename "$f").gz" >/dev/null
        basename "$f"
      done
    else
      for f in /data/wa/*.db; do
        [ -f "$f" ] || continue
        cp -p "$f" "$1/wa/"
        for x in wal shm; do [ ! -f "$f-$x" ] || cp -p "$f-$x" "$1/wa/"; done
        echo "$(basename "$f") (raw copy: this image has no palui snapshot)"
      done
    fi' _ "$stage") || {
    log "couldn't snapshot the WhatsApp stores in /data/wa"
    exit 1
  }
  stores=$(printf '%s' "$stores" | tr -d '\r' | tr '\n' ' ')
  if [ -n "$stores" ]; then
    wa_file="$tmpdir/wa-$ts.tar.age"
    # The .gz snapshots are compressed already, so a plain tar.
    docker compose exec -T app tar -cf - -C "$stage" wa | age -R "$RECIPIENT_FILE" -o "$wa_file"
    log "WhatsApp stores: $stores"
  else
    log "/data/wa has no store, skipping the WhatsApp archive"
  fi
  # shellcheck disable=SC2016
  docker compose exec -T app sh -c 'rm -rf "$1"' _ "$stage" || log "couldn't remove $stage (cleanup tries again)"
else
  log "no /data/wa in the volume, skipping the WhatsApp archive"
fi

if [ "$has_wa_auth" = yes ]; then
  # The old Baileys layout. Baileys rewrote its key files atomically and a
  # restore re-syncs, so a fuzzy copy is acceptable. It stays until the owner
  # deletes /data/wa-auth after pairing WhatsApp again on the Go server.
  wa_auth_file="$tmpdir/wa-auth-$ts.tar.gz.age"
  docker compose exec -T app tar -czf - -C /data wa-auth | age -R "$RECIPIENT_FILE" -o "$wa_auth_file"
  log "archived /data/wa-auth (the old Baileys session)"
fi

# --- upload -----------------------------------------------------------------
r2_size() {
  "$CF" r2 objects list --bucket-name "$BUCKET" --prefix "$1" \
    | python3 -c 'import json,sys
d = json.load(sys.stdin)
items = d if isinstance(d, list) else d.get("objects", d.get("result", []))
print(next((o.get("size") for o in items if o.get("key") == sys.argv[1]), ""))' "$1"
}

upload() {
  local file="$1" key="$2" bytes remote_size
  bytes=$(stat -f %z "$file")
  # Explicit checks: set -e doesn't apply inside a function called from a
  # condition, so every step says what failure means.
  "$CF" r2 objects put "$key" --bucket-name "$BUCKET" --file "$file" \
    --content-type application/octet-stream >/dev/null || { log "upload failed for $key"; return 1; }
  remote_size=$(r2_size "$key") || { log "couldn't list $key after upload"; return 1; }
  if [ "$remote_size" != "$bytes" ]; then
    log "uploaded size mismatch for $key: local $bytes, r2 '${remote_size}'"
    return 1
  fi
  log "uploaded r2://$BUCKET/$key ($bytes bytes)"
}

upload "$db_file" "$db_key" || exit 1
if [ -n "$wa_file" ]; then
  upload "$wa_file" "$wa_key" || exit 1
fi
if [ -n "$wa_auth_file" ]; then
  upload "$wa_auth_file" "$wa_auth_key" || exit 1
fi
