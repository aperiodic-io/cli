#!/bin/bash
set -e

# Aperiodic CLI Installation Script
# This script downloads the appropriate binary for your OS and architecture.

REPO="aperiodic-io/cli"
VERSION=${1:-latest}

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case $OS in
    linux) OS="linux" ;;
    darwin) OS="darwin" ;;
    msys*|mingw*) OS="windows" ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

BINARY_NAME="aperiodic"
EXT=""
if [ "$OS" == "windows" ]; then
    BINARY_NAME="aperiodic.exe"
    EXT=".exe"
fi

if [ "$VERSION" == "latest" ]; then
    URL="https://github.com/$REPO/releases/latest/download/aperiodic-$OS-$ARCH$EXT"
else
    URL="https://github.com/$REPO/releases/download/$VERSION/aperiodic-$OS-$ARCH$EXT"
fi

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

echo "Detected OS: $OS, Architecture: $ARCH"
echo "Downloading Aperiodic CLI from $URL..."

TMP_FILE=$(mktemp)
cleanup() { rm -f "$TMP_FILE"; }
trap cleanup EXIT

# -f makes curl exit non-zero on 404 instead of writing the error body to disk.
if ! curl -fL -o "$TMP_FILE" "$URL"; then
    echo "Error: Failed to download binary from $URL" >&2
    echo "The release may not exist, or it may not have assets for $OS-$ARCH." >&2
    echo "See https://github.com/$REPO/releases for available versions." >&2
    exit 1
fi

# Guard against a truncated or non-binary download slipping through.
if [ ! -s "$TMP_FILE" ]; then
    echo "Error: Downloaded file is empty. Aborting." >&2
    exit 1
fi

if head -c 1024 "$TMP_FILE" | LC_ALL=C grep -qi '<!doctype\|<html\|^Not Found'; then
    echo "Error: Download did not return a binary (got an HTML or error page)." >&2
    echo "URL: $URL" >&2
    exit 1
fi

chmod +x "$TMP_FILE"

if [ -w "$INSTALL_DIR" ]; then
    mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
else
    sudo mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
fi
trap - EXIT

echo "Installation complete. Run 'aperiodic' to get started."
