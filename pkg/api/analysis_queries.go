package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/jasonczech/apexload/pkg/storage"
)

type AnalysisSeriesSelection struct {
	DatasetID   string            `json:"dataset_id,omitempty"`
	RunID       string            `json:"run_id,omitempty"`
	SourceID    string            `json:"source_id,omitempty"`
	Transaction string            `json:"transaction,omitempty"`
	Protocol    string            `json:"protocol,omitempty"`
	Group       string            `json:"group,omitempty"`
	Dimensions  map[string]string `json:"dimensions,omitempty"`
	Host        string            `json:"host,omitempty"`
	Service     string            `json:"service,omitempty"`
	Metric      string            `json:"metric"`
	Aggregation string            `json:"aggregation"`
}

type AnalysisComparisonRequest struct {
	Left           AnalysisSeriesSelection                             `json:"left"`
	Right          AnalysisSeriesSelection                             `json:"right"`
	From           time.Time                                           `json:"from"`
	To             time.Time                                           `json:"to"`
	BucketSeconds  int                                                 `json:"bucket_seconds"`
	MaxLagSeconds  int                                                 `json:"max_lag_seconds"`
	Mode           string                                              `json:"mode"` // overlay or correlation
	providerSeries map[analysisSeriesSelectionKey]analysis.MetricPoint `json:"-"`
}

type analysisSeriesSelectionKey string

func analysisSelectionKey(selection AnalysisSeriesSelection) analysisSeriesSelectionKey {
	body, _ := json.Marshal(selection)
	return analysisSeriesSelectionKey(body)
}

func (q AnalysisComparisonRequest) validate() error {
	if q.From.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > 366*24*time.Hour || q.BucketSeconds < 1 || q.BucketSeconds > 86400 || q.To.Sub(q.From).Seconds()/float64(q.BucketSeconds) > analysis.MaxSeriesPoints || q.MaxLagSeconds < 0 || q.MaxLagSeconds > 3600 || q.MaxLagSeconds%q.BucketSeconds != 0 || 2*q.MaxLagSeconds/q.BucketSeconds+1 > 121 {
		return errors.New("invalid bounded comparison window or lag")
	}
	if q.Mode != "overlay" && q.Mode != "correlation" {
		return errors.New("choose overlay or correlation")
	}
	for _, s := range []AnalysisSeriesSelection{q.Left, q.Right} {
		if s.DatasetID != "" {
			if err := validateImportedIdentitySelection(s); err != nil {
				return err
			}
		}
		if len(s.Protocol) > 64 || len(s.Group) > 256 || len(s.Host) > 256 || len(s.Service) > 256 || len(s.Dimensions) > 16 {
			return errors.New("protocol or group identity exceeds limit")
		}
		for k, v := range s.Dimensions {
			if k == "" || len(k) > 128 || len(v) > 128 {
				return errors.New("invalid selected dimensions")
			}
		}
		switch s.Aggregation {
		case "avg", "min", "max", "sum", "count", "rate", "p50", "p95", "p99":
		default:
			return errors.New("unsupported aggregation")
		}
		if (s.DatasetID == "") == (s.RunID == "") || s.Metric == "" || len(s.Metric) > 256 || len(s.Transaction) > 256 || s.DatasetID == "" && len(s.SourceID) > 128 || s.DatasetID != "" && len(s.SourceID) > 256 || len(s.RunID) > 128 || len(s.DatasetID) > 128 {
			return errors.New("each series requires a dataset or run and bounded metric identity")
		}
	}
	return nil
}

