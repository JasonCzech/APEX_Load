package importreview

import (
	"encoding/json"
	"fmt"
	"testing"
)

func largeStructuredCredentialBody(t *testing.T, password any) string {
	t.Helper()
	items := make([]string, 160)
	for index := range items {
		items[index] = fmt.Sprintf("ordinary_payload_candidate_%06d", index)
	}
	payload, err := json.Marshal(map[string]any{"password": password, "count": 1, "items": items})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestStructuredImportPayloadProofBudgetsDoNotResetForNestedDocuments(t *testing.T) {
	body := largeStructuredCredentialBody(t, "fixture-password")
	for _, test := range []struct {
		name  string
		proof StructuredImportPayloadProof
	}{
		{name: "byte limit", proof: StructuredImportPayloadProof{remainingBytes: len(body) - 1, remainingValues: 1000}},
		{name: "value limit", proof: StructuredImportPayloadProof{remainingBytes: len(body), remainingValues: 10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if ExactStructuredImportPayload([]byte(body), "application/json", 0, &test.proof) {
				t.Fatal("structured recovery exceeded its shared proof budget")
			}
		})
	}
	proof := &StructuredImportPayloadProof{remainingBytes: len(body) - 1, remainingValues: 1000}
	if ExactStructuredImportScalar(body, 0, proof) {
		t.Fatal("nested document reset the parent byte budget")
	}
}
