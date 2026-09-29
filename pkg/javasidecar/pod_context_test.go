package javasidecar

import (
	"github.com/jasonczech/apexload/pkg/javarunner"
	"testing"
)

func TestBindDedicatedPodContextRejectsOverrides(t *testing.T) {
	c := javarunner.DedicatedHostContext{Contract: javarunner.DedicatedHostContextContract, SessionID: "session", VUserID: 7, GroupName: "group", ScenarioID: "scenario", IgnoreThinkTime: true, ThinkTimeLimitMS: 12}
	p := DedicatedPodProfile{Command: ActionCommand{ClassName: "ApprovedActions", SourceArchiveRef: "approved-source"}}
	got, e := BindDedicatedPodContext(p, c)
	if e != nil || got.Command.ClassName != p.Command.ClassName || got.Command.SourceArchiveRef != p.Command.SourceArchiveRef || got.Command.VUserID != "7" || got.Command.GroupName != "group" || got.Command.ScenarioID != "scenario" || got.Command.IgnoreThinkTime != "true" || got.Command.ThinkTimeLimitMS != "12" || p.Command.VUserID != "" {
		t.Fatal(got, e)
	}
	for _, mutate := range []func(*ActionCommand){func(c *ActionCommand) { c.VUserID = "1" }, func(c *ActionCommand) { c.GroupName = "group" }, func(c *ActionCommand) { c.ScenarioID = "scenario" }, func(c *ActionCommand) { c.IgnoreThinkTime = "false" }, func(c *ActionCommand) { c.ThinkTimeLimitMS = "0" }} {
		bad := p
		mutate(&bad.Command)
		if _, e := BindDedicatedPodContext(bad, c); e == nil {
			t.Fatal("image context silently overridden")
		}
	}
	c.VUserID = -1
	if _, e := BindDedicatedPodContext(p, c); e == nil {
		t.Fatal("invalid context accepted")
	}
}
