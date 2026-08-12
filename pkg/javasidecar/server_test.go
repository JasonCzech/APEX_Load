package javasidecar

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const testSidecarAuthToken = "test-java-vuser-sidecar-auth-token"

func TestSidecarSessionFrameTokenHelper(t *testing.T) {
	payload := os.Getenv("APEXLOAD_TEST_SIDECAR_SESSION_PAYLOAD")
	if payload == "" {
		return
	}
	var command harnessCommand
	if err := json.Unmarshal([]byte(payload), &command); err != nil {
		os.Exit(2)
	}
	decoded, err := hex.DecodeString(command.SessionSecret)
	if err != nil || len(decoded) != 32 {
		os.Exit(3)
	}
	var secret [32]byte
	copy(secret[:], decoded)
	_, _ = fmt.Fprint(os.Stdout, persistentFrameToken(secret, command.RequestID))
	os.Exit(0)
}

func TestOwnerLeaseExpiryDrainsSidecarAndSignalsExit(t *testing.T) {
	server := NewServer(ServerOptions{})
	generation := &ownerGeneration{ownerID: "generator", epoch: "epoch", ready: true, leaseDeadline: time.Now().Add(-time.Second), leaseToken: 7}
	server.ownerMu.Lock()
	server.owner = generation
	server.ownerMu.Unlock()
	server.runtimeLeaseMu.Lock()
	server.runtimeLeases["active-runtime"] = &runtimeLease{id: "active-runtime", owner: generation, state: runtimeLeaseActive}
	server.runtimeLeaseMu.Unlock()
	server.expireOwnerLease(generation, 7)
	select {
	case <-server.OwnerLeaseExpired():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for owner lease expiry")
	}
	if !server.draining.Load() {
		t.Fatal("sidecar did not enter draining state")
	}
}

func TestExpiredOwnerCannotRenewBeforeDelayedTimerCallback(t *testing.T) {
	server := NewServer(ServerOptions{Operator: config.JavaVuserOperatorConfig{
		Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
	}})
	generation := &ownerGeneration{
		ownerID:       "generator-delayed-timer",
		epoch:         "stale-owner-epoch",
		ready:         true,
		leaseDeadline: time.Now().Add(-time.Second),
		leaseToken:    11,
	}
	server.ownerMu.Lock()
	server.owner = generation
	server.ownerMu.Unlock()
	server.runtimeLeaseMu.Lock()
	server.runtimeLeases["active-runtime"] = &runtimeLease{id: "active-runtime", owner: generation, state: runtimeLeaseActive}
	server.runtimeLeaseMu.Unlock()

	body := strings.NewReader(`{"contract":"` + ContractVersion + `","owner_id":"generator-delayed-timer","lease_ttl_ms":5000}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/owner/claim", body)
	request.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("delayed-callback renewal status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode delayed-callback response: %v", err)
	}
	if response.ErrorCode != "server_draining" {
		t.Fatalf("delayed-callback renewal error = %q, want server_draining", response.ErrorCode)
	}
	if generation.ready || !server.draining.Load() {
		t.Fatalf("expired generation was revived: ready=%v draining=%v", generation.ready, server.draining.Load())
	}
	select {
	case <-server.OwnerLeaseExpired():
	case <-time.After(time.Second):
		t.Fatal("expired semantic deadline did not signal the outer exit watchdog")
	}
}

func TestIdleOwnerLeaseExpiryRelinquishesWithoutRunnerExit(t *testing.T) {
	server := NewServer(ServerOptions{})
	generation := &ownerGeneration{
		ownerID: "idle-generator", epoch: "idle-epoch", ready: true,
		leaseDeadline: time.Now().Add(-time.Second), leaseToken: 3,
	}
	server.ownerMu.Lock()
	server.owner = generation
	server.ownerMu.Unlock()
	server.expireOwnerLease(generation, 3)
	if server.draining.Load() {
		t.Fatal("idle owner expiry drained an otherwise reusable sidecar")
	}
	server.ownerMu.Lock()
	owner := server.owner
	server.ownerMu.Unlock()
	if owner != nil {
		t.Fatalf("idle owner expiry retained stale owner %#v", owner)
	}
	select {
	case <-server.OwnerLeaseExpired():
		t.Fatal("idle owner expiry requested a process restart")
	default:
	}
}

type callbackWriteCloser struct {
	close func() error
}

func (c *callbackWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (c *callbackWriteCloser) Close() error {
	if c.close == nil {
		return nil
	}
	return c.close()
}

func TestCapabilityFeaturesWithholdPersistentLifecycleWithoutStrongContainment(t *testing.T) {
	features := capabilityFeatures(false)
	if slices.Contains(features, FeaturePhaseLifecycleV1) ||
		slices.Contains(features, FeaturePersistentLifecycleV1) ||
		slices.Contains(features, FeatureSessionFrameTokenV1) {
		t.Fatalf("capabilityFeatures(false) = %v, persistent lifecycle must fail closed", features)
	}
	for _, required := range []string{
		FeatureExactVMParameters,
		FeatureJDKSelectionV1,
		FeatureDedicatedClassloader,
		FeatureValidationOnly,
		FeatureOwnerFencingV1,
		FeatureExternalRuntimeV1,
		FeatureRunnerOwnerLeaseV1,
		FeatureRuntimeLeaseV1,
	} {
		if !slices.Contains(features, required) {
			t.Fatalf("capabilityFeatures(false) = %v, missing one-shot feature %q", features, required)
		}
	}
}

func TestBoundedHarnessErrorLimitsVersionSkewedDiagnostics(t *testing.T) {
	value := strings.Repeat("x", MaxHarnessErrorBytes+1024)
	got := boundedHarnessError(value)
	if len(got) > MaxHarnessErrorBytes || !strings.HasSuffix(got, "...[truncated]") {
		t.Fatalf("boundedHarnessError length/suffix = %d/%q", len(got), got[len(got)-min(len(got), 32):])
	}
}

func TestPersistentProcessStartFailureMapsStrongContainmentErrors(t *testing.T) {
	failure := persistentProcessStartFailure(
		fmt.Errorf("platform-specific detail: %w", javarunner.ErrStrongProcessContainmentUnavailable),
	)
	if failure == nil || failure.code != "process_containment_unavailable" {
		t.Fatalf("persistentProcessStartFailure() = %#v, want stable containment code", failure)
	}
	if generic := persistentProcessStartFailure(errors.New("fork failed")); generic == nil || generic.code != "process_failed" {
		t.Fatalf("generic persistentProcessStartFailure() = %#v, want process_failed", generic)
	}
	if terminal := persistentProcessStartFailure(javarunner.ErrProcessContainmentFailed); terminal == nil ||
		terminal.code != "process_containment_unavailable" {
		t.Fatalf("terminal persistentProcessStartFailure() = %#v, want process_containment_unavailable", terminal)
	}
}

func TestPersistentSessionFailsClosedBeforeProcessStartWithoutContainment(t *testing.T) {
	startCalled := false
	implementation := NewServer(ServerOptions{
		supportsStrongProcessContainment: func() bool { return false },
		startStrongProcessTree: func(*exec.Cmd) error {
			startCalled = true
			return nil
		},
	})
	session, failure := implementation.startPersistentSession(
		context.Background(),
		preparedRuntime{},
		ExecutionRequest{
			Command: ActionCommand{
				ActionType: "java_vuser_run",
				ClassName:  "Actions",
				Method:     "action",
			},
		},
		&ownerGeneration{},
	)
	if session != nil || failure == nil || failure.code != "process_containment_unavailable" {
		t.Fatalf("startPersistentSession() = %#v, %#v, want containment rejection", session, failure)
	}
	if startCalled {
		t.Fatal("containment rejection reached the persistent process starter")
	}
}

func TestPersistentStartContainmentFailureQuarantinesSidecarCapacity(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck:                  func() error { return nil },
		supportsStrongProcessContainment: func() bool { return true },
		startStrongProcessTree: func(*exec.Cmd) error {
			return errors.Join(
				javarunner.ErrProcessContainmentFailed,
				errors.New("started process cleanup was not proven"),
			)
		},
	})
	session, failure := implementation.startPersistentSession(
		context.Background(),
		preparedRuntime{
			jdk:        javarunner.ResolvedJDK{JavaBin: "must-not-run"},
			harnessJar: "harness.jar",
		},
		ExecutionRequest{
			SessionID: "failed-start",
			Command: ActionCommand{
				ActionType: "java_vuser_run",
				ClassName:  "Actions",
				Method:     "action",
			},
		},
		&ownerGeneration{},
	)
	if session != nil || failure == nil || failure.code != "process_containment_unavailable" {
		t.Fatalf("startPersistentSession() = %#v, %#v, want terminal containment failure", session, failure)
	}
	if !implementation.containmentFailed.Load() || len(implementation.slots) != 1 {
		t.Fatalf(
			"failed start did not quarantine sidecar capacity: terminal=%v slots=%d",
			implementation.containmentFailed.Load(),
			len(implementation.slots),
		)
	}
	if _, nextFailure := implementation.acquireProcessSlot(context.Background()); nextFailure == nil ||
		nextFailure.code != "process_containment_unavailable" {
		t.Fatalf("future capacity admission = %#v, want terminal rejection", nextFailure)
	}
}

func TestPersistentStartContainmentFailureRemainsTrackedThroughShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed failed-start fixture is Unix-only")
	}
	launcher := filepath.Join(t.TempDir(), "failed-start-java")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec /bin/sleep 3600\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	var started *exec.Cmd
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck:                  func() error { return nil },
		supportsStrongProcessContainment: func() bool { return true },
		startStrongProcessTree: func(cmd *exec.Cmd) error {
			started = cmd
			if err := cmd.Start(); err != nil {
				return err
			}
			return errors.Join(
				javarunner.ErrProcessContainmentFailed,
				errors.New("started process cleanup was not proven"),
			)
		},
	})
	t.Cleanup(func() {
		if started != nil && started.Process != nil {
			_ = started.Process.Kill()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = implementation.Shutdown(shutdownCtx)
	})

	session, failure := implementation.startPersistentSession(
		context.Background(),
		preparedRuntime{
			jdk:        javarunner.ResolvedJDK{JavaBin: launcher},
			harnessJar: "harness.jar",
		},
		ExecutionRequest{
			SessionID: "tracked-failed-start",
			Command: ActionCommand{
				ActionType: "java_vuser_run",
				ClassName:  "Actions",
				Method:     "action",
			},
		},
		&ownerGeneration{},
	)
	if session != nil || failure == nil || failure.code != "process_containment_unavailable" {
		t.Fatalf("startPersistentSession() = %#v, %#v, want containment rejection", session, failure)
	}
	if started == nil || started.Process == nil {
		t.Fatal("failed-start fixture did not launch its direct root")
	}

	shortCtx, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	if err := implementation.Shutdown(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() before failed-start reap = %v, want deadline exceeded", err)
	}

	if err := started.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("terminate failed-start root: %v", err)
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := implementation.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() after failed-start reap = %v", err)
	}
	if started.ProcessState == nil {
		t.Fatal("shutdown completed without reaping the failed-start direct root")
	}
}

func TestJDKProbeContainmentFailureTripsSidecarAdmissionGate(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck: func() error { return nil },
		resolveJDK: func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
			return javarunner.ResolvedJDK{}, errors.Join(
				javarunner.ErrProcessContainmentFailed,
				errors.New("probe cleanup detail"),
			)
		},
	})
	_, failure := implementation.prepareRuntime(context.Background(), RuntimeSettings{})
	if failure == nil || failure.code != "process_containment_unavailable" {
		t.Fatalf("prepareRuntime() failure = %#v, want terminal containment rejection", failure)
	}
	if !implementation.containmentFailed.Load() {
		t.Fatal("JDK probe containment failure did not close sidecar process admission")
	}
	if len(implementation.probeSlots) != 1 {
		t.Fatalf("JDK containment failure released sidecar probe capacity: slots=%d", len(implementation.probeSlots))
	}
	if _, healthFailure := implementation.health(context.Background()); healthFailure == nil ||
		healthFailure.code != "process_containment_unavailable" {
		t.Fatalf("post-failure health = %#v, want terminal containment rejection", healthFailure)
	}
	capabilitiesRequest := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	capabilitiesRequest.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
	capabilitiesResponse := httptest.NewRecorder()
	implementation.ServeHTTP(capabilitiesResponse, capabilitiesRequest)
	if capabilitiesResponse.Code != http.StatusServiceUnavailable ||
		!strings.Contains(capabilitiesResponse.Body.String(), "process_containment_unavailable") {
		t.Fatalf(
			"post-failure capabilities = status %d body %q, want terminal rejection",
			capabilitiesResponse.Code,
			capabilitiesResponse.Body.String(),
		)
	}
	if _, acquireFailure := implementation.acquireProcessSlot(context.Background()); acquireFailure == nil ||
		acquireFailure.code != "process_containment_unavailable" {
		t.Fatalf("post-failure process admission = %#v, want terminal containment rejection", acquireFailure)
	}
}

func TestStrongContainmentProbeFailureQuarantinesSidecarProbeCapacity(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck:                  func() error { return nil },
		supportsStrongProcessContainment: func() bool { return true },
	})

	release, failure := implementation.acquireProbeSlot(context.Background())
	if failure != nil {
		t.Fatalf("acquireProbeSlot() failure = %#v", failure)
	}
	if implementation.finishStrongContainmentProbe(release, true, true) {
		t.Fatal("failed strong-containment cleanup was reported available")
	}
	if !implementation.containmentFailed.Load() || len(implementation.probeSlots) != 1 {
		t.Fatalf(
			"failed strong probe did not quarantine sidecar probe capacity: terminal=%v slots=%d",
			implementation.containmentFailed.Load(),
			len(implementation.probeSlots),
		)
	}
	if nextRelease, nextFailure := implementation.acquireProbeSlot(context.Background()); nextRelease != nil ||
		nextFailure == nil || nextFailure.code != "process_containment_unavailable" {
		t.Fatalf(
			"post-failure probe admission = release:%v failure:%#v, want terminal rejection",
			nextRelease != nil,
			nextFailure,
		)
	}
}

func TestPersistentSessionConfigKeyBindsSourceArchiveReference(t *testing.T) {
	base := ActionCommand{
		ClassName:        "Actions",
		SourceArchiveRef: "archive-a",
		VUserID:          "17",
		GroupName:        "Checkout",
	}
	changed := base
	changed.SourceArchiveRef = "archive-b"
	if persistentSessionConfigKey(RuntimeSettings{}, base) ==
		persistentSessionConfigKey(RuntimeSettings{}, changed) {
		t.Fatal("persistent session identity ignored source_archive_ref")
	}
}

func TestServerHealthCapabilitiesAndExecution(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	server := httptest.NewServer(fixture.server(2))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "server-health-test")

	health, err := client.Health(context.Background())
	if err != nil || health.Status != "healthy" {
		t.Fatalf("Health() = %#v, %v", health, err)
	}
	capabilities, err := client.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Healthy || capabilities.JavaMajor != 21 || capabilities.MaxProcesses != 2 {
		t.Fatalf("Capabilities() = %#v", capabilities)
	}
	if !slices.Contains(capabilities.Features, FeaturePhaseLifecycleV1) ||
		!slices.Contains(capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		!slices.Contains(capabilities.Features, FeatureSessionFrameTokenV1) ||
		slices.Contains(capabilities.Features, WarningModernXBootClasspathFallback) {
		t.Fatalf("Capabilities() lifecycle/warning features = %#v", capabilities.Features)
	}

	vmToken := "-Ddatabase.password=imported-password"
	response, err := client.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "exec-1",
		Runtime: RuntimeSettings{
			JDKSelection:           "specified",
			JDKHome:                fixture.jdkHome,
			AdditionalVMParameters: []string{vmToken, "-Xmx128m"},
			Classpath:              []string{fixture.scriptClasspath},
			UseXBootClasspath:      true,
			DedicatedClassLoader:   true,
			StartupTimeoutMS:       250,
		},
		Command: ActionCommand{
			ActionName:      "Action",
			ActionType:      "java_vuser_run",
			ClassName:       "Actions",
			Method:          "full_lifecycle",
			Params:          map[string]any{"parameters": map[string]any{"region": "east"}},
			VUserID:         "7",
			GroupName:       "Java Group",
			IgnoreThinkTime: "true",
		},
		TimeoutMS: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.ErrorCode != "" {
		t.Fatalf("Execute() = %#v", response)
	}
	if !slices.Equal(response.WarningCodes, []string{WarningModernXBootClasspathFallback}) {
		t.Fatalf("warning codes = %v", response.WarningCodes)
	}
	if response.Events != "{\"type\":\"transaction\"}\n" || response.Stderr != "fixture-stderr\n" {
		t.Fatalf("captured streams = events %q stderr %q", response.Events, response.Stderr)
	}

	args := readLines(t, fixture.argsFile)
	if len(args) < 9 || args[0] != vmToken || args[1] != "-Xmx128m" ||
		args[2] != "-XX:+DisableAttachMechanism" ||
		args[3] != "-Dcom.ibm.tools.attach.enable=no" ||
		args[4] != "-XX:-HeapDumpOnOutOfMemoryError" ||
		args[5] != "-XX:-CreateCoredumpOnCrash" {
		t.Fatalf("launcher args = %#v; exact VM tokens were not first", args)
	}
	for _, argument := range args {
		if strings.HasPrefix(strings.ToLower(argument), "-xbootclasspath") {
			t.Fatalf("modern fallback emitted forbidden Xboot argument %q", argument)
		}
	}
	classpathIndex := slices.Index(args, "-cp")
	canonicalHarness := canonicalTestPath(t, fixture.harnessJar)
	if classpathIndex < 0 || classpathIndex+1 >= len(args) || args[classpathIndex+1] != canonicalHarness {
		t.Fatalf("dedicated system classpath args = %#v, want harness only", args)
	}
	var payload struct {
		UserClasspath   []string       `json:"user_classpath"`
		DedicatedLoader bool           `json:"dedicated_class_loader"`
		Params          map[string]any `json:"params"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(mustReadFile(t, fixture.stdinFile)), &payload); err != nil {
		t.Fatal(err)
	}
	wantUserClasspath := []string{canonicalTestPath(t, fixture.scriptClasspath), canonicalTestPath(t, fixture.sharedClasspath)}
	if !payload.DedicatedLoader || !slices.Equal(payload.UserClasspath, wantUserClasspath) {
		t.Fatalf("harness payload = %#v", payload)
	}
	canonicalJDKHome := canonicalTestPath(t, fixture.jdkHome)
	if got := strings.TrimSpace(string(mustReadFile(t, fixture.javaHomeFile))); got != canonicalJDKHome {
		t.Fatalf("JAVA_HOME = %q, want %q", got, canonicalJDKHome)
	}
}

