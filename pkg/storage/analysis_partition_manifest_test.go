package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestAnalysisPartitionManifestClaimPublicationAndGenericMutation(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if in.PartitionManifestVersion != 1 || a.PartitionManifestVersion != 1 || in.PartitionManifest != nil {
		t.Fatal("new claim did not require fresh producer proof")
	}
	if err = s.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, a.ID); err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, in, a)
	for name, mutate := range map[string]func(*AnalysisImport){
		"proof-missing":   func(v *AnalysisImport) { v.PartitionManifest = nil },
		"claim-downgrade": func(v *AnalysisImport) { v.PartitionManifestVersion = 0; v.PartitionManifest = nil },
		"mapping-rebind": func(v *AnalysisImport) {
			m := *v.Mapping
			m.Measures = append([]analysis.ParquetMeasure(nil), m.Measures...)
			m.Measures[0].Name = "other"
			v.Mapping = &m
		},
		"accepted-measures": func(v *AnalysisImport) { v.Accepted++ },
		"artifact-size":     func(v *AnalysisImport) { v.PartitionManifest.Partitions[0].Bytes++ },
		"artifact-key":      func(v *AnalysisImport) { v.PartitionManifest.Partitions[0].ObjectKey = "foreign" },
	} {
		t.Run(name, func(t *testing.T) {
			v := publication.Import
			v.PartitionManifest = publication.Import.PartitionManifest.Clone()
			mutate(&v)
			digest, err := AnalysisAttemptResultDigest(v)
			if err != nil {
				return
			}
			if _, err = s.PublishAnalysisImportAttempt(ctx, AnalysisAttemptPublication{Import: v, AttemptID: a.ID, Digest: digest}); !errors.Is(err, ErrAnalysisAuthority) {
				t.Fatal("nonproducer/foreign proof published", err)
			}
		})
	}
	ready, err := s.PublishAnalysisImportAttempt(ctx, publication)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetAnalysisImport(in.ID)
	if err != nil || !reflect.DeepEqual(loaded.PartitionManifest, ready.PartitionManifest) {
		t.Fatal("durable proof changed", err)
	}
	for _, mutation := range []func(*AnalysisImport){func(v *AnalysisImport) { v.PartitionManifest = nil }, func(v *AnalysisImport) { v.PartitionManifestVersion = 0 }, func(v *AnalysisImport) {
		v.PartitionManifest.Partitions[0].Stats.Identity = &analysis.NormalizedPartitionIdentity{Source: in.ID, Dimensions: map[string]string{}}
	}} {
		v := *loaded
		v.PartitionManifest = loaded.PartitionManifest.Clone()
		mutation(&v)
		if _, err = s.SaveAnalysisImport(v, v.Revision); err == nil {
			t.Fatal("generic save rewrote owner-managed pruning proof")
		}
	}
}

func TestAnalysisPartitionManifestStrictDurableDecodeAndOwnerColumns(t *testing.T) {
	s, _, _ := monitorFixture(t)
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(context.Background(), mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, in, a)
	body, err := json.Marshal(publication.Import)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(string(body), `"partition_manifest_version":1`, `"Partition_manifest_version":1`, 1),
		strings.Replace(string(body), `"partition_manifest_version":1`, `"partition_manifest_version":1,"partition_manifest_version":0`, 1),
		strings.Replace(string(body), `"version":1,`, `"version":1,"version":1,`, 1),
		strings.Replace(string(body), `"rows":3`, `"Rows":3`, 1),
		strings.Replace(string(body), `"rows":3`, `"rows":null`, 1),
		strings.Replace(string(body), `"min_time":`, `"extra":false,"min_time":`, 1),
		strings.Replace(string(body), `"partition_manifest_version":1`, `"partition_manifest_version":0`, 1),
	} {
		var value AnalysisImport
		if err = json.Unmarshal([]byte(bad), &value); err == nil {
			t.Fatal("noncanonical claimed durable proof silently rebound")
		}
	}
	value := AnalysisImport{Owner: "separately-read-owner", LeaseExpiresAt: time.Now().UTC()}
	if err = json.Unmarshal(body, &value); err != nil || value.Owner != "separately-read-owner" || value.LeaseExpiresAt.IsZero() {
		t.Fatal("custom metadata decode lost durable owner columns", err)
	}
	if err = json.Unmarshal([]byte(`{"partition_manifest_version":1,"partition_manifest":{"partitions":[`+strings.Repeat(`{},`, 10000)+`{}]}}`), &value); err == nil {
		t.Fatal("unbounded typed proof population decoded")
	}
}

