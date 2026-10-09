package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// convertJavaScriptWebZip imports a VuGen JavaScript web script: each
// JavaScript action file gets a C twin (Action.js → Action.c, see
// translateJavaScriptToC), the .usr declares the C files, and the archive is
// imported through the C pipeline. It returns false when the script cannot
// be translated, so the caller keeps the review-only result.
func convertJavaScriptWebZip(files map[string]string, projectFileName string, actionFiles []string, opts ConvertOptions) (*ConvertResult, string, error) {
	translated, warnings, reason := translateJavaScriptWebArchive(files, projectFileName, actionFiles)
	if reason != "" {
		return nil, reason, nil
	}
	archive, err := zipArchiveFiles(translated)
	if err != nil {
		return nil, "", err
	}
	result, err := ConvertZip(archive, opts)
	if err != nil {
		return nil, "", err
	}
	note := fmt.Sprintf("Imported from VuGen JavaScript (%s): each web.* and lr.* call was translated into its C form.", strings.Join(actionFiles, ", "))
	result.Warnings = append([]string{note}, append(warnings, result.Warnings...)...)
	if result.DetectedProtocol != nil {
		result.DetectedProtocol.DisplayName = "Web - HTTP/HTML (JavaScript)"
		result.DetectedProtocol.Signals = append(result.DetectedProtocol.Signals, "JavaScript web API calls (web.<function>({...}))")
	}
	return result, "", nil
}

// translateJavaScriptWebArchive returns a copy of the archive with a C twin
// of each JavaScript action file and a .usr that declares the twins.
func translateJavaScriptWebArchive(files map[string]string, projectFileName string, actionFiles []string) (map[string]string, []string, string) {
	if projectFileName == "" {
		return nil, nil, "the archive has no .usr project file"
	}
	out := make(map[string]string, len(files)+len(actionFiles))
	for name, content := range files {
		out[name] = content
	}
	var warnings []string
	renames := map[string]string{}
	for _, name := range actionFiles {
		twin := strings.TrimSuffix(name, path.Ext(name)) + ".c"
		if _, exists := files[twin]; exists {
			return nil, nil, fmt.Sprintf("%s already exists next to %s", twin, name)
		}
		source, notes, err := translateJavaScriptToC(name, files[name])
		if err != nil {
			return nil, nil, err.Error()
		}
		out[twin] = source
		renames[path.Base(name)] = path.Base(twin)
		for _, note := range notes {
			warnings = append(warnings, fmt.Sprintf("%s %s; it imports as a review step", name, note))
		}
	}
	out[projectFileName] = rewriteJavaScriptProject(files[projectFileName], renames)
	return out, warnings, ""
}

var scriptLanguageLine = regexp.MustCompile(`(?im)^\s*ScriptLanguage\s*=.*$`)

// rewriteJavaScriptProject points the .usr at the C twins and drops the
// JavaScript language declaration.
func rewriteJavaScriptProject(content string, renames map[string]string) string {
	content = scriptLanguageLine.ReplaceAllString(content, "")
	names := make([]string, 0, len(renames))
	for name := range renames {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pattern := regexp.MustCompile(`(?i)(=\s*)` + regexp.QuoteMeta(name) + `\s*$`)
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			lines[i] = pattern.ReplaceAllString(strings.TrimRight(line, "\r"), "${1}"+renames[name])
		}
		content = strings.Join(lines, "\n")
	}
	return content
}

func zipArchiveFiles(files map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
