# Helix 🤖  
**An Intelligent, AI‑Powered CLI Assistant, Red-Team Platform, and Native Shell**

---

[![Helix Demo](https://img.youtube.com/vi/_HhHVvOsfuU/maxresdefault.jpg)](https://youtu.be/_HhHVvOsfuU)

> 📺 **[Watch the full end-to-end demo](https://youtu.be/_HhHVvOsfuU)** featuring core functionalities.

---

![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)
![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)
![Security](https://img.shields.io/badge/Security-Enterprise%20Hardened-FF0055?logo=shield)

Helix is an **AI-powered command‑line assistant and adversarial cybersecurity platform** that turns natural language into **safe, executable actions**. It bridges the gap between human intent and machine execution, combining local LLM inference, retrieval-augmented generation (RAG), live threat intelligence, and strict safety pipelines.

It combines:
- **Multi-Provider AI** (OpenAI, Anthropic, DeepSeek, Ollama and more)
- **Live Threat Intelligence** (NVD, CISA KEV, Exploit-DB, MITRE ATT&CK)
- **RAG over System Docs** (900+ indexed MAN pages and CLI tools)
- **A Multi-Layer Safety & Sandbox Engine** around shell, git, packages, and recon
- **Enterprise-Grade Hardening** (Kernel confinement, instruction firewalls, and signed supply chains)
- **Synthetic Tonal Audio** for immersive, synchronized terminal feedback

It’s built as a **portfolio‑grade, enterprise-hardened systems project** in Go, demonstrating real‑world skills in AI integration, sandboxed execution, strict JSON planner protocols, memory-only stealth execution, live threat intelligence pipelines, and verifiable supply-chain security.

---
## Quick Start & Installation

### Option 1: Automated Installer (macOS / Linux)
The fastest way to get Helix running. This one-liner clones the repository, builds the optimized binary, initializes your `~/.helix` configuration directories, and prompts you to set Helix as your default system shell.

```bash
git clone https://github.com/Nibir1/Helix.git && cd Helix && ./scripts/install.sh
```

### Option 2: Windows (PowerShell)
Open PowerShell as Administrator and run the automated Windows setup script. This will build the binary, add it to your system `PATH`, and optionally bootstrap Ollama.

```powershell
git clone https://github.com/Nibir1/Helix.git; cd Helix; .\scripts\install.ps1
```

### Option 3: Go Install (Cross-Platform)
If you already have Go 1.25+ installed and just want the binary in your `$GOPATH/bin`:

```bash
go install github.com/Nibir1/Helix/cmd/helix@latest
```

### Option 4: Pre-compiled Binaries (No Build Required)
Don't want to build from source? Download the latest pre-compiled binary, checksums, and archives for your OS directly from the **[Releases Page](https://github.com/Nibir1/Helix/releases)**. All official releases are cryptographically signed and include a Software Bill of Materials (SBOM). *(See "Verifying Releases" below).*

---

### ⚡ Accelerating Threat Intel: NVD API Key

Helix operates on a **local-first architecture**, downloading and indexing the National Vulnerability Database (NVD), CISA KEV, Exploit-DB, and MITRE ATT&CK directly into your local SQLite knowledge base. This allows `/vuln` and `/explain` queries to run instantly with full context, even when completely offline.

However, the NVD enforces strict rate limits on unauthenticated API requests to prevent server overload. 

| Configuration | Initial Sync Time (119-day window) | Subsequent Syncs |
| :--- | :--- | :--- |
| **Without API Key** | **25 - 40 minutes** (6.5s delay per page) | ~10 seconds |
| **With API Key** | **10 - 15 minutes** (1.0s delay per page) | ~2 seconds |

While the initial sync runs silently in the background on first boot, providing an API key dramatically accelerates the hydration of your local threat intelligence database.

#### 1. Request a Free API Key
1. Navigate to the [NVD API Key Request Page](https://nvd.nist.gov/developers/request-an-api-key).
2. Enter your email address and complete the captcha.
3. Check your inbox and click the activation link to reveal your API key.

#### 2. Configure Helix
To make the API key permanently available to Helix, add it to your shell's environment variables.

**For Zsh (macOS default):**
```bash
echo 'export NVD_API_KEY="your-actual-api-key-here"' >> ~/.zshrc
source ~/.zshrc
```

**For Bash (Linux default):**
```bash
echo 'export NVD_API_KEY="your-actual-api-key-here"' >> ~/.bashrc
source ~/.bashrc
```

#### 3. Verify Acceleration
Launch Helix and execute the knowledge update command:
```text
/knowledge-update
```
The live TrueColor progress bar will now reflect the accelerated sync speed, bypassing the 6.5-second rate limit and fully indexing ~290,000 CVEs in a fraction of the time.

---

### Manual Build (For Developers)
If you prefer to build and run Helix locally without installing it globally:

```bash
git clone https://github.com/Nibir1/Helix.git
cd Helix
make current   # Builds the optimized binary
./dist/helix   # Launches Helix
```

---

## The "Helix Inversion": One Prompt to Rule Them All

Helix inverts the traditional terminal paradigm. Instead of forcing humans to speak machine, **the machine learns to speak human**. 

Using an advanced **Input Classification Engine** (`internal/shell/classify.go`), Helix analyzes every line you type and dynamically routes it:
- Type `ls -la` or `git status` → Helix recognizes shell structure and executes it safely.
- Type `why is my build failing?` → Helix routes it to the AI Planner.
- Type `/vuln CVE-2024-1234` → Helix queries the local threat intelligence database.

**No mode switching. No prefixes required.** Just type.

---

## What Helix Can Do

### 1. Shell Automation & System Administration
- `find all .go files modified in the last 24 hours`
- `list all large files in this directory and delete the logs`
- `compress the src folder and move it to backup`

### 2. Git Workflows (Safe & Dangerous)
- `increase the version in the README to 1.1.0, then stage, commit, and tag v1.1.0`
- `undo the last commit but keep the changes`
- `force push my changes to origin main` *(Requires typed confirmation: "YES, FORCE PUSH")*

### 3. Package Management
- `install docker`
- `update node to the latest version`
- `uninstall python2` *(Blocks critical system package removal)*

### 4. Defensive Threat Intelligence & Reconnaissance
- `/vuln CVE-2021-44228` *(Fetches CVSS, KEV status, and patch guidance)*
- `/explain "git merge --squash feature-branch"` *(AI-powered defensive analysis with MITRE context)*
- `/scan authorize 192.168.1.10 --reason "Internal pentest scope"`
- `/scan 192.168.1.10` *(Runs nmap/masscan through the authorized recon engine)*

### 5. Enterprise Security & Privacy
- `/sandbox strict` *(Enforces kernel-grade write confinement via Landlock/Seatbelt)*
- `/doctor` *(Surfaces local, telemetry-free crash diagnostics and system health)*
- `/purge` *(Cryptographically wipes all local Helix data, keys, and crash reports)*

---

## Comprehensive Command Reference

Helix exposes a rich set of slash commands for system control, the agentic harness, intelligence gathering, and UX tuning. Every command below is defined in one registry (`cmd/helix/registry_tables.go`), which is also what `/help`, Tab completion, and the did-you-mean suggester read — so the menu cannot drift from the code.

At the prompt: **Tab** completes a slash command (or a path), **`/help <command>`** prints one command's full detail, and a mistyped command suggests the closest matches.

### Core & Navigation
| Command | Description |
| :--- | :--- |
| `/help [command]` | Show this menu, or full detail for one command |
| `/about` | Helix philosophy, banner & creator |
| `/version` | Version, build flavor, and platform |
| `/setup` | Unified setup wizard (identity, AI provider) |
| `/config [key [value]]` | Show or change persisted settings |
| `/cd [dir]` | Change directory (sandbox-aware) |
| `/status` | Session state: RAG, provider, harness, sandbox, hooks |
| `/doctor` | Full system diagnostics, including edge and sidecar checks |
| `/online` | Check internet connectivity |
| `/debug <on\|off>` | Toggle verbose debug logging |

### Session & Context
| Command | Description |
| :--- | :--- |
| `/context` | What the model is being told, and how big it is |
| `/cost` | Model traffic for this session, by purpose |
| `/memory [show\|clear]` | Show or wipe conversation memory |
| `/clear` | Archive and clear the conversation, then clear the screen |
| `/compact [focus]` | Summarize the conversation into one dense turn |
| `/resume [id]` | List archived conversations, or reload one |
| `/export [path]` | Write the conversation to a Markdown transcript |
| `/history [pattern]` | Search this machine's Helix command history |

Nothing here destroys a transcript. `/clear`, `/compact`, `/memory clear`, and `/resume` all archive the conversation to `~/.helix/sessions/` (0600) before replacing it, and `/resume` lists the archive.

`/cost` and `/context` report **estimated** token counts (~4 characters per token). No provider in the registry returns a usage block on the streaming path Helix uses, so an exact count is not available to report — call counts, failures, byte counts, and latency are exact. Helix ships no price table: rates change without notice, and a stale hardcoded rate is worse than an honest token count.

### Agentic Harness
| Command | Description |
| :--- | :--- |
| `/agentic [on\|off\|steps <n>]` | Iterative harness: observe step results and self-correct |
| `/plan <request>` | Show the plan for a request without executing anything |
| `/permissions [mode]` | Approval posture: plan, cautious, ask, or auto |
| `/todo [add\|start\|done\|rm\|...]` | Task list the planner can see |
| `/tools` | The harness tool vocabulary and each tool's gate |
| `/hooks [list\|add\|rm\|test\|...]` | Run your own commands around tool execution |
| `/undo` | Reverse the most recent journalled action |
| `/dry-run` | Toggle command execution preview mode |

**Approval posture** (`/permissions`) layers on top of the risk tiers and never replaces them:

| Mode | Behavior |
| :--- | :--- |
| `plan` | Nothing executes. Steps are printed as a plan with their risk tier. |
| `cautious` | Every command asks first, including low risk. |
| `ask` | Default: low runs, medium asks, high is blocked. |
| `auto` | Medium risk is auto-approved. |

High-risk commands stay blocked in **every** mode, typed confirmations stay typed, the sandbox still validates every command, and the Voice Risk Policy still caps anything arriving by voice. The mode can only change the question Helix asks about commands it was already willing to consider.

**Hooks** (`/hooks`) run your own commands around tool execution — the escape hatch for policy Helix cannot know about ("never touch the prod kubeconfig", "gofmt after any write", "log every push"). See [docs/harness.md](docs/harness.md) for the security model; in short: hooks come only from `~/.helix/hooks.json`, they run *after* every built-in gate, and a blocking pre-hook can subtract permission but never grant it.

### Code & Repository
| Command | Description |
| :--- | :--- |
| `/init [--force]` | Study this repository and write HELIX.md project context |
| `/diff [--staged] [path...]` | Show the working tree diff with a summary |
| `/review [--staged] [path...]` | AI review of the current diff |
| `/commit [message]` | Commit staged work, writing the message from the diff |
| `/git <request>` | Natural-language git operations with safety gates |
| `/web <query\|url>` | Guarded web search or page fetch |
| `/explain <command\|technique>` | Defensive analysis: techniques, detections, mitigations |

`/init` writes a `HELIX.md` that Helix then loads on every turn in that directory tree (`AGENTS.md` and `CLAUDE.md` are recognized too). Like retrieved knowledge and conversation memory, it is injected as a **zero-authority** fenced block: it can inform the planner and can never command it.

`/commit` never stages anything for you — what you staged is what gets committed.

### AI & Providers
Local runtimes — Ollama, llama.cpp, whisper.cpp, Piper — have their own guide: [docs/local_runtimes.md](docs/local_runtimes.md). **Ollama is the local default**; llama.cpp is registered and selectable with `/provider use llamacpp` but is not offered in the first-run menu, since it needs a hand-managed server. It covers which to use, how llama.cpp serves models Ollama already pulled, the llama.cpp/whisper.cpp port collision on 8080, and what the `local-gguf` placeholder was doing wrong.

| Command | Description |
| :--- | :--- |
| `/provider [status\|list\|use <name>\|<name>]` | Switch or inspect the AI provider |
| `/provider-status` | Provider health, keys, failover state, planner transport |
| `/model [list\|use <id>\|<id>]` | Switch or list models on the active provider |
| `/models` | List models the active provider offers |
| `/test-basic-ai` | Smoke test the active AI model |

### RAG & Knowledge Base
| Command | Description |
| :--- | :--- |
| `/rag-status` | RAG indexing progress and vector stats |
| `/rag-reindex` | Trigger a background RAG reindex |
| `/rag-rebuild` | Force a full RAG knowledge base rebuild |
| `/rag-reset` | Wipe all RAG vector data |
| `/knowledge-update` | Fetch latest CVEs, CISA KEV, exploits, MITRE ATT&CK |
| `/knowledge-status` | Knowledge database row counts and last update |
| `/knowledge-reindex` | Rebuild the FTS5 search index |

### Security, Recon & Stealth
| Command | Description |
| :--- | :--- |
| `/vuln <CVE\|EDB\|T-ID\|query>` | Defensive vulnerability intelligence lookup |
| `/scan [authorize\|revoke\|status] <target>` | Reconnaissance against an authorized target |
| `/sandbox [off\|current\|strict]` | Directory confinement for every executed command |
| `/stealth <on\|off>` | Private history mode (suppresses shell history) |
| `/crash [list\|view <n>\|clear]` | Inspect and manage local crash diagnostics |

### First run
The first boot walks three stages, each skippable: **AI provider**, **system packages**, then the **speech chain**. The package stage detects the host's package manager (brew, apt, dnf, pacman, zypper, apk, winget, choco) and offers to install what Helix needs — `sox` for the microphone, `ffmpeg` for the camera. Nothing installs without a separate yes, and the exact command is shown before it runs. Re-run any stage later with `/setup`.

Where Helix does not know a verified package name for your platform, it says so and points at the docs rather than running a guess.

### Live mode — `/blackbox`
`/blackbox on` is Helix awake: microphone open, camera on, replies spoken, and a companion loop that looks at the scene on its own and speaks up when something is worth saying. Say **"manual mode"** at any time to return to the keyboard, or **"turn off your eyes"** to close the camera without ending the conversation.

Eight commands (`/voice`, `/manual`, `/voice-setup`, `/voice-status`, `/wake`, `/say`, `/tts`, `/eyes`) folded into this one; typing an old name prints where it went.

Helix has a **persona**, not a default assistant register: it answers first, keeps replies short because most of them are read aloud, says "I ran it" about things it ran, and will tell you when it thinks you are about to do something unwise. The persona shapes tone only — every safety gate sits downstream of it.

**No Docker required, anywhere.** The local voice chain is whisper.cpp plus Piper, which need a binary and Python. Kokoro is the one optional component that ships as a container; Helix will not install a runtime for it, and marks it `needs docker` in the table rather than walking you into a failed pull.

See [docs/voice.md](docs/voice.md) for the spoken-command vocabulary, what voice deliberately cannot reach, and the honest limits (capture is half-duplex — the mic is muted while Helix speaks, so a reply is interrupted with Ctrl+C rather than by talking over it).

| Command | Description |
| :--- | :--- |
| `/blackbox [on\|off\|status\|setup\|look\|eyes\|wake\|tts\|say]` | Live mode — Helix listens, watches, answers, and speaks up |
| `/listen [seconds]` | Record and transcribe one clip (max 60s) |
| `/mictest` | 3s self-test: is the mic actually being heard? |

Subcommands:

- **on** / **off** — go live, or return to the keyboard
- **status** — one report: hearing, sight, speech, wake, companion
- **setup** — configure the STT/TTS providers with live pricing
- **look** *[question]* — capture one frame now and answer a question about it
- **eyes** `on|off` — camera only, without entering or leaving live mode
- **wake** `on|off` — hands-free wake phrase between turns
- **tts** `on|off` — whether ordinary replies are spoken aloud
- **say** *text* — speak text through the TTS chain

### Utilities
| Command | Description |
| :--- | :--- |
| `/audio <on\|off>` | Toggle tonal audio feedback |
| `/typewrite-all <on\|off>` | Typewriter effect for ALL output, not just AI replies |

### DANGER ZONE
| Command | Description |
| :--- | :--- |
| `/purge` | Wipe ALL Helix data (keys, DBs, caches, tasks, hooks, archives) for a fresh start |

### Aliases
`/?` `/h` `/sos` → `/help` · `/v` → `/version` · `/reset` → `/clear` · `/usage` → `/cost` · `/mode` → `/permissions` · `/intel` → `/vuln`

---

## AI & Planner System

Helix uses a **full agent-style planner** that outputs strict JSON.

### Unified Planner Intent System
The planner always returns a JSON `Plan`:

```json
{
  "intent": "chat" | "shell" | "git" | "package" | "multi_step",
  "steps": [
    {
      "tool": "response" | "shell" | "git" | "package" | "recon" | "web",
      "message": "...",
      "command": "...",
      "action": "...",
      "args": { "key": "value" }
    }
  ]
}
```

### Ultra‑Strict Planner Prompt
The planner is guided by a **very strict system prompt** (`internal/ai/planner.go`) that enforces:
- **JSON‑only output** (no markdown, no backticks, no commentary).
- First character **must** be `{`, last character **must** be `}`.
- No trailing commas, no partial fields, no truncated objects.
- Strong rules about which tools are allowed, which git actions are safe vs dangerous, and which commands are forbidden at the shell level.

### Robust Plan Parsing
`ParsePlanFromModelOutput` includes:
- JSON extraction that strips accidental ``` fences if they sneak in.
- A tolerant `rawPlan` type with `map[string]interface{}` for `args`.
- A safe conversion layer: Arrays like `["README.md"]` are normalized to `"README.md"`.
- A `fixPlan` pass that normalizes intent names, maps `upgrade` → `update` for packages, and collapses noisy arg keys.
- A `validatePlan` pass that drops malformed steps, enforces allowed actions per tool, and strips illegal fields.

If the planner ever returns junk, Helix will **drop invalid steps** or fall back to a plain chat response.

---

## Shell Safety Subsystem (Multi‑Layer)

Arbitrary shell execution is **heavily guarded** by a 5-stage pipeline in `internal/commands/safety/` and `internal/confinement/`.

### 1. ValidateAndCleanCommand
Every shell step flows through `ValidateAndCleanShellCommand`:
- **Unicode hazard detection:** Blocks zero-width characters, bidi-spoofing, and control characters.
- **Quick unmatched quote detection** with auto‑fix attempt.
- **Strict balanced quote & brace validation**.
- **Extra high‑level rules:** Blocks `curl ... | sh`, `wget ... | bash`, `eval`, and `mkfs`.
- **Light path sanity checks:** Catches `rm -rf /` and parent directory traversals (`..`) combined with write operators.

### 2. Risk Classification (Low / Medium / High)
`AnalyzeShellRisk` classifies commands:
- **Low** – harmless, read‑only (e.g. `ls`, `cat`). Executes directly.
- **Medium** – file‑modifying (e.g. `sed -i`, redirections `>`). Shows reasons and asks: `Execute anyway? [y/N]`
- **High** – catastrophic patterns (e.g. `rm -rf`, pipe‑into‑shell). Hard-blocked.

### 3. Directory Sandbox (Advisory)
All shell commands execute via a `DirectorySandbox`:
- Prevents traversal outside the allowed root.
- Resolves symlinks and handles case-insensitivity (macOS/Windows).
- Allows READ-ONLY absolute paths anywhere, but MODIFY/WRITE actions are blocked outside the sandbox.

### 4. Kernel-Grade Confinement (`/sandbox strict`)
When strict mode is enabled, write/delete operations outside the jail root are denied **by the OS kernel**, not by string matching:
- **macOS:** Seatbelt (`sandbox-exec`) profile enforcement.
- **Linux:** bubblewrap namespaces (preferred) or the **Landlock LSM** via pure-Go, CGO-free raw syscalls using a `--confined-child` re-exec architecture.
- **Unsupported platforms:** Graceful advisory fallback with a visible warning.

---

## Threat Intelligence & RAG Pipeline

Helix maintains a local SQLite + FTS5 + Vector database updated with live threat feeds (`internal/rag/`).

- **NVD CVEs:** Rolling 120-day window with checkpointing, rate-limit handle, and browser-spoofing.
- **CISA KEV:** Known Exploited Vulnerabilities catalog.
- **Exploit-DB:** Sanitized exploit references for defensive validation.
- **MITRE ATT&CK:** Technique mappings for detection engineering.
- **MAN Pages:** Background indexing of 900+ system commands using 6 parallel workers.
- **First-Run Bootstrap:** `KnowledgeBootstrap` runs silently in the background on first boot to populate the DB without blocking the CLI.

### Instruction Firewall (Prompt-Injection Hardening)
Retrieved knowledge is treated as untrusted **data** with zero authority. The RAG pipeline is defended by five layered controls:
1. **Structured-fields-only context:** Raw text never reaches the planner; only sanitized structured fields wrapped in `authority="data-only"` fences.
2. **Sanitization:** Invisible/bidi Unicode, markdown fences, and imperative injection patterns ("ignore previous instructions") are stripped.
3. **Canary Honeypot:** A per-request random token is embedded in the context; if the model echoes it, execution aborts with an injection alert.
4. **Critic Pass:** A fail-closed, low-temperature JSON call validates the proposed shell plan against the *user request alone*.
5. **Provenance Escalation:** Plan commands carrying tokens sourced from retrieved context (but absent from user input) are forced to Medium risk (mandatory confirmation).

---

## Git Assistant (Safe + Dangerous Flows)

Helix has a dedicated **GitManager** with two faces: `/git` mode (natural language) and Agent mode (structured JSON actions).

### Safe Git Planner Actions
- `commit`, `tag`, `add`, `checkout`, `create-branch`.
- Commit messages are written to a **temporary `.helix-commit-msg.txt` file** and passed via `git commit -F` to avoid shell-escaping issues.

### Dangerous Git Actions
For advanced workflows, the planner may emit dangerous actions. These require **typed confirmation**:
- Force push: `Type "YES, FORCE PUSH" to confirm`
- Hard reset: `Type "YES, RESET HARD" to confirm`
- Clean: `Type "YES, CLEAN WORKTREE" to confirm`
- Delete main branch: `Type "YES, DELETE MAIN" to confirm`

---

## Safe Package Management

Instead of letting the LLM call `apt`/`brew` directly, Helix exposes a `package` tool.
- `IsPackageActionSafe` blocks obviously dangerous operations (e.g., uninstalling `libc6`, `systemd`, or `bash` on Linux).
- `HandlePackageCommand` routes to the appropriate system package manager (apt, brew, choco, winget, pacman) through the sandbox.

---

## Reconnaissance & Stealth

### Authorized Recon Engine (`internal/recon/`)
Helix includes a built-in multi-tool recon orchestrator (`nmap`, `masscan`, `ffuf`, `amass`).
- **Written Scope Enforcement:** Targets *must* be explicitly authorized with a written reason (`/scan authorize <ip> --reason "..."`) before scanning.
- **Dangerous Flag Blocking:** Prevents accidental network floods (e.g., blocking `masscan --rate 1000000`).

### Stealth Execution (`internal/stealth/`)
- `/stealth on` routes commands through a memory-only executor.
- Suppresses local shell history (`HISTFILE=/dev/null`, `HISTSIZE=0`) for private execution.
- Strictly local privacy; no anti-forensic log wiping.

---

## Terminal UX & Synthetic Audio

### SYNAPSE Prompt (`internal/shell/prompt.go`)
- TrueColor animated prompt with git telemetry, glitch effects, and transient history rendering.
- Width-safe glyphs and in-place resize healing (no duplicate prompt lines on SIGWINCH).
- Semantic syntax highlighting (`internal/utils/syntax.go`) colorizes 10+ token types in real-time as you type.

### Synthetic Tonal Audio (`internal/audio/`)
A custom `beep`/`oto` synthesizer generates Tron-style audio feedback:
- **350Hz percussive data-tap** synchronized perfectly with the AI typewriter effect.
- **880Hz high-tech alert ping** for modals and confirmations.
- **110Hz sawtooth buzz** for errors.
- 50ms buffer latency for tight rhythm sync.

---

## Non-Interactive & Scripting Mode

Helix isn't just an interactive TUI; it's a fully compliant shell bridge (`cmd/helix/noninteractive.go`).

```bash
# Execute a single command
helix -c "find . -name '*.log' -delete"

# Pipe a script into Helix (respects safety tiers)
cat deploy.sh | helix

# Execute a script file
helix ./scripts/build.sh
```
*Note: High-risk commands are blocked in non-interactive mode. Medium-risk commands require `HELIX_AUTOCONFIRM=1` to bypass interactive prompts.*

---

## Enterprise Hardening & Supply Chain Security

Helix is built with a verified, mathematically defensible supply chain and rigorous testing harnesses:

- **SBOM & Cryptographic Signing:** Every release artifact ships with an SPDX Software Bill of Materials (via `syft`) and is cryptographically signed using Sigstore keyless signing (`cosign`).
- **Continuous Fuzzing:** The safety surface (shell validation, JSON planner parsing, sandbox path resolution) is continuously fuzzed with invariant assertions to prevent ReDoS and state-machine bypasses.
- **E2E TTY Harness:** A pseudo-terminal (PTY) test suite boots the real Helix binary against a mock provider, proving the safety pipeline end-to-end with zero real AI and zero network.
- **Telemetry-Free Crash Diagnostics:** Panics and fatal signals generate local, 0600, secret-redacted JSON crash reports (`~/.helix/crash-*.json`). The diagnostics package imports zero networking primitives (grep-verified in CI), ensuring field failures are debuggable without violating user privacy.

### Verifying Official Releases
You can verify the integrity and provenance of any downloaded Helix binary using `cosign` and `syft`:
```bash
# Verify the Sigstore signature
cosign verify-blob \
  --certificate Helix_Linux_x86_64.tar.gz.pem \
  --signature Helix_Linux_x86_64.tar.gz.sig \
  --certificate-identity-regexp "https://github.com/Nibir1/Helix/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  Helix_Linux_x86_64.tar.gz

# Inspect the SBOM
syft Helix_Linux_x86_64.tar.gz
```
---

## Architecture Overview

```text
Helix/
├── cmd/helix              # CLI entrypoint, handlers, and non-interactive bridge
├── internal/
│   ├── ai/                # Planner, OpenAI/local model integration, strict JSON parsing
│   ├── agent/             # Agent orchestrator, Instruction Firewall, and step executor
│   ├── audio/             # Synthetic tonal audio engine (beep/oto)
│   ├── commands/          # Shell safety, git manager, package manager, sandbox
│   ├── confinement/       # Kernel-grade write confinement (Seatbelt, Landlock, bwrap)
│   ├── diagnostics/       # Telemetry-free, redacted crash reporting
│   ├── shell/             # SYNAPSE prompt, raw-mode reader, input classifier, TTY hardening
│   ├── rag/               # MAN page indexing, vector store, SQLite FTS5, NVD/KEV/MITRE updaters
│   ├── recon/             # Authorized multi-tool reconnaissance engine
│   ├── stealth/           # Memory-only private history execution
│   ├── ux/                # Terminal UX (typewriter, prompts, colors)
│   └── utils/             # Quote/brace validation, syntax highlighting, history, interrupts
├── tests/e2e/             # PTY-based end-to-end TTY harness
├── dist/                  # Built binaries (make current)
└── scripts/               # Developer, build, and install scripts
```

---

## AI Backends & Provider Support

Helix supports a massive array of AI providers out of the box, managed via `/setup` or `/provider`:

1. **Remote APIs:** OpenAI, Anthropic, DeepSeek, Kimi, Qwen, GLM.
2. **Local Runtimes:** Ollama (auto-installs and pulls models).

API keys are securely stored in `~/.helix/secrets.json` with `0600` permissions, or passed via environment variables.

---

## Helix — Complete Feature & Capability List

### AI & RAG Intelligence
* RAG-enhanced natural language command generation over 900+ indexed MAN pages
* Background RAG indexing with 6 parallel workers without blocking CLI usage
* Live Threat Intelligence pipeline (NVD, CISA KEV, Exploit-DB, MITRE ATT&CK)
* Defensive vulnerability intel (`/vuln`) with CVSS, KEV status, and patch guidance
* Command explanation engine (`/explain`) with MITRE ATT&CK context mapping
* Automatic fallback to standard chat when planning fails
* **Instruction Firewall** with canary honeypots and fail-closed critic passes

### Planner, Agent System & Tool Protocol
* Unified multi-tool agent system: response, shell, git, package, recon, web
* Ultra-strict JSON planner protocol with schema enforcement and truncation-resistance
* Dual-provider inference: local Ollama + remote APIs
* Argument normalization (array flattening, trimming, synonym resolution)
* Auto-intent classification: chat, shell, git, package, multi_step

### Shell Execution & Confinement (Safety-First)
* Directory sandbox with safe-path enforcement and symlink resolution
* Multi-layer safety pipeline (Unicode validation → risk scoring → sandbox → execution)
* **Kernel-Grade Confinement** (Landlock/Seatbelt) for `/sandbox strict`
* Automatic quote/brace/syntax repairs for minor command issues
* Detection of destructive patterns (e.g., `rm -rf /`, `curl | sh`, `eval`)
* Medium-risk command confirmation prompts (`sed -i`, redirections)
* Cross-shell integration: bash, zsh, fish, PowerShell, CMD

### Git Automation (Safe + Dangerous Modes)
* Safe Git actions: commit, add, tag, checkout, create-branch
* High-risk Git actions requiring typed confirmations (push --force, reset --hard, clean -fdx)
* Commit messaging via safe temporary file to prevent shell injection
* Multi-step Git flows: update → stage → commit → tag

### System Architecture & Infrastructure
* Ollama integration (auto-installs and pulls recommended models based on RAM)
* Parallel RAG indexing with persistent vector stores for instant reload
* OS & shell auto-detection with TTY hardening (SIGTTIN/SIGTTOU handling)
* Non-interactive shell bridge for pipes and scripts
* **Telemetry-Free Crash Diagnostics** (local, redacted, opt-outable)

### Enterprise Hardening & Testing
* **Supply Chain Security:** SBOM generation (Syft) and Sigstore keyless signing (Cosign)
* **Continuous Fuzzing:** Invariant-aware fuzzing of all safety and planner parsers
* **E2E TTY Harness:** PTY-based integration tests with mock providers
* **Vulnerability Scanning:** Automated `govulncheck` and CodeQL SAST in CI

### UX & Developer Productivity
* SYNAPSE TrueColor animated prompt with glitch effects and transient history
* Semantic syntax highlighting (10+ token types) in real-time
* Synthetic tonal audio feedback (350Hz tap, 880Hz alert, 110Hz error)
* Animated typewriter effects synchronized with audio
* In-place terminal resize healing (no duplicate prompt lines)

---

## Why This Project Is User‑Friendly

Helix is intentionally structured as a **systems‑level AI project**, not just a wrapper:

- **Real Tool‑Use & Safety:** Implements actual sandboxing, risk-tiering, kernel confinement, and typed confirmations for dangerous paths.
- **Architectural Thinking:** Clear separation between planning, safety, execution, and telemetry layers.
- **Modern AI Practices:** Strict JSON tool calling, truncation-resistant prompt engineering, RAG augmentation, instruction firewalls, and local/remote model fallbacks.
- **Cybersecurity Focus:** Integrates live NVD/KEV pipelines, MITRE ATT&CK mappings, authorized recon engines, and prompt-injection defenses.
- **Enterprise Assurance:** Verifiable supply chain, continuous fuzzing, and telemetry-free diagnostics.
- **Written in Go:** Demonstrates mastery of concurrency (goroutines for background indexing/audio), raw TTY manipulation, CGO-free builds, Landlock syscalls, and modular package design.

**Where to start reading the code:**
- `internal/ai/planner.go` (Strict JSON protocol)
- `internal/shell/classify.go` (Unified input routing)
- `internal/commands/safety/shell.go` (Multi-layer risk analysis)
- `internal/confinement/confine_linux.go` (Kernel-grade Landlock enforcement)
- `internal/agent/firewall.go` (Prompt-instruction firewall)
- `internal/rag/updater.go` (Live threat intelligence pipeline)
- `internal/audio/audio.go` (Synthetic tonal feedback)

---

## Contributing

Ideas, issues, and PRs are welcome:

1. Fork the repo
2. Create a feature branch
3. Run your changes locally with `make start`
4. Open a PR with a clear description and demo steps

---

## License

Helix is released under the **MIT License** (see `LICENSE`).  
LLaMA and other model weights have **their own licenses** — please review them before use.

---

## Developer Spotlight

**Nahasat Nibir** — Building intelligent, high‑performance developer tools, AI‑powered systems, and adversarial platforms in Go and Rust.

- **GitHub:** https://github.com/Nibir1
- **LinkedIn:** https://www.linkedin.com/in/nibir-1/
- **ArtStation:** https://www.artstation.com/nibir

---

<div align="center">
<strong>Helix</strong> — Making the command line safer, smarter, and more approachable with AI.
</div>

---