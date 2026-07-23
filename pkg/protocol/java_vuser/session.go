package java_vuser

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Two 128 KiB diagnostic fields can expand to six-byte JSON escapes for every
// captured control byte, so leave enough room for the worst valid envelope.
const maxSessionFrameBytes = 2 << 20

type runnerEnvelope struct {
	Contract          string `json:"contract"`
	Kind              string `json:"kind"`
	RequestID         string `json:"request_id"`
	FrameToken        string `json:"frame_token,omitempty"`
	Success           bool   `json:"success"`
	Error             string `json:"error,omitempty"`
	ClassName         string `json:"class_name,omitempty"`
	Method            string `json:"method,omitempty"`
	DurationMS        int64  `json:"duration_ms,omitempty"`
	Stdout            string `json:"stdout,omitempty"`
	Stderr            string `json:"stderr,omitempty"`
	StdoutTruncated   bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated   bool   `json:"stderr_truncated,omitempty"`
	EventFallbackUsed bool   `json:"event_fallback_used,omitempty"`
	EventsTruncated   bool   `json:"events_truncated,omitempty"`
	Fatal             bool   `json:"fatal,omitempty"`
}

type runnerSession struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	responses  chan runnerEnvelope
	waitDone   chan struct{}
	stdoutDone chan struct{}
	stdoutExit chan error
	stderrDone chan struct{}
	waitErr    error

	sessionSecret [32]byte
	bootstrapped  bool

	eventPath         string
	eventOffset       int64
	eventCleanup      func()
	diagnosticPath    string
	diagnosticCleanup func()

	release     func()
	releaseOnce sync.Once
	stdinOnce   sync.Once
	killOnce    sync.Once
	cleanupOnce sync.Once
	killFunc    func(*exec.Cmd) error
	killErr     error

	stdoutNoise *synchronizedCappedBuffer
	stderrNoise *synchronizedCappedBuffer
}

type synchronizedCappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *synchronizedCappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := b.max - b.buf.Len(); remaining > 0 {
		accepted := len(p)
		if accepted > remaining {
			accepted = remaining
			b.truncated = true
		}
		_, _ = b.buf.Write(p[:accepted])
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *synchronizedCappedBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String(), b.truncated
}

func (b *synchronizedCappedBuffer) markTruncated() {
	b.mu.Lock()
	b.truncated = true
	b.mu.Unlock()
}

