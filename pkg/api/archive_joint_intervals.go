package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Joint evidence v4 archive (B5). After finalize, a run with a joint evidence
// projection gets joint_intervals_v4.jsonl (and .parquet): one row per
// measured tuple of each retained source publication (a generator batch, or
// a local window), one for the samples past the tuple bound, and one per
// publication whose source had no joint evidence.

const (
	jointIntervalsFileName           = "joint_intervals_v4.jsonl"
	jointIntervalsParquetFileName    = "joint_intervals_v4.parquet"
	jointIntervalRecordType          = "joint_intervals_v4"
	jointIntervalSchemaVersion       = "apexload.joint_interval.v4"
	envArchiveJointIntervalsMaxBytes = "APEXLOAD_ARCHIVE_JOINT_INTERVALS_MAX_BYTES"
	// jointIntervalsParquetOverhead is the Parquet copy's room beyond the
	// JSONL's allowance for its own format (schema, footer, page headers).
	jointIntervalsParquetOverhead = 1 << 20
)

// JointIntervalRecord is one archived joint evidence row.
type JointIntervalRecord struct {
	SchemaVersion       string    `json:"schema_version"`
	Timestamp           time.Time `json:"timestamp"`
	WindowSeconds       float64   `json:"window_seconds"`
	SourceID            string    `json:"source_id"`
	SourceEpoch         string    `json:"source_epoch,omitempty"`
	ExecutionGeneration uint64    `json:"execution_generation,omitempty"`
	Sequence            uint64    `json:"sequence,omitempty"`
	Population          string    `json:"population"`
	Dimensions          []string  `json:"dimensions"`
	// Availability is "measured" for a tuple or other row; "unavailable"
	// (with Reason) for a publication whose source had no joint evidence.
	Availability   string                  `json:"availability"`
	Reason         string                  `json:"reason,omitempty"`
	Values         []string                `json:"values,omitempty"`
	Other          bool                    `json:"other,omitempty"`
	Count          int64                   `json:"count,omitempty"`
	Errors         int64                   `json:"errors,omitempty"`
	AvgMs          float64                 `json:"avg_ms,omitempty"`
	P50Ms          float64                 `json:"p50_ms,omitempty"`
	P90Ms          float64                 `json:"p90_ms,omitempty"`
	P95Ms          float64                 `json:"p95_ms,omitempty"`
	P99Ms          float64                 `json:"p99_ms,omitempty"`
	DurationDigest *engine.TDigestSnapshot `json:"duration_digest,omitempty"`
	// Incomplete and OmittedTuples describe the publication: tuples past the
	// bound are in its other row.
	Incomplete    bool  `json:"incomplete,omitempty"`
	OmittedTuples int64 `json:"omitted_tuples,omitempty"`
	// DurationSketch is the version 2 distribution of the row's durations;
	// rows from sources that predate it carry only the t-digest.
	DurationSketch *engine.DurationSketchSnapshot `json:"duration_sketch,omitempty"`
}

type parquetJointInterval struct {
	SchemaVersion       string  `parquet:"name=schema_version, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TimestampUnixMs     int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	WindowSeconds       float64 `parquet:"name=window_seconds, type=DOUBLE"`
	SourceID            string  `parquet:"name=source_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	SourceEpoch         string  `parquet:"name=source_epoch, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ExecutionGeneration int64   `parquet:"name=execution_generation, type=INT64"`
	Sequence            int64   `parquet:"name=sequence, type=INT64"`
	Population          string  `parquet:"name=population, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	DimensionsJSON      string  `parquet:"name=dimensions_json, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Availability        string  `parquet:"name=availability, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Reason              string  `parquet:"name=reason, type=BYTE_ARRAY, convertedtype=UTF8"`
	ValuesJSON          string  `parquet:"name=values_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	Other               bool    `parquet:"name=other, type=BOOLEAN"`
	Count               int64   `parquet:"name=count, type=INT64"`
	Errors              int64   `parquet:"name=errors, type=INT64"`
	AvgMs               float64 `parquet:"name=avg_ms, type=DOUBLE"`
	P50Ms               float64 `parquet:"name=p50_ms, type=DOUBLE"`
	P90Ms               float64 `parquet:"name=p90_ms, type=DOUBLE"`
	P95Ms               float64 `parquet:"name=p95_ms, type=DOUBLE"`
	P99Ms               float64 `parquet:"name=p99_ms, type=DOUBLE"`
	DurationDigestJSON  string  `parquet:"name=duration_digest_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	DurationSketchJSON  string  `parquet:"name=duration_sketch_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	Incomplete          bool    `parquet:"name=incomplete, type=BOOLEAN"`
	OmittedTuples       int64   `parquet:"name=omitted_tuples, type=INT64"`
}

