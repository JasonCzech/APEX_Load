package api

import (
	"log/slog"
	"net/http"
)

func (s *Server) handleGetReleaseTrendExport(w http.ResponseWriter, r *http.Request) {
	releaseID := r.PathValue("id")
	runs, err := s.collectReleaseTrendRuns(releaseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-release-trend-"+releaseID+".csv\"")
	if err := writeReleaseTrendCSV(w, runs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write release trend csv: "+err.Error())
	}
}

// handleGetReleaseTrend returns the multi-run trend analysis for a release as
// JSON: an ordered run series with run-over-run deltas, per-run regression
// flags, and a first→last trajectory summary.
func (s *Server) handleGetReleaseTrend(w http.ResponseWriter, r *http.Request) {
	releaseID := r.PathValue("id")
	runs, err := s.collectReleaseTrendRuns(releaseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buildReleaseTrend(releaseID, runs))
}

// collectReleaseTrendRuns gathers archived runs for a release and builds their
// reports — shared by the CSV export and the JSON trend analysis.
func (s *Server) collectReleaseTrendRuns(releaseID string) ([]releaseTrendRun, error) {
	tests := s.runner.ListTestsByContext("", releaseID, "")
	runs := make([]releaseTrendRun, 0, len(tests))
	for _, test := range tests {
		record, ok := s.runner.GetRunArchive(test.ID)
		if !ok {
			continue
		}
		report, err := buildRunArchiveReport(record)
		if err != nil {
			// One corrupt/unreadable archive must not fail the whole trend; skip it.
			slog.Warn("release trend: archive report unavailable", "test", test.ID, "err", err)
			continue
		}
		runs = append(runs, releaseTrendRun{Test: test, Report: report})
	}
	return runs, nil
}
