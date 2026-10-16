package javarunner

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// hostCompileJDK resolves the host's JDK, skipping when it has no javac.
func hostCompileJDK(t *testing.T) ResolvedJDK {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	jdk, err := ResolveJDK(ctx, ResolveOptions{Selection: JDKSelectionInternal, AllowLegacyJava: true})
	if err != nil {
		t.Skipf("no host JDK: %v", err)
	}
	if _, ok := javacFor(jdk); !ok {
		t.Skip("host JDK has no javac")
	}
	return jdk
}

// lrAPIJar compiles a minimal lrapi.lr into a jar the sources compile against.
func lrAPIJar(t *testing.T, jdk ResolvedJDK) string {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "empty.jar")
	writeZip(t, empty, map[string]string{"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\n\r\n"})
	result, err := CompileJavaSources(context.Background(), CompileSpec{
		JDK:         jdk,
		Sources:     []CompileSource{{Path: "lrapi/lr.java", Content: []byte("package lrapi;\npublic final class lr {\n  public static final int AUTO = 2;\n  public static int start_transaction(String name) { return 0; }\n  public static int end_transaction(String name, int status) { return 0; }\n  public static String eval_string(String value) { return value; }\n}\n")}},
		APIJar:      empty,
		TargetClass: "lrapi.lr",
		WorkDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("compile lr stub: %v", err)
	}
	path := filepath.Join(t.TempDir(), "lr-api.jar")
	if err := os.WriteFile(path, result.Jar, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jarEntries(t *testing.T, jar []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(jar), int64(len(jar)))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte)
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(handle)
		handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[file.Name] = data
	}
	return out
}

const compileActionsSource = `import lrapi.lr;
import infra.BasicClient;

public class Actions extends BasicClient {
    public int init() throws Throwable { return 0; }
    public int action() throws Throwable {
        lr.start_transaction("order");
        lr.end_transaction("order", lr.AUTO);
        return 0;
    }
    public int end() throws Throwable { return 0; }
}
`

const compileBaseSource = `package infra;

public abstract class BasicClient {
    protected String name() { return "client"; }
}
`

func TestCompileJavaSourcesBuildsADeterministicJarOfTheScriptClasses(t *testing.T) {
	jdk := hostCompileJDK(t)
	api := lrAPIJar(t, jdk)
	spec := CompileSpec{
		JDK:         jdk,
		Sources:     []CompileSource{{Path: "Actions.java", Content: []byte(compileActionsSource)}, {Path: "Infra/BasicClient.java", Content: []byte(compileBaseSource)}},
		APIJar:      api,
		TargetClass: "Actions",
		WorkDir:     t.TempDir(),
	}
	first, err := CompileJavaSources(context.Background(), spec)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !slices.Equal(first.Classes, []string{"Actions", "infra.BasicClient"}) {
		t.Fatalf("classes = %v", first.Classes)
	}
	entries := jarEntries(t, first.Jar)
	if _, ok := entries["lrapi/lr.class"]; ok {
		t.Fatal("the jar must not carry the lr API it compiled against")
	}
	if !strings.HasPrefix(string(entries["META-INF/MANIFEST.MF"]), "Manifest-Version: 1.0") {
		t.Fatal("jar has no manifest")
	}
	if first.Release != jdk.Major || first.CompilerVersion != jdk.Version {
		t.Fatalf("release %d compiler %q, want the runtime's own javac", first.Release, first.CompilerVersion)
	}
	spec.WorkDir = t.TempDir()
	second, err := CompileJavaSources(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatal("the same sources must compile to the same jar bytes")
	}
	leftovers, _ := filepath.Glob(filepath.Join(spec.WorkDir, compileDirPrefix+"*"))
	if len(leftovers) != 0 {
		t.Fatalf("compile directory was not removed: %v", leftovers)
	}
}

