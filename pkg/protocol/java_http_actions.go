package protocol

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// JavaHTTPActions keeps actual action populations separate from synthetic
// business transactions. Every action owns its measurement denominators.
// Oversized or malformed envelopes retain an explicit refusal, never a prefix
// which could be mistaken for all actions. Nil means no evidence from this peer.
type JavaHTTPActions struct {
	Version           int                              `json:"version"`
	Incomplete        bool                             `json:"incomplete,omitempty"`
	UnavailableReason string                           `json:"unavailable_reason,omitempty"`
	Actions           map[string]JavaHTTPActionSummary `json:"actions,omitempty"`
}
type JavaHTTPActionSummary struct {
	Population  int64            `json:"population"`
	Diagnostics *JavaHTTPSummary `json:"diagnostics"`
}

const MaxJavaHTTPActions = 128
const MaxJavaHTTPActionsBytes = 1 << 20

func unavailableJavaHTTPActions(reason string) *JavaHTTPActions {
	return &JavaHTTPActions{Version: 1, Incomplete: true, UnavailableReason: reason}
}
func javaHTTPActionNameValid(name string) bool {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (s *JavaHTTPActions) Retained() *JavaHTTPActions {
	if s == nil {
		return nil
	}
	if s.Version != 1 {
		return unavailableJavaHTTPActions("unsupported")
	}
	if s.UnavailableReason != "" {
		switch s.UnavailableReason {
		case "unsupported", "malformed", "action_limit", "byte_limit":
			return unavailableJavaHTTPActions(s.UnavailableReason)
		}
		return unavailableJavaHTTPActions("malformed")
	}
	if len(s.Actions) > MaxJavaHTTPActions {
		return unavailableJavaHTTPActions("action_limit")
	}
	out := &JavaHTTPActions{Version: 1, Incomplete: s.Incomplete, Actions: make(map[string]JavaHTTPActionSummary, len(s.Actions))}
	for name, a := range s.Actions {
		if !javaHTTPActionNameValid(name) || a.Population < 0 || a.Population > javaHTTPMaxExactBytes || a.Diagnostics == nil {
			return unavailableJavaHTTPActions("malformed")
		}
		a.Diagnostics = a.Diagnostics.RetainedForPopulation(a.Population)
		out.Incomplete = out.Incomplete || a.Diagnostics.Incomplete
		out.Actions[name] = a
	}
	return out
}

// AddJavaHTTPAction merges populations for the same action. The caller marks
// envelopes incomplete when source coverage is unknown; absence is not zero.
func AddJavaHTTPAction(dst **JavaHTTPActions, name string, population int64, s *JavaHTTPSummary) {
	if dst == nil || s == nil {
		return
	}
	if *dst == nil {
		*dst = &JavaHTTPActions{Version: 1, Actions: map[string]JavaHTTPActionSummary{}}
	}
	d := *dst
	if d.UnavailableReason != "" {
		return
	}
	if d.Version != 1 || !javaHTTPActionNameValid(name) || population < 0 || population > javaHTTPMaxExactBytes {
		*dst = unavailableJavaHTTPActions("malformed")
		return
	}
	old, exists := d.Actions[name]
	if !exists && len(d.Actions) >= MaxJavaHTTPActions {
		*dst = unavailableJavaHTTPActions("action_limit")
		return
	}
	if population > javaHTTPMaxExactBytes-old.Population {
		*dst = unavailableJavaHTTPActions("malformed")
		return
	}
	if d.Actions == nil {
		d.Actions = map[string]JavaHTTPActionSummary{}
	}
	old.Population += population
	MergeJavaHTTPSummary(&old.Diagnostics, s.RetainedForPopulation(population))
	old.Diagnostics = old.Diagnostics.RetainedForPopulation(old.Population)
	d.Incomplete = d.Incomplete || old.Diagnostics.Incomplete
	d.Actions[name] = old
}

func MergeJavaHTTPActions(dst **JavaHTTPActions, src *JavaHTTPActions) {
	if dst == nil || src == nil {
		return
	}
	src = src.Retained()
	if *dst == nil {
		*dst = src
		return
	}
	if src.UnavailableReason != "" {
		*dst = src
		return
	}
	if (*dst).UnavailableReason != "" {
		return
	}
	(*dst).Incomplete = (*dst).Incomplete || src.Incomplete
	for name, a := range src.Actions {
		AddJavaHTTPAction(dst, name, a.Population, a.Diagnostics)
	}
}

// MarshalJSON applies the same encoded-byte bound to live API, WebSocket and
// raw interval projections. Cardinality alone cannot bound JSON escaping or
// populated histogram counters. The plain alias avoids recursive marshaling.
func (s JavaHTTPActions) MarshalJSON() ([]byte, error) {
	type plain JavaHTTPActions
	b, err := json.Marshal((*plain)(s.Retained()))
	if err != nil || len(b) > MaxJavaHTTPActionsBytes {
		return json.Marshal((*plain)(unavailableJavaHTTPActions("byte_limit")))
	}
	return b, nil
}

// JavaHTTPActionsJSON bounds retained history/export envelopes independently of
// request bodies. A failed size check becomes a small explicit refusal.
func JavaHTTPActionsJSON(s *JavaHTTPActions) string {
	if s == nil {
		return ""
	}
	b, err := json.Marshal(s)
	if err != nil || len(b) > MaxJavaHTTPActionsBytes {
		b, _ = json.Marshal(unavailableJavaHTTPActions("byte_limit"))
	}
	return string(b)
}
func ParseJavaHTTPActionsJSON(raw string) *JavaHTTPActions {
	if len(raw) > MaxJavaHTTPActionsBytes {
		return unavailableJavaHTTPActions("byte_limit")
	}
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return nil
	}
	var s JavaHTTPActions
	if json.Unmarshal([]byte(raw), &s) != nil {
		return unavailableJavaHTTPActions("malformed")
	}
	return s.Retained()
}
