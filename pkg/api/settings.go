package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const systemSettingsStoreKey = "system_settings"

const systemSettingsAuthRefreshInterval = 2 * time.Second

const redactedSecretConfiguredField = "has_api_token"

const (
	defaultTelemetrySampleAgeBudgetMs           = 2000.0
	defaultTelemetryGeneratorProcessingBudgetMs = 500.0
	defaultTelemetryTransportBudgetMs           = 500.0
	defaultTelemetryControllerFanoutBudgetMs    = 500.0
	defaultTelemetryBrowserRenderBudgetMs       = 500.0
)

type SystemSettings struct {
	General          GeneralSettings          `json:"general"`
	Thresholds       ThresholdSettings        `json:"thresholds"`
	CapacityPlanning CapacityPlanningSettings `json:"capacity_planning"`
	ArchiveRetention ArchiveRetentionSettings `json:"archive_retention"`
	ScheduleDefaults ScheduleDefaultsSettings `json:"schedule_defaults"`
	Notifications    NotificationsSettings    `json:"notifications"`
	Auth             AuthSettings             `json:"auth"`
	GitHub           GitHubSettings           `json:"github"`
	Prometheus       PrometheusSettings       `json:"prometheus"`
	ClientApp        ClientAppSettings        `json:"client_app"`
	ClientDownloads  ClientDownloadsSettings  `json:"client_downloads"`
}

// GitHubSettings is the global (admin-owned) configuration for the GitHub script
// sync integration. The OAuth client_secret is never stored here — only the name
// of the environment variable that holds it, mirroring AuthSettings. The base
// URLs default to github.com but are pointed at a self-hosted GitHub Enterprise
// Server (e.g. https://ghe.example.com/api/v3) in production.
type GitHubSettings struct {
	Enabled               bool     `json:"enabled"`
	ClientID              string   `json:"client_id,omitempty"`
	ClientSecretEnvVar    string   `json:"client_secret_env_var,omitempty"`
	AppID                 int64    `json:"app_id,omitempty"`
	AppSlug               string   `json:"app_slug,omitempty"`
	AppPrivateKeyEnvVar   string   `json:"app_private_key_env_var,omitempty"`
	AppPrivateKeyFile     string   `json:"app_private_key_file,omitempty"`
	WebhookSecretEnvVar   string   `json:"webhook_secret_env_var,omitempty"`
	PATEnvVar             string   `json:"pat_env_var,omitempty"`
	AutomationMode        string   `json:"automation_mode,omitempty"`
	ChecksEnabled         bool     `json:"checks_enabled,omitempty"`
	APIBaseURL            string   `json:"api_base_url,omitempty"` // e.g. https://ghe.example.com/api/v3
	WebBaseURL            string   `json:"web_base_url,omitempty"` // e.g. https://ghe.example.com
	CACertFile            string   `json:"ca_cert_file,omitempty"`
	TLSInsecureSkipVerify bool     `json:"tls_insecure_skip_verify,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	DefaultRepo           string   `json:"default_repo,omitempty"`
	DefaultBranch         string   `json:"default_branch,omitempty"`
	PathTemplate          string   `json:"path_template,omitempty"`
}

type PrometheusSettings struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type ClientAppSettings struct {
	Availability string `json:"availability"`
}

// ClientDownloadsSettings gates whether the dashboard offers downloadable client
// software (the local Recording Agent and the desktop app). When disabled, the
// header download button is hidden AND the download endpoints fail closed (403).
// It defaults to enabled — admins opt out, mirroring the user-facing wording
// "appears unless an admin has disabled it".
type ClientDownloadsSettings struct {
	Enabled bool `json:"enabled"`
}

const (
	defaultGitHubAPIBaseURL = "https://api.github.com"
	defaultGitHubWebBaseURL = "https://github.com"
	defaultGitHubBranch     = "main"
	defaultGitHubPathTmpl   = "apexload/scripts/{project}/{ref_key}.json"
	defaultGitHubAutoMode   = "app_oauth_pat"
	githubTokenKeyEnvVar    = "APEXLOAD_GITHUB_TOKEN_KEY"
	clientAppEnabled        = "enabled"
	clientAppDisabled       = "disabled"
)

func defaultGitHubSettings() GitHubSettings {
	return GitHubSettings{
		Enabled:        false,
		APIBaseURL:     defaultGitHubAPIBaseURL,
		WebBaseURL:     defaultGitHubWebBaseURL,
		AutomationMode: defaultGitHubAutoMode,
		Scopes:         []string{"repo"},
		DefaultBranch:  defaultGitHubBranch,
		PathTemplate:   defaultGitHubPathTmpl,
	}
}

func defaultPrometheusSettings() PrometheusSettings {
	baseURL := strings.TrimRight(strings.TrimSpace(envValue("APEXLOAD_PROMETHEUS_URL")), "/")
	return PrometheusSettings{
		Enabled:        baseURL != "",
		BaseURL:        baseURL,
		TimeoutSeconds: 5,
	}
}

func defaultClientAppSettings() ClientAppSettings {
	return ClientAppSettings{Availability: clientAppDisabled}
}

func defaultClientDownloadsSettings() ClientDownloadsSettings {
	return ClientDownloadsSettings{Enabled: true}
}

// normalizeClientDownloadsSettings is a no-op today (a bool has no invalid
// states) but exists for symmetry with the other settings sections and to give
// future fields a single normalization point.
func normalizeClientDownloadsSettings(in ClientDownloadsSettings) ClientDownloadsSettings {
	return in
}

func clientDownloadsEnabled(settings ClientDownloadsSettings) bool {
	return settings.Enabled
}

func normalizeClientAppSettings(in ClientAppSettings) ClientAppSettings {
	out := in
	out.Availability = strings.ToLower(strings.TrimSpace(out.Availability))
	if out.Availability == "" {
		out.Availability = clientAppDisabled
	}
	return out
}

func clientAppAvailable(settings ClientAppSettings) bool {
	return normalizeClientAppSettings(settings).Availability == clientAppEnabled
}

// normalizePrometheusSettings fills in defaults for the base URL and timeout but
// is FAITHFUL to the Enabled flag it is given — it never silently re-enables a
// disabled section. The default-enable behavior when APEXLOAD_PROMETHEUS_URL is
// set is applied only where presence can be determined: at fresh-install
// defaults (defaultPrometheusSettings) and at legacy-store migration
// (loadSystemSettings). This is what lets an admin persist Prometheus=disabled
// even while the env URL is configured.
func normalizePrometheusSettings(in PrometheusSettings) PrometheusSettings {
	defaults := defaultPrometheusSettings()
	out := in
	out.BaseURL = strings.TrimRight(strings.TrimSpace(out.BaseURL), "/")
	if out.BaseURL == "" && defaults.BaseURL != "" {
		out.BaseURL = defaults.BaseURL
	}
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = defaults.TimeoutSeconds
	}
	return out
}

// rawJSONHasKey reports whether the top-level JSON object in raw contains key.
// Used to distinguish an omitted section (inherit default) from an explicitly
// provided one (respect as given, including an explicit disable).
func rawJSONHasKey(raw string, key string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return false
	}
	_, ok := fields[key]
	return ok
}

func (p PrometheusSettings) allowlistHosts() []string {
	parsed, err := url.Parse(strings.TrimSpace(p.BaseURL))
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	return []string{parsed.Hostname()}
}

func normalizeGitHubSettings(in GitHubSettings) GitHubSettings {
	out := in
	out.ClientID = strings.TrimSpace(out.ClientID)
	out.ClientSecretEnvVar = strings.TrimSpace(out.ClientSecretEnvVar)
	out.AppSlug = strings.TrimSpace(out.AppSlug)
	out.AppPrivateKeyEnvVar = strings.TrimSpace(out.AppPrivateKeyEnvVar)
	out.AppPrivateKeyFile = strings.TrimSpace(out.AppPrivateKeyFile)
	out.WebhookSecretEnvVar = strings.TrimSpace(out.WebhookSecretEnvVar)
	out.PATEnvVar = strings.TrimSpace(out.PATEnvVar)
	out.AutomationMode = strings.ToLower(strings.TrimSpace(out.AutomationMode))
	out.APIBaseURL = strings.TrimRight(strings.TrimSpace(out.APIBaseURL), "/")
	out.WebBaseURL = strings.TrimRight(strings.TrimSpace(out.WebBaseURL), "/")
	out.CACertFile = strings.TrimSpace(out.CACertFile)
	out.DefaultRepo = strings.TrimSpace(out.DefaultRepo)
	out.DefaultBranch = strings.TrimSpace(out.DefaultBranch)
	out.PathTemplate = strings.TrimSpace(out.PathTemplate)
	if out.AutomationMode == "" {
		out.AutomationMode = defaultGitHubAutoMode
	}
	if out.APIBaseURL == "" {
		out.APIBaseURL = defaultGitHubAPIBaseURL
	}
	if out.WebBaseURL == "" {
		out.WebBaseURL = defaultGitHubWebBaseURL
	}
	if out.DefaultBranch == "" {
		out.DefaultBranch = defaultGitHubBranch
	}
	if out.PathTemplate == "" {
		out.PathTemplate = defaultGitHubPathTmpl
	}
	out.Scopes = normalizeGitHubScopes(out.Scopes)
	return out
}

func normalizeGitHubScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		trimmed := strings.ToLower(strings.TrimSpace(scope))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return []string{"repo"}
	}
	return out
}

// clientSecret resolves the OAuth client secret from the configured environment
// variable. The secret is never persisted or serialized — only the env var name is.
func (g GitHubSettings) clientSecret() string {
	if strings.TrimSpace(g.ClientSecretEnvVar) == "" {
		return ""
	}
	return strings.TrimSpace(envValue(g.ClientSecretEnvVar))
}

// oauthConfigured reports whether the integration is enabled and the OAuth App
// client id + secret are both available (so the connect flow can run).
func (g GitHubSettings) oauthConfigured() bool {
	return g.Enabled && strings.TrimSpace(g.ClientID) != "" && g.clientSecret() != ""
}

func (g GitHubSettings) appPrivateKey() string {
	if strings.TrimSpace(g.AppPrivateKeyEnvVar) != "" {
		if value := strings.TrimSpace(envValue(g.AppPrivateKeyEnvVar)); value != "" {
			return value
		}
	}
	if strings.TrimSpace(g.AppPrivateKeyFile) != "" {
		return strings.TrimSpace(g.AppPrivateKeyFile)
	}
	return ""
}

func (g GitHubSettings) appConfigured() bool {
	return g.Enabled && g.AppID > 0 && strings.TrimSpace(g.appPrivateKey()) != ""
}

func (g GitHubSettings) webhookSecret() string {
	if strings.TrimSpace(g.WebhookSecretEnvVar) == "" {
		return ""
	}
	return strings.TrimSpace(envValue(g.WebhookSecretEnvVar))
}

func (g GitHubSettings) webhookConfigured() bool {
	return g.Enabled && strings.TrimSpace(g.WebhookSecretEnvVar) != "" && g.webhookSecret() != ""
}

func (g GitHubSettings) patToken() string {
	if strings.TrimSpace(g.PATEnvVar) == "" {
		return ""
	}
	return strings.TrimSpace(envValue(g.PATEnvVar))
}

func (g GitHubSettings) patConfigured() bool {
	return g.Enabled && g.patToken() != ""
}

// allowlistHosts returns the hostnames (no port) of the configured GitHub
// endpoints, used to seed the host-scoped SSRF egress allowance so a private
// GHES host can be reached without opening general private egress.
func (g GitHubSettings) allowlistHosts() []string {
	hosts := make([]string, 0, 2)
	for _, raw := range []string{g.APIBaseURL, g.WebBaseURL} {
		if parsed, err := url.Parse(strings.TrimSpace(raw)); err == nil && parsed.Hostname() != "" {
			hosts = append(hosts, parsed.Hostname())
		}
	}
	return hosts
}

type GeneralSettings struct {
	APIEndpoint                  string `json:"api_endpoint"`
	MaxConcurrentTests           int    `json:"max_concurrent_tests"`
	MetricsIntervalSeconds       int    `json:"metrics_interval_seconds"`
	UsageSnapshotIntervalSeconds int    `json:"usage_snapshot_interval_seconds,omitempty"`
}

type ThresholdSettings struct {
	MaxErrorRatePct                *float64 `json:"max_error_rate_pct,omitempty"`
	MaxP99LatencyMs                *float64 `json:"max_p99_latency_ms,omitempty"`
	MaxAvgServerProcessingMs       *float64 `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs               *float64 `json:"max_avg_transfer_ms,omitempty"`
	MaxTelemetrySampleAgeMs        *float64 `json:"max_telemetry_sample_age_ms,omitempty"`
	MaxTelemetryGeneratorLagMs     *float64 `json:"max_telemetry_generator_lag_ms,omitempty"`
	MaxTelemetryTransportLagMs     *float64 `json:"max_telemetry_transport_lag_ms,omitempty"`
	MaxTelemetryControllerFanoutMs *float64 `json:"max_telemetry_controller_fanout_ms,omitempty"`
	MaxTelemetryBrowserRenderLagMs *float64 `json:"max_telemetry_browser_render_lag_ms,omitempty"`
	FleetConstrainedAvailableSlots *int     `json:"fleet_constrained_available_slots,omitempty"`
	FleetSaturatedAvailableSlots   *int     `json:"fleet_saturated_available_slots,omitempty"`
}

