package importreview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const sourceReviewKeptCredential = "Bearer source-review-synthetic-kept-token"

// sourceReviewActionSource is the source text an archive's review keeps: it
// quotes the literal Authorization header the conversion kept on "orders".
const sourceReviewActionSource = "Action()\n{\n" +
	"\tweb_add_header(\"Authorization\", \"" + sourceReviewKeptCredential + "\");\n" +
	"\tweb_url(\"orders\", \"URL=https://origin.example.test/orders\", \"Resource=0\", LAST);\n" +
	"\tweb_url(\"cart\", \"URL=https://origin.example.test/cart\", \"Resource=0\", LAST);\n" +
	"\treturn 0;\n}\n"

// sourceReviewCredentialScript is a credential-preserving archive import whose
// preserved source review quotes the credential kept on actions[0].
func sourceReviewCredentialScript() *dsl.Script {
	script := &dsl.Script{
		Name: "Source review credential", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				"source_review": map[string]any{
					"source_files": []any{map[string]any{"path": "Action.c", "content": sourceReviewActionSource}},
					"mappings":     []any{map[string]any{"source_file": "Action.c", "source_line": 4, "action_path": "actions[0]"}},
				},
			},
		}},
		Actions: []dsl.Action{
			{Name: "orders", Type: "GET", Method: "GET", Target: "https://origin.example.test/orders", Headers: map[string]string{"Authorization": sourceReviewKeptCredential}},
			{Name: "cart", Type: "GET", Method: "GET", Target: "https://origin.example.test/cart"},
		},
	}
	MarkCredentialPreservingImportScript(script)
	return script
}

func sourceReviewFiles(script *dsl.Script) []any {
	review, _ := script.Metadata.Import.Preserved["source_review"].(map[string]any)
	files, _ := review["source_files"].([]any)
	return files
}

// The review's copy of the source quotes the kept credential, so disclosure
// masks it. That copy is evidence no run reads: it pins no execution context,
// and a save may add a parameter or retarget a request without the kept
// credential. The kept credential's own request still cannot move, and the
// evidence cannot gain or change a credential.
func TestValidatePreservedImportCredentialEditsIgnoresSourceReviewEvidence(t *testing.T) {
	prior := sourceReviewCredentialScript()
	masked := MaskScriptSecrets(prior)
	if masked == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	disclosed, _ := sourceReviewFiles(masked)[0].(map[string]any)["content"].(string)
	if disclosed == sourceReviewActionSource || strings.Contains(disclosed, "source-review-synthetic-kept-token") {
		t.Fatalf("disclosed source text = %q, want the quoted credential masked", disclosed)
	}

	for name, edit := range map[string]func(*dsl.Script){
		"parameter": func(script *dsl.Script) {
			script.Parameters = append(script.Parameters, dsl.Parameter{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"})
		},
		"data source": func(script *dsl.Script) {
			script.DataSources = append(script.DataSources, dsl.DataSource{Name: "regions", Type: "csv", InlineData: "region\nwest\n", Strategy: "sequential", FirstRowHeader: true})
		},
		"request without the kept credential": func(script *dsl.Script) {
			script.Actions[1].Target = "https://origin.example.test/cart/summary"
		},
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
				t.Fatalf("%v, want the edit accepted", err)
			}
		})
	}

	refusals := []struct {
		name string
		edit func(*dsl.Script)
		want string
	}{
		{"kept credential's request to another host", func(script *dsl.Script) {
			script.Actions[0].Target = "https://other.example.test/orders"
		}, "retained imported credentials at actions[0]"},
		{"transport the kept credential's request uses", func(script *dsl.Script) {
			script.Config.BaseURL = "https://other.example.test"
		}, "retained imported credentials at actions[0]"},
		{"new credential in the source review", func(script *dsl.Script) {
			review := script.Metadata.Import.Preserved["source_review"].(map[string]any)
			review["source_files"] = append(sourceReviewFiles(script), map[string]any{
				"path":    "Extra.c",
				"content": "web_add_header(\"Authorization\", \"Bearer source-review-synthetic-new-token\");\n",
			})
		}, "introduces new executable credentials"},
		{"changed credential in the source review", func(script *dsl.Script) {
			file := sourceReviewFiles(script)[0].(map[string]any)
			file["content"] = strings.ReplaceAll(sourceReviewActionSource, "kept-token", "changed-token")
		}, "introduces new executable credentials"},
		{"credential moved out of the source review", func(script *dsl.Script) {
			script.Actions[1].Headers = map[string]string{"Authorization": sourceReviewKeptCredential}
		}, "introduces new executable credentials"},
	}
	for _, refusal := range refusals {
		t.Run("refuses "+refusal.name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			refusal.edit(candidate)
			err := ValidatePreservedImportCredentialEdits(candidate, prior)
			if err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("%v, want the refusal %q", err, refusal.want)
			}
			if strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("refusal disclosed a credential: %v", err)
			}
		})
	}
}

