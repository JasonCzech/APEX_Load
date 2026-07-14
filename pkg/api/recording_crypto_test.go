package api

import (
	"bytes"
	"testing"
)

func TestRecordingBaselineEncryptionRoundTrip(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "unit-test-recording-key")
	plain := []byte(`[{"password":"not-in-ciphertext"}]`)
	encrypted, err := encryptRecordingBaseline(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(encrypted), []byte("not-in-ciphertext")) {
		t.Fatal("ciphertext contains plaintext")
	}
	decrypted, err := decryptRecordingBaseline(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("round trip = %q, want %q", decrypted, plain)
	}
}

func TestRecordingBaselineEncryptionFailsClosedWithoutKey(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "")
	if _, err := encryptRecordingBaseline([]byte("secret")); err == nil {
		t.Fatal("encryption unexpectedly succeeded without a key")
	}
	if _, err := decryptRecordingBaseline("enc:v1:invalid"); err == nil {
		t.Fatal("decryption unexpectedly succeeded without a key")
	}
}
