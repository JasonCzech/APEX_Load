package scrub

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

// Preserve the prior callback implementation as an equivalence oracle. This
// optimization changes capture reuse, not the malformed JSON safety boundary.
func previousJSONFallbackFields(value string) string {
	value = redactMalformedJSONBareScalars(value, credential.SensitiveName)
	value = redactMalformedJSONStrings(value, credential.SensitiveName)
	value = jsonFallbackFieldPattern.ReplaceAllStringFunc(value, func(field string) string {
		matches := jsonFallbackFieldPattern.FindStringSubmatch(field)
		name, err := strconv.Unquote(`"` + matches[2] + `"`)
		if err != nil || !credential.SensitiveName(name) {
			return field
		}
		return matches[1] + `"` + Redacted + `"` + matches[4]
	})
	return redactJSONCompoundFields(value, credential.SensitiveName)
}

func TestJSONFallbackCaptureReuseMatchesPreviousProjection(t *testing.T) {
	cases := []string{
		`prefix {"password":123,"ordinary":"retain"} suffix`,
		`{"pass\u0077ord":"canary","token":true,"client_secret":null} trailing`,
		`{"password":123abc,"ordinary":"retain","token":"canary"} trailing`,
		`{"password":"canary"broken,"ordinary":"retain"}`,
		`{"password":{"nested":"canary"},"ordinary":"retain"} trailing`,
		`{"password":"unclosed canary`,
		`"ordinary":"long escaped \\\"quoted\\\" value","password":"canary"`,
		strings.Repeat(`source "ordinary":"escaped \\\"field\\\"", "password":"canary"; `, 2000),
	}
	rng := rand.New(rand.NewSource(19))
	fragments := []string{`"ordinary":`, `"password":`, `"token":`, `"canary"`, `"`, `\`, `{"nested":`, `[`, `}`, `]`, `,`, `\"`, `1e+2`, `123abc`, `true`, "\n", " ", ":"}
	for i := 0; i < 250; i++ {
		var value strings.Builder
		for j := 0; j < 25; j++ {
			value.WriteString(fragments[rng.Intn(len(fragments))])
		}
		cases = append(cases, value.String())
	}
	for index, value := range cases {
		got := redactJSONFallbackFields(value, credential.SensitiveName)
		if want := previousJSONFallbackFields(value); got != want {
			t.Fatalf("case %d: capture reuse changed redaction", index)
		}
	}
}

func BenchmarkJSONFallbackLargeEscapedSource(b *testing.B) {
	value := strings.Repeat(`source "ordinary":"escaped \\\"field\\\"", "password":"canary"; `, 2000)
	b.SetBytes(int64(len(value)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = redactJSONFallbackFields(value, credential.SensitiveName)
	}
}
