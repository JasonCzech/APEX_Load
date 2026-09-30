package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type SecretResolver interface {
	Resolve(name string) (string, bool)
}

// ContextSecretResolver is the optional cancellation-aware form used by
// launch/preflight batch resolution. Implementations should stop provider work
// when ctx is done so one overall budget applies across every referenced name.
type ContextSecretResolver interface {
	SecretResolver
	ResolveContext(ctx context.Context, name string) (string, bool)
}

type SecretResolverFunc func(name string) (string, bool)

func (fn SecretResolverFunc) Resolve(name string) (string, bool) {
	return fn(name)
}

type ContextSecretResolverFunc func(context.Context, string) (string, bool)

func (fn ContextSecretResolverFunc) Resolve(name string) (string, bool) {
	return fn(context.Background(), name)
}

func (fn ContextSecretResolverFunc) ResolveContext(ctx context.Context, name string) (string, bool) {
	return fn(ctx, name)
}

type cachingSecretResolver struct {
	delegate    SecretResolver
	mu          sync.Mutex
	values      map[string]string
	missing     map[string]time.Time
	inflight    map[string]*secretResolutionFlight
	order       []string
	bytes       int
	now         func() time.Time
	negativeTTL time.Duration
}

const (
	maxSecretNameBytes             = 256
	maxResolvedSecretValueBytes    = 64 << 10
	maxCachedSecrets               = 1024
	maxCachedSecretBytes           = 4 << 20
	maxAzureKeyVaultResponseBytes  = 1 << 20
	maxAzureKeyVaultAllowedNames   = 1024
	secretNegativeCacheTTL         = 5 * time.Second
	defaultSecretResolutionBudget  = 10 * time.Second
	maxConcurrentSecretResolutions = 32
)

var secretResolutionSlots = make(chan struct{}, maxConcurrentSecretResolutions)

type secretResolutionFlight struct {
	done  chan struct{}
	value string
	ok    bool
}

func newCachingSecretResolver(delegate SecretResolver) SecretResolver {
	if delegate == nil {
		return nil
	}
	if _, ok := delegate.(*cachingSecretResolver); ok {
		return delegate
	}
	return &cachingSecretResolver{
		delegate:    delegate,
		values:      make(map[string]string),
		missing:     make(map[string]time.Time),
		inflight:    make(map[string]*secretResolutionFlight),
		now:         time.Now,
		negativeTTL: secretNegativeCacheTTL,
	}
}

// CacheSecretResolverForRun adds bounded, concurrency-safe positive and
// negative caching to an externally supplied resolver. Runtime entry points
// call this before resolving script references so database and external
// providers are not queried once per VUser/action. An already cached resolver
// is returned unchanged.
func CacheSecretResolverForRun(delegate SecretResolver) SecretResolver {
	return newCachingSecretResolver(delegate)
}

func (r *cachingSecretResolver) Resolve(name string) (string, bool) {
	return r.ResolveContext(context.Background(), name)
}

