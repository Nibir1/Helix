// cmd/helix/voice_mode.go
// Purpose: live-mode switching (BlackBox Phase 2, ADR-008) and the
// per-turn voice capture loop. Voice mode replaces the typed line with a
// record→transcribe cycle; every transcript flows through the SAME pipeline
// as typed input, stamped Channel=voice so the Voice Risk Policy applies.
// Mic failures degrade gracefully: one typed turn is offered rather than
// bricking the shell on mic-less machines.
package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"helix/internal/agent"
	"helix/internal/ambient"
	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/input"
	"helix/internal/journal"
	"helix/internal/metrics"
	"helix/internal/shell"
	"helix/internal/speech"
	"helix/internal/utils"
	"helix/internal/ux"
	"helix/internal/vision"
	"helix/internal/wakeword"
)

var (
	voicePrompter *VoicePrompter
	ttyPrompter   commands.Prompter
)

// initVoiceMode wires prompters and restores the persisted mode. Called once
// from main after speech.Init and agent construction.
func initVoiceMode() {
	ttyPrompter = commands.ActivePrompter()
	voicePrompter = NewVoicePrompter()

	// The one read of config that decides the mode. Everything after this is
	// the enum; nothing re-derives the state from disk mid-session.
	initial := resolveInitialMode()
	if cfg.UserPrefs.VoiceMode && initial != modeAwake {
		// Persisted live mode that the host cannot deliver. Do not strand the
		// user in a mode with no microphone, and do not leave the preference
		// claiming otherwise. Falls back to STANDBY rather than MANUAL: the
		// wake word was not what failed.
		if _, err := speech.DetectRecorder(); err != nil {
			uiWarn("voice mode skipped at startup", err.Error())
		}
		cfg.UserPrefs.VoiceMode = false
		_ = cfg.SavePreferences()
	}
	if initial == modeAwake {
		// A restored session is a conversation, camera and companion included.
		// Restoring it any other way is the "same mode by a different door"
		// defect Phase 13 records.
		setListenMode(modeAwake, causeRestore)
		return
	}
	modeCur.Store(int32(initial))
}

// printLiveBanner is the moment Helix wakes up, and it should look like it.
//
// It is also the only place some of this is ever said: which senses just came
// online, and how to get back out. The old single cyan line carried the exit
// instruction and nothing else, so a user could not tell from the screen
// whether the camera had opened.
//
// FULL ONCE, THEN ONE LINE. Live mode is now entered by speaking, which makes
// it cheap to enter and therefore frequent — a seven-line panel on every wake
// is the same repetition problem the per-turn status line had, and it pushes
// the conversation off the screen. The panel earns its height the first time,
// when it is telling the user something they do not know; after that a single
// line carries the same four facts.
//
// The collapse is CONDITIONAL on nothing being wrong, and that is the load-
// bearing part. blackBoxEyesLine exists because "watching" over a camera that
// delivers no frames is a readiness lie; abbreviating a fault into "eyes on"
// would reintroduce exactly that lie in a new place. So a degraded sense
// re-opens the full panel, however many times live mode has been entered.
func printLiveBanner() {
	first := !liveBannerShown
	liveBannerShown = true

	if !first && liveStateNominal() {
		printLiveReentryLine()
		return
	}

	fmt.Println(shell.PanelTitle("live"))

	w := shell.KVWidth("HEARING", "SIGHT", "VOICE", "EXIT")
	fmt.Println(shell.KV("HEARING", blackBoxHearingLine(), w))
	fmt.Println(shell.KV("SIGHT", blackBoxEyesLine(), w))
	if speech.TTSEnabled() {
		fmt.Println(shell.KV("VOICE", shell.Badge(shell.StateGood, "replies spoken aloud"), w))
	} else {
		fmt.Println(shell.KV("VOICE", shell.Badge(shell.StateIdle, "silent")+
			shell.Muted("  /blackbox tts on"), w))
	}
	fmt.Println(shell.KV("EXIT", shell.Muted("say ")+shell.Value("\"manual mode\"")+
		shell.Muted("  ·  or type /blackbox off"), w))

	// Full explanation on the first panel, a reminder on any later one. A
	// later panel only appears because a sense is degraded, and someone reading
	// a fault report does not need the wake rules restated at four lines.
	for _, line := range voiceModeWakeNotes(cfg.Speech.WakeWord.Listening(),
		cfg.Speech.WakeWord.Engine, first) {
		fmt.Println(shell.PanelLine(shell.Muted(line)))
	}
	fmt.Println(shell.PanelEnd())
}

// liveBannerShown records that the full panel has been printed this session.
// Same discipline as printArmedPrompt and noteWakeLapse: a message that
// explains a RULE is said in full the first time and referenced after.
var liveBannerShown bool

// liveStateNominal reports whether every sense is in a state that can be
// honestly abbreviated.
//
// TTS is not consulted: spoken or silent are both choices the user made, not
// faults, and the collapsed line says which one is in force either way.
func liveStateNominal() bool {
	if classifyHearing() != hearingReady {
		return false
	}
	switch cond, _ := classifyEyes(); cond {
	case eyesNoFrames, eyesOnButBlind:
		return false
	}
	return true
}

// printLiveReentryLine is the collapsed form: the same four facts the panel
// carries, on one row.
//
// Shaped like printArmedPrompt's "◉ listening" line rather than like a panel,
// because it is doing that job — a continuous indicator of which channels are
// open, not an explanation. The exit instruction stays: it is the one thing on
// the panel that is an instruction rather than a status, and the one a user in
// live mode is most likely to want and least able to guess.
func printLiveReentryLine() {
	parts := []string{}
	if chain := sttChainDescription(); chain != "" {
		parts = append(parts, chain)
	}
	if cond, _ := classifyEyes(); cond == eyesWatching {
		parts = append(parts, "eyes on")
	} else {
		parts = append(parts, "eyes off")
	}
	if speech.TTSEnabled() {
		parts = append(parts, "spoken aloud")
	} else {
		parts = append(parts, "silent")
	}
	parts = append(parts, `say "manual mode" to leave`)

	fmt.Println("  " + shell.Fg(shell.HexSecondary, "◉ ") +
		shell.Fg(shell.HexText, "live") +
		shell.Muted("  ·  "+strings.Join(parts, "  ·  ")))
}

