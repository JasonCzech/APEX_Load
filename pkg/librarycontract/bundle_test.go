package librarycontract

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBundleTransferPreservesNestedPayloads(t *testing.T) {
	const raw = `{"schema_version":"apexload.library_bundle.v1","bundle_id":"b","created_at":"2026-10-17T00:00:00Z","source":{"instance_id":"source"},"items":[{"kind":"scenario","scenario":{"request":{"future_policy":{"id":9007199254740993,"explicit_zero":0,"empty":[],"null":null},"groups":[]}}},{"kind":"result","result":{"test":{"history":[{"total_transactions":9007199254740993}]},"archive":{"metadata":{"future":"retained"}}}}],"provenance":{"nested":{"decimal":1.2300,"empty":{}},"number":18446744073709551615}}`
	var bundle LibraryBundle
	if err := json.Unmarshal([]byte(raw), &bundle); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(LibraryImportRequest{Bundle: bundle, ProjectID: "destination"})
	if err != nil {
		t.Fatal(err)
	}
	var forwarded struct {
		Bundle    json.RawMessage `json:"bundle"`
		ProjectID string          `json:"project_id"`
	}
	if err := json.Unmarshal(encoded, &forwarded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forwarded.Bundle, []byte(raw)) || forwarded.ProjectID != "destination" {
		t.Fatalf("bundle changed: %s", encoded)
	}
}

func TestBundleFileAdmissionRetainsEnvelopeBounds(t *testing.T) {
	for _, raw := range []string{`{"unknown":true}`, `{"items":[`, `{"source":{"unknown":true}}`} {
		var bundle LibraryBundle
		decoder := json.NewDecoder(bytes.NewBufferString(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&bundle) == nil {
			t.Fatalf("invalid envelope accepted: %s", raw)
		}
	}
}
