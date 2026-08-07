package enterprisearchive

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// itemdata_form_body_test.go replaces substring probing of converted ITEMDATA
// bodies with structural assertions.
//
// A converted web_submit_data/web_submit_form body is an ordered sequence of
// name/value pairs. `strings.Contains(action.Body, "username")` cannot see field
// ORDER, cannot see a DROPPED field, cannot see a DUPLICATED field, and cannot see
// a value whose encoding merged two fields into one — all of which are the ways an
// ITEMDATA translation actually breaks. Everything here parses the body back into
// fields and compares them as data.

// formField is one decoded name/value pair from an
// application/x-www-form-urlencoded body, in emission order.
type formField struct {
	Name  string
	Value string
}

func (f formField) String() string { return fmt.Sprintf("%s=%s", f.Name, f.Value) }

// parseFormBodyFields parses a converted form body into its ordered fields.
//
// It deliberately does NOT use url.ParseQuery: url.Values is a map, so it loses
// field order outright and buries duplicate names in a slice detached from their
// position. Order and position are exactly what the substring probes could not
// check, so the parse keeps the raw sequence.
//
// Names and values are returned exactly as emitted (still percent-encoded). That
// pins the encoding too: the converter escapes "&" and "=" inside values, and a
// regression that stopped escaping would split one field into several here.
func parseFormBodyFields(body string) []formField {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	parts := strings.Split(body, "&")
	fields := make([]formField, 0, len(parts))
	for _, part := range parts {
		name, value, _ := strings.Cut(part, "=")
		fields = append(fields, formField{Name: name, Value: value})
	}
	return fields
}

// decodedFormValue percent-decodes an emitted value for readability in assertions
// that care about the source text rather than the wire encoding.
func decodedFormValue(t *testing.T, value string) string {
	t.Helper()
	decoded, err := url.QueryUnescape(value)
	if err != nil {
		t.Fatalf("value %q is not valid percent-encoding: %v", value, err)
	}
	return decoded
}

func formatFormFields(fields []formField) string {
	rendered := make([]string, 0, len(fields))
	for _, field := range fields {
		rendered = append(rendered, field.String())
	}
	return "[" + strings.Join(rendered, " ") + "]"
}

// assertFormBodyFields compares the whole ordered field list, so a dropped,
// duplicated, reordered or merged field fails.
func assertFormBodyFields(t *testing.T, body string, want []formField) {
	t.Helper()
	got := parseFormBodyFields(body)
	if len(got) != len(want) {
		t.Fatalf("form body %q parsed to %d fields %s, want %d %s",
			body, len(got), formatFormFields(got), len(want), formatFormFields(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("form body %q field[%d] = %q, want %q (full: %s want %s)",
				body, index, got[index], want[index], formatFormFields(got), formatFormFields(want))
		}
	}
}

// countLiveENDITEMBlocks counts the ITEMDATA rows a VuGen source declares, ignoring
// rows inside // and /* */ comments — the converter must drop exactly those.
//
// It tracks string literals, because "https://host" is not a comment and a naive
// scanner would silently under-count exactly the rows this check exists to protect.
func countLiveENDITEMBlocks(source string) int {
	var live strings.Builder
	inBlockComment := false
	inLineComment := false
	inString := false
	escaped := false
	for index := 0; index < len(source); index++ {
		char := source[index]
		next := byte(0)
		if index+1 < len(source) {
			next = source[index+1]
		}
		switch {
		case inBlockComment:
			if char == '*' && next == '/' {
				inBlockComment = false
				index++
			}
		case inLineComment:
			if char == '\n' {
				inLineComment = false
				live.WriteByte(char)
			}
		case inString:
			live.WriteByte(char)
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
		case char == '"':
			inString = true
			live.WriteByte(char)
		case char == '/' && next == '*':
			inBlockComment = true
			index++
		case char == '/' && next == '/':
			inLineComment = true
			index++
		default:
			live.WriteByte(char)
		}
	}
	return strings.Count(live.String(), "ENDITEM")
}

func convertFormBodyFixture(t *testing.T, body string) *ConvertResult {
	t.Helper()
	source := "#include \"web_api.h\"\n\nAction()\n{\n" + body + "\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v\nsource:\n%s", err, source)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v\nsource:\n%s", err, source)
	}
	return result
}

