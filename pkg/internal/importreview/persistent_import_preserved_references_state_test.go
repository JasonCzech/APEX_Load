package importreview

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// expectRefusal fails unless the edit is refused as a context change of the
// request at want ("actions[0]", or "script" for a credential held by the whole
// script).
func expectRefusal(t *testing.T, candidate, prior *dsl.Script, want string) {
	t.Helper()
	err := ValidatePreservedImportCredentialEdits(candidate, prior)
	if err == nil || !strings.Contains(err.Error(), "retained imported credentials at "+want) {
		t.Fatalf("%v, want the refusal at %s", err, want)
	}
	if strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("refusal disclosed a credential: %v", err)
	}
}

func expectAccepted(t *testing.T, candidate, prior *dsl.Script) {
	t.Helper()
	if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatalf("%v, want the edit accepted", err)
	}
}

func jsonStep(function string, params map[string]any) dsl.Action {
	merged := map[string]any{"function": function}
	for key, value := range params {
		merged[key] = value
	}
	return dsl.Action{Name: function, Type: "source_json", Params: merged}
}

// A source step keeps state the template scan cannot see: lr_eval_json stores a
// document under its JsonObject name, and lr_json_get_values and
// lr_json_stringify publish a variable from it, so the host a request reads
// comes from the eval step's buffer and whatever fills it.
func TestValidatePreservedImportCredentialEditsPinsJSONDocumentChain(t *testing.T) {
	eval := func(buffer string) dsl.Action {
		return jsonStep("lr_eval_json", map[string]any{"json_object": "doc", "buffer": buffer})
	}
	get := jsonStep("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "host", "query": "$.host", "select_all": "no"})
	stringify := jsonStep("lr_json_stringify", map[string]any{"json_object": "doc", "output_param": "host"})
	login := dsl.Action{
		Name: "login", Type: "GET", Method: "GET", Target: "https://login.example.test/token",
		Extractors: []dsl.Extractor{{Name: "resp", Source: "body", Type: "regex", Expression: "(.*)"}},
	}

	for name, fixture := range map[string]struct {
		init []dsl.Action
		edit func(*dsl.Script)
	}{
		"document buffer": {
			init: []dsl.Action{eval(`{"host":"origin.example.test"}`), get},
			edit: func(s *dsl.Script) { s.Init[0] = eval(`{"host":"attacker.test"}`) },
		},
		"stringified document buffer": {
			init: []dsl.Action{eval(`"origin.example.test"`), stringify},
			edit: func(s *dsl.Script) { s.Init[0] = eval(`"attacker.test"`) },
		},
		"request that fills the document": {
			init: []dsl.Action{login, eval(`{{var.resp}}`), get},
			edit: func(s *dsl.Script) { s.Init[0].Target = "https://attacker.test/token" },
		},
		"document name spelled in another case": {
			init: []dsl.Action{eval(`{"host":"origin.example.test"}`), jsonStep("lr_json_get_values", map[string]any{"json_object": " DOC ", "value_param": "host", "query": "$.host"})},
			edit: func(s *dsl.Script) { s.Init[0] = eval(`{"host":"attacker.test"}`) },
		},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			prior := varHostCredentialScript()
			prior.Init = fixture.init
			candidate := CloneScriptDefinition(prior)
			fixture.edit(candidate)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}

	t.Run("accepts another document", func(t *testing.T) {
		prior := varHostCredentialScript()
		other := jsonStep("lr_eval_json", map[string]any{"json_object": "other", "buffer": `{"region":"west"}`})
		prior.Init = []dsl.Action{eval(`{"host":"origin.example.test"}`), get, other}
		candidate := CloneScriptDefinition(prior)
		candidate.Init[2] = jsonStep("lr_eval_json", map[string]any{"json_object": "other", "buffer": `{"region":"east"}`})
		expectAccepted(t, candidate, prior)
	})
}

// lr_save_string with params.source_array copies one entry of an array,
// name_1..name_n and name_count, with no template to say so.
func TestValidatePreservedImportCredentialEditsPinsBareNameReads(t *testing.T) {
	collect := dsl.Action{
		Name: "collect", Type: "GET", Method: "GET", Target: "https://origin.example.test/hosts",
		Extractors: []dsl.Extractor{{Name: "hosts", Mode: "all", Source: "body", Type: "regex", Expression: `"host":"(.*?)"`}},
	}
	pick := dsl.Action{
		Name: "pick", Type: "source_parameter",
		Params: map[string]any{
			"function": "lr_save_string", "args": []any{"unused", "host"},
			"source_array": map[string]any{"name": "hosts", "selection": "random"},
		},
	}
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{collect, pick}

	t.Run("refuses the array's extractor", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Extractors[0].DefaultValue = stringPointer("attacker.test")
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("refuses the array's request", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Target = "https://attacker.test/hosts"
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("refuses an array published under the same name by another step", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, dsl.Action{
			Name: "more", Type: "GET", Method: "GET", Target: "https://attacker.test/hosts",
			Extractors: []dsl.Extractor{{Name: "hosts", Mode: "all", Source: "body", Type: "regex", Expression: `(.*)`}},
		})
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("accepts an extractor for an unrelated name on the same step", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Extractors = append(candidate.Init[0].Extractors,
			dsl.Extractor{Name: "hosts_note", Source: "body", Type: "regex", Expression: `(.*)`})
		expectAccepted(t, candidate, prior)
	})
}

// A step's output name can be computed (the runtime interpolates an OAuth
// step's save_as), so such a step may write any name.
func TestValidatePreservedImportCredentialEditsPinsComputedOutputNames(t *testing.T) {
	prior := varHostCredentialScript()
	prior.Parameters = append(prior.Parameters, dsl.Parameter{Name: "slot", Type: "list", Values: []string{"token"}, Update: "once"})
	candidate := CloneScriptDefinition(prior)
	candidate.Init = append(candidate.Init, dsl.Action{
		Name: "token", Type: "http_oauth2_token",
		Params: map[string]any{"token_url": "https://attacker.test/token", "save_as": "{{param.slot}}"},
	})
	expectRefusal(t, candidate, prior, "actions[0]")
}

func TestImportVariableNameVariantsStripsEveryParallelPrefix(t *testing.T) {
	for name, want := range map[string][]string{
		"host":                               {"host"},
		"parallel.eu.host":                   {"parallel.eu.host", "host"},
		"parallel.eu.west.host":              {"parallel.eu.west.host", "west.host", "host"},
		"PARALLEL.outer.parallel.inner.host": {"PARALLEL.outer.parallel.inner.host", "parallel.inner.host", "inner.host", "host"},
		"parallel.":                          {"parallel."},
	} {
		got := importVariableNameVariants(name)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("variants of %q = %v, want %v", name, got, want)
		}
	}
}

func TestValidatePreservedImportCredentialEditsPinsWritersInsideParallelBranches(t *testing.T) {
	branch := func(name string) dsl.Action {
		return dsl.Action{Name: "fork", Type: "control_parallel", Control: &dsl.ControlFlow{
			Branches: []dsl.ParallelBranch{{Name: name, Steps: []dsl.Action{{
				Name: "seed", Type: "GET", Method: "GET", Target: "https://origin.example.test/seed",
				Extractors: []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: "(.*)"}},
			}}}},
		}}
	}
	for _, reference := range []string{"parallel.eu.west.host", "parallel.outer.parallel.inner.host"} {
		t.Run(reference, func(t *testing.T) {
			prior := referencedCredentialScript()
			prior.Actions[0].Target = "https://{{var." + reference + "}}/orders"
			prior.Init = []dsl.Action{branch("eu.west")}
			candidate := CloneScriptDefinition(prior)
			candidate.Init[0].Control.Branches[0].Steps[0].Extractors[0].DefaultValue = stringPointer("attacker.test")
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
}

// Variables the engine and the protocol adapters publish under fixed names
// come from whatever the family's steps connect to.
func TestValidatePreservedImportCredentialEditsPinsProtocolVariableFamilies(t *testing.T) {
	cases := map[string]struct {
		variable string
		prior    []dsl.Action
		edit     func(*dsl.Script)
	}{
		"WebSocket message retargets the connection": {
			variable: "ws_message",
			prior: []dsl.Action{
				{Name: "connect", Type: "ws_connect", Target: "wss://origin.example.test/feed"},
				{Name: "on message", Type: "ws_on_message", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Name: "note", Type: "GET", Method: "GET", Target: "https://origin.example.test/note"}}}},
			},
			edit: func(s *dsl.Script) { s.Init[0].Target = "wss://attacker.test/feed" },
		},
		"Kafka key from a new consumer": {
			variable: "kafka_key",
			prior:    []dsl.Action{{Name: "consume", Type: "kafka_consume", Target: "orders"}},
			edit: func(s *dsl.Script) {
				s.Init = append(s.Init, dsl.Action{Name: "consume more", Type: "kafka_consume", Target: "attacker-topic"})
			},
		},
		"async response body from a new conversation": {
			variable: "async_response_body",
			prior:    nil,
			edit: func(s *dsl.Script) {
				s.Init = append(s.Init, dsl.Action{
					Name: "bg", Type: "http_async_start",
					Params: map[string]any{"id": "bg", "pattern": "once", "request": map[string]any{
						"name": "ping", "type": "GET", "method": "GET", "target": "https://attacker.test/ping",
					}},
				})
			},
		},
	}
	for name, fixture := range cases {
		t.Run("refuses "+name, func(t *testing.T) {
			prior := referencedCredentialScript()
			prior.Actions[0].Target = "https://{{var." + fixture.variable + "}}/orders"
			prior.Init = fixture.prior
			candidate := CloneScriptDefinition(prior)
			fixture.edit(candidate)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}

	t.Run("refuses a new step of a protocol whose steps all publish the variable", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Protocol = "java_http"
		prior.Config.JavaHTTP.ResponseMapping = map[string]string{"host": `"host":"(.*?)"`}
		prior.Actions[0].Target = "https://{{var.host}}/orders"
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, dsl.Action{Name: "seed", Type: "GET", Method: "GET", Target: "https://attacker.test/seed"})
		expectRefusal(t, candidate, prior, "actions[0]")
	})

	// loop_index is the loop's pass counter: a number, but a port, an address
	// octet or a host label is a number, so the loop's count is held for the
	// request that reads it.
	t.Run("refuses a loop count beside a request that reads loop_index", func(t *testing.T) {
		prior := sourceReviewCredentialScript()
		orders := prior.Actions[0]
		orders.Target = "https://10.0.0.{{var.loop_index}}/orders"
		prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{orders}}}
		candidate := CloneScriptDefinition(prior)
		candidate.Actions[0].Control.Count = 255
		expectRefusal(t, candidate, prior, "actions[0].control.steps[0]")
	})
	t.Run("accepts a loop count beside a request that does not read loop_index", func(t *testing.T) {
		prior := sourceReviewCredentialScript()
		prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{prior.Actions[0]}}}
		candidate := CloneScriptDefinition(prior)
		candidate.Actions[0].Control.Count = 255
		expectAccepted(t, candidate, prior)
	})

	t.Run("accepts an unrelated step of the family", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0].Target = "https://origin.example.test/orders/{{var.region_note}}"
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, dsl.Action{Name: "consume", Type: "kafka_consume", Target: "orders"})
		expectAccepted(t, candidate, prior)
	})
}

