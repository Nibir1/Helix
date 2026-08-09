# Makefile for Helix - Build, Test, and Run Automation
# Build configuration
BINARY_NAME=helix
DIST_DIR=dist
SCRIPTS_DIR=scripts
USER_HOME=$(shell echo $$HOME)
HELIX_HOME=$(USER_HOME)/.helix
PROJECT_ROOT=$(shell pwd)

# Default target
all: current

# -------------------------------------------------------------------
# Ensure all shell scripts are executable before building / testing
# -------------------------------------------------------------------
define fix-perms
	@chmod +x $(SCRIPTS_DIR)/*.sh 2>/dev/null || true
endef

# Build targets using the build script
current:
	$(fix-perms)
	./$(SCRIPTS_DIR)/build.sh current

macos:
	$(fix-perms)
	./$(SCRIPTS_DIR)/build.sh macos

linux:
	$(fix-perms)
	./$(SCRIPTS_DIR)/build.sh linux

windows:
	$(fix-perms)
	./$(SCRIPTS_DIR)/build.sh windows

# Build for all platforms
build-all: all
	./$(SCRIPTS_DIR)/build.sh all

# Lint the codebase using golangci-lint
lint:
	@echo "Running golangci-lint..."
	@golangci-lint run ./... --timeout=5m || (echo "Install golangci-lint: https://golangci-lint.run/usage/install/" && exit 1)

# Clean build artifacts AND generated data (but keep models)
clean:
	$(fix-perms)
	@echo "Cleaning build artifacts and generated data..."
	./$(SCRIPTS_DIR)/build.sh clean
	@echo "Cleaning user data (preserving models)..."
	# Remove RAG indexes
	-rm -rf "$(HELIX_HOME)/rag_index"
	-rm -rf "$(HELIX_HOME)/vector_index"
	-rm -rf "$(HELIX_HOME)/man_index"
	# Remove the knowledge database
	-rm -f "$(HELIX_HOME)/helix.db"
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
	@echo "Clean completed (models preserved in $(HELIX_HOME)/models/)"

# Deep clean (including models) - USE WITH CAUTION
deep-clean: clean
	@echo "Deep cleaning (including models)..."
	-rm -rf "$(HELIX_HOME)/models"
	@echo "All data including models have been removed"

# Development build (fast, for testing)
dev: current
	@echo "Running development build..."
	./$(DIST_DIR)/helix

# Run the built application
run: dev

# Generic build target that defaults to the current platform
build: current

# Install Helix as a system shell
install: current
	@echo "Running Helix installer..."
	@chmod +x $(SCRIPTS_DIR)/install.sh
	@./$(SCRIPTS_DIR)/install.sh

# Show build info
info:
	@echo "Build Information:"
	@echo "Binary: $(BINARY_NAME)"
	@echo "Dist dir: $(DIST_DIR)"
	@echo "Scripts dir: $(SCRIPTS_DIR)"
	@echo "Helix home: $(HELIX_HOME)"
	@echo "Available targets: current, macos, linux, windows, all, clean, deep-clean"

# To run the project without building first
start:
	$(fix-perms)
	./$(SCRIPTS_DIR)/run-helix.sh

test:
	$(fix-perms)
	@echo "Running all tests..."
	go test ./... -v -count=1

# Run local security vulnerability scan using govulncheck
sec-scan:
	@echo "Running govulncheck..."
	@command -v govulncheck >/dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
	@govulncheck ./...


FUZZTIME ?= 30s

# Run continuous fuzzing across all safety-critical parsers.
# Go's native fuzzing only allows ONE -fuzz target per `go test` invocation,
# so we explicitly list every target to avoid wildcard collisions.
fuzz:
	@echo "Fuzzing safety surface..."
	@go test ./internal/commands/safety -run=^$$ -fuzz=FuzzValidateAndCleanShellCommand -fuzztime=$(FUZZTIME)
	@go test ./internal/commands/safety -run=^$$ -fuzz=FuzzAnalyzeShellRisk -fuzztime=$(FUZZTIME)
	@go test ./internal/shell -run=^$$ -fuzz=FuzzClassify -fuzztime=$(FUZZTIME)
	@go test ./internal/ai -run=^$$ -fuzz=FuzzParsePlanFromModelOutput -fuzztime=$(FUZZTIME)
	@go test ./internal/commands -run=^$$ -fuzz=FuzzSandboxValidateCommand -fuzztime=$(FUZZTIME)
	@go test ./internal/commands -run=^$$ -fuzz=FuzzValidateSafePath -fuzztime=$(FUZZTIME)

# CI smoke test: shorter duration to keep the pipeline fast.
fuzz-ci:
	@echo "Running CI fuzz smoke test (20s per target)..."
	@go test ./internal/commands/safety -run=^$$ -fuzz=FuzzValidateAndCleanShellCommand -fuzztime=20s
	@go test ./internal/commands/safety -run=^$$ -fuzz=FuzzAnalyzeShellRisk -fuzztime=20s
	@go test ./internal/shell -run=^$$ -fuzz=FuzzClassify -fuzztime=20s
	@go test ./internal/ai -run=^$$ -fuzz=FuzzParsePlanFromModelOutput -fuzztime=20s
	@go test ./internal/commands -run=^$$ -fuzz=FuzzSandboxValidateCommand -fuzztime=20s
	@go test ./internal/commands -run=^$$ -fuzz=FuzzValidateSafePath -fuzztime=20s

# Run the end-to-end TTY harness (Linux/macOS; the build tag skips Windows)
e2e:
	@echo "Running E2E TTY harness..."
	go test ./tests/e2e/... -v -count=1 -timeout 300s

# Run all tasks: lint, build, test, install
work: lint sec-scan build test install


.PHONY: all build current macos linux windows build-all clean deep-clean dev run info start test lint work sec-scan install fuzz fuzz-ci e2e