func TestRuntimeLeaseExpiryReapsOnlyAbandonedRunWhileSiblingRenews(t *testing.T) {
	started := filepath.Join(t.TempDir(), "run-a-started")
	fixture := makeSidecarFixture(t, `
if printf '%s' "$payload" | grep -q 'run-a-blocked'; then
  touch `+shellQuote(started)+`
  sleep 30
fi`)
	implementation := fixture.server(2)
	server := httptest.NewServer(implementation)
	defer server.Close()

	runA := newClaimedTestClient(t, server.URL, server.Client(), "shared-generator")
	runB, err := NewClientWithToken(server.URL, testSidecarAuthToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runB.ClaimOwner(context.Background(), "shared-generator"); err != nil {
		t.Fatal(err)
	}
	if _, err := runB.ClaimRuntimeLease(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopRunBRenewal := keepTestOwnerLease(runB, "shared-generator")
	defer stopRunBRenewal()

	runADone := make(chan error, 1)
	go func() {
		_, executeErr := runA.Execute(context.Background(), fixture.executionRequest("run-a-blocked"))
		runADone <- executeErr
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, statErr := os.Stat(started); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run A worker did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Model a lost cleanup transport after run A's keeper has stopped. Run B
	// retains and renews the same process owner, so only the independent A lease
	// can fence the abandoned worker.
	implementation.runtimeLeaseMu.Lock()
	abandoned := implementation.runtimeLeases[runA.leaseID]
	if abandoned == nil {
		implementation.runtimeLeaseMu.Unlock()
		t.Fatal("run A runtime lease was not registered")
	}
	abandoned.deadline = time.Now().Add(100 * time.Millisecond)
	implementation.armRuntimeLeaseLocked(abandoned)
	implementation.runtimeLeaseMu.Unlock()
	if _, err := runB.RenewRuntimeLease(context.Background()); err != nil {
		t.Fatalf("run B renewal failed while run A was abandoned: %v", err)
	}

	select {
	case <-abandoned.cleanupDone:
		if abandoned.cleanupErr != nil {
			t.Fatalf("run A cleanup failed: %v", abandoned.cleanupErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run A was not fenced and positively reaped by its lease")
	}
	select {
	case <-runADone:
	case <-time.After(time.Second):
		t.Fatal("run A execution remained live after lease reap")
	}
	if implementation.containmentUnavailable() {
		t.Fatal("cooperative run A cleanup poisoned the sidecar")
	}
	if _, err := runB.Execute(context.Background(), fixture.executionRequest("run-b-continues")); err != nil {
		t.Fatalf("run B did not remain usable after run A reap: %v", err)
	}
	if _, err := runB.RevokeRuntimeLease(context.Background()); err != nil {
		t.Fatalf("run B cleanup: %v", err)
	}
}

func TestRuntimeLeaseCleanupDeadlineSignalsTerminalContainmentFailure(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator:                    config.JavaVuserOperatorConfig{Enabled: true, AuthToken: testSidecarAuthToken},
		runtimeLeaseCleanupDeadline: 25 * time.Millisecond,
	})
	lease := &runtimeLease{
		id: "runtime-stuck", token: strings.Repeat("b", 64), state: runtimeLeaseDraining,
		cleanupDone: make(chan struct{}),
	}
	lease.operations.Add(1)
	implementation.runtimeLeaseMu.Lock()
	implementation.runtimeLeases[lease.id] = lease
	implementation.runtimeLeaseMu.Unlock()
	implementation.startRuntimeLeaseCleanup(lease)
	select {
	case <-implementation.ContainmentFailed():
	case <-time.After(time.Second):
		t.Fatal("runtime cleanup watchdog did not signal terminal containment failure")
	}
	lease.operations.Done()
	select {
	case <-lease.cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("runtime cleanup did not finish after the blocked operation released")
	}
}

func TestOpaqueLeaseCredentialsFailClosedWithoutSecureRandomness(t *testing.T) {
	wantErr := errors.New("entropy unavailable")
	if token, err := newOpaqueToken(func([]byte) (int, error) { return 0, wantErr }); token != "" || !errors.Is(err, wantErr) {
		t.Fatalf("newOpaqueToken() = %q, %v; want empty token and entropy error", token, err)
	}
	if token, err := newOpaqueToken(func(buffer []byte) (int, error) {
		buffer[0] = 1
		return 1, nil
	}); token != "" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("newOpaqueToken(short read) = %q, %v; want fail-closed unexpected EOF", token, err)
	}
	implementation := NewServer(ServerOptions{
		Operator:    config.JavaVuserOperatorConfig{Enabled: true, AuthToken: testSidecarAuthToken},
		randomBytes: func([]byte) (int, error) { return 0, wantErr },
	})
	body := strings.NewReader(`{"contract":"` + ContractVersion + `","owner_id":"generator-a","lease_ttl_ms":5000}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/owner/claim", body)
	request.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
	response := httptest.NewRecorder()
	implementation.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "owner_identity_unavailable") {
		t.Fatalf("owner claim without entropy = %d %s", response.Code, response.Body.String())
	}
}

func TestServerEnablementAuthenticationAndProbeBoundaries(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	executionBody := strings.NewReader(`{"contract":"` + ContractVersion + `","execution_id":"blocked","runtime":{},"command":{"class_name":"Actions"}}`)

	disabled := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: false, AuthToken: testSidecarAuthToken,
		},
		CredentialCheck: func() error { return nil },
	})
	recorder := httptest.NewRecorder()
	disabled.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("disabled /livez status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	disabled.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable ||
		!strings.Contains(recorder.Body.String(), "runner_disabled") {
		t.Fatalf("disabled /readyz = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	disabled.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/executions", executionBody))
	if recorder.Code != http.StatusServiceUnavailable ||
		!strings.Contains(recorder.Body.String(), "runner_disabled") {
		t.Fatalf("disabled execute = %d %s", recorder.Code, recorder.Body.String())
	}

	missingAuth := NewServer(ServerOptions{
		Operator:        config.JavaVuserOperatorConfig{Enabled: true},
		CredentialCheck: func() error { return nil },
	})
	recorder = httptest.NewRecorder()
	missingAuth.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable ||
		!strings.Contains(recorder.Body.String(), "runner_auth_unavailable") {
		t.Fatalf("missing-auth /readyz = %d %s", recorder.Code, recorder.Body.String())
	}

	enabled := fixture.server(1)
	recorder = httptest.NewRecorder()
	enabled.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodPost,
			"/v1/executions",
			strings.NewReader(`{"contract":"`+ContractVersion+`","execution_id":"unauthorized","runtime":{},"command":{"class_name":"Actions"}}`),
		),
	)
	if recorder.Code != http.StatusUnauthorized ||
		!strings.Contains(recorder.Body.String(), "runner_unauthorized") {
		t.Fatalf("unauthenticated execute = %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(fixture.startedFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected request started a JVM: %v", err)
	}
}

func TestReadinessIsCachedAndPreservesPersistentState(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	var checkMu sync.Mutex
	credentialChecks := 0
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled:        true,
			AuthToken:      testSidecarAuthToken,
			DefaultJDKHome: fixture.jdkHome,
			HarnessJar:     fixture.harnessJar,
			Classpath:      []string{fixture.sharedClasspath},
			MaxProcesses:   1,
		},
		CredentialCheck: func() error {
			checkMu.Lock()
			credentialChecks++
			checkMu.Unlock()
			return nil
		},
	})
	generation := &ownerGeneration{ownerID: "probe-owner", epoch: "probe-epoch", ready: true, leaseDeadline: time.Now().Add(time.Hour)}
	session := &persistentSession{generation: generation}
	implementation.owner = generation
	implementation.sessions["probe-session"] = session
	implementation.allSessions[session] = generation
	implementation.slots <- struct{}{}

	server := httptest.NewServer(implementation)
	defer server.Close()
	for index := 0; index < 2; index++ {
		response, err := server.Client().Get(server.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("/readyz attempt %d status = %d", index, response.StatusCode)
		}
	}
	checkMu.Lock()
	checks := credentialChecks
	checkMu.Unlock()
	if checks != 1 {
		t.Fatalf("credential readiness checks = %d, want one cached dependency probe", checks)
	}
	implementation.sessionsMu.Lock()
	preserved := implementation.sessions["probe-session"] == session &&
		implementation.allSessions[session] == generation
	implementation.sessionsMu.Unlock()
	if !preserved || len(implementation.slots) != 1 {
		t.Fatalf("readiness probe changed persistent state: preserved=%v slots=%d", preserved, len(implementation.slots))
	}
}

func TestReadinessFailureUsesShortCache(t *testing.T) {
	var checkMu sync.Mutex
	checks := 0
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken,
		},
		CredentialCheck: func() error {
			checkMu.Lock()
			checks++
			checkMu.Unlock()
			return errors.New("credential isolation failed")
		},
	})
	for index := 0; index < 2; index++ {
		recorder := httptest.NewRecorder()
		implementation.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if recorder.Code != http.StatusServiceUnavailable ||
			!strings.Contains(recorder.Body.String(), "ambient_credentials_detected") {
			t.Fatalf("/readyz failure %d = %d %s", index, recorder.Code, recorder.Body.String())
		}
	}
	checkMu.Lock()
	got := checks
	checkMu.Unlock()
	if got != 1 {
		t.Fatalf("failed readiness dependency checks = %d, want one within failure TTL", got)
	}
}

func TestOwnerClaimQueuedBehindDrainIsRejected(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken,
		},
		CredentialCheck: func() error { return nil },
	})
	implementation.claimMu.Lock()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/owner/claim",
			strings.NewReader(`{"contract":"`+ContractVersion+`","owner_id":"queued-owner"}`),
		)
		request.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
		recorder := httptest.NewRecorder()
		implementation.ServeHTTP(recorder, request)
		done <- recorder
	}()
	time.Sleep(10 * time.Millisecond)
	implementation.draining.Store(true)
	implementation.claimMu.Unlock()

	select {
	case recorder := <-done:
		if recorder.Code != http.StatusServiceUnavailable ||
			!strings.Contains(recorder.Body.String(), "server_draining") {
			t.Fatalf("queued owner claim = %d %s", recorder.Code, recorder.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("queued owner claim did not finish")
	}
}

func TestShutdownDrainsOwnerOperationsAndRejectsNewWork(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken,
		},
		CredentialCheck: func() error { return nil },
	})
	generation := &ownerGeneration{ownerID: "shutdown-owner", epoch: "shutdown-epoch", ready: true, leaseDeadline: time.Now().Add(time.Hour)}
	implementation.owner = generation
	active, ownerError := implementation.beginOwnerOperation(generation.ownerID, generation.epoch)
	if ownerError != "" || active != generation {
		t.Fatalf("beginOwnerOperation = %#v, %q", active, ownerError)
	}
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- implementation.Shutdown(context.Background())
	}()
	deadline := time.Now().Add(time.Second)
	for !implementation.draining.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !implementation.draining.Load() {
		t.Fatal("Shutdown did not enter draining state")
	}
	if _, code := implementation.beginOwnerOperation(generation.ownerID, generation.epoch); code != "server_draining" {
		t.Fatalf("new work during drain code = %q, want server_draining", code)
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before active owner operation completed: %v", err)
	default:
	}
	generation.operations.Done()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after owner operation completed")
	}
}

func TestOwnerTakeoverCleansIdleExitedSessionCaptures(t *testing.T) {
	implementation, server, session := startIdleExitedSessionForCleanupTest(
		t,
		"idle-exit-old-owner",
		"idle-exit-takeover-session",
	)
	replacement, err := NewClientWithToken(server.URL, testSidecarAuthToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replacement.ClaimOwner(context.Background(), "idle-exit-new-owner"); err != nil {
		t.Fatalf("replacement ClaimOwner: %v", err)
	}
	assertSessionCapturesRemoved(t, session)
	implementation.sessionsMu.Lock()
	_, retained := implementation.allSessions[session]
	implementation.sessionsMu.Unlock()
	if retained {
		t.Fatal("owner takeover retained the idle-exited session")
	}
}

func TestShutdownCleansIdleExitedSessionCaptures(t *testing.T) {
	implementation, _, session := startIdleExitedSessionForCleanupTest(
		t,
		"idle-exit-shutdown-owner",
		"idle-exit-shutdown-session",
	)
	if err := implementation.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	assertSessionCapturesRemoved(t, session)
	implementation.sessionsMu.Lock()
	_, retained := implementation.allSessions[session]
	implementation.sessionsMu.Unlock()
	if retained {
		t.Fatal("Shutdown retained the idle-exited session")
	}
}

func TestShutdownDeadlineDoesNotBlockOnLockedSessionCleanup(t *testing.T) {
	implementation, _, session := startLiveSessionForBoundedShutdownTest(t)
	implementation.ownerMu.Lock()
	owner := implementation.owner
	implementation.ownerMu.Unlock()
	if owner == nil {
		t.Fatal("live cleanup fixture has no claimed owner")
	}
	active, ownerError := implementation.beginOwnerOperation(owner.ownerID, owner.epoch)
	if ownerError != "" || active != owner {
		t.Fatalf("beginOwnerOperation = %#v, %q", active, ownerError)
	}
	session.invokeMu.Lock()
	released := false
	defer func() {
		if released {
			return
		}
		session.invokeMu.Unlock()
		owner.operations.Done()
		session.closeStdin()
		_ = session.killTree()
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- implementation.Shutdown(shutdownCtx)
	}()
	var shutdownErr error
	blockedPastDeadline := false
	select {
	case shutdownErr = <-shutdownDone:
	case <-time.After(time.Second):
		blockedPastDeadline = true
	}

	// Simulate the active invocation/close finishing after Shutdown's caller
	// has already received its deadline. Background cleanup must then reap the
	// process, release its slot, and remove both capture files.
	owner.operations.Done()
	session.invokeMu.Unlock()
	released = true
	session.closeStdin()
	_ = session.killTree()

	if blockedPastDeadline {
		select {
		case <-shutdownDone:
		case <-time.After(5 * time.Second):
			t.Fatal("Shutdown remained blocked after the active session was released")
		}
		t.Fatal("Shutdown blocked on invokeMu after its context deadline")
	}
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context deadline exceeded", shutdownErr)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		implementation.sessionsMu.Lock()
		_, tracked := implementation.allSessions[session]
		implementation.sessionsMu.Unlock()
		_, eventErr := os.Stat(session.eventPath)
		_, diagnosticErr := os.Stat(session.diagnosticPath)
		if len(implementation.slots) == 0 && !tracked &&
			errors.Is(eventErr, os.ErrNotExist) && errors.Is(diagnosticErr, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"asynchronous cleanup incomplete: slots=%d tracked=%v event=%v diagnostic=%v",
				len(implementation.slots), tracked, eventErr, diagnosticErr,
			)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestShutdownDeadlineIsIndependentOfBlockingCleanupKill(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck: func() error { return nil },
	})
	generation := &ownerGeneration{ownerID: "blocking-kill-owner", epoch: "blocking-kill-epoch", ready: true, leaseDeadline: time.Now().Add(time.Hour)}
	implementation.owner = generation
	eventPath := filepath.Join(t.TempDir(), "blocking-kill-events.log")
	diagnosticPath := filepath.Join(t.TempDir(), "blocking-kill-diagnostics.log")
	for _, path := range []string{eventPath, diagnosticPath} {
		if err := os.WriteFile(path, []byte("capture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	killStarted := make(chan struct{})
	releaseKill := make(chan struct{})
	var releaseKillOnce sync.Once
	releaseBlockedKill := func() {
		releaseKillOnce.Do(func() { close(releaseKill) })
	}
	defer releaseBlockedKill()
	session := &persistentSession{
		waitDone:       make(chan struct{}),
		eventPath:      eventPath,
		diagnosticPath: diagnosticPath,
		generation:     generation,
	}
	session.killFunc = func(*exec.Cmd) error {
		close(killStarted)
		<-releaseKill
		<-implementation.slots
		close(session.waitDone)
		return nil
	}
	session.stdin = &callbackWriteCloser{close: session.killTree}
	implementation.sessions["blocking-kill-session"] = session
	implementation.allSessions[session] = generation
	implementation.slots <- struct{}{}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- implementation.Shutdown(shutdownCtx)
	}()
	select {
	case <-killStarted:
	case <-time.After(time.Second):
		t.Fatal("shared cleanup did not reach the blocking process kill")
	}
	var shutdownErr error
	select {
	case shutdownErr = <-shutdownDone:
	case <-time.After(time.Second):
		releaseBlockedKill()
		t.Fatal("blocking process kill held Shutdown past its context deadline")
	}
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context deadline exceeded", shutdownErr)
	}

	releaseBlockedKill()
	select {
	case <-implementation.shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shared cleanup did not finish after the process kill was released")
	}
	assertSessionCapturesRemoved(t, session)
	if occupied := len(implementation.slots); occupied != 0 {
		t.Fatalf("blocking-kill cleanup retained %d capacity slots", occupied)
	}
}

func TestRepeatedTimedOutShutdownCallsShareOneDrainWorker(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken,
		},
		CredentialCheck: func() error { return nil },
	})
	generation := &ownerGeneration{ownerID: "coalesced-owner", epoch: "coalesced-epoch", ready: true, leaseDeadline: time.Now().Add(time.Hour)}
	implementation.owner = generation
	active, ownerError := implementation.beginOwnerOperation(generation.ownerID, generation.epoch)
	if ownerError != "" || active != generation {
		t.Fatalf("beginOwnerOperation = %#v, %q", active, ownerError)
	}
	released := false
	defer func() {
		if !released {
			generation.operations.Done()
		}
	}()

	baseline := runtime.NumGoroutine()
	const attempts = 24
	for attempt := 0; attempt < attempts; attempt++ {
		callerCtx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := implementation.Shutdown(callerCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Shutdown attempt %d error = %v, want context canceled", attempt, err)
		}
	}
	// Give any incorrectly per-call drain/cleanup goroutines time to park.
	time.Sleep(25 * time.Millisecond)
	if growth := runtime.NumGoroutine() - baseline; growth > 6 {
		t.Fatalf("repeated timed-out Shutdown calls added %d goroutines; cleanup was not coalesced", growth)
	}
	select {
	case <-implementation.shutdownDone:
		t.Fatal("shared shutdown completed while its owner operation remained active")
	default:
	}

	generation.operations.Done()
	released = true
	select {
	case <-implementation.shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shared shutdown did not finish after the owner operation was released")
	}
	if err := implementation.Shutdown(context.Background()); err != nil {
		t.Fatalf("idempotent completed Shutdown: %v", err)
	}
}

func TestFailedContainmentQuarantinesSidecarProcessCapacity(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck: func() error { return nil },
	})
	release, failure := implementation.acquireProcessSlot(context.Background())
	if failure != nil {
		t.Fatalf("acquire initial process slot: %#v", failure)
	}
	blockedAcquire := make(chan *safeFailure, 1)
	go func() {
		_, failure := implementation.acquireProcessSlot(context.Background())
		blockedAcquire <- failure
	}()
	select {
	case failure := <-blockedAcquire:
		t.Fatalf("capacity waiter returned before containment failed: %#v", failure)
	case <-time.After(10 * time.Millisecond):
	}
	implementation.finishProcessCapacity(release, errors.New("descendant tree remained populated"))
	select {
	case failure := <-blockedAcquire:
		if failure == nil || failure.code != "process_containment_unavailable" {
			t.Fatalf("blocked acquire failure = %#v, want process_containment_unavailable", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked capacity waiter did not observe terminal containment failure")
	}

	if occupied := len(implementation.slots); occupied != 1 {
		t.Fatalf("failed containment left %d occupied slots, want quarantined capacity 1", occupied)
	}
	secondCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	if _, failure := implementation.acquireProcessSlot(secondCtx); failure == nil ||
		failure.code != "process_containment_unavailable" {
		t.Fatalf("second acquire failure = %#v, want process_containment_unavailable", failure)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("failed-containment acquire waited %s instead of failing closed", elapsed)
	}
	if _, failure := implementation.health(context.Background()); failure == nil ||
		failure.code != "process_containment_unavailable" {
		t.Fatalf("health after containment failure = %#v, want unavailable", failure)
	}
}

func TestPersistentKillFailureFailsClosedWithoutWaitingForeverForReap(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck: func() error { return nil },
	})
	release, failure := implementation.acquireProcessSlot(context.Background())
	if failure != nil {
		t.Fatalf("acquire initial process slot: %#v", failure)
	}
	generation := &ownerGeneration{ownerID: "failed-kill-owner", epoch: "failed-kill-epoch", ready: true, leaseDeadline: time.Now().Add(time.Hour)}
	eventPath := filepath.Join(t.TempDir(), "failed-kill-events.log")
	diagnosticPath := filepath.Join(t.TempDir(), "failed-kill-diagnostics.log")
	for _, path := range []string{eventPath, diagnosticPath} {
		if err := os.WriteFile(path, []byte("capture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	waitDone := make(chan struct{})
	session := &persistentSession{
		waitDone:           waitDone,
		eventPath:          eventPath,
		diagnosticPath:     diagnosticPath,
		generation:         generation,
		stdoutNoise:        &lockedCappedBuffer{buf: cappedBuffer{max: MaxCaptureBytes}},
		stderrNoise:        &lockedCappedBuffer{buf: cappedBuffer{max: MaxCaptureBytes}},
		failedKillReapWait: 5 * time.Millisecond,
		containmentFailure: func(err error) {
			implementation.finishProcessCapacity(release, err)
		},
		killFunc: func(*exec.Cmd) error {
			return errors.New("strong process-tree kill failed")
		},
	}
	implementation.owner = generation
	implementation.sessions["failed-kill-session"] = session
	implementation.allSessions[session] = generation

	outcomeDone := make(chan processOutcome, 1)
	go func() {
		outcomeDone <- session.failAndReap("execution_timeout")
	}()
	var outcome processOutcome
	select {
	case outcome = <-outcomeDone:
	case <-time.After(time.Second):
		t.Fatal("persistent action waited indefinitely for an unreaped JVM after kill failure")
	}
	if outcome.errorCode != "execution_timeout" ||
		!outcome.stdoutTruncated || !outcome.stderrTruncated || !outcome.eventsTruncated {
		t.Fatalf("bounded failed-kill outcome = %#v", outcome)
	}
	if !implementation.containmentFailed.Load() {
		t.Fatal("kill failure did not immediately mark the sidecar runtime unavailable")
	}
	if occupied := len(implementation.slots); occupied != 1 {
		t.Fatalf("kill failure left %d occupied slots, want one quarantined token", occupied)
	}
	implementation.poisonSession("failed-kill-session", session)
	implementation.sessionsMu.Lock()
	_, trackedBeforeReap := implementation.allSessions[session]
	implementation.sessionsMu.Unlock()
	if !trackedBeforeReap || !implementation.sessionUnusable("failed-kill-session", generation) {
		t.Fatal("unreaped poisoned session lost ownership or its unusable tombstone")
	}

	close(waitDone)
	deadline := time.Now().Add(time.Second)
	for {
		implementation.sessionsMu.Lock()
		_, tracked := implementation.allSessions[session]
		implementation.sessionsMu.Unlock()
		if !tracked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("eventual reap did not retire the poisoned session")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(eventPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("event capture remained after eventual reap: %v", err)
	}
	if _, err := os.Stat(diagnosticPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("diagnostic capture remained after eventual reap: %v", err)
	}
}

func TestPersistentSessionCloseReportsContainmentFailure(t *testing.T) {
	waitDone := make(chan struct{})
	close(waitDone)
	session := &persistentSession{
		waitDone: waitDone,
		killErr: errors.Join(
			javarunner.ErrProcessContainmentFailed,
			errors.New("whole-tree cleanup failed"),
		),
	}
	failure := session.close(context.Background())
	if failure == nil || failure.code != "process_containment_unavailable" {
		t.Fatalf("persistentSession.close() = %#v, want containment failure", failure)
	}
}

func TestPersistentSessionFailureIncludesJVMExitStatus(t *testing.T) {
	waitDone := make(chan struct{})
	close(waitDone)
	tempDir := t.TempDir()
	eventPath := filepath.Join(tempDir, "events.log")
	diagnosticPath := filepath.Join(tempDir, "diagnostics.log")
	for _, path := range []string{eventPath, diagnosticPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	session := &persistentSession{
		waitDone:       waitDone,
		waitErr:        errors.New("signal: killed"),
		eventPath:      eventPath,
		diagnosticPath: diagnosticPath,
		stdoutNoise:    &lockedCappedBuffer{buf: cappedBuffer{max: MaxCaptureBytes}},
		stderrNoise:    &lockedCappedBuffer{buf: cappedBuffer{max: MaxCaptureBytes}},
		killFunc:       func(*exec.Cmd) error { return nil },
	}
	outcome := session.failAndReap("session_unusable")
	if !strings.Contains(outcome.stderr, "signal: killed") {
		t.Fatalf("persistent failure stderr = %q, want JVM exit status", outcome.stderr)
	}
}

func TestOwnerClaimRejectsContainmentFailureWithoutWaitingForReap(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck: func() error { return nil },
	})
	release, failure := implementation.acquireProcessSlot(context.Background())
	if failure != nil {
		t.Fatalf("acquire initial process slot: %#v", failure)
	}
	generation := &ownerGeneration{ownerID: "old-owner", epoch: "old-epoch", ready: true, leaseDeadline: time.Now().Add(time.Hour)}
	waitDone := make(chan struct{})
	session := &persistentSession{
		waitDone:     waitDone,
		generation:   generation,
		closeTimeout: 5 * time.Millisecond,
		containmentFailure: func(err error) {
			implementation.finishProcessCapacity(release, err)
		},
		killFunc: func(*exec.Cmd) error {
			return errors.New("strong process-tree kill failed")
		},
	}
	implementation.owner = generation
	implementation.sessions["failed-owner-session"] = session
	implementation.allSessions[session] = generation

	body := strings.NewReader(`{"contract":"` + ContractVersion + `","owner_id":"new-owner"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/owner/claim", body)
	request.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
	recorder := httptest.NewRecorder()
	handlerDone := make(chan struct{})
	go func() {
		implementation.ServeHTTP(recorder, request)
		close(handlerDone)
	}()
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("owner claim waited indefinitely for an unreaped JVM after containment failure")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("owner claim status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode owner-claim response: %v", err)
	}
	if response.ErrorCode != "owner_claim_failed" {
		t.Fatalf("owner-claim error = %q, want owner_claim_failed", response.ErrorCode)
	}
	if !implementation.containmentFailed.Load() || len(implementation.slots) != 1 {
		t.Fatalf("owner claim did not retain terminal quarantine: failed=%v slots=%d", implementation.containmentFailed.Load(), len(implementation.slots))
	}
	close(waitDone)
	session.cleanup()
	implementation.evictSessionObject(session)
}

func TestBackgroundShutdownWaitsForDelayedReapBeforeCompletion(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 1,
		},
		CredentialCheck: func() error { return nil },
	})
	generation := &ownerGeneration{ownerID: "delayed-reap-owner", epoch: "delayed-reap-epoch", ready: true, leaseDeadline: time.Now().Add(time.Hour)}
	implementation.owner = generation
	eventPath := filepath.Join(t.TempDir(), "delayed-reap-events.log")
	diagnosticPath := filepath.Join(t.TempDir(), "delayed-reap-diagnostics.log")
	for _, path := range []string{eventPath, diagnosticPath} {
		if err := os.WriteFile(path, []byte("capture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	release, failure := implementation.acquireProcessSlot(context.Background())
	if failure != nil {
		t.Fatalf("acquire delayed-reap slot: %#v", failure)
	}
	killStarted := make(chan struct{})
	allowKillReturn := make(chan struct{})
	killReturned := make(chan struct{})
	session := &persistentSession{
		stdin:          &callbackWriteCloser{},
		waitDone:       make(chan struct{}),
		eventPath:      eventPath,
		diagnosticPath: diagnosticPath,
		generation:     generation,
		closeTimeout:   5 * time.Millisecond,
	}
	session.killFunc = func(*exec.Cmd) error {
		close(killStarted)
		<-allowKillReturn
		close(killReturned)
		return nil
	}
	implementation.sessions["delayed-reap-session"] = session
	implementation.allSessions[session] = generation

	shutdownResult := make(chan error, 1)
	go func() {
		shutdownResult <- implementation.Shutdown(context.Background())
	}()
	select {
	case <-killStarted:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not initiate delayed-reap process termination")
	}
	close(allowKillReturn)
	select {
	case <-killReturned:
	case <-time.After(time.Second):
		t.Fatal("test process-tree kill did not return")
	}
	select {
	case err := <-shutdownResult:
		t.Fatalf("Shutdown completed before cmd.Wait/capacity release: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	implementation.sessionsMu.Lock()
	_, trackedBeforeReap := implementation.allSessions[session]
	implementation.sessionsMu.Unlock()
	if !trackedBeforeReap || len(implementation.slots) != 1 {
		t.Fatalf(
			"shutdown dropped delayed-reap ownership early: tracked=%v slots=%d",
			trackedBeforeReap,
			len(implementation.slots),
		)
	}

	implementation.finishProcessCapacity(release, nil)
	close(session.waitDone)
	select {
	case err := <-shutdownResult:
		if err == nil {
			t.Fatal("Shutdown hid the graceful-close timeout")
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not complete after delayed cmd.Wait")
	}
	assertSessionCapturesRemoved(t, session)
	implementation.sessionsMu.Lock()
	_, trackedAfterReap := implementation.allSessions[session]
	implementation.sessionsMu.Unlock()
	if trackedAfterReap || len(implementation.slots) != 0 {
		t.Fatalf(
			"shutdown cleanup incomplete after delayed reap: tracked=%v slots=%d",
			trackedAfterReap,
			len(implementation.slots),
		)
	}
}

func TestShutdownCancelsAndWaitsForQueuedValidationProbe(t *testing.T) {
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled: true, AuthToken: testSidecarAuthToken,
		},
		CredentialCheck: func() error { return nil },
	})
	implementation.probeSlots <- struct{}{}
	defer func() { <-implementation.probeSlots }()

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/validate",
		strings.NewReader(`{"contract":"`+ContractVersion+`","runtime":{},"timeout_ms":30000}`),
	)
	request.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
	responseDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		implementation.ServeHTTP(recorder, request)
		responseDone <- recorder
	}()
	deadline := time.Now().Add(time.Second)
	for {
		implementation.operationMu.Lock()
		active := len(implementation.operationCancels)
		implementation.operationMu.Unlock()
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("validation probe was not admitted")
		}
		time.Sleep(time.Millisecond)
	}

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- implementation.Shutdown(context.Background())
	}()
	select {
	case recorder := <-responseDone:
		if recorder.Code != http.StatusOK ||
			!strings.Contains(recorder.Body.String(), "execution_canceled") {
			t.Fatalf("drained validation = %d %s", recorder.Code, recorder.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("draining did not cancel the queued validation probe")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not wait for validation handler cleanup")
	}
	if release, failure := implementation.acquireProbeSlot(context.Background()); release != nil ||
		failure == nil || failure.code != "server_draining" {
		t.Fatalf("acquireProbeSlot after drain = release:%v failure:%#v", release != nil, failure)
	}
}

func TestValidateDoesNotLeakForbiddenVMParameter(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	const secret = "resolved-secret-value"
	body := fmt.Sprintf(`{"contract":%q,"runtime":{"jdk_selection":"specified","jdk_home":%q,"additional_vm_parameters":["-javaagent:/tmp/%s.jar"]}}`, ContractVersion, fixture.jdkHome, secret)
	response, err := authenticatedPost(server.Client(), server.URL+"/v1/validate", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if strings.Contains(string(data), secret) {
		t.Fatalf("validation response leaked VM parameter: %s", data)
	}
	var result ValidateResponse
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Valid || len(result.Issues) != 1 || result.Issues[0].Code != "vm_parameter_agent_hook" || result.Issues[0].Index == nil || *result.Issues[0].Index != 0 {
		t.Fatalf("validation response = %#v", result)
	}
}

func TestHealthDoesNotLeakCredentialCheckDetails(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	const secret = "ambient-secret-value"
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled:        true,
			AuthToken:      testSidecarAuthToken,
			DefaultJDKHome: fixture.jdkHome,
			HarnessJar:     fixture.harnessJar,
		},
		CredentialCheck: func() error { return errors.New(secret) },
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	recorder := httptest.NewRecorder()
	implementation.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), secret) ||
		!strings.Contains(recorder.Body.String(), "ambient_credentials_detected") {
		t.Fatalf("health response = status %d body %s", recorder.Code, recorder.Body.String())
	}
}

