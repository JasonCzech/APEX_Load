package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

type ReplayOptions struct {
	GroupName       string
	ReplaySessionID string
	ScriptRevision  string
	RuntimeProfile  *ReplayRuntimeProfile
	Variables       map[string]string
	RedactionRules  []RedactionRule
	SecretResolver  SecretResolver
	IgnoreThinkTime bool
	// Seed, when set, makes random data selection and {{fn.random}}/{{fake.*}}
	// reproducible across replays. Nil uses defaultReplaySeed.
	Seed            *int64
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
	// Condition is set only on ReplayProgressCondition events: it carries the
	// evaluated operands and branch outcome of a control_if/control_loop
	// predicate so the debugger can show *why* a branch was taken. Action
	// identifies the container (phase/action_index/step_path/iteration_path).
	Condition *ConditionEvidence
}

// replayFlow is the per-action signal the replay expander returns, mirroring the
// load path's actionFlow: continue to the next action, break/continue the
// enclosing control_loop, or stop the phase after a failure.
type replayFlow int

const (
	replayNext replayFlow = iota
	replayBreak
	replayContinue
	replayStop
	replayExitIteration
)

// leafReplayFlow maps a leaf's (shouldContinue, abort) result onto the replayFlow
// vocabulary: a failed leaf becomes replayStop (the caller still honors
// continueOnError), a successful one replayNext.
func leafReplayFlow(shouldContinue, abort bool) (replayFlow, bool) {
	if abort {
		return replayStop, true
	}
	if !shouldContinue {
		return replayStop, false
	}
	return replayNext, false
}

type ReplayDebugController interface {
	BeforeAction(context.Context, ReplayDebugAction) error
}

// ReplayDebugControllerAfter is an optional capability for ReplayDebugController
// implementations. When implemented, AfterAction is invoked once per action
// after it has executed but before the stop-on-error decision, letting a
// controller pause (e.g. break-on-failure) and hold execution until the
// operator resumes, steps, or stops. A non-nil error aborts the replay, mirroring
// BeforeAction.
type ReplayDebugControllerAfter interface {
	AfterAction(context.Context, ReplayDebugAction, *ReplayActionEvent) error
}

// ReplayDebugControllerCorrelationObserver is an optional, internal-only
// capability for replay debug controllers. It is invoked after protocol
// execution but before the replay event is scrubbed. CorrelationEvidence is raw
// and ephemeral: implementations must copy only bounded data they need and must
// never publish or retain the event pointer. The engine clears the evidence
// before the event enters reports, progress callbacks, exports, or WebSockets.
type ReplayDebugControllerCorrelationObserver interface {
	ObserveCorrelationEvidence(context.Context, ReplayDebugAction, *ReplayActionEvent)
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
	// Sub-request / sub-step identity, set when this leaf is a child of a
	// container action (http_sequence / control_if / control_loop). Absent for a
	// plain top-level action so existing payloads are unchanged.
	ParentActionName string   `json:"parent_action_name,omitempty"`
	RequestIndex     *int     `json:"request_index,omitempty"`
	RequestCount     int      `json:"request_count,omitempty"`
	StepPath         []string `json:"step_path,omitempty"`
	Iteration        *int     `json:"iteration,omitempty"`
	IterationPath    []int    `json:"iteration_path,omitempty"`
	LeafOrdinal      int      `json:"leaf_ordinal,omitempty"`

	parentBusinessTransaction bool
}

const (
	ReplayProgressStarted         = "replay_started"
	ReplayProgressActionStarted   = "action_started"
	ReplayProgressActionCompleted = "action_completed"
	ReplayProgressActionFailed    = "action_failed"
	ReplayProgressEventRun        = "replay_event"
	ReplayProgressComplete        = "replay_complete"
	ReplayProgressCondition       = "replay_condition"
)

type ReplayReport struct {
	ReplaySessionID             string                    `json:"replay_session_id,omitempty"`
	ScriptHash                  string                    `json:"script_hash,omitempty"`
	ScriptRevision              string                    `json:"script_revision,omitempty"`
	RuntimeProfile              ReplayRuntimeProfile      `json:"runtime_profile,omitempty"`
	ArtifactManifest            *ReplayArtifactManifest   `json:"artifact_manifest,omitempty"`
	DiagnosisSummary            *ReplayDiagnosisSummary   `json:"diagnosis_summary,omitempty"`
	RepairSuggestions           []ReplayRepairSuggestion  `json:"repair_suggestions,omitempty"`
	Group                       string                    `json:"group"`
	Protocol                    string                    `json:"protocol"`
	StartedAt                   time.Time                 `json:"started_at"`
	CompletedAt                 time.Time                 `json:"completed_at"`
	Success                     bool                      `json:"success"`
	Error                       string                    `json:"error,omitempty"`
	Events                      []ReplayActionEvent       `json:"events"`
	DroppedEventCount           int                       `json:"dropped_event_count,omitempty"`
	TruncatedEventEvidenceCount int                       `json:"truncated_event_evidence_count,omitempty"`
	FinalVariables              map[string]string         `json:"final_variables,omitempty"`
	Snapshots                   []*protocol.ErrorSnapshot `json:"snapshots,omitempty"`

	totalEventCount         int
	retainedEventJSONBytes  int
	eventJSONBytes          []int
	consumedEvidenceBytes   int
	evidenceProjectionCount int
	manifestSnapshots       map[*protocol.ErrorSnapshot]struct{}
	retainedSnapshotClones  map[*protocol.ErrorSnapshot]*protocol.ErrorSnapshot
}

