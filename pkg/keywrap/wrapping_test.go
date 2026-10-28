package keywrap

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type wrappingTestCredential struct{}

func (wrappingTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestLocalAESKeyWrapperRoundTripAndAADBinding(t *testing.T) {
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: bytes.Repeat([]byte{0x42}, 32), KeyID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("data-encryption-key")
	aad := []byte("project-a/artifact")
	wrapped, err := wrapper.WrapKey(context.Background(), plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	unwrapped, err := wrapper.UnwrapKey(context.Background(), wrapped, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unwrapped, plaintext) {
		t.Fatalf("unwrapped key = %x, want %x", unwrapped, plaintext)
	}
	if _, err := wrapper.UnwrapKey(context.Background(), wrapped, []byte("project-b/artifact")); err == nil {
		t.Fatal("unwrap succeeded with different authenticated context")
	}
}

func TestDecodeConfiguredKeyRejectsShortValues(t *testing.T) {
	if _, err := DecodeConfiguredKey("too-short"); err == nil {
		t.Fatal("short wrapping key was accepted")
	}
}

func TestAzureKeyVaultWrapperRequiresPinnedVersionedKeyID(t *testing.T) {
	for _, keyID := range []string{
		"https://vault.example/keys/name",
		"https://vault.example/keys/name/version/extra",
		"https://vault.example/prefix/keys/name/version",
		"https://vault.example/keys//version",
		"https://vault.example/keys/name/%2F",
		"https://vault.example/keys/name/%2e%2e",
	} {
		t.Run(keyID, func(t *testing.T) {
			if _, err := NewAzureKeyVaultKeyWrapper(keyID, wrappingTestCredential{}, nil); err == nil {
				t.Fatalf("NewAzureKeyVaultKeyWrapper(%q) accepted an unpinned or malformed key id", keyID)
			}
		})
	}
	if _, err := NewAzureKeyVaultKeyWrapper(
		"https://vault.example/keys/name/version",
		wrappingTestCredential{},
		nil,
	); err != nil {
		t.Fatalf("valid pinned key id was rejected: %v", err)
	}
}

func TestAzureKeyVaultWrapperDoesNotExposeProviderErrorBody(t *testing.T) {
	const echoedMaterial = "provider-echoed-data-encryption-key"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, echoedMaterial), http.StatusBadRequest)
	}))
	defer server.Close()

	wrapper, err := NewAzureKeyVaultKeyWrapper(
		server.URL+"/keys/wrapping-key/version",
		wrappingTestCredential{},
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrapper.WrapKey(context.Background(), []byte("plaintext-data-encryption-key"), nil)
	if err == nil {
		t.Fatal("WrapKey succeeded against a failing provider")
	}
	if strings.Contains(err.Error(), echoedMaterial) || strings.Contains(err.Error(), "plaintext-data-encryption-key") {
		t.Fatalf("provider error exposed key material: %v", err)
	}
	if got, want := err.Error(), "Azure Key Vault wrapkey returned 400"; got != want {
		t.Fatalf("WrapKey error = %q, want %q", got, want)
	}
}

func TestAzureKeyVaultWrapperDoesNotReplayPlaintextKeyAcrossRedirect(t *testing.T) {
	destinationRequests := make(chan struct{}, 1)
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case destinationRequests <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()

	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL+"/capture")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	followingClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // test-only TLS servers
	}
	wrapper, err := NewAzureKeyVaultKeyWrapper(
		source.URL+"/keys/wrapping-key/version",
		wrappingTestCredential{},
		followingClient,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapper.WrapKey(context.Background(), []byte("plaintext-data-encryption-key"), nil); err == nil {
		t.Fatal("WrapKey accepted a redirect response")
	}
	select {
	case <-destinationRequests:
		t.Fatal("WrapKey replayed its plaintext key body to the redirect destination")
	default:
	}
}

func TestKeyRingWrapperAndRotateWrappedKeyPreserveKeyMaterial(t *testing.T) {
	ctx := context.Background()
	oldWrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "master-v1"})
	if err != nil {
		t.Fatal(err)
	}
	newWrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: []byte("fedcba9876543210fedcba9876543210"), KeyID: "master-v2"})
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("apexload:authoring-receipt-credential-key:v1")
	dek := []byte("credential-hmac-secret-material!")

	wrappedV1, err := oldWrapper.WrapKey(ctx, dek, aad)
	if err != nil {
		t.Fatal(err)
	}

	ring, err := NewKeyRingWrapper(newWrapper, oldWrapper)
	if err != nil {
		t.Fatal(err)
	}
	unwrappedFromRing, err := ring.UnwrapKey(ctx, wrappedV1, aad)
	if err != nil {
		t.Fatalf("UnwrapKey v1 envelope via ring: %v", err)
	}
	if string(unwrappedFromRing) != string(dek) {
		t.Fatalf("unwrapped key = %q, want %q", unwrappedFromRing, dek)
	}

	rotated, err := RotateWrappedKey(ctx, oldWrapper, newWrapper, wrappedV1, aad)
	if err != nil {
		t.Fatalf("RotateWrappedKey: %v", err)
	}
	if _, err := oldWrapper.UnwrapKey(ctx, rotated, aad); err == nil {
		t.Fatal("old wrapper unwrapped rotated key envelope")
	}
	unwrappedV2, err := newWrapper.UnwrapKey(ctx, rotated, aad)
	if err != nil {
		t.Fatalf("newWrapper.UnwrapKey rotated envelope: %v", err)
	}
	if string(unwrappedV2) != string(dek) {
		t.Fatalf("rotated unwrapped key = %q, want %q", unwrappedV2, dek)
	}

	// Verify canceled context aborts KeyRingWrapper.UnwrapKey immediately.
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ring.UnwrapKey(canceledCtx, wrappedV1, aad); !errors.Is(err, context.Canceled) {
		t.Fatalf("ring.UnwrapKey with canceled context = %v, want context.Canceled", err)
	}

	// Verify joined error diagnostics when all wrappers fail.
	errActive := errors.New("active-wrapper-failure")
	errPrev := errors.New("previous-wrapper-failure")
	failingRing, err := NewKeyRingWrapper(
		failingTestKeyWrapper{err: errActive},
		failingTestKeyWrapper{err: errPrev},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, unwrapErr := failingRing.UnwrapKey(ctx, wrappedV1, aad)
	if !errors.Is(unwrapErr, errActive) || !errors.Is(unwrapErr, errPrev) {
		t.Fatalf("failingRing.UnwrapKey error = %v, want joined errors containing %v and %v", unwrapErr, errActive, errPrev)
	}
}

type failingTestKeyWrapper struct {
	err error
}

func (f failingTestKeyWrapper) WrapKey(context.Context, []byte, []byte) (WrappedKey, error) {
	return WrappedKey{}, f.err
}

func (f failingTestKeyWrapper) UnwrapKey(context.Context, WrappedKey, []byte) ([]byte, error) {
	return nil, f.err
}