func (j *JavaVuser) executePersistent(ctx context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
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
	method := actionMethod(action)
	if !isPersistentLifecycleMethod(method) {
		finalizeResult(result, start, action, fmt.Errorf("unsupported persistent Java Vuser lifecycle method %q", method), "", "", "")
		return result, nil
	}
	if contract := stringParam(action.Params, "event_contract"); contract != "" && contract != javaVuserEventV2 {
		finalizeResult(result, start, action, fmt.Errorf("unsupported Java Vuser event contract %q", contract), "", "", "")
		return result, nil
	}
	timeout, err := actionTimeout(action, j.cfg.ActionTimeout)
	if err != nil {
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}
	if !j.cfg.Enabled {
		err = fmt.Errorf("java_vuser runner is disabled; an operator must enable it on this generator via %s=true (script config cannot enable it)", config.EnvJavaVuserRunnerEnabled)
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}
	if localProcessContainmentFailed() {
		finalizeResult(
			result,
			start,
			action,
			fmt.Errorf("java_vuser persistent runtime is unavailable (process_containment_unavailable)"),
			"",
			"",
			"",
		)
		return result, nil
	}
	j.sessionMu.Lock()
	defer j.sessionMu.Unlock()
	if j.sessionErr != nil {
		finalizeResult(result, start, action, fmt.Errorf("java_vuser persistent session is unusable: %w", j.sessionErr), "", "", "")
		return result, nil
	}
	if j.session == nil && !j.supportsStrongProcessContainmentAvailable(ctx) {
		finalizeResult(result, start, action, fmt.Errorf("java_vuser persistent runtime is unavailable (process_containment_unavailable)"), "", "", "")
		return result, nil
	}
	if err := subprocessguard.ValidateAmbientCredentials(); err != nil {
		finalizeResult(result, start, action, fmt.Errorf("java_vuser runner isolation check failed: %w", err), "", "", "")
		return result, nil
	}
	request := runCommand{
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
	j.decorateRunCommand(&request)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if j.session == nil {
		var queueWait time.Duration
		j.session, queueWait, err = j.startPersistentSession(runCtx, request)
		if err != nil {
			finalizeResult(result, start, action, err, "", "", "")
			return result, nil
		}
		if queueWait > 0 {
			result.Extractions["java_vuser_queue_wait_ms"] = strconv.FormatInt(queueWait.Milliseconds(), 10)
		}
	}

	runStart := time.Now()
	result.StartTime = runStart
	result.CorrelationID = request.RequestID
	envelope, events, eventBytes, eventTruncated, invokeErr := j.session.invoke(runCtx, request)
	if envelope.Fatal && invokeErr == nil {
		message := strings.TrimSpace(envelope.Error)
		if message == "" {
			message = "runner reported no diagnostic"
		}
		invokeErr = fmt.Errorf("java_vuser persistent runner reported a fatal session failure: %s", message)
	}
	if invokeErr != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			invokeErr = fmt.Errorf("java_vuser runner timed out after %s", timeout)
		} else if runCtx.Err() != nil {
			invokeErr = fmt.Errorf("java_vuser runner canceled: %w", runCtx.Err())
		}
		j.sessionErr = invokeErr
		trailingEvents, trailingBytes, trailingTruncated := j.session.abort(runnerWaitDelay, request.RequestID)
		events = mergeRunnerEvents(events, trailingEvents)
		eventBytes += trailingBytes
		eventTruncated = eventTruncated || trailingTruncated
	}
	finalizePersistentResult(result, runStart, action, envelope, events, eventBytes, eventTruncated, invokeErr)
	if emit != nil {
		if err := emitRunnerTransactionsAt(action, events, result.StartTime.Add(result.Duration), eventTruncated, emit); err != nil {
			return result, err
		}
	}
	return result, nil
}

func isPersistentLifecycleMethod(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "init", "vuser_init", "initialize", "action", "run", "end", "vuser_end", "teardown":
		return true
	default:
		return false
	}
}

