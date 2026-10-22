package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const BrowserDocumentReceiptClosureVersion = 3
const MaxBrowserProofActiveSlots = 512

// BrowserSourceEvent is emitted by the execution owner, never synthesized from
// successful provider snapshots. Slot expectation is durably acknowledged before
// Init; context expectation precedes NewContext. A terminal callback, physical
// native close and successful publication are three distinct obligations.
type BrowserSourceEvent struct {
	SourceOriginNS int64              `json:"source_origin_ns"`
	VUsers         int                `json:"vusers"`
	Authority      BrowserClockDomain `json:"authority"`
	OwnerID        string             `json:"owner_id"`
	Kind           string             `json:"kind"`
	SlotID         string             `json:"slot_id"`
	Sequence       uint64             `json:"sequence"`
	AtNS           int64              `json:"at_ns"`
	ExpectedSlots  uint64             `json:"expected_slots"`
	Successful     bool               `json:"successful"`
}

// BrowserNativeDocumentExpectation comes from native committed loader events.
// Missing target bindings retain an expectation with empty document identity.
// NativeID is an opaque hash; URLs and renderer tokens are never serialized.
type BrowserNativeDocumentExpectation struct {
	NativeID    string `json:"native_id"`
	DocumentID  string `json:"document_id"`
	LifecycleID string `json:"lifecycle_id"`
	Terminal    bool   `json:"terminal"`
	LowerNS     int64  `json:"lower_ns"`
}
type BrowserNativeSourceClosure struct {
	Method            string                             `json:"method"`
	DiscoveryComplete bool                               `json:"discovery_complete"`
	Documents         []BrowserNativeDocumentExpectation `json:"documents"`
}

func (e BrowserSourceEvent) valid() bool {
	if !e.Authority.Valid() || len(e.OwnerID) == 0 || len(e.OwnerID) > 256 || e.Sequence == 0 || e.Sequence > MaxBrowserDocumentReceiptRecords || e.AtNS < 0 || e.SourceOriginNS < 0 || e.SourceOriginNS > e.AtNS || e.VUsers < 0 || e.ExpectedSlots > MaxBrowserDocumentReceiptRecords {
		return false
	}
	switch e.Kind {
	case "owner_expected", "owner_terminal", "steady", "unknown":
		return e.SlotID == ""
	case "slot_expected", "slot_terminal", "context_expected", "context_terminal":
		return BrowserEvidenceGeneration(e.SlotID)
	default:
		return false
	}
}
func (c *BrowserNativeSourceClosure) clone(generation string) *BrowserNativeSourceClosure {
	if c == nil {
		return nil
	}
	if c.Method != "chromium_cdp_commits_v1" && c.Method != "unavailable" || len(c.Documents) > MaxBrowserDocumentObservations {
		return nil
	}
	out := *c
	if c.Documents != nil {
		out.Documents = append(make([]BrowserNativeDocumentExpectation, 0, len(c.Documents)), c.Documents...)
	}
	ids := map[string]bool{}
	for _, d := range c.Documents {
		if d.LowerNS < 0 || len(d.NativeID) != 64 || stringsNotHex(d.NativeID) || ids[d.NativeID] {
			return nil
		}
		ids[d.NativeID] = true
		if d.DocumentID != "" && !browserEvidenceID(d.DocumentID, generation, false) || d.LifecycleID != "" && !browserEvidenceID(d.LifecycleID, generation, false) || (d.DocumentID == "") != (d.LifecycleID == "") || d.Terminal && d.DocumentID == "" {
			return nil
		}
	}
	if c.DiscoveryComplete && c.Method != "chromium_cdp_commits_v1" {
		return nil
	}
	return &out
}
func stringsNotHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return true
		}
	}
	return false
}

// BrowserSourceJournal is owned by one archive writer. Its emit function must
// durably persist each record before returning nil. Failed expectation writes
// prevent the corresponding native operation; later failure remains sticky.
type BrowserSourceJournal struct {
	mu              sync.Mutex
	authority       BrowserClockDomain
	owner           string
	start           time.Time
	emit            func(*BrowserDocumentReceiptEvidence) error
	sequence, total uint64
	vusers          int
	active          map[string]*BrowserSourceLease
	failure         error
	sealed          bool
}
type browserExpectedContext struct {
	closed   bool
	originNS int64
}
type BrowserSourceLease struct {
	journal  *BrowserSourceJournal
	id       string
	terminal bool
	contexts map[string]browserExpectedContext
}
type BrowserSourceJournalProvider interface {
	BrowserDocumentSourceJournal() (*BrowserSourceJournal, error)
}
type BrowserSourceLeaseProvider interface {
	SetBrowserDocumentSourceLease(*BrowserSourceLease)
	BrowserDocumentSourceLease() *BrowserSourceLease
}