type CapacityPlanningSettings struct {
	HeadroomPct  float64                   `json:"headroom_pct"`
	Generator    GeneratorCapacitySettings `json:"generator"`
	Availability AvailabilitySettings      `json:"availability"`
	Reservations []CapacityReservation     `json:"reservations,omitempty"`
}

type GeneratorCapacitySettings struct {
	CPUCores    float64 `json:"cpu_cores"`
	MemoryMB    int     `json:"memory_mb"`
	NetworkMbps float64 `json:"network_mbps"`
	MaxVUsers   int     `json:"max_vusers"`
}

type AvailabilitySettings struct {
	Source                        string `json:"source"`
	ConfiguredAvailableGenerators int    `json:"configured_available_generators"`
}

type CapacityReservation struct {
	ProjectID          string `json:"project_id"`
	ReservedGenerators int    `json:"reserved_generators"`
	// PoolID scopes a reservation to one fleet pool's capacity envelope instead of
	// the global pool. Empty (the default) means a global reservation, unchanged.
	// Pool-scoped reservations are excluded from the global reservation math and
	// enforced separately against the pool's member capacity.
	PoolID string `json:"pool_id,omitempty"`
	// StartsAt/EndsAt make a reservation a timeslot: it is only active within the
	// window. Either bound may be nil for an open-ended window; both nil means the
	// reservation is always active (backward compatible).
	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
}

