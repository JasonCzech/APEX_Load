package api

import (
	"bytes"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/storage"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
)

func batchTestPreview(t *testing.T, stores ...storage.Store) (*Server, map[string]any, []*dsl.Script) {
	t.Helper()
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x61}, 32), KeyID: "batch-test"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper}
	if len(stores) > 0 {
		cfg.Store = stores[0]
	}
	server := NewServer(cfg)
	source, err := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	source = bytes.ReplaceAll(source, []byte(`<stringProp name="sharedAccessKey">***</stringProp>`), []byte(`<stringProp name="sharedAccessKey"></stringProp>`))
	result, err := jmeter.ConvertJMX(source, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.Scripts {
		s.ProjectID = "p"
	}
	w := httptest.NewRecorder()
	server.writeJMeterImportResponse(w, httptest.NewRequest("POST", "/", nil), 200, result, &jmeterImportRequest{Data: source, ProjectID: "p"})
	if w.Code != 200 {
		t.Fatalf("preview %d: %s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return server, response, result.Scripts
}
func decodeBatchIdentity(t *testing.T, v any) jmeterBatchIdentity {
	t.Helper()
	var id jmeterBatchIdentity
	b, _ := json.Marshal(v)
	if err := json.Unmarshal(b, &id); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestJMeterBatchRepairPreviewApplyAndRevisionFence(t *testing.T) {
	server, original, scripts := batchTestPreview(t)
	id := decodeBatchIdentity(t, original["batch"])
	req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
	call := func(apply bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), apply, req)
		return w
	}
	preview := call(false)
	if preview.Code != 200 {
		t.Fatalf("preview %d: %s", preview.Code, preview.Body.String())
	}
	var response map[string]any
	_ = json.Unmarshal(preview.Body.Bytes(), &response)
	req.PreviewDigest = response["preview_digest"].(string)
	if response["review_summary"] == nil || response["preflight"] == nil {
		t.Fatal("repair omitted refreshed review evidence")
	}
	// A fresh controller must recover the protected revision and durable receipt.
	server = NewServer(ServerConfig{BrunoArtifactDir: server.brunoArtifactDir, BrunoArtifactWrapper: server.brunoArtifactWrapper})
	applied := call(true)
	if applied.Code != 200 {
		t.Fatalf("apply %d: %s", applied.Code, applied.Body.String())
	}
	server = NewServer(ServerConfig{BrunoArtifactDir: server.brunoArtifactDir, BrunoArtifactWrapper: server.brunoArtifactWrapper})
	if again := call(true); again.Code != 200 || batchJSONDigest(again.Body.Bytes()) != batchJSONDigest(applied.Body.Bytes()) {
		t.Fatalf("retry not idempotent: %d %s", again.Code, again.Body.String())
	}
	if again := call(false); again.Code != 409 {
		t.Fatalf("stale preview: %d", again.Code)
	}
	_ = json.Unmarshal(applied.Body.Bytes(), &response)
	next := decodeBatchIdentity(t, response["batch"])
	b, claim, err := server.claimJMeterBatch(httptest.NewRequest("POST", "/", nil), next, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer server.releaseImportCandidateClaim(claim)
	if len(b.Scripts) != 3 || len(b.Scenario.Groups) != 3 || b.SourceDigest != batchDigest(b.Source) {
		t.Fatal("batch membership/source changed")
	}
	for i, s := range b.Scripts {
		if !s.Config.JMeter.ContextConfirmed || s.Config.JMeter.SourceGroupID != scripts[i].Config.JMeter.SourceGroupID || len(s.Config.SamplerSchedules) != 1 {
			t.Fatal("lost compatibility state")
		}
	}
	var stale []dsl.Script
	raw, _ := json.Marshal(original["scripts"])
	_ = json.Unmarshal(raw, &stale)
	if err = server.fenceJMeterBatchSave(httptest.NewRequest("POST", "/", nil), stale, b.Scenario); err == nil {
		t.Fatal("superseded batch saved")
	}
}
func TestJMeterBatchSaveSealsAgainstRepairAndRejectsMixedMembership(t *testing.T) {
	server, original, _ := batchTestPreview(t)
	id := decodeBatchIdentity(t, original["batch"])
	var scripts []dsl.Script
	data, _ := json.Marshal(original["scripts"])
	_ = json.Unmarshal(data, &scripts)
	var scenario jmeter.ScenarioDraft
	data, _ = json.Marshal(original["scenario_draft"])
	_ = json.Unmarshal(data, &scenario)
	request := httptest.NewRequest("POST", "/", nil)
	if err := server.fenceJMeterBatchSave(request, scripts[:2], &scenario); err == nil {
		t.Fatal("partial batch saved")
	}
	if err := server.fenceJMeterBatchSave(request, scripts, &scenario); err != nil {
		t.Fatal(err)
	}
	if err := server.fenceJMeterBatchSave(request, scripts, &scenario); err != nil {
		t.Fatalf("save retry %v", err)
	}
	w := httptest.NewRecorder()
	server.processImportRepair(w, request, false, ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}})
	if w.Code != 409 {
		t.Fatalf("repair raced saved batch: %d", w.Code)
	}
}
func TestJMeterBatchPayloadPointer(t *testing.T) {
	got, err := replacePayloadPointer(`{"a/b":{"n":9007199254740993,"values":["old"]}}`, "/a~1b/values/0", "{{param.value}}")
	if err != nil || got != `{"a/b":{"n":9007199254740993,"values":["{{param.value}}"]}}` {
		t.Fatalf("payload: %s %v", got, err)
	}
	if _, err := replacePayloadPointer(`{"x":1} {"y":2}`, "/x", "z"); err == nil {
		t.Fatal("repair discarded trailing payload")
	}
	for _, pointer := range []string{"", "/missing", "/a~1b/values/9", "/a~1b/values/+0", "/a~1b/values/00", "/a~2b"} {
		if _, err = replacePayloadPointer(got, pointer, "x"); err == nil {
			t.Fatalf("invalid pointer %s accepted", pointer)
		}
	}
}

