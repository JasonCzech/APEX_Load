package api

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errApplicationProjectMismatch = errors.New("application does not belong to project")

type projectReleaseFamilyCreator interface {
	CreateProjectReleaseFamily(projectID, name, preferredApplicationID string, createdAt time.Time) (*storage.Release, error)
}

type releaseFamilyMemberLister interface {
	ListReleaseFamilyMembers(id string) ([]*storage.Release, error)
}

func projectReleaseFamilyProjection(preferred *storage.Release, members []*storage.Release) *storage.Release {
	if preferred == nil {
		return nil
	}
	projected := *preferred
	projected.ID = firstNonEmpty(preferred.GroupID, preferred.ID)
	projected.GroupID = projected.ID
	projected.ApplicationReleaseID = preferred.ID
	projected.MemberIDs = make(map[string]string, len(members))
	for _, member := range members {
		if member != nil {
			projected.MemberIDs[member.ApplicationID] = member.ID
		}
	}
	return &projected
}

func (s *Server) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req struct {
		Name      string `json:"name"`
		ProjectID string `json:"project_id,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "application name is required")
		return
	}
	application := &storage.Application{ID: uuid.New().String(), ProjectID: normalizeProjectID(req.ProjectID), Name: strings.TrimSpace(req.Name), CreatedAt: time.Now()}
	if err := s.store.SaveApplication(application); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "application.create", "success", "application", application.ID, application.ProjectID, map[string]any{"name": application.Name})
	writeJSON(w, http.StatusCreated, application)
}

func (s *Server) handleListApplications(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"applications": []*storage.Application{}, "count": 0})
		return
	}
	applications, err := s.store.ListApplications()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Application.ProjectID is optional; normalizeProjectID maps an unset one to
	// the default project, so a project-less application follows that project's
	// visibility rather than becoming invisible to everyone.
	_, visibleProjects, unrestricted, err := s.visibleScopeForRequest(r, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !unrestricted {
		filtered := make([]*storage.Application, 0, len(applications))
		for _, application := range applications {
			if application == nil {
				continue
			}
			if _, ok := visibleProjects[normalizeProjectID(application.ProjectID)]; ok {
				filtered = append(filtered, application)
			}
		}
		applications = filtered
	}
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		normalizedProjectID := normalizeProjectID(projectID)
		filtered := make([]*storage.Application, 0, len(applications))
		for _, application := range applications {
			if normalizeProjectID(application.ProjectID) == normalizedProjectID {
				filtered = append(filtered, application)
			}
		}
		applications = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": applications, "count": len(applications)})
}

func (s *Server) handleGetApplication(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	application, err := s.store.GetApplication(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	writeJSON(w, http.StatusOK, application)
}

func (s *Server) handleUpdateApplication(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	application, err := s.store.GetApplication(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	var req struct {
		Name      string    `json:"name"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.UpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "updated_at is required")
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		application.Name = strings.TrimSpace(req.Name)
	}
	updater, supported := s.store.(interface {
		UpdateApplicationIfUnchanged(application *storage.Application, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic application updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateApplicationIfUnchanged(application, req.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "application changed while the update was in flight; reload and retry")
		return
	}
	s.recordAuditEvent(r, "application.update", "success", "application", application.ID, application.ProjectID, map[string]any{"name": application.Name})
	writeJSON(w, http.StatusOK, application)
}

func (s *Server) handleDeleteApplication(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	name := ""
	if application, err := s.store.GetApplication(id); err == nil && application != nil {
		projectID = application.ProjectID
		name = application.Name
	}
	cleanupNames := []string{}
	cleanupProviders := []string{}
	if projectID != "" {
		listed := []secretstore.Metadata{}
		var err error
		var records *storage.SQLiteStore
		switch store := s.store.(type) {
		case *storage.SQLiteStore:
			records = store
		case *storage.PostgresStore:
			records = store.SQLiteStore
		}
		if records != nil {
			var indexed []storage.ProjectSecretMetadata
			indexed, err = (storage.ApplicationSecretStorage{SQLiteStore: records}).ListExternalProjectSecretMetadata(storage.ApplicationSecretScopeID(id))
			for _, item := range indexed {
				listed = append(listed, secretstore.Metadata{Name: item.SecretName, Provider: item.Provider})
			}
		} else if s.applicationSecretStore != nil {
			listed, err = s.applicationSecretStore.List(r.Context(), storage.ApplicationSecretScopeID(id))
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "application secret metadata is unavailable")
			return
		}
		providers := map[string]struct{}{}
		for _, metadata := range listed {
			if metadata.Provider == "encrypted_database" || metadata.Provider == "environment_read_only" {
				continue
			}
			cleanupNames = append(cleanupNames, metadata.Name)
			providers[metadata.Provider] = struct{}{}
		}
		for provider := range providers {
			cleanupProviders = append(cleanupProviders, provider)
		}
		sort.Strings(cleanupNames)
		sort.Strings(cleanupProviders)
	}
	if err := s.store.DeleteApplication(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "application.delete", "success", "application", id, projectID, map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id,
		"external_secret_cleanup_required":  len(cleanupNames) > 0,
		"external_secret_cleanup_names":     cleanupNames,
		"external_secret_cleanup_providers": cleanupProviders,
		"external_secret_cleanup_prefix":    engine.ProjectSecretBindingPrefix(storage.ApplicationSecretScopeID(id))})
}

