// Package javasidecar implements the credential-minimized loopback HTTP
// contract used by generators to execute reviewed Java Vuser code in a
// separate container.
package javasidecar

import (
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	ContractVersion = "apexload-java-vuser-runner.v1"

	MaxRequestBytes int64 = 1 << 20
	// Three 256KiB streams can expand by up to 6x when JSON escaped. Keep the
	// transport bounded while allowing every independently bounded stream.
	MaxResponseBytes int64 = 5 << 20
	MaxCaptureBytes        = 256 << 10
	// MaxHarnessErrorBytes matches the v2 Harness error-envelope budget and
	// prevents a version-skewed runner from duplicating an oversized diagnostic
	// into the sidecar response.
	MaxHarnessErrorBytes = 64 << 10

	DefaultExecutionTimeout = 30 * time.Second
	MaximumExecutionTimeout = time.Hour

	FeatureExactVMParameters     = "java_vuser_exact_vm_parameters"
	FeatureJDKSelectionV1        = "java_vuser_jdk_selection_v1"
	FeatureDedicatedClassloader  = "java_vuser_dedicated_classloader_v1"
	FeatureValidationOnly        = "java_vuser_validation_only_v1"
	FeaturePhaseLifecycleV1      = protocol.FeatureJavaVuserPhaseLifecycleV1
	FeaturePersistentLifecycleV1 = protocol.FeatureJavaVuserPersistentLifecycleV1
	FeatureSessionFrameTokenV1   = protocol.FeatureJavaVuserSessionFrameTokenV1
	FeatureOwnerFencingV1        = "java_vuser_owner_fencing_v1"

	WarningModernXBootClasspathFallback = "java_vuser_xbootclasspath_modern_fallback"
)

func boundedHarnessError(value string) string {
	if len(value) <= MaxHarnessErrorBytes {
		return value
	}
	const suffix = "...[truncated]"
	return boundedtext.TruncateUTF8(value, MaxHarnessErrorBytes-len(suffix)) + suffix
}

// boundedCaptureField caps a captured stream (or a concatenation of captured
// streams) to MaxCaptureBytes so a single response field cannot exceed the
// per-stream budget the transport-size accounting (MaxResponseBytes) assumes.
// It returns the possibly-truncated value and whether truncation occurred.
func boundedCaptureField(value string) (string, bool) {
	if len(value) <= MaxCaptureBytes {
		return value, false
	}
	const suffix = "...[truncated]"
	return boundedtext.TruncateUTF8(value, MaxCaptureBytes-len(suffix)) + suffix, true
}

// RuntimeSettings contains only script-owned runtime settings. Operator-owned
// runner command, harness, work directory, shared classpath, and process limit
// deliberately do not appear in the wire contract.
type RuntimeSettings struct {
	JDKSelection           string   `json:"jdk_selection,omitempty"`
	JDKHome                string   `json:"jdk_home,omitempty"`
	AdditionalVMParameters []string `json:"additional_vm_parameters,omitempty"`
	Classpath              []string `json:"classpath,omitempty"`
	UseXBootClasspath      bool     `json:"use_xbootclasspath,omitempty"`
	DedicatedClassLoader   bool     `json:"dedicated_class_loader,omitempty"`
	StartupTimeoutMS       int64    `json:"startup_timeout_ms,omitempty"`
}

// Target identifies one Java Actions class/lifecycle method for validation.
type Target struct {
	ClassName string `json:"class_name"`
	Method    string `json:"method,omitempty"`
}

// ActionCommand is the typed harness command. It intentionally exposes no
// arbitrary environment map; the small execution-context fields below are
// translated to known APEXLOAD_* variables by the server.
type ActionCommand struct {
	ActionName       string         `json:"action_name,omitempty"`
	ActionType       string         `json:"action_type,omitempty"`
	ClassName        string         `json:"class_name"`
	Method           string         `json:"method,omitempty"`
	SourceArchiveRef string         `json:"source_archive_ref,omitempty"`
	Params           map[string]any `json:"params,omitempty"`

	VUserID          string `json:"vuser_id,omitempty"`
	GroupName        string `json:"group_name,omitempty"`
	ScenarioID       string `json:"scenario_id,omitempty"`
	IgnoreThinkTime  string `json:"ignore_think_time,omitempty"`
	ThinkTimeLimitMS string `json:"think_time_limit_ms,omitempty"`
}

