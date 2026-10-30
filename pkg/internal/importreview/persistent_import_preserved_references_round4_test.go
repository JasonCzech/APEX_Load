package importreview

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// What nothing publishes is not a dependency: adding a data source no request
// reads must not change what a request that reads a dotted name resolves
// through, whether or not the dotted name could ever have named a column.
func TestValidatePreservedImportCredentialEditsIgnoresAnUnrelatedDataSource(t *testing.T) {
	for name, reference := range map[string]string{
		"dotted variable with a parameter fallback": "{{var.api.host}}",
		"data reference to a missing source":        "{{data.hosts.host}}",
	} {
		t.Run(name, func(t *testing.T) {
			prior := referencedCredentialScript()
			prior.Parameters = append(prior.Parameters, dsl.Parameter{Name: "api.host", Type: "list", Values: []string{"origin.example.test"}, Update: "once"})
			prior.Actions[0].Target = "https://" + reference + "/orders"
			for _, source := range []string{"regions", "r", "a-much-longer-source-name"} {
				candidate := CloneScriptDefinition(prior)
				candidate.DataSources = append(candidate.DataSources, dsl.DataSource{Name: source, Type: "csv", InlineData: "region\nwest\n", Strategy: "sequential", FirstRowHeader: true})
				expectAccepted(t, candidate, prior)
			}
		})
	}
}

// Two underscores begin ordinary correlation names too (__VIEWSTATE), and
// those are published by the extractor that names them, not by the engine.
func TestValidatePreservedImportCredentialEditsTreatsDoubleUnderscoreNamesAsOrdinary(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Method = "POST"
	prior.Actions[0].Target = "https://origin.example.test/login.aspx"
	prior.Actions[0].Body = "__VIEWSTATE={{var.__VIEWSTATE}}&user=a"
	prior.Init = []dsl.Action{
		{Name: "begin", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []any{"Login"}}},
		{Name: "page", Type: "GET", Method: "GET", Target: "https://origin.example.test/login.aspx", Extractors: []dsl.Extractor{{Name: "__VIEWSTATE", Source: "body", Type: "regex", Expression: "(.*)"}}},
	}
	for name, edit := range map[string]func(*dsl.Script){
		"rename the transaction": func(s *dsl.Script) { s.Init[0].Params["args"] = []any{"Sign In"} },
		"add an unrelated save":  func(s *dsl.Script) { s.Init = append(s.Init, saveStep("region", "east", "region")) },
		"add a header step": func(s *dsl.Script) {
			s.Init = append(s.Init, dsl.Action{Name: "header", Type: "source_parameter", Params: map[string]any{"function": "web_add_header", "args": []any{"X-A", "b"}}})
		},
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			expectAccepted(t, candidate, prior)
		})
	}
	t.Run("refuses the extractor that publishes it", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[1].Extractors[0].DefaultValue = stringPointer("attacker")
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	// lr_exit stores its arguments under __source_exit_requested.
	t.Run("refuses a step that stores engine state the request reads", func(t *testing.T) {
		scenario := CloneScriptDefinition(prior)
		scenario.Actions[0].Target = "https://{{var.__source_exit_requested}}/login.aspx"
		candidate := CloneScriptDefinition(scenario)
		candidate.Init = append(candidate.Init, dsl.Action{Name: "exit", Type: "loadrunner_runtime", Params: map[string]any{"function": "lr_exit", "args": []any{"attacker.test"}}})
		expectRefusal(t, candidate, scenario, "actions[0]")
	})
}

// A name longer than any the guard looks up still resolves to its parameter or
// its writer.
func TestValidatePreservedImportCredentialEditsResolvesVeryLongNames(t *testing.T) {
	long := strings.Repeat("n", 5000)
	t.Run("parameter", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Parameters[0].Name = long
		prior.Actions[0].Target = "https://{{param." + long + "}}/orders"
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[0].Values = []string{"attacker.test"}
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("writer", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0].Target = "https://{{var." + long + "}}/orders"
		prior.Init = []dsl.Action{saveStep("save", "origin.example.test", long)}
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0] = saveStep("save", "attacker.test", long)
		expectRefusal(t, candidate, prior, "actions[0]")
	})
}

