package javarunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in: create a secret-free archive with the selected JDK and verify
// the operator path against the real VM. This is not a memory/containment gate.
func TestOperatorCDSRealArchive(t *testing.T) {
	home := os.Getenv("APEXLOAD_JAVA_CDS_TEST_JDK_HOME")
	if home == "" {
		t.Skip("set APEXLOAD_JAVA_CDS_TEST_JDK_HOME to test real AppCDS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	jdk, err := ResolveJDK(ctx, ResolveOptions{Selection: JDKSelectionSpecified, SpecifiedHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if jdk.Major < 17 || jdk.VMFamily != JavaVMHotSpot {
		t.Fatal("test requires HotSpot 17+")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "CDSProbe.java")
	classes := filepath.Join(dir, "classes")
	if err = os.WriteFile(source, []byte(`public final class CDSProbe { public static void main(String[] args) { System.out.print("archive-probe"); } }`), 0600); err != nil {
		t.Fatal(err)
	}
	tool := func(name string) string {
		path := filepath.Join(jdk.Home, "bin", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
		return path + ".exe"
	}
	run := func(bin string, args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, out)
		}
		return string(out)
	}
	run(tool("javac"), "--release", "17", "-d", classes, source)
	jar := filepath.Join(dir, "probe.jar")
	run(tool("jar"), "--create", "--file", jar, "--main-class", "CDSProbe", "-C", classes, ".")
	spec := CommandSpec{ResolvedJDK: &jdk, JavaMajor: jdk.Major, VMFamily: jdk.VMFamily, HarnessJar: jar, HarnessMain: "CDSProbe"}
	plan, err := BuildCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "probe.jsa")
	run(jdk.JavaBin, append([]string{"-XX:ArchiveClassesAtExit=" + archive}, plan.Args...)...)
	if err = os.Chmod(archive, 0400); err != nil {
		t.Fatal(err)
	}
	hash := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		return hex.EncodeToString(digest[:])
	}
	manifest := OperatorCDSManifest{Schema: "apexload.java-vuser-cds/v1", JavaVersion: jdk.Version, JavaSHA256: hash(jdk.JavaBin), ReleaseSHA256: hash(filepath.Join(jdk.Home, "release")), HarnessSHA256: hash(jar), ClasspathSHA256: []string{}, ArchivePath: archive, ArchiveSHA256: hash(archive)}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	spec.OperatorCDSManifest = filepath.Join(dir, "manifest.json")
	if err = os.WriteFile(spec.OperatorCDSManifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err = BuildCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(jdk.JavaBin, plan.Args...); !strings.Contains(got, "archive-probe") {
		t.Fatalf("missing execution result: %s", got)
	}
}