// A reviewed payload correction changes the one selected value. Key order,
// indentation, number spelling and escapes elsewhere are the bytes the source
// sampler sent, so they stay as they were.
func TestJMeterBatchPayloadPointerPreservesOtherBytes(t *testing.T) {
	body := "{\n  \"zeta\": 100.0,\n  \"alpha\": {\"note\": \"a<b & \\u0041\\\"}\",\n    \"items\" : [ 1 , {\"sent\":\"{{param.jmeter_time_1}}T{{param.jmeter_time_2}}Z\", \"k\":\"]\"} ]\n  }\n}\n"
	got, err := replacePayloadPointer(body, "/alpha/items/1/sent", "{{fn.timestamp.iso8601()}}<x>")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(body, `"{{param.jmeter_time_1}}T{{param.jmeter_time_2}}Z"`, `"{{fn.timestamp.iso8601()}}<x>"`, 1)
	if got != want {
		t.Fatalf("payload bytes changed outside the selected value:\n got %q\nwant %q", got, want)
	}
	// encoding/json keeps the last of duplicated keys, so the correction does too.
	got, err = replacePayloadPointer(`{"k":"first","k":"second"}`, "/k", "v")
	if err != nil || got != `{"k":"first","k":"v"}` {
		t.Fatalf("duplicate key: %s %v", got, err)
	}
	if _, err := replacePayloadPointer(`{"k":"v"}`, "/k/inner", "x"); err == nil {
		t.Fatal("pointer through a scalar accepted")
	}
}

func batchJSONDigest(data []byte) string {
	var value any
	_ = json.Unmarshal(data, &value)
	return batchDigest(value)
}

