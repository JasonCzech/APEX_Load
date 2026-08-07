package keywrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

const testKMSKeyARN = "arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab"

func testKMSCredentials() aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "")
}

// newKMSWrapper points a wrapper at a local stub standing in for the KMS
// endpoint, so the SigV4 signing, redirect policy, and error handling are all
// exercised for real.
func newKMSWrapper(t *testing.T, handler http.HandlerFunc) (*AwsKMSKeyWrapper, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	wrapper, err := NewAwsKMSKeyWrapper(testKMSKeyARN, testKMSCredentials(), server.Client())
	if err != nil {
		t.Fatalf("NewAwsKMSKeyWrapper: %v", err)
	}
	wrapper.endpoint = server.URL + "/"
	return wrapper, server
}

func decodeKMSRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return decoded
}

func TestAwsKMSKeyWrapperRequiresPinnedKeyARN(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keyID   string
		wantErr bool
	}{
		{name: "full key ARN", keyID: testKMSKeyARN},
		{name: "govcloud partition", keyID: "arn:aws-us-gov:kms:us-gov-west-1:123456789012:key/abc-123"},
		{name: "alias ARN can be repointed", keyID: "arn:aws:kms:us-east-1:123456789012:alias/apexload", wantErr: true},
		{name: "bare key id", keyID: "1234abcd-12ab-34cd-56ef-1234567890ab", wantErr: true},
		{name: "alias name", keyID: "alias/apexload", wantErr: true},
		{name: "extra path segments", keyID: testKMSKeyARN + "/extra", wantErr: true},
		{name: "empty", keyID: "", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAwsKMSKeyWrapper(tc.keyID, testKMSCredentials(), nil)
			if tc.wantErr != (err != nil) {
				t.Fatalf("NewAwsKMSKeyWrapper(%q) error = %v, wantErr %v", tc.keyID, err, tc.wantErr)
			}
		})
	}
}

// TestAwsKMSKeyWrapperBindsEncryptionContext is the property the Azure wrapper
// structurally cannot provide: the AAD is authenticated by KMS itself, so a
// stolen envelope cannot be unwrapped against a different record even at the
// KMS layer.
func TestAwsKMSKeyWrapperBindsEncryptionContext(t *testing.T) {
	const aad = "apexload:cluster-connection:cluster-1:aad-client-secret"
	wantContext := base64.RawURLEncoding.EncodeToString([]byte(aad))

	var sawEncryptContext, sawDecryptContext string
	wrapper, _ := newKMSWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		request := decodeKMSRequest(t, r)
		context, _ := request["EncryptionContext"].(map[string]any)
		value, _ := context[kmsEncryptionContextKey].(string)

		switch r.Header.Get("X-Amz-Target") {
		case "TrentService.Encrypt":
			sawEncryptContext = value
			_ = json.NewEncoder(w).Encode(map[string]string{
				"CiphertextBlob": base64.StdEncoding.EncodeToString([]byte("wrapped")),
				"KeyId":          testKMSKeyARN,
			})
		case "TrentService.Decrypt":
			sawDecryptContext = value
			if value != wantContext {
				// This is what real KMS does on a context mismatch.
				w.Header().Set("X-Amzn-ErrorType", "InvalidCiphertextException")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Plaintext": base64.StdEncoding.EncodeToString([]byte("the-data-key")),
				"KeyId":     testKMSKeyARN,
			})
		}
	})

	ctx := context.Background()
	wrapped, err := wrapper.WrapKey(ctx, []byte("the-data-key"), []byte(aad))
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	if sawEncryptContext != wantContext {
		t.Fatalf("Encrypt encryption context = %q, want %q", sawEncryptContext, wantContext)
	}
	if bound, _ := wrapped.Metadata["encryption_context"].(bool); !bound {
		t.Error("WrapKey did not record that the ciphertext is context-bound")
	}

	plaintext, err := wrapper.UnwrapKey(ctx, wrapped, []byte(aad))
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}
	if string(plaintext) != "the-data-key" {
		t.Fatalf("UnwrapKey = %q, want the original data key", plaintext)
	}
	if sawDecryptContext != wantContext {
		t.Fatalf("Decrypt encryption context = %q, want %q", sawDecryptContext, wantContext)
	}

	// A different AAD must be refused by KMS, not silently accepted.
	if _, err := wrapper.UnwrapKey(ctx, wrapped, []byte("apexload:cluster-connection:cluster-2:aad-client-secret")); err == nil {
		t.Fatal("UnwrapKey accepted a ciphertext bound to a different record")
	}
}

