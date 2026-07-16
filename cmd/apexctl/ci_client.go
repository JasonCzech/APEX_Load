package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	P95LatencyMs             *float64 `json:"p95_latency_ms,omitempty"`
	MaxErrorPct              *float64 `json:"max_error_pct,omitempty"`
	MaxAvgServerProcessingMs *float64 `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs         *float64 `json:"max_avg_transfer_ms,omitempty"`
	MaxErrorCount            *int     `json:"max_error_count,omitempty"`
	RequireSLA               bool     `json:"require_sla"`
	RequireResults           bool     `json:"require_results"`
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
	ProjectID    string          `json:"project_id"`
	Name         string          `json:"name,omitempty"`
	ReleaseID    string          `json:"release_id,omitempty"`
	ScriptPath   string          `json:"script_path,omitempty"`
	ScriptID     string          `json:"script_id,omitempty"`
	Script       json.RawMessage `json:"script,omitempty"`
	Execution    CIExecution     `json:"execution"`
	Goals        CIGoals         `json:"goals"`
	Source       CISource        `json:"source"`
	PublishCheck bool            `json:"publish_check"`
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
	SchemaVersion int      `json:"schema_version"`
	TestID        string   `json:"test_id"`
	Status        string   `json:"status"`
	Outcome       string   `json:"outcome,omitempty"`
	StatusURL     string   `json:"status_url,omitempty"`
	ReportURL     string   `json:"report_url,omitempty"`
	CheckURL      string   `json:"check_url,omitempty"`
	SLAEvaluated  bool     `json:"sla_evaluated"`
	SLABreached   bool     `json:"sla_breached"`
	SLADetails    []string `json:"sla_details,omitempty"`
	Transactions  int64    `json:"transactions"`
	Errors        int64    `json:"errors"`
	TPS           float64  `json:"tps"`
	DurationMS    int64    `json:"duration_ms"`
	// Metrics accepts early v1 controller builds that nested the same fields.
	Metrics CIMetrics `json:"metrics,omitempty"`
	Error   string    `json:"error,omitempty"`
}

type CIAbortResponse struct {
	Status string `json:"status"`
	TestID string `json:"test_id"`
}

// CIAPIError is a deliberately body-free controller error. Controller error
// bodies are not included because an intermediary can reflect credentials or
// other sensitive request data into them. Callers can safely classify failures
// using StatusCode without parsing log text.
type CIAPIError struct {
	Method     string
	Path       string
	StatusCode int
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
	var out CIRunResponse
	if err := c.ciJSON(ctx, http.MethodPost, "/api/v1/ci/runs", request, headers, http.StatusAccepted, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.TestID) == "" {
		return nil, fmt.Errorf("controller CI run response is missing test_id")
	}
	return &out, nil
}

func (c *ApiClient) GetCIRun(ctx context.Context, testID string) (*CIRunResponse, error) {
	var out CIRunResponse
	path := "/api/v1/ci/runs/" + url.PathEscape(strings.TrimSpace(testID))
	if err := c.ciJSON(ctx, http.MethodGet, path, nil, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *ApiClient) AbortCIRun(ctx context.Context, testID string) (*CIAbortResponse, error) {
	var out CIAbortResponse
	path := "/api/v1/ci/runs/" + url.PathEscape(strings.TrimSpace(testID)) + "/abort"
	if err := c.ciJSON(ctx, http.MethodPost, path, struct{}{}, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
		}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}
