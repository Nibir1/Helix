#!/usr/bin/env bash
# scripts/build.sh
# CGO-free Helix build script.
set -e

ROOT_DIR=$(pwd)
DIST_DIR="$ROOT_DIR/dist"
MAIN_PACKAGE="./cmd/helix"
TARGET=${1:-current}

mkdir -p "$DIST_DIR"

build_current() {
    echo "Building Helix for current platform..."
    # CGO_ENABLED=0, like every other target here and like what actually ships.
    # This was the ONE build in the project that used cgo: the cross-compiled
    # targets below are CGO-free because cross-compiling disables cgo, CI's
    # step is named "Build Binary (CGO-free)" and sets this explicitly, and
    # .goreleaser.yml sets it for every released artifact. So `make build`
    # produced a binary built differently from the one users get, and it was
    # the only thing in the repo that needed a working system linker — which
    # is how a host Xcode/CommandLineTools SDK mismatch turned into "Helix
    # does not build" on a machine where nothing about Helix was wrong.
    CGO_ENABLED=0 go build -o "$DIST_DIR/helix" "$MAIN_PACKAGE"
    echo "Build completed: $DIST_DIR/helix"
}

build_macos() {
    echo "Building for macOS..."
    GOOS=darwin GOARCH=amd64 go build -o "$DIST_DIR/helix-macos-amd64" "$MAIN_PACKAGE"
    GOOS=darwin GOARCH=arm64 go build -o "$DIST_DIR/helix-macos-arm64" "$MAIN_PACKAGE"
    echo "macOS builds completed"
}

build_linux() {
    echo "Building for Linux..."
    GOOS=linux GOARCH=amd64 go build -o "$DIST_DIR/helix-linux-amd64" "$MAIN_PACKAGE"
    GOOS=linux GOARCH=arm64 go build -o "$DIST_DIR/helix-linux-arm64" "$MAIN_PACKAGE"
    echo "Linux builds completed"
}

build_windows() {
    echo "Building for Windows..."
    GOOS=windows GOARCH=amd64 go build -o "$DIST_DIR/helix-windows-amd64.exe" "$MAIN_PACKAGE"
    echo "Windows build completed"
}

build_all() {
    build_macos
    build_linux
    build_windows
}

case "$TARGET" in
    current)
        build_current
        ;;
    macos)
        build_macos
        ;;
    linux)
        build_linux
        ;;
    windows)
        build_windows
        ;;
    all)
        build_all
        ;;
    clean)
        echo "Cleaning build artifacts..."
        rm -rf "$DIST_DIR"
        echo "Clean completed"
        ;;
    *)
        echo "Usage: $0 {current|macos|linux|windows|all|clean}"
        exit 1
        ;;
esac

echo ""
echo "Build process completed!"
echo "Run './dist/helix' to start Helix"