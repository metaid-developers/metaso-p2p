package agentpedia

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDumpViewsForCrossDiff (scratch harness) dumps the Go view of every
// pipeline vector to /tmp/views-go.json so the two implementations can be
// diffed mechanically. Neutralized to a no-op by default; enable by setting
// AGENTPEDIA_DUMP_VIEWS=1.
func TestDumpViewsForCrossDiff(t *testing.T) {
	if os.Getenv("AGENTPEDIA_DUMP_VIEWS") == "" {
		t.Skip("set AGENTPEDIA_DUMP_VIEWS=1 to dump")
	}
	f := loadVectors(t)
	out := map[string]any{}
	for _, vec := range f.Vectors {
		events, opts := f.buildEvents(vec)
		view := Replay(events, opts)
		normalized, err := json.Marshal(view)
		if err != nil {
			t.Fatalf("%s: %v", vec.ID, err)
		}
		var generic any
		_ = json.Unmarshal(normalized, &generic)
		out[vec.ID] = generic
	}
	blob, _ := json.MarshalIndent(out, "", " ")
	if err := os.WriteFile("/tmp/views-go.json", blob, 0o644); err != nil {
		t.Fatalf("write dump: %v", err)
	}
	t.Logf("wrote %d views", len(out))
}
