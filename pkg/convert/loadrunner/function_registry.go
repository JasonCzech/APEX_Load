package loadrunner

import (
	"embed"
	"sort"
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
	Reason      string
}

// loadRunnerFunctionCatalog holds the per-family CSV catalogs describing the entire
// VuGen function reference. Each row is "name,family,support_mode[,reason]"; the optional
// reason column lets curators document a function-specific disposition while the loader
// falls back to a deterministic family/support-mode default when it is omitted.
//
//go:embed testdata/loadrunner_functions/*.csv
var loadRunnerFunctionCatalog embed.FS

const loadRunnerFunctionCatalogDir = "testdata/loadrunner_functions"

var loadRunnerFunctionRegistry = loadLoadRunnerFunctionRegistry()

func loadLoadRunnerFunctionRegistry() map[string]LoadRunnerFunctionDefinition {
	registry := make(map[string]LoadRunnerFunctionDefinition)
	entries, err := loadRunnerFunctionCatalog.ReadDir(loadRunnerFunctionCatalogDir)
	if err != nil {
		return registry
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}
		data, err := loadRunnerFunctionCatalog.ReadFile(loadRunnerFunctionCatalogDir + "/" + entry.Name())
		if err != nil {
			continue
		}
		for name, def := range parseLoadRunnerFunctionRegistry(string(data)) {
			registry[name] = def
		}
	}
	return registry
}

func lookupLoadRunnerFunction(name string) (LoadRunnerFunctionDefinition, bool) {
	def, ok := loadRunnerFunctionRegistry[strings.ToLower(strings.TrimSpace(name))]
	return def, ok
}

func allLoadRunnerFunctionDefinitions() []LoadRunnerFunctionDefinition {
	defs := make([]LoadRunnerFunctionDefinition, 0, len(loadRunnerFunctionRegistry))
	for _, def := range loadRunnerFunctionRegistry {
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

func parseLoadRunnerFunctionRegistry(content string) map[string]LoadRunnerFunctionDefinition {
	registry := make(map[string]LoadRunnerFunctionDefinition)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 4)
		if len(parts) < 3 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		family := strings.TrimSpace(parts[1])
		mode := LoadRunnerSupportMode(strings.TrimSpace(parts[2]))
		if name == "" || family == "" || mode == "" {
			continue
		}
		reason := ""
		if len(parts) == 4 {
			reason = strings.TrimSpace(parts[3])
		}
		if reason == "" {
			reason = defaultLoadRunnerReason(family, mode)
		}
		registry[name] = LoadRunnerFunctionDefinition{
			Name:        name,
			Family:      family,
			SupportMode: mode,
			Reason:      reason,
		}
	}
	return registry
}

// defaultLoadRunnerReason returns a deterministic disposition note for a catalog row that
// does not supply an explicit reason. The wording mirrors loadRunnerSupportNotes so the
// source-review audit trail is unchanged for rows that predate the reason column.
func defaultLoadRunnerReason(family string, mode LoadRunnerSupportMode) string {
	switch mode {
	case LoadRunnerSupportExternalRequired:
		return "Documented LoadRunner function requires external runtime or governed secret integration."
	case LoadRunnerSupportReviewOnly:
		return "Documented LoadRunner function preserved as a native review action."
	case LoadRunnerSupportNativeNoop:
		return "Documented LoadRunner function has no direct APEX Load side effect and is handled as a native no-op."
	case LoadRunnerSupportCustomReview:
		return "Parsed custom C helper call preserved as a review action."
	default:
		return "Converted documented LoadRunner " + family + " function to a native action."
	}
}

func isDocumentedLoadRunnerFunction(name string) bool {
	_, ok := lookupLoadRunnerFunction(name)
	return ok
}
