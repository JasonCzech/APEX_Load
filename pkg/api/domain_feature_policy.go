package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const domainFeatureCatalogVersion = "2026-08-27.1"

type domainFeatureDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category"`
	ParentID    string `json:"parent_id,omitempty"`
}

type domainFeatureState struct {
	domainFeatureDefinition
	ConfiguredEnabled bool `json:"configured_enabled"`
	EffectiveEnabled  bool `json:"effective_enabled"`
}

type domainFeatureCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type domainFeaturePolicyResponse struct {
	DomainID           string                  `json:"domain_id"`
	CatalogVersion     string                  `json:"catalog_version"`
	Categories         []domainFeatureCategory `json:"categories"`
	Features           []domainFeatureState    `json:"features"`
	DisabledFeatureIDs []string                `json:"disabled_feature_ids"`
	Revision           string                  `json:"revision"`
	UpdatedAt          *time.Time              `json:"updated_at,omitempty"`
}

var domainFeatureCategories = []domainFeatureCategory{
	{ID: "script_builder", Name: "Script Builder Workflows", Description: "Ways users can start or source a script."},
	{ID: "blank_editors", Name: "Blank Editors", Description: "Protocols available when starting from a blank script."},
	{ID: "script_import", Name: "Imported Collections & Scripts", Description: "Sources accepted by the Script Builder import workflow."},
	{ID: "workload_shape", Name: "Workload Shapes", Description: "Named workload presets. Custom workload configuration always remains available."},
}

