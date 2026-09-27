package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type archiveEvidenceArgs struct {
	expected                         archiveEvidenceExpectation
	api, apiKey, bearerToken, output string
	timeout                          time.Duration
	help                             bool
}

func parseArchiveEvidenceArgs(args []string) (archiveEvidenceArgs, error) {
	c := archiveEvidenceArgs{api: firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://localhost:8080"),
		apiKey: os.Getenv("APEXLOAD_API_KEY"), bearerToken: os.Getenv("APEXLOAD_BEARER_TOKEN"), timeout: 2 * time.Minute}
	flags := flag.NewFlagSet("apexctl archive evidence", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&c.help, "help", false, "show help")
	flags.BoolVar(&c.help, "h", false, "show help")
	flags.StringVar(&c.api, "api", c.api, "controller URL")
	flags.StringVar(&c.apiKey, "api-key", c.apiKey, "API key")
	flags.StringVar(&c.bearerToken, "bearer-token", c.bearerToken, "bearer token")
	flags.StringVar(&c.expected.TestID, "test", "", "expected test ID")
	flags.StringVar(&c.expected.ProjectID, "project", "", "expected project ID")
	flags.StringVar(&c.expected.Status, "status", "COMPLETE", "expected terminal status")
	flags.StringVar(&c.expected.MetadataBackend, "metadata-backend", "sqlite", "sqlite or postgres")
	created := ""
	flags.StringVar(&created, "created-at", "", "expected run creation timestamp")
	flags.StringVar(&c.output, "output", "", "new absolute private evidence directory")
	flags.DurationVar(&c.timeout, "timeout", c.timeout, "total API observation timeout")
	if err := flags.Parse(args); err != nil {
		return c, err
	}
	if c.help {
		return c, nil
	}
	if flags.NArg() != 0 {
		return c, fmt.Errorf("use --test for the test ID; positional arguments are unsupported")
	}
	parsed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return c, fmt.Errorf("--created-at requires an RFC3339 timestamp")
	}
	c.expected.CreatedAt = parsed
	if err := c.expected.validate(); err != nil {
		return c, err
	}
	if c.timeout <= 0 || c.timeout > 30*time.Minute {
		return c, fmt.Errorf("--timeout must be positive and at most 30m")
	}
	if !filepath.IsAbs(c.output) {
		return c, fmt.Errorf("--output must be a new absolute directory path")
	}
	c.api = strings.TrimRight(strings.TrimSpace(c.api), "/")
	if err := validateCLIControllerURL(c.api); err != nil {
		return c, err
	}
	if c.api == "" {
		return c, fmt.Errorf("--api cannot be empty")
	}
	return c, nil
}

func runArchiveEvidenceCommand(args []string, stdout, stderr io.Writer) int {
	c, err := parseArchiveEvidenceArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "archive evidence configuration error:", err)
		return exitCodeUsageOrConfiguration
	}
	if c.help {
		fmt.Fprintln(stdout, archiveEvidenceUsage())
		return exitCodeSuccess
	}
	if err := os.Mkdir(c.output, 0o700); err != nil {
		fmt.Fprintln(stderr, "could not create new private evidence directory")
		return exitCodeUsageOrConfiguration
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	result := collectArchiveEvidence(ctx, NewApiClientWithAuth(c.api, c.apiKey, c.bearerToken), c.expected)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "could not encode archive evidence")
		return exitCodeControllerOrTransport
	}
	data = append(data, '\n')
	file, err := os.OpenFile(filepath.Join(c.output, "archive-evidence.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, "could not create archive evidence artifact")
		return exitCodeControllerOrTransport
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		fmt.Fprintln(stderr, "could not finish archive evidence artifact")
		return exitCodeControllerOrTransport
	}
	if _, err := stdout.Write(data); err != nil {
		return exitCodeControllerOrTransport
	}
	if !result.Passed {
		return exitCodeTestFailure
	}
	return exitCodeSuccess
}

func archiveEvidenceUsage() string {
	return `Archive evidence (apexctl archive evidence):
  --test             Expected test ID (required)
  --project          Expected project ID (required)
  --created-at       Expected RFC3339 run creation timestamp (required)
  --status           Expected terminal status: COMPLETE, FAILED, ABORTED [COMPLETE]
  --metadata-backend Timestamp identity precision: sqlite, postgres     [sqlite]
  --output           New absolute private evidence directory (required)
  --timeout          Total API observation timeout, at most 30m         [2m]
  --api              Controller URL (APEXLOAD_API or http://localhost:8080)
  --api-key          API key (or APEXLOAD_API_KEY)
  --bearer-token     Bearer token (or APEXLOAD_BEARER_TOKEN)

Writes archive-evidence.json and JSON stdout. Checks a terminal run, its archive
manifest and the controller's integrity report, then rechecks the run/catalog.
Exits nonzero for missing/unverified archives, changed identity, unknown/partial
source coverage, or best-effort collection. Does not wait for run completion.
A pass verifies the declared source snapshot, not zero measurement loss, SLA
success, independent artifact hashing or production failover qualification.`
}
