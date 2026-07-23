package api

import (
	"fmt"
	"net/url"
	"strings"
)

// PowerBISettings is the global (admin-owned) configuration for publishing run
// results to a Microsoft Power BI push dataset. The Azure AD client secret is
// never stored here — only the name of the environment variable that holds it,
// mirroring GitHubSettings.ClientSecretEnvVar. Auth/API base URLs default to
// the public cloud but are pointed at sovereign or internal endpoints in
// on-prem deployments.
type PowerBISettings struct {
	Enabled            bool   `json:"enabled"`
	TenantID           string `json:"tenant_id,omitempty"`
	ClientID           string `json:"client_id,omitempty"`
	ClientSecretEnvVar string `json:"client_secret_env_var,omitempty"`
	WorkspaceID        string `json:"workspace_id,omitempty"`
	DatasetName        string `json:"dataset_name,omitempty"`
	AuthBaseURL        string `json:"auth_base_url,omitempty"`
	APIBaseURL         string `json:"api_base_url,omitempty"`
	Scope              string `json:"scope,omitempty"`
	TimeoutSeconds     int    `json:"timeout_seconds,omitempty"`
	// DashboardBaseURL, when set, is the absolute base used for the
	// apexload_url deep links pushed into the dataset (e.g.
	// https://apexload.example.com). When empty the publish request's own
	// Host header is used after strict syntactic validation.
	DashboardBaseURL string `json:"dashboard_base_url,omitempty"`
}

const (
	defaultPowerBIAuthBaseURL = "https://login.microsoftonline.com"
	defaultPowerBIAPIBaseURL  = "https://api.powerbi.com/v1.0/myorg"
	defaultPowerBIScope       = "https://analysis.windows.net/powerbi/api/.default"
	defaultPowerBIDatasetName = "APEX Load Performance Runs"

	powerBIInsecureHTTPEnv = "APEXLOAD_POWERBI_ALLOW_INSECURE_HTTP"
)

func defaultPowerBISettings() PowerBISettings {
	return PowerBISettings{
		Enabled:        false,
		DatasetName:    defaultPowerBIDatasetName,
		AuthBaseURL:    defaultPowerBIAuthBaseURL,
		APIBaseURL:     defaultPowerBIAPIBaseURL,
		Scope:          defaultPowerBIScope,
		TimeoutSeconds: 30,
	}
}

func normalizePowerBISettings(in PowerBISettings) PowerBISettings {
	defaults := defaultPowerBISettings()
	out := in
	out.TenantID = strings.TrimSpace(out.TenantID)
	out.ClientID = strings.TrimSpace(out.ClientID)
	out.ClientSecretEnvVar = strings.TrimSpace(out.ClientSecretEnvVar)
	out.WorkspaceID = strings.TrimSpace(out.WorkspaceID)
	out.DatasetName = strings.TrimSpace(out.DatasetName)
	out.AuthBaseURL = strings.TrimRight(strings.TrimSpace(out.AuthBaseURL), "/")
	out.APIBaseURL = strings.TrimRight(strings.TrimSpace(out.APIBaseURL), "/")
	out.Scope = strings.TrimSpace(out.Scope)
	out.DashboardBaseURL = strings.TrimRight(strings.TrimSpace(out.DashboardBaseURL), "/")
	if out.DatasetName == "" {
		out.DatasetName = defaults.DatasetName
	}
	if out.AuthBaseURL == "" {
		out.AuthBaseURL = defaults.AuthBaseURL
	}
	if out.APIBaseURL == "" {
		out.APIBaseURL = defaults.APIBaseURL
	}
	if out.Scope == "" {
		out.Scope = defaults.Scope
	}
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = defaults.TimeoutSeconds
	}
	return out
}

