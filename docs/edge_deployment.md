# Helix on Linux Edge Devices — Deployment Matrix

> **Scope:** How to run Helix (and its BlackBox voice stack) on Raspberry Pi, NVIDIA Jetson,
> generic arm64/amd64 mini-PCs, and other Linux single-board computers.
> **Companion to:** `docs/BlackBox_Development.md` §6B Phase 10, `docs/blackbox.md` (user guide).

---

## §1. The one idea that explains everything

Helix splits cleanly into two layers, and they have **different portability rules**:

| Layer | What it is | Portability |
|-------|-----------|-------------|
| **Core** | The `helix` binary: shell, planner, **agentic harness**, safety pipeline, IPC/daemon, mic capture orchestration, cloud STT/TTS clients | **Fully portable.** Pure Go, CGO-free, statically linked. Runs on any CPU arch Go targets, independent of the device's glibc/kernel version. |
| **Sidecars & device I/O** | Local ML services (Ollama, whisper.cpp, Piper/Kokoro, openWakeWord) + on-device **audio output** + kernel confinement | **Per-device.** These depend on the board's CPU/RAM/GPU, kernel version, and what you can build/install for it. |

**So "does it run on device X" is really three questions:**

1. Is X's CPU architecture one Go compiles for? (arch → build flag, §2)
2. Do you want **cloud** or **local** inference, and can X host the sidecars you need? (§4)
3. Do you need on-device **speaker output** and kernel **confinement**? (§3 — the two Linux gotchas)

The core is the easy part. Everything device-specific lives in §3–§4.

---

## §2. Build matrix (architecture → flags)

The release binary is CGO-free. `scripts/build.sh linux` already emits `linux/amd64` and
`linux/arm64`; other targets are a one-line cross-compile:

