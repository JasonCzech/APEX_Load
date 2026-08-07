package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// githubRenewFailureStore keeps the real acquire/release/finalize behaviour but
// forces claim renewal to fail, which is the path that previously bumped
// job.Errors / job.Conflicts without recording any reason.
type githubRenewFailureStore struct {
	storage.GitHubScriptLinkStore
	storage.GitHubScriptSyncClaimStore
	renewErr error
}

func (s githubRenewFailureStore) RenewGitHubScriptSyncClaim(string, string, time.Time) (bool, error) {
	if s.renewErr != nil {
		return false, s.renewErr
	}
	return false, nil
}

func parseGitHubSyncSummary(t *testing.T, job *storage.GitHubSyncJob) githubSyncSummary {
	t.Helper()
	if strings.TrimSpace(job.SummaryJSON) == "" {
		t.Fatalf("job recorded errors=%d conflicts=%d but wrote no summary; the dashboard would show a bare counter",
			job.Errors, job.Conflicts)
	}
	var summary githubSyncSummary
	if err := json.Unmarshal([]byte(job.SummaryJSON), &summary); err != nil {
		t.Fatalf("decode summary %q: %v", job.SummaryJSON, err)
	}
	return summary
}

func runGitHubPushWithClaimStore(
	t *testing.T,
	linkStore storage.GitHubScriptLinkStore,
	server *Server,
	scriptID string,
) *storage.GitHubSyncJob {
	t.Helper()
	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	job := &storage.GitHubSyncJob{
		ID: "job-renew", ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main",
	}
	newGitHubSyncService(server.runner, nil, linkStore).runPush(
		context.Background(),
		&githubResolvedClient{Client: client, Settings: settings},
		job,
		[]string{scriptID},
		"commit",
	)
	return job
}

// TestGitHubPushClaimRenewalErrorRecordsReason pins that a failed claim renewal
// no longer degrades the job to a bare Errors counter.
func TestGitHubPushClaimRenewalErrorRecordsReason(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)

	linkStore := githubRenewFailureStore{
		GitHubScriptLinkStore:      server.store.(storage.GitHubScriptLinkStore),
		GitHubScriptSyncClaimStore: server.store.(storage.GitHubScriptSyncClaimStore),
		renewErr:                   errors.New("claim table is unavailable"),
	}
	job := runGitHubPushWithClaimStore(t, linkStore, server, stored.ID)

	if job.Errors != 1 || job.Updated != 0 {
		t.Fatalf("job = %#v, want a single renewal error and no writes", job)
	}
	summary := parseGitHubSyncSummary(t, job)
	if len(summary.Errors) != 1 {
		t.Fatalf("summary.Errors = %#v, want one attributed failure", summary.Errors)
	}
	issue := summary.Errors[0]
	if !strings.Contains(issue.Error, "renew github sync claim") {
		t.Fatalf("error reason = %q, want the renewal cause", issue.Error)
	}
	if issue.ScriptID != stored.ID {
		t.Fatalf("error script_id = %q, want %q", issue.ScriptID, stored.ID)
	}
	if strings.TrimSpace(issue.TargetPath) == "" {
		t.Fatalf("error target_path is empty; the operator cannot tell which file failed: %#v", issue)
	}
}

// TestGitHubPushClaimExpiryRecordsReason covers the non-error branch: the claim
// simply lapsed, which is a conflict rather than a failure.
func TestGitHubPushClaimExpiryRecordsReason(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)

	linkStore := githubRenewFailureStore{
		GitHubScriptLinkStore:      server.store.(storage.GitHubScriptLinkStore),
		GitHubScriptSyncClaimStore: server.store.(storage.GitHubScriptSyncClaimStore),
	}
	job := runGitHubPushWithClaimStore(t, linkStore, server, stored.ID)

	if job.Conflicts != 1 || job.Errors != 0 || job.Updated != 0 {
		t.Fatalf("job = %#v, want a single attributed claim-expiry conflict", job)
	}
	summary := parseGitHubSyncSummary(t, job)
	if len(summary.Conflicts) != 1 {
		t.Fatalf("summary.Conflicts = %#v, want one attributed conflict", summary.Conflicts)
	}
	issue := summary.Conflicts[0]
	if !strings.Contains(issue.Error, "claim expired") {
		t.Fatalf("conflict reason = %q, want the claim-expiry cause", issue.Error)
	}
	if issue.ScriptID != stored.ID {
		t.Fatalf("conflict script_id = %q, want %q", issue.ScriptID, stored.ID)
	}
}

// TestGitHubSyncUnpushableScriptIssueExplainsRejection covers the candidate
// filter that used to bump job.Conflicts with no attribution at all.
func TestGitHubSyncUnpushableScriptIssueExplainsRejection(t *testing.T) {
	cases := []struct {
		name         string
		stored       *StoredScript
		wantContains string
		wantScriptID string
	}{
		{
			name:         "nil entry",
			stored:       nil,
			wantContains: "unreadable script entry",
		},
		{
			name:         "missing definition",
			stored:       &StoredScript{ID: "script-1"},
			wantContains: "no stored definition",
			wantScriptID: "script-1",
		},
		{
			name:         "foreign project",
			stored:       &StoredScript{ID: "script-2", Script: nonNilGitHubTestScript()},
			wantContains: "not owned by the synchronized project",
			wantScriptID: "script-2",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			issue := githubSyncUnpushableScriptIssue(testCase.stored)
			if !strings.Contains(issue.Error, testCase.wantContains) {
				t.Fatalf("reason = %q, want it to contain %q", issue.Error, testCase.wantContains)
			}
			if issue.ScriptID != testCase.wantScriptID {
				t.Fatalf("script_id = %q, want %q", issue.ScriptID, testCase.wantScriptID)
			}
		})
	}
}

func TestGitHubSyncJobLogReasonsReadsBackLeadingCauses(t *testing.T) {
	builder := githubSyncSummaryBuilder{}
	builder.addError(githubSyncSummaryItem{Error: "read github import script file", Path: "a.json"})
	builder.addConflict(githubSyncSummaryItem{Error: "duplicate github import script id", Path: "b.json"})

	firstError, firstConflict := githubSyncJobLogReasons(builder.JSON())
	if firstError != "read github import script file" {
		t.Fatalf("first_error = %q", firstError)
	}
	if firstConflict != "duplicate github import script id" {
		t.Fatalf("first_conflict = %q", firstConflict)
	}

	if gotError, gotConflict := githubSyncJobLogReasons(""); gotError != "" || gotConflict != "" {
		t.Fatalf("empty summary reasons = %q/%q, want empty", gotError, gotConflict)
	}
	if gotError, gotConflict := githubSyncJobLogReasons("{not json"); gotError != "" || gotConflict != "" {
		t.Fatalf("malformed summary reasons = %q/%q, want empty", gotError, gotConflict)
	}
}

func nonNilGitHubTestScript() *dsl.Script {
	return &dsl.Script{Name: "foreign", Version: "1", Protocol: "http", ProjectID: "team-b"}
}
