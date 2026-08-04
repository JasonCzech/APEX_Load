package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
)

var apexctlCommandHandlers = map[string]func(){
	"run":          runTest,
	"ci":           ciCmd,
	"script":       scriptCmd,
	"status":       statusCmd,
	"wait":         waitCmd,
	"archive":      archiveCmd,
	"record":       recordCmd,
	"record-agent": recordAgentCmd,
	"github":       githubCmd,
	"import":       importCmd,
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	loadLocalDotEnv()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(exitCodeUsageOrConfiguration)
	}

	if handler, ok := apexctlCommandHandlers[os.Args[1]]; ok {
		handler()
		return
	}
	switch os.Args[1] {
	case "version":
		info := buildinfo.Get()
		fmt.Printf("apexctl %s (%s, %s)\n", info.DisplayVersion, firstNonEmptyCLI(info.Commit, "unknown"), firstNonEmptyCLI(info.BuildTime, "unknown"))
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

func loadLocalDotEnv() {
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ENV")))
	if appEnv == "production" || appEnv == "prod" {
		return
	}
	if err := config.LoadDotEnv(".env"); err != nil {
		if os.IsNotExist(err) {
			return
		}
		slog.Warn("failed to load local .env", "err", err)
	}
}

func runTest() {
	args := parseRunArgs(os.Args[2:])
	if args.err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", args.err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if args.help {
		printUsage()
		return
	}

	if args.script == "" {
		fmt.Fprintln(os.Stderr, "error: --script is required")
		os.Exit(exitCodeUsageOrConfiguration)
	}

	script, err := loadScriptForRun(args.script, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error preparing script: %v\n", err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	goalConfig := resolveGoalConfig(script, args)

	var result *engine.RunResult
	var apiResult *ApiTestResult
	var runErr error
	var slaSummary *SLASummary

	if args.api != "" {
		scriptBody, err := marshalScriptForUpload(script)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error preparing upload script: %v\n", err)
			os.Exit(exitCodeUsageOrConfiguration)
		}
		client := NewApiClientWithAuth(args.api, args.apiKey, args.bearerToken)
		client.PollTimeout = args.timeout
		apiResult, runErr = client.RunPreparedTest(args.script, scriptBody, args.vusers, args.duration.String(), args.rampUp.String(), args.format, goalConfig)
		if apiResult != nil {
			slaSummary = apiResult.SLASummary
		}
	} else {

		eng, err := engine.New(engine.Config{
			Script:   script,
			VUsers:   args.vusers,
			Duration: args.duration,
			RampUp:   args.rampUp,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating engine: %v\n", err)
			os.Exit(exitCodeUsageOrConfiguration)
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		if args.format != "json" {
			fmt.Printf("Starting test: %s\n", script.Name)
			fmt.Printf("  Protocol: %s\n", script.Protocol)
			fmt.Printf("  VUsers:   %d\n", args.vusers)
			fmt.Printf("  Duration: %s\n", args.duration)
			if args.rampUp > 0 {
				fmt.Printf("  Ramp-up:  %s\n", args.rampUp)
			}
			fmt.Println()
		}

		result, runErr = eng.Run(ctx)
		slaSummary = evaluateLocalSLA(result, goalConfig)
	}

	var evidenceErr error
	if args.junit != "" {
		evidenceErr = writeRunJUnit(args, script, result, apiResult, slaSummary, runErr)
		if evidenceErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", evidenceErr)
		}
	}

	if args.format == "json" {
		out := map[string]any{
			"status": testRunOutcome(args.api != "", apiTestStatus(apiResult), slaSummary != nil && slaSummary.Breached, runErr),
		}
		if slaSummary != nil && slaSummary.Evaluated {
			out["sla_evaluated"] = true
			out["sla_breached"] = slaSummary.Breached
			if len(slaSummary.Details) > 0 {
				out["sla_details"] = slaSummary.Details
			}
		}
		if slaSummary != nil && slaSummary.Breached {
			if runErr != nil {
				out["error"] = runErr.Error()
			} else {
				out["error"] = "SLA thresholds breached"
			}
		} else if runErr != nil {
			out["error"] = runErr.Error()
		}
		if result != nil {
			out["duration_ms"] = result.Duration.Milliseconds()
			out["transactions"] = result.TotalTransactions
			out["errors"] = result.TotalErrors
			out["tps"] = result.FinalSnapshot.TPS
		} else if apiResult != nil {
			if apiResult.TestID != "" {
				out["test_id"] = apiResult.TestID
			}
			if apiResult.Status != "" {
				out["test_status"] = apiResult.Status
			}
			out["duration_ms"] = apiResult.Duration.Milliseconds()
			out["transactions"] = apiResult.TotalTransactions
			out["errors"] = apiResult.TotalErrors
			out["tps"] = apiResult.FinalSnapshot.TPS
		}

		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))

		if evidenceErr != nil {
			os.Exit(exitCodeUsageOrConfiguration)
		}
		if code := testRunExitCode(args.api != "", apiTestStatus(apiResult), slaSummary != nil && slaSummary.Breached, runErr); code != exitCodeSuccess {
			os.Exit(code)
		}
		return
	}
	if runErr != nil && result == nil && (apiResult == nil || !isTerminalStatus(apiResult.Status)) {
		fmt.Fprintf(os.Stderr, "test failed: %v\n", runErr)
		if evidenceErr != nil {
			os.Exit(exitCodeUsageOrConfiguration)
		}
		os.Exit(testRunExitCode(args.api != "", apiTestStatus(apiResult), false, runErr))
	}

	fmt.Println("\n=== Test Complete ===")
	if result != nil {
		fmt.Printf("  Duration:     %s\n", result.Duration.Round(time.Millisecond))
		fmt.Printf("  Transactions: %d\n", result.TotalTransactions)
		fmt.Printf("  Errors:       %d\n", result.TotalErrors)
		if result.TotalTransactions > 0 {
			errorPct := float64(result.TotalErrors) / float64(result.TotalTransactions) * 100
			fmt.Printf("  Error Rate:   %.2f%%\n", errorPct)
		}
		snap := result.FinalSnapshot
		if snap.Transactions > 0 {
			fmt.Printf("  TPS:          %.1f\n", snap.TPS)
		}
	} else if apiResult != nil {
		fmt.Printf("  Duration:     %s\n", apiResult.Duration.Round(time.Millisecond))
		fmt.Printf("  Transactions: %d\n", apiResult.TotalTransactions)
		fmt.Printf("  Errors:       %d\n", apiResult.TotalErrors)
		if apiResult.TotalTransactions > 0 {
			errorPct := float64(apiResult.TotalErrors) / float64(apiResult.TotalTransactions) * 100
			fmt.Printf("  Error Rate:   %.2f%%\n", errorPct)
		}
		fmt.Printf("  TPS:          %.1f\n", apiResult.FinalSnapshot.TPS)
	}
	if slaSummary != nil && slaSummary.Evaluated {
		status := "passing"
		if slaSummary.Breached {
			status = "breached"
		}
		fmt.Printf("  SLA:          %s\n", status)
		for _, detail := range slaSummary.Details {
			fmt.Printf("    - %s\n", detail)
		}
	}
	if evidenceErr != nil {
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "test failed: %v\n", runErr)
		os.Exit(testRunExitCode(args.api != "", apiTestStatus(apiResult), false, runErr))
	}
	if slaSummary != nil && slaSummary.Breached {
		os.Exit(exitCodeTestFailure)
	}
}

