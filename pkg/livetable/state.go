package livetable

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const formatVersion = 1

type record struct {
	ControlClients []string `json:"control_clients,omitempty"`
	Row            Row      `json:"row"`
	Lease          *Lease   `json:"lease,omitempty"`
	Attempts       uint32   `json:"attempts"`
	Quarantined    bool     `json:"quarantined,omitempty"`
}

type receipt struct {
	ClientID  string    `json:"client_id"`
	Sequence  uint64    `json:"sequence"`
	Digest    string    `json:"digest"`
	ExpiresAt time.Time `json:"expires_at"`
	Result    Result    `json:"result"`
	Error     *Error    `json:"error,omitempty"`
	Bytes     int64     `json:"bytes"`
}

type state struct {
	ID           string             `json:"id"`
	RunID        string             `json:"run_id,omitempty"`
	Scope        string             `json:"scope,omitempty"`
	ScopeID      string             `json:"scope_id,omitempty"`
	Profile      Profile            `json:"profile"`
	Limits       Limits             `json:"limits"`
	Revision     uint64             `json:"revision"`
	Columns      []string           `json:"columns"`
	Records      map[string]record  `json:"records"`
	Order        []string           `json:"order"`
	Data         map[string][]Cell  `json:"data"`
	Cursors      map[string]uint64  `json:"cursors"`
	Clients      map[string]uint64  `json:"clients"`
	Receipts     map[string]receipt `json:"receipts"`
	DataBytes    int64              `json:"data_bytes"`
	Fields       int                `json:"fields"`
	ReceiptBytes int64              `json:"receipt_bytes"`
	Counts       Counts             `json:"counts"`
	Closed       bool               `json:"closed"`
}

// A transition carries resolved choices, not commands to be reevaluated during
// recovery. Unchanged columns/records and retained outcomes are not recopied.
type transition struct {
	FormatVersion  int               `json:"format_version"`
	Upsert         []record          `json:"upsert,omitempty"`
	Delete         []string          `json:"delete,omitempty"`
	Append         []string          `json:"append,omitempty"`
	ColumnData     map[string][]Cell `json:"column_data,omitempty"`
	ColumnSplices  []columnSplice    `json:"column_splices,omitempty"`
	RecordPatches  []recordPatch     `json:"record_patches,omitempty"`
	Columns        []string          `json:"columns,omitempty"`
	Cursors        map[string]uint64 `json:"cursors,omitempty"`
	Receipt        *receipt          `json:"receipt,omitempty"`
	Retire         []string          `json:"retire,omitempty"`
	RetireScope    string            `json:"retire_scope,omitempty"`
	Counts         *Counts           `json:"counts,omitempty"`
	DataBytes      int64             `json:"data_bytes"`
	Fields         int               `json:"fields"`
	Close          bool              `json:"close,omitempty"`
	ReleaseOnClose bool              `json:"release_on_close,omitempty"`
	DiscardAll     bool              `json:"discard_all,omitempty"`
}

type pending struct {
	request  Request
	digest   string
	deadline time.Time
	done     chan struct{}
	result   Result
	err      error
}

type Table struct {
	mu                   sync.Mutex
	observationMu        sync.Mutex
	observations         Observations
	activeAttempts       atomic.Int64
	s                    state
	committer            Committer
	authorize            func(Request) error
	now                  func() time.Time
	unavailable          bool
	waiters              []*pending
	pending              map[string]*pending
	pendingClients       map[string]string
	leaseDeadlines       map[string]time.Time
	recoveryHoldUntil    time.Time
	recoveryHolds        map[string]string
	recoveryReceiptUntil time.Time
	recoveryReceipts     map[string]bool
	opCount              atomic.Uint64
	opErrors             atomic.Uint64
	opUnknown            atomic.Uint64
	opLatencyTotal       atomic.Uint64
	opLatencyMax         atomic.Uint64
	opLatencyBuckets     [8]atomic.Uint64
}

