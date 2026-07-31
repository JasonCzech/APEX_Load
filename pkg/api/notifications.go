package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	notificationFormatSlack            = "slack"
	notificationFormatTeams            = "teams"
	notificationFormatDynatrace        = "dynatrace"
	notificationsAllowInsecureHTTPEnv  = "APEXLOAD_NOTIFICATIONS_ALLOW_INSECURE_HTTP"
	maxNotificationWebhooks            = 64
	maxNotificationDeliveries          = 16
	maxNotificationPerDestination      = 2
	notificationDestinationStripes     = 64
	maxNotificationNameBytes           = 256
	maxNotificationURLBytes            = 4 << 10
	maxNotificationAPITokenBytes       = 16 << 10
	maxNotificationEntitySelectorBytes = 4 << 10
	maxNotificationEvents              = 64
	maxNotificationEventBytes          = 128
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

	"threshold_alert": {},

	"fleet_alert":                {},
	"fleet_alert_acknowledged":   {},
	"fleet_alert_unacknowledged": {},
	"fleet_degraded":             {},
}

var notificationDeliveryFailuresTotal atomic.Int64
var notificationDeliveryDroppedTotal atomic.Int64

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
	client           *http.Client
	deliverySlots    chan struct{}
	destinationSlots []chan struct{}
	closeOnce        sync.Once
	closed           chan struct{}
	ctx              context.Context
	cancel           context.CancelFunc
}

func newNotificationDispatcher(client *http.Client) *notificationDispatcher {
	if client == nil {
		client = newSSRFSafeHTTPClient(5 * time.Second)
	}
	destinationSlots := make([]chan struct{}, notificationDestinationStripes)
	for i := range destinationSlots {
		destinationSlots[i] = make(chan struct{}, maxNotificationPerDestination)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &notificationDispatcher{
		client:           client,
		deliverySlots:    make(chan struct{}, maxNotificationDeliveries),
		destinationSlots: destinationSlots,
		closed:           make(chan struct{}),
		ctx:              ctx,
		cancel:           cancel,
	}
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
	if len(settings.Webhooks) > maxNotificationWebhooks {
		return fmt.Errorf("notifications.webhooks contains %d entries, limit is %d", len(settings.Webhooks), maxNotificationWebhooks)
	}
	for index, webhook := range settings.Webhooks {
		prefix := fmt.Sprintf("notifications.webhooks[%d]", index)
		if len(webhook.Name) > maxNotificationNameBytes {
			return fmt.Errorf("%s.name exceeds %d bytes", prefix, maxNotificationNameBytes)
		}
		if len(webhook.URL) > maxNotificationURLBytes {
			return fmt.Errorf("%s.url exceeds %d bytes", prefix, maxNotificationURLBytes)
		}
		if len(webhook.APIToken) > maxNotificationAPITokenBytes {
			return fmt.Errorf("%s.api_token exceeds %d bytes", prefix, maxNotificationAPITokenBytes)
		}
		if len(webhook.EntitySelector) > maxNotificationEntitySelectorBytes {
			return fmt.Errorf("%s.entity_selector exceeds %d bytes", prefix, maxNotificationEntitySelectorBytes)
		}
		if len(webhook.Events) > maxNotificationEvents {
			return fmt.Errorf("%s.events contains %d entries, limit is %d", prefix, len(webhook.Events), maxNotificationEvents)
		}
		parsedURL, err := url.ParseRequestURI(webhook.URL)
		if err != nil || parsedURL == nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return fmt.Errorf("%s.url must be a valid http or https URL", prefix)
		}
		if err := validateOutboundURL(webhook.URL); err != nil {
			return fmt.Errorf("%s.url is not an allowed destination: %w", prefix, err)
		}
		if err := validateCredentialTransportURL(prefix+".url", webhook.URL, notificationsAllowInsecureHTTPEnv); err != nil {
			return err
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
			if len(eventType) > maxNotificationEventBytes {
				return fmt.Errorf("%s.events contains a value exceeding %d bytes", prefix, maxNotificationEventBytes)
			}
			if _, ok := supportedNotificationEvents[eventType]; !ok {
				return fmt.Errorf("%s.events contains unsupported event %q", prefix, eventType)
			}
		}
	}
	return nil
}

// Dispatch submits an event to every enabled matching webhook and reports
// whether at least one delivery was admitted by the bounded executor.
func (d *notificationDispatcher) Dispatch(settings NotificationsSettings, event NotificationEvent) bool {
	if d == nil {
		return false
	}
	admitted := false
	for _, webhook := range settings.Webhooks {
		if !webhook.Enabled || !notificationWebhookMatchesEvent(webhook, event.Type) {
			continue
		}
		payload, err := buildNotificationPayloadForWebhook(webhook, event)
		if err != nil {
			slog.Warn("notification payload build failed", "event", event.Type, "format", webhook.Format, "err", err)
			continue
		}
		if !d.tryDispatch(webhook, payload, notificationDedupeKey(webhook, event)) {
			notificationDeliveryDroppedTotal.Add(1)
			slog.Warn("notification delivery dropped by bounded dispatcher", "event", event.Type, "webhook", webhook.Name)
			continue
		}
		admitted = true
	}
	return admitted
}

