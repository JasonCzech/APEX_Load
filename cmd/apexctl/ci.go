package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	ciOutcomePassed             = "passed"
	ciOutcomeSLABreached        = "sla_breached"
	ciOutcomeExecutionError     = "execution_error"
	ciOutcomeConfigurationError = "configuration_error"
	ciOutcomeTimedOut           = "timed_out"
	ciOutcomeCancelled          = "cancelled"
)

type ciConfigurationFailure struct {
	err error
}

func (e *ciConfigurationFailure) Error() string {
	if e == nil || e.err == nil {
		return "invalid CI configuration"
	}
	return e.err.Error()
}

func (e *ciConfigurationFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func ciConfigurationErrorf(format string, values ...any) error {
	return &ciConfigurationFailure{err: fmt.Errorf(format, values...)}
}

var (
	ciPollInterval          = 2 * time.Second
	newGitHubOIDCHTTPClient = func() *http.Client {
		return &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("OIDC endpoint redirects are not allowed")
			},
		}
	}
)

type ciArgs struct {
	executionMode            string
	controllerURL            string
	projectID                string
	scriptPath               string
	scriptID                 string
	releaseID                string
	testName                 string
	authMode                 string
	apiKey                   string
	vusers                   int
	duration                 time.Duration
	rampUp                   time.Duration
	baseURL                  string
	variablesFile            string
	p95LatencyMs             *float64
	maxErrorPct              *float64
	maxAvgServerProcessingMs *float64
	maxAvgTransferMs         *float64
	maxErrorCount            *int
	requireSLA               bool
	requireResults           bool
	timeout                  time.Duration
	abortOnCancel            bool
	publishCheck             bool
	jsonPath                 string
	junitPath                string
	githubOutput             string
	githubSummary            string
	idempotencyKey           string
}

type optionalFloat struct{ value **float64 }

func (o optionalFloat) String() string { return "" }
func (o optionalFloat) Set(raw string) error {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return err
	}
	*o.value = &parsed
	return nil
}

type optionalInt struct{ value **int }

func (o optionalInt) String() string { return "" }
func (o optionalInt) Set(raw string) error {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	*o.value = &parsed
	return nil
}

type durationValue struct {
	value *time.Duration
}

func (d durationValue) String() string {
	if d.value == nil {
		return ""
	}
	return d.value.String()
}
func (d durationValue) Set(raw string) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	*d.value = parsed
	return nil
}

func defaultCIArgs() ciArgs {
	return ciArgs{
		executionMode:  "controller",
		controllerURL:  strings.TrimSpace(os.Getenv("APEXLOAD_API")),
		authMode:       "oidc",
		apiKey:         strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")),
		vusers:         10,
		duration:       time.Minute,
		requireSLA:     true,
		requireResults: true,
		timeout:        30 * time.Minute,
		abortOnCancel:  true,
		publishCheck:   true,
		jsonPath:       "apexload-result.json",
		junitPath:      "apexload-results.xml",
	}
}

