package engine

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAppendHTTPSecurityConfigPassesAuthHostScope(t *testing.T) {
	script := &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
		Type: "basic", Username: "alice", Password: "pw", Hosts: []string{"shop.test:443", "api.shop.test"},
	}}}}
	cfg := map[string]any{}
	appendHTTPSecurityConfig(cfg, script)
	if got, _ := cfg["auth_hosts"].([]string); !reflect.DeepEqual(got, []string{"shop.test:443", "api.shop.test"}) {
		t.Fatalf("auth_hosts = %#v", cfg["auth_hosts"])
	}
	script.Config.HTTP.Auth.Hosts[0] = "mutated.test"
	if got, _ := cfg["auth_hosts"].([]string); got[0] != "shop.test:443" {
		t.Fatal("the protocol config must not alias the script's host list")
	}

	unscoped := map[string]any{}
	appendHTTPSecurityConfig(unscoped, &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "basic", Username: "a"}}}})
	if _, ok := unscoped["auth_hosts"]; ok {
		t.Fatal("unscoped auth must not set auth_hosts")
	}
}
