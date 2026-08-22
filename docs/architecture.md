# Helix Architecture

## High-Level Overview
Helix is an AI-native command-line shell and adversarial cybersecurity platform written in Go. It bridges the gap between human intent and machine execution by combining local/remote LLM inference, retrieval-augmented generation (RAG), and a multi-layer safety pipeline.

## Core Subsystems

### 1. Input Classification Engine (`internal/shell/classify.go`)
The entry point for all user input. It uses a weighted evidence system to classify input into:
- **Shell Command**: Bypasses the AI planner and executes directly through the safety pipeline.
- **Natural Language**: Routes to the AI Planner for intent resolution.
- **Slash Command**: Intercepts Helix-specific controls (`/help`, `/vuln`, `/audio`).

### 2. AI Planner & Agent Orchestrator (`internal/ai/planner.go`, `internal/agent/agent.go`)
- **Strict JSON Protocol**: The planner is forced to output a rigid JSON schema defining `intent` and `steps`.
- **Tool Use**: Supports `response`, `shell`, `git`, `package`, `recon`, `web`, and `vision` tools.
- **Web Tool** (`internal/agent/web.go`): read-only network retrieval —
  `action: "search"` (DuckDuckGo Lite, top 5 results) and `action: "fetch"` (one URL,
  HTML stripped to text). Classified at the same risk tier as a read-only shell
  command: it cannot write, install, or execute anything. A model-chosen `fetch`
  URL passes an SSRF guard (no loopback, link-local, private, or CGNAT
  destinations, http/https only, redirects re-checked), and an unmentioned URL
  goes through the Instruction Firewall's critic and provenance escalation
  exactly as a URL inside a shell command does. Retrieved text returns to the
  planner inside the harness's `authority="data-only"` fence, which earns one
  follow-up iteration so the model answers from the results.
- **Safety Layer**: Intercepts the plan to inject missing `git add` steps, normalize versions, and validate arguments before execution.

### 3. Shell Safety & Sandbox (`internal/commands/safety/`, `internal/commands/sandbox.go`)
A defensive pipeline; each stage can only refuse, never grant:
1. **Validation**: Unicode hazard detection, quote/brace balancing, and malicious pattern blocking.
2. **Risk Classification**: Categorizes commands into Low, Medium, and High risk.
3. **Approval Posture** (`internal/agent/permission.go`): the session's
   `/permissions` mode decides which of those tiers is asked about. It layers on
   top of the tiers and never replaces them — high risk stays blocked in every
   mode, typed confirmations stay typed, and voice-originated plans stay capped.
4. **Sandbox Confinement**: Prevents write/delete operations outside the allowed directory tree.
5. **Local Policy Hooks** (`internal/hooks/`): user-defined commands from
   `~/.helix/hooks.json`, fired last — after everything above has already
   approved the step. A blocking pre-hook can deny it. Because hooks run at the
   end, they can subtract permission and never add it. See `docs/harness.md`.
6. **Execution**: Runs the command via the detected native shell.

### 3a. Local Runtime Sidecars (`internal/providers/llamacpp/`, `internal/speech/`, `internal/edge/endpoints.go`)
Four user-managed local services (ADR-002: Helix never launches them).

- **Route discovery.** whisper.cpp serves transcription at `/inference`, not the
  OpenAI path; Piper's own server serves synthesis at `/`, not `/api/tts`. Both
  adapters try the known routes, cache the winner, and report it in
  `/voice-status`. Pointing at one hardcoded route made both providers 404 on
  every request.
- **Health checks prove the service, not the socket.** A local probe performs the
  real operation (transcribe 200 ms of silence; synthesize one word and require
  RIFF bytes), because "something answered on this port" is not evidence — most
  visibly on macOS, where AirPlay Receiver owns port 5000.
- **Endpoint conflicts.** llama-server and whisper-server both default to 8080.
  `edge.FindConflicts` groups configured endpoints by host:port and `/doctor` and
  `/voice-status` name the clash, since whichever process owns the port answers
  and makes a naive probe look healthy.
- **Placeholder model resolution** (`internal/ai/local_model.go`). `local-gguf`
  is a display label, but the model name is also the capability key — so while it
  was active the context limit fell back to 8k and vision was reported
  unsupported whatever was loaded. `llama-server` reports the real model on
  `/v1/models`; Helix asks once and substitutes it.
- **Ollama weight reuse** (`internal/ollama/blobs.go`). Ollama stores plain GGUF
  content-addressed; llama.cpp reads GGUF by magic bytes, so those blobs can be
  served directly. Helix reads the manifests to map names to blob paths and
  prints the `llama-server` command.

See `docs/local_runtimes.md`.

### 4. RAG & Threat Intelligence (`internal/rag/`)
- **Vector Store**: TF-IDF and keyword-based search over 900+ indexed MAN pages.
- **Knowledge Base**: SQLite + FTS5 database hydrated with live feeds from NVD, CISA KEV, Exploit-DB, and MITRE ATT&CK.
- **Defensive Intel**: `/vuln` and `/explain` commands query this database to provide CVSS scores, patch guidance, and detection engineering context.

### 5. Terminal UX & Audio (`internal/shell/reader.go`, `internal/audio/`)
- **SYNAPSE Prompt**: TrueColor animated prompt with glitch effects, git telemetry, and transient history.
- **Synthetic Audio**: A `beep`/`oto` based synthesizer providing 350Hz data taps, 880Hz alerts, and 110Hz error buzzes synchronized with the typewriter effect.
- **Completion**: Tab completes slash commands and paths, extending to the
  longest common prefix and listing the alternatives. The command names come
  from the registry via `shell.SetSlashCommands`, so completion cannot become a
  stale second copy of the command list.

