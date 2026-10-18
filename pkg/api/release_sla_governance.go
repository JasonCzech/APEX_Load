package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type releaseSLAWaiverPersistence interface {
	SaveReleaseSLAWaiver(waiver *storage.ReleaseSLAWaiver) error
	ListReleaseSLAWaivers(releaseID string) ([]*storage.ReleaseSLAWaiver, error)
}
type ReleaseSLAGovernanceResponse struct {
	GeneratedAt   time.Time                   `json:"generated_at"`
	ReleaseID     string                      `json:"release_id"`
	ReleaseName   string                      `json:"release_name,omitempty"`
	ApplicationID string                      `json:"application_id,omitempty"`
	ProjectID     string                      `json:"project_id,omitempty"`
	Owner         ReleaseSLAGovernanceOwner   `json:"owner"`
	Status        string                      `json:"status"`
	Summary       ReleaseSLAGovernanceSummary `json:"summary"`
	BreachHistory []ReleaseSLAGovernanceRun   `json:"breach_history,omitempty"`
	Waivers       []*storage.ReleaseSLAWaiver `json:"waivers,omitempty"`
	AuditTrail    []*storage.AuditEvent       `json:"audit_trail,omitempty"`
	NextActions   []string                    `json:"next_actions,omitempty"`
}

type ReleaseSLAGovernanceOwner struct {
	Type        string `json:"type"`
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"display_name"`
}

type ReleaseSLAGovernanceSummary struct {
	TotalRuns        int `json:"total_runs"`
	ArchivedRuns     int `json:"archived_runs"`
	PassedRuns       int `json:"passed_runs"`
	FailedRuns       int `json:"failed_runs"`
	WaivedBreaches   int `json:"waived_breaches"`
	UnwaivedBreaches int `json:"unwaived_breaches"`
}

type ReleaseSLAGovernanceRun struct {
	TestID          string     `json:"test_id"`
	TestName        string     `json:"test_name,omitempty"`
	Status          string     `json:"status"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	SLABreached     bool       `json:"sla_breached"`
	SLADetails      []string   `json:"sla_details,omitempty"`
	TelemetryStatus string     `json:"telemetry_status,omitempty"`
	Completeness    string     `json:"completeness,omitempty"`
}

