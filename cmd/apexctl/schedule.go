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
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/runcontract"
)

func parseAnalysisPolicyFlag(flagName, raw string) (*runcontract.AnalysisPolicySelection, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false, nil
	}
	if trimmed != raw {
		return nil, false, fmt.Errorf("%s cannot contain leading or trailing whitespace", flagName)
	}
	if trimmed == "project_default" {
		return nil, true, nil
	}
	id, revStr, ok := strings.Cut(trimmed, "@")
	if !ok || id == "" || id != strings.TrimSpace(id) || len(id) > 256 || strings.Contains(revStr, "@") {
		return nil, false, fmt.Errorf("%s must be <id>@<revision> or project_default", flagName)
	}
	rev, err := strconv.ParseInt(revStr, 10, 64)
	if err != nil || rev < 1 || revStr != strconv.FormatInt(rev, 10) {
		return nil, false, fmt.Errorf("%s revision must be a positive integer", flagName)
	}
	return &runcontract.AnalysisPolicySelection{ID: id, Revision: rev}, false, nil
}

func cloneAnalysisPolicySelection(in *runcontract.AnalysisPolicySelection) *runcontract.AnalysisPolicySelection {
	if in == nil {
		return nil
	}
	copied := *in
	return &copied
}

func cloneAnalysisPolicySelections(in *runcontract.AnalysisPolicySelections) *runcontract.AnalysisPolicySelections {
	if in == nil {
		return nil
	}
	return &runcontract.AnalysisPolicySelections{
		Browser: cloneAnalysisPolicySelection(in.Browser),
		Journey: cloneAnalysisPolicySelection(in.Journey),
	}
}

func sameAnalysisPolicySelection(left, right *runcontract.AnalysisPolicySelection) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.ID == right.ID && left.Revision == right.Revision
}

func formatAnalysisPolicySelection(sel *runcontract.AnalysisPolicySelection) string {
	if sel == nil {
		return ""
	}
	return fmt.Sprintf("%s@%d", sel.ID, sel.Revision)
}

type scheduleUpsertPayload struct {
	Name                     string                                `json:"name"`
	CronExpr                 string                                `json:"cron_expr"`
	Timezone                 string                                `json:"timezone,omitempty"`
	ReleaseID                string                                `json:"release_id,omitempty"`
	TimeslotID               string                                `json:"timeslot_id,omitempty"`
	RunNameTemplate          string                                `json:"run_name_template,omitempty"`
	Enabled                  *bool                                 `json:"enabled,omitempty"`
	ApprovalPolicy           string                                `json:"approval_policy,omitempty"`
	MissedRunPolicy          string                                `json:"missed_run_policy,omitempty"`
	BlackoutWindows          json.RawMessage                       `json:"blackout_windows,omitempty"`
	MaintenanceWindows       json.RawMessage                       `json:"maintenance_windows,omitempty"`
	AvailabilityWindows      json.RawMessage                       `json:"availability_windows,omitempty"`
	DisabledFrom             json.RawMessage                       `json:"disabled_from,omitempty"`
	DisabledUntil            json.RawMessage                       `json:"disabled_until,omitempty"`
	AnalysisPolicySelections *runcontract.AnalysisPolicySelections `json:"analysis_policy_selections,omitempty"`
	ExpectedRevision         *int                                  `json:"expected_revision,omitempty"`
}

