package api

import "testing"

func TestGitHubTokenEncryptionRoundTrip(t *testing.T) {
	t.Setenv("APEXLOAD_GITHUB_TOKEN_KEY", "a-sufficiently-random-operator-key")

	if !githubTokenEncryptionAvailable() {
		t.Fatal("encryption should be available when the key env var is set")
	}

	const token = "ghs_exampletoken1234567890" //nolint:gosec // test fixture
	enc, err := encryptGitHubToken(token)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == "" || enc == token {
		t.Fatalf("ciphertext should be non-empty and not equal plaintext: %q", enc)
	}

	// Nonce randomization: two encryptions differ.
	enc2, _ := encryptGitHubToken(token)
	if enc == enc2 {
		t.Fatal("expected distinct ciphertexts due to random nonce")
	}

	dec, err := decryptGitHubToken(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != token {
		t.Fatalf("round-trip mismatch: %q != %q", dec, token)
	}

	// Empty passes through.
	if out, err := encryptGitHubToken(""); err != nil || out != "" {
		t.Fatalf("empty encrypt = (%q,%v), want (\"\",nil)", out, err)
	}

	// Tamper detection.
	tampered := enc[:len(enc)-2] + "00"
	if _, err := decryptGitHubToken(tampered); err == nil {
		t.Fatal("expected decryption of tampered ciphertext to fail")
	}
}

func TestGitHubTokenEncryptionUnavailableWithoutKey(t *testing.T) {
	t.Setenv("APEXLOAD_GITHUB_TOKEN_KEY", "")
	if githubTokenEncryptionAvailable() {
		t.Fatal("encryption must be unavailable when the key is unset")
	}
	if _, err := encryptGitHubToken("token"); err == nil {
		t.Fatal("encrypt without a key must fail (fail-closed)")
	}
}
