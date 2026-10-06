package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorcontract"
)

func seedRetiredMonitorJobs(t *testing.T, s *SQLiteStore, now time.Time) map[string]string {
	t.Helper()
	before := map[string]string{}
	for _, item := range []struct{ id, pack, state, collector string }{
		{"retired-direct", "appdynamics", "collecting", ""},
		{"retired-collector", "appdynamics", "reconciling", "monitor-collector"},
		{"retired-terminal", "appdynamics", "stopped", ""},
		{"elastic-active", "elastic", "collecting", ""},
		{"generic-active", "linux-hosts", "collecting", "monitor-collector"},
	} {
		job := RunMonitorJob{ID: item.id, ProjectID: "monitor-p", RunID: "monitor-run", State: item.state, Source: MonitorSource{ID: item.id, ProjectID: "monitor-p", Name: item.id, Pack: item.pack, CollectorID: item.collector}, ConnectionSnapshot: &AppDynamicsConnectionSnapshot{ConnectionID: "retained-connection", PasswordSecretName: "retained-secret"}, StartedAt: now, ExpiresAt: now.Add(time.Hour), NextSequence: 7}
		body, err := json.Marshal(job)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		fields["future_private"] = json.RawMessage(`{"identity":"preserve-me"}`)
		body, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		before[item.id] = string(body)
		if _, err = s.db.Exec(`INSERT INTO run_monitor_jobs(id,project_id,test_id,collector_id,state,next_sequence,expires_at,payload) VALUES(?,?,?,?,?,7,?,?)`, item.id, "monitor-p", "monitor-run", item.collector, item.state, now.Add(time.Hour), string(body)); err != nil {
			t.Fatal(err)
		}
	}
	return before
}

