package protocol

import (
	"bytes"
	"encoding/json"
)

// UnmarshalJSON rejects shape loss before fixed-size histograms are decoded:
// encoding/json otherwise silently drops array elements beyond a Go array's
// length. Invalid evidence becomes a marker that Retained discloses, not an
// accepted prefix. This is validation only; it never deserializes Java objects.
func (s *JavaHTTPSummary) UnmarshalJSON(raw []byte) error {
	*s = JavaHTTPSummary{}
	if candidate, ok := decodeJavaHTTPSummary(raw); ok && candidate.Valid() {
		*s = candidate
	}
	return nil
}

// decodeJavaHTTPSummary reports false on any shape loss; the caller keeps the
// invalid marker rather than failing the enclosing document.
func decodeJavaHTTPSummary(raw []byte) (JavaHTTPSummary, bool) {
	if len(raw) > 64<<10 {
		return JavaHTTPSummary{}, false
	}
	type plain JavaHTTPSummary
	var v plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&v) != nil {
		return JavaHTTPSummary{}, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return JavaHTTPSummary{}, false
	}
	for _, key := range []string{"base64_decode_ms", "base64_encode_ms", "enclosing_ms"} {
		var d struct {
			Buckets []json.RawMessage `json:"buckets"`
		}
		if json.Unmarshal(fields[key], &d) != nil || len(d.Buckets) != len(v.Enclosing.Buckets) {
			return JavaHTTPSummary{}, false
		}
	}
	return JavaHTTPSummary(v), true
}