// voiceModeWakeNotes explains how wake word and voice mode interact, which is
// the part the two banners together used to get wrong.
//
// /blackbox wake on promised listening "for the wake word" and going live said nothing
// about it, so the reasonable reading — every turn needs the phrase — was doubly
// false: the FIRST turn after /voice on is open capture, and continuous turns
// only pass through wake gating between them (see wakeListenUntilArmed and the
// main loop). Saying so here costs one line and removes the surprise.
//
// Args:
//   - wakeEnabled: cfg.Speech.WakeWord.Enabled.
//   - engine: the configured wake engine.
//   - full: print the whole explanation. False returns the one-line reminder
//     used on re-entry, once the rules have already been stated this session.
//
// Returns: the extra banner lines (nil when wake is off).
// Complexity: O(1).
func voiceModeWakeNotes(wakeEnabled bool, engine string, full bool) []string {
	if !wakeEnabled {
		return nil
	}
	if !full {
		// One line, carrying the two facts that matter: it keeps going, and
		// here is how to stop it.
		return []string{
			fmt.Sprintf("Listening until you say so  ·  \"you can turn off now\" pauses  ·  "+
				"\"manual mode\" closes the mic  ·  stands down after %s quiet",
				roundedDuration(cfg.Speech.WakeWord.AwakeIdleStandDown())),
		}
	}
	// REWRITTEN, because every sentence it used to carry became false.
	//
	// It said wake gating sat BETWEEN turns and that only the first turn needed
	// no wake word. Both were true of the old per-turn hold and are now the
	// opposite of the truth: every turn runs without re-waking, which is the
	// whole point of the change. Leaving the old text would make the banner the
	// most authoritative wrong answer in the shell.
	lines := []string{
		"I stay awake once you wake me — every turn, no waking in between.",
		fmt.Sprintf("Say \"you can turn off now\" to pause, or \"manual mode\" to close the "+
			"microphone. I also stand down on my own after %s with nothing said.",
			roundedDuration(cfg.Speech.WakeWord.AwakeIdleStandDown())),
		"Ctrl+C takes a turn now. Type at any time — the keyboard stays live.",
	}
	if engine != "sidecar" {
		lines = append(lines,
			fmt.Sprintf("Engine %q wakes on any speech, not on a phrase (/blackbox status).",
				engineOrDefault(engine)))
	}
	return lines
}

// handleVoiceCommand: /voice [on|off|status]
// visionSvc is the camera capture service, package-level so readiness can be
// REPORTED rather than assumed. It was a local in main(), which is why nothing
// outside the capture closure could ask whether a frame was possible.
var visionSvc *vision.VisionCaptureService

// captureAvailable reports whether a frame could actually be grabbed on this
// host (ffmpeg discoverable). Distinct from whether a model could understand
// one — see visionReady.
func captureAvailable() bool {
	return visionSvc != nil && visionSvc.Available()
}

// voiceEntryPreflight checks the two conditions without which "voice mode"
// would be a lie: something to record with, and something to transcribe with.
//
// Extracted from the old /voice handler so /blackbox on and any future entry
// point cannot drift apart on what counts as ready.
func voiceEntryPreflight() error {
	if _, err := speech.DetectRecorder(); err != nil {
		return err
	}
	if reg := speech.Default(); reg == nil || len(reg.STTChain()) == 0 {
		return fmt.Errorf("no STT provider configured — run /blackbox setup first")
	}
	return nil
}

// speakDirect speaks text through the TTS chain irrespective of the /tts
// toggle.
//
// /tts governs whether ordinary REPLIES are spoken. Voice-channel bookkeeping —
// a command acknowledgement, a refusal, a clarification — is not a reply: the
// user just spoke to a terminal they may not be looking at, so silence there is
// the actual failure. agentCore.OnSpeak deliberately honors /tts, which is why
// this exists separately rather than reusing it.
func speakDirect(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := speech.SpeakStream(ctx, text); err != nil && utils.IsDebugMode() {
		fmt.Fprintf(os.Stderr, "[voice] notice: %v\n", err)
	}
}

// voiceTurn performs one capture→transcribe cycle and returns the stamped
// input event. The transcript is echoed like a typed line would be. The
// capture registers with the interrupt manager so Ctrl+C cancels recording
// instead of killing Helix.
//
// When the active STT provider supports streaming, voiceTurn shows interim
// partials live and finalizes on the utterance-final result; a failed stream
// dial degrades to the proven batch path.
func voiceTurn(ctx context.Context) (input.InputEvent, error) {
	// Anything the companion has been holding is said HERE, which is one of the
	// two points where the microphone is provably closed: the previous turn has
	// ended and this one has not opened the recorder yet. It used to be drained
	// by the main loop between the turn and the wake hold; that hold is gone,
	// so the drain moved to the surviving boundary rather than to a new one.
	drainCompanion()

	// A new turn supersedes the previous reply. Capture is half-duplex (the
	// recorder cannot run while the speaker does), so anything still playing
	// here is a reply the user has stopped waiting for — most often an ambient
	// notice that landed as they were about to speak.
	speech.StopSpeaking()

	// Ready cue, then get out of the microphone's way. PlayAlertSync (not
	// PlayAlert) waits for the tone to finish and micSettleDelay covers the
	// ring-down after it: arming the recorder inside the chime made sox's
	// silence gate open on Helix's own audio, and a 0.1s clip of the 880Hz ping
	// came back from STT as the word "you" — a full reply to a turn nobody took.
	audio.PlayAlertSync()
	time.Sleep(micSettleDelay)

	if s, ok := speech.StreamingSTT(); ok {
		ev, err := streamingVoiceTurn(ctx, s)
		if err == nil {
			return ev, nil
		}
		if !errors.Is(err, errStreamDial) {
			// Kill phrase, empty result, or a stream that started but produced
			// nothing — do not silently re-record over these.
			return ev, err
		}
		uiIdle("batch capture", "streaming is unavailable: "+err.Error())
	}
	return batchVoiceTurn(ctx)
}

// quietTurnsBetweenReassurance is how many silent turns pass before Helix says
// it is still there.
//
// Not a retry budget — there isn't one. A silent capture blocks until someone
// speaks or the 45s backstop fires, so four of them is roughly three minutes of
// quiet, which is a reasonable interval to confirm the microphone is still open
// without narrating every pause in a conversation.
const quietTurnsBetweenReassurance = 4

// micSettleDelay is the pause between the ready chime finishing and the
// recorder arming. Even after the last sample is played the speaker cone, the
// desk, and the mic's AGC keep ringing for a few tens of milliseconds, and that
// tail is loud enough to trip sox's `silence 1 0.1 1%` gate. 150ms is
// imperceptible as a gap and clears the measured decay of the 880Hz ping.
const micSettleDelay = 150 * time.Millisecond

