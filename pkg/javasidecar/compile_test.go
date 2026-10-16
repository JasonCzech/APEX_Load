package javasidecar

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

// hostCompileJDKHome is the host JDK's home, or a skip without a javac.
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

func compileServer(t *testing.T, jdkHome, harnessJar string) (*Server, *Client) {
	t.Helper()
	implementation := NewServer(ServerOptions{
		Operator: config.JavaVuserOperatorConfig{
			Enabled:        true,
			AuthToken:      testSidecarAuthToken,
			DefaultJDKHome: jdkHome,
			HarnessJar:     harnessJar,
			WorkDir:        t.TempDir(),
			MaxProcesses:   1,
		},
		CredentialCheck: func() error { return nil },
	})
	server := httptest.NewServer(implementation)
	t.Cleanup(server.Close)
	client, err := NewClientWithToken(server.URL, testSidecarAuthToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return implementation, client
}

func zipFile(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte(content))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// lrHarnessJar compiles an lr API stub through the route itself, so the next
// compile has a harness jar that provides lrapi.lr.
func lrHarnessJar(t *testing.T, jdkHome string) string {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "empty.jar")
	zipFile(t, empty, map[string]string{"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\n\r\n"})
	_, client := compileServer(t, jdkHome, empty)
	response, err := client.Compile(context.Background(), CompileRequest{
		TargetClass: "lrapi.lr",
		Sources:     []CompileSourceFile{{Path: "lrapi/lr.java", Content: []byte("package lrapi;\npublic final class lr {\n  public static final int AUTO = 2;\n  public static int start_transaction(String name) { return 0; }\n  public static int end_transaction(String name, int status) { return 0; }\n}\n")}},
	})
	if err != nil || response.ErrorCode != "" {
		t.Fatalf("compile lr stub: %v %+v", err, response)
	}
	jar := filepath.Join(t.TempDir(), "harness.jar")
	if err := os.WriteFile(jar, response.Jar, 0o600); err != nil {
		t.Fatal(err)
	}
	return jar
}

func TestCompileRouteCompilesSourceAgainstTheHarnessAndReportsMissingEntries(t *testing.T) {
	home := hostCompileJDKHome(t)
	_, client := compileServer(t, home, lrHarnessJar(t, home))
	response, err := client.Compile(context.Background(), CompileRequest{
		Runtime:     RuntimeSettings{Classpath: []string{"missing/library.jar"}},
		TargetClass: "Actions",
		Sources: []CompileSourceFile{
			{Path: "Actions.java", Content: []byte("import lrapi.lr;\npublic class Actions extends infra.Base {\n  public int action() { lr.start_transaction(\"t\"); return lr.end_transaction(\"t\", lr.AUTO); }\n}\n")},
			{Path: "Infra/Base.java", Content: []byte("package infra;\npublic class Base {}\n")},
		},
	})
	if err != nil || response.ErrorCode != "" {
		t.Fatalf("compile: %v %+v", err, response)
	}
	if !slices.Equal(response.Classes, []string{"Actions", "infra.Base"}) || response.SHA256 == "" || len(response.Jar) == 0 {
		t.Fatalf("response = %+v", response)
	}
	if !slices.Equal(response.UnavailableClasspath, []int{0}) {
		t.Fatalf("unavailable classpath = %v, want [0]", response.UnavailableClasspath)
	}
}

func TestCompileRouteReturnsJavacDiagnosticsForAFailedCompile(t *testing.T) {
	home := hostCompileJDKHome(t)
	_, client := compileServer(t, home, lrHarnessJar(t, home))
	response, err := client.Compile(context.Background(), CompileRequest{
		TargetClass: "Actions",
		Sources:     []CompileSourceFile{{Path: "Actions.java", Content: []byte("public class Actions {\n  int x = weblogic.jndi.Environment.NONE;\n}\n")}},
	})
	if err != nil {
		t.Fatalf("a failed compile is a response, not an error: %v", err)
	}
	if response.ErrorCode != javarunner.CompileFailed || len(response.Jar) != 0 {
		t.Fatalf("response = %+v", response)
	}
	if len(response.Diagnostics) == 0 || response.Diagnostics[0].File != "Actions.java" || response.Diagnostics[0].Line != 2 || !strings.Contains(response.Diagnostics[0].Message, "weblogic") {
		t.Fatalf("diagnostics = %+v", response.Diagnostics)
	}
}

func TestCompileRouteRefusesInvalidRequestsAndASecondConcurrentCompile(t *testing.T) {
	implementation, client := compileServer(t, t.TempDir(), filepath.Join(t.TempDir(), "harness.jar"))
	_, err := client.Compile(context.Background(), CompileRequest{TargetClass: "Actions"})
	if code := remoteErrorCode(err); code != javarunner.CompileSourcesInvalid {
		t.Fatalf("no sources: %v", err)
	}
	_, err = client.Compile(context.Background(), CompileRequest{TargetClass: "not a class", Sources: []CompileSourceFile{{Path: "Actions.java"}}})
	if code := remoteErrorCode(err); code != javarunner.CompileSourcesInvalid {
		t.Fatalf("bad target: %v", err)
	}
	implementation.compileSlots <- struct{}{}
	defer func() { <-implementation.compileSlots }()
	_, err = client.Compile(context.Background(), CompileRequest{TargetClass: "Actions", Sources: []CompileSourceFile{{Path: "Actions.java"}}})
	if code := remoteErrorCode(err); code != "compile_capacity_unavailable" {
		t.Fatalf("busy compiler: %v", err)
	}
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("busy compiler status: %v", err)
	}
}

func TestCompileRouteReportsAMissingCompiler(t *testing.T) {
	fixture := makeSidecarFixture(t, "")
	harness := filepath.Join(t.TempDir(), "harness.jar")
	zipFile(t, harness, map[string]string{"lrapi/lr.class": "x"})
	_, client := compileServer(t, fixture.jdkHome, harness)
	response, err := client.Compile(context.Background(), CompileRequest{TargetClass: "Actions", Sources: []CompileSourceFile{{Path: "Actions.java", Content: []byte("public class Actions {}")}}})
	if err != nil || response.ErrorCode != javarunner.CompileCompilerUnavailable {
		t.Fatalf("a JRE without a compiler JDK: %v %+v", err, response)
	}
}

func TestCapabilitiesAdvertiseSourceCompile(t *testing.T) {
	if !slices.Contains(capabilityFeatures(false), FeatureSourceCompileV1) {
		t.Fatal("capabilities must advertise source compile")
	}
}
