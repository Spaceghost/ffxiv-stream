#!/bin/bash
# Run by GoW's base-app entrypoint inside sway as the retro user.
set -e
source /opt/gow/bash-lib/utils.sh

# XIVLauncher keeps the login through libsecret: a keyring with an empty
# password, as in ffxiv-stream's own sessions.
mkdir -p "$HOME/.local/share/keyrings"
[ -e "$HOME/.local/share/keyrings/login.keyring" ] || printf '[keyring]\ndisplay-name=login\nctime=0\nmtime=0\nlock-on-idle=false\nlock-after=false\n' > "$HOME/.local/share/keyrings/login.keyring"
chmod 600 "$HOME/.local/share/keyrings/login.keyring"
printf login > "$HOME/.local/share/keyrings/default"
eval "$(dbus-launch --sh-syntax)"
printf '' | gnome-keyring-daemon --replace --daemonize --unlock --components=secrets >/dev/null

gow_log "Starting XIVLauncher"
export DXVK_FRAME_RATE=${DXVK_FRAME_RATE:-60}
exec /opt/xivlauncher/XIVLauncher.Core
