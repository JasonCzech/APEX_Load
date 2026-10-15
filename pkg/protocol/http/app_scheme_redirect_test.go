package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A native or hybrid app's OAuth sign-in ends on a redirect to the app's
// private URI scheme. Replay must stop there with the 3xx as the final
// response, so the authorization code in its Location can be extracted.
func TestHTTPRedirectToAppSchemeEndsOnThe3xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "com.example.app:/oauth/done?display=touch&code=aPrx%3D%3D")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	for _, tc := range []struct {
		name         string
		maxRedirects *int
	}{
		{name: "default redirect policy"},
		{name: "explicit redirect limit", maxRedirects: intPointer(5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &HTTP{}
			if err := h.Init(context.Background(), map[string]any{}); err != nil {
				t.Fatal(err)
			}
			defer h.Teardown(context.Background())
			result, err := h.Execute(context.Background(), &protocol.Action{
				Name:         "authorize",
				Type:         "GET",
				Method:       "GET",
				Target:       server.URL + "/services/oauth2/authorize",
				MaxRedirects: tc.maxRedirects,
				Extractors: []protocol.Extractor{{
					Name:       "code",
					Source:     "header",
					Type:       "regex",
					Expression: `Location: [^?\s]*\?(?:[^&\s]*&)*code=([^&\s]+)`,
					Convert:    "url_decode",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Success || result.StatusCode != http.StatusFound {
				t.Fatalf("result = success %v, status %d, error %q; want the 302 as a successful final response", result.Success, result.StatusCode, result.ErrorMessage)
			}
			if got := result.Extractions["code"]; got != "aPrx==" {
				t.Fatalf("code = %q, want the decoded Location code", got)
			}
		})
	}
}

func TestFollowableRedirectSchemeAcceptsOnlyWebURLs(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://example.test/next":        true,
		"HTTP://example.test/next":         true,
		"com.example.app:/oauth/done":      false,
		"testsfdc:///mobilesdk/oauth/done": false,
		"mailto:someone@example.test":      false,
	} {
		target, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got := followableRedirectScheme(target.URL); got != want {
			t.Fatalf("followableRedirectScheme(%q) = %v, want %v", raw, got, want)
		}
	}
	if followableRedirectScheme(nil) {
		t.Fatal("a nil redirect target must not be followed")
	}
}
