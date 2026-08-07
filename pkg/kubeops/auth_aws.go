package kubeops

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// EKS authenticates a bearer token that is a presigned STS GetCallerIdentity
// URL carrying the cluster name as a SIGNED header. The cluster's webhook
// re-signs nothing: it decodes the URL, checks its shape, and replays it to
// STS. Every constant below is therefore a wire-protocol constant of that
// contract, not a tunable.
const (
	// EKSTokenPrefix is the scheme marker the EKS webhook requires.
	EKSTokenPrefix = "k8s-aws-v1."
	// eksClusterIDHeader must survive as a SIGNED header (not hoisted into the
	// query) so the webhook can bind the token to one cluster.
	eksClusterIDHeader = "x-k8s-aws-id"
	// eksPresignExpiresSeconds must satisfy 0 < X-Amz-Expires <= 900. The
	// webhook does not use it for freshness (X-Amz-Date is), so this matches
	// aws-iam-authenticator rather than trying to be clever.
	eksPresignExpiresSeconds = 60
	// eksTokenLifetime is how long the webhook accepts a token after its
	// X-Amz-Date. Combined with the shared tokenRenewalSkew this re-mints at
	// 13 minutes.
	eksTokenLifetime = 15 * time.Minute
	// maxEKSTokenBytes is the webhook's token size cap.
	maxEKSTokenBytes = 4096
	// assumeRoleDuration bounds a role session. Short by design: the session is
	// refreshed transparently by the SDK's credentials cache.
	assumeRoleDuration = 15 * time.Minute
)

// AwsCredentialMode selects how the controller obtains the AWS identity it
// presigns with. It is always explicit — never inferred from which fields
// happen to be populated, because inference is how a connection silently ends
// up authenticating as the controller's own identity against a cluster the
// administrator did not intend.
type AwsCredentialMode string

const (
	// AwsCredentialModeStaticKeys uses an administrator-supplied access key.
	AwsCredentialModeStaticKeys AwsCredentialMode = "static_keys"
	// AwsCredentialModeAssumeRole assumes a role, optionally chaining from
	// static keys, otherwise from the controller's ambient identity.
	AwsCredentialModeAssumeRole AwsCredentialMode = "assume_role"
	// AwsCredentialModeAmbient uses the controller's own identity: IRSA or EKS
	// Pod Identity in a Kubernetes deployment.
	AwsCredentialModeAmbient AwsCredentialMode = "ambient"
)

// awsSTSHostPattern bounds where a presigned GetCallerIdentity URL may point.
//
// This is the security centrepiece of the AWS path and the direct analogue of
// allowedAzureAuthorityHosts. A presigned URL IS a bearer credential: any host
// that receives one can replay it to real STS and impersonate the controller
// for the token's lifetime. Because STSEndpoint is administrator-supplied, an
// unchecked value is a credential-exfiltration channel. STS is regional, so
// unlike the Azure authority this has to be a pattern rather than a fixed set:
// sts.amazonaws.com, sts.{region}.amazonaws.com, the FIPS endpoints, and the
// aws-cn partition suffix.
var awsSTSHostPattern = regexp.MustCompile(`^sts(-fips)?(\.[a-z0-9-]{1,32})?\.amazonaws\.com(\.cn)?$`)

// awsRoleARNPattern accepts the IAM role ARNs of every partition.
var awsRoleARNPattern = regexp.MustCompile(`^arn:aws[a-z-]*:iam::\d{12}:role/.+$`)

// awsRegionPattern is deliberately strict: the region is interpolated into an
// endpoint host.
var awsRegionPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// eksClusterNamePattern is a security check, not cosmetics. The cluster name is
// written into a SIGNED HTTP header, so any CR/LF or space is a header
// injection into the SigV4 canonical request.
var eksClusterNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)

// awsSessionNamePattern is IAM's own charset for a role session name.
var awsSessionNamePattern = regexp.MustCompile(`^[\w+=,.@-]{2,64}$`)

