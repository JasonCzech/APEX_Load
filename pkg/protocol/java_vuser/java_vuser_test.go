package java_vuser

import (
	"archive/zip"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func enableBestEffortPersistentProcessForTest(runner *JavaVuser) {
	runner.supportsStrongProcessContainment = func() bool { return true }
	runner.startStrongProcessTree = javarunner.StartProcessTree
}

func TestJavaVuserSessionFrameTokenHelper(t *testing.T) {
	payload := os.Getenv("APEXLOAD_TEST_JAVA_SESSION_PAYLOAD")
	if payload == "" {
		return
	}
	var command runCommand
	if err := json.Unmarshal([]byte(payload), &command); err != nil {
		os.Exit(2)
	}
	decoded, err := hex.DecodeString(command.SessionSecret)
	if err != nil || len(decoded) != 32 {
		os.Exit(3)
	}
	var secret [32]byte
	copy(secret[:], decoded)
	_, _ = fmt.Fprint(os.Stdout, sessionFrameToken(secret, command.RequestID))
	os.Exit(0)
}

func shellQuoteJavaVuserTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func TestPersistentRunnerStartMapsStrongContainmentErrors(t *testing.T) {
	runner := &JavaVuser{
		startStrongProcessTree: func(*exec.Cmd) error {
			return fmt.Errorf("platform-specific detail: %w", javarunner.ErrStrongProcessContainmentUnavailable)
		},
	}
	command := exec.Command("must-not-start")
	err := runner.startPersistentRunnerCommand(context.Background(), command)
	if err == nil || !errors.Is(err, javarunner.ErrStrongProcessContainmentUnavailable) ||
		!strings.Contains(err.Error(), "process_containment_unavailable") {
		t.Fatalf("startPersistentRunnerCommand() error = %v, want stable containment rejection", err)
	}
	if command.Process != nil {
		t.Fatal("strong-containment rejection started the target process")
	}

	terminal := &JavaVuser{
		startStrongProcessTree: func(*exec.Cmd) error {
			return errors.Join(
				javarunner.ErrStrongProcessContainmentUnavailable,
				javarunner.ErrProcessContainmentFailed,
				errors.New("platform detail must not be reflected"),
			)
		},
	}
	err = terminal.startPersistentRunnerCommand(context.Background(), exec.Command("must-not-start"))
	if !errors.Is(err, javarunner.ErrProcessContainmentFailed) ||
		strings.Contains(err.Error(), "platform detail") {
		t.Fatalf("terminal start mapping = %v, want value-free containment-failed classification", err)
	}
}

func TestPersistentExecutionFailsClosedBeforeProcessStartWithoutContainment(t *testing.T) {
	startCalled := false
	runner := &JavaVuser{
		cfg: configuration{Enabled: true},
		supportsStrongProcessContainment: func() bool {
			return false
		},
		startStrongProcessTree: func(*exec.Cmd) error {
			startCalled = true
			return nil
		},
	}
	result, err := runner.executePersistent(context.Background(), &protocol.Action{
		Name:   "Java action",
		Type:   "java_vuser_run",
		Target: "Actions",
		Params: map[string]any{"method": "action"},
	}, nil)
	if err != nil {
		t.Fatalf("executePersistent() error = %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "process_containment_unavailable") {
		t.Fatalf("executePersistent() = %#v, want value-free containment rejection", result)
	}
	if startCalled {
		t.Fatal("containment rejection reached the persistent process starter")
	}
}

func TestPersistentStartContainmentFailureQuarantinesLocalCapacity(t *testing.T) {
	resetProcessSlots()
	defer resetProcessSlots()
	t.Setenv(config.EnvJavaVuserMaxProcesses, "1")

	runner := &JavaVuser{
		cfg: configuration{
			Enabled:       true,
			RunnerCommand: "must-not-run",
			ActionTimeout: time.Second,
		},
		supportsStrongProcessContainment: func() bool { return true },
		startStrongProcessTree: func(*exec.Cmd) error {
			return errors.Join(
				javarunner.ErrProcessContainmentFailed,
				errors.New("started process cleanup was not proven"),
			)
		},
	}
	session, _, err := runner.startPersistentSession(context.Background(), runCommand{
		RequestID:  "start-containment-failure",
		ActionType: "java_vuser_run",
		ClassName:  "Actions",
		Method:     "action",
	})
	if session != nil || !errors.Is(err, javarunner.ErrProcessContainmentFailed) {
		t.Fatalf("startPersistentSession() = %#v, %v, want terminal containment failure", session, err)
	}
	if !localContainmentFailed.Load() || len(processSlotChan()) != 1 {
		t.Fatalf(
			"failed start did not quarantine local capacity: terminal=%v slots=%d",
			localContainmentFailed.Load(),
			len(processSlotChan()),
		)
	}
	if _, acquireErr := acquireProcessSlot(context.Background()); acquireErr == nil ||
		!strings.Contains(acquireErr.Error(), "process_containment_unavailable") {
		t.Fatalf("future capacity admission = %v, want terminal rejection", acquireErr)
	}
}

func TestJDKProbeContainmentFailureTripsLocalAdmissionGate(t *testing.T) {
	resetProcessSlots()
	defer resetProcessSlots()

	cfg := configuration{
		Enabled:               true,
		RunnerCommand:         "java",
		RunnerCommandExplicit: true,
		MaxProcesses:          1,
	}
	_, err := prepareLocalConfigurationWithResolver(
		context.Background(),
		cfg,
		func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
			return javarunner.ResolvedJDK{}, errors.Join(
				javarunner.ErrProcessContainmentFailed,
				errors.New("probe cleanup detail"),
			)
		},
	)
	if !errors.Is(err, javarunner.ErrProcessContainmentFailed) ||
		!strings.Contains(err.Error(), "process_containment_unavailable") {
		t.Fatalf("prepareLocalConfigurationWithResolver() error = %v, want stable containment rejection", err)
	}
	if !localContainmentFailed.Load() {
		t.Fatal("JDK probe containment failure did not close local process admission")
	}
	if len(localHarnessProbeSlot) != 1 {
		t.Fatalf("JDK containment failure released local probe capacity: slots=%d", len(localHarnessProbeSlot))
	}
	if capabilities := (&JavaVuser{cfg: cfg}).RuntimeCapabilities(); capabilities.Available || capabilities.MaxVUsers != 0 {
		t.Fatalf("post-failure runtime capabilities = %#v, want unavailable", capabilities)
	}
	if _, acquireErr := acquireProcessSlot(context.Background()); acquireErr == nil ||
		!strings.Contains(acquireErr.Error(), "process_containment_unavailable") {
		t.Fatalf("post-failure process admission = %v, want terminal rejection", acquireErr)
	}
}

func TestStrongContainmentProbeFailureQuarantinesLocalProbeCapacity(t *testing.T) {
	resetProcessSlots()
	defer resetProcessSlots()

	release, err := acquireLocalRuntimeProbe(context.Background())
	if err != nil {
		t.Fatalf("acquireLocalRuntimeProbe() = %v", err)
	}
	if finishLocalStrongContainmentProbe(release, true, true) {
		t.Fatal("failed strong-containment cleanup was reported available")
	}
	if !localContainmentFailed.Load() || len(localHarnessProbeSlot) != 1 {
		t.Fatalf(
			"failed strong probe did not quarantine local probe capacity: terminal=%v slots=%d",
			localContainmentFailed.Load(),
			len(localHarnessProbeSlot),
		)
	}
	if nextRelease, nextErr := acquireLocalRuntimeProbe(context.Background()); nextRelease != nil ||
		nextErr == nil || !strings.Contains(nextErr.Error(), "process_containment_unavailable") {
		t.Fatalf(
			"post-failure probe admission = release:%v error:%v, want terminal rejection",
			nextRelease != nil,
			nextErr,
		)
	}
}

type fakeJavaVuserSidecarClient struct {
	healthResponse       javasidecar.HealthResponse
	healthErr            error
	capabilitiesResponse javasidecar.CapabilitiesResponse
	capabilitiesErr      error
	validateResponse     javasidecar.ValidateResponse
	validateErr          error
	claimResponse        javasidecar.ClaimOwnerResponse
	claimErr             error
	executeResponse      javasidecar.ExecutionResponse
	executeErr           error
	validateRequest      javasidecar.ValidateRequest
	executeRequest       javasidecar.ExecutionRequest
	executeRequests      []javasidecar.ExecutionRequest
	closeResponse        javasidecar.CloseSessionResponse
	closeErr             error
	closeSessionID       string
	healthCalls          int
	capabilityCalls      int
	validateCalls        int
	claimCalls           int
	executeCalls         int
	closeCalls           int
}

func (f *fakeJavaVuserSidecarClient) Health(context.Context) (javasidecar.HealthResponse, error) {
	f.healthCalls++
	return f.healthResponse, f.healthErr
}

func (f *fakeJavaVuserSidecarClient) Capabilities(context.Context) (javasidecar.CapabilitiesResponse, error) {
	f.capabilityCalls++
	return f.capabilitiesResponse, f.capabilitiesErr
}

func (f *fakeJavaVuserSidecarClient) Validate(_ context.Context, request javasidecar.ValidateRequest) (javasidecar.ValidateResponse, error) {
	f.validateCalls++
	f.validateRequest = request
	return f.validateResponse, f.validateErr
}

func (f *fakeJavaVuserSidecarClient) ClaimOwner(_ context.Context, ownerID string) (javasidecar.ClaimOwnerResponse, error) {
	f.claimCalls++
	response := f.claimResponse
	if response.OwnerID == "" {
		response.OwnerID = ownerID
	}
	if response.Epoch == "" {
		response.Epoch = "test-owner-epoch"
	}
	response.Claimed = f.claimErr == nil
	return response, f.claimErr
}

func (f *fakeJavaVuserSidecarClient) Execute(_ context.Context, request javasidecar.ExecutionRequest) (javasidecar.ExecutionResponse, error) {
	f.executeCalls++
	f.executeRequest = request
	f.executeRequests = append(f.executeRequests, request)
	response := f.executeResponse
	if response.ExecutionID == "" {
		response.ExecutionID = request.ExecutionID
	}
	response.Stdout = strings.ReplaceAll(response.Stdout, "$EXECUTION_ID", request.ExecutionID)
	response.Stderr = strings.ReplaceAll(response.Stderr, "$EXECUTION_ID", request.ExecutionID)
	response.Events = strings.ReplaceAll(response.Events, "$EXECUTION_ID", request.ExecutionID)
	return response, f.executeErr
}

func (f *fakeJavaVuserSidecarClient) CloseSession(_ context.Context, sessionID string) (javasidecar.CloseSessionResponse, error) {
	f.closeCalls++
	f.closeSessionID = sessionID
	response := f.closeResponse
	if response.SessionID == "" {
		response.SessionID = sessionID
	}
	return response, f.closeErr
}

func TestJavaVuserRejectsUnboundedTimeouts(t *testing.T) {
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": dsl.MaxConfiguredProtocolTimeout.Milliseconds() + 1,
	}); err == nil {
		t.Fatal("Init accepted timeout above the runtime cap")
	}
	runner.cfg.ActionTimeout = defaultActionTimeout
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name: "bad timeout", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"timeout_ms": dsl.MaxConfiguredProtocolTimeout.Milliseconds() + 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "between 0") {
		t.Fatalf("unbounded timeout result = %#v", result)
	}
}

