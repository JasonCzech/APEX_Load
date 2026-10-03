package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func managementAPIFixture(t *testing.T) (*Server, storage.Store, *storage.MonitorSource, []storage.RunMonitorJob, ed25519.PrivateKey) {
	t.Helper()
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_MESSAGING_MANAGEMENT_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(key.Seed()))
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	for _, err := range []error{
		store.SaveScript(&dsl.Script{ID: "management-script", ProjectID: "proj-a", Name: "script", Protocol: "http", CreatedAt: now}),
		store.SaveScenario(&storage.Scenario{ID: "management-scenario", Name: "scenario", Duration: "1m", CreatedAt: now}, []storage.ScenarioScript{{ScriptID: "management-script"}}),
		store.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "management-collector", ProjectID: "proj-a", Name: "collector", TokenHash: hashAPIKey("management-token"), Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"kafka-offsets"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	source := storage.MonitorSource{ID: "management-source", ProjectID: "proj-a", Name: "Kafka offsets", CollectorID: "management-collector", Pack: "kafka-offsets", Destination: "management://broker-readonly", IntervalSeconds: 10, Enabled: true, ManagementScope: &analysis.ManagementScope{Version: 1, Kind: analysis.ManagementKindKafka, Provider: "broker-cluster", Isolation: "read_uncommitted", Entities: []analysis.ManagementEntity{{Kafka: &analysis.KafkaManagementEntity{Topic: "orders", Group: "workers", Partition: 0}}}}, ManagementIdentity: &analysis.ManagementIdentity{Mode: "anonymous", Principal: "approved-local-broker", PolicyRevision: "policy-v1"}}
	saved, err := store.(storage.RunMonitorStore).SaveMonitorSource(source, 0)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := saved.ManagementScope.Hash()
	identity, _ := saved.ManagementIdentity.Fingerprint()
	if err = store.(storage.ManagementMonitorStore).SaveCollectorManagementReadiness("proj-a", "management-collector", storage.CollectorManagementReadiness{InstanceID: "instance-1", Capabilities: []string{collectoragent.FeatureMessagingManagementEvidenceV1}, Scopes: []storage.ManagementReadinessScope{{ScopeHash: scope, IdentityHash: identity}}, Packs: []string{"kafka-offsets"}, Destinations: []string{source.Destination}, SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.(storage.RunMonitorStore).SaveMonitorBindings(storage.MonitorBindings{ProjectID: "proj-a", ScenarioID: "management-scenario", Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}, 0); err != nil {
		t.Fatal(err)
	}
	jobs := []storage.RunMonitorJob{}
	for _, run := range []string{"management-run-a", "management-run-b"} {
		if err = store.SaveTest(&storage.TestRecord{ID: run, Name: run, ProjectID: "proj-a", ScenarioID: "management-scenario", Status: "RUNNING", StartedAt: &now, StartTime: now}); err != nil {
			t.Fatal(err)
		}
		bound, err := store.(storage.RunMonitorStore).StartRunMonitors("proj-a", run, "management-scenario", now, now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, bound...)
	}
	return server, store, saved, jobs, key
}

func admittedManagementBatch(t *testing.T, server *Server, leader storage.RunMonitorJob, key ed25519.PrivateKey, observed bool) (collectoragent.RunSampleBatch, collectoragent.ManagementPollGrant) {
	t.Helper()
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + leader.ID + "/management-polls"
	response := collectorRequest(t, server, http.MethodPost, path, "management-token", collectoragent.ManagementPollControl{Operation: "begin", InstanceID: "instance-1", Nonce: "nonce-1"})
	if response.Code != 200 {
		t.Fatalf("begin: %d %s", response.Code, response.Body.String())
	}
	var control collectoragent.ManagementPollResponse
	if err := json.Unmarshal(response.Body.Bytes(), &control); err != nil || control.Grant == nil {
		t.Fatalf("grant: %s %v", response.Body.String(), err)
	}
	grant, err := collectoragent.VerifyManagementPoll(*control.Grant, key.Public().(ed25519.PublicKey), "management-collector", "instance-1", time.Now().UTC(), false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := &analysis.MessagingManagementSnapshot{Version: 1, Kind: analysis.ManagementKindKafka, Provider: "broker-cluster", ScopeHash: grant.ScopeHash, IdentityHash: grant.IdentityHash, SourceID: grant.SourceID, SourceRevision: grant.SourceRevision, CollectorID: grant.CollectorID, CollectorInstanceID: grant.CollectorInstanceID, Generation: grant.Generation, PollID: grant.PollID, PollSequence: grant.PollSequence, StartedAt: now, CompletedAt: now, CapturedAt: now, IntervalSeconds: 10, MissedTicks: "0", ExpectedEntities: 1, Rows: []analysis.MessagingManagementRow{}, Incomplete: true, UnavailableReason: "permission_denied"}
	if observed {
		exact := func(value int64) analysis.ManagementInteger {
			v, e := analysis.ObservedManagementInteger(value)
			if e != nil {
				t.Fatal(e)
			}
			return v
		}
		missing := analysis.ManagementInteger{State: analysis.ManagementStateMissing, Reason: "not_observed"}
		unsupported := analysis.ManagementInteger{State: analysis.ManagementStateUnsupported, Reason: "unsupported"}
		snapshot.Incomplete = false
		snapshot.InventoryComplete = true
		snapshot.UnavailableReason = ""
		snapshot.Rows = []analysis.MessagingManagementRow{{Entity: leader.Source.ManagementScope.Entities[0], State: analysis.ManagementStateObserved, ObservedAt: &now, KafkaOffsets: &analysis.KafkaOffsets{CommittedNext: exact(0), HighWatermarkNext: exact(9007199254740993), LastStableNext: missing, LogEndNext: unsupported, LeaderEpoch: exact(0), CommittedLeaderEpoch: missing}}}
	}
	if err = snapshot.ValidateFor(*leader.Source.ManagementScope, *leader.Source.ManagementIdentity); err != nil {
		t.Fatal(err)
	}
	return collectoragent.RunSampleBatch{Sequence: leader.NextSequence, Points: []analysis.MetricPoint{}, EvidenceVersion: 2, MessagingManagement: snapshot, ManagementGrant: control.Grant}, grant
}

func TestManagementCohortDurabilityUnavailableAndReplay(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(strconv.FormatBool(observed), func(t *testing.T) {
			server, store, source, jobs, key := managementAPIFixture(t)
			batch, grant := admittedManagementBatch(t, server, jobs[0], key, observed)
			path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/samples"
			for i := 0; i < 2; i++ {
				reply := collectorRequest(t, server, http.MethodPost, path, "management-token", batch)
				if reply.Code != 200 {
					t.Fatalf("samples: %d %s", reply.Code, reply.Body.String())
				}
			}
			for _, job := range jobs {
				receipts, err := store.(storage.RunMonitorStore).ListMonitorBatchReceipts("proj-a", job.RunID, "", -1, 10)
				if err != nil || len(receipts) != 1 || receipts[0].SampleCount != 0 {
					t.Fatalf("receipts %+v %v", receipts, err)
				}
				evidence, err := readMonitorEvidence(context.Background(), "proj-a", job.RunID, receipts[0])
				if err != nil || evidence.Attribution.JobID != job.ID {
					t.Fatalf("attribution %+v %v", evidence, err)
				}
				read := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitor-management", nil)
				var response MonitorManagementResponse
				if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &response) != nil || len(response.Records) != 1 {
					t.Fatalf("read %d %s", read.Code, read.Body.String())
				}
				if observed && (!strings.Contains(read.Body.String(), `"9007199254740993"`) || !strings.Contains(read.Body.String(), `"value":"0"`)) {
					t.Fatal("exact counts lost")
				}
				if !observed && (!strings.Contains(read.Body.String(), `"rows":[]`) || !strings.Contains(read.Body.String(), "permission_denied")) {
					t.Fatal("unavailable evidence lost")
				}
			}
			t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
			t.Setenv("APEXLOAD_MESSAGING_MANAGEMENT_PROJECTS", "")
			for _, job := range jobs {
				if err := store.(storage.RunMonitorStore).StopRunMonitors("proj-a", job.RunID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.(storage.RunMonitorStore).DeleteMonitorSource("proj-a", source.ID, source.Revision); err != nil {
				t.Fatal(err)
			}
			reply := collectorRequest(t, server, http.MethodPost, path, "management-token", batch)
			if reply.Code != 200 {
				t.Fatalf("accepted stopped replay: %d %s", reply.Code, reply.Body.String())
			}
			batch.MessagingManagement = batch.MessagingManagement.Clone()
			batch.MessagingManagement.MissedTicks = "1"
			if reply = collectorRequest(t, server, http.MethodPost, path, "management-token", batch); reply.Code != 409 {
				t.Fatalf("changed replay: %d %s", reply.Code, reply.Body.String())
			}
			read := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+jobs[0].RunID+"/monitor-management", nil)
			if read.Code != 200 {
				t.Fatalf("disabled read: %d %s", read.Code, read.Body.String())
			}
			archiveRoot := filepath.Join(resolveArchiveRoot(), "completed")
			if err := os.MkdirAll(archiveRoot, 0700); err != nil {
				t.Fatal(err)
			}
			record := &storage.RunArchiveRecord{TestID: jobs[0].RunID, ArchivePath: archiveRoot, ManifestPath: filepath.Join(archiveRoot, "manifest.json")}
			manifest := &RunArchiveManifest{TestID: jobs[0].RunID, SchemaVersion: archiveSchemaVersion}
			budget, err := newArchiveAdjunctBudgetForRecord(context.Background(), record)
			if err != nil {
				t.Fatal(err)
			}
			defer budget.close()
			if err = server.runner.persistRunMonitorArchive("proj-a", jobs[0].RunID, record, manifest, budget); err != nil {
				t.Fatal(err)
			}
			if manifest.MonitorManagementSnapshotCount != 1 || manifest.MonitorEvidenceCount != 0 || manifest.MonitorManagementVersion != 1 {
				t.Fatalf("manifest %+v", manifest)
			}
			bytes, err := os.ReadFile(filepath.Join(archiveRoot, manifest.MonitorManagementFile))
			if err != nil {
				t.Fatal(err)
			}
			firstCoverage := len(manifest.MonitorCoverage)
			if err = server.runner.persistRunMonitorArchive("proj-a", jobs[0].RunID, record, manifest, budget); err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile(filepath.Join(archiveRoot, manifest.MonitorManagementFile))
			if err != nil || string(second) != string(bytes) || len(manifest.MonitorCoverage) != firstCoverage || manifest.MonitorManagementSnapshotCount != 1 {
				t.Fatalf("archive refresh changed coverage/evidence: %v %+v", err, manifest)
			}
			var archived MonitorManagementRecord
			if err = json.Unmarshal(bytes, &archived); err != nil || archived.Evidence.MessagingManagement.PollID != grant.PollID {
				t.Fatalf("archive %s %v", bytes, err)
			}
		})
	}
}

func TestManagementAdmissionAndSelectionRejectInvalidInputs(t *testing.T) {
	server, store, _, jobs, key := managementAPIFixture(t)
	batch, grant := admittedManagementBatch(t, server, jobs[0], key, true)
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/samples"
	bad := batch
	bad.ManagementGrant = &collectoragent.SignedManagementPoll{Payload: batch.ManagementGrant.Payload + " ", Signature: batch.ManagementGrant.Signature}
	if reply := collectorRequest(t, server, http.MethodPost, path, "management-token", bad); reply.Code != 403 {
		t.Fatalf("tampered grant %d", reply.Code)
	}
	bad = batch
	bad.MessagingManagement = batch.MessagingManagement.Clone()
	bad.MessagingManagement.Generation = "2"
	if reply := collectorRequest(t, server, http.MethodPost, path, "management-token", bad); reply.Code != 400 {
		t.Fatalf("generation %d", reply.Code)
	}
	if reply := collectorRequest(t, server, http.MethodPost, path, "wrong", batch); reply.Code != 401 {
		t.Fatalf("token %d", reply.Code)
	}
	if reply := collectorRequest(t, server, http.MethodPost, path, "management-token", batch); reply.Code != 200 {
		t.Fatalf("valid %d %s", reply.Code, reply.Body.String())
	}
	for _, query := range []string{"limit=101", "cursor=bad", "from=invalid", "from=2026-10-03T01:00:00Z&to=2026-10-03T00:00:00Z", "entity_id=bad"} {
		if reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+jobs[0].RunID+"/monitor-management?"+query, nil); reply.Code != 400 {
			t.Fatalf("query %s %d %s", query, reply.Code, reply.Body.String())
		}
	}
	binding := managementCursorBinding("proj-a", jobs[0].RunID, "", "", time.Time{}, time.Time{})
	receipts, err := store.(storage.RunMonitorStore).ListMonitorBatchReceipts("proj-a", jobs[0].RunID, "", -1, 10)
	if err != nil || len(receipts) != 1 {
		t.Fatal(err)
	}
	cursor := encodeManagementCursor(binding, receipts[0])
	if reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+jobs[1].RunID+"/monitor-management?cursor="+url.QueryEscape(cursor), nil); reply.Code != 400 {
		t.Fatalf("cross-run cursor %d", reply.Code)
	}
	release := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/management-polls"
	if reply := collectorRequest(t, server, http.MethodPost, release, "management-token", collectoragent.ManagementPollControl{Operation: "abandon", InstanceID: "other", PollID: grant.PollID, Generation: grant.Generation, CompletionConfirmed: true}); reply.Code != 403 {
		t.Fatalf("other instance release %d", reply.Code)
	}
}

