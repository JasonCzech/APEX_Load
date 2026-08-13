package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScriptRedactsRunEvidenceDefaultsOff(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name   string
		script *dsl.Script
		want   bool
	}{
		{name: "nil script", script: nil, want: false},
		{name: "unset", script: &dsl.Script{}, want: false},
		{name: "explicit off", script: &dsl.Script{Config: dsl.ScriptConfig{Log: dsl.LogConfig{RedactRunEvidence: &off}}}, want: false},
		{name: "explicit on", script: &dsl.Script{Config: dsl.ScriptConfig{Log: dsl.LogConfig{RedactRunEvidence: &on}}}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ScriptRedactsRunEvidence(test.script); got != test.want {
				t.Fatalf("ScriptRedactsRunEvidence() = %t, want %t", got, test.want)
			}
		})
	}
}
