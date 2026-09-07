package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"testing"
)

func TestImportResponsePreservesDiagnosticSeverity(t *testing.T) {
	diagnostics := diag.Diagnostics{{Element: "headers", Severity: diag.Info, Detail: "source_order_unavailable"}, {Element: "unsupported feature", Severity: diag.Blocker, Detail: "No runtime adapter"}}
	response := newScriptImportResponse(scriptImportResponseOptions{Diagnostics: diagnostics, UnsupportedElements: diagnostics.Strings()})
	data, err := json.Marshal(response["diagnostics"])
	if err != nil {
		t.Fatal(err)
	}
	var got diag.Diagnostics
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Severity != diag.Info || got[1].Severity != diag.Blocker {
		t.Fatalf("severity lost: %+v", got)
	}
}