func (s *Server) handleAnalysisAssociation(w http.ResponseWriter, r *http.Request) {
	store, in := s.loadAnalysisImport(w, r)
	if in == nil {
		return
	}
	if in.State != "ready" {
		writeError(w, 409, "only committed datasets can be associated")
		return
	}
	var req struct {
		RunID    string `json:"run_id"`
		Revision int64  `json:"revision"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Revision != in.Revision {
		writeError(w, 409, "dataset revision conflict")
		return
	}
	if req.RunID != "" {
		run, err := s.store.GetTest(req.RunID)
		if err != nil || run.ProjectID != in.ProjectID {
			writeError(w, 404, "run not found in this project")
			return
		}
	}
	in.RunID = req.RunID
	saved, err := store.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		writeError(w, 409, "dataset association changed concurrently")
		return
	}
	s.recordAuditEvent(r, "analysis.dataset.associate", "success", "analysis_import", in.ID, in.ProjectID, map[string]any{"run_id": req.RunID})
	writeJSON(w, 200, saved)
}

func (s *Server) handleAnalysisComparison(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project_id")
	if !analysisProjectEnabled(project) && !runMonitoringEnabled(project) {
		writeError(w, 409, "analysis is not enabled for this project")
		return
	}
	var q AnalysisComparisonRequest
	if !decodeAnalysisQueryRequest(w, r, &q) {
		return
	}
	if err := q.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if !admitAnalysisPipeline(w) {
		return
	}
	defer func() { <-analysisPipelineJobs }()
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	ctx, pipeline, dir, err := s.prepareNativeAnalysisComparison(ctx, project, q)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	defer closeNativeAnalysisPipeline(pipeline, dir)
	q.providerSeries = map[analysisSeriesSelectionKey]analysis.MetricPoint{}
	left, leftRun, err := s.loadAnalysisSelection(ctx, project, q.Left, q)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	right, rightRun, err := s.loadAnalysisSelection(ctx, project, q.Right, q)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	// An explicit, project-authorized association is required before overlaying
	// external evidence with a managed run. Dataset identities remain separate.
	if q.Left.RunID != "" && q.Right.DatasetID != "" && rightRun != q.Left.RunID || q.Right.RunID != "" && q.Left.DatasetID != "" && leftRun != q.Right.RunID {
		writeError(w, 409, "associate the external dataset with this run before comparison")
		return
	}
	response := map[string]any{"left": left, "right": right, "request": q, "advisory": true, "interpretation": "Correlation is association, not proof of causation."}
	if q.Mode == "overlay" {
		leftProvider, leftNative := q.providerSeries[analysisSelectionKey(q.Left)]
		rightProvider, rightNative := q.providerSeries[analysisSelectionKey(q.Right)]
		if leftNative && rightNative && !compatibleNativeMonitorComparison(leftProvider, rightProvider) {
			writeError(w, 422, "provider metric identity, dimensions, statistic and resolution must match for comparison")
			return
		}
		if q.Left.Aggregation != q.Right.Aggregation || len(left) > 0 && len(right) > 0 && !analysis.CompatibleOverlay(left[0], right[0]) {
			writeError(w, 422, "incompatible units or measurement semantics prevent overlay")
			return
		}
	} else {
		// Normalize only the authorized calculation copies; response evidence
		// retains its original dataset/run/source identities.
		a, b := append([]analysis.MetricPoint(nil), left...), append([]analysis.MetricPoint(nil), right...)
		for _, points := range [][]analysis.MetricPoint{a, b} {
			for i := range points {
				points[i].RunID = ""
				points[i].DatasetID = ""
				points[i].RelativeTimeMS = nil
				points[i].ActiveRuntimeMS = nil
			}
		}
		scores, err := analysis.LaggedCorrelation(a, b, q.From, q.To, q.BucketSeconds, q.MaxLagSeconds)
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		response["correlation"] = scores
	}
	if err := s.verifyNativeAnalysisComparison(ctx, project); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if !requireAuthorizedProjectMatch(w, r, project) {
		return
	}
	if !analysisQueryContextLive(ctx) {
		writeError(w, 409, "comparison deadline or cancellation reached")
		return
	}
	writeJSON(w, 200, response)
}

func compatibleNativeMonitorComparison(a, b analysis.MetricPoint) bool {
	if a.Provider == nil || b.Provider == nil {
		return false
	}
	x, y := a.Provider, b.Provider
	return a.Unit == b.Unit && a.Kind == b.Kind && a.InputKind == b.InputKind && maps.Equal(a.Dimensions, b.Dimensions) && maps.Equal(x.EntityIDs, y.EntityIDs) && x.Name == y.Name && x.Backend == y.Backend && x.MetricID == y.MetricID && x.Statistic == y.Statistic && x.ResolutionSeconds == y.ResolutionSeconds && x.QueryWindow == y.QueryWindow && x.Synthetic == y.Synthetic
}

func (s *Server) loadAnalysisSelection(ctx context.Context, project string, selection AnalysisSeriesSelection, q AnalysisComparisonRequest) ([]analysis.MetricPoint, string, error) {
	if err := ensureAnalysisBackend(s.store); err != nil {
		return nil, "", err
	}
	if selection.DatasetID != "" {
		if shared, _ := ctx.Value(analysisComparisonPipelineKey{}).(*analysisComparisonPipeline); shared != nil {
			plan, exists := shared.plans[analysisDatasetQueryIdentity{selection.DatasetID, selection.Metric}]
			if !exists || plan.dataset.ProjectID != project {
				return nil, "", errors.New("dataset selection is outside comparison plan")
			}
			nativeQuery, err := importedNativeSeriesQuery(project, plan.dataset, selection, q)
			if err != nil {
				return nil, "", err
			}
			points, err := queryAnalysisDataset(ctx, plan.dataset, nativeQuery)
			return points, plan.dataset.RunID, err
		}
		store, ok := s.store.(storage.AnalysisImportStore)
		if !ok {
			return nil, "", errors.New("dataset storage unavailable")
		}
		in, err := store.GetAnalysisImport(selection.DatasetID)
		if err != nil || in.ProjectID != project || in.State != "ready" {
			return nil, "", errors.New("committed dataset not found in project")
		}
		if !analysisProjectEnabled(project) {
			return nil, "", errors.New("native analysis disabled for project")
		}
		nativeQuery, err := importedNativeSeriesQuery(project, in, selection, q)
		if err != nil {
			return nil, "", err
		}
		points, err := queryAnalysisDataset(ctx, in, nativeQuery)
		return points, in.RunID, err
	}
	run, err := s.store.GetTest(selection.RunID)
	if err != nil || run.ProjectID != project {
		return nil, "", errors.New("run not found in project")
	}
	points := []analysis.MetricPoint{}
	exactPrometheusWindow := false
	appendPoint := func(p analysis.MetricPoint) error {
		if p.Provider != nil && p.Provider.Name == "prometheus" && p.Provider.Mode == "query_window" {
			exactPrometheusWindow = true
			return nil
		}
		if p.TimestampUTC.Before(q.From) || !p.TimestampUTC.Before(q.To) {
			return nil
		}
		if len(points) >= analysis.MaxSeriesPoints {
			return errors.New("selected raw evidence exceeds 10000 samples; narrow the time window")
		}
		points = append(points, p)
		return nil
	}
	if selection.SourceID != "" {
		store, ok := s.store.(storage.RunMonitorStore)
		if !ok {
			return nil, "", errors.New("monitor storage unavailable")
		}
		job, jobErr := store.GetRunMonitorJob(project, selection.RunID+":"+selection.SourceID)
		if jobErr == nil && job.Source.CanonicalProvider() {
			after := ""
			for n := 0; n < 100; n++ {
				page, err := loadCanonicalMonitorData(ctx, store, project, selection.RunID, selection.SourceID, "point", after, 999)
				if err != nil {
					return nil, "", err
				}
				for _, p := range page.Points {
					if p.MetricName == selection.Metric && matchesAnalysisDimensions(p, selection) {
						p.RelativeTimeMS = nil
						if err := appendPoint(p); err != nil {
							return nil, "", err
						}
					}
				}
				if !page.HasMore {
					if len(points) == 0 && exactPrometheusWindow {
						return nil, "", errors.New("exact-window Prometheus results cannot be rebucketed into intervals; select evaluation timeline evidence")
					}
					if q.providerSeries != nil && len(points) > 0 {
						q.providerSeries[analysisSelectionKey(selection)] = points[0]
					}
					result, err := analysis.BucketSeries(points, q.From, q.To, q.BucketSeconds, selection.Aggregation)
					return result, selection.RunID, err
				}
				after = page.AfterKey
			}
			return nil, "", errors.New("monitor scan exceeds canonical budget")
		}
		afterJob, afterSequence := "", int64(-1)
		for page := 0; ; page++ {
			if err := ctx.Err(); err != nil {
				return nil, "", errors.New("analysis query deadline exceeded")
			}
			if page >= 100 {
				return nil, "", errors.New("monitor scan exceeds 10000 batches; use the archived monitor evidence")
			}
			receipts, err := store.ListMonitorBatchReceipts(project, selection.RunID, afterJob, afterSequence, 100)
			if err != nil {
				return nil, "", errors.New("monitor evidence unavailable")
			}
			if len(receipts) == 0 {
				break
			}
			for _, receipt := range receipts {
				evidence, err := readMonitorEvidence(ctx, project, selection.RunID, receipt)
				if err != nil {
					return nil, "", errors.New("monitor evidence unavailable or corrupt")
				}
				for _, p := range evidence.Points {
					if p.Source == selection.SourceID && p.MetricName == selection.Metric && matchesAnalysisDimensions(p, selection) {
						p.ReceivedAt = receipt.ReceivedAt
						p.Quality = monitorPointQuality(p, receipt.ReceivedAt)
						p.RelativeTimeMS = nil
						if err := appendPoint(p); err != nil {
							return nil, "", err
						}
					}
				}
				afterJob, afterSequence = receipt.JobID, receipt.Sequence
			}
		}
	} else {
		if selection.Metric != "transaction.duration" || selection.Transaction == "" {
			return nil, "", errors.New("select a monitor source or a named transaction.duration measurement")
		}
		record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, selection.RunID)
		if !ok {
			return nil, "", errors.New("transaction analysis requires a completed run archive")
		}
		err := forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
			if tx.ActionName != selection.Transaction || !tx.Success || selection.Protocol != "" && selection.Protocol != tx.DependencyType || selection.Group != "" && selection.Group != tx.Group {
				return nil
			}
			value := tx.DurationMs
			point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: project, RunID: selection.RunID, Source: "transaction:" + sha256String(selection.Transaction), TimestampUTC: tx.Timestamp.UTC(), MetricName: selection.Metric, Kind: analysis.KindObservation, Value: &value, Unit: "ms", Quality: analysis.QualityValid, Dimensions: map[string]string{"protocol": analysisDimensionIdentity(tx.DependencyType), "group": analysisDimensionIdentity(tx.Group), "population": archiveEventPopulation(*tx)}}
			// Apply the same exact selectors as monitor evidence. In particular,
			// an archive without host/service evidence cannot satisfy a requested
			// host/service, and a population filter must not change cohorts.
			if !matchesAnalysisDimensions(point, selection) {
				return nil
			}
			return appendPoint(point)
		})
		if err != nil {
			return nil, "", errors.New("transaction evidence unavailable or exceeds bounded query limits")
		}
	}
	points, err = analysis.BucketSeries(points, q.From, q.To, q.BucketSeconds, selection.Aggregation)
	return points, selection.RunID, err
}

func matchesAnalysisDimensions(p analysis.MetricPoint, selection AnalysisSeriesSelection) bool {
	if selection.Host != "" && p.Host != selection.Host || selection.Service != "" && p.Service != selection.Service {
		return false
	}
	for k, v := range selection.Dimensions {
		if actual, ok := p.Dimensions[k]; !ok || actual != v {
			return false
		}
	}
	return true
}

func analysisDimensionIdentity(value string) string {
	if len(value) <= 128 {
		return value
	}
	return "sha256:" + sha256String(value)
}

// Staging includes an aggregate disk bound; per-file limits alone would permit
// a large partition set to consume all controller disk before native admission.
type analysisStagingWriter struct {
	out        io.Writer
	dir        string
	remaining  *int64
	untilCheck int64
}

func (w *analysisStagingWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > *w.remaining {
		return 0, errors.New("analysis staging quota exceeded")
	}
	if w.untilCheck <= 0 {
		free, err := diskspace.AvailablePath(w.dir)
		if err != nil || free < 512<<20 {
			return 0, errors.New("analysis cancelled to preserve disk headroom")
		}
		w.untilCheck = 8 << 20
	}
	n, err := w.out.Write(p)
	*w.remaining -= int64(n)
	w.untilCheck -= int64(n)
	return n, err
}

func queryAnalysisDataset(ctx context.Context, in *storage.AnalysisImport, query analytics.SeriesQuery) ([]analysis.MetricPoint, error) {
	var queryErr error
	query, queryErr = bindImportedNativeSeriesQuery(in, query)
	if queryErr != nil {
		return nil, queryErr
	}
	var pipeline *analytics.NativePipeline
	var dir string
	var selected []analysisDatasetQueryPart
	var total int64
	if shared, _ := ctx.Value(analysisComparisonPipelineKey{}).(*analysisComparisonPipeline); shared != nil {
		plan, exists := shared.plans[analysisDatasetQueryIdentity{in.ID, query.Metric}]
		if !exists || plan.dataset.ProjectID != in.ProjectID || plan.dataset.Revision != in.Revision {
			return nil, errors.New("dataset query is outside reserved comparison")
		}
		in, selected, total, pipeline = plan.dataset, plan.parts, plan.bytes, shared.pipeline
		if len(selected) == 0 {
			return []analysis.MetricPoint{}, nil
		}
		var err error
		dir, err = pipeline.CreateStagingDirectory(shared.root)
		if err != nil {
			return nil, errors.New("comparison staging ownership unavailable")
		}
		// The enclosing comparison owns this directory, all entered IO, both
		// sequential children and final cleanup. No per-side Release is permitted.
	} else {
		var err error
		in, err = snapshotAnalysisQueryDataset(in)
		if err != nil {
			return nil, err
		}
		ctx, pipeline, dir, err = beginNativeAnalysisPipeline(ctx, 12, 12)
		if err != nil {
			return nil, errors.New("analytical admission unavailable")
		}
		defer closeNativeAnalysisPipeline(pipeline, dir)
		selected, total, err = selectAnalysisDatasetParts(ctx, in, query.Metric)
		if err != nil {
			return nil, err
		}
		if len(selected) == 0 {
			return []analysis.MetricPoint{}, nil
		}
		if err = pipeline.AdmitGeometry(filepath.Dir(dir), total, 12, total); err != nil {
			return nil, errors.New("analytical geometry admission unavailable")
		}
	}
	files := []string{}
	remaining := total
	for _, part := range selected {
		ioLease, err := pipeline.AcquireIO()
		if err != nil {
			return nil, err
		}
		file := filepath.Join(dir, fmt.Sprintf("part-%d.parquet", part.index))
		out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			ioLease.Joined()
			return nil, errors.New("cannot stage dataset")
		}
		ioLease.TrackOwner(out)
		digest, readErr := analysisObjectRead(ctx, part.key, &analysisStagingWriter{out: out, dir: dir, remaining: &remaining}, part.size)
		syncErr, closeErr := out.Sync(), ioLease.CloseOwner(out)
		ioLease.Joined()
		if errors.Join(readErr, syncErr, closeErr) != nil || in.NormalizedSHA256[part.key] == "" || digest != in.NormalizedSHA256[part.key] {
			return nil, errors.New("dataset artifact unavailable, corrupt, or exceeds staging quota")
		}
		files = append(files, file)
	}
	engine, err := analytics.NewForNativePipeline(ctx, dir)
	if err != nil {
		return nil, err
	}
	points, err := engine.Series(ctx, files, query)
	if err != nil {
		return nil, errors.New("analytical query failed: verify measurement semantics, time bounds, and resource limits")
	}
	return points, nil
}
