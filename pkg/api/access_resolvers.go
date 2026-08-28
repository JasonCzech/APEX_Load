package api

// access_resolvers.go — split from middleware.go (same package, behavior-preserving).
// requestProjectLookup resolvers: derive the project/domain/repository scope of a
// request for project-scoped RBAC enforcement.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
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
	script, err := getPublishedImportScript(store, r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "script not found"
	}
	return normalizeProjectID(script.ProjectID), 0, ""
}

func resolveRemoteRunLinkProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "remote run link storage is not configured"
	}
	links, err := loadRemoteRunLinksFromStore(store)
	if err != nil {
		return "", http.StatusServiceUnavailable, "remote run links are unavailable"
	}
	link, ok := findProjectScopedRemoteRunLink(links, r.PathValue("id"), r.PathValue("remote_test_id"))
	if !ok {
		return "", http.StatusNotFound, "project-scoped remote run link not found"
	}
	return normalizeProjectID(link.LocalProjectID), 0, ""
}

func resolveGitHubSyncJobProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "github sync job storage is not configured"
	}
	githubStore, ok := store.(storage.GitHubEnterpriseStore)
	if !ok {
		return "", http.StatusServiceUnavailable, "github sync job storage is not configured"
	}
	job, err := githubStore.GetGitHubSyncJob(strings.TrimSpace(r.PathValue("id")))
	if err != nil || job == nil {
		return "", http.StatusNotFound, "github sync job not found"
	}
	return normalizeProjectID(job.ProjectID), 0, ""
}

// resolveGitHubCheckLinksProjectID requires a durable project-scoped anchor for
// check-link reads. A global viewer may omit both filters (global roles bypass
// project lookup), while a project-scoped viewer must name a test or script that
// belongs to the project they can view. When both are supplied they must agree.
func resolveGitHubCheckLinksProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "github check link storage is not configured"
	}
	testID := strings.TrimSpace(r.URL.Query().Get("test_id"))
	scriptID := strings.TrimSpace(r.URL.Query().Get("script_id"))
	if testID == "" && scriptID == "" {
		return "", http.StatusBadRequest, "test_id or script_id is required for project-scoped check link access"
	}
	projectID := ""
	if testID != "" {
		test, err := store.GetTest(testID)
		if err != nil || test == nil {
			return "", http.StatusNotFound, "test not found"
		}
		projectID = normalizeProjectID(test.ProjectID)
	}
	if scriptID != "" {
		script, err := getPublishedImportScript(store, scriptID)
		if err != nil || script == nil {
			return "", http.StatusNotFound, "script not found"
		}
		scriptProjectID := normalizeProjectID(script.ProjectID)
		if projectID != "" && projectID != scriptProjectID {
			return "", http.StatusNotFound, "test and script are not in the same project"
		}
		projectID = scriptProjectID
	}
	return projectID, 0, ""
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
	if err != nil || test == nil {
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

	resolvedProjectID := ""
	mergeProjectID := func(candidate, mismatchMessage string) (int, string) {
		candidate = normalizeProjectID(candidate)
		if resolvedProjectID == "" {
			resolvedProjectID = candidate
			return 0, ""
		}
		if candidate != resolvedProjectID {
			return http.StatusBadRequest, mismatchMessage
		}
		return 0, ""
	}

	var revisionSource *storage.TestRecord
	if sourceTestID := strings.TrimSpace(req.RevisionSourceTestID); sourceTestID != "" {
		if store == nil {
			return "", http.StatusServiceUnavailable, "test storage is not configured"
		}
		source, err := store.GetTest(sourceTestID)
		if err != nil || source == nil {
			return "", http.StatusNotFound, "revision source test not found"
		}
		if status, msg := mergeProjectID(source.ProjectID, "revision source test project does not match referenced test resources"); status != 0 {
			return "", status, msg
		}
		revisionSource = source
	}

	// A revision is authorized from its immutable source before any client-
	// supplied project/resource hint is considered. This prevents a deleted live
	// source script from turning an otherwise valid source-scoped request into a
	// 404 before project RBAC is evaluated.
	if strings.TrimSpace(req.ProjectID) != "" {
		if status, msg := mergeProjectID(req.ProjectID, "project_id does not match referenced test resources"); status != 0 {
			return "", status, msg
		}
	}

	if req.FolderID != nil {
		projectID, status, msg := resolveFolderProjectIDValue(*req.FolderID, store)
		if status != 0 {
			return "", status, msg
		}
		if status, msg := mergeProjectID(projectID, "folder project does not match referenced test resources"); status != 0 {
			return "", status, msg
		}
	}

	var inferredProjectID string
	var status int
	var msg string
	if revisionSource != nil {
		inferredProjectID, status, msg = resolveRevisionCreateTestScriptProjectID(req, revisionSource, store)
	} else {
		inferredProjectID, status, msg = resolveCreateTestScriptProjectID(req, store)
	}
	if status != 0 {
		return "", status, msg
	}
	if inferredProjectID != "" {
		if status, msg := mergeProjectID(inferredProjectID, "test scripts do not match referenced test resources"); status != 0 {
			return "", status, msg
		}
	}

	if scenarioID := strings.TrimSpace(req.ScenarioID); scenarioID != "" {
		if revisionSource != nil {
			if scenarioID != strings.TrimSpace(revisionSource.ScenarioID) {
				return "", http.StatusBadRequest, "revision source test does not belong to the requested scenario"
			}
			if status, msg := mergeProjectID(revisionSource.ProjectID, "scenario project does not match referenced test resources"); status != 0 {
				return "", status, msg
			}
		} else {
			scenarioReq := r.Clone(r.Context())
			scenarioReq.SetPathValue("id", scenarioID)
			scenarioProjectID, scenarioStatus, scenarioMsg := resolveScenarioProjectID(scenarioReq, store)
			if scenarioStatus != 0 {
				return "", scenarioStatus, scenarioMsg
			}
			if status, msg := mergeProjectID(scenarioProjectID, "scenario project does not match referenced test resources"); status != 0 {
				return "", status, msg
			}
		}
	}

	implicitRevisionReleaseContext := revisionSource != nil && matchesImplicitRevisionReleaseContext(
		req.ApplicationID, req.ReleaseID, revisionSource.ApplicationID, revisionSource.ReleaseID,
	)
	if releaseID := strings.TrimSpace(req.ReleaseID); releaseID != "" && !implicitRevisionReleaseContext {
		release, err := store.GetRelease(releaseID)
		if err != nil || release == nil {
			return "", http.StatusNotFound, "release not found"
		}
		if applicationID := strings.TrimSpace(req.ApplicationID); applicationID != "" && applicationID != strings.TrimSpace(release.ApplicationID) {
			return "", http.StatusBadRequest, "application_id does not match release"
		}
		releaseReq := r.Clone(r.Context())
		releaseReq.SetPathValue("id", releaseID)
		releaseProjectID, releaseStatus, releaseMsg := resolveReleaseProjectID(releaseReq, store)
		if releaseStatus != 0 {
			return "", releaseStatus, releaseMsg
		}
		if status, msg := mergeProjectID(releaseProjectID, "release project does not match referenced test resources"); status != 0 {
			return "", status, msg
		}
	}

	if applicationID := strings.TrimSpace(req.ApplicationID); applicationID != "" && !implicitRevisionReleaseContext {
		applicationReq := r.Clone(r.Context())
		applicationReq.SetPathValue("id", applicationID)
		applicationProjectID, applicationStatus, applicationMsg := resolveApplicationProjectID(applicationReq, store)
		if applicationStatus != 0 {
			return "", applicationStatus, applicationMsg
		}
		if status, msg := mergeProjectID(applicationProjectID, "application project does not match referenced test resources"); status != 0 {
			return "", status, msg
		}
	}

	return normalizeProjectID(resolvedProjectID), 0, ""
}

