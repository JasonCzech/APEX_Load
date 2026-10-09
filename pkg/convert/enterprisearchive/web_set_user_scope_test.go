package enterprisearchive

import (
	"reflect"
	"strings"
	"testing"
)

func TestWebSetUserScopesCredentialsToHosts(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		hosts     []string
		gated     bool
		unscoped  bool
		attribute bool
	}{
		{
			name:  "host and port",
			body:  `web_set_user("alice", "pw", "shop.test:443");`,
			hosts: []string{"shop.test:443"},
		},
		{
			name:  "URL form is normalized",
			body:  `web_set_user("alice", "pw", "https://Shop.Test:8443/app");`,
			hosts: []string{"shop.test:8443"},
		},
		{
			name:  "same credentials for a second host widen the scope",
			body:  `web_set_user("alice", "pw", "a.test"); web_set_user("alice", "pw", "b.test");`,
			hosts: []string{"a.test", "b.test"},
		},
		{
			name:  "same credentials without a host apply everywhere",
			body:  `web_set_user("alice", "pw", "a.test"); web_set_user("alice", "pw");`,
			hosts: nil,
		},
		{
			name:  "different credentials are gated",
			body:  `web_set_user("alice", "pw", "a.test"); web_set_user("bob", "other", "b.test");`,
			hosts: []string{"a.test"},
			gated: true,
		},
		{
			name:      "parameterized host stays unscoped",
			body:      `web_set_user("alice", "pw", "{AuthHost}");`,
			hosts:     nil,
			unscoped:  true,
			attribute: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertSingleWebSource(t, "\t"+tt.body+"\n\tweb_url(\"home\", \"URL=https://shop.test/\", LAST);")
			auth := result.Script.Config.HTTP.Auth
			if auth.Username != "alice" || auth.Type != "negotiate_any" {
				t.Fatalf("auth = %+v", auth)
			}
			if !reflect.DeepEqual(auth.Hosts, tt.hosts) {
				t.Fatalf("hosts = %#v, want %#v", auth.Hosts, tt.hosts)
			}
			gated := false
			for _, action := range result.Script.Actions {
				if blockers, _ := action.Metadata["execution_blockers"].([]string); len(blockers) > 0 && blockers[0] == webSetUserConflictReasonCode {
					gated = true
				}
			}
			if gated != tt.gated {
				t.Fatalf("conflict gate = %v, want %v", gated, tt.gated)
			}
			warned := strings.Contains(strings.Join(result.Warnings, "\n"), "apply to every host")
			if warned != tt.unscoped {
				t.Fatalf("unscoped warning = %v, want %v (%v)", warned, tt.unscoped, result.Warnings)
			}
			_, attribute := result.Script.Config.WebRuntime.AdditionalAttributes["web_set_user.host"]
			if attribute != tt.attribute {
				t.Fatalf("web_set_user.host attribute = %v, want %v", attribute, tt.attribute)
			}
		})
	}
}
