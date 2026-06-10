package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	notificationFormatSlack     = "slack"
	notificationFormatTeams     = "teams"
	notificationFormatDynatrace = "dynatrace"
)

var supportedNotificationEvents = map[string]struct{}{
	"test_created":  {},
	"test_pending":  {},
	"test_started":  {},
	"test_stopping": {},
	"test_paused":   {},
	"test_resumed":  {},
	"test_failed":   {},
	"test_complete": {},

	"telemetry_degraded":  {},
	"telemetry_breached":  {},
	"telemetry_recovered": {},
}

type NotificationsSettings struct {
	Webhooks []NotificationWebhookSettings `json:"webhooks,omitempty"`
}

type NotificationWebhookSettings struct {
	Name           string   `json:"name,omitempty"`
	Enabled        bool     `json:"enabled"`
	URL            string   `json:"url"`
	Format         string   `json:"format"`
	Events         []string `json:"events,omitempty"`
	APIToken       string   `json:"api_token,omitempty"`
	EntitySelector string   `json:"entity_selector,omitempty"`
	TimeoutMinutes int      `json:"timeout_minutes,omitempty"`
}

type NotificationEvent struct {
	Type       string    `json:"type"`
	TestID     string    `json:"test_id"`
	ProjectID  string    `json:"project_id,omitempty"`
	TestName   string    `json:"test_name"`
	Status     string    `json:"status"`
	Severity   string    `json:"severity,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Conditions []string  `json:"conditions,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

type notificationDispatcher struct {
	client *http.Client
}

func newNotificationDispatcher(client *http.Client) *notificationDispatcher {
	if client == nil {
		client = newSSRFSafeHTTPClient(5 * time.Second)
	}
	return &notificationDispatcher{client: client}
}

func cloneNotificationsSettings(in NotificationsSettings) NotificationsSettings {
	if len(in.Webhooks) == 0 {
		return NotificationsSettings{}
	}
	out := NotificationsSettings{Webhooks: make([]NotificationWebhookSettings, 0, len(in.Webhooks))}
	for _, webhook := range in.Webhooks {
		copied := webhook
		if len(webhook.Events) > 0 {
			copied.Events = append([]string(nil), webhook.Events...)
		}
		out.Webhooks = append(out.Webhooks, copied)
	}
	return out
}

func validateNotificationsSettings(settings NotificationsSettings) error {
	for index, webhook := range settings.Webhooks {
		prefix := fmt.Sprintf("notifications.webhooks[%d]", index)
		parsedURL, err := url.ParseRequestURI(webhook.URL)
		if err != nil || parsedURL == nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return fmt.Errorf("%s.url must be a valid http or https URL", prefix)
		}
		if err := validateOutboundURL(webhook.URL); err != nil {
			return fmt.Errorf("%s.url is not an allowed destination: %w", prefix, err)
		}
		switch webhook.Format {
		case notificationFormatSlack, notificationFormatTeams, notificationFormatDynatrace:
		default:
			return fmt.Errorf("%s.format must be one of %q, %q, or %q", prefix, notificationFormatSlack, notificationFormatTeams, notificationFormatDynatrace)
		}
		if webhook.Format == notificationFormatDynatrace {
			if strings.TrimSpace(webhook.APIToken) == "" {
				return fmt.Errorf("%s.api_token is required for Dynatrace webhooks", prefix)
			}
			if webhook.TimeoutMinutes < 0 || webhook.TimeoutMinutes > 360 {
				return fmt.Errorf("%s.timeout_minutes must be between 0 and 360", prefix)
			}
		}
		for _, eventType := range webhook.Events {
			if _, ok := supportedNotificationEvents[eventType]; !ok {
				return fmt.Errorf("%s.events contains unsupported event %q", prefix, eventType)
			}
		}
	}
	return nil
}

func (d *notificationDispatcher) Dispatch(settings NotificationsSettings, event NotificationEvent) {
	if d == nil {
		return
	}
	for _, webhook := range settings.Webhooks {
		if !webhook.Enabled || !notificationWebhookMatchesEvent(webhook, event.Type) {
			continue
		}
		payload, err := buildNotificationPayloadForWebhook(webhook, event)
		if err != nil {
			slog.Warn("notification payload build failed", "event", event.Type, "format", webhook.Format, "err", err)
			continue
		}
		go d.postJSON(webhook, payload)
	}
}

func notificationWebhookMatchesEvent(webhook NotificationWebhookSettings, eventType string) bool {
	if len(webhook.Events) == 0 {
		return true
	}
	for _, configured := range webhook.Events {
		if configured == eventType {
			return true
		}
	}
	return false
}

func buildNotificationPayload(format string, event NotificationEvent) (any, error) {
	return buildNotificationPayloadForWebhook(NotificationWebhookSettings{Format: format}, event)
}

func buildNotificationPayloadForWebhook(webhook NotificationWebhookSettings, event NotificationEvent) (any, error) {
	format := webhook.Format
	switch format {
	case notificationFormatSlack:
		return buildSlackNotificationPayload(event), nil
	case notificationFormatTeams:
		return buildTeamsNotificationPayload(event), nil
	case notificationFormatDynatrace:
		return buildDynatraceNotificationPayload(webhook, event), nil
	default:
		return nil, fmt.Errorf("unsupported notification format %q", format)
	}
}

