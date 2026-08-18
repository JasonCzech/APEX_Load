package keywrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
)

const (
	kmsSigningService = "kms"
	// kmsEncryptionContextKey carries the caller's AAD. Unlike Azure Key
	// Vault's RSA wrap, KMS authenticates this map, so the KeyWrapper
	// interface's aad parameter can actually be honored here.
	kmsEncryptionContextKey = "apexload-aad"
	kmsMaxResponseBytes     = 1 << 20
)

// kmsKeyARNPattern requires a fully-qualified key ARN.
//
// Aliases are deliberately rejected: an alias can be repointed at a different
// key, which is exactly the rotation-confusion hazard the Key Vault wrapper
// guards against by demanding a versioned key ID. A wrapped DEK whose key
// silently changed is unrecoverable ciphertext.
var kmsKeyARNPattern = regexp.MustCompile(`^arn:aws[a-z-]*:kms:([a-z0-9-]{1,32}):\d{12}:key/[A-Za-z0-9-]+$`)

// AwsKMSKeyWrapper wraps data-encryption keys with an AWS KMS customer managed
// key. It speaks the KMS JSON API directly over a SigV4-signed request rather
// than pulling the KMS service module, matching AzureKeyVaultKeyWrapper's
// hand-rolled REST approach.
type AwsKMSKeyWrapper struct {
	keyARN      string
	region      string
	endpoint    string
	credentials aws.CredentialsProvider
	signer      *v4.Signer
	client      *http.Client
}

// NewAwsKMSKeyWrapper builds a wrapper for a pinned KMS key ARN. Region and
// endpoint are derived from the ARN so there is no second setting that could
// disagree with it.
func NewAwsKMSKeyWrapper(keyARN string, credentials aws.CredentialsProvider, client *http.Client) (*AwsKMSKeyWrapper, error) {
	keyARN = strings.TrimSpace(keyARN)
	match := kmsKeyARNPattern.FindStringSubmatch(keyARN)
	if match == nil {
		return nil, errors.New("AWS KMS key id must be a fully qualified arn:aws:kms:{region}:{account}:key/{id} ARN (aliases are not accepted because they can be repointed)")
	}
	region := match[1]

	if credentials == nil {
		loaded, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(region))
		if err != nil {
			return nil, fmt.Errorf("resolve AWS credentials for the KMS wrapping key: %w", err)
		}
		credentials = loaded.Credentials
	}
	if credentials == nil {
		return nil, errors.New("no AWS credentials are available for the KMS wrapping key")
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
	return &AwsKMSKeyWrapper{
		keyARN:      keyARN,
		region:      region,
		endpoint:    "https://kms." + region + ".amazonaws.com/",
		credentials: credentials,
		signer:      v4.NewSigner(),
		client:      &safeClient,
	}, nil
}

// WrapKey encrypts a data key under the KMS key, binding the caller's AAD into
// the KMS encryption context.
//
// This is a genuine improvement over the Azure path rather than a copy of it:
// AzureKeyVaultKeyWrapper must ignore its aad argument because Key Vault's RSA
// wrap cannot authenticate additional data, so cross-record binding there rests
// entirely on the inner AEAD layer. KMS authenticates the encryption context,
// so a stolen wrapped DEK cannot be unwrapped against a different record even
// at the KMS layer.
func (w *AwsKMSKeyWrapper) WrapKey(ctx context.Context, plaintext, aad []byte) (WrappedKey, error) {
	request := map[string]any{
		"KeyId":     w.keyARN,
		"Plaintext": base64.StdEncoding.EncodeToString(plaintext),
	}
	if len(aad) > 0 {
		request["EncryptionContext"] = kmsEncryptionContext(aad)
	}
	response, err := w.call(ctx, "Encrypt", request)
	if err != nil {
		return WrappedKey{}, err
	}
	ciphertext := strings.TrimSpace(response.CiphertextBlob)
	if ciphertext == "" {
		return WrappedKey{}, errors.New("AWS KMS Encrypt returned an empty ciphertext")
	}
	return WrappedKey{Ciphertext: ciphertext, Metadata: map[string]any{
		"provider": "aws_kms", "algorithm": "SYMMETRIC_DEFAULT", "key_id": w.keyARN,
		// Records whether a context was ACTUALLY applied, so UnwrapKey can
		// refuse an unbound ciphertext. Claiming true unconditionally would
		// make the metadata lie whenever a caller passed no AAD, and the
		// fail-closed check in UnwrapKey would then wave that ciphertext
		// through to KMS instead of rejecting it with an accurate message.
		"encryption_context": len(aad) > 0,
	}}, nil
}

