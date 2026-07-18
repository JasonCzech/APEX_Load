package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		os.Exit(2)
	}
	result, inspected, err := runGrafanaImport(context.Background(), args)
	if err != nil {
		if args.format == "json" {
			writeJSON(map[string]any{"status": "error", "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "Grafana import failed: %v\n", err)
		}
		os.Exit(1)
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
	next := func(i *int, flag string) (string, error) {
		if *i+1 >= len(raw) {
			return "", fmt.Errorf("%s requires a value", flag)
		}
		*i = *i + 1
		return raw[*i], nil
	}
	set := func(i *int, flag string, destination *string) error {
		value, err := next(i, flag)
		if err == nil {
			*destination = value
		}
		return err
	}
	for i := 0; i < len(raw); i++ {
		flag := raw[i]
		switch flag {
		case "--path", "-p":
			if err := set(&i, flag, &a.path); err != nil {
				return a, err
			}
		case "--project":
			if err := set(&i, flag, &a.projectID); err != nil {
				return a, err
			}
		case "--name":
			if err := set(&i, flag, &a.name); err != nil {
				return a, err
			}
		case "--api":
			if err := set(&i, flag, &a.api); err != nil {
				return a, err
			}
		case "--api-key":
			if err := set(&i, flag, &a.apiKey); err != nil {
				return a, err
			}
		case "--bearer-token":
			if err := set(&i, flag, &a.bearerToken); err != nil {
				return a, err
			}
		case "--format":
			if err := set(&i, flag, &a.format); err != nil {
				return a, err
			}
		case "--timeout":
			value, err := next(&i, flag)
			if err != nil {
				return a, err
			}
			a.timeout, err = time.ParseDuration(value)
			if err != nil || a.timeout <= 0 {
				return a, errors.New("--timeout must be a positive duration")
			}
		case "--binding":
			value, err := next(&i, flag)
			if err != nil {
				return a, err
			}
			name, bound, ok := strings.Cut(value, "=")
			if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(bound) == "" {
				return a, errors.New("--binding must use name=value")
			}
			name = strings.TrimSpace(name)
			if _, duplicate := a.bindings[name]; duplicate {
				return a, fmt.Errorf("--binding %s was supplied more than once", name)
			}
			a.bindings[name] = strings.TrimSpace(bound)
		case "--inspect-only":
			a.inspectOnly = true
		default:
			return a, fmt.Errorf("unknown Grafana import flag %q", flag)
		}
	}
	a.path, a.projectID, a.name, a.api, a.format = strings.TrimSpace(a.path), strings.TrimSpace(a.projectID), strings.TrimSpace(a.name), strings.TrimRight(strings.TrimSpace(a.api), "/"), strings.ToLower(strings.TrimSpace(a.format))
	if a.path == "" || a.projectID == "" || a.api == "" {
		return a, errors.New("--path, --project, and --api (or APEXLOAD_API) are required")
	}
	if a.format != "text" && a.format != "json" {
		return a, errors.New("--format must be text or json")
	}
	return a, nil
}

func runGrafanaImport(ctx context.Context, args grafanaImportArgs) (map[string]any, *grafanaCLIInspectResponse, error) {
	info, err := os.Stat(args.path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat dashboard: %w", err)
	}
	if info.Size() > maxGrafanaCLIImportBytes {
		return nil, nil, fmt.Errorf("dashboard exceeds the %d-byte import limit", maxGrafanaCLIImportBytes)
	}
	file, err := os.Open(args.path)
	if err != nil {
		return nil, nil, fmt.Errorf("open dashboard: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxGrafanaCLIImportBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read dashboard: %w", err)
	}
	if int64(len(content)) > maxGrafanaCLIImportBytes {
		return nil, nil, fmt.Errorf("dashboard exceeds the %d-byte import limit", maxGrafanaCLIImportBytes)
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
		return nil, &inspected, errors.New("dashboard has blocking import diagnostics")
	}
	if inspected.ImportSessionID == "" {
		return nil, &inspected, errors.New("inspect response omitted import_session_id")
	}
	if args.inspectOnly {
		return nil, &inspected, nil
	}
	for _, required := range inspected.RequiredBindings {
		if strings.TrimSpace(args.bindings[required]) == "" {
			return nil, &inspected, fmt.Errorf("required binding %q is missing", required)
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
