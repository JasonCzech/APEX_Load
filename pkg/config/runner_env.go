package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Operator-owned environment variables that govern the Java Vuser external
// runner. These are intentionally read from the generator/controller process
// environment — NEVER from uploaded script DSL — so that enabling local JVM
// process execution and steering the runner command/classpath/working
// directory is a privileged, operator-only decision. Script-supplied
// equivalents (config.java_vuser.runner_command, classpath, work_dir,
// harness_jar, enabled) are inert; they cannot enable or redirect execution.
const (
	EnvJavaVuserRunnerEnabled = "APEXLOAD_JAVA_VUSER_RUNNER_ENABLED"
	EnvJavaVuserRunnerCommand = "APEXLOAD_JAVA_VUSER_RUNNER_COMMAND"
	EnvJavaVuserHarnessJar    = "APEXLOAD_JAVA_VUSER_HARNESS_JAR"
	EnvJavaVuserClasspath     = "APEXLOAD_JAVA_VUSER_CLASSPATH"
	EnvJavaVuserWorkDir       = "APEXLOAD_JAVA_VUSER_WORK_DIR"
	EnvJavaVuserMaxProcesses  = "APEXLOAD_JAVA_VUSER_MAX_PROCESSES"
)

// DefaultJavaVuserRunnerCommand is used when the operator enables the runner
// without overriding the command.
const DefaultJavaVuserRunnerCommand = "java"

// DefaultJavaVuserMaxProcesses caps the number of concurrent runner processes
// per generator when the operator does not configure a limit.
const DefaultJavaVuserMaxProcesses = 25

// JavaVuserOperatorConfig is the operator-owned runtime configuration for the
// Java Vuser external runner, resolved entirely from process environment.
type JavaVuserOperatorConfig struct {
	Enabled       bool
	RunnerCommand string
	HarnessJar    string
	Classpath     []string
	WorkDir       string
	MaxProcesses  int
}

// LoadJavaVuserOperatorConfig resolves the Java Vuser runner configuration from
// the operator environment. When the runner is not explicitly enabled the
// returned config has Enabled=false and callers must refuse execution.
func LoadJavaVuserOperatorConfig() JavaVuserOperatorConfig {
	cfg := JavaVuserOperatorConfig{
		Enabled:       envBool(EnvJavaVuserRunnerEnabled),
		RunnerCommand: firstNonEmptyEnv(EnvJavaVuserRunnerCommand, DefaultJavaVuserRunnerCommand),
		HarnessJar:    strings.TrimSpace(os.Getenv(EnvJavaVuserHarnessJar)),
		Classpath:     splitClasspath(os.Getenv(EnvJavaVuserClasspath)),
		WorkDir:       strings.TrimSpace(os.Getenv(EnvJavaVuserWorkDir)),
		MaxProcesses:  envInt(EnvJavaVuserMaxProcesses, DefaultJavaVuserMaxProcesses),
	}
	if cfg.MaxProcesses <= 0 {
		cfg.MaxProcesses = DefaultJavaVuserMaxProcesses
	}
	return cfg
}

// JavaVuserRunnerEnabled reports whether an operator has enabled the Java Vuser
// runner on this process. Used by launch preflight gating so that the gate
// reflects actual runner capability rather than an uploaded-script flag.
func JavaVuserRunnerEnabled() bool {
	return envBool(EnvJavaVuserRunnerEnabled)
}

func envBool(key string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstNonEmptyEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitClasspath(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, string(filepath.ListSeparator))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
