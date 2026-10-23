package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/cmd/apexctl/internal/ciclient"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	maxCIScriptBytes              = 4 << 20
	maxCIVariablesFileBytes       = 3 << 20
	maxCIVariableEntries          = 128
	maxCIVariableKeyBytes         = 128
	maxCIVariableValueBytes       = 16 << 10
	maxCIRunRequestBytes          = 4 << 20
	ciOutcomePassed               = "passed"
	ciOutcomeSLABreached          = "sla_breached"
	ciOutcomeExecutionError       = "execution_error"
	ciOutcomeConfigurationError   = "configuration_error"
	ciOutcomeTimedOut             = "timed_out"
	ciOutcomeCancelled            = "cancelled"
	ciOutcomeInconclusive         = "inconclusive"
	ciOutcomeMonitoringIncomplete = "monitoring_incomplete"
	// ciOutcomeRunInvalid is a run whose validity the project's CI validity
	// gate fails: the measurement cannot judge the system under test.
	ciOutcomeRunInvalid = "run_invalid"
	// ciOutcomeTelemetryFailed is an otherwise passed run whose telemetry
	// failed a fail_ci acceptance policy (or was inconclusive and mapped to
	// fail); ciOutcomeTelemetryInconclusive is one whose inconclusive verdict
	// had no mapping.
	ciOutcomeTelemetryFailed       = "telemetry_failed"
	ciOutcomeTelemetryInconclusive = "telemetry_inconclusive"
)

var (
	// The active-controller EndpointSlice can legitimately be empty for the
	// 30-second leadership lease during failover. Keep controller operations
	// retrying beyond that interval, while bounding both attempts and elapsed
	// retry time so a permanently unavailable controller still fails.
	ciControllerRequestAttempts = 16
	ciControllerRetryBaseDelay  = 250 * time.Millisecond
	ciControllerRetryMaxDelay   = 5 * time.Second
	ciControllerRetryWindow     = 40 * time.Second
	ciAbortConfirmationTimeout  = 45 * time.Second
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
	monitorBindingsFile      string
	monitorRulesFile         string
	variablesFile            string
	p95LatencyMs             *float64
	maxErrorPct              *float64
	maxAvgServerProcessingMs *float64
	maxAvgTransferMs         *float64
	maxErrorCount            *int
	requireSLA               bool
	requireResults           bool
	timeout                  time.Duration
	waitAnalysis             time.Duration
	abortOnCancel            bool
	publishCheck             bool
	jsonPath                 string
	junitPath                string
	githubOutput             string
	githubSummary            string
	idempotencyKey           string
	telemetryInconclusive    string
	telemetryPolicy          string
	telemetryBudgetPolicy    string
}

type optionalFloat struct{ value **float64 }

