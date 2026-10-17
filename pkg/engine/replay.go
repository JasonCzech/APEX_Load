package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

var replaySandboxSecretReference = regexp.MustCompile(`\{\{\s*secret\.`)

type ReplayOptions struct {
	// LiveTableSeeds are isolated ephemeral seeds; replay never accepts a live run client.
	LiveTableSeeds  []LiveTableSandboxSeed
	GroupName       string
	ReplaySessionID string
	ScriptRevision  string
	RuntimeProfile  *ReplayRuntimeProfile
	Variables       map[string]string
	RedactionRules  []RedactionRule
	SecretResolver  SecretResolver
	IgnoreThinkTime bool
	// RecordHAR records a native HAR of a browser replay through the
	// browser's own capture (owner, scrub and publication path), whatever the
	// script's own capture policy.
	RecordHAR bool
	// DiscardHAR marks a caller that handles a replay's HARs: the report
	// hands over every HAR the browser published (TakeHARArtifacts) once it
	// has closed, whether RecordHAR or the script's own capture policy
	// recorded it, and DiscardHAR receives one that no report carries (the
	// replay returned an error after the recording opened), so nothing is
	// left in the HAR directory.
	DiscardHAR func([]protocol.BrowserHARArtifact)
	// Seed, when set, makes random data selection and {{fn.random}}/{{fake.*}}
	// reproducible across replays. Nil uses defaultReplaySeed.
	Seed *int64
	// RunID is the value {{run.id}} resolves to. Empty leaves it unresolved,
	// and an action that uses it fails closed, as in a direct run.
	RunID string
	// ContinueOnError decides what follows a failed action the debugger does
	// not pause on. Nil uses the script's own setting.
	ContinueOnError *bool
	OnProgress      func(ReplayProgressEvent)
	DebugController ReplayDebugController
}

type ReplayProgressEvent struct {
	RuntimeEvidenceUnavailable bool `json:"runtime_evidence_unavailable,omitempty"`
	Type                       string
	EventID                    string
	SessionID                  string
	Timestamp                  time.Time
	EventIndex                 int
	Action                     *ReplayDebugAction
	Event                      *ReplayActionEvent
	Report                     *ReplayReport
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
	RuntimeEvidenceUnavailable  bool                      `json:"runtime_evidence_unavailable,omitempty"`
	Stopped                     bool                      `json:"stopped,omitempty"`
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

	messageDetailBudget *replaySnapshotBudget
	// harArtifacts are the native HARs a replay's browser published (RecordHAR
	// or the script's own capture policy), for a caller that handles them
	// (DiscardHAR); never serialized, since they name local files.
	harArtifacts            []protocol.BrowserHARArtifact
	totalEventCount         int
	retainedEventJSONBytes  int
	eventJSONBytes          []int
	consumedEvidenceBytes   int
	evidenceProjectionCount int
	manifestSnapshots       map[*protocol.ErrorSnapshot]struct{}
	retainedSnapshotClones  map[*protocol.ErrorSnapshot]*protocol.ErrorSnapshot
}

