package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// powerBIDatasetSchemaVersion identifies the push-dataset table layout this
// build creates. Bump it when the runs/transactions column sets change; a
// stored publish with a different version marks the remote dataset as drifted.
// Version 2 added the HTTP version mix columns.
const powerBIDatasetSchemaVersion = 2

// powerBIMaxRowsPerPost is the Power BI push API's documented per-request row
// ceiling.
const powerBIMaxRowsPerPost = 10000

const (
	powerBIRunsTable         = "runs"
	powerBITransactionsTable = "transactions"
)

// powerBIClient is a minimal Power BI REST client scoped to what the publish
// flow needs. All traffic — including Azure AD token acquisition — goes through
// a host-allowlisted SSRF-safe HTTP client derived from the admin-configured
// endpoints.
type powerBIClient struct {
	settings PowerBISettings
	http     *http.Client
	now      func() time.Time

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

func newPowerBIClient(settings PowerBISettings) *powerBIClient {
	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &powerBIClient{
		settings: settings,
		http:     newHostAllowlistedHTTPClient(settings.allowlistHosts(), timeout),
		now:      time.Now,
	}
}

func (c *powerBIClient) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cachedToken != "" && c.now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}
	secret := c.settings.clientSecret()
	if secret == "" {
		return "", fmt.Errorf("power bi client secret is not available: set the %q environment variable", c.settings.ClientSecretEnvVar)
	}
	cfg := clientcredentials.Config{
		ClientID:     c.settings.ClientID,
		ClientSecret: secret,
		TokenURL:     c.settings.AuthBaseURL + "/" + url.PathEscape(c.settings.TenantID) + "/oauth2/v2.0/token",
		Scopes:       []string{c.settings.Scope},
	}
	tok, err := cfg.Token(context.WithValue(ctx, oauth2.HTTPClient, c.http))
	if err != nil {
		return "", fmt.Errorf("acquire power bi token: %w", err)
	}
	c.cachedToken = tok.AccessToken
	c.tokenExpiry = tok.Expiry.Add(-2 * time.Minute)
	if tok.Expiry.IsZero() {
		c.tokenExpiry = c.now().Add(5 * time.Minute)
	}
	return c.cachedToken, nil
}

type powerBIDataset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type powerBITable struct {
	Name string `json:"name"`
}

// powerBIAPIError carries the upstream status and (truncated) body so publish
// failures surface something actionable in the stored record.
type powerBIAPIError struct {
	Status int
	Body   string
}

func (e *powerBIAPIError) Error() string {
	return fmt.Sprintf("power bi api returned status %d: %s", e.Status, e.Body)
}

func (c *powerBIClient) doJSON(ctx context.Context, method, requestURL string, payload any, out any) error {
	return c.doJSONAttempts(ctx, method, requestURL, payload, out, 3, true, true)
}