func (j *JavaVuser) startPersistentSession(ctx context.Context, first runCommand) (*runnerSession, time.Duration, error) {
	if !j.supportsStrongProcessContainmentAvailable(ctx) {
		return nil, 0, fmt.Errorf("java_vuser persistent runtime is unavailable (process_containment_unavailable)")
	}
	sessionSecret, err := newSessionSecret()
	if err != nil {
		return nil, 0, fmt.Errorf("java_vuser persistent runner could not create a session secret")
	}
	queueStart := time.Now()
	release, err := acquireProcessSlot(ctx)
	if err != nil {
		return nil, 0, err
	}
	queueWait := time.Since(queueStart)
	eventPath, eventCleanup := createRunnerEventFile()
	if eventPath == "" {
		release()
		return nil, queueWait, fmt.Errorf("create java_vuser persistent event capture")
	}
	diagnosticPath, diagnosticCleanup := createRunnerDiagnosticFile()
	if diagnosticPath == "" {
		eventCleanup()
		release()
		return nil, queueWait, fmt.Errorf("create java_vuser persistent diagnostic capture")
	}
	s := &runnerSession{
		responses:         make(chan runnerEnvelope, 8),
		waitDone:          make(chan struct{}),
		stdoutDone:        make(chan struct{}),
		stdoutExit:        make(chan error, 1),
		stderrDone:        make(chan struct{}),
		sessionSecret:     sessionSecret,
		eventPath:         eventPath,
		eventCleanup:      eventCleanup,
		diagnosticPath:    diagnosticPath,
		diagnosticCleanup: diagnosticCleanup,
		release:           release,
		stdoutNoise:       &synchronizedCappedBuffer{max: defaultRunnerMaxCaptureBytes},
		stderrNoise:       &synchronizedCappedBuffer{max: defaultRunnerMaxCaptureBytes},
	}

	args, err := j.commandArgs(first)
	if err != nil {
		s.cleanupUnstarted()
		return nil, queueWait, err
	}
	args = append(args, "--session")
	cmd := exec.Command(j.cfg.RunnerCommand, args...)
	javarunner.ConfigureProcessTree(cmd)
	cmd.WaitDelay = runnerWaitDelay
	if j.cfg.WorkDir != "" {
		cmd.Dir = j.cfg.WorkDir
	}
	extraEnvironment := cloneStringMap(j.cfg.Environment)
	if eventPath != "" {
		extraEnvironment["APEXLOAD_JAVA_VUSER_EVENT_FILE"] = eventPath
	}
	if diagnosticPath != "" {
		extraEnvironment["APEXLOAD_JAVA_VUSER_DIAGNOSTIC_FILE"] = diagnosticPath
	}
	cmd.Env = subprocessguard.Environment(extraEnvironment)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.cleanupUnstarted()
		return nil, queueWait, fmt.Errorf("open java_vuser runner stdin: %w", err)
	}
	stdout, stdoutChild, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		s.cleanupUnstarted()
		return nil, queueWait, fmt.Errorf("open java_vuser runner stdout: %w", err)
	}
	stderr, stderrChild, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutChild.Close()
		s.cleanupUnstarted()
		return nil, queueWait, fmt.Errorf("open java_vuser runner stderr: %w", err)
	}
	cmd.Stdout = stdoutChild
	cmd.Stderr = stderrChild
	s.cmd = cmd
	s.stdin = stdin
	if err := j.startPersistentRunnerCommand(ctx, cmd); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutChild.Close()
		_ = stderr.Close()
		_ = stderrChild.Close()
		if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
			// The strong starter launched a target but could not prove complete
			// cleanup. This token is the quarantine lease; releasing it would
			// allow new JVMs while an earlier process tree remains unproven.
			finishLocalProcessCapacity(nil, err)
			j.trackFailedStartReap(cmd, s.cleanup)
		} else {
			s.cleanupUnstarted()
		}
		return nil, queueWait, err
	}
	// The child owns its inherited writer descriptors after Start. Keeping the
	// parent copies open would prevent the readers from ever observing EOF.
	_ = stdoutChild.Close()
	_ = stderrChild.Close()
	go func() {
		defer stdout.Close()
		s.readStdout(stdout)
	}()
	go func() {
		defer close(s.stderrDone)
		defer stderr.Close()
		_, _ = io.Copy(s.stderrNoise, stderr)
	}()
	go func() {
		// Caller-owned pipes let Wait reap the direct JVM without closing a
		// reader under a concurrent scan. Once it exits, kill any native
		// descendants that inherited the writers, then drain both streams before
		// publishing process completion or releasing capacity.
		s.waitErr = cmd.Wait()
		containmentErr := s.killTree()
		s.waitErr = errors.Join(s.waitErr, containmentErr)
		if !javarunner.WaitForOutputDrain(
			runnerWaitDelay,
			[]io.Closer{stdout, stderr},
			s.stdoutDone,
			s.stderrDone,
		) {
			s.stdoutNoise.markTruncated()
			s.stderrNoise.markTruncated()
		}
		if containmentErr != nil {
			finishLocalProcessCapacity(nil, containmentErr)
		} else {
			s.releaseSlot()
		}
		close(s.waitDone)
	}()
	return s, queueWait, nil
}

