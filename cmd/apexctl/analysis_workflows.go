package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
func rcaCmd()      { workflowCommand("rca") }

func workflowCommand(kind string) {
	if err := executeWorkflowCommand(context.Background(), kind, os.Args[2:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var verdict *verdictError
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
	if kind == "rca" && action == "eval" {
		return executeRCAEval(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "browser-resources" {
		return runBrowserResourcesCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "browser-documents" {
		return executeBrowserDocumentCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "browser-har" {
		return executeBrowserHARCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "telemetry-reevaluate" {
		return executeTelemetryReevaluateCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "telemetry-policy-change" {
		return executeTelemetryPolicyChangeCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "journeys" {
		return executeAnalysisJourneysCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "browser-findings" {
		return executeAnalysisBrowserFindingsCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "interactions" {
		return executeAnalysisInteractionsCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "har-entry" {
		return executeAnalysisHAREntryCommand(ctx, args[1:], out)
	}
	if kind == "analysis" && action == "policies" {
		return executeAnalysisPoliciesCommand(ctx, args[1:], out)
	}
	flags := flag.NewFlagSet(kind+" "+action, flag.ContinueOnError)
	flags.SetOutput(out)
	endpoint := flags.String("api", "http://127.0.0.1:8080", "controller URL")
	project := flags.String("project", "", "project ID")
	id := flags.String("id", "", "test, scenario, source, or dataset ID")
	file := flags.String("file", "", "JSON request or Parquet input")
	fileID := flags.String("file-id", "", "file ID within an import")
	preparationID := flags.String("preparation-id", "", "ready preparation ID")
	revision := flags.String("revision", "", "expected revision when starting, renewing or deleting a source; a panel's saved revision to show (panel) or its expected revision (delete-panel)")
	selection := flags.String("capacity", "", "existing or ephemeral preparation capacity")
	spot := flags.String("spot", "off", "off, preferred, or required")
	floor := flags.Int("on-demand-floor", 0, "minimum on-demand ephemeral generators")
	ceiling := flags.Int("spot-ceiling", 0, "maximum spot percentage")
	mode := flags.String("mode", "live", "live or archive evidence")
	group := flags.String("group", "", "processor execution group")
	sourceID := flags.String("source-id", "", "immutable monitor or processor source identity")
	entityID := flags.String("entity-id", "", "approved management entity identity")
	cursor := flags.String("cursor", "", "opaque management receipt cursor")
	from := flags.String("from", "", "inclusive capture timestamp (evidence requires UTC RFC3339)")
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
	evidenceKind := flags.String("kind", "", "diagnostic kind filter: event, violation, snapshot, log, transaction, target or alert; Dynatrace also supports problem, span and trace")
	service := flags.String("service", "", "evidence service filter")
	severity := flags.String("severity", "", "evidence severity filter")
	traceID := flags.String("trace-id", "", "evidence trace ID filter")
	until := flags.String("until", "", "exclusive evidence end (UTC RFC3339)")
	afterKey := flags.String("after-key", "", "monitor evidence pagination cursor")
	analysisID := flags.String("analysis-id", "", "root-cause v2 analysis ID")
	enrich := flags.Bool("enrich", false, "capture a new root-cause evidence revision before analysis")
	baselineID := flags.String("baseline-id", "", "baseline run for root-cause analysis")
	wait := flags.Duration("wait", 0, "rca analyze, status and show: poll up to this long until the analysis ends (show: until an automatic analysis completes); at most 10m")
	outFile := flags.String("out", "", "stream output directly to this file path")
	population := flags.String("population", "", "analysis population (action, transaction, byte, message, browser_document, java_http_action)")
	aggregation := flags.String("aggregation", "", "analysis aggregation (avg, min, max, sum, count, rate, p50, p75, p90, p95, p99)")
	protocol := flags.String("protocol", "", "analysis protocol filter (http, grpc, websocket)")
	actionFilter := flags.String("action", "", "analysis action filter")
	bucketSeconds := flags.Int("bucket-seconds", 0, "analysis bucket width in seconds")
	startSeconds := flags.Int("start-seconds", 0, "analysis window start offset in seconds")
	endSeconds := flags.Int("end-seconds", 0, "analysis window end offset in seconds")
	liveBucketMode := flags.String("live-bucket-mode", "", "live bucket mode (merged or source)")
	chunkBytes := flags.Int64("chunk-bytes", 0, "upload chunk size in bytes (1024 to 4194304)")
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
	if *wait != 0 && (kind != "rca" || (action != "analyze" && action != "status" && action != "show")) {
		return errors.New("--wait requires rca analyze, status or show")
	}
	if *wait < 0 || *wait > maxRCAWait {
		return fmt.Errorf("--wait must be between 0 and %s", maxRCAWait)
	}
	if kind == "analysis" && analysisCLIIdentityCommand(action) {
		if err := analysisCLIScopeAdmission(*id, *project); err != nil {
			return err
		}
	}
	client := NewApiClient(*endpoint)
	client.Client.Timeout = 2 * time.Minute
	if kind == "analysis" && action == "telemetry-policy" {
		return client.readTelemetryPolicy(ctx, flags, *id, *project, *mode, out)
	}
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
	case "rca":
		if err := requireID(); err != nil {
			return err
		}
		base := "/api/v1/tests/" + url.PathEscape(*id) + "/archive/root-cause"
		switch action {
		case "analyze":
			method, path = http.MethodPost, base+"/analyses"
			body, _ = json.Marshal(map[string]any{"enrich": *enrich, "baseline_test_id": *baselineID})
		case "status", "bundle":
			if *analysisID == "" {
				return errors.New("--analysis-id is required")
			}
			path = base + "/analyses/" + url.PathEscape(*analysisID)
			if action == "bundle" {
				path += "/bundle"
			}
		case "show":
			path = base + "/v2"
		default:
			return errors.New("rca actions: analyze, status, show, bundle, eval")
		}
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
					if !strings.Contains("|event|violation|snapshot|log|transaction|target|alert|problem|span|trace|error|health|", "|"+*evidenceKind+"|") {
						return errors.New("unsupported diagnostic --kind")
					}
					query.Set("kind", *evidenceKind)
				}
				filter := storage.EvidenceHeadFilter{Kind: *evidenceKind, Service: *service, Severity: *severity, TraceID: *traceID}
				for key, value := range map[string]string{"service": *service, "severity": *severity, "trace_id": *traceID} {
					if value != "" {
						query.Set(key, value)
					}
				}
				for _, bound := range []struct {
					name, value string
					target      **time.Time
				}{{"from", *from, &filter.From}, {"until", *until, &filter.Until}} {
					if bound.value == "" {
						continue
					}
					parsed, err := time.Parse(time.RFC3339Nano, bound.value)
					if err != nil || !strings.HasSuffix(bound.value, "Z") {
						return fmt.Errorf("--%s must be a UTC RFC3339 timestamp ending in Z", bound.name)
					}
					parsed = parsed.UTC()
					*bound.target = &parsed
					query.Set(bound.name, parsed.Format(time.RFC3339Nano))
				}
				if err := filter.Validate(); err != nil || len(*sourceID) > 256 || len(*afterKey) > 64 {
					return errors.New("invalid evidence filters: use bounded values and an until timestamp after from")
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
		case "panels":
			// Saved panel definitions (dashboards) of a project.
			if *project == "" {
				return errors.New("--project is required")
			}
			path = "/api/v1/monitoring/dashboard-definitions?project_id=" + url.QueryEscape(*project)
		case "panel", "update-panel", "delete-panel", "replay-panel":
			if err := requireID(); err != nil {
				return err
			}
			if action == "replay-panel" {
				if len(body) > 0 {
					return errors.New("--file is not accepted by replay-panel")
				}
				targetOut, closeOut, err := openWorkflowOutput(*outFile, out)
				if err != nil {
					return err
				}
				defer closeOut()
				return client.replaySavedPanel(ctx, *project, *id, *revision, targetOut)
			}
			path = "/api/v1/monitoring/dashboard-definitions/" + url.PathEscape(*id)
			switch action {
			case "panel":
				// --revision reopens exactly that saved revision.
				if *revision != "" {
					if n, err := strconv.ParseInt(*revision, 10, 64); err != nil || n <= 0 {
						return errors.New("--revision must be a positive integer")
					}
					path += "?revision=" + url.QueryEscape(*revision)
				}
			case "update-panel":
				method = http.MethodPatch
				if len(body) == 0 {
					return errors.New("--file JSON patch with the expected revision is required")
				}
			case "delete-panel":
				if n, err := strconv.ParseInt(*revision, 10, 64); err != nil || n <= 0 {
					return errors.New("--revision (the revision being deleted) is required")
				}
				method, path = http.MethodDelete, path+"?revision="+url.QueryEscape(*revision)
			}
		case "save-panel":
			if len(body) == 0 {
				return errors.New("--file JSON panel definition with project_id is required")
			}
			method, path = http.MethodPost, "/api/v1/monitoring/dashboard-definitions"
		case "run-query", "evidence-query", "byte-query":
			if err := requireID(); err != nil {
				return err
			}
			if action == "byte-query" && *mode != "live" && *mode != "archive" {
				return errors.New("--mode must be live or archive")
			}
			if len(body) == 0 {
				if *metric == "" {
					return errors.New("--file JSON query is required")
				}
				built, err := buildAnalysisFlagQuery(action, *mode, *population, *metric, *aggregation, *protocol, *actionFilter, *startSeconds, *endSeconds, *bucketSeconds, *liveBucketMode)
				if err != nil {
					return err
				}
				body = built
			}
			method = http.MethodPost
			if action == "evidence-query" || (action == "byte-query" && *mode == "live") {
				path = "/api/v1/tests/" + url.PathEscape(*id) + "/native-evidence"
			} else {
				path = "/api/v1/tests/" + url.PathEscape(*id) + "/archive/native-series"
			}
		case "list", "create", "capabilities", "catalog", "compare", "mappings", "save-mapping", "comparisons", "save-comparison":
			if *project == "" {
				return errors.New("--project is required")
			}
			path = "/api/v1/analysis/imports?project_id=" + url.QueryEscape(*project)
			if action == "capabilities" || action == "catalog" {
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
			case "precision-review":
				method = http.MethodPost
				path += "/precision-review"
				if len(body) == 0 {
					return errors.New("--file JSON with revision and mapping is required")
				}
			case "preview":
				if *fileID == "" {
					return errors.New("--file-id is required")
				}
				path += "/files/" + url.PathEscape(*fileID) + "/preview"
			case "upload":
				return client.uploadAnalysisFile(ctx, *id, *fileID, *file, *chunkBytes, out)
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
				return errors.New("analysis actions: telemetry-policy, telemetry-policy-change, telemetry-reevaluate, browser-documents, browser-resources, browser-har, progress, baseline, catalog, capabilities, list, create, show, upload, preview, precision-review, map, commit, cancel, query, associate, compare, mappings, save-mapping, comparisons, save-comparison, panels, panel, save-panel, update-panel, delete-panel, replay-panel, run-query, evidence-query, byte-query")
			}
		}
	default:
		return errors.New("unknown workflow")
	}
	if kind == "analysis" {
		if err := client.preflightAnalysisIdentity(ctx, action, *project, *id, body); err != nil {
			return err
		}
	}
	targetOut, closeOut, err := openWorkflowOutput(*outFile, out)
	if err != nil {
		return err
	}
	defer closeOut()
	out = targetOut
	if kind == "analysis" && action == "panel" && *revision != "" {
		// A controller that predates saved revisions ignores ?revision and
		// answers the current definition: refuse it rather than print it as
		// the saved revision.
		response, err := client.workflowRequest(ctx, method, path, nil, "")
		if err != nil {
			return err
		}
		if err := checkPanelRevision(response, *revision); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(response))
		return err
	}
	if kind == "rca" && *wait > 0 {
		response, err := client.waitForRCAAnalysis(ctx, method, path, body, *id, *wait)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(response))
		return err
	}
	if !*failOnRegression {
		// Results stream to the output as they arrive; nothing holds a
		// whole response in memory.
		return client.workflowStream(ctx, method, path, bytes.NewReader(body), "application/json", out)
	}
	response, err := client.workflowRequest(ctx, method, path, bytes.NewReader(body), "application/json")
	if err != nil {
		if *failOnRegression {
			return &verdictError{exitCodeControllerOrTransport, err.Error()}
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

// maxRCAWait bounds rca --wait.
const maxRCAWait = 10 * time.Minute

// rcaWaitPoll is how often rca --wait polls.
var rcaWaitPoll = 2 * time.Second

// rcaWaitState is what rca --wait reads from an analysis response.
type rcaWaitState struct {
	AnalysisID  string `json:"analysis_id"`
	State       string `json:"state"`
	AutoAnalyze bool   `json:"auto_analyze"`
}

// waitForRCAAnalysis makes an rca request and, while the analysis it answers
// with is queued or running (or, for show, while none has completed but the
// project analyzes every run automatically), polls until it ends or wait
// passes. It returns the last response; reaching the deadline is not an
// error, so a caller still sees the state. A poll that fails or answers with
// something unreadable is a controller or transport failure, and cancellation
// returns the context's error: the last response would otherwise read as the
// analysis's final state.
func (c *ApiClient) waitForRCAAnalysis(ctx context.Context, method, path string, body []byte, testID string, wait time.Duration) ([]byte, error) {
	response, err := c.workflowRequest(ctx, method, path, bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for polled := false; ; polled = true {
		var state rcaWaitState
		if err := json.Unmarshal(response, &state); err != nil {
			if polled {
				return nil, &verdictError{exitCodeControllerOrTransport, fmt.Sprintf("root-cause analysis poll returned an unreadable response: %v", err)}
			}
			return response, nil
		}
		pending := state.State == "queued" || state.State == "running"
		awaitingAutomatic := state.State == "none" && state.AutoAnalyze
		if (!pending && !awaitingAutomatic) || !time.Now().Before(deadline) {
			return response, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(rcaWaitPoll):
		}
		next := "/api/v1/tests/" + url.PathEscape(testID) + "/archive/root-cause/v2"
		if pending && state.AnalysisID != "" {
			next = "/api/v1/tests/" + url.PathEscape(testID) + "/archive/root-cause/analyses/" + url.PathEscape(state.AnalysisID)
		}
		latest, err := c.workflowRequest(ctx, http.MethodGet, next, nil, "")
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			last := fmt.Sprintf("last state %q", state.State)
			if state.AnalysisID != "" {
				last += ", analysis " + state.AnalysisID
			}
			return nil, &verdictError{exitCodeControllerOrTransport, fmt.Sprintf("root-cause analysis poll failed (%s): %v", last, err)}
		}
		response = latest
	}
}

// checkPanelRevision refuses a panel response that is not the revision asked
// for.
func checkPanelRevision(response []byte, requested string) error {
	var panel struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(response, &panel); err != nil {
		return fmt.Errorf("the panel response could not be read: %w", err)
	}
	want, err := strconv.ParseInt(requested, 10, 64)
	if err != nil {
		return fmt.Errorf("--revision must be a positive integer")
	}
	if panel.Revision != want {
		return fmt.Errorf("the controller returned revision %d, not the requested revision %d (a controller that predates saved revisions answers the current one)", panel.Revision, want)
	}
	return nil
}

type replayedSavedPanelItem struct {
	TabID    string                 `json:"tab_id"`
	PanelID  string                 `json:"panel_id"`
	Title    string                 `json:"title"`
	RunID    string                 `json:"run_id"`
	Query    storage.AnalyticsQuery `json:"query"`
	Response json.RawMessage        `json:"response"`
}

func sameReplayPanelRunInput(a, b storage.BrowserPanelRunInput) bool {
	if a.RunID != b.RunID || a.ManifestSHA256 != b.ManifestSHA256 || len(a.TupleIDs) != len(b.TupleIDs) || len(a.PolicyPins) != len(b.PolicyPins) {
		return false
	}
	for i := range a.TupleIDs {
		if a.TupleIDs[i] != b.TupleIDs[i] {
			return false
		}
	}
	for i := range a.PolicyPins {
		if a.PolicyPins[i] != b.PolicyPins[i] {
			return false
		}
	}
	return true
}

func sameReplayPanelInputs(a, b storage.BrowserPanelInputs) bool {
	if a.Version != b.Version || a.QuerySHA256 != b.QuerySHA256 || a.SelectionReceiptSHA256 != b.SelectionReceiptSHA256 || len(a.PolicyPins) != len(b.PolicyPins) {
		return false
	}
	for i := range a.PolicyPins {
		if a.PolicyPins[i] != b.PolicyPins[i] {
			return false
		}
	}
	if !sameReplayPanelRunInput(a.Current, b.Current) {
		return false
	}
	if (a.Baseline == nil) != (b.Baseline == nil) {
		return false
	}
	return a.Baseline == nil || sameReplayPanelRunInput(*a.Baseline, *b.Baseline)
}

func sameReplaySavedSourcePin(a, b storage.SavedPanelSourcePin) bool {
	if a.RunID != b.RunID || a.ManifestSHA256 != b.ManifestSHA256 || a.DatasetID != b.DatasetID || a.DatasetArtifactSHA256 != b.DatasetArtifactSHA256 || a.SourceID != b.SourceID || a.Protocol != b.Protocol || len(a.PolicyPins) != len(b.PolicyPins) {
		return false
	}
	for i := range a.PolicyPins {
		if a.PolicyPins[i] != b.PolicyPins[i] {
			return false
		}
	}
	return true
}

func sameReplaySavedPanelInputs(a, b storage.SavedPanelInputs) bool {
	if a.Version != b.Version || a.CatalogVersion != b.CatalogVersion || a.Alignment != b.Alignment || a.CoveragePolicy != b.CoveragePolicy || a.Population != b.Population || a.Metric != b.Metric || a.Aggregation != b.Aggregation || a.QuerySHA256 != b.QuerySHA256 || a.Drilldown != b.Drilldown || len(a.PolicyPins) != len(b.PolicyPins) {
		return false
	}
	for i := range a.PolicyPins {
		if a.PolicyPins[i] != b.PolicyPins[i] {
			return false
		}
	}
	if !sameReplaySavedSourcePin(a.Current, b.Current) {
		return false
	}
	if (a.Baseline == nil) != (b.Baseline == nil) || (a.Baseline != nil && !sameReplaySavedSourcePin(*a.Baseline, *b.Baseline)) {
		return false
	}
	if (a.Overlay == nil) != (b.Overlay == nil) || (a.Overlay != nil && !sameReplaySavedSourcePin(*a.Overlay, *b.Overlay)) {
		return false
	}
	if b.ComparisonID != "" && (a.ComparisonID != b.ComparisonID || a.ComparisonRevision != b.ComparisonRevision || a.ComparisonSHA256 != b.ComparisonSHA256) {
		return false
	}
	return true
}

func (c *ApiClient) replayComparisonPanel(ctx context.Context, project, panelID string, q storage.AnalyticsQuery) ([]byte, error) {
	p := *q.PanelInputs
	var compareReq map[string]any
	if p.ComparisonID != "" {
		rawDefs, err := c.workflowRequest(ctx, http.MethodGet, "/api/v1/analysis/comparisons?project_id="+url.QueryEscape(project), nil, "")
		if err != nil {
			return nil, err
		}
		var defs []storage.AnalysisDefinition
		if err := json.Unmarshal(rawDefs, &defs); err != nil {
			return nil, err
		}
		var matched *storage.AnalysisDefinition
		for i := range defs {
			if defs[i].ID == p.ComparisonID {
				matched = &defs[i]
				break
			}
		}
		if matched == nil {
			return nil, fmt.Errorf("missing_input: panel %q saved comparison definition %q was not found", panelID, p.ComparisonID)
		}
		sum := sha256.Sum256(matched.Definition)
		if matched.Revision != p.ComparisonRevision || hex.EncodeToString(sum[:]) != p.ComparisonSHA256 {
			return nil, fmt.Errorf("stale: panel %q saved comparison definition %q changed since save", panelID, p.ComparisonID)
		}
		if err := json.Unmarshal(matched.Definition, &compareReq); err != nil {
			return nil, err
		}
	} else {
		if p.Baseline == nil {
			return nil, fmt.Errorf("panel %q dataset replay requires a baseline pin or comparison_id", panelID)
		}
		anchorRunID := p.Current.RunID
		if anchorRunID == "" {
			anchorRunID = p.Baseline.RunID
		}
		if anchorRunID == "" {
			return nil, fmt.Errorf("panel %q dataset comparison replay requires comparison_id in panel_inputs to anchor the UTC comparison window", panelID)
		}
		rawRun, err := c.workflowRequest(ctx, http.MethodGet, "/api/v1/tests/"+url.PathEscape(anchorRunID), nil, "")
		if err != nil {
			return nil, err
		}
		var runMeta struct {
			StartTime time.Time  `json:"start_time"`
			StartedAt *time.Time `json:"started_at"`
		}
		if err := json.Unmarshal(rawRun, &runMeta); err != nil {
			return nil, err
		}
		anchor := runMeta.StartTime
		if anchor.IsZero() && runMeta.StartedAt != nil {
			anchor = *runMeta.StartedAt
		}
		if anchor.IsZero() {
			return nil, fmt.Errorf("panel %q comparison replay requires a started run or comparison_id to anchor the UTC comparison window", panelID)
		}
		from := anchor.UTC().Add(time.Duration(q.StartSeconds) * time.Second)
		to := anchor.UTC().Add(time.Duration(q.EndSeconds) * time.Second)
		left := map[string]any{
			"metric":      q.Metric,
			"aggregation": q.Aggregation,
		}
		if p.Current.DatasetID != "" {
			left["dataset_id"] = p.Current.DatasetID
			left["dataset_artifact_sha256"] = p.Current.DatasetArtifactSHA256
		} else {
			left["run_id"] = p.Current.RunID
			left["archive_manifest_sha256"] = p.Current.ManifestSHA256
		}
		if p.Current.SourceID != "" {
			left["source_id"] = p.Current.SourceID
		}
		if p.Current.Protocol != "" {
			left["protocol"] = p.Current.Protocol
		} else if proto := q.Filters["protocol"]; proto != "" {
			left["protocol"] = proto
		}
		right := map[string]any{
			"metric":      q.Metric,
			"aggregation": q.Aggregation,
		}
		if p.Baseline.DatasetID != "" {
			right["dataset_id"] = p.Baseline.DatasetID
			right["dataset_artifact_sha256"] = p.Baseline.DatasetArtifactSHA256
		} else {
			right["run_id"] = p.Baseline.RunID
			right["archive_manifest_sha256"] = p.Baseline.ManifestSHA256
		}
		if p.Baseline.SourceID != "" {
			right["source_id"] = p.Baseline.SourceID
		}
		if p.Baseline.Protocol != "" {
			right["protocol"] = p.Baseline.Protocol
		} else if proto := q.Filters["protocol"]; proto != "" {
			right["protocol"] = proto
		}
		if action := q.Filters["action"]; action != "" {
			left["transaction"] = action
			right["transaction"] = action
		}
		compareReq = map[string]any{
			"left":            left,
			"right":           right,
			"from":            from.Format(time.RFC3339),
			"to":              to.Format(time.RFC3339),
			"bucket_seconds":  q.BucketSeconds,
			"max_lag_seconds": p.Drilldown.MaxLagSeconds,
			"mode":            "overlay",
			"alignment":       p.Alignment,
			"coverage_policy": p.CoveragePolicy,
			"catalog_version": p.CatalogVersion,
		}
	}
	reqBytes, err := json.Marshal(compareReq)
	if err != nil {
		return nil, err
	}
	return c.workflowRequest(ctx, http.MethodPost, "/api/v1/analysis/compare?project_id="+url.QueryEscape(project), bytes.NewReader(reqBytes), "application/json")
}

func (c *ApiClient) replaySavedPanel(ctx context.Context, project, id, revision string, out io.Writer) error {
	path := "/api/v1/monitoring/dashboard-definitions/" + url.PathEscape(id)
	if revision != "" {
		if n, err := strconv.ParseInt(revision, 10, 64); err != nil || n <= 0 {
			return errors.New("--revision must be a positive integer")
		}
		path += "?revision=" + url.QueryEscape(revision)
	}
	rawDef, err := c.workflowRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	if revision != "" {
		if err := checkPanelRevision(rawDef, revision); err != nil {
			return err
		}
	}
	var def storage.DashboardDefinition
	if err := json.Unmarshal(rawDef, &def); err != nil {
		return fmt.Errorf("the panel response could not be read: %w", err)
	}
	if def.ID != id {
		return errors.New("controller returned a different dashboard definition")
	}
	if project != "" && def.ProjectID != "" && def.ProjectID != project {
		return fmt.Errorf("dashboard definition %q belongs to project %q, not %q", id, def.ProjectID, project)
	}
	effectiveProject := def.ProjectID
	if effectiveProject == "" {
		effectiveProject = project
	}
	var items []replayedSavedPanelItem
	for _, tab := range def.Tabs {
		for _, panel := range tab.Panels {
			if panel.Query == nil {
				continue
			}
			q := *panel.Query
			if q.PanelInputs != nil {
				storage.NormalizeSavedPanelQueryDefaults(&q)
				if err := storage.ValidateSavedPanelInputs(q); err != nil {
					return fmt.Errorf("panel %q has invalid or missing panel input pins: %w", panel.ID, err)
				}
				if len(items) >= 20 {
					return errors.New("saved dashboard definition exceeds 20 pinned panels")
				}
				runID := q.PanelInputs.Current.RunID
				var seriesRaw []byte
				if q.PanelInputs.Current.DatasetID != "" || (q.PanelInputs.Baseline != nil && q.PanelInputs.Baseline.DatasetID != "") || q.Population == "" {
					if runID == "" {
						runID = q.PanelInputs.Current.DatasetID
					}
					seriesRaw, err = c.replayComparisonPanel(ctx, effectiveProject, panel.ID, q)
				} else {
					if err := storage.ValidateNativeArchiveQuery(q); err != nil {
						return fmt.Errorf("panel %q has invalid archive query: %w", panel.ID, err)
					}
					reqBytes, mErr := json.Marshal(q)
					if mErr != nil {
						return mErr
					}
					seriesRaw, err = c.workflowRequest(ctx, http.MethodPost, "/api/v1/tests/"+url.PathEscape(runID)+"/archive/native-series", bytes.NewReader(reqBytes), "application/json")
				}
				if err != nil {
					return err
				}
				var seriesCheck struct {
					PanelInputs *storage.SavedPanelInputs `json:"panel_inputs"`
				}
				if err := json.Unmarshal(seriesRaw, &seriesCheck); err != nil || seriesCheck.PanelInputs == nil || !sameReplaySavedPanelInputs(*q.PanelInputs, *seriesCheck.PanelInputs) {
					return fmt.Errorf("panel %q replay returned mismatched or missing panel input pins", panel.ID)
				}
				items = append(items, replayedSavedPanelItem{
					TabID:    tab.ID,
					PanelID:  panel.ID,
					Title:    panel.Title,
					RunID:    runID,
					Query:    q,
					Response: json.RawMessage(seriesRaw),
				})
				continue
			}
			if q.BrowserInputs == nil && q.QueryVersion != 4 && q.QueryVersion != 5 {
				continue
			}
			if q.BrowserInputs == nil || storage.ValidateNativeArchiveQuery(q) != nil {
				return fmt.Errorf("panel %q has invalid or missing browser input pins", panel.ID)
			}
			if len(items) >= 20 {
				return errors.New("saved dashboard definition exceeds 20 pinned browser panels")
			}
			reqBytes, err := json.Marshal(q)
			if err != nil {
				return err
			}
			runID := q.BrowserInputs.Current.RunID
			seriesRaw, err := c.workflowRequest(ctx, http.MethodPost, "/api/v1/tests/"+url.PathEscape(runID)+"/archive/native-series", bytes.NewReader(reqBytes), "application/json")
			if err != nil {
				return err
			}
			var seriesCheck struct {
				BrowserInputs *storage.BrowserPanelInputs `json:"browser_inputs"`
			}
			if err := json.Unmarshal(seriesRaw, &seriesCheck); err != nil || seriesCheck.BrowserInputs == nil || !sameReplayPanelInputs(*q.BrowserInputs, *seriesCheck.BrowserInputs) {
				return fmt.Errorf("panel %q replay returned mismatched or missing browser input pins", panel.ID)
			}
			items = append(items, replayedSavedPanelItem{
				TabID:    tab.ID,
				PanelID:  panel.ID,
				Title:    panel.Title,
				RunID:    runID,
				Query:    q,
				Response: json.RawMessage(seriesRaw),
			})
		}
	}
	if len(items) == 0 {
		return errors.New("saved dashboard definition has no pinned browser panels to replay")
	}
	envelope := map[string]any{
		"dashboard_id": def.ID,
		"name":         def.Name,
		"revision":     def.Revision,
		"panels":       items,
	}
	if len(items) == 1 {
		var single map[string]any
		if json.Unmarshal(items[0].Response, &single) == nil {
			for k, v := range single {
				if _, exists := envelope[k]; !exists {
					envelope[k] = v
				}
			}
		}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

func openWorkflowOutput(path string, fallback io.Writer) (io.Writer, func() error, error) {
	if path == "" {
		return fallback, func() error { return nil }, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

func buildAnalysisFlagQuery(action, mode, population, metric, aggregation, protocol, actionFilter string, startSeconds, endSeconds, bucketSeconds int, liveBucketMode string) ([]byte, error) {
	q := storage.AnalyticsQuery{
		Population:    population,
		Metric:        metric,
		Aggregation:   aggregation,
		StartSeconds:  startSeconds,
		EndSeconds:    endSeconds,
		BucketSeconds: bucketSeconds,
	}
	if action == "byte-query" && q.Population == "" {
		q.Population = "byte"
	}
	if q.Aggregation == "" {
		if q.Metric == "bytes_sent_rate" || q.Metric == "bytes_received_rate" {
			q.Aggregation = "rate"
		} else if q.Population == "byte" {
			q.Aggregation = "sum"
		} else {
			q.Aggregation = "avg"
		}
	}
	if protocol != "" || actionFilter != "" {
		q.Filters = map[string]string{}
		if protocol != "" {
			q.Filters["protocol"] = protocol
		}
		if actionFilter != "" {
			q.Filters["action"] = actionFilter
		}
	}
	if action == "evidence-query" || (action == "byte-query" && mode == "live") {
		q.EvidenceMode = mode
		if q.EvidenceMode == "" {
			q.EvidenceMode = "live"
		}
		if liveBucketMode != "" {
			q.LiveBucketMode = liveBucketMode
		} else if q.EvidenceMode == "live" {
			q.LiveBucketMode = "merged"
		}
		q.MaxSeries = 1
		if q.BucketSeconds == 0 {
			q.BucketSeconds = 10
		}
		if q.EndSeconds == 0 {
			q.EndSeconds = 60
		}
		if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
			return nil, err
		}
		return json.Marshal(q)
	}
	storage.NormalizeSavedPanelQueryDefaults(&q)
	if err := storage.ValidateNativeArchiveQuery(q); err != nil {
		return nil, err
	}
	return json.Marshal(q)
}

// verdictError is a command's verdict on what it checked, with the exit
// code that carries it: a baseline comparison or an evaluation's release
// bars.
type verdictError struct {
	code    int
	message string
}

func (e *verdictError) Error() string { return e.message }
func checkBaselineVerdict(response []byte) error {
	var snapshot struct {
		Verdict *struct {
			Status      string `json:"status"`
			Unavailable int    `json:"unavailable"`
			Evaluated   int    `json:"evaluated"`
		} `json:"verdict"`
	}
	if err := json.Unmarshal(response, &snapshot); err != nil || snapshot.Verdict == nil {
		return &verdictError{exitCodeControllerOrTransport, "Baseline verdict unavailable"}
	}
	if snapshot.Verdict.Status == "regressed" {
		return &verdictError{exitCodeTestFailure, "Baseline regression detected"}
	}
	if snapshot.Verdict.Status != "passed" || snapshot.Verdict.Unavailable != 0 || snapshot.Verdict.Evaluated < 1 {
		return &verdictError{exitCodeControllerOrTransport, "Baseline evidence not evaluated"}
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

// workflowStream sends a workflow request and copies a successful response
// to out as it arrives, followed by a newline; a failure's bounded body is
// the error.
func (c *ApiClient) workflowStream(ctx context.Context, method, path string, body io.Reader, contentType string, out io.Writer) error {
	req, err := c.newRequest(method, path, body, contentType)
	if err != nil {
		return err
	}
	client := *c.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return fmt.Errorf("controller returned %s: %s", res.Status, data)
	}
	written, err := io.Copy(out, res.Body)
	if err != nil {
		return err
	}
	if written > 0 {
		_, err = fmt.Fprintln(out)
	}
	return err
}

const defaultAnalysisUploadChunkSize = int64(4 << 20)

func (c *ApiClient) uploadAnalysisFile(ctx context.Context, id, fileID, path string, chunkBytes int64, out io.Writer) error {
	if path == "" || fileID == "" {
		return errors.New("--file and --file-id are required")
	}
	chunkSize := defaultAnalysisUploadChunkSize
	if chunkBytes != 0 {
		if chunkBytes < 1024 || chunkBytes > defaultAnalysisUploadChunkSize {
			return errors.New("--chunk-bytes must be between 1024 and 4194304")
		}
		chunkSize = chunkBytes
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
