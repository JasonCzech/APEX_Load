package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
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

type applicationSecretCleanupGuidance struct {
	ApplicationID     string   `json:"application_id"`
	Names             []string `json:"names"`
	Providers         []string `json:"providers"`
	Prefix            string   `json:"prefix"`
	DiscoveryComplete bool     `json:"discovery_complete"`
}

// Called under the project mutation gate before graph deletion drops the
// applications and their metadata. Only local, value-free records are read.
func (s *Server) projectApplicationSecretCleanup(projectID string) ([]applicationSecretCleanupGuidance, error) {
	applications, err := s.store.ListApplications()
	if err != nil {
		return nil, err
	}
	metadataStore, supported := s.store.(interface {
		ListExternalApplicationSecretMetadata(string) ([]storage.ProjectSecretMetadata, error)
	})
	provider := secretStoreProvider(s.applicationSecretStore)
	azure := strings.Contains(provider, "azure_key_vault")
	external := azure || strings.Contains(provider, "aws_secrets_manager")
	result := []applicationSecretCleanupGuidance{}
	for _, application := range applications {
		if application.ProjectID != projectID {
			continue
		}
		if !supported {
			if external {
				return nil, errors.New("application secret metadata storage is unavailable")
			}
			continue
		}
		metadata, err := metadataStore.ListExternalApplicationSecretMetadata(application.ID)
		if err != nil {
			return nil, err
		}
		if len(metadata) == 0 && !external {
			continue
		}
		cleanup := applicationSecretCleanupGuidance{
			ApplicationID: application.ID, Names: []string{}, Providers: []string{},
			Prefix: engine.ProjectSecretBindingPrefix(storage.ApplicationSecretScopeID(application.ID)),
			// Pre-existing cloud secrets can be unindexed. Preserve their scope
			// prefix and explicitly require operator discovery after deletion.
			DiscoveryComplete: !external,
		}
		names, providers := map[string]bool{}, map[string]bool{}
		if azure {
			providers["azure_key_vault"] = true
		}
		if strings.Contains(provider, "aws_secrets_manager") {
			providers["aws_secrets_manager"] = true
		}
		for _, item := range metadata {
			if name := strings.TrimSpace(item.SecretName); name != "" {
				names[name] = true
			}
			if provider := strings.TrimSpace(item.Provider); provider != "" {
				providers[provider] = true
			}
		}
		for name := range names {
			cleanup.Names = append(cleanup.Names, name)
		}
		for provider := range providers {
			cleanup.Providers = append(cleanup.Providers, provider)
		}
		sort.Strings(cleanup.Names)
		sort.Strings(cleanup.Providers)
		result = append(result, cleanup)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ApplicationID < result[j].ApplicationID })
	return result, nil
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
	req.Settings.GitHub = normalizeGitHubProjectPolicy(req.Settings.GitHub)
	if err := validateProjectSettings(req.Settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !githubProjectPoliciesEqual(req.Settings.GitHub, storage.GitHubProjectPolicy{}) && !s.requireSystemAdminForGitHubPolicyMutation(w, r) {
		return
	}
	req.Settings.GitHub.Actions = s.stampGitHubActionsPolicyUpdate(r, storage.GitHubActionsPolicy{}, req.Settings.GitHub.Actions)
	domainID, categoryID, err := s.resolveProjectPlacement(stringPtrValue(req.DomainID), stringPtrValue(req.CategoryID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project placement: "+err.Error())
		return
	}
	now := time.Now()
	project := &storage.Project{ID: uuid.New().String(), DomainID: domainID, CategoryID: categoryID, Name: strings.TrimSpace(req.Name), Settings: req.Settings, CreatedAt: now, UpdatedAt: now}
	projectCreator, supportsAtomicHierarchy := s.store.(interface {
		CreateProjectWithLegacyHierarchy(project *storage.Project) error
	})
	if !supportsAtomicHierarchy {
		writeError(w, http.StatusNotImplemented, "atomic project hierarchy creation is not supported by this storage backend")
		return
	}
	if err := projectCreator.CreateProjectWithLegacyHierarchy(project); err != nil {
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
	// Scope filtering runs before the query-param filters so a caller cannot
	// use ?domain_id= to reach a domain they hold no binding on.
	_, visibleProjects, unrestricted, err := s.visibleScopeForRequest(r, projects)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !unrestricted {
		filtered := make([]*storage.Project, 0, len(projects))
		for _, project := range projects {
			if project == nil {
				continue
			}
			if _, ok := visibleProjects[normalizeProjectID(project.ID)]; ok {
				filtered = append(filtered, project)
			}
		}
		projects = filtered
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
	req.Settings.GitHub = normalizeGitHubProjectPolicy(req.Settings.GitHub)
	if err := validateProjectSettings(req.Settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !githubProjectPoliciesEqual(project.Settings.GitHub, req.Settings.GitHub) && !s.requireSystemAdminForGitHubPolicyMutation(w, r) {
		return
	}
	req.Settings.GitHub.Actions = s.stampGitHubActionsPolicyUpdate(r, project.Settings.GitHub.Actions, req.Settings.GitHub.Actions)
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
		placementChanged := normalizeDomainID(project.DomainID) != resolvedDomainID || normalizeRepositoryCategoryID(project.CategoryID) != resolvedCategoryID
		if placementChanged && !s.requireProjectPlacementAccess(w, r, resolvedDomainID, resolvedCategoryID) {
			return
		}
		project.DomainID = resolvedDomainID
		project.CategoryID = resolvedCategoryID
	}
	project.Name = strings.TrimSpace(req.Name)
	// An omitted authoring policy is unchanged: a settings form that does not
	// know the policy must not silently switch a strict save policy off. Send
	// "authoring": {} to clear it.
	previousAuthoring := projectAuthoringPolicyDetails(project.Settings.Authoring)
	if req.Settings.Authoring == nil {
		req.Settings.Authoring = project.Settings.Authoring
	}
	// The same holds for the telemetry opt-ins.
	if req.Settings.Telemetry == nil {
		req.Settings.Telemetry = project.Settings.Telemetry
	}
	project.Settings = req.Settings
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
	s.runner.ReconcilePendingTests()
	details := map[string]any{"name": project.Name, "max_concurrent_tests": project.Settings.MaxConcurrentTests, "max_vusers": project.Settings.MaxVUsers}
	if current := projectAuthoringPolicyDetails(project.Settings.Authoring); current != previousAuthoring {
		details["authoring_policy_before"], details["authoring_policy_after"] = previousAuthoring, current
	}
	s.recordAuditEvent(r, "project.update", "success", "project", project.ID, project.ID, details)
	writeJSON(w, http.StatusOK, project)
}

// projectAuthoringPolicyDetails names the strict save policies in force, for
// audit details.
func projectAuthoringPolicyDetails(policy *storage.ProjectAuthoringPolicy) string {
	var parts []string
	if policy != nil && policy.RequireScriptSaveIfMatch {
		parts = append(parts, "require_script_save_if_match")
	}
	if policy != nil && policy.RequireScenarioSaveIfMatch {
		parts = append(parts, "require_scenario_save_if_match")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

// requireProjectPlacementAccess is the second authorization gate for moving an
// existing project into a different domain/repository. The route-level policy
// proves the caller administers the source project; without this target check,
// that caller could attach it to any repository they cannot otherwise modify.
func (s *Server) requireProjectPlacementAccess(w http.ResponseWriter, r *http.Request, domainID, categoryID string) bool {
	// Global administrators and explicit auth-bypass mode are intentionally not
	// project-bound by middleware and retain cross-domain placement authority.
	if _, scoped := authorizedProjectFromRequest(r); !scoped {
		return true
	}
	user, ok := AuthenticatedUserFromContext(r.Context())
	if !ok || user == nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	if user.hasGlobalRole(RoleProjectAdmin, RoleSystemAdmin) || user.hasDomainRole(domainID, RoleEditor, RoleProjectAdmin) {
		return true
	}
	category, err := s.store.GetRepositoryCategory(categoryID)
	if err == nil && category != nil && user.hasRepositoryRole(category.RepositoryID, RoleEditor, RoleProjectAdmin) {
		return true
	}
	writeError(w, http.StatusForbidden, "target project placement is forbidden")
	return false
}

func githubProjectPoliciesEqual(left, right storage.GitHubProjectPolicy) bool {
	return reflect.DeepEqual(normalizeGitHubProjectPolicy(left), normalizeGitHubProjectPolicy(right))
}

// requireSystemAdminForGitHubPolicyMutation protects controller-owned GitHub
// App/PAT credentials from being remapped through generic project CRUD. Project
// admins may administer ordinary project settings, but only a system admin may
// define the repository/branch policy that grants use of controller authority.
func (s *Server) requireSystemAdminForGitHubPolicyMutation(w http.ResponseWriter, r *http.Request) bool {
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok && user != nil {
		if user.hasGlobalRole(RoleSystemAdmin) {
			return true
		}
		writeError(w, http.StatusForbidden, "github project policy changes require system_admin access")
		return false
	}
	if isAuthBypassMode(s.authSettingsForRequest().Mode) {
		return true
	}
	writeError(w, http.StatusForbidden, "github project policy changes require system_admin access")
	return false
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
	if settings.MaxConcurrentTests > maxCapacityUnit {
		return errProjectSettings(fmt.Sprintf("max_concurrent_tests must not exceed %d", maxCapacityUnit))
	}
	if settings.MaxVUsers < 0 {
		return errProjectSettings("max_vusers must be greater than or equal to 0")
	}
	if settings.MaxVUsers > maxCapacityUnit {
		return errProjectSettings(fmt.Sprintf("max_vusers must not exceed %d", maxCapacityUnit))
	}
	if settings.MaxVUsersPerTest < 0 {
		return errProjectSettings("max_vusers_per_test must be greater than or equal to 0")
	}
	if settings.MaxVUsersPerTest > maxManagedTestVUsers {
		return errProjectSettings(fmt.Sprintf("max_vusers_per_test must not exceed %d", maxManagedTestVUsers))
	}
	if settings.MaxTestDurationMinutes < 0 {
		return errProjectSettings("max_test_duration_minutes must be greater than or equal to 0")
	}
	if _, ok := checkedDuration(settings.MaxTestDurationMinutes, time.Minute); !ok {
		return errProjectSettings("max_test_duration_minutes is too large")
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
		if quota.MaxConcurrentTests > maxCapacityUnit {
			return errProjectSettings(fmt.Sprintf("pool_quotas.max_concurrent_tests must not exceed %d", maxCapacityUnit))
		}
		if quota.MaxVUsers < 0 {
			return errProjectSettings("pool_quotas.max_vusers must be greater than or equal to 0")
		}
		if quota.MaxVUsers > maxCapacityUnit {
			return errProjectSettings(fmt.Sprintf("pool_quotas.max_vusers must not exceed %d", maxCapacityUnit))
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
	if err := validateGitHubActionsPolicy(settings.GitHub.Actions); err != nil {
		return err
	}
	if len(settings.RedactionRules) == 0 {
		return nil
	}
	if _, err := engine.NewRedactionConfig(settings.RedactionRules); err != nil {
		return err
	}
	return nil
}

func validateGitHubActionsPolicy(policy storage.GitHubActionsPolicy) error {
	policy = normalizeGitHubProjectPolicy(storage.GitHubProjectPolicy{Actions: policy}).Actions
	if policy.MaxVUsers < 0 {
		return errProjectSettings("github.actions.max_vusers must be greater than or equal to 0")
	}
	if policy.MaxVUsers > maxManagedTestVUsers {
		return errProjectSettings(fmt.Sprintf("github.actions.max_vusers must not exceed %d", maxManagedTestVUsers))
	}
	if policy.MaxDurationMinutes < 0 {
		return errProjectSettings("github.actions.max_duration_minutes must be greater than or equal to 0")
	}
	if _, ok := checkedDuration(policy.MaxDurationMinutes, time.Minute); !ok {
		return errProjectSettings("github.actions.max_duration_minutes is too large")
	}
	switch policy.ValidityGate {
	case "", ciValidityGateInvalid, ciValidityGateSuspect:
	default:
		return errProjectSettings(`github.actions.validity_gate must be empty, "invalid" or "suspect"`)
	}
	if !policy.Enabled {
		return nil
	}
	if err := validateGitHubActionsTrustURL("issuer", policy.Issuer); err != nil {
		return errProjectSettings(err.Error())
	}
	if err := validateGitHubActionsTrustURL("server URL", policy.ServerURL); err != nil {
		return errProjectSettings(err.Error())
	}
	if err := validateGitHubActionsIssuerServerPair(policy.Issuer, policy.ServerURL); err != nil {
		return errProjectSettings(err.Error())
	}
	if policy.Audience != "" {
		if err := validateCIOIDCAudience(policy.Audience); err != nil {
			return errProjectSettings("github.actions.audience is invalid: " + err.Error())
		}
	}
	if len(policy.AllowedRepositoryIDs) == 0 && len(policy.AllowedRepositories) == 0 {
		return errProjectSettings("github.actions requires at least one allowed repository id or name")
	}
	for _, repo := range policy.AllowedRepositories {
		if _, _, ok := splitOwnerRepo(repo); !ok && !strings.ContainsAny(repo, "*?[") {
			return errProjectSettings("github.actions.allowed_repositories entries must be owner/repo or a glob")
		}
	}
	if len(policy.AllowedRefs) == 0 {
		return errProjectSettings("github.actions.allowed_refs must not be empty when Actions is enabled")
	}
	if len(policy.AllowedWorkflowRefs) == 0 {
		return errProjectSettings("github.actions.allowed_workflow_refs must not be empty when Actions is enabled")
	}
	if policy.MaxVUsers == 0 {
		return errProjectSettings("github.actions.max_vusers must be set when Actions is enabled")
	}
	if policy.MaxDurationMinutes == 0 {
		return errProjectSettings("github.actions.max_duration_minutes must be set when Actions is enabled")
	}
	if len(policy.AllowedProtocols) == 0 {
		return errProjectSettings("github.actions.allowed_protocols must not be empty when Actions is enabled")
	}
	if len(policy.AllowedTargetHosts) == 0 {
		return errProjectSettings("github.actions.allowed_target_hosts must not be empty when Actions is enabled")
	}
	for _, host := range policy.AllowedTargetHosts {
		if strings.Contains(host, "://") || strings.ContainsAny(host, "{}@/\\\r\n\x00") {
			return errProjectSettings("github.actions.allowed_target_hosts entries must be host names or host globs without schemes, credentials, paths, or templates")
		}
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
	id := r.PathValue("id")
	project, err := s.store.GetProject(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id = project.ID
	domainID := project.DomainID
	secretProvider := secretStoreProvider(s.projectSecretStore)
	externalSecretCleanupNames := make([]string, 0)
	externalSecretCleanupProviders := make([]string, 0)
	externalSecretCleanupPrefix := ""
	externalSecretDiscoveryComplete := true
	var externalSecretDiscoveryErr error
	externalSecretDiscoveryComplete, externalSecretDiscoveryErr = s.discoverProjectSecretMetadata(r.Context(), id)
	if externalSecretDiscoveryErr != nil {
		externalSecretDiscoveryComplete = false
		slog.Warn("external project secret discovery incomplete; continuing with indexed cleanup guidance", "project_id", id, "reason", "provider_unavailable")
	}
	legacyKeyVaultConfigured := engine.ProjectKeyVaultProjectAllowlisted(id)
	if hasConfiguredExternalSecretProvider(secretProvider) || legacyKeyVaultConfigured {
		externalSecretCleanupPrefix = engine.ProjectSecretBindingPrefix(id)
	}
	// Authorization and external-provider discovery have completed before the
	// project-keyed exclusive section. From here through the durable graph delete,
	// wait only for mutations already admitted for this project; unrelated
	// projects remain fully concurrent.
	releaseProjectMutation := s.projectMutations.acquire(id, true)
	projectMutationHeld := true
	defer func() {
		if projectMutationHeld {
			releaseProjectMutation()
		}
	}()
	runnerDeletionHeld := false
	if s.runner != nil {
		if err := s.runner.beginProjectDeletion(id); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		runnerDeletionHeld = true
		defer func() {
			if runnerDeletionHeld {
				s.runner.endProjectDeletion(id)
			}
		}()
	}
	// Provider discovery above may overlap an already-admitted secret mutation.
	// Re-read the durable metadata after the project tombstone is installed and
	// all earlier same-project mutations have drained. This bounded local query
	// closes that race without holding the project gate across provider I/O.
	if metadataStore, ok := s.store.(storage.ProjectSecretMetadataStore); ok {
		secretMetadata, err := metadataStore.ListExternalProjectSecretMetadata(id)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "failed to enumerate externally retained project secrets")
			return
		}
		if len(secretMetadata) > 0 && externalSecretCleanupPrefix == "" {
			externalSecretCleanupPrefix = engine.ProjectSecretBindingPrefix(id)
		}
		seenExternalNames := make(map[string]struct{}, len(secretMetadata))
		seenExternalProviders := make(map[string]struct{}, len(secretMetadata)+1)
		for _, metadata := range secretMetadata {
			name := strings.TrimSpace(metadata.SecretName)
			if name == "" {
				continue
			}
			if _, duplicate := seenExternalNames[name]; duplicate {
				continue
			}
			seenExternalNames[name] = struct{}{}
			externalSecretCleanupNames = append(externalSecretCleanupNames, name)
			if provider := strings.TrimSpace(metadata.Provider); provider != "" {
				if _, duplicate := seenExternalProviders[provider]; !duplicate {
					seenExternalProviders[provider] = struct{}{}
					externalSecretCleanupProviders = append(externalSecretCleanupProviders, provider)
				}
			}
		}
		for _, provider := range []string{"azure_key_vault", "aws_secrets_manager"} {
			if strings.Contains(secretProvider, provider) || (provider == "azure_key_vault" && legacyKeyVaultConfigured) {
				if _, found := seenExternalProviders[provider]; !found {
					externalSecretCleanupProviders = append(externalSecretCleanupProviders, provider)
				}
			}
		}
		sort.Strings(externalSecretCleanupNames)
		sort.Strings(externalSecretCleanupProviders)
	} else {
		for _, provider := range []string{"azure_key_vault", "aws_secrets_manager"} {
			if strings.Contains(secretProvider, provider) || (provider == "azure_key_vault" && legacyKeyVaultConfigured) {
				externalSecretCleanupProviders = append(externalSecretCleanupProviders, provider)
			}
		}
	}
	applicationSecretCleanup, err := s.projectApplicationSecretCleanup(id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to enumerate externally retained application secrets")
		return
	}
	for _, cleanup := range applicationSecretCleanup {
		externalSecretDiscoveryComplete = externalSecretDiscoveryComplete && cleanup.DiscoveryComplete
	}
	externalSecretCleanupRequired := len(externalSecretCleanupNames) > 0 || externalSecretCleanupPrefix != "" || len(applicationSecretCleanup) > 0
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
	deleteResult, err := s.deleteProjectDurably(id)
	if err != nil {
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if storage.IsGitHubScriptSyncBusy(err) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch deleteResult {
	case storage.LeaseFencedDeleteActive:
		writeError(w, http.StatusConflict, "project has active tests or synchronization operations")
		return
	case storage.LeaseFencedDeleteMissing:
		writeError(w, http.StatusNotFound, "project not found")
		return
	case storage.LeaseFencedDeleteDeleted:
		// Fence every request that resolved this project before the durable
		// deletion. The marker is installed while the exclusive project gate is
		// still held, before any queued mutation can enter its handler.
		s.projectMutations.markDeleted(id)
		// The durable graph deletion and coordinator tombstone are now complete.
		// Release the project gate before in-memory/session cleanup: Bruno cleanup
		// may wait on a shared filesystem or object backend, and queued requests are
		// already rejected by the tombstone rather than allowed to recreate state.
		releaseProjectMutation()
		projectMutationHeld = false
		// Continue with local-index and physical archive cleanup below.
	default:
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("unexpected durable project delete result %q", deleteResult))
		return
	}
	if s.runner != nil {
		s.runner.forgetProject(id)
		// Durable deletion and local index eviction are the only operations that
		// must exclude a test's create→install boundary. Release before provider,
		// filesystem, and archive-object cleanup so slow physical cleanup does not
		// stall unrelated internal work for this now-deleted project.
		s.runner.endProjectDeletion(id)
		runnerDeletionHeld = false
	}
	s.clearProjectSourceSecretSessions(id)
	cleanupErrors := make([]string, 0)
	if err := s.clearProjectBrunoImportSessions(id); err != nil {
		cleanupErrors = append(cleanupErrors, "Bruno artifacts: "+err.Error())
	}
	for _, record := range archiveRecords {
		if err := s.cleanupQueuedArchiveUnderFence(record); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Sprintf("%s: %v", record.TestID, err))
		}
	}
	if len(cleanupErrors) > 0 {
		s.recordAuditEventWithProjectSnapshot(r, "project.delete", "partial", "project", id, id, domainID, map[string]any{
			"archive_cleanup_errors":              cleanupErrors,
			"external_secret_cleanup_required":    externalSecretCleanupRequired,
			"external_application_secret_cleanup": applicationSecretCleanup,
			"external_secret_cleanup_names":       externalSecretCleanupNames,
			"external_secret_cleanup_providers":   externalSecretCleanupProviders,
			"external_secret_cleanup_prefix":      externalSecretCleanupPrefix,
			"secret_provider":                     secretProvider,
			"external_secret_discovery_complete":  externalSecretDiscoveryComplete,
		})
		// The project row and its governed secret metadata are already gone at
		// this point. Preserve the external-provider cleanup guidance in the
		// error response so an administrator is not forced to reconstruct it
		// after a separate physical-artifact cleanup failure.
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":                               "project metadata was deleted but physical archive cleanup was incomplete: " + strings.Join(cleanupErrors, "; "),
			"deleted":                             id,
			"external_secret_cleanup_required":    externalSecretCleanupRequired,
			"external_application_secret_cleanup": applicationSecretCleanup,
			"external_secret_cleanup_names":       externalSecretCleanupNames,
			"external_secret_cleanup_providers":   externalSecretCleanupProviders,
			"external_secret_cleanup_prefix":      externalSecretCleanupPrefix,
			"secret_provider":                     secretProvider,
			"external_secret_discovery_complete":  externalSecretDiscoveryComplete,
		})
		return
	}
	s.recordAuditEventWithProjectSnapshot(r, "project.delete", "success", "project", id, id, domainID, map[string]any{
		"external_secret_cleanup_required":    externalSecretCleanupRequired,
		"external_application_secret_cleanup": applicationSecretCleanup,
		"external_secret_cleanup_names":       externalSecretCleanupNames,
		"external_secret_cleanup_providers":   externalSecretCleanupProviders,
		"external_secret_cleanup_prefix":      externalSecretCleanupPrefix,
		"secret_provider":                     secretProvider,
		"external_secret_discovery_complete":  externalSecretDiscoveryComplete,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":                             id,
		"external_secret_cleanup_required":    externalSecretCleanupRequired,
		"external_application_secret_cleanup": applicationSecretCleanup,
		"external_secret_cleanup_names":       externalSecretCleanupNames,
		"external_secret_cleanup_providers":   externalSecretCleanupProviders,
		"external_secret_cleanup_prefix":      externalSecretCleanupPrefix,
		"secret_provider":                     secretProvider,
		"external_secret_discovery_complete":  externalSecretDiscoveryComplete,
	})
}

func (s *Server) deleteProjectDurably(id string) (storage.LeaseFencedDeleteResult, error) {
	// The project's business key goes with it; a later project with the same
	// ID must not reuse the cached digester.
	defer func() {
		if s != nil && s.runner != nil {
			s.runner.businessKeys.Delete(normalizeProjectID(id))
		}
	}()
	if s == nil || s.store == nil {
		return storage.LeaseFencedDeleteFenced, fmt.Errorf("project storage is not configured")
	}
	if s.runner != nil && s.runner.durableExecutionFence {
		leaseOwnerID := s.runner.currentExecutionFenceToken()
		if leaseOwnerID == "" {
			return storage.LeaseFencedDeleteFenced, errNotExecutionLeader
		}
		store, ok := s.store.(leaseFencedProjectDeletePersistence)
		if !ok {
			return storage.LeaseFencedDeleteFenced, fmt.Errorf("project persistence does not support execution-lease-fenced deletion")
		}
		result, err := store.DeleteProjectIfLeaseHeld(id, executionLeaseName, leaseOwnerID)
		if err != nil {
			return result, err
		}
		if result == storage.LeaseFencedDeleteFenced {
			return result, errNotExecutionLeader
		}
		return result, nil
	}
	if err := s.store.DeleteProject(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.LeaseFencedDeleteMissing, nil
		}
		if storage.IsGitHubScriptSyncBusy(err) {
			return storage.LeaseFencedDeleteActive, nil
		}
		return storage.LeaseFencedDeleteFenced, err
	}
	return storage.LeaseFencedDeleteDeleted, nil
}
