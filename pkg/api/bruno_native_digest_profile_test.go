package api

import (
	"testing"
	"time"

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
func TestBrunoNativeCommitDefaultDigestProfileIsV3(t *testing.T) {
	if brunoNativeCommitCurrentDigestProfile != storage.ImportBatchDigestBrunoNativeCommitV3 {
		t.Fatalf("current digest profile = %q, want v3", brunoNativeCommitCurrentDigestProfile)
	}
	request := brunoCommitRequest{ProjectID: "project-a", ImportSessionID: "session-1", Mode: "create_native_batch"}
	if err := normalizeBrunoNativeCommitRequest(&request); err != nil {
		t.Fatalf("normalize request: %v", err)
	}
	got, err := brunoNativeCommitRequestSHA(request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV3, request)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("brunoNativeCommitRequestSHA no longer matches the current profile")
	}
}

func brunoNativeDigestGoldenRequest(t *testing.T) brunoCommitRequest {
	t.Helper()
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
		SharedValues:           map[string]string{"sv_base": "https://stage.example.com"},
		MigrateSecrets:         &migrate,
		AcknowledgedFindingIDs: []string{" finding-b ", "finding-a"},
		UpdateTargets: map[string]brunoNativeUpdateTarget{
			" candidate-a ": {ScriptID: " script-a ", ExpectedUpdatedAt: time.Date(2026, 10, 14, 9, 30, 0, 123456789, time.FixedZone("CDT", -5*3600))},
		},
		HeaderRepairs: map[string]string{" finding-a ": " TRIM "},
	}
	if err := normalizeBrunoNativeCommitRequest(&request); err != nil {
		t.Fatalf("normalize request: %v", err)
	}
	return request
}

// V2 receipts exist, so its hash is frozen like V1's.
func TestBrunoNativeCommitV2DigestGolden(t *testing.T) {
	got, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV2, brunoNativeDigestGoldenRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	const want = "60a7adf24c6ec7e4179c575c2cfdb674a9fd8170588df13d3420aaa65e81386b"
	if got != want {
		t.Fatalf("digest = %q, want frozen %q", got, want)
	}
}

func TestBrunoNativeCommitV3DigestGolden(t *testing.T) {
	got, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV3, brunoNativeDigestGoldenRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	const want = "e0fe3df1cbc59d9ef0bd20335520d5a7125e3a9faca22ffd561483db83c3d1a1"
	if got != want {
		t.Fatalf("digest = %q, want frozen %q", got, want)
	}
}

func TestBrunoNativeCommitV3DigestCoversUpdateTargetsAndHeaderRepairs(t *testing.T) {
	base := brunoNativeDigestGoldenRequest(t)
	digest := func(request brunoCommitRequest) string {
		t.Helper()
		got, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV3, request)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	baseDigest := digest(base)

	copyRow := base
	copyRow.UpdateTargets = map[string]brunoNativeUpdateTarget{}
	laterReview := base
	laterReview.UpdateTargets = map[string]brunoNativeUpdateTarget{
		"candidate-a": {ScriptID: "script-a", ExpectedUpdatedAt: base.UpdateTargets["candidate-a"].ExpectedUpdatedAt.Add(time.Nanosecond)},
	}
	removeHeader := base
	removeHeader.HeaderRepairs = map[string]string{"finding-a": "remove"}
	for name, request := range map[string]brunoCommitRequest{
		"create instead of update": copyRow,
		"different reviewed time":  laterReview,
		"different header repair":  removeHeader,
	} {
		if digest(request) == baseDigest {
			t.Fatalf("v3 digest ignores %s", name)
		}
	}
	v2Base, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV2, base)
	if err != nil {
		t.Fatal(err)
	}
	v2Copy, err := brunoNativeCommitRequestSHAForProfile(storage.ImportBatchDigestBrunoNativeCommitV2, copyRow)
	if err != nil {
		t.Fatal(err)
	}
	if v2Base != v2Copy {
		t.Fatal("v2 unexpectedly distinguishes update targets; this test documents why v3 exists")
	}
}

func TestBrunoNativeCommitRequestRejectsInvalidUpdateTargetsAndRepairs(t *testing.T) {
	for name, mutate := range map[string]func(*brunoCommitRequest){
		"empty script id": func(request *brunoCommitRequest) {
			request.UpdateTargets = map[string]brunoNativeUpdateTarget{"candidate-a": {ExpectedUpdatedAt: time.Now()}}
		},
		"missing reviewed time": func(request *brunoCommitRequest) {
			request.UpdateTargets = map[string]brunoNativeUpdateTarget{"candidate-a": {ScriptID: "script-a"}}
		},
		"same script twice": func(request *brunoCommitRequest) {
			now := time.Now()
			request.UpdateTargets = map[string]brunoNativeUpdateTarget{
				"candidate-a": {ScriptID: "script-a", ExpectedUpdatedAt: now},
				"candidate-b": {ScriptID: "script-a", ExpectedUpdatedAt: now},
			}
		},
		"unknown repair": func(request *brunoCommitRequest) {
			request.HeaderRepairs = map[string]string{"finding-a": "rewrite"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := brunoCommitRequest{ProjectID: "project-a", ImportSessionID: "session-1", Mode: "create_native_batch"}
			mutate(&request)
			if err := normalizeBrunoNativeCommitRequest(&request); err == nil {
				t.Fatal("normalize accepted an invalid request")
			}
		})
	}
}
