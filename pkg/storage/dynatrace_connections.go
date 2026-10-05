package storage

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dynatrace"
)

const (
	DynatracePlatformAuthToken = "platform_token"
	DynatracePlatformAuthOAuth = "oauth_client"
)

var dynatraceMetricNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
var dynatraceFieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)
var dynatraceOAuthScopeAllowlist = map[string]struct{}{
	"storage:buckets:read": {},
	"storage:logs:read":    {},
	"storage:spans:read":   {}, "storage:metrics:read": {}, "storage:events:read": {}, "storage:entities:read": {}, "storage:smartscape:read": {},
}

type DynatraceMetricQuery struct {
	Name           string `json:"name"`
	MetricSelector string `json:"metric_selector"`
	EntitySelector string `json:"entity_selector,omitempty"`
	Aggregation    string `json:"aggregation"`
}

type DynatraceCapabilityHealth struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// DynatraceConnection is a project-scoped, read-only Dynatrace SaaS query profile.
// Secret fields are logical project-secret references and are never serialized.
type DynatraceConnection struct {
	Revision                  int64                                `json:"revision"`
	VerifiedRevision          int64                                `json:"verified_revision"`
	CollectionMode            string                               `json:"collection_mode"`
	CollectorAgentID          string                               `json:"collector_agent_id,omitempty"`
	Capabilities              dynatrace.Capabilities               `json:"capabilities"`
	EventsEnabled             bool                                 `json:"events_enabled"`
	SpansEnabled              bool                                 `json:"spans_enabled"`
	GrailMetricsEnabled       bool                                 `json:"grail_metrics_enabled"`
	GrailEventsEnabled        bool                                 `json:"grail_events_enabled"`
	TopologyMode              string                               `json:"topology_mode,omitempty"`
	RequestsPerMinute         int                                  `json:"requests_per_minute,omitempty"`
	PlatformRequestsPerMinute int                                  `json:"platform_requests_per_minute,omitempty"`
	MaxScanBytes              int64                                `json:"max_scan_bytes,omitempty"`
	MaxRunScanBytes           int64                                `json:"max_run_scan_bytes,omitempty"`
	DrainReserveBytes         int64                                `json:"drain_reserve_bytes,omitempty"`
	ID                        string                               `json:"id"`
	ProjectID                 string                               `json:"project_id"`
	Name                      string                               `json:"name"`
	Description               string                               `json:"description,omitempty"`
	Enabled                   bool                                 `json:"enabled"`
	EnvironmentURL            string                               `json:"environment_url,omitempty"`
	PlatformURL               string                               `json:"platform_url,omitempty"`
	MetricsEnabled            bool                                 `json:"metrics_enabled"`
	ProblemsEnabled           bool                                 `json:"problems_enabled"`
	LogsEnabled               bool                                 `json:"logs_enabled"`
	EnvironmentTokenSecret    string                               `json:"-"`
	PlatformAuthType          string                               `json:"platform_auth_type,omitempty"`
	PlatformTokenSecret       string                               `json:"-"`
	OAuthClientIDSecret       string                               `json:"-"`
	OAuthClientSecretSecret   string                               `json:"-"`
	OAuthResource             string                               `json:"oauth_resource,omitempty"`
	OAuthScopes               []string                             `json:"oauth_scopes,omitempty"`
	MetricQueries             []DynatraceMetricQuery               `json:"metric_queries,omitempty"`
	ProblemSelector           string                               `json:"problem_selector,omitempty"`
	ProblemEntitySelector     string                               `json:"problem_entity_selector,omitempty"`
	LogBaseFilter             string                               `json:"log_base_filter,omitempty"`
	LogTestIDField            string                               `json:"log_test_id_field,omitempty"`
	LogTimestampField         string                               `json:"log_timestamp_field,omitempty"`
	LogStatusField            string                               `json:"log_status_field,omitempty"`
	LogMessageField           string                               `json:"log_message_field,omitempty"`
	LogTraceIDField           string                               `json:"log_trace_id_field,omitempty"`
	PollIntervalSeconds       int                                  `json:"poll_interval_seconds,omitempty"`
	LastCheckedAt             *time.Time                           `json:"last_checked_at,omitempty"`
	LastCheckStatus           string                               `json:"last_check_status,omitempty"`
	LastCheckMessage          string                               `json:"last_check_message,omitempty"`
	LastCapabilityHealth      map[string]DynatraceCapabilityHealth `json:"last_capability_health,omitempty"`
	CreatedAt                 time.Time                            `json:"created_at"`
	UpdatedAt                 time.Time                            `json:"updated_at"`
}