func TestValidateLoadsTargetsWithoutExecutingThem(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "validate-target-test")
	response, err := client.Validate(context.Background(), ValidateRequest{
		Runtime: RuntimeSettings{
			JDKSelection: "specified", JDKHome: fixture.jdkHome,
			Classpath: []string{fixture.scriptClasspath}, DedicatedClassLoader: true,
		},
		Targets: []Target{{ClassName: "Actions", Method: "action"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Valid || response.JavaMajor != 21 {
		t.Fatalf("Validate() = %#v", response)
	}
	payload := string(mustReadFile(t, fixture.stdinFile))
	if !strings.Contains(payload, `"validation_only":true`) {
		t.Fatalf("validation payload = %s", payload)
	}
}

func TestValidationIssueDistinguishesBatchTimeoutFromTargetRejection(t *testing.T) {
	active := validationIssueForOutcome(context.Background(), 2)
	if active.Code != "target_validation_failed" || active.Index == nil || *active.Index != 2 {
		t.Fatalf("active validation issue = %#v", active)
	}
	canceled, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	timedOut := validationIssueForOutcome(canceled, 3)
	if timedOut.Code != "validation_timeout" || timedOut.Index == nil || *timedOut.Index != 3 {
		t.Fatalf("timed-out validation issue = %#v", timedOut)
	}
}

func TestServerRejectsUnknownOperatorFieldsAndOversizedRequests(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()

	unknown := fmt.Sprintf(`{"contract":%q,"runtime":{},"runner_command":"/tmp/evil"}`, ContractVersion)
	response, err := authenticatedPost(server.Client(), server.URL+"/v1/validate", strings.NewReader(unknown))
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusBadRequest, "invalid_request")

	oversized := `{"contract":"` + strings.Repeat("x", int(MaxRequestBytes)) + `"}`
	response, err = authenticatedPost(server.Client(), server.URL+"/v1/validate", strings.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusRequestEntityTooLarge, "request_too_large")

	wrongContract := `{"contract":"apexload-java-vuser-runner.v2","runtime":{}}`
	response, err = authenticatedPost(server.Client(), server.URL+"/v1/validate", strings.NewReader(wrongContract))
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusPreconditionFailed, "contract_version_mismatch")
}

func TestValidationFailsClosedOnHarnessVersionSkew(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	launcher := filepath.Join(fixture.jdkHome, "bin", "java")
	script := strings.ReplaceAll(string(mustReadFile(t, launcher)), harnessIPCContract, "apexload.java_vuser.ipc.v99")
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "version-skew-test")
	response, err := client.Validate(context.Background(), ValidateRequest{
		Runtime: RuntimeSettings{JDKSelection: "specified", JDKHome: fixture.jdkHome},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Valid || len(response.Issues) != 1 || response.Issues[0].Code != "harness_health_failed" {
		t.Fatalf("Validate() = %#v", response)
	}
}

func TestValidHarnessHealthRequiresContractsAndFeatures(t *testing.T) {
	valid := `APEXLOAD_JAVA_VUSER_V2 {"contract":"apexload.java_vuser.ipc.v2","kind":"capabilities","event_contract":"apexload.java_vuser.lrapi_events.v2","features":["java_vuser_validation_only_v1","java_vuser_dedicated_classloader_v1","java_vuser_persistent_lifecycle_v1","java_vuser_session_frame_token_v1"]}`
	for name, output := range map[string]string{
		"valid":               valid,
		"wrong contract":      strings.Replace(valid, harnessIPCContract, "apexload.java_vuser.ipc.v99", 1),
		"wrong event":         strings.Replace(valid, harnessEventContract, "apexload.java_vuser.events.v99", 1),
		"missing validation":  strings.Replace(valid, `"java_vuser_validation_only_v1",`, "", 1),
		"missing loader":      strings.Replace(valid, `,"java_vuser_dedicated_classloader_v1"`, "", 1),
		"missing persistent":  strings.Replace(valid, `,"java_vuser_persistent_lifecycle_v1"`, "", 1),
		"missing frame token": strings.Replace(valid, `,"java_vuser_session_frame_token_v1"`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if got := validHarnessHealth(output); got != (name == "valid") {
				t.Fatalf("validHarnessHealth() = %v", got)
			}
		})
	}
}

func TestCanonicalLifecycleMethodAcceptsFreshPhaseAliases(t *testing.T) {
	tests := map[string]string{
		"":               "full_lifecycle",
		"init":           "init",
		"VUSER_INIT":     "vuser_init",
		"initialize":     "initialize",
		"action":         "action",
		"Action":         "Action",
		"RUN":            "run",
		"end":            "end",
		"VUSER_END":      "vuser_end",
		"teardown":       "teardown",
		"FULL_LIFECYCLE": "full_lifecycle",
	}
	for input, want := range tests {
		got, ok := canonicalLifecycleMethod(input)
		if !ok || got != want {
			t.Errorf("canonicalLifecycleMethod(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	if _, ok := canonicalLifecycleMethod("destroy"); ok {
		t.Fatal("canonicalLifecycleMethod accepted an arbitrary method")
	}
}

func TestExecutionCaptureIsBounded(t *testing.T) {
	largeDiagnostic := strings.Repeat("x", MaxCaptureBytes+4096)
	fixture := makeSidecarFixture(t, "printf '%s' "+shellQuote(largeDiagnostic)+" >&2")
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "bounded-output-test")
	response, err := client.Execute(context.Background(), fixture.executionRequest("bounded-output"))
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || !response.StderrTruncated || len(response.Stderr) != MaxCaptureBytes {
		t.Fatalf("bounded execution = success %v truncated %v stderr bytes %d", response.Success, response.StderrTruncated, len(response.Stderr))
	}
}

func TestOneShotResultUsesStrictRequestBoundTail(t *testing.T) {
	fixture := makeSidecarFixture(t,
		`dd if=/dev/zero bs=1024 count=300 2>/dev/null | tr '\000' x; printf '\n{"success":false}\n'`,
	)
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "tail-bound-test")
	response, err := client.Execute(context.Background(), fixture.executionRequest("noisy-result"))
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.ErrorCode != "" || !response.StdoutTruncated {
		t.Fatalf("noisy execution = %#v", response)
	}
	if !strings.Contains(response.Stdout, `"request_id":"noisy-result"`) {
		t.Fatalf("bounded stdout lost the authoritative tail frame: %q", response.Stdout)
	}
}

func TestOneShotEventFallbackSignalFailsClosedWhenDiagnosticsTruncate(t *testing.T) {
	fixture := makeSidecarFixture(t,
		`export APEXLOAD_TEST_EVENT_FALLBACK_USED=true; dd if=/dev/zero bs=1024 count=300 2>/dev/null | tr '\000' x >&2`,
	)
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "fallback-truncated-test")
	response, err := client.Execute(context.Background(), fixture.executionRequest("fallback-truncated"))
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || !response.StderrTruncated || !response.EventsTruncated {
		t.Fatalf("fallback truncation response = %#v", response)
	}
}