func buildSlackNotificationPayload(event NotificationEvent) map[string]any {
	title := fmt.Sprintf("APEX Load %s", humanizeNotificationEvent(event.Type))
	return map[string]any{
		"text": fmt.Sprintf("%s: %s (%s)", title, event.TestName, event.Status),
		"blocks": []map[string]any{
			{
				"type": "header",
				"text": map[string]any{
					"type":  "plain_text",
					"text":  title,
					"emoji": true,
				},
			},
			{
				"type":   "section",
				"fields": notificationSlackFields(event),
			},
			{
				"type": "context",
				"elements": []map[string]any{{
					"type": "mrkdwn",
					"text": fmt.Sprintf("Event: `%s` at %s", event.Type, event.Timestamp.UTC().Format(time.RFC3339)),
				}},
			},
		},
	}
}

func notificationSlackFields(event NotificationEvent) []map[string]any {
	fields := []map[string]any{
		{"type": "mrkdwn", "text": fmt.Sprintf("*Test*\n%s", event.TestName)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Status*\n%s", event.Status)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Test ID*\n%s", event.TestID)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Project*\n%s", fallbackNotificationValue(event.ProjectID))},
	}
	if strings.TrimSpace(event.Severity) != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*Severity*\n%s", event.Severity)})
	}
	if strings.TrimSpace(event.Summary) != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*Summary*\n%s", event.Summary)})
	}
	if len(event.Conditions) > 0 {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*Conditions*\n%s", strings.Join(event.Conditions, ", "))})
	}
	return fields
}

func buildTeamsNotificationPayload(event NotificationEvent) map[string]any {
	title := fmt.Sprintf("APEX Load %s", humanizeNotificationEvent(event.Type))
	return map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"contentUrl":  nil,
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body": []map[string]any{
					{"type": "TextBlock", "weight": "Bolder", "size": "Medium", "text": title},
					{"type": "FactSet", "facts": notificationTeamsFacts(event)},
				},
			},
		}},
	}
}

func notificationTeamsFacts(event NotificationEvent) []map[string]any {
	facts := []map[string]any{
		{"title": "Test", "value": event.TestName},
		{"title": "Status", "value": event.Status},
		{"title": "Test ID", "value": event.TestID},
		{"title": "Project", "value": fallbackNotificationValue(event.ProjectID)},
		{"title": "Event", "value": event.Type},
		{"title": "Timestamp", "value": event.Timestamp.UTC().Format(time.RFC3339)},
	}
	if strings.TrimSpace(event.Severity) != "" {
		facts = append(facts, map[string]any{"title": "Severity", "value": event.Severity})
	}
	if strings.TrimSpace(event.Summary) != "" {
		facts = append(facts, map[string]any{"title": "Summary", "value": event.Summary})
	}
	if len(event.Conditions) > 0 {
		facts = append(facts, map[string]any{"title": "Conditions", "value": strings.Join(event.Conditions, ", ")})
	}
	return facts
}

func buildDynatraceNotificationPayload(webhook NotificationWebhookSettings, event NotificationEvent) map[string]any {
	eventType := "CUSTOM_INFO"
	if event.Type == "test_failed" || event.Type == "telemetry_breached" {
		eventType = "ERROR_EVENT"
	} else if event.Type == "telemetry_degraded" {
		eventType = "CUSTOM_ALERT"
	}
	payload := map[string]any{
		"eventType": eventType,
		"title":     fmt.Sprintf("APEX Load %s: %s", humanizeNotificationEvent(event.Type), event.TestName),
		"startTime": event.Timestamp.UTC().UnixMilli(),
		"properties": map[string]string{
			"Tool":                "APEX Load",
			"apexload.event_type": event.Type,
			"apexload.project_id": fallbackNotificationValue(event.ProjectID),
			"apexload.status":     event.Status,
			"apexload.test_id":    event.TestID,
			"apexload.test_name":  event.TestName,
		},
	}
	properties := payload["properties"].(map[string]string)
	if strings.TrimSpace(event.Severity) != "" {
		properties["apexload.severity"] = event.Severity
	}
	if strings.TrimSpace(event.Summary) != "" {
		properties["apexload.summary"] = event.Summary
	}
	if len(event.Conditions) > 0 {
		properties["apexload.conditions"] = strings.Join(event.Conditions, ",")
	}
	if selector := strings.TrimSpace(webhook.EntitySelector); selector != "" {
		payload["entitySelector"] = selector
	}
	if webhook.TimeoutMinutes > 0 {
		payload["timeout"] = webhook.TimeoutMinutes
	}
	return payload
}

func humanizeNotificationEvent(eventType string) string {
	parts := strings.Split(strings.TrimSpace(eventType), "_")
	for index, part := range parts {
		if part == "" {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func fallbackNotificationValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Unassigned"
	}
	return value
}

func (d *notificationDispatcher) postJSON(webhook NotificationWebhookSettings, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("notification marshal failed", "format", webhook.Format, "url", redactURL(webhook.URL), "err", err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, webhook.URL, bytes.NewReader(body))
	if err != nil {
		slog.Warn("notification request build failed", "format", webhook.Format, "url", redactURL(webhook.URL), "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if webhook.Format == notificationFormatDynatrace && strings.TrimSpace(webhook.APIToken) != "" {
		req.Header.Set("Authorization", "Api-Token "+strings.TrimSpace(webhook.APIToken))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		slog.Warn("notification delivery failed", "format", webhook.Format, "url", redactURL(webhook.URL), "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		slog.Warn("notification delivery returned error status", "format", webhook.Format, "url", redactURL(webhook.URL), "status", resp.StatusCode)
	}
}