func (r *cachingSecretResolver) ResolveContext(ctx context.Context, name string) (string, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxSecretNameBytes {
		return "", false
	}
	r.mu.Lock()
	if value, ok := r.values[name]; ok {
		r.mu.Unlock()
		return value, true
	}
	if expiresAt, ok := r.missing[name]; ok {
		if r.nowTime().Before(expiresAt) {
			r.mu.Unlock()
			return "", false
		}
		delete(r.missing, name)
		r.bytes -= len(name)
		r.removeOrderLocked(name)
	}
	if flight := r.inflight[name]; flight != nil {
		r.mu.Unlock()
		select {
		case <-flight.done:
			return flight.value, flight.ok
		case <-ctx.Done():
			return "", false
		}
	}
	if len(r.inflight) >= maxCachedSecrets {
		r.mu.Unlock()
		return "", false
	}
	flight := &secretResolutionFlight{done: make(chan struct{})}
	r.inflight[name] = flight
	r.mu.Unlock()

	value, ok := resolveSecretWithContext(ctx, r.delegate, name)
	if len(value) > maxResolvedSecretValueBytes {
		value = ""
		ok = false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.admitLocked(name, value, ok)
	flight.value = value
	flight.ok = ok
	delete(r.inflight, name)
	close(flight.done)
	return value, ok
}

func resolveSecretWithContext(ctx context.Context, resolver SecretResolver, name string) (string, bool) {
	if resolver == nil {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if contextual, ok := resolver.(ContextSecretResolver); ok {
		value, resolved := contextual.ResolveContext(ctx, name)
		if !resolved || !IsUsableResolvedSecretValue(value) {
			return "", false
		}
		return value, true
	}
	select {
	case secretResolutionSlots <- struct{}{}:
	case <-ctx.Done():
		return "", false
	}
	result := make(chan secretResolutionFlight, 1)
	go func() {
		value, ok := resolver.Resolve(name)
		<-secretResolutionSlots
		result <- secretResolutionFlight{value: value, ok: ok}
	}()
	select {
	case resolved := <-result:
		if !resolved.ok || !IsUsableResolvedSecretValue(resolved.value) {
			return "", false
		}
		return resolved.value, true
	case <-ctx.Done():
		return "", false
	}
}

// IsUsableResolvedSecretValue applies the value-level runtime invariant shared
// by managed stores and external providers. A canonical redaction sentinel is
// evidence that the source value was not recoverable; it must never become an
// executable credential.
func IsUsableResolvedSecretValue(value string) bool {
	return value != "" && !strings.EqualFold(strings.TrimSpace(value), "[REDACTED]")
}

// ResolveSecret applies cancellation to both context-aware providers and
// legacy resolver implementations. Legacy calls are isolated behind a bounded
// process-wide gate so a faulty resolver cannot create unbounded goroutines.
func ResolveSecret(ctx context.Context, resolver SecretResolver, name string) (string, bool) {
	return resolveSecretWithContext(ctx, resolver, name)
}

func (r *cachingSecretResolver) admitLocked(name, value string, ok bool) {
	entryBytes := len(name)
	if ok {
		entryBytes += len(value)
	}
	if entryBytes > maxCachedSecretBytes {
		return
	}
	for len(r.values)+len(r.missing) >= maxCachedSecrets || r.bytes+entryBytes > maxCachedSecretBytes {
		if len(r.order) == 0 {
			return
		}
		victim := r.order[0]
		r.order[0] = ""
		r.order = r.order[1:]
		if cached, exists := r.values[victim]; exists {
			delete(r.values, victim)
			r.bytes -= len(victim) + len(cached)
		} else if _, exists := r.missing[victim]; exists {
			delete(r.missing, victim)
			r.bytes -= len(victim)
		}
	}
	if ok {
		r.values[name] = value
	} else {
		r.missing[name] = r.nowTime().Add(r.negativeTTL)
	}
	r.order = append(r.order, name)
	r.bytes += entryBytes
}

func (r *cachingSecretResolver) nowTime() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *cachingSecretResolver) removeOrderLocked(name string) {
	for index := 0; index < len(r.order); {
		if r.order[index] != name {
			index++
			continue
		}
		copy(r.order[index:], r.order[index+1:])
		r.order[len(r.order)-1] = ""
		r.order = r.order[:len(r.order)-1]
	}
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
	return r.ResolveContext(context.Background(), name)
}

func (r chainedSecretResolver) ResolveContext(ctx context.Context, name string) (string, bool) {
	for _, delegate := range r.delegates {
		if value, ok := resolveSecretWithContext(ctx, delegate, name); ok {
			return value, true
		}
		if ctx.Err() != nil {
			return "", false
		}
	}
	return "", false
}

type envSecretResolver struct{}

func (envSecretResolver) Resolve(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	// Uploaded scripts must not be able to turn {{secret.NAME}} into an
	// arbitrary process-environment lookup. The generator environment also
	// contains platform credentials (gRPC bearer tokens, OIDC secrets, cloud
	// identity settings, database DSNs, and projected-token paths). Operators
	// expose a value to the script secret store only by placing it in the
	// dedicated APEXLOAD_SECRET_* namespace.
	value, ok := os.LookupEnv(scriptSecretEnvironmentName(name))
	if !ok || !IsUsableResolvedSecretValue(value) {
		return "", false
	}
	return value, true
}

func (r envSecretResolver) ResolveContext(ctx context.Context, name string) (string, bool) {
	if ctx != nil && ctx.Err() != nil {
		return "", false
	}
	return r.Resolve(name)
}

func scriptSecretEnvironmentName(name string) string {
	normalized := strings.NewReplacer(".", "_", "-", "_").Replace(strings.ToUpper(strings.TrimSpace(name)))
	return "APEXLOAD_SECRET_" + normalized
}

type projectScopedSecretResolver struct {
	projectID string
	delegate  SecretResolver
}

func (r projectScopedSecretResolver) Resolve(name string) (string, bool) {
	return r.ResolveContext(context.Background(), name)
}

func (r projectScopedSecretResolver) ResolveContext(ctx context.Context, name string) (string, bool) {
	if r.delegate == nil {
		return "", false
	}
	boundName := ProjectSecretBindingName(r.projectID, name)
	if boundName == "" {
		return "", false
	}
	return resolveSecretWithContext(ctx, r.delegate, boundName)
}

// ProjectSecretBindingName returns the operator-facing logical binding used by
// both the environment and Azure Key Vault resolvers. Project-bound scripts
// never fall back to the legacy global name. Projectless local CLI scripts keep
// the legacy binding for backwards compatibility.
func ProjectSecretBindingName(projectID, name string) string {
	name = strings.TrimSpace(name)
	projectID = strings.TrimSpace(projectID)
	if name == "" || projectID == "" {
		return name
	}
	prefix := ProjectSecretBindingPrefix(projectID)
	normalizedName := normalizeAzureKeyVaultSecretName(name)
	if validAzureKeyVaultSecretName(normalizedName) && len(prefix)+len(normalizedName) <= 127 {
		return prefix + normalizedName
	}
	nameHash := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%ssecret-%x", prefix, nameHash[:16])
}

// ProjectSecretBindingPrefix is the value-free prefix shared by every
// deterministic external-provider secret name for a project. It gives
// administrators actionable cleanup guidance even when no logical names were
// indexed before the project was deleted.
func ProjectSecretBindingPrefix(projectID string) string {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ""
	}
	projectHash := sha256.Sum256([]byte(projectID))
	return fmt.Sprintf("apexload-p-%x-", projectHash[:10])
}

