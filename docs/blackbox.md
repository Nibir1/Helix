# Helix BlackBox — Voice-First AI Companion (User Guide)

BlackBox transforms Helix from a typed CLI into a multimodal, always-on, voice-first
assistant. This guide covers setup, the voice loop, the wake word, the Living AI
daemon, camera vision, and the privacy controls behind each.

> Governing documents: `BlackBox_Development.md` (roadmap + ADRs) and
> `threat_model_voice.md` (V1–V8 threat model). Every feature here passes through
> Helix's unchanged safety pipeline — classify → plan → firewall → risk tiers →
> sandbox. No input channel bypasses it.

## 1. First-time voice setup

```text
/blackbox setup
```

On a fresh install this runs automatically as part of first boot, after the AI
provider and the system-package stage (`/setup` re-runs any stage later).

### Recommended chains (the one-pick route)

The wizard opens with three pre-worked answers, because for most people the
provider tables are the escape hatch rather than the decision:

| Chain | Hears you | Answers you | Why |
| :--- | :--- | :--- | :--- |
| **Cheapest cloud** ★ | `groq` whisper-large-v3-turbo | `openai` gpt-4o-mini-tts | large-model accuracy at ~$0.04/hr (ADR-011) |
| **Lowest latency** | `deepgram` nova-3 | `deepgram` aura-2 | streaming partials, ~300 ms first byte |
| **Fully local / private** | `whisper-local` | `piper-local` | no key, no per-call cost, nothing leaves the machine, no Docker |

Each cloud chain pre-fills a **local** fallback, because the point of a fallback
is surviving the failure most likely to happen — the network — and a second
cloud vendor does not. The local chain deliberately has no fallback: adding a
cloud one would quietly undo the reason you picked private.

Picking a chain does not skip any step. Keys are still requested and verified,
sidecar ports are still assigned and probed, and the chain is still verified
before the wizard claims success — a preset fills in answers, it does not take
shortcuts. "Choose manually" is always the last option.

### Choosing it yourself

The wizard prints a pricing table (provider, model, estimated $/month at 2h/day,
latency, what it requires, ★ best value) for both speech-to-text (STT) and
text-to-speech (TTS), then collects API keys and optional fallback providers.
Fallbacks form a **failover chain**: the first provider that succeeds wins, and
failures are aggregated if the whole chain collapses.

A key you have already given Helix is reused rather than requested again: a
saved key is verified, and only one the provider actually *rejects* prompts for
a replacement. A key entered for an AI provider is adopted for the same vendor's
speech services.

- **Private-by-default:** configure the local sidecars (`whisper-local` STT,
  `piper-local` TTS) to keep audio on your machine. See §5.
- `/blackbox status` shows chain health, key state, and recorder availability.
- `/blackbox say <text>` speaks text immediately; `/blackbox tts on|off` gates automatic spoken
  responses; `/listen [sec]` records and transcribes one clip (push-to-talk).

## 2. Voice mode and the safety valve

```text
/blackbox on   # go live: microphone, camera, speech, and initiative
/blackbox off  # instant fallback to typing
```

Say **"manual mode"** to leave without touching the keyboard — matched at the
end of a sentence, so "okay, now switch to manual mode" works and is not
confused with a question about the feature.

Live mode replaces the typed line with a record→transcribe cycle. Transcripts
are stamped `Channel=voice` and pass through the **Voice Risk Policy** (ADR-005):

- Voice plans are capped at **Medium risk** — High-risk is unreachable by voice.
- Dangerous actions (force push, hard reset, clean worktree, delete main,
  critical package removal) always require **typed** confirmation; voice cannot
  satisfy them.
- Voice confirmations **fail closed**: silence, timeout, or an unintelligible
  answer = decline.

Mic-less machines are never stranded: `/blackbox on` refuses entry without a
recorder, and a failed capture offers one typed turn while staying in voice
mode.

**Listening is self-healing.** Each turn is amplitude-gated *before* the STT
round-trip: a dead mic or a silent room doesn't burn a cloud transcription or
return a confusing empty result — Helix says *"I didn't catch that — please
speak again"* and re-arms the mic (up to 3 times) before offering typed
fallback. Empty transcriptions from a provider also count as retryable, and a
provider that returns silence hands off to the next fallback in the chain.

**Is the AI hearing you?** Run `/mictest` — it captures 3s, reports the
recorder, the captured duration, the measured level (RMS + dBFS), and whether
that clears the speech gate. A `QUIET` result points at the OS input device,
not Helix. `HELIX_SOX_SILENCE_PCT` (default `1%`) tunes how quiet a voice can
be before sox treats it as silence.

## 3. Hands-free wake word

```text
/blackbox wake on       # enable hands-free (applies safe defaults the first time)
/blackbox wake off      # disable
/blackbox status   # phrase, engine, recorder readiness
```