func TestParseHarnessSummaryRequiresExactV2Identity(t *testing.T) {
	valid := `{"contract":"apexload.java_vuser.ipc.v2","kind":"result","request_id":"request-1","success":true}`
	for name, output := range map[string]string{
		"valid":          valid,
		"user json":      `{"success":true}`,
		"wrong contract": strings.Replace(valid, harnessIPCContract, "future", 1),
		"wrong kind":     strings.Replace(valid, `"kind":"result"`, `"kind":"event"`, 1),
		"wrong request":  strings.Replace(valid, "request-1", "request-2", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := parseHarnessSummary(output, "request-1")
			if ok != (name == "valid") {
				t.Fatalf("parseHarnessSummary(%s) = %v", name, ok)
			}
		})
	}
}

func TestTailCappedBufferMarksDroppedPriorBytes(t *testing.T) {
	var buffer tailCappedBuffer
	buffer.max = 4
	_, _ = buffer.Write([]byte("a"))
	_, _ = buffer.Write([]byte("bcde"))
	if got := buffer.String(); got != "bcde" || !buffer.Truncated() {
		t.Fatalf("tail buffer = %q truncated=%v, want bcde/true", got, buffer.Truncated())
	}
}

func TestCaptureBuffersAllowConcurrentFailedStartSnapshots(t *testing.T) {
	for name, buffer := range map[string]captureBuffer{
		"head": &cappedBuffer{max: 128},
		"tail": &tailCappedBuffer{max: 128},
	} {
		t.Run(name, func(t *testing.T) {
			writerDone := make(chan struct{})
			go func() {
				defer close(writerDone)
				for iteration := 0; iteration < 1_000; iteration++ {
					_, _ = buffer.Write([]byte("runner output\n"))
				}
			}()
			for {
				_ = buffer.String()
				_ = buffer.Truncated()
				select {
				case <-writerDone:
					return
				default:
				}
			}
		})
	}
}