// A form-encoded structured body (a Salesforce Aura message) keeps its
// templates percent-encoded in the stored text; the runtime resolves them.
func TestValidatePreservedImportCredentialEditsPinsFormEncodedBodyReferences(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://origin.example.test/aura"
	prior.Actions[0].Method = "POST"
	prior.Actions[0].Body = "message=%7B%22callback%22%3A%22https%3A%2F%2F%7B%7Bparam.host%7D%7D%2Fhook%22%7D&aura.token=undefined"
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[0].Values = []string{"attacker.test"}
	expectRefusal(t, candidate, prior, "actions[0]")
}

// A credential kept at script level can be sent with any request, so the
// producers of every variable a request reads are held as well.
func TestValidatePreservedImportCredentialEditsPinsProducersForScriptWideCredentials(t *testing.T) {
	newPrior := func() *dsl.Script {
		script := &dsl.Script{
			Name: "Script wide credential", Version: "1", Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source: "enterprise_script_archive", Preserved: map[string]any{},
			}},
			Actions: []dsl.Action{
				{
					Name: "discover", Type: "GET", Method: "GET", Target: "https://origin.example.test/discover",
					Extractors: []dsl.Extractor{
						{Name: "host", Source: "body", Type: "regex", Expression: `"host":"(.*?)"`},
						{Name: "note", Source: "body", Type: "regex", Expression: `"note":"(.*?)"`},
					},
				},
				{Name: "orders", Type: "GET", Method: "GET", Target: "https://{{var.host}}/orders"},
			},
		}
		script.Config.HTTP.Auth = dsl.AuthConfig{Type: "bearer", Token: "source-review-synthetic-script-token"}
		MarkCredentialPreservingImportScript(script)
		return script
	}
	prior := newPrior()
	if err := ValidatePreservedImportCredentialEdits(CloneScriptDefinition(prior), prior); err != nil {
		t.Fatalf("unchanged script: %v", err)
	}
	for name, edit := range map[string]func(*dsl.Script){
		"extractor default":    func(s *dsl.Script) { s.Actions[0].Extractors[0].DefaultValue = stringPointer("attacker.test") },
		"extractor expression": func(s *dsl.Script) { s.Actions[0].Extractors[0].Expression = `"other":"(.*?)"` },
		"new writer": func(s *dsl.Script) {
			s.Init = append(s.Init, hostExtractorStep("seed"))
		},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			expectRefusal(t, candidate, prior, "script")
		})
	}
	t.Run("accepts an extractor nothing reads", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Actions[0].Extractors[1].Expression = `"other":"(.*?)"`
		expectAccepted(t, candidate, prior)
	})
}

