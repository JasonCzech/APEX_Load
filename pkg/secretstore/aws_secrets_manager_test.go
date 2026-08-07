package secretstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func awsTestCredentials() aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "")
}

func newSecretsManagerStore(t *testing.T, handler http.HandlerFunc) *AwsSecretsManagerStore {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &AwsSecretsManagerStore{
		Region:      "us-east-1",
		Endpoint:    server.URL + "/",
		Credentials: awsTestCredentials(),
		Signer:      v4.NewSigner(),
		Client:      server.Client(),
	}
}

func secretsManagerTarget(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "secretsmanager.")
}

func TestSecretsManagerEndpointMustBeAnAllowedAWSHost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		wantErr  bool
		want     string
	}{
		{name: "empty derives the regional endpoint", endpoint: "", want: "https://secretsmanager.us-east-1.amazonaws.com/"},
		{name: "regional override", endpoint: "https://secretsmanager.us-east-1.amazonaws.com", want: "https://secretsmanager.us-east-1.amazonaws.com/"},
		{name: "fips endpoint", endpoint: "https://secretsmanager-fips.us-east-1.amazonaws.com", want: "https://secretsmanager-fips.us-east-1.amazonaws.com/"},
		{name: "china partition", endpoint: "https://secretsmanager.cn-north-1.amazonaws.com.cn", want: "https://secretsmanager.cn-north-1.amazonaws.com.cn/"},

		{name: "suffix impersonation", endpoint: "https://secretsmanager.us-east-1.amazonaws.com.evil.example", wantErr: true},
		{name: "unrelated host", endpoint: "https://attacker.example", wantErr: true},
		{name: "plaintext", endpoint: "http://secretsmanager.us-east-1.amazonaws.com", wantErr: true},
		{name: "carries a path", endpoint: "https://secretsmanager.us-east-1.amazonaws.com/collect", wantErr: true},
		{name: "embeds credentials", endpoint: "https://user:pass@secretsmanager.us-east-1.amazonaws.com", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSecretsManagerEndpoint(tc.endpoint, "us-east-1")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveSecretsManagerEndpoint(%q) = %q, want an error", tc.endpoint, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSecretsManagerEndpoint(%q) = %v", tc.endpoint, err)
			}
			if got != tc.want {
				t.Fatalf("resolveSecretsManagerEndpoint(%q) = %q, want %q", tc.endpoint, got, tc.want)
			}
		})
	}
}

func TestSecretsManagerCreateIsUnsupported(t *testing.T) {
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("Create reached AWS; the database provider stays authoritative for new values")
	})
	store.Metadata = &failingMetadataReplaceStore{}
	_, err := store.Create(context.Background(), "project-1", "TOKEN", "value", "actor")
	if !errors.Is(err, ErrCreateUnsupported) {
		t.Fatalf("Create error = %v, want ErrCreateUnsupported", err)
	}
	if store.CreateSupported() {
		t.Error("CreateSupported() = true")
	}
}

func TestSecretsManagerGetReturnsTheSecretString(t *testing.T) {
	var sawSecretID, sawTarget string
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		sawTarget = secretsManagerTarget(r)
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		_ = json.Unmarshal(body, &request)
		sawSecretID, _ = request["SecretId"].(string)
		_ = json.NewEncoder(w).Encode(map[string]string{"SecretString": "the-value"})
	})

	value, metadata, err := store.Get(context.Background(), "project-1", "TOKEN")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if value != "the-value" {
		t.Fatalf("Get = %q, want the-value", value)
	}
	if sawTarget != "GetSecretValue" {
		t.Errorf("X-Amz-Target = %q, want GetSecretValue", sawTarget)
	}
	if want := engine.ProjectSecretBindingName("project-1", "TOKEN"); sawSecretID != want {
		t.Errorf("SecretId = %q, want the deterministic binding name %q", sawSecretID, want)
	}
	if metadata.Provider != secretsManagerProvider {
		t.Errorf("Provider = %q, want %q", metadata.Provider, secretsManagerProvider)
	}
}

