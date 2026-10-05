package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	maxELKResponseBytes    = 2 << 20
	maxELKSampleIDBytes    = 512
	maxELKSampleLevelBytes = 128
	maxELKSampleTextBytes  = 4096
	maxELKCorrelationBytes = 512
)

var elkAuthorizationPattern = regexp.MustCompile(`(?i)(\bauthorization\s*[:=]\s*(?:bearer|basic|api\s*key|apikey)\s+)[^\s,;]+`)

// ELKCaptureConfig is the safe, immutable per-run snapshot. Project identity
// and secret names are process-only and never appear in API or archive JSON.
type ELKCaptureConfig struct {
	ConnectionID       string                         `json:"connection_id"`
	ConnectionName     string                         `json:"connection_name,omitempty"`
	Endpoint           string                         `json:"-"`
	IndexPattern       string                         `json:"-"`
	TimestampField     string                         `json:"-"`
	LevelField         string                         `json:"-"`
	MessageField       string                         `json:"-"`
	TestIDField        string                         `json:"-"`
	TraceIDField       string                         `json:"-"`
	BaseFilter         string                         `json:"-"`
	ErrorFilter        string                         `json:"-"`
	MetricAggregations []storage.ELKMetricAggregation `json:"-"`
	ProjectID          string                         `json:"-"`
	AuthType           string                         `json:"-"`
	APIKeySecretName   string                         `json:"-"`
	UsernameSecretName string                         `json:"-"`
	PasswordSecretName string                         `json:"-"`
}

type storedELKCapture struct {
	ConnectionID       string                         `json:"connection_id"`
	ConnectionName     string                         `json:"connection_name,omitempty"`
	Endpoint           string                         `json:"endpoint"`
	IndexPattern       string                         `json:"index_pattern"`
	TimestampField     string                         `json:"timestamp_field"`
	LevelField         string                         `json:"level_field,omitempty"`
	MessageField       string                         `json:"message_field,omitempty"`
	TestIDField        string                         `json:"test_id_field,omitempty"`
	TraceIDField       string                         `json:"trace_id_field,omitempty"`
	BaseFilter         string                         `json:"base_filter,omitempty"`
	ErrorFilter        string                         `json:"error_filter,omitempty"`
	MetricAggregations []storage.ELKMetricAggregation `json:"metric_aggregations,omitempty"`
	ProjectID          string                         `json:"project_id"`
	AuthType           string                         `json:"auth_type"`
	APIKeySecretName   string                         `json:"api_key_secret_name,omitempty"`
	UsernameSecretName string                         `json:"username_secret_name,omitempty"`
	PasswordSecretName string                         `json:"password_secret_name,omitempty"`
}

type ELKCaptureStatus struct {
	ConnectionID     string             `json:"connection_id"`
	ConnectionName   string             `json:"connection_name,omitempty"`
	Status           string             `json:"status"`
	LastPolledAt     *time.Time         `json:"last_polled_at,omitempty"`
	LastSuccessfulAt *time.Time         `json:"last_successful_at,omitempty"`
	Message          string             `json:"message,omitempty"`
	Metrics          map[string]float64 `json:"metrics,omitempty"`
	Samples          []ELKLogSample     `json:"samples,omitempty"`
	// Observation labels the legacy capture's values as cumulative from the
	// run start; they are never reinterpreted as interval buckets.
	Observation string `json:"observation,omitempty"`
}

type ELKLogSample struct {
	ID        string    `json:"id,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Level     string    `json:"level,omitempty"`
	Message   string    `json:"message,omitempty"`
	TestID    string    `json:"test_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
}

