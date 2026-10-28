package keywrap

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/zalando/go-keyring"
)

const (
	defaultKeyringService = "apexload.key-wrapping"
	defaultKeyringAccount = "master-key-v1"
	keyVaultAPIVersion    = "7.4"
	keyVaultAlgorithm     = "RSA-OAEP-256"
)

type LocalKeyOptions struct {
	Key            []byte
	KeyID          string
	AllowKeyring   bool
	KeyringService string
	KeyringAccount string
}

type LocalAESKeyWrapper struct {
	aead  cipher.AEAD
	keyID string
}

func NewLocalAESKeyWrapper(options LocalKeyOptions) (*LocalAESKeyWrapper, error) {
	key := append([]byte(nil), options.Key...)
	keyID := strings.TrimSpace(options.KeyID)
	if len(key) == 0 {
		var err error
		key, keyID, err = loadLocalMasterKey(options)
		if err != nil {
			return nil, err
		}
	}
	if len(key) < 32 {
		return nil, errors.New("wrapping master key must contain at least 32 bytes")
	}
	digest := sha256.Sum256(key)
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if keyID == "" {
		keyID = "local:" + base64.RawURLEncoding.EncodeToString(digest[:8])
	}
	return &LocalAESKeyWrapper{aead: aead, keyID: keyID}, nil
}

func loadLocalMasterKey(options LocalKeyOptions) ([]byte, string, error) {
	if configured := strings.TrimSpace(os.Getenv(MasterKeyEnvVar)); configured != "" {
		key, err := DecodeConfiguredKey(configured)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", MasterKeyEnvVar, err)
		}
		return key, "env:" + MasterKeyEnvVar, nil
	}
	if !options.AllowKeyring {
		return nil, "", fmt.Errorf("wrapping key is not configured; set %s or configure a Key Vault key", MasterKeyEnvVar)
	}
	service := firstConfigured(strings.TrimSpace(options.KeyringService), defaultKeyringService)
	account := firstConfigured(strings.TrimSpace(options.KeyringAccount), defaultKeyringAccount)
	stored, err := keyring.Get(service, account)
	if err == nil {
		key, decodeErr := base64.RawStdEncoding.DecodeString(stored)
		if decodeErr != nil || len(key) != 32 {
			return nil, "", errors.New("OS keychain contains an invalid wrapping master key")
		}
		return key, "keyring:" + service + "/" + account, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, "", fmt.Errorf("read wrapping master key from OS keychain: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, "", err
	}
	if err := keyring.Set(service, account, base64.RawStdEncoding.EncodeToString(key)); err != nil {
		return nil, "", fmt.Errorf("persist wrapping master key in OS keychain: %w", err)
	}
	return key, "keyring:" + service + "/" + account, nil
}

func DecodeConfiguredKey(value string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil && len(decoded) >= 32 {
			return decoded, nil
		}
	}
	if len([]byte(value)) < 32 {
		return nil, errors.New("must be at least 32 bytes or base64-encode at least 32 random bytes")
	}
	return []byte(value), nil
}

func (w *LocalAESKeyWrapper) WrapKey(_ context.Context, plaintext, aad []byte) (WrappedKey, error) {
	nonce := make([]byte, w.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return WrappedKey{}, err
	}
	sealed := w.aead.Seal(nonce, nonce, plaintext, aad)
	return WrappedKey{Ciphertext: base64.RawStdEncoding.EncodeToString(sealed), Metadata: map[string]any{
		"provider": "local_aes_gcm", "algorithm": "AES-256-GCM", "key_id": w.keyID,
	}}, nil
}

func (w *LocalAESKeyWrapper) UnwrapKey(_ context.Context, wrapped WrappedKey, aad []byte) ([]byte, error) {
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(wrapped.Ciphertext))
	if err != nil {
		return nil, fmt.Errorf("decode wrapped key: %w", err)
	}
	if len(raw) < w.aead.NonceSize() {
		return nil, errors.New("wrapped key is truncated")
	}
	return w.aead.Open(nil, raw[:w.aead.NonceSize()], raw[w.aead.NonceSize():], aad)
}