func (j *JavaVuser) supportsStrongProcessContainmentAvailable(ctx context.Context) bool {
	release, err := acquireLocalRuntimeProbe(ctx)
	if err != nil {
		return false
	}
	var available bool
	if j.supportsStrongProcessContainment != nil {
		available = j.supportsStrongProcessContainment()
	} else {
		available = javarunner.SupportsStrongProcessContainment()
	}
	return finishLocalStrongContainmentProbe(
		release,
		available,
		javarunner.ProcessContainmentFailed(),
	)
}

func finishLocalStrongContainmentProbe(release func(), available, containmentFailed bool) bool {
	if containmentFailed {
		// Keep the probe token as a quarantine lease. The OS capability check
		// launches a real process on platforms such as Linux; if its cleanup
		// cannot be proven, releasing the probe slot would let later checks
		// create more potentially uncontained descendants.
		markLocalContainmentFailed()
		return false
	}
	if release != nil {
		release()
	}
	return available
}

func (j *JavaVuser) startPersistentRunnerCommand(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	start := j.startStrongProcessTree
	if start == nil {
		start = javarunner.StartStrongProcessTree
	}
	if err := start(cmd); err != nil {
		if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
			// Preserve the terminal cleanup classification without reflecting
			// platform details into action or API diagnostics.
			return fmt.Errorf(
				"java_vuser persistent runtime is unavailable (process_containment_unavailable): %w",
				javarunner.ErrProcessContainmentFailed,
			)
		}
		if errors.Is(err, javarunner.ErrStrongProcessContainmentUnavailable) {
			return fmt.Errorf(
				"java_vuser persistent runtime is unavailable (process_containment_unavailable): %w",
				javarunner.ErrStrongProcessContainmentUnavailable,
			)
		}
		return err
	}
	return nil
}

func (s *runnerSession) readStdout(stdout io.Reader) {
	var scanErr error
	defer func() {
		s.stdoutExit <- scanErr
		close(s.stdoutDone)
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxSessionFrameBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, sessionFramePrefix) {
			_, _ = s.stdoutNoise.Write(append([]byte(line), '\n'))
			continue
		}
		var envelope runnerEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, sessionFramePrefix)), &envelope); err != nil {
			_, _ = s.stdoutNoise.Write(append([]byte(line), '\n'))
			continue
		}
		select {
		case s.responses <- envelope:
		default:
			_, _ = s.stdoutNoise.Write([]byte("java_vuser response queue overflow\n"))
		}
	}
	if err := scanner.Err(); err != nil {
		scanErr = err
		_, _ = s.stdoutNoise.Write([]byte("java_vuser stdout framing error: " + err.Error() + "\n"))
	}
}

