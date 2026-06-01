package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type ReplayOptions struct {
	GroupName       string
	ReplaySessionID string
	ScriptRevision  string
	RuntimeProfile  *ReplayRuntimeProfile
	Variables       map[string]string
	RedactionRules  []RedactionRule
	IgnoreThinkTime bool
	OnProgress      func(ReplayProgressEvent)
	DebugController ReplayDebugController
}

type ReplayProgressEvent struct {
	Type       string
	EventID    string
	SessionID  string
	Timestamp  time.Time
	EventIndex int
	Action     *ReplayDebugAction
	Event      *ReplayActionEvent
	Report     *ReplayReport
}

type ReplayDebugController interface {
	BeforeAction(context.Context, ReplayDebugAction) error
}

type ReplayDebugAction struct {
	EventID      string `json:"event_id,omitempty"`
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
	ReplayProgressStarted         = "replay_started"
	ReplayProgressActionStarted   = "action_started"
	ReplayProgressActionCompleted = "action_completed"
	ReplayProgressActionFailed    = "action_failed"
	ReplayProgressEventRun        = "replay_event"
	ReplayProgressComplete        = "replay_complete"
)

type ReplayReport struct {
	ReplaySessionID   string                    `json:"replay_session_id,omitempty"`
	ScriptHash        string                    `json:"script_hash,omitempty"`
	ScriptRevision    string                    `json:"script_revision,omitempty"`
	RuntimeProfile    ReplayRuntimeProfile      `json:"runtime_profile,omitempty"`
	ArtifactManifest  *ReplayArtifactManifest   `json:"artifact_manifest,omitempty"`
	DiagnosisSummary  *ReplayDiagnosisSummary   `json:"diagnosis_summary,omitempty"`
	RepairSuggestions []ReplayRepairSuggestion  `json:"repair_suggestions,omitempty"`
	Group             string                    `json:"group"`
	Protocol          string                    `json:"protocol"`
	StartedAt         time.Time                 `json:"started_at"`
	CompletedAt       time.Time                 `json:"completed_at"`
	Success           bool                      `json:"success"`
	Error             string                    `json:"error,omitempty"`
	Events            []ReplayActionEvent       `json:"events"`
	FinalVariables    map[string]string         `json:"final_variables,omitempty"`
	Snapshots         []*protocol.ErrorSnapshot `json:"snapshots,omitempty"`
}

type ReplayActionEvent struct {
	EventID           string                   `json:"event_id,omitempty"`
	ActionIndex       int                      `json:"action_index,omitempty"`
	EventIndex        int                      `json:"event_index,omitempty"`
	Phase             string                   `json:"phase"`
	ActionName        string                   `json:"action_name"`
	ActionType        string                   `json:"action_type"`
	Method            string                   `json:"method,omitempty"`
	Target            string                   `json:"target"`
	Selector          string                   `json:"selector,omitempty"`
	Value             string                   `json:"value,omitempty"`
	Headers           map[string]string        `json:"headers,omitempty"`
	Body              string                   `json:"body,omitempty"`
	StartedAt         time.Time                `json:"started_at"`
	CompletedAt       time.Time                `json:"completed_at"`
	DurationMs        float64                  `json:"duration_ms"`
	StatusCode        int                      `json:"status_code,omitempty"`
	Success           bool                     `json:"success"`
	ErrorMessage      string                   `json:"error_message,omitempty"`
	Extractions       map[string]string        `json:"extractions,omitempty"`
	PageURL           string                   `json:"page_url,omitempty"`
	PageTitle         string                   `json:"page_title,omitempty"`
	HTML              string                   `json:"html,omitempty"`
	Screenshot        string                   `json:"screenshot_base64,omitempty"`
	Console           []protocol.ConsoleEntry  `json:"console_entries,omitempty"`
	Network           []protocol.NetworkEntry  `json:"network_entries,omitempty"`
	DOMSummary        *protocol.DOMSummary     `json:"dom_summary,omitempty"`
	ClientMetrics     *protocol.ClientMetrics  `json:"client_metrics,omitempty"`
	ProtocolEvidence  map[string]any           `json:"protocol_evidence,omitempty"`
	Snapshot          *protocol.ErrorSnapshot  `json:"snapshot,omitempty"`
	SourceStepID      string                   `json:"source_step_id,omitempty"`
	SourceStepIndex   int                      `json:"source_step_index,omitempty"`
	SourceAction      string                   `json:"source_action,omitempty"`
	SourceActionName  string                   `json:"source_action_name,omitempty"`
	SourceTransaction string                   `json:"source_transaction,omitempty"`
	SourceProfile     string                   `json:"source_profile,omitempty"`
	Diagnosis         *ReplayDiagnosis         `json:"diagnosis,omitempty"`
	RepairSuggestions []ReplayRepairSuggestion `json:"repair_suggestions,omitempty"`
}

