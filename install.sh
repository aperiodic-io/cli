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

INSTALL_DIR="/usr/local/bin"

echo "Detected OS: $OS, Architecture: $ARCH"
echo "Downloading Aperiodic CLI from $URL..."

TMP_FILE=$(mktemp)
if ! curl -L -o "$TMP_FILE" "$URL"; then
    echo "Error: Failed to download binary. Please check the repository and version."
    rm -f "$TMP_FILE"
    exit 1
fi

chmod +x "$TMP_FILE"

if [ -w "$INSTALL_DIR" ]; then
    mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
else
    sudo mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
fi

echo "Installation complete. Run 'aperiodic' to get started."
