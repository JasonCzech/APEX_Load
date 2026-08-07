package kubeops

import (
	"context"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The AWS documentation's example key pair. It is public, non-functional, and
// used here only because presigning is entirely offline: no request is made, so
// a real-looking key produces a real, deterministic canonical request without a
// network or a mock framework.
const (
	testAccessKeyID     = "AKIAIOSFODNN7EXAMPLE"
	testSecretAccessKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

func staticKeySource(t *testing.T, mutate func(*AwsIAMConfig)) *AwsIAMTokenSource {
	t.Helper()
	cfg := AwsIAMConfig{
		Region:          "us-east-1",
		ClusterName:     "prod-perf",
		CredentialMode:  AwsCredentialModeStaticKeys,
		AccessKeyID:     testAccessKeyID,
		SecretAccessKey: testSecretAccessKey,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	source, err := NewAwsIAMTokenSource(cfg)
	if err != nil {
		t.Fatalf("NewAwsIAMTokenSource: %v", err)
	}
	return source
}

// decodeEKSToken reverses the webhook's wire format so assertions can be made
// against the presigned URL the cluster would actually receive.
func decodeEKSToken(t *testing.T, token string) *url.URL {
	t.Helper()
	if !strings.HasPrefix(token, EKSTokenPrefix) {
		t.Fatalf("token %q does not carry the %q prefix", token, EKSTokenPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, EKSTokenPrefix))
	if err != nil {
		t.Fatalf("token payload is not base64url: %v", err)
	}
	parsed, err := url.Parse(string(raw))
	if err != nil {
		t.Fatalf("token payload is not a URL: %v", err)
	}
	return parsed
}

// TestAwsIAMTokenFormatMatchesEKSWebhookContract pins every property the EKS
// authentication webhook checks. It runs fully offline.
func TestAwsIAMTokenFormatMatchesEKSWebhookContract(t *testing.T) {
	source := staticKeySource(t, nil)
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if len(token) > maxEKSTokenBytes {
		t.Fatalf("token length = %d, want <= %d", len(token), maxEKSTokenBytes)
	}

	parsed := decodeEKSToken(t, token)
	if parsed.Scheme != "https" {
		t.Fatalf("presigned scheme = %q, want https", parsed.Scheme)
	}
	if err := ValidateSTSEndpoint("https://"+parsed.Host, "us-east-1"); err != nil {
		t.Fatalf("presigned host %q is not an allowed STS endpoint: %v", parsed.Host, err)
	}

	query := parsed.Query()
	if got := query.Get("Action"); got != "GetCallerIdentity" {
		t.Fatalf("Action = %q, want GetCallerIdentity", got)
	}
	// The cluster binding must be a SIGNED HEADER, not a query parameter: the
	// webhook rejects a token that does not sign x-k8s-aws-id.
	signed := strings.ToLower(query.Get("X-Amz-SignedHeaders"))
	if !strings.Contains(signed, eksClusterIDHeader) {
		t.Fatalf("X-Amz-SignedHeaders = %q, want it to include %q", signed, eksClusterIDHeader)
	}
	// X-Amz-Expires must have been hoisted from a header into the query.
	expires, err := strconv.Atoi(query.Get("X-Amz-Expires"))
	if err != nil {
		t.Fatalf("X-Amz-Expires = %q, want an integer: %v", query.Get("X-Amz-Expires"), err)
	}
	if expires <= 0 || expires > 900 {
		t.Fatalf("X-Amz-Expires = %d, want 0 < n <= 900", expires)
	}
	if query.Get("X-Amz-Signature") == "" {
		t.Fatal("presigned URL carries no X-Amz-Signature")
	}
	if query.Get("X-Amz-Date") == "" {
		t.Fatal("presigned URL carries no X-Amz-Date")
	}
	// The credential scope must name the configured key and region, which is
	// what proves the configured static key -- and not some ambient identity --
	// signed this request.
	credential := query.Get("X-Amz-Credential")
	if !strings.HasPrefix(credential, testAccessKeyID+"/") {
		t.Fatalf("X-Amz-Credential = %q, want it to begin with the configured access key", credential)
	}
	if !strings.Contains(credential, "/us-east-1/sts/aws4_request") {
		t.Fatalf("X-Amz-Credential = %q, want the us-east-1 sts scope", credential)
	}
}

// TestAwsSTSEndpointMustBeAnAllowedSTSHost is the direct analogue of
// TestAzureAuthorityMustBeAnAllowedLoginEndpoint. A presigned GetCallerIdentity
// URL is a replayable bearer credential, so an administrator-supplied endpoint
// that is not real STS is a credential-exfiltration channel.
func TestAwsSTSEndpointMustBeAnAllowedSTSHost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		region   string
		wantErr  bool
	}{
		{name: "empty resolves normally", endpoint: "", region: "us-east-1"},
		{name: "global endpoint", endpoint: "https://sts.amazonaws.com", region: "us-east-1"},
		{name: "regional endpoint", endpoint: "https://sts.us-east-1.amazonaws.com", region: "us-east-1"},
		{name: "fips endpoint", endpoint: "https://sts-fips.us-east-1.amazonaws.com", region: "us-east-1"},
		{name: "govcloud", endpoint: "https://sts.us-gov-west-1.amazonaws.com", region: "us-gov-west-1"},
		{name: "china partition", endpoint: "https://sts.cn-north-1.amazonaws.com.cn", region: "cn-north-1"},

		{name: "suffix impersonation", endpoint: "https://sts.us-east-1.amazonaws.com.evil.example", region: "us-east-1", wantErr: true},
		{name: "unrelated host", endpoint: "https://attacker.example", region: "us-east-1", wantErr: true},
		{name: "plaintext", endpoint: "http://sts.us-east-1.amazonaws.com", region: "us-east-1", wantErr: true},
		{name: "carries a path", endpoint: "https://sts.us-east-1.amazonaws.com/collect", region: "us-east-1", wantErr: true},
		{name: "carries a query", endpoint: "https://sts.us-east-1.amazonaws.com?to=evil", region: "us-east-1", wantErr: true},
		{name: "embeds credentials", endpoint: "https://user:pass@sts.us-east-1.amazonaws.com", region: "us-east-1", wantErr: true},
		{name: "region mismatch", endpoint: "https://sts.eu-west-1.amazonaws.com", region: "us-east-1", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSTSEndpoint(tc.endpoint, tc.region)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateSTSEndpoint(%q, %q) = nil, want an error", tc.endpoint, tc.region)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateSTSEndpoint(%q, %q) = %v, want nil", tc.endpoint, tc.region, err)
			}

			// The constructor must enforce the same rule, so a bad endpoint is
			// rejected when the connection is saved rather than at scale time.
			_, ctorErr := NewAwsIAMTokenSource(AwsIAMConfig{
				Region: tc.region, ClusterName: "prod-perf",
				CredentialMode: AwsCredentialModeStaticKeys,
				AccessKeyID:    testAccessKeyID, SecretAccessKey: testSecretAccessKey,
				STSEndpoint: tc.endpoint,
			})
			if tc.wantErr != (ctorErr != nil) {
				t.Fatalf("NewAwsIAMTokenSource endpoint error = %v, wantErr %v", ctorErr, tc.wantErr)
			}
		})
	}
}