type ReplayRuntimeProfile struct {
	Name            string `json:"name,omitempty"`
	CaptureMode     string `json:"capture_mode,omitempty"`
	LogLevel        string `json:"log_level,omitempty"`
	IgnoreThinkTime bool   `json:"ignore_think_time,omitempty"`
	BrowserEngine   string `json:"browser_engine,omitempty"`
	DeviceProfile   string `json:"device_profile,omitempty"`
	Headless        *bool  `json:"headless,omitempty"`
	FailurePolicy   string `json:"failure_policy,omitempty"`
}

type ReplayArtifactManifest struct {
	ReplayID          string               `json:"replay_id,omitempty"`
	ScriptHash        string               `json:"script_hash,omitempty"`
	ScriptRevision    string               `json:"script_revision,omitempty"`
	CreatedAt         time.Time            `json:"created_at,omitempty"`
	RuntimeProfile    ReplayRuntimeProfile `json:"runtime_profile,omitempty"`
	DataSources       []string             `json:"data_sources,omitempty"`
	SeedVariables     []string             `json:"seed_variables,omitempty"`
	Screenshots       int                  `json:"screenshots"`
	NetworkEntries    int                  `json:"network_entries"`
	ConsoleEntries    int                  `json:"console_entries"`
	Snapshots         int                  `json:"snapshots"`
	ProtocolArtifacts map[string]int       `json:"protocol_artifacts,omitempty"`
	HTMLCaptured      bool                 `json:"html_captured,omitempty"`
	BrowserEvidence   bool                 `json:"browser_evidence,omitempty"`
}

type ReplayDiagnosis struct {
	Category         string   `json:"category"`
	Severity         string   `json:"severity"`
	Cause            string   `json:"cause"`
	Evidence         string   `json:"evidence,omitempty"`
	Action           string   `json:"action,omitempty"`
	Signals          []string `json:"signals,omitempty"`
	FrameContext     string   `json:"frame_context,omitempty"`
	SuggestedLocator string   `json:"suggested_locator,omitempty"`
}

type ReplayRepairSuggestion struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Description    string            `json:"description"`
	DraftPatchType string            `json:"draft_patch_type"`
	Action         string            `json:"action,omitempty"`
	Confidence     float64           `json:"confidence,omitempty"`
	Fields         map[string]string `json:"fields,omitempty"`
}