// clientSecret resolves the Azure AD client secret at request time. Only the
// environment-variable name is persisted; the secret value never crosses the
// settings API or a durable storage boundary.
func (p PowerBISettings) clientSecret() string {
	if strings.TrimSpace(p.ClientSecretEnvVar) == "" {
		return ""
	}
	return strings.TrimSpace(envValue(p.ClientSecretEnvVar))
}

// allowlistHosts returns the hosts of the two configured endpoints. These are
// the only hosts a Power BI publish may reach, and they are also permitted to
// resolve to private address space so internal/sovereign deployments work.
func (p PowerBISettings) allowlistHosts() []string {
	hosts := make([]string, 0, 2)
	for _, raw := range []string{p.AuthBaseURL, p.APIBaseURL} {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		hosts = append(hosts, parsed.Hostname())
	}
	return hosts
}

func powerBIGUIDValid(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 5 {
		return false
	}
	lengths := []int{8, 4, 4, 4, 12}
	for i, part := range parts {
		if len(part) != lengths[i] {
			return false
		}
		for _, r := range part {
			if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
				continue
			}
			return false
		}
	}
	return true
}

func validatePowerBISettings(settings PowerBISettings) error {
	settings = normalizePowerBISettings(settings)
	if settings.TimeoutSeconds <= 0 || settings.TimeoutSeconds > 60 {
		return fmt.Errorf("powerbi.timeout_seconds must be between 1 and 60")
	}
	if !validSecretEnvVarName(settings.ClientSecretEnvVar) {
		return fmt.Errorf("powerbi.client_secret_env_var must be a valid environment variable name")
	}
	if !settings.Enabled {
		return nil
	}
	if settings.TenantID == "" || settings.ClientID == "" || settings.ClientSecretEnvVar == "" || settings.WorkspaceID == "" {
		return fmt.Errorf("powerbi.tenant_id, powerbi.client_id, powerbi.client_secret_env_var, and powerbi.workspace_id are required when powerbi is enabled")
	}
	if !powerBIGUIDValid(settings.WorkspaceID) {
		return fmt.Errorf("powerbi.workspace_id must be a workspace GUID")
	}
	// The allowlist is deliberately derived from the URLs under validation, so
	// the host portion of validateOutboundURLAllowingHosts can never fail here —
	// that is the knob letting sovereign/internal Power BI endpoints resolve to
	// private address space (GHES precedent). Only the scheme/userinfo checks
	// bite at save time; the same hosts become the publish client's dial
	// allowlist, which is what actually constrains egress.
	allow := map[string]struct{}{}
	for _, host := range settings.allowlistHosts() {
		allow[strings.ToLower(host)] = struct{}{}
	}
	if settings.DashboardBaseURL != "" {
		linkAllow := map[string]struct{}{}
		parsed, parseErr := url.Parse(settings.DashboardBaseURL)
		if parseErr == nil && parsed.Hostname() != "" {
			linkAllow[strings.ToLower(parsed.Hostname())] = struct{}{}
		}
		if err := validateOutboundURLAllowingHosts(settings.DashboardBaseURL, linkAllow); err != nil {
			return fmt.Errorf("powerbi.dashboard_base_url %w", err)
		}
		// The base is concatenated with /tests/{id}/report into durable links;
		// a query or fragment would corrupt every published URL.
		if parsed != nil && (parsed.RawQuery != "" || parsed.Fragment != "") {
			return fmt.Errorf("powerbi.dashboard_base_url must not contain a query string or fragment")
		}
	}
	for label, raw := range map[string]string{
		"powerbi.auth_base_url": settings.AuthBaseURL,
		"powerbi.api_base_url":  settings.APIBaseURL,
	} {
		if err := validateOutboundURLAllowingHosts(raw, allow); err != nil {
			return fmt.Errorf("%s %w", label, err)
		}
		if productionMode() {
			parsed, _ := url.Parse(raw)
			if parsed == nil || !strings.EqualFold(parsed.Scheme, "https") {
				return fmt.Errorf("%s must use https in production", label)
			}
		}
	}
	return nil
}