// doJSONAttempts performs a bounded number of attempts. Callers must disable
// ambiguous retries for operations, such as push-dataset row insertion, that
// have no upstream idempotency primitive. Explicit 401 responses remain safe
// after refreshing the token. Callers may also enable explicit 429 retries when
// a throttle rejection is safe for that operation; Retry-After is then honored.
func (c *powerBIClient) doJSONAttempts(ctx context.Context, method, requestURL string, payload any, out any, maxAttempts int, retryAmbiguous, retryRejectedThrottle bool) error {
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode power bi request: %w", err)
		}
		body = encoded
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return err
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
		if err != nil {
			return fmt.Errorf("build power bi request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if !retryAmbiguous || attempt == maxAttempts {
				return fmt.Errorf("power bi request failed: %w", err)
			}
			sleepWithContext(ctx, notificationRetryDelay(nil, attempt))
			continue
		}
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			var decodeErr error
			if out != nil {
				decodeErr = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
			}
			_ = resp.Body.Close()
			if decodeErr != nil {
				return fmt.Errorf("decode power bi response: %w", decodeErr)
			}
			return nil
		}
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		apiErr := &powerBIAPIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(errBody))}
		if resp.StatusCode == http.StatusUnauthorized {
			// Token may have been revoked or expired early; drop the cache so
			// the retry mints a fresh one.
			c.mu.Lock()
			c.cachedToken = ""
			c.mu.Unlock()
		}
		retryable := resp.StatusCode == http.StatusUnauthorized ||
			(retryRejectedThrottle && resp.StatusCode == http.StatusTooManyRequests) ||
			(retryAmbiguous && retryableNotificationStatus(resp.StatusCode))
		delay := notificationRetryDelay(resp, attempt)
		_ = resp.Body.Close()
		if !retryable || attempt == maxAttempts {
			return apiErr
		}
		sleepWithContext(ctx, delay)
	}
	return fmt.Errorf("power bi request retries exhausted")
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	if ctx == nil {
		time.Sleep(d)
		return
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (c *powerBIClient) groupURL(parts ...string) string {
	base := c.settings.APIBaseURL + "/groups/" + url.PathEscape(c.settings.WorkspaceID)
	for _, part := range parts {
		base += "/" + part
	}
	return base
}

func (c *powerBIClient) listDatasets(ctx context.Context) ([]powerBIDataset, error) {
	var out struct {
		Value []powerBIDataset `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.groupURL("datasets"), nil, &out); err != nil {
		return nil, err
	}
	return out.Value, nil
}

func (c *powerBIClient) findDatasetByName(ctx context.Context) (*powerBIDataset, error) {
	datasets, err := c.listDatasets(ctx)
	if err != nil {
		return nil, err
	}
	for i := range datasets {
		if strings.EqualFold(strings.TrimSpace(datasets[i].Name), c.settings.DatasetName) {
			return &datasets[i], nil
		}
	}
	return nil, nil
}

func (c *powerBIClient) listTables(ctx context.Context, datasetID string) ([]powerBITable, error) {
	var out struct {
		Value []powerBITable `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.groupURL("datasets", url.PathEscape(datasetID), "tables"), nil, &out); err != nil {
		return nil, err
	}
	return out.Value, nil
}

// powerBIEnsureDatasetMu serializes dataset creation process-wide: Power BI
// does not enforce unique dataset names, so two first-ever publishes of
// different tests racing through list-then-create would mint two same-named
// datasets and split rows between them. (Cross-replica HA still races; the
// per-name list check bounds the window.)
var powerBIEnsureDatasetMu sync.Mutex

// ensureDataset returns the id of the configured push dataset, creating it
// (tables + run_id relationship) when absent. The created flag lets callers
// skip the drift check on a dataset this call just created.
func (c *powerBIClient) ensureDataset(ctx context.Context) (datasetID string, created bool, err error) {
	powerBIEnsureDatasetMu.Lock()
	defer powerBIEnsureDatasetMu.Unlock()
	existing, err := c.findDatasetByName(ctx)
	if err != nil {
		return "", false, err
	}
	if existing != nil {
		return existing.ID, false, nil
	}
	var out powerBIDataset
	// Dataset creation has no idempotency primitive and names are not unique.
	// Retry only an explicit 401: transport, throttle, and server failures may
	// mean the first request committed even though its response was ambiguous.
	createErr := c.doJSONAttempts(ctx, http.MethodPost, c.groupURL("datasets"), powerBIDatasetDefinition(c.settings.DatasetName), &out, 3, false, false)
	if createErr != nil {
		// The failed response may conceal our own committed create, or another
		// publisher may have won the list/create race. Reconcile after every
		// failure class without issuing another mutation.
		if raced, listErr := c.findDatasetByName(ctx); listErr == nil && raced != nil {
			return raced.ID, false, nil
		}
		return "", false, createErr
	}
	if strings.TrimSpace(out.ID) == "" {
		if fresh, listErr := c.findDatasetByName(ctx); listErr == nil && fresh != nil {
			// The dataset found by re-listing may have been created by a
			// concurrent process with a different schema — report created=false
			// so the caller's drift check runs against it.
			return fresh.ID, false, nil
		}
		return "", false, fmt.Errorf("power bi dataset create returned no id")
	}
	return out.ID, true, nil
}

func (c *powerBIClient) deleteDataset(ctx context.Context, datasetID string) error {
	return c.doJSON(ctx, http.MethodDelete, c.groupURL("datasets", url.PathEscape(datasetID)), nil, nil)
}

// pushRows POSTs rows to a push-dataset table in chunks of at most
// powerBIMaxRowsPerPost, invoking onChunk with the cumulative pushed count
// after each successful chunk so callers can persist progress.
func (c *powerBIClient) pushRows(ctx context.Context, datasetID string, table string, rows []map[string]any, onChunk func(pushed int)) (int, error) {
	pushed := 0
	for start := 0; start < len(rows); start += powerBIMaxRowsPerPost {
		end := start + powerBIMaxRowsPerPost
		if end > len(rows) {
			end = len(rows)
		}
		payload := map[string]any{"rows": rows[start:end]}
		// Power BI does not offer an idempotency key for push-dataset rows.
		// Retrying an ambiguous result can duplicate a whole chunk under the
		// same publish_id, which reports cannot distinguish. Retry only explicit
		// authentication and throttle rejections; transport and server failures
		// leave the publish partial for operator-controlled recovery.
		if err := c.doJSONAttempts(ctx, http.MethodPost, c.groupURL("datasets", url.PathEscape(datasetID), "tables", url.PathEscape(table), "rows"), payload, nil, 3, false, true); err != nil {
			return pushed, err
		}
		pushed = end
		if onChunk != nil {
			onChunk(pushed)
		}
	}
	return pushed, nil
}

// powerBIDatasetDefinition is the create-dataset payload: both tables plus the
// transactions→runs relationship, so downstream reports can slice transaction
// stats by run attributes without modeling work.
func powerBIDatasetDefinition(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"defaultMode": "Push",
		"tables": []map[string]any{
			{"name": powerBIRunsTable, "columns": powerBIRunsColumns()},
			{"name": powerBITransactionsTable, "columns": powerBITransactionsColumns()},
		},
		"relationships": []map[string]any{
			{
				"name":                   "transactions_to_runs",
				"fromTable":              powerBITransactionsTable,
				"fromColumn":             "run_id",
				"toTable":                powerBIRunsTable,
				"toColumn":               "run_id",
				"crossFilteringBehavior": "bothDirections",
			},
		},
	}
}

