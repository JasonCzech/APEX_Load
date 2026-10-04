package api

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

func TestImportDraftStructureDigestTreatsOnlyKnownProjectionMarkerAsTransient(t *testing.T) {
	for _, layout := range []string{"top_level", "control_loop", "http_sequence"} {
		t.Run(layout, func(t *testing.T) {
			request := dsl.Action{
				Name: "Synthetic request", Type: "POST", Method: "POST", Target: "/synthetic", Body: `{"ordinary":"value"}`,
				Metadata: map[string]any{
					"execution_blocker":  "source_review_required",
					"execution_blockers": []string{"source_review_required"},
					"import_review":      map[string]any{"version": 1, "source": "synthetic", "reason_codes": []string{"source_fidelity_review"}},
				},
			}
			script := &dsl.Script{Name: "Synthetic source", Version: "1", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}}
			switch layout {
			case "top_level":
				script.Actions = []dsl.Action{request}
			case "control_loop":
				script.Actions = []dsl.Action{{Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{request}}}}
			case "http_sequence":
				script.Actions = []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{request}}}}
			}
			ensureCredentialPreservingImportMetadata(script, "jmeter")
			baseline, err := canonicalImportDraftStructureDigest(script)
			if err != nil {
				t.Fatal(err)
			}
			withMarker := cloneScriptDefinition(script)
			if err := dslwalk.Walk(withMarker, func(ref dslwalk.SurfaceRef) error {
				metadata := ref.Metadata()
				if metadata == nil {
					return nil
				}
				metadata[scriptProjectionResponseGateMetadataKey] = map[string]any{"version": 1}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(withMarker)
			marked, err := canonicalImportDraftStructureDigest(withMarker)
			if err != nil || marked != baseline {
				t.Fatalf("same source with transient response marker changed structural receipt: error=%v equal=%t", err, marked == baseline)
			}
			after, _ := json.Marshal(withMarker)
			if !bytes.Equal(before, after) {
				t.Fatal("receipt comparison mutated source metadata")
			}
			baselineExact, _ := fullImportDraftExecutionDigest(script)
			markedExact, _ := fullImportDraftExecutionDigest(withMarker)
			if baselineExact == markedExact {
				t.Fatal("exact execution attestation ignored marker mutation")
			}
			for _, change := range []string{"unknown_marker", "source_gate", "removed_gate", "source_review", "removed_review", "source_body"} {
				changed := cloneScriptDefinition(withMarker)
				if err := dslwalk.Walk(changed, func(ref dslwalk.SurfaceRef) error {
					metadata := ref.Metadata()
					if metadata == nil {
						return nil
					}
					switch change {
					case "unknown_marker":
						metadata[scriptProjectionResponseGateMetadataKey] = map[string]any{"version": 2}
					case "source_gate":
						metadata["execution_blocker"] = "changed_source_gate"
					case "removed_gate":
						delete(metadata, "execution_blocker")
						delete(metadata, "execution_blockers")
					case "removed_review":
						delete(metadata, "import_review")
					case "source_review":
						metadata["import_review"] = map[string]any{"version": 1, "source": "synthetic", "reason_codes": []string{"changed_source_fidelity"}}
					case "source_body":
						if ref.Action != nil {
							ref.Action.Body = `{"ordinary":"changed"}`
						} else {
							ref.Request["body"] = `{"ordinary":"changed"}`
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				digest, err := canonicalImportDraftStructureDigest(changed)
				if err != nil || digest == baseline {
					t.Fatalf("%s did not retain source distinction: error=%v equal=%t", change, err, digest == baseline)
				}
			}
		})
	}
}
