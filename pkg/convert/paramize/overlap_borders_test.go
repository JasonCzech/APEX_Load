package paramize

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// The linear border search must report exactly the suffix/prefix overlaps the
// former per-length comparison found; collision safety depends on it.
func TestSuffixPrefixBordersMatchBruteForce(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	alphabet := "ab%"
	text := func() string {
		value := make([]byte, random.Intn(24))
		for index := range value {
			value[index] = alphabet[random.Intn(len(alphabet))]
		}
		return string(value)
	}
	for iteration := 0; iteration < 20000; iteration++ {
		first, second := text(), text()
		var want []int
		for overlap := min(len(first), len(second)) - 1; overlap > 0; overlap-- {
			if first[len(first)-overlap:] == second[:overlap] {
				want = append(want, overlap)
			}
		}
		failure, matched, ok := NewLiteralOverlapSearchBudget().suffixPrefixBorders(first, second)
		if !ok {
			t.Fatal("budget exhausted on a tiny pair")
		}
		var got []int
		for overlap := matched; overlap > 0; overlap = failure[overlap-1] {
			if overlap < min(len(first), len(second)) {
				got = append(got, overlap)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("borders(%q,%q)=%v want %v", first, second, got, want)
		}
	}
}

// A literal repeated in every request must not exhaust the budget: each
// occurrence is charged for the bytes scanned, not the remaining surface.
func TestRepeatedLiteralSurfaceScanIsLinear(t *testing.T) {
	const requests = 4000
	request := "message=%7B%22actions%22%3A%5B%5D%7D&aura.context=one%3Aone&aura.token=abc&"
	surface := make([]byte, 0, len(request)*requests)
	for index := 0; index < requests; index++ {
		surface = append(surface, request...)
	}
	budget := NewLiteralOverlapSearchBudget()
	// "one%3Aone" recurs in every request; "e%3A" shares a border with it.
	if budget.LiteralsPartiallyOverlapInSurface(string(surface), "one%3Aone", "e%3Azzz") || budget.Exhausted() {
		t.Fatalf("repeated literal scan exhausted=%v", budget.Exhausted())
	}
}

// The occurrence index must agree with the merged-region predicate it replaces.
func TestLiteralOccurrenceIndexMatchesMergedRegionPredicate(t *testing.T) {
	random := rand.New(rand.NewSource(11))
	text := func(limit int) string {
		value := make([]byte, 1+random.Intn(limit))
		for index := range value {
			value[index] = "abc"[random.Intn(3)]
		}
		return string(value)
	}
	for iteration := 0; iteration < 20000; iteration++ {
		surface, left, right := text(40), text(6), text(6)
		if left == right || strings.Contains(left, right) || strings.Contains(right, left) {
			continue // containment is decided separately by callers
		}
		want := NewLiteralOverlapSearchBudget().LiteralsPartiallyOverlapInSurface(surface, left, right)
		budget := NewLiteralOverlapSearchBudget()
		got := budget.NewLiteralOccurrenceIndex(surface).PartiallyOverlap(left, right)
		if got != want || budget.Exhausted() {
			t.Fatalf("surface=%q left=%q right=%q index=%v predicate=%v", surface, left, right, got, want)
		}
	}
}

// Many claims against a large surface stay within budget: each literal is
// indexed at most once, and unbordered pairs never scan the surface.
func TestLiteralOccurrenceIndexScalesToManyClaims(t *testing.T) {
	var surface strings.Builder
	for request := 0; request < 3000; request++ {
		surface.WriteString("message=%7B%22id%22%3A%22")
		surface.WriteString(strings.Repeat("x", 300))
		surface.WriteString("%22%7D&aura.token=eyJsynthetic&aura.context=one%3Aone\x00")
	}
	budget := NewLiteralOverlapSearchBudget()
	index := budget.NewLiteralOccurrenceIndex(surface.String())
	claims := make([]string, 0, 150)
	for claim := 0; claim < 150; claim++ {
		claims = append(claims, "value-"+strings.Repeat(string(rune('a'+claim%26)), 1+claim%17)+"-1")
	}
	for left := range claims {
		for right := left + 1; right < len(claims); right++ {
			index.PartiallyOverlap(claims[left], claims[right])
		}
	}
	if budget.Exhausted() {
		t.Fatal("pairwise overlap analysis exhausted the budget on an ordinary claim set")
	}
}
