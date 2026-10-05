// Package monitorprovider contains disclosure-safe contracts shared by monitoring providers.
package monitorprovider

import "time"

type Entity struct {
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	ParentID  string            `json:"parent_id,omitempty"`
	Path      string            `json:"path,omitempty"`
	EntityIDs map[string]string `json:"entity_ids,omitempty"`
}
type Diagnostic struct {
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
type Coverage struct {
	Status          string     `json:"status"`
	From            time.Time  `json:"from"`
	To              time.Time  `json:"to"`
	Points          int        `json:"points"`
	Evidence        int        `json:"evidence"`
	Truncated       bool       `json:"truncated"`
	Message         string     `json:"message,omitempty"`
	ExpectedPoints  int        `json:"expected_points,omitempty"`
	MissingPoints   int        `json:"missing_points,omitempty"`
	Warnings        []string   `json:"warnings,omitempty"`
	Infos           []string   `json:"infos,omitempty"`
	Cached          bool       `json:"cached,omitempty"`
	CacheAgeSeconds int        `json:"cache_age_seconds,omitempty"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
	Freshness       string     `json:"freshness,omitempty"`
}

// NextTime is the first uncommitted evaluation timestamp for NextQuery. It is
// carried with the accepted batch, so a retry cannot advance the durable cursor.
type QueryProgress struct {
	NextQuery            int              `json:"next_query"`
	TotalQueries         int              `json:"total_queries"`
	Complete             bool             `json:"complete"`
	NextTime             *time.Time       `json:"next_time,omitempty"`
	Cursor               string           `json:"cursor,omitempty"`
	CursorExpiresAt      *time.Time       `json:"cursor_expires_at,omitempty"`
	ScannedBytes         int64            `json:"scanned_bytes,omitempty"`
	ScanReservationBytes int64            `json:"scan_reservation_bytes,omitempty"`
	ScanReservationIDs   []string         `json:"scan_reservation_ids,omitempty"`
	ScanSettlements      []ScanSettlement `json:"scan_settlements,omitempty"`
	QueryCostKnown       bool             `json:"query_cost_known,omitempty"`
}

// EvaluationWindow binds a terminal query to the immutable test/rule interval.
type EvaluationWindow struct {
	QueryID string    `json:"query_id"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
}

// ScanSettlement reports the cost of one admitted upstream attempt.
type ScanSettlement struct {
	AttemptID    string `json:"attempt_id"`
	ScannedBytes int64  `json:"scanned_bytes"`
}