// TestITEMDATABodiesAreStructurallyFaithful is the table this file exists for: each
// case declares the exact field sequence the converted body must carry.
func TestITEMDATABodiesAreStructurallyFaithful(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   []formField
	}{
		{
			name: "ordered_fields",
			source: `	web_submit_data("Login",
		"Action=https://example.test/login",
		"Method=POST",
		ITEMDATA,
		"Name=username", "Value=ada", ENDITEM,
		"Name=password", "Value=hunter2", ENDITEM,
		LAST);
`,
			want: []formField{{"username", "ada"}, {"password", "hunter2"}},
		},
		{
			// Order is the whole point: a map-based translation would emit these in
			// hash order and a substring probe would never notice.
			name: "declaration_order_is_not_alphabetical",
			source: `	web_submit_data("Order",
		"Action=https://example.test/order",
		"Method=POST",
		ITEMDATA,
		"Name=zeta", "Value=1", ENDITEM,
		"Name=alpha", "Value=2", ENDITEM,
		"Name=mid", "Value=3", ENDITEM,
		LAST);
`,
			want: []formField{{"zeta", "1"}, {"alpha", "2"}, {"mid", "3"}},
		},
		{
			// Repeated names are a multi-select; collapsing them to one entry (or
			// reordering the values) changes what the server receives.
			name: "repeated_field_names_keep_every_occurrence_in_order",
			source: `	web_submit_data("Filters",
		"Action=https://example.test/search",
		"Method=POST",
		ITEMDATA,
		"Name=color", "Value=red", ENDITEM,
		"Name=color", "Value=blue", ENDITEM,
		"Name=sort", "Value=price", ENDITEM,
		"Name=color", "Value=green", ENDITEM,
		LAST);
`,
			want: []formField{{"color", "red"}, {"color", "blue"}, {"sort", "price"}, {"color", "green"}},
		},
		{
			// An empty value is a present field, not an absent one.
			name: "empty_value_field_survives",
			source: `	web_submit_data("Flags",
		"Action=https://example.test/flags",
		"Method=POST",
		ITEMDATA,
		"Name=before", "Value=1", ENDITEM,
		"Name=flag", "Value=", ENDITEM,
		"Name=after", "Value=2", ENDITEM,
		LAST);
`,
			want: []formField{{"before", "1"}, {"flag", ""}, {"after", "2"}},
		},
		{
			// A raw "&" or "=" inside a value must be escaped, or the field boundary
			// is ambiguous and one field silently becomes three.
			name: "reserved_characters_in_values_are_escaped",
			source: `	web_submit_data("Callback",
		"Action=https://example.test/callback",
		"Method=POST",
		ITEMDATA,
		"Name=next", "Value=https://example.test/done?a=1&b=2", ENDITEM,
		"Name=tail", "Value=end", ENDITEM,
		LAST);
`,
			want: []formField{{"next", "https%3A%2F%2Fexample.test%2Fdone%3Fa%3D1%26b%3D2"}, {"tail", "end"}},
		},
		{
			// Correlated/parameterized values stay as literal template references —
			// escaping the braces would break resolution at replay.
			name: "template_references_are_emitted_literally",
			source: `	web_reg_save_param("Token", "LB=token=", "RB=;", LAST);
	web_url("Home", "URL=https://example.test/home", LAST);
	web_submit_data("Use",
		"Action=https://example.test/use",
		"Method=POST",
		ITEMDATA,
		"Name=token", "Value={Token}", ENDITEM,
		"Name=static", "Value=x", ENDITEM,
		LAST);
`,
			want: []formField{{"token", "{{var.token}}"}, {"static", "x"}},
		},
		{
			// Commented-out ITEMDATA rows are dead source and must not reach the body.
			// The old probe only asked "does the body contain 'disabled'"; this asks
			// for the exact surviving field list.
			name: "commented_rows_are_dropped_without_disturbing_the_rest",
			source: `	web_submit_form("login.jsp_2",
		"Snapshot=t2.inf",
		ITEMDATA,
		"Name=user", "Value=ada", ENDITEM,
		// "Name=disabled", "Value=should_not_import", ENDITEM,
		"Name=realm", "Value=prod", ENDITEM,
		/* "Name=blocked", "Value=nope", ENDITEM, */
		LAST);
`,
			want: []formField{{"user", "ada"}, {"realm", "prod"}},
		},
		{
			name: "submit_form_bodies_are_parsed_like_submit_data",
			source: `	web_url("Login Page", "URL=https://example.test/mi/login.jsp", LAST);
	web_submit_form("login.jsp_2",
		"Snapshot=t2.inf",
		ITEMDATA,
		"Name=user", "Value=ada", ENDITEM,
		"Name=pass", "Value=secret", ENDITEM,
		LAST);
`,
			want: []formField{{"user", "ada"}, {"pass", "secret"}},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := convertFormBodyFixture(t, tc.source)

			var form *struct {
				name string
				body string
			}
			for _, action := range result.Script.Actions {
				if action.Body == "" {
					continue
				}
				form = &struct {
					name string
					body string
				}{action.Name, action.Body}
			}
			if form == nil {
				t.Fatalf("no action with a body was emitted: %#v", result.Script.Actions)
			}
			assertFormBodyFields(t, form.body, tc.want)

			// Field conservation, derived from the source rather than restated:
			// exactly the live ENDITEM rows become fields.
			if live := countLiveENDITEMBlocks(tc.source); live != len(parseFormBodyFields(form.body)) {
				t.Fatalf("source declares %d live ITEMDATA rows but the body carries %d fields %s — fields were dropped or invented",
					live, len(parseFormBodyFields(form.body)), formatFormFields(parseFormBodyFields(form.body)))
			}
		})
	}
}