type HealthResponse struct {
	Contract  string `json:"contract"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
}

type CapabilitiesResponse struct {
	Contract              string   `json:"contract"`
	Healthy               bool     `json:"healthy"`
	ErrorCode             string   `json:"error_code,omitempty"`
	Features              []string `json:"features"`
	MinimumJavaMajor      int      `json:"minimum_java_major"`
	JavaMajor             int      `json:"java_major,omitempty"`
	MaxProcesses          int      `json:"max_processes"`
	MaxExecutionTimeoutMS int64    `json:"max_execution_timeout_ms"`
	MaxCaptureBytes       int      `json:"max_capture_bytes"`
}

type ValidateRequest struct {
	Contract  string          `json:"contract"`
	Runtime   RuntimeSettings `json:"runtime"`
	Targets   []Target        `json:"targets,omitempty"`
	TimeoutMS int64           `json:"timeout_ms,omitempty"`
}

// ValidationIssue is intentionally value-free. Index points to a VM token or
// target, depending on Code, without echoing template-resolved content.
type ValidationIssue struct {
	Code  string `json:"code"`
	Index *int   `json:"index,omitempty"`
}

type ValidateResponse struct {
	Contract     string            `json:"contract"`
	Valid        bool              `json:"valid"`
	Issues       []ValidationIssue `json:"issues,omitempty"`
	WarningCodes []string          `json:"warning_codes,omitempty"`
	JavaMajor    int               `json:"java_major,omitempty"`
}

type ClaimOwnerRequest struct {
	Contract string `json:"contract"`
	OwnerID  string `json:"owner_id"`
}

type ClaimOwnerResponse struct {
	Contract string `json:"contract"`
	OwnerID  string `json:"owner_id"`
	Epoch    string `json:"epoch"`
	Claimed  bool   `json:"claimed"`
}

type ExecutionRequest struct {
	Contract               string          `json:"contract"`
	ExecutionID            string          `json:"execution_id"`
	SessionID              string          `json:"session_id,omitempty"`
	OwnerID                string          `json:"owner_id"`
	OwnerEpoch             string          `json:"owner_epoch"`
	RequireExistingSession bool            `json:"require_existing_session,omitempty"`
	Runtime                RuntimeSettings `json:"runtime"`
	Command                ActionCommand   `json:"command"`
	TimeoutMS              int64           `json:"timeout_ms,omitempty"`
}

type ExecutionResponse struct {
	Contract        string   `json:"contract"`
	ExecutionID     string   `json:"execution_id"`
	Success         bool     `json:"success"`
	ErrorCode       string   `json:"error_code,omitempty"`
	Error           string   `json:"error,omitempty"`
	Stdout          string   `json:"stdout,omitempty"`
	Stderr          string   `json:"stderr,omitempty"`
	Events          string   `json:"events,omitempty"`
	StdoutTruncated bool     `json:"stdout_truncated,omitempty"`
	StderrTruncated bool     `json:"stderr_truncated,omitempty"`
	EventsTruncated bool     `json:"events_truncated,omitempty"`
	DurationMS      int64    `json:"duration_ms"`
	WarningCodes    []string `json:"warning_codes,omitempty"`
}

type CancelResponse struct {
	Contract    string `json:"contract"`
	ExecutionID string `json:"execution_id"`
	Canceled    bool   `json:"canceled"`
}

type CancelRequest struct {
	Contract   string `json:"contract"`
	OwnerID    string `json:"owner_id"`
	OwnerEpoch string `json:"owner_epoch"`
}

type CloseSessionRequest struct {
	Contract   string `json:"contract"`
	OwnerID    string `json:"owner_id"`
	OwnerEpoch string `json:"owner_epoch"`
}

type CloseSessionResponse struct {
	Contract  string `json:"contract"`
	SessionID string `json:"session_id"`
	Closed    bool   `json:"closed"`
}

type ErrorResponse struct {
	Contract  string `json:"contract"`
	ErrorCode string `json:"error_code"`
}