func TestManagementCredentialVersionPinsContextAndSecret(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	original := managementCredentialVersion(key, "project", "ref", "secret")
	if original != managementCredentialVersion(key, "project", "ref", "secret") {
		t.Fatal("unstable version")
	}
	for _, other := range []string{managementCredentialVersion(key, "other", "ref", "secret"), managementCredentialVersion(key, "project", "other", "secret"), managementCredentialVersion(key, "project", "ref", "rotated")} {
		if other == original {
			t.Fatal("credential version context collision")
		}
	}
}

func TestManagementMissingSigningKeyDoesNotAdmitCollection(t *testing.T) {
	server, store, _, jobs, key := managementAPIFixture(t)
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", "")
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/management-polls"
	reply := collectorRequest(t, server, http.MethodPost, path, "management-token", collectoragent.ManagementPollControl{Operation: "begin", InstanceID: "instance-1", Nonce: "before-key"})
	if reply.Code != 503 {
		t.Fatalf("missing key %d %s", reply.Code, reply.Body.String())
	}
	if poll, err := store.(storage.ManagementCollectionStateStore).GetCurrentManagementPoll("proj-a", "management-source"); err == nil || poll != nil {
		t.Fatalf("missing key acquired source: %+v %v", poll, err)
	}
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(key.Seed()))
	admittedManagementBatch(t, server, jobs[0], key, false)
}

