package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInternalTransitionTokensAreNotJSONExposed(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{
			name:  "scenario schedule occurrence",
			value: ScenarioScheduleOccurrence{ID: "occurrence", TransitionToken: "scenario-secret-fence"},
		},
		{
			name:  "execution plan schedule occurrence",
			value: ExecutionPlanScheduleOccurrence{ID: "occurrence", TransitionToken: "plan-schedule-secret-fence"},
		},
		{
			name:  "execution plan run",
			value: ExecutionPlanRun{ID: "run", TransitionToken: "plan-run-secret-fence"},
		},
		{
			name: "archive finalization",
			value: ArchiveFinalizationJob{
				TestID:       "archive-test",
				IntentToken:  "archive-secret-fence",
				RefreshToken: "refresh-secret-fence",
			},
		},
		{
			name:  "archive refresh",
			value: ArchiveRefreshJob{TestID: "archive-test", IntentToken: "refresh-job-secret-fence"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			payload := string(encoded)
			if strings.Contains(payload, "transition_token") || strings.Contains(payload, "intent_token") || strings.Contains(payload, "refresh_token") || strings.Contains(payload, "secret-fence") {
				t.Fatalf("internal transition token leaked in JSON: %s", payload)
			}
			if !strings.Contains(payload, `"id"`) && !strings.Contains(payload, `"test_id"`) {
				t.Fatalf("public fields missing from JSON: %s", payload)
			}
		})
	}
}
