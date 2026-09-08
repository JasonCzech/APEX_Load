package scriptdisclosure

import (
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
)

func TestSAPSessionBehaviorIsConfiguration(t *testing.T) {
	for _, value := range []string{"attach", "new_session", "reuse", "secret-canary"} {
		script := &dsl.Script{Protocol: "sap_gui", Config: dsl.ScriptConfig{SAPGUI: dsl.SAPGUIConfig{SessionBehavior: value, Password: "password-canary"}}, Actions: []dsl.Action{{Type: "sapgui_open_connection", Target: "attach"}}}
		got, err := Project(script)
		if err != nil {
			t.Fatal(err)
		}
		want := credential.Redacted
		if dsl.IsSAPGUISessionBehavior(value) {
			want = value
		}
		if got.Config.SAPGUI.SessionBehavior != want {
			t.Fatalf("%s: got %s", value, got.Config.SAPGUI.SessionBehavior)
		}
		if got.Config.SAPGUI.Password != credential.Redacted {
			t.Fatal("password disclosed")
		}
		if dsl.IsSAPGUISessionBehavior(value) && got.Actions[0].Target != "attach" {
			t.Fatal("public session enum tainted unrelated request target")
		}
	}
}

func TestSAPSessionBehaviorInOpaqueParamsRemainsSensitive(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{
		Type: "request", Target: "attach",
		Params: map[string]any{"sapgui": map[string]any{"session_behavior": "attach"}},
	}}}
	got, err := Project(script)
	if err != nil {
		t.Fatal(err)
	}
	if got.Actions[0].Params["sapgui"].(map[string]any)["session_behavior"] != credential.Redacted {
		t.Fatal("opaque session value disclosed")
	}
	if got.Actions[0].Target != credential.Redacted {
		t.Fatal("opaque credential was not collected for equivalent-value redaction")
	}
}
