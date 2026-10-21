package storage

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func browserAcceptanceBE(id string) *FeatureAcceptanceRecord {
	return &FeatureAcceptanceRecord{ID: id, RequirementID: "BE-4", Feature: "browser-metrics", Profile: "desktop", Provider: "chromium", Platform: "darwin-arm64", CommitSHA: strings.Repeat("a", 40), BinarySHA256: strings.Repeat("b", 64), Suite: "native-document-acceptance", Result: AcceptancePassed, Artifacts: []FeatureAcceptanceArtifact{{Name: "qualified.json", SHA256: strings.Repeat("c", 64)}}, RecordedAt: time.Now().UTC(), RecordedBy: "user:reviewer", Qualification: &BrowserAcceptanceQualification{Version: 1, RuntimeSHA256: strings.Repeat("d", 64), CapabilitySHA256: strings.Repeat("e", 64), Metric: "lcp", MeasurementAlgorithm: "document-metrics.v1"}}
}
func browserAcceptanceBI(t *testing.T, base *FeatureAcceptanceRecord) *FeatureAcceptanceRecord {
	t.Helper()
	record := browserAcceptanceBE("bi")
	record.RequirementID = "BI-1"
	record.Feature = "browser-findings"
	record.Suite = "browser-intelligence"
	record.Qualification.AnalysisAlgorithm = "threshold.v1"
	hash, err := BrowserAcceptanceRecordSHA256(base)
	if err != nil {
		t.Fatal(err)
	}
	record.Qualification.Dependencies = []BrowserAcceptanceDependency{{RecordID: base.ID, RecordSHA256: hash}}
	return record
}

func TestBrowserAcceptanceDependencyRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*FeatureAcceptanceRecord)
	}{
		{"missing-cohort", func(r *FeatureAcceptanceRecord) { r.Qualification = nil }},
		{"missing-dependency", func(r *FeatureAcceptanceRecord) { r.Qualification.Dependencies = nil }},
		{"unknown-record", func(r *FeatureAcceptanceRecord) { r.Qualification.Dependencies[0].RecordID = "absent" }},
		{"record-digest", func(r *FeatureAcceptanceRecord) {
			r.Qualification.Dependencies[0].RecordSHA256 = strings.Repeat("f", 64)
		}},
		{"binary", func(r *FeatureAcceptanceRecord) { r.BinarySHA256 = strings.Repeat("f", 64) }},
		{"full-commit", func(r *FeatureAcceptanceRecord) { r.CommitSHA = r.CommitSHA[:7] }},
		{"other-commit", func(r *FeatureAcceptanceRecord) { r.CommitSHA = strings.Repeat("f", 40) }},
		{"engine", func(r *FeatureAcceptanceRecord) { r.Provider = "webkit" }},
		{"profile", func(r *FeatureAcceptanceRecord) { r.Profile = "pixel_8" }},
		{"platform", func(r *FeatureAcceptanceRecord) { r.Platform = "linux-amd64" }},
		{"runtime", func(r *FeatureAcceptanceRecord) { r.Qualification.RuntimeSHA256 = strings.Repeat("f", 64) }},
		{"capability", func(r *FeatureAcceptanceRecord) { r.Qualification.CapabilitySHA256 = strings.Repeat("f", 64) }},
		{"metric", func(r *FeatureAcceptanceRecord) { r.Qualification.Metric = "inp" }},
		{"measurement", func(r *FeatureAcceptanceRecord) { r.Qualification.MeasurementAlgorithm = "other.v1" }},
		{"analysis-missing", func(r *FeatureAcceptanceRecord) { r.Qualification.AnalysisAlgorithm = "" }},
		{"version", func(r *FeatureAcceptanceRecord) { r.Qualification.Version = 2 }},
		{"duplicate-pin", func(r *FeatureAcceptanceRecord) {
			r.Qualification.Dependencies = append(r.Qualification.Dependencies, r.Qualification.Dependencies[0])
		}},
		{"empty-promotion-evidence", func(r *FeatureAcceptanceRecord) { r.Artifacts = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAuthoringReceiptTestStore(t)
			be := browserAcceptanceBE("be")
			if err := s.AppendFeatureAcceptanceRecord(be); err != nil {
				t.Fatal(err)
			}
			bi := browserAcceptanceBI(t, be)
			tc.mutate(bi)
			if err := s.AppendFeatureAcceptanceRecord(bi); !errors.Is(err, ErrBrowserAcceptanceDependency) {
				t.Fatalf("accepted mismatch: %v", err)
			}
			rows, err := s.ListFeatureAcceptanceRecords("BI-1", "", 10, 0)
			if err != nil || len(rows) != 0 {
				t.Fatal("refusal persisted a claim", rows, err)
			}
		})
	}
}

