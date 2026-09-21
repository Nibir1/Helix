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
#   4. NOTHING ASSUMES sudo EXISTS. (Learned on Windows: `make install` under
#      MSYS2/MINGW64 died at the `sudo cp` with "sudo: command not found",
#      exit 127, before it had installed anything at all. The same assumption
#      breaks a root container, which has no sudo and needs none.)
set -e

BINARY_NAME="helix"
HELIX_HOME="$HOME/.helix"

# Windows here means a POSIX emulation layer on Windows — MSYS2's MINGW64,
# Git Bash, Cygwin. It is where `make install` is the natural thing to type,
# so it has to work, but three of this script's assumptions do not hold there:
# there is no sudo, there is no /etc/shells, and a binary is not runnable from
# cmd or PowerShell unless it is named .exe. (Note that `go build -o dist/helix`
# does NOT append .exe — Go only supplies that suffix when it picks the name
# itself — so the build output is a PE file with no extension.)
IS_WINDOWS=0
case "$OSTYPE" in
    msys*|cygwin*|win32*) IS_WINDOWS=1 ;;
esac

INSTALL_DIR="${HELIX_INSTALL_DIR:-/usr/local/bin}"
INSTALLED_NAME="$BINARY_NAME"
if [ "$IS_WINDOWS" = "1" ]; then
    INSTALLED_NAME="$BINARY_NAME.exe"
fi
TARGET_BINARY="$INSTALL_DIR/$INSTALLED_NAME"

# run_privileged: run a command that may need rights this user does not have.
#
# The order matters. Try the plain command when we are already root or the
# destination is ours, because that is the common case and it asks for nothing.
# Reach for sudo only when we actually need it AND it exists. When it does not
# exist, say which command could not be run rather than letting the shell's
# "command not found" stand as the whole explanation.
run_privileged() {
    if [ "$(id -u)" = "0" ] || [ "$IS_WINDOWS" = "1" ]; then
        "$@"
        return
    fi
    if command -v sudo >/dev/null 2>&1; then
        sudo "$@"
        return
    fi
    # stderr, not stdout: callers pipe INTO this function (`echo x |
    # run_privileged tee ...`) and redirect the pipeline's stdout away, which
    # would swallow the one message explaining why the step did not happen.
    echo "✘ This step needs administrator rights, and sudo is not installed here:" >&2
    echo "    $*" >&2
    echo "  Re-run as root, or set HELIX_INSTALL_DIR to somewhere you own:" >&2
    echo "    HELIX_INSTALL_DIR=\"\$HOME/.local/bin\" ./scripts/install.sh" >&2
    return 1
}

echo "⚡ Helix Shell Installer"
echo "────────────────────────────────────────"

# 1. Build or locate binary
if [ ! -f "./dist/$BINARY_NAME" ]; then
    echo "Building Helix from source..."
    make current
fi

# 2. Install binary (real step — may fail)
echo "Installing binary to $TARGET_BINARY..."
if [ ! -d "$INSTALL_DIR" ]; then
    run_privileged mkdir -p "$INSTALL_DIR"
fi
if [ -w "$INSTALL_DIR" ]; then
    # Ours already — do not ask for a password to write to our own directory.
    cp "./dist/$BINARY_NAME" "$TARGET_BINARY"
    chmod +x "$TARGET_BINARY"
else
    run_privileged cp "./dist/$BINARY_NAME" "$TARGET_BINARY"
    run_privileged chmod +x "$TARGET_BINARY"
fi

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
#
# The old test compared $OSTYPE for exact equality with "msys", which MSYS2
# does not report — it sets msys2.0 (and Cygwin sets cygwin, with a version).
# So this branch was taken on Windows too, and the only reason it did not fail
# there is that the script never got this far.
if [ "$IS_WINDOWS" = "0" ]; then
    # Non-fatal on purpose. This line only enables the OPTIONAL step below —
    # making Helix a login shell — and by now the binary is installed and
    # runnable. Exiting here would leave a working install reported as a
    # failure, which is the worse of the two wrong answers.
    if ! grep -q "$TARGET_BINARY" /etc/shells 2>/dev/null; then
        echo "Registering $TARGET_BINARY in /etc/shells..."
        if echo "$TARGET_BINARY" | run_privileged tee -a /etc/shells > /dev/null; then
            :
        else
            echo "⚠️  Could not register in /etc/shells."
            echo "   Helix is installed and runnable; it just cannot be set as"
            echo "   your login shell until that line exists."
            SKIP_CHSH=1
        fi
    else
        echo "Already registered in /etc/shells"
    fi

    confirm=""
    if [ "${SKIP_CHSH:-0}" = "1" ]; then
        confirm=""
    elif [ -t 0 ]; then
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
    echo ""
    echo "Windows detected — installed inside this POSIX environment."
    echo "  helix is on PATH in this shell now: type 'helix'."
    if command -v cygpath >/dev/null 2>&1; then
        echo "  From cmd or PowerShell it lives at:"
        echo "    $(cygpath -w "$TARGET_BINARY" 2>/dev/null || echo "$TARGET_BINARY")"
        echo "  Add that folder to your Windows PATH to type 'helix' there too,"
    else
        echo "  Add $INSTALL_DIR to your Windows PATH to type 'helix' there too,"
    fi
    echo "  or run scripts/install.ps1 from an elevated PowerShell for a"
    echo "  system-wide install that does it for you."
fi

echo ""
echo "⚡ Installation complete! Run 'helix' to start."