func (o optionalFloat) String() string { return "" }
func (o optionalFloat) Set(raw string) error {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return err
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return fmt.Errorf("must be a finite number")
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
	fs.StringVar(&args.monitorBindingsFile, "monitor-bindings-file", "", "controller monitoring bindings JSON array (empty disables capture)")
	fs.StringVar(&args.monitorRulesFile, "monitor-rules-file", "", "terminal monitoring rules JSON array")
	fs.StringVar(&args.variablesFile, "variables-file", "", "non-secret JSON variables")
	fs.Var(optionalFloat{value: &args.p95LatencyMs}, "p95-latency-ms", "maximum P95 latency")
	fs.Var(optionalFloat{value: &args.maxErrorPct}, "max-error-pct", "maximum error percentage")
	fs.Var(optionalFloat{value: &args.maxAvgServerProcessingMs}, "max-avg-server-processing-ms", "maximum server processing time")
	fs.Var(optionalFloat{value: &args.maxAvgTransferMs}, "max-avg-transfer-ms", "maximum transfer time")
	fs.Var(optionalInt{value: &args.maxErrorCount}, "max-error-count", "maximum error count")
	fs.BoolVar(&args.requireSLA, "require-sla", args.requireSLA, "require an evaluated SLA")
	fs.BoolVar(&args.requireResults, "require-results", args.requireResults, "require transactions")
	fs.Var(durationValue{value: &args.timeout}, "timeout", "CI run timeout")
	fs.Var(durationValue{value: &args.waitAnalysis}, "wait-analysis", "after the run ends, wait up to this long for its root-cause analysis (0 does not wait; at most 10m)")
	fs.BoolVar(&args.abortOnCancel, "abort-on-cancel", args.abortOnCancel, "abort a remote run after cancellation")
	fs.BoolVar(&args.publishCheck, "publish-check", args.publishCheck, "publish a controller-owned GitHub Check")
	fs.StringVar(&args.jsonPath, "json", args.jsonPath, "JSON evidence path")
	fs.StringVar(&args.junitPath, "junit", args.junitPath, "JUnit evidence path")
	fs.StringVar(&args.githubOutput, "github-output", "", "GitHub output file")
	fs.StringVar(&args.githubSummary, "github-summary", "", "GitHub job summary file")
	fs.StringVar(&args.idempotencyKey, "idempotency-key", "", "explicit idempotency key")
	fs.StringVar(&args.telemetryInconclusive, "telemetry-inconclusive", "", "map an inconclusive fail_ci telemetry verdict: pass or fail (no default)")
	fs.StringVar(&args.telemetryPolicy, "telemetry-policy", "", "judge the run by this project telemetry acceptance policy (its ID) instead of the project's default")
	fs.StringVar(&args.telemetryBudgetPolicy, "telemetry-budget-policy", "", "set the run's live telemetry budgets from this project telemetry budget policy (its ID) instead of the system threshold settings")
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
	args.telemetryInconclusive = strings.ToLower(strings.TrimSpace(args.telemetryInconclusive))
	// project_default is the project's default, which omitting the flag
	// already selects.
	if args.telemetryPolicy = strings.TrimSpace(args.telemetryPolicy); args.telemetryPolicy == "project_default" {
		args.telemetryPolicy = ""
	}
	if args.telemetryBudgetPolicy = strings.TrimSpace(args.telemetryBudgetPolicy); args.telemetryBudgetPolicy == "project_default" {
		args.telemetryBudgetPolicy = ""
	}
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
	if args.waitAnalysis < 0 || args.waitAnalysis > maxCIWaitAnalysis {
		return fmt.Errorf("--wait-analysis must be between 0 and %s", maxCIWaitAnalysis)
	}
	if err := validateCISLAThresholds(args); err != nil {
		return err
	}
	if strings.TrimSpace(args.jsonPath) == "" || strings.TrimSpace(args.junitPath) == "" {
		return fmt.Errorf("--json and --junit evidence paths are required")
	}
	if err := validateDistinctCIOutputPaths(args); err != nil {
		return err
	}
	switch args.telemetryInconclusive {
	case "", "pass", "fail":
	default:
		return fmt.Errorf("--telemetry-inconclusive must be pass or fail")
	}
	if args.executionMode == "local" {
		if args.telemetryInconclusive != "" {
			return fmt.Errorf("--telemetry-inconclusive requires controller mode")
		}
		if args.telemetryPolicy != "" {
			return fmt.Errorf("--telemetry-policy requires controller mode")
		}
		if args.telemetryBudgetPolicy != "" {
			return fmt.Errorf("--telemetry-budget-policy requires controller mode")
		}
		if args.monitorBindingsFile != "" || args.monitorRulesFile != "" {
			return fmt.Errorf("monitoring configuration requires controller mode")
		}
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

func validateDistinctCIOutputPaths(args ciArgs) error {
	paths := []struct {
		name string
		path string
	}{
		{name: "--json", path: args.jsonPath},
		{name: "--junit", path: args.junitPath},
		{name: "--github-output", path: args.githubOutput},
		{name: "--github-summary", path: args.githubSummary},
	}
	for i := range paths {
		if strings.TrimSpace(paths[i].path) == "" {
			continue
		}
		first, err := canonicalCIOutputPath(paths[i].path)
		if err != nil {
			return fmt.Errorf("resolve %s path: %w", paths[i].name, err)
		}
		for j := 0; j < i; j++ {
			if strings.TrimSpace(paths[j].path) == "" {
				continue
			}
			second, err := canonicalCIOutputPath(paths[j].path)
			if err != nil {
				return fmt.Errorf("resolve %s path: %w", paths[j].name, err)
			}
			same := first == second
			if runtime.GOOS == "windows" {
				same = strings.EqualFold(first, second)
			}
			if !same {
				firstInfo, firstErr := os.Stat(first)
				secondInfo, secondErr := os.Stat(second)
				same = firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
			}
			if same {
				return fmt.Errorf("%s and %s must use distinct files", paths[j].name, paths[i].name)
			}
		}
	}
	return nil
}

func canonicalCIOutputPath(path string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	current := absolute
	suffix := make([]string, 0, 4)
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func validateCISLAThresholds(args ciArgs) error {
	return validateSLAThresholdValues(
		args.p95LatencyMs,
		args.maxErrorPct,
		args.maxAvgServerProcessingMs,
		args.maxAvgTransferMs,
		args.maxErrorCount,
	)
}

func validateSLAThresholdValues(
	p95LatencyMs *float64,
	maxErrorPct *float64,
	maxAvgServerProcessingMs *float64,
	maxAvgTransferMs *float64,
	maxErrorCount *int,
) error {
	if p95LatencyMs != nil && (math.IsNaN(*p95LatencyMs) || math.IsInf(*p95LatencyMs, 0) || *p95LatencyMs <= 0) {
		return fmt.Errorf("--p95-latency-ms must be greater than zero")
	}
	if maxErrorPct != nil && (math.IsNaN(*maxErrorPct) || math.IsInf(*maxErrorPct, 0) || *maxErrorPct < 0 || *maxErrorPct > 100) {
		return fmt.Errorf("--max-error-pct must be between 0 and 100")
	}
	if maxAvgServerProcessingMs != nil && (math.IsNaN(*maxAvgServerProcessingMs) || math.IsInf(*maxAvgServerProcessingMs, 0) || *maxAvgServerProcessingMs <= 0) {
		return fmt.Errorf("--max-avg-server-processing-ms must be greater than zero")
	}
	if maxAvgTransferMs != nil && (math.IsNaN(*maxAvgTransferMs) || math.IsInf(*maxAvgTransferMs, 0) || *maxAvgTransferMs <= 0) {
		return fmt.Errorf("--max-avg-transfer-ms must be greater than zero")
	}
	if maxErrorCount != nil && *maxErrorCount < 0 {
		return fmt.Errorf("--max-error-count must be non-negative")
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
	MonitoringStatus   string                       `json:"monitoring_status,omitempty"`
	MonitorRuleResults []analysis.MonitorRuleResult `json:"monitor_rule_results,omitempty"`
	SchemaVersion      int                          `json:"schema_version"`
	Outcome            string                       `json:"outcome"`
	TestID             string                       `json:"test_id,omitempty"`
	Status             string                       `json:"status"`
	SLAEvaluated       bool                         `json:"sla_evaluated"`
	SLABreached        bool                         `json:"sla_breached"`
	SLADetails         []string                     `json:"sla_details,omitempty"`
	Transactions       int64                        `json:"transactions"`
	Errors             int64                        `json:"errors"`
	TPS                float64                      `json:"tps"`
	DurationMS         int64                        `json:"duration_ms"`
	ReportURL          string                       `json:"report_url,omitempty"`
	CheckURL           string                       `json:"check_url,omitempty"`
	Message            string                       `json:"message,omitempty"`

	// Validity is the controller's run validity verdict and the project's
	// CI validity gate.
	Validity *CIRunValidity `json:"validity,omitempty"`

	// RootCause is the run's root-cause analysis in brief, for projects that
	// present root cause; it never changes the outcome.
	RootCause *CIRunRootCause `json:"root_cause,omitempty"`

	// TelemetryVerdict is the controller's telemetry acceptance verdict,
	// separate from the application outcome.
	TelemetryVerdict *CITelemetryVerdict `json:"telemetry_verdict,omitempty"`
	// TelemetryBudgetPolicyID is the telemetry budget policy the run chose to
	// set its live budgets; empty for the system threshold settings.
	TelemetryBudgetPolicyID string `json:"telemetry_budget_policy_id,omitempty"`
}

func ciCmd() {
	if len(os.Args) < 3 || os.Args[2] == "help" || os.Args[2] == "--help" || os.Args[2] == "-h" {
		printCIUsage()
		return
	}
	if os.Args[2] != "run" {
		fmt.Fprintf(os.Stderr, "unknown ci command: %s\n", os.Args[2])
		printCIUsage()
		os.Exit(exitCodeUsageOrConfiguration)
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
		os.Exit(exitCodeUsageOrConfiguration)
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
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "CI run failed: %v\n", runErr)
	}
	os.Exit(ciResultExitCode(result))
}

func executeCI(ctx context.Context, args ciArgs) (CIResult, error) {
	variables, err := loadTrustedCIVariables(ctx, args.variablesFile)
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, err
	}
	if args.executionMode == "local" {
		return executeLocalCI(ctx, args, variables)
	}
	return executeControllerCI(ctx, args, variables)
}

func executeLocalCI(ctx context.Context, args ciArgs, variables map[string]string) (CIResult, error) {
	if err := validateLocalCIGitHubTrust(); err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "UNTRUSTED_SOURCE"}, err
	}
	rawScript, resolvedScriptPath, err := loadTrustedCIScriptSnapshot(ctx, args.scriptPath)
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "UNTRUSTED_SOURCE"}, err
	}
	if err := validateGitHubLocalCIAssetIsolation(rawScript); err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "UNTRUSTED_SOURCE"}, err
	}
	run := runArgs{
		script: resolvedScriptPath, baseURL: args.baseURL, env: variables,
		vusers: args.vusers, duration: args.duration, rampUp: args.rampUp,
		p95LatencyMs: args.p95LatencyMs, maxErrorPct: args.maxErrorPct,
		maxAvgServerProcessingMs: args.maxAvgServerProcessingMs,
		maxAvgTransferMs:         args.maxAvgTransferMs, maxErrorCount: args.maxErrorCount,
	}
	script, err := loadScriptSnapshotForRun(resolvedScriptPath, rawScript, run)
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, fmt.Errorf("prepare script: %w", err)
	}
	localGoals := resolveGoalConfig(script, run)
	if args.requireSLA && (localGoals == nil ||
		(localGoals.P95LatencyMs == nil &&
			localGoals.MaxErrorPct == nil &&
			localGoals.MaxAvgServerProcessingMs == nil &&
			localGoals.MaxAvgTransferMs == nil &&
			localGoals.MaxErrorCount == nil)) {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "MISSING_SLA"}, ciConfigurationErrorf("no SLA threshold was configured")
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
		sla := evaluateLocalSLA(runResult, localGoals)
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

