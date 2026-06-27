package main

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/buildinfo"
)

func TestBuildAboutMessage(t *testing.T) {
	msg := buildAboutMessage(buildinfo.Info{DisplayVersion: "1.4.2", Commit: "abc1234", BuildTime: "2026-06-27T10:00:00Z"})
	for _, want := range []string{"Enterprise Performance Testing Platform", "1.4.2", "abc1234", "2026-06-27", "Project Stormrunner"} {
		if !strings.Contains(msg, want) {
			t.Errorf("about message missing %q\n%s", want, msg)
		}
	}
	// Guard against the old hardcoded version-less copyright line.
	if !strings.Contains(msg, "Version 1.4.2") {
		t.Errorf("about message should include the live version: %s", msg)
	}
}

func TestBuildAboutMessageMinimal(t *testing.T) {
	// No commit/build-time should not produce empty parens or "Built ".
	msg := buildAboutMessage(buildinfo.Info{DisplayVersion: "0.1.0"})
	if strings.Contains(msg, "()") {
		t.Errorf("should not render empty commit parens: %s", msg)
	}
	if strings.Contains(msg, "Built ") {
		t.Errorf("should not render a blank build time: %s", msg)
	}
	if !strings.Contains(msg, "Version 0.1.0") {
		t.Errorf("expected version line: %s", msg)
	}
}
