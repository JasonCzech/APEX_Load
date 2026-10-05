package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	envArchiveIntervalDistributionsMaxBytes = "APEXLOAD_ARCHIVE_INTERVAL_DISTRIBUTIONS_MAX_BYTES"

	intervalDistributionsArchiveFileName = "interval_distributions.jsonl"
	intervalDistributionSchema           = "apexload.interval_distribution.v1"
	intervalDistributionRecordType       = "interval_distributions_v1"

	// Sources: copied from the live native evidence, or rebuilt from archived
	// transaction rows for runs that kept no mergeable distributions.
	intervalDistributionSourceNative = "native_evidence"
	intervalDistributionSourceRows   = "reconstructed_from_rows"

	// Percentile quality per key.
	intervalPercentileNativeDigest = "native_tdigest"
	intervalPercentileNativeScalar = "native_scalar"
	intervalPercentileRows         = "reconstructed_from_rows"

	// Engine digests hold nanoseconds; scalar percentiles are milliseconds.
	intervalDistributionDigestUnit = "ns"
	// intervalDistributionCompression matches the engine's duration digests.
	intervalDistributionCompression = 200
)

// Reconstruction bounds, shared with the archive-mode native evidence query.
// Variables so tests can exercise truncation without huge fixtures.
var (
	intervalDistributionMaxObservations = 100000
	intervalDistributionMaxBuckets      = 1000
	intervalDistributionMaxKeys         = 128
)

// IntervalDistributionRecord is one collection interval's duration
// distributions, per dimension and key. Percentiles are never averaged across
// intervals; merge the digests instead.
type IntervalDistributionRecord struct {
	Schema          string                                           `json:"schema"`
	Timestamp       time.Time                                        `json:"timestamp"`
	WindowSeconds   float64                                          `json:"window_seconds"`
	Source          string                                           `json:"source"`
	EvidenceVersion int                                              `json:"evidence_version,omitempty"`
	DigestUnit      string                                           `json:"digest_unit"`
	Incomplete      bool                                             `json:"incomplete,omitempty"`
	Dimensions      map[string]map[string]IntervalDistributionMetric `json:"dimensions"`
}

// IntervalDistributionMetric is one key's distribution within an interval.
// P50 to P99 are milliseconds as the source reported them, or quantiles of a
// reconstructed digest.
type IntervalDistributionMetric struct {
	Count             int64                   `json:"count"`
	Errors            int64                   `json:"errors"`
	DurationDigest    *engine.TDigestSnapshot `json:"duration_digest,omitempty"`
	PercentileQuality string                  `json:"percentile_quality"`
	P50               float64                 `json:"p50_ms"`
	P90               float64                 `json:"p90_ms"`
	P95               float64                 `json:"p95_ms"`
	P99               float64                 `json:"p99_ms"`
}

// persistIntervalDistributionArchive writes interval_distributions.jsonl once,
// after finalize. Later adjunct refreshes keep the first sidecar because the
// stored history it was copied from may since have been pruned.
//
// The sidecar has its own byte budget. Sharing the adjunct budget would let a
// long run's distributions exhaust it and fail the archive manifest write that
// follows. When its own budget runs out the sidecar stops early and is marked
// truncated.
func (tr *TestRunner) persistIntervalDistributionArchive(ctx context.Context, testID string, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if manifest.IntervalDistributionsFile != "" {
		return nil
	}
	budget, err := newIntervalDistributionBudget(ctx, record)
	if err != nil {
		return err
	}
	defer budget.close()
	reader, _ := tr.testStore.(storage.NativeIntervalEvidenceReader)
	var count int64
	source, truncated := intervalDistributionSourceNative, false
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, intervalDistributionsArchiveFileName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		out := &archiveOutputFile{file: file, budget: budget}
		emit := func(r IntervalDistributionRecord) error {
			line, err := json.Marshal(r)
			if err != nil {
				return err
			}
			line = append(line, '\n')
			if !budget.admits(len(line)) {
				return errIntervalDistributionBudget
			}
			if _, err := out.Write(line); err != nil {
				return err
			}
			count++
			return nil
		}
		stopped := func(err error) (bool, error) {
			if errors.Is(err, errIntervalDistributionBudget) {
				return true, nil
			}
			return false, err
		}
		if reader != nil {
			skipped, err := copyNativeIntervalDistributions(ctx, reader, testID, emit)
			cut, err := stopped(err)
			if err != nil {
				return 0, err
			}
			truncated = cut || skipped > 0 && count > 0
			if cut {
				return 0, nil
			}
		}
		if count == 0 {
			source = intervalDistributionSourceRows
			bounded, err := reconstructIntervalDistributions(ctx, record, emit)
			cut, err := stopped(err)
			if err != nil {
				return 0, err
			}
			truncated = bounded || cut
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	manifest.IntervalDistributionsFile = intervalDistributionsArchiveFileName
	manifest.IntervalDistributionCount = count
	manifest.IntervalDistributionsSource = source
	manifest.IntervalDistributionsTruncated = truncated
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: intervalDistributionsArchiveFileName, Kind: "evidence", Format: "jsonl", RecordType: intervalDistributionRecordType, Count: count})
}

