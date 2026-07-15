// Package boundedtext provides deterministic, UTF-8-safe projections for
// retaining user-controlled strings and maps under strict byte/cardinality
// budgets.
package boundedtext

import (
	"container/heap"
	"sort"
	"strings"
	"unicode/utf8"
)

// TruncateUTF8 returns a detached, valid UTF-8 string no larger than maxBytes.
// Invalid input bytes are replaced only within the retained prefix, avoiding
// an allocation proportional to an oversized discarded suffix.
func TruncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || value == "" {
		return ""
	}
	if utf8.ValidString(value) {
		if len(value) <= maxBytes {
			return strings.Clone(value)
		}
		end := maxBytes
		for end > 0 && !utf8.ValidString(value[:end]) {
			end--
		}
		return strings.Clone(value[:end])
	}

	var out strings.Builder
	out.Grow(min(maxBytes, len(value)))
	for len(value) > 0 {
		r, width := utf8.DecodeRuneInString(value)
		piece := value[:width]
		if r == utf8.RuneError && width == 1 {
			piece = string(utf8.RuneError)
		}
		if out.Len()+len(piece) > maxBytes {
			break
		}
		out.WriteString(piece)
		value = value[width:]
	}
	return out.String()
}

// SmallestStringKeys returns at most limit keys in ascending order. It keeps a
// fixed-size max heap so deterministic projection does not allocate a second
// slice proportional to an attacker-sized input map.
func SmallestStringKeys[V any](values map[string]V, limit int) []string {
	if len(values) == 0 || limit <= 0 {
		return nil
	}
	limit = min(limit, len(values))
	keys := make(maxStringHeap, 0, limit)
	for key := range values {
		if len(keys) < limit {
			heap.Push(&keys, key)
			continue
		}
		if key < keys[0] {
			keys[0] = key
			heap.Fix(&keys, 0)
		}
	}
	sort.Strings(keys)
	return keys
}

type maxStringHeap []string

func (h maxStringHeap) Len() int           { return len(h) }
func (h maxStringHeap) Less(i, j int) bool { return h[i] > h[j] }
func (h maxStringHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *maxStringHeap) Push(value any) {
	*h = append(*h, value.(string))
}

func (h *maxStringHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	old[len(old)-1] = ""
	*h = old[:len(old)-1]
	return last
}
