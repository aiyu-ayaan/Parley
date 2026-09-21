#!/usr/bin/env bash
# Installs Parley for the current user: binary, icon and app-menu entry.
# Works from a release tarball (parley + appicon.png next to this script) or
# from the source tree (builds with ./build.sh when there is no binary yet).
#
#   ./install.sh              install into ~/.local
#   ./install.sh --uninstall  remove it again (profiles are kept)
set -euo pipefail

cd "$(dirname "$0")"
PREFIX="${PREFIX:-$HOME/.local}"
BIN="$PREFIX/bin/parley"
ICON="$PREFIX/share/icons/hicolor/256x256/apps/parley.png"
DESKTOP="$PREFIX/share/applications/parley.desktop"

refresh() {
    command -v gtk-update-icon-cache >/dev/null && gtk-update-icon-cache -qft "$PREFIX/share/icons/hicolor" 2>/dev/null || true
    command -v update-desktop-database >/dev/null && update-desktop-database -q "$PREFIX/share/applications" 2>/dev/null || true
}

if [ "${1:-}" = --uninstall ]; then
    rm -f "$BIN" "$ICON" "$DESKTOP" "$HOME/.config/autostart/parley.desktop"
    refresh
    echo "Parley removed. Profiles are still in ~/.parley."
    exit 0
fi

if [ -f parley ]; then
    src=parley
else
    [ -f build/bin/parley ] || ./build.sh -tags webkit2_41 -trimpath
    src=build/bin/parley
fi
[ -f appicon.png ] || { echo "appicon.png not found next to install.sh" >&2; exit 1; }

install -Dm755 "$src" "$BIN"
install -Dm644 appicon.png "$ICON"
mkdir -p "$(dirname "$DESKTOP")"
cat > "$DESKTOP" <<EOF
[Desktop Entry]
Type=Application
Name=Parley
Comment=WhatsApp Web Desktop Application
Exec=$BIN
Icon=parley
Terminal=false
Categories=Network;InstantMessaging;
StartupNotify=true
StartupWMClass=parley
EOF
refresh

echo "Installed Parley to $BIN"
case ":$PATH:" in *":$PREFIX/bin:"*) ;; *) echo "Note: $PREFIX/bin is not on your PATH; launch Parley from the app menu." ;; esac
