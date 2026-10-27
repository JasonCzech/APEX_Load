package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/storage"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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

// A Kafka header or JMS property is delivered on the wire, and a protocol step
// refuses an unresolved {{param.*}} outside its import evidence, so a bind
// repair must rebind a use there, even when it is the step's only use. Import
// evidence and metadata replay does not read keep the recorded text.
func TestBindRepairRebindsTheMetadataReplayReads(t *testing.T) {
	evidence := func(ref string) map[string]any {
		return map[string]any{"source_evidence": ref, "source_path": "Action.c " + ref, "Original_URL": "/orders/" + ref, "asyncapi_channel": ref}
	}
	with := func(base map[string]any, entries map[string]any) map[string]any {
		for key, value := range entries {
			base[key] = value
		}
		return base
	}
	kafka := func(ref string) map[string]any {
		return with(evidence(ref), map[string]any{"tenant": ref, "jmeter_note": ref, "schema": map[string]any{"subject": []any{"orders-" + ref}}})
	}
	jms := func(ref string) map[string]any {
		return with(evidence(ref), map[string]any{"tenant": "t-" + ref, "source_node_id": ref})
	}
	http := func(ref string) map[string]any {
		return with(evidence(ref), map[string]any{"note": ref})
	}
	for _, test := range []struct {
		name     string
		protocol string
		action   string
		ref      string
		metadata func(string) map[string]any
		rebound  []string
	}{
		// A Kafka send delivers string entries that are not provenance, so a
		// {{var.*}} use is read only in the header it sends.
		{name: "kafka header var", protocol: "kafka", action: "kafka_produce", ref: "{{var.missing}}", metadata: kafka, rebound: []string{"tenant"}},
		{name: "kafka header param", protocol: "kafka", action: "kafka_produce", ref: "{{param.missing}}", metadata: kafka, rebound: []string{"tenant", "jmeter_note", "schema"}},
		{name: "jms property var", protocol: "jms-solace", action: "jms_publish", ref: "{{var.missing}}", metadata: jms, rebound: []string{"tenant", "source_node_id"}},
		{name: "jms property param", protocol: "jms-solace", action: "jms_request_reply", ref: "{{ param.missing }}", metadata: jms, rebound: []string{"tenant", "source_node_id"}},
		{name: "http param outside import evidence", protocol: "http", action: "POST", ref: "{{param.missing}}", metadata: http, rebound: []string{"note"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			namespace := "var"
			if strings.Contains(test.ref, "param.") {
				namespace = "param"
			}
			script := &dsl.Script{Name: "binding", Version: "1.0", Protocol: test.protocol, Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Parameters: []dsl.Parameter{{Name: "existing", Type: "list", Values: []string{"value"}, Update: "once"}}, Actions: []dsl.Action{{Name: "consumer", Type: test.action, Target: "orders", Body: "{}", Metadata: test.metadata(test.ref)}}}
			switch test.protocol {
			case "kafka":
				script.Config.Kafka.Mock = true
			case "jms-solace":
				script.Config.JMS.Mock = true
				script.Config.JMS.ReplyTo = "replies"
			default:
				script.Actions[0].Target = "https://example.test/orders"
			}
			findings, err := importreview.RefreshDependencies(script)
			if err != nil || len(findings) != 1 || findings[0].Code != "missing_producer:missing" || findings[0].ActionPath != "actions[0]" {
				t.Fatalf("fixture findings = %+v, %v; want the step's metadata use as missing_producer:missing", findings, err)
			}
			if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "missing_producer:missing", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "bind_variable", Producer: "existing"}}); err != nil {
				t.Fatalf("%v validation=%v", err, dsl.ValidateScriptIssuesWithPreservedImportCredentials(script))
			}
			want := test.metadata(test.ref)
			bound := test.metadata("{{" + namespace + ".existing}}")
			for _, key := range test.rebound {
				want[key] = bound[key]
			}
			for key, value := range want {
				if got := script.Actions[0].Metadata[key]; mustJSONForRepairTest(t, got) != mustJSONForRepairTest(t, value) {
					t.Errorf("metadata[%q] = %s, want %s", key, mustJSONForRepairTest(t, got), mustJSONForRepairTest(t, value))
				}
			}
			if script.Actions[0].Body != "{}" {
				t.Fatalf("body = %q, want it unchanged", script.Actions[0].Body)
			}
			if findings, err := importreview.RefreshDependencies(script); err != nil || len(findings) != 0 {
				t.Fatalf("repair unresolved: %+v %v", findings, err)
			}
		})
	}
}