func (c *DynatraceConnection) ConfigurationStatus() string {
	if c == nil || !c.Enabled {
		return "disabled"
	}
	if !c.MetricsEnabled && !c.ProblemsEnabled && !c.LogsEnabled && !c.EventsEnabled && !c.SpansEnabled && !c.GrailMetricsEnabled && !c.GrailEventsEnabled && c.TopologyMode == "" {
		return "unconfigured"
	}
	if (c.MetricsEnabled || c.ProblemsEnabled || c.EventsEnabled || c.TopologyMode == "classic") && (c.EnvironmentURL == "" || c.EnvironmentTokenSecret == "") {
		return "unconfigured"
	}
	if c.LogsEnabled || c.SpansEnabled || c.GrailMetricsEnabled || c.GrailEventsEnabled || c.TopologyMode == "smartscape" {
		if c.PlatformURL == "" {
			return "unconfigured"
		}
		switch c.PlatformAuthType {
		case DynatracePlatformAuthToken:
			if c.PlatformTokenSecret == "" {
				return "unconfigured"
			}
		case DynatracePlatformAuthOAuth:
			if c.OAuthClientIDSecret == "" || c.OAuthClientSecretSecret == "" || c.OAuthResource == "" || len(c.OAuthScopes) == 0 {
				return "unconfigured"
			}
		default:
			return "unconfigured"
		}
	}
	return "ready"
}

func validateDynatraceURL(label, raw, requiredSuffix string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return "", fmt.Errorf("%s must be an https URL without credentials, query, or fragment", label)
	}
	if requiredSuffix != "" && !strings.HasSuffix(strings.ToLower(parsed.Hostname()), requiredSuffix) {
		return "", fmt.Errorf("%s must use a Dynatrace SaaS %s host", label, requiredSuffix)
	}
	return strings.TrimRight(raw, "/"), nil
}

func (c *DynatraceConnection) Validate() error {
	if c == nil {
		return errors.New("Dynatrace connection is required")
	}
	c.ID, c.ProjectID, c.Name = strings.TrimSpace(c.ID), strings.TrimSpace(c.ProjectID), strings.TrimSpace(c.Name)
	if c.ID == "" || c.ProjectID == "" || c.Name == "" || len(c.ID) > 256 || len(c.ProjectID) > 256 || len(c.Name) > 256 || len(c.Description) > 4096 {
		return errors.New("Dynatrace connection id, project id, and name are required")
	}
	if c.CollectionMode == "" {
		c.CollectionMode = "direct"
	}
	if c.CollectionMode != "direct" && c.CollectionMode != "collector" || c.CollectionMode == "collector" && c.CollectorAgentID == "" || c.CollectionMode == "direct" && c.CollectorAgentID != "" || len(c.CollectorAgentID) > 256 {
		return errors.New("invalid Dynatrace execution location")
	}
	var err error
	if c.EnvironmentURL, err = validateDynatraceURL("Dynatrace environment URL", c.EnvironmentURL, ".live.dynatrace.com"); err != nil {
		return err
	}
	if c.PlatformURL, err = validateDynatraceURL("Dynatrace platform URL", c.PlatformURL, ".apps.dynatrace.com"); err != nil {
		return err
	}
	if c.PlatformAuthType != "" && c.PlatformAuthType != DynatracePlatformAuthToken && c.PlatformAuthType != DynatracePlatformAuthOAuth {
		return fmt.Errorf("unsupported Dynatrace platform authentication type %q", c.PlatformAuthType)
	}
	if len(c.MetricQueries) > 20 {
		return errors.New("Dynatrace connections support at most 20 metric queries")
	}
	seen := make(map[string]struct{}, len(c.MetricQueries))
	for index := range c.MetricQueries {
		query := &c.MetricQueries[index]
		query.Name = strings.TrimSpace(query.Name)
		query.MetricSelector = strings.TrimSpace(query.MetricSelector)
		query.EntitySelector = strings.TrimSpace(query.EntitySelector)
		query.Aggregation = strings.ToLower(strings.TrimSpace(query.Aggregation))
		if !dynatraceMetricNamePattern.MatchString(query.Name) || query.MetricSelector == "" {
			return fmt.Errorf("Dynatrace metric query %d requires a safe name and selector", index)
		}
		switch query.Aggregation {
		case "avg", "min", "max", "sum":
		default:
			return fmt.Errorf("Dynatrace metric query %q has unsupported aggregation %q", query.Name, query.Aggregation)
		}
		key := strings.ToLower(query.Name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate Dynatrace metric query name %q", query.Name)
		}
		seen[key] = struct{}{}
	}
	if c.PollIntervalSeconds == 0 {
		c.PollIntervalSeconds = 15
	}
	if c.PollIntervalSeconds < 10 || c.PollIntervalSeconds > 300 {
		return errors.New("Dynatrace polling interval must be between 10 and 300 seconds")
	}
	c.LogTimestampField = firstNonEmptyDynatrace(strings.TrimSpace(c.LogTimestampField), "timestamp")
	c.LogStatusField = firstNonEmptyDynatrace(strings.TrimSpace(c.LogStatusField), "status")
	c.LogMessageField = firstNonEmptyDynatrace(strings.TrimSpace(c.LogMessageField), "content")
	c.LogTraceIDField = firstNonEmptyDynatrace(strings.TrimSpace(c.LogTraceIDField), "trace_id")
	for label, field := range map[string]string{"timestamp": c.LogTimestampField, "status": c.LogStatusField, "message": c.LogMessageField, "test ID": c.LogTestIDField, "trace ID": c.LogTraceIDField} {
		if field != "" && !dynatraceFieldNamePattern.MatchString(field) {
			return fmt.Errorf("Dynatrace log %s field %q is invalid", label, field)
		}
	}
	c.OAuthScopes = compactSortedStorageStrings(c.OAuthScopes)
	for _, scope := range c.OAuthScopes {
		if _, allowed := dynatraceOAuthScopeAllowlist[scope]; !allowed {
			return fmt.Errorf("unsupported Dynatrace OAuth scope %q", scope)
		}
	}
	for _, ref := range []string{c.EnvironmentTokenSecret, c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret} {
		if len(ref) > 256 || strings.ContainsAny(ref, "\r\n\x00") {
			return errors.New("invalid Dynatrace secret reference")
		}
	}
	if c.RuntimeConfig().EnvironmentURL != "" || c.RuntimeConfig().PlatformURL != "" {
		if err := c.RuntimeConfig().Validate(); err != nil {
			return err
		}
	}
	if c.TopologyMode != "" && c.TopologyMode != "classic" && c.TopologyMode != "smartscape" {
		return errors.New("invalid Dynatrace topology mode")
	}
	return nil
}

