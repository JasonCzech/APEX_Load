package postman

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/convert/srcparse"
	"github.com/jasonczech/apexload/pkg/convert/tmpl"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"golang.org/x/net/http/httpguts"
)

type ImportRequest struct {
	Name                string         `json:"name,omitempty"`
	ProjectID           string         `json:"project_id,omitempty"`
	Content             string         `json:"content"`
	CollectionFileName  string         `json:"collection_file_name,omitempty"`
	EnvironmentContent  string         `json:"environment_content,omitempty"`
	EnvironmentFileName string         `json:"environment_file_name,omitempty"`
	Options             ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	ScriptName          string `json:"script_name,omitempty"`
	BaseURL             string `json:"base_url,omitempty"`
	IncludeAssertions   *bool  `json:"include_assertions,omitempty"`
	SanitizeSensitive   *bool  `json:"sanitize_sensitive,omitempty"`
	UseParameterDataSet *bool  `json:"use_parameter_data_source,omitempty"`
	DefaultThinkTimeMs  int    `json:"default_think_time_ms,omitempty"`
}

type ConvertStats struct {
	Format            string   `json:"format"`
	CollectionVersion string   `json:"collection_version,omitempty"`
	Folders           int      `json:"folders"`
	TotalRequests     int      `json:"total_requests"`
	ConvertedActions  int      `json:"converted_actions"`
	VariablesDetected int      `json:"variables_detected"`
	EnvironmentValues int      `json:"environment_values,omitempty"`
	AuthSchemes       []string `json:"auth_schemes,omitempty"`
	UnsupportedBodies int      `json:"unsupported_bodies"`
	Assertions        int      `json:"assertions"`
	Extractors        int      `json:"extractors,omitempty"`
	Correlations      int      `json:"correlations,omitempty"`
	SkippedItems      []string `json:"skipped_items,omitempty"`
}

// Correlation records a response→request chain detected from a Postman test
// script (pm.<scope>.set("var", pm.response...)). It mirrors the OpenAPI
// importer's Correlation so the API layer can feed both through paramize.
type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ExtractType string `json:"extract_type"`
	SampleValue string `json:"sample_value,omitempty"`
	FoundInStep int    `json:"found_in_step"`
	UsedInSteps []int  `json:"used_in_steps"`
}

type ReviewSummary struct {
	CollectionName  string   `json:"collection_name,omitempty"`
	EnvironmentName string   `json:"environment_name,omitempty"`
	SourceFiles     []string `json:"source_files,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	Variables       []string `json:"variables,omitempty"`
	Requests        []string `json:"requests,omitempty"`
	AuthSchemes     []string `json:"auth_schemes,omitempty"`
}

type ConvertResult struct {
	Script       *dsl.Script        `json:"script"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Correlations []Correlation      `json:"correlations,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	Summary      ReviewSummary      `json:"summary"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`
	// finalization is deliberately package-private and excluded from the wire.
	// Raw requirement values are cleared before it is attached.
	finalization *postmanFinalization

	// ProtectedRequirements is an in-memory, one-shot adapter-to-API handoff.
	// Its contents are never part of the JSON conversion result.
	ProtectedRequirements credentialpolicy.ProtectedRequirements `json:"-"`
}

type collection struct {
	Info     collectionInfo `json:"info"`
	Item     []item         `json:"item"`
	Variable []variable     `json:"variable"`
	Auth     *auth          `json:"auth"`
	Event    []event        `json:"event"`
}

type collectionInfo struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type item struct {
	Name    string     `json:"name"`
	Item    []item     `json:"item"`
	Request requestRaw `json:"request"`
	Event   []event    `json:"event"`
	Auth    *auth      `json:"auth"`
}

type event struct {
	Listen string      `json:"listen"`
	Script eventScript `json:"script"`
}

type eventScript struct {
	Type string   `json:"type"`
	Exec []string `json:"exec"`
}

type requestRaw struct {
	Method string     `json:"method"`
	Header []header   `json:"header"`
	URL    postmanURL `json:"url"`
	Body   *body      `json:"body"`
	Auth   *auth      `json:"auth"`
	raw    string
}

type header struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

type body struct {
	Mode       string          `json:"mode"`
	Raw        string          `json:"raw"`
	URLEncoded []keyValueEntry `json:"urlencoded"`
	FormData   []keyValueEntry `json:"formdata"`
	GraphQL    *graphqlBody    `json:"graphql"`
}

type keyValueEntry struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Type        string `json:"type"`
	ContentType string `json:"contentType"`
	Src         any    `json:"src"`
	Disabled    bool   `json:"disabled"`
}

type graphqlBody struct {
	Query     string `json:"query"`
	Variables string `json:"variables"`
}

type variable struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled"`
}

type environmentFile struct {
	Name   string                `json:"name"`
	Values []environmentVariable `json:"values"`
}

type environmentVariable struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Enabled  *bool  `json:"enabled"`
	Disabled bool   `json:"disabled"`
}

type auth struct {
	Type       string                     `json:"type"`
	Bearer     []keyValueEntry            `json:"bearer"`
	Basic      []keyValueEntry            `json:"basic"`
	Digest     []keyValueEntry            `json:"digest"`
	NTLM       []keyValueEntry            `json:"ntlm"`
	APIKey     []keyValueEntry            `json:"apikey"`
	OAuth2     []keyValueEntry            `json:"oauth2"`
	AWSV4      []keyValueEntry            `json:"awsv4"`
	Hawk       []keyValueEntry            `json:"hawk"`
	RawEntries map[string][]keyValueEntry `json:"-"`
}

func (a *auth) UnmarshalJSON(data []byte) error {
	type authWire auth
	var decoded authWire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	decoded.RawEntries = make(map[string][]keyValueEntry)
	for key, payload := range raw {
		if strings.EqualFold(key, "type") {
			continue
		}
		var entries []keyValueEntry
		if err := json.Unmarshal(payload, &entries); err == nil {
			decoded.RawEntries[strings.ToLower(strings.TrimSpace(key))] = entries
		}
	}
	*a = auth(decoded)
	return nil
}

func (a *auth) entriesForType() []keyValueEntry {
	if a == nil {
		return nil
	}
	return a.RawEntries[strings.ToLower(strings.TrimSpace(a.Type))]
}

type postmanURL struct {
	Raw      string          `json:"raw"`
	Protocol string          `json:"protocol"`
	Host     []string        `json:"host"`
	Port     string          `json:"port"`
	Path     []string        `json:"path"`
	Query    []keyValueEntry `json:"query"`
	Hash     string          `json:"hash"`
	Variable []keyValueEntry `json:"variable"`
}

func (u *postmanURL) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		u.Raw = raw
		return nil
	}
	type alias postmanURL
	var parsed alias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*u = postmanURL(parsed)
	return nil
}

func (r *requestRaw) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		r.Method = "GET"
		r.URL = postmanURL{Raw: raw}
		r.raw = raw
		return nil
	}
	type alias requestRaw
	var parsed alias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*r = requestRaw(parsed)
	return nil
}

type requestItem struct {
	Name          string
	Folder        string
	Request       requestRaw
	Events        []event
	InheritedAuth *auth
}

var variablePattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