func validateLocalCIGitHubTrust() error {
	return validateCIGitHubTrust("local")
}

func validateControllerCIGitHubTrust() error {
	return validateCIGitHubTrust("controller-backed")
}

func validateCIGitHubTrust(mode string) error {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")), "true") {
		return nil
	}
	eventName := strings.ToLower(strings.TrimSpace(os.Getenv("GITHUB_EVENT_NAME")))
	if eventName == "pull_request_target" {
		return ciConfigurationErrorf("%s CI execution is prohibited for pull_request_target", mode)
	}
	if eventName != "pull_request" {
		return nil
	}
	source := sourceFromEnvironment()
	baseName := strings.TrimSpace(source.Repository)
	headName := strings.TrimSpace(source.HeadRepository)
	baseID := strings.TrimSpace(source.RepositoryID)
	headID := strings.TrimSpace(source.HeadRepositoryID)
	if baseName == "" || headName == "" || baseID == "" || headID == "" {
		return ciConfigurationErrorf("%s CI execution cannot verify the pull-request repository identity from the trusted action context", mode)
	}
	if !strings.EqualFold(baseName, headName) || baseID != headID {
		return ciConfigurationErrorf("%s CI execution is prohibited for fork pull requests", mode)
	}
	return nil
}

func executeControllerCI(ctx context.Context, args ciArgs, variables map[string]string) (CIResult, error) {
	if err := validateControllerCIGitHubTrust(); err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "UNTRUSTED_SOURCE"}, err
	}
	var rawScript []byte
	if args.scriptPath != "" {
		var err error
		rawScript, _, err = loadTrustedCIScriptSnapshot(ctx, args.scriptPath)
		if err != nil {
			return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "UNTRUSTED_SOURCE"}, err
		}
		var object map[string]any
		if err := json.Unmarshal(rawScript, &object); err != nil || object == nil {
			return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, fmt.Errorf("script must be a JSON object")
		}
	}
	bindings, monitorRules, err := loadCIMonitorConfiguration(ctx, args.monitorBindingsFile, args.monitorRulesFile)
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, err
	}
	request := CIRunRequest{
		MonitorBindings: bindings, ProjectID: args.projectID, Name: args.testName, ReleaseID: args.releaseID,
		ScriptPath: args.scriptPath, ScriptID: args.scriptID,
		Execution: CIExecution{VUsers: args.vusers, Duration: args.duration.String(), RampUp: args.rampUp.String(), BaseURL: args.baseURL, Variables: variables},
		Goals: CIGoals{
			MonitorRules: monitorRules,
			P95LatencyMs: args.p95LatencyMs, MaxErrorPct: args.maxErrorPct,
			MaxAvgServerProcessingMs: args.maxAvgServerProcessingMs,
			MaxAvgTransferMs:         args.maxAvgTransferMs, MaxErrorCount: args.maxErrorCount,
			RequireSLA: args.requireSLA, RequireResults: args.requireResults,
		},
		Source: sourceFromEnvironment(), PublishCheck: args.publishCheck, TelemetryInconclusive: args.telemetryInconclusive,
		TelemetryAcceptancePolicyID: args.telemetryPolicy,
		TelemetryBudgetPolicyID:     args.telemetryBudgetPolicy,
	}
	if len(rawScript) > 0 {
		request.Script = json.RawMessage(rawScript)
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, fmt.Errorf("encode controller CI request: %w", err)
	}
	if len(encodedRequest) > maxCIRunRequestBytes {
		return CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, ciConfigurationErrorf("controller CI request exceeds the %d byte API limit", maxCIRunRequestBytes)
	}
	idempotencyKey := args.idempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = deriveCIIdempotencyKey(request)
	}
	clientAPIKey := ""
	if args.authMode == "api-key" {
		clientAPIKey = args.apiKey
	}
	client := NewApiClientWithAuth(args.controllerURL, clientAPIKey, "")
	auth := &ciAuthSession{mode: strings.ToLower(args.authMode), projectID: args.projectID}
	coordinator := newCIRequestCoordinator(auth, client)
	// A controller that predates policy selection ignores the choice and
	// judges the run by the project's default: never report that as a pass.
	// The accepted run is stopped as soon as its create response shows it,
	// instead of spending the whole load first.
	unconfirmed := func(response *CIRunResponse) error {
		var echoedAcceptance, echoedBudget string
		if response != nil {
			echoedAcceptance, echoedBudget = response.TelemetryAcceptancePolicyID, response.TelemetryBudgetPolicyID
		}
		if chosen := request.TelemetryAcceptancePolicyID; chosen != "" && echoedAcceptance != chosen {
			return ciConfigurationErrorf("the controller did not confirm --telemetry-policy %q, so the run would be judged by the project's default telemetry policy; upgrade the controller", chosen)
		}
		if chosen := request.TelemetryBudgetPolicyID; chosen != "" && echoedBudget != chosen {
			return ciConfigurationErrorf("the controller did not confirm --telemetry-budget-policy %q, so the run would be classified by the system threshold settings; upgrade the controller", chosen)
		}
		return nil
	}
	unconfirmedTestID := ""
	coordinator.Transport.Create = func(ctx context.Context) (*CIRunResponse, error) {
		response, err := client.CreateCIRun(ctx, request, idempotencyKey)
		if err == nil && unconfirmed(response) != nil {
			if response != nil {
				unconfirmedTestID = response.TestID
			}
			return nil, unconfirmed(response)
		}
		return response, err
	}
	persistEvidence := func(state ciclient.State[*CIRunResponse], err error) {
		persistCICoreEvidenceBeforeAbort(args, controllerCIResult(state, err))
	}
	coordinator.BeforeAbort = persistEvidence
	coordinator.OnObservationFailure = persistEvidence
	state, err := coordinator.Run(ctx, deterministicCITestID(args.projectID, idempotencyKey), args.abortOnCancel)
	result := controllerCIResult(state, err)
	if unconfirmedTestID != "" {
		result.TestID = unconfirmedTestID
		if abortErr := abortControllerCIRun(context.WithoutCancel(ctx), auth, client, unconfirmedTestID, ""); abortErr != nil {
			err = errors.Join(err, fmt.Errorf("controller may still be running the unconfirmed CI load: %w", abortErr))
		}
	}
	if err != nil {
		return result, err
	}
	if unconfirmedErr := unconfirmed(state.Response); unconfirmedErr != nil {
		result.Outcome = ciOutcomeConfigurationError
		return result, unconfirmedErr
	}
	classifyCIResult(&result)
	if checkErr := enforceRequiredCICheck(args, state.Response, &result); checkErr != nil {
		return result, checkErr
	}
	if args.waitAnalysis > 0 {
		result.RootCause = waitForCIRootCause(ctx, auth, client, state.Response, args.waitAnalysis)
	}
	return result, nil
}