type ReplayActionEvent struct {
	ResponseBodyState string                   `json:"response_body_state,omitempty"`
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
	if err := nativeScriptExecutionHostError(script); err != nil {
		return nil, err
	}
	if err := ValidateParameterAllocationTopology(script); err != nil {
		return nil, err
	}
	if err := importExecutionGateError(script, "replay"); err != nil {
		return nil, err
	}

	aliases, err := dsl.ScriptLiveTableAliases(script)
	if err != nil {
		return nil, err
	}
	if len(aliases) > 0 && len(opts.LiveTableSeeds) == 0 {
		return nil, fmt.Errorf("live_table replay requires explicit isolated sandbox seeds")
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
	// Inline sandbox contents are executable data, not evidence. Reject managed
	// secrets in values, schema and identities before initializing them; redacting
	// any of these strings would change the input. Imported literals keep their policy.
	managedValues := allRuntimeSecretValues(redactionConfig)
	importedValues := allRuntimeScriptSourcedValues(redactionConfig)
	containsManagedSecret := func(value string) bool {
		if replaySandboxSecretReference.MatchString(value) {
			return true
		}
		for secret := range managedValues {
			if _, imported := importedValues[secret]; !imported && strings.Contains(value, secret) {
				return true
			}
		}
		return false
	}
	for _, seed := range opts.LiveTableSeeds {
		if containsManagedSecret(seed.Alias) {
			return nil, fmt.Errorf("managed secrets cannot seed isolated live tables")
		}
		for _, column := range seed.Columns {
			if containsManagedSecret(column) {
				return nil, fmt.Errorf("managed secrets cannot seed isolated live tables")
			}
		}
		for _, row := range seed.Rows {
			if containsManagedSecret(row.ID) {
				return nil, fmt.Errorf("managed secrets cannot seed isolated live tables")
			}
			for key, value := range row.Values {
				if containsManagedSecret(key) || containsManagedSecret(value) {
					return nil, fmt.Errorf("managed secrets cannot seed isolated live tables")
				}
			}
		}
	}
	liveClient, liveBindings, liveClientID, err := NewLiveTableSandbox(opts.LiveTableSeeds)
	if err != nil {
		return nil, fmt.Errorf("initialize isolated replay tables: %w", err)
	}
	if err := validateLiveTableBindings(script, liveClient, liveClientID, liveBindings); err != nil {
		return nil, err
	}
	groupName = scrubReplayGroupName(groupName, redactionConfig)

	proto, err := protocol.Get(script.Protocol)
	if err != nil {
		return nil, fmt.Errorf("protocol init: %s", scrubSensitiveTextWithConfig(err.Error(), redactionConfig))
	}

	_, captureCorrelationEvidence := opts.DebugController.(ReplayDebugControllerCorrelationObserver)
	protoConfig := buildProtoConfigForRunModeWithSecretResolver(script, "replay", secretResolver)
	// A replay runs as the session's first identity.
	identity, err := browserIdentityForOrdinal(script, 0)
	if err == nil && identity != nil {
		protoConfig, err = withBrowserIdentityConfig(protoConfig, identity, script)
		if err == nil {
			script, err = scriptForBrowserIdentity(script, identity)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("browser session identity: %s", scrubSensitiveTextWithConfig(err.Error(), redactionConfig))
	}
	requiredFeatures := dsl.RequiredNativeRuntimeFeatures(script)
	if protocol.RuntimeFeaturesSatisfy(requiredFeatures, protocol.FeatureNativeDataJavaV1) {
		targets, err := runtimePreflightTargets(ctx, script, secretResolver)
		if err != nil {
			return nil, fmt.Errorf("native runtime preflight: %s", scrubSensitiveTextWithConfig(err.Error(), redactionConfig))
		}
		_, supported, err := protocol.RunRuntimePreflight(ctx, proto, protocol.RuntimePreflightRequest{Config: protoConfig, Targets: targets, RequiredFeatures: requiredFeatures})
		if err != nil {
			return nil, fmt.Errorf("native runtime preflight: %s", scrubSensitiveTextWithConfig(err.Error(), redactionConfig))
		}
		if !supported {
			return nil, nativeServiceUnavailable("native Java preflight unavailable")
		}
	}
	if captureCorrelationEvidence {
		protoConfig[protocol.CorrelationEvidenceCaptureConfigKey] = true
	}
	_, harProvider := proto.(protocol.BrowserHARArtifactProvider)
	recordsHAR := harProvider && opts.RecordHAR
	// A caller that handles HARs is handed whatever the browser published,
	// so a script's own capture policy cannot leave a replay's HAR behind.
	collectsHAR := harProvider && (opts.RecordHAR || opts.DiscardHAR != nil)
	if recordsHAR {
		// Both aliases: the policy key is applied after the plain one, so a
		// script's own "on_error" policy would otherwise discard the
		// recording of a successful replay.
		protoConfig["browser_har_capture"] = "always"
		protoConfig["browser_har_capture_policy"] = "always"
	}
	// discardUnreportedHAR hands a recording no report carries to the caller.
	reported := false
	discardUnreportedHAR := func() {
		if !collectsHAR || reported || opts.DiscardHAR == nil {
			return
		}
		if provider, ok := proto.(protocol.BrowserHARArtifactProvider); ok {
			if artifacts := takeBrowserHARArtifacts(provider); len(artifacts) > 0 {
				opts.DiscardHAR(artifacts)
			}
		}
	}
	bindProtocolRuntimeRedaction(proto, func() *RedactionConfig { return redactionConfig })
	if err := callProtocolInit(ctx, proto, protoConfig, redactionConfig); err != nil {
		runProtocolTeardown(proto, func() *RedactionConfig { return redactionConfig })
		discardUnreportedHAR()
		return nil, fmt.Errorf("protocol init: %s", err)
	}
	var vu *VUser
	// The browser closes once: at the end of a replay, so that a recorded HAR
	// is published before the report is returned, or on an early return.
	var teardownOnce sync.Once
	teardownProtocol := func() {
		teardownOnce.Do(func() {
			runProtocolTeardown(proto, func() *RedactionConfig {
				if vu != nil {
					return vu.resultPublicationRedactionConfig()
				}
				return redactionConfig
			})
		})
	}
	defer func() {
		teardownProtocol()
		discardUnreportedHAR()
	}()

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
	if policy := script.Config.Results.MessageDetail; policy != nil && strings.ToLower(strings.TrimSpace(policy.Mode)) != "off" {
		report.messageDetailBudget = &replaySnapshotBudget{
			maxItems:       normalizedMessageDetailMaxItems(policy.MaxItems),
			maxItemBytes:   int(normalizedMessageDetailMaxItemBytes(policy.MaxItemBytes)),
			remainingBytes: int(normalizedMessageDetailMaxTotalBytes(policy.MaxTotalBytes)),
		}
	}
	publishReplayProgress(opts.OnProgress, ReplayProgressEvent{
		EventID:    replaySessionEventID(replaySessionID, "started"),
		SessionID:  replaySessionID,
		Type:       ReplayProgressStarted,
		Timestamp:  report.StartedAt,
		EventIndex: -1,
		Report:     cloneReplayReport(report),
	})

	vu = &VUser{
		ParameterAllocation:          &ParameterAllocation{Population: 1, Offset: 0, Iterations: 1},
		ID:                           0,
		Group:                        groupName,
		RunID:                        opts.RunID,
		Proto:                        proto,
		Script:                       script,
		browserIdentity:              identity,
		Metrics:                      NewMetricsCollector(16),
		Variables:                    make(map[string]string),
		DataProvider:                 dataProvider,
		LiveTableClient:              liveClient,
		LiveTableClientID:            liveClientID,
		LiveTableBindings:            liveBindings,
		SecretResolver:               secretResolver,
		RedactionConfig:              redactionConfig,
		importedCredentialInspection: importedCredentials,
		SnapshotStore:                NewSnapshotStore(128),
	}
	if replayRuntimeEnabled(opts.DebugController) {
		vu.replayRuntime = &replayRuntimeState{rows: make(map[string]ReplayRuntimeRow)}
	}
	defer vu.closeErrorLogs()
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

	if err := vu.prepareRuntimeState(); err != nil {
		return nil, fmt.Errorf("prepare replay data: %w", err)
	}
	defer vu.closeLiveTables()
	vu.startLiveTableRenewal(ctx)
	for key, value := range opts.Variables {
		vu.Variables[key] = value
	}
	if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
		return nil, fmt.Errorf("prepare replay credential redaction: %s", err)
	}

	onProgress := vu.guardedReplayProgress(report, opts.OnProgress)
	continueOnError := script.Config.ErrorHandling.ContinueOnErrorEnabled()
	if opts.ContinueOnError != nil {
		continueOnError = *opts.ContinueOnError
	}
	globalThinkCfg := protocol.ThinkTimeConfig{
		Type:       script.Config.ThinkTime.Type,
		MinPercent: script.Config.ThinkTime.MinPercent,
		MaxPercent: script.Config.ThinkTime.MaxPercent,
		MeanMs:     script.Config.ThinkTime.MeanMs,
		StdMs:      script.Config.ThinkTime.StdMs,
		MinMs:      script.Config.ThinkTime.MinMs,
		MaxMs:      script.Config.ThinkTime.MaxMs,
		Multiplier: script.Config.ThinkTime.Multiplier,
		LimitMs:    script.Config.ThinkTime.LimitMs,
		Ignore:     script.Config.ThinkTime.Ignore,
	}

	vu.onAsyncResult = func(result *protocol.Result) { appendReplayAsyncEvent(vu, report, result) }
	shouldContinue := runReplayPhase(ctx, vu, script.Init, "init", false, opts.IgnoreThinkTime, globalThinkCfg, report, onProgress, opts.DebugController)
	if shouldContinue {
		if controller, ok := vu.Proto.(protocol.IterationController); ok {
			if err := controller.BeginIteration(ctx); err != nil {
				report.Success = false
				report.Error = "prepare replay iteration: " + scrubSensitiveError(err, vu.RedactionConfig).Error()
				shouldContinue = false
			}
		}
		if shouldContinue {
			shouldContinue = runReplayPhaseInOrder(ctx, vu, script.Actions, vu.iterationOrder(), "action", continueOnError, opts.IgnoreThinkTime, globalThinkCfg, report, onProgress, opts.DebugController)
		}
	}
	vu.stopAllAsyncConversations(ctx, "replay end")
	teardownCtx, teardownCancel := teardownContext()
	runReplayPhase(teardownCtx, vu, script.Teardown, "teardown", true, opts.IgnoreThinkTime, globalThinkCfg, report, onProgress, opts.DebugController)
	teardownCancel()
	vu.stopAllAsyncConversations(teardownCtx, "replay end")
	finalizedSourceTransactions := vu.finalizeOpenSourceTransactions("source transaction ended without a matching end marker during replay")
	appendReplaySourceTransactionFinalizations(report, finalizedSourceTransactions, vu.RedactionConfig, onProgress)

	report.CompletedAt = time.Now()
	if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
		report.Success = false
		if report.Error == "" {
			report.Error = "runtime credential redaction failed"
		}
	}
	report.FinalVariables = vu.replayFinalVariables()
	if vu.runtimeCredentialRedactionErr != nil {
		OmitUnavailableReplayReport(report)
	} else {
		setReplayReportSnapshots(report)
		report.RepairSuggestions = collectReplayRepairSuggestions(report.Events)
	}
	if report.ArtifactManifest != nil {
		report.ArtifactManifest.CreatedAt = report.CompletedAt
	}
	if !shouldContinue && report.Error == "" && !report.Success {
		report.Error = "replay stopped after action failure"
	}
	publishReplayProgress(onProgress, ReplayProgressEvent{
		EventID:    replaySessionEventID(replaySessionID, "completed"),
		SessionID:  replaySessionID,
		Type:       ReplayProgressComplete,
		Timestamp:  report.CompletedAt,
		EventIndex: -1,
		Report:     cloneReplayReport(report),
	})
	if collectsHAR {
		teardownProtocol()
		if provider, ok := proto.(protocol.BrowserHARArtifactProvider); ok {
			report.harArtifacts = takeBrowserHARArtifacts(provider)
		}
	}
	reported = true
	return report, nil
}

