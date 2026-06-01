package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ctlArgs holds the flags shared by the status/wait subcommands, which operate
// on an already-created test by ID against a running controller.
type ctlArgs struct {
	api     string
	test    string
	format  string
	junit   string
	timeout time.Duration
}

func parseCtlArgs(args []string) ctlArgs {
	c := ctlArgs{format: "text", timeout: 30 * time.Minute}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--api":
			if i+1 < len(args) {
				i++
				c.api = args[i]
			}
		case "--test", "--test-id", "-t":
			if i+1 < len(args) {
				i++
				c.test = args[i]
			}
		case "--format":
			if i+1 < len(args) {
				i++
				c.format = args[i]
			}
		case "--junit":
			if i+1 < len(args) {
				i++
				c.junit = args[i]
			}
		case "--timeout":
			if i+1 < len(args) {
				i++
				if d, err := time.ParseDuration(args[i]); err == nil {
					c.timeout = d
				}
			}
		}
	}
	return c
}

func requireCtlTarget(c ctlArgs) {
	if c.api == "" {
		fmt.Fprintln(os.Stderr, "error: --api is required (e.g. http://localhost:8080)")
		os.Exit(2)
	}
	if c.test == "" {
		fmt.Fprintln(os.Stderr, "error: --test <id> is required")
		os.Exit(2)
	}
}

// statusCmd prints a point-in-time status + SLA verdict for a test and exits
// non-zero if the SLA is breached or the test failed — a cheap pipeline check.
func statusCmd() {
	c := parseCtlArgs(os.Args[2:])
	requireCtlTarget(c)

	client := NewApiClient(c.api)
	report, err := client.GetSLAStatus(c.test)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error fetching status: %v\n", err)
		os.Exit(2)
	}

	failed := report.SLABreached || report.Status == "FAILED" || report.Status == "ABORTED"

	if c.format == "json" {
		out := map[string]any{
			"test_id":      report.TestID,
			"status":       report.Status,
			"sla_breached": report.SLABreached,
			"failed":       failed,
		}
		if len(report.Details) > 0 {
			out["sla_details"] = report.Details
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("Test:   %s\n", report.TestID)
		fmt.Printf("Status: %s\n", report.Status)
		sla := "passing"
		if report.SLABreached {
			sla = "breached"
		}
		fmt.Printf("SLA:    %s\n", sla)
		for _, d := range report.Details {
			fmt.Printf("  - %s\n", d)
		}
	}

	if failed {
		os.Exit(1)
	}
}

// waitCmd blocks on an already-started test until it reaches a terminal state,
// emits the final verdict (and optional JUnit), and exits non-zero on
// failure/SLA breach so CI gates on it.
func waitCmd() {
	c := parseCtlArgs(os.Args[2:])
	requireCtlTarget(c)

	client := NewApiClient(c.api)
	result, runErr := client.WaitForTest(c.test, c.format, c.timeout)

	var sla *SLASummary
	if result != nil {
		sla = result.SLASummary
	}

	if c.junit != "" {
		in := junitInput{suiteName: "apexload:" + c.test, classname: "apexload", sla: sla, runErr: runErr}
		if result != nil {
			in.duration = result.Duration
			in.transactions = result.TotalTransactions
			in.errors = result.TotalErrors
			in.tps = result.FinalSnapshot.TPS
		}
		if err := writeJUnitReport(c.junit, in); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}

	breached := sla != nil && sla.Breached

	if c.format == "json" {
		out := map[string]any{"test_id": c.test, "status": "success"}
		if runErr != nil {
			out["status"] = "error"
			out["error"] = runErr.Error()
		} else if breached {
			out["status"] = "failed"
		}
		if sla != nil {
			out["sla_evaluated"] = sla.Evaluated
			out["sla_breached"] = sla.Breached
			if len(sla.Details) > 0 {
				out["sla_details"] = sla.Details
			}
		}
		if result != nil {
			out["duration_ms"] = result.Duration.Milliseconds()
			out["transactions"] = result.TotalTransactions
			out["errors"] = result.TotalErrors
			out["tps"] = result.FinalSnapshot.TPS
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Println("\n=== Test Complete ===")
		if result != nil {
			fmt.Printf("  Duration:     %s\n", result.Duration.Round(time.Millisecond))
			fmt.Printf("  Transactions: %d\n", result.TotalTransactions)
			fmt.Printf("  Errors:       %d\n", result.TotalErrors)
		}
		if sla != nil && sla.Evaluated {
			status := "passing"
			if sla.Breached {
				status = "breached"
			}
			fmt.Printf("  SLA:          %s\n", status)
			for _, d := range sla.Details {
				fmt.Printf("    - %s\n", d)
			}
		}
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "test failed: %v\n", runErr)
		}
	}

	if runErr != nil {
		// A non-SLA execution error (timeout, transport, FAILED/ABORTED) is a
		// hard failure for the pipeline.
		if breached {
			os.Exit(1)
		}
		os.Exit(1)
	}
	if breached {
		os.Exit(1)
	}
}

// printCtlUsage is referenced by the help text for the new subcommands.
func ctlUsage() string {
	return strings.TrimSpace(`
Status Flags (apexctl status):
  --api     Controller base URL (e.g. http://localhost:8080)   [required]
  --test    Test ID to inspect                                  [required]
  --format  Output format (text, json)                          [default: text]

Wait Flags (apexctl wait):
  --api     Controller base URL (e.g. http://localhost:8080)   [required]
  --test    Test ID to wait on                                  [required]
  --timeout Max time to wait for terminal state                 [default: 30m]
  --format  Output format (text, json)                          [default: text]
  --junit   Write a JUnit XML report to this path`)
}
