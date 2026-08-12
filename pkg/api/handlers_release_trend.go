package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
)

func (s *Server) handleGetReleaseTrendExport(w http.ResponseWriter, r *http.Request) {
	releaseID := r.PathValue("id")
	family, err := s.resolveReleaseFamily(releaseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	runs, err := s.collectReleaseTrendRunsForFamily(family, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-release-trend-"+family.CanonicalID+".csv\"")
	if err := writeReleaseTrendCSV(w, runs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write release trend csv: "+err.Error())
	}
}

// handleGetReleaseTrend returns the multi-run trend analysis for a release as
// JSON: an ordered run series with run-over-run deltas, per-run regression
// flags, and a first→last trajectory summary.
func (s *Server) handleGetReleaseTrend(w http.ResponseWriter, r *http.Request) {
	releaseID := r.PathValue("id")
	family, err := s.resolveReleaseFamily(releaseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	runs, err := s.collectReleaseTrendRunsForFamily(family, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buildReleaseTrend(family.CanonicalID, runs))
}

// collectReleaseTrendRuns gathers archived runs for a release and builds their
// reports — shared by the CSV export and the JSON trend analysis.
func (s *Server) collectReleaseTrendRuns(releaseID string, requireReport bool) ([]releaseTrendRun, error) {
	family, err := s.resolveReleaseFamily(releaseID)
	if err != nil {
		return nil, err
	}
	return s.collectReleaseTrendRunsForFamily(family, requireReport)
}

func (s *Server) collectReleaseTrendRunsForFamily(family resolvedReleaseFamily, requireReport bool) ([]releaseTrendRun, error) {
	tests := s.listReleaseFamilyTests(family)
	runs := make([]releaseTrendRun, 0, len(tests))
	for _, test := range tests {
		record, ok := s.runner.GetRunArchive(test.ID)
		if !ok {
			continue
		}
		var cachedPoint *ReleaseTrendPoint
		if strings.TrimSpace(record.TrendPointJSON) != "" {
			var point ReleaseTrendPoint
			if err := json.Unmarshal([]byte(record.TrendPointJSON), &point); err == nil && point.RunID == test.ID {
				cachedPoint = &point
				if !requireReport {
					runs = append(runs, releaseTrendRun{Test: test, Point: cachedPoint})
					continue
				}
			}
		}
		report, err := cachedRunArchiveReport(record)
		if err != nil {
			// One corrupt/unreadable archive must not fail the whole trend; skip it.
			slog.Warn("release trend: archive report unavailable", "test", test.ID, "err", err)
			continue
		}
		// Missing, malformed, and mismatched projections are all repairable. Use a
		// targeted publication CAS so this request cannot rewrite a concurrently
		// refreshed archive from its stale catalog snapshot.
		if cachedPoint == nil {
			point := trendPointFromRun(releaseTrendRun{Test: test, Report: report})
			if encoded, encodeErr := json.Marshal(point); encodeErr == nil {
				if _, saveErr := s.runner.updateRunArchiveTrendPointIfUnchanged(record, string(encoded)); saveErr != nil {
					slog.Warn("release trend: compact trend point could not be cached", "test", test.ID, "err", saveErr)
				}
			}
		}
		runs = append(runs, releaseTrendRun{Test: test, Report: report})
	}
	return runs, nil
}

// attachReleaseTrend enriches a freshly built archive report with the compact
// release-over-release trend summary. It is the storage-aware counterpart to the
// pure buildRunArchiveReport: it reads the run's release id from provenance, runs
// the multi-run release-trend analysis over the release's archived siblings, and
// distills this run's slice of it.
//
// Best-effort: a run with no release association or an unreadable sibling archive
// simply leaves report.ReleaseTrend nil so the report still renders. Note this
// collects and rebuilds every archived report in the release (the same cost as the
// /releases/{id}/trend endpoint); it is on-demand only, never on the live path.
func (s *Server) attachReleaseTrend(report *RunArchiveReport) {
	if report == nil || report.Manifest == nil {
		return
	}
	releaseID := strings.TrimSpace(report.Manifest.Provenance.ReleaseID)
	if releaseID == "" {
		return
	}
	family, err := s.resolveReleaseFamily(releaseID)
	if err != nil {
		slog.Warn("archive report: release trend unavailable",
			"release", releaseID, "test", report.Manifest.TestID, "err", err)
		return
	}
	runs, err := s.collectReleaseTrendRunsForFamily(family, false)
	if err != nil {
		slog.Warn("archive report: release trend unavailable",
			"release", releaseID, "test", report.Manifest.TestID, "err", err)
		return
	}
	report.ReleaseTrend = buildRunArchiveReleaseTrendSummary(buildReleaseTrend(family.CanonicalID, runs), report.Manifest.TestID)
}

// attachReleaseTrendForProfile attaches the release-trend summary only when the
// named profile actually renders the release_trend section, sparing the multi-run
// collection cost for profiles (engineer/audit) that never display it.
func (s *Server) attachReleaseTrendForProfile(report *RunArchiveReport, profileID string) {
	if !profileIncludesReleaseTrend(profileID) {
		return
	}
	s.attachReleaseTrend(report)
}

// profileIncludesReleaseTrend reports whether the resolved report profile declares
// the release_trend section. An empty/unknown id resolves through the same default
// (engineer) the renderers use, which does not include the section.
func profileIncludesReleaseTrend(profileID string) bool {
	return profileIncludesSection(profileID, "release_trend")
}

// profileIncludesSection reports whether the resolved report profile declares the
// given section. An empty/unknown id resolves through the same default the renderers
// use.
func profileIncludesSection(profileID, section string) bool {
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return false
	}
	return slices.Contains(profile.IncludedSections, section)
}
