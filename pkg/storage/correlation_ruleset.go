package storage

import "time"

// CorrelationRuleset is a project-scoped, reusable catalog of learned
// correlation rules — the dynamic values a script's recorded baseline reveals
// (discovered via pkg/convert/correlate) persisted so they can be applied to
// other scripts in the project. It is the LoadRunner "persistent correlation
// rules engine" analog. Rules are stored inline as JSON.
type CorrelationRuleset struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"project_id,omitempty"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Source      string            `json:"source,omitempty"`
	Rules       []CorrelationRule `json:"rules"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// CorrelationRule is one learned correlation. It mirrors the fields of
// correlate.Correlation / correlate.SuggestedExtractor but is a storage-local
// type so the storage layer stays decoupled from pkg/convert/correlate. The
// (Source, ExtractType, Expression) triple is exactly a dsl.Extractor, so a rule
// can be applied to a script as an extractor.
type CorrelationRule struct {
	Name        string `json:"name"`
	RuleID      string `json:"rule_id,omitempty"`
	Source      string `json:"source"`       // response_body | response_headers | ...
	ExtractType string `json:"extract_type"` // jsonpath | boundary | regex | ...
	Expression  string `json:"expression"`
	// SampleValue is retained only as an in-memory compatibility field for
	// callers that still construct the legacy shape. Learned values can contain
	// live credentials and must never cross the storage or API serialization
	// boundary.
	SampleValue string  `json:"-"`
	Confidence  float64 `json:"confidence,omitempty"`
}
