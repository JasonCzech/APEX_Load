package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/scrub"
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

const maxCLIScriptBytes = 32 << 20

func readBoundedCLIScriptFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxCLIScriptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCLIScriptBytes {
		return nil, fmt.Errorf("script exceeds %d byte limit", maxCLIScriptBytes)
	}
	return data, nil
}

func parseScriptFileForCLI(path string) (*dsl.Script, *engine.RedactionConfig, error) {
	data, err := readBoundedCLIScriptFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read script file: %w", err)
	}
	return parseScriptSnapshotForCLI(path, data)
}

func parseScriptSnapshotForCLI(path string, data []byte) (*dsl.Script, *engine.RedactionConfig, error) {
	var wrapper struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, nil, fmt.Errorf("parse script JSON: %w", err)
	}
	parsed := &wrapper.Script
	if parsed.Name == "" && parsed.Protocol == "" && len(parsed.Actions) == 0 {
		var direct dsl.Script
		if err := json.Unmarshal(data, &direct); err != nil {
			return nil, nil, fmt.Errorf("parse script JSON: %w", err)
		}
		parsed = &direct
	}
	rc := cliScriptRedactionConfig(parsed)
	baseDir := filepath.Dir(path)
	if resolveErr := dsl.ResolveBodyRefs(parsed, baseDir); resolveErr != nil {
		return nil, rc, resolveErr
	}
	rc = cliScriptRedactionConfig(parsed)
	if baseDir != "" {
		absDir, absErr := filepath.Abs(baseDir)
		if absErr != nil {
			return nil, rc, fmt.Errorf("resolve script directory: %w", absErr)
		}
		if regErr := fileasset.RegisterRoot(absDir); regErr != nil {
			return nil, rc, fmt.Errorf("register script asset directory: %w", regErr)
		}
		for i := range parsed.DataSources {
			dsPath := parsed.DataSources[i].Path
			if dsPath == "" || filepath.IsAbs(dsPath) {
				continue
			}
			resolved, relErr := filepath.Abs(filepath.Join(absDir, dsPath))
			if relErr != nil {
				return nil, rc, fmt.Errorf("resolve data source %q: %w", dsPath, relErr)
			}
			parsed.DataSources[i].Path = resolved
		}
	}
	rc = cliScriptRedactionConfig(parsed)
	if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(parsed); len(issues) > 0 {
		return nil, rc, &dsl.ValidationError{Issues: issues}
	}
	return parsed, rc, nil
}

func loadScriptForCLIExecution(path string, args runArgs) (*dsl.Script, *engine.RedactionConfig, error) {
	script, rc, err := parseScriptFileForCLI(path)
	if err != nil {
		return nil, attachCLIEnvSecrets(rc, args.env), err
	}
	overridden, err := applyRuntimeOverrides(script, args)
	if err != nil {
		return nil, attachCLIEnvSecrets(rc, args.env), err
	}
	return overridden, cliScriptExecutionRedactionConfig(overridden, args.env), nil
}

func loadScriptSnapshotForCLIExecution(path string, data []byte, args runArgs) (*dsl.Script, *engine.RedactionConfig, error) {
	script, rc, err := parseScriptSnapshotForCLI(path, data)
	if err != nil {
		return nil, attachCLIEnvSecrets(rc, args.env), err
	}
	overridden, err := applyRuntimeOverrides(script, args)
	if err != nil {
		return nil, attachCLIEnvSecrets(rc, args.env), err
	}
	return overridden, cliScriptExecutionRedactionConfig(overridden, args.env), nil
}

func cliScriptExecutionRedactionConfig(script *dsl.Script, env map[string]string) *engine.RedactionConfig {
	return attachCLIEnvSecrets(cliScriptRedactionConfig(script), env)
}

func attachCLIEnvSecrets(rc *engine.RedactionConfig, env map[string]string) *engine.RedactionConfig {
	if len(env) == 0 {
		return rc
	}
	if rc == nil {
		var err error
		rc, err = engine.NewRedactionConfig(nil)
		if err != nil {
			return nil
		}
	}
	var envSecrets []string
	for key, value := range env {
		if value == "" {
			continue
		}
		if scrub.SensitiveName(key) || scrub.Value(value, scrub.Policy{}) != value {
			envSecrets = append(envSecrets, value)
		}
	}
	if len(envSecrets) == 0 {
		return rc
	}
	if withEnv, err := engine.WithScriptSourcedSecretValueRedaction(rc, envSecrets); err == nil {
		return withEnv
	}
	return rc
}

