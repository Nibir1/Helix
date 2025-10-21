# Build configuration
BINARY_NAME=helix
DIST_DIR=dist

# Default target
all: current

# Build targets using the build script
current:
	./build.sh current

macos:
	./build.sh macos

linux:
	./build.sh linux

windows:
	./build.sh windows

# Build for all platforms
build-all: all
	./build.sh all

# Clean build artifacts
clean:
	./build.sh clean

# Development build (fast, for testing)
dev: current
	@echo "🚀 Running development build..."
	./dist/helix

# Run the built application
run: dev

# Show build info
info:
	@echo "📊 Build Information:"
	@echo "Binary: $(BINARY_NAME)"
	@echo "Dist dir: $(DIST_DIR)"
	@echo "Available targets: current, macos, linux, windows, all, clean"

# To run the project without building first
start:
	./run-helix.sh

.PHONY: all current macos linux windows build-all clean dev run info start