type ReplayActionEvent struct {
	EventID           string                   `json:"event_id,omitempty"`
	ActionIndex       int                      `json:"action_index"`
	EventIndex        int                      `json:"event_index"`
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
	// Sub-request / sub-step identity for leaves of a container action
	// (http_sequence / control_if / control_loop). Absent for a plain top-level
	// action so existing events serialize unchanged.
	ParentActionName string   `json:"parent_action_name,omitempty"`
	RequestIndex     *int     `json:"request_index,omitempty"`
	RequestCount     int      `json:"request_count,omitempty"`
	StepPath         []string `json:"step_path,omitempty"`
	Iteration        *int     `json:"iteration,omitempty"`
	IterationPath    []int    `json:"iteration_path,omitempty"`
	LeafOrdinal      int      `json:"leaf_ordinal,omitempty"`

	parentBusinessTransaction bool
	// CorrelationEvidence exists only between protocol execution and the
	// pre-scrub debug observer. It is cleared before any public replay boundary.
	CorrelationEvidence *protocol.CorrelationEvidence `json:"-"`
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
	EventCount        int                  `json:"event_count"`
	FailureCount      int                  `json:"failure_count"`
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

// defaultReplaySeed seeds replay's random data selection and random template
// builtins when ReplayOptions.Seed is not set, so replays are reproducible.
const defaultReplaySeed int64 = 1

func ReplayScript(ctx context.Context, script *dsl.Script, opts ReplayOptions) (*ReplayReport, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if err := importExecutionGateError(script, "replay"); err != nil {
		return nil, err
	}

	groupName := opts.GroupName
	if groupName == "" {
		groupName = "replay"
	}
	redactionConfig, err := NewRedactionConfig(opts.RedactionRules)
	if err != nil {
		return nil, fmt.Errorf("redaction config: %s", scrubSensitiveText(err.Error()))
	}
	secretResolver := opts.SecretResolver
	if secretResolver == nil {
		secretResolver = defaultSecretResolverForScript(script)
	}
	resolutionCtx, resolutionCancel := WithSecretResolutionBudget(ctx)
	secretResolver, secretValues, err := snapshotReferencedSecretsContext(resolutionCtx, script, secretResolver)
	resolutionCancel()
	if err != nil {
		return nil, fmt.Errorf("inspect script secret references: %s", scrubSensitiveText(err.Error()))
	}
	// One shared chokepoint with engine.New, NewMultiGroup, and shakeout; see
	// script_sourced_run_evidence.go.
	secretValues, scriptSourcedValues, importedCredentials, err := runtimeSecretValuesForScript(script, secretResolver, secretValues)
	if err != nil {
		return nil, fmt.Errorf("inspect retained import credentials: %s", scrubSensitiveText(err.Error()))
	}
	// seedSecretValues is origin-independent: an operator-supplied replay variable
	// is governed by its NAME, not by the script. It stays platform-managed and is
	// never gated by the setting.
	seedSecretValues := make([]string, 0)
	for key, value := range opts.Variables {
		if value != "" && (scrub.SensitiveName(key) || scrub.Value(value, scrub.Policy{}) != value) {
			seedSecretValues = append(seedSecretValues, value)
		}
	}
	secretValues = append(secretValues, seedSecretValues...)
	redactionConfig, scriptSourcedDropped, err := buildRuntimeSecretRedaction(redactionConfig, secretValues, scriptSourcedValues)
	if err != nil {
		return nil, fmt.Errorf("prepare runtime secret redaction: %s", scrubSensitiveText(err.Error()))
	}
	if scriptSourcedDropped {
		warnScriptSourcedRedactionDropped("group", groupName, "script", script.Name)
	}
	groupName = scrubReplayGroupName(groupName, redactionConfig)

	proto, err := protocol.Get(script.Protocol)
	if err != nil {
		return nil, fmt.Errorf("protocol init: %s", scrubSensitiveTextWithConfig(err.Error(), redactionConfig))
	}

	_, captureCorrelationEvidence := opts.DebugController.(ReplayDebugControllerCorrelationObserver)
	protoConfig := buildProtoConfigForRunModeWithSecretResolver(script, "replay", secretResolver)
	if captureCorrelationEvidence {
		protoConfig[protocol.CorrelationEvidenceCaptureConfigKey] = true
	}
	bindProtocolRuntimeRedaction(proto, func() *RedactionConfig { return redactionConfig })
	if err := callProtocolInit(ctx, proto, protoConfig, redactionConfig); err != nil {
		runProtocolTeardown(proto, func() *RedactionConfig { return redactionConfig })
		return nil, fmt.Errorf("protocol init: %s", err)
	}
	defer runProtocolTeardown(proto, func() *RedactionConfig { return redactionConfig })

	dataProvider, err := NewDataProvider(script.DataSources)
	if err != nil {
		return nil, fmt.Errorf("data source init: %s", scrubSensitiveTextWithConfig(err.Error(), redactionConfig))
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
		Events:           make([]ReplayActionEvent, 0, min(MaxRetainedReplayEvents, len(script.Init)+len(script.Actions)+len(script.Teardown))),
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
		ID:                           0,
		Group:                        groupName,
		Proto:                        proto,
		Script:                       script,
		Metrics:                      NewMetricsCollector(16),
		Variables:                    make(map[string]string),
		DataProvider:                 dataProvider,
		SecretResolver:               secretResolver,
		RedactionConfig:              redactionConfig,
		importedCredentialInspection: importedCredentials,
		SnapshotStore:                NewSnapshotStore(128),
	}
	// Deterministic replay: seed random data selection and random template builtins
	// BEFORE prepareRuntimeState, whose initial data-row load (loadDataRow → GetRow)
	// draws from the data provider's RNG. Seeding afterward left the first row's
	// selection on the global rand source, so same-seed replays diverged.
	replaySeed := defaultReplaySeed
	if opts.Seed != nil {
		replaySeed = *opts.Seed
	}
	dataProvider.SeedRandom(replaySeed)
	vu.rng = rand.New(rand.NewSource(replaySeed))

	vu.prepareRuntimeState()
	for key, value := range opts.Variables {
		vu.Variables[key] = value
	}
	if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
		return nil, fmt.Errorf("prepare replay credential redaction: %s", err)
	}

	continueOnError := script.Config.ErrorHandling.ContinueOnErrorEnabled()
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
	teardownCtx, teardownCancel := teardownContext()
	runReplayPhase(teardownCtx, vu, script.Teardown, "teardown", true, opts.IgnoreThinkTime, globalThinkCfg, report, opts.OnProgress, opts.DebugController)
	teardownCancel()
	finalizedSourceTransactions := vu.finalizeOpenSourceTransactions("source transaction ended without a matching end marker during replay")
	appendReplaySourceTransactionFinalizations(report, finalizedSourceTransactions, vu.RedactionConfig, opts.OnProgress)

	report.CompletedAt = time.Now()
	if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
		report.Success = false
		if report.Error == "" {
			report.Error = "runtime credential redaction failed"
		}
	}
	report.FinalVariables = scrubReplayFinalVariables(vu.Variables, vu.RedactionConfig)
	setReplayReportSnapshots(report)
	report.RepairSuggestions = collectReplayRepairSuggestions(report.Events)
	if report.ArtifactManifest != nil {
		report.ArtifactManifest.CreatedAt = report.CompletedAt
	}
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

// appendReplaySourceTransactionFinalizations exposes the same failed logical
// transactions that the load path records when replay reaches the end with an
// unmatched source start marker. Without a replay event, the report could
// otherwise claim success while its transaction metric recorded a failure.
func appendReplaySourceTransactionFinalizations(
	report *ReplayReport,
	results []*protocol.Result,
	redactionConfig *RedactionConfig,
	onProgress func(ReplayProgressEvent),
) {
	if report == nil || len(results) == 0 {
		return
	}
	report.Success = false
	if report.Error == "" {
		if len(results) == 1 {
			report.Error = fmt.Sprintf("source transaction %q ended without a matching end marker", results[0].ActionName)
		} else {
			report.Error = fmt.Sprintf("%d source transactions ended without matching end markers", len(results))
		}
	}
	report.Error = scrubSensitiveTextWithConfig(report.Error, redactionConfig)

	for _, result := range results {
		if result == nil {
			continue
		}
		eventIndex := report.nextEventIndex()
		eventID := replayActionEventID(report.ReplaySessionID, "finalize", -1, eventIndex)
		completedAt := result.StartTime.Add(result.Duration)
		if completedAt.IsZero() || completedAt.Before(result.StartTime) {
			completedAt = time.Now()
		}
		event := ReplayActionEvent{
			EventID:           eventID,
			ActionIndex:       -1,
			EventIndex:        eventIndex,
			Phase:             "finalize",
			ActionName:        result.ActionName,
			ActionType:        "source_transaction",
			StartedAt:         result.StartTime,
			CompletedAt:       completedAt,
			DurationMs:        float64(result.Duration.Microseconds()) / 1000,
			Success:           false,
			ErrorMessage:      result.ErrorMessage,
			SourceStepID:      result.SourceStepID,
			SourceStepIndex:   result.SourceStepIndex,
			SourceAction:      result.SourceAction,
			SourceActionName:  result.SourceActionName,
			SourceTransaction: firstEngineValue(result.ActionName, result.SourceTransaction),
			SourceProfile:     result.SourceProfile,
		}
		attachReplayDiagnosis(&event)
		scrubReplayActionEvent(&event, redactionConfig)
		event = appendReplayReportEvent(report, event)

		debugAction := &ReplayDebugAction{
			EventID:     event.EventID,
			Phase:       event.Phase,
			ActionIndex: event.ActionIndex,
			EventIndex:  event.EventIndex,
			ActionName:  event.ActionName,
			ActionType:  event.ActionType,
		}
		publishReplayProgress(onProgress, ReplayProgressEvent{
			Type:       ReplayProgressActionFailed,
			EventID:    event.EventID,
			SessionID:  report.ReplaySessionID,
			Timestamp:  event.CompletedAt,
			EventIndex: event.EventIndex,
			Action:     debugAction,
			Event:      cloneReplayActionEvent(event),
		})
		publishReplayProgress(onProgress, ReplayProgressEvent{
			Type:       ReplayProgressEventRun,
			EventID:    event.EventID,
			SessionID:  report.ReplaySessionID,
			Timestamp:  event.CompletedAt,
			EventIndex: event.EventIndex,
			Event:      cloneReplayActionEvent(event),
		})
	}
}

