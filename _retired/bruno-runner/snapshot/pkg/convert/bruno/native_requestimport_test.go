package bruno

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestFinalizeNativeRequestActionPreservesBrunoV1Projection(t *testing.T) {
	t.Parallel()

	redirects := 7
	scaffold := dsl.Action{
		Name:   "Create",
		Type:   "POST",
		Method: "POST",
		Target: "https://example.test/items?z=1&z=2",
		Headers: map[string]string{
			"X-Trace": "first",
			"x-trace": "second",
		},
		HeaderList: dsl.HeaderList{
			{Name: "X-Trace", Value: "first"},
			{Name: "x-trace", Value: "second"},
		},
		Body:         "<root a=\"1\">\n  <value>{{secret.API_TOKEN}}</value>\n</root>",
		Auth:         &dsl.AuthConfig{Type: "digest", Username: "{{secret.USER}}", Password: "{{secret.PASS}}"},
		TimeoutMs:    45_000,
		MaxRedirects: &redirects,
		Assertions: []dsl.Assertion{
			{Type: "status_code", Expected: 201},
		},
		Metadata: map[string]any{
			"source_profile": "bruno_native",
			"source_step_id": "request-uid",
			"source_path":    "folder/create.yml",
		},
	}

	action, report, err := finalizeNativeRequestAction(
		scaffold,
		CollectionItem{UID: "request-uid", Path: "folder/create.yml"},
		sourceFile{Path: "collection/folder/create.yml"},
		"xml",
	)
	if err != nil {
		t.Fatalf("finalize native request action: %v", err)
	}
	if report.Disposition != requestimport.ExecutableExact {
		t.Fatalf("disposition = %q, want %q", report.Disposition, requestimport.ExecutableExact)
	}
	if !reflect.DeepEqual(action, scaffold) {
		got, _ := json.MarshalIndent(action, "", "  ")
		want, _ := json.MarshalIndent(scaffold, "", "  ")
		t.Fatalf("shared request projection changed Bruno v1 action\n got: %s\nwant: %s", got, want)
	}
}

func TestFinalizeNativeRequestActionPreservesReviewOnlyAuthContract(t *testing.T) {
	t.Parallel()

	redirects := nativeDefaultRedirects
	scaffold := dsl.Action{
		Name:         "NTLM review",
		Type:         "GET",
		Method:       "GET",
		Target:       "https://example.test",
		HeaderList:   dsl.HeaderList{},
		Auth:         &dsl.AuthConfig{Type: "ntlm", Username: "{{secret.USER}}", Domain: "EXAMPLE"},
		TimeoutMs:    nativeDefaultTimeoutMS,
		MaxRedirects: &redirects,
		Metadata: map[string]any{
			"source_profile":  "bruno_native",
			"review_required": true,
			"review_gate":     "bruno_auth",
			"review_reasons":  []string{"bruno_auth_review_required"},
		},
	}

	action, report, err := finalizeNativeRequestAction(
		scaffold,
		CollectionItem{UID: "ntlm-uid", Path: "ntlm.yml"},
		sourceFile{Path: "collection/ntlm.yml"},
		"",
	)
	if err != nil {
		t.Fatalf("finalize review-only native request action: %v", err)
	}
	if report.Disposition != requestimport.ExecutableExact {
		// Bruno's existing durable gate is intentionally authored before the
		// shared boundary, so the generic report does not add a second finding.
		t.Fatalf("generic disposition = %q, want compatibility value %q", report.Disposition, requestimport.ExecutableExact)
	}
	if !reflect.DeepEqual(action, scaffold) {
		got, _ := json.MarshalIndent(action, "", "  ")
		want, _ := json.MarshalIndent(scaffold, "", "  ")
		t.Fatalf("shared request projection changed review-only Bruno action\n got: %s\nwant: %s", got, want)
	}
	if _, exists := action.Metadata["execution_blockers"]; exists {
		t.Fatal("Bruno C2 migration must not add a second generic execution gate")
	}
}

func TestFinalizeNativeRequestActionIsDeterministic(t *testing.T) {
	t.Parallel()

	redirects := nativeDefaultRedirects
	scaffold := dsl.Action{
		Name:         "Stable",
		Type:         "PATCH",
		Method:       "PATCH",
		Target:       "https://example.test/{{param.id}}",
		Headers:      map[string]string{"Accept": "application/json"},
		HeaderList:   dsl.HeaderList{{Name: "Accept", Value: "application/json"}},
		Body:         `{"enabled":true,"count":7}`,
		TimeoutMs:    nativeDefaultTimeoutMS,
		MaxRedirects: &redirects,
		Metadata:     map[string]any{"source_profile": "bruno_native"},
	}

	var baseline []byte
	for iteration := 0; iteration < 25; iteration++ {
		action, report, err := finalizeNativeRequestAction(
			scaffold,
			CollectionItem{UID: "stable-uid", Path: "stable.yml"},
			sourceFile{Path: "collection/stable.yml"},
			"json",
		)
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		encoded, err := json.Marshal(struct {
			Action dsl.Action           `json:"action"`
			Report requestimport.Report `json:"report"`
		}{Action: action, Report: report})
		if err != nil {
			t.Fatalf("iteration %d marshal: %v", iteration, err)
		}
		if iteration == 0 {
			baseline = encoded
			continue
		}
		if string(encoded) != string(baseline) {
			t.Fatalf("iteration %d produced a non-deterministic shared projection", iteration)
		}
	}
}