// silenceIsNotAFailure reports whether an error means "nothing was said".
//
// The distinction this function exists to make: a quiet room and a broken
// microphone produce different errors, and only one of them is a problem.
func silenceIsNotAFailure(err error) bool {
	return errors.Is(err, speech.ErrNoSpeech) || errors.Is(err, speech.ErrEmptyTranscript)
}

// voiceTurnWithRetry runs voice turns until one produces something to act on.
//
// **Silence is not a failure and has no budget.** It used to have one: three
// quiet turns and the shell printed "voice unavailable" and dropped to a typed
// prompt. That is wrong twice over. Staying quiet is the ordinary state of a
// person who is not talking — a listening assistant that gives up after two
// minutes of it is not listening — and leaving live mode is a decision that
// belongs to the user, who has two ways to make it ("manual mode", or
// /blackbox off) and did not use either.
//
// Looping forever is safe rather than merely intended: a silent capture blocks
// in sox's leading-silence gate until someone speaks or the 45s backstop fires,
// so this waits, it does not spin.
//
// Real errors — a missing recorder, a provider chain that collapsed — still
// return. Those are not silence, they are a broken microphone, and the caller
// offers one typed turn WITHOUT leaving voice mode so a dead mic cannot strand
// anyone.
func voiceTurnWithRetry(ctx context.Context) (input.InputEvent, error) {
	quiet := 0
	for {
		ev, err := voiceTurn(ctx)
		if err == nil {
			return ev, nil
		}
		if errors.Is(err, errVoiceHandled) || errors.Is(err, errModeChanged) {
			// The utterance was served (a command ran, or a kill phrase fired).
			// Re-recording here would ask the user to repeat something that
			// already worked.
			return ev, err
		}
		if silenceIsNotAFailure(err) {
			quiet++
			if quiet%quietTurnsBetweenReassurance == 0 {
				// Worded for both readings, because this cannot tell them
				// apart: nobody spoke, or somebody spoke and was not heard.
				uiIdle("still listening",
					"nothing heard yet · /mictest checks the microphone · "+
						"say \"manual mode\" for the keyboard")
			}
			// voiceTurn plays the ready cue itself — no extra beep here.
			continue
		}
		return ev, err
	}
}

// batchVoiceTurn is the original record-whole-clip→transcribe path (Phase 2).
//
// The turn ends when the SPEAKER stops, not when a timer does. It used to cap
// capture at 12s, which meant a sentence longer than that was cut mid-word,
// transcribed, answered, and its remainder arrived as a separate turn with a
// separate answer — one thought, two half-conversations. The cap is now a
// backstop against a stuck microphone and sits far outside any real utterance.
func batchVoiceTurn(parent context.Context) (input.InputEvent, error) {
	// The context must outlast the capture backstop, or IT becomes the cutter
	// and we are back to a stopwatch ending turns. Capture stops on silence
	// long before either fires in any normal turn.
	//
	// A CHILD of the caller's context, not a fresh Background: the caller may
	// cancel this turn because a key was pressed, and a backstop rooted in
	// Background would keep the recorder running until it expired.
	ctx, cancel := context.WithTimeout(parent,
		speech.ConversationalMaxDuration+15*time.Second)
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
	defer cancel()

	viz := ux.NewVoiceViz()
	viz.Start(ux.VizListening)
	clip, err := speech.RecordClip(ctx, speech.CaptureOptions{
		MaxDuration: speech.ConversationalMaxDuration,
	})
	if err != nil {
		viz.Stop()
		if errors.Is(err, speech.ErrNoSpeech) {
			return input.InputEvent{}, speech.ErrNoSpeech
		}
		return input.InputEvent{}, fmt.Errorf("capture: %w", err)
	}

	// Amplitude AND duration gate BEFORE the STT round-trip: a dead mic, a
	// silent room, or a sub-0.3s transient (the ready chime's tail, a key
	// click) must not burn a cloud transcription — that is exactly how a clip
	// of Helix's own chime came back as the word "you" and ran as a real turn.
	// Re-arm the mic instead.
	if !speech.UsableSpeech(clip) {
		viz.Stop()
		return input.InputEvent{}, speech.ErrNoSpeech
	}

	// A cancelled capture must not be transcribed, and this is the one place
	// that can tell. RecordClip returns a partial clip with NO ERROR when a
	// killed recorder had already flushed a header (see capture.go), so the
	// error is not the signal — the context is. Without this, every keystroke
	// during a conversation would burn a cloud STT call on half a sentence,
	// and that half sentence could come back as a stop phrase.
	if ctx.Err() != nil {
		viz.Stop()
		return input.InputEvent{}, ctx.Err()
	}

	// Presence, recorded HERE rather than on a successful transcript. A clip
	// that cleared the gate is a person in the room, whatever the provider
	// then makes of it — so being MISHEARD cannot stand you down, which it
	// would if the clock only reset on text that parsed.
	noteVoiceActivity(time.Now())
	viz.SetState(ux.VizTranscribing)

	// Transcription gets its own budget — a capture that used most of the
	// 25s window must not starve the STT round trip.
	tctx, tcancel := context.WithTimeout(context.Background(), 60*time.Second)
	tunreg := utils.RegisterOperation(tcancel)
	defer tunreg()
	defer tcancel()
	transcript, err := speech.Transcribe(tctx, clip)
	viz.Stop()
	// The clip duration used to get its own row here, immediately above the
	// transcript echo — two lines per turn where the second one already had a
	// metadata field with room in it. It is now folded into that field by
	// finishVoiceTranscript, which receives the clip and can measure it.
	if err != nil {
		return input.InputEvent{}, fmt.Errorf("transcribe: %w", err)
	}
	return finishVoiceTranscript(strings.TrimSpace(transcript.Text), transcript, clip)
}

// joinField appends one "  ·  "-separated field, skipping the separator when
// there is nothing to separate from.
func joinField(base, add string) string {
	if base == "" {
		return add
	}
	return base + "  ·  " + add
}

// errStreamDial marks a failure to open the streaming connection (distinct
// from a stream that started but delivered no final).
var errStreamDial = errors.New("stream dial failed")

