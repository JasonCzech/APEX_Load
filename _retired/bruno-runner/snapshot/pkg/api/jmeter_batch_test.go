package api

import (
	"bytes"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/storage"
	"net/http/httptest"
	"os"
	"path/filepath"
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
