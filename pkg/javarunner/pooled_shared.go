package javarunner

import (
	"archive/zip"
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/zipbounds"
)

// SharedJVMMode is the decision mode of a runner-qualified shared JVM. Unlike
// "pooled_pilot" it needs no hand-signed operator manifest: the operator turns
// the policy on, and the runner qualifies each workload itself.
const SharedJVMMode = "shared"

// SharedJVMProfileID names the automatic qualification profile in decisions.
const SharedJVMProfileID = "shared-auto"

// Stable reasons a workload stays on dedicated JVMs. They are value-free and
// safe to report in run manifests and controller errors.
const (
	SharedJVMReasonRuntime    = "shared_jvm_runtime_unsupported"
	SharedJVMReasonClasspath  = "shared_jvm_classpath_unsupported"
	SharedJVMReasonNative     = "shared_jvm_native_code_detected"
	SharedJVMReasonIncomplete = "shared_jvm_inspection_incomplete"
	SharedJVMReasonInputs     = "shared_jvm_inputs_unsupported"
	SharedJVMReasonCapacity   = "shared_jvm_capacity_invalid"
)

// SharedJVMResourceProfile derives a host's admission reservation from the
// operator's heap and slot count. The non-heap terms are conservative
// accounting for metaspace (one private loader per slot), thread stacks, JIT
// code cache, native buffers and event capture; none is an OS memory limit.
func SharedJVMResourceProfile(slots int, heapBytes int64) (PooledResourceProfile, bool) {
	if slots < 2 || slots > MaxPooledSlotsPerHost || heapBytes < 64<<20 || heapBytes > 1<<40 {
		return PooledResourceProfile{}, false
	}
	n := int64(slots)
	p := PooledResourceProfile{
		ID:             "shared-auto-v1",
		SlotsPerHost:   slots,
		HeapBytes:      heapBytes,
		NativeBytes:    64 << 20,
		StackBytes:     n * (256 << 10),
		MetaspaceBytes: 128<<20 + n*(256<<10),
		CodeCacheBytes: 64 << 20,
		CaptureBytes:   max(576<<10, n*(32<<10)),
		OtherBytes:     32 << 20,
	}
	if _, ok := p.memoryBytes(); !ok {
		return PooledResourceProfile{}, false
	}
	return p, true
}

// SharedJVMHostMemoryBytes is the per-JVM reservation for a resource profile.
func SharedJVMHostMemoryBytes(p PooledResourceProfile) (int64, bool) { return p.memoryBytes() }

// heapSizingPrefixes are per-process heap ceilings and initial sizes. A shared
// JVM's heap belongs to the operator profile, so script values written for one
// JVM per VUser are dropped (and reported) rather than applied to every VUser.
var heapSizingPrefixes = []string{"-Xmx", "-Xms", "-Xsoftmx", "-XX:MaxHeapSize=", "-XX:InitialHeapSize=", "-XX:MinHeapSize=", "-XX:MaxRAM", "-XX:InitialRAM", "-XX:MinRAM"}

// SharedJVMArguments replaces script heap sizing with the operator heap. It
// keeps every other validated script token in order.
func SharedJVMArguments(script []string, heapBytes int64) (args []string, droppedHeapSizing bool) {
	for _, arg := range script {
		heapFlag := false
		for _, prefix := range heapSizingPrefixes {
			if strings.HasPrefix(arg, prefix) {
				heapFlag = true
				break
			}
		}
		if heapFlag {
			droppedHeapSizing = true
			continue
		}
		args = append(args, arg)
	}
	return append(args, "-Xmx"+formatHeap(heapBytes)), droppedHeapSizing
}

func formatHeap(bytes int64) string {
	switch {
	case bytes%(1<<20) == 0:
		return strconv.FormatInt(bytes>>20, 10) + "m"
	case bytes%(1<<10) == 0:
		return strconv.FormatInt(bytes>>10, 10) + "k"
	default:
		return strconv.FormatInt(bytes, 10)
	}
}