// AwsCallerIdentity is the non-secret result of an STS GetCallerIdentity call.
// The ARN is the single most useful diagnostic on this path: it is exactly the
// principal an administrator must map in an EKS access entry or the aws-auth
// ConfigMap.
type AwsCallerIdentity struct {
	Account string
	ARN     string
	UserID  string
}

// AwsIAMConfig configures the EKS bearer-token flow.
type AwsIAMConfig struct {
	// Region is the EKS cluster's region and the STS signing region.
	Region string
	// ClusterName is the EKS cluster name, sent as the x-k8s-aws-id header.
	ClusterName string
	// CredentialMode is required and must be one of the AwsCredentialMode
	// constants.
	CredentialMode AwsCredentialMode

	AccessKeyID     string
	SecretAccessKey string

	// RoleARN and ExternalID configure AwsCredentialModeAssumeRole.
	RoleARN    string
	ExternalID string
	// SessionName appears in CloudTrail and in the assumed-role ARN the
	// administrator must map, so callers should pass something stable and
	// identifiable. Defaults to "apexload".
	SessionName string

	// STSEndpoint overrides endpoint resolution. Empty is the normal case and
	// resolves to sts.{Region}.amazonaws.com.
	STSEndpoint string

	// HTTPClient carries the caller's egress policy for the paths that actually
	// make a request (assume-role, GetCallerIdentity). Presigning itself is
	// entirely offline.
	//
	// Trust-source boundary: values that arrive from a DATABASE ROW
	// (STSEndpoint, RoleARN) are administrator input and go through this
	// guarded client plus the host allow-list. Values that arrive from the
	// PROCESS ENVIRONMENT (IRSA's AWS_WEB_IDENTITY_TOKEN_FILE and friends) are
	// set by whoever deployed the controller, not typed into a form, so ambient
	// credential resolution uses the SDK's default client.
	HTTPClient *http.Client

	// AllowInstanceMetadata opts ambient mode into the EC2 IMDS credential
	// provider. It is off by default because IMDS (169.254.169.254) and EKS Pod
	// Identity (169.254.170.23) are link-local — precisely the range the
	// cluster dial guard exists to block.
	AllowInstanceMetadata bool
}

// eksPresigner is the single test seam over the STS SDK, so the caching and
// invalidation behaviour can be driven without a network or a mock framework.
type eksPresigner interface {
	PresignGetCallerIdentity(ctx context.Context, clusterName string) (string, error)
}

// AwsIAMTokenSource mints EKS bearer tokens by presigning STS
// GetCallerIdentity, and caches them until shortly before the webhook stops
// accepting them. It is safe for concurrent use.
type AwsIAMTokenSource struct {
	cfg AwsIAMConfig

	mu        sync.Mutex
	token     string
	expiresAt time.Time

	// creds is built once and deliberately survives Invalidate — see the
	// comment there.
	creds     aws.CredentialsProvider
	presigner eksPresigner
	// now is a test seam; nil means time.Now.
	now func() time.Time
}

var (
	_ TokenSource             = (*AwsIAMTokenSource)(nil)
	_ invalidatingTokenSource = (*AwsIAMTokenSource)(nil)
)