// appendReplayAsyncEvent reports one background conversation response
// (http_async_start) as an "async" replay event. A failed response fails the
// replay without stopping it, as a failed response does in a load test.
func appendReplayAsyncEvent(vu *VUser, report *ReplayReport, result *protocol.Result) {
	event := ReplayActionEvent{
		EventIndex:  report.nextEventIndex(),
		ActionIndex: -1,
		Phase:       "async",
		ActionName:  result.ActionName,
		ActionType:  "http_async",
		StartedAt:   result.StartTime,
		CompletedAt: result.StartTime.Add(result.Duration),
		DurationMs:  float64(result.Duration.Microseconds()) / 1000,
		StatusCode:  result.StatusCode,
		Success:     result.Success,
	}
	if !result.Success {
		report.Success = false
		message := result.ErrorMessage
		if message == "" {
			message = "async response failed"
		}
		event.ErrorMessage = scrubSensitiveError(errors.New(message), vu.resultPublicationRedactionConfig()).Error()
	}
	appendReplayReportEvent(report, event)
}

// TakeHARArtifacts hands over the native HARs a replay recorded, once.
func (r *ReplayReport) TakeHARArtifacts() []protocol.BrowserHARArtifact {
	if r == nil {
		return nil
	}
	out := r.harArtifacts
	r.harArtifacts = nil
	return out
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
	prepared                  *preparedHTTPChild
	skipDebugBoundary         bool
	synthErr                  error // when set, emit a failed event instead of executing (e.g. parse error)
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
	return runReplayPhaseInOrder(ctx, vu, actions, nil, phase, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
}

// runReplayPhaseInOrder runs the actions at the given indexes (all of them,
// in order, when order is nil); run-logic blocks choose the order.
func runReplayPhaseInOrder(
	ctx context.Context,
	vu *VUser,
	actions []dsl.Action,
	order []int,
	phase string,
	continueOnError bool,
	ignoreThinkTime bool,
	globalThinkCfg protocol.ThinkTimeConfig,
	report *ReplayReport,
	onProgress func(ReplayProgressEvent),
	debugController ReplayDebugController,
) bool {
	defer vu.pushReplayRuntimeFrame(ReplayRuntimeFrame{Kind: "phase", Phase: phase, ActionIndex: -1})()
	if order == nil {
		order = make([]int, len(actions))
		for index := range order {
			order[index] = index
		}
	}
	// An observer whose session signs its browser in skips the recorded
	// sign-in; the other steps keep their positions.
	var signIn *recordedSignInPlan
	if strings.EqualFold(strings.TrimSpace(phase), "init") {
		signIn = newRecordedSignInPlan(vu.Script)
	}
	for _, actionIndex := range order {
		action := actions[actionIndex]
		if signIn.skips(actionIndex) {
			continue
		}
		action = signIn.action(action)
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

	if kind := replayContainerKind(action); kind != "" {
		if kind == "control_parallel" {
			kind = "parallel"
		}
		if kind == "control_loop" && action.Control != nil && action.Control.ForEach != nil {
			kind = "foreach"
		}
		defer vu.pushReplayRuntimeFrame(ReplayRuntimeFrame{Kind: kind, Phase: phase, ActionIndex: actionIndex, StepPath: stepPath, IterationPath: iterationPath})()
	}
	switch replayContainerKind(action) {
	case "http_sequence":
		mode, _, modeErr := dsl.HTTPSequenceExecutionSettings(action)
		if modeErr == nil && mode == dsl.HTTPSequenceConcurrent {
			return runReplayConcurrentHTTP(ctx, vu, action, phase, actionIndex, stepPath, iterationPath, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
		}
		children, err := httpSequenceChildActions(action)
		if modeErr != nil {
			err = modeErr
		}
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
		publishConditionEvidence(vu, onProgress, report, action, phase, actionIndex, stepPath, iterationPath, evidence)
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
			publishConditionEvidence(vu, onProgress, report, action, phase, actionIndex, stepPath, iterationPath, evidence)
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
		if parentVariables == nil {
			parentVariables = make(map[string]string)
		}
		var parentRuntimeRows map[string]ReplayRuntimeRow
		if vu.replayRuntime != nil {
			parentRuntimeRows = cloneReplayRuntimeRows(vu.replayRuntime.rows)
		}
		parentExtracted := cloneIntMap(vu.extractedVariables)
		parentExtractedBytes := vu.extractedVariableBytes
		parentRestored := false
		restoreParent := func() {
			if parentRestored {
				return
			}
			vu.Variables = parentVariables
			if vu.replayRuntime != nil {
				vu.replayRuntime.rows = parentRuntimeRows
			}
			vu.extractedVariables = parentExtracted
			vu.extractedVariableBytes = parentExtractedBytes
			parentRestored = true
		}
		// Debugger stops and cancellations still run teardown. It must observe
		// parent state rather than the interrupted branch's private variables.
		defer restoreParent()
		type branchExtractions struct {
			name   string
			values map[string]string
		}
		merged := make([]branchExtractions, 0, len(action.Control.Branches))
		success := true
		for branchIndex, branch := range action.Control.Branches {
			if vu.replayRuntime != nil {
				vu.replayRuntime.rows = cloneReplayRuntimeRows(parentRuntimeRows)
			}
			vu.Variables = cloneStringMap(parentVariables)
			if vu.Variables == nil {
				vu.Variables = make(map[string]string)
			}
			vu.extractedVariables = make(map[string]int)
			vu.extractedVariableBytes = 0
			branchAborted := func() bool {
				index := branchIndex
				defer vu.pushReplayRuntimeFrame(ReplayRuntimeFrame{Kind: "parallel_branch", Phase: phase, ActionIndex: actionIndex, StepPath: stepPath, IterationPath: iterationPath, BranchIndex: &index})()
				for stepIndex, child := range branch.Steps {
					childPath := appendStepPath(stepPath, fmt.Sprintf("branches[%d].steps[%d]", branchIndex, stepIndex))
					flow, abort := expandReplayAction(ctx, vu, child, phase, actionIndex, depth+1, childPath, iterationPath, leafCounter, continueOnError, ignoreThinkTime, globalThinkCfg, report, onProgress, debugController)
					if abort {
						return true
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
				return false
			}()
			if branchAborted {
				return replayStop, true
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
		restoreParent()
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
		cursor := newControlLoopCursor(vu, loop)
		// Replay keeps bindings visible through container think time.
		defer cursor.restoreBindings()

		// runPass replays loop.Steps once for a given iteration path, returning the
		// loop-level signal (next/break/continue) or replayStop when a child failed
		// under !continueOnError. abort halts the whole replay.
		runPass := func(childIterPath []int) (replayFlow, bool) {
			defer vu.enterReplayRuntimeIteration(childIterPath)()
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

		for cursor.pending() {
			if ctx.Err() != nil {
				return replayStop, true
			}
			pass, ok := cursor.next()
			if !ok {
				break
			}
			if loop.ForEach != nil {
				vu.recordReplayRuntimeRow("foreach", strings.TrimSpace(loop.ForEach.Source), strings.TrimSpace(loop.ForEach.Var), pass.rowReceipt)
			}
			childIterPath := append(append([]int(nil), iterationPath...), pass.index)
			if pass.checkCondition {
				condOK, evidence := vu.evalConditionEvidence(pass.condition)
				publishConditionEvidence(vu, onProgress, report, action, phase, actionIndex, stepPath, childIterPath, evidence)
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
	vu *VUser,
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
	scrubReplayDebugAction(dbg, vu.resultPublicationRedactionConfig())
	if vu.resultPublicationUnavailable() {
		omitUnredactableReplayDebugAction(dbg)
		ev.Expr, ev.Left, ev.Right, ev.Op = "", "", "", ""
	} else {
		ev.Expr = scrubSensitiveTextWithConfig(ev.Expr, vu.resultPublicationRedactionConfig())
		ev.Left = scrubSensitiveTextWithConfig(ev.Left, vu.resultPublicationRedactionConfig())
		ev.Right = scrubSensitiveTextWithConfig(ev.Right, vu.resultPublicationRedactionConfig())
	}
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
	runtimeLocation := *cloneReplayDebugAction(debugAction)
	defer vu.enterReplayRuntimeEvent(eventID)()
	scrubReplayDebugAction(&debugAction, vu.resultPublicationRedactionConfig())
	if vu.runtimeCredentialRedactionErr != nil {
		omitUnredactableReplayDebugAction(&debugAction)
	}
	if vu.resultPublicationUnavailable() {
		debugAction.Target, debugAction.Selector, debugAction.SourceStepID = "", "", ""
		debugAction.ActionName = "Concurrent request"
	}

	if debugController != nil && !unit.skipDebugBoundary {
		if err := vu.replayBeforeBoundary(ctx, debugController, debugAction, runtimeLocation); err != nil {
			report.Success = false
			if report.Error == "" {
				report.Error = vu.replayPublicationError(err)
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
			ErrorMessage: scrubSensitiveTextWithConfig(unit.synthErr.Error(), vu.resultPublicationRedactionConfig()),
		}
		applyReplaySourceMetadata(&event, unit.action.Metadata)
		attachReplayDiagnosis(&event)
	} else if unit.prepared != nil {
		child := unit.prepared
		_, capture := debugController.(ReplayDebugControllerCorrelationObserver)
		event, shouldContinue = finishReplayProtocolAction(ctx, vu, unit.action, child.action, child.result, child.err, child.started, child.completed, unit.phase, unit.actionIndex, eventIndex, eventID, continueOnError, true, globalThinkCfg, report, capture, nil)
	} else {
		_, captureCorrelationEvidence := debugController.(ReplayDebugControllerCorrelationObserver)
		event, shouldContinue = runReplayAction(ctx, vu, unit.action, unit.phase, unit.actionIndex, eventIndex, eventID, continueOnError, ignoreThinkTime, globalThinkCfg, report, captureCorrelationEvidence)
	}
	applyReplayUnitIdentityEvent(&event, unit)
	if vu.runtimeCredentialRedactionErr != nil {
		omitUnredactableReplayActionEvent(&event)
		omitUnredactableReplayDebugAction(&debugAction)
	}
	if observer, ok := debugController.(ReplayDebugControllerCorrelationObserver); ok {
		observer.ObserveCorrelationEvidence(ctx, debugAction, &event)
	}
	scrubReplayActionEvent(&event, vu.resultPublicationRedactionConfig())
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
	if debugController != nil && !unit.skipDebugBoundary {
		if err := vu.replayAfterBoundary(ctx, debugController, debugAction, runtimeLocation, &event); err != nil {
			report.Success = false
			if report.Error == "" {
				report.Error = vu.replayPublicationError(err)
			}
			scrubReplayActionEvent(&event, vu.resultPublicationRedactionConfig())
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
			Extractions:  vu.liveTableReplayOutputs(action),
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
	if err := vu.beforeWebSocketAction(ctx, action.Type); err != nil {
		event := ReplayActionEvent{EventID: eventID, ActionIndex: actionIndex, EventIndex: eventIndex, Phase: phase, ActionName: action.Name, ActionType: action.Type}
		event.Success = false
		event.ErrorMessage = scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig)
		attachReplayDiagnosis(&event)
		return event, false
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
	vu.acceptWebSocketCallback(action, result, err)
	completedAt := time.Now()
	return finishReplayProtocolAction(ctx, vu, action, resolved, result, err, start, completedAt, phase, actionIndex, eventIndex, eventID, continueOnError, ignoreThinkTime, globalThinkCfg, report, captureCorrelationEvidence, streamedChildFailure)
}

func finishReplayProtocolAction(ctx context.Context, vu *VUser, action dsl.Action, resolved *protocol.Action, result *protocol.Result, err error, start, completedAt time.Time, phase string, actionIndex, eventIndex int, eventID string, continueOnError, ignoreThinkTime bool, globalThinkCfg protocol.ThinkTimeConfig, report *ReplayReport, captureCorrelationEvidence bool, streamedChildFailure *protocol.Result) (ReplayActionEvent, bool) {
	event := ReplayActionEvent{
		EventID:     eventID,
		ActionIndex: actionIndex,
		EventIndex:  eventIndex,
		Phase:       phase,
		ActionName:  action.Name,
		ActionType:  action.Type,
		Method:      resolved.Method,
		Target:      scrubReplayTarget(resolved, vu.resultPublicationRedactionConfig()),
		Selector:    scrubReplaySelector(resolved, vu.resultPublicationRedactionConfig()),
		Value:       scrubReplayValue(resolved, vu.resultPublicationRedactionConfig()),
		Headers:     scrubReplayHeaders(resolved, vu.resultPublicationRedactionConfig()),
		Body:        scrubReplayBody(resolved, vu.resultPublicationRedactionConfig()),
		StartedAt:   start,
		CompletedAt: completedAt,
		DurationMs:  float64(completedAt.Sub(start).Microseconds()) / 1000,
	}
	applyReplaySourceMetadata(&event, action.Metadata)
	if vu.resultPublicationUnavailable() {
		event.Target, event.Selector, event.Value, event.Body, event.Headers = "", "", "", "", nil
		event.ActionName = "Concurrent request"
		event.SourceStepID, event.SourceAction, event.SourceActionName, event.SourceProfile = "", "", "", ""
	}

	if err != nil {
		vu.noteSourceTransactionResult(action, false)
		event.CorrelationEvidence = replayCorrelationEvidence(resolved, result, action, captureCorrelationEvidence)
		discardCorrelationEvidence(result)
		event.Success = false
		event.ErrorMessage = vu.replayPublicationError(err)
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
	logCfg := vu.Script.Config.Log
	logAction, logName := resolved, action.Name
	if vu.resultPublicationUnavailable() {
		logAction, logName = nil, "Concurrent request"
	}
	logger := vu.makeLogger(logCfg)
	if !result.Success {
		logger.Error("replay action failed", actionLogAttrs(logName, logAction, result, logCfg, true, vu.resultPublicationRedactionConfig())...)
	} else if logCfg.OnErrorOnly || logCfg.Level == "extended" {
		logger.Info("replay action ok", actionLogAttrs(logName, logAction, result, logCfg, false, vu.resultPublicationRedactionConfig())...)
	}
	retainedSnapshotCount := len(vu.SnapshotStore.Snapshots())
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
	if streamedChildFailure == nil && !result.Success && result.Snapshot != nil && vu.SnapshotStore != nil {
		if retained := vu.SnapshotStore.Snapshots(); len(retained) > retainedSnapshotCount {
			// Reuse the store's detached bounded copy so ReplayReport.Events and
			// ReplayReport.Snapshots do not retain two copies of the same evidence.
			event.Snapshot = retained[len(retained)-1]
		}
	}
	if event.Snapshot != nil {
		event.ResponseBodyState = event.Snapshot.ResponseBodyState
	} else {
		event.ResponseBodyState = "not_captured"
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
	if (ignoreThinkTime && !action.ThinkTime.Force) || !action.ThinkTimeAfter {
		// No wait was attempted, so an already-expired action/teardown context
		// must not be misreported as cancellation during think time. A forced
		// delay (lr_force_think_time) waits even when replay ignores think time,
		// as it does in VuGen.
		return true
	}
	thinkCfg := resolveActionThinkTime(globalThinkCfg, action.ThinkTime)

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
	// Structural Aura bodies have contextual template semantics even when an
	// all-literal replay is byte-identical to the draft. Raw repair offsets do
	// not attest those semantics or maintain occurrence contracts.
	requestBodyTransformed = requestBodyTransformed || actionBodyTemplateValueEncoding(authored.Metadata) == dsl.ImportAuraJSONTemplateValueEncoding
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
	if config != nil && config.runtimePublicationUnavailable {
		omitUnredactableReplayActionEvent(event)
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
	if config != nil && config.runtimePublicationUnavailable {
		omitUnredactableReplayDebugAction(action)
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

// A failed classification cannot provide a complete matcher. Preserve only
// fixed/numeric replay identity and outcome evidence, including engine-only
// actions that may continue without executing network traffic.
// OmitUnavailableReplayDebugAction removes authored/detail text while retaining
// bounded structural phase, path, ordinal and action-type identity.
func OmitUnavailableReplayDebugAction(action *ReplayDebugAction) {
	omitUnredactableReplayDebugAction(action)
}

func omitUnredactableReplayDebugAction(action *ReplayDebugAction) {
	if action == nil {
		return
	}
	action.ActionName = "Action evidence unavailable"
	action.Target, action.Selector, action.SourceStepID = "", "", ""
	action.ParentActionName = ""
	action.EventID = truncateReplayScalar(action.EventID, maxReplayIdentityBytes)
	action.Phase = truncateReplayScalar(action.Phase, maxReplayIdentityBytes)
	action.ActionType = truncateReplayScalar(action.ActionType, maxReplayIdentityBytes)
	action.Method = truncateReplayScalar(action.Method, maxReplayIdentityBytes)
	action.StepPath = append([]string(nil), action.StepPath[:min(len(action.StepPath), maxReplayPathDepth)]...)
	for i := range action.StepPath {
		action.StepPath[i] = truncateReplayScalar(action.StepPath[i], maxReplayIdentityBytes)
	}
	action.IterationPath = append([]int(nil), action.IterationPath[:min(len(action.IterationPath), maxReplayPathDepth)]...)
}

func omitUnredactableReplayActionEvent(event *ReplayActionEvent) {
	if event == nil {
		return
	}
	// Event/phase/path identifiers are structural, independent of authored data.
	event.ActionName = "Action evidence unavailable"
	if event.ErrorMessage != "" {
		event.ErrorMessage = "replay error detail unavailable"
	}
	event.Target, event.Selector, event.Value, event.Body = "", "", "", ""
	event.Headers, event.Extractions = nil, nil
	event.PageURL, event.PageTitle, event.HTML, event.Screenshot = "", "", "", ""
	event.Console, event.Network, event.DOMSummary, event.Snapshot, event.ClientMetrics = nil, nil, nil, nil, nil
	event.ProtocolEvidence, event.Diagnosis, event.RepairSuggestions = nil, nil, nil
	event.SourceStepID, event.SourceAction, event.SourceActionName, event.SourceTransaction, event.SourceProfile, event.ParentActionName = "", "", "", "", "", ""
	event.ResponseBodyState = "not_retained"
	event.CorrelationEvidence = nil
	event.StepPath = append([]string(nil), event.StepPath[:min(len(event.StepPath), maxReplayPathDepth)]...)
	event.IterationPath = append([]int(nil), event.IterationPath[:min(len(event.IterationPath), maxReplayPathDepth)]...)
	boundReplayEventScalarFields(event)
}

func (vu *VUser) replayFinalVariables() map[string]string {
	if vu.runtimeCredentialRedactionErr != nil {
		return nil
	}
	return scrubReplayFinalVariables(vu.Variables, vu.RedactionConfig)
}

func (vu *VUser) replayPublicationError(err error) string {
	if err == nil {
		return ""
	}
	if vu.resultPublicationUnavailable() {
		return "replay error detail unavailable"
	}
	return scrubSensitiveTextWithConfig(err.Error(), vu.resultPublicationRedactionConfig())
}

// OmitUnavailableReplayReport is an idempotent, bounded terminal projection.
// An incomplete classification cannot safely repair prior truncated details;
// omit them while retaining event IDs, ordinals, times and numeric outcomes.
func OmitUnavailableReplayReport(report *ReplayReport) {
	if report == nil {
		return
	}
	report.RuntimeEvidenceUnavailable, report.Success = true, false
	report.Error = "runtime credential redaction failed"
	report.Group = "replay"
	report.RuntimeProfile = ReplayRuntimeProfile{}
	report.FinalVariables, report.ArtifactManifest, report.DiagnosisSummary, report.RepairSuggestions, report.Snapshots = nil, nil, nil, nil, nil
	report.manifestSnapshots, report.retainedSnapshotClones, report.messageDetailBudget = nil, nil, nil
	if len(report.Events) > MaxRetainedReplayEvents {
		report.DroppedEventCount += len(report.Events) - MaxRetainedReplayEvents
		report.Events = append([]ReplayActionEvent(nil), report.Events[:MaxRetainedReplayEvents]...)
	}
	report.eventJSONBytes = make([]int, len(report.Events))
	report.retainedEventJSONBytes, report.consumedEvidenceBytes = 0, 0
	for i := range report.Events {
		omitUnredactableReplayActionEvent(&report.Events[i])
		report.Events[i].CorrelationEvidence = nil
		size := ReplayActionEventRetentionBytes(report.Events[i])
		report.eventJSONBytes[i] = size
		report.retainedEventJSONBytes += size
	}
	for report.retainedEventJSONBytes > MaxRetainedReplayEventBytes {
		report.dropOldestRetainedEvent()
	}
}

func (vu *VUser) guardedReplayProgress(report *ReplayReport, callback func(ReplayProgressEvent)) func(ReplayProgressEvent) {
	return func(event ReplayProgressEvent) {
		if vu.runtimeCredentialRedactionErr != nil {
			if !report.RuntimeEvidenceUnavailable {
				OmitUnavailableReplayReport(report)
			}
			report.FinalVariables, report.ArtifactManifest, report.DiagnosisSummary, report.RepairSuggestions, report.Snapshots = nil, nil, nil, nil, nil
			event.RuntimeEvidenceUnavailable = true
			if event.Report != nil {
				OmitUnavailableReplayReport(event.Report)
			}
			omitUnredactableReplayDebugAction(event.Action)
			omitUnredactableReplayActionEvent(event.Event)
			if event.Condition != nil {
				event.Condition.Expr, event.Condition.Left, event.Condition.Right, event.Condition.Op = "", "", "", ""
			}
		}
		if callback != nil {
			callback(event)
		}
	}
}