func Convert(req ImportRequest) (*ConvertResult, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errors.New("content is required")
	}

	// A Postman collection is attacker-supplied JSON. encoding/json silently
	// keeps the last of a duplicate key, so a collection can present one request
	// to a reviewer and convert another — including one whose auth block differs.
	// Reject that ambiguity before decoding.
	if err := srcparse.ValidateJSON([]byte(content), srcparse.Limits{}); err != nil {
		return nil, fmt.Errorf("invalid Postman collection: %w", err)
	}
	environmentContent := strings.TrimSpace(req.EnvironmentContent)
	if environmentContent != "" {
		// Environment values participate in the same executable-variable
		// resolution as collection values. Reject duplicate-key ambiguity at
		// this boundary too, rather than letting encoding/json silently choose
		// a different credential value than the source review appears to show.
		if err := srcparse.ValidateJSON([]byte(environmentContent), srcparse.Limits{}); err != nil {
			return nil, fmt.Errorf("invalid Postman environment: %w", err)
		}
	}
	var col collection
	if err := json.Unmarshal([]byte(content), &col); err != nil {
		return nil, fmt.Errorf("invalid Postman collection: %w", err)
	}
	if len(col.Item) == 0 {
		return nil, errors.New("Postman collection contains no items")
	}

	requests := []requestItem{}
	folders := 0
	collectItems(col.Item, "", col.Event, col.Auth, &requests, &folders)
	if len(requests) == 0 {
		return nil, errors.New("Postman collection contains no requests")
	}

	var diagnostics diag.Collector
	sourceFileName := safePostmanSourceFileName(req.CollectionFileName, "postman_collection.json")
	environmentFileName := ""
	if strings.TrimSpace(req.EnvironmentContent) != "" {
		environmentFileName = safePostmanSourceFileName(req.EnvironmentFileName, "postman_environment.json")
		if environmentFileName == sourceFileName {
			environmentFileName = "postman_environment.json"
			if environmentFileName == sourceFileName {
				environmentFileName = "environment_" + sourceFileName
			}
		}
	}
	sources := map[string]string{sourceFileName: content}
	if environmentFileName != "" {
		sources[environmentFileName] = req.EnvironmentContent
	}
	sourceMappings := make([]sourceview.Mapping, 0)
	collectionVars := collectionVariables(col.Variable)
	envVars := environmentVariables(req.EnvironmentContent, &diagnostics)
	environmentName := postmanEnvironmentName(req.EnvironmentContent)
	if environmentFileName != "" {
		sourceMappings = append(sourceMappings, sourceview.Mapping{
			SourceFile: environmentFileName,
			StartLine:  1,
			EndLine:    strings.Count(req.EnvironmentContent, "\n") + 1,
			Function:   convertutil.FirstNonEmpty(environmentName, "Postman environment"),
			TargetKind: "environment",
			Status:     "converted",
			Notes:      fmt.Sprintf("Selected Postman environment supplied %d enabled value(s) to collection variable resolution.", len(envVars)),
		})
	}
	scopedVars := newPostmanScopedVariables(collectionVars, envVars)
	sourceVars, effectiveLiteralAssignments := scopedVars.effectiveValues()
	variableBindings := resolvePostmanVariableBindings(sourceVars, &diagnostics)
	effectiveLiteralAssignmentNames := postmanEffectiveLiteralAssignmentNames(collectionVars, envVars, requests)
	responseAssignmentNames := postmanResponseAssignmentNames(requests)
	parameterlessLiteralNames := postmanLiteralOnlyAssignmentNames(effectiveLiteralAssignmentNames, responseAssignmentNames)
	variableBindings.Lookup.inlineLiterals = unionPostmanVariableSets(parameterlessLiteralNames, effectiveLiteralAssignments)
	vars := variableBindings.Lookup
	baseURL, warnings := deriveBaseURL(requests, vars, req.Options.BaseURL)
	scriptName := sanitizeName(convertutil.FirstNonEmpty(req.Options.ScriptName, req.Name, col.Info.Name, "Imported_Postman_Collection"))
	authSchemes := sortedAuthSchemes(requests, col.Auth)

	result := &ConvertResult{
		Script: &dsl.Script{
			Name:          scriptName,
			Version:       "1.0.0",
			Protocol:      "http",
			GeneratedBy:   "apexload-postman-converter",
			GeneratedFrom: "postman",
			CreatedAt:     time.Now(),
			Config: dsl.ScriptConfig{
				BaseURL: baseURL,
				ThinkTime: dsl.ThinkTimeConfig{
					Type:   "fixed",
					MeanMs: float64(firstPositive(req.Options.DefaultThinkTimeMs, 1000)),
				},
				Pacing:   dsl.PacingConfig{Type: "none"},
				RunLogic: dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "none"},
				// Unset: the scenario continue-on-error policy decides.
				ErrorHandling: dsl.ErrorHandlingConfig{},
				Log:           dsl.LogConfig{Level: "standard"},
				HTTP:          dsl.HTTPConfig{DefaultHeaders: map[string]string{"Accept": "application/json"}},
			},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source:           "postman",
				SourceScriptName: col.Info.Name,
				Preserved: map[string]any{
					"collection_schema":       col.Info.Schema,
					"collection_source_file":  sourceFileName,
					"environment_source_file": environmentFileName,
					"environment_name":        environmentName,
					"environment_values":      len(envVars),
				},
			}},
		},
		Warnings: warnings,
		Stats: ConvertStats{
			Format:            "postman",
			CollectionVersion: strings.TrimPrefix(col.Info.Schema, "https://schema.getpostman.com/json/collection/"),
			Folders:           folders,
			TotalRequests:     len(requests),
			EnvironmentValues: len(envVars),
			AuthSchemes:       authSchemes,
		},
		Summary: ReviewSummary{
			CollectionName:  col.Info.Name,
			EnvironmentName: environmentName,
			SourceFiles:     postmanSourceFiles(sourceFileName, environmentFileName),
			BaseURL:         baseURL,
			AuthSchemes:     authSchemes,
		},
	}

	result.Script.Parameters = buildParameters(postmanRuntimeValuesWithoutInlinedLiterals(variableBindings.RuntimeValues, parameterlessLiteralNames), baseURL)
	result.Script.Parameters = mergeParameters(result.Script.Parameters, postmanParametersWithoutInlinedLiterals(pathVariableParameters(requests), parameterlessLiteralNames))
	if convertutil.BoolOption(req.Options.UseParameterDataSet, true) {
		result.Script.Parameters, result.Script.DataSources = postmanParameterDataSource(
			result.Script.Parameters,
			variableBindings.ExcludeFromDataSource,
		)
	}
	result.Stats.VariablesDetected = len(result.Script.Parameters)
	result.Summary.Variables = parameterNames(result.Script.Parameters)
	reportUnsupportedPostmanAuth(col.Auth, &diagnostics, "collection")

	// The dashboard sends this option explicitly (and defaults it on). Preserve
	// compatibility for API clients that predate the option and omit it.
	sanitizeSensitive := convertutil.BoolOption(req.Options.SanitizeSensitive, false)
	includeAssertions := convertutil.BoolOption(req.Options.IncludeAssertions, true)
	requestPipeline := newPostmanRequestPipeline(
		variableBindings.SourceValues,
		sanitizeSensitive,
		sourceFileName,
		postmanSourceVariablePaths(collectionVars, envVars, sourceFileName, environmentFileName),
		variableBindings.Dependencies,
	)
	chainItems := make([]postmanChainItem, 0, len(requests))
	sourceLineIndex := sourceview.NewLineIndex(content)
	eventSourceRedacted := false
	eventSourceNames := make(map[string]struct{}, len(sources))
	for name := range sources {
		eventSourceNames[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	for index, item := range requests {
		scopedVars.clearLocal()
		applyPostmanScopedEventAssignmentsForListen(scopedVars, item, "prerequest", &diagnostics)
		sourceVars, effectiveLiteralAssignments = scopedVars.effectiveValues()
		currentBindings := resolvePostmanVariableBindings(sourceVars, &diagnostics)
		currentBindings.Lookup.inlineLiterals = unionPostmanVariableSets(parameterlessLiteralNames, effectiveLiteralAssignments)
		vars = currentBindings.Lookup
		requestPipeline.updateSourceVariables(currentBindings.SourceValues, currentBindings.Dependencies)
		reportPostmanEvents(item, &diagnostics)
		preservePostmanEventSources(result.Script, item)

		// Extract JS scripts to virtual review files
		for i, ev := range item.Events {
			if len(ev.Script.Exec) == 0 {
				continue
			}
			scriptContent := strings.Join(ev.Script.Exec, "\n")
			listenType := convertutil.FirstNonEmpty(ev.Listen, "script")
			filename := uniquePostmanReviewFileName(
				fmt.Sprintf("%s_%s_%d.js", sanitizeName(item.Name), listenType, i),
				eventSourceNames,
			)
			publicScript := disclosureSafePostmanEventSource(scriptContent)
			sources[filename] = publicScript
			eventSourceRedacted = eventSourceRedacted || publicScript != scriptContent
			sourceMappings = append(sourceMappings, sourceview.Mapping{
				SourceFile: filename,
				StartLine:  1,
				EndLine:    len(ev.Script.Exec),
				Function:   postmanItemLocation(item) + " / " + listenType,
				TargetKind: "review",
				Status:     "review_only",
				Notes:      fmt.Sprintf("Postman %s script preserved for manual review and translation.", listenType),
			})
		}

		action, assets, actionWarnings, invalidMultipartMetadata := buildAction(index, item, col.Auth, baseURL, vars, includeAssertions, &diagnostics, requestPipeline.registry)
		if action.Name == "" {
			result.Stats.SkippedItems = append(result.Stats.SkippedItems, item.Name)
			diagnostics.Add(diag.Warning, "Postman item skipped", postmanItemLocation(item), "request item could not be converted into an executable action")
			sourceMappings = append(sourceMappings, postmanSourceMapping(sourceFileName, content, sourceLineIndex, item, "", postmanItemLocation(item), "request", "review_only", "Postman item could not be converted into an executable action."))
			continue
		}
		hasUnsupportedBody := item.Request.Body != nil && unsupportedBodyMode(item.Request.Body.Mode)
		applyPostmanImportReview(
			&action,
			item,
			len(assets) != 0,
			hasUnsupportedBody,
			currentBindings.ReviewRequired,
			invalidMultipartMetadata,
			&diagnostics,
		)
		actionPath := fmt.Sprintf("actions[%d]", len(result.Script.Actions))
		finalizedAction, finalizeErr := requestPipeline.FinalizeAction(index, item, action, actionPath)
		if finalizeErr != nil {
			return nil, fmt.Errorf("finalize Postman request %q: %w", postmanItemLocation(item), finalizeErr)
		}
		action = finalizedAction
		result.Warnings = append(result.Warnings, actionWarnings...)
		result.Script.DataSources = mergePostmanDataSources(result.Script.DataSources, assets)
		if action.Body == "" && item.Request.Body != nil && unsupportedBodyMode(item.Request.Body.Mode) {
			result.Stats.UnsupportedBodies++
		}
		if len(action.Assertions) > 0 {
			result.Stats.Assertions += len(action.Assertions)
		}
		actionIndex := len(result.Script.Actions)
		result.Script.Actions = append(result.Script.Actions, action)
		sourceMappings = append(sourceMappings, postmanSourceMapping(sourceFileName, content, sourceLineIndex, item, fmt.Sprintf("actions[%d]", actionIndex), action.Name, "action", "converted", "Postman request was converted into a generated APEX Load action."))
		result.Summary.Requests = append(result.Summary.Requests, action.Method+" "+action.Target)
		result.Stats.ConvertedActions++
		responseWrites := applyPostmanScopedEventAssignmentsForListen(scopedVars, item, "test", &diagnostics)
		// pm.variables is request-local. Clear it after post-response scripts before
		// choosing which persistent response write supplies later request templates.
		scopedVars.clearLocal()
		chainItems = append(chainItems, postmanChainItem{
			ActionIndex: actionIndex,
			Item:        item,
			Extractors:  scopedVars.effectiveResponseExtractors(responseWrites),
		})
	}
	if len(result.Script.Actions) == 0 {
		return nil, errors.New("Postman collection contains no supported requests")
	}
	// Retain deterministic literal assignments as authoring parameters after
	// applying them in source order. This preserves the existing review surface
	// without leaking a later assignment backward into earlier requests.
	sourceVars, _ = scopedVars.effectiveValues()
	finalBindings := resolvePostmanVariableBindings(sourceVars, &diagnostics)
	result.Script.Parameters = mergeParameters(result.Script.Parameters, buildParameters(postmanRuntimeValuesWithoutInlinedLiterals(finalBindings.RuntimeValues, parameterlessLiteralNames), baseURL))
	// Turn pm.<scope>.set("var", pm.response...) test-script assignments into
	// response extractors on the producing action so later {{var.X}} references
	// resolve at replay (Postman's response→request chaining idiom).
	result.Correlations = applyPostmanResponseChaining(result.Script, chainItems, &diagnostics)
	result.Stats.Correlations = len(result.Correlations)
	for i := range result.Script.Actions {
		result.Stats.Extractors += len(result.Script.Actions[i].Extractors)
	}
	credentialVariableNames := requestPipeline.consumedSourceVariableNames()
	if len(credentialVariableNames) > 0 &&
		result.Script.Metadata != nil &&
		result.Script.Metadata.Import != nil {
		if result.Script.Metadata.Import.Preserved == nil {
			result.Script.Metadata.Import.Preserved = make(map[string]any)
		}
		result.Script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey] =
			append([]string(nil), credentialVariableNames...)
	}
	result.Stats.VariablesDetected = len(result.Script.Parameters)
	result.Summary.Variables = parameterNames(result.Script.Parameters)
	finalization, err := requestPipeline.FinalizeScript(result.Script)
	if err != nil {
		return nil, fmt.Errorf("finalize Postman script: %w", err)
	}
	result.Script = finalization.Result.Script
	if sanitizeSensitive && result.Script != nil && result.Script.Metadata != nil && result.Script.Metadata.Import != nil {
		if result.Script.Metadata.Import.Preserved == nil {
			result.Script.Metadata.Import.Preserved = make(map[string]any)
		}
		delete(result.Script.Metadata.Import.Preserved, dsl.ImportPreservedCredentialPolicyKey)
		result.Script.Metadata.Import.Preserved["credential_projection"] = string(credentialpolicy.SecretReference)
	}
	result.Summary.BaseURL = result.Script.Config.BaseURL
	result.finalization = finalization
	protectedRequirements := finalization.takeProtectedRequirements()
	result.ProtectedRequirements.Store(protectedRequirements)
	for _, item := range finalization.Result.Diagnostics {
		diagnostics.Add(item.Severity, item.Element, item.Location, item.Detail)
	}
	if gap := result.Stats.TotalRequests - result.Stats.ConvertedActions - len(result.Stats.SkippedItems); gap > 0 {
		diagnostics.Add(diag.Warning, "Postman completeness guard", "collection", fmt.Sprintf("%d request item(s) were present but not converted or explicitly skipped", gap))
	}
	result.Unsupported = diagnostics.Items()
	if eventSourceRedacted {
		// Event scripts are embedded in the collection JSON as well as exposed as
		// virtual .js files. Omit the public collection view too, otherwise an
		// arbitrary literal in residual JavaScript could bypass field-aware
		// source redaction through the encoded exec array.
		sources[sourceFileName] = disclosureSafePostmanEventSource(sources[sourceFileName])
	}
	if variableBindings.OmitSourceReview {
		sources[sourceFileName] = "// [POSTMAN SOURCE OMITTED: VARIABLE GRAPH REQUIRES REVIEW]"
		if environmentFileName != "" {
			sources[environmentFileName] = "// [POSTMAN ENVIRONMENT OMITTED: VARIABLE GRAPH REQUIRES REVIEW]"
		}
	}
	protectedSourceRedacted := maskPostmanProtectedSourceValues(sources, protectedRequirements)
	protectedSourceRedacted = requestPipeline.redactSourceReviewBodies(sources) ||
		protectedSourceRedacted
	protectedSourceRedacted = maskPostmanSourceVariableValues(
		sources,
		credentialVariableNames,
		requestPipeline.sourceVariables,
	) || protectedSourceRedacted
	protectedSourceRedacted = maskPostmanLiteralSourceValues(
		sources,
		variableBindings.InvalidSourceValues,
	) || protectedSourceRedacted
	for index := range protectedRequirements {
		protectedRequirements[index].Value = ""
	}
	result.SourceReview = sourceview.Build(sources, sourceMappings)
	if protectedSourceRedacted || eventSourceRedacted || variableBindings.OmitSourceReview {
		markPostmanSourceReviewRedacted(result.SourceReview)
	}
	scrubPostmanSourceReview(result.SourceReview)
	return result, nil
}

