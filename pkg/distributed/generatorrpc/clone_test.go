package generatorrpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSnapshotJSONReturnsValidTypedTruncation(t *testing.T) {
	raw := snapshotJSON(&protocol.ErrorSnapshot{
		RequestMethod:  "GET",
		RequestURL:     "https://example.test/large",
		ResponseStatus: 500,
		ResponseBody:   strings.Repeat("x", 64<<10),
	})
	var snapshot protocol.ErrorSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatalf("truncated snapshot is invalid JSON: %v; raw=%q", err, raw)
	}
	if snapshot.ResponseStatus != 500 || !strings.Contains(snapshot.ResponseBody, "truncated") {
		t.Fatalf("truncated snapshot = %+v", snapshot)
	}
}
