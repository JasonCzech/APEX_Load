package api

import (
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importsession"
)

// TestImportProvenanceEvidenceKeysAreNotPortableIdentifiers is the reason the
// two sets are separate.
//
// Both classes are exempt from secret-binding discovery, which makes the lists
// look like they ought to be identical — and a well-meaning "fix the drift"
// change that merged them would start projecting original_url to clients as a
// portable identifier. original_url is captured raw review evidence: it
// routinely carries query credentials and URL userinfo. If this test fails
// because a key moved from evidence to identifier, that key's values must first
// be proven credential-free.
func TestImportProvenanceEvidenceKeysAreNotPortableIdentifiers(t *testing.T) {
	if len(importProvenanceEvidenceKeys) == 0 {
		t.Fatal("no provenance evidence keys are declared")
	}
	for key := range importProvenanceEvidenceKeys {
		if isPortableImportProvenanceIdentifierKey(key) {
			t.Fatalf("provenance evidence key %q is treated as a portable identifier", key)
		}
		if !isImportProvenanceMetadataKey(key) {
			t.Fatalf("provenance evidence key %q is not exempt from secret-binding discovery", key)
		}
	}
}

func TestImportProvenanceIdentifierKeysAreBothPortableAndBindingExempt(t *testing.T) {
	if len(importProvenanceIdentifierKeys) == 0 {
		t.Fatal("no provenance identifier keys are declared")
	}
	for key := range importProvenanceIdentifierKeys {
		if !isPortableImportProvenanceIdentifierKey(key) {
			t.Fatalf("provenance identifier key %q is not portable", key)
		}
		if !isImportProvenanceMetadataKey(key) {
			t.Fatalf("provenance identifier key %q is not exempt from secret-binding discovery", key)
		}
	}
}

func TestImportProvenanceKeyClassification(t *testing.T) {
	tests := []struct {
		key        string
		portable   bool
		provenance bool
	}{
		{key: "source_path", portable: true, provenance: true},
		{key: "SOURCE_PATH", portable: true, provenance: true},
		{key: "  source_uid  ", portable: true, provenance: true},
		{key: "original_url", portable: false, provenance: true},
		{key: "source_transaction", portable: false, provenance: true},
		{key: "asyncapi_channel", portable: false, provenance: true},
		{key: "asyncapi_operation_id", portable: false, provenance: true},
		// Near misses must not be swept in by a prefix or substring match.
		{key: "source_secret", portable: false, provenance: false},
		{key: "source_pathname", portable: false, provenance: false},
		{key: "authorization", portable: false, provenance: false},
		{key: "asyncapi", portable: false, provenance: false},
		{key: "", portable: false, provenance: false},
	}
	for _, test := range tests {
		if got := isPortableImportProvenanceIdentifierKey(test.key); got != test.portable {
			t.Errorf("isPortableImportProvenanceIdentifierKey(%q) = %v, want %v", test.key, got, test.portable)
		}
		if got := isImportProvenanceMetadataKey(test.key); got != test.provenance {
			t.Errorf("isImportProvenanceMetadataKey(%q) = %v, want %v", test.key, got, test.provenance)
		}
	}
}

// TestImportSessionCapacityIsRetryableNotInternal pins the status mapping for
// protected import session failures.
//
// ErrSessionCapacity and ErrProjectDeleted used to fall into the default branch
// and surface as 500. Capacity exhaustion is transient and the caller's session
// is intact, so reporting it as an internal error told operators to file a bug
// instead of retrying. These sentinels are shared by every format that uses the
// commit service, so the mapping is a cross-importer contract.
func TestImportSessionCapacityIsRetryableNotInternal(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "capacity", err: importsession.ErrSessionCapacity, wantStatus: http.StatusTooManyRequests},
		{name: "project deleted", err: importsession.ErrProjectDeleted, wantStatus: http.StatusNotFound},
		{name: "claimed", err: importsession.ErrSessionClaimed, wantStatus: http.StatusConflict},
		{name: "expired", err: importsession.ErrSessionExpired, wantStatus: http.StatusNotFound},
		{name: "scope", err: importsession.ErrSessionScope, wantStatus: http.StatusNotFound},
		{name: "missing", err: os.ErrNotExist, wantStatus: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := importSessionClaimStatus(test.err); got != test.wantStatus {
				t.Fatalf("status for %v = %d, want %d", test.err, got, test.wantStatus)
			}
		})
	}
	if got := importSessionClaimStatus(errors.New("unexpected backend failure")); got != http.StatusInternalServerError {
		t.Fatalf("status for an unclassified error = %d, want %d", got, http.StatusInternalServerError)
	}
}