func postmanSourceMapping(sourceFile string, content string, lineIndex sourceview.LineIndex, item requestItem, targetPath string, targetName string, targetKind string, status string, notes string) sourceview.Mapping {
	line := postmanItemLine(content, lineIndex, item)
	return sourceview.Mapping{
		SourceFile: sourceFile,
		StartLine:  line,
		EndLine:    line,
		Function:   postmanItemLocation(item),
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     status,
		Notes:      notes,
	}
}

func postmanItemLine(content string, lineIndex sourceview.LineIndex, item requestItem) int {
	for _, needle := range []string{`"` + item.Name + `"`, item.Request.URL.Raw, item.Request.Method} {
		if strings.TrimSpace(needle) == "" {
			continue
		}
		if offset := strings.Index(content, needle); offset >= 0 {
			return lineIndex.LineForOffset(offset)
		}
	}
	return 1
}

func collectItems(items []item, folder string, inheritedEvents []event, inheritedAuth *auth, requests *[]requestItem, folders *int) {
	for _, item := range items {
		events := append([]event{}, inheritedEvents...)
		events = append(events, item.Event...)
		if len(item.Item) > 0 {
			nextFolder := strings.TrimSpace(item.Name)
			if folder != "" && nextFolder != "" {
				nextFolder = folder + " / " + nextFolder
			} else if nextFolder == "" {
				nextFolder = folder
			}
			(*folders)++
			collectItems(item.Item, nextFolder, events, inheritedPostmanAuth(inheritedAuth, item.Auth), requests, folders)
			continue
		}
		if item.Request.URL.Raw == "" && item.Request.raw == "" && item.Request.Method == "" {
			continue
		}
		*requests = append(*requests, requestItem{
			Name: item.Name, Folder: folder, Request: item.Request, Events: events, InheritedAuth: inheritedAuth,
		})
	}
}

func inheritedPostmanAuth(parent *auth, child *auth) *auth {
	if child == nil {
		return parent
	}
	switch strings.ToLower(strings.TrimSpace(child.Type)) {
	case "", "inherit":
		return parent
	default:
		return child
	}
}

func collectionVariables(vars []variable) map[string]string {
	out := map[string]string{}
	for _, v := range vars {
		if v.Disabled || strings.TrimSpace(v.Key) == "" {
			continue
		}
		out[v.Key] = fmt.Sprint(v.Value)
	}
	return out
}

func environmentVariables(content string, diagnostics *diag.Collector) map[string]string {
	out := map[string]string{}
	content = strings.TrimSpace(content)
	if content == "" {
		return out
	}
	var env environmentFile
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman environment", "environment", "environment file could not be parsed: "+err.Error())
		}
		return out
	}
	for _, v := range env.Values {
		if v.Disabled || strings.TrimSpace(v.Key) == "" {
			continue
		}
		if v.Enabled != nil && !*v.Enabled {
			continue
		}
		out[v.Key] = fmt.Sprint(v.Value)
	}
	if len(out) > 0 && diagnostics != nil {
		diagnostics.Add(diag.Info, "Postman environment", convertutil.FirstNonEmpty(env.Name, "environment"), fmt.Sprintf("imported %d environment value(s)", len(out)))
	}
	return out
}

func postmanEnvironmentName(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	var env environmentFile
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		return ""
	}
	return strings.TrimSpace(env.Name)
}

func safePostmanSourceFileName(raw, fallback string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	name := path.Base(raw)
	if name == "" || name == "." || name == "/" {
		name = fallback
	}
	// Source-review paths are identifiers, not filesystem destinations. Keep
	// names readable while excluding control characters that can spoof rows.
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, name)
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}

func postmanSourceFiles(collectionFile, environmentFile string) []string {
	files := []string{collectionFile}
	if strings.TrimSpace(environmentFile) != "" {
		files = append(files, environmentFile)
	}
	return files
}

func postmanSourceVariablePaths(collectionValues, environmentValues map[string]string, collectionFile, environmentFile string) map[string]string {
	paths := make(map[string]string, len(collectionValues)+len(environmentValues))
	set := func(name, file string) {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(file) == "" {
			return
		}
		paths[name] = file
		if canonical := normalizeVariableName(name); canonical != "" {
			paths[canonical] = file
		}
		if canonical := postmanVariableCanonicalName(name); canonical != "" {
			paths[canonical] = file
		}
	}
	for name := range collectionValues {
		set(name, collectionFile)
	}
	// Environment values have higher Postman precedence, so overwrite an
	// identically named collection origin with the selected environment file.
	for name := range environmentValues {
		set(name, environmentFile)
	}
	return paths
}

func uniquePostmanReviewFileName(raw string, used map[string]struct{}) string {
	name := safePostmanSourceFileName(raw, "postman_event.js")
	extension := path.Ext(name)
	base := strings.TrimSuffix(name, extension)
	if strings.TrimSpace(base) == "" {
		base = "postman_event"
	}
	for suffix := 1; ; suffix++ {
		candidate := name
		if suffix > 1 {
			candidate = fmt.Sprintf("%s_%d%s", base, suffix, extension)
		}
		identity := strings.ToLower(strings.TrimSpace(candidate))
		if _, duplicate := used[identity]; duplicate {
			continue
		}
		used[identity] = struct{}{}
		return candidate
	}
}