func parseCIArgs(arguments []string) (ciArgs, error) {
	args := defaultCIArgs()
	fs := flag.NewFlagSet("apexctl ci run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&args.executionMode, "execution-mode", args.executionMode, "controller or local")
	fs.StringVar(&args.controllerURL, "controller-url", args.controllerURL, "controller base URL")
	fs.StringVar(&args.controllerURL, "api", args.controllerURL, "controller base URL")
	fs.StringVar(&args.projectID, "project-id", "", "controller project ID")
	fs.StringVar(&args.scriptPath, "script-path", "", "path to a JSON DSL script")
	fs.StringVar(&args.scriptID, "script-id", "", "existing controller script ID")
	fs.StringVar(&args.releaseID, "release-id", "", "release correlation ID")
	fs.StringVar(&args.testName, "test-name", "", "test name")
	fs.StringVar(&args.authMode, "auth-mode", args.authMode, "oidc or api-key")
	fs.IntVar(&args.vusers, "vusers", args.vusers, "virtual users")
	fs.Var(durationValue{value: &args.duration}, "duration", "test duration")
	fs.Var(durationValue{value: &args.rampUp}, "ramp-up", "ramp-up duration")
	fs.StringVar(&args.baseURL, "base-url", "", "policy-controlled base URL override")
	fs.StringVar(&args.variablesFile, "variables-file", "", "non-secret JSON variables")
	fs.Var(optionalFloat{value: &args.p95LatencyMs}, "p95-latency-ms", "maximum P95 latency")
	fs.Var(optionalFloat{value: &args.maxErrorPct}, "max-error-pct", "maximum error percentage")
	fs.Var(optionalFloat{value: &args.maxAvgServerProcessingMs}, "max-avg-server-processing-ms", "maximum server processing time")
	fs.Var(optionalFloat{value: &args.maxAvgTransferMs}, "max-avg-transfer-ms", "maximum transfer time")
	fs.Var(optionalInt{value: &args.maxErrorCount}, "max-error-count", "maximum error count")
	fs.BoolVar(&args.requireSLA, "require-sla", args.requireSLA, "require an evaluated SLA")
	fs.BoolVar(&args.requireResults, "require-results", args.requireResults, "require transactions")
	fs.Var(durationValue{value: &args.timeout}, "timeout", "CI run timeout")
	fs.BoolVar(&args.abortOnCancel, "abort-on-cancel", args.abortOnCancel, "abort a remote run after cancellation")
	fs.BoolVar(&args.publishCheck, "publish-check", args.publishCheck, "publish a controller-owned GitHub Check")
	fs.StringVar(&args.jsonPath, "json", args.jsonPath, "JSON evidence path")
	fs.StringVar(&args.junitPath, "junit", args.junitPath, "JUnit evidence path")
	fs.StringVar(&args.githubOutput, "github-output", "", "GitHub output file")
	fs.StringVar(&args.githubSummary, "github-summary", "", "GitHub job summary file")
	fs.StringVar(&args.idempotencyKey, "idempotency-key", "", "explicit idempotency key")
	if err := fs.Parse(arguments); err != nil {
		return args, err
	}
	if fs.NArg() != 0 {
		return args, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	args.executionMode = strings.ToLower(strings.TrimSpace(args.executionMode))
	args.authMode = strings.ToLower(strings.TrimSpace(args.authMode))
	args.controllerURL = strings.TrimSpace(args.controllerURL)
	args.projectID = strings.TrimSpace(args.projectID)
	args.scriptPath = strings.TrimSpace(args.scriptPath)
	args.scriptID = strings.TrimSpace(args.scriptID)
	return args, validateCIArgs(args)
}

func validateCIArgs(args ciArgs) error {
	if args.executionMode != "controller" && args.executionMode != "local" {
		return fmt.Errorf("--execution-mode must be controller or local")
	}
	if (strings.TrimSpace(args.scriptPath) == "") == (strings.TrimSpace(args.scriptID) == "") {
		return fmt.Errorf("exactly one of --script-path or --script-id is required")
	}
	if args.vusers < 1 || args.duration <= 0 || args.rampUp < 0 || args.timeout <= 0 {
		return fmt.Errorf("vusers, duration, ramp-up, or timeout is outside its valid range")
	}
	if args.rampUp > args.duration {
		return fmt.Errorf("--ramp-up cannot exceed --duration")
	}
	if strings.TrimSpace(args.jsonPath) == "" || strings.TrimSpace(args.junitPath) == "" {
		return fmt.Errorf("--json and --junit evidence paths are required")
	}
	if args.executionMode == "local" {
		if args.scriptID != "" {
			return fmt.Errorf("--script-id is only valid in controller mode")
		}
		if args.publishCheck {
			return fmt.Errorf("--publish-check must be false in local mode")
		}
		return nil
	}
	if strings.TrimSpace(args.controllerURL) == "" || strings.TrimSpace(args.projectID) == "" {
		return fmt.Errorf("--controller-url and --project-id are required in controller mode")
	}
	if err := validateControllerURL(args.controllerURL); err != nil {
		return err
	}
	switch args.authMode {
	case "oidc":
	case "api-key":
		if strings.TrimSpace(args.apiKey) == "" {
			return fmt.Errorf("APEXLOAD_API_KEY is required when --auth-mode=api-key")
		}
	default:
		return fmt.Errorf("--auth-mode must be oidc or api-key")
	}
	return nil
}

func validateControllerURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("--controller-url must be an absolute URL without credentials, query, or fragment")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()) {
		return nil
	}
	if parsed.Scheme == "http" && strings.EqualFold(os.Getenv("APEXLOAD_ALLOW_INSECURE_HTTP"), "true") {
		return nil
	}
	return fmt.Errorf("--controller-url must use HTTPS (set APEXLOAD_ALLOW_INSECURE_HTTP=true only for an isolated development controller)")
}

