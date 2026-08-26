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
  `/blackbox status`. Pointing at one hardcoded route made both providers 404 on
  every request.
- **Health checks prove the service, not the socket.** A local probe performs the
  real operation (transcribe 200 ms of silence; synthesize one word and require
  RIFF bytes), because "something answered on this port" is not evidence — most
  visibly on macOS, where AirPlay Receiver owns port 5000.
- **Endpoint conflicts.** llama-server and whisper-server both default to 8080.
  `edge.FindConflicts` groups configured endpoints by host:port and `/doctor` and
  `/blackbox status` name the clash, since whichever process owns the port answers
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

**Sesame CSM-1B** joins the same pattern as the quality local voice
(`internal/speech/adapter_csm_tts.go`). Two things about it shaped the
integration. Its reference implementation is PyTorch, so Helix speaks to the Rust
`csm.rs` server instead — the CGO-free binary and the no-Python rule both hold,
and the OpenAI-shaped `/v1/audio/speech` contract meant no new transport. And its
default port is 8080, which whisper.cpp and llama.cpp also claim, so Helix
defaults it to 28195: the person running a local chain is exactly the person who
wants CSM, and they would otherwise collide on first launch. Unlike the other
sidecars it is neither auto-installed (the compute backend is a compile-time
choice, and choosing it for the user would hand a GPU owner a CPU build) nor
auto-downloaded (the weights are gated on Hugging Face behind the user's own
account).

**Conversational context** (`internal/speech/conversation.go`) is what separates
CSM from ordinary TTS in this codebase. CSM conditions on prior turns as
(speaker, text, audio), so Helix keeps a bounded ring of them and sends it with
each synthesis. Three properties are load-bearing rather than incidental:

- **Memory only.** Context needs prior AUDIO, and the standing guarantee is that
  captured clips are deleted the moment they are read. The store therefore
  imports no filesystem or networking API — a test parses its imports and fails
  if it ever gains `os` or `net` — so "never written to disk" is unchanged and
  `/purge` has nothing new to reach.
- **Bounded twice and mode-scoped.** Turn count and total bytes, oldest-first,
  cleared when live mode ends. The audio was already in memory a moment earlier
  for transcription; what changed is how long, which is why the bounds are the
  design.
- **Never assumed to have worked.** No upstream CSM server implements a context
  field, and csm.rs's request struct ignores unknown fields rather than rejecting
  them — so an unpatched server accepts context and silently drops it. Helix
  reads an `X-CSM-Context-Segments` response header (added by
  `docs/csm-context.patch`) and reports "ignored" when it is absent, rather than
  claiming conversational prosody it is not receiving.

That last property is only worth having if it reaches a person, so the detection
is exposed rather than merely recorded. `ContextCapable` is an optional interface
a TTS provider implements to report what the server did with the context it was
sent; `ConversationReport` combines that with retention state, and the `CONTEXT`
row of `/blackbox status` renders it as **conditioning** (acknowledged),
**not applied** (accepted and silently discarded — an unpatched sidecar),
**retained, unused** (turns held with no context-capable voice in the chain, a
privacy cost with no benefit) or **ready** (nothing sent yet, so nothing known).
The interface is asserted at compile time because it is satisfied structurally: a
renamed method would otherwise downgrade the row to "no context-capable voice"
without a single test failing.

### 3c. Persona (`internal/agent/persona.go`)
Who is speaking, as opposed to what may be executed. Every other prompt in the
tree constrains format — emit this JSON, use that tool — so replies came back in
whatever register the selected provider defaults to. The persona is prepended to
the planner (where most spoken replies are born as `response` steps), the chat
fallback, and the camera path.

`VoicePersona` applies only to turns that will be read aloud, because the
constraints genuinely differ: a screen tolerates a table, a speaker does not.
The persona shapes tone and never authority — it grants no capability, cannot
loosen a gate, and every safety control runs downstream of the text it produces.

