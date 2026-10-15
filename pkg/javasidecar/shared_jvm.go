package javasidecar

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

const (
	// FeatureSharedJVMV1 means the runner qualifies and stages shared JVMs for
	// claim_shared requests. It implies preparation, commit, invocation and
	// diagnostics for those runs; profile-based pilot features are separate.
	FeatureSharedJVMV1 = "java_vuser_shared_jvm_v1"
	// WarningSharedJVMHeapIgnored reports that script heap sizing, written for
	// one JVM per VUser, was replaced by the operator's shared-JVM heap.
	WarningSharedJVMHeapIgnored = "shared_jvm_script_heap_ignored"
	// SharedJVMDisabled is the stable error for claims on a runner without the
	// operator policy.
	SharedJVMDisabled = "shared_jvm_disabled"

	sharedJVMProfileTargetPrivate   = "shared-auto-target-private"
	sharedJVMProfileDedicatedLoader = "shared-auto-dedicated-loader"
)

var ErrSharedJVMConfig = errors.New("shared_jvm_configuration_invalid")

const (
	sharedJVMRuntimeRejected     = "shared_jvm_runtime_rejected"
	sharedJVMVMParameterRejected = "shared_jvm_vm_parameter_invalid"
	sharedJVMQualificationFailed = "shared_jvm_qualification_failed"
)

// sharedJVMRuntimeCodes are dedicated-mode runtime failures a shared claim
// reports unchanged; any other preparation failure collapses to one code so
// the claim error vocabulary stays closed.
var sharedJVMRuntimeCodes = map[string]bool{
	"ambient_credentials_detected": true, "dedicated_classpath_required": true, "execution_canceled": true,
	"execution_timeout": true, "harness_unavailable": true, "jdk_home_invalid": true, "jdk_home_required": true,
	"jdk_selection_invalid": true, "jdk_unavailable": true, "legacy_runner_wrapper_unsupported": true,
	"process_containment_unavailable": true, "shared_classpath_unavailable": true,
	"startup_timeout_out_of_range": true, "work_dir_unavailable": true,
}

// sharedJVMClaimErrorCodes is the complete claim_shared error vocabulary that
// is specific to shared JVMs; clients reject anything outside it.
var sharedJVMClaimErrorCodes = map[string]bool{
	SharedJVMDisabled: true, sharedJVMRuntimeRejected: true, sharedJVMVMParameterRejected: true, sharedJVMQualificationFailed: true,
	javarunner.SharedJVMReasonRuntime: true, javarunner.SharedJVMReasonClasspath: true, javarunner.SharedJVMReasonNative: true,
	javarunner.SharedJVMReasonIncomplete: true, javarunner.SharedJVMReasonInputs: true, javarunner.SharedJVMReasonCapacity: true,
}

// SharedJVMClaimErrorCode reports whether code is a claim_shared refusal that
// leaves the workload eligible for dedicated JVMs.
func SharedJVMClaimErrorCode(code string) bool {
	return sharedJVMClaimErrorCodes[code] || sharedJVMRuntimeCodes[code]
}

func sharedJVMRuntimeReason(code string) string {
	switch {
	case sharedJVMRuntimeCodes[code]:
		return code
	case strings.HasPrefix(code, "vm_parameter_"):
		return sharedJVMVMParameterRejected
	default:
		return sharedJVMRuntimeRejected
	}
}

// SharedJVMClaim is the script-derived input of a shared run. Runtime carries
// exactly what a dedicated session would send and is validated identically;
// the runner, not the caller, decides heap, slots, jars and qualification.
type SharedJVMClaim struct {
	Runtime      RuntimeSettings `json:"runtime"`
	TargetClass  string          `json:"target_class"`
	ScriptSHA256 string          `json:"script_sha256"`
	// VUsersPerJVM may lower the operator's slots per JVM; zero keeps it.
	VUsersPerJVM int `json:"vusers_per_jvm,omitempty"`
}

