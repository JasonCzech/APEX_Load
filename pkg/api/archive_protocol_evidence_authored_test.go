package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

// protocol_evidence the engine marked (engine.ProtocolEvidenceAuthoredRequestMarker)
// keeps its authored request-describing entries in the archive; every other
// entry, and unmarked evidence entirely, keeps the full name/shape scrub.
func TestArchiveSanitizerHonoursProtocolEvidenceAuthoredMarker(t *testing.T) {
	const (
		literal  = "lit-archive-evidence-8201"
		response = "resp-archive-evidence-8202"
	)
	if archivedEvidenceAuthoredMarker != engine.ProtocolEvidenceAuthoredRequestMarker {
		t.Fatalf("archive marker %q drifted from the engine's %q", archivedEvidenceAuthoredMarker, engine.ProtocolEvidenceAuthoredRequestMarker)
	}
	for _, marked := range []bool{true, false} {
		evidence := map[string]any{
			"protocol":  "network",
			"operation": "ldap_bind",
			"target":    "ldap://svc:" + literal + "@directory.example.test/?password=" + literal,
			"metadata": map[string]any{
				"headers":     map[string]any{"Authorization": "Bearer " + literal},
				"params":      map[string]any{"password": literal, "nested": map[string]any{"client_secret": literal}},
				"soap":        map[string]any{"action": "urn:Login"},
				"extractions": map[string]any{"session_token": response},
				"error":       "server said password=" + response,
			},
			"records": map[string]any{"password": response},
		}
		if marked {
			evidence[archivedEvidenceAuthoredMarker] = true
		}
		event := TransactionEvent{ActionName: "ldap", ProtocolEvidence: evidence}
		sanitizeArchivedTransaction(&event)
		encoded, err := json.Marshal(event.ProtocolEvidence)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		if strings.Contains(text, response) {
			t.Fatalf("marked=%v: response-derived value kept: %s", marked, text)
		}
		visible := strings.Count(text, literal)
		if marked && visible != 5 {
			t.Fatalf("marked evidence lost authored values (%d visible): %s", visible, text)
		}
		if !marked && visible != 0 {
			t.Fatalf("unmarked evidence kept authored values: %s", text)
		}
		if got, _ := event.ProtocolEvidence[archivedEvidenceAuthoredMarker].(bool); got != marked {
			t.Fatalf("marker = %v, want %v", got, marked)
		}
	}
}
