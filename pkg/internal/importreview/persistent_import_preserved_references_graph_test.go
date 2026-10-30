package importreview

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

func saveStep(name, value, as string) dsl.Action {
	return dsl.Action{Name: name, Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{value, as}}}
}

// lr_save_string with params.source_array picks one entry of an array by name:
// it reads name_count and name_<n>, however they are written.
func TestValidatePreservedImportCredentialEditsPinsEveryArraySlot(t *testing.T) {
	pick := dsl.Action{
		Name: "pick", Type: "source_parameter",
		Params: map[string]any{
			"function": "lr_save_string", "args": []any{"unused", "host"},
			"source_array": map[string]any{"name": "Hosts", "selection": "random"},
		},
	}
	byHand := varHostCredentialScript()
	byHand.Init = []dsl.Action{saveStep("one", "origin.example.test", "hosts_1"), saveStep("count", "1", "hosts_count"), pick}

	for name, edit := range map[string]func(*dsl.Script){
		"a slot's value": func(s *dsl.Script) { s.Init[0] = saveStep("one", "attacker.test", "hosts_1") },
		"the count":      func(s *dsl.Script) { s.Init[1] = saveStep("count", "2", "hosts_count") },
		"a new slot": func(s *dsl.Script) {
			s.Init = append([]dsl.Action{saveStep("two", "attacker.test", "hosts_2")}, s.Init...)
		},
		"a slot written by an extractor": func(s *dsl.Script) {
			s.Init = append(s.Init, dsl.Action{
				Name: "slot", Type: "GET", Method: "GET", Target: "https://origin.example.test/slot",
				Extractors: []dsl.Extractor{{Name: "HOSTS_3", Source: "body", Type: "regex", Expression: "(.*)", DefaultValue: stringPointer("attacker.test")}},
			})
		},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(byHand)
			edit(candidate)
			expectRefusal(t, candidate, byHand, "actions[0]")
		})
	}
	t.Run("accepts a variable that is not a slot", func(t *testing.T) {
		candidate := CloneScriptDefinition(byHand)
		candidate.Init = append(candidate.Init, saveStep("note", "x", "hosts_note"), saveStep("other", "x", "hostsfile"))
		expectAccepted(t, candidate, byHand)
	})
}

// The runtime reads a JsonObject name from params.json_object or, for older
// imports, from JsonObject= in args, which can be a list or a single value and
// can repeat.
func TestValidatePreservedImportCredentialEditsLinksJSONDocumentsLikeTheRuntime(t *testing.T) {
	eval := func(params map[string]any) dsl.Action { return jsonStep("lr_eval_json", params) }
	get := jsonStep("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "host", "query": "$.host"})
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{eval(map[string]any{"json_object": "doc", "buffer": `{"host":"origin.example.test"}`}), get}

	for name, replacement := range map[string]dsl.Action{
		"scalar args":         eval(map[string]any{"args": "JsonObject=doc", "buffer": `{"host":"attacker.test"}`}),
		"list args":           eval(map[string]any{"args": []any{"Buffer=x", "jsonobject = Doc"}}),
		"repeated JsonObject": eval(map[string]any{"args": []any{"JsonObject=other", "JsonObject=doc"}}),
		"params and args":     eval(map[string]any{"json_object": "other", "args": []any{"JsonObject=doc"}}),
	} {
		t.Run("refuses a new document step with "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			candidate.Init = append([]dsl.Action{candidate.Init[0], replacement}, candidate.Init[1:]...)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
}

// A step nested in an HTTP sequence is a raw map; the engine decodes its keys
// case-insensitively, so a spelling the editor chooses must not hide a writer.
func TestValidatePreservedImportCredentialEditsReadsNestedStepsLikeTheEngine(t *testing.T) {
	prior := varHostCredentialScript()
	t.Run("refuses an extractor list spelled in another case", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, dsl.Action{
			Name: "seq", Type: "http_sequence",
			Params: map[string]any{"requests": []any{map[string]any{
				"Name": "seed", "Type": "GET", "Method": "GET", "Target": "https://attacker.test/seed",
				"EXTRACTORS": []any{map[string]any{"name": "host", "source": "status", "type": "regex", "expression": "(x)", "default_value": "attacker.test"}},
			}}},
		})
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("refuses a base64 body spelled in another case", func(t *testing.T) {
		scenario := referencedCredentialScript()
		scenario.Actions[0] = dsl.Action{Name: "seq", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{
			"name": "orders", "type": "GET", "method": "POST", "target": "https://origin.example.test/orders",
			"headers": map[string]any{"Authorization": sourceReviewKeptCredential},
			// {"callback":"https://{{param.host}}/hook"}
			"Body": "eyJjYWxsYmFjayI6Imh0dHBzOi8ve3twYXJhbS5ob3N0fX0vaG9vayJ9", "Body_Encoding": "base64",
		}}}}
		candidate := CloneScriptDefinition(scenario)
		candidate.Parameters[0].Values = []string{"attacker.test"}
		expectRefusal(t, candidate, scenario, "actions[0].params.requests[0]")
	})
}

// A script-level credential held in a parameter can be copied into a record
// header or a message property a Kafka or JMS send delivers, so what those
// entries read is part of what the whole script holds.
func TestValidatePreservedImportCredentialEditsPinsSentMetadataForScriptWideCredentials(t *testing.T) {
	prior := &dsl.Script{
		Name: "Kafka script credential", Version: "1", Protocol: "kafka",
		Config:     dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true}},
		Parameters: []dsl.Parameter{{Name: "api_password", Type: "list", Values: []string{"source-review-synthetic-param-secret"}, Update: "once"}},
		Metadata:   &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "enterprise_script_archive", Preserved: map[string]any{}}},
		Actions: []dsl.Action{
			{Name: "first", Type: "kafka_produce", Target: "orders", Body: "{}"},
			{Name: "second", Type: "kafka_produce", Target: "audit", Body: "{}", Metadata: map[string]any{"x-note": "plain"}},
		},
	}
	MarkCredentialPreservingImportScript(prior)
	leak := CloneScriptDefinition(prior)
	leak.Actions[1].Metadata = map[string]any{"x-note": "plain", "X-Leak": "{{param.api_password}}"}
	expectRefusal(t, leak, prior, "script")

	literal := CloneScriptDefinition(prior)
	literal.Actions[1].Metadata = map[string]any{"x-note": "edited"}
	expectAccepted(t, literal, prior)
}

