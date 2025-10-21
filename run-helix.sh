#!/bin/bash
set -e

# --- Configuration ---
ROOT_DIR=$(pwd)
LLAMA_WRAPPER_DIR="${ROOT_DIR}/go-llama.cpp"
LLAMA_CPP_DIR="${LLAMA_WRAPPER_DIR}/llama.cpp"
BUILD_JOBS=$(sysctl -n hw.ncpu 2>/dev/null || echo 4)

echo "🏗️  Building Helix AI CLI"
echo "Root: $ROOT_DIR"
echo "Llama wrapper dir: $LLAMA_WRAPPER_DIR"
echo "Llama.cpp dir: $LLAMA_CPP_DIR"
echo "Build jobs: $BUILD_JOBS"
echo

# --- Dependency Checks ---
dependencies=("cmake" "go")
for dep in "${dependencies[@]}"; do
    if ! command -v "$dep" &> /dev/null; then
        echo "❌ $dep not found. Please install it first."
        exit 1
    fi
done

# --- Ensure llama.cpp exists ---
if [ ! -d "$LLAMA_CPP_DIR" ]; then
    echo "❌ Missing llama.cpp directory at: $LLAMA_CPP_DIR"
    echo "💡 You might need to initialize submodules: git submodule update --init --recursive"
    exit 1
fi

# --- Build llama.cpp ---
echo "🔧 Building llama.cpp..."
if [ ! -f "${LLAMA_CPP_DIR}/build/libllama.a" ]; then
    (cd "$LLAMA_CPP_DIR" && \
        mkdir -p build && \
        cd build && \
        cmake .. && \
        make -j$BUILD_JOBS)
else
    echo "✅ llama.cpp already built, skipping..."
fi

# --- Build llama.cpp bindings ---
echo "🔧 Building llama.cpp bindings (libbinding.a)..."
(cd "$LLAMA_WRAPPER_DIR" && \
    make clean && \
    make libbinding.a)

# --- Export CGO environment variables ---
export CGO_CFLAGS="-I${LLAMA_CPP_DIR} -I${LLAMA_CPP_DIR}/common"
export CGO_LDFLAGS="-L${LLAMA_WRAPPER_DIR} -L${LLAMA_CPP_DIR}/build -lbinding -lllama -framework Accelerate -framework Metal -framework MetalKit -framework Foundation"
export CGO_CXXFLAGS="$CGO_CFLAGS"

echo
echo "✅ Using CGO_CFLAGS: $CGO_CFLAGS"
echo "✅ Using CGO_LDFLAGS: $CGO_LDFLAGS"
echo "✅ Using CGO_CXXFLAGS: $CGO_CXXFLAGS"
echo

# --- Verify libraries ---
echo "🔍 Verifying libraries exist..."
required_libs=(
    "${LLAMA_WRAPPER_DIR}/libbinding.a"
    "${LLAMA_CPP_DIR}/build/libllama.a"
)

for lib in "${required_libs[@]}"; do
    if [ ! -f "$lib" ]; then
        echo "❌ Library not found: $lib"
        exit 1
    fi
    echo "✅ Found: $(basename "$lib")"
done

echo "✅ All libraries verified successfully"
echo

# --- Run Helix CLI ---
echo "🚀 Running Helix..."
exec go run . "$@"