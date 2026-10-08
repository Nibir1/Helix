// internal/providers/openai_compatible/usage_test.go
// Purpose: reported token usage and the thinking switch (Metabolism D-031,
// planning mode). DeepSeek sends the usage frame after the finish frame, so a
// parser that stops at the finish never sees it.
package openaicompatible

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"helix/internal/providers"
)

// deepseekStream answers as DeepSeek does with include_usage: content, a
// finish frame, then a frame with no choices and the usage.
const deepseekStream = "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hm\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4600,\"completion_tokens\":900,\"prompt_cache_hit_tokens\":3800,\"prompt_cache_miss_tokens\":800,\"completion_tokens_details\":{\"reasoning_tokens\":780}}}\n\n" +
	"data: [DONE]\n\n"

func recordingServer(t *testing.T, stream string, seen *[]map[string]any, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*seen = append(*seen, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func deepseekLike(url string) *Provider {
	return New(Config{Name: "deepseek", DisplayName: "DeepSeek", BaseURL: url, APIKey: "test",
		DefaultModel: "deepseek-v4.1-flash", ReportsUsage: true, ThinkingSwitch: true},
		providers.NewHTTPClient(10*1e9))
}

func TestUsageFrameAfterFinishIsReported(t *testing.T) {
	var seen []map[string]any
	var mu sync.Mutex
	p := deepseekLike(recordingServer(t, deepseekStream, &seen, &mu).URL)
	res, err := providers.CollectChatResult(context.Background(), p, providers.ChatRequest{
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "ok" {
		t.Fatalf("text = %q, want ok (reasoning content is not the answer)", res.Text)
	}
	want := providers.TokenUsage{Prompt: 4600, CacheHit: 3800, Completion: 900, Reasoning: 780}
	if res.Usage == nil || *res.Usage != want {
		t.Fatalf("usage = %+v, want %+v", res.Usage, want)
	}
	opts, _ := seen[0]["stream_options"].(map[string]any)
	if opts["include_usage"] != true {
		t.Fatalf("stream_options = %v, want include_usage", seen[0]["stream_options"])
	}
	if _, ok := seen[0]["thinking"]; ok {
		t.Fatalf("thinking sent without DisableThinking: %v", seen[0]["thinking"])
	}
}

func TestDisableThinkingIsSentOnlyWhereItWorks(t *testing.T) {
	var seen []map[string]any
	var mu sync.Mutex
	srv := recordingServer(t, deepseekStream, &seen, &mu)
	req := providers.ChatRequest{Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}}, DisableThinking: true}

	if _, err := providers.CollectChatResult(context.Background(), deepseekLike(srv.URL), req); err != nil {
		t.Fatal(err)
	}
	th, _ := seen[0]["thinking"].(map[string]any)
	if th["type"] != "disabled" {
		t.Fatalf("thinking = %v, want type disabled", seen[0]["thinking"])
	}

	plain := newProvider(t, "groq", "llama", srv.URL)
	if _, err := providers.CollectChatResult(context.Background(), plain, req); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"thinking", "stream_options"} {
		if _, ok := seen[1][k]; ok {
			t.Fatalf("%s sent to a provider without the option: %v", k, seen[1][k])
		}
	}
	if providers.CanSwitchThinking(plain) || !providers.CanSwitchThinking(deepseekLike(srv.URL)) {
		t.Fatal("CanSwitchThinking should follow the config")
	}
}

// A stream that ends after the finish frame with no [DONE] is still a whole
// answer, and one without the usage option still ends at the finish frame.
func TestStreamEndsWithoutDone(t *testing.T) {
	var seen []map[string]any
	var mu sync.Mutex
	noDone := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"
	srv := recordingServer(t, noDone, &seen, &mu)
	for _, p := range []*Provider{deepseekLike(srv.URL), newProvider(t, "groq", "llama", srv.URL)} {
		res, err := providers.CollectChatResult(context.Background(), p, providers.ChatRequest{
			Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}}})
		if err != nil || res.Text != "ok" || res.Usage != nil {
			t.Fatalf("%s: text %q usage %+v err %v", p.Name(), res.Text, res.Usage, err)
		}
	}
}
