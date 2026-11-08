#!/bin/sh
# One-step installer for Linux.
#   curl -fsSL https://github.com/Aryan-mor/jellyfin-telegram/releases/latest/download/install.sh | sh
# It downloads the latest build into ~/jellyfin-telegram, makes it start at login (systemd user
# service) and prints the setup address. Run it again to update; settings and library are kept.
set -eu

repo="Aryan-mor/jellyfin-telegram"
dir="${JFTG_DIR:-$HOME/jellyfin-telegram}"

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "Unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$dir"
echo "Downloading jellyfin-telegram..."
tmp="$(mktemp)"
curl -fsSL "https://github.com/$repo/releases/latest/download/jellyfin-telegram-linux-$arch.tar.gz" -o "$tmp"
systemctl --user stop jellyfin-telegram 2>/dev/null || true
tar xzf "$tmp" -C "$dir"
rm -f "$tmp"
chmod +x "$dir/jellyfin-telegram"

# A launcher in the applications menu and on the Desktop that opens the panel.
desktop="[Desktop Entry]
Type=Application
Name=jellyfin-telegram panel
Comment=Open the jellyfin-telegram panel
Exec=xdg-open http://127.0.0.1:8097/panel
Terminal=false
Categories=AudioVideo;"
mkdir -p "$HOME/.local/share/applications"
printf '%s\n' "$desktop" > "$HOME/.local/share/applications/jellyfin-telegram.desktop"
desk="$(xdg-user-dir DESKTOP 2>/dev/null || echo "$HOME/Desktop")"
if [ -d "$desk" ]; then
  printf '%s\n' "$desktop" > "$desk/jellyfin-telegram.desktop"
  chmod +x "$desk/jellyfin-telegram.desktop"
  command -v gio >/dev/null 2>&1 && gio set "$desk/jellyfin-telegram.desktop" metadata::trusted true 2>/dev/null || true
fi

if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
  unitdir="$HOME/.config/systemd/user"
  mkdir -p "$unitdir"
  cat > "$unitdir/jellyfin-telegram.service" <<EOF
[Unit]
Description=jellyfin-telegram
After=network-online.target

[Service]
WorkingDirectory=$dir
ExecStart=$dir/jellyfin-telegram serve
Restart=on-failure

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable --now jellyfin-telegram
  # Keep it running after logout and start it at boot.
  loginctl enable-linger "$USER" 2>/dev/null || true
  sleep 2
  echo
  echo "Installed and running. Open the setup address:"
  journalctl --user -u jellyfin-telegram -n 30 --no-pager 2>/dev/null | grep -o 'http://[^ ]*/setup?code=[^ ]*' | tail -n 1 || true
  echo "(If no address is shown: journalctl --user -u jellyfin-telegram -n 30)"
else
  echo "systemd is not available for your user, starting it in this terminal."
  cd "$dir" && exec ./jellyfin-telegram
fi