type scheduleResolvePayload struct {
	Action           string `json:"action"`
	Reason           string `json:"reason,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

func scheduleCmd() {
	os.Exit(executeSchedule(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
}

func executeSchedule(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, scheduleUsage())
		return exitCodeSuccess
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return runScheduleList(ctx, rest, stdout, stderr)
	case "preview":
		return runScheduleUpsertOrPreview(ctx, "preview", rest, stdout, stderr)
	case "create":
		return runScheduleUpsertOrPreview(ctx, "create", rest, stdout, stderr)
	case "update":
		return runScheduleUpsertOrPreview(ctx, "update", rest, stdout, stderr)
	case "delete":
		return runScheduleDelete(ctx, rest, stdout, stderr)
	case "occurrences":
		return runScheduleOccurrences(ctx, rest, stdout, stderr)
	case "resolve":
		return runScheduleResolve(ctx, rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown schedule command: %s\n%s\n", sub, scheduleUsage())
		return exitCodeUsageOrConfiguration
	}
}

type scheduleCommonFlags struct {
	api         string
	apiKey      string
	bearerToken string
	scenarioID  string
	format      string
	help        bool
}

func bindScheduleCommonFlags(fs *flag.FlagSet) *scheduleCommonFlags {
	common := &scheduleCommonFlags{
		api:         strings.TrimSpace(os.Getenv("APEXLOAD_API")),
		apiKey:      strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")),
		bearerToken: strings.TrimSpace(os.Getenv("APEXLOAD_BEARER_TOKEN")),
		format:      "json",
	}
	fs.BoolVar(&common.help, "help", false, "show help")
	fs.BoolVar(&common.help, "h", false, "show help")
	fs.StringVar(&common.api, "api", common.api, "controller URL")
	fs.StringVar(&common.apiKey, "api-key", common.apiKey, "API key")
	fs.StringVar(&common.bearerToken, "bearer-token", common.bearerToken, "bearer token")
	fs.StringVar(&common.scenarioID, "scenario", "", "scenario ID")
	fs.StringVar(&common.format, "format", common.format, "output format (json or text)")
	return common
}

func validateScheduleCommonFlags(common *scheduleCommonFlags) (*ApiClient, error) {
	common.api = strings.TrimRight(strings.TrimSpace(common.api), "/")
	common.apiKey = strings.TrimSpace(common.apiKey)
	common.bearerToken = strings.TrimSpace(common.bearerToken)
	common.scenarioID = strings.TrimSpace(common.scenarioID)
	common.format = strings.ToLower(strings.TrimSpace(common.format))
	if common.api == "" {
		return nil, fmt.Errorf("--api is required")
	}
	if err := validateCLIControllerURL(common.api); err != nil {
		return nil, err
	}
	if common.scenarioID == "" {
		return nil, fmt.Errorf("--scenario is required")
	}
	if err := validateCLIOutputFormat(common.format); err != nil {
		return nil, err
	}
	return NewApiClientWithAuth(common.api, common.apiKey, common.bearerToken), nil
}

func runScheduleList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apexctl schedule list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	common := bindScheduleCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	if common.help {
		fmt.Fprintln(stdout, scheduleUsage())
		return exitCodeSuccess
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return exitCodeUsageOrConfiguration
	}
	client, err := validateScheduleCommonFlags(common)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	path := fmt.Sprintf("/api/v1/scenarios/%s/schedules", url.PathEscape(common.scenarioID))
	return doScheduleRequest(ctx, client, http.MethodGet, path, nil, common.format, stdout, stderr)
}

func runScheduleUpsertOrPreview(ctx context.Context, mode string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apexctl schedule "+mode, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	common := bindScheduleCommonFlags(fs)
	var (
		scheduleID       string
		name             string
		cronExpr         string
		timezone         string
		releaseID        string
		timeslotID       string
		runNameTemplate  string
		approvalPolicy   string
		missedRunPolicy  string
		browserPolicyRaw string
		journeyPolicyRaw string
		enabledRaw       string
		expectedRev      int
		hasExpectedRev   bool
		count            int
	)
	if mode == "update" {
		fs.StringVar(&scheduleID, "schedule", "", "schedule ID")
		fs.Func("expected-revision", "expected schedule revision", func(raw string) error {
			parsed, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || parsed < 1 {
				return fmt.Errorf("--expected-revision must be a positive integer")
			}
			expectedRev = parsed
			hasExpectedRev = true
			return nil
		})
	}
	if mode == "preview" {
		fs.IntVar(&count, "count", 5, "number of occurrences to preview")
	}
	fs.StringVar(&name, "name", "", "schedule name")
	fs.StringVar(&cronExpr, "cron", "", "5-field cron expression")
	fs.StringVar(&timezone, "timezone", "", "IANA timezone")
	fs.StringVar(&releaseID, "release", "", "release ID")
	fs.StringVar(&timeslotID, "timeslot", "", "timeslot ID")
	fs.StringVar(&runNameTemplate, "run-name-template", "", "run name template")
	fs.StringVar(&approvalPolicy, "approval-policy", "", "approval policy")
	fs.StringVar(&missedRunPolicy, "missed-run-policy", "", "missed-run policy")
	fs.StringVar(&enabledRaw, "enabled", "", "enable or disable schedule (true or false)")
	fs.StringVar(&browserPolicyRaw, "browser-policy", "", "browser policy revision (<id>@<revision> or project_default)")
	fs.StringVar(&journeyPolicyRaw, "journey-policy", "", "journey policy revision (<id>@<revision> or project_default)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	if common.help {
		fmt.Fprintln(stdout, scheduleUsage())
		return exitCodeSuccess
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return exitCodeUsageOrConfiguration
	}
	client, err := validateScheduleCommonFlags(common)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	scheduleID = strings.TrimSpace(scheduleID)
	name = strings.TrimSpace(name)
	cronExpr = strings.TrimSpace(cronExpr)
	if mode == "update" {
		if scheduleID == "" {
			fmt.Fprintln(stderr, "error: --schedule is required")
			return exitCodeUsageOrConfiguration
		}
		if !hasExpectedRev {
			fmt.Fprintln(stderr, "error: --expected-revision is required for schedule update")
			return exitCodeUsageOrConfiguration
		}
	}
	if mode == "preview" && count < 1 {
		fmt.Fprintln(stderr, "error: --count must be a positive integer")
		return exitCodeUsageOrConfiguration
	}
	var enabledPtr *bool
	if strings.TrimSpace(enabledRaw) != "" {
		parsed, err := strconv.ParseBool(strings.TrimSpace(enabledRaw))
		if err != nil {
			fmt.Fprintln(stderr, "error: --enabled must be true or false")
			return exitCodeUsageOrConfiguration
		}
		enabledPtr = &parsed
	}
	bp, bpDefault, err := parseAnalysisPolicyFlag("--browser-policy", browserPolicyRaw)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	jp, jpDefault, err := parseAnalysisPolicyFlag("--journey-policy", journeyPolicyRaw)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	hasBrowserFlag := strings.TrimSpace(browserPolicyRaw) != ""
	hasJourneyFlag := strings.TrimSpace(journeyPolicyRaw) != ""

	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})

	var (
		existingSelections  *runcontract.AnalysisPolicySelections
		blackoutWindows     json.RawMessage
		maintenanceWindows  json.RawMessage
		availabilityWindows json.RawMessage
		disabledFrom        json.RawMessage
		disabledUntil       json.RawMessage
	)
	if mode == "update" {
		existing, code := fetchExistingScenarioSchedule(ctx, client, common.scenarioID, scheduleID, stderr)
		if code != exitCodeSuccess {
			return code
		}
		if !visited["name"] && name == "" {
			name = existing.Name
		}
		if !visited["cron"] && cronExpr == "" {
			cronExpr = existing.CronExpr
		}
		if !visited["timezone"] {
			timezone = existing.Timezone
		}
		if !visited["release"] {
			releaseID = existing.ReleaseID
		}
		if !visited["timeslot"] {
			timeslotID = existing.TimeslotID
		}
		if !visited["run-name-template"] {
			runNameTemplate = existing.RunNameTemplate
		}
		if !visited["approval-policy"] {
			approvalPolicy = existing.ApprovalPolicy
		}
		if !visited["missed-run-policy"] {
			missedRunPolicy = existing.MissedRunPolicy
		}
		if !visited["enabled"] && enabledPtr == nil {
			enabled := existing.Enabled
			enabledPtr = &enabled
		}
		blackoutWindows = existing.BlackoutWindows
		maintenanceWindows = existing.MaintenanceWindows
		availabilityWindows = existing.AvailabilityWindows
		disabledFrom = existing.DisabledFrom
		disabledUntil = existing.DisabledUntil
		existingSelections = existing.AnalysisPolicySelections
	}
	if name == "" {
		fmt.Fprintln(stderr, "error: --name is required")
		return exitCodeUsageOrConfiguration
	}
	if cronExpr == "" {
		fmt.Fprintln(stderr, "error: --cron is required")
		return exitCodeUsageOrConfiguration
	}

	var selections *runcontract.AnalysisPolicySelections
	if hasBrowserFlag || hasJourneyFlag {
		selections = &runcontract.AnalysisPolicySelections{}
		if hasBrowserFlag {
			if !bpDefault {
				selections.Browser = bp
			}
		} else if existingSelections != nil {
			selections.Browser = cloneAnalysisPolicySelection(existingSelections.Browser)
		}
		if hasJourneyFlag {
			if !jpDefault {
				selections.Journey = jp
			}
		} else if existingSelections != nil {
			selections.Journey = cloneAnalysisPolicySelection(existingSelections.Journey)
		}
		if mode != "update" && selections.Browser == nil && selections.Journey == nil {
			selections = nil
		}
	}

	payload := scheduleUpsertPayload{
		Name:                     name,
		CronExpr:                 cronExpr,
		Timezone:                 strings.TrimSpace(timezone),
		ReleaseID:                strings.TrimSpace(releaseID),
		TimeslotID:               strings.TrimSpace(timeslotID),
		RunNameTemplate:          strings.TrimSpace(runNameTemplate),
		Enabled:                  enabledPtr,
		ApprovalPolicy:           strings.TrimSpace(approvalPolicy),
		MissedRunPolicy:          strings.TrimSpace(missedRunPolicy),
		BlackoutWindows:          blackoutWindows,
		MaintenanceWindows:       maintenanceWindows,
		AvailabilityWindows:      availabilityWindows,
		DisabledFrom:             disabledFrom,
		DisabledUntil:            disabledUntil,
		AnalysisPolicySelections: selections,
	}
	if hasExpectedRev {
		payload.ExpectedRevision = &expectedRev
	}

	switch mode {
	case "preview":
		path := fmt.Sprintf("/api/v1/scenarios/%s/schedules/preview?count=%d", url.PathEscape(common.scenarioID), count)
		return doScheduleRequest(ctx, client, http.MethodPost, path, payload, common.format, stdout, stderr)
	case "create":
		path := fmt.Sprintf("/api/v1/scenarios/%s/schedules", url.PathEscape(common.scenarioID))
		return doScheduleUpsertWithEchoCheck(ctx, client, http.MethodPost, common.scenarioID, path, payload, selections, common.format, stdout, stderr)
	default:
		path := fmt.Sprintf("/api/v1/scenarios/%s/schedules/%s", url.PathEscape(common.scenarioID), url.PathEscape(scheduleID))
		return doScheduleUpsertWithEchoCheck(ctx, client, http.MethodPut, common.scenarioID, path, payload, selections, common.format, stdout, stderr)
	}
}

func doScheduleUpsertWithEchoCheck(ctx context.Context, client *ApiClient, method, scenarioID, path string, payload scheduleUpsertPayload, expected *runcontract.AnalysisPolicySelections, format string, stdout, stderr io.Writer) int {
	encoded, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	resp, err := client.doContext(ctx, method, path, bytes.NewReader(encoded), "application/json")
	if err != nil {
		var configErr *ciConfigurationFailure
		if errors.As(err, &configErr) {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return exitCodeUsageOrConfiguration
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeControllerOrTransport
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "error: %s: %s\n", resp.Status, strings.TrimSpace(string(raw)))
		return scheduleHTTPExitCode(resp.StatusCode)
	}
	if expected != nil {
		var returned existingScheduleWire
		if err := json.Unmarshal(raw, &returned); err != nil {
			fmt.Fprintf(stderr, "error: decode schedule response: %v\n", err)
			return exitCodeControllerOrTransport
		}
		var echoedBrowser, echoedJourney *runcontract.AnalysisPolicySelection
		if returned.AnalysisPolicySelections != nil {
			echoedBrowser = returned.AnalysisPolicySelections.Browser
			echoedJourney = returned.AnalysisPolicySelections.Journey
		}
		if !sameAnalysisPolicySelection(expected.Browser, echoedBrowser) || !sameAnalysisPolicySelection(expected.Journey, echoedJourney) {
			if method == http.MethodPost && returned.ID != "" {
				delPath := fmt.Sprintf("/api/v1/scenarios/%s/schedules/%s", url.PathEscape(scenarioID), url.PathEscape(returned.ID))
				if delResp, delErr := client.doContext(ctx, http.MethodDelete, delPath, nil, ""); delErr == nil && delResp != nil {
					_ = delResp.Body.Close()
				}
			}
			fmt.Fprintln(stderr, "error: the controller did not confirm the requested analysis_policy_selections on the schedule; upgrade the controller")
			return exitCodeControllerOrTransport
		}
	}
	writeScheduleOutput(raw, format, stdout)
	return exitCodeSuccess
}

type existingScheduleWire struct {
	ID                       string                                `json:"id"`
	Name                     string                                `json:"name"`
	CronExpr                 string                                `json:"cron_expr"`
	Timezone                 string                                `json:"timezone"`
	ReleaseID                string                                `json:"release_id"`
	TimeslotID               string                                `json:"timeslot_id"`
	RunNameTemplate          string                                `json:"run_name_template"`
	Enabled                  bool                                  `json:"enabled"`
	ApprovalPolicy           string                                `json:"approval_policy"`
	MissedRunPolicy          string                                `json:"missed_run_policy"`
	BlackoutWindows          json.RawMessage                       `json:"blackout_windows,omitempty"`
	MaintenanceWindows       json.RawMessage                       `json:"maintenance_windows,omitempty"`
	AvailabilityWindows      json.RawMessage                       `json:"availability_windows,omitempty"`
	DisabledFrom             json.RawMessage                       `json:"disabled_from,omitempty"`
	DisabledUntil            json.RawMessage                       `json:"disabled_until,omitempty"`
	AnalysisPolicySelections *runcontract.AnalysisPolicySelections `json:"analysis_policy_selections"`
}

func fetchExistingScenarioSchedule(ctx context.Context, client *ApiClient, scenarioID, scheduleID string, stderr io.Writer) (*existingScheduleWire, int) {
	path := fmt.Sprintf("/api/v1/scenarios/%s/schedules", url.PathEscape(scenarioID))
	resp, err := client.doContext(ctx, http.MethodGet, path, nil, "application/json")
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, exitCodeControllerOrTransport
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "error: %s: %s\n", resp.Status, strings.TrimSpace(string(body)))
		return nil, scheduleHTTPExitCode(resp.StatusCode)
	}
	var list struct {
		Schedules []existingScheduleWire `json:"schedules"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		fmt.Fprintf(stderr, "error: decode schedule list: %v\n", err)
		return nil, exitCodeControllerOrTransport
	}
	for i := range list.Schedules {
		if list.Schedules[i].ID == scheduleID {
			return &list.Schedules[i], exitCodeSuccess
		}
	}
	fmt.Fprintf(stderr, "error: scenario schedule %q not found\n", scheduleID)
	return nil, exitCodeUsageOrConfiguration
}

