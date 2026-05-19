package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestWebAPISampleScriptsValidate(t *testing.T) {
	for _, name := range []string{
		"ecommerce-http.json",
		"soap-account-service.json",
		"grpc-inventory.json",
		"websocket-order-feed.json",
		"jms-order-flow.json",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "examples", "web-api", name))
			if err != nil {
				t.Fatalf("ReadFile returned error: %v", err)
			}
			var script dsl.Script
			if err := json.Unmarshal(data, &script); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if issues := dsl.ValidateScriptIssues(&script); len(issues) > 0 {
				t.Fatalf("sample validation issues = %v", issues)
			}
		})
	}
}

func TestWebAPIMixedScenarioSampleReferencesScripts(t *testing.T) {
	for _, name := range []string{"mixed-http-browser-canary.json", "browser-canary-plus-protocol-load.json"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "examples", "web-api", name))
			if err != nil {
				t.Fatalf("ReadFile returned error: %v", err)
			}
			var scenario struct {
				Name        string `json:"name"`
				ClaimPolicy string `json:"claim_policy"`
				Groups      []struct {
					Name             string      `json:"name"`
					Protocol         string      `json:"protocol"`
					ScriptPath       string      `json:"script_path"`
					BenchmarkProfile string      `json:"benchmark_profile"`
					Script           *dsl.Script `json:"script"`
				} `json:"groups"`
			}
			if err := json.Unmarshal(data, &scenario); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if scenario.Name == "" || len(scenario.Groups) < 2 {
				t.Fatalf("scenario = %+v, want name and at least two groups", scenario)
			}
			if scenario.Groups[0].ScriptPath != "examples/web-api/ecommerce-http.json" {
				t.Fatalf("script path = %q", scenario.Groups[0].ScriptPath)
			}
			browserCanaries := 0
			for _, group := range scenario.Groups {
				if group.Protocol != "browser" {
					continue
				}
				browserCanaries++
				if group.Script == nil {
					t.Fatalf("browser group %q script is missing", group.Name)
				}
				if issues := dsl.ValidateScriptIssues(group.Script); len(issues) > 0 {
					t.Fatalf("browser canary validation issues = %v", issues)
				}
				if name == "browser-canary-plus-protocol-load.json" && group.BenchmarkProfile == "" {
					t.Fatalf("browser group %q missing benchmark_profile", group.Name)
				}
			}
			if browserCanaries == 0 {
				t.Fatalf("scenario %s has no browser canary groups", name)
			}
			if name == "browser-canary-plus-protocol-load.json" && scenario.ClaimPolicy == "" {
				t.Fatalf("scenario %s missing claim_policy", name)
			}
		})
	}
}

func TestWebAPIArchiveSmokeFixtureContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "web-api", "archive-smoke-fixtures.json"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var contract struct {
		Fixtures []struct {
			ID                     string   `json:"id"`
			Protocol               string   `json:"protocol"`
			ScriptPath             string   `json:"script_path"`
			ExpectedArchiveSignals []string `json:"expected_archive_signals"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(contract.Fixtures) < 6 {
		t.Fatalf("fixtures = %d, want at least 6", len(contract.Fixtures))
	}
	seen := map[string]bool{}
	for _, fixture := range contract.Fixtures {
		if fixture.ID == "" || fixture.Protocol == "" || fixture.ScriptPath == "" {
			t.Fatalf("incomplete fixture: %+v", fixture)
		}
		if len(fixture.ExpectedArchiveSignals) == 0 {
			t.Fatalf("fixture %s has no archive signals", fixture.ID)
		}
		if _, err := os.Stat(filepath.Join("..", "..", fixture.ScriptPath)); err != nil {
			t.Fatalf("fixture %s script path is not valid: %v", fixture.ID, err)
		}
		seen[fixture.Protocol] = true
	}
	for _, protocol := range []string{"http", "soap", "grpc", "websocket", "jms-solace", "scenario"} {
		if !seen[protocol] {
			t.Fatalf("archive smoke fixtures missing protocol %s", protocol)
		}
	}
}
