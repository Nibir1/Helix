## Helix v1.5.0 — BlackBox: The Voice-First Companion

v1.0.0 taught the terminal to speak human. v1.5.0 lets you stop typing.

BlackBox turns Helix from a reactive text tool into an always-on multimodal companion: it listens, transcribes, plans, executes, watches through a camera when you ask, answers aloud, and — with `helix daemon` — keeps doing so after you close the terminal. The intelligence is the same intelligence. Every spoken word lands in the *same* pipeline typed input has always used (classify → plan → Instruction Firewall → risk tiers → sandbox → kernel confinement), because a new input channel is not a reason to build a second, weaker door.

It remains local-first and telemetry-free. The whole voice stack runs offline if you want it to — whisper.cpp for ears, Piper for a voice, Ollama or llama.cpp for a brain — and **no component requires Docker**. Keys stay in a 0600 file. Nothing you say is written to disk unless you ask for it, and camera frames are never written at all.

**56,000 lines across 12 new packages, one unchanged moat.**

### 🎙️ Voice

- **Multi-provider speech** with failover chains: Groq, OpenAI and Deepgram for transcription; OpenAI, Deepgram, ElevenLabs for speech; whisper.cpp, Piper, Kokoro and **Sesame CSM-1B** as local sidecars. Pricing is *data* (`pricing.json`, user-overridable), never hardcoded routing.
- **Recommended chains** — one keystroke in `/blackbox setup` picks cheapest-cloud (Groq + `gpt-4o-mini-tts`), lowest-latency (Deepgram Nova-3 + Aura-2), or fully-local/private (whisper.cpp + Piper). Every cloud chain pre-fills a *local* fallback, because the failure worth surviving is the network.
- **Hands-free wake word** — energy detector by default (pure Go, works everywhere, honest about detecting onset rather than a phrase) or an openWakeWord-class sidecar for true keyword spotting. Between turns Helix holds in wake-only listening: nothing is transcribed until a wake event fires.
- **Streaming both ways** — live interim transcripts (Deepgram WebSocket), and sentence-pipelined TTS that starts playing after the first sentence synthesizes instead of the whole paragraph. Time-to-first-audio dropped from a measured 2,280 ms to ~150 ms + network.
- **Barge-in** — Ctrl+C stops a spoken reply mid-sentence (~50 ms), not at the next sentence boundary. Opt-in voice interruption stops it by speaking in the pause between sentences, with no echo cancellation required.
- **A sci-fi HUD** — listening waveform driven by the real microphone level (log-scaled, because speech RMS on a linear meter barely leaves the floor), decode sweep, speaking wave, wake-standby pulse. Terminal-native; no GUI dependency.

### 🗣️ Sesame CSM-1B — a local voice that sounds like a conversation

The speech model behind Sesame's "crossing the uncanny valley of voice" demo, running
on your own machine with **no Python, no Docker and no API calls**. Not the whole demo —
what Sesame open-sourced is the speech *generator*, which cannot produce text — so Helix's
planner still decides what to say. What it changes is how that sounds.

- **A Rust sidecar, not PyTorch.** `csm.rs` (candle) with CUDA, Metal, Accelerate and MKL
  backends and an OpenAI-shaped endpoint. The CGO-free single binary is untouched.
- **Conditioned on the conversation.** CSM's prosody depends on hearing the last few turns,
  which is the difference between very good TTS and something that sounds like it was
  listening. Helix assembles that context and sends it; `docs/csm-context.patch` is the
  (verified, working) upstream patch that teaches a CSM server to accept it.
- **Honest about whether it worked.** An unpatched server accepts the context field and
  silently drops it, so Helix reads a response header rather than assuming. The `CONTEXT`
  row of `/blackbox status` distinguishes *conditioning* from *not applied*, and flags
  *retained, unused* when turns are being held that no configured voice can consume —
  retention with a privacy cost and no benefit is surfaced, not hidden.
- **Retention is memory-only, bounded and off by default.** Context needs prior audio;
  nothing is written to disk, it is capped by turns and bytes, and `/blackbox off` drops it.

