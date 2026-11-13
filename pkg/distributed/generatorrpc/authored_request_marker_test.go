package generatorrpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The metrics-transport truncation keeps the engine's authored-request marker so
// the controller's archive sanitizer does not re-mask what the script authored.
func TestSnapshotJSONTruncationKeepsAuthoredRequestMarker(t *testing.T) {
	snapshot := &protocol.ErrorSnapshot{
		AuthoredRequestRetained: true,
		RequestMethod:           "POST",
		RequestURL:              "https://api.example.test/login",
		ResponseBody:            strings.Repeat("x", 64<<10),
		HTML:                    strings.Repeat("y", 64<<10),
	}
	var decoded protocol.ErrorSnapshot
	if err := json.Unmarshal([]byte(snapshotJSON(snapshot)), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.AuthoredRequestRetained {
		t.Fatalf("marker lost in transport projection: %+v", decoded)
	}
}
