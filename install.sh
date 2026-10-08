#!/bin/sh
set -eu

REPO="AmrUser-48/agy-open"
INSTALL_DIR="${AGY_INSTALL_DIR:-$HOME/.local/bin}"
URL="https://github.com/${REPO}/releases/latest/download/agy"

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux) ;;
  *)
    echo "agy-open: unsupported OS: $OS (Linux x86_64 is currently supported)" >&2
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64|amd64) ;;
  *)
    echo "agy-open: unsupported architecture: $ARCH (x86_64 is currently supported)" >&2
    exit 1
    ;;
esac

mkdir -p "$INSTALL_DIR"
tmp="$(mktemp "${TMPDIR:-/tmp}/agy.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

echo "Downloading latest agy-open release..."
curl -fL --retry 3 "$URL" -o "$tmp"
chmod 0755 "$tmp"
mv "$tmp" "$INSTALL_DIR/agy"
trap - EXIT

echo "Installed $INSTALL_DIR/agy"

case ":$PATH:" in
  *:"$INSTALL_DIR":*) ;;
  *)
    echo
    echo "Add this to your shell profile if $INSTALL_DIR is not already on PATH:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

"$INSTALL_DIR/agy" --version
