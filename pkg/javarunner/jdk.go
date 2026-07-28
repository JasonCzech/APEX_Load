package javarunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
)

const (
	// MinimumJavaMajor is the oldest Java runtime supported by the Java Vuser
	// harness and runner contract.
	MinimumJavaMajor = 17
	// MinimumLegacyJavaMajor is the floor that still applies when an operator
	// opts into the legacy lane. The opt-in exists for middleware that only runs
	// on Java 8; it is not a way to disable version checking, and nothing older
	// than 8 is accepted regardless of the flag.
	MinimumLegacyJavaMajor = 8
	defaultProbeTimeout    = 5 * time.Second
	maxProbeOutputBytes    = 32 << 10
)

// EffectiveMinimumJavaMajor is the version floor for a given legacy opt-in.
// Callers that gate on a probed major version — including the controller-side
// sidecar capability check — must use this rather than MinimumJavaMajor, or the
// opt-in silently fails somewhere other than where it was configured.
func EffectiveMinimumJavaMajor(allowLegacyJava bool) int {
	if allowLegacyJava {
		return MinimumLegacyJavaMajor
	}
	return MinimumJavaMajor
}

type JDKSelection string

const (
	JDKSelectionInternal  JDKSelection = "internal"
	JDKSelectionSpecified JDKSelection = "specified"
)

type JDKSource string

const (
	JDKSourceSpecifiedHome  JDKSource = "specified_home"
	JDKSourceExplicitRunner JDKSource = "explicit_runner"
	JDKSourceDefaultHome    JDKSource = "default_home"
	JDKSourceJavaHome       JDKSource = "java_home"
	JDKSourceJREHome        JDKSource = "jre_home"
	JDKSourcePath           JDKSource = "path"
)

// JavaVMFamily identifies the VM option dialect reported by the selected
// launcher. HotSpot includes OpenJDK/Oracle/GraalVM HotSpot-derived runtimes;
// OpenJ9 includes Eclipse OpenJ9 and IBM J9-derived runtimes.
type JavaVMFamily string

const (
	JavaVMUnknown JavaVMFamily = ""
	JavaVMHotSpot JavaVMFamily = "hotspot"
	JavaVMOpenJ9  JavaVMFamily = "openj9"
)

var ErrUnsupportedRunnerCommand = errors.New("runner command is not a direct java launcher")

// ResolveOptions describes one executor-local JDK resolution. An empty
// Selection means internal. ExplicitJavaCommand must contain only an executable
// name or path (no arguments), and callers should populate it only for an
// explicitly configured direct java/java.exe override. Legacy wrapper commands
// must bypass resolution when they are permitted for compatibility.
type ResolveOptions struct {
	Selection           JDKSelection
	SpecifiedHome       string
	ExplicitJavaCommand string
	DefaultHome         string
	// AllowLegacyJava is the operator's legacy opt-in, never a script's. It
	// lowers the accepted major version to MinimumLegacyJavaMajor.
	AllowLegacyJava bool
	ProbeTimeout    time.Duration
}

// ResolvedJDK is a canonical, probed Java installation. JavaBin has symlinks
// resolved. For home-based candidates, Home is the canonical selected home;
// for a direct launcher candidate it is derived from the canonical binary.
type ResolvedJDK struct {
	Home     string
	JavaBin  string
	Version  string
	Major    int
	VMFamily JavaVMFamily
	Source   JDKSource
}

// EnvironmentOverrides returns the child-process environment values associated
// with this JDK. The resolved JDK bin directory is prepended to currentPath.
func (jdk ResolvedJDK) EnvironmentOverrides(currentPath string) map[string]string {
	binDir := filepath.Join(jdk.Home, "bin")
	pathValue := binDir
	if currentPath != "" {
		pathValue += string(filepath.ListSeparator) + currentPath
	}
	return map[string]string{
		"JAVA_HOME": jdk.Home,
		"JRE_HOME":  jdk.Home,
		"PATH":      pathValue,
	}
}

