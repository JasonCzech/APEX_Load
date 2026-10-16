package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

var javaCompileTestSources = map[string]string{
	"Actions.java":           "public class Actions extends infra.Base {}\n",
	"Infra/BasicClient.java": "package infra;\npublic class Base {}\n",
}

func javaCompileTestJar(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create("Actions.class")
	_, _ = entry.Write([]byte{0xca, 0xfe, 0xba, 0xbe})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// stubJavaCompile replaces the runner call and records its request.
func stubJavaCompile(t *testing.T, respond func(javasidecar.CompileRequest) (javasidecar.CompileResponse, error)) *javasidecar.CompileRequest {
	t.Helper()
	var seen javasidecar.CompileRequest
	previous := compileJavaSource
	compileJavaSource = func(_ context.Context, _ config.JavaVuserOperatorConfig, request javasidecar.CompileRequest) (javasidecar.CompileResponse, error) {
		seen = request
		return respond(request)
	}
	t.Cleanup(func() { compileJavaSource = previous })
	return &seen
}

func postJavaCompile(t *testing.T, server *Server, projectID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/import-1/java-compile?project_id="+projectID, bytes.NewReader(data))
	request.SetPathValue("id", "import-1")
	recorder := httptest.NewRecorder()
	server.handleCompileJavaSource(recorder, request)
	return recorder
}

func javaCompileTestBody(manifest *enterprisearchive.JavaVuserSourceManifest, classpath []string, assets []dsl.JavaClasspathAsset) map[string]any {
	return map[string]any{"target_class": "Actions", "source_manifest": manifest, "java_vuser": map[string]any{"classpath": classpath, "classpath_assets": assets}}
}

func TestCompileJavaSourceStoresTheJarWithItsSourceProvenance(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	server := javaReviewTestServer(t, false, nil)
	server.retainJavaSourceTexts(context.Background(), "project-a", javaCompileTestSources)
	manifest := enterprisearchive.BuildJavaVuserSourceManifest(javaCompileTestSources)
	dependency := javaReviewTestAsset(t, server, "project-a", "libs/client.jar", "client")
	jar := javaCompileTestJar(t)
	sum := sha256.Sum256(jar)
	seen := stubJavaCompile(t, func(javasidecar.CompileRequest) (javasidecar.CompileResponse, error) {
		return javasidecar.CompileResponse{Jar: jar, SHA256: hex.EncodeToString(sum[:]), Classes: []string{"Actions", "infra.Base"}, UnavailableClasspath: []int{1}}, nil
	})
	classpath := []string{enterprisearchive.JavaVuserScriptClassesEntry, "libs/client.jar", `\\host\share\weblogic.jar`}
	stale := dependency
	stale.Entry = enterprisearchive.JavaVuserScriptClassesEntry
	recorder := postJavaCompile(t, server, "project-a", javaCompileTestBody(manifest, classpath, []dsl.JavaClasspathAsset{dependency, stale}))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Join(seen.Runtime.Classpath, "|") != `libs/client.jar|\\host\share\weblogic.jar` || len(seen.Runtime.ClasspathAssets) != 1 || seen.Runtime.ClasspathAssets[0].Entry != "libs/client.jar" {
		t.Fatalf("the compile must leave out the classes entry and its old jar: %+v", seen.Runtime)
	}
	if len(seen.Sources) != 2 || seen.Sources[0].Path != "Actions.java" || string(seen.Sources[1].Content) != javaCompileTestSources["Infra/BasicClient.java"] {
		t.Fatalf("sources = %+v", seen.Sources)
	}
	var response javaCompileResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Entry != enterprisearchive.JavaVuserScriptClassesEntry || response.CompiledFrom != manifest.Digest || response.SHA256 != hex.EncodeToString(sum[:]) || strings.Join(response.UnavailableClasspath, "|") != `\\host\share\weblogic.jar` {
		t.Fatalf("response = %+v", response)
	}
	asset := dsl.JavaClasspathAsset{Entry: response.Entry, FileName: response.FileName, SHA256: response.SHA256, SizeBytes: response.SizeBytes, StorageRef: response.StorageRef, ContentType: response.ContentType, CompiledFrom: response.CompiledFrom}
	script := &dsl.Script{Protocol: "java_vuser", ProjectID: "project-a"}
	script.Config.JavaVuser.Classpath = classpath
	script.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{asset}
	if err := server.validateAuthoringJavaClasspathAssets(script, classpath); err != nil {
		t.Fatalf("a compiled jar must validate: %v", err)
	}
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"review_artifact": map[string]any{"metadata": map[string]any{enterprisearchive.JavaVuserSourceManifestMetadataKey: manifest}}}}}
	if err := server.validateAuthoringJavaClasspathAssets(script, classpath); err != nil {
		t.Fatalf("a jar compiled from the draft's own manifest must validate: %v", err)
	}
	other := enterprisearchive.BuildJavaVuserSourceManifest(map[string]string{"Actions.java": "public class Actions {}\n"})
	script.Metadata.Import.Preserved["review_artifact"] = map[string]any{"metadata": map[string]any{enterprisearchive.JavaVuserSourceManifestMetadataKey: other}}
	if err := server.validateAuthoringJavaClasspathAssets(script, classpath); err == nil {
		t.Fatal("a jar compiled from another script's source must be refused")
	}
	script.Metadata = nil
	forged := asset
	forged.CompiledFrom = other.Digest
	script.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{forged}
	if err := server.validateAuthoringJavaClasspathAssets(script, classpath); err == nil {
		t.Fatal("a binding must not claim provenance the server did not record")
	}
	elsewhere := asset
	elsewhere.Entry = "libs/client.jar"
	script.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{elsewhere}
	if err := server.validateAuthoringJavaClasspathAssets(script, classpath); err == nil {
		t.Fatal("a compiled jar belongs only on the script classes entry")
	}
	uploaded := dependency
	uploaded.CompiledFrom = manifest.Digest
	script.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{uploaded}
	if err := server.validateAuthoringJavaClasspathAssets(script, classpath); err == nil {
		t.Fatal("an uploaded jar must not claim compile provenance")
	}
	ref, _ := url.Parse(response.StorageRef)
	if recorded, err := os.ReadFile(ref.Path + javaCompileProvenanceSuffix); err != nil || string(recorded) != manifest.Digest {
		t.Fatalf("provenance file = %q %v", recorded, err)
	}
}

