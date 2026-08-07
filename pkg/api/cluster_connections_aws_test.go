package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

func awsClusterConnection() *storage.ClusterConnection {
	return &storage.ClusterConnection{
		ID:                   "cluster-eks",
		Name:                 "us-east-1-perf",
		Kind:                 storage.ClusterKindEKS,
		APIServerURL:         "https://ABCDEF0123456789.gr7.us-east-1.eks.amazonaws.com",
		Namespace:            "apex",
		GeneratorStatefulSet: "apexload-generator",
		TLSMode:              storage.ClusterTLSSystemRoots,
		AuthMode:             storage.ClusterAuthAWSIAM,
		AWSRegion:            "us-east-1",
		AWSClusterName:       "prod-perf",
		AWSCredentialMode:    storage.AWSCredentialModeStaticKeys,
		AWSAccessKeyID:       "AKIAIOSFODNN7EXAMPLE",
	}
}

// TestClusterCredentialAADCoversTheAWSSlots extends the slot-binding property
// to the two new credential kinds: an AWS secret access key must not be
// unsealable as a role external ID (or as an AAD client secret), so a stolen
// envelope cannot be replayed into a different credential position.
func TestClusterCredentialAADCoversTheAWSSlots(t *testing.T) {
	wrapper := testKeyWrapper(t)
	ctx := context.Background()

	ciphertext, wrapped, err := sealClusterCredential(ctx, wrapper, "cluster-eks", clusterCredentialAWSSecretKey, "secret-key")
	if err != nil {
		t.Fatalf("sealClusterCredential: %v", err)
	}
	for _, wrongSlot := range []clusterCredentialKind{
		clusterCredentialAWSExternalID,
		clusterCredentialAADClientSecret,
		clusterCredentialStaticToken,
	} {
		if _, err := openClusterCredential(ctx, wrapper, "cluster-eks", wrongSlot, ciphertext, wrapped); err == nil {
			t.Errorf("an AWS secret access key decrypted as %q", wrongSlot)
		}
	}
	if _, err := openClusterCredential(ctx, wrapper, "cluster-other", clusterCredentialAWSSecretKey, ciphertext, wrapped); err == nil {
		t.Error("an AWS envelope copied to another cluster connection decrypted successfully")
	}

	// And the correct slot still round-trips.
	value, err := openClusterCredential(ctx, wrapper, "cluster-eks", clusterCredentialAWSSecretKey, ciphertext, wrapped)
	if err != nil || value != "secret-key" {
		t.Fatalf("openClusterCredential = (%q, %v), want the original value", value, err)
	}
}

// TestResolveCredentialDoesNotApplyEnvVarToTheExternalIDSlot pins the fix for a
// real trap: a connection has ONE env-var reference but may hold SEVERAL
// credentials. Resolving every slot from that one variable would silently hand
// the access key's secret back as the role external ID.
func TestResolveCredentialDoesNotApplyEnvVarToTheExternalIDSlot(t *testing.T) {
	const envVar = "APEXLOAD_TEST_EKS_SECRET"
	t.Setenv(envVar, "the-secret-access-key")

	wrapper := testKeyWrapper(t)
	cache := newClusterClientCache(wrapper, nil)
	ctx := context.Background()

	conn := awsClusterConnection()
	conn.ClientSecretEnvVar = envVar

	externalCipher, externalWrapped, err := sealClusterCredential(ctx, wrapper, conn.ID, clusterCredentialAWSExternalID, "the-external-id")
	if err != nil {
		t.Fatalf("sealClusterCredential: %v", err)
	}

	// The secret-key slot legitimately reads the env var.
	secret, err := cache.resolveCredential(ctx, conn, clusterCredentialAWSSecretKey, conn.ClientSecretEnvVar, "", "")
	if err != nil {
		t.Fatalf("resolveCredential(secret key): %v", err)
	}
	if secret != "the-secret-access-key" {
		t.Fatalf("secret key = %q, want the env var value", secret)
	}

	// The external-ID slot must NOT: it passes no env var name, so it unseals
	// its own envelope.
	externalID, err := cache.resolveCredential(ctx, conn, clusterCredentialAWSExternalID, "", externalCipher, externalWrapped)
	if err != nil {
		t.Fatalf("resolveCredential(external id): %v", err)
	}
	if externalID == "the-secret-access-key" {
		t.Fatal("the client-secret env var leaked into the external-ID slot")
	}
	if externalID != "the-external-id" {
		t.Fatalf("external id = %q, want the sealed value", externalID)
	}
}

// TestAWSTokenSourceForAmbientReadsNoStoredCredential proves an IRSA connection
// never touches the envelope path, so it works on a controller with no key
// wrapper configured at all.
func TestAWSTokenSourceForAmbientReadsNoStoredCredential(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAAMBIENTAMBIENT00")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret")
	t.Setenv("AWS_REGION", "us-east-1")

	// Deliberately no key wrapper: sealing/opening would fail closed.
	cache := newClusterClientCache(nil, nil)
	conn := awsClusterConnection()
	conn.AWSCredentialMode = storage.AWSCredentialModeAmbient
	conn.AWSAccessKeyID = ""

	source, err := cache.awsTokenSourceFor(context.Background(), conn)
	if err != nil {
		t.Fatalf("awsTokenSourceFor: %v", err)
	}
	if _, ok := source.(*kubeops.AwsIAMTokenSource); !ok {
		t.Fatalf("token source = %T, want *kubeops.AwsIAMTokenSource", source)
	}
	if describe := source.Describe(); !strings.Contains(describe, "workload identity") {
		t.Errorf("Describe() = %q, want it to name workload identity", describe)
	}
}