| Device class | `GOARCH` | Build command |
|--------------|----------|---------------|
| Raspberry Pi 5 / Pi 4 (64-bit OS), Jetson (all), most modern SBCs | `arm64` | `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o helix ./cmd/helix` |
| amd64 mini-PC (Intel N100, etc.) | `amd64` | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o helix ./cmd/helix` |
| Older 32-bit boards (Pi 2/3 on 32-bit OS, some SBCs) | `arm` + `GOARM=7` | `CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o helix ./cmd/helix` |
| RISC-V boards (VisionFive 2, etc.) | `riscv64` | `CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -o helix ./cmd/helix` |

Because the binary is statically linked, it does **not** care about the device's glibc version —
this sidesteps the classic `GLIBC_2.xx not found` failure that breaks most prebuilt binaries on
older distros (notably JetPack 4.x / Ubuntu 18.04 on the Jetson Nano).

That protection covers **Helix**, not the sidecars it talks to. Piper's standalone binary is one of
those prebuilt binaries, and on old boards it is `libstdc++` rather than glibc that stops it — see
§4.2.

---

## §3. The two Linux gotchas (they hit the core, not just sidecars)

### 3.1 On-device speaker output needs a CGO build

The default Linux release binary is **silent** — its audio backend is a no-op
(`internal/audio/backend_noop.go`, build tag `linux && !audio_cgo`). To make Helix **speak through
the device's own speaker**, build it on-device with ALSA:

```bash
sudo apt install -y libasound2-dev
CGO_ENABLED=1 go build -tags audio_cgo -o helix ./cmd/helix
```

Notes:
- This is the **only** part of Helix that needs CGO, and only for local speaker output.
- **Microphone input needs no CGO** — capture shells out to `sox`/`ffmpeg`.
- If audio output is a no-op (you shipped the CGO-free binary), STT still works and text still
  prints; you just won't hear TTS. `/blackbox status` and `/mictest` report the recorder/output state.
- Headless daemon on a board with no speaker: keep the CGO-free binary; drive it over
  `helix remote` and read replies as text, or attach a speaker and rebuild with `audio_cgo`.

### 3.2 Kernel confinement degrades on old kernels

Helix's kernel-grade sandbox (`internal/confinement`) prefers **bubblewrap**, falls back to the
**Landlock** LSM, and probes support at runtime. Landlock requires **kernel ≥ 5.13**. On older
kernels (e.g. Jetson Nano JetPack 4.6 = **kernel 4.9**) Landlock is unavailable, so:

- If `bwrap` is installed → bubblewrap namespace confinement is used.
- If neither is available → confinement falls back to **none**: you keep the directory sandbox and
  the full safety pipeline (classify → firewall → risk tiers), but lose the extra kernel-LSM
  hardening layer. **It degrades, it does not crash.**

Recommendation on older-kernel boards: `sudo apt install -y bubblewrap` to keep a real confinement
layer.

---

## §4. Voice-path guidance: cloud, hybrid, or local

The right inference path depends on the board's horsepower and your privacy/cost needs.

| Path | STT | LLM | TTS | Needs | Best for |
|------|-----|-----|-----|-------|----------|
| **Cloud** (recommended default for weak boards) | Groq `whisper-large-v3-turbo` | any cloud provider | `gpt-4o-mini-tts` | network + mic + `sox`/`ffmpeg` | Jetson Nano 1st-gen, Pi 4, anything CPU-limited |
| **Hybrid** (resilient) | cloud primary, `whisper-local` fallback | cloud primary, small Ollama fallback | cloud primary, Piper/Kokoro fallback | above + local sidecars | Pi 5, amd64 mini-PC |
| **Fully local** (private/offline) | whisper.cpp / sherpa-onnx | Ollama 3B (Q4) | Kokoro-82M / Piper | Ollama + whisper.cpp + Piper/Kokoro + openWakeWord | amd64 mini-PC, Pi 5 (tight) |

The heavy compute in the **cloud path is remote**, so a slow board runs it smoothly as long as it
has a network connection. Configure any path with `/blackbox setup`; set a local fallback so a dropped
network keeps voice alive (the failover chain flips local-first automatically — ADR-011/012).

### 4.1 Keeping the *brain* alive too (Phase 11)

Speech failover keeps a board hearing and speaking offline; the **LLM** needs its own fallback or
Helix goes quiet in a different way — it hears you and cannot answer. Add an `llm` section:

```jsonc
"llm": {
  "fallback": { "enabled": true, "provider": "ollama", "model": "qwen2.5:3b" }
}
```

- `provider` is `ollama` on any board Ollama supports, or `llamacpp` for a user-run `llama-server`
  (the Jetson Nano 1st-gen case — see §5).
- The switch is a circuit breaker (ADR-016): repeated unreachable-provider errors or a connectivity
  drop flip Helix to the local model with a spoken notice, and a periodic probe switches back.
- It **health-checks the local model before switching**, so leaving this armed on a board with no
  local runtime changes nothing — it simply never engages.
- If you have selected the local provider *directly* — the ordinary outcome of choosing Ollama at
  first run — the primary and the fallback are the same provider and the breaker is inert. There
  is nothing to fall back to. `/provider-status` says so ("not applicable — ollama is already the
  local provider") rather than reporting a fallback that cannot help; it used to render the
  self-referential "armed — will switch to ollama if ollama fails".
- `ensure_ready: true` additionally lets `helix daemon` pull the Ollama model at startup. Left at
  the default (`false`) the daemon only verifies and warns in the journal — deliberate, because a
  multi-gigabyte download on a metered edge link should be your choice, not a side effect.

Verify with `/provider status` (an "Offline fallback" line) or `helix remote status`
(`llm_fallback`, `llm_local_mode`).

---

### 4.2 Piper on edge boards: `libstdc++`, not glibc

Helix runs Piper as a **persistent process** with the voice model resident, which
matters more here than on a laptop: Piper's cost is dominated by *loading* the
model, and that is the part that scales with CPU. Paying it once per session
instead of once per sentence is a bigger win on a Pi than on an M4 Air, where it
already measured 4× (513 ms → 128 ms per utterance).

Architecture coverage is complete — `aarch64` (Pi 4/5 on a 64-bit OS, all
Jetsons) and `armv7l` (32-bit Pi OS, Pi Zero 2) — and **glibc is a non-issue**:
the aarch64 build needs only `GLIBC_2.17`, from 2012.

The gate is the C++ runtime. `libpiper_phonemize.so` imports `GLIBCXX_3.4.26`
(GCC 9) and the archive does **not** bundle libstdc++, so the system must supply
it:

| Board / OS | Native Piper |
| :--- | :--- |
| Pi OS Bookworm (GCC 12), Bullseye (GCC 10) | works |
| Pi OS Buster (GCC 8 → `GLIBCXX_3.4.25`) | **fails** |
| Jetson Nano 1st-gen, JetPack 4.x (Ubuntu 18.04, GCC 7.5) | **fails** |

Helix probes the system libstdc++ **before** offering the ~50 MB download and
names the missing version rather than fetching something that cannot start. On
the Nano this is consistent with the guidance above rather than a new limit —
that board is already the cloud-path recommendation, and Ollama does not support
it either. The Python `piper.http_server` still works there if you want local
TTS on it.

Budget roughly **150–250 MB RSS** for the resident model: fine on a Pi 4/5 or a
Jetson (4 GB+), too tight on a Pi Zero 2 W (512 MB).

## §5. Per-device notes

### Raspberry Pi 5 (8 GB, arm64) — the reference local box
- Kernel 6.x → Landlock available; glibc modern → any sidecar installs cleanly.
- Fully-local stack is realistic: openWakeWord → whisper.cpp/sherpa-onnx → `qwen2.5:3b`/`llama3.2:3b`
  (Q4) via Ollama → Kokoro-82M (quality) or Piper (lowest latency). Expected voice-to-first-audio
  ~1–1.5 s with sentence-streamed TTS (`SpeakStream`).
- This is the box the BlackBox Phase 10 reference stack targets.

### Raspberry Pi 4 (4 GB, arm64)
- Same OS story as the Pi 5 but a slower CPU (A72 @ ~1.5 GHz) and less RAM headroom.
- **Cloud or hybrid** path recommended; fully-local works but a 3B model is sluggish — prefer a
  1.5B model if you insist on local LLM.

### NVIDIA Jetson Nano (first-gen, 4 GB, arm64) — cloud path recommended
- **The core runs** (arm64, static binary sidesteps the Ubuntu 18.04 / glibc 2.27 problem).
- **Kernel 4.9 → no Landlock.** Install `bubblewrap` to keep confinement, else it degrades to none.
- **The GPU is largely a trap here.** JetPack for the first-gen Nano is frozen at 4.6 (Ubuntu 18.04,
  CUDA 10.2, Maxwell compute 5.3). **Ollama does not support this device.** A local LLM means
  building `llama.cpp` from source (CPU, or CUDA with the legacy toolkit — fiddly) and staying at a
  **1.5–3B Q4** model, which is tight in 4 GB shared with the GPU/OS.
- **If you do build llama.cpp, Helix talks to it directly.** Run it as a sidecar and point Helix at
  it — this is the `llamacpp` provider (ADR-016), and it is a valid offline-fallback target, so the
  Nano can keep thinking through a network cut:
  ```bash
  llama-server -m ~/models/qwen2.5-3b-instruct-q4_k_m.gguf --port 8080 &
  export HELIX_LLAMACPP_URL=http://127.0.0.1:8080   # or set llm.llamacpp_url in config.json
  ```
  Then set `llm.fallback.provider` to `llamacpp` (see §4). Helix never installs or downloads a
  GGUF — `llama-server` is user-managed, like whisper.cpp and Piper.
- **Recommended:** run the **cloud voice path** (Groq STT + `gpt-4o-mini-tts`). The Nano just needs
  `sox`/`ffmpeg`, a mic/speaker, and internet; the CPU limits don't matter because inference is
  remote. Treat local LLM as best-effort.
- For speaker output, do the on-device `audio_cgo` build (§3.1).

### Generic amd64 mini-PC (Intel N100 / N305, 8–16 GB) — best local box
- Fast CPU, plenty of RAM, modern kernel/glibc, Ollama fully supported.
- Best fully-local experience of any device here; treat it like a Pi 5 with more headroom.

### Generic arm64 SBC (Orange Pi 5, Rock 5, etc.)
- `arm64` build; behaves like a Pi 4/5 depending on SoC. Check kernel version for Landlock and RAM
  for local-LLM viability. Cloud/hybrid is the safe default.

### RISC-V (`riscv64`) boards
- The **core** compiles (`GOARCH=riscv64`). The **sidecar ecosystem is sparse** — Ollama/whisper.cpp
  RISC-V support is immature. Expect the **cloud path only** for now.

---

## §5.1 Guided setup: `scripts/edge-setup.sh`

Rather than following §3–§5 by hand, run the setup script on the device:

```bash
./scripts/edge-setup.sh --check      # detection only — safe on a live appliance
./scripts/edge-setup.sh --dry-run    # print exactly what it would do
./scripts/edge-setup.sh              # interactive; --yes to accept everything
```

It detects the architecture, board, kernel (Landlock availability), and package
manager; installs `sox` and `bubblewrap` through the distro; and offers Ollama
behind a **SHA-256-verified** install that refuses to run on a checksum mismatch.
On a **Jetson Nano 1st-gen it declines Ollama outright** and points at the cloud
path — matching §5's guidance rather than letting you install something that
cannot work.

What it deliberately does **not** do is download whisper.cpp, Piper, Kokoro, or
openWakeWord. None of them publishes a stable, per-architecture, checksummable
release artifact (whisper.cpp is built from source, Kokoro ships as a container),
so a pinned auto-installer would rot into false assurance. They are user-managed
sidecars; the script prints the exact commands for each.

`--assume-board="..."` forces a board path, which is how you preview the Jetson
behavior from a laptop.

**Piper needs two things, not one.** Its "binary" is a Python interpreter, so
`python3` being present says nothing about whether the server can run. Install
the module *and* flask — `piper.http_server` imports flask, which is not one of
piper-tts's dependencies, so installing only piper-tts yields a server that dies
on startup:

```bash
python3 -m pip install --user piper-tts flask
```

Helix checks this before anything else: `/blackbox setup` verifies that
`import piper.http_server` succeeds *before* offering the ~60 MB voice download,
and re-checks after an install, since `pip` can exit 0 having installed into a
different interpreter than the one about to be launched.

## §5.2 Running as a service on a headless board

```bash
helix daemon install     # writes ~/.config/systemd/user/helix-daemon.service
```

The emitted unit is edge-aware: it pulls in `network-online.target` (with
`Wants=`, not merely `After=`, which alone is inert), bounds restart storms so a
crash loop cannot hammer a small board, and carries commented `Environment=`
lines for the knobs in §6.

`/reboot` and this are **different kinds of supervision and do not interact**.
The unit supervises `helix daemon`, a background service with no terminal;
`/reboot` restarts an *interactive* shell and supervises it from inside its own
process, so systemd sees one unit that never exits and never restarts. Exit
status **86** is the interactive shell's private signal to its own supervisor and
never reaches systemd — but if you write a unit of your own around an interactive
Helix, do not add it to `RestartPreventExitStatus=`, and do not treat it as a
failure code.

**The step that catches people out** is lingering. A `systemd --user` service
stops at logout and does **not** start at boot unless the account has lingering
enabled — so on an appliance nobody logs into, a perfectly installed daemon
simply never runs:

```bash
sudo loginctl enable-linger "$USER"
```

`helix daemon install` checks this for you and says so when it is off. Two more
things a headless board usually needs:

```bash
sudo usermod -aG audio "$USER"          # microphone + speaker access (re-login)
journalctl --user -u helix-daemon -f    # watch it run
```

Output written by `helix daemon` under systemd or launchd is **plain text**:
colour switches itself off when stdout is not a terminal, so the journal holds
no escape sequences. `NO_COLOR` disables it everywhere; `CLICOLOR_FORCE=1`
re-enables it when you are piping into something that renders ANSI.

**Updating a board** is `/reboot`, which self-updates from the project's GitHub
releases. The release itself is published from a workstation with `make release`
(see the README's Releasing section) — a board never builds its own, and the
binary it installs is the same signed artifact everyone else gets.

## §6. Post-deploy verification (any device)

Run these inside Helix after install:

- `/doctor` — full system diagnostics, including an **"Edge appliance" section**:
  detected board, whether this build can actually produce sound (the `audio_cgo`
  question, answered for the running binary), which confinement backend is truly
  in force plus how to fix it if it degraded, recorder presence, each local
  sidecar's reachability, whether the offline-LLM fallback model is pulled, and
  temperature with a throttling verdict.
- `/blackbox status` — mode, hearing, sight, wake, retained context, interrupt method; then STT/TTS chain health, which keys are set, recorder detection.
- `/blackbox stats` — what the board has actually *measured*: wake→execution
  latency, TTS time-to-first-audio, frame-to-insight, wake rate, and daemon
  availability, each judged against the project's targets with local and cloud
  paths graded separately. On an edge box this is the honest answer to "is this
  fast enough here", replacing a guess with the numbers from your own hardware.
- `/mictest` — 3-second capture self-test: proves the mic is actually being heard (level + dBFS +
  speech-gate verdict). The fastest way to catch a wrong input device on a headless board.
- `/reboot check` — reports whether a newer Helix exists without installing or
  restarting anything. On an appliance, knowing is often the whole question.
- `/reboot` — worth running once on a headless board, because "does it come back"
  is a question with a real answer here: the shell restarts itself and should
  return in the same mode, in the same directory, with the conversation intact. On
  an appliance you cannot easily reach, finding out that it does *not* while you
  are still standing next to it is the point.

### A note on CSM-1B for edge boards

Sesame CSM-1B (§3.5 of [local_runtimes.md](local_runtimes.md)) is the best local
voice Helix can use and is **not** an edge-board option. It is a 1B autoregressive
model generating 12.5 audio frames per second and wants roughly 8 GB of VRAM to
stay ahead of playback; a Pi 5 has neither the memory bandwidth nor a candle
backend for it. On every board in this matrix, Piper remains the right voice.
CSM belongs on the workstation-class machines at the top of §4: a discrete NVIDIA
GPU, or Apple Silicon with Metal.

### Measured costs of the always-on parts

Numbers from an M4 Mac — a fast box, so treat these as a floor and re-measure on
the board with `/blackbox stats` and `make live-sidecar`. They are here because
"will the always-listening parts eat my Pi?" is the first question an edge
deployment raises, and it now has an answer instead of an assurance.

| Always-on component | Cost per 1.5 s chunk | Duty cycle |
| :--- | :--- | :--- |
| Wake-word detection (energy engine) | 21 µs, zero allocations | **0.0014 %** |
| Ambient analysis (when `ambient.enabled`) | 571 µs, 852 KB allocated | **0.038 %** |
| Restart supervisor (after one `/reboot`) | idle, blocked in `wait` | **not measured** |

`/reboot` also self-updates — automatically, with no confirmation — which has two consequences on a small board.
It replaces the binary **in place** and keeps the previous one beside it as
`helix.prev`, so budget for two copies on a constrained filesystem. And it
refuses when the install directory is not writable by the user running Helix —
the normal state for a package-manager install under `/usr/local/bin` — checked
BEFORE the download rather than after, so the refusal costs nothing.

The supervisor row is listed rather than omitted because the section promises an
answer instead of an assurance, and this one is honestly unknown. Using `/reboot`
once leaves the original process alive for the life of the session, blocked
waiting on its replacement — it holds no database handle and does no work, but it
is a second Go heap on a board with little to spare, and nobody has put a number
on it. It never becomes a third: a supervised child asking to reboot exits with a
status the supervisor already waiting recognises, rather than spawning anything.

Both microphone figures are pure Go on the CPU and both are tee'd off one capture
stream, so enabling ambient awareness does not add a second microphone reader. The ambient
figure includes a WAV decode and an FFT padded to 32768 points; it scales
superlinearly with chunk length, so raising `speech.wake_word.chunk_ms` costs more
than proportionally.

One-shot local sidecar costs on the same machine, for scale against the cloud
path: whisper.cpp `ggml-base.en` transcribed a 3-second clip in **133 ms** at
**97 % word accuracy** on synthesized clean speech; Piper returned a short
sentence in **103 ms**. A Pi 5 will be several times slower on both and still
comfortably inside a conversational budget; a Pi 4 or Jetson Nano is why §4
recommends the cloud path.

Useful environment knobs for edge boards:

- `HELIX_AUDIO_DEVICE` — override the capture device when the board's default mic isn't the one you
  want (e.g. a USB mic on a Jetson).
- `HELIX_SOX_SILENCE_SECS` — how long a pause may run before Helix treats the turn as finished
  (default `1.5`, clamped to 0.3–10). Lower is snappier and risks cutting you off mid-thought;
  higher feels slower to answer. This, not a duration cap, is what ends a turn.
- `HELIX_SOX_SILENCE_PCT` — raise the silence floor (e.g. `2%`) in a noisy room / with a hot mic so
  endpointing doesn't trip early.

**Camera note for headless boards.** Vision shells out to `ffmpeg` against
`/dev/video0` by default. A capture is bounded at 8 seconds and then reports why
it failed, so a missing or busy device says so instead of stalling a turn — and
`/blackbox status` will not claim the camera is working until a frame has actually
arrived. On Linux the usual causes are the user not being in the `video` group, or
no camera at all; there is no per-app permission prompt to grant as there is on
macOS.

---

## §7. Summary

- **Helix-the-shell is genuinely portable** across Linux edge devices — one CGO-free cross-compile.
- **Helix-the-voice-assistant is as capable as the device's sidecar story allows.** Weak boards
  (Jetson Nano 1st-gen, Pi 4) → cloud voice path, smooth. Strong boards (Pi 5, amd64 mini-PC) →
  fully-local is realistic.
- Two device-specific build concerns beyond arch: **`audio_cgo`** for speaker output, and
  **`bubblewrap`** to preserve confinement on kernels older than 5.13.