// streamingVoiceTurn streams 300ms chunks to the provider, echoes interim
// partials, and returns on the utterance-final transcript. A 3s silence gap
// finalizes the turn so a silent mic doesn't hang the shell.
//
// The chunk scanner arms as soon as this is entered, so the ready chime must
// already be finished and settled: voiceTurn owns that ordering
// (PlayAlertSync + micSettleDelay) for this path and the batch one alike.
func streamingVoiceTurn(parent context.Context, s speech.StreamingSTTProvider) (input.InputEvent, error) {
	// Same rule as the batch path: the deadline is a backstop, never the thing
	// that ends a turn. At 15s a speaker who ran long had the stream closed
	// under them mid-sentence, and the remainder became a separate turn with a
	// separate answer. The provider's utterance-final and the silence gap below
	// do the endpointing.
	// A child of the caller's context — see batchVoiceTurn for why.
	ctx, cancel := context.WithTimeout(parent,
		speech.ConversationalMaxDuration+15*time.Second)
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
	defer cancel()

	chunkMs := cfg.Speech.STT.StreamChunkMs
	if chunkMs <= 0 {
		chunkMs = 300
	}
	// P12.4: a live HUD driven by the real microphone. This path reads the
	// capture stream in chunks, so each one can be metered before it is sent
	// upstream — the batch path cannot do this (sox writes a whole file with
	// no incremental readback) and keeps the synthetic animation.
	viz := ux.NewVoiceViz()
	viz.Start(ux.VizListening)
	defer viz.Stop()

	scanner := speech.NewChunkScanner(time.Duration(chunkMs)*time.Millisecond, 16000)
	chunks := make(chan speech.AudioFormat)
	go func() {
		defer close(chunks)
		defer func() { _ = scanner.Close() }()
		for {
			cctx, ccancel := context.WithTimeout(ctx, 3*time.Second)
			clip, err := scanner.NextChunk(cctx)
			ccancel()
			if err != nil {
				return
			}
			// Meter before forwarding: the waveform now tracks the actual mic
			// instead of animating regardless of whether anything is heard.
			viz.SetLevel(speech.ClipLevel(clip))
			select {
			case chunks <- clip:
			case <-ctx.Done():
				return
			}
		}
	}()

	stream, err := s.Stream(ctx, chunks)
	if err != nil {
		return input.InputEvent{}, fmt.Errorf("%w: %v", errStreamDial, err)
	}

	idle := time.NewTimer(3 * time.Second)
	defer idle.Stop()

	var last string
	heard := false

	// The HUD and the interim-transcript line share one terminal row, so the
	// waveform hands the line over as soon as real words arrive: before that
	// the meter answers "is the mic live?", after it the text is strictly more
	// informative.
	yieldLine := func() {
		if viz.Running() {
			viz.Stop()
		}
	}

	finalize := func() (input.InputEvent, error) {
		if !heard {
			// Nothing was transcribed. Before blaming the microphone, ask the
			// provider whether IT failed: a streaming adapter whose wire
			// format is partly inferred (the OpenAI realtime one) opens a
			// socket, meters a live mic and transcribes nothing when a guess
			// is wrong — indistinguishable from a quiet room from here. Wrap
			// it as a dial failure so the turn falls back to batch, which is
			// the path that actually works.
			if r, ok := s.(speech.StreamFaultReporter); ok {
				if fault := r.StreamFault(); fault != nil {
					return input.InputEvent{}, fmt.Errorf("%w: %v", errStreamDial, fault)
				}
			}
			return input.InputEvent{}, speech.ErrNoSpeech
		}
		if last == "" {
			return input.InputEvent{}, speech.ErrEmptyTranscript
		}
		fmt.Print("\r\x1b[2K")
		return finishVoiceTranscript(last,
			speech.Transcript{Text: last, Provider: s.Name(), IsFinal: true},
			speech.AudioFormat{})
	}

	for {
		select {
		case t, ok := <-stream:
			if !ok {
				return finalize()
			}
			text := strings.TrimSpace(t.Text)
			if text != "" {
				// Only actual words count as "heard" — empty frames from a
				// silent mic must report ErrNoSpeech (retry prompt says
				// "speak again"), not ErrEmptyTranscript.
				heard = true
				// Streaming turns hold no clip to measure, so the words
				// themselves are the evidence of presence.
				noteVoiceActivity(time.Now())
			}
			if !t.IsFinal {
				if text != "" && text != last {
					yieldLine()
					last = text
					fmt.Printf("\r[hearing] %s", text)
				}
				resetTimer(idle, 3*time.Second)
				continue
			}
			if text == "" {
				resetTimer(idle, 3*time.Second)
				continue
			}
			yieldLine()
			fmt.Print("\r\x1b[2K")
			// Streaming: the utterance arrived as chunks, so there is no single
			// clip to retain. The turn contributes text-only context.
			return finishVoiceTranscript(text, t, speech.AudioFormat{})
		case <-idle.C:
			return finalize()
		case <-ctx.Done():
			return finalize()
		}
	}
}