// replayUnit is one thing the debugger stops at: a leaf action plus its identity
// within the (possibly nested) container tree. A plain top-level action is a unit
// with no parent and an empty step path.
type replayUnit struct {
	action                    dsl.Action
	phase                     string
	actionIndex               int      // top-level action index (shared by all leaves under it)
	parentName                string   // container action name ("" for a plain top-level action)
	parentBusinessTransaction bool     // true only when parentName identifies an http_sequence transaction
	stepPath                  []string // static path to this leaf, e.g. ["requests[42]"], ["loop[0]"]
	iterationPath             []int    // 1-based pass numbers, one per enclosing loop (outer→inner); nil outside loops
	requestIndex              *int     // leaf ordinal within an http_sequence container; nil otherwise
	requestCount              int      // sibling count within an http_sequence container; 0 otherwise
	leafOrdinal               int      // 0-based execution order of this leaf within its top-level action
	synthErr                  error    // when set, emit a failed event instead of executing (e.g. parse error)
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
		leafOrdinal := 0
		flow, abort := expandReplayAction(ctx, vu, action, phase, actionIndex, 0, nil, nil, &leafOrdinal, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
		if abort {
			return false
		}
		if flow == replayStop && !continueOnError {
			return false
		}
		if flow == replayExitIteration {
			return false
		}
		// A break/continue that escaped to the phase level (no enclosing loop) is
		// a no-op: fall through to the next top-level action.
	}
	return true
}

// replayContainerKind reports whether an action is a container the replay engine
// must expand into child leaves. It mirrors the load-path dispatch
// (vuser.go: http_sequence + control_if/control_loop) exactly.
func replayContainerKind(action dsl.Action) string {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "http_sequence", "control_if", "control_switch", "control_loop", "control_parallel":
		return strings.ToLower(strings.TrimSpace(action.Type))
	default:
		return ""
	}
}