func (s *runnerSession) invoke(ctx context.Context, command runCommand) (runnerEnvelope, []runnerEvent, int, bool, error) {
	if err := s.resetEventFile(); err != nil {
		return runnerEnvelope{}, nil, 0, false, fmt.Errorf("reset java_vuser event stream: %w", err)
	}
	if err := resetRunnerCaptureFile(s.diagnosticPath); err != nil {
		return runnerEnvelope{}, nil, 0, false, fmt.Errorf("reset java_vuser diagnostic stream: %w", err)
	}
	expectedToken := sessionFrameToken(s.sessionSecret, command.RequestID)
	if !s.bootstrapped {
		command.SessionSecret = hex.EncodeToString(s.sessionSecret[:])
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return runnerEnvelope{}, nil, 0, false, err
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := s.stdin.Write(append(payload, '\n'))
		writeDone <- writeErr
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			return runnerEnvelope{}, nil, 0, false, fmt.Errorf("write java_vuser session command: %w", err)
		}
		s.bootstrapped = true
	case <-s.waitDone:
		if envelope, ok := s.responseAfterExit(ctx, command.RequestID, expectedToken); ok {
			events, eventBytes, truncated := s.readEventDelta(command.RequestID, envelope.Stderr, envelope.StderrTruncated)
			truncated = envelopeEventCaptureTruncated(envelope, truncated)
			return envelope, events, eventBytes, truncated, nil
		}
		events, eventBytes, truncated := s.readFailureEventDelta(command.RequestID)
		return s.failureEnvelope(command.RequestID), events, eventBytes, truncated, s.processExitError()
	case framingErr := <-s.stdoutExit:
		events, eventBytes, truncated := s.readFailureEventDelta(command.RequestID)
		return s.failureEnvelope(command.RequestID), events, eventBytes, truncated, s.stdoutExitError(framingErr)
	case <-ctx.Done():
		events, eventBytes, truncated := s.readFailureEventDelta(command.RequestID)
		return s.failureEnvelope(command.RequestID), events, eventBytes, truncated, ctx.Err()
	}

	for {
		select {
		case envelope := <-s.responses:
			if envelope.RequestID != command.RequestID {
				continue
			}
			if !sessionFrameTokenMatches(envelope.FrameToken, expectedToken) {
				continue
			}
			if envelope.Contract != javaVuserIPCContract || envelope.Kind != "result" {
				return runnerEnvelope{}, nil, 0, false, fmt.Errorf("java_vuser runner returned an unsupported session envelope")
			}
			events, eventBytes, truncated := s.readEventDelta(command.RequestID, envelope.Stderr, envelope.StderrTruncated)
			truncated = envelopeEventCaptureTruncated(envelope, truncated)
			return envelope, events, eventBytes, truncated, nil
		case <-s.waitDone:
			if envelope, ok := s.responseAfterExit(ctx, command.RequestID, expectedToken); ok {
				events, eventBytes, truncated := s.readEventDelta(command.RequestID, envelope.Stderr, envelope.StderrTruncated)
				truncated = envelopeEventCaptureTruncated(envelope, truncated)
				return envelope, events, eventBytes, truncated, nil
			}
			events, eventBytes, truncated := s.readFailureEventDelta(command.RequestID)
			return s.failureEnvelope(command.RequestID), events, eventBytes, truncated, s.processExitError()
		case framingErr := <-s.stdoutExit:
			events, eventBytes, truncated := s.readFailureEventDelta(command.RequestID)
			return s.failureEnvelope(command.RequestID), events, eventBytes, truncated, s.stdoutExitError(framingErr)
		case <-ctx.Done():
			events, eventBytes, truncated := s.readFailureEventDelta(command.RequestID)
			return s.failureEnvelope(command.RequestID), events, eventBytes, truncated, ctx.Err()
		}
	}
}

func envelopeEventCaptureTruncated(envelope runnerEnvelope, truncated bool) bool {
	return truncated || envelope.EventsTruncated ||
		(envelope.EventFallbackUsed && envelope.StderrTruncated)
}

func (s *runnerSession) stdoutExitError(err error) error {
	if err != nil {
		return fmt.Errorf("java_vuser runner stdout framing failed: %w", err)
	}
	select {
	case <-s.waitDone:
		return s.processExitError()
	default:
		return fmt.Errorf("java_vuser runner closed its protocol output before returning a result")
	}
}

func (s *runnerSession) failureEnvelope(requestID string) runnerEnvelope {
	diagnostic, truncated := s.readDiagnostics()
	return runnerEnvelope{
		Contract:        javaVuserIPCContract,
		Kind:            "result",
		RequestID:       requestID,
		Stderr:          diagnostic,
		StderrTruncated: truncated,
		Fatal:           true,
	}
}

func (s *runnerSession) responseAfterExit(ctx context.Context, requestID, expectedToken string) (runnerEnvelope, bool) {
	select {
	case <-s.stdoutDone:
	case <-ctx.Done():
		return runnerEnvelope{}, false
	}
	for {
		select {
		case envelope := <-s.responses:
			if envelope.RequestID == requestID &&
				sessionFrameTokenMatches(envelope.FrameToken, expectedToken) &&
				envelope.Contract == javaVuserIPCContract && envelope.Kind == "result" {
				return envelope, true
			}
		default:
			return runnerEnvelope{}, false
		}
	}
}

