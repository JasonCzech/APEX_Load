package trafficprofile

import "time"

const (
	MaxPointsPerSeries  = 10_000
	MaxPointsPerProfile = 50_000
)

type ValueSemantics string

const (
	SemanticsTransactionsPerBucket ValueSemantics = "transactions_per_bucket"
	SemanticsCumulativeCounter     ValueSemantics = "cumulative_counter"
	SemanticsTPS                   ValueSemantics = "tps"
	SemanticsVUsers                ValueSemantics = "vusers"
)

type GapPolicy string

const (
	GapHold GapPolicy = "hold"
	GapZero GapPolicy = "zero"
	GapDrop GapPolicy = "drop"
)

type Point struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type Series struct {
	Key    string            `json:"key"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Points []Point           `json:"points"`
}

type Stats struct {
	Count          int           `json:"count"`
	Start          time.Time     `json:"start"`
	End            time.Time     `json:"end"`
	Min            float64       `json:"min"`
	Max            float64       `json:"max"`
	Mean           float64       `json:"mean"`
	DetectedBucket time.Duration `json:"detected_bucket"`
	Gaps           int           `json:"gaps"`
}

type RuntimePoint struct {
	OffsetMS int64   `json:"offset_ms"`
	Target   float64 `json:"target"`
}

type WindowSpec struct {
	Mode     string        `json:"mode,omitempty"`
	Start    time.Time     `json:"start,omitempty"`
	End      time.Time     `json:"end,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
}

type NormalizeOptions struct {
	Semantics      ValueSemantics `json:"value_semantics"`
	GapPolicy      GapPolicy      `json:"gap_policy,omitempty"`
	Window         WindowSpec     `json:"window,omitempty"`
	SourceEnd      time.Time      `json:"source_end,omitempty"`
	TargetDuration time.Duration  `json:"target_duration,omitempty"`
	AmplitudeMode  string         `json:"amplitude_mode,omitempty"`
	Amplitude      float64        `json:"amplitude,omitempty"`
}

type NormalizedSeries struct {
	Source         Series         `json:"source"`
	Points         []RuntimePoint `json:"points"`
	SourceDuration time.Duration  `json:"source_duration"`
	TargetDuration time.Duration  `json:"target_duration"`
	DetectedBucket time.Duration  `json:"detected_bucket"`
	RuntimeTarget  string         `json:"runtime_target"`
	Warnings       []string       `json:"warnings,omitempty"`
}

type Sheet struct {
	Name    string     `json:"name"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"-"`
	Samples [][]string `json:"samples,omitempty"`
}

type TableOptions struct {
	TimestampColumn string
	ValueColumns    []string
	SeriesColumn    string
	Timezone        *time.Location
}
