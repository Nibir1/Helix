// internal/daemon/fuzz_test.go
// Purpose: Phase 7 (P7.5) — fuzz the NDJSON IPC message parser. Invariant:
// decoding/marshaling an arbitrary Request byte stream never panics.
package daemon

import (
	"encoding/json"
	"testing"
)

func FuzzRequestJSON(f *testing.F) {
	f.Add([]byte(`{"type":"status"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"type":"submit","text":"ls -la","channel":"voice","meta":{"x":1}}`))
	f.Add([]byte(`not json at all`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var req Request
		_ = json.Unmarshal(data, &req)
		_, _ = json.Marshal(req)
	})
}
