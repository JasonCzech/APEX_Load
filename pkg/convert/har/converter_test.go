package har

import "testing"

func TestConvertHAR(t *testing.T) {
	result, err := Convert([]byte(`{
  "log": {
    "entries": [
      {
        "startedDateTime": "2026-01-01T00:00:00Z",
        "time": 100,
        "request": {
          "method": "POST",
          "url": "https://api.example.test/session",
          "headers": [{"name": "Content-Type", "value": "application/json"}],
          "postData": {"text": "{\"username\":\"ada\"}"}
        },
        "response": {
          "status": 201,
          "headers": [{"name": "Content-Type", "value": "application/json"}],
          "content": {"text": "{\"sessionId\":\"abc12345\"}"}
        }
      },
      {
        "startedDateTime": "2026-01-01T00:00:02Z",
        "time": 50,
        "request": {
          "method": "GET",
          "url": "https://api.example.test/session/abc12345/items",
          "headers": []
        },
        "response": {"status": 200, "content": {"text": "{\"items\":[]}"}}
      }
    ]
  }
}`), ConvertOptions{ScriptName: "HAR Import"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Script.Name != "HAR Import" {
		t.Fatalf("script name = %q, want HAR Import", result.Script.Name)
	}
	if result.Script.Config.BaseURL != "https://api.example.test" {
		t.Fatalf("base_url = %q", result.Script.Config.BaseURL)
	}
	if result.Stats.ConvertedActions != 2 {
		t.Fatalf("converted_actions = %d, want 2", result.Stats.ConvertedActions)
	}
	if result.Script.Actions[1].Target != "/session/{{var.corr_sessionid}}/items" {
		t.Fatalf("correlated target = %q", result.Script.Actions[1].Target)
	}
	if len(result.Correlations) == 0 {
		t.Fatal("expected detected correlations")
	}
}

func TestConvertHARRequiresContent(t *testing.T) {
	_, err := Convert(nil, ConvertOptions{})
	if err == nil {
		t.Fatal("expected error for empty HAR content")
	}
}
