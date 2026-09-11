// cmd/helix/duplex.go
// Purpose: gpt-live-1 as a way of being AWAKE.
//
// WHAT IS AND IS NOT NEW HERE. No new listening state, no new exit, no new
// stop phrase. STANDBY/AWAKE/MANUAL already mean the three things a user can
// want (listen_mode.go), and full duplex is a different way of taking a turn
// inside AWAKE — not a fourth state. So the session opens where a conversation
// opens and closes where one closes, the stop phrases work because the
// transcript reaches finishVoiceTranscript exactly as a half-duplex one does,
// and the inactivity stand-down still bounds it — which matters more here than
// anywhere else, because an open duplex session bills $0.05 a minute per second
// whether or not anyone is talking.
//
// HOW A TURN REACHES THE PIPELINE, which is the whole point of the design:
//
//	audio  → WebRTC/Opus → gpt-live-1 decides the turn ended
//	       → session.delegation.created + the input transcript
//	       → finishVoiceTranscript  (the SAME funnel the half-duplex paths use)
//	       → matchModePhrase · isVoiceRebootPhrase · isEyesOffPhrase
//	       → dispatchVoiceCommand · shell.Classify + directShellAllowed (FALSE
//	         for voice, always) → the planner → risk tiers → sandbox
//
// gpt-live-1 decides NOTHING in that chain. It is the ear and the mouth; Helix
// is still the brain, and the Instruction Firewall, the Medium risk ceiling and
// the sandbox are all exactly where they were. That is the whole reason this is
// client delegation and not Responses delegation (see internal/live).
//
// THE KEYBOARD STAYS LIVE because awakeTurn already owns that and this is
// installed as its capture hook — the cbreak poll, the mid-turn cancel and the
// "a keystroke discards the partial" rule are inherited unchanged rather than
// reimplemented.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"helix/internal/audio"
	"helix/internal/config"
	"helix/internal/input"
	"helix/internal/live"
	"helix/internal/shell"
	"helix/internal/speech"
	"helix/internal/utils"
)

// duplexModel is the only model that takes this path. An exact match, not a
// prefix: gpt-live-transcribe is a different endpoint with a different
// contract, and routing it here would open a WebRTC session against a
// transcription model.
const duplexModel = live.DefaultModel

// duplexCaptureChunk is how much microphone audio is handed to the session at a
// time. 100 ms — five Opus frames — keeps the recorder read cheap while staying
// far below the end-of-turn latency gpt-live-1 works at.
const duplexCaptureChunk = 100 * time.Millisecond

// duplexQuietGap is how long the model must stop talking before commentary is
// appended.
//
// MEASURED, and the reason this constant exists at all: commentary appended
// while the model is still speaking its own acknowledgement is CONSUMED by that
// acknowledgement and never spoken. Sending "Nahasat Nibir" on the same tick as
// the delegation produced session.commentary.appended and the single word
// "On it." — the content silently gone. 1.5 s clears every acknowledgement seen
// (they run 2–5 words) with room for the 1.5–2 s lag between the audio and the
// transcript deltas that report it.
const duplexQuietGap = 1500 * time.Millisecond

// duplexQuietWait bounds that wait, so a model that never stops talking cannot
// hold a turn open forever.
const duplexQuietWait = 12 * time.Second

// duplexAnswerWait bounds a spoken yes/no inside a duplex session. ADR-005
// rule 3: silence declines.
const duplexAnswerWait = 20 * time.Second

// duplexSession is the live session plus the turn bookkeeping the REPL needs.
type duplexSession struct {
	sess     *live.Session
	recorder *speech.StreamRecorder
	cancel   context.CancelFunc

	mu         sync.Mutex
	heard      strings.Builder // the current turn's transcript, as it arrives
	delegation string          // the delegation the current turn belongs to
	lastSpoke  time.Time       // when the model last emitted an output delta
	partial    bool            // a partial line is on screen and needs clearing

	turns chan duplexTurn
	stop  sync.Once
}

// duplexTurn is one completed user turn.
type duplexTurn struct {
	text       string
	delegation string
}

// duplexCur holds the open session, or nil. An atomic because OnSpeak, the
// prompter and the companion all read it from goroutines that are not the REPL.
var duplexCur atomic.Pointer[duplexSession]

