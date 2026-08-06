package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestGitHubSyncSummaryBuilderCollectsEntries(t *testing.T) {
	summary := githubSyncSummaryBuilder{}
	if got := summary.JSON(); got != "" {
		t.Fatalf("expected empty summary when there are no entries, got %q", got)
	}

	summary.addError(githubSyncSummaryItem{Error: "first", Path: "scripts/one.json"})
	summary.addConflict(githubSyncSummaryItem{Error: "second", ScriptID: "id-1"})

	jsonText := summary.JSON()
	if jsonText == "" {
		t.Fatalf("expected non-empty summary JSON")
	}

	parsed := githubSyncSummary{}
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		t.Fatalf("unmarshal summary JSON: %v", err)
	}
	if len(parsed.Errors) != 1 || len(parsed.Conflicts) != 1 {
		t.Fatalf("unexpected summary sections: %#v", parsed)
	}
}

func TestGitHubSyncSummaryBuilderSetPullRequest(t *testing.T) {
	summary := githubSyncSummaryBuilder{}
	summary.addConflict(githubSyncSummaryItem{Error: "retryable conflict"})
	summary.setPullRequest("refs/heads/apexload/bulk-123", "https://example/pull/7", 7)

	jsonText := summary.JSON()
	if jsonText == "" {
		t.Fatalf("expected JSON after setting pull request metadata")
	}

	parsed := githubSyncSummary{}
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		t.Fatalf("unmarshal summary JSON: %v", err)
	}
	if parsed.Strategy != "pr" || parsed.Branch != "refs/heads/apexload/bulk-123" {
		t.Fatalf("expected pull metadata, got %#v", parsed)
	}
	if len(parsed.Conflicts) != 1 {
		t.Fatalf("expected conflict to remain after setting pull metadata, got %#v", parsed)
	}
}

// SummaryJSON is persisted and returned to the dashboard, so a directory import
// with many failing files must not grow the stored blob without bound.
func TestGitHubSyncSummaryBuilderCapsRetainedItems(t *testing.T) {
	summary := githubSyncSummaryBuilder{}
	const total = githubSyncSummaryMaxItems + 25
	for index := 0; index < total; index++ {
		summary.addError(githubSyncSummaryItem{Error: fmt.Sprintf("error %d", index)})
		summary.addConflict(githubSyncSummaryItem{Error: fmt.Sprintf("conflict %d", index)})
	}

	parsed := githubSyncSummary{}
	if err := json.Unmarshal([]byte(summary.JSON()), &parsed); err != nil {
		t.Fatalf("unmarshal summary JSON: %v", err)
	}
	if len(parsed.Errors) != githubSyncSummaryMaxItems || len(parsed.Conflicts) != githubSyncSummaryMaxItems {
		t.Fatalf("retained %d errors and %d conflicts, want %d of each",
			len(parsed.Errors), len(parsed.Conflicts), githubSyncSummaryMaxItems)
	}
	if parsed.ErrorsTruncated != 25 || parsed.ConflictsTruncated != 25 {
		t.Fatalf("truncation counts = %d/%d, want 25/25", parsed.ErrorsTruncated, parsed.ConflictsTruncated)
	}
	// The retained window is the first N, so the overflow is what got dropped.
	if parsed.Errors[0].Error != "error 0" {
		t.Fatalf("first retained error = %q, want the earliest failure", parsed.Errors[0].Error)
	}
}

// Upstream GitHub and storage errors reach this field verbatim, so they must be
// credential-scrubbed and length-bounded before being surfaced.
func TestGitHubSyncSummaryBuilderScrubsAndBoundsErrorText(t *testing.T) {
	summary := githubSyncSummaryBuilder{}
	summary.addError(githubSyncSummaryItem{Error: "put file failed: https://api.github.test/repos?access_token=ghp_supersecretvalue"})
	summary.addConflict(githubSyncSummaryItem{Error: strings.Repeat("x", githubSyncSummaryMaxErrorBytes+500)})

	parsed := githubSyncSummary{}
	if err := json.Unmarshal([]byte(summary.JSON()), &parsed); err != nil {
		t.Fatalf("unmarshal summary JSON: %v", err)
	}
	if strings.Contains(parsed.Errors[0].Error, "ghp_supersecretvalue") {
		t.Fatalf("summary leaked a credential: %q", parsed.Errors[0].Error)
	}
	if len(parsed.Conflicts[0].Error) > githubSyncSummaryMaxErrorBytes+len("… (truncated)") {
		t.Fatalf("conflict error was not bounded: %d bytes", len(parsed.Conflicts[0].Error))
	}
}
