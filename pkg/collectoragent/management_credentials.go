package collectoragent

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

const ManagementCredentialPurpose = "messaging_management"

type ManagementSASLCredentials struct {
	Mechanism string `json:"mechanism"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}
type ManagementSharedKeyCredentials struct {
	KeyName string `json:"key_name"`
	Key     string `json:"key"`
}
type ManagementBasicCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type ManagementCredentials struct {
	Version   uint32                          `json:"version"`
	Purpose   string                          `json:"purpose"`
	Mode      string                          `json:"mode"`
	SASL      *ManagementSASLCredentials      `json:"sasl,omitempty"`
	SharedKey *ManagementSharedKeyCredentials `json:"shared_key,omitempty"`
	Basic     *ManagementBasicCredentials     `json:"basic,omitempty"`
}

func (c ManagementCredentials) Validate() error {
	if c.Version != 2 || c.Purpose != ManagementCredentialPurpose {
		return errors.New("management credential purpose rejected")
	}
	union := 0
	if c.SASL != nil {
		union++
	}
	if c.SharedKey != nil {
		union++
	}
	if c.Basic != nil {
		union++
	}
	switch c.Mode {
	case "sasl":
		if union != 1 || c.SASL == nil || !managementLabel(c.SASL.Username, 1024) || c.SASL.Password == "" || len(c.SASL.Password) > 8192 || c.SASL.Mechanism != "plain" && c.SASL.Mechanism != "scram_sha_256" && c.SASL.Mechanism != "scram_sha_512" {
			return errors.New("invalid management SASL credentials")
		}
	case "shared_key":
		if union != 1 || c.SharedKey == nil || !managementLabel(c.SharedKey.KeyName, 1024) || c.SharedKey.Key == "" || len(c.SharedKey.Key) > 8192 {
			return errors.New("invalid management shared-key credentials")
		}
	case "basic":
		if union != 1 || c.Basic == nil || !managementLabel(c.Basic.Username, 1024) || c.Basic.Password == "" || len(c.Basic.Password) > 8192 {
			return errors.New("invalid management basic credentials")
		}
	case "workload", "managed", "mtls", "anonymous":
		if union != 0 {
			return errors.New("management workload identity cannot contain secrets")
		}
	default:
		return errors.New("unsupported management credential mode")
	}
	return nil
}
func managementCredentialContext(g ManagementPollGrant) []byte {
	data, _ := json.Marshal([]any{"apexload.monitor-credentials.v2", ManagementCredentialPurpose, g.PollID, g.ProjectID, g.SourceID, g.SourceRevision, g.CollectorID, g.CollectorInstanceID, g.Generation, g.Pack, g.Destination, g.ScopeHash, g.IdentityHash, g.Deadline.UTC(), g.AuthorizationExpiresAt.UTC()})
	return data
}
func SealManagementCredentials(g ManagementPollGrant, publicKey []byte, c ManagementCredentials) (*SealedCredentials, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	remote, err := ecdh.X25519().NewPublicKey(publicKey)
	if err != nil {
		return nil, errors.New("collector credential key unavailable")
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(remote)
	if err != nil {
		return nil, errors.New("collector credential key rejected")
	}
	defer clear(shared)
	key := sha256.Sum256(append(shared, managementCredentialContext(g)...))
	defer clear(key[:])
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	body, err := json.Marshal(c)
	if err != nil || len(body) > 16<<10 {
		return nil, errors.New("management credential bundle exceeds bounds")
	}
	defer clear(body)
	encode := base64.StdEncoding.EncodeToString
	return &SealedCredentials{Version: 2, EphemeralKey: encode(ephemeral.PublicKey().Bytes()), Nonce: encode(nonce), Ciphertext: encode(aead.Seal(nil, nonce, body, managementCredentialContext(g)))}, nil
}
func OpenManagementCredentials(g ManagementPollGrant, privateKey []byte, now time.Time) (ManagementCredentials, error) {
	var out ManagementCredentials
	if !g.AuthorizationExpiresAt.After(now) || !g.Deadline.After(now) || g.Credentials == nil || g.Credentials.Version != 2 {
		return out, errors.New("management credential lease unavailable")
	}
	if err := g.Validate(); err != nil {
		return out, err
	}
	local, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return out, errors.New("collector credential key unavailable")
	}
	sealed := g.Credentials
	if len(sealed.EphemeralKey) > 128 || len(sealed.Nonce) > 128 || len(sealed.Ciphertext) > 24<<10 {
		return out, errors.New("management credential ciphertext exceeds bounds")
	}
	raw, err := base64.StdEncoding.DecodeString(sealed.EphemeralKey)
	if err != nil {
		return out, errors.New("invalid management credential key")
	}
	remote, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return out, errors.New("invalid management credential key")
	}
	shared, err := local.ECDH(remote)
	if err != nil {
		return out, errors.New("management credential key rejected")
	}
	defer clear(shared)
	key := sha256.Sum256(append(shared, managementCredentialContext(g)...))
	defer clear(key[:])
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return out, err
	}
	nonce, err := base64.StdEncoding.DecodeString(sealed.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return out, errors.New("invalid management credential nonce")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(sealed.Ciphertext)
	if err != nil || len(ciphertext) > 17<<10 {
		return out, errors.New("invalid management credential ciphertext")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, managementCredentialContext(g))
	if err != nil {
		return out, errors.New("management credential identity rejected")
	}
	defer clear(plaintext)
	if err = strictManagementJSON(plaintext, &out); err != nil {
		return ManagementCredentials{}, errors.New("invalid management credential bundle")
	}
	if err = out.Validate(); err != nil {
		return ManagementCredentials{}, err
	}
	return out, nil
}

func managementCredentialMode(identity string) string {
	switch identity {
	case "sasl_plain", "sasl_scram":
		return "sasl"
	case "workload_identity":
		return "workload"
	case "managed_identity":
		return "managed"
	}
	return identity
}

// MatchesIdentity validates the purpose-bound bundle against the locally
// approved public principal before any credential or transport use.
func (c ManagementCredentials) MatchesIdentity(identity analysis.ManagementIdentity) bool {
	if c.Validate() != nil || identity.Validate() != nil || c.Mode != managementCredentialMode(identity.Mode) {
		return false
	}
	switch identity.Mode {
	case "sasl_plain":
		return c.SASL != nil && c.SASL.Username == identity.Principal && c.SASL.Mechanism == "plain"
	case "sasl_scram":
		return c.SASL != nil && c.SASL.Username == identity.Principal && (c.SASL.Mechanism == "scram_sha_256" || c.SASL.Mechanism == "scram_sha_512")
	case "basic":
		return c.Basic != nil && c.Basic.Username == identity.Principal
	case "shared_key":
		return c.SharedKey != nil && c.SharedKey.KeyName == identity.Principal
	default:
		return true
	}
}

func managementCredentialsMatchIdentity(c ManagementCredentials, identity analysis.ManagementIdentity) bool {
	return c.MatchesIdentity(identity)
}