// duplexActive reports whether a full-duplex session is open.
func duplexActive() bool { return duplexCur.Load() != nil }

// duplexSelected reports whether the configuration asks for full duplex.
//
// The model id IS the switch, with no new plumbing: model selection is already
// runtime-resolved, so typing gpt-live-1 as the STT model is how you choose
// this. The provider check keeps a local sidecar that happens to advertise the
// same id from opening an OpenAI session.
func duplexSelected() bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Speech.STT.Provider), "openai") &&
		strings.EqualFold(strings.TrimSpace(cfg.Speech.STT.Model), duplexModel)
}

// duplexPreflight reports why a duplex session cannot open, or nil.
//
// Checked at the door rather than discovered halfway in: every failure below
// happens BEFORE a session is created, and a session that is created and then
// abandoned has already been billed.
func duplexPreflight() error {
	if _, err := speech.DetectRecorder(); err != nil {
		return err
	}
	if err := live.OpusAvailable(); err != nil {
		return err
	}
	if duplexAPIKey() == "" {
		return errors.New("no OpenAI key for speech — run /blackbox setup")
	}
	// Checked here rather than discovered later, because the failure is silent
	// in the worst way: the session connects, bills, transcribes and answers,
	// and the user hears nothing at all. A half-duplex chain degrades to a
	// readable screen; this one degrades to a microphone that seems to do
	// nothing.
	if !audio.IsEnabled() {
		return errors.New("audio output is off — /blackbox would have nothing to speak with (/audio on)")
	}
	return nil
}

// duplexAPIKey reads the OpenAI STT key from the speech keystore.
func duplexAPIKey() string {
	reg := speech.Default()
	if reg == nil {
		return ""
	}
	return strings.TrimSpace(reg.Keys().Get(speech.STTKeyPrefix + "openai"))
}

// duplexOptions builds the session options from config, escape hatches and all.
func duplexOptions() live.Options {
	lc := cfg.Speech.Live
	return live.Options{
		APIKey:            duplexAPIKey(),
		Model:             duplexModel,
		Instructions:      lc.Instructions,
		Voice:             lc.Voice,
		CreateURL:         lc.CreateURL,
		SessionOverride:   []byte(lc.Session),
		MaxSessionSeconds: lc.MaxSessionSeconds,
	}
}

// startDuplex opens the session and starts both audio pumps. Called from
// enterAwakeLocked; a failure there falls back to the half-duplex chain rather
// than refusing the conversation.
func startDuplex() error {
	if duplexActive() {
		return nil
	}
	if err := duplexPreflight(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &duplexSession{cancel: cancel, turns: make(chan duplexTurn, 4)}

	rec, err := speech.NewStreamRecorder(ctx, live.AudioSampleRate)
	if err != nil {
		cancel()
		return err
	}
	d.recorder = rec

	dialCtx, dialCancel := context.WithTimeout(ctx, live.CreateTimeout)
	defer dialCancel()
	sess, err := live.Dial(dialCtx, duplexOptions(), live.Handlers{
		OnHeard:       d.onHeard,
		OnSpoke:       d.onSpoke,
		OnDelegation:  d.onDelegation,
		OnUsage:       d.onUsage,
		OnServerError: d.onServerError,
		OnEvent:       duplexDebugEvent,
		OnClosed:      d.onClosed,
	})
	if err != nil {
		_ = rec.Close()
		cancel()
		return err
	}
	d.sess = sess

	duplexCur.Store(d)
	// Which libopus was loaded is the first question a "the audio is wrong"
	// report needs answered, and there are seven candidate paths. It does not
	// earn a line on the panel — the row there is already at the width the
	// panel will render — so it goes where diagnostics go.
	if utils.IsDebugMode() {
		_, _ = fmt.Fprintf(stderrOut(), "[duplex] session %s  opus %s\n",
			sess.ID(), live.OpusLibraryName())
	}
	go d.pumpMicrophone(ctx)
	go d.playModelAudio(ctx)
	return nil
}

// stopDuplex closes the session. Idempotent, and safe to call when none is open.
func stopDuplex() {
	d := duplexCur.Swap(nil)
	if d == nil {
		return
	}
	d.shutdown()
}

func (d *duplexSession) shutdown() {
	d.stop.Do(func() {
		if d.sess != nil {
			_ = d.sess.Close()
		}
		if d.recorder != nil {
			_ = d.recorder.Close()
		}
		d.cancel()
		d.clearPartial()
	})
}

// pumpMicrophone reads the recorder and feeds the session for the whole
// conversation. Unlike every other capture path in Helix it never stops between
// turns — that IS full duplex, and it is why the model can be interrupted.
func (d *duplexSession) pumpMicrophone(ctx context.Context) {
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}
		clip, err := d.recorder.ReadChunk(ctx, duplexCaptureChunk)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Five consecutive failures end the loop, matching the wake
			// scanner: a dead microphone must present as a dead microphone
			// rather than as a shell that still claims to be listening.
			failures++
			if failures >= 5 {
				uiWarn("microphone", "capture stopped: "+err.Error())
				stopDuplex()
				return
			}
			continue
		}
		failures = 0
		pcm, err := speech.DecodeWAVPCM16(clip.Bytes)
		if err != nil {
			continue
		}
		d.sess.WriteAudio(pcm)
	}
}

