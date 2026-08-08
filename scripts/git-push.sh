#!/usr/bin/env bash
set -e

echo "⚡ Helix v1.0.0 Release Pipeline"

# 1. Stage and commit the final production hardening
git add .
git commit -m "$(cat <<'EOF'
feat(release): finalize v1.0.0 production hardening, UX polish, and resilient git pipeline

UX & Shell Hardening:
- Add "HELIX :: REASONING" animated TrueColor thinker for AI latency
- Intercept history/fc/!! natively to bypass child-shell isolation
- Guard unknown slash commands to prevent stdin black-hole hangs
- Harden install.sh/install.ps1 against non-interactive stdin EOF crashes
- Fix shell-aware quote balancing (literal quotes inside opposing types)

Resilient Git Pipeline (Production-Ready):
- Implement idempotent git commit (graceful skip on clean working tree)
- Add resilient git add fallback (auto-stages modified files on pathspec miss)
- Fix silent error swallowing in sandbox execution (non-zero exits halt pipeline)
- Add full annotated tag support via secure temporary message files
- Remove unused parameters and satisfy strict linter rules

Knowledge Base & RAG (Phase 3.5c):
- Implement schema versioning and transactional migrations
- Add ETag conditional-GET caching for NVD, KEV, Exploit-DB, MITRE
- Complete defensive /explain triad with "Safer Operational Alternatives"
- Add OpenAI -> Ollama zero-key embedding fallback chain

System & Infrastructure:
- Add /purge command for full local data wipe with double confirmation
- Wire determinate progress bars with exact row counts for all fetches
- Finalize CI/CD matrix, GoReleaser v2, and cross-platform installers
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