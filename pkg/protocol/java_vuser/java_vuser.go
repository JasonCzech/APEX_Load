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
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	defaultRunnerCommand  = "java"
	defaultMaxProcesses   = 25
	defaultActionTimeout  = 30 * time.Second
	defaultHarnessMain    = "Harness"
	javaVuserProtocolName = "java_vuser"
)

var processSlots = make(chan struct{}, defaultMaxProcesses)

func init() {
	protocol.Register(javaVuserProtocolName, func() protocol.Protocol {
		return &JavaVuser{}
	})
}

type JavaVuser struct {
	cfg config
}

type config struct {
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

func (j *JavaVuser) Name() string {
	return javaVuserProtocolName
}

func (j *JavaVuser) Init(_ context.Context, cfg map[string]any) error {
	j.cfg = config{
		Enabled:        boolValue(cfg, "java_vuser_enabled"),
		RunnerCommand:  firstNonEmpty(stringValue(cfg, "java_vuser_runner_command"), defaultRunnerCommand),
		HarnessJar:     stringValue(cfg, "java_vuser_harness_jar"),
		Classpath:      stringSliceValue(cfg, "java_vuser_classpath"),
		WorkDir:        stringValue(cfg, "java_vuser_work_dir"),
		MaxProcesses:   intValue(cfg, "java_vuser_max_processes", defaultMaxProcesses),
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
		err := fmt.Errorf("java_vuser runner is disabled; set config.java_vuser.enabled=true only on governed generator-java runners")
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
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("java_vuser runner timed out after %s", timeout)
	}
	finalizeResult(result, start, action, err, stdout.String(), stderr.String())
	return result, nil
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
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence["protocol"] = javaVuserProtocolName
	result.ProtocolEvidence["runner_stdout_bytes"] = len(stdout)
	result.ProtocolEvidence["runner_stderr_bytes"] = len(stderr)
	if stderr != "" {
		result.ProtocolEvidence["runner_stderr"] = stderr
	}
	if err != nil {
		result.Success = false
		result.StatusCode = 500
		result.ErrorMessage = err.Error()
	}
}

func acquireProcessSlot(ctx context.Context) (func(), error) {
	select {
	case processSlots <- struct{}{}:
		return func() { <-processSlots }, nil
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
