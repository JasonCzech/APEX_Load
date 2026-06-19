package sourceview

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
)

const (
	MaxFiles     = 20
	MaxFileChars = 60000
)

// Review contains redacted source files and source-to-DSL mappings produced by
// import converters for side-by-side review.
type Review struct {
	SourceFiles []SourceFile `json:"source_files,omitempty"`
	Mappings    []Mapping    `json:"mappings,omitempty"`
}

type SourceFile struct {
	Path          string `json:"path"`
	Language      string `json:"language,omitempty"`
	Content       string `json:"content"`
	OriginalBytes int    `json:"original_bytes"`
	Truncated     bool   `json:"truncated,omitempty"`
	Redacted      bool   `json:"redacted,omitempty"`
}

type Mapping struct {
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

func Build(files map[string]string, mappings []Mapping) *Review {
	cleanMappings := NormalizeMappings(mappings)
	sourceFiles := buildFiles(files, cleanMappings)
	if len(sourceFiles) == 0 && len(cleanMappings) == 0 {
		return nil
	}
	return &Review{
		SourceFiles: sourceFiles,
		Mappings:    cleanMappings,
	}
}

func NormalizeMappings(mappings []Mapping) []Mapping {
	if len(mappings) == 0 {
		return nil
	}
	normalized := make([]Mapping, 0, len(mappings))
	seen := make(map[string]int, len(mappings))
	for _, mapping := range mappings {
		mapping = NormalizeMapping(mapping)
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

func NormalizeMapping(mapping Mapping) Mapping {
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
		mapping.ID = stableMappingID(mapping)
	}
	return mapping
}

func LineForOffset(content string, offset int) int {
	if offset <= 0 {
		return 1
	}
	if offset > len(content) {
		offset = len(content)
	}
	return strings.Count(content[:offset], "\n") + 1
}

func CoarseMapping(sourceFile string, function string, targetPath string, targetName string, targetKind string, status string, notes string) Mapping {
	return Mapping{
		SourceFile: sourceFile,
		StartLine:  1,
		EndLine:    1,
		Function:   function,
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     convertutil.FirstNonEmpty(status, "review_only"),
		Notes:      convertutil.FirstNonEmpty(notes, "coarse source range; exact child span unavailable"),
	}
}

func Language(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".h":
		return "c"
	case ".js":
		return "javascript"
	case ".json", ".har":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".xml", ".jmx", ".wsdl":
		return "xml"
	case ".usr", ".cfg", ".ini":
		return "ini"
	case ".prm", ".dat", ".csv":
		return "data"
	case ".http":
		return "http"
	default:
		return "text"
	}
}

func Redact(content string) (string, bool) {
	redacted := content
	for _, redactor := range sourceRedactors {
		next := redactor.re.ReplaceAllString(redacted, redactor.replacement)
		if next != redacted {
			redacted = next
		}
	}
	return redacted, redacted != content
}

func buildFiles(files map[string]string, mappings []Mapping) []SourceFile {
	if len(files) == 0 {
		return nil
	}
	referenced := make(map[string]struct{})
	for _, mapping := range mappings {
		if mapping.SourceFile == "" {
			continue
		}
		referenced[strings.ToLower(mapping.SourceFile)] = struct{}{}
		referenced[strings.ToLower(filepath.Base(mapping.SourceFile))] = struct{}{}
	}

	names := make([]string, 0, len(files))
	for name := range files {
		if shouldIncludeFile(name, referenced) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > MaxFiles {
		names = names[:MaxFiles]
	}

	out := make([]SourceFile, 0, len(names))
	for _, name := range names {
		content, redacted := Redact(files[name])
		truncated := false
		if len(content) > MaxFileChars {
			content = content[:MaxFileChars] + "\n\n[truncated for source review]"
			truncated = true
		}
		out = append(out, SourceFile{
			Path:          name,
			Language:      Language(name),
			Content:       content,
			OriginalBytes: len([]byte(files[name])),
			Truncated:     truncated,
			Redacted:      redacted,
		})
	}
	return out
}

func shouldIncludeFile(name string, referenced map[string]struct{}) bool {
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
	case ".c", ".h", ".usr", ".cfg", ".prm", ".dat", ".xml", ".jmx", ".wsdl", ".js", ".json", ".har", ".yaml", ".yml", ".http", ".txt":
		return true
	default:
		return len(referenced) == 0
	}
}

func stableMappingID(mapping Mapping) string {
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

type sourceRedactor struct {
	re          *regexp.Regexp
	replacement string
}

var sourceRedactors = []sourceRedactor{
	{regexp.MustCompile(`(?i)(lr_unmask\(\s*")([^"]+)("\s*\))`), `${1}[REDACTED]${3}`},
	{regexp.MustCompile(`(?i)("(?:password|passwd|pwd|token|access_token|refresh_token|id_token|api[_-]?key|secret|authorization|cookie|set-cookie)"\s*:\s*")([^"]+)(")`), `${1}[REDACTED]${3}`},
	{regexp.MustCompile(`(?i)("Name=(?:password|passwd|pwd|token|access_token|refresh_token|id_token|api[_-]?key|secret)"\s*,\s*"Value=)([^"]+)(")`), `${1}[REDACTED]${3}`},
	{regexp.MustCompile(`(?i)((?:password|passwd|pwd|token|access_token|refresh_token|id_token|api[_-]?key|secret)\s*=\s*)([^&\s",;]+)`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)((?:Authorization|Cookie|Set-Cookie)\s*[:=]\s*)([^\r\n"]+)`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), `${1}[REDACTED]`},
}
