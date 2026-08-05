package javasidecar

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

const maxPersistentFrameBytes = 2 << 20

type persistentEnvelope struct {
	Contract          string `json:"contract"`
	Kind              string `json:"kind"`
	RequestID         string `json:"request_id"`
	FrameToken        string `json:"frame_token,omitempty"`
	Success           bool   `json:"success"`
	Error             string `json:"error,omitempty"`
	Stdout            string `json:"stdout,omitempty"`
	Stderr            string `json:"stderr,omitempty"`
	StdoutTruncated   bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated   bool   `json:"stderr_truncated,omitempty"`
	EventFallbackUsed bool   `json:"event_fallback_used,omitempty"`
	EventsTruncated   bool   `json:"events_truncated,omitempty"`
	Fatal             bool   `json:"fatal,omitempty"`
}

type persistentSession struct {
	invokeMu sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser

	responses  chan persistentEnvelope
	waitDone   chan struct{}
	stdoutDone chan struct{}
	stderrDone chan struct{}
	waitErr    error

	sessionSecret [32]byte
	bootstrapped  bool

	eventPath, diagnosticPath string
	configKey                 string
	generation                *ownerGeneration
	closeTimeout              time.Duration
	warningCodes              []string
	userClasspath             []string
	stdoutNoise, stderrNoise  *lockedCappedBuffer

	stdinOnce, killOnce, cleanupOnce sync.Once
	killFunc                         func(*exec.Cmd) error
	killErr                          error
	containmentFailure               func(error)
	failedKillReapWait               time.Duration
}

type lockedCappedBuffer struct {
	mu  sync.Mutex
	buf cappedBuffer
}

func (b *lockedCappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedCappedBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String(), b.buf.Truncated()
}

func (b *lockedCappedBuffer) markTruncated() {
	b.mu.Lock()
	b.buf.markTruncated()
	b.mu.Unlock()
}

