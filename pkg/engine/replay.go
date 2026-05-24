package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type ReplayOptions struct {
	GroupName       string
	Variables       map[string]string
	RedactionRules  []RedactionRule
	IgnoreThinkTime bool
	OnProgress      func(ReplayProgressEvent)
	DebugController ReplayDebugController
}

type ReplayProgressEvent struct {
	Type       string
	Timestamp  time.Time
	EventIndex int
	Event      *ReplayActionEvent
	Report     *ReplayReport
}

type ReplayDebugController interface {
	BeforeAction(context.Context, ReplayDebugAction) error
}

type ReplayDebugAction struct {
	Phase        string `json:"phase"`
	ActionIndex  int    `json:"action_index"`
	EventIndex   int    `json:"event_index"`
	ActionName   string `json:"action_name"`
	ActionType   string `json:"action_type"`
	Method       string `json:"method,omitempty"`
	Target       string `json:"target,omitempty"`
	Selector     string `json:"selector,omitempty"`
	SourceStepID string `json:"source_step_id,omitempty"`
}

const (
	ReplayProgressStarted  = "replay_started"
	ReplayProgressEventRun = "replay_event"
	ReplayProgressComplete = "replay_complete"
)

type ReplayReport struct {
	Group          string                    `json:"group"`
	Protocol       string                    `json:"protocol"`
	StartedAt      time.Time                 `json:"started_at"`
	CompletedAt    time.Time                 `json:"completed_at"`
	Success        bool                      `json:"success"`
	Error          string                    `json:"error,omitempty"`
	Events         []ReplayActionEvent       `json:"events"`
	FinalVariables map[string]string         `json:"final_variables,omitempty"`
	Snapshots      []*protocol.ErrorSnapshot `json:"snapshots,omitempty"`
}

type ReplayActionEvent struct {
	Phase             string                  `json:"phase"`
	ActionName        string                  `json:"action_name"`
	ActionType        string                  `json:"action_type"`
	Method            string                  `json:"method,omitempty"`
	Target            string                  `json:"target"`
	Selector          string                  `json:"selector,omitempty"`
	Value             string                  `json:"value,omitempty"`
	Headers           map[string]string       `json:"headers,omitempty"`
	Body              string                  `json:"body,omitempty"`
	StartedAt         time.Time               `json:"started_at"`
	CompletedAt       time.Time               `json:"completed_at"`
	DurationMs        float64                 `json:"duration_ms"`
	StatusCode        int                     `json:"status_code,omitempty"`
	Success           bool                    `json:"success"`
	ErrorMessage      string                  `json:"error_message,omitempty"`
	Extractions       map[string]string       `json:"extractions,omitempty"`
	PageURL           string                  `json:"page_url,omitempty"`
	PageTitle         string                  `json:"page_title,omitempty"`
	HTML              string                  `json:"html,omitempty"`
	Screenshot        string                  `json:"screenshot_base64,omitempty"`
	Console           []protocol.ConsoleEntry `json:"console_entries,omitempty"`
	Network           []protocol.NetworkEntry `json:"network_entries,omitempty"`
	DOMSummary        *protocol.DOMSummary    `json:"dom_summary,omitempty"`
	ClientMetrics     *protocol.ClientMetrics `json:"client_metrics,omitempty"`
	Snapshot          *protocol.ErrorSnapshot `json:"snapshot,omitempty"`
	SourceStepID      string                  `json:"source_step_id,omitempty"`
	SourceStepIndex   int                     `json:"source_step_index,omitempty"`
	SourceAction      string                  `json:"source_action,omitempty"`
	SourceActionName  string                  `json:"source_action_name,omitempty"`
	SourceTransaction string                  `json:"source_transaction,omitempty"`
	SourceProfile     string                  `json:"source_profile,omitempty"`
}

