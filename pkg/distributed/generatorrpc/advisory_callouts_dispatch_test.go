package generatorrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// legacyGeneratorExecutionBlockers is the gate a generator built before the
// encrypted proxy callout became advisory applies (engine
// executionBlockerCodes on origin/main): every non-empty execution_blocker(s)
// code blocks the assignment.
func legacyGeneratorExecutionBlockers(t *testing.T, script *dsl.Script) []string {
	t.Helper()
	var blocked []string
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		metadata := ref.Metadata()
		codes := dslwalk.MetadataStrings(metadata, "execution_blockers")
		if code := dslwalk.MetadataString(metadata, "execution_blocker"); code != "" {
			codes = append(codes, code)
		}
		for _, code := range codes {
			blocked = append(blocked, ref.Path+" "+code)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(blocked)
	return slices.Compact(blocked)
}

// A script stored before the encrypted proxy callout became advisory carries
// it as execution_blocker metadata. The controller rewrites the copy it
// dispatches so a generator that predates the advisory rule does not refuse
// the assignment, while every real blocker and the stored script stay as
// they were.
func TestPrepareDistributedScriptMovesLegacyAdvisoryBlockers(t *testing.T) {
	legacyCallout := func() map[string]any {
		return map[string]any{
			"execution_blocker":  dsl.EncryptedProxyPasswordFindingCode,
			"execution_blockers": []any{dsl.EncryptedProxyPasswordFindingCode},
			"import_review":      map[string]any{"reason_codes": []any{dsl.EncryptedProxyPasswordFindingCode}, "resolution": "replace_proxy_secret"},
		}
	}
	script := &dsl.Script{
		Name: "legacy proxy", Version: "1", Protocol: "http",
		Init: []dsl.Action{{Name: "Restore proxy credential", Type: "source_review", Metadata: legacyCallout()}},
		Actions: []dsl.Action{
			{Name: "mixed", Type: "source_review", Metadata: map[string]any{
				"execution_blocker":  dsl.EncryptedProxyPasswordFindingCode,
				"execution_blockers": []any{dsl.EncryptedProxyPasswordFindingCode, "legacy_tls_replacement"},
			}},
			{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{
				map[string]any{"name": "child", "method": "GET", "target": "https://example.test/", "metadata": legacyCallout()},
			}}},
		},
	}
	if blocked := legacyGeneratorExecutionBlockers(t, script); len(blocked) != 4 {
		t.Fatalf("fixture legacy blockers = %v", blocked)
	}

	content, hash, _, err := PrepareDistributedScript(script)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if hash != hex.EncodeToString(digest[:]) {
		t.Fatal("content hash does not describe the rewritten content")
	}
	var staged dsl.Script
	if err := json.Unmarshal(content, &staged); err != nil {
		t.Fatal(err)
	}
	if blocked := legacyGeneratorExecutionBlockers(t, &staged); !slices.Equal(blocked, []string{"actions[0] legacy_tls_replacement"}) {
		t.Fatalf("dispatched legacy blockers = %v, want only the real blocker", blocked)
	}
	advisory := map[string][]string{}
	if err := dslwalk.Walk(&staged, func(ref dslwalk.SurfaceRef) error {
		if codes := dslwalk.MetadataStrings(ref.Metadata(), dsl.AdvisoryCalloutsMetadataKey); len(codes) > 0 {
			advisory[ref.Path] = codes
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"init[0]", "actions[0]", "actions[1].params.requests[0]"} {
		if !slices.Equal(advisory[path], []string{dsl.EncryptedProxyPasswordFindingCode}) {
			t.Fatalf("dispatched callouts = %v, want the callout kept at %s", advisory, path)
		}
	}
	if !strings.Contains(string(content), "replace_proxy_secret") {
		t.Fatal("dispatched callout lost its import_review provenance")
	}
	// The stored script keeps its shape for guided repairs and fingerprints.
	if blocked := legacyGeneratorExecutionBlockers(t, script); len(blocked) != 4 {
		t.Fatalf("stored script changed: %v", blocked)
	}

	// The rewrite is deterministic, so every dispatch path (start,
	// evacuation, source-plan hashing) agrees on the content hash.
	_, again, _, err := PrepareDistributedScript(script)
	if err != nil || again != hash {
		t.Fatalf("second staging hash = %q err=%v, want %q", again, err, hash)
	}
}