// expandReplayAction executes one action during replay. Container actions are
// recursively expanded into child leaves so each sub-request / sub-step flows
// through the same BeforeAction/AfterAction/progress machinery as a top-level
// action; leaves bottom out in runReplayUnit. It returns (replayFlow, abort):
// replayFlow mirrors the load path (next/break/continue/stop) so a control_loop
// can consume a break/continue produced by a nested control_break/control_continue
// leaf; abort means a debug controller returned an error (Stop/cancel) and the
// whole replay must halt.
func expandReplayAction(
	ctx context.Context,
	vu *VUser,
	action dsl.Action,
	phase string,
	actionIndex int,
	depth int,
	stepPath []string,
	iterationPath []int,
	leafCounter *int,
	continueOnError bool,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
	report *ReplayReport,
	onProgress func(ReplayProgressEvent),
	debugController ReplayDebugController,
) (replayFlow, bool) {
	if ctx != nil && ctx.Err() != nil {
		report.Success = false
		if report.Error == "" {
			report.Error = ctx.Err().Error()
		}
		return replayStop, true
	}
	if exitsIteration, _ := sourceMainIterationExit(action); exitsIteration {
		unit := replayUnit{action: action, phase: phase, actionIndex: actionIndex, stepPath: stepPath, iterationPath: iterationPath}
		_, _, abort := runReplayUnit(ctx, vu, unit, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
		if abort {
			return replayStop, true
		}
		return replayExitIteration, false
	}
	if vu.sourceTransactionCompletionWillExecute(action) {
		// Transaction completion reports a business-transaction status; LR_FAIL,
		// LR_STOP, or LR_AUTO resolving to failure does not itself transfer source
		// control. Malformed/unmatched end calls do not enter this lane and honor
		// the ordinary replay failure policy.
		// Keep the failed replay event/report signal produced by runReplayUnit,
		// but always execute the following source statements so an exact
		// error-message/lr_exit branch is not skipped when ContinueOnError is off.
		unit := replayUnit{action: action, phase: phase, actionIndex: actionIndex, stepPath: stepPath, iterationPath: iterationPath}
		_, _, abort := runReplayUnit(ctx, vu, unit, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
		if abort {
			return replayStop, true
		}
		return replayNext, false
	}

	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "control_break", "control_continue":
		// Loop-control transfer: emit the leaf event (so it shows in the timeline
		// and honors breakpoints), then return the flow so the enclosing
		// control_loop can consume it. Live load/lifecycle execution transfers
		// immediately, so suppress leaf think time here for timing parity.
		unit := replayUnit{action: action, phase: phase, actionIndex: actionIndex, stepPath: stepPath, iterationPath: iterationPath}
		_, shouldContinue, abort := runReplayUnit(ctx, vu, unit, leafCounter, continueOnError, true, globalThinkCfg, report, onProgress, debugController)
		if abort {
			return replayStop, true
		}
		if !shouldContinue {
			return replayStop, false
		}
		if strings.EqualFold(strings.TrimSpace(action.Type), "control_break") {
			return replayBreak, false
		}
		return replayContinue, false
	}

	switch replayContainerKind(action) {
	case "http_sequence":
		children, err := httpSequenceChildActions(action)
		if err != nil {
			unit := replayUnit{action: action, phase: phase, actionIndex: actionIndex, parentName: action.Name, parentBusinessTransaction: true, stepPath: stepPath, iterationPath: iterationPath, synthErr: err}
			_, shouldContinue, abort := runReplayUnit(ctx, vu, unit, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
			flow, abort := leafReplayFlow(shouldContinue, abort)
			// The live path represents a malformed sequence as a failed container
			// result, then retains the container's action-local wait when error
			// handling continues. Preserve the same source ordering in replay.
			if !abort && flow == replayStop && continueOnError {
				if !applyReplayContainerThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg, report) {
					return replayStop, true
				}
			}
			return flow, abort
		}
		for childIndex, child := range children {
			idx := childIndex
			count := len(children)
			unit := replayUnit{
				action:                    child,
				phase:                     phase,
				actionIndex:               actionIndex,
				parentName:                action.Name,
				parentBusinessTransaction: true,
				stepPath:                  appendStepPath(stepPath, fmt.Sprintf("requests[%d]", childIndex)),
				iterationPath:             iterationPath,
				requestIndex:              &idx,
				requestCount:              count,
			}
			_, shouldContinue, abort := runReplayUnit(ctx, vu, unit, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
			if abort {
				return replayStop, true
			}
			if !shouldContinue && !continueOnError {
				return replayStop, false
			}
		}
		if !applyReplayContainerThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg, report) {
			return replayStop, true
		}
		return replayNext, false
	case "control_if":
		if action.Control == nil || depth >= maxControlDepth {
			return replayNext, false
		}
		ok, evidence := vu.evalConditionEvidence(action.Control.Condition)
		body := action.Control.Else
		branch := "else"
		if ok {
			body = action.Control.Steps
			branch = "then"
		}
		evidence.Branch = branch
		publishConditionEvidence(onProgress, report, action, phase, actionIndex, stepPath, iterationPath, evidence)
		for stepIndex, child := range body {
			childPath := appendStepPath(stepPath, fmt.Sprintf("%s[%d]", branch, stepIndex))
			flow, abort := expandReplayAction(ctx, vu, child, phase, actionIndex, depth+1, childPath, iterationPath, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
			if abort {
				return replayStop, true
			}
			switch flow {
			case replayStop:
				if !continueOnError {
					return replayStop, false
				}
			case replayBreak, replayContinue, replayExitIteration:
				// Propagate the transfer up to the enclosing loop; stop running
				// the rest of this branch.
				return flow, false
			}
		}
		if !applyReplayContainerThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg, report) {
			return replayStop, true
		}
		return replayNext, false
	case "control_switch":
		if action.Control == nil || depth >= maxControlDepth {
			return replayNext, false
		}
		body := action.Control.Default
		branch := "default"
		for caseIndex, switchCase := range action.Control.Cases {
			ok, evidence := vu.evalConditionEvidence(switchCase.Condition)
			evidence.Branch = fmt.Sprintf("case[%d]", caseIndex)
			publishConditionEvidence(onProgress, report, action, phase, actionIndex, stepPath, iterationPath, evidence)
			if ok {
				body = switchCase.Steps
				branch = evidence.Branch
				break
			}
		}
		for stepIndex, child := range body {
			childPath := appendStepPath(stepPath, fmt.Sprintf("%s[%d]", branch, stepIndex))
			flow, abort := expandReplayAction(ctx, vu, child, phase, actionIndex, depth+1, childPath, iterationPath, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
			if abort {
				return replayStop, true
			}
			switch flow {
			case replayStop:
				if !continueOnError {
					return replayStop, false
				}
			case replayBreak, replayContinue, replayExitIteration:
				return flow, false
			}
		}
		if !applyReplayContainerThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg, report) {
			return replayStop, true
		}
		return replayNext, false
	case "control_parallel":
		if action.Control == nil || depth >= maxControlDepth || !parallelActionsRuntimeSafe(action.Control.Branches) {
			return replayStop, false
		}
		parentVariables := cloneStringMap(vu.Variables)
		parentExtracted := cloneIntMap(vu.extractedVariables)
		parentExtractedBytes := vu.extractedVariableBytes
		type branchExtractions struct {
			name   string
			values map[string]string
		}
		merged := make([]branchExtractions, 0, len(action.Control.Branches))
		success := true
		for branchIndex, branch := range action.Control.Branches {
			vu.Variables = cloneStringMap(parentVariables)
			vu.extractedVariables = make(map[string]int)
			vu.extractedVariableBytes = 0
			for stepIndex, child := range branch.Steps {
				childPath := appendStepPath(stepPath, fmt.Sprintf("branches[%d].steps[%d]", branchIndex, stepIndex))
				flow, abort := expandReplayAction(ctx, vu, child, phase, actionIndex, depth+1, childPath, iterationPath, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
				if abort {
					return replayStop, true
				}
				if flow == replayStop {
					success = false
					if !continueOnError {
						break
					}
				}
				if flow == replayBreak || flow == replayContinue || flow == replayExitIteration {
					success = false
					break
				}
			}
			values := make(map[string]string, len(vu.extractedVariables))
			for key := range vu.extractedVariables {
				if value, ok := vu.Variables[key]; ok {
					values[key] = value
				}
			}
			merged = append(merged, branchExtractions{name: strings.TrimSpace(branch.Name), values: values})
			if !success && !continueOnError {
				break
			}
		}
		vu.Variables = parentVariables
		vu.extractedVariables = parentExtracted
		vu.extractedVariableBytes = parentExtractedBytes
		for _, branch := range merged {
			for key, value := range branch.values {
				if err := vu.mergeExtractions(&protocol.Result{Extractions: map[string]string{"parallel." + branch.name + "." + key: value}}); err != nil {
					success = false
				}
			}
		}
		if !applyReplayContainerThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg, report) {
			return replayStop, true
		}
		if !success {
			return replayStop, false
		}
		return replayNext, false
	case "control_loop":
		if action.Control == nil || depth >= maxControlDepth {
			return replayNext, false
		}
		loop := action.Control
		maxPasses := controlLoopMaxPasses(loop)
		outerLoopIndex, hadOuterLoopIndex := vu.Variables["loop_index"]
		restoreLoopIndex := func() {
			if hadOuterLoopIndex {
				vu.Variables["loop_index"] = outerLoopIndex
			} else {
				delete(vu.Variables, "loop_index")
			}
		}

		// runPass replays loop.Steps once for a given iteration path, returning the
		// loop-level signal (next/break/continue) or replayStop when a child failed
		// under !continueOnError. abort halts the whole replay.
		runPass := func(childIterPath []int) (replayFlow, bool) {
			for stepIndex, child := range loop.Steps {
				childPath := appendStepPath(stepPath, fmt.Sprintf("loop[%d]", stepIndex))
				flow, abort := expandReplayAction(ctx, vu, child, phase, actionIndex, depth+1, childPath, childIterPath, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
				if abort {
					return replayStop, true
				}
				switch flow {
				case replayStop:
					if !continueOnError {
						return replayStop, false
					}
				case replayBreak, replayContinue, replayExitIteration:
					return flow, false
				}
			}
			return replayNext, false
		}

		// foreach mode (precedence: for_each > count > condition).
		if loop.ForEach != nil {
			rows := vu.foreachRows(loop.ForEach)
			restoreRows := vu.captureForEachRestore(loop.ForEach.Var, rows)
			defer restoreRows()
			defer restoreLoopIndex()
			for pass := 0; pass < rows.Len() && pass < maxPasses; pass++ {
				row := rows.Row(pass)
				if ctx.Err() != nil {
					return replayStop, true
				}
				vu.Variables["loop_index"] = strconv.Itoa(pass + 1)
				vu.bindForEachRow(loop.ForEach.Var, row)
				childIterPath := append(append([]int(nil), iterationPath...), pass+1)
				if loop.ForEach.Condition != nil {
					condOK, evidence := vu.evalConditionEvidence(loop.ForEach.Condition)
					publishConditionEvidence(onProgress, report, action, phase, actionIndex, stepPath, childIterPath, evidence)
					if !condOK {
						break
					}
				}
				flow, abort := runPass(childIterPath)
				if abort {
					return replayStop, true
				}
				if flow == replayStop {
					return replayStop, false
				}
				if flow == replayBreak {
					break
				}
				if flow == replayExitIteration {
					return replayExitIteration, false
				}
				// replayContinue / replayNext: advance to the next row.
			}
			if !applyReplayContainerThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg, report) {
				return replayStop, true
			}
			return replayNext, false
		}

		defer restoreLoopIndex()
		fixed := loop.Count > 0
		for pass := 0; ; pass++ {
			if ctx.Err() != nil {
				return replayStop, true
			}
			vu.Variables["loop_index"] = strconv.Itoa(pass + 1)
			if fixed {
				if pass >= loop.Count || pass >= maxPasses {
					break
				}
			} else {
				if pass >= maxPasses {
					break
				}
				// do_while runs the body once before the predicate is first checked
				// (post-test), mirroring the live engine, so a loop whose
				// terminal-state var persists from a prior iteration re-primes it on
				// pass 0 instead of being skipped.
				if !(loop.DoWhile && pass == 0) {
					condOK, evidence := vu.evalConditionEvidence(loop.Condition)
					publishConditionEvidence(onProgress, report, action, phase, actionIndex, stepPath, append(append([]int(nil), iterationPath...), pass+1), evidence)
					if !condOK {
						break
					}
				}
			}
			childIterPath := append(append([]int(nil), iterationPath...), pass+1)
			flow, abort := runPass(childIterPath)
			if abort {
				return replayStop, true
			}
			if flow == replayStop {
				return replayStop, false
			}
			if flow == replayBreak {
				break
			}
			if flow == replayExitIteration {
				return replayExitIteration, false
			}
			// replayContinue / replayNext: advance to the next pass.
		}
		if !applyReplayContainerThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg, report) {
			return replayStop, true
		}
		return replayNext, false
	default:
		unit := replayUnit{action: action, phase: phase, actionIndex: actionIndex, stepPath: stepPath, iterationPath: iterationPath}
		_, shouldContinue, abort := runReplayUnit(ctx, vu, unit, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
		return leafReplayFlow(shouldContinue, abort)
	}
}

// publishConditionEvidence emits a ReplayProgressCondition event describing the
// evaluated operands and outcome of a control_if/control_loop predicate, keyed
// (via the carried debug action) by phase/action_index/step_path so the debugger
// can show why a branch was taken or a loop pass ran.
func publishConditionEvidence(
	onProgress func(ReplayProgressEvent),
	report *ReplayReport,
	action dsl.Action,
	phase string,
	actionIndex int,
	stepPath []string,
	iterationPath []int,
	evidence ConditionEvidence,
) {
	if onProgress == nil {
		return
	}
	dbg := &ReplayDebugAction{
		Phase:         phase,
		ActionIndex:   actionIndex,
		ActionName:    action.Name,
		ActionType:    strings.ToLower(strings.TrimSpace(action.Type)),
		StepPath:      append([]string(nil), stepPath...),
		IterationPath: append([]int(nil), iterationPath...),
	}
	ev := evidence
	publishReplayProgress(onProgress, ReplayProgressEvent{
		Type:       ReplayProgressCondition,
		SessionID:  report.ReplaySessionID,
		Timestamp:  time.Now(),
		EventIndex: -1,
		Action:     dbg,
		Condition:  &ev,
	})
}

// runReplayUnit runs ONE leaf through the debug-hook + progress + event pipeline
// (BeforeAction -> execute -> progress events -> AfterAction). It returns
// (event, shouldContinue, abort) where abort means a controller returned an error
// and the whole replay must halt.
func runReplayUnit(
	ctx context.Context,
	vu *VUser,
	unit replayUnit,
	leafCounter *int,
	continueOnError bool,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
	report *ReplayReport,
	onProgress func(ReplayProgressEvent),
	debugController ReplayDebugController,
) (ReplayActionEvent, bool, bool) {
	if leafCounter != nil {
		unit.leafOrdinal = *leafCounter
		*leafCounter++
	}
	eventIndex := report.nextEventIndex()
	eventID := replayLeafEventID(report.ReplaySessionID, unit.phase, unit.actionIndex, eventIndex, unit.stepPath)
	debugAction := replayDebugActionFor(unit.action, unit.phase, unit.actionIndex, eventIndex)
	debugAction.EventID = eventID
	applyReplayUnitIdentityDebug(&debugAction, unit)
	scrubReplayDebugAction(&debugAction, vu.RedactionConfig)

	if debugController != nil {
		if err := debugController.BeforeAction(ctx, debugAction); err != nil {
			report.Success = false
			if report.Error == "" {
				report.Error = scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig)
			}
			return ReplayActionEvent{}, false, true
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

	var event ReplayActionEvent
	var shouldContinue bool
	if unit.synthErr != nil {
		now := time.Now()
		event = ReplayActionEvent{
			EventID:      eventID,
			ActionIndex:  unit.actionIndex,
			EventIndex:   eventIndex,
			Phase:        unit.phase,
			ActionName:   unit.action.Name,
			ActionType:   unit.action.Type,
			StartedAt:    now,
			CompletedAt:  now,
			Success:      false,
			ErrorMessage: scrubSensitiveTextWithConfig(unit.synthErr.Error(), vu.RedactionConfig),
		}
		applyReplaySourceMetadata(&event, unit.action.Metadata)
		attachReplayDiagnosis(&event)
	} else {
		_, captureCorrelationEvidence := debugController.(ReplayDebugControllerCorrelationObserver)
		event, shouldContinue = runReplayAction(ctx, vu, unit.action, unit.phase, unit.actionIndex, eventIndex, eventID, continueOnError, ignoreThinkTime, globalThinkCfg, report, captureCorrelationEvidence)
	}
	applyReplayUnitIdentityEvent(&event, unit)
	if observer, ok := debugController.(ReplayDebugControllerCorrelationObserver); ok {
		observer.ObserveCorrelationEvidence(ctx, debugAction, &event)
	}
	scrubReplayActionEvent(&event, vu.RedactionConfig)
	event = appendReplayReportEvent(report, event)

	actionProgressType := ReplayProgressActionCompleted
	if !event.Success {
		actionProgressType = ReplayProgressActionFailed
	}
	publishReplayProgress(onProgress, ReplayProgressEvent{
		Type:       actionProgressType,
		EventID:    eventID,
		SessionID:  report.ReplaySessionID,
		Timestamp:  event.CompletedAt,
		EventIndex: event.EventIndex,
		Action:     cloneReplayDebugAction(debugAction),
		Event:      cloneReplayActionEvent(event),
	})
	publishReplayProgress(onProgress, ReplayProgressEvent{
		Type:       ReplayProgressEventRun,
		EventID:    eventID,
		SessionID:  report.ReplaySessionID,
		Timestamp:  event.CompletedAt,
		EventIndex: event.EventIndex,
		Event:      cloneReplayActionEvent(event),
	})
	if after, ok := debugController.(ReplayDebugControllerAfter); ok {
		if err := after.AfterAction(ctx, debugAction, &event); err != nil {
			report.Success = false
			if report.Error == "" {
				report.Error = scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig)
			}
			scrubReplayActionEvent(&event, vu.RedactionConfig)
			return event, shouldContinue, true
		}
	}
	if !shouldContinue {
		report.Success = false
		if report.Error == "" {
			report.Error = event.ErrorMessage
		}
	}
	if ctx != nil && ctx.Err() != nil {
		report.Success = false
		if report.Error == "" {
			report.Error = ctx.Err().Error()
		}
		return event, false, true
	}
	return event, shouldContinue, false
}

// appendStepPath returns base + seg as a freshly-allocated slice so recursive
// callers never share (and clobber) a backing array.
func appendStepPath(base []string, seg string) []string {
	next := make([]string, len(base)+1)
	copy(next, base)
	next[len(base)] = seg
	return next
}

func applyReplayUnitIdentityDebug(action *ReplayDebugAction, unit replayUnit) {
	action.ParentActionName = unit.parentName
	action.parentBusinessTransaction = unit.parentBusinessTransaction
	action.RequestCount = unit.requestCount
	action.LeafOrdinal = unit.leafOrdinal
	if unit.requestIndex != nil {
		idx := *unit.requestIndex
		action.RequestIndex = &idx
	}
	if len(unit.stepPath) > 0 {
		action.StepPath = append([]string(nil), unit.stepPath...)
	}
	if len(unit.iterationPath) > 0 {
		action.IterationPath = append([]int(nil), unit.iterationPath...)
		innermost := unit.iterationPath[len(unit.iterationPath)-1]
		action.Iteration = &innermost
	}
}

func applyReplayUnitIdentityEvent(event *ReplayActionEvent, unit replayUnit) {
	event.ParentActionName = unit.parentName
	event.parentBusinessTransaction = unit.parentBusinessTransaction
	event.RequestCount = unit.requestCount
	event.LeafOrdinal = unit.leafOrdinal
	if unit.requestIndex != nil {
		idx := *unit.requestIndex
		event.RequestIndex = &idx
	}
	if len(unit.stepPath) > 0 {
		event.StepPath = append([]string(nil), unit.stepPath...)
	}
	if len(unit.iterationPath) > 0 {
		event.IterationPath = append([]int(nil), unit.iterationPath...)
		innermost := unit.iterationPath[len(unit.iterationPath)-1]
		event.Iteration = &innermost
	}
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
	continueOnError bool,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
	report *ReplayReport,
	captureCorrelationEvidence bool,
) (ReplayActionEvent, bool) {
	if handled, err := vu.handleEngineAction(ctx, action); handled {
		now := time.Now()
		success := err == nil
		errorMessage := ""
		if err != nil {
			if dsl.CanonicalActionType(action.Type) != "source_transaction" {
				vu.noteSourceTransactionResult(action, false)
			}
			errorMessage = scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig)
		}
		if err == nil {
			if ended, name, transactionSuccess := vu.sourceTransactionActionOutcome(action); ended && !transactionSuccess {
				success = false
				errorMessage = fmt.Sprintf("source transaction %q ended with failure", name)
			}
			if exitsIteration, failed := sourceMainIterationExit(action); exitsIteration && failed {
				success = false
				errorMessage = "source iteration exited with LR_FAIL"
			}
		}
		event := ReplayActionEvent{
			EventID:      eventID,
			ActionIndex:  actionIndex,
			EventIndex:   eventIndex,
			Phase:        phase,
			ActionName:   action.Name,
			ActionType:   action.Type,
			StartedAt:    now,
			CompletedAt:  now,
			Success:      success,
			ErrorMessage: errorMessage,
		}
		applyReplaySourceMetadata(&event, action.Metadata)
		if !event.Success {
			attachReplayDiagnosis(&event)
		}
		exitsIteration, _ := sourceMainIterationExit(action)
		if !exitsIteration && (err == nil || continueOnError) && ctx.Err() == nil {
			// The event's completed timestamp is captured before the wait so
			// response/action duration excludes think time, while the next replay
			// event still observes the source-ordered delay.
			if !applyReplayActionThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg) {
				event.Success = false
				event.ErrorMessage = replayThinkTimeCancellationError(ctx)
				attachReplayDiagnosis(&event)
			}
		}
		return event, event.Success
	}
	resolved := vu.resolveAction(action)
	start := time.Now()
	var streamedChildFailure *protocol.Result
	result, err := vu.executeWithRetryStream(ctx, resolved, vu.Script.Config.ErrorHandling, func(child *protocol.Result) error {
		if child == nil {
			return nil
		}
		vu.recordResult(child, vu.Script.Config.ErrorHandling, vu.Script.Config.Log)
		vu.noteSourceTransactionResult(action, child.Success)
		if !child.Success && streamedChildFailure == nil {
			failure := *child
			streamedChildFailure = &failure
		}
		return nil
	})
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
		Selector:    scrubReplaySelector(resolved, vu.RedactionConfig),
		Value:       scrubReplayValue(resolved, vu.RedactionConfig),
		Headers:     scrubReplayHeaders(resolved, vu.RedactionConfig),
		Body:        scrubReplayBody(resolved, vu.RedactionConfig),
		StartedAt:   start,
		CompletedAt: completedAt,
		DurationMs:  float64(completedAt.Sub(start).Microseconds()) / 1000,
	}
	applyReplaySourceMetadata(&event, action.Metadata)

	if err != nil {
		vu.noteSourceTransactionResult(action, false)
		event.CorrelationEvidence = replayCorrelationEvidence(resolved, result, action, captureCorrelationEvidence)
		discardCorrelationEvidence(result)
		event.Success = false
		event.ErrorMessage = scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig)
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
		if continueOnError && ctx.Err() == nil {
			_ = applyReplayActionThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg)
		}
		return event, false
	}

	if result == nil {
		vu.noteSourceTransactionResult(action, false)
		event.CorrelationEvidence = replayCorrelationEvidence(resolved, nil, action, captureCorrelationEvidence)
		event.Success = false
		event.ErrorMessage = fmt.Sprintf("%s %q returned no result", phase, action.Name)
		attachReplayDiagnosis(&event)
		if continueOnError && ctx.Err() == nil {
			_ = applyReplayActionThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg)
		}
		return event, false
	}

	// Detach the raw diagnosis-only exchange before recordResult. Result sinks are
	// metrics/publication boundaries and must never receive CorrelationEvidence,
	// even though the field is JSON-excluded. Capture ResponseBody now as well:
	// recordResult scrubs and bounds retained result evidence in place.
	event.CorrelationEvidence = replayCorrelationEvidence(resolved, result, action, captureCorrelationEvidence)
	discardCorrelationEvidence(result)
	vu.recordResult(result, vu.Script.Config.ErrorHandling, vu.Script.Config.Log)
	vu.noteSourceTransactionResult(action, result.Success)
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
	if streamedChildFailure != nil {
		event.Success = false
		event.ErrorMessage = streamedChildFailure.ErrorMessage
		if event.ErrorMessage == "" {
			event.ErrorMessage = fmt.Sprintf("streamed child %q failed", streamedChildFailure.ActionName)
		}
		event.ProtocolEvidence = protocol.BuildProtocolEvidence(resolved, streamedChildFailure)
		if streamedChildFailure.Snapshot != nil {
			event.Snapshot = streamedChildFailure.Snapshot
		}
	}
	if !result.Success && result.Snapshot != nil && vu.SnapshotStore != nil {
		if retained := vu.SnapshotStore.Snapshots(); len(retained) > 0 {
			// Reuse the store's detached bounded copy so ReplayReport.Events and
			// ReplayReport.Snapshots do not retain two copies of the same evidence.
			event.Snapshot = retained[len(retained)-1]
		}
	}
	deduplicateReplayBrowserSnapshot(&event)
	if result.Duration > 0 {
		event.DurationMs = float64(result.Duration.Microseconds()) / 1000
	}

	if !event.Success {
		if event.ErrorMessage == "" {
			event.ErrorMessage = fmt.Sprintf("status %d", result.StatusCode)
		}
		attachReplayDiagnosis(&event)
		if continueOnError {
			_ = applyReplayActionThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg)
		}
		return event, false
	}

	attachSelfHealSuggestion(&event)
	if !applyReplayActionThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg) {
		event.Success = false
		event.ErrorMessage = replayThinkTimeCancellationError(ctx)
		attachReplayDiagnosis(&event)
		return event, false
	}

	return event, true
}

