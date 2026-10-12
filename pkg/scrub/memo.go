package scrub

import "sync"

const (
	maxMemoEntries = 1 << 16
	maxMemoBytes   = 8 << 20
)

type memoKind uint8

const (
	memoValue memoKind = iota
	memoLabel
)

type memoKey struct {
	kind  memoKind
	value string
}

// Memo caches Value and ValueLabel results under the zero Policy, the one
// disclosure projections scrub with. Each is a pure function of its input, so
// a cached result is the one a fresh call returns. An import draft's
// disclosure projection scrubs the same header values, labels and snippets
// thousands of times, and every call runs a dozen regexps however short its
// input. The cache is bounded by entries and bytes; past either bound a Memo
// scrubs without caching. A Memo is safe for concurrent use; its zero value is
// ready, and a nil *Memo scrubs without caching.
type Memo struct {
	mu     sync.Mutex
	values map[memoKey]string
	bytes  int
}

// Value returns Value(value, Policy{}).
func (m *Memo) Value(value string) string { return m.scrub(memoValue, value) }

// ValueLabel returns ValueLabel(value, Policy{}).
func (m *Memo) ValueLabel(value string) string { return m.scrub(memoLabel, value) }

func (m *Memo) scrub(kind memoKind, value string) string {
	if value == "" {
		return value
	}
	scrub := func() string {
		if kind == memoLabel {
			return ValueLabel(value, Policy{})
		}
		return Value(value, Policy{})
	}
	if m == nil {
		return scrub()
	}
	key := memoKey{kind: kind, value: value}
	m.mu.Lock()
	cached, ok := m.values[key]
	m.mu.Unlock()
	if ok {
		return cached
	}
	scrubbed := scrub()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, raced := m.values[key]; raced {
		// Another caller scrubbed the same value meanwhile; count it once.
		return scrubbed
	}
	if size := len(value) + len(scrubbed); len(m.values) < maxMemoEntries && size <= maxMemoBytes-m.bytes {
		if m.values == nil {
			m.values = make(map[memoKey]string)
		}
		m.values[key] = scrubbed
		m.bytes += size
	}
	return scrubbed
}
