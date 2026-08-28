#!/usr/bin/env bash
# scripts/edge-setup.sh
#
# Purpose: prepare a Linux edge device (Raspberry Pi, NVIDIA Jetson, arm64/amd64
# mini-PC, other SBCs) to run Helix and its BlackBox voice stack.
# Companion to docs/edge_deployment.md and BlackBox_Development.md §6B Phase 10.
#
# What it does:
#   1. Detects architecture and board model.
#   2. Installs the two things the CORE needs beyond the binary — a recorder
#      (sox/ffmpeg) and bubblewrap (confinement on kernels without Landlock) —
#      through the system package manager.
#   3. Offers Ollama for a local LLM, with a pinned-checksum install, and
#      REFUSES it on boards that cannot run it (Jetson Nano 1st-gen).
#   4. Prints guided, copy-pasteable setup for the ML sidecars.
#
# Design rules (BlackBox guardrail §12 #1 — consent-gated, checksum-pinned):
#   * Nothing installs without an explicit yes. --yes opts in up front.
#   * Remote scripts are SHA-256 verified before execution. Never pipe curl|sh.
#   * Package-manager installs inherit distro signature verification; that is
#     why sox/bubblewrap/ffmpeg go through apt/dnf/pacman rather than tarballs.
#   * whisper.cpp / Piper / Kokoro / openWakeWord are NOT auto-downloaded. They
#     have no stable, per-arch, checksummable release artifact (whisper.cpp is
#     built from source, Kokoro ships as Docker), so pinning them would be
#     security theater that rots. They are user-managed sidecars (ADR-002,
#     P7.7) and this script prints exact instructions instead.
#   * Prompts run only on a TTY and tolerate EOF, so piped/CI runs cannot hang
#     or abort under `set -e` (the v1.0.0 install lesson, see install.sh).
#
# Usage:
#   ./scripts/edge-setup.sh                 # interactive
#   ./scripts/edge-setup.sh --yes           # assume yes to every prompt
#   ./scripts/edge-setup.sh --dry-run       # print the plan, change nothing
#   ./scripts/edge-setup.sh --check         # detection report only
#   ./scripts/edge-setup.sh --assume-board="NVIDIA Jetson Nano"   # test a path
set -euo pipefail

# Keep this in sync with ollamaInstallScriptSHA256 in internal/ollama/installer.go.
# Both verify the same upstream artifact; a drift between them means one of the
# two install paths is trusting something the other rejects.
OLLAMA_INSTALL_SHA256="25f64b810b947145095956533e1bdf56eacea2673c55a7e586be4515fc882c9f"
OLLAMA_INSTALL_URL="https://ollama.com/install.sh"

ASSUME_YES=0
DRY_RUN=0
CHECK_ONLY=0
ASSUME_BOARD=""

for arg in "$@"; do
    case "$arg" in
        --yes|-y)          ASSUME_YES=1 ;;
        --dry-run)         DRY_RUN=1 ;;
        --check)           CHECK_ONLY=1 ;;
        --assume-board=*)  ASSUME_BOARD="${arg#*=}" ;;
        --help|-h)
            sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "Unknown option: $arg (try --help)" >&2
            exit 2
            ;;
    esac
done

say()  { printf '%s\n' "$*"; }
info() { printf '  %s\n' "$*"; }
warn() { printf '  !! %s\n' "$*" >&2; }
step() { printf '\n== %s\n' "$*"; }

# ask returns 0 for yes. Non-TTY or EOF means "no" — a setup script must never
# block a headless provisioning run waiting for an answer nobody can give.
ask() {
    local prompt="$1"
    if [ "$ASSUME_YES" -eq 1 ]; then
        info "$prompt -> yes (--yes)"
        return 0
    fi
    if [ ! -t 0 ]; then
        info "$prompt -> no (not a terminal; re-run with --yes to opt in)"
        return 1
    fi
    local reply=""
    printf '  %s [y/N] ' "$prompt"
    read -r reply || true
    case "$reply" in [yY]|[yY][eE][sS]) return 0 ;; *) return 1 ;; esac
}

