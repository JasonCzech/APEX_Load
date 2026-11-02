package aura

import (
	"testing"
)

func TestIsGenericAuraActionName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "", want: true},
		{name: "POST /aura", want: true},
		{name: "POST aura", want: true},
		{name: "POST /s/sfsites/aura", want: true},
		{name: "POST s sfsites aura", want: true},
		{name: "38 - POST aura", want: true},
		{name: "01 - POST s sfsites aura", want: true},
		{name: "38 - POST aura — OrderController.submit", want: false},
		{name: "POST /services/data/v60.0/query", want: false},
	}
	for _, tc := range tests {
		if got := IsGenericAuraActionName(tc.name); got != tc.want {
			t.Errorf("IsGenericAuraActionName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEnrichBatchActionName(t *testing.T) {
	body := "message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FApexActionController%2FACTION%24execute%22%2C%22params%22%3A%7B%22classname%22%3A%22OrderController%22%2C%22method%22%3A%22submit%22%7D%7D%2C%7B%22id%22%3A%222%3Ba%22%2C%22descriptor%22%3A%22serviceComponent%3A%2F%2Fui.force.components.controllers.lists.selectableListDataProvider.SelectableListDataProviderController%2FACTION%24getItems%22%7D%5D%7D"

	if got := EnrichBatchActionName("38 - POST aura", body); got != "38 - POST aura — OrderController.submit (+1)" {
		t.Fatalf("EnrichBatchActionName numbered = %q, want %q", got, "38 - POST aura — OrderController.submit (+1)")
	}
	if got := EnrichBatchActionName("POST /aura", body); got != "POST /aura — OrderController.submit (+1)" {
		t.Fatalf("EnrichBatchActionName unnumbered = %q, want %q", got, "POST /aura — OrderController.submit (+1)")
	}
	if got := EnrichBatchActionName("Custom Step Name", body); got != "Custom Step Name" {
		t.Fatalf("EnrichBatchActionName custom = %q, want unchanged", got)
	}
}

func TestParseEnvelopeActionsTokens(t *testing.T) {
	raw := `{"actions":[{"id":"1;a","descriptor":"aura://ApexActionController/ACTION$execute","params":{"namespace":"ns","classname":"MyClass","method":"run"}},{"id":"2;a","descriptor":"aura://RecordUiController/ACTION$getRecordWithFields"}]}`
	acts := ParseEnvelopeActions(raw, 0)
	if len(acts) != 2 {
		t.Fatalf("ParseEnvelopeActions len = %d, want 2", len(acts))
	}
	if got := acts[0].ShortTitle(); got != "ns.MyClass.run" {
		t.Errorf("acts[0].ShortTitle() = %q, want ns.MyClass.run", got)
	}
	if got := acts[0].DiffToken(); got != "Apex:ns.MyClass.run" {
		t.Errorf("acts[0].DiffToken() = %q, want Apex:ns.MyClass.run", got)
	}
	if got := acts[0].FilterToken(); got != "aura://ApexActionController/ACTION$execute[ns.MyClass.run]" {
		t.Errorf("acts[0].FilterToken() = %q", got)
	}
	if got := acts[1].ShortTitle(); got != "RecordUiController.getRecordWithFields" {
		t.Errorf("acts[1].ShortTitle() = %q, want RecordUiController.getRecordWithFields", got)
	}
}
