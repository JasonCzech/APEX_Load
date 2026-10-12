package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRendezvousDurableOutcomeFencing(t *testing.T) {
	checkRendezvousDurableOutcomeFencing(t, newHAFencingStore(t))
}
func checkRendezvousDurableOutcomeFencing(t *testing.T, s *SQLiteStore) {
	ctx := context.Background()
	base, term := liveTableTestFixture(t, s)
	r := RendezvousRecord{RunID: base.TestID, ProjectID: base.ProjectID, Group: "checkout", Snapshot: []byte(`{"revision":0}`)}
	if err := s.InitializeRendezvous(ctx, r, term); err != nil {
		t.Fatal(err)
	}
	r.Revision = 1
	r.Snapshot = []byte(`{"revision":1,"outcome":"released"}`)
	if err := s.SaveRendezvous(ctx, r, 0, term); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRendezvous(ctx, r, 0, term); err != nil {
		t.Fatal("lost reply", err)
	}
	changed := r
	changed.Snapshot = []byte(`{"revision":1,"outcome":"waiting"}`)
	if err := s.SaveRendezvous(ctx, changed, 0, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("changed outcome %v", err)
	}
	if _, err := s.db.Exec(`UPDATE scheduler_leases SET expires_at=? WHERE name=?`, time.Now().Add(-time.Minute), term.LeaseName); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRendezvous(ctx, r, 0, term); !errors.Is(err, ErrLiveTableFenced) {
		t.Fatalf("expired owner %v", err)
	}
	held, lease, err := s.TryAcquireSchedulerLeaseTerm(term.LeaseName, "successor", time.Now(), time.Minute)
	if err != nil || !held {
		t.Fatal(err)
	}
	next := LiveTableExecutionTerm{LeaseName: lease.Name, OwnerID: lease.OwnerID, Generation: lease.Generation}
	restored, err := s.ClaimRendezvous(ctx, r.RunID, r.Group, r.ProjectID, next)
	if err != nil || restored.Revision != 1 || string(restored.Snapshot) != string(r.Snapshot) {
		t.Fatalf("restore %+v %v", restored, err)
	}
	r.Revision = 2
	r.Snapshot = []byte(`{"revision":2}`)
	if err = s.SaveRendezvous(ctx, r, 1, term); !errors.Is(err, ErrLiveTableFenced) {
		t.Fatalf("old writer %v", err)
	}
	if err = s.SaveRendezvous(ctx, r, 1, next); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimRendezvous(ctx, r.RunID, r.Group, "foreign", next); err == nil {
		t.Fatal("cross project read")
	}
	if err = s.DeleteTest(r.RunID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM run_rendezvous WHERE run_id=?`, r.RunID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("run cleanup %d %v", count, err)
	}
}
