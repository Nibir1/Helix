#!/bin/bash
set -e

echo "🏗️  Building Helix..."

# Configuration
ROOT_DIR=$(pwd)
DIST_DIR="$ROOT_DIR/dist"
LLAMA_WRAPPER_DIR="$ROOT_DIR/go-llama.cpp"
LLAMA_CPP_DIR="$LLAMA_WRAPPER_DIR/llama.cpp"
MAIN_PACKAGE="./cmd/helix"  # NEW: Path to main package

# Default build target (current platform)
TARGET=${1:-current}

# Create dist directory
mkdir -p "$DIST_DIR"

# Function to build llama.cpp and bindings
build_dependencies() {
    echo "🔧 Building dependencies..."
    
    # Build llama.cpp if not already built
    if [ ! -f "$LLAMA_CPP_DIR/build/libllama.a" ]; then
        echo "🔧 Building llama.cpp..."
        (cd "$LLAMA_CPP_DIR" && mkdir -p build && cd build && cmake .. && make -j$(sysctl -n hw.ncpu 2>/dev/null || echo 4))
    else
        echo "✅ llama.cpp already built"
    fi

    # Build bindings if not already built
    if [ ! -f "$LLAMA_WRAPPER_DIR/libbinding.a" ]; then
        echo "🔧 Building llama.cpp bindings..."
        (cd "$LLAMA_WRAPPER_DIR" && make clean && make libbinding.a)
    else
        echo "✅ llama.cpp bindings already built"
    fi
}

# Function to set CGO environment for macOS
setup_macos_cgo() {
    export CGO_CFLAGS="-I$LLAMA_CPP_DIR -I$LLAMA_CPP_DIR/common"
    export CGO_LDFLAGS="-L$LLAMA_WRAPPER_DIR -L$LLAMA_CPP_DIR/build -lbinding -lllama -framework Accelerate -framework Metal -framework MetalKit -framework Foundation"
    export CGO_CXXFLAGS="$CGO_CFLAGS"
}

# Function to set CGO environment for Linux
setup_linux_cgo() {
    export CGO_CFLAGS="-I$LLAMA_CPP_DIR -I$LLAMA_CPP_DIR/common"
    export CGO_LDFLAGS="-L$LLAMA_WRAPPER_DIR -L$LLAMA_CPP_DIR/build -lbinding -lllama -lstdc++ -lm"
    export CGO_CXXFLAGS="$CGO_CFLAGS"
}

# Function to set CGO environment for Windows
setup_windows_cgo() {
    export CGO_CFLAGS="-I$LLAMA_CPP_DIR -I$LLAMA_CPP_DIR/common"
    export CGO_LDFLAGS="-L$LLAMA_WRAPPER_DIR -L$LLAMA_CPP_DIR/build -lbinding -lllama -static"
    export CGO_CXXFLAGS="$CGO_CFLAGS"
}

# Function to verify libraries exist
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

# Function to build for current platform
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
    
    echo "📝 CGO Environment:"
    echo "CGO_CFLAGS: $CGO_CFLAGS"
    echo "CGO_LDFLAGS: $CGO_LDFLAGS"
    
    verify_libraries
    # CHANGED: Build from the main package path
    go build -o "$OUTPUT" "$MAIN_PACKAGE"
    echo "✅ Build completed: $OUTPUT"
}

# Function to build for macOS
build_macos() {
    echo "🍎 Building for macOS..."
    setup_macos_cgo
    
    echo "📝 CGO Environment:"
    echo "CGO_CFLAGS: $CGO_CFLAGS"
    echo "CGO_LDFLAGS: $CGO_LDFLAGS"
    
    verify_libraries
    # CHANGED: Build from the main package path
    GOOS=darwin GOARCH=amd64 go build -o "$DIST_DIR/helix-macos-amd64" "$MAIN_PACKAGE"
    GOOS=darwin GOARCH=arm64 go build -o "$DIST_DIR/helix-macos-arm64" "$MAIN_PACKAGE"
    echo "✅ macOS builds completed:"
    echo "   - $DIST_DIR/helix-macos-amd64 (Intel)"
    echo "   - $DIST_DIR/helix-macos-arm64 (Apple Silicon)"
}

