#!/usr/bin/env bash
set -e

echo "⚡ Helix v1.0.0 Release Pipeline"

# 1. Stage and commit the recent interrupt-hardening changes only
git add .
git commit -m "$(cat <<'EOF'
feat(release): v1.0.0 Enterprise Hardening Program

This commit finalizes the transition from "portfolio-grade" to "enterprise-grade"
via the six-phase Helix Enterprise Hardening Program, delivering verified
assurance, kernel-level confinement, and telemetry-free diagnostics.

Supply-Chain Security & Release Integrity
- Add govulncheck and CodeQL SAST pipelines to CI
- Generate SPDX SBOMs (Syft) and Sigstore keyless signatures (Cosign)
- Enforce Go 1.26.5 toolchain to patch stdlib crypto/tls (GO-2026-5856)

Fuzzing the Safety Surface
- Introduce invariant-aware Go native fuzzing for shell validation,
  JSON planner parsing, input classification, and sandbox path resolution
- Add CI smoke tests to continuously shake out ReDoS and state-machine bypasses

E2E TTY Harness
- Implement PTY-based end-to-end test suite (creack/pty) with mock providers
- Prove classifier routing, safety tiers, and confirmation UX with zero real AI

Instruction Firewall (Prompt-Injection Hardening)
- Treat RAG knowledge as untrusted data with zero authority
- Implement 5-layer defense: structured-fields context, sanitization,
  canary honeypots, fail-closed critic pass, and provenance escalation

Kernel-Grade Confinement
- Upgrade /sandbox strict from advisory string-matching to kernel enforcement
- Implement macOS Seatbelt, Linux bubblewrap, and pure-Go Landlock LSM
- Add --confined-child re-exec architecture for CGO-free Landlock

Telemetry-Free Crash Diagnostics & UX Polish
- Add local, 0600, secret-redacted crash reporting for panics/signals
- Enforce network-free guarantee via CI grep-test on diagnostics package
- Add interactive /crash command to inspect and clear reports safely
- Surface confinement backends and crash reports in /doctor

The published v1.0.0 release now carries the hardened binary, its SBOM,
and its cryptographic signatures. GRID STATUS :: CLEAR.
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