package api

// access_resolvers.go — split from middleware.go (same package, behavior-preserving).
// requestProjectLookup resolvers: derive the project/domain/repository scope of a
// request for project-scoped RBAC enforcement.

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/storage"
)

func resolveFolderProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "folder storage is not configured"
	}
	folder, err := store.GetFolder(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "folder not found"
	}
	return normalizeProjectID(folder.ProjectID), 0, ""
}

func resolveProjectIDFromQuery(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveDomainIDFromPath(r *http.Request, _ storage.Store) (string, int, string) {
	return normalizeDomainID(r.PathValue("id")), 0, ""
}

func resolveRepositoryIDFromDomainPath(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "repository storage is not configured"
	}
	repository, err := store.GetApplicationRepository(normalizeDomainID(r.PathValue("id")))
	if err != nil {
		return "", http.StatusNotFound, "application repository not found"
	}
	return strings.TrimSpace(repository.ID), 0, ""
}

func resolveCategoryDomainID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "repository category storage is not configured"
	}
	category, err := store.GetRepositoryCategory(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "category not found"
	}
	return normalizeDomainID(category.DomainID), 0, ""
}

func resolveCategoryRepositoryID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "repository category storage is not configured"
	}
	category, err := store.GetRepositoryCategory(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "category not found"
	}
	return strings.TrimSpace(category.RepositoryID), 0, ""
}

func resolveProjectCreateDomainID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		DomainID   string `json:"domain_id"`
		CategoryID string `json:"category_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil && err != io.EOF {
		return "", http.StatusBadRequest, "invalid request body"
	}
	if strings.TrimSpace(req.DomainID) != "" {
		return normalizeDomainID(req.DomainID), 0, ""
	}
	if strings.TrimSpace(req.CategoryID) != "" && store != nil {
		category, err := store.GetRepositoryCategory(req.CategoryID)
		if err != nil {
			return "", http.StatusNotFound, "category not found"
		}
		return normalizeDomainID(category.DomainID), 0, ""
	}
	return storage.DefaultDomainID, 0, ""
}

func resolveProjectCreateRepositoryID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "repository storage is not configured"
	}
	var req struct {
		DomainID   string `json:"domain_id"`
		CategoryID string `json:"category_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil && err != io.EOF {
		return "", http.StatusBadRequest, "invalid request body"
	}
	if strings.TrimSpace(req.CategoryID) != "" {
		category, err := store.GetRepositoryCategory(req.CategoryID)
		if err != nil {
			return "", http.StatusNotFound, "category not found"
		}
		return strings.TrimSpace(category.RepositoryID), 0, ""
	}
	repository, err := store.GetApplicationRepository(normalizeDomainID(req.DomainID))
	if err != nil {
		return "", http.StatusNotFound, "application repository not found"
	}
	return strings.TrimSpace(repository.ID), 0, ""
}

func resolveProjectDomainIDFromPath(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "project storage is not configured"
	}
	project, err := store.GetProject(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "project not found"
	}
	return normalizeDomainID(project.DomainID), 0, ""
}

func resolveProjectRepositoryIDFromPath(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "project storage is not configured"
	}
	project, err := store.GetProject(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "project not found"
	}
	if strings.TrimSpace(project.CategoryID) != "" {
		category, err := store.GetRepositoryCategory(project.CategoryID)
		if err == nil && category != nil {
			return strings.TrimSpace(category.RepositoryID), 0, ""
		}
	}
	repository, err := store.GetApplicationRepository(normalizeDomainID(project.DomainID))
	if err != nil {
		return "", http.StatusNotFound, "application repository not found"
	}
	return strings.TrimSpace(repository.ID), 0, ""
}

func resolveApplicationListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveApplicationCreateProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body"
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveApplicationProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "application storage is not configured"
	}
	application, err := store.GetApplication(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "application not found"
	}
	return normalizeProjectID(application.ProjectID), 0, ""
}

func resolveReleaseProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "release storage is not configured"
	}
	release, err := store.GetRelease(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "release not found"
	}
	if strings.TrimSpace(release.ProjectID) != "" {
		return normalizeProjectID(release.ProjectID), 0, ""
	}
	application, err := store.GetApplication(release.ApplicationID)
	if err != nil {
		return "", http.StatusNotFound, "application not found"
	}
	return normalizeProjectID(application.ProjectID), 0, ""
}

func resolveScriptProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "script storage is not configured"
	}
	script, err := store.GetScript(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "script not found"
	}
	return normalizeProjectID(script.ProjectID), 0, ""
}

func resolveProjectIDFromPath(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "project storage is not configured"
	}
	id := normalizeProjectID(r.PathValue("id"))
	if _, err := store.GetProject(id); err != nil {
		return "", http.StatusNotFound, "project not found"
	}
	return id, 0, ""
}

func resolveScriptListProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	if !r.URL.Query().Has("folder_id") {
		return "", http.StatusBadRequest, "project_id or folder_id is required for project-scoped script access"
	}
	return resolveFolderProjectIDValue(r.URL.Query().Get("folder_id"), store)
}

func resolveTestProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "test storage is not configured"
	}
	test, err := store.GetTest(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "test not found"
	}
	return normalizeProjectID(test.ProjectID), 0, ""
}

func resolveTestListProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	if !r.URL.Query().Has("folder_id") {
		return "", http.StatusBadRequest, "project_id or folder_id is required for project-scoped test access"
	}
	return resolveFolderProjectIDValue(r.URL.Query().Get("folder_id"), store)
}

func resolveTestContextProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "test storage is not configured"
	}
	resolvedProjectID := ""
	mergeProjectID := func(candidate string) (int, string) {
		normalized := normalizeProjectID(candidate)
		if resolvedProjectID == "" {
			resolvedProjectID = normalized
			return 0, ""
		}
		if resolvedProjectID != normalized {
			return http.StatusBadRequest, "test context filters must belong to the same project"
		}
		return 0, ""
	}
	if applicationID := strings.TrimSpace(r.URL.Query().Get("application_id")); applicationID != "" {
		application, err := store.GetApplication(applicationID)
		if err != nil {
			return "", http.StatusNotFound, "application not found"
		}
		if status, msg := mergeProjectID(application.ProjectID); status != 0 {
			return "", status, msg
		}
	}
	if releaseID := strings.TrimSpace(r.URL.Query().Get("release_id")); releaseID != "" {
		releaseReq := r.Clone(r.Context())
		releaseReq.SetPathValue("id", releaseID)
		releaseProjectID, status, msg := resolveReleaseProjectID(releaseReq, store)
		if status != 0 {
			return "", status, msg
		}
		if status, msg := mergeProjectID(releaseProjectID); status != 0 {
			return "", status, msg
		}
	}
	if scenarioID := strings.TrimSpace(r.URL.Query().Get("scenario_id")); scenarioID != "" {
		scenarioReq := r.Clone(r.Context())
		scenarioReq.SetPathValue("id", scenarioID)
		scenarioProjectID, status, msg := resolveScenarioProjectID(scenarioReq, store)
		if status != 0 {
			return "", status, msg
		}
		if status, msg := mergeProjectID(scenarioProjectID); status != 0 {
			return "", status, msg
		}
	}
	if resolvedProjectID == "" {
		return "", http.StatusBadRequest, "application_id, release_id, or scenario_id is required"
	}
	return resolvedProjectID, 0, ""
}

func resolveTestCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req CreateTestRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}

	requestedProjectID := normalizeProjectID(req.ProjectID)
	folderProjectID := ""
	if req.FolderID != nil {
		projectID, status, msg := resolveFolderProjectIDValue(*req.FolderID, store)
		if status != 0 {
			return "", status, msg
		}
		folderProjectID = projectID
		if req.ProjectID != "" && requestedProjectID != folderProjectID {
			return "", http.StatusBadRequest, "project_id does not match folder project"
		}
	}

	inferredProjectID, status, msg := resolveCreateTestScriptProjectID(req, store)
	if status != 0 {
		return "", status, msg
	}
	if inferredProjectID != "" {
		if req.ProjectID != "" && requestedProjectID != inferredProjectID {
			return "", http.StatusBadRequest, "project_id does not match test scripts"
		}
		if folderProjectID != "" && folderProjectID != inferredProjectID {
			return "", http.StatusBadRequest, "folder project does not match test scripts"
		}
	}

	if folderProjectID != "" {
		return folderProjectID, 0, ""
	}
	if req.ProjectID != "" {
		return requestedProjectID, 0, ""
	}
	if inferredProjectID != "" {
		return inferredProjectID, 0, ""
	}
	return normalizeProjectID(""), 0, ""
}

func resolveCreateTestScriptProjectID(req CreateTestRequest, store storage.Store) (string, int, string) {
	scriptIDs := make([]string, 0, len(req.Groups)+1)
	if req.ScriptID != "" {
		scriptIDs = append(scriptIDs, req.ScriptID)
	}
	for _, group := range req.Groups {
		if group.ScriptID != "" {
			scriptIDs = append(scriptIDs, group.ScriptID)
		}
	}
	projectID := ""
	for _, scriptID := range scriptIDs {
		script, err := store.GetScript(scriptID)
		if err != nil {
			return "", http.StatusNotFound, "script not found"
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "test scripts span multiple projects"
		}
	}
	return projectID, 0, ""
}

func resolveTestMoveProjectID(r *http.Request, store storage.Store) (string, int, string) {
	projectID, status, msg := resolveTestProjectID(r, store)
	if status != 0 {
		return "", status, msg
	}
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.FolderID == nil {
		return projectID, 0, ""
	}
	destinationProjectID, destStatus, destMsg := resolveFolderProjectIDValue(*req.FolderID, store)
	if destStatus != 0 {
		return "", destStatus, destMsg
	}
	if destinationProjectID != projectID {
		return "", http.StatusBadRequest, "destination folder belongs to a different project"
	}
	return projectID, 0, ""
}

func resolveScenarioProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "scenario storage is not configured"
	}
	_, scripts, err := store.GetScenario(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "scenario not found"
	}
	return resolveProjectIDFromScenarioScripts(scripts, store)
}

func resolveScenarioListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped scenario access"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveScenarioCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req CreateScenarioRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	scripts := make([]storage.ScenarioScript, 0, len(req.Groups))
	for index, group := range req.Groups {
		scripts = append(scripts, storage.ScenarioScript{Order: index, ScriptID: group.ScriptID})
	}
	return resolveProjectIDFromScenarioScripts(scripts, store)
}

func resolveProjectIDFromScenarioScripts(scripts []storage.ScenarioScript, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "script storage is not configured"
	}
	projectID := ""
	for _, scenarioScript := range scripts {
		if strings.TrimSpace(scenarioScript.ScriptID) == "" {
			continue
		}
		script, err := store.GetScript(scenarioScript.ScriptID)
		if err != nil {
			return "", http.StatusNotFound, "script not found"
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "scenario scripts span multiple projects"
		}
	}
	if projectID == "" {
		return normalizeProjectID(""), 0, ""
	}
	return projectID, 0, ""
}

func resolveDraftScriptProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		Script *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.Script == nil {
		return "", http.StatusBadRequest, "script is required"
	}
	return normalizeProjectID(req.Script.ProjectID), 0, ""
}

func resolveBrowserToHTTPPreviewProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID           string `json:"project_id,omitempty"`
		SourceBrowserScript *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"source_browser_script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) != "" {
		return normalizeProjectID(req.ProjectID), 0, ""
	}
	if req.SourceBrowserScript != nil {
		return normalizeProjectID(req.SourceBrowserScript.ProjectID), 0, ""
	}
	return normalizeProjectID(""), 0, ""
}

func resolveAuthoringSessionRequestProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
		Draft     *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"draft,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) != "" {
		return normalizeProjectID(req.ProjectID), 0, ""
	}
	if req.Draft != nil {
		return normalizeProjectID(req.Draft.ProjectID), 0, ""
	}
	return normalizeProjectID(""), 0, ""
}

func resolveAuthoringSessionProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "authoring session storage is not configured"
	}
	sessionStore, ok := store.(interface {
		GetAuthoringSession(id string) (*storage.AuthoringSession, error)
	})
	if !ok {
		return "", http.StatusServiceUnavailable, "authoring session storage is not configured"
	}
	session, err := sessionStore.GetAuthoringSession(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "authoring session not found"
	}
	return normalizeProjectID(session.ProjectID), 0, ""
}

func resolveGoalSizingBaselineProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "test storage is not configured"
	}
	var req struct {
		BaselineTestID string `json:"baseline_test_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	test, err := store.GetTest(strings.TrimSpace(req.BaselineTestID))
	if err != nil {
		return "", http.StatusNotFound, "baseline test not found"
	}
	return normalizeProjectID(test.ProjectID), 0, ""
}

func resolveJMeterSaveProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		Scripts []struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"scripts"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if len(req.Scripts) == 0 {
		return "", http.StatusBadRequest, "at least one script is required"
	}
	projectID := ""
	for _, script := range req.Scripts {
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "imported scripts span multiple projects"
		}
	}
	return projectID, 0, ""
}

func resolvePostmanSaveProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		Mode           string `json:"mode,omitempty"`
		TargetScriptID string `json:"target_script_id,omitempty"`
		Script         *struct {
			ID        string `json:"id,omitempty"`
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.Script == nil {
		return "", http.StatusBadRequest, "script is required"
	}
	targetID := strings.TrimSpace(req.TargetScriptID)
	if targetID == "" && strings.EqualFold(strings.TrimSpace(req.Mode), "replace") {
		targetID = strings.TrimSpace(req.Script.ID)
	}
	if targetID != "" {
		if store == nil {
			if projectID := strings.TrimSpace(req.Script.ProjectID); projectID != "" {
				return normalizeProjectID(projectID), 0, ""
			}
			return "", http.StatusServiceUnavailable, "script storage is not configured"
		}
		script, err := store.GetScript(targetID)
		if err != nil {
			return "", http.StatusNotFound, "target script not found"
		}
		return normalizeProjectID(script.ProjectID), 0, ""
	}
	return normalizeProjectID(req.Script.ProjectID), 0, ""
}

func resolveOpenAPICommitProjectID(r *http.Request, store storage.Store) (string, int, string) {
	return resolvePostmanSaveProjectID(r, store)
}

func resolveAsyncAPICommitProjectID(r *http.Request, store storage.Store) (string, int, string) {
	return resolvePostmanSaveProjectID(r, store)
}

func resolveDataPoolListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped data pool access"
	}
	return normalizeProjectID(projectID), 0, ""
}

// bodyProjectIDResolver returns a resolver that reads {project_id} from the JSON
// request body, returning requiredMsg when it is absent. Used by project-scoped
// create endpoints to keep the decode/validate shape in one place.
func bodyProjectIDResolver(requiredMsg string) func(*http.Request, storage.Store) (string, int, string) {
	return func(r *http.Request, _ storage.Store) (string, int, string) {
		var req struct {
			ProjectID string `json:"project_id,omitempty"`
		}
		if err := decodeRequestBody(r, &req); err != nil {
			return "", http.StatusBadRequest, "invalid request body: " + err.Error()
		}
		if strings.TrimSpace(req.ProjectID) == "" {
			return "", http.StatusBadRequest, requiredMsg
		}
		return normalizeProjectID(req.ProjectID), 0, ""
	}
}

// entityProjectIDResolver returns a resolver that loads an entity by its path {id}
// via getProjectID and returns the owning project. notFoundMsg / loadFailMsg shape
// the standard error responses, keeping the nil-store / ErrNoRows / load-fail shape
// in one place. Used by {id}-scoped access checks.
func entityProjectIDResolver(notFoundMsg, loadFailMsg string, getProjectID func(storage.Store, string) (string, error)) func(*http.Request, storage.Store) (string, int, string) {
	return func(r *http.Request, store storage.Store) (string, int, string) {
		if store == nil {
			return "", http.StatusServiceUnavailable, "storage is not configured"
		}
		projectID, err := getProjectID(store, r.PathValue("id"))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", http.StatusNotFound, notFoundMsg
			}
			return "", http.StatusInternalServerError, loadFailMsg
		}
		return normalizeProjectID(projectID), 0, ""
	}
}

var resolveDataPoolCreateProjectID = bodyProjectIDResolver("project_id is required for project-scoped data pool access")

var resolveDataPoolProjectID = entityProjectIDResolver("data pool not found", "failed to load data pool", func(store storage.Store, id string) (string, error) {
	pool, err := store.GetDataPool(id)
	if err != nil {
		return "", err
	}
	return pool.ProjectID, nil
})

// resolveTimeslotCreateProjectID extracts the owning project from the request
// body for capacity-reservation create/availability calls.
var resolveTimeslotCreateProjectID = bodyProjectIDResolver("project_id is required for project-scoped capacity reservations")

// resolveTimeslotProjectID looks up an existing capacity reservation to find the
// project that owns it, for {id}-scoped access checks.
var resolveTimeslotProjectID = entityProjectIDResolver("timeslot not found", "failed to load timeslot", func(store storage.Store, id string) (string, error) {
	reservation, err := store.GetCapacityReservation(id)
	if err != nil {
		return "", err
	}
	return reservation.ProjectID, nil
})

// resolveExecutionPlanCreateProjectID reads the owning project from the request
// body for execution-plan creation.
var resolveExecutionPlanCreateProjectID = bodyProjectIDResolver("project_id is required for project-scoped execution plans")

// resolveExecutionPlanProjectID looks up an existing execution plan to find the
// project that owns it, for {id}-scoped access checks.
var resolveExecutionPlanProjectID = entityProjectIDResolver("execution plan not found", "failed to load execution plan", func(store storage.Store, id string) (string, error) {
	plan, err := store.GetExecutionPlan(id)
	if err != nil {
		return "", err
	}
	return plan.ProjectID, nil
})

// resolveObjectRepositoryCreateProjectID reads the owning project from the
// request body for object-repository creation.
var resolveObjectRepositoryCreateProjectID = bodyProjectIDResolver("project_id is required for project-scoped object repositories")

// resolveObjectRepositoryProjectID looks up an existing object repository to
// find the project that owns it, for {id}-scoped access checks.
var resolveObjectRepositoryProjectID = entityProjectIDResolver("object repository not found", "failed to load object repository", func(store storage.Store, id string) (string, error) {
	repo, err := store.GetObjectRepository(id)
	if err != nil {
		return "", err
	}
	return repo.ProjectID, nil
})

// resolveObjectRepositoryFromScriptProjectID resolves the project from the
// source script referenced in the seed-from-script request body.
func resolveObjectRepositoryFromScriptProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "storage is not configured"
	}
	var req struct {
		ScriptID string `json:"script_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ScriptID) == "" {
		return "", http.StatusBadRequest, "script_id is required"
	}
	script, err := store.GetScript(strings.TrimSpace(req.ScriptID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", http.StatusNotFound, "script not found"
		}
		return "", http.StatusInternalServerError, "failed to load script"
	}
	return normalizeProjectID(script.ProjectID), 0, ""
}