func ReplayScript(ctx context.Context, script *dsl.Script, opts ReplayOptions) (*ReplayReport, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}

	groupName := opts.GroupName
	if groupName == "" {
		groupName = "replay"
	}

	proto, err := protocol.Get(script.Protocol)
	if err != nil {
		return nil, fmt.Errorf("protocol init: %w", err)
	}

	if err := proto.Init(ctx, buildProtoConfig(script)); err != nil {
		return nil, fmt.Errorf("protocol init: %w", err)
	}
	defer proto.Teardown(context.Background())

	dataProvider, err := NewDataProvider(script.DataSources)
	if err != nil {
		return nil, fmt.Errorf("data source init: %w", err)
	}
	redactionConfig, err := NewRedactionConfig(opts.RedactionRules)
	if err != nil {
		return nil, fmt.Errorf("redaction config: %w", err)
	}

	report := &ReplayReport{
		Group:     groupName,
		Protocol:  script.Protocol,
		StartedAt: time.Now(),
		Success:   true,
		Events:    make([]ReplayActionEvent, 0, len(script.Init)+len(script.Actions)+len(script.Teardown)),
	}
	publishReplayProgress(opts.OnProgress, ReplayProgressEvent{
		Type:       ReplayProgressStarted,
		Timestamp:  report.StartedAt,
		EventIndex: -1,
		Report:     cloneReplayReport(report),
	})

	vu := &VUser{
		ID:              0,
		Group:           groupName,
		Proto:           proto,
		Script:          script,
		Metrics:         NewMetricsCollector(16),
		Variables:       make(map[string]string),
		DataProvider:    dataProvider,
		RedactionConfig: redactionConfig,
		SnapshotStore:   NewSnapshotStore(128),
	}
	vu.prepareRuntimeState()
	for key, value := range opts.Variables {
		vu.Variables[key] = value
	}

	continueOnError := script.Config.ErrorHandling.ContinueOnError
	globalThinkCfg := protocol.ThinkTimeConfig{
		Type:       script.Config.ThinkTime.Type,
		MeanMs:     script.Config.ThinkTime.MeanMs,
		StdMs:      script.Config.ThinkTime.StdMs,
		MinMs:      script.Config.ThinkTime.MinMs,
		MaxMs:      script.Config.ThinkTime.MaxMs,
		Multiplier: script.Config.ThinkTime.Multiplier,
		LimitMs:    script.Config.ThinkTime.LimitMs,
		Ignore:     script.Config.ThinkTime.Ignore,
	}

	shouldContinue := runReplayPhase(ctx, vu, script.Init, "init", false, opts.IgnoreThinkTime, globalThinkCfg, report, opts.OnProgress, opts.DebugController)
	if shouldContinue {
		shouldContinue = runReplayPhase(ctx, vu, script.Actions, "action", continueOnError, opts.IgnoreThinkTime, globalThinkCfg, report, opts.OnProgress, opts.DebugController)
	}
	runReplayPhase(context.Background(), vu, script.Teardown, "teardown", true, true, globalThinkCfg, report, opts.OnProgress, opts.DebugController)

	report.CompletedAt = time.Now()
	report.FinalVariables = cloneStringMap(vu.Variables)
	report.Snapshots = vu.SnapshotStore.Snapshots()
	if !shouldContinue && report.Error == "" && !report.Success {
		report.Error = "replay stopped after action failure"
	}
	publishReplayProgress(opts.OnProgress, ReplayProgressEvent{
		Type:       ReplayProgressComplete,
		Timestamp:  report.CompletedAt,
		EventIndex: -1,
		Report:     cloneReplayReport(report),
	})

	return report, nil
}

func runReplayPhase(
	ctx context.Context,
	vu *VUser,
	actions []dsl.Action,
	phase string,
	continueOnError bool,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
	report *ReplayReport,
	onProgress func(ReplayProgressEvent),
	debugController ReplayDebugController,
) bool {
	for actionIndex, action := range actions {
		if debugController != nil {
			if err := debugController.BeforeAction(ctx, replayDebugActionFor(action, phase, actionIndex, len(report.Events))); err != nil {
				report.Success = false
				if report.Error == "" {
					report.Error = err.Error()
				}
				return false
			}
		}
		event, shouldContinue := runReplayAction(ctx, vu, action, phase, ignoreThinkTime, globalThinkCfg, report)
		report.Events = append(report.Events, event)
		publishReplayProgress(onProgress, ReplayProgressEvent{
			Type:       ReplayProgressEventRun,
			Timestamp:  event.CompletedAt,
			EventIndex: len(report.Events) - 1,
			Event:      cloneReplayActionEvent(event),
		})
		if !shouldContinue {
			report.Success = false
			if report.Error == "" {
				report.Error = event.ErrorMessage
			}
			if !continueOnError {
				return false
			}
		}
	}
	return true
}

func replayDebugActionFor(action dsl.Action, phase string, actionIndex int, eventIndex int) ReplayDebugAction {
	debugAction := ReplayDebugAction{
		Phase:       phase,
		ActionIndex: actionIndex,
		EventIndex:  eventIndex,
		ActionName:  action.Name,
		ActionType:  action.Type,
		Method:      action.Method,
		Target:      action.Target,
		Selector:    action.Selector,
	}
	if len(action.Metadata) > 0 {
		debugAction.SourceStepID = sourceMetadataString(action.Metadata, "source_step_id")
	}
	return debugAction
}