// A producer is held by what it does, not where it sits: a step inserted or
// removed ahead of it moves it, and an extractor of another name on the same
// step is not the value the request reads.
func TestValidatePreservedImportCredentialEditsHoldsProducersByContent(t *testing.T) {
	prior := varHostCredentialScript()
	bootstrap := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "GET", Method: "GET", Target: "https://origin.example.test/" + name}
	}
	discover := dsl.Action{
		Name: "discover", Type: "GET", Method: "GET", Target: "https://origin.example.test/discover",
		Extractors: []dsl.Extractor{
			{Name: "host", Source: "body", Type: "regex", Expression: `"host":"(.*?)"`},
			{Name: "note", Source: "body", Type: "regex", Expression: `"note":"(.*?)"`},
		},
	}
	prior.Init = []dsl.Action{bootstrap("warmup"), discover}

	t.Run("accepts a step inserted ahead of the producer", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append([]dsl.Action{bootstrap("extra")}, candidate.Init...)
		expectAccepted(t, candidate, prior)
	})
	t.Run("accepts a step removed ahead of the producer", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init = candidate.Init[1:]
		expectAccepted(t, candidate, prior)
	})
	t.Run("accepts an unrelated extractor edited or added on the producer", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[1].Extractors[1].Expression = `"other":"(.*?)"`
		candidate.Init[1].Extractors = append(candidate.Init[1].Extractors, dsl.Extractor{Name: "extra", Source: "body", Type: "regex", Expression: "(.*)"})
		expectAccepted(t, candidate, prior)
	})
	t.Run("refuses the matching extractor", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[1].Extractors[0].DefaultValue = stringPointer("attacker.test")
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("refuses the producer's request", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[1].Target = "https://attacker.test/discover"
		expectRefusal(t, candidate, prior, "actions[0]")
	})
}