// resetTimer restarts a time.Timer, draining a pending fire if needed.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// finishVoiceTranscript applies kill-switch checks and stamps the input event
// shared by the batch and streaming turn paths.
// finishVoiceTranscript funnels every finalized transcript.
//
// audio is the clip the transcript came from, retained as conversational context
// for a context-conditioned voice. It may be the zero value: the streaming path
// consumes the utterance as chunks and never holds one contiguous clip, so those
// turns contribute text-only context, which CSM still conditions on.
func finishVoiceTranscript(text string, transcript speech.Transcript, audio speech.AudioFormat) (input.InputEvent, error) {
	// One line, whatever produced it.
	//
	// Registry.Transcribe already normalises the batch path, but the streaming
	// path assembles its own transcript from chunks and never goes through it.
	// Both end here, which makes this the one place that covers every route —
	// and the text below is not just echoed, it is SUBMITTED, so a stray
	// newline would arrive at the classifier as a second line of input.
	text = speech.OneLine(text)
	if text == "" {
		return input.InputEvent{}, speech.ErrEmptyTranscript
	}
	transcript.Text = text

	// "  ·  " between the words and every field after them, not whitespace.
	//
	// Whitespace alone left the label looking like part of what was said:
	// `❯ reboot.   whisper-local` was reported as the transcript being
	// contaminated with a provider name. It never was — but a separator that
	// can be mistaken for a pause in speech is the wrong separator, and this
	// is the one the rest of Helix already uses to mean "different field".
	//
	// Everything known about the turn lands on this ONE line. The clip length
	// used to print on a row of its own directly above, which cost a line per
	// turn to say something the eye reads as part of the same fact; the field
	// separator that already existed here is what made merging free. The
	// streaming path holds no single clip, so it contributes no duration and
	// the field is simply absent.
	meta := transcript.Provider
	if d := speech.ClipDuration(audio); d > 0 {
		meta = joinField(meta, fmt.Sprintf("heard %.1fs", d))
	}
	if transcript.Confidence > 0 {
		meta = joinField(meta, fmt.Sprintf("confidence %.2f", transcript.Confidence))
	}
	line := "  " + shell.Fg(shell.HexSecondary, "❯ ") + shell.Fg(shell.HexText, text)
	if meta != "" {
		line += shell.Muted("  ·  " + meta)
	}
	fmt.Println(line)

	// Mode phrases (ADR-005 wake controls): recognized before dispatch, because
	// they END the turn rather than being served by it. A "manual mode" that
	// fell through to the planner would be answered with a sentence about
	// manual mode.
	if target, mustConfirm, ok := matchModePhrase(text); ok {
		// A pending question is only answered by the mode it asked about.
		if modePhrasePending.active && modePhrasePending.target != target {
			modePhrasePending.active = false
		}
		if mustConfirm && !modePhrasePending.active {
			// A conversational closer. Real wording, but it also lands as the
			// tail of an ordinary request, so it is always worth one question.
			logHeard(text, transcript.Provider, transcript.Confidence, journal.OutcomeKillPhrase)
			modePhrasePending.active, modePhrasePending.target = true, target
			speakDirect(modePhraseQuestion(target) + " Say it again and I will.")
			fmt.Println(shell.Step(shell.StateWarn, "just checking",
				"that sounded like you were finished — say it again to confirm"))
			return input.InputEvent{}, errVoiceHandled
		}
		if !modePhraseTrusted(transcript, audio) {
			// Weak evidence for a decision that ends the conversation: ask once
			// instead of acting or ignoring. See modePhraseTrusted.
			logHeard(text, transcript.Provider, transcript.Confidence, journal.OutcomeKillPhrase)
			modePhrasePending.active, modePhrasePending.target = true, target
			speakDirect(modePhraseQuestion(target) + " Say it again and I will.")
			fmt.Println(shell.Step(shell.StateWarn, "not sure",
				"heard a stop phrase in a clip too weak to trust — say it again to confirm"))
			return input.InputEvent{}, errVoiceHandled
		}
		modePhrasePending.active = false
		logHeard(text, transcript.Provider, transcript.Confidence, journal.OutcomeKillPhrase)
		// One door. It closes the camera and the companion as well, which is
		// what a safety valve that leaves either running has not actually done.
		setListenMode(target, causeSpoken)
		return input.InputEvent{}, errModeChanged
	}
	// Anything else clears a pending confirmation: the user moved on, so a
	// stop phrase heard two turns ago is not an answer to a question nobody
	// remembers being asked.
	modePhrasePending.active = false

	// Restart, recognized in the same place and for the same reason as the
	// kill phrases: it ENDS the turn rather than being served by it, so the
	// planner must never see it. A spoken "reboot" that fell through to the
	// planner would be answered with a sentence about rebooting instead of a
	// reboot — the same failure "manual mode" had before it was a kill phrase.
	//
	// Deliberately BEFORE dispatchVoiceCommand even though /reboot is VoiceOK
	// and would eventually route: the route form only matches the phrase at the
	// START of an utterance, and people say "okay, please reboot".
	if isVoiceRebootPhrase(text) {
		logHeard(text, transcript.Provider, transcript.Confidence, journal.OutcomeReboot)
		handleRebootSpoken()
		// errVoiceHandled, not errVoiceStopped: the turn is complete and the
		// loop takes the next iteration, where rebootRequested breaks it. Voice
		// mode stays ACTIVE on the way out, which is what makes the record say
		// "voice" and the restart come back listening.
		return input.InputEvent{}, errVoiceHandled
	}

	// Vision privacy kill switch (threat V4): "turn off your eyes" deactivates
	// the camera immediately WITHOUT leaving live mode.
	if isEyesOffPhrase(text) {
		logHeard(text, transcript.Provider, transcript.Confidence, journal.OutcomeEyesOff)
		setVisionEnabled(false)
		// errVoiceHandled, which is what this always MEANT: the camera closed,
		// the conversation continues. It used to return the same sentinel as a
		// kill phrase, so "the mode ended" and "the mode did not end" were one
		// value.
		return input.InputEvent{}, errVoiceHandled
	}

	// Spoken command routing (voice_commands.go). A transcript never contains a
	// "/", so without this the whole slash-command surface is unreachable by
	// voice — /status, /plan, /todo, /diff, /web and the rest. Only commands
	// marked VoiceOK are reachable, and a refusal is spoken.
	//
	// This runs after the kill switches (which must never be overridden) and
	// before the planner (so "what's on my list" reads the list instead of
	// becoming a shell plan).
	if dispatchVoiceCommand(text) {
		logHeard(text, transcript.Provider, transcript.Confidence, journal.OutcomeCommand)
		return input.InputEvent{}, errVoiceHandled
	}

	logHeard(text, transcript.Provider, transcript.Confidence, journal.OutcomePlanner)
	// The user's half of the context CSM conditions on. Recorded here because
	// this is the one funnel every finalized transcript passes through, and only
	// for turns that reach the planner — a kill phrase is not conversation.
	speech.RecordUserTurn(text, audio)
	return input.InputEvent{
		Text:    text,
		Channel: input.ChannelVoice,
		Meta: map[string]any{
			"stt_provider":   transcript.Provider,
			"stt_confidence": transcript.Confidence,
			"stt_language":   transcript.Language,
		},
	}, nil
}

// errVoiceHandled signals the utterance was served without reaching the
// planner, and the mode is UNCHANGED. The loop simply takes the next turn.
//
// It now covers three things that are the same thing from the loop's point of
// view: a spoken command, the eyes-off switch, and the "say it again" round
// after a stop phrase arrived on weak evidence. The last of those used to
// return speech.ErrNoSpeech, which was a lie — it made the silence-reassurance
// counter tick on a turn where someone had plainly spoken.
var errVoiceHandled = errors.New("voice command handled")

// errModeChanged signals that the listening mode changed under the caller, so
// it should re-dispatch on whatever is current now.
//
// This replaces errVoiceStopped, which meant TWO different things — "a kill
// phrase ended live mode" and "eyes were turned off, the mode continues" — and
// was compared with == at one call site and errors.Is one line below. No branch
// needs to know WHICH mode was entered: the REPL switches on currentMode() at
// the top of the next iteration.
var errModeChanged = errors.New("listening mode changed")

// errKeyboardPreempted signals that a key was pressed during a capture, so the
// turn belongs to the keyboard.
//
// Distinct from silence, and that distinction is load-bearing:
// voiceTurnWithRetry treats silence as "keep listening" and would re-open the
// recorder on top of the line the user is now typing.
var errKeyboardPreempted = errors.New("keyboard pre-empted the voice turn")