var domainFeatureCatalog = []domainFeatureDefinition{
	{ID: "script_builder.existing_scripts", Name: "Model Existing Scripts", Category: "script_builder"},
	{ID: "script_builder.starter_templates", Name: "Starter Templates", Category: "script_builder"},
	{ID: "script_builder.reusable_blocks", Name: "Reusable Blocks", Category: "script_builder"},
	{ID: "script_builder.browser_recording", Name: "Browser Recording", Category: "script_builder"},
	{ID: "script_builder.blank_editors", Name: "Blank Editors", Category: "script_builder"},
	{ID: "script_builder.imports", Name: "Imported Collections & Scripts", Category: "script_builder"},

	{ID: "script_editor.api", Name: "API", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.soap", Name: "SOAP", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.java_http", Name: "Java-over-HTTP", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.websocket", Name: "WebSocket", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.grpc", Name: "gRPC", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.database", Name: "Database", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.network", Name: "Network", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.browser", Name: "Browser", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.jms", Name: "JMS", Category: "blank_editors", ParentID: "script_builder.blank_editors"},
	{ID: "script_editor.kafka", Name: "Kafka", Category: "blank_editors", ParentID: "script_builder.blank_editors"},

	{ID: "script_import.openapi", Name: "OpenAPI", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.bruno", Name: "Bruno", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.postman", Name: "Postman", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.wsdl", Name: "WSDL", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.asyncapi", Name: "AsyncAPI", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.raml", Name: "RAML", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.traffic_har", Name: "HAR", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.recorder_export", Name: "Recorder Export", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.sample", Name: "Request / Response Samples", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.enterprise_archive", Name: "LoadRunner Enterprise Archives", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.jmeter", Name: "JMeter", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.devweb", Name: "DevWeb", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.k6", Name: "k6", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.cloudtest", Name: "CloudTest", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.eventhubs_capture", Name: "Event Hubs Capture", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.broker_config", Name: "Broker Config", Category: "script_import", ParentID: "script_builder.imports"},
	{ID: "script_import.oracle_ebusiness_review", Name: "Oracle E-Business / Siebel Review", Category: "script_import", ParentID: "script_builder.imports"},

	{ID: "workload_shape.closed-vuser", Name: "Closed VUser", Category: "workload_shape"},
	{ID: "workload_shape.open-arrival-rate", Name: "Open Arrival Rate", Category: "workload_shape"},
	{ID: "workload_shape.stepped-ramp", Name: "Stepped Ramp", Category: "workload_shape"},
	{ID: "workload_shape.production-replay", Name: "Production Traffic Profile", Category: "workload_shape"},
	{ID: "workload_shape.spike-rendezvous", Name: "Rendezvous Spike", Category: "workload_shape"},
	{ID: "workload_shape.soak", Name: "Soak", Category: "workload_shape"},
	{ID: "workload_shape.stress", Name: "Stress", Category: "workload_shape"},
	{ID: "workload_shape.goal-based", Name: "Goal-Based", Category: "workload_shape"},
	{ID: "workload_shape.chaos-resilience", Name: "Chaos Resilience", Category: "workload_shape"},
	{ID: "workload_shape.mixed-protocol", Name: "Mixed Protocol", Category: "workload_shape"},
}

func knownDomainFeatureIDs() map[string]domainFeatureDefinition {
	result := make(map[string]domainFeatureDefinition, len(domainFeatureCatalog))
	for _, feature := range domainFeatureCatalog {
		result[feature.ID] = feature
	}
	return result
}

func domainFeaturePolicyStore(store storage.Store) storage.DomainFeaturePolicyStore {
	policyStore, _ := store.(storage.DomainFeaturePolicyStore)
	return policyStore
}

func domainFeatureResponse(policy *storage.DomainFeaturePolicy) domainFeaturePolicyResponse {
	if policy == nil {
		policy = &storage.DomainFeaturePolicy{Revision: "0"}
	}
	known := knownDomainFeatureIDs()
	disabled := make(map[string]bool, len(policy.DisabledFeatureIDs))
	knownDisabled := make([]string, 0, len(policy.DisabledFeatureIDs))
	for _, id := range policy.DisabledFeatureIDs {
		if _, ok := known[id]; ok {
			disabled[id] = true
			knownDisabled = append(knownDisabled, id)
		}
	}
	sort.Strings(knownDisabled)
	states := make([]domainFeatureState, 0, len(domainFeatureCatalog))
	for _, feature := range domainFeatureCatalog {
		configured := !disabled[feature.ID]
		effective := configured
		for parent := feature.ParentID; effective && parent != ""; {
			effective = !disabled[parent]
			parent = known[parent].ParentID
		}
		states = append(states, domainFeatureState{domainFeatureDefinition: feature, ConfiguredEnabled: configured, EffectiveEnabled: effective})
	}
	var updatedAt *time.Time
	if !policy.UpdatedAt.IsZero() {
		value := policy.UpdatedAt
		updatedAt = &value
	}
	return domainFeaturePolicyResponse{DomainID: policy.DomainID, CatalogVersion: domainFeatureCatalogVersion, Categories: domainFeatureCategories, Features: states, DisabledFeatureIDs: knownDisabled, Revision: policy.Revision, UpdatedAt: updatedAt}
}

func (s *Server) loadDomainFeaturePolicy(domainID string) (*storage.DomainFeaturePolicy, error) {
	store := domainFeaturePolicyStore(s.store)
	if store == nil {
		return nil, errors.New("domain feature policy storage is not configured")
	}
	return store.GetDomainFeaturePolicy(normalizeDomainID(domainID))
}

func (s *Server) handleGetDomainFeatures(w http.ResponseWriter, r *http.Request) {
	policy, err := s.loadDomainFeaturePolicy(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "domain not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := domainFeatureResponse(policy)
	w.Header().Set("ETag", fmt.Sprintf("%q", response.Revision))
	writeJSON(w, http.StatusOK, response)
}

type updateDomainFeaturesRequest struct {
	DisabledFeatureIDs []string `json:"disabled_feature_ids"`
	ExpectedRevision   string   `json:"expected_revision,omitempty"`
}

func requestExpectedRevision(r *http.Request, body string) string {
	revision := strings.TrimSpace(r.Header.Get("If-Match"))
	if revision == "" {
		revision = strings.TrimSpace(body)
	}
	if strings.HasPrefix(revision, "W/") {
		revision = strings.TrimSpace(strings.TrimPrefix(revision, "W/"))
	}
	return strings.Trim(revision, `"`)
}

func (s *Server) handleUpdateDomainFeatures(w http.ResponseWriter, r *http.Request) {
	var req updateDomainFeaturesRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	expected := requestExpectedRevision(r, req.ExpectedRevision)
	if expected == "" {
		writeError(w, http.StatusPreconditionRequired, "If-Match or expected_revision is required")
		return
	}
	known := knownDomainFeatureIDs()
	requestedKnown := make([]string, 0, len(req.DisabledFeatureIDs))
	for _, id := range req.DisabledFeatureIDs {
		id = strings.TrimSpace(id)
		if _, ok := known[id]; !ok {
			writeError(w, http.StatusBadRequest, "unknown domain feature id: "+id)
			return
		}
		requestedKnown = append(requestedKnown, id)
	}
	store := domainFeaturePolicyStore(s.store)
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "domain feature policy storage is not configured")
		return
	}
	current, err := store.GetDomainFeaturePolicy(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "domain not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if expected == "*" {
		expected = current.Revision
	}
	// Preserve feature IDs written by a newer controller without allowing this
	// older API to accept arbitrary identifiers from clients.
	merged := append([]string(nil), requestedKnown...)
	for _, id := range current.DisabledFeatureIDs {
		if _, ok := known[id]; !ok {
			merged = append(merged, id)
		}
	}
	updated, ok, err := store.UpdateDomainFeaturePolicyIfRevision(r.PathValue("id"), merged, expected)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "domain feature policy changed while the update was in flight; reload and retry")
		return
	}
	before := append([]string(nil), current.DisabledFeatureIDs...)
	after := append([]string(nil), updated.DisabledFeatureIDs...)
	sort.Strings(before)
	sort.Strings(after)
	s.recordAuditEvent(r, "domain.feature_policy.update", "success", "domain", updated.DomainID, "", map[string]any{"before": before, "after": after, "changed_feature_ids": symmetricStringDifference(before, after)})
	s.broadcastDomainFeaturePolicyChanged(updated.DomainID, updated.Revision)
	response := domainFeatureResponse(updated)
	w.Header().Set("ETag", fmt.Sprintf("%q", response.Revision))
	writeJSON(w, http.StatusOK, response)
}

