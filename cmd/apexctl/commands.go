package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ctlArgs holds the flags shared by the status/wait subcommands, which operate
// on an already-created test by ID against a running controller.
type ctlArgs struct {
	err         error
	help        bool
	api         string
	apiKey      string
	bearerToken string
	test        string
	format      string
	junit       string
	timeout     time.Duration
}

func parseCtlArgs(args []string) ctlArgs {
	c := ctlArgs{
		api:         strings.TrimSpace(os.Getenv("APEXLOAD_API")),
		apiKey:      strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")),
		bearerToken: strings.TrimSpace(os.Getenv("APEXLOAD_BEARER_TOKEN")),
		format:      "text",
		timeout:     0,
	}
	flags := flag.NewFlagSet("apexctl control", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&c.help, "help", false, "show help")
	flags.BoolVar(&c.help, "h", false, "show help")
	flags.StringVar(&c.api, "api", c.api, "controller URL")
	flags.StringVar(&c.apiKey, "api-key", c.apiKey, "API key")
	flags.StringVar(&c.bearerToken, "bearer-token", c.bearerToken, "bearer token")
	flags.StringVar(&c.test, "test", "", "test ID")
	flags.StringVar(&c.test, "test-id", "", "test ID")
	flags.StringVar(&c.test, "t", "", "test ID")
	flags.StringVar(&c.format, "format", c.format, "output format")
	flags.StringVar(&c.junit, "junit", "", "JUnit output")
	flags.DurationVar(&c.timeout, "timeout", 0, "wait timeout")
	if err := flags.Parse(args); err != nil {
		c.err = err
	} else if flags.NArg() != 0 {
		c.err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
	} else if c.timeout < 0 {
		c.err = fmt.Errorf("--timeout cannot be negative")
	} else if err := validateCLIOutputFormat(c.format); err != nil {
		c.err = err
	} else if err := validateCLIControllerURL(c.api); err != nil {
		c.err = err
	}
	c.api = strings.TrimRight(strings.TrimSpace(c.api), "/")
	c.apiKey = strings.TrimSpace(c.apiKey)
	c.bearerToken = strings.TrimSpace(c.bearerToken)
	c.test = strings.TrimSpace(c.test)
	c.format = strings.ToLower(strings.TrimSpace(c.format))
	return c
}

