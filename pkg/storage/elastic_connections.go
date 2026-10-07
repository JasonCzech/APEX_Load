package storage

import (
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/elastic"
	"maps"
	"reflect"
	"strings"
	"time"
)

type ComponentSecretReferences struct {
	AuthType                    string `json:"auth_type,omitempty"`
	APIKeySecretName            string `json:"api_key_secret_name,omitempty"`
	UsernameSecretName          string `json:"username_secret_name,omitempty"`
	PasswordSecretName          string `json:"password_secret_name,omitempty"`
	BearerTokenSecretName       string `json:"bearer_token_secret_name,omitempty"`
	ClientCertificateSecretName string `json:"client_certificate_secret_name,omitempty"`
	ClientKeySecretName         string `json:"client_key_secret_name,omitempty"`
}
type ExpandedElasticConnectionSnapshot struct {
	KibanaCredentials   ComponentSecretReferences `json:"kibana_credentials,omitempty"`
	LogstashCredentials ComponentSecretReferences `json:"logstash_credentials,omitempty"`

	ConnectionID                string               `json:"connection_id"`
	ProjectID                   string               `json:"project_id"`
	Revision                    int64                `json:"revision"`
	Config                      elastic.Config       `json:"config"`
	Capabilities                elastic.Capabilities `json:"capabilities"`
	CollectionMode              string               `json:"collection_mode"`
	CollectorAgentID            string               `json:"collector_agent_id,omitempty"`
	APIKeySecretName            string               `json:"api_key_secret_name,omitempty"`
	UsernameSecretName          string               `json:"username_secret_name,omitempty"`
	PasswordSecretName          string               `json:"password_secret_name,omitempty"`
	BearerTokenSecretName       string               `json:"bearer_token_secret_name,omitempty"`
	ClientCertificateSecretName string               `json:"client_certificate_secret_name,omitempty"`
	ClientKeySecretName         string               `json:"client_key_secret_name,omitempty"`
}

func (c *ELKConnection) RuntimeConfig() elastic.Config {
	cfg := c.Config
	if cfg.Endpoint == "" {
		cfg.Endpoint = c.Endpoint
	}
	if cfg.Product == "" {
		cfg.Product = c.Product
	}
	if cfg.Product == "" {
		cfg.Product = elastic.ProductElasticsearch
	}
	if cfg.AuthType == "" {
		cfg.AuthType = c.AuthType
	}
	return cfg.Defaults()
}
func (c *ELKConnection) validateElasticProfile() error {
	if c.Config.Endpoint == "" {
		return nil
	}
	if len(c.Name) > 256 || len(c.Description) > 4096 {
		return errors.New("Elastic connection identity exceeds bounds")
	}
	if c.CollectionMode == "" {
		c.CollectionMode = "direct"
	}
	if c.CollectionMode != "direct" && c.CollectionMode != "collector" {
		return errors.New("unsupported Elastic execution mode")
	}
	if c.CollectionMode == "collector" && strings.TrimSpace(c.CollectorAgentID) == "" || len(c.CollectorAgentID) > 256 {
		return errors.New("Elastic collector assignment required")
	}
	cfg := c.RuntimeConfig()
	if err := cfg.Validate(); err != nil {
		return err
	}
	c.Config = cfg
	c.Endpoint = cfg.Endpoint
	c.AuthType = cfg.AuthType
	c.Product = cfg.Product
	for _, v := range []string{c.APIKeySecretName, c.UsernameSecretName, c.PasswordSecretName, c.BearerTokenSecretName, c.ClientCertificateSecretName, c.ClientKeySecretName} {
		if len(v) > 256 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid Elastic secret reference")
		}
	}
	for _, component := range []ComponentSecretReferences{c.KibanaCredentials, c.LogstashCredentials} {
		for _, v := range []string{component.APIKeySecretName, component.UsernameSecretName, component.PasswordSecretName, component.BearerTokenSecretName, component.ClientCertificateSecretName, component.ClientKeySecretName} {
			if len(v) > 256 || strings.ContainsAny(v, "\r\n\x00") {
				return errors.New("invalid component secret reference")
			}
		}
		if component.AuthType != "" && component.AuthType != "api_key" && component.AuthType != "basic" && component.AuthType != "bearer" && component.AuthType != "mtls" {
			return errors.New("invalid component authentication")
		}
	}
	return nil
}
func (c *ELKConnection) elasticConfigurationStatus() string {
	cfg := c.RuntimeConfig()
	if cfg.Validate() != nil {
		return "unconfigured"
	}
	switch cfg.AuthType {
	case elastic.AuthAPIKey:
		if c.APIKeySecretName == "" {
			return "unconfigured"
		}
	case elastic.AuthBasic:
		if c.UsernameSecretName == "" || c.PasswordSecretName == "" {
			return "unconfigured"
		}
	case elastic.AuthBearer:
		if c.BearerTokenSecretName == "" {
			return "unconfigured"
		}
	case elastic.AuthMTLS:
		if c.ClientCertificateSecretName == "" || c.ClientKeySecretName == "" {
			return "unconfigured"
		}
	default:
		return "unconfigured"
	}
	if c.Revision < 1 || c.VerifiedRevision != c.Revision || c.LastCheckStatus != "connected" {
		return "unverified"
	}
	return "ready"
}
func (c *ELKConnection) Snapshot() *ExpandedElasticConnectionSnapshot {
	if c == nil {
		return nil
	}
	cfg := c.RuntimeConfig()
	cfg.ApprovedCIDRs = append([]string(nil), cfg.ApprovedCIDRs...)
	cfg.KibanaApprovedCIDRs = append([]string(nil), cfg.KibanaApprovedCIDRs...)
	cfg.LogstashApprovedCIDRs = append([]string(nil), cfg.LogstashApprovedCIDRs...)
	caps := c.Capabilities
	caps.Status = maps.Clone(caps.Status)
	mode := c.CollectionMode
	if mode == "" {
		mode = "direct"
	}
	return &ExpandedElasticConnectionSnapshot{KibanaCredentials: c.KibanaCredentials, LogstashCredentials: c.LogstashCredentials, Capabilities: caps, ConnectionID: c.ID, ProjectID: c.ProjectID, Revision: c.Revision, Config: cfg, CollectionMode: mode, CollectorAgentID: c.CollectorAgentID, APIKeySecretName: c.APIKeySecretName, UsernameSecretName: c.UsernameSecretName, PasswordSecretName: c.PasswordSecretName, BearerTokenSecretName: c.BearerTokenSecretName, ClientCertificateSecretName: c.ClientCertificateSecretName, ClientKeySecretName: c.ClientKeySecretName}
}

