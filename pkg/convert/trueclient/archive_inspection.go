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
		inventory, err := inspectDefaultXML(xmlName, xmlContent)
		if err != nil {
			return nil, err
		}
		inspection.StepInventory = inventory
	} else {
		inspection.Warnings = append(inspection.Warnings, "No TruClient default.xml was found in the archive.")
	}

	inspection.PreservedMetadataInputs = preservedMetadataInputs(inspection)
	return inspection, nil
}

func inspectDefaultXML(name string, content string) (StepInventory, error) {
	decoded := normalizeXMLDeclaration(decodeXMLContent(content))
	var doc truScriptDocument
	if err := xml.Unmarshal([]byte(decoded), &doc); err != nil {
		return StepInventory{}, fmt.Errorf("parse TruClient %s for archive inspection: %w", filepath.Base(name), err)
	}

	inventory := StepInventory{
		Supported:   map[string]int{},
		ReviewOnly:  map[string]int{},
		Unsupported: map[string]int{},
	}
	for _, step := range executionSteps(doc.Steps) {
		action := strings.TrimSpace(step.Action)
		if action == "" {
			continue
		}
		inventory.Total++
		scope, ok := LookupStepScope(action)
		if !ok {
			inventory.Unsupported[action]++
			continue
		}
		switch scope.Status {
		case ScopeSupported:
			inventory.Supported[scope.CanonicalName]++
		case ScopeReviewOnly:
			inventory.ReviewOnly[scope.CanonicalName]++
		default:
			inventory.Unsupported[scope.CanonicalName]++
		}
	}
	return inventory, nil
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
