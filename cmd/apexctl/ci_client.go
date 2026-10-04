package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

const ciSchemaVersion = 1

// CIDiscovery is the stable, unauthenticated controller capability document.
type CIDiscovery struct {
	SchemaVersion int      `json:"schema_version"`
	ControllerID  string   `json:"controller_id"`
	OIDCAudience  string   `json:"oidc_audience"`
	CIAPIVersion  string   `json:"ci_api_version"`
	AuthModes     []string `json:"auth_modes"`
}

type CIExchangeRequest struct {
	IDToken   string `json:"id_token"`
	ProjectID string `json:"project_id"`
}

type CIExchangeResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresIn   int64     `json:"expires_in"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type CIExecution struct {
	VUsers    int               `json:"vusers"`
	Duration  string            `json:"duration"`
	RampUp    string            `json:"ramp_up"`
	BaseURL   string            `json:"base_url,omitempty"`
	Variables map[string]string `json:"variables,omitempty"`
}

type CIGoals struct {
	MonitorRules             []analysis.MonitorRule `json:"monitor_rules,omitempty"`
	P95LatencyMs             *float64               `json:"p95_latency_ms,omitempty"`
	MaxErrorPct              *float64               `json:"max_error_pct,omitempty"`
	MaxAvgServerProcessingMs *float64               `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs         *float64               `json:"max_avg_transfer_ms,omitempty"`
	MaxErrorCount            *int                   `json:"max_error_count,omitempty"`
	RequireSLA               bool                   `json:"require_sla"`
	RequireResults           bool                   `json:"require_results"`
}

