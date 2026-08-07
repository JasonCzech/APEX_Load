package api

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	readinessStatusReady    = "ready"
	readinessStatusDegraded = "degraded"
	readinessStatusNotReady = "not_ready"
)

type SystemReadinessResponse struct {
	Status      string                 `json:"status"`
	GeneratedAt time.Time              `json:"generated_at"`
	Checks      []SystemReadinessCheck `json:"checks"`
}

type SystemReadinessCheck struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (s *Server) handleSystemReadiness(w http.ResponseWriter, r *http.Request) {
	response := s.systemReadiness(r.Context())
	statusCode := http.StatusOK
	if response.Status == readinessStatusNotReady {
		statusCode = http.StatusServiceUnavailable
	}
	writeJSON(w, statusCode, response)
}

func (s *Server) systemReadiness(ctx context.Context) SystemReadinessResponse {
	checks := []SystemReadinessCheck{
		s.metadataStoreReadiness(),
		s.archiveWriterReadiness(),
		s.cacheBackendReadiness(ctx),
		s.generatorRPCReadiness(ctx),
		s.telemetryFanoutReadiness(),
		s.authModeReadiness(),
		s.platformIntegrationsReadiness(),
		s.githubReadiness(),
		s.schedulerReadiness(),
	}

	return SystemReadinessResponse{
		Status:      aggregateReadinessStatus(checks),
		GeneratedAt: time.Now().UTC(),
		Checks:      checks,
	}
}

func (s *Server) cacheBackendReadiness(ctx context.Context) SystemReadinessCheck {
	if s == nil || s.cache == nil {
		return SystemReadinessCheck{
			Name:    "cache_backend",
			Status:  readinessStatusNotReady,
			Message: "cache backend is not configured",
		}
	}
	info := s.cache.Info()
	probeCtx, cancel := context.WithTimeout(ctx, defaultCacheProbeTimeout)
	defer cancel()
	start := time.Now()
	err := s.cache.Ping(probeCtx)
	latency := time.Since(start)
	details := cacheReadinessDetails(info)
	details["probe_latency_ms"] = roundMilliseconds(latency)
	if err != nil {
		status := readinessStatusDegraded
		if cacheBackendIsRemote(info.Backend) {
			status = readinessStatusNotReady
		}
		return SystemReadinessCheck{
			Name:    "cache_backend",
			Status:  status,
			Message: fmt.Sprintf("cache backend probe failed: %v", err),
			Details: details,
		}
	}
	message := "in-memory cache backend is ready"
	if cacheBackendIsRemote(info.Backend) {
		message = fmt.Sprintf("%s cache backend is reachable", info.Backend)
	}
	return SystemReadinessCheck{
		Name:    "cache_backend",
		Status:  readinessStatusReady,
		Message: message,
		Details: details,
	}
}

// roundMilliseconds converts a duration to milliseconds with microsecond precision.
func roundMilliseconds(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(d.Microseconds()) / 1000.0
}

func aggregateReadinessStatus(checks []SystemReadinessCheck) string {
	status := readinessStatusReady
	for _, check := range checks {
		switch check.Status {
		case readinessStatusNotReady:
			return readinessStatusNotReady
		case readinessStatusDegraded:
			status = readinessStatusDegraded
		}
	}
	return status
}

func (s *Server) metadataStoreReadiness() SystemReadinessCheck {
	if s == nil || s.store == nil {
		return SystemReadinessCheck{
			Name:    "metadata_store",
			Status:  readinessStatusNotReady,
			Message: "metadata store is not configured",
		}
	}
	if settingsStore, ok := s.store.(settingsPersistence); ok {
		if _, err := settingsStore.GetSetting(systemSettingsStoreKey); err != nil {
			return SystemReadinessCheck{
				Name:    "metadata_store",
				Status:  readinessStatusNotReady,
				Message: fmt.Sprintf("metadata store probe failed: %v", err),
			}
		}
	}
	details := map[string]any{}
	if provider, ok := s.store.(storage.BackendInfoProvider); ok {
		info := provider.BackendInfo()
		if info.Type != "" {
			details["store_type"] = info.Type
		}
		if info.Driver != "" {
			details["driver"] = info.Driver
		}
		if info.MigrationVersion != "" {
			details["migration_version"] = info.MigrationVersion
		}
		details["lease_capability"] = info.LeaseCapability
		details["timescaledb_enabled"] = info.TimescaleDBEnabled
	}
	return SystemReadinessCheck{
		Name:    "metadata_store",
		Status:  readinessStatusReady,
		Message: "metadata store is reachable",
		Details: details,
	}
}

