package api

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const githubClientTimeout = 30 * time.Second

// githubClientForRequest resolves the per-user GitHub client for the current
// request: it enforces the feature guard, loads the caller's stored connection,
// decrypts the access token, and builds a host-allowlisted client. On any
// failure it writes the appropriate error response and returns ok=false.
func (s *Server) githubClientForRequest(w http.ResponseWriter, r *http.Request) (*githubClient, GitHubSettings, bool) {
	settings := s.currentGitHubSettings()
	if !s.githubFeatureGuard(w, settings) {
		return nil, settings, false
	}
	store, _ := s.githubConnectionStore()
	conn, err := store.GetGitHubConnection(githubUserKey(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read github connection")
		return nil, settings, false
	}
	if conn == nil {
		writeError(w, http.StatusUnauthorized, "github account is not connected")
		return nil, settings, false
	}
	token, err := decryptGitHubToken(conn.AccessTokenEnc)
	if err != nil || strings.TrimSpace(token) == "" {
		writeError(w, http.StatusServiceUnavailable, "stored github token could not be read")
		return nil, settings, false
	}
	client, err := newGitHubClient(settings, token, githubClientTimeout)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return nil, settings, false
	}
	return client, settings, true
}

// writeGitHubUpstreamError maps a GitHub API error to an HTTP response, passing
// through 401/403/404 so callers can react (e.g. re-auth) while collapsing other
// upstream failures into 502.
func writeGitHubUpstreamError(w http.ResponseWriter, err error) {
	if apiErr, ok := err.(*githubAPIError); ok {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			writeError(w, http.StatusUnauthorized, "github rejected the stored token; reconnect your account")
			return
		case http.StatusForbidden:
			writeError(w, http.StatusForbidden, "github denied access to this resource")
			return
		case http.StatusNotFound:
			writeError(w, http.StatusNotFound, "github resource not found")
			return
		}
	}
	writeError(w, http.StatusBadGateway, "github request failed")
}

func (s *Server) handleGitHubRepos(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	repos, err := client.ListRepos(r.Context())
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(repos))
	for _, repo := range repos {
		out = append(out, map[string]any{
			"name":           repo.Name,
			"full_name":      repo.FullName,
			"owner":          repo.Owner.Login,
			"private":        repo.Private,
			"default_branch": repo.DefaultBranch,
			"html_url":       repo.HTMLURL,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": out})
}

func (s *Server) handleGitHubBranches(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if owner == "" || repo == "" {
		writeError(w, http.StatusBadRequest, "owner and repo query parameters are required")
		return
	}
	client, _, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	branches, err := client.ListBranches(r.Context(), owner, repo)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(branches))
	for _, branch := range branches {
		out = append(out, map[string]any{"name": branch.Name, "commit_sha": branch.Commit.SHA})
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": out})
}

func (s *Server) handleGitHubContents(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if owner == "" || repo == "" {
		writeError(w, http.StatusBadRequest, "owner and repo query parameters are required")
		return
	}
	contentPath := strings.TrimSpace(r.URL.Query().Get("path"))
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	client, _, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	contents, err := client.GetContents(r.Context(), owner, repo, contentPath, ref)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	if contents.IsDir {
		entries := make([]map[string]any, 0, len(contents.Entries))
		for _, entry := range contents.Entries {
			entries = append(entries, map[string]any{
				"type": entry.Type,
				"name": entry.Name,
				"path": entry.Path,
				"sha":  entry.SHA,
				"size": entry.Size,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"type": "dir", "path": contentPath, "entries": entries})
		return
	}
	file := contents.File
	writeJSON(w, http.StatusOK, map[string]any{
		"type":     "file",
		"name":     file.Name,
		"path":     file.Path,
		"sha":      file.SHA,
		"size":     file.Size,
		"html_url": file.HTMLURL,
	})
}

type githubImportRequest struct {
	ProjectID string `json:"project_id"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Ref       string `json:"ref,omitempty"`
	Name      string `json:"name,omitempty"`
}

// handleImportGitHub pulls a single script file from a GitHub repository, parses
// and validates it as DSL, and returns a standard import-review response (no save
// — the dashboard saves through the existing create/update flow after review).
func (s *Server) handleImportGitHub(w http.ResponseWriter, r *http.Request) {
	var req githubImportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Owner = strings.TrimSpace(req.Owner)
	req.Repo = strings.TrimSpace(req.Repo)
	req.Path = strings.TrimSpace(req.Path)
	if req.Owner == "" || req.Repo == "" || req.Path == "" {
		writeError(w, http.StatusBadRequest, "owner, repo, and path are required")
		return
	}

	client, _, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	contents, err := client.GetContents(r.Context(), req.Owner, req.Repo, req.Path, req.Ref)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	if contents.IsDir || contents.File == nil {
		writeError(w, http.StatusBadRequest, "the selected github path is a directory, not a script file")
		return
	}
	data, err := contents.File.DecodedContent()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to decode github file content")
		return
	}

	script, err := dsl.Parse(data)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "github file is not a valid script: "+err.Error())
		return
	}
	script.ProjectID = normalizeProjectID(req.ProjectID)

	sourceName := strings.TrimSpace(req.Name)
	if sourceName == "" {
		sourceName = strings.TrimSuffix(path.Base(req.Path), path.Ext(req.Path))
	}
	branch := strings.TrimSpace(req.Ref)
	if branch == "" {
		branch = "(default)"
	}
	review := map[string]any{
		"owner":      req.Owner,
		"repo":       req.Repo,
		"branch":     branch,
		"path":       contents.File.Path,
		"commit_sha": contents.File.SHA,
		"html_url":   contents.File.HTMLURL,
	}
	attachSourceReviewToScript(script, "github", sourceName, review)

	var warnings []string
	if issues := validateScript(script); len(issues) > 0 {
		warnings = append(warnings, issues...)
	}

	s.recordAuditEvent(r, "github.script.import", "success", "script", script.Name, script.ProjectID, map[string]any{
		"owner": req.Owner, "repo": req.Repo, "path": contents.File.Path,
	})
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Script:   script,
		Warnings: warnings,
		Summary: map[string]any{
			"owner":      req.Owner,
			"repo":       req.Repo,
			"branch":     branch,
			"path":       contents.File.Path,
			"commit_sha": contents.File.SHA,
		},
		ReviewSections: []scriptImportReviewSection{{
			Key:      "github_source",
			Title:    "GitHub Source",
			Severity: "info",
			Summary:  "Imported from a GitHub repository. Review and save to add it to your library.",
			Items: []string{
				req.Owner + "/" + req.Repo + " @ " + branch,
				contents.File.Path,
				"commit " + shortGitHubSHA(contents.File.SHA),
			},
		}},
		SourceReview: review,
	}))
}

func shortGitHubSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