func TestCompileJavaSourceReportsCompileErrorsAndRefusals(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	server := javaReviewTestServer(t, false, nil)
	server.retainJavaSourceTexts(context.Background(), "project-a", javaCompileTestSources)
	manifest := enterprisearchive.BuildJavaVuserSourceManifest(javaCompileTestSources)
	classpath := []string{enterprisearchive.JavaVuserScriptClassesEntry, `\\host\share\weblogic.jar`}
	stubJavaCompile(t, func(javasidecar.CompileRequest) (javasidecar.CompileResponse, error) {
		return javasidecar.CompileResponse{ErrorCode: javarunner.CompileFailed, Diagnostics: []javarunner.CompileDiagnostic{{File: "Actions.java", Line: 4, Kind: "error", Message: "package weblogic.jndi does not exist"}}, UnavailableClasspath: []int{0}}, nil
	})
	recorder := postJavaCompile(t, server, "project-a", javaCompileTestBody(manifest, classpath, nil))
	var failure javaCompileFailureResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &failure)
	if recorder.Code != http.StatusUnprocessableEntity || failure.Code != javarunner.CompileFailed || len(failure.Diagnostics) != 1 || strings.Join(failure.UnavailableClasspath, "|") != `\\host\share\weblogic.jar` {
		t.Fatalf("compile failure = %d %s", recorder.Code, recorder.Body.String())
	}
	stubJavaCompile(t, func(javasidecar.CompileRequest) (javasidecar.CompileResponse, error) {
		return javasidecar.CompileResponse{}, &javasidecar.RemoteError{StatusCode: http.StatusServiceUnavailable, Code: "runner_disabled"}
	})
	if recorder := postJavaCompile(t, server, "project-a", javaCompileTestBody(manifest, classpath, nil)); recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "runner_disabled") {
		t.Fatalf("disabled runner = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := postJavaCompile(t, server, "project-b", javaCompileTestBody(manifest, classpath, nil)); recorder.Code != http.StatusConflict {
		t.Fatalf("another project's source must be unavailable: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := postJavaCompile(t, server, "project-a", javaCompileTestBody(manifest, []string{`\\host\share\weblogic.jar`}, nil)); recorder.Code != http.StatusBadRequest {
		t.Fatalf("a classpath without the classes entry: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := postJavaCompile(t, server, "project-a", javaCompileTestBody(nil, classpath, nil)); recorder.Code != http.StatusBadRequest {
		t.Fatalf("no manifest: %d %s", recorder.Code, recorder.Body.String())
	}
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "false")
	if recorder := postJavaCompile(t, server, "project-a", javaCompileTestBody(manifest, classpath, nil)); recorder.Code != http.StatusNotFound {
		t.Fatalf("uploads off: %d", recorder.Code)
	}
}

func TestRetainJavaSourceTextsKeepsNothingWhileUploadsAreOff(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "false")
	server := javaReviewTestServer(t, false, nil)
	server.retainJavaSourceTexts(context.Background(), "project-a", javaCompileTestSources)
	sum := sha256.Sum256([]byte(javaCompileTestSources["Actions.java"]))
	if ok, _ := server.projectTextExists(context.Background(), javaSourceTextKind, "project-a", hex.EncodeToString(sum[:])); ok {
		t.Fatal("source kept while uploads are off")
	}
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	server.retainJavaSourceTexts(context.Background(), "project-a", javaCompileTestSources)
	if ok, _ := server.projectTextExists(context.Background(), javaSourceTextKind, "project-a", hex.EncodeToString(sum[:])); !ok {
		t.Fatal("source not kept")
	}
	if ok, _ := server.sourceReviewTextExists(context.Background(), "project-a", hex.EncodeToString(sum[:])); ok {
		t.Fatal("Java source must not be readable as a source-review text")
	}
}
