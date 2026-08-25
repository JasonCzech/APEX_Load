package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	PollTimeout time.Duration
	// pollWait is a test seam for entering a terminal polling condition without
	// spending the command-wide timeout budget on preparation requests.
	pollWait func(context.Context, time.Duration) error
}

type controllerHTTPStatusError struct {
	operation  string
	status     string
	statusCode int
	detail     string
}

func (e *controllerHTTPStatusError) Error() string {
	if e == nil {
		return "controller request failed"
	}
	message := strings.TrimSpace(e.operation)
	if message == "" {
		message = "controller request"
	}
	message += " failed: " + strings.TrimSpace(e.status)
	if detail := strings.TrimSpace(e.detail); detail != "" {
		message += ": " + detail
	}
	return message
}

func isPermanentControllerHTTPError(err error) bool {
	var statusErr *controllerHTTPStatusError
	return errors.As(err, &statusErr) && !retryableHTTPStatus(statusErr.statusCode)
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
				// A 307/308 redirect replays the original method and body. Merely
				// stripping authentication headers would still disclose credential
				// bodies such as the GitHub OIDC ID-token exchange payload, along
				// with inline CI scripts and variables. Controller API redirects
				// therefore remain confined to the configured origin.
				return fmt.Errorf("refusing cross-origin redirect from %s to %s", origin.Redacted(), next.URL.Redacted())
			}
		}
		if baseRedirectPolicy != nil {
			return baseRedirectPolicy(next, via)
		}
		return nil
	}
	return clientCopy.Do(req)
}

func (c *ApiClient) doStatusRead(path string) (*http.Response, error) {
	return c.doStatusReadContext(context.Background(), path)
}

func (c *ApiClient) doStatusReadContext(ctx context.Context, path string) (*http.Response, error) {
	const maxAttempts = 4
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		response, err := c.doContext(ctx, http.MethodGet, path, nil, "")
		if err == nil && !retryableHTTPStatus(response.StatusCode) {
			return response, nil
		}
		if ctx.Err() != nil {
			if response != nil {
				_ = response.Body.Close()
			}
			return nil, ctx.Err()
		}
		if err == nil && attempt == maxAttempts-1 {
			return response, nil
		}
		if err != nil {
			lastErr = err
			if attempt == maxAttempts-1 {
				return nil, err
			}
		} else {
			lastErr = fmt.Errorf("retryable controller response: %s", response.Status)
		}
		delay := retryDelay(attempt, response)
		if response != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
		}
		if err := waitForContext(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryableHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(attempt int, response *http.Response) time.Duration {
	if response != nil {
		raw := strings.TrimSpace(response.Header.Get("Retry-After"))
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			return min(time.Duration(seconds)*time.Second, 5*time.Second)
		}
		if at, err := http.ParseTime(raw); err == nil {
			return min(max(time.Until(at), 0), 5*time.Second)
		}
	}
	return min(250*time.Millisecond*time.Duration(1<<attempt), 2*time.Second)
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
	Status            string
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
	ctx := context.Background()
	cancel := func() {}
	if c.PollTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.PollTimeout)
	}
	defer cancel()
	return c.runPreparedTestContext(ctx, scriptName, scriptBody, vusers, duration, rampUp, format, goalConfig)
}

func (c *ApiClient) runPreparedTestContext(ctx context.Context, scriptName string, scriptBody []byte, vusers int, duration string, rampUp string, format string, goalConfig *api.GoalConfig) (*ApiTestResult, error) {
	commandStarted := time.Now()
	// 1. Upload script
	scriptID, err := c.uploadScriptContext(ctx, scriptName, scriptBody)
	if err != nil {
		return nil, c.preparedRunStageError(ctx, "upload script", err)
	}

	// 2. Create Test
	testID, err := c.createTestContext(ctx, scriptID, vusers, duration, rampUp, goalConfig)
	if err != nil {
		return nil, c.preparedRunStageError(ctx, "create test", err)
	}
	partial := &ApiTestResult{TestID: testID, Duration: time.Since(commandStarted)}

	if format != "json" {
		fmt.Printf("Created test %s from script %s. Starting...\n", testID, scriptID)
	}

	// 3. Start Test
	err = c.startTestContext(ctx, testID)
	if err != nil {
		partial.Duration = time.Since(commandStarted)
		return partial, c.preparedRunStageError(ctx, "start test", err)
	}

	// 4. Poll Test Status. Preserve the durable test identity even when the
	// controller becomes unavailable while polling, so JSON/JUnit evidence can
	// point operators at the still-running server-side run.
	pollStarted := time.Now()
	result, err := c.pollTestContext(ctx, testID, format, goalConfig != nil, pollStarted)
	if result == nil {
		result = partial
		result.Duration = time.Since(pollStarted)
	} else {
		result.TestID = testID
	}
	return result, err
}