func toParquetJointInterval(row JointIntervalRecord) parquetJointInterval {
	values := ""
	if row.Values != nil {
		values = mustJSON(row.Values)
	}
	digest, sketch := "", ""
	if row.DurationDigest != nil {
		digest = mustJSON(row.DurationDigest)
	}
	if row.DurationSketch != nil {
		sketch = mustJSON(row.DurationSketch)
	}
	return parquetJointInterval{SchemaVersion: row.SchemaVersion, TimestampUnixMs: row.Timestamp.UnixMilli(), WindowSeconds: row.WindowSeconds, SourceID: row.SourceID, SourceEpoch: row.SourceEpoch, ExecutionGeneration: int64(row.ExecutionGeneration), Sequence: int64(row.Sequence), Population: row.Population, DimensionsJSON: mustJSON(row.Dimensions), Availability: row.Availability, Reason: row.Reason, ValuesJSON: values, Other: row.Other, Count: row.Count, Errors: row.Errors, AvgMs: row.AvgMs, P50Ms: row.P50Ms, P90Ms: row.P90Ms, P95Ms: row.P95Ms, P99Ms: row.P99Ms, DurationDigestJSON: digest, DurationSketchJSON: sketch, Incomplete: row.Incomplete, OmittedTuples: row.OmittedTuples}
}

func jointIntervalRow(base JointIntervalRecord, metric storage.NativeIntervalMetric) JointIntervalRecord {
	base.Count, base.Errors, base.AvgMs = metric.Count, metric.Errors, metric.Avg
	base.P50Ms, base.P90Ms, base.P95Ms, base.P99Ms = metric.P50, metric.P90, metric.P95, metric.P99
	base.DurationDigest = engine.CloneTDigestSnapshot(metric.DurationDigest)
	base.DurationSketch = engine.CloneDurationSketchSnapshot(metric.DurationSketch)
	return base
}

// jointIntervalRows turns live history into archive rows, once per source
// publication.
func jointIntervalRows(history []*LiveMetrics) []JointIntervalRecord {
	var rows []JointIntervalRecord
	seen := map[string]bool{}
	for _, live := range history {
		if live != nil {
			rows = append(rows, jointIntervalEvidenceRows(live.Timestamp, live.NativeEvidence, seen)...)
		}
	}
	return rows
}

// jointIntervalEvidenceRows turns one interval's evidence into archive rows:
// its own window (a local window, or the source publication it carries) and
// the joint evidence of the other source batches accepted since the
// previous point, each unless already seen.
func jointIntervalEvidenceRows(timestamp time.Time, evidence *storage.NativeIntervalEvidence, seen map[string]bool) []JointIntervalRecord {
	if evidence == nil {
		return nil
	}
	var rows []JointIntervalRecord
	if p := evidence.Publication; p != nil {
		rows = append(rows, jointPublicationRows(p, seen)...)
	} else {
		base := JointIntervalRecord{SchemaVersion: jointIntervalSchemaVersion, Timestamp: timestamp.UTC(), WindowSeconds: evidence.WindowSeconds, SourceID: "local"}
		// A local window is identified by its time at the store's resolution
		// (PostgreSQL keeps microseconds), so the persisted and the retained
		// copy of one window are the same window.
		identity := fmt.Sprintf("local/%d", timestamp.UTC().Truncate(time.Microsecond).UnixNano())
		rows = append(rows, jointRows(base, evidence.Joint, evidence.Dimensions[storage.NativeJointDimension], identity, seen)...)
	}
	for _, p := range evidence.JointPublications {
		if p != nil {
			rows = append(rows, jointPublicationRows(p, seen)...)
		}
	}
	return rows
}