func persistentSessionConfigKey(runtime RuntimeSettings, command ActionCommand) string {
	value := struct {
		Runtime          RuntimeSettings `json:"runtime"`
		ClassName        string          `json:"class_name"`
		SourceArchiveRef string          `json:"source_archive_ref"`
		VUserID          string          `json:"vuser_id"`
		GroupName        string          `json:"group_name"`
		ScenarioID       string          `json:"scenario_id"`
		IgnoreThinkTime  string          `json:"ignore_think_time"`
		ThinkTimeLimitMS string          `json:"think_time_limit_ms"`
	}{
		runtime, strings.TrimSpace(command.ClassName), strings.TrimSpace(command.SourceArchiveRef), command.VUserID,
		command.GroupName, command.ScenarioID, command.IgnoreThinkTime, command.ThinkTimeLimitMS,
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func (s *Server) startPersistentSession(
	ctx context.Context,
	prepared preparedRuntime,
	request ExecutionRequest,
	generation *ownerGeneration,
) (*persistentSession, *safeFailure) {
	if !s.supportsStrongProcessContainmentAvailable(ctx) {
		return nil, &safeFailure{code: "process_containment_unavailable"}
	}
	method, ok := canonicalLifecycleMethod(request.Command.Method)
	if !ok || strings.EqualFold(method, "full_lifecycle") {
		return nil, &safeFailure{code: "target_method_invalid"}
	}
	plan, err := javarunner.BuildCommand(javarunner.CommandSpec{
		VMParameters: prepared.vmParameters, JavaMajor: prepared.jdk.Major, VMFamily: prepared.jdk.VMFamily, HarnessJar: prepared.harnessJar,
		ScriptClasspath: prepared.scriptClasspath, SharedClasspath: prepared.sharedClasspath,
		DedicatedClassLoader: prepared.dedicatedLoader, HarnessMain: defaultHarnessMain,
		HarnessArgs: []string{"--class", strings.TrimSpace(request.Command.ClassName), "--method", method, "--session"},
	})
	if err != nil {
		return nil, &safeFailure{code: "harness_configuration_invalid"}
	}
	sessionSecret, secretErr := newPersistentSessionSecret()
	if secretErr != nil {
		return nil, &safeFailure{code: "harness_payload_invalid"}
	}
	release, failure := s.acquireProcessSlot(ctx)
	if failure != nil {
		// No JVM was started and no Actions instance exists. Distinguish this
		// safe-to-retry capacity wait from a timeout/cancellation after invoke.
		return nil, &safeFailure{code: "session_not_started"}
	}
	eventPath, failure := newSessionFile("events")
	if failure != nil {
		release()
		return nil, failure
	}
	diagnosticPath, failure := newSessionFile("diagnostics")
	if failure != nil {
		_ = os.Remove(eventPath)
		release()
		return nil, failure
	}
	session := &persistentSession{
		responses: make(chan persistentEnvelope, 8), waitDone: make(chan struct{}),
		stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}),
		sessionSecret: sessionSecret,
		eventPath:     eventPath, diagnosticPath: diagnosticPath,
		configKey:     persistentSessionConfigKey(request.Runtime, request.Command),
		generation:    generation,
		warningCodes:  append([]string(nil), prepared.warningCodes...),
		userClasspath: append([]string(nil), plan.UserClasspath...),
		stdoutNoise:   &lockedCappedBuffer{buf: cappedBuffer{max: MaxCaptureBytes}},
		stderrNoise:   &lockedCappedBuffer{buf: cappedBuffer{max: MaxCaptureBytes}},
		containmentFailure: func(err error) {
			s.finishProcessCapacity(nil, err)
		},
	}
	cmd := exec.Command(prepared.jdk.JavaBin, plan.Args...)
	cmd.WaitDelay = processWaitDelay
	if prepared.workDir != "" {
		cmd.Dir = prepared.workDir
	}
	environment := prepared.jdk.EnvironmentOverrides(os.Getenv("PATH"))
	addKnownEnvironment(environment, request.Command)
	environment["APEXLOAD_JAVA_VUSER_EVENT_FILE"] = eventPath
	environment["APEXLOAD_JAVA_VUSER_DIAGNOSTIC_FILE"] = diagnosticPath
	cmd.Env = subprocessguard.Environment(environment)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		session.cleanup()
		release()
		return nil, &safeFailure{code: "harness_unavailable"}
	}
	stdout, stdoutChild, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		session.cleanup()
		release()
		return nil, &safeFailure{code: "harness_unavailable"}
	}
	stderr, stderrChild, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutChild.Close()
		session.cleanup()
		release()
		return nil, &safeFailure{code: "harness_unavailable"}
	}
	cmd.Stdout = stdoutChild
	cmd.Stderr = stderrChild
	session.cmd, session.stdin = cmd, stdin
	javarunner.ConfigureProcessTree(cmd)
	if err := s.startStrongProcessTree(cmd); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutChild.Close()
		_ = stderr.Close()
		_ = stderrChild.Close()
		if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
			// A target was launched but strong cleanup became unprovable. Keep
			// this exact slot occupied and close the sidecar-wide admission gate.
			s.finishProcessCapacity(nil, err)
			s.trackFailedStartReap(cmd, session.cleanup)
		} else {
			release()
			session.cleanup()
		}
		return nil, persistentProcessStartFailure(err)
	}
	s.registerPersistentSession(session, generation)
	_ = stdoutChild.Close()
	_ = stderrChild.Close()
	go func() {
		defer stdout.Close()
		session.readStdout(stdout)
	}()
	go func() {
		defer close(session.stderrDone)
		defer stderr.Close()
		_, _ = io.Copy(session.stderrNoise, stderr)
	}()
	go func() {
		// Caller-owned pipes prevent Wait from closing a reader under a
		// concurrent scan. Reap the JVM, terminate inherited native descendants,
		// then drain output before releasing the per-generator capacity slot.
		session.waitErr = cmd.Wait()
		containmentErr := session.killTree()
		if !javarunner.WaitForOutputDrain(
			processWaitDelay,
			[]io.Closer{stdout, stderr},
			session.stdoutDone,
			session.stderrDone,
		) {
			session.stdoutNoise.markTruncated()
			session.stderrNoise.markTruncated()
		}
		s.finishProcessCapacity(release, containmentErr)
		// Keep the reaped object visible to owner takeover and Shutdown until
		// an invocation, explicit close, or generation cleanup has consumed its
		// event and diagnostic files. Removing it here races an Actions process
		// that exits just after returning a successful frame: no invocation is
		// left to call cleanup, and generation cleanup would no longer find it.
		close(session.waitDone)
	}()
	return session, nil
}

func persistentProcessStartFailure(err error) *safeFailure {
	if errors.Is(err, javarunner.ErrStrongProcessContainmentUnavailable) ||
		errors.Is(err, javarunner.ErrProcessContainmentFailed) {
		return &safeFailure{code: "process_containment_unavailable"}
	}
	return &safeFailure{code: "process_failed"}
}

func newSessionFile(kind string) (string, *safeFailure) {
	file, err := os.CreateTemp("", "apexload-java-vuser-session-"+kind+"-*.log")
	if err != nil {
		return "", &safeFailure{code: "event_capture_unavailable"}
	}
	path := file.Name()
	_ = file.Close()
	return path, nil
}