func TestManagementTerminalResolutionPreservesAcceptedCore(t *testing.T) {
	server, _, _, jobs, key := managementAPIFixture(t)
	batch, grant := admittedManagementBatch(t, server, jobs[0], key, false)
	prefix := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID
	if reply := collectorRequest(t, server, http.MethodPost, prefix+"/samples", "management-token", batch); reply.Code != 200 {
		t.Fatalf("publish %d %s", reply.Code, reply.Body.String())
	}
	core, err := analysis.MonitorEvidenceCoreBytes(analysis.MonitorEvidenceEnvelope{Version: 2, Points: []analysis.MetricPoint{}, MessagingManagement: batch.MessagingManagement})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(core)
	for _, operation := range []string{"get", "abandon"} {
		reply := collectorRequest(t, server, http.MethodPost, prefix+"/management-polls", "management-token", collectoragent.ManagementPollControl{Operation: operation, InstanceID: grant.CollectorInstanceID, PollID: grant.PollID, Generation: grant.Generation, CompletionConfirmed: true})
		var control collectoragent.ManagementPollResponse
		if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &control) != nil || control.State != "accepted" || control.Grant == nil {
			t.Fatalf("terminal resolution %d %s", reply.Code, reply.Body.String())
		}
		metadata, err := collectoragent.VerifyManagementPoll(*control.Grant, key.Public().(ed25519.PublicKey), grant.CollectorID, grant.CollectorInstanceID, grant.Deadline.Add(time.Hour), true)
		if err != nil || metadata.AcceptedCoreDigest != hex.EncodeToString(hash[:]) || metadata.Credentials != nil {
			t.Fatalf("metadata %+v %v", metadata, err)
		}
		if _, err = collectoragent.VerifyManagementPoll(*control.Grant, key.Public().(ed25519.PublicKey), grant.CollectorID, grant.CollectorInstanceID, time.Now().UTC(), false); err == nil {
			t.Fatal("accepted terminal metadata admitted observer")
		}
	}
}