type createReleaseSLAWaiverRequest struct {
	Scope      string     `json:"scope,omitempty"`
	Reason     string     `json:"reason"`
	ApprovedBy string     `json:"approved_by,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) handleGetReleaseSLAGovernance(w http.ResponseWriter, r *http.Request) {
	releaseID := strings.TrimSpace(r.PathValue("id"))
	response, err := s.buildReleaseSLAGovernance(releaseID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleCreateReleaseSLAWaiver(w http.ResponseWriter, r *http.Request) {
	releaseID := strings.TrimSpace(r.PathValue("id"))
	var req createReleaseSLAWaiverRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Scope = strings.TrimSpace(req.Scope)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Scope == "" {
		req.Scope = "release"
	}
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "waiver reason is required")
		return
	}
	family, err := s.resolveReleaseFamily(releaseID)
	if err != nil && s.store != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	projectID := family.ProjectID
	storageReleaseID := releaseID
	if len(family.Members) > 0 {
		storageReleaseID = family.Members[0].ID
		for _, member := range family.Members {
			if member != nil && member.ArchivedAt != nil {
				writeError(w, http.StatusConflict, "cannot create an SLA waiver for an archived release")
				return
			}
		}
		if storageReleaseID == "" {
			writeError(w, http.StatusConflict, "cannot create an SLA waiver for an archived release")
			return
		}
	}
	// approved_by is always derived from the authenticated actor and never
	// trusted from the request body, so the governance/audit record of who
	// accepted the risk cannot be spoofed by the caller.
	_, subject, displayName := s.resolveAuditActor(r)
	req.ApprovedBy = firstNonEmpty(displayName, subject)
	now := time.Now().UTC()
	waiver := &storage.ReleaseSLAWaiver{
		ID:         uuid.New().String(),
		ReleaseID:  storageReleaseID,
		ProjectID:  projectID,
		Scope:      req.Scope,
		Reason:     req.Reason,
		ApprovedBy: req.ApprovedBy,
		Status:     "active",
		ExpiresAt:  req.ExpiresAt,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if waiver.ExpiresAt != nil && waiver.ExpiresAt.Before(now) {
		waiver.Status = "expired"
	}
	if err := s.saveReleaseSLAWaiver(waiver); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save SLA waiver: "+err.Error())
		return
	}
	canonicalID := firstNonEmpty(family.CanonicalID, releaseID)
	publicWaiver := *waiver
	publicWaiver.ReleaseID = canonicalID
	s.recordAuditEvent(r, "release.sla_waiver.create", "success", "release", canonicalID, projectID, map[string]any{
		"waiver_id":  waiver.ID,
		"scope":      waiver.Scope,
		"reason":     waiver.Reason,
		"expires_at": waiver.ExpiresAt,
	})
	governance, err := s.buildReleaseSLAGovernance(canonicalID)
	if err != nil {
		writeJSON(w, http.StatusCreated, map[string]any{"waiver": &publicWaiver})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"waiver": &publicWaiver, "governance": governance})
}

func (s *Server) buildReleaseSLAGovernance(releaseID string) (ReleaseSLAGovernanceResponse, error) {
	if releaseID == "" {
		return ReleaseSLAGovernanceResponse{}, fmt.Errorf("release id is required")
	}
	family, err := s.resolveReleaseFamily(releaseID)
	if err != nil && s.store != nil {
		return ReleaseSLAGovernanceResponse{}, err
	}
	waivers, err := s.listReleaseFamilySLAWaivers(family)
	if err != nil {
		return ReleaseSLAGovernanceResponse{}, err
	}
	tests := s.listReleaseFamilyTests(family)
	sort.SliceStable(tests, func(left, right int) bool {
		return managedTestCompletedAt(tests[left]).After(managedTestCompletedAt(tests[right]))
	})
	response := ReleaseSLAGovernanceResponse{
		GeneratedAt: time.Now().UTC(),
		ReleaseID:   firstNonEmpty(family.CanonicalID, releaseID),
		ReleaseName: family.Name,
		ProjectID:   family.ProjectID,
		Owner: ReleaseSLAGovernanceOwner{
			Type:        "project",
			ID:          family.ProjectID,
			DisplayName: "Project administrators",
		},
		Waivers: waivers,
	}
	for _, test := range tests {
		if test == nil {
			continue
		}
		response.Summary.TotalRuns++
		run := ReleaseSLAGovernanceRun{
			TestID:      test.ID,
			TestName:    test.Name,
			Status:      test.Status,
			CompletedAt: test.EndedAt,
		}
		if record, ok := s.runner.GetRunArchive(test.ID); ok {
			report, err := s.archiveReadCaches().cachedRunArchiveReport(record)
			if err != nil {
				// A single corrupt/unreadable archive must not 500 the whole
				// release's governance view. Surface the run as degraded
				// (conservatively breached) and continue with the other runs.
				slog.Warn("release SLA governance: archive report unavailable", "test", test.ID, "err", err)
				response.Summary.ArchivedRuns++
				run.Completeness = "unknown"
				run.TelemetryStatus = "unknown"
				run.SLABreached = true
				run.SLADetails = []string{"archive report unavailable: " + err.Error()}
				recordReleaseSLABreach(&response, run, waivers)
				continue
			}
			response.Summary.ArchivedRuns++
			if report.Manifest != nil {
				run.SLABreached = report.Manifest.SLABreached
				run.SLADetails = append([]string(nil), report.Manifest.SLADetails...)
			}
			if report.Overview.Telemetry != nil {
				run.TelemetryStatus = report.Overview.Telemetry.Status
			}
			run.Completeness = report.Completeness.Status
		} else if test.Status == "FAILED" {
			run.SLABreached = true
			run.SLADetails = []string{firstNonEmpty(test.Error, "Run failed before archive evidence was available.")}
		}
		if run.SLABreached {
			recordReleaseSLABreach(&response, run, waivers)
		} else if test.Status == "COMPLETE" {
			response.Summary.PassedRuns++
		}
	}
	switch {
	case response.Summary.TotalRuns == 0:
		response.Status = "no_runs"
		response.NextActions = []string{"Run at least one release-scoped scenario before evaluating the release SLA gate."}
	case response.Summary.UnwaivedBreaches > 0:
		response.Status = "failed"
		response.NextActions = []string{"Investigate breach history and create an approved waiver only when release governance accepts the risk."}
	case response.Summary.WaivedBreaches > 0:
		response.Status = "waived"
		response.NextActions = []string{"Review waiver expiration and audit trail before promotion."}
	default:
		response.Status = "passed"
		response.NextActions = []string{"Export executive or audit report profiles for release evidence."}
	}
	response.AuditTrail = s.releaseFamilyAuditTrail(family)
	return response, nil
}

func recordReleaseSLABreach(response *ReleaseSLAGovernanceResponse, run ReleaseSLAGovernanceRun, waivers []*storage.ReleaseSLAWaiver) {
	if response == nil {
		return
	}
	response.Summary.FailedRuns++
	response.BreachHistory = append(response.BreachHistory, run)
	for _, waiver := range waivers {
		if releaseSLAWaiverCoversRun(waiver, run, response.GeneratedAt) {
			response.Summary.WaivedBreaches++
			return
		}
	}
	response.Summary.UnwaivedBreaches++
}

func releaseSLAWaiverCoversRun(waiver *storage.ReleaseSLAWaiver, run ReleaseSLAGovernanceRun, evaluatedAt time.Time) bool {
	if waiver == nil || !strings.EqualFold(strings.TrimSpace(waiver.Status), "active") ||
		waiver.CreatedAt.IsZero() || run.CompletedAt == nil || run.CompletedAt.IsZero() {
		return false
	}
	if waiver.ExpiresAt != nil && !waiver.ExpiresAt.After(evaluatedAt) {
		return false
	}
	// A waiver records acceptance of evidence that already existed. It cannot
	// prospectively waive a breach produced after that approval was created.
	if run.CompletedAt.After(waiver.CreatedAt) {
		return false
	}
	scope := strings.TrimSpace(waiver.Scope)
	if strings.EqualFold(scope, "release") {
		return true
	}
	if scope == run.TestID {
		return true
	}
	lowerScope := strings.ToLower(scope)
	return lowerScope == "test:"+strings.ToLower(run.TestID) || lowerScope == "run:"+strings.ToLower(run.TestID)
}

func (s *Server) releaseContext(releaseID string) (projectID string, releaseName string, applicationID string, err error) {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return "", "", "", fmt.Errorf("release id is required")
	}
	if s.store == nil {
		return storage.DefaultProjectID, releaseID, "", nil
	}
	release, err := s.store.GetRelease(releaseID)
	if err != nil || release == nil {
		return "", "", "", fmt.Errorf("release not found")
	}
	projectID = storage.DefaultProjectID
	if application, appErr := s.store.GetApplication(release.ApplicationID); appErr == nil && application != nil {
		projectID = normalizeProjectID(application.ProjectID)
	}
	return projectID, release.Name, release.ApplicationID, nil
}

func (s *Server) saveReleaseSLAWaiver(waiver *storage.ReleaseSLAWaiver) error {
	if store, ok := s.store.(releaseSLAWaiverPersistence); ok {
		return store.SaveReleaseSLAWaiver(waiver)
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	releaseID := strings.TrimSpace(waiver.ReleaseID)
	copied := *waiver
	s.slaWaivers[releaseID] = append(s.slaWaivers[releaseID], &copied)
	return nil
}

func (s *Server) listReleaseSLAWaivers(releaseID string) ([]*storage.ReleaseSLAWaiver, error) {
	if store, ok := s.store.(releaseSLAWaiverPersistence); ok {
		return store.ListReleaseSLAWaivers(releaseID)
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	waivers := s.slaWaivers[strings.TrimSpace(releaseID)]
	out := make([]*storage.ReleaseSLAWaiver, 0, len(waivers))
	now := time.Now().UTC()
	for _, waiver := range waivers {
		if waiver == nil {
			continue
		}
		copied := *waiver
		if copied.ExpiresAt != nil && copied.ExpiresAt.Before(now) && copied.Status == "active" {
			copied.Status = "expired"
		}
		out = append(out, &copied)
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].CreatedAt.After(out[right].CreatedAt)
	})
	return out, nil
}

func (s *Server) listReleaseFamilySLAWaivers(family resolvedReleaseFamily) ([]*storage.ReleaseSLAWaiver, error) {
	ids := family.memberIDs()
	if family.CanonicalID != "" {
		ids = append(ids, family.CanonicalID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	seenWaivers := make(map[string]struct{})
	waivers := make([]*storage.ReleaseSLAWaiver, 0)
	seenReleaseIDs := make(map[string]struct{})
	for _, releaseID := range ids {
		if releaseID == "" {
			continue
		}
		if _, ok := seenReleaseIDs[releaseID]; ok {
			continue
		}
		seenReleaseIDs[releaseID] = struct{}{}
		listed, err := s.listReleaseSLAWaivers(releaseID)
		if err != nil {
			return nil, err
		}
		for _, waiver := range listed {
			if waiver == nil {
				continue
			}
			if _, ok := seenWaivers[waiver.ID]; ok {
				continue
			}
			seenWaivers[waiver.ID] = struct{}{}
			projected := *waiver
			projected.ReleaseID = firstNonEmpty(family.CanonicalID, waiver.ReleaseID)
			waivers = append(waivers, &projected)
		}
	}
	sort.SliceStable(waivers, func(left, right int) bool {
		return waivers[left].CreatedAt.After(waivers[right].CreatedAt)
	})
	return waivers, nil
}

func (s *Server) releaseFamilyAuditTrail(family resolvedReleaseFamily) []*storage.AuditEvent {
	if s.store == nil {
		return nil
	}
	ids := family.memberIDs()
	ids = append(ids, family.CanonicalID)
	seenEvents := make(map[string]struct{})
	events := make([]*storage.AuditEvent, 0)
	seenReleaseIDs := make(map[string]struct{})
	for _, releaseID := range ids {
		releaseID = strings.TrimSpace(releaseID)
		if releaseID == "" {
			continue
		}
		if _, ok := seenReleaseIDs[releaseID]; ok {
			continue
		}
		seenReleaseIDs[releaseID] = struct{}{}
		listed, err := s.store.ListAuditEvents(storage.AuditEventFilter{
			ResourceType: "release",
			ResourceID:   releaseID,
			Limit:        20,
		})
		if err != nil {
			continue
		}
		for _, event := range listed {
			if event == nil {
				continue
			}
			if _, ok := seenEvents[event.ID]; ok {
				continue
			}
			seenEvents[event.ID] = struct{}{}
			events = append(events, event)
		}
	}
	sort.SliceStable(events, func(left, right int) bool {
		return events[left].CreatedAt.After(events[right].CreatedAt)
	})
	if len(events) > 20 {
		events = events[:20]
	}
	return events
}

func managedTestCompletedAt(test *ManagedTest) time.Time {
	if test == nil {
		return time.Time{}
	}
	if test.EndedAt != nil {
		return *test.EndedAt
	}
	if test.StartedAt != nil {
		return *test.StartedAt
	}
	return test.CreatedAt
}