// Leaving the review evidence out of the context comparison leaves every
// executable credential where it was: a script-wide credential still pins the
// whole script, and so does a credential in preserved metadata a run reads.
func TestValidatePreservedImportCredentialEditsKeepsExecutableCredentialContext(t *testing.T) {
	addParameter := func(script *dsl.Script) {
		script.Parameters = append(script.Parameters, dsl.Parameter{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"})
	}
	for name, fixture := range map[string]func(*dsl.Script){
		"script-wide auth": func(script *dsl.Script) {
			script.Config.HTTP.Auth = dsl.AuthConfig{Type: "bearer", Token: "source-review-synthetic-script-token"}
		},
		"recorded resource URL": func(script *dsl.Script) {
			script.Metadata.Import.Preserved["recorded_resource_urls_available"] = true
			script.Metadata.Import.Preserved["recorded_resource_urls"] = []any{"https://user:source-review-synthetic-url-secret@origin.example.test/app.js"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			prior := sourceReviewCredentialScript()
			fixture(prior)
			disclosed, err := json.Marshal(MaskScriptSecrets(prior))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(disclosed), "synthetic-script-token") || strings.Contains(string(disclosed), "synthetic-url-secret") {
				t.Fatal("fixture credential is not masked on disclosure")
			}
			candidate := CloneScriptDefinition(prior)
			addParameter(candidate)
			err = ValidatePreservedImportCredentialEdits(candidate, prior)
			if err == nil || !strings.Contains(err.Error(), "retained imported credentials at script") {
				t.Fatalf("adding a parameter = %v, want the script-wide refusal", err)
			}
		})
	}
}

// projectSettingsTransactionOrder is a project file's transaction order as an
// archive records it: names joined by '*'. The long mixed names read as opaque
// tokens to the disclosure's free-text scrub, as a real archive's did, so
// disclosure masks parts of this setting although it holds no credential.
const projectSettingsTransactionOrder = "T01_Launch*S01_Tablet_StoreLookup_T02_SubmitQuoteApproval*S01_Tablet_StoreLookup_T03_ReviewOrderSummary*T04_Logout"

// projectSettingsCredentialScript is a credential-preserving archive import
// that keeps a credential on actions[0] and records its project file's
// sections, whose transaction order disclosure masks. actions[1] is an
// ordinary POST that reads {{var.store_id}}.
func projectSettingsCredentialScript() *dsl.Script {
	script := &dsl.Script{
		Name: "Project settings credential", Version: "1", Protocol: "http",
		Config:     dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Parameters: []dsl.Parameter{{Name: "store_id", Type: "list", Values: []string{"store-1001"}, Update: "once"}},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				"project_settings": map[string]any{
					"General":           map[string]any{"Type": "Multi"},
					"TransactionsOrder": map[string]any{"Order": projectSettingsTransactionOrder},
				},
			},
		}},
		Actions: []dsl.Action{
			{Name: "orders", Type: "GET", Method: "GET", Target: "https://origin.example.test/orders", Headers: map[string]string{"Authorization": sourceReviewKeptCredential}},
			{Name: "quote", Type: "POST", Method: "POST", Target: "https://origin.example.test/quote", Body: `{"store":"{{var.store_id}}","items":2}`},
		},
	}
	MarkCredentialPreservingImportScript(script)
	return script
}

func projectSettingsOrder(script *dsl.Script) string {
	settings, _ := script.Metadata.Import.Preserved["project_settings"].(map[string]any)
	order, _ := settings["TransactionsOrder"].(map[string]any)
	text, _ := order["Order"].(string)
	return text
}

func setProjectSettingsOrder(script *dsl.Script, order string) {
	settings, _ := script.Metadata.Import.Preserved["project_settings"].(map[string]any)
	settings["TransactionsOrder"] = map[string]any{"Order": order}
}

