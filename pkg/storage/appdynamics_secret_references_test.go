package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
)

func TestAppDynamicsSecretConsumersAndPreparationInvalidation(t *testing.T) {
	store, source, start := appDynamicsMonitorFixture(t)
	connection, err := store.GetAppDynamicsConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	connection.ClientIDSecretName, connection.ClientSecretSecretName = "shared.key", "shared-key"
	connection.UsernameSecretName, connection.PasswordSecretName, connection.EventsAPIKeySecretName = "shared_key", "SHARED.KEY", "sHaReD-KEY"
	if updated, err := store.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !updated {
		t.Fatalf("update %v %v", updated, err)
	}
	if _, err = store.UpdateAppDynamicsConnectionHealth(connection.ID, source.ProjectID, connection.Revision, "connected", "", appdynamics.Capabilities{Metrics: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v %v", jobs, err)
	}
	queued := &TestRecord{ID: "secret-prepared", Name: "queued", ProjectID: source.ProjectID, Status: "QUEUED", MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}}
	if err = store.SaveTest(queued); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	prep, err := store.CreatePreparation(Preparation{ID: "secret-prep", ProjectID: source.ProjectID, TestID: queued.ID, ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "secret-prep", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	prep, err = store.TransitionPreparation(prep.ID, "preparing", "ready", prep.Revision, prep.OwnerFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	consumers, err := store.ListAppDynamicsSecretConsumers(source.ProjectID, "SHARED_KEY")
	if err != nil || len(consumers) != 3 {
		t.Fatalf("consumers %+v %v", consumers, err)
	}
	for _, consumer := range consumers {
		if consumer.ReferenceCount != 5 {
			t.Fatalf("missing a credential field: %+v", consumer)
		}
	}
	if other, err := store.ListAppDynamicsSecretConsumers("different-project", "SHARED_KEY"); err != nil || len(other) != 0 {
		t.Fatalf("cross project consumers %+v %v", other, err)
	}
	cancelled, err := store.InvalidateAppDynamicsSecretPreparations(source.ProjectID, "unrelated", now)
	if err != nil || len(cancelled) != 0 {
		t.Fatalf("unrelated cancellation %+v %v", cancelled, err)
	}
	cancelled, err = store.InvalidateAppDynamicsSecretPreparations(source.ProjectID, "SHARED_KEY", now)
	if err != nil || len(cancelled) != 1 || cancelled[0].ID != prep.ID {
		t.Fatalf("cancellation %+v %v", cancelled, err)
	}
	current, _ := store.GetPreparation(prep.ID)
	if current.State != "cancelled" || current.Revision != prep.Revision+1 {
		t.Fatalf("preparation remained authorized: %+v", current)
	}
	connection, _ = store.GetAppDynamicsConnection(source.ConnectionID)
	if connection.VerifiedRevision != 0 || connection.ConfigurationStatus() != "unverified" {
		t.Fatalf("connection remained verified: %+v", connection)
	}
	job, _ := store.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if job.ConnectionSnapshot.ClientSecretSecretName != "shared-key" || job.State != "collecting" {
		t.Fatal("secret change mutated a running snapshot")
	}
	if _, err := store.TransitionPreparation(prep.ID, "preparing", "ready", prep.Revision, prep.OwnerFence, nil); err == nil {
		t.Fatal("stale preparation worker reauthorized changed credentials")
	}
}

func TestConsumedAppDynamicsPreparationRetainsCredentialsBeforeJobsStart(t *testing.T) {
	store, source, _ := appDynamicsMonitorFixture(t)
	now := time.Now().UTC()
	connection, err := store.GetAppDynamicsConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	connection.PasswordSecretName = "Frozen.Password"
	if ok, err := store.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
		t.Fatalf("connection alias update %v %v", ok, err)
	}
	if _, err := store.UpdateAppDynamicsConnectionHealth(connection.ID, source.ProjectID, connection.Revision, "connected", "", appdynamics.Capabilities{Metrics: true}, now); err != nil {
		t.Fatal(err)
	}
	run := &TestRecord{ID: "consumed-secret-run", Name: "consumed", ProjectID: source.ProjectID, Status: "QUEUED", MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}}
	if err := store.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	preparation, err := store.CreatePreparation(Preparation{ID: "consumed-secret-prep", ProjectID: source.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "consumed-secret", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	preparation, err = store.TransitionPreparation(preparation.ID, "preparing", "ready", preparation.Revision, preparation.OwnerFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "INITIALIZING"
	if err = store.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	preparation, err = store.ConsumePreparation(preparation.ID, preparation.Revision, preparation.OwnerFence, "starter", "Starter")
	if err != nil {
		t.Fatal(err)
	}
	// The current connection no longer refers to the frozen credential. The
	// consumed preparation must still prevent an exclusive replacement.
	connection, err = store.GetAppDynamicsConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	connection.PasswordSecretName = "replacement"
	if ok, err := store.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
		t.Fatalf("connection update %v %v", ok, err)
	}
	consumers, err := store.ListAppDynamicsSecretConsumers(source.ProjectID, "FROZEN_PASSWORD")
	if err != nil || len(consumers) != 1 || consumers[0].ResourceType != "preparation" || consumers[0].ResourceID != preparation.ID {
		t.Fatalf("consumed authorization gap: %+v %v", consumers, err)
	}
	if cancelled, err := store.InvalidateAppDynamicsSecretPreparations(source.ProjectID, "frozen-password", now); err != nil || len(cancelled) != 0 {
		t.Fatalf("consumed preparation was invalidated: %+v %v", cancelled, err)
	}
	jobs, err := store.StartRunMonitors(source.ProjectID, run.ID, "", now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("start %+v %v", jobs, err)
	}
	consumers, err = store.ListAppDynamicsSecretConsumers(source.ProjectID, "FROZEN_PASSWORD")
	if err != nil || len(consumers) != 1 || consumers[0].ResourceType != "run_monitor_job" || jobs[0].ConnectionSnapshot.PasswordSecretName != "Frozen.Password" {
		t.Fatalf("frozen job authorization gap: %+v %v", consumers, err)
	}
}

func TestAppDynamicsSecretReferenceCountCanonicalNames(t *testing.T) {
	snapshot := &AppDynamicsConnectionSnapshot{ClientIDSecretName: "shared.key", ClientSecretSecretName: "SHARED-KEY", UsernameSecretName: "shared_key", PasswordSecretName: "sHaReD.Key", EventsAPIKeySecretName: "shared-key"}
	for _, test := range []struct {
		name string
		want int
	}{
		{"SHARED_KEY", 5}, {"shared.key", 5}, {"Shared-Key", 5},
		{"different", 0}, {"", 0}, {"shared/key", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := appDynamicsSecretReferenceCount(snapshot, test.name); got != test.want {
				t.Fatalf("canonical reference count = %d, want %d", got, test.want)
			}
		})
	}
	if got := appDynamicsSecretReferenceCount(nil, "SHARED_KEY"); got != 0 {
		t.Fatalf("nil profile reference count = %d", got)
	}
}
