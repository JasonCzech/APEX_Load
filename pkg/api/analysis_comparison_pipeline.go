package api

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

type analysisDatasetQueryIdentity struct{ dataset, metric, signature string }
type analysisDatasetQueryPart struct {
	index int
	key   string
	size  int64
}
type analysisDatasetQueryPlan struct {
	dataset *storage.AnalysisImport
	parts   []analysisDatasetQueryPart
	bytes   int64
	query   analytics.SeriesQuery
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
	// Refuse protected persisted paths before the bounded projection omits the
	// original projected paths and before any shared heavy/native admission.
	if in.Mapping != nil {
		if err := in.Mapping.ValidateDisclosure(); err != nil {
			return nil, err
		}
	}
	if _, err := analysis.MarshalBoundedAnalysisJSON(in, 2<<20); err != nil {
		return nil, errors.New("dataset exceeds bounded analytical metadata")
	}
	if err := validateAnalysisQueryManifest(in); err != nil {
		return nil, err
	}
	out := &storage.AnalysisImport{ID: in.ID, ProjectID: in.ProjectID, RunID: in.RunID, State: in.State, Revision: in.Revision, Accepted: in.Accepted, PartitionManifestVersion: in.PartitionManifestVersion, PartitionManifest: in.PartitionManifest.Clone(), Normalized: append([]string{}, in.Normalized...), NormalizedSHA256: map[string]string{}}
	for _, key := range in.Normalized {
		if !strings.HasPrefix(key, analysisObjectKey(in.ProjectID, in.ID, "")) || !analysisObjectPattern.MatchString(key) || !analysisDatasetArtifactDigestValid(in.NormalizedSHA256[key]) {
			return nil, errors.New("dataset artifact ownership or digest mismatch")
		}
		out.NormalizedSHA256[key] = in.NormalizedSHA256[key]
	}
	if in.Mapping != nil {
		if in.Mapping.Version != 1 && in.Mapping.Version != 2 {
			return nil, errors.New("dataset mapping identity version unavailable")
		}
		out.Mapping = &analysis.ParquetMapping{Version: in.Mapping.Version, Measures: make([]analysis.ParquetMeasure, len(in.Mapping.Measures))}
		for i, measure := range in.Mapping.Measures {
			out.Mapping.Measures[i].Name = measure.Name
		}
		if in.PartitionManifest != nil {
			// Manifest validation binds the complete immutable mapping. Keep a
			// private bounded clone so a second snapshot cannot lose that proof.
			body, err := analysis.MarshalBoundedAnalysisJSON(in.Mapping, 128<<10)
			if err != nil {
				return nil, err
			}
			if err = json.Unmarshal(body, out.Mapping); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func validateAnalysisQueryManifest(in *storage.AnalysisImport) error {
	if in == nil {
		return analysis.ErrPartitionManifest
	}
	if in.PartitionManifestVersion == 0 && in.PartitionManifest == nil {
		return nil
	}
	if in.PartitionManifestVersion != analysis.PartitionManifestVersion || in.PartitionManifest == nil || in.Mapping == nil {
		return analysis.ErrPartitionManifest
	}
	mappingHash, err := analysis.PartitionMappingDigest(*in.Mapping)
	if err != nil {
		return analysis.ErrPartitionManifest
	}
	return in.PartitionManifest.MatchesArtifacts(in.ProjectID, in.ID, mappingHash, in.Mapping.Version, in.Normalized, in.NormalizedSHA256, in.Accepted)
}

// The complete effective native query controls both selection and admission.
// Equivalent UTC instants and nil/empty filter maps have the same key; different
// identities, windows, aggregations, revisions or manifests never share a plan.
func analysisNativeQueryIdentity(in *storage.AnalysisImport, query analytics.SeriesQuery) (analysisDatasetQueryIdentity, error) {
	query, err := bindImportedNativeSeriesQuery(in, query)
	if err != nil {
		return analysisDatasetQueryIdentity{}, err
	}
	query.From, query.To = query.From.UTC(), query.To.UTC()
	if len(query.Dimensions) == 0 {
		query.Dimensions = nil
	}
	manifestDigest := ""
	if in.PartitionManifest != nil {
		manifestDigest = in.PartitionManifest.Digest
	}
	body, err := json.Marshal(struct {
		Query          analytics.SeriesQuery
		Revision       int64
		ManifestDigest string
		CatalogVersion string
	}{query, in.Revision, manifestDigest, analysis.CatalogVersion})
	if err != nil {
		return analysisDatasetQueryIdentity{}, err
	}
	return analysisDatasetQueryIdentity{dataset: in.ID, metric: query.Metric, signature: string(body)}, nil
}

func selectAnalysisDatasetParts(ctx context.Context, in *storage.AnalysisImport, query analytics.SeriesQuery) ([]analysisDatasetQueryPart, int64, error) {
	if !analysisQueryContextLive(ctx) {
		return nil, 0, errors.New("analysis deadline or cancellation reached")
	}
	var bindErr error
	query, bindErr = bindImportedNativeSeriesQuery(in, query)
	if bindErr != nil {
		return nil, 0, bindErr
	}
	if err := validateAnalysisQueryManifest(in); err != nil {
		return nil, 0, err
	}
	parts := []analysisDatasetQueryPart{}
	var total int64
	if in.PartitionManifest != nil {
		indices, err := in.PartitionManifest.Select(analysis.PartitionSelection{Metric: query.Metric, IdentityVersion: query.IdentitySchemaVersion, Source: query.SourceID, Host: query.Host, Service: query.Service, Dimensions: query.Dimensions, From: query.From, To: query.To})
		if err != nil {
			return nil, 0, err
		}
		for _, index := range indices {
			p := in.PartitionManifest.Partitions[index]
			if p.Bytes > analysisImportMaxBytes-total {
				return nil, 0, errors.New("dataset staging exceeds analytical quota")
			}
			total += p.Bytes
			parts = append(parts, analysisDatasetQueryPart{index: index, key: p.ObjectKey, size: p.Bytes})
		}
		return parts, total, nil
	}
	// Old populations carry no producer proof: neither filename nor measure
	// ordinal establishes membership. Scan every legacy object conservatively.
	for i, key := range in.Normalized {
		size, err := analysisObjectSize(ctx, key, analysisImportMaxBytes-total)
		if err != nil || size < 12 {
			return nil, 0, errors.New("dataset artifact size unavailable or invalid")
		}
		total += size
		parts = append(parts, analysisDatasetQueryPart{index: i, key: key, size: size})
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
	identities := []analysisDatasetQueryIdentity{}
	snapshots := map[string]*storage.AnalysisImport{}
	for _, selection := range []AnalysisSeriesSelection{q.Left, q.Right} {
		if selection.DatasetID == "" {
			continue
		}
		snapshot := snapshots[selection.DatasetID]
		if snapshot == nil {
			in, err := imports.GetAnalysisImport(selection.DatasetID)
			if err != nil || in == nil || in.ProjectID != project || in.State != "ready" {
				return ctx, nil, "", errors.New("committed dataset not found in project")
			}
			snapshot, err = snapshotAnalysisQueryDataset(in)
			if err != nil {
				return ctx, nil, "", err
			}
			snapshots[selection.DatasetID] = snapshot
		}
		query, err := importedNativeSeriesQuery(project, snapshot, selection, q)
		if err != nil {
			return ctx, nil, "", err
		}
		identity, err := analysisNativeQueryIdentity(snapshot, query)
		if err != nil {
			return ctx, nil, "", err
		}
		identities = append(identities, identity)
		if _, exists := plans[identity]; !exists {
			plans[identity] = analysisDatasetQueryPlan{dataset: snapshot, query: query}
		}
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
		plan.parts, plan.bytes, err = selectAnalysisDatasetParts(ctx, plan.dataset, plan.query)
		if err != nil {
			return fail(err)
		}
		plans[identity] = plan
	}
	sizes := []int64{}
	for _, identity := range identities {
		sizes = append(sizes, plans[identity].bytes)
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
		if plan.dataset == nil {
			return errors.New("comparison plan lacks dataset")
		}
		snapshots = append(snapshots, plan.dataset)
	}
	return s.verifyNativeDatasetQuery(ctx, project, snapshots...)
}
