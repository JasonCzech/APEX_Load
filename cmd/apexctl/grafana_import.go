package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	maxGrafanaCLIImportBytes   int64 = 8 << 20
	maxGrafanaCLIResponseBytes int64 = 16 << 20
)

type grafanaImportArgs struct {
	path, projectID, name, api, apiKey, bearerToken, format string
	timeout                                                 time.Duration
	bindings                                                map[string]string
	inspectOnly                                             bool
	help                                                    bool
}

type grafanaCLIInspectResponse struct {
	ImportSessionID  string           `json:"import_session_id"`
	Dashboard        map[string]any   `json:"dashboard"`
	RequiredBindings []string         `json:"required_bindings"`
	ReviewSections   []map[string]any `json:"review_sections"`
	HasBlockers      bool             `json:"has_blockers"`
	Stats            map[string]any   `json:"stats"`
}

func grafanaImportCmd() {
	args, err := parseGrafanaImportArgs(os.Args[3:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n\n%s\n", err, grafanaImportUsage())
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if args.help {
		fmt.Println(grafanaImportUsage())
		return
	}
	result, inspected, err := runGrafanaImport(context.Background(), args)
	if err != nil {
		if args.format == "json" {
			writeJSON(map[string]any{"status": "error", "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "Grafana import failed: %v\n", err)
		}
		os.Exit(importCommandExitCode(err))
	}
	if args.inspectOnly {
		if args.format == "json" {
			writeJSON(inspected)
		} else {
			fmt.Printf("Dashboard is importable; %d binding(s) required.\n", len(inspected.RequiredBindings))
		}
		return
	}
	if args.format == "json" {
		writeJSON(map[string]any{"status": "success", "dashboard": result})
		return
	}
	fmt.Printf("Imported Grafana dashboard %q (%v)\n", result["name"], result["id"])
}

func parseGrafanaImportArgs(raw []string) (grafanaImportArgs, error) {
	a := grafanaImportArgs{api: strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_API")), "/"), apiKey: strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")), bearerToken: strings.TrimSpace(os.Getenv("APEXLOAD_BEARER_TOKEN")), format: "text", timeout: defaultCLIImportTimeout, bindings: map[string]string{}}
	flags := flag.NewFlagSet("apexctl import grafana", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&a.help, "help", false, "show help")
	flags.BoolVar(&a.help, "h", false, "show help")
	flags.StringVar(&a.path, "path", "", "dashboard path")
	flags.StringVar(&a.path, "p", "", "dashboard path")
	flags.StringVar(&a.projectID, "project", "", "project ID")
	flags.StringVar(&a.name, "name", "", "dashboard name")
	flags.StringVar(&a.api, "api", a.api, "controller URL")
	flags.StringVar(&a.apiKey, "api-key", a.apiKey, "API key")
	flags.StringVar(&a.bearerToken, "bearer-token", a.bearerToken, "bearer token")
	flags.StringVar(&a.format, "format", a.format, "output format")
	flags.DurationVar(&a.timeout, "timeout", a.timeout, "HTTP timeout")
	flags.Var(uniqueCLIAssignments{values: a.bindings, flagName: "--binding"}, "binding", "binding name=value")
	flags.BoolVar(&a.inspectOnly, "inspect-only", false, "inspect without committing")
	if err := flags.Parse(raw); err != nil {
		return a, err
	}
	if flags.NArg() != 0 {
		return a, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	a.path, a.projectID, a.name, a.api, a.format = strings.TrimSpace(a.path), strings.TrimSpace(a.projectID), strings.TrimSpace(a.name), strings.TrimRight(strings.TrimSpace(a.api), "/"), strings.ToLower(strings.TrimSpace(a.format))
	a.apiKey, a.bearerToken = strings.TrimSpace(a.apiKey), strings.TrimSpace(a.bearerToken)
	if a.help {
		return a, nil
	}
	if a.path == "" || a.projectID == "" || a.api == "" {
		return a, errors.New("--path, --project, and --api (or APEXLOAD_API) are required")
	}
	if a.timeout <= 0 {
		return a, errors.New("--timeout must be a positive duration")
	}
	if err := validateCLIOutputFormat(a.format); err != nil {
		return a, err
	}
	if err := validateCLIControllerURL(a.api); err != nil {
		return a, err
	}
	return a, nil
}

type uniqueCLIAssignments struct {
	values   map[string]string
	flagName string
}

func (a uniqueCLIAssignments) String() string { return "name=value" }

func (a uniqueCLIAssignments) Set(raw string) error {
	name, value, ok := strings.Cut(raw, "=")
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if !ok || name == "" || value == "" {
		return fmt.Errorf("%s must use name=value", a.flagName)
	}
	if _, duplicate := a.values[name]; duplicate {
		return fmt.Errorf("%s %s was supplied more than once", a.flagName, name)
	}
	a.values[name] = value
	return nil
}

func runGrafanaImport(ctx context.Context, args grafanaImportArgs) (map[string]any, *grafanaCLIInspectResponse, error) {
	info, err := os.Stat(args.path)
	if err != nil {
		return nil, nil, cliInputErrorf("stat dashboard: %v", err)
	}
	if info.Size() > maxGrafanaCLIImportBytes {
		return nil, nil, cliInputErrorf("dashboard exceeds the %d-byte import limit", maxGrafanaCLIImportBytes)
	}
	file, err := os.Open(args.path)
	if err != nil {
		return nil, nil, cliInputErrorf("open dashboard: %v", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxGrafanaCLIImportBytes+1))
	if err != nil {
		return nil, nil, cliInputErrorf("read dashboard: %v", err)
	}
	if int64(len(content)) > maxGrafanaCLIImportBytes {
		return nil, nil, cliInputErrorf("dashboard exceeds the %d-byte import limit", maxGrafanaCLIImportBytes)
	}
	client := NewApiClientWithAuth(args.api, args.apiKey, args.bearerToken)
	client.Client.Timeout = args.timeout
	inspectPayload, _ := json.Marshal(map[string]any{"project_id": args.projectID, "name": args.name, "content": string(content)})
	resp, err := client.doContext(ctx, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana", bytes.NewReader(inspectPayload), "application/json")
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, brunoCLIResponseError("inspect Grafana dashboard", resp)
	}
	var inspected grafanaCLIInspectResponse
	if err := decodeGrafanaCLIJSON(resp.Body, &inspected); err != nil {
		return nil, nil, err
	}
	if inspected.HasBlockers {
		return nil, &inspected, cliInputErrorf("dashboard has blocking import diagnostics")
	}
	if inspected.ImportSessionID == "" {
		return nil, &inspected, errors.New("inspect response omitted import_session_id")
	}
	if args.inspectOnly {
		return nil, &inspected, nil
	}
	for _, required := range inspected.RequiredBindings {
		if strings.TrimSpace(args.bindings[required]) == "" {
			return nil, &inspected, cliInputErrorf("required binding %q is missing", required)
		}
	}
	commitPayload, _ := json.Marshal(map[string]any{"project_id": args.projectID, "import_session_id": inspected.ImportSessionID, "name": args.name, "bindings": args.bindings})
	resp, err = client.doContext(ctx, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit", bytes.NewReader(commitPayload), "application/json")
	if err != nil {
		return nil, &inspected, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &inspected, brunoCLIResponseError("commit Grafana dashboard", resp)
	}
	var result map[string]any
	if err := decodeGrafanaCLIJSON(resp.Body, &result); err != nil {
		return nil, &inspected, err
	}
	return result, &inspected, nil
}

func decodeGrafanaCLIJSON(reader io.Reader, destination any) error {
	payload, err := io.ReadAll(io.LimitReader(reader, maxGrafanaCLIResponseBytes+1))
	if err != nil {
		return err
	}
	if int64(len(payload)) > maxGrafanaCLIResponseBytes {
		return fmt.Errorf("controller response exceeds %d bytes", maxGrafanaCLIResponseBytes)
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		return fmt.Errorf("decode controller response: %w", err)
	}
	return nil
}

func grafanaImportUsage() string {
	return `Usage:
  apexctl import grafana --api <controller> --path dashboard.json --project <project-id> [options]

Options:
  --binding name=value    Bind a required Grafana variable (repeatable)
  --inspect-only          Validate and print review data without committing
  --name name             Override the stored dashboard name
  --format text|json      Output format
  --timeout duration      HTTP timeout (default 30m)
  --api-key key           API key authentication
  --bearer-token token    Bearer-token authentication`
}
