# Live mode — `/blackbox`

`/blackbox on` turns Helix into a spoken interface: you talk, it plans, it acts,
it answers out loud — and it watches. One command opens the microphone, the
camera, the spoken replies, and the companion loop that lets Helix speak up
without being asked. Eight commands (`/voice`, `/manual`, `/voice-setup`,
`/voice-status`, `/wake`, `/say`, `/tts`, `/eyes`) folded into it; typing an old
name prints where it went.

This document covers what the voice channel can reach, what it deliberately
cannot, and where the honest limits are.

Voice is an **untrusted input channel** — see
[threat_model_voice.md](threat_model_voice.md) for the full model. A transcript
arrives with the authority of a typed line but with no proof of who spoke, and
everything below follows from that.

---

## 1. The turn

```
wake phrase (optional)
  └─▶ capture ──▶ transcribe ──▶ confidence gate
                                   │
                                   ├─▶ kill phrase?        → leave voice mode
                                   ├─▶ "turn off your eyes"→ camera off, stay in voice
                                   ├─▶ spoken COMMAND?     → dispatch, speak the answer
                                   └─▶ otherwise           → planner → safety pipeline
```

With a streaming STT provider (Deepgram), interim words appear as you speak and
the turn finalizes on the utterance-final result or ~3 s of silence. Batch
providers record a clip and transcribe it. Spoken replies stream sentence by
sentence, so the first word plays after roughly one short sentence's synthesis
rather than the whole paragraph's.

---

## 2. Spoken commands

The gap this closes: a transcript never contains a `/`, so the entire slash-
command surface used to be unreachable by voice. Voice could hold a conversation
and run planner-generated steps, but it could not ask for `/status`, look at a
`/diff`, add a `/todo`, run a `/plan`, or search the `/web` — most of what the
harness is for.

Say the plain-English phrase. `/blackbox status` prints the full vocabulary; a
sample:

| Say | Runs |
| :--- | :--- |
| "status" · "how are you doing" | `/status` |
| "what's this costing" | `/cost` |
| "what's on my list" | `/todo` |
| "add a task fix the parser" | `/todo add fix the parser` |
| "mark task three done" | `/todo done 3` |
| "plan a migration of the config loader" | `/plan …` |
| "what changed" | `/diff` |
| "review my changes" | `/review` |
| "search the web for …" | `/web …` |
| "undo that" | `/undo` |
| "what do you see" | `/blackbox look` |
| "run diagnostics" | `/doctor` |
| "turn on agentic mode" | `/agentic on` |
| "stop talking" | `/blackbox tts off` |

Anything without a phrase is reachable by saying **"slash \<command name\>"** —
"slash provider status", "slash knowledge status", "slash dry run". Hyphens are
spoken as separate words.

Number words become digits, so "mark task three done" and "mark task 3 done" are
the same request.

### What voice cannot reach

Default-deny: a command is voice-reachable only if it is marked so in the
registry. Unreachable by design:

- **Irreversible**: `/purge`, `/rag-reset`, `/rag-rebuild`, `/knowledge-update`
- **Changes what runs unattended**: `/permissions <mode>`, `/sandbox <mode>`,
  `/config <key> <value>`, `/hooks add`, `/stealth`
- **Mutates the repository or config**: `/commit`, `/init`, `/setup`,
  `/provider use`, `/model use`, `/resume`
- **Authorizes scanning**: `/scan authorize`

`/permissions` and `/sandbox` are *readable* by voice and not settable — you can
ask what the posture is, and changing it has to be typed. The refusal is spoken,
never silent: a misheard phrase tells you it was refused rather than quietly
doing something else.

---

## 3. Safety in the voice channel

These hold regardless of the approval posture:

1. **Risk is capped at Medium.** A high-risk command is unreachable from voice
   whatever the phrasing, and the refusal is spoken.
2. **Typed confirmations stay typed.** Force push, hard reset, worktree clean,
   deleting the main branch — the voice prompter refuses `AskTypedConfirmation`
   outright.