// web_convert_param fills in its source, and setSourceVariable fills in the
// result again, so a template stored in a held value is read.
func TestValidatePreservedImportCredentialEditsReadsTemplatesStoredInHeldValues(t *testing.T) {
	prior := varHostCredentialScript()
	prior.Parameters = append(prior.Parameters, dsl.Parameter{Name: "tmpl", Type: "list", Values: []string{"{{var.cfg}}"}, Update: "once"})
	prior.Init = []dsl.Action{
		saveStep("cfg", "origin.example.test", "cfg"),
		{Name: "conv", Type: "source_parameter", Params: map[string]any{"function": "web_convert_param", "save_param": "host", "source_string": "{{param.tmpl}}"}},
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Init[0] = saveStep("cfg", "attacker.test", "cfg")
	expectRefusal(t, candidate, prior, "actions[0]")
}

// native_javascript and Java Vuser steps read and write the live tables that
// live_table steps do.
func TestValidatePreservedImportCredentialEditsHoldsEveryStepThatSharesTableState(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://{{var.js.host}}/orders"
	prior.Init = []dsl.Action{
		{Name: "seed", Type: "live_table", Params: map[string]any{"table": "hosts", "operation": "push", "values": map[string]any{"host": "origin.example.test"}}},
		{Name: "read", Type: "native_javascript", Params: map[string]any{"source": "return table.get('hosts')", "result_prefix": "js"}},
	}
	for name, edit := range map[string]func(*dsl.Script){
		"the live table step that seeds it": func(s *dsl.Script) {
			s.Init[0].Params = map[string]any{"table": "hosts", "operation": "push", "values": map[string]any{"host": "attacker.test"}}
		},
		"a native_javascript step that writes it": func(s *dsl.Script) {
			s.Init = append(s.Init, dsl.Action{Name: "poke", Type: "native_javascript", Params: map[string]any{"source": "table.push('hosts', {host: 'attacker.test'})"}})
		},
		"a Java Vuser step that writes it": func(s *dsl.Script) {
			s.Init = append(s.Init, dsl.Action{Name: "java", Type: "java_vuser_run", Params: map[string]any{"method": "run"}})
		},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
}

// Held steps count: copying a step that accumulates (lr_param_increment adds
// to its own variable every time it runs) or looping it yields a value no held
// step supplies, while moving a held step does not change what it supplies.
func TestValidatePreservedImportCredentialEditsHoldsHowOftenAStepRuns(t *testing.T) {
	increment := dsl.Action{Name: "bump", Type: "source_parameter", Params: map[string]any{"function": "lr_param_increment", "args": []any{"port", "1"}}}
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://origin.example.test:{{var.port}}/orders"
	prior.Init = []dsl.Action{saveStep("port", "8000", "port"), increment}

	t.Run("refuses a copy of the step", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, increment)
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("refuses a loop around the step", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[1] = dsl.Action{Name: "again", Type: "control_loop", Control: &dsl.ControlFlow{Count: 40, Steps: []dsl.Action{increment}}}
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("refuses moving the step to another phase", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init = candidate.Init[:1]
		candidate.Teardown = append(candidate.Teardown, increment)
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("accepts moving the step within its phase", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append([]dsl.Action{saveStep("note", "x", "note")}, candidate.Init...)
		expectAccepted(t, candidate, prior)
	})
	t.Run("accepts a copy of a step that yields the same value every time", func(t *testing.T) {
		scenario := CloneScriptDefinition(prior)
		scenario.Actions[0].Target = "https://{{var.host}}/orders"
		scenario.Init = []dsl.Action{saveStep("host", "origin.example.test", "host")}
		candidate := CloneScriptDefinition(scenario)
		candidate.Init = append(candidate.Init, scenario.Init[0])
		expectAccepted(t, candidate, scenario)
	})
}

// Steps that share a name may read each other: two extractors that read each
// other's variable are one component, and an unchanged component compares equal.
func TestValidatePreservedImportCredentialEditsHandlesVariablesThatReadEachOther(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://{{var.x}}/orders"
	prior.Init = []dsl.Action{
		{Name: "x", Type: "GET", Method: "GET", Target: "https://origin.example.test/{{var.y}}", Extractors: []dsl.Extractor{{Name: "x", Source: "body", Type: "regex", Expression: "(.*)"}}},
		{Name: "y", Type: "GET", Method: "GET", Target: "https://origin.example.test/{{var.x}}", Extractors: []dsl.Extractor{{Name: "y", Source: "body", Type: "regex", Expression: "(.*)"}}},
	}
	expectAccepted(t, CloneScriptDefinition(prior), prior)
	for _, index := range []int{0, 1} {
		t.Run(fmt.Sprintf("refuses an edit of step %d", index), func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			candidate.Init[index].Extractors[0].DefaultValue = stringPointer("attacker.test")
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
	t.Run("accepts an unrelated parameter", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[1].Values = []string{"changed"}
		expectAccepted(t, candidate, prior)
	})
}

// A header name is interpolated like its value, and support_mode selects how a
// source step behaves.
func TestValidatePreservedImportCredentialEditsReadsHeaderNamesAndSupportMode(t *testing.T) {
	t.Run("header name", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0].Target = "https://origin.example.test/orders"
		prior.Actions[0].Headers["X-{{var.tenant}}"] = "1"
		prior.Init = []dsl.Action{{Name: "t", Type: "GET", Method: "GET", Target: "https://origin.example.test/t", Extractors: []dsl.Extractor{{Name: "tenant", Source: "body", Type: "regex", Expression: "(.*)"}}}}
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Extractors[0].DefaultValue = stringPointer("attacker")
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("support mode", func(t *testing.T) {
		prior := varHostCredentialScript()
		step := saveStep("save", "origin.example.test", "host")
		step.Metadata = map[string]any{"support_mode": "native"}
		prior.Init = []dsl.Action{step}
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Metadata = map[string]any{"support_mode": "review"}
		expectRefusal(t, candidate, prior, "actions[0]")
	})
}

// A CEL condition names the variables it reads in several spellings.
func TestValidatePreservedImportCredentialEditsReadsEveryCELSpelling(t *testing.T) {
	for name, expr := range map[string]string{
		"var dot":           `var.limit == "1"`,
		"vars dot":          `vars.limit == "1"`,
		"var index":         `var["limit"] == "1"`,
		"data dot":          `data.cfg.limit == "1"`,
		"data double index": `data["cfg"]["limit"] == "1"`,
	} {
		t.Run(name, func(t *testing.T) {
			prior := sourceReviewCredentialScript()
			prior.DataSources = []dsl.DataSource{
				{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true},
				{Name: "cfg", Type: "csv", InlineData: "limit\n1\n", Strategy: "sequential", FirstRowHeader: true},
			}
			prior.Parameters = []dsl.Parameter{{Name: "limit", Type: "list", Values: []string{"1"}, Update: "once"}}
			orders := prior.Actions[0]
			orders.Target = "https://{{var.row.host}}/orders"
			prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{
				ForEach: &dsl.ForEachLoop{Source: "hosts", Var: "row", Condition: &dsl.Condition{Expr: expr}},
				Steps:   []dsl.Action{orders},
			}}
			candidate := CloneScriptDefinition(prior)
			if strings.Contains(name, "data") {
				candidate.DataSources[1].InlineData = "limit\n99\n"
			} else {
				candidate.Parameters[0].Values = []string{"99"}
			}
			expectRefusal(t, candidate, prior, "actions[0].control.steps[0]")
		})
	}
}

// Reordering parameters or data sources changes nothing a Java Vuser step
// receives: it gets them all as a set.
func TestValidatePreservedImportCredentialEditsTreatsAllParametersAsASet(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0] = dsl.Action{Name: "java", Type: "java_vuser_run", Params: map[string]any{"method": "run", "lr_parameters": map[string]any{"token": sourceReviewKeptCredential}}}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[0], candidate.Parameters[1] = candidate.Parameters[1], candidate.Parameters[0]
	expectAccepted(t, candidate, prior)
}

// The families that pin every step they name are fingerprinted once, however
// many variables of the family a request reads.
func TestValidatePreservedImportCredentialEditsBoundsTheCostOfAFamily(t *testing.T) {
	const steps = 3000
	prior := sourceReviewCredentialScript()
	prior.Protocol = "websocket"
	prior.Parameters = []dsl.Parameter{{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"}, {Name: "spare", Type: "list", Values: []string{"x"}, Update: "once"}}
	prior.Actions = prior.Actions[:0]
	for index := 0; index < steps; index++ {
		prior.Actions = append(prior.Actions, dsl.Action{
			Name: fmt.Sprintf("s%d", index), Type: "ws_send",
			Target:  fmt.Sprintf("wss://{{param.region}}.example.test/{{var.ws_%d}}", index),
			Headers: map[string]string{"Authorization": sourceReviewKeptCredential},
		})
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[1].Values = []string{"changed"}
	started := time.Now()
	expectAccepted(t, candidate, prior)
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("validating %d steps that each read a WebSocket variable took %v", steps, elapsed)
	}
	// Steps that compute their output name may write anything: they are held
	// once, not once per reference.
	for index := 0; index < steps; index++ {
		prior.Init = append(prior.Init, dsl.Action{Name: fmt.Sprintf("t%d", index), Type: "http_oauth2_token", Params: map[string]any{"save_as": fmt.Sprintf("{{param.slot}}%d", index)}})
	}
	candidate = CloneScriptDefinition(prior)
	candidate.Parameters[1].Values = []string{"changed"}
	started = time.Now()
	expectAccepted(t, candidate, prior)
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("validating %d computed-name steps took %v", steps, elapsed)
	}
}

// The route an editor's save takes: the masked draft is reconciled against the
// stored script and then validated. These are the references the guard holds,
// through that route.
func TestPreparePersistentLibraryScriptHoldsReferencedValues(t *testing.T) {
	cases := map[string]struct {
		prior func() *dsl.Script
		edit  func(*dsl.Script)
	}{
		"parameter": {referencedCredentialScript, func(s *dsl.Script) { s.Parameters[0].Values = []string{"attacker.test"} }},
		"extractor default": {producerCredentialScript, func(s *dsl.Script) {
			s.Actions[0].Extractors[0].DefaultValue = stringPointer("attacker.test")
		}},
		"new writer in init": {varHostCredentialScript, func(s *dsl.Script) { s.Init = append(s.Init, saveHostStep("seed")) }},
		"array slot": {
			func() *dsl.Script {
				prior := varHostCredentialScript()
				prior.Init = []dsl.Action{saveStep("one", "origin.example.test", "hosts_1"), saveStep("count", "1", "hosts_count"), {
					Name: "pick", Type: "source_parameter",
					Params: map[string]any{"function": "lr_save_string", "args": []any{"x", "host"}, "source_array": map[string]any{"name": "hosts", "selection": "random"}},
				}}
				return prior
			},
			func(s *dsl.Script) { s.Init[0] = saveStep("one", "attacker.test", "hosts_1") },
		},
		"JSON document": {
			func() *dsl.Script {
				prior := varHostCredentialScript()
				prior.Init = []dsl.Action{
					jsonStep("lr_eval_json", map[string]any{"json_object": "doc", "buffer": `{"host":"origin.example.test"}`}),
					jsonStep("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "host", "query": "$.host"}),
				}
				return prior
			},
			func(s *dsl.Script) {
				s.Init[0] = jsonStep("lr_eval_json", map[string]any{"json_object": "doc", "buffer": `{"host":"attacker.test"}`})
			},
		},
	}
	for name, fixture := range cases {
		t.Run("refuses "+name, func(t *testing.T) {
			prior := fixture.prior()
			disclosed := MaskScriptSecrets(prior)
			fixture.edit(disclosed)
			saved, err := PreparePersistentLibraryScript(disclosed, prior)
			if err == nil || !strings.Contains(err.Error(), "retained imported credentials") {
				t.Fatalf("%v (saved %v), want the refusal", err, saved != nil)
			}
		})
	}
	t.Run("accepts a parameter nothing reads", func(t *testing.T) {
		prior := referencedCredentialScript()
		disclosed := MaskScriptSecrets(prior)
		disclosed.Parameters[1].Values = []string{"east"}
		saved, err := PreparePersistentLibraryScript(disclosed, prior)
		if err != nil {
			t.Fatal(err)
		}
		if got := saved.Actions[0].Headers["Authorization"]; got != sourceReviewKeptCredential {
			t.Fatalf("Authorization = %q, want the kept credential", got)
		}
	})
}

// A template stored in a data source's rows is read when a source step saves
// the row's value, as one stored in a parameter is.
func TestValidatePreservedImportCredentialEditsReadsTemplatesStoredInDataRows(t *testing.T) {
	prior := varHostCredentialScript()
	prior.DataSources = []dsl.DataSource{{Name: "cfg", Type: "csv", InlineData: "tmpl\n{{var.origin}}\n", Strategy: "sequential", FirstRowHeader: true}}
	prior.Init = []dsl.Action{
		saveStep("origin", "origin.example.test", "origin"),
		{Name: "conv", Type: "source_parameter", Params: map[string]any{"function": "web_convert_param", "save_param": "host", "source_string": "{{data.cfg.tmpl}}"}},
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Init[0] = saveStep("origin", "attacker.test", "origin")
	expectRefusal(t, candidate, prior, "actions[0]")
}

// The IP spoofing steps keep their state under a name of their own.
func TestValidatePreservedImportCredentialEditsPinsIPSpoofingState(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://{{var.__lr_ip_spoofing}}.example.test/orders"
	candidate := CloneScriptDefinition(prior)
	candidate.Init = append(candidate.Init, dsl.Action{Name: "spoof", Type: "source_runtime", Params: map[string]any{"function": "lr_enable_ip_spoofing"}})
	expectRefusal(t, candidate, prior, "actions[0]")
}

// A script whose executable form cannot be read cannot be compared, so the
// guard holds.
func TestImportReferenceGuardHoldsWhenTheExecutableScriptIsUnreadable(t *testing.T) {
	projected := map[string]any{"actions": []any{map[string]any{"name": "a", "type": "GET"}}}
	guard := newImportReferenceGuard("not a map", projected, projected, projected)
	if !guard.requestChanged("/actions/0") || !guard.scriptChanged() {
		t.Fatal("an unreadable candidate must hold")
	}
	readable := newImportReferenceGuard(projected, projected, projected, projected)
	if readable.requestChanged("/actions/0") || readable.scriptChanged() {
		t.Fatal("identical scripts must compare equal")
	}
}

// The writer index matches names the way the runtime publishes them.
func TestImportReferenceScopeMatchesWriterNames(t *testing.T) {
	scope := newImportReferenceScope(map[string]any{
		"actions": []any{
			map[string]any{"name": "g", "type": "GET", "extractors": []any{
				map[string]any{"name": "token"},
				map[string]any{"name": "items", "mode": "all"},
			}},
			map[string]any{"name": "js", "type": "native_javascript", "params": map[string]any{"result_prefix": "out"}},
			map[string]any{"name": "slot", "type": "source_parameter", "params": map[string]any{"function": "lr_save_string", "args": []any{"x", "Rows_2"}}},
		},
	}, newImportInterner())
	for name, want := range map[string]int{
		"token":      1, // exact
		"TOKEN":      1,
		"items":      1, // the array itself
		"items_3":    1, // a slot
		"items_n":    0,
		"out":        1, // the prefix itself
		"out.status": 1, // a field under the prefix
		"outside":    0,
		"rows_2":     1, // a slot written by hand
		"nothing":    0,
	} {
		if got := len(scope.namedWriters(name)); got != want {
			t.Errorf("namedWriters(%q) = %d writers, want %d", name, got, want)
		}
	}
	if got := len(scope.arrayWriterSet("rows")); got != 1 {
		t.Errorf("arrayWriterSet(rows) = %d writers, want the hand-written slot", got)
	}
}

// Digests cannot be made equal by moving text between their parts.
func TestImportInternerFramesItsParts(t *testing.T) {
	interner := newImportInterner()
	token := interner.token
	if token("ab", "c") == token("a", "bc") || token("a", "") == token("", "a") || token("a") == token("a", "") || token("a:b") == token("a", "b") || token("a", "b") != token("a", "b") {
		t.Fatal("token does not tell apart different splits of the same text, or differs for equal parts")
	}
}

// A producer hands data to the consumers of its family: a consumer's extractor
// publishes what the script itself wrote to the topic or queue, whatever the
// variable is called.
func TestValidatePreservedImportCredentialEditsHoldsEveryStepOfABrokerFamily(t *testing.T) {
	for name, fixture := range map[string]struct {
		protocol, produce, consume string
	}{
		"Kafka":      {"kafka", "kafka_produce", "kafka_consume"},
		"JMS":        {"jms", "jms_publish", "jms_consume"},
		"Event Hubs": {"eventhubs", "eventhubs_produce", "eventhubs_consume"},
	} {
		t.Run(name, func(t *testing.T) {
			prior := sourceReviewCredentialScript()
			prior.Protocol = fixture.protocol
			prior.Actions = []dsl.Action{
				{Name: "announce", Type: fixture.produce, Target: "control", Body: `{"topic":"orders-origin"}`, Metadata: map[string]any{"x-topic": "orders-origin"}},
				{Name: "read", Type: fixture.consume, Target: "control", Extractors: []dsl.Extractor{{Name: "topic", Source: "body", Type: "regex", Expression: `"topic":"(.*?)"`}}},
				{Name: "send", Type: fixture.produce, Target: "{{var.topic}}", Body: "payload", Headers: map[string]string{"Authorization": sourceReviewKeptCredential}},
				{Name: "audit", Type: fixture.produce, Target: "audit", Body: "x"},
			}
			prior.Metadata.Import.Preserved["source_review"].(map[string]any)["mappings"] = []any{map[string]any{"source_file": "Action.c", "source_line": 4, "action_path": "actions[2]"}}
			edits := map[string]func(*dsl.Script){
				"the producer's body":        func(s *dsl.Script) { s.Actions[0].Body = `{"topic":"attacker-topic"}` },
				"the producer's target":      func(s *dsl.Script) { s.Actions[0].Target = "attacker-control" },
				"the consumer's target":      func(s *dsl.Script) { s.Actions[1].Target = "other" },
				"another step of the family": func(s *dsl.Script) { s.Actions[3].Body = "changed" },
			}
			if fixture.protocol != "eventhubs" {
				// Kafka sends metadata as record headers and JMS as message
				// properties; the Event Hubs adapter sends none.
				edits["the producer's metadata"] = func(s *dsl.Script) { s.Actions[0].Metadata = map[string]any{"x-topic": "attacker-topic"} }
			}
			for edit, apply := range edits {
				t.Run("refuses "+edit, func(t *testing.T) {
					candidate := CloneScriptDefinition(prior)
					apply(candidate)
					expectRefusal(t, candidate, prior, "actions[2]")
				})
			}
			t.Run("accepts a parameter nothing reads", func(t *testing.T) {
				candidate := CloneScriptDefinition(prior)
				candidate.Parameters = append(candidate.Parameters, dsl.Parameter{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"})
				expectAccepted(t, candidate, prior)
			})
		})
	}
}

// A plain extractor in a loop does not accumulate: the loop's count stays
// editable.
func TestValidatePreservedImportCredentialEditsLeavesLoopsAroundPlainExtractorsEditable(t *testing.T) {
	prior := sourceReviewCredentialScript()
	orders := prior.Actions[0]
	orders.Target = "https://origin.example.test/orders/{{var.orderId}}"
	prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{
		{Name: "login", Type: "POST", Method: "POST", Target: "https://origin.example.test/login", Extractors: []dsl.Extractor{{Name: "orderId", Source: "body", Type: "regex", Expression: "(.*)"}}},
		orders,
	}}}
	candidate := CloneScriptDefinition(prior)
	candidate.Actions[0].Control.Count = 10
	candidate.Actions[0].Control.MaxIterations = 50
	expectAccepted(t, candidate, prior)
}

// The Java process also receives the values steps save under a data source's
// name.
func TestValidatePreservedImportCredentialEditsHoldsDataSourceVariablesForAJavaStep(t *testing.T) {
	prior := referencedCredentialScript()
	prior.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "endpoint\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true}}
	prior.Actions[0] = dsl.Action{Name: "java", Type: "java_vuser_run", Params: map[string]any{"method": "run", "lr_parameters": map[string]any{"token": sourceReviewKeptCredential}}}
	for name, step := range map[string]dsl.Action{
		"saved by a step":     saveStep("save", "attacker.test", "hosts.endpoint"),
		"extracted by a step": {Name: "x", Type: "GET", Method: "GET", Target: "https://origin.example.test/x", Extractors: []dsl.Extractor{{Name: "hosts.endpoint", Source: "body", Type: "regex", Expression: "(.*)"}}},
	} {
		t.Run("refuses a variable "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			candidate.Init = append(candidate.Init, step)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
	t.Run("accepts a variable of another name", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, saveStep("save", "x", "regions.endpoint"))
		expectAccepted(t, candidate, prior)
	})
}

// A branch name with very many dots holds every writer, not only the
// parameters and data sources.
func TestValidatePreservedImportCredentialEditsHoldsEveryWriterForAnOverlongBranchName(t *testing.T) {
	branch := strings.TrimSuffix(strings.Repeat("a.", 70), ".")
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://{{var.parallel." + branch + ".host}}/orders"
	prior.Init = []dsl.Action{{Name: "fork", Type: "control_parallel", Control: &dsl.ControlFlow{
		Branches: []dsl.ParallelBranch{{Name: branch, Steps: []dsl.Action{{
			Name: "seed", Type: "GET", Method: "GET", Target: "https://origin.example.test/seed",
			Extractors: []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: "(.*)"}},
		}}}},
	}}}
	for name, edit := range map[string]func(*dsl.Script){
		"the extractor": func(s *dsl.Script) {
			s.Init[0].Control.Branches[0].Steps[0].Extractors[0].DefaultValue = stringPointer("attacker.test")
		},
		"the request": func(s *dsl.Script) { s.Init[0].Control.Branches[0].Steps[0].Target = "https://attacker.test/seed" },
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
}

// Steps that share state are fingerprinted as a group once, not once per step.
func TestValidatePreservedImportCredentialEditsBoundsTheCostOfSharedState(t *testing.T) {
	const steps = 1500
	prior := sourceReviewCredentialScript()
	prior.Parameters = []dsl.Parameter{{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"}, {Name: "spare", Type: "list", Values: []string{"x"}, Update: "once"}}
	prior.Actions = prior.Actions[:0]
	for index := 0; index < steps; index++ {
		prior.Init = append(prior.Init,
			dsl.Action{Name: fmt.Sprintf("js%d", index), Type: "native_javascript", Params: map[string]any{"source": "return 1", "result_prefix": fmt.Sprintf("js%d", index)}},
			jsonStep("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": fmt.Sprintf("v%d", index), "query": fmt.Sprintf("{{var.v%d}}", index+1)}),
		)
		prior.Actions = append(prior.Actions, dsl.Action{
			Name: fmt.Sprintf("r%d", index), Type: "GET", Method: "GET",
			Target:  fmt.Sprintf("https://{{param.region}}.example.test/{{var.js%d.x}}/{{var.v%d}}", index, index),
			Headers: map[string]string{"Authorization": sourceReviewKeptCredential},
		})
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[1].Values = []string{"changed"}
	started := time.Now()
	expectAccepted(t, candidate, prior)
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("validating %d requests that read table and document state took %v", steps, elapsed)
	}
}

// A source step takes its function from its metadata only when its params do
// not say: metadata beside a params.function is evidence and stays editable.
func TestValidatePreservedImportCredentialEditsReadsMetadataFunctionOnlyWhenParamsDoNotSayIt(t *testing.T) {
	prior := varHostCredentialScript()
	step := saveStep("save", "origin.example.test", "host")
	step.Metadata = map[string]any{"source_function": "lr_save_string", "support_mode": "native"}
	step.Params["support_mode"] = "native"
	prior.Init = []dsl.Action{step}
	candidate := CloneScriptDefinition(prior)
	candidate.Init[0].Metadata = map[string]any{"source_function": "lr_save_int", "support_mode": "review"}
	expectAccepted(t, candidate, prior)
}

// The number of iterations is a load setting and stays editable although a
// counter or an accumulating step yields a different value on each iteration.
// This is a documented limit, not an oversight.
func TestValidatePreservedImportCredentialEditsLeavesRunLogicEditable(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://origin.example.test:{{var.port}}/orders"
	prior.Init = []dsl.Action{saveStep("port", "8000", "port"), {Name: "bump", Type: "source_parameter", Params: map[string]any{"function": "lr_param_increment", "args": []any{"port", "1"}}}}
	prior.Config.RunLogic.Iterations = 3
	candidate := CloneScriptDefinition(prior)
	candidate.Config.RunLogic.Iterations = 200
	expectAccepted(t, candidate, prior)
}

// A CEL expression tolerates space between a namespace and what it indexes.
func TestValidatePreservedImportCredentialEditsReadsSpacedCELReferences(t *testing.T) {
	for _, expr := range []string{`var . limit == "1"`, `vars [ 'limit' ] == "1"`, `data . cfg . limit == "1"`} {
		t.Run(expr, func(t *testing.T) {
			var out []string
			collectImportCELReferences("expr", expr, &out)
			if len(out) == 0 {
				t.Fatalf("no variable found in %q", expr)
			}
		})
	}
}

// A Java Vuser step that a request reaches through a variable it publishes
// also receives the values steps save under a data source's name.
func TestValidatePreservedImportCredentialEditsHoldsDataSourceVariablesForAReachedJavaStep(t *testing.T) {
	prior := referencedCredentialScript()
	prior.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "endpoint\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true}}
	prior.Actions[0].Target = "https://origin.example.test/orders/{{var.java_vuser_duration_ms}}"
	prior.Init = []dsl.Action{{Name: "java", Type: "java_vuser_run", Params: map[string]any{"method": "run"}}}
	candidate := CloneScriptDefinition(prior)
	candidate.Init = append(candidate.Init, saveStep("save", "attacker.test", "hosts.endpoint"))
	expectRefusal(t, candidate, prior, "actions[0]")
}

// A held step that reads the pass counter is held with the loops around it.
func TestValidatePreservedImportCredentialEditsHoldsLoopsAroundAStepThatReadsLoopIndex(t *testing.T) {
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{
		saveStep("host", "10.0.0.{{var.loop_index}}", "host"),
	}}}}
	candidate := CloneScriptDefinition(prior)
	candidate.Init[0].Control.Count = 255
	expectRefusal(t, candidate, prior, "actions[0]")
}

// A step that reads the variable it writes accumulates: another copy of it
// changes the value, where another copy of a step that sets a constant does not.
func TestValidatePreservedImportCredentialEditsCountsStepsThatAppendToTheirOwnVariable(t *testing.T) {
	appendLabel := dsl.Action{Name: "append", Type: "source_parameter", Params: map[string]any{"function": "lr_param_sprintf", "args": []any{"host", "{{var.host}}.x"}}}
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{saveStep("host", "origin", "host"), appendLabel}
	candidate := CloneScriptDefinition(prior)
	candidate.Init = append(candidate.Init, appendLabel)
	expectRefusal(t, candidate, prior, "actions[0]")
}

// How often a loop runs an accumulating step is decided by the loop's
// condition, so what the condition reads is held too.
func TestValidatePreservedImportCredentialEditsFollowsTheConditionOfALoopAroundAnAccumulator(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Parameters = append(prior.Parameters, dsl.Parameter{Name: "limit", Type: "list", Values: []string{"8010"}, Update: "once"})
	prior.Actions[0].Target = "https://origin.example.test:{{var.port}}/orders"
	prior.Init = []dsl.Action{
		saveStep("port", "8000", "port"),
		{Name: "until", Type: "control_loop", Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Left: "{{var.port}}", Op: "lt", Right: "{{param.limit}}"},
			Steps:     []dsl.Action{{Name: "bump", Type: "source_parameter", Params: map[string]any{"function": "lr_param_increment", "args": []any{"port", "1"}}}},
		}},
	}
	unrelated := CloneScriptDefinition(prior)
	unrelated.Parameters[1].Values = []string{"changed"}
	expectAccepted(t, unrelated, prior)

	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[2].Values = []string{"65000"}
	expectRefusal(t, candidate, prior, "actions[0]")
}