func TestAnalysisPartitionManifestLegacyDigestsAndAttemptPublication(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// A retained pre-upgrade owner is reconstructed through the durable historical
	// fixture only; no generic mutation can downgrade a current attempt.
	in.PartitionManifestVersion = 0
	a.PartitionManifestVersion = 0
	inputBody, err := json.Marshal(struct {
		UploadWriterVersion int
		Files               []AnalysisImportFile
		Mapping             any
		AllowRejected       bool
		MappingID           string
		MappingRevision     int64
	}{in.UploadWriterVersion, in.Files, in.Mapping, in.AllowRejected, in.MappingID, in.MappingRevision})
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(inputBody)
	a.InputDigest, err = analysisAttemptInputDigest(*in)
	if err != nil || a.InputDigest != hex.EncodeToString(expected[:]) {
		t.Fatal("legacy input digest changed", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = analysisAttemptUpdate(ctx, tx, in, in.Revision, in.Owner, in.State); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = analysisAttemptJournalWrite(ctx, tx, a); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, in, a)
	v := publication.Import
	resultBody, err := json.Marshal(struct {
		Files              []AnalysisImportFile
		Normalized         []string
		NormalizedSHA256   map[string]string
		RejectionArtifacts []string
		RejectionSHA256    map[string]string
		Rejections         any
		Accepted, Rejected int64
	}{v.Files, v.Normalized, v.NormalizedSHA256, v.RejectionArtifacts, v.RejectionSHA256, v.Rejections, v.Accepted, v.Rejected})
	if err != nil {
		t.Fatal(err)
	}
	expected = sha256.Sum256(resultBody)
	if publication.Digest != hex.EncodeToString(expected[:]) {
		t.Fatal("legacy publication digest changed")
	}
	if err = s.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, a.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := s.PublishAnalysisImportAttempt(ctx, publication)
	if err != nil || ready.PartitionManifestVersion != 0 || ready.PartitionManifest != nil {
		t.Fatal("retained legacy publication contract refused/upgraded", err)
	}
}

func TestAnalysisPartitionManifestTakeoverClearsPriorProof(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	prior := nativeAttemptPublicationFixture(t, in, a).Import
	// Retained failed metadata is seeded physically; a generic save cannot attach
	// unpublished worker evidence. The next owner must never inherit its proof.
	prior.State, prior.Owner, prior.LeaseExpiresAt = "failed", "", time.Time{}
	priorBody, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE analysis_imports SET payload=?,state='failed',owner='',lease_expires_at=? WHERE id=?`, string(priorBody), time.Time{}, prior.ID); err != nil {
		t.Fatal(err)
	}
	next, b, err := s.ClaimAnalysisImportAttempt(ctx, prior.ID, prior.ProjectID, prior.Revision, time.Minute)
	if err != nil || next.PartitionManifestVersion != 1 || b.PartitionManifestVersion != 1 || next.PartitionManifest != nil || next.Accepted != 0 || next.Rejected != 0 || b.ID == a.ID {
		t.Fatal("new owner inherited stale producer proof", err)
	}
	for _, key := range next.Normalized {
		if key == prior.Normalized[0] {
			t.Fatal("takeover reused prior physical output")
		}
	}
}

func TestAnalysisPartitionManifestReceiptRejectsRedigestedCorruption(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, in, a)
	if err = s.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublishAnalysisImportAttempt(ctx, publication); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := analysisAttemptJournalLoad(ctx, tx, a.ID, in.ProjectID)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	parts := receipt.Published.PartitionManifest.Clone().Partitions
	later := parts[0].Stats.MaxTime.Add(time.Second)
	parts[0].Stats.MaxTime = &later
	receipt.Published.PartitionManifest, err = analysis.NewNormalizedPartitionManifest(in.ProjectID, in.ID, *in.Mapping, parts)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	// Corruption retains the old publication digest and a structurally valid,
	// internally redigested manifest. No application writer may create this row.
	body, err := json.Marshal(receipt)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE analysis_attempt_journal SET payload=? WHERE id=?`, string(body), a.ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveAnalysisAttemptPublication(ctx, in.ID, in.ProjectID, a.ID, publication.Digest); !errors.Is(err, ErrAnalysisPublicationUnknown) {
		t.Fatal("corrupted receipt reconciled as accepted evidence", err)
	}
	if _, err = s.PublishAnalysisImportAttempt(ctx, publication); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("corrupted receipt replay trusted unchanged old digest", err)
	}
}