// TestCreateScriptDurableReportsIdempotentReplay pins the outcome signal the
// shared import commit now surfaces.
//
// Create-only persistence was already idempotent: a caller supplying a
// deterministic script id (protectedImportCreateScriptID) that retries after an
// interrupted commit gets the existing row back rather than an error. What was
// missing is that the outcome was invisible, so a retrying client could not
// distinguish "I committed this" from "this was already committed" and had to
// assume the former — the exact ambiguity the Bruno commit path solves with a
// durable receipt.
func TestCreateScriptDurableReportsIdempotentReplay(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := &dsl.Script{
		ID:       protectedImportCreateScriptID("openapi", "session-replay"),
		Name:     "orders",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "GET", Method: "GET", Target: "/orders",
		}},
	}

	first, created, err := server.runner.CreateScriptDurableReportingReplay(cloneScriptDefinition(script))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if !created {
		t.Fatal("first create reported a replay")
	}

	second, created, err := server.runner.CreateScriptDurableReportingReplay(cloneScriptDefinition(script))
	if err != nil {
		t.Fatalf("retry of an interrupted commit failed instead of replaying: %v", err)
	}
	if created {
		t.Fatal("retry reported a fresh create; a client would double-count the import")
	}
	if second.ID != first.ID {
		t.Fatalf("replay returned script %q, want the original %q", second.ID, first.ID)
	}
	if second.RefKey != first.RefKey {
		t.Fatalf("replay allocated a new reference key %q, want %q", second.RefKey, first.RefKey)
	}
}

// TestCreateScriptDurableStillRejectsForeignScriptID keeps the replay narrow:
// an id collision with a script this import does not own is a genuine conflict,
// not a replay.
func TestCreateScriptDurableStillRejectsForeignScriptID(t *testing.T) {
	server := NewServer(ServerConfig{})
	base := &dsl.Script{
		ID:       protectedImportCreateScriptID("openapi", "session-foreign"),
		Name:     "orders",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "GET", Method: "GET", Target: "/orders",
		}},
	}
	if _, _, err := server.runner.CreateScriptDurableReportingReplay(cloneScriptDefinition(base)); err != nil {
		t.Fatalf("seed create: %v", err)
	}

	different := cloneScriptDefinition(base)
	different.Actions[0].Target = "/something-else"
	if _, _, err := server.runner.CreateScriptDurableReportingReplay(different); err == nil {
		t.Fatal("a different script reusing the id was accepted as a replay")
	}
}

// Engine actions (imported source_* steps, custom_call) never send metadata,
// so a long descriptive source file name that the scrubber masks as
// token-shaped must not become a secret-binding requirement.
func TestEngineActionProvenanceMetadataIsNotABindingRequirement(t *testing.T) {
	const tokenShapedFile = "Action_Store_QA_08_Newactivation_Phone17Esim.c"
	metadata := func() map[string]any {
		return map[string]any{"source_file": tokenShapedFile, "source_line": 4}
	}
	script := &dsl.Script{
		Name: "provenance", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "message", Type: "source_message", Value: "hello", Params: map[string]any{"function": "lr_output_message"}, Metadata: metadata()},
			{Name: "step", Type: "custom_call", Params: map[string]any{"function": "FormatValue"}, Metadata: metadata()},
			{Name: "page", Type: "GET", Method: "GET", Target: "/", Metadata: metadata()},
		},
	}
	masked := maskScriptSecrets(script)
	if masked == nil {
		t.Fatal("mask failed")
	}
	if requirements := discoverSecretBindingRequirements(masked, 0); len(requirements) != 0 {
		t.Fatalf("requirements = %+v, want none for engine-action provenance metadata", requirements)
	}
}