// jointPublicationRows is one source publication's rows.
func jointPublicationRows(p *storage.NativePublicationEvidence, seen map[string]bool) []JointIntervalRecord {
	base := JointIntervalRecord{SchemaVersion: jointIntervalSchemaVersion, Timestamp: p.Timestamp.UTC(), WindowSeconds: p.WindowSeconds, SourceID: p.SourceID, SourceEpoch: p.SourceEpoch,
		ExecutionGeneration: p.ExecutionGeneration, Sequence: p.Sequence}
	return jointRows(base, p.Joint, p.Dimensions[storage.NativeJointDimension], nativePublicationIdentity(p), seen)
}

func jointRows(base JointIntervalRecord, joint *storage.NativeJointEvidence, tuples map[string]storage.NativeIntervalMetric, identity string, seen map[string]bool) []JointIntervalRecord {
	if joint == nil || seen[identity] {
		return nil
	}
	seen[identity] = true
	base.Population, base.Dimensions = joint.Population, append([]string(nil), joint.Dimensions...)
	base.Availability, base.Reason, base.Incomplete, base.OmittedTuples = joint.Availability, joint.Reason, joint.Incomplete, joint.OmittedTuples
	if joint.Availability != "measured" {
		return []JointIntervalRecord{base}
	}
	keys := make([]string, 0, len(tuples))
	for key := range tuples {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]JointIntervalRecord, 0, len(keys)+1)
	for _, key := range keys {
		values, ok := engine.JointTupleValues(key, len(joint.Dimensions))
		if !ok {
			continue
		}
		row := jointIntervalRow(base, tuples[key])
		row.Values = values
		rows = append(rows, row)
	}
	if joint.Other != nil {
		row := jointIntervalRow(base, *joint.Other)
		row.Other = true
		rows = append(rows, row)
	}
	return rows
}

var errJointIntervalBudget = errors.New("joint interval archive budget exhausted")

