package storage

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
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
	ID                      string                               `json:"id"`
	ProjectID               string                               `json:"project_id"`
	Name                    string                               `json:"name"`
	Description             string                               `json:"description,omitempty"`
	Enabled                 bool                                 `json:"enabled"`
	EnvironmentURL          string                               `json:"environment_url,omitempty"`
	PlatformURL             string                               `json:"platform_url,omitempty"`
	MetricsEnabled          bool                                 `json:"metrics_enabled"`
	ProblemsEnabled         bool                                 `json:"problems_enabled"`
	LogsEnabled             bool                                 `json:"logs_enabled"`
	EnvironmentTokenSecret  string                               `json:"-"`
	PlatformAuthType        string                               `json:"platform_auth_type,omitempty"`
	PlatformTokenSecret     string                               `json:"-"`
	OAuthClientIDSecret     string                               `json:"-"`
	OAuthClientSecretSecret string                               `json:"-"`
	OAuthResource           string                               `json:"oauth_resource,omitempty"`
	OAuthScopes             []string                             `json:"oauth_scopes,omitempty"`
	MetricQueries           []DynatraceMetricQuery               `json:"metric_queries,omitempty"`
	ProblemSelector         string                               `json:"problem_selector,omitempty"`
	ProblemEntitySelector   string                               `json:"problem_entity_selector,omitempty"`
	LogBaseFilter           string                               `json:"log_base_filter,omitempty"`
	LogTestIDField          string                               `json:"log_test_id_field,omitempty"`
	LogTimestampField       string                               `json:"log_timestamp_field,omitempty"`
	LogStatusField          string                               `json:"log_status_field,omitempty"`
	LogMessageField         string                               `json:"log_message_field,omitempty"`
	LogTraceIDField         string                               `json:"log_trace_id_field,omitempty"`
	PollIntervalSeconds     int                                  `json:"poll_interval_seconds,omitempty"`
	LastCheckedAt           *time.Time                           `json:"last_checked_at,omitempty"`
	LastCheckStatus         string                               `json:"last_check_status,omitempty"`
	LastCheckMessage        string                               `json:"last_check_message,omitempty"`
	LastCapabilityHealth    map[string]DynatraceCapabilityHealth `json:"last_capability_health,omitempty"`
	CreatedAt               time.Time                            `json:"created_at"`
	UpdatedAt               time.Time                            `json:"updated_at"`
}

func (c *DynatraceConnection) ConfigurationStatus() string {
	if c == nil || !c.Enabled {
		return "disabled"
	}
	if !c.MetricsEnabled && !c.ProblemsEnabled && !c.LogsEnabled {
		return "unconfigured"
	}
	if (c.MetricsEnabled || c.ProblemsEnabled) && (c.EnvironmentURL == "" || c.EnvironmentTokenSecret == "") {
		return "unconfigured"
	}
	if c.MetricsEnabled && len(c.MetricQueries) == 0 {
		return "unconfigured"
	}
	if c.LogsEnabled {
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
	if c.ID == "" || c.ProjectID == "" || c.Name == "" {
		return errors.New("Dynatrace connection id, project id, and name are required")
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
	if len(c.MetricQueries) > 10 {
		return errors.New("Dynatrace connections support at most 10 metric queries")
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
	return nil
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
}