type CIResult struct {
	SchemaVersion int      `json:"schema_version"`
	Outcome       string   `json:"outcome"`
	TestID        string   `json:"test_id,omitempty"`
	Status        string   `json:"status"`
	SLAEvaluated  bool     `json:"sla_evaluated"`
	SLABreached   bool     `json:"sla_breached"`
	SLADetails    []string `json:"sla_details,omitempty"`
	Transactions  int64    `json:"transactions"`
	Errors        int64    `json:"errors"`
	TPS           float64  `json:"tps"`
	DurationMS    int64    `json:"duration_ms"`
	ReportURL     string   `json:"report_url,omitempty"`
	CheckURL      string   `json:"check_url,omitempty"`
	Message       string   `json:"message,omitempty"`
}

func ciCmd() {
	if len(os.Args) < 3 || os.Args[2] == "help" || os.Args[2] == "--help" || os.Args[2] == "-h" {
		printCIUsage()
		return
	}
	if os.Args[2] != "run" {
		fmt.Fprintf(os.Stderr, "unknown ci command: %s\n", os.Args[2])
		printCIUsage()
		os.Exit(2)
	}

	args, parseErr := parseCIArgs(os.Args[3:])
	if errors.Is(parseErr, flag.ErrHelp) {
		printCIUsage()
		return
	}
	if parseErr != nil {
		result := CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID", Message: parseErr.Error()}
		_ = finalizeCIResult(args, result)
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", parseErr)
		os.Exit(2)
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, args.timeout)
	defer cancel()

	result, runErr := executeCI(ctx, args)
	if runErr != nil && result.Message == "" {
		result.Message = runErr.Error()
	}
	if runErr != nil && result.Outcome == "" {
		result.Outcome = ciOutcomeExecutionError
		result.Status = "FAILED"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Outcome = ciOutcomeTimedOut
		result.Status = "TIMED_OUT"
	} else if errors.Is(ctx.Err(), context.Canceled) {
		result.Outcome = ciOutcomeCancelled
		result.Status = "CANCELLED"
	}
	if result.SchemaVersion == 0 {
		result.SchemaVersion = ciSchemaVersion
	}
	if err := enforceCIRequirements(args, &result); err != nil && result.Message == "" {
		result.Message = err.Error()
	}
	if err := finalizeCIResult(args, result); err != nil {
		fmt.Fprintf(os.Stderr, "evidence error: %v\n", err)
		os.Exit(2)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "CI run failed: %v\n", runErr)
	}
	os.Exit(ciExitCode(result.Outcome))
}

func executeCI(ctx context.Context, args ciArgs) (CIResult, error) {
	variables, err := loadCIVariables(args.variablesFile)
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, err
	}
	if args.executionMode == "local" {
		return executeLocalCI(ctx, args, variables)
	}
	return executeControllerCI(ctx, args, variables)
}

func executeLocalCI(ctx context.Context, args ciArgs, variables map[string]string) (CIResult, error) {
	run := runArgs{
		script: args.scriptPath, baseURL: args.baseURL, env: variables,
		vusers: args.vusers, duration: args.duration, rampUp: args.rampUp,
		p95LatencyMs: args.p95LatencyMs, maxErrorPct: args.maxErrorPct,
		maxAvgServerProcessingMs: args.maxAvgServerProcessingMs,
		maxAvgTransferMs:         args.maxAvgTransferMs, maxErrorCount: args.maxErrorCount,
	}
	script, err := loadScriptForRun(args.scriptPath, run)
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, fmt.Errorf("prepare script: %w", err)
	}
	eng, err := engine.New(engine.Config{Script: script, VUsers: args.vusers, Duration: args.duration, RampUp: args.rampUp})
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, fmt.Errorf("create engine: %w", err)
	}
	started := time.Now()
	runResult, runErr := eng.Run(ctx)
	result := CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomePassed, Status: "COMPLETE", DurationMS: time.Since(started).Milliseconds()}
	if runResult != nil {
		result.DurationMS = runResult.Duration.Milliseconds()
		result.Transactions = runResult.TotalTransactions
		result.Errors = runResult.TotalErrors
		result.TPS = runResult.FinalSnapshot.TPS
		sla := evaluateLocalSLA(runResult, resolveGoalConfig(script, run))
		if sla != nil {
			result.SLAEvaluated = sla.Evaluated
			result.SLABreached = sla.Breached
			result.SLADetails = append([]string(nil), sla.Details...)
		}
	}
	if runErr != nil {
		result.Outcome = ciOutcomeExecutionError
		result.Status = "FAILED"
		return result, runErr
	}
	classifyCIResult(&result)
	return result, nil
}

