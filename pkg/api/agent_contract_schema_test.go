package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const agentContractDir = "../../docs/contracts/agent-v1"

func TestAgentContractSchemasAreFresh(t *testing.T) {
	schemas, err := AgentContractSchemas()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range schemas {
		got, err := os.ReadFile(filepath.Join(agentContractDir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run `go generate ./pkg/api`", name)
		}
	}
}

func compileAgentContractSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	schemas, err := AgentContractSchemas()
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas[name]))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(agentContractSchemaURN+name, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(agentContractSchemaURN + name)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateAgentContract(t *testing.T, schema *jsonschema.Schema, label string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(decoded); err != nil {
		t.Fatalf("%s does not match its schema: %v", label, err)
	}
}

// TestAgentOutputsMatchTheirContractSchemas validates every fixture brief,
// a finding detail and a CTRF report against the published schemas, and
// keeps the committed examples current: APEXLOAD_UPDATE_AGENT_EXAMPLES=1
// rewrites them.
func TestAgentOutputsMatchTheirContractSchemas(t *testing.T) {
	briefSchema := compileAgentContractSchema(t, "brief.schema.json")
	detailSchema := compileAgentContractSchema(t, "finding-detail.schema.json")
	ctrfSchema := compileAgentContractSchema(t, "ctrf.schema.json")
	examples := map[string][]byte{}
	for _, fixture := range agentFixtures() {
		for _, detail := range []string{agentDetailSummary, agentDetailStandard, agentDetailFull} {
			brief := buildAgentFixtureBrief(t, fixture, detail)
			validateAgentContract(t, briefSchema, fixture.name+"/"+detail, brief)
			if detail == agentDetailStandard && (fixture.name == "regression" || fixture.name == "injection") {
				body, _ := json.MarshalIndent(brief, "", "  ")
				examples["brief."+fixture.name+".standard.json"] = append(body, '\n')
				examples["brief."+fixture.name+".standard.md"] = []byte(renderAgentBriefMarkdown(brief))
			}
		}
		unbounded, err := buildAgentBrief(agentBriefInputs{Report: fixture.report}, agentBriefOptions{Detail: agentDetailFull, Unbounded: true})
		if err != nil {
			t.Fatal(err)
		}
		ctrf := buildAgentCTRF(unbounded)
		validateAgentContract(t, ctrfSchema, fixture.name+"/ctrf", ctrf)
		for _, finding := range unbounded.Findings {
			detail, ok := buildAgentFindingDetail(unbounded, fixture.report, finding.ID)
			if !ok {
				t.Fatalf("%s: finding %s did not resolve", fixture.name, finding.ID)
			}
			validateAgentContract(t, detailSchema, fixture.name+"/"+finding.ID, detail)
			if fixture.name == "injection" && finding.Kind == agentKindErrorGroup {
				body, _ := json.MarshalIndent(detail, "", "  ")
				examples["finding-detail.error_group.json"] = append(body, '\n')
			}
		}
		if fixture.name == "sla_fail" {
			body, _ := json.MarshalIndent(ctrf, "", "  ")
			examples["ctrf.sla_fail.json"] = append(body, '\n')
		}
	}
	dir := filepath.Join(agentContractDir, "examples")
	for name, want := range examples {
		path := filepath.Join(dir, name)
		if os.Getenv("APEXLOAD_UPDATE_AGENT_EXAMPLES") == "1" {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is stale; rerun with APEXLOAD_UPDATE_AGENT_EXAMPLES=1 after checking the change is intended", path)
		}
	}
}