// ProjectSecretEnvironmentName returns the exact environment-variable name an
// operator must configure for a project-bound script secret.
func ProjectSecretEnvironmentName(projectID, name string) string {
	return scriptSecretEnvironmentName(ProjectSecretBindingName(projectID, name))
}

// ScriptSecretEnvAvailable reports whether name is bound in the dedicated
// environment-backed script secret namespace. It does not consult arbitrary
// process environment variables and does not return the credential value.
func ScriptSecretEnvAvailable(name string) bool {
	_, ok := (envSecretResolver{}).Resolve(name)
	return ok
}

// ProjectScriptSecretEnvAvailable is the project-aware counterpart used by
// controller preflight checks. A non-empty project never probes the legacy
// global secret namespace.
func ProjectScriptSecretEnvAvailable(projectID, name string) bool {
	_, ok := (envSecretResolver{}).Resolve(ProjectSecretBindingName(projectID, name))
	return ok
}

// ProjectKeyVaultAllowlistConfigured reports whether the legacy read-only
// resolver has a valid operator allowlist.
func ProjectKeyVaultAllowlistConfigured() bool {
	if _, err := validateAzureKeyVaultBaseURL(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_URL")); err != nil {
		return false
	}
	allowed, err := azureKeyVaultAllowedSecretNamesFromEnv()
	return err == nil && len(allowed) > 0
}

