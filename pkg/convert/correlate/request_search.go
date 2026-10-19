package correlate

import "strings"

// RequestSearch answers reuse queries over one fixed observation list. Each
// observation's searchable request surfaces (raw, query-decoded and parsed
// form values) are built once on first use instead of once per query, which
// keeps candidate-by-observation scans linear in the bytes actually searched.
// UsedAfter and ClientSentThrough also build a suffix-array index over every
// observation's surfaces on first use, so a query costs its own length and
// matches rather than a rescan of every later request.
type RequestSearch struct {
	observations []Observation
	all          []*observationSearch
	client       []*observationSearch
	allIndex     *surfaceIndex
	clientIndex  *surfaceIndex
	allBuilt     bool
	clientBuilt  bool
}

func NewRequestSearch(observations []Observation) *RequestSearch {
	return &RequestSearch{
		observations: observations,
		all:          make([]*observationSearch, len(observations)),
		client:       make([]*observationSearch, len(observations)),
	}
}

func (requests *RequestSearch) search(index int) *observationSearch {
	if requests.all[index] == nil {
		search := newObservationSearch(requests.observations[index])
		requests.all[index] = &search
	}
	return requests.all[index]
}

func (requests *RequestSearch) clientSearch(index int) *observationSearch {
	if requests.client[index] == nil {
		search := newClientObservationSearch(requests.observations[index])
		requests.client[index] = &search
	}
	return requests.client[index]
}

// Contains reports whether observation index sends value on any request
// surface, matching ObservationContains.
func (requests *RequestSearch) Contains(index int, value string) bool {
	return requests.search(index).Contains(value)
}

// SearchBytes is the number of bytes Contains scans for observation index.
func (requests *RequestSearch) SearchBytes(index int) int64 {
	return requests.search(index).searchBytes()
}

// UsedAfter returns the IDs, in order, of the observations after index whose
// request surfaces contain value, matching Contains on each of them. charge
// is called with the work each step costs before it runs; its error stops
// the search.
func (requests *RequestSearch) UsedAfter(index int, value string, charge func(int64) error) ([]int, error) {
	usedIn := make([]int, 0)
	if value == "" {
		return usedIn, nil
	}
	if found, ok, err := requests.indexed(false).observationsContaining(value, charge); ok || err != nil {
		if err != nil {
			return nil, err
		}
		for _, later := range found {
			if later > index {
				usedIn = append(usedIn, requests.observations[later].ID)
			}
		}
		return usedIn, nil
	}
	for later := index + 1; later < len(requests.observations); later++ {
		if err := charge(requests.SearchBytes(later)); err != nil {
			return nil, err
		}
		if requests.Contains(later, value) {
			usedIn = append(usedIn, requests.observations[later].ID)
		}
	}
	return usedIn, nil
}

// ObservationsContaining returns the indexes, in order, of every observation
// whose request surfaces contain value (see Contains), or ok=false when the
// surfaces were too large to index or value cannot be looked up in it; the
// caller then searches observation by observation.
func (requests *RequestSearch) ObservationsContaining(value string, charge func(int64) error) (found []int, ok bool, err error) {
	if value == "" {
		return nil, true, nil
	}
	return requests.indexed(false).observationsContaining(value, charge)
}

func (requests *RequestSearch) indexed(client bool) *surfaceIndex {
	if client {
		if !requests.clientBuilt {
			requests.clientBuilt = true
			requests.clientIndex = newSurfaceIndex(len(requests.observations), requests.clientSearch)
		}
		return requests.clientIndex
	}
	if !requests.allBuilt {
		requests.allBuilt = true
		requests.allIndex = newSurfaceIndex(len(requests.observations), requests.search)
	}
	return requests.allIndex
}

// ClientSentThrough reports whether the client itself sent value in any
// request up to and including observation through (see
// newClientObservationSearch). charge is called with the bytes each
// observation scan costs before it runs; its error stops the search.
func (requests *RequestSearch) ClientSentThrough(through int, value string, charge func(int64) error) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return false, nil
	}
	if found, ok, err := requests.indexed(true).observationsContaining(value, charge); ok || err != nil {
		if err != nil {
			return false, err
		}
		return len(found) > 0 && found[0] <= through, nil
	}
	for earlier := 0; earlier <= through && earlier < len(requests.observations); earlier++ {
		search := requests.clientSearch(earlier)
		if err := charge(search.searchBytes()); err != nil {
			return false, err
		}
		if search.Contains(value) {
			return true, nil
		}
	}
	return false, nil
}

// CandidateCanCorrelate applies the value-only precision rules shared by every
// detector: name-shaped values from name heuristics, authentication scheme
// names and bare URL origins are never server state.
func CandidateCanCorrelate(candidate Candidate) bool {
	return candidateValueCanCorrelate(candidate)
}

// ClientOriginCheckApplies reports whether a rule's correlations must be
// dropped when the client already sent the value (see ClientSentThrough).
func ClientOriginCheckApplies(ruleID string) bool {
	return clientOriginCheckApplies(ruleID)
}
