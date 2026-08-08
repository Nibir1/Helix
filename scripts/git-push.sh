#!/usr/bin/env bash
set -e

echo "⚡ Helix v1.0.0 Release Pipeline"

# 1. Stage and commit the recent interrupt-hardening changes only
git add .
git commit -m "$(cat <<'EOF'
feat(release): v1.0.0 interrupt hardening — cancellable Ctrl+C pipelines

Interrupt & Signal Hardening:
- Add process-wide SIGINT manager (internal/utils/interrupt.go) that cancels
  the running operation instead of killing the shell
- Ctrl+C now aborts /knowledge-update, /rag-reindex, /rag-rebuild, planner
  waits, and embedding calls, then returns to a live prompt
- Ctrl+C at the prompt redraws a fresh prompt (shell-like, never exits)
- `helix update` is cancellable and exits 130 on interrupt
- AI HTTP waits and embedding resolution register interrupt scopes

Cancellable RAG Pipelines:
- UpdateAll checks the caller context between every threat-feed stage
- MAN indexer workers drain-on-cancel so rebuilds unwind in milliseconds
- UpdateKnowledgeCtx / RebuildWithProgressCtx add phase-boundary checkpoints
- Progress bars and cursor always heal on cancellation exit paths
EOF
)" || echo "⚠️  No changes to commit, continuing..."

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