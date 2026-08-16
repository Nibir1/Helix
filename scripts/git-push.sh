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
fixing CI jobs
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