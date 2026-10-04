package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	maxDynatraceResponseBytes = 2 << 20
	maxDynatraceEvidence      = 100
	maxDynatraceSampleText    = 4096
	maxDynatraceFieldText     = 512
)

// DynatraceCaptureConfig is the immutable run snapshot. Only the safe identity
// fields are serialized to API clients and archive manifests.
type DynatraceCaptureConfig struct {
	ConnectionID            string                         `json:"connection_id"`
	ConnectionName          string                         `json:"connection_name,omitempty"`
	ProjectID               string                         `json:"-"`
	EnvironmentURL          string                         `json:"-"`
	PlatformURL             string                         `json:"-"`
	MetricsEnabled          bool                           `json:"-"`
	ProblemsEnabled         bool                           `json:"-"`
	LogsEnabled             bool                           `json:"-"`
	EnvironmentTokenSecret  string                         `json:"-"`
	PlatformAuthType        string                         `json:"-"`
	PlatformTokenSecret     string                         `json:"-"`
	OAuthClientIDSecret     string                         `json:"-"`
	OAuthClientSecretSecret string                         `json:"-"`
	OAuthResource           string                         `json:"-"`
	OAuthScopes             []string                       `json:"-"`
	MetricQueries           []storage.DynatraceMetricQuery `json:"-"`
	ProblemSelector         string                         `json:"-"`
	ProblemEntitySelector   string                         `json:"-"`
	LogBaseFilter           string                         `json:"-"`
	LogTestIDField          string                         `json:"-"`
	LogTimestampField       string                         `json:"-"`
	LogStatusField          string                         `json:"-"`
	LogMessageField         string                         `json:"-"`
	LogTraceIDField         string                         `json:"-"`
	PollIntervalSeconds     int                            `json:"-"`
}

type storedDynatraceCapture struct {
	ConnectionID            string                         `json:"connection_id"`
	ConnectionName          string                         `json:"connection_name,omitempty"`
	ProjectID               string                         `json:"project_id"`
	EnvironmentURL          string                         `json:"environment_url,omitempty"`
	PlatformURL             string                         `json:"platform_url,omitempty"`
	MetricsEnabled          bool                           `json:"metrics_enabled"`
	ProblemsEnabled         bool                           `json:"problems_enabled"`
	LogsEnabled             bool                           `json:"logs_enabled"`
	EnvironmentTokenSecret  string                         `json:"environment_token_secret,omitempty"`
	PlatformAuthType        string                         `json:"platform_auth_type,omitempty"`
	PlatformTokenSecret     string                         `json:"platform_token_secret,omitempty"`
	OAuthClientIDSecret     string                         `json:"oauth_client_id_secret,omitempty"`
	OAuthClientSecretSecret string                         `json:"oauth_client_secret_secret,omitempty"`
	OAuthResource           string                         `json:"oauth_resource,omitempty"`
	OAuthScopes             []string                       `json:"oauth_scopes,omitempty"`
	MetricQueries           []storage.DynatraceMetricQuery `json:"metric_queries,omitempty"`
	ProblemSelector         string                         `json:"problem_selector,omitempty"`
	ProblemEntitySelector   string                         `json:"problem_entity_selector,omitempty"`
	LogBaseFilter           string                         `json:"log_base_filter,omitempty"`
	LogTestIDField          string                         `json:"log_test_id_field,omitempty"`
	LogTimestampField       string                         `json:"log_timestamp_field,omitempty"`
	LogStatusField          string                         `json:"log_status_field,omitempty"`
	LogMessageField         string                         `json:"log_message_field,omitempty"`
	LogTraceIDField         string                         `json:"log_trace_id_field,omitempty"`
	PollIntervalSeconds     int                            `json:"poll_interval_seconds,omitempty"`
}

type DynatraceCapabilityStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type DynatraceLogSample struct {
	Timestamp time.Time `json:"timestamp,omitempty"`
	Status    string    `json:"status,omitempty"`
	Message   string    `json:"message,omitempty"`
	TestID    string    `json:"test_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
}

type DynatraceProblemSummary struct {
	ID             string    `json:"id,omitempty"`
	DisplayID      string    `json:"display_id,omitempty"`
	Title          string    `json:"title,omitempty"`
	Severity       string    `json:"severity,omitempty"`
	Status         string    `json:"status,omitempty"`
	StartTime      time.Time `json:"start_time,omitempty"`
	EndTime        time.Time `json:"end_time,omitempty"`
	AffectedEntity string    `json:"affected_entity,omitempty"`
}

type DynatraceCaptureStatus struct {
	ConnectionID     string                               `json:"connection_id"`
	ConnectionName   string                               `json:"connection_name,omitempty"`
	Status           string                               `json:"status"`
	LastPolledAt     *time.Time                           `json:"last_polled_at,omitempty"`
	LastSuccessfulAt *time.Time                           `json:"last_successful_at,omitempty"`
	Message          string                               `json:"message,omitempty"`
	Capabilities     map[string]DynatraceCapabilityStatus `json:"capabilities,omitempty"`
	Metrics          map[string]float64                   `json:"metrics,omitempty"`
	Logs             []DynatraceLogSample                 `json:"logs,omitempty"`
	Problems         []DynatraceProblemSummary            `json:"problems,omitempty"`
}

type DynatraceEvidenceRecord struct {
	Type    string                   `json:"type"`
	Log     *DynatraceLogSample      `json:"log,omitempty"`
	Problem *DynatraceProblemSummary `json:"problem,omitempty"`
}

type DynatraceConnectionRequest struct {
	ExpectedRevision          int64                          `json:"expected_revision,omitempty"`
	CollectionMode            string                         `json:"collection_mode,omitempty"`
	CollectorAgentID          string                         `json:"collector_agent_id,omitempty"`
	EventsEnabled             bool                           `json:"events_enabled"`
	SpansEnabled              bool                           `json:"spans_enabled"`
	GrailMetricsEnabled       bool                           `json:"grail_metrics_enabled"`
	GrailEventsEnabled        bool                           `json:"grail_events_enabled"`
	TopologyMode              string                         `json:"topology_mode,omitempty"`
	RequestsPerMinute         int                            `json:"requests_per_minute,omitempty"`
	PlatformRequestsPerMinute int                            `json:"platform_requests_per_minute,omitempty"`
	MaxScanBytes              int64                          `json:"max_scan_bytes,omitempty"`
	MaxRunScanBytes           int64                          `json:"max_run_scan_bytes,omitempty"`
	DrainReserveBytes         int64                          `json:"drain_reserve_bytes,omitempty"`
	Name                      string                         `json:"name"`
	Description               string                         `json:"description"`
	Enabled                   bool                           `json:"enabled"`
	EnvironmentURL            string                         `json:"environment_url"`
	PlatformURL               string                         `json:"platform_url"`
	MetricsEnabled            bool                           `json:"metrics_enabled"`
	ProblemsEnabled           bool                           `json:"problems_enabled"`
	LogsEnabled               bool                           `json:"logs_enabled"`
	EnvironmentTokenSecret    *string                        `json:"environment_token_secret,omitempty"`
	PlatformAuthType          string                         `json:"platform_auth_type"`
	PlatformTokenSecret       *string                        `json:"platform_token_secret,omitempty"`
	OAuthClientIDSecret       *string                        `json:"oauth_client_id_secret,omitempty"`
	OAuthClientSecretSecret   *string                        `json:"oauth_client_secret_secret,omitempty"`
	OAuthResource             string                         `json:"oauth_resource"`
	OAuthScopes               []string                       `json:"oauth_scopes"`
	MetricQueries             []storage.DynatraceMetricQuery `json:"metric_queries"`
	ProblemSelector           string                         `json:"problem_selector"`
	ProblemEntitySelector     string                         `json:"problem_entity_selector"`
	LogBaseFilter             string                         `json:"log_base_filter"`
	LogTestIDField            string                         `json:"log_test_id_field"`
	LogTimestampField         string                         `json:"log_timestamp_field"`
	LogStatusField            string                         `json:"log_status_field"`
	LogMessageField           string                         `json:"log_message_field"`
	LogTraceIDField           string                         `json:"log_trace_id_field"`
	PollIntervalSeconds       int                            `json:"poll_interval_seconds"`
	UpdatedAt                 time.Time                      `json:"updated_at"`
}

type DynatraceConnectionResponse struct {
	Capabilities              dynatrace.Capabilities                       `json:"capabilities"`
	Revision                  int64                                        `json:"revision"`
	VerifiedRevision          int64                                        `json:"verified_revision"`
	CollectionMode            string                                       `json:"collection_mode"`
	CollectorAgentID          string                                       `json:"collector_agent_id,omitempty"`
	EventsEnabled             bool                                         `json:"events_enabled"`
	SpansEnabled              bool                                         `json:"spans_enabled"`
	GrailMetricsEnabled       bool                                         `json:"grail_metrics_enabled"`
	GrailEventsEnabled        bool                                         `json:"grail_events_enabled"`
	TopologyMode              string                                       `json:"topology_mode,omitempty"`
	RequestsPerMinute         int                                          `json:"requests_per_minute,omitempty"`
	PlatformRequestsPerMinute int                                          `json:"platform_requests_per_minute,omitempty"`
	MaxScanBytes              int64                                        `json:"max_scan_bytes,omitempty"`
	MaxRunScanBytes           int64                                        `json:"max_run_scan_bytes,omitempty"`
	DrainReserveBytes         int64                                        `json:"drain_reserve_bytes,omitempty"`
	ID                        string                                       `json:"id"`
	ProjectID                 string                                       `json:"project_id"`
	Name                      string                                       `json:"name"`
	Description               string                                       `json:"description,omitempty"`
	Enabled                   bool                                         `json:"enabled"`
	Status                    string                                       `json:"status"`
	EnvironmentURL            string                                       `json:"environment_url,omitempty"`
	PlatformURL               string                                       `json:"platform_url,omitempty"`
	MetricsEnabled            bool                                         `json:"metrics_enabled"`
	ProblemsEnabled           bool                                         `json:"problems_enabled"`
	LogsEnabled               bool                                         `json:"logs_enabled"`
	HasEnvironmentToken       bool                                         `json:"has_environment_token"`
	PlatformAuthType          string                                       `json:"platform_auth_type,omitempty"`
	HasPlatformToken          bool                                         `json:"has_platform_token"`
	HasOAuthClientID          bool                                         `json:"has_oauth_client_id"`
	HasOAuthClientSecret      bool                                         `json:"has_oauth_client_secret"`
	OAuthResource             string                                       `json:"oauth_resource,omitempty"`
	OAuthScopes               []string                                     `json:"oauth_scopes"`
	MetricQueries             []storage.DynatraceMetricQuery               `json:"metric_queries"`
	ProblemSelector           string                                       `json:"problem_selector,omitempty"`
	ProblemEntitySelector     string                                       `json:"problem_entity_selector,omitempty"`
	LogBaseFilter             string                                       `json:"log_base_filter,omitempty"`
	LogTestIDField            string                                       `json:"log_test_id_field,omitempty"`
	LogTimestampField         string                                       `json:"log_timestamp_field,omitempty"`
	LogStatusField            string                                       `json:"log_status_field,omitempty"`
	LogMessageField           string                                       `json:"log_message_field,omitempty"`
	LogTraceIDField           string                                       `json:"log_trace_id_field,omitempty"`
	PollIntervalSeconds       int                                          `json:"poll_interval_seconds"`
	LastCheckedAt             *time.Time                                   `json:"last_checked_at,omitempty"`
	LastCheckStatus           string                                       `json:"last_check_status,omitempty"`
	LastCheckMessage          string                                       `json:"last_check_message,omitempty"`
	LastCapabilityHealth      map[string]storage.DynatraceCapabilityHealth `json:"last_capability_health,omitempty"`
	CreatedAt                 time.Time                                    `json:"created_at"`
	UpdatedAt                 time.Time                                    `json:"updated_at"`
}

func dynatraceConnectionResponse(c *storage.DynatraceConnection) DynatraceConnectionResponse {
	return DynatraceConnectionResponse{ID: c.ID, ProjectID: c.ProjectID, Name: c.Name, Description: c.Description, Enabled: c.Enabled,
		Revision: c.Revision, VerifiedRevision: c.VerifiedRevision, CollectionMode: c.CollectionMode, CollectorAgentID: c.CollectorAgentID,
		EventsEnabled: c.EventsEnabled, SpansEnabled: c.SpansEnabled, GrailMetricsEnabled: c.GrailMetricsEnabled, GrailEventsEnabled: c.GrailEventsEnabled, TopologyMode: c.TopologyMode,
		RequestsPerMinute: c.RequestsPerMinute, PlatformRequestsPerMinute: c.PlatformRequestsPerMinute, MaxScanBytes: c.MaxScanBytes, MaxRunScanBytes: c.MaxRunScanBytes, DrainReserveBytes: c.DrainReserveBytes,
		Capabilities: c.Capabilities, Status: c.Readiness(), EnvironmentURL: c.EnvironmentURL, PlatformURL: c.PlatformURL, MetricsEnabled: c.MetricsEnabled,
		ProblemsEnabled: c.ProblemsEnabled, LogsEnabled: c.LogsEnabled, HasEnvironmentToken: c.EnvironmentTokenSecret != "",
		PlatformAuthType: c.PlatformAuthType, HasPlatformToken: c.PlatformTokenSecret != "", HasOAuthClientID: c.OAuthClientIDSecret != "",
		HasOAuthClientSecret: c.OAuthClientSecretSecret != "", OAuthResource: c.OAuthResource, OAuthScopes: append([]string(nil), c.OAuthScopes...),
		MetricQueries: append([]storage.DynatraceMetricQuery(nil), c.MetricQueries...), ProblemSelector: c.ProblemSelector,
		ProblemEntitySelector: c.ProblemEntitySelector, LogBaseFilter: c.LogBaseFilter, LogTestIDField: c.LogTestIDField,
		LogTimestampField: c.LogTimestampField, LogStatusField: c.LogStatusField, LogMessageField: c.LogMessageField,
		LogTraceIDField: c.LogTraceIDField, PollIntervalSeconds: c.PollIntervalSeconds, LastCheckedAt: c.LastCheckedAt,
		LastCheckStatus: c.LastCheckStatus, LastCheckMessage: c.LastCheckMessage, LastCapabilityHealth: c.LastCapabilityHealth,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func applyDynatraceConnectionRequest(c *storage.DynatraceConnection, req DynatraceConnectionRequest) {
	// Omitted replacements preserve credentials only for the same approved identity.
	if c.EnvironmentURL != req.EnvironmentURL {
		c.EnvironmentTokenSecret = ""
	}
	if c.PlatformURL != req.PlatformURL || c.PlatformAuthType != req.PlatformAuthType || c.OAuthResource != req.OAuthResource {
		c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret = "", "", ""
	}
	c.CollectionMode, c.CollectorAgentID = req.CollectionMode, req.CollectorAgentID
	c.EventsEnabled, c.SpansEnabled, c.GrailMetricsEnabled, c.TopologyMode = req.EventsEnabled, req.SpansEnabled, req.GrailMetricsEnabled, req.TopologyMode
	c.GrailEventsEnabled = req.GrailEventsEnabled
	c.RequestsPerMinute, c.PlatformRequestsPerMinute, c.MaxScanBytes, c.MaxRunScanBytes, c.DrainReserveBytes = req.RequestsPerMinute, req.PlatformRequestsPerMinute, req.MaxScanBytes, req.MaxRunScanBytes, req.DrainReserveBytes
	c.Name, c.Description, c.Enabled = req.Name, req.Description, req.Enabled
	c.EnvironmentURL, c.PlatformURL = req.EnvironmentURL, req.PlatformURL
	c.MetricsEnabled, c.ProblemsEnabled, c.LogsEnabled = req.MetricsEnabled, req.ProblemsEnabled, req.LogsEnabled
	c.PlatformAuthType, c.OAuthResource = req.PlatformAuthType, req.OAuthResource
	c.OAuthScopes = append([]string(nil), req.OAuthScopes...)
	c.MetricQueries = append([]storage.DynatraceMetricQuery(nil), req.MetricQueries...)
	c.ProblemSelector, c.ProblemEntitySelector = req.ProblemSelector, req.ProblemEntitySelector
	c.LogBaseFilter, c.LogTestIDField = req.LogBaseFilter, req.LogTestIDField
	c.LogTimestampField, c.LogStatusField, c.LogMessageField, c.LogTraceIDField = req.LogTimestampField, req.LogStatusField, req.LogMessageField, req.LogTraceIDField
	c.PollIntervalSeconds = req.PollIntervalSeconds
	if req.EnvironmentTokenSecret != nil {
		if value := strings.TrimSpace(*req.EnvironmentTokenSecret); value != "" {
			c.EnvironmentTokenSecret = value
		}
	}
	if req.PlatformTokenSecret != nil {
		if value := strings.TrimSpace(*req.PlatformTokenSecret); value != "" {
			c.PlatformTokenSecret = value
		}
	}
	if req.OAuthClientIDSecret != nil {
		if value := strings.TrimSpace(*req.OAuthClientIDSecret); value != "" {
			if value != c.OAuthClientIDSecret {
				c.OAuthClientSecretSecret = ""
			}
			c.OAuthClientIDSecret = value
		}
	}
	if req.OAuthClientSecretSecret != nil {
		if value := strings.TrimSpace(*req.OAuthClientSecretSecret); value != "" {
			c.OAuthClientSecretSecret = value
		}
	}
	if c.PlatformAuthType == storage.DynatracePlatformAuthToken {
		c.OAuthClientIDSecret, c.OAuthClientSecretSecret = "", ""
	} else if c.PlatformAuthType == storage.DynatracePlatformAuthOAuth {
		c.PlatformTokenSecret = ""
	} else {
		c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret = "", "", ""
	}
}

func (s *Server) dynatraceConnectionStore(w http.ResponseWriter) (storage.DynatraceConnectionStore, bool) {
	store, ok := s.store.(storage.DynatraceConnectionStore)
	if !ok {
		writeError(w, 503, "Dynatrace connection storage is unavailable")
		return nil, false
	}
	return store, true
}

func (s *Server) handleCreateDynatraceConnection(w http.ResponseWriter, r *http.Request) {
	release, err := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if err != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	store, ok := s.dynatraceConnectionStore(w)
	if !ok {
		return
	}
	var req DynatraceConnectionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Enabled {
		writeError(w, 409, "save the connection disabled and verify it before enabling")
		return
	}
	c := &storage.DynatraceConnection{ID: uuid.NewString(), ProjectID: strings.TrimSpace(r.PathValue("id"))}
	applyDynatraceConnectionRequest(c, req)
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := validateDynatraceSecretReferences(c); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := store.CreateDynatraceConnection(c); err != nil {
		writeError(w, 409, "failed to create Dynatrace connection: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "monitoring.dynatrace.create", "success", "dynatrace_connection", c.ID, c.ProjectID, map[string]any{"name": c.Name, "status": c.ConfigurationStatus()})
	writeJSON(w, 201, dynatraceConnectionResponse(c))
}

func (s *Server) handleListDynatraceConnections(w http.ResponseWriter, r *http.Request) {
	store, ok := s.dynatraceConnectionStore(w)
	if !ok {
		return
	}
	projectID := strings.TrimSpace(r.PathValue("id"))
	items, err := store.ListDynatraceConnections(projectID)
	if err != nil {
		writeError(w, 500, "failed to list Dynatrace connections")
		return
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	canManage := s.authSettingsForRequest().Mode == authModeDev || (user != nil && (user.hasGlobalRole(RoleProjectAdmin, RoleSystemAdmin) || userHasProjectOrParentRole(s.store, user, projectID, RoleProjectAdmin)))
	if canManage {
		responses := make([]DynatraceConnectionResponse, 0, len(items))
		for _, item := range items {
			responses = append(responses, dynatraceConnectionResponse(item))
		}
		writeJSON(w, 200, map[string]any{"connections": responses, "count": len(responses)})
		return
	}
	responses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		responses = append(responses, map[string]any{"id": item.ID, "project_id": item.ProjectID, "name": item.Name, "status": item.Readiness(), "revision": item.Revision, "enabled": item.Enabled, "collection_mode": item.CollectionMode, "collector_agent_id": item.CollectorAgentID, "capabilities": item.Capabilities})
	}
	writeJSON(w, 200, map[string]any{"connections": responses, "count": len(responses)})
}

func (s *Server) loadProjectDynatraceConnection(w http.ResponseWriter, r *http.Request) (*storage.DynatraceConnection, storage.DynatraceConnectionStore, bool) {
	store, ok := s.dynatraceConnectionStore(w)
	if !ok {
		return nil, nil, false
	}
	c, err := store.GetDynatraceConnection(strings.TrimSpace(r.PathValue("connection_id")))
	if errors.Is(err, sql.ErrNoRows) || c == nil || c.ProjectID != strings.TrimSpace(r.PathValue("id")) {
		writeError(w, 404, "Dynatrace connection not found")
		return nil, nil, false
	}
	if err != nil {
		writeError(w, 500, "failed to load Dynatrace connection")
		return nil, nil, false
	}
	return c, store, true
}

func (s *Server) handleGetDynatraceConnection(w http.ResponseWriter, r *http.Request) {
	if c, _, ok := s.loadProjectDynatraceConnection(w, r); ok {
		writeJSON(w, 200, dynatraceConnectionResponse(c))
	}
}

func (s *Server) handleUpdateDynatraceConnection(w http.ResponseWriter, r *http.Request) {
	release, mutationErr := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if mutationErr != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	c, store, ok := s.loadProjectDynatraceConnection(w, r)
	if !ok {
		return
	}
	var req DynatraceConnectionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.ExpectedRevision <= 0 && req.UpdatedAt.IsZero() {
		writeError(w, 428, "expected_revision or updated_at is required")
		return
	}
	previous := *c
	previousVerified := c.VerifiedRevision == c.Revision && c.LastCheckStatus == "connected"
	applyDynatraceConnectionRequest(c, req)
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if c.Enabled && (!previousVerified || !storage.SameDynatraceIdentity(&previous, c)) {
		writeError(w, 409, "save configuration disabled and verify the selected execution location before enabling")
		return
	}
	if err := validateDynatraceSecretReferences(c); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var updated bool
	var err error
	if req.ExpectedRevision > 0 {
		if revisioned, ok := store.(interface {
			UpdateDynatraceConnectionIfRevision(*storage.DynatraceConnection, int64) (bool, error)
		}); ok {
			updated, err = revisioned.UpdateDynatraceConnectionIfRevision(c, req.ExpectedRevision)
		} else {
			err = errors.New("revisioned connection storage unavailable")
		}
	} else {
		updated, err = store.UpdateDynatraceConnectionIfUnchanged(c, req.UpdatedAt)
	}
	if err != nil {
		writeError(w, 409, "failed to update Dynatrace connection: "+err.Error())
		return
	}
	if !updated {
		writeError(w, 409, "Dynatrace connection changed since it was loaded; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.dynatrace.update", "success", "dynatrace_connection", c.ID, c.ProjectID, map[string]any{"status": c.ConfigurationStatus()})
	writeJSON(w, 200, dynatraceConnectionResponse(c))
}

func (s *Server) handleDeleteDynatraceConnection(w http.ResponseWriter, r *http.Request) {
	release, mutationErr := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if mutationErr != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	c, store, ok := s.loadProjectDynatraceConnection(w, r)
	if !ok {
		return
	}
	expected, parseErr := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 64)
	if parseErr != nil || expected <= 0 {
		writeError(w, 428, "expected_revision is required")
		return
	}
	revisioned, ok := store.(interface {
		DeleteDynatraceConnectionIfRevision(string, string, int64) (bool, error)
	})
	if !ok {
		writeError(w, 503, "revisioned connection storage unavailable")
		return
	}
	deleted, err := revisioned.DeleteDynatraceConnectionIfRevision(c.ID, c.ProjectID, expected)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if !deleted {
		writeError(w, 409, "Dynatrace connection changed; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.dynatrace.delete", "success", "dynatrace_connection", c.ID, c.ProjectID, nil)
	w.WriteHeader(204)
}

// DynatraceConnectionVerificationResponse is the synchronous compatibility
// verifier's wire contract; native setup operations use the async response.
type DynatraceConnectionVerificationResponse struct {
	Status       string                 `json:"status"`
	CheckedAt    time.Time              `json:"checked_at"`
	Message      string                 `json:"message"`
	Capabilities dynatrace.Capabilities `json:"capabilities"`
}

func (s *Server) handleTestDynatraceConnection(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.loadProjectDynatraceConnection(w, r)
	if !ok {
		return
	}
	if c.CollectionMode == "collector" {
		r.Body = io.NopCloser(strings.NewReader(`{"kind":"verify"}`))
		r.ContentLength = int64(len(`{"kind":"verify"}`))
		s.handleCreateDynatraceOperation(w, r)
		return
	}
	if c.RuntimeConfig().Validate() != nil {
		writeError(w, 409, "Dynatrace connection is not fully configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	caps := dynatrace.Capabilities{}
	client, err := s.nativeDynatraceClient(ctx, c.Snapshot(), "direct:"+s.controllerID)
	if err == nil {
		caps, err = client.Test(ctx)
	}
	if err == nil && !dynatraceWantedCapabilitiesAvailable(dynatraceWantedCapabilities(c), caps) {
		err = errors.New("selected capability unavailable")
	}
	message, overall, status := "Dynatrace capabilities verified", "connected", 200
	if err != nil {
		message, overall, status = "Dynatrace verification failed", "error", 502
	}
	caps = sanitizeDynatraceCapabilitiesForConfig(caps, c.RuntimeConfig())
	s.publishDynatraceOperationHealth(&storage.DynatraceOperation{ConnectionID: c.ID, ProjectID: c.ProjectID, ConnectionRevision: c.Revision}, func() string {
		if err != nil {
			return message
		}
		return ""
	}(), caps)
	writeJSON(w, status, DynatraceConnectionVerificationResponse{Status: overall, CheckedAt: time.Now().UTC(), Message: message, Capabilities: caps})
}

func dynatraceCaptureFromConnection(c *storage.DynatraceConnection) *DynatraceCaptureConfig {
	if c == nil {
		return nil
	}
	return &DynatraceCaptureConfig{ConnectionID: c.ID, ConnectionName: c.Name, ProjectID: c.ProjectID, EnvironmentURL: c.EnvironmentURL,
		PlatformURL: c.PlatformURL, MetricsEnabled: c.MetricsEnabled, ProblemsEnabled: c.ProblemsEnabled, LogsEnabled: c.LogsEnabled,
		EnvironmentTokenSecret: c.EnvironmentTokenSecret, PlatformAuthType: c.PlatformAuthType, PlatformTokenSecret: c.PlatformTokenSecret,
		OAuthClientIDSecret: c.OAuthClientIDSecret, OAuthClientSecretSecret: c.OAuthClientSecretSecret, OAuthResource: c.OAuthResource,
		OAuthScopes: append([]string(nil), c.OAuthScopes...), MetricQueries: append([]storage.DynatraceMetricQuery(nil), c.MetricQueries...),
		ProblemSelector: c.ProblemSelector, ProblemEntitySelector: c.ProblemEntitySelector, LogBaseFilter: c.LogBaseFilter,
		LogTestIDField: c.LogTestIDField, LogTimestampField: c.LogTimestampField, LogStatusField: c.LogStatusField,
		LogMessageField: c.LogMessageField, LogTraceIDField: c.LogTraceIDField, PollIntervalSeconds: c.PollIntervalSeconds}
}

func cloneDynatraceCaptureConfig(c *DynatraceCaptureConfig) *DynatraceCaptureConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.OAuthScopes = append([]string(nil), c.OAuthScopes...)
	cloned.MetricQueries = append([]storage.DynatraceMetricQuery(nil), c.MetricQueries...)
	return &cloned
}

func dynatraceCaptureForStorage(c *DynatraceCaptureConfig) any {
	if c == nil {
		return nil
	}
	return storedDynatraceCapture{ConnectionID: c.ConnectionID, ConnectionName: c.ConnectionName, ProjectID: c.ProjectID, EnvironmentURL: c.EnvironmentURL, PlatformURL: c.PlatformURL,
		MetricsEnabled: c.MetricsEnabled, ProblemsEnabled: c.ProblemsEnabled, LogsEnabled: c.LogsEnabled, EnvironmentTokenSecret: c.EnvironmentTokenSecret,
		PlatformAuthType: c.PlatformAuthType, PlatformTokenSecret: c.PlatformTokenSecret, OAuthClientIDSecret: c.OAuthClientIDSecret,
		OAuthClientSecretSecret: c.OAuthClientSecretSecret, OAuthResource: c.OAuthResource, OAuthScopes: append([]string(nil), c.OAuthScopes...),
		MetricQueries: append([]storage.DynatraceMetricQuery(nil), c.MetricQueries...), ProblemSelector: c.ProblemSelector, ProblemEntitySelector: c.ProblemEntitySelector,
		LogBaseFilter: c.LogBaseFilter, LogTestIDField: c.LogTestIDField, LogTimestampField: c.LogTimestampField, LogStatusField: c.LogStatusField,
		LogMessageField: c.LogMessageField, LogTraceIDField: c.LogTraceIDField, PollIntervalSeconds: c.PollIntervalSeconds}
}

func cloneDynatraceCaptureStatus(status *DynatraceCaptureStatus) *DynatraceCaptureStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	cloned.LastPolledAt = cloneTimePtr(status.LastPolledAt)
	cloned.LastSuccessfulAt = cloneTimePtr(status.LastSuccessfulAt)
	cloned.Metrics = cloneFloat64Map(status.Metrics)
	cloned.Logs = append([]DynatraceLogSample(nil), status.Logs...)
	cloned.Problems = append([]DynatraceProblemSummary(nil), status.Problems...)
	cloned.Capabilities = make(map[string]DynatraceCapabilityStatus, len(status.Capabilities))
	for key, value := range status.Capabilities {
		cloned.Capabilities[key] = value
	}
	return &cloned
}

func (tr *TestRunner) resolveDynatraceCaptureForTest(projectID string, requested *DynatraceCaptureConfig) (*DynatraceCaptureConfig, error) {
	if requested == nil || strings.TrimSpace(requested.ConnectionID) == "" {
		return nil, nil
	}
	if tr == nil || tr.dynatraceStore == nil {
		return nil, errors.New("Dynatrace connections are unavailable")
	}
	connection, err := tr.dynatraceStore.GetDynatraceConnection(strings.TrimSpace(requested.ConnectionID))
	if errors.Is(err, sql.ErrNoRows) || connection == nil {
		return nil, errors.New("selected Dynatrace connection was not found")
	}
	if err != nil {
		return nil, errors.New("selected Dynatrace connection could not be loaded")
	}
	if normalizeProjectID(connection.ProjectID) != normalizeProjectID(projectID) {
		return nil, errors.New("selected Dynatrace connection belongs to a different project")
	}
	if connection.ConfigurationStatus() != "ready" {
		return nil, errors.New("selected Dynatrace connection is not fully configured and enabled")
	}
	return dynatraceCaptureFromConnection(connection), nil
}

type dynatraceQueryResult struct {
	Capabilities map[string]DynatraceCapabilityStatus
	Metrics      map[string]float64
	Logs         []DynatraceLogSample
	Problems     []DynatraceProblemSummary
}

type dynatraceOAuthToken struct {
	Value     string
	ExpiresAt time.Time
}

func queryDynatrace(ctx context.Context, runner *TestRunner, cfg *DynatraceCaptureConfig, testID string, traceIDs []string, start, end time.Time) dynatraceQueryResult {
	return queryDynatraceWithClient(ctx, runner, cfg, testID, traceIDs, start, end, nil)
}

func queryDynatraceWithClient(ctx context.Context, runner *TestRunner, cfg *DynatraceCaptureConfig, testID string, traceIDs []string, start, end time.Time, client *http.Client) dynatraceQueryResult {
	result := dynatraceQueryResult{Capabilities: map[string]DynatraceCapabilityStatus{}, Metrics: map[string]float64{}, Logs: []DynatraceLogSample{}, Problems: []DynatraceProblemSummary{}}
	if cfg == nil || runner == nil {
		return result
	}
	if cfg.MetricsEnabled {
		metrics, err := queryDynatraceMetrics(ctx, runner, cfg, start, end, client)
		result.Metrics = metrics
		result.Capabilities["metrics"] = dynatraceCapabilityResult(err)
	}
	if cfg.ProblemsEnabled {
		problems, count, open, err := queryDynatraceProblems(ctx, runner, cfg, start, end, client)
		result.Problems = problems
		if err == nil {
			result.Metrics["problems.total"] = count
			result.Metrics["problems.open"] = open
		}
		result.Capabilities["problems"] = dynatraceCapabilityResult(err)
	}
	if cfg.LogsEnabled {
		logs, totals, err := queryDynatraceLogs(ctx, runner, cfg, testID, traceIDs, start, end, client)
		result.Logs = logs
		for key, value := range totals {
			result.Metrics[key] = value
		}
		result.Capabilities["logs"] = dynatraceCapabilityResult(err)
	}
	return result
}

func dynatraceCapabilityResult(err error) DynatraceCapabilityStatus {
	if err != nil {
		return DynatraceCapabilityStatus{Status: "error", Message: err.Error()}
	}
	return DynatraceCapabilityStatus{Status: "connected"}
}

func dynatraceEnvironmentRequest(ctx context.Context, runner *TestRunner, cfg *DynatraceCaptureConfig, method, path string, params url.Values, client *http.Client) (*http.Response, error) {
	endpoint, parsed, err := dynatraceEndpoint(cfg.EnvironmentURL, path, params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	token, found := engine.ResolveSecret(ctx, runner.secretResolverForProject(cfg.ProjectID), cfg.EnvironmentTokenSecret)
	if !found {
		return nil, errors.New("Dynatrace environment token project secret is unavailable")
	}
	req.Header.Set("Authorization", "Api-Token "+token)
	if client == nil {
		client = newHostAllowlistedHTTPClient([]string{parsed.Hostname()}, 8*time.Second)
	}
	return client.Do(req)
}

func dynatraceEndpoint(base, path string, params url.Values) (string, *url.URL, error) {
	parsed, err := url.Parse(base)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return "", nil, errors.New("invalid Dynatrace endpoint")
	}
	endpoint := strings.TrimRight(base, "/") + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	if err := validateOutboundURLAllowingHosts(endpoint, map[string]struct{}{strings.ToLower(parsed.Hostname()): {}}); err != nil {
		return "", nil, errors.New("Dynatrace endpoint is not permitted")
	}
	return endpoint, parsed, nil
}

func queryDynatraceMetrics(ctx context.Context, runner *TestRunner, cfg *DynatraceCaptureConfig, start, end time.Time, client *http.Client) (map[string]float64, error) {
	metrics := map[string]float64{}
	for _, query := range cfg.MetricQueries {
		params := url.Values{"metricSelector": {query.MetricSelector}, "from": {start.UTC().Format(time.RFC3339Nano)}, "to": {end.UTC().Format(time.RFC3339Nano)}, "resolution": {"Inf"}}
		if query.EntitySelector != "" {
			params.Set("entitySelector", query.EntitySelector)
		}
		resp, err := dynatraceEnvironmentRequest(ctx, runner, cfg, http.MethodGet, "/api/v2/metrics/query", params, client)
		if err != nil {
			return metrics, errors.New("Dynatrace metrics request failed")
		}
		data, err := readDynatraceResponse(resp)
		if err != nil {
			return metrics, err
		}
		values, err := decodeDynatraceMetricValues(data)
		if err != nil {
			return metrics, err
		}
		if value, ok := reduceDynatraceValues(values, query.Aggregation); ok {
			metrics[query.Name] = value
		}
	}
	return metrics, nil
}

func decodeDynatraceMetricValues(data []byte) ([]float64, error) {
	var response struct {
		Result []struct {
			Data []struct {
				Values []*float64 `json:"values"`
			} `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, errors.New("Dynatrace metrics returned invalid JSON")
	}
	values := []float64{}
	for _, result := range response.Result {
		for _, series := range result.Data {
			for _, value := range series.Values {
				if value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) {
					values = append(values, *value)
				}
			}
		}
	}
	return values, nil
}

