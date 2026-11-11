#!/bin/bash
# Install (or reinstall) the nightly Palui backup on the Docker host.
#   - copies scripts/backup.sh to ~/selfhost/bin/palui-backup.sh, so the job
#     doesn't depend on any checkout staying in place
#   - renders ops/com.zakiego.palui-backup.plist into ~/Library/LaunchAgents
#   - (re)loads it with launchctl
# Rerun after changing either file. scripts/pre-deploy.sh reruns it when
# backup.sh changes. Run the job once now with:
#   launchctl kickstart gui/$(id -u)/com.zakiego.palui-backup
#
# Before the first run: `brew install age`, put the age public key in
# ~/.config/palui/age-recipient, and the identity in the Keychain (see the
# backups docs page).
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
label=com.zakiego.palui-backup
plist="$HOME/Library/LaunchAgents/$label.plist"
domain="gui/$(id -u)"

mkdir -p "$HOME/selfhost/bin" "$HOME/Library/LaunchAgents"
install -m 755 "$repo/scripts/backup.sh" "$HOME/selfhost/bin/palui-backup.sh"
sed "s|__HOME__|$HOME|g" "$repo/ops/$label.plist" > "$plist"
plutil -lint "$plist" >/dev/null

launchctl bootout "$domain/$label" 2>/dev/null || true
launchctl bootstrap "$domain" "$plist"
echo "installed $label: daily at 03:30, log ~/selfhost/palui-backup.log"
