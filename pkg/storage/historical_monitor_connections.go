package storage

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorcontract"
)

// AppDynamicsConnection contains safe connection settings and logical project
// secret references. Resolved credentials never persist in this record.
type AppDynamicsConnection struct {
	ID                     string                                  `json:"id"`
	ProjectID              string                                  `json:"project_id"`
	Name                   string                                  `json:"name"`
	Description            string                                  `json:"description,omitempty"`
	Enabled                bool                                    `json:"enabled"`
	CollectionMode         string                                  `json:"collection_mode"`
	CollectorAgentID       string                                  `json:"collector_agent_id,omitempty"`
	Config                 monitorcontract.AppDynamicsConfig       `json:"config"`
	Revision               int64                                   `json:"revision"`
	VerifiedRevision       int64                                   `json:"verified_revision"`
	Capabilities           monitorcontract.AppDynamicsCapabilities `json:"capabilities"`
	ClientIDSecretName     string                                  `json:"-"`
	ClientSecretSecretName string                                  `json:"-"`
	UsernameSecretName     string                                  `json:"-"`
	PasswordSecretName     string                                  `json:"-"`
	EventsAPIKeySecretName string                                  `json:"-"`
	LastCheckedAt          *time.Time                              `json:"last_checked_at,omitempty"`
	LastCheckStatus        string                                  `json:"last_check_status,omitempty"`
	LastCheckMessage       string                                  `json:"last_check_message,omitempty"`
	CreatedAt              time.Time                               `json:"created_at"`
	UpdatedAt              time.Time                               `json:"updated_at"`
}

// AppDynamicsConnectionSnapshot is private durable job state. It must never be
// included in API projections, collector grants or browser payloads.
type AppDynamicsConnectionSnapshot struct {
	ConnectionID           string                            `json:"connection_id"`
	ProjectID              string                            `json:"project_id"`
	Revision               int64                             `json:"revision"`
	Config                 monitorcontract.AppDynamicsConfig `json:"config"`
	CollectionMode         string                            `json:"collection_mode"`
	CollectorAgentID       string                            `json:"collector_agent_id,omitempty"`
	ClientIDSecretName     string                            `json:"client_id_secret_name,omitempty"`
	ClientSecretSecretName string                            `json:"client_secret_secret_name,omitempty"`
	UsernameSecretName     string                            `json:"username_secret_name,omitempty"`
	PasswordSecretName     string                            `json:"password_secret_name,omitempty"`
	EventsAPIKeySecretName string                            `json:"events_api_key_secret_name,omitempty"`
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

// AppDynamicsConnectionStore supports retained metadata and secret references only.
type AppDynamicsConnectionStore interface {
	GetAppDynamicsConnection(string) (*AppDynamicsConnection, error)
	ListAppDynamicsConnections(string) ([]*AppDynamicsConnection, error)
}
