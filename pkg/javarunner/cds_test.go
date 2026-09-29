package javarunner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cdsFixture(t *testing.T) (CommandSpec, OperatorCDSManifest, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name string) (string, string) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0400); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256([]byte(name))
		return p, hex.EncodeToString(h[:])
	}
	java, jhash := write("java")
	_, release := write("release")
	harness, hhash := write("harness.jar")
	archive, ahash := write("archive.jsa")
	cp, chash := write("actions.jar")
	m := OperatorCDSManifest{Schema: "apexload.java-vuser-cds/v1", JavaVersion: "21", JavaSHA256: jhash, ReleaseSHA256: release, HarnessSHA256: hhash, ClasspathSHA256: []string{chash}, ArchivePath: archive, ArchiveSHA256: ahash}
	path := filepath.Join(dir, "cds.json")
	data, _ := json.Marshal(m)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return CommandSpec{OperatorCDSManifest: path, ResolvedJDK: &ResolvedJDK{Home: dir, JavaBin: java, Version: "21", Major: 21, VMFamily: JavaVMHotSpot}, JavaMajor: 21, VMFamily: JavaVMHotSpot, HarnessJar: harness, ScriptClasspath: []string{cp}}, m, path
}

func TestOperatorCDS(t *testing.T) {
	for _, name := range []string{"valid", "wrong hash", "wrong jdk", "wrong classpath", "writable", "conflict", "legacy", "trailing", "inconsistent major", "inconsistent family"} {
		t.Run(name, func(t *testing.T) {
			s, m, path := cdsFixture(t)
			switch name {
			case "wrong hash":
				m.ArchiveSHA256 = strings.Repeat("0", 64)
			case "wrong jdk":
				s.ResolvedJDK.Version = "17"
			case "wrong classpath":
				m.ClasspathSHA256 = nil
			case "writable":
				if err := os.Chmod(m.ArchivePath, 0600); err != nil {
					t.Fatal(err)
				}
			case "conflict":
				s.VMParameters = []string{"-Xshare:off"}
			case "legacy":
				s.ResolvedJDK.Major = 8
			case "inconsistent major":
				s.JavaMajor = 17
			case "inconsistent family":
				s.VMFamily = JavaVMOpenJ9
			}
			data, _ := json.Marshal(m)
			if name == "trailing" {
				data = append(data, []byte("{}")...)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			plan, err := BuildCommand(s)
			if name != "valid" {
				if err == nil || strings.Contains(err.Error(), m.ArchivePath) {
					t.Fatalf("expected value-free refusal, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Join(plan.Args, " ")
			canonical, _ := filepath.EvalSymlinks(m.ArchivePath)
			if !strings.Contains(args, "-Xshare:on") || !strings.Contains(args, "-XX:SharedArchiveFile="+canonical) {
				t.Fatal(args)
			}
		})
	}
}

func TestScriptCDSStillRejected(t *testing.T) {
	for _, arg := range []string{"-XX:SharedArchiveFile=/tmp/a.jsa", "-XX:ArchiveClassesAtExit=/tmp/a.jsa"} {
		if len(ValidateVMParametersForExecution([]string{arg})) == 0 {
			t.Fatalf("accepted script CDS: %s", arg)
		}
	}
}
