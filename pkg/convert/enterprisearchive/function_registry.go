package enterprisearchive

import (
	_ "embed"
	"strings"
)

type SourceFunctionSupportMode string

const (
	SourceFunctionSupportExecutable       SourceFunctionSupportMode = "executable"
	SourceFunctionSupportNativeNoop       SourceFunctionSupportMode = "native_noop"
	SourceFunctionSupportReviewOnly       SourceFunctionSupportMode = "review_only"
	SourceFunctionSupportExternalRequired SourceFunctionSupportMode = "external_required"
	SourceFunctionSupportCustomReview     SourceFunctionSupportMode = "custom_review"
)

type SourceFunctionDefinition struct {
	Name        string
	Family      string
	SupportMode SourceFunctionSupportMode
}

//go:embed testdata/source_c_functions_26_1.txt
var sourceFunctionRegistryText string

//go:embed testdata/sapgui_functions_26_1.txt
var sapGUIFunctionRegistryText string

//go:embed testdata/web_http_functions_26_1.txt
var webHTTPFunctionRegistryText string

var sourceFunctionRegistry = parseSourceFunctionRegistry(sourceFunctionRegistryText + "\n" + sapGUIFunctionRegistryText)

// webHTTPFunctionRegistry is the complete published LoadRunner Web (HTTP/HTML)
// protocol library. It is kept separate from sourceFunctionRegistry (the C/VUser
// utility baseline) so the documented-baseline coverage test stays scoped to the
// utility functions, while lookupSourceFunction transparently resolves both.
var webHTTPFunctionRegistry = parseSourceFunctionRegistry(webHTTPFunctionRegistryText)

func lookupSourceFunction(name string) (SourceFunctionDefinition, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if def, ok := sourceFunctionRegistry[key]; ok {
		return def, true
	}
	def, ok := webHTTPFunctionRegistry[key]
	return def, ok
}

func allSourceFunctionDefinitions() []SourceFunctionDefinition {
	defs := make([]SourceFunctionDefinition, 0, len(sourceFunctionRegistry))
	for _, def := range sourceFunctionRegistry {
		defs = append(defs, def)
	}
	return defs
}

// allWebHTTPFunctionDefinitions returns every published Web (HTTP/HTML) function
// definition (used by the coverage doc generator and the completeness test).
func allWebHTTPFunctionDefinitions() []SourceFunctionDefinition {
	defs := make([]SourceFunctionDefinition, 0, len(webHTTPFunctionRegistry))
	for _, def := range webHTTPFunctionRegistry {
		defs = append(defs, def)
	}
	return defs
}

func parseSourceFunctionRegistry(content string) map[string]SourceFunctionDefinition {
	registry := make(map[string]SourceFunctionDefinition)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		family := strings.TrimSpace(parts[1])
		mode := SourceFunctionSupportMode(strings.TrimSpace(parts[2]))
		if name == "" || family == "" || mode == "" {
			continue
		}
		registry[name] = SourceFunctionDefinition{
			Name:        name,
			Family:      family,
			SupportMode: mode,
		}
	}
	return registry
}

func isDocumentedSourceFunction(name string) bool {
	_, ok := lookupSourceFunction(name)
	return ok
}
