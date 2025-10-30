# Build configuration
BINARY_NAME=helix
DIST_DIR=dist
SCRIPTS_DIR=scripts
USER_HOME=$(shell echo $$HOME)
HELIX_HOME=$(USER_HOME)/.helix
PROJECT_ROOT=$(shell pwd)

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

# Clean build artifacts AND generated data (but keep models)
clean:
	@echo "🧹 Cleaning build artifacts and generated data..."
	./$(SCRIPTS_DIR)/build.sh clean
	@echo "🧹 Cleaning user data (preserving models)..."
	# Remove RAG indexes
	-rm -rf "$(HELIX_HOME)/rag_index"
	-rm -rf "$(HELIX_HOME)/vector_index"
	-rm -rf "$(HELIX_HOME)/man_index"
	# Remove history and logs
	-rm -f "$(HELIX_HOME)/helix_history"
	-rm -f "$(HELIX_HOME)/.helix_history"
	-rm -f "$(HELIX_HOME)/config.json"
	-rm -f "$(HELIX_HOME)/*.log"
	-rm -f "$(HELIX_HOME)/llama_*.log"
	-rm -f "$(PROJECT_ROOT)/*.log"
	# Remove temporary files but KEEP models directory
	-find "$(HELIX_HOME)" -name "*.tmp" -delete
	-find "$(HELIX_HOME)" -name "*.json" -not -path "*/models/*" -delete
	@echo "✅ Clean completed (models preserved in $(HELIX_HOME)/models/)"

# Deep clean (including models) - USE WITH CAUTION
deep-clean: clean
	@echo "🔥 Deep cleaning (including models)..."
	-rm -rf "$(HELIX_HOME)/models"
	@echo "⚠️  All data including models have been removed"

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
	@echo "Helix home: $(HELIX_HOME)"
	@echo "Available targets: current, macos, linux, windows, all, clean, deep-clean"

# To run the project without building first
start:
	./$(SCRIPTS_DIR)/run-helix.sh

.PHONY: all current macos linux windows build-all clean deep-clean dev run info start