// ResolveJDK resolves and probes a Java runtime at or above
// EffectiveMinimumJavaMajor(options.AllowLegacyJava) — Java 17 by default, or
// Java 8 under the operator's legacy opt-in. Specified mode has no fallback.
// Internal mode tries, in order, an explicit direct Java override, the operator
// default home, JAVA_HOME, JRE_HOME, and PATH, returning the first installation
// that satisfies the floor.
func ResolveJDK(ctx context.Context, options ResolveOptions) (ResolvedJDK, error) {
	selection := options.Selection
	if selection == "" {
		selection = JDKSelectionInternal
	}
	timeout := options.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}

	switch selection {
	case JDKSelectionSpecified:
		return resolveSpecifiedJDK(ctx, strings.TrimSpace(options.SpecifiedHome), timeout, options.AllowLegacyJava)
	case JDKSelectionInternal:
		return resolveInternalJDK(ctx, options, timeout)
	default:
		return ResolvedJDK{}, fmt.Errorf("unsupported JDK selection %q", selection)
	}
}

func resolveSpecifiedJDK(ctx context.Context, home string, timeout time.Duration, allowLegacyJava bool) (ResolvedJDK, error) {
	if home == "" {
		return ResolvedJDK{}, fmt.Errorf("specified JDK home is required")
	}
	if !filepath.IsAbs(home) {
		return ResolvedJDK{}, fmt.Errorf("specified JDK home is not absolute on this executor")
	}
	javaBin, resolvedHome, err := javaBinaryInHome(home)
	if err != nil {
		return ResolvedJDK{}, fmt.Errorf("resolve specified JDK home: %w", err)
	}
	return probeResolvedJDK(ctx, javaBin, resolvedHome, JDKSourceSpecifiedHome, timeout, allowLegacyJava)
}

func resolveInternalJDK(ctx context.Context, options ResolveOptions, timeout time.Duration) (ResolvedJDK, error) {
	return resolveInternalJDKWithProbe(ctx, options, timeout, probeResolvedJDK)
}

func resolveInternalJDKWithProbe(
	ctx context.Context,
	options ResolveOptions,
	timeout time.Duration,
	probe func(context.Context, string, string, JDKSource, time.Duration, bool) (ResolvedJDK, error),
) (ResolvedJDK, error) {
	type candidate struct {
		source  JDKSource
		command string
		home    string
	}
	candidates := make([]candidate, 0, 5)
	if command := strings.TrimSpace(options.ExplicitJavaCommand); command != "" {
		if !IsDirectJavaCommand(command) {
			return ResolvedJDK{}, fmt.Errorf("%w: explicit runner", ErrUnsupportedRunnerCommand)
		}
		candidates = append(candidates, candidate{source: JDKSourceExplicitRunner, command: command})
	}
	if home := strings.TrimSpace(options.DefaultHome); home != "" {
		candidates = append(candidates, candidate{source: JDKSourceDefaultHome, home: home})
	}
	if home := strings.TrimSpace(os.Getenv("JAVA_HOME")); home != "" {
		candidates = append(candidates, candidate{source: JDKSourceJavaHome, home: home})
	}
	if home := strings.TrimSpace(os.Getenv("JRE_HOME")); home != "" {
		candidates = append(candidates, candidate{source: JDKSourceJREHome, home: home})
	}
	for _, name := range javaExecutableNames() {
		candidates = append(candidates, candidate{source: JDKSourcePath, command: name})
	}

	failures := make([]error, 0, len(candidates))
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return ResolvedJDK{}, err
		}
		var javaBin, home string
		var err error
		if candidate.home != "" {
			javaBin, home, err = javaBinaryInHome(candidate.home)
		} else {
			javaBin, home, err = resolveDirectJavaCommand(candidate.command)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", candidate.source, err))
			continue
		}
		if _, exists := seen[javaBin]; exists {
			continue
		}
		seen[javaBin] = struct{}{}
		jdk, err := probe(ctx, javaBin, home, candidate.source, timeout, options.AllowLegacyJava)
		if err == nil {
			return jdk, nil
		}
		// A post-start containment failure is terminal for the owning
		// executor. Trying another JDK candidate could launch another process
		// tree while the first one's cleanup is still unproven.
		if errors.Is(err, ErrProcessContainmentFailed) {
			return ResolvedJDK{}, fmt.Errorf("%s: %w", candidate.source, err)
		}
		if ctx.Err() != nil {
			return ResolvedJDK{}, ctx.Err()
		}
		failures = append(failures, fmt.Errorf("%s: %w", candidate.source, err))
	}
	minimumMajor := EffectiveMinimumJavaMajor(options.AllowLegacyJava)
	if len(failures) == 0 {
		return ResolvedJDK{}, fmt.Errorf("no Java %d+ runtime candidates were configured", minimumMajor)
	}
	return ResolvedJDK{}, fmt.Errorf("no compatible Java %d+ runtime found: %w", minimumMajor, errors.Join(failures...))
}