func TestJMeterBatchRepairedSaveTransaction(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveProject(&storage.Project{ID: "p", Name: "Batch"}); err != nil {
		t.Fatal(err)
	}
	server, preview, _ := batchTestPreview(t, store)
	id := decodeBatchIdentity(t, preview["batch"])
	req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
	w := httptest.NewRecorder()
	server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), false, req)
	var response map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	req.PreviewDigest = response["preview_digest"].(string)
	w = httptest.NewRecorder()
	server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), true, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	payload, _ := json.Marshal(map[string]any{"scripts": response["scripts"], "scenario_draft": response["scenario_draft"]})
	w = httptest.NewRecorder()
	server.handleSaveJMeterImport(w, httptest.NewRequest("POST", "/", bytes.NewReader(payload)))
	if w.Code != 201 {
		t.Fatalf("save repaired batch %d: %s", w.Code, w.Body.String())
	}
	var saved struct {
		Scripts []*StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Scripts) != 3 {
		t.Fatal("batch members lost")
	}
	for _, stored := range saved.Scripts {
		if _, active := scriptBatch(stored.Script); active {
			t.Fatal("saved asset retains active batch capability")
		}
		origin := decodeBatchIdentity(t, stored.Script.Metadata.Import.Preserved[jmeterBatchOriginMetadata])
		if origin != decodeBatchIdentity(t, response["batch"]) {
			t.Fatal("batch provenance lost")
		}
	}

	var requested []*dsl.Script
	encoded, _ := json.Marshal(response["scripts"])
	if err := json.Unmarshal(encoded, &requested); err != nil {
		t.Fatal(err)
	}
	for i := range requested {
		requested[i].ID = saved.Scripts[i].ID
	}
	if !recoveredJMeterImportIdentityMatches(server, saved.Scripts, requested, nil, nil) {
		t.Fatal("saved batch provenance prevents receipt recovery")
	}
	// Recovery ignores only response-only projection bookkeeping. Changes
	// to source behavior or its durable review gate must still fence adoption.
	changedBehavior := append([]*dsl.Script(nil), requested...)
	changedBehavior[0] = cloneScriptDefinition(requested[0])
	changedBehavior[0].Actions[0].Target += "/changed"
	if recoveredJMeterImportIdentityMatches(server, saved.Scripts, changedBehavior, nil, nil) {
		t.Fatal("changed source behavior passed batch receipt recovery")
	}
	changedGate := append([]*dsl.Script(nil), requested...)
	changedGate[0] = cloneScriptDefinition(requested[0])
	changedGate[0].Actions[0].Metadata["execution_blocker"] = "changed_source_gate"
	if recoveredJMeterImportIdentityMatches(server, saved.Scripts, changedGate, nil, nil) {
		t.Fatal("changed durable source gate passed batch receipt recovery")
	}
	previous := batchJSONDigest(w.Body.Bytes())
	w = httptest.NewRecorder()
	server.handleSaveJMeterImport(w, httptest.NewRequest("POST", "/", bytes.NewReader(payload)))
	if w.Code != 201 || previous != batchJSONDigest(w.Body.Bytes()) {
		t.Fatalf("save retry changed assets: %d %s", w.Code, w.Body.String())
	}
}

func TestJMeterBatchIdentityCannotBeStrippedFromChild(t *testing.T) {
	server, preview, _ := batchTestPreview(t)
	var scripts []dsl.Script
	data, _ := json.Marshal(preview["scripts"])
	_ = json.Unmarshal(data, &scripts)
	delete(scripts[0].Metadata.Import.Preserved, jmeterBatchMetadata)
	claim, err := server.claimImportDraftAttestation(httptest.NewRequest("POST", "/", nil), &scripts[0])
	if claim != nil {
		server.releaseImportDraftAttestation(claim)
	}
	if err == nil {
		t.Fatal("stripped parent identity bypassed batch fencing")
	}
}

func TestJMeterBatchOperationsPreserveSourceAndSchedules(t *testing.T) {
	server, preview, _ := batchTestPreview(t)
	id := decodeBatchIdentity(t, preview["batch"])
	b, claim, err := server.claimJMeterBatch(httptest.NewRequest("POST", "/", nil), id, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer server.releaseImportCandidateClaim(claim)
	sourceHash, scenarioHash := b.SourceDigest, batchDigest(b.Scenario)
	group := func(i int) string { return b.Scripts[i].Config.JMeter.SourceGroupID }
	operations := []JMeterBatchOperation{
		{Type: "normalize_oauth_options", SourceGroupID: group(0)},
		{Type: "bind_kafka_credentials", SourceGroupID: group(0), SecretName: "KAFKA_SECRET", ClientIDSecret: "KAFKA_ID"},
		{Type: "bind_eventhubs_credentials", SourceGroupID: group(1), SecretName: "EH_EAST"},
		{Type: "bind_eventhubs_credentials", SourceGroupID: group(2), SecretName: "EH_WEST"},
		{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true},
		{Type: "eventhubs_property", SourceGroupID: group(1), SourceNodeID: b.Scripts[1].Actions[0].Metadata["source_node_id"].(string), PropertyName: "trace-id", UUID: true},
	}
	for _, op := range operations {
		if err = applyJMeterBatchOperation(b, op); err != nil {
			t.Fatalf("%s: %v", op.Type, err)
		}
	}
	if b.SourceDigest != sourceHash || batchDigest(b.Source) != sourceHash || batchDigest(b.Scenario) != scenarioHash {
		t.Fatal("repair altered immutable source/workload")
	}
	if b.Scripts[1].Config.EventHubs.ConnectionStringSecret == b.Scripts[2].Config.EventHubs.ConnectionStringSecret {
		t.Fatal("east/west credentials collapsed")
	}
	for _, s := range b.Scripts {
		if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(s); len(issues) > 0 {
			t.Fatalf("invalid repaired script: %v", issues)
		}
		data, _ := json.Marshal(s)
		if !bytes.Contains(data, []byte("jmeter_messaging_compatibility_unverified")) {
			t.Fatal("repair cleared strict gate")
		}
	}
}

func TestJMeterBatchConcurrentRepairsPublishOneRevision(t *testing.T) {
	server, preview, scripts := batchTestPreview(t)
	id := decodeBatchIdentity(t, preview["batch"])
	requests := []ImportRepairRequest{
		{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}},
		{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "normalize_oauth_options", SourceGroupID: scripts[0].Config.JMeter.SourceGroupID}}},
	}
	for i := range requests {
		w := httptest.NewRecorder()
		server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), false, requests[i])
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var response map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &response)
		requests[i].PreviewDigest = response["preview_digest"].(string)
	}
	start := make(chan struct{})
	codes := make(chan int, 2)
	for _, req := range requests {
		go func(req ImportRepairRequest) {
			<-start
			w := httptest.NewRecorder()
			server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), true, req)
			codes <- w.Code
		}(req)
	}
	close(start)
	one, two := <-codes, <-codes
	if !((one == 200 && two == 409) || (one == 409 && two == 200)) {
		t.Fatalf("concurrent repair status: %d %d", one, two)
	}
}

