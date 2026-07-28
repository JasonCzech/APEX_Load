package javarunner

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBuildCommandOrdersVMParametersAndClasspaths(t *testing.T) {
	plan, err := BuildCommand(CommandSpec{
		VMParameters:    []string{"-Xmx512m", "-Dlabel=hello world"},
		HarnessJar:      "/runner/harness.jar",
		ScriptClasspath: []string{"/script/classes", "/script/lib.jar"},
		SharedClasspath: []string{"/operator/shared.jar"},
		HarnessArgs:     []string{"--class", "Actions", "--method", "action"},
	})
	if err != nil {
		t.Fatal(err)
	}
	classpath := filepath.Join("/runner", "harness.jar") + string(filepath.ListSeparator) +
		filepath.Join("/script", "classes") + string(filepath.ListSeparator) +
		filepath.Join("/script", "lib.jar") + string(filepath.ListSeparator) +
		filepath.Join("/operator", "shared.jar")
	want := []string{
		"-Xmx512m", "-Dlabel=hello world",
		enforcedDisableAttachMechanism, enforcedDisableOpenJ9Attach, enforcedDisableHeapDumpOnOOM,
		enforcedDisableCrashCoreDump,
		"-cp", classpath, "Harness",
		"--class", "Actions", "--method", "action",
	}
	if !reflect.DeepEqual(plan.Args, want) {
		t.Fatalf("Args = %#v, want %#v", plan.Args, want)
	}
	wantClasspath := []string{"/runner/harness.jar", "/script/classes", "/script/lib.jar", "/operator/shared.jar"}
	if !reflect.DeepEqual(plan.SystemClasspath, wantClasspath) {
		t.Fatalf("SystemClasspath = %#v, want %#v", plan.SystemClasspath, wantClasspath)
	}
	if len(plan.UserClasspath) != 0 {
		t.Fatalf("UserClasspath = %#v, want empty in normal mode", plan.UserClasspath)
	}
}

