package api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
	"github.com/jasonczech/apexload/pkg/storage"
)

var recordingProfileAPIRouteCatalog = apiRouteCatalog{Domain: "recording-profiles", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/recording-profiles", "handleGetRecordingProfiles", viewerProjectAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetRecordingProfiles) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/projects/{id}/recording-profiles", "handlePutRecordingProfiles", editorProjectAccessWithBodyLimit(resolveProjectIDFromPath, 1<<20), func(s *Server) http.Handler { return http.HandlerFunc(s.handlePutRecordingProfiles) }),
}}

// recordingProfilesResponse is a project's recording profiles and the
// correlation rules a profile can turn off.
type recordingProfilesResponse struct {
	Profiles  []recordingprofile.Profile `json:"profiles"`
	Rules     []correlate.RuleInfo       `json:"rules"`
	UpdatedAt time.Time                  `json:"updated_at"`
}

type recordingProfilesRequest struct {
	Profiles []recordingprofile.Profile `json:"profiles"`
	// ExpectedUpdatedAt, when set, must match the project's updated_at, so
	// two editors cannot overwrite each other.
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

// handleGetRecordingProfiles godoc
// @Summary List a project's recording profiles
// @Description Saved recording options (headers, filters, think-time threshold, resources, script level, checks, async scan, correlation settings) the proxy and browser recorders and HAR imports apply.
// @Tags recording-profiles
// @Produce json
// @Param id path string true "Project ID"
// @Success 200 {object} recordingProfilesResponse
// @Failure 404 {object} map[string]string
// @Router /api/v1/projects/{id}/recording-profiles [get]
func (s *Server) handleGetRecordingProfiles(w http.ResponseWriter, r *http.Request) {
	project, ok := s.recordingProfileProject(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, recordingProfilesBody(project))
}

// handlePutRecordingProfiles godoc
// @Summary Replace a project's recording profiles
// @Tags recording-profiles
// @Accept json
// @Produce json
// @Param id path string true "Project ID"
// @Param body body recordingProfilesRequest true "Profiles"
// @Success 200 {object} recordingProfilesResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /api/v1/projects/{id}/recording-profiles [put]
func (s *Server) handlePutRecordingProfiles(w http.ResponseWriter, r *http.Request) {
	project, ok := s.recordingProfileProject(w, r)
	if !ok {
		return
	}
	var req recordingProfilesRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Profiles == nil {
		req.Profiles = []recordingprofile.Profile{}
	}
	if err := recordingprofile.Validate(req.Profiles); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ExpectedUpdatedAt != nil && !req.ExpectedUpdatedAt.Equal(project.UpdatedAt) {
		writeError(w, http.StatusConflict, "the project changed since it was read; reload and retry")
		return
	}
	project.Settings.RecordingProfiles = req.Profiles
	updater, supported := s.store.(interface {
		UpdateProjectIfUnchanged(project *storage.Project, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic project updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateProjectIfUnchanged(project, project.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "project changed while the update was in flight; reload and retry")
		return
	}
	if fresh, err := s.store.GetProject(project.ID); err == nil {
		project = fresh
	}
	s.recordAuditEvent(r, "project.recording_profiles.update", "success", "project", project.ID, project.ID, map[string]any{"profiles": len(req.Profiles)})
	writeJSON(w, http.StatusOK, recordingProfilesBody(project))
}

func (s *Server) recordingProfileProject(w http.ResponseWriter, r *http.Request) (*storage.Project, bool) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return nil, false
	}
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "project not found")
		return nil, false
	}
	return project, true
}

func recordingProfilesBody(project *storage.Project) recordingProfilesResponse {
	profiles := project.Settings.RecordingProfiles
	if profiles == nil {
		profiles = []recordingprofile.Profile{}
	}
	return recordingProfilesResponse{Profiles: profiles, Rules: correlate.RuleCatalog(), UpdatedAt: project.UpdatedAt}
}

// projectRecordingProfile resolves a recording profile by ID in a project.
// An empty ID means none.
func (s *Server) projectRecordingProfile(projectID, profileID string) (*recordingprofile.Profile, error) {
	if profileID == "" {
		return nil, nil
	}
	if s.store == nil || projectID == "" {
		return nil, errors.New("recording profiles need a project")
	}
	project, err := s.store.GetProject(projectID)
	if err != nil {
		return nil, errors.New("project not found")
	}
	profile, ok := recordingprofile.Find(project.Settings.RecordingProfiles, profileID)
	if !ok {
		return nil, errors.New("recording profile " + profileID + " not found in the project")
	}
	return &profile, nil
}
