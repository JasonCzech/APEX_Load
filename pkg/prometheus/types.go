// Package prometheus provides bounded, read-only PromQL target-system monitoring.
// It does not scrape or reconfigure load generators or Prometheus servers.
package prometheus

import (
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

const (
	BackendPrometheus   = "prometheus"
	BackendAzureManaged = "azure_managed"
	AuthNone            = "none"
	AuthBasic           = "basic"
	AuthBearer          = "bearer"
	AuthAzure           = "azure"
	AzureScope          = "https://prometheus.monitor.azure.com/.default"
	MaxJobConfigBytes   = 64 << 10
)

type Config struct {
	Endpoint            string   `json:"endpoint"`
	ContextPath         string   `json:"context_path,omitempty"`
	BackendProfile      string   `json:"backend_profile,omitempty"`
	AuthType            string   `json:"auth_type,omitempty"`
	AllowPrivateHTTP    bool     `json:"allow_private_http,omitempty"`
	ApprovedCIDRs       []string `json:"approved_cidrs,omitempty"`
	CAPEM               string   `json:"ca_pem,omitempty"`
	MTLS                bool     `json:"mtls,omitempty"`
	ClientID            string   `json:"client_id,omitempty"`
	AzureTenantID       string   `json:"azure_tenant_id,omitempty"`
	AzureCredentialMode string   `json:"azure_credential_mode,omitempty"`
	TimeoutSeconds      int      `json:"timeout_seconds,omitempty"`
	RequestsPerMinute   int      `json:"requests_per_minute,omitempty"`
	MaxResponseBytes    int      `json:"max_response_bytes,omitempty"`
	MaxSeries           int      `json:"max_series,omitempty"`
	MaxPoints           int      `json:"max_points,omitempty"`
	MaxRequestsPerPoll  int      `json:"max_requests_per_poll,omitempty"`
	MaxPollSeconds      int      `json:"max_poll_seconds,omitempty"`
}

// Credentials are runtime only, never public source, grant or operation JSON.
type Credentials struct {
	Token             string `json:"-"`
	Username          string `json:"-"`
	Password          string `json:"-"`
	ClientSecret      string `json:"-"`
	ClientCertificate string `json:"-"`
	ClientKey         string `json:"-"`
}

func (c Config) Defaults() Config {
	if c.BackendProfile == "" {
		c.BackendProfile = BackendPrometheus
	}
	if c.AuthType == "" {
		c.AuthType = AuthNone
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 10
	}
	if c.RequestsPerMinute == 0 {
		c.RequestsPerMinute = 60
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 4 << 20
	}
	if c.MaxSeries == 0 {
		c.MaxSeries = 200
	}
	if c.MaxPoints == 0 {
		c.MaxPoints = 10000
	}
	if c.MaxRequestsPerPoll == 0 {
		c.MaxRequestsPerPoll = 30
	}
	if c.MaxPollSeconds == 0 {
		c.MaxPollSeconds = 30
	}
	return c
}

var identityRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
var labelRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func (c Config) Validate() error {
	if err := validateJSONSize(c); err != nil {
		return err
	}
	c = c.Defaults()
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" || strings.ContainsAny(c.Endpoint, "?#\r\n\x00") || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("endpoint must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	if len(c.Endpoint) > 2048 || len(c.ContextPath) > 256 || strings.ContainsAny(c.ContextPath, "?#%\\\r\n\x00") || c.ContextPath != "" && (!strings.HasPrefix(c.ContextPath, "/") || strings.Contains(c.ContextPath, "..") || strings.HasSuffix(c.ContextPath, "/")) {
		return errors.New("invalid Prometheus context path")
	}
	if u.Scheme == "http" && (!c.AllowPrivateHTTP || len(c.ApprovedCIDRs) == 0 || c.BackendProfile != BackendPrometheus || c.AuthType != AuthNone || c.MTLS) {
		return errors.New("HTTP requires explicit private-network approval and CIDRs")
	}
	if c.BackendProfile != BackendPrometheus && c.BackendProfile != BackendAzureManaged {
		return errors.New("unsupported Prometheus backend profile")
	}
	if c.AuthType != AuthNone && c.AuthType != AuthBasic && c.AuthType != AuthBearer && c.AuthType != AuthAzure {
		return errors.New("unsupported Prometheus authentication type")
	}
	if c.MTLS && u.Scheme != "https" {
		return errors.New("mTLS requires HTTPS")
	}
	if c.AuthType == AuthAzure && c.BackendProfile != BackendAzureManaged || c.BackendProfile == BackendAzureManaged && c.AuthType != AuthAzure {
		return errors.New("Azure managed profile requires Azure authentication")
	}
	if c.BackendProfile == BackendAzureManaged {
		host := strings.ToLower(u.Hostname())
		if u.Scheme != "https" || u.Port() != "" && u.Port() != "443" || !strings.HasSuffix(host, ".prometheus.monitor.azure.com") || strings.Count(host, ".") < 5 || c.ContextPath != "" {
			return errors.New("Azure managed endpoint must be the workspace query origin")
		}
		switch c.AzureCredentialMode {
		case "workload_identity":
			if c.AzureTenantID == "" || c.ClientID == "" {
				return errors.New("Azure workload identity requires saved tenant and client IDs")
			}
		case "managed_identity":
		case "service_principal":
			if c.AzureTenantID == "" || c.ClientID == "" {
				return errors.New("Azure service principal requires tenant and client IDs")
			}
		default:
			return errors.New("select an explicit Azure credential mode")
		}
	}
	if len(c.ClientID) > 256 || len(c.AzureTenantID) > 256 || strings.ContainsAny(c.ClientID+c.AzureTenantID, "\r\n\x00") || len(c.CAPEM) > 65536 || len(c.ApprovedCIDRs) > 32 {
		return errors.New("configuration exceeds bounds")
	}
	for _, v := range c.ApprovedCIDRs {
		if _, err := netip.ParsePrefix(v); err != nil {
			return errors.New("invalid approved CIDR")
		}
	}
	for _, b := range []struct{ v, lo, hi int }{{c.TimeoutSeconds, 1, 30}, {c.RequestsPerMinute, 1, 60}, {c.MaxResponseBytes, 1024, 4 << 20}, {c.MaxSeries, 1, 200}, {c.MaxPoints, 1, 10000}, {c.MaxRequestsPerPoll, 1, 60}, {c.MaxPollSeconds, 1, 30}} {
		if b.v < b.lo || b.v > b.hi {
			return errors.New("collection limit outside supported bounds")
		}
	}
	if c.MaxPoints < c.MaxSeries {
		return errors.New("point budget must accommodate at least one evaluation of the selected series limit")
	}
	return nil
}