// SharedJVMCapabilities is the runner's shared-JVM admission envelope. Usage
// fields are a point-in-time snapshot of the shared admission budget, which
// dedicated JVMs on the same runner also charge.
type SharedJVMCapabilities struct {
	VUsersPerJVM    int   `json:"vusers_per_jvm"`
	JVMMemoryBytes  int64 `json:"jvm_memory_bytes"`
	MaxJVMs         int   `json:"max_jvms"`
	MaxVUsers       int   `json:"max_vusers"`
	MaxMemoryBytes  int64 `json:"max_memory_bytes"`
	AvailableVUsers int   `json:"available_vusers"`
}

// SharedJVMPolicy is the operator's shared-JVM configuration on one runner.
type SharedJVMPolicy struct {
	vusersPerJVM            int
	heapBytes               int64
	hostMemoryBytes         int64
	slotAPIJar, slotShimJar string
}

// NewSharedJVMPolicy validates the operator policy and builds the one shared
// admission authority that dedicated sessions and shared runs both charge.
// The caller must also require Linux; qualification binds delegated_linux.
func NewSharedJVMPolicy(operator config.JavaVuserOperatorConfig) (*SharedJVMPolicy, *javarunner.DedicatedHostAdmission, error) {
	shared := operator.SharedJVM
	if !shared.Enabled || !operator.Enabled || operator.AuthToken == "" || operator.MaxProcesses < 1 {
		return nil, nil, ErrSharedJVMConfig
	}
	for _, jar := range []string{shared.SlotAPIJar, shared.SlotShimJar} {
		info, err := os.Stat(jar)
		if !filepath.IsAbs(jar) || err != nil || !info.Mode().IsRegular() {
			return nil, nil, ErrSharedJVMConfig
		}
	}
	heap := int64(shared.HeapMB) << 20
	profile, ok := javarunner.SharedJVMResourceProfile(shared.VUsersPerJVM, heap)
	if !ok {
		return nil, nil, ErrSharedJVMConfig
	}
	hostMemory, _ := javarunner.SharedJVMHostMemoryBytes(profile)
	dedicated := int64(shared.DedicatedJVMMemoryMB) << 20
	budget := javarunner.HostBudget{MaxHosts: operator.MaxProcesses, MaxVUsers: min(100000, operator.MaxProcesses*shared.VUsersPerJVM), MaxMemoryBytes: int64(shared.MemoryBudgetMB) << 20}
	if budget.MaxMemoryBytes == 0 {
		budget.MaxMemoryBytes = int64(operator.MaxProcesses) * max(hostMemory, dedicated)
	}
	if budget.MaxMemoryBytes < hostMemory || budget.MaxMemoryBytes < dedicated {
		return nil, nil, ErrSharedJVMConfig
	}
	capacity, err := javarunner.NewHostCapacity(budget)
	if err != nil {
		return nil, nil, ErrSharedJVMConfig
	}
	admission, err := javarunner.NewDedicatedHostAdmission(capacity, dedicated)
	if err != nil {
		return nil, nil, ErrSharedJVMConfig
	}
	return &SharedJVMPolicy{vusersPerJVM: shared.VUsersPerJVM, heapBytes: heap, hostMemoryBytes: hostMemory, slotAPIJar: shared.SlotAPIJar, slotShimJar: shared.SlotShimJar}, admission, nil
}

func validSharedJVMClaim(c *SharedJVMClaim) bool {
	return c != nil && javarunner.ValidPooledTargetClass(c.TargetClass) && validPooledScriptSHA256(c.ScriptSHA256) && (c.VUsersPerJVM == 0 || c.VUsersPerJVM >= 2 && c.VUsersPerJVM <= javarunner.MaxPooledSlotsPerHost)
}

