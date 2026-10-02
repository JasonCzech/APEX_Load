package sourceview

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	MaxFiles     = 20
	MaxFileChars = 60000
)

var mappingIDUnsafeChars = regexp.MustCompile(`[^a-z0-9_.\[\]-]+`)

// Review contains imported source files and source-to-DSL mappings produced by
// import converters for side-by-side review.
type Review struct {
	SourceFiles        []SourceFile `json:"source_files,omitempty"`
	Mappings           []Mapping    `json:"mappings,omitempty"`
	TotalSourceFiles   int          `json:"total_source_files,omitempty"`
	OmittedSourceFiles []string     `json:"omitted_source_files,omitempty"`
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
	sourceFiles, totalSourceFiles, omittedSourceFiles := buildFiles(files, cleanMappings)
	if len(sourceFiles) == 0 && len(cleanMappings) == 0 {
		return nil
	}
	return &Review{
		SourceFiles:        sourceFiles,
		Mappings:           cleanMappings,
		TotalSourceFiles:   totalSourceFiles,
		OmittedSourceFiles: omittedSourceFiles,
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
	return NewLineIndex(content).LineForOffset(offset)
}

type LineIndex struct {
	contentLen     int
	newlineOffsets []int
}

func NewLineIndex(content string) LineIndex {
	index := LineIndex{contentLen: len(content)}
	for offset, char := range content {
		if char == '\n' {
			index.newlineOffsets = append(index.newlineOffsets, offset)
		}
	}
	return index
}

func (idx LineIndex) LineForOffset(offset int) int {
	if offset <= 0 {
		return 1
	}
	if offset > idx.contentLen {
		offset = idx.contentLen
	}
	return sort.SearchInts(idx.newlineOffsets, offset) + 1
}

func (idx LineIndex) MaxLine(fallback int) int {
	if idx.contentLen == 0 {
		return fallback
	}
	return len(idx.newlineOffsets) + 1
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
	switch strings.ToLower(archiveingest.LogicalExt(path)) {
	case ".c", ".h":
		return "c"
	case ".js":
		return "javascript"
	case ".java":
		return "java"
	case ".json", ".har":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".xml", ".jmx", ".wsdl":
		return "xml"
	case ".usr", ".cfg", ".usp", ".ini":
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
	redacted = redactStructuredNameValuePairs(redacted)
	for _, redactor := range sourceRedactors {
		next := redactor.re.ReplaceAllString(redacted, redactor.replacement)
		if next != redacted {
			redacted = next
		}
	}
	return redacted, redacted != content
}

// Source-review payloads frequently preserve a name and its value as sibling
// fields instead of using the sensitive name as the JSON/XML key. Postman
// headers (`{"key":"X-Auth-Token","value":"..."}`) and TruClient arguments
// (`<Argument name="Password" value="..."/>`) are common examples. Generic
// key-aware scrubbers cannot associate those siblings, so handle those shapes
// before the ordinary text redactors run. Replacements preserve surrounding
// formatting so source-to-script line mappings remain valid.
func redactStructuredNameValuePairs(content string) string {
	content = jsonNamedValuePair.ReplaceAllStringFunc(content, redactJSONNamedValuePair)
	content = jsonValueNamedPair.ReplaceAllStringFunc(content, redactJSONValueNamedPair)
	content = xmlTagPattern.ReplaceAllStringFunc(content, redactXMLNamedValueTag)
	content = cssSensitiveAttributePattern.ReplaceAllString(content, `${1}[REDACTED]${4}`)
	return content
}

func redactJSONNamedValuePair(candidate string) string {
	matches := jsonNamedValuePair.FindStringSubmatch(candidate)
	if len(matches) != 5 || !reviewSensitiveName(unquoteJSONString(matches[2])) {
		return candidate
	}
	return matches[1] + scrub.Redacted + matches[4]
}

func redactJSONValueNamedPair(candidate string) string {
	matches := jsonValueNamedPair.FindStringSubmatch(candidate)
	if len(matches) != 5 || !reviewSensitiveName(unquoteJSONString(matches[4])) {
		return candidate
	}
	return matches[1] + scrub.Redacted + matches[3]
}

func redactXMLNamedValueTag(tag string) string {
	matches := xmlSemanticNameAttribute.FindStringSubmatch(tag)
	if len(matches) != 3 || !reviewSensitiveName(matches[2]) {
		return tag
	}
	return xmlValueAttribute.ReplaceAllStringFunc(tag, func(attribute string) string {
		quoteOffset := strings.IndexAny(attribute, `"'`)
		if quoteOffset < 0 {
			return attribute
		}
		return attribute[:quoteOffset+1] + scrub.Redacted + attribute[len(attribute)-1:]
	})
}

func unquoteJSONString(value string) string {
	decoded, err := strconv.Unquote(`"` + value + `"`)
	if err != nil {
		return value
	}
	return decoded
}

func reviewSensitiveName(name string) bool {
	return scrub.SensitiveName(name) || (scrub.Policy{}).SensitiveHeader(name)
}

func buildFiles(files map[string]string, mappings []Mapping) ([]SourceFile, int, []string) {
	if len(files) == 0 {
		return nil, 0, nil
	}
	referenced := make(map[string]struct{})
	exactReferenced := make(map[string]struct{})
	for _, mapping := range mappings {
		if mapping.SourceFile == "" {
			continue
		}
		referenced[strings.ToLower(mapping.SourceFile)] = struct{}{}
		exactReferenced[strings.ToLower(mapping.SourceFile)] = struct{}{}
		referenced[strings.ToLower(archiveingest.LogicalBase(mapping.SourceFile))] = struct{}{}
	}

	names := make([]string, 0, len(files))
	for name := range files {
		if shouldIncludeFile(name, referenced) {
			names = append(names, name)
		}
	}
	// Spend the bounded display budget on files that a review mapping opens
	// before auxiliary source. Keep deterministic order within both groups.
	sort.Slice(names, func(i, j int) bool {
		_, left := exactReferenced[strings.ToLower(names[i])]
		_, right := exactReferenced[strings.ToLower(names[j])]
		if left != right {
			return left
		}
		return names[i] < names[j]
	})
	totalSourceFiles := len(names)
	var omittedSourceFiles []string
	if len(names) > MaxFiles {
		omittedSourceFiles = append([]string(nil), names[MaxFiles:]...)
		names = names[:MaxFiles]
	}

	out := make([]SourceFile, 0, len(names))
	for _, name := range names {
		raw := files[name]
		content := raw
		if decoded, ok := decodeReviewSourceText(name, raw); ok {
			content = decoded
		}
		truncated := false
		if len(content) > MaxFileChars {
			end := MaxFileChars
			for end > 0 && !utf8.RuneStart(content[end]) {
				end--
			}
			content = content[:end] + "\n\n[truncated for source review]"
			truncated = true
		}
		out = append(out, SourceFile{
			Path:          name,
			Language:      Language(name),
			Content:       content,
			OriginalBytes: len([]byte(raw)),
			Truncated:     truncated,
			Redacted:      false,
		})
	}
	return out, totalSourceFiles, omittedSourceFiles
}

func shouldIncludeFile(name string, referenced map[string]struct{}) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if _, ok := referenced[lower]; ok {
		return true
	}
	if _, ok := referenced[strings.ToLower(archiveingest.LogicalBase(lower))]; ok {
		return true
	}
	switch archiveingest.LogicalExt(lower) {
	case ".c", ".h", ".usr", ".cfg", ".usp", ".prm", ".dat", ".xml", ".jmx", ".wsdl", ".js", ".java", ".json", ".har", ".yaml", ".yml", ".http", ".txt":
		return true
	default:
		return len(referenced) == 0
	}
}

func stableMappingID(mapping Mapping) string {
	parts := []string{
		archiveingest.LogicalBase(mapping.SourceFile),
		fmt.Sprintf("%d-%d", mapping.StartLine, mapping.EndLine),
		mapping.Function,
		mapping.TargetPath,
		mapping.Status,
	}
	for index, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		part = mappingIDUnsafeChars.ReplaceAllString(part, "-")
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

var (
	jsonNamedValuePair = regexp.MustCompile(`(?is)("(?:key|name|field|label|header)"\s*:\s*"((?:\\.|[^"\\])*)"\s*,\s*"(?:value|text|content|selector|xpath)"\s*:\s*")((?:\\.|[^"\\])*)(")`)
	jsonValueNamedPair = regexp.MustCompile(`(?is)("(?:value|text|content|selector|xpath)"\s*:\s*")((?:\\.|[^"\\])*)("\s*,\s*"(?:key|name|field|label|header)"\s*:\s*"((?:\\.|[^"\\])*)")`)
	// Go's regexp engine caps a single counted repetition at 1000. Segment the
	// 4096-byte tag bound so generated binaries compile while preserving the
	// original source-review work limit.
	xmlTagPattern                = regexp.MustCompile(`(?is)<[^>]{1,1000}[^>]{0,1000}[^>]{0,1000}[^>]{0,1000}[^>]{0,96}>`)
	xmlSemanticNameAttribute     = regexp.MustCompile(`(?is)\b(?:name|key|field|label|header)\s*=\s*(["'])([^"']*)["']`)
	xmlValueAttribute            = regexp.MustCompile(`(?is)\b(?:value|text|content|selector|xpath|regex)\s*=\s*(?:"[^"]*"|'[^']*')`)
	cssSensitiveAttributePattern = regexp.MustCompile(`(?is)(\[\s*(?:data[-_:]?|aria[-_:]?)?(?:password|passwd|pwd|token|secret|api[-_:]?key|authorization|cookie|session(?:[-_:]?id)?)\s*(?:[*^$|~]?=)\s*(["']))([^"']*)(["'])`)
)

// ReviewOnlyActionCount is how many converted actions were preserved for review
// rather than execution. It is the review-only bucket of an import's conversion
// accounting, and it reads only the shared review shape, so every importer
// derives it the same way.
func (r *Review) ReviewOnlyActionCount() int {
	if r == nil {
		return 0
	}
	count := 0
	for _, mapping := range r.Mappings {
		if strings.EqualFold(mapping.TargetKind, "action") &&
			strings.TrimSpace(mapping.TargetPath) != "" &&
			strings.EqualFold(mapping.Status, "review_only") {
			count++
		}
	}
	return count
}
