//go:build multiproject_integration

package multiproject

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// suiteConfig is everything the suite needs to reach one deployment. stack.py
// fills it for its own Docker stack; an AKS run supplies the same variables for
// an existing installation (README.md lists them).
type suiteConfig struct {
	Profile        string
	BaseURL        string
	ControllerURLs []string
	AdminAPIKey    string
	ProxySecret    string
	AdminSubject   string
	// TargetURL is the counting target as generators reach it; ObserverURL is
	// the same target as this process reaches it.
	TargetURL       string
	ObserverURL     string
	TargetRunID     string
	TargetLoadToken string
	ObserverToken   string
	// MutateGlobal allows the suite to lower the global concurrent-test limit.
	// Leave it off on a shared installation: the global phases are then skipped.
	MutateGlobal     bool
	EvidenceDir      string
	Prefix           string
	VUsers           int
	PacingMS         int
	MinGenerators    int
	RaceRounds       int
	CountTolerance   float64
	CountToleranceAt int64
}

func loadSuiteConfig(t *testing.T) suiteConfig {
	t.Helper()
	env := func(name string) string { return strings.TrimSpace(os.Getenv(name)) }
	cfg := suiteConfig{
		Profile:         firstNonEmpty(env("APEXLOAD_MP_PROFILE"), "external"),
		BaseURL:         strings.TrimRight(env("APEXLOAD_MP_BASE_URL"), "/"),
		AdminAPIKey:     env("APEXLOAD_MP_ADMIN_API_KEY"),
		ProxySecret:     env("APEXLOAD_MP_PROXY_SECRET"),
		AdminSubject:    env("APEXLOAD_MP_ADMIN_SUBJECT"),
		TargetURL:       strings.TrimRight(env("APEXLOAD_MP_TARGET_URL"), "/"),
		ObserverURL:     strings.TrimRight(env("APEXLOAD_MP_OBSERVER_URL"), "/"),
		TargetRunID:     env("APEXLOAD_MP_TARGET_RUN_ID"),
		TargetLoadToken: env("APEXLOAD_MP_TARGET_LOAD_TOKEN"),
		ObserverToken:   env("APEXLOAD_MP_TARGET_OBSERVER_TOKEN"),
		MutateGlobal:    envBool(env("APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS")),
		EvidenceDir:     env("APEXLOAD_MP_EVIDENCE_DIR"),
		Prefix:          env("APEXLOAD_MP_PREFIX"),
	}
	if cfg.BaseURL == "" {
		t.Skip("APEXLOAD_MP_BASE_URL is not set; start a stack with scripts/multiproject/stack.py (see README.md)")
	}
	for _, raw := range strings.Split(env("APEXLOAD_MP_CONTROLLER_URLS"), ",") {
		if raw = strings.TrimRight(strings.TrimSpace(raw), "/"); raw != "" {
			cfg.ControllerURLs = append(cfg.ControllerURLs, raw)
		}
	}
	for name, value := range map[string]string{
		"APEXLOAD_MP_TARGET_URL":            cfg.TargetURL,
		"APEXLOAD_MP_OBSERVER_URL":          cfg.ObserverURL,
		"APEXLOAD_MP_TARGET_RUN_ID":         cfg.TargetRunID,
		"APEXLOAD_MP_TARGET_LOAD_TOKEN":     cfg.TargetLoadToken,
		"APEXLOAD_MP_TARGET_OBSERVER_TOKEN": cfg.ObserverToken,
	} {
		if value == "" {
			t.Fatalf("%s is required when APEXLOAD_MP_BASE_URL is set", name)
		}
	}
	if cfg.AdminAPIKey == "" && (cfg.ProxySecret == "" || cfg.AdminSubject == "") {
		t.Fatal("set APEXLOAD_MP_ADMIN_API_KEY, or APEXLOAD_MP_PROXY_SECRET and APEXLOAD_MP_ADMIN_SUBJECT for trusted-header auth")
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "mp-" + strconv.FormatInt(time.Now().Unix(), 36)
	}
	cfg.VUsers = envInt(t, "APEXLOAD_MP_VUSERS", 4, 1, 200)
	cfg.PacingMS = envInt(t, "APEXLOAD_MP_PACING_MS", 250, 20, 10000)
	cfg.MinGenerators = envInt(t, "APEXLOAD_MP_MIN_GENERATORS", 2, 1, 1000)
	cfg.RaceRounds = envInt(t, "APEXLOAD_MP_RACE_ROUNDS", 3, 1, 20)
	// Graceful stops let every VUser finish its in-flight request, so the
	// reported and target-observed counts should agree exactly. The small
	// default allowance covers a request whose response was lost in teardown.
	cfg.CountToleranceAt = int64(envInt(t, "APEXLOAD_MP_COUNT_TOLERANCE_REQUESTS", 2, 0, 1000000))
	cfg.CountTolerance = 0.005
	if raw := env("APEXLOAD_MP_COUNT_TOLERANCE_RATIO"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value < 0 || value > 0.5 {
			t.Fatalf("APEXLOAD_MP_COUNT_TOLERANCE_RATIO must be a ratio from 0 to 0.5")
		}
		cfg.CountTolerance = value
	}
	return cfg
}

