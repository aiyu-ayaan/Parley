#!/bin/sh
# One-line installer: downloads the newest Parley release tarball and runs its install.sh.
#
#   curl -fsSL https://raw.githubusercontent.com/aiyu-ayaan/Whatsweb/master/scripts/install-linux.sh | sh
#   ... | sh -s -- --channel beta     newest beta, else alpha
#   ... | sh -s -- --channel alpha    newest alpha
#   ... | sh -s -- --uninstall        remove it; account data is kept
#
# Default channel is stable; if no stable release exists it falls back to
# beta, then alpha.
set -eu

REPO=aiyu-ayaan/Whatsweb
channel=stable
while [ $# -gt 0 ]; do
    case "$1" in
        --channel) channel="${2:-}"; shift 2 ;;
        --uninstall) uninstall=1; shift ;;
        *) echo "unknown option: $1" >&2; exit 1 ;;
    esac
done
case "$channel" in stable|beta|alpha) ;; *) echo "channel must be stable, beta or alpha" >&2; exit 1 ;; esac

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT

# Newest-first list of tarball URLs (the API lists pre-releases too).
curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=50" \
    | grep -o '"browser_download_url": *"[^"]*linux-amd64\.tar\.gz"' | cut -d'"' -f4 > "$tmp/urls"
[ -s "$tmp/urls" ] || { echo "no Parley release found for $REPO" >&2; exit 1; }

pick() { # $1 = channel; prints newest URL of that channel
    case "$1" in
        stable) grep -v -e '-alpha' -e '-beta' "$tmp/urls" ;;
        *) grep -e "-$1" "$tmp/urls" ;;
    esac | head -n1
}

case "$channel" in
    stable) order="stable beta alpha" ;;
    beta) order="beta alpha" ;;
    alpha) order="alpha" ;;
esac
url=
for c in $order; do
    url=$(pick "$c")
    if [ -n "$url" ]; then
        [ "$c" = "$channel" ] || echo "No $channel release yet; using $c."
        break
    fi
done
[ -n "$url" ] || { echo "no release found for channel '$channel'" >&2; exit 1; }

echo "Downloading $url"
curl -fL "$url" | tar -xzf - -C "$tmp"
if [ "${uninstall:-}" = 1 ]; then "$tmp/install.sh" --uninstall; else "$tmp/install.sh"; fi
