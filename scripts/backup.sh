#!/bin/bash
# Nightly encrypted backup of Palui to Cloudflare R2 (Zakiego Lab).
#
#   1. snapshot /data/palui.db inside the running app container with the bundled
#      admin CLI (online backup API, gzipped), copy it out, check it, encrypt it
#   2. stream a tar of the WhatsApp auth dir (/data/wa-auth, written by the
#      runtime, read through the app container on the same volume) straight
#      into age, so the plaintext never touches the host disk
#   3. upload both to r2://palui-backups/palui/YYYY-MM-DD/ and check each size
#   4. delete the snapshot in the container and every local temp file
#
# Both files are encrypted with age to the public key in
# ~/.config/palui/age-recipient. The matching identity (private key) lives only
# in the Keychain, never on disk and never in this repo; restore-drill.sh reads
# it from there.
#
# Why wa-auth is encrypted too: the auth dir alone is enough to take over the
# bot's WhatsApp number as a linked device. Treat that archive like a password.
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
wa_key="palui/$day/wa-auth-$ts.tar.gz.age"
remote="/data/backups/$db_name"
tmpdir=""

cleanup() {
  status=$?
  [ -n "$tmpdir" ] && rm -rf "$tmpdir"
  # shellcheck disable=SC2016 # $1 expands inside the container's shell
  (cd "$PALUI_DIR" && docker compose exec -T app sh -c 'rm -f "$1" "$1".tmp-*' _ "$remote") >/dev/null 2>&1 || true
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

snapshot() {
  docker compose exec -T app node .output/server/cli.mjs backup "$remote"
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

# --- WhatsApp auth ----------------------------------------------------------
# Read through the app container: it's up (the snapshot just worked) and
# mounts the same volume, so a stopped runtime can't turn into "not paired".
# Not paired yet (or WhatsApp removed): nothing to keep, and that's fine. Any
# answer other than a clear yes or no fails the night.
wa_file=""
wa_present=$(docker compose exec -T app sh -c 'if [ -d /data/wa-auth ]; then echo yes; else echo no; fi') || {
  log "couldn't check /data/wa-auth through the app container"
  exit 1
}
case "$wa_present" in
  *yes)
    wa_file="$tmpdir/wa-auth-$ts.tar.gz.age"
    # Streamed: tar runs in the container, age on the host, the plaintext only
    # ever exists in the pipe. Baileys may write mid-tar; it rewrites its key
    # files atomically and a restore re-syncs, so a fuzzy copy is acceptable.
    docker compose exec -T app tar -czf - -C /data wa-auth | age -R "$RECIPIENT_FILE" -o "$wa_file"
    ;;
  *no)
    log "no /data/wa-auth in the volume, skipping the WhatsApp auth archive"
    ;;
  *)
    log "unexpected answer checking /data/wa-auth: $wa_present"
    exit 1
    ;;
esac

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