// NewAwsIAMTokenSource validates the credential shape up front so a
// misconfigured cluster connection fails at save time rather than on the first
// scale attempt — the same contract as NewAzureServicePrincipalTokenSource.
func NewAwsIAMTokenSource(cfg AwsIAMConfig) (*AwsIAMTokenSource, error) {
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.ClusterName = strings.TrimSpace(cfg.ClusterName)
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	cfg.SecretAccessKey = strings.TrimSpace(cfg.SecretAccessKey)
	cfg.RoleARN = strings.TrimSpace(cfg.RoleARN)
	cfg.ExternalID = strings.TrimSpace(cfg.ExternalID)
	cfg.SessionName = firstNonEmpty(strings.TrimSpace(cfg.SessionName), "apexload")
	cfg.STSEndpoint = strings.TrimRight(strings.TrimSpace(cfg.STSEndpoint), "/")

	if !awsRegionPattern.MatchString(cfg.Region) {
		return nil, fmt.Errorf("aws region %q is not a valid region name", cfg.Region)
	}
	if !eksClusterNamePattern.MatchString(cfg.ClusterName) {
		return nil, fmt.Errorf("eks cluster name %q is not valid", cfg.ClusterName)
	}
	if !awsSessionNamePattern.MatchString(cfg.SessionName) {
		return nil, fmt.Errorf("aws role session name %q is not valid", cfg.SessionName)
	}
	if err := ValidateSTSEndpoint(cfg.STSEndpoint, cfg.Region); err != nil {
		return nil, err
	}

	switch cfg.CredentialMode {
	case AwsCredentialModeStaticKeys:
		if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
			return nil, fmt.Errorf("an aws access key ID and secret access key are required for static-key authentication")
		}
	case AwsCredentialModeAssumeRole:
		if !awsRoleARNPattern.MatchString(cfg.RoleARN) {
			return nil, fmt.Errorf("aws role ARN %q is not an IAM role ARN", cfg.RoleARN)
		}
		if (cfg.AccessKeyID == "") != (cfg.SecretAccessKey == "") {
			return nil, fmt.Errorf("aws static keys must be supplied together or not at all")
		}
	case AwsCredentialModeAmbient:
		// An ambiguous configuration must fail loudly rather than pick. Silently
		// preferring one over the other is how an operator ends up believing a
		// key is in use when the controller's own role is.
		if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
			return nil, fmt.Errorf("ambient credentials cannot be combined with static aws keys")
		}
	default:
		return nil, fmt.Errorf("unsupported aws credential mode %q", cfg.CredentialMode)
	}

	source := &AwsIAMTokenSource{cfg: cfg}
	return source, nil
}

// ValidateSTSEndpoint enforces that a custom STS endpoint is an https bare host
// on the AWS STS allow-list, in the configured region. Empty is valid and means
// "let the SDK resolve the regional endpoint".
func ValidateSTSEndpoint(raw, region string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("aws STS endpoint is not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("aws STS endpoint must be an https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return fmt.Errorf("aws STS endpoint must be a bare https host")
	}
	host := strings.ToLower(parsed.Hostname())
	if !awsSTSHostPattern.MatchString(host) {
		return fmt.Errorf("aws STS endpoint host %q is not an allowed AWS STS endpoint", host)
	}
	// A host that names a region must name the configured one, so a credential
	// signed for one region can never be shipped to a host claiming another.
	if err := requireSTSHostRegion(host, region); err != nil {
		return err
	}
	return nil
}

// requireSTSHostRegion rejects sts.<other-region>.amazonaws.com. The global
// sts.amazonaws.com carries no region label and is always accepted.
func requireSTSHostRegion(host, region string) error {
	labels := strings.Split(strings.TrimSuffix(host, ".cn"), ".")
	// sts[-fips].{region}.amazonaws.com => 4 labels; sts.amazonaws.com => 3.
	if len(labels) != 4 {
		return nil
	}
	if got := labels[1]; got != strings.TrimSpace(region) {
		return fmt.Errorf("aws STS endpoint is in region %q but the connection is configured for %q", got, region)
	}
	return nil
}

func (s *AwsIAMTokenSource) Describe() string {
	if s == nil {
		return "aws iam (unconfigured)"
	}
	detail := "unknown credentials"
	switch s.cfg.CredentialMode {
	case AwsCredentialModeStaticKeys:
		// Never any part of the key itself.
		detail = "static access key"
	case AwsCredentialModeAssumeRole:
		// The role ARN is not secret and is the most useful diagnostic there is.
		// The external ID emphatically is secret and never appears here.
		detail = "assume role " + s.cfg.RoleARN
	case AwsCredentialModeAmbient:
		detail = "workload identity"
	}
	return fmt.Sprintf("aws iam (eks cluster %s, region %s, %s)", s.cfg.ClusterName, s.cfg.Region, detail)
}

