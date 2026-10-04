package dsl

import (
	"encoding/base64"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
)

func concurrentAuraContractBody(t *testing.T, representation string, active bool) Action {
	t.Helper()
	body := `{"actions":[{"id":"a","descriptor":"fixture","params":{"literal":"{{var.first}}"}}]}`
	encoding := ""
	if representation == "direct JSON" {
		if !active {
			body = strings.ReplaceAll(body, "{{var.first}}", `\u007b\u007bvar.first\u007d\u007d`)
		}
	} else {
		body = "message=" + url.QueryEscape(body)
		if active {
			body = strings.ReplaceAll(body, url.QueryEscape("{{var.first}}"), "{{var.first}}")
		}
		if representation == "base64 form" {
			body = base64.StdEncoding.EncodeToString([]byte(body))
			encoding = "base64"
		}
	}
	contract, _, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, encoding, "", nil, aurapayload.MaxWorkBytes)
	if err != nil || (len(contract.LiteralOccurrences) == 1) == active {
		t.Fatalf("fixture occurrence contract: %v, %v", contract, err)
	}
	return Action{Type: "POST", Target: "/aura", Body: body, BodyEncoding: encoding, Metadata: map[string]any{
		ImportURLFormTemplateValueEncodingMetadataKey: ImportAuraJSONTemplateValueEncoding,
		ImportAuraTemplateContractMetadataKey:         contract,
	}}
}

func TestHTTPConcurrentAuraDependencyContracts(t *testing.T) {
	producer := Action{Type: "GET", Target: "/producer", Extractors: []Extractor{{Name: "first", Type: "jsonpath", Expression: "$.first"}}}
	for _, representation := range []string{"plain form", "base64 form", "direct JSON"} {
		for _, active := range []bool{false, true} {
			name := representation + "/literal"
			if active {
				name = representation + "/active"
			}
			t.Run(name, func(t *testing.T) {
				consumer := concurrentAuraContractBody(t, representation, active)
				work, err := ValidateHTTPConcurrentChildrenWithWorkLimit([]Action{producer, consumer}, MaxHTTPConcurrentPreparationWork)
				if work <= 0 || (err != nil) != active || active && !strings.Contains(err.Error(), "group extractor outputs") {
					t.Fatalf("dependency admission: work=%d err=%v active=%t", work, err, active)
				}
				if !active {
					if exact, err := ValidateHTTPConcurrentChildrenWithWorkLimit([]Action{producer, consumer}, work); err != nil || exact != work {
						t.Fatalf("exact work admission: work=%d want=%d err=%v", exact, work, err)
					}
					if charged, err := ValidateHTTPConcurrentChildrenWithWorkLimit([]Action{producer, consumer}, work-1); err == nil || charged > work-1 {
						t.Fatalf("work ceiling bypassed: work=%d limit=%d err=%v", charged, work-1, err)
					}
				}
			})
		}
		t.Run(representation+"/malformed contract", func(t *testing.T) {
			consumer := concurrentAuraContractBody(t, representation, false)
			consumer.Metadata[ImportAuraTemplateContractMetadataKey].(*aurapayload.TemplateContract).LiteralOccurrences[0].ContextID = strings.Repeat("0", 64)
			if _, err := ValidateHTTPConcurrentChildrenWithWorkLimit([]Action{producer, consumer}, MaxHTTPConcurrentPreparationWork); err == nil {
				t.Fatal("stale literal contract was admitted")
			}
		})
	}
}

func TestHTTPConcurrentStructuralBodyRetainsSourceAdmission(t *testing.T) {
	action := concurrentAuraContractBody(t, "plain form", false)
	ordinary := &httpConcurrentSourceBudget{remaining: MaxHTTPConcurrentPreparationBytes, nodes: 32768, workRemaining: MaxHTTPConcurrentPreparationWork}
	structural := *ordinary
	var rawBodyVisits, structuralBodyVisits int
	if err := ordinary.walk(reflect.ValueOf(action), 0, func(value string) error {
		if value == action.Body {
			rawBodyVisits++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := structural.walkWithRootBodyVisit(reflect.ValueOf(action), 0, func(value string) error {
		if value == action.Body {
			structuralBodyVisits++
		}
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	if *ordinary != structural || rawBodyVisits != 1 || structuralBodyVisits != 0 {
		t.Fatalf("structural visit changed source admission: ordinary=%+v structural=%+v visits=%d/%d", ordinary, structural, rawBodyVisits, structuralBodyVisits)
	}
}

func TestHTTPConcurrentAuraPaddedPolicy(t *testing.T) {
	producer := Action{Type: "GET", Target: "/producer", Extractors: []Extractor{{Name: "first", Type: "jsonpath", Expression: "$.first"}}}
	for _, representation := range []string{"plain form", "base64 form", "direct JSON"} {
		for _, policy := range []string{"literal", "active", "malformed"} {
			t.Run(representation+"/"+policy, func(t *testing.T) {
				consumer := concurrentAuraContractBody(t, representation, policy == "active")
				consumer.Metadata[ImportURLFormTemplateValueEncodingMetadataKey] = " \t" + ImportAuraJSONTemplateValueEncoding + "\n "
				if policy == "malformed" {
					consumer.Metadata[ImportAuraTemplateContractMetadataKey].(*aurapayload.TemplateContract).LiteralOccurrences[0].ContextID = strings.Repeat("0", 64)
				}
				_, err := ValidateHTTPConcurrentChildrenWithWorkLimit([]Action{producer, consumer}, MaxHTTPConcurrentPreparationWork)
				if (err != nil) != (policy != "literal") {
					t.Fatalf("padded policy admission: policy=%s err=%v", policy, err)
				}
			})
		}
	}
}

func TestBodyEncodingWrapperKeepsSharedLimitSentinel(t *testing.T) {
	_, _, err := DecodeBodyEncoding("YWJjZA==", "base64", 3)
	if ErrBodyEncodingLimit != bodyencoding.ErrLimit || !errors.Is(err, bodyencoding.ErrLimit) {
		t.Fatal("DSL wrapper changed the shared decoder's limit identity")
	}
}
