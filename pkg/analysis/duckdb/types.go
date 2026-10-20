// Package duckdb provides approved analytical operations. It exposes no SQL
// execution API and is imported only by controllers and desktop applications.
package duckdb

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

var ErrUnavailable = errors.New("native analytics unavailable: build controller with CGO and analytics_duckdb")

// ErrIncompatibleHistogramBounds refuses a histogram series whose selected
// rows declare different bounds: only identical bounds merge. It crosses the
// worker boundary as its own result code.
var ErrIncompatibleHistogramBounds = errors.New("histogram bounds differ within the selected series; only identical bounds merge")
var ErrBusy = errors.New("another heavy analytics job is running")

type Column = analysis.ParquetColumnPath
type Measure = analysis.ParquetMeasure
type Mapping = analysis.ParquetMapping

func unitScale(unit string) (string, float64, error) { return analysis.NormalizeUnit(unit) }

type NormalizeRequest struct {
	ProjectID                string
	DatasetID                string
	Files                    []string
	Mapping                  Mapping
	AllowRejected            bool
	PrecisionAcknowledgement string
	verifiedInputHashes      []string // child-only hashes verified against the immutable bootstrap
}

// NormalizedFilePartition binds a bounded producer summary to its physical output.
// File is an owned local output, never a durable artifact key.
type NormalizedFilePartition struct {
	File   string                            `json:"file"`
	Bytes  int64                             `json:"bytes"`
	SHA256 string                            `json:"sha256"`
	Stats  analysis.NormalizedPartitionStats `json:"stats"`
}
type NormalizeResult struct {
	Partitions     []NormalizedFilePartition `json:"partitions"`
	Files          []string                  `json:"files"`
	RejectionFiles []string                  `json:"rejection_files,omitempty"`
	Rejections     []RejectionSummary        `json:"rejections,omitempty"`
	Accepted       int64                     `json:"accepted"`
	Rejected       int64                     `json:"rejected"`
	InputRows      int64                     `json:"input_rows"`
}
type RejectionSummary = analysis.ParquetRejectionSummary
type SeriesQuery struct {
	ProjectID             string            `json:"project_id"`
	DatasetID             string            `json:"dataset_id"`
	Metric                string            `json:"metric"`
	IdentitySchemaVersion int               `json:"identity_schema_version,omitempty"`
	SourceID              string            `json:"source_id,omitempty"`
	Host                  string            `json:"host,omitempty"`
	Service               string            `json:"service,omitempty"`
	Dimensions            map[string]string `json:"dimensions,omitempty"`
	From                  time.Time         `json:"from"`
	To                    time.Time         `json:"to"`
	BucketSeconds         int               `json:"bucket_seconds"`
	Aggregation           string            `json:"aggregation"`
	// Derivation is evaluated by the controller over the worker's points
	// with the same pkg/analysis functions; it never reaches the worker.
	Derivation *analysis.QueryDerivation `json:"derivation,omitempty"`
	// CounterReadings reads a counter as each bucket's closing reading
	// (aggregation max; a bucket whose readings fall is missing with
	// counter_reset) for a controller-side counter derivation. The
	// controller sets it; a display query converts counters to rates.
	CounterReadings bool `json:"counter_readings,omitempty" openapi:"-"`
}
type Preview struct {
	Schema  analysis.ParquetSchemaPreview `json:"schema"`
	Samples []map[string]any              `json:"samples"`
}

type Engine struct {
	root             string
	MemoryLimit      string
	SpillLimit       string
	Timeout          time.Duration
	workerBeforeHash func()
	workerThreads    int
	workerProfile    workerProcessProfile
	borrowedParent   *NativePipeline
	workerMu         sync.Mutex
	workerResult     *workerRunResult
	workerOutput     map[string]workerOutput
}

func New(root string) (*Engine, error) {
	if !Available() {
		return nil, ErrUnavailable
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	return &Engine{root: absolute, MemoryLimit: "2GiB", SpillLimit: "8GiB", Timeout: 15 * time.Minute}, nil
}

func (e *Engine) authorizedPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(e.root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", errors.New("analytics file is outside staging root")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("analytics input must be a regular file")
	}
	return resolved, nil
}

func (q SeriesQuery) Validate() error {
	if q.ProjectID == "" || q.DatasetID == "" || q.Metric == "" || len(q.Metric) > 256 || q.From.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > 366*24*time.Hour || q.BucketSeconds < 1 || q.BucketSeconds > 86400 || math.Ceil(q.To.Sub(q.From).Seconds()/float64(q.BucketSeconds)) > analysis.MaxSeriesPoints {
		return errors.New("invalid or excessive analytical series window")
	}
	if err := validateNativeSeriesIdentity(q); err != nil {
		return err
	}
	if analysis.ValidSummaryStatistic(q.Aggregation) {
		// A declared summary's statistic; the engine checks it against the data.
		return nil
	}
	return analysis.ValidateScalarAggregationName(q.Aggregation)
}

// API callers use context cancellation and bounded job deadlines; no operation
// takes ownership of a caller's files or exposes arbitrary SQL.
type Operations interface {
	Preview(context.Context, string) (Preview, error)
	Normalize(context.Context, NormalizeRequest) (NormalizeResult, error)
	Series(context.Context, []string, SeriesQuery) ([]analysis.MetricPoint, error)
}
