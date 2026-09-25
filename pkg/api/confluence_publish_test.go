package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestConfluencePageBodyEscapesReportData(t *testing.T) {
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: "run-1", TestName: "Checkout <script>", Status: "FAILED"}, Verdict: &ReportVerdict{Status: "failed", Explanation: "<img src=x>"}, ByTransaction: map[string]RunArchiveActionStats{"<danger>": {Transactions: 4}}}
	body := confluencePageBody(report, "https://apex.example.test/report?a=1&b=2")
	for _, unsafe := range []string{"<script>", "<img src=x>", "<danger>", `a=1&b=2`} {
		if strings.Contains(body, unsafe) {
			t.Errorf("unescaped content %q in %s", unsafe, body)
		}
	}
	for _, safe := range []string{"&lt;img src=x&gt;", "&lt;danger&gt;", "a=1&amp;b=2"} {
		if !strings.Contains(body, safe) {
			t.Errorf("escaped content %q missing", safe)
		}
	}
	if !strings.Contains(confluencePageTitle(report, "run-1"), "run-1") {
		t.Fatal("page title lacks stable run ID")
	}
}

func TestConfluenceDestinationChecksParentAndSpace(t *testing.T) {
	space := "42"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/100" {
			t.Errorf("unexpected destination request %s", r.URL.Path)
		}
		writeJSON(w, 200, confluencePage{ID: "100", SpaceID: space})
	}))
	defer server.Close()
	c := &storage.ConfluenceConnection{SiteURL: server.URL, SpaceID: "42", ParentPageID: "100"}
	if err := checkConfluenceDestinationPage(context.Background(), server.Client(), c, "token"); err != nil {
		t.Fatalf("valid destination: %v", err)
	}
	space = "99"
	if err := checkConfluenceDestinationPage(context.Background(), server.Client(), c, "token"); err == nil {
		t.Fatal("parent page in another space was accepted")
	}
}

func TestConfluenceCreateUncertainResponseReconcilesAndUpdates(t *testing.T) {
	var posts, puts int
	var stored confluencePage
	var lastBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "writer@example.com" || pass != "token" {
			t.Errorf("missing basic authentication")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/wiki/api/v2/pages?"):
			t.Fatal("query is in RawQuery, not URL.Path")
		case r.Method == "GET" && r.URL.Path == "/wiki/api/v2/pages":
			if stored.ID == "" {
				_, _ = w.Write([]byte(`{"results":[]}`))
			} else {
				writeJSON(w, 200, map[string]any{"results": []confluencePage{stored}})
			}
		case r.Method == "POST" && r.URL.Path == "/wiki/api/v2/pages":
			posts++
			var payload struct {
				Title string `json:"title"`
				Body  struct {
					Value string `json:"value"`
				} `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			lastBody = payload.Body.Value
			stored = confluencePage{ID: "200", Title: payload.Title, SpaceID: "42", ParentID: "100"}
			stored.Version.Number = 1
			w.WriteHeader(502) // Remote create committed but response was ambiguous.
		case r.Method == "PUT" && r.URL.Path == "/wiki/api/v2/pages/200":
			puts++
			var payload struct {
				Version struct {
					Number int `json:"number"`
				} `json:"version"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.Version.Number != 2 {
				t.Errorf("update version=%d", payload.Version.Number)
			}
			stored.Version.Number = 2
			writeJSON(w, 200, stored)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := &storage.ConfluenceConnection{SiteURL: server.URL, Email: "writer@example.com", SpaceID: "42", ParentPageID: "100"}
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: "run-1", TestName: "Checkout", Status: "COMPLETE"}}
	if _, _, err := publishConfluencePage(context.Background(), server.Client(), c, "token", nil, report, "https://apex.test/#/report/run-1"); err == nil {
		t.Fatal("ambiguous create reported success")
	}
	id, link, err := publishConfluencePage(context.Background(), server.Client(), c, "token", nil, report, "https://apex.test/#/report/run-1")
	if err != nil || id != "200" || !strings.Contains(link, "/pages/200") {
		t.Fatalf("reconcile: %q %q %v", id, link, err)
	}
	if posts != 1 || puts != 1 {
		t.Fatalf("posts=%d puts=%d", posts, puts)
	}
	if !strings.Contains(lastBody, "Open full report") {
		t.Fatalf("page body missing report link: %q", lastBody)
	}
}

func TestConfluenceConnectionResponseOmitsSecretReference(t *testing.T) {
	connection := &storage.ConfluenceConnection{ID: "c", ProjectID: "p", Enabled: true, TokenSecretName: "private-secret-name", LastCheckStatus: "error"}
	encoded, _ := json.Marshal(confluenceConnectionView(connection))
	if strings.Contains(string(encoded), "private-secret-name") || !strings.Contains(string(encoded), `"has_token":true`) || !strings.Contains(string(encoded), `"status":"error"`) {
		t.Fatalf("unsafe response: %s", encoded)
	}
}

func TestConfluenceAutomaticReportURLRequiresValidPublicBase(t *testing.T) {
	t.Setenv("APEXLOAD_PUBLIC_URL", "")
	if _, err := confluenceReportURL(nil, "run-1"); err == nil {
		t.Fatal("automatic report URL accepted without public base")
	}
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://user:pass@example.test")
	if _, err := confluenceReportURL(nil, "run-1"); err == nil {
		t.Fatal("credential-bearing public URL accepted")
	}
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	got, err := confluenceReportURL(nil, "run-1")
	if err != nil || got != "https://apex.example.test/#/report/run-1" {
		t.Fatalf("report URL = %q, %v", got, err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://apex.example.test/api/v1/tests/run-1/publish/confluence", nil)
	if got, err := confluenceReportURL(req, "run-1"); err != nil || got != "https://apex.example.test/#/report/run-1" {
		t.Fatalf("manual report URL with public base = %q, %v", got, err)
	}
	t.Setenv("APEXLOAD_PUBLIC_URL", "")
	if got, err := confluenceReportURL(req, "run-1"); err != nil || got != "http://apex.example.test/#/report/run-1" {
		t.Fatalf("manual report URL from request = %q, %v", got, err)
	}
}

func TestConfluenceTokenResolvesFromProjectSecret(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "false")
	secrets := &devSecretStore{value: "secret-token"}
	s := &Server{runner: &TestRunner{projectSecretStore: secrets}}
	token, err := s.confluenceToken(context.Background(), &storage.ConfluenceConnection{ProjectID: "project-a", TokenSecretName: "wiki-token"})
	if err != nil || token != "secret-token" || secrets.calls != 1 {
		t.Fatalf("resolved token = %q, calls=%d, err=%v", token, secrets.calls, err)
	}
}

func TestConfluenceAutomaticJobFollowsDurableArchiveFinalization(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	connection := &storage.ConfluenceConnection{ID: "auto-wiki", ProjectID: storage.DefaultProjectID, Name: "Wiki", Enabled: true, SiteURL: "https://example.atlassian.net", Email: "writer@example.com", SpaceID: "42", ParentPageID: "100", TokenSecretName: "token", AutoPublish: true}
	if err := store.CreateConfluenceConnection(connection); err != nil {
		t.Fatal(err)
	}
	test, record := createArchiveFinalizationTest(t, store, "confluence-auto-run", archiveRoot)
	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err != nil {
		t.Fatal(err)
	}
	queued, err := store.ListPendingConfluencePublications(10)
	if err != nil || len(queued) != 1 || queued[0].TestID != test.ID {
		t.Fatalf("automatic publication intent: %v, %v", queued, err)
	}
}
