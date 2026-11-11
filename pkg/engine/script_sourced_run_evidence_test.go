package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScriptRedactsRunEvidenceDefaultsOffWhileCredentialsAlwaysRedact(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name            string
		script          *dsl.Script
		wantEvidence    bool
		wantCredentials bool
	}{
		{name: "nil script", script: nil, wantEvidence: false, wantCredentials: false},
		{name: "unset", script: &dsl.Script{}, wantEvidence: false, wantCredentials: true},
		{name: "explicit off", script: &dsl.Script{Config: dsl.ScriptConfig{Log: dsl.LogConfig{RedactRunEvidence: &off}}}, wantEvidence: false, wantCredentials: true},
		{name: "explicit on", script: &dsl.Script{Config: dsl.ScriptConfig{Log: dsl.LogConfig{RedactRunEvidence: &on}}}, wantEvidence: true, wantCredentials: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ScriptRedactsRunEvidence(test.script); got != test.wantEvidence {
				t.Fatalf("ScriptRedactsRunEvidence() = %t, want %t", got, test.wantEvidence)
			}
			if got := scriptRedactsCredentials(test.script); got != test.wantCredentials {
				t.Fatalf("scriptRedactsCredentials() = %t, want %t", got, test.wantCredentials)
			}
		})
	}
}
