#!/bin/bash
# Nightly encrypted backup of Palui to Cloudflare R2.
#
#   1. snapshot /data/palui.db inside the running app container with the bundled
#      admin CLI (`palui backup`: VACUUM INTO, gzipped), copy it out with
#      `docker compose cp`, check it, encrypt it
#   2. archive the WhatsApp sessions, whichever layout exists:
#      - /data/wa/ (the Go server): one SQLite store per connector, <id>.db in
#        WAL mode. Each is snapshotted in the app container with `palui
#        snapshot` (VACUUM INTO, so the WAL is included and a write mid-copy
#        can't tear it) into a staging dir in the volume, and that dir is
#        tarred. Lock files are skipped.
#      - /data/wa-auth/ (the old Node runtime's Baileys files), tarred as is
#      Both tars stream straight into age, so no plaintext touches the host disk
#   3. upload everything to r2://palui-backups/palui/YYYY-MM-DD/ and check each size
#   4. delete the snapshot and the staging dir in the volume and every local
#      temp file
#
# The app image is distroless: no sh, tar, ls or rm, only `palui`. So `palui`
# commands run in the app container (`docker compose exec`), and the plain file
# work (does wa/ exist, list the stores, tar, cleanup) runs in a pinned alpine
# helper that borrows the app container's /data mount (`--volumes-from`), as
# uid 1000 like the app, with no network. The helper works the same with any
# palui image running.
#
#   palui-backup.sh             the nightly run
#   palui-backup.sh --dry-run   everything but the upload (no R2 credentials
#                               needed); set BACKUP_DRY_RUN_DIR to keep the
#                               encrypted files there for a look
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
# Uploads go through the `cf` CLI, or the command in CF_WRAPPER (for example a
# wrapper that logs in to the right Cloudflare account).
# Run by launchd (ops/palui-backup.plist, installed by ops/install-backup.sh),
# or by hand.
set -euo pipefail

# Host settings: the environment wins, then ~/.config/palui/host.conf
# (PALUI_HOST_CONF), then the defaults below. host.conf holds KEY=value
# lines and may use $HOME. See the backups docs page.
load_host_conf() {
  local f="${PALUI_HOST_CONF:-$HOME/.config/palui/host.conf}" line key
  [ -f "$f" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in '' | \#*) continue ;; esac
    key="${line%%=*}"
    case "$key" in *[!A-Za-z0-9_]* | '') continue ;; esac
    [ -n "${!key+x}" ] && continue
    eval "export $line"
  done <"$f"
}
load_host_conf
PALUI_HOST_DIR="${PALUI_HOST_DIR:-$HOME/.palui}"
PALUI_DIR="${PALUI_DIR:-$HOME/palui}"
LOG="${BACKUP_LOG:-$PALUI_HOST_DIR/palui-backup.log}"
BUCKET="${BACKUP_BUCKET:-palui-backups}"
CF="${CF_WRAPPER:-cf}"
RECIPIENT_FILE="${AGE_RECIPIENT_FILE:-$HOME/.config/palui/age-recipient}"
MIN_FREE_MB=500
SNAPSHOT_ATTEMPTS=3
SNAPSHOT_RETRY_DELAY="${SNAPSHOT_RETRY_DELAY:-60}"
# Same pin as scripts/pre-deploy.sh and scripts/restore-drill.sh.
HELPER_IMAGE="${PALUI_HELPER_IMAGE:-alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf}"
DRY_RUN="${BACKUP_DRY_RUN:-0}"
DRY_RUN_DIR="${BACKUP_DRY_RUN_DIR:-}"
case "${1:-}" in
  "") ;;
  --dry-run) DRY_RUN=1 ;;
  *) echo "usage: $0 [--dry-run]" >&2; exit 2 ;;
esac

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
# The app container, set once the snapshot worked; the helper mounts its volume.
app_id=""

# helper ro|rw <command...>: run a command in the alpine helper on the app's
# /data, read-only or read-write.
helper() {
  local mode="$1"
  shift
  docker run --rm --network none --user 1000:1000 \
    --volumes-from "$app_id:$mode" "$HELPER_IMAGE" "$@"
}