// tryDispatch is a zero-backlog bounded executor. It admits at most a fixed
// number of deliveries process-wide per dispatcher and a smaller fixed number
// per destination stripe. Saturation is observable and drops the new delivery
// instead of retaining an unbounded goroutine/queue while a webhook is slow.
func (d *notificationDispatcher) tryDispatch(webhook NotificationWebhookSettings, payload any, dedupeKey string) bool {
	if d == nil || d.deliverySlots == nil || len(d.destinationSlots) == 0 {
		return false
	}
	select {
	case <-d.closed:
		return false
	default:
	}
	select {
	case d.deliverySlots <- struct{}{}:
	default:
		return false
	}
	destination := d.destinationSlots[notificationDestinationStripe(webhook.URL, len(d.destinationSlots))]
	select {
	case destination <- struct{}{}:
	case <-d.closed:
		<-d.deliverySlots
		return false
	default:
		<-d.deliverySlots
		return false
	}
	go func() {
		defer func() {
			<-destination
			<-d.deliverySlots
		}()
		d.postJSON(webhook, payload, dedupeKey)
	}()
	return true
}

func notificationDestinationStripe(rawURL string, stripes int) int {
	if stripes <= 1 {
		return 0
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.TrimSpace(rawURL)))
	return int(hash.Sum32() % uint32(stripes))
}

// Close prevents new deliveries. No persistent workers are retained: admitted
// goroutines are bounded and terminate under the HTTP client's finite timeout.
func (d *notificationDispatcher) Close() {
	if d == nil || d.closed == nil {
		return
	}
	d.closeOnce.Do(func() {
		close(d.closed)
		if d.cancel != nil {
			d.cancel()
		}
	})
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
	if event.Type == "test_failed" || event.Type == "telemetry_breached" ||
		((event.Type == "fleet_alert" || event.Type == "threshold_alert") && strings.EqualFold(event.Severity, "danger")) {
		eventType = "ERROR_EVENT"
	} else if event.Type == "telemetry_degraded" || event.Type == "fleet_alert" || event.Type == "fleet_degraded" || event.Type == "threshold_alert" {
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
	properties["apexload.idempotency_key"] = notificationDedupeKey(webhook, event)
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

func notificationDedupeKey(webhook NotificationWebhookSettings, event NotificationEvent) string {
	parts := []string{
		strings.TrimSpace(webhook.Name),
		strings.TrimSpace(webhook.URL),
		strings.TrimSpace(event.Type),
		strings.TrimSpace(event.TestID),
		// Status disambiguates events sharing Type+TestID+Timestamp: a single
		// threshold_alert routing pass can emit several breaches (distinct
		// metrics or transactions) stamped with the same instant, and each must
		// keep its own idempotency key for receiver-side dedupe.
		strings.TrimSpace(event.Status),
		strings.TrimSpace(event.Summary),
		event.Timestamp.UTC().Format(time.RFC3339Nano),
	}
	// The key is sent as an HTTP header and a Dynatrace property. Hash the
	// canonical identity so user-influenced names/summaries cannot inject
	// control bytes, exceed intermediary header limits, or disclose webhook
	// configuration while still preserving event-level uniqueness.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
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

func (d *notificationDispatcher) postJSON(webhook NotificationWebhookSettings, payload any, dedupeKey string) {
	if err := validateCredentialTransportURL("notification webhook URL", webhook.URL, notificationsAllowInsecureHTTPEnv); err != nil {
		slog.Warn("notification delivery rejected", "format", webhook.Format, "url", redactURL(webhook.URL), "err", err)
		notificationDeliveryFailuresTotal.Add(1)
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("notification marshal failed", "format", webhook.Format, "url", redactURL(webhook.URL), "err", err)
		notificationDeliveryFailuresTotal.Add(1)
		return
	}
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx := d.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(body))
		if err != nil {
			slog.Warn("notification request build failed", "format", webhook.Format, "url", redactURL(webhook.URL), "err", err)
			notificationDeliveryFailuresTotal.Add(1)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if dedupeKey != "" {
			req.Header.Set("X-APEXLoad-Notification-Key", dedupeKey)
		}
		if webhook.Format == notificationFormatDynatrace && strings.TrimSpace(webhook.APIToken) != "" {
			req.Header.Set("Authorization", "Api-Token "+strings.TrimSpace(webhook.APIToken))
		}
		resp, err := d.client.Do(req)
		if err == nil && resp != nil && resp.StatusCode < http.StatusBadRequest {
			_ = resp.Body.Close()
			return
		}
		retryable := err != nil || (resp != nil && retryableNotificationStatus(resp.StatusCode))
		if !retryable || attempt == maxAttempts {
			if err != nil {
				slog.Warn("notification delivery failed", "format", webhook.Format, "url", redactURL(webhook.URL), "attempt", attempt, "err", err)
			} else if resp != nil {
				slog.Warn("notification delivery returned error status", "format", webhook.Format, "url", redactURL(webhook.URL), "attempt", attempt, "status", resp.StatusCode)
			}
			notificationDeliveryFailuresTotal.Add(1)
			if resp != nil {
				_ = resp.Body.Close()
			}
			return
		}
		delay := notificationRetryDelay(resp, attempt)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if d.ctx == nil {
			time.Sleep(delay)
			continue
		}
		select {
		case <-time.After(delay):
		case <-d.ctx.Done():
			return
		}
	}
}

func retryableNotificationStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func notificationRetryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After")); retryAfter != "" {
			if seconds, err := time.ParseDuration(retryAfter + "s"); err == nil && seconds > 0 {
				return minDuration(seconds, 2*time.Second)
			}
			if when, err := http.ParseTime(retryAfter); err == nil {
				delay := time.Until(when)
				if delay > 0 {
					return minDuration(delay, 2*time.Second)
				}
			}
		}
	}
	base := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
	jitter := time.Duration(time.Now().UnixNano()%50) * time.Millisecond
	return base + jitter
}

func notificationDeliveryFailures() int64 {
	return notificationDeliveryFailuresTotal.Load()
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