func applyReplayActionThinkTime(
	ctx context.Context,
	vu *VUser,
	action dsl.Action,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
) bool {
	if ignoreThinkTime || !action.ThinkTimeAfter {
		// No wait was attempted, so an already-expired action/teardown context
		// must not be misreported as cancellation during think time.
		return true
	}
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
	delay := computeThinkTime(thinkCfg)
	elapsed, completed := sleepReplayThinkTime(ctx, delay)
	vu.noteSourceTransactionThinkTime(elapsed)
	// sleepReplayThinkTime owns the distinction between a completed wait and a
	// cancellation that interrupted one. Do not fold ambient context state back
	// in here: a configured wait that resolves to zero never started and must not
	// be mislabeled as "canceled during think time".
	return completed
}

func applyReplayContainerThinkTime(
	ctx context.Context,
	vu *VUser,
	action dsl.Action,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
	report *ReplayReport,
) bool {
	if applyReplayActionThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg) {
		return true
	}
	if report != nil {
		report.Success = false
		if report.Error == "" {
			report.Error = replayThinkTimeCancellationError(ctx)
		}
	}
	return false
}

func sleepReplayThinkTime(ctx context.Context, delay time.Duration) (time.Duration, bool) {
	return sleepCtxElapsed(ctx, delay)
}