### 3d. Live Mode & the Companion (`cmd/helix/blackbox.go`, `companion.go`)
`/blackbox on` opens microphone, camera, speech and initiative together. The
companion is the only part of the voice stack that is not turn-shaped: it
samples the camera on an interval and may speak without being asked.

Three constraints shape it. Frames are diffed in-process (a 16×16 luminance
fingerprint) so an unchanged scene never costs a model call — raw JPEG bytes
cannot be compared, since two frames of a motionless room differ almost
everywhere. The model is asked to return a sentinel when nothing is worth
saying. And because capture is half-duplex, the companion never speaks: it
QUEUES, and the main loop drains the queue only where the microphone is provably
closed, or Helix would transcribe and answer itself. Sentence-boundary barge-in
does not change that rule — it opens the mic only in the gap where the speaker is
idle, measures loudness, and never transcribes — so "provably closed" still means
what it meant.

Pacing adapts by backing OFF — the gap is `max(interval, smoothed last look)` —
so a slow host never queues behind itself. It deliberately does not speed up on
a fast one: a companion is bounded by how often a person wants to be spoken to.

### 3e. Host Dependencies (`internal/deps/`)
What Helix needs from the machine, how to detect it, and how to install it on
this particular host (brew/apt/dnf/pacman/zypper/apk/winget/choco). Detection is
by CAPABILITY, not package — `rec` satisfies sox — and no install command is
emitted for a package name that has not been verified for that manager.

Helix never requires Docker. The whole local voice chain (whisper.cpp, Piper)
needs a binary and Python; Kokoro is the one optional container-hosted component
and declares an `Unmet` precondition rather than walking the user toward a pull
that cannot succeed.

**Setup can fail without costing the user their shell.** Everything first-run
setup does is configuration, so a failure leaves Helix degraded rather than
broken: the provider may be unchosen and the model unpulled, but `/help`,
`/doctor`, `/provider use` and every local command still work, and `/doctor`
names precisely what is missing. `main` reports what did not finish and starts
anyway.

That is a correction, not a description. It used to `return`, so any first-run
error exited to the login shell — and the one that fired in practice was a model
DOWNLOAD, with Ollama's registry briefly answering `503`. Ejecting the user is
the single outcome they cannot recover from in place, and it is the first thing
a new user ever sees. The rule is the same one live mode states as "degrade,
never refuse the whole mode".

Registry errors are classified rather than echoed (`internal/ollama`
`DiagnosePull`), because Ollama streams the registry's own proxy text straight
through and the two common causes need opposite advice: "try again shortly" is
right when the registry is down and misleading when the tag does not exist. The
raw error is always kept as the last line — a diagnosis that hides what actually
happened cannot be debugged. The classifier matches on error TEXT, which is
Ollama's to change; it fails safe to a generic "pull it yourself" rather than to
a wrong explanation.

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

### 5c. Report Rendering (`internal/shell/panel.go`)
One visual language for everything Helix reports: a titled panel, a gutter, a
closing rule, content-measured tables, and state badges. It exists because
`/help` had *a* language and nothing else used it, so each report grew its own
flat stack of coloured lines.

The irony took a while to land: `/help` was then the last screen still drawing
itself by hand, with a hardcoded 76-column rule and a fixed 30-column gutter
that clamped its padding when a usage line ran long — so nine of fifty-six
commands started their description at a different column, and the widest row
was 124 columns against a rule that could not hold it. `/help`,
`/help <command>`, `/about` and the unknown-command screen now render through
the same primitives as everything else, which is what the paragraph above
always claimed.

One trap the shared primitives do not remove, recorded because it looks correct
in source: `Value(strings.Join(items, Muted(sep)))` puts a colour RESET inside a
coloured span, so only the first item renders styled. Colour each item, then
join.

