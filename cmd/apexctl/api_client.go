package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/engine"
)

type ApiClient struct {
	BaseURL     string
	Client      *http.Client
	APIKey      string
	BearerToken string
}

func NewApiClient(baseURL string) *ApiClient {
	return NewApiClientWithAuth(baseURL, os.Getenv("APEXLOAD_API_KEY"), os.Getenv("APEXLOAD_BEARER_TOKEN"))
}

func NewApiClientWithAuth(baseURL string, apiKey string, bearerToken string) *ApiClient {
	return &ApiClient{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		Client:      &http.Client{Timeout: 30 * time.Second},
		APIKey:      strings.TrimSpace(apiKey),
		BearerToken: strings.TrimSpace(bearerToken),
	}
}

func (c *ApiClient) newRequest(method string, path string, body io.Reader, contentType string) (*http.Request, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if strings.TrimSpace(c.APIKey) != "" {
		req.Header.Set("X-APEXLoad-API-Key", strings.TrimSpace(c.APIKey))
	}
	if strings.TrimSpace(c.BearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.BearerToken))
	}
	return req, nil
}

func (c *ApiClient) do(method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	return c.doContext(context.Background(), method, path, body, contentType)
}

func (c *ApiClient) doContext(ctx context.Context, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	return c.doContextWithHeaders(ctx, method, path, body, contentType, nil)
}

func (c *ApiClient) doContextWithHeaders(ctx context.Context, method string, path string, body io.Reader, contentType string, headers http.Header) (*http.Response, error) {
	req, err := c.newRequest(method, path, body, contentType)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	clientCopy := *client
	baseRedirectPolicy := client.CheckRedirect
	clientCopy.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 && baseRedirectPolicy == nil {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if len(via) > 0 {
			origin := via[0].URL
			if strings.EqualFold(origin.Scheme, "https") && strings.EqualFold(next.URL.Scheme, "http") {
				return fmt.Errorf("refusing HTTPS-to-HTTP redirect to %s", next.URL.Redacted())
			}
			if !sameHTTPOrigin(origin, next.URL) {
				next.Header.Del("X-APEXLoad-API-Key")
				next.Header.Del("Authorization")
			}
		}
		if baseRedirectPolicy != nil {
			return baseRedirectPolicy(next, via)
		}
		return nil
	}
	return clientCopy.Do(req)
}

func sameHTTPOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || !strings.EqualFold(a.Scheme, b.Scheme) || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return effectiveURLPort(a) == effectiveURLPort(b)
}

func effectiveURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		_, port, _ := net.SplitHostPort(u.Host)
		return port
	}
}

// Result mimics engine.TestResult for formatting
type ApiTestResult struct {
	TestID            string
	Duration          time.Duration
	TotalTransactions int64
	TotalErrors       int64
	FinalSnapshot     engine.MetricsSnapshot
	SLASummary        *SLASummary
}

func (c *ApiClient) RunTest(scriptPath string, vusers int, duration string, rampUp string, format string, goalConfig *api.GoalConfig) (*ApiTestResult, error) {
	file, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("read script: %w", err)
	}
	return c.RunPreparedTest(scriptPath, file, vusers, duration, rampUp, format, goalConfig)
}

func (c *ApiClient) RunPreparedTest(scriptName string, scriptBody []byte, vusers int, duration string, rampUp string, format string, goalConfig *api.GoalConfig) (*ApiTestResult, error) {
	// 1. Upload script
	scriptID, err := c.uploadScript(scriptName, scriptBody)
	if err != nil {
		return nil, fmt.Errorf("upload script: %w", err)
	}

	// 2. Create Test
	testID, err := c.createTest(scriptID, vusers, duration, rampUp, goalConfig)
	if err != nil {
		return nil, fmt.Errorf("create test: %w", err)
	}

	if format != "json" {
		fmt.Printf("Created test %s from script %s. Starting...\n", testID, scriptID)
	}

	// 3. Start Test
	err = c.startTest(testID)
	if err != nil {
		return nil, fmt.Errorf("start test: %w", err)
	}

	// 4. Poll Test Status
	result, err := c.pollTest(testID, format, goalConfig != nil)
	if result != nil {
		result.TestID = testID
	}
	return result, err
}