func symmetricStringDifference(left, right []string) []string {
	counts := map[string]int{}
	for _, id := range left {
		counts[id]++
	}
	for _, id := range right {
		counts[id]--
	}
	result := make([]string, 0)
	for id, count := range counts {
		if count != 0 {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func (s *Server) broadcastDomainFeaturePolicyChanged(domainID, revision string) {
	if s.hub == nil || s.store == nil {
		return
	}
	projects, err := s.store.ListProjects()
	if err != nil {
		return
	}
	for _, project := range projects {
		if project != nil && normalizeDomainID(project.DomainID) == normalizeDomainID(domainID) {
			s.hub.BroadcastScopedJSON("", project.ID, map[string]any{"type": "domain_feature_policy_changed", "project_id": project.ID, "domain_id": domainID, "revision": revision})
		}
	}
}

// domainFeaturePolicyForProject resolves a project's effective feature policy
// once, so callers that test many features against one project do not repeat
// the project read, the policy read, and the catalog rebuild per feature.
//
// A nil response means "not governed" and every feature reads as enabled. That
// covers a storage-less server, which cannot persist restrictions at all, and a
// project that does not exist: the calling handler owns missing-entity status,
// and reporting a permanent client error as 503 told clients and uptime
// monitoring to retry a request that can never succeed.
func (s *Server) domainFeaturePolicyForProject(projectID string) (*domainFeaturePolicyResponse, error) {
	if s.store == nil {
		return nil, nil
	}
	project, err := s.store.GetProject(normalizeProjectID(projectID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	policy, err := s.loadDomainFeaturePolicy(project.DomainID)
	if err != nil {
		return nil, err
	}
	response := domainFeatureResponse(policy)
	response.DomainID = project.DomainID
	return &response, nil
}

// featureEnabled fails closed for an identifier this controller's catalog does
// not define, but treats an ungoverned policy as fully enabled.
func (p *domainFeaturePolicyResponse) featureEnabled(featureID string) bool {
	if p == nil {
		return true
	}
	for _, feature := range p.Features {
		if feature.ID == featureID {
			return feature.EffectiveEnabled
		}
	}
	return false
}

func (s *Server) domainFeatureEnabledForProject(projectID, featureID string) (bool, string, error) {
	if s.store == nil {
		return false, "", errors.New("storage is not configured")
	}
	project, err := s.store.GetProject(normalizeProjectID(projectID))
	if err != nil {
		return false, "", err
	}
	policy, err := s.loadDomainFeaturePolicy(project.DomainID)
	if err != nil {
		return false, project.DomainID, err
	}
	response := domainFeatureResponse(policy)
	for _, feature := range response.Features {
		if feature.ID == featureID {
			return feature.EffectiveEnabled, project.DomainID, nil
		}
	}
	return false, project.DomainID, fmt.Errorf("unknown domain feature id %q", featureID)
}

func writeDomainFeatureDisabled(w http.ResponseWriter, domainID, featureID string) {
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error":      "This feature has been disabled by an administrator.",
		"code":       "domain_feature_disabled",
		"feature_id": featureID,
		"domain_id":  domainID,
	})
}

type domainFeatureDisabledError struct {
	domainID  string
	featureID string
}

var errDomainFeaturePolicyUnavailable = errors.New("domain feature policy is unavailable")

func (e *domainFeatureDisabledError) Error() string {
	return "domain feature is disabled: " + e.featureID
}

func (s *Server) validateDomainFeaturesForProject(projectID string, featureIDs []string) error {
	for _, featureID := range featureIDs {
		featureID = strings.TrimSpace(featureID)
		if featureID == "" {
			continue
		}
		enabled, domainID, err := s.domainFeatureEnabledForProject(projectID, featureID)
		if err != nil {
			return fmt.Errorf("%w: %v", errDomainFeaturePolicyUnavailable, err)
		}
		if !enabled {
			return &domainFeatureDisabledError{domainID: domainID, featureID: featureID}
		}
	}
	return nil
}

func domainFeatureIDForRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	path := strings.TrimSpace(r.URL.Path)
	if strings.HasPrefix(path, "/api/v1/browser-recorder/") || strings.HasPrefix(path, "/api/v1/recorder/") {
		return "script_builder.browser_recording"
	}
	if !strings.HasPrefix(path, "/api/v1/scripts/import/") {
		return ""
	}
	profiles := []struct {
		needle    string
		featureID string
	}{
		{"/enterprise-script-archive", "script_import.enterprise_archive"},
		{"/eventhubs-capture", "script_import.eventhubs_capture"},
		{"/browser-recorder", "script_builder.browser_recording"},
		{"/oracle-ebusiness", "script_import.oracle_ebusiness_review"},
		{"/recorder", "script_import.recorder_export"},
		{"/salesforce", "script_import.traffic_har"},
		{"/openapi", "script_import.openapi"},
		{"/postman", "script_import.postman"},
		{"/asyncapi", "script_import.asyncapi"},
		{"/broker-config", "script_import.broker_config"},
		{"/cloudtest", "script_import.cloudtest"},
		{"/bruno", "script_import.bruno"},
		{"/jmeter", "script_import.jmeter"},
		{"/devweb", "script_import.devweb"},
		{"/sample", "script_import.sample"},
		{"/raml", "script_import.raml"},
		{"/wsdl", "script_import.wsdl"},
		{"/har", "script_import.traffic_har"},
		{"/k6", "script_import.k6"},
	}
	for _, profile := range profiles {
		if strings.Contains(path, profile.needle) {
			return profile.featureID
		}
	}
	// Importer-neutral mutation routes are gated inside their handlers after the
	// request-size limit has been installed and the server-owned draft/session
	// profile has been recovered. Never inspect an unbounded body in access
	// metadata resolution, and never trust source_profile as authorization input.
	return ""
}

func featureIDForImportProfile(profile string) string {
	profile = strings.ToLower(strings.TrimSpace(profile))
	switch profile {
	case "har", "salesforce":
		profile = "traffic_har"
	case "browser_recorder", "browser_recording_har":
		return "script_builder.browser_recording"
	case "recorder", "playwright":
		profile = "recorder_export"
	case "enterprise_script_archive", "loadrunner", "truclient", "trueclient_mobile_web", "web2_browser_profile":
		profile = "enterprise_archive"
	case "raw_http", "curl", "rest_json":
		profile = "sample"
	case "bruno_native":
		profile = "bruno"
	case "oracle_ebusiness", "siebel":
		profile = "oracle_ebusiness_review"
	}
	if _, ok := knownDomainFeatureIDs()["script_import."+profile]; ok {
		return "script_import." + profile
	}
	if _, ok := importerRegistrationByProfile(profile); ok {
		// Some internal or compound import profiles intentionally have no
		// independently configurable catalog entry. They remain governed by the
		// Imported Collections & Scripts parent instead of being hard-denied.
		return "script_builder.imports"
	}
	return ""
}

func domainFeatureAllowsRequest(w http.ResponseWriter, r *http.Request, store storage.Store, requirement accessRequirement) bool {
	featureID := strings.TrimSpace(requirement.DomainFeatureID)
	if featureID == "" {
		return true
	}
	if store == nil {
		// Storage-less development/test servers cannot persist restrictions, so
		// they retain the catalog default of every feature enabled.
		return true
	}
	projectID := ""
	if requirement.ProjectLookup != nil {
		resolved, status, message := requirement.ProjectLookup(r, store)
		if status != 0 {
			writeError(w, status, message)
			return false
		}
		projectID = normalizeProjectID(resolved)
	} else {
		projectID = strings.TrimSpace(r.URL.Query().Get("project_id"))
		if projectID == "" {
			// Global support matrices have no domain until the caller opts into a
			// project-filtered response.
			return true
		}
	}
	project, err := store.GetProject(projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Leave missing-entity and invalid-request handling to the endpoint.
			// The feature layer must not replace its established 400/404/409
			// contract with an unrelated policy response.
			return true
		}
		writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
		return false
	}
	policyStore := domainFeaturePolicyStore(store)
	if policyStore == nil {
		writeError(w, http.StatusServiceUnavailable, "domain feature policy storage is not configured")
		return false
	}
	policy, err := policyStore.GetDomainFeaturePolicy(project.DomainID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
		return false
	}
	for _, state := range domainFeatureResponse(policy).Features {
		if state.ID == featureID {
			if !state.EffectiveEnabled {
				writeDomainFeatureDisabled(w, project.DomainID, featureID)
				return false
			}
			return true
		}
	}
	writeError(w, http.StatusForbidden, "forbidden")
	return false
}

// requireDomainFeatureForProject is the handler-level counterpart to route
// metadata enforcement. Importer-neutral endpoints use it only after deriving
// the feature from a protected draft/session or verified import metadata.
func (s *Server) requireDomainFeatureForProject(w http.ResponseWriter, projectID, featureID string) bool {
	featureID = strings.TrimSpace(featureID)
	if featureID == "" || s.store == nil {
		// Importer-neutral transformations also serve legacy/non-import drafts
		// that have no governed source profile. A validated batch commit always
		// supplies a registered profile and therefore never takes this branch.
		return true
	}
	enabled, domainID, err := s.domainFeatureEnabledForProject(projectID, featureID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The downstream handler owns missing-project validation and its public
			// status code. No policy exists to evaluate for a missing project.
			return true
		}
		writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
		return false
	}
	if !enabled {
		writeDomainFeatureDisabled(w, domainID, featureID)
		return false
	}
	return true
}

// domainFeatureScopedAuth adds the feature-policy gate after a specialized
// project-scoped authentication middleware has authenticated and authorized
// the request. Specialized recorder routes do not pass through authMiddleware,
// so they need the same explicit second gate used by generic routes.
func (s *Server) domainFeatureScopedAuth(base func(http.Handler) http.Handler, featureID string, lookup requestProjectLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requirement := accessRequirement{
				DomainFeatureID: featureID,
				ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
					return lookup(r)
				},
			}
			if !domainFeatureAllowsRequest(w, r, s.store, requirement) {
				return
			}
			next.ServeHTTP(w, r)
		})
		return base(gated)
	}
}
