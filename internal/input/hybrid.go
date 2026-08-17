// internal/input/hybrid.go
// Purpose: BlackBox Phase 7 (P7.1) — HybridSource multiplexes several input
// sources (TTY + voice simultaneously) into one event stream. Per-turn channel
// tagging already flows through the Voice Risk Policy, so an event's origin
// determines its authority regardless of which source produced it.
package input

import (
	"context"
	"sync"
)

// HybridSource merges multiple Sources into a single event stream.
type HybridSource struct {
	sources []Source
}

// NewHybridSource builds a hybrid source over the given sources.
func NewHybridSource(sources ...Source) *HybridSource {
	return &HybridSource{sources: sources}
}

// Events starts every source and multiplexes their events onto one channel.
// The output channel closes when all sources have closed or ctx is cancelled.
func (h *HybridSource) Events(ctx context.Context) (<-chan InputEvent, error) {
	streams := make([]<-chan InputEvent, 0, len(h.sources))
	for _, src := range h.sources {
		ch, err := src.Events(ctx)
		if err != nil {
			return nil, err
		}
		streams = append(streams, ch)
	}

	out := make(chan InputEvent)
	var wg sync.WaitGroup
	for _, ch := range streams {
		ch := ch
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ev := range ch {
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out, nil
}

// Close releases every underlying source.
func (h *HybridSource) Close() error {
	for _, s := range h.sources {
		_ = s.Close()
	}
	return nil
}

// chanSource adapts a channel-backed fake for tests and simple cases.
type chanSource struct {
	ch     <-chan InputEvent
	closed bool
}

// EventsFrom returns a Source that yields the given events then closes.
func EventsFrom(events ...InputEvent) Source {
	ch := make(chan InputEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return &chanSource{ch: ch}
}

func (c *chanSource) Events(ctx context.Context) (<-chan InputEvent, error) {
	out := make(chan InputEvent)
	go func() {
		defer close(out)
		for ev := range c.ch {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (c *chanSource) Close() error {
	c.closed = true
	return nil
}
