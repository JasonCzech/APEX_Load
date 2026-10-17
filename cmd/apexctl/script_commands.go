package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

type scriptArgs struct {
	command string
	script  string
	format  string
	help    bool
	err     error
}

type scriptValidationResult struct {
	Valid    bool     `json:"valid"`
	Script   string   `json:"script,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
	Issues   []string `json:"issues"`
	// HTTP3 says what the script's HTTP/3 policies need to run; it is not
	// a validation issue.
	HTTP3 string `json:"http3,omitempty"`
}

type scriptExplainResult struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"base_url,omitempty"`
	// Actions retains the count of root definitions in the main actions phase.
	// Checks include every phase, branch, and embedded request definition.
	Actions        int      `json:"actions"`
	Assertions     int      `json:"assertions"`
	Extractors     int      `json:"extractors"`
	DataSources    int      `json:"data_sources"`
	GoalConfigured bool     `json:"goal_configured"`
	GeneratedBy    string   `json:"generated_by,omitempty"`
	GeneratedFrom  string   `json:"generated_from,omitempty"`
	Summary        string   `json:"summary"`
	NextActions    []string `json:"next_actions,omitempty"`
}

func scriptCmd() {
	args := parseScriptArgs(os.Args[2:])
	if args.err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", args.err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if args.help || args.command == "help" || args.command == "--help" || args.command == "-h" {
		fmt.Println("Usage: apexctl script <validate|explain> --script <path> [--format text|json]")
		return
	}
	if args.command == "" {
		fmt.Fprintln(os.Stderr, "error: script subcommand is required (validate or explain)")
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if args.script == "" {
		fmt.Fprintln(os.Stderr, "error: --script is required")
		os.Exit(exitCodeUsageOrConfiguration)
	}
	switch args.command {
	case "validate":
		result := validateScriptFile(args.script)
		writeScriptValidation(args, result)
		if !result.Valid {
			os.Exit(exitCodeUsageOrConfiguration)
		}
	case "explain":
		result, err := explainScriptFile(args.script)
		if err != nil {
			if args.format == "json" {
				writeJSON(map[string]any{"status": "error", "error": err.Error()})
			} else {
				fmt.Fprintf(os.Stderr, "error explaining script: %v\n", err)
			}
			os.Exit(exitCodeUsageOrConfiguration)
		}
		writeScriptExplain(args, result)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown script subcommand %q\n", args.command)
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

func parseScriptArgs(raw []string) scriptArgs {
	args := scriptArgs{format: "text"}
	if len(raw) > 0 {
		args.command = strings.TrimSpace(raw[0])
		raw = raw[1:]
	}
	flags := flag.NewFlagSet("apexctl script", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&args.help, "help", false, "show help")
	flags.BoolVar(&args.help, "h", false, "show help")
	flags.StringVar(&args.script, "script", "", "script path")
	flags.StringVar(&args.script, "s", "", "script path")
	flags.StringVar(&args.format, "format", args.format, "output format")
	if err := flags.Parse(raw); err != nil {
		args.err = err
	} else if flags.NArg() != 0 {
		args.err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	args.command = strings.TrimSpace(args.command)
	args.script = strings.TrimSpace(args.script)
	args.format = strings.ToLower(strings.TrimSpace(args.format))
	if err := validateCLIOutputFormat(args.format); args.err == nil && err != nil {
		args.err = err
	}
	return args
}

func validateScriptFile(path string) scriptValidationResult {
	script, err := dsl.ParseFile(path)
	if err != nil {
		// One issue per finding, as the API's validation routes list them.
		var invalid *dsl.ValidationError
		if errors.As(err, &invalid) {
			return scriptValidationResult{Valid: false, Issues: append([]string{}, invalid.Issues...)}
		}
		return scriptValidationResult{Valid: false, Issues: []string{err.Error()}}
	}
	issues := dsl.ValidateScriptIssues(script)
	return scriptValidationResult{
		Valid:    len(issues) == 0,
		Script:   script.Name,
		Protocol: script.Protocol,
		Issues:   issuesOrEmpty(issues),
		HTTP3:    scriptHTTP3Note(script),
	}
}

func explainScriptFile(path string) (scriptExplainResult, error) {
	script, err := dsl.ParseFile(path)
	if err != nil {
		return scriptExplainResult{}, err
	}
	assertions := 0
	extractors := 0
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil {
			assertions += len(ref.Action.Assertions)
			extractors += len(ref.Action.Extractors)
		} else {
			// ParseFile retains embedded request fields as JSON-shaped arrays.
			requestAssertions, _ := ref.Request["assertions"].([]any)
			requestExtractors, _ := ref.Request["extractors"].([]any)
			assertions += len(requestAssertions)
			extractors += len(requestExtractors)
		}
		return nil
	}); err != nil {
		return scriptExplainResult{}, fmt.Errorf("count script checks: %w", err)
	}
	result := scriptExplainResult{
		Name:           script.Name,
		Protocol:       script.Protocol,
		BaseURL:        script.Config.BaseURL,
		Actions:        len(script.Actions),
		Assertions:     assertions,
		Extractors:     extractors,
		DataSources:    len(script.DataSources),
		GoalConfigured: script.GoalConfig != nil,
		GeneratedBy:    script.GeneratedBy,
		GeneratedFrom:  script.GeneratedFrom,
		Summary:        fmt.Sprintf("%s is a %s script with %d top-level action%s, %d assertion%s, and %d extractor%s across all phases and nested requests.", script.Name, script.Protocol, len(script.Actions), plural(len(script.Actions)), assertions, plural(assertions), extractors, plural(extractors)),
	}
	if len(script.DataSources) == 0 {
		result.NextActions = append(result.NextActions, "Add data sources if this flow needs per-user or per-iteration variation.")
	}
	if assertions == 0 {
		result.NextActions = append(result.NextActions, "Add assertions for release-gate confidence.")
	}
	if extractors == 0 && strings.EqualFold(script.Protocol, "http") {
		result.NextActions = append(result.NextActions, "Review responses for dynamic tokens and add extractors where needed.")
	}
	if script.GoalConfig == nil {
		result.NextActions = append(result.NextActions, "Add goal_config thresholds for CI/CD gating.")
	}
	return result, nil
}

func writeScriptValidation(args scriptArgs, result scriptValidationResult) {
	if args.format == "json" {
		writeJSON(result)
		return
	}
	status := "valid"
	if !result.Valid {
		status = "invalid"
	}
	fmt.Printf("Script:   %s\n", firstText(result.Script, args.script))
	if result.Protocol != "" {
		fmt.Printf("Protocol: %s\n", result.Protocol)
	}
	fmt.Printf("Status:   %s\n", status)
	if result.HTTP3 != "" {
		fmt.Printf("HTTP/3:   %s\n", result.HTTP3)
	}
	for _, issue := range result.Issues {
		fmt.Printf("  - %s\n", issue)
	}
}

func writeScriptExplain(args scriptArgs, result scriptExplainResult) {
	if args.format == "json" {
		writeJSON(result)
		return
	}
	fmt.Println(result.Summary)
	if result.BaseURL != "" {
		fmt.Printf("Base URL: %s\n", result.BaseURL)
	}
	if len(result.NextActions) > 0 {
		fmt.Println("Next actions:")
		for _, action := range result.NextActions {
			fmt.Printf("  - %s\n", action)
		}
	}
}

func writeJSON(value any) {
	payload, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(payload))
}

func issuesOrEmpty(issues []string) []string {
	if issues == nil {
		return []string{}
	}
	return issues
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func firstText(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
