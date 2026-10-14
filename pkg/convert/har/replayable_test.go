package har

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// A correlated number in a JSON body becomes an unquoted variable reference;
// the request stays replayable (a recorded Tableau event body was blocked as
// malformed JSON).
func TestConvertHARKeepsCorrelatedJSONNumbersReplayable(t *testing.T) {
	result, err := Convert([]byte(`{"log":{"entries":[
 {"startedDateTime":"2026-01-01T00:00:00Z","time":10,"request":{"method":"GET","url":"https://app.example.test/api/session","headers":[]},
  "response":{"status":200,"headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","text":"{\"currentProjectId\":1249177,\"name\":\"x\"}"}}},
 {"startedDateTime":"2026-01-01T00:00:01Z","time":10,"request":{"method":"POST","url":"https://app.example.test/api/report","headers":[{"name":"Content-Type","value":"application/json"}],"postData":{"mimeType":"application/json","text":"{\"event\":{\"ProjectId\":1249177,\"Kind\":\"Bootstrap\"}}"}},
  "response":{"status":200,"headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","text":"{}"}}}
]}}`), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report := result.Script.Actions[1]
	if !strings.Contains(report.Body, `"ProjectId":{{var.`) {
		t.Fatalf("body = %s, want the project id correlated", report.Body)
	}
	if blockers := report.Metadata["execution_blockers"]; blockers != nil {
		t.Fatalf("correlated JSON body is blocked: %v", blockers)
	}
}

// A form field named for a property of a session (Tableau's
// :session_feature_flags, a JSON object of feature toggles, one of them
// "SalesforceSingleUseRefreshToken":false) is not a credential, so its
// recorded value stays in the script instead of becoming a secret to
// configure.
func TestConvertHARKeepsSessionPropertyFieldsRecorded(t *testing.T) {
	var flags []string
	for i := 0; i < 140; i++ {
		flags = append(flags, fmt.Sprintf(`"FeatureToggleNumber%03d":true`, i))
	}
	flags = append(flags, `"SalesforceSingleUseRefreshToken":false`, `"LegacyAuthToken":null`)
	value := url.QueryEscape("{" + strings.Join(flags, ",") + "}")
	body := "sheet_id=Global&%3Asession_feature_flags=" + value + "&locale=en_US"
	content, _ := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{map[string]any{
		"startedDateTime": "2026-01-01T00:00:00Z", "time": 10,
		"request": map[string]any{"method": "POST", "url": "https://viz.example.test/vizql/bootstrapSession", "headers": []any{map[string]any{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
			"postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": body}},
		"response": map[string]any{"status": 200, "headers": []any{}, "content": map[string]any{"text": ""}},
	}}}})
	result, err := Convert(content, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	if strings.Contains(action.Body, "{{secret.") || !strings.Contains(action.Body, value) {
		t.Fatalf("body = %.200s, want the recorded flags kept", action.Body)
	}
	if blockers := action.Metadata["execution_blockers"]; blockers != nil {
		t.Fatalf("form body is blocked: %v", blockers)
	}
}