// newIntervalDistributionBudget sizes the sidecar's own allowance. It keeps
// the adjunct free-space floor and shares the archive root's backlog budget.
func newIntervalDistributionBudget(ctx context.Context, record *storage.RunArchiveRecord) (*archiveOutputBudget, error) {
	root, err := archiveBacklogRootForRecord(record)
	if err != nil {
		return nil, err
	}
	limit, err := collectionCacheByteLimit(envArchiveIntervalDistributionsMaxBytes, 256<<20, 4<<30)
	if err != nil {
		return nil, err
	}
	minFree, err := collectionCacheByteLimit(envArchiveAdjunctMinFreeBytes, 64<<20, 1<<50)
	if err != nil {
		return nil, err
	}
	backlog, err := newArchiveBacklogBudget(root)
	if err != nil {
		return nil, err
	}
	return &archiveOutputBudget{backlog: backlog, root: root, remaining: limit, minFree: minFree, limitErr: errArchiveAdjunctLimit, freeSpaceErr: errArchiveAdjunctFreeSpace, ctx: ctx}, nil
}

// copyNativeIntervalDistributions copies every stored interval whose native
// evidence keeps mergeable distributions (version 2 and later). It returns how
// many stored intervals were skipped for lacking them.
func copyNativeIntervalDistributions(ctx context.Context, reader storage.NativeIntervalEvidenceReader, testID string, emit func(IntervalDistributionRecord) error) (int, error) {
	skipped := 0
	var previous time.Time
	err := reader.ForEachNativeIntervalEvidence(ctx, testID, func(timestamp time.Time, evidence *storage.NativeIntervalEvidence) error {
		if evidence == nil || evidence.Version < 2 {
			skipped++
			return nil
		}
		timestamp = timestamp.UTC()
		if !previous.IsZero() && !timestamp.After(previous) {
			skipped++ // a repeated timestamp is ambiguous; keep the first
			return nil
		}
		previous = timestamp
		out := IntervalDistributionRecord{Schema: intervalDistributionSchema, Timestamp: timestamp, WindowSeconds: evidence.WindowSeconds, Source: intervalDistributionSourceNative, EvidenceVersion: evidence.Version, DigestUnit: intervalDistributionDigestUnit, Incomplete: evidence.Incomplete, Dimensions: map[string]map[string]IntervalDistributionMetric{}}
		for dimension, values := range evidence.Dimensions {
			keys := make(map[string]IntervalDistributionMetric, len(values))
			for key, metric := range values {
				keys[key] = intervalDistributionFromNative(metric)
			}
			out.Dimensions[dimension] = keys
		}
		return emit(out)
	})
	return skipped, err
}

func intervalDistributionFromNative(m storage.NativeIntervalMetric) IntervalDistributionMetric {
	out := IntervalDistributionMetric{Count: m.Count, Errors: m.Errors, PercentileQuality: intervalPercentileNativeScalar, P50: m.P50, P90: m.P90, P95: m.P95, P99: m.P99}
	if m.DurationDigest != nil && engine.ValidateDurationDigest(m.DurationDigest, m.DurationDigest.Count) == nil {
		out.DurationDigest = engine.CloneTDigestSnapshot(m.DurationDigest)
		out.PercentileQuality = intervalPercentileNativeDigest
	}
	return out
}

// intervalWindow is one archived interval's (start, end] span; summary is the
// index of the interval summary it came from.
type intervalWindow struct {
	start, end time.Time
	summary    int
}

// archiveIntervalWindows turns archived interval summaries into
// non-overlapping (start, end] windows. A summary without a recorded window
// takes the spacing to its neighbour; repeated timestamps keep the first.
func archiveIntervalWindows(ordered []IntervalSummaryRecord) []intervalWindow {
	windows := make([]intervalWindow, 0, len(ordered))
	for i, summary := range ordered {
		end := summary.Timestamp.UTC()
		if len(windows) > 0 && !end.After(windows[len(windows)-1].end) {
			continue
		}
		span := time.Duration(summary.SampleWindowSeconds * float64(time.Second))
		if span <= 0 {
			switch {
			case len(windows) > 0:
				span = end.Sub(windows[len(windows)-1].end)
			case i+1 < len(ordered):
				span = ordered[i+1].Timestamp.UTC().Sub(end)
			}
		}
		if span <= 0 {
			continue
		}
		start := end.Add(-span)
		if len(windows) > 0 && start.Before(windows[len(windows)-1].end) {
			start = windows[len(windows)-1].end
		}
		windows = append(windows, intervalWindow{start: start, end: end, summary: i})
	}
	return windows
}

