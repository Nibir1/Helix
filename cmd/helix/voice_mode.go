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
	voiceModeActive bool
	voicePrompter   *VoicePrompter
	ttyPrompter     commands.Prompter
)

// initVoiceMode wires prompters and restores the persisted mode. Called once
// from main after speech.Init and agent construction.
func initVoiceMode() {
	ttyPrompter = commands.ActivePrompter()
	voicePrompter = NewVoicePrompter()

	if cfg.UserPrefs.VoiceMode {
		// Refuse to strand the user in voice mode without a recorder.
		if _, err := speech.DetectRecorder(); err == nil {
			enterVoiceMode(false)
			// A restored session is live mode too. Without this, restarting
			// with voice persisted gave you the microphone but no companion —
			// the same mode reached by a different door, behaving differently.
			startCompanion()
		} else {
			uiWarn("voice mode skipped at startup", err.Error())
			cfg.UserPrefs.VoiceMode = false
			_ = cfg.SavePreferences()
		}
	}
}

func enterVoiceMode(persist bool) {
	voiceModeActive = true
	commands.SetPrompter(voicePrompter)

	// Conversational context is scoped to the mode: it only makes sense while a
	// conversation is happening, and scoping it here is what makes "leaving live
	// mode drops the retained audio" true rather than aspirational.
	speech.EnableConversationContext(cfg.Speech.TTS.ContextTurns, cfg.Speech.TTS.ContextMaxBytes)

	// Scoped to the mode like context is: the probe only makes sense while a
	// conversation is happening, and it must not keep sampling the microphone
	// after /blackbox off.
	speech.EnableBargeIn(cfg.Speech.TTS.BargeIn)
	if persist {
		cfg.UserPrefs.VoiceMode = true
		_ = cfg.SavePreferences()
	}
	audio.PlayAlert()
	printLiveBanner()
}

