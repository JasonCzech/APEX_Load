package loadrunner

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

// ConvertResult holds the output of a LoadRunner to Stormrunner DSL conversion.
type ConvertResult struct {
	Script   *dsl.Script `json:"script"`
	Warnings []string    `json:"warnings,omitempty"`
	Stats    ConvertStats `json:"stats"`
}

// ConvertStats provides conversion statistics for reporting.
type ConvertStats struct {
	TotalFunctions   int `json:"total_functions"`
	ConvertedActions int `json:"converted_actions"`
	Extractors       int `json:"extractors"`
	ThinkTimes       int `json:"think_times"`
	Transactions     int `json:"transactions"`
	Skipped          int `json:"skipped"`
	UnsupportedFuncs []string `json:"unsupported_funcs,omitempty"`
}

// ConvertOptions controls the conversion behavior.
type ConvertOptions struct {
	ScriptName        string  // Override the generated script name
	BaseURL           string  // Override base URL (otherwise auto-detected from first URL)
	DefaultThinkTimeMs float64 // Default think time if none in script (0 = no default)
	IncludeAssertions  bool    // Add default status_code assertions
	PreserveTransactions bool  // Wrap actions in transaction markers (as comments in name)
}

// Convert transforms parsed LoadRunner action files into a Stormrunner DSL Script.
func Convert(actions []*ParsedAction, opts ConvertOptions) (*ConvertResult, error) {
	result := &ConvertResult{
		Warnings: make([]string, 0),
		Script: &dsl.Script{
			Version:       "1.0.0",
			Protocol:      "http",
			GeneratedBy:   "stormrunner-loadrunner-converter",
			GeneratedFrom: "loadrunner",
			Config: dsl.ScriptConfig{},
			Actions:       make([]dsl.Action, 0),
		},
	}

	// Collect all functions across action files in order
	var allFunctions []ParsedFunction
	for _, a := range actions {
		allFunctions = append(allFunctions, a.Functions...)
		result.Stats.TotalFunctions += len(a.Functions)
	}

	if len(allFunctions) == 0 {
		return nil, fmt.Errorf("no LoadRunner functions found in action files")
	}

	// Auto-detect base URL from first web_ call
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = detectBaseURL(allFunctions)
	}
	result.Script.Config.BaseURL = baseURL

	// Set script name
	if opts.ScriptName != "" {
		result.Script.Name = opts.ScriptName
	} else {
		result.Script.Name = "Converted_LR_Script"
	}

	// Process functions — correlations/extractors come BEFORE the request they apply to
	var pendingExtractors []dsl.Extractor
	var pendingHeaders map[string]string
	var currentTransaction string
	var thinkTimes []float64

	actionNameCounts := make(map[string]int)

	for _, fn := range allFunctions {
		switch fn.Name {
		case "web_reg_save_param":
			ext := convertExtractor(fn)
			if ext != nil {
				pendingExtractors = append(pendingExtractors, *ext)
				result.Stats.Extractors++
			}

		case "web_reg_find":
			// web_reg_find is a check/assertion — we note it but can't perfectly map
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("line %d: web_reg_find '%s' — manual review recommended for assertion logic", fn.LineNumber, fn.StepName))

		case "web_add_header", "web_add_auto_header":
			if pendingHeaders == nil {
				pendingHeaders = make(map[string]string)
			}
			for k, v := range fn.Headers {
				pendingHeaders[k] = v
			}

		case "lr_start_transaction":
			currentTransaction = fn.StepName
			result.Stats.Transactions++

		case "lr_end_transaction":
			currentTransaction = ""

		case "lr_think_time":
			sec, ok := ExtractThinkTimeSeconds(fn)
			if ok {
				thinkTimes = append(thinkTimes, sec)
				result.Stats.ThinkTimes++
			}

		case "web_url", "web_submit_data", "web_submit_form", "web_custom_request":
			action, warnings := convertAction(fn, baseURL, currentTransaction, opts)
			if action != nil {
				// Attach pending extractors
				if len(pendingExtractors) > 0 {
					action.Extractors = append(action.Extractors, pendingExtractors...)
					pendingExtractors = nil
				}

				// Attach pending headers
				if len(pendingHeaders) > 0 {
					if action.Headers == nil {
						action.Headers = make(map[string]string)
					}
					for k, v := range pendingHeaders {
						if _, exists := action.Headers[k]; !exists {
							action.Headers[k] = v
						}
					}
					pendingHeaders = nil
				}

				// Default assertions
				if opts.IncludeAssertions {
					action.Assertions = append(action.Assertions, dsl.Assertion{
						Type:     "status_code",
						Expected: 200,
					})
				}

				// Deduplicate action names
				actionNameCounts[action.Name]++
				if actionNameCounts[action.Name] > 1 {
					action.Name = fmt.Sprintf("%s_%d", action.Name, actionNameCounts[action.Name])
				}

				result.Script.Actions = append(result.Script.Actions, *action)
				result.Stats.ConvertedActions++
			}
			result.Warnings = append(result.Warnings, warnings...)

		default:
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
		}
	}

	// Configure think time from collected lr_think_time values
	if len(thinkTimes) > 0 {
		mean, std := computeThinkTimeStats(thinkTimes)
		result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{
			Type:   "normal",
			MeanMs: mean * 1000,
			StdMs:  std * 1000,
			MinMs:  math.Max(0, (mean-2*std)*1000),
			MaxMs:  (mean + 2*std) * 1000,
		}
	} else if opts.DefaultThinkTimeMs > 0 {
		result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{
			Type:   "fixed",
			MeanMs: opts.DefaultThinkTimeMs,
		}
	}

	// Mark actions with think_time_after (all except last)
	for i := range result.Script.Actions {
		if i < len(result.Script.Actions)-1 {
			result.Script.Actions[i].ThinkTimeAfter = true
		}
	}

	if len(result.Script.Actions) == 0 {
		return nil, fmt.Errorf("no HTTP actions could be converted from the LoadRunner script")
	}

	return result, nil
}

