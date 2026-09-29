package javasidecar

import (
	"strconv"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// BindDedicatedPodContext fills only the per-Vuser fields of an operator-owned
// image profile. Conflicting baked-in context is rejected rather than silently
// overriding it. Call before NewDedicatedPodRuntime; no JVM/user code is run.
func BindDedicatedPodContext(p DedicatedPodProfile, c javarunner.DedicatedHostContext) (DedicatedPodProfile, error) {
	if c.Validate() != nil || p.Command.VUserID != "" || p.Command.GroupName != "" || p.Command.ScenarioID != "" || p.Command.IgnoreThinkTime != "" || p.Command.ThinkTimeLimitMS != "" {
		return DedicatedPodProfile{}, ErrPodRuntime
	}
	p.Command.VUserID = strconv.FormatInt(c.VUserID, 10)
	p.Command.GroupName = c.GroupName
	p.Command.ScenarioID = c.ScenarioID
	p.Command.IgnoreThinkTime = strconv.FormatBool(c.IgnoreThinkTime)
	p.Command.ThinkTimeLimitMS = strconv.FormatInt(c.ThinkTimeLimitMS, 10)
	return p, nil
}