func browserProofID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func NewBrowserSourceJournal(runID, owner string, emit func(*BrowserDocumentReceiptEvidence) error) (*BrowserSourceJournal, error) {
	id, err := browserProofID()
	if err != nil {
		return nil, err
	}
	domain, err := browserProofID()
	if err != nil {
		return nil, err
	}
	j := &BrowserSourceJournal{authority: BrowserClockDomain{RunID: runID, Incarnation: id, DomainID: domain}, owner: owner, start: time.Now(), emit: emit, active: map[string]*BrowserSourceLease{}}
	if !j.authority.Valid() || owner == "" || len(owner) > 256 || emit == nil {
		return nil, errors.New("browser source journal identity unavailable")
	}
	if err = j.writeLocked("owner_expected", "", true, nil); err != nil {
		return nil, err
	}
	return j, nil
}
func (j *BrowserSourceJournal) writeLocked(kind, slot string, success bool, e *BrowserDocumentReceiptEvidence) (err error) {
	defer func() {
		if recover() != nil {
			j.failure = errors.New("browser source publisher panicked")
			err = j.failure
		}
	}()
	if j.failure != nil {
		return j.failure
	}
	if j.sealed {
		return errors.New("browser source journal sealed")
	}
	if j.sequence >= MaxBrowserDocumentReceiptRecords {
		j.failure = errors.New("browser source journal limit exceeded")
		return j.failure
	}
	j.sequence++
	if e == nil {
		e = &BrowserDocumentReceiptEvidence{Version: 3, SourceGeneration: j.authority.DomainID, StartedAt: j.start.UTC(), Receipts: []BrowserDocumentReceipt{}}
	} else {
		e = e.Clone()
		if e == nil {
			j.failure = errors.New("browser source receipt malformed")
			return j.failure
		}
		e.Version = 3
	}
	e.Source = &BrowserSourceEvent{Authority: j.authority, OwnerID: j.owner, Kind: kind, SlotID: slot, Sequence: j.sequence, AtNS: int64(time.Since(j.start)), ExpectedSlots: j.total, Successful: success, VUsers: j.vusers}
	if l := j.active[slot]; l != nil {
		if context, ok := l.contexts[e.SourceGeneration]; ok {
			e.Source.SourceOriginNS = context.originNS
		}
	}
	j.failure = j.emit(e)
	return j.failure
}
func (j *BrowserSourceJournal) Begin() (*BrowserSourceLease, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.active) >= MaxBrowserProofActiveSlots {
		j.failure = errors.New("browser source active slot limit exceeded")
		return nil, j.failure
	}
	id, err := browserProofID()
	if err != nil {
		j.failure = err
		return nil, err
	}
	j.total++
	if err = j.writeLocked("slot_expected", id, true, nil); err != nil {
		return nil, err
	}
	l := &BrowserSourceLease{journal: j, id: id, contexts: map[string]browserExpectedContext{}}
	j.active[id] = l
	return l, nil
}
func (l *BrowserSourceLease) ExpectContext(e *BrowserDocumentReceiptEvidence, nativeStart time.Time) error {
	if l == nil {
		return nil
	}
	j := l.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	if l.terminal || e == nil || !BrowserEvidenceGeneration(e.SourceGeneration) || len(l.contexts) >= MaxBrowserProofActiveSlots {
		j.failure = errors.New("browser context expectation unavailable")
		return j.failure
	}
	if _, exists := l.contexts[e.SourceGeneration]; exists {
		j.failure = errors.New("duplicate browser context expectation")
		return j.failure
	}
	if nativeStart == nativeStart.Round(0) || !nativeStart.UTC().Equal(e.StartedAt) || nativeStart.Before(j.start) {
		j.failure = errors.New("source monotonic origin unavailable")
		return j.failure
	}
	origin := nativeStart.Sub(j.start).Nanoseconds()
	l.contexts[e.SourceGeneration] = browserExpectedContext{originNS: origin}
	if err := j.writeLocked("context_expected", l.id, true, e); err != nil {
		return err
	}
	l.contexts[e.SourceGeneration] = browserExpectedContext{originNS: origin}
	return nil
}
func (l *BrowserSourceLease) CloseContext(e *BrowserDocumentReceiptEvidence, success bool) error {
	if l == nil {
		return nil
	}
	j := l.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	if e == nil {
		return errors.New("browser terminal snapshot missing")
	}
	closed, expected := l.contexts[e.SourceGeneration]
	if !expected {
		return errors.New("unexpected browser context terminal")
	}
	if closed.closed {
		return nil
	}
	if err := j.writeLocked("context_terminal", l.id, success, e); err != nil {
		return err
	}
	closed.closed = true
	l.contexts[e.SourceGeneration] = closed
	return nil
}
func (l *BrowserSourceLease) Finish(success bool) error {
	if l == nil {
		return nil
	}
	j := l.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	if l.terminal {
		return nil
	}
	for _, closed := range l.contexts {
		success = success && closed.closed
	}
	if err := j.writeLocked("slot_terminal", l.id, success, nil); err != nil {
		return err
	}
	l.terminal = true
	delete(j.active, l.id)
	return nil
}
func (j *BrowserSourceJournal) Phase(steady bool, vusers int) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.vusers = max(0, vusers)
	kind := "unknown"
	if steady {
		kind = "steady"
	}
	return j.writeLocked(kind, "", true, nil)
}
func (j *BrowserSourceJournal) Seal(success bool) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.sealed {
		return j.failure
	}
	err := j.writeLocked("owner_terminal", "", success && len(j.active) == 0, nil)
	j.sealed = true
	return err
}
func (j *BrowserSourceJournal) String() string {
	return fmt.Sprintf("browser source journal for %s", j.owner)
}
