package dsl

func liveTableActionSpecs() []ActionSpec {
	return []ActionSpec{{
		Type: "live_table", Protocols: []string{"*"},
		Summary:          "Performs an atomic operation on an explicitly bound run-scoped live table. Existing variables remain snapshots until read again.",
		ExecutionPosture: "supported",
		ExecutionNote:    "Requires an admitted live-table binding. Unmanaged runs and replay fail closed without an explicit isolated seed. Leases permit redelivery; they do not make external work exactly once.",
		Fields: []FieldSpec{
			{Name: "params.alias", Kind: FieldKindString, Required: true, Doc: "Static alias of a trusted run binding; never a remote URL or table credential."},
			{Name: "params.operation", Kind: FieldKindEnum, Required: true, SchemaEnum: true, Enum: []string{"read", "next", "push", "pop", "lease", "renew", "ack", "release", "update", "increment", "clear", "rotate", "create_column", "read_column"}},
			{Name: "params.result_prefix", Kind: FieldKindString, Doc: "Static var namespace prefix. Row fields appear under <prefix>.values.<column>; metadata includes status, revision, record_id and record_version. Bounded ranges use JSON rows/cells/columns. Missing results clear prior outputs. For lease, also identifies the private lease reference."},
			{Name: "params.record_id", Kind: FieldKindString, Doc: "Stable native record ID; templates allowed."},
			{Name: "params.position", Kind: FieldKindInt, Maximum: 1000000, Doc: "One-based position for operations supporting positional access."},
			{Name: "params.columns", Kind: FieldKindArray, Doc: "Distinct static column identifiers. Required for column operations."},
			{Name: "params.values", Kind: FieldKindObject, Doc: "Column-to-string values. Missing keys remain absent; empty strings remain present. Managed secrets are prohibited."},
			{Name: "params.expected", Kind: FieldKindObject, Doc: "Expected column values for supported conditional operations."},
			{Name: "params.expected_version", Kind: FieldKindString, Doc: "Required native update version as a positive decimal string or template."},
			{Name: "params.delta", Kind: FieldKindInt, Doc: "Signed 64-bit increment; overflow and nonnumeric cells fail."},
			{Name: "params.lease_ref", Kind: FieldKindString, Doc: "Private lease reference created by an earlier lease action's result_prefix. Tokens are never exposed as variables."},
			{Name: "params.lease_ms", Kind: FieldKindInt, Maximum: 300000, Doc: "Lease/renew duration, at most 300000 ms; zero uses the admitted binding duration."},
			{Name: "params.wait_ms", Kind: FieldKindInt, Maximum: 60000, Doc: "Bounded empty-table wait for read/next/pop/lease, at most 60000 ms."},
			{Name: "params.limit", Kind: FieldKindInt, Maximum: 1000, Doc: "Maximum range rows or column cells returned, at most 1000."},
			{Name: "params.cursor", Kind: FieldKindString, Doc: "Named shared sequential cursor for next; omitted uses the table-wide cursor."},
			{Name: "params.on_empty", Kind: FieldKindEnum, SchemaEnum: true, Enum: []string{"error", "skip"}, Doc: "Empty/missing result policy; defaults to error. Outputs are cleared in either case."},
			{Name: "params.on_condition", Kind: FieldKindEnum, SchemaEnum: true, Enum: []string{"error", "skip"}, Doc: "Failed comparison policy; defaults to error."},
		},
		Validate: validateLiveTableAction,
	}}
}
