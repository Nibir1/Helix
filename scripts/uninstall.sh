#!/usr/bin/env bash
# scripts/uninstall.sh
# Purpose: remove Helix from this machine. The counterpart to install.sh, and
# the thing `make uninstall` runs.
#
# THIS SCRIPT IS A WRAPPER, NOT THE IMPLEMENTATION. install.sh has to be a
# script because it runs before there is a binary; uninstall runs when there is
# one, so the paths live in Go (internal/uninstall) where /purge and
# `helix uninstall` read the same list. A second copy here would be a second
# thing to keep in step, and the failure mode of a stale copy is not cosmetic:
# a launchd job left pointing at a deleted binary is retried forever.
#
# So the only real logic below is FINDING a binary to ask. In order:
#   1. the installed one on PATH  — the copy the user actually types;
#   2. ./dist/helix               — this checkout, if it has been built;
#   3. build it                   — a source tree with no build yet.
set -e

BINARY_NAME="helix"

echo "⚡ Helix Uninstaller"
echo "────────────────────────────────────────"

HELIX_BIN=""
if command -v "$BINARY_NAME" >/dev/null 2>&1; then
    HELIX_BIN="$(command -v "$BINARY_NAME")"
    echo "Using the installed binary: $HELIX_BIN"
elif [ -x "./dist/$BINARY_NAME" ]; then
    HELIX_BIN="./dist/$BINARY_NAME"
    echo "No installed binary on PATH; using this checkout's build."
else
    echo "No Helix binary found. Building one to run the uninstall..."
    make current
    HELIX_BIN="./dist/$BINARY_NAME"
fi

# No --yes. The binary prints the manifest and asks, because the manifest is
# the part that makes the answer informed, and a `make` target that removes a
# login shell without showing what it is about to do is not a target anyone
# should have to trust.
exec "$HELIX_BIN" uninstall