func postmanParameterDataSource(params []dsl.Parameter, excludedSets ...map[string]struct{}) ([]dsl.Parameter, []dsl.DataSource) {
	if len(params) == 0 {
		return params, nil
	}
	var excluded map[string]struct{}
	if len(excludedSets) > 0 {
		excluded = excludedSets[0]
	}
	columns := make([]string, 0, len(params))
	values := make([]string, 0, len(params))
	seenColumns := map[string]struct{}{}
	for _, param := range params {
		if dsl.CanonicalParameterType(param.Type) != "list" || len(param.Values) == 0 {
			continue
		}
		column := normalizeVariableName(param.Name)
		if _, keepAsList := excluded[strings.ToLower(column)]; keepAsList {
			continue
		}
		if strings.TrimSpace(column) == "" || strings.Contains(param.Values[0], "{{") {
			continue
		}
		if _, seen := seenColumns[column]; seen {
			continue
		}
		seenColumns[column] = struct{}{}
		columns = append(columns, column)
		values = append(values, param.Values[0])
	}
	if len(columns) == 0 {
		return params, nil
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	_ = writer.Write(columns)
	_ = writer.Write(values)
	writer.Flush()

	next := make([]dsl.Parameter, len(params))
	for i, param := range params {
		next[i] = param
		column := normalizeVariableName(param.Name)
		if containsString(columns, column) {
			next[i].Values = nil
			next[i].DataSource = "postman"
			next[i].Column = column
			next[i].Selection = "sequential"
		}
	}
	return next, []dsl.DataSource{{
		Name:           "postman",
		Type:           "csv",
		InlineData:     buf.String(),
		Strategy:       "sequential",
		Delimiter:      ",",
		FirstRowHeader: true,
		Columns:        columns,
	}}
}

func removePostmanProtectedSourceVariables(
	parameters []dsl.Parameter,
	dataSources []dsl.DataSource,
	names []string,
) ([]dsl.Parameter, []dsl.DataSource) {
	if len(names) == 0 {
		return parameters, dataSources
	}
	protected := make(map[string]struct{}, len(names))
	for _, name := range names {
		if normalized := normalizeVariableName(name); normalized != "" {
			protected[strings.ToLower(normalized)] = struct{}{}
		}
	}
	filteredParameters := make([]dsl.Parameter, 0, len(parameters))
	for _, parameter := range parameters {
		if _, credential := protected[strings.ToLower(normalizeVariableName(parameter.Name))]; credential {
			continue
		}
		filteredParameters = append(filteredParameters, parameter)
	}

	filteredSources := make([]dsl.DataSource, 0, len(dataSources))
	for _, source := range dataSources {
		if !strings.EqualFold(source.Name, "postman") || !strings.EqualFold(source.Type, "csv") {
			filteredSources = append(filteredSources, source)
			continue
		}
		reader := csv.NewReader(strings.NewReader(source.InlineData))
		records, err := reader.ReadAll()
		if err != nil || len(records) == 0 {
			// This source is generated locally immediately above. If its shape
			// is unexpectedly unreadable, omit it rather than risk retaining a
			// protected source variable in an opaque inline payload.
			continue
		}
		header := records[0]
		keep := make([]int, 0, len(header))
		for index, column := range header {
			if _, credential := protected[strings.ToLower(normalizeVariableName(column))]; !credential {
				keep = append(keep, index)
			}
		}
		if len(keep) == 0 {
			continue
		}
		var buf bytes.Buffer
		writer := csv.NewWriter(&buf)
		for _, record := range records {
			filtered := make([]string, 0, len(keep))
			for _, index := range keep {
				value := ""
				if index < len(record) {
					value = record[index]
				}
				filtered = append(filtered, value)
			}
			_ = writer.Write(filtered)
		}
		writer.Flush()
		source.InlineData = buf.String()
		source.Columns = make([]string, 0, len(keep))
		for _, index := range keep {
			if index < len(header) {
				source.Columns = append(source.Columns, header[index])
			}
		}
		filteredSources = append(filteredSources, source)
	}
	return filteredParameters, filteredSources
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func preservePostmanEventSources(script *dsl.Script, item requestItem) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	existing, _ := script.Metadata.Import.Preserved["postman_event_sources"].([]map[string]string)
	for index, event := range item.Events {
		if len(event.Script.Exec) == 0 || !postmanEventRequiresReview(event) {
			continue
		}
		existing = append(existing, map[string]string{
			"source_ref": fmt.Sprintf("%s / event[%d]", postmanItemLocation(item), index),
			"listen":     event.Listen,
			"script":     strings.Join(event.Script.Exec, "\n"),
		})
	}
	if len(existing) != 0 {
		script.Metadata.Import.Preserved["postman_event_sources"] = existing
	}
}

func postmanEventRequiresReview(event event) bool {
	script := strings.Join(event.Script.Exec, "\n")
	if strings.EqualFold(strings.TrimSpace(event.Listen), "test") {
		if postmanSafeStatusTestPattern.MatchString(script) || postmanCorrelationScriptFullyTranslated(event) {
			return false
		}
	}
	return true
}

var postmanSetPattern = regexp.MustCompile(`pm\.(environment|collectionVariables|globals|variables)\.set\s*\(\s*["']([^"']+)["']\s*,\s*["']([^"']*)["']\s*\)`)

// postmanEffectiveLiteralAssignmentNames simulates the collection event walk
// and records only script literals that actually win scope resolution for an
// authored request. A lower-scope write that remains shadowed by a stable
// environment value must not turn that environment parameter into a collection-
// wide inline literal.
func postmanEffectiveLiteralAssignmentNames(
	collectionValues map[string]string,
	environmentValues map[string]string,
	requests []requestItem,
) map[string]struct{} {
	state := newPostmanScopedVariables(collectionValues, environmentValues)
	names := make(map[string]struct{})
	for _, request := range requests {
		state.clearLocal()
		applyPostmanScopedEventAssignmentsForListen(state, request, "prerequest", nil)
		_, effective := state.effectiveValues()
		for name := range effective {
			names[name] = struct{}{}
		}
		applyPostmanScopedEventAssignmentsForListen(state, request, "test", nil)
		state.clearLocal()
	}
	return names
}

func postmanResponseAssignmentNames(requests []requestItem) map[string]struct{} {
	names := make(map[string]struct{})
	for _, request := range requests {
		for _, ev := range request.Events {
			for _, occurrence := range postmanAssignmentOccurrences(ev) {
				if occurrence.extractor != nil {
					if name := postmanVariableCanonicalName(occurrence.name); name != "" {
						names[name] = struct{}{}
					}
				}
			}
		}
	}
	return names
}

func postmanLiteralOnlyAssignmentNames(literal, response map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(literal))
	for name := range literal {
		if _, dynamic := response[name]; !dynamic {
			result[name] = struct{}{}
		}
	}
	return result
}

type postmanAssignmentOccurrence struct {
	offset    int
	scope     postmanVariableScope
	name      string
	value     string
	literal   bool
	extractor *detectedExtractor
}

func postmanAssignmentOccurrences(event event) []postmanAssignmentOccurrence {
	script := strings.Join(event.Script.Exec, "\n")
	occurrences := make([]postmanAssignmentOccurrence, 0)
	for _, match := range postmanSetPattern.FindAllStringSubmatchIndex(script, -1) {
		if len(match) < 8 || match[2] < 0 || match[3] < 0 ||
			match[4] < 0 || match[5] < 0 || match[6] < 0 || match[7] < 0 {
			continue
		}
		scope, ok := parsePostmanVariableScope(script[match[2]:match[3]])
		name := strings.TrimSpace(script[match[4]:match[5]])
		if ok && name != "" {
			occurrences = append(occurrences, postmanAssignmentOccurrence{
				offset:  match[0],
				scope:   scope,
				name:    name,
				value:   script[match[6]:match[7]],
				literal: true,
			})
		}
	}
	if strings.EqualFold(strings.TrimSpace(event.Listen), "test") {
		aliases := make(map[string]struct{})
		for _, match := range postmanJSONAliasPattern.FindAllStringSubmatch(script, -1) {
			if len(match) > 1 {
				aliases[match[1]] = struct{}{}
			}
		}
		for _, match := range postmanSetJSONPattern.FindAllStringSubmatchIndex(script, -1) {
			if len(match) < 8 || match[2] < 0 || match[3] < 0 ||
				match[4] < 0 || match[5] < 0 || match[6] < 0 || match[7] < 0 {
				continue
			}
			scope, ok := parsePostmanVariableScope(script[match[2]:match[3]])
			name := strings.TrimSpace(script[match[4]:match[5]])
			if !ok || name == "" {
				continue
			}
			extractor := &detectedExtractor{
				varName:     normalizeVariableName(name),
				source:      "body",
				extractType: "jsonpath",
				expression:  jsonPathFromDotPath(script[match[6]:match[7]]),
			}
			occurrences = append(occurrences, postmanAssignmentOccurrence{
				offset: match[0], scope: scope, name: name, extractor: extractor,
			})
		}
		for _, match := range postmanSetHeaderPattern.FindAllStringSubmatchIndex(script, -1) {
			if len(match) < 8 || match[2] < 0 || match[3] < 0 ||
				match[4] < 0 || match[5] < 0 || match[6] < 0 || match[7] < 0 {
				continue
			}
			scope, ok := parsePostmanVariableScope(script[match[2]:match[3]])
			name := strings.TrimSpace(script[match[4]:match[5]])
			if !ok || name == "" {
				continue
			}
			extractor := &detectedExtractor{
				varName:     normalizeVariableName(name),
				source:      "header",
				extractType: "header",
				expression:  script[match[6]:match[7]],
			}
			occurrences = append(occurrences, postmanAssignmentOccurrence{
				offset: match[0], scope: scope, name: name, extractor: extractor,
			})
		}
		for _, match := range postmanSetAliasPattern.FindAllStringSubmatchIndex(script, -1) {
			if len(match) < 10 || match[2] < 0 || match[3] < 0 ||
				match[4] < 0 || match[5] < 0 || match[6] < 0 || match[7] < 0 ||
				match[8] < 0 || match[9] < 0 {
				continue
			}
			if _, ok := aliases[script[match[6]:match[7]]]; !ok {
				continue
			}
			scope, ok := parsePostmanVariableScope(script[match[2]:match[3]])
			name := strings.TrimSpace(script[match[4]:match[5]])
			if !ok || name == "" {
				continue
			}
			extractor := &detectedExtractor{
				varName:     normalizeVariableName(name),
				source:      "body",
				extractType: "jsonpath",
				expression:  jsonPathFromDotPath(script[match[8]:match[9]]),
			}
			occurrences = append(occurrences, postmanAssignmentOccurrence{
				offset: match[0], scope: scope, name: name, extractor: extractor,
			})
		}
	}
	sort.SliceStable(occurrences, func(left, right int) bool {
		return occurrences[left].offset < occurrences[right].offset
	})
	return occurrences
}

func postmanRuntimeValuesWithoutInlinedLiterals(values map[string]string, inline map[string]struct{}) map[string]string {
	if len(values) == 0 || len(inline) == 0 {
		return values
	}
	out := make(map[string]string, len(values))
	for name, value := range values {
		if _, assigned := inline[postmanVariableCanonicalName(name)]; assigned {
			continue
		}
		out[name] = value
	}
	return out
}

func postmanParametersWithoutInlinedLiterals(parameters []dsl.Parameter, inline map[string]struct{}) []dsl.Parameter {
	if len(parameters) == 0 || len(inline) == 0 {
		return parameters
	}
	out := make([]dsl.Parameter, 0, len(parameters))
	for _, parameter := range parameters {
		if _, assigned := inline[postmanVariableCanonicalName(parameter.Name)]; assigned {
			continue
		}
		out = append(out, parameter)
	}
	return out
}

func reportPostmanEvents(item requestItem, diagnostics *diag.Collector) {
	location := postmanItemLocation(item)
	for _, ev := range item.Events {
		listen := strings.ToLower(strings.TrimSpace(ev.Listen))
		if listen == "" {
			continue
		}
		switch listen {
		case "prerequest":
			diagnostics.Add(diag.Warning, "Postman pre-request script", location, "pre-request script is not executed during import; review auth signing, variable mutation, and correlation logic before replay")
		case "test":
			diagnostics.Add(diag.Warning, "Postman test script", location, "test script is not executed during import; translate assertions or side effects before replay")
		default:
			diagnostics.Add(diag.Info, "Postman event script", location, "event script "+listen+" is not executed during import")
		}
	}
}

var postmanSafeStatusTestPattern = regexp.MustCompile(`(?s)^\s*pm\.test\(\s*["'][^"']*["']\s*,\s*function\s*\(\s*\)\s*\{\s*pm\.response\.to\.have\.status\s*\(\s*([1-5][0-9]{2})\s*\)\s*;?\s*\}\s*\)\s*;?\s*$`)

