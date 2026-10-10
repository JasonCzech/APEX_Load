//go:generate go run ../../cmd/dslschema -schema ../../docs/dsl.schema.json -reference ../../docs/DSL_ACTION_REFERENCE.md

package dsl

import (
	"sort"
	"strings"
	"sync"
)

// FieldKind classifies a declared action field for schema generation and
// editor tooling. It mirrors JSON value kinds plus "enum" for curated string
// choices and "any" for fields whose type depends on the action. Enum choices
// are editor suggestions unless FieldSpec.SchemaEnum marks a validator-enforced
// closed set; protocol validators remain authoritative for compatibility aliases.
type FieldKind string

const (
	FieldKindString FieldKind = "string"
	FieldKindInt    FieldKind = "int"
	FieldKindFloat  FieldKind = "float"
	FieldKindBool   FieldKind = "bool"
	FieldKindEnum   FieldKind = "enum"
	FieldKindObject FieldKind = "object"
	FieldKindArray  FieldKind = "array"
	FieldKindAny    FieldKind = "any"
)

// FieldSpec declares one field an action type understands. Name is either a
// top-level Action JSON field ("selector") or a dotted params path
// ("params.max_messages"). Required marks fields the action cannot run
// without; one-of-several constraints are expressed in Doc and enforced by
// the owning ActionSpec.Validate function.
type FieldSpec struct {
	Name           string    `json:"name"`
	Required       bool      `json:"required,omitempty"`
	Kind           FieldKind `json:"kind"`
	Enum           []string  `json:"enum,omitempty"`
	EnumAliases    []string  `json:"enum_aliases,omitempty"`
	SchemaEnum     bool      `json:"schema_enum,omitempty"`
	EnumCaseFold   bool      `json:"enum_case_fold,omitempty"`
	EnumTrimSpace  bool      `json:"enum_trim_space,omitempty"`
	EnumAllowEmpty bool      `json:"enum_allow_empty,omitempty"`
	EnumAllowBlank bool      `json:"enum_allow_blank,omitempty"`
	Maximum        int64     `json:"maximum,omitempty"`
	Doc            string    `json:"doc,omitempty"`
}

// ActionSpec is the declarative contract for one DSL action type: which
// protocols may use it, which fields it understands, and the residual
// cross-field validation rules that field metadata alone cannot express.
// The catalog built from these specs is the single source of truth consumed
// by the validator, the generated JSON Schema, the action reference doc, and
// the dashboard editor.
type ActionSpec struct {
	Type      string   `json:"type"`
	Aliases   []string `json:"aliases,omitempty"`
	Protocols []string `json:"protocols"`
	Summary   string   `json:"summary"`
	// ExecutionPosture describes whether the action can execute in the bundled
	// runtime or needs an external runner. ExecutionNote carries the operator-
	// facing qualification rendered by every generated catalog surface.
	ExecutionPosture string      `json:"execution_posture,omitempty"`
	ExecutionNote    string      `json:"execution_note,omitempty"`
	Fields           []FieldSpec `json:"fields,omitempty"`
	// RequireOneOf lists alternative-field groups: for each group, at least
	// one of the named fields (top-level or params.* path) must be set.
	// Groups mirror the one-of rules the Validate funcs enforce, expressed
	// as data so the generated schema (anyOf) and editor can consume them.
	RequireOneOf [][]string `json:"require_one_of,omitempty"`
	// Validate holds residual per-type rules. Issues are labelled with the
	// "actions[<index>]" prefix convention used across validate.go.
	Validate func(index int, action Action) []string `json:"-"`
	// validateInScript holds residual rules that need the owning script's
	// protocol configuration. Keeping this callback on the same catalog entry
	// as membership and field metadata prevents messaging semantics from
	// becoming a second, independently maintained action catalog.
	validateInScript func(path string, action Action, script *Script) []string
}

// EngineAction reports whether the spec describes an engine-level action
// that is valid inside any protocol script (Protocols contains "*").
func (s ActionSpec) EngineAction() bool {
	for _, protocol := range s.Protocols {
		if protocol == "*" {
			return true
		}
	}
	return false
}

// ProtocolFamily maps a script protocol string to the catalog family used
// for action membership, collapsing provider-suffixed protocols (e.g.
// "jms-solace", "jms-ibmmq") onto their family ("jms").
func ProtocolFamily(protocol string) string {
	trimmed := strings.TrimSpace(protocol)
	if trimmed == "http/1.1" || trimmed == "http/2" || trimmed == "http/3" {
		return "http"
	}
	if strings.HasPrefix(trimmed, "jms-") {
		return "jms"
	}
	return trimmed
}

var (
	catalogOnce    sync.Once
	catalogByType  map[string]*ActionSpec
	catalogOrdered []*ActionSpec
)

