package generatorrpc

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const FeatureMessagingProgressV1 = "messaging_progress_v1"

func messagingProgressVersion(s *protocol.MessagingProgressSnapshot) uint32 {
	if s == nil {
		return 0
	}
	return protocol.MessagingProgressVersion
}

func messagingProgressJSON(s *protocol.MessagingProgressSnapshot) string {
	if s == nil {
		return ""
	}
	// Clone validates and converts malformed input into explicit unavailable evidence.
	s = s.Clone()
	b, err := json.Marshal(s)
	if err != nil || len(b) > protocol.MaxMessagingProgressBytes {
		b, _ = json.Marshal(protocol.NewUnavailableMessagingProgress("oversized"))
	}
	return string(b)
}

func messagingProgressFromJSON(raw string, version uint32) *protocol.MessagingProgressSnapshot {
	if raw == "" && version == 0 {
		return nil
	}
	if version != protocol.MessagingProgressVersion {
		return protocol.NewUnavailableMessagingProgress("unsupported_version")
	}
	if len(raw) > protocol.MaxMessagingProgressBytes {
		return protocol.NewUnavailableMessagingProgress("oversized")
	}
	var s protocol.MessagingProgressSnapshot
	if raw == "" || json.Unmarshal([]byte(raw), &s) != nil || !s.Valid() {
		return protocol.NewUnavailableMessagingProgress("malformed")
	}
	return s.Clone()
}

func messagingProgressForGroup(s *protocol.MessagingProgressSnapshot, group string) *protocol.MessagingProgressSnapshot {
	if s == nil {
		return nil
	}
	out := s.Clone()
	rows := out.Rows
	out.Rows = out.Rows[:0]
	for _, row := range rows {
		if row.Group == group {
			out.Rows = append(out.Rows, row)
		}
	}
	// The clone ensures row pointer fields remain private after filtering.
	return out.Clone()
}