func (s *persistentSession) readStdout(stdout io.Reader) {
	defer close(s.stdoutDone)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxPersistentFrameBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, harnessSessionPrefix) {
			_, _ = s.stdoutNoise.Write(append([]byte(line), '\n'))
			continue
		}
		var envelope persistentEnvelope
		if json.Unmarshal([]byte(strings.TrimPrefix(line, harnessSessionPrefix)), &envelope) != nil {
			_, _ = s.stdoutNoise.Write(append([]byte(line), '\n'))
			continue
		}
		select {
		case s.responses <- envelope:
		default:
			_, _ = s.stdoutNoise.Write([]byte("response queue overflow\n"))
		}
	}
	if scanner.Err() != nil {
		_, _ = s.stdoutNoise.Write([]byte("stdout framing failure\n"))
	}
}

func (s *persistentSession) invoke(ctx context.Context, request ExecutionRequest) processOutcome {
	s.invokeMu.Lock()
	defer s.invokeMu.Unlock()
	select {
	case <-s.waitDone:
		return processOutcome{errorCode: "session_unusable"}
	default:
	}
	if truncateSessionFile(s.eventPath) != nil || truncateSessionFile(s.diagnosticPath) != nil {
		return processOutcome{errorCode: "event_capture_unavailable"}
	}
	method, ok := canonicalLifecycleMethod(request.Command.Method)
	if !ok || strings.EqualFold(method, "full_lifecycle") {
		return processOutcome{errorCode: "target_method_invalid"}
	}
	command := harnessCommand{
		ProtocolVersion: harnessIPCContract, EventContract: harnessEventContract,
		RequestID: request.ExecutionID, ActionName: request.Command.ActionName,
		ActionType: request.Command.ActionType, ClassName: strings.TrimSpace(request.Command.ClassName),
		Method: method, SourceArchiveRef: request.Command.SourceArchiveRef,
		StartupTimeoutMS: request.Runtime.StartupTimeoutMS, UserClasspath: append([]string(nil), s.userClasspath...),
		DedicatedLoader: request.Runtime.DedicatedClassLoader, Params: request.Command.Params,
	}
	expectedToken := persistentFrameToken(s.sessionSecret, request.ExecutionID)
	if !s.bootstrapped {
		command.SessionSecret = hex.EncodeToString(s.sessionSecret[:])
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return processOutcome{errorCode: "harness_payload_invalid"}
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := s.stdin.Write(append(payload, '\n'))
		writeDone <- writeErr
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			return s.failAndReap("session_unusable")
		}
		s.bootstrapped = true
	case <-s.waitDone:
		return s.failAndReap("session_unusable")
	case <-s.stdoutDone:
		return s.failAndReap("session_unusable")
	case <-ctx.Done():
		return s.failAndReap(contextErrorCode(ctx))
	}
	for {
		select {
		case envelope := <-s.responses:
			if envelope.RequestID != request.ExecutionID {
				continue
			}
			if !persistentFrameTokenMatches(envelope.FrameToken, expectedToken) {
				continue
			}
			if envelope.Contract != harnessIPCContract || envelope.Kind != "result" {
				return s.failAndReap("contract_version_mismatch")
			}
			events, eventsTruncated := readCappedFile(s.eventPath, MaxCaptureBytes)
			outcome := processOutcome{
				success: envelope.Success, errorMessage: boundedHarnessError(envelope.Error),
				stdout: envelope.Stdout, stderr: envelope.Stderr, events: events,
				stdoutTruncated: envelope.StdoutTruncated, stderrTruncated: envelope.StderrTruncated,
				eventsTruncated: eventsTruncated || envelope.EventsTruncated ||
					(envelope.EventFallbackUsed && envelope.StderrTruncated),
			}
			if !envelope.Success {
				outcome.errorCode = "harness_reported_failure"
			}
			if envelope.Fatal {
				failed := s.failAndReap("session_unusable")
				failed.errorMessage = outcome.errorMessage
				failed.stdout = firstNonEmptyDiagnostic(outcome.stdout, failed.stdout)
				mergedStderr, stderrCapped := boundedCaptureField(joinDiagnostics(outcome.stderr, failed.stderr))
				failed.stderr = mergedStderr
				failed.events = firstNonEmptyDiagnostic(outcome.events, failed.events)
				failed.stdoutTruncated = outcome.stdoutTruncated || failed.stdoutTruncated
				failed.stderrTruncated = outcome.stderrTruncated || failed.stderrTruncated || stderrCapped
				failed.eventsTruncated = outcome.eventsTruncated || failed.eventsTruncated
				return failed
			}
			return outcome
		case <-s.waitDone:
			return s.failAndReap("session_unusable")
		case <-s.stdoutDone:
			return s.failAndReap("session_unusable")
		case <-ctx.Done():
			return s.failAndReap(contextErrorCode(ctx))
		}
	}
}

