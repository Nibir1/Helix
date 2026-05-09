# scripts/build.sh
#!/bin/bash
set -e

echo "🏗️  Building Helix..."

ROOT_DIR=$(pwd)
DIST_DIR="$ROOT_DIR/dist"
LLAMA_WRAPPER_DIR="$ROOT_DIR/go-llama.cpp"
LLAMA_CPP_DIR="$LLAMA_WRAPPER_DIR/llama.cpp"
MAIN_PACKAGE="./cmd/helix"

TARGET=${1:-current}
mkdir -p "$DIST_DIR"

# ---------- architecture detection ----------
HOST_ARCH=$(uname -m)          # arm64 / x86_64
case "$HOST_ARCH" in
    arm64|aarch64)  GOARCH="arm64"  CMAKE_ARCH="arm64" ;;
    x86_64)         GOARCH="amd64"  CMAKE_ARCH="x86_64" ;;
    *)              echo "❌ Unknown architecture: $HOST_ARCH"; exit 1 ;;
esac
echo "Detected architecture: $HOST_ARCH (Go: $GOARCH, CMake: $CMAKE_ARCH)"

# ---------- build llama.cpp ----------
build_dependencies() {
    echo "🔧 Building dependencies..."
    if [ ! -f "$LLAMA_CPP_DIR/build/libllama.a" ]; then
        echo "🔧 Building llama.cpp..."
        (
            cd "$LLAMA_CPP_DIR"
            mkdir -p build && cd build
            cmake .. -DCMAKE_OSX_ARCHITECTURES="$CMAKE_ARCH"
            make -j$(sysctl -n hw.ncpu 2>/dev/null || echo 4)
        )
    else
        echo "✅ llama.cpp already built"
    fi

    # Build bindings only if missing
    if [ ! -f "$LLAMA_WRAPPER_DIR/libbinding.a" ]; then
        echo "🔧 Building llama.cpp bindings..."
        (
            cd "$LLAMA_WRAPPER_DIR"
            make clean
            make libbinding.a
        )
    else
        echo "✅ llama.cpp bindings already built"
    fi
}

# ---------- CGO environment per platform ----------
setup_macos_cgo() {
    export CGO_CFLAGS="-I$LLAMA_CPP_DIR -I$LLAMA_CPP_DIR/common"
    export CGO_LDFLAGS="-L$LLAMA_WRAPPER_DIR -L$LLAMA_CPP_DIR/build -lbinding -lllama -framework Accelerate -framework Metal -framework MetalKit -framework Foundation"
    export CGO_CXXFLAGS="$CGO_CFLAGS"
}

setup_linux_cgo() {
    export CGO_CFLAGS="-I$LLAMA_CPP_DIR -I$LLAMA_CPP_DIR/common"
    export CGO_LDFLAGS="-L$LLAMA_WRAPPER_DIR -L$LLAMA_CPP_DIR/build -lbinding -lllama -lstdc++ -lm"
    export CGO_CXXFLAGS="$CGO_CFLAGS"
}

setup_windows_cgo() {
    export CGO_CFLAGS="-I$LLAMA_CPP_DIR -I$LLAMA_CPP_DIR/common"
    export CGO_LDFLAGS="-L$LLAMA_WRAPPER_DIR -L$LLAMA_CPP_DIR/build -lbinding -lllama -static"
    export CGO_CXXFLAGS="$CGO_CFLAGS"
}

verify_libraries() {
    echo "🔍 Verifying libraries..."
    if [ ! -f "$LLAMA_WRAPPER_DIR/libbinding.a" ]; then
        echo "❌ libbinding.a not found!"
        exit 1
    fi
    if [ ! -f "$LLAMA_CPP_DIR/build/libllama.a" ]; then
        echo "❌ libllama.a not found!"
        exit 1
    fi
    echo "✅ Libraries verified"
}

# ---------- build functions ----------
build_current() {
    echo "🏗️  Building for current platform ($(uname -s))..."
    case "$(uname -s)" in
        Darwin*)
            setup_macos_cgo
            OUTPUT="$DIST_DIR/helix"
            ;;
        Linux*)
            setup_linux_cgo
            OUTPUT="$DIST_DIR/helix"
            ;;
        *)
            echo "❌ Unsupported platform: $(uname -s)"
            exit 1
            ;;
    esac
    verify_libraries
    GOARCH="$GOARCH" go build -o "$OUTPUT" "$MAIN_PACKAGE"
    echo "✅ Build completed: $OUTPUT"
}

build_macos() {
    echo "🍎 Building for macOS..."
    # Force architecture to match detection (override with GOARCH if needed)
    setup_macos_cgo
    verify_libraries
    GOOS=darwin GOARCH=amd64 go build -o "$DIST_DIR/helix-macos-amd64" "$MAIN_PACKAGE"
    GOOS=darwin GOARCH=arm64 go build -o "$DIST_DIR/helix-macos-arm64" "$MAIN_PACKAGE"
    echo "✅ macOS builds completed"
}

build_linux() {
    echo "🐧 Building for Linux..."
    setup_linux_cgo
    verify_libraries
    GOOS=linux GOARCH=amd64 go build -o "$DIST_DIR/helix-linux-amd64" "$MAIN_PACKAGE"
    GOOS=linux GOARCH=arm64 go build -o "$DIST_DIR/helix-linux-arm64" "$MAIN_PACKAGE"
}

build_windows() {
    echo "🪟 Building for Windows..."
    setup_windows_cgo
    verify_libraries
    GOOS=windows GOARCH=amd64 go build -o "$DIST_DIR/helix-windows-amd64.exe" "$MAIN_PACKAGE"
}

build_all() {
    build_macos
    build_linux
    build_windows
}

# ---------- main ----------
case "$TARGET" in
    current)
        build_dependencies
        build_current
        ;;
    macos|linux|windows|all)
        build_dependencies
        build_$TARGET
        ;;
    clean)
        echo "🧹 Cleaning build artifacts..."
        rm -rf "$DIST_DIR"
        if [ -d "$LLAMA_CPP_DIR/build" ]; then
            rm -rf "$LLAMA_CPP_DIR/build"
        fi
        if [ -f "$LLAMA_WRAPPER_DIR/libbinding.a" ]; then
            (cd "$LLAMA_WRAPPER_DIR" && make clean)
        fi
        echo "✅ Clean completed"
        ;;
    *)
        echo "Usage: $0 {current|macos|linux|windows|all|clean}"
        exit 1
        ;;
esac

echo ""
echo "🎉 Build process completed!"
echo "💡 Run './dist/helix' to start your application"