// Mode phrases. Three lists, because "close the microphone" and "stop talking
// to me for now" are different requests and used to be the same one.
//
// All matched as a SUFFIX of the utterance, not as the whole of it — see
// matchModePhrase.
var (
	// manualPhrases close the microphone and persist it. The deep exit.
	manualPhrases = []string{
		"switch to manual mode", "switch to manual", "go to manual mode",
		"go manual", "manual mode", "keyboard mode", "keyboard only",
		"i want to type", "i'll type", "let me type",
		"close the microphone", "close the mic",
		"turn off the microphone", "turn off the mic",
		"mute the microphone", "mute the mic", "mic off", "microphone off",
		"stop listening completely", "stop listening entirely",
		"blackbox off", "black box off",
	}

	// standbyPhrases end the conversation but keep listening for a wake.
	//
	// "stop listening" and "go to sleep" USED to live in the same list as
	// "manual mode" and did the same thing. They are the lighter request and
	// now say so: Helix stops taking turns, and a sound brings it back.
	standbyPhrases = []string{
		"you can turn off now", "you can turn off", "you can stop now",
		"you can stop listening", "you can rest", "you can relax",
		"go to sleep", "go back to sleep", "sleep now", "go to standby",
		"stand down", "stop listening", "stop listening for now",
		"pause listening", "stop voice", "take a break",
		"i'm done for now", "im done for now", "i'm done talking",
		"im done talking",
	}

	// softStandbyPhrases also mean standby, but ALWAYS ask first.
	//
	// These are how people actually close a conversation, so they have to
	// work — and they are also how people end an ordinary request ("...and
	// then deploy it, that's all"). Suffix matching cannot tell those apart,
	// and the cost of guessing wrong is a conversation that ends itself. So
	// they route through the confirmation round unconditionally, whatever the
	// clip's energy says. That is the difference between hearing you and
	// obeying a noise.
	softStandbyPhrases = []string{
		"that's all", "thats all", "that's all for now", "thats all for now",
		"that will be all", "that'll be all",
		"we're done", "we are done", "were done",
		"i'm done", "im done", "nothing else", "that's everything",
		"thats everything",
	}
)

// modePhrasePending remembers WHICH mode was asked for when a stop phrase
// arrived on evidence too weak to act on, and Helix asked again.
//
// Session state rather than a parameter because the two halves are different
// turns: the question is asked at the end of one and answered at the start of
// the next. Cleared by any other utterance, so it cannot be satisfied by a
// phrase heard minutes earlier.
//
// It carries the TARGET, not just a flag. As a bare bool, a "go to sleep" that
// needed confirming could be satisfied by a later "manual mode" and vice
// versa — Helix would have asked one question and acted on the answer to
// another.
var modePhrasePending struct {
	active bool
	target listenMode
}

// killPhraseMinRMSMultiple is how much louder than "audible" a session-ending
// phrase must be.
//
// A judgment, not a measurement, and small on purpose. The real safety net is
// the confirmation round, not this number: getting it slightly wrong costs one
// extra "say it again" rather than either a trapped user or a false exit, which
// is why it is 2 and not the 10 the barge-in probe uses. That probe wants to be
// deliberately deaf; this one only wants to tell a spoken sentence from a
// hallucination out of room noise.
const killPhraseMinRMSMultiple = 2.0

// modePhraseTrusted reports whether a stop phrase came with enough evidence to
// end the session on the spot.
//
// WHY THIS EXISTS. The kill phrase used to be acted on unconditionally, with
// the transcript's confidence printed on the line directly above the check and
// ignored by it. A live session showed the cost: three turns of room noise, the
// last transcribed as "Manual mode.", and live mode ended by itself — breaking
// the one promise the owner had asked for in writing. Removing the ungated
// capture that produced those clips made it rare; it did not make the valve
// robust, because any capture whose transcript happens to read "manual mode"
// still ends the session.
//
// Two signals, and the ORDER is deliberate:
//
//  1. A reported confidence below the Voice Risk Policy's gate is a refusal.
//     The provider is telling us it guessed.
//  2. Otherwise the CLIP's energy decides. whisper-local reports no confidence
//     at all — it was the provider in the session that failed — so a
//     confidence-only rule would have changed nothing for the case that
//     prompted this. Energy is the signal that discriminates a sentence from a
//     fan.
//
// It fails toward ASKING, never toward silence. A streaming turn holds no
// contiguous clip (documented on finishVoiceTranscript) and a mic-less path has
// no audio at all; in both cases there is nothing to measure, and refusing a
// valve because a measurement is missing would trap someone in live mode whose
// only other exit is Ctrl+C. So an unmeasurable clip is TRUSTED, and the
// confirmation round is what stands between noise and an unwanted exit
// everywhere else.
func modePhraseTrusted(transcript speech.Transcript, audio speech.AudioFormat) bool {
	if modePhrasePending.active {
		return true // this is the confirmation; the first one already asked
	}
	if transcript.Confidence > 0 &&
		transcript.Confidence < agent.DefaultVoicePolicy().MinTranscriptConfidence {
		return false
	}
	if len(audio.Bytes) == 0 {
		return true // nothing to measure — see the comment above
	}
	return speech.HasSpeech(audio, speech.SpeechRMSFloor*killPhraseMinRMSMultiple)
}

// matchModePhrase reports which listening mode an utterance asked for.
//
// Suffix matching, because people do not speak in bare commands. QA said
// "Excellent. Now switch to manual mode." and Helix — which required the whole
// transcript to equal one of the phrases exactly — sent it to the planner,
// which replied by asking what to switch to manual mode FOR. The literal
// "Manual mode." on the next turn worked, which is the whole complaint: the
// safety valve only opened for someone who already knew its exact wording.
//
// A suffix rather than a substring, deliberately. "How do I switch to manual
// mode?" is a question about the feature, not a request to use it, and the
// phrase lands mid-sentence there. Ending on it is what makes it an
// instruction.
//
// The longest match across all three lists wins, so "stop listening
// completely" closes the microphone rather than matching the "stop listening"
// that means standby.
//
// Args: text: the raw transcript.
// Returns: the mode asked for, whether it must be confirmed regardless of
// evidence, and whether anything matched at all.
// Complexity: O(len(text) × number of phrases).
func matchModePhrase(text string) (target listenMode, mustConfirm bool, ok bool) {
	bare := strings.TrimRight(strings.ToLower(strings.TrimSpace(text)), " .!?,")

	// TWO candidates, not one. Courtesy trimming has to happen for "turn off
	// the microphone please" to work, but some phrases END in a word the
	// trimmer removes — "you can stop now" would become "you can stop" and
	// stop matching. Trying both forms means no entry in the lists above can
	// be broken by the trimmer, which is the kind of coupling that would
	// otherwise be discovered by a user rather than a test.
	candidates := []string{bare}
	if trimmed := trimTrailingCourtesy(bare); trimmed != bare && trimmed != "" {
		candidates = append(candidates, trimmed)
	}

	best := -1
	for _, c := range []struct {
		phrases []string
		target  listenMode
		confirm bool
	}{
		{manualPhrases, modeManual, false},
		{standbyPhrases, modeStandby, false},
		{softStandbyPhrases, modeStandby, true},
	} {
		for _, p := range c.phrases {
			for _, t := range candidates {
				if t != p && !strings.HasSuffix(t, " "+p) {
					continue
				}
				if len(p) > best {
					best, target, mustConfirm, ok = len(p), c.target, c.confirm, true
				}
				break
			}
		}
	}
	return target, mustConfirm, ok
}

