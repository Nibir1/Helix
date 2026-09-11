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
| V1 | **Voice-channel injection.** Ambient audio transcribed into text that is then planned and executed with full user authority, bypassing the Instruction Firewall (which guards RAG text only). | Voice Risk Policy (ADR-005): voice-originated plans capped at Medium risk; High-risk unreachable from voice regardless of phrasing; fail-closed confirmations; re-opening a closed microphone is refused for any non-typed cause, enforced inside the state transition itself rather than only at the command handler; transcript provenance escalation mirrors firewall rules. |
| V2 | **Wake-word false positive** → unintended listening and possible execution of whatever is said next. | Sensitivity presets + debounce/cooldown; full safety pipeline still applies to anything executed; false-positive/hour metric logged locally; two spoken stop switches with different strengths — `"you can turn off now"` / `"stop listening"` end the conversation but keep the wake word live, `"manual mode"` / `"close the mic"` close the microphone and persist it — plus a re-arm grace window so the tail of the phrase itself cannot re-trigger the wake it just dismissed. |
| V3 | **Voice mimicry / playback attack** used to confirm a dangerous action (recorded "yes", synthesized clone). | Dangerous actions (force push, hard reset, clean worktree, delete main, critical-package removal) require *typed* confirmation. Voice confirmation is structurally impossible: the voice prompter refuses to satisfy typed-confirmation requests and instructs the user to type. |
| V4 | **Camera privacy.** Frames persisted to disk, sent to an unintended provider, or captured while the user believes vision is off. | Vision is OFF by default and opens only on an explicit act: `/blackbox eyes on`, or `/blackbox on`, which enables it as part of going live. **That second path is a deliberate widening** — live mode is a camera consent moment by definition, so it is announced (TTS + banner) and `/blackbox off` closes the camera with the mode. Frames are memory-only (filesystem-snapshot test enforces zero persistence); one configured vision provider (`vision.provider`/`vision.model`); every frame batch journaled (metadata only, never pixels); `/blackbox eyes off` and the voice phrase "turn off your eyes" deactivate instantly without leaving the conversation. The phrase is matched loosely on purpose: a privacy control should fail toward closing the camera. |
| V4b | **Unattended capture.** The companion loop samples the camera on a timer, with no per-frame user action. | Runs only inside live mode, which the user entered explicitly and which announced the camera; stops with `/blackbox off` or "turn off your eyes"; `companion.enabled=false` disables initiative entirely while keeping the camera available on request. Frames are diffed in-process and an unchanged scene never reaches a model at all, so a still room is not silently streamed to a provider. Same memory-only and journal guarantees as any other frame. |
| V4c | **Camera intent guessing** (RESOLVED 2026-08-22). A heuristic fired the camera on any spoken sentence containing "this", "that" or "here", so ordinary phrasing became a capture. | Removed, not tuned. It answered "what do we have in *this* directory?" from a vision model with no knowledge of the shell — a frame taken for a question the camera could not answer. The camera now has exactly two doors, both explicit: `/blackbox look`, and the planner choosing its `vision` tool. Recorded rather than erased because the mitigation is the absence of the heuristic, and a future convenience feature would reintroduce the threat. |
| V5b | **Conversational context retention.** Enabling a context-conditioned voice (CSM-1B, `speech.tts.context_turns > 0`) makes Helix hold recent turns — including captured USER audio — in memory for longer than the turn that produced it, where previously a clip was discarded the moment it was transcribed. | Off by default. **Memory only**: the store touches no filesystem API, enforced by an import test, so "captured audio is never written to disk" is unchanged and there is nothing new for `/purge` to reach. Bounded twice, by turn count and total bytes, evicting oldest-first. Scoped to live mode — `/blackbox off` drops it, so retained audio never outlives the conversation. The audio held is the same audio that was already in memory a moment earlier for transcription; what changed is how long, which is why the bounds are the control. **Observable while active:** the `CONTEXT` row of `/blackbox status` reports the retained turn count and byte total, and flags **retained, unused** when audio is being held that no configured voice can consume. |
| V5c | **Microphone opened during Helix's own reply.** Sentence-boundary barge-in (`/config barge-in on`) samples the microphone in the pause between spoken sentences, so the recorder now runs at moments the user did not initiate a turn — previously it ran only when Helix was listening for one. | Off by default. The probe is ~250 ms per sentence boundary, and its clip follows the same path as every other capture — a temp WAV the recorder writes and `RecordClip` deletes the moment it is read (`defer os.Remove`). Only one RMS value is computed from it: it is **never transcribed**, so no audio reaches a provider, a log, or the conversational context. Its threshold is 10× the ordinary speech gate, which is a privacy property as well as a usability one — the probe is deliberately deaf to anything short of someone clearly addressing the machine. Scoped to live mode and released on `/blackbox off`. Reported on the INTERRUPT row of `/blackbox status` rather than living as a silent config flag, because a feature that opens the microphone should be visible where the other sensors are. |
| V5d | **Voice-triggered persistence of what you just said.** A spoken `/reboot` could have written an excerpt of the last utterance to `~/.helix/reboot.json` on a channel a television can trigger. | **It does not, and that is the control.** The continuity record omits conversation content entirely when the request arrived through the microphone: a spoken restart carries the mode, working directory, provider/model and in-progress task texts, and nothing you said. Rule 8 ("voice may reduce what is collected but never increase it") therefore holds **without an exception** — the feature was changed to fit the principle rather than the principle amended to fit the feature. A TYPED reboot stores a 240-rune excerpt, truncated on a rune boundary, 0600 in 0700, consumed on read, ignored past 12 h, wiped by `/purge`. |
| V5e | **A spoken word installs software.** `/reboot` self-updates — it downloads a release and makes it the program the user runs — and it is voice-reachable, so a television or a bystander could in principle cause an install. | **Accepted, by owner decision, and bounded rather than blocked.** The spoken path DOES install: the release comes from a repository the owner controls and tags deliberately, so publishing it is the authorization, and a confirmation prompt in front of that has one sensible answer. What remains is ADR-019's chain — mandatory checksum matched by filename, a pinned host with redirects refused, a payload that must prove it is Helix for this machine — plus the supervisor's automatic rollback when the new binary cannot start within ten seconds. The residual risk is explicit: whoever can publish to the configured repo can replace the binary with no human present, and a bystander saying "reboot" can trigger the install. `update.check: false` removes it on a machine where the publisher and the operator are not the same person. |
| V5f | **A spoken word ends the process.** `/reboot` is voice-reachable, so a television, a podcast or a person in the room saying "reboot" at the end of a sentence restarts the shell. | Bounded by making the restart cheap rather than by making it hard to trigger. The continuity record is written **before** the process ends, so the cost of a false positive is a few seconds and a shell that comes back in the same mode, the same directory, on the same provider, with the conversation intact and the in-progress task named. Matched as a **suffix**, so it must end the utterance, and **questions are excluded** by their opening word — "what happens when you reboot" is answered, not obeyed — because STT punctuation is a guess and several providers never emit a question mark. It is the only DANGER ZONE command voice can reach, and the carve-out is data loss rather than severity: every other one destroys something. Journalled as its own outcome (`reboot`) so an audit can tell a spoken restart from a typed one. |
| V5 | **Transcript/audio persistence leakage** from logs. | Voice interaction log opt-in and **default absent** — with it off there is no directory and no file, enforced by test, not merely an empty log. Enabled, it stores **text only, never audio**: captured clips are deleted the moment they are read, so there is no audio artifact to reference. `~/.helix/voice_log/` is 0600 inside a 0700 directory, control characters are stripped so a transcript cannot carry terminal escapes into a later `cat`, entries are length-bounded, and the file rotates (1 MiB × 3 generations) so an always-on assistant cannot fill a small board's disk. `/purge` wipes it, and `make delete-secrets` removes it alongside the API keys — a transcript is a credential-adjacent artifact and belongs in the same sweep. **Voice may stop the log but never start it** — enabling it moves the privacy posture, so it is typed-only, while disabling by voice always works. The writing package imports no networking at all, grep-enforced in CI like `internal/diagnostics`. |
| V6 | **Cloud provider data exposure.** Audio, text, or frames leaving the machine to STT/TTS/vision vendors. | Per-provider explicit opt-in with user-entered keys; the setup wizard states exactly what is sent where; local sidecar chain (whisper.cpp, Piper) is the documented private default; no telemetry, no phone-home pricing/catalog fetches (pricing is embedded data + local user override). **Streaming STT is a different shape of exposure and is called out separately:** `deepgram` and `gpt-live-transcribe` hold a WebSocket open and stream continuous PCM for the length of a turn, rather than POSTing one clip and closing. Nothing is retained locally either way — there is no clip to delete because none is written — but "audio leaves the machine" is continuous rather than per-utterance while a streaming turn is in progress. Neither is a chain default; both are opt-in by provider selection. |
| V7 | **Daemon IPC hijack.** A local process connects to the daemon socket and issues commands. | The token is per-start and revocable: `make delete-secrets` removes `daemon.conn.json` with the API keys. Socket lives in `~/.helix/` (0700 dir) with 0600 permissions — only the owning UID can connect; optional shared-token file; the daemon refuses `submit` while an interactive TTY session holds the active-session lock. |
| V8 | **Sidecar supply chain.** whisper.cpp / Piper / openWakeWord installers fetch attacker-substituted binaries. | Installer scripts pin versions and publish checksums; health checks report sidecar version mismatches. **Consent is now per CHAIN rather than per component** (ADR-002's "optional auto-install"): selecting a speech chain installs everything that chain needs — runtime, model file, server, and host packages such as Python, git or cargo — without a further prompt, because being asked about the file a chosen chain cannot start without has one sensible answer. The control that carries the weight is unchanged and structural: **Helix never runs a guessed package name** (a manager with no verified catalogue entry produces guidance, not a command), the command is printed before it runs, and what may be installed is bounded by `internal/deps` plus the wizard's own sidecar table. The residual risk is stated plainly in SECURITY.md §5c: on Linux those commands carry `sudo`, they follow one selection rather than one per package, and installers now inherit stdin so a `sudo` password prompt reaches the terminal rather than failing off-screen. The password goes to `sudo`; Helix neither reads nor stores it. **Piper's standalone binary is the concrete case:** ~26 MB of executable fetched over the network and then run, so the release tag and a SHA-256 are pinned in `internal/speech/piper_native.go`, the download is verified BEFORE extraction and the archive deleted on mismatch, and extraction refuses any entry whose path escapes the destination (tar-slip). Done in Go rather than as a shell command — `runVisibleCommand` execs directly with no shell, so a `curl \| shasum \| tar` pipeline could not have run at all, let alone verified anything. |

| V2b | **The microphone is open at a keyboard prompt, by default** (2026-09-08; default reversed 2026-09-09). `/blackbox wake on` arms an idle manual prompt so a sound can enter live mode, which means the recorder runs during work that has nothing to do with voice — potentially all day — and as of 2026-09-09 it does so **on a fresh install, without anyone enabling it**. | **The off-by-default mitigation is gone, by owner decision, and that is stated rather than absorbed.** What remains: nothing is transcribed while armed (the detector scores chunks and discards them — this is the STANDBY state, and it is the only state where that claim holds; see V2c for AWAKE); the armed state is **visible**, announced once per session in full with the switch that stops it and continuously by the standby HUD, which exists exactly while the microphone does; it arms only where a recorder AND a transcriber AND keystroke readiness all exist, so a host that cannot listen behaves exactly as before rather than pretending; `/blackbox wake off` closes the microphone outright (the MANUAL state), and voice can never reopen it; and ADR-005 is intact where it matters — turning listening **on** is typed-only (`voiceStartsAlwaysListen` refuses the spoken form, which had to move from `wake always on` to `wake on` when the switches merged), while turning it **off** always works by voice. Unix-only; Windows reports unavailable instead of appearing to work. **The residual risk is the default itself:** someone who installs Helix and never reads a banner has an open microphone at their prompt, and the honest mitigation for that is one config key (`speech.wake_word.enabled: false`) rather than a claim that it cannot happen. **And for one day this row overstated the exposure:** the reversed default was written into `WakeWordDefaults()` but `applyWakeWordDefaults` never touched the booleans, so every config that already existed kept the microphone closed while this document said it was open. Fixed 2026-09-09 by making both keys `*bool` — recorded here because a threat model that describes a stronger default than the code applies is a defect in the same direction as one that describes a weaker one. |

| V2c | **AWAKE holds an open transcribing microphone** (2026-09-11). A wake no longer gates each turn: once awake, every utterance is transcribed, sent to an STT provider and planned, with no further gate, until something ends the conversation. This is strictly larger than V2b — V2b is a microphone that scores and discards, this one ships audio off the machine continuously — and it is billed per minute. | **Five controls, and one of them is the only one that works with nobody present.** (1) The inactivity stand-down, `speech.wake_word.awake_idle_stand_down_s`, default 600s, drops AWAKE back to STANDBY; `0` disables it and removes the bound. (2) Three classes of spoken stop phrase, two of which end the conversation and one of which closes the microphone outright. (3) A stop phrase on weak evidence asks first, and the conversational closers ("that's all", "we're done") ask *every* time, because they are also how an ordinary request ends. (4) Entering AWAKE or STANDBY from MANUAL is refused for any non-typed cause, enforced in `setListenMode` rather than in a command handler, so it holds for every door — voice can close the microphone but can never reopen it. (5) The state is visible: `/blackbox status` names it, the LIVE panel announces the exits, and the standby HUD runs exactly while the microphone does. **Residual risk, stated:** the stand-down clock is refreshed by any audible clip including a misheard one, so a continuously noisy room holds AWAKE open indefinitely; and a user who sets the key to `0` has an unbounded open microphone with no warning beyond this row. |
| V2d | **A wake event fires on the tail of the sentence dismissing it.** With the default energy engine a wake is speech ONSET, not a phrase, so "you can turn off now" ends the conversation and its own decay immediately starts another — the reported failure that made the stop phrases feel broken. | A re-arm grace window (`speech.wake_word.rearm_delay_ms`, default 3000 ms, 5000 ms after a *spoken* exit) drops wake events just after entering STANDBY. Implemented as drop-and-continue inside the wake hold, so the scanner keeps running and a genuine wake a moment later still lands — the window suppresses events, it does not close the microphone. `0` restores the old instant re-arm. The cost is symmetrical and worth knowing: for those few seconds you cannot deliberately re-enter either. |
| V2e | **The keyboard is live during a capture** (2026-09-11), so a stdin poller runs and the terminal is held in cbreak mode for the length of every voice turn. | Privacy-POSITIVE on balance, and the reason it is recorded: a keystroke cancels the capture and the partial clip is **discarded without being transcribed**, so audio recorded up to the moment you started typing never reaches a provider. That guarantee has a test behind it rather than a comment, because a killed recorder can return a fragment with no error and transcribing half a sentence could come back as a stop phrase. The terminal is left in **cbreak, not raw**, specifically so `ISIG` survives and Ctrl+C still cancels a recorder — raw mode would have silently deleted that escape hatch. `speech.wake_word.awake_keyboard: false` turns the whole mechanism off; Windows has no termios and never had it. |

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
5. **A wake opens a conversation, and the bound on it is the inactivity
   stand-down.** Rewritten 2026-09-11, and the change is an increase in
   exposure that is stated rather than absorbed: waking no longer gates each
   turn. Once AWAKE, every utterance is transcribed and planned with no
   re-waking in between, until a stop phrase, a typed `/blackbox off`, or
   `speech.wake_word.awake_idle_stand_down_s` of silence (default 600s). So the
   exposure is **one sound buys a conversation**, not one sound buys a turn.
   Setting that key to `0` removes the only control that does not require a
   person, and should be read as doing exactly that.

   The stand-down is measured from the last sign of a person — any clip that
   clears the energy gate, including one that is then misheard, and any
   keystroke — so a continuously noisy room can hold AWAKE open indefinitely.
   That is the residual risk, and it is recorded here rather than argued away.

   **The earlier defect this replaces** is kept because the shape of the mistake
   matters: a 60-second *deadline on wake-only listening* expired into OPEN
   capture, which is §5 inverted. A real session showed the cost — sixty quiet
   seconds after going live, the microphone opened unbidden, fan noise became a
   0.5s clip, whisper turned it into "May he leave." and Helix answered a turn
   nobody took; two turns later a hallucinated "Manual mode." matched a stop
   phrase and ended live mode by itself. The stand-down runs the other way: it
   expires into LESS listening, never into transcription, and it is evaluated
   before a capture opens rather than as a deadline inside one.

   **STANDBY is unchanged:** nothing is transcribed while waiting for a wake,
   for as long as that takes, and a scanner that dies still falls through with
   a notice because stranding a user behind a broken microphone is worse than
   an ungated capture they can see.
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

**The Medium cap is a ceiling, and until 2026-09-08 the floor had a hole worth
recording here.** Every control above is stated in terms of tiers, which assumes
the tiers are assigned correctly — and the Medium tier for redirection tested for
`" > "` with a space on each side. So a plan step written `echo key
>~/.ssh/authorized_keys` was graded **Low**: not capped, not confirmed, not
loud, just run. Nothing about the voice channel caused it (a typed turn reached
the same tier), but the voice channel is where it mattered most, because "the
attempt is loud" is the entire residual-risk argument and a Low-risk step makes
no sound at all. Redirection is now matched by operator shape rather than by
spacing, and the separate hard block against writing to a raw disk device — which
had never matched anything, being mis-escaped — works too. See `SECURITY.md` §1.

The general lesson, since this file will be read again after the next feature:
a control expressed as "capped at Medium" inherits every bug in how Medium is
decided, and those bugs live in a different package from this policy.
