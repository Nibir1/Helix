// internal/live/session_test.go
// Purpose: prove the handshake and the event contract WITHOUT the network or a
// microphone (§9 rule 1).
//
// The create endpoint is an httptest server and the far side of the WebRTC
// connection is a second pion peer in this process, so the whole path —
// gathered offer, create body shape, answer applied, data channel open, events
// both ways, audio both ways — runs on loopback in under a second. The codec is
// injected, so the suite does not need libopus either; opus_test.go covers the
// real one and skips loudly when it is absent (§9 rule 6).
package live

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// fakeCodec stands in for libopus.
//
// It must COMPRESS, not just copy: a 20 ms frame of 48 kHz mono is 1,920 raw
// bytes, which exceeds pion's receive MTU, and a passthrough "codec" makes
// every packet unreadable at the far end with "short buffer" — a failure that
// looks exactly like a broken transport and has nothing to do with one. Four
// bytes carry the frame's first sample and its length, which is enough to prove
// the audio that arrived is the audio that was sent.
type fakeCodec struct{}

func (fakeCodec) Encode(pcm []int16, frameSize int) ([]byte, error) {
	v := uint16(pcm[0])
	n := uint16(frameSize)
	return []byte{byte(v), byte(v >> 8), byte(n), byte(n >> 8)}, nil
}

func (fakeCodec) Decode(pkt []byte) ([]int16, error) {
	if len(pkt) < 4 {
		return nil, io.ErrUnexpectedEOF
	}
	v := int16(uint16(pkt[0]) | uint16(pkt[1])<<8)
	n := int(uint16(pkt[2]) | uint16(pkt[3])<<8)
	out := make([]int16, n)
	for i := range out {
		out[i] = v
	}
	return out, nil
}

func (fakeCodec) Close() {}

// testPeer is the far side: it answers the offer, holds the event channel and
// can send audio back.
type testPeer struct {
	pc    *webrtc.PeerConnection
	track *webrtc.TrackLocalStaticSample

	mu       sync.Mutex
	dc       *webrtc.DataChannel
	received [][]byte
	rtp      int
}

