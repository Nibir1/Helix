package daemon

import (
	"encoding/json"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	req := Request{Type: TypeSubmit, Text: "list files", Channel: "voice"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var back Request
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if back.Type != TypeSubmit || back.Text != "list files" || back.Channel != "voice" {
		t.Errorf("round-trip lost fields: %+v", back)
	}
}