func envInt(t *testing.T, name string, fallback, low, high int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < low || value > high {
		t.Fatalf("%s must be an integer from %d to %d", name, low, high)
	}
	return value
}

// identity carries one caller's credentials. Tenants use project-scoped
// service-account keys, which work the same way in Docker and on AKS.
type identity struct {
	Name   string
	header http.Header
}

func apiKeyIdentity(name, key string) identity {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+key)
	return identity{Name: name, header: header}
}

func trustedHeaderIdentity(name, subject, proxySecret string) identity {
	header := http.Header{}
	header.Set("X-Auth-Request-User", subject)
	header.Set("X-APEXLoad-Proxy-Secret", proxySecret)
	return identity{Name: name, header: header}
}

// apiClient is a minimal JSON client. It retries only what cannot have
// mutated anything: reads, refused connections, and 503s that the router or a
// non-leader replica returns before a handler runs.
type apiClient struct {
	base string
	id   identity
	http *http.Client
}

const (
	retryWindow     = 45 * time.Second
	maxResponseBody = 32 << 20
)

func newAPIClient(base string, id identity) *apiClient {
	return &apiClient{base: base, id: id, http: &http.Client{Timeout: 2 * time.Minute}}
}

func (c *apiClient) get(ctx context.Context, path string, out any) error {
	_, err := c.do(ctx, http.MethodGet, path, nil, out)
	return err
}

func (c *apiClient) post(ctx context.Context, path string, body, out any) error {
	_, err := c.do(ctx, http.MethodPost, path, body, out)
	return err
}

func (c *apiClient) do(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	return c.doWithHeaders(ctx, method, path, nil, body, out)
}

func (c *apiClient) doWithHeaders(ctx context.Context, method, path string, extra http.Header, body, out any) (http.Header, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(retryWindow)
	delay := 250 * time.Millisecond
	for {
		header, status, raw, err := c.once(ctx, method, path, extra, payload)
		retry := false
		switch {
		case err != nil:
			retry = method == http.MethodGet || isDialFailure(err)
		case status == http.StatusServiceUnavailable:
			retry = method == http.MethodGet || retryableUnavailable(raw)
		case status == http.StatusTooManyRequests:
			retry = true
		}
		if retry && ctx.Err() == nil && time.Now().Add(delay).Before(deadline) {
			select {
			case <-ctx.Done():
			case <-time.After(delay):
			}
			if delay < 2*time.Second {
				delay *= 2
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s %s as %s: %w", method, path, c.id.Name, err)
		}
		if status < 200 || status > 299 {
			return header, &apiError{Method: method, Path: path, Status: status, Body: string(raw)}
		}
		if out != nil && len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return header, fmt.Errorf("decode %s %s: %w", method, path, err)
			}
		}
		return header, nil
	}
}

