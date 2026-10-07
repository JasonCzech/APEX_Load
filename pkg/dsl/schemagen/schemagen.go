// Package schemagen derives the published DSL artifacts — the JSON Schema
// (docs/dsl.schema.json) and the action reference (docs/DSL_ACTION_REFERENCE.md)
// — from the action catalog in pkg/dsl and the typed config structs. The
// catalog is the single source of truth: regenerate with `go generate ./pkg/dsl`
// (or `go run ./cmd/dslschema`) after changing it.
package schemagen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const schemaID = "https://github.com/jasonczech/apexload/docs/dsl.schema.json"

// GenerateSchema renders the JSON Schema (draft 2020-12) for a Script
// document. The config sections are reflected from the typed structs; the
// per-action-type contracts are composed from the action catalog as if/then
// clauses on the Action definition.
//
// The schema is deliberately never stricter than the Go validator: required
// markers come from the catalog (not from struct tags), and conditional
// rules the schema cannot express (one-of-several fields, config fallbacks)
// stay documentation-only here and enforced by dsl.ValidateScriptIssues.
func GenerateSchema() ([]byte, error) {
	reflector := &jsonschema.Reflector{}
	reflected := reflector.Reflect(&dsl.Script{})
	reflected.ID = schemaID
	reflected.Title = "APEX Load Script DSL"
	reflected.Description = "JSON DSL for APEX Load test scripts. Generated from the action catalog in pkg/dsl — do not edit by hand."

	raw, err := json.Marshal(reflected)
	if err != nil {
		return nil, fmt.Errorf("marshal reflected schema: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal reflected schema: %w", err)
	}

	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("reflected schema has no $defs")
	}
	// Legacy DTOs still decode saved scripts and source revisions, while the
	// authoring contract exposes only configuration supported by this build.
	if config, ok := defs["ScriptConfig"].(map[string]any); ok {
		if properties, ok := config["properties"].(map[string]any); ok {
			delete(properties, "bruno")
		}
	}
	delete(defs, "BrunoConfig")
	delete(defs, "BrunoRuntimeConfig")
	delete(defs, "BrunoRuntimeLimits")

	// Struct tags without omitempty are not a statement of requiredness —
	// the catalog and the Go validator are. Drop every reflected required
	// list, then restore the requirements validation actually enforces.
	for _, def := range defs {
		if typed, ok := def.(map[string]any); ok {
			delete(typed, "required")
		}
	}
	if script, ok := defs["Script"].(map[string]any); ok {
		script["required"] = []any{"name", "protocol", "actions"}
	}
	action, ok := defs["Action"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("reflected schema has no Action definition")
	}
	action["required"] = []any{"name", "type"}
	action["allOf"] = actionTypeClauses()
	if properties, ok := action["properties"].(map[string]any); ok {
		properties["http_version"] = map[string]any{"type": "string", "enum": []string{"auto", "1.1", "2"}}
		if typeProperty, ok := properties["type"].(map[string]any); ok {
			typeProperty["description"] = "Action type discriminator. Known types are listed in examples; protocols accepting request-shaped actions (http, soap, java_http) also accept other method spellings."
			typeProperty["examples"] = knownActionTypeNames()
		}
	}

	return json.MarshalIndent(doc, "", "  ")
}

// actionTypeClauses builds one if/then clause per catalog spec so the schema
// expresses "these fields are valid/required for this type".
func actionTypeClauses() []any {
	specs := dsl.AllActionSpecs()
	clauses := make([]any, 0, len(specs))
	for _, spec := range specs {
		clauses = append(clauses, map[string]any{
			"if": map[string]any{
				"properties": map[string]any{
					"type": map[string]any{"enum": toAnySlice(specTypeNames(spec))},
				},
				"required": []any{"type"},
			},
			"then": actionThenClause(spec),
		})
	}
	return clauses
}

