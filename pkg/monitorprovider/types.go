package monitorprovider

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ScalarValue is an explicitly typed, bounded projection. Provider records never
// carry arbitrary nested source documents into durable evidence.
type ScalarValue struct {
	Kind        string   `json:"kind"`
	StringValue string   `json:"string_value,omitempty"`
	NumberValue *float64 `json:"number_value,omitempty"`
	BoolValue   *bool    `json:"bool_value,omitempty"`
}

func (v ScalarValue) Validate() error {
	switch v.Kind {
	case "string":
		if len(v.StringValue) > 4096 || v.NumberValue != nil || v.BoolValue != nil {
			return errors.New("invalid projected string")
		}
	case "integer":
		if _, err := strconv.ParseInt(v.StringValue, 10, 64); err != nil || v.NumberValue != nil || v.BoolValue != nil {
			return errors.New("invalid projected integer")
		}
	case "number":
		if v.NumberValue == nil || math.IsNaN(*v.NumberValue) || math.IsInf(*v.NumberValue, 0) || v.StringValue != "" || v.BoolValue != nil {
			return errors.New("invalid projected number")
		}
	case "boolean":
		if v.BoolValue == nil || v.StringValue != "" || v.NumberValue != nil {
			return errors.New("invalid projected boolean")
		}
	case "null":
		if v.StringValue != "" || v.NumberValue != nil || v.BoolValue != nil {
			return errors.New("invalid projected null")
		}
	default:
		return errors.New("unsupported projected scalar")
	}
	return nil
}

type ProviderLog struct {
	Message    string                 `json:"message"`
	Level      string                 `json:"level,omitempty"`
	Attributes map[string]ScalarValue `json:"attributes,omitempty"`
}
type ProviderSpan struct {
	TraceID      string                 `json:"trace_id"`
	SpanID       string                 `json:"span_id"`
	ParentSpanID string                 `json:"parent_span_id,omitempty"`
	Operation    string                 `json:"operation,omitempty"`
	DurationMS   float64                `json:"duration_ms"`
	Attributes   map[string]ScalarValue `json:"attributes,omitempty"`
}
type ProviderTransaction struct {
	Name       string                 `json:"name"`
	DurationMS float64                `json:"duration_ms"`
	Attributes map[string]ScalarValue `json:"attributes,omitempty"`
}

// ProviderEvidence retains the original AppDynamics diagnostic wire fields.
type ProviderEvidence struct {
	ID            string               `json:"id"`
	Kind          string               `json:"kind"`
	ApplicationID string               `json:"application_id"`
	Severity      string               `json:"severity,omitempty"`
	Summary       string               `json:"summary,omitempty"`
	Status        string               `json:"status,omitempty"`
	DeepLink      string               `json:"deep_link,omitempty"`
	IngestedAt    *time.Time           `json:"ingested_at,omitempty"`
	ReceivedAt    time.Time            `json:"received_at,omitzero"`
	TimestampUTC  time.Time            `json:"timestamp_utc"`
	EntityIDs     map[string]string    `json:"entity_ids,omitempty"`
	Properties    map[string]string    `json:"properties,omitempty"`
	Provider      string               `json:"provider,omitempty"`
	Backend       string               `json:"backend,omitempty"`
	QueryID       string               `json:"query_id,omitempty"`
	Index         string               `json:"index,omitempty"`
	DocumentID    string               `json:"document_id,omitempty"`
	Service       string               `json:"service,omitempty"`
	Correlation   string               `json:"correlation,omitempty"`
	Log           *ProviderLog         `json:"log,omitempty"`
	Span          *ProviderSpan        `json:"span,omitempty"`
	Transaction   *ProviderTransaction `json:"transaction,omitempty"`
}

