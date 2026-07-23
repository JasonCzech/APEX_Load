package api

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testRecordingBaselineBinding() recordingBaselineBinding {
	return recordingBaselineBinding{
		ScriptID:          "script-1",
		ProjectID:         "project-1",
		ScriptIncarnation: "sha256:incarnation-one",
		ActionGraphDigest: "sha256:action-graph-one",
		SchemaVersion:     recordingBaselineSchemaVersion,
	}
}

func TestRecordingBaselineEncryptionV3RoundTripAndBinding(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "unit-test-recording-key")
	plain := []byte(`[{"password":"not-in-ciphertext"}]`)
	binding := testRecordingBaselineBinding()
	encrypted, err := encryptRecordingBaseline(plain, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encrypted, recordingBaselineCipherPrefixV3) {
		t.Fatalf("ciphertext prefix = %q, want v3", encrypted)
	}
	if bytes.Contains([]byte(encrypted), []byte("not-in-ciphertext")) {
		t.Fatal("ciphertext contains plaintext")
	}
	decrypted, err := decryptRecordingBaseline(encrypted, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("round trip = %q, want %q", decrypted, plain)
	}

	tamperedScript := binding
	tamperedScript.ScriptID = "script-2"
	tamperedProject := binding
	tamperedProject.ProjectID = "project-2"
	tamperedIncarnation := binding
	tamperedIncarnation.ScriptIncarnation = "sha256:incarnation-two"
	tamperedGraph := binding
	tamperedGraph.ActionGraphDigest = "sha256:action-graph-two"
	tamperedSchema := binding
	tamperedSchema.SchemaVersion = "apexload.recording.v4"
	tamperedBindings := []recordingBaselineBinding{tamperedScript, tamperedProject, tamperedIncarnation, tamperedGraph, tamperedSchema}
	for _, tampered := range tamperedBindings {
		if _, err := decryptRecordingBaseline(encrypted, tampered); err == nil {
			t.Fatalf("v3 ciphertext decrypted with altered binding: %#v", tampered)
		}
	}
}

func TestRecordingBaselineEncryptionRejectsUnboundLegacyV1(t *testing.T) {
	if _, err := decryptRecordingBaseline(recordingBaselineCipherPrefixV1+"opaque", testRecordingBaselineBinding()); !errors.Is(err, errRecordingBaselineCiphertextUnbound) {
		t.Fatalf("decrypt legacy v1 error = %v, want unbound ciphertext error", err)
	}
}

func TestRecordingBaselineEncryptionRejectsUnboundLegacyV2(t *testing.T) {
	if _, err := decryptRecordingBaseline(recordingBaselineCipherPrefixV2+"opaque", testRecordingBaselineBinding()); !errors.Is(err, errRecordingBaselineCiphertextUnbound) {
		t.Fatalf("decrypt legacy v2 error = %v, want unbound ciphertext error", err)
	}
}

func TestRecordingBaselineEncryptionFailsClosedWithoutKey(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "")
	if _, err := encryptRecordingBaseline([]byte("secret"), testRecordingBaselineBinding()); err == nil {
		t.Fatal("encryption unexpectedly succeeded without a key")
	}
	if _, err := decryptRecordingBaseline("enc:v1:invalid", testRecordingBaselineBinding()); err == nil {
		t.Fatal("decryption unexpectedly succeeded without a key")
	}
}

func TestRecordingBaselineEncryptionRejectsOversizedPlaintextAndCiphertext(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "unit-test-recording-key")
	if _, err := encryptRecordingBaseline(make([]byte, maxRecordedBaselinePlaintextSize+1), testRecordingBaselineBinding()); err == nil || !strings.Contains(err.Error(), "plaintext exceeds") {
		t.Fatalf("oversized plaintext error = %v", err)
	}
	oversized := recordingBaselineCipherPrefixV3 + strings.Repeat("A", maxRecordingBaselineCiphertextSize-len(recordingBaselineCipherPrefixV3)+1)
	if _, err := decryptRecordingBaseline(oversized, testRecordingBaselineBinding()); err == nil || !strings.Contains(err.Error(), "ciphertext exceeds") {
		t.Fatalf("oversized ciphertext error = %v", err)
	}
}

func TestRecordingBaselineEncryptionRejectsNonCanonicalAndTruncatedEnvelopes(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "unit-test-recording-key")
	binding := testRecordingBaselineBinding()
	encrypted, err := encryptRecordingBaseline([]byte(`{"token":"value"}`), binding)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimPrefix(encrypted, recordingBaselineCipherPrefixV3)
	withNewline := recordingBaselineCipherPrefixV3 + encoded[:8] + "\n" + encoded[8:]
	if _, err := decryptRecordingBaseline(withNewline, binding); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("newline-bearing envelope error = %v, want malformed", err)
	}

	// Twenty-eight zero bytes have the minimum nonce+tag length and end in a
	// canonical zero-valued partial symbol. Changing only its unused low bits
	// creates a second textual representation that permissive decoders accept.
	canonicalMinimum := base64.RawStdEncoding.EncodeToString(make([]byte, 12+16))
	nonCanonicalTrailingBits := recordingBaselineCipherPrefixV3 + strings.TrimSuffix(canonicalMinimum, "A") + "B"
	if _, err := decryptRecordingBaseline(nonCanonicalTrailingBits, binding); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("non-canonical trailing bits error = %v, want malformed", err)
	}

	truncated := recordingBaselineCipherPrefixV3 + base64.RawStdEncoding.EncodeToString(make([]byte, 12+16-1))
	if _, err := decryptRecordingBaseline(truncated, binding); err == nil {
		t.Fatal("truncated nonce/tag envelope unexpectedly decrypted")
	}
}