// resolveRevisionCreateTestScriptProjectID validates immutable source-group
// identities against the durable queued-test snapshot. Indexed source groups do
// not depend on the mutable script library and therefore remain authorizable
// after their live script is deleted. Explicitly new groups still resolve the
// live script so project isolation and deleted-script 404 behavior are retained.
func resolveRevisionCreateTestScriptProjectID(req CreateTestRequest, source *storage.TestRecord, store storage.Store) (string, int, string) {
	if strings.TrimSpace(req.ScriptID) != "" {
		return "", http.StatusBadRequest, "queued-run revisions require canonical groups with source identities; script_id shorthand is not supported"
	}
	sourceGroups := decodeStoredGroups(source.Groups)
	usedSourceGroups := make(map[int]struct{}, len(req.Groups))
	newGroupProjectID := ""
	for _, group := range req.Groups {
		if group.RevisionNewGroup {
			if group.RevisionSourceGroupIndex != nil {
				return "", http.StatusBadRequest, "revision_new_group cannot be combined with revision_source_group_index"
			}
			script, err := getPublishedImportScript(store, strings.TrimSpace(group.ScriptID))
			if err != nil || script == nil {
				return "", http.StatusNotFound, "script not found"
			}
			candidate := normalizeProjectID(script.ProjectID)
			if newGroupProjectID == "" {
				newGroupProjectID = candidate
			} else if newGroupProjectID != candidate {
				return "", http.StatusBadRequest, "test scripts span multiple projects"
			}
			continue
		}
		if group.RevisionSourceGroupIndex == nil {
			return "", http.StatusBadRequest, "exactly one of revision_source_group_index or revision_new_group is required"
		}
		sourceIndex := *group.RevisionSourceGroupIndex
		if sourceIndex < 0 || sourceIndex >= len(sourceGroups) {
			return "", http.StatusBadRequest, "revision_source_group_index is out of range"
		}
		if strings.TrimSpace(sourceGroups[sourceIndex].ScriptID) != strings.TrimSpace(group.ScriptID) {
			return "", http.StatusBadRequest, "revision source group script does not match"
		}
		if _, duplicate := usedSourceGroups[sourceIndex]; duplicate {
			return "", http.StatusBadRequest, "revision source group is referenced more than once"
		}
		usedSourceGroups[sourceIndex] = struct{}{}
	}
	return newGroupProjectID, 0, ""
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
		script, err := getPublishedImportScript(store, scriptID)
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
	_, scripts, err := getPublishedImportScenario(store, r.PathValue("id"))
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
	hasScript := false
	for index, group := range req.Groups {
		scripts = append(scripts, storage.ScenarioScript{Order: index, ScriptID: group.ScriptID})
		hasScript = hasScript || strings.TrimSpace(group.ScriptID) != ""
	}
	resolved, status, message := resolveProjectIDFromScenarioScripts(scripts, store)
	requested := strings.TrimSpace(req.ProjectID)
	if requested == "" {
		return resolved, status, message
	}
	requested = normalizeProjectID(requested)
	if status != 0 {
		return "", status, message
	}
	if hasScript && resolved != requested {
		return "", http.StatusBadRequest, "scenario scripts do not belong to project_id"
	}
	return requested, 0, ""
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
		script, err := getPublishedImportScript(store, scenarioScript.ScriptID)
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
		ProjectID string `json:"project_id,omitempty"`
		Script    *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.Script == nil {
		return "", http.StatusBadRequest, "script is required"
	}
	requestProjectID := strings.TrimSpace(req.ProjectID)
	scriptProjectID := strings.TrimSpace(req.Script.ProjectID)
	if requestProjectID != "" && scriptProjectID != "" && normalizeProjectID(requestProjectID) != normalizeProjectID(scriptProjectID) {
		return "", http.StatusBadRequest, "project_id does not match script.project_id"
	}
	if requestProjectID != "" {
		return normalizeProjectID(requestProjectID), 0, ""
	}
	return normalizeProjectID(scriptProjectID), 0, ""
}

func resolveBrowserToHTTPPreviewProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID           string `json:"project_id,omitempty"`
		ReplaySessionID     string `json:"replay_session_id,omitempty"`
		SourceBrowserScript *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"source_browser_script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	sourceProjectID := ""
	if req.SourceBrowserScript != nil {
		sourceProjectID = req.SourceBrowserScript.ProjectID
	}
	if strings.TrimSpace(req.ReplaySessionID) != "" && strings.TrimSpace(req.ProjectID) == "" && strings.TrimSpace(sourceProjectID) == "" {
		return "", http.StatusBadRequest, "project_id or source_browser_script.project_id is required with replay_session_id"
	}
	projectID, err := reconcileProjectIDCandidates(req.ProjectID, sourceProjectID)
	if err != nil {
		return "", http.StatusBadRequest, "project_id does not match source_browser_script.project_id"
	}
	return normalizeProjectID(projectID), 0, ""
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
	requestProjectID := strings.TrimSpace(req.ProjectID)
	draftProjectID := ""
	if req.Draft != nil {
		draftProjectID = strings.TrimSpace(req.Draft.ProjectID)
	}
	if requestProjectID != "" && draftProjectID != "" && normalizeProjectID(requestProjectID) != normalizeProjectID(draftProjectID) {
		return "", http.StatusBadRequest, "project_id does not match draft.project_id"
	}
	if requestProjectID != "" {
		return normalizeProjectID(requestProjectID), 0, ""
	}
	return normalizeProjectID(draftProjectID), 0, ""
}

func resolveAuthoringSessionProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "authoring session storage is not configured"
	}
	if projectStore, ok := store.(interface {
		GetAuthoringSessionProjectID(id string) (string, error)
	}); ok {
		projectID, err := projectStore.GetAuthoringSessionProjectID(r.PathValue("id"))
		if err != nil {
			return "", http.StatusNotFound, "authoring session not found"
		}
		return normalizeProjectID(projectID), 0, ""
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
		ImportSessionID string `json:"import_session_id,omitempty"`
		ProjectID       string `json:"project_id,omitempty"`
		Mode            string `json:"mode,omitempty"`
		TargetScriptID  string `json:"target_script_id,omitempty"`
		Script          *struct {
			ID        string `json:"id,omitempty"`
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	protectedCommit := strings.TrimSpace(req.ImportSessionID) != ""
	if req.Script == nil && !protectedCommit {
		return "", http.StatusBadRequest, "script is required"
	}
	if protectedCommit {
		// Authorize protected commits from the candidate project supplied by
		// the inspection workflow. The handler claims that project-scoped
		// server candidate before it looks up a replace target. Resolving the
		// target here would make a missing/stale session usable as a script-ID
		// existence oracle because middleware runs before the claim.
		projectID := strings.TrimSpace(req.ProjectID)
		if projectID == "" && req.Script != nil {
			projectID = strings.TrimSpace(req.Script.ProjectID)
		}
		if projectID == "" {
			return "", http.StatusBadRequest, "project_id is required when committing a protected import session"
		}
		return normalizeProjectID(projectID), 0, ""
	}
	targetID := strings.TrimSpace(req.TargetScriptID)
	if targetID == "" && req.Script != nil && strings.EqualFold(strings.TrimSpace(req.Mode), "replace") {
		targetID = strings.TrimSpace(req.Script.ID)
	}
	if targetID != "" {
		if store == nil {
			return "", http.StatusServiceUnavailable, "script storage is not configured"
		}
		script, err := getPublishedImportScript(store, targetID)
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
	script, err := getPublishedImportScript(store, strings.TrimSpace(req.ScriptID))
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
	script, err := getPublishedImportScript(store, strings.TrimSpace(req.ScriptID))
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

// resolveGrafanaDashboardProjectID looks up an imported dashboard's owning project for
// {id}-scoped access checks.
var resolveGrafanaDashboardProjectID = entityProjectIDResolver("grafana dashboard not found", "failed to load grafana dashboard", func(store storage.Store, id string) (string, error) {
	dashboard, err := store.GetGrafanaDashboard(id)
	if err != nil {
		return "", err
	}
	return dashboard.ProjectID, nil
})

var resolveGrafanaDashboardCommitProjectID = bodyProjectIDResolver("project_id is required for grafana dashboard import commit")

var resolveDashboardDefinitionCreateProjectID = bodyProjectIDResolver("project_id is required for project dashboard creation")

var resolveDashboardDefinitionProjectID = entityProjectIDResolver("dashboard definition not found", "failed to load dashboard definition", func(store storage.Store, id string) (string, error) {
	nativeStore, ok := store.(interface {
		GetDashboardDefinition(string) (*storage.DashboardDefinition, error)
	})
	if !ok {
		return "", sql.ErrNoRows
	}
	dashboard, err := nativeStore.GetDashboardDefinition(id)
	if err != nil {
		return "", err
	}
	return dashboard.ProjectID, nil
})

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

func resolveBrowserRecordingSessionProjectID(server *Server) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if server == nil {
			return "", http.StatusServiceUnavailable, "browser recorder is not configured"
		}
		var manager *BrowserRecordingManager
		manager = server.browserRecording
		if manager == nil {
			return "", http.StatusServiceUnavailable, "browser recorder is not configured"
		}
		session, ok := manager.Get(r.PathValue("id"))
		if ok {
			return normalizeProjectID(session.ProjectID), 0, ""
		}
		// A successful browser-recording save can outlive the process-local
		// recording manager. Permit only the exact save route to recover its
		// project from the durable, receipt-bound scripts. Other recording
		// controls still require the live capability and fail closed.
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/convert") {
			return "", http.StatusNotFound, "browser recording session not found"
		}
		recovery, err := server.loadDurableBrowserRecordingConversion(r.PathValue("id"))
		if err != nil {
			if errors.Is(err, errBrowserRecordingSaveFinalizing) {
				return "", http.StatusConflict, "the saved browser recording is still finalizing; retry conversion"
			}
			return "", http.StatusInternalServerError, "failed to resolve saved browser recording: " + err.Error()
		}
		if recovery == nil {
			return "", http.StatusNotFound, "browser recording session not found"
		}
		return recovery.ProjectID, 0, ""
	}
}

type durableBrowserRecordingConversion struct {
	ProjectID string
	Scripts   []*dsl.Script
}

var errBrowserRecordingSaveFinalizing = errors.New("saved browser recording publication is still finalizing")

