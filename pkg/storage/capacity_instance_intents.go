package storage

import "time"

const (
	CapacityIntentPlanned     = "planned"
	CapacityIntentLaunching   = "launching"
	CapacityIntentReady       = "ready"
	CapacityIntentDraining    = "draining"
	CapacityIntentTerminating = "terminating"
	CapacityIntentTerminated  = "terminated"
	CapacityIntentFailed      = "failed"

	CapacityArchivePending  = "pending"
	CapacityArchiveDraining = "draining"
	CapacityArchiveTerminal = "terminal"
	CapacityArchiveExpired  = "expired"
)

// CapacityInstanceIntent is persisted before the remote launch call. The
// client token makes that call idempotent, while provider tags let startup
// reconciliation recover resources if the database write after launch fails.
type CapacityInstanceIntent struct {
	ID                    string     `json:"id"`
	ExecutionTargetID     string     `json:"execution_target_id"`
	FleetPoolID           string     `json:"fleet_pool_id"`
	TestID                string     `json:"test_id,omitempty"`
	ProvisioningRequestID string     `json:"provisioning_request_id,omitempty"`
	ClientToken           string     `json:"client_token"`
	LaunchNonce           string     `json:"launch_nonce"`
	GeneratorID           string     `json:"generator_id"`
	ProviderID            string     `json:"provider_id,omitempty"`
	State                 string     `json:"state"`
	ArchiveState          string     `json:"archive_state"`
	CleanupDeadline       *time.Time `json:"cleanup_deadline,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type CapacityInstanceIntentStore interface {
	CreateCapacityInstanceIntent(*CapacityInstanceIntent) error
	UpdateCapacityInstanceIntentIfUnchanged(*CapacityInstanceIntent, time.Time) (bool, error)
	GetCapacityInstanceIntent(id string) (*CapacityInstanceIntent, error)
	ListCapacityInstanceIntents(executionTargetID, provisioningRequestID string) ([]*CapacityInstanceIntent, error)
}
