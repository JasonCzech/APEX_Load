package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBrunoNativeCommitV1DigestGolden(t *testing.T) {
	folderID := " native-folder "
	migrate := true
	request := brunoCommitRequest{
		ProjectID:              " project-a ",
		ImportSessionID:        " session-1 ",
		Mode:                   " CREATE_NATIVE_BATCH ",
		FolderID:               &folderID,
		CandidateIDs:           []string{" candidate-b ", "candidate-a"},
		NameOverrides:          map[string]string{" candidate-b ": " Beta ", "candidate-a": "Alpha"},
		SecretBindings:         map[string]string{" requirement-b ": " TOKEN_B ", "requirement-a": "TOKEN_A"},
		MigrateSecrets:         &migrate,
		AcknowledgedFindingIDs: []string{" finding-b ", "finding-a"},
	}
	if err := normalizeBrunoNativeCommitRequest(&request); err != nil {
		t.Fatalf("normalize request: %v", err)
	}

	got, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV1, request)
	if err != nil {
		t.Fatalf("digest request: %v", err)
	}
	const want = "ff0184f8855cdc3fccac09d9860cef4dddc0bea195da6162038bab3e22ac4a0d"
	if got != want {
		t.Fatalf("digest = %q, want frozen %q", got, want)
	}
}

func TestBrunoNativeCommitV1DigestPreservesOmissionDistinctions(t *testing.T) {
	base := brunoCommitRequest{
		ProjectID:       "project-a",
		ImportSessionID: "session-1",
		Mode:            "create_native_batch",
	}
	omitted := base
	if err := normalizeBrunoNativeCommitRequest(&omitted); err != nil {
		t.Fatalf("normalize omitted request: %v", err)
	}
	explicit := base
	explicit.CandidateIDs = []string{"candidate-a"}
	explicit.NameOverrides = map[string]string{}
	if err := normalizeBrunoNativeCommitRequest(&explicit); err != nil {
		t.Fatalf("normalize explicit request: %v", err)
	}

	omittedSHA, err := brunoNativeCommitRequestSHA(omitted)
	if err != nil {
		t.Fatal(err)
	}
	explicitSHA, err := brunoNativeCommitRequestSHA(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if omittedSHA == explicitSHA {
		t.Fatal("v1 digest collapsed omitted and explicit candidate/name fields")
	}
}

func TestBrunoNativeCommitDigestRejectsUnknownProfile(t *testing.T) {
	_, err := brunoNativeCommitRequestSHAForProfile("bruno-native-commit-v99", brunoCommitRequest{
		ImportSessionID: "session-1",
	})
	if err == nil {
		t.Fatal("unknown digest profile was accepted")
	}
}

// V1 omitted shared_values, so two commits differing only in the operator's
// collection-wide values hashed identically and the idempotent replay path
// returned the first commit's batch while reporting success for the second.
func TestBrunoNativeCommitV2DigestCoversSharedValues(t *testing.T) {
	base := brunoCommitRequest{
		ProjectID:       "project-a",
		ImportSessionID: "session-1",
		Mode:            "create_native_batch",
		CandidateIDs:    []string{"candidate-a"},
	}
	stage := base
	stage.SharedValues = map[string]string{"sv_base": "https://stage.example.com"}
	prod := base
	prod.SharedValues = map[string]string{"sv_base": "https://prod.example.com"}
	for _, request := range []*brunoCommitRequest{&stage, &prod} {
		if err := normalizeBrunoNativeCommitRequest(request); err != nil {
			t.Fatalf("normalize request: %v", err)
		}
	}

	v1Stage, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV1, stage)
	if err != nil {
		t.Fatal(err)
	}
	v1Prod, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV1, prod)
	if err != nil {
		t.Fatal(err)
	}
	if v1Stage != v1Prod {
		t.Fatal("v1 unexpectedly distinguishes shared values; this test documents why v2 exists")
	}

	v2Stage, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV2, stage)
	if err != nil {
		t.Fatal(err)
	}
	v2Prod, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV2, prod)
	if err != nil {
		t.Fatal(err)
	}
	if v2Stage == v2Prod {
		t.Fatal("v2 digest collapsed two commits that differ in shared values")
	}
}

// New receipts must be written under the profile whose hash produced them.
func TestBrunoNativeCommitDefaultDigestProfileIsV2(t *testing.T) {
	if brunoNativeCommitCurrentDigestProfile != storage.ImportBatchDigestBrunoNativeCommitV2 {
		t.Fatalf("current digest profile = %q, want v2", brunoNativeCommitCurrentDigestProfile)
	}
	request := brunoCommitRequest{ProjectID: "project-a", ImportSessionID: "session-1", Mode: "create_native_batch"}
	if err := normalizeBrunoNativeCommitRequest(&request); err != nil {
		t.Fatalf("normalize request: %v", err)
	}
	got, err := brunoNativeCommitRequestSHA(request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV2, request)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("brunoNativeCommitRequestSHA no longer matches the current profile")
	}
}