// Writer names match the way the runtime publishes them.
func TestValidatePreservedImportCredentialEditsMatchesArrayNamesPrecisely(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Init = []dsl.Action{{
		Name: "collect", Type: "GET", Method: "GET", Target: "https://origin.example.test/hosts",
		Extractors: []dsl.Extractor{{Name: "hosts", Mode: "all", Source: "body", Type: "regex", Expression: `(.*)`}},
	}}
	for reference, refused := range map[string]bool{
		"hosts_1":     true,
		"hosts_count": true,
		"hosts_12":    true,
		"hosts_note":  false,
		"hosts_":      false,
		"hostsfile":   false,
	} {
		t.Run(reference, func(t *testing.T) {
			scenario := CloneScriptDefinition(prior)
			scenario.Actions[0].Target = "https://{{var." + reference + "}}/orders"
			candidate := CloneScriptDefinition(scenario)
			candidate.Init[0].Extractors[0].DefaultValue = stringPointer("attacker.test")
			if refused {
				expectRefusal(t, candidate, scenario, "actions[0]")
			} else {
				expectAccepted(t, candidate, scenario)
			}
		})
	}
}

// What a request reads reaches further than its own fields: a parameter is
// read through the data source and the parameter it follows, a producer
// through its own target and extractor defaults, a file reference through its
// data source, and a data-driven loop through its row condition.
func TestValidatePreservedImportCredentialEditsFollowsTransitiveReferences(t *testing.T) {
	t.Run("producer's request reads a parameter", func(t *testing.T) {
		prior := varHostCredentialScript()
		prior.Init = []dsl.Action{{
			Name: "discover", Type: "GET", Method: "GET", Target: "https://{{param.region}}.example.test/discover",
			Extractors: []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: "(.*)"}},
		}}
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[1].Values = []string{"attacker"}
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("extractor default reads an earlier extractor", func(t *testing.T) {
		prior := varHostCredentialScript()
		prior.Init = []dsl.Action{
			{Name: "first", Type: "GET", Method: "GET", Target: "https://origin.example.test/first",
				Extractors: []dsl.Extractor{{Name: "fallback", Source: "body", Type: "regex", Expression: "(.*)"}}},
			{Name: "second", Type: "GET", Method: "GET", Target: "https://origin.example.test/second",
				Extractors: []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: "(.*)", DefaultValue: stringPointer("{{var.fallback}}")}}},
		}
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Target = "https://attacker.test/first"
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("file reference", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0].Target = "https://origin.example.test/orders"
		prior.Actions[0].Method = "POST"
		prior.Actions[0].Body = "{{file.payload}}"
		prior.DataSources = []dsl.DataSource{
			{Name: "payload", Type: "file", InlineData: "one", Strategy: "sequential"},
			{Name: "other", Type: "file", InlineData: "two", Strategy: "sequential"},
		}
		candidate := CloneScriptDefinition(prior)
		candidate.DataSources[0].InlineData = "attacker"
		expectRefusal(t, candidate, prior, "actions[0]")
		unrelated := CloneScriptDefinition(prior)
		unrelated.DataSources[1].InlineData = "changed"
		expectAccepted(t, unrelated, prior)
	})
	t.Run("data-driven loop's row condition", func(t *testing.T) {
		prior := sourceReviewCredentialScript()
		prior.Parameters = []dsl.Parameter{{Name: "limit", Type: "list", Values: []string{"10"}, Update: "once"}}
		prior.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true}}
		orders := prior.Actions[0]
		orders.Target = "https://{{var.row.host}}/orders"
		prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{
			ForEach: &dsl.ForEachLoop{Source: "hosts", Var: "row", Condition: &dsl.Condition{Left: "{{param.limit}}", Op: "gt", Right: "0"}},
			Steps:   []dsl.Action{orders},
		}}
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[0].Values = []string{"20"}
		expectRefusal(t, candidate, prior, "actions[0].control.steps[0]")
	})
	t.Run("java vuser step reads every parameter", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0] = dsl.Action{
			Name: "java", Type: "java_vuser_run", Params: map[string]any{"method": "run", "lr_parameters": map[string]any{"token": sourceReviewKeptCredential}},
		}
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[1].Values = []string{"changed"}
		expectRefusal(t, candidate, prior, "actions[0]")
	})
}

