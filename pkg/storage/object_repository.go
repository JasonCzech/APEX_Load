package storage

import "time"

// ObjectRepository is a project-scoped, reusable catalog of named UI element
// descriptors (the LoadRunner/TruClient object-repository analog). Scripts
// reference objects by logical name so selectors are maintained centrally and
// shared/healed across scripts. Objects are stored inline as JSON.
type ObjectRepository struct {
	ID          string             `json:"id"`
	ProjectID   string             `json:"project_id,omitempty"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Source      string             `json:"source,omitempty"`
	Objects     []RepositoryObject `json:"objects"`
	// AutoPromoteOnHeal makes replay self-heal promote a healed locator to the
	// object's primary (instead of the default conservative append), so the
	// working selector is used on the next run without manual intervention.
	AutoPromoteOnHeal bool      `json:"auto_promote_on_heal,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// RepositoryObject is one named element in a repository. It mirrors the fields
// of convert/trueclient.ObjectDescriptor and selector candidates, but is a
// storage-local type so the storage layer stays decoupled from those packages.
type RepositoryObject struct {
	LogicalName    string                      `json:"logical_name"`
	Description    string                      `json:"description,omitempty"`
	CSS            string                      `json:"css,omitempty"`
	XPath          string                      `json:"xpath,omitempty"`
	Role           string                      `json:"role,omitempty"`
	Text           string                      `json:"text,omitempty"`
	Attributes     map[string]string           `json:"attributes,omitempty"`
	Ordinal        string                      `json:"ordinal,omitempty"`
	Regex          string                      `json:"regex,omitempty"`
	FrameContext   string                      `json:"frame_context,omitempty"`
	WindowContext  string                      `json:"window_context,omitempty"`
	Candidates     []RepositoryObjectCandidate `json:"candidates,omitempty"`
	BestConfidence int                         `json:"best_confidence,omitempty"`
	SourceScriptID string                      `json:"source_script_id,omitempty"`
}

// RepositoryObjectCandidate mirrors selector.Candidate (a ranked locator).
type RepositoryObjectCandidate struct {
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	Confidence int    `json:"confidence"`
	Reason     string `json:"reason,omitempty"`
}