// TestITEMDATABodyRoundTripsThroughQueryDecoding proves the emitted encoding is
// actually consumable: decoding each field yields the source text back, so no value
// was double-escaped or left half-escaped.
func TestITEMDATABodyRoundTripsThroughQueryDecoding(t *testing.T) {
	result := convertFormBodyFixture(t, `	web_submit_data("Callback",
		"Action=https://example.test/callback",
		"Method=POST",
		ITEMDATA,
		"Name=next", "Value=https://example.test/done?a=1&b=2", ENDITEM,
		"Name=note", "Value=hello world", ENDITEM,
		LAST);
`)
	action := result.Script.Actions[0]
	fields := parseFormBodyFields(action.Body)
	if len(fields) != 2 {
		t.Fatalf("fields = %s, want 2", formatFormFields(fields))
	}
	if got := decodedFormValue(t, fields[0].Value); got != "https://example.test/done?a=1&b=2" {
		t.Fatalf("decoded next = %q, want the source URL verbatim", got)
	}
	if got := decodedFormValue(t, fields[1].Value); got != "hello world" {
		t.Fatalf("decoded note = %q, want the source text verbatim", got)
	}
	if got := action.Headers["Content-Type"]; got != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q, want the form encoding the body actually uses", got)
	}
}

// TestCountLiveENDITEMBlocksIgnoresComments guards the conservation helper itself —
// a helper that miscounts would make the conservation check above vacuous.
func TestCountLiveENDITEMBlocksIgnoresComments(t *testing.T) {
	source := `	ITEMDATA,
	"Name=a", "Value=1", ENDITEM,
	// "Name=b", "Value=2", ENDITEM,
	"Name=c", "Value=3", ENDITEM,
	/* "Name=d", "Value=4", ENDITEM, */
	/* multi
	"Name=e", "Value=5", ENDITEM,
	*/
	"Name=f", "Value=6", ENDITEM,
	"Name=g", "Value=https://example.test/x", ENDITEM,
`
	if got := countLiveENDITEMBlocks(source); got != 4 {
		t.Fatalf("live ENDITEM rows = %d, want 4 (a, c, f, g — the // inside a URL literal is not a comment)", got)
	}
}
