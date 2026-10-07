package rca

import "time"

// DetectParamsVersion identifies the onset-detection parameters an assessment
// used. Change it whenever a value below changes; WP3.9 recalibrates them on
// the calibration split only.
const DetectParamsVersion = "detect.v2"

// DetectParams are onset detection's starting values.
type DetectParams struct {
	Version string `json:"version"`
	// MADScale turns a median absolute deviation into a normal standard
	// deviation.
	MADScale float64 `json:"mad_scale"`
	// The robust scale is never below RelativeFloor of the baseline median
	// or a unit floor: LatencyFloorMs for latency, ErrorRateFloorPct
	// (percentage points) for error rates and ThroughputFloor (per second)
	// for throughput. A series of another unit falls back to UnitFloor only
	// when its baseline has no spread and a zero median.
	RelativeFloor     float64 `json:"relative_floor"`
	LatencyFloorMs    float64 `json:"latency_floor_ms"`
	ErrorRateFloorPct float64 `json:"error_rate_floor_pct"`
	ThroughputFloor   float64 `json:"throughput_floor_per_s"`
	UnitFloor         float64 `json:"unit_floor"`
	// ScoreFloor is the robust scale's floor for unitless scores (layout
	// shift): a change well below it builds no onset.
	ScoreFloor float64 `json:"score_floor"`
	// Two-sided CUSUM on robust z-scores: K is the allowance, H the alarm.
	K float64 `json:"cusum_k"`
	H float64 `json:"cusum_h"`
	// MinBaselineBuckets must precede the first tested bucket; the in-run
	// baseline stops growing at MaxBaselineBuckets, so a slow drift is
	// measured against the level's start rather than absorbed. A larger cap
	// estimates the baseline better: on healthy synthetic series 120 buckets
	// give about a third of the false onsets 30 do.
	MinBaselineBuckets int `json:"min_baseline_buckets"`
	MaxBaselineBuckets int `json:"max_baseline_buckets"`
	// ConfirmBuckets consecutive buckets beyond K declare an onset; as many
	// RecoveryBuckets within K end it. A shorter excursion is a spike.
	ConfirmBuckets  int `json:"confirm_buckets"`
	RecoveryBuckets int `json:"recovery_buckets"`
	// LevelSettle is how much of each load level is skipped. The archive's
	// steady spans already exclude it; it is recorded so an assessment
	// states the rule it was built under.
	LevelSettle time.Duration `json:"level_settle_ns"`
	// LevelTolerance is how far two loads may differ and still be the same
	// level.
	LevelTolerance float64 `json:"level_tolerance"`
	// A gap longer than MaxGapBuckets resets the CUSUM.
	MaxGapBuckets int `json:"max_gap_buckets"`
	// Output bounds; the largest effects are kept.
	MaxAnomalies int `json:"max_anomalies"`
	MaxSpikes    int `json:"max_spikes"`
}

// DefaultDetectParams are the plan's starting values.
var DefaultDetectParams = DetectParams{
	Version:  DetectParamsVersion,
	MADScale: 1.4826, RelativeFloor: 0.05, LatencyFloorMs: 5, ErrorRateFloorPct: 0.5, ThroughputFloor: 0.1, UnitFloor: 1, ScoreFloor: 0.01,
	K: 0.5, H: 5,
	MinBaselineBuckets: 10, MaxBaselineBuckets: 120,
	ConfirmBuckets: 3, RecoveryBuckets: 3,
	LevelSettle: 30 * time.Second, LevelTolerance: 0.05,
	MaxGapBuckets: 3,
	MaxAnomalies:  500, MaxSpikes: 200,
}
