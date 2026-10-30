package importreview

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// referencedCredentialScript is a credential-preserving import whose kept
// Authorization header sits on actions[0], a request whose target is templated
// on the "host" parameter. "region" is a parameter nothing references.
func referencedCredentialScript() *dsl.Script {
	script := sourceReviewCredentialScript()
	script.Parameters = []dsl.Parameter{
		{Name: "host", Type: "list", Values: []string{"origin.example.test"}, Update: "once"},
		{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"},
	}
	script.Actions[0].Target = "https://{{param.host}}/orders"
	return script
}

// A request that carries a kept credential is sent wherever its target,
// headers, body and auth resolve to, so the parameters, data sources and
// variable producers it interpolates are part of its execution context. An
// editor who cannot read the credential could otherwise redirect it by editing
// the parameter that feeds the target.
func TestValidatePreservedImportCredentialEditsPinsReferencedParameters(t *testing.T) {
	prior := referencedCredentialScript()

	refusals := []struct {
		name  string
		setup func(*dsl.Script)
		edit  func(*dsl.Script)
	}{
		{"parameter feeding the target", nil, func(script *dsl.Script) {
			script.Parameters[0].Values = []string{"attacker.test"}
		}},
		{"parameter named in another case", func(script *dsl.Script) {
			script.Actions[0].Target = "https://{{ param.HOST }}/orders"
		}, func(script *dsl.Script) {
			script.Parameters[0].Values = []string{"attacker.test"}
		}},
		{"parameter reached through the var fallback", func(script *dsl.Script) {
			script.Actions[0].Target = "https://{{var.host}}/orders"
		}, func(script *dsl.Script) {
			script.Parameters[0].Values = []string{"attacker.test"}
		}},
		{"parameter feeding a header", func(script *dsl.Script) {
			script.Actions[0].Target = "https://origin.example.test/orders"
			script.Actions[0].Headers["X-Forwarded-Host"] = "{{param.host}}"
		}, func(script *dsl.Script) {
			script.Parameters[0].Values = []string{"attacker.test"}
		}},
		{"parameter feeding the body", func(script *dsl.Script) {
			script.Actions[0].Target = "https://origin.example.test/orders"
			script.Actions[0].Method = "POST"
			script.Actions[0].Body = `{"callback":"https://{{param.host}}/hook"}`
		}, func(script *dsl.Script) {
			script.Parameters[0].Values = []string{"attacker.test"}
		}},
		{"parameter feeding a request parameter", func(script *dsl.Script) {
			script.Actions[0].Target = "https://origin.example.test/orders"
			script.Actions[0].Params = map[string]any{"proxy_host": "{{param.host}}"}
		}, func(script *dsl.Script) {
			script.Parameters[0].Values = []string{"attacker.test"}
		}},
		{"type of the parameter feeding the target", nil, func(script *dsl.Script) {
			script.Parameters[0] = dsl.Parameter{Name: "host", Type: "file", Values: []string{"origin.example.test"}, Update: "once"}
		}},
		{"update policy of the parameter feeding the target", nil, func(script *dsl.Script) {
			script.Parameters[0].Update = "each_occurrence"
		}},
		{"parameter feeding the target removed", nil, func(script *dsl.Script) {
			script.Parameters = script.Parameters[1:]
		}},
		{"parameter feeding the target renamed away", nil, func(script *dsl.Script) {
			script.Parameters[0].Name = "origin"
		}},
		{"parameter the target's parameter follows", func(script *dsl.Script) {
			script.Parameters = append(script.Parameters,
				dsl.Parameter{Name: "master", Type: "list", Values: []string{"origin.example.test"}, Update: "once"})
			script.Parameters[0] = dsl.Parameter{Name: "host", Type: "list", SameLineAs: "master", Values: []string{"origin.example.test"}, Update: "once"}
		}, func(script *dsl.Script) {
			script.Parameters[2].Values = []string{"attacker.test"}
		}},
		{"data source behind the target's parameter", func(script *dsl.Script) {
			script.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true}}
			script.Parameters[0] = dsl.Parameter{Name: "host", Type: "file", DataSource: "hosts", Column: "host", Update: "once"}
		}, func(script *dsl.Script) {
			script.DataSources[0].InlineData = "host\nattacker.test\n"
		}},
		{"data source the target reads directly", func(script *dsl.Script) {
			script.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true}}
			script.Actions[0].Target = "https://{{data.hosts.host}}/orders"
		}, func(script *dsl.Script) {
			script.DataSources[0].InlineData = "host\nattacker.test\n"
		}},
		{"data source the target reads through var", func(script *dsl.Script) {
			script.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true}}
			script.Actions[0].Target = "https://{{var.hosts.host}}/orders"
		}, func(script *dsl.Script) {
			script.DataSources[0].InlineData = "host\nattacker.test\n"
		}},
	}
	for _, refusal := range refusals {
		t.Run("refuses "+refusal.name, func(t *testing.T) {
			prior := CloneScriptDefinition(prior)
			if refusal.setup != nil {
				refusal.setup(prior)
			}
			candidate := CloneScriptDefinition(prior)
			refusal.edit(candidate)
			err := ValidatePreservedImportCredentialEdits(candidate, prior)
			if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
				t.Fatalf("%v, want the refusal at actions[0]", err)
			}
			if strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("refusal disclosed a credential: %v", err)
			}
		})
	}

	accepts := []struct {
		name  string
		setup func(*dsl.Script)
		edit  func(*dsl.Script)
	}{
		{"parameter nothing references", nil, func(script *dsl.Script) {
			script.Parameters[1].Values = []string{"east"}
		}},
		{"selection policy of the parameter feeding the target", nil, func(script *dsl.Script) {
			script.Parameters[0].Selection = "random"
		}},
		{"added parameter", nil, func(script *dsl.Script) {
			script.Parameters = append(script.Parameters, dsl.Parameter{Name: "tenant", Type: "list", Values: []string{"t1"}, Update: "once"})
		}},
		{"added data source", nil, func(script *dsl.Script) {
			script.DataSources = append(script.DataSources, dsl.DataSource{Name: "regions", Type: "csv", InlineData: "region\nwest\n", Strategy: "sequential", FirstRowHeader: true})
		}},
		{"data source the target does not read", func(script *dsl.Script) {
			script.DataSources = []dsl.DataSource{{Name: "regions", Type: "csv", InlineData: "region\nwest\n", Strategy: "sequential", FirstRowHeader: true}}
		}, func(script *dsl.Script) {
			script.DataSources[0].InlineData = "region\neast\n"
		}},
		{"data source behind a parameter nothing references", func(script *dsl.Script) {
			script.DataSources = []dsl.DataSource{{Name: "regions", Type: "csv", InlineData: "region\nwest\n", Strategy: "sequential", FirstRowHeader: true}}
			script.Parameters[1] = dsl.Parameter{Name: "region", Type: "file", DataSource: "regions", Column: "region", Update: "once"}
		}, func(script *dsl.Script) {
			script.DataSources[0].InlineData = "region\neast\n"
		}},
		{"request without the kept credential", nil, func(script *dsl.Script) {
			script.Actions[1].Target = "https://{{param.region}}.example.test/cart"
		}},
		{"parameter only a request without the kept credential reads", func(script *dsl.Script) {
			script.Actions[1].Target = "https://{{param.region}}.example.test/cart"
		}, func(script *dsl.Script) {
			script.Parameters[1].Values = []string{"east"}
		}},
	}
	for _, accept := range accepts {
		t.Run("accepts "+accept.name, func(t *testing.T) {
			prior := CloneScriptDefinition(prior)
			if accept.setup != nil {
				accept.setup(prior)
			}
			candidate := CloneScriptDefinition(prior)
			accept.edit(candidate)
			if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
				t.Fatalf("%v, want the edit accepted", err)
			}
		})
	}
}

