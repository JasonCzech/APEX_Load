package importreview

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// JMeter copies an inherited header manager into config.http.default_headers
// and into each sampler's own headers. A review gate on the sampler hides its
// copies from discovery, so the one binding offered is the default header. The
// action copy replaces the default on the wire, so binding the default alone
// would leave the recorded value as the one sent.
func TestPreservedDefaultHeaderBindingRebindsShadowingActionCopies(t *testing.T) {
	const recorded = "synthetic-client-secret-7f3a"
	script := &dsl.Script{
		Name: "Split", Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test", HTTP: dsl.HTTPConfig{
			DefaultHeaders: map[string]string{"client_secret": recorded, "client_id": "public-client"},
		}},
		Actions: []dsl.Action{{
			Name: "BALANCE", Type: "request", Method: "POST", Target: "/balance",
			Headers: map[string]string{"client_secret": recorded, "Content-Type": "application/json"},
			HeaderList: dsl.HeaderList{
				{Name: "Content-Type", Value: "application/json"},
				{Name: "Client_Secret", Value: recorded},
			},
			Body:     `{"amount":1}`,
			Metadata: map[string]any{"execution_blocker": "import_review_required", "import_review": map[string]any{"reason_codes": []any{"jmeter_timestamp_timezone_required"}}},
		}, {
			Name: "OTHER", Type: "request", Method: "GET", Target: "/other",
			Headers: map[string]string{"client_secret": "a-different-value"},
		}},
	}
	EnsureCredentialPreservingImportMetadata(script, "jmeter")
	requirements := DiscoverSecretBindingRequirements(MaskScriptSecrets(script), 0)
	var defaultHeader *SecretBindingRequirement
	for index := range requirements {
		if requirements[index].Path == "/config/http/default_headers/client_secret" {
			defaultHeader = &requirements[index]
		}
	}
	if defaultHeader == nil {
		t.Fatalf("no default-header requirement in %#v", requirements)
	}
	bound, _, err := ApplyCredentialPreservingSecretBindings(script, 0, []SecretBindingSelection{{RequirementID: defaultHeader.ID, SecretName: "HTTP_CLIENT_SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	const ref = "{{secret.HTTP_CLIENT_SECRET}}"
	action := bound.Actions[0]
	if bound.Config.HTTP.DefaultHeaders["client_secret"] != ref || action.Headers["client_secret"] != ref || action.HeaderList[1].Value != ref {
		t.Fatalf("binding left a shadowing copy: default=%q headers=%q list=%q",
			bound.Config.HTTP.DefaultHeaders["client_secret"], action.Headers["client_secret"], action.HeaderList[1].Value)
	}
	effective, _ := httpcontract.EffectiveAuthoredHeaderList(bound.Config.HTTP.DefaultHeaders, protocolHeaders(bound.Config.HTTP.DefaultHeaderList), action.Headers, protocolHeaders(action.HeaderList))
	for _, header := range effective {
		if strings.EqualFold(header.Name, "client_secret") && header.Value != ref {
			t.Fatalf("effective header sent %q, want the bound reference", header.Value)
		}
	}
	if bound.Actions[1].Headers["client_secret"] != "a-different-value" {
		t.Fatal("a header the action sets to its own value was rebound")
	}
	if action.Headers["Content-Type"] != "application/json" || bound.Config.HTTP.DefaultHeaders["client_id"] != "public-client" {
		t.Fatal("binding changed unrelated headers")
	}
	if script.Actions[0].Headers["client_secret"] != recorded {
		t.Fatal("binding changed the caller's script")
	}
}

func protocolHeaders(list dsl.HeaderList) protocol.HeaderList {
	out := make(protocol.HeaderList, 0, len(list))
	for _, entry := range list {
		out = append(out, protocol.HeaderEntry{Name: entry.Name, Value: entry.Value})
	}
	return out
}