func (s *Server) archiveWriterReadiness() SystemReadinessCheck {
	backend := currentArchiveBackend()
	if err := backend.validate(); err != nil {
		return SystemReadinessCheck{
			Name:    "archive_writer",
			Status:  readinessStatusNotReady,
			Message: err.Error(),
			Details: backend.readinessDetails(),
		}
	}
	root := backend.LocalRoot
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SystemReadinessCheck{
			Name:    "archive_writer",
			Status:  readinessStatusNotReady,
			Message: fmt.Sprintf("archive root cannot be created: %v", err),
			Details: backend.readinessDetails(),
		}
	}
	probe, err := os.CreateTemp(root, ".readiness-*.tmp")
	if err != nil {
		return SystemReadinessCheck{
			Name:    "archive_writer",
			Status:  readinessStatusNotReady,
			Message: fmt.Sprintf("archive root is not writable: %v", err),
			Details: backend.readinessDetails(),
		}
	}
	probePath := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(probePath)
	if closeErr != nil || removeErr != nil {
		return SystemReadinessCheck{
			Name:    "archive_writer",
			Status:  readinessStatusDegraded,
			Message: "archive root is writable but cleanup reported an error",
			Details: mergeReadinessDetails(backend.readinessDetails(), map[string]any{
				"probe_file":   filepath.Base(probePath),
				"close_error":  errorString(closeErr),
				"remove_error": errorString(removeErr),
			}),
		}
	}
	return SystemReadinessCheck{
		Name:    "archive_writer",
		Status:  readinessStatusReady,
		Message: "archive root is writable",
		Details: backend.readinessDetails(),
	}
}

func mergeReadinessDetails(base map[string]any, extra map[string]any) map[string]any {
	merged := map[string]any{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func (s *Server) generatorRPCReadiness(ctx context.Context) SystemReadinessCheck {
	inventory := s.systemInventory(ctx)
	transport := generatorrpc.TransportSecurityFromEnv()
	if inventory.ExecutionMode == "local" {
		return SystemReadinessCheck{
			Name:    "generator_rpc",
			Status:  readinessStatusReady,
			Message: "local execution mode does not require generator RPC",
			Details: map[string]any{
				"execution_mode": inventory.ExecutionMode,
				"grpc_transport": transport.String(),
			},
		}
	}

	available := 0
	unreachable := 0
	for _, generator := range inventory.Generators {
		status := strings.ToLower(strings.TrimSpace(generator.Status))
		if status == "" || status == "connected" || status == "healthy" || status == "ready" {
			available++
			continue
		}
		unreachable++
	}
	if available == 0 {
		return SystemReadinessCheck{
			Name:    "generator_rpc",
			Status:  readinessStatusNotReady,
			Message: "distributed execution is configured but no generator RPC targets are reachable",
			Details: map[string]any{
				"execution_mode": inventory.ExecutionMode,
				"generators":     len(inventory.Generators),
				"unreachable":    unreachable,
				"grpc_transport": transport.String(),
			},
		}
	}
	status := readinessStatusReady
	message := "generator RPC targets are reachable"
	if unreachable > 0 {
		status = readinessStatusDegraded
		message = "some generator RPC targets are not reachable"
	}
	return SystemReadinessCheck{
		Name:    "generator_rpc",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"execution_mode": inventory.ExecutionMode,
			"generators":     len(inventory.Generators),
			"available":      available,
			"unreachable":    unreachable,
			"grpc_transport": transport.String(),
		},
	}
}

func (s *Server) telemetryFanoutReadiness() SystemReadinessCheck {
	settings := s.runner.GetSystemSettings()
	interval := settings.General.MetricsIntervalSeconds
	status := readinessStatusReady
	message := "telemetry fan-out is initialized"
	if s.hub == nil {
		return SystemReadinessCheck{
			Name:    "telemetry_fanout",
			Status:  readinessStatusNotReady,
			Message: "websocket hub is not initialized",
		}
	}
	if interval <= 0 {
		status = readinessStatusDegraded
		message = "metrics interval is not configured; default runtime behavior may apply"
	}
	return SystemReadinessCheck{
		Name:    "telemetry_fanout",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"ws_clients":               s.hub.ClientCount(),
			"metrics_interval_seconds": interval,
		},
	}
}

func (s *Server) authModeReadiness() SystemReadinessCheck {
	auth := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	status := readinessStatusReady
	message := "auth mode is configured"
	if isAuthBypassMode(auth.Mode) {
		status = readinessStatusDegraded
		message = "auth bypass mode is active; suitable for local development only"
	}
	return SystemReadinessCheck{
		Name:    "auth_mode",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"mode": auth.Mode,
		},
	}
}

