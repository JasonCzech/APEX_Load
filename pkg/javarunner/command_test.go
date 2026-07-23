package javarunner

import (
	"path/filepath"
	"reflect"
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
	wantArgs := []string{"-Xms64m", "-cp", "/runner/harness.jar", "Harness", "--class", "Actions"}
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
	want := []string{"-jar", "/runner/harness.jar"}
	if !reflect.DeepEqual(plan.Args, want) {
		t.Fatalf("Args = %#v, want %#v", plan.Args, want)
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