func runScheduleDelete(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apexctl schedule delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	common := bindScheduleCommonFlags(fs)
	var scheduleID string
	fs.StringVar(&scheduleID, "schedule", "", "schedule ID")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	if common.help {
		fmt.Fprintln(stdout, scheduleUsage())
		return exitCodeSuccess
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return exitCodeUsageOrConfiguration
	}
	client, err := validateScheduleCommonFlags(common)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	scheduleID = strings.TrimSpace(scheduleID)
	if scheduleID == "" {
		fmt.Fprintln(stderr, "error: --schedule is required")
		return exitCodeUsageOrConfiguration
	}
	path := fmt.Sprintf("/api/v1/scenarios/%s/schedules/%s", url.PathEscape(common.scenarioID), url.PathEscape(scheduleID))
	return doScheduleRequest(ctx, client, http.MethodDelete, path, nil, common.format, stdout, stderr)
}

func runScheduleOccurrences(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apexctl schedule occurrences", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	common := bindScheduleCommonFlags(fs)
	var (
		scheduleID string
		limit      int
		offset     int
	)
	fs.StringVar(&scheduleID, "schedule", "", "schedule ID")
	fs.IntVar(&limit, "limit", 50, "page size")
	fs.IntVar(&offset, "offset", 0, "page offset")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	if common.help {
		fmt.Fprintln(stdout, scheduleUsage())
		return exitCodeSuccess
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return exitCodeUsageOrConfiguration
	}
	client, err := validateScheduleCommonFlags(common)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	scheduleID = strings.TrimSpace(scheduleID)
	if scheduleID == "" {
		fmt.Fprintln(stderr, "error: --schedule is required")
		return exitCodeUsageOrConfiguration
	}
	if limit < 1 || offset < 0 {
		fmt.Fprintln(stderr, "error: --limit must be positive and --offset cannot be negative")
		return exitCodeUsageOrConfiguration
	}
	path := fmt.Sprintf("/api/v1/scenarios/%s/schedules/%s/occurrences?limit=%d&offset=%d",
		url.PathEscape(common.scenarioID), url.PathEscape(scheduleID), limit, offset)
	return doScheduleRequest(ctx, client, http.MethodGet, path, nil, common.format, stdout, stderr)
}

