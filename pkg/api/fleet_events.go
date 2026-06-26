package api

import "time"

func (s *Server) broadcastFleetEvent(event string, data map[string]any) {
	if s == nil || s.hub == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	s.hub.BroadcastJSON(map[string]any{
		"type":          "fleet_event",
		"event":         event,
		"data":          data,
		"timestamp":     time.Now().UTC().Format(time.RFC3339Nano),
		"broadcast_all": true,
	})
}