func (p *testPeer) send(t *testing.T, frame string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		dc := p.dc
		p.mu.Unlock()
		if dc != nil && dc.ReadyState() == webrtc.DataChannelStateOpen {
			if err := dc.SendText(frame); err != nil {
				t.Fatalf("send %s: %v", frame, err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("data channel never opened on the far side")
}

func (p *testPeer) clientEvents() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, 0, len(p.received))
	for _, raw := range p.received {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func (p *testPeer) rtpCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rtp
}

// newCreateServer stands up the REST endpoint and the answering peer.
func newCreateServer(t *testing.T, onBody func(createBody)) (*httptest.Server, *testPeer) {
	t.Helper()
	peer := &testPeer{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body createBody
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("create body is not JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if onBody != nil {
			onBody(body)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}

		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Errorf("far peer: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		peer.pc = pc
		track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: AudioSampleRate, Channels: 2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		}, "audio", "server")
		if err != nil {
			t.Errorf("far track: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		peer.track = track
		if _, err := pc.AddTrack(track); err != nil {
			t.Errorf("far add track: %v", err)
		}
		pc.OnDataChannel(func(d *webrtc.DataChannel) {
			peer.mu.Lock()
			peer.dc = d
			peer.mu.Unlock()
			d.OnMessage(func(msg webrtc.DataChannelMessage) {
				peer.mu.Lock()
				peer.received = append(peer.received, append([]byte(nil), msg.Data...))
				peer.mu.Unlock()
			})
		})
		pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			go func() {
				for {
					if _, _, err := tr.ReadRTP(); err != nil {
						return
					}
					peer.mu.Lock()
					peer.rtp++
					peer.mu.Unlock()
				}
			}()
		})

		if err := pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer, SDP: body.Transport.SDP,
		}); err != nil {
			t.Errorf("far apply offer: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			t.Errorf("far answer: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		gathered := webrtc.GatheringCompletePromise(pc)
		if err := pc.SetLocalDescription(answer); err != nil {
			t.Errorf("far set local: %v", err)
		}
		<-gathered

		var out createResponse
		out.Session.ID = "live_test_1"
		out.Transport.Type = "webrtc"
		out.Transport.SDP = pc.LocalDescription().SDP
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(func() {
		srv.Close()
		if peer.pc != nil {
			_ = peer.pc.Close()
		}
	})
	return srv, peer
}

func dialTest(t *testing.T, url string, h Handlers) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := Dial(ctx, Options{
		APIKey:    "test-key",
		CreateURL: url,
		NewCodec:  func() (Codec, error) { return fakeCodec{}, nil },
	}, h)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// The create body is the part that was wrong four different ways before it was
// measured. Each assertion below is a 400 the live service actually returned.
func TestCreateBodyMatchesTheMeasuredSchema(t *testing.T) {
	var got createBody
	srv, _ := newCreateServer(t, func(b createBody) { got = b })
	dialTest(t, srv.URL, Handlers{})

	if got.Transport.Type != "webrtc" {
		t.Errorf("transport.type = %q, want webrtc — the service accepts no other", got.Transport.Type)
	}
	if !strings.HasPrefix(got.Transport.SDP, "v=0") {
		t.Errorf("transport.sdp is not an SDP offer: %.40q", got.Transport.SDP)
	}
	// A gathered offer, not a trickling one: the create call is a single round
	// trip with nowhere to put later candidates.
	if !strings.Contains(got.Transport.SDP, "a=candidate:") {
		t.Error("offer carries no ICE candidates — gathering did not complete before the POST")
	}

	var session map[string]any
	if err := json.Unmarshal(got.Session, &session); err != nil {
		t.Fatalf("session object: %v", err)
	}
	if session["model"] != DefaultModel {
		t.Errorf("session.model = %v, want %s (a TOP-LEVEL model is rejected as an unknown field)",
			session["model"], DefaultModel)
	}
	if _, ok := session["type"]; ok {
		t.Error("session.type is sent, and the service answers \"Unknown parameter: 'session.type'\"")
	}
	instr, _ := session["instructions"].(string)
	if instr != DefaultInstructions {
		t.Error("session.instructions is not the measured default; an uninstructed session answers for itself")
	}
}

// The Opus line must claim two channels or SetRemoteDescription fails after the
// session has already been created and billed.
func TestOfferDeclaresStereoOpus(t *testing.T) {
	var got createBody
	srv, _ := newCreateServer(t, func(b createBody) { got = b })
	dialTest(t, srv.URL, Handlers{})

	if !strings.Contains(got.Transport.SDP, "opus/48000/2") {
		t.Error("offer does not advertise opus/48000/2; a mono declaration is refused by the answer")
	}
}

func TestSessionOverrideReplacesTheSessionObject(t *testing.T) {
	var got createBody
	srv, _ := newCreateServer(t, func(b createBody) { got = b })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := Dial(ctx, Options{
		APIKey:          "test-key",
		CreateURL:       srv.URL,
		Voice:           "cedar",
		SessionOverride: []byte(`{"model":"gpt-live-9","future_field":true}`),
		NewCodec:        func() (Codec, error) { return fakeCodec{}, nil },
	}, Handlers{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = sess.Close() }()

	var session map[string]any
	_ = json.Unmarshal(got.Session, &session)
	if session["model"] != "gpt-live-9" || session["future_field"] != true {
		t.Errorf("SessionOverride was not sent verbatim: %v", session)
	}
	if _, ok := session["audio"]; ok {
		t.Error("Voice leaked into an overridden session object; the override must win outright")
	}
}

// Every server event this build knows must reach its handler, and an unknown
// one must be survivable — a vendor adding an event may not kill a session.
func TestServerEventsReachTheirHandlers(t *testing.T) {
	var (
		mu         sync.Mutex
		heard      strings.Builder
		spoke      strings.Builder
		delegation string
		usage      int
		errCode    string
		seen       []string
	)
	srv, peer := newCreateServer(t, nil)
	sess := dialTest(t, srv.URL, Handlers{
		OnHeard: func(d string, _ int) { mu.Lock(); heard.WriteString(d); mu.Unlock() },
		OnSpoke: func(d string) { mu.Lock(); spoke.WriteString(d); mu.Unlock() },
		OnDelegation: func(id string) {
			mu.Lock()
			delegation = id
			mu.Unlock()
		},
		OnUsage:       func(s int) { mu.Lock(); usage = s; mu.Unlock() },
		OnServerError: func(c, _, _ string) { mu.Lock(); errCode = c; mu.Unlock() },
		OnEvent:       func(typ, _ string) { mu.Lock(); seen = append(seen, typ); mu.Unlock() },
	})
	_ = sess

	peer.send(t, `{"type":"session.started","session":{"id":"live_test_1","model":"gpt-live-1"}}`)
	peer.send(t, `{"type":"session.input_transcript.delta","delta":"delete ","start_ms":0,"end_ms":200}`)
	peer.send(t, `{"type":"session.input_transcript.delta","delta":"everything","start_ms":200,"end_ms":400}`)
	peer.send(t, `{"type":"session.delegation.created","delegation":{"id":"item_abc","target":"client","type":"delegation"}}`)
	peer.send(t, `{"type":"session.output_transcript.delta","delta":"Okay."}`)
	peer.send(t, `{"type":"session.usage.updated","usage":{"seconds":15}}`)
	peer.send(t, `{"type":"error","error":{"code":"missing_required_parameter","message":"Missing required parameter: 'content'.","param":"content"}}`)
	peer.send(t, `{"type":"session.some.future.event","whatever":{"nested":1}}`)
	peer.send(t, `not json at all`)

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return delegation != "" && usage != 0 && errCode != "" && len(seen) >= 8
	}, "every event to arrive")

	mu.Lock()
	defer mu.Unlock()
	if heard.String() != "delete everything" {
		t.Errorf("input transcript = %q", heard.String())
	}
	if spoke.String() != "Okay." {
		t.Errorf("output transcript = %q", spoke.String())
	}
	if delegation != "item_abc" {
		t.Errorf("delegation = %q; this is the ONLY end-of-turn signal there is", delegation)
	}
	if usage != 15 {
		t.Errorf("usage seconds = %d", usage)
	}
	if errCode != "missing_required_parameter" {
		t.Errorf("error code = %q", errCode)
	}
	if !containsString(seen, "session.some.future.event") {
		t.Error("an unrecognised event was dropped rather than reported")
	}
	select {
	case <-sess.Done():
		t.Error("the session ended; an unknown event and a malformed frame must both be survivable")
	default:
	}
}

// The client events, with the field names the service refused three other
// spellings of.
func TestClientEventsUseTheMeasuredFieldNames(t *testing.T) {
	srv, peer := newCreateServer(t, nil)
	sess := dialTest(t, srv.URL, Handlers{})

	waitFor(t, func() bool { return sess.dc.ReadyState() == webrtc.DataChannelStateOpen }, "the event channel to open")

	if err := sess.Commentary("item_abc", "Nahasat Nibir"); err != nil {
		t.Fatalf("Commentary: %v", err)
	}
	if err := sess.Thinking("item_abc", "reading the file"); err != nil {
		t.Fatalf("Thinking: %v", err)
	}
	if err := sess.Instruct("item_abc", "verbatim please"); err != nil {
		t.Fatalf("Instruct: %v", err)
	}
	if err := sess.MuteInput(); err != nil {
		t.Fatalf("MuteInput: %v", err)
	}
	if err := sess.UnmuteInput(); err != nil {
		t.Fatalf("UnmuteInput: %v", err)
	}

	waitFor(t, func() bool { return len(peer.clientEvents()) >= 5 }, "the client events to arrive")

	want := []struct{ typ, delegation, content string }{
		{"session.commentary.append", "item_abc", "Nahasat Nibir"},
		{"session.thinking.append", "item_abc", "reading the file"},
		{"session.instructions.append", "item_abc", "verbatim please"},
	}
	got := peer.clientEvents()
	for i, w := range want {
		if got[i]["type"] != w.typ {
			t.Errorf("event %d type = %v, want %s", i, got[i]["type"], w.typ)
		}
		if got[i]["delegation_id"] != w.delegation {
			t.Errorf("event %d delegation_id = %v; the service refuses the event without it", i, got[i]["delegation_id"])
		}
		if got[i]["content"] != w.content {
			t.Errorf("event %d content = %v; the field is `content`, not `text`", i, got[i]["content"])
		}
	}
	if got[3]["type"] != "session.input_audio.mute" || got[4]["type"] != "session.input_audio.unmute" {
		t.Errorf("mute/unmute types = %v %v", got[3]["type"], got[4]["type"])
	}
}

// Commentary without a delegation id must fail locally rather than costing a
// round trip to be told so.
func TestCommentaryRequiresADelegation(t *testing.T) {
	srv, _ := newCreateServer(t, nil)
	sess := dialTest(t, srv.URL, Handlers{})
	for name, err := range map[string]error{
		"Commentary": sess.Commentary("", "hello"),
		"Thinking":   sess.Thinking("", "hello"),
		"Instruct":   sess.Instruct("", "hello"),
	} {
		if err == nil {
			t.Errorf("%s with no delegation id returned nil", name)
		}
	}
}

func TestMutedTracksTheServerRoundTrip(t *testing.T) {
	srv, _ := newCreateServer(t, nil)
	sess := dialTest(t, srv.URL, Handlers{})
	waitFor(t, func() bool { return sess.dc.ReadyState() == webrtc.DataChannelStateOpen }, "the event channel to open")

	if sess.Muted() {
		t.Fatal("a new session reports muted")
	}
	if err := sess.MuteInput(); err != nil {
		t.Fatal(err)
	}
	if !sess.Muted() {
		t.Error("MuteInput did not record the state; /blackbox status reads this")
	}
	if err := sess.UnmuteInput(); err != nil {
		t.Fatal(err)
	}
	if sess.Muted() {
		t.Error("UnmuteInput did not clear the state")
	}
}

// Audio has to flow BOTH ways, and the outbound pump must keep sending while
// the microphone is silent — a track that stops is a stream that ended, not
// silence, and gpt-live-1's turn detection reads it that way.
func TestAudioFlowsBothWaysAndSilenceKeepsSending(t *testing.T) {
	srv, peer := newCreateServer(t, nil)
	sess := dialTest(t, srv.URL, Handlers{})

	waitFor(t, func() bool { return peer.rtpCount() > 5 }, "outbound silence frames")
	quiet := peer.rtpCount()

	tone := make([]int16, FrameSamples*3)
	for i := range tone {
		tone[i] = int16(i % 1000)
	}
	sess.WriteAudio(tone)
	waitFor(t, func() bool { return peer.rtpCount() > quiet+3 }, "outbound speech frames")

	// Inbound: the far peer speaks and the PCM must reach Session.Audio.
	go func() {
		payload, _ := fakeCodec{}.Encode(tone[:FrameSamples], FrameSamples)
		for range 50 {
			_ = peer.track.WriteSample(media.Sample{Data: payload, Duration: FrameMillis * time.Millisecond})
			time.Sleep(FrameMillis * time.Millisecond)
		}
	}()

	buf := make([]byte, 1024)
	done := make(chan int, 1)
	go func() {
		n, err := sess.Audio().Read(buf)
		if err != nil {
			done <- 0
			return
		}
		done <- n
	}()
	select {
	case n := <-done:
		if n == 0 {
			t.Error("no audio arrived from the model")
		}
	case <-time.After(10 * time.Second):
		t.Error("Session.Audio never produced the model's speech")
	}
}

// A create failure must name the field the service named. "HTTP 400" is not a
// fixable report; "Unknown parameter: 'session.type'" is.
func TestCreateFailureCarriesTheServiceMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Unknown parameter: 'session.type'.","param":"session.type","code":"unknown_parameter"}}`))
	}))
	defer srv.Close()

	_, err := Dial(context.Background(), Options{
		APIKey: "test-key", CreateURL: srv.URL,
		NewCodec: func() (Codec, error) { return fakeCodec{}, nil },
	}, Handlers{})
	if err == nil {
		t.Fatal("a 400 returned no error")
	}
	if !strings.Contains(err.Error(), "session.type") {
		t.Errorf("error does not name the offending parameter: %v", err)
	}
}

