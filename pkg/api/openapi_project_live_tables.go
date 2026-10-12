package api

import "net/http"

// The bounded project-table decoders share typed DTOs with the handlers. Add
// the admitted wire semantics that AST types alone cannot express, without
// publishing the core's identity, lease or transport fields as operator inputs.
func applyProjectLiveTableRequestContract(route discoveredRoute, analysis *handlerContractAnalysis) {
	if analysis == nil || analysis.requestSchema == nil || route.Method != http.MethodPost {
		return
	}
	switch route.Path {
	case "/api/v1/projects/{id}/live-tables", "/api/v1/projects/{id}/live-tables/{tableID}/mutations", "/api/v1/projects/{id}/live-tables/{tableID}/lifecycle":
	default:
		return
	}
	schema := analysis.requestSchema
	closeStructuredOpenAPISchema(schema)
	schema.Description = "One JSON object of at most 1 MiB. Unknown fields are rejected."
	setDescription := func(name, description string) {
		field := schema.Properties[name]
		field.Description = description
		schema.Properties[name] = field
	}
	switch route.Path {
	case "/api/v1/projects/{id}/live-tables":
		setDescription("name", "Table name: 1–256 bytes after trimming whitespace.")
		profile := schema.Properties["profile"]
		profile.Enum = []string{"native_queue", "vts_columns", ""}
		profile.Description = "Native record queue or shared columns. Omitted or empty defaults to native_queue."
		schema.Properties["profile"] = profile
		setDescription("columns", "Declare 1–128 unique column names, each 1–128 bytes after trimming whitespace.")
		setDescription("seed_pool_id", "Optional existing data pool in this project. Its rows seed the new instance; the pool is not changed.")
		limits := schema.Properties["limits"]
		// The storage config also carries schema, but create rejects nonempty
		// nested schema fields: these belong exclusively at the top level.
		delete(limits.Properties, "profile")
		delete(limits.Properties, "columns")
		limits.Description = "Optional capacity and lease settings. Omitted or zero values use the server defaults; profile and columns belong at the top level."
		schema.Properties["limits"] = limits
	case "/api/v1/projects/{id}/live-tables/{tableID}/mutations":
		schema.Description += " Field names are case-sensitive and must be unique. Null values, duplicate cell keys and malformed Unicode are rejected; cells remain literal strings, including explicit empty strings. Retain the complete request unchanged to reconcile an uncertain result within the retry window."
		setDescription("idempotency_key", "Nonzero canonical UUID identifying this operator command. Reuse the exact request body for retries.")
		key := schema.Properties["idempotency_key"]
		key.Format = "uuid"
		schema.Properties["idempotency_key"] = key
		setDescription("issued_at", "Original command creation time. Must be inside the table retry window and at most 30 seconds in the future; keep unchanged on retry.")
		setDescription("expected_instance_id", "Immutable instance being edited; reset never redirects a command to the replacement instance.")
		setDescription("expected_revision", "Current table revision as a canonical unsigned decimal string, including zero. Exact retained retries are resolved before the revision check.")
		operation := schema.Properties["operation"]
		operation.Enum = []string{"append", "update", "clear", "increment"}
		operation.Description = "append requires values only; update requires values; clear requires columns; increment requires one column and delta. Non-append native_queue edits require record_id and expected_version; vts_columns edits require a positive position instead."
		schema.Properties["operation"] = operation
		setDescription("record_id", "Native record identity for update, clear or increment; unavailable for append and shared-column edits.")
		setDescription("position", "One-based shared-column row position (1–100000) for update, clear or increment.")
		setDescription("expected_version", "Native row version as a positive canonical unsigned decimal string.")
		setDescription("values", "At most 128 literal string cells. Append leaves omitted cells absent; update leaves unspecified cells unchanged. An explicit empty string is a present empty cell. Null is not a cell value.")
		setDescription("columns", "At most 128 selected column names for clear, or exactly one for increment.")
		setDescription("delta", "Canonical signed 64-bit decimal string, required only for increment. Numeric JSON values are not accepted.")
	case "/api/v1/projects/{id}/live-tables/{tableID}/lifecycle":
		action := schema.Properties["action"]
		action.Enum = []string{"quiesce", "resume", "reset", "archive"}
		schema.Properties["action"] = action
		setDescription("expected_config_revision", "Current definition revision as a canonical unsigned decimal string.")
		setDescription("expected_instance_id", "Current immutable instance identity.")
		setDescription("expected_revision", "Current table revision as a canonical unsigned decimal string, including zero.")
		setDescription("reason", "Required audit reason: nonblank and at most 1024 bytes.")
	}
}
