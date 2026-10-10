package api

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Root-cause summary states beyond an analysis's own states.
const (
	// rcaSummaryNotEnabled: the project does not present root cause.
	rcaSummaryNotEnabled = "not_enabled"
	// rcaSummaryNone: the project presents root cause but the run has no
	// analysis.
	rcaSummaryNone = "none"
)

// ReportRootCauseSummary is a run's root cause in brief, for the surfaces
// that list or headline runs (Summary, Results, the dashboard home, Command
// Center): whether the project presents root cause, the state of the run's
// newest analysis, and the verdict of its newest completed one. Every
// archive report response carries it, single or batch.
type ReportRootCauseSummary struct {
	// State is not_enabled, none, or the newest analysis's state: queued,
	// running, completed, failed or cancelled.
	State     string `json:"state"`
	Presented bool   `json:"presented"`
	// AnalysisEnabled says the project computes new analyses; AutoAnalyze
	// that it analyzes every archived run.
	AnalysisEnabled bool `json:"analysis_enabled"`
	AutoAnalyze     bool `json:"auto_analyze,omitempty"`
	// The verdict fields describe the newest completed analysis, which may be
	// older than a queued, running or failed newest analysis.
	AnalysisID      string                     `json:"analysis_id,omitempty"`
	Revision        int64                      `json:"revision,omitempty"`
	Origin          string                     `json:"origin,omitempty"`
	Outcome         string                     `json:"outcome,omitempty"`
	Validity        string                     `json:"validity,omitempty"`
	TopHypothesis   *ReportRootCauseHypothesis `json:"top_hypothesis,omitempty"`
	HypothesisCount int                        `json:"hypothesis_count"`
	AnomalyCount    int                        `json:"anomaly_count"`
	CompletedAt     *time.Time                 `json:"completed_at,omitempty"`
	// ArchiveChanged says the run's archive evidence changed after the
	// completed analysis read it.
	ArchiveChanged bool `json:"archive_changed,omitempty"`
	// Latest is the newest analysis when it is not the completed one.
	Latest *ReportRootCauseLatest `json:"latest,omitempty"`
}