func TestJavaVuserFailsClosedUnderCITargetPolicy(t *testing.T) {
	var runner JavaVuser
	err := runner.Init(context.Background(), map[string]any{
		protocol.TargetPolicyConfigKey: []string{"java.example.test"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot enforce the CI runtime target policy") {
		t.Fatalf("Init policy error = %v, want fail-closed sidecar rejection", err)
	}
}

// resetProcessSlots clears the lazily-initialized, generator-wide process
// limiter so a test can observe its sizing from a fresh operator environment.
func resetProcessSlots() {
	processSlotsOnce = sync.Once{}
	processSlots = nil
	select {
	case <-localHarnessProbeSlot:
	default:
	}
	localContainmentFailed.Store(false)
	localContainmentSignal.Lock()
	localContainmentSignal.done = make(chan struct{})
	localContainmentSignal.closed = false
	localContainmentSignal.Unlock()
	resetLocalHarnessCapabilitiesCache()
	resetSidecarRuntimeCaches()
}

func resetSidecarRuntimeCaches() {
	sidecarCapabilitiesCache.Lock()
	sidecarCapabilitiesCache.entries = nil
	sidecarCapabilitiesCache.Unlock()
	healthCache.Lock()
	healthCache.key = ""
	healthCache.checkedAt = time.Time{}
	healthCache.healthy = false
	healthCache.Unlock()
}

func resetLocalHarnessCapabilitiesCache() {
	localHarnessCapabilitiesCache.Lock()
	localHarnessCapabilitiesCache.entries = nil
	localHarnessCapabilitiesCache.Unlock()
}

func TestFailedContainmentQuarantinesLocalProcessCapacity(t *testing.T) {
	resetProcessSlots()
	defer resetProcessSlots()
	t.Setenv(config.EnvJavaVuserMaxProcesses, "1")

	release, err := acquireProcessSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire initial process slot: %v", err)
	}
	blockedAcquire := make(chan error, 1)
	go func() {
		_, err := acquireProcessSlot(context.Background())
		blockedAcquire <- err
	}()
	select {
	case err := <-blockedAcquire:
		t.Fatalf("capacity waiter returned before containment failed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	finishLocalProcessCapacity(release, errors.New("descendant tree remained populated"))
	select {
	case err := <-blockedAcquire:
		if err == nil || !strings.Contains(err.Error(), "process_containment_unavailable") {
			t.Fatalf("blocked acquire error = %v, want process_containment_unavailable", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked capacity waiter did not observe terminal containment failure")
	}

	slots := processSlotChan()
	if occupied := len(slots); occupied != 1 {
		t.Fatalf("failed containment left %d occupied slots, want quarantined capacity 1", occupied)
	}
	secondCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	if _, err := acquireProcessSlot(secondCtx); err == nil ||
		!strings.Contains(err.Error(), "process_containment_unavailable") {
		t.Fatalf("second acquire error = %v, want fail-closed containment error", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("failed-containment acquire waited %s instead of failing closed", elapsed)
	}

	runner := &JavaVuser{cfg: configuration{
		Enabled: true, RunnerMode: config.JavaVuserRunnerModeLocal,
	}}
	if capabilities := runner.RuntimeCapabilities(); capabilities.Available || capabilities.MaxVUsers != 0 {
		t.Fatalf("capabilities after containment failure = %#v, want unavailable", capabilities)
	}
	if runner.RuntimeHealthy(context.Background()) {
		t.Fatal("runtime remained healthy after whole-tree containment failure")
	}
	if runner.LegacyRuntimeAvailable() {
		t.Fatal("legacy availability remained true after whole-tree containment failure")
	}
}

func TestPersistentKillFailureTripsLocalContainmentGateBeforeReap(t *testing.T) {
	resetProcessSlots()
	defer resetProcessSlots()
	t.Setenv(config.EnvJavaVuserMaxProcesses, "1")

	release, err := acquireProcessSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire initial process slot: %v", err)
	}
	waitDone := make(chan struct{})
	session := &runnerSession{
		waitDone: waitDone,
		release:  release,
		killFunc: func(*exec.Cmd) error {
			return errors.New("strong process-tree kill failed")
		},
	}
	abortDone := make(chan struct{})
	go func() {
		session.abort(5*time.Millisecond, "request")
		close(abortDone)
	}()
	select {
	case <-abortDone:
	case <-time.After(time.Second):
		t.Fatal("persistent abort waited indefinitely for an unreaped JVM after kill failure")
	}
	if !localContainmentFailed.Load() {
		t.Fatal("kill failure did not immediately mark the local runtime unavailable")
	}
	if occupied := len(processSlotChan()); occupied != 1 {
		t.Fatalf("kill failure left %d occupied slots, want one quarantined token", occupied)
	}
	blockedCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := acquireProcessSlot(blockedCtx); err == nil ||
		!strings.Contains(err.Error(), "process_containment_unavailable") {
		t.Fatalf("post-failure acquire = %v, want terminal containment rejection", err)
	}
	close(waitDone)
}

func TestTeardownDoesNotSuppressContainmentFailureAfterSessionError(t *testing.T) {
	waitDone := make(chan struct{})
	close(waitDone)
	runner := &JavaVuser{
		cfg: configuration{ActionTimeout: time.Second},
		session: &runnerSession{
			waitDone: waitDone,
			waitErr:  errors.New("earlier process exit"),
			killErr: errors.Join(
				javarunner.ErrProcessContainmentFailed,
				errors.New("whole-tree cleanup failed"),
			),
		},
		sessionErr: errors.New("earlier action failure"),
	}
	err := runner.Teardown(context.Background())
	if !errors.Is(err, javarunner.ErrProcessContainmentFailed) ||
		!strings.Contains(err.Error(), "process_containment_unavailable") {
		t.Fatalf("Teardown() = %v, want containment failure despite prior session error", err)
	}
}

func TestTeardownStillSuppressesDuplicateExitAfterSessionError(t *testing.T) {
	waitDone := make(chan struct{})
	close(waitDone)
	runner := &JavaVuser{
		cfg: configuration{ActionTimeout: time.Second},
		session: &runnerSession{
			waitDone: waitDone,
			waitErr:  errors.New("same failed process exit"),
		},
		sessionErr: errors.New("earlier action failure"),
	}
	if err := runner.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown() duplicated an already-reported ordinary session error: %v", err)
	}
}

func TestJavaVuserRunnerIsDisabledByDefault(t *testing.T) {
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{Name: "Run", Type: "java_vuser_run", Target: "Actions"})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success {
		t.Fatal("result.Success = true, want disabled runner failure")
	}
	if result.StatusCode != 500 || result.ErrorMessage == "" {
		t.Fatalf("result = %#v, want status 500 with error message", result)
	}
}

func TestJavaVuserSidecarInitValidatesLoopbackAndDefersJDKResolution(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, "http://runner.example.test:8098")
	var rejected JavaVuser
	if err := rejected.Init(context.Background(), map[string]any{}); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("Init(remote sidecar URL) error = %v, want loopback rejection", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerURL, "http://127.0.0.1:19001")
	runner := JavaVuser{sidecar: &fakeJavaVuserSidecarClient{}}
	err := runner.Init(context.Background(), map[string]any{
		"java_vuser_jdk_selection": string("specified"),
		"java_vuser_jdk_home":      "/path/that/exists/only/in/the/sidecar",
	})
	if err != nil {
		t.Fatalf("Init(sidecar) resolved the remote JDK locally: %v", err)
	}
	if runner.cfg.RunnerMode != config.JavaVuserRunnerModeSidecar || runner.cfg.ResolvedJDK != nil || runner.sidecar == nil {
		t.Fatalf("sidecar configuration = %#v / client=%T", runner.cfg, runner.sidecar)
	}
}

func TestJavaVuserSidecarExecuteUsesTypedRemoteContractWithoutLocalCredentialGuard(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, "http://127.0.0.1:19002")
	t.Setenv(config.EnvJavaVuserClasspath, "/operator/shared.jar")
	// A local subprocess must reject this credential. Sidecar mode must not call
	// that local guard because the JVM is forked in the credential-minimized
	// sibling container instead.
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "generator-secret")

	fake := &fakeJavaVuserSidecarClient{executeResponse: javasidecar.ExecutionResponse{
		Success:         true,
		Stdout:          "{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":17}\n",
		Stderr:          "diagnostic\n",
		Events:          "{\"type\":\"transaction_end\",\"name\":\"Checkout\",\"message\":\"PASS\"}\n",
		DurationMS:      23,
		StdoutTruncated: true,
		WarningCodes:    []string{javasidecar.WarningModernXBootClasspathFallback},
	}}
	runner := JavaVuser{sidecar: fake}
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_jdk_selection":            "specified",
		"java_vuser_jdk_home":                 "/runner/jdk-21",
		"java_vuser_additional_vm_parameters": []any{"-Dlabel=hello world"},
		"java_vuser_classpath":                []any{"/assets/app.jar"},
		"java_vuser_use_xbootclasspath":       true,
		"java_vuser_dedicated_class_loader":   true,
		// Enforced engine-side (the automatic lr_parameters snapshot is
		// suppressed before dispatch); the protocol must tolerate the key.
		"java_vuser_skip_parameter_evaluation": true,
		"java_vuser_startup_timeout_ms":        float64(2500),
		"java_vuser_action_timeout_ms":         float64(60000),
		"vuser_id":                             "vu-7",
		"group_name":                           "Checkout",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name: "Run", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "full_lifecycle", "source_archive_ref": "archive-1"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success || fake.executeCalls != 1 {
		t.Fatalf("result = %#v; execute calls = %d", result, fake.executeCalls)
	}
	request := fake.executeRequest
	if request.Runtime.JDKHome != "/runner/jdk-21" || len(request.Runtime.AdditionalVMParameters) != 1 || request.Runtime.AdditionalVMParameters[0] != "-Dlabel=hello world" {
		t.Fatalf("sidecar runtime = %#v", request.Runtime)
	}
	if got := request.Runtime.Classpath; len(got) != 1 || got[0] != "/assets/app.jar" {
		t.Fatalf("wire classpath = %#v, want script entries only", got)
	}
	if request.Command.VUserID != "vu-7" || request.Command.GroupName != "Checkout" || request.Command.SourceArchiveRef != "archive-1" {
		t.Fatalf("sidecar command = %#v", request.Command)
	}
	if result.Extractions["java_vuser_duration_ms"] != "23" || result.ProtocolEvidence["runner_backend"] != config.JavaVuserRunnerModeSidecar {
		t.Fatalf("result evidence/extractions = %#v / %#v", result.ProtocolEvidence, result.Extractions)
	}
	if result.ProtocolEvidence["runner_stdout_truncated"] != true {
		t.Fatalf("truncation evidence = %#v", result.ProtocolEvidence)
	}
	warnings, ok := result.ProtocolEvidence["runner_warning_codes"].([]string)
	if !ok || len(warnings) != 1 || warnings[0] != modernXBootWarning {
		t.Fatalf("warning evidence = %#v", result.ProtocolEvidence["runner_warning_codes"])
	}
}

func TestJavaVuserPersistentSidecarPreservesHarnessFailureDiagnostic(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, "http://127.0.0.1:19012")

	fake := &fakeJavaVuserSidecarClient{executeResponse: javasidecar.ExecutionResponse{
		Success:   false,
		ErrorCode: "harness_reported_failure",
		Error:     "lifecycle method action returned non-PASS status 1",
	}}
	runner := JavaVuser{sidecar: fake}
	if err := runner.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name: "Action phase", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "action"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "non-PASS status 1") {
		t.Fatalf("persistent sidecar failure result = %#v, want harness diagnostic", result)
	}
	if runner.sidecarSessionErr != nil || !runner.sidecarSessionLive {
		t.Fatalf(
			"ordinary lifecycle failure poisoned persistent sidecar state: live=%v err=%v",
			runner.sidecarSessionLive,
			runner.sidecarSessionErr,
		)
	}
}

func TestBoundedSidecarHarnessErrorLimitsPeerDiagnostic(t *testing.T) {
	value := strings.Repeat("x", javasidecar.MaxHarnessErrorBytes+1024)
	got := boundedSidecarHarnessError(value)
	if len(got) > javasidecar.MaxHarnessErrorBytes || !strings.HasSuffix(got, "...[truncated]") {
		t.Fatalf("boundedSidecarHarnessError length/suffix = %d/%q", len(got), got[len(got)-min(len(got), 32):])
	}
}

func TestJavaVuserSidecarPreflightUsesValueFreeValidationIssues(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, "http://127.0.0.1:19003")
	issueIndex := 0
	fake := &fakeJavaVuserSidecarClient{validateResponse: javasidecar.ValidateResponse{
		Valid:  false,
		Issues: []javasidecar.ValidationIssue{{Code: "target_validation_failed", Index: &issueIndex}},
	}}
	runner := JavaVuser{sidecar: fake}
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_additional_vm_parameters": []any{"-Dopaque=resolved-super-secret"},
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	_, err := runner.PreflightRuntime(context.Background(), protocol.RuntimePreflightRequest{
		Config: map[string]any{
			"java_vuser_additional_vm_parameters": []any{"-Dopaque=resolved-super-secret"},
		},
		Targets: []protocol.RuntimePreflightTarget{{ClassName: "ResolvedSecretClass", Method: "full_lifecycle"}},
	})
	if err == nil || !strings.Contains(err.Error(), "target_validation_failed at index 0") {
		t.Fatalf("PreflightRuntime error = %v", err)
	}
	for _, secret := range []string{"resolved-super-secret", "ResolvedSecretClass"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("PreflightRuntime leaked %q in %v", secret, err)
		}
	}
	if fake.validateCalls != 1 || len(fake.validateRequest.Targets) != 1 {
		t.Fatalf("validate request/calls = %#v / %d", fake.validateRequest, fake.validateCalls)
	}
}

func TestJavaVuserSidecarAdvertisesPersistentLifecycleCapacity(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, "http://127.0.0.1:19004")
	fake := &fakeJavaVuserSidecarClient{
		healthResponse: javasidecar.HealthResponse{Status: "healthy"},
		capabilitiesResponse: javasidecar.CapabilitiesResponse{
			Healthy:      true,
			JavaMajor:    21,
			MaxProcesses: 7,
			Features: []string{
				"java_vuser_validation_v1",
				protocol.FeatureJavaVuserPersistentLifecycleV1,
				javasidecar.FeatureSessionFrameTokenV1,
				javasidecar.FeatureOwnerFencingV1,
			},
		},
		executeResponse: javasidecar.ExecutionResponse{
			Success: true,
			Stdout:  "{\"success\":true,\"class_name\":\"Actions\",\"method\":\"action\",\"duration_ms\":1}\n",
		},
	}
	runner := JavaVuser{sidecar: fake}
	if err := runner.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	capabilities := runner.RuntimeCapabilities()
	if !capabilities.Available || capabilities.MaxVUsers != 7 {
		t.Fatalf("RuntimeCapabilities = %#v", capabilities)
	}
	if !protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) {
		t.Fatalf("sidecar did not advertise persistent lifecycle support: %#v", capabilities.Features)
	}
	if !runner.RuntimeHealthy(context.Background()) || !runner.LegacyRuntimeHealthy() {
		t.Fatal("healthy sidecar was not accepted by runtime health checks")
	}
	if fake.claimCalls != 1 || fake.capabilityCalls != 1 || fake.healthCalls == 0 {
		t.Fatalf("sidecar calls: claims=%d capabilities=%d health=%d", fake.claimCalls, fake.capabilityCalls, fake.healthCalls)
	}

	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name: "Action phase", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "action"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || fake.executeCalls != 1 || fake.executeRequest.Command.Method != "action" ||
		fake.executeRequest.ExecutionID == "" || fake.executeRequest.RequireExistingSession {
		t.Fatalf("persistent phase result/request/calls = %#v / %#v / %d", result, fake.executeRequest, fake.executeCalls)
	}
	if err := runner.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown = %v", err)
	}
}

func TestSidecarTeardownDoesNotSuppressContainmentFailureAfterSessionError(t *testing.T) {
	fake := &fakeJavaVuserSidecarClient{
		closeErr: &javasidecar.RemoteError{
			StatusCode: 500,
			Code:       "process_containment_unavailable",
		},
	}
	runner := &JavaVuser{
		cfg:                configuration{ActionTimeout: time.Second},
		sidecar:            fake,
		sidecarSessionID:   "session-containment-failed",
		sidecarSessionErr:  errors.New("earlier sidecar action failure"),
		sidecarSessionLive: true,
	}
	err := runner.Teardown(context.Background())
	if err == nil || !strings.Contains(err.Error(), "process_containment_unavailable") {
		t.Fatalf("Teardown() = %v, want containment failure despite prior sidecar session error", err)
	}
}

func TestJavaVuserSidecarKeepsLegacyAvailabilityWithoutPersistentFrameToken(t *testing.T) {
	resetSidecarRuntimeCaches()
	t.Cleanup(resetSidecarRuntimeCaches)

	const runnerURL = "http://127.0.0.1:19108"
	fake := &fakeJavaVuserSidecarClient{
		capabilitiesResponse: javasidecar.CapabilitiesResponse{
			Healthy:      true,
			JavaMajor:    21,
			MaxProcesses: 7,
			Features: []string{
				javasidecar.FeatureValidationOnly,
				javasidecar.FeatureOwnerFencingV1,
			},
		},
	}
	runner := JavaVuser{
		cfg: configuration{
			Enabled:       true,
			RunnerMode:    config.JavaVuserRunnerModeSidecar,
			RunnerURL:     runnerURL,
			RunnerCommand: defaultRunnerCommand,
		},
		sidecar: fake,
	}
	capabilities := runner.RuntimeCapabilities()
	if !capabilities.Available || capabilities.MaxVUsers != 7 {
		t.Fatalf("legacy-only sidecar capabilities = %#v, want available one-shot runtime", capabilities)
	}
	if protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPhaseLifecycleV1) {
		t.Fatalf("legacy-only sidecar claimed persistent/phase lifecycle: %#v", capabilities.Features)
	}
	if !runner.LegacyRuntimeAvailable() {
		t.Fatal("legacy-only sidecar was unavailable to full_lifecycle admission")
	}
}

