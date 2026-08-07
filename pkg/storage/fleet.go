package storage

import (
	"errors"
	"time"
)

var (
	// ErrFleetPoolHasActiveProvisioning prevents deleting the durable identity
	// for a pool while an external scale attempt can still complete against it.
	ErrFleetPoolHasActiveProvisioning = errors.New("fleet pool has an active provisioning request")
	// ErrFleetPoolNotActive prevents a stale preflight read from launching a new
	// provisioning attempt after an administrator suspended the pool.
	ErrFleetPoolNotActive = errors.New("fleet pool is not active")
	// ErrGeneratorAlreadyInPool enforces one generator, one pool. A pool carries
	// at most one domain, so a generator in two pools could be claimed by two
	// domains at once and the domain scoping would mean nothing.
	ErrGeneratorAlreadyInPool = errors.New("generator already belongs to another fleet pool")
)

// FleetPool is a named, cloud-managed group of load generators with sizing
// configuration — the managed-fleet analog of LoadRunner Enterprise host pools.
// Credentials are still NOT modeled here: a pool references a registered
// ClusterConnection by ID, and that connection owns the credential.
type FleetPool struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	CloudProvider string            `json:"cloud_provider"` // kubernetes, aws, azure, gcp, static
	Region        string            `json:"region,omitempty"`
	MinSize       int               `json:"min_size,omitempty"`
	MaxSize       int               `json:"max_size,omitempty"`
	DesiredSize   int               `json:"desired_size,omitempty"`
	Status        string            `json:"status"` // active, suspended, error
	Tags          map[string]string `json:"tags,omitempty"`
	// ClusterID binds the pool to a registered cluster connection. Empty means
	// the controller's env-configured provisioner, which is what every pool
	// created before the cluster registry uses.
	ClusterID string `json:"cluster_id,omitempty"`
	// DomainID scopes the pool to a domain, which subjects it to that domain's
	// deployment target. Empty means UNSCOPED: the constraint does not apply,
	// which is what every pool created before domain targets carries and is why
	// adding a target can never break an existing deployment.
	DomainID string `json:"domain_id,omitempty"`
	// Allocation is the pod resource budget and node placement applied to this
	// pool's generators. Nil means the workload's own manifest decides.
	Allocation *PoolAllocation    `json:"allocation,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Members    []*FleetPoolMember `json:"members,omitempty"`
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

// GeneratorMetadataPatch carries presence-aware partial edits. FleetPoolIDSet
// distinguishes an omitted membership edit from an explicit nil (detach).
type GeneratorMetadataPatch struct {
	Target         *string
	FleetPoolIDSet bool
	FleetPoolID    *string
	Region         *string
	ProviderID     *string
	ProviderStatus *string
	Cordoned       *bool
	Tags           map[string]string
	TagsSet        bool
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
