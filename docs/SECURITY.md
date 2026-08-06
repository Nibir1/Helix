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

## Secret Handling
- API keys are stored in `~/.helix/secrets.json` with strict `0600` file permissions.
- Helix preferentially reads secrets from environment variables to avoid disk persistence in ephemeral environments.
- No secrets are ever logged, even when `HELIX_DEBUG=1` is enabled.

## Reporting a Vulnerability
If you discover a security vulnerability in Helix (e.g., a sandbox escape, a planner injection flaw, or a bypass of the safety pipeline), please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email the maintainer or use the GitHub Security Advisories feature to report the issue privately. We will acknowledge receipt within 48 hours and work on a patch.