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

const recordingBaselineKeyEnvVar = "APEXLOAD_RECORDING_ENCRYPTION_KEY"
const recordingBaselineCipherPrefix = "enc:v1:"

func recordingBaselineCipher() (cipher.AEAD, error) {
	key := strings.TrimSpace(os.Getenv(recordingBaselineKeyEnvVar))
	if key == "" {
		return nil, fmt.Errorf("recording baseline encryption key (%s) is not configured", recordingBaselineKeyEnvVar)
	}
	digest := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func recordingBaselineEncryptionAvailable() bool {
	return strings.TrimSpace(os.Getenv(recordingBaselineKeyEnvVar)) != ""
}

func encryptRecordingBaseline(plaintext []byte) (string, error) {
	aead, err := recordingBaselineCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)
	packed := append(nonce, ciphertext...)
	return recordingBaselineCipherPrefix + base64.RawStdEncoding.EncodeToString(packed), nil
}

func decryptRecordingBaseline(encoded string) ([]byte, error) {
	if !strings.HasPrefix(encoded, recordingBaselineCipherPrefix) {
		return nil, fmt.Errorf("unsupported recording baseline ciphertext")
	}
	aead, err := recordingBaselineCipher()
	if err != nil {
		return nil, err
	}
	packed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, recordingBaselineCipherPrefix))
	if err != nil {
		return nil, err
	}
	if len(packed) < aead.NonceSize() {
		return nil, fmt.Errorf("recording baseline ciphertext is truncated")
	}
	return aead.Open(nil, packed[:aead.NonceSize()], packed[aead.NonceSize():], nil)
}
