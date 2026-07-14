package storage

import "time"

// FleetPool is a named, cloud-managed group of load generators with sizing
// configuration — the managed-fleet analog of LoadRunner Enterprise host pools.
// Credentials/provisioning config are intentionally NOT modeled here yet; they
// arrive with a real provider integration so this surface carries no secrets.
type FleetPool struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Description   string             `json:"description,omitempty"`
	CloudProvider string             `json:"cloud_provider"` // kubernetes, aws, azure, gcp, static
	Region        string             `json:"region,omitempty"`
	MinSize       int                `json:"min_size,omitempty"`
	MaxSize       int                `json:"max_size,omitempty"`
	DesiredSize   int                `json:"desired_size,omitempty"`
	Status        string             `json:"status"` // active, suspended, error
	Tags          map[string]string  `json:"tags,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Members       []*FleetPoolMember `json:"members,omitempty"`
}

// FleetPoolMember links a generator to a pool.
type FleetPoolMember struct {
	ID          string    `json:"id"`
	PoolID      string    `json:"pool_id"`
	GeneratorID string    `json:"generator_id"`
	Target      string    `json:"target"` // host:port or pod name
	Status      string    `json:"status"` // provisioned, ready, draining, terminated
	JoinedAt    time.Time `json:"joined_at"`
}

// GeneratorMetadata is persistent, operator-managed metadata for a generator —
// labels/tags, pool membership, cordon state, and provider identity — keyed by a
// stable generator_id. It augments the in-memory GeneratorInventoryEntry.
type GeneratorMetadata struct {
	GeneratorID    string            `json:"generator_id"`
	Target         string            `json:"target,omitempty"`
	FleetPoolID    *string           `json:"fleet_pool_id,omitempty"`
	Region         string            `json:"region,omitempty"`
	ProviderID     string            `json:"provider_id,omitempty"`     // e.g. aws:i-..., gcp:instances/...
	ProviderStatus string            `json:"provider_status,omitempty"` // running, stopping, stopped, error
	Cordoned       bool              `json:"cordoned,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// ProvisioningRequest tracks a request to scale a fleet pool. Until a real cloud
// or Kubernetes provisioner is wired, requests resolve to status "not_configured"
// with an explanatory event rather than silently succeeding.
type ProvisioningRequest struct {
	ID                 string               `json:"id"`
	Revision           int                  `json:"revision,omitempty"`
	AttemptToken       string               `json:"-"`
	FleetPoolID        string               `json:"fleet_pool_id"`
	ProjectID          string               `json:"project_id,omitempty"`
	Status             string               `json:"status"` // pending, accepted, scaling, waiting_ready, provisioning, success, failed, not_configured, cancelled, timed_out
	DesiredCount       int                  `json:"desired_count"`
	Reason             string               `json:"reason,omitempty"`
	RequestedBySubject string               `json:"requested_by_subject,omitempty"`
	Events             []*ProvisioningEvent `json:"events,omitempty"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	CompletedAt        *time.Time           `json:"completed_at,omitempty"`
}

// ProvisioningEvent is one entry in a provisioning request's audit trail.
type ProvisioningEvent struct {
	ID        string    `json:"id"`
	RequestID string    `json:"request_id"`
	EventType string    `json:"event_type"` // requested, retry, not_configured, provisioning, success, failure, cancelled
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// FleetAlertAck persists an operator acknowledgement for a currently-derived
// fleet alert. Alerts themselves are still derived from live inventory; this row
// records that an admin has seen and accepted/routed the condition.
type FleetAlertAck struct {
	ID                        string    `json:"id"`
	AlertType                 string    `json:"alert_type"`
	Target                    string    `json:"target"`
	Region                    string    `json:"region,omitempty"`
	Severity                  string    `json:"severity"`
	Summary                   string    `json:"summary"`
	Note                      string    `json:"note,omitempty"`
	AcknowledgedBySubject     string    `json:"acknowledged_by_subject,omitempty"`
	AcknowledgedByDisplayName string    `json:"acknowledged_by_display_name,omitempty"`
	AcknowledgedAt            time.Time `json:"acknowledged_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}
