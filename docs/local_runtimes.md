# Local runtimes: llama.cpp, Ollama, whisper.cpp, Piper

Helix talks to four local services. All four are **user-managed sidecars**
(ADR-002): Helix never links a GGUF runtime, never downloads weights for them,
and never launches them. That keeps the core CGO-free and keeps model licensing
and GPU setup where they belong — with you.

This document exists because the defaults collide and the routes are not
obvious, and both failures look identical from the outside: "the local thing
doesn't work."

---

## 1. What each one is for

| Service | Role | Helix provider | Stock port |
| :--- | :--- | :--- | :--- |
| **Ollama** | local LLM, with model management | `ollama` | 11434 |
| **llama.cpp** (`llama-server`) | local LLM, no model management | `llamacpp` | **8080** |
| **whisper.cpp** (`whisper-server`) | speech → text | `whisper-local` | **8080** |
| **Piper** (`piper.http_server`) | text → speech | `piper-local` | 5000 |
| **Kokoro-FastAPI** | text → speech, higher quality | `kokoro-local` | 8880 |

### Which one the setup wizard offers

**Ollama.** llama.cpp is deliberately absent from the first-run provider menu:
choosing it there commits you to installing a runtime, obtaining a GGUF the
installed build can actually load, and launching a server by hand — before
anything works at all.

It is **demoted, not removed**. It stays registered, stays a valid offline
failover target, and is selected with:

```text
/provider use llamacpp
```

The wizard names it as available rather than hiding it, and `/provider list`
shows the full registered set.

### Ollama vs llama.cpp — why both?

They do the same job. Ollama is the better experience: it pulls models, manages
them, and Helix can install it for you. llama.cpp exists for the machines Ollama
cannot serve — most concretely the first-generation Jetson Nano, frozen on
JetPack 4.6 with CUDA 10.2 and Maxwell 5.3 (see
[edge_deployment.md](edge_deployment.md) §5). On hardware like that a hand-built
`llama-server` is the only local-LLM path, so it is registered as a first-class
provider and is a valid target for the offline failover chain.

**If Ollama runs on your machine, use Ollama.** Reach for llama.cpp when it does
not, or when you want a specific quantization Ollama does not package.

Worth knowing when weighing the two: **Ollama is built on llama.cpp**, vendoring
a pinned build for many architectures alongside its own Go/GGML engine and MLX on
Apple Silicon. Choosing Ollama does not avoid llama.cpp; it means using the build
Ollama pinned rather than one you control. And llama.cpp is not the less
maintained of the pair — it is the upstream Ollama tracks.

### Installing llama.cpp

It is a *build*, not a package, on most platforms — there is no
`apt install llama.cpp`. Where Homebrew is available the bottle is a signed
prebuilt binary and a single command, so Helix offers to run it for you:

```bash
brew install llama.cpp
```

Everywhere else it prints the CMake build instead of offering to run it. That
line is deliberate: building llama.cpp means choosing a GPU backend (CUDA, Metal,
Vulkan, CPU), and that is the user's decision, not Helix's — the same reasoning
as ADR-002. A Homebrew bottle makes no such choice, which is why it is the one
case Helix will execute.

Helix checks whether the binary exists before suggesting any launch command. If
you have a healthy Ollama and no llama.cpp, it says so — llama.cpp is not the
easier option unless Ollama cannot serve your hardware.

### Starting it

Having installed the binary and found your models, the wizard offers to start
llama-server too, waits for the model to load, and only then reports success.
Nothing starts implicitly and nothing is restarted for you — ADR-002 still holds
— but making you copy a command back into the shell you are already in was the
step that left the flow feeling unfinished. `/reboot` restarts **Helix**, never a
sidecar, so the rule is not weakened by it.

The started process is **detached on purpose**: tying it to Helix's lifetime
would reload several gigabytes of weights on every restart, which defeats the
point of a local runtime. So it keeps running after Helix exits, and Helix tells
you its PID and how to stop it.

That property stopped being theoretical when `/reboot` arrived — and more so now
that `/reboot` self-updates, since installing a new Helix is a thing you might do
weekly rather than never. Restarting the
shell is now a routine thing to do — after a `/purge`, after a provider change,
after repointing a sidecar — and every sidecar survives it untouched:
llama-server keeps its weights resident, whisper.cpp keeps its model, and the
persistent Piper process keeps its voice loaded. A restart costs the shell, not
the gigabytes behind it. Output goes to `~/.helix/llama-server.log`; when
a load fails, the last lines of that log are printed with the error.

The readiness wait scales its budget with the model size, and distinguishes
"still loading" from "died trying" — a server that exits mid-load is reported
immediately instead of waiting out the whole timeout.