// applyPostmanImportReview projects the intentionally narrow, source-script
// subset whose behavior is fully represented by the DSL and durably gates any
// residual behavior. Diagnostics alone are never treated as execution safety.
func applyPostmanImportReview(
	action *dsl.Action,
	item requestItem,
	hasFileBinding bool,
	unsupportedBody bool,
	variableGraphReview bool,
	invalidMultipartMetadata bool,
	diagnostics *diag.Collector,
) {
	if action == nil {
		return
	}
	reasonSet := map[string]struct{}{}
	sourceRefSet := map[string]struct{}{}
	location := postmanItemLocation(item)
	statuses := map[int]struct{}{}
	for index, ev := range item.Events {
		if len(ev.Script.Exec) == 0 {
			continue
		}
		sourceRefSet[fmt.Sprintf("%s / event[%d]", location, index)] = struct{}{}
		script := strings.Join(ev.Script.Exec, "\n")
		if strings.EqualFold(strings.TrimSpace(ev.Listen), "test") {
			if match := postmanSafeStatusTestPattern.FindStringSubmatch(script); len(match) == 2 {
				status, err := strconv.Atoi(match[1])
				if err == nil {
					statuses[status] = struct{}{}
					if diagnostics != nil {
						diagnostics.Add(diag.Info, "Postman test assertion", location, fmt.Sprintf("translated exact response status assertion %d", status))
					}
					continue
				}
			}
			if postmanCorrelationScriptFullyTranslated(ev) {
				if diagnostics != nil {
					diagnostics.Add(diag.Info, "Postman correlation script", location, "translated the complete response-capture script into deterministic extractors")
				}
				continue
			}
		}
		reasonSet["unsupported_source_script"] = struct{}{}
	}
	if len(statuses) == 1 {
		for status := range statuses {
			action.Assertions = replacePostmanDefaultStatusAssertion(action.Assertions, status)
		}
	} else if len(statuses) > 1 {
		reasonSet["conflicting_source_assertions"] = struct{}{}
	}
	if _, unsupportedAuth := action.Metadata["postman_auth_review_only"]; unsupportedAuth {
		reasonSet["unsupported_auth"] = struct{}{}
		sourceRefSet[location+" / auth"] = struct{}{}
	}
	if unsupportedBody {
		reasonSet["unsupported_body"] = struct{}{}
		sourceRefSet[location+" / body"] = struct{}{}
	}
	if hasFileBinding {
		reasonSet["missing_file_binding"] = struct{}{}
		sourceRefSet[location+" / body/formdata"] = struct{}{}
	}
	if variableGraphReview {
		reasonSet["source_variable_graph_limit"] = struct{}{}
		sourceRefSet["collection/environment variables"] = struct{}{}
	}
	if invalidMultipartMetadata {
		reasonSet["invalid_multipart_metadata"] = struct{}{}
		sourceRefSet[location+" / body/formdata"] = struct{}{}
	}
	if len(reasonSet) == 0 {
		return
	}

	reasons := sortedPostmanSet(reasonSet)
	sourceRefs := sortedPostmanSet(sourceRefSet)
	blocker := "import_review_required"
	resolution := "replace_behavior_and_remove_gate"
	if len(reasons) == 1 && reasons[0] == "missing_file_binding" {
		blocker = "import_missing_file_bindings"
		resolution = "bind_file_assets_and_remove_gate"
	}
	action.Metadata["execution_blocker"] = blocker
	action.Metadata["import_review"] = map[string]any{
		"version":      1,
		"source":       "postman",
		"reason_codes": reasons,
		"source_refs":  sourceRefs,
		"resolution":   resolution,
	}
}

func replacePostmanDefaultStatusAssertion(assertions []dsl.Assertion, status int) []dsl.Assertion {
	out := make([]dsl.Assertion, 0, len(assertions)+1)
	for _, assertion := range assertions {
		if assertion.Type == "status_code_range" && assertion.Min == 200 && assertion.Max == 299 {
			continue
		}
		if assertion.Type == "status_code" && assertion.Expected == status {
			return assertions
		}
		out = append(out, assertion)
	}
	return append(out, dsl.Assertion{Type: "status_code", Expected: status})
}

func sortedPostmanSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func postmanItemLocation(item requestItem) string {
	if item.Folder != "" && item.Name != "" {
		return item.Folder + " / " + item.Name
	}
	return convertutil.FirstNonEmpty(item.Name, item.Folder, "Postman request")
}

func deriveBaseURL(requests []requestItem, vars postmanVariableLookup, override string) (string, []string) {
	if strings.TrimSpace(override) != "" {
		return strings.TrimRight(strings.TrimSpace(override), "/"), nil
	}
	for _, key := range []string{"baseUrl", "base_url", "host", "url"} {
		if value, _, ok := vars.Get(key); ok && looksAbsoluteURL(strings.TrimSpace(value)) {
			value = strings.TrimSpace(value)
			return strings.TrimRight(value, "/"), nil
		}
	}
	for _, item := range requests {
		raw := expandVariables(rawURL(item.Request.URL), vars)
		parsed, err := url.Parse(raw)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			return parsed.Scheme + "://" + parsed.Host, nil
		}
	}
	return "", []string{"base URL could not be derived automatically; imported request targets may remain absolute or variable-based"}
}

func buildParameters(vars map[string]string, baseURL string) []dsl.Parameter {
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	params := make([]dsl.Parameter, 0, len(keys))
	for _, key := range keys {
		value := vars[key]
		if value == "" || (looksAbsoluteURL(value) && strings.TrimRight(value, "/") == strings.TrimRight(baseURL, "/")) {
			continue
		}
		params = append(params, dsl.Parameter{
			Name:   normalizeVariableName(key),
			Type:   "list",
			Update: "once",
			Values: []string{tmpl.TranslateDynamic(value, nil, "collection variable "+key)},
		})
	}
	return params
}