func runScheduleResolve(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apexctl schedule resolve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	common := bindScheduleCommonFlags(fs)
	var (
		scheduleID     string
		occurrenceID   string
		action         string
		reason         string
		expectedRev    int64
		hasExpectedRev bool
	)
	fs.StringVar(&scheduleID, "schedule", "", "schedule ID")
	fs.StringVar(&occurrenceID, "occurrence", "", "occurrence ID")
	fs.StringVar(&action, "action", "", "resolution action: retry, catch_up, or skip")
	fs.StringVar(&reason, "reason", "", "operator resolution reason")
	fs.Func("expected-revision", "expected occurrence revision", func(raw string) error {
		parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || parsed < 1 {
			return fmt.Errorf("--expected-revision must be a positive integer")
		}
		expectedRev = parsed
		hasExpectedRev = true
		return nil
	})
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	if common.help {
		fmt.Fprintln(stdout, scheduleUsage())
		return exitCodeSuccess
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return exitCodeUsageOrConfiguration
	}
	client, err := validateScheduleCommonFlags(common)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeUsageOrConfiguration
	}
	scheduleID = strings.TrimSpace(scheduleID)
	occurrenceID = strings.TrimSpace(occurrenceID)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "catch-up" || action == "catchup" {
		action = "catch_up"
	}
	if scheduleID == "" || occurrenceID == "" {
		fmt.Fprintln(stderr, "error: --schedule and --occurrence are required")
		return exitCodeUsageOrConfiguration
	}
	switch action {
	case "retry", "catch_up", "skip":
	default:
		fmt.Fprintln(stderr, "error: --action must be retry, catch_up, or skip")
		return exitCodeUsageOrConfiguration
	}
	payload := scheduleResolvePayload{
		Action: action,
		Reason: strings.TrimSpace(reason),
	}
	if hasExpectedRev {
		payload.ExpectedRevision = &expectedRev
	}
	path := fmt.Sprintf("/api/v1/scenarios/%s/schedules/%s/occurrences/%s/resolve",
		url.PathEscape(common.scenarioID), url.PathEscape(scheduleID), url.PathEscape(occurrenceID))
	return doScheduleRequest(ctx, client, http.MethodPost, path, payload, common.format, stdout, stderr)
}