func TestJMeterBatchPreviewDigestBindsResultAndActorScope(t *testing.T) {
	server, preview, _ := batchTestPreview(t)
	id := decodeBatchIdentity(t, preview["batch"])
	req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}, PreviewDigest: "unreviewed"}
	w := httptest.NewRecorder()
	server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), true, req)
	if w.Code != 409 {
		t.Fatalf("unreviewed apply: %d", w.Code)
	}
	req.ProjectID = "another-project"
	w = httptest.NewRecorder()
	server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), false, req)
	if w.Code != 409 {
		t.Fatalf("cross-project preview: %d", w.Code)
	}
}

func TestJMeterBatchRepairAndSaveCannotBothPublish(t *testing.T) {
	server, response, scripts := batchTestPreview(t)
	id := decodeBatchIdentity(t, response["batch"])
	req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
	w := httptest.NewRecorder()
	server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), false, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var preview map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	req.PreviewDigest = preview["preview_digest"].(string)
	b, claim, err := server.claimJMeterBatch(httptest.NewRequest("POST", "/", nil), id, "p")
	if err != nil {
		t.Fatal(err)
	}
	server.releaseImportCandidateClaim(claim)
	defer clearJMeterBatchSource(b)
	values := make([]dsl.Script, len(scripts))
	for i, script := range scripts {
		values[i] = *script
	}
	start := make(chan struct{})
	saveResult := make(chan error, 1)
	repairResult := make(chan int, 1)
	go func() {
		<-start
		saveResult <- server.fenceJMeterBatchSave(httptest.NewRequest("POST", "/", nil), values, b.Scenario)
	}()
	go func() {
		<-start
		w := httptest.NewRecorder()
		server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), true, req)
		repairResult <- w.Code
	}()
	close(start)
	saveErr, repairCode := <-saveResult, <-repairResult
	if (saveErr == nil) == (repairCode == 200) {
		t.Fatalf("expected exactly one publication: save=%v repair=%d", saveErr, repairCode)
	}
	receipt, err := server.openBatchReceipt(httptest.NewRequest("POST", "/", nil), id.Revision, "p")
	if err != nil {
		t.Fatal(err)
	}
	if saveErr == nil && receipt.Kind != "save" || repairCode == 200 && receipt.Kind != "repair" {
		t.Fatal("publication receipt disagrees with winner")
	}
}

func TestJMeterBatchChildrenCannotUseStandaloneMutations(t *testing.T) {
	server, response, _ := batchTestPreview(t)
	data, _ := json.Marshal(response["scripts"])
	var scripts []dsl.Script
	if err := json.Unmarshal(data, &scripts); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", nil)
	for i := range scripts {
		if claim, err := server.claimImportDraftAttestation(r, &scripts[i]); err == nil {
			server.releaseImportDraftAttestation(claim)
			t.Fatal("standalone save consumed batch child")
		}
		if claim, err := server.claimImportDraftMutation(r, &scripts[i]); err == nil {
			server.releaseImportDraftAttestation(claim)
			t.Fatal("standalone mutation consumed batch child")
		}
		claim, err := server.claimImportDraftAttestation(withJMeterBatchSave(r), &scripts[i])
		if err != nil {
			t.Fatalf("whole-batch handler cannot claim child: %v", err)
		}
		server.releaseImportDraftAttestation(claim)
	}
}