func (c *DynatraceConnection) RuntimeConfig() dynatrace.Config {
	if c == nil {
		return dynatrace.Config{}
	}
	cfg := dynatrace.Config{EnvironmentURL: c.EnvironmentURL, PlatformURL: c.PlatformURL, PlatformAuthType: c.PlatformAuthType, OAuthResource: c.OAuthResource, OAuthScopes: append([]string(nil), c.OAuthScopes...), RequestsPerMinute: c.RequestsPerMinute, PlatformRequestsPerMinute: c.PlatformRequestsPerMinute, MaxScanBytes: c.MaxScanBytes, MaxRunScanBytes: c.MaxRunScanBytes, DrainReserveBytes: c.DrainReserveBytes}
	return cfg.Defaults()
}
func (c *DynatraceConnection) Readiness() string {
	if c == nil || !c.Enabled {
		return "disabled"
	}
	if c.RuntimeConfig().Validate() != nil {
		return "unconfigured"
	}
	if c.ConfigurationStatus() != "ready" {
		return c.ConfigurationStatus()
	}
	if c.Revision < 1 || c.VerifiedRevision != c.Revision || c.LastCheckStatus != "connected" {
		return "unverified"
	}
	return "ready"
}

type DynatraceConnectionSnapshot struct {
	ConnectionID            string                 `json:"connection_id"`
	ProjectID               string                 `json:"project_id"`
	Revision                int64                  `json:"revision"`
	VerifiedRevision        int64                  `json:"verified_revision"`
	Config                  dynatrace.Config       `json:"config"`
	AllowedCapabilities     dynatrace.Capabilities `json:"allowed_capabilities"`
	Capabilities            dynatrace.Capabilities `json:"capabilities"`
	CollectionMode          string                 `json:"collection_mode"`
	CollectorAgentID        string                 `json:"collector_agent_id,omitempty"`
	EnvironmentTokenSecret  string                 `json:"environment_token_secret,omitempty"`
	PlatformTokenSecret     string                 `json:"platform_token_secret,omitempty"`
	OAuthClientIDSecret     string                 `json:"oauth_client_id_secret,omitempty"`
	OAuthClientSecretSecret string                 `json:"oauth_client_secret_secret,omitempty"`
}

