#!/usr/bin/env bash
set -e

# Setup pkg-config path for webkit2gtk-4.1 compatibility on Ubuntu/Mint
export PKG_CONFIG_PATH="$(pwd)/pkgconfig:${PKG_CONFIG_PATH}"

# Run wails build
if command -v wails >/dev/null 2>&1; then
    wails build "$@"
elif [ -f "$HOME/go/bin/wails" ]; then
    "$HOME/go/bin/wails" build "$@"
else
    echo "Error: wails CLI not found in PATH or ~/go/bin/wails"
    exit 1
fi
