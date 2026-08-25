package cloudtest

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jasonczech/apexload/pkg/convert/convertresult"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

const (
	maxArchiveEntries = 2_000
	maxEntryBytes     = int64(16 << 20)
	maxTotalBytes     = int64(64 << 20)
	maxXMLDepth       = 64
	maxXMLElements    = 100_000
	maxXMLAttributes  = 128
	maxReviewActions  = 2_000
	maxDraftAssets    = 256
)

type ConvertOptions struct {
	ProjectID string `json:"project_id,omitempty"`
	FileName  string `json:"file_name,omitempty"`
}

type ElementDisposition struct {
	SourceFile        string      `json:"source_file"`
	Path              string      `json:"path"`
	Element           string      `json:"element"`
	CanonicalType     string      `json:"canonical_type,omitempty"`
	Recognized        bool        `json:"recognized"`
	FixtureProven     bool        `json:"fixture_proven"`
	Disposition       Disposition `json:"disposition"`
	Reason            string      `json:"reason"`
	OwningClip        string      `json:"owning_clip,omitempty"`
	OwningComposition string      `json:"owning_composition,omitempty"`
}

type ScenarioDraft struct {
	Name           string               `json:"name"`
	Duration       string               `json:"duration,omitempty"`
	Groups         []ScenarioGroupDraft `json:"groups"`
	ReviewRequired bool                 `json:"review_required"`
	SourceFile     string               `json:"source_file"`
	SourcePath     string               `json:"source_path"`
}

type ScenarioGroupDraft struct {
	Name           string `json:"name"`
	ScriptName     string `json:"script_name,omitempty"`
	StartOffset    string `json:"start_offset,omitempty"`
	VUsers         int    `json:"vusers,omitempty"`
	RampUp         string `json:"ramp_up,omitempty"`
	Pacing         string `json:"pacing,omitempty"`
	LoadZone       string `json:"load_zone,omitempty"`
	ReviewRequired bool   `json:"review_required"`
	SourcePath     string `json:"source_path"`
}

type AssetDraft struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	SourceFile     string `json:"source_file"`
	SourcePath     string `json:"source_path"`
	ReviewRequired bool   `json:"review_required"`
}

type ConvertStats struct {
	XMLFiles           int `json:"xml_files"`
	Elements           int `json:"elements"`
	RecognizedElements int `json:"recognized_elements"`
	UnknownElements    int `json:"unknown_elements"`
	Clips              int `json:"clips"`
	Compositions       int `json:"compositions"`
	ReviewActions      int `json:"review_actions"`
	TruncatedActions   int `json:"truncated_actions"`
}

type ConvertResult struct {
	Scripts            []*dsl.Script            `json:"scripts"`
	Script             *dsl.Script              `json:"script,omitempty"`
	ScenarioDrafts     []ScenarioDraft          `json:"scenario_drafts"`
	DataPoolDrafts     []AssetDraft             `json:"data_pool_drafts"`
	EnvironmentDrafts  []AssetDraft             `json:"environment_drafts"`
	MonitorDrafts      []AssetDraft             `json:"monitor_drafts"`
	PreservedArtifacts []AssetDraft             `json:"preserved_artifacts"`
	Dispositions       []ElementDisposition     `json:"dispositions"`
	Warnings           []string                 `json:"warnings"`
	Unsupported        diag.Diagnostics         `json:"unsupported"`
	Stats              ConvertStats             `json:"stats"`
	SourceFormat       string                   `json:"source_format"`
	FixtureGate        string                   `json:"fixture_gate"`
	Accounting         convertresult.Accounting `json:"accounting"`
}

type documentState struct {
	file         string
	dispositions []ElementDisposition
	clips        []*clipDraft
	scenarios    []ScenarioDraft
	dataPools    []AssetDraft
	environments []AssetDraft
	monitors     []AssetDraft
	preserved    []AssetDraft
	warnings     []string
	unsupported  diag.Diagnostics
}

type clipDraft struct {
	name     string
	file     string
	path     string
	elements []ElementDisposition
}

type parseFrame struct {
	name             string
	path             string
	children         map[string]int
	clipIndex        int
	compositionIndex int
	groupIndex       int
}

