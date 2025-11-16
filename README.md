# Helix 🤖  
**An Intelligent, AI‑Powered CLI Assistant That Understands Natural Language & System Documentation**

![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)
![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)

Helix is an **AI-powered command‑line assistant** that turns natural language into **safe, executable actions**.  
It combines:

- 🧠 **Local llama.cpp models** (privacy‑friendly, offline)
- ☁️ **OpenAI planner support** for high‑quality reasoning
- 📚 **RAG over system docs** so it actually *knows* your tools
- 🛡️ **A multi-layer safety engine** around shell, git & packages

It’s built as a **portfolio‑grade systems project** in Go, demonstrating real‑world skills in:

- AI integration & tool‑calling
- sandboxed command execution
- planner design & strict JSON protocols
- git + package workflows from natural language

---

## ✨ What Helix Can Do

You talk. Helix figures out *how* to do it safely.

Examples of things you can type:

- **Plain chat**
  - `why is the sky blue?`
- **Shell automation**
  - `find all .go files modified in the last 24 hours`
  - `list all files in this folder`
- **Git workflows**
  - `increase the version in the README to 1.1.0, then stage, commit, and tag v1.1.0`
  - `undo the last commit but keep the changes`
  - `force push my changes to origin main`
- **Package management**
  - `install git`
  - `update node`
  - `uninstall docker`
- **Documentation/Q&A**
  - `/explain "git merge --squash feature-branch"`
  - `/ask "how do I set up an nginx reverse proxy?"`

Under the hood, Helix:

1. Calls a planner model (local or OpenAI).
2. Produces a **strict JSON plan** with steps.
3. Runs the steps through a **safety subsystem**.
4. Executes shell / git / package actions **inside a sandbox**.
5. Streams back what it did in a friendly, colorized UX.

---

## 🧠 AI & Planner System (Phase 3.x)

Helix now has a **full agent-style planner** instead of only a “/cmd” translator.

### Unified Planner Intent System

The planner always returns a JSON `Plan`:

```json
{
  "intent": "chat" | "shell" | "git" | "package" | "multi_step",
  "steps": [
    {
      "tool": "response" | "shell" | "git" | "package",
      "message": "...",
      "command": "...",
      "action": "...",
      "args": { "key": "value" }
    }
  ]
}
```

Supported tools:

- `response` – normal LLM reply
- `shell` – run shell commands via sandbox
- `git` – structured git operations (with safety)
- `package` – cross‑platform install/update/remove

`multi_step` plans can chain these together, e.g.:

```json
{
  "intent": "multi_step",
  "steps": [
    { "tool": "shell", "command": "perl -pi -e "s/1.0.0/1.1.0/g" README.md" },
    { "tool": "git",   "action": "add",    "args": { "paths": ["README.md"] } },
    { "tool": "git",   "action": "commit", "args": { "message": "Update version in README to 1.1.0" } },
    { "tool": "git",   "action": "tag",    "args": { "name": "v1.1.0" } }
  ]
}
```

### Ultra‑Strict Planner Prompt

The planner is guided by a **very strict system prompt** that enforces:

- **JSON‑only output** (no markdown, no backticks, no commentary).
- First character **must** be `{`, last character **must** be `}`.
- No trailing commas, no partial fields, no truncated objects.
- Strong rules about:
  - which tools are allowed,
  - which git actions are safe vs dangerous,
  - how package operations must be represented,
  - which commands are forbidden at the shell level.

This drastically reduces “LLM did something weird” failure modes.

### Robust Plan Parsing

`internal/ai/planner.go` includes:

