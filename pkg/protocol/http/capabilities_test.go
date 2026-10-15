package http

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestNormalizeAndLookupAuthCapability(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw     string
		mode    string
		support AuthSupport
	}{
		{"", "", AuthExecutable},
		{" NOAUTH ", AuthTypeNone, AuthExecutable},
		{"basic", AuthTypeBasic, AuthExecutable},
		{"Bearer", AuthTypeBearer, AuthExecutable},
		{"digest", AuthTypeDigest, AuthExecutable},
		{"ntlm", AuthTypeNTLM, AuthExecutable},
		{"Kerberos", AuthTypeKerberos, AuthReviewOnly},
		{"negotiate", AuthTypeNegotiate, AuthExecutable},
		{"Negotiate_Any", AuthTypeNegotiateAny, AuthExecutable},
		{"aws", AuthTypeAWS, AuthReviewOnly},
		{"hawk", AuthTypeHawk, AuthReviewOnly},
		{"future-auth", "future-auth", AuthReviewOnly},
	}
	for _, test := range tests {
		test := test
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			got := LookupAuthCapability(test.raw)
			if got.Type != test.mode || got.Support != test.support {
				t.Fatalf("LookupAuthCapability(%q) = %#v", test.raw, got)
			}
			if AuthTypeExecutable(test.raw) != (test.support == AuthExecutable) {
				t.Fatalf("AuthTypeExecutable(%q) disagrees with capability", test.raw)
			}
		})
	}
}

func TestAuthCapabilityFieldRequirements(t *testing.T) {
	t.Parallel()
	basic := LookupAuthCapability(AuthTypeBasic)
	if !basic.RequiresUsername || !basic.RequiresPassword || basic.RequiresToken {
		t.Fatalf("basic capability = %#v", basic)
	}
	bearer := LookupAuthCapability(AuthTypeBearer)
	if !bearer.RequiresToken || bearer.RequiresPassword {
		t.Fatalf("bearer capability = %#v", bearer)
	}
	digest := LookupAuthCapability(AuthTypeDigest)
	if !digest.ChallengeResponse || !digest.RequiresUsername || !digest.RequiresPassword {
		t.Fatalf("digest capability = %#v", digest)
	}
	if !LookupAuthCapability(AuthTypeNone).ExplicitNoAuth {
		t.Fatal("none must remain distinguishable from an omitted action auth")
	}
	inherit := LookupAuthCapability(AuthTypeInherit)
	if !inherit.ExplicitNoAuth || inherit.InheritsScriptAuth {
		t.Fatalf("literal runtime inherit is an explicit empty override: %#v", inherit)
	}
	if !LookupAuthCapability("").InheritsScriptAuth {
		t.Fatal("an omitted action auth is the only true script-auth inheritance")
	}
}

func TestValidateAuthTypePreservesRuntimeBehavior(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "none", "noauth", "inherit", "basic", "bearer", "digest", "ntlm", "negotiate", "negotiate_any"} {
		if err := ValidateAuthType(mode); err != nil {
			t.Fatalf("ValidateAuthType(%q): %v", mode, err)
		}
	}
	if err := ValidateAuthType("kerberos"); err == nil || !strings.Contains(err.Error(), "external auth flow") {
		t.Fatalf("review-only auth error = %v", err)
	}
	if err := ValidateAuthType("unknown"); err == nil || !strings.Contains(err.Error(), "unsupported HTTP auth type") {
		t.Fatalf("unknown auth error = %v", err)
	}
}

func TestCapabilitiesExposeRuntimeConstantsAndDetachedSlice(t *testing.T) {
	t.Parallel()
	first := Capabilities()
	if first.MaxTimeoutMS != int(MaxConfiguredTimeout/time.Millisecond) ||
		first.MaxRedirects != MaxConfiguredRedirects ||
		first.MaxAssertions != MaxActionAssertions {
		t.Fatalf("capabilities = %#v", first)
	}
	if len(first.Authentication) != 10 {
		t.Fatalf("auth capabilities = %#v", first.Authentication)
	}
	first.Authentication[0].Type = "mutated"
	first.Methods[0] = "MUTATED"
	second := Capabilities()
	if second.Authentication[0].Type == "mutated" || second.Methods[0] == "MUTATED" {
		t.Fatal("Capabilities returned shared mutable storage")
	}
}