// Convert accepts a single XML export or a bounded dependency-closure ZIP.
func Convert(data []byte, opts ConvertOptions) (*ConvertResult, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("CloudTest XML or ZIP content is required")
	}
	files, sourceFormat, err := sourceFiles(data, opts.FileName)
	if err != nil {
		return nil, err
	}
	result := &ConvertResult{
		Scripts:            []*dsl.Script{},
		ScenarioDrafts:     []ScenarioDraft{},
		DataPoolDrafts:     []AssetDraft{},
		EnvironmentDrafts:  []AssetDraft{},
		MonitorDrafts:      []AssetDraft{},
		PreservedArtifacts: []AssetDraft{},
		Dispositions:       []ElementDisposition{},
		Warnings:           []string{},
		SourceFormat:       sourceFormat,
		FixtureGate:        "blocked_until_sanitized_dependency_complete_exports_are_fixture_proven",
	}

	for _, file := range files {
		state, parseErr := inspectXML(file.Name, file.Data)
		if parseErr != nil {
			return nil, fmt.Errorf("inspect CloudTest source %q: %w", file.Name, parseErr)
		}
		if err := enforceBundleReviewBounds(result, state); err != nil {
			return nil, err
		}
		result.Stats.XMLFiles++
		result.Dispositions = append(result.Dispositions, state.dispositions...)
		result.ScenarioDrafts = append(result.ScenarioDrafts, state.scenarios...)
		result.DataPoolDrafts = append(result.DataPoolDrafts, state.dataPools...)
		result.EnvironmentDrafts = append(result.EnvironmentDrafts, state.environments...)
		result.MonitorDrafts = append(result.MonitorDrafts, state.monitors...)
		result.PreservedArtifacts = append(result.PreservedArtifacts, state.preserved...)
		result.Warnings = append(result.Warnings, state.warnings...)
		result.Unsupported = append(result.Unsupported, state.unsupported...)
		for _, clip := range state.clips {
			if len(result.Scripts) >= maxDraftAssets {
				return nil, fmt.Errorf("CloudTest export contains more than %d Clip assets", maxDraftAssets)
			}
			script, truncated := buildReviewScript(clip, opts.ProjectID)
			result.Stats.TruncatedActions += truncated
			result.Stats.ReviewActions += len(script.Actions)
			result.Scripts = append(result.Scripts, script)
		}
	}

	for _, disposition := range result.Dispositions {
		result.Stats.Elements++
		if disposition.Recognized {
			result.Stats.RecognizedElements++
		} else {
			result.Stats.UnknownElements++
		}
	}
	result.Stats.Clips = len(result.Scripts)
	result.Stats.Compositions = len(result.ScenarioDrafts)
	if len(result.Scripts) > 0 {
		result.Script = result.Scripts[0]
	}
	result.Accounting = convertresult.Accounting{
		Detected:   result.Stats.Elements,
		ReviewOnly: result.Stats.Elements,
	}
	result.Warnings = append(result.Warnings,
		"CloudTest export mappings are provisional; generated assets are blocked from execution until fixture evidence and operator review resolve every source disposition.",
	)
	if result.Stats.TruncatedActions > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d element-level review placeholders exceeded the per-Clip action cap; the complete disposition ledger remains in the import result.",
			result.Stats.TruncatedActions,
		))
	}
	result.Warnings = uniqueSorted(result.Warnings)
	sort.SliceStable(result.Dispositions, func(i, j int) bool {
		if result.Dispositions[i].SourceFile != result.Dispositions[j].SourceFile {
			return result.Dispositions[i].SourceFile < result.Dispositions[j].SourceFile
		}
		return result.Dispositions[i].Path < result.Dispositions[j].Path
	})
	return result, nil
}

func enforceBundleReviewBounds(result *ConvertResult, state *documentState) error {
	if result == nil || state == nil {
		return nil
	}
	checks := []struct {
		name     string
		current  int
		incoming int
		limit    int
	}{
		{name: "XML elements", current: len(result.Dispositions), incoming: len(state.dispositions), limit: maxXMLElements},
		{name: "Clip assets", current: len(result.Scripts), incoming: len(state.clips), limit: maxDraftAssets},
		{name: "Composition assets", current: len(result.ScenarioDrafts), incoming: len(state.scenarios), limit: maxDraftAssets},
		{name: "data-pool assets", current: len(result.DataPoolDrafts), incoming: len(state.dataPools), limit: maxDraftAssets},
		{name: "environment assets", current: len(result.EnvironmentDrafts), incoming: len(state.environments), limit: maxDraftAssets},
		{name: "monitor assets", current: len(result.MonitorDrafts), incoming: len(state.monitors), limit: maxDraftAssets},
		{name: "preserved assets", current: len(result.PreservedArtifacts), incoming: len(state.preserved), limit: maxDraftAssets},
	}
	for _, check := range checks {
		if check.incoming > check.limit-check.current {
			return fmt.Errorf("CloudTest export contains more than %d %s across the dependency closure", check.limit, check.name)
		}
	}
	return nil
}