// actionCatalogSeeds assembles every per-protocol spec list. Seed files live
// one per protocol (catalog_browser.go, catalog_grpc.go, ...) mirroring the
// protocol package layout.
func actionCatalogSeeds() []ActionSpec {
	var seeds []ActionSpec
	seeds = append(seeds, engineActionSpecs()...)
	seeds = append(seeds, httpActionSpecs()...)
	seeds = append(seeds, browserActionSpecs()...)
	seeds = append(seeds, websocketActionSpecs()...)
	seeds = append(seeds, grpcActionSpecs()...)
	seeds = append(seeds, databaseActionSpecs()...)
	seeds = append(seeds, networkActionSpecs()...)
	seeds = append(seeds, sapGUIActionSpecs()...)
	seeds = append(seeds, javaVuserActionSpecs()...)
	seeds = append(seeds, jmsActionSpecs()...)
	seeds = append(seeds, kafkaActionSpecs()...)
	seeds = append(seeds, eventHubsActionSpecs()...)
	return seeds
}

func buildActionCatalog() {
	seeds := actionCatalogSeeds()
	catalogByType = make(map[string]*ActionSpec, len(seeds)*2)
	catalogOrdered = make([]*ActionSpec, 0, len(seeds))
	for i := range seeds {
		spec := &seeds[i]
		catalogOrdered = append(catalogOrdered, spec)
		catalogByType[spec.Type] = spec
		for _, alias := range spec.Aliases {
			catalogByType[alias] = spec
		}
	}
}

func actionCatalogIndex() map[string]*ActionSpec {
	catalogOnce.Do(buildActionCatalog)
	return catalogByType
}

func actionCatalogList() []*ActionSpec {
	catalogOnce.Do(buildActionCatalog)
	return catalogOrdered
}

// SpecForActionType resolves an action type (or registered alias) to its
// catalog spec. Legacy "loadrunner_*" engine types are canonicalized first.
// Lookup is exact-match; protocols that accept case-insensitive types
// (database, network) normalize before calling.
func SpecForActionType(actionType string) (ActionSpec, bool) {
	spec, ok := actionCatalogIndex()[CanonicalActionType(actionType)]
	if !ok {
		return ActionSpec{}, false
	}
	return *spec, true
}

// protocolActionSpec resolves an action type within one protocol family,
// matching only specs that list the family explicitly. Engine ("*") specs are
// validated by the common lifecycle path before protocol-specific dispatch.
func protocolActionSpec(family, actionType string) (*ActionSpec, bool) {
	spec, ok := actionCatalogIndex()[actionType]
	if !ok {
		return nil, false
	}
	for _, protocol := range spec.Protocols {
		if protocol == family {
			return spec, true
		}
	}
	return nil, false
}

// validateCatalogActionInScript dispatches configuration-dependent residual
// validation through the same ActionSpec that establishes protocol membership.
// The boolean distinguishes an action without a catalog entry/callback from a
// valid action whose callback reports no issues.
func validateCatalogActionInScript(family, path string, action Action, script *Script) ([]string, bool) {
	spec, ok := protocolActionSpec(family, action.Type)
	if !ok || spec.validateInScript == nil {
		return nil, false
	}
	return spec.validateInScript(path, action, script), true
}

// AllActionSpecs returns every catalog spec sorted by canonical type.
func AllActionSpecs() []ActionSpec {
	list := actionCatalogList()
	specs := make([]ActionSpec, 0, len(list))
	for _, spec := range list {
		specs = append(specs, *spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Type < specs[j].Type })
	return specs
}

// ActionSpecsForProtocol returns the specs usable in scripts of the given
// protocol, sorted by canonical type. Engine actions are valid for every
// protocol family and execute through the common engine lifecycle path.
func ActionSpecsForProtocol(protocol string) []ActionSpec {
	family := ProtocolFamily(protocol)
	var specs []ActionSpec
	for _, spec := range actionCatalogList() {
		if spec.EngineAction() {
			specs = append(specs, *spec)
			continue
		}
		for _, candidate := range spec.Protocols {
			if candidate == family {
				specs = append(specs, *spec)
				break
			}
		}
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Type < specs[j].Type })
	return specs
}

// CatalogProtocolFamilies returns every protocol family that owns at least
// one non-engine action spec, sorted alphabetically.
func CatalogProtocolFamilies() []string {
	seen := make(map[string]struct{})
	for _, spec := range actionCatalogList() {
		for _, protocol := range spec.Protocols {
			if protocol == "*" {
				continue
			}
			seen[protocol] = struct{}{}
		}
	}
	families := make([]string, 0, len(seen))
	for family := range seen {
		families = append(families, family)
	}
	sort.Strings(families)
	return families
}