// convertAction converts a single web_ function to a DSL Action.
func convertAction(fn ParsedFunction, baseURL, transaction string, opts ConvertOptions) (*dsl.Action, []string) {
	var warnings []string

	action := &dsl.Action{
		Headers: make(map[string]string),
	}

	// Determine name
	name := sanitizeName(fn.StepName)
	if transaction != "" {
		name = sanitizeName(transaction) + "_" + name
	}
	action.Name = name

	// Set method and type
	action.Method = fn.Method
	action.Type = fn.Method
	if action.Type == "" {
		action.Type = "GET"
		action.Method = "GET"
	}

	// Determine target URL (relative to base)
	rawURL := fn.URL
	if rawURL == "" {
		warnings = append(warnings, fmt.Sprintf("line %d: %s '%s' has no URL, skipping", fn.LineNumber, fn.Name, fn.StepName))
		return nil, warnings
	}

	target := makeRelative(rawURL, baseURL)
	action.Target = target

	// Apply headers
	for k, v := range fn.Headers {
		action.Headers[k] = v
	}

	// Apply body
	if fn.Body != "" {
		action.Body = fn.Body
		// Convert LoadRunner parameter syntax {ParamName} → template syntax {{var.ParamName}}
		action.Body = convertParamReferences(action.Body)
	}

	// Convert parameter references in URL/target too
	action.Target = convertParamReferences(action.Target)

	return action, warnings
}

// convertExtractor converts a web_reg_save_param to a DSL Extractor.
func convertExtractor(fn ParsedFunction) *dsl.Extractor {
	paramName := fn.StepName
	if paramName == "" {
		return nil
	}

	lb := fn.Args["LB"]
	rb := fn.Args["RB"]

	if lb == "" && rb == "" {
		// Try positional args: web_reg_save_param("Name", "LB=...", "RB=...", ...)
		for _, raw := range fn.RawArgs {
			if strings.HasPrefix(raw, "LB=") {
				lb = strings.TrimPrefix(raw, "LB=")
			}
			if strings.HasPrefix(raw, "RB=") {
				rb = strings.TrimPrefix(raw, "RB=")
			}
		}
	}

	ext := &dsl.Extractor{
		Name:   paramName,
		Source: "body",
	}

	// If boundaries look like JSON path markers, use jsonpath
	if strings.Contains(lb, "\"") && strings.Contains(rb, "\"") {
		ext.Type = "regex"
		ext.Expression = regexp.QuoteMeta(lb) + "(.+?)" + regexp.QuoteMeta(rb)
	} else if lb != "" || rb != "" {
		ext.Type = "boundary"
		ext.Expression = fmt.Sprintf("LB=%s|RB=%s", lb, rb)
	} else {
		ext.Type = "regex"
		ext.Expression = "(.+?)"
	}

	return ext
}

// detectBaseURL finds the common base URL from all web_ function calls.
func detectBaseURL(functions []ParsedFunction) string {
	var urls []string
	for _, fn := range functions {
		if fn.URL != "" && strings.HasPrefix(fn.URL, "http") {
			urls = append(urls, fn.URL)
		}
	}

	if len(urls) == 0 {
		return "https://example.com"
	}

	// Parse first URL to get base
	parsed, err := url.Parse(urls[0])
	if err != nil {
		return urls[0]
	}

	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

// makeRelative strips the base URL from an absolute URL to create a relative path.
func makeRelative(rawURL, baseURL string) string {
	if baseURL != "" && strings.HasPrefix(rawURL, baseURL) {
		rel := strings.TrimPrefix(rawURL, baseURL)
		if rel == "" {
			rel = "/"
		}
		return rel
	}

	// Try to extract just the path
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	path := parsed.Path
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if path == "" {
		path = "/"
	}
	return path
}

// sanitizeName converts a LoadRunner step name to a valid DSL action name.
func sanitizeName(name string) string {
	if name == "" {
		return "unnamed_action"
	}

	// Replace spaces and special chars with underscores
	replacer := strings.NewReplacer(
		" ", "_",
		"-", "_",
		".", "_",
		"/", "_",
		"\\", "_",
		"(", "",
		")", "",
		"[", "",
		"]", "",
		"'", "",
		"\"", "",
		",", "",
		":", "_",
	)

	result := replacer.Replace(name)

	// Remove consecutive underscores
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}

	// Trim leading/trailing underscores
	result = strings.Trim(result, "_")

	if result == "" {
		return "unnamed_action"
	}

	return result
}

// convertParamReferences converts LoadRunner {ParamName} to Stormrunner {{var.ParamName}}.
func convertParamReferences(s string) string {
	// Match {ParamName} but not {{already.converted}}
	re := regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	return re.ReplaceAllString(s, "{{var.$1}}")
}

// computeThinkTimeStats calculates mean and standard deviation for think times.
func computeThinkTimeStats(times []float64) (mean, std float64) {
	if len(times) == 0 {
		return 0, 0
	}

	sum := 0.0
	for _, t := range times {
		sum += t
	}
	mean = sum / float64(len(times))

	if len(times) == 1 {
		return mean, 0
	}

	variance := 0.0
	for _, t := range times {
		diff := t - mean
		variance += diff * diff
	}
	variance /= float64(len(times) - 1)
	std = math.Sqrt(variance)

	return mean, std
}

// Ensure strconv is used (for future parameter parsing)
var _ = strconv.Atoi
