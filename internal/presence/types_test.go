package presence

import (
	"encoding/json"
	"testing"
)

func TestPresenceTypesOnlineEntryJSONOmitEmptyFederationFields(t *testing.T) {
	entry := OnlineEntry{
		MetaId:      "meta-1",
		Type:        "pc",
		ConnectedAt: 1710000000000,
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal online entry: %v", err)
	}

	want := `{"metaid":"meta-1","type":"pc","connectedAt":1710000000000}`
	if string(raw) != want {
		t.Fatalf("unexpected json:\nwant: %s\n got: %s", want, raw)
	}
}

func TestPresenceTypesOnlineEntryJSONIncludesFederationFields(t *testing.T) {
	entry := OnlineEntry{
		MetaId:        "meta-1",
		Type:          "app",
		ConnectedAt:   1710000000000,
		LastSeenAt:    1710000000500,
		SourceNodeIds: []string{"node-a", "node-b"},
		Sources:       2,
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal online entry: %v", err)
	}

	want := `{"metaid":"meta-1","type":"app","connectedAt":1710000000000,"lastSeenAt":1710000000500,"sourceNodeIds":["node-a","node-b"],"sources":2}`
	if string(raw) != want {
		t.Fatalf("unexpected json:\nwant: %s\n got: %s", want, raw)
	}
}
