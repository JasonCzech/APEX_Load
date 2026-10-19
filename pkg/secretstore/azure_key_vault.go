package secretstore

import (
	"bytes"
	"context"
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
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	keyVaultProvider         = "azure_key_vault"
	keyVaultDefaultAPIVerion = "7.4"
	keyVaultResponseLimit    = 1 << 20
	keyVaultDiscoveryLimit   = 256
)

type AzureKeyVaultStore struct {
	BaseURL    string
	APIVersion string
	Credential azcore.TokenCredential
	Client     *http.Client
	Metadata   storage.ProjectSecretMetadataStore
}

func NewAzureKeyVaultStoreFromEnv() (*AzureKeyVaultStore, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED")), "true") {
		return nil, nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_URL")), "/")
	if baseURL == "" {
		return nil, errors.New("APEXLOAD_AZURE_KEY_VAULT_URL is required when Key Vault writes are enabled")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Azure Key Vault URL must be an HTTPS vault origin")
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	apiVersion := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_API_VERSION"))
	if apiVersion == "" {
		apiVersion = keyVaultDefaultAPIVerion
	}
	return &AzureKeyVaultStore{
		BaseURL:    baseURL,
		APIVersion: apiVersion,
		Credential: credential,
		Client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (s *AzureKeyVaultStore) Writable() bool {
	return s.configured() && s.Metadata != nil
}

func (*AzureKeyVaultStore) Provider() string      { return keyVaultProvider }
func (*AzureKeyVaultStore) CreateSupported() bool { return false }

func (s *AzureKeyVaultStore) Create(context.Context, string, string, string, string) (Metadata, error) {
	if !s.Writable() {
		return Metadata{}, ErrReadOnly
	}
	// Key Vault's Set Secret operation is an upsert: if a name appears between
	// any existence probe and PUT, the PUT creates a new version and supersedes
	// the external value. The data-plane API has no conditional create operation,
	// so imports must fail closed. Administrators can provision the name in Key
	// Vault and use Replace only for explicit rotations of APEX-managed metadata.
	return Metadata{}, ErrCreateUnsupported
}

func (s *AzureKeyVaultStore) Replace(ctx context.Context, projectID, name, value, actor string) (Metadata, error) {
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
	if existing.Provider != keyVaultProvider {
		return Metadata{}, ErrReadOnly
	}
	metadata, err := s.write(ctx, projectID, name, value, actor)
	if err != nil {
		return Metadata{}, err
	}
	claim := &storage.ProjectSecretMetadata{
		ProjectID:  projectID,
		SecretName: name,
		Provider:   keyVaultProvider,
		CreatedAt:  existing.CreatedAt,
		UpdatedAt:  metadata.UpdatedAt,
		CreatedBy:  strings.TrimSpace(actor),
	}
	replaced, err := s.Metadata.ReplaceProjectSecretMetadata(claim)
	if err != nil {
		// The remote Set Secret already succeeded. Preserve a distinct commit
		// ambiguity so the API can verify both the value and this metadata claim
		// before acknowledging the rotation.
		return Metadata{}, fmt.Errorf("%w: %v", ErrMetadataCommit, err)
	}
	if !replaced {
		return Metadata{}, ErrMetadataCommit
	}
	metadata.CreatedAt = existing.CreatedAt
	return metadata, nil
}

func (s *AzureKeyVaultStore) write(ctx context.Context, projectID, name, value, actor string) (Metadata, error) {
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
	body, err := json.Marshal(map[string]any{
		"value": value,
		"tags": map[string]string{
			"apexload-managed": "true",
		},
	})
	if err != nil {
		return Metadata{}, err
	}
	defer clear(body)
	response, err := s.request(ctx, http.MethodPut, projectID, name, bytes.NewReader(body))
	if err != nil {
		return Metadata{}, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, keyVaultResponseLimit+1))
	if err != nil {
		return Metadata{}, err
	}
	defer clear(payload)
	if len(payload) > keyVaultResponseLimit {
		return Metadata{}, errors.New("Azure Key Vault response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Metadata{}, fmt.Errorf("Azure Key Vault secret write returned %d", response.StatusCode)
	}
	// The metadata record stores this value, and the dashboard compares the
	// returned metadata with the listed one; PostgreSQL keeps microseconds.
	now := time.Now().UTC().Truncate(time.Microsecond)
	return Metadata{
		ProjectID: projectID,
		Name:      name,
		Provider:  keyVaultProvider,
		Writable:  true,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: strings.TrimSpace(actor),
	}, nil
}

func (s *AzureKeyVaultStore) Get(ctx context.Context, projectID, name string) (string, Metadata, error) {
	if !s.configured() {
		return "", Metadata{}, ErrNotFound
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return "", Metadata{}, err
	}
	response, err := s.request(ctx, http.MethodGet, projectID, name, nil)
	if err != nil {
		return "", Metadata{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", Metadata{}, ErrNotFound
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, keyVaultResponseLimit+1))
	if err != nil {
		return "", Metadata{}, err
	}
	defer clear(payload)
	if len(payload) > keyVaultResponseLimit {
		return "", Metadata{}, errors.New("Azure Key Vault response exceeds limit")
	}
	if response.StatusCode != http.StatusOK {
		return "", Metadata{}, fmt.Errorf("Azure Key Vault secret read returned %d", response.StatusCode)
	}
	var decoded struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Value == "" {
		return "", Metadata{}, errors.New("Azure Key Vault returned no secret value")
	}
	if s.Metadata == nil {
		return decoded.Value, Metadata{
			ProjectID: projectID,
			Name:      name,
			Provider:  keyVaultProvider,
		}, nil
	}
	if _, err := s.claimManagedMetadata(projectID, name, time.Now().UTC()); err != nil {
		decoded.Value = ""
		return "", Metadata{}, err
	}
	managed, err := s.Metadata.GetProjectSecretMetadata(projectID, name)
	if err != nil {
		decoded.Value = ""
		return "", Metadata{}, err
	}
	return decoded.Value, Metadata{
		ProjectID: managed.ProjectID,
		Name:      managed.SecretName,
		Provider:  managed.Provider,
		Writable:  managed.Provider == keyVaultProvider && s.Writable(),
		CreatedAt: managed.CreatedAt,
		UpdatedAt: managed.UpdatedAt,
		CreatedBy: managed.CreatedBy,
	}, nil
}

// DiscoverManagedMetadata probes only caller-supplied deterministic names and
// records value-free lifecycle metadata for existing Key Vault secrets. The
// response body is streamed to discard, so provider values never enter an API
// response or an application-level string.
func (s *AzureKeyVaultStore) DiscoverManagedMetadata(ctx context.Context, projectID string, names []string) error {
	if !s.configured() || s.Metadata == nil {
		return nil
	}
	ctx, cancel := engine.WithSecretResolutionBudget(ctx)
	defer cancel()
	uniqueNames := make([]string, 0, min(len(names), keyVaultDiscoveryLimit))
	seen := make(map[string]struct{}, min(len(names), keyVaultDiscoveryLimit))
	for _, rawName := range names {
		normalizedProjectID, name, err := validateIdentity(projectID, rawName)
		if err != nil {
			return err
		}
		projectID = normalizedProjectID
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		if len(uniqueNames) == keyVaultDiscoveryLimit {
			break
		}
		seen[name] = struct{}{}
		uniqueNames = append(uniqueNames, name)
	}
	for _, name := range uniqueNames {
		response, err := s.request(ctx, http.MethodGet, projectID, name, nil)
		if err != nil {
			return err
		}
		read, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, keyVaultResponseLimit+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if read > keyVaultResponseLimit {
			return errors.New("Azure Key Vault response exceeds limit")
		}
		if response.StatusCode == http.StatusNotFound {
			continue
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("Azure Key Vault secret probe returned %d", response.StatusCode)
		}
		if _, err := s.claimManagedMetadata(projectID, name, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (s *AzureKeyVaultStore) claimManagedMetadata(projectID, name string, now time.Time) (bool, error) {
	if s.Metadata == nil {
		return false, nil
	}
	claim := &storage.ProjectSecretMetadata{
		ProjectID:  projectID,
		SecretName: name,
		Provider:   keyVaultProvider,
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
	if existing.Provider != keyVaultProvider {
		return false, ErrReadOnly
	}
	return true, nil
}

func (s *AzureKeyVaultStore) List(ctx context.Context, projectID string) ([]Metadata, error) {
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
		if record.Provider != keyVaultProvider {
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

func (s *AzureKeyVaultStore) request(ctx context.Context, method, projectID, name string, body io.Reader) (*http.Response, error) {
	token, err := s.Credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://vault.azure.net/.default"},
	})
	if err != nil {
		return nil, errors.New("authenticate Azure Key Vault secret store")
	}
	physicalName := engine.ProjectSecretBindingName(projectID, name)
	endpoint := s.BaseURL + "/secrets/" + url.PathEscape(physicalName) + "?api-version=" + url.QueryEscape(s.APIVersion)
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token.Token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return s.Client.Do(request)
}

func (s *AzureKeyVaultStore) configured() bool {
	return s != nil && s.BaseURL != "" && s.Credential != nil && s.Client != nil
}
