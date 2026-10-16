package java_vuser

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

func hostCompileJDKHome(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	jdk, err := javarunner.ResolveJDK(ctx, javarunner.ResolveOptions{Selection: javarunner.JDKSelectionInternal})
	if err != nil || !javarunner.JavaCompilerAvailable(jdk) {
		t.Skip("no host JDK with javac")
	}
	return jdk.Home
}

func emptyJar(t *testing.T) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create("META-INF/MANIFEST.MF")
	_, _ = entry.Write([]byte("Manifest-Version: 1.0\r\n\r\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "empty.jar")
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

var lrStubSource = []byte("package lrapi;\npublic final class lr {\n  public static int start_transaction(String name) { return 0; }\n}\n")

func TestCompileSourceCompilesLocallyAgainstTheOperatorHarness(t *testing.T) {
	home := hostCompileJDKHome(t)
	operator := config.JavaVuserOperatorConfig{Enabled: true, RunnerMode: config.JavaVuserRunnerModeLocal, DefaultJDKHome: home, HarnessJar: emptyJar(t), WorkDir: t.TempDir()}
	stub, err := CompileSource(context.Background(), operator, javasidecar.CompileRequest{TargetClass: "lrapi.lr", Sources: []javasidecar.CompileSourceFile{{Path: "lrapi/lr.java", Content: lrStubSource}}})
	if err != nil || stub.ErrorCode != "" {
		t.Fatalf("compile lr stub: %v %+v", err, stub)
	}
	operator.HarnessJar = filepath.Join(t.TempDir(), "harness.jar")
	if err := os.WriteFile(operator.HarnessJar, stub.Jar, 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := CompileSource(context.Background(), operator, javasidecar.CompileRequest{
		Runtime:     javasidecar.RuntimeSettings{Classpath: []string{"absent.jar"}},
		TargetClass: "Actions",
		Sources:     []javasidecar.CompileSourceFile{{Path: "Actions.java", Content: []byte("public class Actions { int t = lrapi.lr.start_transaction(\"t\"); }\n")}},
	})
	if err != nil || response.ErrorCode != "" || !slices.Equal(response.Classes, []string{"Actions"}) || !slices.Equal(response.UnavailableClasspath, []int{0}) {
		t.Fatalf("local compile: %v %+v", err, response)
	}
	failed, err := CompileSource(context.Background(), operator, javasidecar.CompileRequest{
		TargetClass: "Actions",
		Sources:     []javasidecar.CompileSourceFile{{Path: "Actions.java", Content: []byte("public class Actions { int t = missing(); }\n")}},
	})
	if err != nil || failed.ErrorCode != javarunner.CompileFailed || len(failed.Diagnostics) == 0 {
		t.Fatalf("failed local compile: %v %+v", err, failed)
	}
}

func TestCompileSourceRefusesWithStableCodes(t *testing.T) {
	refusal := func(err error) string {
		var remote *javasidecar.RemoteError
		if errors.As(err, &remote) {
			return remote.Code
		}
		return ""
	}
	request := javasidecar.CompileRequest{TargetClass: "Actions", Sources: []javasidecar.CompileSourceFile{{Path: "Actions.java"}}}
	if _, err := CompileSource(context.Background(), config.JavaVuserOperatorConfig{}, request); refusal(err) != "runner_disabled" {
		t.Fatalf("disabled runner: %v", err)
	}
	operator := config.JavaVuserOperatorConfig{Enabled: true, RunnerMode: config.JavaVuserRunnerModeLocal, WorkDir: t.TempDir()}
	if _, err := CompileSource(context.Background(), operator, javasidecar.CompileRequest{TargetClass: "Actions"}); refusal(err) != javarunner.CompileSourcesInvalid {
		t.Fatalf("no sources: %v", err)
	}
	localCompileSlot <- struct{}{}
	_, err := CompileSource(context.Background(), operator, request)
	<-localCompileSlot
	if refusal(err) != "compile_capacity_unavailable" {
		t.Fatalf("busy compiler: %v", err)
	}
	operator.DefaultJDKHome = hostCompileJDKHome(t)
	if _, err := CompileSource(context.Background(), operator, request); refusal(err) != "harness_unavailable" {
		t.Fatalf("no harness: %v", err)
	}
}

func TestCompileSourceUsesTheSidecarInSidecarMode(t *testing.T) {
	home := hostCompileJDKHome(t)
	server := httptest.NewServer(javasidecar.NewServer(javasidecar.ServerOptions{
		Operator:        config.JavaVuserOperatorConfig{Enabled: true, AuthToken: "compile-test-token", DefaultJDKHome: home, HarnessJar: emptyJar(t), WorkDir: t.TempDir(), MaxProcesses: 1},
		CredentialCheck: func() error { return nil },
	}))
	defer server.Close()
	operator := config.JavaVuserOperatorConfig{Enabled: true, RunnerMode: config.JavaVuserRunnerModeSidecar, RunnerURL: server.URL, AuthToken: "compile-test-token"}
	response, err := CompileSource(context.Background(), operator, javasidecar.CompileRequest{TargetClass: "lrapi.lr", Sources: []javasidecar.CompileSourceFile{{Path: "lrapi/lr.java", Content: lrStubSource}}})
	if err != nil || response.ErrorCode != "" || !slices.Equal(response.Classes, []string{"lrapi.lr"}) {
		t.Fatalf("sidecar compile: %v %+v", err, response)
	}
}