// A Java Vuser keeps one JVM across its steps and the table steps share the
// run's live tables, so a credential-bearing step of those types holds the
// others, not only the steps a variable names.
func TestValidatePreservedImportCredentialEditsHoldsTheSessionOfACredentialBearingJavaStep(t *testing.T) {
	newPrior := func() *dsl.Script {
		prior := sourceReviewCredentialScript()
		prior.Protocol = "java_vuser"
		prior.Config.BaseURL = ""
		prior.Init = []dsl.Action{{Name: "init", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "init", "lr_parameters": map[string]any{"host": "origin.example.test"}}}}
		prior.Actions = []dsl.Action{{Name: "action", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "action", "lr_parameters": map[string]any{"password": sourceReviewKeptCredential}}}}
		prior.Metadata.Import.Preserved["source_review"].(map[string]any)["mappings"] = []any{map[string]any{"source_file": "Action.c", "source_line": 4, "action_path": "actions[0]"}}
		return prior
	}
	for name, edit := range map[string]func(*dsl.Script){
		"a sibling step's parameters": func(s *dsl.Script) {
			s.Init[0].Params = map[string]any{"method": "init", "lr_parameters": map[string]any{"host": "attacker.test"}}
		},
		"a new sibling step": func(s *dsl.Script) {
			s.Init = append(s.Init, dsl.Action{Name: "more", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "init", "lr_parameters": map[string]any{"host": "attacker.test"}}})
		},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			prior := newPrior()
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
	t.Run("refuses a live table beside a credential-bearing native_javascript step", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0] = dsl.Action{Name: "js", Type: "native_javascript", Params: map[string]any{"source": "return 1", "inputs": map[string]any{"token": sourceReviewKeptCredential}}}
		prior.Init = []dsl.Action{{Name: "seed", Type: "live_table", Params: map[string]any{"table": "hosts", "operation": "push", "values": map[string]any{"host": "origin.example.test"}}}}
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Params = map[string]any{"table": "hosts", "operation": "push", "values": map[string]any{"host": "attacker.test"}}
		expectRefusal(t, candidate, prior, "actions[0]")
	})
}