func TestOneShotCleanupKillsNativeDescendantsAndReleasesSlot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group regression")
	}
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	fixture := makeSidecarFixture(t,
		`/bin/sleep 30 >/dev/null 2>&1 & printf '%s\n' "$!" > `+shellQuote(childPIDFile),
	)
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "descendant-cleanup-test")
	response, err := client.Execute(context.Background(), fixture.executionRequest("descendant-1"))
	if err != nil || !response.Success {
		t.Fatalf("first Execute() = %#v, %v", response, err)
	}
	pidText := strings.TrimSpace(string(mustReadFile(t, childPIDFile)))
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		t.Fatalf("child PID %q: %v", pidText, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !testProcessExists(pid) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if testProcessExists(pid) {
		t.Fatalf("native descendant %d survived completed one-shot execution", pid)
	}
	response, err = client.Execute(context.Background(), fixture.executionRequest("descendant-2"))
	if err != nil || !response.Success {
		t.Fatalf("capacity slot was not reusable: %#v, %v", response, err)
	}
}

func TestPersistentCapacityTimeoutReportsSessionNotStarted(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	implementation := fixture.server(1)
	request := fixture.executionRequest("capacity-timeout")
	request.SessionID = "capacity-session"
	generation := &ownerGeneration{ownerID: "test-owner", epoch: "test-epoch", ready: true, leaseDeadline: time.Now().Add(time.Hour)}
	implementation.owner = generation
	prepared, failure := implementation.prepareRuntime(context.Background(), request.Runtime)
	if failure != nil {
		t.Fatalf("prepareRuntime() = %#v", failure)
	}
	implementation.slots <- struct{}{}
	defer func() { <-implementation.slots }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	session, failure := implementation.createPersistentSession(
		ctx, request.SessionID, prepared, request, generation,
	)
	if session != nil || failure == nil || failure.code != "session_not_started" {
		t.Fatalf("capacity-limited persistent creation = session %#v failure %#v", session, failure)
	}
	implementation.sessionsMu.Lock()
	_, sessionExists := implementation.sessions[request.SessionID]
	_, pendingExists := implementation.pendingSessions[request.SessionID]
	implementation.sessionsMu.Unlock()
	if sessionExists || pendingExists {
		t.Fatal("pre-start capacity timeout left a persistent session behind")
	}
}