// TestAwsIAMTokenSourceNeverFallsBackToAmbientCredentials is the AWS
// transposition of TestNewRemoteClientNeverFallsBackToInClusterEnvironment: a
// connection that declares static keys must never sign with the controller's
// own identity.
func TestAwsIAMTokenSourceNeverFallsBackToAmbientCredentials(t *testing.T) {
	// Ambient credentials are present in the environment throughout.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAAMBIENTAMBIENT00")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret-should-never-be-used")
	t.Setenv("AWS_REGION", "us-east-1")

	t.Run("an incomplete static key is refused rather than falling back", func(t *testing.T) {
		if _, err := NewAwsIAMTokenSource(AwsIAMConfig{
			Region: "us-east-1", ClusterName: "prod-perf",
			CredentialMode: AwsCredentialModeStaticKeys,
			AccessKeyID:    testAccessKeyID,
		}); err == nil {
			t.Fatal("NewAwsIAMTokenSource = nil error, want a refusal for a missing secret key")
		}
	})

	t.Run("the configured key signs, not the ambient one", func(t *testing.T) {
		source := staticKeySource(t, nil)
		token, err := source.Token(context.Background())
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		credential := decodeEKSToken(t, token).Query().Get("X-Amz-Credential")
		if !strings.HasPrefix(credential, testAccessKeyID+"/") {
			t.Fatalf("X-Amz-Credential = %q, want the configured key %q", credential, testAccessKeyID)
		}
		if strings.Contains(credential, "AMBIENT") {
			t.Fatalf("X-Amz-Credential = %q, ambient environment credentials leaked into the signature", credential)
		}
	})

	t.Run("ambient mode refuses to also carry static keys", func(t *testing.T) {
		if _, err := NewAwsIAMTokenSource(AwsIAMConfig{
			Region: "us-east-1", ClusterName: "prod-perf",
			CredentialMode: AwsCredentialModeAmbient,
			AccessKeyID:    testAccessKeyID, SecretAccessKey: testSecretAccessKey,
		}); err == nil {
			t.Fatal("NewAwsIAMTokenSource = nil error, want a refusal for an ambiguous credential configuration")
		}
	})
}