3. **Confirmations fail closed.** Timeout, silence, or an unintelligible answer
   counts as "no".
4. **Low-confidence transcripts ask again** rather than executing, when the STT
   provider reports a score below the gate.
5. **Hooks still run.** A blocking pre-hook can refuse a spoken step exactly as
   it would a typed one.

---

## 4. Hands-free

`/blackbox wake on` holds the microphone in wake-only listening between turns — no
transcription happens until the wake phrase fires, so nothing leaves the machine
while you are not addressing it. After a completed turn Helix returns to wake-only
listening; when the idle window lapses it says so once rather than silently
reverting to open capture.

Kill phrases end voice mode without touching the keyboard. "Turn off your eyes"
disables the camera immediately while staying in voice mode.

---

## 5. Eyes

`/blackbox eyes on` opts into the camera. Frames are captured **to memory only** and never
written to disk.

`/blackbox on` opens it as part of going live — one announced consent moment
rather than a second switch to remember. `"turn off your eyes"` closes it
without ending the conversation.

Two ways to use it, and both are explicit:

- **Ask outright**: `/blackbox look [question]`, typed or spoken.
- **Let the planner decide**: `vision` is a tool in the closed harness
  vocabulary, so "turn on the camera and tell me what you see" plans a `vision`
  step. It reaches the same capture path — one in-memory frame — and with the
  camera off the step says so rather than reaching for a shell workaround.

There used to be a third: any spoken sentence containing "this", "that" or
"here" was routed to the camera before the planner ever saw it. It is **gone**.
Demonstratives are far too common to be an intent signal — "what do we have in
*this* directory?" got a description of the room, and "show me the commands in
*this* helix" was answered by a vision model that had never heard of Helix. The
planner has a camera tool now, and a model choosing between its own tools beats
a substring match on English.

### The companion loop

In live mode Helix also looks on its own schedule. Every `companion.interval_s`
(default 20s) it captures one frame and compares it — in memory — against the
last frame it actually asked a model about. If the scene has not moved past
`companion.change_threshold`, **no model call happens at all**: an unchanged room
costs nothing, which is what makes the loop affordable on a local vision model.

When the scene has moved, the model is asked whether anything is worth saying,
and told to answer with a sentinel if not. A remark that survives that is capped
to one sentence and rate-limited by `companion.cooldown_s` (default 45s).

```json
"companion": { "enabled": true, "interval_s": 20, "cooldown_s": 45, "change_threshold": 0.08 }
```

`interval_s` is a **floor**, not a schedule: the gap before the next look is
`max(interval, how long the last look took)`, smoothed. On a fast host that is
just the interval; on a slow one the loop backs off by itself, so looking never
consumes more than about half the wall clock and never queues behind itself.
It deliberately does not speed up when looks return early — a companion is
bounded by how often a person wants to be spoken to, not by camera throughput.

### Choosing a vision model

`vision.model` routes frames to a different model than chat, so the companion
can run something small while your conversation keeps the big model:

```json
"vision": { "enabled": true, "provider": "ollama", "model": "gemma4:e2b" }
```

Measured on an M-series MacBook Air, through Helix's own wire path:

| Model | First token | Notes |
| :--- | :--- | :--- |
| `gemma4:e2b` (5.1B) | ~6.5s, repeatable | Correct, fluent. Needs ≥1024 max tokens — see below |
| `moondream:1.8b-v2-q4_K_M` | ~0.3s | **Unusable here**: returns nothing at all for instruction-style prompts |

Moondream is widely recommended as the small edge VLM, and its latency is real
— but Ollama's build is a VQA model, not an instruction-following one. Given the
companion's prompt it returns an empty string; given a slight rephrasing it
returns coordinate arrays like `ids: [0.39, 0.3, 0.57, 0.44]`. Only bare
questions ("What colors are in this image?") produce prose, and it hallucinated
flags in a test image of three stripes. Speed is not the binding constraint if
the output cannot be used.

