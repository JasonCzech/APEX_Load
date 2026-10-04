package storage

import (
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"testing"
	"time"
)

func TestDynatracePlatformOnlyProfilesUseIndependentCredentials(t *testing.T) {
	s, _, now := monitorFixture(t)
	for _, kind := range []string{"logs", "spans", "metrics", "events", "smartscape"} {
		t.Run(kind, func(t *testing.T) {
			c := &DynatraceConnection{ID: "platform-" + kind, ProjectID: "monitor-p", Name: kind, Enabled: true, PlatformURL: "https://fixture.apps.dynatrace.com", PlatformAuthType: DynatracePlatformAuthToken, PlatformTokenSecret: "platform-private"}
			switch kind {
			case "logs":
				c.LogsEnabled = true
			case "spans":
				c.SpansEnabled = true
			case "metrics":
				c.GrailMetricsEnabled = true
			case "events":
				c.GrailEventsEnabled = true
			case "smartscape":
				c.TopologyMode = "smartscape"
			}
			if err := s.CreateDynatraceConnection(c); err != nil {
				t.Fatal(err)
			}
			if c.ConfigurationStatus() != "ready" || c.Readiness() != "unverified" {
				t.Fatal(c.ConfigurationStatus(), c.Readiness())
			}
			caps := dynatrace.Capabilities{Logs: true, Spans: true, GrailMetrics: true, GrailEvents: true, Smartscape: true}
			if ok, err := s.UpdateDynatraceConnectionHealthIfRevision(c.ID, c.ProjectID, c.Revision, "connected", "", caps, now); err != nil || !ok {
				t.Fatal(ok, err)
			}
			stored, err := s.GetDynatraceConnection(c.ID)
			if err != nil || stored.Readiness() != "ready" || stored.GrailEventsEnabled != c.GrailEventsEnabled || DynatraceDestination(stored.Snapshot()) != stored.PlatformURL {
				t.Fatal(stored, err)
			}
			stored.PlatformTokenSecret = ""
			stored.EnvironmentTokenSecret = "wrong-credential-plane"
			if stored.Readiness() != "unconfigured" {
				t.Fatal("environment token authorized platform", stored.Readiness())
			}
		})
	}
}

func TestDynatraceRaisedQueryScanLimitUsesImmutableBudget(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	c, err := s.GetDynatraceConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	c.MaxScanBytes = 100_000_000_000
	c.MaxRunScanBytes = 300_000_000_000
	c.DrainReserveBytes = 50_000_000_000
	if ok, err := s.UpdateDynatraceConnectionIfRevision(c, c.Revision); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := s.UpdateDynatraceConnectionHealthIfRevision(c.ID, c.ProjectID, c.Revision, "connected", "", dynatrace.Capabilities{Metrics: true}, start); err != nil || !ok {
		t.Fatal(ok, err)
	}
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ReserveDynatraceScan(source.ProjectID, jobs[0].ID, "raised-limit", 100_000_000_000, start); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := s.ReserveDynatraceScan(source.ProjectID, jobs[0].ID, "too-large", 100_000_000_001, start); err == nil {
		t.Fatal("unbounded reservation accepted")
	}
}

func TestDynatraceSourceDefaultsToDirectExecution(t *testing.T) {
	s, source, _ := dynatraceMonitorFixture(t)
	source.ID, source.Name, source.Revision = "dt-default-direct", "Default direct", 0
	source.ExecutorMode, source.CollectorID, source.CollectorPublicKey = "", "", ""
	source.IntervalSeconds = 0
	saved, err := s.SaveMonitorSource(source, 0)
	if err != nil || saved == nil || saved.ExecutorMode != "direct" || saved.IntervalSeconds != 60 || saved.CollectorID != "" {
		t.Fatalf("omitted Dynatrace execution mode must select direct execution: %+v %v", saved, err)
	}
	stored, err := s.ListMonitorSources(source.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, got := range stored {
		if got.ID == source.ID {
			found = got.ExecutorMode == "direct" && got.Destination != "" && got.CollectorID == ""
		}
	}
	if !found {
		t.Fatal("resolved direct executor was not persisted")
	}
	source.ID, source.ExecutorMode = "dt-explicit-collector", "collector"
	if _, err = s.SaveMonitorSource(source, 0); err == nil {
		t.Fatal("explicit collector execution accepted without an assignment")
	}
	source.ID, source.Pack, source.Dynatrace, source.ExecutorMode = "legacy-default-collector", "go-runtime", nil, ""
	source.ConnectionID, source.IntervalSeconds = "", 60
	if _, err = s.SaveMonitorSource(source, 0); err == nil {
		t.Fatal("omitted legacy pack executor no longer requires its collector")
	}
}