// Result and exit policy stay in the command adapter; the coordinator returns
// the last complete response plus the boundary that ended observation.
func controllerCIResult(state ciclient.State[*CIRunResponse], err error) CIResult {
	result := CIResult{SchemaVersion: ciSchemaVersion, TestID: state.TestID}
	if state.Observed {
		result = ciResultFromResponse(state.Response)
	}
	if state.Cancellation != nil {
		result.Outcome, result.Status = ciOutcomeCancelled, "CANCELLED"
		if errors.Is(state.Cancellation, context.DeadlineExceeded) {
			result.Outcome, result.Status = ciOutcomeTimedOut, "TIMED_OUT"
		}
	} else if err != nil {
		result.Outcome = ciFailureOutcome(err)
		result.Status = "CREATE_FAILED"
		if state.Observed {
			result.Status = "STATUS_FAILED"
		} else {
			var authFailure *ciControllerAuthFailure
			if errors.As(err, &authFailure) {
				result.Status = "AUTH_FAILED"
			}
		}
	}
	return result
}

type ciFileProvenance struct {
	path        string
	repository  string
	gitPath     string
	expectedOID string
}

func loadTrustedCIScriptSnapshot(ctx context.Context, path string) ([]byte, string, error) {
	return loadTrustedCIFileSnapshot(ctx, path, maxCIScriptBytes, "script-path")
}

func loadTrustedCIFileSnapshot(ctx context.Context, path string, maxBytes int64, label string) ([]byte, string, error) {
	provenance, err := prepareCIFileProvenance(ctx, path, label)
	if err != nil {
		return nil, "", err
	}
	if provenance.repository == "" {
		raw, err := readCIFileLimited(provenance.path, maxBytes)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", label, err)
		}
		return raw, provenance.path, nil
	}
	sizeRaw, err := runCIGit(ctx, provenance.repository, nil, "cat-file", "-s", provenance.expectedOID)
	if err != nil {
		return nil, "", ciConfigurationErrorf("cannot inspect the GitHub %s snapshot at GITHUB_SHA: %v", label, err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeRaw)), 10, 64)
	if err != nil || size < 0 || size > maxBytes {
		return nil, "", ciConfigurationErrorf("%s at GITHUB_SHA exceeds the %d byte CI limit", label, maxBytes)
	}
	raw, err := runCIGit(ctx, provenance.repository, nil, "cat-file", "blob", provenance.expectedOID)
	if err != nil {
		return nil, "", ciConfigurationErrorf("cannot read the GitHub %s snapshot at GITHUB_SHA: %v", label, err)
	}
	if int64(len(raw)) != size {
		return nil, "", ciConfigurationErrorf("Git returned an incomplete %s snapshot at GITHUB_SHA", label)
	}
	return raw, provenance.path, nil
}

func readCIFileLimited(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("invalid read limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("script-path must be a regular file")
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("script exceeds the %d byte CI limit", maxBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("script exceeds the %d byte CI limit", maxBytes)
	}
	return raw, nil
}

// GitHub local mode is intentionally a deterministic smoke-test fallback. The
// JSON script itself is loaded from GITHUB_SHA, but ParseFile normally opens
// body files and other local assets from the mutable runner workspace. Reject
// those file-backed inputs before parsing so an alternate checkout, untracked
// file, or surviving process on a self-hosted runner cannot change execution
// after the commit identity has been authenticated. Callers can inline smoke
// data or use controller mode for governed project assets and secrets.
func validateGitHubLocalCIAssetIsolation(raw []byte) error {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")), "true") {
		return nil
	}
	script, err := decodeCIScriptDefinition(raw)
	if err != nil {
		return ciConfigurationErrorf("decode script for GitHub local CI asset validation: %v", err)
	}
	bodyFile, browserInputFile := inspectCILocalActions(script)
	if bodyFile {
		return ciConfigurationErrorf("GitHub local CI requires inline action bodies; body_file is not allowed")
	}
	if browserInputFile {
		return ciConfigurationErrorf("GitHub local CI cannot execute browser_set_files from runner-local paths")
	}
	for _, source := range script.DataSources {
		if strings.TrimSpace(source.Path) != "" || strings.TrimSpace(source.StorageRef) != "" || strings.TrimSpace(source.StagedAsset) != "" {
			return ciConfigurationErrorf("GitHub local CI requires data_sources to use inline_data; runner-local and staged assets are not allowed")
		}
	}
	if len(script.Config.GRPC.ProtoFiles) != 0 {
		return ciConfigurationErrorf("GitHub local CI cannot load gRPC descriptor files from runner-local paths")
	}
	if hasCITLSFilePaths(script.Config.HTTP.TLS) {
		return ciConfigurationErrorf("GitHub local CI cannot load HTTP TLS material from runner-local paths")
	}
	if script.Config.Kafka.TLS != nil && hasCITLSFilePaths(dsl.TLSConfig{
		ClientCertFile: script.Config.Kafka.TLS.ClientCertFile,
		ClientKeyFile:  script.Config.Kafka.TLS.ClientKeyFile,
		CACertFile:     script.Config.Kafka.TLS.CACertFile,
	}) {
		return ciConfigurationErrorf("GitHub local CI cannot load Kafka TLS material from runner-local paths")
	}
	if script.Config.JMS.TLS != nil {
		caMaterial := strings.TrimSpace(script.Config.JMS.TLS.CACertSecret)
		if caMaterial != "" && !strings.Contains(caMaterial, "BEGIN CERTIFICATE") && !strings.Contains(caMaterial, "{{secret.") {
			return ciConfigurationErrorf("GitHub local CI cannot load JMS TLS material from runner-local paths")
		}
	}
	for _, certificate := range script.Config.Browser.ClientCertificates {
		if strings.TrimSpace(certificate.CertPath) != "" || strings.TrimSpace(certificate.KeyPath) != "" || strings.TrimSpace(certificate.PfxPath) != "" {
			return ciConfigurationErrorf("GitHub local CI cannot load browser client certificates from runner-local paths")
		}
	}
	return nil
}

func decodeCIScriptDefinition(raw []byte) (*dsl.Script, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	payload := raw
	if wrapped, ok := root["script"]; ok && len(wrapped) != 0 && string(wrapped) != "null" {
		payload = wrapped
	}
	var script dsl.Script
	if err := json.Unmarshal(payload, &script); err != nil {
		return nil, err
	}
	return &script, nil
}