func (s *Server) handleCreateRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req struct {
		Name      string `json:"name"`
		ProjectID string `json:"project_id,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "release name is required")
		return
	}
	application, err := s.store.GetApplication(r.PathValue("id"))
	if err != nil || application == nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	projectID := normalizeProjectID(application.ProjectID)
	if requested := strings.TrimSpace(req.ProjectID); requested != "" && normalizeProjectID(requested) != projectID {
		writeError(w, http.StatusBadRequest, "project_id does not match application project")
		return
	}
	creator, supported := s.store.(projectReleaseFamilyCreator)
	if !supported {
		writeError(w, http.StatusNotImplemented, "project-wide release creation is not supported by this storage backend")
		return
	}
	release, err := creator.CreateProjectReleaseFamily(projectID, strings.TrimSpace(req.Name), application.ID, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "release.create", "success", "release", firstNonEmpty(release.GroupID, release.ID), projectID, map[string]any{"name": release.Name, "application_id": release.ApplicationID})
	writeJSON(w, http.StatusCreated, release)
}

func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"releases": []*storage.Release{}, "count": 0})
		return
	}
	includeArchived := strings.EqualFold(r.URL.Query().Get("include_archived"), "true")
	releases, err := s.store.ListReleases(r.PathValue("id"), includeArchived)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": releases, "count": len(releases)})
}

func (s *Server) handleCreateProjectRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	projectID := normalizeProjectID(r.PathValue("id"))
	if _, err := s.store.GetProject(projectID); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	var req struct {
		Name          string `json:"name"`
		ApplicationID string `json:"application_id,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "release name is required")
		return
	}
	applicationID, err := s.resolveReleaseApplicationID(projectID, strings.TrimSpace(req.ApplicationID))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	creator, supported := s.store.(projectReleaseFamilyCreator)
	if !supported {
		writeError(w, http.StatusNotImplemented, "project-wide release creation is not supported by this storage backend")
		return
	}
	release, err := creator.CreateProjectReleaseFamily(projectID, strings.TrimSpace(req.Name), applicationID, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	members := []*storage.Release{release}
	if lister, ok := s.store.(releaseFamilyMemberLister); ok {
		if listed, listErr := lister.ListReleaseFamilyMembers(release.ID); listErr == nil {
			members = listed
		}
	}
	family := projectReleaseFamilyProjection(release, members)
	s.recordAuditEvent(r, "release.create", "success", "release", family.ID, projectID, map[string]any{"name": release.Name, "application_id": release.ApplicationID, "project_scoped": true})
	writeJSON(w, http.StatusCreated, family)
}

