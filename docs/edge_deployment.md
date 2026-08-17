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
  prints; you just won't hear TTS. `/voice-status` and `/mictest` report the recorder/output state.
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
has a network connection. Configure any path with `/voice-setup`; set a local fallback so a dropped
network keeps voice alive (the failover chain flips local-first automatically — ADR-011/012).

---

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

## §6. Post-deploy verification (any device)

Run these inside Helix after install:

- `/doctor` — full system diagnostics (recorder, sidecars, daemon, confinement backend).
- `/voice-status` — STT/TTS chain health, which keys are set, recorder detection.
- `/mictest` — 3-second capture self-test: proves the mic is actually being heard (level + dBFS +
  speech-gate verdict). The fastest way to catch a wrong input device on a headless board.

Useful environment knobs for edge boards:

- `HELIX_AUDIO_DEVICE` — override the capture device when the board's default mic isn't the one you
  want (e.g. a USB mic on a Jetson).
- `HELIX_SOX_SILENCE_PCT` — raise the silence floor (e.g. `2%`) in a noisy room / with a hot mic so
  endpointing doesn't trip early.

---

## §7. Summary

- **Helix-the-shell is genuinely portable** across Linux edge devices — one CGO-free cross-compile.
- **Helix-the-voice-assistant is as capable as the device's sidecar story allows.** Weak boards
  (Jetson Nano 1st-gen, Pi 4) → cloud voice path, smooth. Strong boards (Pi 5, amd64 mini-PC) →
  fully-local is realistic.
- Two device-specific build concerns beyond arch: **`audio_cgo`** for speaker output, and
  **`bubblewrap`** to preserve confinement on kernels older than 5.13.