func runReplayAction(
	ctx context.Context,
	vu *VUser,
	action dsl.Action,
	phase string,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
	report *ReplayReport,
) (ReplayActionEvent, bool) {
	if handled, err := vu.handleEngineAction(ctx, action); handled {
		now := time.Now()
		event := ReplayActionEvent{
			Phase:       phase,
			ActionName:  action.Name,
			ActionType:  action.Type,
			StartedAt:   now,
			CompletedAt: now,
			Success:     err == nil,
		}
		applyReplaySourceMetadata(&event, action.Metadata)
		if err != nil {
			event.ErrorMessage = err.Error()
		}
		return event, err == nil
	}
	resolved := vu.resolveAction(action)
	start := time.Now()
	result, err := vu.executeWithRetry(ctx, resolved, vu.Script.Config.ErrorHandling)
	completedAt := time.Now()

	event := ReplayActionEvent{
		Phase:       phase,
		ActionName:  action.Name,
		ActionType:  action.Type,
		Method:      resolved.Method,
		Target:      scrubReplayTarget(resolved, vu.RedactionConfig),
		Selector:    resolved.Selector,
		Value:       resolved.Value,
		Headers:     scrubReplayHeaders(resolved, vu.RedactionConfig),
		Body:        scrubReplayBody(resolved, vu.RedactionConfig),
		StartedAt:   start,
		CompletedAt: completedAt,
		DurationMs:  float64(completedAt.Sub(start).Microseconds()) / 1000,
	}
	applyReplaySourceMetadata(&event, action.Metadata)

	if err != nil {
		event.Success = false
		event.ErrorMessage = err.Error()
		if event.ErrorMessage == "" {
			event.ErrorMessage = fmt.Sprintf("%s %q failed", phase, action.Name)
		}
		return event, false
	}

	if result == nil {
		event.Success = false
		event.ErrorMessage = fmt.Sprintf("%s %q returned no result", phase, action.Name)
		return event, false
	}

	vu.recordResult(result, vu.Script.Config.ErrorHandling)
	event.StatusCode = result.StatusCode
	event.Success = result.Success
	event.ErrorMessage = result.ErrorMessage
	event.Extractions = cloneStringMap(result.Extractions)
	event.PageURL = result.PageURL
	event.PageTitle = result.PageTitle
	event.HTML = result.HTML
	event.Screenshot = result.ScreenshotBase64
	event.Console = cloneConsoleEntries(result.ConsoleEntries)
	event.Network = cloneNetworkEntries(result.NetworkEntries)
	event.DOMSummary = cloneDOMSummary(result.DOMSummary)
	event.ClientMetrics = cloneClientMetrics(result.ClientMetrics)
	event.Snapshot = result.Snapshot
	if result.Duration > 0 {
		event.DurationMs = float64(result.Duration.Microseconds()) / 1000
	}

	if !result.Success {
		if event.ErrorMessage == "" {
			event.ErrorMessage = fmt.Sprintf("status %d", result.StatusCode)
		}
		return event, false
	}

	if !ignoreThinkTime && action.ThinkTimeAfter {
		thinkCfg := globalThinkCfg
		if action.ThinkTime.Type != "" {
			thinkCfg = protocol.ThinkTimeConfig{
				Type:       action.ThinkTime.Type,
				MeanMs:     action.ThinkTime.MeanMs,
				StdMs:      action.ThinkTime.StdMs,
				MinMs:      action.ThinkTime.MinMs,
				MaxMs:      action.ThinkTime.MaxMs,
				Multiplier: action.ThinkTime.Multiplier,
				LimitMs:    action.ThinkTime.LimitMs,
				Ignore:     action.ThinkTime.Ignore,
			}
			if thinkCfg.Multiplier == 0 {
				thinkCfg.Multiplier = globalThinkCfg.Multiplier
			}
			if thinkCfg.LimitMs == 0 {
				thinkCfg.LimitMs = globalThinkCfg.LimitMs
			}
			if globalThinkCfg.Ignore {
				thinkCfg.Ignore = true
			}
		}
		applyThinkTime(thinkCfg)
	}

	return event, true
}

func scrubReplayTarget(action *protocol.Action, config *RedactionConfig) string {
	if action == nil {
		return ""
	}
	target := action.Target
	if action.RedactedTarget != "" {
		target = action.RedactedTarget
	}
	return scrubSensitiveTextWithConfig(target, config)
}

