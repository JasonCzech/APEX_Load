package storage

import "time"

// ReusableBlock is a user-saved authoring component: a named bundle of script
// fragments (config/data_sources/parameters/init/actions/teardown) captured from
// one script and re-insertable into another. The component payload is stored as
// opaque JSON (Component) so the storage layer stays decoupled from the API's
// AuthoringComponent shape; the API marshals/unmarshals it. Blocks are scoped to
// a project alongside the curated, built-in catalog.
type ReusableBlock struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Component string    `json:"-"` // serialized AuthoringComponent JSON
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
