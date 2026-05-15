package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/browser"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/http"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/jms"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/soap"
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
	case "version":
		fmt.Println("stormctl v0.1.0")
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

	var result *engine.RunResult
	var apiResult *ApiTestResult
	var runErr error

	if args.api != "" {
		client := NewApiClient(args.api)
		apiResult, runErr = client.RunTest(args.script, args.vusers, args.duration.String(), args.rampUp.String(), args.format)
	} else {
		script, err := dsl.ParseFile(args.script)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error parsing script: %v\n", err)
			os.Exit(1)
		}

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
	}

	if args.format == "json" {
		out := map[string]any{
			"status": "success",
		}
		if runErr != nil {
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

		if runErr != nil {
			os.Exit(1)
		}
		return
	}

	if runErr != nil {
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
}

type runArgs struct {
	script   string
	api      string
	format   string
	vusers   int
	duration time.Duration
	rampUp   time.Duration
}

func parseRunArgs(args []string) runArgs {
	r := runArgs{
		vusers:   10,
		duration: 60 * time.Second,
		format:   "text",
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
		case "--format":
			if i+1 < len(args) {
				i++
				r.format = args[i]
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
		}
	}
	return r
}

func printUsage() {
	fmt.Println(`stormctl - Stormrunner Performance Testing CLI

Usage:
  stormctl <command> [flags]

Commands:
  run       Execute a performance test
  version   Print version information
  help      Show this help

Run Flags:
  --script, -s    Path to test script (JSON DSL)     [required]
  --vusers, -v    Number of virtual users             [default: 10]
  --duration, -d  Test duration (e.g. 5m, 1h)         [default: 1m]
  --ramp-up, -r   Ramp-up period (e.g. 30s, 2m)       [default: 0s]
  --api           Run via API instead of locally (e.g. http://localhost:8080)
  --format        Output format (text, json)          [default: text]

Examples:
  stormctl run --script order-flow.json --vusers 100 --duration 5m
  stormctl run --api http://localhost:8080 --script order-flow.json --format json`)
}
