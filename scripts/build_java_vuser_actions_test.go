package scripts_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The auto-detect fallback enables APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA, a
// security-relevant floor opt-in, so its fail-closed shape is pinned here the
// way other operator scripts in this directory are: probing requires the
// caller to NAME the incompatible middleware class, an empty probe list is a
// hard error rather than a silent downgrade, and no probe class ships as a
// default (the old org.w3c.dom.ls.DocumentLS default exists in no modern JDK,
// which made downgrading to Java 8 the unconditional outcome).
func TestBuildJavaVuserActionsFailsClosedOnLegacyAutoDetect(t *testing.T) {
	content, err := os.ReadFile("build-java-vuser-actions.ps1")
	if err != nil {
		t.Fatalf("read build helper: %v", err)
	}
	script := string(content)

	for _, check := range []string{
		"[string[]]$LegacyProbeClasses = @()",
		"-AutoDetectLegacyJava requires -LegacyProbeClasses",
		"APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA",
	} {
		if !strings.Contains(script, check) {
			t.Fatalf("build helper missing %q", check)
		}
	}

	// No default probe-class value may reappear: match the parameter
	// declaration and require the empty array literal.
	declaration := regexp.MustCompile(`\[string\[\]\]\$LegacyProbeClasses\s*=\s*@\(([^)]*)\)`)
	match := declaration.FindStringSubmatch(script)
	if match == nil {
		t.Fatal("build helper no longer declares $LegacyProbeClasses")
	}
	if strings.TrimSpace(match[1]) != "" {
		t.Fatalf("$LegacyProbeClasses default = %q, want empty (a non-loadable default silently downgrades every run to Java 8)", match[1])
	}

	// The legacy env flip must only happen on the auto-detect fallback path,
	// never as an initial value.
	if !strings.Contains(script, "$effectiveAllowLegacyJava = 'false'") {
		t.Fatal("build helper must initialize the legacy flag to 'false'")
	}
}

func TestBuildJavaVuserActionsUsesIsolatedScratchDirectories(t *testing.T) {
	content, err := os.ReadFile("build-java-vuser-actions.ps1")
	if err != nil {
		t.Fatalf("read build helper: %v", err)
	}
	script := string(content)

	for _, unsafe := range []string{
		"$classesDir = Join-Path $outputDir 'classes'",
		"$probeDir = Join-Path $RepoRoot 'artifacts'",
	} {
		if strings.Contains(script, unsafe) {
			t.Fatalf("build helper still uses recursively deleted caller/repository path %q", unsafe)
		}
	}
	for _, required := range []string{
		"[System.IO.Path]::GetTempPath()",
		"apexload-java-vuser-probe-",
		"apexload-java-vuser-classes-",
		"[Guid]::NewGuid().ToString('N')",
		"Remove-Item -Recurse -Force -LiteralPath $probeDir",
		"Remove-Item -Recurse -Force -LiteralPath $classesDir",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("build helper missing isolated scratch behavior %q", required)
		}
	}
}

func TestBuildJavaVuserActionsCompilesAgainstEffectiveLegacyHarness(t *testing.T) {
	content, err := os.ReadFile("build-java-vuser-actions.ps1")
	if err != nil {
		t.Fatalf("read build helper: %v", err)
	}
	script := string(content)

	fallback := strings.Index(script, "$effectiveHarnessJar = Build-LegacyHarnessJar")
	compileClasspath := strings.Index(script, "$compileClasspathEntries = @($effectiveHarnessJar) + $DependencyClasspath")
	javac := strings.Index(script, "Invoke-External -Exe $javacExe -Arguments @('--release', $effectiveRelease, '-nowarn', '-cp', $classpathValue")
	if fallback < 0 || compileClasspath < 0 || javac < 0 {
		t.Fatal("build helper is missing the legacy fallback, effective compile classpath, or javac invocation")
	}
	if compileClasspath <= fallback || javac <= compileClasspath {
		t.Fatal("effective legacy harness must replace the compile classpath after fallback and before javac")
	}
	if !strings.Contains(script, "-ClasspathValue $probeClasspathValue") {
		t.Fatal("modern compatibility probe must keep its separate pre-fallback classpath")
	}
}