// A key must never reach an error message, a log line or an event dump.
func TestRedactHidesKeyShapedText(t *testing.T) {
	for _, s := range []string{
		"Bearer sk-proj-AbCdEf0123456789",
		"ephemeral ek-abcdefgh12345678 issued",
	} {
		if out := Redact(s); strings.Contains(out, "sk-proj") || strings.Contains(out, "ek-abcdefgh") {
			t.Errorf("Redact(%q) = %q", s, out)
		}
	}
	if got := Redact("no secrets here"); got != "no secrets here" {
		t.Errorf("Redact mangled ordinary text: %q", got)
	}
}

func TestDialRefusesWithoutAKey(t *testing.T) {
	_, err := Dial(context.Background(), Options{CreateURL: "http://127.0.0.1:1"}, Handlers{})
	if err == nil {
		t.Fatal("Dial with no API key succeeded")
	}
}

func TestCloseTellsTheServiceFirst(t *testing.T) {
	srv, peer := newCreateServer(t, nil)
	sess := dialTest(t, srv.URL, Handlers{})
	waitFor(t, func() bool { return sess.dc.ReadyState() == webrtc.DataChannelStateOpen }, "the event channel to open")

	_ = sess.Close()
	waitFor(t, func() bool {
		for _, e := range peer.clientEvents() {
			if e["type"] == "session.close" {
				return true
			}
		}
		return false
	}, "session.close to be sent — a session left to time out keeps billing")

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Error("Done was not closed")
	}
	if err := sess.Commentary("item_abc", "late"); err == nil {
		t.Error("a closed session still accepts events")
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
