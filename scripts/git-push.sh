#!/usr/bin/env bash
set -e

echo "⚡ Helix v1.0.0 Release Pipeline"

# 1. Stage and commit the recent interrupt-hardening changes only
git add .
git commit -m "$(cat <<'EOF'
switching the signs block to use the ${...} environment variable syntax, which tells Cosign exactly where 
to write the .pem and .sig files so GoReleaser can attach them to the release.
EOF
)"

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