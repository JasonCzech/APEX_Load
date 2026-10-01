package api

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"math"
	"strings"
	"time"
)

const baselineComparisonRuleVersion = "baseline-comparison-v1"
const maxBaselineComparisonWindows = 128
const maxBaselineComparisonCells = 4096

type BaselineComparisonPolicy struct {
	Revision         int64                     `json:"revision"`
	LoadTolerancePct float64                   `json:"load_tolerance_pct"`
	LatencyPct       float64                   `json:"latency_pct"`
	LatencyMs        float64                   `json:"latency_ms"`
	ThroughputPct    float64                   `json:"throughput_pct"`
	ErrorPoints      float64                   `json:"error_points"`
	ResourcePct      float64                   `json:"resource_pct"`
	GroupMappings    map[string]string         `json:"group_mappings,omitempty"` // baseline run ID + / + original group -> current group
	ResourceMappings []BaselineResourceMapping `json:"resource_mappings,omitempty"`
}

type BaselineResourceMapping struct {
	RunID             string   `json:"run_id"`
	Keys              []string `json:"keys"`
	Identity          string   `json:"identity"`
	Unit              string   `json:"unit"`
	Kind              string   `json:"kind"`
	Aggregation       string   `json:"aggregation"`
	Group             string   `json:"group,omitempty"`
	AbsoluteThreshold float64  `json:"absolute_threshold,omitempty"`
	Confirmed         bool     `json:"confirmed"`
}

type BaselineComparisonWindow struct {
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
}

type BaselineComparisonRequest struct {
	MinimumLoad         float64                             `json:"minimum_load,omitempty"`
	MaximumLoad         float64                             `json:"maximum_load,omitempty"`
	BaselineIDs         []string                            `json:"baseline_ids"`
	Policy              *BaselineComparisonPolicy           `json:"policy,omitempty"`
	Windows             map[string]BaselineComparisonWindow `json:"windows,omitempty"`
	Population          string                              `json:"population"`
	AllowOtherScenarios bool                                `json:"allow_other_scenarios"`
}

