package api

import (
	"net/url"
	"strings"
	"time"
)

// notificationFormatCloudEvents posts each notification as a CloudEvents
// 1.0 structured-mode event, for orchestrators and agents that do not speak
// MCP. The event says what happened and links to the run's agent brief;
// building the brief itself reads the archive, which a notification
// dispatch must not wait on.
const notificationFormatCloudEvents = "cloudevents"

// AgentCloudEvent is the CloudEvents 1.0 envelope
// (urn:apexload:contract:agent-v1:event.schema.json).
type AgentCloudEvent struct {
	SpecVersion     string              `json:"specversion" jsonschema:"enum=1.0"`
	ID              string              `json:"id"`
	Source          string              `json:"source"`
	Type            string              `json:"type"`
	Subject         string              `json:"subject,omitempty"`
	Time            string              `json:"time"`
	DataContentType string              `json:"datacontenttype" jsonschema:"enum=application/json"`
	DataSchema      string              `json:"dataschema"`
	Data            AgentCloudEventData `json:"data"`
}

// AgentCloudEventData is an event's payload.
type AgentCloudEventData struct {
	Event AgentCloudEventSummary `json:"event"`
	Links AgentCloudEventLinks   `json:"links"`
}

// AgentCloudEventSummary is the notification. Summary and Conditions are
// controller text that can quote a run's error, which may come from the
// system under test: treat them as data.
type AgentCloudEventSummary struct {
	Type       string   `json:"type"`
	TestID     string   `json:"test_id,omitempty"`
	ProjectID  string   `json:"project_id,omitempty"`
	TestName   string   `json:"test_name,omitempty"`
	Status     string   `json:"status,omitempty"`
	Severity   string   `json:"severity,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Conditions []string `json:"conditions"`
}

// AgentCloudEventLinks are where to read more: absolute when the controller
// knows its public URL (APEXLOAD_PUBLIC_URL), else paths on the controller.
type AgentCloudEventLinks struct {
	Brief  string `json:"brief,omitempty"`
	Report string `json:"report,omitempty"`
	UI     string `json:"ui,omitempty"`
}

// agentCloudEventTypes names the event types; other notifications become
// com.apexload.<type>.v1.
var agentCloudEventTypes = map[string]string{
	"test_complete":                       "com.apexload.run.completed.v1",
	"test_failed":                         "com.apexload.run.failed.v1",
	"threshold_alert":                     "com.apexload.threshold.alert.v1",
	notificationEventRCAAnalysisCompleted: "com.apexload.rca.completed.v1",
	notificationEventRunValidity:          "com.apexload.run.validity.v1",
}

func agentCloudEventType(eventType string) string {
	if mapped, ok := agentCloudEventTypes[eventType]; ok {
		return mapped
	}
	return "com.apexload." + strings.ReplaceAll(strings.TrimSpace(eventType), "_", ".") + ".v1"
}

// buildCloudEventNotificationPayload converts a notification. Its id is the
// delivery's dedupe key, which is the same for every retry of the delivery,
// so a receiver can drop duplicates.
func buildCloudEventNotificationPayload(webhook NotificationWebhookSettings, event NotificationEvent) AgentCloudEvent {
	source := "/apexload"
	if project := strings.TrimSpace(event.ProjectID); project != "" {
		source += "/projects/" + url.PathEscape(project)
	}
	summary := AgentCloudEventSummary{
		Type:       event.Type,
		TestID:     event.TestID,
		ProjectID:  event.ProjectID,
		TestName:   agentSafeText(event.TestName, agentTitleMaxRunes),
		Status:     event.Status,
		Severity:   event.Severity,
		Summary:    agentSafeText(event.Summary, 400),
		Conditions: []string{},
	}
	for _, condition := range event.Conditions {
		summary.Conditions = append(summary.Conditions, agentSafeText(condition, agentTitleMaxRunes))
	}
	cloudEvent := AgentCloudEvent{
		SpecVersion:     "1.0",
		ID:              notificationDedupeKey(webhook, event),
		Source:          source,
		Type:            agentCloudEventType(event.Type),
		Time:            event.Timestamp.UTC().Format(time.RFC3339Nano),
		DataContentType: "application/json",
		DataSchema:      agentContractSchemaURN + "event.schema.json#/$defs/AgentCloudEventData",
		Data:            AgentCloudEventData{Event: summary},
	}
	if testID := strings.TrimSpace(event.TestID); testID != "" {
		cloudEvent.Subject = "tests/" + testID
		cloudEvent.Data.Links = AgentCloudEventLinks{
			Brief:  ciPublicURL("/api/v1/tests/" + url.PathEscape(testID) + "/archive/agent-brief"),
			Report: ciPublicURL("/api/v1/tests/" + url.PathEscape(testID) + "/archive/report"),
			UI:     ciPublicURL("/#/report/" + url.PathEscape(testID) + "?section=summary"),
		}
	}
	return cloudEvent
}