func (c *ApiClient) uploadScript(scriptName string, scriptBody []byte) (string, error) {
	return c.UploadScriptWithProject(scriptName, scriptBody, "")
}

// UploadScriptWithProject uploads a script JSON document via the controller's
// JSON wrapper contract ({project_id, script}), optionally scoping it to a
// project. Returns the created script ID.
func (c *ApiClient) UploadScriptWithProject(scriptName string, scriptBody []byte, projectID string) (string, error) {
	var script map[string]any
	if err := json.Unmarshal(scriptBody, &script); err != nil {
		return "", fmt.Errorf("script %s is not valid JSON: %w", scriptName, err)
	}
	wrapper := map[string]any{"script": script}
	if projectID != "" {
		wrapper["project_id"] = projectID
	}
	b, err := json.Marshal(wrapper)
	if err != nil {
		return "", err
	}

	resp, err := c.do(http.MethodPost, "/api/v1/scripts", bytes.NewReader(b), "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("bad status: %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}

	var m map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	id, _ := m["id"].(string)
	if id == "" {
		return "", fmt.Errorf("upload response missing script id")
	}
	return id, nil
}

func (c *ApiClient) createTest(scriptID string, vusers int, duration, rampUp string, goalConfig *api.GoalConfig) (string, error) {
	reqBody := api.CreateTestRequest{
		Name:     "CLI Run " + time.Now().Format("2006-01-02 15:04:05"),
		Duration: duration,
		ScriptID: scriptID,
		RampUp:   rampUp,
		VUsers:   vusers,
	}
	if goalConfig != nil {
		reqBody.ScenarioMode = "goal_oriented"
		reqBody.GoalConfig = goalConfig
	}

	b, _ := json.Marshal(reqBody)
	resp, err := c.do(http.MethodPost, "/api/v1/tests", bytes.NewReader(b), "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("bad status: %s, %s", resp.Status, string(b))
	}

	var test api.ManagedTest
	json.NewDecoder(resp.Body).Decode(&test)
	return test.ID, nil
}

func (c *ApiClient) startTest(testID string) error {
	resp, err := c.do(http.MethodPost, "/api/v1/tests/"+testID+"/start", nil, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	return nil
}

// maxPollErrors bounds consecutive transient poll failures before pollTest gives
// up, so the CLI cannot hang forever on a persistent error response.
const maxPollErrors = 5

func (c *ApiClient) pollTest(testID string, format string, expectSLA bool) (*ApiTestResult, error) {
	start := time.Now()
	consecutiveErrors := 0
	for {
		time.Sleep(2 * time.Second)

		// GetTestStatus validates the status code (404 -> "not found", other
		// non-2xx -> error) and surfaces decode errors, so a deleted test or an
		// auth/posture flip no longer leaves Status empty and loops forever.
		test, err := c.GetTestStatus(testID)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil, err
			}
			consecutiveErrors++
			if consecutiveErrors >= maxPollErrors {
				return nil, fmt.Errorf("polling test %s failed after %d consecutive errors: %w", testID, consecutiveErrors, err)
			}
			if format != "json" {
				fmt.Printf("... status check failed (%d/%d): %v\n", consecutiveErrors, maxPollErrors, err)
			}
			continue
		}
		consecutiveErrors = 0

		if format != "json" {
			fmt.Printf("... Status: %s\n", test.Status)
		}

		if test.Status == "COMPLETE" || test.Status == "FAILED" || test.Status == "ABORTED" {
			res := &ApiTestResult{
				Duration: time.Since(start),
			}
			if test.LiveMetrics != nil {
				res.TotalTransactions = test.LiveMetrics.TotalTx
				res.TotalErrors = test.LiveMetrics.TotalErrors
				res.FinalSnapshot = engine.MetricsSnapshot{
					Transactions: test.LiveMetrics.TotalTx,
					TPS:          test.LiveMetrics.TPS,
				}
			}
			if expectSLA {
				sla, err := c.GetSLAStatus(testID)
				if err != nil {
					return res, fmt.Errorf("fetch SLA status: %w", err)
				}
				if sla != nil {
					res.SLASummary = &SLASummary{
						Evaluated: true,
						Breached:  sla.SLABreached,
						Details:   append([]string(nil), sla.Details...),
					}
					if sla.SLABreached {
						return res, fmt.Errorf("SLA breached: %s", strings.Join(sla.Details, "; "))
					}
				}
			}
			if test.Status == "FAILED" || test.Status == "ABORTED" {
				return res, fmt.Errorf("test completed with status %s", strings.ToLower(test.Status))
			}
			return res, nil
		}
	}
}

