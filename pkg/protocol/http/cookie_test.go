package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTPClearCookiesEmptiesJar(t *testing.T) {
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": "https://example.test"}); err != nil {
		t.Fatalf("init error: %v", err)
	}
	if h.client.Jar == nil {
		t.Fatal("expected a cookie jar when cookies are enabled by default")
	}

	target, _ := url.Parse("https://example.test/app")
	h.client.Jar.SetCookies(target, []*http.Cookie{{Name: "session", Value: "abc123"}})
	if got := h.client.Jar.Cookies(target); len(got) != 1 {
		t.Fatalf("seeded cookies = %#v, want one session cookie", got)
	}

	if err := h.ClearCookies(context.Background()); err != nil {
		t.Fatalf("ClearCookies error: %v", err)
	}
	if got := h.client.Jar.Cookies(target); len(got) != 0 {
		t.Fatalf("cookies after clear = %#v, want empty jar", got)
	}
}

func TestBoundedCookieJarEnforcesCookieAndProcessBudgets(t *testing.T) {
	target, _ := url.Parse("https://example.test/app/page")
	probe := &http.Cookie{Name: "session", Value: "small"}
	budget := &responseCaptureBudget{limit: boundedCookieSize(probe) + int64(len(probe.Value))}
	jar, err := newBoundedCookieJarWithBudget(budget)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(target, []*http.Cookie{probe, {Name: "second", Value: "small"}})
	if got := jar.Cookies(target); len(got) != 1 || got[0].Name != "session" {
		t.Fatalf("cookies after process budget = %#v, want only session", got)
	}
	// Formerly asserted through the jar's value side table
	// (runtimeCredentialValuesSnapshot() == []string{"small"}). The jar no longer
	// republishes cookie values for redaction, so the accepted value is observed
	// where it is actually used: on the wire.
	if got := jar.Cookies(target); got[0].Value != "small" {
		t.Fatalf("accepted cookie value = %q, want the value retained for transport", got[0].Value)
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "oversized", Value: strings.Repeat("x", maxHTTPCookieBytes)}})
	if got := jar.Cookies(target); len(got) != 1 {
		t.Fatalf("oversized cookie was retained: %#v", got)
	}
	if err := jar.clear(); err != nil {
		t.Fatal(err)
	}
	if got := budget.used.Load(); got != 0 {
		t.Fatalf("cookie clear leaked %d process bytes", got)
	}
}

func TestBoundedCookieJarAcceptedReplacementsDoNotGrowHistory(t *testing.T) {
	target, _ := url.Parse("https://example.test/app")
	budget := &responseCaptureBudget{limit: maxHTTPProcessCookieBytes}
	jar, err := newBoundedCookieJarWithBudget(budget)
	if err != nil {
		t.Fatal(err)
	}
	// Each stage previously asserted the jar's redaction side table
	// (runtimeCredentialValuesSnapshot()). The same accept/replace/delete
	// accounting is now observed on the jar contents that actually reach the wire.
	jar.SetCookies(target, []*http.Cookie{{Name: "opaque", Value: "first-cookie-value"}})
	if got := jar.Cookies(target); !reflect.DeepEqual(cookieValues(got), []string{"first-cookie-value"}) {
		t.Fatalf("first accepted values = %#v", got)
	}
	jar.SetCookies(target, []*http.Cookie{
		{Name: "opaque", Value: "second-cookie-value"},
		{Name: "opaque", Value: "second-cookie-value"},
	})
	if got := jar.Cookies(target); !reflect.DeepEqual(cookieValues(got), []string{"second-cookie-value"}) {
		t.Fatalf("replacement accepted values = %#v", got)
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "opaque", Value: "", MaxAge: -1}})
	if got := jar.Cookies(target); len(got) != 0 {
		t.Fatalf("cookie deletion left jar values = %#v", got)
	}
	// The deleted cookie's high-water reservation is deliberately conservative:
	// deletion never releases the entry, so a rotation cannot under-account.
	if got := len(jar.entries); got != 1 {
		t.Fatalf("entries after deletion = %d, want the conservative reservation kept", got)
	}

	constrainedBudget := &responseCaptureBudget{limit: 1}
	constrained, err := newBoundedCookieJarWithBudget(constrainedBudget)
	if err != nil {
		t.Fatal(err)
	}
	constrained.SetCookies(target, []*http.Cookie{{Name: "rejected", Value: "not-retained"}})
	if got := constrained.Cookies(target); len(got) != 0 {
		t.Fatalf("budget-rejected cookie was retained = %#v", got)
	}
}

