#!/bin/bash
# Install (or reinstall) the Palui host bridge on the Docker host.
#   - builds apps/host-bridge into one file and copies it to
#     ~/selfhost/bin/palui-host-bridge.mjs, so it doesn't depend on a checkout
#   - creates ~/.config/palui/host-bridge.json with a fresh token on first run
#     (prints the token once; it's also in that file)
#   - renders ops/com.zakiego.palui-host-bridge.plist into ~/Library/LaunchAgents
#     and (re)loads it with launchctl
# After editing the allowlist, check it and restart:
#   node ~/selfhost/bin/palui-host-bridge.mjs check
#   launchctl kickstart -k gui/$(id -u)/com.zakiego.palui-host-bridge
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
label=com.zakiego.palui-host-bridge
plist="$HOME/Library/LaunchAgents/$label.plist"
bundle="$HOME/selfhost/bin/palui-host-bridge.mjs"
domain="gui/$(id -u)"
node_bin="$(command -v node)"

(cd "$repo" && pnpm --filter @palui/host-bridge build)
mkdir -p "$HOME/selfhost/bin" "$HOME/Library/LaunchAgents"
install -m 755 "$repo/apps/host-bridge/dist/index.mjs" "$bundle"

if [ ! -f "$HOME/.config/palui/host-bridge.json" ]; then
  "$node_bin" "$bundle" init
fi
"$node_bin" "$bundle" check || echo "fix the warnings above, then rerun the kickstart command" >&2

sed -e "s|__HOME__|$HOME|g" -e "s|__NODE__|$node_bin|g" "$repo/ops/$label.plist" > "$plist"
plutil -lint "$plist" >/dev/null

launchctl bootout "$domain/$label" 2>/dev/null || true
launchctl bootstrap "$domain" "$plist"
echo "installed $label: http://127.0.0.1:4893/mcp, log ~/selfhost/palui-host-bridge.log"
echo "In Palui: add an MCP server connector with the Host commands preset."
