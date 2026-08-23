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

### 6. Telemetry-Free Local Records
Crash reports are written ONLY to local disk (0600), contain redacted
environment values (`*_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`), are never
transmitted, are capped at 5, and are removable via `/purge`. The diagnostics
package is provably network-free via a CI-enforced import grep test.

The same contract now covers everything else Helix writes about a session:
`internal/journal` (the daemon interaction journal and the opt-in voice
transcript log) and `internal/metrics` (latency and liveness samples) each carry
their own CI-enforced grep test proving they import no networking. Code that
writes down what you said or did cannot send it anywhere. All of it is 0600
inside a 0700 directory, size-rotated, and wiped by `/purge`.

### 7. Voice Channel Controls
Speech is an **untrusted input channel**, not a convenient keyboard. Transcribed
audio arrives with user authority, so a television, a podcast, or a person in the
room becomes text that could otherwise plan and execute. The controls are
structural rather than advisory:

- **Risk is capped at Medium.** A high-risk command is unreachable from voice
  whatever the phrasing, and the refusal is spoken as well as printed.
- **Typed confirmations stay typed.** Force push, hard reset, worktree clean,
  deleting a main branch: the voice prompter refuses these outright, so voice
  cannot satisfy them even with a perfect impersonation.
- **Confirmations fail closed.** Silence, timeout, or an unintelligible answer
  counts as "no".
- **Spoken input never takes the shell fast path.** A confidently classified
  command line runs as typed when typed; from the microphone it always goes to
  the planner, because the classifier decides on the first token and ordinary
  sentences begin with command names.
- **Default-deny command surface.** A command is reachable by voice only if the
  registry marks it so; nine remain unreachable by design (data destruction,
  scanning, history writes, posture and privacy switches, key entry).
- **Voice may reduce what is collected, never increase it.** "Turn off your eyes"
  closes the camera and `/blackbox log off` stops transcript recording, both by
  voice; opening the camera is an explicit announced act and starting a
  transcript log must be typed.

Full model, including the residual risk accepted for a voice-first assistant:
`docs/threat_model_voice.md`.

### 8. Camera and Transcript Privacy
- **Camera frames are memory-only, always.** One frame at a time, downscaled,
  held in RAM, never written to disk — enforced by a filesystem-snapshot test.
  Only metadata (provider, count, timestamp) reaches the journal.
- **Vision is off by default** and opens on an explicit, announced act.
  `/blackbox status` will not claim the camera is working until a frame has
  actually arrived.
- **Nothing you say is stored unless you ask.** `/blackbox log on` starts a local
  transcript log; with it off there is no directory and no file. It records text
  and metadata only — never audio, because captured clips are deleted the moment
  they are read.

## Secret Handling
- API keys are stored in `~/.helix/secrets.json` with strict `0600` file permissions.
- Helix preferentially reads secrets from environment variables to avoid disk persistence in ephemeral environments.
- No secrets are ever logged, even when `HELIX_DEBUG=1` is enabled.
- Speech keys are namespaced (`stt.*` / `tts.*`) in the same store, and a saved
  key is verified and reused rather than requested again.
- **Misdirected-key guard.** A pasted key whose prefix unambiguously belongs to
  another vendor (`sk-ant-`, `xai-`, `gsk_`) is flagged before it is stored,
  because GroqCloud and xAI are different companies one letter apart and the
  mistake otherwise surfaces later as an auth failure on every transcription.
  The check is negative-only: it never asserts what a valid key looks like, since
  vendors change formats and a positive rule would start rejecting good keys.

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
  --certificate Helix_Linux_x86_64.tar.gz.pem \
  --signature Helix_Linux_x86_64.tar.gz.sig \
  --certificate-identity-regexp "https://github.com/Nibir1/Helix/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  Helix_Linux_x86_64.tar.gz
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