// Every family of fixed-name variables pins the steps that publish them.
func TestValidatePreservedImportCredentialEditsPinsEveryVariableFamily(t *testing.T) {
	for name, fixture := range map[string]struct {
		variable string
		protocol string
		step     dsl.Action
	}{
		"WebSocket":          {"ws_message_type", "", dsl.Action{Name: "connect", Type: "ws_connect", Target: "wss://attacker.test/feed"}},
		"Kafka":              {"kafka_topic", "", dsl.Action{Name: "consume", Type: "kafka_consume", Target: "attacker"}},
		"JMS":                {"jms_destination", "", dsl.Action{Name: "consume", Type: "jms_consume", Target: "attacker"}},
		"Event Hubs":         {"eventhubs_operation", "", dsl.Action{Name: "consume", Type: "eventhubs_consume", Target: "attacker"}},
		"Java HTTP":          {"java_http_codec", "java_http", dsl.Action{Name: "seed", Type: "GET", Method: "GET", Target: "https://attacker.test/seed"}},
		"SOAP":               {"soap_endpoint", "soap", dsl.Action{Name: "seed", Type: "GET", Method: "GET", Target: "https://attacker.test/seed"}},
		"async":              {"async_chunk", "", dsl.Action{Name: "bg", Type: "http_async_start", Params: map[string]any{"id": "bg", "pattern": "once", "request": map[string]any{"name": "p", "type": "GET", "method": "GET", "target": "https://attacker.test/p"}}}},
		"engine state names": {"__source_exit_requested", "", dsl.Action{Name: "exit", Type: "source_runtime", Params: map[string]any{"function": "lr_exit", "args": []any{"attacker.test"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			prior := referencedCredentialScript()
			prior.Protocol = fixture.protocol
			if fixture.protocol == "" {
				prior.Protocol = "http"
			}
			prior.Actions[0].Target = "https://{{var." + fixture.variable + "}}/orders"
			candidate := CloneScriptDefinition(prior)
			candidate.Init = append(candidate.Init, fixture.step)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
}

// A live table step reads what other live table steps stored.
func TestValidatePreservedImportCredentialEditsPinsLiveTableState(t *testing.T) {
	read := dsl.Action{Name: "read", Type: "live_table", Params: map[string]any{"table": "hosts", "operation": "get", "result_prefix": "row"}}
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://{{var.row.host}}/orders"
	prior.Init = []dsl.Action{read}
	candidate := CloneScriptDefinition(prior)
	candidate.Init = append([]dsl.Action{{Name: "write", Type: "live_table", Params: map[string]any{"table": "hosts", "operation": "update", "values": map[string]any{"host": "attacker.test"}}}}, candidate.Init...)
	expectRefusal(t, candidate, prior, "actions[0]")
}

// The variables a loop's condition reads, templated or in a CEL expression,
// decide which rows it binds.
func TestValidatePreservedImportCredentialEditsFollowsLoopConditionVariables(t *testing.T) {
	prior := sourceReviewCredentialScript()
	prior.Parameters = []dsl.Parameter{{Name: "limit", Type: "list", Values: []string{"10"}, Update: "once"}}
	prior.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true}}
	orders := prior.Actions[0]
	orders.Target = "https://{{var.row.host}}/orders"
	prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{
		ForEach: &dsl.ForEachLoop{Source: "hosts", Var: "row", Condition: &dsl.Condition{Expr: "int(var.row.rank) <= int(var.limit)"}},
		Steps:   []dsl.Action{orders},
	}}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[0].Values = []string{"20"}
	expectRefusal(t, candidate, prior, "actions[0].control.steps[0]")
}

// A parameter is held by what it can produce: a counter's start and step, a
// range, a padding and whether it advances all decide the value, and a number
// can be a port, an address octet or a host label. Only the fields that choose
// among the values, or share a pool out, are free, and so is a data source's
// selection strategy.
func TestValidatePreservedImportCredentialEditsHoldsWhatAParameterProduces(t *testing.T) {
	prior := sourceReviewCredentialScript()
	prior.Parameters = []dsl.Parameter{
		{Name: "port", Type: "counter", Start: 443, Step: 1, Update: "once"},
		{Name: "octet", Type: "random_number", Min: 7, Max: 7},
		{Name: "user", Type: "file", DataSource: "users", Column: "user", Update: "each_iteration", Selection: "sequential", StartRow: 1, BlockSize: 10},
	}
	prior.DataSources = []dsl.DataSource{{Name: "users", Type: "csv", InlineData: "user\nu1\n", Strategy: "sequential", FirstRowHeader: true}}
	prior.Actions[0].Method = "POST"
	prior.Actions[0].Target = "https://10.0.0.{{param.octet}}:{{param.port}}/orders"
	prior.Actions[0].Body = `{"user":"{{param.user}}"}`

	for name, edit := range map[string]func(*dsl.Script){
		"selection and seed of a pool": func(s *dsl.Script) {
			seed := int64(7)
			s.Parameters[2].Selection, s.Parameters[2].Seed = "random", &seed
			s.Parameters[2].AutoAllocate, s.Parameters[2].ValueForEachVUser = true, true
		},
		"data pool strategy": func(s *dsl.Script) { s.DataSources[0].Strategy = "random" },
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			expectAccepted(t, candidate, prior)
		})
	}
	for name, edit := range map[string]func(*dsl.Script){
		"counter start": func(s *dsl.Script) { s.Parameters[0].Start = 6379 },
		// Running out of rows can make a parameter empty, and an empty host
		// label is another host.
		"exhaustion policy of a pool": func(s *dsl.Script) { s.Parameters[2].OnExhaustion = "empty" },
		"first row of a pool":         func(s *dsl.Script) { s.Parameters[2].StartRow = 99 },
		"block of a pool":             func(s *dsl.Script) { s.Parameters[2].BlockSize = 1 },
		"counter step":                func(s *dsl.Script) { s.Parameters[0].Step = 5 },
		"counter update":              func(s *dsl.Script) { s.Parameters[0].Update = "each_iteration" },
		"pad width":                   func(s *dsl.Script) { s.Parameters[0].PadWidth = 3 },
		"pad character":               func(s *dsl.Script) { s.Parameters[0].PadChar = "x" },
		"random range":                func(s *dsl.Script) { s.Parameters[1].Min, s.Parameters[1].Max = 66, 66 },
		"random seed":                 func(s *dsl.Script) { seed := int64(7); s.Parameters[1].Seed = &seed },
		"counter block":               func(s *dsl.Script) { s.Parameters[0].BlockSize = 50 },
		"counter sharing":             func(s *dsl.Script) { s.Parameters[0].Selection = "unique" },
		"data row":                    func(s *dsl.Script) { s.DataSources[0].InlineData = "user\nu1\nattacker\n" },
		"parameter type":              func(s *dsl.Script) { s.Parameters[0].Type = "list"; s.Parameters[0].Values = []string{"attacker"} },
		"column of a pool":            func(s *dsl.Script) { s.Parameters[2].Column = "region" },
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			expectRefusal(t, candidate, prior, "actions[0]")
		})
	}
}

