package collectoragent

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// Credentials are sealed for one signed job lease and never written in plaintext
// to the collector spool. Destination identity is authenticated as associated data.
type Credentials struct {
	Prometheus  *PrometheusCredentials  `json:"prometheus,omitempty"`
	AppDynamics *AppDynamicsCredentials `json:"appdynamics,omitempty"`
	Username    string                  `json:"username,omitempty"`
	Password    string                  `json:"password,omitempty"`
	PrivateKey  string                  `json:"private_key,omitempty"`
}
type SealedCredentials struct {
	Version      uint32 `json:"version,omitempty"`
	EphemeralKey string `json:"ephemeral_key"`
	Nonce        string `json:"nonce"`
	Ciphertext   string `json:"ciphertext"`
}

func credentialContext(job RunJob) []byte {
	b, _ := json.Marshal([]any{"apexload.monitor-credentials.v1", job.ID, job.ProjectID, job.CollectorID, job.SourceID, job.SourceRevision, job.Pack, job.Destination, job.LeaseExpiresAt.UTC()})
	return b
}
func SealCredentials(job RunJob, publicKey []byte, credentials Credentials) (*SealedCredentials, error) {
	remote, err := ecdh.X25519().NewPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(remote)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(append(shared, credentialContext(job)...))
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	body, err := json.Marshal(credentials)
	if err != nil || len(body) > 16<<10 {
		return nil, errors.New("credential bundle too large")
	}
	encode := base64.StdEncoding.EncodeToString
	return &SealedCredentials{EphemeralKey: encode(ephemeral.PublicKey().Bytes()), Nonce: encode(nonce), Ciphertext: encode(aead.Seal(nil, nonce, body, credentialContext(job)))}, nil
}
func OpenCredentials(job RunJob, privateKey []byte, now time.Time) (Credentials, error) {
	var out Credentials
	if !job.LeaseExpiresAt.After(now) || job.Credentials == nil || job.Credentials.Version > 1 {
		return out, errors.New("credential lease unavailable")
	}
	local, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return out, err
	}
	b, err := base64.StdEncoding.DecodeString(job.Credentials.EphemeralKey)
	if err != nil {
		return out, err
	}
	remote, err := ecdh.X25519().NewPublicKey(b)
	if err != nil {
		return out, err
	}
	shared, err := local.ECDH(remote)
	if err != nil {
		return out, err
	}
	key := sha256.Sum256(append(shared, credentialContext(job)...))
	aead, _ := chacha20poly1305.NewX(key[:])
	nonce, err := base64.StdEncoding.DecodeString(job.Credentials.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return out, errors.New("invalid credential nonce")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(job.Credentials.Ciphertext)
	if err != nil || len(ciphertext) > 17<<10 {
		return out, errors.New("invalid credential ciphertext")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, credentialContext(job))
	if err != nil {
		return out, errors.New("credential identity check failed")
	}
	err = json.Unmarshal(plaintext, &out)
	clear(plaintext)
	return out, err
}
