package trueclient

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type ArchiveInspection struct {
	ScriptType              string                 `json:"script_type"`
	ScriptTypeScope         *ScriptTypeScope       `json:"script_type_scope,omitempty"`
	ProjectFiles            []ArchiveFileSummary   `json:"project_files,omitempty"`
	DefaultXML              *ArchiveFileSummary    `json:"default_xml,omitempty"`
	ObjectRepositories      []ArchiveFileSummary   `json:"object_repositories,omitempty"`
	ParameterFiles          []ArchiveFileSummary   `json:"parameter_files,omitempty"`
	RuntimeFiles            []ArchiveFileSummary   `json:"runtime_files,omitempty"`
	StepInventory           StepInventory          `json:"step_inventory"`
	Steps                   []StepInspection       `json:"steps,omitempty"`
	UnsupportedFragments    []SourceFragment       `json:"unsupported_fragments,omitempty"`
	PreservedMetadataInputs []string               `json:"preserved_metadata_inputs,omitempty"`
	Warnings                []string               `json:"warnings,omitempty"`
	CapabilityStatuses      map[string]ScopeStatus `json:"capability_statuses,omitempty"`
}

type ArchiveFileSummary struct {
	Path     string `json:"path"`
	BaseName string `json:"base_name"`
	Kind     string `json:"kind"`
	Bytes    int    `json:"bytes"`
}

type StepInventory struct {
	Total       int            `json:"total"`
	Supported   map[string]int `json:"supported,omitempty"`
	ReviewOnly  map[string]int `json:"review_only,omitempty"`
	Unsupported map[string]int `json:"unsupported,omitempty"`
}

type StepInspection struct {
	StepID        string      `json:"step_id,omitempty"`
	Index         string      `json:"index,omitempty"`
	Action        string      `json:"action"`
	ActionName    string      `json:"action_name,omitempty"`
	Status        ScopeStatus `json:"status"`
	ScopeName     string      `json:"scope_name,omitempty"`
	SourceSnippet string      `json:"source_snippet,omitempty"`
}

type SourceFragment struct {
	StepID        string      `json:"step_id,omitempty"`
	Index         string      `json:"index,omitempty"`
	Action        string      `json:"action"`
	Status        ScopeStatus `json:"status"`
	SourceSnippet string      `json:"source_snippet,omitempty"`
	Reason        string      `json:"reason"`
}

func InspectArchive(files map[string]string, scriptType string) (*ArchiveInspection, error) {
	if files == nil {
		files = map[string]string{}
	}

	inspection := &ArchiveInspection{
		ScriptType:              strings.TrimSpace(scriptType),
		CapabilityStatuses:      web2CapabilityStatusMap(),
		PreservedMetadataInputs: make([]string, 0),
	}
	if scope, ok := LookupScriptTypeScope(scriptType); ok {
		inspection.ScriptTypeScope = &scope
	}

	for name, content := range files {
		summary := archiveFileSummary(name, content)
		switch classifyArchiveFile(name) {
		case "project":
			summary.Kind = "project"
			inspection.ProjectFiles = append(inspection.ProjectFiles, summary)
		case "object_repository":
			summary.Kind = "object_repository"
			inspection.ObjectRepositories = append(inspection.ObjectRepositories, summary)
		case "parameter":
			summary.Kind = "parameter"
			inspection.ParameterFiles = append(inspection.ParameterFiles, summary)
		case "runtime":
			summary.Kind = "runtime"
			inspection.RuntimeFiles = append(inspection.RuntimeFiles, summary)
		}
	}
	sortArchiveFiles(inspection.ProjectFiles)
	sortArchiveFiles(inspection.ObjectRepositories)
	sortArchiveFiles(inspection.ParameterFiles)
	sortArchiveFiles(inspection.RuntimeFiles)

	if xmlName, xmlContent, ok := findDefaultXML(files); ok {
		summary := archiveFileSummary(xmlName, xmlContent)
		summary.Kind = "default_xml"
		inspection.DefaultXML = &summary
		inventory, steps, fragments, err := inspectDefaultXML(xmlName, xmlContent)
		if err != nil {
			return nil, err
		}
		inspection.StepInventory = inventory
		inspection.Steps = steps
		inspection.UnsupportedFragments = fragments
	} else {
		inspection.Warnings = append(inspection.Warnings, "No TruClient default.xml was found in the archive.")
	}

	inspection.PreservedMetadataInputs = preservedMetadataInputs(inspection)
	return inspection, nil
}

