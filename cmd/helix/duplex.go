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
	"helix/internal/ux"
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

// duplexTranscriptSettle is how long the input transcript must be quiet before a
// delegated turn is considered complete.
//
// MEASURED, and the reason this exists: transcript deltas lag the audio they
// describe by 1.5–2 s, and session.delegation.created arrives on the model's
// own clock. So when the delegation lands, deltas for the sentence that just
// ended are STILL IN FLIGHT. Snapshotting the transcript at that instant cuts
// the end off the user's words and — worse — the late deltas then land in the
// buffer for the NEXT turn. It shows up as a fragment of one sentence glued to
// the front of the next: "[clear throat Are you still here", where the model
// had annotated a throat-clear and only half of it survived the cut.
const duplexTranscriptSettle = 600 * time.Millisecond

// duplexSettleCap bounds that wait, so a stuck delta stream cannot hold a turn.
const duplexSettleCap = 3 * time.Second

// duplexOrphanWait is how long speech may sit un-delegated before Helix takes
// the turn anyway.
//
// The model decides when a turn ends, and measurement showed it sometimes
// decides NEVER: one probe utterance produced a full transcript, no delegation,
// no speech and no event at all. §13 records that and says the case must be
// reported rather than waited on — and then the first implementation waited on
// it forever, which is how "manual mode" stopped working in a real session. The
// transcript was on screen under [hearing] and never reached
// finishVoiceTranscript, so the stop phrase never took effect.
//
// THE SAFETY VALVE MUST NOT DEPEND ON THE VENDOR'S TURN DETECTION. That is the
// whole argument for this constant: ADR-005's "manual mode" is the way out of a
// live microphone, and a way out that a third party can withhold is not one.
// 2.5s, and the number was measured rather than chosen. When the model DOES
// delegate it does so essentially the instant the user stops — the delegation
// and the last transcript delta arrive together — so any silence past a couple
// of seconds with no delegation means it has decided not to.
//
// The first value was 6s and it merged turns. A live run produced the single
// turn "Are you still there Manual mode": two utterances four seconds apart,
// both accumulated into one buffer because the rescue timer restarts on every
// new delta and the second sentence arrived before it expired. The planner got
// one garbled question, and the stop phrase only worked because matchModePhrase
// is suffix-matched — a safety valve saved by an unrelated design decision is
// not a safety valve that was working.
const duplexOrphanWait = 2500 * time.Millisecond

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
	lastHeard  time.Time       // when the USER's transcript last grew
	partial    bool            // a partial line is on screen and needs clearing

	turns       chan duplexTurn
	delegations chan string // delegation ids awaiting transcript settle
	say         chan string // replies awaiting a gap in the model's speech
	stop        sync.Once

	// viz is the waveform HUD for the turn currently being waited on, or nil.
	//
	// An atomic pointer because the METER is fed from pumpMicrophone — a
	// goroutine that has been running since the session opened — while the HUD
	// itself belongs to one turn and is created and destroyed by the REPL.
	viz atomic.Pointer[ux.VoiceViz]
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
	d := &duplexSession{
		cancel:      cancel,
		turns:       make(chan duplexTurn, 4),
		delegations: make(chan string, 8),
		say:         make(chan string, 16),
	}

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
	go d.assembleTurns(ctx)
	go d.pumpSpeech(ctx)
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
		// Metered on the way past, exactly as streamingVoiceTurn does it. The
		// duplex path reads the microphone in chunks for its own reasons, so
		// the waveform costs nothing extra and tracks the real input rather
		// than animating regardless of whether anything is heard.
		if v := d.viz.Load(); v != nil {
			v.SetLevel(speech.ClipLevel(clip))
		}
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
	d.lastHeard = time.Now()
	d.partial = true
	d.mu.Unlock()
	noteVoiceActivity(time.Now())
	// The HUD and the interim transcript share ONE terminal row, so the
	// waveform hands it over as soon as there are words: before that the meter
	// answers "is the microphone live?", after it the text is strictly more
	// informative. Same handover streamingVoiceTurn calls yieldLine.
	d.stopViz()
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
	d.delegation = id
	d.mu.Unlock()
	// NOT snapshotted here. The transcript is still arriving — see
	// duplexTranscriptSettle — so the assembler waits for it to settle first.
	// Taking it at this instant is what glued the tail of one sentence onto the
	// front of the next.
	select {
	case d.delegations <- id:
	default:
	}
}

