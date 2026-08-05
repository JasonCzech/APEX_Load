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
	if got := jar.runtimeCredentialValuesSnapshot(); !reflect.DeepEqual(got, []string{"small"}) {
		t.Fatalf("accepted runtime credential values = %#v", got)
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

func TestBoundedCookieJarSnapshotsAcceptedReplacementsWithoutGrowingHistory(t *testing.T) {
	target, _ := url.Parse("https://example.test/app")
	budget := &responseCaptureBudget{limit: maxHTTPProcessCookieBytes}
	jar, err := newBoundedCookieJarWithBudget(budget)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "opaque", Value: "first-cookie-value"}})
	if got := jar.runtimeCredentialValuesSnapshot(); !reflect.DeepEqual(got, []string{"first-cookie-value"}) {
		t.Fatalf("first accepted values = %#v", got)
	}
	jar.SetCookies(target, []*http.Cookie{
		{Name: "opaque", Value: "second-cookie-value"},
		{Name: "opaque", Value: "second-cookie-value"},
	})
	if got := jar.runtimeCredentialValuesSnapshot(); !reflect.DeepEqual(got, []string{"second-cookie-value"}) {
		t.Fatalf("replacement accepted values = %#v", got)
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "opaque", Value: "", MaxAge: -1}})
	if got := jar.runtimeCredentialValuesSnapshot(); !reflect.DeepEqual(got, []string{"second-cookie-value"}) {
		t.Fatalf("conservative deleted-cookie snapshot = %#v, want prior bounded value", got)
	}
	if got := jar.Cookies(target); len(got) != 0 {
		t.Fatalf("cookie deletion left jar values = %#v", got)
	}

	constrainedBudget := &responseCaptureBudget{limit: 1}
	constrained, err := newBoundedCookieJarWithBudget(constrainedBudget)
	if err != nil {
		t.Fatal(err)
	}
	constrained.SetCookies(target, []*http.Cookie{{Name: "rejected", Value: "not-retained"}})
	if got := constrained.runtimeCredentialValuesSnapshot(); len(got) != 0 {
		t.Fatalf("rejected cookie produced runtime credential values = %#v", got)
	}
}

func TestBoundedCookieJarRuntimeSnapshotProtectsShortApplicationDefinedCookies(t *testing.T) {
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
	if got := jar.runtimeCredentialValuesSnapshot(); !reflect.DeepEqual(got, []string{"en", "sensitive-session-token", "true"}) {
		t.Fatalf("runtime credential snapshot = %#v", got)
	}
	if got := jar.Cookies(target); len(got) != 3 {
		t.Fatalf("ordinary cookies must still be retained for transport: %#v", got)
	}
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
		if got := jar.runtimeCredentialValuesSnapshot(); !reflect.DeepEqual(got, []string{value}) {
			t.Fatalf("rotation %d snapshot = %#v, want only current value", index, got)
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

func TestHTTPExecuteReportsCookiesAcceptedAcrossRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.SetCookie(w, &http.Cookie{Name: "first", Value: "redirect-cookie", Path: "/"})
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			http.SetCookie(w, &http.Cookie{Name: "second", Value: "final-cookie", Path: "/"})
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
	if !result.RuntimeCredentialValuesSnapshot {
		t.Fatal("redirect cookie values were not marked as a complete snapshot")
	}
	if !reflect.DeepEqual(result.RuntimeCredentialValues, []string{"final-cookie", "redirect-cookie"}) {
		t.Fatalf("redirect runtime credential values = %#v", result.RuntimeCredentialValues)
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
