package clientids

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	session     = "6f1c2a3b-4d5e-4f60-8172-93a4b5c6d7e8"
	sessionTwo  = "7a1c2a3b-4d5e-4f60-8172-93a4b5c6d7e8"
	logOne      = "11111111-2222-4333-8444-555555555555"
	logTwo      = "21111111-2222-4333-8444-555555555555"
	logThree    = "31111111-2222-4333-8444-555555555555"
	device      = "D1C2A3B4-4D5E-4F60-8172-93A4B5C6D7E8"
	serverID    = "a0a0a0a0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"
	tokenID     = "b0b0b0b0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"
	pathID      = "c0c0c0c0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"
	assertedID  = "d0d0d0d0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"
	upperRotate = "E0E0E0E0-B1B1-4C2C-8D3D-E4E4E4E4E4E4"
)

func syntheticHAR(t *testing.T, bodies bool) []byte {
	t.Helper()
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"tid":"` + tokenID + `"}`))
	text := func(value string) any {
		if !bodies {
			return nil
		}
		return value
	}
	har := map[string]any{"log": map[string]any{"entries": []any{
		map[string]any{"request": map[string]any{"method": "GET", "url": "https://example.test/"}, "response": map[string]any{
			"status": 200, "content": map[string]any{"mimeType": "application/json", "text": text(`{"record":"` + serverID + `","access_token":"eyJhbGciOiJIUzI1NiJ9.` + payload + `.sig"}`)}}},
		map[string]any{"request": map[string]any{"method": "GET", "url": "https://example.test/app.js"}, "response": map[string]any{
			"status": 200, "content": map[string]any{"mimeType": "text/javascript", "text": text("var x=1;")}}},
	}}}
	data, err := json.Marshal(har)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func syntheticScript() *dsl.Script {
	escaped, _ := json.Marshal(`{"clientSessionId":"` + session + `","deviceId":"` + device + `"}`)
	return &dsl.Script{Name: "s", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{
		{Name: "log", Type: "POST", Target: "https://example.test/log", Body: `{"logLines":[{"id":"` + logOne + `","payload":` + string(escaped) + `},{"id":"` + logTwo + `"},{"id":"` + logThree + `"}]}`},
		{Name: "next", Type: "POST", Target: "https://example.test/next?clientSessionId=" + session + "&record=" + serverID + "&tid=" + tokenID, Body: `{"clientSessionId":"` + sessionTwo + `"}`,
			HeaderList: []dsl.HeaderEntry{{Name: "X-Request-Id", Value: upperRotate}}},
		{Name: "resend", Type: "POST", Target: "https://example.test/log", Body: `{"logLines":[{"id":"` + logOne + `"},{"id":"` + logTwo + `"}]}`},
		{Name: "item", Type: "GET", Target: "https://example.test/items/" + pathID},
		{Name: "checked", Type: "POST", Target: "https://example.test/check", Body: `{"eventId":"` + assertedID + `"}`,
			Assertions: []dsl.Assertion{{Type: "body_contains", Value: assertedID}}},
	}}
}

func TestApplyParameterizesOnlyGeneratedClientValues(t *testing.T) {
	server, ok, err := ServerUUIDs(syntheticHAR(t, true))
	if err != nil || !ok {
		t.Fatalf("ServerUUIDs: ok=%t err=%v", ok, err)
	}
	if !server[serverID] || !server[tokenID] {
		t.Fatalf("server UUIDs missing body or JWT values: %v", server)
	}
	script := syntheticScript()
	report := Apply(script, server)

	params := map[string]dsl.Parameter{}
	for _, parameter := range script.Parameters {
		params[parameter.Name] = parameter
	}
	if p := params["client_session_id"]; p.Type != "uuid" || p.Update != "each_iteration" || p.Format != "" || len(p.Values) != 0 {
		t.Fatalf("client_session_id = %+v", p)
	}
	if p := params["client_session_id_2"]; p.Update != "each_iteration" {
		t.Fatalf("rotated session value = %+v", p)
	}
	if p := params["id_each_use"]; p.Update != "each_occurrence" || len(p.Values) != 0 {
		t.Fatalf("single-use log ids = %+v", p)
	}
	if p := params["x_request_id"]; p.Format != "uppercase" || p.Update != "each_iteration" {
		t.Fatalf("uppercase request id = %+v", p)
	}
	if len(script.Parameters) != 4 {
		t.Fatalf("parameters = %+v", script.Parameters)
	}
	body := script.Actions[0].Body
	for _, literal := range []string{session, logOne, logTwo, logThree} {
		if strings.Contains(body, literal) {
			t.Fatalf("generated literal %s kept in %s", literal, body)
		}
	}
	if !strings.Contains(body, `"id":"{{param.id_each_use}}"`) || !strings.Contains(body, `{{param.client_session_id}}`) || !strings.Contains(body, device) {
		t.Fatalf("log body = %s", body)
	}
	next := script.Actions[1]
	if !strings.Contains(next.Target, "clientSessionId={{param.client_session_id}}") || !strings.Contains(next.Target, serverID) || !strings.Contains(next.Target, tokenID) {
		t.Fatalf("next target = %s", next.Target)
	}
	if next.HeaderList[0].Value != "{{param.x_request_id}}" {
		t.Fatalf("request id header = %s", next.HeaderList[0].Value)
	}
	if script.Actions[2].Body != `{"logLines":[{"id":"{{param.id_each_use}}"},{"id":"{{param.id_each_use}}"}]}` {
		t.Fatalf("resent log ids = %s", script.Actions[2].Body)
	}
	if !strings.Contains(script.Actions[3].Target, pathID) || !strings.Contains(script.Actions[4].Body, assertedID) {
		t.Fatal("keyless or asserted values changed")
	}
	// device (identity), path segment and asserted event id stay recorded.
	if report.LeftAsRecorded != 3 {
		t.Fatalf("left as recorded = %d", report.LeftAsRecorded)
	}
	warning := report.Warning()
	for _, want := range []string{"client_session_id", "id_each_use", "at every reference", "set a parameter's update to once", "left as recorded"} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning missing %q: %s", want, warning)
		}
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("parameterized script invalid: %v", issues)
	}
}

func TestServerUUIDsNeedsCapturedBodies(t *testing.T) {
	if _, ok, err := ServerUUIDs(syntheticHAR(t, false)); err != nil || ok {
		t.Fatalf("capture without bodies judged: ok=%t err=%v", ok, err)
	}
}

func TestApplyLeavesSingleNeutralValues(t *testing.T) {
	script := &dsl.Script{Name: "s", Protocol: "http", Actions: []dsl.Action{{Name: "a", Type: "POST", Target: "https://example.test/", Body: `{"manifestId":"` + logOne + `","c":"` + logTwo + `"}`}}}
	report := Apply(script, map[string]bool{})
	if len(script.Parameters) != 0 || report.LeftAsRecorded != 2 {
		t.Fatalf("parameters %+v, left %d", script.Parameters, report.LeftAsRecorded)
	}
}

func TestApplyKeepsFlowValuesPerIteration(t *testing.T) {
	script := &dsl.Script{Name: "s", Protocol: "http", Actions: []dsl.Action{
		{Name: "a", Type: "POST", Target: "https://example.test/", Body: `{"sessionId":"` + logOne + `"}`},
		{Name: "b", Type: "POST", Target: "https://example.test/", Body: `{"sessionId":"` + logTwo + `"}`},
		{Name: "c", Type: "POST", Target: "https://example.test/", Body: `{"sessionId":"` + logThree + `"}`},
	}}
	Apply(script, map[string]bool{})
	if len(script.Parameters) != 3 {
		t.Fatalf("session values = %+v", script.Parameters)
	}
	for _, parameter := range script.Parameters {
		if parameter.Update != "each_iteration" {
			t.Fatalf("session parameter = %+v", parameter)
		}
	}
}

func TestApplyLeavesCredentialLocatedFields(t *testing.T) {
	script := &dsl.Script{Name: "s", Protocol: "http", Actions: []dsl.Action{
		{Name: "a", Type: "GET", Target: "https://example.test/",
			HeaderList: []dsl.HeaderEntry{{Name: "X-Session-Id", Value: session}, {Name: "X-Request-Id", Value: logOne}},
			Metadata:   map[string]any{dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{{Field: "header_list", Index: 0}}}},
		{Name: "b", Type: "GET", Target: "https://example.test/", HeaderList: []dsl.HeaderEntry{{Name: "X-Session-Id", Value: session}, {Name: "X-Request-Id", Value: logTwo}}},
	}}
	report := Apply(script, map[string]bool{})
	if script.Actions[0].HeaderList[0].Value != session || script.Actions[1].HeaderList[0].Value != session {
		t.Fatal("credential-located session value rewritten")
	}
	if script.Actions[0].HeaderList[1].Value != "{{param.x_request_id}}" || report.LeftAsRecorded != 1 {
		t.Fatalf("request ids = %+v, left %d", script.Actions[0].HeaderList, report.LeftAsRecorded)
	}
}

func TestAdjacentUUIDsAreEachMatched(t *testing.T) {
	found := map[string]bool{}
	collectUUIDs(found, `{"ids":"`+serverID+`,`+tokenID+`"}`)
	if !found[serverID] || !found[tokenID] {
		t.Fatalf("server corpus = %v, want both comma-separated ids", found)
	}
	if spans := uuidSpans("0" + serverID); len(spans) != 0 {
		t.Fatalf("spans = %v, want a longer hex run rejected", spans)
	}
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{
		{Name: "a", Type: "POST", Target: "https://example.test/a", Body: `{"requestIds":"` + logOne + `,` + logTwo + `"}`},
	}}
	report := Apply(script, nil)
	if len(report.Parameters) != 2 || strings.Contains(script.Actions[0].Body, logTwo) || strings.Contains(script.Actions[0].Body, logOne) {
		t.Fatalf("report = %+v, body = %s, want both adjacent values replaced", report, script.Actions[0].Body)
	}
}

func TestApplyKeepsSetupValuesOncePerVirtualUser(t *testing.T) {
	script := &dsl.Script{Protocol: "http",
		Init: []dsl.Action{{Name: "login", Type: "POST", Target: "https://example.test/login", Body: `{"clientSessionId":"` + session + `"}`}},
		Actions: []dsl.Action{
			{Name: "use", Type: "POST", Target: "https://example.test/use", Body: `{"clientSessionId":"` + session + `","requestId":"` + logOne + `"}`},
		},
	}
	report := Apply(script, nil)
	updates := map[string]string{}
	for _, parameter := range script.Parameters {
		updates[parameter.Name] = parameter.Update
	}
	if updates["client_session_id"] != "once" || updates["request_id"] != "each_iteration" {
		t.Fatalf("updates = %v, want the init-phase session once and the action-only request id per iteration", updates)
	}
	if warning := report.Warning(); !strings.Contains(warning, "client_session_id once per virtual user") || !strings.Contains(warning, "request_id each iteration") {
		t.Fatalf("warning = %q", warning)
	}
}