// playModelAudio plays the model's voice through the existing beep/oto stack
// (ADR-007). One long-lived stream for the session, which is why
// StreamPlayback.MaxSeconds exists.
func (d *duplexSession) playModelAudio(ctx context.Context) {
	err := audio.PlaySpeechStream(ctx, audio.StreamFormat{
		SampleRate: live.AudioSampleRate,
		Channels:   1,
	}, d.sess.Audio(), audio.StreamPlayback{MaxSeconds: d.sess.MaxSeconds()})
	if err == nil || ctx.Err() != nil {
		return
	}
	// Said out loud, not left to HELIX_DEBUG. Playback is the entire output
	// half of a duplex session: without it the conversation still connects,
	// still transcribes and still bills, and the user simply hears nothing —
	// which reads as "it is broken" with nothing on screen to say why.
	uiWarn("live session", "the model's voice cannot play: "+live.Redact(err.Error()))
}

// onHeard accumulates the user's words and paints the partial line, the way
// streamingVoiceTurn does — same row, same "[hearing]" label, so a duplex turn
// looks like a streaming turn rather than like a new feature.
func (d *duplexSession) onHeard(delta string, _ int) {
	if strings.TrimSpace(delta) == "" {
		return
	}
	d.mu.Lock()
	d.heard.WriteString(delta)
	text := strings.TrimSpace(d.heard.String())
	d.partial = true
	d.mu.Unlock()
	noteVoiceActivity(time.Now())
	fmt.Printf("\r\x1b[2K[hearing] %s", text)
}

// onSpoke records when the model last said something. Only the TIME is kept:
// what it says is its own paraphrase and Helix has no use for it, but the
// timing is what duplexQuietGap is measured against.
func (d *duplexSession) onSpoke(string) {
	d.mu.Lock()
	d.lastSpoke = time.Now()
	d.mu.Unlock()
}

// onDelegation closes the turn. This is the ONLY end-of-turn signal the service
// publishes — there is no input-transcript-done event — so a turn that
// transcribes and never delegates is a turn that is lost, which is why
// duplexCapture reports it instead of waiting forever.
func (d *duplexSession) onDelegation(id string) {
	d.mu.Lock()
	text := strings.TrimSpace(d.heard.String())
	d.heard.Reset()
	d.delegation = id
	d.mu.Unlock()
	d.clearPartial()
	if text == "" {
		return
	}
	select {
	case d.turns <- duplexTurn{text: text, delegation: id}:
	default:
		// The REPL is busy serving the previous turn. Dropping is right: a
		// queued turn would be answered minutes after it was spoken.
	}
}

func (d *duplexSession) onUsage(seconds int) {
	if utils.IsDebugMode() {
		_, _ = fmt.Fprintf(stderrOut(), "[duplex] billed %ds\n", seconds)
	}
}

func (d *duplexSession) onServerError(code, message, param string) {
	// Reported, not fatal: a rejected event is not a dead connection, and the
	// service names the offending field — which is the only actionable part.
	detail := message
	if param != "" {
		detail += " (" + param + ")"
	}
	uiWarn("live session", strings.TrimSpace(code+" "+detail))
}

