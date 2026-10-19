package correlate

import (
	"index/suffixarray"
	"sort"
	"strings"
)

// maxIndexedSurfaceBytes bounds the request surfaces one RequestSearch
// indexes (the index costs about five bytes per surface byte). A capture
// with more request text falls back to observation-by-observation search
// under the caller's work budget.
const maxIndexedSurfaceBytes = 256 << 20

// surfaceSeparator ends every indexed surface. A value that contains it is
// never looked up in the index, so no match can span two surfaces.
const surfaceSeparator = 0

// surfaceIndex finds which observations' request surfaces contain a value
// with one suffix-array lookup. Its answer is the same as asking Contains of
// every observation: each surface Contains searches is indexed separately.
type surfaceIndex struct {
	index *suffixarray.Index
	// starts[i] is the offset of observation i's first surface.
	starts []int
}

func newSurfaceIndex(count int, search func(int) *observationSearch) *surfaceIndex {
	var total int64
	for index := 0; index < count; index++ {
		surfaces := search(index)
		total += surfaces.searchBytes() + int64(len(surfaces.rawSurfaces)+len(surfaces.decodedSurfaces)+len(surfaces.formValues))
		if total > maxIndexedSurfaceBytes {
			return nil
		}
	}
	text := make([]byte, 0, total)
	starts := make([]int, count)
	for index := 0; index < count; index++ {
		starts[index] = len(text)
		surfaces := search(index)
		for _, group := range [][]string{surfaces.rawSurfaces, surfaces.decodedSurfaces, surfaces.formValues} {
			for _, surface := range group {
				text = append(text, surface...)
				text = append(text, surfaceSeparator)
			}
		}
	}
	return &surfaceIndex{index: suffixarray.New(text), starts: starts}
}

// observationsContaining returns the indexes, in order, of the observations
// with a surface containing value. ok is false when there is no index or
// value cannot be looked up in it. charge is called with the work of the
// lookup (the value's length, then its match count) before that work is
// done; its error is returned with ok=true.
func (x *surfaceIndex) observationsContaining(value string, charge func(int64) error) (found []int, ok bool, err error) {
	if x == nil || value == "" || strings.IndexByte(value, surfaceSeparator) >= 0 {
		return nil, false, nil
	}
	if err := charge(int64(len(value))); err != nil {
		return nil, true, err
	}
	offsets := x.index.Lookup([]byte(value), -1)
	if err := charge(int64(len(offsets))); err != nil {
		return nil, true, err
	}
	seen := make(map[int]bool, len(offsets))
	for _, offset := range offsets {
		seen[sort.SearchInts(x.starts, offset+1)-1] = true
	}
	found = make([]int, 0, len(seen))
	for observation := range seen {
		found = append(found, observation)
	}
	sort.Ints(found)
	return found, true, nil
}
