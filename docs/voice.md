# Voice mode

`/voice` turns Helix into a spoken interface: you talk, it plans, it acts, it
answers out loud. This document covers what the voice channel can reach, what it
deliberately cannot, and where the honest limits are.

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

Say the plain-English phrase. `/voice-status` prints the full vocabulary; a
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
| "what do you see" | `/eyes look` |
| "run diagnostics" | `/doctor` |
| "turn on agentic mode" | `/agentic on` |
| "stop talking" | `/tts off` |

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

`/wake on` holds the microphone in wake-only listening between turns — no
transcription happens until the wake phrase fires, so nothing leaves the machine
while you are not addressing it. After a completed turn Helix returns to wake-only
listening; when the idle window lapses it says so once rather than silently
reverting to open capture.

Kill phrases end voice mode without touching the keyboard. "Turn off your eyes"
disables the camera immediately while staying in voice mode.

---

## 5. Eyes

`/eyes on` opts into the camera. Frames are captured **to memory only** and never
written to disk.

Two ways to use it:

- **In conversation, spoken**: a question that points at something ("what's wrong
  with *this*?") captures one frame for that turn.
- **Explicitly, typed or spoken**: `/eyes look [question]`, or say "what do you
  see".

Typed conversational input deliberately does **not** trigger the camera, even
with eyes on. Deictic words are ambiguous in typed input in a way they are not
when spoken — "what does this do?" typed at a terminal almost always means the
text on screen, and firing a camera on the most ordinary phrasing there is would
be a privacy surprise. `/eyes look` is the explicit path instead.

Vision needs a genuinely multimodal model, detected from the model name. With a
local runtime reporting a placeholder name Helix cannot tell, and `/eyes on`
refuses — select the provider once (`/provider use llamacpp`) so the real model
name is resolved. See [local_runtimes.md](local_runtimes.md).

---

## 6. Honest limits

Worth stating plainly, because "realtime voice AI" implies things this does not
yet do:

- **Capture is half-duplex.** The recorder does not run while the speaker does,
  so you cannot talk over a reply and be heard. Interrupting a spoken reply works
  via **Ctrl+C**, and leaving voice mode (`/manual`) stops it mid-sentence. True
  talk-over barge-in needs concurrent capture plus acoustic echo cancellation,
  which is not implemented — without AEC the microphone would hear Helix's own
  voice and transcribe it as your input.
- **"Stop talking" only lands between turns**, for the same reason.
- **Latency depends entirely on the providers.** Streaming STT plus streaming TTS
  puts first-audio in the low hundreds of milliseconds on a good cloud chain, and
  seconds on a CPU-bound local chain. `/voice-status` reports the measured
  time-to-first-audio against the budget, and labels whether the streamed or
  buffered path served it.
- **Spoken command matching is phrase-based, not model-based.** It is
  predictable and costs no tokens, but it only knows the phrases in the table.
  Anything else goes to the planner — which is the correct fallback, not a
  failure.

---

## 7. Setup

```text
/voice-setup     configure STT and TTS providers, then verify the chain
/voice-status    chains, health, endpoints, resolved routes, spoken vocabulary
/mictest         3-second check: is the microphone actually being heard?
/voice           enter voice mode
/manual          back to the keyboard (also stops a reply mid-sentence)
```

`/voice-setup` probes what you selected before reporting success, so a sidecar
that is not running (or a port owned by something else) is named at setup rather
than surfacing later as a failed `/say`.

Local, offline chain: whisper.cpp for STT and Piper or Kokoro for TTS. See
[local_runtimes.md](local_runtimes.md) for ports and launch commands — and note
two traps there: whisper.cpp's stock port collides with llama.cpp's, and on macOS
AirPlay Receiver owns Piper's default port 5000.
