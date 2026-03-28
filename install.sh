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

echo "Detected OS: $OS, Architecture: $ARCH"
echo "Downloading Aperiodic CLI from $URL..."

if ! curl -L -o "$BINARY_NAME" "$URL"; then
    echo "Error: Failed to download binary. Please check the repository and version."
    exit 1
fi

chmod +x "$BINARY_NAME"

echo "Installation complete. You can now run ./$BINARY_NAME"
