# scripts/get-cgo-flags.sh
#!/bin/bash
# Outputs CGO flags for the current platform.
ROOT_DIR=$(pwd)
LLAMA_WRAPPER_DIR="$ROOT_DIR/go-llama.cpp"
LLAMA_CPP_DIR="$LLAMA_WRAPPER_DIR/llama.cpp"

case "$(uname -s)" in
    Darwin)
        CFLAGS="-I$LLAMA_CPP_DIR -I$LLAMA_CPP_DIR/common"
        LDFLAGS="-L$LLAMA_WRAPPER_DIR -L$LLAMA_CPP_DIR/build -lbinding -lllama -framework Accelerate -framework Metal -framework MetalKit -framework Foundation"
        ;;
    Linux)
        CFLAGS="-I$LLAMA_CPP_DIR -I$LLAMA_CPP_DIR/common"
        LDFLAGS="-L$LLAMA_WRAPPER_DIR -L$LLAMA_CPP_DIR/build -lbinding -lllama -lstdc++ -lm"
        ;;
    *)
        echo "Unsupported OS" >&2; exit 1
        ;;
esac

case "$1" in
    cflags)   echo "$CFLAGS" ;;
    ldflags)  echo "$LDFLAGS" ;;
    *)        echo "Usage: $0 cflags|ldflags" >&2; exit 1 ;;
esac