type BaselineRunIdentity struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	ReleaseID        string     `json:"release_id"`
	ReleaseName      string     `json:"release_name"`
	ScenarioID       string     `json:"scenario_id"`
	Status           string     `json:"status"`
	VUsers           int        `json:"vusers"`
	PeakVUsers       int64      `json:"peak_vusers"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	Pinned           bool       `json:"pinned"`
	ArchiveAvailable bool       `json:"archive_available"`
}

type BaselineComparisonSnapshot struct {
	ID           string                      `json:"id"`
	ProjectID    string                      `json:"project_id"`
	Current      BaselineRunIdentity         `json:"current"`
	Baselines    []BaselineRunIdentity       `json:"baselines"`
	CreatedAt    time.Time                   `json:"created_at"`
	RuleVersion  string                      `json:"rule_version"`
	Request      BaselineComparisonRequest   `json:"request"`
	Policy       BaselineComparisonPolicy    `json:"policy"`
	Transactions []BaselineComparisonRow     `json:"transactions"`
	Resources    []BaselineComparisonRow     `json:"resources"`
	Setup        []BaselineSetupDifference   `json:"setup"`
	Findings     []BaselineComparisonFinding `json:"findings"`
	Warnings     []string                    `json:"warnings"`
}

type BaselineMeasurement struct {
	GRPC                 *protocol.GRPCSummary              `json:"grpc,omitempty"`
	WebSocket            *protocol.WebSocketSummary         `json:"websocket,omitempty"`
	Samples              int64                              `json:"samples"`
	Successes            int64                              `json:"successes"`
	Errors               int64                              `json:"errors"`
	Mean                 float64                            `json:"mean"`
	Min                  float64                            `json:"min"`
	Max                  float64                            `json:"max"`
	P50                  float64                            `json:"p50"`
	P90                  float64                            `json:"p90"`
	P95                  float64                            `json:"p95"`
	P99                  float64                            `json:"p99"`
	Throughput           float64                            `json:"throughput"`
	SuccessfulThroughput float64                            `json:"successful_throughput"`
	ErrorPct             float64                            `json:"error_pct"`
	CoveragePct          float64                            `json:"coverage_pct"`
	Instances            int                                `json:"instances"`
	Points               []BaselineMetricPoint              `json:"points,omitempty"`
	SLA                  []TransactionResponseTimeSLAResult `json:"sla,omitempty"`
}

type BaselineMetricPoint struct {
	Seconds float64 `json:"seconds"`
	Value   float64 `json:"value"`
	Load    float64 `json:"load"`
}

type BaselineLoadWindow struct {
	BaselineComparisonWindow
	Load      float64            `json:"load"`
	TotalLoad float64            `json:"total_load"`
	Group     string             `json:"group"`
	Direction string             `json:"direction"`
	Mix       map[string]float64 `json:"mix"`
}

type BaselineComparisonCell struct {
	Measurement   *BaselineMeasurement `json:"measurement,omitempty"`
	Window        *BaselineLoadWindow  `json:"window,omitempty"`
	Qualification []string             `json:"qualification"`
	Approximate   bool                 `json:"approximate"`
	Delta         *float64             `json:"delta,omitempty"`
	DeltaPct      *float64             `json:"delta_pct,omitempty"`
}

type BaselineComparisonRow struct {
	ID        string                            `json:"id"`
	Name      string                            `json:"name"`
	Group     string                            `json:"group"`
	Protocol  string                            `json:"protocol,omitempty"`
	Source    string                            `json:"source"`
	Unit      string                            `json:"unit"`
	Current   *BaselineMeasurement              `json:"current,omitempty"`
	Window    *BaselineLoadWindow               `json:"window,omitempty"`
	Baselines map[string]BaselineComparisonCell `json:"baselines"`
}

type BaselineSetupDifference struct {
	BaselineID string `json:"baseline_id"`
	Field      string `json:"field"`
	Current    string `json:"current"`
	Baseline   string `json:"baseline"`
	Status     string `json:"status"`
}

type BaselineComparisonFinding struct {
	BaselineID string `json:"baseline_id"`
	RowID      string `json:"row_id"`
	Category   string `json:"category"`
	Metric     string `json:"metric"`
	Message    string `json:"message"`
	Status     string `json:"status"`
}

func defaultBaselineComparisonPolicy() BaselineComparisonPolicy {
	return BaselineComparisonPolicy{LoadTolerancePct: 5, LatencyPct: 10, LatencyMs: 10, ThroughputPct: 10, ErrorPoints: 1, ResourcePct: 20}
}

func validateBaselineComparisonPolicy(p BaselineComparisonPolicy) error {
	for _, v := range []float64{p.LoadTolerancePct, p.LatencyPct, p.LatencyMs, p.ThroughputPct, p.ErrorPoints, p.ResourcePct} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100000 {
			return fmt.Errorf("thresholds must be finite nonnegative values")
		}
	}
	if p.Revision < 0 || p.LoadTolerancePct > 25 || p.ErrorPoints > 100 || len(p.GroupMappings) > 200 || len(p.ResourceMappings) > 200 {
		return fmt.Errorf("comparison policy exceeds supported limits")
	}
	for k, v := range p.GroupMappings {
		if k == "" || v == "" || len(k) > 512 || len(v) > 256 {
			return fmt.Errorf("invalid group mapping")
		}
	}
	identities := map[string]bool{}
	for i, m := range p.ResourceMappings {
		for _, previous := range p.ResourceMappings[:i] {
			if previous.Identity == m.Identity && (previous.RunID == "*" || m.RunID == "*") {
				return fmt.Errorf("wildcard and run-specific resource mappings must not overlap")
			}
		}
		identity := m.RunID + "/" + m.Identity
		if identities[identity] {
			return fmt.Errorf("duplicate resource mapping identity")
		}
		identities[identity] = true
		keys := map[string]bool{}
		for _, key := range m.Keys {
			if keys[key] {
				return fmt.Errorf("duplicate metric key in aggregation")
			}
			keys[key] = true
		}
		if strings.TrimSpace(m.RunID) == "" || strings.TrimSpace(m.Unit) == "" || strings.TrimSpace(m.Identity) == "" || len(m.Identity) > 256 || len(m.Keys) == 0 || len(m.Keys) > 100 || len(m.Unit) > 64 || len(m.Group) > 256 || len(m.RunID) > 128 {
			return fmt.Errorf("resource mappings need bounded identities and keys")
		}
		if m.Kind != "gauge" && m.Kind != "counter" {
			return fmt.Errorf("resource mapping kind must be gauge or counter")
		}
		if m.Aggregation != "sum" && m.Aggregation != "mean" && m.Aggregation != "max" {
			return fmt.Errorf("resource aggregation must be sum, mean, or max")
		}
		_, unitFactor := canonicalBaselineUnit(m.Unit)
		if !finiteComparison(m.AbsoluteThreshold) || !finiteComparison(m.AbsoluteThreshold*unitFactor) || m.AbsoluteThreshold < 0 {
			return fmt.Errorf("invalid resource absolute threshold")
		}
		for _, k := range m.Keys {
			if strings.TrimSpace(k) == "" || len(k) > 1024 {
				return fmt.Errorf("resource key too long")
			}
		}
	}
	return nil
}

func finiteComparison(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func baselineDelta(a, b float64) (*float64, *float64) {
	d := a - b
	if b == 0 {
		return &d, nil
	}
	p := 100 * d / math.Abs(b)
	return &d, &p
}
