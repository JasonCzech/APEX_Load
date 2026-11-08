package api

import "fmt"

// applyScriptDebugRequestDocs documents request fields whose bounds the
// handler enforces but the Go type cannot express.
func applyScriptDebugRequestDocs(pkg, name string, schema *oasSchema) {
	if schema == nil || (pkg != "" && pkg != "api") || name != "ScriptDebugStartRequest" {
		return
	}
	if property, ok := schema.Properties["iterations"]; ok {
		property.Description = fmt.Sprintf("Action-phase iterations to run, 1 to %d. 0 or omitted runs one iteration; other values return 400.", maxScriptDebugIterations)
		schema.Properties["iterations"] = property
	}
}