func TestJavaVuserSidecarCapabilitiesCacheIsScopedByAuthCredential(t *testing.T) {
	resetSidecarRuntimeCaches()
	t.Cleanup(resetSidecarRuntimeCaches)

	const (
		runnerURL    = "http://127.0.0.1:19101"
		validToken   = "valid-runner-token"
		invalidToken = "invalid-runner-token"
	)
	healthy := &fakeJavaVuserSidecarClient{
		capabilitiesResponse: javasidecar.CapabilitiesResponse{
			Healthy:      true,
			JavaMajor:    21,
			MaxProcesses: 7,
			Features: []string{
				protocol.FeatureJavaVuserPersistentLifecycleV1,
				javasidecar.FeatureSessionFrameTokenV1,
				javasidecar.FeatureOwnerFencingV1,
			},
		},
	}
	valid := JavaVuser{
		cfg: configuration{
			Enabled:       true,
			RunnerMode:    config.JavaVuserRunnerModeSidecar,
			RunnerURL:     runnerURL,
			RunnerCommand: defaultRunnerCommand,
			AuthToken:     validToken,
		},
		sidecar: healthy,
	}
	if capabilities := valid.RuntimeCapabilities(); !capabilities.Available || capabilities.MaxVUsers != 7 {
		t.Fatalf("valid-token capabilities = %#v, want available capacity 7", capabilities)
	}

	unauthorized := &fakeJavaVuserSidecarClient{capabilitiesErr: errors.New("runner unauthorized")}
	invalid := JavaVuser{
		cfg: configuration{
			Enabled:       true,
			RunnerMode:    config.JavaVuserRunnerModeSidecar,
			RunnerURL:     runnerURL,
			RunnerCommand: defaultRunnerCommand,
			AuthToken:     invalidToken,
		},
		sidecar: unauthorized,
	}
	if capabilities := invalid.RuntimeCapabilities(); capabilities.Available || capabilities.MaxVUsers != 0 {
		t.Fatalf("invalid-token capabilities = %#v, want unavailable", capabilities)
	}
	if healthy.capabilityCalls != 1 || unauthorized.capabilityCalls != 1 {
		t.Fatalf("capability probes valid/invalid = %d/%d, want 1/1", healthy.capabilityCalls, unauthorized.capabilityCalls)
	}
	for _, token := range []string{validToken, invalidToken} {
		if strings.Contains(sidecarCredentialCacheKey(runnerURL, token), token) {
			t.Fatalf("credential cache key retained raw token %q", token)
		}
	}
}

func TestJavaVuserEffectiveConfigurationScopesCapabilitiesByOperatorSidecarCredential(t *testing.T) {
	resetSidecarRuntimeCaches()
	t.Cleanup(resetSidecarRuntimeCaches)

	const (
		runnerURL    = "http://127.0.0.1:19110"
		validToken   = "operator-sidecar-token"
		invalidToken = "rotated-sidecar-token"
	)
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, runnerURL)
	t.Setenv(config.EnvJavaVuserRunnerAuthToken, validToken)

	healthy := &fakeJavaVuserSidecarClient{
		capabilitiesResponse: javasidecar.CapabilitiesResponse{
			Healthy: true, JavaMajor: 21, MaxProcesses: 7,
			Features: []string{
				protocol.FeatureJavaVuserPersistentLifecycleV1,
				javasidecar.FeatureSessionFrameTokenV1,
				javasidecar.FeatureOwnerFencingV1,
			},
		},
	}
	valid := JavaVuser{sidecar: healthy}
	if capabilities := valid.RuntimeCapabilities(); !capabilities.Available || capabilities.MaxVUsers != 7 {
		t.Fatalf("operator-token capabilities = %#v, want available capacity 7", capabilities)
	}
	if cfg := valid.effectiveConfiguration(); cfg.AuthToken != validToken {
		t.Fatalf("effective sidecar auth token = %q, want operator credential", cfg.AuthToken)
	}

	t.Setenv(config.EnvJavaVuserRunnerAuthToken, invalidToken)
	unauthorized := &fakeJavaVuserSidecarClient{capabilitiesErr: errors.New("runner unauthorized")}
	invalid := JavaVuser{sidecar: unauthorized}
	if capabilities := invalid.RuntimeCapabilities(); capabilities.Available || capabilities.MaxVUsers != 0 {
		t.Fatalf("rotated-token capabilities = %#v, want unavailable", capabilities)
	}
	if healthy.capabilityCalls != 1 || unauthorized.capabilityCalls != 1 {
		t.Fatalf("capability probes before/after credential rotation = %d/%d, want 1/1", healthy.capabilityCalls, unauthorized.capabilityCalls)
	}
}

func TestJavaVuserSidecarPreflightPublishesFreshCapacityForAdmission(t *testing.T) {
	resetSidecarRuntimeCaches()
	t.Cleanup(resetSidecarRuntimeCaches)

	const runnerURL = "http://127.0.0.1:19109"
	features := []string{
		protocol.FeatureJavaVuserPersistentLifecycleV1,
		javasidecar.FeatureSessionFrameTokenV1,
		javasidecar.FeatureOwnerFencingV1,
	}
	stale := &fakeJavaVuserSidecarClient{
		capabilitiesResponse: javasidecar.CapabilitiesResponse{
			Healthy: true, JavaMajor: 21, MaxProcesses: 7, Features: features,
		},
	}
	runner := JavaVuser{
		cfg: configuration{
			Enabled:       true,
			RunnerMode:    config.JavaVuserRunnerModeSidecar,
			RunnerURL:     runnerURL,
			RunnerCommand: defaultRunnerCommand,
		},
		sidecar: stale,
	}
	if capabilities := runner.RuntimeCapabilities(); capabilities.MaxVUsers != 7 {
		t.Fatalf("initial cached capacity = %#v, want 7", capabilities)
	}

	fresh := &fakeJavaVuserSidecarClient{
		capabilitiesResponse: javasidecar.CapabilitiesResponse{
			Healthy: true, JavaMajor: 21, MaxProcesses: 3, Features: features,
		},
		validateResponse: javasidecar.ValidateResponse{Valid: true},
	}
	runner.sidecar = fresh
	if _, err := runner.preflightSidecar(context.Background(), []protocol.RuntimePreflightTarget{{
		ClassName: "Actions",
		Method:    "action",
	}}); err != nil {
		t.Fatalf("preflightSidecar: %v", err)
	}
	if capabilities := runner.RuntimeCapabilities(); capabilities.MaxVUsers != 3 {
		t.Fatalf("post-preflight cached capacity = %#v, want fresh limit 3", capabilities)
	}
	if stale.capabilityCalls != 1 || fresh.capabilityCalls != 1 {
		t.Fatalf("capability probes stale/fresh = %d/%d, want 1/1", stale.capabilityCalls, fresh.capabilityCalls)
	}
}

func TestJavaVuserSidecarHealthCacheIsScopedByAuthCredential(t *testing.T) {
	resetSidecarRuntimeCaches()
	t.Cleanup(resetSidecarRuntimeCaches)

	const (
		runnerURL    = "http://127.0.0.1:19102"
		validToken   = "valid-health-token"
		invalidToken = "invalid-health-token"
	)
	healthy := &fakeJavaVuserSidecarClient{
		healthResponse: javasidecar.HealthResponse{Status: "healthy"},
	}
	valid := JavaVuser{
		cfg: configuration{
			Enabled:       true,
			RunnerMode:    config.JavaVuserRunnerModeSidecar,
			RunnerURL:     runnerURL,
			RunnerCommand: defaultRunnerCommand,
			AuthToken:     validToken,
		},
		sidecar: healthy,
	}
	if !valid.RuntimeHealthy(context.Background()) {
		t.Fatal("valid-token runtime health = false, want true")
	}

	unauthorized := &fakeJavaVuserSidecarClient{healthErr: errors.New("runner unauthorized")}
	invalid := JavaVuser{
		cfg: configuration{
			Enabled:       true,
			RunnerMode:    config.JavaVuserRunnerModeSidecar,
			RunnerURL:     runnerURL,
			RunnerCommand: defaultRunnerCommand,
			AuthToken:     invalidToken,
		},
		sidecar: unauthorized,
	}
	if invalid.RuntimeHealthy(context.Background()) {
		t.Fatal("invalid-token runtime health = true, want false")
	}
	if healthy.healthCalls != 1 || unauthorized.healthCalls != 1 {
		t.Fatalf("health probes valid/invalid = %d/%d, want 1/1", healthy.healthCalls, unauthorized.healthCalls)
	}
}

func TestJavaVuserSidecarPersistentEventsRequireV2RequestBinding(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, "http://127.0.0.1:19005")
	fake := &fakeJavaVuserSidecarClient{}
	runner := JavaVuser{sidecar: fake}
	if err := runner.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	validEvents := strings.Join([]string{
		`{"type":"transaction_unmatched","name":"requestless-forgery","message":"1"}`,
		`{"contract":"` + javaVuserEventV2 + `","request_id":"other","type":"transaction_unmatched","name":"other-request","message":"1"}`,
		`{"contract":"` + javaVuserEventV2 + `","request_id":"$EXECUTION_ID","timestamp_ms":1000,"sequence":1,"transaction_id":"tx-1","type":"transaction_start","name":"Checkout"}`,
		`{"contract":"` + javaVuserEventV2 + `","request_id":"$EXECUTION_ID","timestamp_ms":1025,"sequence":2,"transaction_id":"tx-1","type":"transaction_end","name":"Checkout","message":"0"}`,
	}, "\n")
	fake.executeResponse = javasidecar.ExecutionResponse{
		Success: true,
		Events:  validEvents,
	}
	var children []*protocol.Result
	result, err := runner.ExecuteStream(context.Background(), &protocol.Action{
		Name: "Action phase", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "action"},
	}, func(child *protocol.Result) error {
		children = append(children, child)
		return nil
	})
	if err != nil || !result.Success {
		t.Fatalf("persistent sidecar result = %#v, %v", result, err)
	}
	if len(children) != 1 || !children[0].Success || children[0].ActionName != "Checkout" ||
		children[0].Duration != 25*time.Millisecond {
		t.Fatalf("strictly bound transaction children = %#v", children)
	}

	fake.executeResponse = javasidecar.ExecutionResponse{
		Success:         true,
		Events:          validEvents,
		EventsTruncated: true,
	}
	var truncatedChildren []*protocol.Result
	truncated, err := runner.ExecuteStream(context.Background(), &protocol.Action{
		Name: "Truncated phase", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "action"},
	}, func(child *protocol.Result) error {
		truncatedChildren = append(truncatedChildren, child)
		return nil
	})
	if err != nil || !truncated.Success || truncated.ErrorMessage != "" ||
		truncated.ProtocolEvidence["runner_event_stream_truncated"] != true {
		t.Fatalf("truncated persistent sidecar result = %#v, %v", truncated, err)
	}
	if len(truncatedChildren) != 2 || !truncatedChildren[0].Success ||
		truncatedChildren[1].ActionName != "Java transaction event stream" ||
		truncatedChildren[1].Success {
		t.Fatalf("truncated children = %#v, want paired transaction plus one synthetic failed child", truncatedChildren)
	}
	if len(fake.executeRequests) != 2 ||
		fake.executeRequests[0].RequireExistingSession ||
		!fake.executeRequests[1].RequireExistingSession {
		t.Fatalf("persistent RequireExisting sequence = %#v", fake.executeRequests)
	}
}

func TestJavaVuserPersistentSidecarPoisonsOnlyUnsafeOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		executeErr error
		errorCode  string
		wantRetry  bool
	}{
		{name: "ambiguous first transport failure", executeErr: errors.New("response lost")},
		{name: "first invocation timeout", errorCode: "execution_timeout"},
		{name: "first invocation cancellation", errorCode: "execution_canceled"},
		{name: "unknown future execution outcome", errorCode: "future_after_invoke"},
		{
			name:       "authoritative session configuration rejection",
			executeErr: &javasidecar.RemoteError{StatusCode: 409, Code: "session_configuration_mismatch"},
		},
		{name: "capacity expired before JVM start", errorCode: "session_not_started", wantRetry: true},
	}
	action := &protocol.Action{
		Name: "Action phase", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "action"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeJavaVuserSidecarClient{
				executeErr: test.executeErr,
				executeResponse: javasidecar.ExecutionResponse{
					ErrorCode: test.errorCode,
				},
			}
			runner := &JavaVuser{
				cfg: configuration{
					Enabled: true, RunnerMode: config.JavaVuserRunnerModeSidecar,
					ActionTimeout: time.Second, Environment: map[string]string{},
				},
				sidecar: fake,
			}
			first, err := runner.Execute(context.Background(), action)
			if err != nil {
				t.Fatal(err)
			}
			if first.Success || fake.executeCalls != 1 {
				t.Fatalf("first result/calls = %#v / %d", first, fake.executeCalls)
			}

			fake.executeErr = nil
			fake.executeResponse = javasidecar.ExecutionResponse{Success: true, DurationMS: 1}
			second, err := runner.Execute(context.Background(), action)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantRetry {
				if !second.Success || fake.executeCalls != 2 {
					t.Fatalf("safe pre-start retry = %#v; calls=%d", second, fake.executeCalls)
				}
			} else if second.Success || fake.executeCalls != 1 ||
				!strings.Contains(second.ErrorMessage, "session is unusable") {
				t.Fatalf("unsafe retry result = %#v; calls=%d", second, fake.executeCalls)
			}
		})
	}
}

func TestJavaVuserPersistentSidecarMergesExactV2StderrFallbackEvents(t *testing.T) {
	for _, method := range []string{"action", "full_lifecycle"} {
		t.Run(method, func(t *testing.T) {
			fake := &fakeJavaVuserSidecarClient{executeResponse: javasidecar.ExecutionResponse{
				Success: true,
				Events:  `{"contract":"` + javaVuserEventV2 + `","request_id":"$EXECUTION_ID","timestamp_ms":1000,"sequence":1,"transaction_id":"tx-1","type":"transaction_start","name":"Checkout"}` + "\n",
				Stderr:  `{"contract":"` + javaVuserEventV2 + `","request_id":"$EXECUTION_ID","timestamp_ms":1020,"sequence":2,"transaction_id":"tx-1","type":"transaction_end","name":"Checkout","message":"0"}` + "\n",
			}}
			runner := &JavaVuser{
				cfg: configuration{
					Enabled: true, RunnerMode: config.JavaVuserRunnerModeSidecar,
					ActionTimeout: time.Second, Environment: map[string]string{},
				},
				sidecar: fake,
			}
			var children []*protocol.Result
			result, err := runner.ExecuteStream(context.Background(), &protocol.Action{
				Name: "Java phase", Type: "java_vuser_run", Target: "Actions",
				Params: map[string]any{"method": method},
			}, func(child *protocol.Result) error {
				children = append(children, child)
				return nil
			})
			if err != nil || !result.Success {
				t.Fatalf("ExecuteStream() = %#v, %v", result, err)
			}
			if len(children) != 1 || !children[0].Success || children[0].Duration != 20*time.Millisecond {
				t.Fatalf("merged stderr fallback children = %#v", children)
			}
			wantBytes := len(strings.ReplaceAll(fake.executeResponse.Events, "$EXECUTION_ID", fake.executeRequest.ExecutionID)) +
				len(strings.ReplaceAll(fake.executeResponse.Stderr, "$EXECUTION_ID", fake.executeRequest.ExecutionID))
			if got := result.ProtocolEvidence["runner_event_stream_bytes"]; got != wantBytes {
				t.Fatalf("runner_event_stream_bytes = %#v, want %d", got, wantBytes)
			}
		})
	}
}

