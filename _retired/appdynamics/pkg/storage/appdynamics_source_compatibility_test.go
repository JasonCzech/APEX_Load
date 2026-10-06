package storage

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
)

func appDynamicsCollectorSourceFixture(t *testing.T) (*SQLiteStore, MonitorSource, string, time.Time) {
	t.Helper()
	store, source, _ := appDynamicsMonitorFixture(t)
	now := time.Now().UTC()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
	collector := &MonitoringCollector{ID: "appd-compat-collector", ProjectID: source.ProjectID, Name: "APM", TokenHash: "fixture", Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"appdynamics"}}
	if err := store.SaveMonitoringCollector(collector); err != nil {
		t.Fatal(err)
	}
	connection, err := store.GetAppDynamicsConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	connection.CollectionMode, connection.CollectorAgentID = "collector", collector.ID
	if ok, err := store.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
		t.Fatalf("connection update %v %v", ok, err)
	}
	if _, err := store.UpdateAppDynamicsConnectionHealth(connection.ID, source.ProjectID, connection.Revision, "connected", "", appdynamics.Capabilities{Metrics: true}, now); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCollectorRunReadiness(source.ProjectID, collector.ID, []string{"appdynamics", "appdynamics.v1"}, []string{connection.Config.Endpoint + "#" + key}, now); err != nil {
		t.Fatal(err)
	}
	source.ExecutorMode, source.CollectorID, source.CollectorPublicKey = "collector", collector.ID, key
	saved, err := store.SaveMonitorSource(source, source.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return store, *saved, key, now
}

func TestAppDynamicsConnectionEditsRequireCompatibleSourceForNewRuns(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*AppDynamicsConnection)
	}{
		{"execution mode", func(c *AppDynamicsConnection) { c.CollectionMode, c.CollectorAgentID = "direct", "" }},
		{"collector assignment", func(c *AppDynamicsConnection) { c.CollectorAgentID = "different-collector" }},
		{"Controller origin", func(c *AppDynamicsConnection) { c.Config.Endpoint = "https://different-controller.example" }},
	} {
		for _, required := range []bool{false, true} {
			name := change.name + "/optional"
			if required {
				name = change.name + "/required"
			}
			t.Run(name, func(t *testing.T) {
				store, source, _, now := appDynamicsCollectorSourceFixture(t)
				run, _ := store.GetTest("monitor-run")
				run.MonitorBindings.Sources[0].Required = required
				if err := store.SaveTest(run); err != nil {
					t.Fatal(err)
				}
				connection, _ := store.GetAppDynamicsConnection(source.ConnectionID)
				change.edit(connection)
				if ok, err := store.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
					t.Fatalf("connection edit %v %v", ok, err)
				}
				if _, err := store.UpdateAppDynamicsConnectionHealth(connection.ID, source.ProjectID, connection.Revision, "connected", "", appdynamics.Capabilities{Metrics: true}, now); err != nil {
					t.Fatal(err)
				}
				jobs, err := store.StartRunMonitors(source.ProjectID, run.ID, run.ScenarioID, now, now.Add(time.Hour))
				if required {
					if err == nil || !strings.Contains(err.Error(), "connection_source_mismatch") {
						t.Fatalf("required mismatch was not blocked: %+v %v", jobs, err)
					}
				} else if err != nil || len(jobs) != 1 || jobs[0].State != "incomplete" || jobs[0].CoverageReason != "connection_source_mismatch" {
					t.Fatalf("optional mismatch was not explicit: %+v %v", jobs, err)
				}
			})
		}
	}
}

func TestAppDynamicsConnectionEditPreservesActiveJobSnapshot(t *testing.T) {
	store, source, _, now := appDynamicsCollectorSourceFixture(t)
	jobs, err := store.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("initial start %+v %v", jobs, err)
	}
	connection, _ := store.GetAppDynamicsConnection(source.ConnectionID)
	connection.CollectionMode, connection.CollectorAgentID = "direct", ""
	connection.Config.Endpoint = "https://new-controller.example"
	if ok, err := store.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
		t.Fatalf("connection edit %v %v", ok, err)
	}
	current, err := store.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour))
	if err != nil || len(current) != 1 || current[0].State != "collecting" || current[0].ConnectionSnapshot.CollectionMode != "collector" || current[0].ConnectionSnapshot.Config.Endpoint != jobs[0].ConnectionSnapshot.Config.Endpoint || current[0].Source.CollectorID != source.CollectorID {
		t.Fatalf("active immutable assignment changed: %+v %v", current, err)
	}
}

func TestAppDynamicsSourceCannotReuseUnenrolledOrRotatedKey(t *testing.T) {
	store, source, key, now := appDynamicsCollectorSourceFixture(t)
	if err := store.SaveCollectorRunReadiness(source.ProjectID, source.CollectorID, []string{"appdynamics", "appdynamics.v1"}, nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMonitorSource(source, source.Revision); err == nil {
		t.Fatal("cached encryption key bypassed exact destination enrollment")
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	replacement := base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
	if replacement == key {
		t.Fatal("fixture generated duplicate encryption keys")
	}
	if err := store.SaveCollectorRunReadiness(source.ProjectID, source.CollectorID, []string{"appdynamics", "appdynamics.v1"}, []string{source.Destination + "#" + replacement}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour)); err == nil {
		t.Fatal("required source retained a rotated collector encryption key")
	}
	if _, err := store.SaveMonitorSource(source, source.Revision); err == nil {
		t.Fatal("explicit old key silently rebound to a new collector identity")
	}
	source.CollectorPublicKey = ""
	updated, err := store.SaveMonitorSource(source, source.Revision)
	if err != nil || updated.CollectorPublicKey != replacement {
		t.Fatalf("explicit source refresh did not enroll replacement key: %+v %v", updated, err)
	}
	if jobs, err := store.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour)); err != nil || len(jobs) != 1 || jobs[0].State != "collecting" {
		t.Fatalf("updated source was not ready: %+v %v", jobs, err)
	}
}
