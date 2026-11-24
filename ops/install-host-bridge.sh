#!/bin/bash
# Install (or reinstall) the Palui host bridge on the Docker host. macOS only
# (launchd).
#   - builds apps/host-bridge into one file and copies it to
#     $PALUI_HOST_DIR/bin/palui-host-bridge.mjs, so it doesn't depend on a checkout
#   - creates ~/.config/palui/host-bridge.json with a fresh token on first run
#   - renders ops/palui-host-bridge.plist into ~/Library/LaunchAgents as
#     $PALUI_LAUNCHD_PREFIX-host-bridge,
#     (re)loads it with launchctl and checks the bridge answers
# Rerun after upgrading Node: the LaunchAgent pins the node binary found now.
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
PALUI_LAUNCHD_PREFIX="${PALUI_LAUNCHD_PREFIX:-palui}"

repo="$(cd "$(dirname "$0")/.." && pwd)"
label="$PALUI_LAUNCHD_PREFIX-host-bridge"
plist="$HOME/Library/LaunchAgents/$label.plist"
bundle="$PALUI_HOST_DIR/bin/palui-host-bridge.mjs"
config="$HOME/.config/palui/host-bridge.json"
log="$PALUI_HOST_DIR/palui-host-bridge.log"
domain="gui/$(id -u)"
restart="launchctl kickstart -k $domain/$label"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "error: this installer uses launchd, so it only runs on macOS" >&2
  exit 1
fi
node_bin="$(command -v node || true)"
if [ -z "$node_bin" ]; then
  echo "error: node isn't on PATH. Install Node 24 or newer, then rerun this script." >&2
  exit 1
fi

(cd "$repo" && pnpm --filter @palui/host-bridge build >/dev/null)
mkdir -p "$PALUI_HOST_DIR/bin" "$HOME/Library/LaunchAgents"
install -m 755 "$repo/apps/host-bridge/dist/index.mjs" "$bundle"

if [ ! -f "$config" ]; then
  echo
  "$node_bin" "$bundle" init
  echo
fi
sed -e "s|__LABEL__|$label|g" -e "s|__HOST_DIR__|$PALUI_HOST_DIR|g" -e "s|__NODE__|$node_bin|g" \
  "$repo/ops/palui-host-bridge.plist" >"$plist"
plutil -lint "$plist" >/dev/null

launchctl bootout "$domain/$label" 2>/dev/null || true
# bootout returns before launchd has removed the job, and a bootstrap in that
# window fails with "Bootstrap failed: 5: Input/output error". Wait up to 10s.
for _ in $(seq 1 50); do
  launchctl print "$domain/$label" >/dev/null 2>&1 || break
  sleep 0.2
done
launchctl bootstrap "$domain" "$plist"

echo
echo "Installed $label"
echo "  node:   $node_bin (rerun this script after upgrading Node)"
echo "  config: $config (the token is its \"token\" field)"
echo "  log:    $log"
if "$node_bin" "$bundle" status; then
  echo
  echo "Next:"
  echo "  1. In Palui: Connectors > Add connector > MCP server > Host commands, paste the token, Connect."
  echo "  2. Press Load tools, check the classes, then Save tool settings."
  echo "After editing the config:"
  echo "  $node_bin $bundle check && $restart"
else
  echo
  "$node_bin" "$bundle" check >&2 || true
  echo "Fix it, then restart with:" >&2
  echo "  $restart" >&2
  echo "Last log lines:" >&2
  tail -n 5 "$log" >&2 2>/dev/null || true
  exit 1
fi