// Manager resolves opaque run-scoped table IDs. Callers must authorize access
// before calling it; possession of an ID is not an authorization capability.
type Manager struct {
	mu     sync.RWMutex
	tables map[string]*Table
}

func NewManager() *Manager { return &Manager{tables: make(map[string]*Table)} }

func (m *Manager) Create(c Config) (*Table, error) {
	t, err := New(c)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tables == nil {
		m.tables = make(map[string]*Table)
	}
	if _, ok := m.tables[c.ID]; ok {
		return nil, fail(CodeConflict, "table already exists")
	}
	m.tables[c.ID] = t
	return t, nil
}

func (m *Manager) Add(t *Table) error {
	if t == nil {
		return fail(CodeInvalid, "table is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tables == nil {
		m.tables = make(map[string]*Table)
	}
	if _, exists := m.tables[t.s.ID]; exists {
		return fail(CodeConflict, "table already exists")
	}
	m.tables[t.s.ID] = t
	return nil
}

func (m *Manager) Get(id string) (*Table, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tables[id]
	return t, ok
}
func (m *Manager) Execute(ctx context.Context, id string, r Request) (Result, error) {
	t, ok := m.Get(id)
	if !ok {
		return Result{}, fail(CodeClosed, "table is unavailable")
	}
	return t.Execute(ctx, r)
}

// Remove releases a terminal table; an active table must be closed first.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tables[id]
	if !ok {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.s.Closed {
		return fail(CodeConflict, "close table before removal")
	}
	delete(m.tables, id)
	return nil
}

func New(c Config) (*Table, error) {
	if !validName(c.ID) || !validScope(c.RunID, c.Scope, c.ScopeID) {
		return nil, fail(CodeInvalid, "table and scope identities are required and bounded")
	}
	if c.Profile == "" {
		c.Profile = ProfileNative
	}
	if c.Profile == "native" {
		c.Profile = ProfileNative
	}
	if c.Profile == "vts" {
		c.Profile = ProfileVTS
	}
	if c.Profile != ProfileNative && c.Profile != ProfileVTS {
		return nil, fail(CodeInvalid, "unknown semantic profile")
	}
	l, err := normalizeLimits(c.Limits)
	if err != nil {
		return nil, err
	}
	if len(c.Columns) == 0 || len(c.Columns) > l.MaxColumns {
		return nil, fail(CodeInvalid, "declared columns are required and bounded")
	}
	seen := map[string]bool{}
	for _, col := range c.Columns {
		if !validName(col) || seen[col] {
			return nil, fail(CodeInvalid, "columns must have unique bounded names")
		}
		seen[col] = true
	}
	t := &Table{s: state{ID: c.ID, RunID: c.RunID, Scope: c.Scope, ScopeID: c.ScopeID, Profile: c.Profile, Limits: l, Columns: append([]string(nil), c.Columns...), Records: map[string]record{}, Data: map[string][]Cell{}, Cursors: map[string]uint64{}, Clients: map[string]uint64{}, Receipts: map[string]receipt{}}, committer: c.Committer, authorize: c.Authorize, now: c.Now, pending: map[string]*pending{}, pendingClients: map[string]string{}, leaseDeadlines: map[string]time.Time{}}
	t.observations.Version = 1
	if t.now == nil {
		t.now = time.Now
	}
	if len(c.Rows) > l.MaxRows {
		return nil, fail(CodeCapacity, "seed row limit exceeded")
	}
	for _, col := range c.Columns {
		t.s.Data[col] = nil
	}
	for i, row := range c.Rows {
		if err := t.validateValues(row.Values); err != nil {
			return nil, err
		}
		row = copyRow(row)
		row.Version = 1
		row.State = ""
		row.DeliveryAttempts = 0
		if c.Profile == ProfileNative {
			if row.ID == "" {
				row.ID = fmt.Sprintf("seed:%d", i+1)
			}
			if !validName(row.ID) {
				return nil, fail(CodeInvalid, "invalid seed record identity")
			}
			if _, exists := t.s.Records[row.ID]; exists {
				return nil, fail(CodeConflict, "duplicate seed record identity")
			}
			t.s.Records[row.ID] = record{Row: row}
			t.s.Order = append(t.s.Order, row.ID)
		} else {
			for _, col := range t.s.Columns {
				value, present := row.Values[col]
				t.s.Data[col] = append(t.s.Data[col], Cell{Present: present, Value: value})
			}
		}
	}
	t.s.Counts = Counts{Seeded: uint64(len(c.Rows)), Available: uint64(len(c.Rows))}
	t.s.DataBytes, t.s.Fields = t.measureData(nil)
	if t.s.DataBytes > l.MaxBytes || t.s.Fields > l.MaxFields {
		return nil, fail(CodeCapacity, "seed data exceeds table budget")
	}
	encoded, err := json.Marshal(t.s)
	// Reserve bounded observation history and the outer recovery envelope even
	// for a seed that will never receive another mutation. Transition admission
	// already reserves 16 KiB for fixed checkpoint overhead.
	if err != nil || len(encoded)+8192 > l.MaxSnapshotBytes {
		return nil, fail(CodeCapacity, "seed snapshot exceeds recovery budget")
	}
	return t, nil
}

func normalizeLimits(l Limits) (Limits, error) {
	def := Limits{MaxBytes: 64 << 20, MaxRows: 100000, MaxFields: 200000, MaxColumns: 1024, MaxFieldBytes: 16 << 10, MaxRequestBytes: DefaultMessageBytes, MaxResultBytes: DefaultMessageBytes, MaxReceipts: 10000, MaxReceiptBytes: 16 << 20, MaxClients: 4096, MaxWaiters: 1024, MaxScanCells: 200000, MaxSnapshotBytes: 128 << 20, MaxEntryBytes: 2 << 20, ReceiptRetention: 2 * time.Minute, MaxWait: time.Minute, DefaultLeaseDuration: 30 * time.Second, MaxLeaseDuration: 5 * time.Minute, MaxLeaseLifetime: 30 * time.Minute, MaxDeliveryAttempts: 10}
	intFields := []struct {
		v *int
		d int
	}{{&l.MaxRows, def.MaxRows}, {&l.MaxFields, def.MaxFields}, {&l.MaxColumns, def.MaxColumns}, {&l.MaxFieldBytes, def.MaxFieldBytes}, {&l.MaxRequestBytes, def.MaxRequestBytes}, {&l.MaxResultBytes, def.MaxResultBytes}, {&l.MaxReceipts, def.MaxReceipts}, {&l.MaxClients, def.MaxClients}, {&l.MaxWaiters, def.MaxWaiters}, {&l.MaxScanCells, def.MaxScanCells}, {&l.MaxSnapshotBytes, def.MaxSnapshotBytes}}
	for _, f := range intFields {
		if *f.v < 0 {
			return l, fail(CodeInvalid, "limits must be nonnegative")
		}
		if *f.v == 0 {
			*f.v = f.d
		}
	}
	if l.MaxEntryBytes < 0 {
		return l, fail(CodeInvalid, "entry byte limit must be nonnegative")
	}
	if l.MaxEntryBytes == 0 {
		l.MaxEntryBytes = min(def.MaxEntryBytes, l.MaxSnapshotBytes)
	}
	if l.MaxBytes < 0 || l.MaxReceiptBytes < 0 {
		return l, fail(CodeInvalid, "byte limits must be nonnegative")
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = def.MaxBytes
	}
	if l.MaxReceiptBytes == 0 {
		l.MaxReceiptBytes = def.MaxReceiptBytes
	}
	durations := []struct {
		v *time.Duration
		d time.Duration
	}{{&l.ReceiptRetention, def.ReceiptRetention}, {&l.MaxWait, def.MaxWait}, {&l.DefaultLeaseDuration, def.DefaultLeaseDuration}, {&l.MaxLeaseDuration, def.MaxLeaseDuration}, {&l.MaxLeaseLifetime, def.MaxLeaseLifetime}}
	for _, f := range durations {
		if *f.v < 0 {
			return l, fail(CodeInvalid, "durations must be nonnegative")
		}
		if *f.v == 0 {
			*f.v = f.d
		}
	}
	if l.MaxDeliveryAttempts == 0 {
		l.MaxDeliveryAttempts = def.MaxDeliveryAttempts
	}
	if l.DefaultLeaseDuration > l.MaxLeaseDuration || l.MaxLeaseDuration > l.MaxLeaseLifetime {
		return l, fail(CodeInvalid, "inconsistent lease duration limits")
	}
	return l, nil
}

func validName(s string) bool      { return s != "" && len(s) <= 256 && utf8.ValidString(s) }
func fail(c Code, s string) *Error { return &Error{Code: c, Message: s} }
func copyRow(r Row) Row            { r.Values = copyValues(r.Values); return r }
func copyValues(v map[string]string) map[string]string {
	out := make(map[string]string, len(v))
	for k, v := range v {
		out[k] = v
	}
	return out
}
func copyRecord(r record) record {
	r.ControlClients = append([]string(nil), r.ControlClients...)
	r.Row = copyRow(r.Row)
	if r.Lease != nil {
		l := *r.Lease
		r.Lease = &l
	}
	return r
}
func cloneResult(r Result) Result {
	b, _ := json.Marshal(r)
	var out Result
	_ = json.Unmarshal(b, &out)
	return out
}
func requestKey(client string, seq uint64) string { return fmt.Sprintf("%s/%d", client, seq) }
func newToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func requestDigest(r Request) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (t *Table) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	stats := Stats{TableID: t.s.ID, Profile: t.s.Profile, Revision: t.s.Revision, DataBytes: t.s.DataBytes, ReceiptBytes: t.s.ReceiptBytes, Receipts: len(t.s.Receipts), Clients: len(t.s.Clients), Waiters: len(t.waiters), Closed: t.s.Closed, Unavailable: t.unavailable, Counts: t.s.Counts, OpCount: t.opCount.Load(), OpErrors: t.opErrors.Load(), OpUnknown: t.opUnknown.Load(), OpLatencyTotalNS: t.opLatencyTotal.Load(), OpLatencyMaxNS: t.opLatencyMax.Load()}
	for i := range stats.OpLatencyBuckets {
		stats.OpLatencyBuckets[i] = t.opLatencyBuckets[i].Load()
	}
	stats.Observations = t.observationSnapshot(time.Now())
	if t.s.Profile == ProfileVTS {
		stats.Counts.Available = 0
		for _, cells := range t.s.Data {
			if uint64(len(cells)) > stats.Counts.Available {
				stats.Counts.Available = uint64(len(cells))
			}
		}
	}
	return stats
}

func (t *Table) Limits() Limits { t.mu.Lock(); defer t.mu.Unlock(); return t.s.Limits }

func (t *Table) checkServing() error {
	if t.unavailable {
		return &Error{Code: CodeUnavailable, Message: "table requires authoritative recovery", OutcomeUnknown: true}
	}
	if t.s.Closed {
		return fail(CodeClosed, "table is closed")
	}
	return nil
}

func (t *Table) validateValues(v map[string]string) error {
	for k, value := range v {
		if !utf8.ValidString(value) {
			return fail(CodeInvalid, "table values must be valid UTF-8")
		}
		if !contains(t.s.Columns, k) {
			return fail(CodeInvalid, "unknown column")
		}
		if len(value) > t.s.Limits.MaxFieldBytes {
			return fail(CodeCapacity, "field byte limit exceeded")
		}
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func sortedRecordKeys(m map[string]record) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
