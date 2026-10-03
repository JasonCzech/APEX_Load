package storage

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/dsl"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestObjectArchiveLifecycle(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkObjectArchiveLifecycle(t, s)
}
func checkObjectArchiveLifecycle(t *testing.T, s *SQLiteStore) {
	t.Helper()
	script := &dsl.Script{ID: "activity-script", Name: "Checkout", Protocol: "http", Version: "1", Actions: []dsl.Action{}}
	if err := s.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	scenario := &Scenario{ID: "activity-scenario", Name: "Checkout load", Duration: "1m"}
	groups := []ScenarioScript{{ScriptID: script.ID, VUsers: 1}}
	if err := s.SaveScenario(scenario, groups); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"script", "scenario"} {
		t.Run(kind, func(t *testing.T) {
			id := script.ID
			at := script.UpdatedAt
			if kind == "scenario" {
				id = scenario.ID
				at = scenario.UpdatedAt
			}
			if _, err := s.SetObjectArchived(kind, id, at.Add(-time.Second), true); err == nil {
				t.Fatal("stale archive succeeded")
			}
			changed, err := s.SetObjectArchived(kind, id, at, true)
			if err != nil || !changed {
				t.Fatalf("archive %v %v", changed, err)
			}
			changed, err = s.SetObjectArchived(kind, id, at, true)
			if err != nil || changed {
				t.Fatalf("idempotent archive %v %v", changed, err)
			}
			var updated time.Time
			if kind == "script" {
				current, err := s.GetScript(id)
				if err != nil || current.ArchivedAt == nil {
					t.Fatalf("archive read %v %#v", err, current)
				}
				updated = current.UpdatedAt
				current.Name = "Changed"
				if err := s.SaveScript(current); !errors.Is(err, ErrObjectArchived) {
					t.Fatalf("edit = %v", err)
				}
				if err := s.MoveScript(id, nil); !errors.Is(err, ErrObjectArchived) {
					t.Fatalf("move = %v", err)
				}
				list, err := s.ListScripts("", nil)
				if err != nil || list[0].ArchivedAt == nil {
					t.Fatalf("archive list %v", err)
				}
			} else {
				current, _, err := s.GetScenario(id)
				if err != nil || current.ArchivedAt == nil {
					t.Fatalf("scenario read %v %#v", err, current)
				}
				updated = current.UpdatedAt
				if err := s.SaveScenario(current, groups); !errors.Is(err, ErrObjectArchived) {
					t.Fatalf("scenario edit = %v", err)
				}
			}
			changed, err = s.SetObjectArchived(kind, id, updated, false)
			if err != nil || !changed {
				t.Fatalf("restore %v %v", changed, err)
			}
		})
	}
}
func TestObjectActivityFiltersAndSummaries(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkObjectActivityFiltersAndSummaries(t, s)
}