func pathVariableParameters(requests []requestItem) []dsl.Parameter {
	values := map[string]string{}
	for _, item := range requests {
		for _, variable := range item.Request.URL.Variable {
			if variable.Disabled || strings.TrimSpace(variable.Key) == "" {
				continue
			}
			values[normalizeVariableName(variable.Key)] = convertutil.FirstNonEmpty(variable.Value, "sample-"+normalizeVariableName(variable.Key))
		}
		for _, name := range pathVariableNames(rawURL(item.Request.URL)) {
			if _, ok := values[normalizeVariableName(name)]; !ok {
				values[normalizeVariableName(name)] = "sample-" + normalizeVariableName(name)
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	params := make([]dsl.Parameter, 0, len(keys))
	for _, key := range keys {
		params = append(params, dsl.Parameter{Name: key, Type: "list", Update: "once", Values: []string{values[key]}})
	}
	return params
}

func mergeParameters(existing []dsl.Parameter, extra []dsl.Parameter) []dsl.Parameter {
	if len(extra) == 0 {
		return existing
	}
	seen := map[string]struct{}{}
	out := append([]dsl.Parameter{}, existing...)
	for _, param := range existing {
		seen[strings.ToLower(param.Name)] = struct{}{}
	}
	for _, param := range extra {
		if _, ok := seen[strings.ToLower(param.Name)]; ok {
			continue
		}
		out = append(out, param)
		seen[strings.ToLower(param.Name)] = struct{}{}
	}
	return out
}

func buildAction(index int, item requestItem, collectionAuth *auth, baseURL string, vars postmanVariableLookup, includeAssertions bool, diagnostics *diag.Collector, credentialNames *credentialpolicy.NameRegistry) (dsl.Action, []dsl.DataSource, []string, bool) {
	req := item.Request
	location := postmanItemLocation(item)
	method := strings.ToUpper(convertutil.FirstNonEmpty(req.Method, "GET"))
	raw := rawURL(req.URL)
	target := toTarget(raw, baseURL, vars, diagnostics, location)
	headers := map[string]string{}
	headerList := make(dsl.HeaderList, 0, len(req.Header)+1)
	for _, h := range req.Header {
		if h.Disabled || strings.TrimSpace(h.Key) == "" {
			continue
		}
		// Keep the effective source value intact until the shared credential
		// projector sees this ordered occurrence. Pre-rewriting to {{env.*}}
		// would suppress the protected requirement and lose migration material.
		value := rewriteVariablesWithContext(h.Value, vars, diagnostics, location)
		headerList = append(headerList, dsl.HeaderEntry{Name: h.Key, Value: value})
		setHeaderSummary(headers, h.Key, value)
	}

	existingContentType := headerValue(headers, "Content-Type")
	assetPrefix := fmt.Sprintf("postman_%d_%s", index+1, actionName(index, item))
	bodyText, contentType, assets, warnings, invalidMultipartMetadata := requestBody(req.Body, existingContentType, assetPrefix, diagnostics, location)
	if contentType != "" && headerValue(headers, "Content-Type") == "" {
		headerList = append(headerList, dsl.HeaderEntry{Name: "Content-Type", Value: contentType})
		setHeaderSummary(headers, "Content-Type", contentType)
	} else if contentType != "" && strings.Contains(strings.ToLower(contentType), "multipart/form-data") &&
		multipartBoundary(existingContentType) == "" {
		headerList = replaceHeaderListValue(headerList, "Content-Type", contentType)
		setHeaderSummary(headers, "Content-Type", contentType)
	}
	// JSON/XML/form/multipart literals are projected by requestimport. Do not
	// decode, re-marshal, or replace fields before that byte-preserving stage.
	bodyText = rewriteVariablesWithContext(bodyText, vars, diagnostics, location)

	action := dsl.Action{
		Name:       actionName(index, item),
		Type:       "request",
		Method:     method,
		Target:     target,
		Headers:    headers,
		HeaderList: headerList,
		Body:       bodyText,
		Metadata: map[string]any{
			"postman_item": item.Name,
		},
	}
	if item.Folder != "" {
		action.Metadata["postman_folder"] = item.Folder
	}
	inheritedAuth := item.InheritedAuth
	if inheritedAuth == nil {
		inheritedAuth = collectionAuth
	}
	applyPostmanAuth(
		&action,
		req.Auth,
		inheritedAuth,
		vars,
		diagnostics,
		location,
		fmt.Sprintf("requests[%d]\x00%s", index, location),
		credentialNames,
	)
	if len(action.Headers) == 0 {
		action.Headers = nil
	}
	if len(action.HeaderList) == 0 {
		action.HeaderList = nil
	}
	if includeAssertions {
		action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code_range", Min: 200, Max: 299})
	}
	return action, assets, warnings, invalidMultipartMetadata
}

func setHeaderSummary(headers map[string]string, name string, value string) {
	for existing := range headers {
		if strings.EqualFold(existing, name) {
			delete(headers, existing)
		}
	}
	headers[name] = value
}

func replaceHeaderListValue(headers dsl.HeaderList, name string, value string) dsl.HeaderList {
	out := make(dsl.HeaderList, 0, len(headers)+1)
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			continue
		}
		out = append(out, header)
	}
	return append(out, dsl.HeaderEntry{Name: name, Value: value})
}

func rawURL(u postmanURL) string {
	if strings.TrimSpace(u.Raw) != "" {
		raw := strings.TrimSpace(u.Raw)
		if len(u.Query) > 0 {
			raw = reconcileRawQuery(raw, u.Query)
		}
		if hash := strings.TrimPrefix(strings.TrimSpace(u.Hash), "#"); hash != "" && !strings.Contains(raw, "#") {
			raw += "#" + hash
		}
		return raw
	}
	host := strings.Join(u.Host, ".")
	path := strings.Join(u.Path, "/")
	if host == "" && path == "" {
		return ""
	}
	prefix := ""
	if u.Protocol != "" && host != "" {
		prefix = u.Protocol + "://"
	}
	raw := prefix + host
	if port := strings.TrimSpace(u.Port); port != "" && host != "" {
		raw += ":" + port
	}
	if path != "" {
		raw += "/" + path
	}
	if encoded := postmanQueryString(u.Query); encoded != "" {
		raw += "?" + encoded
	}
	if hash := strings.TrimPrefix(strings.TrimSpace(u.Hash), "#"); hash != "" {
		raw += "#" + hash
	}
	return raw
}

func reconcileRawQuery(raw string, entries []keyValueEntry) string {
	base, fragment, hasFragment := strings.Cut(raw, "#")
	query := postmanQueryString(entries)
	if query == "" {
		if before, _, ok := strings.Cut(base, "?"); ok {
			base = before
		}
	} else if before, _, ok := strings.Cut(base, "?"); ok {
		base = before + "?" + query
	} else {
		base += "?" + query
	}
	if hasFragment {
		base += "#" + fragment
	}
	return base
}

func postmanQueryString(entries []keyValueEntry) string {
	parts := make([]string, 0, len(entries))
	for _, q := range entries {
		key := strings.TrimSpace(q.Key)
		if q.Disabled || key == "" {
			continue
		}
		if q.Value == "" {
			parts = append(parts, key)
			continue
		}
		parts = append(parts, key+"="+q.Value)
	}
	return strings.Join(parts, "&")
}

func toTarget(raw string, baseURL string, vars postmanVariableLookup, diagnostics *diag.Collector, location string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	expanded := expandBaseURLVariables(raw, vars, baseURL)
	if strings.TrimSpace(baseURL) != "" {
		base := strings.TrimRight(baseURL, "/")
		if strings.HasPrefix(expanded, base+"/") {
			return rewriteVariablesWithContext(rewritePathVariablesWithContext(strings.TrimPrefix(expanded, base), vars, diagnostics, location), vars, diagnostics, location)
		}
		if expanded == base {
			return "/"
		}
	}
	if parsed, err := url.Parse(expanded); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		base, baseErr := url.Parse(strings.TrimSpace(baseURL))
		if parsed.User != nil || baseErr != nil || !postmanURLSameOrigin(parsed, base) ||
			(strings.TrimSpace(base.EscapedPath()) != "" && strings.TrimSpace(base.EscapedPath()) != "/") {
			// Absolute targets on another origin must remain absolute. Otherwise
			// the HTTP runtime would resolve their path against the collection's
			// first-derived base URL and silently send the request to the wrong
			// host. Userinfo likewise remains visible to the shared credential
			// projector instead of being discarded during relativization.
			return rewriteVariablesWithContext(rewritePathVariablesWithContext(expanded, vars, diagnostics, location), vars, diagnostics, location)
		}
		target := parsed.EscapedPath()
		if target == "" {
			target = "/"
		}
		if parsed.RawQuery != "" {
			target += "?" + parsed.RawQuery
		}
		if parsed.Fragment != "" {
			target += "#" + parsed.EscapedFragment()
		}
		return rewriteVariablesWithContext(rewritePathVariablesWithContext(target, vars, diagnostics, location), vars, diagnostics, location)
	}
	if strings.HasPrefix(raw, "{{") {
		return rewriteVariablesWithContext(raw, vars, diagnostics, location)
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return rewriteVariablesWithContext(rewritePathVariablesWithContext(raw, vars, diagnostics, location), vars, diagnostics, location)
}

func postmanURLSameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || left.Scheme == "" || right.Scheme == "" ||
		left.Host == "" || right.Host == "" {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		postmanURLPort(left) == postmanURLPort(right)
}

func postmanURLPort(value *url.URL) string {
	if value == nil {
		return ""
	}
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(strings.TrimSpace(value.Scheme)) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

const (
	postmanFilePlaceholder = "[APEXLOAD_IMPORT_FILE_PLACEHOLDER]"
)

func requestBody(b *body, authoredContentType string, assetPrefix string, diagnostics *diag.Collector, location string) (string, string, []dsl.DataSource, []string, bool) {
	if b == nil {
		return "", "", nil, nil, false
	}
	switch strings.ToLower(strings.TrimSpace(b.Mode)) {
	case "", "raw":
		return b.Raw, detectContentType(b.Raw), nil, nil, false
	case "urlencoded":
		parts := make([]string, 0, len(b.URLEncoded))
		for _, entry := range b.URLEncoded {
			if entry.Disabled || entry.Key == "" {
				continue
			}
			// Postman form fields are ordered and may repeat. Preserve both facts
			// and leave template spans visible for the subsequent shared rewrite.
			parts = append(parts,
				encodeQueryComponentPreservingTemplates(entry.Key)+"="+
					encodeQueryComponentPreservingTemplates(entry.Value),
			)
		}
		return strings.Join(parts, "&"), "application/x-www-form-urlencoded", nil, nil, false
	case "formdata":
		boundary := multipartBoundary(authoredContentType)
		if boundary == "" {
			boundary = deterministicPostmanMultipartBoundary(assetPrefix, b.FormData)
		}
		payload, assets, invalidMetadata := postmanMultipartBody(b.FormData, boundary, assetPrefix, diagnostics, location)
		return payload, "multipart/form-data; boundary=" + boundary, assets, nil, invalidMetadata
	case "graphql":
		return postmanGraphQLBody(b.GraphQL), "application/json", nil, nil, false
	default:
		diagnostics.Add(diag.Warning, "Postman body mode", location, fmt.Sprintf("body mode %q is not natively converted; add the payload manually before replay", b.Mode))
		return "", "", nil, nil, false
	}
}

// postmanGraphQLBody builds only the JSON envelope owned by the Postman
// adapter. The variables string is itself authored JSON and remains verbatim
// so the shared bounded JSON projector can detect duplicate keys, preserve
// numeric spellings and member order, and assign malformed input its safe
// blocked/rejected disposition. Decoding into interface{} here would silently
// collapse duplicates; treating malformed JSON as a string would make it
// executable with different wire semantics.
func postmanGraphQLBody(graphQL *graphqlBody) string {
	if graphQL == nil {
		return "{}"
	}
	query, _ := json.Marshal(graphQL.Query)
	var payload strings.Builder
	payload.WriteString(`{"query":`)
	payload.Write(query)
	if strings.TrimSpace(graphQL.Variables) != "" {
		payload.WriteString(`,"variables":`)
		payload.WriteString(graphQL.Variables)
	}
	payload.WriteByte('}')
	return payload.String()
}

func deterministicPostmanMultipartBoundary(candidateIdentity string, entries []keyValueEntry) string {
	for attempt := 0; ; attempt++ {
		boundary := postmanMultipartBoundaryCandidate(candidateIdentity, attempt)
		if !postmanMultipartBoundaryOccursInParts(boundary, entries) {
			return boundary
		}
	}
}

func postmanMultipartBoundaryCandidate(candidateIdentity string, attempt int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("postman-multipart-v1\x00%s\x00%d", candidateIdentity, attempt)))
	return fmt.Sprintf("----apexload-postman-%x", digest[:12])
}

func postmanMultipartBoundaryOccursInParts(boundary string, entries []keyValueEntry) bool {
	for _, entry := range entries {
		if strings.Contains(entry.Value, boundary) ||
			strings.Contains(postmanFormFileSource(entry), boundary) ||
			strings.Contains(entry.ContentType, boundary) {
			return true
		}
	}
	return false
}

func multipartBoundary(contentType string) string {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return ""
	}
	return strings.TrimSpace(params["boundary"])
}

func postmanMultipartBody(entries []keyValueEntry, boundary string, assetPrefix string, diagnostics *diag.Collector, location string) (string, []dsl.DataSource, bool) {
	var body strings.Builder
	assets := make([]dsl.DataSource, 0)
	assetNames := map[string]int{}
	invalidMetadata := false
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Key)
		if entry.Disabled || name == "" {
			continue
		}
		name, nameChanged := credentialpolicy.SanitizeMultipartDispositionToken(name)
		name = credentialpolicy.EscapeMultipartDispositionToken(name)
		invalidMetadata = invalidMetadata || nameChanged
		body.WriteString("--" + boundary + "\r\n")
		if strings.EqualFold(strings.TrimSpace(entry.Type), "file") {
			filename := postmanFormFileName(entry)
			filename, filenameChanged := credentialpolicy.SanitizeMultipartDispositionToken(filename)
			filename = credentialpolicy.EscapeMultipartDispositionToken(filename)
			invalidMetadata = invalidMetadata || filenameChanged
			assetName := postmanFileAssetName(assetPrefix, name, assetNames)
			contentType := strings.TrimSpace(entry.ContentType)
			if contentType == "" {
				contentType = "application/octet-stream"
			} else if !httpguts.ValidHeaderFieldValue(contentType) {
				contentType = "application/octet-stream"
				invalidMetadata = true
			}
			body.WriteString(`Content-Disposition: form-data; name="` + name + `"; filename="` + filename + `"` + "\r\n")
			body.WriteString("Content-Type: " + contentType + "\r\n\r\n")
			body.WriteString("{{file." + assetName + "}}\r\n")
			assets = append(assets, dsl.DataSource{
				Name: assetName,
				Type: "file",
				// Source workstation paths are not portable or controller-
				// governed. The action is durably gated until this inert
				// placeholder is replaced by an uploaded file binding.
				InlineData:  postmanFilePlaceholder,
				FileName:    filename,
				ContentType: contentType,
			})
			if diagnostics != nil {
				diagnostics.Add(diag.Warning, "Postman form-data file", location, "file part "+name+" references source file "+filename+"; bind the generated file data source before replay")
			}
			continue
		}
		body.WriteString(`Content-Disposition: form-data; name="` + name + `"` + "\r\n")
		if contentType := strings.TrimSpace(entry.ContentType); contentType != "" {
			if httpguts.ValidHeaderFieldValue(contentType) {
				body.WriteString("Content-Type: " + contentType + "\r\n")
			} else {
				invalidMetadata = true
			}
		}
		body.WriteString("\r\n")
		body.WriteString(tmpl.TranslateDynamic(entry.Value, diagnostics, location))
		body.WriteString("\r\n")
	}
	body.WriteString("--" + boundary + "--\r\n")
	if invalidMetadata && diagnostics != nil {
		diagnostics.Add(diag.Warning, "Postman multipart metadata", location, "invalid multipart field metadata was replaced in a blocked draft")
	}
	return body.String(), assets, invalidMetadata
}

