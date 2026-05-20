package engine

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type SecretResolver interface {
	Resolve(name string) (string, bool)
}

type SecretResolverFunc func(name string) (string, bool)

func (fn SecretResolverFunc) Resolve(name string) (string, bool) {
	return fn(name)
}

type cachingSecretResolver struct {
	delegate SecretResolver
	mu       sync.Mutex
	values   map[string]string
	missing  map[string]struct{}
}

func newCachingSecretResolver(delegate SecretResolver) SecretResolver {
	if delegate == nil {
		return nil
	}
	return &cachingSecretResolver{
		delegate: delegate,
		values:   make(map[string]string),
		missing:  make(map[string]struct{}),
	}
}

func (r *cachingSecretResolver) Resolve(name string) (string, bool) {
	r.mu.Lock()
	if value, ok := r.values[name]; ok {
		r.mu.Unlock()
		return value, true
	}
	if _, ok := r.missing[name]; ok {
		r.mu.Unlock()
		return "", false
	}
	r.mu.Unlock()

	value, ok := r.delegate.Resolve(name)

	r.mu.Lock()
	defer r.mu.Unlock()
	if ok {
		r.values[name] = value
		return value, true
	}
	r.missing[name] = struct{}{}
	return "", false
}

type chainedSecretResolver struct {
	delegates []SecretResolver
}

func newChainedSecretResolver(delegates ...SecretResolver) SecretResolver {
	filtered := make([]SecretResolver, 0, len(delegates))
	for _, delegate := range delegates {
		if delegate != nil {
			filtered = append(filtered, delegate)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return chainedSecretResolver{delegates: filtered}
}

func (r chainedSecretResolver) Resolve(name string) (string, bool) {
	for _, delegate := range r.delegates {
		if value, ok := delegate.Resolve(name); ok {
			return value, true
		}
	}
	return "", false
}

type envSecretResolver struct{}

func (envSecretResolver) Resolve(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	if value, ok := os.LookupEnv(name); ok {
		return value, true
	}
	normalized := strings.NewReplacer(".", "_", "-", "_").Replace(strings.ToUpper(name))
	value, ok := os.LookupEnv("APEXLOAD_SECRET_" + normalized)
	return value, ok
}

type azureKeyVaultSecretResolver struct {
	baseURL    string
	token      string
	apiVersion string
	client     *http.Client
}

type azureKeyVaultSecretResponse struct {
	Value string `json:"value"`
}

func newAzureKeyVaultSecretResolverFromEnv() SecretResolver {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_URL")), "/")
	if baseURL == "" {
		return nil
	}
	token := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("AZURE_KEY_VAULT_TOKEN"))
	}
	if token == "" {
		return nil
	}
	apiVersion := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_API_VERSION"))
	if apiVersion == "" {
		apiVersion = "7.4"
	}
	return &azureKeyVaultSecretResolver{
		baseURL:    baseURL,
		token:      token,
		apiVersion: apiVersion,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

func (r *azureKeyVaultSecretResolver) Resolve(name string) (string, bool) {
	if r == nil || name == "" {
		return "", false
	}
	endpoint, err := url.Parse(r.baseURL + "/secrets/" + url.PathEscape(normalizeAzureKeyVaultSecretName(name)))
	if err != nil {
		return "", false
	}
	query := endpoint.Query()
	query.Set("api-version", r.apiVersion)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+r.token)

	resp, err := r.client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", false
	}
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	var payload azureKeyVaultSecretResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", false
	}
	if payload.Value == "" {
		return "", false
	}
	return payload.Value, true
}

func normalizeAzureKeyVaultSecretName(name string) string {
	return strings.NewReplacer("_", "-", ".", "-").Replace(name)
}

func newDefaultSecretResolver() SecretResolver {
	return newCachingSecretResolver(newChainedSecretResolver(
		envSecretResolver{},
		newAzureKeyVaultSecretResolverFromEnv(),
	))
}

func resolveSecretTemplateValue(name string, resolver SecretResolver) (string, bool) {
	if resolver == nil {
		return "", false
	}
	return resolver.Resolve(strings.TrimSpace(name))
}

func interpolateSecretReferences(input string, resolver SecretResolver) string {
	if input == "" {
		return input
	}
	return templatePattern.ReplaceAllStringFunc(input, func(token string) string {
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if !strings.HasPrefix(expr, "secret.") {
			return token
		}
		if value, ok := resolveSecretTemplateValue(strings.TrimPrefix(expr, "secret."), resolver); ok {
			return value
		}
		return token
	})
}

func resolveProtoConfigSecrets(value any, resolver SecretResolver) any {
	switch typed := value.(type) {
	case string:
		return interpolateSecretReferences(typed, resolver)
	case []string:
		resolved := make([]string, len(typed))
		for i := range typed {
			resolved[i] = interpolateSecretReferences(typed[i], resolver)
		}
		return resolved
	case map[string]string:
		resolved := make(map[string]string, len(typed))
		for key, item := range typed {
			resolved[key] = interpolateSecretReferences(item, resolver)
		}
		return resolved
	case []map[string]any:
		resolved := make([]map[string]any, len(typed))
		for i := range typed {
			resolved[i] = resolveProtoConfigSecrets(typed[i], resolver).(map[string]any)
		}
		return resolved
	case map[string]any:
		resolved := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved[key] = resolveProtoConfigSecrets(item, resolver)
		}
		return resolved
	default:
		return value
	}
}