func executeControllerCI(ctx context.Context, args ciArgs, variables map[string]string) (CIResult, error) {
	clientAPIKey := ""
	if args.authMode == "api-key" {
		clientAPIKey = args.apiKey
	}
	client := NewApiClientWithAuth(args.controllerURL, clientAPIKey, "")
	auth := &ciAuthSession{mode: strings.ToLower(args.authMode), projectID: args.projectID}
	if err := auth.ensure(ctx, client); err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciFailureOutcome(err), Status: "AUTH_FAILED"}, err
	}

	request := CIRunRequest{
		ProjectID: args.projectID, Name: args.testName, ReleaseID: args.releaseID,
		ScriptPath: args.scriptPath, ScriptID: args.scriptID,
		Execution: CIExecution{VUsers: args.vusers, Duration: args.duration.String(), RampUp: args.rampUp.String(), BaseURL: args.baseURL, Variables: variables},
		Goals: CIGoals{
			P95LatencyMs: args.p95LatencyMs, MaxErrorPct: args.maxErrorPct,
			MaxAvgServerProcessingMs: args.maxAvgServerProcessingMs,
			MaxAvgTransferMs:         args.maxAvgTransferMs, MaxErrorCount: args.maxErrorCount,
			RequireSLA: args.requireSLA, RequireResults: args.requireResults,
		},
		Source: sourceFromEnvironment(), PublishCheck: args.publishCheck,
	}
	if args.scriptPath != "" {
		raw, err := os.ReadFile(args.scriptPath)
		if err != nil {
			return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, fmt.Errorf("read script: %w", err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, fmt.Errorf("script must be a JSON object")
		}
		request.Script = json.RawMessage(raw)
	}
	idempotencyKey := args.idempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = deriveCIIdempotencyKey(request)
	}
	created, err := client.CreateCIRun(ctx, request, idempotencyKey)
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciFailureOutcome(err), Status: "CREATE_FAILED"}, err
	}
	result := ciResultFromResponse(created)

	for {
		if isTerminalCIResponse(created) {
			classifyCIResult(&result)
			if checkErr := enforceRequiredCICheck(args, created, &result); checkErr != nil {
				return result, checkErr
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			if args.abortOnCancel {
				abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = auth.ensure(abortCtx, client)
				_, _ = client.AbortCIRun(abortCtx, created.TestID)
				cancel()
			}
			return result, ctx.Err()
		case <-time.After(ciPollInterval):
		}
		if err := auth.ensure(ctx, client); err != nil {
			result.Outcome = ciFailureOutcome(err)
			result.Status = "AUTH_REFRESH_FAILED"
			return result, fmt.Errorf("refresh controller authentication: %w", err)
		}
		created, err = client.GetCIRun(ctx, created.TestID)
		if err != nil {
			result.Outcome = ciFailureOutcome(err)
			result.Status = "STATUS_FAILED"
			return result, err
		}
		result = ciResultFromResponse(created)
	}
}

type ciAuthSession struct {
	mode      string
	projectID string
	expiresAt time.Time
	discovery *CIDiscovery
}

func (a *ciAuthSession) ensure(ctx context.Context, client *ApiClient) error {
	if a.mode == "api-key" {
		if strings.TrimSpace(client.APIKey) == "" {
			return ciConfigurationErrorf("APEXLOAD_API_KEY is not set")
		}
		return nil
	}
	if client.BearerToken != "" && time.Until(a.expiresAt) > time.Minute {
		return nil
	}
	if a.discovery == nil {
		discovery, err := client.DiscoverCI(ctx)
		if err != nil {
			return fmt.Errorf("discover controller CI authentication: %w", err)
		}
		if !containsCIString(discovery.AuthModes, "oidc") || strings.TrimSpace(discovery.OIDCAudience) == "" {
			return ciConfigurationErrorf("controller does not advertise GitHub Actions OIDC")
		}
		a.discovery = discovery
	}
	idToken, err := requestGitHubIDToken(ctx, a.discovery.OIDCAudience)
	if err != nil {
		return err
	}
	exchanged, err := client.ExchangeGitHubOIDC(ctx, CIExchangeRequest{IDToken: idToken, ProjectID: a.projectID})
	if err != nil {
		return fmt.Errorf("exchange GitHub OIDC token: %w", err)
	}
	client.BearerToken = exchanged.AccessToken
	a.expiresAt = exchanged.ExpiresAt
	if a.expiresAt.IsZero() && exchanged.ExpiresIn > 0 {
		a.expiresAt = time.Now().Add(time.Duration(exchanged.ExpiresIn) * time.Second)
	}
	if a.expiresAt.IsZero() {
		a.expiresAt = time.Now().Add(5 * time.Minute)
	}
	return nil
}

