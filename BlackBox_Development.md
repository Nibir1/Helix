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
2. **Verify repository state:**
   ```bash
   cd /Users/laptopbazaar/Projects/Helix   # (or wherever the repo lives)
   git branch --show-current               # MUST be blackBox
   git log --oneline -10                   # see what changed since this doc was written
   git status                              # note any uncommitted work
   ```
3. **Check §13 Progress Tracker** → find the first phase that is not `DONE`. That is your phase.
4. **Read that phase's section in §6** and every file it lists under "Files touched".
5. **Verify the baseline is green before changing anything:**
   ```bash
   make test    # unit tests
   make e2e     # PTY end-to-end suite (Linux/macOS only)
   ```
6. **Follow the ADRs in §3.** If a decision genuinely needs revisiting, do not silently deviate —
   update the ADR with new rationale first.
7. **Respect the guardrails in §12** (non-negotiables).
8. **When you finish a work session:** update §13 (mark tasks, append a dev-log entry with date,
   what changed, what's next), then commit with a conventional message
   (`feat(speech): ...`, `refactor(agent): ...`, etc.). Do NOT push to main. Do NOT merge.

> **Note on line numbers:** file:line references in this document are accurate for commit `fd34503`.
> They will drift as code changes — use them as guides (symbol names are the stable anchor).

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
cmd/helix/handlers.go        Slash-command dispatch (giant switch); /voice, /eyes go here
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
internal/commands/prompt.go  Prompter INTERFACE (swappable confirmation seam) ← key for voice
internal/commands/safety/    ValidateAndCleanCommand, AnalyzeShellRisk (Low/Med/High tiers)
internal/confinement/        Kernel-grade sandbox: Seatbelt (macOS), Landlock/bwrap (Linux)
internal/providers/          ★ AIProvider interface + Registry + KeyStore + 8 adapters
internal/providers/types.go  AIProvider, ChatMessage (TEXT-ONLY today), Capabilities{Vision...}
internal/ollama/             Ollama HTTP client + auto-installer + model pull (sidecar pattern!)
internal/audio/              Output-only synth tones (beep/oto); no microphone path exists
internal/config/config.go    Config + UserPrefs persisted to ~/.helix/config.json
internal/rag/                SQLite+FTS5+vector knowledge base, threat intel updaters
internal/recon/              Authorized recon engine (nmap/masscan orchestrator)
internal/stealth/            Memory-only private execution
internal/diagnostics/        Telemetry-free local crash reports (0600, redacted)
internal/ux/                 Terminal UX: typewriter, PrintAIMessage ← TTS tap-in point
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
| Setup wizard pattern | `cmd/helix/helpers.go:277` `useProviderInteractive` → `setupProvider` → `selectModelForProvider` | Copy for `/voice-setup` (STT/TTS selection + pricing display). |
| Slash dispatch | `cmd/helix/handlers.go:43` `handleSlashCommand` | Add `/voice`, `/manual`, `/eyes`, `/voice-setup`, `/tts`. |
| Classifier | `internal/shell/classify.go:124` `Classify()` (HighConfidence 0.65 at :57) | Voice transcripts (no shell metachars) naturally route to the AI planner. Optionally bias per mode. |
| Prompter seam | `internal/commands/prompt.go:13` `Prompter` interface; `commands.SetPrompter` | Swap in a `VoicePrompter` that speaks questions and listens for yes/no. **Fail-closed on timeout.** |
| Audio output stack | `internal/audio/` — beep/oto, 44.1kHz PCM, `Init()` at audio.go:72; Linux needs `audio_cgo` tag else noop backend | TTS playback rides this. Add a `PlaySpeech`-style API. |
| Ollama sidecar pattern | `internal/ollama/client.go` + `installer.go` (auto-install, health, model pull over HTTP) | **The proven pattern for local Whisper/Piper/openWakeWord:** external local service, HTTP, CGO-free core. |
| Injectable model call | `internal/agent/firewall.go:104` `criticRun` var | Precedent for making model calls mockable in unit tests. |
| Interrupt idiom | `internal/utils/interrupt.go` `RegisterOperation(cancel)` | Every long op (STT streaming, TTS, wake loop) registers here for Ctrl+C safety. |
| E2E mock provider | `tests/e2e/harness_test.go` — httptest server (:82-108) + isolated `$HOME` + PTY | Extend with mock STT/TTS/vision endpoints + hit counters. |
| Config prefs | `internal/config/config.go` `UserPrefs` (:30-39) | Add voice/vision prefs + new `speech` config section. |
| Vision capability flag | `internal/providers/types.go:43` `Capabilities.Vision` | Exists but **unconsumed**; ChatMessage is text-only. Phase 5 consumes it. |

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

### ADR-006 — Pricing data is data, not code.
**Decision:** The provider pricing catalog lives in an embedded JSON file
(`internal/speech/pricing.json`), user-overridable at `~/.helix/pricing.json`. The plan's tables
were partly speculative/stale; hardcoding them in Go source guarantees rot.
**Consequences:** The `/voice-setup` wizard reads merged (embedded + user) pricing at runtime.

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

### ADR-009 — `main` remains shippable; blackBox is a long-lived integration branch.
**Decision:** All BlackBox work commits to `blackBox`. Merges to `main` only by explicit owner
approval, after the full test suite passes. Keep commits conventional and phases delimited by tags
(`blackbox-phase1`, ...) for navigation.

---

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
| V4 | **Camera privacy**: frames leaked, stored, or sent to unintended provider | Strict opt-in (`/eyes on`), no disk persistence (fs-snapshot test enforced), single configured vision provider, journal entry per frame batch, `/eyes off` immediate |
| V5 | **Audio/ transcript persistence leakage** | Voice logs opt-in, 0600, redacted like diagnostics; `/purge` extended to wipe them; default = no persistence |
| V6 | **Cloud provider data exposure** (audio/text/frames to STT/TTS/vision vendors) | Explicit per-provider opt-in with key entry; wizard shows exactly what is sent where; local sidecar path documented as the private default |
| V7 | **Daemon IPC hijack** (local attacker sends commands to socket) | Socket 0600 in `~/.helix/` (0700 dir); optional shared-token file; daemon refuses requests when TTY session is "locked" |
| V8 | **Sidecar supply chain** (whisper.cpp/Piper installers) | Installers pin versions + checksums; mirror the Ollama installer's explicit-consent UX |

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

### Phase 0 — Decisions, Guardrails & Threat Model *(~0.5–1 week)*

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

### Phase 1 — Speech Provider Layer & TTS Output *(~2–3 weeks)*

**Goal:** Helix can **speak** (TTS through its own speaker stack) and **transcribe** (recorded clip
→ text via any configured provider), with the multi-provider registry, failover, pricing-aware
setup wizard, and push-to-talk recording utility. No main-loop integration yet (that's Phase 2).

**Tasks:**
- [ ] P1.1 `internal/speech/types.go` — core contracts (modeled on `AIProvider`):
  ```go
  type AudioFormat struct{ Kind string; SampleRate int; Channels int; Bytes []byte } // wav|mp3|pcm
  type Transcript struct{ Text string; Language string; Confidence float64; Provider string }

  type STTProvider interface {
      Name() string; DisplayName() string
      Transcribe(ctx context.Context, audio AudioFormat) (Transcript, error)
      SetAPIKey(key string); HealthCheck(ctx context.Context) error
      RequiresAPIKey() bool; IsLocal() bool; DefaultModel() string
      ListModels(ctx context.Context) ([]providers.ModelInfo, error)
  }
  type StreamingSTTProvider interface { // implemented by Deepgram/OpenAI realtime adapters
      Stream(ctx context.Context, mic <-chan AudioFormat) (<-chan Transcript, error)
  }
  type TTSProvider interface {
      Name() string; DisplayName() string
      Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error)
      SetAPIKey(key string); HealthCheck(ctx context.Context) error
      RequiresAPIKey() bool; IsLocal() bool; DefaultModel() string
  }
  ```
- [ ] P1.2 `internal/speech/registry.go` — copy `internal/providers/registry.go` pattern; keys
      namespaced `stt.<name>` / `tts.<name>` in the existing KeyStore (`~/.helix/secrets.json`);
      **failover chain**: configured order, health-gated (this satisfies the plan's automatic
      failover requirement — the LLM registry never had it; speech gets it first).
- [ ] P1.3 `internal/speech/pricing.json` + `pricing.go` — embedded catalog + `~/.helix/pricing.json`
      override; merged view; monthly-cost estimator (usage presets: light 1h/d, heavy 8h/d).
- [ ] P1.4 Cloud adapters (each with httptest-mocked unit tests):
      `adapter_openai_whisper.go` (files endpoint), `adapter_elevenlabs_tts.go`,
      `adapter_deepgram.go` (WS streaming), `adapter_openai_tts.go`.
- [ ] P1.5 Local sidecar adapters: `adapter_whisper_sidecar.go` (whisper.cpp server /
      faster-whisper HTTP), `adapter_piper_sidecar.go` — health-check + version probe; reuse
      Ollama-style install guidance text (auto-install optional, consent-gated, checksummed).
- [ ] P1.6 `internal/audio` speech playback: decode MP3/WAV → beep streamer → speaker; API
      `audio.PlaySpeech(fmt AudioFormat) error`; queue + barge-in cancel (context); Linux no-cgo
      → noop with warning (ADR-007).
- [ ] P1.7 Capture utility (ADR-003): `internal/speech/capture.go` — `RecordClip(ctx, opts)`
      via `sox`/`ffmpeg` shell-out to temp WAV (0600, deleted after transcription); device
      selection; `/doctor` check + setup-wizard install offer (brew/apt/choco).
- [ ] P1.8 Setup UX: `/voice-setup` wizard (copy `useProviderInteractive` pattern,
      `cmd/helix/helpers.go:277`): pick STT + TTS with the pricing comparison table (provider,
      model, $/min or $/1K chars, est. monthly cost, latency class, languages, key required,
      "recommended" badge = best value default), test-utterance smoke test, persist to config.
      Also `/stt-status`, `/tts-status` (health + active chain).
- [ ] P1.9 Slash commands: `/tts <on|off>`, `/say <text>` (dev utility), `/speak-test`.
- [ ] P1.10 E2E: extend `tests/e2e/harness_test.go` with mock STT/TTS `httptest` endpoints +
       hit-counters (mirror `ChatHits`); assert `helix -c`… no — assert `/say hello` hits the mock
       TTS server once; `/voice-setup` writes config.json correctly.
- [ ] P1.11 Fuzz the pricing-catalog merge + WAV/MP3 header parsing (`go-fuzz` targets, following
       existing fuzz corpus conventions).

**Files touched:** new `internal/speech/*`; `internal/audio/` additions; `cmd/helix/handlers.go`,
`helpers.go`, `main.go` (wizard wiring); `internal/config/config.go` (speech section);
`tests/e2e/*`; `.github/workflows/ci.yml` (audio-dependent tests skip gracefully when no device).

**Acceptance criteria:**
- [ ] `/voice-setup` → pick cloud STT+TTS with API keys → `/say "Hello"` speaks audibly (manual
      check) and hits mock server in e2e.
- [ ] Push-to-talk helper records → transcribes → prints text with provider+confidence.
- [ ] Primary STT down (mock 500s) → secondary used, status line shows failover.
- [ ] Local sidecar adapter works offline against a real whisper.cpp server (manual QA log).
- [ ] `make test`, `make e2e` green on all 3 OSes (audio-playback asserts are mock-based, not
      device-based).
- [ ] No regressions: full existing suite passes untouched.

**Risks:** audio decode licensing/MP3 patents (use WAV/PCM from providers where offered; mp3 decode
via pure-Go decoder if needed); sox/ffmpeg absence (detected + guided install).

---

### Phase 2 — Voice Input Loop, Mode Switching & Voice Risk Policy *(~2–3 weeks)*

**Goal:** The first true voice loop: push-to-talk → transcript → **existing** pipeline → spoken
result, with `/voice` ↔ `/manual` switching and the Voice Risk Policy enforced end-to-end.

**Tasks:**
- [ ] P2.1 `internal/input/` abstraction:
  ```go
  type Channel string // "text" | "voice"
  type InputEvent struct{ Text string; Channel Channel; Meta map[string]any }
  type Source interface { Events(ctx context.Context) (<-chan InputEvent, error); Close() error }
  ```
  Implement `TTYSource` (wraps `shell.ReadLine`, emits Channel=text) and
  `VoiceSource` (push-to-talk: hotkey or `/voice`-armed single-shot: record → STT → emit
  Channel=voice). Hybrid source (both live) arrives in Phase 7 but the interface supports it now.
- [ ] P2.2 REPL integration (`cmd/helix/main.go:170-198`): replace direct `shell.ReadLine` call
      with the active `Source`; select source from `UserPrefs.VoiceMode` + `/voice` `/manual`
      commands; mode switch ≤1s; wake chime via `audio.PlayAlert`.
- [ ] P2.3 Channel-aware dispatch: `Agent` gains an entry wrapper
      `HandleInputEvent(ev InputEvent)` that stamps the channel into the execution context and
      calls the existing `HandleInput(ev.Text)` path (`internal/agent/agent.go:139`). The entire
      classify→plan→safety pipeline stays untouched.
- [ ] P2.4 `internal/agent/policy_voice.go` — Voice Risk Policy engine (ADR-005):
      - voice channel → risk cap Medium (High blocked with spoken explanation);
      - deny-by-voice list (force push / hard reset / clean / delete-main / package uninstall of
        critical) → always "typed confirmation required", never voice-confirmable;
      - confidence gate: transcript confidence < threshold → clarification instead of execution;
      - policy decisions journaled.
      Table-driven unit tests: every dangerous action × channel × confidence.
- [ ] P2.5 `VoicePrompter` (implements `internal/commands/prompt.go:13` `Prompter`): speaks the
      question via TTS, opens a short listen window for yes/no/repeat/cancel,
      **timeout/no-answer = decline (fail-closed)**; `AskTypedConfirmation` by voice = always
      refuse with instructions to type. Wired via `commands.SetPrompter` when in voice mode;
      restores TTY prompter in manual mode.
- [ ] P2.6 Spoken responses: tap `ux.PrintAIMessage` / `handleResponseStep` — in voice mode, text
      renders to terminal AND synthesizes via TTS (async, cancellable by next input); long outputs
      truncated for speech with "say more" follow-up (configurable).
- [ ] P2.7 Clarification loop (plan Layer 2 step 4): low-confidence plan or missing entity →
      VoicePrompter asks; answer re-enters pipeline with conversation turn context (uses the
      single-turn scratch context; full session memory is Phase 4).
- [ ] P2.8 Voice interaction log (opt-in, default OFF): `~/.helix/voice_log/` 0600, redacted,
      `/purge` extended to wipe (wire into `cmd/helix/purge.go`).
- [ ] P2.9 E2E: **synthetic transcript injection** — a test hook injects InputEvents with
      Channel=voice (no audio hardware needed) to exercise policy paths in the PTY harness:
      medium-risk command → spoken confirm flow mocked; deny-listed command → blocked;
      low-confidence transcript → clarification. Assert mock TTS server hits.

**Acceptance criteria:**
- [ ] Full loop with real mic (manual QA): push-to-talk "list the go files here" → spoken result.
- [ ] Medium-risk by voice requires spoken confirmation; silence/timeout declines safely.
- [ ] "force push origin main" by voice → hard refusal + typed-confirmation instruction, always.
- [ ] `/manual` instantly returns to pure typing; `/voice` returns (≤1s, measured in e2e).
- [ ] Policy unit tests cover the deny-list matrix at 100% of listed actions.
- [ ] All pre-existing tests green.

**Risks:** VoicePrompter concurrency (TTS playback while listening — half-duplex v1: stop
playback before listening; full-duplex/barge-in deferred to Phase 3); transcript classification
edge cases (speech transcripts always NL-routed — verify classifier behavior with tests).

---

### Phase 3 — Wake Word & Hands-Free Operation *(~2 weeks)*

**Goal:** "Hey Helix" hands-free activation with tuned false-positive behavior, replacing
push-to-talk as the default voice-mode interaction.

**Tasks:**
- [ ] P3.1 Engine selection & spike (1–2 days): evaluate (a) **openWakeWord sidecar**
      (recommended — consistent with ADR-002; small Python/ONNX service exposing HTTP
      `/predict` over frames), (b) Porcupine (requires Picovoice access key + custom keyword
      training — note licensing), (c) onnxruntime CGO build (rejected for default per ADR-003).
      Document the spike result in this file's dev log.
- [ ] P3.2 `internal/wakeword/` — `WakeWordService`: continuous capture loop (chunked reads from
      recorder), sliding-window scoring vs threshold (sensitivity config), debounce/cooldown,
      emits wake events; **only active in voice mode**; explicit `"stop listening"` /
      `"go to sleep"` phrases + `/voice off` kill switch.
- [ ] P3.3 Flow wiring: wake event → chime (`PlayAlert`) → arm STT session (silence-timeout VAD:
      energy threshold v1; consider webrtc-vad sidecar later) → Phase-2 pipeline. 60s inactivity
      lockout back to wake-only (ADR-005 §5).
- [ ] P3.4 Barge-in v1: new wake event or user speech above threshold cancels TTS playback
      (`audio` speech queue cancel API from P1.6).
- [ ] P3.5 Metrics: wake latency, detection/FP counts → `~/.helix/metrics/` (local only);
      `/voice-stats` displays them.
- [ ] P3.6 Tests: WAV fixture corpus (self-recorded: 20+ positives incl. accents/noise, 50+
      negatives: TV/music/speech-without-wake) replayed through the engine — golden tests;
      sensitivity tuning documented as config presets (strict/balanced/loose).
- [ ] P3.7 Docs: setup guide for the sidecar (install, autostart alongside daemon in Phase 4).

**Acceptance criteria:**
- [ ] ≥97% detection on the fixture corpus at "balanced" preset; ≤1 FP/hour on a 2h ambient
      recording (manual QA, logged).
- [ ] Wake → transcription start ≤300ms (cloud path e2e ≤3s wake→execution, logged in metrics).
- [ ] Kill switch works instantly from voice and from terminal.
- [ ] Fixture tests run in CI against the sidecar's scoring function (or are skipped-with-notice
      if the sidecar binary is absent — never silently pass).

**Risks:** engine licensing (Porcupine); FP tuning time (budgeted); continuous capture CPU (chunk
size + sleep intervals; measure and record % CPU in dev log).

---

### Phase 4 — Headless Agent Refactor + HelixDaemon ("Living AI") *(~3–4 weeks — the big one)*

**Goal:** `helix daemon` runs as a supervised background service with session memory, IPC, journal,
graceful degradation, and auto-start on boot. The interactive TUI and the daemon share the same
headless-capable Agent core.

**Stage 4A — Agent decoupling (prerequisite, ~1 week):**
- [ ] P4.1 Introduce `internal/agent.Renderer` interface capturing exactly what Agent uses from
      `*ux.UX` today (typewriter, status lines, thinker spinner); implementations:
      `TTYRenderer` (wraps ux) and `HeadlessRenderer` (no-op + structured log). Replace direct
      `*ux.UX` field (`internal/agent/agent.go`) — mechanical, test-guarded.
- [ ] P4.2 Move slash-command dispatch behind a `SlashDispatcher` interface so the daemon can run
      without `cmd/helix` closures (`OnSlashCommand` wiring at `cmd/helix/main.go:166`).
- [ ] P4.3 Verify: interactive behavior byte-identical (PTY e2e suite unchanged and green is the
      proof).

**Stage 4B — Session state & memory:**
- [ ] P4.4 `internal/session/` — `SessionStore`: ring buffer (last N=20 turns default), persisted
      `~/.helix/session.json` (0600); injected into planner calls at
      `internal/ai/model.go:151` (message construction gains optional prior-context prefix —
      keep planner prompt strict-JSON contract intact; history goes in a clearly-fenced context
      block with data-only authority, mirroring firewall conventions).
- [ ] P4.5 Referential queries: "what did I ask five minutes ago", "do that again" answered from
      the store; `NewSlashCommands`: `/memory <clear|show>`.
- [ ] P4.6 Safe-subset **undo journal**: actions with a known reversal (git commit → `git reset
      --soft HEAD~1`; file created → move to trash dir `~/.helix/trash/`) are journaled;
      `"undo that"` offers the reversal (still passes the safety pipeline + risk policy).
      Explicitly out of scope: reversing overwrites/deletes (documented honestly).

**Stage 4C — Daemon & IPC:**
- [ ] P4.7 `internal/daemon/` + `cmd/helix/daemon.go` — `helix daemon`:
      owns wake service, speech providers, VoiceSource, headless Agent; supervision loop
      (panic recovery via `diagnostics.Guard` precedent, restart backoff); owns sidecar health
      checks (Ollama, whisper, piper, wakeword — `Health()` polling).
- [ ] P4.8 IPC (ADR-004): UDS `~/.helix/daemon.sock` 0600 (named pipe on Windows); NDJSON
      protocol: `{type:"status"|"submit"|"mode"|"log_tail"|"stop", ...}`; client
      `helix remote status|say|mode|logs|stop`; optional token file auth; refuses `submit` while
      a TTY session holds the "active session" lock.
- [ ] P4.9 Single-instance rules: daemon and interactive TUI coordinate via the socket (TTY lock
      transfers mic ownership; both can serve text).
- [ ] P4.10 Graceful degradation: connectivity monitor (reuse `/online` logic) → on loss: switch
      STT/TTS to local chain, spoken notice, log event; on restore: switch back.

**Stage 4D — Service installers & crash safety:**
- [ ] P4.11 `helix daemon install|uninstall|status`: launchd plist (macOS, `~/Library/LaunchAgents`),
      systemd user unit (Linux), Windows service via `sc.exe`/winged wrapper — templates embedded,
      user consent required, docs per OS.
- [ ] P4.12 Crash & journal: interactions journaled append-only `~/.helix/journal/` (redacted,
      rotated, `/purge` wipes); crash reports via existing `internal/diagnostics`; supervisor
      restarts on failure with backoff.
- [ ] P4.13 Ambient presence v1 (plan §3.3): tray indicator **deferred to Phase 7** (needs GUI
      dep decision); interim: TTS greeting on daemon start + `/doctor` daemon section + optional
      chime on wake (already shipped in Phase 3). Idle proactive suggestions: OFF by default,
      config-gated, v1 = break reminder after configurable focus time only.

**Stage 4E — Testing:**
- [ ] P4.14 Integration tests: spawn daemon in temp `$HOME` → IPC round-trip (status, submit a
      low-risk command via synthetic voice channel, expect spoken+logged result);
      `kill -9` → supervisor restart <5s; 72h soak script (`scripts/soak.sh`) logging uptime.
- [ ] P4.15 E2E: PTY suite extended with `helix remote` client paths against a test daemon.

**Acceptance criteria:**
- [ ] Daemon survives logout/reboot via service config on all 3 OSes (manual QA checklists).
- [ ] 99.5% uptime over 72h soak (metrics file evidences it).
- [ ] `"what did I ask five minutes ago"` answered correctly from session store (e2e).
- [ ] `"undo that"` after a voice-initiated `git commit` performs soft reset with confirmation.
- [ ] Network cut mid-session → local fallback engages within 5s, spoken notice heard.
- [ ] Interactive TUI behavior unchanged (Phase-4A e2e proof still green).

**Risks:** refactor regressions (mitigated by byte-identical e2e requirement); IPC on Windows
named pipes (spike early); service-install privilege differences across OSes.

---

### Phase 5 — Camera-Based Visual Perception *(~2–3 weeks)*

**Goal:** Opt-in "eyes": capture a frame on demand during an active conversation, attach it to a
multimodal planner request, get vision-grounded answers. Privacy controls are load-bearing.

**Tasks:**
- [ ] P5.1 Multimodal message format (backward compatible) in `internal/providers/types.go`:
  ```go
  type MessagePart struct{ Type string; Text string; ImageURL string; ImageData []byte /* base64 at adapter */ }
  // ChatMessage.Content stays for text; new optional Parts []MessagePart
  ```
  Adapter updates: OpenAI content-array format, Anthropic vision blocks, Gemini, Ollama
  (llava/gemma3 tags — this finally consumes `Capabilities.Vision`, `types.go:43`, as a gate:
  refuse gracefully if the active model can't see).
- [ ] P5.2 `internal/vision/capture.go` — `VisionCaptureService`: single-frame grab via
  `ffmpeg` shell-out (platform devices: avfoundation macOS / dshow Windows / v4l2 Linux —
  documented); downscale to ≤1024px JPEG q80; **memory-only** — never written to disk
  (enforced by test).
- [ ] P5.3 Opt-in lifecycle: `/eyes on|off` (default OFF); on activation TTS announces it; every
  frame batch journaled (provider + count + timestamp, no pixels); deactivation immediate and
  confirmed vocally. "Turn off your eyes" voice phrase = same as `/eyes off`.
- [ ] P5.4 Conversational wiring: in voice mode with eyes on, deictic utterances ("what's wrong
  with **this** code?", "read this serial number") trigger frame capture → attach as image part →
  planner/vision LLM. Non-deictic queries unaffected. One frame per turn (interval polling for
  "activity awareness" is **deferred** — cost/privacy trade-off documented).
- [ ] P5.5 Vision LLM routing: use the configured chat provider if vision-capable, else a
  dedicated vision provider entry in config (`vision.provider`) — health-gated like speech.
- [ ] P5.6 Tests: mock multimodal endpoint asserts base64 image part arrived; fs-snapshot test
  proves zero frame persistence during a vision turn; capability-gate test (non-vision model →
  polite refusal); capture unit test skipped cleanly when ffmpeg/device absent.

**Acceptance criteria:**
- [ ] "Hey Helix, what's wrong with this code?" (camera pointed at screen) → spoken, relevant
      diagnosis (manual QA, ≥3 scenarios logged).
- [ ] Frame-to-insight ≤5s (cloud provider, measured metric).
- [ ] Filesystem snapshot test: no image bytes on disk during/after vision turns.
- [ ] `/eyes off` + voice phrase both deactivate instantly; journal shows every frame event.

**Risks:** ffmpeg device-flag quirks per OS (spike on all 3 early); model availability locally
(llava quality is modest — set expectations in docs).

---

### Phase 6 — Ambient Noise Detection *(~1–2 weeks, OPTIONAL — demoted by review)*

**Goal:** Basic auditory awareness: loud-noise/alarm/music/multi-voice/silence events with
configurable contextual responses. Ships only if Phases 1–5 are stable; **runs only in full voice
mode and opt-in**.

**Tasks:**
- [ ] P6.1 `internal/ambient/` — `AudioMonitorService` sharing the wake-loop capture stream:
      v1 = pure-Go analysis (RMS energy spike detection, silence tracking, crude spectral
      centroid via hand-rolled FFT or `go-dsp` if license-clean) → categories: `loud_noise`,
      `alarm_like` (sustained narrow band), `music_like`, `speech_multi` (deferred — hard), `silence`.
      **YAMNet/classifier models explicitly deferred** — document as future sidecar.
- [ ] P6.2 Config: per-category enable, sensitivity, response mode `vocal|log|ignore`; cooldowns
      per category (no loops of "are you okay?").
- [ ] P6.3 Contextual responses (from original plan §5.2): loud noise → "Are you okay?" (once,
      cooldown 10min); alarm → offer to check; music → TTS volume ducking (oto gain) instead of
      chatter; prolonged silence after a question → offer repeat.
- [ ] P6.4 Tests: WAV fixture corpus per category, golden classification tests; CPU budget test
      (<5% idle overhead on dev laptop, logged).

**Acceptance criteria:**
- [ ] ≥90% accuracy on the configured fixture categories.
- [ ] No response spam (cooldowns proven by unit test).
- [ ] Disabled by default; enabling documented in one line of config.

---

### Phase 7 — Hybrid Mode, Polish, Hardening & Release *(~2 weeks)*

**Goal:** Ship-quality: hybrid input, performance, full e2e matrix, docs, tagged branch release.

**Tasks:**
- [ ] P7.1 Hybrid mode: `input.HybridSource` (TTY + voice simultaneously — the Phase-2 interface
      makes this cheap); per-turn channel tagging already flows through policy.
- [ ] P7.2 Performance pass: audio buffer sizing, streaming STT partial transcripts displayed
      live, TTS first-byte latency budget (<800ms cloud), speech queue tuning; benchmark suite
      (`go test -bench`) for speech hot paths; results logged in dev log.
- [ ] P7.3 Presence polish (plan §3.3 remainder): system-tray indicator — decide dependency
      (pure-Go tray libs are immature; options: `fyne.io/systray` CGO, or a tiny separate
      helper binary) — **decision required at phase start**, record as ADR-010; HUD overlay
      out of scope (documented).
- [ ] P7.4 E2E matrix completion: PTY + mock STT/TTS/vision endpoints covering: voice happy path,
      failover, policy denials, daemon remote flows, mode switches, eyes on/off; CI green on
      3-OS matrix with graceful skips for hardware-dependent tests.
- [ ] P7.5 Fuzzing: transcript→policy parser, NDJSON IPC messages, WAV/MP3 headers (extend
      existing fuzz conventions).
- [ ] P7.6 Docs: `docs/blackbox.md` (user guide: setup wizard, sidecars, daemon install, privacy
      controls), README BlackBox section, `docs/architecture.md` + `docs/threat_model.md` updated;
      this file's §13 finalized for the release.
- [ ] P7.7 Supply chain: goreleaser config for the blackBox binary unchanged (CGO-free);
      sidecar installers checksum-pinned; SBOM/cosign as-is.
- [ ] P7.8 Metrics collection run against §10 table; gaps documented honestly.
- [ ] P7.9 Tag `blackbox-v0.1.0` on the branch. **No merge to `main` without explicit owner
      approval** (ADR-009).

**Acceptance criteria:** all §10 targets measured and logged; full suite green 3-OS; docs complete;
release tagged.

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
    "stt":    { "provider": "openai", "model": "whisper-1", "fallbacks": ["whisper-local"] },
    "tts":    { "provider": "elevenlabs", "model": "eleven_turbo_v2_5", "voice_id": "",
                "fallbacks": ["piper-local"] },
    "wake_word": { "enabled": false, "engine": "openwakeword-sidecar", "phrase": "hey helix",
                "sensitivity_preset": "balanced", "cooldown_s": 2 },
    "capture": { "backend": "auto", "device": "default", "sample_rate": 16000,
                "silence_timeout_ms": 1500, "max_utterance_s": 30 }
  },
  "voice_policy": { "max_risk": "medium", "confirm_timeout_s": 8,
                "dangerous_needs_typed": true, "min_transcript_confidence": 0.6 },
  "vision": { "enabled": false, "provider": null, "max_frames_per_turn": 1 },
  "ambient": { "enabled": false, "sensitivity": 0.5, "response_mode": "log",
                "categories": { "loud_noise": true, "alarm_like": true, "music_like": false } },
  "daemon":  { "autostart": false, "journal": true, "session_turns": 20 }
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
| `journal/` | append-only interaction journal | redacted, rotated, `/purge` wipes |
| `voice_log/` | opt-in transcripts+audio refs | default absent; 0600; `/purge` wipes |
| `metrics/` | wake/latency/FP counters | local only |
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
   opt-in paths; diagnostics-style grep test keeps networking out of journal/redaction code.
5. **Fuzz** every new parser: pricing merge, WAV/MP3 headers, NDJSON IPC, transcript→policy.
6. **E2E stays honest**: if a required sidecar binary is absent, tests skip loudly
   (`t.Skipf`) — never silently pass.
7. **Baseline rule**: `make test && make e2e` green on `blackBox` before every merge/commit-series;
   pre-existing suites are never weakened — only extended.

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
| Daemon uptime | 99.5% (72h soak) | same | soak script |
| Mode switch latency | ≤1s | same | e2e |
| Frame-to-insight (vision) | ≤5s | best-effort (llava) | metrics log |
| Noise classification (enabled categories) | ≥90% | same | Phase 6 fixtures |

Rationale: the original plan's single ≤3s target assumed cloud providers end-to-end; the repo's
local-first direction (Ollama standardization, commit `ca9560b`) makes honest dual targets
necessary.

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
| 1 — Speech Provider Layer | `IN PROGRESS` | 2026-08-16 | — | |
| 2 — Voice Input & Policy | `NOT STARTED` | — | — | |
| 3 — Wake Word | `NOT STARTED` | — | — | Engine spike decision pending |
| 4 — Daemon & Living AI | `NOT STARTED` | — | — | Biggest lift; 4A refactor first |
| 5 — Vision | `NOT STARTED` | — | — | |
| 6 — Ambient Audio (optional) | `NOT STARTED` | — | — | May be descoped |
| 7 — Polish & Release | `NOT STARTED` | — | — | Tray ADR-010 pending |

### Task-level checkboxes

All task checkboxes inside §6 phase sections are the authoritative task list. Tick them as work
completes and record evidence (test names, metrics, QA logs) in the dev log below.

### Dev log (append-only, newest last)

| Date | Session summary | State left in | Next step |
|------|-----------------|---------------|-----------|
| 2026-08-16 | Full codebase analysis + plan review; this roadmap authored; no code written | `blackBox` clean at `fd34503`, doc at repo root, uncommitted | Phase 0: ratify ADRs, write `docs/threat_model_voice.md`, create compiling package skeletons, commit |

### Known open questions / pending decisions

- [ ] ADR-010: tray indicator implementation (Phase 7 start).
- [ ] Phase 3 engine spike: openWakeWord sidecar vs Porcupine (licensing check).
- [ ] MP3 decode in pure Go for TTS playback — verify library license during P1.6; prefer
      WAV/PCM provider outputs when available.
- [ ] `go-dsp` license compatibility for Phase 6 FFT (else hand-rolled).
- [ ] Windows named-pipe spike scheduling (early Phase 4).

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
- **Synthetic transcript injection** — testing technique feeding `InputEvent{Channel:"voice"}`
  programmatically instead of using a microphone.
- **PTY harness** — pseudo-terminal e2e suite (`tests/e2e/`) driving the real binary against mock
  providers.
- **Grid/GRID STATUS** — Helix's terminal status line branding.

---

*End of BlackBox_Development.md — maintain it as the single source of truth. If reality diverges
from this document, update the document in the same commit as the code.*
