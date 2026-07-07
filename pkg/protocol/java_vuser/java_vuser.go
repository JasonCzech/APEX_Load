// Package java_vuser runs governed LoadRunner Java Vuser sidecar actions.
package java_vuser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	defaultRunnerCommand  = config.DefaultJavaVuserRunnerCommand
	defaultMaxProcesses   = config.DefaultJavaVuserMaxProcesses
	defaultActionTimeout  = 30 * time.Second
	defaultHarnessMain    = "Harness"
	javaVuserProtocolName = "java_vuser"
)

var runnerWaitDelay = 2 * time.Second

// processSlots bounds the number of concurrent runner processes per generator.
// It is sized once from the operator-owned APEXLOAD_JAVA_VUSER_MAX_PROCESSES so
// that a single generator-wide cap applies across every java_vuser group,
// regardless of any (now-ignored) script-supplied max_processes value.
var (
	processSlots     chan struct{}
	processSlotsOnce sync.Once
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

func init() {
	protocol.Register(javaVuserProtocolName, func() protocol.Protocol {
		return &JavaVuser{}
	})
}

type JavaVuser struct {
	cfg configuration
}

type configuration struct {
	Enabled        bool
	RunnerCommand  string
	HarnessJar     string
	Classpath      []string
	WorkDir        string
	MaxProcesses   int
	StartupTimeout time.Duration
	ActionTimeout  time.Duration
}

type runCommand struct {
	ActionName       string         `json:"action_name"`
	ActionType       string         `json:"action_type"`
	ClassName        string         `json:"class_name"`
	Method           string         `json:"method"`
	SourceArchiveRef string         `json:"source_archive_ref,omitempty"`
	StartupTimeoutMS int64          `json:"startup_timeout_ms,omitempty"`
	Params           map[string]any `json:"params,omitempty"`
}

type runnerSummary struct {
	Success    *bool  `json:"success,omitempty"`
	Error      string `json:"error,omitempty"`
	ClassName  string `json:"class_name,omitempty"`
	Method     string `json:"method,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type runnerEvent struct {
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
}

func (j *JavaVuser) Name() string {
	return javaVuserProtocolName
}

func (j *JavaVuser) Init(_ context.Context, cfg map[string]any) error {
	// Enablement and the runner command/classpath/working-directory are
	// operator-owned (resolved from process environment), NOT taken from the
	// uploaded script. A script author cannot enable the runner or steer which
	// binary executes; the only script-controlled knobs are non-privileged
	// per-action timeouts.
	operator := config.LoadJavaVuserOperatorConfig()
	j.cfg = configuration{
		Enabled:        operator.Enabled,
		RunnerCommand:  protocol.FirstNonEmpty(operator.RunnerCommand, defaultRunnerCommand),
		HarnessJar:     operator.HarnessJar,
		Classpath:      operator.Classpath,
		WorkDir:        operator.WorkDir,
		MaxProcesses:   operator.MaxProcesses,
		StartupTimeout: durationValue(cfg, "java_vuser_startup_timeout_ms", 0),
		ActionTimeout:  durationValue(cfg, "java_vuser_action_timeout_ms", defaultActionTimeout),
	}
	if j.cfg.MaxProcesses <= 0 {
		j.cfg.MaxProcesses = defaultMaxProcesses
	}
	if j.cfg.ActionTimeout <= 0 {
		j.cfg.ActionTimeout = defaultActionTimeout
	}
	return nil
}

func (j *JavaVuser) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
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
	if !j.cfg.Enabled {
		err := fmt.Errorf("java_vuser runner is disabled; an operator must enable it on this generator via %s=true (script config cannot enable it)", config.EnvJavaVuserRunnerEnabled)
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}

	release, err := acquireProcessSlot(ctx)
	if err != nil {
		finalizeResult(result, start, action, err, "", "", "")
		return result, nil
	}
	defer release()
	queueWait := time.Since(start)
	runStart := time.Now()
	result.StartTime = runStart
	if queueWait > 0 {
		result.Extractions["java_vuser_queue_wait_ms"] = strconv.FormatInt(queueWait.Milliseconds(), 10)
	}

	timeout := actionTimeout(action, j.cfg.ActionTimeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmdPayload := runCommand{
		ActionName:       action.Name,
		ActionType:       action.Type,
		ClassName:        strings.TrimSpace(action.Target),
		Method:           actionMethod(action),
		SourceArchiveRef: stringParam(action.Params, "source_archive_ref"),
		StartupTimeoutMS: j.cfg.StartupTimeout.Milliseconds(),
		Params:           action.Params,
	}
	payload, _ := json.Marshal(cmdPayload)
	args := j.commandArgs(cmdPayload)
	cmd := exec.CommandContext(runCtx, j.cfg.RunnerCommand, args...)
	cmd.WaitDelay = runnerWaitDelay
	if j.cfg.WorkDir != "" {
		cmd.Dir = j.cfg.WorkDir
	}
	eventFile, cleanupEventFile := createRunnerEventFile()
	if cleanupEventFile != nil {
		defer cleanupEventFile()
	}
	if eventFile != "" {
		cmd.Env = append(os.Environ(), "APEXLOAD_JAVA_VUSER_EVENT_FILE="+eventFile)
	}
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	// Cap stdout/stderr capture so a chatty or runaway runner cannot OOM the
	// generator: the action timeout bounds wall-clock, not bytes, and up to
	// MaxProcesses runners capture concurrently.
	stdout := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	stderr := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = startRunnerCommand(ctx, cmd, j.cfg.StartupTimeout)
	if err == nil {
		err = cmd.Wait()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("java_vuser runner timed out after %s", timeout)
	}
	eventStream, eventStreamTruncated := readRunnerEventFile(eventFile)
	finalizeResult(result, runStart, action, err, stdout.buf.String(), stderr.buf.String(), eventStream)
	if stdout.truncated {
		result.ProtocolEvidence["runner_stdout_truncated"] = true
	}
	if stderr.truncated {
		result.ProtocolEvidence["runner_stderr_truncated"] = true
	}
	if eventFile != "" {
		result.ProtocolEvidence["runner_event_stream_bytes"] = len(eventStream)
	}
	if eventStreamTruncated {
		result.ProtocolEvidence["runner_event_stream_truncated"] = true
	}
	return result, nil
}

const defaultRunnerMaxCaptureBytes = 256 << 10 // 256 KiB per stream

// cappedBuffer captures up to max bytes and discards the rest. Write always
// reports the full length so cmd.Run does not abort a still-running child with
// io.ErrShortWrite/SIGPIPE; the overflow is dropped and flagged via truncated.
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
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

func (j *JavaVuser) Teardown(context.Context) error {
	return nil
}

func (j *JavaVuser) HealthCheck(context.Context) error {
	if !j.cfg.Enabled {
		return fmt.Errorf("java_vuser runner is disabled")
	}
	if _, err := exec.LookPath(j.cfg.RunnerCommand); err != nil {
		return fmt.Errorf("java_vuser runner command %q is not available: %w", j.cfg.RunnerCommand, err)
	}
	return nil
}

func (j *JavaVuser) commandArgs(cmd runCommand) []string {
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
	args = append(args, "--class", cmd.ClassName, "--method", cmd.Method)
	if cmd.SourceArchiveRef != "" {
		args = append(args, "--source-archive-ref", cmd.SourceArchiveRef)
	}
	if cmd.StartupTimeoutMS > 0 {
		args = append(args, "--startup-timeout-ms", strconv.FormatInt(cmd.StartupTimeoutMS, 10))
	}
	return args
}

func finalizeResult(result *protocol.Result, start time.Time, action *protocol.Action, err error, stdout string, stderr string, eventStream string) {
	result.Duration = time.Since(start)
	result.ResponseBody = stdout
	result.BytesReceived = int64(len(stdout))
	if result.Extractions == nil {
		result.Extractions = map[string]string{}
	}
	summary, hasSummary := parseRunnerSummary(stdout)
	eventText := eventStream
	if strings.TrimSpace(eventText) == "" {
		eventText = stderr
	}
	events := parseRunnerEvents(eventText)
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
			if err == nil {
				err = fmt.Errorf("java_vuser runner reported failed transaction(s): %s", strings.Join(failedTransactions, ", "))
			}
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

func startRunnerCommand(ctx context.Context, cmd *exec.Cmd, startupTimeout time.Duration) error {
	if startupTimeout <= 0 {
		return cmd.Start()
	}
	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Start()
	}()
	select {
	case err := <-errCh:
		return err
	case <-startCtx.Done():
		return fmt.Errorf("java_vuser runner failed to start within %s", startupTimeout)
	}
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
		if !strings.EqualFold(event.Type, "transaction_end") {
			continue
		}
		status, ok := runnerTransactionStatus(event.Message)
		if !ok || status == 0 {
			continue
		}
		name := strings.TrimSpace(event.Name)
		if name == "" {
			name = "(unnamed)"
		}
		failed = append(failed, fmt.Sprintf("%s=%d", name, status))
	}
	return failed
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
	slots := processSlotChan()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func actionMethod(action *protocol.Action) string {
	method := strings.ToLower(strings.TrimSpace(stringParam(action.Params, "method")))
	if method == "" {
		return "full_lifecycle"
	}
	return method
}

func actionTimeout(action *protocol.Action, fallback time.Duration) time.Duration {
	if timeoutMs := intParam(action.Params, "timeout_ms", 0); timeoutMs > 0 {
		return time.Duration(timeoutMs) * time.Millisecond
	}
	return fallback
}

func stringValue(cfg map[string]any, key string) string {
	if value, ok := cfg[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
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

func stringSliceValue(cfg map[string]any, key string) []string {
	switch value := cfg[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return strings.Split(value, string(filepath.ListSeparator))
	default:
		return nil
	}
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
