package dsl

// The table profiles an operation can need (pkg/livetable Profile values).
const (
	liveTableKindWorkQueue     = "native_queue"
	liveTableKindSharedColumns = "vts_columns"
)

var liveTableEmptyLabels = map[string]string{"error": "End the iteration with an error", "skip": "Skip and continue"}

func liveTableActionSpecs() []ActionSpec {
	return []ActionSpec{{
		Type: "live_table", Protocols: []string{"*"},
		Summary:          "Performs an atomic operation on an explicitly bound run-scoped live table. Existing variables remain snapshots until read again.",
		ExecutionPosture: "supported",
		ExecutionNote:    "Requires an admitted live-table binding. Unmanaged runs and replay fail closed without an explicit isolated seed. Leases permit redelivery; they do not make external work exactly once.",
		Fields: []FieldSpec{
			{Name: "params.alias", Kind: FieldKindString, Required: true, Label: "Table alias", Widget: FieldWidgetAlias, Placeholder: "orders", Doc: "Static alias of a trusted run binding; never a remote URL or table credential."},
			{Name: "params.operation", Kind: FieldKindEnum, Required: true, SchemaEnum: true, Label: "Operation", Enum: []string{"read", "next", "push", "pop", "lease", "renew", "ack", "release", "update", "increment", "clear", "rotate", "create_column", "read_column"}},
			{Name: "params.result_prefix", Kind: FieldKindString, Label: "Save results as", Widget: FieldWidgetText, Placeholder: "order", Doc: "Static var namespace prefix. Row fields appear under <prefix>.values.<column>; metadata includes status, revision, record_id and record_version. Bounded ranges use JSON rows/cells/columns. Missing results clear prior outputs. For lease, also identifies the private lease reference."},
			{Name: "params.record_id", Kind: FieldKindString, Label: "Record ID", Widget: FieldWidgetOutputRef, OutputKind: "record_id", Doc: "Stable native record ID; templates allowed."},
			{Name: "params.position", Kind: FieldKindInt, Maximum: 1000000, Label: "Position", Placeholder: "1", Doc: "One-based position for operations supporting positional access."},
			{Name: "params.columns", Kind: FieldKindArray, Label: "Columns", Widget: FieldWidgetColumns, Doc: "Distinct static column identifiers. Required for column operations."},
			{Name: "params.values", Kind: FieldKindObject, Label: "Values", Widget: FieldWidgetValues, Doc: "Column-to-string values. Missing keys remain absent; empty strings remain present. Managed secrets are prohibited."},
			{Name: "params.expected", Kind: FieldKindObject, Label: "Only when the row has", Widget: FieldWidgetValues, Doc: "Expected column values for supported conditional operations."},
			{Name: "params.expected_version", Kind: FieldKindString, Label: "Expected version", Widget: FieldWidgetOutputRef, OutputKind: "record_version", Doc: "Required native update version as a positive decimal string or template."},
			{Name: "params.delta", Kind: FieldKindInt64, Label: "Add", Widget: FieldWidgetInt64, Placeholder: "1", Doc: "Signed 64-bit increment, as a number or a decimal string; overflow and nonnumeric cells fail."},
			{Name: "params.lease_ref", Kind: FieldKindString, Label: "Lease", Widget: FieldWidgetOutputRef, OutputKind: "lease", Doc: "Private lease reference created by an earlier lease action's result_prefix. Tokens are never exposed as variables."},
			{Name: "params.lease_ms", Kind: FieldKindInt, Maximum: 300000, Label: "Lease for", Widget: FieldWidgetDurationMS, Advanced: true, Doc: "Lease/renew duration, at most 300000 ms; zero uses the admitted binding duration."},
			{Name: "params.wait_ms", Kind: FieldKindInt, Maximum: 60000, Label: "Wait for a row up to", Widget: FieldWidgetDurationMS, Advanced: true, Doc: "Bounded empty-table wait for read/next/pop/lease, at most 60000 ms."},
			{Name: "params.limit", Kind: FieldKindInt, Maximum: 1000, Label: "Rows to return", Advanced: true, Doc: "Maximum range rows or column cells returned, at most 1000."},
			{Name: "params.cursor", Kind: FieldKindString, Label: "Shared cursor", Widget: FieldWidgetText, Advanced: true, Doc: "Named shared sequential cursor for next; omitted uses the table-wide cursor."},
			{Name: "params.on_empty", Kind: FieldKindEnum, SchemaEnum: true, Enum: []string{"error", "skip"}, Label: "When no row is found", Widget: FieldWidgetEnum, EnumLabels: liveTableEmptyLabels, Doc: "Empty/missing result policy; defaults to error. Outputs are cleared in either case."},
			{Name: "params.on_condition", Kind: FieldKindEnum, SchemaEnum: true, Enum: []string{"error", "skip"}, Label: "When the row does not match", Widget: FieldWidgetEnum, EnumLabels: liveTableEmptyLabels, Doc: "Failed comparison policy; defaults to error."},
		},
		Discriminator: "params.operation",
		Variants:      liveTableVariants(),
		Validate:      validateLiveTableAction,
	}}
}