func sourceFiles(data []byte, fileName string) ([]archiveingest.File, string, error) {
	isZIP := bytes.HasPrefix(data, []byte("PK\x03\x04")) || strings.EqualFold(filepath.Ext(fileName), ".zip")
	if !isZIP {
		name := strings.TrimSpace(fileName)
		if name == "" {
			name = "cloudtest-export.xml"
		}
		validated, err := archiveingest.ValidateFilePath(filepath.Base(name), 1)
		if err != nil {
			return nil, "", err
		}
		if !strings.EqualFold(filepath.Ext(validated), ".xml") {
			return nil, "", errors.New("CloudTest source must be XML or ZIP")
		}
		return []archiveingest.File{{Name: validated, Data: data}}, "cloudtest_xml", nil
	}
	extracted, err := archiveingest.ReadZIP(data, archiveingest.Options{Limits: archiveingest.Limits{
		MaxEntries:    maxArchiveEntries,
		MaxEntryBytes: maxEntryBytes,
		MaxTotalBytes: maxTotalBytes,
		MaxDepth:      32,
	}})
	if err != nil {
		return nil, "", err
	}
	files := make([]archiveingest.File, 0, len(extracted))
	for _, file := range extracted {
		if strings.EqualFold(archiveingest.LogicalExt(file.Name), ".xml") {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return nil, "", errors.New("CloudTest ZIP contains no XML files")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, "cloudtest_zip", nil
}

func inspectXML(fileName string, data []byte) (*documentState, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	state := &documentState{file: fileName}
	frames := []parseFrame{{clipIndex: -1, compositionIndex: -1, groupIndex: -1, children: map[string]int{}}}
	elements := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch typed := token.(type) {
		case xml.Directive:
			if strings.Contains(strings.ToUpper(string(typed)), "DOCTYPE") {
				return nil, errors.New("DOCTYPE declarations are not accepted")
			}
		case xml.StartElement:
			elements++
			if elements > maxXMLElements {
				return nil, fmt.Errorf("XML contains more than %d elements", maxXMLElements)
			}
			if len(frames) > maxXMLDepth {
				return nil, fmt.Errorf("XML nesting exceeds %d levels", maxXMLDepth)
			}
			if len(typed.Attr) > maxXMLAttributes {
				return nil, fmt.Errorf("element %q contains more than %d attributes", typed.Name.Local, maxXMLAttributes)
			}
			parent := &frames[len(frames)-1]
			local := strings.TrimSpace(typed.Name.Local)
			canonical := canonicalToken(local)
			parent.children[canonical]++
			path := parent.path + "/" + canonical + "[" + strconv.Itoa(parent.children[canonical]) + "]"
			frame := parseFrame{
				name: canonical, path: path, children: map[string]int{},
				clipIndex: parent.clipIndex, compositionIndex: parent.compositionIndex, groupIndex: parent.groupIndex,
			}
			attrs := safeAttributes(typed.Attr)
			label := firstNonEmpty(attrs["name"], attrs["label"], attrs["id"], local)
			if canonical == "clip" {
				if len(state.clips) >= maxDraftAssets {
					return nil, fmt.Errorf("XML file contains more than %d Clip assets", maxDraftAssets)
				}
				state.clips = append(state.clips, &clipDraft{name: label, file: fileName, path: path})
				frame.clipIndex = len(state.clips) - 1
			}
			if canonical == "composition" {
				if len(state.scenarios) >= maxDraftAssets {
					return nil, fmt.Errorf("XML file contains more than %d Composition assets", maxDraftAssets)
				}
				state.scenarios = append(state.scenarios, ScenarioDraft{
					Name: label, Groups: []ScenarioGroupDraft{}, ReviewRequired: true, SourceFile: fileName, SourcePath: path,
					Duration: durationValue(attrs, "duration", "testduration", "endtime"),
				})
				frame.compositionIndex = len(state.scenarios) - 1
				frame.groupIndex = -1
			}
			if (canonical == "track" || canonical == "distribution") && frame.compositionIndex >= 0 {
				scenario := &state.scenarios[frame.compositionIndex]
				group := ScenarioGroupDraft{
					Name: label, ScriptName: firstNonEmpty(attrs["clip"], attrs["clipref"], attrs["script"], attrs["content"]),
					StartOffset: durationValue(attrs, "startoffset", "offset", "starttime"),
					RampUp:      durationValue(attrs, "rampup", "ramp", "rampduration"),
					Pacing:      firstNonEmpty(attrs["pacing"], attrs["pace"]), LoadZone: firstNonEmpty(attrs["loadzone"], attrs["location"], attrs["region"]),
					VUsers: positiveInt(attrs, "vusers", "users", "virtualusers", "concurrency"), ReviewRequired: true, SourcePath: path,
				}
				scenario.Groups = append(scenario.Groups, group)
				frame.groupIndex = len(scenario.Groups) - 1
			}
			disposition := classifyElement(fileName, path, local, canonical, label, frame, state)
			state.dispositions = append(state.dispositions, disposition)
			if frame.clipIndex >= 0 && frame.clipIndex < len(state.clips) {
				state.clips[frame.clipIndex].elements = append(state.clips[frame.clipIndex].elements, disposition)
			}
			collectAssetDraft(state, disposition, label)
			frames = append(frames, frame)
		case xml.EndElement:
			if len(frames) <= 1 {
				return nil, errors.New("XML contains an unexpected closing element")
			}
			frames = frames[:len(frames)-1]
		}
	}
	if len(frames) != 1 {
		return nil, errors.New("XML contains unclosed elements")
	}
	if elements == 0 {
		return nil, errors.New("XML document is empty")
	}
	return state, nil
}

func classifyElement(fileName, path, local, canonical, label string, frame parseFrame, state *documentState) ElementDisposition {
	recognizedType, recognized := recognizedElements[canonical]
	clipName := ""
	if frame.clipIndex >= 0 && frame.clipIndex < len(state.clips) {
		clipName = state.clips[frame.clipIndex].name
	}
	compositionName := ""
	if frame.compositionIndex >= 0 && frame.compositionIndex < len(state.scenarios) {
		compositionName = state.scenarios[frame.compositionIndex].Name
	}
	disposition := ElementDisposition{
		SourceFile: fileName, Path: path, Element: local, CanonicalType: recognizedType,
		Recognized: recognized, FixtureProven: false, Disposition: DispositionBlockedDraft,
		OwningClip: clipName, OwningComposition: compositionName,
	}
	if recognized {
		disposition.Reason = "recognized provisional mapping is execution-blocked until a sanitized golden fixture proves this element's wire shape"
		return disposition
	}
	disposition.Reason = "unknown CloudTest element preserved with source path and type evidence"
	state.unsupported = append(state.unsupported, diag.Diagnostic{
		Element: local, Location: path, Severity: diag.Warning,
		Detail: "unknown CloudTest element was preserved for review and not made executable",
	})
	_ = label
	return disposition
}

func collectAssetDraft(state *documentState, disposition ElementDisposition, label string) {
	if len(state.dataPools)+len(state.environments)+len(state.monitors)+len(state.preserved) >= maxDraftAssets*4 {
		return
	}
	draft := AssetDraft{Kind: disposition.CanonicalType, Name: label, SourceFile: disposition.SourceFile, SourcePath: disposition.Path, ReviewRequired: true}
	switch disposition.CanonicalType {
	case "seed_data":
		state.dataPools = append(state.dataPools, draft)
	case "target", "property":
		state.environments = append(state.environments, draft)
	case "monitor":
		state.monitors = append(state.monitors, draft)
	case "dashboard", "touchtest_app_action", "javascript", "hds", "launch_composition":
		state.preserved = append(state.preserved, draft)
	}
}

func buildReviewScript(clip *clipDraft, projectID string) (*dsl.Script, int) {
	name := sanitizeName(firstNonEmpty(clip.name, "CloudTest Clip"))
	script := &dsl.Script{
		Name: name, Version: "1.0.0", Protocol: "http", ProjectID: strings.TrimSpace(projectID),
		GeneratedBy: "apexload-cloudtest-review-importer", GeneratedFrom: "cloudtest", CreatedAt: time.Now().UTC(),
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "cloudtest", SourceScriptName: clip.name,
			Preserved: map[string]any{"fixture_proven": false, "source_file": clip.file, "source_path": clip.path},
		}},
		Config: dsl.ScriptConfig{
			BaseURL: "http://cloudtest-import.invalid",
			Pacing:  dsl.PacingConfig{Type: "none"},
		},
		Actions: []dsl.Action{},
	}
	truncated := 0
	for _, element := range clip.elements {
		if !reviewActionElement(element.CanonicalType, element.Recognized) {
			continue
		}
		if len(script.Actions) >= maxReviewActions {
			truncated++
			continue
		}
		action := dsl.Action{
			Name:   sanitizeName(firstNonEmpty(element.CanonicalType, element.Element, "CloudTest element")),
			Type:   "source_review",
			Params: map[string]any{"function": element.Element},
			Metadata: map[string]any{
				"source_profile": "cloudtest", "source_path": element.Path,
				"source_element": element.Element, "import_disposition": string(element.Disposition),
				"fixture_proven": false,
			},
		}
		importreview.ApplyToAction(&action, []importreview.Finding{{
			Code: "cloudtest_fixture_mapping_unproven", Severity: diag.Blocker,
			Element: element.Element, Detail: element.Reason, Location: element.Path,
			Source: "cloudtest", SourceRef: element.Path, ReasonCode: "cloudtest_fixture_mapping_unproven",
			Resolution: "validate_fixture_mapping_then_replace_or_remove_review_gate", Gate: importreview.GateExecution,
		}})
		script.Actions = append(script.Actions, action)
	}
	if len(script.Actions) == 0 {
		action := dsl.Action{
			Name: "CloudTest Clip review", Type: "source_review", Params: map[string]any{"function": "clip"},
			Metadata: map[string]any{"source_profile": "cloudtest", "source_path": clip.path, "import_disposition": string(DispositionBlockedDraft), "fixture_proven": false},
		}
		importreview.ApplyToAction(&action, []importreview.Finding{{
			Code: "cloudtest_fixture_mapping_unproven", Severity: diag.Blocker, Element: "Clip",
			Detail: "Clip contents require fixture-backed mapping before execution", Location: clip.path,
			Source: "cloudtest", SourceRef: clip.path, ReasonCode: "cloudtest_fixture_mapping_unproven",
			Resolution: "validate_fixture_mapping_then_replace_or_remove_review_gate", Gate: importreview.GateExecution,
		}})
		script.Actions = append(script.Actions, action)
	}
	return script, truncated
}

