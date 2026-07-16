package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFindCheckRunByExternalIDPaginatesUntilMatch(t *testing.T) {
	pages := []string{}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total_count": 101,
				"check_runs": []map[string]any{{
					"id": 77, "name": ciGitHubCheckName, "external_id": "test-77", "status": "in_progress",
					"app": map[string]any{"id": 42},
				}},
			})
			return
		}
		checks := make([]map[string]any, 100)
		for index := range checks {
			checks[index] = map[string]any{"id": index + 1, "name": ciGitHubCheckName, "external_id": "other"}
		}
		checks[0] = map[string]any{
			"id": 76, "name": ciGitHubCheckName, "external_id": "test-77",
			"status": "completed", "conclusion": "success", "app": map[string]any{"id": 42},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 101, "check_runs": checks})
	}))
	defer stub.Close()
	base, err := url.Parse(stub.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &githubClient{http: stub.Client(), apiBase: stub.URL, appID: 42, allowed: map[string]struct{}{base.Hostname(): {}}}
	check, err := client.FindCheckRunByExternalID(context.Background(), "octo", "apex", "0123456789abcdef0123456789abcdef01234567", ciGitHubCheckName, "test-77")
	if err != nil || check == nil || check.ID != 77 {
		t.Fatalf("paginated check lookup = %#v, %v", check, err)
	}
	if strings.Join(pages, ",") != "1,2" {
		t.Fatalf("requested pages = %#v", pages)
	}
}

func TestFindCheckRunByExternalIDPrefersActiveDuplicate(t *testing.T) {
	for _, test := range []struct {
		name   string
		checks []map[string]any
	}{
		{
			name: "terminal first",
			checks: []map[string]any{
				{"id": 77, "name": ciGitHubCheckName, "external_id": "test-77", "status": "completed", "conclusion": "success", "app": map[string]any{"id": 42}},
				{"id": 88, "name": ciGitHubCheckName, "external_id": "test-77", "status": "in_progress", "app": map[string]any{"id": 42}},
			},
		},
		{
			name: "active first",
			checks: []map[string]any{
				{"id": 88, "name": ciGitHubCheckName, "external_id": "test-77", "status": "queued", "app": map[string]any{"id": 42}},
				{"id": 99, "name": ciGitHubCheckName, "external_id": "test-77", "status": "completed", "conclusion": "failure", "app": map[string]any{"id": 42}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(test.checks), "check_runs": test.checks})
			}))
			defer stub.Close()
			base, err := url.Parse(stub.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := &githubClient{http: stub.Client(), apiBase: stub.URL, appID: 42, allowed: map[string]struct{}{base.Hostname(): {}}}
			check, err := client.FindCheckRunByExternalID(context.Background(), "octo", "apex", "0123456789abcdef0123456789abcdef01234567", ciGitHubCheckName, "test-77")
			if err != nil || check == nil || check.ID != 88 {
				t.Fatalf("active duplicate selection = %#v, %v", check, err)
			}
		})
	}
}

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
