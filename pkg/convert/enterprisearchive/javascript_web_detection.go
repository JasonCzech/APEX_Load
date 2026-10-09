package enterprisearchive

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/trueclient"
)

// javaScriptWebReasonCode marks a VuGen Web HTTP/HTML script written in
// JavaScript (Action.js with web.url({...}) calls) that could not be
// translated into C (see js_parser.go); such archives become review
// artifacts instead of being misread as Java Vuser projects.
const javaScriptWebReasonCode = "javascript_web_script_unsupported"

// javaScriptWebCallRe recognizes the VuGen JavaScript web API: web.<function>(
// with an object-literal argument, as in web.url({ name: 'home', url: ... }).
var javaScriptWebCallRe = regexp.MustCompile(`\bweb\.[A-Za-z]+\s*\(\s*\{`)

// scriptLanguageIsJava reports whether a .usr ScriptLanguage or RunType value
// names Java. It matches "java" as a whole word, so "JAVA" and
// "Java-Interpreter" count and "JavaScript" does not.
func scriptLanguageIsJava(value string) bool {
	if scriptLanguageIsJavaScript(value) {
		return false
	}
	return javaWordRe.MatchString(strings.ToLower(strings.TrimSpace(value)))
}

// scriptLanguageIsJavaScript reports whether a .usr ScriptLanguage names
// JavaScript.
func scriptLanguageIsJavaScript(value string) bool {
	normalized := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case "javascript", "jscript", "js":
		return true
	}
	return false
}

// javaScriptWebActionFiles reports whether the archive is a VuGen Web
// HTTP/HTML script written in JavaScript and returns its JavaScript action
// files. TruClient and DevWeb projects also use JavaScript but have their own
// importers, so they never match. sections is the raw parsed .usr, needed
// because ParseUSRFile substitutes a default Action.c when no C or Java action
// file is declared.
func javaScriptWebActionFiles(files map[string]string, cfg *ProjectConfig, sections map[string]map[string]string) ([]string, bool) {
	if sourceZipHasDevWebMain(files) {
		return nil, false
	}
	if cfg != nil {
		if _, ok := trueclient.LookupScriptTypeScope(cfg.ScriptType); ok {
			return nil, false
		}
		if strings.EqualFold(strings.TrimSpace(cfg.ScriptType), "devweb") {
			return nil, false
		}
		declaredJS, declaredC := declaredActionFileLanguages(cfg, sections)
		if declaredC {
			return nil, false
		}
		if scriptLanguageIsJavaScript(cfg.ScriptLanguage) || len(declaredJS) > 0 {
			resolved := make([]string, 0, len(declaredJS))
			for _, name := range declaredJS {
				if found, _, ok := findFileEntry(files, name); ok {
					resolved = append(resolved, found)
				}
			}
			if len(resolved) == 0 {
				resolved = javaScriptWebSourceFiles(files)
			}
			sort.Strings(resolved)
			return resolved, true
		}
		if strings.TrimSpace(cfg.ScriptLanguage) != "" {
			// An explicit non-JavaScript language is authoritative.
			return nil, false
		}
	}
	if archiveHasCActionFile(files) {
		return nil, false
	}
	sources := javaScriptWebSourceFiles(files)
	return sources, len(sources) > 0
}

// declaredActionFileLanguages lists the .usr action files written in
// JavaScript and reports whether any is written in C. It reads the raw
// [Actions] section when one exists, because the parsed config carries a
// substituted default Action.c when no C or Java file is declared.
func declaredActionFileLanguages(cfg *ProjectConfig, sections map[string]map[string]string) (javaScript []string, hasC bool) {
	consider := func(name, language string) {
		lowerName := strings.ToLower(strings.TrimSpace(name))
		switch {
		case strings.HasSuffix(lowerName, ".js") || scriptLanguageIsJavaScript(language):
			javaScript = append(javaScript, strings.TrimSpace(name))
		case strings.HasSuffix(lowerName, ".c") || strings.EqualFold(strings.TrimSpace(language), "c"):
			hasC = true
		}
	}
	for name, section := range sections {
		if !strings.EqualFold(strings.TrimSpace(name), "actions") {
			continue
		}
		keys := make([]string, 0, len(section))
		for key := range section {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			consider(cleanConfigValue(section[key]), "")
		}
		return javaScript, hasC
	}
	for _, source := range cfg.SourceActionFiles {
		consider(source.Name, source.Language)
	}
	for _, name := range cfg.ActionFiles {
		consider(name, "")
	}
	return javaScript, hasC
}

func archiveHasCActionFile(files map[string]string) bool {
	for name := range files {
		if strings.HasSuffix(strings.ToLower(name), ".c") {
			return true
		}
	}
	return false
}

// javaScriptWebSourceFiles returns the archive's .js files that call the
// VuGen JavaScript web API.
func javaScriptWebSourceFiles(files map[string]string) []string {
	var out []string
	for name, content := range files {
		if !strings.EqualFold(path.Ext(name), ".js") {
			continue
		}
		if javaScriptWebCallRe.MatchString(content) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// newJavaScriptWebReviewResult builds the review-only result for a JavaScript
// web script that could not be translated: no executable script, the action
// files preserved for review, and a stable reason code.
func newJavaScriptWebReviewResult(files map[string]string, cfg *ProjectConfig, actionFiles []string) *ConvertResult {
	signals := []string{"JavaScript web API calls (web.<function>({...}))"}
	sourceType, language := "", ""
	if cfg != nil {
		sourceType = strings.TrimSpace(cfg.ScriptType)
		language = strings.TrimSpace(cfg.ScriptLanguage)
		if language != "" {
			signals = append(signals, ".usr [General] ScriptLanguage="+language)
		}
	}
	detected := DetectedProtocol{
		CanonicalSourceProtocol: "web_http_html",
		APEXLoadProtocol:        "http",
		ImportMode:              ImportModeReviewOnly,
		Confidence:              DetectionConfidenceHigh,
		DisplayName:             "Web - HTTP/HTML (JavaScript)",
		SourceType:              sourceType,
		Signals:                 signals,
		Warnings: []string{
			"This Web HTTP/HTML script is written in JavaScript and could not be translated into the C form APEX Load imports, so no executable script was generated (" + javaScriptWebReasonCode + "). Its action files are preserved for review; fix the JavaScript or re-record the script in C.",
		},
	}
	return newEnterpriseArchiveReviewResult(detected, files, nil, map[string]any{
		"reason_codes":            []string{javaScriptWebReasonCode},
		"javascript_action_files": actionFiles,
		"script_language":         language,
	})
}