func TestExecutionCancellationKillsAndReapsProcess(t *testing.T) {
	fixture := makeSidecarFixture(t, "/bin/sleep 30")
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "cancel-test")

	// Starting an execution includes bounded JDK and harness probes before the
	// fixture reaches its process-start marker. Under `go test ./...`, package
	// compilation and other Java fixtures can delay those short-lived processes
	// beyond the old three-second polling budget. Keep the request alive long
	// enough to test explicit cancellation rather than racing its own timeout.
	request := fixture.executionRequest("cancel-me")
	request.TimeoutMS = 30_000
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan ExecutionResponse, 1)
	errors := make(chan error, 1)
	go func() {
		response, executeErr := client.Execute(ctx, request)
		result <- response
		errors <- executeErr
	}()
	waitForFile(t, fixture.startedFile, 15*time.Second)
	cancelResponse, err := client.Cancel(context.Background(), "cancel-me")
	if err != nil || !cancelResponse.Canceled {
		t.Fatalf("Cancel() = %#v, %v", cancelResponse, err)
	}
	select {
	case response := <-result:
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if response.Success || response.ErrorCode != "execution_canceled" {
			t.Fatalf("canceled execution response = %#v", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled JVM process was not reaped")
	}
}

func TestRequestCancellationKillsAndReapsProcess(t *testing.T) {
	fixture := makeSidecarFixture(t, "/bin/sleep 30")
	implementation := fixture.server(1)
	server := httptest.NewServer(implementation)
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "request-cancel-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := fixture.executionRequest("request-cancel")
	request.TimeoutMS = 30_000
	done := make(chan error, 1)
	go func() {
		_, executeErr := client.Execute(ctx, request)
		done <- executeErr
	}()
	waitForFile(t, fixture.startedFile, 15*time.Second)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("request-canceled execution returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request-canceled JVM process was not reaped")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		implementation.runningMu.Lock()
		running := len(implementation.running)
		implementation.runningMu.Unlock()
		if running == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("request-canceled execution remained registered")
}

func TestServerProcessSemaphoreSerializesExecutions(t *testing.T) {
	fixture := makeSidecarFixture(t, "/bin/sleep 0.2")
	server := httptest.NewServer(fixture.server(1))
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "semaphore-test")
	started := time.Now()
	var wg sync.WaitGroup
	for _, id := range []string{"serial-1", "serial-2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			response, executeErr := client.Execute(context.Background(), fixture.executionRequest(id))
			if executeErr != nil || !response.Success {
				t.Errorf("Execute(%s) = %#v, %v", id, response, executeErr)
			}
		}(id)
	}
	wg.Wait()
	if elapsed := time.Since(started); elapsed < 350*time.Millisecond {
		t.Fatalf("two 200ms executions completed in %s; process cap was not enforced", elapsed)
	}
}

