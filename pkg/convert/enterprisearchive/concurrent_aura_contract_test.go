package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

func TestSourceConcurrentAuraContractPreservesLiteralDependency(t *testing.T) {
	body := `{"actions":[{"id":"a","descriptor":"fixture","params":{"input":"{\"literal\":\"\\u007b\\u007bvar.first\\u007d\\u007d\"}","active":"{{var.external}}"}}]}`
	contract, _, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, "", "", nil, aurapayload.MaxWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	producer := dsl.Action{Type: "GET", Target: "/producer", Extractors: []dsl.Extractor{{Name: "first", Type: "jsonpath", Expression: "$.first"}}}
	consumer := dsl.Action{Type: "POST", Target: "/aura", Body: body, Metadata: map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportAuraJSONTemplateValueEncoding}}
	plan := &sourceConcurrentPlan{workRemaining: maxSourceConcurrencyWork}
	if got := sourceConcurrentStructuredChildrenReason([]dsl.Action{producer, consumer}, plan); got != "structured_body_review" {
		t.Fatalf("unannotated decoded dependency unexpectedly admitted: %s", got)
	}
	consumer.Metadata[dsl.ImportAuraTemplateContractMetadataKey] = contract
	plan = &sourceConcurrentPlan{workRemaining: maxSourceConcurrencyWork}
	if got := sourceConcurrentStructuredChildrenReason([]dsl.Action{producer, consumer}, plan); got != "" || plan.workRemaining >= maxSourceConcurrencyWork {
		t.Fatalf("preserved literal invented a source dependency: %s", got)
	}
	contract.LiteralOccurrences[0].ContextID = "invalid"
	plan = &sourceConcurrentPlan{workRemaining: maxSourceConcurrencyWork}
	if got := sourceConcurrentStructuredChildrenReason([]dsl.Action{producer, consumer}, plan); got != "structured_body_review" {
		t.Fatal("malformed contract admitted by source scanner")
	}
}

func TestSourceConcurrentAuraContractPaddedPolicy(t *testing.T) {
	for _, policy := range []string{"literal", "active", "malformed"} {
		t.Run(policy, func(t *testing.T) {
			body := `{"actions":[{"id":"a","descriptor":"fixture","params":{"literal":"\u007b\u007bvar.first\u007d\u007d"}}]}`
			if policy == "active" {
				body = strings.ReplaceAll(body, `\u007b\u007bvar.first\u007d\u007d`, "{{var.first}}")
			}
			contract, _, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, "", "", nil, aurapayload.MaxWorkBytes)
			if err != nil {
				t.Fatal(err)
			}
			if policy == "malformed" {
				contract.LiteralOccurrences[0].ContextID = strings.Repeat("0", 64)
			}
			producer := dsl.Action{Type: "GET", Target: "/producer", Extractors: []dsl.Extractor{{Name: "first", Type: "jsonpath", Expression: "$.first"}}}
			consumer := dsl.Action{Type: "POST", Target: "/aura", Body: body, Metadata: map[string]any{
				dsl.ImportURLFormTemplateValueEncodingMetadataKey: " \t" + dsl.ImportAuraJSONTemplateValueEncoding + "\n ",
				dsl.ImportAuraTemplateContractMetadataKey:         contract,
			}}
			plan := &sourceConcurrentPlan{workRemaining: maxSourceConcurrencyWork}
			got := sourceConcurrentStructuredChildrenReason([]dsl.Action{producer, consumer}, plan)
			if (got == "") != (policy == "literal") || plan.workRemaining >= maxSourceConcurrencyWork {
				t.Fatalf("padded source policy admission: policy=%s reason=%s", policy, got)
			}
		})
	}
}