// An LR transaction group copies its first request's target into the
// http_sequence wrapper, but replay sends only the requests. Binding the
// request is the whole repair: neither replay readiness nor promotion keeps
// reporting the copy the wrapper still holds.
func TestBindRepairOnASequenceRequestClearsReadinessAndPromotion(t *testing.T) {
	target := "https://example.test/orders/{{var.order}}?region={{param.region}}"
	request := map[string]any{"name": "orders", "type": "GET", "method": "GET", "target": target, "metadata": map[string]any{"source_evidence": target}}
	script := &dsl.Script{Name: "binding", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Parameters: []dsl.Parameter{{Name: "existing", Type: "list", Values: []string{"value"}, Update: "once"}}, Actions: []dsl.Action{{
		Name: "checkout", Type: "http_sequence", Method: "SEQUENCE", Target: target,
		Params:   map[string]any{"requests": []any{request}},
		Metadata: map[string]any{"source_evidence": target, "source_transaction": "checkout", "source_request_count": 1},
	}}}
	findings, err := importreview.RefreshDependencies(script)
	if err != nil {
		t.Fatal(err)
	}
	requestPath := "actions[0].params.requests[0]"
	var atRequest []string
	for _, f := range findings {
		if f.ActionPath == requestPath {
			atRequest = append(atRequest, f.Code)
		}
	}
	slices.Sort(atRequest)
	if !slices.Equal(atRequest, []string{"missing_producer:order", "missing_producer:region"}) {
		t.Fatalf("fixture findings = %+v; want order and region at the request", findings)
	}
	for _, finding := range atRequest {
		if _, err := applyImportRepair(script, ImportRepairRequest{Finding: finding, ActionPath: requestPath, Operation: ImportRepairOperation{Type: "bind_variable", Producer: "existing"}}); err != nil {
			t.Fatalf("%s: %v validation=%v", finding, err, dsl.ValidateScriptIssuesWithPreservedImportCredentials(script))
		}
	}
	if script.Actions[0].Target != target {
		t.Fatalf("wrapper target = %q, want the recorded copy kept", script.Actions[0].Target)
	}
	if findings, err := importreview.RefreshDependencies(script); err != nil || len(findings) != 0 {
		t.Fatalf("promotion findings after binding the request = %+v, %v", findings, err)
	}
	if preflight := buildReplayPreflight(script, nil, nil, nil); !preflight.Valid || len(preflight.MissingVariables) != 0 || len(preflight.Issues) != 0 {
		t.Fatalf("readiness after binding the request: valid=%v missing=%v issues=%v", preflight.Valid, preflight.MissingVariables, preflight.Issues)
	}
}

// A bind on a control step rewrites the step's own condition. Its body
// steps have findings and import evidence of their own, which keep their text.
func TestBindRepairOnAControlStepLeavesItsBodyAsRecorded(t *testing.T) {
	inner := dsl.Action{Name: "inner", Type: "GET", Target: "https://example.test/{{var.missing}}", Metadata: map[string]any{"source_evidence": "{{var.missing}}"}}
	script := &dsl.Script{Name: "binding", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Parameters: []dsl.Parameter{{Name: "existing", Type: "list", Values: []string{"value"}, Update: "once"}}, Actions: []dsl.Action{
		{Name: "gate", Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "{{var.missing}}", Op: "eq", Right: "yes"}, Steps: []dsl.Action{inner}}},
	}}
	if _, err := importreview.RefreshDependencies(script); err != nil {
		t.Fatal(err)
	}
	if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "missing_producer:missing", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "bind_variable", Producer: "existing"}}); err != nil {
		t.Fatalf("%v validation=%v", err, dsl.ValidateScriptIssuesWithPreservedImportCredentials(script))
	}
	control := script.Actions[0].Control
	if control.Condition.Left != "{{var.existing}}" {
		t.Fatalf("condition = %q, want it rebound", control.Condition.Left)
	}
	if got := control.Steps[0]; got.Target != inner.Target || got.Metadata["source_evidence"] != "{{var.missing}}" {
		t.Fatalf("body step = %q %v, want its target and import evidence as recorded", got.Target, got.Metadata["source_evidence"])
	}
	findings, err := importreview.RefreshDependencies(script)
	if err != nil || len(findings) != 1 || findings[0].ActionPath != "actions[0].control.steps[0]" || findings[0].Code != "missing_producer:missing" {
		t.Fatalf("findings = %+v, %v; want only the body step's own use", findings, err)
	}
}

// A request's copy on its http_sequence wrapper is not a read, so a wrapper
// finding can come only from its attribution, which the runtime interpolates
// for the aggregate result. A bind there rewrites the wrapper's own fields
// and leaves its requests as recorded.
func TestBindRepairOnASequenceWrapperLeavesItsRequestsAsRecorded(t *testing.T) {
	requestTarget := "https://example.test/{{var.missing}}"
	script := &dsl.Script{Name: "binding", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Parameters: []dsl.Parameter{{Name: "existing", Type: "list", Values: []string{"value"}, Update: "once"}}, Actions: []dsl.Action{{
		Name: "checkout", Type: "http_sequence", Method: "SEQUENCE", Target: requestTarget,
		Attribution: &dsl.AnalyticsAttribution{HierarchyPath: "Checkout/{{var.missing}}"},
		Params:      map[string]any{"requests": []any{map[string]any{"name": "orders", "type": "GET", "method": "GET", "target": requestTarget, "metadata": map[string]any{"source_evidence": "{{var.missing}}"}}}},
	}}}
	if _, err := importreview.RefreshDependencies(script); err != nil {
		t.Fatal(err)
	}
	if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "missing_producer:missing", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "bind_variable", Producer: "existing"}}); err != nil {
		t.Fatalf("%v validation=%v", err, dsl.ValidateScriptIssuesWithPreservedImportCredentials(script))
	}
	if got := script.Actions[0].Attribution.HierarchyPath; got != "Checkout/{{var.existing}}" {
		t.Fatalf("attribution = %q, want it rebound", got)
	}
	request := script.Actions[0].Params["requests"].([]any)[0].(map[string]any)
	if request["target"] != requestTarget || request["metadata"].(map[string]any)["source_evidence"] != "{{var.missing}}" {
		t.Fatalf("request = %v, want its target and import evidence as recorded", request)
	}
	findings, err := importreview.RefreshDependencies(script)
	if err != nil || len(findings) != 1 || findings[0].ActionPath != "actions[0].params.requests[0]" || findings[0].Code != "missing_producer:missing" {
		t.Fatalf("findings = %+v, %v; want only the request's own use", findings, err)
	}
}

