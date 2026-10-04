// Package dynatrace implements bounded, read-only Dynatrace SaaS collection.
package dynatrace

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const AuthToken = "platform_token"
const AuthOAuth = "oauth_client"

type Config struct {
	EnvironmentURL            string   `json:"environment_url,omitempty"`
	PlatformURL               string   `json:"platform_url,omitempty"`
	PlatformAuthType          string   `json:"platform_auth_type,omitempty"`
	OAuthResource             string   `json:"oauth_resource,omitempty"`
	OAuthScopes               []string `json:"oauth_scopes,omitempty"`
	TimeoutSeconds            int      `json:"timeout_seconds,omitempty"`
	RequestsPerMinute         int      `json:"requests_per_minute,omitempty"`
	PlatformRequestsPerMinute int      `json:"platform_requests_per_minute,omitempty"`
	MaxResponseBytes          int      `json:"max_response_bytes,omitempty"`
	MaxSeries                 int      `json:"max_series,omitempty"`
	MaxPoints                 int      `json:"max_points,omitempty"`
	MaxRequestsPerPoll        int      `json:"max_requests_per_poll,omitempty"`
	MaxPollSeconds            int      `json:"max_poll_seconds,omitempty"`
	MaxScanBytes              int64    `json:"max_scan_bytes,omitempty"`
	MaxRunScanBytes           int64    `json:"max_run_scan_bytes,omitempty"`
	DrainReserveBytes         int64    `json:"drain_reserve_bytes,omitempty"`
}
type Credentials struct {
	EnvironmentToken string `json:"-"`
	PlatformToken    string `json:"-"`
	ClientID         string `json:"-"`
	ClientSecret     string `json:"-"`
	VersionIdentity  string `json:"-"`
}

