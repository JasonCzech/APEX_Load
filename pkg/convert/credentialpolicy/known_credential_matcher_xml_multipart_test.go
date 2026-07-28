package credentialpolicy

import (
	"mime"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func TestProjectXMLKnownCredentialMatcherRejectsDescendantSplitValue(t *testing.T) {
	t.Parallel()
	const secret = "secret"
	options := knownCredentialRedactOptions(func(value string) bool {
		return strings.Contains(value, secret)
	})

	output, result, err := ProjectXML(
		[]byte(`<root>se<child>cr</child>et</root>`),
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != nil || result.Disposition != Rejected ||
		!resultHasFindingCode(
			result,
			"credential_xml_known_value_split_content",
		) {
		t.Fatalf("descendant-split XML = %q / %#v", output, result)
	}
}

func TestProjectXMLKnownCredentialMatcherChecksAggregateAfterEarlierMatch(t *testing.T) {
	t.Parallel()
	options := knownCredentialRedactOptions(func(value string) bool {
		return value == "x" || strings.Contains(value, "secret")
	})

	output, result, err := ProjectXML(
		[]byte(`<root>x<![CDATA[se]]>cr<![CDATA[et]]></root>`),
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != nil || result.Disposition != Rejected ||
		!resultHasFindingCode(
			result,
			"credential_xml_known_value_split_content",
		) {
		t.Fatalf("partially matched split XML = %q / %#v", output, result)
	}
}

func TestRedactKnownMultipartFilenameProjectsExtensionParameters(t *testing.T) {
	t.Parallel()
	const secret = "canary-secret"
	input := `form-data; name="upload"; filename="safe.txt"; x-note="` +
		secret + `"`

	output := RedactKnownMultipartFilename(
		input,
		func(value string) bool { return strings.Contains(value, secret) },
	)
	disposition, parameters, err := mime.ParseMediaType(output)
	if err != nil {
		t.Fatalf("parse projected Content-Disposition %q: %v", output, err)
	}
	if disposition != "form-data" ||
		parameters["name"] != "upload" ||
		parameters["filename"] != "safe.txt" ||
		parameters["x-note"] != credential.Redacted {
		t.Fatalf("projected Content-Disposition = %q / %#v", output, parameters)
	}
}

func TestKnownMultipartStructuralHeadersFailClosedWhenMalformed(t *testing.T) {
	t.Parallel()
	const secret = "canary-secret"
	matcher := func(value string) bool {
		return strings.Contains(value, secret)
	}
	if got := RedactKnownMultipartFilename(
		`form-data; x-note="`+secret,
		matcher,
	); got != credential.Redacted {
		t.Fatalf("malformed Content-Disposition = %q", got)
	}
	if got := RedactKnownMultipartBoundary(
		`multipart/form-data; boundary="`+secret,
		matcher,
	); got != credential.Redacted {
		t.Fatalf("malformed Content-Type = %q", got)
	}
}

func TestKnownMIMEHeadersPreserveBoundedGrammarAndMaskMalformedValues(t *testing.T) {
	t.Parallel()
	const secret = "Giraffe7!"
	matcher := func(value string) bool {
		return strings.Contains(value, secret)
	}
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "Content-Transfer-Encoding",
			value: "7bit " + secret,
			want:  credential.Redacted,
		},
		{
			name:  "Content-Transfer-Encoding",
			value: secret,
			want:  credential.Redacted,
		},
		{
			name:  "MIME-Version",
			value: "1.0 " + secret,
			want:  credential.Redacted,
		},
		{
			name:  "Content-Type",
			value: `application/json; x-note="` + secret + `"`,
			want:  `application/json; x-note="` + credential.Redacted + `"`,
		},
		{
			name:  "Content-Type",
			value: `text/plain; charset="` + secret + `"`,
			want:  `text/plain; charset="` + credential.Redacted + `"`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := RedactKnownMIMEHeader(
				test.name,
				test.value,
				matcher,
			); got != test.want {
				t.Fatalf("%s projection = %q, want %q", test.name, got, test.want)
			}
		})
	}
	if got := RedactKnownMIMEHeader(
		"Content-Transfer-Encoding",
		"x-custom",
		func(value string) bool { return value == "x-custom" },
	); got != "x-custom" {
		t.Fatalf("extension transfer encoding = %q", got)
	}
	if got := RedactKnownMIMEHeader(
		"MIME-Version",
		"1.0",
		func(value string) bool { return value == "1.0" },
	); got != "1.0" {
		t.Fatalf("standard MIME version = %q", got)
	}
	if got := RedactKnownMIMEHeader(
		"Content-Type",
		"text/plain; charset=windows-1252",
		func(value string) bool { return value == "windows-1252" },
	); got != "text/plain; charset=windows-1252" {
		t.Fatalf("registered charset = %q", got)
	}
}

