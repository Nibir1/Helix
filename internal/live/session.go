// internal/live/session.go
// Purpose: the session itself — create, connect, pump audio both ways, and
// carry events in both directions.
//
// THE HANDSHAKE, in the order the service demands it and no other:
//
//  1. A peer connection with ONE audio track (sendrecv) and ONE data channel
//     labelled "oai-events", created by the client.
//  2. CreateOffer, SetLocalDescription, then WAIT FOR ICE GATHERING. The create
//     call is a single REST round trip with no trickle channel, so a
//     non-gathered offer negotiates a session that never connects.
//  3. POST {"session":{"model":…},"transport":{"type":"webrtc","sdp":offer}}.
//  4. SetRemoteDescription with the answer's transport.sdp.
//
// THE ONE NON-OBVIOUS PART is step 1: the Opus track must be declared with
// Channels 2. The media is mono and stays mono. But the answer's only Opus line
// is `opus/48000/2`, pion matches a local track against the negotiated codec by
// full capability, and a track declared mono fails SetRemoteDescription with
// "codec is not supported by remote" — after a paid session has already been
// created. Measured, because it is exactly the kind of thing reading the spec
// gets wrong in a way that costs money to discover.
package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Handlers are the callbacks a session makes. Every one is optional; a nil
// handler drops its events rather than panicking.
//
// They are called from the session's own goroutines and must not block: the
// data-channel reader is single-threaded, so a handler that waits stalls every
// later event including the delegation the caller is waiting for.
type Handlers struct {
	// OnHeard receives partial user speech. endMs is STREAM time, not a clock.
	OnHeard func(delta string, endMs int)

	// OnSpoke receives what the model is saying, as it says it. This is the
	// ONLY way to know what was actually vocalised — the text handed to
	// Commentary is not what comes out.
	OnSpoke func(delta string)

	// OnDelegation fires when the model hands the turn to Helix. This is the
	// end-of-turn signal, and there is no other: the service publishes no
	// explicit input-transcript-done event.
	OnDelegation func(id string)

	// OnUsage reports billed seconds, roughly every 15 s.
	OnUsage func(seconds int)

	// OnServerError reports an `error` frame. A session survives these — a
	// rejected event is not a dead connection — so this reports rather than
	// closes.
	OnServerError func(code, message, param string)

	// OnEvent sees every frame, recognised or not, already redacted. For
	// HELIX_DEBUG; nil in normal use.
	OnEvent func(eventType string, raw string)

	// OnClosed fires once when the session ends, with the cause (nil for a
	// clean Close).
	OnClosed func(err error)
}

// Session is one live conversation. Safe for concurrent use.
type Session struct {
	id    string
	pc    *webrtc.PeerConnection
	dc    *webrtc.DataChannel
	codec Codec
	h     Handlers

	track *webrtc.TrackLocalStaticSample

	mu      sync.Mutex
	pending []int16 // microphone PCM not yet packetised
	muted   bool

	audioOut *pcmQueue

	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error

	maxSeconds int
}

// secretPattern matches an OpenAI key or ephemeral secret anywhere in a string.
var secretPattern = regexp.MustCompile(`\b(?:sk|ek)-[A-Za-z0-9_\-]{8,}`)

// Redact removes anything key-shaped. Applied to every string this package can
// put in front of a user or a log, because an SDP body and an error body both
// pass through here and one of them is 1.7 kB of material nobody reads closely.
func Redact(s string) string { return secretPattern.ReplaceAllString(s, "[redacted]") }

// Dial creates the session and connects to it. The returned Session is live:
// audio flows as soon as WriteAudio is called, and events are already arriving.
//
// ctx bounds the handshake only. The session outlives it and ends at Close, at
// a transport failure, or at MaxSessionSeconds.
func Dial(ctx context.Context, opts Options, h Handlers) (*Session, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, errors.New("live: no OpenAI API key")
	}
	newCodec := opts.NewCodec
	if newCodec == nil {
		newCodec = newOpusCodec
	}
	codec, err := newCodec()
	if err != nil {
		return nil, err
	}

	s := &Session{
		codec:      codec,
		h:          h,
		closed:     make(chan struct{}),
		audioOut:   newPCMQueue(),
		maxSeconds: opts.MaxSessionSeconds,
	}
	if s.maxSeconds <= 0 {
		s.maxSeconds = DefaultMaxSessionSeconds
	}

	if err := s.setupPeer(opts); err != nil {
		codec.Close()
		return nil, err
	}

	offer, err := s.negotiate(ctx)
	if err != nil {
		s.fail(err)
		return nil, err
	}
	answer, sessionID, err := createSession(ctx, opts, offer)
	if err != nil {
		s.fail(err)
		return nil, err
	}
	s.id = sessionID
	if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: answer,
	}); err != nil {
		err = fmt.Errorf("live: apply answer: %s", Redact(err.Error()))
		s.fail(err)
		return nil, err
	}

	go s.pumpAudio()
	return s, nil
}

