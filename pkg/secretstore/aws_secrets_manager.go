package secretstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	secretsManagerProvider       = "aws_secrets_manager"
	secretsManagerSigningService = "secretsmanager"
	secretsManagerResponseLimit  = 1 << 20
	secretsManagerDiscoveryLimit = 256
)

// secretsManagerHostPattern bounds a custom endpoint to real AWS Secrets
// Manager. The threat is the same as the STS allow-list in pkg/kubeops: a
// SigV4-signed GetSecretValue sent to an attacker-controlled host is a
// replayable credential for the controller's whole secret namespace.
var secretsManagerHostPattern = regexp.MustCompile(`^secretsmanager(-fips)?\.[a-z0-9-]{1,32}\.amazonaws\.com(\.cn)?$`)

// AwsSecretsManagerStore reads and rotates project secrets held in AWS Secrets
// Manager. It mirrors AzureKeyVaultStore: the encrypted database provider stays
// authoritative for new managed values, and this store never invents names.
type AwsSecretsManagerStore struct {
	Region      string
	Endpoint    string
	Credentials aws.CredentialsProvider
	Signer      *v4.Signer
	Client      *http.Client
	Metadata    storage.ProjectSecretMetadataStore
}

func NewAwsSecretsManagerStoreFromEnv() (*AwsSecretsManagerStore, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED")), "true") {
		return nil, nil
	}
	region := strings.TrimSpace(os.Getenv("APEXLOAD_AWS_SECRETS_MANAGER_REGION"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if region == "" {
		return nil, errors.New("APEXLOAD_AWS_SECRETS_MANAGER_REGION is required when Secrets Manager writes are enabled")
	}
	endpoint, err := resolveSecretsManagerEndpoint(os.Getenv("APEXLOAD_AWS_SECRETS_MANAGER_ENDPOINT"), region)
	if err != nil {
		return nil, err
	}
	loaded, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("resolve AWS credentials for the Secrets Manager store: %w", err)
	}
	if loaded.Credentials == nil {
		return nil, errors.New("no AWS credentials are available for the Secrets Manager store")
	}
	return &AwsSecretsManagerStore{
		Region:      region,
		Endpoint:    endpoint,
		Credentials: loaded.Credentials,
		Signer:      v4.NewSigner(),
		Client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// resolveSecretsManagerEndpoint validates an operator-supplied override, or
// derives the regional endpoint when none is given.
func resolveSecretsManagerEndpoint(raw, region string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "https://secretsmanager." + region + ".amazonaws.com/", nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("AWS Secrets Manager endpoint is not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return "", errors.New("AWS Secrets Manager endpoint must be an https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return "", errors.New("AWS Secrets Manager endpoint must be a bare https host")
	}
	host := strings.ToLower(parsed.Hostname())
	if !secretsManagerHostPattern.MatchString(host) {
		return "", fmt.Errorf("AWS Secrets Manager endpoint host %q is not an allowed AWS endpoint", host)
	}
	return "https://" + host + "/", nil
}

func (s *AwsSecretsManagerStore) Writable() bool {
	return s.configured() && s.Metadata != nil
}

func (*AwsSecretsManagerStore) Provider() string { return secretsManagerProvider }

// CreateSupported reports false as a POLICY choice, not an API limitation.
//
// This differs from AzureKeyVaultStore, whose Set Secret operation is an upsert
// with no conditional-create form and therefore genuinely cannot implement
// create-if-absent. Secrets Manager's CreateSecret does fail with
// ResourceExistsException and could support it. It is disabled here so the
// encrypted database provider stays authoritative for new managed values —
// matching config.go's chain semantics — and so imports never have to reason
// about a name that is pending deletion.
func (*AwsSecretsManagerStore) CreateSupported() bool { return false }

func (s *AwsSecretsManagerStore) Create(context.Context, string, string, string, string) (Metadata, error) {
	if !s.Writable() {
		return Metadata{}, ErrReadOnly
	}
	return Metadata{}, ErrCreateUnsupported
}

func (s *AwsSecretsManagerStore) Replace(ctx context.Context, projectID, name, value, actor string) (Metadata, error) {
	if !s.Writable() {
		return Metadata{}, ErrReadOnly
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return Metadata{}, err
	}
	existing, err := s.Metadata.GetProjectSecretMetadata(projectID, name)
	if errors.Is(err, ErrNotFound) {
		if discoverErr := s.DiscoverManagedMetadata(ctx, projectID, []string{name}); discoverErr != nil {
			return Metadata{}, discoverErr
		}
		existing, err = s.Metadata.GetProjectSecretMetadata(projectID, name)
	}
	if err != nil {
		return Metadata{}, err
	}
	if existing.Provider != secretsManagerProvider {
		return Metadata{}, ErrReadOnly
	}
	metadata, err := s.write(ctx, projectID, name, value, actor)
	if err != nil {
		return Metadata{}, err
	}
	claim := &storage.ProjectSecretMetadata{
		ProjectID:  projectID,
		SecretName: name,
		Provider:   secretsManagerProvider,
		CreatedAt:  existing.CreatedAt,
		UpdatedAt:  metadata.UpdatedAt,
		CreatedBy:  strings.TrimSpace(actor),
	}
	replaced, err := s.Metadata.ReplaceProjectSecretMetadata(claim)
	if err != nil {
		// The remote PutSecretValue already succeeded. Preserve a distinct
		// commit ambiguity so the API can verify both the value and this
		// metadata claim before acknowledging the rotation.
		return Metadata{}, fmt.Errorf("%w: %v", ErrMetadataCommit, err)
	}
	if !replaced {
		return Metadata{}, ErrMetadataCommit
	}
	metadata.CreatedAt = existing.CreatedAt
	return metadata, nil
}

func (s *AwsSecretsManagerStore) write(ctx context.Context, projectID, name, value, actor string) (Metadata, error) {
	if !s.configured() {
		return Metadata{}, ErrReadOnly
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return Metadata{}, err
	}
	if len(value) == 0 || len(value) > maxSecretBytes {
		return Metadata{}, fmt.Errorf("secret value must contain 1-%d bytes", maxSecretBytes)
	}
	_, err = s.call(ctx, "PutSecretValue", map[string]any{
		"SecretId":     engine.ProjectSecretBindingName(projectID, name),
		"SecretString": value,
	})
	if err != nil {
		return Metadata{}, err
	}
	// The metadata record stores this value, and the dashboard compares the
	// returned metadata with the listed one; PostgreSQL keeps microseconds.
	now := time.Now().UTC().Truncate(time.Microsecond)
	return Metadata{
		ProjectID: projectID,
		Name:      name,
		Provider:  secretsManagerProvider,
		Writable:  true,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: strings.TrimSpace(actor),
	}, nil
}

func (s *AwsSecretsManagerStore) Get(ctx context.Context, projectID, name string) (string, Metadata, error) {
	if !s.configured() {
		return "", Metadata{}, ErrNotFound
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return "", Metadata{}, err
	}
	payload, err := s.call(ctx, "GetSecretValue", map[string]any{
		"SecretId": engine.ProjectSecretBindingName(projectID, name),
	})
	if errors.Is(err, ErrNotFound) {
		return "", Metadata{}, ErrNotFound
	}
	if err != nil {
		return "", Metadata{}, err
	}
	defer clear(payload)

	var decoded struct {
		SecretString string `json:"SecretString"`
		SecretBinary string `json:"SecretBinary"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", Metadata{}, errors.New("AWS Secrets Manager returned an undecodable secret")
	}
	if decoded.SecretString == "" {
		if decoded.SecretBinary != "" {
			// Refuse by name rather than guessing an encoding: a binary secret
			// silently reinterpreted as text is a corrupted credential.
			return "", Metadata{}, errors.New("AWS Secrets Manager secret holds binary data, which APEX Load cannot bind as a string value")
		}
		return "", Metadata{}, errors.New("AWS Secrets Manager returned no secret value")
	}
	if s.Metadata == nil {
		return decoded.SecretString, Metadata{
			ProjectID: projectID,
			Name:      name,
			Provider:  secretsManagerProvider,
		}, nil
	}
	if _, err := s.claimManagedMetadata(projectID, name, time.Now().UTC()); err != nil {
		decoded.SecretString = ""
		return "", Metadata{}, err
	}
	managed, err := s.Metadata.GetProjectSecretMetadata(projectID, name)
	if err != nil {
		decoded.SecretString = ""
		return "", Metadata{}, err
	}
	return decoded.SecretString, Metadata{
		ProjectID: managed.ProjectID,
		Name:      managed.SecretName,
		Provider:  managed.Provider,
		Writable:  managed.Provider == secretsManagerProvider && s.Writable(),
		CreatedAt: managed.CreatedAt,
		UpdatedAt: managed.UpdatedAt,
		CreatedBy: managed.CreatedBy,
	}, nil
}

// DiscoverManagedMetadata probes only caller-supplied deterministic names and
// records value-free lifecycle metadata.
//
// It uses DescribeSecret, which is value-free by construction — strictly better
// than the Key Vault path, which must issue a full read and stream the body to
// discard because that API has no metadata-only operation.
func (s *AwsSecretsManagerStore) DiscoverManagedMetadata(ctx context.Context, projectID string, names []string) error {
	if !s.configured() || s.Metadata == nil {
		return nil
	}
	ctx, cancel := engine.WithSecretResolutionBudget(ctx)
	defer cancel()

	uniqueNames := make([]string, 0, min(len(names), secretsManagerDiscoveryLimit))
	seen := make(map[string]struct{}, min(len(names), secretsManagerDiscoveryLimit))
	for _, rawName := range names {
		normalizedProjectID, name, err := validateIdentity(projectID, rawName)
		if err != nil {
			return err
		}
		projectID = normalizedProjectID
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		if len(uniqueNames) == secretsManagerDiscoveryLimit {
			break
		}
		seen[name] = struct{}{}
		uniqueNames = append(uniqueNames, name)
	}
	for _, name := range uniqueNames {
		_, err := s.call(ctx, "DescribeSecret", map[string]any{
			"SecretId": engine.ProjectSecretBindingName(projectID, name),
		})
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := s.claimManagedMetadata(projectID, name, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (s *AwsSecretsManagerStore) claimManagedMetadata(projectID, name string, now time.Time) (bool, error) {
	if s.Metadata == nil {
		return false, nil
	}
	claim := &storage.ProjectSecretMetadata{
		ProjectID:  projectID,
		SecretName: name,
		Provider:   secretsManagerProvider,
		CreatedAt:  now.UTC(),
		UpdatedAt:  now.UTC(),
	}
	created, err := s.Metadata.CreateProjectSecretMetadata(claim)
	if err != nil {
		return false, err
	}
	if created {
		return true, nil
	}
	existing, err := s.Metadata.GetProjectSecretMetadata(projectID, name)
	if err != nil {
		return false, err
	}
	if existing.Provider != secretsManagerProvider {
		return false, ErrReadOnly
	}
	return true, nil
}

func (s *AwsSecretsManagerStore) List(ctx context.Context, projectID string) ([]Metadata, error) {
	if s == nil || s.Metadata == nil {
		return []Metadata{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := s.Metadata.ListExternalProjectSecretMetadata(strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	result := make([]Metadata, 0, len(records))
	for _, record := range records {
		if record.Provider != secretsManagerProvider {
			continue
		}
		result = append(result, Metadata{
			ProjectID: record.ProjectID,
			Name:      record.SecretName,
			Provider:  record.Provider,
			Writable:  s.Writable(),
			CreatedAt: record.CreatedAt,
			UpdatedAt: record.UpdatedAt,
			CreatedBy: record.CreatedBy,
		})
	}
	return result, nil
}

// call issues one SigV4-signed Secrets Manager JSON request. A
// ResourceNotFoundException is translated to ErrNotFound; every other failure
// reports the status and the safe X-Amzn-ErrorType header, never the body.
func (s *AwsSecretsManagerStore) call(ctx context.Context, operation string, payload map[string]any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	defer clear(body)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-amz-json-1.1")
	request.Header.Set("X-Amz-Target", "secretsmanager."+operation)

	credentials, err := s.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, errors.New("authenticate AWS Secrets Manager secret store")
	}
	digest := sha256.Sum256(body)
	if err := s.Signer.SignHTTP(ctx, credentials, request, hex.EncodeToString(digest[:]),
		secretsManagerSigningService, s.Region, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("sign AWS Secrets Manager %s", operation)
	}

	response, err := s.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("AWS Secrets Manager %s: %w", operation, err)
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, secretsManagerResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(result) > secretsManagerResponseLimit {
		clear(result)
		return nil, errors.New("AWS Secrets Manager response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errorType := strings.TrimSpace(response.Header.Get("X-Amzn-ErrorType"))
		if errorType == "" {
			// The type is also carried in the body's __type field; read only
			// that one field rather than surfacing the document, which for
			// PutSecretValue would echo the secret value.
			var typed struct {
				Type string `json:"__type"`
			}
			_ = json.Unmarshal(result, &typed)
			errorType = strings.TrimSpace(typed.Type)
		}
		clear(result)
		if strings.Contains(errorType, "ResourceNotFoundException") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("AWS Secrets Manager %s returned %d (%s)", operation, response.StatusCode, errorType)
	}
	return result, nil
}

func (s *AwsSecretsManagerStore) configured() bool {
	return s != nil && s.Region != "" && s.Endpoint != "" && s.Credentials != nil && s.Signer != nil && s.Client != nil
}
