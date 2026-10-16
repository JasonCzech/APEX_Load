package javarunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// threadModeFixture is a thread-mode request on Java 8 whose shared library
// is a large WebLogic-style client jar with a manifest Class-Path.
func threadModeFixture(t *testing.T) PooledQualificationRequest {
	t.Helper()
	r := qualificationFixture(t)
	r.Launch.LoadRunnerThreads = true
	r.Launch.JDK.Major, r.Launch.JDK.Version = 8, "1.8.0_504"
	entries := []qualificationJarEntry{{"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\r\nClass-Path: ../../common/lib/apache_xbean.jar schema/weblogic-con\r\n tainer-binding.jar webservices.jar\r\n\r\n"}}
	for index := 0; index < 20000; index++ {
		entries = append(entries, qualificationJarEntry{fmt.Sprintf("weblogic/c%05d.class", index), "x"})
	}
	if err := os.WriteFile(r.Launch.SharedClasspath[0], qualificationJarBytes(t, entries), 0600); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestQualifyLoadRunnerThreadsAdmitsLegacyRuntimeAndLinkedLibraries(t *testing.T) {
	clean := NativeInspection{Complete: true, Assessment: "no_static_indicators"}
	r := threadModeFixture(t)
	selection, reason, err := QualifySharedAutomatically(context.Background(), r, clean)
	if err != nil || reason != "" || selection.Decision().Mode != SharedJVMMode {
		t.Fatalf("thread mode refused a Java 8 workload with a linked library: %q %v", reason, err)
	}
	threadsDigest := selection.Decision().ConfigurationSHA256
	provider, err := selection.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	total, _ := r.Resources.memoryBytes()
	if spec, err := provider(context.Background(), HostAllocation{ID: "host", SlotIDs: []string{"a"}, MemoryBytes: total, Binding: HostBinding{ConfigurationSHA256: threadsDigest}}); err != nil || !spec.LoadRunnerThreads {
		t.Fatalf("launch spec lost thread mode: %v", err)
	}

	// The same workload in the isolated mode is refused: Java 8, then the
	// library's size and manifest.
	isolated := r
	isolated.Launch.LoadRunnerThreads = false
	if _, reason, _ := QualifySharedAutomatically(context.Background(), isolated, clean); reason != SharedJVMReasonRuntime {
		t.Fatalf("isolated shared JVMs admitted Java 8: %q", reason)
	}
	isolated.Launch.JDK.Major = 17
	if _, reason, _ := QualifySharedAutomatically(context.Background(), isolated, clean); reason != SharedJVMReasonInputs {
		t.Fatalf("isolated shared JVMs admitted a manifest-linked library: %q", reason)
	}

	for name, tc := range map[string]struct {
		mutate func(*testing.T, *PooledQualificationRequest)
		want   string
	}{
		"unknown vm": {func(_ *testing.T, r *PooledQualificationRequest) { r.Launch.JDK.VMFamily = "" }, SharedJVMReasonRuntime},
		"present class-path file": {func(t *testing.T, r *PooledQualificationRequest) {
			if err := os.WriteFile(filepath.Join(filepath.Dir(r.Launch.SharedClasspath[0]), "webservices.jar"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
		}, SharedJVMReasonInputs},
		"per-slot jar keeps the strict checks": {func(t *testing.T, r *PooledQualificationRequest) {
			jar := qualificationJarBytes(t, []qualificationJarEntry{{"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\r\nClass-Path: absent.jar\r\n\r\n"}})
			if err := os.WriteFile(r.Launch.ScriptClasspath[0], jar, 0600); err != nil {
				t.Fatal(err)
			}
		}, SharedJVMReasonInputs},
		"url class-path": {func(t *testing.T, r *PooledQualificationRequest) {
			jar := qualificationJarBytes(t, []qualificationJarEntry{{"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\r\nClass-Path: http://example.invalid/x.jar\r\n\r\n"}})
			if err := os.WriteFile(r.Launch.SharedClasspath[0], jar, 0600); err != nil {
				t.Fatal(err)
			}
		}, SharedJVMReasonInputs},
		"jar index": {func(t *testing.T, r *PooledQualificationRequest) {
			jar := qualificationJarBytes(t, []qualificationJarEntry{{"META-INF/INDEX.LIST", "JarIndex-Version: 1.0\n"}})
			if err := os.WriteFile(r.Launch.SharedClasspath[0], jar, 0600); err != nil {
				t.Fatal(err)
			}
		}, SharedJVMReasonInputs},
		"native per-slot code": {nil, SharedJVMReasonNative},
	} {
		t.Run(name, func(t *testing.T) {
			r := threadModeFixture(t)
			native := clean
			if tc.mutate != nil {
				tc.mutate(t, &r)
			} else {
				native = NativeInspection{Complete: true, Assessment: "native_indicators_found", Findings: []NativeFinding{{Kind: "packaged_native_library"}}}
			}
			if _, reason, err := QualifySharedAutomatically(context.Background(), r, native); err != nil || reason != tc.want {
				t.Fatalf("reason %q err %v, want %q", reason, err, tc.want)
			}
		})
	}
}

func TestLoadRunnerThreadsFlagKeepsOtherModesDigests(t *testing.T) {
	spec := PooledLaunchSpec{TargetClass: "Actions"}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "LoadRunnerThreads") {
		t.Fatal("an unset thread-mode flag must not change a configuration digest")
	}
	r := qualificationFixture(t)
	isolated, err := InspectPooledQualification(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.Launch.LoadRunnerThreads = true
	threads, err := InspectPooledQualification(context.Background(), r)
	if err != nil || threads == isolated {
		t.Fatalf("thread mode must be part of the identity: %v", err)
	}
}

func TestSplitThreadModeClasspathKeepsOnlyScriptCodePerSlot(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, entries ...qualificationJarEntry) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, qualificationJarBytes(t, entries), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	actions := write("actions.jar", qualificationJarEntry{"Actions.class", "class Actions"})
	infra := write("infra.jar", qualificationJarEntry{"infra/BasicClient.class", "...lrapi/lr...message"})
	library := write("weblogic.jar", qualificationJarEntry{"weblogic/jndi/Environment.class", "javax/naming"})
	native := write("native-client.jar", qualificationJarEntry{"vendor/TerminalIO.class", "loadLibrary"}, qualificationJarEntry{"vendor/libterm.so", "ELF"})
	entries := []string{actions, infra, library, native}

	private, shared, reason := SplitThreadModeClasspath(context.Background(), entries, "Actions", false)
	if reason != "" || !slices.Equal(private, []string{actions, infra}) || !slices.Equal(shared, []string{library, native}) {
		t.Fatalf("private %v shared %v reason %q", private, shared, reason)
	}
	if private, shared, reason = SplitThreadModeClasspath(context.Background(), entries, "Actions", true); reason != "" || len(private) != 4 || len(shared) != 0 {
		t.Fatalf("LoadRunner's per-VUser class loader keeps every jar per slot: %v %v %q", private, shared, reason)
	}
	if _, _, reason = SplitThreadModeClasspath(context.Background(), []string{library}, "Actions", false); reason != SharedJVMReasonClasspath {
		t.Fatalf("no per-slot jar: %q", reason)
	}
	if _, _, reason = SplitThreadModeClasspath(context.Background(), []string{actions, dir}, "Actions", false); reason != SharedJVMReasonClasspath {
		t.Fatalf("a directory entry: %q", reason)
	}
	broken := filepath.Join(dir, "broken.jar")
	if err := os.WriteFile(broken, []byte("not a zip"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, reason = SplitThreadModeClasspath(context.Background(), []string{actions, broken}, "Actions", false); reason != SharedJVMReasonIncomplete {
		t.Fatalf("an unreadable jar: %q", reason)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, reason = SplitThreadModeClasspath(ctx, entries, "Actions", false); reason != SharedJVMReasonIncomplete {
		t.Fatalf("a cancelled scan: %q", reason)
	}
}

func TestManifestClassPathUnfoldsContinuationLines(t *testing.T) {
	got := manifestClassPath("Manifest-Version: 1.0\r\nClass-Path: a.jar sche\r\n ma/b.jar\r\n  c.jar\r\n\r\nName: x\nclass-path: d.jar\n")
	if !slices.Equal(got, []string{"a.jar", "schema/b.jar", "c.jar", "d.jar"}) {
		t.Fatalf("got %v", got)
	}
	dir := t.TempDir()
	jar := filepath.Join(dir, "lib.jar")
	if !classPathReferenceAbsent(jar, "missing.jar") || !classPathReferenceAbsent(jar, "sub%20dir/x.jar") {
		t.Fatal("absent references must be absent")
	}
	if err := os.WriteFile(filepath.Join(dir, "here.jar"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if classPathReferenceAbsent(jar, "here.jar") || classPathReferenceAbsent(jar, "file:///x.jar") || classPathReferenceAbsent(jar, "%zz") {
		t.Fatal("present, URL and undecodable references count as present")
	}
}

// Java 8 reports java.home as the jre directory inside its JDK; the launcher
// and the release file sit in the JDK above it.
func TestQualifyLoadRunnerThreadsCoversAJava8JDKAboveItsJRE(t *testing.T) {
	r := threadModeFixture(t)
	jdk := r.Launch.JDK.Home
	if err := os.MkdirAll(filepath.Join(jdk, "jre", "lib"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jdk, "jre", "lib", "rt.jar"), []byte("runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	// Outside the JRE, a JDK's man pages link a locale to a directory.
	if err := os.MkdirAll(filepath.Join(jdk, "man", "ja_JP.UTF-8"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ja_JP.UTF-8", filepath.Join(jdk, "man", "ja")); err != nil {
		t.Fatal(err)
	}
	r.Launch.JDK.Home = filepath.Join(jdk, "jre")
	digest, err := InspectPooledQualification(context.Background(), r)
	if err != nil {
		t.Fatalf("a Java 8 JDK with its JRE home was refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jdk, "jre", "lib", "rt.jar"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if changed, err := InspectPooledQualification(context.Background(), r); err != nil || changed == digest {
		t.Fatal("the identity must cover the JRE inside the JDK")
	}
	if err := os.Remove(filepath.Join(jdk, "release")); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPooledQualification(context.Background(), r); err == nil {
		t.Fatal("a JRE home outside a JDK must not qualify with a launcher above it")
	}
}

// A full JDK links a man-page locale to a directory inside its home, which
// thread mode qualifies as the isolated mode does
// (TestPooledQualificationRuntimeSymlinks); a directory link leaving the home
// does not.
func TestQualifyLoadRunnerThreadsAcceptsInHomeDirectoryLinks(t *testing.T) {
	r := threadModeFixture(t)
	home := r.Launch.JDK.Home
	if err := os.MkdirAll(filepath.Join(home, "man", "ja_JP.UTF-8"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "man", "ja_JP.UTF-8", "java.1"), []byte("page"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ja_JP.UTF-8", filepath.Join(home, "man", "ja")); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPooledQualification(context.Background(), r); err != nil {
		t.Fatalf("thread mode refused an in-home directory link: %v", err)
	}
	outside := t.TempDir()
	if err := os.Remove(filepath.Join(home, "man", "ja")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "man", "ja")); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPooledQualification(context.Background(), r); err == nil {
		t.Fatal("a directory link leaving the home must not qualify")
	}
}
