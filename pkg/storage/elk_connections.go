package storage

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	ELKAuthAPIKey = "api_key"
	ELKAuthBasic  = "basic"
)

var elkMetricNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

type ELKMetricAggregation struct {
	Name      string `json:"name"`
	Field     string `json:"field"`
	Operation string `json:"operation"`
}

// ELKConnection is project-scoped Elasticsearch query configuration.
// Credential fields contain logical project-secret names, never secret values.
type ELKConnection struct {
	ID                 string                 `json:"id"`
	ProjectID          string                 `json:"project_id"`
	Name               string                 `json:"name"`
	Description        string                 `json:"description,omitempty"`
	Enabled            bool                   `json:"enabled"`
	Endpoint           string                 `json:"endpoint,omitempty"`
	IndexPattern       string                 `json:"index_pattern,omitempty"`
	TimestampField     string                 `json:"timestamp_field,omitempty"`
	LevelField         string                 `json:"level_field,omitempty"`
	MessageField       string                 `json:"message_field,omitempty"`
	TestIDField        string                 `json:"test_id_field,omitempty"`
	TraceIDField       string                 `json:"trace_id_field,omitempty"`
	BaseFilter         string                 `json:"base_filter,omitempty"`
	ErrorFilter        string                 `json:"error_filter,omitempty"`
	AuthType           string                 `json:"auth_type,omitempty"`
	APIKeySecretName   string                 `json:"-"`
	UsernameSecretName string                 `json:"-"`
	PasswordSecretName string                 `json:"-"`
	MetricAggregations []ELKMetricAggregation `json:"metric_aggregations,omitempty"`
	LastCheckedAt      *time.Time             `json:"last_checked_at,omitempty"`
	LastCheckStatus    string                 `json:"last_check_status,omitempty"`
	LastCheckMessage   string                 `json:"last_check_message,omitempty"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

func (c *ELKConnection) ConfigurationStatus() string {
	if c == nil || !c.Enabled {
		return "disabled"
	}
	if strings.TrimSpace(c.Endpoint) == "" || strings.TrimSpace(c.IndexPattern) == "" || strings.TrimSpace(c.TimestampField) == "" || strings.TrimSpace(c.MessageField) == "" {
		return "unconfigured"
	}
	switch c.AuthType {
	case ELKAuthAPIKey:
		if strings.TrimSpace(c.APIKeySecretName) == "" {
			return "unconfigured"
		}
	case ELKAuthBasic:
		if strings.TrimSpace(c.UsernameSecretName) == "" || strings.TrimSpace(c.PasswordSecretName) == "" {
			return "unconfigured"
		}
	default:
		return "unconfigured"
	}
	return "ready"
}

// Validate permits intentionally incomplete connections so the plumbing can be
// prepared before endpoint and credential details are available.
func (c *ELKConnection) Validate() error {
	if c == nil {
		return errors.New("ELK connection is required")
	}
	c.ID, c.ProjectID, c.Name = strings.TrimSpace(c.ID), strings.TrimSpace(c.ProjectID), strings.TrimSpace(c.Name)
	if c.ID == "" || c.ProjectID == "" || c.Name == "" {
		return errors.New("ELK connection id, project id, and name are required")
	}
	if endpoint := strings.TrimSpace(c.Endpoint); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return errors.New("ELK endpoint must be an https URL without embedded credentials")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("ELK endpoint must not contain a query or fragment")
		}
		c.Endpoint = strings.TrimRight(endpoint, "/")
	}
	if c.AuthType != "" && c.AuthType != ELKAuthAPIKey && c.AuthType != ELKAuthBasic {
		return fmt.Errorf("unsupported ELK authentication type %q", c.AuthType)
	}
	if len(c.MetricAggregations) > 16 {
		return errors.New("ELK connections support at most 16 metric aggregations")
	}
	seen := make(map[string]struct{}, len(c.MetricAggregations))
	for i := range c.MetricAggregations {
		a := &c.MetricAggregations[i]
		a.Name, a.Field, a.Operation = strings.TrimSpace(a.Name), strings.TrimSpace(a.Field), strings.ToLower(strings.TrimSpace(a.Operation))
		if !elkMetricNamePattern.MatchString(a.Name) || a.Field == "" {
			return fmt.Errorf("ELK metric aggregation %d requires a safe name and field", i)
		}
		switch a.Operation {
		case "avg", "min", "max", "sum":
		default:
			return fmt.Errorf("ELK metric aggregation %q has unsupported operation %q", a.Name, a.Operation)
		}
		key := strings.ToLower(a.Name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate ELK metric aggregation name %q", a.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type ELKConnectionStore interface {
	CreateELKConnection(*ELKConnection) error
	UpdateELKConnectionIfUnchanged(*ELKConnection, time.Time) (bool, error)
	UpdateELKConnectionHealth(id, projectID, status, message string, checkedAt time.Time) error
	GetELKConnection(id string) (*ELKConnection, error)
	ListELKConnections(projectID string) ([]*ELKConnection, error)
	DeleteELKConnection(id, projectID string) (bool, error)
}
