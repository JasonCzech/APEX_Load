package dsl

import (
	"fmt"
	"testing"
)

func TestProtocolTimeoutValidatorsEnforceRuntimeCap(t *testing.T) {
	tooLarge := MaxConfiguredProtocolTimeoutMS + 1

	grpcScript := &Script{Protocol: "grpc", Config: ScriptConfig{GRPC: GRPCConfig{TimeoutMs: tooLarge}}}
	assertIssue(t, validateGRPCConfigIssues(grpcScript), fmt.Sprintf("config.grpc.timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, validateGRPCActionIssues(0, Action{Name: "call", Type: "grpc_unary", Target: "pkg.Service/Method", Params: map[string]any{"timeout_ms": tooLarge}}), fmt.Sprintf(`actions[0] "call": grpc_unary timeout_ms must not exceed %d`, MaxConfiguredProtocolTimeoutMS))

	websocketScript := &Script{Protocol: "websocket", Config: ScriptConfig{WebSocket: WebSocketConfig{ReadTimeoutMs: tooLarge, WriteTimeoutMs: tooLarge}}}
	websocketIssues := validateWebSocketConfigIssues(websocketScript)
	assertIssue(t, websocketIssues, fmt.Sprintf("config.websocket.read_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, websocketIssues, fmt.Sprintf("config.websocket.write_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, validateWebSocketActionIssues(0, Action{Name: "receive", Type: "ws_receive", Params: map[string]any{"timeout_ms": tooLarge}}), fmt.Sprintf(`actions[0] "receive": ws_receive timeout_ms must not exceed %d`, MaxConfiguredProtocolTimeoutMS))

	databaseIssues := validateDatabaseScriptIssues(&Script{Config: ScriptConfig{Database: DatabaseConfig{QueryTimeoutMs: tooLarge}}})
	assertIssue(t, databaseIssues, fmt.Sprintf("config.database.query_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))

	javaScript := &Script{Config: ScriptConfig{JavaVuser: JavaVuserConfig{StartupTimeoutMs: tooLarge, ActionTimeoutMs: tooLarge}}}
	javaIssues := validateJavaVuserScriptIssues(javaScript)
	assertIssue(t, javaIssues, fmt.Sprintf("config.java_vuser.startup_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, javaIssues, fmt.Sprintf("config.java_vuser.action_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, validateJavaVuserActionIssues(0, Action{Name: "run", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"timeout_ms": tooLarge}}), fmt.Sprintf(`actions[0] "run": java_vuser_run params.timeout_ms must be between 0 and %d`, MaxConfiguredProtocolTimeoutMS))

	browserAction := Action{
		Name:    "wait",
		Type:    "browser_wait",
		Target:  "body",
		Params:  map[string]any{"timeout_ms": tooLarge},
		WaitFor: &WaitCondition{TimeoutMs: tooLarge},
		Sync:    &BrowserSyncPolicy{TimeoutMs: tooLarge, StableMs: tooLarge},
	}
	browserIssues := validateBrowserActionIssues(0, browserAction)
	assertIssue(t, browserIssues, fmt.Sprintf(`actions[0] "wait": params.timeout_ms must not exceed %d`, MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, browserIssues, fmt.Sprintf(`actions[0] "wait": wait_for.timeout_ms must not exceed %d`, MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, browserIssues, fmt.Sprintf(`actions[0] "wait": sync.timeout_ms must not exceed %d`, MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, browserIssues, fmt.Sprintf(`actions[0] "wait": sync.stable_ms must not exceed %d`, MaxConfiguredProtocolTimeoutMS))
}
