// internal/live/pcmqueue_test.go
// Purpose: the audio queue must never block the RTP reader, and must never
// misalign samples when it drops.
package live

import (
	"io"
	"testing"
	"time"
)

// The writer is the goroutine draining the socket. If it can ever block, the
// jitter buffer grows until the connection is unusable.
func TestWriteNeverBlocksEvenWithNoReader(t *testing.T) {
	q := newPCMQueue()
	done := make(chan struct{})
	go func() {
		defer close(done)
		frame := make([]int16, FrameSamples)
		for range 500 { // 10 s of audio into a queue nobody reads
			q.write(frame)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("write blocked with no reader; the RTP reader would stall")
	}
}

// An overflow must resume on a 20 ms FRAME boundary, or the decoder is handed a
// partial frame and the splice clicks.
//
// The fixture is a ramp whose every sample carries its own index, so the first
// surviving sample says exactly where the cut landed. An earlier version of
// this test asserted sample alignment instead and could not fail: write appends
// two bytes per sample, so a byte-count drop is always even anyway (§9 rule 8 —
// found by mutating the guard away and watching it stay green).
func TestOverflowResumesOnAFrameBoundary(t *testing.T) {
	q := newPCMQueue()
	// An odd number of samples first, so the surviving offset is only frame
	// aligned if the drop was computed to make it so.
	total := pcmQueueMaxBytes + 777
	ramp := make([]int16, total)
	for i := range ramp {
		ramp[i] = int16(i % FrameSamples)
	}
	q.write(ramp)

	buf := make([]byte, 4)
	if _, err := io.ReadFull(q, buf); err != nil {
		t.Fatal(err)
	}
	first := int16(uint16(buf[0]) | uint16(buf[1])<<8)
	if first != 0 {
		t.Errorf("the queue resumes at sample %d of a frame, not at a frame boundary", first)
	}
}

func TestQueueIsBounded(t *testing.T) {
	q := newPCMQueue()
	q.write(make([]int16, pcmQueueMaxBytes*4))
	q.mu.Lock()
	held := len(q.buf)
	q.mu.Unlock()
	if held > pcmQueueMaxBytes {
		t.Errorf("queue holds %d bytes, cap is %d", held, pcmQueueMaxBytes)
	}
}

func TestCloseUnblocksAPendingRead(t *testing.T) {
	q := newPCMQueue()
	errc := make(chan error, 1)
	go func() {
		_, err := q.Read(make([]byte, 16))
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = q.Close()
	select {
	case err := <-errc:
		if err != io.EOF {
			t.Errorf("Read returned %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not unblock the reader; playback would hang on teardown")
	}
}

func TestWriteAfterCloseIsDropped(t *testing.T) {
	q := newPCMQueue()
	_ = q.Close()
	q.write(make([]int16, 10)) // must not panic or resurrect the queue
	if _, err := q.Read(make([]byte, 4)); err != io.EOF {
		t.Errorf("a closed queue accepted a write: %v", err)
	}
}