**The palette is split by ROLE, and the split is the point.** Helix's identity
colours (electric cyan, neon magenta, aggressive red) are the brand and are
untouched. The *reading* layer is drawn from the Tron Legacy poster palette
(`#193f4a` / `#2f8ca3` / `#f4af2d` / `#030504`), lifted along its own hue until
it is legible on a dark terminal:

| Constant | Value | Role | Contrast on `#282C34` |
| :--- | :--- | :--- | :--- |
| `HexText` | `#FAFAFA` | primary prose, state words | 13.4:1 |
| `HexMuted` | `#4FB8D4` | secondary prose, labels, table headers | 6.1:1 |
| `HexAmber` | `#F4AF2D` | values — model names, ports, paths, commands | 7.3:1 |
| `HexTertiary` | `#FF9900` | warnings only | 6.5:1 |
| `HexSubtle` | `#2C6E82` | chrome only — rules, gutters, ghost text | 2.4:1 |

`HexSubtle` used to be `#444444` and carried both jobs. That is a flat grey
measuring **1.44:1**, against a WCAG floor of 4.5:1 for body text — so `/about`
rendered its philosophy in something very close to invisible, and nobody could
point at a rule saying why it was wrong. One value cannot serve both a frame
that should recede and prose that must be read; at a single setting the readable
half always loses.

The numbers are measured against `#282C34` — a common dark theme and the
*lighter* of the plausible backgrounds, so they are the worst case rather than
the flattering one. `internal/shell/contrast_test.go` holds each constant to the
standard its role requires, and keeps `#444444` named as a regression so a drift
back to that neighbourhood fails with the number that explains it.

The one deliberate exception is the ghost-text suggestion in the line editor: it
stays at chrome contrast because it must be legible enough to read and dim
enough that it can never be mistaken for what you actually typed. Readability
past a point is the bug there.

Two properties are load-bearing rather than cosmetic. Widths are measured on
VISIBLE COLUMNS, because `%-9s` counts ANSI escape bytes and pads a coloured
cell to nothing, and because a rune is not a column — a CJK character occupies
two. `visibleWidth` is the single definition (ANSI stripped, `runewidth`
applied), and every primitive measures through it. And **nothing is allowed to
leave the frame** — a line wider than the panel wraps at the TERMINAL edge and
restarts at column zero, outside the gutter, destroying the block it belongs to.
Each primitive handles that in the way its content allows: `Table` shaves its
widest column (`truncateANSI` cuts to a column budget, preserving escape
sequences), `PanelWrap` wraps prose and splits a word too long to fit, and `KV`
wraps its value with continuation lines hanging under the value column.
`Truncate` and `PanelRuleWidth` expose the first and the budget itself, for a
caller aligning a shared column across several blocks — `/help`'s command index
is the one that needs it, since per-block auto-sizing would give each category
its own axis.

All three measured in the wrong unit at some point, in three different ways —
`KV` did not wrap at all, `PanelWrap` counted bytes, `truncateANSI` counted
runes — and each produced output that looked correct in source and broke on
screen. Hence the single shared definition rather than three local ones.

`KV`'s wrapping is colour-aware in both directions, which is the only reason it
can be done at the primitive rather than pushed onto callers: escape sequences
are never counted toward a width and never severed, and the colour in effect at
a break is closed at the end of one line and reopened at the start of the next —
otherwise a wrapped value renders its first line coloured and its remainder
plain, and a line ending mid-colour bleeds into the gutter glyph beneath it. A
value that already fits is returned byte for byte unchanged, so adding the
wrapping changed no panel that was already correct.

That last point is why this belongs in the primitive at all. Before it, every
caller was implicitly responsible for keeping strings short enough for a width
it cannot see — a rule nothing enforced and several callers broke, including a
camera message 95 columns wide in a 74-column row.

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

### 5d. Local Logs (`internal/journal/`)

One append-only NDJSON writer behind every on-disk record Helix keeps: the
daemon's interaction journal and the opt-in voice interaction log. Both wanted
the same three properties, so they share one implementation rather than two that
drift.

