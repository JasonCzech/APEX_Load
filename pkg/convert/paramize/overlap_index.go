package paramize

import "strings"

// maxIndexedLiteralOccurrences bounds the positions retained for one literal.
const maxIndexedLiteralOccurrences = 1 << 20

// LiteralOccurrenceIndex answers merged-region questions for one rewrite
// surface. Each literal's occurrences are found once and cached; a pair is then
// compared by position instead of rescanning the surface for every candidate
// overlap length. Exhausting the shared budget fails closed (reports overlap).
type LiteralOccurrenceIndex struct {
	budget   *LiteralOverlapSearchBudget
	surfaces []string
	// bases place each surface at a virtual offset one byte past the previous
	// one, so positions stay ordered and no occurrence spans two surfaces.
	bases  []int
	starts map[string][]int
}

// NewLiteralOccurrenceIndex indexes one surface lazily under budget. A caller
// that joins fields must separate them with a byte its literals cannot hold
// (NUL); literals containing NUL are reported as overlapping.
func (budget *LiteralOverlapSearchBudget) NewLiteralOccurrenceIndex(surface string) *LiteralOccurrenceIndex {
	return budget.NewLiteralOccurrenceIndexForSurfaces([]string{surface})
}

// NewLiteralOccurrenceIndexForSurfaces indexes separate fields without copying
// them into one aggregate string.
func (budget *LiteralOverlapSearchBudget) NewLiteralOccurrenceIndexForSurfaces(surfaces []string) *LiteralOccurrenceIndex {
	bases := make([]int, len(surfaces))
	next := 0
	for index, surface := range surfaces {
		bases[index] = next
		next += len(surface) + 1
	}
	return &LiteralOccurrenceIndex{budget: budget, surfaces: surfaces, bases: bases, starts: make(map[string][]int)}
}

// PartiallyOverlap reports whether an occurrence of one literal begins inside
// an occurrence of the other and extends past its end. Containment is not a
// partial overlap; callers decide containment separately.
func (index *LiteralOccurrenceIndex) PartiallyOverlap(left, right string) bool {
	if left == "" || right == "" || len(index.surfaces) == 0 {
		return false
	}
	if strings.IndexByte(left, 0) >= 0 || strings.IndexByte(right, 0) >= 0 {
		return true // a separator inside a literal defeats field isolation
	}
	bordered := false
	for _, pair := range [2][2]string{{left, right}, {right, left}} {
		failure, matched, ok := index.budget.suffixPrefixBorders(pair[0], pair[1])
		if !ok {
			return true
		}
		for overlap := matched; overlap > 0; overlap = failure[overlap-1] {
			if overlap < min(len(pair[0]), len(pair[1])) {
				bordered = true
				break
			}
		}
	}
	if !bordered {
		return false // no suffix of one is a prefix of the other
	}
	leftStarts, ok := index.occurrences(left)
	if !ok {
		return true
	}
	rightStarts, ok := index.occurrences(right)
	if !ok {
		return true
	}
	return startsInsideAndExtendsPast(leftStarts, len(left), rightStarts, len(right)) ||
		startsInsideAndExtendsPast(rightStarts, len(right), leftStarts, len(left))
}

func (index *LiteralOccurrenceIndex) occurrences(literal string) ([]int, bool) {
	if starts, ok := index.starts[literal]; ok {
		return starts, true
	}
	var starts []int
	for surfaceIndex, surface := range index.surfaces {
		for offset := 0; offset <= len(surface)-len(literal); {
			relative := strings.Index(surface[offset:], literal)
			scanned := len(surface) - offset
			if relative >= 0 {
				scanned = relative + len(literal)
			}
			if !index.budget.charge(scanned) {
				return nil, false
			}
			if relative < 0 {
				break
			}
			if len(starts) == maxIndexedLiteralOccurrences {
				index.budget.charge(-1) // exhaust: the position list is incomplete
				return nil, false
			}
			starts = append(starts, index.bases[surfaceIndex]+offset+relative)
			offset += relative + 1
		}
	}
	index.starts[literal] = starts
	return starts, true
}

// startsInsideAndExtendsPast reports whether some second occurrence b satisfies
// a < b < a+firstLen < b+secondLen for a first occurrence a. Both start lists
// are ascending, so one forward pass suffices.
func startsInsideAndExtendsPast(first []int, firstLen int, second []int, secondLen int) bool {
	cursor := 0
	for _, start := range first {
		end := start + firstLen
		lower := max(start, end-secondLen) // b must exceed both bounds
		for cursor < len(second) && second[cursor] <= lower {
			cursor++
		}
		if cursor < len(second) && second[cursor] < end {
			return true
		}
	}
	return false
}