func scrubReplayHeaders(action *protocol.Action, config *RedactionConfig) map[string]string {
	if action == nil {
		return nil
	}
	if len(action.RedactedHeaders) > 0 {
		return scrubSensitiveHeaders(cloneStringMap(action.RedactedHeaders), config)
	}
	return scrubSensitiveHeaders(cloneStringMap(action.Headers), config)
}

func scrubReplayBody(action *protocol.Action, config *RedactionConfig) string {
	if action == nil {
		return ""
	}
	body := string(action.Body)
	if len(action.RedactedBody) > 0 {
		body = string(action.RedactedBody)
	}
	return scrubSensitiveTextWithConfig(body, config)
}

func applyReplaySourceMetadata(event *ReplayActionEvent, metadata map[string]any) {
	if event == nil || len(metadata) == 0 {
		return
	}
	event.SourceStepID = sourceMetadataString(metadata, "source_step_id")
	event.SourceStepIndex = sourceMetadataInt(metadata, "source_step_index")
	event.SourceAction = sourceMetadataString(metadata, "source_action")
	event.SourceActionName = sourceMetadataString(metadata, "source_action_name")
	event.SourceTransaction = sourceMetadataString(metadata, "source_transaction")
	event.SourceProfile = sourceMetadataString(metadata, "source_profile")
}

func sourceMetadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func sourceMetadataInt(metadata map[string]any, key string) int {
	value := sourceMetadataString(metadata, key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneReplayActionEvent(event ReplayActionEvent) *ReplayActionEvent {
	cloned := event
	cloned.Headers = cloneStringMap(event.Headers)
	cloned.Extractions = cloneStringMap(event.Extractions)
	cloned.Console = cloneConsoleEntries(event.Console)
	cloned.Network = cloneNetworkEntries(event.Network)
	cloned.DOMSummary = cloneDOMSummary(event.DOMSummary)
	cloned.ClientMetrics = cloneClientMetrics(event.ClientMetrics)
	if event.Snapshot != nil {
		snapshot := *event.Snapshot
		snapshot.RequestHeaders = cloneStringMap(event.Snapshot.RequestHeaders)
		snapshot.ResponseHeaders = cloneStringMap(event.Snapshot.ResponseHeaders)
		snapshot.ConsoleEntries = cloneConsoleEntries(event.Snapshot.ConsoleEntries)
		snapshot.NetworkEntries = cloneNetworkEntries(event.Snapshot.NetworkEntries)
		snapshot.DOMSummary = cloneDOMSummary(event.Snapshot.DOMSummary)
		cloned.Snapshot = &snapshot
	}
	return &cloned
}

func cloneReplayReport(report *ReplayReport) *ReplayReport {
	if report == nil {
		return nil
	}
	cloned := *report
	cloned.FinalVariables = cloneStringMap(report.FinalVariables)
	if len(report.Events) > 0 {
		cloned.Events = make([]ReplayActionEvent, len(report.Events))
		for i, event := range report.Events {
			cloned.Events[i] = *cloneReplayActionEvent(event)
		}
	}
	if len(report.Snapshots) > 0 {
		cloned.Snapshots = make([]*protocol.ErrorSnapshot, len(report.Snapshots))
		for i, snapshot := range report.Snapshots {
			if snapshot == nil {
				continue
			}
			copySnapshot := *snapshot
			copySnapshot.RequestHeaders = cloneStringMap(snapshot.RequestHeaders)
			copySnapshot.ResponseHeaders = cloneStringMap(snapshot.ResponseHeaders)
			cloned.Snapshots[i] = &copySnapshot
		}
	}
	return &cloned
}

func publishReplayProgress(callback func(ReplayProgressEvent), event ReplayProgressEvent) {
	if callback == nil {
		return
	}
	callback(event)
}

func cloneConsoleEntries(entries []protocol.ConsoleEntry) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.ConsoleEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneNetworkEntries(entries []protocol.NetworkEntry) []protocol.NetworkEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.NetworkEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneDOMSummary(summary *protocol.DOMSummary) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.VisibleText) > 0 {
		cloned.VisibleText = append([]string(nil), summary.VisibleText...)
	}
	return &cloned
}

func cloneClientMetrics(metrics *protocol.ClientMetrics) *protocol.ClientMetrics {
	if metrics == nil {
		return nil
	}
	cloned := *metrics
	return &cloned
}
