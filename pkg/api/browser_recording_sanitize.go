package api

import (
	"strings"

	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/selector"
)

// sanitizeBrowserRecordingSessionForResponse returns a disclosure-safe API
// projection for read-only users. The recorder keeps the raw artifact in
// memory for conversion/replay, while viewer responses omit screenshot pixels
// and redact credentials from typed values, URLs, selectors, notes, and
// diagnostics.
func sanitizeBrowserRecordingSessionForResponse(session *BrowserRecordingSession) *BrowserRecordingSession {
	if session == nil {
		return nil
	}
	policy := scrub.Policy{}
	clone := &BrowserRecordingSession{
		ID:                   session.ID,
		Name:                 scrub.Value(session.Name, policy),
		ProjectID:            session.ProjectID,
		Status:               session.Status,
		StartURL:             scrub.ValueURL(session.StartURL, policy),
		CaptureHAR:           session.CaptureHAR,
		StartedAt:            session.StartedAt,
		StopReason:           session.StopReason,
		Error:                scrub.Value(session.Error, policy),
		CurrentURL:           scrub.ValueURL(session.CurrentURL, policy),
		StepCount:            session.StepCount,
		NavigationCount:      session.NavigationCount,
		NetworkRequestCount:  session.NetworkRequestCount,
		WebSocketCount:       session.WebSocketCount,
		WarningCount:         session.WarningCount,
		MarkerCount:          session.MarkerCount,
		AnnotationCount:      session.AnnotationCount,
		CapHits:              cloneStringInt64Map(session.CapHits),
		OpenTransactions:     sanitizeBrowserRecordingStrings(session.OpenTransactions, policy),
		Section:              session.Section,
		DeviceProfile:        session.DeviceProfile,
		RecordingEnvironment: recbrowser.NormalizeRecordingEnvironment(session.RecordingEnvironment, ""),
		Picking:              session.Picking,
		HARAvailable:         session.HARAvailable,
		StorageStateReady:    session.StorageStateReady,
	}
	if session.StoppedAt != nil {
		stoppedAt := *session.StoppedAt
		clone.StoppedAt = &stoppedAt
	}
	if len(session.Steps) > 0 {
		clone.Steps = make([]recbrowser.Step, len(session.Steps))
		for index, step := range session.Steps {
			clone.Steps[index] = sanitizeBrowserRecordingStep(step, policy)
		}
	}
	if len(session.Markers) > 0 {
		clone.Markers = make([]recbrowser.Marker, len(session.Markers))
		copy(clone.Markers, session.Markers)
		for index := range clone.Markers {
			clone.Markers[index].Name = scrub.Value(clone.Markers[index].Name, policy)
			clone.Markers[index].Text = scrub.Value(clone.Markers[index].Text, policy)
		}
	}
	if len(session.Annotations) > 0 {
		clone.Annotations = make([]recbrowser.Annotation, len(session.Annotations))
		copy(clone.Annotations, session.Annotations)
		for index := range clone.Annotations {
			clone.Annotations[index].Text = scrub.Value(clone.Annotations[index].Text, policy)
			clone.Annotations[index].URL = scrub.ValueURL(clone.Annotations[index].URL, policy)
		}
	}
	clone.Warnings = sanitizeBrowserRecordingStrings(session.Warnings, policy)
	return clone
}

func sanitizeBrowserRecordingStep(step recbrowser.Step, policy scrub.Policy) recbrowser.Step {
	secretValue := step.Masked || step.MarkSecret || browserRecordingElementSensitive(step.Element)
	if secretValue && step.Value != "" {
		step.Value = scrub.Redacted
	} else {
		step.Value = scrub.Value(step.Value, policy)
	}
	if len(step.Values) > 0 {
		step.Values = append([]string(nil), step.Values...)
		for index := range step.Values {
			if secretValue && step.Values[index] != "" {
				step.Values[index] = scrub.Redacted
			} else {
				step.Values[index] = scrub.Value(step.Values[index], policy)
			}
		}
	}
	step.Modifiers = sanitizeBrowserRecordingStrings(step.Modifiers, policy)
	step.URL = scrub.ValueURL(step.URL, policy)
	step.Title = scrub.Value(step.Title, policy)
	step.FrameSelector = scrub.Value(step.FrameSelector, policy)
	step.FramePath = sanitizeBrowserRecordingStrings(step.FramePath, policy)
	step.Element = sanitizeBrowserRecordingElement(step.Element, policy)
	step.Candidates = sanitizeBrowserRecordingCandidates(step.Candidates, policy)
	step.TargetElement = sanitizeBrowserRecordingElement(step.TargetElement, policy)
	step.TargetSelector = scrub.Value(step.TargetSelector, policy)
	step.TargetCandidates = sanitizeBrowserRecordingCandidates(step.TargetCandidates, policy)
	// Screenshots can contain arbitrary credentials/PII and cannot be safely
	// text-scrubbed. They remain available only through an authorized raw view.
	step.Thumbnail = ""
	step.DialogMessage = scrub.Value(step.DialogMessage, policy)
	return step
}