type ElasticConnectionStore interface {
	ELKConnectionStore
	UpdateElasticConnectionIfRevision(*ELKConnection, int64) (bool, error)
	UpdateElasticConnectionHealth(string, string, int64, string, string, elastic.Capabilities, time.Time) (bool, error)
}
type elasticConnectionPrivate struct {
	Public  ELKConnection                      `json:"public"`
	Secrets *ExpandedElasticConnectionSnapshot `json:"secrets"`
}

func marshalElasticConnection(c *ELKConnection) ([]byte, error) {
	return json.Marshal(elasticConnectionPrivate{*c, c.Snapshot()})
}
func applyElasticConnectionProfile(c *ELKConnection, body string) error {
	if body == "" {
		c.Revision = 1
		return nil
	}
	var p elasticConnectionPrivate
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return err
	}
	c.Product, c.Config, c.Revision, c.VerifiedRevision = p.Public.Product, p.Public.Config, p.Public.Revision, p.Public.VerifiedRevision
	c.CollectionMode, c.CollectorAgentID, c.Capabilities = p.Public.CollectionMode, p.Public.CollectorAgentID, p.Public.Capabilities
	if p.Secrets != nil {
		c.KibanaCredentials = p.Secrets.KibanaCredentials
		c.LogstashCredentials = p.Secrets.LogstashCredentials
		c.BearerTokenSecretName = p.Secrets.BearerTokenSecretName
		c.ClientCertificateSecretName = p.Secrets.ClientCertificateSecretName
		c.ClientKeySecretName = p.Secrets.ClientKeySecretName
	}
	return nil
}
func SameElasticIdentity(a, b *ELKConnection) bool {
	if a == nil || b == nil {
		return false
	}
	return a.KibanaCredentials == b.KibanaCredentials && a.LogstashCredentials == b.LogstashCredentials && reflect.DeepEqual(a.Snapshot().Config, b.Snapshot().Config) && a.CollectionMode == b.CollectionMode && a.CollectorAgentID == b.CollectorAgentID && a.APIKeySecretName == b.APIKeySecretName && a.UsernameSecretName == b.UsernameSecretName && a.PasswordSecretName == b.PasswordSecretName && a.BearerTokenSecretName == b.BearerTokenSecretName && a.ClientCertificateSecretName == b.ClientCertificateSecretName && a.ClientKeySecretName == b.ClientKeySecretName
}
func (s *SQLiteStore) UpdateElasticConnectionIfRevision(c *ELKConnection, expected int64) (bool, error) {
	old, err := s.GetELKConnection(c.ID)
	if err != nil {
		return false, err
	}
	if old.ProjectID != c.ProjectID || old.Revision != expected {
		return false, nil
	}
	return s.UpdateELKConnectionIfUnchanged(c, old.UpdatedAt)
}
func (s *SQLiteStore) UpdateElasticConnectionHealth(id, project string, expected int64, status, message string, caps elastic.Capabilities, at time.Time) (bool, error) {
	c, err := s.GetELKConnection(id)
	if err != nil {
		return false, err
	}
	if c.ProjectID != project || c.Revision != expected {
		return false, nil
	}
	if len(message) > 1024 || len(caps.Status) > 32 {
		return false, errors.New("invalid Elastic verification result")
	}
	c.VerifiedRevision = 0
	if status == "connected" {
		c.VerifiedRevision = expected
	}
	c.Capabilities = caps
	c.LastCheckStatus = status
	c.LastCheckMessage = message
	c.LastCheckedAt = &at
	b, err := marshalElasticConnection(c)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`UPDATE elk_connections SET elastic_profile_json=?,last_checked_at=?,last_check_status=?,last_check_message=? WHERE id=? AND project_id=? AND updated_at=?`, string(b), at.UTC(), status, message, id, project, c.UpdatedAt)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (c *ELKConnection) Readiness() string { return c.ConfigurationStatus() }
