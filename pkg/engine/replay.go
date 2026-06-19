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
	Name             string `json:"name,omitempty"`
	CaptureMode      string `json:"capture_mode,omitempty"`
	LogLevel         string `json:"log_level,omitempty"`
	IgnoreThinkTime  bool   `json:"ignore_think_time,omitempty"`
	BrowserEngine    string `json:"browser_engine,omitempty"`
	DeviceProfile    string `json:"device_profile,omitempty"`
	Headless         *bool  `json:"headless,omitempty"`
	FailurePolicy    string `json:"failure_policy,omitempty"`
	ExperienceCanary bool   `json:"experience_canary,omitempty"`
	HARCapturePolicy string `json:"har_capture_policy,omitempty"`
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
		if !profile.ExperienceCanary {
			profile.ExperienceCanary = script.Config.Browser.ExperienceCanary
		}
		if strings.TrimSpace(profile.HARCapturePolicy) == "" {
			profile.HARCapturePolicy = firstNonEmpty(script.Config.Browser.HARCapturePolicy, script.Config.Browser.HARCapture)
		}
	}
	return profile
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func publishReplayProgress(callback func(ReplayProgressEvent), event ReplayProgressEvent) {
	if callback == nil {
		return
	}
	callback(event)
}