// TestAwsIAMTokenSourceRejectsUnsafeClusterNames guards header injection: the
// cluster name is written into a signed HTTP header.
func TestAwsIAMTokenSourceRejectsUnsafeClusterNames(t *testing.T) {
	for _, name := range []string{
		"",
		"prod cluster",
		"prod\r\nX-Evil: 1",
		"prod\ncluster",
		"-leading-dash",
		strings.Repeat("a", 200),
	} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			if _, err := NewAwsIAMTokenSource(AwsIAMConfig{
				Region: "us-east-1", ClusterName: name,
				CredentialMode: AwsCredentialModeStaticKeys,
				AccessKeyID:    testAccessKeyID, SecretAccessKey: testSecretAccessKey,
			}); err == nil {
				t.Fatalf("NewAwsIAMTokenSource(cluster %q) = nil error, want a refusal", name)
			}
		})
	}
}

// countingPresigner records how often a token was actually minted.
type countingPresigner struct {
	calls int
}

func (p *countingPresigner) PresignGetCallerIdentity(_ context.Context, clusterName string) (string, error) {
	p.calls++
	return "https://sts.us-east-1.amazonaws.com/?Action=GetCallerIdentity&Version=2011-06-15" +
		"&X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=" + testAccessKeyID +
		"%2F20260806%2Fus-east-1%2Fsts%2Faws4_request&X-Amz-Date=20260806T000000Z" +
		"&X-Amz-Expires=60&X-Amz-SignedHeaders=host%3B" + eksClusterIDHeader +
		"&X-Amz-Signature=" + strconv.Itoa(p.calls), nil
}

func TestAwsIAMTokenSourceCachesAndInvalidates(t *testing.T) {
	source := staticKeySource(t, nil)
	presigner := &countingPresigner{}
	source.presigner = presigner
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	source.now = func() time.Time { return now }

	ctx := context.Background()
	first, err := source.Token(ctx)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if _, err := source.Token(ctx); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if presigner.calls != 1 {
		t.Fatalf("presign calls = %d after two reads, want 1 (the token must be cached)", presigner.calls)
	}

	source.Invalidate()
	second, err := source.Token(ctx)
	if err != nil {
		t.Fatalf("Token after Invalidate: %v", err)
	}
	if presigner.calls != 2 {
		t.Fatalf("presign calls = %d after Invalidate, want 2", presigner.calls)
	}
	if first == second {
		t.Fatal("Invalidate did not force a fresh token")
	}

	// Advancing past the lifetime minus the renewal skew must re-mint.
	now = now.Add(eksTokenLifetime - tokenRenewalSkew + time.Second)
	if _, err := source.Token(ctx); err != nil {
		t.Fatalf("Token after expiry: %v", err)
	}
	if presigner.calls != 3 {
		t.Fatalf("presign calls = %d after expiry, want 3", presigner.calls)
	}
}

// TestAwsIAMInvalidateKeepsTheCredentialProvider pins the deliberate asymmetry
// documented on Invalidate: dropping the credentials too would turn a 403-storm
// from an unmapped caller ARN into one real AssumeRole per request.
func TestAwsIAMInvalidateKeepsTheCredentialProvider(t *testing.T) {
	source := staticKeySource(t, nil)
	if _, err := source.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if source.creds == nil {
		t.Fatal("credential provider was not cached after the first mint")
	}
	cached := source.creds
	source.Invalidate()
	if source.token != "" {
		t.Fatal("Invalidate did not drop the bearer token")
	}
	if source.creds == nil {
		t.Fatal("Invalidate dropped the credential provider; a 403 retry would re-fetch credentials every request")
	}
	if source.creds != cached {
		t.Fatal("Invalidate replaced the credential provider")
	}
}

func TestAwsIAMDescribeOmitsSecrets(t *testing.T) {
	const externalID = "super-secret-external-id"
	for _, tc := range []struct {
		name string
		cfg  AwsIAMConfig
		want string
	}{
		{
			name: "static keys",
			cfg: AwsIAMConfig{
				Region: "us-east-1", ClusterName: "prod-perf",
				CredentialMode: AwsCredentialModeStaticKeys,
				AccessKeyID:    testAccessKeyID, SecretAccessKey: testSecretAccessKey,
			},
			want: "static access key",
		},
		{
			name: "assume role",
			cfg: AwsIAMConfig{
				Region: "us-east-1", ClusterName: "prod-perf",
				CredentialMode: AwsCredentialModeAssumeRole,
				RoleARN:        "arn:aws:iam::123456789012:role/apexload-eks",
				ExternalID:     externalID,
			},
			want: "arn:aws:iam::123456789012:role/apexload-eks",
		},
		{
			name: "ambient",
			cfg: AwsIAMConfig{
				Region: "us-east-1", ClusterName: "prod-perf",
				CredentialMode: AwsCredentialModeAmbient,
			},
			want: "workload identity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := NewAwsIAMTokenSource(tc.cfg)
			if err != nil {
				t.Fatalf("NewAwsIAMTokenSource: %v", err)
			}
			describe := source.Describe()
			if !strings.Contains(describe, tc.want) {
				t.Fatalf("Describe() = %q, want it to mention %q", describe, tc.want)
			}
			for _, secret := range []string{testSecretAccessKey, externalID} {
				if strings.Contains(describe, secret) {
					t.Fatalf("Describe() = %q leaks a secret", describe)
				}
			}
			// The access key ID is half a credential pair; keep it out too.
			if strings.Contains(describe, testAccessKeyID) {
				t.Fatalf("Describe() = %q leaks the access key ID", describe)
			}
		})
	}
}

