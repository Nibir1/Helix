# BlackBox Development — Master Roadmap & Context Document

> **Project:** Helix "BlackBox" — transformation of Helix from a text-driven CLI assistant into a
> multimodal, always-on, voice-first AI companion.
> **Branch:** `blackBox` (all work happens here; `main` is never touched without explicit approval)
> **Baseline commit at time of writing:** `fd34503` ("fixing CI jobs") — `blackBox` was at parity with `main`, clean tree, **no BlackBox work started yet**.
> **Owner:** Nahasat Nibir (AI Engineer & Project Lead)
> **Document status:** LIVING DOCUMENT — update §13 (Progress Tracker) after every work session.

---

## §0. Session Bootstrap Protocol — READ THIS FIRST in a fresh chat

If you (human or AI agent) are starting a new session to continue BlackBox development, follow this
exact sequence:

1. **Read this document top-to-bottom.** It contains the complete plan, all architecture decisions,
   and the current state.
2. **Verify repository state.** Deliberately no absolute path: this step used to hardcode one
   developer's home directory, which was wrong for everyone else and eventually wrong for its own
   author. Identify the checkout by what it *is*, not where it sits.
   ```bash
   cd <your Helix clone>                   # wherever it lives on this machine
   git rev-parse --show-toplevel           # you should be at the repo ROOT, not a subdirectory
   grep '^module' go.mod                   # MUST be: module helix
   git branch --show-current               # MUST be blackBox
   git log --oneline -10                   # see what changed since this doc was written
   git status                              # note any uncommitted work
   ```
3. **Check §13 Progress Tracker** → find the first phase that is not `DONE`. That is your phase.
   Note that "core DONE" is not "done": phases 7, 9, 10, 11 and 12 all carry unfinished tasks,
   and §13 now lists every open checkbox in one place so the remaining work is visible without
   reading all of §6. **As of 2026-08-23 every one of those is hardware-, key- or owner-gated** —
   there is no unwritten *planned* code left, so if you are here to work through the tracker, the
   honest answer is that the next task is a device, a credential, or a decision. (Full-duplex
   barge-in is code, but its full-duplex form is parked on an ADR-level conflict; the
   sentence-boundary form shipped 2026-08-26.)
   Verify that before trusting it: on 2026-08-23 two "done" items turned out to be partly
   unwritten, one of them a checkbox naming two deliverables of which only one existed.

   **An empty tracker is not the same as correct code, and the entries after 2026-08-23 are the
   evidence.** Everything since — CSM-1B and its context conditioning, the interface pass, and the
   three panel-primitive width bugs — was work no checkbox asked for: a capability the owner
   requested, then defects found by auditing and by *rendering* what the code produced. None of it
   would have been visible from §13. If you are here to write Go and the tracker looks finished,
   read §9's rules 8-12 and go looking at output rather than at checkboxes.
4. **Read that phase's section in §6** and every file it lists under "Files touched".
5. **Verify the baseline is green before changing anything:**
   ```bash
   make test           # unit tests
   make e2e            # PTY end-to-end suite (Linux/macOS only)
   make release-check  # everything a release checks, tagging nothing
   ```
6. **Follow the ADRs in §3.** If a decision genuinely needs revisiting, do not silently deviate —
   update the ADR with new rationale first.
7. **Respect the guardrails in §12** (non-negotiables).
8. **When you finish a work session:** update §13 (mark tasks, append a dev-log entry with date,
   what changed, what's next), then commit with a conventional message
   (`feat(speech): ...`, `refactor(agent): ...`, etc.). Do NOT push to main. Do NOT merge.
9. **Releasing is the owner's, and it is `make release` from `main` after the merge**
   (`scripts/release.sh`; see the README's Releasing section). It tags `v` + the
   `HelixVersion` constant and refuses if the two disagree, refuses a dirty tree
   or a non-`main` branch, and refuses to re-tag a published version without
   `--force` — because `/reboot` verifies downloads against the checksums file
   published with a release, so replacing artifacts under an existing tag makes
   every user's update fail in a way that looks like tampering. ADR-009 still
   governs the merge itself: owner approval only.

> **Note on line numbers:** file:line references in this document were accurate for commit
> `fd34503` (2026-08-16) and are now **43 commits stale** — treat them as historical, not as
> coordinates. Symbol names are the stable anchor: grep for the function or constant, not the
> line. This note is not re-pinned on every commit deliberately; a number that is refreshed
> occasionally is more misleading than one that is openly out of date.

> **Note on command names (2026-08-22):** the voice/perception surface was
> **unified into one command**. `/voice`, `/manual`, `/voice-setup`,
> `/voice-status`, `/wake`, `/say`, `/tts` and `/eyes` are gone; every one of
> them is now a `/blackbox` subcommand, and typing an old name prints where it
> went. Phase sections below were written before that change and still name the
> original commands **where they record what was built at the time** — that is
> history and is left intact on purpose. Anything describing the CURRENT surface
> (§2.2, §2.3, §7, §13, the ADR amendments) has been corrected. When the two
> disagree, the current-surface sections win.
>
> | Was | Now |
> |-----|-----|
> | `/voice`, `/voice on` | `/blackbox on` |
> | `/manual`, `/voice off` | `/blackbox off` (or say "manual mode") |
> | `/voice-setup` | `/blackbox setup` |
> | `/voice-status` | `/blackbox status` |
> | `/eyes on\|off\|status` | `/blackbox eyes on\|off`, `/blackbox status` |
> | `/eyes look` | `/blackbox look` |
> | `/wake on\|off\|status` | `/blackbox wake on\|off`, `/blackbox status` |
> | `/say <text>` | `/blackbox say <text>` |
> | `/tts on\|off` | `/blackbox tts on\|off` |

---

## §1. Mission & Vision (condensed from the original BlackBox plan)

Transform Helix from a **reactive, text-input CLI tool** into a **fully autonomous, always-on,
multimodal AI companion**: the user *speaks*, Helix *listens, understands, sees, responds vocally,
and acts* — like a shipboard AI (JARVIS/Cortana archetype).

Six capability layers from the original plan:

| # | Layer | One-line summary |
|---|-------|------------------|
| 1 | Multi-provider Speech Recognition (STT) & Synthesis (TTS) | Provider-agnostic hearing and speaking with transparent pricing |
| 2 | Voice-Driven Task Execution | Speak naturally → intent → safe execution → spoken feedback |
| 3 | Persistent Always-On AI Presence ("Living AI") | Daemon, session memory, ambient presence, graceful degradation |
| 4 | Camera-Based Visual Perception | Opt-in camera frames + multimodal LLM context |
| 5 | Ambient Noise Detection | Sound-event awareness and contextual responses |
| 6 | Mode Switching | Voice autonomy ↔ manual typing, plus hybrid mode |

**Critical review finding (2026-08-16):** the original plan is sound in vision and phasing logic,
but was written without deep codebase knowledge. It has been **amended** in this roadmap. The key
amendments (full rationale in §3 ADRs):

1. **Go, not Python.** Helix's crown jewels (safety pipeline, kernel confinement, instruction
   firewall, CGO-free signed single binary, PTY e2e harness) must not be rewritten.
2. **Local speech models follow the Ollama sidecar pattern** (external local service over HTTP),
   preserving the CGO-free default build.
3. **Phase 3 (daemon) is the project-defining refactor** and is scoped accordingly (Agent must be
   decoupled from the TTY first).
4. **A Voice Risk Policy and voice-channel threat model are written BEFORE any voice execution
   ships** — voice is an untrusted input channel that bypasses the existing Instruction Firewall
   if not treated with its own controls.
5. **Mode switching is pulled forward into Phase 2** (safety valve + testability).
6. **Ambient noise detection is demoted** to optional Phase 6 (lowest value, permanent mic cost).

---

## §2. Current Codebase Analysis (Helix today — the foundation)

### 2.1 What Helix is

Go 1.25 single-binary application (~10K lines across core packages), CGO-free by default,
cross-platform (macOS/Linux/Windows). An AI-powered native shell + adversarial cybersecurity
platform: natural language → strict-JSON AI plan → multi-layer safety pipeline → sandboxed execution.
Local-first AI direction (recently standardized on Ollama — commit `ca9560b`).

**Dependencies (go.mod):** `creack/pty`, `fatih/color`, `gopxl/beep/v2` (+ `ebitengine/oto/v3`),
`mattn/go-isatty`, `mattn/go-runewidth`, `golang.org/x/sys`, `golang.org/x/term`,
`modernc.org/sqlite` (CGO-free SQLite). That's it — the project is deliberately near-zero-dependency.

### 2.2 Architecture map (files that matter for BlackBox)

```
cmd/helix/main.go            REPL entrypoint; THE injection point for voice input
cmd/helix/handlers.go        Slash-command handlers (/about, /setup, /debug, …)
cmd/helix/registry.go        Command REGISTRY — one table drives dispatch, /help, completion, voice
cmd/helix/blackbox.go        /blackbox: live mode + every folded voice/vision subcommand
cmd/helix/companion.go       The initiative loop — samples the camera, speaks unprompted
cmd/helix/first_run.go       First-boot stages: provider → system packages → speech chain
cmd/helix/reboot.go          /reboot: capture what Helix was doing, ask the loop to stop, resume
cmd/helix/reboot_exec.go     The supervisor (ADR-018) — NOT syscall.Exec; see the file comment
cmd/helix/voice_ui.go        Wizard glue: panel-voiced confirmations and step rendering
cmd/helix/helpers.go         Provider/model setup wizards (copy this pattern for speech setup)
cmd/helix/noninteractive.go  `helix -c "..."`, pipes, scripts (shell-only, no AI)
internal/shell/reader.go     Raw-mode TTY line editor (stdin-only; no alternate input source)
internal/shell/classify.go   Input classifier: shell vs natural-language vs slash command
internal/ai/planner.go       Strict-JSON planner protocol; Plan/PlanStep types; 5 tools
internal/ai/model.go         Model facade; STATELESS one-user-message planner calls
internal/ai/providers.go     Package-level provider globals, InitProviders, UseProvider/UseModel
internal/agent/agent.go      Agent orchestrator: HandleInput → classify → plan → execute
internal/agent/firewall.go   Instruction Firewall (canary, critic pass, provenance escalation)
internal/agent/fastpath.go   Deterministic regex fast-path plans
internal/commands/           Safety pipeline, GitManager, package manager, DirectorySandbox
                             (ChangeDirectory announces the move; SetDirectory is the silent
                             form, for anything moving on the user's behalf)
internal/commands/prompt.go  Prompter INTERFACE (swappable confirmation seam) ← key for voice
internal/commands/safety/    ValidateAndCleanCommand, AnalyzeShellRisk (Low/Med/High tiers)
internal/confinement/        Kernel-grade sandbox: Seatbelt (macOS), Landlock/bwrap (Linux)
internal/providers/          ★ AIProvider interface + Registry + KeyStore + 12 adapters
internal/providers/types.go  AIProvider, ChatMessage (TEXT-ONLY today), Capabilities{Vision...}
internal/ollama/             Ollama HTTP client + auto-installer + model pull (sidecar pattern!)
                             DiagnosePull classifies registry failures; a failed pull is
                             reported and survivable, never fatal to the session
internal/audio/              Output-only synth tones (beep/oto); no microphone path exists
internal/config/config.go    Config + UserPrefs persisted to ~/.helix/config.json
internal/rag/                SQLite+FTS5+vector knowledge base, threat intel updaters
internal/recon/              Authorized recon engine (nmap/masscan orchestrator)
internal/stealth/            Memory-only private execution
internal/diagnostics/        Telemetry-free local crash reports (0600, redacted)
internal/journal/            One rotating NDJSON writer: daemon journal + opt-in voice log
internal/session/continuity.go   What /reboot carries across a restart. Small on purpose: NOT a
                             second copy of the conversation, consumed on read, expires in 12 h
internal/update/             Self-update: GitHub releases + local builds. Mandatory checksum,
                             pinned host, build-info proof, atomic install, auto-rollback (ADR-019)
internal/speech/conversation.go  Bounded in-memory turn history conditioning CSM-1B (ADR-017)
internal/speech/piper_native.go  Piper without an interpreter: a persistent process holding the
                             voice model resident (model load dominates; pay it once, not per
                             sentence). libstdc++ probed before any download
internal/ux/                 Terminal UX: typewriter, PrintAIMessage ← TTS tap-in point
internal/shell/panel.go      Report rendering: panels, badges, wrapping KV rows, self-fitting tables
                             (one visual language; all widths in VISIBLE columns via visibleWidth)
internal/shell/wizard.go     The same language for screens that ASK: Step/StepDetail/StepCommand,
                             PromptDanger (red — a yes that destroys something), Plain (strip ANSI
                             before text reaches a TTS engine)
internal/shell/prompt.go     The palette, split by ROLE: identity (brand) · text (must be read)
                             · chrome (must recede). Contrast enforced by contrast_test.go
internal/agent/persona.go    Who Helix IS — tone for planner/chat/vision replies, never authority
internal/agent/vision.go     Camera turns; two explicit doors only (/blackbox look, vision tool)
internal/deps/               Host packages Helix needs + per-OS install commands (never Docker)
internal/utils/              Interrupt manager (RegisterOperation idiom), syntax highlight
tests/e2e/                   PTY harness: real binary + in-process mock provider (httptest)
```

### 2.3 Extension-point inventory (the seams BlackBox bolts onto)

| Seam | Location | Why it matters |
|------|----------|----------------|
| REPL input injection | `cmd/helix/main.go:170-198` — `shell.ReadLine()` at :171, `agentCore.HandleInput(input)` at :195 | **The single most important fact:** anything that produces a `string` can drive the entire intelligence pipeline. STT output goes here unchanged. |
| Output tap for TTS | `internal/ux/ux.go` `PrintAIMessage` (~:184); `Agent.handleResponseStep` (`agent.go:561`) | All AI text lands here — speak it via TTS. |
| Provider contract | `internal/providers/types.go:51` `AIProvider` interface | Template for `STTProvider`/`TTSProvider` interfaces. |
| Provider registry | `internal/providers/registry.go` | Mutex-protected map + keystore hydration — copy for speech. |
| Key storage | `internal/providers/keystore.go` — `~/.helix/secrets.json` (0600) + env-var overrides | Reuse directly; namespace keys as `stt.<name>` / `tts.<name>`. |
| Setup wizard pattern | `cmd/helix/helpers.go:277` `useProviderInteractive` → `setupProvider` → `selectModelForProvider` | Copied for `/blackbox setup` (STT/TTS selection + pricing display). First run chains provider → packages → speech (`first_run.go`). |
| Slash dispatch | `cmd/helix/registry.go` — one table; `registry_tables.go` holds the entries | The whole voice/vision surface is ONE entry (`/blackbox`) with subcommands. `VoiceOK`/`VoiceReadOnly` on the same entry decide what speech can reach. |
| Report rendering | `internal/shell/panel.go` — `PanelTitle`/`KV`/`Table`/`Badge` | Every report uses one visual language. Widths measure VISIBLE text (ANSI is not content); `Table` fits itself to the panel and `KV` wraps its value with a hanging indent, so no primitive can spill outside the frame. Colour is assigned by ROLE — text tones are held to WCAG AA by test, chrome is held BELOW them so a frame cannot compete with what it frames. |
| Persona | `internal/agent/persona.go` `PersonaPrompt` | Prepended to planner, chat fallback and vision calls. Shapes tone only; grants nothing. |
| Host dependencies | `internal/deps/` `Catalog`/`Missing`/`InstallCommand` | Detects by capability, installs per OS, and never requires Docker. |
| Local log writer | `internal/journal/` `Appender`/`Redact`/`VoiceLog` | 0600-in-0700, size-rotated, redacted NDJSON. Shared by the daemon journal and the opt-in voice log; imports no networking (grep-enforced). |
| Classifier | `internal/shell/classify.go:124` `Classify()` (HighConfidence 0.65 at :57) | **This row was wrong until 2026-08-23.** It claimed voice transcripts "naturally route to the AI planner" — they did not. `Classify` decides on the FIRST TOKEN, so any spoken sentence beginning with an executable's name was read as a shell command at full confidence and executed verbatim: "make a new branch called test" ran as `make a new branch called test`. Voice now never takes the direct-shell bypass (`Agent.directShellAllowed`), which is what makes the claim true — at the routing layer, deliberately, rather than as an accident of the classifier. The "optionally bias per mode" note is no longer optional. |
| Prompter seam | `internal/commands/prompt.go:13` `Prompter` interface; `commands.SetPrompter` | Swap in a `VoicePrompter` that speaks questions and listens for yes/no. **Fail-closed on timeout.** |
| Audio output stack | `internal/audio/` — beep/oto, 44.1kHz PCM, `Init()` at audio.go:72; Linux needs `audio_cgo` tag else noop backend | TTS playback rides this. Add a `PlaySpeech`-style API. |
| Ollama sidecar pattern | `internal/ollama/client.go` + `installer.go` (auto-install, health, model pull over HTTP) | **The proven pattern for local Whisper/Piper/openWakeWord:** external local service, HTTP, CGO-free core. |
| Injectable model call | `internal/agent/firewall.go:104` `criticRun` var | Precedent for making model calls mockable in unit tests. |
| Interrupt idiom | `internal/utils/interrupt.go` `RegisterOperation(cancel)` | Every long op (STT streaming, TTS, wake loop) registers here for Ctrl+C safety. |
| E2E mock provider | `tests/e2e/harness_test.go` — httptest server (:82-108) + isolated `$HOME` + PTY | Extend with mock STT/TTS/vision endpoints + hit counters. |
| Config prefs | `internal/config/config.go` `UserPrefs` (:30-39) | Add voice/vision prefs + new `speech` config section. |
| Vision capability flag | `internal/providers/types.go:43` `Capabilities.Vision` | Consumed by Phase 5: `ChatMessage.Parts` multimodal format + `Capabilities.Vision` gate in `ai.RunVisionModel`. |

### 2.4 What exists vs. what is net-new

**Exists (reusable):** multi-provider adapter/registry/keystore pattern; setup wizard UX; output
audio stack; safety pipeline + risk tiers + typed confirmations; sandbox & kernel confinement;
instruction firewall; Ollama sidecar management; PTY e2e harness with mock servers; interrupt
manager; diagnostics; config/persistence conventions; `HELIX_AUTOCONFIRM` precedent.

**Net-new (must be built):** microphone capture; STT/TTS provider layer; wake-word engine; VAD /
silence detection; VoicePrompter confirmation loop; voice risk policy; conversation session state
(planner is stateless today — one user message at `internal/ai/model.go:151`); undo journal;
headless Agent (decoupled from TTY); daemon + IPC + service installers; multimodal message format;
camera capture; ambient audio analysis; hybrid input mode.

**Confirmed absent** (repo-wide search): microphone/speech/TTS/camera/wake-word/daemon/IPC code.

---

## §3. Architecture Decision Records (ratified; revisit only with written rationale)

### ADR-001 — Language: Go. No Python rewrite.
**Decision:** All BlackBox core development stays in Go.
**Rationale:** The original plan's "Python 3.11+ (or existing Helix language)" would force rewriting
the safety pipeline, confinement, firewall, and supply chain (goreleaser + cosign + SBOM +
CGO-free builds), and orphan the PTY e2e harness. Python's audio/ML ecosystem advantage is
neutralized by ADR-002 (sidecars). Optional helper sidecars may be written in any language — they
are external processes, not part of the Helix binary.
**Consequences:** Go audio ecosystem is thinner; mitigated by ADR-002/ADR-003.

### ADR-002 — Local models via sidecar services (the Ollama pattern).
**Decision:** Local Whisper (whisper.cpp server or faster-whisper), Piper TTS, and the wake-word
engine run as **external local HTTP services**, managed the way `internal/ollama` manages Ollama
(health check, optional auto-install, clear errors). No linking of ML runtimes into helix.
**Rationale:** Preserves the CGO-free default build (a Linux build constraint and supply-chain
guarantee), reuses a proven in-repo pattern, keeps memory/CPU isolation between the daemon and
models.
**Consequences:** One more local process to supervise; the daemon (Phase 4) owns lifecycle checks.

**Amendment (2026-08-22) — sidecars may not require a container runtime.** The
sidecar pattern says "external local HTTP service"; it does not say "container".
whisper.cpp and Piper are plain binaries (plus Python for Piper's server) and
are the documented local chain. Kokoro is the one component distributed only as
an image, and it is therefore OPTIONAL: Helix will not install Docker, will not
attempt a pull when no daemon answers, and marks it unavailable in the provider
table so the constraint is visible before the choice rather than after a failed
`docker pull`. Sidecar specs express this with an `Unmet` precondition — a
dependency Helix declines to resolve, distinct from a missing binary it offers
to install.

### ADR-003 — Microphone capture via external recorder binaries (CGO-free default).
**Decision:** Default audio capture shells out to universally available recorders
(`sox`/`rec`, `ffmpeg`), writing WAV/PCM to a temp file or pipe. A CGO-tagged native backend
(`malgo`/miniaudio, build tag `audio_cgo`, precedent: `internal/audio/backend_beep.go`) may be
added later as an optimization — it must remain optional.
**Rationale:** `make current` / release binaries must build CGO-free on all platforms. Helix
already orchestrates external tools (nmap, masscan, ffuf) — this is idiomatic for the project.
**Consequences:** Dependency on `sox`/`ffmpeg` presence; `/doctor` gains a check for it; setup
wizard offers to install (brew/apt/choco) like Ollama.

### ADR-004 — Daemon IPC: stdlib Unix domain socket / named pipe with NDJSON.
**Decision:** The daemon speaks newline-delimited JSON over a Unix domain socket
(`~/.helix/daemon.sock`, 0600) on macOS/Linux and a named pipe on Windows. Client:
`helix remote <command>`. **No Redis, no ZeroMQ, no gRPC dependency.**
**Rationale:** Zero new dependencies; filesystem permissions are the auth; fits single-binary
philosophy. The original plan's Redis suggestion was rejected as out of character.
**Consequences:** Simple protocol, easy to mock in tests.

### ADR-005 — Voice is an untrusted input channel (Voice Risk Policy).
**Decision:** Transcribed speech is treated as **user-intent input with reduced authority**:
1. Voice-originated plans are capped at **Medium risk**; High-risk is blocked regardless of wording.
2. **Dangerous actions that today require typed confirmation (force push, hard reset, clean
   worktree, delete main) can NEVER be confirmed by voice** — typed confirmation stays mandatory.
3. VoicePrompter confirmations **fail-closed on timeout/no-answer** (silence = decline).
4. Provenance escalation (from the Instruction Firewall) applies to transcripts: tokens in the
   transcript that match ambient/visual context but not a deliberate user pattern get escalated.
5. Wake-word-triggered sessions have a hard 60s inactivity lockout back to wake-only listening.
**Rationale:** A TV, podcast, song lyric, or person in the room becomes *text with user authority*
once transcribed — this bypasses the existing Instruction Firewall, which only guards retrieved
RAG data. Helix's reputation is hardened execution; voice must not become the weak door.
**Consequences:** Some commands are slower/more annoying by voice — that is intentional.

**Amendment (2026-08-22) — the denied set narrowed from 20 commands to 9.** Live
mode is meant to reach the whole shell by speaking, so the question each command
answers changed from "is this important enough to allow" to "would a misheard
phrase, or a voice on the radio, do damage that cannot be undone". Rules 1–5
above are UNCHANGED and still govern; only the allowlist widened. Nine commands
still answer yes, each argued on its own merits rather than as a batch:

| Command | Why voice can never reach it |
|---------|------------------------------|
| `/purge`, `/rag-reset` | destroy data outright |
| `/scan` | fires traffic at a third party |
| `/commit` | writes history (git is never voice-reachable, rule 2) |
| `/config`, `/stealth` | move the approval or privacy posture |
| `/hooks` | installs policy that later runs unattended |
| `/setup` | would have you dictate API keys aloud |
| `/init` | writes HELIX.md, which is planner context from then on |

The list is no longer restated in prose anywhere: `/blackbox status` reads it
from the registry, because a hand-kept copy of a security policy had already
gone stale once.

**Amendment (2026-08-23) — one rule now lands on a SUBCOMMAND, and states the
general principle.** P2.8's transcript log is a privacy-posture switch, which is
precisely why `/config` and `/stealth` are on the denied list — but the denied
list is per-command and `/blackbox` must stay voice-reachable, because the
"manual mode" safety valve lives on it. So `voiceCommandAllowed` refuses
`/blackbox log on` specifically, while `/blackbox log off` is always permitted.
The principle worth writing down, because it now governs three separate controls
(the camera, live mode, and the log) rather than one:

> **Voice may reduce what is collected, never increase it.** "Turn off your eyes"
> and `/blackbox log off` work by voice. Opening the camera happens only as part
> of an explicit, announced live-mode entry, and starting the transcript log must
> be typed. A privacy control should fail toward collecting less — which is also
> why the eyes-off phrase is matched loosely and may over-trigger.

**Amendment (2026-08-27) — a DANGER ZONE command is voice-reachable, and the
criterion is destruction rather than category.** `/reboot` restarts the shell; it
is filed under DANGER ZONE because that is where a command that ends your process
belongs, and it is `VoiceOK` because **it destroys nothing**. The continuity
record is written before the process ends, so the worst a misheard "reboot" costs
is a few seconds, after which the same mode, directory, provider and conversation
are back. Every other command in the table above loses something that does not
come back. Stated as the general rule, since the table would otherwise read as
"category implies denial":

> **A DANGER ZONE command may be voice-reachable if and only if it destroys
> nothing and its effect is recoverable.** The criterion is data loss, not how
> alarming the command sounds.

**The reduce-never-increase principle survived this without an exception, and
that took a code change rather than a doc change.** The continuity record carries
a short excerpt of the last message so the resumed shell can say what it was
doing — which, on a spoken restart, would have been the microphone putting your
words on disk with no opt-in. So it does not: `captureContinuity` omits
conversation content entirely when the request arrived by voice. The resume is
very slightly less specific and the rule stands verbatim. Writing the exception
into four documents would have been faster and would have been the wrong trade.

Rules 1–5 above remain unchanged.

### ADR-006 — Pricing data is data, not code.
**Decision:** The provider pricing catalog lives in an embedded JSON file
(`internal/speech/pricing.json`), user-overridable at `~/.helix/pricing.json`. The plan's tables
were partly speculative/stale; hardcoding them in Go source guarantees rot.
**Consequences:** The `/blackbox setup` wizard reads merged (embedded + user) pricing at runtime.

### ADR-007 — TTS rides the existing beep/oto output stack.
**Decision:** TTS audio decodes (MP3/WAV/PCM) and plays through `internal/audio`'s speaker
ownership (add e.g. `audio.PlaySpeech(...)`); a pre-speech chime reuses `PlayAlert`; Linux
non-cgo keeps the noop fallback with a clear warning.
**Rationale:** One component owns the speaker; avoids two audio backends fighting over the device.

### ADR-008 — Mode switching ships with the first voice-input phase.
**Decision:** `/voice` ↔ `/manual` toggling and the input-source abstraction land in Phase 2,
not Phase 6 of the original plan.
**Rationale:** Safety valve (instant fallback to typing when voice misbehaves) and testability
(synthetic transcript injection needs a mode boundary).

**Amendment (2026-08-23) — "bias per mode" is now mandatory, not optional.** The original
decision noted that the classifier could "optionally bias per mode". It had to: because
`Classify` decides on the first token, spoken sentences beginning with a command name were
executed verbatim instead of being planned. The voice channel no longer takes the direct-shell
bypass at all (`Agent.directShellAllowed`); typed input is unchanged. The mode boundary this ADR
created is what made a one-line fix possible, which is the testability argument it was ratified
on, arriving late but arriving.

**Amendment (2026-08-22) — one command, not two.** `/voice` ↔ `/manual` became
`/blackbox on` ↔ `/blackbox off`, and the spoken safety valve ("manual mode")
is now matched at the END of a sentence rather than as the whole transcript —
QA said "Excellent. Now switch to manual mode." and the exact-match check sent
it to the planner, which asked what to switch to manual mode FOR. Suffix, not
substring, because "how do I switch to manual mode?" is a question about the
feature. The valve itself is unchanged in kind: still instant, still reachable
without the keyboard, and now it also closes the camera and the companion loop,
because live mode opened them.

**Amendment (2026-08-27) — a SECOND suffix phrase, and mode survives a restart.**
"reboot" joins "manual mode" as a phrase matched at the end of an utterance and
handled before spoken-command dispatch, for the same reason: it ENDS a turn
rather than being served by one, and a spoken "reboot" that reached the planner
would be answered with a sentence about rebooting instead of a reboot.

It adds one rule the kill phrases do not have, and that rule was **wrong the
first time**. It began as a blacklist of question openers — "what happens when
you reboot" ends on the phrase and would otherwise fire — and a live session
found the case a blacklist cannot cover:

> "So you don't have any memory that I told you to reboot."

Not a question, so no opener matched. Ends on the phrase, so the suffix matched.
Helix restarted itself in the middle of the user explaining that it had
forgotten restarting. **Reported speech is the shape that breaks it**, and there
is no finite list of ways English can end on a word without meaning it.

So the rule is now an **allowlist of imperative lead-ins**: the phrase fires when
it is the whole utterance, or when what precedes it is something people actually
say before a command ("okay", "please", "go ahead and", "helix"). An allowlist
only has to anticipate how people ASK, which is short, and its failure mode is a
reboot that does not happen — where the user simply says the word again. The kill
phrases keep the older, looser rule: a privacy control that over-triggers fails
in the safe direction, while this one ends the process.

The mode itself now round-trips a restart, which is properly this ADR's concern:
a reboot from live mode comes back in live mode, from the keyboard at the
keyboard. Most of that already worked — `cfg.UserPrefs.VoiceMode` and
`initVoiceMode` restore the mode at boot — but the continuity record carries the
mode explicitly anyway, because the preference records what you last CHOSE and
the record has to describe what was TRUE at the instant of the restart. The two
differ exactly when a session entered or left voice mode without persisting.

### ADR-009 — `main` remains shippable; blackBox is a long-lived integration branch.
**Decision:** All BlackBox work commits to `blackBox`. Merges to `main` only by explicit owner
approval, after the full test suite passes. Keep commits conventional and phases delimited by tags
(`blackbox-phase1`, ...) for navigation.

### ADR-010 — Tray indicator: separate opt-in helper, never CGO in the core.
**Decision:** The system-tray indicator ships (if at all) as a **separate, optional helper
process** that consumes the daemon's NDJSON IPC (`helix remote status` / journal tail) — it is
NOT linked into the helix binary. The HUD overlay stays out of scope.
**Rationale:** Pure-Go systray libraries are immature, and the mature option (`fyne.io/systray`)
requires CGO, which breaks the CGO-free default build (guardrail #8). A small helper follows the
ADR-002 sidecar precedent (external process, any language, optional). Until one exists, ambient
presence v1 = the daemon TTS greeting + `/doctor` daemon section + wake chime (already shipped).
**Consequences:** No tray in v1; the daemon IPC is the stable contract a future helper consumes.

### ADR-011 — Cheapest-good speech defaults; providers are swappable data.
**Decision (2026-08-17):** The recommended cloud defaults are **Groq `whisper-large-v3-turbo`**
for STT (~$0.04/hr, large-model accuracy, ~200× real time) and **OpenAI `gpt-4o-mini-tts`** for
TTS (~$12/1M chars, natural, streaming). Deepgram Nova-3/Aura-2 remain first-class for
lowest-latency streaming; ElevenLabs stays for maximum voice quality. Selection is data
(`pricing.json` + wizard), never hardcoded routing.
**Rationale:** A voice-first assistant that is always listening must be cheap per minute or it is
unusable. Groq turbo is 4–25× cheaper than the previous default with equal accuracy; batch-POST of
VAD-endpointed utterances is the natural pattern for a wake-word assistant and adds only
~300–500 ms. (Research log: 2026-08-17 session.)
**Consequences:** Groq has no WebSocket streaming — live word-by-word partials require Deepgram
(kept as the streaming option). New keystore env mapping `GROQ_API_KEY`.

### ADR-012 — Local Pi-5 stack: Kokoro/Piper TTS + whisper-family STT + small Ollama LLM, all sidecars.
**Decision (2026-08-17):** The recommended Raspberry Pi 5 (8 GB) fully-local stack is
**openWakeWord → whisper.cpp/faster-whisper (or sherpa-onnx streaming Zipformer for live partials)
→ a 3B-class Ollama model (qwen2.5:3b / llama3.2:3b, Q4) → Kokoro-82M (quality) or Piper (lowest
latency) TTS**, every ML component an external HTTP sidecar (ADR-002). Kokoro rides the
OpenAI-compatible `kokoro-local` adapter; whisper.cpp rides `whisper-local`.
**Rationale:** Preserves the CGO-free core; matches the Home-Assistant Wyoming ecosystem so users
can reuse existing add-ons; keeps the private/offline path first-class. Expected fully-local
voice-to-first-audio ~1–1.5 s with sentence-streamed TTS.
**Consequences:** New `kokoro-local` TTS adapter. Pi deployment doc + `helix doctor` sidecar checks
scoped in Phase 10.

### ADR-013 — Genuine agentic harness: bounded plan→act→observe→replan, opt-in, pipeline-preserving.
**Decision (2026-08-17):** Helix gains an iterative harness (`internal/agent/harness.go`): after a
plan executes, the per-step **observation trace** (success/failure + sanitized error) is fed back
to the planner as a **data-only fenced block**, and the planner may self-correct or chain a
follow-up, bounded by a step budget (default 4). Off by default; `/agentic on` enables it and the
preference persists.
**Rationale:** The old planner was single-shot: a failed step aborted the turn with no recovery,
and the model never saw what its commands did. Closing the observe→replan loop is the core of a
"living AI" that gets work done. Opt-in + bounded keeps cost and behavior predictable.
**Guardrail:** Every follow-up plan re-enters the SAME pipeline (`planFirewallExecute`) — classify
is skipped (already an agent turn) but planner, Instruction Firewall, risk tiers, Voice Risk
Policy, sandbox, and confinement all run each iteration. The harness decides only *whether* to
plan again; it never executes anything itself and never relaxes a control (guardrail #3 intact).
The observation block carries `authority="data-only"` and is sanitized (no backticks/braces/tags)
so command output can never become an instruction.
**Amendment (2026-08-17, P8.6):** the observation now carries a bounded tail of what each step
*printed*, plus its true `ExitCode`. Two consequences worth recording:
1. **Output is a genuine injection surface** — unlike an exit code, command output is fully
   attacker-controllable (a crafted filename, a poisoned log line). `sanitizeOutput` is its
   boundary. Sanitizer *ordering* is part of the contract: character-level cleanup runs before
   token-level neutralization, because stripping control characters can otherwise reassemble a
   token a regex already walked past (`Auth\x00ority=` → `Authority=`). A fuzz target
   (`FuzzSanitizeOutput`) pins this, and found exactly that bug.
2. **Exit codes are recorded because execution is lenient.** `RunShellCommand` deliberately treats
   a non-zero exit as success so the user is not nagged about `grep` finding nothing — which made
   the harness blind: a failing `go build` reported OK, `allStepsOK` returned true, and the loop
   stopped without replanning. `OK` keeps its meaning (the handler returned no error) and the
   captured `ExitCode` carries the truth; `allStepsOK` now judges on both. User-facing execution
   semantics are unchanged.

### ADR-014 — Sentence-pipelined TTS for time-to-first-audio.
**Decision (2026-08-17):** Spoken replies play sentence-by-sentence with one-ahead synthesis
(`speech.SpeakStream`): sentence N+1 is fetched from the TTS chain while sentence N plays. Time to
first audio becomes one short sentence's synthesis, not the whole paragraph's.
**Rationale:** The "JARVIS answers instantly" feel is dominated by first-audio latency, not total
synthesis time. Pipelining is a pure client-side win that works with every TTS provider (cloud or
local) and needs no streaming-TTS endpoint.
**Consequences:** Barge-in cancels at a sentence boundary (ctx cancellation between chunks) — good
enough for v1; sub-sentence interrupt stays future work.

### ADR-015 — Sci-fi voice HUD extends the Thinker pattern, never a GUI dependency.
**Decision (2026-08-17):** Voice-mode visual feedback (`internal/ux/voiceviz.go`) is a single-line
animated HUD in the existing TrueColor Helix palette — listening waveform, decode sweep, speaking
wave, wake-standby breathing pulse — built on the proven `Thinker` machinery (100 ms frame ticker,
cursor hide/heal, TTY-gated no-op). No GUI/framebuffer dependency; daemon/headless renders nothing.
**Rationale:** Delivers the "living AI" presence the vision calls for while honoring the CGO-free,
terminal-native, single-binary philosophy (guardrail #8).

### ADR-016 — Two local LLM runtimes (Ollama + llama.cpp), and a circuit-breaker brain failover.
**Decision (2026-08-17):** P11.4 is resolved in favor of **implementing** the `llamacpp` provider
rather than deleting the vestiges. `internal/providers/llamacpp` speaks `llama-server`'s
OpenAI-compatible API as a **user-managed sidecar** (ADR-002: no install, no weight download, no
CGO). Ollama remains the default and recommended local runtime; llama.cpp is the escape hatch.
Automatic cloud→local failover (`internal/ai/failover.go`) is a **circuit breaker**, not a
monitor-only flip:

| State | Behavior |
|-------|----------|
| CLOSED | calls go to the configured provider |
| OPEN | after N consecutive *availability* failures — or `SetOfflineMode(true)` from the daemon monitor — calls go to the local provider; one spoken + printed notice |
| HALF-OPEN | after `retry_after_s`, the next call probes the cloud; success restores it with a notice, failure resets the timer silently |

**Rationale:** (a) The edge matrix (`docs/edge_deployment.md` §5) already documents a board — the
first-gen Jetson Nano, frozen at JetPack 4.6 / CUDA 10.2 / Maxwell 5.3 — where **Ollama is
unsupported and a hand-built llama.cpp is the only local-LLM path**. Deleting the provider would
have left that board with no offline brain at all, contradicting Phase 10. (b) A breaker rather
than a pure connectivity hook is what makes failover work in the **interactive shell**, which has
no connectivity monitor; the daemon's monitor becomes one of two triggers, not the only one.
**Guardrails:** failover changes only *which model writes the plan* — every plan still re-enters
classify → planner → Instruction Firewall → risk tiers → Voice Risk Policy → sandbox → confinement
(§12 #3 intact). Only availability errors count (a 400/401 or Ctrl+C must stay visible, never be
hidden behind a quieter model). The breaker **health-checks the local provider before every
switch**, so on a machine with no local runtime it never engages and behavior is unchanged.
An explicit `/provider use` or `/model` outranks the breaker permanently.
**Consequences:** a second local runtime to document; `llm.fallback.enabled` is the project's
first default-**true** setting, which forces a `*bool` in config so an explicit `false` is
distinguishable from an absent section.

### ADR-017 — Sesame CSM-1B is a TTS provider, its runtime is Rust, and its context is a Helix extension.
**Decision (2026-08-24):** CSM-1B ships as a local `TTSProvider` (`csm-local`) over the
**csm.rs** Rust/candle sidecar, and Helix defines the conversational-context wire format that
CSM's quality depends on.

**Three things this ADR exists to pin down.**

1. **It is a voice, not a brain.** Sesame's blog describes a full conversational system; the
   open release is the speech generator from it, and the model card says it "cannot generate
   text". Anyone reading the demo and expecting speech-to-speech will otherwise mis-scope this:
   the planner still decides what to say, whisper.cpp still hears.
2. **Rust, not PyTorch — ADR-001/002 hold unchanged.** The reference implementation is Python.
   Helix uses `cartesia-one/csm.rs` (candle; CUDA/Metal/Accelerate/MKL) speaking an
   OpenAI-shaped `/v1/audio/speech`. External local HTTP service, no container runtime, nothing
   linked into the CGO-free binary. Unlike every other sidecar it is neither auto-installed (the
   backend is a compile-time choice, and choosing it would hand a GPU owner a CPU build) nor
   auto-downloaded (`sesame/csm-1b` is gated on Hugging Face).
3. **Context is a Helix extension, and it must never be assumed.** CSM conditions on prior turns;
   no CSM server implements a context field. Helix defines one (§7 of `local_runtimes.md`) and
   ships the upstream patch (`docs/csm-context.patch`, verified working). The load-bearing
   detail: csm.rs derives `Deserialize` **without** `deny_unknown_fields`, so an unpatched server
   *accepts and silently drops* the field. Detection therefore cannot rely on an error — the
   patched server returns `X-CSM-Context-Segments`, and absence of that header means "ignored".

**Licensing consequence, recorded because it is asymmetric with the other sidecars.** The
weights are Apache-2.0 but **csm.rs is AGPL-3.0** while Helix is MIT. Helix never links, bundles
or redistributes it — a user-installed separate process spoken to over HTTP — so Helix's licence
is unaffected. The AGPL obligation attaches to whoever *operates* the server; locally, that owes
nothing.

**Consequences:** a second class of local voice (quality vs latency: CSM vs Piper); the first
sidecar Helix declines to install *or* fetch weights for; and the first wire contract Helix
authors rather than consumes. Measured RTF on an M4 Air is 1.69× (slower than playback) because
csm.rs runs the quantized GGUF on CPU regardless of the metal feature — CSM is a discrete-GPU
capability, and Piper remains the default local voice.

---

### ADR-018 — Restarting is a supervisor, not `syscall.Exec`.
**Decision:** `/reboot` does not replace the process image. The original process
becomes a supervisor: it spawns a child Helix with `HELIX_REBOOT_SUPERVISED=1`,
ignores the terminal signals so they reach the child, waits, and exits with the
child's status. A supervised child that wants to reboot exits with status **86**
instead of spawning anything, and the supervisor already waiting starts the next
one — so however many times you reboot, there are exactly two processes.

**Rationale, in the order the alternatives were eliminated:**

1. **`syscall.Exec` crashes this binary.** It is the obvious answer — same PID,
   same terminal, no second process — and Go's implementation takes the runtime's
   exec lock, which in a program with live cgo callback threads lands on a thread
   the runtime cannot park and aborts with `fatal error: notesleep not on g0`.
   Helix has exactly those threads: the audio engine is CoreAudio through cgo,
   started at boot, and oto exposes no teardown to call before exec'ing. This was
   established against the real binary — the goroutine dump names the crash — not
   reasoned about.
2. **Spawn-and-exit is worse than the bug.** If the parent exits, the shell that
   launched Helix sees its foreground job finish and starts reading the terminal
   while the new Helix is also reading it. Two readers on one tty is a state
   nobody can type their way out of. And when Helix **is** the login shell, the
   parent exiting ends the session and hangs up on the child.
3. **Waiting without a loop nests.** A parent that waits fixes (2), but every
   reboot would then add another blocked parent. The exit-86 loop is what bounds
   it at two.

**Consequences, stated rather than discovered later:** the PID changes on every
restart (nothing depends on it — the TTY active-session lock is timestamp-based
and the new process refreshes it on its first turn); one idle process persists
for the life of the session, unmeasured on a small board; and the outgoing
process must be **quiesced** before it becomes a supervisor, because it does not
exit — its companion loop would keep sampling the camera and its speech would
keep talking while a second Helix did the same thing on the same devices.

### ADR-019 — The updater verifies integrity, and says it does not verify signatures.
**Decision:** `/reboot` installs a downloaded release only when its SHA-256
matches the checksums file published with that release, the URL never leaves a
pinned set of GitHub hosts, and the payload proves it is a Helix binary for this
machine by its Go build info. Sigstore signatures — which the release pipeline
does produce — are **not** checked; the UI prints the `cosign verify-blob`
command for verifying a release by hand.

**Rationale.** Keyless signature verification is only as good as the identity and
issuer constraints it is given. A verification that runs with the wrong ones
returns success while proving nothing, and it does so under a label that stops
anyone looking further — which is strictly worse than an honest checksum, because
it buys confidence it has not earned. The checksum chain that IS implemented is
end-to-end: the manifest comes from the same release over the same pinned host,
and the entry is matched by filename so one artifact can never be verified
against another's hash.

**Consequences.** An attacker who can publish a release to the configured repo
can ship a binary, and the checksum will match it — integrity is not
authenticity, and this ADR does not claim otherwise. What the controls do cover
is everything between GitHub and the disk: a tampered download, a redirected
download, a truncated download, an archive that is not a Helix binary, and an
archive that tries to write outside its extraction directory.

**Amendment (2026-08-27) — installing is automatic, including by voice.** The
first version asked for a typed confirmation and carved voice out entirely. The
owner's decision reverses both, and the reasoning belongs here rather than in a
commit message: the update comes from a repository the owner controls and tags
deliberately, so "is this build wanted?" is a question already answered by the
act of publishing it, and a prompt in front of that is a prompt with one sensible
answer.

What it shifts is stated rather than left implicit. Whoever can publish a release
to the configured repo can replace the binary with no human present, and a
bystander saying "reboot" can trigger it. That is a bet on the publisher, not on
the transport — the transport controls above are unchanged and mandatory, and the
supervisor still rolls back a binary that cannot start. `update.check: false`
declines the bet on a machine where the publisher and the operator are not the
same person, and `/reboot check` reports without installing.

**Revisit when** a signature check can be written with pinned identity and issuer
and tested against a real release, at which point it becomes a second mandatory
gate rather than a replacement for this one.

## §4. Target architecture (end state)

```
                         ┌────────────────────────────────────────────────┐
                         │                HelixDaemon (§ Phase 4)          │
                         │  event loop · supervision · session state       │
                         │  IPC: UDS/pipe (NDJSON) ← `helix remote`        │
                         └───┬──────────┬──────────┬──────────┬───────────┘
                             │          │          │          │
                       ┌─────▼───┐ ┌────▼─────┐ ┌──▼──────┐ ┌─▼─────────┐
                       │WakeWord │ │ STT      │ │ Agent   │ │ Vision /  │
                       │Service  │ │ Providers│ │ (head-  │ │ Ambient   │
                       │(sidecar)│ │ (cloud + │ │  less)  │ │ (opt-in)  │
                       └────┬────┘ │  local)  │ └──┬──────┘ └───────────┘
                            │      └────┬─────┘    │
                            │           │     ┌────▼──────────────────────┐
                       ┌────▼───────────▼─┐   │ UNCHANGED CORE (the moat) │
                       │ Mic capture       │   │ classify → planner →      │
                       │ (sox/ffmpeg or    │   │ firewall → safety tiers → │
                       │  audio_cgo)       │   │ sandbox/confinement →     │
                       └───────────────────┘   │ execute                   │
                                               └────┬──────────────────────┘
                        ┌──────────────────────┐    │
                        │ TTS (cloud + local   │◄───┘  responses + VoicePrompter
                        │  Piper) via oto      │       confirmations (fail-closed)
                        └──────────────────────┘
```

**New packages to create (all under `internal/`, each with `_test.go` from day one):**

| Package | Purpose | Phase |
|---------|---------|-------|
| `internal/speech/` | `STTProvider`/`TTSProvider` interfaces, speech registry, adapters (OpenAI Whisper, Deepgram, ElevenLabs, whisper.cpp-sidecar, Piper-sidecar), pricing catalog | 1 |
| `internal/input/` | Input-source abstraction (`InputEvent{Text, Channel}`), tty/voice/hybrid sources | 2 |
| `internal/wakeword/` | Wake-word engine adapters (sidecar first) + tuning/sensitivity config | 3 |
| `internal/session/` | Conversation memory (ring buffer, planner context injection), interaction journal, safe-subset undo | 4 |
| `internal/daemon/` | Event loop, IPC server, supervision, connectivity monitor | 4 |
| `internal/vision/` | Camera capture (ffmpeg shell-out), frame lifecycle, multimodal plumbing | 5 |
| `internal/ambient/` | Audio monitoring: energy/FFT rules, category config | 6 |
| `internal/agent/` additions | `Renderer` interface (TTY vs headless), `policy_voice.go` | 2 & 4 |

**Modified existing files (summary):** `cmd/helix/main.go` (input-source loop, daemon subcommand),
`cmd/helix/handlers.go` (new slash commands), `internal/providers/types.go` (multimodal
`ChatMessage` parts — Phase 5, backward compatible), `internal/ai/model.go` (session context
injection — Phase 4), `internal/config/config.go` (speech/vision/ambient/policy config),
`internal/audio/` (speech playback API), `internal/commands/prompt.go` (unchanged interface —
new implementation), `tests/e2e/` (mock speech/vision endpoints).

---

## §5. Voice & Multimodal Threat Model (extension of `docs/threat_model.md`)

**New attack surfaces introduced by BlackBox, and their controls:**

| # | Threat | Control |
|---|--------|---------|
| V1 | **Voice-channel injection**: ambient audio (TV, podcast, nearby person) transcribed into text with full user authority; bypasses Instruction Firewall (which guards RAG text only) | ADR-005 Voice Risk Policy: Medium-risk cap, fail-closed confirmations, wake lockout, provenance escalation on transcripts |
| V2 | **Wake-word false positive** → unintended listening/execution | Sensitivity config + cooldown/debounce; any execution still passes full safety pipeline; metrics logged (FP/hour) |
| V3 | **Voice mimicry / playback attack** confirming dangerous actions | Dangerous actions are typed-confirmation-only — voice confirmation is structurally impossible (ADR-005 §2) |
| V4 | **Camera privacy**: frames leaked, stored, or sent to unintended provider | Off by default; opens only on an explicit act — `/blackbox eyes on`, or `/blackbox on`, which enables it as part of going live. **That second path is a deliberate widening (2026-08-22)**: live mode is a camera consent moment by definition, so it is announced (TTS + banner) and `/blackbox off` closes the camera with the mode. No disk persistence (fs-snapshot test enforced), one configured vision provider (`vision.provider`/`vision.model`), journal entry per frame batch, `/blackbox eyes off` and "turn off your eyes" immediate. The phrase is matched loosely on purpose: a privacy control should fail toward closing the camera. |
| V4b | **Unattended capture** (2026-08-22): the companion loop samples the camera on a timer, with no per-frame user action | Runs only inside live mode, which the user entered explicitly and which announced the camera; stops with `/blackbox off` or "turn off your eyes"; `companion.enabled=false` disables initiative while keeping the camera available on request. Frames are diffed **in-process** and an unchanged scene never reaches a model, so a still room is not silently streamed to a provider. Same memory-only and journal guarantees as any other frame. |
| V4c | **Camera intent guessing** (RESOLVED 2026-08-22): a heuristic fired the camera on any spoken sentence containing "this"/"that"/"here" | Removed. It captured frames on ordinary phrasing ("what do we have in *this* directory?") and answered from a vision model with no knowledge of the shell. The camera now has exactly two doors, both explicit: `/blackbox look`, and the planner choosing its `vision` tool. |
| V5b | **Conversational context retention** (ADR-017). Enabling a context-conditioned voice makes Helix hold recent turns — including captured USER audio — in memory for longer than the turn that produced it, where a clip was previously discarded the moment it was transcribed. | Off by default (`speech.tts.context_turns: 0`). **Memory only** — the store imports no filesystem or network API, enforced by a test, so "captured audio is never written to disk" (guardrail §12 #6) is unchanged and `/purge` has nothing new to reach. Bounded twice, by turn count and total bytes, oldest-first. Scoped to live mode: `/blackbox off` drops it. The audio held was already in memory a moment earlier for transcription; what changed is how long, which is why the bounds are the control rather than a detail |
| V5c | **Microphone opened during Helix's own reply** (2026-08-26). Sentence-boundary barge-in samples the mic in the pause between spoken sentences, so the recorder runs at moments the user did not initiate a turn. | Off by default (`/config barge-in on`). ~250 ms per sentence boundary; the clip takes the same path as any capture (temp WAV, deleted the moment it is read) and only an RMS value is computed — never transcribed, never sent to a provider, never logged, never entering conversational context. Threshold is 10× the ordinary speech gate. Scoped to live mode; shown on the INTERRUPT row of `/blackbox status`. |
| V5d | **Voice-triggered persistence of what you just said.** A spoken `/reboot` could have written an excerpt of the last utterance to `~/.helix/reboot.json` on a channel a television can trigger. | **It does not, and that is the control.** `captureContinuity` omits conversation content entirely when the request arrived by voice: a spoken restart carries the mode, working directory, provider/model and in-progress task texts, and nothing you said. ADR-005's reduce-never-increase principle therefore holds **without an exception** — the feature was shaped to fit the rule rather than the rule amended to fit the feature. A TYPED reboot stores a 240-rune excerpt (rune-boundary truncated, because a severed UTF-8 sequence makes the record unparseable and silently dropped), 0600 in 0700, consumed on read, ignored past 12 h, `/purge` wipes. |
| V5e | **A spoken word installs software.** `/reboot` self-updates — it downloads a release and makes it the program the user runs — and it is voice-reachable, so a television or a bystander could in principle cause an install. | **Accepted, by owner decision, and bounded rather than blocked.** The spoken path DOES install: the release comes from a repository the owner controls and tags deliberately, so publishing it is the authorization, and a confirmation prompt in front of that has one sensible answer. What remains is ADR-019's chain — mandatory checksum matched by filename, a pinned host with redirects refused, a payload that must prove it is Helix for this machine — plus the supervisor's automatic rollback when the new binary cannot start within ten seconds. The residual risk is explicit: whoever can publish to the configured repo can replace the binary with no human present, and a bystander saying "reboot" can trigger the install. `update.check: false` removes it on a machine where the publisher and the operator are not the same person. |
| V5f | **A spoken word ends the process.** `/reboot` is voice-reachable, so a television, a podcast or a person in the room saying "reboot" at the end of a sentence restarts the shell. | Bounded by making the restart CHEAP rather than hard to trigger. The continuity record is written **before** the process ends, so a false positive costs a few seconds and a shell that comes back in the same mode, directory, provider and conversation. Matched as a **suffix**, so it must end the utterance, and **questions are excluded** by their opening word — "what happens when you reboot" is answered, not obeyed — because STT punctuation is a guess and several providers never emit a question mark. Journalled as its own outcome (`reboot`) so an audit can tell a spoken restart from a typed one. |
| V5 | **Audio/ transcript persistence leakage** | **Shipped 2026-08-23 (P2.8).** Voice log opt-in and default ABSENT — no directory, no file — enforced by a unit test and an e2e test against the real binary (a log opened eagerly at startup would pass the first and fail the second). Text and metadata only, never audio: clips are deleted as soon as they are read, so there is nothing to reference, and a test asserts no entry names an audio file. 0600 in 0700, control characters stripped, entries length-bounded, rotated 1 MiB × 3. `/purge` wipes it. Voice may STOP recording but never start it — enabling moves the privacy posture, so it is typed-only. `internal/journal` imports no networking (grep-enforced) |
| V6 | **Cloud provider data exposure** (audio/text/frames to STT/TTS/vision vendors) | Explicit per-provider opt-in with key entry; wizard shows exactly what is sent where; local sidecar path documented as the private default |
| V7 | **Daemon IPC hijack** (local attacker sends commands to socket) | Socket 0600 in `~/.helix/` (0700 dir); optional shared-token file; daemon refuses requests when TTY session is "locked" |
| V8 | **Sidecar supply chain** (whisper.cpp/Piper installers) | Installers pin versions + checksums; mirror the Ollama installer's explicit-consent UX **Piper's standalone binary is the concrete case (2026-08-27):** release tag and SHA-256 pinned in `internal/speech/piper_native.go`, verified before extraction, archive deleted on mismatch, and extraction refuses any entry escaping the destination. Implemented in Go because `runVisibleCommand` execs with no shell — a `curl`/`shasum`/`tar` pipeline could not have run, let alone verified. |

**Residual risk (honest):** an attacker with physical proximity who can produce convincing speech
in wake state can attempt Medium-risk actions; controls make this loud (confirmations, journal,
chime) and bounded (no High risk, no destructive git). This is the accepted trade-off for a
voice-first assistant.

---

## §6. Phase-by-Phase Roadmap

> Estimated durations assume one focused developer. Every phase ends with: all tests green
> (`make test`, `make e2e`), Progress Tracker updated, conventional commits pushed to `blackBox`,
> and a phase tag (`blackbox-phaseN`).

---

### Phase 0 — Decisions, Guardrails & Threat Model

**Goal:** Lock the architecture, write the security groundwork, verify the baseline. No feature code.

**Tasks:**
- [x] P0.1 Verify baseline: `make test`, `make e2e`, `make current` all green on `blackBox` at `fd34503`+.
      *(2026-08-16: `go test ./... -count=1` exit 0 — all 25 packages incl. tests/e2e.)*
- [x] P0.2 Ratify ADR-001…009 (§3) — adjust only with written rationale appended.
      *(Ratified with the roadmap commit `b1f5302`.)*
- [x] P0.3 Write `docs/threat_model_voice.md` from §5 (adapted into repo doc style). *(2026-08-16)*
- [x] P0.4 Draft the config schema (§7) as a reviewable doc section in this file — no code yet. *(In §7 since roadmap commit.)*
- [x] P0.5 Create package skeletons that **compile**: `internal/input`, `internal/wakeword`,
      `internal/session`, `internal/daemon`, `internal/vision`, `internal/ambient` with
      purpose doc-comments, forward-contract types, and one real test each.
      (`internal/speech` is implemented directly in Phase 1.)
- [x] P0.6 Extend `.github/workflows/ci.yml` if needed so new packages are covered by `go vet`/build
      matrix on the 3 OSes. *(Verified: CI already runs `go test ./...` + `go build` — new
      packages are covered automatically; no change required.)*

**Deliverables:** threat model doc, compiling skeletons, updated tracker.
**Acceptance:** CI green including new packages; ADRs finalized.
**Risks:** none material.

---

### Phase 1 — Speech Provider Layer & TTS Output

**Goal:** Helix can **speak** (TTS through its own speaker stack) and **transcribe** (recorded clip
→ text via any configured provider), with the multi-provider registry, failover, pricing-aware
setup wizard, and push-to-talk recording utility. No main-loop integration yet (that's Phase 2).

**Tasks:**
- [x] P1.1 `internal/speech/types.go` — core contracts (modeled on `AIProvider`):
  `AudioFormat{Kind,SampleRate,Channels,Bytes}`, `Transcript`, `SynthesisOptions`,
  `STTProvider`, `TTSProvider`, `StreamingSTTProvider` (interface defined; WS implementations
  land with Phase 3). Deviation from sketch: `ListModels` dropped from the v1 interface — the
  wizard's model choice comes from the pricing catalog (cleaner; adapters take model at
  construction).
- [x] P1.2 `internal/speech/registry.go` — registry copy of the providers pattern + **failover
      chains** (`STTChain`/`TTSChain` resolve [primary]+fallbacks; `Transcribe`/`Synthesize`
      walk the chain, aggregating failures via `errors.Join`). Keys namespaced `stt.*`/`tts.*`
      in the shared keystore; `providers.HTTPClient` gained `DoRaw` (multipart/binary bodies)
      and `RawClient()` (plain GET health probes); keystore env-mapping extended
      (`stt.openai`→`OPENAI_API_KEY` etc.).
- [x] P1.3 `pricing.json` (embedded) + `pricing.go` — 8 entries (openai/deepgram/whisper-local
      STT; openai tts-1/tts-1-hd, elevenlabs turbo/multilingual, piper-local TTS) + user
      override merge (`~/.helix/pricing.json`) + monthly-cost estimator + `FormatUnit`.
- [x] P1.4 Cloud adapters (httptest-tested): OpenAI STT (multipart), OpenAI TTS (requests WAV),
      ElevenLabs TTS (requests PCM 24k), Deepgram STT (nova-2 REST).
- [x] P1.5 Local sidecar adapters: `whisper-local` (OpenAI-compatible whisper.cpp server, no
      auth header) and `piper-local` (`/api/tts` WAV).
      > **Both routes named here were wrong** (recorded, not rewritten — this is what shipped).
      > A stock `whisper-server` transcribes at `/inference` and Piper's `http_server`
      > synthesizes at `/`, so as written both adapters got HTTP 404 for every request and
      > local speech was unusable. Replaced by route discovery: try the known routes, remember
      > which answered, print it in `/blackbox status`. Proven against the real binaries on
      > 2026-08-23 (see the Phase 1 acceptance criteria) — a mock could not settle it, because
      > the repo's fakes implemented the same wrong contract the adapters did.
- [x] P1.6 `internal/audio/speech.go` — `PlaySpeech(SpeechFormat, volume)`: pure-Go WAV
      (int16 + float32) and raw-PCM decode, beep resample to 44.1kHz, 120s playback cap,
      volume scaling; noop backend returns `ErrSpeechUnsupported`. **MP3 deliberately not
      decoded** — resolves roadmap open question: all providers configured for WAV/PCM.
- [x] P1.7 `internal/speech/capture.go` — `DetectRecorder` (sox preferred, ffmpeg fallback) +
      `RecordClip` (sox trailing-silence stop = crude VAD; ffmpeg fixed duration; 0600 temp
      files deleted after read; tolerant WAV parsing for killed recordings).
- [x] P1.8 `/voice-setup` wizard (pricing table: #, provider, model, price, est $/mo@2h/d,
      latency, key?, local?, ★ recommended) + key entry + fallback selection + voice id;
      `/voice-status` (merged `stt-status`+`tts-status` deviation — one command, chains +
      health + recorder); runtime `/tts on|off`.
- [x] P1.9 Slash commands wired in dispatcher + `/help` VOICE (BLACKBOX) section; `helix` main
      loop calls `speech.Init` (non-fatal on error).
- [x] P1.10 E2E: harness mock gained `/v1/audio/speech` (WAV + hit counter) and speech-enabled
       config path (`HELIX_E2E_SPEECH=1`); `TestE2E_SayHitsMockTTS` proves /say → mock TTS
       round trip; `TestE2E_TTSToggle`. Unit: registry failover matrix (5 tests), pricing
       (parse/override/estimates), 7 adapter contract tests, WAV parser (incl. stale-size
       tolerance), audio decode (mono/stereo/mp3-reject/disabled-fail-closed), recorder smoke.
- [x] P1.11 Fuzz seeds — **fully closed 2026-08-23.** The WAV half landed in the Phase 7
       hardening pass as `FuzzWAVHeaderInfo`; the **pricing-merge half was never written**, and
       this checkbox had been ticked with only one of its two halves done. `FuzzPricingMerge`
       closes it: `~/.helix/pricing.json` is user-authored by design (ADR-006 exists so a user
       can fix rotted prices without a rebuild), which makes the merge a parser of untrusted
       input under §9 rule 5. The contract it pins is availability, not just absence of panics —
       `LoadMergedCatalog` must ALWAYS return a usable catalog, because a wizard that cannot
       render its table cannot configure speech at all — plus that every surviving entry stays
       safe to render and to price (a NaN would print as "$NaN/mo" in a column of dollars, which
       reads as a bug in Helix rather than a bad override). 46k execs clean; listed in both
       Makefile fuzz targets.

**Files touched:** new `internal/speech/*`; `internal/audio/` additions; `cmd/helix/handlers.go`,
`helpers.go`, `main.go` (wizard wiring); `internal/config/config.go` (speech section);
`tests/e2e/*`; `.github/workflows/ci.yml` (audio-dependent tests skip gracefully when no device).

**Acceptance criteria:**
- [x] Setup wizard with pricing table implemented (now `/blackbox setup`; manual QA of audible
      speech pending on a machine with speakers + real key — see dev log).
- [x] (2026-08-22) Wizard hardening from a real first-run: per-provider endpoint overrides so a
      moved sidecar port survives the commit step, saved API keys verified-and-reused instead of
      re-requested, and keys entered for an AI provider adopted for the same vendor's speech.
- [x] Push-to-talk helper (`/listen`) records → transcribes → prints with provider (+confidence
      when the provider reports it).
- [x] Failover proven by unit test (`TestRegistrySTTFailover`/`TestRegistryTTSFailover`).
- [x] Local sidecar adapter against a real whisper.cpp server — **done 2026-08-23, and both
      directions rather than just STT.** Run on an M4 Mac against a stock `whisper-server`
      (Homebrew `whisper-cpp`, `ggml-base.en.bin`) and piper's own `http_server`
      (`en_US-lessac-medium`), driving **Helix's own adapters and registry chain** rather than
      curl:

      | Adapter | Result | Latency |
      |---------|--------|---------|
      | `whisper-local` | "The quick brown fox jumps over the lazy dog." transcribed exactly | **167 ms** |
      | `whisper-local` via registry chain | same, provider reported `whisper-local` | ~1 turn |
      | `piper-local` | 83,500 bytes of WAV, decoded by Helix's own decoder to 41,728 samples @ 22,050 Hz mono, non-silent | **103 ms** |

      Kept as a repeatable test (`internal/speech/live_sidecar_test.go`) rather than a prose QA
      log, because the bug that made this item worth holding open cannot be caught by a mock:
      both local adapters shipped pointing at routes their upstream servers do not serve
      (whisper.cpp answers at `/inference`, piper at `/`), and every fake in the repo agreed
      with the fake. It is **opt-in via `HELIX_LIVE_SIDECAR=1`** and skips loudly with the
      reason when the binary, model or voice is absent (§9 rule 6) — a CI run reports "not
      exercised" instead of passing silently. Route discovery is asserted against the stock
      server specifically, including that the discovered route is reused on a second call.
      **Not covered, deliberately:** playback. That needs a device, §9 rule 1 keeps audio
      hardware out of the suite, and "can Helix decode what the sidecar returned" is the half
      that has ever actually broken.
- [x] `make test` equivalent (`go test ./... -count=1`) green on all 31 packages incl. e2e
      (2026-08-16); zero regressions to pre-existing suites.
- [x] No regressions: full existing suite passes untouched.

**Risks:** audio decode licensing/MP3 patents (use WAV/PCM from providers where offered; mp3 decode
via pure-Go decoder if needed); sox/ffmpeg absence (detected + guided install).

---

### Phase 2 — Voice Input Loop, Mode Switching & Voice Risk Policy

**Goal:** The first true voice loop: push-to-talk → transcript → **existing** pipeline → spoken
result, with `/voice` ↔ `/manual` switching and the Voice Risk Policy enforced end-to-end.

**Tasks:**
- [x] P2.1 `internal/input/` abstraction — `Channel`/`InputEvent` shipped in Phase 0 and now
      load-bearing end-to-end. Design note: the REPL uses per-mode dispatch (typed turn vs
      voice turn) rather than a channel-based `Source`; the `Source` interface remains the
      contract for Phase 4's daemon and Phase 7's hybrid mode. Formal TTYSource/VoiceSource
      implementations deferred to Phase 4 when a second consumer exists.
      > **Status check 2026-08-23: the second consumer never arrived, and neither did the
      > implementations.** `TTYSource`/`VoiceSource` do not exist; Phase 4's daemon and the
      > interactive REPL both construct `InputEvent` values directly. `Source` has exactly one
      > real implementation, `HybridSource` (P7.1), and **nothing outside `internal/input`
      > consumes it** — so hybrid mode is built and unit-tested but not reachable by a user.
      > The doc comment on `Source` said otherwise for months and now says this. Not fixed
      > here on purpose: wiring it means letting a blocking raw-mode line read race a voice
      > capture, which is a REPL change under the "interactive behavior unchanged" guardrail,
      > not plumbing. Recorded against P7.1 as well, since that is the phase that claims it.
- [x] P2.2 REPL integration (`cmd/helix/main.go`): mode dispatch each turn; `/voice` refuses
      entry without recorder+STT (mic-less machines never get stranded); per-turn graceful
      typed fallback on capture/STT failure; mode persisted in `user_preferences.voice_mode`;
      chime via PlayAlert on arm.
- [x] P2.3 Channel-aware dispatch: `Agent.HandleInputEvent(ev)` stamps channel+meta, applies
      the confidence gate, delegates to the untouched `HandleInput` pipeline; channel resets
      after every turn (no leakage).
- [x] P2.4 `internal/agent/policy_voice.go` — Voice Risk Policy engine (ADR-005):
      voiceCapRisk matrix (High unreachable from voice; Medium stays confirm-gated),
      confidence gate (0/unknown never gates), `VoiceDenyList` documentation contract,
      spoken refusals via `OnSpeak` seam. NOTE: hard validation already blocks all known
      High patterns before the analyzer, so the analyzer-level ceiling is defense-in-depth —
      exactly like the analyzer's own High branch (documented in both code and tests).
- [x] P2.5 `VoicePrompter` (`cmd/helix/voice_prompter.go`) implements `commands.Prompter`:
      speaks questions, one clarification round for yes/no, fail-closed on silence/timeout/
      unintelligible; `AskTypedConfirmation` ALWAYS refuses with spoken guidance (makes the
      deny list voice-proof). The agent's 4 confirmation sites now route through
      `commands.AskForConfirmation` (the swappable seam) — behavior identical in text mode.
- [x] P2.6 Spoken responses: `handleResponseStep` speaks when channel=voice (gated by
      `/tts on|off` via the OnSpeak wiring in main). Synchronous v1; async+cancellable
      deferred to Phase 3 barge-in.
- [x] P2.7 Clarification loop — **no longer partial; closed 2026-08-23.** Low-confidence
      transcripts get a spoken "repeat" request; confirmations are conversational via
      VoicePrompter. The multi-turn half turned out to be delivered by Phase 4 session memory
      and never verified: a clarifying question is captured as the turn's `Reply`, so the
      user's answer reaches the planner with the question already in `<session_history>`.
      **Two defects on the gate path had to be fixed for that to actually hold.** The gate
      spoke "could you repeat it?" without recording that it had asked — so the repeat arrived
      with no sign a question had been posed, and read as a fresh request. And the rejected
      transcript was stored as ordinary user speech: text the policy had *just refused to act
      on* became authoritative context for the next twenty turns, so the planner could answer
      the misheard version of a question nobody asked. `session.Turn` gains an optional
      `Unreliable` flag (omitempty; old session files load unchanged), and the planner sees the
      turn labelled `user(voice, not understood)` — a flag the prompt does not surface would
      change nothing about the model's behavior.
- [x] P2.8 Voice interaction log (opt-in, default absent) — **done 2026-08-23**, and the
      deferral paid off exactly as intended: the shared machinery is `internal/journal`
      (`Appender` + `Redact` + rotation), which the Phase 4 daemon journal now uses too.
      Building it in Phase 2 would have produced one implementation and then a second.
      `/blackbox log on|off|status|show`; `~/.helix/voice_log/voice.jsonl`; records what was
      heard (with STT provider, confidence, and the OUTCOME — planner, spoken command, kill
      phrase, or policy refusal) and what was said; **text only, never audio** (§7 correction);
      rotation is the half that did not exist before — the daemon journal had been described as
      rotated since the roadmap was written and never was. Voice can stop the log but not start
      it (see the ADR-005 note below).
- [x] P2.9 Synthetic transcript injection: `internal/agent/policy_voice_test.go` — 8 tests
      covering the policy matrix (cap, gate, reset, deny-list contract, spoken-vs-silent
      refusals); VoicePrompter fail-closed suite (10 tests); e2e mode-switch tests
      (safety valve, refusal-without-STT, loop integrity after mode churn).

**Acceptance criteria:**
- [x] Voice loop implemented end-to-end (push-to-talk per turn; hands-free wake arrives
      Phase 3). Real-mic manual QA still pending (logged).
- [x] Medium-risk by voice routes through VoicePrompter confirmation; silence/timeout
      declines (unit-proven, fail-closed suite).
- [x] Typed-confirmation actions are structurally voice-proof (`AskTypedConfirmation`
      always refuses — unit-proven with the exact phrase).
- [x] `/manual` instantly returns to typing; `/voice` refuses safely without mic/STT
      (e2e-proven, loop integrity after mode churn).
- [x] Policy unit tests cover the matrix (8 agent tests + 10 prompter tests).
- [x] All pre-existing tests green (full suite, 24 packages, 2026-08-16).

**Risks:** VoicePrompter concurrency (TTS playback while listening — half-duplex v1: stop
playback before listening; full-duplex/barge-in deferred to Phase 3); transcript classification
edge cases (speech transcripts always NL-routed — verify classifier behavior with tests).

> **The classification risk was real, and the verification it asked for was never written until
> 2026-08-23.** "Speech transcripts always NL-routed" was false. Measured against realistic
> phrasings, 9 of 9 sentences whose first word is an executable classified as high-confidence
> shell commands and would have executed verbatim — "make a new branch called test", "top three
> biggest directories", "test the code", "history of my commands", "clear the screen". The
> planner would have produced `git checkout -b test` for the first.
>
> **Not a safety bypass, and worth being precise about that:** the direct path runs
> `handleShellStep`, so validation, risk tiers and the ADR-005 Medium cap always applied —
> guardrail §12 #3 was never broken. It was a correctness bug. Voice reaching the whole shell,
> which the ADR-005 amendment widened toward, has to mean the PLANNER reaching it, not the
> classifier guessing from one word.
>
> Fixed by `Agent.directShellAllowed`: typed input keeps the direct path byte for byte, voice
> never takes it. The nine measured sentences are kept as a regression corpus, plus a
> meta-test that fails if none of them still trips the classifier — otherwise the regression
> test would quietly go hollow and pass for the wrong reason. Same shape as the deictic camera
> hijack: a pattern match on English intercepting a spoken sentence before the model that
> could understand it, resolved the same way — delete the shortcut, let the planner choose.

---

### Phase 3 — Wake Word & Hands-Free Operation

**Goal:** "Hey Helix" hands-free activation with tuned false-positive behavior, replacing
push-to-talk as the default voice-mode interaction.

**Tasks:**
- [x] P3.1 Engine decision (ADR-002-consistent, no spike binary needed): **energy detector is
      the shipped default** (pure Go, everywhere-works, honest: detects speech/loud-sound
      onset, not the phrase) + **sidecar client for openWakeWord-class services** (documented
      `/predict` JSON contract) for true keyword spotting. Porcupine rejected (licensing +
      access key). User selects via `speech.wake_word.engine` = `energy` | `sidecar`.
- [x] P3.2 `internal/wakeword/` — `EnergyDetector` (normalized RMS, preset thresholds
      strict/balanced/loose), `SidecarDetector` (+health), `service.go` chunk-scanning loop
      with cooldown debounce, fixture-`Scanner` seam for tests, `NewSoXScanner` production
      scanner (sox chunk recording; `speech.CaptureOptions.NoSilenceStop` added — chunk
      scanning must yield quiet clips, not silence-gate them into errors).
- [x] P3.3 Flow wiring: after each voice turn, the REPL holds in **wake-only listening**
      (nothing transcribed between turns — ADR-005 §5 by construction) for a 60s idle window;
      wake event → chime → next voice turn. Kill switches: "stop listening"/"go to sleep"/
      "manual mode" + `/voice off`/`/manual`; kill phrases recognized before dispatch.
- [x] P3.4 Barge-in v1: wake chime cancels the idle state (full TTS-playback cancellation
      rides the Phase 3 speech-queue work — noted as partial; spoken responses are short).
      > **No longer partial:** P12.5 delivered true mid-sentence cancellation
      > (`audio.PlaySpeechContext` + `ctxStreamer`, ~50 ms), and registered it with the
      > interrupt manager inside `SpeakStream` so every caller gained it at once. The residual
      > is unchanged and documented there: a *mic-triggered* barge-in still needs echo
      > cancellation. Ctrl+C and any programmatic caller work today.
- [x] P3.5 Metrics: wake events appended to `~/.helix/metrics/wake.jsonl` (0700/0600, local
      only). ~~`/voice-stats` display command deferred to polish (file is plain JSONL).~~
      **Display shipped 2026-08-23 as `/blackbox stats`.** The deferral note was true and not
      enough: four metrics files had been WRITTEN since Phase 3 and **nothing ever read one**,
      which is why P7.8 — a release gate — had no tooling behind it. New `internal/metrics`
      owns both ends (paths, field names, parsing, summaries), because a reader written anywhere
      else would have re-declared the field names and been free to disagree with the writer —
      the same dropped-at-the-boundary bug that cost the speech config its `Endpoints` three
      times. **A fifth file was missing entirely:** TTS time-to-first-audio, the one §10 number
      with a hard millisecond budget, lived only in an atomic and vanished on exit, so the
      directory the roadmap says holds "all §10 numbers" never held it. Now `speech.jsonl`.
      The report's honesty rules each cost it a number it could have shown: samples are judged
      against the cloud or local §10 column **by their own recorded provider**; a p95 is only
      printed above 20 samples (below that it is the maximum in a costume); an absent file reads
      as "not measured", never as a pass; and a metric whose typical case passes while its worst
      case fails reads **"typical only"** rather than a flat "meets target" — the first version
      printed "meets target" beside a visible 6.80s worst case against a 6.00s budget. The wake
      false-positive rate is reported as an explicit PROXY (wakes with no answering turn within
      60s), because Helix cannot know whether a user meant to wake it, and a real FP rate is not
      observable from here.
- [x] P3.6 Tests: RMS quiet-vs-loud, preset matrix, sidecar contract (score parsing,
      content-type, health, unreachable), service debounce + clean cancellation — all via
      synthetic fixtures, zero hardware.
- [x] P3.7 Sidecar setup docs → covered in docs/blackbox.md (Phase 7).

**Acceptance criteria:**
- [x] Fixture-corpus detection behavior unit-proven for both engines (real ≥97% keyword
      accuracy applies to the SIDECAR engine and needs a live sidecar — manual QA pending).
- [x] Between-turn lockout proven by construction (wake-only loop; no STT calls between
      turns) — wake→transcription starts immediately on event. **The construction is now
      ENFORCED (2026-08-23)**, not merely true: `TestWakeLoopNeverTranscribes` walks the
      package's AST and fails on any call to `speech.Transcribe`/`SpeakStream`/`Synthesize`,
      because nothing was stopping a future edit from adding one convenience call inside the
      scan loop and quietly sending every ambient chunk of a quiet room to a cloud provider
      (threat V1/V6, and a bill). Its complement asserts the loop still USES capture — a
      lockout enforced by the loop not listening is not a lockout.
- [x] Kill switch from voice (phrases) and terminal (/voice off, /manual) — the /manual and
      /voice-off e2e tests from Phase 2 still gate this.
- [x] Fixture tests run in CI without the sidecar (pure-Go energy engine; sidecar tests hit
      httptest mocks).

**Risks:** engine licensing (Porcupine); FP tuning time (budgeted); continuous capture CPU (chunk
size + sleep intervals; measure and record % CPU in dev log).

> **The CPU measurement was never taken until 2026-08-23.** Phase 6 benchmarked the ambient
> analyzer (26µs — a 1024-sample window, not a chunk; corrected 2026-08-23 when the production
> path measured 623µs) and the wake detector — which runs on every chunk of a permanently-open
> microphone — never got the same treatment. **Measured: 21.4µs per 1500 ms chunk = 0.0014% duty
> cycle, zero allocations** (M4; `BenchmarkEnergyWake`, plus silence and WAV variants). Pinned by
> `TestEnergyWakeDutyCycleIsNegligible` against a deliberately loose 1% budget — the point is not
> the number on one machine, it is failing if detection ever becomes expensive enough to matter on
> a Pi sharing the board with a local model. **Scope, stated because it bounds the claim:** this
> is DETECTION cost, not capture. sox records in another process; what Helix controls is the
> decision per chunk, which is the part that runs forever.

---

### Phase 4 — Headless Agent Refactor + HelixDaemon ("Living AI")

**Goal:** `helix daemon` runs as a supervised background service with session memory, IPC, journal,
graceful degradation, and auto-start on boot. The interactive TUI and the daemon share the same
headless-capable Agent core.

**Stage 4A — Agent decoupling (prerequisite, ~1 week):**
- [x] P4.1 Introduce `internal/agent.Renderer` interface capturing exactly what Agent uses from
      `*ux.UX` today (typewriter, status lines, thinker spinner); implementations:
      `TTYRenderer` (wraps ux) and `HeadlessRenderer` (no-op + structured log). Replace direct
      `*ux.UX` field (`internal/agent/agent.go`) — mechanical, test-guarded.
- [x] P4.2 Move slash-command dispatch behind a `SlashDispatcher` interface so the daemon can run
      without `cmd/helix` closures (wired through `OnSlashCommand` at the time; that symbol
      was removed with the blackBox blueprint — the equivalent today is `handleSlashCommand`
      in `cmd/helix/registry.go`).
- [x] P4.3 Verify: interactive behavior byte-identical (PTY e2e suite unchanged and green is the
      proof).

**Stage 4B — Session state & memory:**
- [x] P4.4 `internal/session/` — `SessionStore`: ring buffer (last N=20 turns default), persisted
      `~/.helix/session.json` (0600); injected into planner calls at
      `internal/ai/model.go:151` (message construction gains optional prior-context prefix —
      keep planner prompt strict-JSON contract intact; history goes in a clearly-fenced context
      block with data-only authority, mirroring firewall conventions).
- [x] P4.5 Referential queries: "what did I ask five minutes ago", "do that again" answered from
      the store; new slash commands: `/memory <clear|show>`.
- [x] P4.6 Safe-subset **undo journal**: actions with a known reversal (git commit → `git reset
      --soft HEAD~1`; file created → move to trash dir `~/.helix/trash/`) are journaled;
      `"undo that"` offers the reversal (still passes the safety pipeline + risk policy).
      Explicitly out of scope: reversing overwrites/deletes (documented honestly).

**Stage 4C — Daemon & IPC:**
- [x] P4.7 `internal/daemon/` + `cmd/helix/daemon_cmd.go` — `helix daemon`:
      owns wake service, speech providers, VoiceSource, headless Agent; supervision loop
      (panic recovery via `diagnostics.Guard` precedent, restart backoff); owns sidecar health
      checks (Ollama, whisper, piper, wakeword — `Health()` polling).
- [x] P4.8 IPC (ADR-004): UDS `~/.helix/daemon.sock` 0600 (named pipe on Windows); NDJSON
      protocol: `{type:"status"|"submit"|"mode"|"log_tail"|"stop", ...}`; client
      `helix remote status|say|mode|logs|stop`; optional token file auth; refuses `submit` while
      a TTY session holds the "active session" lock.
- [x] P4.9 Single-instance rules: daemon and interactive TUI coordinate via the socket (TTY lock
      transfers mic ownership; both can serve text).
- [x] P4.10 Graceful degradation: connectivity monitor (reuse `/online` logic) → on loss: switch
      STT/TTS to local chain, spoken notice, log event; on restore: switch back.

**Stage 4D — Service installers & crash safety:**
- [x] P4.11 `helix daemon install|uninstall|status`: launchd plist (macOS, `~/Library/LaunchAgents`),
      systemd user unit (Linux), Windows service via `sc.exe`/winged wrapper — templates embedded,
      user consent required, docs per OS.
- [x] P4.12 Crash & journal: interactions journaled append-only `~/.helix/journal/` (redacted,
      rotated, `/purge` wipes); crash reports via existing `internal/diagnostics`; supervisor
      restarts on failure with backoff.
- [x] P4.13 Ambient presence v1 (plan §3.3): tray indicator **deferred to Phase 7** (needs GUI
      dep decision); interim: TTS greeting on daemon start + `/doctor` daemon section + optional
      chime on wake (already shipped in Phase 3). Idle proactive suggestions: OFF by default,
      config-gated, v1 = break reminder after configurable focus time only.

**Stage 4E — Testing:**
- [x] P4.14 Integration tests: spawn daemon in temp `$HOME` → IPC round-trip (status, submit a
      low-risk command via synthetic voice channel, expect spoken+logged result);
      `kill -9` → supervisor restart <5s; 72h soak script (`scripts/soak.sh`) logging uptime.
      > **Amended 2026-08-23:** the soak script "logging uptime" was writing
      > `<timestamp> <json>` into a file called `metrics.jsonl`, so nothing could parse its
      > evidence — and the daemon itself recorded no liveness signal, leaving uptime knowable
      > only through an external poller. The daemon now heartbeats to
      > `~/.helix/metrics/uptime.jsonl` and the script emits real NDJSON, so the soak's verdict
      > is `/blackbox stats` rather than an afternoon with `awk`.
- [x] P4.15 E2E: PTY suite extended with `helix remote` client paths against a test daemon.

**Acceptance criteria:**
- [ ] Daemon survives logout/reboot via service config on all 3 OSes (manual QA checklists).
- [ ] 99.5% uptime over 72h soak (metrics file evidences it). **The soak still needs 72 hours of
      wall clock, but as of 2026-08-23 it can finally be EVALUATED.** Two things were missing.
      The daemon recorded no liveness signal at all, so "uptime" had to be inferred from an
      external poller; it now writes a heartbeat every `metrics.UptimeInterval` (60s), and
      availability is observed-over-expected — the only honest reading of in-band sampling,
      since a dead daemon writes nothing and downtime IS the missing sample. And
      `scripts/soak.sh` wrote its evidence as `<timestamp> <json>`, so **every line of a file
      named `metrics.jsonl` was unparseable**; the criterion said "metrics file evidences it"
      while nothing could read the file. Fixed to real NDJSON with the timestamp inside the
      object. `/blackbox stats` now renders a DAEMON panel: samples vs expected, availability
      against the 99.5% target, restarts (detected by the uptime counter falling — each process
      counts from its own start), and the **longest gap**, because a percentage cannot tell one
      long outage from hundreds of short ones and 99.5% of 72h is 21 minutes either way.
      Verified against a synthetic 72h dataset: 4303/4320 samples = 99.61%, 2 restarts, 13m
      worst gap.
- [x] `"what did I ask five minutes ago"` answered correctly from session store (e2e) —
      **the mechanism is proven 2026-08-23** by `TestE2E_PriorTurnReachesPlannerContext`:
      two real turns through the real binary, asserting the earlier turn reaches the
      planner as context. The harness gained a planner-prompt recorder for this, because
      what Helix TELLS the model is invisible in terminal output, and the ring store's own
      unit tests stop at "the store holds it" — the gap was "and the model is told".
      A second test (`TestE2E_SessionContextIsFencedAsData`) pins that the replayed turn
      lands inside the `<session_history authority="data-only">` fence with its never-obey
      instruction, which P4.4 specified and nothing had verified end to end.
      **Still manual:** whether the ANSWER is correct, since a mock provider returns
      whatever the test told it to — that needs a real model.
- [x] `"undo that"` after a voice-initiated `git commit` performs soft reset with confirmation
      — **proven 2026-08-23, and it never needed hardware.** What existed was `isUndoIntent`
      string matching and a proof that a FAILED commit is not journalled; the criterion is about
      the successful path. `TestUndoThatAfterVoiceCommitSoftResets` builds a real repository in a
      temp dir, commits through the git step (which is what journals the reversal), speaks
      "undo that" on the voice channel, and asserts the confirmation was ASKED, the commit is
      gone from the log, and `feature.txt` survives — a *soft* reset must keep the working tree.
      Three companions cover the ways it should refuse: a declined confirmation leaves history
      untouched, a second "undo that" does not rewind a commit that was never journalled (the
      `Pop()` bug, pinned at acceptance level), and an empty journal asks nothing and changes
      nothing. Hermetic git identity with `GIT_CONFIG_GLOBAL=/dev/null`, because the test that
      used to live in this area once committed to the developer's own repository.
- [ ] Network cut mid-session → local fallback engages within 5s, spoken notice heard.
      **The testable half is now covered (2026-08-23); what remains needs ears.** The ≤5s is the
      poll interval and "heard" needs a speaker, but the part that can regress silently — that a
      transition switches BOTH chains, says so, and writes it down — had no test at all, because
      the logic sat inline in a loop driven by a real TCP probe. Extracted as
      `Daemon.applyConnectivityChange` and covered four ways: offline switches the speech chain
      and journals `connectivity` with a spoken notice; online restores and announces; notices
      fire **per transition, not per poll** (a user on flaky wifi must not hear about it every
      five seconds); and a daemon with no `OnSpeak` wired does not panic, since this runs in a
      supervised goroutine whose whole job is staying up. The offline assertion required
      initializing the speech registry first — `SetOfflineMode` is a silent no-op when
      `speech.Default()` is nil, so the check would otherwise have passed while proving nothing.
- [x] Interactive TUI behavior unchanged (Phase-4A e2e proof still green) — the PTY suite
      has stayed green through every session since, including the 2026-08-23 changes; this
      was met on the day and never ticked.

**Risks:** refactor regressions (mitigated by byte-identical e2e requirement); IPC on Windows
named pipes (spike early); service-install privilege differences across OSes.

---

### Phase 5 — Camera-Based Visual Perception

**Goal:** Opt-in "eyes": capture a frame on demand during an active conversation, attach it to a
multimodal planner request, get vision-grounded answers. Privacy controls are load-bearing.

**Tasks:**
- [x] P5.1 Multimodal message format (backward compatible) in `internal/providers/types.go`:
  ```go
  type MessagePart struct{ Type string; Text string; ImageURL string; ImageData []byte /* base64 at adapter */ }
  // ChatMessage.Content stays for text; new optional Parts []MessagePart
  ```
  Adapter updates: OpenAI content-array format, Anthropic vision blocks, Gemini, Ollama
  (llava/gemma3 tags — this finally consumes `Capabilities.Vision`, `types.go:43`, as a gate:
  refuse gracefully if the active model can't see).
  > **Correction 2026-08-23: there is no Gemini adapter, and there never was.**
  > `internal/providers/` holds anthropic, deepseek, glm, kimi, llamacpp, ollama, openai,
  > openai_compatible, qwen and xai. "gemini" appears only in `catalog.go` as a
  > vision-capable MODEL-NAME pattern, which is why the claim survived unnoticed. Third
  > instance of this pattern in the roadmap — after `TTYSource`/`VoiceSource` (P2.1) and
  > `HybridSource` being unwired (P7.1) — so it is worth stating as a habit rather than an
  > incident: **a task list naming an implementation is not evidence the implementation
  > exists.** The three adapters that DO handle vision (OpenAI content-array, Anthropic
  > blocks, Ollama images) are real and tested.
- [x] P5.2 `internal/vision/capture.go` — `VisionCaptureService`: single-frame grab via
  `ffmpeg` shell-out (platform devices: avfoundation macOS / dshow Windows / v4l2 Linux —
  documented); downscale to ≤1024px JPEG q80; **memory-only** — never written to disk
  (enforced by test).
- [x] P5.3 Opt-in lifecycle: `/eyes on|off` (default OFF); on activation TTS announces it; every
  frame batch journaled (provider + count + timestamp, no pixels); deactivation immediate and
  confirmed vocally. "Turn off your eyes" voice phrase = same as `/eyes off`.
- [x] P5.4 Conversational wiring: in voice mode with eyes on, deictic utterances ("what's wrong
  with **this** code?", "read this serial number") trigger frame capture → attach as image part →
  planner/vision LLM. Non-deictic queries unaffected. One frame per turn (interval polling for
  "activity awareness" is **deferred** — cost/privacy trade-off documented).
  > **SUPERSEDED 2026-08-22 — shipped, then removed.** It worked as specified and
  > the specification was wrong. Demonstratives are far too common to be an
  > intent signal: with eyes on, "what do we have in **this** directory?" was
  > answered by a vision model describing the room, and "show me the commands in
  > **this** helix" got a tutorial for the Helix text editor. Because the
  > heuristic ran BEFORE the planner, the shell was unreachable for any sentence
  > containing "this". Replaced by P5.7 — the planner picking its own tool beats
  > a substring match on English. `isDeictic` and its fuzz target are deleted.
- [x] P5.7 (2026-08-22) `vision` joins the CLOSED planner tool vocabulary — `action="look"`,
  optional `args.prompt`, routed to the same `visionTurn` as `/blackbox look`. This is what
  makes the camera reachable from a sentence without guessing: before it, asked to "turn on the
  camera", the planner's only expressible move was a shell step opening Photo Booth while the
  model said it had no camera access.
- [x] P5.8 (2026-08-22) **Interval capture is no longer deferred** — see Phase 13 (companion
  loop). The cost/privacy trade-off named in P5.4 is answered by an in-process frame diff: an
  unchanged scene never reaches a model at all.
- [x] P5.9b (2026-08-23) The same lesson, one layer down: **ffmpeg's stderr is not a diagnosis.**
      P5.9 negotiated the framerate from ffmpeg's own rejection, which was right. What it did
      not notice is that everything else ffmpeg says lands in the same channel — the version
      banner on every run, and non-fatal warnings it self-corrects from — so the failure
      reporter, which trusted "stderr is non-empty" to mean "stderr is the cause", was wrong
      about every timeout. `-hide_banner`, an 8s capture deadline, and a diagnosis that leads
      with the expired deadline while keeping ffmpeg's words as detail. Measured before and
      after against a genuinely unauthorized camera: 30s of silence then a copyright notice,
      versus 8s and the System Settings path.
- [x] P5.9 (2026-08-22) Capture hardening found by running it on real hardware: the darwin path
  hardcoded `-framerate 1`, which no webcam accepts (a MacBook Air advertises 15 and 30 only), so
  **every capture on that machine had always failed**. Dropping the flag is not a fix either —
  ffmpeg then defaults to 29.97, also refused. The rate is now NEGOTIATED from the modes ffmpeg
  names in its own rejection. `vision.model` also lets frames go to a small fast VLM while chat
  keeps the big model.
- [x] P5.5 Vision LLM routing: `ai.RunVisionModel` uses the configured chat provider if
  vision-capable, else a dedicated vision provider entry in config (`vision.provider`) —
  health-gated like speech (`ProviderVisionCapable` + `RunVisionModelWithProvider`).
- [x] P5.6 Tests: mock multimodal endpoint asserts base64 image part arrived; fs-snapshot test
  proves zero frame persistence during a vision turn; capability-gate test (non-vision model →
  polite refusal); capture unit test skipped cleanly when ffmpeg/device absent.

**Acceptance criteria:**
- [ ] "Hey Helix, what's wrong with this code?" (camera pointed at screen) → spoken, relevant
      diagnosis (manual QA, ≥3 scenarios logged). *Partially met 2026-08-22: a real session got
      correct spoken descriptions through the planner's vision tool. Not signed off — the same
      session exposed P5.4's defect, and the run predates the negotiated-framerate fix.*
- [ ] Frame-to-insight ≤5s (cloud provider, measured metric). *Measured LOCAL and missed:
      `gemma4:e2b` took 31.6s cold, then 19.2s, then 8.8s warm on an M-series Air. Cloud
      unmeasured. `moondream` was evaluated as the fast alternative and rejected — Ollama's build
      returns an empty string for instruction-style prompts and coordinate arrays otherwise.*
- [x] Filesystem snapshot test: no image bytes on disk during/after vision turns.
- [x] `/blackbox eyes off` + voice phrase both deactivate instantly; journal shows every frame
      event. (Phrase now suffix-matched, so a sentence works.)
- [ ] **Blocked, not failed** — re-tested 2026-08-23 and the blocker still holds: ffmpeg
      enumerates the MacBook Air Camera fine and then delivers nothing, so a raw
      `ffmpeg -frames:v 1` had to be killed. macOS camera authorization needs a human in
      System Settings; it cannot be granted from here.
      > **What the re-test DID find is that the claim in this line was false.** "Now says so
      > explicitly instead of looking like a hang" was measured and it did neither. A real
      > capture against the denied camera took **30 seconds** and then reported
      > `signal: killed: ffmpeg version 9.0.1 Copyright (c) 2000-2026…` — the ffmpeg version
      > banner. `describeCaptureFailure` returns stderr whenever stderr is non-empty, and
      > ffmpeg writes its banner there on every single run, so the carefully-worded macOS
      > guidance underneath was **unreachable dead code from the day it was written**.
      >
      > Fixed in three steps, each measured: `-hide_banner` on every platform's input args
      > (the root cause); a capture-specific `CaptureDeadline` of 8s, because both call sites
      > pass 30s — right for a turn, absurd for a device open that either answers in under a
      > second or never — and 30 seconds of silence IS the hang this line claimed was gone;
      > and, after the banner fix revealed a *second* masking layer (a non-fatal
      > `Selected pixel format (yuv420p) is not supported` warning ffmpeg self-corrects from),
      > a diagnosis that leads with the expired deadline and carries ffmpeg's words as detail
      > rather than choosing between them. The user now waits 8s and reads: *"no frame before
      > the deadline — macOS is most likely withholding camera access; grant it to your
      > terminal in System Settings → Privacy & Security → Camera, then restart the terminal."*
      >
      > Also fixed the readiness overstatement this exposed: `/blackbox status` reported
      > **watching** on this machine, because ffmpeg-on-PATH and a vision-capable model are
      > both necessary and neither is sufficient — an unauthorized camera passes both. The
      > capture service now remembers outcomes, and a camera that has failed every attempt and
      > never succeeded reads **no frames** with the likely cause. "Never attempted" is
      > deliberately not a failure: a fresh session has not looked yet.

**Risks:** ffmpeg device-flag quirks per OS (spike on all 3 early); model availability locally
(llava quality is modest — set expectations in docs).

---

### Phase 6 — Ambient Noise Detection

**Goal:** Basic auditory awareness: loud-noise/alarm/music/multi-voice/silence events with
configurable contextual responses. Ships only if Phases 1–5 are stable; **runs only in full voice
mode and opt-in**.

**Tasks:**
- [x] P6.1 `internal/ambient/` — `AudioMonitorService` sharing the wake-loop capture stream:
      v1 = pure-Go analysis (RMS energy spike detection, silence tracking, crude spectral
      centroid via hand-rolled FFT or `go-dsp` if license-clean) → categories: `loud_noise`,
      `alarm_like` (sustained narrow band), `music_like`, `speech_multi` (deferred — hard), `silence`.
      **YAMNet/classifier models explicitly deferred** — document as future sidecar.
- [x] P6.2 Config: per-category enable, sensitivity, response mode `vocal|log|ignore`; cooldowns
      per category (no loops of "are you okay?").
- [x] P6.3 Contextual responses (from original plan §5.2): loud noise → "Are you okay?" (once,
      cooldown 10min); alarm → offer to check; music → TTS volume ducking (oto gain) instead of
      chatter; prolonged silence after a question → offer repeat.
      > **Audit 2026-08-23: three of four shipped; the fourth is a SPEC DEFECT, not a missing
      > task.** Loud noise ("Are you okay?", 10-minute cooldown), alarm ("sounds like an alarm.
      > Want me to check?", 5-minute) and silence ("I lost the sound of your voice. Want me to
      > repeat?", 2-minute) are all implemented in `defaultResponse`/`defaultCooldown` exactly as
      > written. **Music ducking is not implemented, and as specified it cannot be**: ducking
      > means attenuating one signal when another is present, and Helix does not control the
      > music — only its own TTS. Lowering its own voice while music plays makes it *less*
      > audible, which is the opposite of the intent; raising it is not expressible either, since
      > `audio.PlaySpeech` defines volume as 0..1 and treats anything outside that as 1, so there
      > is no amplification path and no clipping guard to add one safely.
      >
      > The valuable half of the line — "instead of chatter" — IS delivered: music_like maps to
      > no spoken message, so Helix stays quiet about the music rather than remarking on it.
      > Left unimplemented deliberately rather than invented: what a user would actually want
      > here is a product decision (speak louder over music? pause replies until it stops?
      > nothing at all?), and guessing one into the audio layer is worse than saying it is open.
- [x] P6.4 Tests: WAV fixture corpus per category, golden classification tests; CPU budget test
      (<5% idle overhead on dev laptop, logged).
      > **The CPU budget test did not exist until 2026-08-23 — only a benchmark did**, and its
      > own comment said it was there "so the CPU budget can be evidenced": it enabled the check
      > and never performed it. Nothing computed a percentage, so the <5% claim had never been
      > tested. It also measured the wrong thing. `BenchmarkAnalyzer` analyzes a 1024-sample
      > window (64ms of audio); production calls `ChunkMonitor.Observe` on a whole wake-loop
      > chunk — 1500ms, 24000 samples — which additionally decodes a WAV and pads the FFT up to
      > **32768 points**. Measured side by side: **25µs for the old benchmark vs 623µs for the
      > real path, 25× more.** The "26µs/chunk" figure this phase has been quoting is the
      > 1024-window number presented as a chunk cost.
      >
      > **Measured properly: 571µs per 1500ms chunk = 0.0381% duty cycle** — the budget holds
      > with a ~130× margin, now asserted by `TestAmbientCPUBudget` rather than left to
      > arithmetic, plus a companion pinning that a 500ms chunk stays inside its own span.
      >
      > One thing the honest measurement surfaced that the benchmark hid: **852 KB allocated per
      > chunk** (4 allocs — the padded FFT's two float64 arrays, the magnitude slice, the decoded
      > samples), every 1.5s for as long as live mode runs. That is ~570 KB/s of garbage.
      > Deliberately NOT optimized: at 0.038% CPU and that rate it is negligible even on a Pi,
      > and threading a reusable scratch buffer through a value-type `Analyzer` shared with the
      > tee'd wake stream would add concurrency risk to fix a problem nothing is observing.
      > Recorded so a future chunk-size change is made with the cost visible — the FFT padding
      > makes this superlinear, so doubling the chunk to 3s crosses into a 65536-point transform.

**Acceptance criteria:**
- [x] ≥90% accuracy on the configured fixture categories. **Measured 2026-08-23:
      100% (57/57)** — alarm_like 27/27, loud_noise 12/12, music_like 10/10,
      silence 8/8 (`TestAnalyzerCorpusAccuracy`, which logs the number and asserts
      the target both overall and per category, since an aggregate can hide one
      category failing entirely). The corpus spans the analyzer's real operating
      range — eight tone frequencies × three amplitudes, two-tone sirens, chords of
      7–12 tones, four noise amplitudes × three seeds, and dither below the silence
      floor — plus a band sweep proving the quiet-but-audible gap never fires.
      **Honest scope:** this measures the RULES against the acoustic shapes they
      were written for, not real-world audio. Real alarms and real rooms need the
      trained classifier P6.1 defers to a future sidecar, and the test says so.
- [x] No response spam (cooldowns proven by unit test) — `TestServiceCooldownSuppresses`
      (fires, suppressed within the window, fires again after it, on a controllable
      clock). This had been proven since Phase 6 shipped and was simply never ticked.
- [x] Disabled by default; enabling documented in one line of config — `docs/blackbox.md` §7.

---

### Phase 7 — Hybrid Mode, Polish, Hardening & Release

**Goal:** Ship-quality: hybrid input, performance, full e2e matrix, docs, tagged branch release.

**Tasks:**
- [x] P7.1 Hybrid mode: `input.HybridSource` (TTY + voice simultaneously — the Phase-2 interface
      makes this cheap); per-turn channel tagging already flows through policy.
      > **Correction 2026-08-23 — built, tested, and NOT reachable.** `HybridSource` exists with
      > unit tests and nothing outside `internal/input` consumes it: no code in `cmd/` constructs
      > one, so no user can enter hybrid mode. Per-turn channel tagging is genuinely done and
      > load-bearing (that half is true), but "hybrid mode" as a user-facing capability is not
      > delivered, and this box should not have been ticked for it. The remaining work is not
      > plumbing: the REPL's line reader is a blocking raw-mode read, so hybrid means racing it
      > against a voice capture and cancelling the loser — a change to the interactive loop that
      > the "TUI behavior unchanged" guardrail deliberately makes expensive. Left as an owner
      > decision rather than attempted in passing.
- [x] P7.2 Benchmark suite: `go test -bench` for speech hot paths (WAV mono decode, STT
      registry chain) and the ambient analyzer hot path; results logged in dev log.
- [x] P7.2b Performance (code): streaming STT partial transcripts displayed live (Deepgram
      WebSocket `StreamingSTTProvider` + chunked voice turn + `stt.stream_chunk_ms`), TTS
      first-byte latency budget (`tts.first_byte_ms`) + last-synthesis latency metric in
      /voice-status.
- [x] P7.2c **Streaming TTS playback.** Owner QA measured 2,280 ms TTS latency against the 800 ms
      budget — the adapter buffered the ENTIRE synthesis before a single sample reached the
      speaker, so that was dead air. Now: `StreamingTTSProvider` (optional interface, mirroring
      `StreamingSTTProvider`), `openaiTTS.SynthesizeStream` requesting **raw PCM instead of WAV**
      (a container would mean parsing a header out of bytes that have not arrived; PCM's rate is
      fixed by the API contract, so it plays from byte one), `Registry.SynthesizeStream` walking
      the same failover chain, and `audio.PlaySpeechStream` playing incrementally.
      **Design constraints:**
      • *The speaker callback must never block* — beep pulls samples on the audio goroutine, so a
        producer goroutine fills a queue and `Stream()` only takes what is already there.
      • *Underruns are worse than latency* — a 150 ms preroll (the entire latency cost) absorbs
        ordinary jitter, and a mid-stream stall emits silence rather than ending, so a hiccup
        sounds like a pause instead of a truncated sentence.
      • *First sentence streamed, rest pipelined* — time-to-first-audio is set by sentence 1 alone
        (nothing is playing to hide it behind); sentences 2..N keep the ADR-014 buffered pipeline
        because their latency is already hidden.
      • *Never a new way to be silent* — any failure before audio plays falls back to the buffered
        path, so this can only be faster. `/say` and daemon `remote say` route through it too.
      Expected first-audio ~150 ms + network vs ~2,280 ms measured. **Real-hardware confirmation of
      the number is still owner QA.** Tests: 9 streamer (underrun-does-not-block, EOF, cancel,
      early-failure surfacing, preroll budget) + 6 adapter/registry (PCM requested, WAV unchanged,
      no-streaming-provider signal).
- [x] P7.3 Presence polish (plan §3.3 remainder): system-tray indicator — decide dependency
      (pure-Go tray libs are immature; options: `fyne.io/systray` CGO, or a tiny separate
      helper binary) — **decision required at phase start**, record as ADR-010; HUD overlay
      out of scope (documented).
- [x] P7.4 E2E matrix completion: PTY + mock STT/TTS/vision endpoints covering voice happy path,
      failover, policy denials, daemon remote flows, mode switches, eyes on/off; CI e2e runs on
      all 3 OSes — daemon-remote IPC test is cross-platform (Unix UDS / Windows loopback TCP +
      token), PTY tests skip gracefully via `//go:build !windows`.
- [x] P7.5 Fuzzing: transcript→policy parser, NDJSON IPC messages, WAV/MP3 headers (extend
      existing fuzz conventions). **Extended 2026-08-23** with `FuzzRedact`
      (`internal/journal`) — the voice log's sanitizer sits between a microphone's
      output and a file the user will later `cat`, and its truncation has a
      rune-boundary rule whose failure mode is silent (invalid UTF-8 → unparseable
      JSON line → the entry vanishes on read-back). 5.6M execs clean; listed in both
      Makefile fuzz targets.
- [x] P7.6 Docs: `docs/blackbox.md` (user guide: setup wizard, sidecars, daemon install, privacy
      controls), README BlackBox section, `docs/architecture.md` + `docs/threat_model.md` updated;
      this file's §13 finalized for the release.
      > **Full sweep 2026-08-23 — all twelve `.md` files audited, not just the ones each session
      > happened to touch.** Four had gone stale in ways worth naming. **`SECURITY.md` did not
      > mention the voice channel at all** — it enumerated six safety layers and omitted an entire
      > input channel's controls, which for a security policy is the most consequential of these
      > four; it gains a Voice Channel Controls section and a Camera/Transcript Privacy section.
      > **`threat_model.md`'s control #1 listed only `<retrieved_data>`** as the untrusted block,
      > when four others now ride the same fence (session history, todo, project context, the
      > agentic execution report) — and it never said that a transcript is *not* data-only, which
      > is the whole reason the voice model exists separately. **`harness.md`'s central flow
      > diagram** still showed the high-confidence-shell branch with no channel distinction, stale
      > since voice stopped taking it. **`edge_deployment.md`** had no measured cost for the
      > always-on parts, which is the first question an edge deployment asks; it now carries the
      > wake (0.0014%) and ambient (0.038%) duty cycles, the local sidecar numbers, and a Linux
      > camera note. **`RELEASE_NOTES.md` updated 2026-08-23 on owner instruction** (they will tag
      > `v1.5.0` manually): a full v1.5.0 section covering voice, vision, the harness, the daemon,
      > providers, edge, the new security controls, the measured numbers from §10A, and — in the
      > house style — a **Known limits** list naming hybrid mode's unreachability, music ducking,
      > the unmeasured cloud path, onset-vs-keyword detection, the unrun soak, macOS camera
      > permission and parked full-duplex barge-in. Writing it caught one overclaim in my own
      > draft (native tool calling across "ten providers" — it is **eight**, since `custom` and
      > llama.cpp are deliberately excluded as undetectable) and one real defect: **`HelixVersion`
      > was still `1.0.0`.** goreleaser injects the tag via ldflags so official binaries are
      > right, but `make current` and `go install` fall back to the constant, so a source build at
      > the v1.5.0 tag would have reported 1.0.0. Bumped, with a comment saying why it must track
      > the tag.
- [x] P7.7 Supply chain: goreleaser build is CGO-free (`CGO_ENABLED=0`, verified) + CI build
      step enforces it; Ollama Linux installer downloads then verifies a pinned SHA-256 before
      running (env `HELIX_OLLAMA_INSTALL_SHA256` override); whisper/Piper/wakeword sidecars are
      user-managed (no installer to pin — documented); SBOM/cosign (syft/sigstore) as-is.
- [x] P7.8 Metrics collection run against §10 table; gaps documented honestly. **Run 2026-08-23
      — see §10A for the results.** Three rows were newly measured (local STT accuracy, wake
      detection rate, and — from the Phase 6 pass — noise classification), three were already
      measured, and five remain unmeasurable on this machine for reasons recorded per row rather
      than as a blanket "manual QA". Two of those five need a microphone, one needs camera
      permission, one needs a paid API key, and one needs a human ear; none needs code.
- [ ] P7.9 Tag `blackbox-v0.1.0` on the branch. **No merge to `main` without explicit owner
      approval** (ADR-009).

**Acceptance criteria:** all §10 targets measured and logged; full suite green 3-OS; docs complete;
release tagged.

> **Status 2026-08-23.** "All §10 targets measured" is not achievable on one developer machine and
> §10A now says exactly why, row by row: six of eleven need a microphone, a paid key, a human ear
> or 72 hours, and none needs code. Read the criterion as *every row accounted for with a number
> or a named blocker*, which is now true. Full suite green on this machine and in CI's 3-OS
> matrix; docs complete. Only the tag is outstanding, and it is owner-gated by ADR-009.

---

---

## §6B. Living-AI Roadmap — Phases 8–12 (added 2026-08-17)

> These phases turn Helix from "voice-capable" into a **true living AI** (JARVIS archetype):
> cheap always-on speech, a genuine agentic harness, a Raspberry-Pi appliance build, offline
> resilience, and sci-fi presence. Phases 8, 11, and 12 landed their core code on 2026-08-17;
> Phases 9–10 are hardware-gated. Same guardrails (§12), same "every phase ends green" rule.

---

### Phase 8 — Genuine Agentic Harness

**Goal:** Close the single-shot planner into a bounded plan→act→observe→replan loop so Helix
recovers from failures and chains follow-ups (ADR-013).

**Tasks:**
- [x] P8.1 Refactor the inline step loop into `executePlanSteps` returning a `StepObservation`
      trace (index, tool, command/action, ok, err) — behavior-identical for the single-shot path.
- [x] P8.2 Extract `planFirewallExecute` (planner → firewall 1/2/3 → prepareSafePlan → execute)
      so the harness re-runs the FULL safety pipeline per iteration with no duplication.
- [x] P8.3 `internal/agent/harness.go` — `agenticFollowUp`: replans on a failed step, stops on a
      fully-successful plan, bounded by `MaxAgenticSteps` (default 4). Observation trace fed back
      as an `authority="data-only"` fenced block, sanitized against fence-breakout.
- [x] P8.4 `/agentic on|off|status` toggle + persisted `user_preferences.agentic_mode`; wired in
      `main.go` and `/help`.
- [x] P8.5 Tests: completion detection, data-only fencing + injection sanitization, budget cap.
- [x] P8.6 **Output capture:** `commands.TailBuffer`/`OutputCapture` (bounded, last-N-bytes,
      concurrency-safe) + `DirectorySandbox.RunShellCommandCaptured` tee stdout/stderr to the
      terminal *and* into a per-step tail; `StepObservation` gains `Output`, `OutputTruncated`,
      `ExitCode`; `observationBlock` renders a sanitized tail per step (larger budget for the
      failing step, where the diagnosis is). **Capture is gated on agentic mode** — assigning a
      non-`*os.File` to `cmd.Stdout` makes os/exec insert a pipe, so the child loses its TTY and
      stops emitting colors/progress; the default path keeps inherited descriptors byte for byte.
      **Exit-code capture was required for the feature to work at all** — see the ADR-013
      amendment: lenient execution meant a failing build reported OK and the harness never looped.
      Tests: 10 capture (incl. tee-does-not-swallow, stderr, race), 9 sanitizer/observation,
      5 integration (agentic-on vs off, failure path), `FuzzSanitizeOutput` (945k execs; found a
      real sanitizer-ordering bypass, fixed, seed committed).
- [x] P8.7 **Provider-native tool calling:** `providers.ToolDefinition`/`ToolCall`,
      `ChatRequest.Tools`/`ToolChoice`, `StreamChunk.ToolCalls`, `CollectChatResult`, and a
      `toolCallAccumulator` that reassembles fragmented SSE tool-call deltas (id+name arrive once,
      argument slices accumulate, ordered by provider index — consumers never see partial JSON).
      Implemented in the `openai_compatible` adapter, which **covers 7 providers at once** (openai,
      deepseek, kimi, qwen, glm, custom, llamacpp all embed it). `ai/planner_tools.go` offers the
      plan as an `emit_plan` tool with the schema mirrored from `BuildPlannerPrompt` and
      `tool_choice=required`, so the model cannot answer in prose — the failure the 3-attempt
      JSON-repair ladder exists to absorb. `Capabilities.ToolUse` is now real, via
      `providers.SupportsToolUse`.
      **Design constraints honored:**
      • *Fast path, never a new failure mode* — unsupported provider, transport error, no call,
        wrong tool, or empty arguments all fall through to the untouched prompt ladder.
      • *Honest capability reporting* — the flag describes the ADAPTER's ability, not the vendor's:
        Anthropic and Ollama have their own wire formats and report false rather than cost a wasted
        round trip per plan. `custom` and `llamacpp` are excluded because their tool support is
        undetectable (arbitrary endpoint; llama-server needs `--jinja` + a capable GGUF).
      • *Same safety path* (§12 #3) — tool arguments go through the identical
        `ParsePlanFromModelOutput` → `validatePlan` → firewall → risk tiers → sandbox chain. The
        schema's closed enums are defense in depth, not a replacement for validation.
      • *Composes with P11.2* — `ToolCallingAvailable` asks the provider the BREAKER would pick, so
        a session degraded to a local model correctly stops using tool calling.
      Surfaced in `/provider status` ("Planner protocol"). Tests: 6 accumulator/capability,
      6 adapter wire (incl. ordinary requests staying byte-identical), 8 planner (schema contract,
      shared validation, all four fallback modes).
- [x] P8.7b **Anthropic + Ollama native tool calling.** Shared plumbing extracted to
      `internal/providers/tools.go` (exported `ToolCallAccumulator`, `ToolsToOpenAIWire`,
      `ToolsToAnthropicWire`, `AnthropicToolChoice`) so three wires share one reassembly
      implementation instead of re-deriving — and re-bugging — it per adapter.
      **Anthropic:** flat `tools` with `input_schema` (not OpenAI's nested `function.parameters`),
      `tool_choice` as an object where "must call one" is `{"type":"any"}`; streamed as
      `content_block_start{type:"tool_use"}` + `input_json_delta` fragments keyed by block index —
      different event names, same index-keyed accumulation, so the shared accumulator applies. The
      provider gained an `endpoint` field so the wire is testable against a stub.
      **Ollama:** OpenAI-shaped definitions on `/api/chat`, **no `tool_choice`** — a call cannot be
      forced, only offered, so the field is honestly omitted rather than faked (the planner already
      falls back when no call returns). Its arguments arrive as a JSON **object**, not the string
      every other provider sends; `json.RawMessage` normalizes them without a re-encode round trip.
      **Ollama support is per-MODEL, not per-provider** — `ollamaToolModels` gates on families that
      ship a tool template (llama3.x, qwen2.5/3, mistral-nemo/small/large, command-r, firefunction,
      hermes3). This is load-bearing honesty: Helix's own default local model is `gemma4:e2b`, and
      Gemma has no tool template, so a blanket claim would make the planner attempt a call, get
      prose, and fall through on EVERY plan — burning a wasted round trip on exactly the
      low-powered hardware that can least afford one.
      Tests: 5 Anthropic (wire shape incl. "no OpenAI envelope", streamed reassembly, mixed
      text+tool, truncated stream), 5 Ollama (wire shape incl. "no fabricated tool_choice",
      object→string normalization with a round-trip check, delivered-once, nameless dropped), plus
      updated capability expectations.
- [x] P8.8 **Streaming token render:** `ai.StreamModel` consumes the provider channel directly and
      delivers fragments to a callback while still returning the complete text (script promotion
      and session memory need the whole response — streaming changes when bytes are *displayed*,
      not what the caller holds). `ux.AIStreamWriter` renders live; `agent.AIStream` +
      `agent.StreamingRenderer` are the seam.
      **Design decisions:**
      • *Streaming replaces the typewriter rather than composing with it.* `Typewriter` simulates
        live generation with fixed per-character sleeps; once real tokens arrive that simulation is
        strictly worse — it would add artificial delay on top of genuine latency. The audible tick
        (one per chunk ≈ per token, `PlayType` is already 10 ms-throttled) and the `[NEURAL_NET]`
        prefix keep the established character.
      • *The spinner now stops at the FIRST token*, not at the end — time-to-first-word becomes the
        provider's real latency instead of the whole generation. That is the actual win.
      • *`StreamingRenderer` is an OPTIONAL interface, not part of `Renderer`.* The daemon captures
        its IPC reply by embedding `HeadlessRenderer` and overriding `PrintAIMessage` — an override
        Go does **not** dispatch from inside a `HeadlessRenderer` method. Making streaming opt-in
        means headless paths keep the buffered render byte-for-byte and cannot silently start
        returning empty replies. Two tests pin this (`TestHeadlessRendererDoesNotStream`,
        `TestDaemonRendererDoesNotStream`).
      • *The prefix is deferred to first content*, so a response that produces nothing leaves no
        orphaned `[NEURAL_NET] →` on screen; an unstarted stream falls back to `PrintAIMessage`.
      **Behavior change (deliberate):** the three duplicated chat-fallback blocks became one
      `Agent.chatFallback`, and a fallback response containing fenced scripts now shows its prose
      *before* the run-it prompt. Previously the model's explanation was discarded in that case —
      seeing the reasoning behind a script you are approving is better for an execution decision.
      Tests: 4 stream writer, 7 `StreamModel` (incl. partial-text-on-error and breaker feeding),
      5 renderer-seam/daemon guards.

**Acceptance:** a voice-initiated multi-step task that hits a recoverable error self-corrects
within the step budget (manual QA with a real model); single-shot behavior unchanged with
`/agentic off` (existing suite proves it).

---

### Phase 9 — Cheapest-Good Speech Defaults & Local-First Chains

**Goal:** Make always-on voice affordable and resilient (ADR-011/012).

**Tasks:**
- [x] P9.1 Groq STT adapter (`groq`, OpenAI-compatible audio API, `whisper-large-v3-turbo`).
- [x] P9.2 Deepgram Aura-2 TTS adapter (`deepgram` TTS, linear16 WAV, ~300 ms first byte).
- [x] P9.3 Kokoro local TTS adapter (`kokoro-local`, OpenAI-compatible Kokoro-FastAPI sidecar).
- [x] P9.4 `pricing.json` refresh: Groq turbo + gpt-4o-mini-tts recommended; Nova-3, Aura-2,
      Kokoro added; wizard persists the chosen model (`APIModel()` — local entries send no model).
- [x] P9.5 Keystore `GROQ_API_KEY` mapping; registry registers all new adapters.
- [x] P9.6 Adapter contract tests (Groq STT, Aura-2 TTS, Kokoro local flags).
- [x] P9.7 **Recommended chain presets — done 2026-08-23.** `/blackbox setup` opens with
      three pre-worked answers (`cmd/helix/voice_presets.go`): "Cheapest cloud"
      (Groq turbo + gpt-4o-mini-tts), "Lowest latency" (Deepgram Nova-3 + Aura-2),
      "Fully local / private" (whisper-local + **piper-local**, not Kokoro — the sketch
      said "Kokoro/Piper" but Kokoro is the one component that needs Docker, and a preset
      named *private* must not walk the user into a container runtime, ADR-002 amendment).
      **A preset is a pre-filled ANSWER, not a second code path:** it feeds the same
      key-request/verify, sidecar port-assign/probe and chain-verify steps the manual path
      uses, because a path that skipped them would produce a chain that looks configured
      and cannot serve a request. Two deliberate asymmetries: every cloud preset carries a
      LOCAL fallback (the failure worth surviving is the network, and a second cloud vendor
      does not), and the local preset carries NO fallback (a cloud one would quietly undo
      the reason someone chose private). Presets are pinned to `pricing.json` by test, so a
      catalog rename cannot leave one pointing at a model no provider serves, and
      `availablePresets` drops a chain this build cannot select rather than offering it.
- [ ] P9.8 Manual QA: real Groq key round trip; real Kokoro-FastAPI sidecar.

**Acceptance:** `/voice-setup` shows Groq/gpt-4o-mini-tts as recommended with honest $/mo estimates;
failover chain includes a local sidecar by default so a dropped network keeps voice alive.

---

### Phase 10 — Linux Edge-Device Deployment

**Goal:** Make Helix a first-class citizen on Linux edge devices — Raspberry Pi (4/5), NVIDIA
Jetson, generic arm64/amd64 mini-PCs, and other SBCs — with an honest, per-board capability matrix
rather than a single Pi-only target (ADR-012). Cloud voice path everywhere; fully-local where the
board can host the sidecars.

**Design principle (verified):** the core is CGO-free Go and cross-compiles to `arm64`/`amd64`/
`armv7`/`riscv64` (a static binary that ignores the device's glibc — the key to the Jetson Nano's
frozen Ubuntu 18.04). Only two things are device-specific beyond arch: **`audio_cgo` + libasound**
for on-device speaker output, and **bubblewrap** to preserve kernel confinement where Landlock
(kernel ≥ 5.13) is unavailable.

**Tasks:**
- [x] P10.1 `docs/edge_deployment.md` — the deployment matrix: build flags per arch, the two Linux
      gotchas (audio_cgo, confinement fallback), cloud/hybrid/local path guidance, and per-device
      notes for Pi 5, Pi 4, **Jetson Nano (1st-gen)**, amd64 mini-PC, generic arm64 SBC, and RISC-V.
- [x] P10.2 `scripts/edge-setup.sh` — detects arch/board/kernel/package manager, installs
      sox + bubblewrap through the system package manager, and offers Ollama behind a
      **SHA-256-verified** install that **fails closed** on mismatch or an unverifiable digest.
      **Refuses Ollama on the Jetson Nano 1st-gen** and points at the cloud voice path (Groq +
      gpt-4o-mini-tts) plus the llama.cpp escape hatch from P11.4. Modes: `--check` (detection
      only, inert), `--dry-run` (prints the plan), `--yes`, `--assume-board=` (exercises a board
      path without that board). Prompts are TTY-gated and EOF-tolerant, so a piped/CI run declines
      instead of hanging or aborting under `set -e`.
      **Deviation from the sketch, deliberate:** whisper.cpp / Piper / Kokoro / openWakeWord are
      **not** auto-installed. They have no stable, per-arch, checksummable release artifact
      (whisper.cpp builds from source, Kokoro ships as Docker), so a pinned installer would be
      security theater that rots — the same conclusion P7.7 reached. They stay user-managed
      sidecars (ADR-002) and the script prints exact, copy-pasteable setup for each.
      Tests: 9, incl. a **pin-drift guard** asserting the script's Ollama checksum still matches
      `internal/ollama/installer.go` (a drift means one install path trusts what the other
      rejects), a no-`curl|sh` assertion, consent-gating assertions, and `--dry-run` runs of both
      the Jetson refusal and the Pi 5 non-refusal. `shellcheck` runs in CI when present.
- [x] P10.3 `/doctor` "edge appliance" section, backed by the new `internal/edge` package:
      platform + arch + detected board, **build flavor** (`audio.BackendName`/`SpeechSupported` —
      new, exposing the `audio_cgo` build tag at runtime), **confinement actually in force** with
      the bubblewrap remediation attached when it degraded, recorder presence, each configured
      **local** sidecar's reachability, the offline-LLM fallback's reachability **and whether its
      model is pulled** (P11.3's check, surfaced interactively), and thermals with a throttling
      verdict.
      **Rationale:** both Linux edge gotchas fail *silently* — a CGO-free binary is structurally
      mute however TTS is configured, and confinement degrades to none on an old kernel without
      stopping anything. On a headless board that stays invisible until something important does
      not happen. Tests: 10, using synthetic sysfs fixtures so board/thermal/throttle parsing is
      covered on a dev machine, not only on the boards it targets.
- [x] P10.4 `systemd --user` unit template for headless boards — `internal/edge/systemd.go`
      (`SystemdUnit`, `LingerEnabled`, `SystemdEdgeNotes`), consumed by `helix daemon install`.
      **Two load-bearing corrections, not cosmetics:**
      • `Wants=network-online.target` added alongside `After=`. `After` only *orders* against a
        target; it does not pull it in, and nothing else requests network-online on a minimal
        headless image — so the previous unit's ordering was **silently inert** and the daemon
        raced the network while its first acts are a connectivity probe and a cloud STT/TTS call.
      • **Lingering guidance.** A `--user` service stops at logout and never starts at boot unless
        `loginctl enable-linger` is set. On an appliance nobody logs into that is the difference
        between "installed" and "actually runs". `helix daemon install` now *detects* the linger
        state (reading systemd's marker directly, so no `loginctl` dependency) and warns in yellow
        with the exact fix when it is off — while honestly reporting "unknown" when it cannot tell.
      Also added: `StartLimitIntervalSec`/`StartLimitBurst` to bound restart storms on a small
      board (placed in `[Unit]`, where systemd ≥ 230 expects them — under `[Service]` modern
      systemd logs "Unknown lvalue" and ignores them), `TimeoutStopSec`, `WorkingDirectory=%h`,
      `After=sound.target`, and commented `Environment=` examples for the three edge knobs. Post-
      install notes cover the `audio` group, the silent-build gotcha, `journalctl --user`, and
      `/doctor`. Tests: 10, incl. percent-escaping (`%h` must survive, a literal `%` must double
      or the unit fails to load), a section-placement check, and a well-formed-lines check.
- [ ] P10.5 Manual QA on real hardware:
      - Pi 5 fully-local: wake→first-audio ≤ ~2.5 s, sustained CPU/thermal check.
      - **Jetson Nano (1st-gen), cloud path:** wake→first-audio over Groq + gpt-4o-mini-tts;
        confirm `audio_cgo` speaker build and bwrap-confinement fallback.
      - amd64 mini-PC fully-local: reference "strong local box" numbers for §10.

**Acceptance:** each documented board reaches hands-free conversation from its matrix row; the
Jetson Nano (1st-gen) converses smoothly on the cloud path; the Pi 5 / amd64 boxes converse offline
end-to-end.

---

### Phase 11 — Offline LLM Resilience

**Goal:** Helix keeps thinking when the cloud is gone (not just keeps hearing/speaking).

**Tasks:**
- [x] P11.1 **Daemon provider init (was a silent-failure bug):** `daemon.New` now calls
      `ai.InitProviders` from persisted config — previously every daemon planner/chat call failed
      "no provider configured" and IPC submits returned an empty reply.
- [x] P11.2 **Automatic LLM failover:** `internal/ai/failover.go` — a circuit breaker (CLOSED /
      OPEN / HALF-OPEN, ADR-016) resolves the provider for every model call. Two triggers:
      repeated availability failures at the call site (works in the interactive shell, which has
      no monitor), and `ai.SetOfflineMode` from the daemon's 5 s connectivity monitor, called
      alongside the existing `speech.SetOfflineMode` so ears, voice, and brain move together.
      Switches are spoken *and* printed; the daemon also journals them (`llm_failover`).
      Restore happens on connectivity return or via the half-open cloud probe.
      **Design notes:** only availability errors count (`isAvailabilityError` — a 400/401/Ctrl+C
      stays visible); the local provider is health-checked *before* every switch, so a machine
      with no local runtime never degrades onto a dead brain; `UseProvider`/`UseModel` clear
      breaker state so an explicit user choice is never silently undone; planner retry timeouts
      are now computed per attempt (`plannerTimeout`) because the breaker can flip cloud→local
      *between* the three planner attempts, and a CPU-bound local model handed a 30 s cloud budget
      would time out on the very attempt meant to rescue the turn.
- [x] P11.3 **Ensure-local-ready:** `daemon.ensureLocalBrainReady` (background, guarded) runs
      `ollama.EnsureRunning`, lists installed models, and matches bare names against `name:tag`.
      **Deviation from the sketch (consent, §12 #1):** the model *pull* is a multi-gigabyte
      download, so it happens only when `llm.fallback.ensure_ready` is explicitly true. By default
      the daemon still **verifies and journals** a loud warning — the useful half of the check
      without a surprise download. No-op for `llamacpp` (a user-managed sidecar has no pull API).
- [x] P11.4 llama.cpp — **decision: implement** (ADR-016). `internal/providers/llamacpp` over
      `llama-server`'s OpenAI-compatible API, registered unconditionally (keyless, costs nothing
      until used), `HELIX_LLAMACPP_URL` / `llm.llamacpp_url` override, bare-host URLs normalized
      to `/v1`. Rationale: the Jetson Nano 1st-gen row of the edge matrix has no Ollama, so
      deleting the provider would have left that board with no offline brain.
- [ ] P11.5 **Manual QA (hardware/keys):** cut the network mid-conversation against a real cloud
      key + a real Ollama, confirm the ~5 s spoken switch and the half-open restore; run
      `llama-server` and confirm a full degraded planner turn on the llamacpp provider.

**Acceptance:** cloud cut mid-conversation → within ~5 s Helix answers from the local model with a
spoken "switching to local intelligence" notice; restore switches back.
*(Unit-proven end to end — 9 breaker tests incl. threshold, non-availability errors, dead-local
refusal, half-open restore, user-override precedence. The ~5 s wall-clock and the spoken audio
itself remain manual QA, P11.5.)*

---

### Phase 12 — Sci-Fi Presence & Voice UX Polish

**Goal:** The "living AI" feel — instant speech, reactive visuals, resilient hands-free loop.

**Tasks:**
- [x] P12.1 `internal/ux/voiceviz.go` — voice HUD (listening waveform / decode sweep / speaking
      wave / wake-standby breathing pulse), Thinker-pattern, TTY-gated (ADR-015). Wired into the
      batch voice turn and the wake-standby window.
- [x] P12.2 `speech.SpeakStream` — sentence-pipelined TTS (one-ahead synthesis) for fast
      first-audio (ADR-014); wired into both the interactive and daemon `OnSpeak` seams.
- [x] P12.3 Voice-loop smoothness/bug fixes (from the 2026-08-17 audit): persistent gapless
      `StreamRecorder` (no per-chunk process-spawn gaps), phantom-wake guard (closed-channel
      check), silent-capture → `ErrNoSpeech` (no 80 s dead-air hang), wake-scanner retry (survives
      quiet rooms), `sox`-vs-`rec` binary fix, VoicePrompter now prints questions (visible when TTS
      is down), Deepgram `speech_final` endpointing (no mid-utterance truncation).
- [x] P12.4 **Live amplitude feed:** `speech.ClipLevel` meters each captured chunk and drives
      `VoiceViz.SetLevel` in the streaming voice turn, so the waveform tracks the real microphone.
      **The mapping is logarithmic (dBFS), not linear** — that is the whole point. Speech RMS sits
      around 0.01–0.1, so a linear meter fed by RMS barely leaves the floor and produces exactly
      the dead-looking waveform this replaces; the meter spans −50 dBFS (quiet room) to −10 dBFS
      (close talking) instead. The HUD **hands the terminal line over** to the interim-transcript
      display as soon as real words arrive — they share one row, and text is more informative than
      a waveform once there is text.
      **Honest scope note:** only the *chunked* paths can be metered. `batchVoiceTurn` calls
      `RecordClip`, which shells out to sox writing a whole file with no incremental readback, so
      it keeps the synthetic animation. Metering it would mean restructuring it onto `ChunkScanner`
      — a real change to the proven fallback path, not worth it for an animation.
- [x] P12.5 **Barge-in v2:** `audio.PlaySpeechContext` + a `ctxStreamer` wrapper end the beep
      stream when the context is cancelled, so playback stops at the next buffer (~50 ms) —
      **mid-sentence**, where v1 could only stop between sentences. `SpeakStream` derives a
      cancellable context, publishes it via `speech.StopSpeaking()`/`Speaking()`, and registers it
      with the interrupt manager **inside `SpeakStream`** rather than at each call site, so every
      caller (interactive shell, daemon, ambient responses) gains it at once — closing a real gap
      where **Ctrl+C did not stop a spoken reply at all**. `Speak` (`/say`, `remote say`) is
      context-aware too.
      **Residual, documented honestly:** a *wake-word* barge-in during playback still needs echo
      cancellation — the mic would hear Helix's own voice and re-trigger (the half-duplex
      constraint from Phase 2). `StopSpeaking()` is the seam that path will call; today's working
      triggers are Ctrl+C and any programmatic caller.
- [ ] P12.6 Manual QA: HUD readability across terminals; first-audio latency with a real TTS key.

**Acceptance:** voice mode shows live sci-fi feedback; spoken replies begin within one sentence's
synthesis; a quiet room never hangs or phantom-wakes (fixture + manual QA).

---

### Phase 13 — Unified Surface, Persona & Initiative

**Goal:** Make the capabilities of Phases 1–12 usable by a person who has not read this document.
Everything below came out of driving a real first-run session and reading the transcript; none of
it was planned scope, and most of it is repair.

**Why it needed a phase.** Phases 1–12 each shipped a capability behind its own verb, so the voice
and perception surface was eight commands (`/voice`, `/manual`, `/voice-setup`, `/voice-status`,
`/wake`, `/say`, `/tts`, `/eyes`) whose combined state nothing reported. Turning Helix on meant
remembering which subsystem lived behind which name, and the two halves of perception could sit in
contradictory states with no single place to see it.

**Tasks:**
- [x] P13.1 **One command.** All eight fold into `/blackbox` (`on|off|status|setup|look|eyes|wake|
      tts|say`, alias `/bb`). An old name prints where it went rather than a did-you-mean list.
      `/blackbox on` opens microphone, camera, speech and initiative together; `/blackbox off` and
      the spoken "manual mode" close all of them. See the ADR-008 amendment.
- [x] P13.2 **The companion loop** (`cmd/helix/companion.go`) — the first part of the voice stack
      that is not turn-shaped. Answers P5.4's deferred "activity awareness" question. Two cost
      controls are the entire design: a 16×16 luminance fingerprint diffed in-process decides
      whether a frame is worth a model call, and the model is asked to return a sentinel when
      nothing is worth saying. **Half duplex shaped the rest**: the companion never speaks, it
      QUEUES, and the main loop drains the queue only where the microphone is provably closed —
      otherwise Helix transcribes and answers itself. Pacing backs OFF on a slow host
      (`max(interval, smoothed last look)`) and deliberately does not speed up on a fast one.
- [x] P13.3 **Persona** (`internal/agent/persona.go`). Every prompt in the tree constrained FORMAT
      and none said who was speaking, so replies arrived in the provider's default assistant
      register. Prepended to the planner (where most spoken replies are born as response steps),
      the chat fallback and the camera path. `VoicePersona` applies only to spoken turns. Shapes
      tone, never authority — pinned by test.
- [x] P13.4 **First-run completeness** (`internal/deps/`, `cmd/helix/first_run.go`). A precompiled
      binary had no way to learn that speaking needs sox and seeing needs ffmpeg; both were
      discovered by failing, later. First boot now chains provider → system packages → speech
      chain, detects the host package manager across eight of them, detects by CAPABILITY (`rec`
      satisfies sox), and never emits an install command for a package name it cannot verify.
- [x] P13.5 **No Docker, anywhere** (ADR-002 amendment). QA was walked to `Cannot connect to the
      Docker daemon` as the final line of voice setup. Sidecar specs gained an `Unmet`
      precondition and Kokoro now refuses early, pointing at Piper.
- [x] P13.6 **One visual language** (`internal/shell/panel.go`). `/help` had *a* language and
      nothing else used it. Panels, badges, self-fitting tables, gutter-aware wrapping. Applied to
      `/blackbox status`, the voice chain, `/tools`, and every setup and wizard screen.
      Verified by screenshotting real PTY output — which is how two defects that survived code
      review were found.

      > **Amended 2026-08-26.** This originally read "`/help` had a GOOD one" and listed `/about`
      > among the screens converted. Both were generous. `/help` was never converted at all and
      > was still drawing a hardcoded 76-column rule with a fixed gutter that misaligned nine of
      > fifty-six commands; `/about` had panel *bodies* but no panel titles, so three sections
      > closed with a rule that had no opening rule. `/help`, `/help <command>`, `/about`,
      > `/provider-status`, `/doctor` and the unknown-command screen were all converted later —
      > the last of them nine months of dev-log entries after this box was ticked. Left ticked
      > because the primitives it delivered are real and everything since builds on them; the
      > amendment records that "applied to X" meant "applied to some of X".
- [x] P13.7 **Voice reaches the shell** (ADR-005 amendment): denied set 20 → 9, each argued
      individually, and the report reads it from the registry instead of a stale prose copy.
- [x] P13.8 **Repairs found by running it.** Web-search grounding (the answer directive sat inside
      a fence the prompt tells the model to distrust, so the harness re-searched forever); the
      camera framerate that no webcam accepts (P5.9); reasoning models spending their whole token
      budget thinking and returning empty; sidecar ports lost between assignment and commit; keys
      requested twice; multi-line errors mangled; a live banner that reported the camera state it
      had not set yet.

**Acceptance criteria:**
- [x] One command reaches every voice and perception capability; old names are discoverable.
- [x] A fresh install reaches a working voice chain without reading documentation.
- [x] Nothing in the product requires a container runtime.
- [x] Replies do not read as generic-assistant output (persona present on all three paths).
- [ ] **The companion loop driven by a real camera.** Blocked on macOS camera authorisation for
      the host app, not on code. Measure a true frame-to-remark cycle and tune `interval_s` /
      `change_threshold` against it — the current defaults are reasoned, not measured.
- [ ] A real end-to-end voice session after the endpoint fix: speak, be heard, be answered aloud.
      The fix is proven by test, not by ear.

**Risks:** the persona is prompt-shaped and therefore provider-sensitive — a weaker model may
ignore it; the companion's defaults are guesses until a real camera measures them.

---

### Post-BlackBox roadmap (parked, from original plan — do NOT scope into phases above)

Multi-user voice profiles · smart-home integration (Home Assistant) · proactive AI (calendar/email
monitoring) · mobile companion · custom voice & personality training · vocal emotion adaptation ·
automatic multi-language switching · full-duplex barge-in · YAMNet-class ambient classifiers.

---

## §7. Configuration & Data Layout (target schema)

`~/.helix/config.json` — existing keys plus:

```jsonc
{
  // ...existing Config/UserPrefs fields unchanged...
  "user_prefs": { "voice_mode": false, "typing_effect": true /* etc, unchanged */ },
  "speech": {
    // "endpoints" is PER PROVIDER (added Phase 13). "base_url" only ever reached the
    // PRIMARY, so a local sidecar moved to a free port could not be used as a fallback —
    // its new address landed in a field belonging to whoever was primary.
    "stt":    { "provider": "openai", "model": "whisper-1", "fallbacks": ["whisper-local"],
                "endpoints": { "whisper-local": "http://127.0.0.1:28859" } },
    "tts":    { "provider": "elevenlabs", "model": "eleven_turbo_v2_5", "voice_id": "",
                "fallbacks": ["piper-local"],
                "endpoints": { "piper-local": "http://127.0.0.1:28183" },
                // ADR-017 — conversational context for CSM-1B. 0 disables it, which is the
                // default and the behavior of every other provider. Retention is memory-only
                // and bounded; enabling it holds recent AUDIO for longer than one turn, which
                // is why it is opt-in (threat V5b).
                "context_turns": 0, "context_max_bytes": 4194304 },
    "wake_word": { "enabled": false, "engine": "openwakeword-sidecar", "phrase": "hey helix",
                "sensitivity_preset": "balanced", "cooldown_s": 2 },
    "capture": { "backend": "auto", "device": "default", "sample_rate": 16000,
                "silence_timeout_ms": 1500, "max_utterance_s": 30 }
  },
  "llm": {                                   // Phase 11 (ADR-016)
    "llamacpp_url": "",                      // "" → HELIX_LLAMACPP_URL → http://127.0.0.1:8080/v1
    "fallback": { "enabled": true,           // default TRUE (omit the key to keep it)
                  "provider": "ollama",      // "ollama" | "llamacpp"
                  "model": "",               // "" → provider default / active model
                  "threshold": 2,            // consecutive availability failures that trip it
                  "retry_after_s": 120,      // half-open cloud probe interval
                  "ensure_ready": false }    // true = daemon may PULL the model at startup
  },
  "voice_policy": { "max_risk": "medium", "confirm_timeout_s": 8,
                "dangerous_needs_typed": true, "min_transcript_confidence": 0.6 },
  // "model" (Phase 13) routes FRAMES to a different model than chat: the companion runs on a
  // timer and shares the runtime with the conversation, so the model that answers questions is
  // usually the wrong one to describe a frame every 20 seconds.
  "vision": { "enabled": false, "provider": null, "model": "", "max_frames_per_turn": 1 },
  // Phase 13 — initiative. interval_s is a FLOOR; the real gap is max(interval, last look),
  // so a slow host backs off instead of queueing. An unchanged frame costs no model call at all.
  "companion": { "enabled": true, "interval_s": 20, "cooldown_s": 45, "change_threshold": 0.08 },
  "ambient": { "enabled": false, "sensitivity": 0.5, "response_mode": "log",
                "categories": { "loud_noise": true, "alarm_like": true, "music_like": false } },
  // P2.8 — the opt-in voice interaction log. Absent section means OFF, which is
  // the whole guarantee: with it off there is no directory and no file. Text
  // only, never audio. Rotation bounds it on a small board.
  "voice_log": { "enabled": false, "max_bytes": 1048576, "keep_files": 3 },
  "daemon":  { "autostart": false, "journal": true, "session_turns": 20 },

  // ADR-019 — self-update. `check` is whether /reboot looks on every restart;
  // `channel` is where it looks. Installing is NEVER automatic and never
  // happens on the voice path, so there is no "auto_install" key to set: that
  // is a decision, not a setting.
  "update":  { "channel": "auto", "repo": "Nibir1/Helix", "check": true,
               "local_paths": [] }
}
```

**`~/.helix/` layout (end state):**

| Path | Purpose | Notes |
|------|---------|-------|
| `config.json` | prefs + speech/vision/ambient/policy config | existing |
| `secrets.json` | keys incl. `stt.*`, `tts.*`, vision provider | 0600, existing keystore |
| `helix.db` | RAG/threat-intel SQLite | existing |
| `daemon.sock` | IPC UDS (Unix) / named pipe (Win) | 0600, daemon-owned |
| `session.json` | conversation ring buffer | 0600, `/memory clear` wipes |
| `update-pending` | a note from a child to the restart supervisor that it installed something (so a bad update is rolled back) | 0600, removed when read, `/purge` wipes |
| `reboot.json` | `/reboot` continuity record — mode, cwd, provider/model, in-progress tasks, and (**typed restarts only**) a 240-rune excerpt of the last message | 0600 in 0700; **consumed on read** (one restart, not every boot); ignored past 12 h; `/purge` wipes. A SPOKEN reboot stores no conversation content — see V5d |
| `journal/` | append-only interaction journal | redacted, rotated (1 MiB × 3, `internal/journal`), `/purge` wipes |
| `voice_log/` | opt-in voice interaction log — **transcripts and metadata, no audio** | default absent (no dir, no file); 0600 in 0700; rotated; `/purge` wipes |

> **Correction (2026-08-23):** this table described `voice_log/` as "transcripts +
> audio refs" from the day it was written. There are no audio refs and there
> should not be: captured clips are deleted immediately after they are read
> (P1.7) and frames are never written at all (guardrail #6), so a stored path
> would point at nothing while still being a privacy liability. The shipped log
> records text and metadata only, and a test asserts no entry references an audio
> file. The `journal/` row's "rotated" was aspirational for just as long —
> rotation did not exist until P2.8 built it as shared machinery.
| `metrics/` | wake, voice, speech, vision, ambient, uptime samples (NDJSON) | 0600 in 0700, local only; read by `/blackbox stats`; `/purge` wipes |
| `trash/` | undo-journal staging | `/purge` wipes |
| `crash-*.json` | diagnostics reports | existing |
| `pricing.json` | user override of embedded pricing catalog | optional |

---

## §8. Dependency decisions (summary table)

| Need | Chosen approach | Alternative rejected | ADR |
|------|-----------------|----------------------|-----|
| Cloud STT/TTS | stdlib `net/http` via existing `providers.HTTPClient` (retries built in) | SDKs (bloat) | — |
| Local STT | whisper.cpp / faster-whisper **sidecar over HTTP** | CGO whisper bindings (breaks CGO-free) | 002 |
| Local TTS | Piper **sidecar over HTTP** | CGO/espeak | 002 |
| Mic capture | `sox`/`ffmpeg` shell-out (CGO-free) | malgo/portaudio CGO (optional later via `audio_cgo` tag) | 003 |
| Speech playback | existing beep/oto (`internal/audio`) | new audio lib | 007 |
| Wake word | openWakeWord **sidecar** | Porcupine (licensing/access key), onnxruntime CGO | 002 |
| FFT (ambient) | hand-rolled or `go-dsp` (license check at phase start) | heavier DSP libs | — |
| Camera frames | `ffmpeg` shell-out single-frame | gocv/OpenCV CGO | 003/005 |
| IPC | stdlib UDS/named-pipe + NDJSON | Redis, ZeroMQ, gRPC | 004 |
| Tray indicator | **decision deferred to Phase 7** (systray lib vs helper binary) | — | pending ADR-010 |

---

## §9. Testing strategy (cross-phase rules)

1. **No audio hardware in CI, ever.** All speech tests run against `httptest` mock endpoints
   (extend the `tests/e2e/harness_test.go` pattern) or WAV fixtures. Hardware-dependent checks are
   manual QA with logged checklists.
2. **Synthetic transcript injection** is the primary voice-policy test vehicle: build
   `InputEvent{Channel:"voice", Text:...}` programmatically — no mic needed to prove safety
   behavior.
3. **Policy is table-driven**: deny-list matrix (action × channel × confidence × auth state) must
   stay at full coverage as it grows.
4. **Privacy is test-enforced**: fs-snapshot tests prove no frame/audio bytes land on disk outside
   opt-in paths; diagnostics-style grep test keeps networking out of journal/redaction code
   (shipped for `internal/journal` in P2.8 — `TestNoNetworkImports`). A default-OFF privacy
   feature needs **two** tests, not one: a unit test that the disabled path writes nothing, and an
   e2e test that the shipped wiring actually leaves it disabled. A log opened eagerly at startup
   passes the first and fails the second.
5. **Fuzz** every new parser: pricing merge, WAV/MP3 headers, NDJSON IPC, transcript→policy.
6. **E2E stays honest**: if a required sidecar binary is absent, tests skip loudly
   (`t.Skipf`) — never silently pass.
7. **Baseline rule**: `make test && make e2e` green on `blackBox` before every merge/commit-series;
   pre-existing suites are never weakened — only extended.
8. **A regression test must be able to REACH the regression.** Assert the precondition that puts
   the code under test on the path, not just the outcome. The `Table` alignment test written for
   the `truncateANSI` column bug passed against the buggy code: its fixture was not wide enough
   for `fitTableWidths` to shave anything, so the truncation path never ran. It now asserts the
   cell actually carries an ellipsis before asserting alignment. A test that cannot reach its
   target is worse than no test, because it reads as coverage. **Verify a fix by reverting the
   implementation and watching the test fail** — that is the only cheap proof the test bites.
9. **`make test && make e2e`, not a hand-rolled `go test` line.** `go test` caches a package's
   result on ITS OWN inputs, and `tests/e2e`'s inputs do not include `cmd/helix`: the harness
   builds the binary in a subprocess the go command cannot see. So after a change to the
   binary, `go test ./... ./tests/...` replays a PASS recorded against the OLD binary and
   reports green. That is not hypothetical — it hid a real `/doctor` regression through several
   commits whose messages claimed "full suite incl. e2e green". The Makefile's `-count=1` was
   always the reason `make e2e` was trustworthy. `TestSourceTreeIsPartOfTheCacheKey` now reads
   every source file the binary is built from so the cache key tracks it, and the fix had to go
   in a TEST rather than `TestMain`: the file-open logger is installed by `m.Run`, so reads
   before it are invisible to the cache (measured — the input ID was byte-identical across a
   source change until the reads moved).
10. **Colour is a measurable property, not a taste.** A text colour that looks deliberate in
    source can be unreadable on screen, and rule 11 below would not have caught it either —
    an ANSI capture of unreadable text looks exactly like an ANSI capture of readable text.
    `HexSubtle` was `#444444`, which measures **1.44:1** against a dark terminal where WCAG's
    floor for body text is 4.5:1, and it shipped that way for as long as the palette existed
    because nobody could point at a number. Assert contrast ratios per ROLE
    (`internal/shell/contrast_test.go`): text tones must clear AA, chrome must be visible but
    stay BELOW the text it frames, and measure against the LIGHTEST plausible background so
    the figure is the worst case rather than the flattering one.
11. **A red test is a message, not an obstacle. Read it before re-running it.**
    `TestE2E_ManualModeSafetyValve` failed twice in one session; both times it was re-run, came
    back green, and was written off as "a flake, unrelated — it runs `ls`". It was a real race
    the harness's own doc comment describes verbatim: `Expect` matches the FIRST occurrence in
    the accumulated buffer, so a second command printing the SAME marker is satisfied by the
    previous command's output and returns while this one is still running (`SendExpect` exists
    for exactly this). Re-running until green is not a diagnosis — it is the absence of one, and
    it hides precisely the timing bugs that will fail in CI on someone else's machine. When a
    test does turn out to be flaky, prove it: find the cause, then check whether the pattern is
    systemic rather than assuming — a script over every e2e function found 21 `WriteLine`+`Expect`
    pairs and exactly one that repeats a marker.
12. **Rendered output is verified by RENDERING it.** Reading the code gave a confident wrong answer
   three times in the panel primitives: `KV` overflowing, `PanelWrap` counting bytes,
   `truncateANSI` counting runes. All three look correct in source and break on screen. Print the
   panel (or screenshot the PTY) and measure the visible width; and when the bug is about units,
   check the units of the ASSERTION too — two of the tests written for that batch had the same
   byte-vs-column confusion they were written to catch.

---

## §10. Success metrics (adjusted for local-first reality)

| Metric | Cloud path | Local-only path | Measured by |
|--------|-----------|-----------------|-------------|
| Wake-word detection accuracy | ≥97% | ≥97% | Phase 3 fixture corpus |
| Wake false positives | ≤1/hour (balanced preset) | same | metrics log |
| STT accuracy (clean speech) | ≥95% | ≥90% | fixture corpus |
| E2E voice command latency (wake→execution start) | ≤3s | ≤6s | metrics log |
| TTS first-audio latency | ≤800ms | ≤1.5s | benchmark |
| TTS naturalness (user-rated) | ≥4/5 | ≥3.5/5 | manual QA |
| Daemon uptime | 99.5% (72h soak) | same | daemon heartbeat + `/blackbox stats` (was: soak script, whose log was unparseable) |
| Mode switch latency | ≤1s | same | e2e |
| Frame-to-insight (vision) | ≤5s | best-effort (llava) | metrics log |
| Noise classification (enabled categories) | ≥90% | same | Phase 6 fixtures |
| Companion look → spoken remark | n/a | bounded by the interval floor, not the model | Phase 13 |

**Reading these numbers (added 2026-08-23):** `/blackbox stats` summarizes every metric in this
table that Helix records, judged against the right column — local and cloud samples are separated
by the provider recorded with each sample, because a blended p50 would be graded against a budget
that applies to neither half. Until then the files existed and nothing read them, which is the
practical reason P7.8 had never been run: the data was there and the arithmetic was manual.
`internal/metrics` owns the field names for both writing and reading, so the two cannot drift.

---

## §10A. P7.8 metrics run — results (2026-08-23)

The run the release gate asked for. Every row of §10 is accounted for: measured with a number, or
unmeasured with the specific thing that is missing. "Manual QA" is not a reason; a microphone is.

| # | Metric | Target | Measured | Verdict |
|---|--------|--------|----------|---------|
| 1 | Wake-word detection accuracy | ≥97% | **100%** (20/20 speech-level clips), **0%** false positives (0/18 room-noise clips) — energy engine, balanced preset | ✅ for the shipped engine, with a scope caveat below |
| 2 | Wake false positives | ≤1/hour | **0/18 on fixtures.** Per-hour needs real ambient audio over real hours | ⏳ needs a microphone |
| 3 | STT accuracy (clean speech) | ≥95% cloud / ≥90% local | **97.0% word accuracy** local (65/67 words, 8/10 utterances perfect, slowest 133ms) via stock whisper.cpp + `ggml-base.en` | ✅ local. Cloud needs a key |
| 4 | E2E voice command latency (wake→exec) | ≤3s / ≤6s | — | ⏳ needs a microphone (the wake event is the start of the clock) |
| 5 | TTS first-audio latency | ≤800ms / ≤1.5s | **103ms** local (piper-local, decoded to 41,728 non-silent samples) | ✅ local. Cloud measured once by owner QA at 2,280ms **before** P7.2c's streaming fix and never re-measured |
| 6 | TTS naturalness | ≥4/5 / ≥3.5/5 | — | ⏳ needs a human ear; not automatable by construction |
| 7 | Daemon uptime | 99.5% over 72h | Tooling verified against a synthetic 72h set (4303/4320 = 99.61%, 2 restarts, 13m worst gap) | ⏳ needs 72 hours of wall clock; run `scripts/soak.sh` then `/blackbox stats` |
| 8 | Mode switch latency | ≤1s | — | ⏳ needs a microphone: entering live mode requires a recorder + STT chain, so there is no honest way to time a switch that cannot happen. Measuring the mic-less *refusal* instead would be a different number wearing this row's name |
| 9 | Frame-to-insight (vision) | ≤5s cloud / best-effort local | **8.8s** warm local (`gemma4:e2b`; 31.6s cold, 19.2s second) | ❌ vs the cloud target, ✅ as best-effort local. Cloud needs a key; the local number predates nothing and stands |
| 10 | Noise classification | ≥90% | **100%** (57/57 fixtures; alarm 27/27, loud 12/12, music 10/10, silence 8/8) | ✅ |
| 11 | Companion look → remark | bounded by the interval floor | — | ⏳ needs camera permission (macOS withholds it; re-confirmed 2026-08-23) |

**Scope caveats, because these numbers will be quoted:**

- **Row 1 measures onset detection, not keyword spotting.** ADR-002 chose the energy detector as
  the default and said plainly that it detects speech onset rather than the phrase; it will fire on
  "hey helix", on "hello there", and on a dropped mug. A genuine keyword-accuracy figure requires
  the openWakeWord sidecar and a live server. The 100% above is the detector doing its specified
  job, not evidence it recognises a phrase.
- **Row 3 is synthesized clean speech, which is an upper bound.** One voice, no room, no
  background, perfect articulation. §10 asks for "clean speech" so this is the right measurement
  for the row, but a person in a kitchen will do worse and a corpus of real recordings would be a
  harder, more useful number. Of the two errors, one is a genuine misrecognition
  (`called`→`call`) and one is numeral formatting (`twenty`→`20`), which the planner would read
  correctly — so the recognition-level figure is 98.5% and the reported figure stays 97.0%.
- **Row 5's cloud number is stale, not missing.** 2,280ms was measured against the buffered path
  that P7.2c replaced with streaming; quoting it now would defame a code path that no longer
  exists, and quoting the expected ~150ms would be inventing a measurement. It is listed as
  needing a key.

**What this run says about the release gate.** Of eleven rows, four are measured and pass, one
(frame-to-insight) misses its cloud target with an honest local best-effort number, and six wait on
hardware, a key, a human, or a clock. **No row is blocked on unwritten code.** The two that would
most change the picture are both microphone rows (4 and 8), because they measure the loop a user
actually feels.

Rationale: the original plan's single ≤3s target assumed cloud providers end-to-end; the repo's
local-first direction (Ollama standardization, commit `ca9560b`) makes honest dual targets
necessary.

**First real measurements (2026-08-22, M-series MacBook Air, through Helix's own wire path):**

| Metric | Target | Measured | Verdict |
|--------|--------|----------|---------|
| Frame-to-insight, local `gemma4:e2b` (5.1B) | best-effort | 31.6s cold → 19.2s → **8.8s** warm | misses the ≤5s cloud target; cloud path still unmeasured |
| Frame-to-insight, `moondream:1.8b-v2-q4_K_M` | — | ~0.3s | **rejected** — Ollama's build returns an empty string for instruction-style prompts and coordinate arrays (`ids: [0.39, …]`) otherwise. Speed is irrelevant if the output cannot be used. |

**Local sidecar measurements (2026-08-23, M4 MacBook Air, through Helix's own adapters):**

| Path | Measured | Against |
|------|----------|---------|
| `whisper-local` STT, `ggml-base.en` (3 s clip) | **167 ms** | STT is not separately targeted in the table; for scale, the ≤6 s local wake→execution budget |
| `piper-local` TTS first bytes, `en_US-lessac-medium` | **103 ms** | ≤1.5 s local TTS first-audio target — comfortably inside it |

Both are the sidecar round trip only: no microphone capture, no speaker playback, no model cold
start (the server was already up, which is how the daemon runs it). They say the local chain is
fast enough that latency on the private path is dominated by capture and playback rather than by
the models — which is the opposite of the vision path, where the model dominates everything.

Two things the first measurements exposed that the table could not. A **reasoning** model spends its budget before
it answers: `gemma4:e2b` at the 512-token chat default produced ~770 characters of private
reasoning and emitted no answer at all, which reached the user as "the vision model returned
nothing" — vision calls now get 1024 tokens and budget exhaustion is reported rather than
swallowed. And the ≤5s target is written against a **cloud** vision provider; on a local VLM the
honest number today is ~9s warm, so the companion's interval is a floor rather than a schedule
(see Phase 13) precisely because the model cannot be assumed fast.

---

## §11. Traceability — original plan → this roadmap

| Original plan item | Where it lives now | Amendment |
|--------------------|--------------------|-----------|
| Layer 1 multi-provider STT/TTS + pricing UX | Phase 1 | Go adapters copying `internal/providers` pattern; pricing data-driven (ADR-006); failover added |
| Layer 2 voice execution + confirmation loop | Phase 2 | Reuses pipeline via `HandleInput` seam; VoicePrompter fail-closed; clarification loop scoped |
| Layer 3 Living AI daemon | Phase 4 (4A–4E) | Split into Agent-decoupling → session/undo → daemon/IPC → installers; tray deferred to Phase 7 |
| Layer 4 vision | Phase 5 | Multimodal message parts; ffmpeg capture; interval-polling awareness deferred |
| Layer 5 noise detection | Phase 6 (optional) | Demoted; rule-based v1, classifiers deferred |
| Layer 6 mode switching | Phase 2 (core) + Phase 7 (hybrid) | Pulled forward (ADR-008) |
| Python 3.11+ recommendation | rejected | ADR-001 (Go) |
| Redis/ZeroMQ IPC | rejected | ADR-004 (stdlib sockets) |
| "Existing codebase is stable enough" (assumption) | Confirmed true | §2 analysis; branch `blackBox` at parity, clean |
| Success metrics | §10 | Dual cloud/local targets |
| Security/privacy controls (plan §4.4, assumptions) | §5 threat model + ADR-005 | Extended with voice-channel injection — the plan's biggest gap |

---

## §12. Guardrails — NON-NEGOTIABLE

1. **Go only.** No Python rewrite of core Helix. Sidecars are external processes, consent-gated,
   checksum-pinned.
2. **`main` is sacred.** Everything on `blackBox`; merge only by explicit owner approval.
3. **The safety pipeline is always in the path.** No input channel (voice, IPC, hybrid) bypasses
   classify → plan → firewall → risk tiers → sandbox. Ever.
4. **Voice never confirms destructive actions.** Typed confirmation remains mandatory for the
   deny-list; VoicePrompter fails closed.
5. **Voice risk cap: Medium.** High-risk is unreachable from the voice channel regardless of
   phrasing.
6. **No persistence of audio/frames by default.** Opt-in logs only, 0600, redacted, `/purge`-able;
   camera frames are memory-only, always.
7. **Telemetry-free stays telemetry-free.** No cloud call without a configured provider + key the
   user entered. Pricing/catalog fetches are embedded/local (user override file), not phone-home.
8. **CGO-free default build.** CGO-gated features behind build tags only (`audio_cgo` precedent).
9. **No heavyweight infra dependencies** (Redis/ZeroMQ/message brokers). Stdlib sockets.
10. **Pricing is data, not code** (embedded JSON + user override).
11. **Never weaken existing tests to make new ones pass.** Extend, don't dilute.
12. **Every phase ends green** (`make test && make e2e`), tracker updated, conventional commits.

---

## §13. Progress Tracker (LIVING SECTION — update after EVERY work session)

### Phase status

| Phase | Status | Started | Completed | Notes |
|-------|--------|---------|-----------|-------|
| 0 — Decisions & Threat Model | `DONE` | 2026-08-16 | 2026-08-16 | Baseline green; ADRs ratified; `docs/threat_model_voice.md` written; 6 skeleton packages compiling+tested; CI covers them automatically |
| 1 — Speech Provider Layer | `DONE` | 2026-08-16 | 2026-08-23 | speech pkg (types/registry/failover/pricing/5 adapters/capture), audio.PlaySpeech (WAV/PCM, MP3 skipped by design), the setup/say/listen/tts/status commands (all folded into `/blackbox` in Phase 13), 40+ new tests green. **Phase 13 added** per-provider endpoint overrides and verify-then-reuse for saved keys. **Closed 2026-08-23:** the local-sidecar QA ran live against a stock `whisper-server` (167 ms, exact transcript) and piper's `http_server` (103 ms, decodable non-silent WAV) through Helix's own adapters, kept as an opt-in repeatable test rather than a prose log; and `FuzzPricingMerge` finished P1.11, whose checkbox had been ticked with only its WAV half written. Audible-playback QA remains the one manual item, by design — it needs a speaker, not a test |
| 2 — Voice Input & Policy | `DONE` | 2026-08-16 | 2026-08-23 | HandleInputEvent channel stamping, Voice Risk Policy (cap+gate+deny list), VoicePrompter fail-closed, /voice /manual + graceful fallback, spoken responses. **P2.8 closed 2026-08-23**: opt-in voice interaction log on new shared `internal/journal` machinery (rotation, 0600-in-0700, redaction) which the Phase 4 daemon journal now shares — the deferral was correct, it produced one implementation instead of two. **Closed 2026-08-23:** the Risks line's unwritten verification found that spoken sentences beginning with a command name were executed verbatim rather than planned (9/9 measured phrasings) — voice no longer takes the direct-shell bypass; and the multi-turn clarification carry-over turned out to be delivered by Phase 4 session memory but broken on the confidence-gate path, which asked "repeat that?" without recording that it asked and stored the rejected transcript as trustworthy user speech. Real-mic QA still pending |
| 3 — Wake Word | `DONE` | 2026-08-16 | 2026-08-23 | energy detector default + openWakeWord-class sidecar client; wake-only between turns (ADR-005 §5 by construction); kill phrases; wake.jsonl metrics; fixture+mock tested. **Closed 2026-08-23:** P3.5's deferred display shipped as `/blackbox stats` over a new `internal/metrics` that owns both ends (four metrics files had been written since Phase 3 and nothing ever read one — the practical reason P7.8 had no tooling), plus a fifth file for TTS first-audio, which had lived only in memory; the Risks line's CPU measurement was finally taken (21.4µs/chunk = **0.0014%** duty cycle, zero allocs) and pinned by a duty-cycle test; and the between-turn lockout is now AST-enforced rather than only true. Real-keyword accuracy (sidecar) + a real FP/hour figure = manual QA pending |
| 4 — Daemon & Living AI | `DONE` | 2026-08-16 | 2026-08-23 | Renderer + SlashDispatcher seams, session ring buffer + `/memory`, undo journal, `helix daemon` + NDJSON IPC (UDS / Windows token TCP) + `helix remote`, connectivity local-first failover, service installers, journal + `diagnostics.Guard`, greeting + break reminder, `scripts/soak.sh`, e2e remote test; per-sidecar `Health()` polling loop. **Closed 2026-08-23:** two acceptance criteria that needed no hardware were proven — "undo that" after a voice commit now soft-resets a REAL repository with confirmation (plus three refusal cases), and the connectivity transition is covered four ways after being extracted from its TCP-probe loop. The 72h soak became evaluable: the daemon heartbeats liveness and soak.sh's evidence is valid NDJSON for the first time. Logout/reboot survival and the 72h wall clock remain manual |
| 5 — Vision | `DONE` | 2026-08-16 | 2026-08-23 | `MessagePart` multimodal format + OpenAI/Ollama/Anthropic wire adapters, `ai.RunVisionModel` (capability-gated), ffmpeg memory-only capture (fs-snapshot + stdout-only tests), the camera opt-in + "turn off your eyes" kill switch + metadata-only journal, P5.5 dedicated `vision.provider` fallback. **Amended 2026-08-22:** deictic voice routing was REMOVED (P5.4, superseded — it hijacked any sentence containing "this"); the camera is now reached by `/blackbox look` or the planner's `vision` tool (P5.7), the darwin framerate is negotiated rather than hardcoded (P5.9, every capture on that hardware had been failing), and `vision.model` routes frames separately from chat. Manual QA still pending — blocked on OS camera authorisation, not code. **2026-08-23:** blocker re-tested and confirmed (ffmpeg enumerates the camera, delivers nothing, needs killing), but the re-test disproved this phase's own claim that a frameless capture "says so explicitly instead of looking like a hang" — it took 30s and printed the ffmpeg version banner, because the macOS guidance sat behind a `stderr != ""` guard that ffmpeg's banner made permanently true. Fixed with `-hide_banner`, an 8s capture deadline, and a deadline-led diagnosis; `/blackbox status` also stopped reporting **watching** for a camera that has never delivered a frame |
| 6 — Ambient Audio (optional) | `DONE` | 2026-08-16 | 2026-08-23 | Rule-based analyzer (RMS + hand-rolled FFT concentration → silence/loud/alarm/music) + cooldown-gated service + response mapping + config + golden fixtures + fuzz, live wake-stream `TeeScanner`/`ChunkMonitor` wiring; CPU budget benchmark green. **Corrected 2026-08-23:** the "26µs/chunk" figure quoted here was the 1024-sample *window* cost, not a chunk — the real production path (WAV decode + a 32768-point padded FFT over a 1500ms chunk) is **623µs**, and the duty cycle is **0.038%**, now asserted rather than inferred. **Acceptance closed 2026-08-23**: 57-fixture corpus measures **100%** accuracy per category against a ≥90% target, quiet-band sweep proves the no-spam gap, cooldown proof and default-off doc line were already in place and simply unticked |
| 7 — Polish & Release | `IN PROGRESS` | 2026-08-16 | — | `input.HybridSource`, 3 new fuzz targets, ADR-010 (tray helper), `docs/blackbox.md`, benchmark suite, streaming STT partials (Deepgram WS), TTS latency budget metric, 3-OS e2e matrix (Windows daemon IPC), Ollama installer checksum pinning, §10 latency-metrics instrumentation (wake→exec + frame-to-insight) done. **P7.8 metrics run done 2026-08-23** — results in §10A: four rows measured and passing (wake detection 100%/0% FP, local STT 97.0% word accuracy, local TTS first audio 103ms, noise classification 100%), frame-to-insight honest at 8.8s local against a cloud target, and six rows waiting on a microphone, a key, a human ear or a 72h clock, each recorded with its specific blocker. **Only P7.9 (the `blackbox-v0.1.0` tag, owner-gated) remains** |
| 8 — Agentic Harness | `DONE` | 2026-08-17 | 2026-08-17 | `executePlanSteps`+`planFirewallExecute` refactor, `harness.go` bounded plan→act→observe→replan (data-only fenced observations, ADR-013), `/agentic` toggle + persisted pref. **P8.6 output capture done**: bounded tee-ing `TailBuffer`/`OutputCapture` + `RunShellCommandCaptured`, sanitized per-step output tail + true `ExitCode` in the observation block (ADR-013 amendment), agentic-gated so the default path keeps its TTY; `FuzzSanitizeOutput` found and fixed a sanitizer-ordering bypass. **P8.7 native tool calling done**: normalized `ToolDefinition`/`ToolCall` types + streamed tool-call accumulator + `openai_compatible` implementation (7 providers), planner `emit_plan` tool with `tool_choice=required` and silent fallback to the prompt ladder, honest `Capabilities.ToolUse` via `SupportsToolUse`. **P8.8 streaming render done**: `ai.StreamModel` + `ux.AIStreamWriter` + optional `agent.StreamingRenderer` seam (headless/daemon keep the buffered path by design), spinner stops at the first token, three duplicated chat-fallback blocks unified into `Agent.chatFallback`. **P8.7b**: Anthropic (`tool_use` blocks + `input_schema`) and Ollama (`/api/chat` tools, object-valued arguments normalized, **per-model** gating because Gemma ships no tool template) now drive tools natively; shared plumbing in `providers/tools.go`. 70 new tests across P8.6–8.7b. **Phase 8 fully complete — all five tool-capable cloud providers plus Anthropic and tool-capable Ollama models use native function calling** |
| 9 — Cheap Speech Defaults | `CODE DONE` | 2026-08-17 | — | Groq STT + Aura-2 TTS + Kokoro-local adapters, pricing.json refresh (Groq turbo & gpt-4o-mini-tts recommended, ADR-011), `GROQ_API_KEY` mapping, wizard persists model, contract tests. **P9.7 chain presets done 2026-08-23** — three pre-worked chains at the top of `/blackbox setup`, feeding the same verify path, pinned to the pricing catalog by test; the "private" preset uses piper-local rather than Kokoro because Kokoro needs Docker. Only P9.8 real-key QA remains |
| 10 — Linux Edge-Device Deployment | `TOOLING DONE` | 2026-08-17 | — | `docs/edge_deployment.md` per-board matrix (Pi 5/4, Jetson Nano 1st-gen, amd64 mini-PC, arm64 SBC, RISC-V). **P10.2** `scripts/edge-setup.sh` — consent-gated, SHA-256-verified Ollama install (fail-closed), Jetson-Nano refusal → cloud path, `--check`/`--dry-run`/`--yes`/`--assume-board`, shellcheck-clean; ML sidecars stay user-managed with printed instructions (no pinnable artifact — same call as P7.7). **P10.3** `internal/edge` + `/doctor` "edge appliance" section: board, build flavor via new `audio.SpeechSupported`, confinement in force + remediation, recorder, local sidecar reachability, offline-LLM model-pulled check, thermals/throttling. **P10.4** `internal/edge/systemd.go` — edge-aware `systemd --user` unit (`Wants=network-online` fixing a silently-inert `After=`, restart-storm bounds in `[Unit]`, edge env knobs) + linger detection and post-install notes wired into `helix daemon install`. 29 new tests incl. a checksum pin-drift guard and unit percent-escaping. Only P10.5 hardware QA remains (inherently hardware-gated) |
| 11 — Offline LLM Resilience | `CORE DONE` | 2026-08-17 | — | Daemon `ai.InitProviders` bug fixed (P11.1); `internal/ai/failover.go` circuit-breaker cloud→local brain failover (P11.2) wired into both the interactive shell and the daemon connectivity monitor; `ensureLocalBrainReady` startup verification, consent-gated pull (P11.3); `internal/providers/llamacpp` implemented over llama-server — P11.4 decided in favor of implement (ADR-016). 18 new tests. Real-network/real-key QA (P11.5) remains |
| 12 — Sci-Fi Presence & UX | `CODE DONE` | 2026-08-17 | — | `voiceviz.go` HUD (ADR-015), `SpeakStream` sentence-pipelined TTS (ADR-014), persistent gapless `StreamRecorder`, phantom-wake/silent-hang/wake-retry/sox-rec/prompter-print/Deepgram-endpointing fixes. **P12.4** `speech.ClipLevel` log-scale meter driving `VoiceViz.SetLevel` from the real mic (chunked paths; batch keeps synthetic — sox has no incremental readback). **P12.5** `audio.PlaySpeechContext` + `ctxStreamer` = true mid-sentence cancellation (~50 ms), `speech.StopSpeaking()`/`Speaking()` barge-in handle, interrupt-manager registration inside `SpeakStream` (Ctrl+C previously could NOT stop a spoken reply). 16 new tests. Only P12.6 manual QA remains; mic-triggered barge-in needs echo cancellation (documented residual) |

| 13 — Unified Surface, Persona & Initiative | `CORE DONE` | 2026-08-22 | — | Eight voice/vision commands folded into `/blackbox`; companion loop (interval capture, in-process frame diff, half-duplex queueing); persona on planner/chat/vision; first-run package stage (`internal/deps`, eight package managers, never Docker); one visual language (`internal/shell/panel.go`) across every report and wizard screen; ADR-002/005/008 amendments. Plus repairs found only by running it: web-grounding loop, camera framerate, reasoning-budget exhaustion, sidecar endpoints lost at commit, duplicate key prompts. Remaining: a real camera frame, and one end-to-end voice session after the endpoint fix |

### What is actually left (all phases)

"Core DONE" is not "done". Every unticked checkbox in §6, consolidated — **most are
hardware- or key-gated, not unwritten code**:

| Phase | Open | Kind |
|-------|------|------|
| 1 | audible playback on a machine with speakers | manual |
| 4 | logout/reboot survival on 3 OSes; the 72h soak wall clock (tooling ready); hearing the offline notice | manual |
| 5 | camera QA (needs macOS camera permission granted by a human); frame-to-insight ≤5s (local measured at 8.8s warm, cloud unmeasured — needs a vision key) | hardware + keys |
| 3 | live openWakeWord sidecar accuracy; a real FP/hour figure from daily use | manual |
| 7 | P7.9 `blackbox-v0.1.0` tag — **the only Phase 7 item left** | owner |
| 7 | §10 rows still unmeasured: wake FP/hour, wake→exec latency, mode-switch latency (all need a mic); cloud STT/TTS/vision (keys); TTS naturalness (a human); 72h uptime (a clock) — see §10A | hardware + keys + owner |
| 7 | P7.1 hybrid mode is built but unwired — no user can reach it (found 2026-08-23) | **owner decision** |
| 9 | P9.8 real-key QA (Groq / Kokoro sidecar) | manual |
| 10 | P10.5 hardware QA (Pi 5 / Jetson / amd64) | hardware |
| 11 | P11.5 network-cut QA with a real key | manual |
| 12 | P12.6 HUD/latency QA; mic barge-in needs echo cancellation | manual + **deferred** |
| 13 | real camera frame; end-to-end voice session after the endpoint fix | hardware |

**As of 2026-08-23 there was no unwritten code left in the plan** — and the
sentence has now been wrong twice, which is the reason it is written in the past
tense. It was wrong once for P1.11 (see below), and wrong again on 2026-08-27
when `/reboot` shipped: **unplanned code, asked for by the owner, that no row of
this table anticipated.** A tracker describes what was foreseen, not what the
project is; treat this as a claim to re-verify rather than a fact to inherit. P2.8 (voice log),
P9.7 (chain presets) and Phase 6's acceptance measurement were the last three *named* items. Two
more surfaced only by reading code against this table:

- Phase 4's session-recall criterion said "(e2e)" and no such test existed (written, plus the
  planner-prompt recorder it needed and a data-only fence check P4.4 had specified and nothing
  verified).
- **P1.11 was ticked with half of it unwritten.** It promised fuzzing of "pricing merge + WAV
  headers"; only the WAV half was ever built. This is the useful lesson for whoever reads next: a
  ticked checkbox can be half done, and a task whose text names two deliverables is worth
  re-reading against the code. `FuzzPricingMerge` closes it.

Phases 1, 2, 6 and 9 lost their `code` markers above.

The only remaining PLANNED item that is *code* is full-duplex barge-in, which is **parked rather
than pending** — it needs acoustic echo cancellation, which conflicts with the CGO-free guarantee
(guardrail #8) unless a headset is assumed, and that is a product decision, not a task.

Two rows opened by `/reboot` that are not on any phase: the restart supervisor is
one extra idle process for the life of the session and nobody has measured what
it costs on a small board, and `/reboot` has never been run with Helix as a
**login** shell — which is precisely the case the supervisor exists for, since a
parent that simply exited would end the session.

Everything now open waits on **hardware, API keys, or an owner decision**. Phase 7 is still the
last gate before a release tag, and both of its items (P7.8's metrics run, P7.9's tag) are of that
kind. The honest summary of the project's state is: the plan is built; what remains is proving it
on real devices with real credentials.

### Task-level checkboxes

All task checkboxes inside §6 phase sections are the authoritative task list. Tick them as work
completes and record evidence (test names, metrics, QA logs) in the dev log below.

### Dev log (append-only, newest last)

| Date | Session summary | State left in | Next step |
|------|-----------------|---------------|-----------|
| 2026-08-16 | Full codebase analysis + plan review; this roadmap authored; no code written | `blackBox` clean at `fd34503`, doc at repo root, uncommitted | Phase 0: ratify ADRs, write `docs/threat_model_voice.md`, create compiling package skeletons, commit |
| 2026-08-16 | Roadmap committed (`b1f5302`). Phase 0 complete: baseline verified green (25 pkgs), voice threat model written, 6 skeleton packages compiling+tested, CI coverage confirmed automatic | `97fa983` committed+pushed | Phase 1 implementation |
| 2026-08-16 | Phase 1 complete: `internal/speech` (types, registry w/ failover chains, embedded+override pricing, OpenAI/Deepgram/ElevenLabs/whisper-sidecar/piper adapters, sox/ffmpeg capture), `audio.PlaySpeech` pure-Go WAV/PCM playback, `/voice-setup` `/say` `/listen` `/tts` `/voice-status` + `/help` section, config `speech` section, `providers.HTTPClient.DoRaw`+`RawClient`, keystore speech env-mapping; e2e mock TTS endpoint + 2 speech e2e tests; full suite 31 pkgs green | `0ccd26e` committed | Phase 2 implementation |
| 2026-08-16 | Phase 2 complete: `Agent.HandleInputEvent` (channel stamping + confidence gate + reset), `policy_voice.go` (High-unreachable-by-voice ceiling, deny list, spoken refusals), VoicePrompter (fail-closed, typed-confirmation always refused), agent confirmations rerouted through `commands.AskForConfirmation` seam, `/voice` `/manual` + persisted mode + mic-less graceful fallback, spoken responses via OnSpeak; 18 new voice tests + 2 e2e; full suite 24 pkgs green. Found during testing: hard validation already blocks every known High-risk pattern, so the analyzer-level voice ceiling is defense-in-depth (documented in tests + risk.go comment) | Phase 2 commit pending | Phase 3: wake-word sidecar spike (openWakeWord vs Porcupine licensing) |
| 2026-08-16 | Phase 3 complete: `internal/wakeword` energy detector (default) + openWakeWord-class sidecar client, chunk-scan service with cooldown debounce, wake-only between turns, kill phrases, wake.jsonl metrics; fixture + httptest tested, zero hardware | `3c3c723` committed | Phase 4: headless Agent refactor + daemon |
| 2026-08-16 | Phase 4 complete: `Renderer`/`SlashDispatcher` seams (Agent decoupled from TTY), `session` RingStore + data-only planner context + `/memory`, safe-subset undo journal (`git commit` → soft reset, "undo that"), `helix daemon` runtime + NDJSON IPC (UDS 0600 / Windows loopback token TCP) + `helix remote`, TTY active-session lock (submit refused while foreground session active), connectivity monitor flips `speech.SetOfflineMode` (local-first chain), service installers (launchd/systemd/sc.exe), interaction journal + `diagnostics.Guard`, daemon greeting + config-gated break reminder, `scripts/soak.sh`, e2e `TestE2E_DaemonRemoteStatus`; full suite green (all packages incl. e2e) | Phase 4 commit pending | Manual QA: 72h soak, logout/reboot on 3 OSes; remaining code gaps: per-sidecar `Health()` polling loop (P4.7) and connectivity fallback latency is one poll interval (~30s) not ≤5s |
| 2026-08-16 | Phase 5 complete: `providers.MessagePart` multimodal format (`json:"-"` so text-only wire stays unchanged) + OpenAI content-array / Ollama `images` / Anthropic vision-block adapters, `llava` added to `CapabilitiesFor` vision detection, `ai.RunVisionModel` (fails closed on non-vision models), `internal/vision` ffmpeg single-frame capture (≤1024px JPEG, image2pipe → stdout, memory-only), `/eyes on|off|status` + "turn off your eyes" kill switch + `~/.helix/journal/vision.jsonl` metadata journal, deictic voice routing (`Agent` vision seams); 9 new tests incl. fs-snapshot + capability gate + capture-skip. Full suite green | Phase 5 commit pending | Manual QA (real camera + vision model); P5.5 dedicated `vision.provider` fallback still open |
| 2026-08-16 | Phase 6 complete: `internal/ambient` rule-based analyzer (RMS + hand-rolled radix-2 FFT concentration → silence/loud_noise/alarm_like/music_like), cooldown-gated `AudioMonitorService` + per-category response modes + default responses, `ambient` config section, golden synthetic-fixture tests + fuzz. Full suite green | Phase 6 commit pending | Live wake-stream capture wiring + CPU-budget benchmark remain (hardware/manual) |
| 2026-08-16 | Phase 7 (code items): `input.HybridSource` (TTY+voice multiplex), 3 new fuzz targets (WAV header, ambient analyzer, transcript→policy), ADR-010 (tray = separate opt-in helper, no CGO), `docs/blackbox.md` user guide, Makefile fuzz targets extended. Full suite green | Phase 7 code commit pending | Performance pass, full e2e matrix, supply-chain re-verify, metrics run, `blackbox-v0.1.0` tag (release/owner) |
| 2026-08-16 | Gap-closure pass: P4.7 per-sidecar `Health()` polling loop (`daemon.sidecarHealthLoop`); P5.5 dedicated `vision.provider` routing (`ai.RunVisionModel`/`/eyes`); Phase 6 live wake-stream wiring (`ambient.TeeScanner`+`ChunkMonitor` wired into daemon/voice mode); WAV mono decode helper + `wav_decode_test`; speech/ambient benchmark suites (`go test -bench` — analyzer 26µs/window — see the 2026-08-23 correction: the production chunk path is 623µs, WAV decode 4µs, registry chain 60ns); daemon fuzz target + vision memory e2e. Build+vet+full suite (incl. e2e) green | Full BlackBox diff (Phases 4–7) uncommitted | Owner-gated release steps: performance tuning (streaming partials/buffer sizing), full 3-OS e2e matrix, supply-chain re-verify, §10 metrics run, `blackbox-v0.1.0` tag |
| 2026-08-16 | Release-gap closure: streaming STT (Deepgram WebSocket `StreamingSTTProvider` + `speech.StreamingSTT` + chunked `streamingVoiceTurn` with live interim display + `stt.stream_chunk_ms`), `DecodeWAVPCM16` + mock-WS/adapter/registry tests; Windows/3-OS e2e (`TestDaemonIPCCrossPlatform` over loopback TCP, cross-platform `TestE2E_DaemonRemoteStatus`, e2e CI matrix on Windows, CGO-free CI build step); Ollama Linux installer SHA-256 pinning; TTS `first_byte_ms` budget + `/voice-status` last-latency metric. Added `github.com/coder/websocket` (pure-Go). Build+vet+full suite (incl. e2e) green | Full BlackBox diff (Phases 4–7) uncommitted | Remaining release steps (owner/hardware): P7.2c speech queue tuning + measured latency, P7.8 §10 metrics run, P7.9 `blackbox-v0.1.0` tag; manual QA (real mic/camera, 72h soak) |
| 2026-08-16 | §10 metrics instrumentation: `wakeListenUntilArmed` now returns the wake event so the interactive loop logs **E2E voice-command latency (wake→execution start)** to `~/.helix/metrics/voice.jsonl` at dispatch; `Agent.OnVisionMetric` seam reports **frame-to-insight** latency to `~/.helix/metrics/vision.jsonl` (provider resolved as in `VisionCall`); shared `appendMetricsRecord` helper unifies ambient/wake/voice/vision logs under `~/.helix/metrics/` (0600). Connectivity fallback confirmed at 5s tick (P4.10 ≤5s target) — roadmap note ticked. `TestVisionOnMetricFiresAfterInsight` added. Build+vet+full suite (incl. e2e) green | Full BlackBox diff (Phases 4–7) uncommitted | P7.8 run itself still needs real hardware (real mic for wake→exec, real camera+llava for frame-to-insight); P7.2c measured latency + P7.9 tag remain owner/hardware-gated |
| 2026-08-17 | Voice-loop smoothness pass: `speech.ErrNoSpeech`/`ErrEmptyTranscript` sentinels + `ClipRMS`/`HasSpeech`/`ClipDuration` energy helpers; amplitude gate BEFORE the STT call (dead mic/silent room → no wasted cloud transcription); registry treats empty transcripts as retryable per-provider failures so fallbacks get a chance; `voiceTurnWithRetry` re-arms the mic up to 3× with a "please speak again" prompt instead of silently dumping to typed fallback; `/mictest` self-test (recorder, duration, RMS+dBFS, speech-gate verdict); sox silence floor lowered to 1% (`HELIX_SOX_SILENCE_PCT` override); captured-duration feedback line. Tests: energy helpers + empty-transcript fallback. Build+vet+lint(0 issues)+full suite green | Full BlackBox diff (Phases 4–7) uncommitted | Real-mic QA of retry loop + `/mictest` verdict on macOS/Linux/Windows still manual |
| 2026-08-17 | True hands-free conversation: `/wake on|off|status` command (enable UI with safe defaults: phrase "hey helix", engine energy, preset balanced) + `/help` entry; config fix — `LoadPreferences` now merges the wake_word section field-wise even when STT/TTS providers are unset (previously a wake-only config never loaded) and fills empty fields from `config.WakeWordDefaults()` (the old file's empty section clobbered defaults → broken ""-phrase detector); daemon voice loop smoothed — amplitude gate before STT + spoken "I didn't catch that, please repeat" retry (up to 3 tries) then return to wake-only listening, mirroring the interactive path; `helix remote status` now reports `wake_enabled`/`wake_phrase`/`voice_loop`. Tests: config defaults-merge + custom-phrase-survives. Build+vet+lint(0 issues)+full suite green | Full BlackBox diff (Phases 4–7) uncommitted | Real-mic QA of the daemon wake→turn loop + energy-detector false-positive rate still manual |
| 2026-08-17 | **Edge-device deployment matrix (Phase 10).** Generalized Phase 10 from Pi-5-only to a full Linux edge-device matrix after confirming the core cross-compiles CGO-free to `arm64`/`armv7` (verified builds). New `docs/edge_deployment.md`: build flags per arch; the two Linux gotchas (`audio_cgo`+libasound for on-device TTS output, bubblewrap fallback where Landlock/kernel≥5.13 is absent); cloud/hybrid/local path guidance; per-board notes for Pi 5, Pi 4, **NVIDIA Jetson Nano (1st-gen)**, amd64 mini-PC, generic arm64 SBC, RISC-V. Key finding: Jetson Nano 1st-gen runs the core fine (static binary sidesteps its frozen Ubuntu 18.04/glibc 2.27) but Ollama is unsupported (Maxwell/CUDA 10.2/kernel 4.9) → cloud voice path recommended; kernel 4.9 → no Landlock → install bwrap. Phase 10 tasks + tracker updated | Docs only; no code change | `scripts/edge-setup.sh`, `/doctor` edge section, systemd template, hardware QA (P10.2–10.5) |
| 2026-08-17 | **Living-AI pass (Phases 8/9/11/12 core).** Two parallel audits (voice pipeline + agent/daemon) surfaced ~20 real bugs; fixed the load-bearing ones and built the new capabilities. **Bugs:** daemon never called `ai.InitProviders` (every daemon AI request silently no-op'd → fixed, P11.1); `defer diagnostics.Guard(...)` missing `()` in 2 goroutines (panics went unguarded); double-stop panic (`sync.Once`); phantom wake on closed channel; silent capture hung 80 s (→ `ErrNoSpeech` retry); wake scanner died in a quiet room (retry budget + `NoSilenceStop`); `DetectRecorder` returned "sox" but `RecordClip` ran `rec` (split rec/sox binaries); `/voice-setup` wiped wake+tuning config (field-wise merge) and never persisted the chosen model; VoicePrompter never printed questions (invisible confirmations when TTS down); Deepgram finalized on `is_final` not `speech_final` (mid-utterance truncation → segment accumulation); streaming producer leaked goroutine+HTTP body after ctx cancel (ctx-aware send); undo journal never popped (double-undo rewound an unjournalled commit → `Pop()`); commit journalled for undo even when HEAD didn't move (baseline test failure → `HeadCommit()` guard); ElevenLabs ignored speed; HybridSource leaked on partial start. **New capabilities:** agentic harness (`harness.go`, `executePlanSteps`/`planFirewallExecute` refactor, `/agentic` toggle, ADR-013); Groq STT + Deepgram Aura-2 TTS + Kokoro-local TTS adapters + pricing refresh (ADR-011/012); `SpeakStream` sentence-pipelined TTS (ADR-014); `voiceviz.go` sci-fi voice HUD (ADR-015); persistent gapless `StreamRecorder` replacing per-chunk spawns. New ADRs 011–015; new Phases 8–12 (§6B). ~15 new tests. `go build`+`go vet`+full suite incl. e2e (26 pkgs) green | Full BlackBox diff (Phases 4–12) uncommitted | Hardware/owner-gated: real-key QA (Groq/Kokoro/TTS mic), Pi appliance build (Phase 10), automatic cloud→local LLM failover (P11.2), agentic output-capture (P8.6), barge-in v2 (P12.5) |

| 2026-08-17 | **Phase 11 core complete — Helix keeps thinking offline.** Closed the asymmetry where the speech chain degraded local-first (P4.10) while the planner kept dialing a dead cloud endpoint: Helix could still hear and speak but could no longer think. **P11.4 decided: implement** the `llamacpp` provider (ADR-016) — `internal/providers/llamacpp` over `llama-server`'s OpenAI-compatible API, keyless/local/registered unconditionally, `HELIX_LLAMACPP_URL` + `llm.llamacpp_url` overrides, bare-host URLs normalized to `/v1`; the deciding factor was the Jetson Nano 1st-gen edge-matrix row, where Ollama is unsupported and llama.cpp is the only local-LLM path (deleting the vestiges would have left that board with no offline brain). **P11.2:** `internal/ai/failover.go` — a CLOSED/OPEN/HALF-OPEN circuit breaker resolving the provider for every model call, so failover works in the interactive shell too (no monitor there); `ai.SetOfflineMode` now fires beside `speech.SetOfflineMode` in `watchConnectivity`. Only availability errors count; the local brain is health-checked before every switch (never degrade onto a dead Ollama); switches are spoken + printed + journaled; `UseProvider`/`UseModel` clear breaker state so a user's explicit choice is never silently undone. Follow-on fix found while wiring: `RunPlannerWithRetry` computed its timeouts **once**, so a breaker flip between planner attempts left a CPU-bound local model with a 30 s cloud-sized budget — now per-attempt via `plannerTimeout`. **P11.3:** `daemon.ensureLocalBrainReady` verifies the fallback model at startup (bare-name vs `name:tag` matching); the multi-GB **pull is consent-gated** behind `llm.fallback.ensure_ready` (default false) — verify-and-warn by default, per §12 #1. New `llm` config section, field-wise merged; `enabled` is a `*bool` because it is the project's first default-true setting and a plain bool cannot distinguish "absent" from "explicitly false". Surfaces: `/provider status` offline-fallback line, `helix remote status` `provider`/`model`/`llm_fallback`/`llm_local_mode`. 18 new tests (9 breaker, 5 llamacpp, 4 config, 3 daemon-readiness). `go build` + `go vet` + full suite incl. e2e (27 pkgs) green; golangci-lint reports only 3 findings that predate this change (`takeLast`, `voiceviz.label`, `parseDeepgramTranscript` — all unused, untouched) | Full BlackBox diff (Phases 4–12) uncommitted | P11.5 manual QA (real network cut + real key; a live `llama-server` turn). Then P8.6 agentic output capture, or P10.2/P10.3 edge tooling |

| 2026-08-17 | **P8.6 agentic output capture — the harness can now see what commands printed.** New `internal/commands/capture.go`: `TailBuffer` (fixed-allocation last-N bytes, mutex-guarded because exec copies stdout/stderr on separate goroutines, always reports a full write so truncation never reads as an I/O error and kills the command) and `OutputCapture`; `DirectorySandbox.RunShellCommandCaptured` + `runArgvEnvCapture` tee via `io.MultiWriter` — **the terminal still receives everything**, capture never swallows. `StepObservation` gains `Output`/`OutputTruncated`/`ExitCode`; `observationBlock` renders a sanitized tail per step with an asymmetric budget (25 lines/1500 B for the failing step where the diagnosis is, 6/400 for successful ones — the block is re-sent every iteration, so this is recurring token cost). **Capture is gated on `Agentic`**: assigning a non-`*os.File` to `cmd.Stdout` makes os/exec insert a pipe and the child's isatty check fails (no colors, no progress bars), so the default path keeps inherited descriptors byte for byte and only an opt-in agentic turn pays. **Two findings the tests forced out.** (1) `FuzzSanitizeOutput` (945k execs) found a genuine **sanitizer-ordering bypass**: `authority=` was neutralized *before* control characters were stripped, so `Auth\x00ority=` passed the regex and the later control-char strip *reassembled* `Authority=` inside the prompt — fixed by running character-level cleanup first and token-level neutralization last; failing seed committed to `testdata/fuzz/`. (2) The integration test exposed that **lenient execution made the harness blind**: `RunShellCommand` treats any non-zero exit as success (right for the user — no nagging about `grep` finding nothing), so a failing `go build` produced `OK: true`, `allStepsOK` returned true, and the loop stopped without ever replanning — output capture alone would have delivered nothing. Fixed by recording the true `ExitCode` in the capture and having `allStepsOK`/`observationBlock` judge on it; **user-facing execution semantics unchanged**, only the planner-facing observation regained the truth. Cost: a benign non-zero exit (grep no-match) spends one extra planner iteration, bounded by the step budget — the model now decides whether the goal is met instead of a hardcoded leniency rule. ADR-013 amended with both. 24 new tests (10 capture incl. tee-does-not-swallow + race, 9 sanitizer/observation incl. fence-breakout + multibyte truncation, 5 integration). `go build` + `go vet` + full suite incl. e2e (27 pkgs) green; golangci-lint unchanged at the same 3 pre-existing findings | Full BlackBox diff (Phases 4–12) uncommitted | P8.7 provider-native tool calling, P8.8 streaming token render; or P10.2/P10.3 edge tooling. Manual QA: a real multi-step agentic turn against a live model, confirming self-correction from a captured compiler error |

| 2026-08-17 | **P8.7 provider-native tool calling — the planner stops begging for valid JSON.** The planner protocol was prompt-enforced: a wall of "ABSOLUTE OUTPUT RULES" asking for a bare JSON object, backed by a 3-attempt repair ladder for markdown fences, prose preambles, and truncated braces. Providers with function calling enforce the schema at the API level instead. **Normalized types** in `providers`: `ToolDefinition`, `ToolCall`, `ChatRequest.Tools`/`ToolChoice` (no JSON tags, like `ChatMessage.Parts`, so adapters render their own wire format and non-tool providers are untouched), `StreamChunk.ToolCalls`, `ChatResult` + `CollectChatResult` (`CollectChat` now delegates, text behavior unchanged). **The fiddly part** was `toolCallAccumulator`: providers stream tool calls fragmented — id and function name arrive once on the first frame, argument slices accumulate across later frames keyed by `index`, and parallel calls interleave — so fragments are merged by index (sorted, not arrival order), nameless entries are dropped as truncated streams, and complete calls are emitted only on the terminating frame, so consumers never see partial JSON. `finish_reason:"tool_calls"` is now terminal alongside `"stop"` (some providers omit `[DONE]` after a tool call — treating only `"stop"` as terminal would hang until client timeout). **Implemented in `openai_compatible`, which covers 7 providers at once** (openai/deepseek/kimi/qwen/glm/custom/llamacpp all embed it). **Planner** (`ai/planner_tools.go`): `emit_plan` tool whose JSON Schema mirrors `BuildPlannerPrompt`, with closed enums on tool and intent so a model cannot invent a sixth executor tool; `tool_choice=required` because a chatty model answering in prose is precisely the failure being removed; arguments feed the SAME `ParsePlanFromModelOutput` → `validatePlan` → firewall → risk tiers → sandbox path, so tool output is not trusted one bit more than prompt output (§12 #3 intact — the schema is defense in depth, not a replacement). **Risk posture: fast path, never a new failure mode** — unsupported provider, transport error, no call, wrong tool, or empty arguments each fall through to the untouched ladder, costing at most one round trip. **Capability honesty:** `SupportsToolUse` describes what the ADAPTER can drive, not what the vendor sells — Anthropic and Ollama report false (their own wire formats, P8.7b) rather than burn a wasted round trip on every plan, and `custom`/`llamacpp` are excluded because their support is genuinely undetectable (arbitrary endpoint; llama-server needs `--jinja` plus a capable GGUF). **Composes with P11.2:** `ToolCallingAvailable` queries the provider the breaker would pick, so a session degraded to a local brain correctly stops offering tools. `/provider status` gained a "Planner protocol" line. Follow-up noted, not done: the prompt's JSON-formatting section is now redundant on the native path (harmless, and all semantic tool rules must stay) — trimming it is a separate change. 20 new tests (6 accumulator/capability, 6 adapter wire incl. ordinary requests staying byte-identical, 8 planner). `go build` + `go vet` + full suite incl. e2e (27 pkgs) green; golangci-lint unchanged at the same 3 pre-existing findings | Full BlackBox diff (Phases 4–12) uncommitted | P8.7b Anthropic/Ollama tool wire, P8.8 streaming token render, or P10.2/P10.3 edge tooling. Manual QA: a real OpenAI-key planner turn confirming zero JSON-repair retries |

| 2026-08-17 | **P8.8 streaming token render — Phase 8 complete.** Chat replies now appear as they generate instead of after the whole response buffers. `ai.StreamModel` consumes the provider channel directly, feeding a callback per fragment while still returning the complete text — script promotion and session memory need the whole response, so streaming changes *when bytes are displayed*, not what the caller holds. It keeps the same breaker/interrupt semantics as every other entry point (a test proves streamed failures still feed the P11.2 breaker; without it, streaming would have silently disabled failover on the chat path). `ux.AIStreamWriter` renders live. **Three design calls worth recording.** (1) **Streaming replaces the typewriter rather than composing with it** — `Typewriter` simulates live generation with fixed per-character sleeps, and once real tokens arrive that simulation is strictly worse, adding artificial delay on top of genuine latency; the audible tick (one per chunk, `PlayType` already 10 ms-throttled) and the `[NEURAL_NET]` prefix carry the established character instead. (2) **The spinner stops at the FIRST token**, not at the end — time-to-first-word becomes the provider's real latency rather than the full generation, which is the actual UX win. (3) **`StreamingRenderer` is an optional interface, deliberately NOT part of `Renderer`** — the daemon captures its IPC reply by embedding `HeadlessRenderer` and overriding `PrintAIMessage`, an override Go does **not** dispatch from inside a `HeadlessRenderer` method, so had streaming been added to the base interface the daemon's `submit` would have started returning empty replies silently. Opt-in means headless paths keep the buffered render byte-for-byte; `TestHeadlessRendererDoesNotStream` and `TestDaemonRendererDoesNotStream` pin it. The prefix is deferred to first content so an empty response leaves no orphaned `[NEURAL_NET] →`, and an unstarted stream falls back to `PrintAIMessage`. **Refactor + deliberate behavior change:** the three byte-identical chat-fallback blocks in `planFirewallExecute` became one `Agent.chatFallback` (duplication that streaming would have tripled), and a fallback response containing fenced shell blocks now shows its prose *before* the run-it prompt — previously the model's explanation was discarded in that case, which is worse for an execution decision the user is being asked to approve. 16 new tests (4 stream writer, 7 StreamModel, 5 renderer-seam/daemon guards). PTY e2e suite green unchanged — the proof that interactive behavior did not regress. `go build` + `go vet` + full suite (27 pkgs) green; golangci-lint unchanged at the same 3 pre-existing findings. **Phase 8 is now DONE** (P8.1–8.8); P8.7b (Anthropic/Ollama native tool wire) logged as an optional follow-on | Full BlackBox diff (Phases 4–12) uncommitted | P10.2/P10.3 edge tooling, P12.4/P12.5 (live amplitude feed, barge-in v2), or P8.7b. Manual QA: watch a real streamed reply for first-token latency and tick rhythm |

| 2026-08-17 | **P10.2/P10.3 edge tooling — the appliance can now be provisioned and inspected.** **P10.3** first, because the setup script points at it: new `internal/edge` package collecting the appliance picture, rendered as a `/doctor` "edge appliance" section. It reports platform/arch/board (device-tree + DMI, NUL-trimmed), **build flavor** via new `audio.BackendName`/`audio.SpeechSupported` (which finally expose the `audio_cgo` build tag at runtime), **confinement actually in force** with the bubblewrap fix attached when degraded, recorder presence, each configured **local** sidecar's reachability, the offline-LLM fallback's reachability **and whether its model is pulled** (P11.3's startup check, now visible interactively), and thermals with a throttle verdict. The motivation is that both Linux edge gotchas fail *silently* — a CGO-free binary is structurally mute however TTS is configured, and confinement degrades to none on an old kernel without stopping anything — which on a headless board stays invisible until something important does not happen. Board/thermal/throttle parsing is split into platform-independent halves (`detectBoardFrom`, `readThermalFrom`, `readThrottledFrom`) so synthetic sysfs fixtures cover it on a dev machine; thermal reads take the HOTTEST zone (boards expose CPU/GPU/PMIC and the first is not reliably the throttling one) and discard implausible values. **P10.2** `scripts/edge-setup.sh`: detects arch/board/kernel/package-manager, installs sox + bubblewrap via the system package manager (distro signatures do the integrity work), and offers Ollama behind a **SHA-256-verified** install that **fails closed** on mismatch *or* on having no digest tool at all. It **refuses Ollama on the Jetson Nano 1st-gen** — the one board in the matrix that cannot run it — and points at the cloud voice path plus the llama.cpp escape hatch P11.4 added, with the same Orin carve-out as `IsJetsonNanoFirstGen`. Modes: `--check` (inert), `--dry-run`, `--yes`, `--assume-board=` (exercises a board path without owning that board). Prompts are TTY-gated and EOF-tolerant so a piped/CI run declines rather than hanging or aborting under `set -e` (the v1.0.0 install.sh lesson). **Deliberate deviation from the roadmap sketch:** whisper.cpp / Piper / Kokoro / openWakeWord are NOT auto-installed — no stable per-arch checksummable artifact exists (whisper.cpp builds from source, Kokoro is Docker), so a pinned installer would be security theater that rots; they stay user-managed sidecars (ADR-002) with printed copy-pasteable setup, the same conclusion P7.7 reached. **The test that matters most** is the **pin-drift guard**: the script and `internal/ollama/installer.go` verify the same upstream artifact, and if their pinned checksums diverge one install path trusts a script the other rejects — nothing else would catch that. Also asserted: no `curl|sh` anywhere executable, consent gating, fail-closed verification elements, and `--dry-run` runs of both the Jetson refusal and the Pi 5 non-refusal. `shellcheck` runs as a skippable test and found 3 real issues (two `A && B \|\| C` pseudo-if-then-elses and a malformed `disable` directive), all fixed — the script is shellcheck-clean. 19 new tests; suite now 28 packages. `go build` + `go vet` + full suite green; golangci-lint unchanged at the same 3 pre-existing findings | Full BlackBox diff (Phases 4–12) uncommitted | P10.4 systemd edge template, P10.5 hardware QA (Pi 5 / Jetson Nano / amd64), P12.4/P12.5, or P8.7b |

| 2026-08-17 | **P10.4 systemd edge template — Phase 10 tooling complete.** The Linux unit `helix daemon install` emitted was a five-directive stub; it is now `internal/edge/systemd.go` (testable, not inline `fmt.Sprintf`) with two corrections that are bugs rather than polish. **(1)** `After=network-online.target` was present *without* `Wants=` — a classic systemd footgun, because `After` only ORDERS a unit against a target and does not pull it in. Nothing else requests network-online on a minimal headless image, so the ordering was **silently inert**: the daemon could start before the network existed, while its first two actions are a connectivity probe and (on the cloud voice path) a remote STT/TTS call. **(2)** A `--user` service stops at logout and never starts at boot unless lingering is enabled for the account — on an appliance nobody logs into, that is the difference between "installed" and "actually runs", and nothing previously said so. `helix daemon install` now detects the linger state by reading systemd's marker directory (no `loginctl` dependency, works in containers), warns in yellow with the exact `enable-linger` command when it is off, confirms when on, and reports **unknown** rather than guessing when the marker directory is absent. Also added: `StartLimitIntervalSec`/`StartLimitBurst` so a crash-looping daemon cannot hammer a small board — deliberately in `[Unit]`, where systemd ≥ 230 expects them (under `[Service]` modern systemd logs "Unknown lvalue" and silently ignores them; the oldest board in the matrix, Jetson Nano on Ubuntu 18.04, ships systemd 237); `TimeoutStopSec`, `WorkingDirectory=%h`, `After=sound.target`, and commented `Environment=` examples for the three documented edge knobs. Post-install notes now cover the `audio` group, the silent-CGO-free-build gotcha, `journalctl --user`, and `/doctor`. **Percent escaping is a real hazard here** — systemd treats `%` as a specifier introducer, so a literal percent in a value must be doubled or the unit fails to load; the template threads `%h` and `2%%` through `fmt.Sprintf` correctly and a test pins both. One test initially failed on its own parsing (a naive `strings.Index` matched the literal `[Service]` inside the template's explanatory comment) — fixed by resolving sections from line-start headers while skipping comments, which is what the assertion should have done anyway. 10 new tests; the edge package now carries 29. `go build` + `go vet` + full suite (28 pkgs) green; golangci-lint unchanged at the same 3 pre-existing findings. **Phase 10 code is complete** — only P10.5 hardware QA remains, which needs the boards | Full BlackBox diff (Phases 4–12) uncommitted | P10.5 hardware QA (Pi 5 / Jetson Nano / amd64), P12.4/P12.5 (live amplitude feed, barge-in v2), or P8.7b |

| 2026-08-17 | **P12.4/P12.5 — the HUD tracks the real mic, and speech is genuinely interruptible.** **P12.5** first, because it needed a mechanism that did not exist: `SpeakStream` could only cancel *between* sentences, since `audio.PlaySpeech` blocks until a clip finishes. New `audio.PlaySpeechContext` wraps the decoded source in a `ctxStreamer` that reports "finished" once the context is cancelled — beep *pulls* samples, so ending the stream is the idiomatic stop, and it takes effect at the next buffer (~50 ms) **mid-sentence**, without racing the backend or touching the platform-specific files. `SpeakStream` now derives a cancellable context, publishes it through `speech.StopSpeaking()`/`Speaking()`, and — importantly — registers it with the interrupt manager **inside `SpeakStream`** rather than at each call site, so the interactive shell, the daemon, and ambient responses all gain it at once. That closed a real gap found while wiring it: **Ctrl+C did not stop a spoken reply at all**, because the `OnSpeak` closures never registered their cancel func. `Speak` (`/say`, `remote say`) became context-aware too. A test caught a second real bug: `SpeakStream` returned **nil** on a cancelled context — the producer exits silently and closes the channel, so a barge-in was indistinguishable from a fully-spoken reply; it now returns `ctx.Err()`. **P12.4** `speech.ClipLevel` meters each captured chunk and drives `VoiceViz.SetLevel` in the streaming voice turn. The mapping is **logarithmic (dBFS), not linear** — speech RMS sits around 0.01–0.1, so a linear meter fed by RMS barely leaves the floor, which is precisely the dead-looking waveform this replaces; the meter spans −50 dBFS (quiet room) to −10 dBFS (close talking), and a test asserts normal speech lands mid-meter rather than near zero. The HUD and the interim-transcript display share one terminal row, so the waveform **hands the line over** as soon as real words arrive — before that the meter answers "is the mic live?", after it the text is strictly more informative. **Honest scope limits, both documented rather than papered over:** only the chunked capture paths can be metered (`batchVoiceTurn` calls `RecordClip`, which shells out to sox writing a whole file with no incremental readback, so it keeps the synthetic animation — restructuring the proven fallback path onto `ChunkScanner` is not worth it for an animation); and **mic-triggered** barge-in during playback still needs echo cancellation, or the mic re-triggers on Helix's own voice (the Phase 2 half-duplex constraint) — `StopSpeaking()` is the seam that path will call, while today's working triggers are Ctrl+C and any programmatic caller. Also removed the dead `VoiceViz.label` field while working in that file, dropping golangci-lint from 3 pre-existing findings to 2. 16 new tests (6 barge-in incl. race-safety, 5 level meter, 5 audio cancellation). `go build` + `go vet` + full suite (28 pkgs) green | Full BlackBox diff (Phases 4–12) uncommitted | P12.6 manual QA (HUD readability, first-audio latency, barge-in feel with a real key), P10.5 hardware QA, or P8.7b |

| 2026-08-17 | **P8.7b Anthropic + Ollama native tool calling — Phase 8 fully closed.** P8.7 deliberately reported `false` for both adapters because Helix could not yet drive their wires; this closes that gap rather than leaving the honest-but-limited state. Shared plumbing moved to `internal/providers/tools.go` (exported `ToolCallAccumulator` plus per-wire definition helpers), so three different transports share one reassembly implementation instead of re-deriving — and re-bugging — it per adapter; the openai_compatible and client.go duplicates were folded into it. **Anthropic** needed a genuinely different shape: `tools` is flat with `input_schema` rather than OpenAI's nested `function.parameters` (getting that wrong is a silent 400, so a test asserts the OpenAI envelope is NOT sent), and `tool_choice` is an object whose "you must call a tool" form is `{"type":"any"}`, not the string `"required"`. Its streaming is unrecognizable next to OpenAI's — `content_block_start{type:"tool_use"}` then `input_json_delta` fragments — but both are keyed by an index, which is exactly why the shared accumulator transfers. The provider gained an `endpoint` field purely so the wire could be tested against a stub; it was previously a hardcoded const and therefore untestable. **Ollama** has two quirks worth recording: it exposes **no `tool_choice`**, so a call cannot be forced, only offered — the field is honestly omitted rather than fabricated, and the planner's existing fallback covers the case where the model answers in prose anyway; and its arguments arrive as a JSON **object** where every other provider sends a string, normalized via `json.RawMessage` so the exact bytes pass through without a re-encode round trip. **The most consequential decision was per-MODEL gating for Ollama.** Tool support there is a property of the model's template, not the server: Helix's own default local model is `gemma4:e2b`, and Gemma ships no tool template. A blanket provider-level claim would have made the planner attempt a tool call, receive prose, and fall through to the prompt ladder on *every single plan* — a wasted round trip on precisely the low-powered edge hardware that can least afford one. `ollamaToolModels` allowlists the families that actually have templates. The P8.7 capability test asserted `false` for anthropic/ollama; those expectations were updated because the underlying capability genuinely changed (adapters gained the ability), not to make new code pass — and new cases pin the Gemma/tinyllama exclusions. 10 new tests (5 Anthropic, 5 Ollama) plus updated expectations; suite now 29 packages. `go build` + `go vet` + full suite green; golangci-lint unchanged at 2 pre-existing findings. **Phase 8 is fully complete (P8.1–8.8 + P8.7b)** | Full BlackBox diff (Phases 4–12) uncommitted | Only hardware/owner-gated work remains: P10.5 hardware QA, P12.6 voice QA, P7.2c/P7.8 metrics runs, P7.9 `blackbox-v0.1.0` tag |

| 2026-08-17 | **Lint cleanup — `make lint` green.** The owner's `make lint` surfaced 10 issues my local check had missed: my golangci-lint is **v1.64.8**, theirs is **v2.x**, which folds gosimple/stylecheck into `staticcheck` (enabling the `QF*` quick-fix and `ST*` categories) and drops v1's legacy errcheck exclusions. Compounding it, I had been reading golangci-lint output through `tail`/`grep -c`, so I only saw the last lines and wrongly reported "2 pre-existing findings" three sessions running. Fixed, all mine unless noted: **errcheck** — 13 unchecked `fmt.Fprint(w, …)` calls in the new httptest stubs (only 3 were shown, because golangci-lint caps repeated issues at 3 by default, so the reported list understated it) plus one **pre-existing** unchecked `r.Close()` in `speech/capture.go`; **QF1012** — two `WriteString(fmt.Sprintf(…))` in `harness.go` → checked `fmt.Fprintf(&b, …)`; **QF1001** — a double-negated condition in `renderer_stream_test.go`, plus a second `!(loud > quiet)` in `level_test.go` that the cap had hidden; **ST1012** — `unreachable` error sentinel renamed `errUnreachable`. The two **pre-existing** `unused` findings (`daemonRenderer.takeLast`, `parseDeepgramTranscript`) were genuinely unreferenced dead code and were removed so the target is actually green rather than "green except two". Verified by reproducing v2's default check set under v1 (`staticcheck` with `ST1000/1003/1016/1020-1023` excluded, errcheck exclusions off, issue caps lifted) and proving the config live with a planted violation before trusting the clean result. Build + vet + 29 packages + shellcheck + gofmt all green | Full BlackBox diff uncommitted, ready to commit | Owner: `make lint`, then commit/push to `blackBox`; then the manual-QA tiers |

| 2026-08-17 | **Pinned the lint contract (`.golangci.yml`).** Root-cause fix for the version drift above: the repo had **no** lint config, so `golangci-lint run` meant whatever the local binary defaulted to — v1.64 saw 2 issues, CI and the owner (v2) saw 10. CI already pinned **v2.5.0** (`.github/workflows/ci.yml:41`), so that is now the declared canonical version and the config uses the v2 schema; a v1 binary fails loudly rather than silently linting a different rule set, and the Makefile's failure hint names the exact `go install` line. `linters.default: standard` states the enabled set explicitly so a future release cannot widen or narrow it silently. `staticcheck.checks` is deliberately **not** overridden — v2's default is what CI already enforces and the tree is clean under it; spelling it out would risk enabling the noisy ST1000/ST1003/ST1020-1023 doc-and-naming rules this codebase does not follow, turning the build red for no safety gain. **The load-bearing setting is `max-same-issues: 0` + `max-issues-per-linter: 0`:** golangci-lint caps repeats at 3 by default, which is why 13 unchecked `fmt.Fprint` calls were reported as 3 and a "fix the reported lines" pass would have left the build red. Verified by installing the real v2.5.0 locally: `config verify` passes, the tree reports **0 issues**, and a planted 5-violation probe reports 5/5 with the config versus 3/5 without — the truncation demonstrated directly rather than assumed | Full BlackBox diff uncommitted, ready to commit | Owner: `make lint` (v2.5.0), commit/push to `blackBox`, then the manual-QA tiers |

| 2026-08-17 | **Cross-platform bug sweep from owner QA (Intel + Apple Silicon Macs).** Five real defects, four of them in code this session touched. **(1) `TestDetectRecorderSmoke` failed on BOTH Macs** — `DetectRecorder` returns `"rec"` (its own comment says so; the P12.3 fix split rec from sox because minimal sox packages omit the `rec` symlink) but the test only accepted `sox`/`ffmpeg`. **It passed on the dev machine only because no recorder was installed there — the error branch.** Every machine with sox failed. Fixed by making `recorderNames` the single source of truth for both the detector and the test, plus `TestKnownRecordersMatchRecordClip` pinning detector and runner to one vocabulary; verified by putting a fake `rec` (then `sox`) on PATH to reproduce the exact failing condition. **This is the important lesson: a dev box without optional tooling silently skips the branches that break users.** **(2) `/provider use llamacpp` → "unknown provider"** — P11.4 registered the provider in `ai.InitProviders` (so `/provider status` listed it) but never added it to `setupProvider`'s hardcoded switch, so it was advertised and then rejected. Fixed, and the `default` branch now defers to `ai.HasProvider` so a registered provider can never again be listed-but-unusable; added `setupLlamaCppProvider` (health-probe + start hint, no key, ADR-002) and llama.cpp to the `/setup` menu. **(3) `/voice-setup` invited a paid mistake** — it asked for "Voice id/name" straight after a table whose loudest column is the MODEL, so the obvious answer (`gpt-4o-mini-tts`) was accepted silently and only failed as an HTTP 400 at `/say` time, after "Voice link configured." Now the prompt lists the provider's real voices, warns before accepting an unknown one, and the fallback prompts name valid PROVIDERS up front instead of only in the rejection message. **(4) SQLite `database is locked`** — `busy_timeout` was 3s while a knowledge import holds write transactions for minutes, so a second Helix process failed immediately; raised to 15s (does not make imports concurrent — it stops brief overlap from failing). **(5)** Owner leaked two live API keys into a chat transcript during QA; flagged for immediate rotation. **Cross-platform verification added:** `go build` clean for darwin/amd64+arm64, linux/amd64+arm64+arm(v7)+riscv64, windows/amd64+arm64, and `go vet` (which compiles test files, catching per-OS test breakage) clean for darwin, linux×2, windows. Full suite 29 packages, `make lint` 0 issues | Full BlackBox diff uncommitted | Owner: re-run `make work` on Intel + Silicon; Windows/Linux still need a real run (compile-verified only) |

| 2026-08-17 | **Misdirected-key guard + xAI (Grok) provider.** Both come from one QA incident: an `xai-` key was pasted into Helix's `groq` slot. **Groq (GroqCloud, `api.groq.com`, cheap Whisper STT per ADR-011) and xAI (Grok, `api.x.ai`) are different companies one letter apart**, and the mistake only surfaced later as an auth failure on every transcription. **(B) `providers.MisdirectedKey`** now checks pasted keys at all three entry points (`/setup` chat keys, `/voice-setup` STT and TTS). It is deliberately **negative-only**: it never asserts what a valid key looks like — vendors change formats and a positive rule would start rejecting good keys — it only fires when a prefix unambiguously belongs to somebody else (`sk-ant-`, `xai-`, `gsk_`). A bare `sk-` is **excluded on purpose**: OpenAI, DeepSeek, Kimi and Qwen all use it, so it identifies nothing and would false-alarm on every DeepSeek key. Warn-and-confirm, never a hard block, and `KeyOwnerHint` names the actual Groq/Grok collision with both console URLs rather than just reporting a mismatch. 14 table cases + hint assertions. **(A) `internal/providers/xai`** — Grok over xAI's OpenAI-compatible API, so it is the same thin wrapper as llamacpp: registered in `InitProviders`, `XAI_API_KEY` keystore mapping, `/setup` menu entry, and — since docs.x.ai lists function calling — added to `toolUseProviders`, giving Grok native planner tool calling (P8.7) with no extra wiring. Model context windows added to the catalogue from docs.x.ai (grok-4.6/4.5 500k, 4.3/4.20 1M, grok-build 256k); **without them the 8k default applies and `GetSafeContentLimit` clamps RAG context to ~4k chars**, silently starving a million-token model. Display name is "xAI (Grok)" so the picker shows both words. Verified against the live docs rather than memory. 30 packages green, lint 0 issues, cross-compile spot-checked | Full BlackBox diff uncommitted | Owner: rotate the leaked keys, then `/setup` → xAI with the existing key; a real Groq key from console.groq.com if the cheap STT is still wanted |

| 2026-08-17 | **P7.2c streaming TTS playback — first-audio from ~2,280 ms to ~150 ms.** Owner QA on a real OpenAI key measured `Last TTS latency: 2280ms (budget 800ms)`, and the cause was structural rather than tuning: `adapter_openai_tts` POSTs with `response_format: "wav"` through the buffering `DoRaw`, so the **entire** synthesis completed before one sample reached the speaker. `first_byte_ms` was plumbed and displayed but never acted on. Fixed with an optional `StreamingTTSProvider` (same pattern as `StreamingSTTProvider`): `SynthesizeStream` requests **raw PCM, not WAV** — a container would require parsing a header out of bytes that may not have arrived, whereas PCM's 24 kHz/16-bit/mono is fixed by the API contract and plays from byte one — and `audio.PlaySpeechStream` plays it incrementally. **Three constraints drove the design.** (1) *The speaker callback must never block*: beep pulls samples on the audio goroutine, so a `Stream()` that waited on the network would stall the whole device; a producer goroutine fills a queue and `Stream()` takes only what is already there. (2) *Underruns are worse than latency*: starting on the first byte guarantees gaps, so a **150 ms preroll** — the entire latency cost of streaming — absorbs ordinary jitter, and a mid-stream stall emits silence rather than ending, making a hiccup sound like a pause instead of a truncated sentence. (3) *Only sentence 1 needs streaming*: time-to-first-audio is set by it alone since nothing is playing to hide it behind, so it streams while sentences 2..N keep ADR-014's buffered one-ahead pipeline, whose latency is already hidden. **Risk posture matches P8.7:** any failure before audio plays falls back to the buffered path, so streaming can only be faster, never a new way to be silent; a chain with no streaming-capable provider returns a dedicated `errNoStreamingTTS` sentinel that selects the buffered path quietly rather than surfacing an error. `/say` and daemon `remote say` now route through it too. **Incidental fix:** `PlaySpeechContext` decoded the clip AFTER opening the audio device, so an undecodable clip spun up hardware to discover it was invalid — and under `-race` on macOS that was enough to trip a **pre-existing data race inside oto's CoreAudio driver** (`driver_darwin.go:161`, third-party, unrelated to the clip). Decoding first fixes the ordering and makes `-race` usable on the package. 15 new tests. Build + vet + 30 packages + lint 0 + cross-compile (windows/linux/darwin) green | Full BlackBox diff uncommitted | Owner QA: re-run `/say` and check `/voice-status` — expect the latency line to drop from ~2280ms to a few hundred |

| 2026-08-17 | **Two voice-UX bugs from owner QA — one a P7.2c regression.** **(1) The first sentence was truncated after ~one word**, then playback resumed from sentence 2. Root cause in `beep`, not in Helix's logic: `Resampler.Stream` (resample.go:102) reads its source as `sn, _ := r.s.Stream(r.buf1); if sn < len(r.buf1) { r.end = ... }` — it **discards the ok flag and treats ANY short read as a permanent end-of-stream**. A 24 kHz TTS stream is always resampled to the 44.1 kHz device, so the new streamer's underrun path ("here are 64 samples, more coming") silently marked the stream finished the moment the preroll drained — cutting the sentence to roughly the preroll length. Fixed by inverting the contract: while the stream is live `Stream` now **fills the requested buffer completely**, padding with silence, and a short read is reserved for the true end. Preroll raised 150→250 ms since a pad is audible as a gap and lead is cheaper than a stutter. `TestPCMStreamNeverShortReadsWhileLive` pulls 512-sample buffers (beep's actual `resamplerSingleBufferSize`) and fails on any short read while live, so this cannot regress. **Lesson worth keeping: a `beep.Streamer` may not return partial reads mid-stream, regardless of the ok flag.** **(2) Speech and text were sequential** — `handleResponseStep` called `PrintAIMessage` and only then `speak`, so the voice trailed the screen by a full render (seconds with the typewriter on). Both are presentations of the same text, so they now run concurrently: synthesis starts on a guarded goroutine first (it has network latency to absorb) while printing proceeds on the caller, and the turn waits for speech before returning so the next capture cannot start over the tail of the reply. Non-voice turns keep the original single-call path exactly. Build + vet + 30 packages + lint 0 green | Full BlackBox diff uncommitted | Owner QA: re-run `/voice` — first sentence should be complete, and text + audio should start together |

| 2026-08-17 | **TTS metric now records true time-to-first-audio.** With streaming in place the old measurement became actively misleading: `speakOnceStreamed` timed the whole `PlaySpeechStream` call, which returns when playback *ends* — so `/voice-status` reported the duration of the entire utterance and got WORSE the longer Helix spoke, even though the first word was heard just as fast. Since `first_byte_ms` is a first-audio budget, the number was no longer comparable to it. Fixed with an explicit `audio.StreamPlayback{OnFirstAudio}` hook fired at the instant the preroll is filled and the stream is handed to the speaker — the real first-audio moment, within a few ms — rather than inferring it from call boundaries. The `volume float64` parameter folded into the same options struct (one call site). `lastSpeechStreamed` now records WHICH path produced the number, because the two measure different things and the user should not have to guess: streamed = true first-audio; buffered = full synthesis, which for that path *is* first-audio since nothing plays until it completes. Both stay comparable to the budget. `/voice-status` relabelled to `Last TTS time-to-first-audio: Nms (budget Nms) [streamed|buffered — full synthesis before playback]`. Tests pin the semantics: `TestOnFirstAudioFiresAtPrerollNotAtEnd` fails if the hook fires only after the producer finished — i.e. if it ever regresses to timing the whole utterance — plus an optional-hook test. 30 packages + lint 0 + cross-compile green | Full BlackBox diff uncommitted | Owner QA: `/say` then `/voice-status` — expect a few hundred ms tagged `[streamed]` |
| 2026-08-22 | **Live mode: eight commands became `/blackbox`, and Helix gained initiative.** `/voice`, `/manual`, `/voice-setup`, `/voice-status`, `/wake`, `/say`, `/tts` and `/eyes` were one capability behind eight verbs; they are now subcommands of `/blackbox`, and an old name prints where it went rather than a did-you-mean list. `/blackbox on` opens microphone AND camera together — the Phase 5 `/eyes` opt-in is inverted on purpose, because live mode is a camera consent moment by definition, and the frame invariants (one at a time, memory only, never on disk, metadata-only journal) are unchanged. **New: `cmd/helix/companion.go`**, the first part of the voice stack that is not turn-shaped. It samples the camera on an interval, and the two cost controls are the whole design: a 16×16 luminance fingerprint diffed **in process** decides whether a frame is worth a model call (an unchanged room costs nothing — raw JPEG bytes cannot be compared, since two frames of a motionless scene differ almost everywhere), and the model is asked to return a sentinel when nothing is worth saying, with a cooldown bounding what survives. **Half duplex is what shaped it.** The companion never speaks; it QUEUES, and the main loop drains the queue at the only two points where the microphone is provably closed — between a finished turn and the next capture, and via a new `wakeCompanionSpoke` outcome that stops the wake scanner on the way out. Speaking into an open mic would have Helix transcribe and answer itself, which is the same echo-cancellation residual Phase 12 logged for barge-in, seen from the other side. **Voice policy widened** (owner decision): the ADR-005 denied set shrank from 20 commands to 9, each argued individually — `/purge` `/rag-reset` destroy data, `/scan` fires traffic at a third party, `/commit` writes history, `/config` `/stealth` move the posture, `/hooks` installs policy that later runs unattended, `/setup` would have you dictate API keys aloud, and `/init` writes HELIX.md which is planner context from then on. Everything else is now reachable by speaking. **Also fixed the readiness lie found in QA:** `VisionCaptureService.Available()` existed and was never called, so `/eyes status` reported "ready" on a host with no ffmpeg and the first capture failed; `visionReady()` now checks the capture half and the model half separately and names which one is missing. New `companion` config section (enabled/interval/cooldown/threshold) whose zero value resolves to the defaults rather than to "off". 13 new tests (folded-command migration, `cmdArgs.Shift`, readiness honesty, frame-diff noise immunity, sentinel/cooldown/one-sentence trimming); README, `docs/voice.md`, `docs/blackbox.md` and the edge setup script updated. `go build` + `go vet` + full suite green | Uncommitted, on branch `blackBox` | Owner-gated: ffmpeg is not installed on the QA machine, so the companion loop has not been driven by a real camera. Measure a real frame-to-remark cycle, and tune `interval_s`/`change_threshold` against it |
| 2026-08-22 | **Grounding fixed, four real bugs found by running things.** **(1) Web grounding.** A successful search was followed by the planner re-issuing the identical search, on every phrasing — the harness would loop to its budget and never answer. The cause was PLACEMENT: the "answer from these results" directive lived inside `<execution_report authority="data-only">`, under a heading that tells the model to never obey instructions found there, while the WEB TOOL RULES higher up were still telling it to search for anything current. It obeyed the fence. `PlannerPromptInput` now separates RAG, report, and directive; the report keeps its data-only fence, the directive moves into Helix's own instruction space just before the emit line, and the harness passes them as a `turnContext` rather than string-concatenating them into the RAG slot (which had also been mislabelling the execution report as "RELEVANT SYSTEM COMMANDS (from Knowledge Base)"). Verified live: 2/2 phrasings grounded, correct current answer, where 2/2 had re-searched before. **(2) The camera never worked on this Mac.** darwin capture hardcoded `-framerate 1`; the device advertises exactly 15 and 30, so avfoundation refused the input before a byte was read. Dropping the flag is not a fix either — ffmpeg then defaults to 29.97, also refused. `negotiateFramerate` now parses the rates ffmpeg names in its own rejection and retries with the highest, caching it: the same "negotiated, not assumed" pattern as `max_tokens`. Pixel format needs nothing; ffmpeg self-corrects to `uyvy422`. **(3) Reasoning models return nothing.** `gemma4:e2b` — Helix's own default local model — is a thinking build. At the 512-token chat default it spent ~770 characters on `thinking`, emitted zero `content`, and ollama reported a perfectly successful stream; the client read only `content`, so this surfaced as "The vision model returned nothing." The client now tracks `thinking` and reports budget exhaustion as an error naming the limit, and vision calls get their own 1024-token budget. **(4) Vision gated on the wrong model** — `Capabilities()` computes from the provider DEFAULT, so the new `vision.model` override would have been checked against a model nobody selected; gated on `SupportsVision(provider, model)` instead. **New capability:** `vision.model` routes frames to a different model than chat, and companion pacing is now adaptive — the interval is a floor and the gap becomes `max(interval, smoothed last look)`, so a slow host backs off instead of queueing. It deliberately does NOT speed up on a fast host: a companion is bounded by how often a person wants to be spoken to. **Model evaluation (empirical, not from docs):** moondream is the standard small-VLM recommendation and Ollama's build is unusable for this — given the companion's instruction-style prompt it returns an empty string, given a rephrasing it returns coordinate arrays (`ids: [0.39, 0.3, 0.57, 0.44]`), and it hallucinated flags in an image of three stripes. `gemma4:e2b` stays the recommendation at ~6.5s first token. Also NEW `internal/deps` + first-run stages: a precompiled binary now offers to install sox and ffmpeg during setup, OS-detected across brew/apt/dnf/pacman/zypper/apk/winget/choco, detecting by capability (`rec` satisfies sox) and never emitting a package name it cannot verify. 20 new tests. `go build` + `go vet` + full suite + lint green | Uncommitted, on branch `blackBox` | The companion loop still has not seen a real frame: macOS withholds camera access from the host app here (ffmpeg opens the device, negotiates format, then blocks with no output). Verify in a terminal that has been granted Camera access, then tune `interval_s`/`change_threshold` against a measured frame-to-remark time |
| 2026-08-22 | **QA pass on a real session: intent routing, credential reuse, sidecar endpoints, and a UI system.** **(1) The deictic vision hijack — the big one.** With eyes on, `visionRequested` intercepted any voice utterance containing "this"/"that"/"here" BEFORE the planner saw it, so most of a session went to the camera: "show me the available commands in THIS helix" was answered by a vision model staring at a dark room (it described the Helix *text editor*), "what do we have in THIS directory?" got the furniture, and "…see what's in THIS directory" produced "I don't have shell access" — from a model that has no idea Helix owns a shell. The same transcript proves the fix: every utterance WITHOUT a demonstrative ("can you see me?", "what can you see now?") reached the planner and was routed to the `vision` tool correctly. The heuristic existed because the planner had no way to reach the camera; it does now, so it is deleted along with `isDeictic` and its fuzz target. A model choosing among its own tools beats a substring match on English demonstratives. **(2) Kill phrases were exact-match.** "Excellent. Now switch to manual mode." went to the planner, which asked what to switch to manual mode FOR; only the bare "Manual mode." worked. Now suffix-matched, so a sentence works — but suffix, not substring, because "how do I switch to manual mode?" is a question about the feature. The eyes switch is deliberately looser and MAY over-trigger: a privacy control should fail toward closing the camera. **(3) Credentials were asked for twice.** The AI and speech keystores are separate files (speech works with no LLM), but they are not separate accounts — a user who pasted an OpenAI key on the provider screen was asked for it again three screens later in the same wizard. `adoptAIKeyForSpeech` reuses it for the same vendor only. And `ensureRemoteAPIKey` no longer asks "use the saved key?" — it verifies, and only a key the provider REJECTS prompts for a new one; `keyVerdict` distinguishes rejected from unverifiable so a dropped connection cannot demand a re-paste. **(4) A sidecar could not be a fallback.** `Speech.STT.BaseURL` applies only to the PRIMARY provider, so whisper-local picked as a fallback and moved to a free port had its new URL written into the primary's slot: the probe kept dialling the stale 8080 and reported "did not come up" for a server that had started fine, while the cloud primary was quietly pointed at localhost. Endpoints are now a per-provider map (`Endpoints`), resolved by `sttEndpointFor`/`ttsEndpointFor`. **(5) NEW `internal/shell/panel.go` — one visual language.** /help had a good one (▸ section, │ gutter, closing rule) and nothing else used it, so every other report was a flat stack of `color.Cyan` lines. Extracted into panels, badges, content-measured tables, gutter-aware wrapping and `PadVisible` — the last because `%-9s` counts ANSI escape bytes and pads a coloured cell to nothing, the alignment bug this file exists to stop re-inventing. Applied to `/blackbox status`, the voice-chain report, `/tools` and the endpoint notices. **Verified by looking at it**: a PTY driver captures the real ANSI, a converter renders it to HTML, and the browser screenshots it — which is how the escaping wrap and the wasted badge line were found. The setup wizard screens are NOT converted yet. Also: the spoken-vocabulary report now derives the denied list from the registry instead of a hand-kept copy that had already gone stale, `/setup`'s summary matches its five options, and the first-run banner counts the stages that will actually run. 12 new tests. Build, vet, lint and suite green | Uncommitted, on branch `blackBox` | Convert the setup/wizard screens to panels; the companion loop still has not seen a real camera frame |
| 2026-08-22 | **The visual system reaches the screens people actually meet first.** Setup, live mode and /about were the three surfaces still rendering as flat `color.Cyan` stacks, and setup is the one a fresh install sees before anything else. Two new primitives — `shell.Menu` (numbered choices that carry the REASON to pick one) and `shell.Prompt` (one voice for every question) — plus panels applied to: the provider menu (each option now says "cloud · API key required" or "runs on this machine · no key, no per-call cost", and tags the ones already holding a saved key), the model catalogue (was 25 bare ids and "… and 101 more" — the worst of both, too long to scan and too short to be complete; now capability-tagged rows marking which models can SEE and which support tools, with the default badged), the Ollama local-model screen, the STT/TTS pricing tables (free is rendered as **free** rather than "0.00", which in a column of dollars reads as a rounding error rather than as the entire point of running local), and `/setup` itself, whose menu now reports each stage's CURRENT state — "identity: not set", "ai provider: ollama / gemma4:e2b", "system packages: all present [done]" — so the wizard shows what is finished instead of asking the user to remember. **Live mode got a real entry banner**: which senses just came online and how to get back out, replacing one cyan line that mentioned only the exit. Per-turn output was re-voiced so the three kinds of line are distinguishable at a glance — the transcript echo is a magenta `❯` because it is the user's own words, an unprompted companion remark is a magenta `◉` because nobody asked for it, and "captured 0.8s" (which read like debug output that had escaped) is now a muted turn marker. **/about** gained a THIS INSTALL panel answering the question it could not: the philosophy says what Helix is for and the creation says who built it, but nothing said what THIS copy can do — mind, harness, hearing, sight and confinement now sit between them. **The banner, identity zone, glitch animation, creator text and motto are untouched by explicit request**, and are entirely inside `RenderAbout`; every change is in the caller below it. Also fixed while looking: `/tools`'s e2e expectation still matched the old heading, and `go test ./...` had silently stopped covering `tests/e2e` — the last two "all green" claims did not include it, and the suite is now run as `./... ./tests/...`. Verified throughout by screenshotting real PTY output. 5 new tests. Build, vet, lint and the full suite incl. e2e green | Uncommitted, on branch `blackBox` | The companion loop still has not seen a real camera frame (macOS withholds camera access from the host app here) |
| 2026-08-22 | **Second QA pass: a broken voice chain, a Docker dead end, and Helix finally sounds like itself.** **(1) The endpoint fix from earlier was being thrown away.** whisper-local started fine on a reassigned port 28861, verified, and then every request went to 8080 — the wizard's commit step does `cfg.Speech.STT = sttCfg`, and the freshly-built struct has no `Endpoints`, so the reassignment was wiped between "is answering on port 28861" and "still not answering". The merge comment directly above it already warned about this exact class of bug costing `/wake` its config; `Endpoints` was the next casualty and is now carried across explicitly, with a regression test. **(2) Docker is no longer a dead end.** QA picked kokoro-local as a TTS fallback and was walked through image-pull instructions to `Cannot connect to the Docker daemon` as the final line of voice setup. Sidecar specs gained an `Unmet` precondition — a dependency Helix will NOT resolve for you, distinct from a missing binary it offers to install — and `dockerAvailable()` checks for a daemon that *answers*, not just a binary on PATH, since a dead daemon is exactly the state that failed. Kokoro is now marked `needs docker` in the pricing table (visible before the choice) and refuses early with a pointer to piper-local, which needs only Python. The docker-free guarantee is documented in the README, `docs/voice.md` and `docs/local_runtimes.md`. **(3) The live banner lied about the camera** — eyes were enabled after it printed, so it said "SIGHT • off" and then "Eyes ENABLED" two lines later; the decision now happens before the banner that reports it. **(4) Multi-line errors stopped being mangled**: " — type /blackbox off" was concatenated onto the tail of a shell command inside a chain-failure message. **(5) NEW `internal/agent/persona.go`.** Every prompt in the tree constrained FORMAT and none said who was speaking, so replies came back in the provider's default assistant register — QA got "1 plus 1 equals 2.", gallery-catalogue prose from the camera, and an unasked-for tutorial. The persona is a point of view rather than more rules: Helix shares the machine, so "I ran it" is literally true; it answers first; it has opinions. Wired into the planner (where most spoken replies are born as response steps), the chat fallback, and the camera path. `VoicePersona` is separate and applies only to spoken turns — a test caught the core telling TYPED turns their reply would be read aloud. It shapes tone and never authority, pinned by `TestPersonaShapesToneNotAuthority`. Also: the wizard's own closing line still said `Try: /say Voice link online` (QA dutifully typed it, twice, and got the migration notice), and that plus the last `/wake`/`/manual`/`/voice-status` strings in code, the edge setup script and the docs are gone. 4 new tests. Build, vet, lint and the full suite incl. e2e green | Uncommitted, on branch `blackBox` | The companion loop still has not seen a real camera frame |
| 2026-08-22 | **Fallback tables converted — and the fitter that should have existed first.** The fallback chooser was the last flat screen: five look-alike readiness phrases in one grey column ("yes — server running", "NO — key required", "needs local server") that the reader had to compare by hand. Readiness is the only column that decides the choice, so it is badge-coloured now, and a container-hosted sidecar on a host with no daemon reports **needs docker** with a pointer to piper-local rather than the misleading "not running yet" — the same informed-choice fix as the pricing table, in the other place the choice is made. **Screenshotting caught what the code could not see**: the seven-column pricing table overflowed its panel and wrapped, dropping "★ best value" onto its own line at column zero — the exact failure the panel system exists to prevent, committed by the panel system's own first customer. Fixed twice over: the table lost the raw unit price (the monthly estimate is derived from it and is the number people actually decide on) and merged needs/where into one `requires` column, AND `shell.Table` now **fits itself** to the panel by shaving its widest column, so no future caller can reintroduce the bug. That needed `truncateANSI`, which copies escape sequences verbatim while counting only visible runes — naive slicing either counts escape bytes as content or severs a sequence and bleeds its colour across the rest of the line. A width test written against the no-TTY default (72 columns, stricter than the dev machine's 92) is what proved the table still overflowed an 80-column terminal after the first fix. Also converted: the piper voice-id prompt block and the last two bare `askNumber` prompts, so every question in the wizard now speaks in one voice. 3 new tests. Build, vet, lint and the full suite incl. e2e green | Uncommitted, on branch `blackBox` | Every wizard screen is now on the panel system; the companion loop still has not seen a real camera frame |
| 2026-08-22 | **Documentation audit — three docs were still describing deleted behaviour.** Asked whether the docs were fully updated, the honest answer was no. `docs/voice.md` and `docs/blackbox.md` both still taught the **deictic camera path** as a live feature ("a question that points at something captures one frame", "typed input deliberately does not trigger the camera") — the exact heuristic deleted earlier the same day for hijacking most of a QA session. Both now describe the two explicit doors (`/blackbox look` and the planner's `vision` tool) and say plainly what was removed and why, because a reader who used the old behaviour deserves to know it is gone rather than wonder why it stopped working. **The threat model was materially out of date, which matters more.** V4 read "Vision strictly opt-in (`/blackbox eyes on`, default OFF)" while `/blackbox on` now enables the camera as part of going live. That is a real widening of when a camera can open, and a threat model that does not say so is worse than one that never mentioned it: V4 now records both entry points and marks the second as deliberate, and a new **V4b** covers unattended capture by the companion loop — bounded by live mode, stopped by the same kill phrase, and with the in-process frame diff noted as the control that keeps a still room from being streamed to a provider at all. **`docs/architecture.md` gained the four subsystems built this session** and had no entry for: persona (3c), live mode and the companion (3d), host dependencies (3e), and report rendering (5c) — the last documenting the two load-bearing, non-cosmetic properties, that widths measure visible text because `%-9s` counts escape bytes, and that `Table` fits itself because an overflowing table destroys the alignment it exists for. Also: `docs/blackbox.md` learned that voice setup now runs as part of first boot, that saved keys are verified-and-reused rather than re-requested, that default sidecar ports collide and are reassigned **per provider**, and that "manual mode" is matched at the end of a sentence. The Phase 5 roadmap entries describing deictic routing were left alone — a decision log records what was decided at the time and should not be rewritten. Docs only; build, vet, lint and the full suite incl. e2e re-run green | Uncommitted, on branch `blackBox` | The companion loop still has not seen a real camera frame |
| 2026-08-23 | **The last of the unwritten code: voice log, chain presets, an accuracy measurement — and a bug found on the way.** Asked to finish everything implementable before moving on, which meant §13's three remaining `code` items rather than a phase. **(1) P2.8 voice interaction log**, deferred since Phase 2 to share the Phase 4 journal's machinery — and the deferral was right, because that machinery did not exist: the journal had been described as "rotated" in §7 since the day the roadmap was written and had **no rotation at all**, which on an always-on daemon on a Pi is an unbounded file. New `internal/journal` owns one rotating NDJSON appender (0600 in 0700, 1 MiB × 3 generations, rotate BEFORE the write that would exceed the budget — checking after leaves a quiet log over its limit indefinitely) plus redaction; the daemon journal now delegates to it, public API unchanged, its tests green untouched. The voice log's contract is three negatives: **default absent** (no directory, no file — a nil `*VoiceLog` is a working no-op so no call site can forget a guard), **no audio ever** (§7's "transcripts + audio refs" was wrong from the start — clips are deleted as they are read, so a stored path would point at nothing while still being a liability; a test asserts no entry names one), and **wipeable** (`/purge` already had the target). It records the OUTCOME per utterance — planner, spoken command, kill phrase, refusal — because "heard X" alone cannot distinguish a command that ran from one a policy declined. Redaction truncates on a rune boundary: a severed UTF-8 sequence makes the JSON line unparseable and silently drops the entry, so the audit would lose exactly the over-long utterance someone was looking for. Surfaced as `/blackbox log on|off|status|show`, plus a TRANSCRIPT row in `/blackbox status` — whether speech is being written to disk belongs beside the mic and camera states and had no surface at all. **A policy question fell out of it:** `/blackbox` is VoiceOK because the "manual mode" safety valve lives there, so voice could reach `log on` — switching on a store of everything the microphone hears, which is exactly why ADR-005 denies `/config` and `/stealth`. Resolved with an asymmetry rather than a new deny entry: **voice may reduce what is collected, never increase it.** ADR-005 gains that as a stated principle, since it now governs the camera, live mode and the log alike. **(2) P9.7 chain presets** — three pre-worked answers at the top of `/blackbox setup` (cheapest cloud, lowest latency, fully local), each pre-filling primary + fallback. Deliberately NOT a second code path: presets feed the same key-verify, port-assign and chain-verify steps, because a shortcut would produce a chain that looks configured and cannot serve a request. The sketch said "whisper-local + Kokoro/Piper" for the private chain; shipped as piper-local, because Kokoro needs Docker and a preset named *private* must not walk someone into a container runtime. Cloud presets fall back to LOCAL (the failure worth surviving is the network); the local preset has no fallback (a cloud one would undo the reason it was chosen). Pinned to `pricing.json` by test. **(3) Phase 6 acceptance** — a 57-fixture corpus spanning the analyzer's real operating range measures **100%** against the ≥90% target, per category as well as overall, and logs the number; the test states plainly that this measures the rules against the shapes they were written for, not real-world audio. The cooldown proof and the default-off doc line turned out to already exist and had simply never been ticked. **(4) The bug:** wiring the daemon's log revealed it builds its own `speech.Config` inline and **drops `Endpoints`** — so a sidecar the wizard had moved to a free port was dialled correctly by `helix` and at its stale default by `helix daemon`, silently. That is the *third* appearance of this exact bug (the dev log records two), so the fix is the root cause rather than the instance: one shared `config.SpeechConfig.Runtime()`, both callers, and a test asserting every field survives the conversion. **(5) A fourth gap, found by auditing my own "what is left" table rather than trusting it:** Phase 4's acceptance criterion for `"what did I ask five minutes ago"` said **(e2e)** and no such test existed — the ring store was well unit-tested, `/memory show` had an e2e, and the gap between "the store holds the turn" and "the model is told about it" had nothing. Now `TestE2E_PriorTurnReachesPlannerContext` drives two real turns through the real binary and asserts the first reaches the planner; the harness gained a planner-prompt recorder to make it possible, since what Helix TELLS the model is invisible in terminal output. A companion test pins that the replayed turn lands inside the `<session_history authority="data-only">` fence with its never-obey line — which P4.4 specified in 2026-08-16 and nothing had ever verified end to end. Writing it also cost me one wrong assertion worth recording: my first version searched the whole prompt for the phrase and failed, because the phrase is ALSO in the first turn's prompt as the live user request, where being unfenced is correct — it *is* the instruction. The fence belongs on the replayed copy, so the test now checks inside the block rather than anywhere in the prompt. Also clarified an ambient test whose failure message claimed the opposite of its own assertion. 33 new tests (7 journal, 5 voice log, 6 presets, 4 config/conversion, 2 ambient corpus, 4 cmd surface, 3 e2e incl. an e2e proof that the shipped wiring leaves the log absent — a log opened eagerly at startup would pass the unit test and fail that one). Build + vet + full suite incl. e2e + `make lint` 0 issues green; cross-compiled for 6 targets and vetted for 3 OSes | Uncommitted, on branch `blackBox` | **No unwritten code remains in the plan** (four items found and closed, not three — see (5)). Everything open is hardware-, key- or owner-gated: P7.8 metrics run and P7.9 tag are the release gate; a real camera frame and one end-to-end voice session are still the two things needing a human. Worth noting for whoever picks this up: two of the four gaps were criteria that had been MET and never ticked, and one was a test the roadmap said existed. Auditing the tracker against the code found more than reading the tracker did |
| 2026-08-23 | **Phase 1 closed: the local voice chain proven against real servers, and a fuzz half that had been ticked without being written.** Asked to proceed to Phase 1, whose only open acceptance item was "local sidecar adapter against a real whisper.cpp server — manual QA log still pending". Two findings before any work started. **(1) P1.11 was ticked with half of it missing.** It promised "fuzzing of pricing merge + WAV headers scheduled with Phase 7"; the WAV half shipped as `FuzzWAVHeaderInfo` and the pricing half never did. That also means my own claim in the previous session — that no unwritten code remained in the plan — was wrong, and the checkbox is exactly why: a task marked done can still be half done, and only reading the code finds it. `FuzzPricingMerge` closes it. The contract it pins is **availability**, not merely "does not panic": `~/.helix/pricing.json` is user-authored by design (ADR-006 exists so prices can be corrected without a rebuild), so a malformed override must still yield a usable catalog, because a wizard that cannot render its table cannot configure speech at all. It also asserts every surviving entry stays safe to render and to price — a NaN would print as "$NaN/mo" in a column of dollars, which reads as a bug in Helix rather than as a bad override. 46k execs clean. **(2) The QA was runnable here, not blocked.** This machine already had Homebrew `whisper-cpp`, `sox`, `ffmpeg`, two ggml models and a Piper voice from an earlier setup run, so the item needed no downloads and no hardware I did not have — it had simply never been attempted. Ran it live against a **stock** `whisper-server` and piper's own `http_server`, driving Helix's adapters and registry chain rather than curl: `whisper-local` transcribed "The quick brown fox jumps over the lazy dog." exactly in **167 ms**; `piper-local` returned 83,500 bytes in **103 ms** which Helix's own decoder read as 41,728 non-silent samples at 22,050 Hz mono. Both numbers are in §10 now. **Kept as a repeatable opt-in test rather than a prose QA log** (`internal/speech/live_sidecar_test.go`, `HELIX_LIVE_SIDECAR=1`), because the bug that justified holding this item open is invisible to a mock: both local adapters shipped speaking routes their upstream servers do not serve — whisper.cpp answers at `/inference`, piper at `/` — and every fake in the repo agreed with the fake. It skips loudly when the binary, model or voice is absent (§9 rule 6), so CI reports "not exercised" rather than passing silently, and it asserts route discovery against the stock server including reuse of the discovered route on a second call. Playback is deliberately NOT covered: that needs a device, §9 rule 1 keeps audio hardware out of the suite, and "can Helix decode what the sidecar returned" is the half that has ever actually broken. **One test hardened by its own first run:** the registry-chain case originally spoke "Helix hears me locally" and `base.en` returned "He looks, here's me locally" — the chain worked perfectly and the assertion was one synonym from a false failure, so the audio is now a phrase small models transcribe reliably. Proper nouns are exactly what they get wrong. 5 new tests (4 live + 1 fuzz). Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | Phase 1's only remaining item is audible playback, which needs a speaker and a human ear. The broader gate is unchanged: P7.8 metrics run and P7.9 tag |
| 2026-08-23 | **Phase 2: every box was ticked, and the one line nobody actioned was a real bug.** Asked to proceed to Phase 2, which showed all nine tasks and all six acceptance criteria complete. Three of its own hedges were worth checking, and all three were hiding something. **(1) The Risks line said "speech transcripts always NL-routed — verify classifier behavior with tests", and that verification was never written. The claim was false.** `shell.Classify` decides on the FIRST TOKEN, so any spoken sentence starting with an executable's name was classified as a shell command at confidence 1.00 and executed verbatim. Measured against realistic phrasings, 9 of 9 misrouted: "make a new branch called test" ran as `make a new branch called test`; also "top three biggest directories", "test the code", "history of my commands", "clear the screen". The planner would have produced `git checkout -b test` for the first. **Precision about severity matters here: this was NOT a safety bypass.** The direct path runs `handleShellStep`, so `ValidateAndCleanCommand`, the risk tiers and the ADR-005 Medium cap all applied — guardrail §12 #3 held throughout. It was a correctness bug, and the same *shape* as the deictic camera hijack removed in Phase 13: a pattern match on English intercepting a spoken sentence before the model that could understand it. Resolved the same way — delete the shortcut. `Agent.directShellAllowed` keeps the typed path byte for byte and denies it to voice; ADR-008's "optionally bias per mode" is now mandatory, and §2.3's seam table, which had asserted the false property since it was written, is corrected. The nine sentences are kept as a regression corpus **plus a meta-test that fails if none of them still trips the classifier** — without that, a future classifier change would leave the regression test passing for the wrong reason, asserting a gate on inputs the gate no longer sees. Cost, stated honestly: a spoken "git status" now pays a planner round trip; the deterministic fastpath still runs for voice. **(2) P2.7 was ticked "(partial)" and its other half was the last open carry-over.** Phase 4's session memory had in fact delivered multi-turn clarification — a clarifying question is captured as the turn's `Reply`, so the answer reaches the planner with the question in `<session_history>` — and nobody had verified it. Verifying it exposed two defects on the confidence-gate path: it spoke "could you repeat it?" **without recording that it had asked** (so the repeat arrived looking like a fresh request), and it stored the rejected transcript as ordinary user speech — text the policy had *just refused to act on* becoming authoritative context for twenty turns, so the planner could answer the misheard version of a question nobody asked. `session.Turn` gains an optional `Unreliable` flag (omitempty, so existing session.json files load unchanged) and the planner now reads the turn as `user(voice, not understood)`. Surfacing it in the prompt is the point: a flag the model never sees changes nothing. **(3) P2.1 deferred `TTYSource`/`VoiceSource` "until a second consumer exists". The consumer never came and neither did they** — and `Source`'s doc comment had been promising all three implementations ever since. Following that thread found something bigger: `HybridSource` is the interface's only real implementation and **nothing outside `internal/input` consumes it**, so P7.1's "hybrid mode" is built, unit-tested, and unreachable by any user. Recorded against both P2.1 and P7.1 rather than fixed: wiring it means racing a blocking raw-mode line read against a voice capture and cancelling the loser, which is a change to the interactive loop that the "TUI behavior unchanged" guardrail deliberately makes expensive — an owner call, not a passing plumbing job. **One thing I started and backed out of:** `session.Store` is a second dead contract (declared "the conversation memory contract", referenced by nothing, because `Agent` holds the concrete `*RingStore`). I began making it real, discovered the interface would need `Len`, `Capacity` and `Restore` to satisfy existing callers, and reverted — reshaping production interfaces to enable a test fake is the tail wagging the dog. The tests use a real `RingStore` at a temp path instead, which also exercises the genuine persist-and-load path. The dead contract is reported, not redesigned. 13 new tests (5 routing incl. the corpus meta-test, 4 clarification, plus typed/NL/low-confidence boundaries). Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | Phase 2 has no carry-overs left and no unwritten code. Two owner decisions surfaced: whether hybrid mode gets wired (P7.1) and whether `session.Store`/`input.Source` should become real contracts or be deleted |
| 2026-08-23 | **Phase 3: the deferred display was the release gate's missing half.** Audited Phase 3's four hedges. **(1) P3.5 deferred a `/voice-stats` display with the note "the file is plain JSONL".** True, and not enough: four metrics files had been WRITTEN since Phase 3 and **nothing had ever read one**, which is the practical reason P7.8 — "metrics collection run against the §10 table", one of two items gating the release tag — had no tooling behind it. The data was on disk and the arithmetic was manual. New `internal/metrics` owns paths, field names, parsing and summaries, and the writer in `cmd/helix` now goes through it, because a reader added anywhere else would have re-declared the field names and been free to disagree with the writer — the same dropped-at-the-boundary bug that cost the speech config its `Endpoints` three separate times. **A fifth metric was missing outright:** TTS time-to-first-audio, the one §10 number with a hard millisecond budget, lived only in an atomic in `internal/speech`, so `/blackbox status` could show the last value and it vanished on exit — the directory the roadmap says holds "all §10 numbers so the release run can be audited from one place" never held it. **The report's honesty rules each cost it a number it could have printed:** samples are graded against the cloud or local §10 column by their OWN recorded provider (a blended p50 would be judged against a budget applying to neither half); a p95 appears only above 20 samples, because below that it is the maximum wearing a statistical hat and a release decision should not rest on it; an absent file reads "not measured", never as a pass; and the wake false-positive rate is labelled a PROXY — wakes with no answering turn — because Helix cannot know whether a user meant to wake it, so a real FP rate is not observable from here. **Rendering it against realistic fixtures caught a defect in my own first version:** it printed "meets target" beside a visible 6.80s worst case against a 6.00s budget. True of the p50 and misleading as a release signal, so a typical-passes/worst-fails row now reads **"typical only"**, and a test pins that it never reads as a flat pass. **(2) The Risks line asked to "measure and record % CPU in dev log" and nobody had.** Phase 6 benchmarked the ambient analyzer and the wake detector — which runs on every chunk of a permanently-open microphone — never got the same treatment. Measured: **21.4µs per 1500 ms chunk = 0.0014% duty cycle, zero allocations**, with silence and WAV variants; pinned by a duty-cycle test against a deliberately loose 1% budget, since the point is not the number on this machine but failing if detection ever becomes expensive enough to matter on a Pi sharing the board with a local model. Scope stated in the test: this is DETECTION cost, not capture — sox records in another process. **(3) The between-turn lockout was ticked "proven by construction".** The construction was real and nothing defended it: one convenience call to `speech.Transcribe` inside the scan loop would send every ambient chunk of a quiet room to a cloud provider (threat V1/V6, plus a bill) and no test would have noticed. `TestWakeLoopNeverTranscribes` now walks the package AST and fails on any transcription or synthesis call, with a complement asserting the loop still uses capture — a lockout enforced by the loop not listening is not a lockout. **(4) P3.4's "partial" barge-in** was superseded by P12.5's mid-sentence cancellation; annotated rather than left reading as outstanding. 20 new tests (13 metrics, 6 report wording, 1 duty cycle) plus 3 benchmarks and 2 structural tests. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | P7.8 can now actually be run: use live mode, then `/blackbox stats`. Phase 3's remaining items are a live openWakeWord sidecar and a real FP/hour figure from daily use — both need a human using it, not code |
| 2026-08-23 | **Phase 4: two acceptance criteria needed no hardware, and the one that did could not be evaluated.** Phase 4 had every task ticked and four unchecked acceptance criteria, all filed as "manual". Two of them were not. **(1) "\"undo that\" after a voice-initiated git commit performs soft reset with confirmation"** was fully testable and untested. What existed was `isUndoIntent` string matching and a proof that a FAILED commit is not journalled — the criterion is about the successful path. `TestUndoThatAfterVoiceCommitSoftResets` now builds a real repository in a temp dir, commits through the git step (the thing that journals the reversal), speaks "undo that" on the voice channel, and asserts the confirmation was ASKED, the commit left the log, and the file survived — a *soft* reset must keep the working tree, and nothing had checked that. Three companions cover refusal: a declined confirmation leaves history untouched, a second "undo that" does not rewind a commit that was never journalled (the `Pop()` bug, now pinned at acceptance level rather than only at unit level), and an empty journal asks nothing and changes nothing. Git identity is hermetic (`GIT_CONFIG_GLOBAL=/dev/null`) because the test that used to live in this area once committed to the developer's own repository. **(2) "Network cut → local fallback within 5s, spoken notice heard"** was half testable and wholly untested. The ≤5s is a ticker and "heard" needs ears, but the part that can regress silently — a transition switching BOTH chains, announcing it, and journalling it — had no coverage because the logic sat inline in a loop driven by a real TCP probe. Extracted as `applyConnectivityChange` and covered four ways, including that notices fire **per transition rather than per poll** (flaky wifi must not narrate itself every five seconds) and that a daemon with no `OnSpeak` does not panic, since this runs in a supervised goroutine whose entire job is staying up. Writing it caught a would-be vacuous assertion: `speech.SetOfflineMode` is a silent no-op when `speech.Default()` is nil, so the offline check had to initialize the registry or it would have passed while proving nothing. **(3) The 72h soak criterion says "metrics file evidences it" and no file evidenced anything.** `scripts/soak.sh` wrote `<timestamp> <json>` — so **every line of a file named `metrics.jsonl` was unparseable JSON**, and the only way to check 99.5% over 4320 samples was by eye. Worse, the daemon recorded no liveness signal at all, so uptime was knowable only through an external poller. The daemon now heartbeats every 60s to `uptime.jsonl`, `internal/metrics` computes availability as observed-over-expected — the honest reading of in-band sampling, since a dead daemon writes nothing and downtime IS the absent sample — and detects restarts from the uptime counter falling, which is the only in-band evidence that the process changed. The soak script emits real NDJSON. `/blackbox stats` gained a DAEMON panel showing samples vs expected, availability against 99.5%, restarts, and the **longest gap**, because a percentage cannot distinguish one long outage from hundreds of brief ones and 99.5% of 72 hours is 21 minutes either way. Verified against a synthetic 72h dataset with two injected outages: 4303/4320 = 99.61% passing, 2 restarts, 13m worst gap — which is exactly the case where the percentage alone would have been reassuring and the gap is the real story. 14 new tests (6 undo/connectivity, 4 availability, 4 daemon) plus the soak fix. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green; soak.sh shellcheck-clean and its three line shapes verified as parseable JSON | Uncommitted, on branch `blackBox` | Phase 4's remaining items genuinely need a human and a clock: logout/reboot survival on three OSes, 72 hours of soak (run `scripts/soak.sh`, then read the verdict with `/blackbox stats`), and hearing the offline notice out loud |
| 2026-08-23 | **Phase 5: the blocker was real, and re-testing it disproved the fix that was supposed to make it bearable.** All three open criteria are camera-gated, so the first job was checking whether the blocker still stood rather than assuming — the whisper.cpp precedent being that an assumed blocker was actually an installed binary. This one stood: ffmpeg enumerates "MacBook Air Camera" happily, then delivers nothing, and a raw `-frames:v 1` had to be killed. macOS camera authorization needs a human in System Settings and cannot be granted from a shell. **But the re-test falsified this phase's own claim.** The blocked criterion says a frameless capture "now says so explicitly instead of looking like a hang". Measured, it did neither: **30 seconds**, then `signal: killed: ffmpeg version 9.0.1 Copyright (c) 2000-2026…`. The cause is a one-line ordering assumption — `describeCaptureFailure` returns stderr whenever stderr is non-empty, on the stated theory that an unauthorized camera produces "no stderr at all", and **ffmpeg writes its version banner to stderr on every single run**. The macOS guidance underneath, complete with the System Settings path, had been unreachable dead code since the day it was written. Three fixes, each driven by a measurement rather than a guess. `-hide_banner` on every platform's input args is the root cause. A capture-specific `CaptureDeadline` of 8s, because both call sites pass 30s — correct for a turn, absurd for a device open that either answers in well under a second or never — and thirty seconds of silence IS the hang the line claimed was gone. And then, because the banner fix revealed a *second* masking layer (stderr now held `Selected pixel format (yuv420p) is not supported by the input device`, a warning ffmpeg self-corrects from and continues past), a diagnosis that stops choosing between the two: when the deadline expired, the expiry leads and ffmpeg's words ride along as detail. **This required changing an existing test expectation, and that deserves saying plainly.** `TestCaptureFailureNamesTheLikelyCause` asserted "stderr must win when present". I first inverted the ordering, saw that it would hide a genuine ffmpeg error when both were present, reverted it, and only after the second measurement arrived at the combined form — so the test now asserts that ffmpeg's output SURVIVES into the message while the deadline leads it, plus a new case pinning the original intent where it was right (a healthy context, where ffmpeg's own words really are the cause). Changed on evidence, with the evidence in the comment, not to make new code pass. **One more overstatement fell out of it:** `/blackbox status` reported **watching** on this machine, because ffmpeg-on-PATH and a vision-capable model are each necessary and neither is sufficient — an unauthorized camera satisfies both and delivers nothing forever. The capture service now remembers outcomes and a camera that has failed every attempt without ever succeeding reads **no frames** with the likely cause; "never attempted" is deliberately not a failure, since a fresh session has not looked yet. **And a documentation finding:** P5.1 claims adapter updates for "OpenAI, Anthropic, Gemini, Ollama" — there is no Gemini adapter and never has been; "gemini" exists only as a vision-capable model-name pattern in `catalog.go`. That is the third time a roadmap task has named an implementation that does not exist (after `TTYSource`/`VoiceSource`, and `HybridSource` being built-but-unwired), which makes it a habit worth naming rather than three incidents. 7 new tests (4 outcome tracking, 2 deadline/banner, 1 expectation split). Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | Phase 5 still needs a human: grant the terminal camera access in System Settings, then `/blackbox look` and the companion loop can be QA'd for the first time — and `/blackbox status` will now tell you honestly whether frames are arriving. Frame-to-insight against a CLOUD vision provider needs a key |
| 2026-08-23 | **Phase 6: fully ticked, and two of its claims did not hold.** Phase 6 showed every task and all three acceptance criteria complete — the criteria being ones I closed myself two sessions ago — so the work was auditing the task text against the code. Two things checked out and two did not. **Checked out:** `speech_multi` is genuinely absent rather than a dead constant (P6.1 deferred it honestly, unlike the Gemini adapter Phase 5 named), and ambient IS wired into production — `ambient.Tee` wraps the wake scanner in both the interactive loop and the daemon, so this is not another built-but-unwired case. **(1) P6.3's music ducking is a spec defect, not a missing task.** Three of its four responses ship exactly as written: loud noise "Are you okay?" on a 10-minute cooldown, alarm "sounds like an alarm. Want me to check?" on 5, silence "I lost the sound of your voice. Want me to repeat?" on 2. Music ducking is absent, and as specified it cannot be built: ducking attenuates one signal when another is present, and Helix controls only its own TTS — lowering its own voice while music plays makes it *less* audible, and raising it is not expressible because `audio.PlaySpeech` defines volume as 0..1 and clamps anything outside to 1, so there is no amplification path and no clipping guard to add one safely. The valuable half of the line, "instead of chatter", IS delivered: music maps to no spoken message. Left unimplemented on purpose rather than invented — what a user wants here (speak over it? pause until it stops? nothing?) is a product decision, and guessing one into the audio layer is worse than recording that it is open. **(2) P6.4's "CPU budget test (<5% idle overhead, logged)" was only a benchmark**, whose own comment said it existed "so the CPU budget can be evidenced" — it enabled the check and never performed it, so the percentage was never computed. It also measured the wrong thing: `BenchmarkAnalyzer` analyzes a 1024-sample window (64ms of audio) while production calls `ChunkMonitor.Observe` on a full 1500ms wake chunk, decoding a WAV and padding the FFT to **32768 points**. Measured side by side, **25µs versus 623µs — 25× more** — which means the "26µs/chunk" figure this phase has quoted since Phase 7, including in my own Phase 3 dev-log entry two sessions ago, was a window cost wearing a chunk's label. Both are corrected. **Measured properly: 571µs per 1500ms chunk = 0.0381% duty cycle**, comfortably inside the 5% budget and now asserted by `TestAmbientCPUBudget` instead of left to arithmetic, with a companion pinning that a 500ms chunk also stays inside its own span. **One finding the honest measurement surfaced that the benchmark hid:** 852 KB allocated per chunk in 4 allocations — the padded FFT's two float64 arrays, the magnitude slice, the decoded samples — every 1.5 seconds for as long as live mode runs, about 570 KB/s of garbage. Deliberately not optimized: at 0.038% CPU that is negligible even on a Pi, and threading a reusable scratch buffer through a value-type `Analyzer` shared with the tee'd wake stream would add concurrency risk to fix a problem nothing is observing. Recorded so a future chunk-size change is made with the cost visible, since FFT padding makes it superlinear — doubling the chunk to 3s crosses into a 65536-point transform. 3 new tests, 1 new benchmark. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | Phase 6 has no unwritten code left. The one open question is a product decision, not a task: what Helix should do when it hears music, given it can only change its own voice |
| 2026-08-23 | **Phase 7 minus the tag: the metrics run the release gate was waiting for.** P7.8 asks for a "metrics collection run against the §10 table; gaps documented honestly", and the tooling for it only started existing two sessions ago. The run is now done and written up as a new **§10A**, which accounts for all eleven rows — each with a number or with the specific thing that is missing, because "manual QA" is not a reason and a microphone is. **Three rows newly measured.** *Local STT accuracy*, the row §10 says is "measured by fixture corpus" and for which no corpus existed: a 10-utterance set spanning the phrasings Helix actually receives (including sentences whose first word is a command name, and a spoken kill phrase) synthesized with `say` and put through a stock whisper.cpp via Helix's own adapter — **97.0% word accuracy**, 65/67 words, 8/10 utterances perfect, slowest 133ms, against a ≥90% local floor. *Wake detection rate* — **100%** on 20 speech-level clips with **0 false positives** on 18 room-noise clips, plus a check that the strict/balanced/loose ladder is actually monotonic rather than three names for one threshold. *Noise classification* was measured in the Phase 6 pass (100%, 57 fixtures). **Two scope caveats are load-bearing and are stated in the tests, not just the docs.** The wake figure measures **onset detection, not keyword spotting**: ADR-002 chose the energy detector knowing it fires on "hey helix", on "hello there" and on a dropped mug, so 100% means the detector does its specified job, not that it recognises a phrase — a real keyword number needs the openWakeWord sidecar. And the STT figure is **synthesized clean speech, an upper bound**: one voice, no room, perfect articulation. Of its two errors one is a genuine misrecognition (`called`→`call`) and one is numeral formatting (`twenty`→`20`) which the planner would read correctly, so recognition-level accuracy is 98.5% — reported as 97.0% because normalizing numerals to flatter the number is exactly the move that makes a metrics table worthless. **One row I declined to measure rather than measure badly.** Mode-switch latency (≤1s, "measured by e2e") cannot be timed here: entering live mode requires a recorder plus an STT chain, so there is no honest way to time a switch that cannot happen, and timing the mic-less *refusal* instead would be a different number wearing this row's name. Recorded as needing a mic. Same discipline on TTS cloud first-audio: the only number anyone ever measured is 2,280ms against the buffered path that P7.2c deleted, so quoting it would defame code that no longer exists and quoting the expected ~150ms would be inventing a measurement. **Writing the scorer caught my own error**, worth recording because it is the reason the scorer has tests at all: I asserted that reversing four distinct words costs two edits. It costs four — Levenshtein can preserve at most one alignment — and the implementation was right. A wrong scorer would have made the headline accuracy figure meaningless in either direction. **Net for the release gate:** four rows measured and passing, one (frame-to-insight) honestly missing its cloud target with a local best-effort number, six waiting on hardware, keys, a human or a clock, and **no row blocked on unwritten code**. P7.9, the tag, is the only Phase 7 item left and is owner-gated by ADR-009 — deliberately not done here. 6 new tests. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | The two measurements that would most change the picture are both microphone rows — wake→exec latency and mode-switch latency — because they time the loop a user actually feels. Everything else on the gate is a key, an ear, or 72 hours |
| 2026-08-24 | **Sesame CSM-1B, and the context that makes it worth having.** Owner asked to integrate CSM-1B locally with no Python, working on a 3080 laptop, an M4 Air and a 2019 Intel MBP. Research first, because the marketing and the release differ: Sesame's blog describes a full conversational system, but the open weights are the speech GENERATOR — the model card says it "cannot generate text" — so it is a `TTSProvider` and the planner still decides what to say. The reference runtime is PyTorch, so Helix uses **csm.rs** (Rust/candle, CUDA/Metal/Accelerate/MKL, OpenAI-shaped `/v1/audio/speech`), which keeps ADR-001/002 intact: external local HTTP service, no container runtime, nothing linked into the CGO-free binary. Ratified as **ADR-017**. Port defaults to **28195** rather than csm.rs's 8080, because whisper.cpp and llama.cpp both claim 8080 and the person running a local chain is precisely the person who wants CSM. It is the first sidecar Helix declines to install (the compute backend is a compile-time choice; picking one hands a 3080 owner a CPU build) *and* declines to fetch weights for (`sesame/csm-1b` is gated). **The integration's real work was context.** CSM "sounds best when provided with context" and conditions on prior turns as (speaker, text, audio); synthesizing each sentence cold leaves its distinguishing capability unused. Helix now assembles and sends that — and the privacy design IS the feature, because context needs prior AUDIO while the standing guarantee is that clips are deleted as soon as they are read. Retention is memory-only (a test parses the file's imports and fails if it ever gains `os` or `net`), bounded twice by turn count and bytes, scoped to live mode, and off by default. Recorded as **V5b**. **Reading csm.rs's source corrected an assumption that mattered:** `SpeechRequest` derives `Deserialize` WITHOUT `deny_unknown_fields`, so serde ignores unknown fields and an unpatched server *accepts a context field and silently drops it*. The request succeeds, nothing is conditioned on, and that is indistinguishable from success — the worst failure mode for a feature whose value is subjective. My first design assumed rejection and would never have fired. So the patch returns `X-CSM-Context-Segments` and Helix reads it: absent header means "ignored", and status says so rather than claiming prosody it is not getting. **The patch was written, applied and RUN, not proposed.** `docs/csm-context.patch` against csm.rs@facfd06, built `--features metal`, verified against the **public, ungated** `cartesia/sesame-csm-1b-gguf` weights — so it needed no Hugging Face token at all: plain synthesis 200 with 0 segments, context-with-audio 200 with **2 segments**, text-only 1, malformed base64 a clean 400 before streaming, and through Helix's own adapter **context HONORED**. The audio-bearing case is the one that matters — it exercises Mimi ENCODING on the server, the half of the patch never previously run. Reading the source first was worth it: three drafted assumptions were wrong (no `dtype` field on Generator, `IndexOp` unimported, and — the substantive one — mimi is configured with HALF the model's codebooks while `decode_frames` reads `frame[..half]`, so encode must mirror that asymmetry and zero-fill the upper half). A blind patch would have compiled and produced garbage codes. **Measured, and the number is disappointing in a useful way: RTF 1.69× on the M4 Air** — 11.0s of audio in 18.7s, slower than playback, because csm.rs logs `Using device: Cpu for generation` for the quantized GGUF path *regardless of the metal feature*; Metal accelerates only the full-precision gated weights. My earlier "near real-time" estimate for that machine was too optimistic and the docs now carry the measurement. CSM is a discrete-GPU capability; the 3080 is the machine for it and Piper stays the default local voice. **An unrelated bug fell out of the weights download:** three `llamacpp` `CachedModels` tests override `LLAMA_CACHE`/`HF_HUB_CACHE`/`HF_HOME` but `CacheDirs` also reads `os.UserHomeDir()` unconditionally, so they scanned the developer's REAL cache and passed only on machines holding no GGUF — the same shape as the recorder test that passed only because sox was not installed. Fixed with an `isolateCaches` helper. 25 new tests. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | CSM is unverified on the two machines that matter most for it: the RTX 3080 (CUDA, full-precision weights — the configuration the ~0.8× reference figure describes) and the Intel MBP (CPU-only, expected worse than the M4's 1.69×). `make live-csm` measures both. Upstreaming `docs/csm-context.patch` to csm.rs would make context work without a local fork |
| 2026-08-25 | **A UI polish pass that turned into a staleness audit.** Asked to polish anything stale or flat across the interface. Most of it was already good; what was not was **guidance pointing at commands that no longer exist**. Seven user-facing strings still told people to run `/wake on`, `/say <text>`, `/tts on|off` and `/voice-status` — all eight of those verbs were folded into `/blackbox` and are gone from the registry, so Helix was instructing users to type commands it would then answer with "folded into /blackbox". A sweep of every `/command` appearing in a Go string literal, checked against the registry, confirms the migration map is now the only place the old names survive. **Two functions written for status output had no caller.** `speech.ConversationStats` and `csmTTS.ContextStatus` were both added with the CSM work explicitly "for status output", and neither was ever wired — `ContextStatus` was not even reachable, being a method on an unexported type returned as a `TTSProvider`. So the entire point of the `X-CSM-Context-Segments` design — detecting a sidecar that silently drops context and *saying so* — reached nobody, while `docs/voice.md` already claimed `/blackbox status` reported it. New `speech.ContextCapable` interface (with a compile-time assertion, since structural satisfaction means a rename would silently downgrade the row) plus `ConversationReport`, surfaced as a **CONTEXT row** distinguishing *conditioning* / *not applied* / *retained, unused* / *ready*. **A promise the status panel never kept:** `blackBoxUsage` has advertised "hearing, sight, speech, wake, companion" since the command was created, and wake was the one state it never printed — it lived only behind `/blackbox wake status`. Added a WAKE row rather than downgrading the text. **Rendering the panels found a layout bug that reading them could not:** `shell.KV` neither wraps nor truncates, so an over-wide value wraps at the TERMINAL edge and its tail restarts at column zero, escaping the gutter — the exact defect `PanelWrap` exists to prevent for prose. Five of the seven new CONTEXT branches did this, and so did the pre-existing camera "no frames" line at **95 columns into a 74-column row**. Fixed by making `blackBoxContextLine` a pure function of its report so every branch is testable, then adding a width test computed for the NARROWEST panel; it is verified to fail on the old strings. **A menu bug that has shipped in every preset list Helix has ever drawn:** `presetMenuItems` assigned `Tag = "recommended"` and then let `needsKey()` overwrite it, so the recommendation never rendered — while its endorsement COLOUR stayed on, painting the caution "needs a key" green. The tag now combines both, and the test that was supposed to catch this (it asserted only `Tag != ""`) now asserts what it says it does. Also: `printWakeStatus` was the last status surface still rendering as bare `Printf` — rebuilt as a panel, and while doing so it turned out to print the wake PHRASE unconditionally, which for the default `energy` engine is the same false promise `wakeBannerLines` was corrected for, since that detector scores RMS and cannot match words. It now says a stored phrase is unused. `/blackbox tts off` reported a disable in green; `/say`'s `[voice]` prefix and `spoken (...)` line predated the design system. **The documentation sweep found the same defect class in prose.** All twelve `.md` files audited, not only the ones the code touched. `voice.md` said wake-only listening lasts "until the wake **phrase** fires", `blackbox.md` said the shell "listens for the phrase", `README.md` described wake as a "hands-free wake phrase" — and so did `blackBoxUsage` itself. All four describe a capability the default `energy` engine does not have, which is the same false promise `wakeBannerLines` was corrected for in code and `printWakeStatus` in this pass: the doc had simply never been corrected with it. Now "wake event", with the engine caveat where a reader meets it. Also: `architecture.md` documented the context store but not `ContextCapable`/`ConversationReport`, the surface that makes its honesty property observable; `SECURITY.md` and `threat_model_voice.md` (V5b) listed retention's controls without noting it is now visible while active, including the *retained, unused* state; `local_runtimes.md` gained a "telling conditioning from silence" table of the six `CONTEXT` states and the reason a `5xx` is deliberately not treated as a refusal; `blackbox.md` and `edge_deployment.md` both described `/blackbox status` by a row list that predated three rows; `RELEASE_NOTES.md` gained an Interface section, since v1.5.0 is about to be tagged and the terminal UI is the product surface. `harness.md` and `threat_model.md` are genuinely unaffected — they reference voice only as *policy* (Voice Risk Policy, routing, the tool table), none of which changed — checked rather than assumed. 3 new tests (7 CONTEXT states, the ignored-vs-honored distinction, usage alignment), 1 tightened, 2 e2e expectations updated for deliberately changed output. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | The width constraint was enforced only for the CONTEXT row; `shell.KV` still overflowed for every other caller. **Closed in the next entry** |
| 2026-08-25 | **`shell.KV` wraps, so no panel primitive can leave its frame.** The previous entry closed a status-row overflow by shortening the strings, and recorded the real fix as an open gap: `KV` emitted one line whatever its length, so every one of its 44 call sites was implicitly responsible for keeping values short enough for a width it cannot see — `panelWidth()` adapts to the terminal between 52 and 92 columns, so "short enough" is not even a fixed number. Nothing enforced that rule and several callers broke it. Now `KV` wraps at word boundaries and hangs continuation lines under the value column, joining `Table` (which shaves its widest column) and `PanelWrap` (which wraps prose) so that **every** primitive handles its own overflow. **Colour-aware in both directions, which is what makes it doable in the primitive rather than the caller.** Values arrive already coloured, so wrapping has to (1) never count escape bytes toward a width — otherwise a heavily coloured value wraps earlier than the same text plain, which a test pins directly — (2) never sever a sequence mid-way, and (3) carry the active colour across a break, closing it at the end of one line and reopening it on the next; without the reopen a wrapped value renders coloured then plain, and without the close it bleeds into the gutter glyph beneath. Implemented by decomposing the string into cells of `{preceding escapes, rune, visible width}`, which makes wrapping pure index arithmetic and colour a post-pass. Uses `runewidth.RuneWidth`, so a double-width CJK rune counts as two. **The compatibility property is the one that made this safe to land:** a value that already fits is returned byte for byte unchanged, asserted against the exact pre-wrap output, so every panel that rendered correctly before renders identically now and the diff is confined to lines that were already broken. **What it bought back:** the previous entry had trimmed real information out of two messages purely to fit — the camera's "no frames" line lost *why* (macOS returns no error, it just delivers nothing forever) and the CONTEXT "not applied" line lost which provider silently discarded the context. Both restored, and they now wrap to a clean second line instead of escaping the panel. The width test was reframed rather than deleted: overflow is no longer a correctness bug, so it now asserts that **everyday** states fit one line while diagnostic states may take a second but never a third — a summary panel whose every row wraps has stopped being a summary. 7 new tests (fitting values byte-identical, wrapping inside the panel, hanging indent, text preserved across breaks, hard-break of an unbreakable token, colour carried, escapes not counted, termination at absurd widths). Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | `PanelWrap` still measures with `len()` rather than visible width. **Closed in the next entry, where measuring it showed the estimate here was wrong in both directions** |
| 2026-08-25 | **`PanelWrap` too — and the estimate I recorded for it was wrong twice over.** The previous entry noted `PanelWrap` measures with `len()` rather than visible width and judged it "harmless: it only ever wraps EARLY, never past the frame". Writing the test that would prove it disproved both halves. **(1) Not slight.** `len()` counts a multi-byte rune once per byte, and this codebase's panels are built from `·`, `—` and `→` — one column each, three bytes each. Forty two-column words of `——` wrapped into **4 lines where the identical-width ASCII took 2**: double the lines, not a column here or there. **(2) Not always early.** A word longer than the limit has no break point, so the loop emitted it as its own line and moved on — content straight past the frame. A URL in an endpoint-conflict note rendered **188 columns into a 74-column panel**, twice as far out as the camera line that started this whole thread, and reachable through `c.Describe()`, sidecar `reason`/`why` diagnostics and any path or endpoint in prose. So the "harmless" case was the one already breaking the invariant PanelWrap exists to hold. Both fixed: measurement moved to `visibleWidth` (which strips ANSI and uses `runewidth`, so a double-width CJK rune counts as two), and an over-long word is now split through the same `wrapANSI` cell machinery `KV` uses rather than being waved through. `strings.Fields` semantics and the blank-input-returns-nil contract are unchanged, so no existing caller shifts. Both defects are pinned by tests verified to fail against the previous implementation — the byte-measurement one by comparing wrap output for two strings of equal COLUMN width and different byte length, which is the assertion the original code could never have passed. **The lesson is the one worth keeping:** I recorded that gap as a known-and-harmless in a notes column, which reads as due diligence, and it was a guess wearing the costume of a measurement. The panel primitives are now the third place in this project where reading the code produced a confident wrong answer that rendering or running it corrected. 2 new tests. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | `truncateANSI` is the last width-sensitive primitive left, measured rather than estimated: `truncateANSI(cjk, 15)` returns **29 columns**. **Closed in the next entry** |
| 2026-08-27 | **The install I shipped could not have run, and it made a security claim false.** Asked to update the docs for the Piper work, the audit found two files I had skipped — and one of them mattered. **`threat_model_voice.md` V8 says Helix's sidecar installers "pin versions and publish checksums"**, and the Ollama installer already refuses to run a script whose SHA-256 does not match. My new piper download was `curl -fL <url> | tar -xz` — no pin, no checksum, no verification of ~26 MB of executable payload that is then RUN. Writing the doc update is what surfaced it: the sentence I was about to leave in place had become false. **Then a worse problem underneath it.** Fixing the checksum in the shell command led me to check the runner, and `runVisibleCommand` splits with `strings.Fields` and execs directly — **there is no shell**. Every existing InstallCmd is one executable with flat arguments. My `curl && shasum -c && tar` pipeline would have been split on spaces and handed to `mkdir` as arguments; it could never have worked, and it would have failed in front of a new user during setup. The `<<<` herestring in it was bash-only on top of that. So the install moved into Go: stream the download, hash it, compare against a pinned digest, refuse and delete on mismatch, then extract with the top level stripped and the executable bit preserved — plus a tar-slip guard, because an archive entry named `../../.ssh/authorized_keys` is cheap to refuse and this archive comes off the network. Digests obtained by downloading and hashing all four shipped archives (GitHub publishes no digest field for them) and **verified against a fresh download** afterwards. Docs: V8 now describes the concrete case rather than a general intention, mirrored into §5 to keep the two threat tables at parity; `blackbox.md`'s endpoint table said Piper listens on `127.0.0.1:5000`, which the native path does not — there is no port. 3 new tests, and the lesson is the one worth keeping: **writing the documentation is a review**. The commit message I had already drafted claimed a verified install that could not execute a single one of its steps | Uncommitted, on branch `blackBox` | Nothing here is exercised end to end on Linux or Windows: the extraction and checksum are unit-tested and the digests confirmed against real downloads, but no CI job actually installs and runs piper on those platforms |
| 2026-08-27 | **Piper without Python, and the measurement that rewrote the design twice.** Owner asked whether Helix depends on Python and what happens without it. Audit: the core has **zero** Python — CGO-free Go, native Ollama/llama.cpp/whisper.cpp, Rust for CSM — and exactly one optional component needed an interpreter, `piper-local`. Two things followed. The docs contradicted themselves (`architecture.md` claimed a "no-Python rule" that piper violated), and on a machine with **no Python at all** setup offered to run `python3 -m pip install --user piper-tts flask` — a command beginning with the thing that is missing, the same walked-into-a-dead-end failure `Unmet` exists to prevent. **First design was wrong, and measuring caught it.** I built a native adapter that shells out per synthesis, then benchmarked before shipping: one process serving five utterances costs **128 ms each**; five separate spawns cost **513 ms each**. Breaking it down — bare python3 startup 25 ms, `import piper` 100 ms, and the remaining **~460 ms is model load**. So Python was never the expensive part, and a per-call CLI is a **4× regression against the HTTP server it replaces**, in *any* language. The owner's constraint was "no slower performance", and my first cut would have quietly failed it behind a "no Python!" headline. **Second design: a persistent process** holding the model resident. Measured end to end against a real piper: 800 ms for the first utterance, then **66 ms and 55 ms** — roughly **2× faster than the 103 ms server**, because there is no HTTP hop either. Framing is by filesystem, not log parsing: piper announces finished files on stderr but the C++ binary and the Python module word it differently, and only the Python one is testable on this machine, so the session spawns with `--output-dir` and watches for a file that was not there before. It also waits for the file size to settle — a WAV appears when piper *creates* it, not when it finishes writing, and reading on first sight returns a fraction of the sentence. **macOS gets none of it, and that is upstream's.** Both macOS archives in `2023.11.14-2` — the last release shipping standalone binaries at all — contain the libonnxruntime `.dSYM` and **no `.dylib`**; the extracted binary dies on `dyld: Library not loaded: @rpath/libespeak-ng.1.dylib`. Verified by downloading and running it. Linux carries the `.so` files and Windows the four DLLs; only macOS is broken, the successor project ships **Python wheels only**, and there is no Homebrew formula. So macOS keeps the Python server and is told why rather than fetching 19 MB that cannot start. **Edge, because the owner asked about Jetson and Pi:** architectures are fully covered (`aarch64`, `armv7l`) and **glibc is a non-issue** — the aarch64 build needs only `GLIBC_2.17` (2012). The gate is `libstdc++`: `libpiper_phonemize.so` imports `GLIBCXX_3.4.26` (GCC 9) and libstdc++ is not bundled, so **Pi OS Buster and Jetson Nano JetPack 4.x (Ubuntu 18.04, GCC 7.5) fail** while Bullseye and Bookworm work. Helix probes it before offering the 50 MB download. Consistent with §4's existing guidance rather than a new limit — the Nano was already the cloud-path board. The persistent design helps edge *more*, since model load is what scales with CPU. 9 new tests including an opt-in live one that asserts the **warm-model speedup itself**: if later utterances are not dramatically cheaper than the first, the process is being restarted and the native path has silently decayed back into the per-call CLI it exists to replace. Also fixed three pre-existing `QF1012` lint hits that a golangci-lint update surfaced mid-session. Build + vet + gofmt + full suite + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | The native path is unverified on real Linux/Windows hardware — the live test runs through a `python3 -m piper` shim, which exercises the framing but not the actual binary. Nothing here changes macOS, where the Python server remains the only working Piper |
| 2026-08-26 | **The last three flat screens, and a flake I had twice dismissed.** `/cost`, `/context` and `/memory` converted, finishing the set. **`/cost` was not merely flat — it was 92 columns wide** with a hand-padded eight-column layout and a hardcoded rule beneath it: wider than the panel, wider than an 80-column terminal, so it wrapped at the edge and destroyed the grid it existed to be. `shell.Table` self-fits, so converting fixed the overflow as a side effect. Provider and model now share a cell, and a zero in the FAIL column is muted while a non-zero gets a warning badge — a column of identically-coloured zeroes reads as data, and the only number worth noticing there is one that is not zero. `/memory` gained the fact it never stated: that the turns shown are **replayed to the planner as zero-authority data**, which is the whole reason the screen matters. **The flake was not a flake.** `TestE2E_ManualModeSafetyValve` failed twice in this session; both times I re-ran it, saw green, and wrote it off as unrelated because it runs `ls`. Reading the failure instead showed a genuine race the harness's own doc comment describes verbatim: `Expect` matches the FIRST occurrence in the accumulated buffer, so a second command printing the SAME marker matches the previous command's output and returns while this one is still running. The test sent `/blackbox off` twice and waited for "Already in keyboard mode" both times, so the second wait was satisfied by the first command and the test raced ahead to an `ls` it intermittently lost. Fixed with `SendExpect`, which exists for exactly this and was already documented as the remedy. Then checked whether the pattern was systemic rather than assuming: a script over every e2e test function found **21** `WriteLine`+`Expect` pairs but only this one repeating a marker within a test — the rest wait on a first occurrence and are safe. **Five e2e expectations updated for renamed output, and the fourth one taught the lesson again**: I fixed "No model calls yet" in one file, the suite failed on the same string in another, and only then did I sweep the tree — the same one-at-a-time mistake recorded two entries ago. The sweep found the remaining occurrence plus a fifth in `spokenCost`, which is correctly capitalised because it is spoken aloud, not printed. 3 screens converted, 1 latent test race fixed, 5 e2e expectations updated. Build + vet + gofmt + full suite + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | Every status and report screen in `cmd/helix` now renders through the shared primitives |
| 2026-08-26 | **The last three flat screens, and a fallback that fell back to itself.** Owner screenshotted `/status`, `/rag-status` and `/knowledge-status` still rendering as cyan stacks with box-drawing pseudo-tree glyphs, next to the now-panelized `/provider-status`. All three converted. **`/status` was the worst and needed more than a repaint:** it printed twelve rows of equal weight in which "Stealth Mode: DISABLED" and "Typewrite-All: DISABLED" sat beside the two lines that actually decide how much happens without being asked — approval posture and the agentic harness. Those now open the panel, and the four on/off switches collapsed into a single TOGGLES row that names only what is ON ("all at defaults" when nothing is), because four lines of DISABLED is what made the screen unreadable in the first place. **A real logic bug fell out of the same screenshot:** `/provider-status` read `OFFLINE  armed — will switch to ollama if ollama fails`. Choosing Ollama at first run — the ordinary outcome of picking the local option — makes the primary and the fallback the same provider, so `FailoverStatus` was describing a breaker that protects nothing. That is not a degradation path, it is a sentence. It now says "not applicable — ollama is already the local provider", pinned by a test that reproduces the screenshot's exact string against the old code. Worth noting this was only visible BECAUSE `/provider-status` had been panelized the day before: the same text was in the old flat output and nobody read it. **One implementation note recorded because it nearly shipped:** the knowledge summary was first written with an embedded `\n` inside a KV value. KV wraps its own value and hangs the continuation under the value column, but a raw newline bypasses that entirely and escapes the gutter — the exact defect the wrapping was added to prevent, reintroduced through the one door it does not cover. Caught by rendering it. 1 new test, 3 screens converted. Build + vet + gofmt + full suite + `make lint` 0 issues + `make e2e` green | Uncommitted, on branch `blackBox` | `/cost`, `/context` and `/memory` still render flat. They are short and do not overflow, but they are now the only ones left |
| 2026-08-26 | **A failed download ejected the user from the shell.** First run on a clean machine: owner picked Ollama, accepted the offered `gemma4:e2b`, and Ollama's registry answered `503: upstream connect error or disconnect/reset before headers. reset reason: connection timeout`. Helix printed **"Setup failed:"** and **exited to the zsh prompt.** Two defects, and the second is the serious one. **(1) The error was raw proxy noise.** Ollama streams the registry's error text straight through, and the registry sits behind a proxy that speaks proxy. Every word of that line is true and none of it says what happened (the registry is having a bad minute), whose fault it is (nobody's), or what to do — and it reads like Helix broke, so the next thing a new user does is file a bug or give up. New `ollama.DiagnosePull` classifies registry-down / no-such-tag / daemon-not-running / offline / unknown, and the two that matter most are opposites: "try again shortly" is encouraging when the registry is down and actively misleading when the tag does not exist. The raw error is always kept as the last line, because a diagnosis that hides what actually happened cannot be debugged by the person reading it. **(2) `runNativeSetup` returning an error made `main` RETURN.** So any first-run hiccup exited Helix, and the one that fired was a DOWNLOAD. Nothing about a missing model justifies taking someone's terminal away: the provider is chosen, the config is written, `/help`, `/doctor`, `/provider use` and every local command still work, and `/doctor` now names exactly what is missing. Helix's own rule for live mode is "degrade, never refuse the whole mode" and it applies with more force here, because this is the first thing a new user ever sees. Both pull sites now report and return nil; `main` prints what did not finish and starts anyway. **Ruled out the alternative explanation rather than assuming**: `gemma4:e2b` could have been a bad tag Helix was recommending, which would make every pull fail forever. Queried the registry directly — `registry.ollama.ai/v2/library/gemma4/manifests/e2b` returns **200**, so the tag is real and the 503 was genuinely transient, exactly as the new classification says. 6 new tests, including the reported error string verbatim as a fixture, and two structural tests verified to fail against the original `return err` / `return`. Build + vet + gofmt + full suite + `make lint` 0 issues + `make e2e` green | Uncommitted, on branch `blackBox` | The classifier matches on error TEXT, which is Ollama's to change. It fails safe — an unrecognised error still prints the raw line and a manual `ollama pull` — but a future Ollama that rewords its registry errors would silently fall back to the generic case |
| 2026-08-26 | **The muted text was unreadable, and it was measurable.** Owner sent a screenshot of `/about` with the philosophy and creation prose rendering as near-invisible dark-on-dark, and asked for a Helix × Tron Legacy palette. The aesthetic request had a hard accessibility bug underneath it: `HexSubtle` was `#444444`, which measures **1.44:1** against an ordinary dark background where WCAG's floor for body text is 4.5:1 — and even the large-text floor is 3:1. Not a matter of taste. **The root cause was one constant doing two incompatible jobs.** `HexSubtle` coloured panel rules and gutters, which SHOULD recede, and also prose, labels and table headers, which must be read. At a single value the readable half always loses. Split by role, with the new tones taken from the Tron poster palette (`#193f4a` / `#2f8ca3` / `#f4af2d` / `#030504`, fetched from the page the owner linked — WebFetch got a 403, the in-app browser did not) and lifted along their own hue until they pass: `HexSubtle` `#2C6E82` at 2.4:1 for chrome only, `HexMuted` `#4FB8D4` at 6.1:1 for secondary text, `HexAmber` `#F4AF2D` at 7.3:1 for values. Every number measured against `#282C34` — the LIGHTER of the plausible backgrounds, so the figures are the worst case rather than the flattering one. **Helix's identity is untouched**, by the standing request that covers the banner and the prompt: electric cyan, neon magenta and aggressive red still carry the brand, and the filled blocks were checked rather than assumed (worst is white-on-magenta at 3.74:1, fine for the short bold labels they hold). **Two role decisions worth recording.** The idle badge now prints its word in white with a dim glyph, because every other badge colours its text to match its glyph — and idle's glyph is chrome, so matching it put the state word in the same tone as the muted detail beside it and "off  ready when you are" read as one flat phrase. And the line editor's ghost text deliberately KEEPS chrome contrast: it has to be legible enough to read and dim enough that it can never be mistaken for what you typed, so readability past a point is the bug there. Commented in place so it is not "fixed" later. **Identifier colours unified**: 25 text uses of `HexSubtle` in `cmd/` moved to `HexMuted` after checking that none of them drew a rule or a gutter, and six direct `HexTertiary` uses that were really values (session ids, command names, tool and hook and config-key names, menu labels) moved to the gold, leaving orange to mean *warning* alone. 4 new contrast tests, verified to fail on the original grey with the exact 1.44:1 in the message. One of my own earlier tests hardcoded `HexSubtle` as the KV label colour and had to move with the role. `make e2e` flaked once on `TestE2E_ManualModeSafetyValve` — unrelated to colour, it runs `ls` — and passed both in isolation and on a full re-run. Build + vet + gofmt + full suite + `make lint` 0 issues + `make e2e` green | Uncommitted, on branch `blackBox` | Contrast is asserted against one reference background. A user on a LIGHT terminal theme would find the whole palette wrong, and Helix has never had a light mode — out of scope here, but this is the file that would need a second column |
| 2026-08-26 | **`/help` and `/about`: the last two screens drawing themselves by hand.** Owner reported `/help` as "a lot of text, some overlapping, all stale". Measured before touching it, and the overlap was real and quantifiable: the index padded every command into a fixed 30-column gutter and **clamped the pad at 2** when a usage line ran longer, so **9 of 56 commands** started their description at a different column from the other 47 — `/blackbox [on|off|status|setup|look|eyes|wake|tts|say|log|stats]` is 64 columns on its own. And nothing wrapped: against a hardcoded 76-column rule (not `panelWidth()`, so it ignored the terminal) the widest row was **124 columns**, breaking at the terminal edge and restarting outside the gutter. The same defect as `KV` before it learned to wrap, in the one screen a confused user reaches for first. **The fix that mattered was choosing what an index is FOR.** Simply aligning to the longest usage line would have pushed every description 34 columns right; truncating usage to fit costs 70–71 rendered lines and mangles fifteen signatures mid-syntax. Listing NAMES only costs **70 lines with nothing truncated** — measured all three before deciding — because an index answers "what exists" and `/help <command>` answers "how is it spelled", and the detail screen has the whole width to be complete in. **`/about` had three closing rules with no opening rule**, so each section ended with a horizon that came from nowhere, and its prose carried hand-typed line breaks at ~65 columns — correct at exactly one terminal width, whichever the author had. Now `PanelTitle`/`PanelWrap`, so it fits the terminal it is running in. **Also stale, and the reason the owner said "all stale":** PROMPT ANATOMY documented only the LEFT prompt and labelled the git branch "telemetry", while the entire RIGHT prompt — the clock and the Helix/Red Team/name ribbon, the most colourful thing on screen — went unexplained. Both halves now, in the colours they actually render. Three layout tests, each **verified to fail against the old renderer** rather than assumed: lines at 80–93 columns past the 74-column budget, "description starts at column 32, others at 31", and the 64-column signature in the index. `make lint` then caught three helper functions I had orphaned (`helpSection`, `tipLine`, `tipSub`) — deleted. Exported `shell.Truncate` and `shell.PanelRuleWidth` for callers that align a shared column themselves or must assert they fit it. **`/help <command>` went too**, and panelling it exposed two things the flat layout had hidden: its hand-drawn rule was **60 columns under a 64-column title**, and `/blackbox`'s own usage block — written for a flat 80-column screen — ran to 83 columns, which inside a gutter escapes the frame. Trimmed to the 70-column budget a panel leaves, with the width test tightened from 84 to 70 so it cannot drift back. It also showed the summary printed twice, once from `Summary` and again as the first line of `Detail`; the restatement is gone. 3 new tests, 3 e2e updated (the index assertion moved to `/help /blackbox` where the signature now lives, and `Aliases:` became the `ALIASES` row label) **The did-you-mean block went too, and there turned out to be TWO of them.** Typing `/nosuch` drew a red "⚠ UNRECOGNIZED SIGNAL" with `│` gutter bars and no panel around them — a gutter is the inside edge of a frame, and there was no frame, so those bars were an edge belonging to nothing — while `/help nosuch` drew two bare indented lines with no gutter at all. The same mistake, two presentations, neither framed; a test now asserts both routes render byte-identical output. Panelling it exposed a **colour bug that is invisible in source**: `shell.Value(strings.Join(items, shell.Muted(sep)))` reads correctly and is wrong, because the separator's reset terminates the enclosing colour — so of three suggestions for `/ra`, only `/rag-rebuild` rendered orange and the other two came out plain. Each item is coloured individually now, pinned by a test that counts styled spans and is verified to fail on the natural-looking construction. Also dropped the "ALL COMMANDS /help" row when `/help` is itself the suggestion, which is exactly what a mistyped `/hel` produces — the screen was pointing at the same command twice. 3 more tests, 2 more e2e updated for the renamed title. Full `make e2e` green (44 tests, one skip) | Uncommitted, on branch `blackBox` | Every screen in `cmd/helix/registry.go` now renders through the shared primitives |
| 2026-08-26 | **`make work` failed, and the interesting part is why my own runs had not.** The panelized `/doctor` renamed a line an e2e test asserts on (`Pending crash reports` → a `CRASH REPORTS` row with a `1 pending` badge), so the expectation needed updating — a two-line fix. **The real finding is that I had been verifying with a command that cannot detect this.** `go test` caches a package's result on that package's own inputs, and `tests/e2e`'s inputs do not include `cmd/helix`: `TestMain` builds the binary in a `go build` subprocess, which the go command's dependency tracking cannot see. So `go test ./... ./tests/...` replays a PASS recorded against the OLD binary and prints `ok`. Several commit messages in this session claimed "full suite incl. e2e green" on that basis; the claim was not false about what I ran, but what I ran could not have caught this, and the Makefile's `-count=1` was the only reason `make e2e` was ever trustworthy. Verified the mechanism rather than assuming it: with `GODEBUG=gocachetest=1`, the cached run's **input ID was byte-identical** before and after editing `cmd/helix/blackbox.go`. **Fixed structurally so no caller has to remember `-count=1`:** `TestSourceTreeIsPartOfTheCacheKey` reads every `.go` file under `cmd/` and `internal/`, folding their content into the package's cache key — 342 files, a few milliseconds, and the read itself is the entire mechanism. My first attempt put the reads in `TestMain` and **did not work**, which the same GODEBUG check showed immediately: `go test` records opened files through a logger installed by `m.Run`, so anything read before that call is invisible. Moving them into a test function changed the input ID (`7ed75ec8…` → `da7e0c3a…`) and the suite re-ran instead of replaying. Recorded as §9 rule 9, ahead of the render rule, because it governs whether any of the other rules' evidence is real. Full `make e2e` green: 44 tests, one skip (confinement fallback, not applicable on this host) | Uncommitted, on branch `blackBox` | Every "suite green" claim in this session's earlier commits should be read as "unit tests green, e2e possibly cached". They are green now, checked with `-count=1` |
| 2026-08-26 | **The three left over from the screenshot session.** **(1) `/doctor` told users to pull a model that cannot exist.** With `llm.fallback.model` unset it borrowed `ai.ActiveModel()` — the CLOUD model — and printed ``run `ollama pull deepseek-v4-flash-vision-exp` ``. An unset fallback is unset; it now says so and, better, LISTS what this Ollama actually has, because a user running Ollama usually already has a model and the fix is one config line rather than a download. **(2) The port-collision report read the wrong field.** `localSTTURL`/`localTTSURL` consulted only `cfg.Speech.*.BaseURL` — the PRIMARY provider's URL — and otherwise returned the stock default. But the wizard does not write BaseURL when it moves a sidecar off a busy port: it writes the per-provider `Endpoints` map, which `sidecarEndpoint` already resolves correctly and these never called. So the wizard reassigned whisper-local to 28859, said so on screen, and two panels later `/doctor` announced it on 8080 and warned about a llama.cpp collision that no longer existed. A report whose entire job is naming the right port was reading the field that does not hold it. Both now delegate to `sidecarEndpoint`; the test reproduces the screenshot's exact string before the fix. **(3) Barge-in, the part of it that does not need AEC.** This sat parked as "needs acoustic echo cancellation, which needs CGO" — true of talking OVER a reply, and I had been treating it as true of interruption generally. It is not. `SpeakStream` plays sentence by sentence, and between two `PlaySpeechContext` calls **the speaker is idle** — the one moment a microphone can be trusted with no echo to cancel. `BargeInProbe` samples that gap and cancels the reply on a clear speech level; `StopSpeaking` and the mid-sentence cancellation it needs already existed, waiting for a detector. Honest about its shape rather than sold as barge-in: **you cannot cut in mid-sentence**, a long one plays to its end, and it is interruption at the pace of punctuation. **Off by default, which is a judgement not caution** — nothing transcribes the probe, so a false positive silences Helix with no signal it was wrong, and its RMS floor is 10× the ordinary speech gate for that reason. The 150 ms settle before listening is not optional: without it the probe hears the tail of the sentence just spoken and interrupts Helix on its own voice, which is the chime-heard-as-"you" bug in a new place. The added gap is ~400 ms, inside the 300–500 ms pause ordinary speech already carries between sentences, and a test pins it there so interruption is never bought with a stutter in every reply. Surfaced as an INTERRUPT row in `/blackbox status`, because a feature that samples the microphone should not be an invisible config flag. **Then the owner asked a question that exposed the whole thing.** They asked whether ``/config speech.tts.barge_in true`` — the line I had just written into the INTERRUPT row — was meant to be run outside the Helix shell. It was not meant to be run anywhere: `/config` takes a fixed allowlist of SHORT key names (`tts`, `audio`, `provider`, `stt-url`…), and `speech.tts.barge_in` is the JSON path from config.json. There was no such key, so the single instruction Helix gave for enabling barge-in was a command it would reject. **This is precisely the defect I spent the previous three commits removing** — guidance pointing at something that does not exist — reintroduced by me while fixing it, in the same session. Fixed by adding the key rather than softening the text: `/config barge-in`, plus `context-turns`, which had the same problem one row up. **A drift test was the actual deliverable**, and it earned itself immediately: scanning every `/config <key>` printed from Go source against the allowlist, it found TWO MORE — `/config llm.fallback.model`, in `suggestOllamaModel`, written minutes earlier in this very entry as the fix for `/doctor` suggesting an unpullable model. I had replaced "run a pull that cannot work" with "run a /config that does not exist". Now `/config fallback-model`. The lesson is not that I was careless; it is that **a printed instruction is an interface with no compiler behind it**, and this codebase now has three guards for that class (README ↔ registry, panels ↔ config keys, and §9's rule about verifying by rendering) because reading the string never catches it. 9 new tests. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | Barge-in is unverified against a real microphone and a real room — the failure mode that matters (a false trigger from room noise) cannot be reproduced on a silent test host. Try it with `/config barge-in on` and report whether the floor needs raising |
| 2026-08-26 | **A real session on real hardware, and five defects no test had reached.** Owner ran the wizard and live mode on a second machine and sent screenshots. Every finding below came from that transcript, not from reading code. **(1) piper-local could never have started, and Helix walked the user through three confirmations and a 60 MB download to get there.** Its `Binaries` are `python3`/`python`, so the presence check passed on any machine with any Python; the install step was therefore skipped, the voice model fetched, the server launched, and the process died on `ModuleNotFoundError`. Presence of an interpreter says nothing about the module it is asked to run. Specs gain `Verify`, which for piper imports `piper.http_server` — the SERVER module, not the package root, because piper-tts installs fine while the http server still fails for want of flask, which is not one of its dependencies. It runs BEFORE the model download (structurally pinned), and again after an install, since pip can exit 0 having installed into a different interpreter than the one about to be launched. **(2) The start command had three definitions and two were unrunnable.** The launcher passed `--model <absolute path>`; the diagnosis and the wizard hint both printed `-m en_US-lessac-medium.onnx`, a bare filename that exists in no working directory. The screenshot shows both on one screen with no way to tell which was real. Now one definition in `internal/speech` (`PiperVoicePath`/`PiperArgs`/`PiperStartCmd`), used by all three. **(3) The one actionable line was truncated.** The failure printed `(ModuleNotFoundError: No modul…` — `truncStr(line, 160)` cut off before naming the module. Wrapped now, which `PanelWrap` can do since it learned to split long tokens last commit. **(4) "Voice link configured." printed in green above its own contradiction.** The comment said "Verify BEFORE claiming success" and the code printed the claim first and verified second; `verifySpeechSelection` now returns a bool and the line is earned. **(5) THE WALKIE-TALKIE.** `sox … trim 0 12.0` was a hard cut at twelve seconds, and it — not silence — was ending turns. A sentence longer than that was severed mid-word, the truncated clip transcribed and answered, and the REST arrived as a separate turn with its own unrelated answer: the screenshot shows one sentence about a GitHub URL split into two half-turns and two non-answers. Helix was not taking turns with the speaker, it was interrupting them on a stopwatch. Silence is the endpointer now (`HELIX_SOX_SILENCE_SECS`, default 1.5s, clamped); 45s is a backstop against a stuck mic, and the surrounding context deadlines were raised past it so they cannot become the cutter instead — the streaming path had its own 15s ceiling doing the same thing. **(6) The critic quarantined EVERY plan, so Helix could only chat.** `MaxTokens: 24` is a budget for the answer and none for reaching it; a model that emits any preamble — or spends tokens reasoning, as current models do — returned an empty string with no error, empty parsed as garbage, garbage failed closed. The visible line said "plan quarantined by critic" for a critic that had said nothing, which reads as a judgement on the user's request. Budget raised to 256, and a non-answer is now reported as a critic-model problem, visibly, while still failing closed — fail-closed is the design and a non-answer is not consent. Tests pin that an explicit `no` is NOT reported as a malfunction, so a real refusal keeps its weight. **(7) Background output spliced into the live HUD line**: `LISTENING |▁▂▃| 7.0sNVD update skipped/failed: context deadline exceeded`. The HUD redraws one line in place and ordinary `fmt`/`color` calls land inside it. `ux.LineHeld()` lets the HUD publish that it owns the line; non-urgent background writers check it. A mutex was the wrong tool — the writers are scattered across packages and wrapping every one costs more than the bug. **(8) Stale surfaces polished:** `/provider-status` and `/doctor` were the last flat `=== Header ===` stacks, where "Database ping failed" and "Shell: zsh" carried identical weight. Both panelized; `/provider-status` also gained `ai.ProviderStatusRows`, because the old renderer had to parse `" - "` back out of its own formatted strings — the re-parse-your-own-output mistake `internal/metrics` was created to avoid. 8 new tests. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | Not yet addressed from the same transcript: `/doctor` suggests `ollama pull <cloud-model-name>` for the offline fallback, which cannot work; the endpoint-conflict note names whisper-local on 8080 while it is answering on 28859; and true barge-in (speaking over Helix mid-reply) remains parked on the AEC/CGO conflict — capture is still half-duplex, so "conversational" here means turns are no longer cut, not that you can interrupt |
| 2026-08-25 | **`truncateANSI`, and the panel primitives are finally consistent.** The last width-sensitive primitive, and the same defect a third time: the guard compared COLUMNS (`runeLen`, which is column-aware) while the loop counted RUNES, so every double-width rune consumed one unit of a two-unit budget. Measured before writing it down this time — `truncateANSI(cjk, 15)` returned **29 columns**, `(cjk, 40)` returned **79**, roughly 2× at every width, and mixed ASCII/CJK text overflowed too. Fixed by budgeting `runewidth.RuneWidth` per rune and reserving the ellipsis's own measured width rather than assuming it is one column — the exact assumption being fixed. A wide rune that does not fit is dropped rather than half-printed, so a result may come back one column UNDER budget; under is safe, over is the bug. **The caller is where it actually hurt.** `Table` is entirely column-based — it measures cells with `runeLen` and pads with the difference — so an over-wide truncation drove `pad` negative, the code clamped it to zero, the two-space gap vanished and every column after it shifted. A shaved CJK cell rendered **106 columns into a 74-column frame with its final column 36 columns out of true**, which is the alignment `Table` shaves columns to protect in the first place. **Two process notes worth more than the fix.** First, my initial `Table` test PASSED against the buggy code: the fixture was not wide enough for `fitTableWidths` to shave anything, so the truncation path was never entered and the test proved nothing. It now asserts its own precondition (the cell must actually carry an ellipsis) before asserting alignment — a regression test that cannot reach the regression is worse than none, because it reads as coverage. Second, two of my assertions in this batch were themselves byte-vs-column bugs: one counted every letter `m` as an escape terminator (`"model"` has one), and one compared BYTE offsets to check column alignment on multi-byte text. Writing tests for a units bug in units I had not checked is the same mistake one level up. Every fix in this three-entry sequence is now verified by reverting the implementation and watching the test fail. 4 new tests. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | The panel primitives (`KV`, `PanelWrap`, `Table`/`truncateANSI`) now all measure in visible columns and none can render outside the frame. `visibleWidth` is the single definition of width, so a future primitive gets it right by using it |

### Phase 2 carry-overs (do with Phase 4)

- ~~P2.8 voice interaction log → build once with the Phase 4 journal (redaction/rotation
  shared).~~ **Done 2026-08-23.** The plan was right and slightly optimistic: there was no
  rotation to share, so P2.8 built it (`internal/journal`) and the Phase 4 journal now uses it
  too. One implementation, as intended — just written later than the wording implies.
- ~~Full multi-turn clarification (answer re-enters planner with turn context) → needs Phase 4
  session memory.~~ **Closed 2026-08-23.** Phase 4's session memory had delivered the mechanism
  (a clarifying question is recorded as the turn's reply, so the answer reaches the planner with
  the question in context) and nobody verified it. Verifying it exposed two defects on the
  confidence-gate path, both now fixed — see P2.7. **No Phase 2 carry-overs remain.**
- ~~Async/cancellable spoken responses (barge-in)~~ → delivered by P12.5
  (`audio.PlaySpeechContext`, mid-sentence cancellation). Mic-*triggered* barge-in remains
  parked on echo cancellation.

### Known open questions / pending decisions

- [x] ADR-010: resolved — tray indicator ships (if at all) as a separate opt-in helper over the
      daemon IPC; never CGO in the core binary. HUD overlay out of scope.
- [x] Phase 3 engine spike: resolved — energy detector default + openWakeWord-class sidecar
      client; Porcupine rejected (licensing + access key).
- [ ] MP3 decode in pure Go for TTS playback — verify library license during P1.6; prefer
      WAV/PCM provider outputs when available.
- [x] `go-dsp` license compatibility for Phase 6 FFT: resolved — hand-rolled radix-2 FFT, no
      dependency.
- [x] Windows IPC: resolved — stdlib cannot serve named pipes, so Windows uses a loopback-only
      TCP listener with a random per-start token in `daemon.conn.json` (0600); same-UID by
      construction. Deviation from ADR-004 recorded here.
- [x] Phase 4: per-sidecar `Health()` polling loop (Ollama/whisper/piper/wakeword) — done via
      `daemon.sidecarHealthLoop` (`internal/daemon/runtime.go`).
- [x] Phase 4: connectivity fallback engages on the next poll tick (~30s), not the ≤5s
      acceptance target — tune or add request-level detection — done: `watchConnectivity`
      runs a 5s tick (`time.NewTicker(5 * time.Second)`), matching the P4.10 ≤5s target.
- [x] Phase 5: P5.5 dedicated `vision.provider` fallback — done (`ai.RunVisionModel` routes to
      `vision.provider` when the active chat model cannot see; `ProviderVisionCapable` gates it).
- [x] Phase 6: ambient monitor wired into the live wake-loop capture stream (`ambient.TeeScanner`
      tees wake-loop chunks into `ChunkMonitor`); CPU-budget benchmark run — analyzer 26µs/chunk
      (≪5% idle overhead target).
- [x] Phase 11: P11.4 llama.cpp decision — resolved: **implement** the provider over
      `llama-server` (ADR-016). Ollama stays the default local runtime; llama.cpp is the escape
      hatch for boards Ollama does not support (Jetson Nano 1st-gen).
- [ ] Phase 7: speech queue tuning + measured first-byte/latency validation (P7.2c, hardware),
      §10 metrics run (P7.8 — instrumentation now in place, see dev log), and the
      `blackbox-v0.1.0` tag (P7.9, owner approval).

---

## §14. Glossary

- **STT/TTS** — speech-to-text / text-to-speech.
- **VAD** — voice activity detection (silence endpointing).
- **Wake word** — "Hey Helix" trigger phrase.
- **Sidecar** — external local service (Ollama, whisper.cpp, Piper, openWakeWord) managed by Helix
  over HTTP; keeps the core CGO-free (ADR-002).
- **Voice Risk Policy** — ADR-005 control set capping voice-channel authority.
- **Instruction Firewall** — existing five-layer prompt-injection defense (`internal/agent/firewall.go`,
  `docs/threat_model.md`).
- **Prompter seam** — swappable confirmation interface (`internal/commands/prompt.go:13`).
- **Suffix phrase** — a spoken phrase matched at the END of an utterance rather
  than as the whole of it, because people do not speak in bare commands ("okay,
  now switch to manual mode"). There are two: "manual mode" and "reboot". Both
  END a turn rather than being served by it, so both are handled before
  spoken-command dispatch. The reboot phrase adds a question exclusion the kill
  phrases do not have — "what happens when you reboot" is answered, not obeyed.
- **Continuity record** — `~/.helix/reboot.json`, the state `/reboot` carries
  across a restart: mode, working directory, provider/model, in-progress tasks,
  and (typed restarts only) a 240-rune excerpt of the last message. Consumed on
  read, expires after 12 h. Deliberately NOT a second copy of the conversation,
  which lives in `session.json`.
- **Supervisor** — the process `/reboot` leaves behind: the original Helix, which
  spawns its replacement, ignores the terminal signals so they reach the child,
  waits, and respawns on exit status 86 (ADR-018). **Distinct from the daemon's
  supervision**, which is systemd/launchd restarting `helix daemon`; these never
  interact.
- **Synthetic transcript injection** — testing technique feeding `InputEvent{Channel:"voice"}`
  programmatically instead of using a microphone.
- **PTY harness** — pseudo-terminal e2e suite (`tests/e2e/`) driving the real binary against mock
  providers.
- **Grid/GRID STATUS** — Helix's terminal status line branding.
- **Live mode / BlackBox mode** — `/blackbox on`: microphone, camera, spoken replies and the
  companion loop, all opened together and all closed by `/blackbox off` or "manual mode".
- **Companion loop** — the timer-driven camera sampler that may speak unprompted (Phase 13). The
  only part of the voice stack that is not turn-shaped.
- **Persona** — `internal/agent/persona.go`: who Helix is when it speaks. Shapes tone on the
  planner, chat and vision paths; grants no capability and cannot loosen a gate.
- **Panel** — the report frame in `internal/shell/panel.go` (title chip, gutter, closing rule) that
  every Helix report now uses.
- **Half duplex** — the recorder and the speaker cannot run at once (Phase 2 constraint, ADR-003).
  Why the companion queues its remarks instead of speaking them, and why mic-triggered barge-in
  still needs echo cancellation.
- **`Unmet` precondition** — a sidecar dependency Helix declines to resolve for the user (Docker),
  as distinct from a missing binary it offers to install (ADR-002 amendment).
- **Voice log** — `internal/journal`'s opt-in record of what Helix heard and said
  (`~/.helix/voice_log/`, P2.8). Text and metadata only, never audio; absent entirely until
  enabled; voice can stop it but not start it.
- **Chain preset** — one of the three pre-worked STT+TTS chains `/blackbox setup` offers before
  the pricing tables (P9.7). A pre-filled answer, not a shortcut: it walks the same
  key-verify and sidecar-probe steps as a manual pick.
- **Collect-less rule** — the ADR-005 principle that voice may reduce what is collected but never
  increase it (eyes off and log off by voice; camera opening and log starting are explicit or
  typed).

---

| 2026-08-22 | **Roadmap document brought up to date with the surface it describes.** Asked to update this file for the `/blackbox` unification and to be honest about what is left. Both were overdue and one was a structural defect of my own making: the seven dev-log rows appended this session had landed **after §14**, outside the table they belonged to — relocated into the Dev log where they are actually readable in sequence. Corrections, none of them deletions: **§0** gained a command-migration table and a note that phase sections still name the original commands *where they record what was built at the time*, with the rule that current-surface sections win when the two disagree. **§2.2/§2.3** learned the five files and one package that did not exist when they were written (`blackbox.go`, `companion.go`, `persona.go`, `first_run.go`, `panel.go`, `internal/deps/`) and that slash dispatch is now one registry entry rather than a switch. **Three ADRs amended rather than rewritten:** ADR-002 gains "sidecars may not require a container runtime", ADR-005 records the denied set narrowing 20 → 9 with the per-command argument and states plainly that rules 1–5 are unchanged, ADR-008 records `/voice`↔`/manual` becoming `/blackbox on`↔`off` and the suffix-matched safety valve. **§5 threat model** gains V4b (unattended capture by the companion) and V4c (the removed intent heuristic, recorded as RESOLVED rather than erased), with V4 amended to admit that live mode opens the camera. **P5.4 is marked SUPERSEDED, not deleted** — it shipped as specified and the specification was wrong — with P5.7/P5.8/P5.9 recording what replaced it. **§7** gains `companion`, `vision.model` and per-provider `endpoints`. **§10** gains the first real measurements, including the ones that missed: 8.8s warm frame-to-insight against a ≤5s target, and moondream rejected on output quality despite being 20× faster. **New Phase 13** covers the unification, companion, persona, first-run and visual system as its own scope with its own unmet acceptance criteria. **New "What is actually left"** table consolidates all 24 open checkboxes by phase and kind, because "core DONE" on five phases was hiding how much is genuinely unfinished — the honest summary is that little unwritten *code* remains (P2.8 voice log, P9.7 chain presets, P6 acceptance work, full-duplex barge-in), and almost everything else waits on hardware, keys, or an owner decision. Phase 7 is still the last gate before a release tag | Docs only; uncommitted, on branch `blackBox` | Two things need a human: a real camera frame for the companion loop, and one end-to-end voice session confirming the sidecar-endpoint fix by ear |

| 2026-08-27 | **Gemini and Meta added, and every provider's default model moved to one that can see.** Two vendors were missing and the reason to add them is the same reason the rest of this entry exists: **Gemini** (`internal/providers/gemini`, `https://generativelanguage.googleapis.com/v1beta/openai/`, `gemini-3.7-flash`) and **Meta** (`internal/providers/meta`, `https://api.meta.ai/v1`, `muse-spark-1.2`) both publish OpenAI-compatible surfaces, so each is the same thin adapter configuration as xAI — streaming, native tool calling and the Phase 5 image parts come for free. The registry key for Meta is `meta`, **not `llama`**: Helix also runs Llama weights through Ollama and llama.cpp, and a provider named for the old model family would collide with both. Meta's own quickstart exports the uselessly generic `MODEL_API_KEY`, so `envNames` gained a second lookup — `META_API_KEY` wins, Meta's name is honoured as a fallback, and a test pins that it does **not** leak to any other provider. Google's `AIza` prefix joined `keyPrefixOwners`, the same negative-only guard the xai/groq incident produced. **The larger half: four defaults could not process an image.** `SupportsVision` was widened once already because /eyes refused on providers that see perfectly well — but the complementary hole was never closed, because vision is a per-MODEL property and Helix was DEFAULTING to text-only models. `glm-5.2` cannot see (Z.ai say so in as many words), `deepseek-v4-flash` returns a 400 for an image part, and `gpt-4o` — the OpenAI default since the beginning — is scheduled for **API shutdown on 2026-10-23**, so that default was going to stop resolving on its own. Now: `gpt-5.6-sol` (**corrected to `gpt-5.6-luna` later the same day — see the next entry**), `claude-opus-5`, `gemini-3.7-flash`, `muse-spark-1.2`, `deepseek-v4-flash-vision-exp`, `glm-5.3-flash`, with `kimi-k3`, `qwen3.7-plus`, `grok-4.6` and `gemma4:e2b` already multimodal and now documented as chosen for it. The catalogue gained their context windows for the reason the xAI entry gives — a model missing from `contextLimits` is silently clamped to ~4k characters of RAG by `GetSafeContentLimit`, which starves a 1M-token model without a word — and `visionModelSubstrings` gained the natively-multimodal flagships whose names carry **no marker at all** (`muse-spark`, `glm-5.3-flash`, `kimi-k3`). That list is now full of near misses in both directions and the tests say so: `glm-5.3-flash` sees, `glm-5.3` does not; `qwen3.7-plus` sees, `qwen3.7-max` does not; `kimi-k2.6` sees, `kimi-k2.7-code` does not. **The actual deliverable is the drift test**, `TestEveryRegisteredProviderDefaultsToAModelThatCanSee`: it walks the registry and fails on any provider whose default cannot take an image, with llama.cpp the one honest exemption (`local-gguf` is a UI label, not a routing key — Helix cannot know what GGUF was loaded by hand). Verified non-inert by reverting GLM to `glm-5.2` and watching it fail. Model IDs, endpoints and the gpt-4o shutdown date were read from the vendors' live docs, not from memory. Also refreshed the /eyes refusal text, which still suggested gpt-4o. 30 packages green, gofmt clean | Uncommitted, on branch `blackBox` | Nothing here is verified against a live key: `gemini-3.7-flash` and `muse-spark-1.2` have not been called, and `glm-5.3-flash` shipped a day before this entry. Owner: `/setup` → Google Gemini with a real key and run `/blackbox eyes on` against the camera |

| 2026-08-27 | **The voice wizard was the last screen still printing flat colour, and converting it found two real bugs.** `panel.go` converted every STATUS screen; the screens that ASK were left behind, and a screenshot of a real `/blackbox setup` run showed exactly what that costs. The chain menu rendered inside a proper frame, and then — the moment the user answered — output fell to **column zero** for a port reassignment, a pid, a log path, two verification results and a recorder name: six facts about one operation, in four colours, none of them attached to the provider they were about. `whisper-local verified.` was indented two spaces; `Started whisper-local (pid 57271)…` was not; `It keeps running after Helix exits` was yellow next to a cyan `Log:` line. A wizard is the one screen a new user cannot skip, so it was the worst place in Helix to lose the thread. **Three new primitives in `internal/shell/wizard.go`**, because a wizard step is not a report row: it has a SUBJECT (the thing being set up), an OUTCOME, and usually an explanation or a command underneath. `Step` renders the first two behind the gutter with the state glyph, wrapping continuations under the subject; `StepDetail` indents the explanation past the glyph so it reads as subordinate rather than as a second event; `StepCommand` renders a shell command **and is the one thing in a panel deliberately allowed to run wide** — a launch command with a line break in it cannot be pasted. `PanelWrap` and `StepDetail` were extracted onto one body (`panelWrapIndent`) rather than copied, because two wrappers that measure differently is how a frame starts leaking. The wizard now closes with a **VOICE LINK panel** — ready/not-ready, HEAR, SPEAK, MIC — deliberately the same shape as `/blackbox status`, replacing three unrelated flat lines ("Chain verified…", "Voice link configured.", "Recorder detected: rec") that never named the providers just configured. `verifySpeechSelection` returns its probe report so the summary renders the chains it just resolved rather than probing again and being able to disagree with the line above it. **Bug one, caught by rendering the screen rather than reading it.** `wizDetail` trimmed each line before printing — and **indentation is the diagnosis format's marker for "this is a command"** (`internal/speech` emits the statement at column 0, its reasoning at two, the command at four). Trimming destroyed the signal, so the piper launch command came back word-wrapped across two lines: a command nobody can copy. This is the exact defect `providerDetailLines` already carries a comment about, reintroduced through a new door. The two levels are honoured now, and a test pins the command surviving intact. **Bug two, latent and shipped.** Panel-styled prompts carry ANSI, and `VoicePrompter.say` hands whatever it is given straight to the TTS engine — so a wizard run in voice mode would have **read the escape sequences aloud**, and the answer to an unparseable spoken question is a fail-closed "no". `shell.Plain` strips at the last moment, in `say`, rather than asking every caller to remember. **Also fixed while in there:** `printVoiceVocabulary` is called from INSIDE the voice-chain panel and printed a bare heading plus a column-zero list straight through the frame, leaving the `PanelEnd` beneath it looking like a rule belonging to nothing. Confirmations route through `wizConfirm`, so `Start whisper-local now?` became `▸ start whisper-local now [y/N]:` — the same voice as every other prompt. Single-line errors elsewhere keep `color.Red`: the primitives are for screens, and `handlers.go` uses the same convention. 10 new tests, both breakers verified non-inert by mutation. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | Rendered and read at 102 columns only. The wide `StepCommand` line is a deliberate trade — verify it reads acceptably in a narrow terminal, and re-run `/blackbox setup` end to end on a machine where piper is genuinely absent, since the install and model-fetch steps were converted but not exercised |

| 2026-08-27 | **The last two flat screens, and three content bugs that only structure could surface.** Owner screenshots of `/doctor` and `/config`. **`/doctor` printed its appliance block BETWEEN two panels as a flat green stack starting at column zero** — `--- Edge appliance ---` as a heading on a screen where every other heading is a chip, then twelve lines of equal weight: platform, audio, confinement, recorder, four sidecars, the offline brain and thermals, all the same colour, framed by nothing, with `ENVIRONMENT`'s chip opening underneath as if the stack belonged to neither panel. It is an APPLIANCE panel now — `PLATFORM`/`AUDIO`/`RECORDER`/`THERMALS` as labelled rows, a `LOCAL SIDECARS` section, and the offline brain as a row with its fix as a `StepCommand`. **Three things fell out of doing it.** (a) `Confinement in force: seatbelt (…)` was a **verbatim duplicate** of the `CONFINEMENT` row on the DOCTOR panel two lines above; it now appears only when `rep.Note` carries a caveat, because a caveat is news and repeating a row is not. (b) `printEdgeSidecars` **ignored `InChain`**, and `speech.ProviderStatusRow`'s own doc comment says why that is wrong: "Out-of-chain providers are not probed, so their Healthy=false means standby, not down." Every unselected sidecar rendered as a yellow `unreachable`, so a healthy machine that had simply not chosen csm-local or kokoro-local got two warnings about services it was never using — **a warning that fires on the normal case is a warning nobody reads**. Standby is now idle-grey and says why. (c) `reportEndpointConflicts` had three branches and only two were labelled; the harmless third printed bare wrapped prose, which on `/doctor` landed mid-panel between two labelled blocks as an orphaned paragraph. All three share one shape now. **`/config` was hand-padded at `%-15s` and `%-22s`** — the same defect `/cost` carried, and it bit exactly as predicted: `deepseek-v4-flash-vision-exp` came back as `deepseek-v4-flash-vis…` on a 102-column screen with room to spare, **a settings screen truncating the one thing it exists to report**. `shell.Table` measures the content instead. Three help strings were shortened to fit the column rather than be shaved by it. The API-key rule moved inside the frame as a warn step, because where a secret may be typed is a security property of the screen and not a footnote. **And a fourth content bug, visible only once values were coloured by state:** `fallback-model`'s getter returned the literal string `"(unset)"` — **a rendering decision baked into an accessor** — so the table coloured absence in the same amber as a real value, and any caller comparing `get() == ""` was simply wrong. The getter returns the value; absence is the renderer's to describe, in one vocabulary (`orNone` and `settingValue` now agree on the word). **Also: OpenAI's default is `gpt-5.6-luna`**, not `-sol`. Same 1.05M window, same vision the default exists to guarantee, lower cost — and a DEFAULT is what every user pays for before they have decided anything. `/model use gpt-5.6-sol` is one command away. 1 new e2e test asserting nothing escapes the appliance frame (verified non-inert by restoring the flat heading), 5 e2e expectations updated for deliberately changed output. One of those updates is itself a lesson: `Expect("key store")` failed because the sentence WRAPS at the terminal — **any multi-word expectation on wrapped prose is a flake waiting to happen**, so it asserts a single unwrappable token now. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | The `what it does` column still truncates one row at 72 columns; that is Table doing its job on a narrow terminal, but it means the settings help is width-dependent and nobody has read this screen below 80 |

| 2026-08-27 | **`/purge` polished, and its second confirmation turned out to delete nothing.** The manifest was one undifferentiated yellow wall of fourteen paths in which **"provider API keys (all providers, incl. STT/TTS)" carried exactly the weight of "SQLite shared-memory journal"** — so the single irreplaceable line on the screen was the hardest to notice, in a list whose entire job is to be read before an irreversible yes. That is the same flattening the panel conversion has been undoing everywhere else, except here it is a **safety property**. It is grouped now, ordered by irreplaceability rather than by directory layout — CREDENTIALS alone and first, then KNOWLEDGE, SETTINGS, MEMORY AND HISTORY, and the caches — with a per-group count and **one line saying what losing that group actually costs** ("you will have to paste every key again" vs "rebuilt by /update — a download, not a loss"). Paths shorten to `~/`, because repeating a 21-character home prefix on every row pushed each description right and made the part that DIFFERS the hardest thing on the line to compare; the absolute directory is named once, above. **The bug.** The second prompt — "Also delete downloaded model weights (~/.helix/models)?" — offered the llama.cpp GGUF directory **alone**, while every artifact Helix downloads through its own wizard lands somewhere else: `whisper-models` (hundreds of MB of GGML), `piper-voices` (the ONNX voice the setup fetches) and `piper` (the runtime binary it installs). **On a machine set up entirely through `/blackbox setup`, answering YES deleted nothing at all** — and the manifest's "model weights are NOT deleted by default" was right only by accident, while being contradicted two lines later by the prompt that offers to delete them. All four are covered now, `HELIX_MODEL_DIR` is honoured because `config.DefaultConfig` honours it, each is listed **with its size** (the number is the entire reason anyone answers yes), and the prompt is only asked when at least one exists — a prompt whose yes deletes nothing is worse than no prompt. **`shell.PromptDanger`** is new: `Prompt` renders cyan, which is right for "which provider should hear you" and wrong for "permanently delete everything", and a confirmation that looks identical either way asks the reader to supply the stakes from memory. `compactBytes` grew a GB branch and moved to int64 rather than gaining a second copy — "1433.6 MB" is a worse answer than "1.4 GB", and duplicated formatters drift. Failures are collected and reported once instead of interleaved red lines between silent successes. Also corrected the README, which claimed `/purge` **"cryptographically wipes"** local data: it calls `os.RemoveAll`, and a crypto-grade wipe on a journalling filesystem is a promise this code does not make. 5 unit tests + 2 e2e updated, including one asserting that declining the downloads prompt actually leaves the weights on disk — which nothing could have caught before, because the answer made no difference either way. Build + vet + gofmt + full suite incl. e2e + `make lint` 0 issues green | Uncommitted, on branch `blackBox` | `dirSize` walks the weight directories on every `/purge`, before either confirmation; on a spinning disk with tens of GB of GGUFs that is a pause nobody has measured |

| 2026-08-27 | **`/reboot` — the shell can restart itself now, and it remembers what it was doing.** `/purge` has ended with "restart Helix to finish" since it was written, because open SQLite handles only release on exit; the only way to act on that was to quit and relaunch by hand, throwing away the mode, the directory and any sense of what was in progress. **Most of what a restart needs already survived one** and this is worth stating because it shaped the design: `RingStore` writes `session.json` on every turn and reloads it at boot, and `cfg.UserPrefs.VoiceMode` + `initVoiceMode` already restore live mode — the precedent's own comment says so. What did NOT survive is everything between: the working directory, the active provider and model, the task in progress, and the plain fact that a restart happened. So the new `~/.helix/reboot.json` is deliberately **small and not a second copy of the conversation** — duplicating the transcript would mean two stores that can disagree AND a second copy of everything you said on disk, which V5 exists to prevent. It carries a 240-rune excerpt of the last message and nothing more, truncated on a **rune boundary** because a severed UTF-8 sequence makes the JSON unparseable and the record is then silently dropped — losing exactly the long exchange someone wanted back. It is **consumed on read**: a shell that claims to be picking up where it left off every single morning is telling you nothing. **The implementation is not what I first wrote.** `syscall.Exec` is the obvious answer — same PID, same terminal, no second process — and it **crashes this binary**: Go's exec takes the runtime's exec lock, which in a program with live cgo callback threads lands on a thread the runtime cannot park and aborts with `fatal error: notesleep not on g0`. Helix has exactly those threads, because the audio engine is CoreAudio through cgo and oto has no teardown to call. The shell died on `/reboot` and never came back. **I found that by instrumenting the real binary, not by reading code** — and the sequence is worth recording, because three plausible theories were wrong first: `ps` said the process was alive (it was a **zombie**, and my liveness probe was `Signal(nil)`, which always errors), then the pty went silent (the e2e read loop returns on the first error, and a pty master **returns EIO the instant no process holds the slave** — which is exactly what an image replacement produces, so the harness went permanently deaf at the one moment that mattered and the restarted shell would have blocked on its first write). Only a **marker file** — filesystem, not terminal — settled it: one boot, not two. The harness read loop now survives that gap, which is a fix every future test benefits from. **What shipped is a supervisor**, because the naive alternative is worse than the bug: if the parent just exits, the shell that launched Helix sees its foreground job finish and starts reading the terminal while the new Helix is also reading it — two readers on one tty — and when Helix IS the login shell, the parent exiting ends the session. So the parent stays, ignores the terminal signals so they reach the child, waits, and exits with the child's status. **The loop is what stops it nesting:** a supervised child that wants to reboot exits with code 86 rather than spawning anything, and the supervisor already waiting starts the next one. However many times you reboot, there are two processes. **Spoken, it is a suffix phrase like "manual mode"** — people say "okay, please reboot" — with one addition the kill phrases do not have: **a question is not an instruction.** "What happens when you reboot" ends on the phrase and would have fired; the test caught it. Openers carry that rule rather than a trailing "?", because STT punctuation is a guess and several providers never emit one. `/reboot` is the one DANGER ZONE command that is VoiceOK, and it earns that by destroying nothing: the record is written BEFORE the process ends, so a misheard reboot costs seconds and resumes where it was. **Two things fell out.** `DirectorySandbox.ChangeDirectory` printed its own green "📁 Changed directory" line — right for `/cd`, where the user asked, and wrong for anything moving on their behalf: the restore printed it at column zero directly above the panel about to report the same fact properly. Split into `SetDirectory` (silent) and `ChangeDirectory` (announcing). And the resumed panel said `WAS DOING working on: wire up the parser` above `TASKS wire up the parser` — one fact typed twice, visible only by rendering it. `shell.PromptDanger` is new, red, for a confirmation whose yes destroys something. 15 new tests incl. 2 e2e that prove the process genuinely comes back and that the record is consumed | Uncommitted, on branch `blackBox` | The supervisor is one extra idle process for the life of the session; nobody has measured what it costs on a Pi. And `/reboot` has never been run as a LOGIN shell, which is the case the supervisor exists for |

| 2026-08-27 | **Helix updates itself now, and the interesting parts are all refusals.** `/reboot` checks for a newer Helix, offers it, verifies it, installs it and restarts into it — from **GitHub releases** and from a **binary you built yourself**, which is the channel for whoever is working on Helix (`make current` writes `dist/helix`, and until now adopting it meant quitting and relaunching by hand). Default channel is `auto` and prefers whichever is newer, with a **tie going to the local build**: someone with both a checkout and an installed release is developing, and the thing they just compiled is the thing they meant to run. **This package decides which binary the user runs, so every control is structural and each is tested.** (1) **The checksum is mandatory, never best-effort.** A release with no checksums asset, or an asset the manifest does not list, is *not installable* — an updater that falls back to "no checksum available, continuing" has no integrity control, it has a comment. The entry is matched by **filename**, because goreleaser writes one line per artifact in an order nothing guarantees and picking by index would eventually verify one file against another's hash, which passes a checksum check while proving nothing. (2) **The host is PINNED**, not merely HTTPS — "it must be HTTPS" says nothing about *who* answers, and the whole attack is a reply or a redirect that walks the download elsewhere, so `CheckRedirect` refuses a hop off GitHub rather than following it. (3) **The payload must prove it is Helix for this machine**, read from its Go build info as DATA rather than by running it with `--version`: the question at that moment is whether the file can be trusted, and executing it to find out answers that in the worst possible order. `debug/buildinfo` also gives the release version for free, parsed out of the `-ldflags` goreleaser stamps. (4) **Archive entry paths are never used** — only the base name is matched and the output filename is always ours, so `../../etc/cron.d/helix` lands where we put it. (5) **The install is atomic and reversible**: stage on the same filesystem (rename is only atomic within one), keep the previous binary, rename over. **And the control verification cannot provide:** an authentic release that does not run *here*. The supervisor already knows the child's exit status, so a freshly installed binary that dies **non-zero within ten seconds** is rolled back automatically and the previous version started — bounded by both conditions, so quitting the new version normally, or hitting a crash an hour in, is not mistaken for a bad install. **The policy question, decided the same way as the last one:** `/reboot` is voice-reachable because restarting destroys nothing — but downloading and executing a new binary is a different act, and a television saying "reboot" must not cause it. **A spoken reboot checks, reports, and never installs.** Tested, and the test verified non-inert by removing the guard. *(**Reversed by owner decision later the same day** — installing is now automatic on both paths; see the ADR-019 amendment and the entry two rows down. The reasoning below is left as written because the decision that overruled it is only legible against it.)* **What this deliberately does NOT do is verify the Sigstore signatures the release pipeline produces.** Keyless verification needs the right identity and issuer constraints, and a check running with the wrong ones reports "verified" while proving nothing — worse than an honest checksum, because it buys confidence it has not earned. The UI prints the exact `cosign verify-blob` command instead. Also new: `helix --version`, because a CLI has to be able to say what it is without starting a shell. **Verified against the real repository, not a mock**: `Check` resolved `Nibir1/Helix` v1.0.0, `Fetch` downloaded the 5 MB archive through GitHub's redirect, matched the manifest checksum, extracted the 11.5 MB binary, and `Inspect` read `1.0.0 darwin/arm64` out of its build info — then a deliberately wrong checksum was refused against that same real release. 22 new tests | Uncommitted, on branch `blackBox` | The rollback path has never fired for real — a genuinely broken release is the one input nobody can synthesise honestly. And the local channel compares an unstamped `go build` by **file time**, which is the best available answer and still not a version: it is reported as "a newer local build", never as a version number |

| 2026-08-27 | **Every slash command now renders in one visual language, and five bugs from a live Intel-Mac session are fixed.** Owner screenshots. **(1) The update policy reversed, by owner decision.** Installing is now automatic with no confirmation, from the microphone as well as the keyboard — the release comes from a repository the owner controls and tags on purpose, so publishing it IS the authorization and a prompt in front of that has one sensible answer. ADR-019 and V5e are amended rather than quietly contradicted, and both now state the trade plainly: whoever can publish to the configured repo can replace the binary with no human present, and a bystander saying "reboot" can trigger it. The transport controls are unchanged and mandatory; `update.check: false` declines the bet where publisher and operator differ. **(2) The spoken reboot fired on a sentence ABOUT rebooting.** From the transcript: *"So you don't have any memory that I told you to reboot."* — not a question, so no question-opener matched; ends on the phrase, so the suffix matched; and Helix restarted itself in the middle of the user explaining that it had forgotten restarting. The blacklist is replaced by an **allowlist of imperative lead-ins**, because a blacklist has to anticipate every way English can end on a word without meaning it — which is not a list anyone can finish — while an allowlist only has to anticipate how people ask. Reported speech is the case that matters: "I told you to reboot", "you said reboot", "I am not asking you to reboot". **(3) Helix rebooted and then denied it.** *"Did you reboot yourself?" — "No. I did not reboot myself. I have been running the whole time."* It had, seconds earlier, on the user's spoken instruction. The panel said so on screen and the planner never saw a word of it, because session.json carries the CONVERSATION and a restart is not a turn in it. A synthetic turn is appended on resume now — Helix's own action, never anything the microphone heard, so V5d is untouched. **(4) A startup-ordering bug presented as a missing dependency.** `initVoiceMode()` ran ~40 lines before `visionSvc` was constructed, so a RESTORED live session printed `SIGHT ✘ on but blind — no ffmpeg on PATH` on a machine with working ffmpeg, while the same mode entered by typing `/blackbox on` said `✔ watching` seconds later. Two doors into one mode disagreeing, and the one that ran at boot blamed a dependency for a service that had merely not been built yet. Moved, pinned by a source-order test — **and the existing unit test had encoded the conflation as its expectation** (`visionSvc = nil // stands in for a host with no ffmpeg`), which is exactly why nothing caught it. **(5) A pip failure dumped sixty lines and diagnosed nothing.** On an Intel Mac, `piper-tts` cannot resolve because onnxruntime publishes no macOS x86_64 wheel for that Python; Helix showed the entire resolver log and said "failed exit status 1" — true, useless, and it blames the wrong layer. `runVisibleCommand` now TEES into a bounded ring and reads the failure: it names the packages pip named, says it is neither a Helix nor a network problem, and offers three ways forward. Diagnosis rather than prediction, deliberately — which Python has wheels for what is a moving target Helix would get wrong, while the installer has just finished saying so precisely. **THE SWEEP.** Every one of the 57 slash commands now renders through the panel primitives: `handlers.go` 182 raw colour calls → 0, `harness_cmds.go` 92 → 0, `session_cmds.go` 48 → 0, `dev_cmds.go` 36 → 0, `helpers.go` 92 → 0, plus the voice files. New `cmd/helix/ui.go` holds the three arrangements that account for most of the surface — a toggle, a short labelled report, a one-line outcome — because writing them out at every call site is how fifty-odd commands drifted into fifty-odd slightly different screens. **Four states, not three:** `uiIdle` exists because "no crash reports" and "the update was not installed" are not problems, and rendering them yellow is how a screen full of warnings trains people to stop reading warnings. **Content bugs the conversion surfaced:** `/audio` reported "ON (NOT READY)" — putting a contradiction in brackets — and `/debug` read `cfg.UserPrefs.DebugMode` while logging is actually governed by the env, so the two diverge the moment HELIX_DEBUG is exported by hand. 12 e2e expectations updated for deliberately changed output, and the recurring lesson earned itself twice more: **a multi-word expectation on wrapped prose is a flake waiting to happen**, and `PanelTitle` uppercases its chip. Build + vet + gofmt + `make lint` 0 issues | Uncommitted, on branch `blackBox` | `main.go`, `first_run.go` and `daemon_cmd.go` still hold ~69 raw colour calls. None is a slash command — they are startup, first-boot and the `helix daemon` CLI — but they are the last of it. **Closed the same day; see the next entry** |

| 2026-08-27 | **The last three files, and the asymmetry they exposed.** `main.go`, `first_run.go` and `daemon_cmd.go` — startup, first boot and the `helix daemon` CLI — were the remainder after the command sweep. `cmd/helix` now holds **zero** raw colour calls outside its tests. **Converting the daemon is what surfaced the real problem.** `github.com/fatih/color` disables itself when stdout is not a terminal, so every `color.Cyan` in this codebase has silently been doing the right thing when Helix is piped, redirected, or running as a systemd service. `shell.Fg` never did — it emitted escapes unconditionally, which nobody noticed because everything using it was interactive. Converting the daemon's output to the panel language without fixing that would have written control codes into **journald**, where none appear today: the polish making things worse in the one place nobody looks until something breaks. So the colour gate came first: `NO_COLOR` disables, `CLICOLOR_FORCE` overrides the other way, `TERM=dumb` disables, otherwise it follows whether stdout is a terminal — the de-facto standard rules rather than invented ones, so `helix … | less -R` behaves the way every other tool does. Verified by running the real binary both ways: piped output contains **no escape sequences at all**, forced output contains them. **Two test packages needed TestMain**, and that is a finding rather than a chore: with colour correctly off under `go test`, the tests that assert on rendered escapes would have passed against plain strings and proved nothing. `TestVoicePrompterSpeaksPlainTextNotEscapes` is the sharp one — it exists to prove a panel-styled prompt is stripped before reaching a TTS engine, and with no colour there is nothing to strip, so it would have passed while demonstrating the opposite of its own name. Both packages now force colour explicitly. The rule is split from its cached answer (`shouldColorize` vs the `sync.OnceValue`) because a OnceValue is untestable by construction — the answer cannot change under a running process, which is right in production and makes more than one case impossible in a test. 8 new tests | Uncommitted, on branch `blackBox` | The panel GLYPHS (│ ─ ✔ →) still render with colour off, which is correct for a piped terminal and arguably noise in a log file. Nobody has looked at a week of journald output to decide |

| 2026-08-28 | **A release script that checks instead of assuming, and the check nobody would think to write.** The old `scripts/git-push.sh` did `git add .`, committed a hardcoded message, deleted the tag and recreated it — every run. Three things wrong with that, and one of them only became wrong in v1.5.0. **(1) It committed.** A release should tag what has already been reviewed and merged; a script that stages the working tree at release time is a script that ships whatever was lying in it. A dirty tree is a hard stop now. **(2) It re-tagged by default.** Harmless when the tag was just a marker; **not harmless now that `/reboot` self-updates**, because the updater verifies a download against the checksums file published with that release. Replacing the artifacts under a tag someone has already fetched makes their update fail with a checksum mismatch — which is exactly what a tampered download looks like. `--force` exists, the warning says this, and a new patch version is the recommended path. **(3) It verified nothing.** The new one refuses a non-`main` branch, a dirty tree, a `main` that is behind/ahead/diverged from origin, and runs gofmt, vet, build, the full suite, e2e, lint and a cross-compile of all five release targets before the one irreversible step. **The most valuable check is the version constant.** `internal/config.HelixVersion` is overridden by goreleaser's ldflags for published binaries but NOT by `go build`, `go install` or `make current` — its own doc comment has said for months that it "has to track the tag or /version lies about which Helix you are running", and nothing enforced it. It does now, and the tag DEFAULTS to `v` + that constant, so the two cannot disagree and there is one place to edit. **And the post-release check nobody would write:** whether the published release actually carries a checksums asset and a per-platform archive. `internal/update` treats a missing manifest as *uninstallable* rather than downgrading to no verification — so a release that builds perfectly and publishes no checksums file is a release **no existing Helix can update to**, and the only way to find out is a user report. `gh release view` answers it in one call. **Testing found a bug in my own first draft**, and it is the kind worth recording: the script located the repository via `BASH_SOURCE`, which resolves to `/dev/fd/63` under `bash <(...)` or a pipe — so it `cd`'d to `/dev` and every git command failed with a confusing message about filesystem boundaries. It asks git now, and additionally proves it is the *Helix* repo before tagging anything. **A mistake of mine during that testing is also worth recording**, because it is a class of error rather than a slip: my scratch clone had `origin` pointed back at the working repo, and pushing `main` from it moved the real repo's local `main` onto a bogus commit. Nothing reached GitHub and it was reset, but the lesson is that a test harness pointed at the thing it is testing is not a test harness. Every guard was then exercised individually — bad tag format, missing `v`, wrong branch, dirty tree, behind/ahead/diverged, an already-published tag, a version mismatch — each producing its own refusal. `make release` / `make release-check` wired, README gains a Releasing section, and §0's workflow names the path. **`scripts/git-push.sh` and the `git-tag-push` target are deleted**, not left beside the new script — two release paths that disagree about whether re-tagging is safe is worse than the old one alone, because the dangerous one still runs and still looks official. **And a bug the deletion surfaced:** the Makefile comment and the README both said `release-check` was worth running *on a branch before the merge*, and it was not — the branch guard hard-stopped the dry run too, so the check could only ever run in the two minutes between merging and tagging, which is when it is least useful. Repository *state* (branch, clean tree, in sync with origin, tag already published) is now deferred under `--dry-run` and reported at the end as a count, while code quality still stops it — those are wrong on any branch. The summary says "code checks passed; N repository-state check(s) deferred" rather than "every check passed", because a summary that overstates once gets trusted and regretted later | Uncommitted, on branch `blackBox` | The script has never run to completion: the tag push, the CI watch and the self-update readiness check are all unexercised, because doing so would publish a release. They are the parts a dry run cannot reach |

*End of BlackBox_Development.md — maintain it as the single source of truth. If reality diverges
from this document, update the document in the same commit as the code.*
