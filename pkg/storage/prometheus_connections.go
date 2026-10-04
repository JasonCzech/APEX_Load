package storage

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/prometheus"
)

// PrometheusConnection contains safe connection settings and logical project
// secret references. Resolved credentials never persist in this record.
type PrometheusConnection struct {
	ID                          string                  `json:"id"`
	ProjectID                   string                  `json:"project_id"`
	Name                        string                  `json:"name"`
	Description                 string                  `json:"description,omitempty"`
	Enabled                     bool                    `json:"enabled"`
	CollectionMode              string                  `json:"collection_mode"`
	CollectorAgentID            string                  `json:"collector_agent_id,omitempty"`
	Config                      prometheus.Config       `json:"config"`
	Revision                    int64                   `json:"revision"`
	VerifiedRevision            int64                   `json:"verified_revision"`
	Capabilities                prometheus.Capabilities `json:"capabilities"`
	TokenSecretName             string                  `json:"-"`
	ClientSecretSecretName      string                  `json:"-"`
	UsernameSecretName          string                  `json:"-"`
	PasswordSecretName          string                  `json:"-"`
	ClientCertificateSecretName string                  `json:"-"`
	ClientKeySecretName         string                  `json:"-"`
	LastCheckedAt               *time.Time              `json:"last_checked_at,omitempty"`
	LastCheckStatus             string                  `json:"last_check_status,omitempty"`
	LastCheckMessage            string                  `json:"last_check_message,omitempty"`
	CreatedAt                   time.Time               `json:"created_at"`
	UpdatedAt                   time.Time               `json:"updated_at"`
}

func (c *PrometheusConnection) Validate() error {
	if c == nil {
		return errors.New("Prometheus connection is required")
	}
	c.ID = strings.TrimSpace(c.ID)
	c.ProjectID = strings.TrimSpace(c.ProjectID)
	c.Name = strings.TrimSpace(c.Name)
	if c.ID == "" || c.ProjectID == "" || c.Name == "" || len(c.ID) > 256 || len(c.ProjectID) > 256 || len(c.Name) > 256 || len(c.Description) > 4096 {
		return errors.New("connection identity and bounded name are required")
	}
	if c.CollectionMode == "" {
		c.CollectionMode = "direct"
	}
	if c.CollectionMode != "direct" && c.CollectionMode != "collector" {
		return errors.New("unsupported collection mode")
	}
	if c.CollectionMode == "collector" && strings.TrimSpace(c.CollectorAgentID) == "" {
		return errors.New("collector mode requires a collector ID")
	}
	if c.CollectionMode == "direct" && c.CollectorAgentID != "" {
		return errors.New("direct collection cannot retain a collector assignment")
	}
	if len(c.CollectorAgentID) > 256 {
		return errors.New("collector ID exceeds bound")
	}
	// Incomplete drafts remain savable. Any supplied transport settings must be
	// valid; runtime readiness additionally requires credential references.
	if c.Config.Endpoint != "" {
		if err := c.Config.Validate(); err != nil {
			return err
		}
		c.Config = c.Config.Defaults()
	}
	for _, v := range []string{c.TokenSecretName, c.ClientSecretSecretName, c.UsernameSecretName, c.PasswordSecretName, c.ClientCertificateSecretName, c.ClientKeySecretName} {
		if len(v) > 256 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid secret reference")
		}
	}
	return nil
}
func (c *PrometheusConnection) ConfigurationStatus() string {
	if c == nil || !c.Enabled {
		return "disabled"
	}
	if c.Config.Validate() != nil {
		return "unconfigured"
	}
	switch c.Config.Defaults().AuthType {
	case "bearer":
		if c.TokenSecretName == "" {
			return "unconfigured"
		}
	case "azure":
		if c.Config.AzureCredentialMode == "service_principal" && c.ClientSecretSecretName == "" {
			return "unconfigured"
		}
	case "basic":
		if c.UsernameSecretName == "" || c.PasswordSecretName == "" {
			return "unconfigured"
		}
	}
	if c.Config.MTLS && (c.ClientCertificateSecretName == "" || c.ClientKeySecretName == "") {
		return "unconfigured"
	}
	if c.Revision <= 0 || c.VerifiedRevision != c.Revision || c.LastCheckStatus != "connected" {
		return "unverified"
	}
	return "ready"
}
func (c *PrometheusConnection) Readiness() string                { return c.ConfigurationStatus() }
func (c *PrometheusConnection) RuntimeConfig() prometheus.Config { return c.Config.Defaults() }

