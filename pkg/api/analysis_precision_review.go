package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

const analysisPrecisionConsentVersion = 1

type AnalysisPrecisionReview struct {
	Version               int                              `json:"version"`
	ImportID              string                           `json:"import_id"`
	Revision              int64                            `json:"revision"`
	Review                *analysis.ParquetPrecisionReview `json:"review"`
	AcknowledgementSHA256 string                           `json:"acknowledgement_sha256"`
}

// This binds the explicit acknowledgement to one immutable preview snapshot.
// It is not a signature or authorization token. The worker independently derives
// the review from verified immutable sources before it writes any output.
func analysisPrecisionAcknowledgement(in *storage.AnalysisImport, mapping analysis.ParquetMapping, reviewSHA string) (string, error) {
	if in == nil {
		return "", errors.New("invalid precision review identity")
	}
	return analysis.ParquetPrecisionAcknowledgement(in.ProjectID, in.ID, in.Revision, mapping, reviewSHA)
}

func (s *Server) handleAnalysisPrecisionReview(w http.ResponseWriter, r *http.Request) {
	if !admitAnalysisPipeline(w) {
		return
	}
	defer func() { <-analysisPipelineJobs }()
	_, in := s.loadAnalysisImport(w, r)
	if in == nil {
		return
	}
	if in.State != "uploading" && in.State != "mapped" && in.State != "failed" {
		writeError(w, 409, "precision review requires an editable import")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	var req struct {
		Revision int64           `json:"revision"`
		Mapping  json.RawMessage `json:"mapping"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, 413, "precision review exceeds request byte limit")
		} else {
			writeError(w, 400, "invalid precision review request")
		}
		return
	}
	if err = decodeAnalysisDefinitionJSON(body, &req); err != nil {
		writeError(w, 400, "invalid precision review request")
		return
	}
	mapping, err := analysis.DecodeParquetMappingDefinition(req.Mapping)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Revision != in.Revision {
		writeError(w, 409, "precision review revision conflict")
		return
	}
	if len(in.Files) < 1 || len(in.Files) > 1024 || len(in.Files)*len(mapping.Measures) > 1024 {
		writeError(w, 422, "precision review exceeds source or measure limits")
		return
	}
	var total int64
	for _, f := range in.Files {
		if f.Uploaded != f.Size || f.Size < 12 || f.Size > analysisImportMaxBytes-total {
			writeError(w, 409, "precision review requires complete bounded uploads")
			return
		}
		total += f.Size
	}
	ctx, pipeline, dir, err := beginNativeAnalysisPipeline(r.Context(), total, 12)
	if err != nil {
		writeError(w, 503, "analysis staging unavailable")
		return
	}
	defer closeNativeAnalysisPipeline(pipeline, dir)
	engine, err := analytics.NewForNativePipeline(ctx, dir)
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	reviewer, err := analysis.NewParquetPrecisionReviewer(mapping, len(in.Files))
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	for i, f := range in.Files {
		path, digest, err := materializeAnalysisOriginal(ctx, in, f, dir)
		if err != nil {
			writeError(w, 409, "precision review input unavailable")
			return
		}
		preview, err := engine.Preview(ctx, path)
		if err != nil {
			writeError(w, 422, fmt.Sprintf("cannot inspect precision metadata for file %d", i))
			return
		}
		if err := reviewer.AddSource(analysis.ParquetPrecisionInput{SHA256: digest, Schema: preview.Schema}); err != nil {
			writeError(w, 422, err.Error())
			return
		}
	}
	review, err := reviewer.Review()
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	acknowledgement, err := analysisPrecisionAcknowledgement(in, mapping, review.SHA256)
	if err != nil {
		writeError(w, 422, "invalid precision review identity")
		return
	}
	s.writeNativeAnalysisPrecisionReview(w, r, in, AnalysisPrecisionReview{Version: 1, ImportID: in.ID, Revision: in.Revision, Review: review, AcknowledgementSHA256: acknowledgement})
}