func TestSidecarEndToEndRealHarness(t *testing.T) {
	javaBin, javaErr := exec.LookPath("java")
	javacBin, javacErr := exec.LookPath("javac")
	jarBin, jarErr := exec.LookPath("jar")
	if javaErr != nil || javacErr != nil || jarErr != nil {
		t.Skip("local Java toolchain is unavailable")
	}
	jdk, err := javarunner.ResolveJDK(context.Background(), javarunner.ResolveOptions{ExplicitJavaCommand: javaBin})
	if err != nil {
		t.Skipf("local Java 17+ runtime is unavailable: %v", err)
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	harnessClasses := filepath.Join(tempDir, "harness-classes")
	actionsClasses := filepath.Join(tempDir, "actions-classes")
	if err := os.MkdirAll(harnessClasses, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(actionsClasses, 0o755); err != nil {
		t.Fatal(err)
	}
	harnessSource := filepath.Join(repositoryRoot, "runners", "java_vuser", "Harness.java")
	lrSource := filepath.Join(repositoryRoot, "runners", "java_vuser", "lrapi", "lr.java")
	compileHarness := exec.Command(javacBin, "--release", "17", "-d", harnessClasses, lrSource, harnessSource)
	if output, err := compileHarness.CombinedOutput(); err != nil {
		t.Fatalf("compile harness: %v\n%s", err, output)
	}
	harnessJar := filepath.Join(tempDir, "harness.jar")
	createJar := exec.Command(jarBin, "--create", "--file", harnessJar, "--main-class", "Harness", "-C", harnessClasses, ".")
	if output, err := createJar.CombinedOutput(); err != nil {
		t.Fatalf("create harness jar: %v\n%s", err, output)
	}
	actionsSource := filepath.Join(tempDir, "Actions.java")
	if err := os.WriteFile(actionsSource, []byte(`import java.io.BufferedReader;
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
    count = 10;
    lr.save_string("persisted", "Token");
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
      System.err.println("sidecar-crash-diagnostic");
      System.exit(9);
    }
    count++;
    System.out.println("action:" + count + ":" + lr.eval_string("{Token}") + ":instance:" + instance);
    lr.output_message("sidecar-e2e");
    return lr.PASS;
  }
  public int end() {
    System.out.println("end:" + count + ":" + lr.eval_string("{Token}") + ":instance:" + instance);
    return lr.PASS;
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	compileActions := exec.Command(javacBin, "--release", "17", "-cp", harnessClasses, "-d", actionsClasses, actionsSource)
	if output, err := compileActions.CombinedOutput(); err != nil {
		t.Fatalf("compile Actions: %v\n%s", err, output)
	}

	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled:        true,
			AuthToken:      testSidecarAuthToken,
			DefaultJDKHome: jdk.Home,
			HarnessJar:     harnessJar,
			MaxProcesses:   1,
		},
		CredentialCheck:                  func() error { return nil },
		supportsStrongProcessContainment: func() bool { return true },
		startStrongProcessTree:           javarunner.StartProcessTree,
	})
	server := httptest.NewServer(implementation)
	defer server.Close()
	client := newClaimedTestClient(t, server.URL, server.Client(), "real-harness-test")
	stopOwnerRenewal := keepTestOwnerLease(client, "real-harness-test")
	defer func() { stopOwnerRenewal() }()
	response, err := client.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "real-harness",
		Runtime: RuntimeSettings{
			JDKSelection:         "specified",
			JDKHome:              jdk.Home,
			Classpath:            []string{actionsClasses},
			DedicatedClassLoader: true,
			StartupTimeoutMS:     5000,
		},
		Command:   ActionCommand{ActionType: "java_vuser_run", ClassName: "Actions", Method: "action"},
		TimeoutMS: 15000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.ErrorCode != "" || !strings.Contains(response.Stdout, `"success":true`) {
		t.Fatalf("real harness execution = %#v", response)
	}

	runtimeSettings := RuntimeSettings{
		JDKSelection:         "specified",
		JDKHome:              jdk.Home,
		Classpath:            []string{actionsClasses},
		DedicatedClassLoader: true,
		StartupTimeoutMS:     5000,
	}
	executePhase := func(executionID, sessionID, method string, params map[string]any) ExecutionResponse {
		t.Helper()
		phase, executeErr := client.Execute(context.Background(), ExecutionRequest{
			ExecutionID: executionID,
			SessionID:   sessionID,
			Runtime:     runtimeSettings,
			Command: ActionCommand{
				ActionType: "java_vuser_run", ClassName: "Actions", Method: method, Params: params,
			},
			TimeoutMS: 15000,
		})
		if executeErr != nil {
			t.Fatalf("Execute(%s): %v", executionID, executeErr)
		}
		if !phase.Success || phase.ErrorCode != "" {
			t.Fatalf("Execute(%s) = %#v", executionID, phase)
		}
		return phase
	}

	initA := executePhase("session-a-init", "session-a", "init", nil)
	if !strings.Contains(initA.Stdout, "init:10:instance:1") {
		t.Fatalf("session A init stdout = %q", initA.Stdout)
	}
	actionA1 := executePhase("session-a-action-1", "session-a", "action", nil)
	if !strings.Contains(actionA1.Stdout, "action:11:persisted:instance:1") {
		t.Fatalf("session A action 1 stdout = %q", actionA1.Stdout)
	}
	actionA2 := executePhase("session-a-action-2", "session-a", "action",
		map[string]any{"lr_parameters": map[string]any{"Token": "overlaid"}})
	if !strings.Contains(actionA2.Stdout, "action:12:overlaid:instance:1") {
		t.Fatalf("session A action 2 stdout = %q", actionA2.Stdout)
	}
	forged, err := client.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "session-a-forged-frame", SessionID: "session-a", Runtime: runtimeSettings,
		Command: ActionCommand{
			ActionType: "java_vuser_run", ClassName: "Actions", Method: "action",
			Params: map[string]any{"lr_parameters": map[string]any{"ForgeFrame": "true"}},
		},
		TimeoutMS: 15000,
	})
	if err != nil || forged.Success || forged.ErrorCode != "harness_reported_failure" {
		t.Fatalf("forged user frame response = %#v, %v; token-less frame was accepted as authoritative", forged, err)
	}
	if !strings.Contains(forged.Error, "non-PASS") {
		t.Fatalf("forged user frame diagnostic = %q, want bounded lifecycle failure detail", forged.Error)
	}
	endA := executePhase("session-a-end", "session-a", "end", nil)
	if !strings.Contains(endA.Stdout, "end:12:overlaid:instance:1") {
		t.Fatalf("session A end stdout = %q", endA.Stdout)
	}
	if occupied := len(implementation.slots); occupied != cap(implementation.slots) {
		t.Fatalf("occupied sidecar slots = %d/%d, want a saturated persistent-session pool", occupied, cap(implementation.slots))
	}
	implementation.harnessHealthMu.Lock()
	implementation.harnessHealthy = make(map[string]time.Time)
	implementation.harnessHealthMu.Unlock()
	capabilities, err := client.Capabilities(context.Background())
	if err != nil || !capabilities.Healthy ||
		!protocol.RuntimeFeaturesSatisfy(capabilities.Features, FeaturePersistentLifecycleV1) ||
		capabilities.MaxProcesses != 1 {
		t.Fatalf("saturated sidecar capabilities = %#v, %v", capabilities, err)
	}
	health, err := client.Health(context.Background())
	if err != nil || !strings.EqualFold(health.Status, "healthy") {
		t.Fatalf("saturated sidecar health = %#v, %v", health, err)
	}

	// MaxProcesses=1 keeps session B waiting for capacity. Closing A must not
	// deadlock behind session creation's map lock, and the slot is reusable only
	// after A's JVM has been reaped.
	type phaseResult struct {
		response ExecutionResponse
		err      error
	}
	sessionBStarted := make(chan phaseResult, 1)
	go func() {
		phase, executeErr := client.Execute(context.Background(), ExecutionRequest{
			ExecutionID: "session-b-init", SessionID: "session-b", Runtime: runtimeSettings,
			Command:   ActionCommand{ActionType: "java_vuser_run", ClassName: "Actions", Method: "init"},
			TimeoutMS: 15000,
		})
		sessionBStarted <- phaseResult{response: phase, err: executeErr}
	}()
	select {
	case premature := <-sessionBStarted:
		t.Fatalf("session B bypassed occupied capacity: %#v, %v", premature.response, premature.err)
	case <-time.After(150 * time.Millisecond):
	}
	closedA, err := client.CloseSession(context.Background(), "session-a")
	if err != nil || !closedA.Closed {
		t.Fatalf("CloseSession(session-a) = %#v, %v", closedA, err)
	}
	var initB ExecutionResponse
	select {
	case started := <-sessionBStarted:
		if started.err != nil || !started.response.Success {
			t.Fatalf("session B init after slot release = %#v, %v", started.response, started.err)
		}
		initB = started.response
	case <-time.After(10 * time.Second):
		t.Fatal("session B did not acquire capacity after session A was reaped")
	}
	if !strings.Contains(initB.Stdout, "init:10:instance:1") {
		t.Fatalf("session B init stdout = %q; Actions instance leaked across VUsers", initB.Stdout)
	}
	actionB := executePhase("session-b-action", "session-b", "action", nil)
	if !strings.Contains(actionB.Stdout, "action:11:persisted:instance:1") {
		t.Fatalf("session B action stdout = %q", actionB.Stdout)
	}

	// A new process-global owner simulates a generator restart while the
	// sidecar remains alive. Claim success is the capacity-release barrier: the
	// prior JVM must be reaped before the new lease is returned.
	staleClient := client
	stopOwnerRenewal()
	restartedClient, err := NewClientWithToken(server.URL, testSidecarAuthToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartedClient.ClaimOwner(context.Background(), "restarted-generator"); err != nil {
		t.Fatalf("takeover ClaimOwner: %v", err)
	}
	if _, err := restartedClient.ClaimRuntimeLease(context.Background()); err != nil {
		t.Fatalf("takeover ClaimRuntimeLease: %v", err)
	}
	stopOwnerRenewal = keepTestOwnerLease(restartedClient, "restarted-generator")
	if occupied := len(implementation.slots); occupied != 0 {
		t.Fatalf("occupied slots after owner takeover = %d, want 0", occupied)
	}
	if _, err := staleClient.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "stale-execute", SessionID: "session-b", Runtime: runtimeSettings,
		Command:   ActionCommand{ActionType: "java_vuser_run", ClassName: "Actions", Method: "action"},
		TimeoutMS: 15000,
	}); remoteErrorCode(err) != "owner_fenced" {
		t.Fatalf("stale Execute error = %v, want owner_fenced", err)
	}
	if _, err := staleClient.Cancel(context.Background(), "stale-execute"); remoteErrorCode(err) != "owner_fenced" {
		t.Fatalf("stale Cancel error = %v, want owner_fenced", err)
	}
	if _, err := staleClient.CloseSession(context.Background(), "session-b"); remoteErrorCode(err) != "owner_fenced" {
		t.Fatalf("stale CloseSession error = %v, want owner_fenced", err)
	}
	missingState, err := restartedClient.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "require-existing-after-restart", SessionID: "session-b",
		RequireExistingSession: true, Runtime: runtimeSettings,
		Command:   ActionCommand{ActionType: "java_vuser_run", ClassName: "Actions", Method: "action"},
		TimeoutMS: 15000,
	})
	if err != nil || missingState.ErrorCode != "session_unusable" {
		t.Fatalf("RequireExisting after takeover = %#v, %v", missingState, err)
	}
	client = restartedClient
	restartedInit := executePhase("session-b-restarted-init", "session-b", "init", nil)
	if !strings.Contains(restartedInit.Stdout, "init:10:instance:1") {
		t.Fatalf("restarted session init stdout = %q", restartedInit.Stdout)
	}
	if _, err := client.ClaimOwner(context.Background(), "restarted-generator"); err != nil {
		t.Fatalf("idempotent ClaimOwner: %v", err)
	}
	restartedAction := executePhase("session-b-restarted-action", "session-b", "action", nil)
	if !strings.Contains(restartedAction.Stdout, "action:11:persisted:instance:1") {
		t.Fatalf("idempotent reclaim replaced Actions state: %q", restartedAction.Stdout)
	}
	_ = executePhase("session-b-end", "session-b", "end", nil)
	closedB, err := client.CloseSession(context.Background(), "session-b")
	if err != nil || !closedB.Closed {
		t.Fatalf("CloseSession(session-b) = %#v, %v", closedB, err)
	}

	_ = executePhase("session-c-init", "session-c", "init", nil)
	crashed, err := client.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "session-c-crash", SessionID: "session-c", Runtime: runtimeSettings,
		Command: ActionCommand{
			ActionType: "java_vuser_run", ClassName: "Actions", Method: "action",
			Params: map[string]any{"lr_parameters": map[string]any{"Crash": "true"}},
		},
		TimeoutMS: 15000,
	})
	if err != nil {
		t.Fatalf("crash Execute: %v", err)
	}
	if crashed.Success || crashed.ErrorCode != "session_unusable" ||
		!strings.Contains(crashed.Stderr, "sidecar-crash-diagnostic") {
		t.Fatalf("crashed session response = %#v, want bounded crash diagnostics", crashed)
	}
	retryC, err := client.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "session-c-retry", SessionID: "session-c", Runtime: runtimeSettings,
		Command:   ActionCommand{ActionType: "java_vuser_run", ClassName: "Actions", Method: "action"},
		TimeoutMS: 15000,
	})
	if err != nil {
		t.Fatalf("retry crashed session: %v", err)
	}
	if retryC.Success || retryC.ErrorCode != "session_unusable" {
		t.Fatalf("retry crashed session = %#v; sidecar silently restarted lost state", retryC)
	}
	closedC, err := client.CloseSession(context.Background(), "session-c")
	if err != nil || !closedC.Closed {
		t.Fatalf("CloseSession(session-c) = %#v, %v", closedC, err)
	}

	stealInit := executePhase("session-steal-init", "session-steal", "init",
		map[string]any{"lr_parameters": map[string]any{"StealNext": "true"}})
	if !strings.Contains(stealInit.Stdout, "init:10:instance:1") {
		t.Fatalf("stdin-forge init stdout = %q", stealInit.Stdout)
	}
	stolen := false
	for attempt := 1; attempt <= 5; attempt++ {
		response, executeErr := client.Execute(context.Background(), ExecutionRequest{
			ExecutionID: fmt.Sprintf("session-steal-action-%d", attempt),
			SessionID:   "session-steal",
			Runtime:     runtimeSettings,
			Command:     ActionCommand{ActionType: "java_vuser_run", ClassName: "Actions", Method: "action"},
			TimeoutMS:   300,
		})
		if executeErr != nil {
			t.Fatalf("stdin-forge action %d: %v", attempt, executeErr)
		}
		if response.Success {
			continue
		}
		if response.ErrorCode != "execution_timeout" {
			t.Fatalf("stdin-forge action %d = %#v, want timeout after forged frame rejection", attempt, response)
		}
		stolen = true
		break
	}
	if !stolen {
		t.Fatal("sidecar background stdin reader did not consume any of five bounded action requests")
	}
}

func TestListenLoopbackRejectsWideBind(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "[::]:0", ":8098", "example.test:8098"} {
		if listener, err := ListenLoopback(address); err == nil {
			_ = listener.Close()
			t.Fatalf("ListenLoopback(%q) succeeded", address)
		}
	}
	listener, err := ListenLoopback("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
}

type sidecarFixture struct {
	t                  *testing.T
	jdkHome            string
	harnessJar         string
	scriptClasspath    string
	sharedClasspath    string
	argsFile           string
	stdinFile          string
	javaHomeFile       string
	startedFile        string
	afterReadShellCode string
}

func makeSidecarFixture(t *testing.T, afterReadShellCode string) sidecarFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed Java fixture is Unix-only")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "jdk")
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := sidecarFixture{
		t:                  t,
		jdkHome:            home,
		harnessJar:         filepath.Join(dir, "harness.jar"),
		scriptClasspath:    filepath.Join(dir, "script classes"),
		sharedClasspath:    filepath.Join(dir, "shared.jar"),
		argsFile:           filepath.Join(dir, "args.txt"),
		stdinFile:          filepath.Join(dir, "stdin.json"),
		javaHomeFile:       filepath.Join(dir, "java-home.txt"),
		startedFile:        filepath.Join(dir, "started.txt"),
		afterReadShellCode: afterReadShellCode,
	}
	if err := os.WriteFile(fixture.harnessJar, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fixture.scriptClasspath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.sharedClasspath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "-version" ]; then
  echo 'openjdk version "21.0.7"' >&2
  exit 0
fi
if [ "$1" = "-XshowSettings:properties" ]; then
  echo '    java.home = %s' >&2
  echo 'openjdk version "21.0.7"' >&2
  exit 0
fi
for arg in "$@"; do
  if [ "$arg" = "--health" ]; then
    echo 'APEXLOAD_JAVA_VUSER_V2 {"contract":"apexload.java_vuser.ipc.v2","kind":"capabilities","event_contract":"apexload.java_vuser.lrapi_events.v2","features":["java_vuser_validation_only_v1","java_vuser_dedicated_classloader_v1","java_vuser_persistent_lifecycle_v1","java_vuser_session_frame_token_v1"]}'
    exit 0
  fi
done
printf 'started\n' > %s
printf '%%s\n' "$@" > %s
printf '%%s\n' "$JAVA_HOME" > %s
IFS= read -r payload
printf '%%s\n' "$payload" > %s
%s
request_id=$(printf '%%s' "$payload" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
if [ -n "$APEXLOAD_JAVA_VUSER_EVENT_FILE" ]; then
  printf '{"type":"transaction"}\n' > "$APEXLOAD_JAVA_VUSER_EVENT_FILE"
fi
echo 'fixture-stderr' >&2
printf '{"contract":"apexload.java_vuser.ipc.v2","kind":"result","request_id":"%%s","success":true,"duration_ms":1,"event_fallback_used":%%s,"events_truncated":%%s}\n' "$request_id" "${APEXLOAD_TEST_EVENT_FALLBACK_USED:-false}" "${APEXLOAD_TEST_EVENTS_TRUNCATED:-false}"
`, shellQuote(home), shellQuote(fixture.startedFile), shellQuote(fixture.argsFile), shellQuote(fixture.javaHomeFile), shellQuote(fixture.stdinFile), afterReadShellCode)
	launcher := filepath.Join(home, "bin", "java")
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture sidecarFixture) server(maxProcesses int) *Server {
	return NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled:        true,
			AuthToken:      testSidecarAuthToken,
			DefaultJDKHome: fixture.jdkHome,
			HarnessJar:     fixture.harnessJar,
			Classpath:      []string{fixture.sharedClasspath},
			MaxProcesses:   maxProcesses,
		},
		CredentialCheck:                  func() error { return nil },
		supportsStrongProcessContainment: func() bool { return true },
		startStrongProcessTree:           javarunner.StartProcessTree,
	})
}

