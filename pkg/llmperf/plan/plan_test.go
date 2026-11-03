package plan

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/llmperf/econ"
)

func TestEvaluateCapacityPlanAndKneeDetection(t *testing.T) {
	res, err := Evaluate(Request{
		Steps: []StepObservation{
			{StepIndex: 0, OfferedTPS: 5, AchievedTPS: 5, TTFTP95Ms: 120, TPOTP95Ms: 15, SystemOutputTPS: 600, ErrorRatePct: 0},
			{StepIndex: 1, OfferedTPS: 10, AchievedTPS: 10, TTFTP95Ms: 190, TPOTP95Ms: 22, SystemOutputTPS: 1200, ErrorRatePct: 0.2},
			{StepIndex: 2, OfferedTPS: 15, AchievedTPS: 14.8, TTFTP95Ms: 280, TPOTP95Ms: 32, SystemOutputTPS: 1750, ErrorRatePct: 0.5},
			{StepIndex: 3, OfferedTPS: 20, AchievedTPS: 16.2, TTFTP95Ms: 650, TPOTP95Ms: 58, SystemOutputTPS: 1820, ErrorRatePct: 2.1},
		},
		SLA: SLAPolicy{
			MaxTTFTP95Ms:    400,
			MaxTPOTP95Ms:    45,
			MaxErrorRatePct: 1.0,
		},
		TargetPeakTPS:   50,
		HeadroomPct:     20,
		CurrentReplicas: 1,
		Deployment: &econ.DeploymentSpec{
			BillingMode:       "gpu_hourly",
			GPUCount:          1,
			GPUCostPerHourUSD: 4.00,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.KneeStepIndex != 2 || res.MaxGoodputTPS != 14.8 {
		t.Fatalf("expected knee at step 2 (14.8 TPS), got step=%d maxGoodput=%v", res.KneeStepIndex, res.MaxGoodputTPS)
	}
	// Usable per replica = 14.8 * 0.8 = 11.84 => ceil(50 / 11.84) = 5 replicas
	if res.RequiredReplicas != 5 || res.ProjectedHourlyCostUSD != 20.0 {
		t.Fatalf("expected 5 replicas ($20/hr), got replicas=%d hourly=$%v", res.RequiredReplicas, res.ProjectedHourlyCostUSD)
	}
}