// setupPeer builds the peer connection, the track and the data channel.
func (s *Session) setupPeer(opts Options) error {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return fmt.Errorf("live: codecs: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: opts.ICEServers})
	if err != nil {
		return fmt.Errorf("live: peer connection: %w", err)
	}
	s.pc = pc

	// Channels 2 and this exact fmtp line, or SetRemoteDescription fails. See
	// the file comment.
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   AudioSampleRate,
		Channels:    2,
		SDPFmtpLine: "minptime=10;useinbandfec=1",
	}, "audio", "helix")
	if err != nil {
		return fmt.Errorf("live: audio track: %w", err)
	}
	s.track = track
	if _, err := pc.AddTrack(track); err != nil {
		return fmt.Errorf("live: add track: %w", err)
	}

	dc, err := pc.CreateDataChannel(dataChannelLabel, nil)
	if err != nil {
		return fmt.Errorf("live: data channel: %w", err)
	}
	s.dc = dc
	dc.OnMessage(func(msg webrtc.DataChannelMessage) { s.handleEvent(msg.Data) })

	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { go s.readRemote(tr) })
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		switch st {
		case webrtc.PeerConnectionStateFailed:
			s.fail(errors.New("live: connection failed"))
		case webrtc.PeerConnectionStateClosed:
			s.fail(nil)
		}
		// DISCONNECTED is deliberately absent. It is the transient state — a
		// few lost packets on a laptop that changed Wi-Fi — and WebRTC returns
		// from it to `connected` on its own; `failed` is the terminal one.
		// Ending a paid session on a blip would show up as a conversation that
		// randomly stops mid-sentence, and the reconnect would cost a new
		// session rather than resuming this one.
	})
	return nil
}

// negotiate produces a fully gathered offer.
func (s *Session) negotiate(ctx context.Context) (string, error) {
	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("live: create offer: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(s.pc)
	if err := s.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("live: set local description: %w", err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return "", fmt.Errorf("live: ICE gathering: %w", ctx.Err())
	}
	return s.pc.LocalDescription().SDP, nil
}

// createBody is the REST create payload. `session` and `transport`, and nothing
// else: a top-level "model" is rejected by name.
type createBody struct {
	Session   json.RawMessage `json:"session"`
	Transport struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	} `json:"transport"`
}

// createResponse is the 201. The answer SDP is nested under transport, and the
// session id is the handle /blackbox status reports.
type createResponse struct {
	Session struct {
		ID string `json:"id"`
	} `json:"session"`
	Transport struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	} `json:"transport"`
	Error struct {
		Message string `json:"message"`
		Param   string `json:"param"`
		Code    string `json:"code"`
	} `json:"error"`
}

// sessionConfig is the `session` object. Only these four fields are accepted;
// `type`, `audio.input` and a top-level `model` are each rejected by name.
type sessionConfig struct {
	Model        string `json:"model"`
	Instructions string `json:"instructions,omitempty"`
	Audio        *struct {
		Output struct {
			Voice string `json:"voice"`
		} `json:"output"`
	} `json:"audio,omitempty"`
}

// createSession POSTs the offer and returns the answer SDP and the session id.
func createSession(ctx context.Context, opts Options, offer string) (string, string, error) {
	sess := opts.SessionOverride
	if len(sess) == 0 {
		cfg := sessionConfig{Model: opts.Model, Instructions: opts.Instructions}
		if cfg.Model == "" {
			cfg.Model = DefaultModel
		}
		if cfg.Instructions == "" {
			cfg.Instructions = DefaultInstructions
		}
		if opts.Voice != "" {
			cfg.Audio = &struct {
				Output struct {
					Voice string `json:"voice"`
				} `json:"output"`
			}{}
			cfg.Audio.Output.Voice = opts.Voice
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return "", "", fmt.Errorf("live: encode session: %w", err)
		}
		sess = raw
	}

	var body createBody
	body.Session = sess
	body.Transport.Type = "webrtc"
	body.Transport.SDP = offer
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", fmt.Errorf("live: encode request: %w", err)
	}

	url := opts.CreateURL
	if url == "" {
		url = CreateEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", "", fmt.Errorf("live: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.APIKey)

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: CreateTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("live: create session: %s", Redact(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("live: read response: %w", err)
	}

	var parsed createResponse
	_ = json.Unmarshal(out, &parsed)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// The service names the offending field, and that name is the whole
		// value of the message — "Unknown parameter: 'session.type'" is a
		// fixable report and "400 Bad Request" is not.
		msg := parsed.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if parsed.Error.Param != "" {
			msg += " (" + parsed.Error.Param + ")"
		}
		return "", "", fmt.Errorf("live: create session: HTTP %d: %s", resp.StatusCode, Redact(msg))
	}
	if parsed.Transport.SDP == "" {
		return "", "", errors.New("live: create session: no SDP answer in response")
	}
	return parsed.Transport.SDP, parsed.Session.ID, nil
}

