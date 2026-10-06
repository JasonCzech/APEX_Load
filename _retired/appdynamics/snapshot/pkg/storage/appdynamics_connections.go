package storage

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
)

// AppDynamicsConnection contains safe connection settings and logical project
// secret references. Resolved credentials never persist in this record.
type AppDynamicsConnection struct {
	ID                     string                   `json:"id"`
	ProjectID              string                   `json:"project_id"`
	Name                   string                   `json:"name"`
	Description            string                   `json:"description,omitempty"`
	Enabled                bool                     `json:"enabled"`
	CollectionMode         string                   `json:"collection_mode"`
	CollectorAgentID       string                   `json:"collector_agent_id,omitempty"`
	Config                 appdynamics.Config       `json:"config"`
	Revision               int64                    `json:"revision"`
	VerifiedRevision       int64                    `json:"verified_revision"`
	Capabilities           appdynamics.Capabilities `json:"capabilities"`
	ClientIDSecretName     string                   `json:"-"`
	ClientSecretSecretName string                   `json:"-"`
	UsernameSecretName     string                   `json:"-"`
	PasswordSecretName     string                   `json:"-"`
	EventsAPIKeySecretName string                   `json:"-"`
	LastCheckedAt          *time.Time               `json:"last_checked_at,omitempty"`
	LastCheckStatus        string                   `json:"last_check_status,omitempty"`
	LastCheckMessage       string                   `json:"last_check_message,omitempty"`
	CreatedAt              time.Time                `json:"created_at"`
	UpdatedAt              time.Time                `json:"updated_at"`
}

func (c *AppDynamicsConnection) Validate() error {
	if c == nil {
		return errors.New("AppDynamics connection is required")
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
	for _, v := range []string{c.ClientIDSecretName, c.ClientSecretSecretName, c.UsernameSecretName, c.PasswordSecretName, c.EventsAPIKeySecretName} {
		if len(v) > 256 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid secret reference")
		}
	}
	return nil
}
func (c *AppDynamicsConnection) ConfigurationStatus() string {
	if c == nil || !c.Enabled {
		return "disabled"
	}
	if c.Config.Validate() != nil {
		return "unconfigured"
	}
	switch c.Config.Defaults().AuthType {
	case appdynamics.AuthOAuth:
		if c.ClientIDSecretName == "" || c.ClientSecretSecretName == "" {
			return "unconfigured"
		}
	case appdynamics.AuthBasic:
		if c.UsernameSecretName == "" || c.PasswordSecretName == "" {
			return "unconfigured"
		}
	}
	if c.Config.EventsEndpoint != "" && (c.Config.EventsAccountName == "" || c.EventsAPIKeySecretName == "") {
		return "unconfigured"
	}
	if c.Revision <= 0 || c.VerifiedRevision != c.Revision || c.LastCheckStatus != "connected" {
		return "unverified"
	}
	return "ready"
}
func (c *AppDynamicsConnection) Readiness() string                 { return c.ConfigurationStatus() }
func (c *AppDynamicsConnection) RuntimeConfig() appdynamics.Config { return c.Config.Defaults() }

// SameAppDynamicsIdentity determines whether previous access verification is
// still valid. Display metadata and enabled state do not change the identity.
func SameAppDynamicsIdentity(a, b *AppDynamicsConnection) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.ProjectID == b.ProjectID && a.CollectionMode == b.CollectionMode && a.CollectorAgentID == b.CollectorAgentID && reflect.DeepEqual(a.Config.Defaults(), b.Config.Defaults()) && a.ClientIDSecretName == b.ClientIDSecretName && a.ClientSecretSecretName == b.ClientSecretSecretName && a.UsernameSecretName == b.UsernameSecretName && a.PasswordSecretName == b.PasswordSecretName && a.EventsAPIKeySecretName == b.EventsAPIKeySecretName
}

// AppDynamicsConnectionSnapshot is private durable job state. It must never be
// included in API projections, collector grants or browser payloads.
type AppDynamicsConnectionSnapshot struct {
	ConnectionID           string             `json:"connection_id"`
	ProjectID              string             `json:"project_id"`
	Revision               int64              `json:"revision"`
	Config                 appdynamics.Config `json:"config"`
	CollectionMode         string             `json:"collection_mode"`
	CollectorAgentID       string             `json:"collector_agent_id,omitempty"`
	ClientIDSecretName     string             `json:"client_id_secret_name,omitempty"`
	ClientSecretSecretName string             `json:"client_secret_secret_name,omitempty"`
	UsernameSecretName     string             `json:"username_secret_name,omitempty"`
	PasswordSecretName     string             `json:"password_secret_name,omitempty"`
	EventsAPIKeySecretName string             `json:"events_api_key_secret_name,omitempty"`
}

func (c *AppDynamicsConnection) Snapshot() *AppDynamicsConnectionSnapshot {
	if c == nil {
		return nil
	}
	cfg := c.Config
	cfg.ApprovedCIDRs = append([]string(nil), cfg.ApprovedCIDRs...)
	return &AppDynamicsConnectionSnapshot{ConnectionID: c.ID, ProjectID: c.ProjectID, Revision: c.Revision, Config: cfg, CollectionMode: c.CollectionMode, CollectorAgentID: c.CollectorAgentID, ClientIDSecretName: c.ClientIDSecretName, ClientSecretSecretName: c.ClientSecretSecretName, UsernameSecretName: c.UsernameSecretName, PasswordSecretName: c.PasswordSecretName, EventsAPIKeySecretName: c.EventsAPIKeySecretName}
}

type appDynamicsPrivate struct {
	Public  AppDynamicsConnection         `json:"public"`
	Secrets AppDynamicsConnectionSnapshot `json:"secrets"`
}

func MarshalAppDynamicsConnectionPrivate(c *AppDynamicsConnection) ([]byte, error) {
	if c == nil {
		return nil, errors.New("connection required")
	}
	return json.Marshal(appDynamicsPrivate{Public: *c, Secrets: *c.Snapshot()})
}
func UnmarshalAppDynamicsConnectionPrivate(data []byte) (*AppDynamicsConnection, error) {
	var dto appDynamicsPrivate
	if err := json.Unmarshal(data, &dto); err != nil {
		return nil, err
	}
	c := dto.Public
	c.ClientIDSecretName = dto.Secrets.ClientIDSecretName
	c.ClientSecretSecretName = dto.Secrets.ClientSecretSecretName
	c.UsernameSecretName = dto.Secrets.UsernameSecretName
	c.PasswordSecretName = dto.Secrets.PasswordSecretName
	c.EventsAPIKeySecretName = dto.Secrets.EventsAPIKeySecretName
	return &c, nil
}

type AppDynamicsConnectionStore interface {
	EnsureAppDynamicsSchema() error
	CreateAppDynamicsConnection(*AppDynamicsConnection) error
	GetAppDynamicsConnection(string) (*AppDynamicsConnection, error)
	ListAppDynamicsConnections(string) ([]*AppDynamicsConnection, error)
	UpdateAppDynamicsConnectionIfRevision(*AppDynamicsConnection, int64) (bool, error)
	UpdateAppDynamicsConnectionHealth(id, projectID string, expected int64, status, message string, capabilities appdynamics.Capabilities, checkedAt time.Time) (bool, error)
	DeleteAppDynamicsConnection(id, projectID string) (bool, error)
	DeleteAppDynamicsConnectionIfRevision(id, projectID string, expected int64) (bool, error)
}