// A cookie value is harvested from the SYSTEM UNDER TEST, not issued by the
// platform's secret store. The jar used to publish every value in it to the
// engine's runtime secret matcher, so an ordinary `Set-Cookie: locale=en` made
// the two-letter string "en" a secret and substring-redacted it out of the
// operator's own evidence — and out of metric dimension keys such as an action
// named "open_home". Nothing here republishes cookie values; they exist only to
// be replayed on the wire.
func TestBoundedCookieJarDoesNotHarvestCookieValuesAsSecrets(t *testing.T) {
	target, _ := url.Parse("https://example.test/app")
	jar, err := newBoundedCookieJarWithBudget(&responseCaptureBudget{limit: maxHTTPProcessCookieBytes})
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(target, []*http.Cookie{
		{Name: "locale", Value: "en"},
		{Name: "cookieconsent", Value: "true"},
		{Name: "session_token", Value: "sensitive-session-token"},
	})
	got := jar.Cookies(target)
	if len(got) != 3 {
		t.Fatalf("ordinary cookies must still be retained for transport: %#v", got)
	}
	retained := make(map[string]string, len(got))
	for _, cookie := range got {
		retained[cookie.Name] = cookie.Value
	}
	want := map[string]string{
		"locale":        "en",
		"cookieconsent": "true",
		"session_token": "sensitive-session-token",
	}
	if !reflect.DeepEqual(retained, want) {
		t.Fatalf("cookies retained for transport = %#v, want %#v", retained, want)
	}

	// Structural half of the same contract: neither the jar nor the adapter's
	// result may carry a cookie-value side channel for the engine to register as
	// redaction material.
	if _, exists := reflect.TypeOf(boundedCookieJar{}).FieldByName("currentValues"); exists {
		t.Fatal("cookie jar reintroduced a value side table for the runtime secret matcher")
	}
	for _, field := range []string{"RuntimeCredentialValues", "RuntimeCredentialValuesSnapshot"} {
		if _, exists := reflect.TypeOf(protocol.Result{}).FieldByName(field); exists {
			t.Fatalf("protocol.Result reintroduced the harvested-credential side channel %q", field)
		}
	}
}

func cookieValues(cookies []*http.Cookie) []string {
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		values = append(values, cookie.Value)
	}
	return values
}

func TestBoundedCookieJarMoreThanLegacyRotationLimitKeepsOnlyCurrentValue(t *testing.T) {
	target, _ := url.Parse("https://example.test/app")
	budget := &responseCaptureBudget{limit: maxHTTPProcessCookieBytes}
	jar, err := newBoundedCookieJarWithBudget(budget)
	if err != nil {
		t.Fatal(err)
	}
	const rotations = 640
	for index := 0; index < rotations; index++ {
		value := fmt.Sprintf("rotating-cookie-%04d", index)
		jar.SetCookies(target, []*http.Cookie{{Name: "session", Value: value}})
		if got := jar.Cookies(target); !reflect.DeepEqual(cookieValues(got), []string{value}) {
			t.Fatalf("rotation %d jar contents = %#v, want only current value", index, got)
		}
	}
	if got := jar.Cookies(target); len(got) != 1 ||
		got[0].Value != fmt.Sprintf("rotating-cookie-%04d", rotations-1) {
		t.Fatalf("cookie after rotations = %#v", got)
	}
	if used := budget.used.Load(); used > maxHTTPCookieJarBytes {
		t.Fatalf("rotations retained %d process-budget bytes, limit %d", used, maxHTTPCookieJarBytes)
	}
}

// Cookies accepted across a redirect chain used to be reported back to the
// engine as redaction material. They are now only replayed on the wire, which is
// what the adapter is actually responsible for.
func TestHTTPExecuteAppliesCookiesAcceptedAcrossRedirects(t *testing.T) {
	var sentCookies string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.SetCookie(w, &http.Cookie{Name: "first", Value: "redirect-cookie", Path: "/"})
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			http.SetCookie(w, &http.Cookie{Name: "second", Value: "final-cookie", Path: "/"})
			w.WriteHeader(http.StatusNoContent)
		case "/echo":
			sentCookies = r.Header.Get("Cookie")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runtime := &HTTP{}
	if err := runtime.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Teardown(context.Background())
	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name: "redirect cookies", Method: http.MethodGet, Target: "/start",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("redirect result = %#v", result)
	}
	// Replaces the removed assertions that the result carried
	// RuntimeCredentialValuesSnapshot=true and
	// []string{"final-cookie", "redirect-cookie"}: both cookies accepted across
	// the redirect must be sent on the next request instead.
	if _, err := runtime.Execute(context.Background(), &protocol.Action{
		Name: "reuse redirect cookies", Method: http.MethodGet, Target: "/echo",
	}); err != nil {
		t.Fatal(err)
	}
	if want := "first=redirect-cookie; second=final-cookie"; sentCookies != want {
		t.Fatalf("Cookie header on the next request = %q, want %q", sentCookies, want)
	}
}

func TestBoundedCookieJarLimitsDistinctEntries(t *testing.T) {
	target, _ := url.Parse("https://example.test/")
	budget := &responseCaptureBudget{limit: maxHTTPProcessCookieBytes}
	jar, err := newBoundedCookieJarWithBudget(budget)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxHTTPCookiesPerVUser; i++ {
		jar.SetCookies(target, []*http.Cookie{{Name: fmt.Sprintf("cookie-%d", i), Value: "v"}})
	}
	if got := len(jar.Cookies(target)); got != maxHTTPCookiesPerVUser {
		t.Fatalf("cookie count = %d, want %d", got, maxHTTPCookiesPerVUser)
	}
	_ = jar.clear()
}

func TestHTTPClearCookiesNoJarIsNoop(t *testing.T) {
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": "https://example.test", "cookies_enabled": false}); err != nil {
		t.Fatalf("init error: %v", err)
	}
	if h.client.Jar != nil {
		t.Fatal("expected no cookie jar when cookies are disabled")
	}
	if err := h.ClearCookies(context.Background()); err != nil {
		t.Fatalf("ClearCookies on jar-less client should be a no-op, got: %v", err)
	}
}
