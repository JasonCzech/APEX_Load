package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAzureKeyVaultResolverRefusesRedirects(t *testing.T) {
	var redirectedRequests atomic.Int64
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("redirect target received Authorization header")
		}
		_, _ = w.Write([]byte(`{"value":"stolen"}`))
	}))
	defer redirectTarget.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/secret", http.StatusFound)
	}))
	defer source.Close()

	resolver := &azureKeyVaultSecretResolver{
		baseURL: source.URL, tokens: staticAzureTokenSource("test-token"), apiVersion: "7.4", client: source.Client(),
	}
	if value, ok := resolver.Resolve("db-password"); ok || value != "" {
		t.Fatalf("Resolve = %q/%v, want redirect rejection", value, ok)
	}
	if redirectedRequests.Load() != 0 {
		t.Fatalf("redirect target requests = %d, want zero", redirectedRequests.Load())
	}
}

func TestAzureKeyVaultResolverBoundsResponseAndValue(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "response", body: strings.Repeat("x", maxAzureKeyVaultResponseBytes+1)},
		{name: "value", body: `{"value":"` + strings.Repeat("x", maxResolvedSecretValueBytes+1) + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			resolver := &azureKeyVaultSecretResolver{
				baseURL: server.URL, tokens: staticAzureTokenSource("test-token"), apiVersion: "7.4", client: server.Client(),
			}
			if value, ok := resolver.Resolve("db-password"); ok || value != "" {
				t.Fatalf("Resolve = value bytes %d/%v, want bounded rejection", len(value), ok)
			}
		})
	}
}

func TestValidateAzureKeyVaultBaseURL(t *testing.T) {
	if got, err := validateAzureKeyVaultBaseURL("https://vault.example.test/"); err != nil || got != "https://vault.example.test" {
		t.Fatalf("valid URL = %q, %v", got, err)
	}
	for _, raw := range []string{
		"http://vault.example.test",
		"https://user:password@vault.example.test",
		"https://vault.example.test/path",
		"https://vault.example.test?token=secret",
	} {
		if _, err := validateAzureKeyVaultBaseURL(raw); err == nil {
			t.Errorf("validateAzureKeyVaultBaseURL(%q) returned nil", raw)
		}
	}
}

func TestCachingSecretResolverBoundsNamesValuesAndEntries(t *testing.T) {
	var calls atomic.Int64
	resolver := newCachingSecretResolver(SecretResolverFunc(func(name string) (string, bool) {
		calls.Add(1)
		if name == "oversized-value" {
			return strings.Repeat("x", maxResolvedSecretValueBytes+1), true
		}
		return "value", true
	})).(*cachingSecretResolver)

	if _, ok := resolver.Resolve(strings.Repeat("n", maxSecretNameBytes+1)); ok || calls.Load() != 0 {
		t.Fatalf("oversized name reached delegate: ok=%v calls=%d", ok, calls.Load())
	}
	if value, ok := resolver.Resolve("oversized-value"); ok || value != "" {
		t.Fatalf("oversized value = bytes %d/%v, want rejection", len(value), ok)
	}
	for index := 0; index < maxCachedSecrets+100; index++ {
		if _, ok := resolver.Resolve(fmt.Sprintf("secret-%04d", index)); !ok {
			t.Fatalf("Resolve(%d) returned false", index)
		}
	}
	resolver.mu.Lock()
	entries := len(resolver.values) + len(resolver.missing)
	bytes := resolver.bytes
	resolver.mu.Unlock()
	if entries > maxCachedSecrets || bytes > maxCachedSecretBytes {
		t.Fatalf("cache retained entries=%d bytes=%d", entries, bytes)
	}
}

func TestCachingSecretResolverRetriesNegativeResultAfterTTL(t *testing.T) {
	var calls atomic.Int64
	available := false
	resolver := newCachingSecretResolver(SecretResolverFunc(func(string) (string, bool) {
		calls.Add(1)
		if !available {
			return "", false
		}
		return "recovered", true
	})).(*cachingSecretResolver)
	now := time.Unix(1_700_000_000, 0)
	resolver.now = func() time.Time { return now }
	resolver.negativeTTL = time.Second

	if _, ok := resolver.Resolve("transient"); ok {
		t.Fatal("initial transient miss unexpectedly resolved")
	}
	available = true
	if _, ok := resolver.Resolve("transient"); ok || calls.Load() != 1 {
		t.Fatalf("negative cache was not honored before expiry: ok=%v calls=%d", ok, calls.Load())
	}
	now = now.Add(time.Second)
	if value, ok := resolver.Resolve("transient"); !ok || value != "recovered" {
		t.Fatalf("post-TTL resolution = %q/%v, want recovered", value, ok)
	}
	if calls.Load() != 2 {
		t.Fatalf("delegate calls = %d, want 2", calls.Load())
	}
}

func TestCachingSecretResolverAcceptsNilContext(t *testing.T) {
	resolver := newCachingSecretResolver(SecretResolverFunc(func(name string) (string, bool) {
		return "value-for-" + name, true
	})).(*cachingSecretResolver)
	value, ok := resolver.ResolveContext(nil, "TOKEN") //nolint:staticcheck // the nil context is the input under test
	if !ok || value != "value-for-TOKEN" {
		t.Fatalf("ResolveContext(nil) = %q/%v", value, ok)
	}
}

func TestProjectScopedSecretsDoNotCrossProjectsOrFallbackToGlobal(t *testing.T) {
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "")
	t.Setenv("APEXLOAD_SECRET_DB_PASSWORD", "legacy-global")
	t.Setenv(ProjectSecretEnvironmentName("project-a", "db.password"), "project-a-value")

	projectA := NewProjectSecretResolver("project-a")
	if value, ok := projectA.Resolve("db.password"); !ok || value != "project-a-value" {
		t.Fatalf("project A resolution = %q/%v", value, ok)
	}
	projectB := newDefaultSecretResolverForProject("project-b")
	if value, ok := projectB.Resolve("db.password"); ok || value != "" {
		t.Fatalf("project B crossed namespace: %q/%v", value, ok)
	}
	legacy := newDefaultSecretResolverForProject("")
	if value, ok := legacy.Resolve("db.password"); !ok || value != "legacy-global" {
		t.Fatalf("projectless legacy resolution = %q/%v", value, ok)
	}
	if ProjectSecretBindingName("project-a", "db.password") == ProjectSecretBindingName("project-b", "db.password") {
		t.Fatal("distinct projects produced the same secret binding")
	}
}

func TestEngineUsesScriptProjectSecretNamespace(t *testing.T) {
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "")
	t.Setenv("APEXLOAD_SECRET_API_TOKEN", "legacy-global")
	t.Setenv(ProjectSecretEnvironmentName("project-a", "api.token"), "project-value")
	script := &dsl.Script{ProjectID: "project-a", Protocol: "http"}
	eng, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if value, ok := eng.secrets.Resolve("api.token"); !ok || value != "project-value" {
		t.Fatalf("engine resolution = %q/%v", value, ok)
	}
}

func TestAzureKeyVaultResolverRejectsInvalidSecretNameBeforeRequest(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"value":"secret"}`))
	}))
	defer server.Close()
	resolver := &azureKeyVaultSecretResolver{
		baseURL: server.URL, tokens: staticAzureTokenSource("test-token"), apiVersion: "7.4", client: server.Client(),
	}
	if _, ok := resolver.Resolve("../../metadata"); ok || requests.Load() != 0 {
		t.Fatalf("invalid secret name was requested: ok=%v requests=%d", ok, requests.Load())
	}
}
