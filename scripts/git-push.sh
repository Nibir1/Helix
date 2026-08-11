#!/usr/bin/env bash
set -e

echo "⚡ Helix v1.0.0 Release Pipeline"

# 1. Stage and commit the recent changes
git add .

# FIX: Use `git commit -F -` with a heredoc to safely pass the multi-line message.
# This avoids bash parsing bugs where backticks inside `$(cat <<EOF)` 
# are mistakenly evaluated as command substitutions before the heredoc 
# delimiter is recognized.
git commit -F - <<'EOF'
refactor(ai): standardize on Ollama, adopt Gemma 4, and harden local UX

Architectural Shift:
- Excised raw llama.cpp runtime (downloader, builder, server) and Custom
  OpenAI endpoints from the setup wizard and provider registry.
- Standardized on Ollama as the sole local inference engine to leverage
  its native macOS Metal memory management and avoid upstream dylib bugs.

Model Defaults:
- Upgraded local model recommendations to Google Gemma 4 E2B (default)
  and E4B for high-performance edge inference.

Bug Fixes:
- Fixed critical state-leak bug where Ollama model pulls failed to call
  `ai.UseModel()`, causing Helix to retain the previous provider's model.
- Fixed `/status` and `/rag-status` UI hangs caused by SQLite contention
  during background knowledge syncs (added PRAGMA busy_timeout and
  context-bounded queries).
- Fixed overlapping/ghosting progress bars by widening the stage label
  column to 32 runes, adding live elapsed-time counters, and enforcing
  strict `\033[2K` line clearing for console fallbacks.

Cleanup:
- Purged dead code (internal/llamacpp, local_runtime.go, unused helpers)
  to satisfy golangci-lint `unused` and `errcheck` rules.
EOF

# 2. Push to main
git push origin main

# 3. Delete the old v1.0.0 tag locally and remotely (safe deletion, ignores if missing)
git tag -d v1.0.0 2>/dev/null || true
git push --delete origin v1.0.0 2>/dev/null || true

# 4. Create the final, production-grade v1.0.0 annotated tag
git tag -a v1.0.0 -m "v1.0.0: Production-Ready AI-Native Shell"

# 5. Push the tag to trigger GoReleaser
git push origin v1.0.0

echo "✅ Release v1.0.0 successfully pushed!"