// A producer's analytics label and timeout do not decide what it publishes; its
// source function does.
func TestValidatePreservedImportCredentialEditsHoldsWhatAProducerDoes(t *testing.T) {
	login := dsl.Action{
		Name: "login", Type: "POST", Method: "POST", Target: "https://origin.example.test/login",
		Extractors:  []dsl.Extractor{{Name: "host", Source: "body", Type: "regex", Expression: "(.*)"}},
		Attribution: &dsl.AnalyticsAttribution{PageName: "Login"}, TimeoutMs: 1000,
	}
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{login}
	t.Run("accepts the label and the timeout", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Attribution = &dsl.AnalyticsAttribution{PageName: "Sign in"}
		candidate.Init[0].TimeoutMs = 60000
		expectAccepted(t, candidate, prior)
	})
	t.Run("refuses the method", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Init[0].Method = "GET"
		expectRefusal(t, candidate, prior, "actions[0]")
	})
	t.Run("refuses the source function in metadata", func(t *testing.T) {
		scenario := CloneScriptDefinition(prior)
		scenario.Init = []dsl.Action{{Name: "save", Type: "source_parameter", Metadata: map[string]any{"source_function": "lr_save_string"}, Params: map[string]any{"args": []any{"origin.example.test", "host"}}}}
		candidate := CloneScriptDefinition(scenario)
		// Both functions save args[1]; the digest has to tell them apart.
		candidate.Init[0].Metadata = map[string]any{"source_function": "lr_save_int"}
		expectRefusal(t, candidate, scenario, "actions[0]")
	})
}