# run executes a command, or prints it under --dry-run.
run() {
    if [ "$DRY_RUN" -eq 1 ]; then
        printf '  [dry-run] %s\n' "$*"
        return 0
    fi
    "$@"
}

# ---------------------------------------------------------------- detection

detect_board() {
    if [ -n "$ASSUME_BOARD" ]; then
        printf '%s' "$ASSUME_BOARD"
        return
    fi
    local p
    # Same precedence as internal/edge.boardModelPaths. Device-tree strings are
    # NUL-terminated, hence the tr.
    for p in /proc/device-tree/model \
             /sys/firmware/devicetree/base/model \
             /sys/class/dmi/id/product_name; do
        if [ -r "$p" ]; then
            tr -d '\000' < "$p" | head -n1
            return
        fi
    done
    printf ''
}

# is_jetson_nano_first_gen mirrors internal/edge.IsJetsonNanoFirstGen: the Orin
# Nano is a different, modern device whose name also contains "Nano".
is_jetson_nano_first_gen() {
    local b
    b="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
    case "$b" in
        *jetson*nano*)
            case "$b" in *orin*) return 1 ;; esac
            return 0
            ;;
    esac
    return 1
}

detect_pkg_manager() {
    for m in apt-get dnf pacman zypper apk; do
        if command -v "$m" >/dev/null 2>&1; then printf '%s' "$m"; return; fi
    done
    printf ''
}

pkg_install() {
    local mgr="$1"; shift
    case "$mgr" in
        apt-get) run sudo apt-get install -y "$@" ;;
        dnf)     run sudo dnf install -y "$@" ;;
        pacman)  run sudo pacman -S --noconfirm "$@" ;;
        zypper)  run sudo zypper install -y "$@" ;;
        apk)     run sudo apk add "$@" ;;
        *)       warn "no supported package manager; install manually: $*"; return 1 ;;
    esac
}

ARCH="$(uname -m)"
KERNEL="$(uname -r)"
BOARD="$(detect_board)"
PKG="$(detect_pkg_manager)"

case "$ARCH" in
    aarch64|arm64) GOARCH="arm64" ;;
    x86_64|amd64)  GOARCH="amd64" ;;
    armv7l|armv6l) GOARCH="arm (GOARM=7)" ;;
    riscv64)       GOARCH="riscv64" ;;
    *)             GOARCH="unknown" ;;
esac

# Landlock needs kernel >= 5.13; below that bubblewrap is the only remaining
# confinement layer (docs/edge_deployment.md §3.2).
kernel_has_landlock() {
    local major minor
    major="$(printf '%s' "$KERNEL" | cut -d. -f1)"
    minor="$(printf '%s' "$KERNEL" | cut -d. -f2 | tr -cd '0-9')"
    [ -z "$major" ] && return 1
    [ -z "$minor" ] && minor=0
    if [ "$major" -gt 5 ]; then return 0; fi
    if [ "$major" -eq 5 ] && [ "$minor" -ge 13 ]; then return 0; fi
    return 1
}

say "⚡ Helix edge setup"
say "────────────────────────────────────────"

# This script targets Linux edge devices. On any other OS the kernel/Landlock
# and package-manager logic is meaningless, so say so rather than printing
# confident nonsense (macOS reports a Darwin kernel version that trivially
# "passes" the >= 5.13 Landlock check).
if [ "$(uname -s)" != "Linux" ]; then
    warn "This script targets Linux edge devices; detected $(uname -s)."
    warn "Detection below is not meaningful on this OS. Use --dry-run/--check for review only."
fi

info "Architecture : $ARCH  (build GOARCH=$GOARCH)"
info "Kernel       : $KERNEL"
info "Board        : ${BOARD:-<undetected>}"
info "Packages     : ${PKG:-<none found>}"