func newSessionSecret() ([32]byte, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return [32]byte{}, err
	}
	return value, nil
}

func sessionFrameToken(secret [32]byte, requestID string) string {
	mac := hmac.New(sha256.New, secret[:])
	_, _ = mac.Write([]byte(javaVuserIPCContract))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(requestID))
	return hex.EncodeToString(mac.Sum(nil))
}

func sessionFrameTokenMatches(actual, expected string) bool {
	if len(actual) != len(expected) || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func (s *runnerSession) resetEventFile() error {
	s.eventOffset = 0
	return resetRunnerCaptureFile(s.eventPath)
}

func resetRunnerCaptureFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func (s *runnerSession) readEventDelta(requestID, stderrFallback string, stderrTruncated bool) ([]runnerEvent, int, bool) {
	var data []byte
	truncated := false
	if s.eventPath != "" {
		if file, err := os.Open(s.eventPath); err == nil {
			if info, statErr := file.Stat(); statErr == nil && info.Size() > s.eventOffset {
				remaining := info.Size() - s.eventOffset
				limit := remaining
				if limit > defaultRunnerMaxCaptureBytes {
					limit = defaultRunnerMaxCaptureBytes
					truncated = true
				}
				data = make([]byte, limit)
				n, _ := file.ReadAt(data, s.eventOffset)
				data = data[:n]
				s.eventOffset = info.Size()
			}
			_ = file.Close()
		}
	}
	fileEvents := filterV2RunnerEvents(parseRunnerEvents(string(data)), requestID)
	fallbackEvents := filterV2RunnerEvents(parseRunnerEvents(stderrFallback), requestID)
	events := mergeRunnerEvents(fileEvents, fallbackEvents)
	eventBytes := len(data)
	if len(fallbackEvents) > 0 {
		// The shim mirrors an event to stderr only when its event-file append
		// failed. Preserve those later exact-v2 events even if the file already
		// contains earlier ones, and propagate any diagnostic truncation because
		// it may have cut away additional fallback events.
		eventBytes += len(stderrFallback)
		truncated = truncated || stderrTruncated
	}
	return events, eventBytes, truncated
}

func (s *runnerSession) readFailureEventDelta(requestID string) ([]runnerEvent, int, bool) {
	diagnostics, diagnosticsTruncated := s.readDiagnostics()
	return s.readEventDelta(requestID, diagnostics, diagnosticsTruncated)
}

func filterV2RunnerEvents(events []runnerEvent, requestID string) []runnerEvent {
	filtered := make([]runnerEvent, 0, len(events))
	for _, event := range events {
		if event.Contract == javaVuserEventV2 && event.RequestID == requestID {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func mergeRunnerEvents(streams ...[]runnerEvent) []runnerEvent {
	merged := make([]runnerEvent, 0)
	seen := make(map[string]struct{})
	for _, stream := range streams {
		for _, event := range stream {
			key := strings.Join([]string{
				event.Contract, event.RequestID, strconv.FormatInt(event.Sequence, 10),
				strconv.FormatInt(event.TimestampMS, 10), event.TransactionID,
				event.ParentTransactionID,
				optionalInt64Key(event.DurationMS),
				optionalInt64Key(event.RawDurationMS),
				optionalInt64Key(event.ThinkTimeMS),
				optionalInt64Key(event.WastedTimeMS),
				event.Type, event.Name, event.Message,
			}, "\x00")
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, event)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		left, right := merged[i], merged[j]
		if left.Sequence != right.Sequence {
			if left.Sequence <= 0 {
				return false
			}
			if right.Sequence <= 0 {
				return true
			}
			return left.Sequence < right.Sequence
		}
		if left.TimestampMS > 0 && right.TimestampMS > 0 && left.TimestampMS != right.TimestampMS {
			return left.TimestampMS < right.TimestampMS
		}
		return false
	})
	return merged
}

func optionalInt64Key(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func (s *runnerSession) processExitError() error {
	stdout, _ := s.stdoutNoise.snapshot()
	stderr, _ := s.stderrNoise.snapshot()
	diagnostics, _ := s.readDiagnostics()
	detail := strings.TrimSpace(strings.Join([]string{stdout, stderr, diagnostics}, "\n"))
	if len(detail) > 512 {
		detail = detail[:512]
	}
	if s.waitErr == nil {
		if detail == "" {
			return fmt.Errorf("java_vuser persistent runner exited before returning a result")
		}
		return fmt.Errorf("java_vuser persistent runner exited before returning a result: %s", detail)
	}
	if detail == "" {
		return fmt.Errorf("java_vuser persistent runner exited: %w", s.waitErr)
	}
	return fmt.Errorf("java_vuser persistent runner exited: %w: %s", s.waitErr, detail)
}

func (s *runnerSession) abort(timeout time.Duration, requestID string) ([]runnerEvent, int, bool) {
	_ = s.killTree()
	s.closeStdin()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case <-s.waitDone:
	case <-ctx.Done():
		go func() {
			<-s.waitDone
			s.cleanup()
		}()
	}
	events, eventBytes, truncated := s.readFailureEventDelta(requestID)
	select {
	case <-s.waitDone:
		s.cleanup()
	default:
	}
	return events, eventBytes, truncated
}

func (s *runnerSession) close(ctx context.Context, fallback time.Duration) error {
	s.closeStdin()
	waitCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		if fallback <= 0 {
			fallback = defaultActionTimeout
		} else if fallback > defaultActionTimeout {
			fallback = defaultActionTimeout
		}
		waitCtx, cancel = context.WithTimeout(ctx, fallback)
	}
	defer cancel()
	select {
	case <-s.waitDone:
		s.cleanup()
		return errors.Join(s.waitErr, s.killErr)
	case <-waitCtx.Done():
		killErr := s.killTree()
		select {
		case <-s.waitDone:
			s.cleanup()
		case <-time.After(runnerWaitDelay):
			go func() {
				<-s.waitDone
				s.cleanup()
			}()
		}
		return errors.Join(
			fmt.Errorf("java_vuser persistent runner teardown: %w", waitCtx.Err()),
			killErr,
		)
	}
}

func (s *runnerSession) killTree() error {
	s.killOnce.Do(func() {
		kill := s.killFunc
		if kill == nil {
			kill = javarunner.KillProcessTree
		}
		if killErr := kill(s.cmd); killErr != nil {
			s.killErr = errors.Join(javarunner.ErrProcessContainmentFailed, killErr)
			// A failed whole-tree kill is terminal even when the direct JVM has
			// not exited yet. Waiting for cmd.Wait before closing the admission
			// gate lets other free slots start more JVMs while containment is
			// already unproven. Keep this session's token quarantined; the wait
			// goroutine will perform the same idempotent disposition if it later
			// observes process exit.
			finishLocalProcessCapacity(nil, s.killErr)
		}
	})
	return s.killErr
}

func (s *runnerSession) closeStdin() {
	s.stdinOnce.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
	})
}

