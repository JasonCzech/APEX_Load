package scriptdisclosure

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestDisclosurePreservesTypedHTTPVersionAndSequencePresence(t *testing.T) {
	exact, err := dsl.NewHTTPVersionOverride("2")
	if err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{Name: "HTTP versions", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "basic", Username: "synthetic", Password: "2"}}}, Actions: []dsl.Action{{Name: "exact", Type: "GET", Target: "/", HTTPVersion: exact}, {Name: "inherit", Type: "GET", Target: "/"}, {Name: "group", Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{{Name: "child", Type: "GET", Target: "/", HTTPVersion: exact}}}}}}
	projected, err := Project(script)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Actions[0].HTTPVersion != exact || projected.Actions[1].HTTPVersion.IsPresent() {
		t.Fatal("typed version/presence altered by credential projection")
	}
	raw, err := json.Marshal(projected.Actions[2].Params["requests"])
	if err != nil {
		t.Fatal(err)
	}
	var children []dsl.Action
	if err := json.Unmarshal(raw, &children); err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].HTTPVersion != exact {
		t.Fatalf("sequence policy altered: %s", raw)
	}
}