**It wants a discrete GPU.** Measured 1.69× real-time on an M4 Air (slower than playback:
csm.rs runs the quantized weights on CPU regardless of the Metal build), against a ~0.8×
reference figure on an NVIDIA GPU. Pair it with `piper-local` as the fallback and a machine
that cannot keep up simply uses the fast voice. Setup, per-platform build flags and a
per-machine expectation table are in `docs/local_runtimes.md` §3.5–3.6.

### 👁️ Vision

- **Opt-in camera** via `ffmpeg`, one frame at a time, downscaled to ≤1024 px, **held in memory and never written to disk** — enforced by a filesystem-snapshot test. Only metadata reaches the journal.
- **The planner has a `vision` tool.** Ask "what can you see?" and the model chooses the camera itself. An earlier heuristic that fired on any sentence containing "this" was removed: it answered "what do we have in *this* directory?" by describing the room.
- **A companion loop** that looks on a timer and may speak unprompted — with a 16×16 luminance fingerprint diffed in-process, so an unchanged scene never costs a model call.
- **Capture failures are legible.** A camera the OS has not authorized opens and then delivers nothing; Helix gives up after 8 seconds and names the likely cause, and `/blackbox status` will not claim the camera is watching until a frame has actually arrived.

### 🧠 The agentic harness

- **Bounded plan → act → observe → replan** (`/agentic on`). A failed step feeds its exit code and a sanitized tail of its output back to the planner, which self-corrects. Every iteration re-enters the *entire* safety pipeline; the harness decides only whether to plan again.
- **Native tool calling** across eight providers — one normalized `ToolDefinition` over three different wire formats (OpenAI-shaped, Anthropic's flat `input_schema` blocks, Ollama's `/api/chat`), sharing one streamed-fragment reassembler instead of three chances to re-bug it. Capability reporting describes what the *adapter* can drive, not what the vendor sells: `custom` and llama.cpp are excluded because their tool support is genuinely undetectable, and Ollama is gated **per model** — Helix's own default local model ships no tool template, so it does not waste a round trip pretending otherwise. Where tool calling is unavailable the planner falls back to the prompt ladder silently, costing at most one request.
- **Streaming token render** — replies appear as they generate, and the spinner stops at the first token rather than the last.
- **Session memory** — a persisted ring of recent turns, injected as a zero-authority fenced block. "What did I ask a moment ago" works; a transcript Helix did not trust is labelled `not understood` rather than quoted back as if you had said it cleanly.
- **Safe-subset undo** — `"undo that"` reverses a journalled action (a commit becomes a soft reset) through the normal confirmation and safety path. Overwrites and deletions are explicitly out of scope, and the docs say so.

### 🛰️ The Living AI daemon

- **`helix daemon`** — a supervised background service with its own headless Agent, session memory, wake loop and sidecar health checks. Panic-guarded, restart-backed-off, and it heartbeats its own liveness so uptime is measurable rather than asserted.
- **NDJSON IPC over a 0600 Unix socket** (loopback TCP + token on Windows), driven by `helix remote status|say|mode|logs|stop`. No Redis, no gRPC, no broker — filesystem permissions are the auth.
- **Service installers** for launchd, `systemd --user` and Windows, consent-gated, with lingering detection on Linux because a `--user` service that stops at logout is not installed in any useful sense.
- **Graceful degradation** — a connectivity monitor moves ears, voice *and* brain to local providers together, says so out loud, and journals it.

### 🔌 Providers & offline resilience

- **Ten LLM providers**: OpenAI, Anthropic, DeepSeek, Kimi, Qwen, GLM, xAI (Grok), Ollama, llama.cpp, and any OpenAI-compatible custom endpoint.
- **Circuit-breaker failover** (CLOSED → OPEN → HALF-OPEN) keeps Helix *thinking* when the cloud disappears, not merely hearing and speaking. It health-checks the local brain before every switch, so a machine with no local runtime never degrades onto a dead endpoint, and an explicit `/provider use` always outranks it.
- **Misdirected-key guard** — a pasted key whose prefix unambiguously belongs to another vendor is caught before it is stored. GroqCloud and xAI are different companies one letter apart.

### 🖥️ Linux edge devices

