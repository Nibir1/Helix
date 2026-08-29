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
	@golangci-lint run ./... --timeout=5m || (echo "" && \
	 echo "If this failed to RUN (rather than reporting issues), your golangci-lint" && \
	 echo "is probably v1 — it cannot read the v2 .golangci.yml. Install the version" && \
	 echo "CI uses, so local and CI enforce the same rules:" && \
	 echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0" && exit 1)

# Reset generated state. Build artifacts, indexes, logs, caches.
#
# What this deliberately does NOT touch is credentials. The previous version
# ended with
#
#     find "$(HELIX_HOME)" -name "*.json" -not -path "*/models/*" -delete
#
# which deleted secrets.json — the API keystore — along with session.json and
# every other record. Nobody running `make clean` expects to re-enter their API
# keys, and the target's own message said "preserving models", not "removing
# your credentials". Files are named individually now, so adding a new one is a
# decision rather than an accident.
clean:
	$(fix-perms)
	@echo "Cleaning build artifacts..."
	./$(SCRIPTS_DIR)/build.sh clean
	-rm -f "$(PROJECT_ROOT)/coverage.out" "$(PROJECT_ROOT)/coverage.html"
	-find "$(PROJECT_ROOT)" -maxdepth 1 -name "*.log" -delete
	@echo "Cleaning generated state (models and credentials preserved)..."
	# Derived indexes: rebuilt on demand from sources Helix still has.
	-rm -rf "$(HELIX_HOME)/rag_index" "$(HELIX_HOME)/vector_index" "$(HELIX_HOME)/man_index"
	-rm -f "$(HELIX_HOME)/helix.db"
	# History and per-run records.
	-rm -f "$(HELIX_HOME)/helix_history" "$(HELIX_HOME)/.helix_history"
	-rm -f "$(HELIX_HOME)/config.json"
	-rm -f "$(HELIX_HOME)/session.json" "$(HELIX_HOME)/reboot.json"
	-rm -f "$(HELIX_HOME)/daemon.conn.json" "$(HELIX_HOME)/active.lock"
	-rm -rf "$(HELIX_HOME)/crash" "$(HELIX_HOME)/voice_log" "$(HELIX_HOME)/metrics"
	# Logs. `rm -f "$(HELIX_HOME)/*.log"` did nothing at all for months: the
	# quotes stop the shell expanding the glob, so it looked for one file
	# literally named "*.log". find expands nothing and handles spaces.
	-find "$(HELIX_HOME)" -maxdepth 1 -name "*.log" -delete
	-find "$(HELIX_HOME)" -name "*.tmp" -delete
	@echo "Clean done. Kept: secrets.json, and everything under models/,"
	@echo "  whisper-models/, piper-voices/, piper/ and csm.rs/."

# Everything clean does, plus every model and runtime Helix downloaded.
#
# The list is the same one /purge offers, because they answer the same question
# and drifting apart is how a "deep clean" leaves 6 GB behind. HELIX_MODEL_DIR
# is honoured for the same reason /purge honours it: on a machine that moved its
# GGUFs, the default path is not where they are.
deep-clean: clean
	@echo "Removing downloaded models and runtimes..."
	-rm -rf "$(HELIX_HOME)/models"
	-rm -rf "$${HELIX_MODEL_DIR:-/nonexistent-helix-model-dir}"
	-rm -rf "$(HELIX_HOME)/whisper-models"
	-rm -rf "$(HELIX_HOME)/piper-voices"
	-rm -rf "$(HELIX_HOME)/piper"
	-rm -rf "$(HELIX_HOME)/csm.rs"
	-rm -f "$(HELIX_HOME)/secrets.json"
	@echo ""
	@echo "Removed everything Helix owns under $(HELIX_HOME), including API keys."
	@echo ""
	@echo "NOT removed — these are shared with anything else that uses them:"
	@echo "  Ollama models       $${OLLAMA_MODELS:-$(USER_HOME)/.ollama/models}"
	@echo "  CSM weights (~6GB)  $${HF_HUB_CACHE:-$(USER_HOME)/.cache/huggingface/hub}/models--sesame--csm-1b"
	@echo ""
	@echo "  A Makefile cannot show you their sizes and ask. /purge can, and does."

# Remove every credential Helix stores, and nothing else.
#
# Separate from clean and deep-clean on purpose. `make clean` must NOT take
# credentials — it used to, through a blanket delete of every .json under
# ~/.helix, and re-entering API keys is not part of cleaning a build. But
# revoking what is on this machine is a real thing to want on its own: handing a
# laptop on, filing a bug with a transcript, or after a key has leaked.
#
# What this removes:
#   secrets.json       every provider API key (OpenAI, Anthropic, Deepgram, …)
#   daemon.conn.json   the daemon's per-start auth token
#   voice_log/         spoken transcripts, if the opt-in log was ever enabled
#
# The Hugging Face token is named rather than deleted: it lives in a shared
# cache that other tools authenticate against, and `hf auth logout` is the
# command that revokes it properly rather than leaving a half-removed login.
delete-secrets:
	@echo "Removing credentials from $(HELIX_HOME)..."
	-rm -f "$(HELIX_HOME)/secrets.json"
	-rm -f "$(HELIX_HOME)/daemon.conn.json"
	-rm -rf "$(HELIX_HOME)/voice_log"
	@echo ""
	@echo "Removed: provider API keys, the daemon token, and any voice transcripts."
	@echo ""
	@echo "NOT removed, because it is shared and has its own revoke:"
	@echo "  Hugging Face token   $${HF_HOME:-$(USER_HOME)/.cache/huggingface}/token"
	@echo "    revoke with:  hf auth logout"
	@echo ""
	@echo "Keys set in the environment are not files and survive this."
	@echo "  check with:  env | grep -iE 'API_KEY|_TOKEN'"

# Development build (fast, for testing)
dev: current
	@echo "Running development build..."
	./$(DIST_DIR)/$(BINARY_NAME)

# Run the built application
run: dev

# Generic build target that defaults to the current platform
build: current

# Install Helix as a system shell
install: current
	@echo "Running Helix installer..."
	@chmod +x $(SCRIPTS_DIR)/install.sh
	@./$(SCRIPTS_DIR)/install.sh

# Show build info and what every target does.
#
# The old version listed six targets out of twenty and had not been updated
# since they were added — a help text that is wrong is worse than none, because
# it is believed.
info:
	@echo "Helix $(shell grep -oE 'HelixVersion[[:space:]]*=[[:space:]]*\"[^\"]+\"' internal/config/config.go | grep -oE '\"[^\"]+\"' | tr -d '\"')"
	@echo "  binary      $(BINARY_NAME)"
	@echo "  dist        $(DIST_DIR)"
	@echo "  scripts     $(SCRIPTS_DIR)"
	@echo "  helix home  $(HELIX_HOME)"
	@echo ""
	@echo "BUILD    current macos linux windows build-all install"
	@echo "RUN      dev run start"
	@echo "TEST     test e2e fuzz fuzz-ci live-sidecar live-csm"
	@echo "CHECK    lint lint-workflows sec-scan work"
	@echo "RELEASE  release release-check"
	@echo "CLEAN    clean          generated state; keeps keys and models"
	@echo "         deep-clean     + every model and runtime under ~/.helix"
	@echo "         delete-secrets API keys, daemon token, voice transcripts"
	@echo ""
	@echo "  /purge inside Helix also reaches Ollama's models and the Hugging"
	@echo "  Face cache, with sizes, and asks before each group."

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
# CI trades depth for wall-clock; everything else is identical, so the target
# list must not be.
FUZZTIME_CI ?= 20s

# Every fuzz target in the tree, as one list.
#
# It was written out twice — once for `fuzz` and once for `fuzz-ci` — and the
# copies had already drifted: FuzzRequestJSON and FuzzSanitizeOutput existed in
# the code and appeared in neither, so "fuzzing the safety surface" quietly
# skipped two of its parsers. Go's native fuzzing allows one -fuzz target per
# invocation, which is why this is a list rather than a wildcard.
FUZZ_TARGETS = \
	./internal/commands/safety:FuzzValidateAndCleanShellCommand \
	./internal/commands/safety:FuzzAnalyzeShellRisk \
	./internal/shell:FuzzClassify \
	./internal/ai:FuzzParsePlanFromModelOutput \
	./internal/commands:FuzzSandboxValidateCommand \
	./internal/commands:FuzzValidateSafePath \
	./internal/speech:FuzzWAVHeaderInfo \
	./internal/speech:FuzzPricingMerge \
	./internal/ambient:FuzzAnalyzer \
	./internal/agent:FuzzTranscriptPolicyParsers \
	./internal/agent:FuzzSanitizeOutput \
	./internal/journal:FuzzRedact \
	./internal/daemon:FuzzRequestJSON

# run-fuzz <duration> — one `go test` per target, stopping at the first failure.
define run-fuzz
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; fn=$${t##*:}; \
		echo "  $$fn  ($$pkg)"; \
		go test $$pkg -run='^$$$$' -fuzz=$$fn -fuzztime=$(1) || exit 1; \
	done
endef

# Continuous fuzzing across the whole safety surface.
fuzz:
	@echo "Fuzzing safety surface ($(FUZZTIME) per target)..."
	$(call run-fuzz,$(FUZZTIME))

# CI smoke test: shorter duration to keep the pipeline fast.
fuzz-ci:
	@echo "Running CI fuzz smoke test ($(FUZZTIME_CI) per target)..."
	$(call run-fuzz,$(FUZZTIME_CI))

# Run the live sidecar measurements: real whisper.cpp + Piper servers, driven
# through Helix's own adapters. Needs the binaries and a downloaded model/voice;
# skips loudly with the reason when any is missing, so this is safe anywhere.
# Covers the §10 local STT accuracy row (see docs/BlackBox_Development.md §10A).
live-sidecar:
	@echo "Running live sidecar measurements (whisper.cpp + Piper)..."
	HELIX_LIVE_SIDECAR=1 go test ./internal/speech/ -run 'TestLive' -v -count=1 -timeout 600s

# Measure a running Sesame CSM-1B sidecar (csm.rs) and report its real-time
# factor — the number that decides whether conversation flows or stutters.
# Skips loudly when no sidecar is listening. HELIX_CSM_URL overrides the port.
live-csm:
	@echo "Measuring CSM-1B sidecar (skips if none is running)..."
	HELIX_LIVE_SIDECAR=1 go test ./internal/speech/ -run 'TestLiveCSM' -v -count=1 -timeout 300s

# Run the end-to-end TTY harness (Linux/macOS; the build tag skips Windows)
e2e:
	@echo "Running E2E TTY harness..."
	go test ./tests/e2e/... -v -count=1 -timeout 300s

# Tag and publish a release. Run AFTER merging into main.
#
# The tag defaults to `v` + the HelixVersion constant, so there is one place to
# edit when the version changes. Override or add flags with ARGS:
#
#   make release                      tag v<HelixVersion>
#   make release ARGS=--dry-run       run every check, tag nothing
#   make release ARGS="v1.5.1 --force"
release:
	@./$(SCRIPTS_DIR)/release.sh $(ARGS)

# Everything the release script checks, without tagging anything. Usable from a
# feature branch: the repository-state checks (branch, clean tree, in sync with
# origin) are reported and deferred rather than stopping the run, so this can be
# used before the merge, when it is most useful.
release-check:
	@./$(SCRIPTS_DIR)/release.sh --dry-run $(ARGS)

# Lint the GitHub Actions workflows.
#
# actionlint plus one check it does not perform: a multi-line `run:` in a job
# whose matrix includes Windows must say which shell it is written for, because
# the default there is PowerShell. actionlint cannot know that — `runs-on` is
# `${{ matrix.os }}`, resolved at run time — so it shellchecks every step as
# bash and passes the one case that breaks.
lint-workflows:
	@./$(SCRIPTS_DIR)/check-workflows.sh

# Run all tasks: lint, sec-scan, fuzz-ci, e2e, build, test, install
work: lint lint-workflows sec-scan fuzz-ci e2e build test install


.PHONY: all build current macos linux windows build-all clean deep-clean delete-secrets \
	dev run info start test lint lint-workflows work sec-scan install fuzz fuzz-ci \
	live-sidecar live-csm e2e release release-check