func (s *Server) handleListProjectReleases(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"releases": []*storage.Release{}, "count": 0})
		return
	}
	projectID := normalizeProjectID(r.PathValue("id"))
	includeArchived := strings.EqualFold(r.URL.Query().Get("include_archived"), "true")
	releases, err := s.store.ListReleasesByProject(projectID, includeArchived)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": releases, "count": len(releases)})
}

func (s *Server) resolveReleaseApplicationID(projectID string, requestedApplicationID string) (string, error) {
	if requestedApplicationID != "" {
		application, err := s.store.GetApplication(requestedApplicationID)
		if err != nil {
			return "", err
		}
		if normalizeProjectID(application.ProjectID) != normalizeProjectID(projectID) {
			return "", errApplicationProjectMismatch
		}
		return application.ID, nil
	}
	applications, err := s.store.ListApplications()
	if err != nil {
		return "", err
	}
	for _, application := range applications {
		if normalizeProjectID(application.ProjectID) == normalizeProjectID(projectID) {
			return application.ID, nil
		}
	}
	// Project-scoped release creation may race across HTTP requests. Use a stable
	// identifier for the implicit repository application so concurrent creators
	// converge on the same row instead of fragmenting releases across duplicate
	// default applications.
	applicationID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:project-application:"+normalizeProjectID(projectID))).String()
	now := time.Now()
	application := &storage.Application{
		ID:        applicationID,
		ProjectID: projectID,
		Name:      "Application Repository",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.SaveApplication(application); err != nil {
		return "", err
	}
	// SaveApplication deliberately keeps project ownership immutable on conflict.
	// Reload and verify the deterministic row so a corrupted/pre-existing ID can
	// never bind a release across projects.
	persisted, err := s.store.GetApplication(applicationID)
	if err != nil {
		return "", err
	}
	if normalizeProjectID(persisted.ProjectID) != normalizeProjectID(projectID) {
		return "", errApplicationProjectMismatch
	}
	return persisted.ID, nil
}

func (s *Server) handleGetRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	release, err := s.store.GetRelease(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	writeJSON(w, http.StatusOK, release)
}

func (s *Server) handleUpdateRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	release, err := s.store.GetRelease(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	var req struct {
		Name      string    `json:"name"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.UpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "updated_at is required")
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		release.Name = strings.TrimSpace(req.Name)
	}
	updater, supported := s.store.(interface {
		UpdateReleaseIfUnchanged(release *storage.Release, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic release updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateReleaseIfUnchanged(release, req.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "release changed while the update was in flight; reload and retry")
		return
	}
	projectID := normalizeProjectID(release.ProjectID)
	s.recordAuditEvent(r, "release.update", "success", "release", firstNonEmpty(release.GroupID, release.ID), projectID, map[string]any{"name": release.Name, "application_id": release.ApplicationID})
	writeJSON(w, http.StatusOK, release)
}

func (s *Server) handleDeleteRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	name := ""
	applicationID := ""
	canonicalID := id
	if release, err := s.store.GetRelease(id); err == nil && release != nil {
		name = release.Name
		applicationID = release.ApplicationID
		projectID = normalizeProjectID(release.ProjectID)
		canonicalID = firstNonEmpty(release.GroupID, release.ID)
	}
	if err := s.store.DeleteRelease(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "release.delete", "success", "release", canonicalID, projectID, map[string]any{"name": name, "application_id": applicationID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleArchiveRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	if err := s.store.ArchiveRelease(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	release, err := s.store.GetRelease(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	projectID := normalizeProjectID(release.ProjectID)
	s.recordAuditEvent(r, "release.archive", "success", "release", firstNonEmpty(release.GroupID, release.ID), projectID, map[string]any{"name": release.Name, "application_id": release.ApplicationID})
	writeJSON(w, http.StatusOK, release)
}