func TestProjectXMLBoundsAggregateKnownCredentialComparisons(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("ordinary-", 8_192)
	payload := []byte(strings.Repeat("<n>", 16) + text + strings.Repeat("</n>", 16))
	options := knownCredentialRedactOptions(nil)
	options.Limits = DefaultLimits()
	options.Limits.MaxBytes = len(payload)
	comparedBytes := 0
	options.KnownCredentialMatcher = func(value string) bool {
		comparedBytes += len(value)
		return false
	}

	output, result, err := ProjectXML(payload, options)
	if err != nil {
		t.Fatal(err)
	}
	if output != nil || result.Disposition != Rejected ||
		!resultHasFindingCode(
			result,
			"credential_xml_known_value_split_content",
		) {
		t.Fatalf("bounded nested XML = %q / %#v", output, result)
	}
	if comparedBytes > 2*options.Limits.MaxBytes {
		t.Fatalf(
			"known matcher compared %d bytes, limit %d",
			comparedBytes,
			2*options.Limits.MaxBytes,
		)
	}
}

func TestProjectGraphQLBoundsRepeatedNestedValueComparisons(t *testing.T) {
	t.Parallel()
	number := "1" + strings.Repeat("2", 64<<10)
	query := []byte(
		`query { field(input: {a: {b: {c: ` + number + `}}}) }`,
	)
	options := knownCredentialRedactOptions(nil)
	options.Limits = DefaultLimits()
	options.Limits.MaxBytes = len(query)
	comparedBytes := 0
	options.KnownCredentialMatcher = func(value string) bool {
		comparedBytes += len(value)
		return false
	}

	output, result, err := projectKnownGraphQLScalars(query, options)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != string(query) ||
		result.Disposition != ExecutableExact {
		t.Fatalf("bounded nested GraphQL = %q / %#v", output, result)
	}
	if comparedBytes > 2*options.Limits.MaxBytes {
		t.Fatalf(
			"known matcher compared %d bytes, limit %d",
			comparedBytes,
			2*options.Limits.MaxBytes,
		)
	}
}