// SamePrometheusIdentity determines whether previous access verification is
// still valid. Display metadata and enabled state do not change the identity.
func SamePrometheusIdentity(a, b *PrometheusConnection) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.ProjectID == b.ProjectID && a.CollectionMode == b.CollectionMode && a.CollectorAgentID == b.CollectorAgentID && reflect.DeepEqual(a.Config.Defaults(), b.Config.Defaults()) && a.TokenSecretName == b.TokenSecretName && a.ClientSecretSecretName == b.ClientSecretSecretName && a.UsernameSecretName == b.UsernameSecretName && a.PasswordSecretName == b.PasswordSecretName && a.ClientCertificateSecretName == b.ClientCertificateSecretName && a.ClientKeySecretName == b.ClientKeySecretName
}

// PrometheusConnectionSnapshot is private durable job state. It must never be
// included in API projections, collector grants or browser payloads.
type PrometheusConnectionSnapshot struct {
	ConnectionID                string            `json:"connection_id"`
	ProjectID                   string            `json:"project_id"`
	Revision                    int64             `json:"revision"`
	Config                      prometheus.Config `json:"config"`
	CollectionMode              string            `json:"collection_mode"`
	CollectorAgentID            string            `json:"collector_agent_id,omitempty"`
	TokenSecretName             string            `json:"token_secret_name,omitempty"`
	ClientSecretSecretName      string            `json:"client_secret_secret_name,omitempty"`
	UsernameSecretName          string            `json:"username_secret_name,omitempty"`
	PasswordSecretName          string            `json:"password_secret_name,omitempty"`
	ClientCertificateSecretName string            `json:"client_certificate_secret_name,omitempty"`
	ClientKeySecretName         string            `json:"client_key_secret_name,omitempty"`
}

func (c *PrometheusConnection) Snapshot() *PrometheusConnectionSnapshot {
	if c == nil {
		return nil
	}
	cfg := c.Config
	cfg.ApprovedCIDRs = append([]string(nil), cfg.ApprovedCIDRs...)
	return &PrometheusConnectionSnapshot{ConnectionID: c.ID, ProjectID: c.ProjectID, Revision: c.Revision, Config: cfg, CollectionMode: c.CollectionMode, CollectorAgentID: c.CollectorAgentID, TokenSecretName: c.TokenSecretName, ClientSecretSecretName: c.ClientSecretSecretName, UsernameSecretName: c.UsernameSecretName, PasswordSecretName: c.PasswordSecretName, ClientCertificateSecretName: c.ClientCertificateSecretName, ClientKeySecretName: c.ClientKeySecretName}
}

type prometheusPrivate struct {
	Public  PrometheusConnection         `json:"public"`
	Secrets PrometheusConnectionSnapshot `json:"secrets"`
}

func MarshalPrometheusConnectionPrivate(c *PrometheusConnection) ([]byte, error) {
	if c == nil {
		return nil, errors.New("connection required")
	}
	return json.Marshal(prometheusPrivate{Public: *c, Secrets: *c.Snapshot()})
}
func UnmarshalPrometheusConnectionPrivate(data []byte) (*PrometheusConnection, error) {
	var dto prometheusPrivate
	if err := json.Unmarshal(data, &dto); err != nil {
		return nil, err
	}
	c := dto.Public
	c.TokenSecretName = dto.Secrets.TokenSecretName
	c.ClientSecretSecretName = dto.Secrets.ClientSecretSecretName
	c.UsernameSecretName = dto.Secrets.UsernameSecretName
	c.PasswordSecretName = dto.Secrets.PasswordSecretName
	c.ClientCertificateSecretName = dto.Secrets.ClientCertificateSecretName
	c.ClientKeySecretName = dto.Secrets.ClientKeySecretName
	return &c, nil
}

type PrometheusConnectionStore interface {
	EnsurePrometheusSchema() error
	CreatePrometheusConnection(*PrometheusConnection) error
	GetPrometheusConnection(string) (*PrometheusConnection, error)
	ListPrometheusConnections(string) ([]*PrometheusConnection, error)
	UpdatePrometheusConnectionIfRevision(*PrometheusConnection, int64) (bool, error)
	UpdatePrometheusConnectionHealth(id, projectID string, expected int64, status, message string, capabilities prometheus.Capabilities, checkedAt time.Time) (bool, error)
	DeletePrometheusConnection(id, projectID string) (bool, error)
	DeletePrometheusConnectionIfRevision(id, projectID string, expected int64) (bool, error)
}

func (c *PrometheusConnectionSnapshot) RequiresCredentials() bool {
	return c != nil && (c.Config.MTLS || c.Config.Defaults().AuthType == "basic" || c.Config.Defaults().AuthType == "bearer" || c.Config.Defaults().AuthType == "azure" && c.Config.AzureCredentialMode == "service_principal")
}