// ID is the service's session id, for /blackbox status and the journal.
func (s *Session) ID() string { return s.id }

// Done is closed when the session ends.
func (s *Session) Done() <-chan struct{} { return s.closed }

// WriteAudio queues microphone PCM. 48 kHz, 16-bit, mono.
//
// Non-blocking and lossy under back-pressure by design: this is called from the
// capture loop, and a capture loop that waits on a stalled encoder stops
// reading the recorder, which makes the microphone look dead. Dropping the
// oldest audio in a stall is the correct failure for a live conversation —
// stale speech is worse than missing speech.
func (s *Session) WriteAudio(pcm []int16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, pcm...)
	if max := AudioSampleRate * 2; len(s.pending) > max { // 2 s
		s.pending = s.pending[len(s.pending)-max:]
	}
}

// pumpAudio packetises one 20 ms frame per tick for the life of the session.
//
// Silence when there is nothing queued, rather than nothing at all: gpt-live-1
// decides many times a second whether the user is still talking, and a track
// that simply stops is not silence to it — it is a stream that ended.
func (s *Session) pumpAudio() {
	ticker := time.NewTicker(FrameMillis * time.Millisecond)
	defer ticker.Stop()
	silence := make([]int16, FrameSamples)
	frame := make([]int16, FrameSamples)
	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
			out := silence
			s.mu.Lock()
			if n := copy(frame, s.pending); n > 0 {
				for i := n; i < FrameSamples; i++ {
					frame[i] = 0
				}
				s.pending = s.pending[n:]
				out = frame
			}
			s.mu.Unlock()
			pkt, err := s.codec.Encode(out, FrameSamples)
			if err != nil {
				s.fail(fmt.Errorf("live: encode: %w", err))
				return
			}
			if err := s.track.WriteSample(media.Sample{
				Data: pkt, Duration: FrameMillis * time.Millisecond,
			}); err != nil {
				s.fail(fmt.Errorf("live: send audio: %w", err))
				return
			}
		}
	}
}

// readRemote decodes the model's audio into the playback queue.
func (s *Session) readRemote(tr *webrtc.TrackRemote) {
	for {
		select {
		case <-s.closed:
			return
		default:
		}
		pkt, _, err := tr.ReadRTP()
		if err != nil {
			return // the track ended; Close or a transport failure reports why
		}
		if len(pkt.Payload) == 0 {
			continue
		}
		pcm, err := s.codec.Decode(pkt.Payload)
		if err != nil {
			// One undecodable packet is a glitch, not a session failure. A
			// codec that is genuinely broken fails on the ENCODE side too,
			// where it does end the session.
			continue
		}
		s.audioOut.write(pcm)
	}
}

// handleEvent dispatches one data-channel frame.
func (s *Session) handleEvent(raw []byte) {
	e, ok := parseServerEvent(raw)
	if !ok {
		return
	}
	if s.h.OnEvent != nil {
		s.h.OnEvent(e.Type, Redact(string(raw)))
	}
	switch e.Type {
	case srvSessionStarted:
		// Nothing to do: the id already came back from the create call, and the
		// session is usable from the moment the data channel opens.
	case srvInputTranscript:
		if s.h.OnHeard != nil {
			s.h.OnHeard(e.Delta, e.EndMs)
		}
	case srvOutputTranscript:
		if s.h.OnSpoke != nil {
			s.h.OnSpoke(e.Delta)
		}
	case srvDelegationCreated:
		if s.h.OnDelegation != nil && e.Delegation.ID != "" {
			s.h.OnDelegation(e.Delegation.ID)
		}
	case srvUsageUpdated:
		if s.h.OnUsage != nil {
			s.h.OnUsage(e.Usage.Seconds)
		}
	case srvError:
		if s.h.OnServerError != nil {
			s.h.OnServerError(e.Error.Code, Redact(e.Error.Message), e.Error.Param)
		}
	}
}