func TestManagementCollectionAttestationRequiresExactOwnership(t *testing.T) {
	server, store, _, jobs, key := managementAPIFixture(t)
	_, grant := admittedManagementBatch(t, server, jobs[0], key, false)
	path := "/api/v1/projects/proj-a/monitor-sources/management-source/management-state"
	read := timeslotRequest(t, server, http.MethodGet, path, nil)
	var state ManagementCollectionStateResponse
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &state) != nil || !state.GrantIssued || state.PollID != grant.PollID {
		t.Fatalf("state %d %s", read.Code, read.Body.String())
	}
	request := ManagementCollectionReleaseRequest{PollID: grant.PollID, CollectorID: grant.CollectorID, CollectorInstanceID: grant.CollectorInstanceID, Generation: grant.Generation}
	if reply := timeslotRequest(t, server, http.MethodPost, path, request); reply.Code != 400 {
		t.Fatalf("no attestation %d", reply.Code)
	}
	request.ProcessStopped = true
	request.CollectorInstanceID = "other"
	if reply := timeslotRequest(t, server, http.MethodPost, path, request); reply.Code != 409 {
		t.Fatalf("wrong instance %d", reply.Code)
	}
	request.CollectorInstanceID = grant.CollectorInstanceID
	reply := timeslotRequest(t, server, http.MethodPost, path, request)
	if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &state) != nil || state.State != "abandoned" {
		t.Fatalf("release %d %s", reply.Code, reply.Body.String())
	}
	poll, err := store.(storage.ManagementMonitorStore).GetManagementPoll("proj-a", grant.PollID)
	if err != nil || poll.DrainProof == nil || poll.DrainProof.Kind != "process_stopped" || poll.DrainProof.AuditID == "" {
		t.Fatalf("audit proof %+v %v", poll, err)
	}
}