// The project file's sections are recorded evidence no run reads: the import
// summary lists their names, and the settings a run applies were converted
// into the script. A masked fragment of them pins no execution context, so a
// save may edit a request without the kept credential, as on a real archive
// whose transaction names disclosure masks. The kept credential's own request
// still cannot move, and the recorded settings cannot gain, change or give up
// a masked value.
func TestValidatePreservedImportCredentialEditsIgnoresProjectSettingsEvidence(t *testing.T) {
	prior := projectSettingsCredentialScript()
	masked := MaskScriptSecrets(prior)
	if masked == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	disclosed := projectSettingsOrder(masked)
	if disclosed == projectSettingsTransactionOrder || !strings.Contains(disclosed, "[REDACTED]") {
		t.Fatalf("disclosed transaction order = %q, want part of it masked", disclosed)
	}
	maskedName := "S01_Tablet_StoreLookup_T02_SubmitQuoteApproval"
	if strings.Contains(disclosed, maskedName) {
		t.Fatalf("disclosed transaction order = %q, want %q masked", disclosed, maskedName)
	}

	for name, edit := range map[string]func(*dsl.Script){
		"parameter": func(script *dsl.Script) {
			script.Parameters = append(script.Parameters, dsl.Parameter{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"})
		},
		"data source": func(script *dsl.Script) {
			script.DataSources = append(script.DataSources, dsl.DataSource{Name: "regions", Type: "csv", InlineData: "region\nwest\n", Strategy: "sequential", FirstRowHeader: true})
		},
		"body of a request without the kept credential": func(script *dsl.Script) {
			script.Actions[1].Body = `{"store":"store-2002","items":2}`
		},
		"target of a request without the kept credential": func(script *dsl.Script) {
			script.Actions[1].Target = "https://origin.example.test/quote/v2"
		},
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
				t.Fatalf("%v, want the edit accepted", err)
			}
		})
	}

	refusals := []struct {
		name string
		edit func(*dsl.Script)
		want string
	}{
		{"kept credential's request to another host", func(script *dsl.Script) {
			script.Actions[0].Target = "https://other.example.test/orders"
		}, "retained imported credentials at actions[0]"},
		{"transport the kept credential's request uses", func(script *dsl.Script) {
			script.Config.BaseURL = "https://other.example.test"
		}, "retained imported credentials at actions[0]"},
		{"new masked value in the project settings", func(script *dsl.Script) {
			setProjectSettingsOrder(script, projectSettingsTransactionOrder+"*S01_Tablet_StoreLookup_T05_ConfirmPaymentSchedule")
		}, "introduces new executable credentials"},
		{"changed masked value in the project settings", func(script *dsl.Script) {
			setProjectSettingsOrder(script, strings.Replace(projectSettingsTransactionOrder, "SubmitQuoteApproval", "SubmitQuoteRejection", 1))
		}, "introduces new executable credentials"},
		{"masked name sent as a request credential", func(script *dsl.Script) {
			script.Actions[1].Headers = map[string]string{"Authorization": "Bearer " + maskedName}
		}, "introduces new executable credentials"},
	}
	for _, refusal := range refusals {
		t.Run("refuses "+refusal.name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			refusal.edit(candidate)
			err := ValidatePreservedImportCredentialEdits(candidate, prior)
			if err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("%v, want the refusal %q", err, refusal.want)
			}
			if strings.Contains(err.Error(), "synthetic") || strings.Contains(err.Error(), "Tablet") {
				t.Fatalf("refusal disclosed a recorded value: %v", err)
			}
		})
	}
}

// stepReviewLongName is a variable name long and varied enough that the
// disclosure's free-text scrub masks it inside a review code.
const stepReviewLongName = "cartsessionreferenceq7xk2mp9zt"

// stepReviewCodeMetadata is the metadata import review stamps on a step that
// reads stepReviewLongName before anything writes it.
func stepReviewCodeMetadata() map[string]any {
	code := "missing_producer:" + stepReviewLongName
	return map[string]any{"review_required": true, "promotion_blocker": true, "review_gate": code, "review_reasons": []any{code}}
}

