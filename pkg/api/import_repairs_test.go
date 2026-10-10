package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importsession"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProtectedImportRepairPreviewApplyAndRetry(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "repair-test"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	draft := &dsl.Script{ID: "repair", Name: "repair", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, ProjectID: "p", Actions: []dsl.Action{{Name: "submit", Type: "POST", Method: "POST", Target: "https://example.test/", Body: "{{secret.MISSING}}"}}}
	draft.Actions[0].Metadata = map[string]any{"masked_body": true, "review_required": true}
	ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)
	importreview.ApplyToAction(&draft.Actions[0], []importreview.Finding{{Code: "restored_body_required", ReasonCode: "restored_body_required", Severity: diag.Blocker, Gate: importreview.GateExecution}})
	id, _, err := server.registerImportCandidateSession(httptest.NewRequest("POST", "/", nil), enterpriseArchiveSaveProfile, draft)
	if err != nil || id == "" {
		t.Fatalf("candidate %q %v", id, err)
	}
	token := importDraftProtectedTokenPrefix + id
	body := `{"password":"protected-value","other":"also-protected"}`
	req := ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: "p", Finding: "restored_body_required", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "restore_body", Body: &body}}
	call := func(req ImportRepairRequest, apply bool) (int, []byte) {
		t.Helper()
		b, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/api/v1/scripts/import/repairs/preview", bytes.NewReader(b))
		w := httptest.NewRecorder()
		server.handleImportRepair(w, r, apply)
		return w.Code, w.Body.Bytes()
	}
	code, b := call(req, false)
	if code != 200 {
		t.Fatalf("preview %d: %s", code, b)
	}
	if bytes.Contains(b, []byte("protected-value")) || bytes.Contains(b, []byte("also-protected")) {
		t.Fatal("restored body leaked")
	}
	var preview ImportRepairResponse
	if err = json.Unmarshal(b, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.CandidateID != token {
		t.Fatal("preview mutated candidate")
	}
	wrong := req
	wrong.ProjectID = "other"
	if code, _ := call(wrong, true); code != 409 {
		t.Fatalf("cross-project status %d", code)
	}
	stale := req
	stale.Revision = "old"
	if code, _ := call(stale, true); code != 409 {
		t.Fatalf("revision status %d", code)
	}
	code, b = call(req, true)
	if code != 200 {
		t.Fatalf("apply %d: %s", code, b)
	}
	var applied ImportRepairResponse
	_ = json.Unmarshal(b, &applied)
	if applied.CandidateID == token || !applied.ExpiresAt.Equal(preview.ExpiresAt) {
		t.Fatal("candidate not rotated or expiry extended")
	}
	if code, retry := call(req, true); code != 200 || !bytes.Equal(retry, b) {
		t.Fatalf("retry not stable: %d", code)
	}
	changed := req
	changed.Operation.Type = "inspect"
	if code, _ := call(changed, true); code != 409 {
		t.Fatalf("superseded candidate status %d", code)
	}
	mutation, mutationErr := server.claimImportDraftMutation(httptest.NewRequest("POST", "/", nil), applied.Script)
	if mutationErr != nil || mutation == nil {
		t.Fatalf("repaired candidate cannot use existing save path: %v", mutationErr)
	}
	if mutation.Script.Actions[0].Body != body {
		t.Fatal("save path failed to restore protected body")
	}
	server.releaseImportDraftAttestation(mutation)
	exact, claim, err := server.claimProtectedImportCandidateSession(httptest.NewRequest("POST", "/", nil), enterpriseArchiveSaveProfile, strings.TrimPrefix(applied.CandidateID, importDraftProtectedTokenPrefix), "p")
	if err != nil {
		t.Fatal(err)
	}
	defer server.releaseImportCandidateClaim(claim)
	defer clearProtectedImportCandidate(exact)
	if exact.Script.Actions[0].Body != body {
		t.Fatal("protected body was not preserved")
	}
	if len(applied.Findings) != 0 {
		t.Fatalf("resolved finding persists: %+v", applied.Findings)
	}
}

