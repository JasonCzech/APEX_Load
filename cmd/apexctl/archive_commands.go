package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// archiveCmd dispatches `apexctl archive <subcommand>`.
func archiveCmd() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "error: archive subcommand required (verify)")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, archiveUsage())
		os.Exit(2)
	}
	switch os.Args[2] {
	case "verify":
		archiveVerifyCmd(os.Args[3:])
	case "help", "--help", "-h":
		fmt.Println(archiveUsage())
	default:
		fmt.Fprintf(os.Stderr, "unknown archive subcommand: %s\n", os.Args[2])
		fmt.Fprintln(os.Stderr, archiveUsage())
		os.Exit(2)
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
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--api":
			if i+1 < len(args) {
				i++
				c.api = args[i]
			}
		case "--api-key":
			if i+1 < len(args) {
				i++
				c.apiKey = args[i]
			}
		case "--bearer-token":
			if i+1 < len(args) {
				i++
				c.bearerToken = args[i]
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
		default:
			if c.test == "" && !strings.HasPrefix(args[i], "-") {
				c.test = args[i]
			}
		}
	}
	return c
}

// archiveVerifyCmd recomputes each archived artifact's SHA-256 and compares it to
// the hash recorded in the run manifest, reporting match / mismatch (tampered) /
// missing (corrupt). It exits non-zero when the archive is not verified so CI
// pipelines can gate on tamper detection.
func archiveVerifyCmd(args []string) {
	c := parseArchiveVerifyArgs(args)
	if c.api == "" {
		fmt.Fprintln(os.Stderr, "error: --api is required (e.g. http://localhost:8080) or set APEXLOAD_API")
		os.Exit(2)
	}
	if c.test == "" {
		fmt.Fprintln(os.Stderr, "error: a test id is required (positional <test-id> or --test <id>)")
		os.Exit(2)
	}

	client := NewApiClientWithAuth(c.api, c.apiKey, c.bearerToken)
	report, err := client.VerifyArchive(c.test)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error verifying archive: %v\n", err)
		os.Exit(2)
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
		os.Exit(1)
	}
}

func archiveUsage() string {
	return strings.TrimSpace(`
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