// IsDirectJavaCommand reports whether command names the Java launcher itself,
// rather than an operator wrapper. The command must not contain arguments.
func IsDirectJavaCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\x00\r\n") {
		return false
	}
	base := strings.ToLower(filepath.Base(command))
	return base == "java" || base == "java.exe"
}

func javaBinaryInHome(home string) (javaBin string, resolvedHome string, err error) {
	if !filepath.IsAbs(home) {
		return "", "", fmt.Errorf("JDK home is not absolute on this executor")
	}
	home, err = filepath.Abs(filepath.Clean(home))
	if err != nil {
		return "", "", err
	}
	if resolved, evalErr := filepath.EvalSymlinks(home); evalErr == nil {
		home = resolved
	} else {
		return "", "", evalErr
	}
	for _, name := range javaExecutableNames() {
		candidate := filepath.Join(home, "bin", name)
		resolved, resolveErr := canonicalJavaBinary(candidate)
		if resolveErr != nil {
			continue
		}
		return resolved, home, nil
	}
	return "", "", fmt.Errorf("home does not contain an executable bin/java launcher")
}

func resolveDirectJavaCommand(command string) (javaBin string, home string, err error) {
	if !IsDirectJavaCommand(command) {
		return "", "", ErrUnsupportedRunnerCommand
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return "", "", fmt.Errorf("locate Java launcher: %w", err)
	}
	resolved, err := canonicalJavaBinary(path)
	if err != nil {
		return "", "", err
	}
	return resolved, canonicalHomeFromJava(resolved), nil
}

func canonicalJavaBinary(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve Java launcher: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat Java launcher: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Java launcher is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("Java launcher is not executable")
	}
	return resolved, nil
}

func canonicalHomeFromJava(javaBin string) string {
	return filepath.Dir(filepath.Dir(javaBin))
}

func javaExecutableNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"java.exe", "java"}
	}
	return []string{"java", "java.exe"}
}

func probeResolvedJDK(ctx context.Context, javaBin, home string, source JDKSource, timeout time.Duration, allowLegacyJava bool) (ResolvedJDK, error) {
	return probeResolvedJDKWithRuntimeProbes(ctx, javaBin, home, source, timeout, allowLegacyJava, probeJavaRuntime, probeJavaHome)
}

func probeResolvedJDKWithProbes(
	ctx context.Context,
	javaBin string,
	home string,
	source JDKSource,
	timeout time.Duration,
	allowLegacyJava bool,
	versionProbe func(context.Context, string, time.Duration) (string, int, error),
	homeProbe func(context.Context, string, time.Duration) (string, error),
) (ResolvedJDK, error) {
	return probeResolvedJDKWithRuntimeProbes(
		ctx,
		javaBin,
		home,
		source,
		timeout,
		allowLegacyJava,
		func(ctx context.Context, javaBin string, timeout time.Duration) (string, int, JavaVMFamily, error) {
			version, major, err := versionProbe(ctx, javaBin, timeout)
			return version, major, JavaVMUnknown, err
		},
		homeProbe,
	)
}

