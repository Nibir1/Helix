#!/usr/bin/env bash
# scripts/install.sh - Install Helix as a system shell
set -e

BINARY_NAME="helix"
INSTALL_DIR="/usr/local/bin"
TARGET_BINARY="$INSTALL_DIR/$BINARY_NAME"

echo "⚡ Helix Shell Installer"

# Build first
echo "Building Helix..."
make current

# Install binary
echo "Installing binary to $TARGET_BINARY..."
sudo cp "./dist/$BINARY_NAME" "$TARGET_BINARY"
sudo chmod +x "$TARGET_BINARY"

# Register in /etc/shells (Unix-like only)
if [[ "$OSTYPE" != "msys" && "$OSTYPE" != "cygwin" && "$OSTYPE" != "win32" ]]; then
    if ! grep -q "$TARGET_BINARY" /etc/shells; then
        echo "Registering $TARGET_BINARY in /etc/shells..."
        echo "$TARGET_BINARY" | sudo tee -a /etc/shells > /dev/null
    else
        echo "Already registered in /etc/shells"
    fi

    read -p "Set Helix as your default login shell? (y/N): " confirm
    if [[ "$confirm" =~ ^[Yy]$ ]]; then
        echo "Changing default shell (requires password)..."
        chsh -s "$TARGET_BINARY"
        echo "Helix is now your default shell! Restart your terminal to apply."
    fi
else
    echo "⚠️ Windows detected. Add $TARGET_BINARY to your terminal emulator's shell settings manually."
fi

echo "Installation complete!"