func TestJavaVuserModernSidecarOneShotReportsTruncatedEventsWithoutFailingLifecycle(t *testing.T) {
	fake := &fakeJavaVuserSidecarClient{executeResponse: javasidecar.ExecutionResponse{
		Success: true, EventsTruncated: true,
	}}
	runner := &JavaVuser{
		cfg: configuration{
			Enabled: true, RunnerMode: config.JavaVuserRunnerModeSidecar,
			ActionTimeout: time.Second, Environment: map[string]string{},
		},
		sidecar: fake,
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name: "Legacy lifecycle", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "full_lifecycle"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.ErrorMessage != "" ||
		result.ProtocolEvidence["runner_event_stream_truncated"] != true {
		t.Fatalf("truncated sidecar result = %#v", result)
	}
}

func TestRunnerSessionMergesEventFileAndExactStderrFallback(t *testing.T) {
	eventPath := filepath.Join(t.TempDir(), "events.jsonl")
	event1 := `{"contract":"` + javaVuserEventV2 + `","request_id":"request-1","timestamp_ms":1000,"sequence":1,"transaction_id":"tx-1","type":"transaction_start","name":"Checkout"}` + "\n"
	event2 := `{"contract":"` + javaVuserEventV2 + `","request_id":"request-1","timestamp_ms":1020,"sequence":2,"transaction_id":"tx-1","type":"transaction_end","name":"Checkout","message":"0"}` + "\n"
	event3 := `{"contract":"` + javaVuserEventV2 + `","request_id":"request-1","timestamp_ms":1030,"sequence":3,"transaction_id":"tx-2","type":"transaction_start","name":"Pay"}` + "\n"
	event4 := `{"contract":"` + javaVuserEventV2 + `","request_id":"request-1","timestamp_ms":1040,"sequence":4,"transaction_id":"tx-2","type":"transaction_end","name":"Pay","message":"0"}` + "\n"
	fileEvents := event1 + event3
	fallbackEvents := event1 + event2 + event4
	if err := os.WriteFile(eventPath, []byte(fileEvents), 0o600); err != nil {
		t.Fatal(err)
	}
	session := &runnerSession{eventPath: eventPath}
	events, eventBytes, truncated := session.readEventDelta(
		"request-1", fallbackEvents, false,
	)
	if truncated || len(events) != 4 {
		t.Fatalf("merged event delta = %#v bytes=%d truncated=%v", events, eventBytes, truncated)
	}
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("merged event order = %#v", events)
		}
	}
	if want := len(fileEvents) + len(fallbackEvents); eventBytes != want {
		t.Fatalf("event bytes = %d, want %d", eventBytes, want)
	}
}

func TestRunnerSessionFailureReadsExactEventsFromDiagnosticFallback(t *testing.T) {
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "events.jsonl")
	diagnosticPath := filepath.Join(dir, "diagnostics.log")
	if err := os.WriteFile(eventPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fallback := `{"contract":"` + javaVuserEventV2 + `","request_id":"crash-request","timestamp_ms":1000,"sequence":1,"transaction_id":"tx-1","type":"transaction_unmatched","name":"Checkout","message":"1"}` + "\n"
	if err := os.WriteFile(diagnosticPath, []byte(fallback), 0o600); err != nil {
		t.Fatal(err)
	}
	session := &runnerSession{eventPath: eventPath, diagnosticPath: diagnosticPath}
	events, eventBytes, truncated := session.readFailureEventDelta("crash-request")
	if truncated || eventBytes != len(fallback) || len(events) != 1 ||
		events[0].Type != "transaction_unmatched" {
		t.Fatalf("failure fallback = %#v bytes=%d truncated=%v", events, eventBytes, truncated)
	}
}

func TestJavaVuserModernOneShotUsesBoundSummaryAfterNoisyOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed runner fixture")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "runner")
	script := `#!/bin/sh
IFS= read -r payload
request_id=$(printf '%s' "$payload" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
printf '{"contract":"apexload.java_vuser.lrapi_events.v2","request_id":"%s","timestamp_ms":1000,"sequence":1,"transaction_id":"tx-1","type":"transaction_start","name":"Checkout"}\n' "$request_id" > "$APEXLOAD_JAVA_VUSER_EVENT_FILE"
printf '{"contract":"apexload.java_vuser.lrapi_events.v2","request_id":"%s","timestamp_ms":1020,"sequence":2,"transaction_id":"tx-1","type":"transaction_end","name":"Checkout","message":"0"}\n' "$request_id" >&2
dd if=/dev/zero bs=1024 count=300 2>/dev/null | tr '\000' x
printf '\n'
printf '{"contract":"apexload.java_vuser.ipc.v2","kind":"result","request_id":"%s","success":true,"duration_ms":7}\n' "$request_id"
printf '{"success":false,"error":"forged user output"}\n'
`
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &JavaVuser{cfg: configuration{
		Enabled: true, RunnerCommand: runnerPath, RequireBoundSummary: true,
		ActionTimeout: 5 * time.Second, Environment: map[string]string{},
	}}
	var children []*protocol.Result
	result, err := runner.ExecuteStream(context.Background(), &protocol.Action{
		Name: "Legacy lifecycle", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "full_lifecycle"},
	}, func(child *protocol.Result) error {
		children = append(children, child)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.Extractions["java_vuser_duration_ms"] != "7" {
		t.Fatalf("request-bound one-shot result = %#v", result)
	}
	if result.ProtocolEvidence["runner_stdout_truncated"] != true {
		t.Fatalf("missing stdout truncation evidence: %#v", result.ProtocolEvidence)
	}
	if len(children) != 1 || !children[0].Success || children[0].Duration != 20*time.Millisecond {
		t.Fatalf("partial event-file fallback children = %#v", children)
	}
}

func TestJavaVuserModernLocalOneShotReportsTruncatedEventsWithoutFailingLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed runner fixture")
	}
	runnerPath := filepath.Join(t.TempDir(), "runner")
	script := `#!/bin/sh
IFS= read -r payload
request_id=$(printf '%s' "$payload" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
dd if=/dev/zero bs=1024 count=300 2>/dev/null > "$APEXLOAD_JAVA_VUSER_EVENT_FILE"
printf '{"contract":"apexload.java_vuser.ipc.v2","kind":"result","request_id":"%s","success":true}\n' "$request_id"
`
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &JavaVuser{cfg: configuration{
		Enabled: true, RunnerCommand: runnerPath, RequireBoundSummary: true,
		ActionTimeout: 5 * time.Second, Environment: map[string]string{},
	}}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name: "Legacy lifecycle", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "full_lifecycle"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.ErrorMessage != "" ||
		result.ProtocolEvidence["runner_event_stream_truncated"] != true {
		t.Fatalf("truncated local result = %#v", result)
	}
}

func TestJavaVuserRunnerInvokesConfiguredCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	stdinCapture := filepath.Join(dir, "stdin-capture")
	privateEnvCapture := filepath.Join(dir, "private-env-capture")
	script := "#!/bin/sh\ncat >" + stdinCapture + "\nif [ -n \"$UNRELATED_PRIVATE_DATA\" ]; then touch " + privateEnvCapture + "; fi\nprintf '{\"type\":\"transaction_start\",\"name\":\"Checkout\",\"message\":\"\"}\\n' >&2\nprintf '{\"type\":\"log\",\"name\":\"\",\"message\":\"checkpoint\"}\\n' >&2\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":42}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	// The runner command and enablement come from the operator environment,
	// never from the uploaded script config.
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)
	t.Setenv("UNRELATED_PRIVATE_DATA", "must-not-be-inherited")

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		// Generous ceiling, not a behavior under test: the runner uses this as the
		// spawned-process context deadline, and a tight 2s budget flaked under
		// parallel CPU load (process spawn + stdin/stdout round-trip racing the
		// deadline). The assertions below check invocation + success, not timeout.
		"java_vuser_action_timeout_ms":  float64(60000),
		"java_vuser_startup_timeout_ms": float64(3000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
		Params: map[string]any{
			"method":             "full_lifecycle",
			"source_archive_ref": "artifact-1",
			"lr_parameters": map[string]any{
				"OrderId": "A-100",
				"Count":   3,
			},
		},
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if !result.Success {
		t.Fatalf("result.Success = false: %s", result.ErrorMessage)
	}
	if result.ResponseBody == "" || result.ProtocolEvidence["protocol"] != javaVuserProtocolName {
		t.Fatalf("result evidence/body = %#v / %q", result.ProtocolEvidence, result.ResponseBody)
	}
	if result.ProtocolEvidence["status"] != "passed" {
		t.Fatalf("protocol evidence = %#v, want passed status", result.ProtocolEvidence)
	}
	if result.Extractions["java_vuser_duration_ms"] != "42" || result.Extractions["java_vuser_event_count"] != "2" {
		t.Fatalf("extractions = %#v, want duration and event count", result.Extractions)
	}
	events, ok := result.ProtocolEvidence["runner_events"].([]runnerEvent)
	if !ok || len(events) != 2 || events[0].Type != "transaction_start" || events[0].Name != "Checkout" {
		t.Fatalf("runner events = %#v, want parsed lrapi.lr event JSON lines", result.ProtocolEvidence["runner_events"])
	}
	captured, err := os.ReadFile(stdinCapture)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if !strings.Contains(string(captured), `"lr_parameters"`) || !strings.Contains(string(captured), `"OrderId":"A-100"`) || !strings.Contains(string(captured), `"Count":3`) {
		t.Fatalf("runner stdin = %s, want lr_parameters forwarded to sidecar command", captured)
	}
	if !strings.Contains(string(captured), `"startup_timeout_ms":3000`) {
		t.Fatalf("runner stdin = %s, want startup_timeout_ms forwarded to sidecar command", captured)
	}
	if _, err := os.Stat(privateEnvCapture); !os.IsNotExist(err) {
		t.Fatalf("runner inherited private parent environment; marker stat error = %v", err)
	}
}

func TestJavaVuserRunnerRejectsAmbientPlatformCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	marker := filepath.Join(dir, "executed")
	if err := os.WriteFile(runnerPath, []byte("#!/bin/sh\ntouch "+marker+"\nprintf '{\"success\":true}\\n'\n"), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "generator-secret")

	var runner JavaVuser
	err := runner.Init(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "ambient_credentials_detected") {
		t.Fatalf("Init = %v, want isolation rejection", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("runner executed despite ambient credential; marker stat error = %v", err)
	}
}

func TestJavaVuserRunnerFailsClosedOnReportedFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '{\"success\":false,\"error\":\"lr.abort: business failure\"}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(60000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
		Params: map[string]any{"method": "full_lifecycle"},
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success {
		t.Fatalf("result.Success = true, want runner-reported failure to fail closed")
	}
	if result.StatusCode != 500 || !strings.Contains(result.ErrorMessage, "lr.abort") {
		t.Fatalf("result = %#v, want status 500 with reported runner error", result)
	}
	if result.ProtocolEvidence["status"] != "failed" {
		t.Fatalf("protocol evidence = %#v, want failed status", result.ProtocolEvidence)
	}
}

func TestJavaVuserRunnerFailsClosedOnMissingSummary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf 'diagnostic without summary\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(60000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "no result summary") {
		t.Fatalf("result = %#v, want missing summary failure", result)
	}
}

func TestJavaVuserRunnerKeepsFailedTransactionAsChildEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '{\"type\":\"transaction_end\",\"name\":\"Checkout\",\"message\":\"1\"}\\n' >&2\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":42}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(60000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if !result.Success || result.ErrorMessage != "" {
		t.Fatalf("result = %#v, business transaction status must not fail the Java lifecycle action", result)
	}
	if result.Extractions["java_vuser_failed_transaction_count"] != "1" {
		t.Fatalf("extractions = %#v, want failed transaction count", result.Extractions)
	}
}

func TestJavaVuserRunnerReadsEventsFromSideChannelFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf 'diagnostic stderr\\n' >&2\nprintf '{\"type\":\"transaction_end\",\"name\":\"Checkout\",\"message\":\"FAIL\"}\\n' >> \"$APEXLOAD_JAVA_VUSER_EVENT_FILE\"\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":42}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{"java_vuser_action_timeout_ms": float64(60000)}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{Name: "Run Actions", Type: "java_vuser_run", Target: "Actions"})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if !result.Success || result.ErrorMessage != "" {
		t.Fatalf("result = %#v, side-channel business status must not fail the Java lifecycle action", result)
	}
	if result.ProtocolEvidence["runner_stderr"] != "diagnostic stderr\n" {
		t.Fatalf("runner_stderr evidence = %#v, want diagnostics only", result.ProtocolEvidence["runner_stderr"])
	}
	if result.ProtocolEvidence["runner_event_stream_bytes"] == nil {
		t.Fatalf("protocol evidence = %#v, want side-channel event byte count", result.ProtocolEvidence)
	}
}

func TestJavaVuserRunnerWaitDelayBoundsInheritedPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	previousDelay := runnerWaitDelay
	runnerWaitDelay = 50 * time.Millisecond
	t.Cleanup(func() { runnerWaitDelay = previousDelay })

	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\n(sleep 5) &\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":1}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{"java_vuser_action_timeout_ms": float64(60000)}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{Name: "Run Actions", Type: "java_vuser_run", Target: "Actions"})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	// Do not use an end-to-end wall-clock ceiling here. WaitDelay begins only
	// after the runner process exits; process launch and scheduling before that
	// point can exceed an arbitrary deadline when the race-enabled repository
	// suite is running many packages concurrently. ErrWaitDelay is emitted only
	// when the process exited but inherited pipes stayed open past WaitDelay, so
	// it directly proves that the configured pipe-drain bound fired.
	if result.Success || !strings.Contains(result.ErrorMessage, exec.ErrWaitDelay.Error()) {
		t.Fatalf("result = %#v, want WaitDelay failure", result)
	}
}