- JSON extraction that strips accidental ``` fences if they sneak in.
- A tolerant `rawPlan` type with `map[string]interface{}` for `args`.
- A safe conversion layer:
  - Arrays like `["README.md"]` are normalized to `"README.md"` or `"a b c"`.
  - All args are converted to strings and trimmed.
- A `fixPlan` pass that:
  - normalizes intent names, tool names, and actions,
  - maps `upgrade` → `update` for packages,
  - collapses noisy arg keys (e.g. `"package_name"` → `"name"`).
- A `validatePlan` pass that:
  - drops malformed steps,
  - enforces allowed actions per tool,
  - strips illegal fields (e.g. shell steps can’t have `args`).

If the planner ever returns junk, Helix will **drop invalid steps** or fall back to a plain chat response.

---

## 🛡️ Shell Safety Subsystem (Multi‑Layer)

Arbitrary shell execution is **heavily guarded**.

### 1. ValidateAndCleanCommand

Every shell step flows through `ValidateAndCleanCommand`:

- Trims and normalizes input.
- **Quick unmatched quote detection** with auto‑fix attempt.
- **Strict balanced quote & brace validation**.
- Delegates to `utils.ValidateCommand` for core malicious pattern checks.
- Adds extra high‑level rules in `extraDangerousPatternChecks`, including:
  - Blocking `curl ... | sh`, `wget ... | bash`, and `| zsh` patterns.
  - Blocking `eval` and dangerous `xargs sh` patterns.
  - Blocking `mkfs` and extremely broad `chmod 777 /` / `chown root /` styles.
- Performs **light path sanity checks** to catch:
  - `rm -rf` on `/`, `./` or `*`,
  - parent directory traversals (`..`) combined with write operators (`>`, `>>`, `mv`, `cp`).

There’s also byte‑level debug logging to inspect weird Unicode or encoding issues when needed.

### 2. Risk Classification (Low / Medium / High)

`AnalyzeShellRisk` classifies commands:

- **Low** – harmless, read‑only, or clearly safe (e.g. `ls`, `cat README.md`).
- **Medium** – file‑modifying but not obviously catastrophic:
  - `perl -pi -e "s/.../.../g" file`
  - redirections (`>`, `>>`)
  - `tee` pipelines
- **High** – patterns that should almost never be automated:
  - broad `rm -rf`
  - pipe‑into‑shell installers
  - filesystem‑formatting commands

The Agent behavior:

- **Low**: executes directly in the sandbox.
- **Medium**: shows reasons and asks:
  > Execute anyway? [y/N]
- **High**: is rejected up‑front with a clear error.

### 3. Directory Sandbox

All shell commands execute via a `DirectorySandbox`:

- Prevents traversal outside the allowed root.
- Rejects commands with raw absolute paths when they are unsafe.
- Enforces a final layer of defense even if previous checks miss something.

---

## 🌿 Git Assistant (Safe + Dangerous Flows)

Helix has a dedicated **GitManager** with two faces:

1. **/git mode** – natural language git workflows.
2. **Agent mode** – structured git actions from the planner.

### Safe Git Planner Actions

The planner can safely emit:

- `commit` – `args.message`
- `tag` – `args.name`
- `add` – `args.paths`
- `checkout` – `args.branch`
- `create-branch` – `args.branch`

Commit messages are written to a **temporary `.helix-commit-msg.txt` file** and passed to:

```bash
git commit -F .helix-commit-msg.txt
```

This avoids shell‑escaping issues for messages with spaces or symbols.  
Helix also checks for **staged changes** before committing to avoid empty commits.

### Dangerous Git Actions (Option C)

For advanced workflows, the planner may also emit:

- `push` (with optional `force` flag)
- `reset-hard`
- `clean`
- `delete-branch`

These are never executed blindly:

- Arguments are validated:
  - No absolute paths,
  - No `; | & > < \` $` shell metacharacters,
  - No `..` traversal.
- Critical operations require **typed confirmation**:

  - Force push:
    > Type "YES, FORCE PUSH" to confirm

  - Hard reset:
    > Type "YES, RESET HARD" to confirm

  - Clean:
    > Type "YES, CLEAN WORKTREE" to confirm

  - Delete main branch:
    > Type "YES, DELETE MAIN" to confirm

If the phrase doesn’t match exactly, the operation is cancelled.

### Higher‑Level /git Workflows

For direct `/git` usage, Helix also supports hand‑crafted flows:

- Merge with squash and “accept all incoming” changes.
- Undo last commit but keep changes (`reset --soft HEAD~1`).
- Clean untracked files (`git clean -fd`).
- Stash everything, including untracked.

These flows:

- Show you the **exact commands** that will be run.
- Explain the risks.
- Ask for confirmation before each multi‑step sequence.

---

## 📦 Safe Package Management

Instead of letting the LLM call `apt`/`brew` directly, Helix exposes a `package` tool:

```json
{
  "tool": "package",
  "action": "install" | "update" | "remove",
  "args": { "name": "git" }
}
```

The planner is instructed to:

- Always treat “install/update/remove/uninstall X” as **package management**, even if `X` is `git`, `node`, `docker`, etc.
- Never emit package commands under `shell` or `git`.

