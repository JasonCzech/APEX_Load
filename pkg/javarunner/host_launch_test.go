package javarunner

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func launchFixture(t *testing.T) (PooledLaunchSpec, HostAllocation) {
	t.Helper()
	dir := t.TempDir()
	spec := PooledLaunchSpec{ConfigurationSHA256: strings.Repeat("a", 64), JDK: ResolvedJDK{Home: filepath.Join(dir, "jdk"), JavaBin: filepath.Join(dir, "jdk", "bin", "java"), Version: "17-fixture", Major: 17, VMFamily: JavaVMHotSpot}, WorkDir: dir, TargetClass: "script.Actions", VMParameters: []string{"-Xmx128m"}, QualifiedCleanup: true}
	for _, name := range []string{"host.jar", "shim.jar", "script.jar", "dependency.jar"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture identity"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	spec.HarnessJar = filepath.Join(dir, "host.jar")
	spec.ShimJar = filepath.Join(dir, "shim.jar")
	spec.ScriptClasspath = []string{filepath.Join(dir, "script.jar")}
	spec.SharedClasspath = []string{filepath.Join(dir, "dependency.jar")}
	allocation := HostAllocation{ID: "generation", SlotIDs: []string{"slot-a", "slot-b"}, MemoryBytes: 512 << 20, Binding: HostBinding{ConfigurationSHA256: spec.ConfigurationSHA256}}
	return spec, allocation
}
func TestPooledLaunchFixedEntrypointAndPrivateArtifacts(t *testing.T) {
	spec, a := launchFixture(t)
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "must-not-reach-child")
	t.Setenv("JAVA_TOOL_OPTIONS", "-javaagent:must-not-reach-child")
	cmd, err := buildPooledCommand(spec, a, 1234, pooledRuntimeSupported)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range cmd.Env {
		if strings.Contains(entry, "must-not-reach-child") {
			t.Fatal("ambient options/credentials inherited")
		}
	}
	if cmd.Cancel != nil || cmd.Dir != spec.WorkDir {
		t.Fatal("persistent command lifetime/workdir changed")
	}
	cp := -1
	for i, arg := range cmd.Args {
		if arg == "-cp" {
			cp = i
			break
		}
	}
	if cp < 0 || cmd.Args[cp+1] != spec.HarnessJar || cmd.Args[cp+2] != "apexload.host.PooledMain" {
		t.Fatal("user classes reached system loader", cmd.Args)
	}
	if got := cmd.Args[cp+3 : cp+9]; strings.Join(got, "|") != strings.Join([]string{a.ID, "2", spec.ShimJar, spec.TargetClass, "true", "1234"}, "|") {
		t.Fatal("host bindings not conveyed exactly", got)
	}
}
func TestPooledLaunchRejectsInvalidProfiles(t *testing.T) {
	cases := map[string]func(*PooledLaunchSpec, *HostAllocation){
		"changed configuration": func(s *PooledLaunchSpec, a *HostAllocation) { s.ConfigurationSHA256 = strings.Repeat("b", 64) },
		"invalid digest": func(s *PooledLaunchSpec, a *HostAllocation) {
			s.ConfigurationSHA256 = "invalid"
			a.Binding.ConfigurationSHA256 = "invalid"
		},
		"unqualified runtime": func(s *PooledLaunchSpec, _ *HostAllocation) { s.JDK.Major = 26 },
		"legacy runtime":      func(s *PooledLaunchSpec, _ *HostAllocation) { s.JDK.Major = 8 },
		"unknown VM family":   func(s *PooledLaunchSpec, _ *HostAllocation) { s.JDK.VMFamily = "unknown" },
		"relative launcher":   func(s *PooledLaunchSpec, _ *HostAllocation) { s.JDK.JavaBin = "java" },
		"wrapper launcher":    func(s *PooledLaunchSpec, _ *HostAllocation) { s.JDK.JavaBin = filepath.Join(s.WorkDir, "wrapper") },
		"directory artifact":  func(s *PooledLaunchSpec, _ *HostAllocation) { s.ScriptClasspath = []string{s.WorkDir} },
		"missing artifact":    func(s *PooledLaunchSpec, _ *HostAllocation) { s.ShimJar = filepath.Join(s.WorkDir, "absent.jar") },
		"trimmed artifact identity": func(s *PooledLaunchSpec, _ *HostAllocation) {
			s.HarnessJar += " "
			if err := os.WriteFile(s.HarnessJar, []byte("not the trimmed artifact"), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"missing script":        func(s *PooledLaunchSpec, _ *HostAllocation) { s.ScriptClasspath = nil },
		"invalid target":        func(s *PooledLaunchSpec, _ *HostAllocation) { s.TargetClass = "-javaagent:bad" },
		"invalid workdir":       func(s *PooledLaunchSpec, _ *HostAllocation) { s.WorkDir = s.HarnessJar },
		"no heap profile":       func(s *PooledLaunchSpec, _ *HostAllocation) { s.VMParameters = nil },
		"conflicting heap":      func(s *PooledLaunchSpec, _ *HostAllocation) { s.VMParameters = []string{"-Xmx128m", "-Xmx256m"} },
		"no native budget":      func(s *PooledLaunchSpec, a *HostAllocation) { a.MemoryBytes = 128 << 20 },
		"invalid memory budget": func(s *PooledLaunchSpec, a *HostAllocation) { a.MemoryBytes = -1 },
		"injected agent": func(s *PooledLaunchSpec, _ *HostAllocation) {
			s.VMParameters = append(s.VMParameters, "-javaagent:arbitrary.jar")
		},
		"script archive": func(s *PooledLaunchSpec, _ *HostAllocation) {
			s.VMParameters = append(s.VMParameters, "-XX:SharedArchiveFile=arbitrary.jsa")
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, a := launchFixture(t)
			change(&s, &a)
			if cmd, err := buildPooledCommand(s, a, 1234, pooledRuntimeSupported); err == nil || cmd != nil {
				t.Fatal("invalid profile admitted", err)
			}
		})
	}
	for _, major := range []int{17, 21} {
		t.Run(strconv.Itoa(major), func(t *testing.T) {
			s, a := launchFixture(t)
			s.JDK.Major = major
			if _, err := buildPooledCommand(s, a, 1234, pooledRuntimeSupported); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPooledHeapRequiresUnambiguousBound(t *testing.T) {
	for _, args := range [][]string{nil, {"-Xmx"}, {"-Xmx0"}, {"-Xmx-1"}, {"-Xmx18446744073709551615g"}, {"-Xmx1g", "-XX:MaxHeapSize=1024"}, {"-Xmx1g", "-XX:MaxRAMPercentage=50"}, {"-Xmx1g", "-Xsoftmx512m"}, {"-Xmx1g", "-Xmx2g"}} {
		if _, ok := pooledMaxHeap(args); ok {
			t.Fatalf("accepted %v", args)
		}
	}
	for arg, want := range map[string]uint64{"-Xmx128m": 128 << 20, "-Xmx1G": 1 << 30, "-Xmx1024k": 1 << 20, "-Xmx67108864": 64 << 20} {
		if got, ok := pooledMaxHeap([]string{arg}); !ok || got != want {
			t.Fatal(arg, got, ok)
		}
	}
	s, a := launchFixture(t)
	a.MemoryBytes = 0
	if _, err := buildPooledCommand(s, a, 1234, pooledRuntimeSupported); !errors.Is(err, ErrHostCapacity) {
		t.Fatal(err)
	}
}

func TestValidPooledTargetClass(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"Actions", true}, {"example.Actions$Nested", true}, {"", false}, {"-javaagent:bad", false}, {" example.Actions", false}, {strings.Repeat("A", 4097), false},
	} {
		if got := ValidPooledTargetClass(tc.name); got != tc.valid {
			t.Errorf("target validity %v, want %v", got, tc.valid)
		}
	}
}