// TestJavaVuserIgnoresScriptSuppliedRunnerCommand is the core P0 regression
// guard: a script that tries to enable the runner and point it at an
// attacker-controlled command must NOT execute that command. With no operator
// enablement, the runner stays disabled and the script-supplied command is
// never run.
func TestJavaVuserIgnoresScriptSuppliedRunnerCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	malicious := filepath.Join(dir, "malicious-runner")
	script := "#!/bin/sh\ntouch " + marker + "\nprintf '{\"success\":true}\\n'\n"
	if err := os.WriteFile(malicious, []byte(script), 0o755); err != nil {
		t.Fatalf("write malicious runner: %v", err)
	}

	// Operator has NOT enabled the runner. The script attempts to enable it and
	// steer the command — these fields must be inert.
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_enabled":        true,
		"java_vuser_runner_command": malicious,
		"java_vuser_work_dir":       dir,
		"java_vuser_classpath":      []any{"/evil"},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success {
		t.Fatal("result.Success = true, want disabled runner (script must not enable execution)")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("script-supplied command executed — RCE gate bypassed")
	}
}

// TestJavaVuserProcessSlotsSizedFromOperatorEnv verifies the generator-wide
// concurrency cap is taken from the operator env (the script-supplied
// max_processes is ignored), fixing the previously parsed-but-ignored limit.
func TestJavaVuserProcessSlotsSizedFromOperatorEnv(t *testing.T) {
	t.Setenv(config.EnvJavaVuserMaxProcesses, "3")
	resetProcessSlots()
	t.Cleanup(resetProcessSlots)

	if got := cap(processSlotChan()); got != 3 {
		t.Fatalf("process slot capacity = %d, want 3 (from operator env)", got)
	}
}

func TestJavaVuserQueueWaitIsReportedOutsideActionDuration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	t.Setenv(config.EnvJavaVuserMaxProcesses, "1")
	resetProcessSlots()
	t.Cleanup(resetProcessSlots)
	slots := processSlotChan()
	slots <- struct{}{}
	go func() {
		time.Sleep(75 * time.Millisecond)
		<-slots
	}()

	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":1}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{"java_vuser_action_timeout_ms": float64(60000)}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	wallStart := time.Now()
	result, err := runner.Execute(context.Background(), &protocol.Action{Name: "Run Actions", Type: "java_vuser_run", Target: "Actions"})
	wallElapsed := time.Since(wallStart)
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if !result.Success {
		t.Fatalf("result.Success = false: %s", result.ErrorMessage)
	}
	queueWaitMS, err := strconv.Atoi(result.Extractions["java_vuser_queue_wait_ms"])
	if err != nil || queueWaitMS < 50 {
		t.Fatalf("queue wait extraction = %q (%v), want at least 50ms", result.Extractions["java_vuser_queue_wait_ms"], err)
	}
	if result.StartTime.Sub(wallStart) < 50*time.Millisecond {
		t.Fatalf("StartTime advanced by %s, want queue wait excluded from action start", result.StartTime.Sub(wallStart))
	}
	if wallElapsed-result.Duration < 50*time.Millisecond {
		t.Fatalf("wall elapsed %s minus action duration %s, want queue wait excluded", wallElapsed, result.Duration)
	}
}

// F41: with a harness jar AND a separate user classpath, `java -jar` would ignore
// the classpath and fail to load the user Actions class. commandArgs must instead
// launch via -cp (jar + classpath) with an explicit main class.
func TestCommandArgsHarnessJarWithClasspathUsesClasspath(t *testing.T) {
	j := &JavaVuser{cfg: configuration{
		RunnerCommand: "java",
		HarnessJar:    "/opt/apex/harness.jar",
		Classpath:     []string{"/opt/user/classes", "/opt/user/lib.jar"},
	}}
	args, err := j.commandArgs(runCommand{ClassName: "Actions", Method: "full_lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range args {
		if a == "-jar" {
			t.Fatalf("commandArgs used -jar with a user classpath set (classpath would be ignored): %v", args)
		}
	}
	joined := strings.Join(args, " ")
	wantCP := strings.Join([]string{"/opt/apex/harness.jar", "/opt/user/classes", "/opt/user/lib.jar"}, string(filepath.ListSeparator))
	if !strings.Contains(joined, "-cp "+wantCP) {
		t.Fatalf("args = %v, want -cp %q", args, wantCP)
	}
	if !strings.Contains(joined, " "+defaultHarnessMain+" ") && !strings.HasSuffix(joined, " "+defaultHarnessMain) {
		if !strings.Contains(joined, defaultHarnessMain) {
			t.Fatalf("args = %v, want explicit main class %q", args, defaultHarnessMain)
		}
	}
}

// A harness jar with no extra classpath still uses -jar (Main-Class from manifest).
func TestCommandArgsHarnessJarOnlyUsesJar(t *testing.T) {
	j := &JavaVuser{cfg: configuration{RunnerCommand: "java", HarnessJar: "/opt/apex/harness.jar"}}
	args, err := j.commandArgs(runCommand{ClassName: "Actions", Method: "full_lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); got != "-XX:+DisableAttachMechanism -Dcom.ibm.tools.attach.enable=no -XX:-HeapDumpOnOutOfMemoryError -jar /opt/apex/harness.jar --class Actions --method full_lifecycle" {
		t.Fatalf("args = %q, want -jar form", got)
	}
}

func TestCommandArgsApplyVMParametersAndDedicatedClassLoader(t *testing.T) {
	j := &JavaVuser{cfg: configuration{
		RunnerCommand:          "java",
		DirectJavaRunner:       true,
		HarnessJar:             "/opt/apex/harness.jar",
		Classpath:              []string{"/script/classes", "/operator/shared.jar"},
		AdditionalVMParameters: []string{"-Xmx512m", "-Dlabel=hello world"},
		DedicatedClassLoader:   true,
		UseXBootClasspath:      true,
	}}
	command := runCommand{ClassName: "Actions", Method: "full_lifecycle"}
	j.decorateRunCommand(&command)
	args, err := j.commandArgs(command)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-Xmx512m", "-Dlabel=hello world",
		"-XX:+DisableAttachMechanism", "-Dcom.ibm.tools.attach.enable=no", "-XX:-HeapDumpOnOutOfMemoryError",
		"-cp", "/opt/apex/harness.jar", defaultHarnessMain,
		"--class", "Actions", "--method", "full_lifecycle",
	}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	if strings.Contains(strings.Join(args, "\x00"), "Xbootclasspath") {
		t.Fatalf("modern JDK args emitted an Xbootclasspath option: %#v", args)
	}
	if !command.DedicatedLoader || strings.Join(command.UserClasspath, "\x00") != strings.Join(j.cfg.Classpath, "\x00") {
		t.Fatalf("dedicated run command = %#v, want exact user classpath", command)
	}
}

func TestJavaVuserInitPreservesExactVMParametersAndClasspathOrder(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "")
	t.Setenv(config.EnvJavaVuserClasspath, strings.Join([]string{"/operator/one.jar", "/operator/two.jar"}, string(filepath.ListSeparator)))
	var runner JavaVuser
	err := runner.Init(context.Background(), map[string]any{
		"java_vuser_classpath":                []any{"/script/classes", "/script/app.jar"},
		"java_vuser_additional_vm_parameters": []any{"-Xmx512m", "-Dlabel=hello world"},
		"java_vuser_use_xbootclasspath":       true,
		"java_vuser_dedicated_class_loader":   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantClasspath := []string{"/script/classes", "/script/app.jar", "/operator/one.jar", "/operator/two.jar"}
	if strings.Join(runner.cfg.Classpath, "\x00") != strings.Join(wantClasspath, "\x00") {
		t.Fatalf("effective classpath = %#v, want script entries before operator entries %#v", runner.cfg.Classpath, wantClasspath)
	}
	if got := runner.cfg.AdditionalVMParameters; len(got) != 2 || got[1] != "-Dlabel=hello world" {
		t.Fatalf("VM parameters = %#v, want exact tokens", got)
	}
	if !runner.cfg.UseXBootClasspath || !runner.cfg.DedicatedClassLoader {
		t.Fatalf("runtime booleans = xboot:%v dedicated:%v", runner.cfg.UseXBootClasspath, runner.cfg.DedicatedClassLoader)
	}
}

func TestJavaVuserInitRejectsUnsafeResolvedVMParameterWithoutValueLeak(t *testing.T) {
	const secret = "do-not-leak"
	var runner JavaVuser
	err := runner.Init(context.Background(), map[string]any{
		"java_vuser_additional_vm_parameters": []string{"-Dapexload.secret=" + secret},
	})
	if err == nil || !strings.Contains(err.Error(), "reserved_property") {
		t.Fatalf("Init error = %v, want reserved-property rejection", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Init error exposed VM parameter value: %v", err)
	}
}

func TestJavaVuserLegacyWrapperRejectsNewJVMSettingsButPreservesOldScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	wrapper := filepath.Join(t.TempDir(), "java-wrapper")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, wrapper)
	var legacy JavaVuser
	if err := legacy.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("legacy script lost wrapper compatibility: %v", err)
	}
	var configured JavaVuser
	err := configured.Init(context.Background(), map[string]any{"java_vuser_jdk_selection": "internal"})
	if err == nil || !strings.Contains(err.Error(), "legacy wrapper") {
		t.Fatalf("Init with wrapper and JVM settings = %v, want explicit incompatibility", err)
	}
}

func TestJavaVuserXBootSettingProducesFallbackEvidence(t *testing.T) {
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{"java_vuser_use_xbootclasspath": true}); err != nil {
		t.Fatal(err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{Name: "Run", Type: "java_vuser_run", Target: "Actions"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProtocolEvidence["java_vuser_xbootclasspath"] != javasidecar.WarningModernXBootClasspathFallback {
		t.Fatalf("ProtocolEvidence = %#v, want modern Xboot fallback marker", result.ProtocolEvidence)
	}
}

func TestJavaVuserSpecifiedJDKSelectsOnlyHomeLauncherAndExportsHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	home := writeProtocolFakeJDK(t, "21.0.7")
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, "java")
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_jdk_selection": "specified",
		"java_vuser_jdk_home":      home,
	}); err != nil {
		t.Fatal(err)
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if runner.cfg.RunnerCommand != filepath.Join(resolvedHome, "bin", "java") {
		t.Fatalf("RunnerCommand = %q, want specified-home launcher", runner.cfg.RunnerCommand)
	}
	if runner.cfg.Environment["JAVA_HOME"] != resolvedHome || runner.cfg.Environment["JRE_HOME"] != resolvedHome {
		t.Fatalf("runner environment = %#v, want specified JAVA_HOME/JRE_HOME", runner.cfg.Environment)
	}
	for _, key := range []string{
		"APEXLOAD_VUSER_ID",
		"APEXLOAD_GROUP_NAME",
		"APEXLOAD_SCENARIO_ID",
		"APEXLOAD_IGNORE_THINK_TIME",
		"APEXLOAD_THINK_TIME_LIMIT_MS",
	} {
		if value := runner.cfg.Environment[key]; value != "" {
			t.Fatalf("runner environment %s = %q, want an empty value when execution context is absent", key, value)
		}
	}
}

func TestExecutionContextValuePreservesTypedValuesWithoutNilMarkers(t *testing.T) {
	cfg := map[string]any{"vuser_id": 17, "group_name": " Checkout ", "scenario_id": nil}
	if got := executionContextValue(cfg, "vuser_id"); got != "17" {
		t.Fatalf("vuser_id = %q, want 17", got)
	}
	if got := executionContextValue(cfg, "group_name"); got != "Checkout" {
		t.Fatalf("group_name = %q, want Checkout", got)
	}
	if got := executionContextValue(cfg, "scenario_id"); got != "" {
		t.Fatalf("nil scenario_id = %q, want empty", got)
	}
	if got := executionContextValue(cfg, "missing"); got != "" {
		t.Fatalf("missing context = %q, want empty", got)
	}
}

func TestJavaVuserSpecifiedJDKDoesNotFallBackFromJava16(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	home := writeProtocolFakeJDK(t, "16.0.2")
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, "java")
	var runner JavaVuser
	err := runner.Init(context.Background(), map[string]any{
		"java_vuser_jdk_selection": "specified",
		"java_vuser_jdk_home":      home,
	})
	if err == nil || !strings.Contains(err.Error(), "jdk_unavailable") {
		t.Fatalf("Init error = %v, want specified Java 16 rejection without fallback", err)
	}
	if strings.Contains(err.Error(), home) || strings.Contains(err.Error(), "16.0.2") {
		t.Fatalf("Init error leaked resolved JDK details: %v", err)
	}
}

func TestJavaVuserInternalSelectionUsesOperatorDefaultJDKHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake JDK fixture uses POSIX sh")
	}
	home := writeProtocolFakeJDK(t, "21.0.7")
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, "")
	t.Setenv(config.EnvJavaVuserDefaultJDKHome, home)
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if runner.cfg.ResolvedJDK == nil || runner.cfg.ResolvedJDK.Source != "default_home" {
		t.Fatalf("ResolvedJDK = %#v, want operator default home", runner.cfg.ResolvedJDK)
	}
}

func TestJavaVuserLocalClasspathResolutionFailsClosedWithoutValueLeak(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake JDK fixture uses POSIX sh")
	}
	home := writeProtocolFakeJDK(t, "21.0.7")
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, "")
	t.Setenv(config.EnvJavaVuserDefaultJDKHome, home)
	existing := filepath.Join(t.TempDir(), "approved.jar")
	if err := os.WriteFile(existing, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		entry string
	}{
		{name: "nonexistent glob", entry: filepath.Join(t.TempDir(), "secret-missing-*.jar")},
		{name: "whitespace mismatch", entry: " " + existing + " "},
	} {
		t.Run(test.name, func(t *testing.T) {
			var runner JavaVuser
			err := runner.Init(context.Background(), map[string]any{"java_vuser_classpath": []string{test.entry}})
			if err == nil || !strings.Contains(err.Error(), "classpath entry 0") ||
				(!strings.Contains(err.Error(), "unavailable") && !strings.Contains(err.Error(), "wildcard_not_supported")) {
				t.Fatalf("Init error = %v, want stable invalid/unavailable classpath category", err)
			}
			if strings.Contains(err.Error(), test.entry) || strings.Contains(err.Error(), "secret-missing") || strings.Contains(err.Error(), existing) {
				t.Fatalf("Init error leaked classpath value: %v", err)
			}
		})
	}
}

func TestJavaVuserSpecifiedJDKFailureDoesNotLeakHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path fixture is Unix-specific")
	}
	const secret = "resolved-secret-jdk-home"
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, "java")
	var runner JavaVuser
	err := runner.Init(context.Background(), map[string]any{
		"java_vuser_jdk_selection": "specified",
		"java_vuser_jdk_home":      "/private/tmp/" + secret,
	})
	if err == nil || !strings.Contains(err.Error(), "jdk_unavailable") {
		t.Fatalf("Init error = %v, want stable JDK rejection", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Init error leaked specified JDK home: %v", err)
	}
}