func powerBIColumn(name, dataType string) map[string]any {
	return map[string]any{"name": name, "dataType": dataType}
}

func powerBIRunsColumns() []map[string]any {
	return []map[string]any{
		powerBIColumn("run_id", "String"),
		powerBIColumn("publish_id", "String"),
		powerBIColumn("test_name", "String"),
		powerBIColumn("project_id", "String"),
		powerBIColumn("release_id", "String"),
		powerBIColumn("application_id", "String"),
		powerBIColumn("status", "String"),
		powerBIColumn("verdict", "String"),
		powerBIColumn("started_at", "DateTime"),
		powerBIColumn("completed_at", "DateTime"),
		powerBIColumn("duration_seconds", "Double"),
		powerBIColumn("avg_tps", "Double"),
		powerBIColumn("total_vusers", "Int64"),
		powerBIColumn("peak_active_vusers", "Int64"),
		powerBIColumn("total_transactions", "Int64"),
		powerBIColumn("total_errors", "Int64"),
		powerBIColumn("error_pct", "Double"),
		powerBIColumn("avg_ms", "Double"),
		powerBIColumn("min_ms", "Double"),
		powerBIColumn("max_ms", "Double"),
		powerBIColumn("p50_ms", "Double"),
		powerBIColumn("p90_ms", "Double"),
		powerBIColumn("p95_ms", "Double"),
		powerBIColumn("p99_ms", "Double"),
		powerBIColumn("peak_tps", "Double"),
		powerBIColumn("peak_target_tps", "Double"),
		powerBIColumn("sla_evaluated", "Int64"),
		powerBIColumn("sla_passed", "Int64"),
		powerBIColumn("sla_warned", "Int64"),
		powerBIColumn("sla_failed", "Int64"),
		powerBIColumn("apexload_url", "String"),
		powerBIColumn("published_at", "DateTime"),
		powerBIColumn("http_actions", "Int64"),
		powerBIColumn("http1_actions", "Int64"),
		powerBIColumn("http2_actions", "Int64"),
		powerBIColumn("http3_actions", "Int64"),
		powerBIColumn("http_no_response_actions", "Int64"),
		powerBIColumn("http_cache_actions", "Int64"),
		powerBIColumn("http_attempts", "Int64"),
		powerBIColumn("http3_upgrade_eligible", "Int64"),
		powerBIColumn("http3_fallbacks", "Int64"),
		powerBIColumn("http3_fallback_ratio", "Double"),
		powerBIColumn("quic_attempted", "Int64"),
		powerBIColumn("quic_failed", "Int64"),
		powerBIColumn("quic_failure_ratio", "Double"),
		powerBIColumn("protocol_mix_finding", "String"),
	}
}

func powerBITransactionsColumns() []map[string]any {
	return []map[string]any{
		powerBIColumn("run_id", "String"),
		powerBIColumn("publish_id", "String"),
		powerBIColumn("published_at", "DateTime"),
		powerBIColumn("kind", "String"),
		powerBIColumn("name", "String"),
		powerBIColumn("count", "Int64"),
		powerBIColumn("errors", "Int64"),
		powerBIColumn("error_pct", "Double"),
		powerBIColumn("avg_ms", "Double"),
		powerBIColumn("min_ms", "Double"),
		powerBIColumn("max_ms", "Double"),
		powerBIColumn("p50_ms", "Double"),
		powerBIColumn("p90_ms", "Double"),
		powerBIColumn("p95_ms", "Double"),
		powerBIColumn("p99_ms", "Double"),
		powerBIColumn("peak_tps", "Double"),
		powerBIColumn("has_sla", "Bool"),
		powerBIColumn("sla_ms", "Double"),
		powerBIColumn("sla_result", "String"),
		powerBIColumn("http_version", "String"),
		powerBIColumn("http1_count", "Int64"),
		powerBIColumn("http2_count", "Int64"),
		powerBIColumn("http3_count", "Int64"),
		powerBIColumn("http_fallbacks", "Int64"),
		powerBIColumn("http_attempts", "Int64"),
	}
}