- **A per-board deployment matrix** (`docs/edge_deployment.md`) for Raspberry Pi 5/4, first-gen Jetson Nano, amd64 mini-PCs, arm64 SBCs and RISC-V — including the two Linux gotchas that fail *silently*: `audio_cgo` for on-device speaker output, and bubblewrap where Landlock is unavailable.
- **`scripts/edge-setup.sh`** — arch/board detection, consent-gated installs, and a SHA-256-verified Ollama install that fails closed. It refuses Ollama on the Jetson Nano, the one board in the matrix that cannot run it, and points at the cloud path instead.
- **`/doctor` gained an edge section**: board, build flavour, the confinement backend *actually* in force, recorder presence, per-sidecar reachability, and thermals with a throttling verdict.

### 🛡️ New security & privacy controls

Voice is treated as an **untrusted input channel**, because a television, a podcast or a person in the room becomes text with user authority the moment it is transcribed.

- **Risk capped at Medium** from voice, whatever the phrasing, with a spoken refusal.
- **Typed confirmations stay typed** — force push, hard reset, worktree clean, deleting main. The voice prompter refuses them outright, so a perfect impersonation still cannot satisfy one.
- **Confirmations fail closed** — silence, timeout or an unintelligible answer counts as "no".
- **Spoken input never takes the shell fast path.** The classifier decides on the first token and English sentences start with command names, so "make a new branch called test" now reaches the planner that produces `git checkout -b test` instead of being executed verbatim.
- **Voice may reduce what is collected, never increase it.** "Turn off your eyes" and `/blackbox log off` work by voice; opening the camera is an explicit announced act and starting a transcript log must be typed.
- **Opt-in transcript log** (`/blackbox log on`) — off by default, and off means *no directory and no file*. Text and metadata only, never audio. 0600, rotated, `/purge`-able.
- **Three packages are provably network-free** (diagnostics, journal, metrics), each grep-enforced in CI.

Full model: `docs/threat_model_voice.md`. Policy summary: `docs/SECURITY.md`.

### 📊 Measured, not asserted

`/blackbox stats` reads the latency and liveness samples Helix records locally and grades them against the project's targets — local and cloud paths separately, because they have different budgets. Measured on an M-series Mac:

| What | Measured | Target |
| :--- | :--- | :--- |
| Wake detection (energy engine, fixtures) | **100 %** with **0** false positives | ≥97 % |
| Local STT word accuracy (whisper.cpp `base.en`) | **97.0 %**, slowest 133 ms | ≥90 % |
| Local TTS time-to-first-audio (Piper) | **103 ms** | ≤1.5 s |
| Ambient noise classification (57 fixtures) | **100 %** | ≥90 % |
| Wake detection CPU, continuous | **0.0014 %** duty cycle | — |
| Ambient analysis CPU, continuous | **0.038 %** duty cycle | <5 % |
| Frame-to-insight, local `gemma4:e2b` warm | **8.8 s** | best-effort locally |

The report refuses to flatter: it will not print a p95 a small sample cannot support, it says "not measured" rather than implying a pass, and where the median meets a budget but the worst case does not it says **typical only**.

### 🔧 Fixed after real-hardware testing

A live session on a second machine surfaced defects no test had reached:

- **A spoken sentence longer than 12 seconds was cut mid-word**, the truncated half answered,
  and the remainder delivered as a *separate turn with its own answer*. A stopwatch was doing
  the endpointing. Silence does it now; the duration cap is a backstop against a stuck mic.
- **Every plan needing a critic review was quarantined**, so Helix could only chat. The critic's
  token budget was too small to hold its own verdict, so it returned nothing, and nothing
  fails closed. Fail-closed is unchanged — but a critic that said nothing no longer reads to
  the user as a refusal of their request.
- **piper-local could never have started.** Its presence check tested for `python3`, which
  exists everywhere, so setup skipped the install, downloaded a 60 MB voice, and died on
  `ModuleNotFoundError`. It now verifies the module before the download.
- **"Voice link configured." printed above its own contradiction** — the success line came
  before the verification that disproved it.
- `/doctor` suggested `ollama pull <cloud-model-name>`, and the port-collision report named a
  sidecar's old port after the wizard had moved it. Both fixed.

