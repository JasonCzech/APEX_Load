package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	adapterRetirementDocPath  = "../../docs/qa/ADAPTER_RETIREMENT.md"
	adapterRetirementTestPath = "../../dashboard/src/adapterRetirement.test.ts"
)

func TestAuthoringAdapterRetirementRatchet(t *testing.T) {
	properties := lifecycleAuth2Properties()
	for _, row := range lifecycleAuth2Rows() {
		cells := properties[row.Entry]
		if cells == nil {
			t.Fatalf("AUTH-2 row %q is missing from lifecycleAuth2Properties", row.Entry)
		}
		for _, prop := range lifecycleProperties {
			proofs := cells[prop]
			if len(proofs) == 0 {
				t.Errorf("AUTH-2 row %q / %q has no proofs", row.Entry, prop)
			}
			for _, proof := range proofs {
				if proof.Kind == "open" {
					t.Errorf("AUTH-2 row %q / %q still has an open cell: %s", row.Entry, prop, proof.Title)
				}
			}
		}
	}

	matrixRaw, err := os.ReadFile(lifecycleParityMatrixPath)
	if err != nil {
		t.Fatalf("read %s: %v", lifecycleParityMatrixPath, err)
	}
	matrixDoc := string(matrixRaw)
	if strings.Contains(matrixDoc, "| open |") || strings.Contains(matrixDoc, "**open**:") {
		t.Errorf("%s still contains open property cells", lifecycleParityMatrixPath)
	}

	retirementRaw, err := os.ReadFile(adapterRetirementDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", adapterRetirementDocPath, err)
	}
	retirementDoc := string(retirementRaw)
	if strings.Contains(retirementDoc, "remains open") {
		t.Errorf("%s still describes lifecycle edges or the AUTH-2 property matrix as open", adapterRetirementDocPath)
	}

	ratchetRaw, err := os.ReadFile(adapterRetirementTestPath)
	if err != nil {
		t.Fatalf("read %s: %v", adapterRetirementTestPath, err)
	}
	ratchetSource := string(ratchetRaw)
	for _, requiredSeam := range []string{
		"aliasImportState",
		"CompatibleDebugStartRequest",
		"ScenarioFormState",
		"newTestRunModel",
		"testExecutionScenarioModel",
		"useScriptAuthoringSession",
		"scenarioAuthoringDraftV2.ts",
	} {
		if !strings.Contains(retirementDoc, requiredSeam) {
			t.Errorf("%s is missing seam reference %q", adapterRetirementDocPath, requiredSeam)
		}
		if !strings.Contains(ratchetSource, requiredSeam) {
			t.Errorf("%s is missing seam enforcement for %q", adapterRetirementTestPath, requiredSeam)
		}
	}

	scenarioAuthoringRaw, err := os.ReadFile(filepath.Join("../../dashboard/src/types/scenarioAuthoring.ts"))
	if err != nil {
		t.Fatalf("read scenarioAuthoring.ts: %v", err)
	}
	scenarioAuthoring := string(scenarioAuthoringRaw)
	for _, forbiddenMirror := range []string{
		"runtime_pacing_",
		"runtime_think_time_",
		"runtime_ignore_think_time:",
		"runtime_log_level:",
		"runtime_auto_transactions:",
		"runtime_iterations:",
		"goal_target_average_tps:",
		"assignment_mode:",
	} {
		if strings.Contains(scenarioAuthoring, forbiddenMirror) {
			t.Errorf("ScenarioFormState reintroduced retired flat mirror %q", forbiddenMirror)
		}
	}
}
