package loadrunner

import (
	_ "embed"
	"strings"
)

type LoadRunnerSupportMode string

const (
	LoadRunnerSupportExecutable       LoadRunnerSupportMode = "executable"
	LoadRunnerSupportNativeNoop       LoadRunnerSupportMode = "native_noop"
	LoadRunnerSupportReviewOnly       LoadRunnerSupportMode = "review_only"
	LoadRunnerSupportExternalRequired LoadRunnerSupportMode = "external_required"
	LoadRunnerSupportCustomReview     LoadRunnerSupportMode = "custom_review"
)

type LoadRunnerFunctionDefinition struct {
	Name        string
	Family      string
	SupportMode LoadRunnerSupportMode
}

//go:embed testdata/loadrunner_c_functions_26_1.txt
var loadRunnerFunctionRegistryText string

var loadRunnerFunctionRegistry = parseLoadRunnerFunctionRegistry(loadRunnerFunctionRegistryText)

func lookupLoadRunnerFunction(name string) (LoadRunnerFunctionDefinition, bool) {
	def, ok := loadRunnerFunctionRegistry[strings.ToLower(strings.TrimSpace(name))]
	return def, ok
}

func allLoadRunnerFunctionDefinitions() []LoadRunnerFunctionDefinition {
	defs := make([]LoadRunnerFunctionDefinition, 0, len(loadRunnerFunctionRegistry))
	for _, def := range loadRunnerFunctionRegistry {
		defs = append(defs, def)
	}
	return defs
}

func parseLoadRunnerFunctionRegistry(content string) map[string]LoadRunnerFunctionDefinition {
	registry := make(map[string]LoadRunnerFunctionDefinition)
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
		mode := LoadRunnerSupportMode(strings.TrimSpace(parts[2]))
		if name == "" || family == "" || mode == "" {
			continue
		}
		registry[name] = LoadRunnerFunctionDefinition{
			Name:        name,
			Family:      family,
			SupportMode: mode,
		}
	}
	return registry
}

func isDocumentedLoadRunnerFunction(name string) bool {
	_, ok := lookupLoadRunnerFunction(name)
	return ok
}
