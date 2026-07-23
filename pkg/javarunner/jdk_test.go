package javarunner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseJavaVersion(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantVersion string
		wantMajor   int
	}{
		{name: "OpenJDK 21", output: "openjdk version \"21.0.7\" 2025-04-15", wantVersion: "21.0.7", wantMajor: 21},
		{name: "Oracle 17", output: "java version \"17.0.12\" 2024-07-16 LTS", wantVersion: "17.0.12", wantMajor: 17},
		{name: "early access", output: "openjdk version \"22-ea+24\"", wantVersion: "22-ea+24", wantMajor: 22},
		{name: "legacy", output: "java version \"1.8.0_412\"", wantVersion: "1.8.0_412", wantMajor: 8},
		{name: "short vendor form", output: "openjdk 23.0.1 2024-10-15", wantVersion: "23.0.1", wantMajor: 23},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, major, err := ParseJavaVersion(test.output)
			if err != nil {
				t.Fatal(err)
			}
			if version != test.wantVersion || major != test.wantMajor {
				t.Fatalf("ParseJavaVersion() = %q, %d, want %q, %d", version, major, test.wantVersion, test.wantMajor)
			}
		})
	}
}

func TestParseJavaVersionRejectsUnrecognizedOutputWithoutEcho(t *testing.T) {
	const output = "untrusted-sensitive-output"
	_, _, err := ParseJavaVersion(output)
	if err == nil {
		t.Fatal("ParseJavaVersion succeeded, want error")
	}
	if strings.Contains(err.Error(), output) {
		t.Fatalf("error %q echoed version-probe output", err)
	}
}

