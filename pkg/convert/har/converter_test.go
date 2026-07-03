package har

import (
	"strings"
	"testing"
)

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
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatalf("source review = %#v, want mappings", result.SourceReview)
	}
	foundActionMapping := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetPath == "actions[0]" && mapping.Status == "converted" {
			foundActionMapping = true
			break
		}
	}
	if !foundActionMapping {
		t.Fatalf("missing action source mapping in %#v", result.SourceReview.Mappings)
	}
}

func TestConvertHARRequiresContent(t *testing.T) {
	_, err := Convert(nil, ConvertOptions{})
	if err == nil {
		t.Fatal("expected error for empty HAR content")
	}
}

func TestConvertHARSurfacesSampleDiagnostics(t *testing.T) {
	result, err := Convert([]byte(`{
  "log": {
    "entries": [{
      "request": {
        "method": "POST",
        "url": "https://api.example.test/upload",
        "headers": [],
        "postData": {
          "mimeType": "multipart/form-data",
          "params": [{"name": "file", "fileName": "payload.bin"}]
        }
      },
      "response": {"status": 200, "content": {"text": "{}"}}
    }]
  }
}`), ConvertOptions{InferExtractors: boolPtr(false)})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(result.Unsupported) == 0 {
		t.Fatal("expected HAR diagnostics to surface through wrapper")
	}
	if !strings.Contains(strings.Join(result.Unsupported.Strings(), "\n"), "HAR multipart file") {
		t.Fatalf("diagnostics = %#v, want multipart file warning", result.Unsupported)
	}
}

func TestConvertHARScrubsGovernedSourceReview(t *testing.T) {
	result, err := Convert([]byte(`{
  "log": {
    "entries": [{
      "request": {
        "method": "POST",
        "url": "https://api.example.test/login?access_token=super-secret-token",
        "headers": [
          {"name": "Authorization", "value": "Bearer super-secret-token"},
          {"name": "Content-Type", "value": "application/json"}
        ],
        "cookies": [{"name": "sid", "value": "cookie-secret"}],
        "postData": {"text": "{\"password\":\"secret\",\"email\":\"ada@example.test\"}"}
      },
      "response": {
        "status": 200,
        "headers": [{"name": "Set-Cookie", "value": "sid=cookie-secret"}],
        "content": {"text": "{\"ok\":true}"}
      }
    }]
  }
}`), ConvertOptions{ScriptName: "Scrubbed HAR"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Governance == nil || !result.Governance.RedactionEnabled || result.Governance.Sensitivity != "scrubbed" {
		t.Fatalf("governance = %#v, want scrubbed redaction", result.Governance)
	}
	if result.Governance.RetentionDays != DefaultRetentionDays {
		t.Fatalf("retention days = %d, want %d", result.Governance.RetentionDays, DefaultRetentionDays)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 {
		t.Fatalf("source review = %#v, want scrubbed HAR source", result.SourceReview)
	}
	source := result.SourceReview.SourceFiles[0].Content
	for _, secret := range []string{"super-secret-token", "cookie-secret", "\"password\":\"secret\""} {
		if strings.Contains(source, secret) {
			t.Fatalf("source review leaked %q in %s", secret, source)
		}
	}
}

func TestConvertHARCorrelatesRawBeforeScrub(t *testing.T) {
	result, err := Convert([]byte(`{
  "log": {
    "entries": [
      {
        "request": {"method": "POST", "url": "https://api.example.test/login", "headers": []},
        "response": {
          "status": 200,
          "headers": [{"name": "Content-Type", "value": "application/json"}],
          "content": {"text": "{\"access_token\":\"tok-secret-abcdef123456\"}"}
        }
      },
      {
        "request": {
          "method": "GET",
          "url": "https://api.example.test/orders?access_token=tok-secret-abcdef123456",
          "headers": [{"name": "Authorization", "value": "Bearer tok-secret-abcdef123456"}]
        },
        "response": {"status": 200, "content": {"text": "{\"ok\":true}"}}
      }
    ]
  }
}`), ConvertOptions{ScriptName: "Raw Correlation"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(result.Correlations) != 1 {
		t.Fatalf("correlations = %#v, want one raw token correlation", result.Correlations)
	}
	if result.Correlations[0].SampleValue == "[REDACTED]" {
		t.Fatalf("correlation was detected from scrubbed token: %#v", result.Correlations[0])
	}
	if !strings.Contains(result.Script.Actions[1].Target, "{{var.corr_access_token}}") {
		t.Fatalf("second action target = %q, want raw token parameterized", result.Script.Actions[1].Target)
	}
	if result.Governance == nil || result.Governance.Sensitivity != "scrubbed" {
		t.Fatalf("governance = %#v, want scrubbed marker", result.Governance)
	}
	source := result.SourceReview.SourceFiles[0].Content
	if strings.Contains(source, "tok-secret-abcdef123456") {
		t.Fatalf("source review leaked raw token: %s", source)
	}
}

func TestDiffClassifiesDynamicHARCorrelations(t *testing.T) {
	baseline := []byte(dynamicHAR("cart-111111"))
	comparison := []byte(dynamicHAR("cart-222222"))
	report, err := Diff(baseline, comparison, ConvertOptions{ScriptName: "Checkout Pair"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !report.Compared || report.ArtifactMode != ArtifactModeDesignHARPair {
		t.Fatalf("report = %#v, want design HAR pair", report)
	}
	if report.DynamicCandidates != 1 || report.StaticCandidates != 0 {
		t.Fatalf("candidate counts = dynamic %d static %d, want 1/0", report.DynamicCandidates, report.StaticCandidates)
	}
	if len(report.Classifications) == 0 || report.Classifications[0].Classification != DiffClassificationDynamic {
		t.Fatalf("classifications = %#v, want dynamic first", report.Classifications)
	}
	if report.Classifications[0].DiffConfidence < 0.9 {
		t.Fatalf("confidence = %f, want high confidence", report.Classifications[0].DiffConfidence)
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func dynamicHAR(cartID string) string {
	return `{
  "log": {
    "entries": [
      {
        "startedDateTime": "2026-01-01T00:00:00Z",
        "time": 100,
        "request": {"method": "POST", "url": "https://shop.example.test/cart", "headers": []},
        "response": {"status": 201, "content": {"mimeType": "application/json", "text": "{\"cartId\":\"` + cartID + `\"}"}}
      },
      {
        "startedDateTime": "2026-01-01T00:00:01Z",
        "time": 50,
        "request": {"method": "POST", "url": "https://shop.example.test/cart/` + cartID + `/checkout", "headers": []},
        "response": {"status": 200, "content": {"mimeType": "application/json", "text": "{\"ok\":true}"}}
      }
    ]
  }
}`
}
