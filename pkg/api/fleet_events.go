package api

import "time"

// broadcastFleetEvent sends a content-free refresh signal. broadcast_all reaches
// every socket, including project-scoped viewers, and the hub has no per-client
// identity to filter admin payloads, so data (names, IDs, states) is never sent:
// clients refetch through the role-checked fleet APIs. data documents the change
// for a future identity-routed channel.
func (s *Server) broadcastFleetEvent(event string, data map[string]any) {
	if s == nil || s.hub == nil {
		return
	}
	_ = data
	s.hub.BroadcastJSON(map[string]any{
		"type":          "fleet_event",
		"event":         event,
		"timestamp":     time.Now().UTC().Format(time.RFC3339Nano),
		"broadcast_all": true,
	})
}
