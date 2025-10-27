# Build configuration
BINARY_NAME=helix
DIST_DIR=dist
SCRIPTS_DIR=scripts

# Default target
all: current

# Build targets using the build script
current:
	./$(SCRIPTS_DIR)/build.sh current

macos:
	./$(SCRIPTS_DIR)/build.sh macos

linux:
	./$(SCRIPTS_DIR)/build.sh linux

windows:
	./$(SCRIPTS_DIR)/build.sh windows

# Build for all platforms
build-all: all
	./$(SCRIPTS_DIR)/build.sh all

# Clean build artifacts
clean:
	./$(SCRIPTS_DIR)/build.sh clean

# Development build (fast, for testing)
dev: current
	@echo "🚀 Running development build..."
	./$(DIST_DIR)/helix

# Run the built application
run: dev

# Show build info
info:
	@echo "📊 Build Information:"
	@echo "Binary: $(BINARY_NAME)"
	@echo "Dist dir: $(DIST_DIR)"
	@echo "Scripts dir: $(SCRIPTS_DIR)"
	@echo "Available targets: current, macos, linux, windows, all, clean"

# To run the project without building first
start:
	./$(SCRIPTS_DIR)/run-helix.sh

.PHONY: all current macos linux windows build-all clean dev run info start