### 5a. Command Registry (`cmd/helix/registry.go`, `registry_tables.go`)
One table per command holding its name, aliases, usage line, help text,
category, and handler. Dispatch, `/help`, `/help <command>`, Tab completion, and
the did-you-mean suggester all read it, which makes help-vs-behavior drift
unrepresentable — the class of bug where `/help` documented `/provider <name>`
while only `/provider use <name>` worked. A test asserts the README's published
command reference against the same table.

### 5b. Session & Harness State (`internal/session/`, `internal/hooks/`, `internal/ai/meter.go`)
- **Conversation ring** (`store.go`): bounded recent turns, persisted, injected
  as a zero-authority fenced block. Slash commands are excluded — they are
  control input, not conversation.
- **Snapshots** (`snapshot.go`): every wipe (`/clear`, `/compact`,
  `/memory clear`, `/resume`) archives first, so no path through the session
  commands destroys a transcript.
- **Task list** (`todo.go`): persisted open work, injected as data-only context
  so the agentic harness can resume a multi-turn task.
- **Usage meter** (`internal/ai/meter.go`): per-purpose call counts, failures,
  latency, and *estimated* tokens behind `/cost`. Exact counts are unavailable
  because no provider returns a usage block on the streaming path Helix uses;
  every surface that prints an estimate says so.
- **Project context** (`cmd/helix/dev_cmds.go`): `HELIX.md` / `AGENTS.md` /
  `CLAUDE.md`, discovered by walking up from the working directory and fenced as
  data-only — a committed file is content from whoever wrote the repository.

## Data Flow
```text
User Input → Classifier → [Shell Command] → Safety Pipeline → Sandbox → OS Shell
                          ↓
                    [Natural Language]
                          ↓
                      AI Planner
                          ↓
                    JSON Plan (Steps)
                          ↓
                    Safety Layer (Fixes/Validates)
                          ↓
                    Tool Executor (Git/Pkg/Shell/Recon/Web)
                          ↓
                    Approval Posture → Sandbox → Pre-hook → exec → Post-hook
                          ↓
                    Observations → [agentic] replan (bounded, same pipeline)
```

See `docs/harness.md` for the harness layer in full: tool vocabulary, approval
postures, the task list, hooks, and what the model is told each turn.

### 6a. Voice Command Routing (`cmd/helix/voice_commands.go`)
A spoken transcript never contains a `/`, so the slash-command surface was
unreachable by voice. A phrase table maps plain English onto command lines
("what's on my list" → `/todo`), with "slash \<name\>" as the escape hatch for
anything unphrased. Reachability is default-deny and declared in the command
registry (`VoiceOK`, `VoiceReadOnly`), so the whole voice policy is one readable,
testable table rather than a guard inside each handler — and a refusal is spoken,
never silent. See `docs/voice.md`.

### 6. E2E TTY Harness (`tests/e2e/`)
A pseudo-terminal (PTY) based end-to-end suite that boots the real `helix`
binary against a mock OpenAI-compatible provider (in-process `httptest`
server) with an isolated `$HOME` and a pre-seeded knowledge meta key (to keep
the run fully offline). It proves, with zero real AI and zero network:
- High-confidence shell input bypasses the planner.
- Natural language routes through the strict-JSON planner and executes.
- Medium-risk commands require confirmation; declining skips execution.
- High-risk commands are hard-blocked.
- Non-interactive mode blocks high-risk commands with a non-zero exit.
- `/help` renders and `/purge` respects a declined confirmation.

Run with `make e2e` (Linux/macOS only).

### 7. Instruction Firewall (`internal/agent/firewall.go`, `internal/rag/sanitize_retrieved.go`)
Prompt-injection hardening for RAG-augmented planning: provenance tiers,
retrieved-text sanitization, data-only context fencing, canary honeypots, a
fail-closed critic pass, and provenance-based risk escalation.

### 8. Kernel-Grade Confinement (`internal/confinement/`)
`/sandbox strict` is enforced by the OS kernel, not by string matching:
- **macOS:** Seatbelt (`sandbox-exec`) profile — `(allow default)`, deny all
  file writes, re-allow the jail root (best-effort; Apple-deprecated).
- **Linux:** bubblewrap namespaces preferred (`--ro-bind / /`, writable jail,
  fresh /proc/dev, PID/IPC isolation); fallback to the **Landlock LSM** via
  raw syscalls (CGO-free) using a `helix --confined-child` re-exec that
  confines itself before running the shell.
- **Unsupported platforms:** graceful advisory fallback with a visible warning.
`/doctor` and `/sandbox` report the active backend.

### 9. Telemetry-Free Crash Diagnostics (`internal/diagnostics/`)
Panics and fatal signals (SIGSEGV/SIGABRT/SIGBUS/…) write a local, 0600,
secret-redacted JSON report to `~/.helix/crash-*.json` (last 5 retained).
The package imports no networking primitives (CI grep-test enforced).
Opt out with `HELIX_CRASH_REPORTS=off`; `/doctor` lists reports; `/purge`
deletes them; `HELIX_SELFTEST_PANIC=1` verifies the pipeline (exit 42).