// modeCourtesy are trailing words that carry no instruction.
//
// Suffix matching needs the phrase at the END of the utterance, and people do
// not stop talking there: "turn off the microphone please" and "manual mode
// thanks" are the same request as the bare phrase, and a valve that only opens
// for someone who omits the politeness is the same defect suffix matching was
// introduced to fix. Trailing only — "please stop listening" keeps its lead-in,
// because the phrase is still the tail.
var modeCourtesy = []string{"please", "thanks", "thank you", "now", "ok", "okay", "alright"}

// trimTrailingCourtesy strips trailing punctuation and politeness, repeatedly,
// so "manual mode, please. thanks!" reduces to "manual mode".
func trimTrailingCourtesy(t string) string {
	for {
		t = strings.TrimRight(strings.TrimSpace(t), " .!?,")
		cut := false
		for _, w := range modeCourtesy {
			if t == w {
				return "" // nothing but courtesy is not an instruction
			}
			if strings.HasSuffix(t, " "+w) {
				t, cut = t[:len(t)-len(w)-1], true
				break
			}
		}
		if !cut {
			return t
		}
	}
}

// modePhraseQuestion asks for the confirmation, naming what would happen.
func modePhraseQuestion(target listenMode) string {
	if target == modeManual {
		return "Should I close the microphone?"
	}
	return "Should I stop listening for now?"
}

// WAKE-ONLY LISTENING HAS NO DEADLINE, and that rule outlived the code that
// used to hold it here.
//
// The original defect: a 60-second window read ADR-005 §5 — "wake-word-
// triggered sessions have a hard 60s inactivity lockout back to wake-only
// listening" — as a deadline ON wake-only listening, after which the shell fell
// through to OPEN capture. That is the rule inverted. §5 exists so that an idle
// session needs the wake word again; the implementation made an idle session
// stop needing it.
//
// What it cost, from a real session on 2026-09-09: sixty quiet seconds after
// going live, the gate removed itself and the microphone opened unbidden. Fan
// noise became a 0.5s clip, whisper turned that into "May he leave.", and the
// shell answered a turn nobody took. The next one ran `man motor`. The one
// after that transcribed as "Manual mode." — the stop phrase — and Helix left
// live mode on its own, which is precisely the thing the owner had just asked
// it never to do.
//
// WHERE THE RULE LIVES NOW. The between-turns hold this paragraph used to
// describe is gone for a different reason: a conversation no longer re-waits
// for a wake word between every turn, because having to re-wake for each
// answer is what made hands-free unusable. The only wake-only hold left is
// armedIdleWait (wake_always.go), and it is unbounded — which is what
// TestWakeHoldHasNoDeadline now asserts against.
//
// The AWAKE stand-down is NOT a counterexample and must not be mistaken for
// one. It expires into LESS listening, never into transcription: a
// conversation nobody is speaking in falls back to wake-only. The defect above
// ran the other way. That is also why it is checked at the top of a turn as a
// pure function of the clock rather than as a context deadline inside the
// listening path — see shouldStandDown.

// newWakeService builds the wake detector, scanner and service from config.
//
// Extracted because there are now TWO callers — the between-turns hold inside
// live mode, and the always-listen wait at an idle keyboard prompt — and a
// second copy of this would be a second place for the engine choice, the chunk
// length, the ambient tee and the cooldown to disagree. The repo has paid for
// that shape three times (speech endpoints, the metrics provider list, a linter
// version), so the second caller arrives by extracting rather than by copying.
func newWakeService() (wakeword.Service, error) {
	preset := wakeword.Preset(cfg.Speech.WakeWord.SensitivityPreset)
	var detector wakeword.Detector
	switch cfg.Speech.WakeWord.Engine {
	case "sidecar":
		detector = wakeword.NewSidecarDetector(cfg.Speech.WakeWord.SidecarURL,
			cfg.Speech.WakeWord.Phrase, preset)
	default: // "energy" — the everywhere-works default (ADR-002 honesty)
		detector = wakeword.NewEnergyDetector(preset)
	}

	chunkMs := cfg.Speech.WakeWord.ChunkMs
	if chunkMs <= 0 {
		chunkMs = 1500
	}
	hooks := newWakeScanHooks()

	// Phase 6: ambient awareness shares the wake capture stream when opted in.
	scanner := wakeword.Scanner(wakeword.NewSoXScanner(
		time.Duration(chunkMs)*time.Millisecond, 16000,
		wakeword.OnCaptureNotice(hooks.Notice)))
	if cfg.Ambient.Enabled {
		scanner = ambient.Tee(scanner, interactiveAmbientMonitor())
	}

	return wakeword.NewService(
		scanner,
		detector,
		wakeword.Config{
			Phrase:   cfg.Speech.WakeWord.Phrase,
			Cooldown: time.Duration(cfg.Speech.WakeWord.CooldownS) * time.Second,
			OnError:  hooks.OnError,
			OnScan:   hooks.OnScan,
		})
}

// interactiveAmbientMonitor builds the monitor for the interactive wake loop.
func interactiveAmbientMonitor() *ambient.ChunkMonitor {
	enabled := map[ambient.Category]bool{}
	for name, on := range cfg.Ambient.Categories {
		enabled[ambient.Category(name)] = on
	}
	svc := ambient.NewServiceFromOptions(cfg.Ambient.Sensitivity,
		ambient.ResponseModeFromString(cfg.Ambient.ResponseMode), enabled)
	mon := ambient.NewChunkMonitor(svc)
	mon.OnSpeak = func(text string) {
		if agentCore != nil && agentCore.OnSpeak != nil {
			agentCore.OnSpeak(text)
		}
	}
	mon.OnLog = func(ev ambient.Event) {
		appendAmbientEvent(ev)
	}
	return mon
}