func postmanFormFileName(entry keyValueEntry) string {
	source := postmanFormFileSource(entry)
	source = strings.ReplaceAll(strings.TrimSpace(source), "\\", "/")
	if name := path.Base(source); name != "" && name != "." && name != "/" {
		return name
	}
	name := normalizeVariableName(entry.Key)
	if name == "" {
		name = "upload"
	}
	return name + ".bin"
}

func postmanFormFileSource(entry keyValueEntry) string {
	var source string
	switch value := entry.Src.(type) {
	case string:
		source = value
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				source = text
				break
			}
		}
	}
	if strings.TrimSpace(source) == "" {
		source = entry.Value
	}
	return strings.TrimSpace(source)
}

func postmanFileAssetName(prefix string, field string, seen map[string]int) string {
	base := strings.ToLower(strings.Trim(normalizeVariableName(prefix+"_"+field), "_"))
	if base == "" {
		base = "postman_file"
	}
	seen[base]++
	if seen[base] > 1 {
		return fmt.Sprintf("%s_%d", base, seen[base])
	}
	return base
}

func mergePostmanDataSources(existing []dsl.DataSource, additions []dsl.DataSource) []dsl.DataSource {
	if len(additions) == 0 {
		return existing
	}
	out := append([]dsl.DataSource(nil), existing...)
	seen := make(map[string]struct{}, len(existing)+len(additions))
	for _, source := range existing {
		seen[strings.ToLower(strings.TrimSpace(source.Name))] = struct{}{}
	}
	for _, source := range additions {
		base := source.Name
		if base == "" {
			base = "postman_file"
		}
		name := base
		for suffix := 2; ; suffix++ {
			if _, duplicate := seen[strings.ToLower(name)]; !duplicate {
				break
			}
			name = fmt.Sprintf("%s_%d", base, suffix)
		}
		source.Name = name
		seen[strings.ToLower(name)] = struct{}{}
		out = append(out, source)
	}
	return out
}

func unsupportedBodyMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "raw", "urlencoded", "formdata", "graphql":
		return false
	default:
		return true
	}
}

func sortedAuthSchemes(requests []requestItem, collectionAuth *auth) []string {
	seen := map[string]struct{}{}
	if collectionAuth != nil && collectionAuth.Type != "" {
		seen[collectionAuth.Type] = struct{}{}
	}
	for _, item := range requests {
		if item.InheritedAuth != nil && item.InheritedAuth.Type != "" {
			seen[item.InheritedAuth.Type] = struct{}{}
		}
		if item.Request.Auth != nil && item.Request.Auth.Type != "" {
			seen[item.Request.Auth.Type] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func reportUnsupportedPostmanAuth(value *auth, diagnostics *diag.Collector, location string) {
	if value == nil || diagnostics == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(value.Type)) {
	case "ntlm":
		diagnostics.Add(diag.Warning, "Postman auth", location, value.Type+" auth is not replayable by the HTTP runtime; its source fields were retained behind an execution gate for operator translation")
	case "awsv4", "hawk":
		diagnostics.Add(diag.Warning, "Postman auth", location, value.Type+" request signing cannot be executed during import; its source fields were retained behind an execution gate for operator translation")
	}
}

func applyPostmanAuth(action *dsl.Action, requestAuth *auth, collectionAuth *auth, vars postmanVariableLookup, diagnostics *diag.Collector, location string, authScope string, credentialNames *credentialpolicy.NameRegistry) {
	explicitRequestAuth := requestAuth != nil && strings.TrimSpace(requestAuth.Type) != ""
	if explicitRequestAuth && strings.EqualFold(strings.TrimSpace(requestAuth.Type), "noauth") {
		action.Auth = &dsl.AuthConfig{Type: "noauth"}
		return
	}

	effective := requestAuth
	if effective == nil || strings.TrimSpace(effective.Type) == "" || strings.EqualFold(strings.TrimSpace(effective.Type), "inherit") {
		effective = collectionAuth
	}
	if effective == nil {
		return
	}

	switch strings.ToLower(strings.TrimSpace(effective.Type)) {
	case "", "inherit", "noauth":
		return
	case "apikey":
		applyPostmanAPIKey(action, effective, vars, diagnostics, location)
	case "bearer", "basic", "digest", "oauth2":
		cfg := postmanAuthConfig(effective, vars, diagnostics, location, authScope, credentialNames)
		if strings.TrimSpace(cfg.Type) != "" {
			action.Auth = &cfg
		}
	case "ntlm", "awsv4", "hawk":
		reportUnsupportedPostmanAuth(effective, diagnostics, location)
		retainUnsupportedPostmanAuth(action, effective, vars, diagnostics, location)
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "auth type "+effective.Type+" requires review; its source fields were retained behind an execution gate")
		}
		retainUnsupportedPostmanAuth(action, effective, vars, diagnostics, location)
	}
}

func retainUnsupportedPostmanAuth(
	action *dsl.Action,
	value *auth,
	vars postmanVariableLookup,
	diagnostics *diag.Collector,
	location string,
) {
	if action == nil || value == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	sourceType := strings.ToLower(strings.TrimSpace(value.Type))
	action.Metadata["postman_auth_review_only"] = sourceType

	entries := value.entriesForType()
	sourceFields := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		field := map[string]any{
			"type":     "source_credential",
			"name":     entry.Key,
			"value":    rewriteVariablesWithContext(entry.Value, vars, diagnostics, location),
			"disabled": entry.Disabled,
		}
		sourceFields = append(sourceFields, field)
	}
	if len(sourceFields) > 0 {
		// These are durable, non-executable source fields. Each entry carries an
		// explicit credential semantic marker so ordinary script projections mask
		// its value even when Postman used an unfamiliar field name.
		action.Metadata["postman_auth_source_fields"] = sourceFields
	}

	config := dsl.AuthConfig{Type: sourceType}
	switch sourceType {
	case "ntlm":
		config.Username = postmanAuthEntryValue(entries, vars, diagnostics, location, "username", "user")
		config.Password = postmanAuthEntryValue(entries, vars, diagnostics, location, "password", "pass")
		config.Domain = postmanAuthEntryValue(entries, vars, diagnostics, location, "domain")
	case "hawk":
		config.Username = postmanAuthEntryValue(entries, vars, diagnostics, location, "authId", "id")
		config.Password = postmanAuthEntryValue(entries, vars, diagnostics, location, "authKey", "key")
	}
	action.Auth = &config
}

func postmanAuthEntryValue(
	entries []keyValueEntry,
	vars postmanVariableLookup,
	diagnostics *diag.Collector,
	location string,
	keys ...string,
) string {
	for _, entry := range entries {
		if entry.Disabled {
			continue
		}
		for _, key := range keys {
			if strings.EqualFold(strings.TrimSpace(entry.Key), key) {
				return rewriteVariablesWithContext(entry.Value, vars, diagnostics, location)
			}
		}
	}
	return ""
}

func applyPostmanAPIKey(action *dsl.Action, value *auth, vars postmanVariableLookup, diagnostics *diag.Collector, location string) {
	markReview := func(secret string) {
		if action == nil {
			return
		}
		if action.Metadata == nil {
			action.Metadata = make(map[string]any)
		}
		action.Metadata["postman_auth_review_only"] = "apikey"
		// Carry the exact source value through the typed credential surface. The
		// shared projector keeps it on the durable blocked draft so the importing
		// operator can correct the placement without re-entering the API key.
		action.Auth = &dsl.AuthConfig{
			Type:  "postman_apikey",
			Token: secret,
		}
	}

	key := authValue(value.APIKey, "key", vars, diagnostics, location)
	secret := authCredentialValue(value.APIKey, "value", vars, diagnostics, location)
	placement := strings.ToLower(strings.TrimSpace(authValue(value.APIKey, "in", vars, diagnostics, location)))
	if strings.TrimSpace(key) == "" || strings.TrimSpace(secret) == "" || placement == "" {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "API key auth is missing key, value, or placement and was not converted")
		}
		markReview(secret)
		return
	}
	switch placement {
	case "query", "queryparam", "queryparams":
		action.Target = appendQueryParam(action.Target, key, secret)
	case "header":
		if action.Headers == nil {
			action.Headers = map[string]string{}
		}
		action.HeaderList = replaceHeaderListValue(action.HeaderList, key, secret)
		setHeaderSummary(action.Headers, key, secret)
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "API key placement "+placement+" is not supported and was not converted")
		}
		markReview(secret)
	}
}

func appendQueryParam(target string, key string, value string) string {
	// Split off any existing #fragment so the query parameter is appended before
	// it (appending after would corrupt the URL), then re-attach.
	base, fragment, hasFragment := strings.Cut(target, "#")
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	result := base + sep + encodeQueryComponentPreservingTemplates(key) + "=" + encodeQueryComponentPreservingTemplates(value)
	if hasFragment {
		result += "#" + fragment
	}
	return result
}

// encodeQueryComponentPreservingTemplates QueryEscapes the literal portions of s
// while leaving {{...}} template spans verbatim, so a non-template literal (e.g.
// an unsanitized API-key value with reserved characters) cannot produce a
// malformed query string, yet placeholders still resolve at replay.
func encodeQueryComponentPreservingTemplates(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		open := strings.Index(s, "{{")
		if open < 0 {
			b.WriteString(url.QueryEscape(s))
			break
		}
		b.WriteString(url.QueryEscape(s[:open]))
		rel := strings.Index(s[open:], "}}")
		if rel < 0 {
			b.WriteString(url.QueryEscape(s[open:]))
			break
		}
		end := open + rel + 2
		b.WriteString(s[open:end]) // {{...}} kept verbatim
		s = s[end:]
	}
	return b.String()
}

