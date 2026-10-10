package dsl

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseQUICScript(t *testing.T, quic string) (*Script, []string) {
	t.Helper()
	payload := `{"name":"q","protocol":"http","config":{"base_url":"https://app.example.test","http":{"version":"auto+h3","quic":` + quic + `}},"actions":[{"name":"home","type":"GET","target":"/"}]}`
	script, err := parseScript([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return script, ValidateScriptIssues(script)
}

func TestHTTPQUICConfigDecodesAndValidates(t *testing.T) {
	script, issues := parseQUICScript(t, `{"handshake_timeout_ms":2000,"max_idle_timeout_ms":60000,"keep_alive_ms":15000,"alt_svc_cache":"per_iteration","broken_ttl_ms":0}`)
	if len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	quic := script.Config.HTTP.QUIC
	if quic == nil || quic.HandshakeTimeoutMs != 2000 || quic.MaxIdleTimeoutMs != 60000 || quic.KeepAliveMs != 15000 || quic.AltSvcCache != "per_iteration" || quic.BrokenTTLMs == nil || *quic.BrokenTTLMs != 0 {
		t.Fatalf("decoded = %+v", quic)
	}
	encoded, err := json.Marshal(script.Config.HTTP)
	if err != nil || !strings.Contains(string(encoded), `"broken_ttl_ms":0`) {
		t.Fatalf("an authored zero broken_ttl_ms did not round-trip: %s %v", encoded, err)
	}
	_, issues = parseQUICScript(t, `{"handshake_timeout_ms":100,"alt_svc_cache":"per_run"}`)
	if len(issues) != 2 || !strings.Contains(issues[0], "config.http.quic.handshake_timeout_ms") || !strings.Contains(issues[1], "config.http.quic.alt_svc_cache") {
		t.Fatalf("range issues = %v", issues)
	}
}

func TestHTTPQUICConfigReportsUnsupportedKeysThroughClones(t *testing.T) {
	script, issues := parseQUICScript(t, `{"max_stream_receive_window":1048576,"keep_alive_ms":1000,"zero_rtt":true}`)
	if len(issues) != 2 || !strings.Contains(issues[0], "config.http.quic.max_stream_receive_window is not a script setting") || !strings.Contains(issues[1], "config.http.quic.zero_rtt") {
		t.Fatalf("unsupported-key issues = %v", issues)
	}
	// Overrides are layered through JSON clones; the keys must survive them.
	encoded, err := json.Marshal(script.Config.HTTP)
	if err != nil {
		t.Fatal(err)
	}
	var clone HTTPConfig
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	if issues := validateHTTPQUICConfigIssues(clone.QUIC); len(issues) != 2 {
		t.Fatalf("issues after a clone = %v (%s)", issues, encoded)
	}
	if clone.QUIC.KeepAliveMs != 1000 {
		t.Fatalf("known field lost in the clone: %+v", clone.QUIC)
	}
}
