package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// archiveCmd dispatches `apexctl archive <subcommand>`.
func archiveCmd() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "error: archive subcommand required (verify, evidence)")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, archiveUsage())
		os.Exit(exitCodeUsageOrConfiguration)
	}
	switch os.Args[2] {
	case "evidence":
		os.Exit(runArchiveEvidenceCommand(os.Args[3:], os.Stdout, os.Stderr))
	case "verify":
		archiveVerifyCmd(os.Args[3:])
	case "help", "--help", "-h":
		fmt.Println(archiveUsage())
	default:
		fmt.Fprintf(os.Stderr, "unknown archive subcommand: %s\n", os.Args[2])
		fmt.Fprintln(os.Stderr, archiveUsage())
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

// parseArchiveVerifyArgs parses the flags/positional for `archive verify`. The
// controller URL and credentials default to the same environment variables the
// rest of the CLI honors (APEXLOAD_API / APEXLOAD_API_KEY / APEXLOAD_BEARER_TOKEN),
// matching the github/record subcommands. The first bare (non-flag) argument is
// the positional test ID, so both `archive verify <id>` and
// `archive verify --test <id>` work.
func parseArchiveVerifyArgs(args []string) ctlArgs {
	c := ctlArgs{
		format:      "text",
		api:         firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://localhost:8080"),
		apiKey:      os.Getenv("APEXLOAD_API_KEY"),
		bearerToken: os.Getenv("APEXLOAD_BEARER_TOKEN"),
	}
	flags := flag.NewFlagSet("apexctl archive verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flaggedTest := ""
	flags.BoolVar(&c.help, "help", false, "show help")
	flags.BoolVar(&c.help, "h", false, "show help")
	flags.StringVar(&c.api, "api", c.api, "controller URL")
	flags.StringVar(&c.apiKey, "api-key", c.apiKey, "API key")
	flags.StringVar(&c.bearerToken, "bearer-token", c.bearerToken, "bearer token")
	flags.StringVar(&flaggedTest, "test", "", "test ID")
	flags.StringVar(&flaggedTest, "test-id", "", "test ID")
	flags.StringVar(&flaggedTest, "t", "", "test ID")
	flags.StringVar(&c.format, "format", c.format, "output format")

	// The documented form puts the positional test ID before optional flags.
	// Go's standard flag package stops at the first positional, so parse the
	// remainder once more after consuming that single positional argument.
	if err := flags.Parse(args); err != nil {
		c.err = err
		return c
	}
	remainder := flags.Args()
	positionalTest := ""
	if len(remainder) > 0 {
		positionalTest = remainder[0]
		if err := flags.Parse(remainder[1:]); err != nil {
			c.err = err
			return c
		}
		if flags.NArg() != 0 {
			c.err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
			return c
		}
	}
	if positionalTest != "" && flaggedTest != "" {
		c.err = fmt.Errorf("test ID was supplied both positionally and with --test")
		return c
	}
	c.test = firstNonEmptyCLI(flaggedTest, positionalTest)
	c.api = strings.TrimRight(strings.TrimSpace(c.api), "/")
	c.apiKey = strings.TrimSpace(c.apiKey)
	c.bearerToken = strings.TrimSpace(c.bearerToken)
	c.test = strings.TrimSpace(c.test)
	c.format = strings.ToLower(strings.TrimSpace(c.format))
	if err := validateCLIOutputFormat(c.format); c.err == nil && err != nil {
		c.err = err
	} else if err := validateCLIControllerURL(c.api); c.err == nil && err != nil {
		c.err = err
	}
	return c
}

// archiveVerifyCmd recomputes each archived artifact's SHA-256 and compares it to
// the hash recorded in the run manifest, reporting match / mismatch (tampered) /
// missing (corrupt). It exits non-zero when the archive is not verified so CI
// pipelines can gate on tamper detection.
func archiveVerifyCmd(args []string) {
	c := parseArchiveVerifyArgs(args)
	if c.err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", c.err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if c.help {
		fmt.Println(archiveUsage())
		return
	}
	if c.api == "" {
		fmt.Fprintln(os.Stderr, "error: --api is required (e.g. http://localhost:8080) or set APEXLOAD_API")
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if c.test == "" {
		fmt.Fprintln(os.Stderr, "error: a test id is required (positional <test-id> or --test <id>)")
		os.Exit(exitCodeUsageOrConfiguration)
	}

	client := NewApiClientWithAuth(c.api, c.apiKey, c.bearerToken)
	report, err := client.VerifyArchive(c.test)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error verifying archive: %v\n", err)
		os.Exit(exitCodeControllerOrTransport)
	}

	if c.format == "json" {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("Test:      %s\n", report.TestID)
		fmt.Printf("Integrity: %s\n", strings.ToUpper(report.Status))
		fmt.Printf("Summary:   %s\n", report.Summary)
		fmt.Printf("Artifacts: %d matched, %d mismatched, %d missing", report.Matched, report.Mismatched, report.Missing)
		if report.Unverifiable > 0 {
			fmt.Printf(", %d unverifiable", report.Unverifiable)
		}
		fmt.Println()
		for _, artifact := range report.Artifacts {
			line := fmt.Sprintf("  [%s] %s", strings.ToUpper(artifact.Status), artifact.Name)
			if artifact.Detail != "" {
				line += " — " + artifact.Detail
			}
			fmt.Println(line)
		}
	}

	if !report.Verified {
		os.Exit(exitCodeTestFailure)
	}
}

func archiveUsage() string {
	return strings.TrimSpace(`
Archive subcommands:
  verify    Recompute archive artifact integrity
  evidence  Capture incarnation-bound final archive evidence (--help for flags)

Archive Flags (apexctl archive verify <test-id>):
  --api     Controller base URL (default APEXLOAD_API or http://localhost:8080)
  --api-key API key for secured controllers (or APEXLOAD_API_KEY)
  --bearer-token Bearer token for secured controllers (or APEXLOAD_BEARER_TOKEN)
  --test    Test ID to verify (alternative to the positional argument)
  --format  Output format (text, json)                          [default: text]

Re-verifies an archived run against the SHA-256 hashes recorded in its manifest,
reporting match / mismatch (tampered) / missing (corrupt). Exits non-zero when the
archive is not verified.

Example:
  apexctl archive verify <test-id> --api http://localhost:8080`)
}