// printLiveBanner is the moment Helix wakes up, and it should look like it.
//
// It is also the only place some of this is ever said: which senses just came
// online, and how to get back out. The old single cyan line carried the exit
// instruction and nothing else, so a user could not tell from the screen
// whether the camera had opened.
func printLiveBanner() {
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

	for _, line := range voiceModeWakeNotes(cfg.Speech.WakeWord.Enabled, cfg.Speech.WakeWord.Engine) {
		fmt.Println(shell.PanelLine(shell.Muted(line)))
	}
	fmt.Println(shell.PanelEnd())
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
//
// Returns: the extra banner lines (nil when wake is off).
// Complexity: O(1).
func voiceModeWakeNotes(wakeEnabled bool, engine string) []string {
	if !wakeEnabled {
		return nil
	}
	lines := []string{
		"Wake word is on, but it gates the gaps BETWEEN turns — this first turn starts now,",
		"with no wake needed.",
	}
	if engine != "sidecar" {
		lines = append(lines,
			fmt.Sprintf("Engine %q wakes on any speech, not on a phrase (/blackbox status).",
				engineOrDefault(engine)))
	}
	return lines
}

func exitVoiceMode(persist bool) {
	// Leaving voice mode while Helix is mid-sentence should stop the sentence.
	// Without this, leaving live mode returned the prompt to the keyboard while the
	// previous reply kept talking over it.
	speech.StopSpeaking()

	// Drop retained conversation audio with the mode. Nothing here was ever
	// written to disk, so this is the only place it needs to be released.
	speech.EnableConversationContext(0, 0)
	speech.EnableBargeIn(false)

	voiceModeActive = false
	if ttyPrompter != nil {
		commands.SetPrompter(ttyPrompter)
	}
	if persist {
		cfg.UserPrefs.VoiceMode = false
		_ = cfg.SavePreferences()
	}
	fmt.Println("  " + shell.Fg(shell.HexMuted, "○ ") +
		shell.Fg(shell.HexText, "keyboard") +
		shell.Muted("  ·  /blackbox on goes live again"))
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
func voiceTurn() (input.InputEvent, error) {
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
		ev, err := streamingVoiceTurn(s)
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
	return batchVoiceTurn()
}

// maxVoiceRetries is how many times the mic re-arms after a silent/empty turn
// before falling back to typed input. Never zero — the shell must not brick.
const maxVoiceRetries = 3

// micSettleDelay is the pause between the ready chime finishing and the
// recorder arming. Even after the last sample is played the speaker cone, the
// desk, and the mic's AGC keep ringing for a few tens of milliseconds, and that
// tail is loud enough to trip sox's `silence 1 0.1 1%` gate. 150ms is
// imperceptible as a gap and clears the measured decay of the 880Hz ping.
const micSettleDelay = 150 * time.Millisecond

// voiceTurnWithRetry runs one voice turn, re-arming the mic on silence or an
// empty transcript with a gentle prompt instead of silently switching to
// typing. Real errors (provider down, capture failure) still degrade to the
// typed fallback so a broken mic never strands the user.
func voiceTurnWithRetry() (input.InputEvent, error) {
	for attempt := 0; ; attempt++ {
		ev, err := voiceTurn()
		if err == nil {
			return ev, nil
		}
		if errors.Is(err, errVoiceHandled) || errors.Is(err, errVoiceStopped) {
			// The utterance was served (a command ran, or a kill phrase fired).
			// Re-recording here would ask the user to repeat something that
			// already worked.
			return ev, err
		}
		if errors.Is(err, speech.ErrNoSpeech) || errors.Is(err, speech.ErrEmptyTranscript) {
			if attempt >= maxVoiceRetries {
				return ev, err
			}
			uiWarn("not caught", fmt.Sprintf("please speak again (attempt %d/%d)",
				attempt+1, maxVoiceRetries))
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
func batchVoiceTurn() (input.InputEvent, error) {
	// The context must outlast the capture backstop, or IT becomes the cutter
	// and we are back to a stopwatch ending turns. Capture stops on silence
	// long before either fires in any normal turn.
	ctx, cancel := context.WithTimeout(context.Background(),
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
	viz.SetState(ux.VizTranscribing)

	// Transcription gets its own budget — a capture that used most of the
	// 25s window must not starve the STT round trip.
	tctx, tcancel := context.WithTimeout(context.Background(), 60*time.Second)
	tunreg := utils.RegisterOperation(tcancel)
	defer tunreg()
	defer tcancel()
	transcript, err := speech.Transcribe(tctx, clip)
	viz.Stop()
	if d := speech.ClipDuration(clip); d > 0 && err == nil {
		// A turn marker, not a stat line. "captured 0.8s" floating at column
		// zero between replies read like debug output that had escaped.
		fmt.Println(shell.Muted(fmt.Sprintf("  ◟ heard %.1fs", d)))
	}
	if err != nil {
		return input.InputEvent{}, fmt.Errorf("transcribe: %w", err)
	}
	return finishVoiceTranscript(strings.TrimSpace(transcript.Text), transcript, clip)
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
func streamingVoiceTurn(s speech.StreamingSTTProvider) (input.InputEvent, error) {
	// Same rule as the batch path: the deadline is a backstop, never the thing
	// that ends a turn. At 15s a speaker who ran long had the stream closed
	// under them mid-sentence, and the remainder became a separate turn with a
	// separate answer. The provider's utterance-final and the silence gap below
	// do the endpointing.
	ctx, cancel := context.WithTimeout(context.Background(),
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

	conf := fmt.Sprintf(", confidence %.2f", transcript.Confidence)
	if transcript.Confidence <= 0 {
		conf = ""
	}
	// "  ·  " between the words and the provider, not three spaces.
	//
	// Whitespace alone left the label looking like part of what was said:
	// `❯ reboot.   whisper-local` was reported as the transcript being
	// contaminated with a provider name. It never was — but a separator that
	// can be mistaken for a pause in speech is the wrong separator, and this
	// is the one the rest of Helix already uses to mean "different field".
	fmt.Println("  " + shell.Fg(shell.HexSecondary, "❯ ") +
		shell.Fg(shell.HexText, text) + shell.Muted("  ·  "+transcript.Provider+conf))

	// Hands-free kill switches (ADR-005 wake controls): recognized before
	// dispatch.
	if isVoiceKillPhrase(text) {
		logHeard(text, transcript.Provider, transcript.Confidence, journal.OutcomeKillPhrase)
		// blackBoxOff, not exitVoiceMode: live mode opened the camera and the
		// companion loop too, and a safety valve that leaves either running has
		// not actually let go.
		blackBoxOff()
		return input.InputEvent{}, errVoiceStopped
	}

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
		return input.InputEvent{}, errVoiceStopped
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

// errVoiceHandled signals the utterance was served as a spoken COMMAND, so the
// turn is complete and the planner must not also see it. Distinct from
// errVoiceStopped: voice mode is still active and the loop simply takes the next
// turn.
var errVoiceHandled = errors.New("voice command handled")

// errVoiceStopped signals a kill phrase ended voice mode (the mode line
// already announced it; the main loop treats this as a quiet continue).
var errVoiceStopped = fmt.Errorf("voice stopped by kill phrase")

// killPhrases end live mode. Matched as a SUFFIX of the utterance, not as the
// whole of it — see isVoiceKillPhrase.
var killPhrases = []string{
	"switch to manual mode", "switch to manual", "go to manual mode",
	"manual mode", "stop listening", "go to sleep", "stop voice",
	"i want to type", "blackbox off", "black box off",
}

// isVoiceKillPhrase reports whether the user asked to go back to the keyboard.
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
// Args: text: the raw transcript.
// Returns: whether live mode should end.
// Complexity: O(len(text) × len(killPhrases)).
func isVoiceKillPhrase(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimRight(t, " .!?,")
	for _, p := range killPhrases {
		if t == p || strings.HasSuffix(t, " "+p) {
			return true
		}
	}
	return false
}

// wakeIdleWindow is the ADR-005 §5 lockout window: between turns NOTHING is
// transcribed; only wake scoring runs, for at most this long before the
// shell falls back to push-to-talk turns.
const wakeIdleWindow = 60 * time.Second

// wakeOutcome says how a stretch of wake listening ended.
//
// The distinction exists because wake gating used to lapse SILENTLY: the 60s
// window expiring and wake never being configured both returned a bare false,
// and the main loop reacted identically — back to open capture. From the user's
// seat that is the shell quietly abandoning a privacy control it announced, so
// the two cases now have to be told apart at the call site.
type wakeOutcome int

const (
	// wakeNotEngaged: wake listening never started (disabled, no recorder, no
	// speech engine). Nothing changed, so nothing is announced.
	wakeNotEngaged wakeOutcome = iota

	// wakeFired: a wake event arrived; the caller runs another voice turn.
	wakeFired

	// wakeWindowExpired: the ADR-005 §5 idle window elapsed with no wake.
	wakeWindowExpired

	// wakeScannerFailed: wake listening was configured and engaged but the
	// capture stream died (device yanked, recorder killed, service refused to
	// start). Gating is gone for the same reason expiry loses it, so it is
	// announced too — with its own cause.
	wakeScannerFailed

	// wakeCompanionSpoke ends the listen because Helix has something to say.
	// It is not a failure and must never be announced as a lapse: the scanner
	// is stopped deliberately so the remark is spoken with the microphone
	// closed (half-duplex), and the caller re-enters listening straight after.
	wakeCompanionSpoke
)

// wakeListenUntilArmed blocks in chunk-scanning wake detection. Returns the wake
// event and how the listen ended; only wakeFired carries a usable event. The
// DetectedAt timestamp feeds the §10 wake→execution latency metric.
func wakeListenUntilArmed() (wakeword.WakeEvent, wakeOutcome) {
	if speech.Default() == nil || !cfg.Speech.WakeWord.Enabled {
		return wakeword.WakeEvent{}, wakeNotEngaged
	}
	if _, err := speech.DetectRecorder(); err != nil {
		return wakeword.WakeEvent{}, wakeNotEngaged
	}

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
	// Phase 6: ambient awareness shares the wake capture stream when opted in.
	scanner := wakeword.Scanner(wakeword.NewSoXScanner(time.Duration(chunkMs)*time.Millisecond, 16000))
	if cfg.Ambient.Enabled {
		scanner = ambient.Tee(scanner, interactiveAmbientMonitor())
	}

	svc, err := wakeword.NewService(
		scanner,
		detector,
		wakeword.Config{
			Phrase:   cfg.Speech.WakeWord.Phrase,
			Cooldown: time.Duration(cfg.Speech.WakeWord.CooldownS) * time.Second,
			OnError:  func(error) {},
		})
	if err != nil {
		return wakeword.WakeEvent{}, wakeScannerFailed
	}

	ctx, cancel := context.WithTimeout(context.Background(), wakeIdleWindow)
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
	defer cancel()

	events, err := svc.Start(ctx)
	if err != nil {
		return wakeword.WakeEvent{}, wakeScannerFailed
	}
	defer func() { _ = svc.Stop() }()

	viz := ux.NewVoiceViz()
	viz.Start(ux.VizStandby)
	defer viz.Stop()
	select {
	case ev, ok := <-events:
		if !ok {
			// Scanner failure closed the channel — a zero-value event here is
			// NOT a wake. Fall back to push-to-talk instead of phantom-arming
			// the mic (the exact moment the mic is most likely broken).
			return wakeword.WakeEvent{}, wakeScannerFailed
		}
		logWakeEvent(ev)
		viz.Stop()
		// No chime here: the caller runs a voice turn next and voiceTurn plays
		// the ready cue itself. Two pings back to back read as a stutter, and
		// the second one is the only one whose timing is actually coupled to the
		// recorder arming (PlayAlertSync + micSettleDelay).
		return ev, wakeFired
	case <-companionInterrupt:
		// The deferred svc.Stop() and viz.Stop() run before the caller speaks,
		// which is exactly the point: the recorder must be closed before the
		// speaker opens or Helix transcribes its own remark.
		return wakeword.WakeEvent{}, wakeCompanionSpoke
	case <-ctx.Done():
		return wakeword.WakeEvent{}, wakeWindowExpired
	}
}

// wakeLapseNotice returns the one-line explanation for an outcome that silently
// dropped wake gating, or "" when there is nothing to announce.
func wakeLapseNotice(o wakeOutcome) string {
	switch o {
	case wakeWindowExpired:
		return "wake window expired — listening without the wake word; /blackbox status for info"
	case wakeScannerFailed:
		return "wake listening stopped (recorder unavailable) — listening without the wake word; " +
			"/blackbox status for info"
	default:
		return ""
	}
}

// wakeLapseAnnounced tracks which lapse notices this session has already shown.
//
// Once each, per cause: the message explains a STATE CHANGE, and the idle window
// expires every 60s of quiet, so repeating it would bury the shell in a notice
// about not listening.
var wakeLapseAnnounced = map[wakeOutcome]bool{}

// noteWakeLapse prints the notice for an outcome at most once per session.
func noteWakeLapse(o wakeOutcome) {
	notice := wakeLapseNotice(o)
	if notice == "" || wakeLapseAnnounced[o] {
		return
	}
	wakeLapseAnnounced[o] = true
	uiWarn("wake", notice)
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
	fmt.Printf("Captured %.1fs — level %.3f (%.0f dBFS) — %s\n",
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

// activeTTSProvider names the head of the TTS chain, so a recorded latency can
// be judged against the cloud or local column of §10 rather than an assumed one.
func activeTTSProvider() string {
	reg := speech.Default()
	if reg == nil {
		return ""
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
