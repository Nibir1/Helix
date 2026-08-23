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
step that left the flow feeling unfinished.

The started process is **detached on purpose**: tying it to Helix's lifetime
would reload several gigabytes of weights on every restart, which defeats the
point of a local runtime. So it keeps running after Helix exits, and Helix tells
you its PID and how to stop it. Output goes to `~/.helix/llama-server.log`; when
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

`/blackbox setup` now verifies the chain before declaring success. It used to print
"Voice link configured" and stop, so a selection that could never work only
surfaced later as a failed `/blackbox say` — by which point the wizard appeared to have
succeeded.

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

**Helix never requires Docker.** whisper.cpp and Piper cover the whole local
voice chain with nothing but a binary and Python, and they are what setup
offers. Kokoro is the one component that ships as a container: Helix will not
install a container runtime, will not pull the image when no daemon is
answering, and marks Kokoro `needs docker` in the provider table so the
constraint is visible before you pick it rather than after.

Then in Helix:

```text
/provider use llamacpp      · select the local brain (resolves the real model name)
/blackbox setup             · pick STT/TTS providers and their endpoints
/config stt-url <url>       · point local STT somewhere else
/config tts-url <url>       · point local TTS somewhere else
/blackbox status               · chains, health, endpoints, and resolved routes
/doctor                     · everything above plus conflicts, thermals, confinement
```

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