func TestManagementCredentialPrincipalMatrix(t *testing.T) {
	for _, test := range []struct {
		mode        string
		credentials collectoragent.ManagementCredentials
	}{
		{"sasl_plain", collectoragent.ManagementCredentials{Version: 2, Purpose: collectoragent.ManagementCredentialPurpose, Mode: "sasl", SASL: &collectoragent.ManagementSASLCredentials{Username: "approved", Password: "password", Mechanism: "plain"}}},
		{"sasl_scram", collectoragent.ManagementCredentials{Version: 2, Purpose: collectoragent.ManagementCredentialPurpose, Mode: "sasl", SASL: &collectoragent.ManagementSASLCredentials{Username: "approved", Password: "password", Mechanism: "scram_sha_512"}}},
		{"basic", collectoragent.ManagementCredentials{Version: 2, Purpose: collectoragent.ManagementCredentialPurpose, Mode: "basic", Basic: &collectoragent.ManagementBasicCredentials{Username: "approved", Password: "password"}}},
		{"shared_key", collectoragent.ManagementCredentials{Version: 2, Purpose: collectoragent.ManagementCredentialPurpose, Mode: "shared_key", SharedKey: &collectoragent.ManagementSharedKeyCredentials{KeyName: "approved", Key: "key"}}},
	} {
		t.Run(test.mode, func(t *testing.T) {
			identity := analysis.ManagementIdentity{Mode: test.mode, Principal: "approved", PolicyRevision: "policy-v1"}
			if !managementCredentialMatchesIdentity(test.credentials, identity) {
				t.Fatal("approved principal rejected")
			}
			identity.Principal = "unapproved"
			if managementCredentialMatchesIdentity(test.credentials, identity) {
				t.Fatal("unapproved principal accepted")
			}
		})
	}
	for _, test := range []struct{ identity, credential string }{{"workload_identity", "workload"}, {"managed_identity", "managed"}, {"mtls", "mtls"}, {"anonymous", "anonymous"}} {
		if !managementCredentialMatchesIdentity(collectoragent.ManagementCredentials{Version: 2, Purpose: collectoragent.ManagementCredentialPurpose, Mode: test.credential}, analysis.ManagementIdentity{Mode: test.identity, Principal: "approved", PolicyRevision: "policy-v1"}) {
			t.Fatalf("identity alias rejected %s", test.identity)
		}
	}
}