func newPersistentSessionSecret() ([32]byte, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return [32]byte{}, err
	}
	return value, nil
}

func persistentFrameToken(secret [32]byte, requestID string) string {
	mac := hmac.New(sha256.New, secret[:])
	_, _ = mac.Write([]byte(harnessIPCContract))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(requestID))
	return hex.EncodeToString(mac.Sum(nil))
}

func persistentFrameTokenMatches(actual, expected string) bool {
	if len(actual) != len(expected) || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func (s *persistentSession) failAndReap(code string) processOutcome {
	killErr := s.killTree()
	s.closeStdin()
	reaped := true
	if killErr == nil {
		<-s.waitDone
	} else {
		// A containment primitive can fail without terminating the direct JVM.
		// Do not let a timed-out/canceled action block forever waiting for
		// cmd.Wait. The capacity token remains quarantined and the sidecar-wide
		// containment gate is already terminal; retain capture ownership until
		// an eventual reap completes.
		wait := s.failedKillReapWait
		if wait <= 0 {
			wait = processWaitDelay
		}
		timer := time.NewTimer(wait)
		select {
		case <-s.waitDone:
			timer.Stop()
		case <-timer.C:
			reaped = false
		}
	}
	events, truncated := readCappedFile(s.eventPath, MaxCaptureBytes)
	stdout, stdoutTruncated := s.stdoutNoise.snapshot()
	stderr, stderrTruncated := s.stderrNoise.snapshot()
	diagnostics, diagnosticsTruncated := readCappedFile(s.diagnosticPath, MaxCaptureBytes)
	if reaped {
		s.cleanup()
	}
	exitDiagnostic := ""
	if reaped && s.waitErr != nil {
		exitDiagnostic = "java_vuser persistent JVM exit: " + s.waitErr.Error()
	}
	mergedStderr, stderrCapped := boundedCaptureField(joinDiagnostics(stderr, diagnostics, exitDiagnostic))
	return processOutcome{
		errorCode: code, stdout: stdout, stderr: mergedStderr, events: events,
		stdoutTruncated: stdoutTruncated || !reaped,
		stderrTruncated: stderrTruncated || diagnosticsTruncated || stderrCapped || !reaped,
		eventsTruncated: truncated || !reaped,
	}
}

func (s *persistentSession) close(ctx context.Context) *safeFailure {
	s.invokeMu.Lock()
	defer s.invokeMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	closeCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := s.closeTimeout
		if timeout <= 0 {
			timeout = DefaultExecutionTimeout
		}
		closeCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	s.closeStdin()
	select {
	case <-s.waitDone:
		s.cleanup()
		if s.killErr != nil {
			return &safeFailure{code: "process_containment_unavailable"}
		}
		return nil
	case <-closeCtx.Done():
		killErr := s.killTree()
		go func() {
			<-s.waitDone
			s.cleanup()
		}()
		if killErr != nil {
			return &safeFailure{code: "process_containment_unavailable"}
		}
		return &safeFailure{code: contextErrorCode(closeCtx)}
	}
}

func (s *persistentSession) killTree() error {
	s.killOnce.Do(func() {
		kill := s.killFunc
		if kill == nil {
			kill = javarunner.KillProcessTree
		}
		if killErr := kill(s.cmd); killErr != nil {
			s.killErr = errors.Join(javarunner.ErrProcessContainmentFailed, killErr)
		}
		if s.killErr != nil && s.containmentFailure != nil {
			// Do not defer the terminal admission gate until cmd.Wait. A failed
			// strong kill may leave the root alive indefinitely, and any other
			// released slot must stop admitting JVMs immediately.
			s.containmentFailure(s.killErr)
		}
	})
	return s.killErr
}

func firstNonEmptyDiagnostic(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func joinDiagnostics(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

func (s *persistentSession) closeStdin() {
	s.stdinOnce.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
	})
}

func (s *persistentSession) cleanup() {
	s.cleanupOnce.Do(func() {
		for index := range s.sessionSecret {
			s.sessionSecret[index] = 0
		}
		_ = os.Remove(s.eventPath)
		_ = os.Remove(s.diagnosticPath)
	})
}

func truncateSessionFile(path string) error {
	file, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}
