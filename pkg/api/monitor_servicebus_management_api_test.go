package api

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Exercises the actual controller source, authority and durable evidence routes.
// Provider HTTP/TLS conformance is covered separately by collector fixtures.
func TestServiceBusManagementSourceGrantAndDurableCohort(t *testing.T) {
	server, store, _, _, signingKey := managementAPIFixture(t)
	server.projectSecretStore = secretstore.EnvironmentStore{}
	collectorKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyValue := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	credentials := collectoragent.ManagementCredentials{Version: 2, Purpose: collectoragent.ManagementCredentialPurpose, Mode: "shared_key", SharedKey: &collectoragent.ManagementSharedKeyCredentials{KeyName: "ManageReader", Key: keyValue}}
	encoded, err := json.Marshal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	secretName := engine.ProjectSecretEnvironmentName("proj-a", "SERVICEBUS_MANAGE")
	t.Setenv(secretName, string(encoded))
	packs := []string{"kafka-offsets", "servicebus-runtime"}
	if ok, err := store.(*storage.SQLiteStore).PatchMonitoringCollector("management-collector", "proj-a", nil, nil, nil, &packs); err != nil || !ok {
		t.Fatalf("collector approval: %v %v", ok, err)
	}
	source := storage.MonitorSource{
		ProjectID: "proj-a", Name: "Service Bus runtime", CollectorID: "management-collector", Pack: "servicebus-runtime", Destination: "management://namespace", IntervalSeconds: 30, Enabled: true,
		CollectorPublicKey: base64.StdEncoding.EncodeToString(collectorKey.PublicKey().Bytes()),
		ManagementScope:    &analysis.ManagementScope{Version: 1, Kind: analysis.ManagementKindServiceBus, Provider: "example.servicebus.windows.net", Entities: []analysis.ManagementEntity{{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "Orders/Nested"}}}},
		ManagementIdentity: &analysis.ManagementIdentity{Mode: "shared_key", Principal: "ManageReader", PolicyRevision: "policy-v1", CredentialRef: "SERVICEBUS_MANAGE"},
	}
	sourcePath := "/api/v1/projects/proj-a/monitor-sources"
	created := timeslotRequest(t, server, http.MethodPost, sourcePath, source)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var saved storage.MonitorSource
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.Revision != 1 || saved.ManagementIdentity == nil || !strings.HasPrefix(saved.ManagementIdentity.CredentialVersion, "hmac-v1:") || strings.Contains(created.Body.String(), keyValue) {
		t.Fatal("source receipt lost its identity or disclosed its credential")
	}
	invalid := saved
	invalid.IntervalSeconds = 29
	if reply := timeslotRequest(t, server, http.MethodPut, sourcePath+"/"+saved.ID, invalid); reply.Code != http.StatusBadRequest {
		t.Fatalf("cadence: %d %s", reply.Code, reply.Body.String())
	}
	revised := saved
	revised.Name = "Revised Service Bus runtime"
	update := timeslotRequest(t, server, http.MethodPut, sourcePath+"/"+saved.ID, revised)
	if update.Code != http.StatusOK || json.Unmarshal(update.Body.Bytes(), &revised) != nil || revised.Revision != 2 {
		t.Fatalf("revision: %d %s", update.Code, update.Body.String())
	}
	if reply := timeslotRequest(t, server, http.MethodPut, sourcePath+"/"+saved.ID, saved); reply.Code != http.StatusConflict {
		t.Fatalf("stale revision: %d %s", reply.Code, reply.Body.String())
	}
	saved = revised
	scopeHash, err := saved.ManagementScope.Hash()
	if err != nil {
		t.Fatal(err)
	}
	identityHash, err := saved.ManagementIdentity.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	readiness := storage.CollectorManagementReadiness{InstanceID: "instance-1", Capabilities: []string{collectoragent.FeatureMessagingManagementEvidenceV1}, Scopes: []storage.ManagementReadinessScope{{ScopeHash: scopeHash, IdentityHash: identityHash}}, Packs: []string{"servicebus-runtime"}, Destinations: []string{saved.Destination}, SeenAt: now}
	monitors := store.(storage.RunMonitorStore)
	publishReadiness := func(dest string) {
		t.Helper()
		ad := collectoragent.RunJobReadiness{InstanceID: readiness.InstanceID, Capabilities: readiness.Capabilities, ManagementScopes: []collectoragent.ManagementReadinessScope{{ScopeHash: scopeHash, IdentityHash: identityHash}}, Packs: readiness.Packs, Destinations: []string{dest}, SigningKey: base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))}
		body, err := json.Marshal(ad)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/collectors/management-collector/jobs", nil)
		req.Header.Set("Authorization", "Bearer management-token")
		req.Header.Set("X-APEX-Monitor-Readiness", string(body))
		reply := httptest.NewRecorder()
		server.mux.ServeHTTP(reply, req)
		if reply.Code != http.StatusOK {
			t.Fatalf("readiness request: %d %s", reply.Code, reply.Body.String())
		}
	}
	if _, err := monitors.SaveMonitorBindings(storage.MonitorBindings{ProjectID: "proj-a", ScenarioID: "management-scenario", Sources: []storage.MonitorBinding{{SourceID: saved.ID, Required: true}}}, 1); err != nil {
		t.Fatal(err)
	}
	// Unkeyed and wrong-key advertisements cannot authorize this sealed source.
	for _, destination := range []string{saved.Destination, saved.Destination + "#" + base64.StdEncoding.EncodeToString(make([]byte, 32))} {
		publishReadiness(destination)
		if err := store.(*storage.SQLiteStore).ValidateRunMonitorReadiness("proj-a", "servicebus-pending", "management-scenario", now); err == nil {
			t.Fatal("source accepted mismatched encryption readiness")
		}
	}
	publishReadiness(saved.ReadinessDestination())
	var jobs []storage.RunMonitorJob
	for _, run := range []string{"servicebus-run-a", "servicebus-run-b"} {
		if err := store.SaveTest(&storage.TestRecord{ID: run, Name: run, ProjectID: "proj-a", ScenarioID: "management-scenario", Status: "RUNNING", StartedAt: &now, StartTime: now}); err != nil {
			t.Fatal(err)
		}
		bound, err := monitors.StartRunMonitors("proj-a", run, "management-scenario", now, now.Add(time.Hour))
		if err != nil || len(bound) != 1 {
			t.Fatalf("bound source: %v %v", bound, err)
		}
		jobs = append(jobs, bound[0])
	}
	leader := jobs[0]
	pollPath := "/api/v1/monitoring/collectors/management-collector/jobs/" + leader.ID + "/management-polls"
	response := collectorRequest(t, server, http.MethodPost, pollPath, "management-token", collectoragent.ManagementPollControl{Operation: "begin", InstanceID: "instance-1", Nonce: "servicebus-nonce"})
	var control collectoragent.ManagementPollResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &control) != nil || control.Grant == nil || strings.Contains(response.Body.String(), keyValue) {
		t.Fatalf("sealed grant: %d %s", response.Code, response.Body.String())
	}
	grant, err := collectoragent.VerifyManagementPoll(*control.Grant, signingKey.Public().(ed25519.PublicKey), "management-collector", "instance-1", time.Now().UTC(), false)
	if err != nil || grant.UnavailableReason != "" || grant.SourceRevision != 2 || grant.Pack != "servicebus-runtime" || len(grant.Cohort) != 2 {
		t.Fatalf("owned grant: %v %+v", err, grant)
	}
	opened, err := collectoragent.OpenManagementCredentials(grant, collectorKey.Bytes(), time.Now().UTC())
	if err != nil || opened.SharedKey == nil || opened.SharedKey.KeyName != "ManageReader" || opened.SharedKey.Key != keyValue {
		t.Fatalf("credential identity: %v", err)
	}
	// A rotation cannot substitute credentials into this already pinned source.
	rotated := credentials
	rotatedKey := *credentials.SharedKey
	rotatedKey.Key = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32)))
	rotated.SharedKey = &rotatedKey
	encodedRotation, err := json.Marshal(rotated)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(secretName, string(encodedRotation))
	changed := collectorRequest(t, server, http.MethodPost, pollPath, "management-token", collectoragent.ManagementPollControl{Operation: "get", InstanceID: "instance-1", PollID: grant.PollID})
	var changedControl collectoragent.ManagementPollResponse
	if changed.Code != http.StatusOK || json.Unmarshal(changed.Body.Bytes(), &changedControl) != nil || changedControl.Grant == nil {
		t.Fatalf("rotation response: %d", changed.Code)
	}
	changedGrant, err := collectoragent.VerifyManagementPoll(*changedControl.Grant, signingKey.Public().(ed25519.PublicKey), "management-collector", "instance-1", time.Now().UTC(), false)
	if err != nil || changedGrant.UnavailableReason != "identity_mismatch" || changedGrant.Credentials != nil {
		t.Fatalf("rotation fence: %v", err)
	}
	t.Setenv(secretName, string(encoded))
	observed := func(value int64) analysis.ManagementInteger {
		v, err := analysis.ObservedManagementInteger(value)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	unsupported := analysis.ManagementInteger{State: analysis.ManagementStateUnsupported, Reason: "unsupported"}
	captured := time.Now().UTC()
	runtime := &analysis.ServiceBusRuntime{Total: observed(0), Active: observed(0), Scheduled: observed(0), DeadLetter: observed(0), Transfer: observed(0), TransferDeadLetter: observed(0), SizeBytes: observed(9007199254740993), Deferred: unsupported}
	snapshot := &analysis.MessagingManagementSnapshot{Version: 1, Kind: analysis.ManagementKindServiceBus, Provider: saved.ManagementScope.Provider, ScopeHash: grant.ScopeHash, IdentityHash: grant.IdentityHash, SourceID: grant.SourceID, SourceRevision: grant.SourceRevision, CollectorID: grant.CollectorID, CollectorInstanceID: grant.CollectorInstanceID, Generation: grant.Generation, PollID: grant.PollID, PollSequence: grant.PollSequence, StartedAt: captured, CompletedAt: captured, CapturedAt: captured, IntervalSeconds: 30, MissedTicks: "0", ExpectedEntities: 1, InventoryComplete: true, Rows: []analysis.MessagingManagementRow{{Entity: saved.ManagementScope.Entities[0], State: analysis.ManagementStateObserved, ObservedAt: &captured, ServiceBusRuntime: runtime}}}
	if err := snapshot.ValidateFor(*saved.ManagementScope, *saved.ManagementIdentity); err != nil {
		t.Fatal(err)
	}
	batch := collectoragent.RunSampleBatch{Sequence: leader.NextSequence, Points: []analysis.MetricPoint{}, EvidenceVersion: 2, MessagingManagement: snapshot, ManagementGrant: control.Grant}
	samplesPath := "/api/v1/monitoring/collectors/management-collector/jobs/" + leader.ID + "/samples"
	for range 2 {
		reply := collectorRequest(t, server, http.MethodPost, samplesPath, "management-token", batch)
		if reply.Code != http.StatusOK {
			t.Fatalf("durable retry: %d %s", reply.Code, reply.Body.String())
		}
	}
	for _, job := range jobs {
		read := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitor-management", nil)
		var records MonitorManagementResponse
		if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &records) != nil || len(records.Records) != 1 {
			t.Fatalf("durable evidence: %d %s", read.Code, read.Body.String())
		}
		evidence := records.Records[0].Evidence.MessagingManagement
		if evidence == nil || evidence.Kind != analysis.ManagementKindServiceBus || len(evidence.Rows) != 1 || evidence.Rows[0].ServiceBusRuntime == nil {
			t.Fatal("Service Bus evidence lost its semantic kind")
		}
		actual := evidence.Rows[0].ServiceBusRuntime
		if actual.Active.State != analysis.ManagementStateObserved || actual.Active.Value == nil || *actual.Active.Value != "0" || actual.SizeBytes.Value == nil || *actual.SizeBytes.Value != "9007199254740993" || actual.Deferred.State != analysis.ManagementStateUnsupported || actual.Deferred.Value != nil {
			t.Fatal("observed zero, exact integer or unsupported state changed")
		}
		receipts, err := monitors.ListMonitorBatchReceipts("proj-a", job.RunID, "", -1, 10)
		if err != nil || len(receipts) != 1 {
			t.Fatalf("retry duplicate receipt: %v %v", receipts, err)
		}
	}
}
