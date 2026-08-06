#!/usr/bin/env bash
# scripts/run-helix.sh
# Purpose: Build and run Helix locally without CGO or submodule dependencies.
# Phase 3: Replaced legacy llama.cpp submodule compilation with pure Go build.
set -e

ROOT_DIR=$(pwd)
DIST_DIR="$ROOT_DIR/dist"
MAIN_PACKAGE="./cmd/helix"

echo "Starting Helix AI CLI"
echo "Root: $ROOT_DIR"
echo "Main package: $MAIN_PACKAGE"
echo

# --- Dependency Checks ---
if ! command -v go &> /dev/null; then
    echo "Go not found. Please install Go 1.25+ first."
    exit 1
fi

# --- Build Helix ---
echo "Building Helix (CGO-free)..."
mkdir -p "$DIST_DIR"
go build -o "$DIST_DIR/helix" "$MAIN_PACKAGE"
echo "Build successful: $DIST_DIR/helix"
echo

# --- Run Helix ---
echo "⚡ Launching Helix..."
exec "$DIST_DIR/helix" "$@"