### Yes — llama.cpp downloads models too

It is not "Ollama pulls, llama.cpp doesn't". `llama-server -hf <org>/<repo>`
fetches a GGUF from Hugging Face, caches it, and serves it — one command, no
separate download step, no token for public models:

```bash
llama-server -hf ggml-org/gemma-4-E2B-it-GGUF --port 8080
```

Later launches reuse the cached copy. A bare `llama-server` (or
`--models-dir PATH`) auto-discovers what is already cached and lists it at
`GET /models`.

So there is never a reason to install Ollama merely to obtain weights for
llama.cpp. Helix's setup flow lists what is already on disk first — the llama.cpp
cache *and* Ollama's blobs — and only then offers `-hf` downloads sized to the
machine's RAM.

Cache locations, checked in this order: `$LLAMA_CACHE`, `$HF_HUB_CACHE`,
`$HF_HOME/hub`, `~/.cache/llama.cpp`, `~/.cache/huggingface/hub`. The location
moved from llama.cpp's own cache to the shared Hugging Face hub cache, so both
generations are searched.

### And it can usually serve the models Ollama already pulled

**Usually, not always.** The blobs are plain, complete GGUF files and llama.cpp
identifies them by magic bytes rather than filename, so pointing at one directly
works for most models. But Ollama converts some models with tensor layouts a
given llama.cpp build does not implement yet, and those fail at load:

```
error loading model: done_getting_tensors: wrong number of tensors; expected 2012, got 601
llama_model_load_from_file_impl: failed to load model
```

That is not corruption — the file is byte-complete and unsplit. It means this
llama.cpp build maps only part of what that architecture's GGUF contains.
Newly-released model families are the usual case, since Ollama ships support for
them before an upstream llama.cpp release does.

When it happens, Helix prints the error and the log tail, and `-hf` is the way
past it: download a GGUF built for llama.cpp instead of reusing Ollama's.

Same files, no copy and no conversion. Ollama stores weights as plain GGUF,
content-addressed:

```
~/.ollama/models/blobs/sha256-<hex>          the GGUF itself
~/.ollama/models/manifests/.../<model>/<tag> the JSON naming it
```

llama.cpp identifies GGUF by magic bytes rather than by filename, so a blob path
works directly:

```bash
llama-server -m ~/.ollama/models/blobs/sha256-abc123... --port 8080
```

Finding *which* blob is which is the only hard part, and Helix does it for you:
when you select llama.cpp and the server is not reachable, the setup flow lists
your Ollama models with the exact `llama-server` command for each. Set
`OLLAMA_MODELS` if your store is not in the default location.

Two caveats worth knowing:

- **One at a time.** Ollama and llama-server cannot both own a port, and running
  two copies of a 7B model doubles your RAM for nothing. Stop one.
- **Templates.** Ollama keeps the prompt template in its manifest, not in the
  blob. llama-server uses the chat template embedded in the GGUF instead. For
  nearly all modern GGUFs that is the same template; for an old or unusual quant
  it may not be, which shows up as a model that answers but formats oddly.

### What `local-gguf` meant

It was a **placeholder label**, and it was causing real damage.

`llama-server` serves whichever GGUF it was launched with and ignores the model
name on the request, so there is nothing meaningful to send — hence a stand-in.
But Helix also uses the model name as the key for *capability* lookups, so while
the placeholder was active:

- the context window fell back to the 8k default, and retrieved context was
  clamped to a fraction of what a 128k-context model could accept;
- `SupportsVision` was false, so `/blackbox eyes on` refused even with a multimodal GGUF
  loaded;
- `/status` and `/provider-status` printed `local-gguf`, which tells you nothing.

`llama-server` *does* report the loaded model on `/v1/models`. Helix now asks,
once, at selection and at startup, and replaces the placeholder with the real
name — after which the context limit, vision detection, and status lines are all
correct. If the server reports nothing, Helix says so and explains that
conservative defaults are in force.

---

## 2. The port collision

**llama.cpp and whisper.cpp both default to 8080.** These are the upstream
defaults, not Helix's choices, and they cannot both be honored.

Helix keeps both upstream defaults — changing either would break that project's
documented launch command — and instead *reports the clash*. `/doctor` and
`/blackbox status` name it directly:

```
Endpoint conflict: 127.0.0.1:8080 is configured for llama.cpp (LLM) and whisper-local (STT)
  → One process owns a port. Whichever service is running there will
    answer the other's requests with a 404, which reads as "broken".
```

This matters because the symptom is so misleading: whichever service holds the
port answers, so a naive health check sees a live socket and reports
"reachable", while every actual request 404s.