// send writes one client event.
func (s *Session) send(e clientEvent) error {
	select {
	case <-s.closed:
		return errors.New("live: session is closed")
	default:
	}
	if s.dc == nil || s.dc.ReadyState() != webrtc.DataChannelStateOpen {
		return errors.New("live: event channel is not open")
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("live: encode %s: %w", e.Type, err)
	}
	return s.dc.SendText(string(raw))
}

// Commentary gives the model something to say for a delegated turn.
//
// IT WILL NOT BE SAID VERBATIM — see the package comment. Two rules the caller
// must respect, both measured:
//
//  1. Never send exact output that matters. Paths, SHAs, error lines and
//     version strings have all been reworded, mangled or DROPPED; one appended
//     error line came back with the path deleted and a fabricated explanation
//     in its place. Print those; speak a summary.
//  2. Wait for the acknowledgement to finish. Commentary appended while the
//     model is still speaking its own "Okay, checking it." is consumed by that
//     acknowledgement and never spoken at all. A caller that tracks OnSpoke
//     and waits for a quiet gap — see WaitQuiet — does not hit this.
func (s *Session) Commentary(delegationID, content string) error {
	if delegationID == "" {
		return errors.New("live: commentary needs a delegation id")
	}
	return s.send(clientEvent{Type: evtCommentaryAppend, DelegationID: delegationID, Content: content})
}

// Thinking gives the model private context about the work in progress. It is
// NOT spoken (verified: appended thinking produced session.thinking.appended and
// no output transcript), so it is the safe channel for content that must not be
// vocalised.
func (s *Session) Thinking(delegationID, content string) error {
	if delegationID == "" {
		return errors.New("live: thinking needs a delegation id")
	}
	return s.send(clientEvent{Type: evtThinkingAppend, DelegationID: delegationID, Content: content})
}

// Instruct steers how the model speaks THIS delegation's commentary. The
// session prompt cannot be changed once created — session.update rejects
// session.instructions — so this is the only per-turn control there is.
func (s *Session) Instruct(delegationID, content string) error {
	if delegationID == "" {
		return errors.New("live: instructions need a delegation id")
	}
	return s.send(clientEvent{Type: evtInstructionsAppnd, DelegationID: delegationID, Content: content})
}

// AddItem injects a conversation item. role is one of assistant, system,
// developer, user — the service rejects anything else by enumerating those four.
func (s *Session) AddItem(role, text string) error {
	item, err := json.Marshal(map[string]any{
		"role":    role,
		"content": []map[string]string{{"type": "input_text", "text": text}},
	})
	if err != nil {
		return err
	}
	return s.send(clientEvent{Type: evtResponseItemCreat, Item: item})
}

// MuteInput stops the model hearing the room, server-side.
//
// THIS IS ADR-005 RULE 2's ENFORCEMENT, and it is why a typed confirmation is
// safe inside a duplex session. Verified rather than hoped: while muted, a full
// spoken sentence produced no input transcript and no delegation. Without it,
// a television — or anyone in the room — could answer a destructive prompt on
// the user's behalf while they were still reading it.
func (s *Session) MuteInput() error {
	if err := s.send(clientEvent{Type: evtInputAudioMute}); err != nil {
		return err
	}
	s.mu.Lock()
	s.muted = true
	s.mu.Unlock()
	return nil
}

// UnmuteInput resumes listening.
func (s *Session) UnmuteInput() error {
	if err := s.send(clientEvent{Type: evtInputAudioUnmute}); err != nil {
		return err
	}
	s.mu.Lock()
	s.muted = false
	s.mu.Unlock()
	return nil
}

// Muted reports whether input is currently muted.
func (s *Session) Muted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.muted
}

// Audio is the model's speech as raw 48 kHz mono 16-bit PCM, for
// audio.PlaySpeechStream. Reading it is optional; an unread queue drops old
// audio rather than backing up into the RTP reader.
func (s *Session) Audio() io.ReadCloser { return s.audioOut }

// MaxSeconds is the playback bound to hand audio.StreamPlayback.
func (s *Session) MaxSeconds() int { return s.maxSeconds }

// Close ends the session. It tells the service first — a session left to time
// out keeps billing — and then tears the transport down.
func (s *Session) Close() error {
	_ = s.send(clientEvent{Type: evtSessionClose})
	s.fail(nil)
	return nil
}

// fail ends the session exactly once, whatever ended it.
func (s *Session) fail(err error) {
	s.closeOnce.Do(func() {
		s.closeErr = err
		close(s.closed)
		_ = s.audioOut.Close()
		if s.pc != nil {
			_ = s.pc.Close()
		}
		if s.codec != nil {
			s.codec.Close()
		}
		if s.h.OnClosed != nil {
			s.h.OnClosed(err)
		}
	})
}