On the Helix side:

- `IsPackageActionSafe` blocks obviously dangerous operations (e.g. uninstalling critical tools on certain OSes).
- `HandlePackageCommand` routes to the appropriate system package manager (apt, brew, etc.) through the sandbox.
- The same execution pipeline ensures commands are visible, confirmable, and logged.

---

## 🎨 Terminal UX & Developer Experience

Helix is designed to feel great in the terminal:

- Colorized output with clear emoji for each subsystem:
  - 🤖 planner & agent intent
  - 🖥️ shell commands
  - 🌿 git actions
  - 📦 package operations
- Optional **typewriter effect** for AI responses.
- Explicit step headers:
  - `--- Step 1 ---`, `--- Step 2 ---`, etc.
- Rich debug logs when needed (planner raw JSON, shell byte dumps).
- Sensible fallbacks:
  - If the planner errors, Helix falls back to a simple chat response using the same model.

---

## 🏗 Architecture Overview

```text
Helix/
├── cmd/helix              # CLI entrypoint
├── internal/
│   ├── ai/                # Planner, OpenAI/local model integration
│   │   ├── planner.go     # JSON planning, validation, normalization
│   │   └── openai.go      # OpenAI Chat Completions client
│   ├── agent/             # Agent orchestrator (intent + step executor)
│   ├── commands/          # Shell, git, package managers & safety
│   │   ├── shell_safety.go
│   │   ├── git.go
│   │   └── pkgmanager.go
│   ├── shell/             # Env detection and sandbox wrappers
│   ├── rag/               # Command documentation indexing & vector search
│   ├── ux/                # Terminal UX (typewriter, prompts, colors)
│   └── utils/             # Quote/brace validation, helpers
├── go-llama.cpp/          # Local LLM backend (submodule / vendored)
├── dist/                  # Built binaries (make current)
└── scripts/               # Developer & build scripts
```

---

## 🚀 Quick Start

### Prerequisites

- Go **1.25+**
- macOS / Linux / Windows
- ~4 GB RAM recommended for local inference
- Disk space for model weights (if using llama.cpp)

### Clone & Build

```bash
git clone https://github.com/Nibir1/Helix.git
cd Helix
make current   # Optimized build
./dist/helix
```

For a dev build:

```bash
make start
```

---

## 🧠 AI Backends

Helix can use either:

1. **Local llama.cpp backend**  
2. **OpenAI Chat Completions** (planner + chat)

The OpenAI client lives in `internal/ai/openai.go` and uses a configurable model (e.g. `gpt-4o`).  
API key handling:

- In‑memory configuration via CLI/config.
- Optional `~/.helix/openai.key` file for persistence.

When OpenAI is enabled, Helix:

- Uses the remote model for planning and chat.
- Keeps all safety logic **inside Helix** (planner output is treated as untrusted).

---

## 🧩 RAG & System Knowledge

Helix builds a vector store over hundreds of system command docs:

- Parses & indexes **MAN pages / CLI docs**.
- Embeds content into a local vector store.
- Uses semantic search to:
  - suggest commands,
  - power `/explain`,
  - support `/ask` for OS, networking, DevOps topics.

Indexing runs in the background so the CLI stays responsive.

---

## 🔥 Helix — Complete Feature & Capability List

## 🧠 AI & RAG Intelligence

* RAG-enhanced natural language command generation over 450+ indexed system commands
* MAN page and CLI documentation indexing across 900+ vector documents
* Background RAG indexing without blocking CLI usage
* Q&A system for DevOps, OS, and programming questions (`/ask`)
* Command explanation engine (`/explain`) with usage, options, and examples
* Smart command suggestions before user requests
* Automatic fallback to standard chat when planning fails

---

## 🤖 Planner, Agent System & Tool Protocol

* Unified multi-tool agent system: response, shell, git, package
* Ultra-strict JSON planner protocol with schema enforcement
* Dual-provider inference: local llama.cpp + OpenAI planner
* Argument normalization (array flattening, trimming, synonym resolution)
* Auto-intent classification: chat, shell, git, package, multi_step
* Multi-step operation workflows (edit → stage → commit → tag)
* Truncation-resistant planner prompt engineering
* JSON extraction and fenced-block removal in parser

---

## 💻 Shell Execution System (Safety-First)

