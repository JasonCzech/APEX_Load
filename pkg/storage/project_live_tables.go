package storage

import (
	"context"
	"time"
)

// ProjectLiveTableConfig describes a persistent instance. Per-run bindings may
// choose how to wait for data, but cannot replace this schema or durability.
type ProjectLiveTableConfig struct {
	Profile             string   `json:"profile"`
	Columns             []string `json:"columns"`
	MaxRows             int      `json:"max_rows"`
	MaxBytes            int64    `json:"max_bytes"`
	MaxResultBytes      int      `json:"max_result_bytes"`
	MaxReceipts         int      `json:"max_receipts"`
	RetryWindowMS       int64    `json:"retry_window_ms"`
	LeaseTTLMS          int64    `json:"lease_ttl_ms"`
	MaxLeaseLifetimeMS  int64    `json:"max_lease_lifetime_ms"`
	MaxDeliveryAttempts int      `json:"max_delivery_attempts"`
}

type ProjectLiveTableDefinition struct {
	ID                string                 `json:"id"`
	ProjectID         string                 `json:"project_id"`
	Name              string                 `json:"name"`
	Revision          uint64                 `json:"config_revision,string"`
	State             string                 `json:"state"`
	CurrentInstanceID string                 `json:"instance_id"`
	Config            ProjectLiveTableConfig `json:"config"`
	RetentionDays     int                    `json:"retention_days"`
	CreatedBySubject  string                 `json:"created_by_subject,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

type ProjectLiveTableInstance struct {
	ID                 string     `json:"id"`
	TableID            string     `json:"table_id"`
	ProjectID          string     `json:"project_id"`
	State              string     `json:"state"`
	SeedDigest         string     `json:"seed_digest"`
	SeedMetadata       string     `json:"-"`
	OwnerLeaseName     string     `json:"-"`
	OwnerID            string     `json:"-"`
	OwnerGeneration    uint64     `json:"-"`
	Revision           uint64     `json:"revision,string"`
	CheckpointRevision uint64     `json:"checkpoint_revision,string"`
	LeaseCount         uint64     `json:"lease_count"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	RetireAfter        *time.Time `json:"retire_after,omitempty"`
}

// Detached attachments remain as bounded cleanup tombstones after run deletion.
// Their IDs are never reused, so removing their core identities cannot authorize
// a stale generator request to execute again.
type ProjectLiveTableAttachment struct {
	ID                string     `json:"id"`
	Incarnation       string     `json:"-"`
	ProjectID         string     `json:"project_id"`
	TableID           string     `json:"table_id"`
	InstanceID        string     `json:"instance_id"`
	TestID            string     `json:"test_id"`
	RunIncarnation    string     `json:"-"`
	Group             string     `json:"group"`
	Alias             string     `json:"alias"`
	AllowedOperations []string   `json:"allowed_operations"`
	Principal         string     `json:"principal"`
	State             string     `json:"state"`
	CreatedAt         time.Time  `json:"created_at"`
	DetachedAt        *time.Time `json:"detached_at,omitempty"`
	RetireAfter       *time.Time `json:"retire_after,omitempty"`
	SettledRevision   *uint64    `json:"settled_revision,omitempty,string"`
}

type ProjectLiveTableCreate struct {
	Definition ProjectLiveTableDefinition
	Instance   ProjectLiveTableInstance
	Checkpoint []byte
	Audit      *AuditEvent
}

type ProjectLiveTableAttachmentCreate struct {
	Attachment                 ProjectLiveTableAttachment
	ExpectedDefinitionRevision uint64
}

type ProjectLiveTableLifecycle struct {
	ProjectID                  string
	TableID                    string
	ExpectedDefinitionRevision uint64
	ExpectedInstanceID         string
	ExpectedRevision           uint64
	State                      string
	Reason                     string
	Audit                      *AuditEvent
}

type ProjectLiveTableReset struct {
	ProjectLiveTableLifecycle
	Instance   ProjectLiveTableInstance
	Checkpoint []byte
}

type ProjectLiveTableEntry struct {
	LiveTableEntry
	// TableID in the embedded entry is the immutable instance ID.
	AttachmentID string
	LeasedCount  uint64
}

type ProjectLiveTableRecovery struct {
	Definition ProjectLiveTableDefinition
	Instance   ProjectLiveTableInstance
	Checkpoint []byte
	Entries    []LiveTableEntry
}