if kernel_has_landlock; then
    info "Landlock     : available (kernel >= 5.13)"
else
    info "Landlock     : UNAVAILABLE (kernel < 5.13) — bubblewrap matters here"
fi

if command -v sox >/dev/null 2>&1 || command -v rec >/dev/null 2>&1; then
    info "Recorder     : sox present"
elif command -v ffmpeg >/dev/null 2>&1; then
    info "Recorder     : ffmpeg present (sox preferred)"
else
    info "Recorder     : none — voice input needs one"
fi

if command -v bwrap >/dev/null 2>&1; then
    info "bubblewrap   : present"
else
    info "bubblewrap   : absent"
fi

if command -v ollama >/dev/null 2>&1; then
    info "Ollama       : present"
else
    info "Ollama       : absent"
fi

if [ "$CHECK_ONLY" -eq 1 ]; then
    say ""
    say "Detection only (--check); nothing was changed."
    exit 0
fi

# ------------------------------------------------------- core prerequisites

step "Core prerequisites (recorder + confinement)"
say "  Helix needs sox for microphone capture (ADR-003: capture shells out, no CGO)"
say "  and bubblewrap to keep kernel confinement on kernels without Landlock."

if [ -z "$PKG" ]; then
    warn "No supported package manager found. Install 'sox' and 'bubblewrap' manually."
else
    NEEDED=""
    command -v sox   >/dev/null 2>&1 || NEEDED="$NEEDED sox"
    command -v bwrap >/dev/null 2>&1 || NEEDED="$NEEDED bubblewrap"
    # Arch packages bubblewrap under the same name; only the apt name differs
    # for ffmpeg-less minimal images, so keep the list simple and portable.
    if [ -z "$NEEDED" ]; then
        info "Already installed: sox, bubblewrap"
    elif ask "Install:$NEEDED ?"; then
        # NEEDED is a space-separated package list; word splitting is intended.
        # shellcheck disable=SC2086
        pkg_install "$PKG" $NEEDED || warn "package install failed; continuing"
    else
        info "Skipped. Voice input and/or kernel confinement will be degraded."
    fi
fi

# ------------------------------------------------------------- audio output

step "On-device speaker output (the audio_cgo gotcha)"
say "  The default Linux binary is CGO-free and therefore SILENT: it can still"
say "  transcribe and print, but it cannot speak. To hear TTS on this device,"
say "  build on-device with ALSA:"
say ""
say "    sudo apt install -y libasound2-dev"
say "    CGO_ENABLED=1 go build -tags audio_cgo -o helix ./cmd/helix"
say ""
say "  Headless board with no speaker? Keep the CGO-free binary and drive it"
say "  with 'helix remote'. Check the current state any time with /doctor."

# --------------------------------------------------------------- local LLM

step "Local LLM (Ollama)"

if is_jetson_nano_first_gen "$BOARD"; then
    warn "Board is a first-generation Jetson Nano — Ollama is NOT supported here."
    say "  JetPack for this board is frozen at 4.6 (Ubuntu 18.04, CUDA 10.2,"
    say "  Maxwell compute 5.3), which Ollama does not target."
    say ""
    say "  Recommended instead — the CLOUD voice path, which runs smoothly"
    say "  because the heavy compute is remote:"
    say "    helix  →  /blackbox setup  →  Groq whisper-large-v3-turbo + gpt-4o-mini-tts"
    say ""
    say "  If you want local inference anyway, build llama.cpp from source and"
    say "  point Helix at it (a first-class provider since P11.4):"
    say "    llama-server -m model.gguf --port 8080 &"
    say "    export HELIX_LLAMACPP_URL=http://127.0.0.1:8080"
    say "  then set llm.fallback.provider=llamacpp in ~/.helix/config.json."