**Reasoning models need headroom.** A thinking build spends its token budget
before it answers, and spends more of it on an image. `gemma4:e2b` at the old
512-token chat default produced ~770 characters of private reasoning and then
stopped, with no answer — which reached the user as "The vision model returned
nothing." Vision calls now use 1024, and a stream that is all thinking and no
answer reports that instead of returning empty.

Set `"enabled": false` to keep the camera and microphone but have Helix speak
only when spoken to.

### Helix's voice

Replies are shaped by a persona (`internal/agent/persona.go`), not by whatever
register the selected provider defaults to. It answers first, stays short, says
"I" about what it did, and offers an opinion when it has one.

A turn that will be **spoken** carries extra constraints the typed path does
not: no lists, no code blocks, no URLs, under about forty words, and never a
reference to anything "above" or "on screen" — the listener may not be looking.
That split is why a typed answer can still use a table.

The persona shapes tone and nothing else. It grants no capability, cannot
loosen a gate, and every safety control runs downstream of the text it produces.

### Docker is never required

whisper.cpp (STT) and Piper (TTS) are the local chain, and both need only a
binary and Python. Kokoro is the single optional component distributed as a
container: Helix will not install a container runtime, will not attempt a pull
when no daemon answers, and shows `needs docker` in the provider table so the
constraint is visible before the choice rather than after it.

Vision needs a genuinely multimodal model, detected from the model name. With a
local runtime reporting a placeholder name Helix cannot tell, and `/blackbox eyes on`
refuses — select the provider once (`/provider use llamacpp`) so the real model
name is resolved. See [local_runtimes.md](local_runtimes.md).

---

## 6. Honest limits

Worth stating plainly, because "realtime voice AI" implies things this does not
yet do:

- **Capture is half-duplex.** The recorder does not run while the speaker does,
  so you cannot talk over a reply and be heard. Interrupting a spoken reply works
  via **Ctrl+C**, and leaving voice mode (`/blackbox off`) stops it mid-sentence. True
  talk-over barge-in needs concurrent capture plus acoustic echo cancellation,
  which is not implemented — without AEC the microphone would hear Helix's own
  voice and transcribe it as your input.
- **"Stop talking" only lands between turns**, for the same reason.
- **The companion waits for a closed microphone.** An unprompted remark is
  queued, not spoken on the spot: it lands between a finished turn and the next
  capture, or by interrupting wake listening. That is the same half-duplex rule
  seen from the other side — Helix will not speak into an open mic, because it
  would transcribe itself.
- **Latency depends entirely on the providers.** Streaming STT plus streaming TTS
  puts first-audio in the low hundreds of milliseconds on a good cloud chain, and
  seconds on a CPU-bound local chain. `/blackbox status` reports the measured
  time-to-first-audio against the budget, and labels whether the streamed or
  buffered path served it.
- **Spoken command matching is phrase-based, not model-based.** It is
  predictable and costs no tokens, but it only knows the phrases in the table.
  Anything else goes to the planner — which is the correct fallback, not a
  failure.

---

## 7. Setup

```text
/blackbox setup     configure STT and TTS providers, then verify the chain
/blackbox status    chains, health, endpoints, resolved routes, spoken vocabulary
/mictest            3-second check: is the microphone actually being heard?
/blackbox on        go live — microphone, camera, speech, companion
/blackbox off       back to the keyboard (also stops a reply mid-sentence)
```

`/blackbox setup` probes what you selected before reporting success, so a sidecar
that is not running (or a port owned by something else) is named at setup rather
than surfacing later as a failed `/blackbox say`.

Local, offline chain: whisper.cpp for STT and Piper or Kokoro for TTS. See
[local_runtimes.md](local_runtimes.md) for ports and launch commands — and note
two traps there: whisper.cpp's stock port collides with llama.cpp's, and on macOS
AirPlay Receiver owns Piper's default port 5000.
