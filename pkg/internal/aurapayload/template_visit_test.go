package aurapayload

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestVisitTemplatesContextualAdmissionAndAccounting(t *testing.T) {
	body := "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{"literal":"{{var.shared}}"}}]}`) + "&aura.token={{var.other}}"
	contract, _, err := PromoteTemplateContractWithWorkAccounting(body, "", "", nil, MaxWorkBytes)
	if err != nil || len(contract.LiteralOccurrences) != 1 || len(contract.FormWireOccurrences) != 1 {
		t.Fatalf("fixture contract: %v, %v", contract, err)
	}
	var literals, active int
	visit := func(value string, context TemplateContext) error {
		switch value {
		case "{{var.shared}}":
			if !context.IsLiteralToken(0) || context.IsFormWireToken(0) {
				t.Fatal("decoded literal lost its policy")
			}
			literals++
		case "{{var.other}}":
			if context.IsLiteralToken(0) || !context.IsFormWireToken(0) {
				t.Fatal("active form token lost its policy")
			}
			active++
		}
		return nil
	}
	work, err := VisitTemplatesContextualWithWorkAccounting(body, "", contract, visit, MaxWorkBytes)
	if err != nil || work <= 0 || literals != 1 || active != 1 {
		t.Fatalf("contextual visit: work=%d err=%v literals=%d active=%d", work, err, literals, active)
	}
	if exact, err := VisitTemplatesContextualWithWorkAccounting(body, "", contract, func(string, TemplateContext) error { return nil }, work); err != nil || exact != work {
		t.Fatalf("exact codec work limit: work=%d want=%d err=%v", exact, work, err)
	}
	if charged, err := VisitTemplatesContextualWithWorkAccounting(body, "", contract, func(string, TemplateContext) error { return nil }, work-1); err == nil || charged > work-1 {
		t.Fatalf("codec work ceiling bypassed: work=%d limit=%d err=%v", charged, work-1, err)
	}
	callbackErr := errors.New("stop visitor")
	if charged, err := VisitTemplatesContextualWithWorkAccounting(body, "", contract, func(string, TemplateContext) error { return callbackErr }, MaxWorkBytes); !errors.Is(err, callbackErr) || charged <= 0 {
		t.Fatal("visitor failure lost charged work or error identity")
	}
	contract.LiteralOccurrences[0].ContextID = strings.Repeat("0", 64)
	calls := 0
	if _, err := VisitTemplatesContextualWithWorkAccounting(body, "", contract, func(string, TemplateContext) error { calls++; return nil }, MaxWorkBytes); err == nil || calls != 0 {
		t.Fatal("stale occurrence invoked a visitor")
	}
}

func TestVisitTemplatesContextualKeepsPayloadCeilings(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("x", MaxBodyBytes+1),
		`{"actions":[{"id":"a","descriptor":"fixture","params":{"literal":"` + strings.Repeat("{{var.x}}", MaxTemplateOccurrences+1) + `"}}]}`,
	} {
		calls := 0
		work, err := VisitTemplatesContextualWithWorkAccounting(body, "", nil, func(string, TemplateContext) error { calls++; return nil }, MaxWorkBytes)
		if err == nil || calls != 0 || work > MaxWorkBytes {
			t.Fatalf("payload ceiling bypassed: work=%d err=%v calls=%d", work, err, calls)
		}
	}
}