### ✨ Interface

The terminal UI is the whole product surface here, so it gets the same honesty rules as
everything else: a panel may not report a state the machine cannot deliver.

- **Status reports what it advertises.** `/blackbox status` gained the `WAKE` row it had
  promised in its own usage text since the command was created, and a `CONTEXT` row for
  retained conversation audio — the one privacy-relevant state that had no surface at all.
- **Guidance points at commands that exist.** Seven strings still told users to run `/wake`,
  `/say`, `/tts` and `/voice-status` — verbs folded into `/blackbox` and removed from the
  registry, so Helix was recommending commands it would then answer with "folded into
  /blackbox".
- **The recommended chain now says it is recommended.** The setup menu assigned the
  `recommended` tag and then overwrote it with `needs a key`, so the recommendation never
  rendered while its endorsement colour stayed on — painting a caution green. Both are
  shown now.
- **The wake panel stopped promising phrase detection it does not do.** It printed the
  configured phrase unconditionally, but the default energy detector scores loudness and
  cannot match words; a stored phrase is now reported as stored and unused.
- **The local voice chain no longer needs Python on Linux or Windows.** Piper was the one
  component that did. Helix now runs its standalone binary as a *persistent process* with the
  voice model resident — which is not only interpreter-free but **faster than the HTTP server it
  replaces**: ~55–66 ms per sentence once warm, against the server's 103 ms, because there is no
  HTTP hop and no per-sentence model reload. macOS keeps the Python server, because Piper's
  published macOS archives are missing the libraries they link against and its successor project
  ships Python wheels only. On edge boards the gate is `libstdc++` rather than glibc, and Helix
  checks before downloading rather than after.
- **Every status and report screen renders as a panel.** `/status`, `/rag-status`,
  `/knowledge-status`, `/cost`, `/context` and `/memory` were the last flat ones. `/cost` was
  also 92 columns wide against an 80-column terminal, so it wrapped at the edge and destroyed
  its own grid; the self-fitting table fixed that as a side effect. `/status` also got reordered — approval posture and the agentic
  harness open it, since they decide how much happens without being asked, and the four on/off
  switches collapsed into one line that names only what is on.
- **A fallback that fell back to itself.** Picking Ollama at first run made the primary and
  the offline fallback the same provider, so the status line read "armed — will switch to
  ollama if ollama fails". It now says the fallback is not applicable, and why.
- **A failed model download no longer ends the session.** First run on a clean machine, with
  Ollama's registry briefly returning `503`, printed "Setup failed:" and dropped the user back
  to their login shell — Helix exited because a *download* did not finish. Setup failures are
  now reported and survivable: the shell starts, `/doctor` names what is missing, and `/setup`
  finishes the job later. Registry errors are also classified instead of echoed, since "try
  again shortly" is right when the registry is down and wrong when the tag does not exist.
- **Text you were meant to read was nearly invisible.** The colour used for secondary
  prose measured **1.44:1** against a dark terminal — WCAG asks 4.5:1 for body text — so
  `/about`'s philosophy, panel labels and table headers rendered as dark grey on dark. One
  constant was carrying two incompatible jobs: panel rules, which should recede, and prose,
  which must be read. Split in two, with the new tones drawn from the Tron Legacy palette and
  lifted until they measure: secondary text at 6.1:1, values at 7.3:1, rules deliberately
  below. Helix's identity colours are unchanged, and the numbers are now enforced by test.
- **`/help` is readable again.** Its index padded commands into a fixed column and gave up
  when one ran long, so nine of fifty-six descriptions started at a different place — and
  nothing wrapped, so the widest row ran 124 columns against a 76-column rule. It now lists
  command *names* against one axis, with argument syntax in `/help <command>`, which has the
  width to be complete. The prompt diagram also explains the right-hand prompt — the clock and
  the Helix/Red Team/name ribbon — which it had never mentioned.
- **`/about`, `/help <command>` and the unknown-command screen** were the last three drawing
  themselves by hand. `/about` closed three sections with a rule that had no opening rule, and
  hand-wrapped its prose to one fixed width. A mistyped command and `/help <mistyped>` rendered
  the *same* error two different ways; they are one screen now.
