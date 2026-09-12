package storage

import (
	"fmt"
	"testing"
	"time"
)

func TestThresholdIncidentLifecycleIsolationAndFilters(t *testing.T) {
	s := openTimeslotStore(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	for _, p := range []string{"incident-a", "incident-b"} {
		if err := s.SaveProject(&Project{ID: p, Name: p, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveTest(&TestRecord{ID: p + "-test", ProjectID: p, Name: "Checkout", Status: "RUNNING", StartTime: at}); err != nil {
			t.Fatal(err)
		}
	}
	breach := []ThresholdIncident{{Key: "p99", Severity: "danger", Payload: map[string]any{"metric": "p99_ms", "value": 200, "threshold": 100}}}
	record := func(p string, delta int, value []ThresholdIncident) {
		t.Helper()
		if err := s.RecordThresholdEvaluation(p, p+"-test", "Checkout", at.Add(time.Duration(delta)*time.Second), true, value); err != nil {
			t.Fatal(err)
		}
	}
	list := func(p string, f ThresholdIncidentFilter) []ThresholdIncident {
		t.Helper()
		v, e := s.ListThresholdIncidents(p, 100, 0, f)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	record("incident-a", 0, breach)
	record("incident-b", 0, breach)
	first := list("incident-a", ThresholdIncidentFilter{})[0]
	if ok, err := s.AcknowledgeThresholdIncident(first.ID, "incident-b", "operator", true, at); err != nil || ok {
		t.Fatalf("cross-project ack: %v %v", ok, err)
	}
	if ok, err := s.AcknowledgeThresholdIncident(first.ID, "incident-a", "operator", true, at); err != nil || !ok {
		t.Fatalf("ack: %v %v", ok, err)
	}
	record("incident-a", 1, breach)
	got := list("incident-a", ThresholdIncidentFilter{Status: "acknowledged", Query: "CHECKOUT", Severity: "danger"})
	if len(got) != 1 || got[0].ID != first.ID || got[0].AcknowledgedBy != "operator" || !got[0].Active {
		t.Fatalf("episode changed: %+v", got)
	}
	active, unacked, danger, err := s.ThresholdIncidentCounts("incident-a")
	if err != nil || active != 1 || unacked != 0 || danger != 1 {
		t.Fatalf("counts %d %d %d %v", active, unacked, danger, err)
	}
	record("incident-a", 0, nil) // Old samples cannot resolve newer breaches.
	if len(list("incident-a", ThresholdIncidentFilter{Status: "active"})) != 1 {
		t.Fatal("old sample resolved incident")
	}
	record("incident-a", 2, nil)
	got = list("incident-a", ThresholdIncidentFilter{Status: "resolved"})
	if len(got) != 1 || got[0].ResolvedAt == nil {
		t.Fatalf("resolution: %+v", got)
	}
	record("incident-a", 3, breach)
	got = list("incident-a", ThresholdIncidentFilter{Status: "unacknowledged"})
	if len(got) != 1 || got[0].ID == first.ID || got[0].AcknowledgedAt != nil {
		t.Fatalf("recurrence: %+v", got)
	}
	future := at.Add(time.Hour)
	if len(list("incident-a", ThresholdIncidentFilter{Since: &future})) != 0 {
		t.Fatal("time filter ignored")
	}
	if err := s.RecordThresholdEvaluation("incident-b", "incident-a-test", "bad", at.Add(time.Hour), true, breach); err == nil {
		t.Fatal("cross-project recording accepted")
	}
	if err := s.SaveTest(&TestRecord{ID: "incident-a-test", ProjectID: "incident-a", Name: "Checkout", Status: "COMPLETE", StartTime: at, EndTime: &future}); err != nil {
		t.Fatal(err)
	}
	if len(list("incident-a", ThresholdIncidentFilter{Status: "active"})) != 0 {
		t.Fatal("completed test still active")
	}
	if len(list("incident-b", ThresholdIncidentFilter{Status: "active"})) != 1 {
		t.Fatal("other project changed")
	}
}

func TestFleetAuditFilterAppliedBeforeLimit(t *testing.T) {
	s := openTimeslotStore(t)
	at := time.Now().UTC()
	for i := 0; i < 30; i++ {
		action, resource := "auth.login", "user"
		if i < 3 {
			action, resource = "fleet.pool.update", "fleet_pool"
		}
		if err := s.SaveAuditEvent(&AuditEvent{ID: fmt.Sprint(i), Action: action, ResourceType: resource, Status: "success", CreatedAt: at.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListAuditEvents(AuditEventFilter{FleetOnly: true, Limit: 2})
	if err != nil || len(events) != 2 || events[0].ID != "2" || events[1].ID != "1" {
		t.Fatalf("fleet history truncated by general activity: %+v %v", events, err)
	}
}

func TestThresholdIncidentAcknowledgementSurvivesStoreReopen(t *testing.T) {
	path := t.TempDir() + "/incidents.db"
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err = store.SaveProject(&Project{ID: "persisted", Name: "Persisted", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveTest(&TestRecord{ID: "persistent-run", ProjectID: "persisted", Name: "Checkout", Status: "RUNNING", StartTime: at}); err != nil {
		t.Fatal(err)
	}
	if err = store.RecordThresholdEvaluation("persisted", "persistent-run", "Checkout", at, true, []ThresholdIncident{{Key: "p99", Payload: map[string]any{"metric": "p99_ms"}}}); err != nil {
		t.Fatal(err)
	}
	incidents, err := store.ListThresholdIncidents("persisted", 10, 0, ThresholdIncidentFilter{})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("list %v %v", incidents, err)
	}
	if _, err = store.AcknowledgeThresholdIncident(incidents[0].ID, "persisted", "operator-1", true, at); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetThresholdIncident(incidents[0].ID)
	if err != nil || got.AcknowledgedBy != "operator-1" || got.AcknowledgedAt == nil || !got.Active {
		t.Fatalf("persisted acknowledgement: %+v %v", got, err)
	}
}