func (c *apiClient) once(ctx context.Context, method, path string, extra http.Header, payload []byte) (http.Header, int, []byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, 0, nil, err
	}
	for _, headers := range []http.Header{c.id.header, extra} {
		for name, values := range headers {
			for _, value := range values {
				req.Header.Add(name, value)
			}
		}
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, 0, nil, err
	}
	if len(raw) > maxResponseBody {
		return nil, 0, nil, fmt.Errorf("response exceeds %d bytes", maxResponseBody)
	}
	return resp.Header, resp.StatusCode, raw, nil
}

// isDialFailure reports a connection that was never established, so a
// mutating request cannot have reached a handler.
func isDialFailure(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// testRun is the subset of a managed test the suite reads.
type testRun struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Status          string           `json:"status"`
	ProjectID       string           `json:"project_id"`
	PendingReason   string           `json:"pending_reason"`
	StartedAt       *time.Time       `json:"started_at"`
	EndedAt         *time.Time       `json:"ended_at"`
	Error           string           `json:"error"`
	GeneratorShards []generatorShard `json:"generator_shards"`
}

type generatorShard struct {
	Target string `json:"target"`
}

// reservingStatuses mirror testReservesRuntimeCapacity: the states that hold a
// concurrent-test slot until the run is terminal.
var reservingStatuses = map[string]bool{
	"SHAKEOUT": true, "STARTING": true, "INITIALIZING": true,
	"RUNNING": true, "PAUSED": true, "STOPPING": true,
}

var terminalStatuses = map[string]bool{"COMPLETE": true, "FAILED": true, "ABORTED": true}

// targetObserver reads the independent per-run counters from apexfault-target.
// The counter is keyed by the run ID each request carries, so it is ground
// truth for "which run sent this request" that does not depend on the
// controller's own accounting.
type targetObserver struct {
	base   string
	token  string
	runID  string
	http   *http.Client
	mu     sync.Mutex
	server string
}

type targetSnapshot struct {
	RunID            string `json:"run_id"`
	TestID           string `json:"test_id"`
	TargetInstanceID string `json:"target_instance_id"`
	TargetRequests   uint64 `json:"target_requests"`
}

func (o *targetObserver) count(ctx context.Context, testID string) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/snapshot", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+o.token)
	req.Header.Set("X-Apexload-Fault-Test", testID)
	resp, err := o.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("target snapshot for %s: %w", testID, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("target snapshot for %s: HTTP %d: %s", testID, resp.StatusCode, truncate(string(raw), 200))
	}
	var snapshot targetSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return 0, fmt.Errorf("decode target snapshot: %w", err)
	}
	if snapshot.RunID != o.runID || snapshot.TestID != testID {
		return 0, fmt.Errorf("target snapshot identity mismatch: run %q test %q", snapshot.RunID, snapshot.TestID)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.server == "" {
		o.server = snapshot.TargetInstanceID
	} else if o.server != snapshot.TargetInstanceID {
		return 0, fmt.Errorf("target restarted during the suite (instance %s became %s); its counters reset", o.server, snapshot.TargetInstanceID)
	}
	return snapshot.TargetRequests, nil
}

// admissionSampler polls the leader's active-test list for the whole suite
// and records every observation, so admission invariants are checked
// continuously rather than only at the moments a phase asserts something.
type admissionSampler struct {
	client   *apiClient
	interval time.Duration

	mu            sync.Mutex
	globalLimit   int
	projectLimits map[string]int
	tracked       map[string]string // test ID -> project ID
	samples       int
	errors        int
	lastError     string
	maxGlobal     int
	maxByProject  map[string]int
	violations    []string
	transitions   map[string][]statusChange
	lastStatus    map[string]string
	running       map[string]bool
	overlaps      map[string]time.Time
}

type statusChange struct {
	At     time.Time `json:"at"`
	Status string    `json:"status"`
	Reason string    `json:"reason,omitempty"`
}