func actionThenClause(spec dsl.ActionSpec) map[string]any {
	properties := make(map[string]any)
	paramProperties := make(map[string]any)
	var required []any
	var paramsRequired []any

	for _, field := range spec.Fields {
		schema := fieldSchema(field)
		if paramName, ok := strings.CutPrefix(field.Name, "params."); ok {
			paramProperties[paramName] = schema
			if field.Required {
				paramsRequired = append(paramsRequired, paramName)
			}
			continue
		}
		properties[field.Name] = schema
		if field.Required {
			required = append(required, field.Name)
		}
	}
	if len(paramProperties) > 0 {
		params := map[string]any{
			"type":       "object",
			"properties": paramProperties,
		}
		if len(paramsRequired) > 0 {
			params["required"] = paramsRequired
		}
		properties["params"] = params
		if len(paramsRequired) > 0 {
			required = append(required, "params")
		}
	}

	description := spec.Summary
	if spec.ExecutionNote != "" {
		description += " " + spec.ExecutionNote
	}
	then := map[string]any{"description": description}
	if spec.ExecutionPosture != "" {
		then["x-execution-posture"] = spec.ExecutionPosture
	}
	if len(properties) > 0 {
		then["properties"] = properties
	}
	if len(required) > 0 {
		then["required"] = required
	}
	if len(spec.RequireOneOf) > 0 {
		var groups []any
		for _, group := range spec.RequireOneOf {
			alternatives := make([]any, 0, len(group))
			for _, field := range group {
				alternatives = append(alternatives, requireFieldClause(field))
			}
			groups = append(groups, map[string]any{"anyOf": alternatives})
		}
		then["allOf"] = groups
	}
	return then
}

// requireFieldClause renders "this field must be present" for a top-level
// field or a params.* path.
func requireFieldClause(field string) map[string]any {
	if paramName, ok := strings.CutPrefix(field, "params."); ok {
		return map[string]any{
			"properties": map[string]any{
				"params": map[string]any{"required": []any{paramName}},
			},
			"required": []any{"params"},
		}
	}
	return map[string]any{"required": []any{field}}
}

func fieldSchema(field dsl.FieldSpec) map[string]any {
	schema := make(map[string]any)
	switch field.Kind {
	case dsl.FieldKindString:
		schema["type"] = "string"
	case dsl.FieldKindInt:
		schema["type"] = "integer"
	case dsl.FieldKindFloat:
		schema["type"] = "number"
	case dsl.FieldKindBool:
		schema["type"] = "boolean"
	case dsl.FieldKindEnum:
		schema["type"] = "string"
		if field.SchemaEnum {
			accepted := append(append([]string(nil), field.Enum...), field.EnumAliases...)
			if field.EnumCaseFold || field.EnumTrimSpace {
				if field.EnumAllowBlank {
					accepted = append(accepted, "")
				}
				pattern := normalizedEnumPattern(accepted, field.EnumCaseFold, field.EnumTrimSpace)
				if field.EnumAllowEmpty && !field.EnumAllowBlank {
					schema["anyOf"] = []any{map[string]any{"const": ""}, map[string]any{"pattern": pattern}}
				} else {
					schema["pattern"] = pattern
				}
			} else {
				if field.EnumAllowEmpty || field.EnumAllowBlank {
					accepted = append(accepted, "")
				}
				schema["enum"] = toAnySlice(accepted)
			}
		} else {
			// Open catalog choices remain examples because some protocol runtimes
			// trim, fold case, or accept compatibility aliases outside the editor list.
			schema["examples"] = toAnySlice(field.Enum)
		}
	case dsl.FieldKindObject:
		schema["type"] = "object"
	case dsl.FieldKindArray:
		schema["type"] = "array"
	case dsl.FieldKindAny:
		// No type constraint.
	}
	if field.Doc != "" {
		schema["description"] = field.Doc
	}
	if field.Maximum > 0 {
		schema["maximum"] = field.Maximum
	}
	return schema
}

func normalizedEnumPattern(values []string, caseFold, trimSpace bool) string {
	var pattern strings.Builder
	pattern.WriteByte('^')
	if trimSpace {
		pattern.WriteString(`\s*`)
	}
	pattern.WriteString("(?:")
	for valueIndex, value := range values {
		if valueIndex > 0 {
			pattern.WriteByte('|')
		}
		for index := 0; index < len(value); index++ {
			char := value[index]
			if caseFold && char >= 'a' && char <= 'z' {
				pattern.WriteByte('[')
				pattern.WriteByte(char)
				pattern.WriteByte(char - ('a' - 'A'))
				pattern.WriteByte(']')
				continue
			}
			if caseFold && char >= 'A' && char <= 'Z' {
				pattern.WriteByte('[')
				pattern.WriteByte(char + ('a' - 'A'))
				pattern.WriteByte(char)
				pattern.WriteByte(']')
				continue
			}
			if strings.ContainsRune(`\.^$|?*+()[]{}`, rune(char)) {
				pattern.WriteByte('\\')
			}
			pattern.WriteByte(char)
		}
	}
	pattern.WriteByte(')')
	if trimSpace {
		pattern.WriteString(`\s*`)
	}
	pattern.WriteByte('$')
	return pattern.String()
}