func TestCompileJavaSourcesReportsJavacErrorsAgainstTheSourceFiles(t *testing.T) {
	jdk := hostCompileJDK(t)
	api := lrAPIJar(t, jdk)
	missing := filepath.Join(t.TempDir(), "secret-path", "missing.jar")
	_, err := CompileJavaSources(context.Background(), CompileSpec{
		JDK:         jdk,
		Sources:     []CompileSource{{Path: "Actions.java", Content: []byte("public class Actions {\n  public int action() { return undefinedCall(); }\n}\n")}},
		APIJar:      api,
		Classpath:   []string{missing},
		TargetClass: "Actions",
		WorkDir:     t.TempDir(),
	})
	var compileErr *CompileError
	if !errors.As(err, &compileErr) || compileErr.Category != CompileFailed {
		t.Fatalf("err = %v, want %s", err, CompileFailed)
	}
	found := false
	for _, diagnostic := range compileErr.Diagnostics {
		if strings.Contains(diagnostic.Message, "secret-path") || strings.Contains(diagnostic.Message, api) {
			t.Fatalf("diagnostic echoes a runner path: %+v", diagnostic)
		}
		if diagnostic.File == "Actions.java" && diagnostic.Line == 2 && diagnostic.Kind == "error" && strings.Contains(diagnostic.Message, "undefinedCall") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v, want Actions.java:2 naming undefinedCall", compileErr.Diagnostics)
	}
}

func TestCompileJavaSourcesRequiresTheTargetClass(t *testing.T) {
	jdk := hostCompileJDK(t)
	_, err := CompileJavaSources(context.Background(), CompileSpec{
		JDK:         jdk,
		Sources:     []CompileSource{{Path: "Helper.java", Content: []byte("public class Helper {}\n")}},
		APIJar:      lrAPIJar(t, jdk),
		TargetClass: "Actions",
		WorkDir:     t.TempDir(),
	})
	var compileErr *CompileError
	if !errors.As(err, &compileErr) || compileErr.Category != CompileTargetMissing {
		t.Fatalf("err = %v, want %s", err, CompileTargetMissing)
	}
}

func TestCompileJavaSourcesReadsWindowsEncodedSourceWithAByteOrderMark(t *testing.T) {
	jdk := hostCompileJDK(t)
	// "café" in windows-1252, so the file is not valid UTF-8.
	windows := []byte("public class Actions {\n  public String name() { return \"caf\xe9\"; }\n}\n")
	bom := append([]byte("\xef\xbb\xbf"), []byte("public class Helper {}\n")...)
	result, err := CompileJavaSources(context.Background(), CompileSpec{
		JDK:         jdk,
		Sources:     []CompileSource{{Path: "Actions.java", Content: windows}, {Path: "Helper.java", Content: bom}},
		APIJar:      lrAPIJar(t, jdk),
		TargetClass: "Actions",
		WorkDir:     t.TempDir(),
	})
	if err != nil {
		var compileErr *CompileError
		errors.As(err, &compileErr)
		t.Fatalf("compile: %v %+v", err, compileErr)
	}
	if !bytes.Contains(jarEntries(t, result.Jar)["Actions.class"], []byte("caf\xc3\xa9")) {
		t.Fatal("the windows-1252 literal was not decoded to é")
	}
}

