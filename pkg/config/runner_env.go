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
// process execution and steering the runner command/shared classpath/working
// directory is a privileged, operator-only decision. Script-supplied enabled,
// runner_command, work_dir, harness_jar, and max_processes fields are inert;
// script classpath entries are separately validated and appended by the
// governed runtime.
const (
	EnvJavaVuserRunnerEnabled   = "APEXLOAD_JAVA_VUSER_RUNNER_ENABLED"
	EnvJavaVuserRunnerCommand   = "APEXLOAD_JAVA_VUSER_RUNNER_COMMAND"
	EnvJavaVuserRunnerMode      = "APEXLOAD_JAVA_VUSER_RUNNER_MODE"
	EnvJavaVuserRunnerURL       = "APEXLOAD_JAVA_VUSER_RUNNER_URL"
	EnvJavaVuserDefaultJDKHome  = "APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME"
	EnvJavaVuserHarnessJar      = "APEXLOAD_JAVA_VUSER_HARNESS_JAR"
	EnvJavaVuserClasspath       = "APEXLOAD_JAVA_VUSER_CLASSPATH"
	EnvJavaVuserWorkDir         = "APEXLOAD_JAVA_VUSER_WORK_DIR"
	EnvJavaVuserMaxProcesses    = "APEXLOAD_JAVA_VUSER_MAX_PROCESSES"
	EnvJavaVuserRunnerAuthToken = "APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN"
	EnvJavaVuserCDSManifest     = "APEXLOAD_JAVA_VUSER_CDS_MANIFEST"

	// EnvJavaVuserAllowLegacyJava lowers the runner's Java floor from 17 to 8 so
	// imported Vusers whose middleware only runs on a legacy JVM can execute.
	// The version probe is otherwise the only content check standing between a
	// script-specified JDK home and a forked JVM (there is deliberately no JDK
	// path allowlist), so this is an operator decision, never a script one, and
	// it still refuses anything older than Java 8. It also requires a Java
	// 8-targeted harness jar — see `make java-vuser-runner-legacy`.
	EnvJavaVuserAllowLegacyJava = "APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA"

	// EnvJavaVuserClasspathUploadEnabled opts an operator into honoring
	// user-uploaded classpath JARs for imported Java Vusers. When unset, the
	// upload endpoint is disabled and any attached classpath assets are ignored
	// at runtime, preserving the fail-closed, operator-provisioned model.
	EnvJavaVuserClasspathUploadEnabled = "APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED"
	// EnvJavaVuserClasspathAssetDir points the runner at the controller's import
	// asset store so a local (same-host) sidecar can read uploaded JAR bytes by
	// their file:// storage_ref. Unset for distributed runners, which receive
	// staged bytes instead.
	EnvJavaVuserClasspathAssetDir = "APEXLOAD_JAVA_VUSER_CLASSPATH_ASSET_DIR"

	// Shared JVMs run many Java VUsers of one execution/group in one JVM. The
	// operator enables the policy on the runner sidecar; each script still opts
	// in, and the runner qualifies it (Java 17/21, jar classpath, clean native
	// scan) before staging. Disabled, every Java VUser keeps its own JVM.
	EnvJavaVuserSharedJVM = "APEXLOAD_JAVA_VUSER_SHARED_JVM"
	// EnvJavaVuserSharedJVMVUsersPerJVM is the default lifetime slots per JVM
	// (2-1024); a script may request fewer.
	EnvJavaVuserSharedJVMVUsersPerJVM = "APEXLOAD_JAVA_VUSER_SHARED_JVM_VUSERS_PER_JVM"
	// EnvJavaVuserSharedJVMHeapMB is the -Xmx of each shared JVM in MiB.
	EnvJavaVuserSharedJVMHeapMB = "APEXLOAD_JAVA_VUSER_SHARED_JVM_HEAP_MB"
	// EnvJavaVuserJVMMemoryBudgetMB is the runner-wide admission budget, in MiB,
	// charged by every shared JVM's reservation and every dedicated JVM. Set it
	// to the memory the runner's container can give JVMs.
	EnvJavaVuserJVMMemoryBudgetMB = "APEXLOAD_JAVA_VUSER_JVM_MEMORY_BUDGET_MB"
	// EnvJavaVuserDedicatedJVMMemoryMB is the budget charge of one dedicated JVM.
	EnvJavaVuserDedicatedJVMMemoryMB = "APEXLOAD_JAVA_VUSER_DEDICATED_JVM_MEMORY_MB"
	// Slot API and shim jars for shared JVMs; default beside the harness jar.
	EnvJavaVuserSlotAPIJar  = "APEXLOAD_JAVA_VUSER_SLOT_API_JAR"
	EnvJavaVuserSlotShimJar = "APEXLOAD_JAVA_VUSER_SLOT_SHIM_JAR"

	EnvK8sRestartEnabled           = "APEXLOAD_K8S_RESTART_ENABLED"
	EnvK8sNamespace                = "APEXLOAD_K8S_NAMESPACE"
	EnvK8sControllerDeployment     = "APEXLOAD_K8S_CONTROLLER_DEPLOYMENT"
	EnvK8sGeneratorStatefulSet     = "APEXLOAD_K8S_GENERATOR_STATEFULSET"
	EnvK8sGeneratorSelector        = "APEXLOAD_K8S_GENERATOR_SELECTOR"
	EnvK8sRestartAllowForce        = "APEXLOAD_K8S_RESTART_ALLOW_FORCE"
	DefaultK8sControllerDeployment = "apexload-controller"
	DefaultK8sGeneratorStatefulSet = "apexload-generator"
	DefaultK8sGeneratorSelector    = "app.kubernetes.io/name=apexload-generator"
	ServiceAccountNamespaceFile    = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// DefaultJavaVuserRunnerCommand is used when the operator enables the runner
// without overriding the command.
const DefaultJavaVuserRunnerCommand = "java"

const (
	JavaVuserRunnerModeLocal   = "local"
	JavaVuserRunnerModeSidecar = "sidecar"
	// DefaultJavaVuserRunnerMode preserves the existing in-process child runner
	// unless an operator opts into the credential-minimized loopback sidecar.
	DefaultJavaVuserRunnerMode = JavaVuserRunnerModeLocal
	// DefaultJavaVuserRunnerURL is used only when sidecar mode is selected.
	DefaultJavaVuserRunnerURL = "http://127.0.0.1:8098"
)

const (
	// DefaultJavaVuserMaxProcesses caps the number of concurrent runner
	// processes per generator when the operator does not configure a limit.
	DefaultJavaVuserMaxProcesses = 25
	// MaxJavaVuserMaxProcesses prevents an untrusted or mistaken environment
	// value from causing an unbounded semaphore allocation during runner
	// startup. Deployment schemas enforce the same upper bound.
	MaxJavaVuserMaxProcesses = 10000

	DefaultJavaVuserSharedJVMVUsersPerJVM = 250
	MaxJavaVuserSharedJVMVUsersPerJVM     = 1024
	DefaultJavaVuserSharedJVMHeapMB       = 1024
	DefaultJavaVuserDedicatedJVMMemoryMB  = 256
	DefaultJavaVuserSlotAPIJarName        = "apexload-slot-api.jar"
	DefaultJavaVuserSlotShimJarName       = "apexload-slot-shim.jar"
)

// JavaVuserOperatorConfig is the operator-owned runtime configuration for the
// Java Vuser external runner, resolved entirely from process environment.
type JavaVuserOperatorConfig struct {
	// CDSManifest is an operator-owned, immutable AppCDS identity manifest.
	// It is intentionally absent from script settings and the runner wire API.
	CDSManifest   string
	Enabled       bool
	RunnerCommand string
	// RunnerCommandExplicit distinguishes an operator override from the
	// backward-compatible default "java", allowing internal JDK resolution to
	// try DefaultJDKHome/JAVA_HOME/JRE_HOME before PATH when no override exists.
	RunnerCommandExplicit bool
	RunnerMode            string
	RunnerURL             string
	DefaultJDKHome        string
	// AllowLegacyJava lowers the runner's Java floor from 17 to 8 for legacy
	// middleware. See EnvJavaVuserAllowLegacyJava; it also requires a Java
	// 8-targeted harness jar, since the shipped one is compiled --release 17.
	AllowLegacyJava bool
	HarnessJar      string
	Classpath       []string
	WorkDir         string
	MaxProcesses    int
	AuthToken       string
	// ClasspathUploadEnabled gates honoring user-uploaded classpath JARs. When
	// false, attached JavaClasspathAsset records are ignored and runtime
	// behavior is identical to the pre-feature, fail-closed model.
	ClasspathUploadEnabled bool
	// ClasspathAssetDir is the same-host import asset root a local sidecar reads
	// uploaded JAR bytes from. Empty for distributed runners.
	ClasspathAssetDir string
	// SharedJVM holds the shared-JVM policy; Enabled is false unless the
	// operator set EnvJavaVuserSharedJVM on the runner.
	SharedJVM JavaVuserSharedJVMConfig
}

// JavaVuserSharedJVMConfig is the operator's shared-JVM policy. Values are
// normalized by LoadJavaVuserOperatorConfig; zero MemoryBudgetMB means the
// runner derives a budget from its process ceiling, capped by its container's
// memory limit less a reserve for the runner itself.
type JavaVuserSharedJVMConfig struct {
	Enabled              bool
	VUsersPerJVM         int
	HeapMB               int
	MemoryBudgetMB       int
	DedicatedJVMMemoryMB int
	SlotAPIJar           string
	SlotShimJar          string
}

// K8sRestartOperatorConfig is operator-owned configuration for opt-in
// Kubernetes restart controls. It is deliberately environment-only so uploaded
// scripts or project settings cannot grant the controller cluster privileges.
type K8sRestartOperatorConfig struct {
	Enabled              bool
	Namespace            string
	ControllerDeployment string
	GeneratorStatefulSet string
	GeneratorSelector    string
	AllowForce           bool
}

// LoadJavaVuserOperatorConfig resolves the Java Vuser runner configuration from
// the operator environment. When the runner is not explicitly enabled the
// returned config has Enabled=false and callers must refuse execution.
func LoadJavaVuserOperatorConfig() JavaVuserOperatorConfig {
	runnerCommand := strings.TrimSpace(os.Getenv(EnvJavaVuserRunnerCommand))
	runnerCommandExplicit := runnerCommand != ""
	if !runnerCommandExplicit {
		runnerCommand = DefaultJavaVuserRunnerCommand
	}
	cfg := JavaVuserOperatorConfig{
		CDSManifest:            strings.TrimSpace(os.Getenv(EnvJavaVuserCDSManifest)),
		Enabled:                envBool(EnvJavaVuserRunnerEnabled),
		RunnerCommand:          runnerCommand,
		RunnerCommandExplicit:  runnerCommandExplicit,
		RunnerMode:             strings.ToLower(firstNonEmptyEnv(EnvJavaVuserRunnerMode, DefaultJavaVuserRunnerMode)),
		RunnerURL:              firstNonEmptyEnv(EnvJavaVuserRunnerURL, DefaultJavaVuserRunnerURL),
		DefaultJDKHome:         strings.TrimSpace(os.Getenv(EnvJavaVuserDefaultJDKHome)),
		AllowLegacyJava:        envBool(EnvJavaVuserAllowLegacyJava),
		HarnessJar:             strings.TrimSpace(os.Getenv(EnvJavaVuserHarnessJar)),
		Classpath:              splitClasspath(os.Getenv(EnvJavaVuserClasspath)),
		WorkDir:                strings.TrimSpace(os.Getenv(EnvJavaVuserWorkDir)),
		MaxProcesses:           envInt(EnvJavaVuserMaxProcesses, DefaultJavaVuserMaxProcesses),
		AuthToken:              strings.TrimSpace(os.Getenv(EnvJavaVuserRunnerAuthToken)),
		ClasspathUploadEnabled: envBool(EnvJavaVuserClasspathUploadEnabled),
		ClasspathAssetDir:      strings.TrimSpace(os.Getenv(EnvJavaVuserClasspathAssetDir)),
	}
	if cfg.MaxProcesses <= 0 {
		cfg.MaxProcesses = DefaultJavaVuserMaxProcesses
	} else if cfg.MaxProcesses > MaxJavaVuserMaxProcesses {
		cfg.MaxProcesses = MaxJavaVuserMaxProcesses
	}
	cfg.SharedJVM = loadJavaVuserSharedJVMConfig(cfg.HarnessJar)
	return cfg
}

func loadJavaVuserSharedJVMConfig(harnessJar string) JavaVuserSharedJVMConfig {
	shared := JavaVuserSharedJVMConfig{
		Enabled:              envBool(EnvJavaVuserSharedJVM),
		VUsersPerJVM:         envInt(EnvJavaVuserSharedJVMVUsersPerJVM, DefaultJavaVuserSharedJVMVUsersPerJVM),
		HeapMB:               envInt(EnvJavaVuserSharedJVMHeapMB, DefaultJavaVuserSharedJVMHeapMB),
		MemoryBudgetMB:       envInt(EnvJavaVuserJVMMemoryBudgetMB, 0),
		DedicatedJVMMemoryMB: envInt(EnvJavaVuserDedicatedJVMMemoryMB, DefaultJavaVuserDedicatedJVMMemoryMB),
		SlotAPIJar:           strings.TrimSpace(os.Getenv(EnvJavaVuserSlotAPIJar)),
		SlotShimJar:          strings.TrimSpace(os.Getenv(EnvJavaVuserSlotShimJar)),
	}
	if shared.VUsersPerJVM < 2 || shared.VUsersPerJVM > MaxJavaVuserSharedJVMVUsersPerJVM {
		shared.VUsersPerJVM = DefaultJavaVuserSharedJVMVUsersPerJVM
	}
	if shared.HeapMB < 64 {
		shared.HeapMB = DefaultJavaVuserSharedJVMHeapMB
	}
	if shared.MemoryBudgetMB < 0 {
		shared.MemoryBudgetMB = 0
	}
	if shared.DedicatedJVMMemoryMB < 16 {
		shared.DedicatedJVMMemoryMB = DefaultJavaVuserDedicatedJVMMemoryMB
	}
	if dir := filepath.Dir(strings.TrimSpace(harnessJar)); strings.TrimSpace(harnessJar) != "" {
		if shared.SlotAPIJar == "" {
			shared.SlotAPIJar = filepath.Join(dir, DefaultJavaVuserSlotAPIJarName)
		}
		if shared.SlotShimJar == "" {
			shared.SlotShimJar = filepath.Join(dir, DefaultJavaVuserSlotShimJarName)
		}
	}
	return shared
}

// JavaClasspathUploadEnabled reports whether the operator has opted into
// user-uploaded classpath JARs. The controller/API process uses this to gate
// the upload endpoint without loading the full runner operator config.
func JavaClasspathUploadEnabled() bool {
	return envBool(EnvJavaVuserClasspathUploadEnabled)
}

// LoadK8sRestartOperatorConfig resolves the opt-in Kubernetes restart settings.
// Disabled is the default, and the namespace falls back to the in-cluster
// service-account namespace file when the operator does not set it explicitly.
func LoadK8sRestartOperatorConfig() K8sRestartOperatorConfig {
	return K8sRestartOperatorConfig{
		Enabled:              envBool(EnvK8sRestartEnabled),
		Namespace:            firstNonEmptyEnv(EnvK8sNamespace, serviceAccountNamespace()),
		ControllerDeployment: firstNonEmptyEnv(EnvK8sControllerDeployment, DefaultK8sControllerDeployment),
		GeneratorStatefulSet: firstNonEmptyEnv(EnvK8sGeneratorStatefulSet, DefaultK8sGeneratorStatefulSet),
		GeneratorSelector:    firstNonEmptyEnv(EnvK8sGeneratorSelector, DefaultK8sGeneratorSelector),
		AllowForce:           envBoolDefault(EnvK8sRestartAllowForce, true),
	}
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

func envBoolDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
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

func serviceAccountNamespace() string {
	raw, err := os.ReadFile(ServiceAccountNamespaceFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
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
