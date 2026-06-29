// Package java_vuser runs governed LoadRunner Java Vuser sidecar actions.
package java_vuser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
		finalizeResult(result, start, action, err, "", "")
		return result, nil
	}
	if !j.cfg.Enabled {
		err := fmt.Errorf("java_vuser runner is disabled; an operator must enable it on this generator via %s=true (script config cannot enable it)", config.EnvJavaVuserRunnerEnabled)
		finalizeResult(result, start, action, err, "", "")
		return result, nil
	}

	release, err := acquireProcessSlot(ctx)
	if err != nil {
		finalizeResult(result, start, action, err, "", "")
		return result, nil
	}
	defer release()

	timeout := actionTimeout(action, j.cfg.ActionTimeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmdPayload := runCommand{
		ActionName:       action.Name,
		ActionType:       action.Type,
		ClassName:        strings.TrimSpace(action.Target),
		Method:           actionMethod(action),
		SourceArchiveRef: stringParam(action.Params, "source_archive_ref"),
		Params:           action.Params,
	}
	payload, _ := json.Marshal(cmdPayload)
	args := j.commandArgs(cmdPayload)
	cmd := exec.CommandContext(runCtx, j.cfg.RunnerCommand, args...)
	if j.cfg.WorkDir != "" {
		cmd.Dir = j.cfg.WorkDir
	}
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	// Cap stdout/stderr capture so a chatty or runaway runner cannot OOM the
	// generator: the action timeout bounds wall-clock, not bytes, and up to
	// MaxProcesses runners capture concurrently.
	stdout := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	stderr := &cappedBuffer{max: defaultRunnerMaxCaptureBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("java_vuser runner timed out after %s", timeout)
	}
	finalizeResult(result, start, action, err, stdout.buf.String(), stderr.buf.String())
	if stdout.truncated {
		result.ProtocolEvidence["runner_stdout_truncated"] = true
	}
	if stderr.truncated {
		result.ProtocolEvidence["runner_stderr_truncated"] = true
	}
	return result, nil
}

const defaultRunnerMaxCaptureBytes = 8 << 20 // 8 MiB per stream

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
	if strings.TrimSpace(j.cfg.HarnessJar) != "" {
		args = append(args, "-jar", j.cfg.HarnessJar)
	} else if len(j.cfg.Classpath) > 0 {
		args = append(args, "-cp", strings.Join(j.cfg.Classpath, string(filepath.ListSeparator)), defaultHarnessMain)
	} else if strings.EqualFold(filepath.Base(j.cfg.RunnerCommand), defaultRunnerCommand) {
		args = append(args, defaultHarnessMain)
	}
	args = append(args, "--class", cmd.ClassName, "--method", cmd.Method)
	if cmd.SourceArchiveRef != "" {
		args = append(args, "--source-archive-ref", cmd.SourceArchiveRef)
	}
	return args
}

func finalizeResult(result *protocol.Result, start time.Time, action *protocol.Action, err error, stdout string, stderr string) {
	result.Duration = time.Since(start)
	result.ResponseBody = stdout
	result.BytesReceived = int64(len(stdout))
	if result.Extractions == nil {
		result.Extractions = map[string]string{}
	}
	summary, hasSummary := parseRunnerSummary(stdout)
	events := parseRunnerEvents(stderr)
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