func TestCompileJavaSourcesQuotesClasspathPathsWithSpaces(t *testing.T) {
	jdk := hostCompileJDK(t)
	api := lrAPIJar(t, jdk)
	library, err := CompileJavaSources(context.Background(), CompileSpec{
		JDK:         jdk,
		Sources:     []CompileSource{{Path: "lib/Greeter.java", Content: []byte("package lib;\npublic class Greeter { public static String hi() { return \"hi\"; } }\n")}},
		APIJar:      api,
		TargetClass: "lib.Greeter",
		WorkDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	jar := filepath.Join(t.TempDir(), "a dir with \"quotes\" and spaces", "greeter lib.jar")
	if err := os.MkdirAll(filepath.Dir(jar), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jar, library.Jar, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileJavaSources(context.Background(), CompileSpec{
		JDK:         jdk,
		Sources:     []CompileSource{{Path: "My Script/Actions.java", Content: []byte("public class Actions { String v = lib.Greeter.hi(); }\n")}},
		APIJar:      api,
		Classpath:   []string{jar},
		TargetClass: "Actions",
		WorkDir:     t.TempDir(),
	}); err != nil {
		var compileErr *CompileError
		errors.As(err, &compileErr)
		t.Fatalf("compile against a spaced classpath entry: %v %+v", err, compileErr)
	}
}

func TestCompileJavaSourcesTargetsAnOlderRuntimeWithAnOperatorCompiler(t *testing.T) {
	jdk := hostCompileJDK(t)
	if jdk.Major <= 17 {
		t.Skip("needs a host JDK newer than 17")
	}
	runtime := ResolvedJDK{Home: t.TempDir(), JavaBin: "java", Version: "17", Major: 17, VMFamily: JavaVMHotSpot}
	spec := CompileSpec{
		JDK:         runtime,
		Sources:     []CompileSource{{Path: "Actions.java", Content: []byte("public class Actions {}\n")}},
		APIJar:      lrAPIJar(t, jdk),
		TargetClass: "Actions",
		WorkDir:     t.TempDir(),
	}
	_, err := CompileJavaSources(context.Background(), spec)
	var compileErr *CompileError
	if !errors.As(err, &compileErr) || compileErr.Category != CompileCompilerUnavailable {
		t.Fatalf("a runtime without javac and no compiler JDK: err = %v", err)
	}
	spec.Compiler = &jdk
	result, err := CompileJavaSources(context.Background(), spec)
	if err != nil {
		t.Fatalf("compile with --release: %v", err)
	}
	class := jarEntries(t, result.Jar)["Actions.class"]
	if len(class) < 8 || int(class[6])<<8|int(class[7]) != 61 {
		t.Fatalf("class file major version = %v, want 61 (Java 17)", class[6:8])
	}
	if result.Release != 17 {
		t.Fatalf("release = %d", result.Release)
	}
	older := jdk
	older.Major = 16
	spec.Compiler = &older
	if _, err := CompileJavaSources(context.Background(), spec); !errors.As(err, &compileErr) || compileErr.Category != CompileCompilerUnavailable {
		t.Fatalf("an older compiler JDK must be refused: %v", err)
	}
}

func TestCompileJavaSourcesRefusesUnsafeSourcePaths(t *testing.T) {
	base := CompileSpec{JDK: ResolvedJDK{Major: 17}, APIJar: "api.jar", TargetClass: "Actions", WorkDir: t.TempDir()}
	for name, sources := range map[string][]CompileSource{
		"none":      nil,
		"absolute":  {{Path: "/tmp/Actions.java"}},
		"traversal": {{Path: "../Actions.java"}},
		"inner dot": {{Path: "a/../Actions.java"}},
		"not java":  {{Path: "Actions.txt"}},
		"backslash": {{Path: `Infra\Actions.java`}},
		"duplicate": {{Path: "Actions.java"}, {Path: "actions.java"}},
		"too large": {{Path: "Actions.java", Content: make([]byte, MaxCompileSourceBytes+1)}},
	} {
		spec := base
		spec.Sources = sources
		var compileErr *CompileError
		if _, err := CompileJavaSources(context.Background(), spec); !errors.As(err, &compileErr) || compileErr.Category != CompileSourcesInvalid {
			t.Errorf("%s: err = %v, want %s", name, err, CompileSourcesInvalid)
		}
	}
}

func TestJavacForFindsTheJDKCompilerBesideAJava8JRE(t *testing.T) {
	jdk := t.TempDir()
	if err := os.MkdirAll(filepath.Join(jdk, "jre", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(jdk, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	javac := filepath.Join(jdk, "bin", "javac")
	if err := os.WriteFile(javac, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, ok := javacFor(ResolvedJDK{Home: filepath.Join(jdk, "jre")}); !ok || got != javac {
		t.Fatalf("javacFor = %q %v", got, ok)
	}
	if _, ok := javacFor(ResolvedJDK{Home: filepath.Join(jdk, "jre", "bin")}); ok {
		t.Fatal("only a home named jre falls back to its parent")
	}
}

func TestJavacArgFileQuotesEveryArgument(t *testing.T) {
	got := javacArgFile([]string{"-d", `C:\out dir`, `say "hi"`})
	want := "\"-d\"\n\"C:\\\\out dir\"\n\"say \\\"hi\\\"\"\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolveCompileClasspathSkipsMissingEntriesButNotInvalidOnes(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.jar")
	writeZip(t, present, map[string]string{"a.txt": "a"})
	resolved, unavailable, issue := ResolveCompileClasspath([]string{"missing.jar", present, filepath.Join(dir, "gone.jar")}, dir)
	if issue != nil || len(resolved) != 1 || !slices.Equal(unavailable, []int{0, 2}) {
		t.Fatalf("resolved %v unavailable %v issue %v", resolved, unavailable, issue)
	}
	if _, _, issue := ResolveCompileClasspath([]string{present, "lib/*"}, dir); issue == nil || issue.Index != 1 || issue.Category != ClasspathWildcard {
		t.Fatalf("a wildcard entry must fail the classpath: %v", issue)
	}
}
