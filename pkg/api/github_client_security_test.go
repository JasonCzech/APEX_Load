package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGitHubClientRejectsOversizedJSONResponse(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":"`))
		_, _ = w.Write([]byte(strings.Repeat("x", githubRemoteJSONMaxBytes+1)))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer stub.Close()
	base, err := url.Parse(stub.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &githubClient{
		http:    stub.Client(),
		apiBase: stub.URL,
		allowed: map[string]struct{}{base.Hostname(): {}},
	}
	var response map[string]any
	err = client.doJSON(context.Background(), http.MethodGet, "/oversized", nil, nil, &response)
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("oversized GitHub response error = %v, want bounded-response error", err)
	}
}

func TestGitHubClientRejectsUnsafePathCoordinatesBeforeRequest(t *testing.T) {
	client := &githubClient{}
	for _, test := range []struct {
		name  string
		owner string
		repo  string
		path  string
	}{
		{name: "owner traversal", owner: "..", repo: "scripts", path: "safe.json"},
		{name: "repo traversal", owner: "octo", repo: "../admin", path: "safe.json"},
		{name: "content traversal", owner: "octo", repo: "scripts", path: "allowed/../../.github/workflows/pwn.yml"},
		{name: "backslash traversal", owner: "octo", repo: "scripts", path: `allowed\..\secret.json`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.GetContents(context.Background(), test.owner, test.repo, test.path, "main"); err == nil {
				t.Fatal("unsafe GitHub coordinates reached the HTTP client")
			}
		})
	}
}
