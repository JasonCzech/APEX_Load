package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/browser"
	_ "github.com/jasonczech/apexload/pkg/protocol/grpc"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	_ "github.com/jasonczech/apexload/pkg/protocol/jms"
	_ "github.com/jasonczech/apexload/pkg/protocol/soap"
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runTest()
	case "script":
		scriptCmd()
	case "status":
		statusCmd()
	case "wait":
		waitCmd()
	case "record":
		recordCmd()
	case "version":
		fmt.Println("apexctl v0.1.0")
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runTest() {
	args := parseRunArgs(os.Args[2:])

	if args.script == "" {
		fmt.Fprintln(os.Stderr, "error: --script is required")
		os.Exit(1)
	}

	script, err := loadScriptForRun(args.script, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error preparing script: %v\n", err)
		os.Exit(1)
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
			os.Exit(1)
		}
		client := NewApiClient(args.api)
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
			os.Exit(1)
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

	if args.junit != "" {
		if err := writeRunJUnit(args, script, result, apiResult, slaSummary, runErr); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}

	if args.format == "json" {
		out := map[string]any{
			"status": "success",
		}
		if slaSummary != nil && slaSummary.Evaluated {
			out["sla_evaluated"] = true
			out["sla_breached"] = slaSummary.Breached
			if len(slaSummary.Details) > 0 {
				out["sla_details"] = slaSummary.Details
			}
		}
		if slaSummary != nil && slaSummary.Breached {
			out["status"] = "failed"
			if runErr != nil {
				out["error"] = runErr.Error()
			} else {
				out["error"] = "SLA thresholds breached"
			}
		} else if runErr != nil {
			out["status"] = "error"
			out["error"] = runErr.Error()
		}
		if result != nil {
			out["duration_ms"] = result.Duration.Milliseconds()
			out["transactions"] = result.TotalTransactions
			out["errors"] = result.TotalErrors
			out["tps"] = result.FinalSnapshot.TPS
		} else if apiResult != nil {
			out["duration_ms"] = apiResult.Duration.Milliseconds()
			out["transactions"] = apiResult.TotalTransactions
			out["errors"] = apiResult.TotalErrors
			out["tps"] = apiResult.FinalSnapshot.TPS
		}

		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))

		if runErr != nil || (slaSummary != nil && slaSummary.Breached) {
			os.Exit(1)
		}
		return
	}

	if runErr != nil && result == nil && apiResult == nil {
		fmt.Fprintf(os.Stderr, "test failed: %v\n", runErr)
		os.Exit(1)
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
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "test failed: %v\n", runErr)
		os.Exit(1)
	}
	if slaSummary != nil && slaSummary.Breached {
		os.Exit(1)
	}
}

type runArgs struct {
	script                   string
	api                      string
	baseURL                  string
	format                   string
	junit                    string
	vusers                   int
	duration                 time.Duration
	rampUp                   time.Duration
	env                      map[string]string
	p95LatencyMs             *float64
	maxErrorPct              *float64
	maxAvgServerProcessingMs *float64
	maxAvgTransferMs         *float64
	maxErrorCount            *int
}

func parseRunArgs(args []string) runArgs {
	r := runArgs{
		vusers:   10,
		duration: 60 * time.Second,
		format:   "text",
		env:      make(map[string]string),
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--script", "-s":
			if i+1 < len(args) {
				i++
				r.script = args[i]
			}
		case "--api":
			if i+1 < len(args) {
				i++
				r.api = args[i]
			}
		case "--base-url":
			if i+1 < len(args) {
				i++
				r.baseURL = args[i]
			}
		case "--env", "-env":
			if i+1 < len(args) {
				i++
				if key, value, ok := parseEnvAssignment(args[i]); ok {
					r.env[key] = value
				}
			}
		case "--format":
			if i+1 < len(args) {
				i++
				r.format = args[i]
			}
		case "--junit":
			if i+1 < len(args) {
				i++
				r.junit = args[i]
			}
		case "--vusers", "-v":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &r.vusers)
			}
		case "--duration", "-d":
			if i+1 < len(args) {
				i++
				r.duration, _ = time.ParseDuration(args[i])
			}
		case "--ramp-up", "-r":
			if i+1 < len(args) {
				i++
				r.rampUp, _ = time.ParseDuration(args[i])
			}
		case "--p95-latency-ms":
			if i+1 < len(args) {
				i++
				if value, err := strconv.ParseFloat(args[i], 64); err == nil {
					r.p95LatencyMs = &value
				}
			}
		case "--max-error-pct":
			if i+1 < len(args) {
				i++
				if value, err := strconv.ParseFloat(args[i], 64); err == nil {
					r.maxErrorPct = &value
				}
			}
		case "--max-avg-server-processing-ms":
			if i+1 < len(args) {
				i++
				if value, err := strconv.ParseFloat(args[i], 64); err == nil {
					r.maxAvgServerProcessingMs = &value
				}
			}
		case "--max-avg-transfer-ms":
			if i+1 < len(args) {
				i++
				if value, err := strconv.ParseFloat(args[i], 64); err == nil {
					r.maxAvgTransferMs = &value
				}
			}
		case "--max-error-count":
			if i+1 < len(args) {
				i++
				if value, err := strconv.Atoi(args[i]); err == nil {
					r.maxErrorCount = &value
				}
			}
		}
	}
	return r
}

func printUsage() {
	fmt.Println(`apexctl - APEX Load Performance Testing CLI

Usage:
  apexctl <command> [flags]

Commands:
  run       Execute a performance test
  script    Validate or explain a JSON DSL script
  status    Print a test's current status + SLA verdict (exits 1 on breach/failure)
  wait      Block until a started test finishes, then report SLA verdict
  record    Record a new browser script using Playwright
  version   Print version information
  help      Show this help

Run Flags:
  --script, -s    Path to test script (JSON DSL)     [required]
  --vusers, -v    Number of virtual users             [default: 10]
  --duration, -d  Test duration (e.g. 5m, 1h)         [default: 1m]
  --ramp-up, -r   Ramp-up period (e.g. 30s, 2m)       [default: 0s]
	--env, -env     Runtime variable override (k=v)     [repeatable]
	--base-url      Override scenario base URL at run time
  --api           Run via API instead of locally (e.g. http://localhost:8080)
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
  apexctl wait --api http://localhost:8080 --test <id> --junit results.xml`)
	fmt.Println()
	fmt.Println(ctlUsage())
}