// reconstructIntervalDistributions rebuilds transaction and action
// distributions per archived interval with one streaming scan of the rows.
// It reports whether any bound cut the result.
func reconstructIntervalDistributions(ctx context.Context, record *storage.RunArchiveRecord, emit func(IntervalDistributionRecord) error) (bool, error) {
	summaries, err := readAllArchiveSummariesContext(ctx, record)
	if err != nil {
		return false, err
	}
	sort.SliceStable(summaries, func(i, j int) bool { return summaries[i].Timestamp.Before(summaries[j].Timestamp) })
	windows := archiveIntervalWindows(summaries)
	truncated := false
	if len(windows) > intervalDistributionMaxBuckets {
		windows, truncated = windows[:intervalDistributionMaxBuckets], true
	}
	if len(windows) == 0 {
		return truncated, nil
	}
	type accumulator struct {
		digest        *engine.TDigest
		count, errors int64
	}
	// buckets[window][dimension][key]
	buckets := make([]map[string]map[string]*accumulator, len(windows))
	observations := 0
	err = forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		at := tx.Timestamp.UTC()
		index := sort.Search(len(windows), func(i int) bool { return !windows[i].end.Before(at) })
		if index == len(windows) || !at.After(windows[index].start) {
			return nil
		}
		dimension := archiveEventPopulation(*tx)
		if dimension != "transaction" && dimension != "action" {
			return nil
		}
		if tx.ActionName == "" || len(tx.ActionName) > 256 || math.IsNaN(tx.DurationMs) || math.IsInf(tx.DurationMs, 0) || tx.DurationMs < 0 {
			return nil
		}
		if observations >= intervalDistributionMaxObservations {
			truncated = true
			return errStopIntervalReconstruction
		}
		observations++
		if buckets[index] == nil {
			buckets[index] = map[string]map[string]*accumulator{}
		}
		if buckets[index][dimension] == nil {
			buckets[index][dimension] = map[string]*accumulator{}
		}
		acc := buckets[index][dimension][tx.ActionName]
		if acc == nil {
			acc = &accumulator{digest: engine.NewTDigest(intervalDistributionCompression)}
			buckets[index][dimension][tx.ActionName] = acc
		}
		acc.count++
		if !tx.Success {
			acc.errors++
		}
		acc.digest.Add(tx.DurationMs * float64(time.Millisecond))
		return nil
	})
	if err != nil && !errors.Is(err, errStopIntervalReconstruction) {
		return truncated, err
	}
	for i, window := range windows {
		if buckets[i] == nil {
			continue
		}
		out := IntervalDistributionRecord{Schema: intervalDistributionSchema, Timestamp: window.end, WindowSeconds: window.end.Sub(window.start).Seconds(), Source: intervalDistributionSourceRows, DigestUnit: intervalDistributionDigestUnit, Dimensions: map[string]map[string]IntervalDistributionMetric{}}
		for dimension, values := range buckets[i] {
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if len(keys) > intervalDistributionMaxKeys {
				keys, out.Incomplete, truncated = keys[:intervalDistributionMaxKeys], true, true
			}
			metrics := make(map[string]IntervalDistributionMetric, len(keys))
			for _, key := range keys {
				acc := values[key]
				snapshot := acc.digest.Snapshot()
				metrics[key] = IntervalDistributionMetric{
					Count: acc.count, Errors: acc.errors, DurationDigest: &snapshot, PercentileQuality: intervalPercentileRows,
					P50: acc.digest.Quantile(.50) / float64(time.Millisecond), P90: acc.digest.Quantile(.90) / float64(time.Millisecond),
					P95: acc.digest.Quantile(.95) / float64(time.Millisecond), P99: acc.digest.Quantile(.99) / float64(time.Millisecond),
				}
			}
			out.Dimensions[dimension] = metrics
		}
		if err := emit(out); err != nil {
			return truncated, err
		}
	}
	return truncated, nil
}

var (
	errStopIntervalReconstruction = errors.New("interval reconstruction observation bound reached")
	errIntervalDistributionBudget = errors.New("interval distribution byte budget reached")
)