func (s *runnerSession) releaseSlot() {
	s.releaseOnce.Do(func() {
		if s.release != nil {
			s.release()
		}
	})
}

func (s *runnerSession) cleanup() {
	s.cleanupOnce.Do(func() {
		for index := range s.sessionSecret {
			s.sessionSecret[index] = 0
		}
		if s.eventCleanup != nil {
			s.eventCleanup()
		}
		if s.diagnosticCleanup != nil {
			s.diagnosticCleanup()
		}
	})
}

func (s *runnerSession) cleanupUnstarted() {
	s.releaseSlot()
	s.cleanup()
}

func (s *runnerSession) readDiagnostics() (string, bool) {
	if strings.TrimSpace(s.diagnosticPath) == "" {
		return "", false
	}
	file, err := os.Open(s.diagnosticPath)
	if err != nil {
		return "", false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(defaultRunnerMaxCaptureBytes)+1))
	if err != nil {
		return "", false
	}
	if len(data) > defaultRunnerMaxCaptureBytes {
		return string(data[:defaultRunnerMaxCaptureBytes]), true
	}
	return string(data), false
}

func createRunnerDiagnosticFile() (string, func()) {
	file, err := os.CreateTemp("", "apexload-java-vuser-diagnostics-*.log")
	if err != nil {
		return "", nil
	}
	path := file.Name()
	_ = file.Close()
	return path, func() { _ = os.Remove(path) }
}

