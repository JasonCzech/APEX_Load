package api

import (
	"net/http"
	"strings"
	"time"
)

// reportAnnotationsResponse is the payload returned to the dashboard report
// composer: the (saved or default-seeded) commentary plus the ordered section
// descriptors used to render the editable fields.
type reportAnnotationsResponse struct {
	Annotations *ReportAnnotations        `json:"annotations"`
	Sections    []reportCommentarySection `json:"sections"`
}

// reportAnnotationsRequest is the editable subset an analyst may submit. Server
// owns the test id, profile, and audit fields.
type reportAnnotationsRequest struct {
	Title              string            `json:"title"`
	PreparedBy         string            `json:"prepared_by"`
	ExecutiveNarrative string            `json:"executive_narrative"`
	SectionComments    map[string]string `json:"section_comments"`
	VerdictOverride    string            `json:"verdict_override"`
	VerdictExplanation string            `json:"verdict_explanation"`
	Findings           []ReportFinding   `json:"findings"`
}

// handleGetReportAnnotations returns the analyst commentary for a test+profile,
// seeding generated defaults from the archived report when none are saved.
func (s *Server) handleGetReportAnnotations(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	if _, ok := s.runner.GetTest(testID); !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	profile := r.URL.Query().Get("profile")

	report := s.tryBuildRunArchiveReport(testID)
	annotations, err := resolveReportAnnotations(s.store, report, testID, profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load report commentary: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, reportAnnotationsResponse{
		Annotations: annotations,
		Sections:    reportCommentarySectionOrder(),
	})
}

// handleSaveReportAnnotations persists analyst-edited commentary for a
// test+profile. Gated to editor+ by the auth middleware.
func (s *Server) handleSaveReportAnnotations(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	if _, ok := s.runner.GetTest(testID); !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage backend unavailable")
		return
	}
	profile := normalizeReportProfileID(r.URL.Query().Get("profile"))

	var req reportAnnotationsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.SectionComments == nil {
		req.SectionComments = map[string]string{}
	}

	_, actorSubject, actorDisplay := s.resolveAuditActor(r)
	ann := &ReportAnnotations{
		TestID:             testID,
		Profile:            profile,
		Title:              strings.TrimSpace(req.Title),
		PreparedBy:         strings.TrimSpace(req.PreparedBy),
		ExecutiveNarrative: strings.TrimSpace(req.ExecutiveNarrative),
		SectionComments:    req.SectionComments,
		VerdictOverride:    strings.TrimSpace(req.VerdictOverride),
		VerdictExplanation: strings.TrimSpace(req.VerdictExplanation),
		Findings:           normalizeReportFindings(req.Findings),
		UpdatedAt:          time.Now().UTC(),
		UpdatedBy:          firstNonEmpty(actorDisplay, actorSubject),
	}
	if err := saveReportAnnotations(s.store, ann); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save report commentary: "+err.Error())
		return
	}

	projectID, _, _ := resolveTestProjectID(r, s.store)
	s.recordAuditEvent(r, "report.annotations.update", "success", "test", testID, projectID, map[string]any{
		"profile": profile,
	})

	writeJSON(w, http.StatusOK, reportAnnotationsResponse{
		Annotations: ann,
		Sections:    reportCommentarySectionOrder(),
	})
}

// tryBuildRunArchiveReport returns the archived report for a test when an
// archive exists and materializes, otherwise nil (commentary defaults still
// render from generic text in that case).
func (s *Server) tryBuildRunArchiveReport(testID string) *RunArchiveReport {
	record, ok := s.runner.GetRunArchive(testID)
	if !ok {
		return nil
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		return nil
	}
	return report
}
