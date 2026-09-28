package main

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
)

func TestRunnerPilotRemainsExplicit(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		op := config.JavaVuserOperatorConfig{Enabled: enabled, AuthToken: "token", MaxProcesses: 25}
		options, err := runnerServerOptions(context.Background(), op, "")
		if err != nil || options.HostAdmission != nil || options.PooledControlProfiles != nil || options.Operator.Enabled != op.Enabled || options.Operator.MaxProcesses != 25 {
			t.Fatal("default runner changed", err)
		}
	}
	for _, op := range []config.JavaVuserOperatorConfig{{}, {Enabled: true}, {Enabled: true, AuthToken: "token", MaxProcesses: 25}} {
		if _, err := runnerServerOptions(context.Background(), op, "/missing/operator-pilot.json"); err == nil {
			t.Fatal("invalid explicit pilot silently ignored")
		}
	}
}
