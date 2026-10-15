package generatorrpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
)

const sharedJVMScript = `{
	"name":"shared-java",
	"protocol":"java_vuser",
	"config":{"java_vuser":{"jvm_sharing":"shared"}},
	"init":[{"name":"init","type":"java_vuser_run","target":"Actions","params":{"method":"init"}}],
	"actions":[{"name":"action","type":"java_vuser_run","target":"Actions","params":{"method":"action"}}],
	"teardown":[{"name":"end","type":"java_vuser_run","target":"Actions","params":{"method":"end"}}]
}`

// sharedJVMRunner fakes a sidecar that hosts 4 dedicated JVMs and, when
// shared is set, a shared-JVM envelope; execution stays disabled in these tests.
func sharedJVMRunner(t *testing.T, shared *javasidecar.SharedJVMCapabilities) {
	t.Helper()
	sharedJVMRunnerRefusing(t, shared, "")
}

// sharedJVMRunnerRefusing also answers owner claims and refuses every
// claim_shared with refusal, as a runner would for native code.
func sharedJVMRunnerRefusing(t *testing.T, shared *javasidecar.SharedJVMCapabilities, refusal string) {
	t.Helper()
	features := []string{protocol.FeatureJavaVuserPersistentLifecycleV1, javasidecar.FeatureSessionFrameTokenV1, javasidecar.FeatureOwnerFencingV1, javasidecar.FeatureExternalRuntimeV1, javasidecar.FeatureRunnerOwnerLeaseV1, javasidecar.FeatureRuntimeLeaseV1, javasidecar.FeaturePhaseLifecycleV1}
	if shared != nil {
		features = append(features, javasidecar.FeatureSharedJVMV1)
	}
	runner := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/capabilities":
			_ = json.NewEncoder(response).Encode(javasidecar.CapabilitiesResponse{Contract: javasidecar.ContractVersion, Healthy: true, Features: features, MinimumJavaMajor: 17, JavaMajor: 21, MaxProcesses: 4, MaxExecutionTimeoutMS: javasidecar.MaximumExecutionTimeout.Milliseconds(), MaxCaptureBytes: javasidecar.MaxCaptureBytes, SharedJVM: shared})
		case request.Method == http.MethodGet && request.URL.Path == "/v1/health":
			_ = json.NewEncoder(response).Encode(javasidecar.HealthResponse{Contract: javasidecar.ContractVersion, Status: "healthy"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/validate":
			_ = json.NewEncoder(response).Encode(javasidecar.ValidateResponse{Contract: javasidecar.ContractVersion, Valid: true, JavaMajor: 21})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/owner/claim":
			var claim javasidecar.ClaimOwnerRequest
			_ = json.NewDecoder(request.Body).Decode(&claim)
			_ = json.NewEncoder(response).Encode(javasidecar.ClaimOwnerResponse{Contract: javasidecar.ContractVersion, OwnerID: claim.OwnerID, Epoch: "epoch", Claimed: true, LeaseTTLMS: javasidecar.RunnerOwnerLeaseTTL.Milliseconds()})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/pooled-control" && refusal != "":
			response.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(response).Encode(javasidecar.ErrorResponse{Contract: javasidecar.ContractVersion, ErrorCode: refusal})
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(runner.Close)
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, runner.URL)
	t.Setenv(config.EnvJavaVuserRunnerAuthToken, "shared-"+strings.ReplaceAll(t.Name(), "/", "-"))
	t.Setenv(config.EnvJavaVuserMaxProcesses, "4")
}

func assignJava(t *testing.T, service *Service, testID, scriptID, script string, vusers int32) *pb.AssignmentAck {
	t.Helper()
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: testID, GroupName: "default", ScriptId: scriptID, ScriptContent: []byte(script), VuserCount: vusers})
	if err != nil {
		t.Fatal(err)
	}
	return ack
}

