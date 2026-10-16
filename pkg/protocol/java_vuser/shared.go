package java_vuser

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

// Stable reasons a script that asked for shared JVMs runs on dedicated JVMs
// because of how this generator's runner is configured.
const (
	SharedJVMReasonRunnerDisabled = "shared_jvm_runner_disabled"
	SharedJVMReasonNeedsSidecar   = "shared_jvm_requires_sidecar_runner"
	SharedJVMReasonUnavailable    = "shared_jvm_unavailable"
)

// ProcessOwnerID is this generator's sidecar owner identity. Shared runs must
// claim the same owner as dedicated sessions: a different owner ID would fence
// and reap every JVM the other path owns.
func ProcessOwnerID() string { return javaVuserProcessOwnerID() }

// SharedJVMRuntime is a shared group's sidecar connection and claim, resolved
// from exactly the configuration a dedicated VUser of the group would receive.
type SharedJVMRuntime struct {
	Client        *javasidecar.Client
	Claim         javasidecar.SharedJVMClaim
	ActionTimeout time.Duration
}

// PrepareSharedJVMRuntime resolves cfg like a dedicated VUser's Init without
// claiming an owner. A non-empty reason means the generator's runner cannot
// host shared JVMs and the group stays dedicated; an error means cfg would be
// rejected by dedicated execution too. loadRunnerThreads asks for LoadRunner
// thread mode.
func PrepareSharedJVMRuntime(ctx context.Context, cfg map[string]any, target, scriptSHA256 string, vusersPerJVM int, loadRunnerThreads bool) (SharedJVMRuntime, string, error) {
	// Decide from operator configuration first: a local runner would otherwise
	// resolve a JDK and harness for a mode this group cannot use.
	operator := config.LoadJavaVuserOperatorConfig()
	if !operator.Enabled {
		return SharedJVMRuntime{}, SharedJVMReasonRunnerDisabled, nil
	}
	if mode := strings.ToLower(strings.TrimSpace(operator.RunnerMode)); mode != config.JavaVuserRunnerModeSidecar {
		return SharedJVMRuntime{}, SharedJVMReasonNeedsSidecar, nil
	}
	j := &JavaVuser{}
	if err := j.init(ctx, cfg, false); err != nil {
		return SharedJVMRuntime{}, "", err
	}
	client, ok := j.sidecar.(*javasidecar.Client)
	if !ok || client == nil {
		return SharedJVMRuntime{}, "", errors.New("java_vuser sidecar client is unavailable")
	}
	label := func(key string) string {
		if value := executionContextValue(cfg, key); javasidecar.ValidSharedJVMLabel(value) {
			return value
		}
		return ""
	}
	return SharedJVMRuntime{
		Client: client,
		Claim: javasidecar.SharedJVMClaim{
			Runtime:           j.sidecarRuntimeSettings(),
			TargetClass:       strings.TrimSpace(target),
			ScriptSHA256:      scriptSHA256,
			VUsersPerJVM:      vusersPerJVM,
			GroupName:         label("group_name"),
			ScenarioID:        label("scenario_id"),
			LoadRunnerThreads: loadRunnerThreads,
		},
		ActionTimeout: j.cfg.ActionTimeout,
	}, "", nil
}

// SharedJVMRefusal reports a runner refusal that leaves the group eligible for
// dedicated JVMs, with its stable code. Any other error is ambiguous.
func SharedJVMRefusal(err error) (string, bool) {
	var remote *javasidecar.RemoteError
	if errors.As(err, &remote) && javasidecar.SharedJVMClaimErrorCode(remote.Code) {
		return remote.Code, true
	}
	return "", false
}
