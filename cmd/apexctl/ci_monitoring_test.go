package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAppDynamicsCIMonitorConfigurationIsExplicitBoundedAndIdentified(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	folder := t.TempDir()
	bindingFile, rulesFile := filepath.Join(folder, "bindings.json"), filepath.Join(folder, "rules.json")
	rule := `[{"source_id":"apm","metric":"calls","unit":"count","statistic":"sum","comparator":"lte","threshold":10}]`
	if err := os.WriteFile(rulesFile, []byte(rule), 0600); err != nil {
		t.Fatal(err)
	}
	if bindings, rules, err := loadCIMonitorConfiguration(context.Background(), "", ""); err != nil || bindings != nil || rules != nil {
		t.Fatal("omitted monitor files do not preserve inheritance", err)
	}
	for _, tc := range []struct {
		bindings  string
		wantError bool
	}{
		{`[]`, false},
		{`[{"source_id":"apm","required":true}]`, false},
		{`null`, true},
		{`[{"source_id":"apm","credential":"raw"}]`, true},
		{`[{"source_id":"apm"},{"source_id":"apm"}]`, true},
		{`[] {}`, true},
	} {
		if err := os.WriteFile(bindingFile, []byte(tc.bindings), 0600); err != nil {
			t.Fatal(err)
		}
		bindings, _, err := loadCIMonitorConfiguration(context.Background(), bindingFile, "")
		if (err != nil) != tc.wantError {
			t.Fatalf("bindings %s: %v", tc.bindings, err)
		}
		if !tc.wantError && bindings == nil {
			t.Fatal("explicit binding array became omitted")
		}
	}
	if err := os.WriteFile(bindingFile, []byte(`[{"source_id":"apm"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	bindings, rules, err := loadCIMonitorConfiguration(context.Background(), bindingFile, rulesFile)
	if err != nil || len(rules) != 1 || bindings == nil {
		t.Fatal("configured metric rules unavailable", err)
	}
	if _, _, err = loadCIMonitorConfiguration(context.Background(), "", rulesFile); err == nil {
		t.Fatal("unbound criterion accepted")
	}
	request := CIRunRequest{ScriptID: "script", MonitorBindings: bindings, Goals: CIGoals{MonitorRules: rules}}
	identity := deriveCIIdempotencyKey(request)
	changed := request
	changed.MonitorBindings = &[]storage.MonitorBinding{}
	if deriveCIIdempotencyKey(changed) == identity {
		t.Fatal("binding change reused CI idempotency identity")
	}
	body, _ := json.Marshal(request)
	var decoded CIRunRequest
	if json.Unmarshal(body, &decoded) != nil || decoded.MonitorBindings == nil || len(decoded.Goals.MonitorRules) != 1 {
		t.Fatal("controller CI wire contract loses monitoring configuration")
	}
	local := defaultCIArgs()
	local.executionMode = "local"
	local.scriptPath = "script.json"
	local.publishCheck = false
	local.monitorBindingsFile = bindingFile
	if validateCIArgs(local) == nil {
		t.Fatal("standalone engine CI accepted managed monitor selection")
	}
}

func TestAppDynamicsCIPendingReconciliationAfterEveryTerminalLoadStatus(t *testing.T) {
	for _, status := range []string{"COMPLETE", "FAILED", "ABORTED"} {
		t.Run(status, func(t *testing.T) {
			response := &CIRunResponse{SchemaVersion: ciSchemaVersion, TestID: "run", Status: status, Outcome: "monitoring_pending", MonitoringStatus: "pending"}
			if err := validateCIRunAPIResponse(response, "run"); err != nil {
				t.Fatalf("valid delayed evidence response rejected: %v", err)
			}
			if isTerminalCIResponse(response) {
				t.Fatal("CLI stopped polling before reconciliation finished")
			}
		})
	}
	for _, status := range []string{"QUEUED", "RUNNING", "PAUSED"} {
		response := &CIRunResponse{SchemaVersion: ciSchemaVersion, TestID: "run", Status: status, Outcome: "monitoring_pending"}
		if err := validateCIRunAPIResponse(response, "run"); err == nil {
			t.Fatalf("reconciliation before terminal load status %s accepted", status)
		}
	}
}