func (s *Server) platformIntegrationsReadiness() SystemReadinessCheck {
	backend := currentArchiveBackend()
	prometheus := normalizePrometheusSettings(s.runner.GetSystemSettings().Prometheus)
	dashboard := dashboardStaticAssetsReadiness(s.dashboardStaticFS())
	wrapping := wrappingKeyReadinessDetails()
	status := readinessStatusReady
	message := "deployment integration surfaces are configured"
	if dashboard["status"] != readinessStatusReady {
		status = readinessStatusDegraded
		message = "deployment integration surfaces are configured with dashboard asset warnings"
	}
	// Two wrapping keys configured at once is not a warning to bury: server
	// startup only logs it, the wrapper stays nil, and every credential write
	// then fails with a 503 that does not explain why. Readiness is where an
	// operator will actually see the cause.
	if conflict, _ := wrapping["conflict"].(bool); conflict {
		status = readinessStatusDegraded
		message = "two wrapping keys are configured; set exactly one of " +
			keywrap.KeyVaultKeyIDEnvVar + " or " + keywrap.KMSKeyIDEnvVar
	}
	return SystemReadinessCheck{
		Name:    "platform_integrations",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"grpc_transport":            generatorrpc.TransportSecurityFromEnv().String(),
			"secret_resolver":           secretResolverReadinessDetails(),
			"object_archive":            objectArchiveReadinessDetails(backend),
			"prometheus_scaling_source": prometheusScalingSourceDetails(prometheus),
			"azure_identity":            azureIdentityReadinessDetails(),
			"aws_identity":              awsIdentityReadinessDetails(),
			"wrapping_key":              wrapping,
			"dashboard_static_assets":   dashboard,
		},
	}
}

// wrappingKeyReadinessDetails reports which envelope-wrapping key is selected.
// It is a separate axis from secret_resolver: the wrapper protects cluster
// credentials and project-secret ciphertext, while the secret store is where
// project secret VALUES live.
func wrappingKeyReadinessDetails() map[string]any {
	provider, conflict := keywrap.ConfiguredWrappingProvider()
	return map[string]any{
		"provider": provider,
		"conflict": conflict,
	}
}

func secretResolverReadinessDetails() map[string]any {
	keyVaultURL := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_URL"))
	mode := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_AUTH"))
	if mode == "" {
		mode = "auto"
	}
	details := map[string]any{
		"env_secret_prefix_enabled":  true,
		"azure_key_vault_configured": keyVaultURL != "",
		"aws_secrets_manager_configured": strings.EqualFold(
			strings.TrimSpace(os.Getenv("APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED")), "true"),
	}
	if keyVaultURL != "" {
		details["azure_key_vault_auth"] = mode
	}
	return details
}

func objectArchiveReadinessDetails(backend archiveBackendRuntime) map[string]any {
	details := map[string]any{
		"backend":        backend.Backend,
		"object_backend": backend.Backend != archiveBackendFilesystem,
		"staging_root":   backend.LocalRoot,
	}
	if backend.URIPrefix != "" {
		details["uri_prefix"] = backend.URIPrefix
	}
	if backend.Container != "" {
		details["container"] = backend.Container
	}
	if backend.Bucket != "" {
		details["bucket"] = backend.Bucket
	}
	return details
}

func prometheusScalingSourceDetails(settings PrometheusSettings) map[string]any {
	mode := strings.TrimSpace(os.Getenv("APEXLOAD_PROMETHEUS_SCALING_SOURCE"))
	if mode == "" {
		mode = "prometheus"
	}
	details := map[string]any{
		"source":           mode,
		"settings_enabled": settings.Enabled,
	}
	if settings.BaseURL != "" {
		details["base_url"] = settings.BaseURL
	}
	if remoteWrite := strings.TrimSpace(os.Getenv("APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL")); remoteWrite != "" {
		details["remote_write_configured"] = true
	}
	return details
}

func azureIdentityReadinessDetails() map[string]any {
	clientID := firstNonEmpty(
		strings.TrimSpace(os.Getenv("AZURE_CLIENT_ID")),
		strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_CLIENT_ID")),
	)
	tokenFile := strings.TrimSpace(os.Getenv("AZURE_FEDERATED_TOKEN_FILE"))
	return map[string]any{
		"client_id_configured": clientID != "",
		"federated_token_file": tokenFile != "",
		"tenant_id_configured": strings.TrimSpace(os.Getenv("AZURE_TENANT_ID")) != "",
		"authority_host_set":   strings.TrimSpace(os.Getenv("AZURE_AUTHORITY_HOST")) != "",
	}
}

