package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A rejection artifact contains row ordinals and reasons, never source values.
// Verify ownership and integrity before releasing any download bytes.
func (s *Server) handleAnalysisRejections(w http.ResponseWriter, r *http.Request) {
	_, in := s.loadAnalysisImport(w, r)
	if in == nil {
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || index >= len(in.RejectionArtifacts) || in.State != "ready" {
		writeError(w, 404, "rejection artifact not found")
		return
	}
	key := in.RejectionArtifacts[index]
	if !strings.HasPrefix(key, analysisObjectKey(in.ProjectID, in.ID, "")) {
		writeError(w, 409, "artifact ownership mismatch")
		return
	}
	if !admitAnalysisPipeline(w) {
		return
	}
	defer func() { <-analysisPipelineJobs }()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	dir, err := analysisScratch()
	if err != nil {
		writeError(w, 503, "analytical staging unavailable")
		return
	}
	defer os.RemoveAll(dir)
	file, err := os.OpenFile(filepath.Join(dir, "rejected-rows.parquet"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		writeError(w, 503, "analytical staging unavailable")
		return
	}
	defer file.Close()
	remaining := analysisImportMaxBytes
	digest, err := analysisObjectRead(ctx, key, &analysisStagingWriter{out: file, dir: dir, remaining: &remaining}, remaining)
	if err != nil || digest != in.RejectionSHA256[key] || digest == "" {
		writeError(w, 503, "rejection artifact unavailable or corrupt")
		return
	}
	if _, err = file.Seek(0, 0); err != nil {
		writeError(w, 503, "rejection artifact unavailable")
		return
	}
	if err = ctx.Err(); errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeError(w, 408, "download cancelled")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apache.parquet")
	w.Header().Set("Content-Disposition", `attachment; filename="rejected-rows.parquet"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "rejected-rows.parquet", in.UpdatedAt, file)
}
