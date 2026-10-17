package recordbridge

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

func portableDraft(id string) *dsl.Script {
	return &dsl.Script{ID: id, Name: "import", Protocol: "http", ProjectID: "project-a", Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "sample", Preserved: map[string]any{}}}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/", HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer imported-value"}}}}}
}
func TestMemoryDraftPortsResponseAndMutationUseSharedOwner(t *testing.T) {
	owner := importreview.NewMemoryDraftOwner()
	ports := NewMemoryDraftPorts(owner, nil)
	request := httptest.NewRequest(http.MethodPost, "/import", nil)
	script := portableDraft("draft")
	response := ports.Response(request, importreview.ScriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}})
	if response["script"] == nil || response["import_draft_expires_at"] == nil || owner.Len() != 1 {
		t.Fatalf("response did not attach exactly one capability: %#v %d", response, owner.Len())
	}
	original := importreview.CloneScriptDefinition(script)
	claim, err := ports.Claim(request, script)
	if err != nil || claim == nil || claim.Script.Actions[0].HeaderList[0].Value != "Bearer imported-value" {
		t.Fatalf("claim = %#v %v", claim, err)
	}
	if _, err := ports.Claim(request, script); !errors.Is(err, importreview.ErrDraftAttestationBusy) {
		t.Fatalf("duplicate claim = %v", err)
	}
	if _, err := claim.Complete(request, claim.Script); err != nil {
		t.Fatal(err)
	}
	claim.Release()
	if _, err := ports.Claim(request, original); !errors.Is(err, importreview.ErrDraftAttestationInvalid) {
		t.Fatalf("consumed claim = %v", err)
	}
	next, err := ports.Claim(request, claim.Script)
	if err != nil || next == nil {
		t.Fatalf("replacement = %#v %v", next, err)
	}
	next.Release()
}
func TestMemoryDraftPortsBatchFailureRollsBackAndWritesRetryableResponse(t *testing.T) {
	owner := importreview.NewMemoryDraftOwner(importreview.MemoryDraftOptions{Capacity: 1})
	ports := NewMemoryDraftPorts(owner, nil)
	first, second := portableDraft("first"), portableDraft("second")
	response := ports.Response(httptest.NewRequest(http.MethodPost, "/import", nil), importreview.ScriptImportResponseOptions{Scripts: []*dsl.Script{first, second}})
	if owner.Len() != 0 || importreview.ImportDraftAttestationToken(first) != "" || importreview.ImportDraftAttestationToken(second) != "" {
		t.Fatal("failed batch kept partial capabilities")
	}
	out := httptest.NewRecorder()
	WriteMemoryDraftResponse(out, http.StatusOK, response)
	if out.Code != http.StatusServiceUnavailable || out.Header().Get("Retry-After") == "" || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("failure response = %d %#v %s", out.Code, out.Header(), out.Body)
	}
}
