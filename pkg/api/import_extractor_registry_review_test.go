package api

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

func TestEnterpriseNestedTypedExtractorKeepsSelectableAuraAdvice(t *testing.T) {
	source := `Action(){
        lr_start_transaction("save account");
        web_reg_save_param("recordid", "LB=id=", "RB=;", LAST);
        web_url("bootstrap", "URL=https://synthetic.invalid/bootstrap", LAST);
        web_custom_request("save", "URL=https://synthetic.invalid/aura", "Method=POST", "EncType=application/x-www-form-urlencoded", "Body=message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22synthetic%3A%2F%2Fsave%22%2C%22params%22%3A%7B%22recordId%22%3A%22synthetic-record%22%7D%7D%5D%7D", LAST);
        lr_end_transaction("save account", LR_AUTO);
        return 0;
    }`
	parsed, err := enterprisearchive.ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatal(err)
	}
	result, err := enterprisearchive.Convert([]*enterprisearchive.ParsedAction{parsed}, enterprisearchive.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	typedFound := false
	if err := dslwalk.Walk(result.Script, func(ref dslwalk.SurfaceRef) error {
		if ref.Request != nil {
			if extractors, ok := ref.Request["extractors"].([]dsl.Extractor); ok && len(extractors) == 1 && extractors[0].Name == "recordid" {
				typedFound = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !typedFound {
		t.Fatal("fixture did not reproduce importer typed sequence extractors")
	}
	advice := buildImportParameterization(result.Script, nil)
	response := newScriptImportResponse(scriptImportResponseOptions{Script: result.Script, Parameterization: advice})
	encoded, err := json.Marshal(response["parameterization"])
	if err != nil {
		t.Fatal(err)
	}
	var wire paramize.Result
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	var retained []paramize.Suggestion
	for _, suggestion := range wire.Suggestions {
		if len(suggestion.Locations) > 0 && len(suggestion.Locations[0].AuraRoute) > 0 {
			retained = append(retained, suggestion)
		}
	}
	if len(retained) != 1 || retained[0].VarName != "recordid_2" {
		t.Fatalf("selectable Aura advice = %+v, original=%+v", retained, advice.Suggestions)
	}
	if wire.AuraPayload == nil || wire.AuraPayload.RetainedCandidates != len(retained) {
		t.Fatalf("coverage lost selectable row: %+v", wire.AuraPayload)
	}
	resp, applied := postParameterizeApply(t, NewServer(ServerConfig{}), importParameterizeApplyRequest{Script: result.Script, SelectedIDs: []string{retained[0].ID}})
	if resp.Code != 200 || applied.AppliedCount != 1 {
		t.Fatalf("selectable advice cannot apply: status=%d body=%s", resp.Code, resp.Body.String())
	}
}