func (c *ApiClient) preparedRunStageError(ctx context.Context, stage string, err error) error {
	if c.PollTimeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s during %s: %w", c.PollTimeout, stage, ctx.Err())
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", stage, ctx.Err())
	}
	return fmt.Errorf("%s: %w", stage, err)
}

func (c *ApiClient) uploadScript(scriptName string, scriptBody []byte) (string, error) {
	return c.uploadScriptContext(context.Background(), scriptName, scriptBody)
}

func (c *ApiClient) uploadScriptContext(ctx context.Context, scriptName string, scriptBody []byte) (string, error) {
	return c.UploadScriptWithProjectContext(ctx, scriptName, scriptBody, "")
}

// UploadScriptWithProject uploads a script JSON document via the controller's
// JSON wrapper contract ({project_id, script}), optionally scoping it to a
// project. Returns the created script ID.
func (c *ApiClient) UploadScriptWithProject(scriptName string, scriptBody []byte, projectID string) (string, error) {
	return c.UploadScriptWithProjectContext(context.Background(), scriptName, scriptBody, projectID)
}

func (c *ApiClient) UploadScriptWithProjectContext(ctx context.Context, scriptName string, scriptBody []byte, projectID string) (string, error) {
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

	resp, err := c.doContext(ctx, http.MethodPost, "/api/v1/scripts", bytes.NewReader(b), "application/json")
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
	return c.createTestContext(context.Background(), scriptID, vusers, duration, rampUp, goalConfig)
}

func (c *ApiClient) createTestContext(ctx context.Context, scriptID string, vusers int, duration, rampUp string, goalConfig *api.GoalConfig) (string, error) {
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
	resp, err := c.doContext(ctx, http.MethodPost, "/api/v1/tests", bytes.NewReader(b), "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("bad status: %s, %s", resp.Status, string(b))
	}

	var test api.ManagedTest
	if err := json.NewDecoder(resp.Body).Decode(&test); err != nil {
		return "", fmt.Errorf("decode create-test response: %w", err)
	}
	if strings.TrimSpace(test.ID) == "" {
		return "", fmt.Errorf("create-test response missing test id")
	}
	return test.ID, nil
}

func (c *ApiClient) startTest(testID string) error {
	return c.startTestContext(context.Background(), testID)
}

func (c *ApiClient) startTestContext(ctx context.Context, testID string) error {
	resp, err := c.doContext(ctx, http.MethodPost, "/api/v1/tests/"+url.PathEscape(testID)+"/start", nil, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("bad status: %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}
	return nil
}

// maxPollErrors bounds consecutive transient status-poll failures before the
// run and wait commands give up, so brief ingress/controller outages do not
// false-fail CI while persistent failures remain bounded.
const maxPollErrors = 5

func (c *ApiClient) pollTest(testID string, format string, expectSLA bool) (*ApiTestResult, error) {
	start := time.Now()
	ctx := context.Background()
	cancel := func() {}
	if c.PollTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.PollTimeout)
	}
	defer cancel()
	return c.pollTestContext(ctx, testID, format, expectSLA, start)
}

