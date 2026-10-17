package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

func EnsureCredentialPreservingImportMetadata(script *dsl.Script, sourceProfile string) {
	if script == nil {
		return
	}
	sourceProfile = strings.TrimSpace(sourceProfile)
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if strings.TrimSpace(script.Metadata.Import.Source) == "" {
		script.Metadata.Import.Source = sourceProfile
	}
	MarkCredentialPreservingImportScript(script)
}

func IsCredentialPreservingImport(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		strings.TrimSpace(script.Metadata.Import.Source) == "" {
		return false
	}
	policy, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey].(string)
	return strings.EqualFold(
		strings.TrimSpace(policy),
		dsl.ImportPreservedCredentialPolicyPreserve,
	)
}

func ValidateStoredExecutionScript(script *dsl.Script) []string {
	if IsCredentialPreservingImport(script) {
		return dsl.ValidateScriptIssuesWithPreservedImportCredentials(script)
	}
	return dsl.ValidateScriptIssues(script)
}

func MarkCredentialPreservingImportScript(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		strings.TrimSpace(script.Metadata.Import.Source) == "" {
		return
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey] =
		dsl.ImportPreservedCredentialPolicyPreserve
}