# Function to build for Linux
build_linux() {
    echo "🐧 Building for Linux..."
    setup_linux_cgo
    
    echo "📝 CGO Environment:"
    echo "CGO_CFLAGS: $CGO_CFLAGS"
    echo "CGO_LDFLAGS: $CGO_LDFLAGS"
    
    verify_libraries
    # CHANGED: Build from the main package path
    GOOS=linux GOARCH=amd64 go build -o "$DIST_DIR/helix-linux-amd64" "$MAIN_PACKAGE"
    GOOS=linux GOARCH=arm64 go build -o "$DIST_DIR/helix-linux-arm64" "$MAIN_PACKAGE"
    echo "✅ Linux builds completed:"
    echo "   - $DIST_DIR/helix-linux-amd64 (64-bit)"
    echo "   - $DIST_DIR/helix-linux-arm64 (ARM64)"
}

# Function to build for Windows
build_windows() {
    echo "🪟 Building for Windows..."
    setup_windows_cgo
    
    echo "📝 CGO Environment:"
    echo "CGO_CFLAGS: $CGO_CFLAGS"
    echo "CGO_LDFLAGS: $CGO_LDFLAGS"
    
    verify_libraries
    # CHANGED: Build from the main package path
    GOOS=windows GOARCH=amd64 go build -o "$DIST_DIR/helix-windows-amd64.exe" "$MAIN_PACKAGE"
    echo "✅ Windows build completed: $DIST_DIR/helix-windows-amd64.exe"
}

# Function to build all platforms
build_all() {
    echo "🌍 Building for all platforms..."
    build_macos
    build_linux
    build_windows
    echo "🎉 All platform builds completed!"
}

# Show usage information
show_usage() {
    echo "Usage: $0 [TARGET]"
    echo ""
    echo "Build targets:"
    echo "  current    Build for current platform (default)"
    echo "  macos      Build for macOS (Intel + Apple Silicon)"
    echo "  linux      Build for Linux (AMD64 + ARM64)" 
    echo "  windows    Build for Windows (AMD64)"
    echo "  all        Build for all platforms"
    echo "  clean      Clean build artifacts"
    echo ""
    echo "Examples:"
    echo "  $0              # Build for current platform"
    echo "  $0 macos        # Build for macOS"
    echo "  $0 all          # Build for all platforms"
}

# Clean build artifacts
clean_build() {
    echo "🧹 Cleaning build artifacts..."
    rm -rf "$DIST_DIR"
    if [ -d "$LLAMA_CPP_DIR/build" ]; then
        echo "🧹 Cleaning llama.cpp build..."
        rm -rf "$LLAMA_CPP_DIR/build"
    fi
    if [ -f "$LLAMA_WRAPPER_DIR/libbinding.a" ]; then
        echo "🧹 Cleaning bindings..."
        (cd "$LLAMA_WRAPPER_DIR" && make clean)
    fi
    echo "✅ Clean completed"
}

# Main build logic
case "$TARGET" in
    current)
        build_dependencies
        build_current
        ;;
    macos)
        build_dependencies
        build_macos
        ;;
    linux)
        build_dependencies
        build_linux
        ;;
    windows)
        build_dependencies
        build_windows
        ;;
    all)
        build_dependencies
        build_all
        ;;
    clean)
        clean_build
        ;;
    -h|--help|help)
        show_usage
        ;;
    *)
        echo "❌ Unknown target: $TARGET"
        show_usage
        exit 1
        ;;
esac

echo ""
echo "🎉 Build process completed!"
echo "💡 Run './dist/helix' to start your application (on current platform)"