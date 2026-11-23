#!/bin/bash
# Install (or reinstall) the nightly Palui backup on a macOS Docker host.
#   - copies scripts/backup.sh to $PALUI_HOST_DIR/bin/palui-backup.sh, so the
#     job doesn't depend on any checkout staying in place
#   - renders ops/palui-backup.plist into ~/Library/LaunchAgents as
#     $PALUI_LAUNCHD_PREFIX-backup
#   - (re)loads it with launchctl
# Rerun after changing either file. scripts/pre-deploy.sh reruns it when
# backup.sh changes. Run the job once now with:
#   launchctl kickstart gui/$(id -u)/<prefix>-backup
#
# Before the first run: install age, put the age public key in
# ~/.config/palui/age-recipient, and the identity in the Keychain (see the
# backups docs page).
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
PALUI_DIR="${PALUI_DIR:-$HOME/palui}"

repo="$(cd "$(dirname "$0")/.." && pwd)"
label="$PALUI_LAUNCHD_PREFIX-backup"
plist="$HOME/Library/LaunchAgents/$label.plist"
domain="gui/$(id -u)"

mkdir -p "$PALUI_HOST_DIR/bin" "$HOME/Library/LaunchAgents"
install -m 755 "$repo/scripts/backup.sh" "$PALUI_HOST_DIR/bin/palui-backup.sh"
sed -e "s|__LABEL__|$label|g" -e "s|__HOST_DIR__|$PALUI_HOST_DIR|g" -e "s|__PALUI_DIR__|$PALUI_DIR|g" \
  "$repo/ops/palui-backup.plist" >"$plist"
plutil -lint "$plist" >/dev/null

launchctl bootout "$domain/$label" 2>/dev/null || true
launchctl bootstrap "$domain" "$plist"
echo "installed $label: daily at 03:30, log $PALUI_HOST_DIR/palui-backup.log"
