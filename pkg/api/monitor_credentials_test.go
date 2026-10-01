package api

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestMonitorCredentialLeaseAndEncryptionReadiness(t *testing.T) {
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	public, signer, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(signer.Seed()))
	collectorKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	server, store := newTimeslotServer(t)
	server.projectSecretStore = secretstore.EnvironmentStore{}
	t.Setenv(engine.ProjectSecretEnvironmentName("proj-a", "MONITOR_LOGIN"), `{"username":"monitor","password":"sensitive-password"}`)
	now := time.Now().UTC()
	for _, err := range []error{
		store.SaveScript(&dsl.Script{ID: "credential-script", Name: "script", Protocol: "http", ProjectID: "proj-a", CreatedAt: now}),
		store.SaveScenario(&storage.Scenario{ID: "credential-scenario", Name: "scenario", Duration: "1m", CreatedAt: now}, []storage.ScenarioScript{{ScriptID: "credential-script"}}),
		store.SaveTest(&storage.TestRecord{ID: "credential-run", Name: "run", ProjectID: "proj-a", ScenarioID: "credential-scenario", Status: "RUNNING", StartedAt: &now, StartTime: now}),
		store.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "credential-collector", Name: "collector", ProjectID: "proj-a", TokenHash: hashAPIKey("monitor-token"), Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"ssh-linux"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	monitors := store.(storage.RunMonitorStore)
	source, err := monitors.SaveMonitorSource(storage.MonitorSource{ID: "credential-source", Name: "SSH", ProjectID: "proj-a", CollectorID: "credential-collector", Pack: "ssh-linux", Destination: "ssh://server:22", CredentialRef: "MONITOR_LOGIN", CollectorPublicKey: base64.StdEncoding.EncodeToString(collectorKey.PublicKey().Bytes()), IntervalSeconds: 15, Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = monitors.SaveMonitorBindings(storage.MonitorBindings{ScenarioID: "credential-scenario", ProjectID: "proj-a", Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := validateRequiredMonitorCredentials(context.Background(), monitors, nil, "proj-a", "credential-scenario"); err == nil {
		t.Fatal("required credential accepted without secret store")
	}
	if err := validateRequiredMonitorCredentials(context.Background(), monitors, server.projectSecretStore, "proj-a", "credential-scenario"); err != nil {
		t.Fatal(err)
	}
	// Unencrypted destination advertisement cannot satisfy encrypted readiness.
	if err = monitors.SaveCollectorRunReadiness("proj-a", "credential-collector", []string{"ssh-linux"}, []string{source.Destination}, now); err == nil {
		t.Fatal("missing key accepted in readiness")
	}
	if err = monitors.SaveCollectorRunReadiness("proj-a", "credential-collector", []string{"ssh-linux"}, []string{source.ReadinessDestination()}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = monitors.StartRunMonitors("proj-a", "credential-run", "credential-scenario", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reply := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/credential-collector/jobs", "monitor-token", nil)
	if reply.Code != 200 || strings.Contains(reply.Body.String(), "sensitive-password") {
		t.Fatalf("credential response: %d", reply.Code)
	}
	var envelopes []collectoragent.SignedRunJob
	if err = json.Unmarshal(reply.Body.Bytes(), &envelopes); err != nil || len(envelopes) != 1 {
		t.Fatalf("jobs: %v", err)
	}
	job, err := collectoragent.VerifyRunJob(envelopes[0], public, "credential-collector", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	server.projectSecretStore = nil
	unavailable := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/credential-collector/jobs", "monitor-token", nil)
	var failedJobs []collectoragent.SignedRunJob
	if unavailable.Code != 200 || json.Unmarshal(unavailable.Body.Bytes(), &failedJobs) != nil || len(failedJobs) != 1 {
		t.Fatal("one unavailable credential blocked collector job polling")
	}
	blocked, err := collectoragent.VerifyRunJob(failedJobs[0], public, "credential-collector", time.Now().UTC())
	if err != nil || blocked.UnavailableReason == "" || blocked.Credentials != nil {
		t.Fatal("unavailable job did not carry signed explicit coverage failure")
	}
	credentials, err := collectoragent.OpenCredentials(job, collectorKey.Bytes(), time.Now().UTC())
	if err != nil || credentials.Password != "sensitive-password" {
		t.Fatalf("credential lease: %v", err)
	}
}