func TestManagementFreshGrantGETAfterAllRunsStopIsFenced(t *testing.T) {
	server, store, _, jobs, key := managementAPIFixture(t)
	_, grant := admittedManagementBatch(t, server, jobs[0], key, false)
	for _, job := range jobs {
		if err := store.(storage.RunMonitorStore).StopRunMonitors("proj-a", job.RunID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/management-polls"
	reply := collectorRequest(t, server, http.MethodPost, path, "management-token", collectoragent.ManagementPollControl{Operation: "get", InstanceID: grant.CollectorInstanceID, PollID: grant.PollID})
	if reply.Code != http.StatusConflict {
		t.Fatalf("fresh GET granted provider I/O after all runs stopped: %d %s", reply.Code, reply.Body.String())
	}
}

func TestManagementMaintenanceRetainsReceiptsAndReclaimsDeletedRunJournals(t *testing.T) {
	server, store, _, jobs, key := managementAPIFixture(t)
	batch, grant := admittedManagementBatch(t, server, jobs[0], key, false)
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/samples"
	if reply := collectorRequest(t, server, http.MethodPost, path, "management-token", batch); reply.Code != 200 {
		t.Fatalf("publication: %d %s", reply.Code, reply.Body.String())
	}
	now := time.Now().UTC()
	management := store.(storage.ManagementMonitorStore)
	if err := server.pruneManagementPollJournals(context.Background(), now.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := management.GetManagementPoll("proj-a", grant.PollID); err != nil {
		t.Fatal("receipt-referenced journal was pruned", err)
	}
	for _, job := range jobs {
		if err := store.(storage.RunMonitorStore).StopRunMonitors("proj-a", job.RunID, now); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteTest(job.RunID); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.pruneManagementPollJournals(context.Background(), now.Add(6*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := management.GetManagementPoll("proj-a", grant.PollID); err != nil {
		t.Fatal("retry retention window lost", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.pruneManagementPollJournals(cancelled, now.Add(8*24*time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sweep: %v", err)
	}
	for range 3 {
		if err := server.pruneManagementPollJournals(context.Background(), now.Add(8*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := management.GetManagementPoll("proj-a", grant.PollID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("orphan journal retained: %v", err)
	}
}

func TestManagementPartialObjectWriteNeverPublishesAndCleanupReclaimsOrphans(t *testing.T) {
	server, store, _, jobs, key := managementAPIFixture(t)
	batch, _ := admittedManagementBatch(t, server, jobs[0], key, false)
	blockedKey, _ := monitorArtifactKey("proj-a", jobs[1].RunID, nil)
	blocked := filepath.Join(currentArchiveBackend().LocalRoot, "_analysis", filepath.Dir(blockedKey))
	if err := os.MkdirAll(filepath.Dir(blocked), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("controlled write failure"), 0600); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/samples"
	if reply := collectorRequest(t, server, http.MethodPost, path, "management-token", batch); reply.Code != 503 {
		t.Fatalf("partial writer: %d %s", reply.Code, reply.Body.String())
	}
	for _, job := range jobs {
		receipts, err := store.(storage.RunMonitorStore).ListMonitorBatchReceipts("proj-a", job.RunID, "", -1, 10)
		if err != nil || len(receipts) != 0 {
			t.Fatalf("partial cohort receipt: %+v %v", receipts, err)
		}
		current, err := store.(storage.RunMonitorStore).GetRunMonitorJob("proj-a", job.ID)
		if err != nil || current.NextSequence != 0 {
			t.Fatalf("partial sequence advancement: %+v %v", current, err)
		}
	}
	queued, err := store.(storage.AnalysisCleanupStore).ListAnalysisCleanupJobs(time.Now().Add(time.Hour), 50)
	if err != nil || len(queued) != 2 {
		t.Fatalf("writer cleanup journal: %+v %v", queued, err)
	}
	if err = os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	cleanupStore := &analysisCleanupReadFailureStore{SQLiteStore: store.(*storage.SQLiteStore)}
	server.store = cleanupStore
	if err = server.retryAnalysisCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, job := range queued {
		var ref storage.MonitorObjectCleanupReference
		if err = json.Unmarshal([]byte(job.ResourceID), &ref); err != nil {
			t.Fatal(err)
		}
		physical := filepath.Join(currentArchiveBackend().LocalRoot, "_analysis", ref.ArtifactKey)
		if _, err = os.Stat(physical); !os.IsNotExist(err) {
			t.Fatalf("unreferenced object retained: %s %v", ref.ArtifactKey, err)
		}
	}
	if reply := collectorRequest(t, server, http.MethodPost, path, "management-token", batch); reply.Code != 200 {
		t.Fatalf("retry after partial writer: %d %s", reply.Code, reply.Body.String())
	}
	for _, job := range jobs {
		receipts, err := store.(storage.RunMonitorStore).ListMonitorBatchReceipts("proj-a", job.RunID, "", -1, 10)
		if err != nil || len(receipts) != 1 {
			t.Fatalf("atomic retry publication: %+v %v", receipts, err)
		}
	}
}

// A real cleanup transaction may finish after the request has expired. The
// artifact writer must recheck cancellation after that transaction returns.
type managementCleanupHookStore struct {
	*storage.SQLiteStore
	afterQueue func()
}

func (s *managementCleanupHookStore) QueueMonitorObjectCleanup(project, run, key string) error {
	if err := s.SQLiteStore.QueueMonitorObjectCleanup(project, run, key); err != nil {
		return err
	}
	s.afterQueue()
	return nil
}

func TestManagementCancelledAfterCleanupCannotRecreateDeletedProjectArtifacts(t *testing.T) {
	server, store, _, jobs, key := managementAPIFixture(t)
	batch, _ := admittedManagementBatch(t, server, jobs[0], key, false)
	sqlite := store.(*storage.SQLiteStore)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hookCalls := 0
	server.store = &managementCleanupHookStore{SQLiteStore: sqlite, afterQueue: func() {
		hookCalls++
		if err := sqlite.DeleteProject("proj-a"); err != nil {
			t.Fatal(err)
		}
		cleanupServer := &Server{store: &analysisCleanupReadFailureStore{SQLiteStore: sqlite}}
		if err := cleanupServer.retryAnalysisCleanup(context.Background()); err != nil {
			t.Fatal(err)
		}
		pending, err := sqlite.ListAnalysisCleanupJobs(time.Now().Add(time.Hour), 50)
		if err != nil || len(pending) != 0 {
			t.Fatalf("deleted project cleanup not drained: %+v %v", pending, err)
		}
		cancel()
	}}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/samples"
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body))).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer management-token")
	reply := httptest.NewRecorder()
	server.mux.ServeHTTP(reply, req)
	if reply.Code != 503 || hookCalls != 1 {
		t.Fatalf("expired persistence: %d %s, hooks=%d", reply.Code, reply.Body.String(), hookCalls)
	}
	root := filepath.Join(currentArchiveBackend().LocalRoot, "_analysis")
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Errorf("artifact recreated after cleanup: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestManagementCancelledArtifactWriteCreatesNoNamespace(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	dir := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key, _ := monitorArtifactKey("project", "run", []byte("[]"))
	if err := writeMonitorArtifact(ctx, key, []byte("[]")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled writer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "_analysis")); !os.IsNotExist(err) {
		t.Fatalf("cancelled writer created namespace: %v", err)
	}
}

func TestManagementReceiptClockMatchesDurablePrecision(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"fractional", "2026-10-03T00:00:01.123456789Z", "2026-10-03T00:00:01.123456Z"},
		{"no rounding carry", "2026-10-03T00:00:01.999999999Z", "2026-10-03T00:00:01.999999Z"},
		{"already exact", "2026-10-03T00:00:01.123456Z", "2026-10-03T00:00:01.123456Z"},
		{"non UTC source", "2026-10-02T20:00:01.123456789-04:00", "2026-10-03T00:00:01.123456Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := time.Parse(time.RFC3339Nano, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if got := managementReceiptTime(input); got.Format(time.RFC3339Nano) != tc.want || got.Location() != time.UTC {
				t.Fatalf("durable receive clock: %s (%s), want %s UTC", got.Format(time.RFC3339Nano), got.Location(), tc.want)
			}
			if got := input.Format(time.RFC3339Nano); got != tc.input {
				t.Fatalf("source clock changed: %s", got)
			}
		})
	}
}

func TestManagementPollControlRequiresCollectorTokenDespiteBrowserRole(t *testing.T) {
	server, _, _, jobs, key := managementAPIFixture(t)
	enableTrustedHeaderAuth(t, server)
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/management-polls"
	control := collectoragent.ManagementPollControl{Operation: "begin", InstanceID: "instance-1", Nonce: "unauthorized"}
	for _, token := range []string{"", "wrong"} {
		if reply := collectorRequest(t, server, http.MethodPost, path, token, control); reply.Code != 401 {
			t.Fatalf("collector token %q: %d %s", token, reply.Code, reply.Body.String())
		}
	}
	body, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Request-User", "management-browser-admin")
	req.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
	req.Header.Set("X-APEXLoad-Project-Roles", "proj-a="+RoleProjectAdmin)
	reply := httptest.NewRecorder()
	server.mux.ServeHTTP(reply, req)
	if reply.Code != 401 {
		t.Fatalf("browser roles admitted collector poll: %d %s", reply.Code, reply.Body.String())
	}
	// Valid collector authority still reaches signed admission under the same
	// browser auth profile; roles and the collector token remain separate.

	_, grant := admittedManagementBatch(t, server, jobs[0], key, false)
	get := collectoragent.ManagementPollControl{Operation: "get", InstanceID: grant.CollectorInstanceID, PollID: grant.PollID, Generation: grant.Generation}
	body, err = json.Marshal(get)
	if err != nil {
		t.Fatal(err)
	}
	apiKeyReq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	apiKeyReq.Header.Set("Content-Type", "application/json")
	apiKeyReq.Header.Set("X-APEXLoad-API-Key", "management-token")
	apiKeyReply := httptest.NewRecorder()
	server.mux.ServeHTTP(apiKeyReply, apiKeyReq)
	if apiKeyReply.Code != 200 {
		t.Fatalf("collector API-key channel rejected: %d %s", apiKeyReply.Code, apiKeyReply.Body.String())
	}
}