func (w *AwsKMSKeyWrapper) UnwrapKey(ctx context.Context, wrapped WrappedKey, aad []byte) ([]byte, error) {
	if keyID, _ := wrapped.Metadata["key_id"].(string); strings.TrimSpace(keyID) != "" && keyID != w.keyARN {
		return nil, errors.New("wrapped key belongs to a different KMS key")
	}
	// Fail closed on a ciphertext that carries no context binding. This wrapper
	// is new, so no legacy ciphertext exists to accommodate, and refusing is
	// cheaper than carrying a silent downgrade path forever.
	if len(aad) > 0 {
		if bound, _ := wrapped.Metadata["encryption_context"].(bool); !bound {
			return nil, errors.New("wrapped key was not sealed with a KMS encryption context")
		}
	}
	request := map[string]any{
		"KeyId":          w.keyARN,
		"CiphertextBlob": wrapped.Ciphertext,
	}
	if len(aad) > 0 {
		request["EncryptionContext"] = kmsEncryptionContext(aad)
	}
	response, err := w.call(ctx, "Decrypt", request)
	if err != nil {
		return nil, err
	}
	// KMS echoes the key that actually decrypted the blob; verifying it closes
	// the case where a caller was pointed at a different key than the ARN says.
	if returned := strings.TrimSpace(response.KeyID); returned != "" && returned != w.keyARN {
		return nil, errors.New("AWS KMS decrypted the key with a different KMS key")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(response.Plaintext))
	if err != nil {
		return nil, fmt.Errorf("decode AWS KMS plaintext: %w", err)
	}
	return decoded, nil
}

// kmsEncryptionContext base64-encodes the AAD unconditionally. Encryption
// context values must be printable UTF-8; the cluster-connection AAD happens to
// be ASCII, but artifact and project-secret AADs need not be.
func kmsEncryptionContext(aad []byte) map[string]string {
	return map[string]string{kmsEncryptionContextKey: base64.RawURLEncoding.EncodeToString(aad)}
}

type kmsResponse struct {
	CiphertextBlob string `json:"CiphertextBlob"`
	Plaintext      string `json:"Plaintext"`
	KeyID          string `json:"KeyId"`
}

func (w *AwsKMSKeyWrapper) call(ctx context.Context, operation string, payload map[string]any) (kmsResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return kmsResponse{}, err
	}
	defer clear(body)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint, bytes.NewReader(body))
	if err != nil {
		return kmsResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-amz-json-1.1")
	request.Header.Set("X-Amz-Target", "TrentService."+operation)

	credentials, err := w.credentials.Retrieve(ctx)
	if err != nil {
		return kmsResponse{}, fmt.Errorf("retrieve AWS credentials for KMS %s: %w", operation, err)
	}
	digest := sha256.Sum256(body)
	if err := w.signer.SignHTTP(ctx, credentials, request, hex.EncodeToString(digest[:]),
		kmsSigningService, w.region, time.Now().UTC()); err != nil {
		return kmsResponse{}, fmt.Errorf("sign AWS KMS %s: %w", operation, err)
	}

	response, err := w.client.Do(request)
	if err != nil {
		return kmsResponse{}, fmt.Errorf("AWS KMS %s: %w", operation, err)
	}
	defer response.Body.Close()
	payloadBytes, err := io.ReadAll(io.LimitReader(response.Body, kmsMaxResponseBytes))
	if err != nil {
		return kmsResponse{}, err
	}
	defer clear(payloadBytes)

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// KMS and compatible gateways may echo request fields in an error
		// document. For Encrypt that request contains the plaintext data
		// encryption key, so the response body must never be promoted into an
		// application error, log, audit event, or API response. The
		// X-Amzn-ErrorType header is a safe, stable error code.
		return kmsResponse{}, fmt.Errorf("AWS KMS %s returned %d (%s)", operation,
			response.StatusCode, strings.TrimSpace(response.Header.Get("X-Amzn-ErrorType")))
	}
	var decoded kmsResponse
	if err := json.Unmarshal(payloadBytes, &decoded); err != nil {
		return kmsResponse{}, fmt.Errorf("decode AWS KMS %s response: %w", operation, err)
	}
	return decoded, nil
}