func TestJMeterBatchCreateScriptRejectsPartialSaveWithoutToken(t *testing.T) {
	server, response, _ := batchTestPreview(t)
	data, _ := json.Marshal(response["scripts"])
	var scripts []dsl.Script
	if err := json.Unmarshal(data, &scripts); err != nil {
		t.Fatal(err)
	}
	for _, removeToken := range []bool{false, true} {
		if removeToken {
			stripImportDraftAttestation(&scripts[0])
		}
		body, _ := json.Marshal(map[string]any{"script": scripts[0]})
		w := httptest.NewRecorder()
		server.handleCreateScript(w, httptest.NewRequest("POST", "/api/v1/scripts", bytes.NewReader(body)))
		if w.Code != 409 {
			t.Fatalf("partial save status=%d", w.Code)
		}
	}
}

// JMeter formats __time in the JVM's default zone. Confirming the reference
// context may name that zone (for example the source timezone picked at
// import) instead of forcing UTC over it.
func TestJMeterBatchReferenceContextAcceptsSourceTimezone(t *testing.T) {
	newBatch := func(zone string) *jmeterBatchCandidate {
		return &jmeterBatchCandidate{Scripts: []*dsl.Script{{
			Protocol: "kafka",
			Config:   dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{SourceGroupID: "g1", Timezone: zone}},
			Parameters: []dsl.Parameter{
				{Name: "jmeter_time_1", Type: "date_time", Format: "yyyy-MM-dd", Timezone: zone},
				{Name: "seq", Type: "counter"},
			},
			Actions: []dsl.Action{{Name: "send", Type: "kafka_produce", Metadata: map[string]any{
				"execution_blocker": "import_review_required", "execution_blockers": []string{"import_review_required"},
				"review_gate": "jmeter_reference_context_confirmation_required", "review_reasons": []string{"jmeter_reference_context_confirmation_required"},
			}}},
		}}}
	}
	for _, zone := range []string{"America/Chicago", "UTC"} {
		b := newBatch("America/Chicago")
		if err := applyJMeterBatchOperation(b, JMeterBatchOperation{Type: "confirm_reference_context", Timezone: zone, ConfirmNoOverrides: true}); err != nil {
			t.Fatalf("confirm %s: %v", zone, err)
		}
		s := b.Scripts[0]
		if !s.Config.JMeter.ContextConfirmed || s.Config.JMeter.Timezone != zone || s.Parameters[0].Timezone != zone || s.Parameters[1].Timezone != "" {
			t.Fatalf("confirm %s left config %+v parameters %+v", zone, s.Config.JMeter, s.Parameters)
		}
		if reasons, _ := s.Actions[0].Metadata["review_reasons"].([]string); len(reasons) != 0 {
			t.Fatalf("confirm %s kept review reasons %v", zone, reasons)
		}
	}
	for _, op := range []JMeterBatchOperation{
		{Type: "confirm_reference_context", Timezone: "Mars/Olympus", ConfirmNoOverrides: true},
		{Type: "confirm_reference_context", Timezone: "Local", ConfirmNoOverrides: true},
		{Type: "confirm_reference_context", Timezone: "", ConfirmNoOverrides: true},
		{Type: "confirm_reference_context", Timezone: "UTC"},
	} {
		if err := applyJMeterBatchOperation(newBatch(""), op); err == nil {
			t.Fatalf("confirmation %+v accepted", op)
		}
	}
}

