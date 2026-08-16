# Helix Threat Model — BlackBox Voice & Multimodal Extension

Companion to `threat_model.md` (Instruction Firewall). Covers the new attack
surfaces introduced by the BlackBox initiative: microphone input (STT), spoken
output (TTS), wake-word listening, camera frames (vision), ambient audio
analysis, and the always-on daemon with local IPC.

Governing decision records: ADR-002 (sidecars), ADR-003 (external recorders),
ADR-004 (stdlib socket IPC), ADR-005 (Voice Risk Policy) — see
`BlackBox_Development.md` §3.

## Core principle: voice is an untrusted input channel

The existing Instruction Firewall treats *retrieved knowledge* as zero-authority
data. Speech transcription inverts the problem: any audible source in the
environment — a TV, a podcast, a song lyric, a person across the room — becomes
*text with user authority* the moment it is transcribed. Nothing in the text
itself distinguishes "the owner said it" from "the room said it". BlackBox
therefore treats the voice channel as **user-intent input with permanently
reduced authority**, enforced structurally (see ADR-005), not by prompting.

## Threats and controls

| # | Threat | Control |
|---|--------|---------|
| V1 | **Voice-channel injection.** Ambient audio transcribed into text that is then planned and executed with full user authority, bypassing the Instruction Firewall (which guards RAG text only). | Voice Risk Policy (ADR-005): voice-originated plans capped at Medium risk; High-risk unreachable from voice regardless of phrasing; fail-closed confirmations; wake-session lockout; transcript provenance escalation mirrors firewall rules. |
| V2 | **Wake-word false positive** → unintended listening and possible execution of whatever is said next. | Sensitivity presets + debounce/cooldown; full safety pipeline still applies to anything executed; false-positive/hour metric logged locally; `"stop listening"` kill switch from voice and terminal. |
| V3 | **Voice mimicry / playback attack** used to confirm a dangerous action (recorded "yes", synthesized clone). | Dangerous actions (force push, hard reset, clean worktree, delete main, critical-package removal) require *typed* confirmation. Voice confirmation is structurally impossible: the voice prompter refuses to satisfy typed-confirmation requests and instructs the user to type. |
| V4 | **Camera privacy.** Frames persisted to disk, sent to an unintended provider, or captured while the user believes vision is off. | Vision strictly opt-in (`/eyes on`, default OFF); activation announced via TTS; frames are memory-only (filesystem-snapshot test enforces zero persistence); one configured vision provider; every frame batch journaled (metadata only, never pixels); `/eyes off` and the voice phrase "turn off your eyes" deactivate instantly. |
| V5 | **Transcript/audio persistence leakage** from logs. | Voice interaction log opt-in (default absent); 0600 permissions; redaction consistent with the diagnostics package; `/purge` wipes all voice artifacts. |
| V6 | **Cloud provider data exposure.** Audio, text, or frames leaving the machine to STT/TTS/vision vendors. | Per-provider explicit opt-in with user-entered keys; the setup wizard states exactly what is sent where; local sidecar chain (whisper.cpp, Piper) is the documented private default; no telemetry, no phone-home pricing/catalog fetches (pricing is embedded data + local user override). |
| V7 | **Daemon IPC hijack.** A local process connects to the daemon socket and issues commands. | Socket lives in `~/.helix/` (0700 dir) with 0600 permissions — only the owning UID can connect; optional shared-token file; the daemon refuses `submit` while an interactive TTY session holds the active-session lock. |
| V8 | **Sidecar supply chain.** whisper.cpp / Piper / openWakeWord installers fetch attacker-substituted binaries. | Installer scripts pin versions and publish checksums; installation requires explicit user consent per component (mirrors the Ollama installer UX); health checks report sidecar version mismatches. |

## Voice Risk Policy (normative summary)

1. Voice-originated plans are capped at **Medium risk**. High-risk commands are
   blocked with a spoken explanation.
2. The deny-by-voice list (actions whose built-in confirmation is *typed*) can
   never be satisfied by voice: force push, hard reset, clean worktree, delete
   main branch, critical package uninstall.
3. Voice confirmations **fail closed**: timeout, silence, or an unintelligible
   answer equals *decline*.
4. Transcripts below the confidence threshold trigger a clarification loop,
   never execution.
5. Wake-armed sessions lock back to wake-only listening after 60 seconds of
   inactivity.
6. Every policy decision (cap applied, deny, timeout-decline) is journaled.

## Testing enforcement

- Policy behavior is proven by **synthetic transcript injection** —
  `InputEvent{Channel: "voice", Text: ...}` fed programmatically, no microphone
  required — in table-driven unit tests covering the full deny-list matrix
  (action × channel × confidence).
- A filesystem-snapshot test proves no frame or audio bytes land on disk outside
  explicitly opt-in paths.
- Hardware-independent CI: all speech tests run against `httptest` mock
  endpoints or WAV fixtures; hardware checks are manual QA with logged
  checklists.

## Residual risk (honest statement)

An attacker with physical proximity who can produce convincing speech while the
assistant is wake-armed can *attempt* Medium-risk actions. The controls make
such attempts loud (spoken confirmations, chimes, journal entries) and bounded
(no High risk, no destructive git, no typed-confirmation actions). This is the
accepted trade-off for a voice-first assistant; it cannot be eliminated without
eliminating voice.