// TestSecretsManagerGetRefusesBinarySecrets pins that a binary value is
// reported by name rather than silently reinterpreted as text, which would
// hand back a corrupted credential.
func TestSecretsManagerGetRefusesBinarySecrets(t *testing.T) {
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"SecretBinary": "AAECAw=="})
	})
	_, _, err := store.Get(context.Background(), "project-1", "TOKEN")
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("Get error = %v, want an explicit binary-secret refusal", err)
	}
}

func TestSecretsManagerTranslatesResourceNotFound(t *testing.T) {
	for _, tc := range []struct {
		name      string
		viaHeader bool
	}{
		{name: "error type in the header", viaHeader: true},
		{name: "error type in the __type body field", viaHeader: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.viaHeader {
					w.Header().Set("X-Amzn-ErrorType", "ResourceNotFoundException:")
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"__type":"ResourceNotFoundException","message":"Secrets Manager can't find it"}`))
			})
			if _, _, err := store.Get(context.Background(), "project-1", "TOKEN"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get error = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestSecretsManagerNeverSurfacesResponseBody guards the leak channel: a
// PutSecretValue error document can echo the request, which carries the secret.
func TestSecretsManagerNeverSurfacesResponseBody(t *testing.T) {
	const secret = "SUPER-SECRET-VALUE"
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amzn-ErrorType", "InvalidRequestException")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad","echo":{"SecretString":"` + secret + `"}}`))
	})
	_, err := store.write(context.Background(), "project-1", "TOKEN", secret, "actor")
	if err == nil {
		t.Fatal("write = nil error for a 400 response")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("write error leaked the secret value: %s", err)
	}
	if !strings.Contains(err.Error(), "InvalidRequestException") {
		t.Errorf("write error = %q, want the safe error type", err)
	}
}

func TestSecretsManagerRequestIsSigV4Signed(t *testing.T) {
	var authorization, contentType string
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		_ = json.NewEncoder(w).Encode(map[string]string{"SecretString": "v"})
	})
	if _, _, err := store.Get(context.Background(), "project-1", "TOKEN"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("Authorization = %q, want a SigV4 signature", authorization)
	}
	if !strings.Contains(authorization, "/us-east-1/secretsmanager/aws4_request") {
		t.Errorf("Authorization = %q, want the secretsmanager scope", authorization)
	}
	if contentType != "application/x-amz-json-1.1" {
		t.Errorf("Content-Type = %q, want application/x-amz-json-1.1", contentType)
	}
}

// TestSecretsManagerDiscoveryUsesDescribeSecret pins the value-free probe.
// The Key Vault path has to issue a full read and discard the body because that
// API has no metadata-only operation; Secrets Manager does, and using it means
// no secret value ever crosses the wire during discovery.
func TestSecretsManagerDiscoveryUsesDescribeSecret(t *testing.T) {
	var targets []string
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		targets = append(targets, secretsManagerTarget(r))
		_ = json.NewEncoder(w).Encode(map[string]string{"Name": "apexload-project-1-token"})
	})
	store.Metadata = &recordingMetadataStore{}

	if err := store.DiscoverManagedMetadata(context.Background(), "project-1", []string{"TOKEN", "TOKEN", "OTHER"}); err != nil {
		t.Fatalf("DiscoverManagedMetadata: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("issued %d probes for 3 names with one duplicate, want 2: %v", len(targets), targets)
	}
	for _, target := range targets {
		if target != "DescribeSecret" {
			t.Fatalf("probe target = %q, want DescribeSecret (a value-free operation)", target)
		}
	}
}

func TestSecretsManagerDiscoveryHonorsTheNameCap(t *testing.T) {
	var probes int
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		probes++
		_ = json.NewEncoder(w).Encode(map[string]string{"Name": "x"})
	})
	store.Metadata = &recordingMetadataStore{}

	names := make([]string, 0, secretsManagerDiscoveryLimit+50)
	for i := 0; i < secretsManagerDiscoveryLimit+50; i++ {
		names = append(names, "SECRET_"+strings.Repeat("A", 1+i%20)+string(rune('a'+i%26))+itoa(i))
	}
	if err := store.DiscoverManagedMetadata(context.Background(), "project-1", names); err != nil {
		t.Fatalf("DiscoverManagedMetadata: %v", err)
	}
	if probes != secretsManagerDiscoveryLimit {
		t.Fatalf("probes = %d, want the %d cap", probes, secretsManagerDiscoveryLimit)
	}
}

