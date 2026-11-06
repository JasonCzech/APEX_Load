package jmeter

import (
	"os"
	"strings"
	"testing"
)

// The OAuth token endpoint is an address, not a credential by name, so the
// messaging source review keeps it readable; a credential carried in its query
// or userinfo is still masked there.
func TestMessagingSourceReviewMasksTokenEndpointQueryCredentials(t *testing.T) {
	data, err := os.ReadFile("testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	const endpoint = "oauth2/v2.0/token</stringProp>"
	if !strings.Contains(string(data), endpoint) {
		t.Fatal("fixture has no token endpoint")
	}
	for _, tc := range []struct{ name, suffix, secret string }{
		{"query client_secret", "?client_secret=QUERYSECRETVALUE123&amp;scope=api", "QUERYSECRETVALUE123"},
		{"percent-encoded query token", "?token=ENC%2BTOKENVALUE456", "TOKENVALUE456"},
		{"short query password", "?password=pw1", "password=pw1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := strings.Replace(string(data), endpoint, strings.TrimSuffix(endpoint, "</stringProp>")+tc.suffix+"</stringProp>", 1)
			res, err := ConvertJMX([]byte(plan), ConvertOptions{})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if res.SourceReview == nil || len(res.SourceReview.SourceFiles) == 0 {
				t.Fatal("no source review")
			}
			for _, f := range res.SourceReview.SourceFiles {
				if strings.Contains(f.Content, tc.secret) {
					t.Fatalf("source review %s (redacted=%v) keeps the token endpoint query credential", f.Path, f.Redacted)
				}
				if !strings.Contains(f.Content, "login.microsoftonline.com") {
					t.Fatalf("source review %s lost the token endpoint address", f.Path)
				}
			}
		})
	}
}

// The token endpoint is scanned as text, not through url.Parse, so a templated
// or schemeless address, an unparseable one and a fragment still have their
// credential parameters masked; a parameter whose name cannot be read masks
// the whole value.
func TestMessagingSourceReviewMasksUnparseableTokenEndpointCredentials(t *testing.T) {
	data, err := os.ReadFile("testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	const endpoint = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token"
	if !strings.Contains(string(data), endpoint) {
		t.Fatal("fixture has no token endpoint")
	}
	for _, tc := range []struct{ name, value, secret, keep string }{
		{"templated host", "https://${IDP_HOST}/t/oauth2/v2.0/token?client_secret=HOSTSECRETVALUE1", "HOSTSECRETVALUE1", "${IDP_HOST}/t/oauth2"},
		{"fragment", "https://login.microsoftonline.com/t/oauth2/v2.0/token#client_secret=FRAGSECRETVALUE2", "FRAGSECRETVALUE2", "login.microsoftonline.com/t/oauth2"},
		{"schemeless", "login.microsoftonline.com/t/oauth2/v2.0/token?client_secret=BARESECRETVALUE3", "BARESECRETVALUE3", "login.microsoftonline.com/t/oauth2"},
		{"percent-encoded key", "https://${IDP_HOST}/token?client%5Fsecret=ENCKEYSECRETVALUE4", "ENCKEYSECRETVALUE4", "${IDP_HOST}/token"},
		{"mixed case key", "https://${IDP_HOST}/token?Client_SECRET=CASESECRETVALUE5", "CASESECRETVALUE5", "${IDP_HOST}/token"},
		{"invalid path escape", "https://login.microsoftonline.com/%zz/token?client_secret=ESCSECRETVALUE6", "ESCSECRETVALUE6", "login.microsoftonline.com/%zz"},
		{"whole-URL variable", "${TOKEN_URL}?client_secret=VARSECRETVALUE7&amp;scope=api", "VARSECRETVALUE7", "${TOKEN_URL}?client_secret="},
		{"templated host userinfo", "https://svc:USERINFOSECRET8@${IDP_HOST}/token", "USERINFOSECRET8", "${IDP_HOST}/token"},
		{"unreadable key fails closed", "https://${IDP_HOST}/token?client%zzsecret=UNREADSECRETVALUE9", "UNREADSECRETVALUE9", ""},
		// Userinfo the scan cannot split with confidence masks the whole value.
		{"userinfo base64 padding fails closed", "https://svc:PADSECRETVALUE10==@${IDP_HOST}/token", "PADSECRETVALUE10", ""},
		{"userinfo semicolon fails closed", "https://svc:SEMI;SEMISECRETVALUE11@${IDP_HOST}/token", "SEMISECRETVALUE11", ""},
		{"userinfo space fails closed", "https://svc:SPACE SPACESECRETVALUE12@${IDP_HOST}/token", "SPACESECRETVALUE12", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := strings.Replace(string(data), endpoint, tc.value, 1)
			res, err := ConvertJMX([]byte(plan), ConvertOptions{})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if res.SourceReview == nil || len(res.SourceReview.SourceFiles) == 0 {
				t.Fatal("no source review")
			}
			for _, f := range res.SourceReview.SourceFiles {
				if strings.Contains(f.Content, tc.secret) {
					t.Fatalf("source review %s (redacted=%v) keeps the token endpoint credential", f.Path, f.Redacted)
				}
				if tc.keep != "" && !strings.Contains(f.Content, tc.keep) {
					t.Fatalf("source review %s lost the token endpoint address %q", f.Path, tc.keep)
				}
			}
		})
	}
}