type CISource struct {
	Provider          string `json:"provider"`
	ServerURL         string `json:"server_url,omitempty"`
	APIURL            string `json:"api_url,omitempty"`
	Repository        string `json:"repository,omitempty"`
	RepositoryID      string `json:"repository_id,omitempty"`
	HeadRepository    string `json:"head_repository,omitempty"`
	HeadRepositoryID  string `json:"head_repository_id,omitempty"`
	Ref               string `json:"ref,omitempty"`
	SHA               string `json:"sha,omitempty"`
	WorkflowRef       string `json:"workflow_ref,omitempty"`
	JobWorkflowRef    string `json:"job_workflow_ref,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	RunAttempt        string `json:"run_attempt,omitempty"`
	Actor             string `json:"actor,omitempty"`
	EventName         string `json:"event_name,omitempty"`
	Environment       string `json:"environment,omitempty"`
	RunnerEnvironment string `json:"runner_environment,omitempty"`
	HeadRef           string `json:"head_ref,omitempty"`
	HeadSHA           string `json:"head_sha,omitempty"`
}

type CIRunRequest struct {
	MonitorBindings *[]storage.MonitorBinding `json:"monitor_bindings,omitempty"`
	ProjectID       string                    `json:"project_id"`
	Name            string                    `json:"name,omitempty"`
	ReleaseID       string                    `json:"release_id,omitempty"`
	ScriptPath      string                    `json:"script_path,omitempty"`
	ScriptID        string                    `json:"script_id,omitempty"`
	Script          json.RawMessage           `json:"script,omitempty"`
	Execution       CIExecution               `json:"execution"`
	Goals           CIGoals                   `json:"goals"`
	Source          CISource                  `json:"source"`
	PublishCheck    bool                      `json:"publish_check"`
}

type CIMetrics struct {
	Transactions int64   `json:"transactions"`
	Errors       int64   `json:"errors"`
	TPS          float64 `json:"tps"`
	DurationMS   int64   `json:"duration_ms"`
}

// CIRunResponse is returned by both create and status. Fields unavailable at
// creation are omitted until the controller reaches a terminal state.
type CIRunResponse struct {
	MonitoringStatus   string                       `json:"monitoring_status,omitempty"`
	MonitorRuleResults []analysis.MonitorRuleResult `json:"monitor_rule_results,omitempty"`
	SchemaVersion      int                          `json:"schema_version"`
	TestID             string                       `json:"test_id"`
	Status             string                       `json:"status"`
	Outcome            string                       `json:"outcome,omitempty"`
	StatusURL          string                       `json:"status_url,omitempty"`
	ReportURL          string                       `json:"report_url,omitempty"`
	CheckURL           string                       `json:"check_url,omitempty"`
	SLAEvaluated       bool                         `json:"sla_evaluated"`
	SLABreached        bool                         `json:"sla_breached"`
	SLADetails         []string                     `json:"sla_details,omitempty"`
	Transactions       int64                        `json:"transactions"`
	Errors             int64                        `json:"errors"`
	TPS                float64                      `json:"tps"`
	DurationMS         int64                        `json:"duration_ms"`
	// Metrics accepts early v1 controller builds that nested the same fields.
	Metrics CIMetrics `json:"metrics,omitempty"`
	Error   string    `json:"error,omitempty"`
}

type CIAbortResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	TestID        string `json:"test_id"`
}

type CIAbortRequest struct {
	Reason string `json:"reason,omitempty"`
}

// CIAPIError is a deliberately body-free controller error. Controller error
// bodies are not included because an intermediary can reflect credentials or
// other sensitive request data into them. Callers can safely classify failures
// using StatusCode without parsing log text.
type CIAPIError struct {
	Method     string
	Path       string
	StatusCode int
	RetryAfter time.Duration
}

func (e *CIAPIError) Error() string {
	if e == nil {
		return "controller CI API request failed"
	}
	statusText := http.StatusText(e.StatusCode)
	if statusText == "" {
		statusText = "unknown status"
	}
	return fmt.Sprintf("%s %s returned HTTP %d %s", e.Method, e.Path, e.StatusCode, statusText)
}

func (c *ApiClient) DiscoverCI(ctx context.Context) (*CIDiscovery, error) {
	var out CIDiscovery
	if err := c.ciJSON(ctx, http.MethodGet, "/.well-known/apexload-ci", nil, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	if out.SchemaVersion != ciSchemaVersion || strings.TrimSpace(out.CIAPIVersion) != "v1" || strings.TrimSpace(out.ControllerID) == "" {
		return nil, fmt.Errorf("controller returned an unsupported CI discovery document")
	}
	return &out, nil
}

func (c *ApiClient) ExchangeGitHubOIDC(ctx context.Context, request CIExchangeRequest) (*CIExchangeResponse, error) {
	var out CIExchangeResponse
	if err := c.ciJSON(ctx, http.MethodPost, "/api/v1/auth/ci/github/exchange", request, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.AccessToken) == "" || !strings.EqualFold(firstNonEmptyCLI(out.TokenType, "Bearer"), "Bearer") {
		return nil, fmt.Errorf("controller returned an invalid CI token response")
	}
	return &out, nil
}

func (c *ApiClient) CreateCIRun(ctx context.Context, request CIRunRequest, idempotencyKey string) (*CIRunResponse, error) {
	headers := make(http.Header)
	if strings.TrimSpace(idempotencyKey) != "" {
		headers.Set("Idempotency-Key", strings.TrimSpace(idempotencyKey))
	}
	if leaseToken := strings.TrimSpace(os.Getenv("APEXLOAD_DEPLOYMENT_LEASE_TOKEN")); leaseToken != "" {
		if len(leaseToken) != 64 {
			return nil, fmt.Errorf("deployment lease token has invalid length")
		}
		for _, char := range leaseToken {
			if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
				return nil, fmt.Errorf("deployment lease token has invalid format")
			}
		}
		headers.Set("X-APEXLoad-Deployment-Lease-Token", leaseToken)
	}
	var out CIRunResponse
	if err := c.ciJSON(ctx, http.MethodPost, "/api/v1/ci/runs", request, headers, http.StatusAccepted, &out); err != nil {
		return nil, err
	}
	if err := validateCIRunAPIResponse(&out, ""); err != nil {
		return nil, fmt.Errorf("controller returned an invalid CI run response: %w", err)
	}
	return &out, nil
}

func (c *ApiClient) GetCIRun(ctx context.Context, testID string) (*CIRunResponse, error) {
	var out CIRunResponse
	path := "/api/v1/ci/runs/" + url.PathEscape(strings.TrimSpace(testID))
	if err := c.ciJSON(ctx, http.MethodGet, path, nil, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	if err := validateCIRunAPIResponse(&out, strings.TrimSpace(testID)); err != nil {
		return nil, fmt.Errorf("controller returned an invalid CI run response: %w", err)
	}
	return &out, nil
}

func validateCIRunAPIResponse(response *CIRunResponse, expectedTestID string) error {
	if response == nil || response.SchemaVersion != ciSchemaVersion {
		return fmt.Errorf("unsupported schema version")
	}
	testID := strings.TrimSpace(response.TestID)
	if testID == "" {
		return fmt.Errorf("test_id is missing")
	}
	if expectedTestID != "" && testID != expectedTestID {
		return fmt.Errorf("test_id does not match the requested run")
	}
	status := strings.ToUpper(strings.TrimSpace(response.Status))
	switch status {
	case "QUEUED", "SCHEDULED", "PENDING", "SHAKEOUT", "SHAKEOUT_REVIEW", "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "COMPLETE", "FAILED", "ABORTED":
	default:
		return fmt.Errorf("status is missing or unsupported")
	}
	outcome := strings.ToLower(strings.TrimSpace(response.Outcome))
	switch outcome {
	case "", "queued", "running", "monitoring_pending", ciOutcomeMonitoringIncomplete, ciOutcomeInconclusive, ciOutcomePassed, ciOutcomeSLABreached, ciOutcomeExecutionError, ciOutcomeConfigurationError, ciOutcomeTimedOut, ciOutcomeCancelled:
	default:
		return fmt.Errorf("outcome is unsupported")
	}
	terminal := status == "COMPLETE" || status == "FAILED" || status == "ABORTED"
	if terminal {
		valid := outcome == "monitoring_pending" ||
			(status == "COMPLETE" && (outcome == ciOutcomeMonitoringIncomplete || outcome == ciOutcomeInconclusive || outcome == ciOutcomePassed || outcome == ciOutcomeSLABreached || outcome == ciOutcomeExecutionError)) ||
			(status == "FAILED" && outcome == ciOutcomeExecutionError) ||
			(status == "ABORTED" && (outcome == ciOutcomeCancelled || outcome == ciOutcomeTimedOut))
		if !valid {
			return fmt.Errorf("terminal status and outcome are inconsistent")
		}
	} else if outcome != "" && outcome != "queued" && outcome != "running" {
		return fmt.Errorf("nonterminal status cannot carry a terminal outcome")
	}
	if response.Transactions < 0 || response.Errors < 0 || response.DurationMS < 0 || response.TPS < 0 || math.IsNaN(response.TPS) || math.IsInf(response.TPS, 0) {
		return fmt.Errorf("metrics are invalid")
	}
	if response.Metrics.Transactions < 0 || response.Metrics.Errors < 0 || response.Metrics.DurationMS < 0 || response.Metrics.TPS < 0 || math.IsNaN(response.Metrics.TPS) || math.IsInf(response.Metrics.TPS, 0) {
		return fmt.Errorf("nested metrics are invalid")
	}
	return nil
}

func (c *ApiClient) AbortCIRunWithReason(ctx context.Context, testID string, reason string) (*CIAbortResponse, error) {
	var out CIAbortResponse
	path := "/api/v1/ci/runs/" + url.PathEscape(strings.TrimSpace(testID)) + "/abort"
	payload := CIAbortRequest{Reason: strings.TrimSpace(reason)}
	if err := c.ciJSON(ctx, http.MethodPost, path, payload, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	if err := validateCIAbortAPIResponse(&out, strings.TrimSpace(testID)); err != nil {
		return nil, fmt.Errorf("controller returned an invalid CI abort response: %w", err)
	}
	return &out, nil
}

func validateCIAbortAPIResponse(response *CIAbortResponse, expectedTestID string) error {
	if response == nil || response.SchemaVersion != ciSchemaVersion {
		return fmt.Errorf("unsupported schema version")
	}
	if strings.TrimSpace(response.TestID) == "" || strings.TrimSpace(response.TestID) != strings.TrimSpace(expectedTestID) {
		return fmt.Errorf("test_id does not match the requested run")
	}
	switch strings.ToUpper(strings.TrimSpace(response.Status)) {
	case "QUEUED", "SCHEDULED", "PENDING", "SHAKEOUT", "SHAKEOUT_REVIEW", "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "COMPLETE", "FAILED", "ABORTED":
		return nil
	default:
		return fmt.Errorf("status is missing or unsupported")
	}
}

func (c *ApiClient) ciJSON(ctx context.Context, method, path string, payload any, headers http.Header, wantStatus int, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode CI API request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	contentType := ""
	if payload != nil {
		contentType = "application/json"
	}
	response, err := c.doContextWithHeaders(ctx, method, path, body, contentType, headers)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		// Do not return controller bodies here: exchange failures can otherwise
		// reflect credentials into CI logs through a misconfigured proxy.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return &CIAPIError{
			Method:     method,
			Path:       path,
			StatusCode: response.StatusCode,
			RetryAfter: parseCIRetryAfter(response.Header.Get("Retry-After"), time.Now()),
		}
	}
	if out == nil {
		return nil
	}
	if err := decodeBoundedSingleJSON(response.Body, 2<<20, out); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

func parseCIRetryAfter(value string, now time.Time) time.Duration {
	const maxRetryAfter = 24 * time.Hour
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds >= int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	delay := retryAt.Sub(now)
	if delay <= 0 {
		return 0
	}
	if delay > maxRetryAfter {
		return maxRetryAfter
	}
	return delay
}

func decodeBoundedSingleJSON(reader io.Reader, maxBytes int64, out any) error {
	if reader == nil {
		return fmt.Errorf("response body is unavailable")
	}
	if maxBytes < 1 {
		return fmt.Errorf("response size limit is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > maxBytes {
		return fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("response must contain one JSON object")
		}
		return fmt.Errorf("response has trailing data: %w", err)
	}
	return nil
}