// sharedJVMCapabilities reports the envelope, or nil when shared JVMs are off.
func (s *Server) sharedJVMCapabilities() *SharedJVMCapabilities {
	if s.sharedJVM == nil || s.hostAdmission == nil {
		return nil
	}
	p := s.sharedJVM
	budget := s.hostAdmission.Budget()
	usage := s.hostAdmission.Snapshot()
	jvms := min(budget.MaxHosts, cap(s.slots), maxPooledControlHosts)
	vusers := min(budget.MaxVUsers, jvms*p.vusersPerJVM, int(min(budget.MaxMemoryBytes/p.hostMemoryBytes, int64(jvms)))*p.vusersPerJVM)
	freeJVMs := min(jvms-usage.Hosts, int((budget.MaxMemoryBytes-usage.MemoryBytes)/p.hostMemoryBytes))
	available := max(0, min(vusers-usage.VUsers, freeJVMs*p.vusersPerJVM))
	s.captureMu.Lock()
	if s.captureDescriptorLimit != 0 {
		descriptors := max(0, s.captureDescriptorLimit)
		vusers = min(vusers, descriptors)
		available = min(available, max(0, descriptors-s.captureDescriptors))
	}
	s.captureMu.Unlock()
	return &SharedJVMCapabilities{VUsersPerJVM: p.vusersPerJVM, JVMMemoryBytes: p.hostMemoryBytes, MaxJVMs: jvms, MaxVUsers: vusers, MaxMemoryBytes: budget.MaxMemoryBytes, AvailableVUsers: available}
}

// sharedJVMSelection resolves the claim exactly as a dedicated session would,
// then qualifies it for one shared JVM per group of slots. A non-empty reason
// is a stable, value-free code; the caller keeps the workload dedicated.
func (s *Server) sharedJVMSelection(ctx context.Context, claim SharedJVMClaim) (javarunner.PooledSelection, []string, string, error) {
	p := s.sharedJVM
	prepared, failure := s.prepareRuntime(ctx, claim.Runtime)
	if err := ctx.Err(); err != nil {
		return javarunner.PooledSelection{}, nil, "", err
	}
	if failure != nil {
		return javarunner.PooledSelection{}, nil, sharedJVMRuntimeReason(failure.code), nil
	}
	canonical := func(path string) (string, bool) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(resolved) {
			return "", false
		}
		return filepath.Clean(resolved), true
	}
	canonicalAll := func(paths []string) ([]string, bool) {
		out := make([]string, 0, len(paths))
		for _, path := range paths {
			resolved, ok := canonical(path)
			if !ok {
				return nil, false
			}
			out = append(out, resolved)
		}
		return out, true
	}
	script, okScript := canonicalAll(prepared.scriptClasspath)
	operatorShared, okShared := canonicalAll(prepared.sharedClasspath)
	jars, okJars := canonicalAll([]string{p.slotAPIJar, p.slotShimJar})
	workDir, okWork := canonical(prepared.workDir)
	jdk := prepared.jdk
	home, okHome := canonical(jdk.Home)
	javaBin, okBin := canonical(jdk.JavaBin)
	if !okScript || !okShared || !okJars || !okWork || !okHome || !okBin {
		return javarunner.PooledSelection{}, nil, javarunner.SharedJVMReasonInputs, nil
	}
	jdk.Home, jdk.JavaBin = home, javaBin
	private, scriptShared, reason := javarunner.SplitSharedClasspath(script, claim.TargetClass, prepared.dedicatedLoader)
	if reason != "" {
		return javarunner.PooledSelection{}, nil, reason, nil
	}
	application := append(append([]string(nil), private...), scriptShared...)
	native, err := javarunner.InspectApplicationNative(ctx, application)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return javarunner.PooledSelection{}, nil, "", ctxErr
	}
	if err != nil {
		native = javarunner.NativeInspection{Assessment: "incomplete"}
	}
	slots := p.vusersPerJVM
	if claim.VUsersPerJVM > 0 {
		slots = min(slots, claim.VUsersPerJVM)
	}
	resources, ok := javarunner.SharedJVMResourceProfile(slots, p.heapBytes)
	if !ok {
		return javarunner.PooledSelection{}, nil, javarunner.SharedJVMReasonCapacity, nil
	}
	args, droppedHeap := javarunner.SharedJVMArguments(prepared.vmParameters, p.heapBytes)
	warnings := append([]string(nil), prepared.warningCodes...)
	if droppedHeap {
		warnings = append(warnings, WarningSharedJVMHeapIgnored)
	}
	sharing := sharedJVMProfileTargetPrivate
	if prepared.dedicatedLoader {
		sharing = sharedJVMProfileDedicatedLoader
	}
	request := javarunner.PooledQualificationRequest{
		Launch: javarunner.PooledLaunchSpec{
			ContainmentBackend: "delegated_linux",
			JDK:                jdk,
			HarnessJar:         jars[0],
			ShimJar:            jars[1],
			TargetClass:        claim.TargetClass,
			WorkDir:            workDir,
			ScriptClasspath:    private,
			SharedClasspath:    append(scriptShared, operatorShared...),
			VMParameters:       args,
			QualifiedCleanup:   true,
		},
		SourceArchive:  private[0],
		SharingProfile: sharing,
		Resources:      resources,
	}
	selection, reason, err := javarunner.QualifySharedAutomatically(ctx, request, native)
	return selection, warnings, reason, err
}

