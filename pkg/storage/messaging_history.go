package storage

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
)

// Each history side channel has an independent bound. Rejected evidence remains
// explicitly incomplete, while SQL NULL preserves absence in older rows.
const maxMessagingHistoryBytes = 128 << 10

func unavailableMessagingHistory() map[string]*protocol.MessagingSummary {
	return map[string]*protocol.MessagingSummary{"__other__": {Incomplete: true}}
}

func nullableMessagingJSON(s map[string]*protocol.MessagingSummary) any {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(protocol.CloneMessagingSummaries(s))
	if err != nil || len(b) > maxMessagingHistoryBytes {
		b, _ = json.Marshal(unavailableMessagingHistory())
	}
	return string(b)
}

func decodeMessagingHistoryJSON(raw string) map[string]*protocol.MessagingSummary {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return nil
	}
	if len(raw) > maxMessagingHistoryBytes {
		return unavailableMessagingHistory()
	}
	var s map[string]*protocol.MessagingSummary
	if json.Unmarshal([]byte(raw), &s) != nil {
		return unavailableMessagingHistory()
	}
	return protocol.CloneMessagingSummaries(s)
}

func nullableMessagingProgressJSON(s *protocol.MessagingProgressSnapshot) any {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(s.Clone())
	if err != nil || len(b) > protocol.MaxMessagingProgressBytes {
		b, _ = json.Marshal(protocol.NewUnavailableMessagingProgress("oversized"))
	}
	return string(b)
}

func decodeMessagingProgressHistoryJSON(raw string) *protocol.MessagingProgressSnapshot {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return nil
	}
	if len(raw) > protocol.MaxMessagingProgressBytes {
		return protocol.NewUnavailableMessagingProgress("oversized")
	}
	var s protocol.MessagingProgressSnapshot
	if json.Unmarshal([]byte(raw), &s) != nil || !s.Valid() {
		return protocol.NewUnavailableMessagingProgress("malformed")
	}
	return s.Clone()
}