func TestBrowserAcceptanceDependencyRoundTripAndWithdrawal(t *testing.T) {
	for _, state := range []string{AcceptanceFailed, AcceptancePending, AcceptanceRevoked, AcceptancePassed} {
		t.Run(state, func(t *testing.T) {
			s := newAuthoringReceiptTestStore(t)
			be := browserAcceptanceBE("be")
			if err := s.AppendFeatureAcceptanceRecord(be); err != nil {
				t.Fatal(err)
			}
			bi := browserAcceptanceBI(t, be)
			if err := s.AppendFeatureAcceptanceRecord(bi); err != nil {
				t.Fatal(err)
			}
			rows, err := s.ListFeatureAcceptanceRecords("BI-1", "", 10, 0)
			if err != nil || len(rows) != 1 {
				t.Fatal(err)
			}
			before, _ := json.Marshal(rows[0])
			if err := s.CheckBrowserAcceptanceDependencies(rows[0]); err != nil {
				t.Fatal(err)
			}
			next := browserAcceptanceBE("withdraw")
			next.Result = state
			next.RecordedAt = be.RecordedAt.Add(-time.Hour)
			if state != AcceptancePassed {
				next.Profile = ""
				next.Qualification = nil
			}
			if err := s.AppendFeatureAcceptanceRecord(next); err != nil {
				t.Fatal(err)
			}
			if err := s.CheckBrowserAcceptanceDependencies(rows[0]); !errors.Is(err, ErrBrowserAcceptanceDependency) {
				t.Fatal("stale BE remained supported", err)
			}
			retry := browserAcceptanceBI(t, be)
			retry.ID = "retry"
			if err := s.AppendFeatureAcceptanceRecord(retry); !errors.Is(err, ErrBrowserAcceptanceDependency) {
				t.Fatal("stale promotion accepted", err)
			}
			afterRows, _ := s.ListFeatureAcceptanceRecords("BI-1", "", 10, 0)
			after, _ := json.Marshal(afterRows[0])
			if string(before) != string(after) {
				t.Fatal("historical result rewritten")
			}
		})
	}
}

func TestBrowserAcceptanceDependencyRequiresAcceptedCurrentBE(t *testing.T) {
	for _, state := range []string{AcceptanceFailed, AcceptancePending, AcceptanceRevoked, "historical", "wrong-requirement", "no-evidence"} {
		t.Run(state, func(t *testing.T) {
			s := newAuthoringReceiptTestStore(t)
			be := browserAcceptanceBE("be")
			switch state {
			case "historical":
				be.Qualification = nil
			case "wrong-requirement":
				be.RequirementID = "TEL-4"
			case "no-evidence":
				be.Artifacts = nil
			default:
				be.Result = state
			}
			if err := s.AppendFeatureAcceptanceRecord(be); err != nil {
				t.Fatal(err)
			}
			bi := browserAcceptanceBI(t, be)
			if err := s.AppendFeatureAcceptanceRecord(bi); !errors.Is(err, ErrBrowserAcceptanceDependency) {
				t.Fatal("unaccepted dependency", err)
			}
		})
	}
}

func TestBrowserAcceptancePromotionRechecksAfterConcurrentWithdrawal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acceptance.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	exerciseBrowserAcceptanceConcurrentWithdrawal(t, first, second)
}

