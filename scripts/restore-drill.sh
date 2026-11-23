#!/bin/bash
# Restore drill: prove last night's backup can actually be restored.
#
#   scripts/restore-drill.sh                 newest palui-*.db.gz.age in R2 (last 7 days)
#   scripts/restore-drill.sh <file>          a local .db.gz.age, .db.gz or .db
#
#   1. fetch the backup (or take the local file) into a private temp dir
#   2. decrypt it with age, using the identity from the Keychain
#      (`security find-generic-password -w`, never written to disk)
#   3. restore it into a scratch compose project, `palui-drill`, with its own
#      volume, and start the server as `palui serve --no-runtime` (plus
#      PALUI_NO_RUNTIME=1): HTTP only, no connectors, engine, scheduler or
#      outbox. The Go server is one process, so a plain `palui serve` would
#      start the real connectors and open a second Telegram poller and a
#      second WhatsApp session against the owner's accounts. As a second
#      guard the container has no network at all (`network_mode: none`), so
#      every check below runs inside it.
#   4. wait for /health to answer 200 and check it reports the runtime off
#      ("runtime": "unreachable"; anything else fails at once; read with
#      `palui healthcheck --print`, since the image has no shell or wget), then run
#      `palui secrets-check --require-some`, which decrypts every connector
#      and hook secret with MASTER_KEY and exits non-zero on any failure, or
#      when there was nothing to decrypt (an empty DB proves nothing)
#   5. tear the drill project down with its volume, whatever happened
#
# Prints PASS or FAIL as the last line and exits 0 or 1.
#
# Needs: docker, age, MASTER_KEY in the environment (the production key: the
# point is to prove the backup and the key still go together), and the Go
# image already built on this host (palui:latest, or PALUI_IMAGE), new enough
# to have `palui healthcheck --print`. Copying the database into the volume
# runs in a pinned alpine helper (the app image is distroless).
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

BUCKET="${BACKUP_BUCKET:-palui-backups}"
CF="${CF_WRAPPER:-cf}"
KEYCHAIN_SERVICE="${AGE_KEYCHAIN_SERVICE:-palui-backup-age-identity}"
IMAGE="${PALUI_IMAGE:-palui:latest}"
PROJECT="palui-drill"
HEALTH_TIMEOUT="${DRILL_HEALTH_TIMEOUT:-120}"
LOOKBACK_DAYS=7
# Same pin as scripts/backup.sh and scripts/pre-deploy.sh.
HELPER_IMAGE="${PALUI_HELPER_IMAGE:-alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf}"