// TestAwsKMSUnwrapRefusesAnUnboundCiphertext pins the fail-closed stance: a
// wrapped key with no recorded context binding must not be silently unwrapped
// as though it had one.
func TestAwsKMSUnwrapRefusesAnUnboundCiphertext(t *testing.T) {
	wrapper, _ := newKMSWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("UnwrapKey reached KMS despite a missing context binding")
	})
	_, err := wrapper.UnwrapKey(context.Background(), WrappedKey{
		Ciphertext: "d3JhcHBlZA==",
		Metadata:   map[string]any{"provider": "aws_kms", "key_id": testKMSKeyARN},
	}, []byte("some-aad"))
	if err == nil {
		t.Fatal("UnwrapKey = nil error for an unbound ciphertext")
	}
}

func TestAwsKMSUnwrapRefusesAnotherKeysCiphertext(t *testing.T) {
	wrapper, _ := newKMSWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("UnwrapKey reached KMS despite a key mismatch")
	})
	_, err := wrapper.UnwrapKey(context.Background(), WrappedKey{
		Ciphertext: "d3JhcHBlZA==",
		Metadata: map[string]any{
			"key_id":             "arn:aws:kms:us-east-1:123456789012:key/ffffffff-ffff-ffff-ffff-ffffffffffff",
			"encryption_context": true,
		},
	}, []byte("some-aad"))
	if err == nil || !strings.Contains(err.Error(), "different KMS key") {
		t.Fatalf("UnwrapKey error = %v, want a key-mismatch refusal", err)
	}
}

// TestAwsKMSKeyWrapperNeverSurfacesResponseBody guards the leak channel: a KMS
// error document may echo the request, and the Encrypt request contains the
// plaintext data-encryption key.
func TestAwsKMSKeyWrapperNeverSurfacesResponseBody(t *testing.T) {
	const dataKey = "SUPER-SECRET-DATA-KEY"
	wrapper, _ := newKMSWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amzn-ErrorType", "ValidationException")
		w.WriteHeader(http.StatusBadRequest)
		// Real gateways have been observed echoing the request document.
		_, _ = w.Write([]byte(`{"message":"bad request","echo":{"Plaintext":"` +
			base64.StdEncoding.EncodeToString([]byte(dataKey)) + `"}}`))
	})

	_, err := wrapper.WrapKey(context.Background(), []byte(dataKey), []byte("aad"))
	if err == nil {
		t.Fatal("WrapKey = nil error for a 400 response")
	}
	message := err.Error()
	if strings.Contains(message, dataKey) || strings.Contains(message, base64.StdEncoding.EncodeToString([]byte(dataKey))) {
		t.Fatalf("WrapKey error leaked the data-encryption key: %s", message)
	}
	if strings.Contains(message, "echo") || strings.Contains(message, "bad request") {
		t.Fatalf("WrapKey error promoted the response body: %s", message)
	}
	// The safe error code is still reported so an operator can diagnose.
	if !strings.Contains(message, "ValidationException") {
		t.Errorf("WrapKey error = %q, want the X-Amzn-ErrorType code", message)
	}
}

// TestAwsKMSKeyWrapperRejectsRedirects pins that a 307 cannot replay the
// plaintext-carrying POST body to another host.
func TestAwsKMSKeyWrapperRejectsRedirects(t *testing.T) {
	var replayed bool
	replay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replayed = true
		_ = json.NewEncoder(w).Encode(map[string]string{"CiphertextBlob": "d3JhcHBlZA=="})
	}))
	defer replay.Close()

	wrapper, _ := newKMSWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, replay.URL, http.StatusTemporaryRedirect)
	})

	if _, err := wrapper.WrapKey(context.Background(), []byte("data-key"), []byte("aad")); err == nil {
		t.Fatal("WrapKey followed a redirect instead of failing")
	}
	if replayed {
		t.Fatal("the plaintext data-encryption key was replayed to the redirect target")
	}
}

func TestAwsKMSRequestIsSigV4Signed(t *testing.T) {
	var authorization, target, contentType string
	wrapper, _ := newKMSWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		target = r.Header.Get("X-Amz-Target")
		contentType = r.Header.Get("Content-Type")
		_ = json.NewEncoder(w).Encode(map[string]string{"CiphertextBlob": "d3JhcHBlZA==", "KeyId": testKMSKeyARN})
	})
	if _, err := wrapper.WrapKey(context.Background(), []byte("data-key"), nil); err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("Authorization = %q, want a SigV4 signature", authorization)
	}
	if !strings.Contains(authorization, "/us-east-1/kms/aws4_request") {
		t.Errorf("Authorization = %q, want the kms scope in the ARN's region", authorization)
	}
	if target != "TrentService.Encrypt" {
		t.Errorf("X-Amz-Target = %q, want TrentService.Encrypt", target)
	}
	if contentType != "application/x-amz-json-1.1" {
		t.Errorf("Content-Type = %q, want application/x-amz-json-1.1", contentType)
	}
}