func newAdmissionSampler(client *apiClient) *admissionSampler {
	return &admissionSampler{
		client:        client,
		interval:      400 * time.Millisecond,
		projectLimits: map[string]int{},
		tracked:       map[string]string{},
		maxByProject:  map[string]int{},
		transitions:   map[string][]statusChange{},
		lastStatus:    map[string]string{},
		running:       map[string]bool{},
		overlaps:      map[string]time.Time{},
	}
}

func (s *admissionSampler) setGlobalLimit(limit int) {
	s.mu.Lock()
	s.globalLimit = limit
	s.mu.Unlock()
}

func (s *admissionSampler) setProjectLimit(projectID string, limit int) {
	s.mu.Lock()
	s.projectLimits[projectID] = limit
	s.mu.Unlock()
}

func (s *admissionSampler) track(testID, projectID string) {
	s.mu.Lock()
	s.tracked[testID] = projectID
	s.mu.Unlock()
}

func (s *admissionSampler) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		s.sample(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *admissionSampler) sample(ctx context.Context) {
	var page struct {
		Tests []testRun `json:"tests"`
	}
	err := s.client.get(ctx, "/api/v1/tests?active_only=true&limit=2000", &page)
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			s.errors++
			s.lastError = err.Error()
		}
		return
	}
	s.samples++
	reserving := 0
	byProject := map[string]int{}
	seen := map[string]string{}
	for _, run := range page.Tests {
		status := strings.ToUpper(run.Status)
		if reservingStatuses[status] {
			reserving++
			byProject[run.ProjectID]++
		}
		if _, ok := s.tracked[run.ID]; ok {
			seen[run.ID] = status
			if s.lastStatus[run.ID] != status {
				s.lastStatus[run.ID] = status
				s.transitions[run.ID] = append(s.transitions[run.ID], statusChange{At: now, Status: status, Reason: run.PendingReason})
			}
		}
	}
	// A tracked run that leaves the active list became terminal (or was
	// deleted); its final status is recorded by the phase that ended it.
	for id := range s.tracked {
		if _, ok := seen[id]; !ok && s.lastStatus[id] != "" && !terminalStatuses[s.lastStatus[id]] {
			s.lastStatus[id] = "INACTIVE"
			s.transitions[id] = append(s.transitions[id], statusChange{At: now, Status: "INACTIVE"})
		}
	}
	if reserving > s.maxGlobal {
		s.maxGlobal = reserving
	}
	if s.globalLimit > 0 && reserving > s.globalLimit {
		s.violations = append(s.violations, fmt.Sprintf("%s: %d tests held capacity with a global limit of %d", now.Format(time.RFC3339Nano), reserving, s.globalLimit))
	}
	for project, count := range byProject {
		if count > s.maxByProject[project] {
			s.maxByProject[project] = count
		}
		if limit := s.projectLimits[project]; limit > 0 && count > limit {
			s.violations = append(s.violations, fmt.Sprintf("%s: project %s held %d slots with a limit of %d", now.Format(time.RFC3339Nano), project, count, limit))
		}
	}
	running := map[string]bool{}
	for id, status := range seen {
		if status == "RUNNING" {
			running[id] = true
		}
	}
	s.running = running
	for key := range s.overlaps {
		if s.overlaps[key].IsZero() && allRunning(running, strings.Split(key, ",")) {
			s.overlaps[key] = now
		}
	}
}

// watchOverlap asks the sampler to record the first sample in which every
// listed run was RUNNING at once.
func (s *admissionSampler) watchOverlap(ids ...string) string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	key := strings.Join(sorted, ",")
	s.mu.Lock()
	if _, ok := s.overlaps[key]; !ok {
		s.overlaps[key] = time.Time{}
	}
	if allRunning(s.running, sorted) {
		s.overlaps[key] = time.Now().UTC()
	}
	s.mu.Unlock()
	return key
}

