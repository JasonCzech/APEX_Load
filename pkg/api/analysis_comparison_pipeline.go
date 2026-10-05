package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

type analysisDatasetQueryIdentity struct{ dataset, metric string }
type analysisDatasetQueryPart struct {
	index int
	key   string
	size  int64
}
type analysisDatasetQueryPlan struct {
	dataset *storage.AnalysisImport
	parts   []analysisDatasetQueryPart
	bytes   int64
}
type analysisComparisonPipeline struct {
	pipeline *analytics.NativePipeline
	root     string
	plans    map[analysisDatasetQueryIdentity]analysisDatasetQueryPlan
}
type analysisComparisonPipelineKey struct{}

// Sequential children share one maximum child geometry. Both private staging
// copies remain owned until the enclosing comparison ends, so their sum is
// charged even when both selections refer to the same dataset/metric.
func analysisComparisonGeometry(sides []int64) (input, staging int64, err error) {
	if len(sides) < 1 || len(sides) > 2 {
		return 0, 0, errors.New("invalid comparison side population")
	}
	input = 12
	for _, size := range sides {
		if size < 0 || size != 0 && size < 12 || size > analysisImportMaxBytes-staging {
			return 0, 0, errors.New("comparison staging exceeds analytical quota")
		}
		input = max(input, size)
		staging += size
	}
	return input, staging, nil
}

func analysisDatasetArtifactDigestValid(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, value := range digest {
		if value < '0' || value > '9' && value < 'a' || value > 'f' {
			return false
		}
	}
	return true
}

// Snapshot only the bounded immutable analytical fields needed by this query.
// Unrelated mutable import metadata and nested mapping slices do not escape.
func snapshotAnalysisQueryDataset(in *storage.AnalysisImport) (*storage.AnalysisImport, error) {
	if in == nil || len(in.Normalized) > 1024 || len(in.NormalizedSHA256) > 1024 || in.Mapping != nil && len(in.Mapping.Measures) > 64 {
		return nil, errors.New("dataset exceeds bounded analytical metadata")
	}
	out := &storage.AnalysisImport{ID: in.ID, ProjectID: in.ProjectID, RunID: in.RunID, State: in.State, Revision: in.Revision, Normalized: append([]string{}, in.Normalized...), NormalizedSHA256: map[string]string{}}
	for _, key := range in.Normalized {
		if !strings.HasPrefix(key, analysisObjectKey(in.ProjectID, in.ID, "")) || !analysisObjectPattern.MatchString(key) || !analysisDatasetArtifactDigestValid(in.NormalizedSHA256[key]) {
			return nil, errors.New("dataset artifact ownership or digest mismatch")
		}
		out.NormalizedSHA256[key] = in.NormalizedSHA256[key]
	}
	if in.Mapping != nil {
		out.Mapping = &analysis.ParquetMapping{Measures: make([]analysis.ParquetMeasure, len(in.Mapping.Measures))}
		for i, measure := range in.Mapping.Measures {
			out.Mapping.Measures[i].Name = measure.Name
		}
	}
	return out, nil
}

func selectAnalysisDatasetParts(ctx context.Context, in *storage.AnalysisImport, metric string) ([]analysisDatasetQueryPart, int64, error) {
	parts := []analysisDatasetQueryPart{}
	var total int64
	for i, key := range in.Normalized {
		if in.Mapping != nil && len(in.Mapping.Measures) > 0 {
			measure := in.Mapping.Measures[i%len(in.Mapping.Measures)]
			if measure.Name != "" && measure.Name != metric {
				continue
			}
		}
		size, err := analysisObjectSize(ctx, key, analysisImportMaxBytes-total)
		if err != nil || size < 12 {
			return nil, 0, errors.New("dataset artifact size unavailable or invalid")
		}
		total += size
		parts = append(parts, analysisDatasetQueryPart{i, key, size})
	}
	return parts, total, nil
}

func (s *Server) prepareNativeAnalysisComparison(ctx context.Context, project string, q AnalysisComparisonRequest) (context.Context, *analytics.NativePipeline, string, error) {
	if q.Left.DatasetID == "" && q.Right.DatasetID == "" {
		return ctx, nil, "", nil
	}
	imports, ok := s.store.(storage.AnalysisImportStore)
	if !ok || !analysisProjectEnabled(project) {
		return ctx, nil, "", errors.New("native dataset analysis unavailable for project")
	}
	plans := map[analysisDatasetQueryIdentity]analysisDatasetQueryPlan{}
	for _, selection := range []AnalysisSeriesSelection{q.Left, q.Right} {
		if selection.DatasetID == "" {
			continue
		}
		identity := analysisDatasetQueryIdentity{selection.DatasetID, selection.Metric}
		if _, exists := plans[identity]; exists {
			continue
		}
		in, err := imports.GetAnalysisImport(selection.DatasetID)
		if err != nil || in == nil || in.ProjectID != project || in.State != "ready" {
			return ctx, nil, "", errors.New("committed dataset not found in project")
		}
		snapshot, err := snapshotAnalysisQueryDataset(in)
		if err != nil {
			return ctx, nil, "", err
		}
		plans[identity] = analysisDatasetQueryPlan{dataset: snapshot}
	}
	ctx, pipeline, dir, err := beginNativeAnalysisPipeline(ctx, 12, 12)
	if err != nil {
		return ctx, nil, "", errors.New("analytical admission unavailable")
	}
	fail := func(err error) (context.Context, *analytics.NativePipeline, string, error) {
		_ = pipeline.Release() // Exact retained continuation owns any unjoined IO.
		return ctx, nil, "", err
	}
	for identity, plan := range plans {
		plan.parts, plan.bytes, err = selectAnalysisDatasetParts(ctx, plan.dataset, identity.metric)
		if err != nil {
			return fail(err)
		}
		plans[identity] = plan
	}
	sizes := []int64{}
	for _, selection := range []AnalysisSeriesSelection{q.Left, q.Right} {
		if selection.DatasetID != "" {
			sizes = append(sizes, plans[analysisDatasetQueryIdentity{selection.DatasetID, selection.Metric}].bytes)
		}
	}
	input, staging, err := analysisComparisonGeometry(sizes)
	if err != nil {
		return fail(err)
	}
	if err := pipeline.AdmitGeometry(filepath.Dir(dir), input, 12, staging); err != nil {
		return fail(errors.New("comparison analytical geometry admission unavailable"))
	}
	shared := &analysisComparisonPipeline{pipeline: pipeline, root: filepath.Dir(dir), plans: plans}
	return context.WithValue(ctx, analysisComparisonPipelineKey{}, shared), pipeline, dir, nil
}

// A retained private plan cannot authorize serving a deleted dataset/project or
// a different association revision after long native work completes.
func (s *Server) verifyNativeAnalysisComparison(ctx context.Context, project string) error {
	if !analysisQueryContextLive(ctx) {
		return errors.New("comparison deadline or cancellation reached")
	}
	shared, _ := ctx.Value(analysisComparisonPipelineKey{}).(*analysisComparisonPipeline)
	if shared == nil {
		return nil
	}
	snapshots := make([]*storage.AnalysisImport, 0, len(shared.plans))
	if len(shared.plans) > 2 {
		return errors.New("comparison plan exceeds admitted population")
	}
	for _, plan := range shared.plans {
		snapshots = append(snapshots, plan.dataset)
	}
	return s.verifyNativeDatasetQuery(ctx, project, snapshots...)
}
