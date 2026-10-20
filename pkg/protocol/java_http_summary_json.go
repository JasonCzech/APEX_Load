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
	if len(raw) > 64<<10 {
		return nil
	}
	type plain JavaHTTPSummary
	var v plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	for _, key := range []string{"base64_decode_ms", "base64_encode_ms", "enclosing_ms"} {
		var d struct {
			Buckets []json.RawMessage `json:"buckets"`
		}
		if err := json.Unmarshal(fields[key], &d); err != nil || len(d.Buckets) != len(v.Enclosing.Buckets) {
			return nil
		}
	}
	candidate := JavaHTTPSummary(v)
	if candidate.Valid() {
		*s = candidate
	}
	return nil
}