// {{data.x}} reads the same runtime variable as {{var.x}}.
func TestValidatePreservedImportCredentialEditsReadsDataSpellingsLikeVar(t *testing.T) {
	t.Run("loop_index", func(t *testing.T) {
		prior := sourceReviewCredentialScript()
		orders := prior.Actions[0]
		orders.Target = "https://10.0.0.{{data.loop_index}}/orders"
		prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{orders}}}
		candidate := CloneScriptDefinition(prior)
		candidate.Actions[0].Control.Count = 255
		expectRefusal(t, candidate, prior, "actions[0].control.steps[0]")
	})
	t.Run("a step that appends to its own variable", func(t *testing.T) {
		appendLabel := dsl.Action{Name: "append", Type: "source_parameter", Params: map[string]any{"function": "lr_param_sprintf", "args": []any{"host", "{{data.host}}.x"}}}
		prior := varHostCredentialScript()
		prior.Init = []dsl.Action{saveStep("host", "origin", "host"), appendLabel}
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, appendLabel)
		expectRefusal(t, candidate, prior, "actions[0]")
	})
}

// A step accumulates when the value it computes feeds back into it: round a
// cycle of steps, through a result prefix, or through the default value of its
// extractor.
func TestValidatePreservedImportCredentialEditsCountsEveryKindOfAccumulation(t *testing.T) {
	cases := map[string]struct {
		init  []dsl.Action
		reads string
	}{
		"a cycle of two steps": {
			init: []dsl.Action{
				saveStep("seed", "origin", "host"),
				{Name: "a", Type: "source_parameter", Params: map[string]any{"function": "lr_param_sprintf", "args": []any{"h2", "{{var.host}}1"}}},
				{Name: "b", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{var.h2}}", "host"}}},
			},
			reads: "{{var.host}}",
		},
		"a result prefix read back": {
			init:  []dsl.Action{{Name: "counter", Type: "native_javascript", Params: map[string]any{"source": "return {n: n + 1}", "inputs": map[string]any{"n": "{{var.ctr.n}}"}, "result_prefix": "ctr"}}},
			reads: "{{var.ctr.n}}",
		},
		"the default value of an extractor": {
			init: []dsl.Action{{Name: "x", Type: "GET", Method: "GET", Target: "https://origin.example.test/x",
				Extractors: []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: "(.*)", DefaultValue: stringPointer("{{var.host}}1")}}}},
			reads: "{{var.host}}",
		},
	}
	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			prior := referencedCredentialScript()
			prior.Actions[0].Target = "https://" + fixture.reads + "/orders"
			prior.Init = fixture.init
			candidate := CloneScriptDefinition(prior)
			candidate.Init = append(candidate.Init, fixture.init...)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
	t.Run("an extractor that overwrites its variable from the response does not accumulate", func(t *testing.T) {
		prior := varHostCredentialScript()
		step := hostExtractorStep("seed")
		step.Extractors[0].DefaultValue = nil
		prior.Init = []dsl.Action{step}
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, step)
		expectAccepted(t, candidate, prior)
	})
}

