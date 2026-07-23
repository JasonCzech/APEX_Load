package enterprisearchive

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	javaLrCallRe = regexp.MustCompile(`\blr\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

func isJavaProtocolsProject(projectConfig *ProjectConfig) bool {
	if projectConfig == nil {
		return false
	}
	if profile, _, ok := profileForProjectConfig(projectConfig); ok {
		if profile.Canonical == "java_vuser" ||
			profile.Canonical == "java_jms_solace" ||
			isSpecializedJavaProfile(profile.Canonical) {
			return true
		}
	}
	if strings.EqualFold(strings.TrimSpace(projectConfig.ScriptType), "Java_protocols") {
		return true
	}
	for _, sourceFile := range projectConfig.SourceActionFiles {
		if strings.EqualFold(sourceFile.Language, "java") || strings.HasSuffix(strings.ToLower(sourceFile.Name), ".java") {
			return true
		}
	}
	return false
}

func orderedJavaSourceActionFiles(projectConfig *ProjectConfig, files map[string]string) []string {
	candidates := make([]string, 0)
	if projectConfig != nil {
		for _, sourceFile := range projectConfig.SourceActionFiles {
			if strings.EqualFold(sourceFile.Language, "java") || strings.HasSuffix(strings.ToLower(sourceFile.Name), ".java") {
				candidates = append(candidates, sourceFile.Name)
			}
		}
	}
	if len(candidates) == 0 {
		candidates = []string{"vuser_init.java", "Actions.java", "vuser_end.java"}
	}
	ordered := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if _, ok := findFile(files, candidate); !ok {
			continue
		}
		key := strings.ToLower(filepath.Base(candidate))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, candidate)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return javaPhaseOrder(sourceActionPhase(ordered[i])) < javaPhaseOrder(sourceActionPhase(ordered[j]))
	})
	return ordered
}

func javaPhaseOrder(phase string) int {
	switch normalizeSourcePhase(phase) {
	case "init":
		return 0
	case "teardown":
		return 2
	default:
		return 1
	}
}

func findTruststoreArtifact(files map[string]string) string {
	names := make([]string, 0)
	for name := range files {
		if strings.Contains(strings.ToLower(filepath.Base(name)), "truststore") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func javaSourcesAsParsedActions(javaSources []string, files map[string]string) []*ParsedAction {
	actions := make([]*ParsedAction, 0, len(javaSources))
	for _, name := range javaSources {
		content, ok := findFile(files, name)
		if !ok {
			continue
		}
		functions := make([]ParsedFunction, 0)
		for _, match := range javaLrCallRe.FindAllStringSubmatchIndex(content, -1) {
			line := 1 + strings.Count(content[:match[0]], "\n")
			fnName := "lr." + content[match[2]:match[3]]
			functions = append(functions, ParsedFunction{
				Name:       fnName,
				Filename:   name,
				Phase:      sourceActionPhase(name),
				LineNumber: line,
				RawBlock:   fnName,
			})
		}
		actions = append(actions, &ParsedAction{
			Filename:  name,
			Phase:     sourceActionPhase(name),
			Functions: functions,
		})
	}
	return actions
}