func (c Config) Defaults() Config {
	if c.PlatformAuthType == "" {
		c.PlatformAuthType = AuthOAuth
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 10
	}
	if c.RequestsPerMinute == 0 {
		c.RequestsPerMinute = 60
	}
	if c.PlatformRequestsPerMinute == 0 {
		c.PlatformRequestsPerMinute = 60
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
	if c.MaxScanBytes == 0 {
		c.MaxScanBytes = 1_000_000_000
	}
	if c.MaxRunScanBytes == 0 {
		c.MaxRunScanBytes = 10_000_000_000
	}
	if c.DrainReserveBytes == 0 {
		c.DrainReserveBytes = 2_000_000_000
	}
	return c
}
func ValidateOrigin(raw, suffix string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Path != "" && u.Path != "/" || u.Port() != "" && u.Port() != "443" || !strings.HasSuffix(strings.ToLower(u.Hostname()), suffix) || len(u.Hostname()) <= len(suffix) {
		return errors.New("endpoint must be a Dynatrace SaaS HTTPS origin")
	}
	if id := strings.TrimSuffix(strings.ToLower(u.Hostname()), suffix); strings.Contains(id, ".") {
		return errors.New("endpoint must identify one standard SaaS environment")
	}
	return nil
}

// EnvironmentIdentity reads the documented standard SaaS environment identity
// from its origin; callers must first validate the corresponding origin.
func EnvironmentIdentity(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range []string{".live.dynatrace.com", ".apps.dynatrace.com"} {
		if strings.HasSuffix(host, suffix) {
			return strings.TrimSuffix(host, suffix)
		}
	}
	return ""
}
func (c Config) Validate() error {
	c = c.Defaults()
	if c.EnvironmentURL == "" && c.PlatformURL == "" {
		return errors.New("at least one Dynatrace SaaS endpoint is required")
	}
	if c.EnvironmentURL != "" {
		if err := ValidateOrigin(c.EnvironmentURL, ".live.dynatrace.com"); err != nil {
			return err
		}
	}
	if c.PlatformURL != "" {
		if err := ValidateOrigin(c.PlatformURL, ".apps.dynatrace.com"); err != nil {
			return err
		}
	}
	if c.EnvironmentURL != "" && c.PlatformURL != "" && EnvironmentIdentity(c.EnvironmentURL) != EnvironmentIdentity(c.PlatformURL) {
		return errors.New("Classic and Platform origins must identify the same SaaS environment")
	}
	if c.PlatformAuthType != AuthToken && c.PlatformAuthType != AuthOAuth {
		return errors.New("unsupported Platform authentication")
	}
	if len(c.OAuthResource) > 256 || strings.ContainsAny(c.OAuthResource, "\r\n\x00") || len(c.OAuthScopes) > 16 {
		return errors.New("invalid OAuth identity")
	}
	for _, scope := range c.OAuthScopes {
		if !allowedScope(scope) {
			return errors.New("unsupported read-only Dynatrace OAuth scope")
		}
	}
	for _, b := range []struct{ v, lo, hi int }{{c.TimeoutSeconds, 1, 10}, {c.RequestsPerMinute, 1, 60}, {c.PlatformRequestsPerMinute, 1, 60}, {c.MaxResponseBytes, 1024, 4 << 20}, {c.MaxSeries, 1, 200}, {c.MaxPoints, 1, 10000}, {c.MaxRequestsPerPoll, 1, 60}, {c.MaxPollSeconds, 1, 30}} {
		if b.v < b.lo || b.v > b.hi {
			return errors.New("collection limit outside bounds")
		}
	}
	if c.MaxScanBytes < 1 || c.MaxScanBytes > 100_000_000_000 || c.MaxRunScanBytes < c.MaxScanBytes || c.MaxRunScanBytes > 1_000_000_000_000 || c.DrainReserveBytes < 0 || c.DrainReserveBytes > c.MaxRunScanBytes {
		return errors.New("invalid Grail scan budget")
	}
	return nil
}
func allowedScope(s string) bool {
	switch s {
	case "storage:buckets:read", "storage:logs:read", "storage:spans:read", "storage:metrics:read", "storage:events:read", "storage:entities:read", "storage:smartscape:read":
		return true
	}
	return false
}

type MetricSelection struct {
	Name           string `json:"name"`
	MetricSelector string `json:"metric_selector"`
	EntitySelector string `json:"entity_selector,omitempty"`
	Aggregation    string `json:"aggregation"`
	Unit           string `json:"unit,omitempty"`
	Semantics      string `json:"semantics,omitempty"`
}
type GrailQuery struct {
	Name          string   `json:"name"`
	Query         string   `json:"query"`
	Kind          string   `json:"kind"`
	Unit          string   `json:"unit,omitempty"`
	Statistic     string   `json:"statistic,omitempty"`
	ValueField    string   `json:"value_field,omitempty"`
	Fields        []string `json:"fields,omitempty"`
	BucketSeconds int      `json:"bucket_seconds,omitempty"`
	RunIDField    string   `json:"run_id_field,omitempty"`
	EntityIDField string   `json:"entity_id_field,omitempty"`
	EntityIDs     []string `json:"entity_ids,omitempty"`
}
type Selection struct {
	ProjectID           string            `json:"project_id"`
	QueryOffset         int               `json:"query_offset,omitempty"`
	Cursor              string            `json:"cursor,omitempty"`
	Metrics             []MetricSelection `json:"metrics,omitempty"`
	EntitySelector      string            `json:"entity_selector,omitempty"`
	ProblemSelector     string            `json:"problem_selector,omitempty"`
	EventSelector       string            `json:"event_selector,omitempty"`
	Problems            bool              `json:"problems,omitempty"`
	Events              bool              `json:"events,omitempty"`
	TopologyMode        string            `json:"topology_mode,omitempty"`
	SmartscapeNodeType  string            `json:"smartscape_node_type,omitempty"`
	SmartscapeEntityIDs []string          `json:"smartscape_entity_ids,omitempty"`
	Grail               []GrailQuery      `json:"grail,omitempty"`
	SettleSeconds       int               `json:"settle_seconds,omitempty"`
	RunHeader           bool              `json:"run_header,omitempty"`
	HeaderOrigins       []string          `json:"header_origins,omitempty"`
}
type JobConfig struct {
	Config    Config    `json:"config"`
	Selection Selection `json:"selection"`
}

func (j JobConfig) Validate() error {
	if err := j.Config.Validate(); err != nil {
		return err
	}
	return j.Selection.Validate()
}

var safeName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
var safeField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)
var entityID = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}-[A-Fa-f0-9]{16}$`)

// Smartscape IDs are opaque identities and must never be coerced into classic
// entity IDs. This only bounds a provider identity, not its meaning.
var providerEntityID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func (s Selection) Validate() error {
	for _, selector := range []string{s.EntitySelector, s.ProblemSelector, s.EventSelector} {
		if len(selector) > 4096 || strings.ContainsAny(selector, "\r\n\x00") {
			return errors.New("invalid bounded diagnostic selector")
		}
	}
	if s.ProjectID == "" || len(s.ProjectID) > 256 || s.QueryOffset < 0 || s.QueryOffset > 64 || len(s.Cursor) > 16<<10 || len(s.Metrics) > 20 || len(s.Grail) > 5 || len(s.EntitySelector) > 4096 || s.SettleSeconds < 0 || s.SettleSeconds > 3600 {
		return errors.New("selection exceeds bounds")
	}
	if s.TopologyMode != "" && s.TopologyMode != "classic" && s.TopologyMode != "smartscape" {
		return errors.New("unsupported topology mode")
	}
	if s.TopologyMode == "smartscape" && s.EntitySelector != "" {
		return errors.New("Smartscape scope requires explicit Smartscape IDs or node type, not a classic entity selector")
	}
	if s.SmartscapeNodeType != "" && !safeName.MatchString(s.SmartscapeNodeType) || len(s.SmartscapeEntityIDs) > 200 {
		return errors.New("invalid Smartscape selection")
	}
	if (s.SmartscapeNodeType != "" || len(s.SmartscapeEntityIDs) > 0) && s.TopologyMode != "smartscape" {
		return errors.New("Smartscape scope requires Smartscape topology mode")
	}
	for _, id := range s.SmartscapeEntityIDs {
		if !providerEntityID.MatchString(id) {
			return errors.New("invalid Smartscape identity")
		}
	}
	if len(s.Metrics) == 0 && len(s.Grail) == 0 && !s.Problems && !s.Events && s.TopologyMode == "" {
		return errors.New("select a monitoring capability")
	}
	names := map[string]bool{}
	for _, m := range s.Metrics {
		if m.Semantics != "" && m.Semantics != "gauge" && m.Semantics != "request_average" {
			return errors.New("invalid metric semantics")
		}
		if _, err := baseMetricID(m.MetricSelector); err != nil {
			return err
		}
		if !safeName.MatchString(m.Name) || names[m.Name] || m.MetricSelector == "" || len(m.MetricSelector) > 4096 || strings.ContainsAny(m.MetricSelector+m.EntitySelector, "\r\n\x00") || len(m.EntitySelector) > 4096 || len(m.Unit) > 64 {
			return errors.New("invalid metric selection")
		}
		names[m.Name] = true
		switch m.Aggregation {
		case "avg", "min", "max", "sum", "count", "value", "p95", "p99":
		default:
			return errors.New("unsupported metric aggregation")
		}
		if err := validateSelectorTransforms(m.MetricSelector); err != nil {
			return err
		}
		if err := validateSelectorAggregation(m); err != nil {
			return err
		}
	}
	for _, q := range s.Grail {
		if !safeName.MatchString(q.Name) || names[q.Name] {
			return errors.New("Grail names must be unique")
		}
		names[q.Name] = true
		if err := q.Validate(); err != nil {
			return err
		}
	}
	if len(s.HeaderOrigins) > 32 || s.RunHeader && len(s.HeaderOrigins) == 0 {
		return errors.New("run header requires approved origins")
	}
	for _, raw := range s.HeaderOrigins {
		u, e := url.Parse(raw)
		if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("invalid correlation origin")
		}
	}
	return nil
}

type Entity = analysis.ProviderEntity
type Diagnostic = analysis.ProviderDiagnostic
type Coverage = analysis.ProviderCoverage
type QueryProgress = analysis.ProviderQueryProgress
type QueryScanSettlement = analysis.ProviderScanSettlement
type Capabilities struct {
	EnvironmentID         string            `json:"environment_id,omitempty"`
	PlatformEnvironmentID string            `json:"platform_environment_id,omitempty"`
	TenantID              string            `json:"tenant_id,omitempty"`
	EnvironmentOrigin     string            `json:"environment_origin,omitempty"`
	PlatformOrigin        string            `json:"platform_origin,omitempty"`
	VerifiedOrigins       []string          `json:"verified_origins,omitempty"`
	Metrics               bool              `json:"metrics"`
	Problems              bool              `json:"problems"`
	Events                bool              `json:"events"`
	Logs                  bool              `json:"logs"`
	Spans                 bool              `json:"spans"`
	GrailMetrics          bool              `json:"grail_metrics"`
	GrailEvents           bool              `json:"grail_events"`
	ClassicTopology       bool              `json:"classic_topology"`
	Smartscape            bool              `json:"smartscape"`
	Status                map[string]string `json:"status,omitempty"`
}
type Result struct {
	Points       []analysis.MetricPoint `json:"points"`
	Evidence     []Diagnostic           `json:"evidence"`
	Coverage     map[string]Coverage    `json:"coverage"`
	Topology     []Entity               `json:"topology,omitempty"`
	Progress     QueryProgress          `json:"progress"`
	Capabilities Capabilities           `json:"capabilities"`
}
type DiscoveryRequest struct {
	Kind         string `json:"kind"`
	Search       string `json:"search,omitempty"`
	Selector     string `json:"selector,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	TopologyMode string `json:"topology_mode,omitempty"`
}
type DiscoveryResult struct {
	Items      []Entity `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Truncated  bool     `json:"truncated"`
	Message    string   `json:"message,omitempty"`
}
type QuotaHook func(endpoint string, cost int) error
type RequestHook func(string) error
type ScanHook func(int64) error

var _ = time.Second
