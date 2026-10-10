package h3fixture

import (
	"sync"
	"time"
)

// maxLedgerAttempts bounds the ledger so a runaway test cannot exhaust memory.
// Attempts beyond the bound are counted in Overflow but not retained.
const maxLedgerAttempts = 100_000

// Attempt is one application request the target actually received.
type Attempt struct {
	Seq        int64
	At         time.Time
	Listener   string // "tcp", "quic" or "quic-alt"
	Proto      string // r.Proto: "HTTP/1.1", "HTTP/2.0", "HTTP/3.0"
	Method     string
	Path       string
	Key        string // X-Ledger-Key header, else the "key" query parameter
	BodyBytes  int64
	BodySHA256 string
	AltUsed    string // Alt-Used request header, when present
}

// Ledger records every request the fixture handler saw and every side
// effect it applied. Tests use it to prove a write happened exactly once,
// independently of what the client believes.
type Ledger struct {
	mu       sync.Mutex
	seq      int64
	attempts []Attempt
	effects  map[string]int
	overflow int64
}

func newLedger() *Ledger {
	return &Ledger{effects: map[string]int{}}
}

func (l *Ledger) record(a Attempt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	a.Seq = l.seq
	if len(l.attempts) >= maxLedgerAttempts {
		l.overflow++
		return
	}
	l.attempts = append(l.attempts, a)
}

func (l *Ledger) applyEffect(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.effects[key]++
	return l.effects[key]
}

// Attempts returns a copy of the retained attempts in arrival order.
func (l *Ledger) Attempts() []Attempt {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Attempt(nil), l.attempts...)
}

// Count returns how many retained attempts match keep.
func (l *Ledger) Count(keep func(Attempt) bool) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, a := range l.attempts {
		if keep == nil || keep(a) {
			n++
		}
	}
	return n
}

// Effects returns how many times the side effect identified by key ran.
func (l *Ledger) Effects(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.effects[key]
}

// Overflow reports attempts that arrived after the retention bound.
func (l *Ledger) Overflow() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.overflow
}

// Reset clears attempts and effects.
func (l *Ledger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts = nil
	l.effects = map[string]int{}
	l.overflow = 0
}