func TestProjectKnownGraphQLScalarsRejectsMalformedValuePunctuation(t *testing.T) {
	t.Parallel()
	for _, secret := range []string{"123456!", "123456@", "123456/"} {
		secret := secret
		t.Run(secret, func(t *testing.T) {
			t.Parallel()
			options := knownCredentialRedactOptions(func(value string) bool {
				return value == secret
			})
			output, result, err := projectKnownGraphQLScalars(
				[]byte(`query { field(code: `+secret+`) }`),
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
				t.Fatalf("malformed GraphQL punctuation = %q / %#v", output, result)
			}
		})
	}

	t.Run("bare variable marker", func(t *testing.T) {
		t.Parallel()
		options := knownCredentialRedactOptions(func(value string) bool {
			return value == "$"
		})
		output, result, err := projectKnownGraphQLScalars(
			[]byte(`query { field(code: $) }`),
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
			t.Fatalf("bare GraphQL variable = %q / %#v", output, result)
		}
	})

	t.Run("default followed by bare name", func(t *testing.T) {
		t.Parallel()
		options := knownCredentialRedactOptions(func(value string) bool {
			return value == "123 SECRET"
		})
		output, result, err := projectKnownGraphQLScalars(
			[]byte(`query Q($code: Int = 123 SECRET) { field }`),
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
			t.Fatalf("malformed GraphQL default = %q / %#v", output, result)
		}
	})

	t.Run("default followed by selection delimiter", func(t *testing.T) {
		t.Parallel()
		options := knownCredentialRedactOptions(func(value string) bool {
			return value == "123 {"
		})
		output, result, err := projectKnownGraphQLScalars(
			[]byte(`query Q($code: Int = 123 { field }) { root }`),
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
			t.Fatalf("malformed GraphQL default delimiter = %q / %#v", output, result)
		}
	})

	for _, test := range []struct {
		name  string
		query string
		value string
	}{
		{
			name:  "next variable missing type",
			query: `query Q($code: Int = 123 $next:) { field }`,
			value: "123 $next:",
		},
		{
			name:  "directive has invalid tail",
			query: `query Q($code: Int = 123 @foo !) { field }`,
			value: "123 @foo !",
		},
		{
			name:  "directive has invalid arguments",
			query: `query Q($code: Int = 123 @foo(!)) { field }`,
			value: "123 @foo(!)",
		},
		{
			name:  "executable default has SDL follower",
			query: `query Q($code: Int = 123 next:String) { field }`,
			value: "123 next:String",
		},
		{
			name:  "directive has empty arguments",
			query: `query Q($code: Int = 123 @foo()) { field }`,
			value: "123 @foo()",
		},
		{
			name:  "default value is a variable",
			query: `query Q($code: Int = $bar) { field }`,
			value: "$bar",
		},
		{
			name:  "directive const argument is a variable",
			query: `query Q($code: Int = 123 @foo(flag: $bar)) { field }`,
			value: "123 @foo(flag: $bar)",
		},
	} {
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
					"credential_graphql_known_value_malformed",
				) {
				t.Fatalf("malformed GraphQL follower = %q / %#v", output, result)
			}
		})
	}

	t.Run("complete following productions remain valid", func(t *testing.T) {
		t.Parallel()
		query := []byte(
			`query Q($code: Int = 123 @foo(flag: true) ` +
				`$next: [Int!]! = 456) { field }`,
		)
		options := knownCredentialRedactOptions(func(string) bool {
			return false
		})
		output, result, err := projectKnownGraphQLScalars(query, options)
		if err != nil {
			t.Fatal(err)
		}
		if string(output) != string(query) ||
			result.Disposition != ExecutableExact {
			t.Fatalf("valid GraphQL follower = %q / %#v", output, result)
		}

		sdl := []byte(`input Input { code: Int = 123 next: String = 456 }`)
		output, result, err = projectKnownGraphQLScalars(sdl, options)
		if err != nil {
			t.Fatal(err)
		}
		if string(output) != string(sdl) ||
			result.Disposition != ExecutableExact {
			t.Fatalf("valid SDL follower = %q / %#v", output, result)
		}
	})

	t.Run("executable argument variables remain valid", func(t *testing.T) {
		t.Parallel()
		query := []byte(
			`query Q($value: Int) { field(code: $value) }`,
		)
		options := knownCredentialRedactOptions(func(string) bool {
			return true
		})
		output, result, err := projectKnownGraphQLScalars(query, options)
		if err != nil {
			t.Fatal(err)
		}
		if string(output) != string(query) ||
			result.Disposition != ExecutableExact {
			t.Fatalf(
				"valid GraphQL argument variable = %q / %#v",
				output,
				result,
			)
		}
	})
}
