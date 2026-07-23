package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

const recordingBaselineKeyEnvVar = "APEXLOAD_RECORDING_ENCRYPTION_KEY"

const (
	recordingBaselineCipherPrefixV1 = "enc:v1:"
	recordingBaselineCipherPrefixV2 = "enc:v2:"
	recordingBaselineCipherPrefixV3 = "enc:v3:"
	recordingBaselineSchemaVersion  = "apexload.recording.v3"
	// AES-GCM uses a 12-byte nonce and 16-byte authentication tag here. The
	// unpadded base64 ceiling is derived from the same 12 MiB plaintext bound
	// enforced by baseline persistence.
	maxRecordingBaselineCiphertextSize = len(recordingBaselineCipherPrefixV3) +
		((maxRecordedBaselinePlaintextSize+12+16)*4+2)/3
)

var errRecordingBaselineCiphertextUnbound = errors.New("legacy recording baseline ciphertext is not bound to its script row")

type recordingBaselineBinding struct {
	ScriptID          string
	ProjectID         string
	ScriptIncarnation string
	ActionGraphDigest string
	SchemaVersion     string
}

type recordingBaselineAdditionalData struct {
	Domain            string `json:"domain"`
	CipherVersion     string `json:"cipher_version"`
	SchemaVersion     string `json:"schema_version"`
	ScriptID          string `json:"script_id"`
	ProjectID         string `json:"project_id"`
	ScriptIncarnation string `json:"script_incarnation"`
	ActionGraphDigest string `json:"action_graph_digest"`
}

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

func recordingBaselineAAD(binding recordingBaselineBinding) ([]byte, error) {
	binding.ScriptID = strings.TrimSpace(binding.ScriptID)
	binding.ProjectID = normalizeProjectID(strings.TrimSpace(binding.ProjectID))
	binding.ScriptIncarnation = strings.TrimSpace(binding.ScriptIncarnation)
	binding.ActionGraphDigest = strings.TrimSpace(binding.ActionGraphDigest)
	binding.SchemaVersion = strings.TrimSpace(binding.SchemaVersion)
	if binding.ScriptID == "" {
		return nil, fmt.Errorf("recording baseline script id is required")
	}
	if binding.ScriptIncarnation == "" {
		return nil, fmt.Errorf("recording baseline script incarnation is required")
	}
	if binding.ActionGraphDigest == "" {
		return nil, fmt.Errorf("recording baseline action-graph digest is required")
	}
	if binding.SchemaVersion == "" {
		return nil, fmt.Errorf("recording baseline schema version is required")
	}
	return json.Marshal(recordingBaselineAdditionalData{
		Domain:            "apexload.recording-baseline",
		CipherVersion:     "v3",
		SchemaVersion:     binding.SchemaVersion,
		ScriptID:          binding.ScriptID,
		ProjectID:         binding.ProjectID,
		ScriptIncarnation: binding.ScriptIncarnation,
		ActionGraphDigest: binding.ActionGraphDigest,
	})
}

func encryptRecordingBaseline(plaintext []byte, binding recordingBaselineBinding) (string, error) {
	if len(plaintext) > maxRecordedBaselinePlaintextSize {
		return "", fmt.Errorf("recording baseline plaintext exceeds %d bytes", maxRecordedBaselinePlaintextSize)
	}
	aead, err := recordingBaselineCipher()
	if err != nil {
		return "", err
	}
	aad, err := recordingBaselineAAD(binding)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	packed := append(nonce, ciphertext...)
	return recordingBaselineCipherPrefixV3 + base64.RawStdEncoding.EncodeToString(packed), nil
}

func decryptRecordingBaseline(encoded string, binding recordingBaselineBinding) ([]byte, error) {
	var (
		ciphertext string
		aad        []byte
		err        error
	)
	switch {
	case strings.HasPrefix(encoded, recordingBaselineCipherPrefixV3):
		if len(encoded) > maxRecordingBaselineCiphertextSize {
			return nil, fmt.Errorf("recording baseline ciphertext exceeds %d bytes", maxRecordingBaselineCiphertextSize)
		}
		if !storage.IsCanonicalEncryptedScriptRecordingSidecar(encoded) {
			return nil, fmt.Errorf("recording baseline ciphertext envelope is malformed")
		}
		ciphertext = strings.TrimPrefix(encoded, recordingBaselineCipherPrefixV3)
		aad, err = recordingBaselineAAD(binding)
		if err != nil {
			return nil, err
		}
	case strings.HasPrefix(encoded, recordingBaselineCipherPrefixV1), strings.HasPrefix(encoded, recordingBaselineCipherPrefixV2):
		// v1/v2 predate durable parent-incarnation binding: their AAD layouts no
		// longer exist in this codebase, so no re-encryption migration is
		// possible even with the original key, and returning legacy plaintext
		// would let a ciphertext copied from another row feed correlation
		// automation. Storage retains these rows so callers can report a precise
		// recapture requirement; the only remediation is capturing a new
		// recording, which re-seals as v3.
		slog.Warn("recorded correlation baseline uses legacy enc:v1/enc:v2 encryption that current builds cannot decrypt; capture a new recording to restore correlation diagnosis", "script_id", binding.ScriptID)
		return nil, errRecordingBaselineCiphertextUnbound
	case strings.HasPrefix(encoded, "enc:"):
		return nil, fmt.Errorf("unsupported recording baseline ciphertext version")
	default:
		return nil, fmt.Errorf("unsupported recording baseline ciphertext")
	}
	aead, err := recordingBaselineCipher()
	if err != nil {
		return nil, err
	}
	packed, err := base64.RawStdEncoding.Strict().DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	if len(packed) < aead.NonceSize()+aead.Overhead() {
		return nil, fmt.Errorf("recording baseline ciphertext is truncated")
	}
	plaintext, err := aead.Open(nil, packed[:aead.NonceSize()], packed[aead.NonceSize():], aad)
	if err != nil {
		return nil, err
	}
	if len(plaintext) > maxRecordedBaselinePlaintextSize {
		return nil, fmt.Errorf("recording baseline plaintext exceeds %d bytes", maxRecordedBaselinePlaintextSize)
	}
	return plaintext, nil
}
