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
	@echo "Cleaning build artifacts and generated data..."
	./$(SCRIPTS_DIR)/build.sh clean
	@echo "Cleaning user data (preserving models)..."
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
	./$(SCRIPTS_DIR)/run-helix.sh

# ==============================================================================
# THESIS EVALUATION TARGETS
# Automated harness for running the 50-task dataset and extracting telemetry
# ==============================================================================

# 1. Build the Linux binary via an Ubuntu Docker container
eval-build-linux:
	@echo "🐳 Building Linux binary inside an Ubuntu container..."
	docker run --rm -v "$(PROJECT_ROOT):/app" -w /app ubuntu:24.04 bash -c "\
		apt-get update && \
		apt-get install -y golang cmake build-essential libasound2-dev && \
		make clean && \
		./scripts/build.sh current && \
		mv dist/helix dist/helix-linux-amd64 \
	"
	@echo "✅ Linux binary successfully built at dist/helix-linux-amd64"

# 2. Run the Baseline (Control Group)
eval-baseline:
	@echo "📊 Running Baseline Evaluation (OpenAI Raw Execution)..."
	@if [ -z "$$OPENAI_API_KEY" ]; then echo "❌ Error: OPENAI_API_KEY is not set."; exit 1; fi
	python3 thesis_evaluation/run_baseline.py

# 3. Run the Helix Harness (Experimental Group)
eval-helix:
	@echo "🧬 Running Helix Evaluation Harness (Dockerized Telemetry)..."
	@if [ -z "$$OPENAI_API_KEY" ]; then echo "❌ Error: OPENAI_API_KEY is not set."; exit 1; fi
	@if [ ! -f "dist/helix-linux-amd64" ]; then echo "❌ Error: Linux binary missing. Run 'make eval-build-linux' first."; exit 1; fi
	chmod +x thesis_evaluation/run_eval.sh
	./thesis_evaluation/run_eval.sh

# 4. Parse telemetry results (Phase 3.1)
eval-parse:
	@echo "📈 Parsing Helix telemetry (Phase 3.1)..."
	@if [ ! -d "thesis_evaluation/telemetry_results" ]; then echo "❌ Error: No telemetry data found. Run 'make eval-helix' first."; exit 1; fi
	chmod +x thesis_evaluation/parse_results.py
	python3 thesis_evaluation/parse_results.py

# 5. Calculate deltas and metrics (Phase 3.2)
eval-analyze:
	@echo "📊 Calculating thesis metrics (Phase 3.2)..."
	@if [ ! -f "thesis_evaluation/helix_parsed_results.csv" ]; then echo "❌ Error: Parsed results not found. Run 'make eval-parse' first."; exit 1; fi
	chmod +x thesis_evaluation/calculate_deltas.py
	python3 thesis_evaluation/calculate_deltas.py

# 6. Generate visualizations (Phase 4)
eval-visualize:
	@echo "📊 Generating Chapter 6 visualizations..."
	python3 thesis_evaluation/generate_charts.py

# 7. Clean evaluation artifacts
eval-clean:
	@echo "🧹 Cleaning previous thesis evaluation data..."
	rm -rf thesis_evaluation/telemetry_results/*.json
	rm -f thesis_evaluation/helix_parsed_results.csv
	rm -f thesis_evaluation/baseline_results.csv
	rm -f thesis_evaluation/*.png thesis_evaluation/*.pdf
	@echo "✅ Evaluation data cleared."

# 8. Run complete Phase 3 pipeline (parse + analyze)
eval-phase3: eval-parse eval-analyze
	@echo "✅ Phase 3 complete: Data parsed and analyzed"

# 9. Run the entire evaluation pipeline end-to-end
eval-run-all: eval-clean eval-build-linux eval-baseline eval-helix eval-phase3
	@echo "🎉 Entire evaluation pipeline completed successfully!"
	@echo "📁 Results: thesis_evaluation/helix_parsed_results.csv"

.PHONY: all current macos linux windows build-all clean deep-clean dev run info start eval-build-linux eval-baseline eval-helix eval-parse eval-analyze eval-visualize eval-clean eval-phase3 eval-run-all