func (c *DynatraceConnection) Snapshot() *DynatraceConnectionSnapshot {
	if c == nil {
		return nil
	}
	cfg := c.RuntimeConfig()
	cfg.OAuthScopes = append([]string(nil), cfg.OAuthScopes...)
	capabilities := c.Capabilities
	capabilities.Status = maps.Clone(capabilities.Status)
	capabilities.VerifiedOrigins = slices.Clone(capabilities.VerifiedOrigins)
	allowed := dynatrace.Capabilities{Metrics: c.MetricsEnabled, Problems: c.ProblemsEnabled, Events: c.EventsEnabled, Logs: c.LogsEnabled, Spans: c.SpansEnabled, GrailMetrics: c.GrailMetricsEnabled, GrailEvents: c.GrailEventsEnabled, ClassicTopology: c.TopologyMode == "classic", Smartscape: c.TopologyMode == "smartscape"}
	return &DynatraceConnectionSnapshot{ConnectionID: c.ID, ProjectID: c.ProjectID, Revision: c.Revision, VerifiedRevision: c.VerifiedRevision, Config: cfg, AllowedCapabilities: allowed, Capabilities: capabilities, CollectionMode: c.CollectionMode, CollectorAgentID: c.CollectorAgentID, EnvironmentTokenSecret: c.EnvironmentTokenSecret, PlatformTokenSecret: c.PlatformTokenSecret, OAuthClientIDSecret: c.OAuthClientIDSecret, OAuthClientSecretSecret: c.OAuthClientSecretSecret}
}
func SameDynatraceIdentity(a, b *DynatraceConnection) bool {
	if a == nil || b == nil {
		return false
	}
	x, y := a.Snapshot(), b.Snapshot()
	x.Revision, y.Revision = 0, 0
	x.VerifiedRevision, y.VerifiedRevision = 0, 0
	x.Capabilities, y.Capabilities = dynatrace.Capabilities{}, dynatrace.Capabilities{}
	return reflect.DeepEqual(x, y) && reflect.DeepEqual(a.DefaultSelection(), b.DefaultSelection()) && a.MetricsEnabled == b.MetricsEnabled && a.ProblemsEnabled == b.ProblemsEnabled && a.LogsEnabled == b.LogsEnabled && a.EventsEnabled == b.EventsEnabled && a.SpansEnabled == b.SpansEnabled && a.GrailMetricsEnabled == b.GrailMetricsEnabled && a.ProblemSelector == b.ProblemSelector && a.ProblemEntitySelector == b.ProblemEntitySelector && a.LogBaseFilter == b.LogBaseFilter && a.LogTestIDField == b.LogTestIDField && a.PollIntervalSeconds == b.PollIntervalSeconds
}

// DefaultSelection translates approved legacy query profiles for future runs;
// existing scalar archives remain immutable and cannot be reconstructed.
func (c *DynatraceConnection) DefaultSelection() dynatrace.Selection {
	s := dynatrace.Selection{ProjectID: c.ProjectID, Problems: c.ProblemsEnabled, ProblemSelector: c.ProblemSelector, Events: c.EventsEnabled, TopologyMode: c.TopologyMode, EntitySelector: c.ProblemEntitySelector}
	if c.MetricsEnabled {
		for _, q := range c.MetricQueries {
			s.Metrics = append(s.Metrics, dynatrace.MetricSelection{Name: q.Name, MetricSelector: q.MetricSelector, EntitySelector: q.EntitySelector, Aggregation: q.Aggregation})
		}
	}
	if c.LogsEnabled {
		fields := []string{c.LogTimestampField, c.LogStatusField, c.LogMessageField, c.LogTraceIDField}
		if c.LogTestIDField != "" {
			fields = append(fields, c.LogTestIDField)
		}
		fields = compactSortedStorageStrings(fields)
		query := "fetch logs"
		if c.LogBaseFilter != "" {
			query += " | filter (" + c.LogBaseFilter + ")"
		}
		query += " | fields " + strings.Join(fields, ", ")
		s.Grail = append(s.Grail, dynatrace.GrailQuery{Name: "logs", Kind: "log", Query: query, Fields: fields, RunIDField: c.LogTestIDField})
	}
	return s
}

func firstNonEmptyDynatrace(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func compactSortedStorageStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

type DynatraceConnectionStore interface {
	CreateDynatraceConnection(*DynatraceConnection) error
	UpdateDynatraceConnectionIfUnchanged(*DynatraceConnection, time.Time) (bool, error)
	UpdateDynatraceConnectionHealth(id, projectID, status, message string, capabilities map[string]DynatraceCapabilityHealth, checkedAt time.Time) error
	GetDynatraceConnection(id string) (*DynatraceConnection, error)
	ListDynatraceConnections(projectID string) ([]*DynatraceConnection, error)
	DeleteDynatraceConnection(id, projectID string) (bool, error)
	UpdateDynatraceConnectionIfRevision(*DynatraceConnection, int64) (bool, error)
	UpdateDynatraceConnectionHealthIfRevision(id, projectID string, expected int64, status, message string, capabilities dynatrace.Capabilities, checkedAt time.Time) (bool, error)
	DeleteDynatraceConnectionIfRevision(id, projectID string, expected int64) (bool, error)
}
