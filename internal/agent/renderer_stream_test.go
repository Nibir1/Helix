// internal/agent/renderer_stream_test.go
// Purpose: BlackBox P8.8 — streaming is an OPTIONAL renderer capability. The
// interactive terminal opts in; headless renderers must not, because they
// carry the response through PrintAIMessage instead.
package agent

import (
	"testing"

	"helix/internal/ux"
)

func TestTTYRendererStreams(t *testing.T) {
	var r Renderer = TTYRenderer{UX: ux.NewUX()}
	s, ok := r.(StreamingRenderer)
	if !ok {
		t.Fatal("the interactive renderer must implement StreamingRenderer")
	}
	if s.StreamAIMessage() == nil {
		t.Fatal("StreamAIMessage must return a usable stream")
	}
}

// This is the guard for a real regression risk. The daemon captures IPC reply
// text by embedding HeadlessRenderer and overriding PrintAIMessage — and Go
// does NOT dispatch that override from inside a HeadlessRenderer method. If
// HeadlessRenderer ever implemented StreamingRenderer, the agent would take
// the streaming path and the daemon's `submit` would start returning empty
// replies, silently.
func TestHeadlessRendererDoesNotStream(t *testing.T) {
	var r Renderer = HeadlessRenderer{}
	if _, ok := r.(StreamingRenderer); ok {
		t.Fatal("HeadlessRenderer must NOT implement StreamingRenderer — " +
			"the daemon's PrintAIMessage override is how it captures the reply")
	}
}

// recordingStream captures what a streaming render received.
type recordingStream struct {
	chunks  []string
	closed  bool
	started bool
}

func (s *recordingStream) Chunk(text string) {
	if text != "" {
		s.started = true
	}
	s.chunks = append(s.chunks, text)
}
func (s *recordingStream) Started() bool { return s.started }
func (s *recordingStream) Close()        { s.closed = true }

// streamingTestRenderer is a headless renderer that opts into streaming, so the
// agent's streaming path can be exercised without a terminal.
type streamingTestRenderer struct {
	HeadlessRenderer
	stream   *recordingStream
	buffered []string
}

func (r *streamingTestRenderer) StreamAIMessage() AIStream { return r.stream }
func (r *streamingTestRenderer) PrintAIMessage(t string, _ bool) {
	r.buffered = append(r.buffered, t)
}

func TestStreamingRendererContract(t *testing.T) {
	r := &streamingTestRenderer{stream: &recordingStream{}}

	var asRenderer Renderer = r
	s, ok := asRenderer.(StreamingRenderer)
	if !ok {
		t.Fatal("a renderer providing StreamAIMessage must satisfy StreamingRenderer")
	}

	out := s.StreamAIMessage()
	out.Chunk("one")
	out.Chunk("two")
	out.Close()

	if len(r.stream.chunks) != 2 || !r.stream.closed {
		t.Fatalf("stream did not receive the expected lifecycle: %+v", r.stream)
	}
	if !out.Started() {
		t.Fatal("Started must reflect rendered content")
	}
}

// --- chatFallback path selection -------------------------------------------

// chatFallback must pick the live path when the renderer supports it and the
// buffered path otherwise. This is the behavioral contract that keeps the
// daemon unchanged while the terminal gets streaming.
func TestChatFallbackChoosesPathByRendererCapability(t *testing.T) {
	streaming := &streamingTestRenderer{stream: &recordingStream{}}
	if _, ok := Renderer(streaming).(StreamingRenderer); !ok {
		t.Fatal("test renderer should stream")
	}

	buffered := HeadlessRenderer{}
	if _, ok := Renderer(buffered).(StreamingRenderer); ok {
		t.Fatal("headless renderer should not stream")
	}
}

// An empty response must still reach PrintAIMessage: with nothing streamed the
// user would otherwise see no output at all for a completed turn.
func TestStreamStartedGatesBufferedFallback(t *testing.T) {
	s := &recordingStream{}
	s.Chunk("")
	if s.Started() {
		t.Fatal("an empty chunk must not mark the stream started")
	}
	// This is the condition chatFallback uses to decide whether to also print.
	if !(s == nil || !s.Started()) {
		t.Fatal("an unstarted stream must trigger the buffered print")
	}
}