- **Permissions and rotation are the package's job, schemas belong to callers.**
  Files are 0600 in a 0700 directory and rotate at 1 MiB keeping three
  generations. Rotation happens *before* the write that would exceed the budget,
  not after — checking afterwards leaves a file over its limit until the next
  append, which on a log that goes quiet is indefinitely. The journal had no
  rotation at all before this, despite the roadmap describing it as rotated
  since the day it was written.
- **Redaction keeps content visible and bounds it.** These logs exist so a user
  can audit exactly what was asked and heard, so redaction strips control
  characters (a transcript must not carry terminal escapes into a later `cat`)
  and length-bounds each entry — truncating on a rune boundary, because a
  severed UTF-8 sequence makes the whole JSON line unparseable and silently
  drops the entry on read-back.
- **`Open` does not create the file.** For the voice log, "default absent" is a
  privacy guarantee (threat V5), and a zero-byte file is still a file that says
  a voice session happened.
- **Nil is the disabled state.** A nil `*VoiceLog` is a working no-op, so call
  sites record unconditionally and never guard — a forgotten guard is how an
  opt-out leaks.
- **No networking, grep-enforced.** The package that writes down what the user
  said cannot send it anywhere, the same contract `internal/diagnostics` carries.

### 5e. Local Metrics (`internal/metrics/`)

The latency samples behind `/blackbox stats` and the §10 release table: wake,
voice, speech (TTS first audio), vision and ambient, each an NDJSON file under
`~/.helix/metrics/` at 0600 in a 0700 directory, local only.

- **One package owns both ends.** Writing lived at the call site in `cmd/helix`
  and reading did not exist, so a reader added elsewhere would have re-declared
  the field names and been free to disagree with the writer. Paths, field names,
  parsing and summaries are defined once.
- **Verdicts are chosen by the sample's own provider.** §10 sets separate cloud
  and local budgets, so a whisper-local turn is graded against the 6s local
  target and a Groq turn against the 3s cloud one. A blended figure would be
  measured against a threshold that applies to neither half.
- **The summaries refuse to overstate.** A p95 is reported only above 20 samples;
  below that the maximum is shown as the maximum. A metric whose median passes
  while its worst case fails reads "typical only". An absent file is "not
  measured", never a pass. The wake false-positive figure is an explicit upper
  bound, because a false trigger and a change of mind are indistinguishable from
  here.
- **Daemon availability is observed-over-expected.** The daemon heartbeats every
  60s; a dead daemon writes nothing, so downtime is the absent sample rather than
  a recorded event, and a restart is the uptime counter falling back toward zero
  (each process counts from its own start). The longest gap is reported beside the
  percentage because a percentage cannot distinguish one long outage from many
  short ones.
- **Telemetry-free, grep-enforced**, like `diagnostics` and `journal`.

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

**Voice never takes the direct-shell bypass** (`Agent.directShellAllowed`). A
confidently-classified command line runs as typed when typed; on the voice
channel it always goes to the planner. `shell.Classify` decides on the first
token, so spoken sentences beginning with an executable's name — "make a new
branch called test", "top three biggest directories" — were classified as shell
commands at full confidence and executed verbatim. The safety pipeline covered
that path the whole time (validation, risk tiers, the ADR-005 Medium cap), so
this is a routing-correctness fix rather than a closed hole: voice reaching the
whole shell has to mean the planner reaching it, not the classifier guessing from
one word. Same failure shape as the deictic camera heuristic (3d), resolved the
same way — remove the pattern match on English and let the model choose.

### 6. E2E TTY Harness (`tests/e2e/`)
A pseudo-terminal (PTY) based end-to-end suite that boots the real `helix`
binary against a mock OpenAI-compatible provider (in-process `httptest`
server) with an isolated `$HOME` and a pre-seeded knowledge meta key (to keep
the run fully offline). It proves, with zero real AI and zero network:
- High-confidence **typed** shell input bypasses the planner (voice never does — see 6a).
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