func reviewActionElement(canonical string, recognized bool) bool {
	if !recognized {
		return true
	}
	switch canonical {
	case "repository", "composition", "clip", "track", "distribution", "chain", "group", "case", "default":
		return false
	default:
		return true
	}
}

var recognizedElements = map[string]string{
	"repository": "repository", "composition": "composition", "clip": "clip", "track": "track", "distribution": "distribution",
	"chain": "chain", "group": "group", "transaction": "transaction", "page": "page", "burst": "burst",
	"if": "if", "condition": "if", "switch": "switch", "case": "case", "default": "default",
	"target": "target", "property": "property", "seeddata": "seed_data", "seed": "seed_data", "dataseed": "seed_data",
	"monitor": "monitor", "dashboard": "dashboard", "http": "http", "https": "http", "request": "http",
	"soap": "soap", "jms": "jms", "database": "database", "sql": "database", "websocket": "websocket",
	"browser": "browser", "browseraction": "browser", "touchtest": "touchtest_app_action", "appaction": "touchtest_app_action",
	"javascript": "javascript", "script": "javascript", "launchcomposition": "launch_composition",
	"validation": "validation", "assertion": "validation", "extraction": "extraction", "extractor": "extraction",
	"hls": "hls", "dash": "dash", "cmaf": "cmaf", "hds": "hds",
}

