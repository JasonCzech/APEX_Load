package storage

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStartAdmissionRevisionStores(t *testing.T, name string) (*SQLiteStore, *SQLiteStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSQLiteStore(path)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	return first, second
}

func TestPostgresStartAdmissionTriggerContractAndLockOrder(t *testing.T) {
	function := postgresStartAdmissionRevisionFunctionSQL()
	for _, required := range []string{
		"pg_current_xact_id()::text",
		"current_setting('apexload.start_admission_revision_xid', true) IS DISTINCT FROM current_xid",
		"set_config('apexload.start_admission_revision_xid', current_xid, true)",
		"ELSIF TG_OP = 'INSERT'",
		"ELSIF OLD.key = 'system_settings' OR NEW.key = 'system_settings'",
	} {
		if !strings.Contains(function, required) {
			t.Errorf("PostgreSQL admission function is missing %q: %s", required, function)
		}
	}
	for _, table := range startAdmissionRevisionTables() {
		trigger := "apexload_start_admission_" + table
		query := postgresStartAdmissionTriggerSQL(trigger, table)
		for _, required := range []string{
			"CREATE CONSTRAINT TRIGGER " + trigger,
			" AFTER INSERT OR UPDATE OR DELETE ON " + table,
			" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW ",
			"apexload_bump_start_admission_revision()",
		} {
			if !strings.Contains(query, required) {
				t.Errorf("PostgreSQL admission trigger for %s is missing %q: %s", table, required, query)
			}
		}
	}
	probe := postgresStartAdmissionTriggerProbeSQL()
	rewritten := rewriteSQL(probe, storeDialectPostgres)
	for _, required := range []string{"$1::regclass", "tgname = $2", "tgtype = $3", "tgconstraint <> 0", "tgdeferrable AND tginitdeferred"} {
		if !strings.Contains(rewritten, required) {
			t.Errorf("rewritten PostgreSQL admission probe is missing %q: %s", required, rewritten)
		}
	}
	if strings.Contains(rewritten, "?") {
		t.Fatalf("rewritten PostgreSQL admission probe retained SQLite placeholders: %s", rewritten)
	}
	locks := postgresStartAdmissionRevisionLockOrderSQL()
	if len(locks) != 2 || !strings.Contains(locks[0], "scheduler_content_revision") || !strings.Contains(locks[1], "start_admission_revision") {
		t.Fatalf("PostgreSQL singleton lock order = %#v, want scheduler-content then start-admission", locks)
	}
}

