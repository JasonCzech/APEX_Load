// Package appdynamics implements bounded, read-only AppDynamics Controller and
// Events Service collection. Credentials never enter public configuration.
package appdynamics

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

const AuthOAuth = "oauth"
const AuthBasic = "basic"

type Config struct {
	Endpoint                   string   `json:"endpoint"`
	ContextPath                string   `json:"context_path,omitempty"`
	AccountName                string   `json:"account_name"`
	AuthType                   string   `json:"auth_type"`
	EventsEndpoint             string   `json:"events_endpoint,omitempty"`
	EventsAccountName          string   `json:"events_account_name,omitempty"`
	ApprovedCIDRs              []string `json:"approved_cidrs,omitempty"`
	CAPEM                      string   `json:"ca_pem,omitempty"`
	TimeoutSeconds             int      `json:"timeout_seconds,omitempty"`
	RequestsPerMinute          int      `json:"requests_per_minute,omitempty"`
	AnalyticsRequestsPerMinute int      `json:"analytics_requests_per_minute,omitempty"`
	MaxResponseBytes           int      `json:"max_response_bytes,omitempty"`
	MaxSeries                  int      `json:"max_series,omitempty"`
	MaxPoints                  int      `json:"max_points,omitempty"`
	MaxRequestsPerPoll         int      `json:"max_requests_per_poll,omitempty"`
	MaxPollSeconds             int      `json:"max_poll_seconds,omitempty"`
}

type Credentials struct {
	ClientID     string `json:"-"`
	ClientSecret string `json:"-"`
	Username     string `json:"-"`
	Password     string `json:"-"`
	EventsAPIKey string `json:"-"`
}

func (c Config) Defaults() Config {
	if c.ContextPath == "" {
		c.ContextPath = "/controller"
	}
	if c.AuthType == "" {
		c.AuthType = AuthOAuth
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 10
	}
	if c.RequestsPerMinute == 0 {
		c.RequestsPerMinute = 60
	}
	if c.AnalyticsRequestsPerMinute == 0 {
		c.AnalyticsRequestsPerMinute = 30
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

func validateOrigin(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return errors.New("endpoint must be an HTTPS origin without credentials, path, query or fragment")
	}
	return nil
}

func validateHeaderOrigin(value string) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return errors.New("run-header destination must be an HTTP or HTTPS origin without credentials, path, query or fragment")
	}
	return nil
}

// Validate checks only configuration; credential presence and upstream access
// are checked independently by NewClient and Test.
func (c Config) Validate() error {
	c = c.Defaults()
	if err := validateOrigin(c.Endpoint); err != nil {
		return err
	}
	if c.EventsEndpoint != "" {
		if err := validateOrigin(c.EventsEndpoint); err != nil {
			return fmt.Errorf("events %w", err)
		}
	}
	if c.AuthType != AuthOAuth && c.AuthType != AuthBasic {
		return errors.New("unsupported AppDynamics authentication type")
	}
	if c.AccountName == "" || len(c.AccountName) > 256 || strings.ContainsAny(c.AccountName, "\r\n") {
		return errors.New("account name is required and bounded")
	}
	if !regexp.MustCompile(`^(/[A-Za-z0-9_-]+)+$`).MatchString(c.ContextPath) || len(c.ContextPath) > 256 {
		return errors.New("invalid Controller context path")
	}
	if len(c.EventsAccountName) > 256 || strings.ContainsAny(c.EventsAccountName, "\r\n") || len(c.CAPEM) > 65536 || len(c.ApprovedCIDRs) > 32 {
		return errors.New("configuration exceeds bounds")
	}
	for _, cidr := range c.ApprovedCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return errors.New("invalid approved CIDR")
		}
	}
	for _, b := range []struct{ v, lo, hi int }{{c.TimeoutSeconds, 1, 30}, {c.RequestsPerMinute, 1, 60}, {c.AnalyticsRequestsPerMinute, 1, 30}, {c.MaxResponseBytes, 1024, 4 << 20}, {c.MaxSeries, 1, 200}, {c.MaxPoints, 1, 10000}, {c.MaxRequestsPerPoll, 1, 60}, {c.MaxPollSeconds, 1, 30}} {
		if b.v < b.lo || b.v > b.hi {
			return errors.New("collection limit outside supported bounds")
		}
	}
	return nil
}