func TestImportRepairPartialDependencyAndInvalidOperations(t *testing.T) {
	script := &dsl.Script{Name: "dependency", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "consumer", Type: "GET", Target: "https://example.test/{{var.missing}}/{{var.other}}"}}}
	_, err := importreview.RefreshDependencies(script)
	if err != nil {
		t.Fatal(err)
	}
	req := ImportRepairRequest{Finding: "missing_producer:missing", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: "missing", Type: "list", Values: []string{"supplied"}, Update: "once"}}}
	if _, err := applyImportRepair(script, req); err != nil {
		t.Fatal(err)
	}
	findings, err := importreview.RefreshDependencies(script)
	if err != nil {
		t.Fatal(err)
	}
	foundOther := false
	for _, f := range findings {
		if f.Code == "missing_producer:missing" {
			t.Fatal("repaired dependency remains")
		}
		if f.Code == "missing_producer:other" {
			foundOther = true
		}
	}
	if !foundOther {
		t.Fatal("partial repair cleared unrelated blocker")
	}
	req.Finding = "missing_producer:other"
	req.Operation = ImportRepairOperation{Type: "acknowledge"}
	if _, err := applyImportRepair(script, req); err == nil {
		t.Fatal("acknowledgement bypassed blocker")
	}
}

func TestImportRepairTLSAndProxyReplacement(t *testing.T) {
	for _, tc := range []struct {
		code      string
		operation ImportRepairOperation
	}{
		{"legacy_tls_replacement", ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"}},
		{"encrypted_proxy_password", ImportRepairOperation{Type: "replace_proxy_secret", SecretName: "PROJECT_PROXY"}},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s := &dsl.Script{Name: "repair", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Type: "source_review", Name: "source setting"}}}
			importreview.ApplyToAction(&s.Actions[0], []importreview.Finding{{Code: tc.code, ReasonCode: tc.code, Gate: importreview.GateExecution}})
			_, err := applyImportRepair(s, ImportRepairRequest{ActionPath: "actions[0]", Finding: tc.code, Operation: tc.operation})
			if err != nil {
				t.Fatalf("%v issues=%v", err, dsl.ValidateScriptIssuesWithPreservedImportCredentials(s))
			}
			if tc.operation.Type == "select_tls" {
				if s.Config.HTTP.TLS.MinVersion != "1.2" || !strings.Contains(fmt.Sprint(s.Actions[0].Metadata["migration_change"]), "not exact") {
					t.Fatal("TLS migration evidence absent")
				}
			} else if s.Config.HTTP.Proxy.Password != "{{secret.PROJECT_PROXY}}" {
				t.Fatal("proxy secret binding lost")
			}
		})
	}
}

func TestImportRepairRejectsExpiredCandidate(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "expired-repair"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	manager, err := server.protectedImportSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	envelope := importCandidateEnvelope{Version: importCandidateSessionVersion, Profile: enterpriseArchiveSaveProfile, ProjectID: "p", Script: dsl.Script{Name: "expired", ProjectID: "p"}, ExpiresAt: time.Now().Add(-time.Minute)}
	data, _ := json.Marshal(envelope)
	r := httptest.NewRequest("POST", "/", nil)
	_, actor, _ := server.resolveAuditActor(r)
	err = manager.Save(r.Context(), importsession.SessionDescriptor{ID: id, Profile: enterpriseArchiveSaveProfile, ProjectID: "p", Actor: actor, ExpiresAt: time.Now().Add(time.Hour)}, data, importCandidateSessionMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	token := importDraftProtectedTokenPrefix + id
	req := ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: "p", Operation: ImportRepairOperation{Type: "inspect"}}
	body, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	server.handlePreviewImportRepair(w, httptest.NewRequest("POST", "/", bytes.NewReader(body)))
	if w.Code != 409 {
		t.Fatalf("expired candidate returned %d", w.Code)
	}
}

// consumeDuringReadStore makes a competing consumer claim the candidate as
// soon as a repair request has read it, while that request is still
// evaluating. This is the dashboard's save path: the guided-repair panel
// inspects every rotated candidate while the operator's save claims it.
type consumeDuringReadStore struct {
	*importartifact.EncryptedStore
	sessionID string
	attempted bool
	consumed  error
}

func (s *consumeDuringReadStore) OpenSessionScoped(ctx context.Context, scope importsession.Scope) (io.ReadCloser, importsession.SessionDescriptor, error) {
	reader, descriptor, err := s.EncryptedStore.OpenSessionScoped(ctx, scope)
	if err != nil || scope.ID != s.sessionID {
		return reader, descriptor, err
	}
	return &consumeOnCloseReader{ReadCloser: reader, consume: func() { s.consume(ctx, scope) }}, descriptor, nil
}

func (s *consumeDuringReadStore) ClaimSessionScoped(ctx context.Context, scope importsession.Scope, maxBytes int64) ([]byte, importsession.SessionDescriptor, error) {
	payload, descriptor, err := s.EncryptedStore.ClaimSessionScoped(ctx, scope, maxBytes)
	if err == nil && scope.ID == s.sessionID {
		s.consume(ctx, scope)
	}
	return payload, descriptor, err
}

