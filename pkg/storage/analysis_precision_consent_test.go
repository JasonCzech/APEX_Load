package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestAnalysisPrecisionConsentDurableAuthority(t *testing.T) {
	s, _, _ := monitorFixture(t)
	exerciseAnalysisPrecisionConsent(t, s)
}

func exerciseAnalysisPrecisionConsent(t *testing.T, s *SQLiteStore) {
	t.Helper()
	in := nativeAttemptMappedFixture(t, s)
	mapping := *in.Mapping
	mapping.Version, mapping.PrecisionPolicy = 3, analysis.PrecisionAllowFloat64Rounding
	in.Mapping = &mapping
	in.PrecisionReviewSHA256 = strings.Repeat("a", 64)
	in.PrecisionConsentRevision = in.Revision
	ack, err := analysis.ParquetPrecisionAcknowledgement(in.ProjectID, in.ID, in.Revision, mapping, in.PrecisionReviewSHA256)
	if err != nil {
		t.Fatal(err)
	}
	in.PrecisionAcknowledgementSHA256 = ack
	for _, tc := range []struct {
		name   string
		mutate func(*AnalysisImport)
	}{
		{"review missing", func(v *AnalysisImport) { v.PrecisionReviewSHA256 = "" }},
		{"ack missing", func(v *AnalysisImport) { v.PrecisionAcknowledgementSHA256 = "" }},
		{"revision missing", func(v *AnalysisImport) { v.PrecisionConsentRevision = 0 }},
		{"future revision", func(v *AnalysisImport) { v.PrecisionConsentRevision++ }},
		{"legacy mapping", func(v *AnalysisImport) { m := *v.Mapping; m.Version = 2; m.PrecisionPolicy = ""; v.Mapping = &m }},
		{"mapping edit", func(v *AnalysisImport) { m := *v.Mapping; m.Timezone = "Europe/London"; v.Mapping = &m }},
		{"changed review", func(v *AnalysisImport) { v.PrecisionReviewSHA256 = strings.Repeat("b", 64) }},
		{"missing mapping", func(v *AnalysisImport) { v.Mapping = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := *in
			tc.mutate(&changed)
			if _, err := s.SaveAnalysisImport(changed, in.Revision); err == nil {
				t.Fatal("invalid consent accepted")
			}
			current, err := s.GetAnalysisImport(in.ID)
			if err != nil || current.Revision != in.Revision || current.PrecisionAcknowledgementSHA256 != "" {
				t.Fatal("failed consent save mutated durable row", err)
			}
		})
	}
	saved, err := s.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAnalysisImport(*in, in.Revision); !errors.Is(err, ErrAnalysisRevision) {
		t.Fatal("stale consent CAS accepted", err)
	}
	loaded, err := s.GetAnalysisImport(in.ID)
	if err != nil || loaded.PrecisionAcknowledgementSHA256 != ack || loaded.PrecisionConsentRevision != in.Revision {
		t.Fatal("durable consent mismatch", err)
	}
	claimed, attempt, err := s.ClaimAnalysisImportAttempt(context.Background(), saved.ID, saved.ProjectID, saved.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, claimed, attempt)
	changed := publication
	changed.Import.PrecisionAcknowledgementSHA256 = strings.Repeat("b", 64)
	changed.Digest, err = AnalysisAttemptResultDigest(changed.Import)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmAnalysisAttemptWritersQuiesced(context.Background(), in.ID, in.ProjectID, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublishAnalysisImportAttempt(context.Background(), changed); err == nil {
		t.Fatal("publication changed consent")
	}
	ready, err := s.PublishAnalysisImportAttempt(context.Background(), publication)
	if err != nil || ready.PrecisionAcknowledgementSHA256 != ack {
		t.Fatal("consented publication failed", err)
	}
	changedReady := *ready
	changedReady.PrecisionReviewSHA256 = ""
	changedReady.PrecisionAcknowledgementSHA256 = ""
	changedReady.PrecisionConsentRevision = 0
	if _, err = s.SaveAnalysisImport(changedReady, ready.Revision); err == nil {
		t.Fatal("ready consent cleared")
	}
	ready.State = "deleting"
	if _, err = s.SaveAnalysisImport(*ready, ready.Revision); err != nil {
		t.Fatal("consent prevented deletion", err)
	}
}

func TestAnalysisAttemptPrecisionBindingPreservesLegacyDigest(t *testing.T) {
	in := AnalysisImport{Files: []AnalysisImportFile{{ID: "file", Size: 12}}, MappingID: "mapping", MappingRevision: 2}
	legacy, err := json.Marshal(struct {
		PartitionManifestVersion int `json:"partition_manifest_version,omitempty"`
		UploadWriterVersion      int
		Files                    []AnalysisImportFile
		Mapping                  any
		AllowRejected            bool
		MappingID                string
		MappingRevision          int64
	}{in.PartitionManifestVersion, in.UploadWriterVersion, in.Files, in.Mapping, in.AllowRejected, in.MappingID, in.MappingRevision})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(legacy)
	old, err := analysisAttemptInputDigest(in)
	if err != nil || old != hex.EncodeToString(sum[:]) {
		t.Fatal("legacy digest changed", old, err)
	}
	for _, mutate := range []func(*AnalysisImport){
		func(v *AnalysisImport) { v.PrecisionReviewSHA256 = strings.Repeat("a", 64) },
		func(v *AnalysisImport) { v.PrecisionAcknowledgementSHA256 = strings.Repeat("b", 64) },
		func(v *AnalysisImport) { v.PrecisionConsentRevision = 1 },
	} {
		other := in
		mutate(&other)
		got, err := analysisAttemptInputDigest(other)
		if err != nil || got == old {
			t.Fatal("attempt omitted consent binding", err)
		}
	}
}

func TestAnalysisPrecisionConsentCannotRestoreClearedOldReceipt(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := nativeAttemptMappedFixture(t, s)
	mapping := *in.Mapping
	mapping.Version = 3
	mapping.PrecisionPolicy = analysis.PrecisionAllowFloat64Rounding
	in.Mapping = &mapping
	in.PrecisionConsentRevision = in.Revision
	in.PrecisionReviewSHA256 = strings.Repeat("a", 64)
	var err error
	in.PrecisionAcknowledgementSHA256, err = analysis.ParquetPrecisionAcknowledgement(in.ProjectID, in.ID, in.Revision, mapping, in.PrecisionReviewSHA256)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	cleared := *saved
	cleared.PrecisionReviewSHA256 = ""
	cleared.PrecisionAcknowledgementSHA256 = ""
	cleared.PrecisionConsentRevision = 0
	current, err := s.SaveAnalysisImport(cleared, cleared.Revision)
	if err != nil {
		t.Fatal(err)
	}
	restored := *current
	restored.PrecisionReviewSHA256 = saved.PrecisionReviewSHA256
	restored.PrecisionAcknowledgementSHA256 = saved.PrecisionAcknowledgementSHA256
	restored.PrecisionConsentRevision = saved.PrecisionConsentRevision
	if _, err = s.SaveAnalysisImport(restored, current.Revision); err == nil {
		t.Fatal("restored receipt from a superseded review revision")
	}
}
