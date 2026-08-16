#!/usr/bin/env bash
# scripts/install.sh
# Purpose: Install Helix as a system shell, initialize config directories,
# and optionally bootstrap local AI runtimes (Ollama).
#
# Robustness rules (learned from the v1.0.0 install incident):
#   1. Interactive prompts run ONLY when stdin is a TTY ([ -t 0 ]).
#   2. Prompt reads are wrapped with `|| true` so EOF/decline can NEVER
#      abort the install under `set -e` (sudo reads /dev/tty directly,
#      which is why sudo survived while `read` saw EOF in piped contexts).
#   3. Every optional stage degrades gracefully; only real install steps
#      are allowed to fail the run.
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

# 2. Install binary (real step — may fail)
echo "Installing binary to $TARGET_BINARY..."
sudo cp "./dist/$BINARY_NAME" "$TARGET_BINARY"
sudo chmod +x "$TARGET_BINARY"

# 3. Create config directories
echo "Initializing Helix home at $HELIX_HOME..."
mkdir -p "$HELIX_HOME/models"
mkdir -p "$HELIX_HOME/rag_index"
mkdir -p "$HELIX_HOME/vector_index"
mkdir -p "$HELIX_HOME/man_index"

# FIX: Detect and save the underlying shell preference BEFORE chsh changes it.
# This ensures Helix always knows to use zsh/bash for child processes,
# even if Helix becomes the default login shell.
UNDERLYING_SHELL="$SHELL"
if [[ "$UNDERLYING_SHELL" == *"helix"* ]] || [[ -z "$UNDERLYING_SHELL" ]]; then
    if [ -f "/bin/zsh" ]; then UNDERLYING_SHELL="/bin/zsh"
    elif [ -f "/bin/bash" ]; then UNDERLYING_SHELL="/bin/bash"
    else UNDERLYING_SHELL="/bin/sh"; fi
fi
echo "$UNDERLYING_SHELL" > "$HELIX_HOME/shell_pref"
echo "🧠 Saved underlying shell preference: $UNDERLYING_SHELL"

# 4. Optional Ollama bootstrap — interactive ONLY, never fatal.
echo ""
echo "AI Runtime Bootstrapping"
install_ollama=""
if [ -t 0 ]; then
    read -r -p "Install Ollama for local AI inference? (y/N): " install_ollama || true
else
    echo "   (non-interactive stdin detected — skipping Ollama bootstrap)"
fi
if [[ "$install_ollama" =~ ^[Yy]$ ]]; then
    if ! command -v ollama &> /dev/null; then
        echo "Installing Ollama..."
        if [[ "$OSTYPE" == "darwin"* ]]; then
            brew install ollama || echo "⚠️  Ollama install failed; continuing."
        else
            curl -fsSL https://ollama.com/install.sh | sh || echo "⚠️  Ollama install failed; continuing."
        fi
        echo "Starting Ollama service..."
        ollama serve &> /dev/null &
        sleep 2
        echo "Pulling default model (gemma4:e2b)..."
        ollama pull gemma4:e2b || echo "⚠️  Model pull failed; you can pull later."
    else
        echo "Ollama is already installed."
    fi
else
    echo "   Skipping Ollama bootstrap."
fi

# 5. Register in /etc/shells (Unix-like only)
if [[ "$OSTYPE" != "msys" && "$OSTYPE" != "cygwin" && "$OSTYPE" != "win32" ]]; then
    if ! grep -q "$TARGET_BINARY" /etc/shells; then
        echo "Registering $TARGET_BINARY in /etc/shells..."
        echo "$TARGET_BINARY" | sudo tee -a /etc/shells > /dev/null
    else
        echo "Already registered in /etc/shells"
    fi

    confirm=""
    if [ -t 0 ]; then
        read -r -p "Set Helix as your default login shell? (y/N): " confirm || true
    else
        echo "   (non-interactive stdin detected — skipping chsh)"
    fi
    if [[ "$confirm" =~ ^[Yy]$ ]]; then
        echo "Changing default shell (requires password)..."
        chsh -s "$TARGET_BINARY"
        echo "Helix is now your default shell! Restart your terminal to apply."
    fi
else
    echo "⚠️ Windows detected. Add $TARGET_BINARY to your terminal emulator's shell settings manually."
fi

echo ""
echo "⚡ Installation complete! Run 'helix' to start."