package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildProtocolEvidenceSkipsOversizedJSONPayload(t *testing.T) {
	body := `{"row_count":1,"rows":[],"padding":"` + strings.Repeat("x", maxEvidencePayloadBytes) + `"}`
	evidence := BuildProtocolEvidence(&Action{Type: "db_query"}, &Result{
		Success:        true,
		DependencyType: "database",
		ResponseBody:   body,
	})
	if evidence == nil || evidence["protocol"] != "database" {
		t.Fatalf("evidence = %#v, want database metadata", evidence)
	}
	if _, exists := evidence["rows"]; exists {
		t.Fatalf("oversized response body was decoded into retained rows: %#v", evidence["rows"])
	}
}

func TestBoundProtocolEvidenceDetachesAndBoundsCollections(t *testing.T) {
	nested := map[string]any{"value": "before"}
	bounded := BoundProtocolEvidence(map[string]any{
		"large":  strings.Repeat("x", maxEvidenceStringBytes+100),
		"nested": nested,
	})
	if got, _ := bounded["large"].(string); len(got) != maxEvidenceStringBytes {
		t.Fatalf("bounded string length = %d, want %d", len(got), maxEvidenceStringBytes)
	}
	nested["value"] = "after"
	boundedNested, ok := bounded["nested"].(map[string]any)
	if !ok || boundedNested["value"] != "before" {
		t.Fatalf("bounded nested evidence aliases input: %#v", bounded["nested"])
	}

	many := make(map[string]any, maxEvidenceCollection+50)
	for i := 0; i < maxEvidenceCollection+50; i++ {
		many[fmt.Sprintf("key-%03d", i)] = i
	}
	boundedMany := BoundProtocolEvidence(many)
	if len(boundedMany) > maxEvidenceCollection {
		t.Fatalf("bounded map size = %d, limit %d", len(boundedMany), maxEvidenceCollection)
	}

	list := make([]any, maxEvidenceCollection+50)
	for i := range list {
		list[i] = strings.Repeat("v", 32)
	}
	boundedList := BoundProtocolEvidence(map[string]any{"list": list})
	values, ok := boundedList["list"].([]any)
	if !ok || len(values) > maxEvidenceCollection {
		t.Fatalf("bounded list = %#v", boundedList["list"])
	}
}