The command does the config edit for you (no manual JSON): enabling applies the
defaults (phrase `"hey helix"`, engine `"energy"`, preset `"balanced"`),
persists them, and tells you how to go always-on. Equivalently, set
`speech.wake_word.enabled = true` in `~/.helix/config.json`.

Once on, Helix holds in **wake-only listening** between turns — nothing is
transcribed until a wake event fires, so ambient speech between turns is never
executed (ADR-005 §5).

- **Interactive shell:** after each turn it listens for the phrase, beeps, and
  runs the next turn — keep the terminal open and talk hands-free.
- **Always-on (no terminal):** run `helix daemon` — its own voice loop does
  wake → capture → pipeline → spoken reply → wake, forever. `helix remote
  status` reports `wake_enabled` / `wake_phrase` / `voice_loop` so you can
  confirm hands-free is live. `helix daemon install` registers it on login.
- `engine: "energy"` (default) detects loud-sound onset everywhere with zero
  dependencies; `engine: "sidecar"` points at an openWakeWord-class HTTP
  service for true keyword spotting.
- Kill switches, spoken or typed: `"stop listening"`, `"go to sleep"`,
  `"manual mode"`, `/blackbox off`, `/blackbox off`.
- Wake events append to `~/.helix/metrics/wake.jsonl` (local only).

## 4. The Living AI daemon

```bash
helix daemon              # run in the foreground (service managers supervise)
helix remote status       # health, STT/TTS chains, recorder
helix remote submit "..." # run one input through the pipeline
helix remote say "..."    # speak text
helix remote voice|manual # switch interaction mode
helix remote logs         # tail the interaction journal
helix remote stop         # graceful shutdown

helix daemon install      # launchd (macOS) / systemd user unit (Linux) / sc.exe (Windows)
helix daemon status       # is a daemon running?
helix daemon uninstall
```

The daemon owns session memory, the safe-subset undo journal, the wake loop, and
a connectivity monitor. On network loss it flips STT/TTS to local-first chains
and speaks a notice; on restore it switches back. It refuses IPC `submit` while
an interactive TTY session holds the active-session lock.

- **Session memory** persists the last 20 turns (`~/.helix/session.json`, 0600)
  and injects them into the planner as *data-only* context. `/memory show|clear`.
- **Undo:** after a `git commit`, say or type `"undo that"` — Helix offers
  `git reset --soft HEAD~1` through the full safety pipeline.

## 5. Local sidecars (private default)

BlackBox keeps the CGO-free single binary by delegating local models to
external HTTP services — the same pattern Helix already uses for Ollama:

| Component | Service | Default endpoint | Route |
|-----------|---------|------------------|-------|
| Local STT | whisper.cpp `whisper-server` | `http://127.0.0.1:8080` | `/inference`, or `/v1/audio/transcriptions` on an OpenAI-shaped server — **discovered, not assumed** |
| Local TTS | Piper `http_server` | `http://127.0.0.1:5000` | `/`, or `/api/tts` on older Rhasspy builds — likewise discovered |
| Wake word | openWakeWord-class (`/predict`) | configured in `speech.wake_word.sidecar_url` | `/predict` |

Both speech adapters try the known routes in order and remember which answered,
because both once shipped pointing at a single route their upstream servers do
not serve — a stock `whisper-server` answers at `/inference`, and Piper at `/`,
so every request came back 404 and local speech was unusable as shipped.
`/blackbox status` prints the route that actually answered. Verified against the
real binaries, not just mocks: see
[local_runtimes.md](local_runtimes.md) §3 for the one-line command.

Set `stt.provider = "whisper-local"` / `tts.provider = "piper-local"` in the
speech config section (or pick them in `/blackbox setup`).

**Neither needs Docker**, and neither does Helix. Kokoro is the one optional
container-hosted voice: Helix will not install a container runtime, refuses
early when no daemon answers, and marks it `no docker` in the provider table so
the constraint is visible before the choice.

Those default ports are frequently taken — whisper.cpp collides with llama.cpp
on 8080, and macOS AirPlay Receiver owns 5000. Helix moves a sidecar to a free
port and records it **per provider**, so a local service works as a fallback and
not only as the primary.

## 6. Camera vision (`/blackbox eyes`)

```text
/blackbox eyes on       # opt-in; announced vocally
/blackbox eyes off      # instant deactivation
/blackbox status
```

Vision is **off by default** and opens with `/blackbox on`, as part of one
announced consent moment rather than a separate switch. Once on, `/blackbox
look` and the planner's `vision` tool each capture a single frame via `ffmpeg`,
downscale it to ≤1024px JPEG in memory, and send it to the configured
vision-capable model. Both entry points are explicit: Helix no longer guesses at
the camera from words like "this" (see docs/voice.md for why that was removed).
Privacy is enforced, not promised:

- Frames are **memory-only** — never written to disk (filesystem-snapshot test).
- Every frame batch is journaled as *metadata only* (provider + count +
  timestamp) to `~/.helix/journal/vision.jsonl`.