func checkObjectActivityFiltersAndSummaries(t *testing.T, s *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	for i, v := range []struct{ project, kind, id, name, action string }{{"default", "script", "a", "Name%literal", "create"}, {"default", "script", "a", "Name%literal", "modify"}, {"default", "script", "a", "Name%literal", "archive"}, {"default", "test", "b", "Other", "delete"}, {"other", "script", "a", "Name%literal", "delete"}} {
		if err := s.SaveObjectActivity(&ObjectActivityEvent{ID: v.action + v.project, ProjectID: v.project, ResourceType: v.kind, ResourceID: v.id, ResourceName: v.name, Action: v.action, ActorType: "user", ActorSubject: "user:alice", Status: "success", CreatedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListObjectActivity(ObjectActivityFilter{ProjectID: "default", ResourceTypes: []string{"script"}, Search: "%literal", Limit: 2})
	if err != nil || len(events) != 2 || events[0].Action != "archive" {
		t.Fatalf("filter %#v %v", events, err)
	}
	after := now.Add(time.Second).In(time.FixedZone("west", -4*60*60))
	before := now.Add(2 * time.Second).In(time.FixedZone("east", 3*60*60))
	ranged, rangeErr := s.ListObjectActivity(ObjectActivityFilter{ProjectID: "default", ResourceTypes: []string{"script"}, After: &after, Before: &before})
	if rangeErr != nil || len(ranged) != 2 || ranged[0].Action != "archive" || ranged[1].Action != "modify" {
		t.Fatalf("offset date range %#v %v", ranged, rangeErr)
	}
	events, err = s.ListObjectActivity(ObjectActivityFilter{ProjectID: "default", ResourceTypes: []string{}})
	if err != nil || len(events) != 0 {
		t.Fatal("empty authorized scope leaked events")
	}
	summaries, err := s.ObjectActivitySummaries("default", "script", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	v := summaries["a"]
	if v.Creator == nil || v.Creator.Action != "create" || v.LastModifier == nil || v.LastModifier.Action != "modify" || v.LastArchive == nil || v.LastArchive.Action != "archive" {
		t.Fatalf("summary %#v", v)
	}
	if _, err = s.DeleteAuditEventsBefore(now.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	events, err = s.ListObjectActivity(ObjectActivityFilter{ProjectID: "default", ResourceType: "script"})
	if err != nil || len(events) != 1 {
		t.Fatalf("retention %#v %v", events, err)
	}
}

func TestObjectActivityFailedDeleteAndIncarnation(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	for i, action := range []string{"create", "modify", "delete", "create", "delete"} {
		status := "success"
		if i == 4 {
			status = "failure"
		}
		if err := s.SaveObjectActivity(&ObjectActivityEvent{ID: fmtActivityID(i), ProjectID: "default", ResourceType: "script", ResourceID: "a", ResourceName: "Checkout", ActorSubject: fmtActivityID(i), Action: action, Status: status, CreatedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListObjectActivity(ObjectActivityFilter{ProjectID: "default"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Deleted {
			t.Fatal("failed delete marked object deleted")
		}
	}
	summaries, err := s.ObjectActivitySummaries("default", "script", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if summaries["a"].Creator.ActorSubject != "3" || summaries["a"].LastModifier != nil {
		t.Fatalf("old incarnation attributed %#v", summaries)
	}
}
func fmtActivityID(i int) string { return string(rune('0' + i)) }

func TestObjectActivityDelayedEventsPreserveDeletion(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	for i, action := range []string{"create", "delete", "archive", "modify"} {
		if err := s.SaveObjectActivity(&ObjectActivityEvent{ID: fmtActivityID(i), ResourceType: "test", ResourceID: "a", Action: action, Status: "success", CreatedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListObjectActivity(ObjectActivityFilter{})
	if err != nil || len(events) != 4 {
		t.Fatalf("history %#v %v", events, err)
	}
	for _, event := range events {
		if !event.Deleted || event.ProjectID != DefaultProjectID {
			t.Fatalf("delayed event changed deletion or project identity: %#v", event)
		}
	}
	if err := s.SaveObjectActivity(&ObjectActivityEvent{ID: "recreated", ResourceType: "test", ResourceID: "a", Action: "create", Status: "success", CreatedAt: now.Add(5 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	events, err = s.ListObjectActivity(ObjectActivityFilter{})
	if err != nil || events[0].Deleted {
		t.Fatalf("recreated object still deleted: %#v %v", events, err)
	}
}

func TestObjectActivityCommittedAsyncOutcomesHaveAttribution(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	for i, status := range []string{"success", "accepted", "started", "pending", "failure", "blocked"} {
		action := "modify"
		if i == 0 {
			action = "create"
		}
		if err := s.SaveObjectActivity(&ObjectActivityEvent{ID: status, ResourceType: "test", ResourceID: "a", Action: action, ActorSubject: status, Status: status, CreatedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			continue
		}
		summaries, err := s.ObjectActivitySummaries("", "test", []string{"a"})
		if err != nil || summaries["a"].LastModifier == nil {
			t.Fatalf("summary %#v %v", summaries, err)
		}
		want := status
		if i > 3 {
			want = "pending"
		}
		if summaries["a"].LastModifier.ActorSubject != want {
			t.Fatalf("%s attribution %#v", status, summaries)
		}
	}
}

func TestObjectActivityLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAuditEvent(&AuditEvent{ID: "legacy", ProjectID: "default", ResourceType: "script", ResourceID: "stable", Action: "script.create", ActorType: "user", ActorSubject: "alice", Status: "success", CreatedAt: time.Now().UTC(), Details: map[string]any{"name": "Checkout", "password": "never-save-content", "script": "private-content"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM object_activity_migrations`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.ListObjectActivity(ObjectActivityFilter{ProjectID: "default"})
	if err != nil || len(events) != 1 {
		t.Fatalf("migration %v %#v", err, events)
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "never-save-content") || strings.Contains(string(raw), "private-content") {
		t.Fatal("legacy raw details leaked")
	}
	if err := s.backfillObjectActivity(); err != nil {
		t.Fatal(err)
	}
	events, _ = s.ListObjectActivity(ObjectActivityFilter{ProjectID: "default"})
	if len(events) != 1 {
		t.Fatal("migration duplicated events")
	}
}

func TestArchivedSourcesFencePreparedAndCISnapshotStarts(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "admission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkArchivedSourceAdmission(t, s)
}

func checkArchivedSourceAdmission(t *testing.T, s *SQLiteStore) {
	t.Helper()
	script := &dsl.Script{ID: "launch-source", Name: "Source", Version: "1", Protocol: "http", Actions: []dsl.Action{}}
	if err := s.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"script_identity": "script_id:" + script.ID})
	ciKey := "ci:v1:" + base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	for _, tc := range []struct {
		id     string
		groups any
		key    string
	}{
		{"prepared-source", []map[string]string{{"script_id": script.ID}}, ""},
		{"ci-snapshot", []map[string]string{{"script_id": "private-ci-snapshot"}}, ciKey},
	} {
		record := &TestRecord{ID: tc.id, Name: tc.id, Status: "QUEUED", StartTime: now, Groups: tc.groups, MaterializationKey: tc.key}
		if created, err := s.CreateTestIfAbsent(record); err != nil || !created {
			t.Fatalf("create: %v %v", created, err)
		}
	}
	active := &TestRecord{ID: "active-before-archive", Name: "Active", Status: "RUNNING", StartTime: now, Groups: []map[string]string{{"script_id": script.ID}}}
	if err := s.SaveTest(active); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.SetObjectArchived("script", script.ID, script.UpdatedAt, true); err != nil || !changed {
		t.Fatalf("archive %v %v", changed, err)
	}
	for _, id := range []string{"prepared-source", "ci-snapshot"} {
		record, err := s.GetTest(id)
		if err != nil {
			t.Fatal(err)
		}
		record.Status = "INITIALIZING"
		if err := s.SaveTestStartIfNoDeploymentLease(record, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrObjectArchived) {
			t.Fatalf("%s start: %v", id, err)
		}
		current, err := s.GetTest(id)
		if err != nil || current.Status != "QUEUED" {
			t.Fatalf("failed start changed durable state: %#v %v", current, err)
		}
	}
	if created, err := s.CreateTestIfAbsent(&TestRecord{ID: "new-ci", Name: "CI", Status: "QUEUED", StartTime: now, MaterializationKey: ciKey}); created || !errors.Is(err, ErrObjectArchived) {
		t.Fatalf("new CI admission %v %v", created, err)
	}
	// Ordinary persistence of an already active execution remains available.
	if err := s.SaveTest(active); err != nil {
		t.Fatalf("active execution persistence interrupted: %v", err)
	}
	scenario := &Scenario{ID: "archived-launch-scenario", Name: "Archived scenario", Duration: "1m"}
	if err := s.SaveScenario(scenario, nil); err != nil {
		t.Fatal(err)
	}
	prepared := &TestRecord{ID: "scenario-prepared", Name: "Scenario", Status: "QUEUED", StartTime: now, ScenarioID: scenario.ID}
	if created, err := s.CreateTestIfAbsent(prepared); err != nil || !created {
		t.Fatalf("scenario run %v %v", created, err)
	}
	if changed, err := s.SetObjectArchived("scenario", scenario.ID, scenario.UpdatedAt, true); err != nil || !changed {
		t.Fatalf("scenario archive %v %v", changed, err)
	}
	prepared.Status = "INITIALIZING"
	if err := s.SaveTestStartIfNoDeploymentLease(prepared, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrObjectArchived) {
		t.Fatalf("scenario start: %v", err)
	}
	active.Status = "COMPLETE"
	if err := s.SaveTest(active); err != nil {
		t.Fatalf("active completion interrupted: %v", err)
	}
}

func TestObjectArchiveConcurrentTransitionsAndIncarnation(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkObjectArchiveConcurrentTransitions(t, s)
}
func checkObjectArchiveConcurrentTransitions(t *testing.T, s *SQLiteStore) {
	t.Helper()
	script := &dsl.Script{ID: "archive-race", Name: "Race", Protocol: "http", Version: "1", Actions: []dsl.Action{}}
	if err := s.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	changes := make(chan bool, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			changed, err := s.SetObjectArchived("script", script.ID, script.UpdatedAt, true)
			changes <- changed
			failures <- err
		}()
	}
	wg.Wait()
	close(changes)
	close(failures)
	total := 0
	for changed := range changes {
		if changed {
			total++
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != 1 {
		t.Fatalf("transitions=%d", total)
	}
	current, err := s.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetObjectArchived("script", script.ID, script.UpdatedAt, false); err == nil {
		t.Fatal("stale restore accepted")
	}
	if _, err := s.SetObjectArchived("script", script.ID, current.UpdatedAt, false); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.UpdateScriptIfUnchanged(script, script.UpdatedAt); err == nil && changed {
		t.Fatal("pre-archive edit accepted after restore")
	}
	current, err = s.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetObjectArchived("script", script.ID, current.UpdatedAt, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteScript(script.ID); err != nil {
		t.Fatal(err)
	}
	fresh := &dsl.Script{ID: script.ID, Name: "Recreated", Protocol: "http", Version: "1", Actions: []dsl.Action{}}
	if err := s.SaveScript(fresh); err != nil {
		t.Fatal(err)
	}
	recreated, err := s.GetScript(script.ID)
	if err != nil || recreated.ArchivedAt != nil {
		t.Fatalf("old incarnation archive leaked: %#v %v", recreated, err)
	}
}

func TestObjectActivityLegacyRunAnnotationsModifyParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, action := range []string{"test.create", "run.annotation.create", "run.annotation.delete"} {
		actor := "alice"
		if i > 0 {
			actor = "bob"
		}
		if err := s.SaveAuditEvent(&AuditEvent{ID: fmtActivityID(i), ProjectID: "default", ResourceType: "test", ResourceID: "run", Action: action, ActorType: "user", ActorSubject: actor, Status: "success", CreatedAt: now.Add(time.Duration(i) * time.Second), Details: map[string]any{"name": "Run", "text": "private-annotation-text"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM object_activity_migrations`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.ListObjectActivity(ObjectActivityFilter{ProjectID: "default", ResourceID: "run"})
	if err != nil || len(events) != 3 {
		t.Fatalf("legacy annotations: %#v %v", events, err)
	}
	for i, event := range events {
		if event.Deleted {
			t.Fatalf("annotation deletion marked run deleted: %#v", event)
		}
		if i < 2 && (event.Action != "modify" || len(event.ChangedFields) != 1 || event.ChangedFields[0] != "annotations") {
			t.Fatalf("annotation lifecycle: %#v", event)
		}
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "private-annotation-text") {
		t.Fatal("legacy annotation text leaked")
	}
	summaries, err := s.ObjectActivitySummaries("default", "test", []string{"run"})
	if err != nil || summaries["run"].Creator == nil || summaries["run"].Creator.ActorSubject != "alice" || summaries["run"].LastModifier == nil || summaries["run"].LastModifier.ActorSubject != "bob" {
		t.Fatalf("annotation attribution: %#v %v", summaries, err)
	}
}