// ReportRootCauseLatest is a run's newest analysis when it has not completed.
type ReportRootCauseLatest struct {
	AnalysisID string    `json:"analysis_id"`
	Revision   int64     `json:"revision"`
	State      string    `json:"state"`
	Origin     string    `json:"origin,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// rcaVerdict is what a summary takes from a completed analysis's assessment.
// A completed analysis never changes, so it is cached by analysis ID.
type rcaVerdict struct {
	Outcome         string
	Validity        string
	Top             *ReportRootCauseHypothesis
	HypothesisCount int
	AnomalyCount    int
	Payload         rcaAnalysisPayload
}

var (
	rcaVerdicts          sync.Map
	rcaVerdictCount      atomic.Int64
	rcaVerdictCacheLimit = int64(2048)
)

// rcaCompletedVerdict reads a completed analysis's verdict, from the cache
// when it has been read before.
func rcaCompletedVerdict(store storage.RCAStore, analysis *storage.RCAAnalysis) (*rcaVerdict, bool) {
	if cached, ok := rcaVerdicts.Load(analysis.ID); ok {
		return cached.(*rcaVerdict), true
	}
	full, err := store.GetRCAAnalysis(analysis.ProjectID, analysis.TestID, analysis.ID)
	if err != nil || full == nil || full.State != storage.RCAStateCompleted {
		return nil, false
	}
	payload := rcaAnalysisPayloadOf(full)
	if payload.Assessment == nil {
		return nil, false
	}
	verdict := &rcaVerdict{Outcome: payload.Assessment.Outcome, Validity: payload.Assessment.Validity.Verdict, Top: rcaSummaryTop(payload.Assessment),
		HypothesisCount: len(payload.Assessment.Hypotheses), AnomalyCount: len(payload.Assessment.Anomalies)}
	// Keep only what the archive-changed check reads.
	verdict.Payload = rcaAnalysisPayload{Inputs: payload.Inputs, ArchiveManifest: payload.ArchiveManifest}
	if rcaVerdictCount.Add(1) > rcaVerdictCacheLimit {
		rcaVerdicts.Range(func(k, _ any) bool { rcaVerdicts.Delete(k); return true })
		rcaVerdictCount.Store(1)
	}
	rcaVerdicts.Store(analysis.ID, verdict)
	return verdict, true
}

// rootCauseSummaries summarizes the root cause of a project's runs. A run
// whose analyses cannot be read has no summary.
func (s *Server) rootCauseSummaries(ctx context.Context, project string, testIDs []string) map[string]*ReportRootCauseSummary {
	gates := projectAnalysisGates(s.store, project)
	out := make(map[string]*ReportRootCauseSummary, len(testIDs))
	gated := ReportRootCauseSummary{State: rcaSummaryNone, Presented: gates.RootCause.Present, AnalysisEnabled: gates.RootCause.Enabled, AutoAnalyze: gates.RootCause.AutoAnalyze}
	if !gates.RootCause.Present {
		gated.State = rcaSummaryNotEnabled
	}
	store, ok := s.store.(storage.RCAStore)
	if !ok || !gates.RootCause.Present || len(testIDs) == 0 {
		for _, id := range testIDs {
			summary := gated
			out[id] = &summary
		}
		return out
	}
	analyses, err := store.LatestRCAAnalyses(project, testIDs)
	if err != nil {
		slog.Warn("archive report: root-cause summaries unavailable", "project", project, "runs", len(testIDs), "err", err)
		return out
	}
	for _, id := range testIDs {
		summary := gated
		entry := analyses[id]
		if entry.Latest != nil {
			summary.State = entry.Latest.State
		}
		if completed := entry.Completed; completed != nil {
			if verdict, ok := rcaCompletedVerdict(store, completed); ok {
				summary.AnalysisID, summary.Revision, summary.Origin, summary.CompletedAt = completed.ID, completed.Revision, completed.Origin, completed.CompletedAt
				summary.Outcome, summary.Validity, summary.TopHypothesis = verdict.Outcome, verdict.Validity, verdict.Top
				summary.HypothesisCount, summary.AnomalyCount = verdict.HypothesisCount, verdict.AnomalyCount
				summary.ArchiveChanged = s.rcaArchiveChanged(ctx, id, verdict.Payload)
			}
		}
		if latest := entry.Latest; latest != nil && (entry.Completed == nil || latest.ID != entry.Completed.ID) {
			summary.Latest = &ReportRootCauseLatest{AnalysisID: latest.ID, Revision: latest.Revision, State: latest.State, Origin: latest.Origin, CreatedAt: latest.CreatedAt}
		}
		out[id] = &summary
	}
	return out
}

// attachRootCauseSummary sets a report's root-cause summary.
func (s *Server) attachRootCauseSummary(ctx context.Context, report *RunArchiveReport) {
	if report == nil || report.Manifest == nil {
		return
	}
	testID := report.Manifest.TestID
	report.RootCauseSummary = s.rootCauseSummaries(ctx, normalizeProjectID(report.Manifest.Provenance.ProjectID), []string{testID})[testID]
}

// rcaSummaryTop is an assessment's leading hypothesis as the reports name
// it, or nil when it has none.
func rcaSummaryTop(a *rca.Assessment) *ReportRootCauseHypothesis {
	if a == nil {
		return nil
	}
	view := reportRootCauseFromV2(a, nil)
	if len(view.Hypotheses) == 0 {
		return nil
	}
	top := view.Hypotheses[0]
	return &ReportRootCauseHypothesis{ID: top.ID, Rank: top.Rank, Title: top.Title, Confidence: top.Confidence, Explains: top.Explains}
}