func reduceDynatraceValues(values []float64, operation string) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	value := values[0]
	switch operation {
	case "min":
		for _, item := range values[1:] {
			if item < value {
				value = item
			}
		}
	case "max":
		for _, item := range values[1:] {
			if item > value {
				value = item
			}
		}
	case "sum":
		for _, item := range values[1:] {
			value += item
		}
	default:
		for _, item := range values[1:] {
			value += item
		}
		value /= float64(len(values))
	}
	return value, true
}

func queryDynatraceProblems(ctx context.Context, runner *TestRunner, cfg *DynatraceCaptureConfig, start, end time.Time, client *http.Client) ([]DynatraceProblemSummary, float64, float64, error) {
	params := url.Values{"from": {start.UTC().Format(time.RFC3339Nano)}, "to": {end.UTC().Format(time.RFC3339Nano)}, "pageSize": {"100"}}
	if cfg.ProblemSelector != "" {
		params.Set("problemSelector", cfg.ProblemSelector)
	}
	if cfg.ProblemEntitySelector != "" {
		params.Set("entitySelector", cfg.ProblemEntitySelector)
	}
	var result []DynatraceProblemSummary
	var total, open float64
	seenPages := map[string]bool{}
	// Bound network work independently of the much smaller retained evidence.
	// Never publish a page-limited count as a complete aggregate.
	for page := 0; page < 10; page++ {
		resp, err := dynatraceEnvironmentRequest(ctx, runner, cfg, http.MethodGet, "/api/v2/problems", params, client)
		if err != nil {
			return nil, 0, 0, errors.New("Dynatrace problems request failed")
		}
		data, err := readDynatraceResponse(resp)
		if err != nil {
			return nil, 0, 0, err
		}
		var payload struct {
			TotalCount  int    `json:"totalCount"`
			NextPageKey string `json:"nextPageKey"`
			Problems    []struct {
				ProblemID, DisplayID, Title, SeverityLevel, Status string
				StartTime, EndTime                                 int64
				AffectedEntities                                   []struct {
					Name string `json:"name"`
				} `json:"affectedEntities"`
			} `json:"problems"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, 0, 0, errors.New("Dynatrace problems returned invalid JSON")
		}
		if len(payload.Problems) > 100 {
			return nil, 0, 0, errors.New("Dynatrace problems exceeded the requested page size")
		}
		if page == 0 {
			total = float64(payload.TotalCount)
		}
		for _, problem := range payload.Problems {
			if strings.EqualFold(strings.TrimSpace(problem.Status), "open") {
				open++
			}
			// Keep the bounded page results until merging into retained evidence:
			// an already-retained problem may move onto a later page when closed.
			entity := ""
			if len(problem.AffectedEntities) > 0 {
				entity = problem.AffectedEntities[0].Name
			}
			summary := DynatraceProblemSummary{ID: problem.ProblemID, DisplayID: problem.DisplayID, Title: problem.Title, Severity: problem.SeverityLevel, Status: problem.Status, AffectedEntity: entity}
			if problem.StartTime > 0 {
				summary.StartTime = time.UnixMilli(problem.StartTime).UTC()
			}
			if problem.EndTime > 0 {
				summary.EndTime = time.UnixMilli(problem.EndTime).UTC()
			}
			result = append(result, sanitizeDynatraceProblem(summary))
		}
		if payload.NextPageKey == "" {
			return result, total, open, nil
		}
		if seenPages[payload.NextPageKey] {
			break
		}
		seenPages[payload.NextPageKey] = true
		// The API requires all other query parameters to be omitted on later pages.
		params = url.Values{"nextPageKey": {payload.NextPageKey}}
	}
	return nil, 0, 0, errors.New("Dynatrace problems exceeded the pagination budget; counts are incomplete")
}

func queryDynatraceLogs(ctx context.Context, runner *TestRunner, cfg *DynatraceCaptureConfig, testID string, traceIDs []string, start, end time.Time, client *http.Client) ([]DynatraceLogSample, map[string]float64, error) {
	filter := buildDynatraceLogFilter(cfg, testID, traceIDs, start, end)
	fetch := fmt.Sprintf("fetch logs, from: toTimestamp(\"%s\"), to: toTimestamp(\"%s\")", start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano))
	aggregateQuery := fetch + " | filter " + filter + " | summarize logs_total = count(), errors_total = countIf(" + cfg.LogStatusField + " == \"ERROR\" or " + cfg.LogStatusField + " == \"FATAL\" or " + cfg.LogStatusField + " == \"CRITICAL\")"
	aggregates, err := executeDynatraceDQL(ctx, runner, cfg, aggregateQuery, client)
	if err != nil {
		return nil, nil, err
	}
	fields := strings.Join(compactNonEmptyStrings(cfg.LogTimestampField, cfg.LogStatusField, cfg.LogMessageField, cfg.LogTestIDField, cfg.LogTraceIDField), ", ")
	query := fetch + " | filter " + filter + " | fields " + fields + " | sort " + cfg.LogTimestampField + " desc | limit 100"
	records, err := executeDynatraceDQL(ctx, runner, cfg, query, client)
	if err != nil {
		return nil, nil, err
	}
	logs := make([]DynatraceLogSample, 0, len(records))
	errorsTotal := 0.0
	for _, record := range records {
		sample := DynatraceLogSample{Status: dynatraceRecordString(record, cfg.LogStatusField), Message: dynatraceRecordString(record, cfg.LogMessageField), TestID: dynatraceRecordString(record, cfg.LogTestIDField), TraceID: dynatraceRecordString(record, cfg.LogTraceIDField)}
		if timestamp := dynatraceRecordString(record, cfg.LogTimestampField); timestamp != "" {
			sample.Timestamp, _ = time.Parse(time.RFC3339Nano, timestamp)
		}
		if dynatraceErrorStatus(sample.Status) {
			errorsTotal++
		}
		logs = append(logs, sanitizeDynatraceLog(sample))
	}
	totals := map[string]float64{"logs.total": float64(len(records)), "logs.errors": errorsTotal}
	if len(aggregates) > 0 {
		if value, ok := dynatraceRecordNumber(aggregates[0], "logs_total"); ok {
			totals["logs.total"] = value
		}
		if value, ok := dynatraceRecordNumber(aggregates[0], "errors_total"); ok {
			totals["logs.errors"] = value
		}
	}
	return logs, totals, nil
}

func buildDynatraceLogFilter(cfg *DynatraceCaptureConfig, testID string, traceIDs []string, start, end time.Time) string {
	parts := []string{fmt.Sprintf("%s >= toTimestamp(\"%s\") and %s <= toTimestamp(\"%s\")", cfg.LogTimestampField, start.UTC().Format(time.RFC3339Nano), cfg.LogTimestampField, end.UTC().Format(time.RFC3339Nano))}
	if strings.TrimSpace(cfg.LogBaseFilter) != "" {
		parts = append(parts, "("+cfg.LogBaseFilter+")")
	}
	if testID != "" && cfg.LogTestIDField != "" {
		parts = append(parts, cfg.LogTestIDField+" == \""+escapeDQLString(testID)+"\"")
	} else if cfg.LogTraceIDField != "" && len(traceIDs) > 0 {
		if len(traceIDs) > 64 {
			traceIDs = traceIDs[:64]
		}
		values := make([]string, 0, len(traceIDs))
		for _, id := range traceIDs {
			values = append(values, "\""+escapeDQLString(id)+"\"")
		}
		parts = append(parts, "in(toString("+cfg.LogTraceIDField+"), {"+strings.Join(values, ",")+"})")
	}
	return strings.Join(parts, " and ")
}

func escapeDQLString(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r").Replace(value)
}

func executeDynatraceDQL(ctx context.Context, runner *TestRunner, cfg *DynatraceCaptureConfig, query string, client *http.Client) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"query": query, "requestTimeoutMilliseconds": 5000})
	endpoint, parsed, err := dynatraceEndpoint(cfg.PlatformURL, "/platform/storage/query/v1/query:execute", nil)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyDynatracePlatformAuthentication(ctx, runner, cfg, req, client); err != nil {
		return nil, err
	}
	if client == nil {
		client = newHostAllowlistedHTTPClient([]string{parsed.Hostname()}, 8*time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Dynatrace Grail request failed")
	}
	data, err := readDynatraceResponse(resp)
	if err != nil {
		return nil, err
	}
	records, token, state, err := decodeDynatraceDQLResponse(data)
	if err != nil {
		return nil, err
	}
	switch state {
	case "", "SUCCEEDED":
		return records, nil
	case "RUNNING", "NOT_STARTED":
		if token == "" {
			return nil, errors.New("Dynatrace Grail query did not provide a polling token")
		}
	default:
		return nil, errors.New("Dynatrace Grail query did not complete successfully")
	}
	for attempt := 0; attempt < 3; attempt++ {
		// A running poll response has no requestToken; always reuse the execute token.
		params := url.Values{"request-token": {token}, "request-timeout-milliseconds": {"5000"}}
		pollEndpoint, _, endpointErr := dynatraceEndpoint(cfg.PlatformURL, "/platform/storage/query/v1/query:poll", params)
		if endpointErr != nil {
			return nil, endpointErr
		}
		pollReq, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, pollEndpoint, nil)
		if requestErr != nil {
			return nil, requestErr
		}
		if authErr := applyDynatracePlatformAuthentication(ctx, runner, cfg, pollReq, client); authErr != nil {
			return nil, authErr
		}
		pollResp, requestErr := client.Do(pollReq)
		if requestErr != nil {
			return nil, errors.New("Dynatrace Grail poll failed")
		}
		pollData, responseErr := readDynatraceResponse(pollResp)
		if responseErr != nil {
			return nil, responseErr
		}
		records, _, state, err = decodeDynatraceDQLResponse(pollData)
		if err != nil {
			return nil, err
		}
		if state == "SUCCEEDED" {
			return records, nil
		}
		if state != "RUNNING" && state != "NOT_STARTED" {
			return nil, errors.New("Dynatrace Grail query did not complete successfully")
		}
		// Avoid a tight loop if the server returns before its requested wait budget.
		if attempt < 2 {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, errors.New("Dynatrace Grail query did not complete within the polling budget")
}

func applyDynatracePlatformAuthentication(ctx context.Context, runner *TestRunner, cfg *DynatraceCaptureConfig, req *http.Request, client *http.Client) error {
	resolver := runner.secretResolverForProject(cfg.ProjectID)
	switch cfg.PlatformAuthType {
	case storage.DynatracePlatformAuthToken:
		token, found := engine.ResolveSecret(ctx, resolver, cfg.PlatformTokenSecret)
		if !found {
			return errors.New("Dynatrace platform token project secret is unavailable")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	case storage.DynatracePlatformAuthOAuth:
		cacheKey := strings.Join([]string{cfg.ProjectID, cfg.ConnectionID, cfg.OAuthClientIDSecret, cfg.OAuthClientSecretSecret, cfg.OAuthResource, strings.Join(cfg.OAuthScopes, " ")}, "\x00")
		refresh := runner.dynatraceOAuthRefresh.DoChan(cacheKey, func() (any, error) {
			runner.dynatraceOAuthMu.Lock()
			cached := runner.dynatraceOAuthTokens[cacheKey]
			runner.dynatraceOAuthMu.Unlock()
			if cached.Value != "" && time.Now().Before(cached.ExpiresAt.Add(-30*time.Second)) {
				return cached.Value, nil
			}
			clientID, idOK := engine.ResolveSecret(ctx, resolver, cfg.OAuthClientIDSecret)
			clientSecret, secretOK := engine.ResolveSecret(ctx, resolver, cfg.OAuthClientSecretSecret)
			if !idOK || !secretOK {
				return nil, errors.New("Dynatrace OAuth project secrets are unavailable")
			}
			form := url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {clientSecret}, "resource": {cfg.OAuthResource}, "scope": {strings.Join(cfg.OAuthScopes, " ")}}
			tokenReq, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, "https://sso.dynatrace.com/sso/oauth2/token", strings.NewReader(form.Encode()))
			if requestErr != nil {
				return nil, errors.New("Dynatrace OAuth token request could not be created")
			}
			tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			tokenClient := client
			if tokenClient == nil {
				tokenClient = newHostAllowlistedHTTPClient([]string{"sso.dynatrace.com"}, 8*time.Second)
			}
			response, requestErr := tokenClient.Do(tokenReq)
			if requestErr != nil {
				return nil, errors.New("Dynatrace OAuth token request failed")
			}
			data, responseErr := readDynatraceResponse(response)
			if responseErr != nil {
				return nil, responseErr
			}
			var token struct {
				AccessToken string `json:"access_token"`
				ExpiresIn   int    `json:"expires_in"`
			}
			if json.Unmarshal(data, &token) != nil || token.AccessToken == "" {
				return nil, errors.New("Dynatrace OAuth response did not contain an access token")
			}
			if token.ExpiresIn <= 0 {
				token.ExpiresIn = 300
			}
			runner.dynatraceOAuthMu.Lock()
			if runner.dynatraceOAuthTokens == nil {
				runner.dynatraceOAuthTokens = make(map[string]dynatraceOAuthToken)
			}
			runner.dynatraceOAuthTokens[cacheKey] = dynatraceOAuthToken{Value: token.AccessToken, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)}
			runner.dynatraceOAuthMu.Unlock()
			return token.AccessToken, nil
		})
		var value any
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-refresh:
			if result.Err != nil {
				return result.Err
			}
			value = result.Val
		}
		token, ok := value.(string)
		if !ok || token == "" {
			return errors.New("Dynatrace OAuth response did not contain an access token")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	default:
		return errors.New("Dynatrace platform authentication is not configured")
	}
}

func readDynatraceResponse(resp *http.Response) ([]byte, error) {
	if resp == nil {
		return nil, errors.New("Dynatrace returned no response")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Dynatrace returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDynatraceResponseBytes+1))
	if err != nil || len(data) > maxDynatraceResponseBytes {
		return nil, errors.New("Dynatrace response is unreadable or too large")
	}
	return data, nil
}

func decodeDynatraceDQLResponse(data []byte) ([]map[string]any, string, string, error) {
	var payload struct {
		Result struct {
			Records []map[string]any `json:"records"`
		} `json:"result"`
		Records      []map[string]any `json:"records"`
		RequestToken string           `json:"requestToken"`
		State        string           `json:"state"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, "", "", errors.New("Dynatrace Grail returned invalid JSON")
	}
	if payload.Result.Records != nil {
		return payload.Result.Records, payload.RequestToken, strings.ToUpper(payload.State), nil
	}
	if payload.Records != nil {
		return payload.Records, payload.RequestToken, strings.ToUpper(payload.State), nil
	}
	return []map[string]any{}, payload.RequestToken, strings.ToUpper(payload.State), nil
}

func decodeDynatraceDQLRecords(data []byte) ([]map[string]any, error) {
	records, _, _, err := decodeDynatraceDQLResponse(data)
	return records, err
}

func dynatraceRecordString(record map[string]any, field string) string {
	if field == "" {
		return ""
	}
	if value, ok := record[field]; ok {
		return fmt.Sprint(value)
	}
	return elkStringField(record, field)
}

func dynatraceRecordNumber(record map[string]any, field string) (float64, bool) {
	value, ok := record[field]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
func dynatraceErrorStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "error", "fatal", "critical", "severe":
		return true
	}
	return false
}
func sanitizeDynatraceText(value string, limit int) string {
	return boundedtext.TruncateUTF8(scrubELKText(scrub.Value(value, scrub.Policy{})), limit)
}
func sanitizeDynatraceLog(sample DynatraceLogSample) DynatraceLogSample {
	sample.Status = sanitizeDynatraceText(sample.Status, 128)
	sample.Message = sanitizeDynatraceText(sample.Message, maxDynatraceSampleText)
	sample.TestID = sanitizeDynatraceText(sample.TestID, maxDynatraceFieldText)
	sample.TraceID = sanitizeDynatraceText(sample.TraceID, maxDynatraceFieldText)
	return sample
}
func sanitizeDynatraceProblem(problem DynatraceProblemSummary) DynatraceProblemSummary {
	problem.ID = sanitizeDynatraceText(problem.ID, maxDynatraceFieldText)
	problem.DisplayID = sanitizeDynatraceText(problem.DisplayID, maxDynatraceFieldText)
	problem.Title = sanitizeDynatraceText(problem.Title, maxDynatraceSampleText)
	problem.Severity = sanitizeDynatraceText(problem.Severity, 128)
	problem.Status = sanitizeDynatraceText(problem.Status, 128)
	problem.AffectedEntity = sanitizeDynatraceText(problem.AffectedEntity, maxDynatraceFieldText)
	return problem
}

func dynatraceExternalMetrics(connectionID string, values map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(values))
	for key, value := range values {
		result["dynatrace."+connectionID+"."+key] = value
	}
	return result
}

func dynatraceEvidenceKey(record DynatraceEvidenceRecord) string {
	if record.Log != nil {
		return "log|" + record.Log.Timestamp.UTC().Format(time.RFC3339Nano) + "|" + record.Log.TraceID + "|" + record.Log.Message
	}
	if record.Problem != nil {
		return "problem|" + firstNonEmpty(record.Problem.ID, record.Problem.DisplayID, record.Problem.Title+"|"+strconv.FormatInt(record.Problem.StartTime.UnixMilli(), 10))
	}
	return ""
}