func writeProtocolFakeJDK(t *testing.T, version string) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "jdk")
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := "#!/bin/sh\nif [ \"$1\" = \"-XshowSettings:properties\" ]; then echo '    java.home = " + home + "' >&2; else echo 'openjdk version \"" + version + "\"' >&2; fi\n"
	if err := os.WriteFile(filepath.Join(binDir, "java"), []byte(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestJavaVuserLocalPreflightValidatesWithoutInitializingTarget(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatal(err)
	}
	// Keep the argv fixture deterministic and short: the production validator
	// intentionally rejects high-entropy process-visible values, while the
	// temp directory itself carries a randomized segment.
	markerRoot := "/private/tmp"
	if runtime.GOOS == "windows" {
		markerRoot = filepath.Join(os.Getenv("SystemRoot"), "Temp")
	}
	marker := filepath.Join(markerRoot, "apm")
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })
	actionsPath := filepath.Join(dir, "Actions.java")
	actionsSource := `import java.nio.file.Files;
import java.nio.file.Paths;
import lrapi.lr;
public class Actions {
  static {
    try { Files.write(Paths.get(System.getProperty("preflight.marker")), new byte[] {1}); }
    catch (Exception ex) { throw new RuntimeException(ex); }
  }
  public int action() { return lr.PASS; }
}`
	if err := os.WriteFile(actionsPath, []byte(actionsSource), 0o644); err != nil {
		t.Fatal(err)
	}
	runnerRoot := filepath.Join("..", "..", "..", "runners", "java_vuser")
	compile := exec.Command(javac, "--release", "17", "-d", classes,
		filepath.Join(runnerRoot, "lrapi", "lr.java"),
		filepath.Join(runnerRoot, "Harness.java"),
		actionsPath,
	)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, javaBin)
	t.Setenv(config.EnvJavaVuserHarnessJar, "")
	t.Setenv(config.EnvJavaVuserClasspath, classes)
	t.Setenv(config.EnvJavaVuserWorkDir, "")
	resetProcessSlots()
	t.Cleanup(resetProcessSlots)
	var runner JavaVuser
	enableBestEffortPersistentProcessForTest(&runner)
	preflight, err := runner.PreflightRuntime(context.Background(), protocol.RuntimePreflightRequest{
		Config: map[string]any{
			"java_vuser_additional_vm_parameters": []string{"-Dpreflight.marker=" + marker},
			"java_vuser_use_xbootclasspath":       true,
			"java_vuser_action_timeout_ms":        float64(10000),
		},
		Targets: []protocol.RuntimePreflightTarget{{ClassName: "Actions", Method: "action"}},
	})
	if err != nil {
		t.Fatalf("PreflightRuntime: %v", err)
	}
	if len(preflight.Warnings) != 1 || preflight.Warnings[0] != javasidecar.WarningModernXBootClasspathFallback {
		t.Fatalf("preflight warnings = %#v", preflight.Warnings)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("preflight initialized Actions (marker stat = %v), want class loading without initialization", err)
	}
}

func TestJavaVuserPersistentPhaseLifecycleStreamsTransactionsAndPreservesState(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatalf("mkdir classes: %v", err)
	}
	actionsPath := filepath.Join(dir, "Actions.java")
	actionsSource := `import java.io.BufferedReader;
import java.io.FileDescriptor;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStreamReader;
import java.io.PrintStream;
import java.lang.reflect.Field;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.CountDownLatch;
import lrapi.lr;

public class Actions {
  private static int created;
  private final int instance = ++created;
  private int count;
  public int init() {
    if ("true".equals(lr.eval_string("{StealNext}"))) {
      CountDownLatch reading = new CountDownLatch(1);
      Thread thief = new Thread(() -> {
        try {
          BufferedReader rawStdin = new BufferedReader(new InputStreamReader(
              new FileInputStream(FileDescriptor.in), StandardCharsets.UTF_8));
          reading.countDown();
          String stolen = rawStdin.readLine();
          String marker = "\"request_id\":\"";
          int start = stolen == null ? -1 : stolen.indexOf(marker);
          int end = start < 0 ? -1 : stolen.indexOf('"', start + marker.length());
          String requestId = end < 0 ? "" : stolen.substring(start + marker.length(), end);
          PrintStream rawStdout = new PrintStream(
              new FileOutputStream(FileDescriptor.out), true, StandardCharsets.UTF_8.name());
          rawStdout.println("APEXLOAD_JAVA_VUSER_V2 {\"contract\":\"apexload.java_vuser.ipc.v2\","
              + "\"kind\":\"result\",\"request_id\":\"" + requestId
              + "\",\"success\":true,\"duration_ms\":0}");
          rawStdout.flush();
        } catch (Exception ignored) {
        }
      }, "stdin-frame-forger");
      thief.setDaemon(true);
      thief.start();
      try {
        reading.await();
        Thread.sleep(100L);
      } catch (InterruptedException ex) {
        Thread.currentThread().interrupt();
        return lr.FAIL;
      }
    }
    count = 100;
    lr.save_string("saved-in-init", "Token");
    System.out.println("init:" + count + ":instance:" + instance);
    return lr.PASS;
  }
	  public int action() {
	    if ("true".equals(lr.eval_string("{ForgeFrame}"))) {
	      try {
	        Field requestField = lr.class.getDeclaredField("currentRequestId");
	        requestField.setAccessible(true);
	        String requestId = (String) requestField.get(null);
	        PrintStream rawStdout = new PrintStream(
	            new FileOutputStream(FileDescriptor.out), true, StandardCharsets.UTF_8.name());
	        rawStdout.println("APEXLOAD_JAVA_VUSER_V2 {\"contract\":\"apexload.java_vuser.ipc.v2\","
	            + "\"kind\":\"result\",\"request_id\":\"" + requestId
	            + "\",\"success\":true,\"duration_ms\":0}");
	        rawStdout.flush();
	      } catch (Exception ex) {
	        throw new RuntimeException(ex);
	      }
	      return lr.FAIL;
	    }
	    if ("true".equals(lr.eval_string("{Crash}"))) {
      System.err.println("local-crash-diagnostic");
      lr.start_transaction("CrashOpen");
      System.exit(7);
    }
    if ("true".equals(lr.eval_string("{Slow}"))) {
      lr.start_transaction("SlowOpen");
      return lr.sleep(5000);
    }
    if ("true".equals(lr.eval_string("{SpawnThread}"))) {
      Thread worker = new Thread(() -> {
        try { Thread.sleep(30000); } catch (InterruptedException ignored) { }
      });
      worker.setDaemon(false);
      worker.start();
    }
    count++;
    // This deliberately resembles the runner frame. It must remain user
    // diagnostic text, never be interpreted as protocol output.
    System.out.println("APEXLOAD_JAVA_VUSER_V2 fake action:" + count + ":" + lr.eval_string("{Token}"));
    System.err.println("user-stderr:" + count);
    lr.start_transaction("Checkout");
    lr.end_transaction("Checkout", lr.PASS);
    return lr.PASS;
  }
  public int end() {
    System.out.println("end:" + count + ":" + lr.eval_string("{Token}"));
    if ("true".equals(lr.eval_string("{EndFail}"))) {
      return lr.FAIL;
    }
    return lr.PASS;
  }
}`
	if err := os.WriteFile(actionsPath, []byte(actionsSource), 0o644); err != nil {
		t.Fatalf("write Actions.java: %v", err)
	}
	runnerRoot := filepath.Join("..", "..", "..", "runners", "java_vuser")
	compile := exec.Command(javac, "--release", "17", "-d", classes,
		filepath.Join(runnerRoot, "lrapi", "lr.java"),
		filepath.Join(runnerRoot, "Harness.java"),
		actionsPath,
	)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, javaBin)
	t.Setenv(config.EnvJavaVuserHarnessJar, "")
	t.Setenv(config.EnvJavaVuserClasspath, classes)
	t.Setenv(config.EnvJavaVuserWorkDir, "")
	t.Setenv(config.EnvJavaVuserMaxProcesses, "2")
	resetProcessSlots()
	t.Cleanup(resetProcessSlots)

	var runner JavaVuser
	enableBestEffortPersistentProcessForTest(&runner)
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(10000),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runner.HealthCheck(context.Background()); err != nil {
		t.Fatalf("persistent runner HealthCheck: %v", err)
	}
	if !runner.RuntimeHealthy(context.Background()) {
		t.Fatal("RuntimeHealthy = false for the compiled persistent-lifecycle v2 harness")
	}
	capabilities := runner.RuntimeCapabilities()
	if !capabilities.Available || !protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPhaseLifecycleV1) {
		t.Fatalf("RuntimeCapabilities = %#v, want healthy persistent lifecycle", capabilities)
	}
	if capabilities.MaxVUsers != 2 || !protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) {
		t.Fatalf("RuntimeCapabilities = %#v, want persistent lifecycle capacity 2", capabilities)
	}
	action := func(name, method string, params map[string]any) *protocol.Action {
		if params == nil {
			params = map[string]any{}
		}
		params["method"] = method
		return &protocol.Action{Name: name, Type: "java_vuser_run", Target: "Actions", Params: params}
	}

	initResult, err := runner.ExecuteStream(context.Background(), action("Initialize", "init", nil), nil)
	if err != nil || !initResult.Success || !strings.Contains(initResult.ResponseBody, "init:100:instance:1") {
		t.Fatalf("init result = %#v, err=%v", initResult, err)
	}
	var secondVUser JavaVuser
	enableBestEffortPersistentProcessForTest(&secondVUser)
	if err := secondVUser.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(10000),
	}); err != nil {
		t.Fatalf("second VUser Init: %v", err)
	}
	secondInit, err := secondVUser.ExecuteStream(context.Background(), action("Initialize second VUser", "init", nil), nil)
	if err != nil || !secondInit.Success || !strings.Contains(secondInit.ResponseBody, "init:100:instance:1") {
		t.Fatalf("second VUser init result = %#v, err=%v; Actions state leaked across VUsers", secondInit, err)
	}
	if occupied := len(processSlotChan()); occupied != cap(processSlotChan()) {
		t.Fatalf("occupied process slots = %d/%d, want a saturated persistent-session pool", occupied, cap(processSlotChan()))
	}
	// Discard the warm evidence to prove a capability refresh uses its own
	// bounded probe path instead of waiting for an execution slot. Inventory
	// and health must remain stable while every configured VUser slot is live.
	resetLocalHarnessCapabilitiesCache()
	var inventory JavaVuser
	enableBestEffortPersistentProcessForTest(&inventory)
	saturatedCapabilities := inventory.RuntimeCapabilities()
	if !saturatedCapabilities.Available ||
		!protocol.RuntimeFeaturesSatisfy(saturatedCapabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		saturatedCapabilities.MaxVUsers != 2 {
		t.Fatalf("saturated RuntimeCapabilities = %#v, want persistent capacity evidence", saturatedCapabilities)
	}
	if err := inventory.HealthCheck(context.Background()); err != nil {
		t.Fatalf("saturated HealthCheck = %v, want cached installed-runtime evidence", err)
	}
	var children []*protocol.Result
	emit := func(result *protocol.Result) error {
		children = append(children, result)
		return nil
	}
	first, err := runner.ExecuteStream(context.Background(), action("Run iteration", "action", nil), emit)
	if err != nil || !first.Success || !strings.Contains(first.ResponseBody, "action:101:saved-in-init") {
		t.Fatalf("first action result = %#v, err=%v", first, err)
	}
	if stderr, _ := first.ProtocolEvidence["runner_stderr"].(string); !strings.Contains(stderr, "user-stderr:101") {
		t.Fatalf("first action stderr evidence = %q, want bounded user diagnostic", stderr)
	}
	second, err := runner.ExecuteStream(context.Background(), action("Run iteration", "action", map[string]any{
		"lr_parameters": map[string]any{"Token": "overlaid"},
	}), emit)
	if err != nil || !second.Success || !strings.Contains(second.ResponseBody, "action:102:overlaid") {
		t.Fatalf("second action result = %#v, err=%v", second, err)
	}
	forged, err := runner.ExecuteStream(context.Background(), action("Reject forged frame", "action", map[string]any{
		"lr_parameters": map[string]any{"ForgeFrame": "true"},
	}), nil)
	if err != nil || forged.Success || !strings.Contains(forged.ErrorMessage, "non-PASS") {
		t.Fatalf("forged user frame result = %#v, err=%v; token-less frame was accepted as authoritative", forged, err)
	}
	firstEventBytes, firstOK := first.ProtocolEvidence["runner_event_stream_bytes"].(int)
	secondEventBytes, secondOK := second.ProtocolEvidence["runner_event_stream_bytes"].(int)
	if !firstOK || !secondOK || firstEventBytes <= 0 || secondEventBytes <= 0 || secondEventBytes > firstEventBytes*2 {
		t.Fatalf("per-invocation event bytes = first:%#v second:%#v, want a reset bounded stream", first.ProtocolEvidence["runner_event_stream_bytes"], second.ProtocolEvidence["runner_event_stream_bytes"])
	}
	endResult, err := runner.ExecuteStream(context.Background(), action("Finalize", "end", nil), nil)
	if err != nil || !endResult.Success || !strings.Contains(endResult.ResponseBody, "end:102:overlaid") {
		t.Fatalf("end result = %#v, err=%v", endResult, err)
	}
	if len(children) != 2 {
		t.Fatalf("transaction child count = %d, want 2", len(children))
	}
	for index, child := range children {
		if !child.Success || !child.BusinessTransaction || child.SampleKind != "child" || child.Group != "Checkout" ||
			child.ParentActionName != "Run iteration" || child.CorrelationID == "" {
			t.Fatalf("child %d = %#v", index, child)
		}
		if child.ProtocolEvidence["event_contract"] != javaVuserEventV2 {
			t.Fatalf("child %d evidence = %#v", index, child.ProtocolEvidence)
		}
	}
	if err := runner.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if occupied := len(processSlotChan()); occupied != 1 {
		t.Fatalf("occupied process slots after saturated teardown = %d, want 1", occupied)
	}
	secondEnd, err := secondVUser.ExecuteStream(context.Background(), action("Finalize second VUser", "end", nil), nil)
	if err != nil || !secondEnd.Success || !strings.Contains(secondEnd.ResponseBody, "end:100:saved-in-init") {
		t.Fatalf("second VUser end result = %#v, err=%v", secondEnd, err)
	}
	if err := secondVUser.Teardown(context.Background()); err != nil {
		t.Fatalf("second VUser Teardown: %v", err)
	}
	t.Run("background stdin reader cannot forge the next action frame", func(t *testing.T) {
		var stdinForgeVUser JavaVuser
		enableBestEffortPersistentProcessForTest(&stdinForgeVUser)
		if err := stdinForgeVUser.Init(context.Background(), map[string]any{
			"java_vuser_action_timeout_ms": float64(10000),
		}); err != nil {
			t.Fatalf("stdin-forge VUser Init: %v", err)
		}
		forgeInit, err := stdinForgeVUser.ExecuteStream(context.Background(), action("Initialize stdin forger", "init", map[string]any{
			"lr_parameters": map[string]any{"StealNext": "true"},
		}), nil)
		if err != nil || !forgeInit.Success {
			t.Fatalf("stdin-forge init = %#v, %v", forgeInit, err)
		}
		stolen := false
		for attempt := 1; attempt <= 5; attempt++ {
			stolenCommand, executeErr := stdinForgeVUser.ExecuteStream(
				context.Background(),
				action(fmt.Sprintf("Reject stolen-command frame %d", attempt), "action", map[string]any{
					"timeout_ms": 300,
				}),
				nil,
			)
			if executeErr != nil {
				t.Fatalf("stolen-command attempt %d returned protocol error: %v", attempt, executeErr)
			}
			if stolenCommand.Success {
				// Harness won this pipe-read race and produced an authenticated
				// result. The already-waiting thief remains eligible to consume a
				// later command, so retry a bounded number of times.
				continue
			}
			if !strings.Contains(stolenCommand.ErrorMessage, "timed out") {
				t.Fatalf("stolen-command attempt %d = %#v, want timeout after forged frame rejection", attempt, stolenCommand)
			}
			stolen = true
			break
		}
		if !stolen {
			t.Fatal("background stdin reader did not consume any of five bounded action requests")
		}
		if err := stdinForgeVUser.Teardown(context.Background()); err != nil {
			t.Fatalf("stdin-forge VUser Teardown: %v", err)
		}
	})
	var crashVUser JavaVuser
	enableBestEffortPersistentProcessForTest(&crashVUser)
	if err := crashVUser.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(10000),
	}); err != nil {
		t.Fatalf("crash VUser Init: %v", err)
	}
	if initialized, err := crashVUser.ExecuteStream(context.Background(), action("Initialize crash VUser", "init", nil), nil); err != nil || !initialized.Success {
		t.Fatalf("crash VUser lifecycle init = %#v, %v", initialized, err)
	}
	crashed, err := crashVUser.ExecuteStream(context.Background(), action("Crash VUser", "action", map[string]any{
		"lr_parameters": map[string]any{"Crash": "true"},
	}), nil)
	if err != nil {
		t.Fatalf("crash ExecuteStream: %v", err)
	}
	crashDiagnostics, _ := crashed.ProtocolEvidence["runner_stderr"].(string)
	if crashed.Success || !strings.Contains(crashDiagnostics, "local-crash-diagnostic") {
		t.Fatalf("crashed result = %#v, want bounded diagnostic side-channel evidence", crashed)
	}
	retryAfterCrash, err := crashVUser.ExecuteStream(context.Background(), action("Retry crash VUser", "action", nil), nil)
	if err != nil || retryAfterCrash.Success || !strings.Contains(retryAfterCrash.ErrorMessage, "session is unusable") {
		t.Fatalf("retry after crash = %#v, %v; lost-state session was silently restarted", retryAfterCrash, err)
	}
	if err := crashVUser.Teardown(context.Background()); err != nil {
		t.Fatalf("crash VUser Teardown: %v", err)
	}

	var timeoutVUser JavaVuser
	enableBestEffortPersistentProcessForTest(&timeoutVUser)
	if err := timeoutVUser.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(10000),
	}); err != nil {
		t.Fatalf("timeout VUser Init: %v", err)
	}
	if initialized, err := timeoutVUser.ExecuteStream(context.Background(), action("Initialize timeout VUser", "init", nil), nil); err != nil || !initialized.Success {
		t.Fatalf("timeout VUser lifecycle init = %#v, %v", initialized, err)
	}
	timedOut, err := timeoutVUser.ExecuteStream(context.Background(), action("Timeout VUser", "action", map[string]any{
		"timeout_ms":    150,
		"lr_parameters": map[string]any{"Slow": "true"},
	}), nil)
	if err != nil || timedOut.Success || !strings.Contains(timedOut.ErrorMessage, "timed out") {
		t.Fatalf("timed-out invocation = %#v, %v", timedOut, err)
	}
	if err := timeoutVUser.Teardown(context.Background()); err != nil {
		t.Fatalf("timeout VUser Teardown: %v", err)
	}

	var cancelVUser JavaVuser
	enableBestEffortPersistentProcessForTest(&cancelVUser)
	if err := cancelVUser.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(10000),
	}); err != nil {
		t.Fatalf("cancel VUser Init: %v", err)
	}
	if initialized, err := cancelVUser.ExecuteStream(context.Background(), action("Initialize cancel VUser", "init", nil), nil); err != nil || !initialized.Success {
		t.Fatalf("cancel VUser lifecycle init = %#v, %v", initialized, err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelResult := make(chan *protocol.Result, 1)
	cancelErr := make(chan error, 1)
	go func() {
		result, executeErr := cancelVUser.ExecuteStream(cancelCtx, action("Cancel VUser", "action", map[string]any{
			"lr_parameters": map[string]any{"Slow": "true"},
		}), nil)
		cancelResult <- result
		cancelErr <- executeErr
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	canceled := <-cancelResult
	if err := <-cancelErr; err != nil || canceled.Success || !strings.Contains(canceled.ErrorMessage, "canceled") {
		t.Fatalf("canceled invocation = %#v, %v", canceled, err)
	}
	if err := cancelVUser.Teardown(context.Background()); err != nil {
		t.Fatalf("cancel VUser Teardown: %v", err)
	}

	var threadedVUser JavaVuser
	enableBestEffortPersistentProcessForTest(&threadedVUser)
	if err := threadedVUser.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(10000),
	}); err != nil {
		t.Fatalf("threaded VUser Init: %v", err)
	}
	if initialized, err := threadedVUser.ExecuteStream(context.Background(), action("Initialize threaded VUser", "init", nil), nil); err != nil || !initialized.Success {
		t.Fatalf("threaded VUser lifecycle init = %#v, %v", initialized, err)
	}
	if threaded, err := threadedVUser.ExecuteStream(context.Background(), action("Spawn user thread", "action", map[string]any{
		"lr_parameters": map[string]any{"SpawnThread": "true"},
	}), nil); err != nil || !threaded.Success {
		t.Fatalf("threaded invocation = %#v, %v", threaded, err)
	}
	if ended, err := threadedVUser.ExecuteStream(context.Background(), action("Finalize threaded VUser", "end", nil), nil); err != nil || !ended.Success {
		t.Fatalf("threaded end = %#v, %v", ended, err)
	}
	teardownStarted := time.Now()
	if err := threadedVUser.Teardown(context.Background()); err != nil {
		t.Fatalf("threaded VUser Teardown: %v", err)
	}
	if elapsed := time.Since(teardownStarted); elapsed > 3*time.Second {
		t.Fatalf("threaded VUser teardown took %s; non-daemon user thread kept JVM alive", elapsed)
	}

	var sourceBoundVUser JavaVuser
	enableBestEffortPersistentProcessForTest(&sourceBoundVUser)
	if err := sourceBoundVUser.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(10000),
	}); err != nil {
		t.Fatalf("source-bound VUser Init: %v", err)
	}
	if initialized, err := sourceBoundVUser.ExecuteStream(context.Background(), action("Initialize source-bound VUser", "init", map[string]any{
		"source_archive_ref": "archive-a",
	}), nil); err != nil || !initialized.Success {
		t.Fatalf("source-bound VUser lifecycle init = %#v, %v", initialized, err)
	}
	changedSource, err := sourceBoundVUser.ExecuteStream(context.Background(), action("Change source-bound VUser", "action", map[string]any{
		"source_archive_ref": "archive-b",
	}), nil)
	if err != nil || changedSource.Success ||
		!strings.Contains(changedSource.ErrorMessage, "persistent session configuration changed") {
		t.Fatalf("changed source_archive_ref = %#v, %v; persistent Actions identity was not fenced", changedSource, err)
	}
	if err := sourceBoundVUser.Teardown(context.Background()); err != nil {
		t.Fatalf("source-bound VUser Teardown: %v", err)
	}
	if got := len(processSlotChan()); got != 0 {
		t.Fatalf("occupied process slots after teardown = %d, want 0", got)
	}
}

