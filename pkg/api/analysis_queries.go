package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	Metric      string            `json:"metric"`
	Aggregation string            `json:"aggregation"`
}

type AnalysisComparisonRequest struct {
	Left          AnalysisSeriesSelection `json:"left"`
	Right         AnalysisSeriesSelection `json:"right"`
	From          time.Time               `json:"from"`
	To            time.Time               `json:"to"`
	BucketSeconds int                     `json:"bucket_seconds"`
	MaxLagSeconds int                     `json:"max_lag_seconds"`
	Mode          string                  `json:"mode"` // overlay or correlation
}

func (q AnalysisComparisonRequest) validate() error {
	if q.From.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > 366*24*time.Hour || q.BucketSeconds < 1 || q.BucketSeconds > 86400 || q.To.Sub(q.From).Seconds()/float64(q.BucketSeconds) > analysis.MaxSeriesPoints || q.MaxLagSeconds < 0 || q.MaxLagSeconds > 3600 || q.MaxLagSeconds%q.BucketSeconds != 0 || 2*q.MaxLagSeconds/q.BucketSeconds+1 > 121 {
		return errors.New("invalid bounded comparison window or lag")
	}
	if q.Mode != "overlay" && q.Mode != "correlation" {
		return errors.New("choose overlay or correlation")
	}
	for _, s := range []AnalysisSeriesSelection{q.Left, q.Right} {
		if len(s.Protocol) > 64 || len(s.Group) > 256 || len(s.Host) > 256 || len(s.Dimensions) > 16 {
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
		if (s.DatasetID == "") == (s.RunID == "") || s.Metric == "" || len(s.Metric) > 256 || len(s.Transaction) > 256 || len(s.SourceID) > 128 || len(s.RunID) > 128 || len(s.DatasetID) > 128 {
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
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var q AnalysisComparisonRequest
	if !decodeJSONBody(w, r, &q) {
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
		if q.Left.Aggregation != q.Right.Aggregation || len(left) > 0 && len(right) > 0 && (left[0].Unit == "" || right[0].Unit == "" || left[0].Unit != right[0].Unit || left[0].Kind != right[0].Kind || left[0].InputKind != right[0].InputKind) {
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
	writeJSON(w, 200, response)
}

func (s *Server) loadAnalysisSelection(ctx context.Context, project string, selection AnalysisSeriesSelection, q AnalysisComparisonRequest) ([]analysis.MetricPoint, string, error) {
	if err := ensureAnalysisBackend(s.store); err != nil {
		return nil, "", err
	}
	if selection.DatasetID != "" {
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
		points, err := queryAnalysisDataset(ctx, in, analytics.SeriesQuery{ProjectID: project, DatasetID: in.ID, Metric: selection.Metric, From: q.From, To: q.To, BucketSeconds: q.BucketSeconds, Aggregation: selection.Aggregation})
		return points, in.RunID, err
	}
	run, err := s.store.GetTest(selection.RunID)
	if err != nil || run.ProjectID != project {
		return nil, "", errors.New("run not found in project")
	}
	points := []analysis.MetricPoint{}
	appendPoint := func(p analysis.MetricPoint) error {
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
			return appendPoint(analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: project, RunID: selection.RunID, Source: "transaction:" + sha256String(selection.Transaction), TimestampUTC: tx.Timestamp.UTC(), MetricName: selection.Metric, Kind: analysis.KindObservation, Value: &value, Unit: "ms", Quality: analysis.QualityValid, Dimensions: map[string]string{"protocol": analysisDimensionIdentity(tx.DependencyType), "group": analysisDimensionIdentity(tx.Group), "population": archiveEventPopulation(*tx)}})
		})
		if err != nil {
			return nil, "", errors.New("transaction evidence unavailable or exceeds bounded query limits")
		}
	}
	points, err = analysis.BucketSeries(points, q.From, q.To, q.BucketSeconds, selection.Aggregation)
	return points, selection.RunID, err
}

func matchesAnalysisDimensions(p analysis.MetricPoint, selection AnalysisSeriesSelection) bool {
	if selection.Host != "" && p.Host != selection.Host {
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
	if err := query.Validate(); err != nil {
		return nil, err
	}
	dir, err := analysisScratch()
	if err != nil {
		return nil, errors.New("analysis staging unavailable")
	}
	defer os.RemoveAll(dir)
	files := []string{}
	remaining := analysisImportMaxBytes
	for i, key := range in.Normalized {
		// Normalization creates one partition per input file and measure. Wide
		// mappings permit physical projection before remote materialization.
		if in.Mapping != nil && len(in.Mapping.Measures) > 0 {
			m := in.Mapping.Measures[i%len(in.Mapping.Measures)]
			if m.Name != "" && m.Name != query.Metric {
				continue
			}
		}
		if !strings.HasPrefix(key, analysisObjectKey(in.ProjectID, in.ID, "")) {
			return nil, errors.New("dataset artifact ownership mismatch")
		}
		file := filepath.Join(dir, fmt.Sprintf("part-%d.parquet", i))
		out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, errors.New("cannot stage dataset")
		}
		digest, readErr := analysisObjectRead(ctx, key, &analysisStagingWriter{out: out, dir: dir, remaining: &remaining}, remaining)
		closeErr := out.Close()
		if errors.Join(readErr, closeErr) != nil || in.NormalizedSHA256[key] == "" || digest != in.NormalizedSHA256[key] {
			return nil, errors.New("dataset artifact unavailable, corrupt, or exceeds staging quota")
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return []analysis.MetricPoint{}, nil
	}
	engine, err := analytics.New(dir)
	if err != nil {
		return nil, err
	}
	points, err := engine.Series(ctx, files, query)
	if err != nil {
		return nil, errors.New("analytical query failed: verify measurement semantics, time bounds, and resource limits")
	}
	return points, nil
}
