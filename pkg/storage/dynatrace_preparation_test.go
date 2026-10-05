package storage

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"strings"
	"testing"
	"time"
)

func TestDynatracePreparedCredentialsAndRunningSnapshotRemainImmutable(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	queued := &TestRecord{ID: "dt-secret-prepared", Name: "queued", ProjectID: source.ProjectID, Status: "QUEUED", MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}}
	if err = s.SaveTest(queued); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	prep, err := s.CreatePreparation(Preparation{ID: "dt-secret-prep", ProjectID: source.ProjectID, TestID: queued.ID, ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "dt-secret-prep", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	prep, err = s.TransitionPreparation(prep.ID, "preparing", "ready", prep.Revision, prep.OwnerFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Monitors == nil || prep.Monitors.DynatraceConnections[source.ID] == nil {
		t.Fatal("preparation did not freeze its Dynatrace connection")
	}
	body, err := json.Marshal(prep)
	if err != nil {
		t.Fatal(err)
	}
	var recovered Preparation
	if err = json.Unmarshal(body, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Monitors.DynatraceConnections[source.ID].EnvironmentTokenSecret != "dt-private-reference" {
		t.Fatal("prepared credential reference lost on restart")
	}
	consumers, err := s.ListDynatraceSecretConsumers(source.ProjectID, "DT_PRIVATE_REFERENCE")
	if err != nil || len(consumers) != 3 {
		t.Fatalf("consumers %+v %v", consumers, err)
	}
	cancelled, err := s.InvalidateDynatraceSecretPreparations(source.ProjectID, "dt.private.reference", now)
	if err != nil || len(cancelled) != 1 || cancelled[0].ID != prep.ID {
		t.Fatalf("secret cancellation %+v %v", cancelled, err)
	}
	job, _ := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if job.DynatraceSnapshot.EnvironmentTokenSecret != "dt-private-reference" || job.State != "collecting" {
		t.Fatal("secret rotation mutated active immutable snapshot")
	}
	if _, err = s.TransitionPreparation(prep.ID, "preparing", "ready", prep.Revision, prep.OwnerFence, nil); err == nil {
		t.Fatal("stale worker reauthorized changed credentials")
	}
}

func TestDynatraceConfigurationChangesCancelOnlyUnconsumedPreparations(t *testing.T) {
	for _, action := range []string{"source_update", "source_delete", "connection_delete"} {
		t.Run(action, func(t *testing.T) {
			s, source, start := dynatraceMonitorFixture(t)
			jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			queued := &TestRecord{ID: "configuration-prepared", ProjectID: source.ProjectID, Name: "queued", Status: "QUEUED", MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}}
			if err := s.SaveTest(queued); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			prep, err := s.CreatePreparation(Preparation{ID: "configuration-preparation", ProjectID: source.ProjectID, TestID: queued.ID, ExecutionRevision: strings.Repeat("b", 64), IdempotencyKey: "configuration-preparation", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			prep, err = s.TransitionPreparation(prep.ID, "preparing", "ready", prep.Revision, prep.OwnerFence, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch action {
			case "source_update":
				source.Name = "changed"
				_, err = s.SaveMonitorSource(source, source.Revision)
			case "source_delete":
				err = s.DeleteMonitorSource(source.ProjectID, source.ID, source.Revision)
			case "connection_delete":
				_, err = s.DeleteDynatraceConnectionIfRevision(source.ConnectionID, source.ProjectID, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			stored, err := s.GetPreparation(prep.ID)
			if err != nil || stored.State != "cancelled" {
				t.Fatal(stored, err)
			}
			job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
			if err != nil || job.Source.Name != "DT" || job.DynatraceSnapshot.Revision != 1 || job.State != "collecting" {
				t.Fatal("active immutable run changed", job, err)
			}
		})
	}
}

func TestDynatracePreparationFreezesFreshSameRevisionVerification(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	c, err := s.GetDynatraceConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateDynatraceConnectionHealthIfRevision(c.ID, c.ProjectID, c.Revision, "unverified", "", dynatrace.Capabilities{}, start); err != nil || !ok {
		t.Fatal(ok, err)
	}
	legacy, err := s.EnsureLegacyDynatraceMonitorSource(c.ProjectID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := &TestRecord{ID: "dt-unverified-legacy-run", ProjectID: c.ProjectID, Name: "legacy", Status: "QUEUED", ScenarioID: "monitor-scenario", MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: legacy.ID, Required: true}}}}
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	prep, err := s.CreatePreparation(Preparation{ID: "dt-unverified-prep", ProjectID: c.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("c", 64), IdempotencyKey: "dt-unverified-prep", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if prep.Monitors.DynatraceConnections[legacy.ID].Capabilities.Metrics {
		t.Fatal("preparation invented verification")
	}
	if ok, err := s.UpdateDynatraceConnectionHealthIfRevision(c.ID, c.ProjectID, c.Revision, "connected", "verified", dynatrace.Capabilities{Metrics: true, VerifiedOrigins: []string{c.EnvironmentURL}}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	prep, err = s.TransitionPreparation(prep.ID, "preparing", "ready", prep.Revision, prep.OwnerFence, nil)
	if err != nil {
		t.Fatal("health-only verification invalidated immutable configuration", err)
	}
	frozen := prep.Monitors.DynatraceConnections[legacy.ID]
	if frozen.VerifiedRevision != c.Revision || !frozen.Capabilities.Metrics {
		t.Fatal("verification was not frozen at readiness", frozen)
	}
	run.Status = "INITIALIZING"
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	prep, err = s.ConsumePreparation(prep.ID, prep.Revision, prep.OwnerFence, "tester", "Tester")
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.GetDynatraceConnection(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Name = "Edited after consume"
	if ok, err := s.UpdateDynatraceConnectionIfRevision(c, c.Revision); err != nil || !ok {
		t.Fatal(ok, err)
	}
	jobs, err := s.StartRunMonitors(c.ProjectID, run.ID, run.ScenarioID, start, start.Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].DynatraceSnapshot.Revision != frozen.Revision || !jobs[0].DynatraceSnapshot.Capabilities.Metrics {
		t.Fatal("consumed verification depended on later connection health", jobs, err)
	}
}
