package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

func testKeyWrapper(t *testing.T) keywrap.KeyWrapper {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	wrapper, err := keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{Key: key, KeyID: "test"})
	if err != nil {
		t.Fatalf("NewLocalAESKeyWrapper: %v", err)
	}
	return wrapper
}

func TestClusterCredentialSealAndOpenRoundTrip(t *testing.T) {
	wrapper := testKeyWrapper(t)
	ctx := context.Background()
	const secret = "azure-client-secret-value"

	ciphertext, wrapped, err := sealClusterCredential(ctx, wrapper, "cluster-1", clusterCredentialAADClientSecret, secret)
	if err != nil {
		t.Fatalf("sealClusterCredential: %v", err)
	}
	if strings.Contains(ciphertext, secret) || strings.Contains(wrapped, secret) {
		t.Fatal("the sealed envelope contains the plaintext secret")
	}

	opened, err := openClusterCredential(ctx, wrapper, "cluster-1", clusterCredentialAADClientSecret, ciphertext, wrapped)
	if err != nil {
		t.Fatalf("openClusterCredential: %v", err)
	}
	if opened != secret {
		t.Fatalf("opened = %q, want the original secret", opened)
	}
}

// TestClusterCredentialIsBoundToItsConnectionAndSlot pins the AAD binding: an
// envelope copied to another connection row, or read as the wrong credential
// kind, must fail authentication rather than decrypt.
func TestClusterCredentialIsBoundToItsConnectionAndSlot(t *testing.T) {
	wrapper := testKeyWrapper(t)
	ctx := context.Background()
	ciphertext, wrapped, err := sealClusterCredential(ctx, wrapper, "cluster-1", clusterCredentialAADClientSecret, "secret")
	if err != nil {
		t.Fatalf("sealClusterCredential: %v", err)
	}

	if _, err := openClusterCredential(ctx, wrapper, "cluster-2", clusterCredentialAADClientSecret, ciphertext, wrapped); err == nil {
		t.Fatal("an envelope copied to another cluster connection decrypted successfully")
	}
	if _, err := openClusterCredential(ctx, wrapper, "cluster-1", clusterCredentialStaticToken, ciphertext, wrapped); err == nil {
		t.Fatal("an AAD client secret decrypted as a static token")
	}
}

// TestClusterCredentialFailsClosedWithoutAKeyWrapper pins the 503 path: storing
// an administrator's cluster credential in plaintext is never a fallback.
func TestClusterCredentialFailsClosedWithoutAKeyWrapper(t *testing.T) {
	_, _, err := sealClusterCredential(context.Background(), nil, "cluster-1", clusterCredentialAADClientSecret, "secret")
	if !errors.Is(err, errClusterCredentialEncryptionUnavailable) {
		t.Fatalf("seal error = %v, want errClusterCredentialEncryptionUnavailable", err)
	}
	_, err = openClusterCredential(context.Background(), nil, "cluster-1", clusterCredentialAADClientSecret, "x", "y")
	if !errors.Is(err, errClusterCredentialEncryptionUnavailable) {
		t.Fatalf("open error = %v, want errClusterCredentialEncryptionUnavailable", err)
	}
}

func TestSealClusterCredentialRejectsEmptyAndOversizedValues(t *testing.T) {
	wrapper := testKeyWrapper(t)
	ctx := context.Background()

	if _, _, err := sealClusterCredential(ctx, wrapper, "cluster-1", clusterCredentialStaticToken, ""); err == nil {
		t.Fatal("an empty credential was sealed")
	}
	oversized := strings.Repeat("a", maxClusterCredentialBytes+1)
	if _, _, err := sealClusterCredential(ctx, wrapper, "cluster-1", clusterCredentialStaticToken, oversized); err == nil {
		t.Fatal("an oversized credential was sealed")
	}
}

// TestCredentialReferenceIsPresenceAware pins that the env-var reference
// follows the same omit-means-keep rule as the credential itself. Treating it
// like an ordinary replace-on-omit field would let a client that never sends it
// silently detach a connection's credential.
func TestCredentialReferenceIsPresenceAware(t *testing.T) {
	stored := &storage.ClusterConnection{
		ID: "cluster-1", Name: "eastus-perf", Kind: storage.ClusterKindAKS,
		Namespace: "apex", ClientSecretEnvVar: "AKS_SECRET",
	}

	// Omitted: the reference survives.
	applyClusterConnectionRequest(stored, ClusterConnectionRequest{Name: "eastus-perf", Namespace: "apex", Kind: storage.ClusterKindAKS})
	if stored.ClientSecretEnvVar != "AKS_SECRET" {
		t.Fatalf("ClientSecretEnvVar = %q after an omitted reference, want it preserved", stored.ClientSecretEnvVar)
	}

	// Explicit empty string: the reference is cleared.
	blank := ""
	applyClusterConnectionRequest(stored, ClusterConnectionRequest{
		Name: "eastus-perf", Namespace: "apex", Kind: storage.ClusterKindAKS, ClientSecretEnvVar: &blank,
	})
	if stored.ClientSecretEnvVar != "" {
		t.Fatalf("ClientSecretEnvVar = %q after an explicit empty reference, want it cleared", stored.ClientSecretEnvVar)
	}

	// Explicit value: the reference is replaced.
	next := "OTHER_SECRET"
	applyClusterConnectionRequest(stored, ClusterConnectionRequest{
		Name: "eastus-perf", Namespace: "apex", Kind: storage.ClusterKindAKS, ClientSecretEnvVar: &next,
	})
	if stored.ClientSecretEnvVar != "OTHER_SECRET" {
		t.Fatalf("ClientSecretEnvVar = %q, want the submitted reference", stored.ClientSecretEnvVar)
	}
}

func TestSealedCredentialsDifferForIdenticalPlaintext(t *testing.T) {
	wrapper := testKeyWrapper(t)
	ctx := context.Background()
	first, _, err := sealClusterCredential(ctx, wrapper, "cluster-1", clusterCredentialStaticToken, "same")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	second, _, err := sealClusterCredential(ctx, wrapper, "cluster-1", clusterCredentialStaticToken, "same")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// A fresh data key and nonce per record means identical plaintext must not
	// produce identical ciphertext.
	if first == second {
		t.Fatal("identical plaintext produced identical ciphertext; the per-record data key is not being used")
	}
}