func mustJSONForRepairTest(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func legacyTLSGateForTest(name, warning string) dsl.Action {
	gate := dsl.Action{Name: name, Type: "source_review", Metadata: map[string]any{"source_function": "web_set_sockets_option", "covered_warnings": []string{warning}}}
	importreview.ApplyToAction(&gate, []importreview.Finding{{Code: "legacy_tls_replacement", ReasonCode: "legacy_tls_replacement", Severity: diag.Blocker, Gate: importreview.GateExecution, Source: enterpriseArchiveSaveProfile, Resolution: "select_tls"}})
	return gate
}

// encryptedProxyGateForTest is the gate an archive import stamps for an
// encrypted proxy password, which replace_proxy_secret repairs.
func encryptedProxyGateForTest() dsl.Action {
	gate := dsl.Action{Name: "proxy setting", Type: "source_review"}
	importreview.ApplyToAction(&gate, []importreview.Finding{{Code: "encrypted_proxy_password", ReasonCode: "encrypted_proxy_password", Severity: diag.Blocker, Gate: importreview.GateExecution, Source: enterpriseArchiveSaveProfile, Resolution: "replace_proxy_secret"}})
	return gate
}

func legacyTLSWarningForTest(value string) string {
	return fmt.Sprintf("web_set_sockets_option SSL_VERSION=%q is unsupported; APEX TLS defaults remain unchanged and review is required", value)
}

// The TLS range a select_tls repair sets is script-wide, so one apply must
// resolve every step gated on the same requirement, wherever it sits, and
// leave every other requirement in place.
func TestImportRepairSelectTLSResolvesEveryOccurrence(t *testing.T) {
	proxy := dsl.Action{Name: "proxy setting", Type: "source_review"}
	importreview.ApplyToAction(&proxy, []importreview.Finding{{Code: "encrypted_proxy_password", ReasonCode: "encrypted_proxy_password", Severity: diag.Blocker, Gate: importreview.GateExecution}})
	nested := legacyTLSGateForTest("nested setting", legacyTLSWarningForTest("AUTO"))
	sequenceMetadata := map[string]any{}
	for key, value := range legacyTLSGateForTest("sequence setting", legacyTLSWarningForTest("SSL3")).Metadata {
		sequenceMetadata[key] = value
	}
	script := &dsl.Script{Name: "tls", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test", WebRuntime: dsl.WebRuntimeConfig{AdditionalAttributes: map[string]string{"web_set_sockets_option.SSL_VERSION": "2&3"}}},
		Init: []dsl.Action{
			legacyTLSGateForTest("first setting", legacyTLSWarningForTest("2&3")),
			{Name: "home", Type: "GET", Target: "https://example.test/"},
			legacyTLSGateForTest("second setting", legacyTLSWarningForTest("2&3")),
			proxy,
		},
		Actions: []dsl.Action{
			{Name: "branch", Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{nested}}},
			{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "sequence setting", "method": "GET", "target": "https://example.test/sequence", "metadata": sequenceMetadata}}}},
		},
	}
	if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) > 0 {
		t.Fatalf("fixture is invalid: %v", issues)
	}
	_, err := applyImportRepair(script, ImportRepairRequest{ActionPath: "init[0]", Finding: "legacy_tls_replacement", Operation: ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if script.Config.HTTP.TLS.MinVersion != "1.2" || script.Config.HTTP.TLS.MaxVersion != "1.3" {
		t.Fatalf("TLS config = %#v, want 1.2-1.3", script.Config.HTTP.TLS)
	}
	if _, kept := script.Config.WebRuntime.AdditionalAttributes["web_set_sockets_option.SSL_VERSION"]; kept {
		t.Fatal("legacy SSL_VERSION attribute survived the repair")
	}
	findings, err := importreview.FindingsFromScript(script)
	if err != nil {
		t.Fatal(err)
	}
	var remaining []string
	for _, finding := range findings {
		remaining = append(remaining, finding.ActionPath+" "+finding.Code)
	}
	if len(remaining) != 1 || remaining[0] != "init[3] encrypted_proxy_password" {
		t.Fatalf("remaining findings = %v, want only the proxy requirement", remaining)
	}
	migrated := map[string]bool{}
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		metadata := ref.Metadata()
		if dslwalk.MetadataString(metadata, "source_function") != "web_set_sockets_option" {
			return nil
		}
		if !strings.Contains(dslwalk.MetadataString(metadata, "migration_change"), "TLS 1.2–1.3") {
			t.Errorf("%s migration_change = %v, want the operator's TLS selection", ref.Path, metadata["migration_change"])
		}
		if len(dslwalk.MetadataStrings(metadata, "covered_warnings")) != 1 {
			t.Errorf("%s covered_warnings = %v, want the converter warning kept", ref.Path, metadata["covered_warnings"])
		}
		migrated[ref.Path] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"init[0]", "init[2]", "actions[0].control.steps[0]", "actions[1].params.requests[0]"} {
		if !migrated[path] {
			t.Errorf("TLS step %s was not visited; visited %v", path, migrated)
		}
	}
	if _, stamped := script.Init[3].Metadata["migration_change"]; stamped {
		t.Fatal("an unrelated requirement was stamped as a TLS migration")
	}
}