- `"turn off your eyes"` spoken phrase = `/blackbox eyes off`.
- If the active model cannot see, `/blackbox eyes on` refuses with guidance.

Requires `ffmpeg` on PATH — `/setup` offers to install it. Device flags are
avfoundation on macOS, dshow on Windows, v4l2 on Linux; the capture rate is
negotiated from what the device reports rather than assumed. Route frames to a
different model than chat with `vision.model`.

On macOS the OS must also grant camera access to the terminal running Helix. An
unauthorised camera does not error — it opens and then delivers nothing — so a
capture that produces no frame before its deadline says so explicitly instead of
looking like a hang.

## 7. Ambient awareness (optional, opt-in)

`ambient.enabled` in `~/.helix/config.json` turns on rule-based sound awareness
(loud noise, alarm-like, music-like, silence) with per-category response modes
(`vocal|log|ignore`) and cooldowns so Helix never response-spams. Disabled by
default; runs only in full voice mode.

## 8. The voice interaction log (opt-in, off by default)

Nothing you say is written to disk unless you ask for it.

```text
/blackbox log on        # start recording transcripts and replies
/blackbox log off       # stop; existing entries are kept until /purge
/blackbox log status    # is anything being recorded, and where
/blackbox log show [n]  # read the last n entries (default 20)
```

With it **off there is no directory and no file** — that is the guarantee, not
just an empty log. Turning it on creates `~/.helix/voice_log/voice.jsonl`
(0600, in a 0700 directory) and records, per utterance:

- what Helix **heard**, the STT provider that transcribed it, that provider's
  confidence, and **what the pipeline did about it** — dispatched to the
  planner, matched a spoken command, hit a kill phrase, or was refused by the
  Voice Risk Policy;
- what Helix **said**, when speech was actually on;
- session notes such as "no speech detected — re-arming".

It records **text only, never audio.** Captured clips are deleted the moment
they are read and camera frames are never written at all, so there is no audio
file for the log to point at — and storing a path to a file that no longer
exists would be a liability that bought nothing.

The log **rotates** at 1 MiB and keeps three generations
(`voice_log.max_bytes` / `voice_log.keep_files` tune it), so an always-on
assistant cannot fill a small board's filesystem. `/purge` wipes it, and
`/blackbox status` shows whether it is recording next to the microphone and
camera states.

**Voice can stop the log but never start it.** Switching on a store of
everything the microphone hears moves your privacy posture, which is why
ADR-005 keeps `/config` and `/stealth` off the voice surface; enabling the log
has to be typed. Turning it *off* by voice always works, because a privacy
control should fail toward collecting less.

## 9. Measured performance (`/blackbox stats`)

```text
/blackbox stats
```

Helix records a local sample every time it does something with a latency target:
a wake-to-execution turn, a spoken reply's time-to-first-audio, a camera
frame-to-insight. `/blackbox stats` summarizes them against the targets in the
roadmap's §10 table.

Three things it deliberately will not do, because a performance report that
flatters is worse than none:

- **It grades local and cloud separately.** They have different budgets (3s vs
  6s for a voice turn; 800ms vs 1.5s for first audio), so each sample is judged
  by the provider that produced it. A blended average would be measured against
  a threshold that applies to neither half.
- **It will not show a p95 it cannot support.** Below 20 samples you get the
  maximum, labelled as the maximum.
- **It distinguishes "typical" from "always".** When the median meets the budget
  and the worst case does not, the verdict reads *typical only* rather than
  *meets target* — that gap is the whole reason to look.

The wake section reports events per hour and how many wakes produced no turn.
That second number is an **upper bound on false positives, not a measurement**:
Helix cannot tell a false trigger from someone changing their mind.

Samples live in `~/.helix/metrics/` (0600, local only, never transmitted) and
`/purge` wipes them.

## 10. Privacy & data layout

- **Keys:** `~/.helix/secrets.json` (0600), namespaced `stt.*` / `tts.*`.
- **Session:** `~/.helix/session.json` (0600) — `/memory clear` wipes it.
- **Journals:** `~/.helix/journal/` (interactions, undo, vision metadata) —
  append-only, redacted, rotated at 1 MiB × 3 generations, `/purge` wipes all of it.
- **Voice log:** `~/.helix/voice_log/` — **absent unless you enable it** (§8);
  text only, same permissions and rotation, `/purge` wipes it.
- **Metrics:** `~/.helix/metrics/` — wake, voice, speech, vision and ambient
  samples; local only, read by `/blackbox stats` (§9).
- **No telemetry:** nothing leaves the machine without a provider + key you
  entered; the pricing catalog is embedded data + a local override
  (`~/.helix/pricing.json`).

`/purge` wipes keys, DBs, session memory, journals, the voice log, metrics, and
the daemon socket for a clean slate.