// The reviewer's reproduction, through the save preparation an editor's
// masked draft goes through: the kept header is restored from the prior, so
// without the parameter pin the next run sends it to the edited host.
func TestPreparePersistentLibraryScriptRefusesRedirectThroughParameter(t *testing.T) {
	prior := referencedCredentialScript()
	disclosed := MaskScriptSecrets(prior)
	if disclosed == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	if strings.Contains(disclosed.Actions[0].Headers["Authorization"], "synthetic") {
		t.Fatalf("disclosed Authorization = %q, want it masked", disclosed.Actions[0].Headers["Authorization"])
	}
	disclosed.Parameters[0].Values = []string{"attacker.test"}

	saved, err := PreparePersistentLibraryScript(disclosed, prior)
	if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
		t.Fatalf("redirecting save = %v (saved %#v), want the refusal at actions[0]", err, saved)
	}

	unrelated := MaskScriptSecrets(prior)
	unrelated.Parameters[1].Values = []string{"east"}
	saved, err = PreparePersistentLibraryScript(unrelated, prior)
	if err != nil {
		t.Fatalf("unrelated parameter edit: %v", err)
	}
	if got := saved.Actions[0].Headers["Authorization"]; got != sourceReviewKeptCredential {
		t.Fatalf("unrelated edit changed the kept Authorization = %q", got)
	}
}