func TestResolveSpecifiedJDK(t *testing.T) {
	requireShellFixture(t)
	home := makeFakeJDK(t, "jdk-21", "21.0.7", "java")
	alias := filepath.Join(t.TempDir(), "jdk-current")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}

	jdk, err := ResolveJDK(context.Background(), ResolveOptions{
		Selection:     JDKSelectionSpecified,
		SpecifiedHome: alias,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if jdk.Home != canonicalHome || jdk.Major != 21 || jdk.Version != "21.0.7" || jdk.Source != JDKSourceSpecifiedHome {
		t.Fatalf("resolved JDK = %#v, want canonical JDK 21 specified home", jdk)
	}
	if filepath.Base(jdk.JavaBin) != "java" {
		t.Fatalf("JavaBin = %q, want bin/java", jdk.JavaBin)
	}
}

func TestResolveSpecifiedJDKSupportsJavaExeLauncher(t *testing.T) {
	requireShellFixture(t)
	home := makeFakeJDK(t, "jdk-exe", "17.0.12", "java.exe")
	jdk, err := ResolveJDK(context.Background(), ResolveOptions{
		Selection:     JDKSelectionSpecified,
		SpecifiedHome: home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(jdk.JavaBin) != "java.exe" || jdk.Major != 17 {
		t.Fatalf("resolved JDK = %#v, want Java 17 java.exe launcher", jdk)
	}
}

func TestResolveSpecifiedJDKHasNoFallback(t *testing.T) {
	requireShellFixture(t)
	oldHome := makeFakeJDK(t, "jdk-16", "16.0.2", "java")
	fallback := makeFakeJDK(t, "jdk-21", "21.0.7", "java")
	t.Setenv("JAVA_HOME", fallback)
	t.Setenv("JRE_HOME", fallback)
	t.Setenv("PATH", filepath.Join(fallback, "bin"))

	_, err := ResolveJDK(context.Background(), ResolveOptions{
		Selection:     JDKSelectionSpecified,
		SpecifiedHome: oldHome,
	})
	if err == nil || !strings.Contains(err.Error(), "version 17 or newer") {
		t.Fatalf("ResolveJDK error = %v, want unsupported Java 16 without fallback", err)
	}
}

func TestResolveSpecifiedJDKRejectsMissingAndRelativeHomes(t *testing.T) {
	for _, home := range []string{"", "relative/jdk", filepath.Join(t.TempDir(), "missing")} {
		_, err := ResolveJDK(context.Background(), ResolveOptions{
			Selection:     JDKSelectionSpecified,
			SpecifiedHome: home,
		})
		if err == nil {
			t.Fatalf("ResolveJDK specified home %q succeeded, want error", home)
		}
	}
}

func TestResolveSpecifiedJDKRejectsNonExecutableLauncher(t *testing.T) {
	requireShellFixture(t)
	home := makeFakeJDK(t, "not-executable", "21.0.7", "java")
	if err := os.Chmod(filepath.Join(home, "bin", "java"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveJDK(context.Background(), ResolveOptions{
		Selection:     JDKSelectionSpecified,
		SpecifiedHome: home,
	}); err == nil {
		t.Fatal("ResolveJDK succeeded with a non-executable Java launcher")
	}
}

func TestResolveInternalJDKPrecedenceAndFallback(t *testing.T) {
	requireShellFixture(t)
	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath)
	t.Setenv("JAVA_HOME", "")
	t.Setenv("JRE_HOME", "")

	explicit17 := makeFakeJDK(t, "explicit-17", "17.0.12", "java")
	default21 := makeFakeJDK(t, "default-21", "21.0.7", "java")
	jdk, err := ResolveJDK(context.Background(), ResolveOptions{
		ExplicitJavaCommand: filepath.Join(explicit17, "bin", "java"),
		DefaultHome:         default21,
	})
	if err != nil {
		t.Fatal(err)
	}
	if jdk.Source != JDKSourceExplicitRunner || jdk.Major != 17 {
		t.Fatalf("resolved JDK = %#v, want explicit Java 17", jdk)
	}

	explicit16 := makeFakeJDK(t, "explicit-16", "16.0.2", "java")
	jdk, err = ResolveJDK(context.Background(), ResolveOptions{
		ExplicitJavaCommand: filepath.Join(explicit16, "bin", "java"),
		DefaultHome:         default21,
	})
	if err != nil {
		t.Fatal(err)
	}
	if jdk.Source != JDKSourceDefaultHome || jdk.Major != 21 {
		t.Fatalf("resolved JDK = %#v, want fallback default Java 21", jdk)
	}
}

func TestResolveInternalJDKEnvironmentAndPathOrder(t *testing.T) {
	requireShellFixture(t)
	emptyDefault := filepath.Join(t.TempDir(), "missing-default")
	java16 := makeFakeJDK(t, "java-home-16", "16.0.2", "java")
	jre17 := makeFakeJDK(t, "jre-home-17", "17.0.12", "java")
	path21 := makeFakeJDK(t, "path-21", "21.0.7", "java")
	t.Setenv("JAVA_HOME", java16)
	t.Setenv("JRE_HOME", jre17)
	t.Setenv("PATH", filepath.Join(path21, "bin"))

	jdk, err := ResolveJDK(context.Background(), ResolveOptions{DefaultHome: emptyDefault})
	if err != nil {
		t.Fatal(err)
	}
	if jdk.Source != JDKSourceJREHome || jdk.Major != 17 {
		t.Fatalf("resolved JDK = %#v, want JRE_HOME Java 17 after JAVA_HOME 16 rejection", jdk)
	}

	t.Setenv("JAVA_HOME", "")
	t.Setenv("JRE_HOME", "")
	jdk, err = ResolveJDK(context.Background(), ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if jdk.Source != JDKSourcePath || jdk.Major != 21 {
		t.Fatalf("resolved JDK = %#v, want PATH Java 21", jdk)
	}
}

func TestResolveInternalJDKRejectsWrapperCommand(t *testing.T) {
	_, err := ResolveJDK(context.Background(), ResolveOptions{ExplicitJavaCommand: "/opt/runner-wrapper"})
	if !errors.Is(err, ErrUnsupportedRunnerCommand) {
		t.Fatalf("ResolveJDK error = %v, want ErrUnsupportedRunnerCommand", err)
	}
}

func TestProbeJavaTimeout(t *testing.T) {
	requireShellFixture(t)
	home := makeFakeJavaScript(t, "slow-jdk", "exec /bin/sleep 5")
	_, _, err := ProbeJava(context.Background(), filepath.Join(home, "bin", "java"), 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("ProbeJava error = %v, want timeout", err)
	}
}

func TestResolvedJDKEnvironmentOverrides(t *testing.T) {
	jdk := ResolvedJDK{Home: filepath.Join("", "opt", "jdk")}
	overrides := jdk.EnvironmentOverrides("existing-path")
	wantPath := filepath.Join(jdk.Home, "bin") + string(filepath.ListSeparator) + "existing-path"
	if overrides["JAVA_HOME"] != jdk.Home || overrides["JRE_HOME"] != jdk.Home || overrides["PATH"] != wantPath {
		t.Fatalf("EnvironmentOverrides() = %#v", overrides)
	}
}

func TestResolveJDKPrefersRuntimeReportedJavaHome(t *testing.T) {
	requireShellFixture(t)
	reportedHome := t.TempDir()
	body := "if [ \"$1\" = \"-XshowSettings:properties\" ]; then echo '    java.home = " + reportedHome + "' >&2; else echo 'openjdk version \"21.0.7\"' >&2; fi"
	launcherHome := makeFakeJavaScript(t, "launcher-shim", body)
	jdk, err := ResolveJDK(context.Background(), ResolveOptions{
		ExplicitJavaCommand: filepath.Join(launcherHome, "bin", "java"),
	})
	if err != nil {
		t.Fatal(err)
	}
	canonicalHome, err := filepath.EvalSymlinks(reportedHome)
	if err != nil {
		t.Fatal(err)
	}
	if jdk.Home != canonicalHome {
		t.Fatalf("Home = %q, want runtime-reported %q instead of launcher-derived home", jdk.Home, canonicalHome)
	}
}

func TestExecuteJavaProbeCommandReapsContainmentFailedStartExactlyOnce(t *testing.T) {
	command := exec.Command("must-not-run")
	reapCalls := 0
	reaped := make(chan error)
	close(reaped)
	err := executeJavaProbeCommand(
		command,
		func(*exec.Cmd) error {
			return errors.Join(
				ErrProcessContainmentFailed,
				errors.New("platform cleanup detail"),
			)
		},
		func(*exec.Cmd) error { return nil },
		func(got *exec.Cmd) <-chan error {
			if got != command {
				t.Fatal("failed-start reaper received a different command")
			}
			reapCalls++
			return reaped
		},
	)
	if !errors.Is(err, ErrProcessContainmentFailed) {
		t.Fatalf("executeJavaProbeCommand() error = %v, want containment sentinel", err)
	}
	if reapCalls != 1 {
		t.Fatalf("failed-start reap calls = %d, want exactly one", reapCalls)
	}
	if FailedStartReapDone(err) != reaped {
		t.Fatal("failed-start reap ownership was not propagated with the probe error")
	}
}

func TestExecuteJavaProbeCommandClassifiesCleanupFailure(t *testing.T) {
	err := executeJavaProbeCommand(
		exec.Command("must-not-run"),
		func(*exec.Cmd) error { return errors.New("start failed") },
		func(*exec.Cmd) error { return errors.New("cleanup failed") },
		func(*exec.Cmd) <-chan error {
			t.Fatal("ordinary start failure must not transfer Wait ownership")
			return nil
		},
	)
	if !errors.Is(err, ErrProcessContainmentFailed) {
		t.Fatalf("cleanup failure = %v, want containment sentinel", err)
	}
}

func TestClassifyJavaProbeErrorDoesNotMaskContainmentWithTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := classifyJavaProbeError(
		ctx,
		errors.Join(context.DeadlineExceeded, ErrProcessContainmentFailed),
	)
	if !errors.Is(err, ErrProcessContainmentFailed) || strings.Contains(err.Error(), "timed out") {
		t.Fatalf("classifyJavaProbeError() = %v, want containment precedence", err)
	}
}

func TestProbeResolvedJDKPropagatesOptionalHomeContainmentFailure(t *testing.T) {
	_, err := probeResolvedJDKWithProbes(
		context.Background(),
		filepath.Join(string(filepath.Separator), "opt", "jdk", "bin", "java"),
		filepath.Join(string(filepath.Separator), "opt", "jdk"),
		JDKSourceExplicitRunner,
		time.Second,
		func(context.Context, string, time.Duration) (string, int, error) {
			return "17.0.1", 17, nil
		},
		func(context.Context, string, time.Duration) (string, error) {
			return "", errors.Join(
				ErrProcessContainmentFailed,
				errors.New("java.home probe cleanup failed"),
			)
		},
	)
	if !errors.Is(err, ErrProcessContainmentFailed) {
		t.Fatalf("probeResolvedJDKWithProbes() error = %v, want containment sentinel", err)
	}
}

func TestResolveInternalJDKStopsAfterContainmentFailure(t *testing.T) {
	requireShellFixture(t)
	firstHome := makeFakeJDK(t, "first", "17.0.1", "java")
	secondHome := makeFakeJDK(t, "second", "21.0.1", "java")
	t.Setenv("JAVA_HOME", "")
	t.Setenv("JRE_HOME", "")
	t.Setenv("PATH", "")

	probeCalls := 0
	_, err := resolveInternalJDKWithProbe(
		context.Background(),
		ResolveOptions{
			ExplicitJavaCommand: filepath.Join(firstHome, "bin", "java"),
			DefaultHome:         secondHome,
		},
		time.Second,
		func(context.Context, string, string, JDKSource, time.Duration) (ResolvedJDK, error) {
			probeCalls++
			return ResolvedJDK{}, ErrProcessContainmentFailed
		},
	)
	if !errors.Is(err, ErrProcessContainmentFailed) {
		t.Fatalf("resolveInternalJDKWithProbe() error = %v, want containment sentinel", err)
	}
	if probeCalls != 1 {
		t.Fatalf("JDK candidates probed after terminal containment failure = %d, want one", probeCalls)
	}
}

func TestSanitizedProbeEnvironmentRemovesJavaInjectionOptions(t *testing.T) {
	for name, value := range map[string]string{
		"PATH":              "/bin",
		"JAVA_TOOL_OPTIONS": "-javaagent:/tmp/agent.jar",
		"JDK_JAVA_OPTIONS":  "@/tmp/options",
		"_JAVA_OPTIONS":     "-Xbootclasspath/a:/tmp/classes",
		"CLASSPATH":         "/tmp/classes",
		"SAFE":              "value",
	} {
		t.Setenv(name, value)
	}
	got := strings.Join(sanitizedProbeEnvironment(), "\x00")
	if !strings.Contains(got, "PATH=/bin") {
		t.Fatalf("sanitizedProbeEnvironment() = %q, want PATH", got)
	}
	for _, forbidden := range []string{"JAVA_TOOL_OPTIONS=", "JDK_JAVA_OPTIONS=", "_JAVA_OPTIONS=", "CLASSPATH=", "SAFE="} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitizedProbeEnvironment() retained %q: %q", forbidden, got)
		}
	}
}

func TestIsDirectJavaCommand(t *testing.T) {
	for _, command := range []string{"java", "java.exe", filepath.Join("opt", "jdk", "bin", "java")} {
		if !IsDirectJavaCommand(command) {
			t.Errorf("IsDirectJavaCommand(%q) = false, want true", command)
		}
	}
	for _, command := range []string{"", "runner-wrapper", "java -Xmx1g", "java\nnext"} {
		if IsDirectJavaCommand(command) {
			t.Errorf("IsDirectJavaCommand(%q) = true, want false", command)
		}
	}
}

func makeFakeJDK(t *testing.T, name, version, launcher string) string {
	t.Helper()
	return makeFakeJavaScript(t, name, "echo 'openjdk version \""+version+"\"' >&2", launcher)
}

func makeFakeJavaScript(t *testing.T, name, body string, launcherName ...string) string {
	t.Helper()
	launcher := "java"
	if len(launcherName) > 0 {
		launcher = launcherName[0]
	}
	home := filepath.Join(t.TempDir(), name)
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(binDir, launcher)
	contents := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func requireShellFixture(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake JDK fixture is Unix-only")
	}
}