func TestPostgresStartCommitLocksPlacementBeforeAdmissionSingletons(t *testing.T) {
	source, err := os.ReadFile("deployment_lease.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	start := strings.Index(body, "func (s *SQLiteStore) saveTestStartIfNoDeploymentLease")
	if start < 0 {
		t.Fatal("could not locate start-commit implementation")
	}
	end := strings.Index(body[start:], "\nfunc validateDeploymentLeaseIdentity")
	if end < 0 {
		t.Fatal("could not locate start-commit implementation")
	}
	body = body[start : start+end]
	reservation := strings.Index(body, "validateCapacityReservationForTestStart")
	placement := strings.Index(body, "validateExistingTestStartPlacement")
	revision := strings.Index(body, "validateExpectedPostgresStartAdmissionRevision")
	write := strings.Index(body, "writePreparedTestWithExecutor")
	if reservation < 0 || placement < 0 || revision < 0 || write < 0 ||
		!(reservation < placement && placement < revision && revision < write) {
		t.Fatalf("PostgreSQL start lock order must be reservation/test placement -> singleton revisions -> upsert; offsets reservation=%d placement=%d revision=%d write=%d", reservation, placement, revision, write)
	}
}

func TestStartCommitCannotRecreateDeletedOrReplacedTestIncarnation(t *testing.T) {
	store, _ := newStartAdmissionRevisionStores(t, "start-incarnation-fence")
	now := time.Now().UTC().Truncate(time.Microsecond)
	original := queuedStartAdmissionRecord("start-incarnation", now)
	original.RefKey = "RUN-ORIGINAL"
	if err := store.SaveTest(original); err != nil {
		t.Fatalf("SaveTest(original): %v", err)
	}
	staleStart, err := store.GetTest(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleStart.Status = "INITIALIZING"
	if err := store.DeleteTest(original.ID); err != nil {
		t.Fatalf("DeleteTest: %v", err)
	}
	if err := store.SaveTestStartIfNoDeploymentLease(staleStart, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrTestStartIdentityChanged) {
		t.Fatalf("start after delete error = %v, want ErrTestStartIdentityChanged", err)
	}
	if _, err := store.GetTest(original.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale start recreated deleted row: %v", err)
	}

	otherProject := &Project{ID: "start-incarnation-other", Name: "Other", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(otherProject); err != nil {
		t.Fatalf("SaveProject(other): %v", err)
	}
	replacement := queuedStartAdmissionRecord(original.ID, now.Add(time.Second))
	replacement.RefKey = "RUN-REPLACEMENT"
	replacement.ProjectID = otherProject.ID
	if err := store.SaveTest(replacement); err != nil {
		t.Fatalf("SaveTest(replacement): %v", err)
	}
	if err := store.SaveTestStartIfNoDeploymentLease(staleStart, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrTestStartIdentityChanged) {
		t.Fatalf("start against replacement error = %v, want ErrTestStartIdentityChanged", err)
	}
	durable, err := store.GetTest(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.RefKey != replacement.RefKey || durable.Status != "QUEUED" || !durable.StartTime.Equal(replacement.StartTime) {
		t.Fatalf("stale start overwrote replacement: %#v", durable)
	}
}

func queuedStartAdmissionRecord(id string, now time.Time) *TestRecord {
	return &TestRecord{
		ID: id, Name: id, Status: "QUEUED", ProjectID: DefaultProjectID,
		TotalVUsers: 1, Duration: int64(time.Minute), StartTime: now, Groups: []any{},
	}
}

func assertStaleStartAdmissionRevision(t *testing.T, starter *SQLiteStore, record *TestRecord, expected int64) {
	t.Helper()
	start, err := starter.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	start.Status = "INITIALIZING"
	start.ExpectedStartAdmissionRevision = expected
	if err := starter.SaveTestStartIfNoDeploymentLease(start, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrTestStartAdmissionRevisionChanged) {
		t.Fatalf("stale start error = %v, want ErrTestStartAdmissionRevisionChanged", err)
	}
	durable, err := starter.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest after stale start: %v", err)
	}
	if durable.Status != "QUEUED" {
		t.Fatalf("stale start persisted status %q, want QUEUED", durable.Status)
	}
}

func TestStartAdmissionRevisionFencesCrossConnectionReservationMutation(t *testing.T) {
	starter, writer := newStartAdmissionRevisionStores(t, "reservation-admission-revision")
	now := time.Now().UTC()
	record := queuedStartAdmissionRecord("reservation-admission-test", now)
	if err := starter.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	expected, err := starter.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	capacity := 10
	if err := writer.SaveCapacityReservation(&CapacityReservation{
		ID: "unbound-concurrent-slot", ProjectID: DefaultProjectID,
		StartAt: now.Add(-time.Minute), EndAt: now.Add(time.Hour), ApprovalState: "approved",
		RequestedCapacity: &CapacityReservationRequest{VUsers: &capacity},
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	assertStaleStartAdmissionRevision(t, starter, record, expected)

	fresh, err := starter.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	start, err := starter.GetTest(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	start.Status = "INITIALIZING"
	start.ExpectedStartAdmissionRevision = fresh
	if err := starter.SaveTestStartIfNoDeploymentLease(start, DefaultDeploymentLeaseName, "", ""); err != nil {
		t.Fatalf("fresh admission start: %v", err)
	}
}

func TestStartAdmissionRevisionFencesCrossConnectionOccurrenceMutation(t *testing.T) {
	starter, writer := newStartAdmissionRevisionStores(t, "occurrence-admission-revision")
	now := time.Now().UTC()
	if err := writer.SaveScenario(&Scenario{
		ID: "admission-scenario", Name: "Admission scenario",
		Duration: "1m", CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := writer.SaveScenarioSchedule(&ScenarioSchedule{
		ID: "admission-schedule", ScenarioID: "admission-scenario", ProjectID: DefaultProjectID,
		Name: "Admission schedule", CronExpr: "0 * * * *", Timezone: "UTC", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	record := queuedStartAdmissionRecord("occurrence-admission-test", now)
	if err := starter.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	expected, err := starter.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.SaveScenarioScheduleOccurrence(&ScenarioScheduleOccurrence{
		ID: "concurrent-occurrence", ScheduleID: "admission-schedule", ScenarioID: "admission-scenario",
		ProjectID: DefaultProjectID, ScheduleRevision: 1, PlannedAt: now.Add(10 * time.Minute),
		Status: "planned", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
	}
	assertStaleStartAdmissionRevision(t, starter, record, expected)
}

func TestStartAdmissionRevisionTracksOnlyAdmissionSettingsKey(t *testing.T) {
	store, _ := newStartAdmissionRevisionStores(t, "settings-admission-revision")
	before, err := store.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSetting("unrelated_sequence", "1"); err != nil {
		t.Fatal(err)
	}
	afterUnrelated, err := store.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	if afterUnrelated != before {
		t.Fatalf("unrelated setting advanced admission revision from %d to %d", before, afterUnrelated)
	}
	if err := store.SaveSetting("system_settings", `{}`); err != nil {
		t.Fatal(err)
	}
	afterSystem, err := store.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	if afterSystem <= afterUnrelated {
		t.Fatalf("system settings did not advance admission revision: before=%d after=%d", afterUnrelated, afterSystem)
	}
	if _, err := store.db.Exec(`UPDATE app_settings SET value=? WHERE key=?`, "2", "unrelated_sequence"); err != nil {
		t.Fatal(err)
	}
	afterUnrelatedUpdate, err := store.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	if afterUnrelatedUpdate != afterSystem {
		t.Fatalf("unrelated UPDATE advanced admission revision from %d to %d", afterSystem, afterUnrelatedUpdate)
	}
	if _, err := store.db.Exec(`DELETE FROM app_settings WHERE key=?`, "unrelated_sequence"); err != nil {
		t.Fatal(err)
	}
	afterUnrelatedDelete, err := store.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	if afterUnrelatedDelete != afterUnrelatedUpdate {
		t.Fatalf("unrelated DELETE advanced admission revision from %d to %d", afterUnrelatedUpdate, afterUnrelatedDelete)
	}
	if _, err := store.db.Exec(`UPDATE app_settings SET key=? WHERE key=?`, "renamed_system_settings", "system_settings"); err != nil {
		t.Fatal(err)
	}
	afterSystemRenameAway, err := store.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	if afterSystemRenameAway <= afterUnrelatedDelete {
		t.Fatalf("system_settings rename-away did not advance revision: before=%d after=%d", afterUnrelatedDelete, afterSystemRenameAway)
	}
	if _, err := store.db.Exec(`UPDATE app_settings SET key=? WHERE key=?`, "system_settings", "renamed_system_settings"); err != nil {
		t.Fatal(err)
	}
	afterSystemRenameBack, err := store.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	if afterSystemRenameBack <= afterSystemRenameAway {
		t.Fatalf("system_settings rename-back did not advance revision: before=%d after=%d", afterSystemRenameAway, afterSystemRenameBack)
	}
}