// TestNewConfiguredKeyWrapperRefusesTwoWrappingProviders pins that an ambiguous
// deployment fails loudly. Picking one silently would render every envelope
// sealed by the other permanently unreadable.
func TestNewConfiguredKeyWrapperRefusesTwoWrappingProviders(t *testing.T) {
	t.Run("both configured", func(t *testing.T) {
		t.Setenv(KeyVaultKeyIDEnvVar, "https://vault.example/keys/apexload/1")
		t.Setenv(KMSKeyIDEnvVar, testKMSKeyARN)
		_, err := NewConfiguredKeyWrapper(false)
		if err == nil {
			t.Fatal("NewConfiguredKeyWrapper = nil error with two wrapping keys configured")
		}
		for _, name := range []string{KeyVaultKeyIDEnvVar, KMSKeyIDEnvVar} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error = %q, want it to name %s", err, name)
			}
		}
		if provider, conflict := ConfiguredWrappingProvider(); !conflict || provider != "conflict" {
			t.Errorf("ConfiguredWrappingProvider() = (%q, %v), want a reported conflict", provider, conflict)
		}
	})

	t.Run("only KMS", func(t *testing.T) {
		t.Setenv(KeyVaultKeyIDEnvVar, "")
		t.Setenv(KMSKeyIDEnvVar, testKMSKeyARN)
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAIOSFODNN7EXAMPLE")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
		wrapper, err := NewConfiguredKeyWrapper(false)
		if err != nil {
			t.Fatalf("NewConfiguredKeyWrapper: %v", err)
		}
		if _, ok := wrapper.(*AwsKMSKeyWrapper); !ok {
			t.Fatalf("wrapper = %T, want *AwsKMSKeyWrapper", wrapper)
		}
		if provider, conflict := ConfiguredWrappingProvider(); conflict || provider != "aws_kms" {
			t.Errorf("ConfiguredWrappingProvider() = (%q, %v), want (aws_kms, false)", provider, conflict)
		}
	})

	t.Run("neither falls back to local", func(t *testing.T) {
		t.Setenv(KeyVaultKeyIDEnvVar, "")
		t.Setenv(KMSKeyIDEnvVar, "")
		t.Setenv(MasterKeyEnvVar, base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
		wrapper, err := NewConfiguredKeyWrapper(false)
		if err != nil {
			t.Fatalf("NewConfiguredKeyWrapper: %v", err)
		}
		if _, ok := wrapper.(*LocalAESKeyWrapper); !ok {
			t.Fatalf("wrapper = %T, want *LocalAESKeyWrapper", wrapper)
		}
		if provider, conflict := ConfiguredWrappingProvider(); conflict || provider != "local" {
			t.Errorf("ConfiguredWrappingProvider() = (%q, %v), want (local, false)", provider, conflict)
		}
	})
}

// TestAwsKMSMetadataReportsWhetherAContextWasApplied pins that the binding flag
// is truthful. Claiming it unconditionally would make UnwrapKey's fail-closed
// check wave an unbound ciphertext through to KMS with a confusing error
// instead of rejecting it with an accurate one.
func TestAwsKMSMetadataReportsWhetherAContextWasApplied(t *testing.T) {
	var sawContext bool
	wrapper, _ := newKMSWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		request := decodeKMSRequest(t, r)
		_, sawContext = request["EncryptionContext"]
		_ = json.NewEncoder(w).Encode(map[string]string{
			"CiphertextBlob": base64.StdEncoding.EncodeToString([]byte("wrapped")),
			"KeyId":          testKMSKeyARN,
		})
	})
	ctx := context.Background()

	withAAD, err := wrapper.WrapKey(ctx, []byte("dek"), []byte("apexload:cluster-connection:c1:aad-client-secret"))
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	if !sawContext {
		t.Fatal("an AAD was supplied but no EncryptionContext reached KMS")
	}
	if bound, _ := withAAD.Metadata["encryption_context"].(bool); !bound {
		t.Error("metadata says unbound for a ciphertext that WAS context-bound")
	}

	withoutAAD, err := wrapper.WrapKey(ctx, []byte("dek"), nil)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	if sawContext {
		t.Fatal("no AAD was supplied but an EncryptionContext reached KMS")
	}
	if bound, _ := withoutAAD.Metadata["encryption_context"].(bool); bound {
		t.Error("metadata claims context binding for a ciphertext sealed without one")
	}

	// And that unbound ciphertext must be refused when a caller later presents
	// an AAD, rather than being sent to KMS to fail obscurely.
	if _, err := wrapper.UnwrapKey(ctx, withoutAAD, []byte("some-aad")); err == nil {
		t.Fatal("UnwrapKey accepted an unbound ciphertext against a non-empty AAD")
	}
}