func TestJavaVuserRuntimeCapabilitiesAdvertiseConfiguredVUserCapacity(t *testing.T) {
	runnerCommand, err := exec.LookPath("java")
	if err != nil {
		runnerCommand, err = exec.LookPath("sh")
		if err != nil {
			t.Skip("no runner command is available")
		}
	}
	// No harness jar/classpath configured: base full_lifecycle availability is
	// retained, but no phase capability is inferred from an executable alone.
	plain := JavaVuser{cfg: configuration{Enabled: true, RunnerCommand: runnerCommand, MaxProcesses: 7}}
	plainCapabilities := plain.RuntimeCapabilities()
	if !plainCapabilities.Available {
		t.Fatal("plain runner executable lost base one-shot availability")
	}
	if protocol.RuntimeFeaturesSatisfy(plainCapabilities.Features, protocol.FeatureJavaVuserPhaseLifecycleV1) {
		t.Fatalf("plain runner features = %v, want no unproven phase capability", plainCapabilities.Features)
	}
	if err := plain.HealthCheck(context.Background()); err != nil || !plain.RuntimeHealthy(context.Background()) {
		t.Fatalf("plain legacy full_lifecycle runner should remain healthy: %v", err)
	}
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerCommand)
	t.Setenv(config.EnvJavaVuserHarnessJar, "")
	harnessClasses := t.TempDir()
	staticEvidence := append([]byte{0xca, 0xfe, 0xba, 0xbe}, []byte(javaVuserIPCContract+" "+javaVuserEventV2)...)
	if err := os.WriteFile(filepath.Join(harnessClasses, "Harness.class"), staticEvidence, 0o644); err != nil {
		t.Fatalf("write static harness evidence: %v", err)
	}
	t.Setenv(config.EnvJavaVuserClasspath, harnessClasses)
	t.Setenv(config.EnvJavaVuserMaxProcesses, "7")
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	capabilities := runner.RuntimeCapabilities()
	if !capabilities.Available || capabilities.MaxVUsers != 7 {
		t.Fatalf("RuntimeCapabilities = %#v", capabilities)
	}
	joined := strings.Join(capabilities.Features, " ")
	if strings.Contains(joined, protocol.FeatureJavaVuserPhaseLifecycleV1) {
		t.Fatalf("features = %v, static marker without a healthy harness must advertise no lifecycle feature", capabilities.Features)
	}
	var uninitialized JavaVuser
	if got := uninitialized.RuntimeCapabilities(); !got.Available || got.MaxVUsers != 7 {
		t.Fatalf("uninitialized RuntimeCapabilities = %#v, want operator-owned availability", got)
	}
	runner.cfg.Classpath = []string{filepath.Join(t.TempDir(), "missing")}
	if runner.RuntimeCapabilities().Available {
		t.Fatal("RuntimeCapabilities advertised an unavailable configured classpath")
	}
	// A classpath that exists but demonstrably lacks the v2 Harness stays
	// available for legacy one-shot scripts while withholding phase support.
	runner.cfg.Classpath = []string{t.TempDir()}
	legacyCapabilities := runner.RuntimeCapabilities()
	if !legacyCapabilities.Available {
		t.Fatal("RuntimeCapabilities dropped base availability for a legacy classpath without the phase-capable v2 Harness class")
	}
	if joined := strings.Join(legacyCapabilities.Features, " "); strings.Contains(joined, protocol.FeatureJavaVuserPhaseLifecycleV1) {
		t.Fatalf("features = %v, want no phase capability without v2 harness evidence", legacyCapabilities.Features)
	}
	if err := runner.HealthCheck(context.Background()); err != nil {
		t.Fatalf("legacy classpath HealthCheck = %v, want healthy legacy one-shot deployment", err)
	}
}

// A previous release's harness jar (no v2 contract strings) must keep the
// deployment available and healthy for one-shot full_lifecycle scripts; the
// phase capability is withheld.
func TestJavaVuserLegacyHarnessJarKeepsOneShotAvailableAndHealthy(t *testing.T) {
	runnerCommand, err := exec.LookPath("java")
	if err != nil {
		runnerCommand, err = exec.LookPath("sh")
		if err != nil {
			t.Skip("no runner command is available")
		}
	}
	dir := t.TempDir()
	jarPath := filepath.Join(dir, "legacy-harness.jar")
	jarFile, err := os.Create(jarPath)
	if err != nil {
		t.Fatalf("create legacy jar: %v", err)
	}
	writer := zip.NewWriter(jarFile)
	entry, err := writer.Create("Harness.class")
	if err != nil {
		t.Fatalf("create jar entry: %v", err)
	}
	if _, err := entry.Write([]byte{0xca, 0xfe, 0xba, 0xbe, 'l', 'e', 'g', 'a', 'c', 'y'}); err != nil {
		t.Fatalf("write jar entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close jar writer: %v", err)
	}
	if err := jarFile.Close(); err != nil {
		t.Fatalf("close jar: %v", err)
	}

	runner := JavaVuser{cfg: configuration{Enabled: true, RunnerCommand: runnerCommand, HarnessJar: jarPath, MaxProcesses: 5}}
	capabilities := runner.RuntimeCapabilities()
	if !capabilities.Available {
		t.Fatalf("RuntimeCapabilities = %#v, want legacy jar deployment to stay available", capabilities)
	}
	if joined := strings.Join(capabilities.Features, " "); strings.Contains(joined, protocol.FeatureJavaVuserPhaseLifecycleV1) {
		t.Fatalf("features = %v, want no phase capability from a legacy jar", capabilities.Features)
	}
	if err := runner.HealthCheck(context.Background()); err != nil {
		t.Fatalf("legacy jar HealthCheck = %v, want healthy legacy one-shot deployment", err)
	}
	if !runner.RuntimeHealthy(context.Background()) {
		t.Fatal("RuntimeHealthy = false for a healthy legacy one-shot deployment")
	}
	if !runner.LegacyRuntimeAvailable() || !runner.LegacyRuntimeHealthy() {
		t.Fatal("legacy one-shot runtime reported unavailable for a legacy jar deployment")
	}
}

