package api

import (
	"context"
	"net/url"
	"strings"
)

// ciRunRootCause is a CI run's root-cause analysis in brief: the state of its
// newest analysis and, once one completed, the outcome and leading
// hypothesis, with the dashboard route that opens it.
type ciRunRootCause struct {
	// State is none, queued, running, completed, failed or cancelled.
	State         string `json:"state"`
	Outcome       string `json:"outcome,omitempty"`
	TopHypothesis string `json:"top_hypothesis,omitempty"`
	Confidence    string `json:"confidence,omitempty"`
	AnalysisID    string `json:"analysis_id,omitempty"`
	ReportURL     string `json:"report_url,omitempty"`
	// AutoAnalyze says the project analyzes every archived run, so an
	// analysis not yet queued is still expected.
	AutoAnalyze bool `json:"auto_analyze,omitempty"`
}

// ciRunRootCause summarizes a terminal CI run's root cause, or nil when the
// project does not present root cause.
func (s *Server) ciRunRootCause(ctx context.Context, test *ManagedTest) *ciRunRootCause {
	if test == nil {
		return nil
	}
	summary := s.rootCauseSummaries(ctx, normalizeProjectID(test.ProjectID), []string{test.ID})[test.ID]
	if summary == nil || summary.State == rcaSummaryNotEnabled {
		return nil
	}
	out := &ciRunRootCause{State: summary.State, Outcome: summary.Outcome, AnalysisID: summary.AnalysisID, AutoAnalyze: summary.AutoAnalyze}
	hypothesis := ""
	if top := summary.TopHypothesis; top != nil {
		out.TopHypothesis = sanitizeCIExternalText(top.Title, 256)
		out.Confidence = top.Confidence
		hypothesis = top.ID
	}
	out.ReportURL = ciPublicURL("/" + rcaReportLink(url.PathEscape(test.ID), summary.AnalysisID, hypothesis))
	return out
}

// ciRootCauseText is a CI run's root cause in one line, for the GitHub Check
// and the job summary.
func ciRootCauseText(rootCause *ciRunRootCause) string {
	if rootCause == nil {
		return ""
	}
	switch {
	case rootCause.Outcome == "supported" && rootCause.TopHypothesis != "":
		text := rootCause.TopHypothesis
		if rootCause.Confidence != "" {
			text += " (" + rootCause.Confidence + " confidence)"
		}
		return text
	case rootCause.Outcome == "no_supported_hypothesis":
		return "no cause supported"
	case rootCause.Outcome == "insufficient_evidence":
		return "insufficient evidence"
	case rootCause.State == "none":
		return "not analyzed"
	}
	return "analysis " + strings.ReplaceAll(rootCause.State, "_", " ") + "; see the run report"
}
