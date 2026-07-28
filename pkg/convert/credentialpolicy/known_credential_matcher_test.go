package credentialpolicy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func knownCredentialRedactOptions(match func(string) bool) Options {
	return Options{
		Mode:                   Redact,
		Source:                 SourceRef{Format: "known-credential-test"},
		RequestKey:             "known-credential-test",
		KnownCredentialMatcher: match,
	}
}

func TestProjectJSONKnownCredentialMatcherOnlyProjectsValues(t *testing.T) {
	t.Parallel()
	const query = `query Named { field(pin: ":") }`
	payload := []byte(
		`{"key-collision":"keep","pin":"\u003a","query":` +
			`"` + strings.ReplaceAll(query, `"`, `\"`) + `","array":[":"]}`,
	)
	options := knownCredentialRedactOptions(func(value string) bool {
		return value == ":" || value == "key-collision" || value == query
	})

	output, result, err := ProjectJSON(payload, options)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(
		`{"key-collision":"keep","pin":"` + credential.Redacted +
			`","query":"` + strings.ReplaceAll(
			`query Named { field(pin: "`+credential.Redacted+`") }`,
			`"`,
			`\"`,
		) +
			`","array":["` + credential.Redacted + `"]}`,
	)
	if !bytes.Equal(output, want) || result.Disposition != ExecutableExact {
		t.Fatalf("known JSON projection = %s / %#v, want %s", output, result, want)
	}
}

func TestProjectJSONKnownCredentialMatcherRejectsNonStringScalars(t *testing.T) {
	t.Parallel()
	for _, scalar := range []string{"731", "true", "null", "1.00e+2"} {
		scalar := scalar
		t.Run(scalar, func(t *testing.T) {
			t.Parallel()
			options := knownCredentialRedactOptions(func(value string) bool {
				return value == scalar
			})
			output, result, err := ProjectJSON(
				[]byte(`{"code":`+scalar+`,"ordinary":"keep"}`),
				options,
			)
			if err != nil {
				t.Fatal(err)
			}
			if output != nil || result.Disposition != Rejected ||
				!resultHasFindingCode(
					result,
					"credential_json_known_non_string_unsupported",
				) {
				t.Fatalf("known non-string JSON = %s / %#v", output, result)
			}
		})
	}
}

func TestProjectJSONKnownCredentialMatcherHandlesQueryPrecedence(t *testing.T) {
	t.Parallel()
	t.Run("non GraphQL query falls back to ordinary scalar", func(t *testing.T) {
		options := knownCredentialRedactOptions(func(value string) bool {
			return value == "canary-secret"
		})
		output, result, err := ProjectJSON(
			[]byte(`{"query":"canary-secret"}`),
			options,
		)
		if err != nil {
			t.Fatal(err)
		}
		want := []byte(`{"query":"` + credential.Redacted + `"}`)
		if !bytes.Equal(output, want) || result.Disposition != ExecutableExact {
			t.Fatalf("non-GraphQL query = %s / %#v, want %s", output, result, want)
		}
	})
	t.Run("sensitive query receives one replacement", func(t *testing.T) {
		options := knownCredentialRedactOptions(func(value string) bool {
			return strings.Contains(value, ":")
		})
		options.SensitiveNames = map[string]string{"query": "query"}
		output, result, err := ProjectJSON(
			[]byte(`{"query":"query { field(pin: \":\") }"}`),
			options,
		)
		if err != nil {
			t.Fatal(err)
		}
		want := []byte(`{"query":"` + credential.Redacted + `"}`)
		if !bytes.Equal(output, want) || result.Disposition != ExecutableExact {
			t.Fatalf("sensitive query = %s / %#v, want %s", output, result, want)
		}
	})
}

func TestProjectKnownGraphQLScalarsPreservesGrammarTokens(t *testing.T) {
	t.Parallel()
	query := []byte(
		"query Known($value: String) {\n" +
			"  Known: field(pin: \"known\", ordinary: $value)\n" +
			"  # known comment\n" +
			"}",
	)
	structural := map[string]struct{}{
		"query": {}, "Known": {}, "value": {}, "String": {},
		"field": {}, "pin": {}, "ordinary": {},
	}
	options := knownCredentialRedactOptions(func(value string) bool {
		if _, forbidden := structural[value]; forbidden {
			t.Fatalf("known matcher received GraphQL schema/name token %q", value)
		}
		return strings.Contains(value, "known")
	})

	output, result, err := projectKnownGraphQLScalars(query, options)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(
		"query Known($value: String) {\n" +
			"  Known: field(pin: \"" + credential.Redacted +
			"\", ordinary: $value)\n" +
			"  #" + credential.Redacted + "\n" +
			"}",
	)
	if !bytes.Equal(output, want) || result.Disposition != ExecutableExact {
		t.Fatalf("known GraphQL projection = %s / %#v, want %s", output, result, want)
	}
}

func TestProjectKnownGraphQLScalarsRejectsNonStringValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
		value string
	}{
		{
			name:  "number argument",
			query: `query { field(pin: 731) }`,
			value: "731",
		},
		{
			name:  "scientific number argument",
			query: `query { field(pin: 1.00e+2) }`,
			value: "1.00e+2",
		},
		{
			name:  "enum argument",
			query: `query { field(role: KNOWN_ENUM) }`,
			value: "KNOWN_ENUM",
		},
		{
			name:  "variable default",
			query: `query Named($pin: Int = 731) { field(pin: $pin) }`,
			value: "731",
		},
		{
			name:  "nested input value",
			query: `query { field(input: { pin: 731 }) }`,
			value: "731",
		},
		{
			name:  "list value",
			query: `query { field(pins: [100, 731]) }`,
			value: "731",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := knownCredentialRedactOptions(func(value string) bool {
				return value == test.value
			})
			output, result, err := projectKnownGraphQLScalars(
				[]byte(test.query),
				options,
			)
			if err != nil {
				t.Fatal(err)
			}
			if output != nil || result.Disposition != Rejected ||
				!resultHasFindingCode(
					result,
					"credential_graphql_known_non_string_unsupported",
				) {
				t.Fatalf("known non-string GraphQL = %s / %#v", output, result)
			}
		})
	}
}

