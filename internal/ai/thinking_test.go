// internal/ai/thinking_test.go
// Purpose: the planner's thinking switch reaches planner calls and no
// others, and a provider's reported usage reaches the meter (Metabolism
// D-031, planning mode).
package ai

import (
	"context"
	"testing"

	"helix/internal/providers"
)

// thinkingFake records each request's DisableThinking and reports usage.
type thinkingFake struct {
	visionFakeProvider
	switchable bool
	seen       []bool
}

func (p *thinkingFake) ThinkingSwitch() bool { return p.switchable }
func (p *thinkingFake) Chat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	p.seen = append(p.seen, req.DisableThinking)
	ch := make(chan providers.StreamChunk, 2)
	ch <- providers.StreamChunk{Content: "ok"}
	ch <- providers.StreamChunk{Done: true, Usage: &providers.TokenUsage{Prompt: 100, CacheHit: 60, Completion: 40, Reasoning: 30}}
	close(ch)
	return ch, nil
}

func useFake(t *testing.T, p providers.AIProvider) {
	t.Helper()
	oldProvider, oldModel := activeProvider, activeModel
	activeProvider, activeModel = p, "fake-model"
	ResetUsage()
	t.Cleanup(func() {
		activeProvider, activeModel = oldProvider, oldModel
		SetPlannerThinkingOff(false)
		ResetUsage()
	})
}

func TestThinkingSwitchReachesPlannerCallsOnly(t *testing.T) {
	fake := &thinkingFake{switchable: true}
	useFake(t, fake)
	if !ThinkingSwitchable() {
		t.Fatal("a provider with the switch should be switchable")
	}

	SetPlannerThinkingOff(true)
	if _, err := runModelKind(KindPlanner, "plan", PlannerModelConfig(), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runModelKind(KindCritic, "review", DefaultModelConfig(), 0); err != nil {
		t.Fatal(err)
	}
	SetPlannerThinkingOff(false)
	if _, err := runModelKind(KindPlanner, "plan", PlannerModelConfig(), 0); err != nil {
		t.Fatal(err)
	}
	want := []bool{true, false, false}
	for i := range want {
		if fake.seen[i] != want[i] {
			t.Fatalf("DisableThinking per call = %v, want %v", fake.seen, want)
		}
	}
}

func TestReportedUsageReachesTheMeter(t *testing.T) {
	useFake(t, &thinkingFake{})
	if ThinkingSwitchable() {
		t.Fatal("a provider without the switch should not be switchable")
	}
	for range 2 {
		if _, err := runModelKind(KindPlanner, "plan", PlannerModelConfig(), 0); err != nil {
			t.Fatal(err)
		}
	}
	RecordCall(KindChat, "fake", "fake-model", "hi", "hello", 0, nil) // reports nothing
	var got providers.TokenUsage
	calls, reported := 0, 0
	for _, row := range Usage().Rows {
		got = got.Add(row.Reported)
		calls += row.Calls
		reported += row.ReportedCalls
	}
	want := providers.TokenUsage{Prompt: 200, CacheHit: 120, Completion: 80, Reasoning: 60}
	if got != want || calls != 3 || reported != 2 {
		t.Fatalf("reported %+v over %d of %d calls, want %+v over 2 of 3", got, reported, calls, want)
	}
}