type MetricSelection struct {
	ApplicationID string            `json:"application_id"`
	Path          string            `json:"path"`
	Name          string            `json:"name"`
	Unit          string            `json:"unit,omitempty"`
	Statistic     string            `json:"statistic,omitempty"`
	EntityIDs     map[string]string `json:"entity_ids,omitempty"`
}
type AnalyticsQuery struct {
	Name               string `json:"name"`
	Query              string `json:"query"`
	ApplicationID      string `json:"application_id"`
	Kind               string `json:"kind,omitempty"`
	Unit               string `json:"unit,omitempty"`
	Statistic          string `json:"statistic,omitempty"`
	BucketSeconds      int    `json:"bucket_seconds,omitempty"`
	RunIDField         string `json:"run_id_field,omitempty"`
	ApplicationIDField string `json:"application_id_field,omitempty"`
	ApplicationName    string `json:"application_name,omitempty"`
}
type Selection struct {
	ProjectID       string              `json:"project_id"`
	QueryOffset     int                 `json:"query_offset,omitempty"`
	Applications    []string            `json:"applications"`
	Metrics         []MetricSelection   `json:"metrics,omitempty"`
	ScopeIDs        map[string][]string `json:"scope_ids,omitempty"`
	Events          bool                `json:"events,omitempty"`
	Violations      bool                `json:"violations,omitempty"`
	Snapshots       bool                `json:"snapshots,omitempty"`
	Analytics       []AnalyticsQuery    `json:"analytics,omitempty"`
	SettleSeconds   int                 `json:"settle_seconds,omitempty"`
	RunHeader       bool                `json:"run_header,omitempty"`
	HeaderOrigins   []string            `json:"header_origins,omitempty"`
	CollectorHeader string              `json:"collector_header,omitempty"`
	CollectorValue  string              `json:"collector_value,omitempty"`
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

var numericID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var requestGUID = regexp.MustCompile(`^(?:[a-fA-F0-9]{32}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)
var metricName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

func (s Selection) Validate() error {
	if s.QueryOffset < 0 || s.QueryOffset > 121 {
		return errors.New("invalid query continuation offset")
	}
	if s.ProjectID == "" || len(s.ProjectID) > 256 || len(s.Applications) == 0 || len(s.Applications) > 32 || len(s.Metrics) > 20 || len(s.Analytics) > 5 {
		return errors.New("selection requires a project and 1-32 applications, at most 20 metric queries and 5 Analytics queries")
	}
	apps := map[string]bool{}
	for _, id := range s.Applications {
		if !numericID.MatchString(id) || apps[id] {
			return errors.New("application IDs must be unique positive numeric IDs")
		}
		apps[id] = true
	}
	names := map[string]bool{}
	for _, m := range s.Metrics {
		if !apps[m.ApplicationID] || m.Path == "" || len(m.Path) > 4096 || strings.ContainsAny(m.Path, "\r\n") || !metricName.MatchString(m.Name) || len(m.Unit) > 64 || len(m.EntityIDs) > 8 {
			return errors.New("invalid metric selection")
		}
		key := m.ApplicationID + ":" + m.Name
		if names[key] {
			return errors.New("metric and Analytics names must be unique within an application")
		}
		names[key] = true
		switch m.Statistic {
		case "", "value", "average", "sum", "current", "p95", "p99":
		default:
			return errors.New("invalid metric statistic")
		}
		for k, v := range m.EntityIDs {
			if !validEntityKind(k) || !numericID.MatchString(v) || k == "application" && v != m.ApplicationID {
				return errors.New("invalid metric entity IDs")
			}
		}
	}
	if len(s.ScopeIDs) > 5 {
		return errors.New("invalid entity scope")
	}
	for k, ids := range s.ScopeIDs {
		if !validEntityKind(k) || k == "application" || len(ids) > 200 {
			return errors.New("invalid entity scope")
		}
		for _, id := range ids {
			if !numericID.MatchString(id) {
				return errors.New("entity scopes require numeric IDs")
			}
		}
	}
	bytes := 0
	for _, q := range s.Analytics {
		bytes += len(q.Query)
		if !metricName.MatchString(q.Name) || !validAnalyticsQuery(q.Query) || !apps[q.ApplicationID] || len(q.Unit) > 64 || strings.ContainsAny(q.Unit, "\r\n\x00") || q.BucketSeconds < 0 || q.BucketSeconds > 86400 || q.RunIDField != "" && !validAnalyticsField(q.RunIDField) {
			return errors.New("Analytics queries require selected application scope and bounded explicit SELECT fields")
		}
		if (q.ApplicationIDField == "") == (q.ApplicationName == "") || q.ApplicationIDField != "" && !validAnalyticsField(q.ApplicationIDField) || len(q.ApplicationName) > 256 || strings.ContainsAny(q.ApplicationName, "\r\n\x00") {
			return errors.New("Analytics requires an application ID field or a verified Controller application name")
		}
		key := q.ApplicationID + ":" + q.Name
		if names[key] {
			return errors.New("metric and Analytics names must be unique within an application")
		}
		names[key] = true
		switch q.Kind {
		case "", "log", "transaction", "metric":
		default:
			return errors.New("invalid Analytics result kind")
		}
		switch q.Statistic {
		case "", "value", "sum", "average":
		default:
			return errors.New("invalid Analytics metric statistic")
		}
	}
	if bytes > 16<<10 || s.SettleSeconds < 0 || s.SettleSeconds > 3600 {
		return errors.New("selection exceeds bounds")
	}
	if len(s.CollectorHeader) > 128 || len(s.CollectorValue) > 256 || strings.ContainsAny(s.CollectorHeader+s.CollectorValue, "\r\n") {
		return errors.New("invalid collector correlation filter")
	}
	if (s.CollectorHeader == "") != (s.CollectorValue == "") {
		return errors.New("collector header and value must be specified together")
	}
	if len(s.HeaderOrigins) > 32 {
		return errors.New("too many header origins")
	}
	for _, origin := range s.HeaderOrigins {
		if err := validateHeaderOrigin(origin); err != nil {
			return err
		}
	}
	if s.RunHeader && len(s.HeaderOrigins) == 0 {
		return errors.New("run header requires approved origins")
	}
	return nil
}
func validEntityKind(k string) bool {
	switch k {
	case "application", "tier", "node", "business_transaction", "backend", "machine":
		return true
	}
	return false
}
func validAnalyticsQuery(q string) bool {
	q = strings.TrimSpace(q)
	if len(q) <= 7 || len(q) > 16<<10 || !strings.HasPrefix(strings.ToUpper(q), "SELECT ") || strings.ContainsAny(q, ";\x00") || strings.Contains(q, "--") || strings.Contains(q, "/*") || strings.Contains(q, "*/") {
		return false
	}
	// Reject wildcard columns while preserving COUNT(*) and multiplication in
	// explicit expressions. The projector still allowlists returned fields.
	projection := q[7:]
	if at := strings.Index(strings.ToUpper(projection), " FROM "); at >= 0 {
		projection = projection[:at]
	}
	return !regexp.MustCompile(`(?i)(^|,)\s*(?:(?:DISTINCT|ALL)\s+)?(?:[A-Za-z_][A-Za-z0-9_]*\.)?\*(?:\s|,|$)`).MatchString(projection)
}

// Aliases preserve the AppDynamics public and JSON contracts while allowing
// other providers to use the same durable evidence pipeline.
type Entity = monitorprovider.Entity
type Diagnostic = monitorprovider.Diagnostic
type Coverage = monitorprovider.Coverage
type Capabilities struct {
	MetricAPI  string            `json:"metric_api,omitempty"`
	Metrics    bool              `json:"metrics"`
	Events     bool              `json:"events"`
	Violations bool              `json:"violations"`
	Snapshots  bool              `json:"snapshots"`
	Analytics  bool              `json:"analytics"`
	Status     map[string]string `json:"status,omitempty"`
}
type Result struct {
	Points       []analysis.MetricPoint `json:"points"`
	Evidence     []Diagnostic           `json:"evidence"`
	Coverage     map[string]Coverage    `json:"coverage"`
	Capabilities Capabilities           `json:"capabilities"`
	Progress     QueryProgress          `json:"progress"`
	Topology     []Entity               `json:"topology,omitempty"`
}

type QueryProgress = monitorprovider.QueryProgress