func (e ProviderEvidence) Validate() error {
	if e.ID == "" || len(e.ID) > 512 || e.TimestampUTC.IsZero() || e.TimestampUTC.Location() != time.UTC || e.TimestampUTC.Year() < 1 || e.TimestampUTC.Year() > 9999 || len(e.Summary) > 4096 || len(e.Properties) > 16 || len(e.EntityIDs) > 16 {
		return errors.New("invalid provider evidence")
	}
	for _, at := range []*time.Time{e.IngestedAt, &e.ReceivedAt} {
		if at != nil && !at.IsZero() && (at.Location() != time.UTC || at.Year() < 1 || at.Year() > 9999) {
			return errors.New("invalid evidence ingestion/observation time")
		}
	}
	for _, v := range []string{e.QueryID, e.Index, e.DocumentID, e.Service, e.ApplicationID, e.Status, e.Severity} {
		if len(v) > 512 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid evidence identity")
		}
	}
	for k, v := range e.Properties {
		if k == "" || len(k) > 128 || len(v) > 4096 {
			return errors.New("invalid evidence property")
		}
	}
	for k, v := range e.EntityIDs {
		if k == "" || len(k) > 128 || len(v) > 512 {
			return errors.New("invalid evidence entity")
		}
	}
	if e.Provider == "" || e.Provider == ProviderAppDynamics || e.Provider == ProviderDynatrace || e.Provider == ProviderPrometheus {
		return nil
	}
	if e.Provider != ProviderElasticsearch && e.Provider != ProviderOpenSearch {
		return errors.New("unsupported evidence provider")
	}
	n := 0
	var attrs map[string]ScalarValue
	if e.Log != nil {
		n++
		attrs = e.Log.Attributes
		if e.Kind != "log" && e.Kind != "error" && e.Kind != "health" || len(e.Log.Message) > 4096 || len(e.Log.Level) > 128 {
			return errors.New("invalid projected log")
		}
	}
	if e.Span != nil {
		n++
		attrs = e.Span.Attributes
		if e.Kind != "span" && e.Kind != "error" || e.Span.TraceID == "" || e.Span.SpanID == "" || len(e.Span.TraceID) > 128 || len(e.Span.SpanID) > 128 || len(e.Span.ParentSpanID) > 128 || len(e.Span.Operation) > 256 || math.IsNaN(e.Span.DurationMS) || math.IsInf(e.Span.DurationMS, 0) || e.Span.DurationMS < 0 {
			return errors.New("invalid projected span")
		}
	}
	if e.Transaction != nil {
		n++
		attrs = e.Transaction.Attributes
		if e.Kind != "transaction" || e.Transaction.Name == "" || len(e.Transaction.Name) > 256 || math.IsNaN(e.Transaction.DurationMS) || math.IsInf(e.Transaction.DurationMS, 0) || e.Transaction.DurationMS < 0 {
			return errors.New("invalid projected transaction")
		}
	}
	if n != 1 || len(attrs) > 16 {
		return errors.New("provider evidence requires one bounded typed record")
	}
	for k, v := range attrs {
		if k == "" || len(k) > 128 || v.Validate() != nil {
			return errors.New("invalid projected attribute")
		}
	}
	switch e.Correlation {
	case "", "run_id", "trace_id", "service_time", "unmatched":
	default:
		return errors.New("unsupported evidence correlation")
	}
	return nil
}

type ProviderCoverage struct {
	ExpectedPoints     int        `json:"expected_points,omitempty"`
	MissingPoints      int        `json:"missing_points,omitempty"`
	Warnings           []string   `json:"warnings,omitempty"`
	Infos              []string   `json:"infos,omitempty"`
	Cached             bool       `json:"cached,omitempty"`
	CacheAgeSeconds    int        `json:"cache_age_seconds,omitempty"`
	ObservedAt         *time.Time `json:"observed_at,omitempty"`
	Freshness          string     `json:"freshness,omitempty"`
	Status             string     `json:"status"`
	From               time.Time  `json:"from"`
	To                 time.Time  `json:"to"`
	Points             int        `json:"points"`
	Evidence           int        `json:"evidence"`
	Truncated          bool       `json:"truncated"`
	Message            string     `json:"message,omitempty"`
	QueryID            string     `json:"query_id,omitempty"`
	Reason             string     `json:"reason,omitempty"`
	AsOf               *time.Time `json:"as_of,omitempty"`
	LateArrivalSeconds int        `json:"late_arrival_seconds,omitempty"`
}
type ProviderContinuation struct {
	ResolvedIndices []string               `json:"resolved_indices,omitempty"`
	QueryID         string                 `json:"query_id"`
	AsyncID         string                 `json:"async_id,omitempty"`
	KeepAliveUntil  time.Time              `json:"keep_alive_until,omitempty"`
	PITID           string                 `json:"pit_id,omitempty"`
	SearchAfter     []ScalarValue          `json:"search_after,omitempty"`
	CompositeAfter  map[string]ScalarValue `json:"composite_after,omitempty"`
	WindowFrom      time.Time              `json:"window_from"`
	WindowUntil     time.Time              `json:"window_until"`
}

