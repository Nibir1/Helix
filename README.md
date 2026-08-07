# Helix 🤖  
**An Intelligent, AI‑Powered CLI Assistant, Red-Team Platform, and Native Shell**

---

[![Helix Demo](https://img.youtube.com/vi/5IE-ztGXIS8/maxresdefault.jpg)](https://youtu.be/5IE-ztGXIS8)

> 📺 **[Watch the full end-to-end demo](https://youtu.be/5IE-ztGXIS8)** featuring core functionalities.

---

![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)
![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)

Helix is an **AI-powered command‑line assistant and adversarial cybersecurity platform** that turns natural language into **safe, executable actions**. It bridges the gap between human intent and machine execution, combining local LLM inference, retrieval-augmented generation (RAG), live threat intelligence, and strict safety pipelines.

It combines:
- **Multi-Provider AI** (OpenAI, Anthropic, DeepSeek, Ollama, llama.cpp, and more)
- **Live Threat Intelligence** (NVD, CISA KEV, Exploit-DB, MITRE ATT&CK)
- **RAG over System Docs** (900+ indexed MAN pages and CLI tools)
- **A Multi-Layer Safety & Sandbox Engine** around shell, git, packages, and recon
- **Synthetic Tonal Audio** for immersive, synchronized terminal feedback

It’s built as a **portfolio‑grade systems project** in Go, demonstrating real‑world skills in AI integration, sandboxed execution, strict JSON planner protocols, memory-only stealth execution, and live threat intelligence pipelines.

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
Don't want to build from source? Download the latest pre-compiled binary, checksums, and archives for your OS directly from the **[Releases Page](https://github.com/Nibir1/Helix/releases)**.

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

---

## Comprehensive Command Reference

Helix exposes a rich set of slash commands for system control, intelligence gathering, and UX tuning.

### Core & Navigation
| Command | Description |
| :--- | :--- |
| `/help` | Show the SOS protocol and command menu. |
| `/about` | Display the Helix philosophy, ASCII banner, and creator info. |
| `/setup` | Unified setup wizard (Identity, AI Provider configuration). |
| `/status` | Check background RAG indexing, AI provider, and audio engine status. |
| `/doctor` | Run full system diagnostics (DB ping, network, OS, sandbox). |
| `/online` | Check internet connectivity for remote AI and threat feeds. |
| `/debug <on\|off>` | Toggle verbose byte-level debug logging. |
| `/cd <dir>` | Change directory (sandbox-aware). |

### AI & Providers
| Command | Description |
| :--- | :--- |
| `/provider <name>` | Switch AI provider (openai, anthropic, ollama, llamacpp, etc.). |
| `/model <id>` | Switch the active AI model. |
| `/provider-status` | Show detailed provider health and API key status. |
| `/test-basic-ai` | Smoke test the active AI model with a simple prompt. |

### Threat Intelligence & RAG
| Command | Description |
| :--- | :--- |
| `/vuln <query>` | Defensive vulnerability intel (CVE/EDB/MITRE lookup). |
| `/explain <cmd>` | AI-powered defensive analysis of a command or technique. |
| `/knowledge-update` | Fetch latest CVEs, CISA KEV, Exploits, and MITRE data. |
| `/knowledge-stats` | Show knowledge database row counts. |
| `/rag-status` | Show RAG indexing progress and vector stats. |
| `/rag-reindex` | Trigger background RAG reindex. |
| `/rag-rebuild` | Force full RAG knowledge base rebuild (with live progress). |
| `/rag-reset` | Wipe all RAG vector data. |

### Security, Recon & Stealth
| Command | Description |
| :--- | :--- |
| `/scan authorize <ip>` | Authorize recon target with a written scope/reason. |
| `/scan <ip>` | Run nmap/masscan on an authorized target. |
| `/sandbox <mode>` | Directory confinement (`off`, `current`, `strict`). |
| `/stealth <on\|off>` | Private history mode (suppresses shell history, memory-only). |
| `/dry-run` | Toggle command execution preview mode. |
| `/audio <on\|off>` | Toggle synthetic tonal audio feedback. |

### Git & Utilities
| Command | Description |
| :--- | :--- |
| `/git <request>` | Natural language git operations with safety confirmations. |

### DANGER ZONE
| Command | Description |
| :--- | :--- |
| `/purge` | Wipe ALL Helix data (keys, DBs, caches) for a fresh start. |

---

## AI & Planner System (Phase 3.x)

Helix uses a **full agent-style planner** that outputs strict JSON.

### Unified Planner Intent System
The planner always returns a JSON `Plan`:

```json
{
  "intent": "chat" | "shell" | "git" | "package" | "multi_step",
  "steps": [
    {
      "tool": "response" | "shell" | "git" | "package" | "recon",
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

Arbitrary shell execution is **heavily guarded** by a 4-stage pipeline in `internal/commands/safety/`.

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

### 3. Directory Sandbox (Mode C)
All shell commands execute via a `DirectorySandbox`:
- Prevents traversal outside the allowed root.
- Resolves symlinks and handles case-insensitivity (macOS/Windows).
- Allows READ-ONLY absolute paths anywhere, but MODIFY/WRITE actions are blocked outside the sandbox.

---

## Git Assistant (Safe + Dangerous Flows)

Helix has a dedicated **GitManager** with two faces: `/git` mode (natural language) and Agent mode (structured JSON actions).

### Safe Git Planner Actions
- `commit`, `tag`, `add`, `checkout`, `create-branch`.
- Commit messages are written to a **temporary `.helix-commit-msg.txt` file** and passed via `git commit -F` to avoid shell-escaping issues.

### Dangerous Git Actions (Option C)
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

## Threat Intelligence & RAG Pipeline

Helix maintains a local SQLite + FTS5 + Vector database updated with live threat feeds (`internal/rag/`).

- **NVD CVEs:** Rolling 120-day window with checkpointing, rate-limit handling, and browser-spoofing.
- **CISA KEV:** Known Exploited Vulnerabilities catalog.
- **Exploit-DB:** Sanitized exploit references for defensive validation.
- **MITRE ATT&CK:** Technique mappings for detection engineering.
- **MAN Pages:** Background indexing of 900+ system commands using 6 parallel workers.
- **First-Run Bootstrap:** `KnowledgeBootstrap` runs silently in the background on first boot to populate the DB without blocking the CLI.

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

## Architecture Overview

```text
Helix/
├── cmd/helix              # CLI entrypoint, handlers, and non-interactive bridge
├── internal/
│   ├── ai/                # Planner, OpenAI/local model integration, strict JSON parsing
│   ├── agent/             # Agent orchestrator (intent classification + step executor)
│   ├── audio/             # Synthetic tonal audio engine (beep/oto)
│   ├── commands/          # Shell safety, git manager, package manager, sandbox
│   ├── shell/             # SYNAPSE prompt, raw-mode reader, input classifier, TTY hardening
│   ├── rag/               # MAN page indexing, vector store, SQLite FTS5, NVD/KEV/MITRE updaters
│   ├── recon/             # Authorized multi-tool reconnaissance engine
│   ├── stealth/           # Memory-only private history execution
│   ├── ux/                # Terminal UX (typewriter, prompts, colors)
│   └── utils/             # Quote/brace validation, syntax highlighting, history
├── dist/                  # Built binaries (make current)
└── scripts/               # Developer, build, and install scripts
```

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

## AI Backends & Provider Support

Helix supports a massive array of AI providers out of the box, managed via `/setup` or `/provider`:

1. **Remote APIs:** OpenAI, Anthropic, DeepSeek, Kimi, Qwen, GLM, Custom OpenAI-Compatible.
2. **Local Runtimes:** Ollama (auto-installs and pulls models), llama.cpp (auto-clones and builds from source).

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

### Planner, Agent System & Tool Protocol
* Unified multi-tool agent system: response, shell, git, package, recon
* Ultra-strict JSON planner protocol with schema enforcement and truncation-resistance
* Dual-provider inference: local llama.cpp/Ollama + remote APIs
* Argument normalization (array flattening, trimming, synonym resolution)
* Auto-intent classification: chat, shell, git, package, multi_step

### Shell Execution System (Safety-First)
* Directory sandbox with safe-path enforcement and symlink resolution
* Multi-layer safety pipeline (Unicode validation → risk scoring → sandbox → execution)
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
* Full llama.cpp integration (auto-builds from source if missing)
* Ollama integration (auto-installs and pulls recommended models based on RAM)
* Parallel RAG indexing with persistent vector stores for instant reload
* OS & shell auto-detection with TTY hardening (SIGTTIN/SIGTTOU handling)
* Non-interactive shell bridge for pipes and scripts

### UX & Developer Productivity
* SYNAPSE TrueColor animated prompt with glitch effects and transient history
* Semantic syntax highlighting (10+ token types) in real-time
* Synthetic tonal audio feedback (350Hz tap, 880Hz alert, 110Hz error)
* Animated typewriter effects synchronized with audio
* In-place terminal resize healing (no duplicate prompt lines)

---

## Why This Project Is User‑Friendly

Helix is intentionally structured as a **systems‑level AI project**, not just a wrapper:

- **Real Tool‑Use & Safety:** Implements actual sandboxing, risk-tiering, and typed confirmations for dangerous paths.
- **Architectural Thinking:** Clear separation between planning, safety, execution, and telemetry layers.
- **Modern AI Practices:** Strict JSON tool calling, truncation-resistant prompt engineering, RAG augmentation, and local/remote model fallbacks.
- **Cybersecurity Focus:** Integrates live NVD/KEV pipelines, MITRE ATT&CK mappings, and authorized recon engines.
- **Written in Go:** Demonstrates mastery of concurrency (goroutines for background indexing/audio), raw TTY manipulation, CGO-free builds, and modular package design.

**Where to start reading the code:**
- `internal/ai/planner.go` (Strict JSON protocol)
- `internal/shell/classify.go` (Unified input routing)
- `internal/commands/safety/shell.go` (Multi-layer risk analysis)
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