cleanup() {
  status=$?
  [ -n "$tmpdir" ] && rm -rf "$tmpdir"
  if [ -n "$app_id" ]; then
    # shellcheck disable=SC2016 # $1 and $2 expand inside the helper's shell
    helper rw sh -c 'rm -f "$1" "$1".tmp-*; rm -rf "$2"' _ "$remote" "$stage" >/dev/null 2>&1 || true
  fi
  if [ "$status" -eq 0 ]; then log "done"; else log "FAILED (exit $status)"; fi
}
trap cleanup EXIT

log "start $day $ts"
command -v docker >/dev/null || { log "docker not found"; exit 1; }
command -v age >/dev/null || { log "age not found (brew install age)"; exit 1; }
if [ "$DRY_RUN" = 1 ]; then
  log "dry run: everything but the upload"
else
  command -v "$CF" >/dev/null || { log "upload command not found: $CF (set CF_WRAPPER, or put cf on the PATH)"; exit 1; }
fi
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
app_id=$(docker compose ps --all --quiet app | head -n 1)
[ -n "$app_id" ] || { log "snapshot worked but the app container is gone"; exit 1; }

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
# The app is up (the snapshot just worked). Not paired yet (or WhatsApp
# removed): nothing to keep, and that's fine. Any answer other than a clear
# yes or no fails the night.
wa_file=""
wa_auth_file=""
# shellcheck disable=SC2016 # expands inside the helper's shell
wa_dirs=$(helper ro sh -c 'for d in wa wa-auth; do if [ -d "/data/$d" ]; then echo "$d=yes"; else echo "$d=no"; fi; done') || {
  log "couldn't check /data/wa and /data/wa-auth through the helper container"
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
  # shellcheck disable=SC2016
  store_names=$(helper ro sh -c 'for f in /data/wa/*.db; do if [ -f "$f" ]; then basename "$f"; fi; done') || {
    log "couldn't list the WhatsApp stores in /data/wa"
    exit 1
  }
  stores=""
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    # stdin from /dev/null, or exec would eat the rest of this loop's input.
    docker compose exec -T app palui snapshot "/data/wa/$name" "$stage/wa/$name.gz" >/dev/null </dev/null || {
      log "couldn't snapshot /data/wa/$name"
      exit 1
    }
    stores="$stores $name"
  done <<<"$(printf '%s' "$store_names" | tr -d '\r')"
  if [ -n "$stores" ]; then
    wa_file="$tmpdir/wa-$ts.tar.age"
    # The .gz snapshots are compressed already, so a plain tar.
    helper ro tar -cf - -C "$stage" wa | age -R "$RECIPIENT_FILE" -o "$wa_file"
    log "WhatsApp stores:$stores"
  else
    log "/data/wa has no store, skipping the WhatsApp archive"
  fi
  helper rw rm -rf "$stage" || log "couldn't remove $stage (cleanup tries again)"
else
  log "no /data/wa in the volume, skipping the WhatsApp archive"
fi

if [ "$has_wa_auth" = yes ]; then
  # The old Baileys layout. Baileys rewrote its key files atomically and a
  # restore re-syncs, so a fuzzy copy is acceptable. It stays until the owner
  # deletes /data/wa-auth after pairing WhatsApp again on the Go server.
  wa_auth_file="$tmpdir/wa-auth-$ts.tar.gz.age"
  helper ro tar -czf - -C /data wa-auth | age -R "$RECIPIENT_FILE" -o "$wa_auth_file"
  log "archived /data/wa-auth (the old Baileys session)"
fi

if [ "$DRY_RUN" = 1 ]; then
  for f in "$db_file" "$wa_file" "$wa_auth_file"; do
    [ -n "$f" ] || continue
    log "dry run: would upload r2://$BUCKET/palui/$day/$(basename "$f") ($(stat -f %z "$f") bytes)"
    if [ -n "$DRY_RUN_DIR" ]; then
      mkdir -p "$DRY_RUN_DIR"
      cp "$f" "$DRY_RUN_DIR/"
    fi
  done
  [ -z "$DRY_RUN_DIR" ] || log "dry run: kept the encrypted files in $DRY_RUN_DIR"
  exit 0
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
