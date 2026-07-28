package api

import (
	"bytes"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestImportCandidateEnvelopeRoundTripsRawSource pins that retained source
// survives the encrypted-envelope codec intact. It is compared byte-for-byte
// because this is the artifact a later audit is supposed to be able to trust as
// "what was actually uploaded" -- a lossy round trip would be worse than no
// retention at all.
func TestImportCandidateEnvelopeRoundTripsRawSource(t *testing.T) {
	source := []byte("openapi: 3.0.0\ninfo:\n  title: probe\n")
	envelope := importCandidateEnvelope{
		Version:         importCandidateSessionVersion,
		Profile:         "openapi",
		ProjectID:       "project-1",
		Script:          dsl.Script{ID: "script-1", Name: "probe", Protocol: "http"},
		RawSource:       source,
		RawSourceFormat: "openapi",
		CreatedAt:       time.Now().UTC(),
		ExpiresAt:       time.Now().UTC().Add(time.Minute),
	}

	payload, err := marshalImportCandidateEnvelope(envelope, nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded, _, err := unmarshalImportCandidateEnvelope(payload)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !bytes.Equal(decoded.RawSource, source) {
		t.Fatalf("raw source did not round trip: got %q want %q", decoded.RawSource, source)
	}
	if decoded.RawSourceFormat != "openapi" {
		t.Fatalf("raw source format = %q, want openapi", decoded.RawSourceFormat)
	}
}

// TestImportCandidateEnvelopeAcceptsPriorVersion pins the upgrade path. A
// session is claimed by whichever binary is running when the user finishes
// reviewing, so a v1 payload written before a deploy must still commit -- just
// without retained source. Rejecting it would fail every import left open
// across a restart.
func TestImportCandidateEnvelopeAcceptsPriorVersion(t *testing.T) {
	if !supportedImportCandidateSessionVersion(1) {
		t.Fatal("v1 candidate envelopes must remain claimable after the v2 bump")
	}
	if !supportedImportCandidateSessionVersion(importCandidateSessionVersion) {
		t.Fatal("the current envelope version must be claimable")
	}
	if supportedImportCandidateSessionVersion(importCandidateSessionVersion + 1) {
		t.Fatal("a version this build does not understand must be refused")
	}
	if supportedImportCandidateSessionVersion(0) {
		t.Fatal("an absent or zero version must be refused")
	}
}

// TestClearProtectedImportCandidateZeroesRetainedSource pins that the retained
// upload is wiped alongside credential values. The source is unredacted by
// definition, so leaving it in a reachable buffer after commit would undo the
// point of holding it encrypted at rest.
func TestClearProtectedImportCandidateZeroesRetainedSource(t *testing.T) {
	source := []byte("secret-bearing-source")
	candidate := &protectedImportCandidate{
		Script:          &dsl.Script{ID: "script-1"},
		RawSource:       source,
		RawSourceFormat: "openapi",
	}

	clearProtectedImportCandidate(candidate)

	if candidate.RawSource != nil {
		t.Fatal("retained source reference should be dropped")
	}
	for index, b := range source {
		if b != 0 {
			t.Fatalf("retained source byte %d was not zeroed: %q", index, source)
		}
	}
}