// A loop around a step whose extractor reads the pass counter is held.
func TestValidatePreservedImportCredentialEditsHoldsLoopsAroundExtractorsThatReadLoopIndex(t *testing.T) {
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{{
		Name: "x", Type: "GET", Method: "GET", Target: "https://origin.example.test/x",
		Extractors: []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: "(.*)", DefaultValue: stringPointer("10.0.0.{{var.loop_index}}")}},
	}}}}}
	candidate := CloneScriptDefinition(prior)
	candidate.Init[0].Control.Count = 255
	expectRefusal(t, candidate, prior, "actions[0]")
}

// A JSON step that only reads a document does not hold the other readers of it:
// the steps that make the document are its content.
func TestValidatePreservedImportCredentialEditsLeavesOtherReadersOfADocumentEditable(t *testing.T) {
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{
		jsonStep("lr_eval_json", map[string]any{"json_object": "doc", "buffer": `{"host":"origin.example.test","token":"t"}`}),
		jsonStep("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "host", "query": "$.host"}),
		jsonStep("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "token", "query": "$.token"}),
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Init[2] = jsonStep("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "token", "query": "$.access_token"})
	expectAccepted(t, candidate, prior)
	// The step that reads the variable the request needs is still held.
	candidate = CloneScriptDefinition(prior)
	candidate.Init[1] = jsonStep("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "host", "query": "$.other"})
	expectRefusal(t, candidate, prior, "actions[0]")
}

// Copying a step that sets a constant adds nothing, whether or not it has an
// extractor.
func TestValidatePreservedImportCredentialEditsAcceptsACopyOfAConstantExtractorStep(t *testing.T) {
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{hostExtractorStep("seed")}
	candidate := CloneScriptDefinition(prior)
	candidate.Teardown = append(candidate.Teardown, prior.Init[0])
	expectAccepted(t, candidate, prior)
}

// A condition that tests a whole namespace reads every variable.
func TestValidatePreservedImportCredentialEditsReadsWholeNamespaceConditions(t *testing.T) {
	prior := sourceReviewCredentialScript()
	prior.Actions[0].Target = "https://10.0.0.{{var.loop_index}}/orders"
	prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{
		Condition: &dsl.Condition{Expr: `"stop" in vars`},
		Steps:     []dsl.Action{prior.Actions[0]},
	}}
	candidate := CloneScriptDefinition(prior)
	candidate.Init = append(candidate.Init, saveStep("stop", "yes", "stop"))
	expectRefusal(t, candidate, prior, "actions[0].control.steps[0]")
}

// The graph is a general directed graph: a cycle of three references entered
// from two different requests compares the same whichever is visited first.
func TestImportReferenceGraphFingerprintsCyclesIndependentlyOfVisitOrder(t *testing.T) {
	build := func() *dsl.Script {
		script := referencedCredentialScript()
		for _, name := range []string{"a", "b", "c"} {
			next := map[string]string{"a": "b", "b": "c", "c": "a"}[name]
			script.Init = append(script.Init, dsl.Action{
				Name: name, Type: "GET", Method: "GET", Target: "https://origin.example.test/{{var." + next + "}}",
				Extractors: []dsl.Extractor{{Name: name, Source: "body", Type: "regex", Expression: "(.*)"}},
			})
		}
		script.Actions[0].Target = "https://{{var.a}}/one"
		script.Actions[1].Target = "https://{{var.c}}/two"
		script.Actions[1].Headers = map[string]string{"Authorization": sourceReviewKeptCredential}
		return script
	}
	for _, order := range [][2]string{{"/actions/0", "/actions/1"}, {"/actions/1", "/actions/0"}} {
		raw, _ := ScriptAsJSONValue(build())
		interner := newImportInterner()
		scope := newImportReferenceScope(raw.(map[string]any), interner)
		first, second := scope.dependencies(order[0]), scope.dependencies(order[1])
		raw2, _ := ScriptAsJSONValue(build())
		scope2 := newImportReferenceScope(raw2.(map[string]any), interner)
		if scope2.dependencies(order[1]) != second || scope2.dependencies(order[0]) != first {
			t.Fatalf("fingerprints depend on the order requests are visited in (%v)", order)
		}
	}
}

// accumulating finds every step on a cycle of computing steps, however the cycle
// is entered, and a step that reads its own output.
func TestImportReferenceScopeFindsAccumulatingSteps(t *testing.T) {
	sprintf := func(name, target, format string) dsl.Action {
		return dsl.Action{Name: name, Type: "source_parameter", Params: map[string]any{"function": "lr_param_sprintf", "args": []any{target, format}}}
	}
	script := &dsl.Script{
		Name: "accumulators", Version: "1", Protocol: "http",
		Actions: []dsl.Action{
			saveStep("entry", "x", "e"),
			sprintf("a", "ha", "{{var.hc}}1"), // a reads hc, written by c
			sprintf("b", "hb", "{{var.ha}}1"), // b reads ha, written by a
			sprintf("c", "hc", "{{var.hb}}1"), // c reads hb, written by b: a, b, c form a cycle
			sprintf("self", "hs", "{{var.hs}}1"),
			saveStep("plain", "x", "hp"),
			sprintf("reader", "hr", "{{var.hp}}1"), // reads a constant: no cycle
			{Name: "bump", Type: "source_parameter", Params: map[string]any{"function": "lr_param_increment", "args": []any{"n", "1"}}},
		},
	}
	value, err := ScriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	scope := newImportReferenceScope(value.(map[string]any), newImportInterner())
	for path, want := range map[string]bool{
		"/actions/0": false, "/actions/1": true, "/actions/2": true, "/actions/3": true,
		"/actions/4": true, "/actions/5": false, "/actions/6": false, "/actions/7": true,
	} {
		if got := scope.accumulating(path); got != want {
			t.Errorf("accumulating(%s) = %v, want %v", path, got, want)
		}
	}
}

// Another loop around a step that appends to its own variable runs it again.
func TestValidatePreservedImportCredentialEditsHoldsALoopAroundASelfAppendingStep(t *testing.T) {
	appendLabel := dsl.Action{Name: "append", Type: "source_parameter", Params: map[string]any{"function": "lr_param_sprintf", "args": []any{"host", "{{var.host}}.x"}}}
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{saveStep("host", "origin", "host"), appendLabel}
	candidate := CloneScriptDefinition(prior)
	candidate.Init[1] = dsl.Action{Name: "again", Type: "control_loop", Control: &dsl.ControlFlow{Count: 40, Steps: []dsl.Action{appendLabel}}}
	expectRefusal(t, candidate, prior, "actions[0]")
}

// writers returns every step and extractor that may publish the variable name:
// the named ones and the steps that write any name.
func (s *importReferenceScope) writers(name string) []importWriter {
	matched := s.namedWriters(name)
	return append(matched, s.anyWriters...)
}

// A CEL condition names the variables it reads in several spellings, and a
// namespace used as a whole reads every entry.
func TestImportCELReads(t *testing.T) {
	for expr, want := range map[string]string{
		`var.limit == "1"`:                   "var.limit",
		`vars.limit == "1"`:                  "var.limit",
		`vars . limit`:                       "var.limit",
		`vars['limit']`:                      "var.limit",
		`var["a"]["b"]`:                      "var.a.b",
		`vars.a["b"]`:                        "var.a.b",
		`vars.limits.max != vars.port`:       "var.limits.max var.port",
		`data.s.c == 1`:                      "data.s.c",
		`data["s"]["c"] == 1`:                "data.s.c",
		`vars.cfg.size() > 1`:                "var.cfg",
		`vars.size() < 40`:                   "*",
		`vars.exists(k, k == "x")`:           "*",
		`!vars.all(k, k != "x")`:             "*",
		`size(vars) > 1`:                     "*",
		`"x" in vars`:                        "*",
		`vars[name] == "1"`:                  "*",
		`vars.a[key] == "1"`:                 "* var.a",
		`data.exists(k, k == "x")`:           "*",
		`vars.limit == "vars.other"`:         "var.limit",
		`'var.x' == var.y`:                   "var.y",
		`foo.vars.x == 1`:                    "",
		`env.region == "1"`:                  "",
		`int(var.loop_index) <= int(vars.n)`: "var.loop_index var.n",
	} {
		if got := strings.Join(importCELReads(expr), " "); got != want {
			t.Errorf("importCELReads(%q) = %q, want %q", expr, got, want)
		}
	}
}

// What a loop's CEL condition reads decides how often an accumulating step
// runs, whichever way it spells the read.
func TestValidatePreservedImportCredentialEditsFollowsEveryCELReadOfALoop(t *testing.T) {
	newPrior := func(expr string) *dsl.Script {
		prior := referencedCredentialScript()
		prior.Actions[0].Target = "https://origin.example.test:{{var.port}}/orders"
		prior.DataSources = []dsl.DataSource{{Name: "limits", Type: "csv", InlineData: "max\n8010\n", Strategy: "sequential", FirstRowHeader: true}}
		prior.Init = []dsl.Action{
			saveStep("port", "8000", "port"),
			saveStep("cfg", "8010", "cfg.limit"),
			{Name: "until", Type: "control_loop", Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Expr: expr},
				Steps:     []dsl.Action{{Name: "bump", Type: "source_parameter", Params: map[string]any{"function": "lr_param_increment", "args": []any{"port", "1"}}}},
			}},
		}
		return prior
	}
	t.Run("a column of a data source", func(t *testing.T) {
		prior := newPrior(`int(vars.port) < int(vars.limits.max)`)
		candidate := CloneScriptDefinition(prior)
		candidate.DataSources[0].InlineData = "max\n9999\n"
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("a variable saved under a dotted name", func(t *testing.T) {
		prior := newPrior(`int(vars.port) < int(vars.cfg.limit)`)
		candidate := CloneScriptDefinition(prior)
		candidate.Init[1] = saveStep("cfg", "9999", "cfg.limit")
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("a new variable seen through a map method", func(t *testing.T) {
		prior := newPrior(`!vars.exists(k, k == "stop")`)
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append([]dsl.Action{saveStep("stop", "1", "stop")}, candidate.Init...)
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("a whole namespace holds every parameter", func(t *testing.T) {
		prior := newPrior(`int(vars.port) < size(vars) * 1000`)
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters = append(candidate.Parameters, dsl.Parameter{Name: "extra", Type: "list", Values: []string{"x"}, Update: "once"})
		expectRefusal(t, candidate, prior, "actions[0]")
	})
}

// The record headers a producer sends are filled in first: what they read is
// held with the producer.
func TestValidatePreservedImportCredentialEditsFollowsTemplatesInASentHeader(t *testing.T) {
	prior := sourceReviewCredentialScript()
	prior.Protocol = "kafka"
	prior.Parameters = []dsl.Parameter{{Name: "dest", Type: "list", Values: []string{"orders-origin"}, Update: "once"}}
	prior.Actions = []dsl.Action{
		{Name: "announce", Type: "kafka_produce", Target: "control", Body: "x", Metadata: map[string]any{"x-dest": "{{param.dest}}"}},
		{Name: "read", Type: "kafka_consume", Target: "control", Extractors: []dsl.Extractor{{Name: "topic", Source: "header", Type: "header", Expression: "x-dest"}}},
		{Name: "send", Type: "kafka_produce", Target: "{{var.topic}}", Body: "payload", Headers: map[string]string{"Authorization": sourceReviewKeptCredential}},
	}
	prior.Metadata.Import.Preserved["source_review"].(map[string]any)["mappings"] = []any{map[string]any{"source_file": "Action.c", "source_line": 4, "action_path": "actions[2]"}}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[0].Values = []string{"attacker-topic"}
	expectRefusal(t, candidate, prior, "actions[2]")
}

// A held Kafka step holds the steps of its family, not the engine's own steps in
// the same script.
func TestValidatePreservedImportCredentialEditsLeavesEngineStepsOutOfAProtocolFamily(t *testing.T) {
	prior := sourceReviewCredentialScript()
	prior.Protocol = "kafka"
	prior.Actions = []dsl.Action{
		{Name: "read", Type: "kafka_consume", Target: "control", Extractors: []dsl.Extractor{{Name: "topic", Source: "body", Type: "regex", Expression: "(.*)"}}},
		{Name: "note", Type: "source_message", Params: map[string]any{"function": "lr_output_message", "args": []any{"hello"}}},
		{Name: "send", Type: "kafka_produce", Target: "{{var.topic}}", Body: "payload", Headers: map[string]string{"Authorization": sourceReviewKeptCredential}},
	}
	prior.Metadata.Import.Preserved["source_review"].(map[string]any)["mappings"] = []any{map[string]any{"source_file": "Action.c", "source_line": 4, "action_path": "actions[2]"}}
	candidate := CloneScriptDefinition(prior)
	candidate.Actions[1].Params = map[string]any{"function": "lr_output_message", "args": []any{"edited"}}
	expectAccepted(t, candidate, prior)
	// A request of the family's protocol is still a member.
	protocolWide := sourceReviewCredentialScript()
	protocolWide.Protocol = "soap"
	protocolWide.Actions[0].Target = "https://origin.example.test/{{var.soap_endpoint}}"
	candidate = CloneScriptDefinition(protocolWide)
	candidate.Actions[1].Target = "https://attacker.test/cart"
	expectRefusal(t, candidate, protocolWide, "actions[0]")
}

// A condition that reads a namespace as a whole also sees the variables the
// protocol families publish.
func TestValidatePreservedImportCredentialEditsHoldsFamiliesForAWholeNamespaceCondition(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://origin.example.test:{{var.port}}/orders"
	prior.Init = []dsl.Action{
		saveStep("port", "8000", "port"),
		{Name: "until", Type: "control_loop", Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Expr: `vars.size() < 40`},
			Steps:     []dsl.Action{{Name: "bump", Type: "source_parameter", Params: map[string]any{"function": "lr_param_increment", "args": []any{"port", "1"}}}},
		}},
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Init = append(candidate.Init, dsl.Action{Name: "consume", Type: "kafka_consume", Target: "orders"})
	expectRefusal(t, candidate, prior, "actions[0]")
}