// An import converted with time_mode=utc built its clock parameters to send
// true UTC. Confirming a local zone must not quietly rewrite them; it is
// refused, naming the UTC choice, while UTC still confirms. A stored batch
// carries the preserved mode as decoded JSON.
func TestJMeterBatchReferenceContextKeepsUTCTimeMode(t *testing.T) {
	newBatch := func(resolution any) *jmeterBatchCandidate {
		return &jmeterBatchCandidate{Scripts: []*dsl.Script{{
			Protocol: "kafka",
			Config:   dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{SourceGroupID: "g1", Timezone: "UTC"}},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"time_resolution": resolution}}},
			Parameters: []dsl.Parameter{
				{Name: "jmeter_time_1", Type: "date_time", Format: "2006-01-02T15:04:05.000Z", Timezone: "UTC"},
			},
			Actions: []dsl.Action{{Name: "send", Type: "kafka_produce", Metadata: map[string]any{
				"review_gate": "jmeter_reference_context_confirmation_required", "review_reasons": []string{"jmeter_reference_context_confirmation_required"},
			}}},
		}}}
	}
	for _, resolution := range []any{
		map[string]string{"mode": "utc", "timezone": "America/Chicago"},
		map[string]any{"mode": "utc", "timezone": "America/Chicago"},
	} {
		b := newBatch(resolution)
		err := applyJMeterBatchOperation(b, JMeterBatchOperation{Type: "confirm_reference_context", Timezone: "America/Chicago", ConfirmNoOverrides: true})
		if err == nil || !strings.Contains(err.Error(), "time mode utc") {
			t.Fatalf("confirming America/Chicago on a utc-mode import = %v, want a refusal naming the UTC choice", err)
		}
		if s := b.Scripts[0]; s.Config.JMeter.ContextConfirmed || s.Config.JMeter.Timezone != "UTC" || s.Parameters[0].Timezone != "UTC" {
			t.Fatalf("refused confirmation changed config %+v parameters %+v", s.Config.JMeter, s.Parameters)
		}
		if err := applyJMeterBatchOperation(b, JMeterBatchOperation{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}); err != nil {
			t.Fatalf("confirming UTC on a utc-mode import: %v", err)
		}
		if s := b.Scripts[0]; !s.Config.JMeter.ContextConfirmed || s.Parameters[0].Timezone != "UTC" {
			t.Fatalf("UTC confirmation left config %+v parameters %+v", s.Config.JMeter, s.Parameters)
		}
	}
}

// A confirmed zone is stored in its canonical IANA spelling. A UTC alias is
// stored as UTC, which a time_mode=utc import accepts; a case variant that only
// a case-insensitive filesystem (macOS) loads is refused on every host, since a
// Linux generator cannot load it.
func TestJMeterBatchReferenceContextStoresCanonicalZone(t *testing.T) {
	newBatch := func() *jmeterBatchCandidate {
		return &jmeterBatchCandidate{Scripts: []*dsl.Script{{
			Protocol: "kafka",
			Config:   dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{SourceGroupID: "g1", Timezone: "UTC"}},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"time_resolution": map[string]any{"mode": "utc"}}}},
			Parameters: []dsl.Parameter{
				{Name: "jmeter_time_1", Type: "date_time", Format: "2006-01-02T15:04:05.000Z", Timezone: "UTC"},
			},
		}}}
	}
	for _, alias := range []string{"Etc/UTC", "utc", "Zulu"} {
		b := newBatch()
		if err := applyJMeterBatchOperation(b, JMeterBatchOperation{Type: "confirm_reference_context", Timezone: alias, ConfirmNoOverrides: true}); err != nil {
			t.Fatalf("confirm %s on a utc-mode import: %v", alias, err)
		}
		if s := b.Scripts[0]; s.Config.JMeter.Timezone != "UTC" || s.Parameters[0].Timezone != "UTC" {
			t.Fatalf("confirm %s stored config %+v parameters %+v, want UTC", alias, s.Config.JMeter, s.Parameters)
		}
	}
	for _, variant := range []string{"america/chicago", "AMERICA/CHICAGO", "America/chicago"} {
		b := newBatch()
		b.Scripts[0].Metadata = nil
		if err := applyJMeterBatchOperation(b, JMeterBatchOperation{Type: "confirm_reference_context", Timezone: variant, ConfirmNoOverrides: true}); err == nil {
			t.Fatalf("confirm %s accepted, storing %q", variant, b.Scripts[0].Config.JMeter.Timezone)
		}
	}
}

