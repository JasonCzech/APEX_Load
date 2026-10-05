package api

import (
	"net/http"
	"time"
)

// NotificationDelivery contains only operator-safe metadata, never destination
// URLs, credential references, response bodies or transport error strings.
type NotificationDelivery struct {
	Name       string    `json:"name"`
	Format     string    `json:"format"`
	Status     string    `json:"status"`
	FinishedAt time.Time `json:"finished_at"`
}

type NotificationPreviewRequest struct {
	Format    string `json:"format"`
	EventType string `json:"event_type"`
}

type NotificationTestRequest struct {
	Index            *int   `json:"index"`
	ExpectedRevision string `json:"expected_revision"`
}

func (d *notificationDispatcher) recordDelivery(webhook NotificationWebhookSettings, status string) {
	d.historyMu.Lock()
	defer d.historyMu.Unlock()
	d.history = append([]NotificationDelivery{{Name: webhook.Name, Format: webhook.Format, Status: status, FinishedAt: time.Now().UTC()}}, d.history...)
	if len(d.history) > 50 {
		d.history = d.history[:50]
	}
}

func (s *Server) handleNotificationPreview(w http.ResponseWriter, r *http.Request) {
	var req NotificationPreviewRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if _, ok := supportedNotificationEvents[req.EventType]; !ok {
		writeError(w, http.StatusBadRequest, "Choose a supported event type")
		return
	}
	payload, err := buildNotificationPayload(req.Format, NotificationEvent{Type: req.EventType, TestID: "preview", TestName: "Notification preview", Status: "example", Summary: "Sample event; no delivery", Timestamp: time.Now().UTC()})
	if err != nil {
		writeError(w, http.StatusBadRequest, "Choose a supported notification format")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payload": payload, "delivery_disabled": true})
}

func (s *Server) handleNotificationTest(w http.ResponseWriter, r *http.Request) {
	var req NotificationTestRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	settings := s.runner.GetSystemSettings()
	if req.ExpectedRevision == "" || req.ExpectedRevision != systemSettingsRevision(settings) {
		writeError(w, http.StatusConflict, "Settings changed. Reload and review the saved notification route before testing.")
		return
	}
	if req.Index == nil || *req.Index < 0 || *req.Index >= len(settings.Notifications.Webhooks) {
		writeError(w, http.StatusBadRequest, "Select a saved notification route")
		return
	}
	webhook := settings.Notifications.Webhooks[*req.Index]
	if !webhook.Enabled {
		writeError(w, http.StatusConflict, "Enable and save the route before sending a test")
		return
	}
	event := NotificationEvent{Type: "test_complete", TestID: "notification-test", TestName: "APEX Load notification delivery test", Status: "test", Summary: "Operator-requested delivery test", Timestamp: time.Now().UTC()}
	payload, err := buildNotificationPayloadForWebhook(webhook, event)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Saved notification format is invalid")
		return
	}
	if !s.runner.notifier.tryDispatch(webhook, payload, notificationDedupeKey(webhook, event), nil) {
		writeError(w, http.StatusServiceUnavailable, "Notification dispatcher is busy or unavailable; retry later")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "message": "Test accepted. Refresh recent deliveries to see its outcome."})
}

func (s *Server) handleNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	rows := []NotificationDelivery{}
	if d := s.runner.notifier; d != nil {
		d.historyMu.Lock()
		rows = append(rows, d.history...)
		d.historyMu.Unlock()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": rows, "scope": "This controller process; latest 50 completed attempts. Cleared on restart."})
}
