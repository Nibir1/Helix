#!/usr/bin/env bash
# scripts/install.sh
# Purpose: Install Helix as a system shell, initialize config directories,
# and optionally bootstrap local AI runtimes (Ollama).
set -e

BINARY_NAME="helix"
INSTALL_DIR="/usr/local/bin"
TARGET_BINARY="$INSTALL_DIR/$BINARY_NAME"
HELIX_HOME="$HOME/.helix"

echo "⚡ Helix Shell Installer"
echo "────────────────────────────────────────"

# 1. Build or locate binary
if [ ! -f "./dist/$BINARY_NAME" ]; then
    echo "Building Helix from source..."
    make current
fi

# 2. Install binary
echo "Installing binary to $TARGET_BINARY..."
sudo cp "./dist/$BINARY_NAME" "$TARGET_BINARY"
sudo chmod +x "$TARGET_BINARY"

# 3. Create config directories
echo "Initializing Helix home at $HELIX_HOME..."
mkdir -p "$HELIX_HOME/models"
mkdir -p "$HELIX_HOME/rag_index"
mkdir -p "$HELIX_HOME/vector_index"
mkdir -p "$HELIX_HOME/man_index"

# 4. Optional Bootstrapping
echo ""
echo "AI Runtime Bootstrapping"
read -p "Install Ollama for local AI inference? (y/N): " install_ollama
if [[ "$install_ollama" =~ ^[Yy]$ ]]; then
    if ! command -v ollama &> /dev/null; then
        echo "Installing Ollama..."
        if [[ "$OSTYPE" == "darwin"* ]]; then
            brew install ollama
        else
            curl -fsSL https://ollama.com/install.sh | sh
        fi
        echo "Starting Ollama service..."
        ollama serve &> /dev/null &
        sleep 2
        echo "Pulling default model (phi4-mini)..."
        ollama pull phi4-mini
    else
        echo "Ollama is already installed."
    fi
fi

# 5. Register in /etc/shells (Unix-like only)
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
fi

echo ""
echo "⚡ Installation complete! Run 'helix' to start."