export PATH="/usr/local/bin:/opt/homebrew/bin:$PATH"
for d in "$HOME"/.nvm/versions/node/*/bin; do
  [ -x "$d/cf" ] && CF_NODE_BIN="$d"
done
[ -n "${CF_NODE_BIN:-}" ] && export PATH="$CF_NODE_BIN:$PATH"

log() { echo "[restore-drill] $*"; }

tmpdir=""
compose_file=""
drill() { docker compose -p "$PROJECT" -f "$compose_file" "$@"; }

finish() {
  status=$?
  if [ -n "$compose_file" ] && [ -f "$compose_file" ]; then
    log "tearing down $PROJECT"
    drill down -v --remove-orphans >/dev/null 2>&1 || log "WARNING: teardown failed, run: docker compose -p $PROJECT down -v"
  fi
  [ -n "$tmpdir" ] && rm -rf "$tmpdir"
  if [ "$status" -eq 0 ]; then echo "PASS"; else echo "FAIL"; fi
}
trap finish EXIT

command -v docker >/dev/null || { log "docker not found"; exit 1; }
[ -n "${MASTER_KEY:-}" ] || { log "MASTER_KEY isn't set. Export the production key first (from your password manager)."; exit 1; }
docker image inspect "$IMAGE" >/dev/null 2>&1 || { log "image $IMAGE not found on this host (set PALUI_IMAGE)"; exit 1; }
# The old Node image has no --no-runtime; never run it here.
case "$(docker image inspect --format '{{json .Config.Cmd}}' "$IMAGE")" in
  *'"palui","serve"'*) ;;
  *) log "image $IMAGE isn't the Go server (its command isn't palui serve), refusing to start it"; exit 1 ;;
esac

tmpdir=$(mktemp -d /tmp/palui-drill.XXXXXX)
chmod 700 "$tmpdir"

# --- 1. get the backup --------------------------------------------------------
if [ $# -ge 1 ]; then
  src="$1"
  [ -f "$src" ] || { log "no such file: $src"; exit 1; }
  log "using local file $src"
else
  command -v "$CF" >/dev/null || { log "upload command not found: $CF (set CF_WRAPPER, or put cf on the PATH)"; exit 1; }
  command -v cf >/dev/null || { log "cf CLI not found"; exit 1; }
  key=""
  # Keys are palui/YYYY-MM-DD/palui-<ts>.db.gz.age, so walking back day by day
  # and taking the highest name finds the newest without paging the bucket.
  for i in $(seq 0 $((LOOKBACK_DAYS - 1))); do
    day=$(date -v-"${i}"d +%Y-%m-%d)
    key=$("$CF" r2 objects list --bucket-name "$BUCKET" --prefix "palui/$day/palui-" \
      | python3 -c 'import json,sys
d = json.load(sys.stdin)
items = d if isinstance(d, list) else d.get("objects", d.get("result", []))
keys = sorted(o.get("key", "") for o in items if o.get("key", "").endswith(".db.gz.age"))
print(keys[-1] if keys else "")')
    [ -n "$key" ] && break
  done
  [ -n "$key" ] || { log "no database backup in r2://$BUCKET/palui/ in the last $LOOKBACK_DAYS days"; exit 1; }
  src="$tmpdir/$(basename "$key")"
  log "fetching r2://$BUCKET/$key"
  "$CF" r2 objects get "$key" --bucket-name "$BUCKET" > "$src"
  log "fetched $(stat -f %z "$src") bytes"
fi

# --- 2. decrypt and unpack ----------------------------------------------------
work="$src"
case "$work" in
  *.age)
    command -v age >/dev/null || { log "age not found (brew install age)"; exit 1; }
    out="$tmpdir/$(basename "${work%.age}")"
    # The identity goes through a pipe, never a file.
    age -d -i <(security find-generic-password -s "$KEYCHAIN_SERVICE" -w) -o "$out" "$work"
    work="$out"
    log "decrypted"
    ;;
esac
case "$work" in
  *.gz)
    gzip -t "$work"
    gunzip -c "$work" > "$tmpdir/palui.db"
    work="$tmpdir/palui.db"
    ;;
  *.db) cp "$work" "$tmpdir/palui.db"; work="$tmpdir/palui.db" ;;
  *) log "don't know how to restore $work (want .db.gz.age, .db.gz or .db)"; exit 1 ;;
esac
log "database $(stat -f %z "$work") bytes"

# --- 3. scratch compose project -----------------------------------------------
# Its own file, not compose.yaml plus an override: overrides append ports and
# keep the network, so production's port and host access would come along.
# MASTER_KEY reaches the container from this process's environment and is
# never written into the file. No restart policy: a crash stays a crash.
compose_file="$tmpdir/compose.drill.yaml"
cat > "$compose_file" <<YAML
name: $PROJECT
services:
  app:
    image: $IMAGE
    pull_policy: never
    command: ["palui", "serve", "--no-runtime"]
    network_mode: none
    environment:
      NODE_ENV: production
      DATA_DIR: /data
      PALUI_NO_RUNTIME: "1"
      # A host name, not an IP: the passkey relying party id must be a domain.
      APP_URL: http://localhost:3000
      MASTER_KEY: \${MASTER_KEY:?MASTER_KEY is required}
    volumes:
      - data:/data
volumes:
  data:
YAML

log "restoring into volume ${PROJECT}_data"
drill create app >/dev/null
# Copy the file in as root, then hand it to whoever owns /data (the new volume
# took /data from the image, owner included).
# shellcheck disable=SC2016 # expands inside the helper's shell
docker run --rm --user root --network none \
  -v "${PROJECT}_data:/data" -v "$tmpdir:/restore:ro" "$HELPER_IMAGE" \
  sh -c 'cp /restore/palui.db /data/palui.db && chown "$(stat -c %u:%g /data)" /data/palui.db'

log "starting the server with the runtime off (no network)"
drill up -d app >/dev/null

# --- 4. checks ----------------------------------------------------------------
# Inside the container: it has no network, so the host can't reach it.
deadline=$((SECONDS + HEALTH_TIMEOUT))
until drill exec -T app palui healthcheck 2>/dev/null; do
  if [ "$SECONDS" -ge "$deadline" ]; then
    log "/health didn't answer 200 within ${HEALTH_TIMEOUT}s"
    drill logs --tail 50 app || true
    exit 1
  fi
  sleep 2
done
health=$(drill exec -T app palui healthcheck --print) || {
  log "couldn't read /health"
  exit 1
}
log "/health is green: $(printf '%s' "$health" | head -c 300)"
case "$health" in
  *'"runtime":"unreachable"'*) log "runtime is off, no connector started" ;;
  *)
    log "the runtime is NOT off in the drill container, stopping it now"
    exit 1
    ;;
esac

log "checking that every connector and hook secret decrypts"
if ! drill exec -T app palui secrets-check --require-some; then
  log "secrets-check failed: the backup has no secrets, or it and this MASTER_KEY don't go together"
  exit 1
fi
log "secrets-check passed"