func safeAttributes(attrs []xml.Attr) map[string]string {
	result := make(map[string]string)
	allowed := map[string]bool{
		"name": true, "label": true, "id": true, "clip": true, "clipref": true, "script": true, "content": true,
		"duration": true, "testduration": true, "endtime": true, "startoffset": true, "offset": true, "starttime": true,
		"rampup": true, "ramp": true, "rampduration": true, "pacing": true, "pace": true,
		"loadzone": true, "location": true, "region": true, "vusers": true, "users": true, "virtualusers": true, "concurrency": true,
	}
	for _, attr := range attrs {
		key := canonicalToken(attr.Name.Local)
		if !allowed[key] {
			continue
		}
		value := strings.TrimSpace(attr.Value)
		if len(value) > 256 {
			value = value[:256]
		}
		result[key] = value
	}
	return result
}

func durationValue(attrs map[string]string, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(attrs[key])
		if value == "" {
			continue
		}
		if _, err := time.ParseDuration(value); err == nil {
			return value
		}
		if millis, err := strconv.ParseInt(value, 10, 64); err == nil && millis >= 0 {
			return (time.Duration(millis) * time.Millisecond).String()
		}
		return value
	}
	return ""
}

func positiveInt(attrs map[string]string, keys ...string) int {
	for _, key := range keys {
		value, err := strconv.Atoi(strings.TrimSpace(attrs[key]))
		if err == nil && value > 0 {
			return value
		}
	}
	return 0
}

func canonicalToken(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(value))
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "CloudTest Review"
	}
	if len(value) > 160 {
		value = value[:160]
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