func (c ProviderContinuation) Validate() error {
	if c.QueryID == "" || len(c.QueryID) > 128 || len(c.PITID) > 16384 || len(c.AsyncID) > 16384 || len(c.SearchAfter) > 16 || len(c.CompositeAfter) > 16 || c.WindowFrom.IsZero() || c.WindowFrom.Location() != time.UTC || c.WindowUntil.Location() != time.UTC || !c.WindowUntil.After(c.WindowFrom) {
		return errors.New("invalid provider continuation")
	}
	if c.PITID != "" && c.AsyncID != "" || strings.ContainsAny(c.QueryID+c.PITID+c.AsyncID, "\r\n\x00") || !c.KeepAliveUntil.IsZero() && (c.KeepAliveUntil.Location() != time.UTC || c.KeepAliveUntil.Year() < 1 || c.KeepAliveUntil.Year() > 9999) {
		return errors.New("invalid provider owned handle")
	}
	if err := ValidateResolvedIndices(c.ResolvedIndices); err != nil {
		return err
	}
	for _, v := range c.SearchAfter {
		if v.Validate() != nil {
			return errors.New("invalid search cursor")
		}
	}
	for k, v := range c.CompositeAfter {
		if k == "" || len(k) > 128 || v.Validate() != nil {
			return errors.New("invalid aggregation cursor")
		}
	}
	return nil
}

// ValidateResolvedIndices bounds the concrete document scope held privately by
// a fixed query window. Data-stream backing indices are eligible; unrelated
// hidden/system indices cannot enter this release's document capture scope.
func ValidateResolvedIndices(indices []string) error {
	if len(indices) > 200 {
		return errors.New("too many resolved concrete indices")
	}
	seen := make(map[string]bool, len(indices))
	for _, index := range indices {
		if index == "" || len(index) > 255 || index == "." || index == ".." || index == "_all" || strings.HasPrefix(index, ".") && !strings.HasPrefix(index, ".ds-") || strings.ContainsAny(index, `/\\,:*?[]{} `) || strings.IndexFunc(index, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || seen[index] {
			return errors.New("invalid resolved concrete index")
		}
		seen[index] = true
	}
	return nil
}

// NextTime is the first uncommitted evaluation timestamp for NextQuery.
// Provider-owned continuation state advances only with the accepted batch.
type ProviderProgress struct {
	NextTime             *time.Time               `json:"next_time,omitempty"`
	Cursor               string                   `json:"cursor,omitempty"`
	CursorExpiresAt      *time.Time               `json:"cursor_expires_at,omitempty"`
	ScannedBytes         int64                    `json:"scanned_bytes,omitempty"`
	ScanReservationBytes int64                    `json:"scan_reservation_bytes,omitempty"`
	ScanReservationIDs   []string                 `json:"scan_reservation_ids,omitempty"`
	ScanSettlements      []ProviderScanSettlement `json:"scan_settlements,omitempty"`
	QueryCostKnown       bool                     `json:"query_cost_known,omitempty"`
	NextQuery            int                      `json:"next_query"`
	TotalQueries         int                      `json:"total_queries"`
	Complete             bool                     `json:"complete"`
	Continuation         *ProviderContinuation    `json:"continuation,omitempty"`
}

// ProviderEntity preserves the common topology wire contract.
type ProviderEntity struct {
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	ParentID  string            `json:"parent_id,omitempty"`
	Path      string            `json:"path,omitempty"`
	EntityIDs map[string]string `json:"entity_ids,omitempty"`
}

// Aliases retain existing provider names on the additive canonical contracts.
type ProviderDiagnostic = ProviderEvidence
type ProviderQueryProgress = ProviderProgress

// ProviderEvaluationWindow binds a terminal query to the immutable rule interval.
type ProviderEvaluationWindow struct {
	QueryID string    `json:"query_id"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
}

// ProviderScanSettlement reports the cost of one admitted upstream attempt.
type ProviderScanSettlement struct {
	AttemptID    string `json:"attempt_id"`
	ScannedBytes int64  `json:"scanned_bytes"`
}

// Compatibility names used by existing governed providers.
type Entity = ProviderEntity
type Diagnostic = ProviderEvidence
type Coverage = ProviderCoverage
type QueryProgress = ProviderProgress
type EvaluationWindow = ProviderEvaluationWindow
type ScanSettlement = ProviderScanSettlement