// SplitSharedClasspath decides which script classpath jars each VUser loads
// privately. With dedicatedLoader every script entry is private (LoadRunner's
// "dedicated class loader" option); otherwise only the jars holding the target
// class are private, so its statics stay per VUser while libraries load once.
// Every entry must be a jar: the shared host loads private code by URL and its
// qualification hashes exact files, so directories stay on dedicated JVMs.
func SplitSharedClasspath(entries []string, target string, dedicatedLoader bool) (private, shared []string, reason string) {
	if len(entries) == 0 || !ValidPooledTargetClass(target) {
		return nil, nil, SharedJVMReasonClasspath
	}
	entry := strings.ReplaceAll(target, ".", "/") + ".class"
	for _, path := range entries {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, SharedJVMReasonClasspath
		}
		holds, ok := jarHoldsEntry(path, entry)
		if !ok {
			return nil, nil, SharedJVMReasonClasspath
		}
		if dedicatedLoader || holds {
			private = append(private, path)
		} else {
			shared = append(shared, path)
		}
	}
	if len(private) == 0 {
		return nil, nil, SharedJVMReasonClasspath
	}
	return private, shared, ""
}

func jarHoldsEntry(path, entry string) (holds, ok bool) {
	// Same archive bounds as qualification, checked before parsing headers.
	if zipbounds.ValidateFile(path, qualificationJarLimits) != nil {
		return false, false
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return false, false
	}
	defer archive.Close()
	for _, f := range archive.File {
		if f.Name == entry {
			return true, true
		}
	}
	return false, true
}

// QualifySharedAutomatically freezes a shared-JVM selection for a workload the
// runner has already resolved with dedicated-mode validation. It approves only
// a supported HotSpot/OpenJ9 17/21 runtime whose application artifacts passed a
// complete static native scan with no indicators; every other workload returns
// a stable reason and stays on dedicated JVMs. The scan does not prove absence
// of dynamic native loading or background work: that residual risk is the
// operator's when enabling the shared-JVM policy.
func QualifySharedAutomatically(ctx context.Context, request PooledQualificationRequest, native NativeInspection) (PooledSelection, string, error) {
	s := PooledSelection{decision: PooledQualificationDecision{Mode: "dedicated", SlotsPerHost: 1}}
	if err := ctx.Err(); err != nil {
		return s, "", err
	}
	if request.Resources.SlotsPerHost < 2 || request.Resources.SlotsPerHost > MaxPooledSlotsPerHost {
		return s, SharedJVMReasonCapacity, nil
	}
	if !pooledRuntimeSupported(request.Launch.JDK) {
		return s, SharedJVMReasonRuntime, nil
	}
	if !native.Complete || native.Assessment == "incomplete" {
		return s, SharedJVMReasonIncomplete, nil
	}
	if native.Assessment != "no_static_indicators" || len(native.Findings) != 0 {
		return s, SharedJVMReasonNative, nil
	}
	request = cloneQualificationRequest(request)
	digest, err := InspectPooledQualification(ctx, request)
	if ctx.Err() != nil {
		return s, "", ctx.Err()
	}
	if err != nil {
		return s, SharedJVMReasonInputs, nil
	}
	total, ok := request.Resources.memoryBytes()
	if !ok {
		return s, SharedJVMReasonCapacity, nil
	}
	request.Launch.ConfigurationSHA256 = digest
	s.request = request
	s.decision = PooledQualificationDecision{Mode: SharedJVMMode, TargetClass: request.Launch.TargetClass, QualificationProfile: SharedJVMProfileID, ConfigurationSHA256: digest, ResourceProfile: request.Resources.ID, SharingProfile: request.SharingProfile, ContainmentBackend: request.Launch.ContainmentBackend, SlotsPerHost: request.Resources.SlotsPerHost, HostMemoryBytes: total}
	return s, "", nil
}
