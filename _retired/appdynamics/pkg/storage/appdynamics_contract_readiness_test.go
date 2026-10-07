package storage

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
)

func TestAppDynamicsCollectorNeedsTypedContractForStartAndInspection(t *testing.T) {
	store, _, now := monitorFixture(t)
	public, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(public.PublicKey().Bytes())
	origin := "https://apm.example.com"
	collector := &MonitoringCollector{ID: "typed-appd", ProjectID: "monitor-p", Name: "APM", TokenHash: "hash", Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"appdynamics"}}
	if err := store.SaveMonitoringCollector(collector); err != nil {
		t.Fatal(err)
	}
	connection := &AppDynamicsConnection{ID: "typed-connection", ProjectID: "monitor-p", Name: "APM", Enabled: true, CollectionMode: "collector", CollectorAgentID: collector.ID, Config: appdynamics.Config{Endpoint: origin, AccountName: "account", AuthType: "basic"}, UsernameSecretName: "user", PasswordSecretName: "pass"}
	if err := store.CreateAppDynamicsConnection(connection); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAppDynamicsConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", appdynamics.Capabilities{Metrics: true}, now); err != nil {
		t.Fatal(err)
	}
	source := MonitorSource{ID: "typed-source", ProjectID: "monitor-p", Name: "APM", Enabled: true, Pack: "appdynamics", ExecutorMode: "collector", ConnectionID: connection.ID, CollectorID: collector.ID, CollectorPublicKey: key, Destination: origin, IntervalSeconds: 60, AppDynamics: &appdynamics.Selection{ProjectID: "monitor-p", Applications: []string{"1"}, Metrics: []appdynamics.MetricSelection{{ApplicationID: "1", Name: "calls", Path: "calls", Unit: "count", Statistic: "sum"}}}}
	// Sources can be stored with incomplete collector readiness; required Start
	// and connection inspection each independently enforce the typed contract.
	if err := store.SaveCollectorRunReadiness(source.ProjectID, collector.ID, []string{"appdynamics"}, []string{origin + "#" + key}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	run, _ := store.GetTest("monitor-run")
	run.MonitorBindings = &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}
	if err := store.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartRunMonitors(source.ProjectID, run.ID, "monitor-scenario", now, now.Add(time.Hour)); err == nil {
		t.Fatal("legacy collector pack granted typed provider run")
	}
	if _, err := store.AppDynamicsCollectorPublicKey(source.ProjectID, collector.ID, origin, now); err == nil {
		t.Fatal("legacy collector pack authorized inspection")
	}
	if err := store.SaveCollectorRunReadiness(source.ProjectID, collector.ID, []string{"appdynamics", "appdynamics.v1"}, []string{origin + "#" + key}, now); err != nil {
		t.Fatal(err)
	}
	if enrolled, err := store.AppDynamicsCollectorPublicKey(source.ProjectID, collector.ID, origin, now); err != nil || enrolled != key {
		t.Fatalf("typed enrollment %q %v", enrolled, err)
	}
	if jobs, err := store.StartRunMonitors(source.ProjectID, run.ID, "monitor-scenario", now, now.Add(time.Hour)); err != nil || len(jobs) != 1 {
		t.Fatalf("typed start %+v %v", jobs, err)
	}
	if _, err := store.AppDynamicsCollectorPublicKey(source.ProjectID, collector.ID, origin, now.Add(91*time.Second)); err == nil {
		t.Fatal("expired typed readiness authorized inspection")
	}
}