// A review code is stamped on the step it gates and rebuilt by every save; no
// request sends it. Disclosure masks the long variable name in it, but that
// masked text pins no request: the step's own request may still change. The
// code cannot be changed to other masked text. A Kafka send delivers each
// string metadata entry as a record header, so other masked review text it
// sends still pins the step; a dependency code is rebuilt by every save even
// there, so it does not.
func TestValidatePreservedImportCredentialEditsIgnoresUnsentStepReviewCodes(t *testing.T) {
	prior := projectSettingsCredentialScript()
	delete(prior.Metadata.Import.Preserved, "project_settings")
	prior.Actions[1].Body = `{"cart":"{{var.` + stepReviewLongName + `}}"}`
	prior.Actions[1].Metadata = stepReviewCodeMetadata()
	masked := MaskScriptSecrets(prior)
	if masked == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	if gate, _ := masked.Actions[1].Metadata["review_gate"].(string); strings.Contains(gate, stepReviewLongName) || !strings.Contains(gate, "[REDACTED]") {
		t.Fatalf("disclosed review gate = %q, want the variable name masked", gate)
	}

	for name, edit := range map[string]func(*dsl.Script){
		"target of the gated request": func(script *dsl.Script) {
			script.Actions[1].Target = "https://origin.example.test/quote/v2"
		},
		"body of the gated request": func(script *dsl.Script) {
			script.Actions[1].Body = `{"cart":"{{var.` + stepReviewLongName + `}}","items":3}`
		},
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
				t.Fatalf("%v, want the edit accepted", err)
			}
		})
	}
	t.Run("refuses other masked text in the code", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Actions[1].Metadata["review_reasons"] = []any{"missing_producer:basketidentifierw4r8n1vq6zx"}
		err := ValidatePreservedImportCredentialEdits(candidate, prior)
		if err == nil || !strings.Contains(err.Error(), "introduces new executable credentials") {
			t.Fatalf("%v, want the new-credential refusal", err)
		}
	})
	t.Run("refuses the kept credential's request to another host", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.Actions[0].Target = "https://other.example.test/orders"
		err := ValidatePreservedImportCredentialEdits(candidate, prior)
		if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
			t.Fatalf("%v, want the refusal at actions[0]", err)
		}
	})

	// kafkaSend is a credential-preserving Kafka send whose review_gate,
	// which the send delivers as a header, holds code.
	kafkaSend := func(t *testing.T, code string) *dsl.Script {
		t.Helper()
		metadata := stepReviewCodeMetadata()
		metadata["review_gate"], metadata["review_reasons"] = code, []any{code}
		kafka := &dsl.Script{
			Name: "Kafka review code", Version: "1", Protocol: "kafka",
			Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true}},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source: "enterprise_script_archive", Preserved: map[string]any{},
			}},
			Actions: []dsl.Action{{Name: "send", Type: "kafka_produce", Target: "orders", Body: `{"cart":"{{var.` + stepReviewLongName + `}}"}`, Metadata: metadata}},
		}
		MarkCredentialPreservingImportScript(kafka)
		if gate, _ := MaskScriptSecrets(kafka).Actions[0].Metadata["review_gate"].(string); strings.Contains(gate, stepReviewLongName) {
			t.Fatalf("disclosed Kafka review gate = %q, want the name masked", gate)
		}
		return kafka
	}
	t.Run("accepts a Kafka send's topic beside the dependency code it sends", func(t *testing.T) {
		kafka := kafkaSend(t, "missing_producer:"+stepReviewLongName)
		candidate := CloneScriptDefinition(kafka)
		candidate.Actions[0].Target = "orders-audit"
		if err := ValidatePreservedImportCredentialEdits(candidate, kafka); err != nil {
			t.Fatalf("%v, want the edit accepted", err)
		}
	})
	t.Run("refuses other masked text in the dependency code a Kafka send sends", func(t *testing.T) {
		kafka := kafkaSend(t, "missing_producer:"+stepReviewLongName)
		candidate := CloneScriptDefinition(kafka)
		candidate.Actions[0].Metadata["review_gate"] = "missing_producer:basketidentifierw4r8n1vq6zx"
		err := ValidatePreservedImportCredentialEdits(candidate, kafka)
		if err == nil || !strings.Contains(err.Error(), "introduces new executable credentials") {
			t.Fatalf("%v, want the new-credential refusal", err)
		}
	})
	t.Run("refuses a Kafka send's topic beside other review text it sends", func(t *testing.T) {
		kafka := kafkaSend(t, "invalid_parameter_allocation:"+stepReviewLongName)
		candidate := CloneScriptDefinition(kafka)
		candidate.Actions[0].Target = "orders-audit"
		err := ValidatePreservedImportCredentialEdits(candidate, kafka)
		if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
			t.Fatalf("%v, want the refusal at the Kafka send", err)
		}
	})
}