func apiTestStatus(result *ApiTestResult) string {
	if result == nil {
		return ""
	}
	return result.Status
}

type runArgs struct {
	err                      error
	help                     bool
	script                   string
	api                      string
	apiKey                   string
	bearerToken              string
	baseURL                  string
	format                   string
	junit                    string
	vusers                   int
	duration                 time.Duration
	rampUp                   time.Duration
	timeout                  time.Duration
	env                      map[string]string
	p95LatencyMs             *float64
	maxErrorPct              *float64
	maxAvgServerProcessingMs *float64
	maxAvgTransferMs         *float64
	maxErrorCount            *int
}

type envAssignments map[string]string

func (values envAssignments) String() string { return "key=value" }

func (values envAssignments) Set(raw string) error {
	key, value, ok := parseEnvAssignment(raw)
	if !ok {
		return fmt.Errorf("invalid environment assignment %q; expected key=value", raw)
	}
	values[key] = value
	return nil
}

func parseRunArgs(args []string) runArgs {
	r := runArgs{
		api:         strings.TrimSpace(os.Getenv("APEXLOAD_API")),
		apiKey:      strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")),
		bearerToken: strings.TrimSpace(os.Getenv("APEXLOAD_BEARER_TOKEN")),
		vusers:      10,
		duration:    60 * time.Second,
		format:      "text",
		env:         make(map[string]string),
	}
	flags := flag.NewFlagSet("apexctl run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&r.help, "help", false, "show help")
	flags.BoolVar(&r.help, "h", false, "show help")
	flags.StringVar(&r.script, "script", "", "script path")
	flags.StringVar(&r.script, "s", "", "script path")
	flags.StringVar(&r.api, "api", r.api, "controller URL")
	flags.StringVar(&r.apiKey, "api-key", r.apiKey, "API key")
	flags.StringVar(&r.bearerToken, "bearer-token", r.bearerToken, "bearer token")
	flags.StringVar(&r.baseURL, "base-url", "", "base URL")
	flags.Var(envAssignments(r.env), "env", "environment key=value")
	flags.StringVar(&r.format, "format", r.format, "output format")
	flags.StringVar(&r.junit, "junit", "", "JUnit output")
	flags.IntVar(&r.vusers, "vusers", r.vusers, "virtual users")
	flags.IntVar(&r.vusers, "v", r.vusers, "virtual users")
	flags.DurationVar(&r.duration, "duration", r.duration, "duration")
	flags.DurationVar(&r.duration, "d", r.duration, "duration")
	flags.DurationVar(&r.rampUp, "ramp-up", 0, "ramp up")
	flags.DurationVar(&r.rampUp, "r", 0, "ramp up")
	flags.DurationVar(&r.timeout, "timeout", 0, "controller wait timeout")
	floatPointerFlag(flags, "p95-latency-ms", &r.p95LatencyMs)
	floatPointerFlag(flags, "max-error-pct", &r.maxErrorPct)
	floatPointerFlag(flags, "max-avg-server-processing-ms", &r.maxAvgServerProcessingMs)
	floatPointerFlag(flags, "max-avg-transfer-ms", &r.maxAvgTransferMs)
	flags.Func("max-error-count", "maximum error count", func(raw string) error {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return err
		}
		r.maxErrorCount = &value
		return nil
	})
	if err := flags.Parse(args); err != nil {
		r.err = err
	} else if flags.NArg() != 0 {
		r.err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
	} else if r.vusers <= 0 {
		r.err = fmt.Errorf("--vusers must be greater than zero")
	} else if r.duration <= 0 {
		r.err = fmt.Errorf("--duration must be greater than zero")
	} else if r.rampUp < 0 {
		r.err = fmt.Errorf("--ramp-up cannot be negative")
	} else if r.timeout < 0 {
		r.err = fmt.Errorf("--timeout cannot be negative")
	} else if err := validateCLIOutputFormat(r.format); err != nil {
		r.err = err
	} else if err := validateCLIControllerURL(r.api); err != nil {
		r.err = err
	} else if err := validateSLAThresholdValues(
		r.p95LatencyMs,
		r.maxErrorPct,
		r.maxAvgServerProcessingMs,
		r.maxAvgTransferMs,
		r.maxErrorCount,
	); err != nil {
		r.err = err
	} else if r.timeout == 0 {
		r.timeout = r.duration + 30*time.Minute
	}
	r.api = strings.TrimRight(strings.TrimSpace(r.api), "/")
	r.apiKey = strings.TrimSpace(r.apiKey)
	r.bearerToken = strings.TrimSpace(r.bearerToken)
	r.format = strings.ToLower(strings.TrimSpace(r.format))
	return r
}

