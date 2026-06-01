package api

import (
	"net/http"
)

func (s *Server) handleGetReleaseTrendExport(w http.ResponseWriter, r *http.Request) {
	releaseID := r.PathValue("id")
	tests := s.runner.ListTestsByContext("", releaseID, "")
	runs := make([]releaseTrendRun, 0, len(tests))
	for _, test := range tests {
		record, ok := s.runner.GetRunArchive(test.ID)
		if !ok {
			continue
		}
		report, err := buildRunArchiveReport(record)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build archive report for "+test.ID+": "+err.Error())
			return
		}
		runs = append(runs, releaseTrendRun{Test: test, Report: report})
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-release-trend-"+releaseID+".csv\"")
	if err := writeReleaseTrendCSV(w, runs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write release trend csv: "+err.Error())
	}
}
