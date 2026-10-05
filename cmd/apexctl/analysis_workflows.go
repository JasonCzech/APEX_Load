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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

func prepareCmd()  { workflowCommand("prepare") }
func monitorCmd()  { workflowCommand("monitor") }
func analysisCmd() { workflowCommand("analysis") }

func workflowCommand(kind string) {
	if err := executeWorkflowCommand(context.Background(), kind, os.Args[2:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var verdict *baselineVerdictError
		if errors.As(err, &verdict) {
			os.Exit(verdict.code)
		}
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

func executeWorkflowCommand(ctx context.Context, kind string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: apexctl %s <action> --api URL [--project ID] [--id ID] [--file JSON]", kind)
	}
	action := args[0]
	flags := flag.NewFlagSet(kind+" "+action, flag.ContinueOnError)
	flags.SetOutput(out)
	endpoint := flags.String("api", "http://127.0.0.1:8080", "controller URL")
	project := flags.String("project", "", "project ID")
	id := flags.String("id", "", "test, scenario, source, or dataset ID")
	file := flags.String("file", "", "JSON request or Parquet input")
	fileID := flags.String("file-id", "", "file ID within an import")
	preparationID := flags.String("preparation-id", "", "ready preparation ID")
	revision := flags.String("revision", "", "expected revision when starting, renewing or deleting a source")
	selection := flags.String("capacity", "", "existing or ephemeral preparation capacity")
	spot := flags.String("spot", "off", "off, preferred, or required")
	floor := flags.Int("on-demand-floor", 0, "minimum on-demand ephemeral generators")
	ceiling := flags.Int("spot-ceiling", 0, "maximum spot percentage")
	mode := flags.String("mode", "live", "live or archive processor evidence")
	group := flags.String("group", "", "processor execution group")
	sourceID := flags.String("source-id", "", "immutable monitor or processor source identity")
	entityID := flags.String("entity-id", "", "approved management entity identity")
	cursor := flags.String("cursor", "", "opaque management receipt cursor")
	from := flags.String("from", "", "inclusive management capture timestamp (RFC3339)")
	to := flags.String("to", "", "exclusive management capture timestamp (RFC3339)")
	defaultLimit := 100
	if kind == "monitor" && action == "management" {
		defaultLimit = 20
	}
	limit := flags.Int("limit", defaultLimit, "monitor page size (management/evidence: 1 to 100; series: 1 to 999)")
	at := flags.String("at", "", "dependency observation timestamp (RFC3339)")
	failOnRegression := flags.Bool("fail-on-regression", false, "fail CI for regression or unavailable baseline evidence")
	ttl := flags.Duration("ttl", 30*time.Minute, "readiness lifetime")
	metric := flags.String("metric", "", "monitor metric name filter")
	evidenceKind := flags.String("kind", "", "diagnostic kind filter: event, violation, snapshot, log, transaction, target or alert")
	afterKey := flags.String("after-key", "", "monitor evidence pagination cursor")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *failOnRegression && (kind != "analysis" || action != "baseline") {
		return errors.New("--fail-on-regression requires analysis baseline")
	}
	client := NewApiClient(*endpoint)
	client.Client.Timeout = 2 * time.Minute
	var body []byte
	method, path := http.MethodGet, ""
	if *file != "" && action != "upload" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		body, err = io.ReadAll(io.LimitReader(f, 2<<20+1))
		if err != nil {
			return err
		}
		if len(body) > 2<<20 || !json.Valid(body) {
			return errors.New("request file must contain JSON no larger than 2 MiB")
		}
	}
	requireID := func() error {
		if *id == "" {
			return errors.New("--id is required")
		}
		return nil
	}
	switch kind {
	case "prepare":
		if err := requireID(); err != nil {
			return err
		}
		path = "/api/v1/tests/" + url.PathEscape(*id) + "/preparation"
		switch action {
		case "create":
			method = http.MethodPost
			policy := compute.CapacityPolicy{Selection: *selection, Spot: *spot, OnDemandFloor: *floor, SpotCeilingPercent: *ceiling}
			if err := policy.Validate(); err != nil {
				return err
			}
			body, _ = json.Marshal(map[string]any{"ttl_seconds": int(ttl.Seconds()), "capacity": policy})
		case "show":
		case "renew":
			version, err := strconv.ParseInt(*revision, 10, 64)
			if err != nil || version < 1 || *preparationID == "" {
				return errors.New("--preparation-id and numeric --revision are required")
			}
			method = http.MethodPost
			body, _ = json.Marshal(map[string]any{"preparation_id": *preparationID, "revision": version, "ttl_seconds": int(ttl.Seconds())})
		case "cancel":
			method = http.MethodDelete
		case "start":
			if *preparationID == "" || *revision == "" {
				return errors.New("--preparation-id and --revision are required")
			}
			method = http.MethodPost
			path = "/api/v1/tests/" + url.PathEscape(*id) + "/start"
			body, _ = json.Marshal(map[string]string{"preparation_id": *preparationID, "expected_preparation_revision": *revision})
		default:
			return errors.New("prepare actions: create, show, renew, cancel, start")
		}
	case "monitor":
		switch action {
		case "sources", "create-source":
			if *project == "" {
				return errors.New("--project is required")
			}
			path = "/api/v1/projects/" + url.PathEscape(*project) + "/monitor-sources"
			if action == "create-source" {
				method = http.MethodPost
			}
		case "update-source", "delete-source":
			if *project == "" {
				return errors.New("--project is required")
			}
			if err := requireID(); err != nil {
				return err
			}
			path = "/api/v1/projects/" + url.PathEscape(*project) + "/monitor-sources/" + url.PathEscape(*id)
			if action == "delete-source" {
				version, err := strconv.ParseInt(*revision, 10, 64)
				if err != nil || version < 1 {
					return errors.New("--revision must be a positive source revision")
				}
				method, body = http.MethodDelete, nil
				path += "?" + url.Values{"revision": {strconv.FormatInt(version, 10)}}.Encode()
			} else {
				var source struct {
					Revision int64 `json:"revision"`
				}
				if err := json.Unmarshal(body, &source); err != nil || source.Revision < 1 || source.Revision >= 1<<62 {
					return errors.New("--file must contain a source object with a positive revision")
				}
				method = http.MethodPut
			}
		case "create-run":
			method, path = http.MethodPost, "/api/v1/tests"
			if len(body) == 0 {
				return errors.New("--file JSON test configuration with monitor_bindings and goal_config.monitor_rules is required")
			}
			var request map[string]json.RawMessage
			if json.Unmarshal(body, &request) != nil {
				return errors.New("test configuration must be a JSON object")
			}
			if *project != "" {
				if existing := request["project_id"]; existing != nil && string(existing) != strconv.Quote(*project) {
					return errors.New("--project does not match the test configuration")
				}
				request["project_id"], _ = json.Marshal(*project)
				body, _ = json.Marshal(request)
			}
		case "bindings", "bind":
			if err := requireID(); err != nil {
				return err
			}
			path = "/api/v1/scenarios/" + url.PathEscape(*id) + "/monitor-bindings"
			if action == "bind" {
				method = http.MethodPut
			}
		case "collection", "release-collection":
			if err := requireID(); err != nil {
				return err
			}
			if *project == "" {
				return errors.New("--project is required")
			}
			path = "/api/v1/projects/" + url.PathEscape(*project) + "/monitor-sources/" + url.PathEscape(*id) + "/management-state"
			if action == "release-collection" {
				method = http.MethodPost
			}
		case "management":
			if err := requireID(); err != nil {
				return err
			}
			if len(*sourceID) > 256 || len(*entityID) > 64 || len(*cursor) > 2048 || *limit < 1 || *limit > 100 {
				return errors.New("management query exceeds limits")
			}
			values := url.Values{"limit": {strconv.Itoa(*limit)}}
			for key, value := range map[string]string{"source_id": *sourceID, "entity_id": *entityID, "cursor": *cursor} {
				if value != "" {
					values.Set(key, value)
				}
			}
			var bounds [2]time.Time
			for i, raw := range []string{*from, *to} {
				if raw == "" {
					continue
				}
				parsed, err := time.Parse(time.RFC3339Nano, raw)
				if err != nil {
					return errors.New("--from and --to must be RFC3339 timestamps")
				}
				bounds[i] = parsed.UTC()
				values.Set([]string{"from", "to"}[i], bounds[i].Format(time.RFC3339Nano))
			}
			if !bounds[0].IsZero() && !bounds[1].IsZero() && !bounds[1].After(bounds[0]) {
				return errors.New("management query requires --to after --from")
			}
			path = "/api/v1/tests/" + url.PathEscape(*id) + "/monitor-management?" + values.Encode()
		case "dependencies":
			if err := requireID(); err != nil {
				return err
			}
			path = "/api/v1/tests/" + url.PathEscape(*id) + "/dependency-evidence"
			if *at != "" {
				parsed, err := time.Parse(time.RFC3339Nano, *at)
				if err != nil {
					return errors.New("--at must be an RFC3339 timestamp")
				}
				path += "?at=" + url.QueryEscape(parsed.UTC().Format(time.RFC3339Nano))
			}
		case "run":
			if err := requireID(); err != nil {
				return err
			}
			path = "/api/v1/tests/" + url.PathEscape(*id) + "/monitors"
		case "series", "evidence":
			if err := requireID(); err != nil {
				return err
			}
			maxLimit := 999
			if action == "evidence" {
				maxLimit = 100
			}
			if *limit < 1 || *limit > maxLimit {
				return fmt.Errorf("--limit for %s must be between 1 and %d", action, maxLimit)
			}
			query := url.Values{"limit": {strconv.Itoa(*limit)}}
			if *sourceID != "" {
				query.Set("source_id", *sourceID)
			}
			if *afterKey != "" {
				query.Set("after_key", *afterKey)
			}
			path = "/api/v1/tests/" + url.PathEscape(*id) + "/monitor-" + action
			if action == "series" {
				if *metric != "" {
					query.Set("metric", *metric)
				}
			} else {
				if *evidenceKind != "" {
					if !strings.Contains("|event|violation|snapshot|log|transaction|target|alert|", "|"+*evidenceKind+"|") {
						return errors.New("unsupported diagnostic --kind")
					}
					query.Set("kind", *evidenceKind)
				}
				if *afterKey != "" {
					query.Set("after_key", *afterKey)
				}
			}
			path += "?" + query.Encode()
		default:
			return errors.New("monitor actions: sources, create-source, update-source, delete-source, bindings, bind, create-run, run, series, evidence, dependencies, management, collection, release-collection")
		}
		if method != http.MethodGet && method != http.MethodDelete && len(body) == 0 {
			return errors.New("--file JSON configuration is required")
		}
	case "analysis":
		switch action {
		case "progress":
			if err := requireID(); err != nil {
				return err
			}
			if *mode != "live" && *mode != "archive" {
				return errors.New("--mode must be live or archive")
			}
			if len(*group) > 256 || len(*sourceID) > 256 {
				return errors.New("processor group or source identity exceeds limit")
			}
			values := url.Values{"mode": {*mode}}
			if *group != "" {
				values.Set("group", *group)
			}
			if *sourceID != "" {
				values.Set("source_id", *sourceID)
			}
			path = "/api/v1/tests/" + url.PathEscape(*id) + "/messaging-progress?" + values.Encode()
		case "baseline":
			if err := requireID(); err != nil {
				return err
			}
			if len(body) == 0 {
				return errors.New("--file JSON baseline request is required")
			}
			method, path = http.MethodPost, "/api/v1/tests/"+url.PathEscape(*id)+"/baseline-comparisons"
		case "list", "create", "capabilities", "compare", "mappings", "save-mapping", "comparisons", "save-comparison":
			if *project == "" {
				return errors.New("--project is required")
			}
			path = "/api/v1/analysis/imports?project_id=" + url.QueryEscape(*project)
			if action == "capabilities" {
				path = "/api/v1/analysis/capabilities?project_id=" + url.QueryEscape(*project)
			}
			if action == "compare" {
				path = "/api/v1/analysis/compare?project_id=" + url.QueryEscape(*project)
			}
			if action == "mappings" || action == "save-mapping" {
				path = "/api/v1/analysis/mappings?project_id=" + url.QueryEscape(*project)
			}
			if action == "comparisons" || action == "save-comparison" {
				path = "/api/v1/analysis/comparisons?project_id=" + url.QueryEscape(*project)
			}
			if action == "create" || action == "compare" || action == "save-mapping" || action == "save-comparison" {
				method = http.MethodPost
				if len(body) == 0 {
					return errors.New("--file JSON import descriptor is required")
				}
			}
		default:
			if err := requireID(); err != nil {
				return err
			}
			path = "/api/v1/analysis/imports/" + url.PathEscape(*id)
			switch action {
			case "show":
			case "cancel":
				method = http.MethodDelete
			case "commit":
				method = http.MethodPost
				path += "/commit"
			case "map":
				method = http.MethodPut
				path += "/mapping"
				if len(body) == 0 {
					return errors.New("--file JSON mapping is required")
				}
			case "preview":
				if *fileID == "" {
					return errors.New("--file-id is required")
				}
				path += "/files/" + url.PathEscape(*fileID) + "/preview"
			case "upload":
				return client.uploadAnalysisFile(ctx, *id, *fileID, *file, out)
			case "associate":
				method = http.MethodPut
				path = "/api/v1/analysis/datasets/" + url.PathEscape(*id) + "/association"
				if len(body) == 0 {
					return errors.New("--file JSON association with run_id and revision is required")
				}
			case "query":
				method = http.MethodPost
				path = "/api/v1/analysis/datasets/" + url.PathEscape(*id) + "/series"
				if len(body) == 0 {
					return errors.New("--file JSON query is required")
				}
			default:
				return errors.New("analysis actions: progress, baseline, capabilities, list, create, show, upload, preview, map, commit, cancel, query, associate, compare, mappings, save-mapping, comparisons, save-comparison")
			}
		}
	default:
		return errors.New("unknown workflow")
	}
	response, err := client.workflowRequest(ctx, method, path, bytes.NewReader(body), "application/json")
	if err != nil {
		if *failOnRegression {
			return &baselineVerdictError{exitCodeControllerOrTransport, err.Error()}
		}
		return err
	}
	if len(response) > 0 {
		_, err = fmt.Fprintln(out, string(response))
	}
	if err == nil && *failOnRegression {
		return checkBaselineVerdict(response)
	}
	return err
}

type baselineVerdictError struct {
	code    int
	message string
}

func (e *baselineVerdictError) Error() string { return e.message }
func checkBaselineVerdict(response []byte) error {
	var snapshot struct {
		Verdict *struct {
			Status      string `json:"status"`
			Unavailable int    `json:"unavailable"`
			Evaluated   int    `json:"evaluated"`
		} `json:"verdict"`
	}
	if err := json.Unmarshal(response, &snapshot); err != nil || snapshot.Verdict == nil {
		return &baselineVerdictError{exitCodeControllerOrTransport, "Baseline verdict unavailable"}
	}
	if snapshot.Verdict.Status == "regressed" {
		return &baselineVerdictError{exitCodeTestFailure, "Baseline regression detected"}
	}
	if snapshot.Verdict.Status != "passed" || snapshot.Verdict.Unavailable != 0 || snapshot.Verdict.Evaluated < 1 {
		return &baselineVerdictError{exitCodeControllerOrTransport, "Baseline evidence not evaluated"}
	}
	return nil
}

func (c *ApiClient) workflowRequest(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, error) {
	req, err := c.newRequest(method, path, body, contentType)
	if err != nil {
		return nil, err
	}
	client := *c.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 {
		return nil, errors.New("controller response exceeds 8 MiB")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("controller returned %s: %s", res.Status, data)
	}
	return data, nil
}

func (c *ApiClient) uploadAnalysisFile(ctx context.Context, id, fileID, path string, out io.Writer) error {
	if path == "" || fileID == "" {
		return errors.New("--file and --file-id are required")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Parquet input must be a regular file")
	}
	base := "/api/v1/analysis/imports/" + url.PathEscape(id)
	data, err := c.workflowRequest(ctx, http.MethodGet, base, nil, "")
	if err != nil {
		return err
	}
	var in storage.AnalysisImport
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	var selected *storage.AnalysisImportFile
	for i := range in.Files {
		if in.Files[i].ID == fileID {
			selected = &in.Files[i]
			break
		}
	}
	if selected == nil || selected.Size != info.Size() || filepath.Base(selected.RelativePath) != filepath.Base(path) {
		return errors.New("file does not match the import descriptor")
	}
	const chunkSize = int64(4 << 20)
	// Replay acknowledged prefixes: the server's digest check verifies that a
	// resumed upload uses the same immutable input without buffering the file.
	for offset := int64(0); offset < info.Size(); offset += chunkSize {
		length := min(chunkSize, info.Size()-offset)
		_, err := c.workflowRequest(ctx, http.MethodPut, base+"/files/"+url.PathEscape(fileID)+fmt.Sprintf("?offset=%d", offset), io.NewSectionReader(file, offset, length), "application/octet-stream")
		if err != nil {
			return fmt.Errorf("upload offset %d: %w", offset, err)
		}
		if _, err = fmt.Fprintf(out, "%s %d/%d bytes\n", selected.RelativePath, offset+length, info.Size()); err != nil {
			return err
		}
	}
	return nil
}