func finalizePersistentResult(result *protocol.Result, start time.Time, action *protocol.Action, envelope runnerEnvelope, events []runnerEvent, eventBytes int, eventTruncated bool, err error) {
	result.Duration = time.Since(start)
	result.ResponseBody = envelope.Stdout
	result.BytesReceived = int64(len(envelope.Stdout))
	if envelope.RequestID != "" {
		result.CorrelationID = envelope.RequestID
	}
	if result.Extractions == nil {
		result.Extractions = map[string]string{}
	}
	if envelope.DurationMS > 0 {
		result.Extractions["java_vuser_duration_ms"] = strconv.FormatInt(envelope.DurationMS, 10)
	}
	if err == nil && !envelope.Success {
		message := strings.TrimSpace(envelope.Error)
		if message == "" {
			message = "runner reported success=false"
		}
		err = fmt.Errorf("java_vuser runner reported failure: %s", message)
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
		failed := failedRunnerTransactions(events)
		if len(failed) > 0 {
			result.Extractions["java_vuser_failed_transaction_count"] = strconv.Itoa(len(failed))
		}
		streamFailures := runnerTransactionStreamFailures(events)
		if len(streamFailures) > 0 {
			result.Extractions["java_vuser_transaction_diagnostic_count"] = strconv.Itoa(len(streamFailures))
		}
	}
	if eventTruncated {
		result.Extractions["java_vuser_event_stream_truncated"] = "true"
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
	success := envelope.Success
	requestID := envelope.RequestID
	if requestID == "" {
		requestID = result.CorrelationID
	}
	result.ProtocolEvidence["protocol"] = javaVuserProtocolName
	result.ProtocolEvidence["runner_contract"] = javaVuserIPCContract
	result.ProtocolEvidence["runner_event_contract"] = javaVuserEventV2
	result.ProtocolEvidence["runner_request_id"] = requestID
	result.ProtocolEvidence["runner_stdout_bytes"] = len(envelope.Stdout)
	result.ProtocolEvidence["runner_stderr_bytes"] = len(envelope.Stderr)
	result.ProtocolEvidence["runner_event_stream_bytes"] = eventBytes
	result.ProtocolEvidence["runner_result"] = runnerSummary{
		Contract: envelope.Contract, Kind: envelope.Kind, RequestID: requestID,
		Success: &success, Error: envelope.Error, ClassName: envelope.ClassName,
		Method: envelope.Method, DurationMS: envelope.DurationMS,
		EventFallbackUsed: envelope.EventFallbackUsed,
		EventsTruncated:   envelope.EventsTruncated,
	}
	if len(events) > 0 {
		result.ProtocolEvidence["runner_events"] = events
		result.ProtocolEvidence["runner_event_count"] = len(events)
	}
	if envelope.Stderr != "" {
		result.ProtocolEvidence["runner_stderr"] = envelope.Stderr
	}
	if envelope.StdoutTruncated {
		result.ProtocolEvidence["runner_stdout_truncated"] = true
	}
	if envelope.StderrTruncated {
		result.ProtocolEvidence["runner_stderr_truncated"] = true
	}
	if eventTruncated {
		result.ProtocolEvidence["runner_event_stream_truncated"] = true
	}
}