// Parameter is a saved, typed administrator binding, frozen with the source revision.
// Values are never interpreted as arbitrary PromQL text.
type Parameter struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type Query struct {
	ID                  string               `json:"id"`
	Name                string               `json:"name"`
	Expression          string               `json:"expression"`
	Unit                string               `json:"unit,omitempty"`
	Semantics           string               `json:"semantics,omitempty"`
	AllowedLabels       []string             `json:"allowed_labels,omitempty"`
	MinSeries           int                  `json:"min_series,omitempty"`
	FreshnessExpression string               `json:"freshness_expression,omitempty"`
	MaxSampleAgeSeconds int                  `json:"max_sample_age_seconds,omitempty"`
	TerminalExpression  string               `json:"terminal_expression,omitempty"`
	Parameters          map[string]Parameter `json:"parameters,omitempty"`
}
type Selection struct {
	CollectionID     string     `json:"collection_id,omitempty"`
	ProjectID        string     `json:"project_id"`
	Queries          []Query    `json:"queries"`
	EvidenceSelector string     `json:"evidence_selector,omitempty"`
	Targets          bool       `json:"targets,omitempty"`
	Alerts           bool       `json:"alerts,omitempty"`
	SettleSeconds    int        `json:"settle_seconds,omitempty"`
	StepSeconds      int        `json:"step_seconds,omitempty"`
	QueryOffset      int        `json:"query_offset,omitempty"`
	GridAnchor       *time.Time `json:"grid_anchor,omitempty"`
	QueryTime        *time.Time `json:"query_time,omitempty"`
}