type ArchiveRetentionSettings struct {
	Enabled              bool                     `json:"enabled"`
	DefaultRetentionDays int                      `json:"default_retention_days"`
	Policies             []ArchiveRetentionPolicy `json:"policies,omitempty"`
	// HARRetentionDays governs scrubbed browser HAR diagnostic artifacts
	// (har_capture policies). HAR-with-bodies is the most sensitive artifact
	// class, so it defaults shorter than run archives (7 days).
	HARRetentionDays int `json:"har_retention_days,omitempty"`
	// HARArtifactDir is where runtime HAR artifacts are swept from. Empty uses
	// the OS temp directory, the browser protocol's default artifact location.
	HARArtifactDir string `json:"har_artifact_dir,omitempty"`
}

type ArchiveRetentionPolicy struct {
	Name            string `json:"name,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	ReleaseID       string `json:"release_id,omitempty"`
	RunStatus       string `json:"run_status,omitempty"`
	RunType         string `json:"run_type,omitempty"`
	ArtifactType    string `json:"artifact_type,omitempty"`
	ComplianceClass string `json:"compliance_class,omitempty"`
	Reason          string `json:"reason,omitempty"`
	RetentionDays   int    `json:"retention_days"`
}

type ScheduleDefaultsSettings struct {
	ApprovalPolicy        string `json:"approval_policy,omitempty"`
	MissedRunPolicy       string `json:"missed_run_policy,omitempty"`
	BlackoutStartTime     string `json:"blackout_start_time,omitempty"`
	BlackoutEndTime       string `json:"blackout_end_time,omitempty"`
	AvailabilityStartTime string `json:"availability_start_time,omitempty"`
	AvailabilityEndTime   string `json:"availability_end_time,omitempty"`
}

type AuthSettings struct {
	Mode                  string   `json:"mode"`
	ProviderName          string   `json:"provider_name,omitempty"`
	LoginURL              string   `json:"login_url,omitempty"`
	LogoutURL             string   `json:"logout_url,omitempty"`
	IssuerURL             string   `json:"issuer_url,omitempty"`
	ClientID              string   `json:"client_id,omitempty"`
	ClientSecretEnvVar    string   `json:"client_secret_env_var,omitempty"`
	RedirectURL           string   `json:"redirect_url,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	RolesClaim            string   `json:"roles_claim,omitempty"`
	ProjectRolesClaim     string   `json:"project_roles_claim,omitempty"`
	DomainRolesClaim      string   `json:"domain_roles_claim,omitempty"`
	RepositoryRolesClaim  string   `json:"application_repository_roles_claim,omitempty"`
	SessionSecretEnvVar   string   `json:"session_secret_env_var,omitempty"`
	UserHeader            string   `json:"user_header,omitempty"`
	NameHeader            string   `json:"name_header,omitempty"`
	EmailHeader           string   `json:"email_header,omitempty"`
	RolesHeader           string   `json:"roles_header,omitempty"`
	ProjectRolesHeader    string   `json:"project_roles_header,omitempty"`
	DomainRolesHeader     string   `json:"domain_roles_header,omitempty"`
	RepositoryRolesHeader string   `json:"application_repository_roles_header,omitempty"`
	// Trusted-proxy gate for trusted_headers mode (defense-in-depth against header
	// spoofing if the controller port is ever reachable directly). Identity headers are
	// honored only when the request arrives from a TrustedProxyCIDRs source OR carries a
	// constant-time-matching shared secret (env-var name in TrustedProxySecretEnvVar,
	// header X-APEXLoad-Proxy-Secret). In production at least one must be configured.
	TrustedProxyCIDRs        []string `json:"trusted_proxy_cidrs,omitempty"`
	TrustedProxySecretEnvVar string   `json:"trusted_proxy_secret_env_var,omitempty"`
}

const (
	capacityAvailabilityConfigured         = "configured"
	capacityAvailabilityConfiguredPlusLive = "configured_plus_live"
	authModeDev                            = "dev"
	authModeDisabled                       = "disabled"
	authModeTrustedHeaders                 = "trusted_headers"
	authModeOIDC                           = "oidc"
)

func defaultAuthSettings() AuthSettings {
	return AuthSettings{
		Mode:                  authModeDev,
		Scopes:                []string{"openid", "profile", "email"},
		RolesClaim:            "roles",
		ProjectRolesClaim:     "project_roles",
		DomainRolesClaim:      "domain_roles",
		RepositoryRolesClaim:  "application_repository_roles",
		SessionSecretEnvVar:   "APEXLOAD_AUTH_SESSION_SECRET",
		UserHeader:            "X-Auth-Request-User",
		NameHeader:            "X-Auth-Request-Name",
		EmailHeader:           "X-Auth-Request-Email",
		RolesHeader:           "X-APEXLoad-Roles",
		ProjectRolesHeader:    "X-APEXLoad-Project-Roles",
		DomainRolesHeader:     "X-APEXLoad-Domain-Roles",
		RepositoryRolesHeader: "X-APEXLoad-Application-Repository-Roles",
	}
}

func normalizeAuthSettings(in AuthSettings) AuthSettings {
	defaults := defaultAuthSettings()
	out := in
	out.Mode = strings.TrimSpace(out.Mode)
	out.ProviderName = strings.TrimSpace(out.ProviderName)
	out.LoginURL = strings.TrimSpace(out.LoginURL)
	out.LogoutURL = strings.TrimSpace(out.LogoutURL)
	out.IssuerURL = strings.TrimSpace(out.IssuerURL)
	out.ClientID = strings.TrimSpace(out.ClientID)
	out.ClientSecretEnvVar = strings.TrimSpace(out.ClientSecretEnvVar)
	out.RedirectURL = strings.TrimSpace(out.RedirectURL)
	out.RolesClaim = strings.TrimSpace(out.RolesClaim)
	out.ProjectRolesClaim = strings.TrimSpace(out.ProjectRolesClaim)
	out.DomainRolesClaim = strings.TrimSpace(out.DomainRolesClaim)
	out.RepositoryRolesClaim = strings.TrimSpace(out.RepositoryRolesClaim)
	out.SessionSecretEnvVar = strings.TrimSpace(out.SessionSecretEnvVar)
	out.Scopes = normalizeAuthScopes(out.Scopes)
	if isAuthBypassMode(out.Mode) {
		out.Mode = defaults.Mode
	}
	if len(out.Scopes) == 0 {
		out.Scopes = append([]string(nil), defaults.Scopes...)
	}
	if out.RolesClaim == "" {
		out.RolesClaim = defaults.RolesClaim
	}
	if out.ProjectRolesClaim == "" {
		out.ProjectRolesClaim = defaults.ProjectRolesClaim
	}
	if out.DomainRolesClaim == "" {
		out.DomainRolesClaim = defaults.DomainRolesClaim
	}
	if out.RepositoryRolesClaim == "" {
		out.RepositoryRolesClaim = defaults.RepositoryRolesClaim
	}
	if out.SessionSecretEnvVar == "" {
		out.SessionSecretEnvVar = defaults.SessionSecretEnvVar
	}
	if out.UserHeader == "" {
		out.UserHeader = defaults.UserHeader
	}
	if out.NameHeader == "" {
		out.NameHeader = defaults.NameHeader
	}
	if out.EmailHeader == "" {
		out.EmailHeader = defaults.EmailHeader
	}
	if out.RolesHeader == "" {
		out.RolesHeader = defaults.RolesHeader
	}
	if out.ProjectRolesHeader == "" {
		out.ProjectRolesHeader = defaults.ProjectRolesHeader
	}
	if out.DomainRolesHeader == "" {
		out.DomainRolesHeader = defaults.DomainRolesHeader
	}
	if out.RepositoryRolesHeader == "" {
		out.RepositoryRolesHeader = defaults.RepositoryRolesHeader
	}
	return out
}

func isAuthBypassMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "", authModeDev, authModeDisabled:
		return true
	default:
		return false
	}
}

func defaultSystemSettings(maxConcurrent int) SystemSettings {
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}
	return SystemSettings{
		General: GeneralSettings{
			APIEndpoint:                  ":8080",
			MaxConcurrentTests:           maxConcurrent,
			MetricsIntervalSeconds:       1,
			UsageSnapshotIntervalSeconds: 0,
		},
		Thresholds: defaultThresholdSettings(),
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		ArchiveRetention: ArchiveRetentionSettings{
			Enabled:              false,
			DefaultRetentionDays: 90,
		},
		ScheduleDefaults: defaultScheduleDefaultsSettings(),
		Notifications:    NotificationsSettings{},
		Auth:             defaultAuthSettings(),
		GitHub:           defaultGitHubSettings(),
		Prometheus:       defaultPrometheusSettings(),
		ClientApp:        defaultClientAppSettings(),
		ClientDownloads:  defaultClientDownloadsSettings(),
	}
}

func cloneSystemSettings(in SystemSettings) SystemSettings {
	out := in
	if in.Thresholds.MaxErrorRatePct != nil {
		v := *in.Thresholds.MaxErrorRatePct
		out.Thresholds.MaxErrorRatePct = &v
	}
	if in.Thresholds.MaxP99LatencyMs != nil {
		v := *in.Thresholds.MaxP99LatencyMs
		out.Thresholds.MaxP99LatencyMs = &v
	}
	if in.Thresholds.MaxAvgServerProcessingMs != nil {
		v := *in.Thresholds.MaxAvgServerProcessingMs
		out.Thresholds.MaxAvgServerProcessingMs = &v
	}
	if in.Thresholds.MaxAvgTransferMs != nil {
		v := *in.Thresholds.MaxAvgTransferMs
		out.Thresholds.MaxAvgTransferMs = &v
	}
	if in.Thresholds.MaxTelemetrySampleAgeMs != nil {
		v := *in.Thresholds.MaxTelemetrySampleAgeMs
		out.Thresholds.MaxTelemetrySampleAgeMs = &v
	}
	if in.Thresholds.MaxTelemetryGeneratorLagMs != nil {
		v := *in.Thresholds.MaxTelemetryGeneratorLagMs
		out.Thresholds.MaxTelemetryGeneratorLagMs = &v
	}
	if in.Thresholds.MaxTelemetryTransportLagMs != nil {
		v := *in.Thresholds.MaxTelemetryTransportLagMs
		out.Thresholds.MaxTelemetryTransportLagMs = &v
	}
	if in.Thresholds.MaxTelemetryControllerFanoutMs != nil {
		v := *in.Thresholds.MaxTelemetryControllerFanoutMs
		out.Thresholds.MaxTelemetryControllerFanoutMs = &v
	}
	if in.Thresholds.MaxTelemetryBrowserRenderLagMs != nil {
		v := *in.Thresholds.MaxTelemetryBrowserRenderLagMs
		out.Thresholds.MaxTelemetryBrowserRenderLagMs = &v
	}
	if in.Thresholds.FleetConstrainedAvailableSlots != nil {
		v := *in.Thresholds.FleetConstrainedAvailableSlots
		out.Thresholds.FleetConstrainedAvailableSlots = &v
	}
	if in.Thresholds.FleetSaturatedAvailableSlots != nil {
		v := *in.Thresholds.FleetSaturatedAvailableSlots
		out.Thresholds.FleetSaturatedAvailableSlots = &v
	}
	out.Notifications = cloneNotificationsSettings(in.Notifications)
	if in.CapacityPlanning.Reservations != nil {
		out.CapacityPlanning.Reservations = append([]CapacityReservation(nil), in.CapacityPlanning.Reservations...)
	}
	if in.ArchiveRetention.Policies != nil {
		out.ArchiveRetention.Policies = append([]ArchiveRetentionPolicy(nil), in.ArchiveRetention.Policies...)
	}
	out.ScheduleDefaults = in.ScheduleDefaults
	out.Auth = in.Auth
	if in.Auth.Scopes != nil {
		out.Auth.Scopes = append([]string(nil), in.Auth.Scopes...)
	}
	out.GitHub = in.GitHub
	if in.GitHub.Scopes != nil {
		out.GitHub.Scopes = append([]string(nil), in.GitHub.Scopes...)
	}
	out.Prometheus = in.Prometheus
	out.ClientApp = in.ClientApp
	out.ClientDownloads = in.ClientDownloads
	return out
}

func normalizeLoadedSystemSettings(in SystemSettings, defaultMaxConcurrent int) SystemSettings {
	defaults := defaultSystemSettings(defaultMaxConcurrent)
	out := cloneSystemSettings(in)

	if out.General.APIEndpoint == "" {
		out.General.APIEndpoint = defaults.General.APIEndpoint
	}
	if out.General.MaxConcurrentTests == 0 {
		out.General.MaxConcurrentTests = defaults.General.MaxConcurrentTests
	}
	if out.General.MetricsIntervalSeconds == 0 {
		out.General.MetricsIntervalSeconds = defaults.General.MetricsIntervalSeconds
	}
	if out.CapacityPlanning.HeadroomPct == 0 {
		out.CapacityPlanning.HeadroomPct = defaults.CapacityPlanning.HeadroomPct
	}
	if out.CapacityPlanning.Generator.CPUCores == 0 {
		out.CapacityPlanning.Generator.CPUCores = defaults.CapacityPlanning.Generator.CPUCores
	}
	if out.CapacityPlanning.Generator.MemoryMB == 0 {
		out.CapacityPlanning.Generator.MemoryMB = defaults.CapacityPlanning.Generator.MemoryMB
	}
	if out.CapacityPlanning.Generator.NetworkMbps == 0 {
		out.CapacityPlanning.Generator.NetworkMbps = defaults.CapacityPlanning.Generator.NetworkMbps
	}
	if out.CapacityPlanning.Generator.MaxVUsers == 0 {
		out.CapacityPlanning.Generator.MaxVUsers = defaults.CapacityPlanning.Generator.MaxVUsers
	}
	if out.CapacityPlanning.Availability.Source == "" {
		out.CapacityPlanning.Availability.Source = defaults.CapacityPlanning.Availability.Source
	}
	if out.CapacityPlanning.Availability.ConfiguredAvailableGenerators == 0 {
		out.CapacityPlanning.Availability.ConfiguredAvailableGenerators = defaults.CapacityPlanning.Availability.ConfiguredAvailableGenerators
	}
	out.ArchiveRetention = normalizeArchiveRetentionSettings(out.ArchiveRetention)
	out.ScheduleDefaults = normalizeScheduleDefaultsSettings(out.ScheduleDefaults)
	out.CapacityPlanning.Reservations = normalizeCapacityReservations(out.CapacityPlanning.Reservations)
	out.Thresholds = normalizeThresholdSettings(out.Thresholds)
	out.Notifications = cloneNotificationsSettings(out.Notifications)
	out.Auth = normalizeAuthSettings(out.Auth)
	out.GitHub = normalizeGitHubSettings(out.GitHub)
	out.Prometheus = normalizePrometheusSettings(out.Prometheus)
	out.ClientApp = normalizeClientAppSettings(out.ClientApp)
	out.ClientDownloads = normalizeClientDownloadsSettings(out.ClientDownloads)

	return out
}