func (c *ApiClient) pollTestContext(ctx context.Context, testID string, format string, expectSLA bool, start time.Time) (*ApiTestResult, error) {
	timeoutError := func() error {
		return fmt.Errorf("timed out after %s waiting for test %s", c.PollTimeout, testID)
	}
	consecutiveErrors := 0
	for {
		if err := c.waitForTestPoll(ctx, 2*time.Second); err != nil {
			if c.PollTimeout > 0 && (ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded)) {
				return nil, timeoutError()
			}
			return nil, err
		}

		// GetTestStatus validates the status code (404 -> "not found", other
		// non-2xx -> error) and surfaces decode errors, so a deleted test or an
		// auth/posture flip no longer leaves Status empty and loops forever.
		test, err := c.GetTestStatusContext(ctx, testID)
		if err != nil {
			if ctx.Err() != nil && c.PollTimeout > 0 {
				return nil, timeoutError()
			}
			if isPermanentControllerHTTPError(err) {
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

		if isTerminalStatus(test.Status) {
			res := &ApiTestResult{
				Status:   test.Status,
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
				sla, err := c.GetSLAStatusContext(ctx, testID)
				if err != nil {
					if ctx.Err() != nil && c.PollTimeout > 0 {
						return res, timeoutError()
					}
					return res, fmt.Errorf("fetch SLA status: %w", err)
				}
				if sla != nil {
					res.SLASummary = &SLASummary{
						Evaluated:              sla.SLAEvaluated,
						Breached:               sla.SLABreached,
						Details:                append([]string(nil), sla.Details...),
						TransactionRuleResults: append([]api.TransactionSLARuleResult(nil), sla.TransactionRuleResults...),
					}
					if sla.SLABreached {
						return res, fmt.Errorf("SLA breached: %s", strings.Join(sla.Details, "; "))
					}
				}
			}
			if isFailedTestStatus(test.Status) {
				return res, fmt.Errorf("test completed with status %s", strings.ToLower(test.Status))
			}
			return res, nil
		}
	}
}

func (c *ApiClient) waitForTestPoll(ctx context.Context, delay time.Duration) error {
	if c.pollWait != nil {
		return c.pollWait(ctx, delay)
	}
	return waitForContext(ctx, delay)
}

// GetTestStatus fetches the current managed-test record (status + live metrics).
func (c *ApiClient) GetTestStatus(testID string) (*api.ManagedTest, error) {
	return c.GetTestStatusContext(context.Background(), testID)
}

func (c *ApiClient) GetTestStatusContext(ctx context.Context, testID string) (*api.ManagedTest, error) {
	resp, err := c.doStatusReadContext(ctx, "/api/v1/tests/"+url.PathEscape(testID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, &controllerHTTPStatusError{operation: fmt.Sprintf("test %s status", testID), status: resp.Status, statusCode: resp.StatusCode, detail: "test not found"}
	}
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &controllerHTTPStatusError{operation: fmt.Sprintf("test %s status", testID), status: resp.Status, statusCode: resp.StatusCode, detail: string(detail)}
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
	if timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return c.waitForTestContext(ctx, testID, format, timeout, start, nil)
	}

	// The default wait budget depends on the stored test duration, so one
	// bounded-by-HTTP-client status read is required before deriving it. The
	// resulting deadline is still anchored to start, including that first read.
	test, err := c.getTestStatusWithTolerance(context.Background(), testID, format)
	if err != nil {
		return nil, err
	}
	timeout = test.Duration + 30*time.Minute
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithDeadline(context.Background(), start.Add(timeout))
	defer cancel()
	return c.waitForTestContext(ctx, testID, format, timeout, start, test)
}

func (c *ApiClient) waitForTestContext(ctx context.Context, testID, format string, timeout time.Duration, start time.Time, initial *api.ManagedTest) (*ApiTestResult, error) {
	lastStatus := ""
	timeoutError := func() error {
		if lastStatus == "" {
			return fmt.Errorf("timed out after %s waiting for test %s", timeout, testID)
		}
		return fmt.Errorf("timed out after %s waiting for test %s (last status %s)", timeout, testID, lastStatus)
	}
	test := initial
	for {
		if test == nil {
			var err error
			test, err = c.getTestStatusWithTolerance(ctx, testID, format)
			if err != nil {
				if ctx.Err() != nil {
					return nil, timeoutError()
				}
				return nil, err
			}
		}
		lastStatus = test.Status
		if format != "json" {
			fmt.Printf("... Status: %s\n", test.Status)
		}
		if ctx.Err() != nil {
			return nil, timeoutError()
		}
		if isTerminalStatus(test.Status) {
			result, err := c.resolveTestResultContext(ctx, testID, test, time.Since(start))
			if ctx.Err() != nil {
				return result, timeoutError()
			}
			return result, err
		}
		if err := waitForContext(ctx, 2*time.Second); err != nil {
			if ctx.Err() != nil {
				return nil, timeoutError()
			}
			return nil, err
		}
		test = nil
	}
}

func (c *ApiClient) getTestStatusWithTolerance(ctx context.Context, testID, format string) (*api.ManagedTest, error) {
	for consecutiveErrors := 0; ; {
		test, err := c.GetTestStatusContext(ctx, testID)
		if err == nil {
			return test, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isPermanentControllerHTTPError(err) {
			return nil, err
		}
		consecutiveErrors++
		if consecutiveErrors >= maxPollErrors {
			return nil, fmt.Errorf("waiting for test %s failed after %d consecutive status errors: %w", testID, consecutiveErrors, err)
		}
		if format != "json" {
			fmt.Printf("... status check failed (%d/%d): %v\n", consecutiveErrors, maxPollErrors, err)
		}
		if err := waitForContext(ctx, 2*time.Second); err != nil {
			return nil, err
		}
	}
}

// resolveTestResult builds an ApiTestResult (with SLA verdict) from a terminal test.
func (c *ApiClient) resolveTestResult(testID string, test *api.ManagedTest, elapsed time.Duration) (*ApiTestResult, error) {
	return c.resolveTestResultContext(context.Background(), testID, test, elapsed)
}

func (c *ApiClient) resolveTestResultContext(ctx context.Context, testID string, test *api.ManagedTest, elapsed time.Duration) (*ApiTestResult, error) {
	res := &ApiTestResult{Status: test.Status, Duration: elapsed}
	if test.LiveMetrics != nil {
		res.TotalTransactions = test.LiveMetrics.TotalTx
		res.TotalErrors = test.LiveMetrics.TotalErrors
		res.FinalSnapshot = engine.MetricsSnapshot{
			Transactions: test.LiveMetrics.TotalTx,
			TPS:          test.LiveMetrics.TPS,
		}
	}
	sla, err := c.GetSLAStatusContext(ctx, testID)
	if err != nil {
		return res, fmt.Errorf("fetch SLA status: %w", err)
	}
	if sla != nil {
		res.SLASummary = &SLASummary{
			Evaluated:              sla.SLAEvaluated,
			Breached:               sla.SLABreached,
			Details:                append([]string(nil), sla.Details...),
			TransactionRuleResults: append([]api.TransactionSLARuleResult(nil), sla.TransactionRuleResults...),
		}
	}
	if isFailedTestStatus(test.Status) {
		return res, fmt.Errorf("test completed with status %s", strings.ToLower(test.Status))
	}
	return res, nil
}

func isTerminalStatus(status string) bool {
	return api.IsTerminalTestStatus(status)
}

// VerifyArchive re-verifies an archived run against the SHA-256 hashes recorded in
// its manifest, returning the per-artifact match/mismatch/missing verdict. A
// tampered or corrupt archive comes back as a populated report (not an error); a
// transport failure or a missing archive returns an error.
func (c *ApiClient) VerifyArchive(testID string) (*api.RunArchiveIntegrityReport, error) {
	resp, err := c.doStatusRead("/api/v1/tests/" + url.PathEscape(testID) + "/archive/verify")
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
	return c.GetSLAStatusContext(context.Background(), testID)
}

func (c *ApiClient) GetSLAStatusContext(ctx context.Context, testID string) (*api.SLABreachReport, error) {
	resp, err := c.doStatusReadContext(ctx, "/api/v1/tests/"+url.PathEscape(testID)+"/sla-status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &controllerHTTPStatusError{operation: "SLA status request", status: resp.Status, statusCode: resp.StatusCode, detail: string(detail)}
	}

	var report api.SLABreachReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return nil, err
	}
	return &report, nil
}
