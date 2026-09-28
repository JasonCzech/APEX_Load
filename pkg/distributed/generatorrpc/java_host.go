package generatorrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

const FeatureJavaHostPreparedPopulationV1 = "java_host_prepared_population_v1"

const FeatureJavaHostBrokerV1 = "java_host_broker_v1"
const JavaHostSelectionContract = "apexload.java-placement/v1"

// JavaHostApproval is operator-owned and binds the exact immutable source to a
// broker profile. The controller can select only an existing approval; scripts
// cannot supply profiles, endpoints, credentials or targets.
type JavaHostApproval struct {
	ScriptSHA256    string
	TargetClass     string
	Profile         broker.BrokerProfileCapability
	FacadeResources JavaHostFacadeResources
}

// JavaHostFacadeResources is an optional operator budget per admitted Vuser.
// It covers generator/facade overhead only, excluding execution-pod resources.
// All-zero preserves legacy admission without a generator pod ledger.
type JavaHostFacadeResources struct {
	CPU         int64 `json:"cpu_millicores"`
	MemoryBytes int64 `json:"memory_bytes"`
	NetworkKbps int64 `json:"network_kbps"`
}

func (r JavaHostFacadeResources) Digest() (string, error) {
	if r == (JavaHostFacadeResources{}) {
		return "", nil
	}
	if r.CPU < 1 || r.CPU > 1000000 || r.MemoryBytes < 1 || r.MemoryBytes > 1<<40 || r.NetworkKbps < 1 || r.NetworkKbps > 1000000000 {
		return "", errors.New("java_host_facade_resources_invalid")
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("apexload.java-facade-resources/v1\x00%d\x00%d\x00%d", r.CPU, r.MemoryBytes, r.NetworkKbps)))
	return hex.EncodeToString(digest[:]), nil
}

func validJavaHostProfileResources(p *pb.JavaHostProfile) bool {
	digest, err := (JavaHostFacadeResources{CPU: p.FacadeCpuMillicores, MemoryBytes: p.FacadeMemoryBytes, NetworkKbps: p.FacadeNetworkKbps}).Digest()
	return err == nil && digest == p.FacadeResourcesSha256
}

