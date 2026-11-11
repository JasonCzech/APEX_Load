package jmeter

import (
	"os"
	"strings"
	"testing"
)

func TestMessagingSourceReviewPreservesTokenEndpointQueryCredentialsVerbatim(t *testing.T) {
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
				if f.Redacted || f.Content != plan || !strings.Contains(f.Content, tc.secret) {
					t.Fatalf("source review %s (redacted=%v) modified token endpoint source", f.Path, f.Redacted)
				}
			}
		})
	}
}

func TestMessagingSourceReviewPreservesTemplatedAndUserinfoTokenEndpointsVerbatim(t *testing.T) {
	data, err := os.ReadFile("testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	const endpoint = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token"
	if !strings.Contains(string(data), endpoint) {
		t.Fatal("fixture has no token endpoint")
	}
	for _, tc := range []struct{ name, value, secret string }{
		{"templated host", "https://${IDP_HOST}/t/oauth2/v2.0/token?client_secret=HOSTSECRETVALUE1", "HOSTSECRETVALUE1"},
		{"fragment", "https://login.microsoftonline.com/t/oauth2/v2.0/token#client_secret=FRAGSECRETVALUE2", "FRAGSECRETVALUE2"},
		{"templated host userinfo", "https://svc:USERINFOSECRET8@${IDP_HOST}/token", "USERINFOSECRET8"},
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
				if f.Redacted || f.Content != plan || !strings.Contains(f.Content, tc.secret) {
					t.Fatalf("source review %s (redacted=%v) modified token endpoint source", f.Path, f.Redacted)
				}
			}
		})
	}
}