func (d *duplexSession) onClosed(err error) {
	if err != nil {
		uiWarn("live session", "closed: "+live.Redact(err.Error()))
	}
	// The session ended under us. Drop the pointer so nothing keeps speaking
	// into a dead channel; the conversation itself is left alone, because
	// falling back to the half-duplex chain is better than ending it.
	duplexCur.CompareAndSwap(d, nil)
}

func (d *duplexSession) clearPartial() {
	d.mu.Lock()
	on := d.partial
	d.partial = false
	d.mu.Unlock()
	if on {
		fmt.Print("\r\x1b[2K")
	}
}

// duplexDebugEvent mirrors every frame to stderr under HELIX_DEBUG. Already
// redacted by internal/live.
func duplexDebugEvent(eventType, raw string) {
	if utils.IsDebugMode() {
		_, _ = fmt.Fprintf(stderrOut(), "[duplex] %s %s\n", eventType, raw)
	}
}

// awakeCapture is awakeHooks.capture: one turn, taken however this conversation
// is able to take one.
//
// The dispatch is here and not inside voiceTurnWithRetry because a duplex turn
// is not a retry of a half-duplex one — there is no clip, no chime, no silence
// timer and nothing to retry. Keeping voiceTurnWithRetry untouched also keeps
// the guarantee voice_silence_test.go asserts about its body: that it has no
// attempt cap.
func awakeCapture(ctx context.Context) (input.InputEvent, error) {
	if duplexActive() {
		return duplexCapture(ctx)
	}
	return voiceTurnWithRetry(ctx)
}

// duplexCapture takes one duplex turn. Installed as awakeHooks.capture, so the
// keyboard handling, the Ctrl+C registration and the discard-the-partial rule
// are awakeTurn's and are not duplicated here.
func duplexCapture(ctx context.Context) (input.InputEvent, error) {
	d := duplexCur.Load()
	if d == nil {
		// The session died mid-conversation. The half-duplex chain still works,
		// so take the turn that way rather than ending the conversation.
		return voiceTurnWithRetry(ctx)
	}
	// Anything the companion has been holding is said HERE, at the top of a
	// turn, which is where voiceTurn drains it too.
	//
	// It reaches the model on the PREVIOUS turn's delegation, which is still
	// current — the id is replaced when the next one arrives, not cleared when
	// a turn ends, and appending commentary to a delegation long after it was
	// created was measured working (five appends across a minute, all spoken).
	// The one case that cannot be spoken is a remark before the first turn of a
	// conversation, when there is no delegation at all; that prints and stops
	// there, which is the same place every withheld thing goes.
	drainCompanion()

	select {
	case turn := <-d.turns:
		// The same funnel a typed line and a half-duplex clip reach. Nothing
		// about this turn's origin gives it more authority: Channel stays
		// ChannelVoice, so the Medium ceiling and the denied-command list apply.
		return finishVoiceTranscript(turn.text, speech.Transcript{
			Text:     turn.text,
			Provider: duplexModel,
			IsFinal:  true,
		}, speech.AudioFormat{})
	case <-d.sess.Done():
		return input.InputEvent{}, errors.New("live session ended")
	case <-ctx.Done():
		return input.InputEvent{}, ctx.Err()
	}
}

