package javarunner

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func inspectionArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range files {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func inspectFixture(t *testing.T, name string, body []byte) (NativeInspection, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if e := os.WriteFile(path, body, 0600); e != nil {
		t.Fatal(e)
	}
	return InspectApplicationNative(t.Context(), []string{path})
}

func TestNativeInspectionEvidenceAndBounds(t *testing.T) {
	r, e := inspectFixture(t, "Actions.java", []byte("public class Actions {public int action(){return 0;}}"))
	if e != nil || !r.Complete || r.Assessment != "no_static_indicators" || len(r.Limitations) < 3 {
		t.Fatal(r, e)
	}
	r, e = inspectFixture(t, "Actions.java", []byte("class Actions {public native int call();}"))
	if e != nil || r.Assessment != "native_indicators_found" || r.Findings[0].Artifact != r.Artifacts[0].SHA256 {
		t.Fatal(r, e)
	}
	for _, source := range []string{"class A {native int call();}", "class A {void action(){System . load(\"library\");}}", "import com.sun.jna.Native;"} {
		found, err := inspectFixture(t, "Source.java", []byte(source))
		if err != nil || len(found.Findings) == 0 {
			t.Fatal("native source indicator missed", err)
		}
	}
	jar := inspectionArchive(t, map[string][]byte{"lib.so": {1, 2}, "notes.txt": []byte("ordinary resource")})
	r, e = inspectFixture(t, "source.zip", inspectionArchive(t, map[string][]byte{"dependency.jar": jar}))
	if e != nil || len(r.Findings) != 1 || r.Findings[0].Kind != "packaged_native_library" {
		t.Fatal(r, e)
	}
	for _, test := range []struct {
		name string
		body []byte
	}{{"broken.class", []byte("bad")}, {"broken.jar", []byte("bad")}, {"unsupported.bin", []byte("bad")}} {
		r, e = inspectFixture(t, test.name, test.body)
		if e == nil || r.Complete || r.Assessment != "incomplete" {
			t.Fatal(test, r, e)
		}
	}
	nested := jar
	for i := 0; i < 4; i++ {
		nested = inspectionArchive(t, map[string][]byte{"nested.jar": nested})
	}
	if r, e = inspectFixture(t, "nested.jar", nested); e == nil || r.Complete {
		t.Fatal("nesting bound not enforced")
	}
	// The entry limit is checked before archive/zip materializes file objects.
	oversized := inspectionArchive(t, map[string][]byte{"ok.txt": {1}})
	oversized[len(oversized)-12] = 0xff
	oversized[len(oversized)-11] = 0xff
	if r, e = inspectFixture(t, "flood.jar", oversized); e == nil || r.Complete {
		t.Fatal("malformed directory accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, e = InspectApplicationNative(ctx, []string{"unused"}); e == nil || r.Complete {
		t.Fatal("cancelled inspection succeeded")
	}
	if r, e = InspectApplicationNative(t.Context(), []string{t.TempDir()}); e == nil || r.Complete {
		t.Fatal("directory accepted")
	}
}

func TestNativeInspectionRealJavaBytecode(t *testing.T) {
	javac, e := exec.LookPath("javac")
	if e != nil {
		t.Skip("javac unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "NativeActions.java")
	if e = os.WriteFile(source, []byte(`public class NativeActions {public native int invoke();public void load(){System.loadLibrary("fixture");}}`), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if out, e := exec.CommandContext(ctx, javac, "--release", "8", "-d", dir, source).CombinedOutput(); e != nil {
		t.Fatalf("javac: %v %s", e, out)
	}
	class := filepath.Join(dir, "NativeActions.class")
	first, e := InspectApplicationNative(ctx, []string{class})
	if e != nil {
		t.Fatal(e)
	}
	kinds := map[string]bool{}
	for _, f := range first.Findings {
		kinds[f.Kind] = true
	}
	if !kinds["native_method"] || !kinds["bytecode_library_load_indicator"] {
		t.Fatal(first)
	}
	second, e := InspectApplicationNative(ctx, []string{class})
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatal("inspection is not reproducible")
	}
	body, e := os.ReadFile(class)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < len(body); i++ {
		if _, _, _, e := inspectClassNative(body[:i]); e == nil {
			t.Fatal("truncated class accepted", i)
		}
	}
	if strings.Contains(string(a), "System.loadLibrary") {
		t.Fatal("raw source disclosed")
	}
}

// Ordinary middleware calls methods named load (Properties, KeyStore,
// ServiceLoader, Guava's CacheLoader); only System and Runtime loads count.
func TestNativeInspectionResolvesLibraryLoadOwners(t *testing.T) {
	javac, e := exec.LookPath("javac")
	if e != nil {
		t.Skip("javac unavailable")
	}
	dir := t.TempDir()
	sources := map[string]string{
		"CleanActions": `import java.util.*; public class CleanActions { public void load() {} public int run(java.io.InputStream in) throws Exception { new Properties().load(in); ServiceLoader.load(Runnable.class); java.security.KeyStore.getInstance("JKS").load(null, null); load(); return 0; } }`,
		"SystemLoad":   `public class SystemLoad { public void run() { System.load("/opt/lib/libfixture.so"); } }`,
		"RuntimeLoad":  `public class RuntimeLoad { public void run() { Runtime.getRuntime().loadLibrary("fixture"); } }`,
	}
	var files []string
	for name, body := range sources {
		path := filepath.Join(dir, name+".java")
		if e := os.WriteFile(path, []byte(body), 0o600); e != nil {
			t.Fatal(e)
		}
		files = append(files, path)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if out, e := exec.CommandContext(ctx, javac, append([]string{"--release", "8", "-d", dir}, files...)...).CombinedOutput(); e != nil {
		t.Fatalf("javac: %v %s", e, out)
	}
	for name, want := range map[string]bool{"CleanActions": false, "SystemLoad": true, "RuntimeLoad": true} {
		report, e := InspectApplicationNative(ctx, []string{filepath.Join(dir, name+".class")})
		if e != nil {
			t.Fatal(e)
		}
		if found := len(report.Findings) > 0; found != want {
			t.Fatalf("%s: findings %+v, want indicators=%v", name, report.Findings, want)
		}
	}
}