type ELKConnectionRequest struct {
	Name               string                         `json:"name"`
	Description        string                         `json:"description"`
	Enabled            bool                           `json:"enabled"`
	Endpoint           string                         `json:"endpoint"`
	IndexPattern       string                         `json:"index_pattern"`
	TimestampField     string                         `json:"timestamp_field"`
	LevelField         string                         `json:"level_field"`
	MessageField       string                         `json:"message_field"`
	TestIDField        string                         `json:"test_id_field"`
	TraceIDField       string                         `json:"trace_id_field"`
	BaseFilter         string                         `json:"base_filter"`
	ErrorFilter        string                         `json:"error_filter"`
	AuthType           string                         `json:"auth_type"`
	APIKeySecretName   *string                        `json:"api_key_secret_name,omitempty"`
	UsernameSecretName *string                        `json:"username_secret_name,omitempty"`
	PasswordSecretName *string                        `json:"password_secret_name,omitempty"`
	MetricAggregations []storage.ELKMetricAggregation `json:"metric_aggregations"`
	UpdatedAt          time.Time                      `json:"updated_at"`
}

type ELKConnectionResponse struct {
	ID                 string                         `json:"id"`
	ProjectID          string                         `json:"project_id"`
	Name               string                         `json:"name"`
	Description        string                         `json:"description,omitempty"`
	Enabled            bool                           `json:"enabled"`
	Status             string                         `json:"status"`
	Endpoint           string                         `json:"endpoint,omitempty"`
	IndexPattern       string                         `json:"index_pattern,omitempty"`
	TimestampField     string                         `json:"timestamp_field,omitempty"`
	LevelField         string                         `json:"level_field,omitempty"`
	MessageField       string                         `json:"message_field,omitempty"`
	TestIDField        string                         `json:"test_id_field,omitempty"`
	TraceIDField       string                         `json:"trace_id_field,omitempty"`
	BaseFilter         string                         `json:"base_filter,omitempty"`
	ErrorFilter        string                         `json:"error_filter,omitempty"`
	AuthType           string                         `json:"auth_type,omitempty"`
	HasAPIKey          bool                           `json:"has_api_key"`
	HasBasicUsername   bool                           `json:"has_basic_username"`
	HasBasicPassword   bool                           `json:"has_basic_password"`
	MetricAggregations []storage.ELKMetricAggregation `json:"metric_aggregations"`
	LastCheckedAt      *time.Time                     `json:"last_checked_at,omitempty"`
	LastCheckStatus    string                         `json:"last_check_status,omitempty"`
	LastCheckMessage   string                         `json:"last_check_message,omitempty"`
	CreatedAt          time.Time                      `json:"created_at"`
	UpdatedAt          time.Time                      `json:"updated_at"`
}

type ELKConnectionSelectableResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}

