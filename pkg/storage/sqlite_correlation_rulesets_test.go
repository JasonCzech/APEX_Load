package storage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCorrelationRulesetRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "proj-a")

	ruleset := &CorrelationRuleset{
		ID:        "correlation-ruleset-test",
		ProjectID: "proj-a",
		Name:      "Checkout correlations",
		Source:    "script:s1",
		Rules: []CorrelationRule{
			{Name: "csrf_token", RuleID: "form_hidden", Source: "response_body", ExtractType: "boundary", Expression: `name="csrf" value="(.+?)"`, SampleValue: "LIVE-CSRF-SECRET", Confidence: 0.9},
		},
	}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Checkout correlations" || len(got.Rules) != 1 || got.Rules[0].RuleID != "form_hidden" {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
	if got.Rules[0].SampleValue != "" {
		t.Fatalf("storage read returned learned sample %q", got.Rules[0].SampleValue)
	}
	var rawRules string
	if err := store.db.QueryRow(`SELECT rules_json FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(&rawRules); err != nil {
		t.Fatalf("read raw rules: %v", err)
	}
	if strings.Contains(rawRules, "LIVE-CSRF-SECRET") || strings.Contains(rawRules, "sample_value") {
		t.Fatalf("storage write persisted learned sample: %s", rawRules)
	}
	var projectionVersion int
	var projectedAt, updatedAt time.Time
	if err := store.db.QueryRow(`SELECT rules_projection_version, rules_projected_at, updated_at
		FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(&projectionVersion, &projectedAt, &updatedAt); err != nil {
		t.Fatalf("read rules projection witness: %v", err)
	}
	if projectionVersion != correlationRulesetProjectionVersion || !projectedAt.Equal(updatedAt) {
		t.Fatalf("rules projection witness = version:%d projected:%s updated:%s",
			projectionVersion, projectedAt, updatedAt)
	}
	if got.ProjectID != "proj-a" {
		t.Fatalf("project = %q", got.ProjectID)
	}

	list, err := store.ListCorrelationRulesets("proj-a", 0, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d (%v)", len(list), err)
	}
	// Project scoping: a different project sees none.
	if other, _ := store.ListCorrelationRulesets("proj-b", 0, 0); len(other) != 0 {
		t.Fatalf("project scoping leaked: %d", len(other))
	}

	if err := store.DeleteCorrelationRuleset(ruleset.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if remaining, _ := store.ListCorrelationRulesets("proj-a", 0, 0); len(remaining) != 0 {
		t.Fatalf("delete left %d", len(remaining))
	}
}

func TestCorrelationRulesetLegacySampleMigrationPreservesVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	requireProjectFixtures(t, store, "project-a")
	ruleset := &CorrelationRuleset{ID: "legacy-sample", ProjectID: "project-a", Name: "legacy"}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatalf("SaveCorrelationRuleset: %v", err)
	}
	legacyUpdatedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	const secret = "LEGACY-LIVE-SESSION-SECRET"
	// The escaped property name defeats substring-based migrations but is the
	// same semantic legacy sample_value field after JSON decoding.
	legacyRules := `[{"name":"session","source":"response_body","extract_type":"jsonpath","expression":"$.session","sample\u005fvalue":"` + secret + `","confidence":0.9}]`
	if _, err := store.db.Exec(`UPDATE correlation_rulesets SET rules_json = ?, updated_at = ? WHERE id = ?`, legacyRules, legacyUpdatedAt, ruleset.ID); err != nil {
		t.Fatalf("seed legacy rules: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer store.Close()
	var rawRules string
	var migratedUpdatedAt, projectedAt time.Time
	var projectionVersion int
	if err := store.db.QueryRow(`SELECT rules_json, updated_at, rules_projection_version, rules_projected_at
		FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(&rawRules, &migratedUpdatedAt, &projectionVersion, &projectedAt); err != nil {
		t.Fatalf("read migrated rules: %v", err)
	}
	if strings.Contains(rawRules, secret) || strings.Contains(rawRules, "sample_value") {
		t.Fatalf("legacy sample remained at rest: %s", rawRules)
	}
	if !migratedUpdatedAt.Equal(legacyUpdatedAt) {
		t.Fatalf("migration changed optimistic version from %s to %s", legacyUpdatedAt, migratedUpdatedAt)
	}
	if projectionVersion != correlationRulesetProjectionVersion || !projectedAt.Equal(migratedUpdatedAt) {
		t.Fatalf("migration witness = version:%d projected:%s updated:%s",
			projectionVersion, projectedAt, migratedUpdatedAt)
	}
	got, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset: %v", err)
	}
	if len(got.Rules) != 1 || got.Rules[0].SampleValue != "" || got.Rules[0].Expression != "$.session" {
		t.Fatalf("migrated rules = %#v", got.Rules)
	}
}

func TestCorrelationRulesetLegacyRepairConvergesForLateOldWriter(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a")
	for _, id := range []string{"a-late", "z-seed"} {
		if err := store.SaveCorrelationRuleset(&CorrelationRuleset{
			ID: id, ProjectID: "project-a", Name: id,
		}); err != nil {
			t.Fatalf("SaveCorrelationRuleset(%s): %v", id, err)
		}
	}
	if _, err := store.db.Exec(`UPDATE correlation_rulesets
		SET rules_json = ?, updated_at = ?
		WHERE id = 'z-seed'`,
		`[{"name":"seed","source":"response_body","extract_type":"jsonpath","expression":"$.seed","sample_value":"SEED-SECRET"}]`,
		time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER write_late_legacy_ruleset
		AFTER UPDATE OF rules_json ON correlation_rulesets
		WHEN NEW.id = 'z-seed'
		BEGIN
			UPDATE correlation_rulesets
			SET rules_json = '[{"name":"late","source":"response_body","extract_type":"jsonpath","expression":"$.late","sample_value":"LATE-SECRET"}]',
				updated_at = '2035-01-02 03:04:05'
			WHERE id = 'a-late';
		END`); err != nil {
		t.Fatal(err)
	}

	if err := store.repairLegacyCorrelationRulesets(); err != nil {
		t.Fatalf("repairLegacyCorrelationRulesets: %v", err)
	}
	for _, id := range []string{"a-late", "z-seed"} {
		var payload string
		var version int
		var projectedAt, updatedAt time.Time
		if err := store.db.QueryRow(`SELECT rules_json, rules_projection_version, rules_projected_at, updated_at
			FROM correlation_rulesets WHERE id = ?`, id).Scan(&payload, &version, &projectedAt, &updatedAt); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload, "SECRET") || strings.Contains(payload, "sample_value") {
			t.Fatalf("legacy sample survived for %s: %s", id, payload)
		}
		if version != correlationRulesetProjectionVersion || !projectedAt.Equal(updatedAt) {
			t.Fatalf("projection witness(%s) = version:%d projected:%s updated:%s",
				id, version, projectedAt, updatedAt)
		}
	}
	if stale, err := store.hasStaleCorrelationRulesetProjection(); err != nil || stale {
		t.Fatalf("stale projection after convergent repair = (%t, %v)", stale, err)
	}
}