// KeyRingWrapper wraps new data-encryption keys with the active master key and
// unwraps existing envelopes with either the active key or any retired previous
// key, enabling zero-downtime master key rotation.
type KeyRingWrapper struct {
	active   KeyWrapper
	previous []KeyWrapper
}

func NewKeyRingWrapper(active KeyWrapper, previous ...KeyWrapper) (*KeyRingWrapper, error) {
	if active == nil {
		return nil, errors.New("active key wrapper is required")
	}
	filtered := make([]KeyWrapper, 0, len(previous))
	for _, prev := range previous {
		if prev != nil {
			filtered = append(filtered, prev)
		}
	}
	return &KeyRingWrapper{active: active, previous: filtered}, nil
}

func (w *KeyRingWrapper) WrapKey(ctx context.Context, plaintext, aad []byte) (WrappedKey, error) {
	return w.active.WrapKey(ctx, plaintext, aad)
}

func (w *KeyRingWrapper) UnwrapKey(ctx context.Context, wrapped WrappedKey, aad []byte) ([]byte, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	key, err := w.active.UnwrapKey(ctx, wrapped, aad)
	if err == nil {
		return key, nil
	}
	errs := make([]error, 0, 1+len(w.previous))
	errs = append(errs, err)
	for _, prev := range w.previous {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		unwrapped, prevErr := prev.UnwrapKey(ctx, wrapped, aad)
		if prevErr == nil {
			return unwrapped, nil
		}
		errs = append(errs, prevErr)
	}
	return nil, errors.Join(errs...)
}

// RotateWrappedKey unwraps a data-encryption key with current and re-wraps the
// exact same plaintext key material under next using the same AAD.
func RotateWrappedKey(ctx context.Context, current, next KeyWrapper, wrapped WrappedKey, aad []byte) (WrappedKey, error) {
	if current == nil || next == nil {
		return WrappedKey{}, errors.New("current and next key wrappers are required")
	}
	plaintext, err := current.UnwrapKey(ctx, wrapped, aad)
	if err != nil {
		return WrappedKey{}, fmt.Errorf("unwrap key for rotation: %w", err)
	}
	defer clear(plaintext)
	rewrapped, err := next.WrapKey(ctx, plaintext, aad)
	if err != nil {
		return WrappedKey{}, fmt.Errorf("rewrap key for rotation: %w", err)
	}
	return rewrapped, nil
}

type AzureKeyVaultKeyWrapper struct {
	keyID      string
	credential azcore.TokenCredential
	client     *http.Client
}

func NewAzureKeyVaultKeyWrapper(keyID string, credential azcore.TokenCredential, client *http.Client) (*AzureKeyVaultKeyWrapper, error) {
	keyID = strings.TrimRight(strings.TrimSpace(keyID), "/")
	parsed, err := url.Parse(keyID)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || !validVersionedKeyVaultKeyPath(parsed.EscapedPath()) {
		return nil, errors.New("Azure Key Vault key id must be an HTTPS /keys/{name}/{version} URL")
	}
	if credential == nil {
		credential, err = azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, err
		}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	// A 307/308 response causes net/http to replay this client's POST body.
	// WrapKey sends a plaintext data-encryption key in that body, so redirects
	// must be rejected even when a caller supplied a client that follows them.
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &AzureKeyVaultKeyWrapper{keyID: keyID, credential: credential, client: &safeClient}, nil
}

func validVersionedKeyVaultKeyPath(escapedPath string) bool {
	segments := strings.Split(strings.Trim(escapedPath, "/"), "/")
	if len(segments) != 3 || segments[0] != "keys" {
		return false
	}
	for _, segment := range segments[1:] {
		decoded, err := url.PathUnescape(segment)
		if err != nil || strings.TrimSpace(decoded) == "" || decoded == "." || decoded == ".." ||
			strings.ContainsAny(decoded, "/\\") {
			return false
		}
	}
	return true
}

