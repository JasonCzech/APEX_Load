package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type scriptArgs struct {
	command string
	script  string
	format  string
}

type scriptValidationResult struct {
	Valid    bool     `json:"valid"`
	Script   string   `json:"script,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
	Issues   []string `json:"issues"`
}

type scriptExplainResult struct {
	Name           string   `json:"name"`
	Protocol       string   `json:"protocol"`
	BaseURL        string   `json:"base_url,omitempty"`
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
	if args.command == "" {
		fmt.Fprintln(os.Stderr, "error: script subcommand is required (validate or explain)")
		os.Exit(2)
	}
	if args.script == "" {
		fmt.Fprintln(os.Stderr, "error: --script is required")
		os.Exit(2)
	}
	switch args.command {
	case "validate":
		result := validateScriptFile(args.script)
		writeScriptValidation(args, result)
		if !result.Valid {
			os.Exit(1)
		}
	case "explain":
		result, err := explainScriptFile(args.script)
		if err != nil {
			if args.format == "json" {
				writeJSON(map[string]any{"status": "error", "error": err.Error()})
			} else {
				fmt.Fprintf(os.Stderr, "error explaining script: %v\n", err)
			}
			os.Exit(1)
		}
		writeScriptExplain(args, result)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown script subcommand %q\n", args.command)
		os.Exit(2)
	}
}

func parseScriptArgs(raw []string) scriptArgs {
	args := scriptArgs{format: "text"}
	if len(raw) > 0 {
		args.command = strings.TrimSpace(raw[0])
		raw = raw[1:]
	}
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case "--script", "-s":
			if i+1 < len(raw) {
				i++
				args.script = raw[i]
			}
		case "--format":
			if i+1 < len(raw) {
				i++
				args.format = raw[i]
			}
		}
	}
	return args
}

func validateScriptFile(path string) scriptValidationResult {
	script, err := dsl.ParseFile(path)
	if err != nil {
		return scriptValidationResult{Valid: false, Issues: []string{err.Error()}}
	}
	issues := dsl.ValidateScriptIssues(script)
	return scriptValidationResult{
		Valid:    len(issues) == 0,
		Script:   script.Name,
		Protocol: script.Protocol,
		Issues:   issuesOrEmpty(issues),
	}
}

func explainScriptFile(path string) (scriptExplainResult, error) {
	script, err := dsl.ParseFile(path)
	if err != nil {
		return scriptExplainResult{}, err
	}
	assertions := 0
	extractors := 0
	for _, action := range script.Actions {
		assertions += len(action.Assertions)
		extractors += len(action.Extractors)
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
		Summary:        fmt.Sprintf("%s is a %s script with %d action%s, %d assertion%s, and %d extractor%s.", script.Name, script.Protocol, len(script.Actions), plural(len(script.Actions)), assertions, plural(assertions), extractors, plural(extractors)),
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