// claimSharedControl opens one runner-qualified shared run. Qualification
// (JDK probe, whole-JDK hashing, native scan) runs before the claim lock and
// owner operation, so concurrent groups qualify in parallel and an owner
// takeover never waits on it. The run itself is opened under the same
// serialization, concurrent-run bound and owner fence as a profile claim.
func (s *Server) claimSharedControl(w http.ResponseWriter, r *http.Request, q PooledControlRequest) {
	if s.sharedJVM == nil {
		writeError(w, http.StatusPreconditionFailed, SharedJVMDisabled)
		return
	}
	// Refuse a fenced owner before spending qualification work on it.
	precheck, code := s.beginOwnerOperation(q.OwnerID, q.OwnerEpoch)
	if code != "" {
		writePooledControlError(w, javarunner.ErrHostFence)
		return
	}
	precheck.operations.Done()
	selection, warnings, reason, err := s.sharedJVMSelection(r.Context(), *q.Shared)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, sharedJVMQualificationFailed)
		return
	}
	if reason != "" {
		writeError(w, http.StatusConflict, reason)
		return
	}
	s.pooledControlMu.Lock()
	defer s.pooledControlMu.Unlock()
	owner, code := s.beginOwnerOperation(q.OwnerID, q.OwnerEpoch)
	if code != "" {
		writePooledControlError(w, javarunner.ErrHostFence)
		return
	}
	defer owner.operations.Done()
	if !s.pooledRunAdmissionOpen(owner) {
		writeError(w, http.StatusConflict, "pooled_group_limit")
		return
	}
	d := selection.Decision()
	budget := s.hostAdmission.Budget()
	budget.MaxHosts = min(budget.MaxHosts, cap(s.slots), maxPooledControlHosts)
	budget.MaxVUsers = min(budget.MaxVUsers, maxPooledControlVUsers)
	if budget.MaxVUsers < d.SlotsPerHost || budget.MaxMemoryBytes < d.HostMemoryBytes {
		writePooledControlError(w, javarunner.ErrHostCapacity)
		return
	}
	run, err := s.OpenPooledRun(r.Context(), q.OwnerID, q.OwnerEpoch, q.ExecutionID, q.GroupID, budget, selection)
	if err != nil {
		writePooledControlError(w, err)
		return
	}
	decision := PooledControlDecision{ScriptSHA256: q.Shared.ScriptSHA256, TargetClass: d.TargetClass, PreparationEnabled: true, CommitEnabled: true, InvocationEnabled: true, Mode: d.Mode, ProfileID: javarunner.SharedJVMProfileID, ResourceProfile: d.ResourceProfile, ConfigurationSHA256: d.ConfigurationSHA256, ContainmentBackend: d.ContainmentBackend, SlotsPerHost: d.SlotsPerHost, HostMemoryBytes: d.HostMemoryBytes, MaxHosts: budget.MaxHosts, MaxVUsers: budget.MaxVUsers, MaxMemoryBytes: budget.MaxMemoryBytes}
	s.publishPooledControlRun(w, q, run, javarunner.SharedJVMProfileID, decision, warnings)
}