func inspectDefaultXML(name string, content string) (StepInventory, []StepInspection, []SourceFragment, error) {
	decoded := normalizeXMLDeclaration(decodeXMLContent(content))
	var doc truScriptDocument
	if err := xml.Unmarshal([]byte(decoded), &doc); err != nil {
		return StepInventory{}, nil, nil, fmt.Errorf("parse TruClient %s for archive inspection: %w", filepath.Base(name), err)
	}

	inventory := StepInventory{
		Supported:   map[string]int{},
		ReviewOnly:  map[string]int{},
		Unsupported: map[string]int{},
	}
	steps := make([]StepInspection, 0)
	fragments := make([]SourceFragment, 0)
	for _, step := range executionSteps(doc.Steps) {
		action := strings.TrimSpace(step.Action)
		if action == "" {
			continue
		}
		inventory.Total++
		scope, ok := LookupStepScope(action)
		if !ok {
			inventory.Unsupported[action]++
			stepInspection := inspectStep(step, ScopeOutOfScope, "")
			steps = append(steps, stepInspection)
			fragments = append(fragments, sourceFragment(stepInspection, "Unsupported TruClient step action is not mapped to a Stormrunner browser action."))
			continue
		}
		switch scope.Status {
		case ScopeSupported:
			inventory.Supported[scope.CanonicalName]++
		case ScopeReviewOnly:
			inventory.ReviewOnly[scope.CanonicalName]++
			stepInspection := inspectStep(step, scope.Status, scope.CanonicalName)
			steps = append(steps, stepInspection)
			fragments = append(fragments, sourceFragment(stepInspection, "Review-only TruClient step requires replay or guided repair before approval."))
			continue
		default:
			inventory.Unsupported[scope.CanonicalName]++
			stepInspection := inspectStep(step, scope.Status, scope.CanonicalName)
			steps = append(steps, stepInspection)
			fragments = append(fragments, sourceFragment(stepInspection, "TruClient step scope is not executable in the current browser draft importer."))
			continue
		}
		steps = append(steps, inspectStep(step, scope.Status, scope.CanonicalName))
	}
	return inventory, steps, fragments, nil
}

func archiveFileSummary(path string, content string) ArchiveFileSummary {
	return ArchiveFileSummary{
		Path:     path,
		BaseName: filepath.Base(path),
		Bytes:    len([]byte(content)),
	}
}

func classifyArchiveFile(path string) string {
	base := strings.ToLower(filepath.Base(path))
	dir := strings.ToLower(filepath.Dir(path))
	ext := strings.ToLower(filepath.Ext(base))
	compact := strings.ReplaceAll(base, "_", "")
	compact = strings.ReplaceAll(compact, "-", "")
	compactDir := strings.ReplaceAll(dir, "_", "")
	compactDir = strings.ReplaceAll(compactDir, "-", "")

	switch {
	case ext == ".usr":
		return "project"
	case strings.EqualFold(base, "default.cfg") || strings.EqualFold(base, "default.usp") || strings.Contains(compact, "runtime"):
		return "runtime"
	case ext == ".dat" || ext == ".csv" || strings.Contains(compactDir, "parameter") || strings.Contains(compact, "parameter"):
		return "parameter"
	case strings.Contains(compact, "objectrepository") || strings.Contains(compactDir, "objectrepository") || strings.Contains(compactDir, "objects"):
		return "object_repository"
	default:
		return ""
	}
}

func sortArchiveFiles(files []ArchiveFileSummary) {
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
}

func preservedMetadataInputs(inspection *ArchiveInspection) []string {
	inputs := make([]string, 0)
	if inspection == nil {
		return inputs
	}
	appendKind := func(kind string, files []ArchiveFileSummary) {
		for _, file := range files {
			inputs = append(inputs, kind+": "+file.Path)
		}
	}
	if inspection.DefaultXML != nil {
		inputs = append(inputs, "default_xml: "+inspection.DefaultXML.Path)
	}
	appendKind("project", inspection.ProjectFiles)
	appendKind("object_repository", inspection.ObjectRepositories)
	appendKind("parameter", inspection.ParameterFiles)
	appendKind("runtime", inspection.RuntimeFiles)
	sort.Strings(inputs)
	return inputs
}

func web2CapabilityStatusMap() map[string]ScopeStatus {
	statuses := make(map[string]ScopeStatus)
	for _, scope := range Web2ProfileCapabilityScopes() {
		statuses[scope.CanonicalName] = scope.Status
	}
	return statuses
}

func inspectStep(step truXMLStep, status ScopeStatus, scopeName string) StepInspection {
	return StepInspection{
		StepID:        strings.TrimSpace(step.StepID),
		Index:         strings.TrimSpace(step.Index),
		Action:        strings.TrimSpace(step.Action),
		ActionName:    strings.TrimSpace(step.ActionName),
		Status:        status,
		ScopeName:     scopeName,
		SourceSnippet: compactSourceSnippet(step.Arguments),
	}
}

func sourceFragment(step StepInspection, reason string) SourceFragment {
	return SourceFragment{
		StepID:        step.StepID,
		Index:         step.Index,
		Action:        step.Action,
		Status:        step.Status,
		SourceSnippet: step.SourceSnippet,
		Reason:        reason,
	}
}

func compactSourceSnippet(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(value) <= 500 {
		return value
	}
	return value[:500] + "..."
}
