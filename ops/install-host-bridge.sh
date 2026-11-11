#!/bin/bash
# Install (or reinstall) the Palui host bridge on the Docker host. macOS only
# (launchd).
#   - builds apps/host-bridge into one file and copies it to
#     ~/selfhost/bin/palui-host-bridge.mjs, so it doesn't depend on a checkout
#   - creates ~/.config/palui/host-bridge.json with a fresh token on first run
#   - renders ops/com.zakiego.palui-host-bridge.plist into ~/Library/LaunchAgents,
#     (re)loads it with launchctl and checks the bridge answers
# Rerun after upgrading Node: the LaunchAgent pins the node binary found now.
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
label=com.zakiego.palui-host-bridge
plist="$HOME/Library/LaunchAgents/$label.plist"
bundle="$HOME/selfhost/bin/palui-host-bridge.mjs"
config="$HOME/.config/palui/host-bridge.json"
log="$HOME/selfhost/palui-host-bridge.log"
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
mkdir -p "$HOME/selfhost/bin" "$HOME/Library/LaunchAgents"
install -m 755 "$repo/apps/host-bridge/dist/index.mjs" "$bundle"

if [ ! -f "$config" ]; then
  echo
  "$node_bin" "$bundle" init
  echo
fi
config_ok=1
"$node_bin" "$bundle" check >/dev/null || config_ok=0

sed -e "s|__HOME__|$HOME|g" -e "s|__NODE__|$node_bin|g" "$repo/ops/$label.plist" > "$plist"
plutil -lint "$plist" >/dev/null

launchctl bootout "$domain/$label" 2>/dev/null || true
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
  if [ "$config_ok" = 0 ]; then
    echo "The config has problems. See them with:" >&2
    echo "  $node_bin $bundle check" >&2
  fi
  echo "Fix it, then restart with:" >&2
  echo "  $restart" >&2
  echo "Last log lines:" >&2
  tail -n 5 "$log" >&2 2>/dev/null || true
  exit 1
fi