- **Panels stay inside their frame.** Over-wide status values wrapped at the terminal edge
  and restarted at column zero, escaping the panel — including a camera message at 95
  columns in a 74-column row. Fixed in the primitive rather than in each caller: `shell.KV`
  now wraps at word boundaries and hangs continuation lines under the value column, joining
  the table and prose renderers so nothing can spill outside the frame. The wrapping is
  colour-aware — escapes are never counted toward a width or severed, and the active colour
  is closed at a break and reopened on the next line — and a value that already fits is
  returned byte for byte unchanged, so no panel that rendered correctly before changed.
  The prose renderer was measuring byte length rather than columns, so panels built from
  `·`, `—` and `→` wrapped into twice as many lines as their width needed; and a word longer
  than the panel (a URL, an absolute path) was emitted whole, once rendering 188 columns into
  a 74-column frame. Table cells were cut to a count of runes rather than columns, so a
  CJK model name came back at twice its budget and shifted every column after it. All fixed,
  and every panel primitive now measures width through one shared definition.

### ⌨️ New commands

`/blackbox` (`on·off·status·setup·look·eyes·wake·tts·say·log·stats`) replaces eight separate voice verbs — typing an old name tells you where it went. Plus `/agentic`, `/memory`, `/undo`, `/listen`, `/mictest`, `/web`, `/todo`, `/plan`, `/review`, `/diff`, `/context`, `/cost`, `/tools`, `/hooks`, `/permissions`, `/export`, `/resume`, `/compact`, and `helix daemon` / `helix remote`.

### 🚧 Known limits, stated plainly

This project keeps an honest ledger, so here is what v1.5.0 does *not* do:

- **Hybrid mode is not reachable.** `input.HybridSource` exists and is unit-tested, but nothing constructs one: simultaneous typing and speaking would mean racing a blocking raw-mode line read against a voice capture, which is a change to the interactive loop rather than plumbing.
- **Music ducking was specified and is not implemented** — and as written it cannot be: Helix controls only its own voice, so "ducking" would make it *less* audible. Music is recognised and deliberately not remarked upon.
- **Cloud-path latency numbers are unmeasured.** The one figure anyone measured (2,280 ms TTS) was against the buffered path that streaming replaced, so quoting it would defame code that no longer exists.
- **Real keyword spotting needs the sidecar.** The default engine detects speech onset; it will wake on "hey helix", on "hello there" and on a dropped mug.
- **The 72-hour soak has not been run**, though the tooling and the verdict now exist (`scripts/soak.sh`, then `/blackbox stats`).
- **macOS camera access must be granted by hand** (System Settings → Privacy & Security → Camera). Until it is, the camera opens and delivers nothing — which Helix now says instead of hanging.
- **CSM-1B needs a GPU**, and its conversational context needs a patched sidecar. Unpatched
  servers silently ignore the context field — Helix detects and reports that rather than
  overstating, but the prosody benefit only arrives once `docs/csm-context.patch` (or an
  equivalent upstream change) is in place.
- **Full-duplex barge-in is parked; sentence-boundary interruption is not.** You can stop a reply by speaking in the gap *between* sentences (`/config barge-in on`, off by default) — the speaker is idle there, so no echo cancellation is needed. You still cannot talk *over* a sentence: that needs concurrent capture plus AEC, which conflicts with the CGO-free build unless a headset is assumed. `Ctrl+C` remains the instant, microphone-free stop.

### Upgrading from v1.0.0

Nothing is required. Voice is entirely opt-in: existing configs keep working, every new subsystem is off until you enable it, and typed behaviour is unchanged by design (the PTY end-to-end suite is the proof). To start talking:

```text
/blackbox setup     # pick a chain — or take the recommended one
/blackbox on        # go live
```

Say **"manual mode"** to get back to the keyboard.

---

## Helix v1.0.0 — The AI-Native Shell

Helix inverts the terminal paradigm: instead of forcing humans to speak machine, the machine learns to speak human. A single prompt accepts raw shell commands, natural-language requests, git workflows, package operations, and defensive threat-intelligence queries with zero mode switching. Every action flows through a multi-layer safety pipeline (Unicode-aware validation → risk tiering → directory sandbox → typed confirmations), delivering power without recklessness.

