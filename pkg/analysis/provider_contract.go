package analysis

import "time"

// ProviderEntity is a bounded provider identity and topology projection.
type ProviderEntity struct {
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	ParentID  string            `json:"parent_id,omitempty"`
	Path      string            `json:"path,omitempty"`
	EntityIDs map[string]string `json:"entity_ids,omitempty"`
}

// ProviderDiagnostic retains safe evidence only. ApplicationID is preserved
// for already accepted AppDynamics records; other providers identify entities
// through EntityIDs and their immutable source scope.
type ProviderDiagnostic struct {
	Provider      string            `json:"provider,omitempty"`
	Backend       string            `json:"backend,omitempty"`
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	ApplicationID string            `json:"application_id"`
	Severity      string            `json:"severity,omitempty"`
	Summary       string            `json:"summary,omitempty"`
	Status        string            `json:"status,omitempty"`
	DeepLink      string            `json:"deep_link,omitempty"`
	TimestampUTC  time.Time         `json:"timestamp_utc"`
	EntityIDs     map[string]string `json:"entity_ids,omitempty"`
	Properties    map[string]string `json:"properties,omitempty"`
}

type ProviderCoverage struct {
	Status    string    `json:"status"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Points    int       `json:"points"`
	Evidence  int       `json:"evidence"`
	Truncated bool      `json:"truncated"`
	Message   string    `json:"message,omitempty"`
}

// ProviderQueryProgress is committed atomically with its receipt. Cursor and
// expiry are private execution state; public projections must remove them.
// ScannedBytes is the newly reported upstream scan for this accepted pass.
type ProviderQueryProgress struct {
	NextQuery            int        `json:"next_query"`
	TotalQueries         int        `json:"total_queries"`
	Complete             bool       `json:"complete"`
	Cursor               string     `json:"cursor,omitempty"`
	CursorExpiresAt      *time.Time `json:"cursor_expires_at,omitempty"`
	ScannedBytes         int64      `json:"scanned_bytes,omitempty"`
	ScanReservationBytes int64      `json:"scan_reservation_bytes,omitempty"`
	ScanReservationIDs   []string   `json:"scan_reservation_ids,omitempty"`
	// ScanSettlements report the upstream cost of individual attempts that
	// finished in this pass. Each releases only its own reservation, so an
	// attempt still running later in the pass cannot strand finished ones.
	ScanSettlements []ProviderScanSettlement `json:"scan_settlements,omitempty"`
	// QueryCostKnown is a display flag: false while any attempt in the pass is
	// still running or finished without reporting its scanned bytes.
	QueryCostKnown bool `json:"query_cost_known,omitempty"`
}

// ProviderScanSettlement is the reported scanned bytes of one admitted attempt.
type ProviderScanSettlement struct {
	AttemptID    string `json:"attempt_id"`
	ScannedBytes int64  `json:"scanned_bytes"`
}