func validateSystemSettings(settings SystemSettings) error {
	settings.ArchiveRetention = normalizeArchiveRetentionSettings(settings.ArchiveRetention)
	if settings.General.APIEndpoint == "" {
		return fmt.Errorf("general.api_endpoint is required")
	}
	if settings.General.MaxConcurrentTests < 1 {
		return fmt.Errorf("general.max_concurrent_tests must be >= 1")
	}
	if settings.General.MetricsIntervalSeconds < 1 {
		return fmt.Errorf("general.metrics_interval_seconds must be >= 1")
	}
	if settings.General.UsageSnapshotIntervalSeconds < 0 {
		return fmt.Errorf("general.usage_snapshot_interval_seconds must be >= 0")
	}
	if settings.Thresholds.MaxErrorRatePct != nil {
		if *settings.Thresholds.MaxErrorRatePct < 0 || *settings.Thresholds.MaxErrorRatePct > 100 {
			return fmt.Errorf("thresholds.max_error_rate_pct must be between 0 and 100")
		}
	}
	if settings.Thresholds.MaxP99LatencyMs != nil {
		if *settings.Thresholds.MaxP99LatencyMs <= 0 {
			return fmt.Errorf("thresholds.max_p99_latency_ms must be > 0")
		}
	}
	if settings.Thresholds.MaxAvgServerProcessingMs != nil {
		if *settings.Thresholds.MaxAvgServerProcessingMs <= 0 {
			return fmt.Errorf("thresholds.max_avg_server_processing_ms must be > 0")
		}
	}
	if settings.Thresholds.MaxAvgTransferMs != nil {
		if *settings.Thresholds.MaxAvgTransferMs <= 0 {
			return fmt.Errorf("thresholds.max_avg_transfer_ms must be > 0")
		}
	}
	if err := validateOptionalPositiveThreshold("thresholds.max_telemetry_sample_age_ms", settings.Thresholds.MaxTelemetrySampleAgeMs); err != nil {
		return err
	}
	if err := validateOptionalPositiveThreshold("thresholds.max_telemetry_generator_lag_ms", settings.Thresholds.MaxTelemetryGeneratorLagMs); err != nil {
		return err
	}
	if err := validateOptionalPositiveThreshold("thresholds.max_telemetry_transport_lag_ms", settings.Thresholds.MaxTelemetryTransportLagMs); err != nil {
		return err
	}
	if err := validateOptionalPositiveThreshold("thresholds.max_telemetry_controller_fanout_ms", settings.Thresholds.MaxTelemetryControllerFanoutMs); err != nil {
		return err
	}
	if err := validateOptionalPositiveThreshold("thresholds.max_telemetry_browser_render_lag_ms", settings.Thresholds.MaxTelemetryBrowserRenderLagMs); err != nil {
		return err
	}
	if err := validateOptionalNonNegativeIntThreshold("thresholds.fleet_constrained_available_slots", settings.Thresholds.FleetConstrainedAvailableSlots); err != nil {
		return err
	}
	if err := validateOptionalNonNegativeIntThreshold("thresholds.fleet_saturated_available_slots", settings.Thresholds.FleetSaturatedAvailableSlots); err != nil {
		return err
	}
	if settings.CapacityPlanning.HeadroomPct < 0 || settings.CapacityPlanning.HeadroomPct >= 100 {
		return fmt.Errorf("capacity_planning.headroom_pct must be between 0 and 100")
	}
	if settings.CapacityPlanning.Generator.CPUCores <= 0 {
		return fmt.Errorf("capacity_planning.generator.cpu_cores must be > 0")
	}
	if settings.CapacityPlanning.Generator.MemoryMB <= 0 {
		return fmt.Errorf("capacity_planning.generator.memory_mb must be > 0")
	}
	if settings.CapacityPlanning.Generator.NetworkMbps <= 0 {
		return fmt.Errorf("capacity_planning.generator.network_mbps must be > 0")
	}
	if settings.CapacityPlanning.Generator.MaxVUsers <= 0 {
		return fmt.Errorf("capacity_planning.generator.max_vusers must be > 0")
	}
	if settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators <= 0 {
		return fmt.Errorf("capacity_planning.availability.configured_available_generators must be > 0")
	}
	switch settings.CapacityPlanning.Availability.Source {
	case capacityAvailabilityConfigured, capacityAvailabilityConfiguredPlusLive:
	default:
		return fmt.Errorf("capacity_planning.availability.source must be one of %q or %q", capacityAvailabilityConfigured, capacityAvailabilityConfiguredPlusLive)
	}
	totalReserved := 0
	for _, reservation := range settings.CapacityPlanning.Reservations {
		if reservation.ReservedGenerators < 0 {
			return fmt.Errorf("capacity_planning.reservations.reserved_generators must be greater than or equal to 0")
		}
		if reservation.StartsAt != nil && reservation.EndsAt != nil && reservation.EndsAt.Before(*reservation.StartsAt) {
			return fmt.Errorf("capacity_planning.reservations.ends_at must not be before starts_at")
		}
		// Pool-scoped reservations are bounded by their fleet pool's member envelope
		// (enforced at admission), not the global configured pool, so they are
		// excluded from this global capacity sum.
		if strings.TrimSpace(reservation.PoolID) != "" {
			continue
		}
		if strings.TrimSpace(reservation.ProjectID) == "" || reservation.ReservedGenerators == 0 {
			continue
		}
		// Only always-active reservations are summed against total capacity here.
		// Time-windowed reservations may legitimately oversubscribe across
		// non-overlapping windows; the admission path clamps active reservations
		// to available capacity at runtime.
		if reservation.StartsAt == nil && reservation.EndsAt == nil {
			totalReserved += reservation.ReservedGenerators
		}
	}
	if totalReserved > settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators {
		return fmt.Errorf("capacity_planning.reservations cannot exceed configured available generators")
	}
	if err := validateNotificationsSettings(settings.Notifications); err != nil {
		return err
	}
	if err := validateArchiveRetentionSettings(settings.ArchiveRetention); err != nil {
		return err
	}
	if err := validateScheduleDefaultsSettings(settings.ScheduleDefaults); err != nil {
		return err
	}
	if err := validateAuthSettings(settings.Auth); err != nil {
		return err
	}
	if err := validateGitHubSettings(settings.GitHub); err != nil {
		return err
	}
	if err := validatePrometheusSettings(settings.Prometheus); err != nil {
		return err
	}
	if err := validateClientAppSettings(settings.ClientApp); err != nil {
		return err
	}
	return nil
}

func validateClientAppSettings(settings ClientAppSettings) error {
	switch normalizeClientAppSettings(settings).Availability {
	case clientAppEnabled, clientAppDisabled:
		return nil
	default:
		return fmt.Errorf("client_app.availability must be one of %q or %q", clientAppEnabled, clientAppDisabled)
	}
}

func validatePrometheusSettings(settings PrometheusSettings) error {
	normalized := normalizePrometheusSettings(settings)
	if normalized.TimeoutSeconds <= 0 {
		return fmt.Errorf("prometheus.timeout_seconds must be > 0")
	}
	if normalized.TimeoutSeconds > 60 {
		return fmt.Errorf("prometheus.timeout_seconds must be <= 60")
	}
	if !normalized.Enabled && strings.TrimSpace(normalized.BaseURL) == "" {
		return nil
	}
	if strings.TrimSpace(normalized.BaseURL) == "" {
		if normalized.Enabled {
			return fmt.Errorf("prometheus.base_url is required when prometheus is enabled")
		}
		return nil
	}
	allow := map[string]struct{}{}
	for _, host := range normalized.allowlistHosts() {
		allow[strings.ToLower(host)] = struct{}{}
	}
	if err := validateOutboundURLAllowingHosts(normalized.BaseURL, allow); err != nil {
		return fmt.Errorf("prometheus.base_url %w", err)
	}
	return nil
}

