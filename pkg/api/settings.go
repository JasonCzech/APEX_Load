package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const systemSettingsStoreKey = "system_settings"

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
	Notifications    NotificationsSettings    `json:"notifications"`
	Auth             AuthSettings             `json:"auth"`
	GitHub           GitHubSettings           `json:"github"`
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

const (
	defaultGitHubAPIBaseURL = "https://api.github.com"
	defaultGitHubWebBaseURL = "https://github.com"
	defaultGitHubBranch     = "main"
	defaultGitHubPathTmpl   = "apexload/scripts/{project}/{ref_key}.json"
	defaultGitHubAutoMode   = "app_oauth_pat"
	githubTokenKeyEnvVar    = "APEXLOAD_GITHUB_TOKEN_KEY"
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
		Notifications: NotificationsSettings{},
		Auth:          defaultAuthSettings(),
		GitHub:        defaultGitHubSettings(),
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
	out.Notifications = cloneNotificationsSettings(in.Notifications)
	if in.CapacityPlanning.Reservations != nil {
		out.CapacityPlanning.Reservations = append([]CapacityReservation(nil), in.CapacityPlanning.Reservations...)
	}
	if in.ArchiveRetention.Policies != nil {
		out.ArchiveRetention.Policies = append([]ArchiveRetentionPolicy(nil), in.ArchiveRetention.Policies...)
	}
	out.Auth = in.Auth
	if in.Auth.Scopes != nil {
		out.Auth.Scopes = append([]string(nil), in.Auth.Scopes...)
	}
	out.GitHub = in.GitHub
	if in.GitHub.Scopes != nil {
		out.GitHub.Scopes = append([]string(nil), in.GitHub.Scopes...)
	}
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
	out.CapacityPlanning.Reservations = normalizeCapacityReservations(out.CapacityPlanning.Reservations)
	out.Thresholds = normalizeThresholdSettings(out.Thresholds)
	out.Notifications = cloneNotificationsSettings(out.Notifications)
	out.Auth = normalizeAuthSettings(out.Auth)
	out.GitHub = normalizeGitHubSettings(out.GitHub)

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
	if err := validateAuthSettings(settings.Auth); err != nil {
		return err
	}
	if err := validateGitHubSettings(settings.GitHub); err != nil {
		return err
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
	loaded = normalizeLoadedSystemSettings(loaded, defaultMaxConcurrent)
	if err := validateSystemSettings(loaded); err != nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}
	tr.settings = cloneSystemSettings(loaded)
	tr.maxTests = tr.settings.General.MaxConcurrentTests
}

func (tr *TestRunner) GetSystemSettings() SystemSettings {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return cloneSystemSettings(tr.settings)
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
	if err := validateSystemSettings(normalized); err != nil {
		return SystemSettings{}, err
	}

	normalized = cloneSystemSettings(normalized)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return SystemSettings{}, fmt.Errorf("encode settings: %w", err)
	}

	if tr.settingsStore != nil {
		if err := tr.settingsStore.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
			return SystemSettings{}, fmt.Errorf("persist settings: %w", err)
		}
	}

	tr.mu.Lock()
	tr.settings = normalized
	tr.maxTests = normalized.General.MaxConcurrentTests
	tr.mu.Unlock()
	tr.setNotificationSettings(normalized.Notifications)

	return normalized, nil
}