func inspectCILocalActions(script *dsl.Script) (bodyFile, browserInputFile bool) {
	if script == nil {
		return false, false
	}
	pending := make([]dsl.Action, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	pending = append(pending, script.Init...)
	pending = append(pending, script.Actions...)
	pending = append(pending, script.Teardown...)
	for len(pending) != 0 {
		index := len(pending) - 1
		action := pending[index]
		pending = pending[:index]
		if strings.TrimSpace(action.BodyFile) != "" {
			bodyFile = true
		}
		if strings.EqualFold(strings.TrimSpace(action.Type), "browser_set_files") {
			browserInputFile = true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				pending = append(pending, branch.Actions...)
			}
		}
		if requests, ok := action.Params["requests"].([]any); ok {
			for _, value := range requests {
				child, ok := value.(map[string]any)
				if !ok {
					continue
				}
				if raw, ok := child["body_file"].(string); ok && strings.TrimSpace(raw) != "" {
					bodyFile = true
				}
				if raw, ok := child["type"].(string); ok && strings.EqualFold(strings.TrimSpace(raw), "browser_set_files") {
					browserInputFile = true
				}
			}
		}
	}
	return bodyFile, browserInputFile
}

func hasCITLSFilePaths(config dsl.TLSConfig) bool {
	return strings.TrimSpace(config.ClientCertFile) != "" ||
		strings.TrimSpace(config.ClientKeyFile) != "" ||
		strings.TrimSpace(config.CACertFile) != ""
}

func prepareCIFileProvenance(ctx context.Context, path, label string) (ciFileProvenance, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "CI file"
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ciFileProvenance{}, ciConfigurationErrorf("%s is required", label)
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("resolve %s: %v", label, err)
	}
	absolutePath = filepath.Clean(absolutePath)
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")), "true") {
		return ciFileProvenance{path: absolutePath}, nil
	}

	workspace := strings.TrimSpace(os.Getenv("GITHUB_WORKSPACE"))
	claimedSHA := strings.TrimSpace(os.Getenv("GITHUB_SHA"))
	if workspace == "" || !validCIGitObjectID(claimedSHA) {
		return ciFileProvenance{}, ciConfigurationErrorf("GitHub %s provenance requires GITHUB_WORKSPACE and a valid GITHUB_SHA", label)
	}
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("resolve GITHUB_WORKSPACE: %v", err)
	}
	workspaceAbs = filepath.Clean(workspaceAbs)
	lexicalRelative, err := relativePathWithin(workspaceAbs, absolutePath)
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("%s must remain inside GITHUB_WORKSPACE", label)
	}
	if err := rejectCISymlinkComponents(workspaceAbs, lexicalRelative, label); err != nil {
		return ciFileProvenance{}, err
	}
	info, err := os.Lstat(absolutePath)
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("inspect %s: %v", label, err)
	}
	if !info.Mode().IsRegular() {
		return ciFileProvenance{}, ciConfigurationErrorf("%s must be a regular non-symlink file", label)
	}

	workspaceReal, err := filepath.EvalSymlinks(workspaceAbs)
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("resolve GITHUB_WORKSPACE symlinks: %v", err)
	}
	fileReal, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("resolve %s symlinks: %v", label, err)
	}
	workspaceReal = filepath.Clean(workspaceReal)
	fileReal = filepath.Clean(fileReal)
	if _, err := relativePathWithin(workspaceReal, fileReal); err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("%s resolves outside GITHUB_WORKSPACE", label)
	}

	rootRaw, err := runCIGit(ctx, filepath.Dir(fileReal), nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("%s is not inside a usable Git checkout: %v", label, err)
	}
	repositoryRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(rootRaw)))
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("resolve Git repository root: %v", err)
	}
	repositoryRoot = filepath.Clean(repositoryRoot)
	if _, err := relativePathWithin(workspaceReal, repositoryRoot); err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("the Git repository containing %s must remain inside GITHUB_WORKSPACE", label)
	}
	headRaw, err := runCIGit(ctx, repositoryRoot, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("resolve checked-out Git commit: %v", err)
	}
	if !strings.EqualFold(strings.TrimSpace(string(headRaw)), claimedSHA) {
		return ciFileProvenance{}, ciConfigurationErrorf("checked-out Git HEAD does not match GITHUB_SHA")
	}

	relative, err := relativePathWithin(repositoryRoot, fileReal)
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("%s is outside the checked-out repository", label)
	}
	gitPath := filepath.ToSlash(relative)
	treeRaw, err := runCIGit(ctx, repositoryRoot, nil, "ls-tree", "-z", "--full-tree", claimedSHA, "--", gitPath)
	if err != nil {
		return ciFileProvenance{}, ciConfigurationErrorf("resolve %s at GITHUB_SHA: %v", label, err)
	}
	mode, objectType, expectedOID, recordedPath, ok := parseCIGitTreeRecord(treeRaw)
	if !ok || (mode != "100644" && mode != "100755") || objectType != "blob" || recordedPath != gitPath || !validCIGitObjectID(expectedOID) {
		return ciFileProvenance{}, ciConfigurationErrorf("%s is not a tracked regular file at GITHUB_SHA", label)
	}
	return ciFileProvenance{
		path:        fileReal,
		repository:  repositoryRoot,
		gitPath:     gitPath,
		expectedOID: expectedOID,
	}, nil
}

func relativePathWithin(root, candidate string) (string, error) {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("path is outside root")
	}
	return relative, nil
}

func rejectCISymlinkComponents(root, relative, label string) error {
	current := filepath.Clean(root)
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return ciConfigurationErrorf("inspect %s component: %v", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ciConfigurationErrorf("%s must not traverse symbolic links", label)
		}
	}
	return nil
}