// specTypeNames returns every accepted spelling for a spec: canonical type,
// aliases, and the legacy "loadrunner_*" form for engine source_* types.
func specTypeNames(spec dsl.ActionSpec) []string {
	names := append([]string{spec.Type}, spec.Aliases...)
	if spec.EngineAction() {
		if suffix, ok := strings.CutPrefix(spec.Type, "source_"); ok {
			names = append(names, "loadrunner_"+suffix)
		}
	}
	return names
}

func knownActionTypeNames() []any {
	var names []string
	for _, spec := range dsl.AllActionSpecs() {
		names = append(names, specTypeNames(spec)...)
	}
	sort.Strings(names)
	return toAnySlice(names)
}

func toAnySlice(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

// GenerateReference renders the action reference markdown, grouped by
// protocol family with engine actions first.
func GenerateReference() []byte {
	var b strings.Builder
	b.WriteString("# DSL Action Reference\n\n")
	b.WriteString("Generated from the action catalog in `pkg/dsl` — do not edit by hand.\n")
	b.WriteString("Regenerate with `go generate ./pkg/dsl` (or `go run ./cmd/dslschema`).\n\n")
	b.WriteString("Template variables (`{{run.id}}`, `{{env.KEY}}`, `{{data.source.col}}`, `{{var.name}}`, `{{fn.uuid()}}`)\n")
	b.WriteString("are expanded in string fields at runtime. Legacy `loadrunner_*` action types resolve\n")
	b.WriteString("to their `source_*` equivalents.\n\n")

	b.WriteString("## Common fields (all action types)\n\n")
	writeFieldTable(&b, dsl.CommonActionFields())

	var engine []dsl.ActionSpec
	byFamily := make(map[string][]dsl.ActionSpec)
	for _, spec := range dsl.AllActionSpecs() {
		if spec.EngineAction() {
			engine = append(engine, spec)
			continue
		}
		for _, family := range spec.Protocols {
			byFamily[family] = append(byFamily[family], spec)
		}
	}

	b.WriteString("## Engine actions (any protocol)\n\n")
	b.WriteString("Valid in every script, including JMS, Kafka, and Event Hubs; these actions execute in the common engine lifecycle rather than protocol dispatch.\n\n")
	for _, spec := range engine {
		writeActionSpec(&b, spec)
	}

	families := make([]string, 0, len(byFamily))
	for family := range byFamily {
		families = append(families, family)
	}
	sort.Strings(families)
	for _, family := range families {
		fmt.Fprintf(&b, "## Protocol: %s\n\n", family)
		for _, spec := range byFamily[family] {
			writeActionSpec(&b, spec)
		}
	}
	return []byte(b.String())
}

func writeActionSpec(b *strings.Builder, spec dsl.ActionSpec) {
	fmt.Fprintf(b, "### `%s`\n\n", spec.Type)
	fmt.Fprintf(b, "%s\n\n", spec.Summary)
	if spec.ExecutionPosture != "" {
		fmt.Fprintf(b, "Execution posture: `%s` — %s\n\n", spec.ExecutionPosture, spec.ExecutionNote)
	}
	if len(spec.Aliases) > 0 {
		fmt.Fprintf(b, "Aliases: `%s`\n\n", strings.Join(spec.Aliases, "`, `"))
	}
	if len(spec.Protocols) > 1 && !spec.EngineAction() {
		fmt.Fprintf(b, "Protocols: %s\n\n", strings.Join(spec.Protocols, ", "))
	}
	for _, group := range spec.RequireOneOf {
		fmt.Fprintf(b, "Requires one of: `%s`\n\n", strings.Join(group, "`, `"))
	}
	if len(spec.Fields) > 0 {
		writeFieldTable(b, spec.Fields)
	}
}

func writeFieldTable(b *strings.Builder, fields []dsl.FieldSpec) {
	b.WriteString("| Field | Kind | Required | Description |\n")
	b.WriteString("|-------|------|----------|-------------|\n")
	for _, field := range fields {
		kind := string(field.Kind)
		if field.Kind == dsl.FieldKindEnum {
			kind = "enum: " + strings.Join(field.Enum, "\\|")
		}
		required := ""
		if field.Required {
			required = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", field.Name, kind, required, field.Doc)
	}
	b.WriteString("\n")
}
