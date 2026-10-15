// Package java_vuser runs governed LoadRunner Java Vuser sidecar actions.
package java_vuser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	defaultRunnerCommand   = config.DefaultJavaVuserRunnerCommand
	defaultMaxProcesses    = config.DefaultJavaVuserMaxProcesses
	defaultActionTimeout   = 30 * time.Second
	defaultHealthTimeout   = 5 * time.Second
	defaultHarnessMain     = "Harness"
	javaVuserProtocolName  = "java_vuser"
	javaVuserIPCContract   = "apexload.java_vuser.ipc.v2"
	javaVuserEventV2       = "apexload.java_vuser.lrapi_events.v2"
	javaVuserValidationV1  = "java_vuser_validation_only_v1"
	javaVuserClassloaderV1 = "java_vuser_dedicated_classloader_v1"
	// FeatureLocalProcessContainmentV1 distinguishes an in-generator runtime
	// with proven strong process-tree ownership from an external sidecar, whose
	// safety depends on a renewable runner-owner lease.
	FeatureLocalProcessContainmentV1 = "java_vuser_local_process_containment_v1"
	modernXBootWarning               = javasidecar.WarningModernXBootClasspathFallback
	sessionFramePrefix               = "APEXLOAD_JAVA_VUSER_V2 "
)

var runnerWaitDelay = 2 * time.Second

var (
	requestIDPrefix = newRequestIDPrefix()
	requestIDSeq    uint64
)

var (
	_ protocol.StreamingProtocol         = (*JavaVuser)(nil)
	_ protocol.RuntimeCapabilityProvider = (*JavaVuser)(nil)
	_ protocol.RuntimeHealthChecker      = (*JavaVuser)(nil)
	_ protocol.RuntimePreflightProvider  = (*JavaVuser)(nil)
)

// The sidecar contract exposes value-free codes only. Keep the receiver
// allowlisted as well so a misconfigured or version-skewed peer cannot smuggle
// resolved script values into errors, warnings, or generator inventory.
var knownSidecarCodes = map[string]struct{}{
	protocol.FeatureNativeDataServicesV1: {}, protocol.FeatureNativeDataLocalV1: {}, protocol.FeatureNativeDataSidecarV1: {}, protocol.FeatureNativeDataPooledV1: {}, protocol.FeatureNativeDataBrokerV1: {},
	"action_type_invalid": {}, "ambient_credentials_detected": {},
	"classpath_entry_invalid": {}, "classpath_entry_unavailable": {}, "classpath_too_many_entries": {},
	javarunner.ClasspathAssetIntegrity: {}, javarunner.ClasspathAssetUnavailable: {}, javarunner.ClasspathAssetWorkDirRequired: {},
	"contract_version_mismatch": {}, "dedicated_classpath_required": {},
	"event_capture_unavailable": {}, "execution_canceled": {}, "execution_id_conflict": {},
	"execution_id_invalid": {}, "execution_timeout": {}, "harness_configuration_invalid": {},
	"harness_health_failed": {}, "harness_payload_invalid": {}, "harness_reported_failure": {},
	"harness_unavailable": {}, "invalid_error_response": {}, "invalid_request": {},
	"java_vuser_dedicated_classloader_v1": {}, "java_vuser_exact_vm_parameters": {},
	"java_vuser_jdk_selection_v1": {}, "java_vuser_validation_only_v1": {},
	"jdk_home_invalid": {}, "jdk_home_required": {}, "jdk_selection_invalid": {}, "jdk_unavailable": {},
	"legacy_runner_wrapper_unsupported": {}, "method_not_allowed": {}, "not_found": {},
	"process_containment_unavailable": {}, "process_failed": {}, "request_too_large": {},
	"result_summary_missing": {}, "server_draining": {},
	"runner_auth_unavailable": {}, "runner_disabled": {}, "runner_unauthorized": {},
	"session_configuration_mismatch": {}, "session_id_invalid": {}, "session_unusable": {},
	"session_not_started": {},
	"owner_claim_failed":  {}, "owner_claim_in_progress": {}, "owner_fenced": {},
	"owner_id_invalid": {}, "owner_identity_unavailable": {}, "owner_lease_ttl_invalid": {}, "owner_not_claimed": {},
	"runtime_cleanup_failed": {}, "runtime_lease_expired": {}, "runtime_lease_fenced": {},
	"runtime_lease_id_invalid": {}, "runtime_lease_identity_unavailable": {},
	"runtime_lease_not_found": {}, "runtime_lease_revoked": {}, "runtime_lease_ttl_invalid": {},
	"shared_classpath_unavailable": {}, "startup_timeout_out_of_range": {},
	"target_class_invalid": {}, "target_method_invalid": {}, "target_validation_failed": {}, "validation_timeout": {},
	"timeout_out_of_range": {}, "work_dir_unavailable": {},
	javasidecar.WarningModernXBootClasspathFallback:            {},
	protocol.FeatureJavaVuserPhaseLifecycleV1:                  {},
	protocol.FeatureJavaVuserPersistentLifecycleV1:             {},
	protocol.FeatureJavaVuserSessionFrameTokenV1:               {},
	javasidecar.FeatureOwnerFencingV1:                          {},
	javasidecar.FeatureExternalRuntimeV1:                       {},
	javasidecar.FeatureRunnerOwnerLeaseV1:                      {},
	javasidecar.FeatureRuntimeLeaseV1:                          {},
	javasidecar.FeatureSharedJVMV1:                             {},
	"vm_parameter_" + javarunner.VMParameterTooMany:            {},
	"vm_parameter_" + javarunner.VMParameterTotalTooLarge:      {},
	"vm_parameter_" + javarunner.VMParameterEmpty:              {},
	"vm_parameter_" + javarunner.VMParameterTooLong:            {},
	"vm_parameter_" + javarunner.VMParameterControlCharacter:   {},
	"vm_parameter_" + javarunner.VMParameterMustStartWithDash:  {},
	"vm_parameter_" + javarunner.VMParameterArgumentFile:       {},
	"vm_parameter_" + javarunner.VMParameterEntrypointOverride: {},
	"vm_parameter_" + javarunner.VMParameterClasspathOverride:  {},
	"vm_parameter_" + javarunner.VMParameterModuleOverride:     {},
	"vm_parameter_" + javarunner.VMParameterAgentHook:          {},
	"vm_parameter_" + javarunner.VMParameterBootclasspath:      {},
	"vm_parameter_" + javarunner.VMParameterCustomSystemLoader: {},
	"vm_parameter_" + javarunner.VMParameterTerminalOption:     {},
	"vm_parameter_" + javarunner.VMParameterErrorCommandHook:   {},
	"vm_parameter_" + javarunner.VMParameterReservedProperty:   {},
	"vm_parameter_" + javarunner.VMParameterTemplateForbidden:  {},
	"vm_parameter_" + javarunner.VMParameterSensitiveProperty:  {},
}

// processSlots bounds the number of concurrent runner processes per generator.
// It is sized once from the operator-owned APEXLOAD_JAVA_VUSER_MAX_PROCESSES so
// that a single generator-wide cap applies across every java_vuser group,
// regardless of any (now-ignored) script-supplied max_processes value.
var (
	processSlots     chan struct{}
	processSlotsOnce sync.Once
	// localContainmentFailed is terminal for this generator process. Each
	// failed whole-tree cleanup leaves its capacity token occupied, and this
	// gate prevents other released tokens from being recycled into new JVMs.
	localContainmentFailed atomic.Bool
	localContainmentSignal struct {
		sync.Mutex
		done   chan struct{}
		closed bool
	}
	healthCache struct {
		sync.Mutex
		key       string
		checkedAt time.Time
		healthy   bool
	}
	sidecarCapabilitiesCache struct {
		sync.Mutex
		entries map[string]*sidecarCapabilitiesCacheEntry
	}
	localHarnessCapabilitiesCache struct {
		sync.Mutex
		entries map[string]*localHarnessCapabilitiesCacheEntry
	}
	resolvedJDKCache struct {
		sync.Mutex
		entries map[string]cachedResolvedJDK
	}
	localHarnessProbeSlot = make(chan struct{}, 1)
)

type cachedResolvedJDK struct {
	resolved  javarunner.ResolvedJDK
	expiresAt time.Time
}

type sidecarCapabilitiesCacheEntry struct {
	response  javasidecar.CapabilitiesResponse
	err       error
	expiresAt time.Time
	ready     chan struct{}
}

type localHarnessCapabilitiesCacheEntry struct {
	response  runnerHealthEnvelope
	err       error
	expiresAt time.Time
	ready     chan struct{}
}

const (
	sidecarCapabilitiesSuccessTTL = 30 * time.Second
	sidecarCapabilitiesFailureTTL = 2 * time.Second
	localCapabilitiesSuccessTTL   = 30 * time.Second
	localCapabilitiesFailureTTL   = 2 * time.Second
	maxLocalCapabilitiesEntries   = 64
	maxResolvedJDKCacheEntries    = 64
)

func processSlotChan() chan struct{} {
	processSlotsOnce.Do(func() {
		limit := config.LoadJavaVuserOperatorConfig().MaxProcesses
		if limit <= 0 {
			limit = defaultMaxProcesses
		}
		processSlots = make(chan struct{}, limit)
	})
	return processSlots
}

func localContainmentFailureDone() <-chan struct{} {
	localContainmentSignal.Lock()
	defer localContainmentSignal.Unlock()
	if localContainmentSignal.done == nil {
		localContainmentSignal.done = make(chan struct{})
	}
	return localContainmentSignal.done
}

// LocalProcessContainmentFailure returns the process-wide, close-only signal
// for a terminal loss of local Java process-tree containment. The generator
// process owner watches this signal so a controller that is still renewing a
// run lease cannot keep an uncontained JVM (or one of its descendants) alive.
// External sidecars enforce their own owner lease and process-exit contract.
func LocalProcessContainmentFailure() <-chan struct{} {
	// Synchronize a failure first observed by the shared javarunner package into
	// this protocol-level signal before returning it to the process owner.
	_ = localProcessContainmentFailed()
	return localContainmentFailureDone()
}

func markLocalContainmentFailed() {
	localContainmentFailed.Store(true)
	localContainmentSignal.Lock()
	if localContainmentSignal.done == nil {
		localContainmentSignal.done = make(chan struct{})
	}
	if !localContainmentSignal.closed {
		close(localContainmentSignal.done)
		localContainmentSignal.closed = true
	}
	localContainmentSignal.Unlock()
}

func localProcessContainmentFailed() bool {
	if javarunner.ProcessContainmentFailed() {
		markLocalContainmentFailed()
	}
	return localContainmentFailed.Load()
}

func init() {
	protocol.Register(javaVuserProtocolName, func() protocol.Protocol {
		return &JavaVuser{}
	})
}

type JavaVuser struct {
	nativeServiceBinding
	cfg configuration

	// sidecar is deliberately expressed through the narrow typed contract so
	// protocol tests can prove that sidecar mode never falls back to a local
	// fork. Production instances receive *javasidecar.Client during Init.
	sidecar javaVuserSidecarClient

	sessionMu           sync.Mutex
	session             *runnerSession
	sessionErr          error
	sidecarSessionID    string
	sidecarSessionErr   error
	sidecarSessionLive  bool
	sidecarLeaseMu      sync.Mutex
	sidecarLeaseCancel  context.CancelFunc
	sidecarLeaseDone    chan struct{}
	sidecarLeaseClaimed bool

	failedStartMu    sync.Mutex
	failedStartReaps []<-chan struct{}

	// Per-instance hooks let package tests exercise persistent lifecycle
	// semantics on hosts where production containment deliberately fails
	// closed. Registered production instances leave both nil.
	supportsStrongProcessContainment func() bool
	startStrongProcessTree           func(*exec.Cmd) error
}

type configuration struct {
	OperatorCDSManifest    string
	Enabled                bool
	RunnerMode             string
	RunnerURL              string
	RunnerCommand          string
	HarnessJar             string
	ScriptClasspath        []string
	ClasspathAssets        []dsl.JavaClasspathAsset
	ClasspathUploadEnabled bool
	Classpath              []string
	WorkDir                string
	MaxProcesses           int
	StartupTimeout         time.Duration
	ActionTimeout          time.Duration
	Environment            map[string]string
	AdditionalVMParameters []string
	UseXBootClasspath      bool
	DedicatedClassLoader   bool
	JDKSelection           string
	JDKHome                string
	DefaultJDKHome         string
	AuthToken              string
	// AllowLegacyJava is the operator's legacy-JVM opt-in. It lowers the
	// accepted Java major version and must reach both the local resolver and the
	// sidecar capability gate, or the opt-in fails in only one of them.
	AllowLegacyJava       bool
	RunnerCommandExplicit bool
	DirectJavaRunner      bool
	RequireBoundSummary   bool
	ResolvedJDK           *javarunner.ResolvedJDK
}

type javaVuserSidecarClient interface {
	Health(context.Context) (javasidecar.HealthResponse, error)
	Capabilities(context.Context) (javasidecar.CapabilitiesResponse, error)
	Validate(context.Context, javasidecar.ValidateRequest) (javasidecar.ValidateResponse, error)
	ClaimOwner(context.Context, string) (javasidecar.ClaimOwnerResponse, error)
	ClaimRuntimeLease(context.Context) (javasidecar.ClaimRuntimeLeaseResponse, error)
	RenewRuntimeLease(context.Context) (javasidecar.RenewRuntimeLeaseResponse, error)
	RevokeRuntimeLease(context.Context) (javasidecar.RevokeRuntimeLeaseResponse, error)
	Execute(context.Context, javasidecar.ExecutionRequest) (javasidecar.ExecutionResponse, error)
	CloseSession(context.Context, string) (javasidecar.CloseSessionResponse, error)
}

type runCommand struct {
	NativeServices   bool           `json:"native_services,omitempty"`
	ProtocolVersion  string         `json:"protocol_version,omitempty"`
	EventContract    string         `json:"event_contract,omitempty"`
	RequestID        string         `json:"request_id,omitempty"`
	SessionSecret    string         `json:"session_secret,omitempty"`
	ActionName       string         `json:"action_name"`
	ActionType       string         `json:"action_type"`
	ClassName        string         `json:"class_name"`
	Method           string         `json:"method"`
	SourceArchiveRef string         `json:"source_archive_ref,omitempty"`
	StartupTimeoutMS int64          `json:"startup_timeout_ms,omitempty"`
	UserClasspath    []string       `json:"user_classpath,omitempty"`
	DedicatedLoader  bool           `json:"dedicated_class_loader"`
	ValidationOnly   bool           `json:"validation_only"`
	Params           map[string]any `json:"params,omitempty"`
}