// producerCredentialScript keeps its credential on actions[1], whose target is
// templated on {{var.host}}. An init step and actions[0] both extract hosts.
func producerCredentialScript() *dsl.Script {
	script := sourceReviewCredentialScript()
	script.Init = []dsl.Action{{
		Name: "bootstrap", Type: "GET", Method: "GET", Target: "https://origin.example.test/bootstrap",
		Extractors: []dsl.Extractor{{Name: "tenant", Source: "body", Type: "regex", Expression: `"tenant":"(.*?)"`}},
	}}
	script.Actions = []dsl.Action{
		{
			Name: "discover", Type: "GET", Method: "GET", Target: "https://origin.example.test/discover",
			Extractors: []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: `"host":"(.*?)"`}},
		},
		{
			Name: "orders", Type: "GET", Method: "GET", Target: "https://{{var.host}}/orders",
			Headers: map[string]string{"Authorization": sourceReviewKeptCredential},
		},
		{Name: "cart", Type: "GET", Method: "GET", Target: "https://origin.example.test/cart"},
	}
	script.Metadata.Import.Preserved["source_review"].(map[string]any)["mappings"] = []any{
		map[string]any{"source_file": "Action.c", "source_line": 4, "action_path": "actions[1]"},
	}
	return script
}

// A variable a kept-credential request interpolates is only as trustworthy as
// the steps that write it: an extractor that falls back to a default, or a new
// step that saves the name, redirects the credential like an edited parameter.
func TestValidatePreservedImportCredentialEditsPinsVariableProducers(t *testing.T) {
	prior := producerCredentialScript()
	defaultHost := "attacker.test"

	refusals := []struct {
		name string
		edit func(*dsl.Script)
	}{
		{"extractor default", func(script *dsl.Script) {
			script.Actions[0].Extractors[0].DefaultValue = &defaultHost
		}},
		{"extractor expression", func(script *dsl.Script) {
			script.Actions[0].Extractors[0].Expression = `"other":"(.*?)"`
		}},
		{"extractor removed", func(script *dsl.Script) {
			script.Actions[0].Extractors = nil
		}},
		{"producer's own target", func(script *dsl.Script) {
			script.Actions[0].Target = "https://attacker.test/discover"
		}},
		{"extractor added in the init phase", func(script *dsl.Script) {
			script.Init = append(script.Init, dsl.Action{
				Name: "seed", Type: "GET", Method: "GET", Target: "https://origin.example.test/seed",
				Extractors: []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: `(.*)`, DefaultValue: &defaultHost}},
			})
		}},
		{"extractor added in the teardown phase", func(script *dsl.Script) {
			script.Teardown = append(script.Teardown, dsl.Action{
				Name: "late", Type: "GET", Method: "GET", Target: "https://origin.example.test/late",
				Extractors: []dsl.Extractor{{Name: "HOST", Source: "body", Type: "regex", Expression: `(.*)`, DefaultValue: &defaultHost}},
			})
		}},
		{"array extractor added for the name", func(script *dsl.Script) {
			script.Init = append(script.Init, dsl.Action{
				Name: "all", Type: "GET", Method: "GET", Target: "https://origin.example.test/all",
				Extractors: []dsl.Extractor{{Name: "host", Mode: "all", Source: "body", Type: "regex", Expression: `(.*)`}},
			})
		}},
		{"parameter added under the name", func(script *dsl.Script) {
			script.Parameters = append(script.Parameters, dsl.Parameter{Name: "host", Type: "list", Values: []string{"attacker.test"}, Update: "once"})
		}},
	}
	for _, refusal := range refusals {
		t.Run("refuses "+refusal.name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			refusal.edit(candidate)
			err := ValidatePreservedImportCredentialEdits(candidate, prior)
			if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[1]") {
				t.Fatalf("%v, want the refusal at actions[1]", err)
			}
		})
	}

	accepts := []struct {
		name string
		edit func(*dsl.Script)
	}{
		{"extractor of another name", func(script *dsl.Script) {
			script.Init[0].Extractors[0].Expression = `"other":"(.*?)"`
		}},
		{"extractor added for another name", func(script *dsl.Script) {
			script.Init = append(script.Init, dsl.Action{
				Name: "seed", Type: "GET", Method: "GET", Target: "https://origin.example.test/seed",
				Extractors: []dsl.Extractor{{Name: "hostname", Source: "body", Type: "regex", Expression: `(.*)`}},
			})
		}},
		{"unrelated request", func(script *dsl.Script) {
			script.Actions[2].Target = "https://origin.example.test/cart/summary"
		}},
		{"assertion on the producer", func(script *dsl.Script) {
			script.Actions[0].Assertions = []dsl.Assertion{{Type: "status", Expected: 200}}
		}},
	}
	for _, accept := range accepts {
		t.Run("accepts "+accept.name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			accept.edit(candidate)
			if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
				t.Fatalf("%v, want the edit accepted", err)
			}
		})
	}
}