func requireCtlTarget(c ctlArgs) {
	if c.err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", c.err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if c.api == "" {
		fmt.Fprintln(os.Stderr, "error: --api is required (e.g. http://localhost:8080)")
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if c.test == "" {
		fmt.Fprintln(os.Stderr, "error: --test <id> is required")
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

// statusCmd prints a point-in-time status + SLA verdict for a test and exits
// non-zero if the SLA is breached or the test failed — a cheap pipeline check.
func statusCmd() {
	c := parseCtlArgs(os.Args[2:])
	if c.help && c.err == nil {
		fmt.Println(ctlUsage())
		return
	}
	if c.err == nil {
		c.err = validateStatusArgs(c)
	}
	requireCtlTarget(c)

	client := NewApiClientWithAuth(c.api, c.apiKey, c.bearerToken)
	report, err := client.GetSLAStatus(c.test)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error fetching status: %v\n", err)
		os.Exit(exitCodeControllerOrTransport)
	}

	failed := report.SLABreached || isFailedTestStatus(report.Status)

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
		if len(report.TransactionRuleResults) > 0 {
			out["transaction_rule_results"] = report.TransactionRuleResults
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
		for _, result := range report.TransactionRuleResults {
			fmt.Printf("  - %s / %s: %s\n", result.Rule.Transaction, result.Rule.Metric, result.Status)
		}
	}

	if failed {
		os.Exit(exitCodeTestFailure)
	}
}

func validateStatusArgs(c ctlArgs) error {
	if c.timeout != 0 {
		return fmt.Errorf("--timeout is supported by wait, not status")
	}
	if c.junit != "" {
		return fmt.Errorf("--junit is supported by wait, not status")
	}
	return nil
}

// waitCmd blocks on an already-started test until it reaches a terminal state,
// emits the final verdict (and optional JUnit), and exits non-zero on
// failure/SLA breach so CI gates on it.
func waitCmd() {
	c := parseCtlArgs(os.Args[2:])
	if c.help && c.err == nil {
		fmt.Println(ctlUsage())
		return
	}
	requireCtlTarget(c)

	client := NewApiClientWithAuth(c.api, c.apiKey, c.bearerToken)
	result, runErr := client.WaitForTest(c.test, c.format, c.timeout)

	var sla *SLASummary
	if result != nil {
		sla = result.SLASummary
	}

	var evidenceErr error
	if c.junit != "" {
		in := junitInput{suiteName: "apexload:" + c.test, classname: "apexload", sla: sla, runErr: runErr}
		if result != nil {
			in.duration = result.Duration
			in.transactions = result.TotalTransactions
			in.errors = result.TotalErrors
			in.tps = result.FinalSnapshot.TPS
		}
		if err := writeJUnitReport(c.junit, in); err != nil {
			evidenceErr = err
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}

	breached := sla != nil && sla.Breached
	terminalStatus := ""
	if result != nil {
		terminalStatus = result.Status
	}
	failedVerdict := breached || isFailedTestStatus(terminalStatus)

	if c.format == "json" {
		out := map[string]any{"test_id": c.test, "status": "success"}
		if failedVerdict {
			out["status"] = "failed"
			if runErr != nil {
				out["error"] = runErr.Error()
			}
		} else if runErr != nil {
			out["status"] = "error"
			out["error"] = runErr.Error()
		}
		if sla != nil {
			out["sla_evaluated"] = sla.Evaluated
			out["sla_breached"] = sla.Breached
			if len(sla.Details) > 0 {
				out["sla_details"] = sla.Details
			}
			if len(sla.TransactionRuleResults) > 0 {
				out["transaction_rule_results"] = sla.TransactionRuleResults
			}
		}
		if result != nil {
			out["test_status"] = result.Status
			out["duration_ms"] = result.Duration.Milliseconds()
			out["transactions"] = result.TotalTransactions
			out["errors"] = result.TotalErrors
			out["tps"] = result.FinalSnapshot.TPS
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	} else if result != nil {
		fmt.Println("\n=== Test Complete ===")
		fmt.Printf("  Status:       %s\n", result.Status)
		fmt.Printf("  Duration:     %s\n", result.Duration.Round(time.Millisecond))
		fmt.Printf("  Transactions: %d\n", result.TotalTransactions)
		fmt.Printf("  Errors:       %d\n", result.TotalErrors)
		if sla != nil && sla.Evaluated {
			status := "passing"
			if sla.Breached {
				status = "breached"
			}
			fmt.Printf("  SLA:          %s\n", status)
			for _, d := range sla.Details {
				fmt.Printf("    - %s\n", d)
			}
			for _, rule := range sla.TransactionRuleResults {
				fmt.Printf("    - %s / %s: %s\n", rule.Rule.Transaction, rule.Rule.Metric, rule.Status)
			}
		}
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "test failed: %v\n", runErr)
		}
	} else if runErr != nil {
		fmt.Fprintf(os.Stderr, "wait failed: %v\n", runErr)
	}

	if evidenceErr != nil {
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if code := testRunExitCode(true, terminalStatus, breached, runErr); code != exitCodeSuccess {
		os.Exit(code)
	}
}

// printCtlUsage is referenced by the help text for the new subcommands.
//
// The exit-code block is part of apexctl's public contract: automation must be
// able to tell a load-test verdict (exit 1) from a controller/transport failure
// (exit 3) without scraping stderr. ctlUsageExitCodes keeps the documented
// numbers pinned to the exitCode* constants.
func ctlUsage() string {
	return strings.TrimSpace(`
Status Flags (apexctl status):
  --api     Controller base URL (e.g. http://localhost:8080)   [required]
  --api-key API key for secured controllers (or APEXLOAD_API_KEY)
  --bearer-token Bearer token for secured controllers (or APEXLOAD_BEARER_TOKEN)
  --test    Test ID to inspect                                  [required]
  --format  Output format (text, json)                          [default: text]

Wait Flags (apexctl wait):
  --api     Controller base URL (e.g. http://localhost:8080)   [required]
  --api-key API key for secured controllers (or APEXLOAD_API_KEY)
  --bearer-token Bearer token for secured controllers (or APEXLOAD_BEARER_TOKEN)
  --test    Test ID to wait on                                  [required]
  --timeout Max time to wait for terminal state                 [default: test duration + 30m]
  --format  Output format (text, json)                          [default: text]
  --junit   Write a JUnit XML report to this path

` + ctlUsageExitCodes())
}

// ctlUsageExitCodes renders the status/wait exit-code contract from the
// exitCode* constants so the help text cannot drift away from the real codes.
func ctlUsageExitCodes() string {
	return strings.TrimSpace(fmt.Sprintf(`
Exit Codes (apexctl status, apexctl wait):
  %d  Success - the test reached a terminal state with no failed status and no SLA breach
  %d  Test verdict failure - terminal status FAILED/ABORTED, or an evaluated SLA was breached
  %d  Usage or configuration error - bad flags, or the --junit report could not be written
  %d  Controller or transport failure - the controller was unreachable or returned an error,
     the status could not be read, or wait timed out before a terminal state`,
		exitCodeSuccess,
		exitCodeTestFailure,
		exitCodeUsageOrConfiguration,
		exitCodeControllerOrTransport))
}