// liveTableVariants describes each operation as parseLiveTableAction enforces
// it (TestLiveTableVariantsMatchTheValidator checks every one).
func liveTableVariants() []ActionVariant {
	common := []string{"params.alias", "params.operation", "params.result_prefix"}
	allowed := func(fields ...string) []string {
		return append(append([]string(nil), common...), fields...)
	}
	byIDOrPosition := [][]string{{"params.record_id"}, {"params.position"}}
	return []ActionVariant{
		{
			Value: "read", Label: "Read a row by ID or position", Group: "Read",
			Summary: "Reads one row, or a range from a position, without changing the table.",
			Choices: byIDOrPosition,
			Allowed: allowed("params.record_id", "params.position", "params.columns", "params.limit", "params.wait_ms", "params.on_empty"),
			// The first row, read without taking it, is the safe default.
			Defaults: map[string]any{"position": 1},
		},
		{
			Value: "next", Label: "Read the next row", Group: "Read",
			Summary: "Reads the row after the shared cursor and moves the cursor on, leaving the row in the table.",
			Allowed: allowed("params.columns", "params.cursor", "params.wait_ms", "params.on_empty"),
		},
		{
			Value: "read_column", Label: "Read a column", Group: "Read",
			Summary: "Reads the cells of one column.",
			Required: []string{"params.columns"}, Counts: map[string]int{"params.columns": 1},
			Allowed:   allowed("params.columns", "params.position", "params.limit", "params.on_empty"),
			TableKind: liveTableKindSharedColumns,
		},
		{
			Value: "push", Label: "Add a row", Group: "Change", Mutates: true,
			Summary:  "Adds a row with the values given.",
			Required: []string{"params.values"},
			Allowed:  allowed("params.values"),
		},
		{
			Value: "update", Label: "Update a row", Group: "Change", Mutates: true,
			Summary: "Changes a row's values, if it still has the version (or, by position, the cells) you expect.",
			Required: []string{"params.values"},
			Choices:  [][]string{{"params.record_id", "params.expected_version"}, {"params.position"}},
			Allowed:  allowed("params.record_id", "params.position", "params.values", "params.expected", "params.expected_version", "params.on_condition"),
		},
		{
			Value: "increment", Label: "Add to a number", Group: "Change", Mutates: true,
			Summary:  "Adds to an integer cell, atomically.",
			Required: []string{"params.columns"}, Counts: map[string]int{"params.columns": 1},
			Choices:  byIDOrPosition,
			Allowed:  allowed("params.record_id", "params.position", "params.columns", "params.delta", "params.expected", "params.on_condition"),
			Defaults: map[string]any{"delta": "1"},
		},
		{
			Value: "clear", Label: "Clear cells in a row", Group: "Change", Mutates: true,
			Summary:  "Removes the given cells (or all of them) from the row at a position.",
			Required: []string{"params.position"},
			Allowed:  allowed("params.position", "params.columns", "params.expected", "params.on_condition"),
		},
		{
			Value: "pop", Label: "Remove the next row", Group: "Take work", Mutates: true,
			Summary: "Takes the next row out of the table; no other VUser gets it.",
			Allowed: allowed("params.columns", "params.wait_ms", "params.on_empty"),
		},
		{
			Value: "lease", Label: "Lease a row", Group: "Take work", Mutates: true,
			Summary:   "Takes the next row for a while; acknowledge it when done, or it is delivered again.",
			Required:  []string{"params.result_prefix"},
			Allowed:   allowed("params.lease_ms", "params.wait_ms", "params.on_empty"),
			TableKind: liveTableKindWorkQueue,
		},
		{
			Value: "renew", Label: "Renew a lease", Group: "Take work", Mutates: true,
			Summary:   "Extends a lease taken by an earlier step.",
			Required:  []string{"params.lease_ref"},
			Allowed:   allowed("params.lease_ref", "params.lease_ms"),
			TableKind: liveTableKindWorkQueue,
		},
		{
			Value: "ack", Label: "Acknowledge a lease", Group: "Take work", Mutates: true,
			Summary:   "Finishes the leased row's work; the row leaves the table.",
			Required:  []string{"params.lease_ref"},
			Allowed:   allowed("params.lease_ref"),
			TableKind: liveTableKindWorkQueue,
		},
		{
			Value: "release", Label: "Release a lease", Group: "Take work", Mutates: true,
			Summary:   "Gives the leased row back for another VUser.",
			Required:  []string{"params.lease_ref"},
			Allowed:   allowed("params.lease_ref"),
			TableKind: liveTableKindWorkQueue,
		},
		{
			Value: "create_column", Label: "Add a column", Group: "Columns", Mutates: true,
			Summary:  "Adds an empty column to the table.",
			Required: []string{"params.columns"}, Counts: map[string]int{"params.columns": 1},
			Allowed:   allowed("params.columns"),
			TableKind: liveTableKindSharedColumns,
		},
		{
			Value: "rotate", Label: "Rotate columns", Group: "Columns", Mutates: true,
			Summary:   "Reads the first row and moves it to the end of its columns, so the next VUser reads the next one.",
			Allowed:   allowed("params.columns", "params.on_empty"),
			TableKind: liveTableKindSharedColumns,
		},
	}
}