func NewConfiguredKeyWrapper(allowKeyring bool) (KeyWrapper, error) {
	vaultKeyID := strings.TrimSpace(os.Getenv(KeyVaultKeyIDEnvVar))
	kmsKeyARN := strings.TrimSpace(os.Getenv(KMSKeyIDEnvVar))
	if vaultKeyID != "" && kmsKeyARN != "" {
		// Silently picking one is not a misconfiguration, it is data loss:
		// every envelope already sealed by the loser becomes permanently
		// unreadable, and nothing would report why.
		return nil, fmt.Errorf("both %s and %s are set; configure exactly one wrapping key",
			KeyVaultKeyIDEnvVar, KMSKeyIDEnvVar)
	}
	if vaultKeyID != "" {
		return NewAzureKeyVaultKeyWrapper(vaultKeyID, nil, nil)
	}
	if kmsKeyARN != "" {
		return NewAwsKMSKeyWrapper(kmsKeyARN, nil, nil)
	}
	return NewLocalAESKeyWrapper(LocalKeyOptions{AllowKeyring: allowKeyring})
}

// ConfiguredWrappingProvider reports which wrapping key the environment selects
// and whether the selection is ambiguous. Readiness uses it so an operator can
// see a conflict; server startup only logs a warning when the wrapper fails to
// build, which is easy to miss.
func ConfiguredWrappingProvider() (provider string, conflict bool) {
	vaultKeyID := strings.TrimSpace(os.Getenv(KeyVaultKeyIDEnvVar))
	kmsKeyARN := strings.TrimSpace(os.Getenv(KMSKeyIDEnvVar))
	switch {
	case vaultKeyID != "" && kmsKeyARN != "":
		return "conflict", true
	case vaultKeyID != "":
		return "azure_key_vault", false
	case kmsKeyARN != "":
		return "aws_kms", false
	default:
		return "local", false
	}
}

// Azure Key Vault's RSA wrap API cannot authenticate AAD. Artifact and secret
// ciphertexts still bind identity through their own AEAD layer; callers must
// verify the persisted key id before unwrapping.
func (w *AzureKeyVaultKeyWrapper) WrapKey(ctx context.Context, plaintext, _ []byte) (WrappedKey, error) {
	value, err := w.keyOperation(ctx, "wrapkey", base64.RawURLEncoding.EncodeToString(plaintext))
	if err != nil {
		return WrappedKey{}, err
	}
	return WrappedKey{Ciphertext: value, Metadata: map[string]any{
		"provider": "azure_key_vault", "algorithm": keyVaultAlgorithm, "key_id": w.keyID,
	}}, nil
}

func (w *AzureKeyVaultKeyWrapper) UnwrapKey(ctx context.Context, wrapped WrappedKey, _ []byte) ([]byte, error) {
	if keyID, _ := wrapped.Metadata["key_id"].(string); strings.TrimSpace(keyID) != "" && strings.TrimRight(keyID, "/") != w.keyID {
		return nil, errors.New("wrapped key belongs to a different Key Vault key")
	}
	value, err := w.keyOperation(ctx, "unwrapkey", wrapped.Ciphertext)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode Azure Key Vault unwrapped key: %w", err)
	}
	return decoded, nil
}

func (w *AzureKeyVaultKeyWrapper) keyOperation(ctx context.Context, operation, value string) (string, error) {
	token, err := w.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://vault.azure.net/.default"}})
	if err != nil {
		return "", fmt.Errorf("authenticate Azure Key Vault wrapping key: %w", err)
	}
	body, err := json.Marshal(map[string]string{"alg": keyVaultAlgorithm, "value": value})
	if err != nil {
		return "", err
	}
	defer clear(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.keyID+"/"+operation+"?api-version="+keyVaultAPIVersion, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := w.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Azure Key Vault %s: %w", operation, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	defer clear(payload)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Key Vault and compatible gateways may echo request fields in an error
		// document. For wrapkey that request contains the plaintext data-
		// encryption key, so the response body must never be promoted into an
		// application error, log, audit event, or API response.
		return "", fmt.Errorf("Azure Key Vault %s returned %d", operation, response.StatusCode)
	}
	var decoded struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", err
	}
	if strings.TrimSpace(decoded.Value) == "" {
		return "", fmt.Errorf("Azure Key Vault %s returned an empty value", operation)
	}
	return decoded.Value, nil
}

func firstConfigured(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