// The messaging preview shows each script as it will be saved: payloads, hub
// names and token URLs are not credentials, and the converted credentials are
// already {{secret.*}} references.
func TestJMeterBatchPreviewShowsNonCredentialFieldsAndGateGuidance(t *testing.T) {
	_, response, scripts := batchTestPreview(t)
	raw, _ := json.Marshal(response["scripts"])
	// The preserved source review is a copy of the JMX text, in which the
	// converter masks credential values (a disabled PLAIN JAAS password) in
	// place. Everything else in a script is what will be saved and run.
	var generic []map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	for i, script := range generic {
		metadata, _ := script["metadata"].(map[string]any)
		imported, _ := metadata["import"].(map[string]any)
		preserved, _ := imported["preserved"].(map[string]any)
		delete(preserved, "source_review")
		executable, _ := json.Marshal(script)
		if strings.Contains(string(executable), "[REDACTED]") {
			t.Fatalf("batch preview masked fields of script %d outside the source review copy", i)
		}
	}
	var previewed []dsl.Script
	_ = json.Unmarshal(raw, &previewed)
	if len(previewed) != len(scripts) {
		t.Fatalf("previewed %d scripts, want %d", len(previewed), len(scripts))
	}
	for i := range previewed {
		if previewed[i].Actions[0].Body != scripts[i].Actions[0].Body || previewed[i].Actions[0].Body == "" {
			t.Fatalf("script %d body differs from the draft that will be saved", i)
		}
	}
	raw, _ = json.Marshal(response["repair_suggestions"])
	var suggestions []scriptImportRepairSuggestion
	_ = json.Unmarshal(raw, &suggestions)
	operations := map[string]bool{}
	unclearable := map[string]bool{}
	for _, suggestion := range suggestions {
		operations[suggestion.BatchOperation] = true
		if suggestion.Clearable != nil && !*suggestion.Clearable {
			unclearable[suggestion.Code] = true
		}
	}
	for _, operation := range []string{"confirm_reference_context", "bind_kafka_credentials", "bind_eventhubs_credentials"} {
		if !operations[operation] {
			t.Errorf("no suggestion points at %s in %+v", operation, suggestions)
		}
	}
	if !unclearable["jmeter_messaging_compatibility_unverified"] {
		t.Errorf("the messaging conformance gate is not marked as not clearable: %+v", suggestions)
	}
	raw, _ = json.Marshal(response["diagnostics"])
	var diagnostics []struct {
		Code       string `json:"code"`
		Detail     string `json:"detail"`
		Resolution string `json:"resolution"`
	}
	_ = json.Unmarshal(raw, &diagnostics)
	explained := 0
	for _, diagnostic := range diagnostics {
		if strings.HasPrefix(diagnostic.Code, "jmeter_") && strings.HasPrefix(diagnostic.Detail, "Resolve imported behavior before replay: ") {
			t.Errorf("diagnostic %s still shows the raw code: %q", diagnostic.Code, diagnostic.Detail)
		}
		// The meaning is in detail and what clears it in resolution.
		if diagnostic.Code == "jmeter_messaging_compatibility_unverified" && diagnostic.Detail != "" &&
			strings.Contains(diagnostic.Resolution, "cannot run") && !strings.Contains(diagnostic.Detail, diagnostic.Resolution) {
			explained++
		}
	}
	if explained == 0 {
		t.Errorf("no messaging diagnostic says plainly the plan cannot run yet: %+v", diagnostics)
	}
	raw, _ = json.Marshal(response["secret_binding_requirements"])
	var requirements []SecretBindingRequirement
	_ = json.Unmarshal(raw, &requirements)
	eventHubs := 0
	for _, requirement := range requirements {
		if requirement.Path == "/config/eventhubs/connection_string" {
			eventHubs++
			if !strings.Contains(requirement.Context, "Endpoint=sb://") || !strings.Contains(requirement.Context, "SharedAccessKey=") {
				t.Errorf("Event Hubs requirement does not describe the connection string: %q", requirement.Context)
			}
		}
	}
	if eventHubs == 0 {
		t.Error("no Event Hubs credential requirement")
	}
}