func checkMonitorRetirement(t *testing.T, s *SQLiteStore, now time.Time) {
	t.Helper()
	before := seedRetiredMonitorJobs(t, s, now)
	for _, state := range []string{"pending", "running", "complete", "error"} {
		if _, err := s.db.Exec(`INSERT INTO appdynamics_inspections(id,project_id,connection_id,connection_revision,collector_id,kind,state,payload_json,result_json,error,owner_token,lease_expires_at,created_at,expires_at) VALUES(?,?,?,1,'','test',?,'{}',?,'previous-message','previous-owner',?,?,?)`, "inspection-"+state, "monitor-p", "retained-connection", state, `{"historical":"result"}`, now.Add(time.Minute), now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO run_monitor_batches(job_id,sequence,digest,artifact_key,sample_count,received_at) VALUES('retired-direct',0,?,'retained-artifact',0,?)`, strings.Repeat("a", 64), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO appdynamics_inspections(id,project_id,connection_id,connection_revision,collector_id,provider,kind,state,payload_json,created_at,expires_at) VALUES('prom-active','monitor-p','prom',1,'','prometheus','test','pending','{}',?,?)`, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if ok, err := s.RetireAppDynamicsMonitoring("", "", 0); err != nil || !ok {
			t.Fatalf("retirement: %v %v", ok, err)
		}
	}
	var promState string
	if err := s.db.QueryRow(`SELECT state FROM appdynamics_inspections WHERE id='prom-active'`).Scan(&promState); err != nil || promState != "pending" {
		t.Fatalf("supported inspection retired: %s %v", promState, err)
	}
	for id, oldBody := range before {
		job, err := s.GetRunMonitorJob("monitor-p", id)
		if err != nil {
			t.Fatal(err)
		}
		var body string
		if err = s.db.QueryRow(`SELECT payload FROM run_monitor_jobs WHERE id=?`, id).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if id == "retired-direct" || id == "retired-collector" {
			if job.State != "incomplete" || job.CoverageReason != "provider_retired" || job.NextSequence != 7 || job.ConnectionSnapshot == nil || job.ConnectionSnapshot.PasswordSecretName != "retained-secret" || !strings.Contains(body, "preserve-me") {
				t.Fatalf("lost retired state/private data: %+v %s", job, body)
			}
		} else if body != oldBody {
			t.Fatalf("unrelated/terminal payload changed: %s", id)
		}
	}
	for _, original := range []string{"pending", "running", "complete", "error"} {
		var state, result, message, owner string
		if err := s.db.QueryRow(`SELECT state,result_json,error,owner_token FROM appdynamics_inspections WHERE id=?`, "inspection-"+original).Scan(&state, &result, &message, &owner); err != nil {
			t.Fatal(err)
		}
		if result != `{"historical":"result"}` {
			t.Fatal("inspection history lost")
		}
		if original == "pending" || original == "running" {
			if state != "error" || message != "provider_retired" || owner != "" {
				t.Fatalf("unfinished inspection not retired: %s %s %s", state, message, owner)
			}
		} else if state != original || message != "previous-message" || owner != "previous-owner" {
			t.Fatal("terminal inspection changed")
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_monitor_batches WHERE artifact_key='retained-artifact'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("receipt lost: %d %v", n, err)
	}
}

func TestAppDynamicsRetirementPreservesHistoryAndIsIdempotent(t *testing.T) {
	s, _, now := monitorFixture(t)
	checkMonitorRetirement(t, s, now)
}

func TestAppDynamicsRetirementRejectsStaleExecutionTerm(t *testing.T) {
	s, _, now := monitorFixture(t)
	seedRetiredMonitorJobs(t, s, now)
	ok, old, err := s.TryAcquireSchedulerLeaseTerm("monitor-retirement", "old", now, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err = s.ReleaseSchedulerLease("monitor-retirement", "old"); err != nil {
		t.Fatal(err)
	}
	ok, current, err := s.TryAcquireSchedulerLeaseTerm("monitor-retirement", "new", now, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err = s.RetireAppDynamicsMonitoring("monitor-retirement", "old", old.Generation); err != nil || ok {
		t.Fatalf("stale leader mutated: %v %v", ok, err)
	}
	job, err := s.GetRunMonitorJob("monitor-p", "retired-direct")
	if err != nil || job.State != "collecting" {
		t.Fatal(job, err)
	}
	if ok, err = s.RetireAppDynamicsMonitoring("monitor-retirement", "new", current.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, fence := range []struct {
		name, owner string
		generation  uint64
	}{{"name", "", 1}, {"", "owner", 1}, {"name", "owner", 0}} {
		if _, err = s.RetireAppDynamicsMonitoring(fence.name, fence.owner, fence.generation); err == nil {
			t.Fatal("partial fence accepted")
		}
	}
}

func TestRetiredMonitorSourcesAndOptionalBindingsCannotStart(t *testing.T) {
	s, _, now := monitorFixture(t)
	source := MonitorSource{ID: "retired", ProjectID: "monitor-p", Name: "Retired", Pack: "appdynamics", Enabled: false, AppDynamics: &monitorcontract.AppDynamicsSelection{Applications: []string{"1"}}}
	if _, err := s.SaveMonitorSource(source, 0); !errors.Is(err, ErrMonitorProviderRetired) {
		t.Fatal(err)
	}
	body, _ := json.Marshal(source)
	if _, err := s.db.Exec(`INSERT INTO monitor_sources(id,project_id,revision,payload) VALUES(?,'monitor-p',1,?)`, source.ID, string(body)); err != nil {
		t.Fatal(err)
	}
	bindings := MonitorBindings{ProjectID: "monitor-p", ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: source.ID}}}
	if _, err := s.SaveMonitorBindings(bindings, 0); !errors.Is(err, ErrMonitorProviderRetired) {
		t.Fatal(err)
	}
	run, err := s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	run.MonitorBindings = &bindings
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRunMonitors("monitor-p", run.ID, run.ScenarioID, now, now.Add(time.Hour)); !errors.Is(err, ErrMonitorProviderRetired) {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = preparationMonitorSnapshotTx(tx, "monitor-p", run.ID); !errors.Is(err, ErrMonitorProviderRetired) {
		t.Fatal(err)
	}
}