func replayThinkTimeCancellationError(ctx context.Context) string {
	if ctx != nil && ctx.Err() != nil {
		return "replay canceled during think time: " + ctx.Err().Error()
	}
	return "replay canceled during think time"
}

// scrubReplayFinalVariables blanks variables whose NAME is generically sensitive
// (password, token, ...), which is longstanding behavior independent of where a
// script came from. It deliberately no longer blanks a variable merely because an
// import placed it in a credential-governed field: that value is script-sourced
// and the operator replaying the script already has it, so hiding it only makes
// the replay report less useful.
func scrubReplayFinalVariables(
	values map[string]string,
	config *RedactionConfig,
) map[string]string {
	if len(values) == 0 {
		return nil
	}
	scrubbed := make(map[string]string, len(values))
	for key, value := range values {
		if scrub.SensitiveName(key) {
			scrubbed[key] = redactedPayloadValue
			continue
		}
		scrubbed[key] = scrubSensitiveTextWithConfig(value, config)
	}
	return scrubbed
}

func deduplicateReplayBrowserSnapshot(event *ReplayActionEvent) {
	if event == nil || event.Snapshot == nil {
		return
	}
	snapshot := event.Snapshot
	if event.PageURL != "" {
		snapshot.PageURL = ""
	}
	if event.PageTitle != "" {
		snapshot.PageTitle = ""
	}
	if event.HTML != "" {
		snapshot.HTML = ""
	}
	if event.Screenshot != "" {
		snapshot.ScreenshotBase64 = ""
	}
	if len(event.Console) > 0 {
		snapshot.ConsoleEntries = nil
	}
	if len(event.Network) > 0 {
		snapshot.NetworkEntries = nil
	}
	if event.DOMSummary != nil {
		snapshot.DOMSummary = nil
	}
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

// replayLeafEventID extends the action event ID with the container step path for
// sub-request / sub-step leaves. A plain action (empty path) keeps the exact
// legacy ID, so non-container events are unchanged.
func replayLeafEventID(sessionID string, phase string, actionIndex int, eventIndex int, stepPath []string) string {
	base := replayActionEventID(sessionID, phase, actionIndex, eventIndex)
	if len(stepPath) == 0 {
		return base
	}
	return base + ":" + sanitizeReplayEventIDPart(strings.Join(stepPath, "/"))
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
			if script.Config.ErrorHandling.ContinueOnErrorEnabled() {
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

func scrubReplaySelector(action *protocol.Action, config *RedactionConfig) string {
	if action == nil {
		return ""
	}
	value := action.Selector
	if action.RedactedSelector != "" {
		value = action.RedactedSelector
	}
	return scrubSensitiveTextWithConfig(value, config)
}

func scrubReplayValue(action *protocol.Action, config *RedactionConfig) string {
	if action == nil {
		return ""
	}
	value := action.Value
	if action.RedactedValue != "" {
		value = action.RedactedValue
	}
	return scrubSensitiveTextWithConfig(value, config)
}

func replayCorrelationEvidence(action *protocol.Action, result *protocol.Result, authored dsl.Action, enabled bool) *protocol.CorrelationEvidence {
	if !enabled {
		return nil
	}
	if result != nil && result.CorrelationEvidence != nil && result.CorrelationEvidence.UnavailableReason != "" {
		return protocol.UnavailableCorrelationEvidence(result.CorrelationEvidence.UnavailableReason)
	}

	var (
		requestURL             string
		requestBody            string
		requestBodyBytes       []byte
		requestBodyTransformed bool
		requestHeaders         map[string]string
		useActionHeaders       bool
		responseBody           string
		responseHeaders        map[string]string
		responseStatus         int
		responseSuccess        *bool
	)
	if action != nil {
		requestURL = action.Target
		requestBodyBytes = action.Body
		useActionHeaders = true
	}
	if result != nil {
		responseBody = result.ResponseBody
		responseStatus = result.StatusCode
		if supplied := result.CorrelationEvidence; supplied != nil {
			if supplied.RequestURL != "" {
				requestURL = supplied.RequestURL
			}
			if supplied.RequestBody != "" {
				requestBody = supplied.RequestBody
				requestBodyBytes = nil
			}
			requestBodyTransformed = supplied.RequestBodyTransformed
			if supplied.RequestHeaders != nil {
				requestHeaders = supplied.RequestHeaders
				useActionHeaders = false
			}
			// Response fields are authoritative whenever a protocol supplies a
			// correlation snapshot, including a legitimately empty response body.
			responseBody = supplied.ResponseBody
			responseHeaders = supplied.ResponseHeaders
			if supplied.ResponseStatus != 0 {
				responseStatus = supplied.ResponseStatus
			}
			if supplied.ResponseSuccess != nil {
				value := *supplied.ResponseSuccess
				responseSuccess = &value
			}
		}
	}
	if !correlationEvidenceFitsCaptureLimit(requestURL, requestBody, requestBodyBytes, requestHeaders, action, useActionHeaders, authored, responseBody, responseHeaders) {
		return protocol.UnavailableCorrelationEvidence(protocol.CorrelationEvidenceTooLargeReason)
	}
	if useActionHeaders {
		requestHeaders = rawReplayRequestHeaders(action)
	}
	authoredHeaders := rawAuthoredReplayRequestHeaders(authored)
	if requestBody == "" && requestBodyBytes != nil {
		requestBody = string(requestBodyBytes)
	}
	evidence := &protocol.CorrelationEvidence{
		RequestURL:             requestURL,
		RequestBody:            requestBody,
		RequestBodyTransformed: requestBodyTransformed,
		RequestHeaders:         cloneStringMap(requestHeaders),
		AuthoredRequestURL:     authored.Target,
		AuthoredRequestBody:    authored.Body,
		AuthoredRequestHeaders: cloneStringMap(authoredHeaders),
		ResponseBody:           responseBody,
		ResponseHeaders:        cloneStringMap(responseHeaders),
		ResponseStatus:         responseStatus,
		ResponseSuccess:        responseSuccess,
	}
	if evidence.RequestURL == "" && evidence.RequestBody == "" && len(evidence.RequestHeaders) == 0 &&
		evidence.AuthoredRequestURL == "" && evidence.AuthoredRequestBody == "" && len(evidence.AuthoredRequestHeaders) == 0 &&
		evidence.ResponseBody == "" && len(evidence.ResponseHeaders) == 0 && evidence.ResponseStatus == 0 && evidence.ResponseSuccess == nil {
		return nil
	}
	return evidence
}

func correlationEvidenceFitsCaptureLimit(requestURL, requestBody string, requestBodyBytes []byte, requestHeaders map[string]string, action *protocol.Action, useActionHeaders bool, authored dsl.Action, responseBody string, responseHeaders map[string]string) bool {
	budget := correlationEvidenceCaptureBudget{remaining: protocol.MaxCorrelationEvidenceBytes}
	if !budget.consume(len(requestURL)) || !budget.consume(len(requestBody)) || !budget.consume(len(requestBodyBytes)) {
		return false
	}
	if useActionHeaders {
		if !budget.consumeProtocolHeaders(action) {
			return false
		}
	} else if !budget.consumeMap(requestHeaders) {
		return false
	}
	return budget.consume(len(authored.Target)) && budget.consume(len(authored.Body)) && budget.consumeDSLHeaders(authored) &&
		budget.consume(len(responseBody)) && budget.consumeMap(responseHeaders)
}

type correlationEvidenceCaptureBudget struct {
	remaining int
}

func (b *correlationEvidenceCaptureBudget) consume(size int) bool {
	if b == nil || size < 0 || size > b.remaining {
		return false
	}
	b.remaining -= size
	return true
}

func (b *correlationEvidenceCaptureBudget) consumeMap(values map[string]string) bool {
	for key, value := range values {
		if !b.consume(len(key)) || !b.consume(len(value)) {
			return false
		}
	}
	return true
}

type correlationHeaderSize struct {
	name       string
	valueBytes int
	count      int
}

func (b *correlationEvidenceCaptureBudget) consumeProtocolHeaders(action *protocol.Action) bool {
	if action == nil {
		return true
	}
	groups := make(map[string]*correlationHeaderSize, len(action.HeaderList))
	for _, header := range action.HeaderList {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		normalized := strings.ToLower(name)
		group := groups[normalized]
		if group == nil {
			group = &correlationHeaderSize{name: name}
			groups[normalized] = group
		}
		if len(header.Value) > protocol.MaxCorrelationEvidenceBytes-group.valueBytes {
			return false
		}
		group.valueBytes += len(header.Value)
		group.count++
	}
	for name, value := range action.Headers {
		if _, shadowed := groups[strings.ToLower(strings.TrimSpace(name))]; shadowed {
			continue
		}
		if !b.consume(len(name)) || !b.consume(len(value)) {
			return false
		}
	}
	return b.consumeHeaderGroups(groups)
}

func (b *correlationEvidenceCaptureBudget) consumeDSLHeaders(action dsl.Action) bool {
	groups := make(map[string]*correlationHeaderSize, len(action.HeaderList))
	for _, header := range action.HeaderList {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		normalized := strings.ToLower(name)
		group := groups[normalized]
		if group == nil {
			group = &correlationHeaderSize{name: name}
			groups[normalized] = group
		}
		if len(header.Value) > protocol.MaxCorrelationEvidenceBytes-group.valueBytes {
			return false
		}
		group.valueBytes += len(header.Value)
		group.count++
	}
	for name, value := range action.Headers {
		if _, shadowed := groups[strings.ToLower(strings.TrimSpace(name))]; shadowed {
			continue
		}
		if !b.consume(len(name)) || !b.consume(len(value)) {
			return false
		}
	}
	return b.consumeHeaderGroups(groups)
}

func (b *correlationEvidenceCaptureBudget) consumeHeaderGroups(groups map[string]*correlationHeaderSize) bool {
	for _, group := range groups {
		separatorBytes := group.count - 1
		if !b.consume(len(group.name)) || !b.consume(group.valueBytes) || !b.consume(separatorBytes) {
			return false
		}
	}
	return true
}

func rawAuthoredReplayRequestHeaders(action dsl.Action) map[string]string {
	headers := cloneStringMap(action.Headers)
	if len(action.HeaderList) == 0 {
		return headers
	}
	if headers == nil {
		headers = make(map[string]string)
	}
	type repeatedHeader struct {
		name   string
		values []string
	}
	groups := make(map[string]*repeatedHeader, len(action.HeaderList))
	order := make([]string, 0, len(action.HeaderList))
	for _, header := range action.HeaderList {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		normalized := strings.ToLower(name)
		group := groups[normalized]
		if group == nil {
			group = &repeatedHeader{name: name}
			groups[normalized] = group
			order = append(order, normalized)
		}
		group.values = append(group.values, header.Value)
	}
	for _, normalized := range order {
		group := groups[normalized]
		for existing := range headers {
			if strings.EqualFold(existing, group.name) {
				delete(headers, existing)
			}
		}
		headers[group.name] = strings.Join(group.values, "\n")
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// rawReplayRequestHeaders provides a best-effort request-side fallback for
// protocols that do not yet populate Result.CorrelationEvidence. HeaderList has
// wire-level precedence over the summary map and repeated occurrences are
// newline-delimited, matching the correlation engine's observation contract.
func rawReplayRequestHeaders(action *protocol.Action) map[string]string {
	if action == nil {
		return nil
	}
	headers := cloneStringMap(action.Headers)
	if len(action.HeaderList) == 0 {
		return headers
	}
	if headers == nil {
		headers = make(map[string]string)
	}
	type repeatedHeader struct {
		name   string
		values []string
	}
	groups := make(map[string]*repeatedHeader, len(action.HeaderList))
	order := make([]string, 0, len(action.HeaderList))
	for _, header := range action.HeaderList {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		normalized := strings.ToLower(name)
		group := groups[normalized]
		if group == nil {
			group = &repeatedHeader{name: name}
			groups[normalized] = group
			order = append(order, normalized)
		}
		group.values = append(group.values, header.Value)
	}
	for _, normalized := range order {
		group := groups[normalized]
		for existing := range headers {
			if strings.EqualFold(existing, group.name) {
				delete(headers, existing)
			}
		}
		headers[group.name] = strings.Join(group.values, "\n")
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func scrubReplayActionEvent(event *ReplayActionEvent, config *RedactionConfig) {
	if event == nil {
		return
	}
	// Transaction names are authored identity labels, not protocol evidence, and
	// must remain visible exactly as configured. Ordinary actions and
	// non-transaction parents retain the full scrubber.
	if replayActionHasTransactionIdentity(event.ActionType) {
		event.ActionName = scrubSensitiveIdentityWithConfig(event.ActionName, config)
	} else {
		event.ActionName = scrubSensitiveTextWithConfig(event.ActionName, config)
	}
	event.ActionType = scrubSensitiveTextWithConfig(event.ActionType, config)
	event.Method = scrubSensitiveTextWithConfig(event.Method, config)
	event.ErrorMessage = scrubSensitiveTextWithConfig(event.ErrorMessage, config)
	event.Target = scrubSensitiveTextWithConfig(event.Target, config)
	event.Selector = scrubSensitiveTextWithConfig(event.Selector, config)
	event.Value = scrubSensitiveTextWithConfig(event.Value, config)
	event.Headers = scrubSensitiveHeaders(event.Headers, config)
	event.Body = scrubSensitiveTextWithConfig(event.Body, config)
	event.PageURL = scrubSensitiveTextWithConfig(event.PageURL, config)
	event.PageTitle = scrubSensitiveTextWithConfig(event.PageTitle, config)
	event.HTML = scrubSensitiveTextWithConfig(event.HTML, config)
	event.Extractions = scrubSensitiveExtractions(event.Extractions, config)
	event.Console = scrubSensitiveConsoleEntries(event.Console, config)
	event.Network = scrubSensitiveNetworkEntries(event.Network, config)
	event.DOMSummary = scrubSensitiveDOMSummary(event.DOMSummary, config)
	event.ProtocolEvidence = scrubSensitiveEvidenceMap(event.ProtocolEvidence, config)
	if event.Snapshot != nil {
		projection := &protocol.Result{Snapshot: event.Snapshot}
		scrubSensitiveResultWithOptions(projection, config, false)
		event.Snapshot = projection.Snapshot
	}
	event.SourceStepID = scrubSensitiveTextWithConfig(event.SourceStepID, config)
	event.SourceAction = scrubSensitiveTextWithConfig(event.SourceAction, config)
	event.SourceActionName = scrubSensitiveTextWithConfig(event.SourceActionName, config)
	event.SourceTransaction = scrubSensitiveIdentityWithConfig(event.SourceTransaction, config)
	event.SourceProfile = scrubSensitiveTextWithConfig(event.SourceProfile, config)
	if event.parentBusinessTransaction {
		event.ParentActionName = scrubSensitiveIdentityWithConfig(event.ParentActionName, config)
	} else {
		event.ParentActionName = scrubSensitiveTextWithConfig(event.ParentActionName, config)
	}
	for index := range event.StepPath {
		event.StepPath[index] = scrubSensitiveTextWithConfig(event.StepPath[index], config)
	}
	// Raw correlation evidence has exactly one consumer: the optional observer
	// called immediately before this scrub. Never retain it in a report or any
	// downstream clone/publication path. Zero the detached value as well so even
	// an accidental retained event pointer cannot read it after this boundary.
	if event.CorrelationEvidence != nil {
		*event.CorrelationEvidence = protocol.CorrelationEvidence{}
	}
	event.CorrelationEvidence = nil
}

func scrubReplayDebugAction(action *ReplayDebugAction, config *RedactionConfig) {
	if action == nil {
		return
	}
	if replayActionHasTransactionIdentity(action.ActionType) {
		action.ActionName = scrubSensitiveIdentityWithConfig(action.ActionName, config)
	} else {
		action.ActionName = scrubSensitiveTextWithConfig(action.ActionName, config)
	}
	action.ActionType = scrubSensitiveTextWithConfig(action.ActionType, config)
	action.Method = scrubSensitiveTextWithConfig(action.Method, config)
	action.Target = scrubSensitiveTextWithConfig(action.Target, config)
	action.Selector = scrubSensitiveTextWithConfig(action.Selector, config)
	action.SourceStepID = scrubSensitiveTextWithConfig(action.SourceStepID, config)
	if action.parentBusinessTransaction {
		action.ParentActionName = scrubSensitiveIdentityWithConfig(action.ParentActionName, config)
	} else {
		action.ParentActionName = scrubSensitiveTextWithConfig(action.ParentActionName, config)
	}
	for index := range action.StepPath {
		action.StepPath[index] = scrubSensitiveTextWithConfig(action.StepPath[index], config)
	}
}

func replayActionHasTransactionIdentity(actionType string) bool {
	switch dsl.CanonicalActionType(actionType) {
	case "http_sequence", "source_transaction":
		return true
	default:
		return false
	}
}

func scrubReplayGroupName(groupName string, config *RedactionConfig) string {
	return scrubSensitiveIdentityWithConfig(groupName, config)
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