// A repair rebuilds the preserved preflight from the repaired draft. It keeps
// the converter's warnings and derives the lines BuildReplayPreflight writes
// itself again, so a stale one cannot outlive the draft it described.
func TestImportRepairRestampsPreservedReplayPreflight(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "restamp-test"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	converterWarnings := []string{legacyTLSWarningForTest("2&3"), legacyTLSWarningForTest("AUTO"), "Action.c:9 lr_think_time could not be placed after its transaction"}
	draft := &dsl.Script{ID: "restamp", Name: "restamp", Version: "1.0", Protocol: "http", ProjectID: "p", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Init:    []dsl.Action{legacyTLSGateForTest("first setting", converterWarnings[0]), legacyTLSGateForTest("second setting", converterWarnings[1])},
		Actions: []dsl.Action{{Name: "orders", Type: "GET", Method: "GET", Target: "https://example.test/orders/{{var.session}}"}},
	}
	if _, err := importreview.RefreshDependencies(draft); err != nil {
		t.Fatal(err)
	}
	prior := buildReplayPreflight(draft, nil, validateScript(draft), converterWarnings)
	stale := "variable(s) with no seed or preceding extractor are sent as placeholder text: retired"
	prior.Warnings = append(prior.Warnings, stale)
	attachImportReplayPreflight(draft, enterpriseArchiveSaveProfile, prior)
	if prior.Valid || replayPreflightCheckStatus(prior, "review_gates") != "fail" {
		t.Fatalf("fixture preflight = %+v, want both TLS gates to block replay", prior)
	}
	id, _, err := server.registerImportCandidateSession(httptest.NewRequest("POST", "/", nil), enterpriseArchiveSaveProfile, draft)
	if err != nil {
		t.Fatal(err)
	}
	token := importDraftProtectedTokenPrefix + id
	req := ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: "p", Finding: "legacy_tls_replacement", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"}}
	body, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	server.handleImportRepair(w, httptest.NewRequest("POST", "/api/v1/scripts/import/repairs/apply", bytes.NewReader(body)), true)
	if w.Code != 200 {
		t.Fatalf("apply %d: %s", w.Code, w.Body.String())
	}
	var applied ImportRepairResponse
	if err := json.Unmarshal(w.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	for _, finding := range applied.Findings {
		if finding.Code == "legacy_tls_replacement" {
			t.Fatalf("a TLS gate survived one select_tls apply: %+v", applied.Findings)
		}
	}
	if !applied.Readiness.ReplayReady {
		t.Fatalf("readiness = %+v, want replay ready once both TLS gates are cleared", applied.Readiness)
	}

	// The response script is repairDisclosure's projection; it must keep the
	// preserved preflight the review reads.
	preflight := preservedReplayPreflight(applied.Script)
	if preflight == nil {
		t.Fatal("repairDisclosure dropped the preserved replay preflight")
	}
	if !preflight.Valid || replayPreflightCheckStatus(preflight, "review_gates") != "pass" {
		t.Fatalf("restamped preflight = %+v, want valid with passing review gates", preflight)
	}
	placeholder := "variable(s) with no seed or preceding extractor are sent as placeholder text: session"
	want := append(append([]string(nil), converterWarnings...), placeholder)
	if strings.Join(preflight.Warnings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("restamped warnings = %#v, want %#v", preflight.Warnings, want)
	}
	if status, detail := replayPreflightCheckStatus(preflight, "validation"), replayPreflightCheckDetail(preflight, "validation"); status != "warn" || detail != "3 validation warnings" {
		t.Fatalf("validation check = %s %q, want the carried converter warnings to keep it at warn", status, detail)
	}

	// The protected candidate carries the same restamped preflight, so the
	// save-time digest of the submitted script still matches it.
	exact, claim, err := server.claimProtectedImportCandidateSession(httptest.NewRequest("POST", "/", nil), enterpriseArchiveSaveProfile, strings.TrimPrefix(applied.CandidateID, importDraftProtectedTokenPrefix), "p")
	if err != nil {
		t.Fatal(err)
	}
	stored := preservedReplayPreflight(exact.Script)
	clearProtectedImportCandidate(exact)
	server.releaseImportCandidateClaim(claim)
	if stored == nil || strings.Join(stored.Warnings, "\n") != strings.Join(want, "\n") || !stored.Valid {
		t.Fatalf("protected candidate preflight = %+v, want the restamped one", stored)
	}
	mutation, err := server.claimImportDraftMutation(httptest.NewRequest("POST", "/", nil), applied.Script)
	if err != nil || mutation == nil {
		t.Fatalf("the restamped candidate no longer passes the save-time attestation: %v", err)
	}
	server.releaseImportDraftAttestation(mutation)
}

func replayPreflightCheckDetail(preflight *ReplayPreflight, id string) string {
	for _, check := range preflight.Checks {
		if check.ID == id {
			return check.Detail
		}
	}
	return ""
}

// Every warning line BuildReplayPreflight writes itself is recognized as
// generated, so a stale one is never carried as a converter warning, and a
// converter warning is never mistaken for one.
func TestCarriedConverterWarningsDropOnlyGeneratedLines(t *testing.T) {
	scripts := []*dsl.Script{
		{Name: "http", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
			DataSources: []dsl.DataSource{
				{Name: "empty_file", Type: "file"},
				{Name: "openapi_file", Type: "file", InlineData: "APEXLOAD_OPENAPI_FILE_PLACEHOLDER"},
				{Name: "bound_file", Type: "file", InlineData: requestimport.MissingFilePlaceholder},
				{Name: "rows", Type: "csv"},
			},
			Parameters: []dsl.Parameter{{Name: "user", Type: "file", DataSource: "absent"}},
			Actions:    []dsl.Action{{Name: "orders", Type: "GET", Target: "https://example.test/{{var.session}}?key={{secret.WI_SYNTHETIC_UNSET_KEY}}"}},
		},
		{Name: "jms", Version: "1.0", Protocol: "jms-solace", Config: dsl.ScriptConfig{JMS: dsl.JMSConfig{Mock: true}}},
	}
	missingSecrets := engine.SecretResolverFunc(func(string) (string, bool) { return "", false })
	var generated []string
	for _, script := range scripts {
		generated = append(generated, buildReplayPreflight(script, nil, nil, nil).Warnings...)
		generated = append(generated, buildReplayPreflight(script, nil, nil, nil, missingSecrets).Warnings...)
	}
	for _, prefix := range []string{"variable(s) with no seed", "configure project secret(s)", "secret(s) require project configuration", "file data source \"empty_file\"", "file data source \"openapi_file\"", "file data source \"bound_file\"", "data source \"rows\"", "parameter \"user\"", "config.jms.mock"} {
		found := false
		for _, line := range generated {
			found = found || strings.HasPrefix(line, prefix)
		}
		if !found {
			t.Errorf("fixture generated no %q line; generated %#v", prefix, generated)
		}
	}
	for _, line := range generated {
		if !replayPreflightGeneratedWarning(line) {
			t.Errorf("generated line %q is not recognized", line)
		}
	}

	converter := []string{legacyTLSWarningForTest("2&3"), "parameter file users.prm: data file users.dat is referenced with conflicting delimiter/column layouts; separate deterministic data sources were created"}
	prior := cloneScriptDefinition(scripts[0])
	attachImportReplayPreflight(prior, enterpriseArchiveSaveProfile, buildReplayPreflight(prior, nil, nil, converter))
	if got := carriedConverterWarnings(prior); strings.Join(got, "\n") != strings.Join(converter, "\n") {
		t.Fatalf("carried = %#v, want only the converter warnings %#v", got, converter)
	}
	// Once stored, the preflight is decoded JSON rather than the typed value.
	raw, _ := json.Marshal(prior)
	var decoded dsl.Script
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := carriedConverterWarnings(&decoded); strings.Join(got, "\n") != strings.Join(converter, "\n") {
		t.Fatalf("carried from stored candidate = %#v, want %#v", got, converter)
	}
	if got := carriedConverterWarnings(scripts[1]); got != nil {
		t.Fatalf("carried without a prior preflight = %#v, want none", got)
	}
}

// The restamped preflight stays an import preflight, so project-secret state
// never makes it invalid or fails its secrets check. Its missing secrets are
// the ones the repair's readiness could not resolve, not every secret the
// legacy environment probe cannot see: binding the proxy password to a secret
// the project holds lists nothing missing, and binding it to one the project
// lacks still lists that one with the configure-later guidance.
func TestImportRepairRestampedPreflightResolvesProjectSecrets(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "proxy-secret-test"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{Store: store, BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	const configured, unconfigured = "WI_SYNTHETIC_REPAIR_PROXY_PW", "WI_SYNTHETIC_REPAIR_PROXY_PW_UNSET"
	if _, err := server.projectSecretStore.Create(context.Background(), storage.DefaultProjectID, configured, "synthetic-proxy-password", "test"); err != nil {
		t.Fatal(err)
	}
	draft := &dsl.Script{ID: "proxy-secret", Name: "proxy secret", Version: "1.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{ProxyMode: "custom", Proxy: dsl.ProxyConfig{URL: "http://proxy.example.test:8080"}}},
		Init:    []dsl.Action{encryptedProxyGateForTest()},
		Actions: []dsl.Action{{Name: "orders", Type: "GET", Method: "GET", Target: "https://example.test/orders"}},
	}
	attachImportReplayPreflight(draft, enterpriseArchiveSaveProfile, buildReplayPreflight(draft, nil, validateScript(draft), nil))
	id, _, err := server.registerImportCandidateSession(httptest.NewRequest(http.MethodPost, "/", nil), enterpriseArchiveSaveProfile, draft)
	if err != nil {
		t.Fatal(err)
	}
	token := importDraftProtectedTokenPrefix + id
	repair := func(secret string, apply bool) ImportRepairResponse {
		t.Helper()
		body, _ := json.Marshal(ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: storage.DefaultProjectID, Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: "replace_proxy_secret", SecretName: secret}})
		w := httptest.NewRecorder()
		server.handleImportRepair(w, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/repairs/preview", bytes.NewReader(body)), apply)
		if w.Code != http.StatusOK {
			t.Fatalf("repair with %s: %d %s", secret, w.Code, w.Body.String())
		}
		var response ImportRepairResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	assertSecrets := func(label string, preflight *ReplayPreflight, secret string, missing bool) {
		t.Helper()
		if preflight == nil {
			t.Fatalf("%s: no restamped preflight", label)
		}
		wantMissing, wantGuidance, wantDetail := []string(nil), []string(nil), "Required: "+secret
		if missing {
			wantMissing, wantGuidance, wantDetail = []string{secret}, []string{"secret(s) require project configuration before execution: " + secret}, "Missing: "+secret
		}
		if !slices.Equal(preflight.RequiredSecrets, []string{secret}) || !slices.Equal(preflight.MissingSecrets, wantMissing) {
			t.Fatalf("%s: required=%v missing=%v, want required [%s] and missing %v", label, preflight.RequiredSecrets, preflight.MissingSecrets, secret, wantMissing)
		}
		var guidance []string
		for _, line := range preflight.Warnings {
			if strings.HasPrefix(line, "secret(s) require project configuration") || strings.HasPrefix(line, "configure project secret(s)") {
				guidance = append(guidance, line)
			}
		}
		if !slices.Equal(guidance, wantGuidance) {
			t.Fatalf("%s: secret guidance = %#v, want %#v", label, guidance, wantGuidance)
		}
		if status, detail := replayPreflightCheckStatus(preflight, "secrets"), replayPreflightCheckDetail(preflight, "secrets"); status != "warn" || detail != wantDetail {
			t.Fatalf("%s: secrets check = %s %q, want warn %q", label, status, detail, wantDetail)
		}
		if !preflight.Valid {
			t.Fatalf("%s: preflight = %+v, want project-secret state never to make it invalid", label, preflight)
		}
	}

	held := repair(configured, false)
	if !held.Readiness.ReplayReady || held.Readiness.UnresolvedBindings != 0 {
		t.Fatalf("readiness = %+v, want replay ready with the project's secret", held.Readiness)
	}
	assertSecrets("configured preview", preservedReplayPreflight(held.Script), configured, false)

	lacking := repair(unconfigured, false)
	if lacking.Readiness.ReplayReady || lacking.Readiness.UnresolvedBindings != 1 {
		t.Fatalf("readiness = %+v, want the missing project secret unresolved", lacking.Readiness)
	}
	assertSecrets("unconfigured preview", preservedReplayPreflight(lacking.Script), unconfigured, true)

	// The protected candidate keeps the preflight the response showed.
	applied := repair(configured, true)
	assertSecrets("configured apply", preservedReplayPreflight(applied.Script), configured, false)
	exact, claim, err := server.claimProtectedImportCandidateSession(httptest.NewRequest(http.MethodPost, "/", nil), enterpriseArchiveSaveProfile, strings.TrimPrefix(applied.CandidateID, importDraftProtectedTokenPrefix), storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	persisted := preservedReplayPreflight(exact.Script)
	clearProtectedImportCandidate(exact)
	server.releaseImportCandidateClaim(claim)
	assertSecrets("protected candidate", persisted, configured, false)
}

// An archive with two unsupported TLS settings in vuser_init: one select_tls apply
// clears both gates, the restamped candidate validates, and the repaired
// draft saves as reviewed.
func TestImportRepairSelectTLSOnValidatedArchiveSavesRepairedDraft(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "tls-archive-test"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range map[string]string{
		"TLSRepair.usr": "[General]\nType=Web\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n",
		"vuser_init.c":  "vuser_init()\n{\n\tweb_set_sockets_option(\"SSL_VERSION\", \"TLS-next\");\n\tweb_url(\"home\", \"URL=https://example.test/\", \"Resource=0\", LAST);\n\tweb_set_sockets_option(\"SSL_VERSION\", \"TLS-future\");\n\treturn 0;\n}\n",
		// The credential makes the draft a protected candidate, as guided
		// repair requires.
		"Action.c":    "Action()\n{\n\tweb_add_header(\"Authorization\", \"Bearer tls-repair-synthetic-token\");\n\tweb_url(\"orders\", \"URL=https://example.test/orders\", \"Resource=0\", LAST);\n\treturn 0;\n}\n",
		"vuser_end.c": "vuser_end()\n{\n\treturn 0;\n}\n",
	} {
		entry, err := writer.Create("TLSRepair/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	validate := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/validate", bytes.NewReader(archive.Bytes()))
	validate.Header.Set("Content-Type", "application/zip")
	validated := httptest.NewRecorder()
	server.mux.ServeHTTP(validated, validate)
	if validated.Code != http.StatusOK {
		t.Fatalf("validate %d: %s", validated.Code, validated.Body.String())
	}
	var review struct {
		Script   dsl.Script `json:"script"`
		Warnings []string   `json:"warnings"`
	}
	if err := json.Unmarshal(validated.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	var gates []string
	_ = dslwalk.Walk(&review.Script, func(ref dslwalk.SurfaceRef) error {
		if !hasExecutionCode(ref.Metadata(), "legacy_tls_replacement") {
			return nil
		}
		gates = append(gates, ref.Path)
		covered := dslwalk.MetadataStrings(ref.Metadata(), "covered_warnings")
		if len(covered) != 1 || !slices.Contains(review.Warnings, covered[0]) {
			t.Errorf("%s covered_warnings = %v, want one of the response warnings %v", ref.Path, covered, review.Warnings)
		}
		return nil
	})
	if len(gates) != 2 {
		t.Fatalf("TLS gates = %v, want one per unsupported TLS setting", gates)
	}
	if prior := preservedReplayPreflight(&review.Script); prior == nil || prior.Valid {
		t.Fatalf("validated preflight = %+v, want the TLS gates to block replay", prior)
	}
	token, _ := review.Script.Metadata.Import.Preserved[importDraftAttestationMetadataKey].(string)
	if !strings.HasPrefix(token, importDraftProtectedTokenPrefix) {
		t.Fatalf("validated draft has no protected candidate: %q", token)
	}

	body, _ := json.Marshal(ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: review.Script.ProjectID, Finding: "legacy_tls_replacement", ActionPath: gates[0], Operation: ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"}})
	apply := httptest.NewRecorder()
	server.mux.ServeHTTP(apply, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/repairs/apply", bytes.NewReader(body)))
	if apply.Code != http.StatusOK {
		t.Fatalf("apply %d: %s", apply.Code, apply.Body.String())
	}
	var applied ImportRepairResponse
	if err := json.Unmarshal(apply.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	for _, finding := range applied.Findings {
		if finding.Code == "legacy_tls_replacement" {
			t.Fatalf("one apply left a TLS gate: %+v", finding)
		}
	}
	preflight := preservedReplayPreflight(applied.Script)
	if preflight == nil || !preflight.Valid || replayPreflightCheckStatus(preflight, "review_gates") != "pass" {
		t.Fatalf("restamped preflight = %+v, want valid once both gates are cleared", preflight)
	}

	draftValue := func(script *dsl.Script) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(script)
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	// The repair consumed the validated candidate, and the save-time digest
	// covers the restamped preflight, so neither the pre-repair draft nor an
	// edited copy of the repaired one is accepted in its place.
	tampered := draftValue(applied.Script)
	tampered["metadata"].(map[string]any)["import"].(map[string]any)["preserved"].(map[string]any)["replay_preflight"].(map[string]any)["valid"] = false
	for label, draft := range map[string]map[string]any{"pre-repair": draftValue(&review.Script), "tampered": tampered} {
		refused := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "tls-repair-" + label, "scripts": []any{draft}})
		if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "import_draft_attestation_invalid") {
			t.Fatalf("%s draft save %d: %s", label, refused.Code, refused.Body.String())
		}
	}
	saved := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "tls-repair-save", "scripts": []any{draftValue(applied.Script)}})
	if saved.Code != http.StatusCreated {
		t.Fatalf("save of the repaired draft %d: %s", saved.Code, saved.Body.String())
	}
	var result struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &result); err != nil || len(result.Scripts) != 1 {
		t.Fatalf("save response %v: %s", err, saved.Body.String())
	}
	stored, ok := server.runner.GetScript(result.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("repaired script was not stored")
	}
	if tls := stored.Script.Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" {
		t.Fatalf("stored TLS = %#v, want the repaired 1.2-1.3 range", tls)
	}
	migrated := 0
	_ = dslwalk.Walk(stored.Script, func(ref dslwalk.SurfaceRef) error {
		if hasExecutionCode(ref.Metadata(), "legacy_tls_replacement") {
			t.Errorf("stored %s still carries legacy_tls_replacement", ref.Path)
		}
		if dslwalk.MetadataString(ref.Metadata(), "migration_change") != "" {
			migrated++
		}
		return nil
	})
	if migrated != 2 {
		t.Fatalf("stored migration evidence on %d steps, want both TLS steps", migrated)
	}
}

// droppedBodiesFidelityBlocker is the converter's blocker for request bodies
// it detected but did not convert, which no guided repair restores.
const droppedBodiesFidelityBlocker = "One or more source request bodies were detected but not converted."

// assertRepairedFidelityReport fails unless report, as returned or preserved,
// has status and exactly blockers, which a report lists sorted.
func assertRepairedFidelityReport(t *testing.T, label string, report any, status string, blockers ...string) {
	t.Helper()
	blockers = slices.Sorted(slices.Values(blockers))
	decoded := fidelityReportValue(report)
	if decoded == nil {
		t.Fatalf("%s fidelity report = %#v, want a report", label, report)
	}
	if decoded.PromotionStatus != status || strings.Join(decoded.Blockers, "\n") != strings.Join(blockers, "\n") {
		t.Fatalf("%s fidelity report = %s %q, want %s %q", label, decoded.PromotionStatus, decoded.Blockers, status, blockers)
	}
}

// The converter's report names archive-level blockers next to the step gates
// it restates as "<code> at <path>". A repair evaluates the gates again and
// keeps every other converter sentence, and it keeps the lr_unmask sentence
// only while a body still has to be restored.
func TestEvaluateRepairedScriptKeepsConverterFidelityBlockers(t *testing.T) {
	gated := dsl.Action{Name: "login", Type: "POST", Method: "POST", Target: "https://example.test/login", Body: "{{secret.WI_SYNTHETIC_UNMASKED_BODY}}", Metadata: map[string]any{"body_gated_by_secret": true, "source_secret_ref": "WI_SYNTHETIC_UNMASKED_BODY"}}
	importreview.ApplyToAction(&gated, []importreview.Finding{{Code: "restored_body_required", ReasonCode: "restored_body_required", Severity: diag.Blocker, Gate: importreview.GateExecution, Resolution: "restore_body"}})
	script := &dsl.Script{Name: "fidelity", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Init:    []dsl.Action{legacyTLSGateForTest("first setting", legacyTLSWarningForTest("2&3")), {Name: "home", Type: "GET", Method: "GET", Target: "https://example.test/"}},
		Actions: []dsl.Action{gated},
	}
	// One request mentions a body the converter could not carry over.
	dropped := []*enterprisearchive.ParsedAction{{Functions: []enterprisearchive.ParsedFunction{{Name: "web_custom_request", RawBlock: `web_custom_request("save", "Method=POST", "Body=", LAST);`}}}}
	converted := enterprisearchive.BuildImportFidelityReport(&enterprisearchive.ConvertResult{Script: script}, nil, nil, dropped)
	assertRepairedFidelityReport(t, "converter", converted, enterprisearchive.ImportPromotionBlocked,
		droppedBodiesFidelityBlocker, secretGatedFidelityBlocker, "legacy_tls_replacement at init[0]", "restored_body_required at actions[0]")
	ensureScriptImportPreserved(script, enterpriseArchiveSaveProfile)["fidelity_report"] = converted

	if _, err := applyImportRepair(script, ImportRepairRequest{ActionPath: "init[0]", Finding: "legacy_tls_replacement", Operation: ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"}}); err != nil {
		t.Fatalf("select_tls: %v", err)
	}
	_, _, _, report, err := evaluateRepairedScript(script, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertRepairedFidelityReport(t, "after select_tls", report, enterprisearchive.ImportPromotionBlocked,
		droppedBodiesFidelityBlocker, secretGatedFidelityBlocker, "restored_body_required")

	body := "username=synthetic-user"
	if _, err := applyImportRepair(script, ImportRepairRequest{ActionPath: "actions[0]", Finding: "restored_body_required", Operation: ImportRepairOperation{Type: "restore_body", Body: &body}}); err != nil {
		t.Fatalf("restore_body: %v", err)
	}
	_, _, readiness, report, err := evaluateRepairedScript(script, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.PromotionReady {
		t.Fatalf("readiness = %+v, want every step gate cleared", readiness)
	}
	assertRepairedFidelityReport(t, "after restore_body", report, enterprisearchive.ImportPromotionBlocked, droppedBodiesFidelityBlocker)
	if _, counted := report["secret_gated"]; counted {
		t.Fatalf("secret_gated = %v after the body was restored, want it omitted", report["secret_gated"])
	}
	if got := scriptImportPromotionBlockers(script); strings.Join(got, "\n") != droppedBodiesFidelityBlocker {
		t.Fatalf("promotion blockers = %q, want only the dropped body", got)
	}
}

// An unrelated repair on an import candidate keeps the converter's dropped-body
// blocker, so the candidate it stores, and the script saved from it, stay
// blocked from promotion and launch.
func TestImportRepairApplyKeepsConverterFidelityBlockers(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "fidelity-blockers"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	draft := &dsl.Script{ID: "fidelity", Name: "fidelity", Version: "1.0", Protocol: "http", ProjectID: "p", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Init:    []dsl.Action{legacyTLSGateForTest("first setting", legacyTLSWarningForTest("2&3")), {Name: "home", Type: "GET", Method: "GET", Target: "https://example.test/"}, legacyTLSGateForTest("second setting", legacyTLSWarningForTest("AUTO"))},
		Actions: []dsl.Action{{Name: "orders", Type: "GET", Method: "GET", Target: "https://example.test/orders"}},
	}
	ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)["fidelity_report"] = map[string]any{
		"dropped_bodies": 1, "promotion_status": "blocked",
		"blockers": []any{droppedBodiesFidelityBlocker, "legacy_tls_replacement at init[0]", "legacy_tls_replacement at init[2]"},
	}
	id, _, err := server.registerImportCandidateSession(httptest.NewRequest("POST", "/", nil), enterpriseArchiveSaveProfile, draft)
	if err != nil {
		t.Fatal(err)
	}
	token := importDraftProtectedTokenPrefix + id
	body, _ := json.Marshal(ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: "p", Finding: "legacy_tls_replacement", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"}})
	w := httptest.NewRecorder()
	server.handleImportRepair(w, httptest.NewRequest("POST", "/api/v1/scripts/import/repairs/apply", bytes.NewReader(body)), true)
	if w.Code != 200 {
		t.Fatalf("apply %d: %s", w.Code, w.Body.String())
	}
	var applied ImportRepairResponse
	if err := json.Unmarshal(w.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if !applied.Readiness.PromotionReady {
		t.Fatalf("readiness = %+v, want both TLS gates cleared", applied.Readiness)
	}
	assertRepairedFidelityReport(t, "response", applied.FidelityReport, enterprisearchive.ImportPromotionBlocked, droppedBodiesFidelityBlocker)

	exact, claim, err := server.claimProtectedImportCandidateSession(httptest.NewRequest("POST", "/", nil), enterpriseArchiveSaveProfile, strings.TrimPrefix(applied.CandidateID, importDraftProtectedTokenPrefix), "p")
	if err != nil {
		t.Fatal(err)
	}
	defer server.releaseImportCandidateClaim(claim)
	defer clearProtectedImportCandidate(exact)
	assertRepairedFidelityReport(t, "stored candidate", exact.Script.Metadata.Import.Preserved["fidelity_report"], enterprisearchive.ImportPromotionBlocked, droppedBodiesFidelityBlocker)
	if got := scriptImportPromotionBlockers(exact.Script); strings.Join(got, "\n") != droppedBodiesFidelityBlocker {
		t.Fatalf("promotion blockers = %q, want the dropped body", got)
	}
}

// Review codes alone never block a report a repair rewrites. A report blocked
// before the repair stays blocked while one remains; any other report,
// including the absent report of a script launch does not hold, ends
// promotable, and clearing the last review code releases a blocked one.
func TestEvaluateRepairedScriptReviewCodesKeepThePriorPromotionStatus(t *testing.T) {
	placeholders := func(report map[string]any) *dsl.Script {
		script := &dsl.Script{Name: "placeholders", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{
				{Name: "cart", Type: "GET", Method: "GET", Target: "https://example.test/cart/{{var.cart}}"},
				{Name: "store", Type: "GET", Method: "GET", Target: "https://example.test/stores/{{var.store}}"},
			},
		}
		if _, err := importreview.RefreshDependencies(script); err != nil {
			t.Fatal(err)
		}
		if report != nil {
			ensureScriptImportPreserved(script, enterpriseArchiveSaveProfile)["fidelity_report"] = report
		}
		return script
	}
	supply := func(t *testing.T, script *dsl.Script, name string) map[string]any {
		t.Helper()
		path := map[string]string{"cart": "actions[0]", "store": "actions[1]"}[name]
		if _, err := applyImportRepair(script, ImportRepairRequest{ActionPath: path, Finding: "missing_producer:" + name, Operation: ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: name, Type: "list", Values: []string{"synthetic-" + name}, Update: "once"}}}); err != nil {
			t.Fatalf("add_parameter %s: %v", name, err)
		}
		_, _, readiness, report, err := evaluateRepairedScript(script, nil)
		if err != nil {
			t.Fatal(err)
		}
		if readiness.PromotionReady == (len(readiness.ReviewCodes) > 0) {
			t.Fatalf("readiness = %+v, want promotion readiness to follow the review codes", readiness)
		}
		return report
	}
	warnings := []any{"missing_producer:cart", "missing_producer:store"}
	for _, tc := range []struct {
		name   string
		report map[string]any
		status string
	}{
		{name: "promotable", report: map[string]any{"promotion_status": "promotable", "warnings": warnings}, status: "promotable"},
		{name: "no report", status: "promotable"},
		{name: "blocked", report: map[string]any{"promotion_status": "blocked", "warnings": warnings}, status: "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := placeholders(tc.report)
			assertRepairedFidelityReport(t, "one placeholder left", supply(t, script, "cart"), tc.status)
			if got := fidelityReportValue(script.Metadata.Import.Preserved["fidelity_report"]).Warnings; !slices.Equal(got, []string{"missing_producer:store"}) {
				t.Fatalf("report warnings = %q, want the placeholder left", got)
			}
			blockers := scriptImportPromotionBlockers(script)
			if want := map[string][]string{"promotable": {}, "blocked": {"Imported script needs import review before promotion: missing_producer:store."}}[tc.status]; !slices.Equal(blockers, want) {
				t.Fatalf("promotion blockers = %q, want %q", blockers, want)
			}
			assertRepairedFidelityReport(t, "no placeholder left", supply(t, script, "store"), "promotable")
			if got := scriptImportPromotionBlockers(script); len(got) != 0 {
				t.Fatalf("promotion blockers = %q, want none once no review code remains", got)
			}
		})
	}
}