func TestJMeterTimestampGateSuggestsReconversion(t *testing.T) {
	source, err := os.ReadFile("../convert/jmeter/testdata/split_pay_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	result, err := jmeter.ConvertJMX(source, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	NewServer(ServerConfig{}).writeJMeterImportResponse(w, httptest.NewRequest("POST", "/", nil), 200, result, &jmeterImportRequest{Data: source})
	if w.Code != 200 {
		t.Fatalf("import %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		RepairSuggestions []scriptImportRepairSuggestion `json:"repair_suggestions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, suggestion := range response.RepairSuggestions {
		if suggestion.Code == "jmeter_timestamp_timezone_required" {
			if len(suggestion.Reinspect) == 0 || suggestion.Reinspect[0].Field != "time_mode" {
				t.Fatalf("timestamp suggestion has no re-conversion option: %+v", suggestion)
			}
			return
		}
	}
	t.Fatalf("no timestamp suggestion in %+v", response.RepairSuggestions)
}

// review_gated_steps counts steps. A messaging step carries several gate
// reasons at once, and the reason count used to be reported as the step count.
func TestJMeterCompatReportCountsGatedSteps(t *testing.T) {
	source, err := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	source = bytes.ReplaceAll(source, []byte(`<stringProp name="sharedAccessKey">***</stringProp>`), []byte(`<stringProp name="sharedAccessKey"></stringProp>`))
	result, err := jmeter.ConvertJMX(source, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	steps := 0
	for _, script := range result.Scripts {
		steps += len(script.Actions)
	}
	report := buildJMeterProtocolCompatReport(result)
	if report.ReviewGatedSteps == 0 || report.ReviewGatedSteps > steps {
		t.Fatalf("review_gated_steps = %d for %d converted steps", report.ReviewGatedSteps, steps)
	}
	if report.PromotionStatus != "review_required" {
		t.Fatalf("promotion status = %q, want review_required", report.PromotionStatus)
	}
	// Every messaging step is held until its replay is verified, so the plan
	// imports for review only; it used to claim "executable".
	if report.ReviewGatedSteps != steps || report.ImportMode != "review_only" {
		t.Fatalf("import_mode = %q with %d of %d steps gated, want review_only with all gated", report.ImportMode, report.ReviewGatedSteps, steps)
	}
}

func TestJMeterImportModeFollowsGatedSteps(t *testing.T) {
	for _, tc := range []struct {
		gated, steps int
		want         string
	}{{0, 0, "unsupported"}, {3, 3, "review_only"}, {1, 3, "executable"}, {0, 2, "executable"}} {
		if got := jmeterImportMode(tc.gated, tc.steps); got != tc.want {
			t.Fatalf("jmeterImportMode(%d, %d) = %q, want %q", tc.gated, tc.steps, got, tc.want)
		}
	}
}

// A scenario group with no user count or ramp-up of its own takes the thread
// group's, which the converter keeps in each script (source_population), so
// a scenario built from saved scripts does not fall back to 10 users and no
// ramp-up. Values the group sets itself win.
func TestJMeterScenarioGroupSeedsPopulationFromScript(t *testing.T) {
	source, err := os.ReadFile("../convert/jmeter/testdata/split_pay_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	result, err := jmeter.ConvertJMX(source, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ScenarioDraft == nil || len(result.ScenarioDraft.Groups) != 1 {
		t.Fatalf("scenario draft = %+v, want one group", result.ScenarioDraft)
	}
	// Round-trip the script as a saved one is read: numbers decode as float64.
	payload, err := json.Marshal(result.Scripts)
	if err != nil {
		t.Fatal(err)
	}
	var saved []*dsl.Script
	if err := json.Unmarshal(payload, &saved); err != nil {
		t.Fatal(err)
	}
	draft := cloneJMeterScenarioDraft(result.ScenarioDraft)
	draft.Groups[0].VUsers, draft.Groups[0].RampUp = 0, ""
	request, err := buildJMeterScenarioRequest(saved, draft)
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Groups[0]; got.VUsers != 50 || got.RampUp != "1s" {
		t.Fatalf("seeded group = %d users, ramp-up %q; want the thread group's 50 and 1s", got.VUsers, got.RampUp)
	}

	draft.Groups[0].VUsers, draft.Groups[0].RampUp = 7, "30s"
	request, err = buildJMeterScenarioRequest(saved, draft)
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Groups[0]; got.VUsers != 7 || got.RampUp != "30s" {
		t.Fatalf("explicit group = %d users, ramp-up %q; want its own 7 and 30s", got.VUsers, got.RampUp)
	}
}

// The OpenAPI schema for the batch repair routes is generated from
// JMeterBatchRepairResponse, so every key the handler sends must be a field of
// that type, or the documented contract and generated client types omit it.
func TestJMeterBatchRepairResponseKeysAreDocumented(t *testing.T) {
	documented := map[string]bool{}
	typ := reflect.TypeOf(JMeterBatchRepairResponse{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		documented[name] = true
	}
	server, original, _ := batchTestPreview(t)
	id := decodeBatchIdentity(t, original["batch"])
	req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
	for _, apply := range []bool{false, true} {
		w := httptest.NewRecorder()
		server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), apply, req)
		if w.Code != 200 {
			t.Fatalf("repair (apply=%v) %d: %s", apply, w.Code, w.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		for key := range response {
			if !documented[key] {
				t.Errorf("repair (apply=%v) sends %q, which JMeterBatchRepairResponse does not document", apply, key)
			}
		}
		req.PreviewDigest, _ = response["preview_digest"].(string)
	}
}