func browserRecordingElementSensitive(element *selector.ElementMetadata) bool {
	if element == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(element.Type), "password") {
		return true
	}
	return scrub.SensitiveName(element.Name) || scrub.SensitiveName(element.ID) || scrub.SensitiveName(element.AriaLabel)
}

func sanitizeBrowserRecordingElement(element *selector.ElementMetadata, policy scrub.Policy) *selector.ElementMetadata {
	if element == nil {
		return nil
	}
	clone := *element
	clone.ID = scrub.Value(clone.ID, policy)
	clone.Name = scrub.Value(clone.Name, policy)
	clone.AriaLabel = scrub.Value(clone.AriaLabel, policy)
	clone.DataTestID = scrub.Value(clone.DataTestID, policy)
	clone.Placeholder = scrub.Value(clone.Placeholder, policy)
	clone.Text = scrub.Value(clone.Text, policy)
	clone.Classes = sanitizeBrowserRecordingStrings(clone.Classes, policy)
	clone.CSSPath = scrub.Value(clone.CSSPath, policy)
	clone.XPath = scrub.Value(clone.XPath, policy)
	clone.FramePath = sanitizeBrowserRecordingStrings(clone.FramePath, policy)
	if len(element.Attributes) > 0 {
		clone.Attributes = make(map[string]string, len(element.Attributes))
		for key, value := range element.Attributes {
			clone.Attributes[key] = scrub.NamedValue(key, value, policy)
		}
	}
	return &clone
}

func sanitizeBrowserRecordingCandidates(candidates []selector.Candidate, policy scrub.Policy) []selector.Candidate {
	if len(candidates) == 0 {
		return nil
	}
	clone := append([]selector.Candidate(nil), candidates...)
	for index := range clone {
		clone[index].Value = scrub.Value(clone[index].Value, policy)
		clone[index].Reason = scrub.Value(clone[index].Reason, policy)
	}
	return clone
}

func sanitizeBrowserRecordingStrings(values []string, policy scrub.Policy) []string {
	if len(values) == 0 {
		return nil
	}
	clone := append([]string(nil), values...)
	for index := range clone {
		clone[index] = scrub.Value(clone[index], policy)
	}
	return clone
}

func sanitizeBrowserRecordingBroadcastExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	policy := scrub.Policy{}
	clone := make(map[string]any, len(extra))
	for key, value := range extra {
		switch typed := value.(type) {
		case *recbrowser.Step:
			if typed != nil {
				step := sanitizeBrowserRecordingStep(*typed, policy)
				clone[key] = &step
			}
		case recbrowser.Step:
			clone[key] = sanitizeBrowserRecordingStep(typed, policy)
		case *recbrowser.NavEvent:
			if typed != nil {
				nav := *typed
				nav.URL = scrub.ValueURL(nav.URL, policy)
				clone[key] = &nav
			}
		case *recbrowser.Marker:
			if typed != nil {
				marker := *typed
				marker.Name = scrub.Value(marker.Name, policy)
				marker.Text = scrub.Value(marker.Text, policy)
				clone[key] = &marker
			}
		case *recbrowser.Annotation:
			if typed != nil {
				annotation := *typed
				annotation.Text = scrub.Value(annotation.Text, policy)
				annotation.URL = scrub.ValueURL(annotation.URL, policy)
				clone[key] = &annotation
			}
		case string:
			if strings.Contains(strings.ToLower(key), "url") {
				clone[key] = scrub.ValueURL(typed, policy)
			} else {
				clone[key] = scrub.Value(typed, policy)
			}
		default:
			clone[key] = scrub.Structured(typed, policy)
		}
	}
	return clone
}