func runCIGit(ctx context.Context, repository string, stdin []byte, arguments ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", repository}, arguments...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = make([]string, 0, len(os.Environ())+2)
	for _, environment := range os.Environ() {
		name, _, _ := strings.Cut(environment, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		command.Env = append(command.Env, environment)
	}
	command.Env = append(command.Env, "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0")
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

func sameCIFile(first, second string) (bool, error) {
	firstInfo, err := os.Stat(first)
	if err != nil {
		return false, err
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		return false, err
	}
	return os.SameFile(firstInfo, secondInfo), nil
}

func parseCIGitTreeRecord(raw []byte) (mode, objectType, objectID, path string, ok bool) {
	if len(raw) < 2 || raw[len(raw)-1] != 0 || bytes.IndexByte(raw[:len(raw)-1], 0) >= 0 {
		return "", "", "", "", false
	}
	record := raw[:len(raw)-1]
	header, pathBytes, found := bytes.Cut(record, []byte{'\t'})
	if !found {
		return "", "", "", "", false
	}
	fields := strings.Fields(string(header))
	if len(fields) != 3 {
		return "", "", "", "", false
	}
	return fields[0], fields[1], fields[2], string(pathBytes), true
}

func validCIGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

// Compatibility helpers adapt existing command tests and callers to an
// instance-owned coordinator. Retry knobs are copied at construction, so the
// internal package never reads command globals or environment variables.
type ciControllerAuthFailure = ciclient.AuthError

func ciRetryPolicy() ciclient.RetryPolicy {
	return ciclient.RetryPolicy{
		Attempts: ciControllerRequestAttempts, BaseDelay: ciControllerRetryBaseDelay,
		MaxDelay: ciControllerRetryMaxDelay, Window: ciControllerRetryWindow,
	}
}

func classifyCIRequestError(err error) ciclient.Failure {
	failure := ciclient.Failure{}
	var configurationFailure *ciConfigurationFailure
	failure.Permanent = errors.As(err, &configurationFailure)
	var apiErr *CIAPIError
	if errors.As(err, &apiErr) {
		failure.HTTPStatus, failure.RetryAfter = apiErr.StatusCode, apiErr.RetryAfter
	}
	return failure
}

func newCIRequestCoordinator(auth *ciAuthSession, client *ApiClient) *ciclient.Coordinator[*CIRunResponse] {
	coordinator := &ciclient.Coordinator[*CIRunResponse]{
		Retry: ciRetryPolicy(), PollInterval: ciPollInterval, AbortTimeout: ciAbortConfirmationTimeout,
		Classify: classifyCIRequestError,
		Transport: ciclient.Transport[*CIRunResponse]{
			Get: client.GetCIRun,
			Abort: func(ctx context.Context, id, reason string) (bool, error) {
				response, err := client.AbortCIRunWithReason(ctx, id, reason)
				return response != nil && isTerminalCIStatus(response.Status), err
			},
			Inspect: func(response *CIRunResponse) ciclient.Observation {
				if response == nil {
					return ciclient.Observation{}
				}
				return ciclient.Observation{TestID: response.TestID, Terminal: isTerminalCIResponse(response)}
			},
		},
	}
	if auth != nil {
		coordinator.Auth = auth.forClient(client)
	}
	return coordinator
}

func retryControllerCIRequest(ctx context.Context, auth *ciAuthSession, client *ApiClient, operation func() (*CIRunResponse, error)) (*CIRunResponse, error) {
	return newCIRequestCoordinator(auth, client).Request(ctx, operation)
}

func ciControllerRetryDelay(attempt int, err error) time.Duration {
	return ciRetryPolicy().Delay(attempt, classifyCIRequestError(err))
}

func waitForControllerCIRetry(ctx context.Context, delay time.Duration) error {
	return ciclient.Wait(ctx, delay)
}

func ciControllerRequestRetryable(err error) bool {
	return ciclient.Retryable(err, classifyCIRequestError(err))
}

func abortControllerCIRun(parent context.Context, auth *ciAuthSession, client *ApiClient, testID string, reason string) error {
	if strings.TrimSpace(testID) == "" || auth == nil || client == nil {
		return nil
	}
	return newCIRequestCoordinator(auth, client).Abort(parent, testID, reason)
}

var errCIAbortConfirmationPending = ciclient.ErrAbortConfirmationPending

func isTerminalCIStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "FAILED", "ABORTED", "TIMED_OUT", "CANCELLED":
		return true
	default:
		return false
	}
}

func ciAbortReason(err error) string { return ciclient.AbortReason(err) }

func ciAbortRetryable(err error) bool {
	return ciclient.AbortRetryable(err, classifyCIRequestError(err))
}

type ciAuthSession struct {
	mode, projectID string
	client          *ApiClient
	session         *ciclient.Session
}

func (a *ciAuthSession) forClient(client *ApiClient) *ciclient.Session {
	if a.session == nil || a.client != client {
		a.client = client
		a.session = &ciclient.Session{Mode: a.mode, ProjectID: a.projectID, Ports: ciclient.AuthPorts{
			APIKey:         func() string { return client.APIKey },
			BearerToken:    func() string { return client.BearerToken },
			SetBearerToken: func(token string) { client.BearerToken = token },
			Discover: func(ctx context.Context) (ciclient.Discovery, error) {
				discovery, err := client.DiscoverCI(ctx)
				if err != nil {
					return ciclient.Discovery{}, err
				}
				return ciclient.Discovery{Audience: discovery.OIDCAudience, Modes: discovery.AuthModes}, nil
			},
			RequestIDToken: requestGitHubIDToken,
			Exchange: func(ctx context.Context, token, projectID string) (ciclient.Token, error) {
				exchanged, err := client.ExchangeGitHubOIDC(ctx, CIExchangeRequest{IDToken: token, ProjectID: projectID})
				if err != nil {
					return ciclient.Token{}, err
				}
				return ciclient.Token{Value: exchanged.AccessToken, ExpiresAt: exchanged.ExpiresAt, ExpiresIn: exchanged.ExpiresIn}, nil
			},
			ConfigurationError: func(message string) error { return ciConfigurationErrorf("%s", message) },
		}}
	}
	return a.session
}

func (a *ciAuthSession) ensure(ctx context.Context, client *ApiClient) error {
	return a.forClient(client).Ensure(ctx)
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
	if err := decodeBoundedSingleJSON(resp.Body, 1<<20, &out); err != nil || strings.TrimSpace(out.Value) == "" {
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
	raw, err := readCIFileLimited(path, maxCIVariablesFileBytes)
	if err != nil {
		return nil, fmt.Errorf("read variables file: %w", err)
	}
	return decodeCIVariables(raw)
}

func loadTrustedCIVariables(ctx context.Context, path string) (map[string]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	raw, _, err := loadTrustedCIFileSnapshot(ctx, path, maxCIVariablesFileBytes, "variables-file")
	if err != nil {
		return nil, err
	}
	return decodeCIVariables(raw)
}

func decodeCIVariables(raw []byte) (map[string]string, error) {
	var variables map[string]string
	if err := json.Unmarshal(raw, &variables); err != nil {
		return nil, fmt.Errorf("variables file must be a JSON object of string values: %w", err)
	}
	if variables == nil {
		return nil, fmt.Errorf("variables file must be a JSON object of string values")
	}
	if len(variables) > maxCIVariableEntries {
		return nil, fmt.Errorf("variables file exceeds %d entries", maxCIVariableEntries)
	}
	for key, value := range variables {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" || len(trimmed) > maxCIVariableKeyBytes || strings.ContainsAny(trimmed, "\r\n=\x00") {
			return nil, fmt.Errorf("variables file contains an invalid key")
		}
		if len(value) > maxCIVariableValueBytes || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("variables file entry %q is invalid or exceeds %d bytes", key, maxCIVariableValueBytes)
		}
		if sensitiveCIVariableKey(key) || sensitiveCIVariableValue(value) {
			return nil, fmt.Errorf("variables file entry %q looks sensitive; use a controller-managed {{secret.*}} reference", key)
		}
	}
	return variables, nil
}

