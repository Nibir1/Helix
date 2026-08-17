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
/voice-setup
```

The wizard prints a pricing table (provider, model, price, estimated $/month at
2h/day, latency, key requirement, locality, ★ recommended) for both
speech-to-text (STT) and text-to-speech (TTS), then collects API keys and
optional fallback providers. Fallbacks form a **failover chain**: the first
provider that succeeds wins, and failures are aggregated if the whole chain
collapses.

- **Private-by-default:** configure the local sidecars (`whisper-local` STT,
  `piper-local` TTS) to keep audio on your machine. See §5.
- `/voice-status` shows chain health, key state, and recorder availability.
- `/say <text>` speaks text immediately; `/tts on|off` gates automatic spoken
  responses; `/listen [sec]` records and transcribes one clip (push-to-talk).

## 2. Voice mode and the safety valve

```text
/voice     # enter voice mode (speak instead of type)
/manual    # instant fallback to typing
```

Voice mode replaces the typed line with a record→transcribe cycle. Transcripts
are stamped `Channel=voice` and pass through the **Voice Risk Policy** (ADR-005):

- Voice plans are capped at **Medium risk** — High-risk is unreachable by voice.
- Dangerous actions (force push, hard reset, clean worktree, delete main,
  critical package removal) always require **typed** confirmation; voice cannot
  satisfy them.
- Voice confirmations **fail closed**: silence, timeout, or an unintelligible
  answer = decline.

Mic-less machines are never stranded: `/voice` refuses entry without a
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
/wake on       # enable hands-free (applies safe defaults the first time)
/wake off      # disable
/wake status   # phrase, engine, recorder readiness
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
  `"manual mode"`, `/voice off`, `/manual`.
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

| Component | Service | Endpoint |
|-----------|---------|----------|
| Local STT | whisper.cpp server (OpenAI-compatible) | `http://127.0.0.1:8080` |
| Local TTS | Piper (`/api/tts`, WAV) | `http://127.0.0.1:5000` |
| Wake word | openWakeWord-class (`/predict`) | configured in `speech.wake_word.sidecar_url` |

Set `stt.provider = "whisper-local"` / `tts.provider = "piper-local"` in the
speech config section (or pick them in `/voice-setup`).

## 6. Camera vision (`/eyes`)

```text
/eyes on       # opt-in; announced vocally
/eyes off      # instant deactivation
/eyes status
```

Vision is **off by default**. When on, a *deictic* voice utterance ("what's
wrong with **this** code?", "read this serial number") captures a single frame
per turn via `ffmpeg`, downscales it to ≤1024px JPEG in memory, and sends it to
the configured vision-capable model. Privacy is enforced, not promised:

- Frames are **memory-only** — never written to disk (filesystem-snapshot test).
- Every frame batch is journaled as *metadata only* (provider + count +
  timestamp) to `~/.helix/journal/vision.jsonl`.
- `"turn off your eyes"` spoken phrase = `/eyes off`.
- If the active model cannot see, `/eyes on` refuses with guidance.

Requires `ffmpeg` on PATH (device flags are avfoundation on macOS, dshow on
Windows, v4l2 on Linux; override via `vision.provider`/device config).

## 7. Ambient awareness (optional, opt-in)

`ambient.enabled` in `~/.helix/config.json` turns on rule-based sound awareness
(loud noise, alarm-like, music-like, silence) with per-category response modes
(`vocal|log|ignore`) and cooldowns so Helix never response-spams. Disabled by
default; runs only in full voice mode.

## 8. Privacy & data layout

- **Keys:** `~/.helix/secrets.json` (0600), namespaced `stt.*` / `tts.*`.
- **Session:** `~/.helix/session.json` (0600) — `/memory clear` wipes it.
- **Journals:** `~/.helix/journal/` (interactions, undo, vision metadata) —
  append-only, redacted, `/purge` wipes all of it.
- **Metrics:** `~/.helix/metrics/` (wake events) — local only.
- **No telemetry:** nothing leaves the machine without a provider + key you
  entered; the pricing catalog is embedded data + a local override
  (`~/.helix/pricing.json`).

`/purge` wipes keys, DBs, session memory, journals, metrics, and the daemon
socket for a clean slate.
