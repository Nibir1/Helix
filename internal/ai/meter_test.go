package ai

import (
	"errors"
	"testing"
	"time"
)

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("empty text = %d, want 0", got)
	}
	// Short but non-empty text must never estimate as free.
	if got := EstimateTokens("hi"); got != 1 {
		t.Errorf("short text = %d, want 1", got)
	}
	if got := EstimateTokens("12345678"); got != 2 {
		t.Errorf("8 chars = %d, want 2", got)
	}
	// Runes, not bytes: a multi-byte prompt must not be over-counted 3x.
	if got := EstimateTokens("日本語日本語日本語日本語"); got != 3 {
		t.Errorf("12 runes = %d, want 3", got)
	}
}

func TestRecordCallAggregatesByKindProviderModel(t *testing.T) {
	ResetUsage()
	t.Cleanup(ResetUsage)

	RecordCall(KindChat, "openai", "gpt-x", "12345678", "abcd", 100*time.Millisecond, nil)
	RecordCall(KindChat, "openai", "gpt-x", "12345678", "abcd", 300*time.Millisecond, nil)
	RecordCall(KindPlanner, "openai", "gpt-x", "1234", "ab", 50*time.Millisecond, errors.New("boom"))

	rep := Usage()
	if rep.Calls != 3 {
		t.Fatalf("calls = %d, want 3", rep.Calls)
	}
	if rep.Failures != 1 {
		t.Errorf("failures = %d, want 1", rep.Failures)
	}
	if len(rep.Rows) != 2 {
		t.Fatalf("rows = %d, want 2 (chat and planner are separate)", len(rep.Rows))
	}

	// Sorted by estimated tokens descending, so the expensive traffic is on top.
	if rep.Rows[0].Kind != KindChat {
		t.Errorf("first row = %q, want the larger chat row first", rep.Rows[0].Kind)
	}
	chat := rep.Rows[0]
	if chat.Calls != 2 {
		t.Errorf("chat calls = %d, want 2", chat.Calls)
	}
	if chat.AvgLatency() != 200*time.Millisecond {
		t.Errorf("avg latency = %v, want 200ms", chat.AvgLatency())
	}
	if chat.EstPromptTokens != 4 { // 2 calls x 8 chars / 4
		t.Errorf("est prompt tokens = %d, want 4", chat.EstPromptTokens)
	}
	if chat.EstTotalTokens() != chat.EstPromptTokens+chat.EstResponseTokens {
		t.Error("EstTotalTokens must be the sum of its parts")
	}

	// A failed call still cost latency and prompt tokens; omitting it would
	// under-report a session that mostly failed.
	planner := rep.Rows[1]
	if planner.Failures != 1 || planner.EstPromptTokens == 0 {
		t.Errorf("a failed call must still be billed: %+v", planner)
	}
}

func TestRecordCallLabelsUnknownProvider(t *testing.T) {
	ResetUsage()
	t.Cleanup(ResetUsage)

	RecordCall(KindChat, "", "", "prompt", "reply", time.Millisecond, nil)
	rep := Usage()
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rep.Rows))
	}
	if rep.Rows[0].Provider != "unknown" || rep.Rows[0].Model != "unknown" {
		t.Errorf("blank provider/model should be labelled, got %q/%q",
			rep.Rows[0].Provider, rep.Rows[0].Model)
	}
}

func TestResetUsage(t *testing.T) {
	ResetUsage()
	RecordCall(KindChat, "p", "m", "x", "y", time.Millisecond, nil)
	ResetUsage()

	rep := Usage()
	if rep.Calls != 0 || len(rep.Rows) != 0 {
		t.Errorf("reset left %d calls and %d rows", rep.Calls, len(rep.Rows))
	}
	if !rep.Started.IsZero() {
		t.Error("reset must clear the metering start time")
	}
}

func TestUsageEmptyReport(t *testing.T) {
	ResetUsage()
	t.Cleanup(ResetUsage)

	rep := Usage()
	if rep.Calls != 0 || rep.EstTotalTokens() != 0 {
		t.Errorf("a fresh meter must report nothing, got %+v", rep)
	}
	if got := (UsageRow{}).AvgLatency(); got != 0 {
		t.Errorf("AvgLatency on a zero row = %v, want 0 (no division by zero)", got)
	}
}

func TestRecordCallIsConcurrencySafe(t *testing.T) {
	ResetUsage()
	t.Cleanup(ResetUsage)

	const workers, each = 8, 50
	done := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < each; i++ {
				RecordCall(KindChat, "p", "m", "abcd", "ab", time.Millisecond, nil)
			}
		}()
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	if got := Usage().Calls; got != workers*each {
		t.Errorf("calls = %d, want %d — a lost update means the mutex is not covering a field",
			got, workers*each)
	}
}