Helix is knowledge sovereign. Hundreds of thousands of CVEs, tens of thousands of exploit references, the full CISA KEV catalog, MITRE ATT&CK, and ~479 MAN pages living as ~1,000 vector documents — all in a SQLite file on your disk, searchable offline, synced with checkpointed, rate-limit-respecting patience. No cloud. No telemetry. Keys in a 0600 file. 

In an age when intelligence is treated as a subscription, Helix has made it a possession.

### Core Highlights

- **Unified input classification** — `ls -la`, `why is my build failing?`, and `/vuln CVE-2024-1234` coexist in one prompt.
- **Multi-provider AI** — OpenAI, Anthropic, DeepSeek, Kimi, Qwen, GLM, Ollama driven by a strict-JSON planner with truncation-resistant parsing.
- **Local RAG + live threat intel** — 900+ indexed MAN pages plus NVD, CISA KEV, Exploit-DB, and MITRE ATT&CK in a SQLite/FTS5 knowledge base.
- **Safety-first execution** — hard blocks for `rm -rf /`, `curl | sh`, and `eval`; confirmations for medium risk; critical-package protection.
- **Authorized recon** — `/scan` requires written scope; dangerous flags are blocked by default.
- **Helix UX** — TrueColor animated prompt, live syntax highlighting, in-place resize healing, and synthetic tonal audio synced to the typewriter.

### 🛡️ Enterprise Hardening

What separates "portfolio-grade" from "enterprise-grade" is verified assurance. Helix v1.0.0 closes the gap with six mathematically defensible hardening pillars:

- **Supply-Chain Security:** Every release artifact ships with an SPDX SBOM (via `syft`) and is cryptographically signed using Sigstore keyless signing (`cosign`). Continuous `govulncheck` and CodeQL SAST run on every commit.
- **Instruction Firewall:** RAG-retrieved knowledge is treated as untrusted data. A 5-layer defense (structured-fields-only context, sanitization, canary honeypots, a fail-closed critic pass, and provenance escalation) neutralizes indirect prompt injection.
- **Kernel-Grade Confinement:** `/sandbox strict` is no longer advisory. Writes outside the jail root are denied *by the OS kernel* using Seatbelt (macOS), bubblewrap (Linux), or the Landlock LSM via pure-Go raw syscalls.
- **Continuous Fuzzing:** The safety surface (shell validation, JSON planner parsing, sandbox path resolution) is continuously fuzzed with invariant assertions to prevent ReDoS and state-machine bypasses.
- **E2E TTY Harness:** A pseudo-terminal (PTY) test suite boots the real Helix binary against a mock provider, proving the safety pipeline end-to-end with zero real AI and zero network.
- **Telemetry-Free Crash Diagnostics:** Panics and fatal signals generate local, 0600, secret-redacted JSON crash reports. The diagnostics package imports zero networking primitives (grep-verified in CI), and reports are safely inspectable via the new `/crash` command.

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
Don't want to build from source? Download the latest pre-compiled binary, checksums, and archives for your OS directly from this **Releases Page**.

### Manual Build (For Developers)
If you prefer to build and run Helix locally without installing it globally:

```bash
git clone https://github.com/Nibir1/Helix.git
cd Helix
make current   # Builds the optimized binary
./dist/helix   # Launches Helix
```

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

### 🔐 Verifying Release Integrity

Every official Helix binary and archive is cryptographically signed and accompanied by a Software Bill of Materials (SBOM) to guarantee a clean supply chain.

**1. Install the verification tools:**
- [Cosign](https://docs.sigstore.dev/cosign/installation)
- [Syft](https://github.com/anchore/syft#installation)

**2. Verify the Sigstore Signature:**
```bash
cosign verify-blob \
  --certificate helix_Linux_x86_64.tar.gz.pem \
  --signature helix_Linux_x86_64.tar.gz.sig \
  --certificate-identity-regexp "https://github.com/Nibir1/Helix/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  helix_Linux_x86_64.tar.gz
```

**3. Inspect the SBOM:**
```bash
syft helix_Linux_x86_64.tar.gz
```