// awsIdentityReadinessDetails mirrors azureIdentityReadinessDetails: booleans
// only, never an identifier's value, so /readyz can be read by anyone who can
// reach it without disclosing which account the controller runs as.
func awsIdentityReadinessDetails() map[string]any {
	return map[string]any{
		// IRSA projects a token file and a role ARN into the pod.
		"web_identity_token_file": strings.TrimSpace(os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")) != "",
		"role_arn_configured":     strings.TrimSpace(os.Getenv("AWS_ROLE_ARN")) != "",
		"region_configured": firstNonEmpty(
			strings.TrimSpace(os.Getenv("AWS_REGION")),
			strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION")),
		) != "",
		// EKS Pod Identity delivers credentials over the container endpoint.
		"container_credentials": strings.TrimSpace(os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI")) != "" ||
			strings.TrimSpace(os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI")) != "",
		"static_keys_in_env": strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")) != "",
	}
}

// dashboardStaticFS returns the filesystem used to probe embedded dashboard
// assets, defaulting to the compile-time embed when no override is set.
func (s *Server) dashboardStaticFS() fs.FS {
	if s != nil && s.staticFS != nil {
		return s.staticFS
	}
	return StaticFS
}

func dashboardStaticAssetsReadiness(staticFS fs.FS) map[string]any {
	details := map[string]any{
		"status": readinessStatusReady,
	}
	if _, err := fs.Stat(staticFS, "static/index.html"); err != nil {
		details["status"] = readinessStatusDegraded
		details["index_html"] = false
		details["error"] = err.Error()
		return details
	}
	details["index_html"] = true
	assets, err := fs.Glob(staticFS, "static/assets/*")
	if err != nil {
		details["status"] = readinessStatusDegraded
		details["error"] = err.Error()
		return details
	}
	details["asset_count"] = len(assets)
	if len(assets) == 0 {
		details["status"] = readinessStatusDegraded
	}
	return details
}

func (s *Server) githubReadiness() SystemReadinessCheck {
	settings := s.currentGitHubSettings()
	details := map[string]any{
		"enabled":              settings.Enabled,
		"oauth_configured":     settings.oauthConfigured(),
		"app_configured":       settings.appConfigured(),
		"webhook_configured":   settings.webhookConfigured(),
		"pat_configured":       settings.patConfigured(),
		"encryption_available": githubTokenEncryptionAvailable(),
		"automation_mode":      settings.AutomationMode,
		"checks_enabled":       settings.ChecksEnabled,
	}
	if !settings.Enabled {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusReady, Message: "github integration is disabled", Details: details}
	}
	if !settings.oauthConfigured() && !settings.appConfigured() && !settings.patConfigured() {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusNotReady, Message: "github integration is enabled but no authentication method is configured", Details: details}
	}
	if !githubTokenEncryptionAvailable() {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusNotReady, Message: "github token encryption key is not configured", Details: details}
	}
	if settings.ChecksEnabled && !settings.appConfigured() {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusDegraded, Message: "github checks are enabled but GitHub App authentication is not configured", Details: details}
	}
	if settings.WebhookSecretEnvVar != "" && !settings.webhookConfigured() {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusDegraded, Message: "github webhook secret env var is set but the secret value is unavailable", Details: details}
	}
	return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusReady, Message: "github enterprise integration is configured", Details: details}
}

func (s *Server) schedulerReadiness() SystemReadinessCheck {
	if s == nil || s.scheduler == nil {
		return SystemReadinessCheck{
			Name:    "scheduler_state",
			Status:  readinessStatusNotReady,
			Message: "scenario scheduler is not initialized",
		}
	}
	enabled, due, errored := s.schedulerCounts(time.Now().UTC())
	status := readinessStatusReady
	message := "scenario scheduler is initialized"
	if errored > 0 {
		status = readinessStatusDegraded
		message = "scenario scheduler has schedules with previous errors"
	}
	return SystemReadinessCheck{
		Name:    "scheduler_state",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"enabled_schedules": enabled,
			"due_schedules":     due,
			"errored_schedules": errored,
			"ha_mode":           "single-active-controller",
		},
	}
}

func (s *Server) schedulerCounts(now time.Time) (int, int, int) {
	if s == nil || s.runner == nil {
		return 0, 0, 0
	}
	s.runner.mu.RLock()
	defer s.runner.mu.RUnlock()

	enabled := 0
	due := 0
	errored := 0
	for _, schedule := range s.runner.listAllScenarioSchedulesLocked() {
		if schedule == nil || !schedule.Enabled {
			continue
		}
		enabled++
		if schedule.NextRunAt != nil && !schedule.NextRunAt.After(now) {
			due++
		}
		if strings.TrimSpace(schedule.LastError) != "" {
			errored++
		}
	}
	return enabled, due, errored
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
