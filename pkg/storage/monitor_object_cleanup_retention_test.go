package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Retention only needs journal ownership/index metadata. These rows deliberately
// have no live configuration or receipt; observer/grant behavior is exercised
// by the real accepted poll used in the persistence test below.
func seedManagementRetentionJournal(t *testing.T, s *SQLiteStore, project, id, state string, created time.Time) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO monitor_management_polls(id,project_id,source_id,collector_id,instance_id,nonce,generation,poll_sequence,source_revision,scope_hash,identity_hash,state,created_at,deadline,payload)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, project, "retention-source-"+id, "retention-collector", "retention-instance", id, 1, 1, 1, strings.Repeat("a", 64), strings.Repeat("b", 64), state, created.UTC(), created.Add(time.Minute).UTC(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
}

func managementRetentionCursor(t *testing.T, s *SQLiteStore) managementPollRetentionCursor {
	t.Helper()
	raw, err := s.GetSetting(managementPollRetentionCursorKey)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := decodeManagementPollRetentionCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func managementRetentionJournalExists(t *testing.T, s *SQLiteStore, id string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM monitor_management_polls WHERE id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestManagementPollRetentionSweepProtectedProgressPersistsAndWraps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retention.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			s.Close()
		}
	})
	_, _, now := seedMonitorFixture(t, s)
	_, m, _ := managementSeed(t, s, now)
	poll, err := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "retained-receipt", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, managementCore, managementReceipts(poll, now), now); err != nil {
		t.Fatal(err)
	}
	before := now.Add(time.Hour)
	seedManagementRetentionJournal(t, s, m.ProjectID, "orphan-a", "abandoned", now.Add(time.Second))
	seedManagementRetentionJournal(t, s, m.ProjectID, "orphan-b", "accepted", now.Add(2*time.Second))
	seedManagementRetentionJournal(t, s, m.ProjectID, "still-collecting", "collecting", now.Add(3*time.Second))
	seedManagementRetentionJournal(t, s, m.ProjectID, "cutoff-equality", "abandoned", before)
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 1); err != nil || scanned != 1 || pruned != 0 {
		t.Fatalf("protected raw page: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
	if cursor := managementRetentionCursor(t, s); cursor.ID != poll.ID {
		t.Fatalf("protected journal did not advance durable cursor: %#v", cursor)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"orphan-a", "orphan-b"} {
		if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 1); err != nil || scanned != 1 || pruned != 1 {
			t.Fatalf("resumed orphan page %s: scanned=%d pruned=%d err=%v", id, scanned, pruned, err)
		}
		if managementRetentionJournalExists(t, s, id) {
			t.Fatalf("orphan %s survived resumed sweep", id)
		}
	}
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 1); err != nil || scanned != 0 || pruned != 0 {
		t.Fatalf("end-of-pass wrap: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
	if cursor := managementRetentionCursor(t, s); cursor.ID != "" || !cursor.CreatedAt.IsZero() {
		t.Fatalf("empty page failed to wrap cursor: %#v", cursor)
	}
	if err := s.DeleteTest("monitor-run"); err != nil {
		t.Fatal(err)
	}
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 1); err != nil || scanned != 1 || pruned != 1 {
		t.Fatalf("removed receipt reconsidered after wrap: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
	for _, id := range []string{"still-collecting", "cutoff-equality"} {
		if !managementRetentionJournalExists(t, s, id) {
			t.Fatalf("noneligible journal %s was pruned", id)
		}
	}
}

func testManagementPollRetentionProjectPrefix(t *testing.T, s *SQLiteStore) {
	t.Helper()
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for i := 0; i < 53; i++ {
		// Reverse project order makes the candidate order differ from lock order.
		project := fmt.Sprintf("retention-project-%02d", 52-i)
		requireProjectFixtures(t, s, project)
		seedManagementRetentionJournal(t, s, project, fmt.Sprintf("retention-poll-%02d", i), "abandoned", created)
	}
	before := created.Add(time.Hour)
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 512); err != nil || scanned != 53 || pruned != 50 {
		t.Fatalf("project-bound prefix: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
	if cursor := managementRetentionCursor(t, s); cursor.ID != "retention-poll-49" {
		t.Fatalf("unprocessed suffix skipped: %#v", cursor)
	}
	for i := 50; i < 53; i++ {
		if !managementRetentionJournalExists(t, s, fmt.Sprintf("retention-poll-%02d", i)) {
			t.Fatal("project-bound suffix was processed without a fence")
		}
	}
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 512); err != nil || scanned != 3 || pruned != 3 {
		t.Fatalf("project-bound suffix resumed: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
}

func TestManagementPollRetentionSweepProjectPrefix(t *testing.T) {
	s, _, _ := monitorFixture(t)
	testManagementPollRetentionProjectPrefix(t, s)
}

func TestManagementPollRetentionSweepBoundsCancellationAndMalformedCursor(t *testing.T) {
	s, m, now := monitorFixture(t)
	seedManagementRetentionJournal(t, s, m.ProjectID, "retention-boundary", "abandoned", now)
	before := now.Add(time.Hour)
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		before time.Time
		limit  int
	}{
		{"nil context", nil, before, 1},
		{"zero cutoff", context.Background(), time.Time{}, 1},
		{"zero limit", context.Background(), before, 0},
		{"excessive limit", context.Background(), before, 513},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.SweepManagementPollRetention(tc.ctx, tc.before, tc.limit); err == nil {
				t.Fatal("invalid sweep accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.SweepManagementPollRetention(ctx, before, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sweep ignored context: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key=?`, managementPollRetentionCursorKey).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected sweep persisted cursor: %d %v", n, err)
	}
	malformed := `{"version":2,"id":"retention-boundary","created_at":"2026-01-01T00:00:00Z"}`
	if _, err := s.db.Exec(`INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?)`, managementPollRetentionCursorKey, malformed, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SweepManagementPollRetention(context.Background(), before, 1); err == nil {
		t.Fatal("unknown cursor version accepted")
	}
	if raw, err := s.GetSetting(managementPollRetentionCursorKey); err != nil || raw != malformed {
		t.Fatalf("failed sweep overwrote cursor: %q %v", raw, err)
	}
	if !managementRetentionJournalExists(t, s, "retention-boundary") {
		t.Fatal("failed sweep deleted journal")
	}
}

func TestManagementPollRetentionSweepUsesTerminalKeysetIndex(t *testing.T) {
	s, m, now := monitorFixture(t)
	seedManagementRetentionJournal(t, s, m.ProjectID, "indexed-poll", "abandoned", now)
	for _, keyset := range []bool{false, true} {
		query := `EXPLAIN QUERY PLAN SELECT p.id,p.project_id,p.created_at FROM monitor_management_polls p WHERE p.state IN ('accepted','abandoned') AND p.created_at<?`
		args := []any{now.Add(time.Hour)}
		if keyset {
			query += ` AND (p.created_at,p.id)>(?,?)`
			args = append(args, now.Add(-time.Second), "previous")
		}
		query += ` ORDER BY p.created_at,p.id LIMIT ?`
		args = append(args, 512)
		rows, err := s.db.Query(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		indexed := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			indexed = indexed || strings.Contains(detail, "idx_monitor_polls_retention")
			if strings.Contains(detail, "USE TEMP B-TREE") {
				rows.Close()
				t.Fatalf("retention ordered through an unbounded sort: %s", detail)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if !indexed {
			t.Fatalf("terminal keyset page did not use retention index (keyset=%t)", keyset)
		}
	}
}

func TestDecodeManagementPollRetentionCursor(t *testing.T) {
	for _, raw := range []string{"", `null`, `{}`, `{"version":1,"id":"partial"}`, `{"version":1,"created_at":"2026-01-01T00:00:00Z"}`, `{"version":1,"unknown":1}`, `{"version":1} {"version":1}`, strings.Repeat(" ", 4097)} {
		if _, err := decodeManagementPollRetentionCursor(raw); err == nil {
			t.Fatalf("invalid cursor accepted: %q", raw)
		}
	}
	for _, raw := range []string{`{"version":1}`, `{"version":1,"id":"poll","created_at":"2026-01-01T00:00:00+01:00"}`} {
		cursor, err := decodeManagementPollRetentionCursor(raw)
		if err != nil || cursor.Version != 1 || cursor.CreatedAt.Location() != time.UTC {
			t.Fatalf("valid cursor rejected or not normalized: %#v %v", cursor, err)
		}
	}
}

func TestManagementPollRetentionSweepPreservesUnresolvedPhysicalOwnership(t *testing.T) {
	s, m, now := monitorFixture(t)
	seedManagementRetentionJournal(t, s, m.ProjectID, "physical-owned", "abandoned", now)
	seedManagementRetentionJournal(t, s, m.ProjectID, "physical-orphan", "abandoned", now.Add(time.Second))
	if _, err := s.db.Exec(`INSERT INTO monitor_collection_leases(project_id,source_id,collector_id,instance_id,generation,poll_sequence,source_revision,scope_hash,identity_hash,state,poll_id,expires_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, m.ProjectID, "retention-source-physical-owned", "retention-collector", "retention-instance", 1, 1, 1, strings.Repeat("a", 64), strings.Repeat("b", 64), "collecting", "physical-owned", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	before := now.Add(time.Hour)
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 512); err != nil || scanned != 2 || pruned != 1 {
		t.Fatalf("physical ownership protection: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
	if !managementRetentionJournalExists(t, s, "physical-owned") {
		t.Fatal("expired physical authority was treated as completed cleanup")
	}
	if _, err := s.db.Exec(`UPDATE monitor_collection_leases SET state='released' WHERE project_id=? AND source_id=?`, m.ProjectID, "retention-source-physical-owned"); err != nil {
		t.Fatal(err)
	}
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 512); err != nil || scanned != 0 || pruned != 0 {
		t.Fatalf("physical cursor wrap: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 512); err != nil || scanned != 1 || pruned != 1 {
		t.Fatalf("completed physical cleanup not reclaimed: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
}