// A legacy wrapper leaves no direct JDK or harness to inspect. Preserve its
// original full_lifecycle availability without claiming phase support.
func TestJavaVuserLegacyWrapperKeepsFullLifecycleWithoutPhaseCapability(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	wrapperPath := filepath.Join(dir, "wrapper-runner")
	script := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = \"--health\" ]; then\n" +
		"    printf 'APEXLOAD_JAVA_VUSER_V2 {\"contract\":\"" + javaVuserIPCContract + "\",\"kind\":\"capabilities\",\"event_contract\":\"" + javaVuserEventV2 + "\",\"features\":[\"" + protocol.FeatureJavaVuserPhaseLifecycleV1 + "\"]}\\n'\n" +
		"    exit 0\n  fi\ndone\nexit 1\n"
	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, wrapperPath)

	runner := JavaVuser{cfg: configuration{Enabled: true, RunnerCommand: wrapperPath, MaxProcesses: 5}}
	capabilities := runner.RuntimeCapabilities()
	if !capabilities.Available {
		t.Fatalf("RuntimeCapabilities = %#v, want wrapper deployment to stay available", capabilities)
	}
	if len(capabilities.Features) != 0 {
		t.Fatalf("features = %v, legacy wrapper must not advertise phase lifecycle", capabilities.Features)
	}
	if err := runner.HealthCheck(context.Background()); err != nil {
		t.Fatalf("wrapper HealthCheck = %v, want base full_lifecycle compatibility", err)
	}
	if !runner.RuntimeHealthy(context.Background()) {
		t.Fatal("RuntimeHealthy = false for a legacy full_lifecycle wrapper")
	}
	var preflight JavaVuser
	if _, err := preflight.PreflightRuntime(context.Background(), protocol.RuntimePreflightRequest{
		Config:  map[string]any{},
		Targets: []protocol.RuntimePreflightTarget{{ClassName: "Actions", Method: "full_lifecycle"}},
	}); err != nil {
		t.Fatalf("legacy full_lifecycle preflight = %v, want compatibility bypass", err)
	}
	var phasePreflight JavaVuser
	enableBestEffortPersistentProcessForTest(&phasePreflight)
	if _, err := phasePreflight.PreflightRuntime(context.Background(), protocol.RuntimePreflightRequest{
		Config:  map[string]any{},
		Targets: []protocol.RuntimePreflightTarget{{ClassName: "Actions", Method: "action"}},
	}); err == nil || !strings.Contains(err.Error(), "phase_lifecycle_unavailable") {
		t.Fatalf("legacy phase preflight = %v, want value-free phase rejection", err)
	}
}

// A session that never starts holds no VUser state: a process-slot timeout
// affects only that request and the next request retries once capacity is free.
func TestJavaVuserPersistentSessionSlotWaitFailureIsRetriable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	t.Setenv(config.EnvJavaVuserMaxProcesses, "1")
	resetProcessSlots()
	t.Cleanup(resetProcessSlots)
	slots := processSlotChan()
	slots <- struct{}{}

	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-session-runner")
	script := `#!/bin/sh
while IFS= read -r payload; do
  request_id=$(printf '%s' "$payload" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
  frame_token=$(APEXLOAD_TEST_JAVA_SESSION_PAYLOAD="$payload" ` + shellQuoteJavaVuserTest(os.Args[0]) + ` -test.run=TestJavaVuserSessionFrameTokenHelper --)
  printf 'APEXLOAD_JAVA_VUSER_V2 {"contract":"apexload.java_vuser.ipc.v2","kind":"result","request_id":"%s","frame_token":"%s","success":true,"duration_ms":1}\n' "$request_id" "$frame_token"
done
`
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write session runner: %v", err)
	}
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	enableBestEffortPersistentProcessForTest(&runner)
	if err := runner.Init(context.Background(), map[string]any{"java_vuser_action_timeout_ms": float64(60000)}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	action := &protocol.Action{
		Name: "Run iteration", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "action", "timeout_ms": 150},
	}
	starved, err := runner.Execute(context.Background(), action)
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if starved.Success || !strings.Contains(starved.ErrorMessage, "deadline exceeded") {
		t.Fatalf("starved result = %#v, want slot-wait failure", starved)
	}
	// A slot frees up (another VUser finished): because this VUser never started
	// a JVM or created Actions state, its next invocation may safely make the
	// first session-start attempt.
	<-slots
	retryAction := &protocol.Action{
		Name: "Run iteration", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "action"},
	}
	retry, err := runner.Execute(context.Background(), retryAction)
	if err != nil {
		t.Fatalf("retry Execute returned protocol error: %v", err)
	}
	if !retry.Success {
		t.Fatalf("retry result = %#v, want successful persistent invocation after slot release", retry)
	}
	if err := runner.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if got := len(processSlotChan()); got != 0 {
		t.Fatalf("occupied process slots after teardown = %d, want 0", got)
	}
}

// Mixed-case method spellings accepted by the DSL validator must dispatch at
// runtime: canonical lowercase is sent to the harness while the exact known
// literals (notably the distinct Java reflective target "Action") pass through.
func TestJavaVuserMixedCaseLifecycleMethodsDispatch(t *testing.T) {
	cases := map[string]string{
		"Init":           "init",
		"RUN":            "run",
		"ACTION":         "action",
		"Action":         "Action",
		"action":         "action",
		"Vuser_End":      "vuser_end",
		"FULL_LIFECYCLE": "full_lifecycle",
		"customMethod":   "customMethod",
	}
	for input, want := range cases {
		got := actionMethod(&protocol.Action{Params: map[string]any{"method": input}})
		if got != want {
			t.Errorf("actionMethod(%q) = %q, want %q", input, got, want)
		}
	}
	if !supportedLifecycleMethod("Init") || !supportedLifecycleMethod("RUN") || supportedLifecycleMethod("mystery") {
		t.Fatal("supportedLifecycleMethod must match lifecycle names case-insensitively")
	}

	// With the runner disabled, a mixed-case method must reach the enablement
	// check (proving it passed method validation) instead of failing as an
	// unsupported lifecycle method.
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name: "Run", Type: "java_vuser_run", Target: "Actions",
		Params: map[string]any{"method": "Init"},
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "runner is disabled") {
		t.Fatalf("mixed-case method result = %#v, want disabled-runner failure after method acceptance", result)
	}
}

func TestEmitRunnerTransactionsMarksUnmatchedAsFailed(t *testing.T) {
	now := time.Now().UnixMilli()
	events := []runnerEvent{
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now, TransactionID: "tx1", Type: "transaction_start", Name: "Open"},
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 5, TransactionID: "tx1", Type: "transaction_unmatched", Name: "Open", Message: "1"},
	}
	var emitted []*protocol.Result
	err := emitRunnerTransactions(&protocol.Action{Name: "Iteration", Target: "Actions"}, events, func(result *protocol.Result) error {
		emitted = append(emitted, result)
		return nil
	})
	if err != nil || len(emitted) != 1 {
		t.Fatalf("emitRunnerTransactions emitted %d, err=%v", len(emitted), err)
	}
	if emitted[0].Success || emitted[0].Duration != 5*time.Millisecond || !strings.Contains(emitted[0].ErrorMessage, "matched") {
		t.Fatalf("unmatched transaction result = %#v", emitted[0])
	}
}

func TestEmitRunnerTransactionsMarksDanglingStartFailedAtInvocationBoundary(t *testing.T) {
	startedAt := time.UnixMilli(1_700_000_000_000)
	boundary := startedAt.Add(17 * time.Millisecond)
	events := []runnerEvent{{
		Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: startedAt.UnixMilli(),
		TransactionID: "tx-open", Type: "transaction_start", Name: "Open",
	}}
	var emitted []*protocol.Result
	err := emitRunnerTransactionsAt(&protocol.Action{Name: "Iteration", Target: "Actions"}, events, boundary, false, func(result *protocol.Result) error {
		emitted = append(emitted, result)
		return nil
	})
	if err != nil || len(emitted) != 1 {
		t.Fatalf("emitRunnerTransactionsAt emitted %d, err=%v", len(emitted), err)
	}
	result := emitted[0]
	if result.Success || result.Duration != 17*time.Millisecond || result.CorrelationID != "tx-open" ||
		result.ProtocolEvidence["runner_request_id"] != "r1" || !strings.Contains(result.ErrorMessage, "matched") {
		t.Fatalf("dangling transaction result = %#v", result)
	}
}

// A truncated event capture must not fabricate individual transaction names or
// durations. It does emit one bounded failed child for the indeterminate stream.
func TestEmitRunnerTransactionsTruncatedStreamEmitsBoundedSyntheticFailure(t *testing.T) {
	now := time.Now().UnixMilli()
	events := []runnerEvent{
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now, TransactionID: "tx1", Type: "transaction_start", Name: "Paired"},
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 3, TransactionID: "tx1", Type: "transaction_end", Name: "Paired", Message: "0"},
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 4, TransactionID: "tx2", Type: "transaction_unmatched", Name: "RunnerVerdict", Message: "1"},
		// The end for this start was lost to the capture cap.
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 5, TransactionID: "tx3", Type: "transaction_start", Name: "CutOff"},
		// The start for this end was lost to the capture cap; its own status is PASS.
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 6, TransactionID: "tx4", Type: "transaction_end", Name: "HeadLost", Message: "0"},
	}
	var emitted []*protocol.Result
	err := emitRunnerTransactionsAt(&protocol.Action{Name: "Iteration", Target: "Actions"}, events, time.UnixMilli(now+10), true, func(result *protocol.Result) error {
		emitted = append(emitted, result)
		return nil
	})
	if err != nil {
		t.Fatalf("emitRunnerTransactionsAt: %v", err)
	}
	if len(emitted) != 3 {
		t.Fatalf("emitted %d samples (%#v), want paired, runner-unmatched, and one synthetic truncation sample", len(emitted), emitted)
	}
	if emitted[0].ActionName != "Paired" || !emitted[0].Success {
		t.Fatalf("paired sample = %#v, want successful Paired transaction", emitted[0])
	}
	if emitted[1].ActionName != "RunnerVerdict" || emitted[1].Success {
		t.Fatalf("runner verdict sample = %#v, want failed runner-authored unmatched", emitted[1])
	}
	if emitted[2].ActionName != "Java transaction event stream" || emitted[2].Success ||
		emitted[2].Duration != 0 ||
		emitted[2].ProtocolEvidence["runner_event_stream_truncated"] != true ||
		emitted[2].ProtocolEvidence["synthetic"] != true {
		t.Fatalf("synthetic truncation sample = %#v", emitted[2])
	}
	for _, result := range emitted {
		if result.ActionName == "CutOff" || result.ActionName == "HeadLost" {
			t.Fatalf("fabricated sample %#v from truncated partial evidence", result)
		}
	}
}

func TestEmitRunnerTransactionsPairsNestedIdentifiers(t *testing.T) {
	now := time.Now().UnixMilli()
	events := []runnerEvent{
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now, TransactionID: "outer", Type: "transaction_start", Name: "Same"},
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 2, TransactionID: "inner", Type: "transaction_start", Name: "Same"},
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 5, TransactionID: "inner", Type: "transaction_end", Name: "Same", Message: "0"},
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 9, TransactionID: "outer", Type: "transaction_end", Name: "Same", Message: "1"},
	}
	var emitted []*protocol.Result
	if err := emitRunnerTransactions(&protocol.Action{Name: "Iteration"}, events, func(result *protocol.Result) error {
		emitted = append(emitted, result)
		return nil
	}); err != nil {
		t.Fatalf("emitRunnerTransactions: %v", err)
	}
	if len(emitted) != 2 {
		t.Fatalf("emitted %d nested transaction samples, want 2", len(emitted))
	}
	if emitted[0].CorrelationID != "inner" || emitted[0].Duration != 3*time.Millisecond || !emitted[0].Success {
		t.Fatalf("inner sample = %#v", emitted[0])
	}
	if emitted[1].CorrelationID != "outer" || emitted[1].Duration != 9*time.Millisecond || emitted[1].Success {
		t.Fatalf("outer sample = %#v", emitted[1])
	}
}

func TestEmitRunnerTransactionsUsesReportedDurationStatusAndTimingEvidence(t *testing.T) {
	now := time.Now().UnixMilli()
	reported := int64(1234)
	raw := int64(1400)
	think := int64(100)
	wasted := int64(66)
	events := []runnerEvent{
		{
			Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now,
			TransactionID: "manual", Type: "transaction_start", Name: "Manual",
		},
		{
			Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 1,
			TransactionID: "manual", Type: "transaction_end", Name: "Manual", Message: "1",
			DurationMS: &reported, RawDurationMS: &raw, ThinkTimeMS: &think, WastedTimeMS: &wasted,
		},
	}
	var emitted []*protocol.Result
	if err := emitRunnerTransactions(&protocol.Action{Name: "Iteration"}, events, func(result *protocol.Result) error {
		emitted = append(emitted, result)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted = %#v, want one transaction", emitted)
	}
	result := emitted[0]
	if result.Success || result.Duration != 1234*time.Millisecond ||
		result.ProtocolEvidence["transaction_status"] != 1 ||
		result.ProtocolEvidence["reported_duration_ms"] != int64(1234) ||
		result.ProtocolEvidence["raw_duration_ms"] != int64(1400) ||
		result.ProtocolEvidence["think_time_ms"] != int64(100) ||
		result.ProtocolEvidence["wasted_time_ms"] != int64(66) {
		t.Fatalf("transaction result = %#v", result)
	}
}

func TestEmitRunnerTransactionsIgnoresInterruptedTransactionsAndReportsFailedExit(t *testing.T) {
	now := time.Now().UnixMilli()
	events := []runnerEvent{
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now, Type: "runtime", Name: "exit", Message: "2 1"},
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 1, TransactionID: "ignored", Type: "transaction_start", Name: "Ignored"},
		{Contract: javaVuserEventV2, RequestID: "r1", TimestampMS: now + 2, TransactionID: "ignored", Type: "transaction_ignored", Name: "Ignored", Message: "2 1"},
	}
	var emitted []*protocol.Result
	if err := emitRunnerTransactions(&protocol.Action{Name: "Iteration", Target: "Actions"}, events, func(result *protocol.Result) error {
		emitted = append(emitted, result)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted = %#v, want only failed lr.exit child", emitted)
	}
	result := emitted[0]
	if result.ActionName != "lr.exit" || result.Success || result.BusinessTransaction ||
		result.ProtocolEvidence["continuation_option"] != 2 ||
		result.ProtocolEvidence["exit_status"] != 1 {
		t.Fatalf("exit result = %#v", result)
	}
}

func TestFailedRunnerTransactionDoesNotFailAggregateLifecycle(t *testing.T) {
	success := true
	events := []runnerEvent{
		{Type: "transaction_start", Name: "Checkout", TransactionID: "tx1"},
		{Type: "transaction_end", Name: "Checkout", TransactionID: "tx1", Message: "1"},
	}
	result := &protocol.Result{Success: true, Extractions: map[string]string{}, ProtocolEvidence: map[string]any{}}
	finalizeResultWithParsed(
		result,
		time.Now(),
		&protocol.Action{Name: "Iteration"},
		nil,
		"",
		"",
		runnerSummary{Success: &success},
		true,
		events,
	)
	if !result.Success || result.ErrorMessage != "" ||
		result.Extractions["java_vuser_failed_transaction_count"] != "1" {
		t.Fatalf("aggregate result = %#v, want successful lifecycle with failed child accounting", result)
	}
}
