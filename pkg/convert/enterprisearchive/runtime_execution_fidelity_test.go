package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSourceRunOrderingSettingsRemainCompatibilityOnlyAndPolicyBlocked(t *testing.T) {
	runtimeSections := map[string]map[string]map[string]string{
		"default.usp": {
			"RunLogicRunRoot": {
				"RunLogicRunMode":     "Random",
				"RunLogicActionOrder": "Action",
			},
			"Profile Actions": {
				"Profile Actions name": "vuser_init,Action,vuser_end",
			},
		},
	}

	dispositions := classifyRuntimeSelectedSections(runtimeSections, "http")
	for _, source := range []struct {
		section string
		key     string
	}{
		{section: "RunLogicRunRoot", key: "RunLogicRunMode"},
		{section: "RunLogicRunRoot", key: "RunLogicActionOrder"},
		{section: "Profile Actions", key: "Profile Actions name"},
	} {
		disposition := dispositions["default.usp"][source.section][source.key]
		if disposition.Status != RuntimeSettingDispositionCompatibilityOnly {
			t.Fatalf("%s/%s disposition = %#v, want compatibility_only", source.section, source.key, disposition)
		}
	}

	parity := BuildRTSParityReport(runtimeSections)
	for _, id := range []string{"run_logic_block_scheduling", "run_logic_action_sequence"} {
		entry := runtimeParityEntryByID(t, parity, id)
		if entry.Status != RuntimeSettingStatusCompatibilityOnly {
			t.Fatalf("%s status = %q, want compatibility_only", id, entry.Status)
		}
		if strings.Contains(strings.ToLower(entry.Notes), "enforced") ||
			!strings.Contains(strings.ToLower(entry.Notes), "does not consume") {
			t.Fatalf("%s notes overclaim execution: %q", id, entry.Notes)
		}
	}

	readiness := BuildRTSMigrationReadiness(parity, RuntimeSettingMigrationPolicy{
		BlockStatuses: []RuntimeSettingStatus{RuntimeSettingStatusCompatibilityOnly},
	})
	if readiness == nil || !readiness.Blocked {
		t.Fatalf("migration readiness = %#v, want compatibility policy to block execution readiness", readiness)
	}
	if len(readiness.BlockingSettings) != 2 {
		t.Fatalf("blocking settings = %#v, want both inert ordering settings", readiness.BlockingSettings)
	}
}

func TestScriptRunOrderingFieldsCannotClaimExecutedParity(t *testing.T) {
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{
				BlockScheduling: "percentage",
				ActionSequence:  []string{"teardown", "action"},
			},
		},
	}

	parity := BuildRTSParityReportFromScript(script)
	for _, id := range []string{"run_logic_block_scheduling", "run_logic_action_sequence"} {
		entry := runtimeParityEntryByID(t, parity, id)
		if entry.Status != RuntimeSettingStatusCompatibilityOnly {
			t.Fatalf("%s script-derived status = %q, want compatibility_only", id, entry.Status)
		}
	}

	readiness := BuildRTSMigrationReadiness(parity, RuntimeSettingMigrationPolicy{
		BlockStatuses: []RuntimeSettingStatus{RuntimeSettingStatusCompatibilityOnly},
	})
	if readiness == nil || !readiness.Blocked {
		t.Fatalf("script-derived readiness = %#v, want unsupported ordering semantics blocked by executable policy", readiness)
	}
}