func newClaimedTestClient(
	t *testing.T,
	rawURL string,
	httpClient *http.Client,
	ownerID string,
) *Client {
	t.Helper()
	client, err := NewClientWithToken(rawURL, testSidecarAuthToken, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ClaimOwner(context.Background(), ownerID); err != nil {
		t.Fatalf("ClaimOwner(%q): %v", ownerID, err)
	}
	if _, err := client.ClaimRuntimeLease(context.Background()); err != nil {
		t.Fatalf("ClaimRuntimeLease(%q): %v", ownerID, err)
	}
	return client
}

func keepTestOwnerLease(client *Client, ownerID string) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(RunnerOwnerLeaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(ctx, RunnerOwnerLeaseRenewInterval)
				_, _ = client.ClaimOwner(renewCtx, ownerID)
				renewCancel()
				renewCtx, renewCancel = context.WithTimeout(ctx, RuntimeLeaseRenewInterval)
				_, _ = client.RenewRuntimeLease(renewCtx)
				renewCancel()
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			cancel()
			<-done
		})
	}
}

func authenticatedPost(client *http.Client, endpoint string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
	return client.Do(request)
}

func remoteErrorCode(err error) string {
	var remote *RemoteError
	if errors.As(err, &remote) {
		return remote.Code
	}
	return ""
}

func (fixture sidecarFixture) executionRequest(id string) ExecutionRequest {
	return ExecutionRequest{
		ExecutionID: id,
		Runtime: RuntimeSettings{
			JDKSelection: "specified", JDKHome: fixture.jdkHome,
			Classpath: []string{fixture.scriptClasspath},
		},
		Command:   ActionCommand{ActionType: "java_vuser_run", ClassName: "Actions", Method: "action"},
		TimeoutMS: 5000,
	}
}

func startIdleExitedSessionForCleanupTest(
	t *testing.T,
	ownerID string,
	sessionID string,
) (*Server, *httptest.Server, *persistentSession) {
	t.Helper()
	fixture := makeSidecarFixture(t, `
request_id=$(printf '%s' "$payload" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
frame_token=$(APEXLOAD_TEST_SIDECAR_SESSION_PAYLOAD="$payload" `+shellQuote(os.Args[0])+` -test.run=TestSidecarSessionFrameTokenHelper --)
printf '{"contract":"apexload.java_vuser.lrapi_events.v2","request_id":"%s","type":"log","message":"idle-exit-event"}\n' "$request_id" > "$APEXLOAD_JAVA_VUSER_EVENT_FILE"
printf 'idle-exit-diagnostic\n' > "$APEXLOAD_JAVA_VUSER_DIAGNOSTIC_FILE"
printf 'APEXLOAD_JAVA_VUSER_V2 {"contract":"apexload.java_vuser.ipc.v2","kind":"result","request_id":"%s","frame_token":"%s","success":true}\n' "$request_id" "$frame_token"
sleep 1
exit 0
`)
	implementation := fixture.server(1)
	server := httptest.NewServer(implementation)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = implementation.Shutdown(shutdownCtx)
		server.Close()
	})
	client := newClaimedTestClient(t, server.URL, server.Client(), ownerID)
	request := fixture.executionRequest("execute-" + sessionID)
	request.SessionID = sessionID
	response, err := client.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("Execute idle-exit session: %v", err)
	}
	if !response.Success || response.ErrorCode != "" {
		t.Fatalf("idle-exit execution = %#v, want successful response before process exit", response)
	}

	implementation.sessionsMu.Lock()
	session := implementation.sessions[sessionID]
	implementation.sessionsMu.Unlock()
	if session == nil {
		t.Fatal("successful idle-exit execution did not publish its persistent session")
	}
	select {
	case <-session.waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("idle-exit session process was not reaped")
	}
	if occupied := len(implementation.slots); occupied != 0 {
		t.Fatalf("idle-exit session retained %d capacity slots", occupied)
	}
	for _, path := range []string{session.eventPath, session.diagnosticPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("idle-exit capture %q was removed before generation cleanup: %v", path, err)
		}
	}
	return implementation, server, session
}

func startLiveSessionForBoundedShutdownTest(
	t *testing.T,
) (*Server, *httptest.Server, *persistentSession) {
	t.Helper()
	fixture := makeSidecarFixture(t, `
request_id=$(printf '%s' "$payload" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
frame_token=$(APEXLOAD_TEST_SIDECAR_SESSION_PAYLOAD="$payload" `+shellQuote(os.Args[0])+` -test.run=TestSidecarSessionFrameTokenHelper --)
printf '{"contract":"apexload.java_vuser.lrapi_events.v2","request_id":"%s","type":"log","message":"bounded-shutdown-event"}\n' "$request_id" > "$APEXLOAD_JAVA_VUSER_EVENT_FILE"
printf 'bounded-shutdown-diagnostic\n' > "$APEXLOAD_JAVA_VUSER_DIAGNOSTIC_FILE"
printf 'APEXLOAD_JAVA_VUSER_V2 {"contract":"apexload.java_vuser.ipc.v2","kind":"result","request_id":"%s","frame_token":"%s","success":true}\n' "$request_id" "$frame_token"
sleep 30
exit 0
`)
	implementation := fixture.server(1)
	server := httptest.NewServer(implementation)
	t.Cleanup(func() {
		session := func() *persistentSession {
			implementation.sessionsMu.Lock()
			defer implementation.sessionsMu.Unlock()
			for candidate := range implementation.allSessions {
				return candidate
			}
			return nil
		}()
		if session != nil {
			session.closeStdin()
			_ = session.killTree()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = implementation.Shutdown(shutdownCtx)
		server.Close()
	})
	client := newClaimedTestClient(t, server.URL, server.Client(), "bounded-shutdown-owner")
	request := fixture.executionRequest("bounded-shutdown-execution")
	request.SessionID = "bounded-shutdown-session"
	response, err := client.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("Execute live cleanup session: %v", err)
	}
	if !response.Success || response.ErrorCode != "" {
		t.Fatalf("live cleanup execution = %#v, want successful response with live process", response)
	}
	implementation.sessionsMu.Lock()
	session := implementation.sessions[request.SessionID]
	implementation.sessionsMu.Unlock()
	if session == nil {
		t.Fatal("live cleanup execution did not publish its persistent session")
	}
	if occupied := len(implementation.slots); occupied != 1 {
		t.Fatalf("live cleanup fixture occupied slots = %d, want 1", occupied)
	}
	for _, path := range []string{session.eventPath, session.diagnosticPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("live cleanup capture %q is unavailable: %v", path, err)
		}
	}
	return implementation, server, session
}

func assertSessionCapturesRemoved(t *testing.T, session *persistentSession) {
	t.Helper()
	for _, path := range []string{session.eventPath, session.diagnosticPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("session capture %q still exists after cleanup: %v", path, err)
		}
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	return strings.Split(strings.TrimSpace(string(mustReadFile(t, path))), "\n")
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", filepath.Base(path))
}

func assertErrorResponse(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	defer response.Body.Close()
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || body.Contract != ContractVersion || body.ErrorCode != code {
		t.Fatalf("error response = status %d body %#v", response.StatusCode, body)
	}
}