// CommonActionFields lists the fields every action understands regardless of
// type. Per-type specs declare only their type-specific fields on top of
// these.
func CommonActionFields() []FieldSpec {
	return []FieldSpec{
		{Name: "pacing_ids", Kind: FieldKindArray, Doc: "Source sampler schedule identities; requires JMeter messaging compatibility. Currently execution-gated pending runtime conformance."},
		{Name: "name", Required: true, Kind: FieldKindString, Doc: "Unique step name; used in transaction reporting and issue labels."},
		{Name: "type", Required: true, Kind: FieldKindString, Doc: "Action type discriminator; see the per-protocol action catalog."},
		{Name: "think_time", Kind: FieldKindObject, Doc: "Per-action think-time override (type, mean_ms, ...)."},
		{Name: "think_time_after", Kind: FieldKindBool, Doc: "Apply think time after this action completes."},
		{Name: "extractors", Kind: FieldKindArray, Doc: "Response extractors: {name, source, type, expression, on_miss, default_value, mode, save_offset, save_length, convert, scope, dfes}. HTTP source: \"body\" (default), \"header\" (named header, or regex/boundary over the flattened headers), \"all\" (regex, regex_count, regex_ordinal or boundary over the flattened headers followed by the body, LoadRunner's default search scope), \"cookies\" (the same text types over the Set-Cookie header lines, LoadRunner's Scope=Cookies), \"url\" (the final request URL after redirects) or \"status\" (the final status code); url and status also accept type \"\" to save the whole value. HTTP scope widens or filters the responses searched (LoadRunner registrations search every response of a step): {redirects: include|exclude (each redirect hop's status and headers), resources: include|exclude (fetched resource responses), request_url and content_type (wildcard filters; * spans any text, ? one character, every other character is literal, case-insensitive; no wildcard = substring), header_names (limit header/all/cookies searches)}; responses are tried hops first, then the final response, then resources in arrival order, and the first match wins. Without scope only the final response is searched. HTTP dfes (body and all sources) transforms each searched response body in order before the expression runs (LoadRunner data-format extensions): base64, gzip, json_to_xml, xml_to_json, url_decode, url_encode, html_unescape, html_escape; a body a transform cannot read counts as a miss. on_miss: \"fail\" fails the request when nothing matches, \"empty\" assigns an empty string; \"default\" assigns default_value (including an explicitly empty default); mode \"all\" saves every match as name_1..name_N plus name_count; save_offset/save_length trim each value (bytes); convert post-processes it (url_encode | url_decode | html_decode | html_url_encode). The behavior fields are honored by the HTTP protocol family."},
		{Name: "assertions", Kind: FieldKindArray, Doc: "Response assertions: {type, expected, value, max, case_insensitive, source}. HTTP supports status/time/body checks; text_contains, text_not_contains, text_matches and text_not_matches (value is literal text or an RE2 pattern; a parameter inside a pattern is matched literally; case_insensitive applies) over source \"body\" (default), \"headers\" (the raw header block, one \"Name: value\" CRLF line per value) or \"all\" (headers, a blank line, then the body), as LoadRunner's web_reg_find searches; plus header_contains (value is a case-insensitive header name; expected is a case-sensitive substring) and body_regex_capture_equals (value is a Go/RE2 pattern up to 4 KiB; expected equals the first capture group, or the full match when no group exists). aura_actions_success structurally verifies that the actual outgoing Aura request and response contain unique matching action IDs and SUCCESS states; malformed, incomplete, unknown or over-limit envelopes fail with bounded value-free evidence, and framework error responses (*/…/*ERROR*/ or an exception event) fail as invalid_session, client_out_of_sync or framework_error. Its optional expected, {\"recorded\": [{\"ordinal\", \"state\"}]}, lists actions that failed in the recording: a repeat failure is labelled \"also failed in the recording\", and config.http.aura_recorded_failures \"accept_recorded\" accepts one that ends in the recorded state."},
		{Name: "metadata", Kind: FieldKindObject, Doc: "Free-form metadata; importers preserve source details here."},
		{Name: "timeout_ms", Kind: FieldKindInt, Doc: "Per-action timeout override in milliseconds."},
		{Name: "max_redirects", Kind: FieldKindInt, Doc: "Per-action redirect-policy override (-1 disables)."},
		{Name: "auth", Kind: FieldKindObject, Doc: "Per-action auth override: {type, username, password, token, domain}. HTTP types: basic (sent preemptively), bearer, digest, and the challenge-driven ntlm, negotiate (Negotiate carrying NTLM; domain prefixes the user) and negotiate_any (answers whichever Basic, Digest, NTLM or Negotiate challenge the server sends, as LoadRunner's web_set_user does; write DOMAIN\\user for a domain). kerberos is not supported."},
	}
}