// A step whose outputs cannot be read may write any name.
func TestImportReferenceScopeTreatsUndecodableStepsAsWritingAnything(t *testing.T) {
	scope := newImportReferenceScope(map[string]any{
		"actions": []any{
			map[string]any{"name": "odd", "type": 5.0},
			map[string]any{"name": "plain", "type": "GET"},
		},
	}, newImportInterner())
	writers := scope.writers("anything")
	if len(writers) != 1 || writers[0].path != "/actions/0" || !writers[0].any {
		t.Fatalf("writers = %+v, want the undecodable step as a writer of anything", writers)
	}
}

// The closure is linear in what a request reaches: a long chain of correlated
// requests, each carrying a kept credential and reading the variable the
// previous one extracts, still validates quickly.
func TestValidatePreservedImportCredentialEditsScalesWithCorrelatedRequests(t *testing.T) {
	const requests = 600
	prior := sourceReviewCredentialScript()
	prior.Parameters = []dsl.Parameter{{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"}, {Name: "spare", Type: "list", Values: []string{"x"}, Update: "once"}}
	prior.Actions = prior.Actions[:0]
	for index := 0; index < requests; index++ {
		prior.Actions = append(prior.Actions, dsl.Action{
			Name: fmt.Sprintf("r%d", index), Type: "GET", Method: "GET",
			Target:     fmt.Sprintf("https://{{param.region}}.example.test/r%d/{{var.t%d}}", index, index),
			Headers:    map[string]string{"Authorization": sourceReviewKeptCredential},
			Extractors: []dsl.Extractor{{Name: fmt.Sprintf("t%d", index+1), Source: "body", Type: "regex", Expression: "(.*)"}},
		})
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[1].Values = []string{"changed"}

	started := time.Now()
	expectAccepted(t, candidate, prior)
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("validating %d correlated requests took %v", requests, elapsed)
	}
	candidate.Actions[0].Extractors[0].DefaultValue = stringPointer("attacker.test")
	expectRefusal(t, candidate, prior, "actions[1]")
}