func TestAwsIAMTokenSourceValidatesCredentialMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  AwsIAMConfig
	}{
		{name: "unset mode", cfg: AwsIAMConfig{Region: "us-east-1", ClusterName: "c"}},
		{name: "unknown mode", cfg: AwsIAMConfig{Region: "us-east-1", ClusterName: "c", CredentialMode: "instance_profile"}},
		{name: "bad region", cfg: AwsIAMConfig{Region: "US East 1", ClusterName: "c", CredentialMode: AwsCredentialModeAmbient}},
		{
			name: "assume role without an ARN",
			cfg:  AwsIAMConfig{Region: "us-east-1", ClusterName: "c", CredentialMode: AwsCredentialModeAssumeRole},
		},
		{
			name: "assume role with a malformed ARN",
			cfg: AwsIAMConfig{Region: "us-east-1", ClusterName: "c", CredentialMode: AwsCredentialModeAssumeRole,
				RoleARN: "arn:aws:iam::123:role/short-account"},
		},
		{
			name: "assume role with a user ARN",
			cfg: AwsIAMConfig{Region: "us-east-1", ClusterName: "c", CredentialMode: AwsCredentialModeAssumeRole,
				RoleARN: "arn:aws:iam::123456789012:user/someone"},
		},
		{
			name: "assume role with half a static key pair",
			cfg: AwsIAMConfig{Region: "us-east-1", ClusterName: "c", CredentialMode: AwsCredentialModeAssumeRole,
				RoleARN: "arn:aws:iam::123456789012:role/apexload", AccessKeyID: testAccessKeyID},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewAwsIAMTokenSource(tc.cfg); err == nil {
				t.Fatalf("NewAwsIAMTokenSource(%+v) = nil error, want a refusal", tc.cfg)
			}
		})
	}
}

// TestAwsIAMTokenSourceSatisfiesTheKubeopsContracts keeps the client's 401/403
// retry path wired: without invalidatingTokenSource the retry would replay the
// same rejected token.
func TestAwsIAMTokenSourceSatisfiesTheKubeopsContracts(t *testing.T) {
	source := staticKeySource(t, nil)
	var asTokenSource TokenSource = source
	if _, ok := asTokenSource.(invalidatingTokenSource); !ok {
		t.Fatal("AwsIAMTokenSource does not implement invalidatingTokenSource; Client could not drop a rejected token")
	}
}

// TestAwsIAMTokenSourceIsSafeUnderConcurrency exercises the paths that take
// s.mu from different directions. Token holds the lock across presigning and
// calls credentialsLocked beneath it; CallerIdentity takes the same lock via
// credentials(). A future refactor that made one call the other would deadlock,
// and Client fans out concurrent requests per cluster connection.
func TestAwsIAMTokenSourceIsSafeUnderConcurrency(t *testing.T) {
	source := staticKeySource(t, nil)
	presigner := &countingPresigner{}
	source.presigner = presigner

	ctx := context.Background()
	const goroutines = 32
	tokens := make(chan string, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Interleave reads with invalidations so the cache is contended
			// rather than warmed once and read.
			if i%4 == 0 {
				source.Invalidate()
			}
			token, err := source.Token(ctx)
			if err != nil {
				t.Errorf("Token: %v", err)
				return
			}
			tokens <- token
		}(i)
	}
	wg.Wait()
	close(tokens)

	for token := range tokens {
		if !strings.HasPrefix(token, EKSTokenPrefix) {
			t.Fatalf("concurrent Token() produced a malformed token: %q", token)
		}
	}
	// Every mint must have gone through the lock: with 8 invalidations the
	// count can never exceed one mint per goroutine.
	if presigner.calls < 1 || presigner.calls > goroutines {
		t.Fatalf("presign calls = %d, want 1..%d", presigner.calls, goroutines)
	}
}