func doScheduleRequest(ctx context.Context, client *ApiClient, method, path string, payload any, format string, stdout, stderr io.Writer) int {
	var bodyReader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return exitCodeUsageOrConfiguration
		}
		bodyReader = bytes.NewReader(encoded)
	}
	resp, err := client.doContext(ctx, method, path, bodyReader, "application/json")
	if err != nil {
		var configErr *ciConfigurationFailure
		if errors.As(err, &configErr) {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return exitCodeUsageOrConfiguration
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitCodeControllerOrTransport
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "error: %s: %s\n", resp.Status, strings.TrimSpace(string(raw)))
		return scheduleHTTPExitCode(resp.StatusCode)
	}
	writeScheduleOutput(raw, format, stdout)
	return exitCodeSuccess
}

func writeScheduleOutput(raw []byte, format string, stdout io.Writer) {
	if format == "text" {
		fmt.Fprintln(stdout, strings.TrimSpace(string(raw)))
		return
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		fmt.Fprintln(stdout, pretty.String())
	} else {
		fmt.Fprintln(stdout, strings.TrimSpace(string(raw)))
	}
}

func scheduleHTTPExitCode(status int) int {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusPreconditionRequired, http.StatusUnprocessableEntity:
		return exitCodeUsageOrConfiguration
	default:
		return exitCodeControllerOrTransport
	}
}

func scheduleUsage() string {
	return `Schedule Commands:
  apexctl schedule list --api <url> --scenario <id>
  apexctl schedule preview --api <url> --scenario <id> --name <name> --cron <expr> [--browser-policy <id>@<rev>|project_default] [--journey-policy <id>@<rev>|project_default] [--count <n>]
  apexctl schedule create --api <url> --scenario <id> --name <name> --cron <expr> [--browser-policy <id>@<rev>|project_default] [--journey-policy <id>@<rev>|project_default]
  apexctl schedule update --api <url> --scenario <id> --schedule <schedule-id> --expected-revision <rev> [--name <name>] [--cron <expr>] [--browser-policy <id>@<rev>|project_default] [--journey-policy <id>@<rev>|project_default]
  apexctl schedule delete --api <url> --scenario <id> --schedule <schedule-id>
  apexctl schedule occurrences --api <url> --scenario <id> --schedule <schedule-id> [--limit <n>] [--offset <n>]
  apexctl schedule resolve --api <url> --scenario <id> --schedule <schedule-id> --occurrence <occurrence-id> --action retry|catch_up|skip [--reason <text>] [--expected-revision <rev>]`
}