// An async conversation's request is held by what it sends and the extractors
// that publish the variable, not by its label or its other extractors.
func TestValidatePreservedImportCredentialEditsHoldsAsyncRequestsByWhatTheyPublish(t *testing.T) {
	request := func(note string) map[string]any {
		return map[string]any{
			"name": "ping", "type": "GET", "method": "GET", "target": "https://origin.example.test/ping",
			"extractors": []any{
				map[string]any{"name": "host", "source": "body", "type": "regex", "expression": "(.*)"},
				map[string]any{"name": "note", "source": "body", "type": "regex", "expression": note},
			},
		}
	}
	prior := varHostCredentialScript()
	prior.Init = []dsl.Action{{Name: "bg", Type: "http_async_start", Params: map[string]any{"id": "bg", "pattern": "once", "request": request("(a)")}}}
	unrelated := CloneScriptDefinition(prior)
	unrelated.Init[0].Params = map[string]any{"id": "bg", "pattern": "once", "request": request("(b)")}
	expectAccepted(t, unrelated, prior)

	held := CloneScriptDefinition(prior)
	changed := request("(a)")
	changed["extractors"].([]any)[0].(map[string]any)["default_value"] = "attacker.test"
	held.Init[0].Params = map[string]any{"id": "bg", "pattern": "once", "request": changed}
	expectRefusal(t, held, prior, "actions[0]")
}

