package javarunner

import (
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
)

// PooledLaunchProvider is the operator-owned qualification boundary. It must
// revalidate the immutable artifact/runtime/resource identities represented by
// ConfigurationSHA256 before returning. This is not a script or wire callback.
// PooledSelection.LaunchProvider supplies identity verification for pilot profiles;
// sidecar admission must still enforce its ownership, backend, and quota policy.
// Constructing a backend does not itself qualify workloads or enable pooling.
type PooledLaunchProvider func(context.Context, HostAllocation) (PooledLaunchSpec, error)

type PooledLaunchSpec struct {
	// Empty is reserved for internal component providers; qualified selections
	// name the backend and LocalHostBackend checks it before starting a process.
	ContainmentBackend                             string
	ConfigurationSHA256                            string
	JDK                                            ResolvedJDK
	HarnessJar, ShimJar, TargetClass, WorkDir      string
	ScriptClasspath, SharedClasspath, VMParameters []string
	QualifiedCleanup                               bool
}

var pooledTargetClass = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_.$]*$`)

func pooledRuntimeSupported(jdk ResolvedJDK) bool {
	return (jdk.Major == 17 || jdk.Major == 21) && (jdk.VMFamily == JavaVMHotSpot || jdk.VMFamily == JavaVMOpenJ9)
}
func buildPooledCommand(spec PooledLaunchSpec, a HostAllocation, port int, runtimeSupported func(ResolvedJDK) bool) (*exec.Cmd, error) {
	invalid := func() (*exec.Cmd, error) { return nil, ErrHostIdentity }
	digest, err := hex.DecodeString(spec.ConfigurationSHA256)
	if err != nil || len(digest) != 32 || spec.ConfigurationSHA256 != a.Binding.ConfigurationSHA256 || !pooledID(a.ID, false) || len(a.SlotIDs) < 1 || len(a.SlotIDs) > MaxPooledSlotsPerHost || port < 1 || port > 65535 {
		return invalid()
	}
	if !runtimeSupported(spec.JDK) || !filepath.IsAbs(spec.JDK.JavaBin) || !filepath.IsAbs(spec.JDK.Home) || spec.JDK.Version == "" || !filepath.IsAbs(spec.WorkDir) || !ValidPooledTargetClass(spec.TargetClass) {
		return invalid()
	}
	if base := strings.ToLower(filepath.Base(spec.JDK.JavaBin)); base != "java" && base != "java.exe" {
		return invalid()
	}
	work, err := os.Stat(spec.WorkDir)
	if err != nil || !work.IsDir() {
		return invalid()
	}
	if len(spec.ScriptClasspath) < 1 || len(spec.ScriptClasspath) > 256 || len(spec.SharedClasspath) > 256 {
		return invalid()
	}
	artifacts := append([]string{spec.HarnessJar, spec.ShimJar}, spec.ScriptClasspath...)
	artifacts = append(artifacts, spec.SharedClasspath...)
	if len(ValidateClasspath(artifacts)) != 0 {
		return invalid()
	}
	for _, path := range artifacts {
		if !filepath.IsAbs(path) || path != strings.TrimSpace(path) {
			return invalid()
		}
		stat, e := os.Stat(path)
		if e != nil || !stat.Mode().IsRegular() {
			return invalid()
		}
	}
	heap, ok := pooledMaxHeap(spec.VMParameters)
	if !ok || heap >= uint64(a.MemoryBytes) || a.MemoryBytes <= 0 {
		return nil, ErrHostCapacity
	}
	args := []string{a.ID, strconv.Itoa(len(a.SlotIDs)), spec.ShimJar, spec.TargetClass, strconv.FormatBool(spec.QualifiedCleanup), strconv.Itoa(port), strconv.Itoa(len(spec.ScriptClasspath))}
	args = append(args, spec.ScriptClasspath...)
	args = append(args, strconv.Itoa(len(spec.SharedClasspath)))
	args = append(args, spec.SharedClasspath...)
	plan, err := BuildCommand(CommandSpec{JavaMajor: spec.JDK.Major, VMFamily: spec.JDK.VMFamily, VMParameters: spec.VMParameters, HarnessJar: spec.HarnessJar, ScriptClasspath: spec.ScriptClasspath, SharedClasspath: spec.SharedClasspath, DedicatedClassLoader: true, HarnessMain: "apexload.host.PooledMain", HarnessArgs: args})
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(spec.JDK.JavaBin, plan.Args...)
	cmd.Dir = spec.WorkDir
	cmd.Env = subprocessguard.Environment(spec.JDK.EnvironmentOverrides(os.Getenv("PATH")))
	return cmd, nil
}

// Require an explicit heap ceiling and reject alternate/conflicting ceilings;
// never silently override script VM arguments or choose a global tuning profile.
func pooledMaxHeap(arguments []string) (uint64, bool) {
	var heap uint64
	count := 0
	for _, arg := range arguments {
		if strings.HasPrefix(arg, "-XX:MaxHeapSize=") || strings.HasPrefix(arg, "-XX:MaxRAM") || strings.HasPrefix(arg, "-Xsoftmx") {
			return 0, false
		}
		if !strings.HasPrefix(arg, "-Xmx") {
			continue
		}
		count++
		raw := strings.TrimPrefix(arg, "-Xmx")
		if raw == "" {
			return 0, false
		}
		multiplier := uint64(1)
		switch raw[len(raw)-1] {
		case 'k', 'K':
			multiplier = 1 << 10
			raw = raw[:len(raw)-1]
		case 'm', 'M':
			multiplier = 1 << 20
			raw = raw[:len(raw)-1]
		case 'g', 'G':
			multiplier = 1 << 30
			raw = raw[:len(raw)-1]
		}
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || n == 0 || n > ^uint64(0)/multiplier {
			return 0, false
		}
		heap = n * multiplier
	}
	return heap, count == 1
}

// ValidPooledTargetClass validates the bounded target identity used by the pilot.
func ValidPooledTargetClass(name string) bool {
	return len(name) <= 4096 && pooledTargetClass.MatchString(name)
}
