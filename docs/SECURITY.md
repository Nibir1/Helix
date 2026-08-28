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

### 5a. Confined Archive Extraction
Helix extracts exactly one third-party archive: the standalone `piper` speech
binary, fetched over the network and then executed. Extraction writes through
an `os.Root` opened on the destination, so every path is resolved **inside**
that directory by the kernel. Entry names are additionally required to be local
(`filepath.IsLocal`, after slash conversion), which rejects absolute paths,
`..` traversal, and Windows reserved device names.

The `os.Root` is the load-bearing half, not the name check. A name-based guard
can only judge the path an archive *declares*; it cannot know what the
filesystem will do with it. `~/.helix/piper` is reused across installs and
upgrades, so if anything in that tree is a symlink pointing out of it, an entry
named `piper/lib/x` is perfectly local by every string test and a checked
`filepath.Join` still follows the link straight out. A regression test extracts
exactly that archive into exactly that tree and asserts the file outside is
untouched.

The self-updater's extraction is safe by a different route: it never uses an
entry's path at all, matching the binary by base name and always writing to a
filename of its own choosing.

### 5b. Bounded Path Validation
Sandbox validation resolves symlinks, which costs a chain of `lstat` calls per
path. `ValidateCommand` therefore bounds the work it will do for one command:
results are memoised per path, and the number of **distinct** paths one command
may make it resolve is capped. Passing the cap **refuses the command** — the
sandbox never permits a path it declined to check.

This is a denial-of-service control, not an access-control one. Commands reach
the validator from a model, so their length is not under the user's control, and
the storage this runs on at the edge is slow. A fuzzer found the original
version resolving every absolute-looking word in *every* command — including
read-only ones, which discarded the answer — at a cost that grew without limit
alongside the input.

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

`/reboot` writes one short-lived file under the same contract, minus the
rotation it does not need: `~/.helix/reboot.json` carries the state a restart
needs — mode, working directory, provider and model, and in-progress tasks — plus,
**for a typed restart only**, a 240-character excerpt of the last thing you
typed. It is not a second copy of the conversation: duplicating `session.json`
would put everything you said on disk twice, in a file `/memory clear` does not
govern. It is **deleted the moment the restarted shell reads it** rather than
rotated, discarded unread past 12 hours, and wiped by `/purge`. It stores
provider and model **names**, never a key.

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
- **Voice may restart the shell, and nothing else in the danger category.**
  `/reboot` is reachable by voice because it destroys nothing: the continuity
  record is written before the process ends, so the worst a misheard "reboot"
  costs is a few seconds, after which the same mode, directory and conversation
  are back. `/purge`, `/rag-reset`, `/commit`, `/config`, `/hooks`, `/init`, `/scan`,
  `/setup` and `/stealth` — all nine — remain unreachable, and the distinction is
  data loss, not severity of sound.
- **A spoken restart MAY install software, by owner decision.** `/reboot`
  self-updates and does so automatically, from the microphone as well as the
  keyboard. The reasoning is that the release comes from a repository the owner
  controls and tags deliberately, so publishing it IS the authorization. The
  consequence is stated rather than hidden: whoever can publish to the configured
  repo can replace the binary with no human present, and a misheard "reboot" can
  trigger that. What still holds is everything in ADR-019 — mandatory checksum,
  pinned host, build-info proof — plus automatic rollback when the new binary
  cannot start. `update.check: false` turns the check off entirely.
- **A spoken restart writes nothing you said.** `/reboot` is voice-reachable, and
  the continuity record it leaves omits conversation content entirely on the
  spoken path — so the rule below survives without an exception.
- **Confirmations fail closed.** Silence, timeout, or an unintelligible answer
  counts as "no".
- **The microphone opens only for a turn — unless you ask otherwise.** Enabling
  sentence-boundary barge-in (`/config barge-in on`) lets Helix sample the mic in
  the pause between its own spoken sentences, so it can be interrupted by voice.
  That clip follows the same path as every capture — the recorder writes a temp
  WAV, which is deleted the moment it is read — and then only its loudness is
  computed. It is never transcribed, never sent to a provider, never logged, and
  never enters the conversational context. Off by default, scoped to live mode, and shown on the
  INTERRUPT row of `/blackbox status`.
- **Spoken input never takes the shell fast path.** A confidently classified
  command line runs as typed when typed; from the microphone it always goes to
  the planner, because the classifier decides on the first token and ordinary
  sentences begin with command names.
- **Default-deny command surface.** A command is reachable by voice only if the
  registry marks it so; nine remain unreachable by design (data destruction,
  scanning, history writes, posture and privacy switches, key entry). One
  DANGER ZONE command is reachable — `/reboot` — because it destroys nothing;
  the criterion is data loss, not how alarming the command sounds.
- **Voice may reduce what is collected, never increase it.** "Turn off your eyes"
  closes the camera and `/blackbox log off` stops transcript recording, both by
  voice; opening the camera is an explicit announced act and starting a
  transcript log must be typed. The rule has **no exceptions**: `/reboot` is the
  case that tested it, and the feature was shaped to fit — a spoken restart
  stores no conversation content — rather than the rule being amended.

Full model, including the residual risk accepted for a voice-first assistant:
`docs/threat_model_voice.md`.

### 7a. Self-Update Trust Model
Helix updates itself through `/reboot`. What is verified, and what is not, stated
plainly because an updater is the highest-consequence code in the project:

- **Verified.** The download's SHA-256 against the checksums file published with
  that release, matched by filename; the URL and every redirect against a pinned
  set of GitHub hosts; and the payload's own Go build info, proving it is a Helix
  binary for this machine before it is installed. Any of these failing is a
  refusal, never a warning.
- **Not verified.** The Sigstore signatures the release pipeline produces.
  Keyless verification with the wrong identity and issuer constraints reports
  success while proving nothing, under a label that stops anyone looking further
  — worse than an honest checksum. Helix prints the `cosign verify-blob` command
  instead of pretending. See ADR-019.
- **Reversible.** The previous binary is kept, and restored automatically if the
  new one exits non-zero within ten seconds of starting — the failure a checksum
  cannot catch, which is an authentic release that does not run on this machine.
- **Automatic, by owner decision.** Checking is on by default and installing
  needs no confirmation, on the typed and spoken paths alike. This is the one
  place Helix trades a prompt for convenience, and it does so because the
  publisher and the operator are the same person. Turn the whole thing off with
  `update.check: false`, or use `/reboot now` to restart without checking.

### 8. Camera and Transcript Privacy
- **A restart is the only thing that puts an excerpt of your words on disk
  without an opt-in, and only when typed.** `/reboot` stores 240 characters of
  the last typed message in `~/.helix/reboot.json` so the resumed shell can say
  what it was doing; the file is 0600, consumed on read, and expires in 12 hours.
  The spoken path stores none of it.
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
- **Conversational context retention is opt-in and never reaches disk.** A
  context-conditioned voice (Sesame CSM-1B) needs to hear the last few turns, so
  enabling `speech.tts.context_turns` makes Helix hold recent audio in memory for
  longer than the turn that produced it. That retention is **memory only** — the
  store imports no filesystem or networking API, enforced by a test — bounded by
  both turn count and total bytes, and dropped when live mode ends. The audio was
  already in memory a moment earlier for transcription; what changes is how long,
  which is why the bounds are the control. Off by default. **It is visible while
  it is happening:** the `CONTEXT` row of `/blackbox status` reports how many
  turns and how much audio are being held, and says **retained, unused** when
  turns are being kept that no configured voice can actually consume — retention
  with a cost and no benefit is the case worth surfacing, not hiding.

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