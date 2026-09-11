package api

import (
	"math"
	"testing"
)

func TestValidateScenarioRequestTransactionResponseTimeSLAThresholds(t *testing.T) {
	valid := 0.001
	if _, err := validateScenarioRequest(CreateTestRequest{Duration: "1s", GoalConfig: &GoalConfig{TransactionResponseTimeSLAMs: &valid}, Groups: []CreateGroupEntry{{Name: "A", TransactionResponseTimeSLAMs: &valid}}}); err != nil {
		t.Fatalf("valid thresholds: %v", err)
	}
	if _, err := validateScenarioRequest(CreateTestRequest{Duration: "1s", ScenarioMode: "goal_oriented", GoalConfig: &GoalConfig{TransactionResponseTimeSLAMs: &valid, StopOnSLABreach: true}}); err == nil {
		t.Fatal("final-only transaction SLA unexpectedly enabled stop_on_sla_breach")
	}
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		value := value
		t.Run("default", func(t *testing.T) {
			if _, err := validateScenarioRequest(CreateTestRequest{Duration: "1s", GoalConfig: &GoalConfig{TransactionResponseTimeSLAMs: &value}}); err == nil {
				t.Fatalf("default %v unexpectedly accepted", value)
			}
		})
		t.Run("group", func(t *testing.T) {
			if _, err := validateScenarioRequest(CreateTestRequest{Duration: "1s", Groups: []CreateGroupEntry{{Name: "A", TransactionResponseTimeSLAMs: &value}}}); err == nil {
				t.Fatalf("group %v unexpectedly accepted", value)
			}
		})
	}
}