func requestGitHubIDToken(ctx context.Context, audience string) (string, error) {
	requestURL := strings.TrimSpace(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"))
	requestToken := strings.TrimSpace(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
	if requestURL == "" || requestToken == "" {
		return "", ciConfigurationErrorf("GitHub OIDC environment is unavailable; grant id-token: write or use --auth-mode=api-key")
	}
	parsed, err := url.Parse(requestURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", ciConfigurationErrorf("ACTIONS_ID_TOKEN_REQUEST_URL is not a trusted HTTPS URL")
	}
	if err := validateGitHubOIDCRequestOrigin(parsed); err != nil {
		return "", &ciConfigurationFailure{err: err}
	}
	query := parsed.Query()
	query.Set("audience", audience)
	parsed.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+requestToken)
	req.Header.Set("Accept", "application/json")
	client := newGitHubOIDCHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request GitHub OIDC token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("GitHub OIDC endpoint returned %s", resp.Status)
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil || strings.TrimSpace(out.Value) == "" {
		return "", fmt.Errorf("GitHub OIDC endpoint returned an invalid token response")
	}
	return out.Value, nil
}

func validateGitHubOIDCRequestOrigin(requestURL *url.URL) error {
	serverRaw := strings.TrimSpace(os.Getenv("GITHUB_SERVER_URL"))
	serverURL, err := url.Parse(serverRaw)
	if err != nil || serverURL.Scheme != "https" || serverURL.Host == "" || serverURL.User != nil {
		return fmt.Errorf("GITHUB_SERVER_URL is unavailable or invalid")
	}
	requestHost := strings.ToLower(requestURL.Hostname())
	serverHost := strings.ToLower(serverURL.Hostname())
	if serverHost == "github.com" {
		if requestHost != "pipelines.actions.githubusercontent.com" {
			return fmt.Errorf("GitHub.com OIDC request URL has an unexpected host")
		}
		return nil
	}
	if requestHost != serverHost || effectiveURLPort(requestURL) != effectiveURLPort(serverURL) {
		return fmt.Errorf("GHES OIDC request URL must use the configured GitHub server origin")
	}
	return nil
}

func loadCIVariables(path string) (map[string]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read variables file: %w", err)
	}
	var variables map[string]string
	if err := json.Unmarshal(raw, &variables); err != nil {
		return nil, fmt.Errorf("variables file must be a JSON object of string values: %w", err)
	}
	for key := range variables {
		normalized := strings.ToLower(key)
		for _, sensitive := range []string{"password", "passwd", "secret", "token", "credential", "api_key", "apikey"} {
			if strings.Contains(normalized, sensitive) {
				return nil, fmt.Errorf("variables file key %q looks sensitive; use a controller-managed {{secret.*}} reference", key)
			}
		}
	}
	return variables, nil
}

func sourceFromEnvironment() CISource {
	source := CISource{
		Provider: "github", ServerURL: os.Getenv("GITHUB_SERVER_URL"), APIURL: os.Getenv("GITHUB_API_URL"),
		Repository: os.Getenv("GITHUB_REPOSITORY"), RepositoryID: os.Getenv("GITHUB_REPOSITORY_ID"),
		HeadRepository: os.Getenv("GITHUB_HEAD_REPOSITORY"), HeadRepositoryID: os.Getenv("GITHUB_HEAD_REPOSITORY_ID"),
		Ref: os.Getenv("GITHUB_REF"), SHA: os.Getenv("GITHUB_SHA"), WorkflowRef: os.Getenv("GITHUB_WORKFLOW_REF"),
		RunID: os.Getenv("GITHUB_RUN_ID"), RunAttempt: os.Getenv("GITHUB_RUN_ATTEMPT"), Actor: os.Getenv("GITHUB_ACTOR"),
		EventName: os.Getenv("GITHUB_EVENT_NAME"), Environment: os.Getenv("GITHUB_ENVIRONMENT"), RunnerEnvironment: os.Getenv("RUNNER_ENVIRONMENT"),
		HeadRef: os.Getenv("GITHUB_HEAD_REF"), HeadSHA: os.Getenv("GITHUB_HEAD_SHA"),
	}
	if !strings.EqualFold(os.Getenv("GITHUB_ACTIONS"), "true") {
		source.Provider = "local"
	}
	populatePullRequestSource(&source, os.Getenv("GITHUB_EVENT_PATH"))
	return source
}

