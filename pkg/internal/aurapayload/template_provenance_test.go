package aurapayload

import (
	"strings"
	"testing"
)

func TestAuraPromotionSparseOriginalWireProvenance(t *testing.T) {
	direct := `{"actions":[{"id":"a","descriptor":"fixture","params":{"active":"{{var.shared}}","literal":"\u007b\u007bvar.shared\u007d\u007d","input":"{\"literal\":\"\\u007b\\u007bsecret.LITERAL_ONLY\\u007d\\u007d\"}"}}]}`
	contract, work, err := PromoteTemplateContractWithWorkAccounting(direct, "", "", nil, MaxWorkBytes)
	if err != nil || len(contract.FormWireOccurrences) != 0 || len(contract.LiteralOccurrences) != 2 {
		t.Fatalf("direct original-wire policy: work=%d err=%v", work, err)
	}
	var active, literal int
	_, _, _, err = ResolveContextualWithWorkAccounting(direct, "", contract, func(value string, context TemplateContext) (string, string) {
		if strings.HasPrefix(value, "{{") {
			if context.IsLiteralToken(0) {
				literal++
			} else {
				active++
			}
		}
		return value, value
	}, MaxWorkBytes)
	if err != nil || active != 1 || literal != 2 {
		t.Fatalf("same-token mixed provenance: active=%d literal=%d err=%v", active, literal, err)
	}
	if _, bounded, err := PromoteTemplateContractWithWorkAccounting(direct, "", "", nil, work-1); err == nil || bounded > work {
		t.Fatal("provenance work allowance ignored")
	}
	unsupported := `{"actions":[{"id":"a","descriptor":"fixture","params":{"{{var.dynamic_key}}":"business"}}]}`
	if _, _, err := PromoteTemplateContractWithWorkAccounting(unsupported, "", "", nil, MaxWorkBytes); err == nil {
		t.Fatal("unattributed raw key template silently froze during promotion")
	}
}
