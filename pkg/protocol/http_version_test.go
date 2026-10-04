package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionalHTTPVersionJSONPresenceAndStrictPolicy(t *testing.T) {
	for _, tc := range []struct {
		raw, policy   string
		valid, decode bool
	}{
		{`{}`, "2", true, true}, {`{"http_version":"auto"}`, "auto", true, true},
		{`{"http_version":"1.1"}`, "1.1", true, true}, {`{"http_version":"2"}`, "2", true, true},
		{`{"http_version":null}`, "", false, true}, {`{"http_version":""}`, "", false, true},
		{`{"http_version":" 2 "}`, "", false, true}, {`{"http_version":"HTTP/2"}`, "", false, true},
		{`{"http_version":2}`, "", false, false}, {`{"http_version":false}`, "", false, false},
		{`{"http_version":{}}`, "", false, false}, {`{"http_version":[]}`, "", false, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var action Action
			err := json.Unmarshal([]byte(tc.raw), &action)
			if (err == nil) != tc.decode {
				t.Fatalf("decode error=%v", err)
			}
			if err != nil {
				return
			}
			policy, err := action.HTTPVersion.Resolve("2")
			if (err == nil) != tc.valid || policy != tc.policy {
				t.Fatalf("policy=%q err=%v", policy, err)
			}
			encoded, err := json.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			present := strings.Contains(string(encoded), `"http_version"`)
			if present != (tc.raw != `{}`) {
				t.Fatalf("presence lost: %s", encoded)
			}
			var second Action
			if err := json.Unmarshal(encoded, &second); err != nil {
				t.Fatal(err)
			}
			if second.HTTPVersion != action.HTTPVersion {
				t.Fatal("presence/state changed")
			}
		})
	}
}

func TestNewHTTPVersionOverrideRejectsUnsupportedClaims(t *testing.T) {
	for _, raw := range []string{"", "3", "HTTP/2", " 1.1 ", "{{var.version}}"} {
		if _, err := NewHTTPVersionOverride(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, policy := range []string{"auto", "1.1", "2"} {
		value, err := NewHTTPVersionOverride(policy)
		if err != nil || !value.IsPresent() || value.Value() != policy {
			t.Fatalf("policy=%s value=%v err=%v", policy, value, err)
		}
	}
}