// ProjectKeyVaultProjectAllowlisted reports whether any legacy read-only
// Key Vault binding belongs to the project's deterministic namespace.
func ProjectKeyVaultProjectAllowlisted(projectID string) bool {
	if strings.ContainsRune(projectID, '\x00') || !ProjectKeyVaultAllowlistConfigured() {
		return false
	}
	prefix := ProjectSecretBindingPrefix(projectID)
	if prefix == "" {
		return false
	}
	allowed, _ := azureKeyVaultAllowedSecretNamesFromEnv()
	for name := range allowed {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// ProjectKeyVaultBindingAllowlisted prevents a managed create from silently
// shadowing a name assigned to the legacy read-only Key Vault resolver. The
// remote value may be temporarily unavailable, so a failed read is not proof
// that the binding is free.
func ProjectKeyVaultBindingAllowlisted(projectID, name string) bool {
	if strings.TrimSpace(projectID) == "" || strings.ContainsRune(projectID, '\x00') || !ProjectKeyVaultAllowlistConfigured() {
		return false
	}
	normalizedName, err := NormalizeLogicalSecretName(name)
	if err != nil {
		return false
	}
	allowed, _ := azureKeyVaultAllowedSecretNamesFromEnv()
	physicalName := normalizeAzureKeyVaultSecretName(ProjectSecretBindingName(projectID, normalizedName))
	_, ok := allowed[physicalName]
	return ok
}

type azureKeyVaultSecretResolver struct {
	baseURL    string
	tokens     azureTokenSource
	apiVersion string
	client     *http.Client
}

type azureKeyVaultSecretResponse struct {
	Value string `json:"value"`
}

type allowlistedAzureKeyVaultSecretResolver struct {
	delegate SecretResolver
	allowed  map[string]struct{}
}

func (r allowlistedAzureKeyVaultSecretResolver) Resolve(name string) (string, bool) {
	return r.ResolveContext(context.Background(), name)
}

func (r allowlistedAzureKeyVaultSecretResolver) ResolveContext(ctx context.Context, name string) (string, bool) {
	secretName := normalizeAzureKeyVaultSecretName(name)
	if !validAzureKeyVaultSecretName(secretName) {
		return "", false
	}
	if _, ok := r.allowed[secretName]; !ok {
		return "", false
	}
	return resolveSecretWithContext(ctx, r.delegate, secretName)
}

func azureKeyVaultAllowedSecretNamesFromEnv() (map[string]struct{}, error) {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_ALLOWED_SECRETS"))
	if raw == "" {
		return nil, nil
	}
	allowed := make(map[string]struct{})
	for _, configuredName := range strings.Split(raw, ",") {
		configuredName = strings.TrimSpace(configuredName)
		if configuredName == "" {
			continue
		}
		secretName := normalizeAzureKeyVaultSecretName(configuredName)
		if !validAzureKeyVaultSecretName(secretName) {
			return nil, fmt.Errorf("invalid Azure Key Vault secret name %q", configuredName)
		}
		allowed[secretName] = struct{}{}
		if len(allowed) > maxAzureKeyVaultAllowedNames {
			return nil, fmt.Errorf("too many Azure Key Vault secret names (maximum %d)", maxAzureKeyVaultAllowedNames)
		}
	}
	return allowed, nil
}

func newAzureKeyVaultSecretResolverFromEnv() SecretResolver {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_URL")), "/")
	if baseURL == "" {
		return nil
	}
	allowed, err := azureKeyVaultAllowedSecretNamesFromEnv()
	if err != nil {
		slog.Warn("azure key vault resolver disabled: invalid secret allowlist", "error", scrubSensitiveError(err, nil))
		return nil
	}
	// A workload identity is commonly authorized for an entire shared vault.
	// Uploaded scripts are not allowed to turn that identity into a generic
	// secret-reading oracle: operators must explicitly expose each script-safe
	// name. Environment-backed APEXLOAD_SECRET_* values remain an independently
	// explicit, deployment-local secret namespace.
	if len(allowed) == 0 {
		return nil
	}
	validatedBaseURL, err := validateAzureKeyVaultBaseURL(baseURL)
	if err != nil {
		slog.Warn("azure key vault resolver disabled: invalid vault URL", "error", scrubSensitiveError(err, nil))
		return nil
	}
	tokens := azureKeyVaultTokenSourceFromEnv()
	if tokens == nil {
		return nil
	}
	apiVersion := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_API_VERSION"))
	if apiVersion == "" {
		apiVersion = "7.4"
	}
	resolver := &azureKeyVaultSecretResolver{
		baseURL:    validatedBaseURL,
		tokens:     tokens,
		apiVersion: apiVersion,
		client:     &http.Client{Timeout: 5 * time.Second, CheckRedirect: refuseAzureKeyVaultRedirect},
	}
	return allowlistedAzureKeyVaultSecretResolver{delegate: resolver, allowed: allowed}
}

func validateAzureKeyVaultBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" {
		return "", fmt.Errorf("vault URL must use HTTPS and include a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("vault URL must not contain userinfo, query, or fragment")
	}
	if parsed.EscapedPath() != "" && parsed.EscapedPath() != "/" {
		return "", fmt.Errorf("vault URL must not contain a path")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func refuseAzureKeyVaultRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// azureKeyVaultTokenSourceFromEnv picks the Key Vault auth mode:
//
//	APEXLOAD_AZURE_KEY_VAULT_AUTH=token          — static bearer token from env (legacy)
//	APEXLOAD_AZURE_KEY_VAULT_AUTH=azure_identity — DefaultAzureCredential (AKS workload
//	                                               identity, managed identity, env, CLI)
//	unset/auto                                   — static token when present, otherwise
//	                                               DefaultAzureCredential
//
// The token scope defaults to the public-cloud vault audience and can be
// overridden for sovereign clouds via APEXLOAD_AZURE_KEY_VAULT_SCOPE.
func azureKeyVaultTokenSourceFromEnv() azureTokenSource {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_AUTH")))
	staticToken := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_TOKEN"))
	if staticToken == "" {
		staticToken = strings.TrimSpace(os.Getenv("AZURE_KEY_VAULT_TOKEN"))
	}
	switch mode {
	case "token":
		if staticToken == "" {
			return nil
		}
		return staticAzureTokenSource(staticToken)
	case "azure_identity":
		return newAzureIdentityKeyVaultTokenSource()
	case "", "auto":
		if staticToken != "" {
			return staticAzureTokenSource(staticToken)
		}
		return newAzureIdentityKeyVaultTokenSource()
	default:
		slog.Warn("unknown APEXLOAD_AZURE_KEY_VAULT_AUTH mode, expected token|azure_identity|auto", "mode", mode)
		return nil
	}
}

func newAzureIdentityKeyVaultTokenSource() azureTokenSource {
	scope := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_SCOPE"))
	if scope == "" {
		scope = "https://vault.azure.net/.default"
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		slog.Warn("azure key vault resolver disabled: DefaultAzureCredential init failed", "error", scrubSensitiveError(err, nil))
		return nil
	}
	return newAzureCredentialTokenSource(credential, scope)
}

func (r *azureKeyVaultSecretResolver) Resolve(name string) (string, bool) {
	return r.ResolveContext(context.Background(), name)
}

func (r *azureKeyVaultSecretResolver) ResolveContext(parent context.Context, name string) (string, bool) {
	if r == nil || name == "" {
		return "", false
	}
	secretName := normalizeAzureKeyVaultSecretName(name)
	if !validAzureKeyVaultSecretName(secretName) {
		return "", false
	}
	endpoint, err := url.Parse(r.baseURL + "/secrets/" + url.PathEscape(secretName))
	if err != nil {
		return "", false
	}
	query := endpoint.Query()
	query.Set("api-version", r.apiVersion)
	endpoint.RawQuery = query.Encode()

	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	token, err := r.tokens.Token(ctx)
	if err != nil {
		slog.Warn("azure key vault token acquisition failed", "error", scrubSensitiveError(err, nil))
		return "", false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := r.client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	boundedClient := *client
	boundedClient.CheckRedirect = refuseAzureKeyVaultRedirect
	resp, err := boundedClient.Do(req)
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAzureKeyVaultResponseBytes+1))
	if err != nil || len(body) > maxAzureKeyVaultResponseBytes {
		return "", false
	}
	var payload azureKeyVaultSecretResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", false
	}
	if payload.Value == "" || len(payload.Value) > maxResolvedSecretValueBytes {
		return "", false
	}
	return payload.Value, true
}

func normalizeAzureKeyVaultSecretName(name string) string {
	return strings.NewReplacer("_", "-", ".", "-").Replace(strings.TrimSpace(name))
}

func validAzureKeyVaultSecretName(name string) bool {
	if name == "" || len(name) > 127 {
		return false
	}
	for _, r := range name {
		if r != '-' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func newDefaultSecretResolver() SecretResolver {
	return newDefaultSecretResolverForProject("")
}

func newDefaultSecretResolverForProject(projectID string) SecretResolver {
	var delegate SecretResolver = newChainedSecretResolver(
		envSecretResolver{},
		newAzureKeyVaultSecretResolverFromEnv(),
	)
	if strings.TrimSpace(projectID) != "" {
		delegate = projectScopedSecretResolver{projectID: projectID, delegate: delegate}
	}
	return newCachingSecretResolver(delegate)
}

// NewProjectSecretResolver returns the same project-scoped resolver used by
// load execution. Controller preflight paths use this to validate security-
// sensitive values (for example a secret-backed network target) without
// replacing the reference in the persisted script or exposing the resolved
// value to API responses.
func NewProjectSecretResolver(projectID string) SecretResolver {
	return newDefaultSecretResolverForProject(projectID)
}

func defaultSecretResolverForScript(script *dsl.Script) SecretResolver {
	if script == nil {
		return newDefaultSecretResolver()
	}
	return newDefaultSecretResolverForProject(script.ProjectID)
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
			// JVM arguments are observable in the child process command line.
			// Never turn a secret reference into an argv value: concurrent
			// Java VUser processes share an OS identity in the governed runner
			// and could otherwise inspect one another through /proc. The
			// java_vuser protocol validates the preserved tokens and rejects
			// template-bearing arguments before process creation.
			if strings.EqualFold(strings.TrimSpace(key), "java_vuser_additional_vm_parameters") {
				if values, ok := item.([]string); ok {
					resolved[key] = append([]string(nil), values...)
				} else {
					resolved[key] = item
				}
				continue
			}
			if text, ok := item.(string); ok && isDedicatedProtoConfigSecretKey(key) {
				if key == "kafka_oauth_client_secret" && !strings.Contains(text, "{{") {
					// This field is always a reference, never a literal fallback.
					name := strings.TrimSpace(text)
					resolved[key] = "{{secret." + name + "}}"
					if resolver != nil && ValidLogicalSecretName(name) {
						if secret, ok := resolver.Resolve(name); ok {
							resolved[key] = secret
						}
					}
				} else {
					resolved[key] = resolveDedicatedProtoConfigSecret(text, resolver)
				}
				continue
			}
			resolved[key] = resolveProtoConfigSecrets(item, resolver)
		}
		return resolved
	default:
		return value
	}
}

func isDedicatedProtoConfigSecretKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "database_dsn_secret", "kafka_sasl_password_secret", "kafka_oauth_client_secret",
		"eventhubs_connection_string_secret", "jms_tls_ca_cert_secret":
		return true
	default:
		return false
	}
}

func resolveDedicatedProtoConfigSecret(value string, resolver SecretResolver) string {
	resolved := interpolateSecretReferences(value, resolver)
	if resolved != value || strings.Contains(value, "{{") {
		return resolved
	}
	name := strings.TrimSpace(value)
	if !ValidLogicalSecretName(name) || resolver == nil {
		return value
	}
	if secret, ok := resolver.Resolve(name); ok {
		return secret
	}
	return value
}