// GetTestStatus fetches the current managed-test record (status + live metrics).
func (c *ApiClient) GetTestStatus(testID string) (*api.ManagedTest, error) {
	resp, err := c.do(http.MethodGet, "/api/v1/tests/"+testID, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("test %s not found", testID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}
	var test api.ManagedTest
	if err := json.NewDecoder(resp.Body).Decode(&test); err != nil {
		return nil, err
	}
	return &test, nil
}

// WaitForTest blocks until the given test reaches a terminal state (or timeout),
// then resolves its final metrics and SLA verdict. Used by `apexctl wait` to gate
// CI pipelines on an already-started run.
func (c *ApiClient) WaitForTest(testID, format string, timeout time.Duration) (*ApiTestResult, error) {
	start := time.Now()
	var deadline time.Time
	if timeout > 0 {
		deadline = start.Add(timeout)
	}
	for {
		test, err := c.GetTestStatus(testID)
		if err != nil {
			return nil, err
		}
		if format != "json" {
			fmt.Printf("... Status: %s\n", test.Status)
		}
		if isTerminalStatus(test.Status) {
			return c.resolveTestResult(testID, test, time.Since(start))
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for test %s (last status %s)", timeout, testID, test.Status)
		}
		time.Sleep(2 * time.Second)
	}
}

// resolveTestResult builds an ApiTestResult (with SLA verdict) from a terminal test.
func (c *ApiClient) resolveTestResult(testID string, test *api.ManagedTest, elapsed time.Duration) (*ApiTestResult, error) {
	res := &ApiTestResult{Duration: elapsed}
	if test.LiveMetrics != nil {
		res.TotalTransactions = test.LiveMetrics.TotalTx
		res.TotalErrors = test.LiveMetrics.TotalErrors
		res.FinalSnapshot = engine.MetricsSnapshot{
			Transactions: test.LiveMetrics.TotalTx,
			TPS:          test.LiveMetrics.TPS,
		}
	}
	sla, err := c.GetSLAStatus(testID)
	if err != nil {
		return res, fmt.Errorf("fetch SLA status: %w", err)
	}
	if sla != nil {
		res.SLASummary = &SLASummary{
			Evaluated: true,
			Breached:  sla.SLABreached,
			Details:   append([]string(nil), sla.Details...),
		}
	}
	if test.Status == "FAILED" || test.Status == "ABORTED" {
		return res, fmt.Errorf("test completed with status %s", strings.ToLower(test.Status))
	}
	return res, nil
}

func isTerminalStatus(status string) bool {
	switch status {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

// VerifyArchive re-verifies an archived run against the SHA-256 hashes recorded in
// its manifest, returning the per-artifact match/mismatch/missing verdict. A
// tampered or corrupt archive comes back as a populated report (not an error); a
// transport failure or a missing archive returns an error.
func (c *ApiClient) VerifyArchive(testID string) (*api.RunArchiveIntegrityReport, error) {
	resp, err := c.do(http.MethodGet, "/api/v1/tests/"+testID+"/archive/verify", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("archive not found for test %s: %s", testID, strings.TrimSpace(string(detail)))
	}
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("bad status: %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}

	var report api.RunArchiveIntegrityReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return nil, fmt.Errorf("decode integrity report: %w", err)
	}
	return &report, nil
}

func (c *ApiClient) GetSLAStatus(testID string) (*api.SLABreachReport, error) {
	resp, err := c.do(http.MethodGet, "/api/v1/tests/"+testID+"/sla-status", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("SLA status request failed: %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}

	var report api.SLABreachReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return nil, err
	}
	return &report, nil
}