// resolveCorrelationRulesetCreateProjectID reads the owning project from the
// request body for correlation-ruleset creation.
var resolveCorrelationRulesetCreateProjectID = bodyProjectIDResolver("project_id is required for project-scoped correlation rulesets")

// resolveCorrelationRulesetProjectID looks up an existing correlation ruleset to
// find the project that owns it, for {id}-scoped access checks.
var resolveCorrelationRulesetProjectID = entityProjectIDResolver("correlation ruleset not found", "failed to load correlation ruleset", func(store storage.Store, id string) (string, error) {
	ruleset, err := store.GetCorrelationRuleset(id)
	if err != nil {
		return "", err
	}
	return ruleset.ProjectID, nil
})

// resolveCorrelationRulesetFromScriptProjectID resolves the project from the
// source script referenced in the discover-from-script request body.
func resolveCorrelationRulesetFromScriptProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "storage is not configured"
	}
	var req struct {
		ScriptID string `json:"script_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ScriptID) == "" {
		return "", http.StatusBadRequest, "script_id is required"
	}
	script, err := store.GetScript(strings.TrimSpace(req.ScriptID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", http.StatusNotFound, "script not found"
		}
		return "", http.StatusInternalServerError, "failed to load script"
	}
	return normalizeProjectID(script.ProjectID), 0, ""
}

// resolveMonitoringCollectorCreateProjectID reads the owning project from the
// request body for monitoring-collector creation.
var resolveMonitoringCollectorCreateProjectID = bodyProjectIDResolver("project_id is required for project-scoped monitoring collectors")

// resolveMonitoringCollectorProjectID looks up an existing collector to find its
// owning project, for {id}-scoped access checks.
var resolveMonitoringCollectorProjectID = entityProjectIDResolver("monitoring collector not found", "failed to load monitoring collector", func(store storage.Store, id string) (string, error) {
	collector, err := store.GetMonitoringCollector(id)
	if err != nil {
		return "", err
	}
	return collector.ProjectID, nil
})

var resolveReusableBlockCreateProjectID = bodyProjectIDResolver("project_id is required for project-scoped reusable blocks")

var resolveReusableBlockProjectID = entityProjectIDResolver("reusable block not found", "failed to load reusable block", func(store storage.Store, id string) (string, error) {
	block, err := store.GetReusableBlock(id)
	if err != nil {
		return "", err
	}
	return block.ProjectID, nil
})

func resolveRecorderListProjectID(r *http.Request) (string, int, string) {
	projectID := normalizeProjectID(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped recorder access"
	}
	return projectID, 0, ""
}

func resolveRecorderCreateProjectID(r *http.Request) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	projectID := normalizeProjectID(req.ProjectID)
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped recorder access"
	}
	return projectID, 0, ""
}

func resolveRecorderSessionProjectID(rec *recorder.Recorder) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if rec == nil {
			return "", http.StatusServiceUnavailable, "recorder is not configured"
		}
		session, ok := rec.GetSession(r.PathValue("id"))
		if !ok {
			return "", http.StatusNotFound, "recording session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveBrowserRecordingSessionProjectID(manager *BrowserRecordingManager) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if manager == nil {
			return "", http.StatusServiceUnavailable, "browser recorder is not configured"
		}
		session, ok := manager.Get(r.PathValue("id"))
		if !ok {
			return "", http.StatusNotFound, "browser recording session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveBrowserReplaySessionProjectID(manager *BrowserReplayManager) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if manager == nil {
			return "", http.StatusServiceUnavailable, "browser replay is not configured"
		}
		session, ok := manager.Get(r.PathValue("session_id"))
		if !ok {
			return "", http.StatusNotFound, "browser replay session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveScriptDebugSessionProjectID(manager *ScriptDebugManager) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if manager == nil {
			return "", http.StatusServiceUnavailable, "script debugger is not configured"
		}
		session, ok := manager.Get(r.PathValue("session_id"))
		if !ok {
			return "", http.StatusNotFound, "script debug session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveImportProjectIDFromForm(r *http.Request, _ storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	if err := r.ParseMultipartForm(50 << 20); err == nil {
		if projectID := strings.TrimSpace(r.FormValue("project_id")); projectID != "" {
			return normalizeProjectID(projectID), 0, ""
		}
	}
	return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
}

func resolveImportProjectIDFromJSON(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveImportProjectIDFromJSONOrForm(r *http.Request, store storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return resolveImportProjectIDFromForm(r, store)
	}
	if strings.HasPrefix(contentType, "application/json") {
		return resolveImportProjectIDFromJSON(r, store)
	}
	return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
}

func resolveK6ExportProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
		Script    *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if projectID := normalizeProjectID(req.ProjectID); projectID != "" {
		return projectID, 0, ""
	}
	if req.Script != nil {
		if projectID := normalizeProjectID(req.Script.ProjectID); projectID != "" {
			return projectID, 0, ""
		}
	}
	return "", http.StatusBadRequest, "project_id or script.project_id is required for project-scoped export access"
}

// resolveParameterizeApplyProjectID scopes the import parameterization apply
// endpoint to the project of the draft script in the request body.
func resolveParameterizeApplyProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		Script *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.Script == nil || strings.TrimSpace(req.Script.ProjectID) == "" {
		return "", http.StatusBadRequest, "script.project_id is required for project-scoped parameterization access"
	}
	return normalizeProjectID(req.Script.ProjectID), 0, ""
}

func resolveAIAssistanceProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID        string `json:"project_id,omitempty"`
		ExistingScriptID string `json:"existing_script_id,omitempty"`
		ScriptID         string `json:"script_id,omitempty"`
		TargetType       string `json:"target_type,omitempty"`
		TargetID         string `json:"target_id,omitempty"`
		Script           *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if projectID := normalizeProjectID(req.ProjectID); projectID != "" {
		return projectID, 0, ""
	}
	if req.Script != nil {
		return normalizeProjectID(req.Script.ProjectID), 0, ""
	}
	scriptID := firstNonEmpty(req.ScriptID, req.ExistingScriptID)
	if scriptID == "" && strings.EqualFold(strings.TrimSpace(req.TargetType), "script") {
		scriptID = strings.TrimSpace(req.TargetID)
	}
	if scriptID != "" && store != nil {
		script, err := store.GetScript(scriptID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", http.StatusNotFound, "script not found"
			}
			return "", http.StatusInternalServerError, "failed to load script"
		}
		return normalizeProjectID(script.ProjectID), 0, ""
	}
	return "", 0, ""
}

func resolveExternalResultImportProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
		ReleaseID string `json:"release_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) != "" {
		return normalizeProjectID(req.ProjectID), 0, ""
	}
	if releaseID := strings.TrimSpace(req.ReleaseID); releaseID != "" {
		if store == nil {
			return "", http.StatusServiceUnavailable, "release storage is not configured"
		}
		releaseReq := r.Clone(r.Context())
		releaseReq.SetPathValue("id", releaseID)
		return resolveReleaseProjectID(releaseReq, store)
	}
	return normalizeProjectID(""), 0, ""
}

func resolveScriptCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID string  `json:"project_id,omitempty"`
		FolderID  *string `json:"folder_id,omitempty"`
		Script    *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.FolderID != nil {
		return resolveFolderProjectIDValue(*req.FolderID, store)
	}
	if strings.TrimSpace(req.ProjectID) != "" {
		return normalizeProjectID(req.ProjectID), 0, ""
	}
	if req.Script != nil {
		return normalizeProjectID(req.Script.ProjectID), 0, ""
	}
	return normalizeProjectID(""), 0, ""
}

func resolveFolderProjectIDValue(folderID string, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "folder storage is not configured"
	}
	folder, err := store.GetFolder(folderID)
	if err != nil {
		return "", http.StatusNotFound, "folder not found"
	}
	return normalizeProjectID(folder.ProjectID), 0, ""
}