func TestCorrelationRulesetLegacyRepairUsesBoundedDeterministicPages(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a")

	rowCount := legacyCorrelationRulesetRepairBatchRows*2 + 7
	now := time.Now().UTC().Truncate(time.Microsecond)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < rowCount; index++ {
		id := fmt.Sprintf("legacy-page-%04d", index)
		payload := fmt.Sprintf(
			`[{"name":"rule-%d","source":"response_body","extract_type":"jsonpath","expression":"$.token","sample_value":"SECRET-%d"}]`,
			index,
			index,
		)
		if _, err := tx.Exec(`INSERT INTO correlation_rulesets
			(id, project_id, name, rules_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id,
			"project-a",
			id,
			payload,
			now,
			now,
		); err != nil {
			tx.Rollback()
			t.Fatalf("seed legacy ruleset %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	firstPage, nextCursor, done, err := store.loadLegacyCorrelationRulesetRepairBatch("")
	if err != nil {
		t.Fatal(err)
	}
	if done ||
		len(firstPage) != legacyCorrelationRulesetRepairBatchRows ||
		nextCursor != fmt.Sprintf("legacy-page-%04d", legacyCorrelationRulesetRepairBatchRows-1) {
		t.Fatalf("first repair page = rows:%d cursor:%q done:%t", len(firstPage), nextCursor, done)
	}
	if err := store.repairLegacyCorrelationRulesets(); err != nil {
		t.Fatal(err)
	}
	var repaired int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM correlation_rulesets
		WHERE rules_projection_version = ?
			AND rules_projected_at = updated_at
			AND INSTR(rules_json, 'sample_value') = 0
			AND INSTR(rules_json, 'SECRET-') = 0`,
		correlationRulesetProjectionVersion,
	).Scan(&repaired); err != nil {
		t.Fatal(err)
	}
	if repaired != rowCount {
		t.Fatalf("repaired rows = %d, want %d across bounded pages", repaired, rowCount)
	}
}

func TestCorrelationRulesetLegacyRepairUsesRawTimestampWitness(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a")
	ruleset := &CorrelationRuleset{
		ID: "legacy-raw-timestamp", ProjectID: "project-a", Name: "legacy raw timestamp",
	}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatal(err)
	}
	const (
		legacyTimestamp = "2035-01-02 03:04:05"
		secret          = "LEGACY-RAW-TIMESTAMP-SECRET"
	)
	if _, err := store.db.Exec(`UPDATE correlation_rulesets
		SET rules_json = ?,
			rules_projection_version = 0,
			rules_projected_at = ?,
			updated_at = ?
		WHERE id = ?`,
		`[{"name":"legacy","source":"response_body","extract_type":"jsonpath","expression":"$.token","sample_value":"`+secret+`"}]`,
		legacyTimestamp,
		legacyTimestamp,
		ruleset.ID,
	); err != nil {
		t.Fatal(err)
	}

	if err := store.repairLegacyCorrelationRulesets(); err != nil {
		t.Fatalf("repair legacy SQLite timestamp representation: %v", err)
	}
	var payload string
	var version int
	var projectedAtRaw, updatedAtRaw string
	if err := store.db.QueryRow(`SELECT rules_json, rules_projection_version,
			CAST(rules_projected_at AS TEXT), CAST(updated_at AS TEXT)
		FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(
		&payload,
		&version,
		&projectedAtRaw,
		&updatedAtRaw,
	); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, secret) || strings.Contains(payload, "sample_value") {
		t.Fatalf("legacy sample survived raw-timestamp repair: %s", payload)
	}
	if version != correlationRulesetProjectionVersion || projectedAtRaw != updatedAtRaw {
		t.Fatalf("raw-timestamp witness = version:%d projected:%q updated:%q",
			version, projectedAtRaw, updatedAtRaw)
	}
}

func TestCorrelationRulesetFutureProjectionFailsClosedWithoutDownMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	requireProjectFixtures(t, store, "project-a")
	ruleset := &CorrelationRuleset{
		ID:        "future-projection",
		ProjectID: "project-a",
		Name:      "future projection",
		Rules: []CorrelationRule{{
			Name:        "token",
			Source:      "response_body",
			ExtractType: "jsonpath",
			Expression:  "$.token",
		}},
	}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatal(err)
	}
	const futurePayload = `[{"name":"future","source":"response_body","extract_type":"future","expression":"$.future"}]`
	futureVersion := correlationRulesetProjectionVersion + 1
	if _, err := store.db.Exec(`UPDATE correlation_rulesets
		SET rules_json = ?,
			rules_projection_version = ?,
			rules_projected_at = NULL
		WHERE id = ?`,
		futurePayload,
		futureVersion,
		ruleset.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCorrelationRuleset(ruleset.ID); err == nil || !strings.Contains(err.Error(), "projection version") {
		t.Fatalf("GetCorrelationRuleset future projection error = %v", err)
	}
	if listed, err := store.ListCorrelationRulesets("project-a", 0, 0); err == nil || listed != nil || !strings.Contains(err.Error(), "projection version") {
		t.Fatalf("ListCorrelationRulesets future projection = (%#v, %v)", listed, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// An older binary must still be able to start beside a newer writer, but it
	// must neither interpret nor down-migrate the newer projection.
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen with future projection: %v", err)
	}
	defer store.Close()
	var (
		payload         string
		projection      int
		projectedAtNull bool
	)
	if err := store.db.QueryRow(`SELECT rules_json, rules_projection_version,
			rules_projected_at IS NULL
		FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(
		&payload,
		&projection,
		&projectedAtNull,
	); err != nil {
		t.Fatal(err)
	}
	if payload != futurePayload || projection != futureVersion || !projectedAtNull {
		t.Fatalf(
			"future projection was changed on reopen: payload=%q version=%d projected_at_null=%t",
			payload,
			projection,
			projectedAtNull,
		)
	}
	if _, err := store.GetCorrelationRuleset(ruleset.ID); err == nil || !strings.Contains(err.Error(), "projection version") {
		t.Fatalf("GetCorrelationRuleset future projection after reopen error = %v", err)
	}
}

func TestCorrelationRulesetLegacyRepairCASPreservesFreshWrite(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a")
	ruleset := &CorrelationRuleset{ID: "ruleset-repair-cas", ProjectID: "project-a", Name: "initial"}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatal(err)
	}
	staleTime := ruleset.UpdatedAt.Add(time.Second)
	const stalePayload = `[{"name":"legacy","source":"response_body","extract_type":"jsonpath","expression":"$.token","sample_value":"STALE-SECRET"}]`
	if _, err := store.db.Exec(`UPDATE correlation_rulesets
		SET rules_json = ?, updated_at = ?
		WHERE id = ?`, stalePayload, staleTime, ruleset.ID); err != nil {
		t.Fatal(err)
	}
	var snapshot legacyCorrelationRulesetSnapshot
	if err := store.db.QueryRow(`SELECT id, rules_json, CAST(updated_at AS TEXT),
			rules_projection_version, CAST(rules_projected_at AS TEXT)
		FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(
		&snapshot.id,
		&snapshot.payload,
		&snapshot.updatedAtRaw,
		&snapshot.projectionVersion,
		&snapshot.projectedAtRaw,
	); err != nil {
		t.Fatal(err)
	}
	fresh := &CorrelationRuleset{
		ID: ruleset.ID, ProjectID: "project-a", Name: "fresh",
		Rules: []CorrelationRule{{Name: "safe", Source: "response_body", ExtractType: "jsonpath", Expression: "$.safe"}},
	}
	if err := store.SaveCorrelationRuleset(fresh); err != nil {
		t.Fatal(err)
	}
	rewritten, err := store.rewriteLegacyCorrelationRulesetIfUnchanged(snapshot, "[]")
	if err != nil {
		t.Fatal(err)
	}
	if rewritten {
		t.Fatal("stale migration snapshot overwrote a fresh ruleset write")
	}
	got, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "fresh" || len(got.Rules) != 1 || got.Rules[0].Expression != "$.safe" {
		t.Fatalf("fresh ruleset was mutated: %#v", got)
	}
}

func TestCorrelationRulesetRulesAreCountAndByteBounded(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a")

	tooMany := &CorrelationRuleset{
		ID:        "too-many-rules",
		ProjectID: "project-a",
		Name:      "too many",
		Rules:     make([]CorrelationRule, maxCorrelationRulesetRules+1),
	}
	if err := store.SaveCorrelationRuleset(tooMany); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("SaveCorrelationRuleset count error = %v", err)
	}
	tooLargeBeforeMarshal := &CorrelationRuleset{
		ID:        "huge-internal-rule",
		ProjectID: "project-a",
		Name:      "huge internal rule",
		Rules: []CorrelationRule{{
			Name:       "huge",
			Expression: strings.Repeat("x", maxCorrelationRulesetRulesJSONSize+1),
		}},
	}
	if err := store.SaveCorrelationRuleset(tooLargeBeforeMarshal); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("SaveCorrelationRuleset pre-marshal byte error = %v", err)
	}

	valid := &CorrelationRuleset{ID: "oversized-rules", ProjectID: "project-a", Name: "oversized"}
	if err := store.SaveCorrelationRuleset(valid); err != nil {
		t.Fatalf("SaveCorrelationRuleset valid: %v", err)
	}
	// This fixture is over the cap in bytes but below it in rune count, guarding
	// against SQLite LENGTH(text) accidentally becoming the security boundary.
	oversized := `["` + strings.Repeat("界", maxCorrelationRulesetRulesJSONSize/3+1) + `"]`
	if len(oversized) <= maxCorrelationRulesetRulesJSONSize {
		t.Fatalf("oversized fixture = %d bytes", len(oversized))
	}
	if _, err := store.db.Exec(`UPDATE correlation_rulesets SET rules_json = ? WHERE id = ?`, oversized, valid.ID); err != nil {
		t.Fatalf("seed oversized rules: %v", err)
	}
	if _, err := store.GetCorrelationRuleset(valid.ID); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("GetCorrelationRuleset oversized error = %v", err)
	}
	if err := store.repairLegacyCorrelationRulesets(); err != nil {
		t.Fatalf("repairLegacyCorrelationRulesets: %v", err)
	}
	got, err := store.GetCorrelationRuleset(valid.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset repaired: %v", err)
	}
	if len(got.Rules) != 0 {
		t.Fatalf("oversized rules were not cleared: %#v", got.Rules)
	}

	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("marshal repaired ruleset: %v", err)
	}
}

func TestCorrelationRulesetOptimisticUpdateRejectsStaleAndOwnershipChanges(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a", "project-b")

	ruleset := &CorrelationRuleset{ID: "ruleset-cas", ProjectID: "project-a", Name: "original"}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatalf("SaveCorrelationRuleset: %v", err)
	}
	if ruleset.UpdatedAt.Location() != time.UTC || ruleset.UpdatedAt.Nanosecond()%1_000 != 0 {
		t.Fatalf("created updated_at = %s, want UTC microsecond precision", ruleset.UpdatedAt)
	}
	first, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset first: %v", err)
	}
	stale, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset stale: %v", err)
	}
	first.Name = "first writer"
	firstVersion := first.UpdatedAt
	if updated, err := store.UpdateCorrelationRulesetIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	if !first.UpdatedAt.After(firstVersion) || first.UpdatedAt.Nanosecond()%1_000 != 0 {
		t.Fatalf("updated version = %s after %s, want strictly newer microsecond timestamp", first.UpdatedAt, firstVersion)
	}
	stale.Name = "stale writer"
	if updated, err := store.UpdateCorrelationRulesetIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	fresh, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset fresh: %v", err)
	}
	if fresh.Name != "first writer" || fresh.ProjectID != "project-a" {
		t.Fatalf("fresh ruleset = %#v, want first writer in project-a", fresh)
	}

	expected := fresh.UpdatedAt
	fresh.ProjectID = "project-b"
	if updated, err := store.UpdateCorrelationRulesetIfUnchanged(fresh, expected); err != nil || updated {
		t.Fatalf("ownership-changing update = (%v, %v), want rejection", updated, err)
	}
	if err := store.SaveCorrelationRuleset(&CorrelationRuleset{ID: ruleset.ID, ProjectID: "project-b", Name: "legacy update"}); err == nil {
		t.Fatal("SaveCorrelationRuleset accepted a cross-project update")
	}
	fresh, err = store.GetCorrelationRuleset(ruleset.ID)
	if err != nil || fresh.ProjectID != "project-a" || fresh.Name != "first writer" {
		t.Fatalf("ruleset ownership after upsert = %#v, err=%v", fresh, err)
	}
	if err := store.SaveCorrelationRuleset(&CorrelationRuleset{ID: "missing-owner", ProjectID: "missing-project", Name: "bad"}); err == nil {
		t.Fatal("SaveCorrelationRuleset accepted a missing owner project")
	}

	deleteTarget := &CorrelationRuleset{ID: "ruleset-delete-cas", ProjectID: "project-a", Name: "delete me"}
	if err := store.SaveCorrelationRuleset(deleteTarget); err != nil {
		t.Fatalf("SaveCorrelationRuleset delete target: %v", err)
	}
	staleDelete, err := store.GetCorrelationRuleset(deleteTarget.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset stale delete: %v", err)
	}
	currentDelete, err := store.GetCorrelationRuleset(deleteTarget.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset current delete: %v", err)
	}
	currentDelete.Name = "concurrent edit"
	if updated, err := store.UpdateCorrelationRulesetIfUnchanged(currentDelete, currentDelete.UpdatedAt); err != nil || !updated {
		t.Fatalf("concurrent delete-target update = (%v, %v)", updated, err)
	}
	if deleted, err := store.DeleteCorrelationRulesetIfUnchanged(staleDelete.ID, staleDelete.ProjectID, staleDelete.UpdatedAt); err != nil || deleted {
		t.Fatalf("stale guarded delete = (%v, %v), want conflict", deleted, err)
	}
	currentDelete, err = store.GetCorrelationRuleset(deleteTarget.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset after stale delete: %v", err)
	}
	if deleted, err := store.DeleteCorrelationRulesetIfUnchanged(currentDelete.ID, "project-b", currentDelete.UpdatedAt); err != nil || deleted {
		t.Fatalf("cross-project guarded delete = (%v, %v), want rejection", deleted, err)
	}
	if deleted, err := store.DeleteCorrelationRulesetIfUnchanged(currentDelete.ID, currentDelete.ProjectID, currentDelete.UpdatedAt); err != nil || !deleted {
		t.Fatalf("current guarded delete = (%v, %v), want success", deleted, err)
	}
}

func TestScriptUpdateAtomicallyFencesCorrelationRulesetRevision(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a", "project-b")

	script := &dsl.Script{
		ID:        "ruleset-fenced-script",
		ProjectID: "project-a",
		Name:      "Original script",
		Protocol:  "http",
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	currentScript, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	ruleset := &CorrelationRuleset{
		ID:        "script-update-ruleset-fence",
		ProjectID: "project-a",
		Name:      "Original ruleset",
	}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatal(err)
	}
	staleRulesetUpdatedAt := ruleset.UpdatedAt
	concurrent := *ruleset
	concurrent.Name = "Concurrent ruleset editor won"
	updated, err := store.UpdateCorrelationRulesetIfUnchanged(&concurrent, staleRulesetUpdatedAt)
	if err != nil || !updated {
		t.Fatalf("concurrent ruleset update = (%t, %v)", updated, err)
	}

	candidate := *currentScript
	candidate.Name = "Stale apply"
	updated, err = store.UpdateScriptIfUnchangedAndCorrelationRulesetCurrent(
		&candidate,
		currentScript.UpdatedAt,
		ruleset.ID,
		ruleset.ProjectID,
		staleRulesetUpdatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("script update committed against a stale correlation ruleset revision")
	}
	reloaded, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "Original script" {
		t.Fatalf("stale ruleset update mutated script: %#v", reloaded)
	}

	candidate = *reloaded
	candidate.Name = "Current apply"
	updated, err = store.UpdateScriptIfUnchangedAndCorrelationRulesetCurrent(
		&candidate,
		reloaded.UpdatedAt,
		ruleset.ID,
		ruleset.ProjectID,
		concurrent.UpdatedAt,
	)
	if err != nil || !updated {
		t.Fatalf("current ruleset fenced update = (%t, %v)", updated, err)
	}
	reloaded, err = store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreignRuleset := &CorrelationRuleset{
		ID:        "foreign-script-update-ruleset-fence",
		ProjectID: "project-b",
		Name:      "Foreign ruleset",
	}
	if err := store.SaveCorrelationRuleset(foreignRuleset); err != nil {
		t.Fatal(err)
	}
	candidate = *reloaded
	candidate.Name = "Cross-project apply"
	if updated, err := store.UpdateScriptIfUnchangedAndCorrelationRulesetCurrent(
		&candidate,
		reloaded.UpdatedAt,
		foreignRuleset.ID,
		foreignRuleset.ProjectID,
		foreignRuleset.UpdatedAt,
	); err == nil || updated || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("cross-project ruleset fence = (%t, %v), want rejection", updated, err)
	}
}

func TestCorrelationRulesetMigrationSQLRewritesForPostgres(t *testing.T) {
	rewrite := rewriteSQL(`UPDATE correlation_rulesets
		SET rules_json = ?, rules_projection_version = ?, rules_projected_at = updated_at
		WHERE id = ? AND COALESCE(rules_json, '') = ?
			AND CAST(updated_at AS TEXT) = ?
			AND COALESCE(rules_projection_version, 0) = ?
			AND CAST(rules_projected_at AS TEXT) = ?`, storeDialectPostgres)
	if strings.Contains(rewrite, "?") || !strings.Contains(rewrite, "$7") ||
		!strings.Contains(rewrite, "rules_projected_at = updated_at") {
		t.Fatalf("PostgreSQL projection rewrite = %s", rewrite)
	}
	currentLock := rewriteSQL(`SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT)
		FROM correlation_rulesets WHERE id = ? FOR UPDATE`, storeDialectPostgres)
	if strings.Contains(currentLock, "?") || !strings.Contains(currentLock, "$1") || !strings.Contains(currentLock, "FOR UPDATE") {
		t.Fatalf("PostgreSQL current-version lock = %s", currentLock)
	}
	if got := correlationRulesetJSONByteLengthSQL(storeDialectPostgres, "rules_json"); got != "OCTET_LENGTH(COALESCE(rules_json, ''))" {
		t.Fatalf("PostgreSQL rules byte length = %s", got)
	}
}