func sensitiveCIVariableKey(name string) bool {
	if scrub.SensitiveName(name) {
		return true
	}
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(name)))
	for _, marker := range []string{"database_url", "db_url", "jdbc_url", "redis_url", "cache_url", "broker_url", "amqp_url", "dsn", "connection_string"} {
		if normalized == marker || strings.HasSuffix(normalized, "_"+marker) {
			return true
		}
	}
	return false
}

func sensitiveCIVariableValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") || strings.HasPrefix(lower, "apikey ") ||
		strings.Contains(lower, "sharedaccesskey=") || strings.Contains(lower, "accountkey=") || strings.Contains(lower, "sig=") {
		return true
	}
	if (strings.HasPrefix(trimmed, "AKIA") || strings.HasPrefix(trimmed, "ASIA")) && len(trimmed) >= 16 {
		return true
	}
	if scrub.Value(trimmed, scrub.Policy{}) != trimmed {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	if parsed.User != nil {
		return true
	}
	for key, values := range parsed.Query() {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if !scrub.SensitiveName(key) && normalized != "sig" && !strings.HasSuffix(normalized, "signature") {
			continue
		}
		for _, candidate := range values {
			if strings.TrimSpace(candidate) != "" {
				return true
			}
		}
	}
	return false
}

func sourceFromEnvironment() CISource {
	source := CISource{
		Provider: "github", ServerURL: os.Getenv("GITHUB_SERVER_URL"), APIURL: os.Getenv("GITHUB_API_URL"),
		Repository: os.Getenv("GITHUB_REPOSITORY"), RepositoryID: os.Getenv("GITHUB_REPOSITORY_ID"),
		HeadRepository: os.Getenv("APEXLOAD_GITHUB_HEAD_REPOSITORY"), HeadRepositoryID: os.Getenv("APEXLOAD_GITHUB_HEAD_REPOSITORY_ID"),
		Ref: os.Getenv("GITHUB_REF"), SHA: os.Getenv("GITHUB_SHA"), WorkflowRef: os.Getenv("GITHUB_WORKFLOW_REF"),
		JobWorkflowRef: os.Getenv("GITHUB_JOB_WORKFLOW_REF"),
		RunID:          os.Getenv("GITHUB_RUN_ID"), RunAttempt: os.Getenv("GITHUB_RUN_ATTEMPT"), Actor: os.Getenv("GITHUB_ACTOR"),
		EventName: os.Getenv("GITHUB_EVENT_NAME"), Environment: os.Getenv("GITHUB_ENVIRONMENT"), RunnerEnvironment: os.Getenv("RUNNER_ENVIRONMENT"),
		HeadRef: os.Getenv("GITHUB_HEAD_REF"), HeadSHA: os.Getenv("APEXLOAD_GITHUB_HEAD_SHA"),
	}
	if !strings.EqualFold(os.Getenv("GITHUB_ACTIONS"), "true") {
		source.Provider = "local"
		populatePullRequestSource(&source, os.Getenv("GITHUB_EVENT_PATH"))
	}
	return source
}

func populatePullRequestSource(source *CISource, eventPath string) {
	if source == nil || strings.TrimSpace(eventPath) == "" {
		return
	}
	raw, err := readCIFileLimited(eventPath, 1<<20)
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
	requestIdentity, _ := json.Marshal(struct {
		Name            string                    `json:"name"`
		ReleaseID       string                    `json:"release_id"`
		Execution       CIExecution               `json:"execution"`
		Goals           CIGoals                   `json:"goals"`
		MonitorBindings *[]storage.MonitorBinding `json:"monitor_bindings,omitempty"`
		// Omitted when unset, so existing derived keys are unchanged.
		TelemetryAcceptancePolicyID string `json:"telemetry_acceptance_policy_id,omitempty"`
		TelemetryBudgetPolicyID     string `json:"telemetry_budget_policy_id,omitempty"`
	}{
		Name: request.Name, ReleaseID: request.ReleaseID, Execution: request.Execution, Goals: request.Goals, MonitorBindings: request.MonitorBindings,
		TelemetryAcceptancePolicyID: request.TelemetryAcceptancePolicyID,
		TelemetryBudgetPolicyID:     request.TelemetryBudgetPolicyID,
	})
	raw := strings.Join([]string{
		request.Source.ServerURL, request.Source.RepositoryID, request.Source.Repository,
		request.Source.WorkflowRef, request.Source.RunID, request.Source.RunAttempt, scriptIdentity, string(requestIdentity),
	}, "\x00")
	hash := sha256.Sum256([]byte(raw))
	return "apexload-ci-v1-" + hex.EncodeToString(hash[:])
}

func deterministicCITestID(projectID, idempotencyKey string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-run:"+strings.TrimSpace(projectID)+":"+strings.TrimSpace(idempotencyKey))).String()
}

// maxCIWaitAnalysis bounds --wait-analysis: an analysis runs inside the
// engine's two-minute budget once a worker claims it, so ten minutes covers
// a queue several analyses deep.
const maxCIWaitAnalysis = 10 * time.Minute

// ciValidityVerdict is a run's validity verdict, or empty without one.
func ciValidityVerdict(validity *CIRunValidity) string {
	if validity == nil {
		return ""
	}
	return validity.Verdict
}

// waitForCIRootCause polls a finished CI run until its root-cause analysis
// settles or wait passes, and returns the newest root-cause block. The run's
// outcome is already decided; a poll that fails or the overall deadline only
// end the wait.
func waitForCIRootCause(ctx context.Context, auth *ciAuthSession, client *ApiClient, response *CIRunResponse, wait time.Duration) *CIRunRootCause {
	latest := newCIRequestCoordinator(auth, client).WaitFor(ctx, response, wait, func(current *CIRunResponse) bool {
		return current != nil && current.RootCause.pending()
	})
	if latest == nil {
		return nil
	}
	return latest.RootCause.clone()
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
		MonitoringStatus: response.MonitoringStatus, MonitorRuleResults: append([]analysis.MonitorRuleResult(nil), response.MonitorRuleResults...), SchemaVersion: ciSchemaVersion, Outcome: response.Outcome, TestID: response.TestID, Status: response.Status,
		SLAEvaluated: response.SLAEvaluated, SLABreached: response.SLABreached, SLADetails: append([]string(nil), response.SLADetails...),
		Transactions: transactions, Errors: errorsCount, TPS: tps,
		DurationMS: durationMS, ReportURL: response.ReportURL, CheckURL: response.CheckURL, Message: response.Error,
		Validity: response.Validity.clone(), TelemetryVerdict: response.TelemetryVerdict.clone(), RootCause: response.RootCause.clone(),
		TelemetryBudgetPolicyID: response.TelemetryBudgetPolicyID,
	}
}

