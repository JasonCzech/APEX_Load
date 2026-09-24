package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
)

func TestJMeterReviewResponseUsesNormalizedEvidence(t *testing.T) {
	source, err := os.ReadFile("../convert/jmeter/testdata/split_pay_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	server, store := newAuditTestServer(t)
	defer store.Close()
	for _, tc := range []struct {
		name, query string
		blockers    int
	}{{"original", "", 2}, {"UTC", "&time_mode=utc", 1}, {"bindings", "&time_mode=utc&variable_bindings=" + url.QueryEscape(`{"client_id":"cid","client_secret":"cs"}`), 2}} {
		t.Run(tc.name, func(t *testing.T) {
			var previews []map[string]json.RawMessage
			for _, route := range []string{"jmeter", "jmeter/validate"} {
				request := httptest.NewRequest("POST", "/api/v1/scripts/import/"+route+"?project_id=unassigned&filename=source.jmx&scenario_draft=true"+tc.query, bytes.NewReader(source))
				request.Header.Set("Content-Type", "application/xml")
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, request)
				if response.Code != 200 {
					t.Fatalf("status=%d %s", response.Code, response.Body.String())
				}
				var body map[string]json.RawMessage
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				var summary jmeter.ReviewSummary
				json.Unmarshal(body["review_summary"], &summary)
				if summary.BlockingBehaviors != tc.blockers || summary.UnsupportedBehaviors != 0 {
					t.Fatalf("summary=%+v diagnostics=%s", summary, body["diagnostics"])
				}
				var diagnostics diag.Diagnostics
				json.Unmarshal(body["diagnostics"], &diagnostics)
				n := 0
				for _, d := range diagnostics {
					if d.Severity == diag.Blocker {
						n++
					}
					if d.Code == "" || d.SourceRef == "" {
						t.Fatalf("unidentified diagnostic=%+v", d)
					}
				}
				if n != tc.blockers {
					t.Fatalf("blocker diagnostics=%d", n)
				}
				var unsupported []string
				json.Unmarshal(body["unsupported_elements"], &unsupported)
				if len(unsupported) != 0 {
					t.Fatal(unsupported)
				}
				var nested map[string]json.RawMessage
				json.Unmarshal(body["result"], &nested)
				for _, key := range []string{"diagnostics", "review_summary", "unsupported_elements"} {
					if !sameReviewJSON(body[key], nested[key]) {
						t.Fatalf("nested %s differs: top %s nested %s", key, body[key], nested[key])
					}
				}
				var p ReplayPreflight
				json.Unmarshal(body["preflight"], &p)
				if p.Valid {
					t.Fatal("source became ready")
				}
				previews = append(previews, body)
			}
			for _, key := range []string{"diagnostics", "review_summary", "unsupported_elements", "preflight"} {
				if !sameReviewJSON(previews[0][key], previews[1][key]) {
					t.Fatalf("import/validate %s differs", key)
				}
			}
		})
	}
}

func TestJMeterReviewZIPPreservesSourceIdentities(t *testing.T) {
	source, err := os.ReadFile("../convert/jmeter/testdata/split_pay_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	r, err := convertJMeterImportRequest(&jmeterImportRequest{Documents: []jmeterImportDocument{{FileName: "a/plan.jmx", Data: source}, {FileName: "b/plan.jmx", Data: source}}, Opts: jmeter.ConvertOptions{ScenarioDraft: true}})
	if err != nil {
		t.Fatal(err)
	}
	if r.ReviewSummary.BlockingBehaviors != 4 {
		t.Fatalf("summary=%+v", r.ReviewSummary)
	}
	sources := map[string]bool{}
	for _, o := range r.ReviewOutcomes {
		sources[o.Source] = true
	}
	if len(sources) != 2 {
		t.Fatalf("sources=%v", sources)
	}
}

func sameReviewJSON(a, b json.RawMessage) bool {
	var x, y any
	json.Unmarshal(a, &x)
	json.Unmarshal(b, &y)
	return reflect.DeepEqual(x, y)
}

func TestJMeterReviewEvidenceSurvivesAttestedSaveAndReload(t *testing.T) {
	source, err := os.ReadFile("../convert/jmeter/testdata/split_pay_repaired.jmx")
	if err != nil {
		t.Fatal(err)
	}
	server, store := newAuditTestServer(t)
	defer store.Close()
	request := httptest.NewRequest("POST", "/api/v1/scripts/import/jmeter?project_id=unassigned&filename=repaired.jmx&scenario_draft=true&time_mode=utc", bytes.NewReader(source))
	request.Header.Set("Content-Type", "application/xml")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("import=%d %s", response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	json.Unmarshal(response.Body.Bytes(), &body)
	var summary jmeter.ReviewSummary
	json.Unmarshal(body["review_summary"], &summary)
	if summary.BlockingBehaviors != 0 {
		t.Fatalf("repaired blocked=%+v", summary)
	}
	payload, err := json.Marshal(map[string]any{"project_id": "unassigned", "scripts": body["scripts"], "scenario_draft": body["scenario_draft"]})
	if err != nil {
		t.Fatal(err)
	}
	saved := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", payload, "")
	if saved.Code != 201 {
		t.Fatalf("save=%d %s", saved.Code, saved.Body.String())
	}
	var result struct {
		Scripts []StoredScript `json:"scripts"`
	}
	json.Unmarshal(saved.Body.Bytes(), &result)
	if len(result.Scripts) != 1 {
		t.Fatalf("saved=%s", saved.Body.String())
	}
	reloaded, err := store.GetScript(result.Scripts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := json.Marshal(reloaded.Metadata.Import.Preserved["jmeter_review_summary"])
	if err != nil {
		t.Fatal(err)
	}
	if !sameReviewJSON(evidence, body["review_summary"]) {
		t.Fatalf("summary lost: %s", evidence)
	}
	diagnostics, _ := json.Marshal(reloaded.Metadata.Import.Preserved["jmeter_review_diagnostics"])
	if !sameReviewJSON(diagnostics, body["diagnostics"]) {
		t.Fatalf("diagnostics lost: %s", diagnostics)
	}
}
