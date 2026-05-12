package main

import (
	"context"
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

	fmt.Printf("Starting test: %s\n", script.Name)
	fmt.Printf("  Protocol: %s\n", script.Protocol)
	fmt.Printf("  VUsers:   %d\n", args.vusers)
	fmt.Printf("  Duration: %s\n", args.duration)
	if args.rampUp > 0 {
		fmt.Printf("  Ramp-up:  %s\n", args.rampUp)
	}
	fmt.Println()

	result, err := eng.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n=== Test Complete ===")
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
		fmt.Printf("  Avg:          %s\n", snap.AvgDuration.Round(time.Microsecond))
		fmt.Printf("  P50:          %s\n", snap.P50.Round(time.Microsecond))
		fmt.Printf("  P90:          %s\n", snap.P90.Round(time.Microsecond))
		fmt.Printf("  P99:          %s\n", snap.P99.Round(time.Microsecond))
	}
}

type runArgs struct {
	script   string
	vusers   int
	duration time.Duration
	rampUp   time.Duration
}

func parseRunArgs(args []string) runArgs {
	r := runArgs{
		vusers:   10,
		duration: 60 * time.Second,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--script", "-s":
			if i+1 < len(args) {
				i++
				r.script = args[i]
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

Examples:
  stormctl run --script order-flow.json --vusers 100 --duration 5m
  stormctl run -s api-test.json -v 50 -d 2m -r 30s`)
}