func cliScriptBytesRedactionConfig(data []byte) *engine.RedactionConfig {
	if len(data) == 0 {
		return cliScriptRedactionConfig(nil)
	}
	var wrapper struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && (wrapper.Script.Name != "" || wrapper.Script.Protocol != "" || len(wrapper.Script.Actions) > 0) {
		return cliScriptRedactionConfig(&wrapper.Script)
	}
	var direct dsl.Script
	if err := json.Unmarshal(data, &direct); err == nil {
		return cliScriptRedactionConfig(&direct)
	}
	return cliScriptRedactionConfig(nil)
}

func cliScriptRedactionConfig(script *dsl.Script) *engine.RedactionConfig {
	rc, err := engine.NewRedactionConfig(nil)
	if err != nil || script == nil {
		return rc
	}
	if values, inspectErr := engine.ImportedCredentialValues(script); inspectErr == nil && len(values) > 0 {
		if withValues, attachErr := engine.WithScriptSourcedSecretValueRedaction(rc, values); attachErr == nil {
			rc = withValues
		}
	}
	return rc
}

func scrubCLIText(text string, rc *engine.RedactionConfig) string {
	if text == "" {
		return ""
	}
	if rc != nil {
		text = rc.ScrubText(text)
	}
	return scrub.Value(text, scrub.Policy{})
}

func validateScriptFile(path string) scriptValidationResult {
	script, rc, err := parseScriptFileForCLI(path)
	if err != nil {
		// One issue per finding, as the API's validation routes list them.
		var invalid *dsl.ValidationError
		if errors.As(err, &invalid) {
			issues := make([]string, len(invalid.Issues))
			for i, issue := range invalid.Issues {
				issues[i] = scrubCLIText(issue, rc)
			}
			return scriptValidationResult{Valid: false, Issues: issues}
		}
		return scriptValidationResult{Valid: false, Issues: []string{scrubCLIText(err.Error(), rc)}}
	}
	return scriptValidationResult{
		Valid:    true,
		Script:   scrubCLIText(script.Name, rc),
		Protocol: script.Protocol,
		Issues:   []string{},
		HTTP3:    scriptHTTP3Note(script),
	}
}

func scrubCLIURLOrText(raw string, rc *engine.RedactionConfig) string {
	if raw == "" {
		return ""
	}
	if rc != nil {
		raw = rc.ScrubText(raw)
	}
	raw = scrub.ValueURL(raw, scrub.Policy{})
	return scrubCLIText(raw, rc)
}

func explainScriptFile(path string) (scriptExplainResult, error) {
	script, rc, err := parseScriptFileForCLI(path)
	if err != nil {
		return scriptExplainResult{}, errors.New(scrubCLIText(err.Error(), rc))
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
		return scriptExplainResult{}, fmt.Errorf("count script checks: %s", scrubCLIText(err.Error(), rc))
	}
	baseURL := scrubCLIURLOrText(script.Config.BaseURL, rc)
	safeName := scrubCLIText(script.Name, rc)
	result := scriptExplainResult{
		Name:           safeName,
		Protocol:       script.Protocol,
		BaseURL:        baseURL,
		Actions:        len(script.Actions),
		Assertions:     assertions,
		Extractors:     extractors,
		DataSources:    len(script.DataSources),
		GoalConfigured: script.GoalConfig != nil,
		GeneratedBy:    scrubCLIText(script.GeneratedBy, rc),
		GeneratedFrom:  scrubCLIURLOrText(script.GeneratedFrom, rc),
		Summary:        fmt.Sprintf("%s is a %s script with %d top-level action%s, %d assertion%s, and %d extractor%s across all phases and nested requests.", safeName, script.Protocol, len(script.Actions), plural(len(script.Actions)), assertions, plural(assertions), extractors, plural(extractors)),
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