// Invalidate drops the cached bearer token so the next call re-mints one.
//
// It deliberately does NOT drop the credential provider. Client retries once on
// 401/403, and the overwhelmingly common cause of a 403 here is a caller ARN
// with no EKS access entry — a condition that does not resolve on retry. If
// Invalidate also cleared the credentials, a dashboard polling a misconfigured
// cluster would turn into one real AssumeRole call per request and STS would
// throttle the controller. Presigning is offline and free; fetching credentials
// is neither. The Azure path has no equivalent hazard because minting there is
// always a network call.
func (s *AwsIAMTokenSource) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
	s.expiresAt = time.Time{}
}

func (s *AwsIAMTokenSource) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *AwsIAMTokenSource) Token(ctx context.Context) (string, error) {
	if s == nil {
		return "", fmt.Errorf("aws token source is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if s.token != "" && now.Before(s.expiresAt.Add(-tokenRenewalSkew)) {
		return s.token, nil
	}
	presigner, err := s.resolvePresigner(ctx)
	if err != nil {
		return "", err
	}
	rawURL, err := presigner.PresignGetCallerIdentity(ctx, s.cfg.ClusterName)
	if err != nil {
		return "", fmt.Errorf("presign AWS STS GetCallerIdentity: %w", err)
	}
	token, err := s.encodeEKSToken(rawURL)
	if err != nil {
		return "", err
	}
	s.token = token
	s.expiresAt = now.Add(eksTokenLifetime)
	return token, nil
}

// encodeEKSToken applies the webhook's wire format and re-checks every
// post-condition. The host re-check matters because endpoint resolution is an
// SDK-internal moving part: without it, a future resolver change would ship a
// signed credential somewhere unexpected instead of failing loudly.
func (s *AwsIAMTokenSource) encodeEKSToken(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("presigned AWS STS URL is not a valid URL: %w", err)
	}
	if err := ValidateSTSEndpoint("https://"+parsed.Host, s.cfg.Region); err != nil {
		return "", fmt.Errorf("presigned AWS STS URL resolved to an unexpected endpoint: %w", err)
	}
	query := parsed.Query()
	if !strings.Contains(strings.ToLower(query.Get("X-Amz-SignedHeaders")), eksClusterIDHeader) {
		return "", fmt.Errorf("presigned AWS STS URL does not sign the %s header", eksClusterIDHeader)
	}
	token := EKSTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(rawURL))
	if len(token) > maxEKSTokenBytes {
		return "", fmt.Errorf("presigned EKS token exceeds %d bytes", maxEKSTokenBytes)
	}
	return token, nil
}

// CallerIdentity resolves the principal the controller authenticates as. It is
// what the sts_identity connection probe reports, so an administrator can see
// the exact ARN to map in the cluster's access entries.
func (s *AwsIAMTokenSource) CallerIdentity(ctx context.Context) (AwsCallerIdentity, error) {
	if s == nil {
		return AwsCallerIdentity{}, fmt.Errorf("aws token source is not initialized")
	}
	creds, err := s.credentials(ctx)
	if err != nil {
		return AwsCallerIdentity{}, err
	}
	out, err := sts.New(s.stsOptions(creds)).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return AwsCallerIdentity{}, err
	}
	return AwsCallerIdentity{
		Account: aws.ToString(out.Account),
		ARN:     aws.ToString(out.Arn),
		UserID:  aws.ToString(out.UserId),
	}, nil
}

// resolvePresigner builds the presign client once. Callers hold s.mu.
func (s *AwsIAMTokenSource) resolvePresigner(ctx context.Context) (eksPresigner, error) {
	if s.presigner != nil {
		return s.presigner, nil
	}
	creds, err := s.credentialsLocked(ctx)
	if err != nil {
		return nil, err
	}
	s.presigner = stsPresigner{client: sts.New(s.stsOptions(creds))}
	return s.presigner, nil
}

func (s *AwsIAMTokenSource) stsOptions(creds aws.CredentialsProvider) sts.Options {
	options := sts.Options{
		Region:      s.cfg.Region,
		Credentials: creds,
	}
	if s.cfg.HTTPClient != nil {
		options.HTTPClient = s.cfg.HTTPClient
	}
	if s.cfg.STSEndpoint != "" {
		options.BaseEndpoint = aws.String(s.cfg.STSEndpoint)
	}
	return options
}

