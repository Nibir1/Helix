## Security Posture
Helix is designed with a "trust but verify" model. The LLM output is treated as untrusted input. Every shell command, git action, and package installation passes through strict structural validators before reaching the OS. Dangerous operations require explicit typed confirmations (e.g., "YES, FORCE PUSH").

# Security Policy

## Authorized Use Only
Helix is designed as a **defensive cybersecurity platform** and an **AI-powered productivity shell**. 
While it includes reconnaissance engines (`/scan`) and exploit references (via Exploit-DB integration), these are strictly for:
- Authorized penetration testing and red team operations.
- Defensive threat intelligence and detection engineering.
- Educational purposes in controlled environments.

**Unauthorized scanning, exploitation, or malicious use of Helix is strictly prohibited.**

## Safety Guards
Helix treats AI-generated output as untrusted. To prevent catastrophic accidents, the following safety layers are enforced:

### 1. Shell Safety Pipeline
- **Hard Blocks**: Destructive patterns like `rm -rf /`, `mkfs`, `curl | sh`, and `eval` are blocked at the parser level.
- **Risk Tiering**: Medium-risk commands (e.g., `sed -i`, redirections) require explicit user confirmation.
- **Directory Sandbox**: Write and delete operations are confined to the current working directory and its subdirectories. Absolute paths outside the sandbox are rejected.

### 2. Git & Package Safeguards
- **Typed Confirmations**: Dangerous Git operations (`push --force`, `reset --hard`, `clean -fdx`) require the user to type an exact confirmation phrase.
- **Critical Package Protection**: Helix blocks the removal of critical system packages (e.g., `libc6`, `systemd`, `bash`) to prevent OS corruption.

### 3. Reconnaissance Authorization
- The `/scan` engine requires explicit target authorization with a written scope/reason before executing `nmap` or `masscan`.
- Dangerous flags (e.g., `masscan --rate 1000000`) are blocked to prevent network floods.

### 4. Instruction Firewall (Prompt-Injection Hardening)
Retrieved knowledge is untrusted data with zero authority. Planner context is
built only from sanitized structured fields inside `authority="data-only"`
fences; a per-request canary detects context echo; a fail-closed critic validates any plan that exhibits unsolicited network
egress (external URLs absent from the user request); clean local operations
execute without extra review, keeping Helix fast for legitimate red-team work. See `docs/threat_model.md`.

### 5. Kernel-Grade Confinement
In strict mode (`/sandbox strict`), write and delete operations outside the
jail root are denied by the OS kernel (Seatbelt on macOS; bubblewrap or
Landlock on Linux). This closes the gap left by advisory path validation:
even a confused or injected child process cannot write outside the sandbox.
Where no kernel backend exists, Helix warns and degrades to advisory
confinement.

### 6. Telemetry-Free Crash Diagnostics
Crash reports are written ONLY to local disk (0600), contain redacted
environment values (`*_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`), are never
transmitted, are capped at 5, and are removable via `/purge`. The diagnostics
package is provably network-free via a CI-enforced import grep test.

## Secret Handling
- API keys are stored in `~/.helix/secrets.json` with strict `0600` file permissions.
- Helix preferentially reads secrets from environment variables to avoid disk persistence in ephemeral environments.
- No secrets are ever logged, even when `HELIX_DEBUG=1` is enabled.

## Supply-Chain Security & Release Integrity

Every official Helix release is built with a verified supply chain. We generate a Software Bill of Materials (SBOM) in SPDX format and cryptographically sign every release artifact using [Sigstore](https://sigstore.dev/) keyless signing.

### Verifying a Release

You can verify the integrity and provenance of any downloaded Helix binary or archive using `cosign` and `syft`.

**1. Install the tools:**
- [Cosign](https://docs.sigstore.dev/cosign/installation)
- [Syft](https://github.com/anchore/syft#installation)

**2. Verify the signature (Sigstore Keyless):**
```bash
cosign verify-blob \
  --certificate helix_Linux_x86_64.tar.gz.pem \
  --signature helix_Linux_x86_64.tar.gz.sig \
  --certificate-identity-regexp "https://github.com/Nibir1/Helix/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  helix_Linux_x86_64.tar.gz
```

**3. Inspect the SBOM:**
```bash
syft helix_Linux_x86_64.tar.gz
```

### Continuous Security Scanning
Every commit and pull request is automatically scanned for known vulnerabilities in Go dependencies using `govulncheck` and for static application security testing (SAST) using GitHub CodeQL. See `.github/workflows/security.yml` for details. You can run these checks locally via `make sec-scan`.

## Reporting a Vulnerability
If you discover a security vulnerability in Helix (e.g., a sandbox escape, a planner injection flaw, or a bypass of the safety pipeline), please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email the maintainer or use the GitHub Security Advisories feature to report the issue privately. We will acknowledge receipt within 48 hours and work on a patch.