func TestAWSSessionNameIsStableAndValid(t *testing.T) {
	first := awsSessionNameFor("cluster-eks-1")
	if first != awsSessionNameFor("cluster-eks-1") {
		t.Fatal("awsSessionNameFor is not deterministic; the assumed-role ARN would change every call")
	}
	if first == awsSessionNameFor("cluster-eks-2") {
		t.Fatal("two connections produced the same role session name")
	}
	// IAM's charset, and long/exotic IDs must still be usable.
	for _, id := range []string{"", "a", strings.Repeat("x", 80), "id/with:bad*chars"} {
		name := awsSessionNameFor(id)
		if len(name) < 2 || len(name) > 64 {
			t.Errorf("awsSessionNameFor(%q) = %q, want 2-64 characters", id, name)
		}
		if strings.ContainsAny(name, "/:*") {
			t.Errorf("awsSessionNameFor(%q) = %q, which IAM would reject", id, name)
		}
	}
}

// TestClusterConnectionResponseOmitsAWSSecrets guards the read boundary at the
// handler layer, not just the storage struct.
func TestClusterConnectionResponseOmitsAWSSecrets(t *testing.T) {
	conn := awsClusterConnection()
	conn.AWSSecretKeyCiphertext = "aws-secret-cipher"
	conn.AWSSecretKeyWrappedKey = "aws-secret-wrapped"
	conn.AWSExternalIDCiphertext = "aws-external-cipher"
	conn.AWSExternalIDWrappedKey = "aws-external-wrapped"

	response := clusterConnectionResponse(conn)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, secret := range []string{
		"aws-secret-cipher", "aws-secret-wrapped", "aws-external-cipher", "aws-external-wrapped",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("cluster connection response leaked %q: %s", secret, encoded)
		}
	}
	if !response.HasAWSExternalID {
		t.Error("HasAWSExternalID = false, want the UI to know a value is stored")
	}
	if !response.HasClientSecret {
		t.Error("HasClientSecret = false, want a stored AWS secret key to count as a stored credential")
	}
	if !strings.Contains(response.AuthDescription, "prod-perf") {
		t.Errorf("AuthDescription = %q, want it to name the EKS cluster", response.AuthDescription)
	}
}

func TestDescribeClusterAuthOmitsTheExternalID(t *testing.T) {
	conn := awsClusterConnection()
	conn.AWSCredentialMode = storage.AWSCredentialModeAssumeRole
	conn.AWSRoleARN = "arn:aws:iam::123456789012:role/apexload-eks"
	conn.AWSAccessKeyID = ""
	conn.AWSExternalIDCiphertext = "sealed-external-id"

	describe := describeClusterAuth(conn)
	if !strings.Contains(describe, "arn:aws:iam::123456789012:role/apexload-eks") {
		t.Errorf("describeClusterAuth = %q, want the role ARN (the key diagnostic)", describe)
	}
	if strings.Contains(describe, "sealed-external-id") {
		t.Fatalf("describeClusterAuth = %q leaks the external ID", describe)
	}
}

func TestDefaultAuthModeForEKSIsAWSIAM(t *testing.T) {
	if got := defaultAuthModeForKind(storage.ClusterKindEKS); got != storage.ClusterAuthAWSIAM {
		t.Fatalf("defaultAuthModeForKind(eks) = %q, want %q", got, storage.ClusterAuthAWSIAM)
	}
	// The existing kinds are unchanged.
	if got := defaultAuthModeForKind(storage.ClusterKindAKS); got != storage.ClusterAuthStaticToken {
		t.Errorf("defaultAuthModeForKind(aks) = %q, want the unchanged static-token default", got)
	}
	if got := defaultAuthModeForKind(storage.ClusterKindInCluster); got != storage.ClusterAuthInCluster {
		t.Errorf("defaultAuthModeForKind(in_cluster) = %q, want in_cluster", got)
	}
}

func TestDescribeAWSIdentityErrorIsActionable(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"operation error STS: GetCallerIdentity, InvalidClientTokenId: bad key", "access key ID is not valid"},
		{"operation error STS: SignatureDoesNotMatch: nope", "secret access key does not match"},
		{"operation error STS: RequestTimeTooSkewed: drift", "clock is out of sync"},
		{"operation error STS: AccessDenied: not allowed to assume", "trust policy and external ID"},
	} {
		got := describeAWSIdentityError(errors.New(tc.raw))
		if !strings.Contains(got, tc.want) {
			t.Errorf("describeAWSIdentityError(%q) = %q, want it to mention %q", tc.raw, got, tc.want)
		}
	}
}
