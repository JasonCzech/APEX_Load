package api

import (
	"context"
	"errors"
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
	ExpectedUpdatedAt  time.Time         `json:"expected_updated_at"`
	Title              string            `json:"title"`
	PreparedBy         string            `json:"prepared_by"`
	ExecutiveNarrative string            `json:"executive_narrative"`
	SectionComments    map[string]string `json:"section_comments"`
	VerdictOverride    string            `json:"verdict_override"`
	VerdictExplanation string            `json:"verdict_explanation"`
	// ExpectedVerdictUpdatedAt is the verdict revision the editor loaded. The
	// verdict is shared by every report audience, so a change is refused when
	// another audience changed it since. Omitted by older clients.
	ExpectedVerdictUpdatedAt *time.Time      `json:"expected_verdict_updated_at,omitempty"`
	Findings                 []ReportFinding `json:"findings"`
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

	report := s.tryBuildRunArchiveReport(r.Context(), testID)
	annotations, err := resolveReportAnnotations(s.store, report, testID, profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load report commentary: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, reportAnnotationsResponse{
		Annotations: annotations,
		Sections:    reportCommentarySectionOrder(profile),
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

	r.Body = http.MaxBytesReader(w, r.Body, reportAnnotationsMaxRequestBytes)
	var req reportAnnotationsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.SectionComments == nil {
		req.SectionComments = map[string]string{}
	}
	if err := validateReportAnnotationsRequest(profile, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	currentVerdict, currentVerdictRaw, err := loadRunReportVerdictRecord(s.store, testID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load report verdict: "+err.Error())
		return
	}
	verdictOverride := strings.TrimSpace(req.VerdictOverride)
	verdictExplanation := strings.TrimSpace(req.VerdictExplanation)
	verdictChanged := currentVerdict == nil && (verdictOverride != "" || verdictExplanation != "") ||
		currentVerdict != nil && (currentVerdict.VerdictOverride != verdictOverride || currentVerdict.VerdictExplanation != verdictExplanation)
	if verdictChanged && currentVerdict != nil && req.ExpectedVerdictUpdatedAt != nil && !currentVerdict.UpdatedAt.Equal(*req.ExpectedVerdictUpdatedAt) {
		writeError(w, http.StatusConflict, "the analyst verdict was changed from another report audience since it was loaded; reload the latest revision before saving")
		return
	}

	_, actorSubject, actorDisplay := s.resolveAuditActor(r)
	ann := &ReportAnnotations{
		TestID:             testID,
		Profile:            profile,
		Title:              strings.TrimSpace(req.Title),
		PreparedBy:         strings.TrimSpace(req.PreparedBy),
		ExecutiveNarrative: strings.TrimSpace(req.ExecutiveNarrative),
		SectionComments:    req.SectionComments,
		VerdictOverride:    verdictOverride,
		VerdictExplanation: verdictExplanation,
		Findings:           normalizeReportFindings(req.Findings),
		UpdatedAt:          time.Now().UTC(),
		UpdatedBy:          firstNonEmpty(actorDisplay, actorSubject),
	}
	if _, err := marshalReportAnnotations(ann); err != nil {
		if errors.Is(err, errReportAnnotationsTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to encode report commentary: "+err.Error())
		return
	}
	updated, err := saveReportAnnotationsIfUnchanged(s.store, ann, req.ExpectedUpdatedAt)
	if err != nil {
		if errors.Is(err, errReportAnnotationsTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save report commentary: "+err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "report commentary changed since it was loaded; reload the latest revision before saving")
		return
	}

	if verdictChanged {
		currentVerdict = &runReportVerdict{
			VerdictOverride:    verdictOverride,
			VerdictExplanation: verdictExplanation,
			Profile:            profile,
			UpdatedAt:          ann.UpdatedAt,
			UpdatedBy:          ann.UpdatedBy,
		}
		saved, err := saveRunReportVerdictIfUnchanged(s.store, testID, *currentVerdict, currentVerdictRaw)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "report commentary was saved but the analyst verdict was not: "+err.Error())
			return
		}
		if !saved {
			writeError(w, http.StatusConflict, "report commentary was saved, but the analyst verdict changed from another report audience at the same time; reload and set the verdict again")
			return
		}
	}
	applyRunReportVerdict(ann, currentVerdict)

	projectID, _, _ := resolveTestProjectID(r, s.store)
	s.recordAuditEvent(r, "report.annotations.update", "success", "test", testID, projectID, map[string]any{
		"profile":         profile,
		"verdict_changed": verdictChanged,
	})

	writeJSON(w, http.StatusOK, reportAnnotationsResponse{
		Annotations: ann,
		Sections:    reportCommentarySectionOrder(profile),
	})
}

// tryBuildRunArchiveReport returns the archived report for a test when an
// archive exists and materializes, otherwise nil (commentary defaults still
// render from generic text in that case).
func (s *Server) tryBuildRunArchiveReport(ctx context.Context, testID string) *RunArchiveReport {
	record, ok := s.runner.GetRunArchiveContext(ctx, testID)
	if !ok {
		return nil
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil
	}
	defer lease.Release()
	report, err := s.archiveReadCaches().cachedRunArchiveReport(record)
	if err != nil {
		return nil
	}
	return report
}