func elkConnectionResponse(c *storage.ELKConnection) ELKConnectionResponse {
	return ELKConnectionResponse{ID: c.ID, ProjectID: c.ProjectID, Name: c.Name, Description: c.Description, Enabled: c.Enabled,
		Status: c.ConfigurationStatus(), Endpoint: c.Endpoint, IndexPattern: c.IndexPattern, TimestampField: c.TimestampField,
		LevelField: c.LevelField, MessageField: c.MessageField, TestIDField: c.TestIDField, TraceIDField: c.TraceIDField,
		BaseFilter: c.BaseFilter, ErrorFilter: c.ErrorFilter, AuthType: c.AuthType, HasAPIKey: c.APIKeySecretName != "",
		HasBasicUsername: c.UsernameSecretName != "", HasBasicPassword: c.PasswordSecretName != "",
		MetricAggregations: append([]storage.ELKMetricAggregation(nil), c.MetricAggregations...), LastCheckedAt: c.LastCheckedAt,
		LastCheckStatus: c.LastCheckStatus, LastCheckMessage: c.LastCheckMessage, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func applyELKConnectionRequest(c *storage.ELKConnection, req ELKConnectionRequest) {
	c.Name, c.Description, c.Enabled = req.Name, req.Description, req.Enabled
	c.Endpoint, c.IndexPattern, c.TimestampField = req.Endpoint, req.IndexPattern, req.TimestampField
	c.LevelField, c.MessageField, c.TestIDField, c.TraceIDField = req.LevelField, req.MessageField, req.TestIDField, req.TraceIDField
	c.BaseFilter, c.ErrorFilter, c.AuthType = req.BaseFilter, req.ErrorFilter, req.AuthType
	c.MetricAggregations = append([]storage.ELKMetricAggregation(nil), req.MetricAggregations...)
	if req.APIKeySecretName != nil {
		c.APIKeySecretName = strings.TrimSpace(*req.APIKeySecretName)
	}
	if req.UsernameSecretName != nil {
		c.UsernameSecretName = strings.TrimSpace(*req.UsernameSecretName)
	}
	if req.PasswordSecretName != nil {
		c.PasswordSecretName = strings.TrimSpace(*req.PasswordSecretName)
	}
	switch c.AuthType {
	case storage.ELKAuthAPIKey:
		c.UsernameSecretName, c.PasswordSecretName = "", ""
	case storage.ELKAuthBasic:
		c.APIKeySecretName = ""
	default:
		c.APIKeySecretName, c.UsernameSecretName, c.PasswordSecretName = "", "", ""
	}
	if strings.TrimSpace(c.TimestampField) == "" {
		c.TimestampField = "@timestamp"
	}
}

func (s *Server) elkConnectionStore(w http.ResponseWriter) (storage.ELKConnectionStore, bool) {
	store, ok := s.store.(storage.ELKConnectionStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "ELK connection storage is unavailable")
		return nil, false
	}
	return store, true
}

func (s *Server) handleCreateELKConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.elkConnectionStore(w)
	if !ok {
		return
	}
	var req ELKConnectionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	c := &storage.ELKConnection{ID: uuid.NewString(), ProjectID: strings.TrimSpace(r.PathValue("id"))}
	applyELKConnectionRequest(c, req)
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := store.CreateELKConnection(c); err != nil {
		writeError(w, 409, "failed to create ELK connection: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "monitoring.elk.create", "success", "elk_connection", c.ID, c.ProjectID, map[string]any{"name": c.Name, "status": c.ConfigurationStatus()})
	writeJSON(w, http.StatusCreated, elkConnectionResponse(c))
}

func (s *Server) handleListELKConnections(w http.ResponseWriter, r *http.Request) {
	store, ok := s.elkConnectionStore(w)
	if !ok {
		return
	}
	items, err := store.ListELKConnections(strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		writeError(w, 500, "failed to list ELK connections")
		return
	}
	projectID := strings.TrimSpace(r.PathValue("id"))
	user, _ := AuthenticatedUserFromContext(r.Context())
	canManage := s.authSettingsForRequest().Mode == authModeDev || (user != nil &&
		(user.hasGlobalRole(RoleProjectAdmin, RoleSystemAdmin) || userHasProjectOrParentRole(s.store, user, projectID, RoleProjectAdmin)))
	if canManage {
		responses := make([]ELKConnectionResponse, 0, len(items))
		for _, item := range items {
			responses = append(responses, elkConnectionResponse(item))
		}
		writeJSON(w, 200, map[string]any{"connections": responses, "count": len(responses)})
		return
	}
	responses := make([]ELKConnectionSelectableResponse, 0, len(items))
	for _, item := range items {
		if item.ConfigurationStatus() == "ready" {
			responses = append(responses, ELKConnectionSelectableResponse{ID: item.ID, ProjectID: item.ProjectID, Name: item.Name, Status: "ready"})
		}
	}
	writeJSON(w, 200, map[string]any{"connections": responses, "count": len(responses)})
}

func (s *Server) loadProjectELKConnection(w http.ResponseWriter, r *http.Request) (*storage.ELKConnection, storage.ELKConnectionStore, bool) {
	store, ok := s.elkConnectionStore(w)
	if !ok {
		return nil, nil, false
	}
	c, err := store.GetELKConnection(strings.TrimSpace(r.PathValue("connection_id")))
	if errors.Is(err, sql.ErrNoRows) || c == nil || c.ProjectID != strings.TrimSpace(r.PathValue("id")) {
		writeError(w, 404, "ELK connection not found")
		return nil, nil, false
	}
	if err != nil {
		writeError(w, 500, "failed to load ELK connection")
		return nil, nil, false
	}
	return c, store, true
}

func (s *Server) handleGetELKConnection(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.loadProjectELKConnection(w, r)
	if ok {
		writeJSON(w, 200, elkConnectionResponse(c))
	}
}

func (s *Server) handleUpdateELKConnection(w http.ResponseWriter, r *http.Request) {
	c, store, ok := s.loadProjectELKConnection(w, r)
	if !ok {
		return
	}
	var req ELKConnectionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.UpdatedAt.IsZero() {
		writeError(w, 400, "updated_at is required")
		return
	}
	applyELKConnectionRequest(c, req)
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	updated, err := store.UpdateELKConnectionIfUnchanged(c, req.UpdatedAt)
	if err != nil {
		writeError(w, 409, "failed to update ELK connection: "+err.Error())
		return
	}
	if !updated {
		writeError(w, 409, "ELK connection changed since it was loaded; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.elk.update", "success", "elk_connection", c.ID, c.ProjectID, map[string]any{"status": c.ConfigurationStatus()})
	writeJSON(w, 200, elkConnectionResponse(c))
}

func (s *Server) handleDeleteELKConnection(w http.ResponseWriter, r *http.Request) {
	c, store, ok := s.loadProjectELKConnection(w, r)
	if !ok {
		return
	}
	deleted, err := store.DeleteELKConnection(c.ID, c.ProjectID)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if !deleted {
		writeError(w, 404, "ELK connection not found")
		return
	}
	s.recordAuditEvent(r, "monitoring.elk.delete", "success", "elk_connection", c.ID, c.ProjectID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestELKConnection(w http.ResponseWriter, r *http.Request) {
	c, store, ok := s.loadProjectELKConnection(w, r)
	if !ok {
		return
	}
	checkedAt := time.Now().UTC()
	check := *c
	check.Enabled = true // Verify saved configuration without enabling it.
	if check.ConfigurationStatus() != "ready" {
		writeError(w, 409, "ELK connection is not fully configured")
		return
	}
	result, err := queryELK(r.Context(), s.runner, elkCaptureFromConnection(c), "", nil, checkedAt.Add(-time.Minute), checkedAt)
	status, message := "connected", "Elasticsearch search API and index read access verified"
	if err != nil {
		status, message = "error", "ELK connection test failed: "+err.Error()
	}
	_ = store.UpdateELKConnectionHealth(c.ID, c.ProjectID, status, message, checkedAt)
	if err != nil {
		writeError(w, 502, message)
		return
	}
	writeJSON(w, 200, map[string]any{"status": status, "checked_at": checkedAt, "message": message, "matching_documents": result.Metrics["logs_total"]})
}

func elkCaptureFromConnection(c *storage.ELKConnection) *ELKCaptureConfig {
	if c == nil {
		return nil
	}
	return &ELKCaptureConfig{ConnectionID: c.ID, ConnectionName: c.Name, Endpoint: c.Endpoint, IndexPattern: c.IndexPattern,
		TimestampField: c.TimestampField, LevelField: c.LevelField, MessageField: c.MessageField, TestIDField: c.TestIDField,
		TraceIDField: c.TraceIDField, BaseFilter: c.BaseFilter, ErrorFilter: c.ErrorFilter,
		MetricAggregations: append([]storage.ELKMetricAggregation(nil), c.MetricAggregations...), ProjectID: c.ProjectID, AuthType: c.AuthType,
		APIKeySecretName: c.APIKeySecretName, UsernameSecretName: c.UsernameSecretName, PasswordSecretName: c.PasswordSecretName}
}

func cloneELKCaptureConfig(config *ELKCaptureConfig) *ELKCaptureConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	cloned.MetricAggregations = append([]storage.ELKMetricAggregation(nil), config.MetricAggregations...)
	return &cloned
}

func elkCaptureForStorage(config *ELKCaptureConfig) any {
	if config == nil {
		return nil
	}
	return storedELKCapture{ConnectionID: config.ConnectionID, ConnectionName: config.ConnectionName, Endpoint: config.Endpoint,
		IndexPattern: config.IndexPattern, TimestampField: config.TimestampField, LevelField: config.LevelField,
		MessageField: config.MessageField, TestIDField: config.TestIDField, TraceIDField: config.TraceIDField,
		BaseFilter: config.BaseFilter, ErrorFilter: config.ErrorFilter, MetricAggregations: append([]storage.ELKMetricAggregation(nil), config.MetricAggregations...),
		ProjectID: config.ProjectID, AuthType: config.AuthType, APIKeySecretName: config.APIKeySecretName,
		UsernameSecretName: config.UsernameSecretName, PasswordSecretName: config.PasswordSecretName}
}

func cloneELKCaptureStatus(status *ELKCaptureStatus) *ELKCaptureStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	cloned.LastPolledAt = cloneTimePtr(status.LastPolledAt)
	cloned.LastSuccessfulAt = cloneTimePtr(status.LastSuccessfulAt)
	cloned.Metrics = cloneFloat64Map(status.Metrics)
	cloned.Samples = append([]ELKLogSample(nil), status.Samples...)
	return &cloned
}

func (tr *TestRunner) resolveELKCaptureForTest(projectID string, requested *ELKCaptureConfig) (*ELKCaptureConfig, error) {
	if requested == nil || strings.TrimSpace(requested.ConnectionID) == "" {
		return nil, nil
	}
	if tr == nil || tr.elkStore == nil {
		return nil, errors.New("ELK connections are unavailable")
	}
	connection, err := tr.elkStore.GetELKConnection(strings.TrimSpace(requested.ConnectionID))
	if errors.Is(err, sql.ErrNoRows) || connection == nil {
		return nil, errors.New("selected ELK connection was not found")
	}
	if err != nil {
		return nil, errors.New("selected ELK connection could not be loaded")
	}
	if normalizeProjectID(connection.ProjectID) != normalizeProjectID(projectID) {
		return nil, errors.New("selected ELK connection belongs to a different project")
	}
	if connection.ConfigurationStatus() != "ready" {
		return nil, errors.New("selected ELK connection is not fully configured and enabled")
	}
	return elkCaptureFromConnection(connection), nil
}

type elkQueryResult struct {
	Metrics map[string]float64
	Samples []ELKLogSample
}

func queryELK(ctx context.Context, runner *TestRunner, cfg *ELKCaptureConfig, testID string, traceIDs []string, start, end time.Time) (elkQueryResult, error) {
	return queryELKWithClient(ctx, runner, cfg, testID, traceIDs, start, end, nil)
}

func queryELKWithClient(ctx context.Context, runner *TestRunner, cfg *ELKCaptureConfig, testID string, traceIDs []string, start, end time.Time, client *http.Client) (elkQueryResult, error) {
	if cfg == nil {
		return elkQueryResult{}, errors.New("ELK capture is not configured")
	}
	if runner == nil {
		return elkQueryResult{}, errors.New("ELK secret resolution is unavailable")
	}
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return elkQueryResult{}, errors.New("invalid ELK endpoint")
	}
	allowed := map[string]struct{}{strings.ToLower(parsed.Hostname()): {}}
	endpoint := strings.TrimRight(cfg.Endpoint, "/") + "/" + url.PathEscape(cfg.IndexPattern) + "/_search"
	if err := validateOutboundURLAllowingHosts(endpoint, allowed); err != nil {
		return elkQueryResult{}, errors.New("ELK endpoint is not permitted")
	}
	body := buildELKSearchBody(cfg, testID, traceIDs, start, end)
	payload, err := json.Marshal(body)
	if err != nil {
		return elkQueryResult{}, errors.New("ELK search request could not be encoded")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return elkQueryResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resolver := runner.secretResolverForProject(cfg.ProjectID)
	if err := applyELKAuthentication(ctx, req, resolver, cfg); err != nil {
		return elkQueryResult{}, err
	}
	if client == nil {
		client = newHostAllowlistedHTTPClient([]string{parsed.Hostname()}, 5*time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return elkQueryResult{}, errors.New("Elasticsearch request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return elkQueryResult{}, fmt.Errorf("Elasticsearch returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxELKResponseBytes+1))
	if err != nil || len(data) > maxELKResponseBytes {
		return elkQueryResult{}, errors.New("Elasticsearch response is unreadable or too large")
	}
	return decodeELKQueryResult(data, cfg)
}

func applyELKAuthentication(ctx context.Context, req *http.Request, resolver engine.SecretResolver, cfg *ELKCaptureConfig) error {
	if req == nil || cfg == nil {
		return errors.New("ELK request and capture configuration are required")
	}
	switch cfg.AuthType {
	case storage.ELKAuthAPIKey:
		value, found := engine.ResolveSecret(ctx, resolver, cfg.APIKeySecretName)
		if !found {
			return errors.New("ELK API key project secret is unavailable")
		}
		req.Header.Set("Authorization", "ApiKey "+value)
	case storage.ELKAuthBasic:
		username, userOK := engine.ResolveSecret(ctx, resolver, cfg.UsernameSecretName)
		password, passwordOK := engine.ResolveSecret(ctx, resolver, cfg.PasswordSecretName)
		if !userOK || !passwordOK {
			return errors.New("ELK basic-auth project secrets are unavailable")
		}
		req.SetBasicAuth(username, password)
	default:
		return errors.New("ELK authentication is not configured")
	}
	return nil
}

func buildELKSearchBody(cfg *ELKCaptureConfig, testID string, traceIDs []string, start, end time.Time) map[string]any {
	filters := []any{map[string]any{"range": map[string]any{cfg.TimestampField: map[string]any{"gte": start.UTC().Format(time.RFC3339Nano), "lte": end.UTC().Format(time.RFC3339Nano)}}}}
	if strings.TrimSpace(cfg.BaseFilter) != "" {
		filters = append(filters, map[string]any{"query_string": map[string]any{"query": cfg.BaseFilter}})
	}
	if testID != "" && strings.TrimSpace(cfg.TestIDField) != "" {
		filters = append(filters, map[string]any{"term": map[string]any{cfg.TestIDField: testID}})
	} else if strings.TrimSpace(cfg.TraceIDField) != "" && len(traceIDs) > 0 {
		if len(traceIDs) > 64 {
			traceIDs = traceIDs[:64]
		}
		filters = append(filters, map[string]any{"terms": map[string]any{cfg.TraceIDField: traceIDs}})
	}
	aggs := map[string]any{}
	if strings.TrimSpace(cfg.ErrorFilter) != "" {
		aggs["errors_total"] = map[string]any{"filter": map[string]any{"query_string": map[string]any{"query": cfg.ErrorFilter}}}
	} else if strings.TrimSpace(cfg.LevelField) != "" {
		aggs["errors_total"] = map[string]any{"filter": map[string]any{"terms": map[string]any{cfg.LevelField: []string{"error", "fatal", "critical"}}}}
	} else {
		aggs["errors_total"] = map[string]any{"filter": map[string]any{"match_none": map[string]any{}}}
	}
	for _, metric := range cfg.MetricAggregations {
		aggs["metric_"+metric.Name] = map[string]any{metric.Operation: map[string]any{"field": metric.Field}}
	}
	fields := compactNonEmptyStrings(cfg.TimestampField, cfg.LevelField, cfg.MessageField, cfg.TestIDField, cfg.TraceIDField)
	return map[string]any{"size": elkSearchSampleLimit, "track_total_hits": true, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{cfg.TimestampField: "desc"}}, "_source": fields, "aggs": aggs}
}

func decodeELKQueryResult(data []byte, cfg *ELKCaptureConfig) (elkQueryResult, error) {
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		return elkQueryResult{}, errors.New("Elasticsearch returned invalid JSON")
	}
	if timedOut, _ := response["timed_out"].(bool); timedOut {
		return elkQueryResult{}, errors.New("Elasticsearch search timed out; results are incomplete")
	}
	if shards, ok := response["_shards"].(map[string]any); ok {
		if failed, _ := shards["failed"].(float64); failed > 0 {
			return elkQueryResult{}, errors.New("Elasticsearch search has failed shards; results are incomplete")
		}
	}
	if terminated, _ := response["terminated_early"].(bool); terminated {
		return elkQueryResult{}, errors.New("Elasticsearch search terminated early; results are incomplete")
	}
	result := elkQueryResult{Metrics: map[string]float64{"logs_total": 0, "errors_total": 0}, Samples: []ELKLogSample{}}
	hits, ok := response["hits"].(map[string]any)
	if !ok {
		return elkQueryResult{}, errors.New("Elasticsearch response is missing search results")
	}
	switch total := hits["total"].(type) {
	case float64:
		result.Metrics["logs_total"] = total
	case map[string]any:
		if relation, _ := total["relation"].(string); relation != "" && relation != "eq" {
			return elkQueryResult{}, errors.New("Elasticsearch search returned an incomplete hit count")
		}
		result.Metrics["logs_total"], _ = total["value"].(float64)
	}
	if aggregations, ok := response["aggregations"].(map[string]any); ok {
		if errorsAgg, ok := aggregations["errors_total"].(map[string]any); ok {
			result.Metrics["errors_total"], _ = errorsAgg["doc_count"].(float64)
		}
		for _, metric := range cfg.MetricAggregations {
			if agg, ok := aggregations["metric_"+metric.Name].(map[string]any); ok {
				if value, ok := agg["value"].(float64); ok {
					result.Metrics[metric.Name] = value
				}
			}
		}
	}
	rows, _ := hits["hits"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		source, _ := row["_source"].(map[string]any)
		if source == nil {
			continue
		}
		var id string
		if rawID, ok := row["_id"]; ok && rawID != nil {
			id = strings.TrimSpace(fmt.Sprint(rawID))
		}
		sample := ELKLogSample{ID: id, Level: elkStringField(source, cfg.LevelField), Message: elkStringField(source, cfg.MessageField), TestID: elkStringField(source, cfg.TestIDField), TraceID: elkStringField(source, cfg.TraceIDField)}
		if timestamp := elkStringField(source, cfg.TimestampField); timestamp != "" {
			sample.Timestamp, _ = time.Parse(time.RFC3339Nano, timestamp)
		}
		result.Samples = append(result.Samples, sanitizeELKSample(sample))
	}
	return result, nil
}

func scrubELKText(value string) string {
	value = scrub.Value(value, scrub.Policy{})
	return elkAuthorizationPattern.ReplaceAllString(value, `${1}`+scrub.Redacted)
}

func sanitizeELKSample(sample ELKLogSample) ELKLogSample {
	sample.ID = boundedtext.TruncateUTF8(scrubELKText(sample.ID), maxELKSampleIDBytes)
	sample.Level = boundedtext.TruncateUTF8(scrubELKText(sample.Level), maxELKSampleLevelBytes)
	sample.Message = boundedtext.TruncateUTF8(scrubELKText(sample.Message), maxELKSampleTextBytes)
	sample.TestID = boundedtext.TruncateUTF8(scrubELKText(sample.TestID), maxELKCorrelationBytes)
	sample.TraceID = boundedtext.TruncateUTF8(scrubELKText(sample.TraceID), maxELKCorrelationBytes)
	return sample
}

func elkStringField(source map[string]any, field string) string {
	if strings.TrimSpace(field) == "" {
		return ""
	}
	if value, ok := source[field]; ok {
		return elkScalarString(value)
	}
	var value any = source
	for _, part := range strings.Split(field, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[part]
	}
	return elkScalarString(value)
}

func elkScalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		if typed == nil {
			return ""
		}
		return fmt.Sprint(typed)
	}
}

func elkExternalMetrics(connectionID string, values map[string]float64) map[string]float64 {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make(map[string]float64, len(values))
	for _, key := range keys {
		result["elk."+connectionID+"."+key] = values[key]
	}
	return result
}

// elkSearchSampleLimit is the most log rows one ELK search returns.
const elkSearchSampleLimit = 20

// elkFiltersByTrace reports whether a search with these inputs filters by
// trace ID, which only happens when no test ID field is configured.
func elkFiltersByTrace(cfg *ELKCaptureConfig, testID string, traceIDs []string) bool {
	return cfg != nil && len(traceIDs) > 0 && strings.TrimSpace(cfg.TraceIDField) != "" && (testID == "" || strings.TrimSpace(cfg.TestIDField) == "")
}
