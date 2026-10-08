package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Joint evidence v4 archive (B5). After finalize, a run with a joint evidence
// projection gets joint_intervals_v4.jsonl (and .parquet): one row per
// measured tuple of each retained source publication (a generator batch, or
// a local window), one for the samples past the tuple bound, and one per
// publication whose source had no joint evidence. The rows come from the
// controller's retained live history; when that history was trimmed, the
// file is marked truncated.

const (
	jointIntervalsFileName           = "joint_intervals_v4.jsonl"
	jointIntervalsParquetFileName    = "joint_intervals_v4.parquet"
	jointIntervalRecordType          = "joint_intervals_v4"
	jointIntervalSchemaVersion       = "apexload.joint_interval.v4"
	envArchiveJointIntervalsMaxBytes = "APEXLOAD_ARCHIVE_JOINT_INTERVALS_MAX_BYTES"
)

// JointIntervalRecord is one archived joint evidence row.
type JointIntervalRecord struct {
	SchemaVersion       string    `json:"schema_version"`
	Timestamp           time.Time `json:"timestamp"`
	WindowSeconds       float64   `json:"window_seconds"`
	SourceID            string    `json:"source_id"`
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
}

type parquetJointInterval struct {
	SchemaVersion       string  `parquet:"name=schema_version, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TimestampUnixMs     int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	WindowSeconds       float64 `parquet:"name=window_seconds, type=DOUBLE"`
	SourceID            string  `parquet:"name=source_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
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
	Incomplete          bool    `parquet:"name=incomplete, type=BOOLEAN"`
	OmittedTuples       int64   `parquet:"name=omitted_tuples, type=INT64"`
}

func toParquetJointInterval(row JointIntervalRecord) parquetJointInterval {
	values := ""
	if row.Values != nil {
		values = mustJSON(row.Values)
	}
	digest := ""
	if row.DurationDigest != nil {
		digest = mustJSON(row.DurationDigest)
	}
	return parquetJointInterval{SchemaVersion: row.SchemaVersion, TimestampUnixMs: row.Timestamp.UnixMilli(), WindowSeconds: row.WindowSeconds, SourceID: row.SourceID, ExecutionGeneration: int64(row.ExecutionGeneration), Sequence: int64(row.Sequence), Population: row.Population, DimensionsJSON: mustJSON(row.Dimensions), Availability: row.Availability, Reason: row.Reason, ValuesJSON: values, Other: row.Other, Count: row.Count, Errors: row.Errors, AvgMs: row.AvgMs, P50Ms: row.P50Ms, P90Ms: row.P90Ms, P95Ms: row.P95Ms, P99Ms: row.P99Ms, DurationDigestJSON: digest, Incomplete: row.Incomplete, OmittedTuples: row.OmittedTuples}
}

func jointIntervalRow(base JointIntervalRecord, metric storage.NativeIntervalMetric) JointIntervalRecord {
	base.Count, base.Errors, base.AvgMs = metric.Count, metric.Errors, metric.Avg
	base.P50Ms, base.P90Ms, base.P95Ms, base.P99Ms = metric.P50, metric.P90, metric.P95, metric.P99
	base.DurationDigest = engine.CloneTDigestSnapshot(metric.DurationDigest)
	return base
}

// jointIntervalRows turns retained live history into archive rows, once per
// source publication.
func jointIntervalRows(history []*LiveMetrics) []JointIntervalRecord {
	var rows []JointIntervalRecord
	seen := map[string]bool{}
	for _, live := range history {
		if live == nil || live.NativeEvidence == nil {
			continue
		}
		evidence := live.NativeEvidence
		base := JointIntervalRecord{SchemaVersion: jointIntervalSchemaVersion, Timestamp: live.Timestamp.UTC(), WindowSeconds: evidence.WindowSeconds, SourceID: "local"}
		joint, tuples := evidence.Joint, evidence.Dimensions[storage.NativeJointDimension]
		identity := fmt.Sprintf("local/%d", live.Timestamp.UnixNano())
		if p := evidence.Publication; p != nil {
			base.Timestamp, base.WindowSeconds, base.SourceID = p.Timestamp.UTC(), p.WindowSeconds, p.SourceID
			base.ExecutionGeneration, base.Sequence = p.ExecutionGeneration, p.Sequence
			joint, tuples = p.Joint, p.Dimensions[storage.NativeJointDimension]
			identity = fmt.Sprintf("%s/%d/%d", p.SourceID, p.ExecutionGeneration, p.Sequence)
		}
		if joint == nil || seen[identity] {
			continue
		}
		seen[identity] = true
		base.Population, base.Dimensions = joint.Population, append([]string(nil), joint.Dimensions...)
		base.Availability, base.Reason, base.Incomplete, base.OmittedTuples = joint.Availability, joint.Reason, joint.Incomplete, joint.OmittedTuples
		if joint.Availability != "measured" {
			rows = append(rows, base)
			continue
		}
		keys := make([]string, 0, len(tuples))
		for key := range tuples {
			keys = append(keys, key)
		}
		sort.Strings(keys)
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
	}
	return rows
}

// persistJointIntervalArchive writes joint_intervals_v4.jsonl and .parquet
// after finalize for a run with a joint evidence projection.
func (tr *TestRunner) persistJointIntervalArchive(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || record == nil || manifest == nil || manifest.JointIntervalsFile != "" || manifest.Provenance.JointEvidence == nil {
		return nil
	}
	tr.mu.RLock()
	history := append([]*LiveMetrics(nil), test.History...)
	if test.LiveMetrics != nil && (len(history) == 0 || history[len(history)-1] != test.LiveMetrics) {
		history = append(history, test.LiveMetrics)
	}
	trimmed := test.historyTrimmed > 0
	rows := jointIntervalRows(history)
	tr.mu.RUnlock()
	budget, err := newEvidenceSidecarBudget(ctx, record, envArchiveJointIntervalsMaxBytes, 64<<20)
	if err != nil {
		return err
	}
	defer budget.close()
	var count int64
	truncated := trimmed
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, jointIntervalsFileName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		out := &archiveOutputFile{file: file, budget: budget}
		for _, row := range rows {
			line, err := json.Marshal(row)
			if err != nil {
				return 0, err
			}
			line = append(line, '\n')
			if !budget.admits(len(line)) {
				truncated = true
				return 0, nil
			}
			if _, err := out.Write(line); err != nil {
				return 0, err
			}
			count++
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	parquetCount, err := streamJSONLToParquet(filepath.Join(record.ArchivePath, jointIntervalsFileName), filepath.Join(record.ArchivePath, jointIntervalsParquetFileName), toParquetJointInterval, budget)
	if err != nil {
		return fmt.Errorf("write joint interval parquet: %w", err)
	}
	if parquetCount != count {
		return fmt.Errorf("joint interval parquet has %d rows, want %d", parquetCount, count)
	}
	manifest.JointIntervalsFile, manifest.JointIntervalsParquetFile = jointIntervalsFileName, jointIntervalsParquetFileName
	manifest.JointIntervalCount, manifest.JointIntervalsTruncated = count, truncated
	if err := updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: jointIntervalsFileName, Kind: "evidence", Format: "jsonl", RecordType: jointIntervalRecordType, Count: count}); err != nil {
		return err
	}
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: jointIntervalsParquetFileName, Kind: "evidence", Format: "parquet", RecordType: jointIntervalRecordType, Count: count})
}