**Recommended layout** — keep the brain on 8080 and move speech:

```bash
llama-server  -m model.gguf                --port 8080
whisper-server -m models/ggml-base.en.bin  --port 8081
python3 -m piper.http_server -m voice.onnx --port 5001
```

```text
/config stt-url http://127.0.0.1:8081
/config tts-url http://127.0.0.1:5001
```

`/config stt-url` and `/config tts-url` rebuild the speech engine immediately, so
the change applies in the running session.

---

## 3. Route discovery (why local speech used to fail)

Both local speech adapters were pointed at routes their upstream servers do not
serve. Each returned HTTP 404 for every request, which made the provider
unusable as shipped:

| Service | Helix used to request | What the server actually serves |
| :--- | :--- | :--- |
| whisper.cpp | `/v1/audio/transcriptions` | **`/inference`** (the OpenAI path exists only with `--inference-path`) |
| Piper | `/api/tts` | **`/`** — `GET /?text=...` or `POST /` (that's the old Rhasspy path) |

Both adapters now try the known routes in order, remember which one answered,
and report it in `/blackbox status`:

```
whisper-local  Whisper (local sidecar)  healthy  free  local
    endpoint: http://127.0.0.1:8081  route: /inference
```

So a stock `whisper-server` and a stock `piper.http_server` both work with no
flags, and an OpenAI-shaped sidecar (Speaches, Faster-Whisper-Server, LocalAI)
keeps working as before.

### Verifying it yourself against the real servers

Mocks cannot settle this one — the adapters' fakes agreed with the adapters
while both were pointed at the wrong route. So the check runs against the real
binaries, opt-in:

```bash
HELIX_LIVE_SIDECAR=1 go test ./internal/speech/ -run TestLive -v
```

It starts a stock `whisper-server` and a stock `piper.http_server` on free
ports, drives Helix's own adapters and failover chain against them, and asserts
that whisper's transcript matches the spoken words and that Piper's WAV decodes
to non-silent audio through Helix's own decoder. Missing binary, model or voice
makes it **skip with the reason** rather than fail, so it is safe to run
anywhere. Measured on an M4 Mac with `ggml-base.en` and `en_US-lessac-medium`:
**167 ms** to transcribe a three-second clip, **103 ms** to synthesize a short
sentence.

It deliberately does not play the audio — that needs a device, and whether the
bytes are correct is a separate question from whether your speakers work. For
that, `/blackbox say voice link online`.

The same switch runs the **local STT accuracy measurement**: a corpus of
synthesized utterances through the real whisper.cpp server, scored as word
accuracy against the known text. Measured with `ggml-base.en` on an M4 Mac:
**97.0%** (65/67 words, 8/10 utterances perfect, slowest 133ms). That is
synthesized clean speech and therefore an upper bound — a person in a room with a
fridge running will do worse.

### Health checks now prove the service, not the socket

Both local adapters used to treat *any* HTTP response — 404 included — as proof
of life. That produced actively false diagnostics:

- whisper.cpp has no `/models` route at all, so the probe could only ever find a
  404, and reported it as healthy;
- on macOS, **AirPlay Receiver owns port 5000** and answers HTTP, so
  `/blackbox status` showed Piper "reachable" on a machine where Piper was not
  running and every spoken reply failed.

Now each local probe does the real thing: whisper-local transcribes 200 ms of
silence (an empty transcript is a pass — the *route* is what is being verified),
and piper-local synthesizes one short word and requires actual RIFF/WAVE bytes
back. A foreign service on the port fails, and says so.

Route walking treats **any 4xx** as "not on this route" — 404 for a missing path,
401/403 for a server refusing everything — so one squatted route no longer aborts
the search before the other is tried. A **5xx** does stop it: that is the right
endpoint failing, and looking elsewhere would report the wrong cause.

**When an install cannot work, Helix says why rather than showing you the log.**
`piper-tts` needs `onnxruntime`, which publishes no wheel for some
platform/Python combinations — macOS on Intel with a very new Python is the case
that turned up in QA. pip answers that with sixty lines of candidate versions and
one sentence at the end; Helix now reads that sentence, names the packages pip
named, says it is neither a Helix nor a network problem, and offers the three
real ways forward: a different voice, a Python that has wheels, or kokoro-local
if Docker is already running. Diagnosis rather than prediction — which Python has
wheels for what is a moving target Helix would get wrong, while the installer has
just finished saying so precisely.

`/blackbox setup` verifies the chain before declaring success, and closes with a
VOICE LINK panel naming what will hear you, what will answer, and whether every
selected provider actually responded. It used to print a flat "Voice link
configured" and stop, so a selection that could never work only surfaced later as
a failed `/blackbox say` — by which point the wizard appeared to have succeeded.

---

## 3.5 Sesame CSM-1B — the most natural local voice

CSM is the speech model behind Sesame's "crossing the uncanny valley of voice"
demo. What they open-sourced (Apache-2.0) is the **speech generator** from that
system, not the system: the model card is blunt that it "cannot generate text".
So in Helix it is a TTS provider — Helix's planner still decides what to say and
whisper.cpp still does the listening.

Why it sounds different from Piper: a Llama-1B backbone reads interleaved text
and audio and predicts the first codebook of **Mimi**, a split-RVQ neural codec
running at 12.5 Hz, and a small (~100M) decoder fills in the remaining acoustic
codebooks. It is conditioned on conversation context, so replies sound like part
of a dialogue rather than isolated lines. It is also why it wants a GPU: one
second of speech is 12.5 autoregressive steps through a 1B transformer, where
Piper is a single forward pass through something far smaller.

### No Python, no Docker

The reference implementation is PyTorch. Helix does not use it. The sidecar is
[`csm.rs`](https://github.com/cartesia-one/csm.rs) — a Rust/[candle](https://github.com/huggingface/candle)
build with CUDA, Metal, Accelerate and MKL backends and an OpenAI-shaped HTTP
server, which is exactly the sidecar contract Helix already speaks.

```bash
git clone https://github.com/cartesia-one/csm.rs && cd csm.rs

# NVIDIA (Linux / Windows)
cargo build --release --features cuda        # or: --features cudnn

# Apple Silicon (M1–M4) — GPU
cargo build --release --features metal

# macOS CPU (Intel Macs)
RUSTFLAGS="-C target-cpu=native" cargo build --release --features accelerate

# Linux / Windows CPU
RUSTFLAGS="-C target-cpu=native" cargo build --release --features mkl
```

The backend is a **compile-time** choice, which is why Helix prints these rather
than running one for you: picking `mkl` for someone with a 3080 would silently
hand them a CPU build, and Helix's rule is that it only auto-installs when there
is one obvious command that makes no choices on your behalf.

Then start it on the port Helix expects:

```bash
./target/release/server --model-id sesame/csm-1b --port 28195
```

**Not 8080.** csm.rs defaults there, and so do whisper.cpp and llama.cpp — the
collision hits exactly the person running a local chain, which is the person most
likely to want CSM. Helix defaults `csm-local` to **28195** and reassigns per
provider if something is already listening.

### The weights are gated

`sesame/csm-1b` requires accepting Sesame's terms once and being logged in:

```bash
huggingface-cli login    # then accept at https://huggingface.co/sesame/csm-1b
```

csm.rs downloads them on first run (~2 GB bf16, or ~700 MB for the `q4_k` GGUF
from `cartesia/sesame-csm-1b-gguf`). Helix will not do this for you — gated
weights need your account, and a multi-gigabyte download is consent-gated by
policy anyway.

### Will it be smooth on my machine?

Published figures put CSM-1B at **~8 GB VRAM** for comfortable operation and a
**real-time factor of ~0.8×** on a GPU — audio produced about 25% faster than it
plays, which is what a flowing conversation needs. Below is what that implies per
machine class. **These are projections from those figures, not measurements taken
here**; the last column is how you get a real number on your own hardware.

| Machine | Backend | Expectation | Verify with |
| :--- | :--- | :--- | :--- |
| **RTX 3080 laptop (16 GB)** | `cuda` / `cudnn` | Comfortable. Full bf16 weights fit with room; RTF should sit near or below the ~0.8× reference | `make live-csm` |
| **MacBook Air M4 (16 GB)** | `metal` build, but see note | **Measured 1.69× — slower than playback.** csm.rs runs the quantized GGUF on CPU regardless of the metal feature; the full-precision (gated) weights are what Metal accelerates | `make live-csm` |
| **MacBook Pro 2019, Intel i9 (32 GB)** | `accelerate` (CPU) | **Not smooth for live conversation.** candle's Metal backend targets Apple Silicon, so an Intel Mac runs this on CPU: a 1B autoregressive model at 12.5 frames per second of audio will very likely be slower than real time, and replies will pause between sentences | `make live-csm` — and if RTF > 1, use `piper-local` there |

That last row is the honest answer to "all three machines, smoothly". CSM is a
GPU model; the Intel MacBook has no GPU candle can use. Helix handles this
gracefully rather than pretending otherwise — set CSM as your primary and
`piper-local` as the fallback, and the machine that cannot keep up simply uses
the fast voice.

Measure rather than trust the table:

```bash
make live-csm     # attaches to a running csm.rs and reports the real-time factor
```

It skips loudly if no sidecar is listening, and prints the RTF plus a note when
the machine is slower than real time.

## 3.6 Conversational context — the part that makes CSM CSM

CSM's distinguishing capability is not its voice, it is that its prosody is
conditioned on how the conversation has been going. The model card says it
"sounds best when provided with context", and the reference API takes prior turns
as `Segment(text, speaker, audio)`. Synthesize each sentence in isolation and you
get a very good single-shot voice; give it the last few turns and it starts
sounding like a participant.

**Helix now assembles and sends that context.** What is missing is a server that
accepts it: `csm.rs`'s HTTP API is stateless single-utterance, and no CSM server
today implements a context field. So this section specifies the extension, and
Helix behaves correctly on both sides of it.

### What Helix sends

An extra `context` array on the ordinary `/v1/audio/speech` request, oldest turn
first, mirroring the reference `Segment` shape so an implementation has an
obvious target:

```jsonc
{
  "model": "sesame/csm-1b",
  "input": "Both failures are in the parser.",
  "speaker_id": 0,
  "temperature": 0.7,
  "response_format": "wav",
  "context": [
    { "speaker": 1, "text": "did the build pass",  "audio_b64": "…", "format": "wav" },
    { "speaker": 0, "text": "no — two tests failed" }
  ]
}
```

`speaker` follows CSM's convention (0 = assistant, 1 = user). `audio_b64` is
optional: a turn with text but no audio is still worth conditioning on, and that
is what a streamed reply or a streamed transcript produces, since neither leaves
one contiguous clip behind.

### How Helix knows whether it worked

Reading csm.rs's source settled a question that guessing had got wrong.
`SpeechRequest` derives `Deserialize` **without** `deny_unknown_fields`, and
serde's default is to ignore unknown fields — so today's server does not reject
a `context` field, it **accepts the request and silently drops it**. The audio
comes back fine. Nothing was conditioned on. From the outside that is
indistinguishable from success, which is the worst possible failure mode for a
feature whose entire value is subjective.

So the patch adds a response header, and Helix reads it:

```
X-CSM-Context-Segments: 2
```

- **Header present** → the sidecar understands context and reports what it used.
- **Header absent** → an unpatched sidecar dropped it. Helix records the context
  as *ignored* and does not claim conversational prosody it is not getting.
- **`4xx`** → a stricter server rejected the field. Helix retries immediately
  without context and stops sending it for the session.
- **`5xx` or a dropped connection** → explicitly *not* read as refusal. A busy or
  restarting sidecar says nothing about whether it understands the field, and
  treating a hiccup as a refusal would permanently downgrade the voice.

Against today's csm.rs the audible behavior is exactly what it was; the
difference is that Helix now knows, and says, that context is not being applied.
Context can make the voice better; it cannot make it absent.

### Turning it on

```jsonc
"speech": {
  "tts": {
    "provider": "csm-local",
    "context_turns": 4,          // 0 = off (default)
    "context_max_bytes": 4194304 // 4 MiB of retained audio
  }
}
```

**Off by default, deliberately.** Enabling it means Helix holds recent audio in
memory for longer than the turn that produced it, which is a privacy-relevant
change even though nothing reaches disk. The bounds are the design:

- **Memory only.** Nothing in `internal/speech/conversation.go` touches the
  filesystem — a test enforces that it imports neither `os` nor `net`. The
  "captured audio is never written to disk" guarantee is unchanged, and there is
  nothing new for `/purge` to wipe because there is nothing new on disk.
- **Bounded twice**, by turn count and by total bytes, evicting oldest-first. One
  oversize turn is kept rather than dropping to nothing, because a long sentence
  should not erase the conversation.
- **Scoped to live mode.** `/blackbox off` drops it. The audio does not outlive
  the conversation it belongs to.

### Telling conditioning from silence

This matters more than it first appears. csm.rs's request struct does **not**
derive `deny_unknown_fields`, so serde discards a `context` field it does not
know about and answers `200` anyway. An unpatched sidecar therefore *accepts*
context, uses none of it, and looks exactly like success — the worst failure mode
for a feature whose whole value is how something sounds.

So Helix does not report what it sent, it reports what the server acknowledged.
The `CONTEXT` row of `/blackbox status` reads:

| Row says | Meaning |
| :--- | :--- |
| **conditioning** | The sidecar acknowledged the turns, with the count and bytes held |
| **not applied** | It accepted them and silently dropped them — apply `docs/csm-context.patch` |
| **refused** | It rejected the field outright; Helix stopped sending it for this session and speaks plainly |
| **retained, unused** | Turns are being held but no context-capable voice is in the chain — a privacy cost buying nothing |
| **ready** | Nothing has been sent yet, so nothing is known. Unconfirmed, and not claimed as working |
| **off** | Retention is disabled (`context_turns: 0`) |

A rejection is permanent for the session but never fatal: the turn is re-sent
without context and still spoken. A `5xx` is explicitly *not* treated as a
refusal — a busy sidecar says nothing about whether it understands the field, and
one hiccup must not silently downgrade the voice for the rest of the session.

### The patch

`docs/csm-context.patch` implements this against csm.rs as it stands. It is
smaller than it sounds, because everything needed is already in the codebase:
`audio_tokens_and_mask(frame)` turns one Mimi frame into model tokens, and the
`audio_tokenizer` is a `moshi::mimi::Mimi` that encodes as well as decodes. So
context is **token assembly, not new model loading** — no extra weights and no
extra VRAM beyond the KV cache the longer prompt occupies.

What it does:

1. Adds `ContextSegment { speaker_id, text, audio }` to `csm-core`.
2. Encodes each segment's audio to Mimi frames with the same codebook count
   `decode_frames` uses, so the backbone sees frames shaped like the ones it was
   trained to produce.
3. Interleaves text-then-audio per segment, oldest first, and prepends the whole
   prefix to the current prompt — trimming from the FRONT at `max_seq_len`, so a
   long history degrades to recent history instead of erroring.
4. Adds `context: Vec<ContextSegmentDto>` to the server request, decoding
   base64 WAV (by parsing the `data` chunk rather than assuming a 44-byte header,
   since encoders emit `LIST`/`INFO` chunks and a fixed offset shifts the audio
   audibly) or raw PCM.
5. Returns `X-CSM-Context-Segments` so a client can tell conditioning from
   silence.

Segments are validated **before** the response starts streaming, so a malformed
one is a clean 400 rather than a failure discovered after the WAV header has
already gone out.

### Verified end to end (2026-08-24)

The patch was applied to `cartesia-one/csm.rs@facfd06`, built with
`--features metal`, and run against the **public** `cartesia/sesame-csm-1b-gguf`
weights — which are ungated, so no Hugging Face token was needed to prove it:

| Check | Result |
| :--- | :--- |
| Patch compiles | clean, no warnings introduced |
| Plain synthesis (no context) | `200`, `x-csm-context-segments: 0` |
| Context with real audio | `200`, **`x-csm-context-segments: 2`** |
| Text-only context segment | `200`, `x-csm-context-segments: 1` |
| Malformed `audio_b64` | clean `400`, before streaming starts |
| Through Helix's own adapter | context **HONORED**, 2 turns conditioned |

The audio-bearing case is the one that matters: it exercises Mimi *encoding* on
the server, which is the half of the patch that had never been run before.

**Measured on an M4 Air (16 GB): real-time factor 1.69×** — 11.0 s of audio in
18.7 s. Slower than playback, and the reason is worth knowing: csm.rs loads the
**quantized GGUF path on CPU**, logging `Using device: Cpu for generation`, so
Metal is not used for that path at all. The `--features metal` build only helps
the full-precision safetensors weights, which are the gated ones. So on Apple
Silicon the choice is between a CPU-bound quantized model and gated full weights;
neither reaches the ~0.8× a discrete NVIDIA GPU manages.

### Licensing, stated plainly

The **model weights** are Apache-2.0. **csm.rs is AGPL-3.0**, and Helix is MIT.
Helix never links, bundles or redistributes csm.rs — it is a separate process you
install and run, spoken to over HTTP, exactly like whisper.cpp and Ollama — so
Helix's MIT licence is unaffected. The AGPL obligation attaches to whoever
*operates* the server: running it locally for yourself owes nothing, while
exposing it as a public network service would oblige you to offer its source.

### Select it

```text
/blackbox setup     # "Most natural, local" [needs a GPU] — csm-local + piper fallback
/blackbox status    # confirms the endpoint answers, and what CONTEXT is doing
/blackbox say the build finished and two tests failed
```

Speaker identity is a number, not a voice file: CSM was trained on multi-speaker
conversation with the speaker encoded in the text stream. The wizard's voice
prompt takes a speaker id (`0` is the conventional assistant slot); anything
non-numeric falls back to `0` rather than failing.

---

## 3.7 Piper without Python

Piper's HTTP server is a Python module, and for a long time it was the one thing
in Helix that required an interpreter — in a project built around a CGO-free Go
binary, whose CSM integration exists in Rust specifically to avoid PyTorch.
Piper also publishes standalone executables, so Helix uses those where they work.

### It is a persistent process, and that is the whole point

Piper's cost is dominated by **loading** the voice model, not by speaking.
Measured on an M4 Air:

| | per utterance |
| :--- | ---: |
| One process, five utterances | **128 ms** |
| One process per utterance | 513 ms |

So a naive "shell out per sentence" adapter is a **4× regression** against the
HTTP server it replaces — the server is fast precisely because it keeps the
model warm. Helix therefore holds one piper process open and feeds it
utterances, which gets the server's speed with no port, no HTTP hop and no
interpreter:

| | |
| :--- | ---: |
| First utterance (loads the model) | 800 ms |
| Every utterance after | **55–66 ms** |
| HTTP server, for comparison | 103 ms |

The margin is **wider on weak hardware**, not narrower: model load is the part
that scales with CPU, so a Pi pays more per reload than a laptop does.

Framing is by filesystem rather than by log parsing. Piper announces each
finished file on stderr, but the C++ binary and the Python module word it
differently, so Helix spawns with `--output-dir` and watches for *a file that was
not there before*. It also waits for the file size to settle before reading: a
WAV appears when piper creates it, not when it finishes writing it, and reading
on first sight yields a clip that decodes to a fraction of the sentence.

### macOS does not get it, and the reason is upstream

Both macOS archives in Piper's `2023.11.14-2` release — the last one to ship
standalone binaries at all — contain the `libonnxruntime` `.dSYM` debug bundle
and **no `.dylib` files**. The extracted binary dies immediately:

```
dyld: Library not loaded: @rpath/libespeak-ng.1.dylib
```

Verified by downloading and running it, not inferred from a file listing. The
Linux archives carry `libespeak-ng.so` and `libpiper_phonemize.so`; the Windows
zip carries the four matching DLLs. Only macOS is broken, and the successor
project (`OHF-Voice/piper1-gpl`) publishes **Python wheels only**, so there is no
newer standalone build to move to.

On macOS, Helix keeps the Python server and says so, rather than fetching 19 MB
that cannot start. There is no Homebrew formula either.

### Edge boards: libstdc++ is the gate, not glibc

The binaries cover every architecture Helix targets — `aarch64` (Pi 4/5 on a
64-bit OS, all Jetsons) and `armv7l` (32-bit Pi OS, Pi Zero 2). glibc is a
non-issue: the aarch64 build needs only `GLIBC_2.17`, from 2012.

**`libstdc++` is what actually decides it.** `libpiper_phonemize.so` imports
`GLIBCXX_3.4.26` (GCC 9) and the archive does **not** bundle libstdc++:

| Board / OS | Native binary |
| :--- | :--- |
| Pi OS Bookworm (GCC 12), Bullseye (GCC 10) | works |
| Pi OS Buster (GCC 8 → 3.4.25) | **fails** |
| Jetson Nano 1st-gen, JetPack 4.x (Ubuntu 18.04, GCC 7.5) | **fails** |

Helix checks the system libstdc++ **before offering the 50 MB download**, and on
a board that cannot run it says which version is missing instead. That is
consistent with §4's existing guidance rather than a new limitation: the Jetson
Nano was already the cloud-path board, and Ollama does not support it either.

One running cost to plan for: the resident model is roughly 150–250 MB of RSS.
Comfortable on a Pi 4/5 or a Jetson (4 GB+), too tight on a Pi Zero 2 W (512 MB).

---

## 4. Setup commands

```bash
# LLM — Ollama (preferred where supported)
ollama serve && ollama pull llama3.1:8b

# LLM — llama.cpp
llama-server -m /path/to/model.gguf --port 8080
export HELIX_LLAMACPP_URL=http://127.0.0.1:8080   # only if not the default

# STT — whisper.cpp
./build/bin/whisper-server -m models/ggml-base.en.bin --port 8081

# TTS — Piper
python3 -m piper.http_server -m en_US-lessac-medium.onnx --port 5001

# TTS — Kokoro (better quality, heavier, OPTIONAL — the only piece needing Docker)
docker run -p 8880:8880 ghcr.io/remsky/kokoro-fastapi-cpu
```

**Helix never requires Docker, and on Linux and Windows it no longer requires
Python either** — see §3.7. whisper.cpp and Piper cover the whole local voice
chain with nothing but a binary, and they are what setup offers. Kokoro is the one component that ships as a container: Helix will not
install a container runtime, will not pull the image when no daemon is
answering, and marks Kokoro `needs docker` in the provider table so the
constraint is visible before you pick it rather than after.

Then in Helix:

```text
/provider use llamacpp      · select the local brain (resolves the real model name)
/blackbox setup             · pick STT/TTS providers and their endpoints
/config stt-url <url>       · point local STT somewhere else
/config tts-url <url>       · point local TTS somewhere else
/config fallback-model <id> · which Ollama model answers when the cloud fails
/config context-turns <n>   · turns a context-capable voice is conditioned on (0 = off)
/config barge-in on|off     · stop a reply by speaking between sentences
/blackbox status            · chains, health, endpoints, and resolved routes
/doctor                     · everything above plus conflicts, thermals, confinement
```

`/config` with no argument lists every settable key with its current value. The
keys are short names, not the dotted paths from `config.json` — `barge-in`, not
`speech.tts.barge_in`.

---

## 4B. When a model download fails

`ollama pull` failures are reported, not fatal. Helix starts anyway: the
provider is chosen and the config is written, so `/help`, `/doctor` and
`/provider use <name>` all work — what is missing is a model, and `/setup` or a
manual `ollama pull` supplies it later. Helix used to exit to your login shell
when a download failed, which is the one outcome you cannot recover from in
place.

The registry's own error text is classified rather than echoed, because the two
common causes need opposite advice:

| What happened | What Helix says |
| :--- | :--- |
| `503` / `upstream connect error` / timeout | The registry did not answer — upstream of Helix and of your machine. Retry in a few minutes, or `ollama pull <tag>` yourself |
| `404` / `file does not exist` on the manifest | No such tag. Check ollama.com/library; retrying will never help |
| `connection refused` to `127.0.0.1:11434` | Ollama itself is not running — `ollama serve` |
| `no such host` / network unreachable | No route to the internet |

The raw error is always printed underneath. A diagnosis that hides what actually
happened cannot be debugged.

---

## 5. When Ollama cannot see its own models

`ollama list` empty while gigabytes sit in `~/.ollama/models` means the running
server is reading a **different store** — it was started with another `HOME` or
`OLLAMA_MODELS`. Every request for those models then answers 404, including
Helix's own cloud-to-local fallback, which reports itself as "armed" because the
server IS reachable.

Helix compares the filesystem against the API and says so outright. To confirm by
hand:

```bash
lsof -nP -iTCP:11434 -sTCP:LISTEN
ps eww -p <pid> | tr ' ' '\n' | grep -E 'HOME=|OLLAMA_MODELS='
```

Kill that server and start one from your own shell to serve `~/.ollama` again.

A note on how this can happen: any process that calls Ollama's start path
inherits its own environment into the spawned `ollama serve`, and that server
outlives it. Helix now starts Ollama only when `llm.fallback.ensure_ready` is
explicitly true — the same consent gate that governs pulling a model — so a
readiness check never leaves a daemon behind.

## 6. Diagnosing "the local thing doesn't work"

Run `/blackbox status` (speech) or `/doctor` (everything) first. In order of how
often each is the cause:

1. **Endpoint conflict** — two services on one port. Reported explicitly; move
   one.
2. **Nothing listening** — the sidecar is not running. The status line carries
   the exact launch command.
3. **Something else listening** — a foreign service answers. Detected rather
   than reported as healthy, and diagnosed rather than reported as a bare status.
   A 403 from a local sidecar is near-proof of this: a local service has no
   credentials to reject. On macOS port 5000 the answer is almost always AirPlay
   Receiver, and Helix says so:

   ```
   piper-local at http://127.0.0.1:5000: something IS listening and refused the request (HTTP 403).
     That is not this sidecar — a local one has no credentials to reject.
     On this platform port 5000 is normally macOS AirPlay Receiver.
     Either turn it off — System Settings → General → AirDrop & Handoff →
     AirPlay Receiver → Off — or move the sidecar to a free port:
       (it currently holds port 5000)
     Then start the sidecar on a free port:
       python3 -m piper.http_server -m en_US-lessac-medium.onnx --port 5001
     And point Helix at it:
       /config tts-url <url>
   ```

   On other platforms there is no default culprit to name, so Helix hands over
   `lsof -nP -iTCP:<port> -sTCP:LISTEN` instead of guessing.
4. **Not installed vs not running.** These need different fixes, and Helix says
   which one you have. `llama-server` is a build, not a package, on most
   platforms — on macOS with Homebrew it is `brew install llama.cpp`; elsewhere
   see the install hint Helix prints. If you have a working Ollama and no
   llama.cpp, the wizard will say so and point you at Ollama, because llama.cpp
   is not the easier option unless Ollama cannot serve your hardware.
5. **Placeholder model** — `local-gguf` still showing after selection means
   `llama-server` did not report a model; expect conservative capability
   defaults and no vision.
6. **Mute build** — a CGO-free build cannot play audio however TTS is
   configured. `/version` and `/doctor` state the build flavor; rebuild with
   `CGO_ENABLED=1 go build -tags audio_cgo ./cmd/helix`.