// A form-encoded structured body is read per decoded layer: a template written
// with JSON escapes appears only after the runtime decodes the message.
func TestValidatePreservedImportCredentialEditsReadsStructuredBodyContracts(t *testing.T) {
	// The template is written as JSON \u escapes, which only the decoded
	// message shows as {{param.host}}.
	escaped := strings.Builder{}
	for _, r := range "{{param.host}}" {
		fmt.Fprintf(&escaped, "%cu%04x", rune(92), r)
	}
	message := `{"actions":[{"id":"a","descriptor":"fixture","params":{"callback":"https://` + escaped.String() + `/hook"}}]}`
	body := "message=" + url.QueryEscape(message) + "&aura.token=undefined"
	contract, _, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, "", aurapayload.TemplateValueEncodingJSONStrings, nil, aurapayload.MaxWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://origin.example.test/aura"
	prior.Actions[0].Method = "POST"
	prior.Actions[0].Body = body
	prior.Actions[0].Metadata = map[string]any{
		dsl.ImportURLFormTemplateValueEncodingMetadataKey: " " + dsl.ImportAuraJSONTemplateValueEncoding + " ",
		dsl.ImportAuraTemplateContractMetadataKey:         contract,
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[0].Values = []string{"attacker.test"}
	expectRefusal(t, candidate, prior, "actions[0]")
}

// The default header list is read as well as the default header map.
func TestValidatePreservedImportCredentialEditsReadsTheDefaultHeaderList(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://origin.example.test/orders"
	prior.Config.HTTP.DefaultHeaderList = dsl.HeaderList{{Name: "Host", Value: "{{param.region}}.example.test"}}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[1].Values = []string{"attacker-on-the-same-edge"}
	expectRefusal(t, candidate, prior, "actions[0]")
}

// The guard resolves a reference quickly whatever its shape: names that are
// absurdly long or deeply dotted do not stall a save, and neither do many
// requests that read one widely written variable.
func TestValidatePreservedImportCredentialEditsBoundsTheCostOfAReference(t *testing.T) {
	t.Run("long dotted name", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "host\no\n", Strategy: "sequential", FirstRowHeader: true}}
		prior.Actions[0].Headers["X-Note"] = "{{var.parallel." + strings.Repeat("a.", 8000) + "x}}"
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[1].Values = []string{"changed"}
		// A name with more dotted tails than the guard tries holds everything,
		// so the unrelated parameter is held too, and it is decided quickly.
		started := time.Now()
		expectRefusal(t, candidate, prior, "actions[0]")
		if elapsed := time.Since(started); elapsed > 10*time.Second {
			t.Fatalf("validating a 16 KB reference took %v", elapsed)
		}
	})
	t.Run("a variable every response extracts", func(t *testing.T) {
		const requests = 3000
		prior := sourceReviewCredentialScript()
		prior.Parameters = []dsl.Parameter{{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"}, {Name: "spare", Type: "list", Values: []string{"x"}, Update: "once"}}
		prior.Actions = prior.Actions[:0]
		for index := 0; index < requests; index++ {
			prior.Actions = append(prior.Actions, dsl.Action{
				Name: fmt.Sprintf("r%d", index), Type: "GET", Method: "GET",
				Target:     fmt.Sprintf("https://{{param.region}}.example.test/r%d/{{var.id%d}}", index, index),
				Headers:    map[string]string{"Authorization": sourceReviewKeptCredential, "X-CSRF": "{{var.csrf}}"},
				Extractors: []dsl.Extractor{{Name: "csrf", Source: "body", Type: "regex", Expression: "(.*)"}, {Name: fmt.Sprintf("id%d", index), Source: "body", Type: "regex", Expression: "(.*)"}},
			})
		}
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[1].Values = []string{"changed"}
		started := time.Now()
		expectAccepted(t, candidate, prior)
		if elapsed := time.Since(started); elapsed > 15*time.Second {
			t.Fatalf("validating %d requests that read one widely written variable took %v", requests, elapsed)
		}
	})
}

// web_save_param_length reads the variable it names, not by template, and
// publishes <name>_Length, so the extractor behind the name is held when a
// request reads the length. The other functions that take a variable name
// write the name they read: the writers of that name are held already.
func TestValidatePreservedImportCredentialEditsPinsTheVariableALengthIsTakenFrom(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://{{var.base_Length}}.example.test/orders"
	prior.Init = []dsl.Action{
		{
			Name: "discover", Type: "GET", Method: "GET", Target: "https://origin.example.test/discover",
			Extractors: []dsl.Extractor{{Name: "base", Source: "body", Type: "regex", Expression: "(.*)"}},
		},
		{Name: "measure", Type: "source_parameter", Params: map[string]any{"function": "web_save_param_length", "args": []any{"base"}}},
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Init[0].Extractors[0].DefaultValue = stringPointer("attacker")
	expectRefusal(t, candidate, prior, "actions[0]")
}

// A Java Vuser step that publishes a variable the request reads receives every
// parameter and data source, so they are all held.
func TestValidatePreservedImportCredentialEditsHoldsEverythingForAPinnedJavaStep(t *testing.T) {
	prior := referencedCredentialScript()
	prior.Actions[0].Target = "https://origin.example.test/orders/{{var.java_vuser_duration_ms}}"
	prior.Init = []dsl.Action{{Name: "java", Type: "java_vuser_run", Params: map[string]any{"method": "run"}}}
	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[1].Values = []string{"changed"}
	expectRefusal(t, candidate, prior, "actions[0]")
}

// The loop block itself decides which rows a data-driven loop binds.
func TestValidatePreservedImportCredentialEditsHoldsTheLoopBlock(t *testing.T) {
	prior := sourceReviewCredentialScript()
	prior.DataSources = []dsl.DataSource{{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true}}
	orders := prior.Actions[0]
	orders.Target = "https://{{var.row.host}}/orders"
	prior.Actions[0] = dsl.Action{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{
		ForEach: &dsl.ForEachLoop{Source: "hosts", Var: "row"}, MaxIterations: 10, Steps: []dsl.Action{orders},
	}}
	candidate := CloneScriptDefinition(prior)
	candidate.Actions[0].Control.MaxIterations = 1
	expectRefusal(t, candidate, prior, "actions[0].control.steps[0]")
}