func (s *consumeDuringReadStore) consume(ctx context.Context, scope importsession.Scope) {
	if s.attempted {
		return
	}
	s.attempted = true
	payload, descriptor, err := s.EncryptedStore.ClaimSessionScoped(ctx, scope, importCandidateSessionMaxBytes)
	clear(payload)
	if err != nil {
		s.consumed = err
		return
	}
	s.consumed = s.EncryptedStore.ReleaseSessionClaim(scope.ID, scope.ProjectID, scope.Actor, descriptor.ClaimToken)
}

type consumeOnCloseReader struct {
	io.ReadCloser
	consume func()
}

func (r *consumeOnCloseReader) Close() error {
	err := r.ReadCloser.Close()
	r.consume()
	return err
}

func TestImportRepairPreviewDoesNotBlockConcurrentCandidateConsumer(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "preview-consumer"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	draft := &dsl.Script{ID: "inspect", Name: "inspect", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, ProjectID: "p", Actions: []dsl.Action{{Name: "submit", Type: "POST", Method: "POST", Target: "https://example.test/", Body: "{{secret.MISSING}}"}}}
	ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)
	id, _, err := server.registerImportCandidateSession(httptest.NewRequest("POST", "/", nil), enterpriseArchiveSaveProfile, draft)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	spy := &consumeDuringReadStore{EncryptedStore: objects, sessionID: id}
	if server.importSessions, err = importsession.NewManager(spy); err != nil {
		t.Fatal(err)
	}

	token := importDraftProtectedTokenPrefix + id
	body, _ := json.Marshal(ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: "p", Operation: ImportRepairOperation{Type: "inspect"}})
	w := httptest.NewRecorder()
	server.handlePreviewImportRepair(w, httptest.NewRequest("POST", "/", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("inspect status %d: %s", w.Code, w.Body.String())
	}
	if !spy.attempted {
		t.Fatal("inspection never read the candidate")
	}
	if spy.consumed != nil {
		t.Fatalf("a read-only inspection blocked the candidate's consumer: %v", spy.consumed)
	}
}

func TestAllocationRepairPreviewRejectsWhitespaceInlineFileFallback(t *testing.T) {
	script := &dsl.Script{DataSources: []dsl.DataSource{{Name: "rows", Type: "csv", Path: "/must-not-open.csv", InlineData: " \n\t"}}}
	_, err := applyImportRepair(script, ImportRepairRequest{Operation: ImportRepairOperation{Type: "preview_allocation", VUsers: 2}})
	if err == nil || err.Error() != "Allocation preview requires embedded data rows" {
		t.Fatalf("local file fallback was not rejected: %v", err)
	}
}

func TestBindRepairPreservesNamespacesAndAcceptsWhitespace(t *testing.T) {
	for _, ref := range []string{"{{var.missing}}", "{{ var.missing }}", "{{param.missing}}", "{{ param.missing }}"} {
		t.Run(ref, func(t *testing.T) {
			script := &dsl.Script{Name: "binding", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Parameters: []dsl.Parameter{{Name: "existing", Type: "list", Values: []string{"value"}, Update: "once"}}, Actions: []dsl.Action{{Name: "consumer", Type: "POST", Target: "https://example.test/", Body: ref, Metadata: map[string]any{"source_evidence": ref}}}}
			if _, err := importreview.RefreshDependencies(script); err != nil {
				t.Fatalf("%v validation=%v", err, dsl.ValidateScriptIssuesWithPreservedImportCredentials(script))
			}
			_, err := applyImportRepair(script, ImportRepairRequest{Finding: "missing_producer:missing", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "bind_variable", Producer: "existing"}})
			if err != nil {
				t.Fatalf("%v validation=%v", err, dsl.ValidateScriptIssuesWithPreservedImportCredentials(script))
			}
			namespace := "var"
			if strings.Contains(ref, "param.") {
				namespace = "param"
			}
			if script.Actions[0].Body != "{{"+namespace+".existing}}" || script.Actions[0].Metadata["source_evidence"] != ref {
				t.Fatalf("incorrect replacement: %+v", script.Actions[0])
			}
			findings, err := importreview.RefreshDependencies(script)
			if err != nil || len(findings) != 0 {
				t.Fatalf("repair unresolved: %+v %v", findings, err)
			}
		})
	}
}
