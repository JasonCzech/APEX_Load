package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
)

// runBrowserResourcesCommand is dispatched before the broad management flag
// parser: unsupported flags and all exact filters are refused before any I/O.
func runBrowserResourcesCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("analysis browser-resources", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://localhost:8080"), "controller URL")
	apiKey := flags.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key")
	bearerToken := flags.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token")
	id := flags.String("id", "", "canonical managed run ID")
	project := flags.String("project", "", "project scope")
	limit := flags.Int("limit", 50, "retained resource page size, 1 to 100")
	cursor := flags.String("cursor", "", "opaque retained-resource continuation")
	sortBy := flags.String("sort-by", "capture_order", "capture_order, duration_ms, native_start_ms or response_encoded_body")
	sortDir := flags.String("sort-dir", "asc", "asc or desc; unavailable values always last")
	filters := map[string]*string{}
	for _, field := range []string{"action_name", "group", "region", "resource_type", "failure_kind", "state", "source_generation", "request_id", "redirect_chain_id", "document_id", "initiating_action_id", "observation_action_id", "origin", "browser_engine", "device_profile"} {
		filters[field] = flags.String(strings.ReplaceAll(field, "_", "-"), "", "exact retained resource filter")
	}
	if err := browserResourceCLIUniqueFlags(flags, args); err != nil {
		return err
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := io.WriteString(out, browserResourceCLIHelp)
			return err
		}
		return errors.New("invalid browser-resources flags")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected browser-resources positional arguments")
	}
	if err := analysisCLIContextLive(ctx); err != nil {
		return err
	}
	if err := browserResourceCLIScopeAdmission(*id, *project); err != nil {
		return err
	}
	if err := validateCLIControllerURL(*endpoint); err != nil {
		return err
	}
	if strings.TrimSpace(*endpoint) == "" {
		return errors.New("--api requires an absolute http or https URL")
	}
	if strings.TrimSpace(*id) == "" {
		return errors.New("--id requires a canonical managed run ID")
	}
	values := url.Values{"limit": {strconv.Itoa(*limit)}, "sort_by": {*sortBy}, "sort_dir": {*sortDir}}
	if *project != "" {
		values.Set("project_id", *project)
	}
	if *cursor != "" {
		values.Set("cursor", *cursor)
	}
	for field, value := range filters {
		if *value != "" {
			values.Set(field, *value)
		}
	}
	// Explicit empty filters are errors; flag defaults alone are omitted.
	invalidEmpty := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "api", "api-key", "bearer-token", "id", "project", "limit", "sort-by", "sort-dir":
		default:
			if f.Value.String() == "" {
				invalidEmpty = true
			}
		}
	})
	if invalidEmpty {
		return errors.New("explicit resource filters must not be empty")
	}
	if _, err := api.ParseBrowserResourcePageQuery(values); err != nil {
		return errors.New("invalid browser-resources query")
	}
	client := NewApiClientWithAuth(strings.TrimSpace(*endpoint), *apiKey, *bearerToken)
	client.Client.Timeout = 12 * time.Second
	return client.readBrowserResources(ctx, *id, *project, values, out)
}
func (c *ApiClient) readBrowserResources(ctx context.Context, id, project string, query url.Values, out io.Writer) error {
	if err := analysisCLIContextLive(ctx); err != nil {
		return err
	}
	if err := browserResourceCLIScopeAdmission(id, project); err != nil {
		return err
	}
	if id == "" {
		return errors.New("canonical managed run ID is required")
	}
	admittedQuery, err := api.ParseBrowserResourcePageQuery(query)
	if err != nil {
		return errors.New("invalid browser-resources query")
	}
	effectiveProject := project
	if explicit := query.Get("project_id"); explicit != "" {
		if project != "" && explicit != project {
			return errors.New("browser-resources query scope does not match requested project")
		}
		effectiveProject = explicit
	}
	if c == nil || c.Client == nil || strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("invalid browser-resources controller URL")
	}
	if err := validateCLIControllerURL(c.BaseURL); err != nil {
		return err
	}
	path := "/api/v1/tests/" + url.PathEscape(id) + "/archive/browser-resources"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	request, err := c.newRequest(http.MethodGet, path, nil, "")
	if err != nil {
		return errors.New("invalid browser-resources controller URL")
	}
	client := *c.Client
	if client.Timeout <= 0 || client.Timeout > 12*time.Second {
		client.Timeout = 12 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("browser-resources controller request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("browser-resources controller returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, api.MaxBrowserResourcePageBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("browser-resources response could not be read")
	}
	if len(raw) > api.MaxBrowserResourcePageBytes {
		return errors.New("browser-resources response exceeds bounds")
	}
	page, err := api.DecodeBrowserResourcePage(raw)
	if err != nil {
		return errors.New("invalid browser-resources controller projection")
	}
	if len(page.Items) > admittedQuery.Limit {
		return errors.New("browser-resources controller projection exceeds requested page size")
	}
	// This command deliberately requires canonical IDs. A controller response for
	// another run never becomes the user's requested scope through alias guessing.
	if page.TestID != id || effectiveProject != "" && page.ProjectID != effectiveProject {
		return errors.New("browser-resources response scope does not match requested run or project")
	}
	if err := analysisCLIContextLive(ctx); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(page)
}

func browserResourceCLIScopeAdmission(id, project string) error {
	if err := analysisCLIScopeAdmission(id, project); err != nil {
		return err
	}
	if id == "." || id == ".." || id != strings.TrimSpace(id) || project != strings.TrimSpace(project) || strings.ContainsAny(id, "\r\n/\\?") || strings.ContainsAny(project, "\r\n") {
		return errors.New("invalid browser-resources scope")
	}
	return nil
}
func browserResourceCLIUniqueFlags(flags *flag.FlagSet, args []string) error {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		name := strings.TrimPrefix(arg, "-")
		name = strings.TrimPrefix(name, "-")
		name, value, hasValue := strings.Cut(name, "=")
		_ = value
		if flags.Lookup(name) == nil {
			continue
		}
		if seen[name] {
			return errors.New("browser-resources flags must not be repeated")
		}
		seen[name] = true
		if !hasValue {
			i++
		}
	}
	return nil
}

// Help deliberately lists credential environment variable names, never their
// values. flag.PrintDefaults would print the secret-bearing default values.
const browserResourceCLIHelp = `Usage: apexctl analysis browser-resources --id RUN_ID [options]

Read a bounded page of retained archive resource observations as JSON. Counts
refer to retained observations and do not establish browser capture coverage.

  --api URL           Controller URL (APEXLOAD_API or http://localhost:8080)
  --api-key KEY       Controller API key (or APEXLOAD_API_KEY)
  --bearer-token TOKEN Controller bearer token (or APEXLOAD_BEARER_TOKEN)
  --id RUN_ID         Canonical managed run ID
  --project PROJECT   Exact project scope
  --limit N           Page size, 1 to 100 (default 50)
  --cursor CURSOR     Opaque continuation from this exact query
  --sort-by FIELD     capture_order, duration_ms, native_start_ms,
                      response_encoded_body (default capture_order)
  --sort-dir ORDER    asc or desc; unavailable values always last (default asc)

Exact filters: --action-name, --group, --region, --resource-type, --failure-kind,
--state, --source-generation, --request-id, --redirect-chain-id, --document-id,
--initiating-action-id, --observation-action-id, --origin, --browser-engine,
--device-profile. Explicit empty filters and repeated flags are refused.
`