func isTerminalCIResponse(response *CIRunResponse) bool {
	if response == nil {
		return false
	}
	if response.Outcome == "monitoring_pending" {
		return false
	}
	if containsCIString([]string{ciOutcomePassed, ciOutcomeSLABreached, ciOutcomeExecutionError, ciOutcomeConfigurationError, ciOutcomeTimedOut, ciOutcomeCancelled, ciOutcomeInconclusive, ciOutcomeMonitoringIncomplete, ciOutcomeRunInvalid, ciOutcomeTelemetryFailed, ciOutcomeTelemetryInconclusive}, response.Outcome) {
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
	if containsCIString([]string{ciOutcomeSLABreached, ciOutcomeExecutionError, ciOutcomeConfigurationError, ciOutcomeTimedOut, ciOutcomeCancelled, ciOutcomeInconclusive, ciOutcomeMonitoringIncomplete, ciOutcomeRunInvalid, ciOutcomeTelemetryFailed, ciOutcomeTelemetryInconclusive}, result.Outcome) {
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
	encoded, err := writeCICoreEvidence(args, result)
	if err != nil {
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

func persistCICoreEvidenceBeforeAbort(args ciArgs, result CIResult) {
	if result.SchemaVersion == 0 {
		result.SchemaVersion = ciSchemaVersion
	}
	if _, err := writeCICoreEvidence(args, result); err != nil {
		fmt.Fprintf(os.Stderr, "failed to persist partial CI evidence before remote abort: %v\n", err)
	}
}

func writeCICoreEvidence(args ciArgs, result CIResult) ([]byte, error) {
	if err := ensureEvidenceParent(args.jsonPath); err != nil {
		return nil, err
	}
	if err := ensureEvidenceParent(args.junitPath); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(args.jsonPath, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("write JSON evidence: %w", err)
	}
	var runErr error
	// The telemetry outcomes have their own test case: the application run
	// itself passed.
	if result.Outcome != ciOutcomePassed && result.Outcome != ciOutcomeSLABreached && result.Outcome != ciOutcomeTelemetryFailed && result.Outcome != ciOutcomeTelemetryInconclusive {
		runErr = errors.New(firstNonEmptyCLI(result.Message, result.Outcome))
	}
	sla := &SLASummary{Evaluated: result.SLAEvaluated, Breached: result.SLABreached, Details: result.SLADetails}
	if err := writeJUnitReport(args.junitPath, junitInput{
		suiteName: "apexload:ci", classname: "apexload", duration: time.Duration(result.DurationMS) * time.Millisecond,
		transactions: result.Transactions, errors: result.Errors, tps: result.TPS, runErr: runErr, sla: sla,
		telemetry: result.TelemetryVerdict,
	}); err != nil {
		return nil, err
	}
	return encoded, nil
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
		"report-url": result.ReportURL, "check-url": result.CheckURL, "telemetry-verdict": telemetryVerdictOutcome(result.TelemetryVerdict),
		"validity": ciValidityVerdict(result.Validity), "root-cause": result.RootCause.text(),
		"json-path": absoluteOutputPath(args.jsonPath), "junit-path": absoluteOutputPath(args.junitPath),
		"apexload-evidence-complete": "true",
	}
	var builder strings.Builder
	for _, key := range []string{"outcome", "test-id", "status", "sla-evaluated", "sla-breached", "transactions", "errors", "tps", "duration-ms", "report-url", "check-url", "telemetry-verdict", "validity", "root-cause", "json-path", "junit-path", "apexload-evidence-complete"} {
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
	if validity := result.Validity; validity != nil {
		text += fmt.Sprintf("| Run validity | %s |\n| Validity gate | %s |\n", markdownCell(validity.Verdict), markdownCell(firstNonEmptyCLI(validity.Gate, "advisory")))
	}
	if telemetry := result.TelemetryVerdict; telemetry != nil {
		text += fmt.Sprintf("| Telemetry verdict | %s |\n| Telemetry policy mode | %s |\n", markdownCell(telemetry.Outcome), markdownCell(telemetryVerdictModeLabel(telemetry)))
	}
	if result.TelemetryBudgetPolicyID != "" {
		text += fmt.Sprintf("| Telemetry budgets | chosen by the run (%s) |\n", markdownCell(result.TelemetryBudgetPolicyID))
	}
	if rootCause := result.RootCause.text(); rootCause != "" {
		text += fmt.Sprintf("| Root cause | %s |\n", markdownCell(rootCause))
	}
	if result.ReportURL != "" {
		reportURL := strings.ReplaceAll(strings.ReplaceAll(result.ReportURL, "\r", ""), "\n", "")
		text += "\n[Open APEX Load report](" + strings.ReplaceAll(reportURL, ")", "%29") + ")\n"
	}
	if telemetry := result.TelemetryVerdict; telemetry != nil && len(telemetry.Reasons) > 0 {
		text += "\n### Telemetry acceptance\n"
		for _, reason := range telemetry.Reasons {
			text += "\n- " + strings.ReplaceAll(strings.ReplaceAll(reason, "\r", " "), "\n", " ")
		}
		text += "\n"
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
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case ciOutcomePassed:
		return exitCodeSuccess
	case ciOutcomeSLABreached, ciOutcomeRunInvalid, ciOutcomeTelemetryFailed:
		return exitCodeTestFailure
	case ciOutcomeTelemetryInconclusive:
		// The remedy is the caller's mapping (--telemetry-inconclusive).
		return exitCodeUsageOrConfiguration
	case ciOutcomeConfigurationError:
		return exitCodeUsageOrConfiguration
	default:
		return exitCodeControllerOrTransport
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

Telemetry acceptance:
  --telemetry-policy ID  Judge the run by this project telemetry acceptance
                         policy instead of the project's default, where the
                         project's CI policy allows it. The controller must
                         confirm the choice, or the run is a configuration
                         error.
  --telemetry-budget-policy ID
                         Set the run's live telemetry budgets from this
                         project telemetry budget policy instead of the
                         system threshold settings. It needs the same CI
                         permission, and the controller must confirm it.
  --telemetry-inconclusive pass|fail
                         How an inconclusive telemetry verdict maps when the
                         run's acceptance policy is fail_ci. No default: the
                         controller refuses a fail_ci run without it. An
                         advisory policy never changes the exit code.

Evidence:
  --json PATH            JSON result path (default apexload-result.json)
  --junit PATH           JUnit XML path (default apexload-results.xml)
  --github-output PATH   Append stable scalar outputs for a composite action
  --github-summary PATH  Append a Markdown job summary`)
}

// telemetryVerdictOutcome is a verdict's outcome, or empty without one.
func telemetryVerdictOutcome(verdict *CITelemetryVerdict) string {
	if verdict == nil {
		return ""
	}
	return verdict.Outcome
}

func telemetryVerdictModeLabel(verdict *CITelemetryVerdict) string {
	label := verdict.Mode
	if verdict.Enforced {
		label += " (decided the outcome)"
	} else if verdict.Mode != "fail_ci" {
		label += " (never changes the outcome)"
	}
	if verdict.InconclusivePolicy != "" {
		label += "; inconclusive maps to " + verdict.InconclusivePolicy
	}
	if verdict.Policy != nil && verdict.Policy.Source == "run_selection" {
		label += "; policy chosen by the run"
	}
	return label
}
