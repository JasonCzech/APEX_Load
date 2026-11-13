package database

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Authored SQL, request headers and params follow config.log.redact_run_evidence;
// exact platform-managed values stay masked; the server's result keeps its scrub.
func TestDatabaseAuthoredRequestEvidenceFollowsRunEvidenceSetting(t *testing.T) {
	const (
		literalPwd = "lit-db-pwd-7801"
		platform   = "platform-db-val-7802"
	)
	exact := func(value string) string { return strings.ReplaceAll(value, platform, "[REDACTED]") }
	for _, mode := range []struct {
		name   string
		retain bool
	}{
		{name: "redaction_on", retain: false},
		{name: "redaction_off", retain: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			d := &Database{}
			if err := d.Init(context.Background(), map[string]any{
				"database_driver":                        "sqlite",
				"database_dsn":                           ":memory:",
				protocol.RedactAuthoredEvidenceConfigKey: !mode.retain,
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Teardown(context.Background()) })
			d.SetRuntimeTextRedactor(exact)
			d.SetRuntimeAuthoredTextRedactor(exact)
			action := &protocol.Action{
				Type:    "sql_query",
				Target:  "SELECT 1 WHERE key='" + platform + "' -- password=" + literalPwd,
				Headers: map[string]string{"Authorization": "Basic " + literalPwd, "X-Api-Key": platform},
				Params:  map[string]any{"password": literalPwd, "args": map[string]any{"userPassword": literalPwd}},
				Body:    []byte(`{"password":"` + literalPwd + `"}`),
			}
			snapshot := d.databaseSnapshot(action, &protocol.Result{StatusCode: 500, ResponseBody: "password=server-echo-7803"})
			params := d.redactParamsMap(action.RedactedParams, action.Params)
			encoded, _ := json.Marshal(map[string]any{"snapshot": snapshot, "params": params})
			text := string(encoded)
			if strings.Contains(text, platform) || snapshot.RequestHeaders["X-Api-Key"] != "[REDACTED]" {
				t.Fatalf("platform-managed value leaked: %s", text)
			}
			if strings.Contains(snapshot.ResponseBody, "server-echo-7803") {
				t.Fatalf("response lost its scrub: %q", snapshot.ResponseBody)
			}
			if strings.Contains(snapshot.RequestURL, literalPwd) != mode.retain ||
				strings.Contains(snapshot.RequestBody, literalPwd) != mode.retain ||
				strings.Contains(snapshot.RequestHeaders["Authorization"], literalPwd) != mode.retain ||
				(params["password"] == literalPwd) != mode.retain ||
				strings.Contains(text, `"userPassword":"`+literalPwd) != mode.retain {
				t.Fatalf("authored visibility wrong (want %v): %s", mode.retain, text)
			}
		})
	}
}