type ReplayDiagnosisSummary struct {
	FailureCount        int            `json:"failure_count"`
	Categories          map[string]int `json:"categories,omitempty"`
	FirstFailureEventID string         `json:"first_failure_event_id,omitempty"`
	FirstFailureAction  string         `json:"first_failure_action,omitempty"`
	FirstFailureCause   string         `json:"first_failure_cause,omitempty"`
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

	startedAt := time.Now()
	replaySessionID := normalizeReplaySessionID(opts.ReplaySessionID, startedAt)
	runtimeProfile := buildReplayRuntimeProfile(script, opts)
	scriptHash := replayScriptHash(script)
	report := &ReplayReport{
		ReplaySessionID:  replaySessionID,
		ScriptHash:       scriptHash,
		ScriptRevision:   opts.ScriptRevision,
		RuntimeProfile:   runtimeProfile,
		ArtifactManifest: buildReplayArtifactManifest(replaySessionID, scriptHash, opts.ScriptRevision, runtimeProfile, script, opts.Variables, nil, startedAt),
		Group:            groupName,
		Protocol:         script.Protocol,
		StartedAt:        startedAt,
		Success:          true,
		Events:           make([]ReplayActionEvent, 0, len(script.Init)+len(script.Actions)+len(script.Teardown)),
	}
	publishReplayProgress(opts.OnProgress, ReplayProgressEvent{
		EventID:    replaySessionEventID(replaySessionID, "started"),
		SessionID:  replaySessionID,
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
	report.DiagnosisSummary = buildReplayDiagnosisSummary(report.Events)
	report.RepairSuggestions = collectReplayRepairSuggestions(report.Events)
	report.ArtifactManifest = buildReplayArtifactManifest(replaySessionID, scriptHash, opts.ScriptRevision, runtimeProfile, script, opts.Variables, report, report.CompletedAt)
	if !shouldContinue && report.Error == "" && !report.Success {
		report.Error = "replay stopped after action failure"
	}
	publishReplayProgress(opts.OnProgress, ReplayProgressEvent{
		EventID:    replaySessionEventID(replaySessionID, "completed"),
		SessionID:  replaySessionID,
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
		eventIndex := len(report.Events)
		eventID := replayActionEventID(report.ReplaySessionID, phase, actionIndex, eventIndex)
		debugAction := replayDebugActionFor(action, phase, actionIndex, eventIndex)
		debugAction.EventID = eventID
		if debugController != nil {
			if err := debugController.BeforeAction(ctx, debugAction); err != nil {
				report.Success = false
				if report.Error == "" {
					report.Error = err.Error()
				}
				return false
			}
		}
		publishReplayProgress(onProgress, ReplayProgressEvent{
			Type:       ReplayProgressActionStarted,
			EventID:    eventID,
			SessionID:  report.ReplaySessionID,
			Timestamp:  time.Now(),
			EventIndex: eventIndex,
			Action:     cloneReplayDebugAction(debugAction),
		})
		event, shouldContinue := runReplayAction(ctx, vu, action, phase, actionIndex, eventIndex, eventID, ignoreThinkTime, globalThinkCfg, report)
		report.Events = append(report.Events, event)
		actionProgressType := ReplayProgressActionCompleted
		if !event.Success {
			actionProgressType = ReplayProgressActionFailed
		}
		publishReplayProgress(onProgress, ReplayProgressEvent{
			Type:       actionProgressType,
			EventID:    eventID,
			SessionID:  report.ReplaySessionID,
			Timestamp:  event.CompletedAt,
			EventIndex: len(report.Events) - 1,
			Action:     cloneReplayDebugAction(debugAction),
			Event:      cloneReplayActionEvent(event),
		})
		publishReplayProgress(onProgress, ReplayProgressEvent{
			Type:       ReplayProgressEventRun,
			EventID:    eventID,
			SessionID:  report.ReplaySessionID,
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
	actionIndex int,
	eventIndex int,
	eventID string,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
	report *ReplayReport,
) (ReplayActionEvent, bool) {
	if handled, err := vu.handleEngineAction(ctx, action); handled {
		now := time.Now()
		event := ReplayActionEvent{
			EventID:     eventID,
			ActionIndex: actionIndex,
			EventIndex:  eventIndex,
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
			attachReplayDiagnosis(&event)
		}
		return event, err == nil
	}
	resolved := vu.resolveAction(action)
	start := time.Now()
	result, err := vu.executeWithRetry(ctx, resolved, vu.Script.Config.ErrorHandling)
	completedAt := time.Now()

	event := ReplayActionEvent{
		EventID:     eventID,
		ActionIndex: actionIndex,
		EventIndex:  eventIndex,
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
		event.ProtocolEvidence = protocol.BuildProtocolEvidence(resolved, &protocol.Result{
			ActionName:     action.Name,
			StartTime:      start,
			Duration:       completedAt.Sub(start),
			Success:        false,
			ErrorMessage:   event.ErrorMessage,
			DependencyType: inferReplayDependencyType(resolved),
		})
		attachReplayDiagnosis(&event)
		return event, false
	}

	if result == nil {
		event.Success = false
		event.ErrorMessage = fmt.Sprintf("%s %q returned no result", phase, action.Name)
		attachReplayDiagnosis(&event)
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
	event.ProtocolEvidence = cloneAnyMap(result.ProtocolEvidence)
	if len(event.ProtocolEvidence) == 0 {
		event.ProtocolEvidence = protocol.BuildProtocolEvidence(resolved, result)
	}
	event.Snapshot = result.Snapshot
	if result.Duration > 0 {
		event.DurationMs = float64(result.Duration.Microseconds()) / 1000
	}

	if !result.Success {
		if event.ErrorMessage == "" {
			event.ErrorMessage = fmt.Sprintf("status %d", result.StatusCode)
		}
		attachReplayDiagnosis(&event)
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

func normalizeReplaySessionID(input string, startedAt time.Time) string {
	if trimmed := strings.TrimSpace(input); trimmed != "" {
		return trimmed
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return fmt.Sprintf("replay-%d", startedAt.UnixNano())
}

func replaySessionEventID(sessionID string, eventName string) string {
	return strings.Join([]string{sanitizeReplayEventIDPart(sessionID), "session", sanitizeReplayEventIDPart(eventName)}, ":")
}

func replayActionEventID(sessionID string, phase string, actionIndex int, eventIndex int) string {
	return fmt.Sprintf("%s:%s:%03d:%03d", sanitizeReplayEventIDPart(sessionID), sanitizeReplayEventIDPart(phase), actionIndex, eventIndex)
}

func sanitizeReplayEventIDPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "replay"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	normalized := strings.Trim(b.String(), "-")
	if normalized == "" {
		return "replay"
	}
	return normalized
}

func replayScriptHash(script *dsl.Script) string {
	if script == nil {
		return ""
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func buildReplayRuntimeProfile(script *dsl.Script, opts ReplayOptions) ReplayRuntimeProfile {
	profile := ReplayRuntimeProfile{}
	if opts.RuntimeProfile != nil {
		profile = *cloneReplayRuntimeProfile(opts.RuntimeProfile)
	}
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = "debug trace"
	}
	if strings.TrimSpace(profile.CaptureMode) == "" {
		profile.CaptureMode = "diagnostic"
	}
	if strings.TrimSpace(profile.LogLevel) == "" {
		profile.LogLevel = "info"
	}
	profile.IgnoreThinkTime = opts.IgnoreThinkTime
	if script != nil {
		if strings.TrimSpace(profile.BrowserEngine) == "" {
			profile.BrowserEngine = strings.TrimSpace(script.Config.Browser.Engine)
		}
		if strings.TrimSpace(profile.DeviceProfile) == "" {
			profile.DeviceProfile = strings.TrimSpace(script.Config.Browser.DeviceProfile)
		}
		if profile.Headless == nil && script.Config.Browser.Headless != nil {
			headless := *script.Config.Browser.Headless
			profile.Headless = &headless
		}
		if strings.TrimSpace(profile.FailurePolicy) == "" {
			if script.Config.ErrorHandling.ContinueOnError {
				profile.FailurePolicy = "continue_on_error"
			} else {
				profile.FailurePolicy = "stop_on_error"
			}
		}
	}
	return profile
}

func buildReplayArtifactManifest(
	replayID string,
	scriptHash string,
	scriptRevision string,
	runtimeProfile ReplayRuntimeProfile,
	script *dsl.Script,
	seedVariables map[string]string,
	report *ReplayReport,
	createdAt time.Time,
) *ReplayArtifactManifest {
	manifest := &ReplayArtifactManifest{
		ReplayID:       replayID,
		ScriptHash:     scriptHash,
		ScriptRevision: scriptRevision,
		CreatedAt:      createdAt,
		RuntimeProfile: runtimeProfile,
		SeedVariables:  sortedStringMapKeys(seedVariables),
	}
	if script != nil {
		manifest.DataSources = replayDataSourceNames(script.DataSources)
	}
	if report == nil {
		return manifest
	}
	manifest.Snapshots = len(report.Snapshots)
	for _, event := range report.Events {
		if event.Screenshot != "" || event.Snapshot != nil && event.Snapshot.ScreenshotBase64 != "" {
			manifest.Screenshots++
		}
		manifest.NetworkEntries += len(event.Network)
		manifest.ConsoleEntries += len(event.Console)
		if event.Snapshot != nil {
			manifest.NetworkEntries += len(event.Snapshot.NetworkEntries)
			manifest.ConsoleEntries += len(event.Snapshot.ConsoleEntries)
		}
		if event.HTML != "" || event.Snapshot != nil && event.Snapshot.HTML != "" {
			manifest.HTMLCaptured = true
		}
		if event.PageURL != "" || event.PageTitle != "" || event.DOMSummary != nil || len(event.Console) > 0 || len(event.Network) > 0 || event.Screenshot != "" {
			manifest.BrowserEvidence = true
		}
		if protocolName, ok := event.ProtocolEvidence["protocol"].(string); ok && strings.TrimSpace(protocolName) != "" {
			if manifest.ProtocolArtifacts == nil {
				manifest.ProtocolArtifacts = make(map[string]int)
			}
			manifest.ProtocolArtifacts[protocolName]++
		}
	}
	return manifest
}

func replayDataSourceNames(sources []dsl.DataSource) []string {
	if len(sources) == 0 {
		return nil
	}
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		name := strings.TrimSpace(source.Name)
		if name == "" {
			name = strings.TrimSpace(source.Path)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func sortedStringMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	sort.Strings(keys)
	return keys
}

func attachReplayDiagnosis(event *ReplayActionEvent) {
	if event == nil || event.Success {
		return
	}
	diagnosis := classifyReplayFailure(*event)
	event.Diagnosis = &diagnosis
	event.RepairSuggestions = replayRepairSuggestionsForDiagnosis(*event, diagnosis)
}

func classifyReplayFailure(event ReplayActionEvent) ReplayDiagnosis {
	errorText := strings.TrimSpace(event.ErrorMessage)
	statusText := ""
	if event.StatusCode != 0 {
		statusText = fmt.Sprintf("HTTP status %d", event.StatusCode)
	}
	cause := errorText
	if cause == "" {
		cause = statusText
	}
	if cause == "" {
		cause = "Replay action failed without a protocol error message"
	}

	lower := strings.ToLower(strings.Join([]string{
		event.ActionType,
		event.Method,
		event.Target,
		event.Selector,
		event.Body,
		errorText,
		statusText,
	}, " "))
	isBrowserAction := strings.HasPrefix(strings.ToLower(event.ActionType), "browser_") || event.Selector != "" || event.PageURL != "" || event.HTML != ""

	category := "runtime_environment"
	signals := make([]string, 0, 3)
	addSignal := func(signal string) {
		if signal != "" {
			signals = append(signals, signal)
		}
	}

	switch {
	case isBrowserAction && containsAny(lower, "selector", "locator", "strict mode", "no element", "not found", "not visible", "not enabled", "detached", "element"):
		category = "selector"
		addSignal("browser_selector")
	case isBrowserAction && containsAny(lower, "timeout", "timed out", "wait", "navigation"):
		category = "wait_timing"
		addSignal("browser_wait")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "ws_") && containsAny(lower, "handshake", "bad status", "upgrade"):
		category = "websocket_handshake"
		addSignal("websocket_handshake")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "ws_") && containsAny(lower, "timeout", "deadline", "message does not contain"):
		category = "websocket_receive_timeout"
		addSignal("websocket_message")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "grpc_") && containsAny(lower, "reflection", "proto", "descriptor", "method", "service"):
		category = "grpc_contract"
		addSignal("grpc_contract")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "grpc_") && containsAny(lower, "code =", "status", "rpc error", "metadata", "authority", "deadline"):
		category = "grpc_status"
		addSignal("grpc_status")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "db_") || strings.HasPrefix(strings.ToLower(event.ActionType), "database_"):
		switch {
		case containsAny(lower, "dsn", "driver", "connect", "ping", "secret"):
			category = "database_connection"
			addSignal("database_connection")
		case containsAny(lower, "read_only", "read only", "mutation"):
			category = "database_guard"
			addSignal("database_guard")
		case containsAny(lower, "syntax", "sql", "query", "exec", "constraint"):
			category = "database_sql"
			addSignal("database_sql")
		default:
			category = "database_runtime"
			addSignal("database")
		}
	case strings.HasPrefix(strings.ToLower(event.ActionType), "dns_") || strings.HasPrefix(strings.ToLower(event.ActionType), "tcp_") || strings.HasPrefix(strings.ToLower(event.ActionType), "udp_") || strings.HasPrefix(strings.ToLower(event.ActionType), "ftp_") || strings.HasPrefix(strings.ToLower(event.ActionType), "smtp_") || strings.HasPrefix(strings.ToLower(event.ActionType), "pop3_") || strings.HasPrefix(strings.ToLower(event.ActionType), "imap_") || strings.HasPrefix(strings.ToLower(event.ActionType), "ldap_") || strings.HasPrefix(strings.ToLower(event.ActionType), "winsock_"):
		switch {
		case containsAny(lower, "dns", "no such host", "lookup"):
			category = "network_dns_resolution"
			addSignal("dns")
		case containsAny(lower, "tls", "certificate", "auth", "login", "bind"):
			category = "network_auth_tls"
			addSignal("network_auth_tls")
		case containsAny(lower, "timeout", "deadline", "connection refused", "reset by peer"):
			category = "network_connection"
			addSignal("network_connection")
		default:
			category = "network_protocol"
			addSignal("network_protocol")
		}
	case strings.HasPrefix(strings.ToLower(event.ActionType), "jms_") && containsAny(lower, "broker", "destination", "message received", "timeout", "dead_letter", "correlation", "ack"):
		category = "jms_broker_message"
		addSignal("jms")
	case containsAny(lower, "assert", "expected", "mismatch", "check failed"):
		category = "assertion"
		addSignal("assertion")
	case containsAny(lower, "{{var.", "{{ param.", "{{param.", "extractor", "extract ", "jsonpath", "regexp", "regex", "correlat"):
		category = "correlation"
		addSignal("correlation")
	case containsAny(lower, "{{data.", "{{ data.", "{{param.", "data source", "parameter", "missing data", "no row"):
		category = "data"
		addSignal("data_binding")
	case event.StatusCode == 401 || event.StatusCode == 403 || containsAny(lower, "unauthorized", "forbidden", "csrf", "cookie", "session expired", "login", "auth"):
		category = "auth_session"
		addSignal("auth_session")
	case event.StatusCode >= 500 || containsAny(lower, "connection refused", "no such host", "dns", "tls", "certificate", "network", "reset by peer", "unreachable"):
		category = "network"
		addSignal("network")
	case event.StatusCode >= 300 && event.StatusCode < 400 || containsAny(lower, "redirect", "too many redirects"):
		category = "protocol"
		addSignal("redirect")
	case containsAny(lower, "unsupported", "protocol", "codec", "marshal", "unmarshal"):
		category = "protocol"
		addSignal("protocol")
	default:
		addSignal("runtime")
	}

	evidence := replayDiagnosisEvidence(event, category)
	action := replayDiagnosisAction(category)
	diagnosis := ReplayDiagnosis{
		Category: category,
		Severity: "error",
		Cause:    cause,
		Evidence: evidence,
		Action:   action,
		Signals:  signals,
	}
	if event.DOMSummary != nil && len(event.DOMSummary.VisibleText) > 0 {
		diagnosis.FrameContext = strings.Join(event.DOMSummary.VisibleText[:minInt(len(event.DOMSummary.VisibleText), 3)], " | ")
	}
	if category == "selector" && event.Selector != "" {
		diagnosis.SuggestedLocator = event.Selector
	}
	return diagnosis
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func inferReplayDependencyType(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	actionType := strings.ToLower(strings.TrimSpace(action.Type))
	switch {
	case strings.HasPrefix(actionType, "ws_"):
		return "websocket"
	case strings.HasPrefix(actionType, "grpc_"):
		return "grpc"
	case strings.HasPrefix(actionType, "db_") || strings.HasPrefix(actionType, "database_"):
		return "database"
	case strings.HasPrefix(actionType, "dns_"), strings.HasPrefix(actionType, "tcp_"), strings.HasPrefix(actionType, "udp_"), strings.HasPrefix(actionType, "ftp_"), strings.HasPrefix(actionType, "smtp_"), strings.HasPrefix(actionType, "pop3_"), strings.HasPrefix(actionType, "imap_"), strings.HasPrefix(actionType, "ldap_"), strings.HasPrefix(actionType, "winsock_"):
		return "network"
	case strings.HasPrefix(actionType, "jms_"):
		return "jms_queue"
	default:
		return ""
	}
}

func replayDiagnosisEvidence(event ReplayActionEvent, category string) string {
	switch category {
	case "selector":
		if event.Selector != "" {
			return fmt.Sprintf("Selector %q failed for %s", event.Selector, nonEmptyString(event.PageURL, event.Target))
		}
	case "wait_timing":
		return "The browser action did not reach the expected page or element state before timeout."
	case "correlation":
		return "The action references dynamic values or extractors that did not resolve cleanly during replay."
	case "data":
		return "The action depends on parameter or data-source values that were not available for this replay."
	case "auth_session":
		return "The target returned an authentication/session signal during replay."
	case "network":
		return "The protocol reported a transport or server-side availability failure."
	case "protocol":
		return "The protocol/runtime could not complete the action using the current script shape."
	case "websocket_handshake":
		return "The WebSocket connection did not complete the HTTP upgrade/handshake."
	case "websocket_receive_timeout":
		return "The WebSocket receive step timed out or received a message that did not match expectations."
	case "grpc_contract":
		return "The gRPC method, reflection data, or proto contract could not be resolved."
	case "grpc_status":
		return "The gRPC call returned a non-OK status or metadata/timeout failure."
	case "database_connection":
		return "The database adapter could not establish or validate the configured connection."
	case "database_sql":
		return "The SQL action failed during query, exec, constraint, or syntax processing."
	case "database_guard":
		return "A database safety guard blocked a mutating operation."
	case "database_runtime":
		return "The database action failed before producing approved result evidence."
	case "network_dns_resolution":
		return "The network adapter could not resolve the requested DNS target."
	case "network_connection":
		return "The network adapter could not establish or sustain the requested connection."
	case "network_auth_tls":
		return "The network adapter reported TLS, bind, login, or protocol authentication failure."
	case "network_protocol":
		return "The network protocol command failed or returned an unexpected response."
	case "jms_broker_message":
		return "The JMS adapter reported broker, destination, correlation, acknowledgement, timeout, or dead-letter evidence."
	}
	if event.StatusCode != 0 {
		return fmt.Sprintf("Protocol status: %d", event.StatusCode)
	}
	return ""
}

func replayDiagnosisAction(category string) string {
	switch category {
	case "selector":
		return "Review selector evidence, prefer stable role/text/test-id locators, and add an explicit wait if the element is rendered late."
	case "wait_timing":
		return "Add or tune a wait around the page state that proves the next action is safe."
	case "assertion":
		return "Compare expected and actual values, then draft a narrower assertion or update the expected value."
	case "correlation":
		return "Add or repair an extractor before the first unresolved use of the value."
	case "data":
		return "Bind the missing value to a data source row, parameter, or replay variable seed."
	case "auth_session":
		return "Refresh login/session setup, secret bindings, cookies, or CSRF extraction before rerun."
	case "network":
		return "Confirm target reachability, TLS, DNS, redirects, and environment-specific network controls."
	case "protocol":
		return "Inspect the protocol adapter and action fields for unsupported or malformed runtime input."
	case "websocket_handshake":
		return "Check endpoint URL, TLS, headers, origin, and subprotocol before rerunning the WebSocket connect step."
	case "websocket_receive_timeout":
		return "Tune receive timeout or match criteria, then verify the upstream event is published before this step waits."
	case "grpc_contract":
		return "Attach the correct proto files or enable reflection, then confirm the service/method path."
	case "grpc_status":
		return "Inspect gRPC status, metadata, authority, TLS/plaintext mode, and deadline settings."
	case "database_connection":
		return "Verify driver, DSN secret binding, network route, and fixture readiness before replay."
	case "database_sql":
		return "Review SQL text, bind values, fixture data, and row/affected-count assertions."
	case "database_guard":
		return "Move write-capable checks to an approved fixture or disable read-only mode only after review."
	case "database_runtime":
		return "Inspect query output, transaction state, and database evidence before drafting a repair."
	case "network_dns_resolution":
		return "Confirm DNS record type, resolver access, host spelling, and test-network policy."
	case "network_connection":
		return "Check endpoint, port, TLS mode, timeout, and local protocol fixture availability."
	case "network_auth_tls":
		return "Review TLS certificates, bind/login credentials, and protocol-specific authentication commands."
	case "network_protocol":
		return "Inspect the protocol transcript and adjust command, payload, or expected response."
	case "jms_broker_message":
		return "Verify broker settings, destination, reply-to, correlation ID, ack mode, and dead-letter routing."
	default:
		return "Inspect the raw log and action snapshot, then rerun with debug trace capture."
	}
}

func replayRepairSuggestionsForDiagnosis(event ReplayActionEvent, diagnosis ReplayDiagnosis) []ReplayRepairSuggestion {
	baseID := event.EventID
	if baseID == "" {
		baseID = fmt.Sprintf("%s-%d", event.Phase, event.EventIndex)
	}
	switch diagnosis.Category {
	case "selector":
		return []ReplayRepairSuggestion{
			{
				ID:             baseID + ":update-selector",
				Title:          "Draft stronger selector",
				Description:    "Create a draft edit that replaces the brittle selector with a stable locator from DOM evidence.",
				DraftPatchType: "update_selector",
				Action:         event.ActionName,
				Confidence:     0.72,
				Fields:         map[string]string{"selector": event.Selector, "target": event.Target},
			},
			{
				ID:             baseID + ":add-wait",
				Title:          "Add readiness wait",
				Description:    "Insert an explicit wait before this browser action so replay waits for the required element state.",
				DraftPatchType: "add_wait",
				Action:         event.ActionName,
				Confidence:     0.64,
				Fields:         map[string]string{"selector": event.Selector},
			},
		}
	case "wait_timing":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":tune-wait",
			Title:          "Tune wait condition",
			Description:    "Draft a wait condition that anchors on the page or element state seen immediately before the failure.",
			DraftPatchType: "add_wait",
			Action:         event.ActionName,
			Confidence:     0.68,
			Fields:         map[string]string{"selector": event.Selector, "target": event.Target},
		}}
	case "assertion":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":adjust-assertion",
			Title:          "Review assertion",
			Description:    "Draft a focused assertion update using actual replay response evidence.",
			DraftPatchType: "adjust_assertion",
			Action:         event.ActionName,
			Confidence:     0.62,
		}}
	case "correlation":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":add-extractor",
			Title:          "Add missing extractor",
			Description:    "Draft an extractor before the failed action so dynamic values are captured instead of hard-coded.",
			DraftPatchType: "add_extractor",
			Action:         event.ActionName,
			Confidence:     0.70,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "data":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":mark-data",
			Title:          "Mark data requirement",
			Description:    "Draft a data binding requirement for values this replay could not seed.",
			DraftPatchType: "mark_data_requirement",
			Action:         event.ActionName,
			Confidence:     0.66,
		}}
	case "auth_session":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-session",
			Title:          "Refresh session setup",
			Description:    "Draft a setup change for login, cookies, CSRF extraction, or required auth secrets.",
			DraftPatchType: "repair_auth_session",
			Action:         event.ActionName,
			Confidence:     0.58,
		}}
	case "websocket_handshake", "websocket_receive_timeout":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":tune-websocket",
			Title:          "Tune WebSocket evidence",
			Description:    "Draft a WebSocket repair for URL, headers, subprotocol, receive timeout, or message match criteria.",
			DraftPatchType: "repair_websocket",
			Action:         event.ActionName,
			Confidence:     0.61,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "grpc_contract", "grpc_status":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-grpc",
			Title:          "Repair gRPC call shape",
			Description:    "Draft a gRPC edit for proto/reflection, method target, metadata, TLS/plaintext mode, or deadline settings.",
			DraftPatchType: "repair_grpc",
			Action:         event.ActionName,
			Confidence:     0.60,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "database_connection", "database_sql", "database_guard", "database_runtime":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-database",
			Title:          "Repair database action",
			Description:    "Draft a database edit for DSN readiness, bind values, SQL text, read-only policy, or row assertions.",
			DraftPatchType: "repair_database",
			Action:         event.ActionName,
			Confidence:     0.63,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "network_dns_resolution", "network_connection", "network_auth_tls", "network_protocol":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-network",
			Title:          "Repair network protocol step",
			Description:    "Draft a network edit for endpoint, port, TLS/auth, protocol command, timeout, or response assertion.",
			DraftPatchType: "repair_network",
			Action:         event.ActionName,
			Confidence:     0.59,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "jms_broker_message":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-jms",
			Title:          "Repair JMS message flow",
			Description:    "Draft a JMS edit for broker settings, destination, reply-to, correlation ID, timeout, acknowledgement, or dead-letter routing.",
			DraftPatchType: "repair_jms",
			Action:         event.ActionName,
			Confidence:     0.60,
			Fields:         map[string]string{"target": event.Target},
		}}
	default:
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":diagnostic-note",
			Title:          "Create diagnostic note",
			Description:    "Attach this failure evidence to a draft repair note for analyst review.",
			DraftPatchType: "diagnostic_note",
			Action:         event.ActionName,
			Confidence:     0.50,
		}}
	}
}

