// Package livetable implements bounded, run-scoped shared data state machines.
// Authentication, execution-term fencing, encryption, and transport belong to
// callers. Durable Committers must fence and persist an Entry atomically.
package livetable

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type Profile string

// DefaultMessageBytes leaves room for the enclosing RPC frame and identity.
const DefaultMessageBytes = (1 << 20) - 8192

const (
	ProfileNative Profile = "native_queue"
	ProfileVTS    Profile = "vts_columns"
)

type Operation string

const (
	OpRead         Operation = "read"
	OpReadColumn   Operation = "read_column"
	OpNext         Operation = "next"
	OpPush         Operation = "push"
	OpPop          Operation = "pop"
	OpLease        Operation = "lease"
	OpRenew        Operation = "renew"
	OpAck          Operation = "ack"
	OpRelease      Operation = "release"
	OpUpdate       Operation = "update"
	OpIncrement    Operation = "increment"
	OpClear        Operation = "clear"
	OpRotate       Operation = "rotate"
	OpCreateColumn Operation = "create_column"
)

type Status string

const (
	StatusOK              Status = "ok"
	StatusEmpty           Status = "empty"
	StatusConditionFailed Status = "condition_failed"
)

// Missing map keys represent absent cells; a present key with value "" is an
// empty cell. Neither JSON nor the recovery format collapses this distinction.
type Row struct {
	ID      string            `json:"id,omitempty"`
	Version uint64            `json:"version"`
	Values  map[string]string `json:"values"`
	// Inspection metadata; executable queue records keep state in the lease
	// state machine. Export consumers must not make quarantined work available.
	State            string `json:"state,omitempty"`
	DeliveryAttempts uint32 `json:"delivery_attempts,omitempty"`
}

type Cell struct {
	Present bool   `json:"present"`
	Value   string `json:"value,omitempty"`
}

type Lease struct {
	Token           string    `json:"token"`
	RecordID        string    `json:"record_id"`
	ClientID        string    `json:"client_id"`
	ExpiresAt       time.Time `json:"expires_at"`
	MaxExpiresAt    time.Time `json:"max_expires_at"`
	DeliveryAttempt uint32    `json:"delivery_attempt"`
}

type Request struct {
	ClientID        string            `json:"client_id,omitempty"`
	OwnerID         string            `json:"owner_id,omitempty"`
	Sequence        uint64            `json:"sequence,omitempty"`
	Operation       Operation         `json:"operation"`
	RecordID        string            `json:"record_id,omitempty"`
	Position        int               `json:"position,omitempty"`
	Columns         []string          `json:"columns,omitempty"`
	Values          map[string]string `json:"values,omitempty"`
	Expected        map[string]string `json:"expected,omitempty"`
	ExpectedVersion *uint64           `json:"expected_version,omitempty"`
	Delta           int64             `json:"delta,omitempty"`
	LeaseToken      string            `json:"lease_token,omitempty"`
	LeaseDuration   time.Duration     `json:"lease_duration,omitempty"`
	Wait            time.Duration     `json:"wait,omitempty"`
	Limit           int               `json:"limit,omitempty"`
	Cursor          string            `json:"cursor,omitempty"`
}

type Result struct {
	Status   Status   `json:"status"`
	Revision uint64   `json:"revision"`
	Row      *Row     `json:"row,omitempty"`
	Rows     []Row    `json:"rows,omitempty"`
	Cells    []Cell   `json:"cells,omitempty"`
	Columns  []string `json:"columns,omitempty"`
	Lease    *Lease   `json:"lease,omitempty"`
	Value    string   `json:"value,omitempty"`
	Count    int64    `json:"count,omitempty"`
}

type Code string

const (
	CodeInvalid     Code = "invalid"
	CodeClosed      Code = "closed"
	CodeUnavailable Code = "unavailable"
	CodeCapacity    Code = "capacity"
	CodeConflict    Code = "conflict"
	CodeExpired     Code = "expired"
	CodeSequence    Code = "sequence"
	CodeLease       Code = "lease"
	CodeCanceled    Code = "canceled"
	CodeDeadline    Code = "deadline"
)

