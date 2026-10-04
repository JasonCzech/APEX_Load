package paramize_test

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

func TestAuraTemplateContractProjectionAndRepairRemainValueFree(t *testing.T) {
	body := "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{"recordId":"business","privateKeyName":"{{var.existing_correlation}}","apiToken":"synthetic-private-literal"}}]}`) + "&aura.token={{var.existing_correlation}}"
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/aura", Body: body}}}
	advice := paramize.Suggest(script, paramize.Options{EnableDataParams: true})
	selected := map[string]bool{}
	for _, s := range advice.Suggestions {
		if len(s.Locations) == 1 && strings.HasSuffix(s.Locations[0].DecodedFieldPath, ".recordId") {
			selected[s.ID] = true
		}
	}
	if len(selected) != 1 {
		t.Fatal("missing business suggestion")
	}
	applied, _, err := paramize.ApplySuggestions(script, advice.Suggestions, selected)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(applied.Actions[0].Metadata[dsl.ImportAuraTemplateContractMetadataKey])
	for _, forbidden := range []string{"privateKeyName", "existing_correlation", "synthetic-private-literal", "business", "aura.token"} {
		if strings.Contains(string(metadata), forbidden) {
			t.Fatalf("occurrence contract disclosed %s", forbidden)
		}
	}
	projected, err := scriptdisclosure.Project(applied)
	if err != nil {
		t.Fatal(err)
	}
	appliedContract, err := aurapayload.ReadTemplateContract(applied.Actions[0].Metadata[dsl.ImportAuraTemplateContractMetadataKey])
	if err != nil {
		t.Fatal(err)
	}
	projectedContract, err := aurapayload.ReadTemplateContract(projected.Actions[0].Metadata[dsl.ImportAuraTemplateContractMetadataKey])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projectedContract, appliedContract) {
		t.Fatal("disclosure changed value-free occurrence contract")
	}
	for _, version := range []*dsl.Script{applied, projected} {
		contract, err := aurapayload.ReadTemplateContract(version.Actions[0].Metadata[dsl.ImportAuraTemplateContractMetadataKey])
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, err = aurapayload.ResolveContextualWithWorkAccounting(version.Actions[0].Body, "", contract, func(value string, _ aurapayload.TemplateContext) (string, string) { return value, value }, aurapayload.MaxWorkBytes)
		if err != nil {
			t.Fatal("projection or repair invalidated retained occurrence identity", err)
		}
	}
}