// loadDurableBrowserRecordingConversion reads a keyed O(1) locator, then loads
// exactly its bounded script IDs. This runs before project authorization, so a
// bogus lost-session request must never scan/decode the project script library.
func (s *Server) loadDurableBrowserRecordingConversion(recordingID string) (*durableBrowserRecordingConversion, error) {
	if s == nil || s.store == nil || strings.TrimSpace(recordingID) == "" {
		return nil, nil
	}
	locatorKey, err := s.browserRecordingSaveLocatorKey(recordingID)
	if err != nil {
		return nil, err
	}
	raw, err := s.store.GetSetting(locatorKey)
	if err != nil {
		return nil, fmt.Errorf("load saved browser recording locator: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	locator, err := s.decodeBrowserRecordingSaveLocator(raw)
	if err != nil {
		return nil, fmt.Errorf("saved browser recording locator is invalid: %w", err)
	}
	if locator.RecordingID != strings.TrimSpace(recordingID) {
		return nil, errors.New("saved browser recording locator belongs to a different recording")
	}
	if len(locator.ScriptIDs) == 0 || len(locator.ScriptIDs) > maxBridgedBrowserImportScripts+1 {
		return nil, errors.New("saved browser recording locator has an invalid script count")
	}
	if locator.State != browserRecordingSaveLocatorPublished {
		if err := s.recoverStagedBrowserRecordingConversionsAtStartup(); err != nil {
			if errors.Is(err, storage.ErrDirectImportReceiptBusy) {
				return nil, errBrowserRecordingSaveFinalizing
			}
			return nil, fmt.Errorf("recover staged browser recording: %w", err)
		}
		current, err := s.store.GetSetting(locatorKey)
		if err != nil {
			return nil, fmt.Errorf("reload saved browser recording locator: %w", err)
		}
		if current != raw {
			if strings.TrimSpace(current) == "" {
				return nil, nil
			}
			return s.loadDurableBrowserRecordingConversion(recordingID)
		}
	}
	ordered := make([]*dsl.Script, len(locator.ScriptIDs))
	for index, scriptID := range locator.ScriptIDs {
		script, loadErr := getPublishedImportScript(s.store, scriptID)
		if loadErr != nil || script == nil {
			if loadErr == nil {
				loadErr = sql.ErrNoRows
			}
			if locator.State != browserRecordingSaveLocatorPublished && errors.Is(loadErr, sql.ErrNoRows) {
				removed, cleanupErr := s.removeCompensatedBrowserRecordingSaveLocator(locatorKey, raw, locator)
				if cleanupErr != nil {
					return nil, fmt.Errorf("clean compensated browser recording locator: %w", cleanupErr)
				}
				if removed {
					return nil, nil
				}
				return nil, errBrowserRecordingSaveFinalizing
			}
			return nil, fmt.Errorf("saved browser recording script %d is unavailable: %w", index+1, loadErr)
		}
		if normalizeProjectID(script.ProjectID) != locator.ProjectID {
			return nil, errors.New("saved browser recording locator crosses project boundaries")
		}
		preserved := browserRecordingImportPreserved(script)
		if browserRecordingPreservedString(preserved, browserRecordingSaveSessionIDKey) != locator.RecordingID ||
			browserRecordingPreservedInt(preserved, browserRecordingSaveVersionKey) != browserRecordingSaveVersion ||
			browserRecordingPreservedInt(preserved, browserRecordingSaveIndexKey) != index ||
			browserRecordingPreservedInt(preserved, browserRecordingSaveCountKey) != len(locator.ScriptIDs) {
			return nil, errors.New("saved browser recording script ordering does not match its locator")
		}
		if err := s.verifyBrowserRecordingSaveProvenance(script); err != nil {
			return nil, fmt.Errorf("saved browser recording provenance is invalid: %w", err)
		}
		ordered[index] = script
	}
	if locator.State != browserRecordingSaveLocatorPublished {
		return nil, errBrowserRecordingSaveFinalizing
	}
	return &durableBrowserRecordingConversion{ProjectID: locator.ProjectID, Scripts: ordered}, nil
}

// removeCompensatedBrowserRecordingSaveLocator recognizes only a signed
// pre-publication locator whose complete, bounded script set is physically
// absent. A staged script that is merely hidden by a live import claim keeps the
// locator intact. The CAS is against the exact signed encoding so a concurrent
// post-commit promotion wins safely and remains fail-closed.
func (s *Server) removeCompensatedBrowserRecordingSaveLocator(
	locatorKey string,
	raw string,
	locator *browserRecordingSaveLocator,
) (bool, error) {
	if s == nil || s.store == nil || locator == nil ||
		locator.State == browserRecordingSaveLocatorPublished {
		return false, nil
	}
	for _, scriptID := range locator.ScriptIDs {
		script, err := s.store.GetScript(scriptID)
		if err == nil && script != nil {
			return false, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("inspect compensated script %q: %w", scriptID, err)
		}
	}
	cas, ok := s.store.(settingsCompareAndSwapper)
	if !ok {
		return false, errors.New("durable browser recording locator requires atomic settings storage")
	}
	won, err := cas.CompareAndSwapSetting(locatorKey, raw, "")
	if err != nil {
		return false, err
	}
	if won {
		return true, nil
	}
	current, err := s.store.GetSetting(locatorKey)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(current) == "" {
		return true, nil
	}
	if _, err := s.decodeBrowserRecordingSaveLocator(current); err != nil {
		return false, fmt.Errorf("browser recording locator changed to an invalid value: %w", err)
	}
	return false, errors.New("browser recording locator changed concurrently during compensation cleanup")
}

func browserRecordingImportPreserved(script *dsl.Script) map[string]any {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return nil
	}
	return script.Metadata.Import.Preserved
}

func browserRecordingPreservedString(preserved map[string]any, key string) string {
	value, _ := preserved[key].(string)
	return strings.TrimSpace(value)
}

func browserRecordingPreservedInt(preserved map[string]any, key string) int {
	switch value := preserved[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		if value == float64(int(value)) {
			return int(value)
		}
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return -1
}

func resolveBrowserReplaySessionProjectID(manager *BrowserReplayManager) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if manager == nil {
			return "", http.StatusServiceUnavailable, "browser replay is not configured"
		}
		projectID, ok := manager.LookupProjectID(r.PathValue("session_id"))
		if !ok {
			return "", http.StatusNotFound, "browser replay session not found"
		}
		return projectID, 0, ""
	}
}