func TestSupportedMethodsAreCanonicalAndAuthoritative(t *testing.T) {
	t.Parallel()
	want := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	if got := SupportedMethods(); !slices.Equal(got, want) {
		t.Fatalf("SupportedMethods() = %#v, want %#v", got, want)
	}
	for _, method := range want {
		if !MethodExecutable(" " + strings.ToLower(method) + " ") {
			t.Fatalf("MethodExecutable(%q) = false", method)
		}
		if err := ValidateMethod(method); err != nil {
			t.Fatalf("ValidateMethod(%q): %v", method, err)
		}
	}
	for _, method := range []string{"", "CONNECT", "TRACE", "PROPFIND"} {
		if MethodExecutable(method) {
			t.Fatalf("MethodExecutable(%q) = true", method)
		}
		if err := ValidateMethod(method); err == nil {
			t.Fatalf("ValidateMethod(%q) unexpectedly succeeded", method)
		}
	}
	if got := ResolveMethod("", "post"); got != "POST" {
		t.Fatalf("ResolveMethod() = %q", got)
	}
	if got := ResolveMethod("", "request"); got != "REQUEST" {
		t.Fatalf("ResolveMethod() fallback = %q", got)
	}
	if got := ResolveMethod("", "CONNECT"); got != "CONNECT" {
		t.Fatalf("ResolveMethod() unsupported method-shaped type = %q", got)
	}
	for _, method := range []string{"connect", "Trace"} {
		if got := ResolveMethod("", method); got != strings.ToUpper(method) {
			t.Fatalf("ResolveMethod(%q) = %q, want fail-closed legacy method", method, got)
		}
	}
}

func TestRuntimeActionValidationUsesSupportedMethods(t *testing.T) {
	t.Parallel()
	for _, method := range SupportedMethods() {
		if err := validateHTTPActionBounds(&protocol.Action{Type: "request", Method: method}); err != nil {
			t.Fatalf("%s action: %v", method, err)
		}
	}
	for _, method := range []string{"CONNECT", "connect", "TRACE", "Trace", "PROPFIND"} {
		err := validateHTTPActionBounds(&protocol.Action{Type: "request", Method: method})
		if err == nil || !strings.Contains(err.Error(), "unsupported HTTP method") {
			t.Fatalf("%s action error = %v", method, err)
		}
		err = validateHTTPActionBounds(&protocol.Action{Type: method})
		if err == nil || !strings.Contains(err.Error(), "unsupported HTTP method") {
			t.Fatalf("%s action type error = %v", method, err)
		}
	}
}

func TestValidateAuthWireValuesRejectsAmbiguousBasicUsername(t *testing.T) {
	t.Parallel()
	err := ValidateAuthWireValues(&protocol.AuthConfig{
		Type:     "basic",
		Username: "tenant:operator",
		Password: "password",
	})
	var wireErr *AuthWireValueError
	if !errors.As(err, &wireErr) || wireErr.Field != "username" {
		t.Fatalf("ValidateAuthWireValues() error = %v, want username wire error", err)
	}
}

func TestResolveMethodRejectsUnknownTypesUnlessExplicit(t *testing.T) {
	for _, actionType := range []string{"pots", "POTS", "post_order", "db_query", "request"} {
		if err := ValidateMethod(ResolveMethod("", actionType)); err == nil {
			t.Errorf("accepted unknown type %q", actionType)
		}
		if got := ResolveMethod("post", actionType); got != "POST" {
			t.Errorf("explicit method lost: %q", got)
		}
	}
	for _, actionType := range []string{"get", "POST", " put ", "patch", "delete", "head", "options", ""} {
		if err := ValidateMethod(ResolveMethod("", actionType)); err != nil {
			t.Errorf("rejected %q: %v", actionType, err)
		}
	}
}

func TestInvalidHeaderValueDetailExplainsTheCause(t *testing.T) {
	for _, test := range []struct {
		name, value, want string
	}{
		{name: "X-Note", value: "a\nb", want: "header value contains characters HTTP does not allow"},
		{name: "Cookie", value: `a=1" junk`, want: "header value contains characters HTTP does not allow"},
		{name: "Host", value: "", want: "header value cannot be empty"},
		{name: "User-Agent", value: " ", want: "header value cannot be empty"},
		{name: "host", value: "example.test/path", want: "header value is not a valid host"},
	} {
		if got := InvalidHeaderValueDetail(test.name, test.value); got != test.want {
			t.Errorf("InvalidHeaderValueDetail(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}