func probeResolvedJDKWithRuntimeProbes(
	ctx context.Context,
	javaBin string,
	home string,
	source JDKSource,
	timeout time.Duration,
	allowLegacyJava bool,
	versionProbe func(context.Context, string, time.Duration) (string, int, JavaVMFamily, error),
	homeProbe func(context.Context, string, time.Duration) (string, error),
) (ResolvedJDK, error) {
	version, major, vmFamily, err := versionProbe(ctx, javaBin, timeout)
	if err != nil {
		return ResolvedJDK{}, err
	}
	if minimumMajor := EffectiveMinimumJavaMajor(allowLegacyJava); major < minimumMajor {
		return ResolvedJDK{}, fmt.Errorf("Java major version %d is unsupported; version %d or newer is required", major, minimumMajor)
	}
	// Some platforms expose a launcher shim rather than the real JDK binary
	// (notably /usr/bin/java on macOS). Deriving JAVA_HOME by walking two parent
	// directories would produce /usr and can make the shim recurse or hang when
	// that invalid home is exported to the child. Prefer the runtime-reported
	// java.home when available, while retaining the canonical binary-derived
	// value for minimal/fake launchers that do not support show-settings.
	if source == JDKSourceExplicitRunner || source == JDKSourcePath {
		if reportedHome, probeErr := homeProbe(ctx, javaBin, timeout); probeErr == nil {
			home = reportedHome
		} else if errors.Is(probeErr, ErrProcessContainmentFailed) {
			// java.home is optional only when its probe exits safely. A cleanup
			// failure cannot be hidden behind the binary-derived fallback.
			return ResolvedJDK{}, probeErr
		}
	}
	return ResolvedJDK{
		Home:     home,
		JavaBin:  javaBin,
		Version:  version,
		Major:    major,
		VMFamily: vmFamily,
		Source:   source,
	}, nil
}

var javaHomePattern = regexp.MustCompile(`(?m)^\s*java\.home\s*=\s*(.+?)\s*$`)

func probeJavaHome(ctx context.Context, javaBin string, timeout time.Duration) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &boundedBuffer{max: maxProbeOutputBytes}
	cmd := exec.CommandContext(probeCtx, javaBin, "-XshowSettings:properties", "-version")
	ConfigureProcessTree(cmd)
	cmd.WaitDelay = time.Second
	cmd.Env = sanitizedProbeEnvironment()
	cmd.Stdout = output
	cmd.Stderr = output
	err := executeJavaProbeCommand(
		cmd,
		StartProcessTree,
		KillProcessTree,
		ReapFailedStart,
	)
	if err != nil {
		return "", err
	}
	match := javaHomePattern.FindStringSubmatch(output.String())
	if len(match) < 2 {
		return "", fmt.Errorf("Java runtime did not report java.home")
	}
	home := filepath.Clean(strings.TrimSpace(match[1]))
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("Java runtime reported a non-absolute java.home")
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("Java runtime reported an unavailable java.home")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Java runtime reported an unavailable java.home")
	}
	return resolved, nil
}

// ProbeJava executes the resolved launcher with -version under a bounded
// timeout/output budget and returns its normalized version token and major.
func ProbeJava(ctx context.Context, javaBin string, timeout time.Duration) (string, int, error) {
	version, major, _, err := probeJavaRuntime(ctx, javaBin, timeout)
	return version, major, err
}

func probeJavaRuntime(ctx context.Context, javaBin string, timeout time.Duration) (string, int, JavaVMFamily, error) {
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &boundedBuffer{max: maxProbeOutputBytes}
	cmd := exec.CommandContext(probeCtx, javaBin, "-version")
	ConfigureProcessTree(cmd)
	cmd.WaitDelay = time.Second
	cmd.Env = sanitizedProbeEnvironment()
	cmd.Stdout = output
	cmd.Stderr = output
	err := executeJavaProbeCommand(
		cmd,
		StartProcessTree,
		KillProcessTree,
		ReapFailedStart,
	)
	if err != nil {
		return "", 0, JavaVMUnknown, classifyJavaProbeError(probeCtx, err)
	}
	version, major, err := ParseJavaVersion(output.String())
	if err != nil {
		return "", 0, JavaVMUnknown, err
	}
	return version, major, ParseJavaVMFamily(output.String()), nil
}

func classifyJavaProbeError(ctx context.Context, err error) error {
	// Containment is the stronger terminal condition. A simultaneous deadline
	// must not mask it, or the owning runtime could retry another JDK probe
	// while cleanup of this process tree remains unproven.
	if errors.Is(err, ErrProcessContainmentFailed) {
		return fmt.Errorf("Java version probe failed: %w", err)
	}
	if ctx != nil && ctx.Err() != nil {
		return fmt.Errorf("Java version probe timed out: %w", ctx.Err())
	}
	return fmt.Errorf("Java version probe failed: %w", err)
}

