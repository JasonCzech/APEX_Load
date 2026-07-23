package storage

import "time"

// Power BI publish lifecycle states. A publish moves pending → complete, or
// lands in partial/failed when row pushes stop early; partial/failed rows are
// superseded by a fresh force re-publish rather than resumed, because Power BI
// push datasets cannot delete individual rows.
const (
	PowerBIPublishStatusPending  = "pending"
	PowerBIPublishStatusPartial  = "partial"
	PowerBIPublishStatusComplete = "complete"
	PowerBIPublishStatusFailed   = "failed"
)

// PowerBIPublish records one attempt to publish a run's results to a Power BI
// push dataset. It is the dedup anchor for re-publish handling and the audit
// trail surfaced on the run report page.
type PowerBIPublish struct {
	ID                    string     `json:"id"`
	TestID                string     `json:"test_id"`
	ProjectID             string     `json:"project_id"`
	WorkspaceID           string     `json:"workspace_id"`
	DatasetID             string     `json:"dataset_id,omitempty"`
	DatasetName           string     `json:"dataset_name,omitempty"`
	SchemaVersion         int        `json:"schema_version"`
	Status                string     `json:"status"`
	RunRowsPushed         int        `json:"run_rows_pushed"`
	TransactionRowsPushed int        `json:"transaction_rows_pushed"`
	TransactionRowsTotal  int        `json:"transaction_rows_total"`
	Error                 string     `json:"error,omitempty"`
	RequestedBy           string     `json:"requested_by,omitempty"`
	PublishedAt           *time.Time `json:"published_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}
