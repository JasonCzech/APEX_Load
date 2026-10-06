package api

import (
	"context"
	"errors"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

// These are verified artifact selection/staging bytes. They are not DuckDB's
// physical scan counters, memory use, row-group pruning or elapsed query time.
type AnalysisArtifactSelection struct {
	DatasetID            string `json:"dataset_id"`
	DatasetRevision      int64  `json:"dataset_revision"`
	ManifestVersion      int    `json:"manifest_version"`
	ManifestDigest       string `json:"manifest_digest,omitempty"`
	Mode                 string `json:"mode"`
	TotalObjects         int    `json:"total_objects"`
	SelectedObjects      int    `json:"selected_objects"`
	PrunedObjects        int    `json:"pruned_objects"`
	TotalArtifactBytes   int64  `json:"total_artifact_bytes"`
	StagedArtifactBytes  int64  `json:"staged_artifact_bytes"`
	AvoidedArtifactBytes int64  `json:"avoided_artifact_bytes"`
}

type analysisArtifactSelections struct {
	values    []AnalysisArtifactSelection
	snapshots []*storage.AnalysisImport
}
type analysisArtifactSelectionsKey struct{}

func withAnalysisArtifactSelections(ctx context.Context) context.Context {
	return context.WithValue(ctx, analysisArtifactSelectionsKey{}, &analysisArtifactSelections{})
}
func completedAnalysisArtifactSelections(ctx context.Context) []AnalysisArtifactSelection {
	recorder, _ := ctx.Value(analysisArtifactSelectionsKey{}).(*analysisArtifactSelections)
	if recorder == nil {
		return nil
	}
	return append([]AnalysisArtifactSelection(nil), recorder.values...)
}
func completedAnalysisQuerySnapshots(ctx context.Context) []*storage.AnalysisImport {
	recorder, _ := ctx.Value(analysisArtifactSelectionsKey{}).(*analysisArtifactSelections)
	if recorder == nil {
		return nil
	}
	snapshots := make([]*storage.AnalysisImport, len(recorder.snapshots))
	for i, snapshot := range recorder.snapshots {
		copy := *snapshot
		snapshots[i] = &copy
	}
	return snapshots
}

func recordAnalysisArtifactSelection(ctx context.Context, in *storage.AnalysisImport, parts []analysisDatasetQueryPart, bytes int64) error {
	if !analysisQueryContextLive(ctx) {
		return errors.New("analysis deadline or cancellation reached")
	}
	recorder, _ := ctx.Value(analysisArtifactSelectionsKey{}).(*analysisArtifactSelections)
	if recorder == nil {
		return nil
	}
	if len(recorder.values) >= 2 || in == nil || bytes < 0 || len(parts) > len(in.Normalized) {
		return errors.New("analytical selection report exceeds admitted scope")
	}
	value := AnalysisArtifactSelection{DatasetID: in.ID, DatasetRevision: in.Revision, ManifestVersion: in.PartitionManifestVersion, Mode: "legacy_full_scan", TotalObjects: len(in.Normalized), SelectedObjects: len(parts), StagedArtifactBytes: bytes, TotalArtifactBytes: bytes}
	if in.PartitionManifest != nil {
		value.Mode = "producer_manifest"
		value.ManifestDigest = in.PartitionManifest.Digest
		value.TotalArtifactBytes = 0
		for _, p := range in.PartitionManifest.Partitions {
			value.TotalArtifactBytes += p.Bytes
		}
	}
	if bytes > value.TotalArtifactBytes {
		return errors.New("analytical selection byte proof differs")
	}
	value.PrunedObjects = value.TotalObjects - value.SelectedObjects
	value.AvoidedArtifactBytes = value.TotalArtifactBytes - bytes
	recorder.values = append(recorder.values, value)
	// Only fixed authority fields escape; the immutable query snapshot itself
	// has already passed manifest/mapping and selected artifact validation.
	recorder.snapshots = append(recorder.snapshots, &storage.AnalysisImport{ID: in.ID, ProjectID: in.ProjectID, RunID: in.RunID, State: in.State, Revision: in.Revision})
	return nil
}

// Artifact selections are included only after successful native evidence work.
// Final authorization/revision/deadline checks still govern the whole response.
type AnalysisDatasetSeriesResponse struct {
	TimeWindow         *AnalysisTimeWindow         `json:"time_window,omitempty"`
	Points             []analysis.MetricPoint      `json:"points"`
	DatasetID          string                      `json:"dataset_id"`
	RejectedRows       int64                       `json:"rejected_rows"`
	ArtifactSelections []AnalysisArtifactSelection `json:"artifact_selections,omitempty"`
}
type AnalysisComparisonResponse struct {
	TimeWindow         *AnalysisTimeWindow         `json:"time_window,omitempty"`
	Left               []analysis.MetricPoint      `json:"left"`
	Right              []analysis.MetricPoint      `json:"right"`
	Request            AnalysisComparisonRequest   `json:"request"`
	Advisory           bool                        `json:"advisory"`
	Interpretation     string                      `json:"interpretation"`
	Correlation        []analysis.CorrelationScore `json:"correlation,omitempty"`
	ArtifactSelections []AnalysisArtifactSelection `json:"artifact_selections,omitempty"`
}