func (s *admissionSampler) overlapAt(key string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overlaps[key]
}

func (s *admissionSampler) summary() samplerSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	transitions := make(map[string][]statusChange, len(s.transitions))
	for id, changes := range s.transitions {
		transitions[id] = append([]statusChange(nil), changes...)
	}
	maxByProject := make(map[string]int, len(s.maxByProject))
	for project, count := range s.maxByProject {
		maxByProject[project] = count
	}
	return samplerSummary{
		IntervalMS:      s.interval.Milliseconds(),
		Samples:         s.samples,
		Errors:          s.errors,
		LastError:       s.lastError,
		MaxReserving:    s.maxGlobal,
		MaxByProject:    maxByProject,
		Violations:      append([]string{}, s.violations...),
		TrackedStatuses: transitions,
	}
}

type samplerSummary struct {
	IntervalMS      int64                     `json:"interval_ms"`
	Samples         int                       `json:"samples"`
	Errors          int                       `json:"errors"`
	LastError       string                    `json:"last_error,omitempty"`
	MaxReserving    int                       `json:"max_reserving_tests"`
	MaxByProject    map[string]int            `json:"max_reserving_by_project"`
	Violations      []string                  `json:"violations"`
	TrackedStatuses map[string][]statusChange `json:"tracked_status_transitions"`
}

// evidence is written to APEXLOAD_MP_EVIDENCE_DIR/report.json whether the
// suite passes or fails, so a failed run still leaves its timeline behind.
type evidence struct {
	mu         sync.Mutex
	StartedAt  time.Time               `json:"started_at"`
	FinishedAt time.Time               `json:"finished_at"`
	Profile    string                  `json:"profile"`
	Prefix     string                  `json:"prefix"`
	Controller map[string]any          `json:"controller,omitempty"`
	Limits     map[string]any          `json:"limits"`
	Tenants    []tenantEvidence        `json:"tenants"`
	Phases     []phaseEvidence         `json:"phases"`
	Runs       map[string]*runEvidence `json:"runs"`
	Sampler    samplerSummary          `json:"sampler"`
	Notes      []string                `json:"notes,omitempty"`
}

type tenantEvidence struct {
	Key           string `json:"key"`
	ProjectID     string `json:"project_id"`
	ProjectName   string `json:"project_name"`
	MaxConcurrent int    `json:"max_concurrent_tests"`
}

type phaseEvidence struct {
	Name       string    `json:"name"`
	Outcome    string    `json:"outcome"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type runEvidence struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Tenant           string     `json:"tenant"`
	ProjectID        string     `json:"project_id"`
	Phase            string     `json:"phase"`
	FinalStatus      string     `json:"final_status,omitempty"`
	PendingReason    string     `json:"pending_reason,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
	Generators       []string   `json:"generators,omitempty"`
	ReportedRequests *int64     `json:"reported_requests,omitempty"`
	ReportedErrors   *int64     `json:"reported_errors,omitempty"`
	TargetRequests   *uint64    `json:"target_requests,omitempty"`
	RequestNames     []string   `json:"request_names,omitempty"`
}

func (e *evidence) note(format string, args ...any) {
	e.mu.Lock()
	e.Notes = append(e.Notes, fmt.Sprintf(format, args...))
	e.mu.Unlock()
}

func (e *evidence) run(id string) *runEvidence {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Runs[id] == nil {
		e.Runs[id] = &runEvidence{ID: id}
	}
	return e.Runs[id]
}

func (e *evidence) write(dir string) error {
	if dir == "" {
		return nil
	}
	e.mu.Lock()
	raw, err := json.MarshalIndent(e, "", "  ")
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.json"), append(raw, '\n'), 0o600)
}

// eventually polls check until it returns true or the timeout passes.
func eventually(ctx context.Context, timeout, every time.Duration, check func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		done, err := check()
		if err != nil {
			last = err
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			if last != nil {
				return fmt.Errorf("timed out after %s: %w", timeout, last)
			}
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}