func validateGitHubSettings(settings GitHubSettings) error {
	if !settings.Enabled {
		return nil
	}
	if strings.TrimSpace(settings.ClientID) == "" && settings.AppID <= 0 && strings.TrimSpace(settings.PATEnvVar) == "" {
		return fmt.Errorf("github requires at least oauth, app, or PAT configuration when enabled")
	}
	if strings.TrimSpace(settings.ClientID) != "" && strings.TrimSpace(settings.ClientSecretEnvVar) == "" {
		return fmt.Errorf("github.client_secret_env_var is required when github.client_id is set")
	}
	if settings.AppID > 0 && strings.TrimSpace(settings.AppPrivateKeyEnvVar) == "" && strings.TrimSpace(settings.AppPrivateKeyFile) == "" {
		return fmt.Errorf("github.app_private_key_env_var or github.app_private_key_file is required when github.app_id is set")
	}
	switch strings.ToLower(strings.TrimSpace(settings.AutomationMode)) {
	case "", defaultGitHubAutoMode, "app", "oauth", "pat":
	default:
		return fmt.Errorf("github.automation_mode must be one of app_oauth_pat, app, oauth, or pat")
	}
	for label, raw := range map[string]string{"github.api_base_url": settings.APIBaseURL, "github.web_base_url": settings.WebBaseURL} {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			return fmt.Errorf("%s must be a valid http(s) URL", label)
		}
	}
	return nil
}

func defaultThresholdSettings() ThresholdSettings {
	return ThresholdSettings{
		MaxTelemetrySampleAgeMs:        float64Setting(defaultTelemetrySampleAgeBudgetMs),
		MaxTelemetryGeneratorLagMs:     float64Setting(defaultTelemetryGeneratorProcessingBudgetMs),
		MaxTelemetryTransportLagMs:     float64Setting(defaultTelemetryTransportBudgetMs),
		MaxTelemetryControllerFanoutMs: float64Setting(defaultTelemetryControllerFanoutBudgetMs),
		MaxTelemetryBrowserRenderLagMs: float64Setting(defaultTelemetryBrowserRenderBudgetMs),
	}
}

func normalizeThresholdSettings(in ThresholdSettings) ThresholdSettings {
	defaults := defaultThresholdSettings()
	out := in
	if out.MaxTelemetrySampleAgeMs == nil {
		out.MaxTelemetrySampleAgeMs = defaults.MaxTelemetrySampleAgeMs
	}
	if out.MaxTelemetryGeneratorLagMs == nil {
		out.MaxTelemetryGeneratorLagMs = defaults.MaxTelemetryGeneratorLagMs
	}
	if out.MaxTelemetryTransportLagMs == nil {
		out.MaxTelemetryTransportLagMs = defaults.MaxTelemetryTransportLagMs
	}
	if out.MaxTelemetryControllerFanoutMs == nil {
		out.MaxTelemetryControllerFanoutMs = defaults.MaxTelemetryControllerFanoutMs
	}
	if out.MaxTelemetryBrowserRenderLagMs == nil {
		out.MaxTelemetryBrowserRenderLagMs = defaults.MaxTelemetryBrowserRenderLagMs
	}
	return cloneThresholdSettings(out)
}

func cloneThresholdSettings(in ThresholdSettings) ThresholdSettings {
	out := in
	if in.MaxErrorRatePct != nil {
		v := *in.MaxErrorRatePct
		out.MaxErrorRatePct = &v
	}
	if in.MaxP99LatencyMs != nil {
		v := *in.MaxP99LatencyMs
		out.MaxP99LatencyMs = &v
	}
	if in.MaxAvgServerProcessingMs != nil {
		v := *in.MaxAvgServerProcessingMs
		out.MaxAvgServerProcessingMs = &v
	}
	if in.MaxAvgTransferMs != nil {
		v := *in.MaxAvgTransferMs
		out.MaxAvgTransferMs = &v
	}
	if in.MaxTelemetrySampleAgeMs != nil {
		v := *in.MaxTelemetrySampleAgeMs
		out.MaxTelemetrySampleAgeMs = &v
	}
	if in.MaxTelemetryGeneratorLagMs != nil {
		v := *in.MaxTelemetryGeneratorLagMs
		out.MaxTelemetryGeneratorLagMs = &v
	}
	if in.MaxTelemetryTransportLagMs != nil {
		v := *in.MaxTelemetryTransportLagMs
		out.MaxTelemetryTransportLagMs = &v
	}
	if in.MaxTelemetryControllerFanoutMs != nil {
		v := *in.MaxTelemetryControllerFanoutMs
		out.MaxTelemetryControllerFanoutMs = &v
	}
	if in.MaxTelemetryBrowserRenderLagMs != nil {
		v := *in.MaxTelemetryBrowserRenderLagMs
		out.MaxTelemetryBrowserRenderLagMs = &v
	}
	if in.FleetConstrainedAvailableSlots != nil {
		v := *in.FleetConstrainedAvailableSlots
		out.FleetConstrainedAvailableSlots = &v
	}
	if in.FleetSaturatedAvailableSlots != nil {
		v := *in.FleetSaturatedAvailableSlots
		out.FleetSaturatedAvailableSlots = &v
	}
	return out
}

func float64Setting(value float64) *float64 {
	v := value
	return &v
}

func validateOptionalPositiveThreshold(field string, value *float64) error {
	if value != nil && *value <= 0 {
		return fmt.Errorf("%s must be > 0", field)
	}
	return nil
}

func validateOptionalNonNegativeIntThreshold(field string, value *int) error {
	if value != nil && *value < 0 {
		return fmt.Errorf("%s must be >= 0", field)
	}
	return nil
}

func normalizeCapacityReservations(reservations []CapacityReservation) []CapacityReservation {
	out := make([]CapacityReservation, 0, len(reservations))
	for _, reservation := range reservations {
		rawProjectID := strings.TrimSpace(reservation.ProjectID)
		if rawProjectID == "" || reservation.ReservedGenerators == 0 {
			continue
		}
		// Drop reservations with an inverted window (ends before it starts); they
		// can never be active and would otherwise silently reserve nothing.
		if reservation.StartsAt != nil && reservation.EndsAt != nil && reservation.EndsAt.Before(*reservation.StartsAt) {
			continue
		}
		out = append(out, CapacityReservation{
			ProjectID:          normalizeProjectID(rawProjectID),
			ReservedGenerators: reservation.ReservedGenerators,
			PoolID:             strings.TrimSpace(reservation.PoolID),
			StartsAt:           reservation.StartsAt,
			EndsAt:             reservation.EndsAt,
		})
	}
	return out
}

// reservationActiveAt reports whether a reservation's timeslot is active at the
// given instant. Open-ended bounds are treated as unbounded.
func reservationActiveAt(reservation CapacityReservation, now time.Time) bool {
	if reservation.StartsAt != nil && now.Before(*reservation.StartsAt) {
		return false
	}
	if reservation.EndsAt != nil && now.After(*reservation.EndsAt) {
		return false
	}
	return true
}