// handleMicTest implements /mictest — a 3-second self-test that answers the
// question "is the AI actually hearing me?": it reports the recorder, how
// much audio was captured, the measured level (RMS + dBFS), and whether that
// clears the speech gate the voice loop uses. Wrong input device or a muted
// system mic shows up here immediately.
func handleMicTest() {
	recorder, err := speech.DetectRecorder()
	if err != nil {
		uiFail("microphone", err.Error())
		return
	}
	fmt.Printf("Mic test (recorder: %s) — speak now for up to 3s...\n", recorder)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clip, err := speech.RecordClip(ctx, speech.CaptureOptions{MaxDuration: 3 * time.Second})
	if err != nil {
		uiFail("capture", err.Error())
		return
	}

	rms := speech.ClipRMS(clip)
	dB := 0.0
	if rms > 0 {
		dB = 20 * math.Log10(rms)
	}
	status := "QUIET — check your input device / system mic level"
	if speech.HasSpeech(clip, 0) {
		status = "speech detected ✓"
	}
	// Five decimals, not three. The levels that matter here are the ones a
	// built-in microphone actually produces — a quiet room measures ~0.0011
	// and speech ~0.012 on the machine this was calibrated against — and at
	// %.3f the room and a dead mic both print "0.001". This readout is the
	// number a user is asked to paste when hands-free wake is not firing, so
	// it has to resolve the range wake decisions are made in.
	fmt.Printf("Captured %.1fs — level %.5f (%.0f dBFS) — %s\n",
		speech.ClipDuration(clip), rms, dB, status)
	uiDetail("If this reads QUIET, /blackbox status confirms the STT chain — then check " +
		"the OS sound input settings for the active microphone.")
}

// appendMetricsRecord appends one JSON line to ~/.helix/metrics/<name>.jsonl
// (0600, local only, never transmitted). All §10 "measured by metrics log"
// numbers land here so the release run can be audited from one directory.
//
// The writing itself now lives in internal/metrics, which also reads these
// files for `/blackbox stats`. That is deliberate: for three years of commits
// the field names existed only at the write site, so a reader added anywhere
// else would have been free to spell them differently — the same
// dropped-at-the-boundary bug that cost the speech config its Endpoints three
// separate times.
func appendMetricsRecord(name string, fields map[string]any) {
	metrics.Append(name, fields)
}

// appendAmbientEvent records one ambient event to the local metrics journal
// (~/.helix/metrics/ambient.jsonl, local only, never transmitted).
func appendAmbientEvent(ev ambient.Event) {
	appendMetricsRecord(metrics.FileAmbient, map[string]any{
		"ts":        time.Now().UTC().Format(time.RFC3339),
		"category":  string(ev.Category),
		"intensity": ev.Intensity,
	})
}

// logWakeEvent appends one wake event to the local metrics journal
// (~/.helix/metrics/wake.jsonl, local only, never transmitted).
func logWakeEvent(ev wakeword.WakeEvent) {
	appendMetricsRecord(metrics.FileWake, map[string]any{
		"ts":     ev.DetectedAt.UTC().Format(time.RFC3339),
		"score":  ev.Score,
		"phrase": ev.Phrase,
	})
}

// logVoiceLatency records an E2E voice-metrics sample (wake→execution start,
// §10 target ≤6s local). meta carries the STT provider/confidence for context.
func logVoiceLatency(metric string, d time.Duration, meta map[string]any) {
	fields := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339),
		"metric":  metric,
		"latency": d.Milliseconds(),
	}
	for _, k := range []string{"stt_provider", "stt_confidence"} {
		if v, ok := meta[k]; ok {
			fields[k] = v
		}
	}
	appendMetricsRecord(metrics.FileVoice, fields)
}

// logSpeechLatency records TTS time-to-first-audio (§10 target ≤800ms cloud,
// ≤1.5s local).
//
// This was the one §10 number with a hard millisecond budget that never reached
// the metrics directory: it lived in an atomic in internal/speech, so
// /blackbox status could show the LAST value and it vanished on exit. A release
// run that has to audit "all §10 numbers from one directory" could not audit
// this one at all.
//
// Called after speech, and only when a measurement exists — a spoken reply with
// TTS disabled records nothing rather than a zero that would drag the p50 down.
func logSpeechLatency() {
	ms := speech.LastSynthesizeLatencyMs()
	if ms <= 0 {
		return
	}
	appendMetricsRecord(metrics.FileSpeech, map[string]any{
		"ts":       time.Now().UTC().Format(time.RFC3339),
		"metric":   metrics.MetricFirstAudio,
		"latency":  ms,
		"provider": activeTTSProvider(),
		"streamed": speech.LastSpeechStreamed(),
	})
}

// activeTTSProvider names the provider that ACTUALLY SPOKE, so a recorded
// latency is judged against the cloud or local column of §10 rather than an
// assumed one.
//
// It used to return the head of the chain, which is the assumption its own
// comment said it existed to avoid. The two differ in exactly one case, and it
// is the case that matters: a failover. With `piper-local` primary and `openai`
// behind it, a 900 ms cloud synthesis was filed as piper-local and graded
// against the 1500 ms LOCAL budget — a miss of the 800 ms cloud target recorded
// as "meets target", in a table that gates a release. The registry already
// knows who answered (ChainHealth.Used, kept precisely so callers need not
// probe), so nothing had to be measured to fix this; it had to be asked.
//
// The head remains the fallback answer for the case where it is the only thing
// known: a chain that has not run yet, which is what /blackbox status renders
// before the first spoken reply.
func activeTTSProvider() string {
	reg := speech.Default()
	if reg == nil {
		return ""
	}
	if h := reg.LastTTSHealth(); h.Attempted && h.OK && h.Used != "" {
		return h.Used
	}
	if chain := reg.TTSChain(); len(chain) > 0 {
		return chain[0]
	}
	return ""
}

// logVisionLatency records a frame-to-insight sample (§10 target ≤5s
// best-effort on llava). provider is the vision LLM that answered.
func logVisionLatency(metric string, d time.Duration, provider string) {
	appendMetricsRecord(metrics.FileVision, map[string]any{
		"ts":       time.Now().UTC().Format(time.RFC3339),
		"metric":   metric,
		"latency":  d.Milliseconds(),
		"provider": provider,
	})
}