func (s Selection) Defaults() Selection {
	if s.StepSeconds == 0 {
		s.StepSeconds = 15
	}
	if s.SettleSeconds == 0 {
		s.SettleSeconds = 120
	}
	return s
}
func (s Selection) Validate() error {
	if err := validateJSONSize(s); err != nil {
		return err
	}
	s = s.Defaults()
	if len(s.CollectionID) > 256 || strings.ContainsAny(s.CollectionID, "\r\n\x00") {
		return errors.New("invalid monitoring collection identity")
	}
	if s.ProjectID == "" || len(s.ProjectID) > 256 || len(s.Queries) == 0 || len(s.Queries) > 20 || s.QueryOffset < 0 || s.QueryOffset > 256 || s.StepSeconds < 1 || s.StepSeconds > 3600 || s.SettleSeconds < 0 || s.SettleSeconds > 300 {
		return errors.New("invalid Prometheus selection or limits")
	}
	if s.GridAnchor != nil && (s.GridAnchor.IsZero() || s.GridAnchor.Location() != time.UTC || s.GridAnchor.Nanosecond()%1000000 != 0) {
		return errors.New("grid anchor must be a UTC millisecond timestamp")
	}
	if s.QueryTime != nil && (s.QueryTime.IsZero() || s.QueryTime.Location() != time.UTC) {
		return errors.New("query continuation time must be UTC")
	}
	if s.Targets || s.Alerts {
		if _, err := parseEvidenceSelector(s.EvidenceSelector); err != nil {
			return err
		}
	} else if s.EvidenceSelector != "" {
		if _, err := parseEvidenceSelector(s.EvidenceSelector); err != nil {
			return err
		}
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, q := range s.Queries {
		if !identityRE.MatchString(q.ID) || reservedQueryID(q.ID) || !identityRE.MatchString(q.Name) || ids[q.ID] || names[q.Name] || len(q.Unit) > 64 || strings.ContainsAny(q.Unit, "\r\n\x00") || len(q.AllowedLabels) > 12 || q.MinSeries < 0 || q.MinSeries > 200 || q.MaxSampleAgeSeconds < 0 || q.MaxSampleAgeSeconds > 86400 {
			return errors.New("invalid or duplicate Prometheus query definition")
		}
		ids[q.ID] = true
		names[q.Name] = true
		seen := map[string]bool{}
		for _, l := range q.AllowedLabels {
			if len(l) > 128 || !labelRE.MatchString(l) || sensitiveLabel(l) || seen[l] {
				return errors.New("invalid or sensitive allowed label")
			}
			seen[l] = true
		}
		switch q.Semantics {
		case "", "value", "gauge", "counter", "rate", "sum", "average", "minimum", "maximum", "quantile", "p95", "p99", "count":
		default:
			return errors.New("unsupported query semantics")
		}
		rendered, err := materializeQuery(q)
		if err != nil {
			return err
		}
		if err := validateExpression(rendered.Expression); err != nil {
			return err
		}
		if q.FreshnessExpression != "" {
			if err := validateExpression(rendered.FreshnessExpression); err != nil {
				return err
			}
			if err := validateFreshnessTemporalSettings(rendered.Expression, rendered.FreshnessExpression); err != nil {
				return err
			}
		}
		if q.TerminalExpression != "" {
			if err := validateTerminalExpression(rendered.TerminalExpression); err != nil {
				return err
			}
		}
	}
	return nil
}

type JobConfig struct {
	Config    Config    `json:"config"`
	Selection Selection `json:"selection"`
}

// validateJSONSize bounds the encoded signed-job input, including escaping and CA data.
func validateJSONSize(value any) error {
	b, err := json.Marshal(value)
	if err != nil || len(b) > MaxJobConfigBytes {
		return errors.New("Prometheus configuration exceeds the 64 KiB encoded job limit")
	}
	return nil
}

func (j JobConfig) Validate() error {
	if err := validateJSONSize(j); err != nil {
		return err
	}
	if err := j.Config.Validate(); err != nil {
		return err
	}
	if err := j.Selection.Validate(); err != nil {
		return err
	}
	cfg := j.Config.Defaults()
	for _, query := range j.Selection.Queries {
		if query.FreshnessExpression != "" && (cfg.MaxRequestsPerPoll < 2 || time.Duration(cfg.MaxPollSeconds)*time.Second <= time.Minute/time.Duration(cfg.RequestsPerMinute)) {
			return errors.New("freshness companions require an atomic two-request budget and a poll deadline longer than one quota interval")
		}
	}
	return validateProfileSelection(cfg, j.Selection)
}

type Capabilities struct {
	Metrics        bool              `json:"metrics"`
	Discovery      bool              `json:"discovery"`
	Targets        bool              `json:"targets"`
	Alerts         bool              `json:"alerts"`
	Exemplars      bool              `json:"exemplars"`
	Version        string            `json:"version,omitempty"`
	BackendProfile string            `json:"backend_profile"`
	Status         map[string]string `json:"status,omitempty"`
}
type Result struct {
	Points       []analysis.MetricPoint              `json:"points"`
	Evidence     []monitorprovider.Diagnostic        `json:"evidence"`
	Coverage     map[string]monitorprovider.Coverage `json:"coverage"`
	Progress     monitorprovider.QueryProgress       `json:"progress"`
	Topology     []monitorprovider.Entity            `json:"topology,omitempty"`
	Capabilities Capabilities                        `json:"capabilities"`
}

// These keys are generated by ingestion and sampled diagnostic coverage. Saved
// queries must not overwrite them when coverage maps are merged durably.
func reservedQueryID(id string) bool {
	switch id {
	case "query", "targets", "alerts", "targets_polling", "alerts_polling", "targets_last_success", "alerts_last_success", "retention":
		return true
	default:
		return false
	}
}
