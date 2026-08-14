package credentialpolicy

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func TestClassifySourceCredentialValue(t *testing.T) {
	base64Sentinel := base64.StdEncoding.EncodeToString([]byte("[REDACTED]"))
	utf16Sentinel := func(littleEndian, bom bool) string {
		var encoded bytes.Buffer
		if bom {
			if littleEndian {
				encoded.Write([]byte{0xff, 0xfe})
			} else {
				encoded.Write([]byte{0xfe, 0xff})
			}
		}
		for _, current := range "[REDACTED]" {
			if littleEndian {
				encoded.Write([]byte{byte(current), 0})
			} else {
				encoded.Write([]byte{0, byte(current)})
			}
		}
		return base64.StdEncoding.EncodeToString(encoded.Bytes())
	}
	deepSentinel := "[REDACTED]"
	for range 40 {
		deepSentinel = url.QueryEscape(deepSentinel)
	}
	tests := []struct {
		name  string
		value string
		want  SourceCredentialValueStatus
	}{
		{name: "available", value: "real-credential", want: SourceCredentialValueAvailable},
		{name: "missing", value: " \t", want: SourceCredentialValueMissing},
		{name: "case and wrapper", value: "Bearer [rEdAcTeD]", want: SourceCredentialValueRedactedPlaceholder},
		{name: "percent encoded", value: "%5bReDaCtEd%5d", want: SourceCredentialValueRedactedPlaceholder},
		{name: "nested percent encoded", value: "%25255BReDaCtEd%25255D", want: SourceCredentialValueRedactedPlaceholder},
		{name: "malformed percent suffix", value: "%5BReDaCtEd%5D%ZZ", want: SourceCredentialValueRedactedPlaceholder},
		{name: "xml numeric", value: "&#91;ReDaCtEd&#93;", want: SourceCredentialValueRedactedPlaceholder},
		{name: "nested xml", value: "&amp;amp;#91;ReDaCtEd&amp;amp;#93;", want: SourceCredentialValueRedactedPlaceholder},
		{name: "json unicode", value: `\u005bReDaCtEd\u005d`, want: SourceCredentialValueRedactedPlaceholder},
		{name: "escaped newline before json unicode", value: `\n\u005bReDaCtEd\u005d`, want: SourceCredentialValueRedactedPlaceholder},
		{name: "base64", value: base64Sentinel, want: SourceCredentialValueRedactedPlaceholder},
		{name: "base64 utf16 little endian", value: utf16Sentinel(true, false), want: SourceCredentialValueRedactedPlaceholder},
		{name: "base64 utf16 big endian", value: utf16Sentinel(false, false), want: SourceCredentialValueRedactedPlaceholder},
		{name: "base64 utf16 little endian bom", value: utf16Sentinel(true, true), want: SourceCredentialValueRedactedPlaceholder},
		{name: "base64 utf16 big endian bom", value: utf16Sentinel(false, true), want: SourceCredentialValueRedactedPlaceholder},
		{name: "over depth", value: deepSentinel, want: SourceCredentialValueUnsafeEncoding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifySourceCredentialValue(test.value); got != test.want {
				t.Fatalf("ClassifySourceCredentialValue(%q) = %q, want %q", test.value, got, test.want)
			}
			got, ok := RecoverableSourceCredentialValue(test.value)
			if wantOK := test.want == SourceCredentialValueAvailable; ok != wantOK {
				t.Fatalf("RecoverableSourceCredentialValue(%q) ok = %t, want %t", test.value, ok, wantOK)
			} else if ok && got != test.value {
				t.Fatalf("recoverable value = %q, want byte-faithful %q", got, test.value)
			}
		})
	}
}

func TestPreserveProjectionReplacesRedactionPlaceholderWithUnconfiguredReference(t *testing.T) {
	projected, result, err := ProjectHeaders(
		[]HeaderOccurrence{{Name: "Authorization", Value: "Bearer %255Bredacted%255D"}},
		Options{Mode: Preserve, Source: SourceRef{Format: "fixture", Path: "request.json"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want one unavailable credential", result.Requirements)
	}
	requirement := result.Requirements[0]
	if requirement.SourceValueAvailable || requirement.Value != "" ||
		requirement.SourceValueStatus != SourceCredentialValueRedactedPlaceholder {
		t.Fatalf("requirement = %#v, want unavailable redaction source", requirement)
	}
	if len(projected) != 1 || projected[0].Value != requirement.Replacement ||
		!strings.HasPrefix(requirement.Replacement, "{{secret.") {
		t.Fatalf("projected headers = %#v requirement = %#v", projected, requirement)
	}
}

func TestRedactProjectionKeepsDisclosureSentinelWithoutRetainingItAsSource(t *testing.T) {
	projected, result, err := ProjectHeaders(
		[]HeaderOccurrence{{Name: "Authorization", Value: "Bearer [redacted]"}},
		Options{Mode: Redact, Source: SourceRef{Format: "script_disclosure"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 || projected[0].Value != "[REDACTED]" {
		t.Fatalf("projected headers = %#v, want the neutral disclosure sentinel", projected)
	}
	if len(result.Requirements) != 1 || result.Requirements[0].Value != "" ||
		result.Requirements[0].SourceValueAvailable {
		t.Fatalf("requirements = %#v, want no recoverable source value", result.Requirements)
	}
}