// TestProjectSecretBindingNameIsLegalForSecretsManager verifies rather than
// assumes: ProjectSecretBindingName normalizes for Azure Key Vault's charset,
// and its output happens to also satisfy Secrets Manager's, which is narrower
// in some respects (512 chars, [A-Za-z0-9/_+=.@-]).
func TestProjectSecretBindingNameIsLegalForSecretsManager(t *testing.T) {
	legal := regexp.MustCompile(`^[A-Za-z0-9/_+=.@-]+$`)
	for _, name := range []string{
		"TOKEN",
		"api key",
		"weird!!name??",
		strings.Repeat("LONG", 60),
		"ünïcøde",
		"-leading-dash",
	} {
		physical := engine.ProjectSecretBindingName("project-1", name)
		if physical == "" {
			t.Errorf("ProjectSecretBindingName(%q) = empty", name)
			continue
		}
		if len(physical) > 512 {
			t.Errorf("ProjectSecretBindingName(%q) = %d chars, Secrets Manager allows 512", name, len(physical))
		}
		if !legal.MatchString(physical) {
			t.Errorf("ProjectSecretBindingName(%q) = %q, which Secrets Manager would reject", name, physical)
		}
	}
}

func TestSecretsManagerReplacePreservesMetadataCommitAmbiguity(t *testing.T) {
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Name": "x", "VersionId": "v1"})
	})
	records := &recordingMetadataStore{provider: secretsManagerProvider}
	store.Metadata = &failingMetadataReplaceStore{ProjectSecretMetadataStore: records}

	_, err := store.Replace(context.Background(), "project-1", "TOKEN", "new-value", "actor")
	if !errors.Is(err, ErrMetadataCommit) {
		t.Fatalf("Replace error = %v, want ErrMetadataCommit so the API re-verifies", err)
	}
}

func TestSecretsManagerReplaceRefusesAnotherProvidersRecord(t *testing.T) {
	store := newSecretsManagerStore(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("Replace reached AWS for a record owned by another provider")
	})
	store.Metadata = &recordingMetadataStore{provider: "azure_key_vault"}

	_, err := store.Replace(context.Background(), "project-1", "TOKEN", "new-value", "actor")
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Replace error = %v, want ErrReadOnly", err)
	}
}

// recordingMetadataStore is an in-memory ProjectSecretMetadataStore. It keeps
// these tests focused on the AWS wire behavior rather than on SQLite.
type recordingMetadataStore struct {
	provider string
	records  map[string]*storage.ProjectSecretMetadata
	claims   int
}

func (s *recordingMetadataStore) key(projectID, name string) string { return projectID + "\x00" + name }

func (s *recordingMetadataStore) CreateProjectSecretMetadata(metadata *storage.ProjectSecretMetadata) (bool, error) {
	if s.records == nil {
		s.records = map[string]*storage.ProjectSecretMetadata{}
	}
	key := s.key(metadata.ProjectID, metadata.SecretName)
	if _, exists := s.records[key]; exists {
		return false, nil
	}
	s.claims++
	stored := *metadata
	s.records[key] = &stored
	return true, nil
}

func (s *recordingMetadataStore) ReplaceProjectSecretMetadata(metadata *storage.ProjectSecretMetadata) (bool, error) {
	if s.records == nil {
		s.records = map[string]*storage.ProjectSecretMetadata{}
	}
	stored := *metadata
	s.records[s.key(metadata.ProjectID, metadata.SecretName)] = &stored
	return true, nil
}

func (s *recordingMetadataStore) GetProjectSecretMetadata(projectID, secretName string) (*storage.ProjectSecretMetadata, error) {
	if record, ok := s.records[s.key(projectID, secretName)]; ok {
		return record, nil
	}
	// A configured provider means "this name is already externally owned",
	// which is what the Replace-path tests need.
	if s.provider != "" {
		return &storage.ProjectSecretMetadata{ProjectID: projectID, SecretName: secretName, Provider: s.provider}, nil
	}
	return nil, ErrNotFound
}

func (s *recordingMetadataStore) ListExternalProjectSecretMetadata(string) ([]storage.ProjectSecretMetadata, error) {
	return nil, nil
}

func (s *recordingMetadataStore) DeleteProjectSecretMetadata(string, string) error { return nil }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}