func floatPointerFlag(flags *flag.FlagSet, name string, target **float64) {
	flags.Var(optionalFloat{value: target}, name, name)
}

func printUsage() {
	fmt.Println(`apexctl - APEX Load Performance Testing CLI

Usage:
  apexctl <command> [flags]

Commands:
  run       Execute a performance test
  ci        Run a CI performance gate locally or through a controller
  script    Validate or explain a JSON DSL script
  status    Print a test's current status + SLA verdict (exits 1 on breach/failure)
  wait      Block until a started test finishes, then report SLA verdict
  archive   Inspect an archived run (verify re-checks artifact SHA-256 integrity)
  record    Record a browser script with the embedded recorder (ranked selectors + side-car HAR)
  record-agent  Start the localhost browser recording bridge for a remote dashboard
  github    Inspect and run GitHub Enterprise sync/check workflows
  import    Import Bruno collections or Grafana dashboards
  version   Print version information
  help      Show this help

Run Flags:
  --script, -s    Path to test script (JSON DSL)     [required]
  --vusers, -v    Number of virtual users             [default: 10]
  --duration, -d  Test duration (e.g. 5m, 1h)         [default: 1m]
  --ramp-up, -r   Ramp-up period (e.g. 30s, 2m)       [default: 0s]
	--timeout        API run wait timeout                 [default: duration + 30m]
  --env, -env     Runtime variable override (k=v)     [repeatable]
  --base-url      Override scenario base URL at run time
  --api           Run via API instead of locally (e.g. http://localhost:8080)
  --api-key       API key for secured controller calls (or APEXLOAD_API_KEY)
  --bearer-token  Bearer token for secured controller calls (or APEXLOAD_BEARER_TOKEN)
  --format        Output format (text, json)          [default: text]
  --junit         Write a JUnit XML report to this path (for CI test reporting)
  --p95-latency-ms Maximum allowed P95 latency in ms
  --max-error-pct  Maximum allowed error rate percentage
  --max-avg-server-processing-ms  Maximum allowed average server processing time in ms
  --max-avg-transfer-ms           Maximum allowed average transfer time in ms
  --max-error-count               Maximum allowed total error count

Examples:
  apexctl run --script order-flow.json --vusers 100 --duration 5m
  apexctl script validate --script order-flow.json --format json
  apexctl script explain --script order-flow.json
  apexctl run --script order-flow.json -env target_namespace=pr-123 --base-url https://preview.example.com
  apexctl run --script order-flow.json --p95-latency-ms 800 --max-error-pct 2.5
  apexctl run --api http://localhost:8080 --script order-flow.json --format json
  apexctl run --script order-flow.json --p95-latency-ms 800 --junit results.xml
  apexctl wait --api http://localhost:8080 --test <id> --junit results.xml
  apexctl github diagnostics --api http://localhost:8080
  apexctl github sync --api http://localhost:8080 --project <id> --kind import
  apexctl import bruno --api http://localhost:8080 --path ./collection --project <id> --output native --folder <id>`)
	fmt.Println()
	fmt.Println(ctlUsage())
	fmt.Println()
	fmt.Println(archiveUsage())
}