// assembleTurns turns a delegation into a completed turn, once the transcript
// that belongs to it has finished arriving.
//
// One goroutine, so turns stay in order and `heard` has exactly one writer of
// its reset. A second delegation arriving mid-settle waits its turn in the
// channel rather than racing this one.
func (d *duplexSession) assembleTurns(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.closedOrDone():
			return
		case id := <-d.delegations:
			d.waitTranscriptSettled(ctx)
			d.mu.Lock()
			text := strings.TrimSpace(d.heard.String())
			d.heard.Reset()
			d.mu.Unlock()
			d.clearPartial()
			if text == "" {
				continue
			}
			d.emitTurn(duplexTurn{text: text, delegation: id})
		}
	}
}

// waitTranscriptSettled blocks until the user's transcript stops growing.
func (d *duplexSession) waitTranscriptSettled(ctx context.Context) {
	deadline := time.Now().Add(duplexSettleCap)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		d.mu.Lock()
		last := d.lastHeard
		d.mu.Unlock()
		if last.IsZero() || time.Since(last) >= duplexTranscriptSettle {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// emitTurn hands a completed turn to the REPL.
func (d *duplexSession) emitTurn(t duplexTurn) {
	select {
	case d.turns <- t:
	default:
		// The REPL is busy serving the previous turn. Dropping is right: a
		// queued turn would be answered minutes after it was spoken.
		if utils.IsDebugMode() {
			_, _ = fmt.Fprintf(stderrOut(), "[duplex] turn dropped, REPL busy: %q\n", t.text)
		}
	}
}

// closedOrDone is the session's closed channel, for selects.
func (d *duplexSession) closedOrDone() <-chan struct{} {
	if d.sess == nil {
		return nil
	}
	return d.sess.Done()
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
	if !duplexActive() {
		return voiceTurnWithRetry(ctx)
	}
	// THE SILENCE LADDER, which the duplex path does not otherwise get.
	//
	// voiceTurnWithRetry absorbs ErrNoSpeech and ErrEmptyTranscript and loops;
	// duplexCapture is not wrapped by it, so those errors went straight to the
	// REPL — which prints a red "voice unavailable" panel and drops the user to
	// the keyboard. An empty transcript is not an unavailable microphone, and
	// mid-conversation it is not even unusual.
	//
	// The reassurance line voiceTurnWithRetry prints every few quiet turns is
	// deliberately NOT copied: it exists because a half-duplex capture ends
	// silently and leaves nothing on screen. Here the waveform is running the
	// whole time, which answers the same question better.
	for {
		ev, err := duplexCapture(ctx)
		if err != nil && silenceIsNotAFailure(err) {
			continue
		}
		return ev, err
	}
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
	// A reply still playing through the TTS chain is superseded by a new turn,
	// the same way voiceTurn does it. Usually a no-op in duplex — the model's
	// voice arrives over RTP, not through SpeakStream — but not always: a
	// session that failed to open, or a remark queued before it opened, leaves
	// the ordinary chain talking.
	//
	// There is deliberately NO ready chime here, and that is not an omission.
	// voiceTurn plays one because its microphone opens for the turn; this one
	// has been open since the conversation started, so a chime would mark
	// nothing — and it would be HEARD. §13 records sox's silence gate opening
	// on Helix's own 880 Hz ping and STT returning the word "you"; into an
	// always-open transcribing microphone that would be a spurious turn every
	// time.
	speech.StopSpeaking()

	// Anything the companion has been holding is said HERE, at the top of a
	// turn, which is where voiceTurn drains it too. It no longer blocks: the
	// reply is queued for pumpSpeech and this returns immediately.
	//
	// It reaches the model on the PREVIOUS turn's delegation, which is still
	// current — the id is replaced when the next one arrives, not cleared when
	// a turn ends, and appending commentary to a delegation long after it was
	// created was measured working (five appends across a minute, all spoken).
	// The one case that cannot be spoken is a remark before the first turn of a
	// conversation, when there is no delegation at all; that prints and stops
	// there, which is the same place every withheld thing goes.
	drainCompanion()

	// THE WAVEFORM. Every other capture path in Helix has had one since P12.4 —
	// streamingVoiceTurn, batchVoiceTurn and the armed standby prompt all start
	// one — and the duplex turn is a third capture path that was written
	// without it. The result on screen was a bare blinking cursor under the
	// LIVE banner: a microphone that is open, listening, and showing nothing.
	viz := ux.NewVoiceViz()
	viz.Start(ux.VizListening)
	d.viz.Store(viz)
	defer d.stopViz()

	// The orphan ticker is the reason this is not a bare three-way select.
	//
	// The model decides when a turn ends, and it sometimes decides never — a
	// full transcript arrives, no delegation follows, and the words sit on
	// screen under [hearing] having never reached the pipeline. In a real
	// session that stranded "manual mode": the one phrase whose entire job is
	// to close an open microphone, withheld by the vendor's turn detection.
	orphan := time.NewTicker(time.Second)
	defer orphan.Stop()

	for {
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
		case <-orphan.C:
			if turn, ok := d.takeOrphanedTurn(); ok {
				return finishVoiceTranscript(turn.text, speech.Transcript{
					Text:     turn.text,
					Provider: duplexModel,
					IsFinal:  true,
				}, speech.AudioFormat{})
			}
		case <-d.sess.Done():
			// The session died under us — a dropped network, an expiry, a
			// transport failure. Fall back to the HALF-DUPLEX chain rather
			// than returning an error, which the REPL renders as "voice
			// unavailable" and answers by dropping the user to the keyboard.
			//
			// onClosed already says this is the intent ("falling back to the
			// half-duplex chain is better than ending it") and then the one
			// path that could act on it did the opposite. The conversation
			// continues on whatever STT the preset configured as the fallback.
			d.stopViz()
			uiWarn("full duplex", "the live session ended — continuing on the standard chain")
			return voiceTurnWithRetry(ctx)
		case <-ctx.Done():
			return input.InputEvent{}, ctx.Err()
		}
	}
}

// stopViz tears the HUD down and releases the terminal line. Idempotent: it is
// called both when words arrive and again when the turn returns, and the second
// call must not resurrect a line nobody owns.
func (d *duplexSession) stopViz() {
	if v := d.viz.Swap(nil); v != nil {
		v.Stop()
	}
}

// takeOrphanedTurn claims a transcript the model never handed over.
//
// Helix does all the reasoning anyway, so a delegation is only needed to SPEAK
// the reply — and the previous one is still valid for that (appending
// commentary to a delegation long after it was created was measured working).
// Taking the turn therefore costs nothing and rescues the case where the model
// simply never decides.
//
// Bounded by duplexOrphanWait rather than taken eagerly: a delegation usually
// IS coming, and claiming a sentence the user is still speaking would split it
// in half — the walkie-talkie failure the half-duplex path was fixed for.
func (d *duplexSession) takeOrphanedTurn() (duplexTurn, bool) {
	d.mu.Lock()
	text := strings.TrimSpace(d.heard.String())
	last := d.lastHeard
	id := d.delegation
	if text == "" || last.IsZero() || time.Since(last) < duplexOrphanWait {
		d.mu.Unlock()
		return duplexTurn{}, false
	}
	d.heard.Reset()
	d.mu.Unlock()
	d.clearPartial()
	if utils.IsDebugMode() {
		_, _ = fmt.Fprintf(stderrOut(),
			"[duplex] no delegation after %s; taking the turn anyway: %q\n", duplexOrphanWait, text)
	}
	return duplexTurn{text: text, delegation: id}, true
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
	summary, _ := live.SpeakableSummary(text)
	if summary == "" {
		return "", true
	}
	// QUEUED, NOT SPOKEN HERE. This runs on the REPL goroutine, and waiting for
	// a gap in the model's speech used to happen right here — up to 12 seconds
	// of it, on every single reply.
	//
	// That was the reported "laggy, then stuck". gpt-live-1 is conversational
	// and keeps talking, so the gap this waits for kept not arriving and the
	// wait ran its full bound. Nothing else runs during it: the REPL is not
	// reading a turn, and the keyboard watcher only exists inside
	// awakeHooks.capture — so for those seconds neither the microphone nor the
	// keyboard did anything, which is indistinguishable from a hang.
	//
	// pumpSpeech does the waiting instead, off the REPL, in order.
	select {
	case d.say <- summary:
	default:
		if utils.IsDebugMode() {
			_, _ = fmt.Fprintf(stderrOut(), "[duplex] speech queue full, dropped: %q\n", summary)
		}
	}
	return summary, true
}

// pumpSpeech appends queued replies, one at a time, each after the model has
// stopped talking.
//
// Serial by construction: two commentary appends racing would interleave two
// replies into one spoken sentence. The queue is what lets the REPL hand a
// reply over and immediately go back to listening.
func (d *duplexSession) pumpSpeech(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.closedOrDone():
			return
		case text := <-d.say:
			d.waitQuiet()
			d.mu.Lock()
			id := d.delegation
			d.mu.Unlock()
			if id == "" {
				continue
			}
			if err := d.sess.Commentary(id, text); err != nil && utils.IsDebugMode() {
				_, _ = fmt.Fprintf(stderrOut(), "[duplex] commentary: %v\n", err)
			}
		}
	}
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
	// Synchronous here, unlike duplexSpeak, and deliberately: this is a
	// QUESTION, and the next turn is its answer. Queuing it would let the turn
	// arrive before the question had been asked.
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