func populatePullRequestSource(source *CISource, eventPath string) {
	if source == nil || strings.TrimSpace(eventPath) == "" {
		return
	}
	raw, err := os.ReadFile(eventPath)
	if err != nil {
		return
	}
	var event struct {
		PullRequest struct {
			Head struct {
				SHA  string `json:"sha"`
				Repo struct {
					FullName string      `json:"full_name"`
					ID       json.Number `json:"id"`
				} `json:"repo"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&event) != nil {
		return
	}
	if source.HeadRepository == "" {
		source.HeadRepository = event.PullRequest.Head.Repo.FullName
	}
	if source.HeadRepositoryID == "" {
		source.HeadRepositoryID = event.PullRequest.Head.Repo.ID.String()
	}
	if source.HeadSHA == "" {
		source.HeadSHA = event.PullRequest.Head.SHA
	}
}

func deriveCIIdempotencyKey(request CIRunRequest) string {
	scriptIdentity := request.ScriptID
	if scriptIdentity == "" {
		hash := sha256.Sum256(request.Script)
		scriptIdentity = request.ScriptPath + ":" + hex.EncodeToString(hash[:])
	}
	raw := strings.Join([]string{
		request.Source.ServerURL, request.Source.RepositoryID, request.Source.Repository,
		request.Source.WorkflowRef, request.Source.RunID, request.Source.RunAttempt, scriptIdentity,
	}, "\x00")
	hash := sha256.Sum256([]byte(raw))
	return "apexload-ci-v1-" + hex.EncodeToString(hash[:])
}

func ciResultFromResponse(response *CIRunResponse) CIResult {
	if response == nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeExecutionError, Status: "UNKNOWN"}
	}
	transactions, errorsCount, tps, durationMS := response.Transactions, response.Errors, response.TPS, response.DurationMS
	if transactions == 0 && errorsCount == 0 && tps == 0 && durationMS == 0 {
		transactions, errorsCount, tps, durationMS = response.Metrics.Transactions, response.Metrics.Errors, response.Metrics.TPS, response.Metrics.DurationMS
	}
	return CIResult{
		SchemaVersion: ciSchemaVersion, Outcome: response.Outcome, TestID: response.TestID, Status: response.Status,
		SLAEvaluated: response.SLAEvaluated, SLABreached: response.SLABreached, SLADetails: append([]string(nil), response.SLADetails...),
		Transactions: transactions, Errors: errorsCount, TPS: tps,
		DurationMS: durationMS, ReportURL: response.ReportURL, CheckURL: response.CheckURL, Message: response.Error,
	}
}

func isTerminalCIResponse(response *CIRunResponse) bool {
	if response == nil {
		return false
	}
	if containsCIString([]string{ciOutcomePassed, ciOutcomeSLABreached, ciOutcomeExecutionError, ciOutcomeConfigurationError, ciOutcomeTimedOut, ciOutcomeCancelled}, response.Outcome) {
		return true
	}
	switch strings.ToUpper(response.Status) {
	case "COMPLETE", "FAILED", "ABORTED", "TIMED_OUT", "CANCELLED":
		return true
	default:
		return false
	}
}

func classifyCIResult(result *CIResult) {
	if result == nil {
		return
	}
	if containsCIString([]string{ciOutcomeSLABreached, ciOutcomeExecutionError, ciOutcomeConfigurationError, ciOutcomeTimedOut, ciOutcomeCancelled}, result.Outcome) {
		return
	}
	if result.SLABreached {
		result.Outcome = ciOutcomeSLABreached
		return
	}
	switch strings.ToUpper(result.Status) {
	case "FAILED":
		result.Outcome = ciOutcomeExecutionError
	case "ABORTED", "CANCELLED":
		result.Outcome = ciOutcomeCancelled
	case "TIMED_OUT":
		result.Outcome = ciOutcomeTimedOut
	default:
		result.Outcome = ciOutcomePassed
	}
}

func enforceCIRequirements(args ciArgs, result *CIResult) error {
	if result == nil || result.Outcome != ciOutcomePassed {
		return nil
	}
	if args.requireSLA && !result.SLAEvaluated {
		result.Outcome = ciOutcomeConfigurationError
		result.Status = "MISSING_SLA"
		return fmt.Errorf("no SLA thresholds were evaluated")
	}
	if args.requireResults && result.Transactions == 0 {
		result.Outcome = ciOutcomeExecutionError
		result.Status = "NO_RESULTS"
		return fmt.Errorf("the test produced no transactions")
	}
	return nil
}

func ciFailureOutcome(err error) string {
	if err == nil {
		return ciOutcomeExecutionError
	}
	var configurationFailure *ciConfigurationFailure
	if errors.As(err, &configurationFailure) {
		return ciOutcomeConfigurationError
	}
	var apiErr *CIAPIError
	if !errors.As(err, &apiErr) {
		// Transport, timeout, TLS, and malformed remote responses are execution
		// failures. They are not actionable changes to the load-test inputs.
		return ciOutcomeExecutionError
	}
	switch apiErr.StatusCode {
	case http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusUnprocessableEntity:
		return ciOutcomeConfigurationError
	default:
		// Rate limiting and server-side failures remain execution errors. The
		// same conservative classification applies to unknown status codes.
		return ciOutcomeExecutionError
	}
}

func enforceRequiredCICheck(args ciArgs, response *CIRunResponse, result *CIResult) error {
	if !args.publishCheck || response == nil || result == nil {
		return nil
	}
	message := ""
	if strings.TrimSpace(response.CheckURL) == "" {
		message = "the controller did not return the required GitHub Check URL"
	} else if strings.HasPrefix(strings.TrimSpace(response.Error), "GitHub Check update failed:") {
		message = strings.TrimSpace(response.Error)
	}
	if message == "" {
		return nil
	}
	result.Outcome = ciOutcomeExecutionError
	result.Status = "CHECK_FAILED"
	result.Message = message
	return fmt.Errorf("required GitHub Check failed: %s", message)
}

func finalizeCIResult(args ciArgs, result CIResult) error {
	if result.SchemaVersion == 0 {
		result.SchemaVersion = ciSchemaVersion
	}
	if err := ensureEvidenceParent(args.jsonPath); err != nil {
		return err
	}
	if err := ensureEvidenceParent(args.junitPath); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(args.jsonPath, encoded, 0o600); err != nil {
		return fmt.Errorf("write JSON evidence: %w", err)
	}
	var runErr error
	if result.Outcome != ciOutcomePassed && result.Outcome != ciOutcomeSLABreached {
		runErr = errors.New(firstNonEmptyCLI(result.Message, result.Outcome))
	}
	sla := &SLASummary{Evaluated: result.SLAEvaluated, Breached: result.SLABreached, Details: result.SLADetails}
	if err := writeJUnitReport(args.junitPath, junitInput{
		suiteName: "apexload:ci", classname: "apexload", duration: time.Duration(result.DurationMS) * time.Millisecond,
		transactions: result.Transactions, errors: result.Errors, tps: result.TPS, runErr: runErr, sla: sla,
	}); err != nil {
		return err
	}
	if args.githubSummary != "" {
		if err := writeCISummary(args.githubSummary, result); err != nil {
			return err
		}
	}
	if args.githubOutput != "" {
		// Outputs are written last. The private completion marker allows the
		// composite wrapper to distinguish a finalized failing gate from a CLI
		// process that exited midway through evidence generation.
		if err := writeCIOutputs(args.githubOutput, args, result); err != nil {
			return err
		}
	}
	fmt.Print(string(encoded))
	return nil
}

func ensureEvidenceParent(path string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create evidence directory %s: %w", parent, err)
	}
	return nil
}

func writeCIOutputs(path string, args ciArgs, result CIResult) error {
	values := map[string]string{
		"outcome": result.Outcome, "test-id": result.TestID, "status": result.Status,
		"sla-evaluated": strconv.FormatBool(result.SLAEvaluated), "sla-breached": strconv.FormatBool(result.SLABreached),
		"transactions": strconv.FormatInt(result.Transactions, 10), "errors": strconv.FormatInt(result.Errors, 10),
		"tps": strconv.FormatFloat(result.TPS, 'f', -1, 64), "duration-ms": strconv.FormatInt(result.DurationMS, 10),
		"report-url": result.ReportURL, "check-url": result.CheckURL,
		"json-path": absoluteOutputPath(args.jsonPath), "junit-path": absoluteOutputPath(args.junitPath),
		"apexload-evidence-complete": "true",
	}
	var builder strings.Builder
	for _, key := range []string{"outcome", "test-id", "status", "sla-evaluated", "sla-breached", "transactions", "errors", "tps", "duration-ms", "report-url", "check-url", "json-path", "junit-path", "apexload-evidence-complete"} {
		appendGitHubOutput(&builder, key, values[key])
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GitHub output: %w", err)
	}
	defer file.Close()
	_, err = io.WriteString(file, builder.String())
	return err
}

func writeCISummary(path string, result CIResult) error {
	status := "✅ Passed"
	if result.Outcome != ciOutcomePassed {
		status = "❌ " + result.Outcome
	}
	text := fmt.Sprintf("## APEX Load performance gate\n\n%s\n\n| Metric | Value |\n|---|---:|\n| Status | %s |\n| Transactions | %d |\n| Errors | %d |\n| TPS | %.2f |\n| Duration | %d ms |\n| SLA evaluated | %t |\n| SLA breached | %t |\n",
		status, markdownCell(result.Status), result.Transactions, result.Errors, result.TPS, result.DurationMS, result.SLAEvaluated, result.SLABreached)
	if result.ReportURL != "" {
		reportURL := strings.ReplaceAll(strings.ReplaceAll(result.ReportURL, "\r", ""), "\n", "")
		text += "\n[Open APEX Load report](" + strings.ReplaceAll(reportURL, ")", "%29") + ")\n"
	}
	if len(result.SLADetails) > 0 {
		text += "\n### SLA details\n"
		for _, detail := range result.SLADetails {
			text += "\n- " + strings.ReplaceAll(strings.ReplaceAll(detail, "\r", " "), "\n", " ")
		}
		text += "\n"
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GitHub summary: %w", err)
	}
	defer file.Close()
	_, err = io.WriteString(file, text)
	return err
}

func appendGitHubOutput(builder *strings.Builder, key, value string) {
	delimiter := githubOutputDelimiter(key, value)
	builder.WriteString(key)
	builder.WriteString("<<")
	builder.WriteString(delimiter)
	builder.WriteByte('\n')
	builder.WriteString(value)
	// This newline separates the payload from the delimiter. It is always
	// present so a trailing newline in value remains part of the value.
	builder.WriteByte('\n')
	builder.WriteString(delimiter)
	builder.WriteByte('\n')
}

func githubOutputDelimiter(key, value string) string {
	for attempt := 0; ; attempt++ {
		digest := sha256.Sum256([]byte("apexload-github-output-v1\x00" + key + "\x00" + strconv.Itoa(attempt)))
		candidate := "__APEXLOAD_OUTPUT_" + hex.EncodeToString(digest[:]) + "__"
		if !containsGitHubOutputDelimiterLine(value, candidate) {
			return candidate
		}
	}
}

func containsGitHubOutputDelimiterLine(value, delimiter string) bool {
	for _, line := range strings.Split(value, "\n") {
		// A runner reading CRLF environment files treats the CR as part of the
		// line ending. Consider it a collision as well.
		if strings.TrimSuffix(line, "\r") == delimiter {
			return true
		}
	}
	return false
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}

func absoluteOutputPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absolute
}

func containsCIString(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func ciExitCode(outcome string) int {
	switch outcome {
	case ciOutcomePassed:
		return 0
	case ciOutcomeSLABreached:
		return 1
	case ciOutcomeConfigurationError:
		return 2
	case ciOutcomeCancelled:
		return 4
	default:
		return 3
	}
}

func printCIUsage() {
	fmt.Println(`apexctl ci run - run a GitHub Actions performance gate

Usage:
  apexctl ci run --controller-url https://apexload.example --project-id <id> --script-path script.json [flags]
  apexctl ci run --execution-mode local --publish-check=false --script-path script.json [flags]

Authentication:
  OIDC is the default and requires GitHub Actions id-token: write.
  Use --auth-mode=api-key with APEXLOAD_API_KEY for the scoped fallback.

Evidence:
  --json PATH            JSON result path (default apexload-result.json)
  --junit PATH           JUnit XML path (default apexload-results.xml)
  --github-output PATH   Append stable scalar outputs for a composite action
  --github-summary PATH  Append a Markdown job summary`)
}