func TestProjectKnownGraphQLScalarsDoesNotMatchNames(t *testing.T) {
	t.Parallel()
	query := []byte(`query Known { Known: field(arg: $Known) }`)
	calls := 0
	options := knownCredentialRedactOptions(func(string) bool {
		calls++
		return true
	})

	output, result, err := projectKnownGraphQLScalars(query, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, query) || result.Disposition != ExecutableExact ||
		calls != 0 {
		t.Fatalf("GraphQL names reached matcher: %s / %#v / calls=%d", output, result, calls)
	}
}

func TestProjectKnownGraphQLScalarsMasksLexicallySafeSDLScalars(t *testing.T) {
	t.Parallel()
	query := []byte(`type Input { value: String @default(value: "known") } # known`)
	options := knownCredentialRedactOptions(func(value string) bool {
		return strings.Contains(value, "known")
	})
	output, result, err := projectKnownGraphQLScalars(query, options)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(
		`type Input { value: String @default(value: "` +
			credential.Redacted + `") } #` + credential.Redacted,
	)
	if !bytes.Equal(output, want) || result.Disposition != ExecutableExact {
		t.Fatalf("GraphQL SDL projection = %s / %#v, want %s", output, result, want)
	}
}

func TestProjectKnownGraphQLScalarsNormalizesBlockStringValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name: "common indentation and blank lines",
			token: `"""
    :
  """`,
			want: ":",
		},
		{
			name:  "first line is not dedented",
			token: "\"\"\"  first\r\n    second\"\"\"",
			want:  "  first\nsecond",
		},
		{
			name: "relative indentation is retained",
			token: `"""
  first
    second
  """`,
			want: "first\n  second",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := graphQLStringSemanticValue([]byte(test.token)); got != test.want {
				t.Fatalf("block string value = %q, want %q", got, test.want)
			}
		})
	}

	query := []byte("query { field(note: \"\"\"\n    :\n  \"\"\") }")
	options := knownCredentialRedactOptions(func(value string) bool {
		return value == ":"
	})
	output, result, err := projectKnownGraphQLScalars(query, options)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`query { field(note: "` + credential.Redacted + `") }`)
	if !bytes.Equal(output, want) || result.Disposition != ExecutableExact {
		t.Fatalf("block string projection = %s / %#v, want %s", output, result, want)
	}
}

func TestProjectKnownGraphQLScalarsRejectsMalformedNumbers(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"1e", "1.", "1.2e"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			options := knownCredentialRedactOptions(func(candidate string) bool {
				return candidate == value
			})
			output, result, err := projectKnownGraphQLScalars(
				[]byte(`query { field(code: `+value+`) }`),
				options,
			)
			if err != nil {
				t.Fatal(err)
			}
			if output != nil || result.Disposition != Rejected ||
				!resultHasFindingCode(
					result,
					"credential_graphql_known_value_malformed",
				) {
				t.Fatalf("malformed GraphQL number = %s / %#v", output, result)
			}
		})
	}
}

func TestProjectKnownGraphQLScalarsInspectsSDLValuesWithoutSchemaNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "directive application argument",
			query: `type Query @guard(code: 731) { field: Int }`,
		},
		{
			name:  "input field default",
			query: `input Input { code: Int = 731 }`,
		},
		{
			name:  "directive definition default",
			query: `directive @guard(code: Int = 731) on OBJECT`,
		},
	}
	schemaNames := map[string]struct{}{
		"Query": {}, "guard": {}, "code": {}, "field": {},
		"Int": {}, "Input": {}, "OBJECT": {},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := knownCredentialRedactOptions(func(value string) bool {
				if _, schema := schemaNames[value]; schema {
					t.Fatalf("known matcher received GraphQL schema/name token %q", value)
				}
				return value == "731"
			})
			output, result, err := projectKnownGraphQLScalars(
				[]byte(test.query),
				options,
			)
			if err != nil {
				t.Fatal(err)
			}
			if output != nil || result.Disposition != Rejected ||
				!resultHasFindingCode(
					result,
					"credential_graphql_known_non_string_unsupported",
				) {
				t.Fatalf("known SDL value = %s / %#v", output, result)
			}
		})
	}

	calls := 0
	options := knownCredentialRedactOptions(func(value string) bool {
		calls++
		return true
	})
	query := []byte(`type Query { field(code: Int): Int }`)
	output, result, err := projectKnownGraphQLScalars(query, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, query) || result.Disposition != ExecutableExact ||
		calls != 0 {
		t.Fatalf("schema-only GraphQL reached matcher: %s / %#v / calls=%d", output, result, calls)
	}
}

func TestProjectKnownGraphQLScalarsBoundsNestedValueWalk(t *testing.T) {
	t.Parallel()
	options := knownCredentialRedactOptions(func(string) bool { return false })
	options.Limits = DefaultLimits()
	options.Limits.MaxDepth = 2
	output, result, err := projectKnownGraphQLScalars(
		[]byte(`query { field(input: { a: { b: { c: 1 } } }) }`),
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != nil || result.Disposition != Rejected ||
		!resultHasFindingCode(
			result,
			"credential_graphql_known_value_depth_limit_exceeded",
		) {
		t.Fatalf("nested GraphQL bound = %s / %#v", output, result)
	}
}

func resultHasFindingCode(result Result, code string) bool {
	for _, finding := range result.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