* Directory sandbox with safe-path enforcement
* Multi-layer safety pipeline (validation → risk scoring → sandbox → execution)
* Unicode-aware byte-level validator
* Automatic quote/brace/syntax repairs for minor command issues
* Detection of destructive patterns (e.g., rm -rf /, curl | sh)
* Medium-risk command confirmation prompts (sed -i, redirections)
* Blocking of package manager commands inside shell
* Safe inline edit support via perl -pi -e
* Command breakdown explanations
* Dry-run preview mode
* Cross-shell integration: bash, zsh, fish, PowerShell, CMD

---

## 🌿 Git Automation (Safe + Dangerous Modes)

* Natural language → Git automation
* Safe Git actions: commit, add, tag, checkout, create-branch
* High-risk Git actions requiring typed confirmations:
  * push --force
  * reset --hard
  * clean -fdx
  * delete-branch
* Commit messaging via safe temporary file
* Branch/tag name sanitization
* Argument cleaning to prevent injection
* Multi-step Git flows: update → stage → commit → tag
* Undo last commit but keep changes
* Clean untracked files and directories
* Stash all changes including untracked

---

## 📦 Package Management System

* Cross-platform package management abstraction
* Supported managers: apt, brew, choco, winget, pacman, yum, dnf, snap
* Safe structured actions: install, update, remove
* Package status checking & version verification
* Batch package operations
* Never uses shell commands for package management

---

## 🧩 System Architecture & Infrastructure

* Full llama.cpp integration for local inference
* Offline LLaMA model execution (TinyLlama, LLaMA-2/3 GGUF)
* Automatic model download on first run
* Metal-accelerated inference on macOS
* Parallel RAG indexing with 6 workers
* Persistent vector stores for instant reload
* Graceful fallback to mock mode if model unavailable
* OS & shell auto-detection
* Configurable security modes: off, current, strict
* Network connectivity detection with offline fallback

---

## 🎨 UX & Developer Productivity

* Semantic syntax highlighting (10+ token types)
* Rich terminal UI with emojis and color-coded output
* Animated typing effects
* Step-by-step execution logs
* Progress indicators for long operations
* Persistent command history
* Typewriter educational mode
* Detailed debugging output for planner, parser, and validator
* Clear and professional error messages

---

## 🏁 Additional Workflow & System Features

* Natural language → full multi-step workflows combining shell, git, and packages
* Automatic embedding of environment state into planner queries
* Safe parsing of noisy or partially structured model outputs
* Multi-layer validation across planning, parsing, and execution
* Extensible agent architecture to add future tools (Docker, Kubernetes, Terraform, etc.)

---

## 🎯 Why This Project Is Recruiter‑Friendly

Helix is intentionally structured as a **systems‑level AI project**, not just a demo:

- Shows **real tool‑use & safety** around shell/git/package managers.
- Demonstrates **architectural thinking**:
  - clear separation between planning, safety, and execution layers.
- Uses **modern AI practices**:
  - JSON tool calling,
  - safety checks on planner output,
  - local + remote model support.
- Written in **Go**, with attention to:
  - error handling,
  - testability,
  - modular packages.

If you want to see how the agent works internally, start from:

- `internal/ai/planner.go`
- `internal/agent/agent.go`
- `internal/commands/shell_safety.go`
- `internal/commands/git.go`
- `internal/commands/pkgmanager.go`

---

## 🤝 Contributing

Ideas, issues, and PRs are welcome:

1. Fork the repo
2. Create a feature branch
3. Run your changes locally with `make start`
4. Open a PR with a clear description and demo steps

---

## 📄 License

Helix is released under the **MIT License** (see `LICENSE`).  
LLaMA and other model weights have **their own licenses** — please review them before use.

---

## 👤 Developer Spotlight

**Nahasat Nibir** — Building intelligent, high‑performance developer tools and AI‑powered systems in Go and Python.

- GitHub: https://github.com/Nibir1
- LinkedIn: https://www.linkedin.com/in/nibir-1/
- ArtStation: https://www.artstation.com/nibir

---

<div align="center">
Helix — Making the command line safer, smarter, and more approachable with AI.  
<br />
<a href="https://github.com/Nibir1/Helix/issues">🐞 Report Bug</a> ·
<a href="https://github.com/Nibir1/Helix/issues">💡 Request Feature</a> ·
⭐ Star the project
</div>