type runnerSummary struct {
	Contract          string `json:"contract,omitempty"`
	Kind              string `json:"kind,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	Success           *bool  `json:"success,omitempty"`
	Error             string `json:"error,omitempty"`
	ClassName         string `json:"class_name,omitempty"`
	Method            string `json:"method,omitempty"`
	DurationMS        int64  `json:"duration_ms,omitempty"`
	EventFallbackUsed bool   `json:"event_fallback_used,omitempty"`
	EventsTruncated   bool   `json:"events_truncated,omitempty"`
}

type runnerEvent struct {
	Contract            string `json:"contract,omitempty"`
	RequestID           string `json:"request_id,omitempty"`
	TimestampMS         int64  `json:"timestamp_ms,omitempty"`
	Sequence            int64  `json:"sequence,omitempty"`
	TransactionID       string `json:"transaction_id,omitempty"`
	ParentTransactionID string `json:"parent_transaction_id,omitempty"`
	DurationMS          *int64 `json:"duration_ms,omitempty"`
	RawDurationMS       *int64 `json:"raw_duration_ms,omitempty"`
	ThinkTimeMS         *int64 `json:"think_time_ms,omitempty"`
	WastedTimeMS        *int64 `json:"wasted_time_ms,omitempty"`
	Type                string `json:"type"`
	Name                string `json:"name,omitempty"`
	Message             string `json:"message,omitempty"`
}

func (j *JavaVuser) Name() string {
	return javaVuserProtocolName
}

func (j *JavaVuser) Init(ctx context.Context, cfg map[string]any) error {
	return j.init(ctx, cfg, true)
}

func (j *JavaVuser) init(ctx context.Context, cfg map[string]any, claimSidecarOwner bool) error {
	if len(protocol.TargetPolicyHosts(cfg)) > 0 {
		return fmt.Errorf("java_vuser runner cannot enforce the CI runtime target policy")
	}
	// Enablement, runner command, harness, working directory, shared classpath,
	// and process capacity are operator-owned. Reviewed scripts may select a JDK
	// home, exact VM tokens, and precompiled user-classpath references, but can
	// never enable execution or replace the governed backend.
	operator := config.LoadJavaVuserOperatorConfig()
	scriptClasspath, err := exactStringSliceValue(cfg, "java_vuser_classpath")
	if err != nil {
		return err
	}
	vmParameters, err := exactStringSliceValue(cfg, "java_vuser_additional_vm_parameters")
	if err != nil {
		return err
	}
	if issues := javarunner.ValidateVMParametersForExecution(vmParameters); len(issues) > 0 {
		return fmt.Errorf("java_vuser %w", issues[0])
	}
	selection := strings.TrimSpace(stringValue(cfg, "java_vuser_jdk_selection"))
	if selection == "" {
		selection = string(javarunner.JDKSelectionInternal)
	}
	if selection != string(javarunner.JDKSelectionInternal) && selection != string(javarunner.JDKSelectionSpecified) {
		return fmt.Errorf("java_vuser jdk_selection must be internal or specified")
	}
	jdkHome := stringValue(cfg, "java_vuser_jdk_home")
	if len(jdkHome) > 4096 || strings.IndexFunc(jdkHome, unicode.IsControl) >= 0 {
		return fmt.Errorf("java_vuser jdk_home is invalid")
	}
	if selection == string(javarunner.JDKSelectionSpecified) && jdkHome == "" {
		return fmt.Errorf("java_vuser jdk_home is required when jdk_selection is specified")
	}
	startupTimeout, err := protocol.BoundedMilliseconds(cfg["java_vuser_startup_timeout_ms"], 0, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return fmt.Errorf("java_vuser startup_timeout_ms %w", err)
	}
	actionTimeout, err := protocol.BoundedMilliseconds(cfg["java_vuser_action_timeout_ms"], defaultActionTimeout, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return fmt.Errorf("java_vuser action_timeout_ms %w", err)
	}
	runnerMode := strings.ToLower(strings.TrimSpace(operator.RunnerMode))
	if runnerMode == "" {
		runnerMode = config.DefaultJavaVuserRunnerMode
	}
	if runnerMode != config.JavaVuserRunnerModeLocal && runnerMode != config.JavaVuserRunnerModeSidecar {
		return fmt.Errorf("java_vuser runner mode must be local or sidecar")
	}
	effectiveClasspath := append(append([]string(nil), scriptClasspath...), operator.Classpath...)
	if issues := javarunner.ValidateClasspath(effectiveClasspath); len(issues) > 0 {
		return fmt.Errorf("java_vuser %w", issues[0])
	}
	classpathAssets, err := exactClasspathAssetsValue(cfg, "java_vuser_classpath_assets")
	if err != nil {
		return err
	}
	if operator.ClasspathUploadEnabled && strings.TrimSpace(operator.ClasspathAssetDir) != "" {
		// Register the controller's import asset store so a local (same-host)
		// runner can read uploaded JAR bytes by their file:// storage_ref.
		// Idempotent; safe to call per Init.
		_ = fileasset.RegisterProjectRoot(operator.ClasspathAssetDir)
	}
	j.cfg = configuration{
		OperatorCDSManifest:    operator.CDSManifest,
		Enabled:                operator.Enabled,
		RunnerMode:             runnerMode,
		RunnerURL:              operator.RunnerURL,
		RunnerCommand:          protocol.FirstNonEmpty(operator.RunnerCommand, defaultRunnerCommand),
		HarnessJar:             operator.HarnessJar,
		ScriptClasspath:        append([]string(nil), scriptClasspath...),
		ClasspathAssets:        classpathAssets,
		ClasspathUploadEnabled: operator.ClasspathUploadEnabled,
		Classpath:              effectiveClasspath,
		WorkDir:                operator.WorkDir,
		MaxProcesses:           operator.MaxProcesses,
		StartupTimeout:         startupTimeout,
		ActionTimeout:          actionTimeout,
		AdditionalVMParameters: vmParameters,
		UseXBootClasspath:      boolValue(cfg, "java_vuser_use_xbootclasspath"),
		DedicatedClassLoader:   boolValue(cfg, "java_vuser_dedicated_class_loader"),
		JDKSelection:           selection,
		JDKHome:                jdkHome,
		DefaultJDKHome:         operator.DefaultJDKHome,
		AuthToken:              operator.AuthToken,
		AllowLegacyJava:        operator.AllowLegacyJava,
		RunnerCommandExplicit:  operator.RunnerCommandExplicit,
		Environment: map[string]string{
			"APEXLOAD_VUSER_ID":            executionContextValue(cfg, "vuser_id"),
			"APEXLOAD_GROUP_NAME":          executionContextValue(cfg, "group_name"),
			"APEXLOAD_SCENARIO_ID":         executionContextValue(cfg, "scenario_id"),
			"APEXLOAD_IGNORE_THINK_TIME":   executionContextValue(cfg, "ignore_think_time"),
			"APEXLOAD_THINK_TIME_LIMIT_MS": executionContextValue(cfg, "think_time_limit_ms"),
		},
	}
	if runnerMode == config.JavaVuserRunnerModeSidecar {
		client, clientErr := javasidecar.NewClientWithToken(operator.RunnerURL, operator.AuthToken, nil)
		if clientErr != nil {
			return fmt.Errorf("java_vuser sidecar URL is invalid: %w", clientErr)
		}
		if j.sidecar == nil {
			j.sidecar = client
		}
		if operator.Enabled && claimSidecarOwner {
			if _, claimErr := j.sidecar.ClaimOwner(ctx, javaVuserProcessOwnerID()); claimErr != nil {
				return fmt.Errorf("java_vuser sidecar owner claim failed (%s)", sidecarCallErrorCode(claimErr))
			}
			if _, claimErr := j.sidecar.ClaimRuntimeLease(ctx); claimErr != nil {
				return fmt.Errorf("java_vuser sidecar runtime lease claim failed (%s)", sidecarCallErrorCode(claimErr))
			}
			keeperCtx, keeperCancel := context.WithCancel(ctx)
			keeperDone := make(chan struct{})
			j.sidecarLeaseMu.Lock()
			j.sidecarLeaseCancel = keeperCancel
			j.sidecarLeaseDone = keeperDone
			j.sidecarLeaseClaimed = true
			j.sidecarLeaseMu.Unlock()
			go j.renewSidecarLeases(keeperCtx, keeperDone)
		}
	} else if operator.Enabled {
		j.cfg.DirectJavaRunner = javarunner.IsDirectJavaCommand(j.cfg.RunnerCommand)
		if !j.cfg.DirectJavaRunner {
			if hasJavaVuserRuntimeOverrides(cfg) {
				return fmt.Errorf("java_vuser script JVM settings require a direct java/java.exe runner; the operator configured a legacy wrapper")
			}
		}
		prepared, prepareErr := j.prepareLocalConfiguration(ctx, j.cfg)
		if prepareErr != nil {
			return prepareErr
		}
		j.cfg = prepared
	}
	if j.cfg.MaxProcesses <= 0 {
		j.cfg.MaxProcesses = defaultMaxProcesses
	}
	if j.cfg.ActionTimeout <= 0 {
		j.cfg.ActionTimeout = defaultActionTimeout
	}
	return nil
}

func (j *JavaVuser) renewSidecarLeases(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(javasidecar.RunnerOwnerLeaseRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(ctx, javasidecar.RunnerOwnerLeaseRenewInterval)
			_, _ = j.sidecar.ClaimOwner(renewCtx, javaVuserProcessOwnerID())
			cancel()
			renewCtx, cancel = context.WithTimeout(ctx, javasidecar.RuntimeLeaseRenewInterval)
			_, _ = j.sidecar.RenewRuntimeLease(renewCtx)
			cancel()
		}
	}
}

func (j *JavaVuser) stopSidecarLeaseKeeper() bool {
	j.sidecarLeaseMu.Lock()
	cancel, done, claimed := j.sidecarLeaseCancel, j.sidecarLeaseDone, j.sidecarLeaseClaimed
	j.sidecarLeaseCancel, j.sidecarLeaseDone = nil, nil
	j.sidecarLeaseMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return claimed
}

// revokeSidecarRuntimeLease is intentionally not bounded by the engine's
// teardown context after the lease keeper stops. An ambiguous cleanup response
// cannot be converted into quiescence: this barrier remains open until a retry
// positively observes reap, or the generator's outer run fence exits the
// process. Each individual transport attempt remains bounded.
func (j *JavaVuser) revokeSidecarRuntimeLease() error {
	for {
		attemptCtx, cancel := context.WithTimeout(context.Background(), javasidecar.RuntimeLeaseRenewInterval)
		_, err := j.sidecar.RevokeRuntimeLease(attemptCtx)
		cancel()
		if err == nil {
			j.sidecarLeaseMu.Lock()
			j.sidecarLeaseClaimed = false
			j.sidecarLeaseMu.Unlock()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (j *JavaVuser) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	return j.ExecuteStream(ctx, action, nil)
}

func (j *JavaVuser) ExecuteStream(ctx context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if action == nil {
		return &protocol.Result{StartTime: time.Now(), StatusCode: 500, ErrorMessage: "java_vuser action is required"}, nil
	}
	method := actionMethod(action)
	if j.hasNative() && !isPersistentLifecycleMethod(method) {
		return nil, errors.New("native_services_require_persistent_java")
	}
	if strings.EqualFold(method, "full_lifecycle") {
		if j.cfg.RunnerMode == config.JavaVuserRunnerModeSidecar {
			return j.executeSidecar(ctx, action, emit)
		}
		result, err := j.executeOneShot(ctx, action, emit)
		if result != nil && j.cfg.UseXBootClasspath {
			if result.ProtocolEvidence == nil {
				result.ProtocolEvidence = map[string]any{}
			}
			result.ProtocolEvidence["java_vuser_xbootclasspath"] = modernXBootWarning
		}
		return result, err
	}
	if j.cfg.RunnerMode == config.JavaVuserRunnerModeSidecar {
		return j.executeSidecar(ctx, action, emit)
	}
	result, err := j.executePersistent(ctx, action, emit)
	if result != nil && j.cfg.UseXBootClasspath {
		if result.ProtocolEvidence == nil {
			result.ProtocolEvidence = map[string]any{}
		}
		result.ProtocolEvidence["java_vuser_xbootclasspath"] = modernXBootWarning
	}
	return result, err
}

func (j *JavaVuser) executeSidecar(ctx context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	start := time.Now()
	if action == nil {
		return &protocol.Result{StartTime: start, StatusCode: 500, ErrorMessage: "java_vuser action is required"}, nil
	}
	result := &protocol.Result{
		ActionName:       action.Name,
		StartTime:        start,
		StatusCode:       200,
		Success:          true,
		Extractions:      map[string]string{},
		DependencyType:   javaVuserProtocolName,
		DependencyTarget: strings.TrimSpace(action.Target),
	}
	if strings.TrimSpace(action.Type) != "java_vuser_run" {
		finalizeResult(result, start, action, fmt.Errorf("unsupported Java Vuser action type %q", action.Type), "", "", "")
		return result, nil
	}
	if !j.cfg.Enabled {
		finalizeResult(result, start, action, fmt.Errorf("java_vuser runner is disabled; an operator must enable it on this generator via %s=true (script config cannot enable it)", config.EnvJavaVuserRunnerEnabled), "", "", "")
		return result, nil
	}
	method := actionMethod(action)
	if !supportedLifecycleMethod(method) {
		finalizeResult(result, start, action, fmt.Errorf("java_vuser sidecar does not support lifecycle method"), "", "", "")
		return result, nil
	}
	persistent := !strings.EqualFold(method, "full_lifecycle")
	if persistent {
		j.sessionMu.Lock()
		defer j.sessionMu.Unlock()
		if j.sidecarSessionErr != nil {
			finalizeResult(result, start, action, fmt.Errorf("java_vuser persistent sidecar session is unusable: %w", j.sidecarSessionErr), "", "", "")
			return result, nil
		}
		if j.sidecarSessionID == "" {
			j.sidecarSessionID = "session-" + j.nextRequestID()
		}
	}
	timeout, err := actionTimeout(action, j.cfg.ActionTimeout)
	if err != nil {
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}
	client := j.sidecar
	if client == nil {
		finalizeResult(result, start, action, fmt.Errorf("java_vuser sidecar client is unavailable"), "", "", "")
		return result, nil
	}

	executionID := j.nextRequestID()
	result.CorrelationID = executionID
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	runCtx, closeNative, nativeErr := j.nativeContext(runCtx, executionID)
	if nativeErr != nil {
		return nil, nativeErr
	}
	defer closeNative()
	response, callErr := client.Execute(runCtx, javasidecar.ExecutionRequest{
		ExecutionID: executionID,
		SessionID: func() string {
			if persistent {
				return j.sidecarSessionID
			}
			return ""
		}(),
		RequireExistingSession: persistent && j.sidecarSessionLive,
		Runtime:                j.sidecarRuntimeSettings(),
		Command: javasidecar.ActionCommand{
			ActionName:       action.Name,
			ActionType:       action.Type,
			ClassName:        strings.TrimSpace(action.Target),
			Method:           method,
			SourceArchiveRef: stringParam(action.Params, "source_archive_ref"),
			Params:           action.Params,
			VUserID:          j.cfg.Environment["APEXLOAD_VUSER_ID"],
			GroupName:        j.cfg.Environment["APEXLOAD_GROUP_NAME"],
			ScenarioID:       j.cfg.Environment["APEXLOAD_SCENARIO_ID"],
			IgnoreThinkTime:  j.cfg.Environment["APEXLOAD_IGNORE_THINK_TIME"],
			ThinkTimeLimitMS: j.cfg.Environment["APEXLOAD_THINK_TIME_LIMIT_MS"],
		},
		TimeoutMS: timeout.Milliseconds(),
	})

	var executionErr error
	if callErr != nil {
		executionErr = fmt.Errorf("java_vuser sidecar execution failed (%s)", sidecarCallErrorCode(callErr))
	} else if response.ExecutionID != executionID {
		executionErr = fmt.Errorf("java_vuser sidecar execution failed (execution_id_mismatch)")
	} else if response.ErrorCode != "" || !response.Success {
		code := stableSidecarCode(response.ErrorCode, "execution_failed")
		executionErr = fmt.Errorf("java_vuser sidecar execution failed (%s)", code)
	}
	if persistent {
		code := stableSidecarCode(response.ErrorCode, "")
		if callErr != nil {
			var remote *javasidecar.RemoteError
			if errors.As(callErr, &remote) {
				code = stableSidecarCode(remote.Code, "")
			}
		}
		if executionErr == nil || (callErr == nil && response.ExecutionID == executionID && code == "harness_reported_failure") {
			j.sidecarSessionLive = true
		} else {
			// A transport/decode failure is ambiguous even on the first call:
			// the sidecar may have run the method before the response was lost.
			// Retrying that session ID could silently replace the Actions state.
			fatal := true
			if callErr != nil {
				fatal = javasidecar.ExecutionOutcomeAmbiguous(callErr)
				var remote *javasidecar.RemoteError
				if errors.As(callErr, &remote) {
					// A remote code not understood by this generator is
					// fail-closed: a future sidecar may have returned it after
					// mutating the Actions object.
					fatal = !sidecarFailureSafeBeforeInvocation(code)
				}
			} else if response.ExecutionID == executionID {
				fatal = !sidecarFailureSafeBeforeInvocation(code)
			}
			switch code {
			case "session_unusable", "contract_version_mismatch", "session_configuration_mismatch":
				fatal = true
			case "execution_timeout", "execution_canceled":
				// The invocation may have started and mutated state before the
				// process was killed. It is never safe to recreate the session.
				fatal = true
			case "session_not_started":
				// Capacity wait expired before a JVM or Actions object existed.
				fatal = false
			}
			if fatal {
				j.sidecarSessionErr = executionErr
			}
		}
	}
	var capturedEvents []runnerEvent
	capturedEventBytes := len(response.Events)
	capturedEventsTruncated := response.EventsTruncated
	fileEvents := filterV2RunnerEvents(parseRunnerEvents(response.Events), executionID)
	fallbackEvents := filterV2RunnerEvents(parseRunnerEvents(response.Stderr), executionID)
	capturedEvents = mergeRunnerEvents(fileEvents, fallbackEvents)
	if len(fallbackEvents) > 0 {
		capturedEventBytes += len(response.Stderr)
		capturedEventsTruncated = capturedEventsTruncated || response.StderrTruncated
	}
	if persistent {
		resultErr := executionErr
		if callErr == nil && response.ExecutionID == executionID &&
			!response.Success && stableSidecarCode(response.ErrorCode, "") == "harness_reported_failure" {
			// The authenticated persistent frame carries the bounded lifecycle
			// diagnostic. Let finalizePersistentResult use it while retaining
			// the value-free error code for session-state decisions above.
			resultErr = nil
		}
		finalizePersistentResult(result, start, action, runnerEnvelope{
			Contract:        javaVuserIPCContract,
			Kind:            "result",
			RequestID:       executionID,
			Success:         response.Success,
			Error:           boundedSidecarHarnessError(response.Error),
			DurationMS:      response.DurationMS,
			Stdout:          response.Stdout,
			Stderr:          response.Stderr,
			StdoutTruncated: response.StdoutTruncated,
			StderrTruncated: response.StderrTruncated,
		}, capturedEvents, capturedEventBytes, capturedEventsTruncated, resultErr)
	} else {
		summary, hasSummary := parseBoundRunnerSummary(response.Stdout, executionID)
		if !hasSummary && callErr == nil && response.ExecutionID == executionID &&
			(response.Success || response.ErrorCode == "harness_reported_failure") {
			success := response.Success
			summary = runnerSummary{
				Contract: javaVuserIPCContract, Kind: "result", RequestID: executionID,
				Success: &success, ClassName: strings.TrimSpace(action.Target),
				Method: method, DurationMS: response.DurationMS,
				Error: boundedSidecarHarnessError(response.Error),
			}
			hasSummary = true
		}
		finalizeResultWithAuthoritativeSummaryAndEvents(
			result, start, action, executionErr, response.Stdout, response.Stderr,
			summary, hasSummary, capturedEvents,
		)
	}
	if response.DurationMS > 0 {
		result.Extractions["java_vuser_duration_ms"] = strconv.FormatInt(response.DurationMS, 10)
	}
	if response.StdoutTruncated {
		result.ProtocolEvidence["runner_stdout_truncated"] = true
	}
	if response.StderrTruncated {
		result.ProtocolEvidence["runner_stderr_truncated"] = true
	}
	if !persistent && capturedEventBytes > 0 {
		result.ProtocolEvidence["runner_event_stream_bytes"] = capturedEventBytes
	}
	if !persistent && capturedEventsTruncated {
		result.ProtocolEvidence["runner_event_stream_truncated"] = true
	}
	result.ProtocolEvidence["runner_backend"] = config.JavaVuserRunnerModeSidecar
	if warnings := normalizeSidecarWarnings(response.WarningCodes); len(warnings) > 0 {
		result.ProtocolEvidence["runner_warning_codes"] = warnings
	}
	if j.cfg.UseXBootClasspath {
		result.ProtocolEvidence["java_vuser_xbootclasspath"] = modernXBootWarning
	}
	if emit != nil {
		if err := emitRunnerTransactionsAt(action, capturedEvents, result.StartTime.Add(result.Duration), capturedEventsTruncated, emit); err != nil {
			return result, err
		}
	}
	return result, nil
}

func sidecarFailureSafeBeforeInvocation(code string) bool {
	switch code {
	case "action_type_invalid", "ambient_credentials_detected",
		"classpath_entry_invalid", "classpath_entry_unavailable", "classpath_too_many_entries",
		javarunner.ClasspathAssetIntegrity, javarunner.ClasspathAssetUnavailable, javarunner.ClasspathAssetWorkDirRequired,
		"dedicated_classpath_required", "event_capture_unavailable",
		"harness_configuration_invalid", "harness_health_failed", "harness_payload_invalid",
		"harness_unavailable", "invalid_request", "jdk_home_invalid", "jdk_home_required",
		"jdk_selection_invalid", "jdk_unavailable", "legacy_runner_wrapper_unsupported",
		"method_not_allowed", "process_containment_unavailable", "process_failed",
		"request_too_large", "session_not_started",
		"shared_classpath_unavailable", "startup_timeout_out_of_range", "target_class_invalid",
		"target_method_invalid", "target_validation_failed", "timeout_out_of_range",
		"work_dir_unavailable":
		return true
	default:
		return strings.HasPrefix(code, "vm_parameter_")
	}
}

func boundedSidecarHarnessError(value string) string {
	if len(value) <= javasidecar.MaxHarnessErrorBytes {
		return value
	}
	const suffix = "...[truncated]"
	return boundedtext.TruncateUTF8(
		value,
		javasidecar.MaxHarnessErrorBytes-len(suffix),
	) + suffix
}

func (j *JavaVuser) sidecarRuntimeSettings() javasidecar.RuntimeSettings {
	return javasidecar.RuntimeSettings{
		JDKSelection:           j.cfg.JDKSelection,
		JDKHome:                j.cfg.JDKHome,
		AdditionalVMParameters: append([]string(nil), j.cfg.AdditionalVMParameters...),
		Classpath:              append([]string(nil), j.cfg.ScriptClasspath...),
		UseXBootClasspath:      j.cfg.UseXBootClasspath,
		DedicatedClassLoader:   j.cfg.DedicatedClassLoader,
		StartupTimeoutMS:       j.cfg.StartupTimeout.Milliseconds(),
		ClasspathAssets:        j.sidecarClasspathAssetRefs(),
	}
}

// sidecarClasspathAssetRefs projects the script-owned classpath asset bindings
// onto the sidecar wire contract. The sidecar decides whether to honor them
// (operator opt-in); sending them unconditionally keeps the protocol layer
// free of operator-flag logic.
func (j *JavaVuser) sidecarClasspathAssetRefs() []javasidecar.ClasspathAssetRef {
	if len(j.cfg.ClasspathAssets) == 0 {
		return nil
	}
	refs := make([]javasidecar.ClasspathAssetRef, 0, len(j.cfg.ClasspathAssets))
	for _, asset := range j.cfg.ClasspathAssets {
		refs = append(refs, javasidecar.ClasspathAssetRef{
			Entry:      asset.Entry,
			SHA256:     asset.SHA256,
			StorageRef: asset.StorageRef,
		})
	}
	return refs
}

// localClasspathAssets projects DSL classpath asset bindings onto the shared
// javarunner materialization contract used by the in-process local runner.
func localClasspathAssets(assets []dsl.JavaClasspathAsset) []javarunner.ClasspathAsset {
	if len(assets) == 0 {
		return nil
	}
	out := make([]javarunner.ClasspathAsset, 0, len(assets))
	for _, asset := range assets {
		out = append(out, javarunner.ClasspathAsset{
			Entry:      asset.Entry,
			SHA256:     asset.SHA256,
			StorageRef: asset.StorageRef,
		})
	}
	return out
}

func (j *JavaVuser) executeOneShot(ctx context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	start := time.Now()
	if action == nil {
		return &protocol.Result{StartTime: start, StatusCode: 500, ErrorMessage: "java_vuser action is required"}, nil
	}
	result := &protocol.Result{
		ActionName:       action.Name,
		StartTime:        start,
		StatusCode:       200,
		Success:          true,
		Extractions:      map[string]string{},
		DependencyType:   "java_vuser",
		DependencyTarget: strings.TrimSpace(action.Target),
	}
	if strings.TrimSpace(action.Type) != "java_vuser_run" {
		err := fmt.Errorf("unsupported Java Vuser action type %q", action.Type)
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}
	method := actionMethod(action)
	if !supportedLifecycleMethod(method) {
		finalizeResult(result, start, action, fmt.Errorf("unsupported Java Vuser lifecycle method"), "", "", "")
		return result, nil
	}
	timeout, err := actionTimeout(action, j.cfg.ActionTimeout)
	if err != nil {
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}
	if !j.cfg.Enabled {
		err := fmt.Errorf("java_vuser runner is disabled; an operator must enable it on this generator via %s=true (script config cannot enable it)", config.EnvJavaVuserRunnerEnabled)
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}
	if err := subprocessguard.ValidateAmbientCredentials(); err != nil {
		finalizeResult(result, start, action, fmt.Errorf("java_vuser runner isolation check failed: %w", err), "", "", "")
		return result, nil
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	release, err := acquireProcessSlot(runCtx)
	if err != nil {
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}
	releasePending := true
	defer func() {
		if releasePending {
			release()
		}
	}()
	queueWait := time.Since(start)
	runStart := time.Now()
	result.StartTime = runStart
	if queueWait > 0 {
		result.Extractions["java_vuser_queue_wait_ms"] = strconv.FormatInt(queueWait.Milliseconds(), 10)
	}

	cmdPayload := runCommand{
		ProtocolVersion:  javaVuserIPCContract,
		EventContract:    javaVuserEventV2,
		RequestID:        j.nextRequestID(),
		ActionName:       action.Name,
		ActionType:       action.Type,
		ClassName:        strings.TrimSpace(action.Target),
		Method:           method,
		SourceArchiveRef: stringParam(action.Params, "source_archive_ref"),
		StartupTimeoutMS: j.cfg.StartupTimeout.Milliseconds(),
		Params:           action.Params,
	}
	j.decorateRunCommand(&cmdPayload)
	result.CorrelationID = cmdPayload.RequestID
	payload, marshalErr := json.Marshal(cmdPayload)
	if marshalErr != nil {
		finalizeResult(result, runStart, action, fmt.Errorf("java_vuser harness payload is invalid"), "", "", "")
		return result, nil
	}
	args, err := j.commandArgs(cmdPayload)
	if err != nil {
		finalizeResult(result, runStart, action, err, "", "", "")
		return result, nil
	}
	cmd := exec.CommandContext(runCtx, j.cfg.RunnerCommand, args...)
	javarunner.ConfigureProcessTree(cmd)
	cmd.WaitDelay = runnerWaitDelay
	if j.cfg.WorkDir != "" {
		cmd.Dir = j.cfg.WorkDir
	}
	eventFile, cleanupEventFile := createRunnerEventFile()
	eventCleanupPending := cleanupEventFile != nil
	defer func() {
		if eventCleanupPending {
			cleanupEventFile()
		}
	}()
	extraEnvironment := cloneStringMap(j.cfg.Environment)
	if eventFile != "" {
		extraEnvironment["APEXLOAD_JAVA_VUSER_EVENT_FILE"] = eventFile
	}
	cmd.Env = subprocessguard.Environment(extraEnvironment)
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	// Cap stdout/stderr capture so a chatty or runaway runner cannot OOM the
	// generator: the action timeout bounds wall-clock, not bytes, and up to
	// MaxProcesses runners capture concurrently.
	var stdout runnerCaptureBuffer = &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	if j.cfg.RequireBoundSummary {
		// The v2 harness emits its authoritative result last. Retain the bounded
		// tail so arbitrary Actions output cannot evict (or impersonate) it.
		stdout = &tailCappedBuffer{max: defaultRunnerMaxCaptureBytes}
	}
	stderr := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	startErr := startRunnerCommand(runCtx, cmd, j.cfg.StartupTimeout)
	err = startErr
	if startErr == nil {
		err = cmd.Wait()
	}
	containmentErr := javarunner.KillProcessTree(cmd)
	failedStartCapture := errors.Is(startErr, javarunner.ErrProcessContainmentFailed)
	if failedStartCapture {
		j.trackFailedStartReap(cmd, cleanupEventFile)
		eventCleanupPending = false
		containmentErr = errors.Join(containmentErr, startErr)
	}
	if containmentErr != nil {
		finishLocalProcessCapacity(release, containmentErr)
		releasePending = false
		err = errors.Join(err, containmentErr)
	}
	if runCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("java_vuser runner timed out after %s", timeout)
	}
	eventStream, eventStreamTruncated := readRunnerEventFile(eventFile)
	stdoutText, stdoutTruncated := stdout.snapshot()
	stderrText, stderrTruncated := stderr.snapshot()
	if failedStartCapture {
		// The failed-start reaper may still be draining output or the event
		// file. Never present an early bounded snapshot as complete evidence.
		eventStreamTruncated = true
		stdoutTruncated = true
		stderrTruncated = true
	}
	var boundEvents []runnerEvent
	eventStreamBytes := len(eventStream)
	if j.cfg.RequireBoundSummary {
		fileEvents := filterV2RunnerEvents(parseRunnerEvents(eventStream), cmdPayload.RequestID)
		fallbackEvents := filterV2RunnerEvents(parseRunnerEvents(stderrText), cmdPayload.RequestID)
		boundEvents = mergeRunnerEvents(fileEvents, fallbackEvents)
		if len(fallbackEvents) > 0 {
			eventStreamBytes += len(stderrText)
			eventStreamTruncated = eventStreamTruncated || stderrTruncated
		}
	}
	var boundSummary runnerSummary
	hasBoundSummary := false
	if j.cfg.RequireBoundSummary {
		boundSummary, hasBoundSummary = parseBoundRunnerSummary(stdoutText, cmdPayload.RequestID)
		if hasBoundSummary {
			eventStreamTruncated = eventStreamTruncated || boundSummary.EventsTruncated ||
				(boundSummary.EventFallbackUsed && stderrTruncated)
		}
		if err == nil && !hasBoundSummary {
			err = fmt.Errorf("java_vuser runner produced no matching result summary")
		}
	}
	if j.cfg.RequireBoundSummary {
		finalizeResultWithAuthoritativeSummaryAndEvents(
			result, runStart, action, err, stdoutText, stderrText,
			boundSummary, hasBoundSummary, boundEvents,
		)
	} else {
		finalizeResult(result, runStart, action, err, stdoutText, stderrText, eventStream)
	}
	if stdoutTruncated {
		result.ProtocolEvidence["runner_stdout_truncated"] = true
	}
	if stderrTruncated {
		result.ProtocolEvidence["runner_stderr_truncated"] = true
	}
	if eventFile != "" {
		result.ProtocolEvidence["runner_event_stream_bytes"] = eventStreamBytes
	}
	if eventStreamTruncated {
		result.ProtocolEvidence["runner_event_stream_truncated"] = true
		result.Extractions["java_vuser_event_stream_truncated"] = "true"
	}
	if emit != nil {
		events := boundEvents
		truncatedEvents := eventStreamTruncated
		if !j.cfg.RequireBoundSummary {
			eventText := eventStream
			if strings.TrimSpace(eventText) == "" {
				eventText = stderrText
				truncatedEvents = stderrTruncated
			}
			events = parseRunnerEvents(eventText)
		}
		if err := emitRunnerTransactionsAt(action, events, result.StartTime.Add(result.Duration), truncatedEvents, emit); err != nil {
			return result, err
		}
	}
	return result, nil
}

const defaultRunnerMaxCaptureBytes = 256 << 10 // 256 KiB per stream

type runnerCaptureBuffer interface {
	io.Writer
	snapshot() (string, bool)
}

// cappedBuffer captures up to max bytes and discards the rest. Write always
// reports the full length so cmd.Run does not abort a still-running child with
// io.ErrShortWrite/SIGPIPE; the overflow is dropped and flagged via truncated.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if remaining := c.max - c.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			c.buf.Write(p[:remaining])
			c.truncated = true
		} else {
			c.buf.Write(p)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

func (c *cappedBuffer) snapshot() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String(), c.truncated
}

// tailCappedBuffer retains the final max bytes. It is used only for the modern
// request-bound one-shot contract, whose authoritative result frame is last.
type tailCappedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated bool
}

func (c *tailCappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	written := len(p)
	if written == 0 {
		return 0, nil
	}
	if c.max <= 0 {
		c.truncated = true
		return written, nil
	}
	if len(p) >= c.max {
		hadBuffered := len(c.buf) > 0
		c.buf = append(c.buf[:0], p[len(p)-c.max:]...)
		c.truncated = c.truncated || hadBuffered || len(p) > c.max
		return written, nil
	}
	overflow := len(c.buf) + len(p) - c.max
	if overflow > 0 {
		copy(c.buf, c.buf[overflow:])
		c.buf = c.buf[:len(c.buf)-overflow]
		c.truncated = true
	}
	c.buf = append(c.buf, p...)
	return written, nil
}

func (c *tailCappedBuffer) snapshot() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf), c.truncated
}

func (j *JavaVuser) Teardown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	leaseClaimed := j.stopSidecarLeaseKeeper()
	j.sessionMu.Lock()
	defer j.sessionMu.Unlock()

	var teardownErrs []error
	if j.session != nil {
		closeErr := j.session.close(ctx, j.cfg.ActionTimeout)
		if closeErr != nil {
			switch {
			case j.sessionErr == nil:
				teardownErrs = append(teardownErrs, closeErr)
			case errors.Is(closeErr, javarunner.ErrProcessContainmentFailed):
				// The action already reported its invocation failure, but a
				// distinct containment failure means capacity was quarantined
				// and the generator is terminal. Never hide that teardown fact.
				teardownErrs = append(
					teardownErrs,
					fmt.Errorf(
						"java_vuser persistent runner teardown failed (process_containment_unavailable): %w",
						javarunner.ErrProcessContainmentFailed,
					),
				)
			}
			if errors.Is(closeErr, javarunner.ErrProcessContainmentFailed) {
				teardownErrs = append(teardownErrs, protocol.ErrContainmentUnproven)
			}
		}
		j.session = nil
	}
	if leaseClaimed && j.sidecar != nil {
		// Runtime revoke encompasses every one-shot execution and persistent JVM
		// created by this JavaVuser. The server acknowledges only after positive
		// reap, making this the engine's exact quiescence barrier.
		if revokeErr := j.revokeSidecarRuntimeLease(); revokeErr != nil {
			teardownErrs = append(teardownErrs, revokeErr)
		}
		j.sidecarSessionID = ""
		j.sidecarSessionLive = false
	} else if j.sidecarSessionID != "" && j.sidecar != nil {
		_, closeErr := j.sidecar.CloseSession(ctx, j.sidecarSessionID)
		if closeErr != nil {
			code := sidecarCallErrorCode(closeErr)
			if j.sidecarSessionErr == nil || code == "process_containment_unavailable" {
				teardownErrs = append(teardownErrs, fmt.Errorf("java_vuser sidecar session teardown failed (%s)", code))
			}
		}
		j.sidecarSessionID = ""
		j.sidecarSessionLive = false
	}
	if reapErr := j.waitForFailedStartReaps(ctx, j.cfg.ActionTimeout); reapErr != nil {
		teardownErrs = append(teardownErrs, reapErr)
	}
	if localProcessContainmentFailed() {
		reapCtx := ctx
		cancel := func() {}
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			timeout := j.cfg.ActionTimeout
			if timeout <= 0 || timeout > defaultActionTimeout {
				timeout = defaultActionTimeout
			}
			reapCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		if reapErr := javarunner.WaitForProcessContainmentReaps(reapCtx); reapErr != nil {
			teardownErrs = append(
				teardownErrs,
				fmt.Errorf("java_vuser process containment reap: %w", reapErr),
			)
		}
		cancel()
		teardownErrs = append(teardownErrs, errors.Join(
			protocol.ErrContainmentUnproven,
			javarunner.ErrProcessContainmentFailed,
		))
	}
	return errors.Join(teardownErrs...)
}

func (j *JavaVuser) trackFailedStartReap(cmd *exec.Cmd, cleanup func()) {
	j.trackFailedStartReapDoneWithCleanup(javarunner.ReapFailedStart(cmd), cleanup)
}

func (j *JavaVuser) trackFailedStartReapDone(reaped <-chan error) {
	j.trackFailedStartReapDoneWithCleanup(reaped, nil)
}

func (j *JavaVuser) trackFailedStartReapDoneWithCleanup(reaped <-chan error, cleanup func()) {
	if reaped == nil {
		if cleanup != nil {
			cleanup()
		}
		return
	}
	done := make(chan struct{})
	j.failedStartMu.Lock()
	j.failedStartReaps = append(j.failedStartReaps, done)
	j.failedStartMu.Unlock()
	go func() {
		<-reaped
		if cleanup != nil {
			cleanup()
		}
		close(done)
	}()
}

func (j *JavaVuser) waitForFailedStartReaps(ctx context.Context, fallback time.Duration) error {
	j.failedStartMu.Lock()
	reaps := append([]<-chan struct{}(nil), j.failedStartReaps...)
	j.failedStartReaps = nil
	j.failedStartMu.Unlock()
	if len(reaps) == 0 {
		return nil
	}
	waitCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		if fallback <= 0 || fallback > defaultActionTimeout {
			fallback = defaultActionTimeout
		}
		waitCtx, cancel = context.WithTimeout(ctx, fallback)
	}
	defer cancel()
	for index, reaped := range reaps {
		select {
		case <-reaped:
		case <-waitCtx.Done():
			j.failedStartMu.Lock()
			j.failedStartReaps = append(j.failedStartReaps, reaps[index:]...)
			j.failedStartMu.Unlock()
			return fmt.Errorf("java_vuser failed-start process reap: %w", waitCtx.Err())
		}
	}
	return nil
}

func (j *JavaVuser) RuntimeCapabilities() protocol.RuntimeCapabilities {
	cfg := j.effectiveConfiguration()
	if cfg.RunnerMode == config.JavaVuserRunnerModeSidecar {
		if !cfg.Enabled {
			return protocol.RuntimeCapabilities{MaxVUsers: 0}
		}
		client, err := j.sidecarClient(cfg)
		if err != nil {
			return protocol.RuntimeCapabilities{MaxVUsers: 0}
		}
		ctx, cancel := context.WithTimeout(context.Background(), defaultHealthTimeout)
		defer cancel()
		capabilities, err := cachedSidecarCapabilities(ctx, cfg.RunnerURL, cfg.AuthToken, client)
		// The floor is evaluated against this process's legacy opt-in, not the
		// bare constant: the sidecar reports its own major version, and gating
		// on MinimumJavaMajor here would zero out capacity for a legacy lane the
		// operator deliberately enabled, with no error naming this check.
		if err != nil || !capabilities.Healthy || capabilities.JavaMajor < javarunner.EffectiveMinimumJavaMajor(cfg.AllowLegacyJava) {
			return protocol.RuntimeCapabilities{MaxVUsers: 0}
		}
		// External mode is eligible only when the peer explicitly identifies
		// itself as the out-of-process runtime and proves both owner fencing and
		// renewable owner leases. Authenticated session frames remain a separate
		// requirement for persistent phase execution.
		if !protocol.RuntimeFeaturesSatisfy(capabilities.Features, javasidecar.FeatureOwnerFencingV1) ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, javasidecar.FeatureExternalRuntimeV1) ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, javasidecar.FeatureRunnerOwnerLeaseV1) ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, javasidecar.FeatureRuntimeLeaseV1) {
			return protocol.RuntimeCapabilities{MaxVUsers: 0}
		}
		features := make([]string, 0, len(capabilities.Features))
		seen := make(map[string]struct{}, len(capabilities.Features))
		for _, raw := range capabilities.Features {
			feature := stableSidecarCode(raw, "")
			if feature == "" || feature == protocol.FeatureNativeDataJavaV1 {
				continue
			}
			if _, exists := seen[feature]; exists {
				continue
			}
			seen[feature] = struct{}{}
			features = append(features, feature)
		}
		if protocol.RuntimeFeaturesSatisfy(features, protocol.FeatureNativeDataSidecarV1+"+"+protocol.FeatureJavaVuserPersistentLifecycleV1+"+"+protocol.FeatureJavaVuserSessionFrameTokenV1) {
			features = append(features, protocol.FeatureNativeDataJavaV1)
		}
		result := protocol.RuntimeCapabilities{Available: true, Features: features, MaxVUsers: int64(capabilities.MaxProcesses)}
		if shared := capabilities.SharedJVM; shared != nil && protocol.RuntimeFeaturesSatisfy(features, javasidecar.FeatureSharedJVMV1) && shared.VUsersPerJVM >= 2 && shared.MaxVUsers > 0 {
			result.SharedJVM = &protocol.SharedJVMCapacity{VUsersPerJVM: int64(shared.VUsersPerJVM), MaxVUsers: int64(shared.MaxVUsers), AvailableVUsers: max(0, min(int64(shared.AvailableVUsers), int64(shared.MaxVUsers)))}
		}
		return result
	}
	if !cfg.Enabled {
		return protocol.RuntimeCapabilities{MaxVUsers: 0}
	}
	if localProcessContainmentFailed() {
		return protocol.RuntimeCapabilities{MaxVUsers: 0}
	}
	prepareCtx, prepareCancel := context.WithTimeout(context.Background(), defaultHealthTimeout)
	defer prepareCancel()
	prepared, err := j.prepareLocalConfiguration(prepareCtx, cfg)
	if err != nil {
		return protocol.RuntimeCapabilities{MaxVUsers: 0}
	}
	available := true
	// Availability is the base legacy full_lifecycle contract. Only a live
	// harness capability frame may add persistent per-VUser lifecycle support.
	features := []string{}
	containmentCtx, containmentCancel := context.WithTimeout(context.Background(), defaultHealthTimeout)
	strongProcessContainment := j.supportsStrongProcessContainmentAvailable(containmentCtx)
	containmentCancel()
	if strongProcessContainment {
		features = append(features, FeatureLocalProcessContainmentV1)
	}
	if prepared.DirectJavaRunner {
		evidence, evidenceErr := modernHarnessEvidence(prepared)
		healthCtx, cancel := context.WithTimeout(context.Background(), defaultHealthTimeout)
		defer cancel()
		if strongProcessContainment &&
			evidenceErr == nil && evidence == harnessEvidencePresent &&
			checkHarnessFeature(healthCtx, prepared, protocol.FeatureJavaVuserPersistentLifecycleV1) == nil &&
			checkHarnessFeature(healthCtx, prepared, protocol.FeatureJavaVuserSessionFrameTokenV1) == nil {
			if checkHarnessFeature(healthCtx, prepared, protocol.FeatureNativeDataLocalV1) == nil {
				features = append(features, protocol.FeatureNativeDataLocalV1, protocol.FeatureNativeDataServicesV1, protocol.FeatureNativeDataJavaV1)
			}
			features = append(features,
				protocol.FeatureJavaVuserPhaseLifecycleV1,
				protocol.FeatureJavaVuserPersistentLifecycleV1,
				protocol.FeatureJavaVuserSessionFrameTokenV1,
				javaVuserEventV2,
			)
		}
	}
	if localProcessContainmentFailed() {
		return protocol.RuntimeCapabilities{MaxVUsers: 0}
	}
	return protocol.RuntimeCapabilities{
		Available: available,
		Features:  features,
		MaxVUsers: int64(prepared.MaxProcesses),
	}
}

// cachedSidecarCapabilities collapses concurrent probes and keeps successful
// inventory stable for one heartbeat window. Failures are cached only briefly
// so a restarted sidecar is discovered promptly.
func cachedSidecarCapabilities(ctx context.Context, runnerURL, authToken string, client javaVuserSidecarClient) (javasidecar.CapabilitiesResponse, error) {
	key := sidecarCredentialCacheKey(runnerURL, authToken)
	for {
		sidecarCapabilitiesCache.Lock()
		if sidecarCapabilitiesCache.entries == nil {
			sidecarCapabilitiesCache.entries = make(map[string]*sidecarCapabilitiesCacheEntry)
		}
		if entry := sidecarCapabilitiesCache.entries[key]; entry != nil {
			if entry.ready != nil {
				ready := entry.ready
				sidecarCapabilitiesCache.Unlock()
				select {
				case <-ready:
					continue
				case <-ctx.Done():
					return javasidecar.CapabilitiesResponse{}, ctx.Err()
				}
			}
			if time.Now().Before(entry.expiresAt) {
				response, err := entry.response, entry.err
				sidecarCapabilitiesCache.Unlock()
				return response, err
			}
		}
		pending := &sidecarCapabilitiesCacheEntry{ready: make(chan struct{})}
		sidecarCapabilitiesCache.entries[key] = pending
		sidecarCapabilitiesCache.Unlock()

		response, err := client.Capabilities(ctx)
		ttl := sidecarCapabilitiesSuccessTTL
		if err != nil {
			ttl = sidecarCapabilitiesFailureTTL
		}
		sidecarCapabilitiesCache.Lock()
		pending.response = response
		pending.err = err
		pending.expiresAt = time.Now().Add(ttl)
		close(pending.ready)
		pending.ready = nil
		sidecarCapabilitiesCache.Unlock()
		return response, err
	}
}

// publishFreshSidecarCapabilities makes an explicit runtime preflight
// authoritative for subsequent admission checks. Inventory reads deliberately
// cache successful sidecar capabilities for one heartbeat window, but
// distributed CommitWork performs a fresh target preflight. Without publishing
// that observation, a restarted sidecar with a lower MaxProcesses value could
// pass aggregate admission against the previous process's cached limit.
func publishFreshSidecarCapabilities(runnerURL, authToken string, response javasidecar.CapabilitiesResponse, err error) {
	ttl := sidecarCapabilitiesSuccessTTL
	if err != nil {
		ttl = sidecarCapabilitiesFailureTTL
	}
	entry := &sidecarCapabilitiesCacheEntry{
		response:  response,
		err:       err,
		expiresAt: time.Now().Add(ttl),
	}
	key := sidecarCredentialCacheKey(runnerURL, authToken)
	sidecarCapabilitiesCache.Lock()
	if sidecarCapabilitiesCache.entries == nil {
		sidecarCapabilitiesCache.entries = make(map[string]*sidecarCapabilitiesCacheEntry)
	}
	sidecarCapabilitiesCache.entries[key] = entry
	sidecarCapabilitiesCache.Unlock()
}

// LegacyRuntimeAvailable preserves the original one-shot full_lifecycle
// contract without claiming the modern phase-lifecycle capability.
func (j *JavaVuser) LegacyRuntimeAvailable() bool {
	cfg := j.effectiveConfiguration()
	if cfg.RunnerMode == config.JavaVuserRunnerModeSidecar {
		_, err := j.sidecarClient(cfg)
		return cfg.Enabled && err == nil
	}
	if localProcessContainmentFailed() {
		return false
	}
	prepareCtx, cancel := context.WithTimeout(context.Background(), defaultHealthTimeout)
	defer cancel()
	_, err := j.prepareLocalConfiguration(prepareCtx, cfg)
	return err == nil
}

func (j *JavaVuser) LegacyRuntimeHealthy() bool {
	if j.effectiveConfiguration().RunnerMode == config.JavaVuserRunnerModeSidecar {
		ctx, cancel := context.WithTimeout(context.Background(), defaultHealthTimeout)
		defer cancel()
		return j.HealthCheck(ctx) == nil
	}
	return j.LegacyRuntimeAvailable() && subprocessguard.ValidateAmbientCredentials() == nil
}

func (j *JavaVuser) RuntimeHealthy(ctx context.Context) bool {
	cfg := j.effectiveConfiguration()
	if cfg.RunnerMode != config.JavaVuserRunnerModeSidecar && localProcessContainmentFailed() {
		return false
	}
	key := strings.Join([]string{
		strconv.FormatBool(cfg.Enabled), cfg.RunnerMode, sidecarCredentialCacheKey(cfg.RunnerURL, cfg.AuthToken), cfg.RunnerCommand, cfg.HarnessJar,
		strings.Join(cfg.Classpath, string(filepath.ListSeparator)), cfg.WorkDir, cfg.JDKSelection, cfg.JDKHome,
		cfg.DefaultJDKHome, strconv.FormatBool(cfg.AllowLegacyJava),
		strconv.FormatBool(cfg.RunnerCommandExplicit), os.Getenv("JAVA_HOME"),
		os.Getenv("JRE_HOME"), os.Getenv("PATH"), localHarnessCapabilitiesKey(cfg),
	}, "\x00")
	healthCache.Lock()
	if healthCache.key == key && time.Since(healthCache.checkedAt) < 2*time.Second {
		healthy := healthCache.healthy
		healthCache.Unlock()
		return healthy
	}
	healthCache.Unlock()
	healthy := j.checkRunnerHealth(ctx, cfg) == nil
	healthCache.Lock()
	healthCache.key = key
	healthCache.checkedAt = time.Now()
	healthCache.healthy = healthy
	healthCache.Unlock()
	return healthy
}

// sidecarCredentialCacheKey scopes runtime probe results to the endpoint and
// credential that produced them. Only the digest is retained as shared cache
// state so an operator-owned runner token never becomes a printable map key.
func sidecarCredentialCacheKey(runnerURL, authToken string) string {
	endpoint := strings.TrimSpace(runnerURL)
	if endpoint == "" {
		endpoint = config.DefaultJavaVuserRunnerURL
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, endpoint)
	_, _ = hash.Write([]byte{0})
	_, _ = io.WriteString(hash, authToken)
	return hex.EncodeToString(hash.Sum(nil))
}

func (j *JavaVuser) HealthCheck(ctx context.Context) error {
	return j.checkRunnerHealth(ctx, j.effectiveConfiguration())
}

func (j *JavaVuser) checkRunnerHealth(ctx context.Context, cfg configuration) error {
	if cfg.RunnerMode != config.JavaVuserRunnerModeSidecar {
		return j.checkLocalRunnerHealth(ctx, cfg)
	}
	if !cfg.Enabled {
		return fmt.Errorf("java_vuser runner is disabled")
	}
	client, err := j.sidecarClient(cfg)
	if err != nil {
		return fmt.Errorf("java_vuser sidecar configuration is invalid")
	}
	healthCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		healthCtx, cancel = context.WithTimeout(ctx, defaultHealthTimeout)
	}
	defer cancel()
	health, err := client.Health(healthCtx)
	if err != nil {
		return fmt.Errorf("java_vuser sidecar health check failed (%s)", sidecarCallErrorCode(err))
	}
	if !strings.EqualFold(strings.TrimSpace(health.Status), "healthy") || health.ErrorCode != "" {
		return fmt.Errorf("java_vuser sidecar health check failed (%s)", stableSidecarCode(health.ErrorCode, "unhealthy"))
	}
	return nil
}

func (j *JavaVuser) sidecarClient(cfg configuration) (javaVuserSidecarClient, error) {
	if j.sidecar != nil {
		return j.sidecar, nil
	}
	return javasidecar.NewClientWithToken(cfg.RunnerURL, cfg.AuthToken, nil)
}

func javaVuserProcessOwnerID() string {
	return "generator-" + requestIDPrefix
}

// PreflightRuntime validates the local JVM, harness, classpath, and every
// unique target before the engine allocates VUsers. Errors are deliberately
// value-free: VM arguments may contain resolved secrets and must never be
// reflected into logs or API responses.
func (j *JavaVuser) PreflightRuntime(ctx context.Context, request protocol.RuntimePreflightRequest) (protocol.RuntimePreflightResult, error) {
	nativeRequired := protocol.RuntimeFeaturesSatisfy(request.RequiredFeatures, protocol.FeatureNativeDataServicesV1)
	if nativeRequired {
		for _, target := range request.Targets {
			if !isPersistentLifecycleMethod(target.Method) {
				return protocol.RuntimePreflightResult{}, errors.New("native data services require persistent Java init/action/end methods")
			}
		}
	}

	// Preflight contexts are deliberately short lived. Claiming here would
	// start an owner keeper tied to that short context; it would stop before
	// commit and let the external runtime expire while the test is starting.
	// Validation itself is side-effect free and does not require an owner.
	if err := j.init(ctx, request.Config, false); err != nil {
		return protocol.RuntimePreflightResult{}, err
	}
	if !j.cfg.Enabled {
		return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser runner is disabled; an operator must set %s=true", config.EnvJavaVuserRunnerEnabled)
	}
	if j.cfg.RunnerMode == config.JavaVuserRunnerModeSidecar {
		if nativeRequired {
			capabilities, err := j.sidecar.Capabilities(ctx)
			if err != nil || !protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureNativeDataSidecarV1) {
				return protocol.RuntimePreflightResult{}, errors.New("native_data_sidecar_v1 capability unavailable")
			}
		}
		return j.preflightSidecar(ctx, request.Targets)
	}
	if !targetsUseOnlyFullLifecycle(request.Targets) && !j.supportsStrongProcessContainmentAvailable(ctx) {
		return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser local runtime is unavailable (process_containment_unavailable)")
	}
	if err := j.checkRunnerHealth(ctx, j.cfg); err != nil {
		return protocol.RuntimePreflightResult{}, err
	}
	if nativeRequired && (checkHarnessFeature(ctx, j.cfg, protocol.FeatureNativeDataLocalV1) != nil || !j.supportsStrongProcessContainmentAvailable(ctx)) {
		return protocol.RuntimePreflightResult{}, errors.New("native_data_local_v1 capability unavailable")
	}
	modernHarness := false
	if j.cfg.DirectJavaRunner {
		evidence, evidenceErr := modernHarnessEvidence(j.cfg)
		if evidenceErr != nil {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser local runtime is unavailable (harness_inspection_failed)")
		}
		switch evidence {
		case harnessEvidencePresent:
			modernHarness = true
		case harnessEvidenceInconclusive:
			modernHarness = checkHarnessFeature(ctx, j.cfg, protocol.FeatureJavaVuserPhaseLifecycleV1) == nil
		}
	}
	if !modernHarness {
		// Preserve the pre-v2 wrapper contract exactly: old scripts that request
		// only full_lifecycle and carry no new JVM settings still run. Phase calls
		// and new settings require the modern validation-capable harness.
		if hasJavaVuserRuntimeOverrides(request.Config) || !targetsUseOnlyFullLifecycle(request.Targets) {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser local runtime is unavailable (phase_lifecycle_unavailable)")
		}
		return protocol.RuntimePreflightResult{}, nil
	}
	if !targetsUseOnlyFullLifecycle(request.Targets) {
		if err := checkHarnessFeature(ctx, j.cfg, protocol.FeatureJavaVuserPersistentLifecycleV1); err != nil {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser local runtime is unavailable (persistent_lifecycle_unavailable)")
		}
		if err := checkHarnessFeature(ctx, j.cfg, protocol.FeatureJavaVuserSessionFrameTokenV1); err != nil {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser local runtime is unavailable (persistent_lifecycle_unavailable)")
		}
	}
	if err := checkHarnessFeature(ctx, j.cfg, javaVuserValidationV1); err != nil {
		return protocol.RuntimePreflightResult{}, err
	}
	if j.cfg.DedicatedClassLoader {
		if err := checkHarnessFeature(ctx, j.cfg, javaVuserClassloaderV1); err != nil {
			return protocol.RuntimePreflightResult{}, err
		}
	}
	seen := make(map[string]struct{}, len(request.Targets))
	for index, target := range request.Targets {
		className := strings.TrimSpace(target.ClassName)
		method := canonicalLifecycleMethod(strings.TrimSpace(target.Method))
		if method == "" {
			method = "full_lifecycle"
		}
		key := className + "\x00" + method
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if className == "" {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser target %d has an empty class name", index)
		}
		if err := j.validateRuntimeTarget(ctx, className, method); err != nil {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser target %d validation failed: %w", index, err)
		}
	}
	result := protocol.RuntimePreflightResult{}
	if j.cfg.UseXBootClasspath {
		result.Warnings = append(result.Warnings, modernXBootWarning)
	}
	return result, nil
}

func (j *JavaVuser) preflightSidecar(ctx context.Context, targets []protocol.RuntimePreflightTarget) (protocol.RuntimePreflightResult, error) {
	client := j.sidecar
	if client == nil {
		return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser sidecar client is unavailable")
	}
	if !targetsUseOnlyFullLifecycle(targets) {
		capabilityCtx, capabilityCancel := context.WithTimeout(ctx, defaultHealthTimeout)
		capabilities, capabilityErr := client.Capabilities(capabilityCtx)
		capabilityCancel()
		publishFreshSidecarCapabilities(j.cfg.RunnerURL, j.cfg.AuthToken, capabilities, capabilityErr)
		if capabilityErr != nil || !capabilities.Healthy ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserSessionFrameTokenV1) ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, javasidecar.FeatureOwnerFencingV1) ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, javasidecar.FeatureExternalRuntimeV1) ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, javasidecar.FeatureRunnerOwnerLeaseV1) ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, javasidecar.FeatureRuntimeLeaseV1) {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser sidecar runtime is unavailable (persistent_lifecycle_unavailable)")
		}
	}
	requestTargets := make([]javasidecar.Target, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for index, target := range targets {
		className := strings.TrimSpace(target.ClassName)
		method := canonicalLifecycleMethod(strings.TrimSpace(target.Method))
		if method == "" {
			method = "full_lifecycle"
		}
		if !supportedLifecycleMethod(method) {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser target %d has an unsupported lifecycle method", index)
		}
		key := className + "\x00" + method
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		requestTargets = append(requestTargets, javasidecar.Target{ClassName: className, Method: method})
	}
	validateCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := j.cfg.ActionTimeout
		if timeout <= 0 || timeout > javasidecar.MaximumExecutionTimeout {
			timeout = javasidecar.MaximumExecutionTimeout
		}
		validateCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	response, err := client.Validate(validateCtx, javasidecar.ValidateRequest{
		Runtime:   j.sidecarRuntimeSettings(),
		Targets:   requestTargets,
		TimeoutMS: j.cfg.ActionTimeout.Milliseconds(),
	})
	if err != nil {
		return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser sidecar validation failed (%s)", sidecarCallErrorCode(err))
	}
	if !response.Valid {
		if len(response.Issues) == 0 {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser sidecar validation failed (validation_failed)")
		}
		issue := response.Issues[0]
		code := stableSidecarCode(issue.Code, "validation_failed")
		if issue.Index != nil {
			return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser sidecar validation failed (%s at index %d)", code, *issue.Index)
		}
		return protocol.RuntimePreflightResult{}, fmt.Errorf("java_vuser sidecar validation failed (%s)", code)
	}
	warnings := normalizeSidecarWarnings(response.WarningCodes)
	if j.cfg.UseXBootClasspath && !stringSliceContains(warnings, modernXBootWarning) {
		warnings = append(warnings, modernXBootWarning)
	}
	return protocol.RuntimePreflightResult{Warnings: warnings}, nil
}

func (j *JavaVuser) validateRuntimeTarget(ctx context.Context, className, method string) error {
	command := runCommand{
		ProtocolVersion:  javaVuserIPCContract,
		EventContract:    javaVuserEventV2,
		RequestID:        j.nextRequestID(),
		ClassName:        className,
		Method:           method,
		StartupTimeoutMS: j.cfg.StartupTimeout.Milliseconds(),
		ValidationOnly:   true,
	}
	j.decorateRunCommand(&command)
	args, err := j.commandArgs(command)
	if err != nil {
		return err
	}
	args = append(args, "--validate")
	payload, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encode validation request")
	}
	release, err := acquireProcessSlot(ctx)
	if err != nil {
		return fmt.Errorf("acquire JVM validation slot: %w", err)
	}
	releasePending := true
	defer func() {
		if releasePending {
			release()
		}
	}()
	runCtx := ctx
	cancel := func() {}
	if j.cfg.ActionTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, j.cfg.ActionTimeout)
	}
	defer cancel()
	cmd := exec.CommandContext(runCtx, j.cfg.RunnerCommand, args...)
	javarunner.ConfigureProcessTree(cmd)
	cmd.WaitDelay = runnerWaitDelay
	if j.cfg.WorkDir != "" {
		cmd.Dir = j.cfg.WorkDir
	}
	cmd.Env = subprocessguard.Environment(cloneStringMap(j.cfg.Environment))
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	stdout := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	stderr := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	startErr := startRunnerCommand(runCtx, cmd, j.cfg.StartupTimeout)
	if startErr != nil {
		containmentErr := javarunner.KillProcessTree(cmd)
		if errors.Is(startErr, javarunner.ErrProcessContainmentFailed) {
			j.trackFailedStartReap(cmd, nil)
			containmentErr = errors.Join(containmentErr, startErr)
		}
		if containmentErr != nil {
			finishLocalProcessCapacity(release, containmentErr)
			releasePending = false
			return fmt.Errorf("validation runner containment cleanup failed")
		}
		return fmt.Errorf("validation runner failed to start")
	}
	waitErr := cmd.Wait()
	containmentErr := javarunner.KillProcessTree(cmd)
	if containmentErr != nil {
		finishLocalProcessCapacity(release, containmentErr)
		releasePending = false
		return fmt.Errorf("validation runner containment cleanup failed")
	}
	if waitErr != nil {
		if runCtx.Err() != nil {
			return fmt.Errorf("validation runner timed out or was canceled")
		}
		return fmt.Errorf("validation runner exited unsuccessfully")
	}
	stdoutText, _ := stdout.snapshot()
	summary, ok := parseBoundRunnerSummary(stdoutText, command.RequestID)
	if !ok || summary.Success == nil || !*summary.Success {
		return fmt.Errorf("validation runner did not confirm target compatibility")
	}
	return nil
}

func (j *JavaVuser) checkLocalRunnerHealth(ctx context.Context, cfg configuration) error {
	if localProcessContainmentFailed() {
		return fmt.Errorf("java_vuser local runtime is unavailable (process_containment_unavailable)")
	}
	prepared, err := j.prepareLocalConfiguration(ctx, cfg)
	if err != nil {
		return err
	}
	if err := subprocessguard.ValidateAmbientCredentials(); err != nil {
		return fmt.Errorf("java_vuser local runtime is unavailable (ambient_credentials_detected)")
	}
	// Legacy wrappers and pre-v2 harnesses remain usable for their original
	// one-shot full_lifecycle contract, but never advertise phase support.
	if !prepared.DirectJavaRunner {
		return nil
	}
	evidence, err := modernHarnessEvidence(prepared)
	if err != nil {
		return fmt.Errorf("java_vuser local runtime is unavailable (harness_inspection_failed)")
	}
	if evidence != harnessEvidencePresent {
		// Legacy-but-sufficient deployment: the configured harness demonstrably
		// predates the v2 phase contract, or a wrapper leaves no artifact to
		// inspect. Both remain healthy for the legacy one-shot full_lifecycle
		// contract. Phase-based scripts are refused by the capability gate.
		return nil
	}
	return checkHarnessFeature(ctx, prepared, protocol.FeatureJavaVuserPhaseLifecycleV1)
}

func (j *JavaVuser) effectiveConfiguration() configuration {
	cfg := j.cfg
	if strings.TrimSpace(cfg.RunnerCommand) == "" {
		operator := config.LoadJavaVuserOperatorConfig()
		cfg = configuration{
			Enabled:               operator.Enabled,
			RunnerMode:            operator.RunnerMode,
			RunnerURL:             operator.RunnerURL,
			RunnerCommand:         protocol.FirstNonEmpty(operator.RunnerCommand, defaultRunnerCommand),
			HarnessJar:            operator.HarnessJar,
			Classpath:             operator.Classpath,
			WorkDir:               operator.WorkDir,
			MaxProcesses:          operator.MaxProcesses,
			ActionTimeout:         defaultActionTimeout,
			JDKSelection:          string(javarunner.JDKSelectionInternal),
			DefaultJDKHome:        operator.DefaultJDKHome,
			AuthToken:             operator.AuthToken,
			AllowLegacyJava:       operator.AllowLegacyJava,
			RunnerCommandExplicit: operator.RunnerCommandExplicit,
		}
	}
	if strings.TrimSpace(cfg.RunnerMode) == "" {
		cfg.RunnerMode = config.DefaultJavaVuserRunnerMode
	}
	if strings.TrimSpace(cfg.RunnerURL) == "" {
		cfg.RunnerURL = config.DefaultJavaVuserRunnerURL
	}
	if cfg.MaxProcesses <= 0 {
		cfg.MaxProcesses = defaultMaxProcesses
	}
	if cfg.ActionTimeout <= 0 {
		cfg.ActionTimeout = defaultActionTimeout
	}
	return cfg
}

// prepareLocalConfiguration resolves the exact executor-local inputs used by
// local preflight and execution. Returned errors contain stable categories and
// indexes only: runner paths, classpath entries, and resolved secret-bearing
// values must not be reflected into API errors or logs.
func (j *JavaVuser) prepareLocalConfiguration(ctx context.Context, cfg configuration) (configuration, error) {
	return prepareLocalConfigurationWithResolver(
		ctx,
		cfg,
		resolveJavaVuserJDK,
		j.trackFailedStartReapDone,
	)
}

func prepareLocalConfigurationWithResolver(
	ctx context.Context,
	cfg configuration,
	resolveJDK func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error),
	trackFailedStartReap ...func(<-chan error),
) (configuration, error) {
	if !cfg.Enabled {
		return cfg, fmt.Errorf("java_vuser local runtime is unavailable (runner_disabled)")
	}
	if err := subprocessguard.ValidateAmbientCredentials(); err != nil {
		return configuration{}, fmt.Errorf("java_vuser local runtime is unavailable (ambient_credentials_detected)")
	}
	workDir, err := resolveLocalWorkDir(cfg.WorkDir)
	if err != nil {
		return configuration{}, err
	}
	cfg.WorkDir = workDir
	cfg.Environment = cloneStringMap(cfg.Environment)
	cfg.DirectJavaRunner = javarunner.IsDirectJavaCommand(cfg.RunnerCommand)
	if cfg.DirectJavaRunner {
		if cfg.ResolvedJDK == nil {
			selection := javarunner.JDKSelection(strings.TrimSpace(cfg.JDKSelection))
			if selection == "" {
				selection = javarunner.JDKSelectionInternal
			}
			explicitCommand := ""
			if cfg.RunnerCommandExplicit {
				explicitCommand = cfg.RunnerCommand
			}
			release, slotErr := acquireLocalRuntimeProbe(ctx)
			if slotErr != nil {
				return configuration{}, fmt.Errorf("java_vuser local runtime is unavailable (probe_capacity_unavailable)")
			}
			resolved, err := resolveJDK(ctx, javarunner.ResolveOptions{
				Selection:           selection,
				SpecifiedHome:       cfg.JDKHome,
				ExplicitJavaCommand: explicitCommand,
				DefaultHome:         cfg.DefaultJDKHome,
				AllowLegacyJava:     cfg.AllowLegacyJava,
			})
			if err != nil {
				if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
					if reapDone := javarunner.FailedStartReapDone(err); reapDone != nil &&
						len(trackFailedStartReap) > 0 && trackFailedStartReap[0] != nil {
						trackFailedStartReap[0](reapDone)
					}
					// JDK discovery uses the dedicated probe semaphore rather
					// than a Vuser slot. Keep this exact probe admission
					// occupied and close the terminal gate so no retry can
					// launch a second potentially uncontained tree.
					finishLocalProcessCapacity(release, err)
					return configuration{}, fmt.Errorf(
						"java_vuser local runtime is unavailable (process_containment_unavailable): %w",
						javarunner.ErrProcessContainmentFailed,
					)
				}
				finishLocalProcessCapacity(release, nil)
				return configuration{}, fmt.Errorf("java_vuser local runtime is unavailable (jdk_unavailable)")
			}
			finishLocalProcessCapacity(release, nil)
			cfg.ResolvedJDK = &resolved
			cfg.RunnerCommand = resolved.JavaBin
		}
		for key, value := range cfg.ResolvedJDK.EnvironmentOverrides(os.Getenv("PATH")) {
			cfg.Environment[key] = value
		}
	} else if _, err := exec.LookPath(cfg.RunnerCommand); err != nil {
		return configuration{}, fmt.Errorf("java_vuser local runtime is unavailable (runner_unavailable)")
	}

	if cfg.ClasspathUploadEnabled && len(cfg.ClasspathAssets) > 0 {
		// Materialize ONLY over the script-owned classpath slice, never the
		// operator-provisioned shared entries. cfg.Classpath is
		// scriptClasspath ++ operator.Classpath (built in Init); rewriting an
		// operator entry would let a user-uploaded JAR shadow an operator-trusted
		// dependency the approving reviewer cannot see. The sidecar path already
		// materializes ScriptClasspath only; this mirrors it.
		scriptCount := len(cfg.ScriptClasspath)
		if scriptCount > len(cfg.Classpath) {
			scriptCount = len(cfg.Classpath)
		}
		scriptPortion := append([]string(nil), cfg.Classpath[:scriptCount]...)
		materialized, assetIssue := javarunner.MaterializeClasspathAssets(scriptPortion, localClasspathAssets(cfg.ClasspathAssets), cfg.WorkDir)
		if assetIssue != nil {
			// The parenthesized category is the machine-readable, value-free
			// signal; keep the prose neutral so it does not mislabel an integrity
			// or workdir failure as "unavailable".
			if assetIssue.Index >= 0 {
				return configuration{}, fmt.Errorf("java_vuser classpath asset for entry %d failed (%s)", assetIssue.Index, assetIssue.Category)
			}
			return configuration{}, fmt.Errorf("java_vuser classpath asset failed (%s)", assetIssue.Category)
		}
		cfg.Classpath = append(materialized, cfg.Classpath[scriptCount:]...)
	}
	resolvedClasspath, issue := javarunner.ResolveClasspath(cfg.Classpath, cfg.WorkDir)
	if issue != nil {
		if issue.Index >= 0 {
			return configuration{}, fmt.Errorf("java_vuser classpath entry %d is invalid (%s)", issue.Index, issue.Category)
		}
		return configuration{}, fmt.Errorf("java_vuser classpath is invalid (%s)", issue.Category)
	}
	cfg.Classpath = resolvedClasspath
	if scriptCount := len(cfg.ScriptClasspath); scriptCount > 0 {
		cfg.ScriptClasspath = append([]string(nil), resolvedClasspath[:scriptCount]...)
		if strings.TrimSpace(cfg.HarnessJar) == "" {
			return configuration{}, fmt.Errorf("java_vuser local runtime is unavailable (operator_harness_required)")
		}
	}
	if err := validateRunnerFilesBase(cfg); err != nil {
		return configuration{}, err
	}
	if cfg.DirectJavaRunner {
		evidence, evidenceErr := modernHarnessEvidence(cfg)
		if evidenceErr != nil {
			return configuration{}, fmt.Errorf("java_vuser local runtime is unavailable (harness_inspection_failed)")
		}
		cfg.RequireBoundSummary = evidence == harnessEvidencePresent
	}
	return cfg, nil
}

func validateRunnerFilesBase(cfg configuration) error {
	if _, err := exec.LookPath(cfg.RunnerCommand); err != nil {
		return fmt.Errorf("java_vuser local runtime is unavailable (runner_unavailable)")
	}
	if harnessJar := strings.TrimSpace(cfg.HarnessJar); harnessJar != "" {
		resolved := resolveRunnerPath(cfg.WorkDir, harnessJar)
		info, err := os.Stat(resolved)
		if err != nil {
			return fmt.Errorf("java_vuser local runtime is unavailable (harness_unavailable)")
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("java_vuser local runtime is unavailable (harness_invalid)")
		}
	}
	for index, entry := range cfg.Classpath {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		path := resolveRunnerPath(cfg.WorkDir, entry)
		if strings.ContainsAny(path, "*?") {
			path = filepath.Dir(path)
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("java_vuser classpath entry %d is invalid (%s)", index, javarunner.ClasspathUnavailable)
		}
	}
	return nil
}

func resolveLocalWorkDir(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	if len(raw) > 4096 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("java_vuser local runtime is unavailable (work_dir_unavailable)")
	}
	absolute, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("java_vuser local runtime is unavailable (work_dir_unavailable)")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("java_vuser local runtime is unavailable (work_dir_unavailable)")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("java_vuser local runtime is unavailable (work_dir_unavailable)")
	}
	return resolved, nil
}

// harnessEvidence classifies static inspection of operator-configured runner
// artifacts for the modern v2 phase contract.
type harnessEvidence int

const (
	// harnessEvidenceInconclusive: no operator-configured artifact could be
	// inspected — typically a wrapper runner command that embeds the harness
	// itself. The live --health probe is the authority for these deployments.
	harnessEvidenceInconclusive harnessEvidence = iota
	// harnessEvidenceAbsent: configured artifacts were inspected and none carry
	// the v2 contracts — a legacy deployment that supports only one-shot
	// full_lifecycle scripts.
	harnessEvidenceAbsent
	// harnessEvidencePresent: the v2 contract strings were found.
	harnessEvidencePresent
)

func modernHarnessEvidence(cfg configuration) (harnessEvidence, error) {
	inspected := false
	if harnessJar := strings.TrimSpace(cfg.HarnessJar); harnessJar != "" {
		inspected = true
		found, err := archiveContainsHarnessContracts(resolveRunnerPath(cfg.WorkDir, harnessJar))
		if err != nil {
			return harnessEvidenceAbsent, fmt.Errorf("inspect java_vuser harness jar %q: %w", harnessJar, err)
		}
		if found {
			return harnessEvidencePresent, nil
		}
	}
	operatorClasspath := cfg.Classpath
	if scriptCount := len(cfg.ScriptClasspath); scriptCount <= len(operatorClasspath) {
		operatorClasspath = operatorClasspath[scriptCount:]
	}
	for _, entry := range operatorClasspath {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		inspected = true
		found, err := classpathContainsHarnessContracts(resolveRunnerPath(cfg.WorkDir, entry))
		if err != nil {
			return harnessEvidenceAbsent, fmt.Errorf("inspect java_vuser classpath entry %q: %w", entry, err)
		}
		if found {
			return harnessEvidencePresent, nil
		}
	}
	if workDir := strings.TrimSpace(cfg.WorkDir); workDir != "" {
		if found, _ := classFileContainsHarnessContracts(filepath.Join(workDir, "Harness.class")); found {
			return harnessEvidencePresent, nil
		}
	}
	if inspected {
		return harnessEvidenceAbsent, nil
	}
	return harnessEvidenceInconclusive, nil
}

func classpathContainsHarnessContracts(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	if strings.ContainsAny(path, "*?") {
		matches, err := filepath.Glob(path)
		if err != nil {
			return false, err
		}
		for _, match := range matches {
			extension := strings.ToLower(filepath.Ext(match))
			if extension != ".jar" && extension != ".zip" {
				continue
			}
			if found, err := classpathContainsHarnessContracts(match); err != nil {
				return false, err
			} else if found {
				return true, nil
			}
		}
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return classFileContainsHarnessContracts(filepath.Join(path, "Harness.class"))
	}
	return archiveContainsHarnessContracts(path)
}

func classFileContainsHarnessContracts(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil {
		return false, err
	}
	if len(data) > 8<<20 {
		return false, fmt.Errorf("Harness.class exceeds the inspection limit")
	}
	return classBytesContainHarnessContracts(data), nil
}

func archiveContainsHarnessContracts(path string) (bool, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return false, err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if filepath.Base(file.Name) != "Harness.class" {
			continue
		}
		if file.UncompressedSize64 > 8<<20 {
			return false, fmt.Errorf("Harness.class exceeds the inspection limit")
		}
		reader, err := file.Open()
		if err != nil {
			return false, err
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 8<<20))
		_ = reader.Close()
		if readErr != nil {
			return false, readErr
		}
		if classBytesContainHarnessContracts(data) {
			return true, nil
		}
	}
	return false, nil
}

func classBytesContainHarnessContracts(data []byte) bool {
	return len(data) >= 4 && data[0] == 0xca && data[1] == 0xfe && data[2] == 0xba && data[3] == 0xbe &&
		bytes.Contains(data, []byte(javaVuserIPCContract)) && bytes.Contains(data, []byte(javaVuserEventV2))
}

func resolveRunnerPath(workDir, path string) string {
	if filepath.IsAbs(path) || strings.TrimSpace(workDir) == "" {
		return path
	}
	return filepath.Join(workDir, path)
}

type runnerHealthEnvelope struct {
	Contract      string   `json:"contract"`
	Kind          string   `json:"kind"`
	EventContract string   `json:"event_contract"`
	Features      []string `json:"features"`
}

func checkHarnessFeature(ctx context.Context, cfg configuration, requiredFeature string) error {
	capabilities, err := cachedLocalHarnessCapabilities(ctx, cfg)
	if err != nil {
		return err
	}
	if protocol.RuntimeFeaturesSatisfy(capabilities.Features, requiredFeature) {
		return nil
	}
	return fmt.Errorf("java_vuser harness health probe failed (feature_unavailable)")
}

// cachedLocalHarnessCapabilities keeps successful installed-runtime evidence
// separate from VUser capacity. A full persistent-session pool must not make a
// healthy runner disappear from generator inventory, and concurrent inventory
// readers should share one bounded probe instead of starting redundant JVMs.
func cachedLocalHarnessCapabilities(ctx context.Context, cfg configuration) (runnerHealthEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key := localHarnessCapabilitiesKey(cfg)
	for {
		localHarnessCapabilitiesCache.Lock()
		if localHarnessCapabilitiesCache.entries == nil {
			localHarnessCapabilitiesCache.entries = make(map[string]*localHarnessCapabilitiesCacheEntry)
		}
		if entry := localHarnessCapabilitiesCache.entries[key]; entry != nil {
			if entry.ready != nil {
				ready := entry.ready
				localHarnessCapabilitiesCache.Unlock()
				select {
				case <-ready:
					continue
				case <-ctx.Done():
					return runnerHealthEnvelope{}, ctx.Err()
				}
			}
			if time.Now().Before(entry.expiresAt) {
				response, err := entry.response, entry.err
				localHarnessCapabilitiesCache.Unlock()
				return response, err
			}
		}
		if len(localHarnessCapabilitiesCache.entries) >= maxLocalCapabilitiesEntries {
			for cachedKey, entry := range localHarnessCapabilitiesCache.entries {
				if entry.ready == nil {
					delete(localHarnessCapabilitiesCache.entries, cachedKey)
					break
				}
			}
		}
		pending := &localHarnessCapabilitiesCacheEntry{ready: make(chan struct{})}
		localHarnessCapabilitiesCache.entries[key] = pending
		localHarnessCapabilitiesCache.Unlock()

		response, err := probeLocalHarnessCapabilities(ctx, cfg)
		ttl := localCapabilitiesSuccessTTL
		if err != nil {
			ttl = localCapabilitiesFailureTTL
		}
		localHarnessCapabilitiesCache.Lock()
		pending.response = response
		pending.err = err
		pending.expiresAt = time.Now().Add(ttl)
		close(pending.ready)
		pending.ready = nil
		localHarnessCapabilitiesCache.Unlock()
		return response, err
	}
}

func localHarnessCapabilitiesKey(cfg configuration) string {
	// Hash the exact command-shaping fields so VM parameters (which may contain
	// resolved values) never persist as plaintext map keys or appear in logs.
	payload, _ := json.Marshal(struct {
		RunnerCommand          string
		HarnessJar             string
		ScriptClasspath        []string
		Classpath              []string
		WorkDir                string
		AdditionalVMParameters []string
		DedicatedClassLoader   bool
	}{
		RunnerCommand:          cfg.RunnerCommand,
		HarnessJar:             cfg.HarnessJar,
		ScriptClasspath:        cfg.ScriptClasspath,
		Classpath:              cfg.Classpath,
		WorkDir:                cfg.WorkDir,
		AdditionalVMParameters: cfg.AdditionalVMParameters,
		DedicatedClassLoader:   cfg.DedicatedClassLoader,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func probeLocalHarnessCapabilities(ctx context.Context, cfg configuration) (runnerHealthEnvelope, error) {
	healthCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		healthCtx, cancel = context.WithTimeout(ctx, defaultHealthTimeout)
	}
	defer cancel()
	release, err := acquireLocalRuntimeProbe(healthCtx)
	if err != nil {
		return runnerHealthEnvelope{}, fmt.Errorf("java_vuser harness health probe failed (timeout)")
	}
	releasePending := true
	defer func() {
		if releasePending {
			release()
		}
	}()
	probe := &JavaVuser{cfg: cfg}
	args, err := probe.commandArgs(runCommand{ClassName: "Actions", Method: "action"})
	if err != nil {
		return runnerHealthEnvelope{}, err
	}
	args = append(args, "--health")
	cmd := exec.CommandContext(healthCtx, cfg.RunnerCommand, args...)
	javarunner.ConfigureProcessTree(cmd)
	cmd.WaitDelay = runnerWaitDelay
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}
	cmd.Env = subprocessguard.Environment(nil)
	stdout := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	stderr := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	startErr := javarunner.StartProcessTree(cmd)
	err = startErr
	if startErr == nil {
		err = cmd.Wait()
	}
	containmentErr := javarunner.KillProcessTree(cmd)
	if errors.Is(startErr, javarunner.ErrProcessContainmentFailed) {
		probe.trackFailedStartReap(cmd, nil)
		containmentErr = errors.Join(containmentErr, startErr)
	}
	if containmentErr != nil {
		finishLocalProcessCapacity(nil, containmentErr)
		releasePending = false
		return runnerHealthEnvelope{}, fmt.Errorf("java_vuser harness health probe failed (process_containment_unavailable)")
	}
	if healthCtx.Err() != nil {
		return runnerHealthEnvelope{}, fmt.Errorf("java_vuser harness health probe failed (timeout)")
	}
	if err != nil {
		return runnerHealthEnvelope{}, fmt.Errorf("java_vuser harness health probe failed (process_failed)")
	}
	stdoutText, _ := stdout.snapshot()
	for _, line := range strings.Split(stdoutText, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, sessionFramePrefix) {
			continue
		}
		var envelope runnerHealthEnvelope
		if json.Unmarshal([]byte(strings.TrimPrefix(line, sessionFramePrefix)), &envelope) != nil {
			continue
		}
		if envelope.Contract != javaVuserIPCContract || envelope.Kind != "capabilities" || envelope.EventContract != javaVuserEventV2 {
			continue
		}
		return envelope, nil
	}
	return runnerHealthEnvelope{}, fmt.Errorf("java_vuser harness health probe failed (contract_unavailable)")
}

func acquireLocalRuntimeProbe(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if localProcessContainmentFailed() {
		return nil, fmt.Errorf("java_vuser local runtime is unavailable (process_containment_unavailable)")
	}
	containmentDone := localContainmentFailureDone()
	select {
	case localHarnessProbeSlot <- struct{}{}:
		if localProcessContainmentFailed() {
			<-localHarnessProbeSlot
			return nil, fmt.Errorf("java_vuser local runtime is unavailable (process_containment_unavailable)")
		}
		return func() { <-localHarnessProbeSlot }, nil
	case <-containmentDone:
		return nil, fmt.Errorf("java_vuser local runtime is unavailable (process_containment_unavailable)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (j *JavaVuser) commandArgs(cmd runCommand) ([]string, error) {
	harnessArgs := []string{"--class", cmd.ClassName, "--method", cmd.Method}
	if cmd.SourceArchiveRef != "" {
		harnessArgs = append(harnessArgs, "--source-archive-ref", cmd.SourceArchiveRef)
	}
	if cmd.StartupTimeoutMS > 0 {
		harnessArgs = append(harnessArgs, "--startup-timeout-ms", strconv.FormatInt(cmd.StartupTimeoutMS, 10))
	}
	if j.cfg.DirectJavaRunner || javarunner.IsDirectJavaCommand(j.cfg.RunnerCommand) {
		sharedClasspath := j.cfg.Classpath
		if scriptCount := len(j.cfg.ScriptClasspath); scriptCount <= len(sharedClasspath) {
			sharedClasspath = sharedClasspath[scriptCount:]
		}
		plan, err := javarunner.BuildCommand(javarunner.CommandSpec{
			OperatorCDSManifest: j.cfg.OperatorCDSManifest, ResolvedJDK: j.cfg.ResolvedJDK,
			VMParameters:         j.cfg.AdditionalVMParameters,
			JavaMajor:            resolvedJavaMajor(j.cfg.ResolvedJDK),
			VMFamily:             resolvedJavaVMFamily(j.cfg.ResolvedJDK),
			HarnessJar:           j.cfg.HarnessJar,
			ScriptClasspath:      j.cfg.ScriptClasspath,
			SharedClasspath:      sharedClasspath,
			DedicatedClassLoader: j.cfg.DedicatedClassLoader,
			HarnessMain:          defaultHarnessMain,
			HarnessArgs:          harnessArgs,
		})
		if err != nil {
			return nil, fmt.Errorf("java_vuser command configuration is invalid: %w", err)
		}
		return plan.Args, nil
	}

	args := make([]string, 0, 8)
	harnessJar := strings.TrimSpace(j.cfg.HarnessJar)
	switch {
	case harnessJar != "" && len(j.cfg.Classpath) > 0:
		// `java -jar` ignores -cp/CLASSPATH, so a harness jar combined with a
		// separate user classpath cannot load the user's Actions class. Launch via
		// -cp with the jar and the user classes on one classpath and an explicit
		// main class instead.
		cp := append([]string{harnessJar}, j.cfg.Classpath...)
		args = append(args, "-cp", strings.Join(cp, string(filepath.ListSeparator)), defaultHarnessMain)
	case harnessJar != "":
		args = append(args, "-jar", harnessJar)
	case len(j.cfg.Classpath) > 0:
		args = append(args, "-cp", strings.Join(j.cfg.Classpath, string(filepath.ListSeparator)), defaultHarnessMain)
	case strings.EqualFold(filepath.Base(j.cfg.RunnerCommand), defaultRunnerCommand):
		args = append(args, defaultHarnessMain)
	}
	args = append(args, harnessArgs...)
	return args, nil
}

func resolvedJavaMajor(jdk *javarunner.ResolvedJDK) int {
	if jdk == nil {
		return 0
	}
	return jdk.Major
}

func resolvedJavaVMFamily(jdk *javarunner.ResolvedJDK) javarunner.JavaVMFamily {
	if jdk == nil {
		return javarunner.JavaVMUnknown
	}
	return jdk.VMFamily
}

func (j *JavaVuser) decorateRunCommand(command *runCommand) {
	if command == nil || !j.cfg.DedicatedClassLoader {
		return
	}
	command.DedicatedLoader = true
	command.UserClasspath = append([]string(nil), j.cfg.Classpath...)
}

func finalizeResult(result *protocol.Result, start time.Time, action *protocol.Action, err error, stdout string, stderr string, eventStream string) {
	summary, hasSummary := parseRunnerSummary(stdout)
	eventText := eventStream
	if strings.TrimSpace(eventText) == "" {
		eventText = stderr
	}
	finalizeResultWithParsed(result, start, action, err, stdout, stderr, summary, hasSummary, parseRunnerEvents(eventText))
}

// The v2 transport supplies already request-bound summary/event objects. Never
// reparse arbitrary user stdout/stderr when those authoritative objects exist.
func finalizeResultWithAuthoritativeSummaryAndEvents(
	result *protocol.Result,
	start time.Time,
	action *protocol.Action,
	err error,
	stdout string,
	stderr string,
	summary runnerSummary,
	hasSummary bool,
	events []runnerEvent,
) {
	finalizeResultWithParsed(result, start, action, err, stdout, stderr, summary, hasSummary, events)
}

func finalizeResultWithParsed(result *protocol.Result, start time.Time, action *protocol.Action, err error, stdout string, stderr string, summary runnerSummary, hasSummary bool, events []runnerEvent) {
	result.Duration = time.Since(start)
	result.ResponseBody = stdout
	result.BytesReceived = int64(len(stdout))
	if result.Extractions == nil {
		result.Extractions = map[string]string{}
	}
	if !hasSummary && err == nil {
		err = fmt.Errorf("java_vuser runner produced no result summary")
	}
	if hasSummary {
		if summary.DurationMS > 0 {
			result.Extractions["java_vuser_duration_ms"] = strconv.FormatInt(summary.DurationMS, 10)
		}
		if summary.Success != nil && !*summary.Success && err == nil {
			message := strings.TrimSpace(summary.Error)
			if message == "" {
				message = "runner reported success=false"
			}
			err = fmt.Errorf("java_vuser runner reported failure: %s", message)
		}
	}
	if len(events) > 0 {
		errorEvents := 0
		for _, event := range events {
			if strings.EqualFold(event.Type, "error") {
				errorEvents++
			}
		}
		result.Extractions["java_vuser_event_count"] = strconv.Itoa(len(events))
		if errorEvents > 0 {
			result.Extractions["java_vuser_error_event_count"] = strconv.Itoa(errorEvents)
		}
		failedTransactions := failedRunnerTransactions(events)
		if len(failedTransactions) > 0 {
			result.Extractions["java_vuser_failed_transaction_count"] = strconv.Itoa(len(failedTransactions))
		}
		streamFailures := runnerTransactionStreamFailures(events)
		if len(streamFailures) > 0 {
			result.Extractions["java_vuser_transaction_diagnostic_count"] = strconv.Itoa(len(streamFailures))
		}
	}
	if err != nil {
		result.Success = false
		result.StatusCode = 500
		result.ErrorMessage = err.Error()
	}
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence["protocol"] = javaVuserProtocolName
	result.ProtocolEvidence["runner_stdout_bytes"] = len(stdout)
	result.ProtocolEvidence["runner_stderr_bytes"] = len(stderr)
	if hasSummary {
		result.ProtocolEvidence["runner_result"] = summary
	}
	if len(events) > 0 {
		result.ProtocolEvidence["runner_events"] = events
		result.ProtocolEvidence["runner_event_count"] = len(events)
		errorEvents := 0
		for _, event := range events {
			if strings.EqualFold(event.Type, "error") {
				errorEvents++
			}
		}
		if errorEvents > 0 {
			result.ProtocolEvidence["runner_error_event_count"] = errorEvents
		}
	}
	if stderr != "" {
		result.ProtocolEvidence["runner_stderr"] = stderr
	}
}

func startRunnerCommand(ctx context.Context, cmd *exec.Cmd, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Process creation must remain synchronously owned by the caller. Running
	// Start in a goroutine can return on a timer and then launch an orphan after
	// the caller has released its capacity slot and temporary files. Actions
	// construction is bounded inside the harness by startup_timeout_ms.
	return javarunner.StartProcessTree(cmd)
}

func createRunnerEventFile() (string, func()) {
	file, err := os.CreateTemp("", "apexload-java-vuser-events-*.jsonl")
	if err != nil {
		return "", nil
	}
	path := file.Name()
	_ = file.Close()
	return path, func() { _ = os.Remove(path) }
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values)+1)
	for key, value := range values {
		if strings.TrimSpace(value) != "" {
			cloned[key] = value
		}
	}
	return cloned
}

func readRunnerEventFile(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(file, int64(defaultRunnerMaxCaptureBytes)+1))
	if err != nil {
		return "", false
	}
	data := buf.Bytes()
	if n <= int64(defaultRunnerMaxCaptureBytes) {
		return string(data), false
	}
	return string(data[:defaultRunnerMaxCaptureBytes]), true
}

func failedRunnerTransactions(events []runnerEvent) []string {
	failed := []string{}
	for _, event := range events {
		isUnmatched := strings.EqualFold(event.Type, "transaction_unmatched")
		if !strings.EqualFold(event.Type, "transaction_end") && !isUnmatched {
			continue
		}
		status, ok := runnerTransactionStatus(event.Message)
		if !isUnmatched && (!ok || status == 0) {
			continue
		}
		name := strings.TrimSpace(event.Name)
		if name == "" {
			name = "(unnamed)"
		}
		if isUnmatched {
			failed = append(failed, fmt.Sprintf("%s=unmatched", name))
		} else {
			failed = append(failed, fmt.Sprintf("%s=%d", name, status))
		}
	}
	return failed
}

// runnerTransactionStreamFailures separates malformed runner evidence from an
// ordinary failed business transaction. LoadRunner records FAIL/STOP as the
// child transaction outcome without automatically failing the enclosing Java
// action; only malformed/unmatched/lost transaction evidence is an aggregate
// runner failure.
func runnerTransactionStreamFailures(events []runnerEvent) []string {
	failures := make([]string, 0)
	open := make(map[string]int)
	for _, event := range events {
		typeName := strings.ToLower(strings.TrimSpace(event.Type))
		key := transactionEventKey(event)
		name := strings.TrimSpace(event.Name)
		if name == "" {
			name = "(unnamed)"
		}
		switch typeName {
		case "transaction_start":
			open[key]++
		case "transaction_unmatched":
			failures = append(failures, name+"=unmatched")
			if open[key] > 0 {
				open[key]--
			}
		case "transaction_ignored":
			if open[key] > 0 {
				open[key]--
			}
		case "transaction_end":
			if open[key] == 0 {
				failures = append(failures, name+"=missing_start")
			} else {
				open[key]--
			}
			if _, ok := runnerTransactionStatus(event.Message); !ok {
				failures = append(failures, name+"=invalid_status")
			}
			if event.DurationMS != nil && *event.DurationMS < 0 {
				failures = append(failures, name+"=invalid_duration")
			}
		}
	}
	for _, event := range events {
		if strings.EqualFold(strings.TrimSpace(event.Type), "transaction_start") &&
			open[transactionEventKey(event)] > 0 {
			name := strings.TrimSpace(event.Name)
			if name == "" {
				name = "(unnamed)"
			}
			failures = append(failures, name+"=missing_end")
			open[transactionEventKey(event)]--
		}
	}
	return failures
}

func runnerTransactionStatus(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if strings.EqualFold(raw, "PASS") {
		return 0, true
	}
	if strings.EqualFold(raw, "FAIL") {
		return 1, true
	}
	status, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return status, true
}

func parseRunnerSummary(stdout string) (runnerSummary, bool) {
	for _, line := range reverseNonEmptyLines(stdout) {
		var summary runnerSummary
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			continue
		}
		if summary.Success != nil || summary.Error != "" || summary.DurationMS > 0 || summary.ClassName != "" || summary.Method != "" {
			return summary, true
		}
	}
	return runnerSummary{}, false
}

func parseBoundRunnerSummary(stdout, requestID string) (runnerSummary, bool) {
	for _, line := range reverseNonEmptyLines(stdout) {
		var summary runnerSummary
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			continue
		}
		if summary.Contract == javaVuserIPCContract && summary.Kind == "result" &&
			summary.RequestID == requestID && summary.Success != nil {
			return summary, true
		}
	}
	return runnerSummary{}, false
}

func parseRunnerEvents(stderr string) []runnerEvent {
	lines := strings.Split(stderr, "\n")
	events := make([]runnerEvent, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event runnerEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		event.Type = strings.TrimSpace(event.Type)
		event.Contract = strings.TrimSpace(event.Contract)
		event.RequestID = strings.TrimSpace(event.RequestID)
		event.TransactionID = strings.TrimSpace(event.TransactionID)
		event.Name = strings.TrimSpace(event.Name)
		event.Message = strings.TrimSpace(event.Message)
		if event.Type == "" {
			continue
		}
		events = append(events, event)
	}
	return events
}

func reverseNonEmptyLines(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for i := len(raw) - 1; i >= 0; i-- {
		line := strings.TrimSpace(raw[i])
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func acquireProcessSlot(ctx context.Context) (func(), error) {
	if localProcessContainmentFailed() {
		return nil, fmt.Errorf("java_vuser local runtime is unavailable (process_containment_unavailable)")
	}
	slots := processSlotChan()
	containmentDone := localContainmentFailureDone()
	select {
	case slots <- struct{}{}:
		if localProcessContainmentFailed() {
			<-slots
			return nil, fmt.Errorf("java_vuser local runtime is unavailable (process_containment_unavailable)")
		}
		return func() { <-slots }, nil
	case <-containmentDone:
		return nil, fmt.Errorf("java_vuser local runtime is unavailable (process_containment_unavailable)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func finishLocalProcessCapacity(release func(), containmentErr error) {
	if containmentErr != nil {
		// Retaining the token quarantines the unproven process tree. Releasing
		// it would allow MaxProcesses to bound JVM roots while orphan native
		// descendants accumulate without limit.
		markLocalContainmentFailed()
		return
	}
	if release != nil {
		release()
	}
}

func (j *JavaVuser) nextRequestID() string {
	sequence := atomic.AddUint64(&requestIDSeq, 1)
	return fmt.Sprintf("java-vuser-%s-%d", requestIDPrefix, sequence)
}

func newRequestIDPrefix() string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func sidecarCallErrorCode(err error) string {
	var remote *javasidecar.RemoteError
	if errors.As(err, &remote) {
		return stableSidecarCode(remote.Code, "request_failed")
	}
	return "unavailable"
}

func stableSidecarCode(code, fallback string) string {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 128 {
		return fallback
	}
	for _, character := range code {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return fallback
	}
	if _, known := knownSidecarCodes[code]; !known {
		return fallback
	}
	return code
}

func normalizeSidecarWarnings(codes []string) []string {
	seen := make(map[string]struct{}, len(codes))
	warnings := make([]string, 0, len(codes))
	for _, raw := range codes {
		code := stableSidecarCode(raw, "")
		if code == "" {
			continue
		}
		if code == javasidecar.WarningModernXBootClasspathFallback {
			code = modernXBootWarning
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		warnings = append(warnings, code)
	}
	return warnings
}

func stringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// javaLifecycleMethodNames are the exact method spellings the runner harness
// dispatches ("Action" is a distinct reflective target from "action" in Java,
// so both exact literals pass through unchanged).
var javaLifecycleMethodNames = map[string]struct{}{
	"init": {}, "vuser_init": {}, "initialize": {},
	"action": {}, "Action": {}, "run": {},
	"end": {}, "vuser_end": {}, "teardown": {},
	"full_lifecycle": {},
}

func actionMethod(action *protocol.Action) string {
	method := strings.TrimSpace(stringParam(action.Params, "method"))
	if method == "" {
		return "full_lifecycle"
	}
	return canonicalLifecycleMethod(method)
}

// canonicalLifecycleMethod maps any casing the DSL validator accepts (it
// lowercases params.method before matching) onto the exact spelling the Java
// harness dispatches. Exact known literals pass through untouched; unknown
// names are preserved so genuine errors keep their authored spelling.
func canonicalLifecycleMethod(method string) string {
	if _, ok := javaLifecycleMethodNames[method]; ok {
		return method
	}
	lower := strings.ToLower(method)
	if _, ok := javaLifecycleMethodNames[lower]; ok {
		return lower
	}
	return method
}

func supportedLifecycleMethod(method string) bool {
	_, ok := javaLifecycleMethodNames[canonicalLifecycleMethod(strings.TrimSpace(method))]
	return ok
}

func targetsUseOnlyFullLifecycle(targets []protocol.RuntimePreflightTarget) bool {
	for _, target := range targets {
		method := canonicalLifecycleMethod(strings.TrimSpace(target.Method))
		if method == "" {
			method = "full_lifecycle"
		}
		if !strings.EqualFold(method, "full_lifecycle") {
			return false
		}
	}
	return true
}

func actionTimeout(action *protocol.Action, fallback time.Duration) (time.Duration, error) {
	if action == nil || action.Params == nil {
		return fallback, nil
	}
	value, present := action.Params["timeout_ms"]
	if !present {
		return fallback, nil
	}
	timeout, err := protocol.BoundedMilliseconds(value, fallback, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return 0, fmt.Errorf("java_vuser action timeout_ms %w", err)
	}
	return timeout, nil
}

func resolveJavaVuserJDK(ctx context.Context, options javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
	key := strings.Join([]string{
		string(options.Selection), options.SpecifiedHome, options.ExplicitJavaCommand, options.DefaultHome,
		os.Getenv("JAVA_HOME"), os.Getenv("JRE_HOME"), os.Getenv("PATH"),
	}, "\x00")
	now := time.Now()
	resolvedJDKCache.Lock()
	for cachedKey, cached := range resolvedJDKCache.entries {
		if !now.Before(cached.expiresAt) {
			delete(resolvedJDKCache.entries, cachedKey)
		}
	}
	if cached, ok := resolvedJDKCache.entries[key]; ok && now.Before(cached.expiresAt) {
		resolvedJDKCache.Unlock()
		return cached.resolved, nil
	}
	resolvedJDKCache.Unlock()

	resolved, err := javarunner.ResolveJDK(ctx, options)
	if err != nil {
		return javarunner.ResolvedJDK{}, err
	}
	resolvedJDKCache.Lock()
	if resolvedJDKCache.entries == nil {
		resolvedJDKCache.entries = make(map[string]cachedResolvedJDK)
	}
	if len(resolvedJDKCache.entries) >= maxResolvedJDKCacheEntries {
		for cachedKey := range resolvedJDKCache.entries {
			delete(resolvedJDKCache.entries, cachedKey)
			break
		}
	}
	resolvedJDKCache.entries[key] = cachedResolvedJDK{resolved: resolved, expiresAt: now.Add(30 * time.Second)}
	resolvedJDKCache.Unlock()
	return resolved, nil
}

func stringValue(cfg map[string]any, key string) string {
	if value, ok := cfg[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func executionContextValue(cfg map[string]any, key string) string {
	value, ok := cfg[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func boolValue(cfg map[string]any, key string) bool {
	if value, ok := cfg[key].(bool); ok {
		return value
	}
	if value, ok := cfg[key].(string); ok {
		parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
		return parsed
	}
	return false
}

func intValue(cfg map[string]any, key string, fallback int) int {
	switch value := cfg[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func durationValue(cfg map[string]any, key string, fallback time.Duration) time.Duration {
	value := intValue(cfg, key, 0)
	if value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func exactStringSliceValue(cfg map[string]any, key string) ([]string, error) {
	raw, present := cfg[key]
	if !present || raw == nil {
		return nil, nil
	}
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...), nil
	case []any:
		out := make([]string, len(value))
		for index, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("java_vuser %s token %d must be a string", key, index)
			}
			out[index] = text
		}
		return out, nil
	default:
		return nil, fmt.Errorf("java_vuser %s must be an array of exact string tokens", key)
	}
}

// exactClasspathAssetsValue reads user-supplied classpath JAR bindings from the
// flattened protocol config. It tolerates both the in-process typed slice and a
// JSON round-trip ([]any of map[string]any).
func exactClasspathAssetsValue(cfg map[string]any, key string) ([]dsl.JavaClasspathAsset, error) {
	raw, present := cfg[key]
	if !present || raw == nil {
		return nil, nil
	}
	var items []any
	switch value := raw.(type) {
	case []dsl.JavaClasspathAsset:
		if len(value) > javarunner.MaxClasspathEntries {
			return nil, fmt.Errorf("java_vuser %s exceeds %d entries", key, javarunner.MaxClasspathEntries)
		}
		return append([]dsl.JavaClasspathAsset(nil), value...), nil
	case []any:
		items = value
	default:
		return nil, fmt.Errorf("java_vuser %s must be an array of classpath asset objects", key)
	}
	if len(items) > javarunner.MaxClasspathEntries {
		return nil, fmt.Errorf("java_vuser %s exceeds %d entries", key, javarunner.MaxClasspathEntries)
	}
	out := make([]dsl.JavaClasspathAsset, 0, len(items))
	for index, item := range items {
		asset, err := classpathAssetFromAny(item)
		if err != nil {
			return nil, fmt.Errorf("java_vuser %s entry %d: %w", key, index, err)
		}
		out = append(out, asset)
	}
	return out, nil
}

func classpathAssetFromAny(item any) (dsl.JavaClasspathAsset, error) {
	switch typed := item.(type) {
	case dsl.JavaClasspathAsset:
		return typed, nil
	case map[string]any:
		return dsl.JavaClasspathAsset{
			Entry:       stringFromAny(typed["entry"]),
			FileName:    stringFromAny(typed["file_name"]),
			SHA256:      stringFromAny(typed["sha256"]),
			SizeBytes:   int64FromAny(typed["size_bytes"]),
			ContentType: stringFromAny(typed["content_type"]),
			StorageRef:  stringFromAny(typed["storage_ref"]),
			StagedAsset: stringFromAny(typed["staged_asset"]),
		}, nil
	default:
		return dsl.JavaClasspathAsset{}, fmt.Errorf("classpath asset must be an object")
	}
}

func stringFromAny(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
	}
	return 0
}

func hasJavaVuserRuntimeOverrides(cfg map[string]any) bool {
	for _, key := range []string{
		"java_vuser_jdk_selection",
		"java_vuser_jdk_home",
		"java_vuser_additional_vm_parameters",
		"java_vuser_classpath",
		"java_vuser_use_xbootclasspath",
		"java_vuser_dedicated_class_loader",
	} {
		if _, present := cfg[key]; present {
			return true
		}
	}
	return false
}

func stringParam(params map[string]any, key string) string {
	if value, ok := params[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func intParam(params map[string]any, key string, fallback int) int {
	switch value := params[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed
		}
	}
	return fallback
}