elif command -v ollama >/dev/null 2>&1; then
    info "Ollama already installed."
    say "  Pull a size-matched model for this board:"
    case "$GOARCH" in
        amd64) say "    ollama pull qwen2.5:3b     # or llama3.1:8b with 16GB+" ;;
        arm64) say "    ollama pull qwen2.5:3b     # Pi 5 / strong SBC" ;;
        *)     say "    ollama pull qwen2.5:1.5b   # small board" ;;
    esac
elif ask "Install Ollama (downloads the official installer, SHA-256 verified)?"; then
    TMP_INSTALLER="$(mktemp -t helix-ollama-install.XXXXXX)"
    trap 'rm -f "$TMP_INSTALLER"' EXIT

    info "Downloading $OLLAMA_INSTALL_URL"
    if [ "$DRY_RUN" -eq 1 ]; then
        info "[dry-run] would download, verify SHA-256, then run the installer"
    else
        curl -fsSL "$OLLAMA_INSTALL_URL" -o "$TMP_INSTALLER"

        WANT="${HELIX_OLLAMA_INSTALL_SHA256:-$OLLAMA_INSTALL_SHA256}"
        if command -v sha256sum >/dev/null 2>&1; then
            GOT="$(sha256sum "$TMP_INSTALLER" | cut -d' ' -f1)"
        elif command -v shasum >/dev/null 2>&1; then
            GOT="$(shasum -a 256 "$TMP_INSTALLER" | cut -d' ' -f1)"
        else
            warn "No sha256sum/shasum available — refusing to run an unverified installer."
            GOT=""
        fi

        # Fail closed: an unverifiable or mismatched script is never executed.
        if [ -z "$GOT" ] || [ "$GOT" != "$WANT" ]; then
            warn "Checksum mismatch or unverifiable — NOT running the installer."
            warn "  got:  ${GOT:-<none>}"
            warn "  want: $WANT"
            warn "If upstream changed, set HELIX_OLLAMA_INSTALL_SHA256 to the new value."
        else
            info "Checksum verified."
            sh "$TMP_INSTALLER"
        fi
    fi
else
    info "Skipped Ollama. The cloud voice path works without it."
fi

# ---------------------------------------------------------------- sidecars

step "ML sidecars (user-managed — instructions only)"
say "  Helix deliberately does NOT auto-download these. They have no stable,"
say "  per-architecture, checksummable release artifact, so a pinned installer"
say "  would rot into security theater. They are external HTTP services"
say "  (ADR-002) and Helix only ever speaks to them over localhost."
say ""
say "  whisper.cpp (local STT)"
say "    git clone https://github.com/ggml-org/whisper.cpp && cd whisper.cpp && make"
say "    ./build/bin/whisper-server -m models/ggml-base.en.bin --port 8080"
say "    → /blackbox setup, choose 'whisper-local'"
say ""
say "  Piper (local TTS, lowest latency)"
say "    Releases: https://github.com/rhasspy/piper/releases  (pick your arch)"
say "    → /blackbox setup, choose 'piper-local'"
say ""
say "  Kokoro-FastAPI (local TTS, best quality — needs more CPU)"
say "    docker run -p 8880:8880 ghcr.io/remsky/kokoro-fastapi-cpu"
say "    → /blackbox setup, choose 'kokoro-local'"
say ""
say "  openWakeWord (true keyword spotting; Helix defaults to energy onset)"
say "    Serve a /predict endpoint, then: /blackbox wake on  +  speech.wake_word.engine=sidecar"

# ------------------------------------------------------------------ wrap-up

step "Next steps"
say "  1. helix          — start the shell"
say "  2. /doctor        — verify this device (audio build, confinement, sidecars, thermals)"
say "  3. /mictest       — prove the microphone is actually heard"
say "  4. /blackbox setup   — choose your STT/TTS path (cloud, hybrid, or fully local)"
say "  5. /blackbox wake on  — hands-free listening"
say ""
say "  Full matrix and per-board notes: docs/edge_deployment.md"

if [ "$DRY_RUN" -eq 1 ]; then
    say ""
    say "(dry run — nothing was changed)"
fi