func (s *AwsIAMTokenSource) credentials(ctx context.Context) (aws.CredentialsProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.credentialsLocked(ctx)
}

// credentialsLocked builds the credential provider. Callers hold s.mu.
//
// The no-fallback stance here is the AWS transposition of NewRemoteClient's
// refusal to consult the in-cluster environment: when an administrator
// configured a key, a typo must fail rather than quietly sign with the
// controller's own instance or workload identity against a cluster they may not
// administer.
func (s *AwsIAMTokenSource) credentialsLocked(ctx context.Context) (aws.CredentialsProvider, error) {
	if s.creds != nil {
		return s.creds, nil
	}
	var (
		base aws.CredentialsProvider
		err  error
	)
	switch s.cfg.CredentialMode {
	case AwsCredentialModeStaticKeys:
		base = credentials.NewStaticCredentialsProvider(s.cfg.AccessKeyID, s.cfg.SecretAccessKey, "")
	case AwsCredentialModeAmbient:
		base, err = s.ambientCredentials(ctx)
	case AwsCredentialModeAssumeRole:
		if s.cfg.AccessKeyID != "" {
			base = credentials.NewStaticCredentialsProvider(s.cfg.AccessKeyID, s.cfg.SecretAccessKey, "")
		} else {
			base, err = s.ambientCredentials(ctx)
		}
	default:
		return nil, fmt.Errorf("unsupported aws credential mode %q", s.cfg.CredentialMode)
	}
	if err != nil {
		return nil, err
	}

	if s.cfg.CredentialMode == AwsCredentialModeAssumeRole {
		base = stscreds.NewAssumeRoleProvider(sts.New(s.stsOptions(base)), s.cfg.RoleARN, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = s.cfg.SessionName
			o.Duration = assumeRoleDuration
			if s.cfg.ExternalID != "" {
				o.ExternalID = aws.String(s.cfg.ExternalID)
			}
		})
	}
	s.creds = aws.NewCredentialsCache(base)
	return s.creds, nil
}

// ambientCredentials resolves the controller's own identity. This is the only
// path that consults the process environment, and IMDS stays disabled unless
// explicitly opted in.
func (s *AwsIAMTokenSource) ambientCredentials(ctx context.Context) (aws.CredentialsProvider, error) {
	options := []func(*config.LoadOptions) error{config.WithRegion(s.cfg.Region)}
	if !s.cfg.AllowInstanceMetadata {
		options = append(options, config.WithEC2IMDSClientEnableState(imds.ClientDisabled))
	}
	loaded, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("resolve ambient AWS credentials: %w", err)
	}
	if loaded.Credentials == nil {
		return nil, fmt.Errorf("no ambient AWS credentials are available to this controller")
	}
	return loaded.Credentials, nil
}

// stsPresigner is the production eksPresigner.
type stsPresigner struct {
	client *sts.Client
}

func (p stsPresigner) PresignGetCallerIdentity(ctx context.Context, clusterName string) (string, error) {
	presigned, err := sts.NewPresignClient(p.client).PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{},
		func(o *sts.PresignOptions) {
			o.ClientOptions = append(o.ClientOptions, func(so *sts.Options) {
				so.APIOptions = append(so.APIOptions,
					// x-k8s-aws-id does not match the X-Amz- hoisting pattern, so
					// it stays a signed header and lands in X-Amz-SignedHeaders —
					// which is exactly what the webhook binds the token on.
					smithyhttp.SetHeaderValue(eksClusterIDHeader, clusterName),
					// X-Amz-Expires DOES match that pattern, so setting it as a
					// header is what gets it hoisted into the query string.
					// Setting it on the query directly would not survive signing.
					smithyhttp.SetHeaderValue("X-Amz-Expires", strconv.Itoa(eksPresignExpiresSeconds)),
				)
			})
		})
	if err != nil {
		return "", err
	}
	return presigned.URL, nil
}
