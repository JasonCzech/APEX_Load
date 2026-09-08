package api

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
	"testing"
)

func TestStaticPreflightDoesNotClaimRuntimeAvailability(t *testing.T) {
	for _, tc := range []struct{ protocol, detail string }{
		{"http", "connectivity"}, {"java_vuser", "authoring approval"},
		{"sap_gui", "no bundled runtime adapter"}, {"browser", "runtime availability"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			preflight := buildReplayPreflight(&dsl.Script{Protocol: tc.protocol}, nil, nil, nil)
			for _, check := range preflight.Checks {
				if check.ID == "runtime" {
					if check.Status != "warn" || !strings.Contains(check.Detail, tc.detail) {
						t.Fatalf("misleading runtime check: %+v", check)
					}
					return
				}
			}
			t.Fatal("missing runtime check")
		})
	}
}

func TestSAPSessionBehaviorProjectionIsBounded(t *testing.T) {
	for _, value := range []string{"attach", "new_session", "reuse", "secret-canary"} {
		got := projectScriptNode(map[string]any{"session_behavior": value}, scriptProjectionContext{key: "sapgui"}).(map[string]any)["session_behavior"]
		if dsl.IsSAPGUISessionBehavior(value) && got != value {
			t.Fatalf("lost configuration %s", value)
		}
		if !dsl.IsSAPGUISessionBehavior(value) && got == value {
			t.Fatal("arbitrary session material leaked")
		}
	}
	got := projectScriptNode(map[string]any{"session_behavior": "attach"}, scriptProjectionContext{key: "sapgui", metadata: true}).(map[string]any)["session_behavior"]
	if got == "attach" {
		t.Fatal("opaque metadata must remain protected")
	}
}
