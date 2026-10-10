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
		{`{"http_version":"auto+h3"}`, "auto+h3", true, true}, {`{"http_version":"3"}`, "3", true, true},
		{`{"http_version":"h3"}`, "", false, true}, {`{"http_version":"HTTP/3"}`, "", false, true},
		{`{"http_version":"h3-29"}`, "", false, true}, {`{"http_version":"AUTO+H3"}`, "", false, true},
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
	// Leaf policies are strict scalars: spellings that NormalizeVersion maps
	// for config values (HTTP/3, h3) and draft tokens are not leaf policies.
	for _, raw := range []string{"", "HTTP/2", " 1.1 ", "{{var.version}}", "h3", "HTTP/3", " 3", "h3-29", "auto+H3", "auto+h2"} {
		if _, err := NewHTTPVersionOverride(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, policy := range []string{"auto", "1.1", "2", "auto+h3", "3"} {
		value, err := NewHTTPVersionOverride(policy)
		if err != nil || !value.IsPresent() || value.Value() != policy {
			t.Fatalf("policy=%s value=%v err=%v", policy, value, err)
		}
	}
}

func TestOptionalHTTPVersionInheritsHTTP3Policies(t *testing.T) {
	var absent OptionalHTTPVersion
	for _, inherited := range []string{"auto+h3", "3"} {
		if policy, err := absent.Resolve(inherited); err != nil || policy != inherited {
			t.Fatalf("inherited %q resolved to %q, %v", inherited, policy, err)
		}
	}
	// A leaf override beats an inherited HTTP/3 policy, in both directions.
	leaf, _ := NewHTTPVersionOverride("1.1")
	if policy, err := leaf.Resolve("3"); err != nil || policy != "1.1" {
		t.Fatalf("leaf 1.1 over inherited 3 = %q, %v", policy, err)
	}
	leaf, _ = NewHTTPVersionOverride("3")
	if policy, err := leaf.Resolve("2"); err != nil || policy != "3" {
		t.Fatalf("leaf 3 over inherited 2 = %q, %v", policy, err)
	}
	if _, err := absent.Resolve("h3"); err == nil {
		t.Fatal("an unnormalized inherited spelling was accepted")
	}
}
