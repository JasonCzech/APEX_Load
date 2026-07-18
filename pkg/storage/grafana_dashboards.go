package storage

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/grafana"
)

// MaxGrafanaDashboardsPerProject bounds aggregate query fan-out and storage. The API
// also uses this value when loading aggregate results.
const MaxGrafanaDashboardsPerProject = 20

const (
	MaxGrafanaDashboardNameBytes        = 256
	MaxGrafanaDashboardDescriptionBytes = 4096
	MaxGrafanaDashboardSnapshotBytes    = 64 << 20
)

var (
	ErrGrafanaDashboardLimit     = errors.New("grafana dashboard limit reached")
	ErrGrafanaImportSessionLimit = errors.New("grafana import session limit reached")
	ErrGrafanaImportClaimLost    = errors.New("grafana import session claim lost")
)

// GrafanaDashboard is an imported Grafana dashboard: the parsed panel set plus the
// committed variable bindings and import diagnostics. Panels carry PromQL that is run
// against the project's configured Prometheus during a run and persisted in an
// immutable terminal-run snapshot. The raw dashboard JSON is not retained; only its
// SHA-256 is stored for provenance, and a re-import re-derives the panels from source.
type GrafanaDashboard struct {
	ID            string            `json:"id"`
	ProjectID     string            `json:"project_id"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	SourceTitle   string            `json:"source_title,omitempty"`
	SourceUID     string            `json:"source_uid,omitempty"`
	SourceSHA256  string            `json:"source_sha256"`
	SchemaVersion int               `json:"schema_version"`
	Enabled       bool              `json:"enabled"`
	Panels        []grafana.Panel   `json:"panels"`
	Bindings      map[string]string `json:"bindings,omitempty"`
	Diagnostics   diag.Diagnostics  `json:"diagnostics,omitempty"`
	CreatedBy     string            `json:"created_by,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	// ImportSessionID/ImportClaimToken fence two-phase import commit. They are
	// transient inputs consumed atomically by SaveGrafanaDashboard and never stored
	// with, or returned as part of, the dashboard.
	ImportSessionID  string `json:"-"`
	ImportClaimToken string `json:"-"`
}

// GrafanaImportSession is the durable, short-lived source retained between inspect
// and commit. ClaimToken serializes commit across controller replicas.
type GrafanaImportSession struct {
	ID           string
	ProjectID    string
	Name         string
	Content      string
	SourceSHA256 string
	ExpiresAt    time.Time
	ClaimToken   string
	ClaimedAt    time.Time
	CreatedAt    time.Time
}

// GrafanaDashboardSnapshot stores the bounded, resolved dashboard payload associated
// with a test. Payload is JSON so storage does not depend on API response types.
type GrafanaDashboardSnapshot struct {
	TestID     string
	ProjectID  string
	Payload    json.RawMessage
	CapturedAt time.Time
}
