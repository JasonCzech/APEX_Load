package dsl

import "testing"

func TestHTTP3ProbeOriginsListsStaticAndDynamicHTTPSOrigins(t *testing.T) {
	script := &Script{Protocol: "http", Config: ScriptConfig{BaseURL: "https://shop.example", HTTP: HTTPConfig{Version: "auto+h3"}}, Actions: []Action{
		{Name: "home", Type: "GET", Target: "/"},
		{Name: "api", Type: "GET", Target: "https://api.example:8443/orders", HTTPVersion: versionPtr(t, "3")},
		{Name: "again", Type: "GET", Target: "/cart", HTTPVersion: versionPtr(t, "3")},
		{Name: "tcp", Type: "GET", Target: "https://tcp.example/", HTTPVersion: versionPtr(t, "2")},
		{Name: "clear", Type: "GET", Target: "http://plain.example/"},
		{Name: "dynamic", Type: "GET", Target: "https://{{param.host}}/x"},
		{Name: "templated path", Type: "GET", Target: "/orders/{{param.id}}?q={{param.q}}"},
		{Name: "templated absolute path", Type: "GET", Target: "https://cdn.example/{{param.asset}}"},
		{Name: "whole URL", Type: "GET", Target: "{{param.next_url}}"},
		{Name: "think", Type: "think_time"},
	}}
	got := HTTP3ProbeOrigins(script)
	want := []HTTP3ProbeOrigin{
		{Origin: "https://shop.example", Policy: "3"}, // auto+h3 first, then a request requires 3
		{Origin: "https://api.example:8443", Policy: "3"},
		{Origin: "https://{{param.host}}", Policy: "auto+h3", Dynamic: true},
		// A template in the path keeps the origin static.
		{Origin: "https://cdn.example", Policy: "auto+h3"},
		{Origin: "{{param.next_url}}", Policy: "auto+h3", Dynamic: true},
	}
	if len(got) != len(want) {
		t.Fatalf("origins = %+v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("origin %d = %+v, want %+v", index, got[index], want[index])
		}
	}
	templatedBase := &Script{Protocol: "http", Config: ScriptConfig{BaseURL: "https://{{env.host}}/api", HTTP: HTTPConfig{Version: "3"}}, Actions: []Action{{Name: "a", Type: "GET", Target: "/x/{{param.id}}"}}}
	if got := HTTP3ProbeOrigins(templatedBase); len(got) != 1 || got[0] != (HTTP3ProbeOrigin{Origin: "https://{{env.host}}", Policy: "3", Dynamic: true}) {
		t.Fatalf("templated base = %+v", got)
	}
	if HTTP3ProbeOrigins(&Script{Protocol: "http", Config: ScriptConfig{BaseURL: "https://shop.example"}, Actions: []Action{{Name: "home", Type: "GET", Target: "/"}}}) != nil {
		t.Fatal("a TCP-only script has no HTTP/3 origins")
	}
	many := &Script{Protocol: "http", Config: ScriptConfig{HTTP: HTTPConfig{Version: "3"}}}
	for i := 0; i < MaxHTTP3ProbeOrigins+5; i++ {
		many.Actions = append(many.Actions, Action{Name: "a", Type: "GET", Target: "https://host" + string(rune('a'+i)) + ".example/"})
	}
	if got := HTTP3ProbeOrigins(many); len(got) != MaxHTTP3ProbeOrigins {
		t.Fatalf("bounded origins = %d", len(got))
	}
}

func versionPtr(t *testing.T, value string) OptionalHTTPVersion {
	t.Helper()
	version, err := NewHTTPVersionOverride(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
