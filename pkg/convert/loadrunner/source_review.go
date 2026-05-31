package loadrunner

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	sourceReviewMaxFiles     = 20
	sourceReviewMaxFileChars = 60000
)

// SourceReview contains the redacted source files and source-to-DSL mappings
// produced during a LoadRunner import.
type SourceReview struct {
	SourceFiles []SourceReviewFile    `json:"source_files,omitempty"`
	Mappings    []SourceReviewMapping `json:"mappings,omitempty"`
}

type SourceReviewFile struct {
	Path          string `json:"path"`
	Language      string `json:"language,omitempty"`
	Content       string `json:"content"`
	OriginalBytes int    `json:"original_bytes"`
	Truncated     bool   `json:"truncated,omitempty"`
	Redacted      bool   `json:"redacted,omitempty"`
}

type SourceReviewMapping struct {
	ID          string `json:"id"`
	SourceFile  string `json:"source_file"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line,omitempty"`
	Function    string `json:"function,omitempty"`
	TargetPath  string `json:"target_path,omitempty"`
	TargetName  string `json:"target_name,omitempty"`
	TargetKind  string `json:"target_kind,omitempty"`
	Status      string `json:"status"`
	SupportMode string `json:"support_mode,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

func BuildSourceReview(files map[string]string, mappings []SourceReviewMapping) *SourceReview {
	cleanMappings := normalizeSourceReviewMappings(mappings)
	sourceFiles := buildSourceReviewFiles(files, cleanMappings)
	if len(sourceFiles) == 0 && len(cleanMappings) == 0 {
		return nil
	}
	return &SourceReview{
		SourceFiles: sourceFiles,
		Mappings:    cleanMappings,
	}
}

func sourceReviewMappings(result *ConvertResult) []SourceReviewMapping {
	if result == nil || result.SourceReview == nil {
		return nil
	}
	return append([]SourceReviewMapping(nil), result.SourceReview.Mappings...)
}

func appendSourceReviewMapping(result *ConvertResult, mapping SourceReviewMapping) {
	if result == nil {
		return
	}
	mapping = normalizeSourceReviewMapping(mapping)
	if mapping.SourceFile == "" && mapping.TargetPath == "" && mapping.Function == "" {
		return
	}
	if result.SourceReview == nil {
		result.SourceReview = &SourceReview{}
	}
	result.SourceReview.Mappings = append(result.SourceReview.Mappings, mapping)
}

func sourceReviewMappingForFunction(fn ParsedFunction, targetPath string, targetName string, targetKind string, status string, notes string) SourceReviewMapping {
	mapping := SourceReviewMapping{
		SourceFile: fn.Filename,
		StartLine:  fn.LineNumber,
		EndLine:    fn.EndLine,
		Function:   fn.Name,
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     status,
		Notes:      notes,
	}
	return normalizeSourceReviewMapping(mapping)
}

func reviewOnlySourceReviewMappings(actions []*ParsedAction, status string, notes string) []SourceReviewMapping {
	mappings := make([]SourceReviewMapping, 0)
	for _, action := range actions {
		if action == nil {
			continue
		}
		for _, fn := range action.Functions {
			mode := LoadRunnerSupportCustomReview
			if def, ok := lookupLoadRunnerFunction(fn.Name); ok {
				mode = def.SupportMode
			}
			mappingStatus := status
			if mode == LoadRunnerSupportExternalRequired {
				mappingStatus = "external_required"
			}
			mappings = append(mappings, sourceReviewMappingWithSupportMode(fn, "", "", "review", mappingStatus, notes, mode))
		}
	}
	return mappings
}

func normalizeSourceReviewMappings(mappings []SourceReviewMapping) []SourceReviewMapping {
	if len(mappings) == 0 {
		return nil
	}
	normalized := make([]SourceReviewMapping, 0, len(mappings))
	seen := make(map[string]int, len(mappings))
	for _, mapping := range mappings {
		mapping = normalizeSourceReviewMapping(mapping)
		if mapping.SourceFile == "" && mapping.TargetPath == "" && mapping.Function == "" {
			continue
		}
		baseID := mapping.ID
		seen[baseID]++
		if seen[baseID] > 1 {
			mapping.ID = fmt.Sprintf("%s:%d", baseID, seen[baseID])
		}
		normalized = append(normalized, mapping)
	}
	return normalized
}

func normalizeSourceReviewMapping(mapping SourceReviewMapping) SourceReviewMapping {
	mapping.SourceFile = strings.TrimSpace(mapping.SourceFile)
	mapping.Function = strings.TrimSpace(mapping.Function)
	mapping.TargetPath = strings.TrimSpace(mapping.TargetPath)
	mapping.TargetName = strings.TrimSpace(mapping.TargetName)
	mapping.TargetKind = strings.TrimSpace(mapping.TargetKind)
	mapping.Status = strings.TrimSpace(mapping.Status)
	mapping.SupportMode = strings.TrimSpace(mapping.SupportMode)
	mapping.Notes = strings.TrimSpace(mapping.Notes)
	if mapping.Status == "" {
		mapping.Status = "review"
	}
	if mapping.EndLine == 0 {
		mapping.EndLine = mapping.StartLine
	}
	if mapping.StartLine == 0 && mapping.EndLine > 0 {
		mapping.StartLine = mapping.EndLine
	}
	if mapping.ID == "" {
		mapping.ID = stableSourceReviewMappingID(mapping)
	}
	return mapping
}

func stableSourceReviewMappingID(mapping SourceReviewMapping) string {
	parts := []string{
		filepath.Base(mapping.SourceFile),
		fmt.Sprintf("%d-%d", mapping.StartLine, mapping.EndLine),
		mapping.Function,
		mapping.TargetPath,
		mapping.Status,
	}
	for index, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		part = regexp.MustCompile(`[^a-z0-9_.\[\]-]+`).ReplaceAllString(part, "-")
		part = strings.Trim(part, "-")
		if part == "" {
			part = "na"
		}
		parts[index] = part
	}
	return strings.Join(parts, ":")
}

func buildSourceReviewFiles(files map[string]string, mappings []SourceReviewMapping) []SourceReviewFile {
	if len(files) == 0 {
		return nil
	}
	referenced := make(map[string]struct{})
	for _, mapping := range mappings {
		if mapping.SourceFile != "" {
			referenced[strings.ToLower(filepath.Base(mapping.SourceFile))] = struct{}{}
			referenced[strings.ToLower(mapping.SourceFile)] = struct{}{}
		}
	}

	names := make([]string, 0, len(files))
	for name := range files {
		if shouldIncludeSourceReviewFile(name, referenced) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > sourceReviewMaxFiles {
		names = names[:sourceReviewMaxFiles]
	}

	sourceFiles := make([]SourceReviewFile, 0, len(names))
	for _, name := range names {
		content, redacted := redactLoadRunnerSourceContent(files[name])
		truncated := false
		if len(content) > sourceReviewMaxFileChars {
			content = content[:sourceReviewMaxFileChars] + "\n\n[truncated for source review]"
			truncated = true
		}
		sourceFiles = append(sourceFiles, SourceReviewFile{
			Path:          name,
			Language:      sourceReviewLanguage(name),
			Content:       content,
			OriginalBytes: len([]byte(files[name])),
			Truncated:     truncated,
			Redacted:      redacted,
		})
	}
	return sourceFiles
}

func shouldIncludeSourceReviewFile(name string, referenced map[string]struct{}) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if _, ok := referenced[lower]; ok {
		return true
	}
	if _, ok := referenced[strings.ToLower(filepath.Base(lower))]; ok {
		return true
	}
	switch filepath.Ext(lower) {
	case ".c", ".h", ".usr", ".cfg", ".prm", ".dat", ".xml", ".js":
		return true
	default:
		return false
	}
}

func sourceReviewLanguage(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".c", ".h":
		return "c"
	case ".js":
		return "javascript"
	case ".xml":
		return "xml"
	case ".usr", ".cfg":
		return "ini"
	case ".prm", ".dat":
		return "data"
	default:
		return "text"
	}
}

var sourceReviewRedactors = []*regexp.Regexp{
	regexp.MustCompile(`(?i)("(?:password|passwd|pwd|token|access_token|refresh_token|id_token|api[_-]?key|secret|authorization|cookie|set-cookie)"\s*:\s*")([^"]+)(")`),
	regexp.MustCompile(`(?i)("Name=(?:password|passwd|pwd|token|access_token|refresh_token|id_token|api[_-]?key|secret)"\s*,\s*"Value=)([^"]+)(")`),
	regexp.MustCompile(`(?i)((?:password|passwd|pwd|token|access_token|refresh_token|id_token|api[_-]?key|secret)\s*=\s*)([^&\s",;]+)`),
	regexp.MustCompile(`(?i)((?:Authorization|Cookie|Set-Cookie)\s*[:=]\s*)([^\r\n"]+)`),
	regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
}

func redactLoadRunnerSourceContent(content string) (string, bool) {
	redacted := content
	for _, pattern := range sourceReviewRedactors {
		next := pattern.ReplaceAllString(redacted, `${1}[REDACTED]${3}`)
		if next != redacted {
			redacted = next
		}
	}
	return redacted, redacted != content
}
