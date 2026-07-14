package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type projectRequest struct {
	Name       string                  `json:"name"`
	DomainID   *string                 `json:"domain_id,omitempty"`
	CategoryID *string                 `json:"category_id,omitempty"`
	Settings   storage.ProjectSettings `json:"settings,omitempty"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	var req projectRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	if err := validateProjectSettings(req.Settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	domainID, categoryID, err := s.resolveProjectPlacement(stringPtrValue(req.DomainID), stringPtrValue(req.CategoryID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project placement: "+err.Error())
		return
	}
	now := time.Now()
	project := &storage.Project{ID: uuid.New().String(), DomainID: domainID, CategoryID: categoryID, Name: strings.TrimSpace(req.Name), Settings: req.Settings, CreatedAt: now, UpdatedAt: now}
	if err := s.store.SaveProject(project); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuditEvent(r, "project.create", "success", "project", project.ID, project.ID, map[string]any{"name": project.Name})
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": []*storage.Project{{ID: storage.DefaultProjectID, DomainID: storage.DefaultDomainID, CategoryID: storage.DefaultRepositoryCategoryID, Name: "Unassigned", CreatedAt: time.Time{}, UpdatedAt: time.Time{}}},
			"count":    1,
		})
		return
	}
	projects, err := s.store.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if domainID := strings.TrimSpace(r.URL.Query().Get("domain_id")); domainID != "" {
		normalizedDomainID := normalizeDomainID(domainID)
		filtered := make([]*storage.Project, 0, len(projects))
		for _, project := range projects {
			if normalizeDomainID(project.DomainID) == normalizedDomainID {
				filtered = append(filtered, project)
			}
		}
		projects = filtered
	}
	if categoryID := strings.TrimSpace(r.URL.Query().Get("category_id")); categoryID != "" {
		normalizedCategoryID := normalizeRepositoryCategoryID(categoryID)
		filtered := make([]*storage.Project, 0, len(projects))
		for _, project := range projects {
			if normalizeRepositoryCategoryID(project.CategoryID) == normalizedCategoryID {
				filtered = append(filtered, project)
			}
		}
		projects = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects, "count": len(projects)})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if err == sql.ErrNoRows {
			status = http.StatusNotFound
		}
		writeError(w, status, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	var req projectRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	if err := validateProjectSettings(req.Settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DomainID != nil || req.CategoryID != nil {
		domainID := project.DomainID
		categoryID := project.CategoryID
		if req.DomainID != nil {
			domainID = *req.DomainID
		}
		if req.CategoryID != nil {
			categoryID = *req.CategoryID
		}
		resolvedDomainID, resolvedCategoryID, err := s.resolveProjectPlacement(domainID, categoryID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid project placement: "+err.Error())
			return
		}
		project.DomainID = resolvedDomainID
		project.CategoryID = resolvedCategoryID
	}
	project.Name = strings.TrimSpace(req.Name)
	project.Settings = req.Settings
	if err := s.store.SaveProject(project); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.runner.ReconcilePendingTests()
	s.recordAuditEvent(r, "project.update", "success", "project", project.ID, project.ID, map[string]any{"name": project.Name, "max_concurrent_tests": project.Settings.MaxConcurrentTests, "max_vusers": project.Settings.MaxVUsers})
	writeJSON(w, http.StatusOK, project)
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateProjectSettings(settings storage.ProjectSettings) error {
	if settings.MaxConcurrentTests < 0 {
		return errProjectSettings("max_concurrent_tests must be greater than or equal to 0")
	}
	if settings.MaxVUsers < 0 {
		return errProjectSettings("max_vusers must be greater than or equal to 0")
	}
	if settings.MaxVUsersPerTest < 0 {
		return errProjectSettings("max_vusers_per_test must be greater than or equal to 0")
	}
	if settings.MaxTestDurationMinutes < 0 {
		return errProjectSettings("max_test_duration_minutes must be greater than or equal to 0")
	}
	// Per-pool quotas mirror the project-wide caps: reject negatives (which would
	// otherwise fall through the >0 admission guards and silently disable the cap —
	// fail-open, the opposite of the operator's intent) and blank pool keys.
	for poolID, quota := range settings.PoolQuotas {
		if strings.TrimSpace(poolID) == "" {
			return errProjectSettings("pool_quotas keys must be a non-empty fleet pool id")
		}
		if quota.MaxConcurrentTests < 0 {
			return errProjectSettings("pool_quotas.max_concurrent_tests must be greater than or equal to 0")
		}
		if quota.MaxVUsers < 0 {
			return errProjectSettings("pool_quotas.max_vusers must be greater than or equal to 0")
		}
	}
	if settings.GitHub.Enabled {
		for _, repo := range settings.GitHub.AllowedRepos {
			if strings.TrimSpace(repo) == "" {
				continue
			}
			if _, _, ok := splitOwnerRepo(repo); !ok && !strings.Contains(repo, "*") {
				return errProjectSettings("github.allowed_repos entries must be owner/repo or a glob")
			}
		}
		if settings.GitHub.AppInstallationID < 0 {
			return errProjectSettings("github.app_installation_id must be greater than or equal to 0")
		}
		if !settings.GitHub.AllowDirectCommit && !settings.GitHub.RequirePullRequest {
			settings.GitHub.RequirePullRequest = true
		}
	}
	if len(settings.RedactionRules) == 0 {
		return nil
	}
	if _, err := engine.NewRedactionConfig(settings.RedactionRules); err != nil {
		return err
	}
	return nil
}

func errProjectSettings(message string) error {
	return &projectSettingsError{message: message}
}

type projectSettingsError struct {
	message string
}

func (e *projectSettingsError) Error() string {
	return e.message
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	s.projectMutationMu.Lock()
	defer s.projectMutationMu.Unlock()
	id := r.PathValue("id")
	project, err := s.store.GetProject(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id = project.ID
	domainID := project.DomainID
	if s.runner != nil {
		if err := s.runner.beginProjectDeletion(id); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		defer s.runner.endProjectDeletion(id)
	}
	tests, err := s.store.ListTests(id, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enumerate project tests: "+err.Error())
		return
	}
	testIDs := make(map[string]struct{}, len(tests))
	archiveByTest := make(map[string]*storage.RunArchiveRecord, len(tests))
	for _, test := range tests {
		if test == nil {
			continue
		}
		testIDs[test.ID] = struct{}{}
		record, err := s.store.GetRunArchive(test.ID)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			writeError(w, http.StatusInternalServerError, "failed to load project archive: "+err.Error())
			return
		}
		if record != nil {
			archiveByTest[test.ID] = record
		}
	}
	if outbox := s.runner.archiveFinalizationOutbox(); outbox != nil {
		jobs, err := outbox.ListArchiveFinalizationJobs(10000)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enumerate project archive finalizations: "+err.Error())
			return
		}
		for _, job := range jobs {
			if job == nil || normalizeProjectID(job.ProjectID) != normalizeProjectID(id) {
				continue
			}
			testIDs[job.TestID] = struct{}{}
			if _, exists := archiveByTest[job.TestID]; !exists {
				record := job.ArchiveRecord
				archiveByTest[job.TestID] = &record
			}
		}
	}
	if outbox := s.runner.archiveRefreshOutbox(); outbox != nil {
		jobs, err := outbox.ListArchiveRefreshJobs(10000)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enumerate project archive refreshes: "+err.Error())
			return
		}
		for _, job := range jobs {
			if job != nil && normalizeProjectID(job.ProjectID) == normalizeProjectID(id) {
				testIDs[job.TestID] = struct{}{}
			}
		}
	}
	orderedTestIDs := make([]string, 0, len(testIDs))
	for testID := range testIDs {
		orderedTestIDs = append(orderedTestIDs, testID)
	}
	sort.Strings(orderedTestIDs)
	archiveUnlocks := make([]func(), 0, len(orderedTestIDs))
	for _, testID := range orderedTestIDs {
		archiveUnlocks = append(archiveUnlocks, s.runner.lockArchiveCommitFence(testID))
	}
	defer func() {
		for index := len(archiveUnlocks) - 1; index >= 0; index-- {
			archiveUnlocks[index]()
		}
	}()
	if !s.runner.isExecutionLeader() {
		writeError(w, http.StatusServiceUnavailable, errNotExecutionLeader.Error())
		return
	}
	archiveRecords := make([]*storage.RunArchiveRecord, 0, len(archiveByTest))
	for _, testID := range orderedTestIDs {
		if record := archiveByTest[testID]; record != nil {
			archiveRecords = append(archiveRecords, record)
		}
	}
	// Delete the relational project graph atomically before making irreversible
	// object-store changes. If the database transaction fails, every archive is
	// still intact. Physical cleanup happens from the records captured above.
	if err := s.store.DeleteProject(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.runner != nil {
		s.runner.forgetProject(id)
	}
	cleanupErrors := make([]string, 0)
	for _, record := range archiveRecords {
		if err := s.cleanupQueuedArchiveUnderFence(record); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Sprintf("%s: %v", record.TestID, err))
		}
	}
	if len(cleanupErrors) > 0 {
		s.recordAuditEventWithProjectSnapshot(r, "project.delete", "partial", "project", id, id, domainID, map[string]any{"archive_cleanup_errors": cleanupErrors})
		writeError(w, http.StatusInternalServerError, "project metadata was deleted but physical archive cleanup was incomplete: "+strings.Join(cleanupErrors, "; "))
		return
	}
	s.recordAuditEventWithProjectSnapshot(r, "project.delete", "success", "project", id, id, domainID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}