func resolveScriptDebugSessionProjectID(manager *ScriptDebugManager) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if manager == nil {
			return "", http.StatusServiceUnavailable, "script debugger is not configured"
		}
		projectID, ok := manager.LookupProjectID(r.PathValue("session_id"))
		if !ok {
			return "", http.StatusNotFound, "script debug session not found"
		}
		return projectID, 0, ""
	}
}

func resolveImportProjectIDFromForm(r *http.Request, _ storage.Store) (string, int, string) {
	queryProjectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if queryProjectID != "" {
			return normalizeProjectID(queryProjectID), 0, ""
		}
		bodyProjectID, err := peekMultipartProjectID(r)
		if err != nil || bodyProjectID == "" {
			return "", http.StatusBadRequest, "project_id query parameter or leading multipart field is required for multipart imports"
		}
		return normalizeProjectID(bodyProjectID), 0, ""
	}
	return reconcileImportProjectID(queryProjectID, "", "request body")
}

// peekMultipartProjectID reads only leading scalar multipart fields and restores
// every consumed byte. It deliberately stops at the first file, avoiding the
// pre-authorization buffering and temporary files caused by ParseMultipartForm.
func peekMultipartProjectID(r *http.Request) (string, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return "", errors.New("invalid multipart content type")
	}
	var consumed bytes.Buffer
	limited := io.LimitReader(r.Body, 256<<10)
	reader := multipart.NewReader(io.TeeReader(limited, &consumed), params["boundary"])
	defer func() { r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(consumed.Bytes()), r.Body)) }()
	for {
		part, err := reader.NextPart()
		if err != nil {
			return "", err
		}
		if part.FileName() != "" {
			return "", nil
		}
		value, err := io.ReadAll(io.LimitReader(part, (64<<10)+1))
		if err != nil {
			return "", err
		}
		if part.FormName() == "project_id" {
			if len(value) > 64<<10 {
				return "", errors.New("project_id multipart field is too large")
			}
			return strings.TrimSpace(string(value)), nil
		}
	}
}

func authorizedImportProjectID(r *http.Request) string {
	return firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("project_id")), strings.TrimSpace(r.FormValue("project_id")))
}

// jsonImportProjectID preserves the project selected by the import access
// resolver when a JSON wrapper omits its optional project_id field. The access
// resolver has already rejected query/body disagreement before the handler runs.
func jsonImportProjectID(r *http.Request, bodyProjectID string) string {
	// Both sides are trimmed before the choice, so a whitespace-only body field
	// falls through to the query string instead of winning and then collapsing
	// to the empty default.
	return firstNonEmpty(
		strings.TrimSpace(bodyProjectID),
		strings.TrimSpace(r.URL.Query().Get("project_id")),
	)
}

func resolveImportProjectIDFromJSON(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	return reconcileImportProjectID(r.URL.Query().Get("project_id"), req.ProjectID, "JSON body")
}

func resolveBridgedBrowserImportProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
		Scripts   []struct {
			ProjectID string  `json:"project_id,omitempty"`
			FolderID  *string `json:"folder_id,omitempty"`
		} `json:"scripts"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	projectID, err := reconcileProjectIDCandidates(
		r.URL.Query().Get("project_id"),
		req.ProjectID,
	)
	if err != nil {
		return "", http.StatusBadRequest, "project_id query parameter does not match JSON body project_id"
	}
	for _, script := range req.Scripts {
		projectID, err = reconcileProjectIDCandidates(projectID, script.ProjectID)
		if err != nil {
			return "", http.StatusBadRequest, "browser recording scripts belong to different projects"
		}
		if script.FolderID == nil {
			continue
		}
		folderProjectID, status, message := resolveFolderProjectIDValue(*script.FolderID, store)
		if status != 0 {
			return "", status, message
		}
		projectID, err = reconcileProjectIDCandidates(projectID, folderProjectID)
		if err != nil {
			return "", http.StatusBadRequest, "browser recording script folder belongs to a different project"
		}
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveImportProjectIDFromJSONOrForm(r *http.Request, store storage.Store) (string, int, string) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return resolveImportProjectIDFromForm(r, store)
	}
	if strings.HasPrefix(contentType, "application/json") {
		return resolveImportProjectIDFromJSON(r, store)
	}
	// Raw-body uploads (text/yaml, text/xml, application/zip, or no Content-Type)
	// are a supported import path: the handlers read the raw body directly and
	// take project_id from the query string. Honor the query parameter so
	// project-scoped users keep the raw-body upload path (a 400 here locked them
	// out of it entirely). reconcileImportProjectID still 400s when it is absent.
	return reconcileImportProjectID(r.URL.Query().Get("project_id"), "", "query parameter")
}

func reconcileImportProjectID(queryProjectID string, bodyProjectID string, bodySource string) (string, int, string) {
	queryProjectID = normalizeExplicitImportProjectID(queryProjectID)
	bodyProjectID = normalizeExplicitImportProjectID(bodyProjectID)
	if queryProjectID != "" && bodyProjectID != "" && queryProjectID != bodyProjectID {
		return "", http.StatusBadRequest, "project_id query parameter does not match " + bodySource + " project_id"
	}
	if bodyProjectID != "" {
		return bodyProjectID, 0, ""
	}
	if queryProjectID != "" {
		return queryProjectID, 0, ""
	}
	return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
}

func normalizeExplicitImportProjectID(projectID string) string {
	if strings.TrimSpace(projectID) == "" {
		return ""
	}
	return normalizeProjectID(projectID)
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

// resolveSharedValuesApplyProjectID scopes the collection-wide shared-value
// apply endpoint. Every script in one import draft belongs to the same project,
// so the first draft that names one is authoritative; a request that mixes
// projects is rejected rather than authorized against only its first entry.
func resolveSharedValuesApplyProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	type draftRef struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	var req struct {
		Scripts []*draftRef `json:"scripts,omitempty"`
		Script  *draftRef   `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	drafts := req.Scripts
	if len(drafts) == 0 && req.Script != nil {
		drafts = []*draftRef{req.Script}
	}
	projectID := ""
	for _, draft := range drafts {
		if draft == nil {
			continue
		}
		normalized := normalizeProjectID(draft.ProjectID)
		if normalized == "" {
			continue
		}
		if projectID != "" && projectID != normalized {
			return "", http.StatusBadRequest, "every script in one shared-value apply must belong to the same project"
		}
		projectID = normalized
	}
	if projectID == "" {
		return "", http.StatusBadRequest, "script.project_id is required for project-scoped shared-value access"
	}
	return projectID, 0, ""
}

func resolveAIAssistanceProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID        string `json:"project_id,omitempty"`
		ExistingScriptID string `json:"existing_script_id,omitempty"`
		ScriptID         string `json:"script_id,omitempty"`
		SessionID        string `json:"session_id,omitempty"`
		TargetType       string `json:"target_type,omitempty"`
		TargetID         string `json:"target_id,omitempty"`
		Script           *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
		Scenario *struct {
			Groups []struct {
				ScriptID string `json:"script_id,omitempty"`
			} `json:"groups,omitempty"`
		} `json:"scenario,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	projectID := ""
	merge := func(candidate string) bool {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return true
		}
		candidate = normalizeProjectID(candidate)
		if projectID != "" && projectID != candidate {
			return false
		}
		projectID = candidate
		return true
	}
	if !merge(req.ProjectID) {
		return "", http.StatusBadRequest, "AI request references multiple projects"
	}
	if req.Script != nil {
		if !merge(req.Script.ProjectID) {
			return "", http.StatusBadRequest, "AI request references multiple projects"
		}
	}

	needsStore := strings.TrimSpace(req.ExistingScriptID) != "" || strings.TrimSpace(req.ScriptID) != "" ||
		strings.TrimSpace(req.SessionID) != "" || (req.Scenario != nil && len(req.Scenario.Groups) > 0) ||
		(strings.TrimSpace(req.TargetID) != "" && normalizeAITargetType(req.TargetType) != "runtime_settings")
	if needsStore && store == nil {
		return "", http.StatusServiceUnavailable, "AI reference storage is not configured"
	}

	seenScripts := map[string]struct{}{}
	mergeScript := func(id string) (int, string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return 0, ""
		}
		if _, seen := seenScripts[id]; seen {
			return 0, ""
		}
		seenScripts[id] = struct{}{}
		script, err := getPublishedImportScript(store, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return http.StatusNotFound, "script not found"
			}
			return http.StatusInternalServerError, "failed to load script"
		}
		if !merge(script.ProjectID) {
			return http.StatusNotFound, "script not found"
		}
		return 0, ""
	}
	for _, id := range []string{req.ExistingScriptID, req.ScriptID} {
		if status, msg := mergeScript(id); status != 0 {
			return "", status, msg
		}
	}

	if sessionID := strings.TrimSpace(req.SessionID); sessionID != "" {
		sessionStore, ok := store.(interface {
			GetAuthoringSession(id string) (*storage.AuthoringSession, error)
		})
		if !ok {
			return "", http.StatusServiceUnavailable, "authoring session storage is not configured"
		}
		session, err := sessionStore.GetAuthoringSession(sessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", http.StatusNotFound, "authoring session not found"
			}
			return "", http.StatusInternalServerError, "failed to load authoring session"
		}
		if session == nil {
			return "", http.StatusNotFound, "authoring session not found"
		}
		if !merge(session.ProjectID) || (session.Draft != nil && !merge(session.Draft.ProjectID)) {
			return "", http.StatusNotFound, "authoring session not found"
		}
	}

	if req.Scenario != nil {
		for _, group := range req.Scenario.Groups {
			if status, msg := mergeScript(group.ScriptID); status != 0 {
				return "", status, msg
			}
		}
	}

	targetID := strings.TrimSpace(req.TargetID)
	switch normalizeAITargetType(req.TargetType) {
	case "script":
		if status, msg := mergeScript(targetID); status != 0 {
			return "", status, msg
		}
	case "scenario":
		if targetID != "" {
			_, scripts, err := getPublishedImportScenario(store, targetID)
			if errors.Is(err, sql.ErrNoRows) {
				return "", http.StatusNotFound, "scenario not found"
			}
			if err != nil {
				return "", http.StatusInternalServerError, "failed to load scenario"
			}
			// Scenario ownership is normally derived from its stored script
			// references. A legacy/corrupt zero-group scenario has no persisted
			// project field, so require a declared scope for RBAC and let the
			// handler reconcile it against the in-memory StoredScenario owner.
			if len(scripts) == 0 && strings.TrimSpace(req.ProjectID) == "" {
				return "", http.StatusBadRequest, "project_id is required for a scenario without stored script references"
			}
			for _, script := range scripts {
				if status, msg := mergeScript(script.ScriptID); status != 0 {
					return "", status, msg
				}
			}
		}
	case "report":
		if targetID != "" {
			test, err := store.GetTest(targetID)
			if errors.Is(err, sql.ErrNoRows) {
				return "", http.StatusNotFound, "test not found"
			}
			if err != nil {
				return "", http.StatusInternalServerError, "failed to load test"
			}
			if !merge(test.ProjectID) {
				return "", http.StatusNotFound, "test not found"
			}
		}
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveExternalResultImportProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID  string `json:"project_id,omitempty"`
		ReleaseID  string `json:"release_id,omitempty"`
		ScenarioID string `json:"scenario_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	projectID := ""
	if strings.TrimSpace(req.ProjectID) != "" {
		projectID = normalizeProjectID(req.ProjectID)
	}
	if releaseID := strings.TrimSpace(req.ReleaseID); releaseID != "" {
		if store == nil {
			return "", http.StatusServiceUnavailable, "release storage is not configured"
		}
		releaseReq := r.Clone(r.Context())
		releaseReq.SetPathValue("id", releaseID)
		releaseProjectID, status, message := resolveReleaseProjectID(releaseReq, store)
		if status != 0 {
			return "", status, message
		}
		if projectID != "" && projectID != releaseProjectID {
			return "", http.StatusBadRequest, "project_id does not match release project"
		}
		projectID = releaseProjectID
	}
	if scenarioID := strings.TrimSpace(req.ScenarioID); scenarioID != "" {
		if store == nil {
			return "", http.StatusServiceUnavailable, "scenario storage is not configured"
		}
		scenarioReq := r.Clone(r.Context())
		scenarioReq.SetPathValue("id", scenarioID)
		scenarioProjectID, status, message := resolveScenarioProjectID(scenarioReq, store)
		if status != 0 {
			return "", status, message
		}
		if projectID != "" && projectID != scenarioProjectID {
			return "", http.StatusBadRequest, "project_id and result parents do not belong to one project"
		}
		projectID = scenarioProjectID
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveScriptCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID string  `json:"project_id,omitempty"`
		FolderID  *string `json:"folder_id,omitempty"`
		Script    *struct {
			ProjectID string  `json:"project_id,omitempty"`
			FolderID  *string `json:"folder_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	scriptProjectID := ""
	var scriptFolderID *string
	if req.Script != nil {
		scriptProjectID = req.Script.ProjectID
		scriptFolderID = req.Script.FolderID
	}
	projectID, err := reconcileProjectIDCandidates(req.ProjectID, scriptProjectID)
	if err != nil {
		return "", http.StatusBadRequest, "project_id does not match script.project_id"
	}
	effectiveFolderID := scriptFolderID
	if req.FolderID != nil {
		effectiveFolderID = req.FolderID
	}
	if effectiveFolderID != nil {
		folderProjectID, status, message := resolveFolderProjectIDValue(*effectiveFolderID, store)
		if status != 0 {
			return "", status, message
		}
		projectID, err = reconcileProjectIDCandidates(projectID, folderProjectID)
		if err != nil {
			return "", http.StatusBadRequest, "script folder belongs to a different project"
		}
	}
	return normalizeProjectID(projectID), 0, ""
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
