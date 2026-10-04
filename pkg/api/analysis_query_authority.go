package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Metadata implementations predating these queries expose context-free reads.
// Their return alone is not admission: recheck actual context time after every
// read and again before the caller writes any accepted analytical result.
func analysisQueryContextLive(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	deadline, exists := ctx.Deadline()
	return !exists || deadline.After(time.Now())
}

func (s *Server) verifyNativeDatasetQuery(ctx context.Context, project string, snapshots ...*storage.AnalysisImport) error {
	return s.verifyNativeQuerySnapshots(ctx, project, true, snapshots...)
}

func nativePreviewStateAvailable(state string) bool {
	switch state {
	case "uploading", "mapped", "failed", "ready":
		return true
	default:
		return false
	}
}

func (s *Server) verifyNativeQuerySnapshots(ctx context.Context, project string, readyOnly bool, snapshots ...*storage.AnalysisImport) error {
	if !analysisQueryContextLive(ctx) || len(snapshots) < 1 || len(snapshots) > 2 {
		return errors.New("analytical query authority expired")
	}
	owner, err := s.store.GetProject(project)
	if !analysisQueryContextLive(ctx) {
		return errors.New("analytical query authority expired")
	}
	if err != nil || owner == nil || owner.ID != project {
		return errors.New("analytical project is no longer active")
	}
	imports, ok := s.store.(storage.AnalysisImportStore)
	if !ok {
		return errors.New("analytical metadata unavailable")
	}
	for _, snapshot := range snapshots {
		if snapshot == nil || snapshot.ProjectID != project || (!nativePreviewStateAvailable(snapshot.State) || readyOnly && snapshot.State != "ready") || snapshot.Revision < 1 {
			return errors.New("analytical query snapshot mismatch")
		}
		in, err := imports.GetAnalysisImport(snapshot.ID)
		if !analysisQueryContextLive(ctx) {
			return errors.New("analytical query authority expired")
		}
		if err != nil || in == nil || in.ID != snapshot.ID || in.ProjectID != project || in.State != snapshot.State || in.Revision != snapshot.Revision || in.RunID != snapshot.RunID {
			return errors.New("analytical dataset changed or is no longer active")
		}
	}
	if !analysisQueryContextLive(ctx) {
		return errors.New("analytical query authority expired")
	}
	return nil
}

func (s *Server) writeNativeDatasetSeries(w http.ResponseWriter, r *http.Request, captured *storage.AnalysisImport, points []analysis.MetricPoint) bool {
	if captured == nil {
		writeError(w, 409, "analytical query snapshot mismatch")
		return false
	}
	if !requireAuthorizedProjectMatch(w, r, captured.ProjectID) {
		return false
	}
	if err := s.verifyNativeDatasetQuery(r.Context(), captured.ProjectID, captured); err != nil {
		writeError(w, 409, err.Error())
		return false
	}
	if !requireAuthorizedProjectMatch(w, r, captured.ProjectID) {
		return false
	}
	if !analysisQueryContextLive(r.Context()) {
		writeError(w, 409, "analytical query authority expired")
		return false
	}
	writeJSON(w, 200, map[string]any{"points": points, "dataset_id": captured.ID, "rejected_rows": captured.Rejected})
	return true
}

// Preview is a completed analytical result too. A cancelled/deleted import or
// later mapping/upload/association revision cannot authorize the old result.
func (s *Server) writeNativeAnalysisPreview(w http.ResponseWriter, r *http.Request, captured *storage.AnalysisImport, preview analytics.Preview) bool {
	if captured == nil {
		writeError(w, 409, "analytical preview snapshot mismatch")
		return false
	}
	if !requireAuthorizedProjectMatch(w, r, captured.ProjectID) {
		return false
	}
	if err := s.verifyNativeQuerySnapshots(r.Context(), captured.ProjectID, false, captured); err != nil {
		writeError(w, 409, err.Error())
		return false
	}
	if !requireAuthorizedProjectMatch(w, r, captured.ProjectID) {
		return false
	}
	if !analysisQueryContextLive(r.Context()) {
		writeError(w, 409, "analytical preview authority expired")
		return false
	}
	writeJSON(w, 200, preview)
	return true
}
