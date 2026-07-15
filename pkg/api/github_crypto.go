package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

// githubTokenCipher builds the AES-GCM cipher used to encrypt per-user GitHub
// tokens at rest. The key is derived (SHA-256) from APEXLOAD_GITHUB_TOKEN_KEY so
// any sufficiently random operator-supplied string works. When the env var is
// unset the GitHub integration's token storage is unavailable (fail-closed): no
// token is ever written or read in plaintext, and handlers surface a clear error.
func githubTokenCipher() (cipher.AEAD, error) {
	raw := strings.TrimSpace(os.Getenv(githubTokenKeyEnvVar))
	if raw == "" {
		return nil, fmt.Errorf("github token encryption key (%s) is not configured", githubTokenKeyEnvVar)
	}
	if productionMode() && len([]byte(raw)) < productionSecretMinBytes {
		return nil, fmt.Errorf("github token encryption key (%s) must contain at least %d bytes in production", githubTokenKeyEnvVar, productionSecretMinBytes)
	}
	key := sha256.Sum256([]byte(raw))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// githubTokenEncryptionAvailable reports whether token encryption is configured.
func githubTokenEncryptionAvailable() bool {
	_, err := githubTokenCipher()
	return err == nil
}

func encryptGitHubToken(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	aead, err := githubTokenCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptGitHubToken(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	aead, err := githubTokenCipher()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid github token ciphertext: %w", err)
	}
	if len(raw) < aead.NonceSize() {
		return "", fmt.Errorf("github token ciphertext is truncated")
	}
	nonce, ciphertext := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("github token decryption failed: %w", err)
	}
	return string(plaintext), nil
}