func TestBuildCommandDedicatedClassLoaderSeparatesUserClasspath(t *testing.T) {
	plan, err := BuildCommand(CommandSpec{
		VMParameters:         []string{"-Xms64m"},
		HarnessJar:           "/runner/harness.jar",
		ScriptClasspath:      []string{"/script/classes"},
		SharedClasspath:      []string{"/operator/shared.jar"},
		DedicatedClassLoader: true,
		HarnessArgs:          []string{"--class", "Actions"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{
		"-Xms64m",
		enforcedDisableAttachMechanism,
		enforcedDisableOpenJ9Attach,
		enforcedDisableHeapDumpOnOOM,
		enforcedDisableCrashCoreDump,
		"-cp", "/runner/harness.jar", "Harness", "--class", "Actions",
	}
	if !reflect.DeepEqual(plan.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", plan.Args, wantArgs)
	}
	if !reflect.DeepEqual(plan.SystemClasspath, []string{"/runner/harness.jar"}) {
		t.Fatalf("SystemClasspath = %#v", plan.SystemClasspath)
	}
	wantUser := []string{"/script/classes", "/operator/shared.jar"}
	if !reflect.DeepEqual(plan.UserClasspath, wantUser) {
		t.Fatalf("UserClasspath = %#v, want %#v", plan.UserClasspath, wantUser)
	}
}

func TestBuildCommandPreservesHarnessJarLaunchCompatibility(t *testing.T) {
	plan, err := BuildCommand(CommandSpec{HarnessJar: "/runner/harness.jar"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		enforcedDisableAttachMechanism,
		enforcedDisableOpenJ9Attach,
		enforcedDisableHeapDumpOnOOM,
		enforcedDisableCrashCoreDump,
		"-jar", "/runner/harness.jar",
	}
	if !reflect.DeepEqual(plan.Args, want) {
		t.Fatalf("Args = %#v, want %#v", plan.Args, want)
	}
}

func TestBuildCommandLegacyJavaOmitsUnsupportedCrashCoreFlag(t *testing.T) {
	plan, err := BuildCommand(CommandSpec{
		JavaMajor:  8,
		HarnessJar: "/runner/harness.jar",
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plan.Args, enforcedDisableCrashCoreDump) {
		t.Fatalf("legacy Java 8 command includes unsupported crash-core flag: %#v", plan.Args)
	}
	for _, required := range []string{
		enforcedDisableAttachMechanism,
		enforcedDisableOpenJ9Attach,
		enforcedDisableHeapDumpOnOOM,
	} {
		if !slices.Contains(plan.Args, required) {
			t.Fatalf("legacy Java 8 command lost required enforcement %q: %#v", required, plan.Args)
		}
	}
}

func TestBuildCommandDedicatedClassLoaderRequirements(t *testing.T) {
	tests := []CommandSpec{
		{DedicatedClassLoader: true, ScriptClasspath: []string{"/classes"}},
		{DedicatedClassLoader: true, HarnessJar: "/harness.jar"},
	}
	for _, spec := range tests {
		if _, err := BuildCommand(spec); err == nil {
			t.Fatalf("BuildCommand(%#v) succeeded, want error", spec)
		}
	}
}

func TestBuildCommandRejectsHarnessMainBeginningWithDash(t *testing.T) {
	_, err := BuildCommand(CommandSpec{HarnessJar: "/opt/harness.jar", HarnessMain: "-XshowSettings"})
	if err == nil {
		t.Fatal("BuildCommand accepted a harness main class beginning with '-'")
	}
}

func TestBuildCommandRejectsUnsafeVMParameterWithoutExposingValue(t *testing.T) {
	const secret = "do-not-log"
	_, err := BuildCommand(CommandSpec{VMParameters: []string{"-Dapexload.secret=" + secret}})
	if err == nil {
		t.Fatal("BuildCommand succeeded, want validation error")
	}
	if got := err.Error(); got == "" || strings.Contains(got, secret) {
		t.Fatalf("BuildCommand error %q exposed token value", got)
	}
}

func TestBuildCommandRejectsCallerControlledDiagnosticDumpTriggers(t *testing.T) {
	for _, parameter := range []string{
		"-XX:+HeapDumpOnOutOfMemoryError",
		"-XX:-HeapDumpOnOutOfMemoryError",
		"-XX:+HeapDumpBeforeFullGC",
		"-XX:-HeapDumpBeforeFullGC",
		"-XX:+HeapDumpAfterFullGC",
		"-XX:-HeapDumpAfterFullGC",
		"-XX:+CrashOnOutOfMemoryError",
		"-XX:-CrashOnOutOfMemoryError",
		"-XX:+CreateCoredumpOnCrash",
		"-XX:-CreateCoredumpOnCrash",
	} {
		t.Run(parameter, func(t *testing.T) {
			if _, err := BuildCommand(CommandSpec{VMParameters: []string{parameter}}); err == nil {
				t.Fatalf("BuildCommand accepted diagnostic-dump trigger override %q", parameter)
			}
		})
	}
}

func TestBuildCommandRejectsCallerControlledAOTInputsAndOutputs(t *testing.T) {
	for _, parameter := range []string{
		"-XX:AOTLibrary=/tmp/native.so",
		"-XXaltjvm=/tmp/alternate",
		"-J-XXaltjvm=/tmp/alternate",
		"-XX:AOTCache=/tmp/cache.aot",
		"-XX:AOTConfiguration=/tmp/config.aotconf",
		"-XX:AOTCacheOutput=/tmp/cache.aot",
		"-XX:AOTMode=create",
		"-XX:+UseJITServer",
		"-XX:JITServerAddress=compiler.example.test",
		"-Xshareclasses:cacheDir=/tmp/cache,restoreFromSnapshot",
		"-XX:SharedClassListFile=/tmp/classes.lst",
		"-Xshare:dump",
		"-Xlog:help",
		"-XX:+PrInTfLaGsInItIaL",
		"-XX:+PrInTfLaGsWiThCoMmEnTs",
		"-XpRiNtFlAgS",
		"-Xtrace:output=/tmp/trace",
		"-XX:+DumpSharedSpaces",
		"-XX:+PrintSharedArchiveAndExit",
		"-XX:PerfDataSaveFile=/tmp/perf.data",
		"-XX:+PerfDataSaveToFile",
		"-XX:PauseAtStartupFile=/tmp/pause",
		"-XX:+PauseAtStartup",
		"-XX:+PauseAtExit",
		"-Xverbosegclog",
		"-Xlog:gc:/tmp/gc.log",
		"-Xjit:vlog=/tmp/jit.log",
		"-Xaot:limitFile=/tmp/limits",
		"-XX:+PerfTool",
		"-XvErIfY:NoNe",
		"-DcOm.SuN.MaNaGeMeNt",
		"-XX:-IgnoreUnrecognizedXXColonOptions",
		"-VeRsIoN:1.8+",
		"-prof:/tmp/java.prof",
		"-FuLlVeRsIoN",
	} {
		t.Run(parameter, func(t *testing.T) {
			if _, err := BuildCommand(CommandSpec{VMParameters: []string{parameter}}); err == nil {
				t.Fatalf("BuildCommand accepted caller-controlled AOT option %q", parameter)
			}
		})
	}
}