// executeJavaProbeCommand owns all process completion after StartProcessTree.
// A post-start containment failure transfers the sole direct-root Wait to
// ReapFailedStart; every other successful start is waited synchronously.
//
// Cleanup failures are classified as containment failures so an owning local
// runtime or sidecar can close its process-admission gate before another probe
// or Java Vuser is launched.
func executeJavaProbeCommand(
	cmd *exec.Cmd,
	start func(*exec.Cmd) error,
	kill func(*exec.Cmd) error,
	reap func(*exec.Cmd) <-chan error,
) error {
	startErr := start(cmd)
	var waitErr error
	if startErr == nil {
		waitErr = cmd.Wait()
	}
	cleanupErr := kill(cmd)
	var reapDone <-chan error
	if errors.Is(startErr, ErrProcessContainmentFailed) {
		// ReapFailedStart is asynchronous by design. The owning runtime will
		// fail admission closed; the probe must not block indefinitely on a
		// root that the platform could not prove terminated.
		reapDone = reap(cmd)
	}
	if cleanupErr != nil {
		cleanupErr = errors.Join(ErrProcessContainmentFailed, cleanupErr)
	}
	result := errors.Join(startErr, waitErr, cleanupErr)
	if reapDone != nil {
		return &failedStartReapError{cause: result, done: reapDone}
	}
	return result
}

func sanitizedProbeEnvironment() []string {
	// Keep probes on the same minimal environment boundary as executed Java
	// actions. This also strips all Java option-injection variables.
	return subprocessguard.Environment(nil)
}

var (
	javaVersionPattern = regexp.MustCompile(`(?im)\b(?:openjdk|java)\s+version\s+"?([0-9]+(?:\.[0-9]+)*(?:_[0-9]+)?(?:-[0-9A-Za-z.+_-]+)?)`)
	javaShortPattern   = regexp.MustCompile(`(?im)^\s*(?:openjdk|java)\s+"?([0-9]+(?:\.[0-9]+)*(?:_[0-9]+)?(?:-[0-9A-Za-z.+_-]+)?)`)
)

// ParseJavaVersion parses standard java/openjdk -version output without
// returning the rest of the subprocess output to callers.
func ParseJavaVersion(output string) (version string, major int, err error) {
	match := javaVersionPattern.FindStringSubmatch(output)
	if len(match) < 2 {
		match = javaShortPattern.FindStringSubmatch(output)
	}
	if len(match) < 2 {
		return "", 0, fmt.Errorf("Java version probe output was not recognized")
	}
	version = match[1]
	parts := strings.FieldsFunc(version, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return "", 0, fmt.Errorf("Java version probe output was not recognized")
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return "", 0, fmt.Errorf("Java version probe output was not recognized")
	}
	if major == 1 {
		if len(parts) < 2 {
			return "", 0, fmt.Errorf("Java version probe output was not recognized")
		}
		major, err = strconv.Atoi(parts[1])
		if err != nil {
			return "", 0, fmt.Errorf("Java version probe output was not recognized")
		}
	}
	return version, major, nil
}

// ParseJavaVMFamily recognizes the two supported JVM option dialects from
// standard `java -version` output. It never returns or embeds probe output.
func ParseJavaVMFamily(output string) JavaVMFamily {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "openj9") ||
		strings.Contains(lower, "j9 vm") ||
		strings.Contains(lower, "j9vm") {
		return JavaVMOpenJ9
	}
	if strings.Contains(lower, "hotspot") ||
		strings.Contains(lower, "openjdk 64-bit server vm") ||
		strings.Contains(lower, "openjdk server vm") ||
		strings.Contains(lower, "openjdk 64-bit client vm") ||
		strings.Contains(lower, "openjdk client vm") {
		return JavaVMHotSpot
	}
	return JavaVMUnknown
}

type boundedBuffer struct {
	bytes.Buffer
	max int
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := buffer.max - buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.Buffer.Write(data)
	}
	return written, nil
}