func exerciseBrowserAcceptanceConcurrentWithdrawal(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	be := browserAcceptanceBE("be")
	if err := first.AppendFeatureAcceptanceRecord(be); err != nil {
		t.Fatal(err)
	}
	rows, err := second.ListFeatureAcceptanceRecords("BE-4", "", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].RecordSHA256 != be.RecordSHA256 {
		t.Fatal("digest changed after database round trip", err)
	}
	bi := browserAcceptanceBI(t, be)
	if err := second.CheckBrowserAcceptanceDependencies(bi); err != nil {
		t.Fatal(err)
	}
	tx, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	lock := `UPDATE feature_acceptance_records SET seq = seq WHERE 1 = 0`
	if tx.dialect == storeDialectPostgres {
		lock = featureAcceptanceSequenceLock
	}
	if _, err := tx.Exec(lock); err != nil {
		t.Fatal(err)
	}
	started, done := make(chan struct{}), make(chan error, 1)
	go func() { close(started); done <- second.AppendBrowserIntelligenceAcceptanceRecord(bi) }()
	<-started
	select {
	case err := <-done:
		t.Fatalf("promotion escaped writer fence: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := tx.Exec(`INSERT INTO feature_acceptance_records (id,requirement_id,feature,profile,provider,platform,commit_sha,binary_sha256,suite,result,artifacts,notes,recorded_at,recorded_by,seq) SELECT 'withdraw',requirement_id,feature,'','','',commit_sha,binary_sha256,suite,'revoked','[]','',recorded_at,recorded_by,seq+1 FROM feature_acceptance_records WHERE id = ?`, be.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrBrowserAcceptanceDependency) {
			t.Fatal("concurrent withdrawal missed", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("promotion did not unblock")
	}
	rows, err = first.ListFeatureAcceptanceRecords("BI-1", "", 10, 0)
	if err != nil || len(rows) != 0 {
		t.Fatal("withdrawn promotion persisted", rows, err)
	}
}

func TestBrowserAcceptanceLegacySchemaUpgradeKeepsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	old := acceptanceRecord("legacy", "linux", AcceptancePassed, time.Now().UTC())
	if err := s.AppendFeatureAcceptanceRecord(old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE feature_acceptance_records DROP COLUMN cohort_sha256`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE feature_acceptance_records DROP COLUMN qualification`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version = 72`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.ListFeatureAcceptanceRecords("BE-4", "", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].Qualification != nil || rows[0].Seq != old.Seq {
		t.Fatal("legacy history changed", rows, err)
	}
}

func TestBrowserAcceptanceMetricCohortsDoNotSupersedeEachOther(t *testing.T) {
	s := newAuthoringReceiptTestStore(t)
	lcp := browserAcceptanceBE("lcp")
	if err := s.AppendFeatureAcceptanceRecord(lcp); err != nil {
		t.Fatal(err)
	}
	bi := browserAcceptanceBI(t, lcp)
	if err := s.AppendFeatureAcceptanceRecord(bi); err != nil {
		t.Fatal(err)
	}
	cls := browserAcceptanceBE("cls")
	cls.Qualification.Metric = "cls"
	if err := s.AppendFeatureAcceptanceRecord(cls); err != nil {
		t.Fatal(err)
	}
	if cls.CohortSHA256 == lcp.CohortSHA256 {
		t.Fatal("metrics share a subject")
	}
	latest, truncated, err := s.LatestFeatureAcceptanceRecords("BE-4", "browser-metrics", 10)
	if err != nil || truncated || len(latest) != 2 {
		t.Fatal("metric subjects collapsed", latest, err)
	}
	if err := s.CheckBrowserAcceptanceDependencies(bi); err != nil {
		t.Fatal("another metric displaced LCP evidence", err)
	}
	fail := *cls
	fail.ID = "cls-failed"
	fail.Result = AcceptanceFailed
	if err := s.AppendFeatureAcceptanceRecord(&fail); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckBrowserAcceptanceDependencies(bi); err != nil {
		t.Fatal("another metric withdrew LCP evidence", err)
	}
	revoked := *lcp
	revoked.ID = "lcp-revoked"
	revoked.Result = AcceptanceRevoked
	if err := s.AppendFeatureAcceptanceRecord(&revoked); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckBrowserAcceptanceDependencies(bi); !errors.Is(err, ErrBrowserAcceptanceDependency) {
		t.Fatal("matching metric revocation ignored", err)
	}
}

func TestBrowserAcceptanceDigestNormalizesDatabasePrecisionAndIgnoresDisplayDigest(t *testing.T) {
	r := browserAcceptanceBE("be")
	r.Seq = 1
	r.CohortSHA256 = BrowserAcceptanceCohortSHA256(r.Qualification)
	r.RecordedAt = time.Date(2026, 10, 20, 12, 0, 0, 123456789, time.FixedZone("test", 3600))
	first, err := BrowserAcceptanceRecordSHA256(r)
	if err != nil {
		t.Fatal(err)
	}
	r.RecordedAt = r.RecordedAt.UTC().Truncate(time.Microsecond)
	r.RecordSHA256 = "not-an-input"
	second, err := BrowserAcceptanceRecordSHA256(r)
	if err != nil || first != second {
		t.Fatal("database precision or display digest changed binding", err)
	}
	r.Artifacts[0].SHA256 = strings.Repeat("f", 64)
	third, _ := BrowserAcceptanceRecordSHA256(r)
	if first == third {
		t.Fatal("artifact digest not bound")
	}
}

func TestAtomicBrowserPromotionMethodRefusesOtherRecordKinds(t *testing.T) {
	s := newAuthoringReceiptTestStore(t)
	for _, record := range []*FeatureAcceptanceRecord{nil, browserAcceptanceBE("be"), {ID: "pending", RequirementID: "BI-1", Result: AcceptancePending}} {
		if err := s.AppendBrowserIntelligenceAcceptanceRecord(record); !errors.Is(err, ErrBrowserAcceptanceDependency) {
			t.Fatal("non-promotion accepted", err)
		}
	}
}

func TestBrowserEvidenceQualificationCannotVaryByIntelligenceEstimator(t *testing.T) {
	record := browserAcceptanceBE("be")
	record.Qualification.AnalysisAlgorithm = "threshold.v1"
	if err := validateBrowserAcceptanceQualification(record); !errors.Is(err, ErrBrowserAcceptanceDependency) {
		t.Fatal("BE measurement scope accepted a BI estimator", err)
	}
}
