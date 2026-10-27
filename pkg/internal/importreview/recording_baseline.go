package importreview

import (
	"slices"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

// Recorded baseline retention bounds. Durable persistence keeps at most this
// many observations, and at most this many estimated plaintext bytes, in the
// encrypted correlation sidecar. A preview attests exactly the prefix that
// persistence would keep, so the attested payload stays bounded too.
const (
	MaxRecordedBaselineObservations  = 500
	MaxRecordedBaselinePlaintextSize = 12 << 20
)

// RecordingBaseline is the option-resolved baseline a proxy recording preview
// attests for its save. Observations is the bounded prefix persistence keeps;
// RequestCount is how many observations the conversion produced before the
// bound, which persistence records as the original request count.
type RecordingBaseline struct {
	Observations []correlate.Observation `json:"observations,omitempty"`
	RequestCount int                     `json:"request_count"`
}

// NewRecordingBaseline bounds observations for attestation. It returns nil for
// a conversion that produced no observations, so an empty baseline never binds
// a preview differently from a missing one.
func NewRecordingBaseline(observations []correlate.Observation) *RecordingBaseline {
	if len(observations) == 0 {
		return nil
	}
	return &RecordingBaseline{
		Observations: BoundRecordingBaselineObservations(observations),
		RequestCount: len(observations),
	}
}

// Clone returns an independent copy of the baseline's slice header and
// observation list. Observations themselves are treated as immutable.
func (b *RecordingBaseline) Clone() *RecordingBaseline {
	if b == nil {
		return nil
	}
	return &RecordingBaseline{
		Observations: slices.Clone(b.Observations),
		RequestCount: b.RequestCount,
	}
}

// EstimatedRecordingObservationJSONSize conservatively estimates the encoded
// size of one raw baseline observation. encoding/json can expand one input
// byte to at most six bytes (control, invalid UTF-8 and HTML-escaped
// characters); the fixed overhead covers field names, delimiters, the integer
// ID and header entries.
func EstimatedRecordingObservationJSONSize(requestURL, requestBody, responseBody string, requestHeaders, responseHeaders map[string]string) int {
	size := 256
	add := func(value string) { size += 6 * len(value) }
	add(requestURL)
	add(requestBody)
	add(responseBody)
	for key, value := range requestHeaders {
		size += 16
		add(key)
		add(value)
	}
	for key, value := range responseHeaders {
		size += 16
		add(key)
		add(value)
	}
	return size
}

// BoundRecordingBaselineObservations returns the prefix of observations that
// durable persistence retains: at most MaxRecordedBaselineObservations, and
// stopping before the first observation that would push the estimated JSON
// array past MaxRecordedBaselinePlaintextSize.
func BoundRecordingBaselineObservations(observations []correlate.Observation) []correlate.Observation {
	estimatedBytes := 2 // JSON array brackets.
	kept := 0
	for _, obs := range observations {
		if kept >= MaxRecordedBaselineObservations {
			break
		}
		nextSize := estimatedBytes + EstimatedRecordingObservationJSONSize(obs.RequestURL, obs.RequestBody, obs.ResponseBody, obs.RequestHeaders, obs.ResponseHeaders)
		if kept > 0 {
			nextSize++ // comma between array elements
		}
		if nextSize > MaxRecordedBaselinePlaintextSize {
			break
		}
		estimatedBytes = nextSize
		kept++
	}
	return slices.Clone(observations[:kept])
}