type ProjectLiveTableStore interface {
	CreateProjectLiveTable(context.Context, ProjectLiveTableCreate, LiveTableExecutionTerm) (*ProjectLiveTableDefinition, error)
	GetProjectLiveTable(context.Context, string, string) (*ProjectLiveTableDefinition, error)
	ListProjectLiveTables(context.Context, string) ([]ProjectLiveTableDefinition, error)
	GetProjectLiveTableInstance(context.Context, string, string) (*ProjectLiveTableInstance, error)
	ClaimProjectLiveTableInstance(context.Context, string, string, LiveTableExecutionTerm) error
	AppendProjectLiveTableEntry(context.Context, ProjectLiveTableEntry, LiveTableExecutionTerm) error
	PublishProjectLiveTableCheckpoint(context.Context, string, uint64, []byte, LiveTableExecutionTerm) error
	ReadProjectLiveTableRecovery(context.Context, string, string) (*ProjectLiveTableRecovery, error)
	CreateProjectLiveTableAttachments(context.Context, []ProjectLiveTableAttachmentCreate, LiveTableExecutionTerm) ([]ProjectLiveTableAttachment, error)
	ListProjectLiveTableAttachments(context.Context, string, string) ([]ProjectLiveTableAttachment, error)
	ListProjectLiveTableRunAttachments(context.Context, string, string) ([]ProjectLiveTableAttachment, error)
	DetachProjectLiveTableAttachments(context.Context, string, string, time.Time, LiveTableExecutionTerm) error
	MarkProjectLiveTableAttachmentSettled(context.Context, string, uint64, LiveTableExecutionTerm) error
	RetireProjectLiveTableAttachment(context.Context, string, uint64, LiveTableExecutionTerm) error
	TransitionProjectLiveTable(context.Context, ProjectLiveTableLifecycle, LiveTableExecutionTerm) (*ProjectLiveTableDefinition, error)
	ReplaceProjectLiveTableInstance(context.Context, ProjectLiveTableReset, LiveTableExecutionTerm) (*ProjectLiveTableDefinition, error)
}

func projectLiveTableSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS project_live_tables (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			revision BIGINT NOT NULL,
			state TEXT NOT NULL,
			current_instance_id TEXT NOT NULL,
			config_json TEXT NOT NULL,
			retention_days INTEGER NOT NULL,
			created_by_subject TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(project_id) REFERENCES projects(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_project_live_tables_project ON project_live_tables(project_id,state,id)`,
		`CREATE TABLE IF NOT EXISTS project_live_table_instances (
			id TEXT PRIMARY KEY,
			table_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			state TEXT NOT NULL,
			seed_digest TEXT NOT NULL,
			seed_metadata TEXT NOT NULL,
			owner_lease_name TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			owner_generation BIGINT NOT NULL,
			revision BIGINT NOT NULL,
			checkpoint_revision BIGINT NOT NULL,
			lease_count BIGINT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			retire_after DATETIME,
			initial_checkpoint_hash TEXT NOT NULL,
			checkpoint_payload TEXT NOT NULL,
			checkpoint_hash TEXT NOT NULL,
			journal_bytes BIGINT NOT NULL,
			journal_count BIGINT NOT NULL,
			FOREIGN KEY(table_id) REFERENCES project_live_tables(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_project_live_table_instances_table ON project_live_table_instances(table_id,state,id)`,
		`CREATE TABLE IF NOT EXISTS project_live_table_journal (
			instance_id TEXT NOT NULL,
			revision BIGINT NOT NULL,
			previous_revision BIGINT NOT NULL,
			payload TEXT NOT NULL,
			payload_hash TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY(instance_id,revision),
			FOREIGN KEY(instance_id) REFERENCES project_live_table_instances(id)
		)`,
		`CREATE TABLE IF NOT EXISTS project_live_table_attachments (
			id TEXT PRIMARY KEY,
			incarnation TEXT NOT NULL,
			project_id TEXT NOT NULL,
			table_id TEXT NOT NULL,
			instance_id TEXT NOT NULL,
			test_id TEXT NOT NULL,
			run_incarnation TEXT NOT NULL,
			group_name TEXT NOT NULL,
			alias TEXT NOT NULL,
			allowed_operations_json TEXT NOT NULL,
			principal TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			detached_at DATETIME,
			retire_after DATETIME,
			settled_revision BIGINT,
			UNIQUE(test_id,run_incarnation,group_name,alias),
			FOREIGN KEY(instance_id) REFERENCES project_live_table_instances(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_project_live_table_attachments_run ON project_live_table_attachments(project_id,test_id,state)`,
		`CREATE INDEX IF NOT EXISTS idx_project_live_table_attachments_instance ON project_live_table_attachments(instance_id,state,retire_after)`,
		`CREATE TABLE IF NOT EXISTS project_live_table_operator_receipts (
			instance_id TEXT NOT NULL,
			actor_subject TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			digest TEXT NOT NULL,
			issued_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			client_scope TEXT NOT NULL,
			outcome BYTEA NOT NULL,
			PRIMARY KEY(instance_id,actor_subject,key_hash),
			FOREIGN KEY(instance_id) REFERENCES project_live_table_instances(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_project_live_table_operator_receipts_expiry ON project_live_table_operator_receipts(instance_id,expires_at)`,
	}
}