func normalizeArchiveRetentionPolicies(policies []ArchiveRetentionPolicy) []ArchiveRetentionPolicy {
	out := make([]ArchiveRetentionPolicy, 0, len(policies))
	for _, policy := range policies {
		normalized := ArchiveRetentionPolicy{
			Name:            strings.TrimSpace(policy.Name),
			ProjectID:       strings.TrimSpace(policy.ProjectID),
			ReleaseID:       strings.TrimSpace(policy.ReleaseID),
			RunStatus:       strings.ToUpper(strings.TrimSpace(policy.RunStatus)),
			RunType:         strings.TrimSpace(policy.RunType),
			ArtifactType:    strings.TrimSpace(policy.ArtifactType),
			ComplianceClass: strings.TrimSpace(policy.ComplianceClass),
			Reason:          strings.TrimSpace(policy.Reason),
			RetentionDays:   policy.RetentionDays,
		}
		if normalized.ArtifactType == "" {
			normalized.ArtifactType = "bundle"
		}
		if normalized.ComplianceClass == "" {
			normalized.ComplianceClass = "standard"
		}
		if normalized.Reason == "" {
			normalized.Reason = "expired_by_policy"
		}
		out = append(out, normalized)
	}
	return out
}

func normalizeArchiveRetentionSettings(settings ArchiveRetentionSettings) ArchiveRetentionSettings {
	if settings.DefaultRetentionDays == 0 {
		settings.DefaultRetentionDays = defaultSystemSettings(0).ArchiveRetention.DefaultRetentionDays
	}
	settings.Policies = normalizeArchiveRetentionPolicies(settings.Policies)
	return settings
}

func validateArchiveRetentionSettings(settings ArchiveRetentionSettings) error {
	if settings.DefaultRetentionDays < 1 {
		return fmt.Errorf("archive_retention.default_retention_days must be greater than 0")
	}
	for index, policy := range settings.Policies {
		if policy.RetentionDays < 1 {
			return fmt.Errorf("archive_retention.policies[%d].retention_days must be greater than 0", index)
		}
	}
	return nil
}

func defaultScheduleDefaultsSettings() ScheduleDefaultsSettings {
	return ScheduleDefaultsSettings{
		ApprovalPolicy:  "auto",
		MissedRunPolicy: "skip",
	}
}

func normalizeScheduleDefaultsSettings(settings ScheduleDefaultsSettings) ScheduleDefaultsSettings {
	return ScheduleDefaultsSettings{
		ApprovalPolicy:        normalizeScenarioScheduleApprovalPolicy(settings.ApprovalPolicy),
		MissedRunPolicy:       normalizeScenarioScheduleMissedRunPolicy(settings.MissedRunPolicy),
		BlackoutStartTime:     strings.TrimSpace(settings.BlackoutStartTime),
		BlackoutEndTime:       strings.TrimSpace(settings.BlackoutEndTime),
		AvailabilityStartTime: strings.TrimSpace(settings.AvailabilityStartTime),
		AvailabilityEndTime:   strings.TrimSpace(settings.AvailabilityEndTime),
	}
}

func validateScheduleDefaultsSettings(settings ScheduleDefaultsSettings) error {
	switch strings.ToLower(strings.TrimSpace(settings.ApprovalPolicy)) {
	case "", "auto", "manual", "manual_review", "high_cost", "off_hours", "production", "quota_burst":
	default:
		return fmt.Errorf("schedule_defaults.approval_policy is invalid")
	}
	switch strings.ToLower(strings.TrimSpace(settings.MissedRunPolicy)) {
	case "", "skip", "catch_up_once", "catch_up_all", "require_operator_decision":
	default:
		return fmt.Errorf("schedule_defaults.missed_run_policy is invalid")
	}
	if err := validateScheduleDefaultTimePair("blackout", settings.BlackoutStartTime, settings.BlackoutEndTime); err != nil {
		return err
	}
	if err := validateScheduleDefaultTimePair("availability", settings.AvailabilityStartTime, settings.AvailabilityEndTime); err != nil {
		return err
	}
	return nil
}

func validateScheduleDefaultTimePair(label string, start string, end string) error {
	start = strings.TrimSpace(start)
	end = strings.TrimSpace(end)
	if (start == "") != (end == "") {
		return fmt.Errorf("schedule_defaults.%s window requires both start and end times", label)
	}
	for _, item := range []struct {
		field string
		value string
	}{
		{field: "start_time", value: start},
		{field: "end_time", value: end},
	} {
		if item.value == "" {
			continue
		}
		if !validScheduleDefaultClock(item.value) {
			return fmt.Errorf("schedule_defaults.%s_%s must use HH:MM", label, item.field)
		}
	}
	return nil
}

func validScheduleDefaultClock(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return false
	}
	hour := -1
	minute := -1
	if _, err := fmt.Sscanf(value, "%02d:%02d", &hour, &minute); err != nil {
		return false
	}
	return hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59
}

func validateAuthSettings(settings AuthSettings) error {
	mode := strings.TrimSpace(settings.Mode)
	if mode == authModeDisabled {
		mode = authModeDev
	}

	if productionMode() && isAuthBypassMode(mode) {
		return fmt.Errorf("auth.mode %q bypasses authentication and is not permitted in production; set auth.mode to %q or %q (or unset APEXLOAD_ENV/APEXLOAD_REQUIRE_AUTH)", strings.TrimSpace(settings.Mode), authModeOIDC, authModeTrustedHeaders)
	}

	switch mode {
	case "", authModeDev:
		return nil
	case authModeTrustedHeaders:
		if settings.UserHeader == "" {
			return fmt.Errorf("auth.user_header is required when auth.mode=%q", authModeTrustedHeaders)
		}
		if settings.RolesHeader == "" && settings.ProjectRolesHeader == "" {
			return fmt.Errorf("auth.roles_header or auth.project_roles_header is required when auth.mode=%q", authModeTrustedHeaders)
		}
		if err := validateOptionalAuthURL("login_url", settings.LoginURL); err != nil {
			return err
		}
		if err := validateOptionalAuthURL("logout_url", settings.LogoutURL); err != nil {
			return err
		}
		return nil
	case authModeOIDC:
		if settings.IssuerURL == "" {
			return fmt.Errorf("auth.issuer_url is required when auth.mode=%q", authModeOIDC)
		}
		if settings.ClientID == "" {
			return fmt.Errorf("auth.client_id is required when auth.mode=%q", authModeOIDC)
		}
		if settings.ClientSecretEnvVar == "" {
			return fmt.Errorf("auth.client_secret_env_var is required when auth.mode=%q", authModeOIDC)
		}
		if settings.RedirectURL == "" {
			return fmt.Errorf("auth.redirect_url is required when auth.mode=%q", authModeOIDC)
		}
		if settings.SessionSecretEnvVar == "" {
			return fmt.Errorf("auth.session_secret_env_var is required when auth.mode=%q", authModeOIDC)
		}
		if err := validateRequiredAuthURL("issuer_url", settings.IssuerURL); err != nil {
			return err
		}
		if err := validateRequiredAuthURL("redirect_url", settings.RedirectURL); err != nil {
			return err
		}
		if err := validateOptionalAuthURL("login_url", settings.LoginURL); err != nil {
			return err
		}
		if err := validateOptionalAuthURL("logout_url", settings.LogoutURL); err != nil {
			return err
		}
		if len(normalizeAuthScopes(settings.Scopes)) == 0 {
			return fmt.Errorf("auth.scopes must contain at least one scope when auth.mode=%q", authModeOIDC)
		}
		return nil
	default:
		return fmt.Errorf("auth.mode must be one of %q, %q, or %q", authModeDev, authModeTrustedHeaders, authModeOIDC)
	}
}

func normalizeAuthScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		trimmed := strings.TrimSpace(scope)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func validateRequiredAuthURL(field string, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("auth.%s is required", field)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("auth.%s must be an absolute http(s) URL", field)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("auth.%s must be an absolute http(s) URL", field)
	}
	return nil
}

func validateOptionalAuthURL(field string, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "/") {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("auth.%s must be an absolute http(s) URL or absolute path", field)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("auth.%s must be an absolute http(s) URL or absolute path", field)
	}
	return nil
}

func (tr *TestRunner) loadSystemSettings(defaultMaxConcurrent int) {
	tr.settings = defaultSystemSettings(defaultMaxConcurrent)
	defer tr.setNotificationSettings(tr.settings.Notifications)
	if tr.settingsStore == nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}

	raw, err := tr.settingsStore.GetSetting(systemSettingsStoreKey)
	if err != nil || raw == "" {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}

	var loaded SystemSettings
	if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}
	// Migration: a settings blob persisted before the Prometheus section existed
	// has no "prometheus" key, so it unmarshals to the zero value. Seed it from
	// the env-derived defaults (which default-enable when APEXLOAD_PROMETHEUS_URL
	// is set) so legacy installs still light up the graph pack. A blob that DOES
	// carry the section is taken as the admin's explicit choice — including an
	// explicit disable.
	if !rawJSONHasKey(raw, "prometheus") {
		loaded.Prometheus = defaultPrometheusSettings()
	}
	// Migration: a blob persisted before client_downloads existed has no key, so
	// it unmarshals to {Enabled:false}. Seed the enabled default so existing
	// installs keep offering client software; a blob that carries the section is
	// the admin's explicit choice (including an explicit disable).
	if !rawJSONHasKey(raw, "client_downloads") {
		loaded.ClientDownloads = defaultClientDownloadsSettings()
	}
	loaded = normalizeLoadedSystemSettings(loaded, defaultMaxConcurrent)
	if err := validateSystemSettings(loaded); err != nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}
	tr.settings = cloneSystemSettings(loaded)
	tr.maxTests = tr.settings.General.MaxConcurrentTests
}

func (tr *TestRunner) GetSystemSettings() SystemSettings {
	tr.refreshAuthSettingsFromStore()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return cloneSystemSettings(tr.settings)
}

// refreshAuthSettingsFromStore keeps authentication policy consistent across
// HA replicas without replacing unrelated settings that require migration
// defaults or local runtime derivation.
func (tr *TestRunner) refreshAuthSettingsFromStore() {
	if tr == nil || tr.settingsStore == nil {
		return
	}
	tr.settingsRefreshMu.Lock()
	now := time.Now()
	if !tr.settingsAuthRefreshAt.IsZero() && now.Sub(tr.settingsAuthRefreshAt) < systemSettingsAuthRefreshInterval {
		tr.settingsRefreshMu.Unlock()
		return
	}
	tr.settingsAuthRefreshAt = now
	defer tr.settingsRefreshMu.Unlock()
	raw, err := tr.settingsStore.GetSetting(systemSettingsStoreKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return
	}
	var loaded SystemSettings
	if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
		return
	}
	auth := normalizeAuthSettings(loaded.Auth)
	if err := validateAuthSettings(auth); err != nil {
		return
	}
	tr.mu.Lock()
	tr.settings.Auth = auth
	tr.mu.Unlock()
}

func redactedSystemSettingsResponse(settings SystemSettings) map[string]any {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return map[string]any{}
	}
	var response map[string]any
	if err := json.Unmarshal(encoded, &response); err != nil {
		return map[string]any{}
	}
	notifications, _ := response["notifications"].(map[string]any)
	webhooks, _ := notifications["webhooks"].([]any)
	for _, item := range webhooks {
		webhook, ok := item.(map[string]any)
		if !ok {
			continue
		}
		apiToken, _ := webhook["api_token"].(string)
		if strings.TrimSpace(apiToken) != "" {
			webhook[redactedSecretConfiguredField] = true
		}
		webhook["api_token"] = ""
	}
	return response
}

func preserveWriteOnlySystemSettingsSecrets(next SystemSettings, current SystemSettings) SystemSettings {
	out := cloneSystemSettings(next)
	for index := range out.Notifications.Webhooks {
		webhook := &out.Notifications.Webhooks[index]
		if webhook.Format != notificationFormatDynatrace || strings.TrimSpace(webhook.APIToken) != "" {
			continue
		}
		if index < len(current.Notifications.Webhooks) {
			currentWebhook := current.Notifications.Webhooks[index]
			if currentWebhook.Format == notificationFormatDynatrace && strings.TrimSpace(currentWebhook.APIToken) != "" {
				webhook.APIToken = currentWebhook.APIToken
			}
		}
	}
	return out
}

func (tr *TestRunner) setNotificationSettings(settings NotificationsSettings) {
	if tr == nil {
		return
	}
	tr.notifications.Store(cloneNotificationsSettings(settings))
}

func (tr *TestRunner) currentNotificationSettings() NotificationsSettings {
	if tr == nil {
		return NotificationsSettings{}
	}
	stored := tr.notifications.Load()
	if stored == nil {
		return NotificationsSettings{}
	}
	settings, ok := stored.(NotificationsSettings)
	if !ok {
		return NotificationsSettings{}
	}
	return cloneNotificationsSettings(settings)
}

func (tr *TestRunner) UpdateSystemSettings(settings SystemSettings) (SystemSettings, error) {
	normalized := cloneSystemSettings(settings)
	normalized.Thresholds = normalizeThresholdSettings(normalized.Thresholds)
	normalized.ArchiveRetention = normalizeArchiveRetentionSettings(normalized.ArchiveRetention)
	normalized.Auth = normalizeAuthSettings(normalized.Auth)
	normalized.Prometheus = normalizePrometheusSettings(normalized.Prometheus)
	normalized.ClientApp = normalizeClientAppSettings(normalized.ClientApp)
	normalized.ClientDownloads = normalizeClientDownloadsSettings(normalized.ClientDownloads)
	if err := validateSystemSettings(normalized); err != nil {
		return SystemSettings{}, err
	}
	// Reject a pool-scoped reservation that points at a fleet pool which does not
	// exist (write-time only — load-time validation must tolerate a pool deleted
	// after the reservation was created).
	if err := tr.validateReservationPools(normalized); err != nil {
		return SystemSettings{}, err
	}

	normalized = cloneSystemSettings(normalized)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return SystemSettings{}, fmt.Errorf("encode settings: %w", err)
	}

	// Serialize the durable write and in-memory install with refresh so a refresh
	// that read the prior blob cannot overwrite this update afterward.
	tr.settingsRefreshMu.Lock()
	defer tr.settingsRefreshMu.Unlock()
	if tr.settingsStore != nil {
		if err := tr.settingsStore.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
			return SystemSettings{}, fmt.Errorf("persist settings: %w", err)
		}
	}

	tr.mu.Lock()
	tr.settings = normalized
	tr.maxTests = normalized.General.MaxConcurrentTests
	// Capacity advisories are derived from settings (generator specs, headroom,
	// configured reservations), so a settings change must invalidate the cache.
	tr.invalidateCapacityAdvisoryCacheLocked()
	tr.mu.Unlock()
	tr.settingsAuthRefreshAt = time.Now()
	tr.setNotificationSettings(normalized.Notifications)

	return normalized, nil
}