type Error struct {
	Code           Code   `json:"code"`
	Message        string `json:"message"`
	OutcomeUnknown bool   `json:"outcome_unknown,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if errors.Is(err, context.Canceled) {
		return CodeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeDeadline
	}
	return ""
}

type Client interface {
	Execute(context.Context, string, Request) (Result, error)
}

// Limits cover data and retained outcomes separately. Zero fields use safe
// defaults. Clients must serialize mutations per ClientID/table; independent
// renewal lanes use a separate ClientID and the same authorized OwnerID.
type Limits struct {
	MaxBytes             int64         `json:"max_bytes"`
	MaxRows              int           `json:"max_rows"`
	MaxFields            int           `json:"max_fields"`
	MaxColumns           int           `json:"max_columns"`
	MaxFieldBytes        int           `json:"max_field_bytes"`
	MaxRequestBytes      int           `json:"max_request_bytes"`
	MaxResultBytes       int           `json:"max_result_bytes"`
	MaxReceipts          int           `json:"max_receipts"`
	MaxReceiptBytes      int64         `json:"max_receipt_bytes"`
	MaxClients           int           `json:"max_clients"`
	MaxWaiters           int           `json:"max_waiters"`
	MaxScanCells         int           `json:"max_scan_cells"`
	MaxSnapshotBytes     int           `json:"max_snapshot_bytes"`
	MaxEntryBytes        int           `json:"max_entry_bytes"`
	ReceiptRetention     time.Duration `json:"receipt_retention"`
	MaxWait              time.Duration `json:"max_wait"`
	DefaultLeaseDuration time.Duration `json:"default_lease_duration"`
	MaxLeaseDuration     time.Duration `json:"max_lease_duration"`
	MaxLeaseLifetime     time.Duration `json:"max_lease_lifetime"`
	MaxDeliveryAttempts  uint32        `json:"max_delivery_attempts"`
}

type Config struct {
	ID        string
	RunID     string
	Profile   Profile
	Columns   []string
	Rows      []Row
	Limits    Limits
	Committer Committer
	Now       func() time.Time
}

// Commit must atomically verify the current execution owner/term, CAS
// PreviousRevision, and persist Payload. It must return only after durable
// commit. A repeated identical revision is idempotent; mismatched data is an
// error. Any failure closes serving until authoritative recovery, since a
// network error cannot establish whether the database committed.
type Committer interface {
	Commit(context.Context, Entry) error
}

type CommitFunc func(context.Context, Entry) error

func (f CommitFunc) Commit(ctx context.Context, e Entry) error { return f(ctx, e) }

type Entry struct {
	TableID          string          `json:"table_id"`
	Revision         uint64          `json:"revision"`
	PreviousRevision uint64          `json:"previous_revision"`
	At               time.Time       `json:"at"`
	Payload          json.RawMessage `json:"payload"`
}

type Snapshot struct {
	Observations  Observations    `json:"observations,omitempty"`
	FormatVersion int             `json:"format_version"`
	TableID       string          `json:"table_id"`
	RunID         string          `json:"run_id"`
	Revision      uint64          `json:"revision"`
	Payload       json.RawMessage `json:"payload"`
}

type Counts struct {
	Seeded           uint64 `json:"seeded"`
	Pushed           uint64 `json:"pushed"`
	Available        uint64 `json:"available"`
	Leased           uint64 `json:"leased"`
	Acknowledged     uint64 `json:"acknowledged"`
	Popped           uint64 `json:"popped"`
	Quarantined      uint64 `json:"quarantined"`
	Discarded        uint64 `json:"discarded"`
	DeliveryAttempts uint64 `json:"delivery_attempts"`
	Redeliveries     uint64 `json:"redeliveries"`
}

type Stats struct {
	Observations Observations `json:"observations"`
	TableID      string       `json:"table_id"`
	Profile      Profile      `json:"profile"`
	Revision     uint64       `json:"revision"`
	DataBytes    int64        `json:"data_bytes"`
	ReceiptBytes int64        `json:"receipt_bytes"`
	Receipts     int          `json:"receipts"`
	Clients      int          `json:"clients"`
	Waiters      int          `json:"waiters"`
	Closed       bool         `json:"closed"`
	Unavailable  bool         `json:"unavailable"`
	Counts       Counts       `json:"counts"`
	// Local serving telemetry, separate from target traffic. It restarts after
	// recovery and is intentionally not replayed as target execution evidence.
	OpCount          uint64 `json:"op_count"`
	OpErrors         uint64 `json:"op_errors"`
	OpUnknown        uint64 `json:"op_unknown"`
	OpLatencyTotalNS uint64 `json:"op_latency_total_ns"`
	OpLatencyMaxNS   uint64 `json:"op_latency_max_ns"`
	// Inclusive upper bounds: 1, 5, 10, 50, 100, 1000, 10000 ms, then +Inf.
	OpLatencyBuckets [8]uint64 `json:"op_latency_buckets"`
}
