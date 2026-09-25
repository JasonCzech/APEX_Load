package storage

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// ConfluenceConnection is project scoped. API token values live only in the
// project secret provider; TokenSecretName is never serialized to API clients.
type ConfluenceConnection struct {
	ID               string     `json:"id"`
	ProjectID        string     `json:"project_id"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	SiteURL          string     `json:"site_url"`
	Email            string     `json:"email"`
	SpaceID          string     `json:"space_id"`
	ParentPageID     string     `json:"parent_page_id"`
	TokenSecretName  string     `json:"-"`
	AutoPublish      bool       `json:"auto_publish"`
	LastCheckedAt    *time.Time `json:"last_checked_at,omitempty"`
	LastCheckStatus  string     `json:"last_check_status,omitempty"`
	LastCheckMessage string     `json:"last_check_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (c *ConfluenceConnection) Validate() error {
	if c == nil {
		return errors.New("Confluence connection is required")
	}
	c.ID, c.ProjectID, c.Name = strings.TrimSpace(c.ID), strings.TrimSpace(c.ProjectID), strings.TrimSpace(c.Name)
	c.SiteURL, c.Email = strings.TrimRight(strings.TrimSpace(c.SiteURL), "/"), strings.TrimSpace(c.Email)
	c.SpaceID, c.ParentPageID, c.TokenSecretName = strings.TrimSpace(c.SpaceID), strings.TrimSpace(c.ParentPageID), strings.TrimSpace(c.TokenSecretName)
	if c.ID == "" || c.ProjectID == "" || c.Name == "" {
		return errors.New("connection id, project id and name are required")
	}
	if c.SiteURL != "" {
		u, err := url.Parse(c.SiteURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/wiki") {
			return errors.New("Confluence site URL must be an https Cloud site URL without credentials, query or fragment")
		}
		c.SiteURL = strings.TrimSuffix(c.SiteURL, "/wiki")
	}
	for label, id := range map[string]string{"space ID": c.SpaceID, "parent page ID": c.ParentPageID} {
		if id == "" {
			continue
		}
		for _, digit := range id {
			if digit < '0' || digit > '9' {
				return errors.New("Confluence " + label + " must be numeric")
			}
		}
	}
	if c.Enabled && (c.SiteURL == "" || c.Email == "" || c.SpaceID == "" || c.ParentPageID == "" || c.TokenSecretName == "") {
		return errors.New("enabled Confluence connection requires site URL, email, space ID, parent page ID and API token secret")
	}
	if c.AutoPublish && !c.Enabled {
		return errors.New("automatic publishing requires an enabled connection")
	}
	return nil
}

type ConfluencePublication struct {
	TestID       string    `json:"test_id"`
	ProjectID    string    `json:"project_id"`
	ConnectionID string    `json:"connection_id"`
	PageID       string    `json:"page_id,omitempty"`
	PageURL      string    `json:"page_url,omitempty"`
	Status       string    `json:"status"`
	Automatic    bool      `json:"automatic"`
	Error        string    `json:"error,omitempty"`
	Attempts     int       `json:"attempts"`
	RequestedBy  string    `json:"requested_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ConfluenceStore interface {
	CreateConfluenceConnection(*ConfluenceConnection) error
	GetConfluenceConnection(id string) (*ConfluenceConnection, error)
	ListConfluenceConnections(projectID string) ([]*ConfluenceConnection, error)
	UpdateConfluenceConnectionIfUnchanged(*ConfluenceConnection, time.Time) (bool, error)
	UpdateConfluenceConnectionHealth(id, projectID, status, message string, checkedAt, expectedUpdatedAt time.Time) error
	DeleteConfluenceConnection(id, projectID string) (bool, error)
	GetConfluencePublication(testID, connectionID string) (*ConfluencePublication, error)
	ListConfluencePublications(testID string) ([]*ConfluencePublication, error)
	ClaimConfluencePublication(testID, projectID, connectionID, actor string, automatic bool) (*ConfluencePublication, bool, error)
	FinishConfluencePublication(testID, connectionID, status, pageID, pageURL, message string) error
	AbandonConfluencePublication(testID, connectionID, message string) error
	ListPendingConfluencePublications(limit int) ([]*ConfluencePublication, error)
}