func buildReplayDiagnosisSummary(events []ReplayActionEvent) *ReplayDiagnosisSummary {
	summary := &ReplayDiagnosisSummary{Categories: map[string]int{}}
	for _, event := range events {
		if event.Success {
			continue
		}
		summary.FailureCount++
		category := "unknown"
		cause := strings.TrimSpace(event.ErrorMessage)
		if event.Diagnosis != nil {
			category = event.Diagnosis.Category
			cause = event.Diagnosis.Cause
		}
		summary.Categories[category]++
		if summary.FirstFailureEventID == "" {
			summary.FirstFailureEventID = event.EventID
			summary.FirstFailureAction = event.ActionName
			summary.FirstFailureCause = cause
		}
	}
	if summary.FailureCount == 0 {
		return nil
	}
	return summary
}

func collectReplayRepairSuggestions(events []ReplayActionEvent) []ReplayRepairSuggestion {
	var suggestions []ReplayRepairSuggestion
	seen := make(map[string]struct{})
	for _, event := range events {
		for _, suggestion := range event.RepairSuggestions {
			if suggestion.ID == "" {
				continue
			}
			if _, ok := seen[suggestion.ID]; ok {
				continue
			}
			seen[suggestion.ID] = struct{}{}
			suggestions = append(suggestions, cloneReplayRepairSuggestion(suggestion))
		}
	}
	if len(suggestions) == 0 {
		return nil
	}
	return suggestions
}

func nonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
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

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneReplayDebugAction(action ReplayDebugAction) *ReplayDebugAction {
	cloned := action
	return &cloned
}

func cloneReplayActionEvent(event ReplayActionEvent) *ReplayActionEvent {
	cloned := event
	cloned.Headers = cloneStringMap(event.Headers)
	cloned.Extractions = cloneStringMap(event.Extractions)
	cloned.Console = cloneConsoleEntries(event.Console)
	cloned.Network = cloneNetworkEntries(event.Network)
	cloned.DOMSummary = cloneDOMSummary(event.DOMSummary)
	cloned.ClientMetrics = cloneClientMetrics(event.ClientMetrics)
	cloned.ProtocolEvidence = cloneAnyMap(event.ProtocolEvidence)
	cloned.Diagnosis = cloneReplayDiagnosis(event.Diagnosis)
	cloned.RepairSuggestions = cloneReplayRepairSuggestions(event.RepairSuggestions)
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
	cloned.RuntimeProfile = *cloneReplayRuntimeProfile(&report.RuntimeProfile)
	cloned.ArtifactManifest = cloneReplayArtifactManifest(report.ArtifactManifest)
	cloned.DiagnosisSummary = cloneReplayDiagnosisSummary(report.DiagnosisSummary)
	cloned.RepairSuggestions = cloneReplayRepairSuggestions(report.RepairSuggestions)
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

func cloneReplayRuntimeProfile(profile *ReplayRuntimeProfile) *ReplayRuntimeProfile {
	if profile == nil {
		return nil
	}
	cloned := *profile
	if profile.Headless != nil {
		headless := *profile.Headless
		cloned.Headless = &headless
	}
	return &cloned
}

func cloneReplayArtifactManifest(manifest *ReplayArtifactManifest) *ReplayArtifactManifest {
	if manifest == nil {
		return nil
	}
	cloned := *manifest
	cloned.RuntimeProfile = *cloneReplayRuntimeProfile(&manifest.RuntimeProfile)
	if len(manifest.DataSources) > 0 {
		cloned.DataSources = append([]string(nil), manifest.DataSources...)
	}
	if len(manifest.SeedVariables) > 0 {
		cloned.SeedVariables = append([]string(nil), manifest.SeedVariables...)
	}
	if len(manifest.ProtocolArtifacts) > 0 {
		cloned.ProtocolArtifacts = make(map[string]int, len(manifest.ProtocolArtifacts))
		for key, value := range manifest.ProtocolArtifacts {
			cloned.ProtocolArtifacts[key] = value
		}
	}
	return &cloned
}

func cloneReplayDiagnosis(diagnosis *ReplayDiagnosis) *ReplayDiagnosis {
	if diagnosis == nil {
		return nil
	}
	cloned := *diagnosis
	if len(diagnosis.Signals) > 0 {
		cloned.Signals = append([]string(nil), diagnosis.Signals...)
	}
	return &cloned
}

func cloneReplayRepairSuggestions(suggestions []ReplayRepairSuggestion) []ReplayRepairSuggestion {
	if len(suggestions) == 0 {
		return nil
	}
	cloned := make([]ReplayRepairSuggestion, len(suggestions))
	for i, suggestion := range suggestions {
		cloned[i] = cloneReplayRepairSuggestion(suggestion)
	}
	return cloned
}

func cloneReplayRepairSuggestion(suggestion ReplayRepairSuggestion) ReplayRepairSuggestion {
	cloned := suggestion
	if len(suggestion.Fields) > 0 {
		cloned.Fields = cloneStringMap(suggestion.Fields)
	}
	return cloned
}

func cloneReplayDiagnosisSummary(summary *ReplayDiagnosisSummary) *ReplayDiagnosisSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.Categories) > 0 {
		cloned.Categories = make(map[string]int, len(summary.Categories))
		for key, value := range summary.Categories {
			cloned.Categories[key] = value
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