// duplexSpeak hands a reply to the model to vocalise.
//
// Returns the text actually sent — which is NOT the text passed in — and
// whether the duplex session claimed the reply. handled=false means there is no
// session and the caller should use the TTS chain; handled=true with an empty
// spoken string means a session is open but the reply could not be given to it,
// and the caller must NOT fall back (see below).
//
// TWO RULES, BOTH MEASURED, both enforced here rather than at each caller:
//
//  1. Wait for the model to stop talking. Commentary appended over its own
//     acknowledgement is swallowed whole.
//  2. Never hand it exact content. live.SpeakableSummary decides; a reply
//     carrying a path, a SHA, a version, a level tag or more than one line is
//     summarised for the ear and left on screen in full.
func duplexSpeak(text string) (spoken string, handled bool) {
	d := duplexCur.Load()
	if d == nil {
		return "", false
	}
	text = strings.TrimSpace(shell.Plain(text))
	if text == "" {
		return "", true
	}
	d.mu.Lock()
	id := d.delegation
	d.mu.Unlock()
	if id == "" {
		// A session is open but no turn is in flight, so there is no
		// delegation to attach commentary to — and in client delegation there
		// is no other way to make the model speak (response.create is refused
		// outright: "requires a session with Responses delegation").
		//
		// The reply is CLAIMED anyway rather than handed back to the TTS chain.
		// Falling through would play Helix's own voice into a microphone that
		// is open and being transcribed, and the session would hear itself and
		// answer it. Every caller prints what it says, so the text is on screen
		// either way — which is this feature's policy everywhere else too.
		if utils.IsDebugMode() {
			_, _ = fmt.Fprintf(stderrOut(), "[duplex] not spoken (no turn in flight): %q\n", text)
		}
		return "", true
	}
	d.waitQuiet()
	summary, _ := live.SpeakableSummary(text)
	if summary == "" {
		return "", true
	}
	if err := d.sess.Commentary(id, summary); err != nil {
		if utils.IsDebugMode() {
			_, _ = fmt.Fprintf(stderrOut(), "[duplex] commentary: %v\n", err)
		}
		// Still CLAIMED. The send failed, but the microphone is open and being
		// transcribed, so handing this to the TTS chain would play Helix's own
		// voice into the session and have it answer itself.
		return "", true
	}
	return summary, true
}

// waitQuiet blocks until the model has been silent for duplexQuietGap.
func (d *duplexSession) waitQuiet() {
	deadline := time.Now().Add(duplexQuietWait)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		last := d.lastSpoke
		d.mu.Unlock()
		if last.IsZero() || time.Since(last) >= duplexQuietGap {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// duplexAwaitAnswer speaks a question and returns the next thing the user says.
//
// This is what makes a confirmation answerable hands-free in a duplex session:
// there is no second recorder to open — the session already owns the
// microphone — so the answer is simply the next delegated turn. Fails closed on
// timeout (ADR-005 rule 3).
func (d *duplexSession) duplexAwaitAnswer(question string) (string, bool) {
	d.mu.Lock()
	id := d.delegation
	d.mu.Unlock()
	if id == "" {
		return "", false
	}
	d.waitQuiet()
	if err := d.sess.Commentary(id, shell.Plain(question)); err != nil {
		return "", false
	}
	select {
	case turn := <-d.turns:
		return turn.text, true
	case <-time.After(duplexAnswerWait):
		return "", false
	case <-d.sess.Done():
		return "", false
	}
}

// stderrOut is indirected so tests can capture debug output without touching
// os.Stderr globally.
var stderrOut = defaultStderr

// duplexStatusLine describes the session for /blackbox status. Empty when there
// is none.
func duplexStatusLine() string {
	d := duplexCur.Load()
	if d == nil {
		if !duplexSelected() {
			return ""
		}
		// The MODEL leads every one of these, because it is the switch: there
		// is no enabled flag, so "why is this on?" is answered only by naming
		// what selected it. The first cut of this row said "full duplex
		// selected" and nothing else — rendering the panel is what showed that
		// it named neither the model nor the reason, and wrapped besides.
		if err := duplexPreflight(); err != nil {
			return duplexModel + "  ·  unavailable: " + err.Error()
		}
		// The PRICE, not "opens with the next conversation" — which is what the
		// "selected" badge beside it already means, and which was long enough
		// that the panel truncated it mid-sentence. This is the one row in
		// Helix describing something that bills by the second while it is on,
		// and that is the fact worth the width.
		return duplexModel + "  ·  $0.05/min while open"
	}
	line := duplexModel + "  ·  " + d.sess.ID()
	if d.sess.Muted() {
		line += "  ·  input muted"
	}
	return line
}

// duplexLiveConfigured reports whether the config section names anything, for
// /blackbox status.
func duplexLiveConfigured(lc config.SpeechLiveConfig) bool {
	return lc.Instructions != "" || lc.Voice != "" || lc.CreateURL != "" ||
		len(lc.Session) > 0 || lc.MaxSessionSeconds > 0
}