func postmanAuthConfig(value *auth, vars postmanVariableLookup, diagnostics *diag.Collector, location string, authScope string, credentialNames *credentialpolicy.NameRegistry) dsl.AuthConfig {
	if value == nil {
		return dsl.AuthConfig{}
	}
	switch strings.ToLower(strings.TrimSpace(value.Type)) {
	case "", "noauth", "inherit":
		return dsl.AuthConfig{}
	case "bearer":
		token, ok := authCredentialValuePresent(value.Bearer, "token", vars, diagnostics, location)
		if !ok || token == "" {
			token = missingPostmanAuthReference(credentialNames, authScope, location, "bearer", "token", diagnostics)
		}
		return dsl.AuthConfig{Type: "bearer", Token: token}
	case "basic":
		username, usernamePresent := authValuePresent(value.Basic, "username", vars, diagnostics, location)
		if !usernamePresent || username == "" {
			username = missingPostmanAuthReference(credentialNames, authScope, location, "basic", "username", diagnostics)
		}
		password, passwordPresent := authCredentialValuePresent(value.Basic, "password", vars, diagnostics, location)
		if !passwordPresent || password == "" {
			password = missingPostmanAuthReference(credentialNames, authScope, location, "basic", "password", diagnostics)
		}
		return dsl.AuthConfig{
			Type:     "basic",
			Username: username,
			Password: password,
		}
	case "digest":
		username, usernamePresent := authValuePresent(value.Digest, "username", vars, diagnostics, location)
		if !usernamePresent || username == "" {
			username = missingPostmanAuthReference(credentialNames, authScope, location, "digest", "username", diagnostics)
		}
		password, passwordPresent := authCredentialValuePresent(value.Digest, "password", vars, diagnostics, location)
		if !passwordPresent || password == "" {
			password = missingPostmanAuthReference(credentialNames, authScope, location, "digest", "password", diagnostics)
		}
		return dsl.AuthConfig{
			Type:     "digest",
			Username: username,
			Password: password,
		}
	case "oauth2":
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "OAuth2 auth was represented as a bearer token placeholder; review token acquisition before replay")
		}
		token, ok := authCredentialValuePresent(value.OAuth2, "accessToken", vars, diagnostics, location)
		if !ok || token == "" {
			token, ok = authCredentialValuePresent(value.OAuth2, "access_token", vars, diagnostics, location)
		}
		if !ok || token == "" {
			token = missingPostmanAuthReference(credentialNames, authScope, location, "oauth2", "access_token", diagnostics)
		}
		return dsl.AuthConfig{Type: "bearer", Token: token}
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "auth type "+value.Type+" was not converted")
		}
		return dsl.AuthConfig{}
	}
}

func missingPostmanAuthReference(
	registry *credentialpolicy.NameRegistry,
	authScope string,
	location string,
	authType string,
	field string,
	diagnostics *diag.Collector,
) string {
	if registry == nil {
		registry = credentialpolicy.NewNameRegistry()
	}
	hint := strings.Join([]string{
		"POSTMAN",
		sanitizeName(location),
		strings.ToUpper(strings.TrimSpace(authType)),
		strings.ToUpper(strings.TrimSpace(field)),
	}, "_")
	name := registry.Allocate(
		hint,
		"postman-missing-auth\x00"+authScope+"\x00"+authType+"\x00"+field,
	)
	reference, err := credential.Reference(name)
	if err != nil {
		// NameRegistry only returns valid logical names. Keep a fail-closed
		// fallback in case that contract changes rather than reviving an
		// unresolved generic environment placeholder.
		reference = "{{secret.POSTMAN_MISSING_AUTH}}"
	}
	if diagnostics != nil {
		diagnostics.Add(
			diag.Warning,
			"Postman auth",
			location,
			fmt.Sprintf("%s auth has no %s value; configure secret %s before replay", authType, field, name),
		)
	}
	return reference
}

func authValue(entries []keyValueEntry, key string, vars postmanVariableLookup, diagnostics *diag.Collector, location string) string {
	value, _ := authValuePresent(entries, key, vars, diagnostics, location)
	return value
}

func authValuePresent(entries []keyValueEntry, key string, vars postmanVariableLookup, diagnostics *diag.Collector, location string) (string, bool) {
	for _, entry := range entries {
		if entry.Disabled {
			continue
		}
		if strings.EqualFold(entry.Key, key) {
			return rewriteVariablesWithContext(entry.Value, vars, diagnostics, location), true
		}
	}
	return "", false
}

func authCredentialValue(entries []keyValueEntry, key string, vars postmanVariableLookup, diagnostics *diag.Collector, location string) string {
	value, _ := authCredentialValuePresent(entries, key, vars, diagnostics, location)
	return value
}

func authCredentialValuePresent(entries []keyValueEntry, key string, vars postmanVariableLookup, diagnostics *diag.Collector, location string) (string, bool) {
	// Retain literal credentials through the shared EffectiveRequest projector.
	// API response projection masks ordinary reads and reviews, while the
	// authorized import draft and durable destination remain executable without
	// a second credential-entry step.
	return authValuePresent(entries, key, vars, diagnostics, location)
}

func parameterNames(params []dsl.Parameter) []string {
	out := make([]string, 0, len(params))
	for _, param := range params {
		out = append(out, param.Name)
	}
	return out
}

func expandVariables(value string, vars postmanVariableLookup) string {
	return variablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := variablePattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		if replacement, _, ok := vars.Get(matches[1]); ok && replacement != "" {
			return replacement
		}
		return token
	})
}

func expandBaseURLVariables(value string, vars postmanVariableLookup, baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return variablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := variablePattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		name := matches[1]
		replacement, _, ok := vars.Get(name)
		replacement = strings.TrimSpace(replacement)
		if !ok || replacement == "" {
			return token
		}
		if looksAbsoluteURL(replacement) && (base == "" || strings.TrimRight(replacement, "/") == base) {
			return strings.TrimRight(replacement, "/")
		}
		if base != "" && isBaseURLVariableName(name) {
			return base
		}
		return token
	})
}

func rewriteVariables(value string) string {
	return rewriteVariablesWithDiagnostics(value, nil, "")
}

func rewriteVariablesWithDiagnostics(value string, diagnostics *diag.Collector, location string) string {
	return rewriteVariablesWithContext(value, postmanVariableLookup{}, diagnostics, location)
}

func rewriteVariablesWithContext(value string, vars postmanVariableLookup, diagnostics *diag.Collector, location string) string {
	value = tmpl.TranslateDynamic(value, diagnostics, location)
	return variablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := variablePattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		name := strings.TrimSpace(matches[1])
		if isApexExpressionName(name) {
			return "{{" + name + "}}"
		}
		if replacement, ok := inlineRuntimeVariable(name, vars, diagnostics, location); ok {
			return replacement
		}
		if vars.ShouldInline(name) {
			if replacement, _, ok := vars.Get(name); ok {
				return tmpl.TranslateDynamic(replacement, diagnostics, location)
			}
		}
		if _, bindingName, ok := vars.Get(name); ok && bindingName != "" {
			return "{{var." + bindingName + "}}"
		}
		return "{{var." + normalizeVariableName(name) + "}}"
	})
}

func inlineRuntimeVariable(name string, vars postmanVariableLookup, diagnostics *diag.Collector, location string) (string, bool) {
	value, _, ok := vars.Get(name)
	if !ok {
		return "", false
	}
	translated := strings.TrimSpace(tmpl.TranslateDynamic(value, diagnostics, location))
	if !runtimeTemplateValue(translated) {
		return "", false
	}
	return translated, true
}

var postmanRuntimeTemplatePattern = regexp.MustCompile(`\{\{([^{}\r\n]+)\}\}`)

func runtimeTemplateValue(value string) bool {
	matches := postmanRuntimeTemplatePattern.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return false
	}
	for _, match := range matches {
		if len(match) != 2 || !isApexExpressionName(strings.TrimSpace(match[1])) {
			return false
		}
	}
	remainder := postmanRuntimeTemplatePattern.ReplaceAllString(value, "")
	return !strings.Contains(remainder, "{{") && !strings.Contains(remainder, "}}")
}

var pathVariablePattern = regexp.MustCompile(`(^|/):([A-Za-z][A-Za-z0-9_-]*)`)

func rewritePathVariables(value string) string {
	return rewritePathVariablesWithContext(value, postmanVariableLookup{}, nil, "")
}

func rewritePathVariablesWithContext(value string, vars postmanVariableLookup, diagnostics *diag.Collector, location string) string {
	return pathVariablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := pathVariablePattern.FindStringSubmatch(token)
		if len(matches) != 3 {
			return token
		}
		name := matches[2]
		if vars.ShouldInline(name) {
			if replacement, _, ok := vars.Get(name); ok {
				return matches[1] + tmpl.TranslateDynamic(replacement, diagnostics, location)
			}
		}
		return matches[1] + "{{var." + normalizeVariableName(name) + "}}"
	})
}

func pathVariableNames(value string) []string {
	matches := pathVariablePattern.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 3 {
			names = append(names, match[2])
		}
	}
	return names
}

func normalizeVariableName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "{}")
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, ".", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func headerValue(headers map[string]string, key string) string {
	for existingKey, value := range headers {
		if strings.EqualFold(existingKey, key) {
			return value
		}
	}
	return ""
}

// containsTemplateToken reports whether a value references a template variable
// (e.g. "Bearer {{token}}"). Such values are not hard-coded secrets, so
// sanitization must preserve them rather than masking the whole value.
func containsTemplateToken(value string) bool {
	return strings.Contains(value, "{{") && strings.Contains(value, "}}")
}

func detectContentType(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return "application/json"
	}
	if strings.HasPrefix(trimmed, "<") {
		return "application/xml"
	}
	return ""
}

func actionName(index int, item requestItem) string {
	name := convertutil.FirstNonEmpty(item.Name, fmt.Sprintf("Request %d", index+1))
	if item.Folder != "" {
		name = item.Folder + " " + name
	}
	return sanitizeName(name)
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Postman_Request"
	}
	replacer := strings.NewReplacer("/", " ", "\\", " ", ":", " ", "\t", " ", "\n", " ")
	value = replacer.Replace(value)
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "Postman_Request"
	}
	return strings.Join(fields, "_")
}

func looksAbsoluteURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}

func isBaseURLVariableName(value string) bool {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "")) {
	case "baseurl", "host", "url":
		return true
	default:
		return false
	}
}

func isApexExpressionName(value string) bool {
	for _, prefix := range []string{"var.", "env.", "data.", "param.", "secret.", "file.", "fn.", "fake."} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