// persistJointIntervalArchive writes joint_intervals_v4.jsonl and .parquet
// after finalize for a run with a joint evidence projection. Rows come from
// the run's persisted interval evidence, which neither in-memory history
// trimming, terminal compaction nor a controller restart shortens; the store
// itself keeps a bounded number of interval rows per run, so a run that
// reached that bound is marked truncated. A publication the
// retained live history holds but the store lacks (a history write that
// failed, or one still in flight) is added from the history, and the sidecar
// is then marked truncated, since earlier writes may have failed too. Without
// that store rows come from the retained history alone, which cannot prove
// it holds the whole run, so the sidecar is marked truncated. A failed write
// leaves neither file behind.
func (tr *TestRunner) persistJointIntervalArchive(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (err error) {
	if test == nil || record == nil || manifest == nil || manifest.JointIntervalsFile != "" || manifest.Provenance.JointEvidence == nil {
		return nil
	}
	reader, _ := tr.testStore.(storage.NativeIntervalEvidenceReader)
	// History points are replaced, never mutated, so the copied pointers are
	// read after unlocking.
	tr.mu.RLock()
	history := append([]*LiveMetrics(nil), test.History...)
	if test.LiveMetrics != nil && (len(history) == 0 || history[len(history)-1] != test.LiveMetrics) {
		history = append(history, test.LiveMetrics)
	}
	// Batches accepted after the last published point, and the count of those
	// dropped past the pending bound or released because no point of this
	// controller would carry them (a sidecar written after that lacks them)
	// (B5).
	pending := append([]*storage.NativePublicationEvidence(nil), test.pendingJointPublications...)
	pendingDropped, heldFromStart := test.jointPublicationsDropped, test.jointHeldFromStart
	tr.mu.RUnlock()
	budget, err := newEvidenceSidecarBudget(ctx, record, envArchiveJointIntervalsMaxBytes, 64<<20)
	if err != nil {
		return err
	}
	defer budget.close()
	jsonlPath, parquetPath := filepath.Join(record.ArchivePath, jointIntervalsFileName), filepath.Join(record.ArchivePath, jointIntervalsParquetFileName)
	defer func() {
		if err == nil {
			return
		}
		manifest.JointIntervalsFile, manifest.JointIntervalsParquetFile, manifest.JointIntervalCount, manifest.JointIntervalsTruncated = "", "", 0, false
		manifest.Artifacts = slices.DeleteFunc(manifest.Artifacts, func(artifact RunArchiveArtifact) bool {
			return artifact.Name == jointIntervalsFileName || artifact.Name == jointIntervalsParquetFileName
		})
		if root, openErr := os.OpenRoot(record.ArchivePath); openErr == nil {
			_ = root.Remove(jointIntervalsFileName)
			_ = root.Remove(jointIntervalsParquetFileName)
			_ = root.Close()
		}
	}()
	var count int64
	// A distributed run (the manifest names its generator sources) that this
	// controller did not hold from its start (adopted after a failover, or
	// reloaded after a restart): the batches another controller held for its
	// next point are unknown. The store keeps a bounded number of interval
	// rows per run, so a run that reached that bound may have lost its oldest.
	distributed := manifest.SourceCoverage != nil && len(manifest.SourceCoverage.Sources) > 0
	truncated := reader == nil || pendingDropped > 0 || distributed && !heldFromStart || reader != nil && nativeEvidenceTrimmed(ctx, tr.testStore, test.ID)
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, jointIntervalsFileName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		out := &archiveOutputFile{file: file, budget: budget}
		emit := func(row JointIntervalRecord) error {
			line, err := json.Marshal(row)
			if err != nil {
				return err
			}
			line = append(line, '\n')
			if !budget.admits(len(line)) {
				return errJointIntervalBudget
			}
			if _, err := out.Write(line); err != nil {
				return err
			}
			count++
			return nil
		}
		emitAll := func(rows []JointIntervalRecord) error {
			for _, row := range rows {
				if err := emit(row); err != nil {
					return err
				}
			}
			return nil
		}
		var err error
		seen := map[string]bool{}
		if reader != nil {
			err = reader.ForEachNativeIntervalEvidence(ctx, test.ID, func(timestamp time.Time, evidence *storage.NativeIntervalEvidence) error {
				return emitAll(jointIntervalEvidenceRows(timestamp, evidence, seen))
			})
		}
		if err == nil {
			for _, live := range history {
				if live == nil {
					continue
				}
				rows := jointIntervalEvidenceRows(live.Timestamp, live.NativeEvidence, seen)
				if len(rows) > 0 && reader != nil {
					truncated = true
				}
				if err = emitAll(rows); err != nil {
					break
				}
			}
		}
		for _, p := range pending {
			if err != nil {
				break
			}
			err = emitAll(jointPublicationRows(p, seen))
		}
		if errors.Is(err, errJointIntervalBudget) {
			truncated = true
			err = nil
		}
		return 0, err
	}, ctx)
	if err != nil {
		return err
	}
	// The Parquet copy has its own allowance, the JSONL's plus room for its
	// format overhead: the JSONL may have used all of its own before it was
	// truncated.
	parquetBudget, err := newEvidenceSidecarBudget(ctx, record, envArchiveJointIntervalsMaxBytes, 64<<20)
	if err != nil {
		return err
	}
	defer parquetBudget.close()
	parquetBudget.remaining += jointIntervalsParquetOverhead
	parquetCount, err := streamJSONLToParquet(jsonlPath, parquetPath, toParquetJointInterval, parquetBudget)
	if err != nil {
		return fmt.Errorf("write joint interval parquet: %w", err)
	}
	if parquetCount != count {
		return fmt.Errorf("joint interval parquet has %d rows, want %d", parquetCount, count)
	}
	manifest.JointIntervalsFile, manifest.JointIntervalsParquetFile = jointIntervalsFileName, jointIntervalsParquetFileName
	manifest.JointIntervalCount, manifest.JointIntervalsTruncated = count, truncated
	if err = updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: jointIntervalsFileName, Kind: "evidence", Format: "jsonl", RecordType: jointIntervalRecordType, Count: count}); err != nil {
		return err
	}
	err = updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: jointIntervalsParquetFileName, Kind: "evidence", Format: "parquet", RecordType: jointIntervalRecordType, Count: count})
	return err
}

// nativeEvidenceTrimmed reports whether the run's persisted interval rows may
// have been trimmed (or cannot be checked).
func nativeEvidenceTrimmed(ctx context.Context, store any, testID string) bool {
	retention, ok := store.(storage.NativeIntervalEvidenceRetention)
	if !ok {
		return false
	}
	trimmed, err := retention.NativeIntervalEvidenceTrimmed(ctx, testID)
	return err != nil || trimmed
}