func TestBuildJavaVuserActionsRejectsWildcardClasspathBeforePersisting(t *testing.T) {
	content, err := os.ReadFile("build-java-vuser-actions.ps1")
	if err != nil {
		t.Fatalf("read build helper: %v", err)
	}
	script := string(content)

	validation := strings.Index(script, "$wildcardClasspathEntries = @($DependencyClasspath")
	persistence := strings.Index(script, "$sharedClasspath = Join-UniqueClasspath")
	if validation < 0 {
		t.Fatal("build helper does not reject wildcard classpath entries")
	}
	if persistence < 0 {
		t.Fatal("build helper no longer persists the dependency classpath")
	}
	if validation >= persistence {
		t.Fatal("wildcard classpath validation must run before persisting controller configuration")
	}
	for _, required := range []string{
		"$_ -match '[*?\\[]'",
		"Wildcard classpath entries are not supported; enumerate exact files or directories",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("build helper missing wildcard rejection behavior %q", required)
		}
	}
	if strings.Contains(script, "if ($Entry.Contains('*'))") {
		t.Fatal("build helper still treats wildcard classpath entries as valid")
	}
}

func TestBuildJavaVuserActionsPersistsAbsoluteRuntimePaths(t *testing.T) {
	content, err := os.ReadFile("build-java-vuser-actions.ps1")
	if err != nil {
		t.Fatalf("read build helper: %v", err)
	}
	script := string(content)

	for _, required := range []string{
		"$resolvedJdkHome = [System.IO.Path]::GetFullPath((Resolve-JdkHome",
		"$resolvedHarnessJar = [System.IO.Path]::GetFullPath((Resolve-HarnessJar",
		"$DependencyClasspath = @($DependencyClasspath | ForEach-Object",
		"[System.IO.Path]::GetFullPath($_.Trim())",
		"$resolvedLegacyJavaHome = [System.IO.Path]::GetFullPath($LegacyJavaHome.Trim())",
		"$effectiveRunnerHome = $resolvedLegacyJavaHome",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("build helper missing absolute runtime-path behavior %q", required)
		}
	}

	normalization := strings.Index(script, "$DependencyClasspath = @($DependencyClasspath | ForEach-Object")
	compileClasspath := strings.Index(script, "$classpathEntries = @($resolvedHarnessJar) + $DependencyClasspath")
	persistence := strings.Index(script, "$sharedClasspath = Join-UniqueClasspath")
	if normalization < 0 || compileClasspath < 0 || persistence < 0 ||
		normalization >= compileClasspath || compileClasspath >= persistence {
		t.Fatal("dependency classpaths must be made absolute before compilation and persistence")
	}
}

func TestBuildJavaVuserActionsRestartsOnlyOwnedControllerBinary(t *testing.T) {
	content, err := os.ReadFile("build-java-vuser-actions.ps1")
	if err != nil {
		t.Fatalf("read build helper: %v", err)
	}
	script := string(content)

	for _, required := range []string{
		"$resolvedControllerPath = [System.IO.Path]::GetFullPath($resolvedControllerPath)",
		"$controllerProcessName = [System.IO.Path]::GetFileNameWithoutExtension($resolvedControllerPath)",
		"Get-Process -Name $controllerProcessName",
		"[System.StringComparer]::OrdinalIgnoreCase.Equals($candidatePath, $resolvedControllerPath)",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("build helper missing controller ownership fence %q", required)
		}
	}
	if strings.Contains(script, "$controllers = Get-Process controller") {
		t.Fatal("build helper still stops every process named controller")
	}

	selection := strings.Index(script, "$controllers = @(Get-Process -Name $controllerProcessName")
	stop := strings.Index(script, "$controllers | Stop-Process -Force")
	if selection < 0 || stop < 0 || selection >= stop {
		t.Fatal("owned controller selection must precede Stop-Process")
	}
}
