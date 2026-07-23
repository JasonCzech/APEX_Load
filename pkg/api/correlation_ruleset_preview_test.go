package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCorrelationRulesetPreviewTokenIsSharedDeterministicAndStateBound(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "shared-recording-key")
	t.Setenv("APEXLOAD_WRAPPING_MASTER_KEY", "")
	now := time.Date(2026, 7, 22, 12, 0, 0, 123, time.UTC)
	ruleset := &storage.CorrelationRuleset{ID: "rules-1", UpdatedAt: now}
	stored := &StoredScript{
		ID: "script-1",
		Script: &dsl.Script{
			ID:        "script-1",
			UpdatedAt: now.Add(time.Second),
		},
	}
	record := &storage.ScriptRecordingRecord{
		ProjectID:                        "project-1",
		ScriptIncarnation:                "sha256:incarnation-1",
		ActionGraphDigest:                "sha256:graph-1",
		EncryptedCorrelationObservations: "enc:v3:opaque-ciphertext",
		UpdatedAt:                        now.Add(2 * time.Second),
	}
	matched := []paramize.Suggestion{{ID: "correlation-1", LiteralValue: "never-serialize-this-live-token"}}
	diffs := []paramize.Diff{{SuggestionID: "correlation-1", Kind: "extractor_added", Path: "/actions/0/extractors"}}

	first, err := correlationRulesetPreviewToken(ruleset, stored, record, "sha256:graph-1", matched, diffs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := correlationRulesetPreviewToken(ruleset, stored, record, "sha256:graph-1", matched, diffs)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("shared deterministic tokens differ: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "crp1_") || strings.Contains(first, ruleset.ID) || strings.Contains(first, matched[0].LiteralValue) {
		t.Fatalf("preview token is not opaque: %q", first)
	}

	changedRecord := *record
	changedRecord.UpdatedAt = changedRecord.UpdatedAt.Add(time.Nanosecond)
	changed, err := correlationRulesetPreviewToken(ruleset, stored, &changedRecord, "sha256:graph-1", matched, diffs)
	if err != nil {
		t.Fatal(err)
	}
	if correlationRulesetPreviewTokenMatches(first, changed) {
		t.Fatal("recording state change did not invalidate preview token")
	}

	t.Setenv(recordingBaselineKeyEnvVar, "another-replica-key")
	otherKey, err := correlationRulesetPreviewToken(ruleset, stored, record, "sha256:graph-1", matched, diffs)
	if err != nil {
		t.Fatal(err)
	}
	if correlationRulesetPreviewTokenMatches(first, otherKey) {
		t.Fatal("different shared recording key verified the preview token")
	}
}
