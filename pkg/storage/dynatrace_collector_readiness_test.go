package storage

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"testing"
	"time"
)

func TestDynatraceCollectorRequiresTypedContractAndEveryOriginKey(t *testing.T) {
	s, _, now := monitorFixture(t)
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
	origin := "https://fixture.live.dynatrace.com"
	platform := "https://fixture.apps.dynatrace.com"
	collector := &MonitoringCollector{ID: "dt-collector", ProjectID: "monitor-p", Name: "DT", TokenHash: "hash", Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"dynatrace"}}
	if err := s.SaveMonitoringCollector(collector); err != nil {
		t.Fatal(err)
	}
	connection := &DynatraceConnection{ID: "dt-collector-connection", ProjectID: "monitor-p", Name: "DT", Enabled: true, CollectionMode: "collector", CollectorAgentID: collector.ID, EnvironmentURL: origin, PlatformURL: platform, MetricsEnabled: true, LogsEnabled: true, EnvironmentTokenSecret: "env", PlatformAuthType: DynatracePlatformAuthToken, PlatformTokenSecret: "platform"}
	if err := s.CreateDynatraceConnection(connection); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateDynatraceConnectionHealthIfRevision(connection.ID, connection.ProjectID, connection.Revision, "connected", "", dynatrace.Capabilities{Metrics: true, Logs: true}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	selection := dynatrace.Selection{ProjectID: connection.ProjectID, Metrics: []dynatrace.MetricSelection{{Name: "cpu", MetricSelector: "builtin:host.cpu.usage", Aggregation: "avg"}}}
	source := MonitorSource{ID: "dt-collector-source", ProjectID: connection.ProjectID, Name: "DT", Enabled: true, Pack: "dynatrace", ExecutorMode: "collector", ConnectionID: connection.ID, CollectorID: collector.ID, IntervalSeconds: 60, Dynatrace: &selection}
	if err := s.SaveCollectorRunReadiness(source.ProjectID, collector.ID, []string{"dynatrace"}, []string{origin + "#" + key}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMonitorSource(source, 0); err == nil {
		t.Fatal("platform origin without assigned encryption identity accepted")
	}
	if err := s.SaveCollectorRunReadiness(source.ProjectID, collector.ID, []string{"dynatrace"}, []string{origin + "#" + key, platform + "#" + key}, now); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveMonitorSource(source, 0)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetTest("monitor-run")
	run.MonitorBindings = &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRunMonitors(source.ProjectID, run.ID, "monitor-scenario", now, now.Add(time.Hour)); err == nil {
		t.Fatal("unversioned collector accepted")
	}
	if _, err := s.DynatraceCollectorPublicKey(source.ProjectID, collector.ID, origin, now); err == nil {
		t.Fatal("unversioned collector authorized inspection")
	}
	if err := s.SaveCollectorRunReadiness(source.ProjectID, collector.ID, []string{"dynatrace", "dynatrace.v1"}, []string{origin + "#" + key, platform + "#" + key}, now); err != nil {
		t.Fatal(err)
	}
	if value, err := s.DynatraceCollectorPublicKey(source.ProjectID, collector.ID, origin, now); err != nil || value != key {
		t.Fatal(value, err)
	}
	if jobs, err := s.StartRunMonitors(source.ProjectID, run.ID, "monitor-scenario", now, now.Add(time.Hour)); err != nil || len(jobs) != 1 || jobs[0].Source.CollectorPublicKey != key {
		t.Fatal(jobs, err)
	}
	if _, err := s.DynatraceCollectorPublicKey(source.ProjectID, collector.ID, origin, now.Add(91*time.Second)); err == nil {
		t.Fatal("stale enrollment accepted")
	}
	rotated, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rotatedKey := base64.StdEncoding.EncodeToString(rotated.PublicKey().Bytes())
	if err := s.SaveCollectorRunReadiness(source.ProjectID, collector.ID, []string{"dynatrace", "dynatrace.v1"}, []string{origin + "#" + rotatedKey, platform + "#" + rotatedKey}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMonitorSource(*saved, saved.Revision); err == nil {
		t.Fatal("source retained a stale encryption key")
	}
}

func TestDynatraceSourceRejectsUnapprovedCapabilitiesAndRuntimeCursor(t *testing.T) {
	s, source, _ := dynatraceMonitorFixture(t)
	selection := *source.Dynatrace
	selection.Problems = true
	source.Dynatrace = &selection
	if _, err := s.SaveMonitorSource(source, source.Revision); err == nil {
		t.Fatal("unapproved capability accepted")
	}
	selection.Problems = false
	selection.Cursor = "private-upstream-token"
	if err := source.Validate(); err == nil {
		t.Fatal("public source retained continuation token")
	}
	selection.Cursor = ""
	selection.QueryOffset = 1
	if err := source.Validate(); err == nil {
		t.Fatal("public source retained runtime query offset")
	}
}
