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
| V4 | **Camera privacy.** Frames persisted to disk, sent to an unintended provider, or captured while the user believes vision is off. | Vision is OFF by default and opens only on an explicit act: `/blackbox eyes on`, or `/blackbox on`, which enables it as part of going live. **That second path is a deliberate widening** — live mode is a camera consent moment by definition, so it is announced (TTS + banner) and `/blackbox off` closes the camera with the mode. Frames are memory-only (filesystem-snapshot test enforces zero persistence); one configured vision provider (`vision.provider`/`vision.model`); every frame batch journaled (metadata only, never pixels); `/blackbox eyes off` and the voice phrase "turn off your eyes" deactivate instantly without leaving the conversation. The phrase is matched loosely on purpose: a privacy control should fail toward closing the camera. |
| V4b | **Unattended capture.** The companion loop samples the camera on a timer, with no per-frame user action. | Runs only inside live mode, which the user entered explicitly and which announced the camera; stops with `/blackbox off` or "turn off your eyes"; `companion.enabled=false` disables initiative entirely while keeping the camera available on request. Frames are diffed in-process and an unchanged scene never reaches a model at all, so a still room is not silently streamed to a provider. Same memory-only and journal guarantees as any other frame. |
| V4c | **Camera intent guessing** (RESOLVED 2026-08-22). A heuristic fired the camera on any spoken sentence containing "this", "that" or "here", so ordinary phrasing became a capture. | Removed, not tuned. It answered "what do we have in *this* directory?" from a vision model with no knowledge of the shell — a frame taken for a question the camera could not answer. The camera now has exactly two doors, both explicit: `/blackbox look`, and the planner choosing its `vision` tool. Recorded rather than erased because the mitigation is the absence of the heuristic, and a future convenience feature would reintroduce the threat. |
| V5b | **Conversational context retention.** Enabling a context-conditioned voice (CSM-1B, `speech.tts.context_turns > 0`) makes Helix hold recent turns — including captured USER audio — in memory for longer than the turn that produced it, where previously a clip was discarded the moment it was transcribed. | Off by default. **Memory only**: the store touches no filesystem API, enforced by an import test, so "captured audio is never written to disk" is unchanged and there is nothing new for `/purge` to reach. Bounded twice, by turn count and total bytes, evicting oldest-first. Scoped to live mode — `/blackbox off` drops it, so retained audio never outlives the conversation. The audio held is the same audio that was already in memory a moment earlier for transcription; what changed is how long, which is why the bounds are the control. **Observable while active:** the `CONTEXT` row of `/blackbox status` reports the retained turn count and byte total, and flags **retained, unused** when audio is being held that no configured voice can consume. |
| V5c | **Microphone opened during Helix's own reply.** Sentence-boundary barge-in (`/config barge-in on`) samples the microphone in the pause between spoken sentences, so the recorder now runs at moments the user did not initiate a turn — previously it ran only when Helix was listening for one. | Off by default. The probe is ~250 ms per sentence boundary, and its clip follows the same path as every other capture — a temp WAV the recorder writes and `RecordClip` deletes the moment it is read (`defer os.Remove`). Only one RMS value is computed from it: it is **never transcribed**, so no audio reaches a provider, a log, or the conversational context. Its threshold is 10× the ordinary speech gate, which is a privacy property as well as a usability one — the probe is deliberately deaf to anything short of someone clearly addressing the machine. Scoped to live mode and released on `/blackbox off`. Reported on the INTERRUPT row of `/blackbox status` rather than living as a silent config flag, because a feature that opens the microphone should be visible where the other sensors are. |
| V5d | **Voice-triggered persistence of what you just said.** A spoken `/reboot` could have written an excerpt of the last utterance to `~/.helix/reboot.json` on a channel a television can trigger. | **It does not, and that is the control.** The continuity record omits conversation content entirely when the request arrived through the microphone: a spoken restart carries the mode, working directory, provider/model and in-progress task texts, and nothing you said. Rule 8 ("voice may reduce what is collected but never increase it") therefore holds **without an exception** — the feature was changed to fit the principle rather than the principle amended to fit the feature. A TYPED reboot stores a 240-rune excerpt, truncated on a rune boundary, 0600 in 0700, consumed on read, ignored past 12 h, wiped by `/purge`. |
| V5e | **A spoken word installs software.** `/reboot` self-updates — it downloads a release and makes it the program the user runs — and it is voice-reachable, so a television or a bystander could in principle cause an install. | **Accepted, by owner decision, and bounded rather than blocked.** The spoken path DOES install: the release comes from a repository the owner controls and tags deliberately, so publishing it is the authorization, and a confirmation prompt in front of that has one sensible answer. What remains is ADR-019's chain — mandatory checksum matched by filename, a pinned host with redirects refused, a payload that must prove it is Helix for this machine — plus the supervisor's automatic rollback when the new binary cannot start within ten seconds. The residual risk is explicit: whoever can publish to the configured repo can replace the binary with no human present, and a bystander saying "reboot" can trigger the install. `update.check: false` removes it on a machine where the publisher and the operator are not the same person. |
| V5f | **A spoken word ends the process.** `/reboot` is voice-reachable, so a television, a podcast or a person in the room saying "reboot" at the end of a sentence restarts the shell. | Bounded by making the restart cheap rather than by making it hard to trigger. The continuity record is written **before** the process ends, so the cost of a false positive is a few seconds and a shell that comes back in the same mode, the same directory, on the same provider, with the conversation intact and the in-progress task named. Matched as a **suffix**, so it must end the utterance, and **questions are excluded** by their opening word — "what happens when you reboot" is answered, not obeyed — because STT punctuation is a guess and several providers never emit a question mark. It is the only DANGER ZONE command voice can reach, and the carve-out is data loss rather than severity: every other one destroys something. Journalled as its own outcome (`reboot`) so an audit can tell a spoken restart from a typed one. |
| V5 | **Transcript/audio persistence leakage** from logs. | Voice interaction log opt-in and **default absent** — with it off there is no directory and no file, enforced by test, not merely an empty log. Enabled, it stores **text only, never audio**: captured clips are deleted the moment they are read, so there is no audio artifact to reference. `~/.helix/voice_log/` is 0600 inside a 0700 directory, control characters are stripped so a transcript cannot carry terminal escapes into a later `cat`, entries are length-bounded, and the file rotates (1 MiB × 3 generations) so an always-on assistant cannot fill a small board's disk. `/purge` wipes it. **Voice may stop the log but never start it** — enabling it moves the privacy posture, so it is typed-only, while disabling by voice always works. The writing package imports no networking at all, grep-enforced in CI like `internal/diagnostics`. |
| V6 | **Cloud provider data exposure.** Audio, text, or frames leaving the machine to STT/TTS/vision vendors. | Per-provider explicit opt-in with user-entered keys; the setup wizard states exactly what is sent where; local sidecar chain (whisper.cpp, Piper) is the documented private default; no telemetry, no phone-home pricing/catalog fetches (pricing is embedded data + local user override). |
| V7 | **Daemon IPC hijack.** A local process connects to the daemon socket and issues commands. | Socket lives in `~/.helix/` (0700 dir) with 0600 permissions — only the owning UID can connect; optional shared-token file; the daemon refuses `submit` while an interactive TTY session holds the active-session lock. |
| V8 | **Sidecar supply chain.** whisper.cpp / Piper / openWakeWord installers fetch attacker-substituted binaries. | Installer scripts pin versions and publish checksums; health checks report sidecar version mismatches. **Consent is now per CHAIN rather than per component** (ADR-002's "optional auto-install"): selecting a speech chain installs everything that chain needs — runtime, model file, server, and host packages such as Python, git or cargo — without a further prompt, because being asked about the file a chosen chain cannot start without has one sensible answer. The control that carries the weight is unchanged and structural: **Helix never runs a guessed package name** (a manager with no verified catalogue entry produces guidance, not a command), the command is printed before it runs, and what may be installed is bounded by `internal/deps` plus the wizard's own sidecar table. The residual risk is stated plainly in SECURITY.md §5c: on Linux those commands carry `sudo`, and they follow one selection rather than one per package. **Piper's standalone binary is the concrete case:** ~26 MB of executable fetched over the network and then run, so the release tag and a SHA-256 are pinned in `internal/speech/piper_native.go`, the download is verified BEFORE extraction and the archive deleted on mismatch, and extraction refuses any entry whose path escapes the destination (tar-slip). Done in Go rather than as a shell command — `runVisibleCommand` execs directly with no shell, so a `curl \| shasum \| tar` pipeline could not have run at all, let alone verified anything. |

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
7. Spoken input never takes the shell fast path. A confidently-classified command
   line runs directly when TYPED; on the voice channel it always goes to the
   planner, because the classifier decides on the first token and ordinary
   sentences begin with command names ("make a new branch called test" ran as
   `make ...`). The safety pipeline covered that path throughout — validation,
   risk tiers and the Medium cap all applied — so this is defence in depth and a
   correctness fix, not a closed hole.
8. Voice may **reduce** what is collected but never increase it. "Turn off your
   eyes" closes the camera and `/blackbox log off` stops transcript recording,
   both by voice; opening the camera as part of live mode is an explicit,
   announced act, and starting the transcript log must be typed. A privacy
   control should fail toward collecting less. **This rule has no exceptions,
   and `/reboot` is where it was tested:** the continuity record it writes omits
   conversation content entirely on the spoken path, so the feature was shaped
   to fit the rule rather than the rule amended to fit the feature (V5d).
9. A **DANGER ZONE command may be voice-reachable if and only if it destroys
   nothing** and its effect is recoverable. The rule is about the ACT, not the
   command: `/reboot` restarts (allowed by voice) and also self-updates
   (typed only), and the split runs through the middle of one command
   because that is where the difference actually is. `/reboot` is the only one that
   qualifies: the continuity record is written before the process ends, so the
   worst a misheard trigger costs is a few seconds, after which the same mode,
   directory, provider and conversation are back. `/purge`, `/rag-reset`, `/commit`,
   `/config`, `/hooks`, `/init`, `/scan`, `/setup` and `/stealth` — the whole
   nine — destroy or move something and remain unreachable. The criterion is data loss, not how alarming the
   command sounds.

## Testing enforcement

- The **spoken restart** carries three proofs: a unit test that the phrase fires
  on how people actually ask ("okay, please reboot") and not on a question about
  it, a unit test that the spoken path stores no conversation content, and two
  end-to-end tests against the real binary proving the process genuinely comes
  back and that the record is consumed rather than replayed on every boot.
- Policy behavior is proven by **synthetic transcript injection** —
  `InputEvent{Channel: "voice", Text: ...}` fed programmatically, no microphone
  required — in table-driven unit tests covering the full deny-list matrix
  (action × channel × confidence).
- A filesystem-snapshot test proves no frame or audio bytes land on disk outside
  explicitly opt-in paths. A second pair — one unit, one end-to-end against the
  real binary — proves the voice log's *default absence*: the unit test asserts a
  disabled log leaves the directory uncreated, and the e2e test asserts the
  shipped wiring leaves it disabled. Those are different claims, and a log opened
  eagerly at startup would pass the first and fail the second.
- A grep test keeps `internal/journal` free of networking imports, so the code
  that writes down what the user said cannot send it anywhere.
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