// A Kafka send delivers its metadata as record headers. They do not pick where
// the header that holds the kept credential goes (the topic does), so a header
// beside it may be rebound to a parameter, as a stored repair does, while the
// topic stays pinned.
func TestValidatePreservedImportCredentialEditsIgnoresSentMetadataReferences(t *testing.T) {
	prior := &dsl.Script{
		Name: "Kafka sibling header", Version: "1", Protocol: "kafka",
		Config:     dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true}},
		Parameters: []dsl.Parameter{{Name: "orderId", Type: "list", Values: []string{"order-1001"}, Update: "once"}},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive", Preserved: map[string]any{},
		}},
		Actions: []dsl.Action{{
			Name: "send", Type: "kafka_produce", Target: "orders", Body: "{}",
			Metadata: map[string]any{"x-auth": sourceReviewKeptCredential, "x-corr": "{{var.corr}}"},
		}},
	}
	MarkCredentialPreservingImportScript(prior)

	rebound := CloneScriptDefinition(prior)
	rebound.Actions[0].Metadata["x-corr"] = "{{var.orderId}}"
	if err := ValidatePreservedImportCredentialEdits(rebound, prior); err != nil {
		t.Fatalf("%v, want the sibling header rebind accepted", err)
	}

	retargeted := CloneScriptDefinition(prior)
	retargeted.Actions[0].Target = "orders-audit"
	err := ValidatePreservedImportCredentialEdits(retargeted, prior)
	if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
		t.Fatalf("%v, want the refusal at the Kafka send", err)
	}
}