// WithJavaHostRuntime enables explicit negotiated dedicated pod placement. The
// caller owns Runtime and must close it on generator shutdown. No default changes.
func WithJavaHostRuntime(runtime *javasidecar.BrokerRuntime, approvals []JavaHostApproval) (ServiceOption, error) {
	if runtime == nil || runtime.Err() != nil || len(approvals) < 1 || len(approvals) > 256 {
		return nil, errors.New("java_host_configuration_invalid")
	}
	caps := runtime.Capabilities()
	approved := map[string]JavaHostApproval{}
	for _, a := range approvals {
		found := false
		for _, p := range caps.Profiles {
			if a.Profile == p {
				found = true
			}
		}
		if _, err := a.FacadeResources.Digest(); err != nil {
			return nil, err
		}
		if !found || !javaHostDigest(a.ScriptSHA256) || !javarunner.ValidPooledTargetClass(a.TargetClass) {
			return nil, errors.New("java_host_approval_invalid")
		}
		if _, exists := approved[a.ScriptSHA256]; exists {
			return nil, errors.New("java_host_approval_duplicate")
		}
		approved[a.ScriptSHA256] = a
	}
	return func(s *Service) { s.javaHostRuntime = runtime; s.javaHostApprovals = approved }, nil
}
func javaHostDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}
func validateJavaHostSelection(q *pb.WorkAssignment) error {
	s := q.GetJavaHost()
	if s == nil {
		return nil
	}
	if s.FacadeResourcesSha256 != "" && !javaHostDigest(s.FacadeResourcesSha256) {
		return errors.New("java_host_selection_invalid")
	}
	if s.Contract != JavaHostSelectionContract || !javaHostDigest(s.ScriptSha256) || !javaHostDigest(s.ConfigurationSha256) || !javaHostDigest(s.PolicySha256) || len(s.ProfileId) < 1 || len(s.ProfileId) > 128 || s.PreparationMs < 1 || s.PreparationMs > 3600000 || s.RunLifetimeMs < 1 || s.RunLifetimeMs > 86400000 {
		return errors.New("java_host_selection_invalid")
	}
	if q.SourcePlan != nil || q.GetAdoptionGrace().AsDuration() != 0 || q.GetDurationOffset().AsDuration() != 0 || (q.VuserCount < 0 || javaHostPreparedVUsers(q) < 1 || s.PreparedVusers < 0 || s.PreparedVusers > 10000 || s.PreparedVusers > 0 && (s.PreparedVusers < q.VuserCount || q.TerminationMode == "iterations")) {
		return errors.New("java_host_assignment_shape_unsupported")
	}
	return nil
}
func (s *Service) javaHostGroupOptions(q *pb.WorkAssignment, source, effective *dsl.Script) (*engine.JavaBrokerGroupOptions, error) {
	selection := q.GetJavaHost()
	if selection == nil {
		return nil, nil
	}
	if s.javaHostRuntime == nil || s.javaHostRuntime.Err() != nil || !s.executionEnabled {
		return nil, errors.New("java_host_backend_unavailable")
	}
	if err := validateJavaHostSelection(q); err != nil {
		return nil, err
	}
	digest, err := engine.JavaPooledScriptSHA256(source)
	if err != nil || digest != selection.ScriptSha256 {
		return nil, errors.New("java_host_script_identity_mismatch")
	}
	approval, ok := s.javaHostApprovals[digest]
	p := approval.Profile
	resourcesDigest, _ := approval.FacadeResources.Digest()
	if selection.FacadeResourcesSha256 != resourcesDigest {
		return nil, errors.New("java_host_facade_resources_changed")
	}
	if !ok || p.ID != selection.ProfileId || p.PolicySHA256 != selection.PolicySha256 || p.ConfigurationSHA256 != selection.ConfigurationSha256 || selection.PreparationMs > p.PreparationMS || selection.RunLifetimeMs > p.MaxRunMS {
		return nil, errors.New("java_host_profile_not_approved")
	}
	if time.Duration(selection.PreparationMs)*time.Millisecond > s.lifecycleCoordinator.stageTTLValue() {
		return nil, errors.New("java_host_preparation_exceeds_staging_deadline")
	}
	effectiveDigest, err := engine.JavaPooledScriptSHA256(effective)
	if err != nil {
		return nil, err
	}
	// Opaque placement names include the execution generation and run incarnation;
	// human group/scenario labels remain separately bound in each host context.
	execution := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%x", q.TestId, q.ExecutionGeneration, q.RunSupervisionToken)))
	group := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", q.GroupName, q.ShardOrdinal)))
	return &engine.JavaBrokerGroupOptions{PreparedVUsers: int(selection.PreparedVusers), Runtime: s.javaHostRuntime, Profile: p, ScriptSHA256: effectiveDigest, TargetClass: approval.TargetClass, ExecutionID: hex.EncodeToString(execution[:]), GroupID: hex.EncodeToString(group[:]), PreparationTimeout: time.Duration(selection.PreparationMs) * time.Millisecond, RunLifetime: time.Duration(selection.RunLifetimeMs) * time.Millisecond}, nil
}
func (s *Service) javaHostCapabilities() *pb.JavaHostCapabilities {
	if s.javaHostRuntime == nil || s.javaHostRuntime.Err() != nil || !s.executionEnabled {
		return nil
	}
	caps := s.javaHostRuntime.Capabilities()
	out := &pb.JavaHostCapabilities{Contract: JavaHostSelectionContract, Mode: caps.Mode, Containment: caps.Containment, MaxHosts: int32(caps.MaxHosts), MaxVusers: int32(caps.MaxVUsers), MaxMemoryBytes: caps.MaxMemoryBytes}
	keys := make([]string, 0, len(s.javaHostApprovals))
	for k := range s.javaHostApprovals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := s.javaHostApprovals[k]
		p := a.Profile
		digest, _ := a.FacadeResources.Digest()
		out.Profiles = append(out.Profiles, &pb.JavaHostProfile{ScriptSha256: k, ProfileId: p.ID, ConfigurationSha256: p.ConfigurationSHA256, PolicySha256: p.PolicySHA256, MemoryBytes: p.MemoryBytes, PreparationMs: min(p.PreparationMS, s.lifecycleCoordinator.stageTTLValue().Milliseconds()), MaxRunMs: p.MaxRunMS, InvocationMs: p.InvocationMS, FacadeCpuMillicores: a.FacadeResources.CPU, FacadeMemoryBytes: a.FacadeResources.MemoryBytes, FacadeNetworkKbps: a.FacadeResources.NetworkKbps, FacadeResourcesSha256: digest})
	}
	return out
}

// javaHostPreparedVUsers is the lifetime admission count, including cold spare
// sessions. Ordinary dedicated assignments continue to reserve their population.
func javaHostPreparedVUsers(q *pb.WorkAssignment) int32 {
	if q.GetJavaHost().GetPreparedVusers() > 0 {
		return q.JavaHost.PreparedVusers
	}
	return q.GetVuserCount()
}

func javaHostPreparedSpare(a assignedTest) int64 {
	var spare int64
	for name, prepared := range a.groupJavaHostPrepared {
		spare += max(0, int64(prepared)-int64(a.groups[name]))
	}
	return spare
}
func reservedAssignmentVUsers(a assignedTest) int32 {
	return int32(min(int64(maxGeneratorVUsers), int64(a.vuserCount)+javaHostPreparedSpare(a)))
}
func (s *Service) capacityVUsers64Locked() int64 {
	total := s.activeVUsers64Locked()
	for _, a := range s.assignments {
		total += javaHostPreparedSpare(a)
	}
	return total
}
