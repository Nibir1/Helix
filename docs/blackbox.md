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

One more transcription option is registered but deliberately not a preset:
`gpt-live-transcribe`, OpenAI's realtime model over a WebSocket
(`wss://api.openai.com/v1/realtime?intent=transcription`, $0.017/min). It gives
live partials from a vendor you probably already have a key for, and it captures
at **24 kHz** rather than the 16 kHz everything else uses — the server enforces
that as a minimum, so the recorder is opened to match. Select it with
`speech.stt.model = "gpt-live-transcribe"`. It is not recommended and not a
chain head because there is no streaming-STT failover: if it fails, the whole
voice path fails with it, and parts of its wire format were reconstructed rather
than read from a specification. `speech.stt.realtime` in `~/.helix/config.json`
overrides every one of those reconstructed parts, including a `session` field
sent to the server byte for byte.

`gpt-live-1` — the full-duplex model that speaks as well as listens — is **not**
supported and cannot be, yet. Its only transport is WebRTC: the session endpoint
answers `"Only the webrtc transport is supported."` and requires an SDP offer,
which means ICE, DTLS-SRTP and Opus rather than a WebSocket client. It is
selectable by typed model id like any other, but selecting it produces a session
nothing here can talk to.

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

- **The Python interpreter is chosen, not assumed.** `piper-local` needs one on
  macOS (no native binary exists there — upstream's archive ships no `.dylib`),
  and `python3` is not always the one that works: on an Intel Mac running Python
  3.14 it cannot be, because onnxruntime publishes no macOS x86_64 wheel for
  cp314. Setup discovers every interpreter on the host, prefers one that already
  has piper, then your own `python3`, then any other that pip says can install
  it — and names the one it picked. See
  [local_runtimes.md](local_runtimes.md) §3.7.
- **Private-by-default:** configure the local sidecars (`whisper-local` STT,
  `piper-local` TTS) to keep audio on your machine. See §5.
- `/blackbox status` is the one report: mode, hearing, sight, wake, initiative,
  retained context, interrupt method and transcript logging, followed by chain health, key state
  and recorder availability.
- `/blackbox say <text>` speaks text immediately; `/blackbox tts on|off` gates automatic spoken
  responses; `/listen [sec]` records and transcribes one clip (push-to-talk).

## 2. Voice mode and the safety valve

```text
/blackbox on   # open a conversation: microphone, camera, speech, initiative
/blackbox off  # end the conversation — back to STANDBY, mic still listening
```

**`/blackbox off` does not close the microphone.** It ends the conversation and
leaves Helix in STANDBY, where a wake word can start another one. The command
that closes the microphone is `/blackbox wake off`, or the spoken phrase
"manual mode". See §3 for the three states.

There are three ways out of a conversation, and they mean different things:

| you say | what happens |
|---|---|
| "manual mode", "close the mic", "keyboard mode", "blackbox off" | **MANUAL** — microphone closed and persisted. Only a typed command reopens it. |
| "you can turn off now", "go to sleep", "stand down", "stop listening" | **STANDBY** — the conversation ends, the wake word keeps listening. |
| "that's all", "we're done", "nothing else" | **STANDBY, after asking.** These are also how an ordinary request ends ("…and then deploy it, that's all"), so they always confirm first. |

Note the asymmetry in the first row: the *spoken* phrase "blackbox off" closes
the microphone, while the *typed* `/blackbox off` only returns to standby. The
spoken form is how people say "stop listening to me"; the typed form is how
people say "I'll type now", and the keyboard was never the thing at risk.

Every phrase is matched at the END of a sentence, so "okay, now switch to manual
mode" works and a question about the feature does not. Longest match wins across
all three lists, so "stop listening completely" closes the mic rather than
matching the "stop listening" that only pauses. Trailing politeness is trimmed:
"turn off the microphone please" works.

**A stop phrase heard on weak evidence asks before it acts,** and the
conversational closers ask every time regardless of evidence. Ending the
session is the one decision a mis-transcription can make that the user cannot
undo by speaking again, and it happened: three turns of room noise, the last
transcribed as *"Manual mode."*, and live mode ended by itself. So a stop phrase
now needs either a confidence the provider vouches for or a clip clearly above
the audible floor; short of that Helix asks *"Did you say manual mode? Say it
again and I will go."* — and the second time it goes, however weak, so a quiet
microphone cannot trap you. Nothing to measure (a streaming turn holds no single
clip) is trusted rather than refused, for the same reason.

Say **"reboot"** (or "please reboot") to restart the shell itself. It is matched
the same way and, like the safety valve, ends the turn rather than being answered
by it — a question such as "what happens when you reboot" is answered instead.
The restart comes back **in live mode**, in the same directory, on the same
provider and model, with the conversation intact and whatever you were working on
named on the way in. A reboot from STANDBY comes back in STANDBY with the
microphone still listening; only MANUAL comes back with it closed.

`/reboot` also **self-updates**, and does so automatically — from the microphone
as well as the keyboard, with no confirmation. That is an owner decision, and the
trade is worth knowing: the release comes from a repository the owner controls
and tags deliberately, so publishing it is the authorization; the cost is that a
bystander saying "reboot" can trigger an install. The download must still match
the release's checksum, come from GitHub, and prove it is a Helix binary for this
machine, and the supervisor restores the previous one if the new one cannot
start. `update.check: false` turns it off.

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
not Helix.

**A turn ends when you stop speaking, not when a timer runs out.** sox's silence
gate does the endpointing: capture begins on speech and ends after
`HELIX_SOX_SILENCE_SECS` (default `1.5`) below the noise floor. The 45-second cap
is a backstop against a microphone that never goes quiet — it is not a limit on
how long you may talk. It used to be 12 seconds and it used to be doing the
endpointing, which cut long sentences mid-word: the truncated half was
transcribed and answered, and the remainder arrived as a *separate turn with a
separate answer*. One thought became two half-conversations.

`HELIX_SOX_SILENCE_PCT` (default `1%`) tunes how quiet a voice can
be before sox treats it as silence.

## 3. Three listening states

Helix is always in exactly one of three states. The old model was a bool — live
or not — and it could not express the difference between "stop talking to me for
a minute" and "close the microphone", so both were the same list of phrases
doing the same thing, and saying either one left the wake word on to put you
straight back.

| | microphone | keyboard | leaves by |
|---|---|---|---|
| **STANDBY** *(the startup default)* | wake detection only — chunks are scored and discarded, nothing is transcribed | live | any sound → AWAKE · "manual mode" → MANUAL |
| **AWAKE** | transcribing **every turn, with no re-waking in between** | live | a stop phrase · `/blackbox off` → STANDBY · 10 minutes of silence → STANDBY |
| **MANUAL** | **closed** | live | `/blackbox wake on` → STANDBY |

The state is persisted on config keys that already existed, so there is no new
setting to learn and nothing to migrate:

```text
AWAKE    user_preferences.voice_mode: true
STANDBY  speech.wake_word.enabled absent or true
MANUAL   speech.wake_word.enabled: false
```

`/blackbox status` shows which state you are in; `/blackbox wake status` is the
detailed report.

### Waking is once per conversation, not once per turn

Until this changed, every single turn had to be re-woken: you said the wake
word, got one answer, and said it again. That made hands-free exhausting for
anything longer than a single question.

Now a wake opens a **conversation**. Helix keeps taking turns until you end it.
What ends it:

- any of the stop phrases in §2;
- `/blackbox off`, typed;
- **ten minutes with nothing said** — the inactivity stand-down.

The stand-down is the only one that is not your decision, and it exists because
AWAKE holds an open transcribing microphone: a conversation you walked away
from would otherwise keep listening, and keep billing per-minute STT, until the
shell exited. Configure it with `speech.wake_word.awake_idle_stand_down_s`
(default `600`; `0` disables it entirely, which removes the bound).

It is measured from the last sign of a person, not from the last successful
transcript — a clip that was clearly speech but came back misheard still counts.
Being misunderstood should not stand you down.

### A short grace window after standby

Entering STANDBY drops wake events for a moment
(`speech.wake_word.rearm_delay_ms`, default `3000`; `5000` after a spoken exit).

This is not a nicety. With the default energy engine a "wake event" is speech
**onset**, not a phrase — so the tail of the very sentence asking Helix to stand
down is itself a wake, and without the window you are handed straight back into
the conversation you just left. Set it to `0` for the old instant re-arm.

### Type while it listens

The keyboard is live **during a capture**, not only at an idle prompt. Start
typing mid-turn and the capture is cancelled, the partial clip is discarded
without being transcribed, and your line is read as if you had typed it a moment
later.

The clip is discarded on purpose. A killed recorder can still hand back a
fragment with no error, and transcribing half a sentence risks it coming back as
a stop phrase and ending the conversation you were in the middle of.

The cost, stated because it is real: once a keystroke wins, the microphone is
closed for as long as you are typing that line. Speaking mid-line is not heard.

Turn it off with `speech.wake_word.awake_keyboard: false` and AWAKE takes
voice-only turns, leaving the terminal untouched. It is unavailable on Windows,
which has no termios; there AWAKE is voice-only regardless.

### One switch, and it is already on

```text
/blackbox wake on       # STANDBY: listen for a wake at the prompt
/blackbox wake off      # MANUAL: close the microphone
/blackbox wake status   # state, detector, recorder, and whether the prompt is armed
```

**This is on by default** (owner decision, 2026-09-09). A fresh install starts
in STANDBY: make any sound and Helix wakes, keep typing and nothing changes.
That is the point — the keyboard and the microphone at the same time, with
nothing to activate.

`speech.wake_word.always_listen: false` un-arms the idle prompt without closing
the microphone entirely. It used to also mean "listen between spoken turns
only"; there are no gaps between turns to listen in any more, so that half of
its meaning is gone.

**Upgrading from a build before 2026-09-09? Type `/blackbox wake on` once.**
Your config almost certainly holds a literal `"enabled": false` that you never
chose: the old build stored this as a plain `bool`, and a plain bool is always
written out, so every save recorded its zero value. Under the three-state model
that config now has a name — you start in **MANUAL**, with the microphone
closed. The new default reaches a config where the key is **absent**, and it
deliberately does not override an explicit `false`, because that is the opt-out
this document tells you to use and resurrecting it would open a microphone on a
guess.

Both keys are tri-state (`*bool`) for that reason: absent, `true` and `false`
are three different answers. A plain `bool` collapses the first two, and the
first cut of this shipped that way — on by default in the code, off in every
session that had a config file, with a passing test to match.

Three more things worth knowing:

- **Voice can reduce listening but never reopen it.** A spoken phrase can move
  you to STANDBY or to MANUAL. Only a *typed* command can come back out of
  MANUAL — enforced in the transition itself, not just in the command handler,
  so it holds for every door. ADR-005: a transcript carries user authority with
  no proof of who spoke.
- **With the default engine, any sound wakes it** — a cough, a door, a sentence
  meant for someone else. The energy detector scores loudness against the room's
  own noise floor, not words. For a prompt that answers only to "hey helix", run
  the sidecar engine (§5.1).
- **Typing wins at an idle prompt too.** A word spoken mid-line is not seen
  until you submit it, so the editor behaves identically whether the prompt is
  armed or not.

### Full duplex, if you want it

The three states above describe *when* Helix listens. `gpt-live-1` changes *how*
a turn is taken inside AWAKE: the microphone stays open for the whole
conversation, you can talk over Helix mid-sentence, and the model decides when
you have finished speaking rather than a silence timer deciding for it.

```text
/blackbox setup     # pick openai / gpt-live-1 when it asks what should hear you
```

It is a row in the STT table like any other provider, and choosing it is the
whole switch — there is no separate on/off. `/blackbox status` grows a `DUPLEX`
row once it is picked.

Three things to know before you turn it on, none of which is a detail:

- **It bills while it is open** — $0.05 a minute, per second, on top of your
  planner's model. The ten-minute stand-down is what stops a conversation you
  walked away from running up a bill.
- **It needs libopus** (`brew install opus` / `apt install libopus0`). Without
  it Helix says so in `/blackbox status` and uses the ordinary chain.
- **It paraphrases.** Helix therefore never gives it exact output: paths,
  hashes, versions and error lines stay on the screen and it says *"done — it's
  on screen"* instead. `docs/voice.md` §7c has the measurements behind that,
  including the one where it deleted a path and invented an explanation.

You do not need headphones: the service cancels its own voice out of the
microphone, and it was measured doing so on built-in speakers at three volumes
while still hearing a real voice talking over it. That is what makes
interrupting it work.

Everything else is unchanged — same stop phrases, same three states, same
pipeline, and a destructive action still needs a typed confirmation. What is
new is that typing one now works mid-conversation: Helix mutes the session
first, so nothing in the room can answer the prompt for you.

### How loud is loud enough

The energy detector measures the room and wakes on speech that rises above it,
rather than comparing against a fixed number. That is a correction: it shipped
with absolute thresholds (balanced `0.12` normalized RMS) fitted to synthetic
test tones, and on real hardware it could not fire at all. A MacBook Pro
built-in microphone at input volume 38 measures about `0.0011` RMS for a quiet
room and `0.012`–`0.033` for an audible voice — so the old bar sat roughly 25×
above anything the device could produce, and the prompt said "listening"
forever.

The presets are now multiples of the measured floor: `strict` 4×, `balanced`
2.5×, `loose` 2×, with an absolute audibility gate underneath so a muted or
permission-denied microphone cannot wake on its own dither. `HELIX_WAKE_RATIO`
and `HELIX_WAKE_MIN_RMS` override them for a room the heuristic reads wrongly.
`HELIX_DEBUG=1` prints the level and the bar for every chunk, which is the fast
way to tell "the room is too quiet" from "the microphone is not working".

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

### Restarting Helix is not restarting the daemon

`/reboot` restarts the **interactive shell** and supervises the replacement from
inside its own process — the launching terminal sees one job throughout, and
systemd or launchd sees nothing at all. The daemon's supervision is the opposite
shape: a service manager restarting `helix daemon`, which has no terminal. The
two never interact, and a `/reboot` leaves a running daemon alone.

Local sidecars are also left alone: whisper.cpp, llama-server and the persistent
Piper process all survive a restart with their models resident, so rebooting the
shell costs seconds rather than gigabytes of reloading.

## 5. Local sidecars (private default)

BlackBox keeps the CGO-free single binary by delegating local models to
external HTTP services — the same pattern Helix already uses for Ollama:

| Component | Service | Default endpoint | Route |
|-----------|---------|------------------|-------|
| Local STT | whisper.cpp `whisper-server` | `http://127.0.0.1:8080` | `/inference`, or `/v1/audio/transcriptions` on an OpenAI-shaped server — **discovered, not assumed** |
| Local TTS | Piper — a persistent local process on Linux/Windows, `http_server` on macOS | none (no port) / `http://127.0.0.1:5000` | n/a for the binary; `/`, or `/api/tts` on older Rhasspy builds — likewise discovered |
| Local TTS (natural) | Sesame CSM-1B via `csm.rs` | `http://127.0.0.1:28195` | `/v1/audio/speech` (OpenAI-shaped) |
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

**Sesame CSM-1B** with `context_turns` set is the closest Helix gets to the
Sesame demo: prosody conditioned on the last few turns rather than each sentence
synthesized cold. Helix assembles and sends that context today; a CSM server that
accepts it is the remaining piece, and until one does, Helix detects the refusal
once and falls back to ordinary synthesis for the session. See
[local_runtimes.md](local_runtimes.md) §3.6 — including why retention is
memory-only and off by default.

**Sesame CSM-1B** is the quality option: a 1B conversational speech model whose
prosody is conditioned on the dialogue, run through the Rust `csm.rs` sidecar —
**no Python, no Docker**. It wants a GPU (~8 GB VRAM) to stay ahead of playback;
on a machine without one, pair it with `piper-local` as the fallback so a slow
box simply uses the fast voice. `/blackbox setup` **builds it for you**: the compute
backend is detected rather than guessed — CUDA if `nvidia-smi` answers, Metal on
Apple Silicon, a tuned CPU build otherwise — and printed with its evidence
before anything compiles, so a choice you can see is not a choice made on your
behalf. Choosing this chain also enables **conversational context** (four turns), which
is the conditioning the name refers to — without it CSM synthesizes each reply
cold and is indistinguishable from the fallback. The one remaining step is
yours: the weights are licence-gated, and
consent tied to an account is not something a program supplies. Everything
around it is automated — the Hugging Face CLI is installed if missing, the terms
page opens in a browser, and `huggingface-cli login` runs so you can paste a
token — because none of those three is the decision. Per-platform
flags, the gated-weights step and an honest per-machine expectation table are in
[local_runtimes.md](local_runtimes.md) §3.5; `make live-csm` measures the
real-time factor on your own hardware.

**None of them needs Docker**, and neither does Helix. Kokoro is the one optional
container-hosted voice: Helix will not install a container runtime, refuses
early when no daemon answers, and marks it `no docker` in the provider table so
the constraint is visible before the choice.

**Live mode is left on your word, not on a timer.** Silence carries no budget —
Helix waits indefinitely for you to speak — and the only two ways out are "manual
mode" and `/blackbox off`.

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

On macOS the OS must also grant camera access to the terminal running Helix:
**System Settings → Privacy & Security → Camera**, then restart the terminal. An
unauthorised camera does not error — it opens and then delivers nothing — so a
capture gives up after 8 seconds and names that as the likely cause, rather than
stalling the turn for half a minute and quoting whatever ffmpeg last printed.

`/blackbox status` will not claim the camera is *watching* until a frame has
actually arrived. If every attempt has failed it reports **no frames**, because
ffmpeg being installed and the model being multimodal are both necessary and
neither is sufficient — an unauthorised camera passes both checks.

## 7. Ambient awareness (optional, opt-in)

`ambient.enabled` in `~/.helix/config.json` turns on rule-based sound awareness
(loud noise, alarm-like, music-like, silence) with per-category response modes
(`vocal|log|ignore`) and cooldowns so Helix never response-spams. Disabled by
default; runs only in full voice mode.

In `vocal` mode Helix offers one short line per category and then holds its
tongue: *"Are you okay?"* after a loud noise (10-minute cooldown), *"sounds like
an alarm. Want me to check?"* (5 minutes), *"I lost the sound of your voice. Want
me to repeat?"* after prolonged silence (2 minutes). **Music gets no remark at
all** — it is recognised so Helix can avoid commenting on it, not so it can talk
about it.

These are heuristics on energy and spectral shape, not a trained classifier: a
pure tone reads as alarm-like, broadband noise as a loud noise, many tones as
music. Real-world accuracy needs the classifier sidecar the roadmap defers; what
is measured today is the rules against the acoustic shapes they were written for
(100% on a 57-fixture corpus). It shares the wake loop's microphone stream and
costs about 0.04% of a core while live mode is on.

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
roadmap's §10 table; §10A records what has actually been measured so far, and
which rows still wait on a microphone, an API key, a human ear or a 72-hour
clock.

Three things it deliberately will not do, because a performance report that
flatters is worse than none:

- **It grades local and cloud separately.** They have different budgets (3s vs
  6s for a voice turn; 800ms vs 1.5s for first audio), so each sample is judged
  by the provider that produced it. A blended average would be measured against
  a threshold that applies to neither half.

  *Two ways this was getting the provider wrong, both fixed 2026-09-08.* A TTS
  sample recorded the **head of the failover chain** rather than the voice that
  actually spoke, so on a failover — local primary, cloud fallback — a 900ms
  cloud synthesis was filed under the local provider and passed the 1.5s local
  budget instead of missing the 800ms cloud one. And `csm-local` was absent from
  the list of local providers, so CSM — whose real-time factor is 1.69× by
  design, because it wants a discrete GPU — was graded against the cloud budget
  and every honest measurement of it read as a hard failure. A test now walks
  the speech registry and fails if any adapter's own `IsLocal()` disagrees with
  the metrics reader, in either direction.
- **It will not show a p95 it cannot support.** Below 20 samples you get the
  maximum, labelled as the maximum.
- **It distinguishes "typical" from "always".** When the median meets the budget
  and the worst case does not, the verdict reads *typical only* rather than
  *meets target* — that gap is the whole reason to look.

When the daemon has been running, a **daemon** panel reports availability:
heartbeats observed against heartbeats expected, restarts, and the longest gap
between them. The gap is shown next to the percentage on purpose — 99.5% of three
days is 21 minutes of downtime, and it matters a great deal whether that was one
outage or four hundred.

A heartbeat line whose timestamp cannot be parsed is **not** a data point, which
took a fix (2026-09-08). The reader keeps such a line on purpose — a latency or
category summary needs no clock — but it carries the zero time, and it used to
sort ahead of every real heartbeat: the gap to the first one was measured from
year 1 and printed as **2562047h**, the step up from its counter read as the
counter falling and invented a restart, and it still counted toward the observed
total while being excluded from the window. One malformed line in a 72-hour soak
was enough to report a phantom restart and a three-century outage.

The wake section reports events per hour and how many wakes produced no turn.
That second number is an **upper bound on false positives, not a measurement**:
Helix cannot tell a false trigger from someone changing their mind.

Samples live in `~/.helix/metrics/` (0600, local only, never transmitted) and
`/purge` wipes them.

## 10. Privacy & data layout

- **Keys:** `~/.helix/secrets.json` (0600), namespaced `stt.*` / `tts.*`.
- **Session:** `~/.helix/session.json` (0600) — `/memory clear` wipes it.
- **Update marker:** `~/.helix/update-pending` (0600) — a note from a restarting
  Helix to the supervisor waiting on it, saying a new binary was just installed
  so a bad one can be rolled back. Removed as soon as it is read.
- **Reboot record:** `~/.helix/reboot.json` (0600) — written only by `/reboot`,
  **deleted the moment the restarted shell reads it**, ignored past 12 hours.
  Holds the mode, working directory, provider/model and in-progress tasks —
  plus, **for a TYPED restart only**, a 240-character excerpt of your last
  message. A spoken reboot stores no conversation content at all, which is what
  lets "voice may reduce what is collected, never increase it" hold without an
  exception. Never a copy of the conversation either way.
- **Journals:** `~/.helix/journal/` (interactions, undo, vision metadata) —
  append-only, redacted, rotated at 1 MiB × 3 generations, `/purge` wipes all of it.
- **Voice log:** `~/.helix/voice_log/` — **absent unless you enable it** (§8);
  text only, same permissions and rotation, `/purge` wipes it.
- **Metrics:** `~/.helix/metrics/` — wake, voice, speech, vision, ambient and
  daemon-uptime samples; local only, read by `/blackbox stats` (§9).
- **No telemetry:** nothing leaves the machine without a provider + key you
  entered; the pricing catalog is embedded data + a local override
  (`~/.helix/pricing.json`).

`/purge` wipes keys, DBs, session memory, the reboot record and update marker,
journals, the voice log, metrics, and the daemon socket for a clean slate — then `/reboot` finishes
the job, because open database handles only release when the process exits.