func TestSharedJVMAssignmentsUseSharedEnvelope(t *testing.T) {
	sharedJVMRunner(t, &javasidecar.SharedJVMCapabilities{VUsersPerJVM: 100, MaxJVMs: 4, MaxVUsers: 400, AvailableVUsers: 400})
	service := newIsolatedService(t, 1000)
	// 300 Java VUsers would need 300 dedicated JVMs; the runner has 4.
	if ack := assignJava(t, service, "shared-a", "shared-script", sharedJVMScript, 300); !ack.GetAccepted() {
		t.Fatalf("shared assignment refused: %#v", ack)
	}
	service.mu.RLock()
	shared, staged := service.assignments["shared-a"].groupSharedJVM["default"], service.assignments["shared-a"].groupSharedJVMVUsers["default"]
	dedicated := service.activePersistentJavaVusersLocked()
	service.mu.RUnlock()
	if !shared || staged != 300 || dedicated != 0 {
		t.Fatalf("shared=%v staged=%d dedicated=%d", shared, staged, dedicated)
	}
	// The generator counts its own admissions even while the runner's figure
	// (cached between heartbeats) still says 400 are free.
	if ack := assignJava(t, service, "shared-b", "shared-script", sharedJVMScript, 200); ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), "insufficient java_vuser shared JVM capacity: 100 available") {
		t.Fatalf("over-admitted shared VUsers: %#v", ack)
	}
	// Dedicated Java work still sees all four dedicated JVMs.
	dedicatedScript := strings.Replace(sharedJVMScript, `"jvm_sharing":"shared"`, `"jvm_sharing":"dedicated"`, 1)
	if ack := assignJava(t, service, "dedicated-a", "dedicated-script", dedicatedScript, 4); !ack.GetAccepted() {
		t.Fatalf("dedicated work blocked by shared admissions: %#v", ack)
	}

	_, _, capacities := runtimeAdvertisementWithCapacities(context.Background(), true, map[string]int64{"java_vuser": 4, sharedJVMAdmittedKey: 300, sharedJVMPendingKey: 0}, nil)
	var java *pb.ProtocolRuntimeCapacity
	for _, capacity := range capacities {
		if capacity.GetProtocol() == "java_vuser" {
			java = capacity
		}
	}
	if java == nil || java.GetSharedJvm() == nil || java.GetSharedJvm().GetVusersPerJvm() != 100 || java.GetSharedJvm().GetMaxVusers() != 400 || java.GetSharedJvm().GetAvailableVusers() != 100 || java.GetMaxVusers() != 4 {
		t.Fatalf("heartbeat capacity %+v", java)
	}

	if commit, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "shared-a"}); err != nil || !commit.GetAccepted() {
		t.Fatalf("commit %+v %v", commit, err)
	}
	ack, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: "shared-a", GroupName: "default", TargetVusers: 301})
	if err == nil && ack.GetAccepted() {
		t.Fatal("shared group grew past its staged slots")
	}
	if err != nil && !strings.Contains(err.Error(), "java_shared_jvm_additional_slots_required") {
		t.Fatalf("scale-up error %v", err)
	}
}

func TestSharedJVMScriptOnRunnerWithoutPolicyUsesDedicatedCapacity(t *testing.T) {
	sharedJVMRunner(t, nil)
	service := newIsolatedService(t, 1000)
	if ack := assignJava(t, service, "no-policy", "shared-script", sharedJVMScript, 300); ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), "insufficient java_vuser runtime capacity") {
		t.Fatalf("shared script without runner policy: %#v", ack)
	}
	if ack := assignJava(t, service, "no-policy-small", "shared-script", sharedJVMScript, 3); !ack.GetAccepted() {
		t.Fatalf("shared script within dedicated capacity refused: %#v", ack)
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.assignments["no-policy-small"].groupSharedJVM["default"] || service.activePersistentJavaVusersLocked() != 3 {
		t.Fatal("dedicated fallback was not charged to dedicated capacity")
	}
}

func TestSharedJVMAvailableTakesLowerBound(t *testing.T) {
	capacity := &protocol.SharedJVMCapacity{VUsersPerJVM: 100, MaxVUsers: 400, AvailableVUsers: 250}
	for _, tc := range []struct{ admitted, pending, want int64 }{{0, 0, 250}, {100, 0, 250}, {300, 0, 100}, {200, 100, 150}, {500, 0, 0}, {0, 300, 0}} {
		if got := sharedJVMAvailable(capacity, tc.admitted, tc.pending); got != tc.want {
			t.Fatalf("admitted=%d pending=%d: %d, want %d", tc.admitted, tc.pending, got, tc.want)
		}
	}
	if sharedJVMAvailable(nil, 0, 0) != 0 {
		t.Fatal("missing envelope reported capacity")
	}
}

func TestSharedJVMRefusalAtStagingIsRecordedAsDedicated(t *testing.T) {
	sharedJVMRunnerRefusing(t, &javasidecar.SharedJVMCapabilities{VUsersPerJVM: 100, MaxJVMs: 4, MaxVUsers: 400, AvailableVUsers: 400}, "shared_jvm_native_code_detected")
	service := newIsolatedService(t, 1000, WithExecution())
	// 300 VUsers fit the shared envelope at admission, but the runner refuses
	// the script, and 300 dedicated JVMs do not fit four.
	if ack := assignJava(t, service, "refused-large", "shared-script", sharedJVMScript, 300); ack.GetAccepted() {
		t.Fatalf("refused shared group admitted beyond dedicated capacity: %#v", ack)
	}
	if ack := assignJava(t, service, "refused-small", "shared-script", sharedJVMScript, 3); !ack.GetAccepted() {
		t.Fatalf("refused shared group within dedicated capacity: %#v", ack)
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	assignment := service.assignments["refused-small"]
	if assignment.groupSharedJVM["default"] || service.activeSharedJavaVusersLocked() != 0 || service.activePersistentJavaVusersLocked() != 3 {
		t.Fatalf("fallback recorded as shared=%v sharedActive=%d dedicated=%d", assignment.groupSharedJVM["default"], service.activeSharedJavaVusersLocked(), service.activePersistentJavaVusersLocked())
	}
	if execution := service.executions["refused-small"]["default"]; execution == nil || execution.javaShared {
		t.Fatal("fallback execution still marked shared")
	}
}
