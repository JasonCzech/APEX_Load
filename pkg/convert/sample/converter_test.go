package sample

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// headerListContentType returns the (last) Content-Type value from an ordered
// HeaderList, failing the test if none is present.
func headerListContentType(t *testing.T, list dsl.HeaderList) string {
	t.Helper()
	value := ""
	found := false
	for _, h := range list {
		if strings.EqualFold(h.Name, "Content-Type") {
			value = h.Value
			found = true
		}
	}
	if !found {
		t.Fatalf("HeaderList has no Content-Type entry: %#v", list)
	}
	return value
}

func TestApplyCorrelationVarsToHeaderListPreservesHeaderNames(t *testing.T) {
	const token = "dynamic-token-123456"
	headers := dsl.HeaderList{{Name: "X-" + token, Value: "Bearer " + token}}
	got := applyCorrelationVarsToHeaderList(headers, map[string]string{token: "{{var.corr_token}}"})
	if len(got) != 1 {
		t.Fatalf("headers = %#v, want one entry", got)
	}
	if got[0].Name != "X-"+token {
		t.Fatalf("header name = %q, want authored name preserved", got[0].Name)
	}
	if got[0].Value != "Bearer {{var.corr_token}}" {
		t.Fatalf("header value = %q, want correlation replacement", got[0].Value)
	}
}

func TestConvertSensitiveHeadersUsesSharedWholeValueProjection(t *testing.T) {
	tests := []struct {
		name   string
		header string
		value  string
		want   string
	}{
		{
			name:   "authorization template plus residual literal",
			header: "Authorization",
			value:  "Bearer {{var.corr_token}} residual-secret",
			want:   "Bearer {{var.corr_token}} residual-secret",
		},
		{
			name:   "cookie template plus residual literal",
			header: "Cookie",
			value:  "session={{var.corr_session}}; admin=literal-credential",
			want:   "session={{var.corr_session}}; admin=literal-credential",
		},
		{
			name:   "authorization scheme plus unproven token",
			header: "Authorization",
			value:  "Bearer {{var.corr_token}}",
			want:   "Bearer {{var.corr_token}}",
		},
		{
			name:   "composed cookie values are governed as one value",
			header: "Cookie",
			value:  "session={{var.corr_session}}; csrf={{secret.csrf}}",
			want:   "session={{var.corr_session}}; csrf={{secret.csrf}}",
		},
		{
			name:   "whole value governed",
			header: "X-API-Key",
			value:  "{{secret.api_key}}",
			want:   "{{secret.API_KEY}}",
		},
		{
			name:   "authorization malformed builtin arguments",
			header: "Authorization",
			value:  "Bearer {{fn.random(1,literal-secret)}}",
			want:   "Bearer {{fn.random(1,literal-secret)}}",
		},
		{
			name:   "cookie unsupported builtin call",
			header: "Cookie",
			value:  "session={{fake.email(literal-secret)}}",
			want:   "session={{fake.email(literal-secret)}}",
		},
		{
			name:   "authorization composed builtin",
			header: "Authorization",
			value:  "Bearer {{fn.random_string(32)}}",
			want:   "Bearer {{fn.random_string(32)}}",
		},
		{
			name:   "cookie composed builtins",
			header: "Cookie",
			value:  "nonce={{fn.uuid()}}; shard={{fn.random(1,4)}}",
			want:   "nonce={{fn.uuid()}}; shard={{fn.random(1,4)}}",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inferExtractors := false
			result, err := Convert(ImportRequest{
				Format: "rest_json",
				Content: fmt.Sprintf(
					`{"request":{"method":"GET","url":"https://api.example.com","headers":{%q:%q}}}`,
					test.header,
					test.value,
				),
				Options: ConvertOptions{InferExtractors: &inferExtractors},
			})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if got := result.Script.Actions[0].Headers[test.header]; got != test.want {
				t.Fatalf("header = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConvertRESTJSON(t *testing.T) {
	includeAssertions := true
	inferExtractors := true
	result, err := Convert(ImportRequest{
		Name:   "Order Flow",
		Format: "rest_json",
		Content: `{
			"requests": [
				{
					"request": {
						"method": "POST",
						"url": "https://api.example.com/v1/login",
						"headers": {"Content-Type": "application/json", "Authorization": "Bearer top-secret"},
						"body": {"username": "alice", "password": "secret"}
					},
					"response": {
						"status": 200,
						"headers": {"Content-Type": "application/json"},
						"body": {"token": "abc123token", "userId": 91234}
					}
				},
				{
					"request": {
						"method": "GET",
						"url": "https://api.example.com/v1/users/91234/orders",
						"headers": {"Authorization": "Bearer abc123token"}
					},
					"response": {
						"status": 200,
						"body": {"orders": []}
					}
				}
			]
		}`,
		Options: ConvertOptions{
			IncludeAssertions: &includeAssertions,
			InferExtractors:   &inferExtractors,
		},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://api.example.com" {
		t.Fatalf("base URL = %q, want https://api.example.com", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Headers["Authorization"]; got != "Bearer top-secret" {
		t.Fatalf("authorization header = %q, want imported executable value", got)
	}
	if got := result.Script.Actions[0].Body; !strings.Contains(got, `"password":"secret"`) {
		t.Fatalf("request body lost imported password: %q", got)
	}
	if len(result.Script.Actions[0].Assertions) != 1 {
		t.Fatalf("first action assertions = %d, want 1", len(result.Script.Actions[0].Assertions))
	}
	if len(result.Script.Actions[0].Extractors) == 0 {
		t.Fatal("expected first action to include inferred extractors")
	}
	if result.Script.Actions[0].Extractors[0].OnMiss != "fail" {
		t.Fatalf("generated extractor on_miss = %q, want fail", result.Script.Actions[0].Extractors[0].OnMiss)
	}
	if got := result.Script.Actions[1].Target; !strings.Contains(got, "{{var.corr_userid}}") {
		t.Fatalf("expected correlated user ID in second target, got %q", got)
	}
	if result.Stats.CorrelationsDetected == 0 {
		t.Fatal("expected correlation stats to be populated")
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 || len(result.SourceReview.Mappings) == 0 {
		t.Fatalf("source review = %#v, want files and mappings", result.SourceReview)
	}
	assertSampleSourceMapping(t, result.SourceReview.Mappings, "rest_json_request", "actions[0]", "converted")
	assertSampleSourceMapping(t, result.SourceReview.Mappings, "response_status", "actions[0].assertions[0]", "contributor")
	if !strings.Contains(result.SourceReview.SourceFiles[0].Content, "top-secret") || !strings.Contains(result.SourceReview.SourceFiles[0].Content, "secret\"") {
		t.Fatalf("source review lost imported content: %s", result.SourceReview.SourceFiles[0].Content)
	}
}

func TestConvertRESTJSONPreservesSensitiveNumericValueWithoutStringCoercion(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "rest_json",
		Content: `{"request":{"method":"POST","url":"https://api.example.com/login",
			"body":{"password":"abc\"residual-secret","passcode":123456}},
			"response":{"status":200}}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	body := result.Script.Actions[0].Body
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode preserved body: %v", err)
	}
	if decoded["password"] != `abc"residual-secret` || decoded["passcode"] != float64(123456) {
		t.Fatalf("preserved JSON values = %#v", decoded)
	}
	if _, ok := decoded["passcode"].(string); ok {
		t.Fatalf("sensitive numeric value was coerced to string: %s", body)
	}
}

func TestConvertRelativeTargetDoesNotTrimLookalikeHost(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "rest_json",
		Content: `{
			"requests": [
				{"method": "GET", "url": "https://api.example.com/login"},
				{"method": "GET", "url": "https://api.example.com.attacker.test/callback"}
			]
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[0].Target; got != "/login" {
		t.Fatalf("first target = %q, want /login", got)
	}
	if got := result.Script.Actions[1].Target; got != "https://api.example.com.attacker.test/callback" {
		t.Fatalf("second target = %q, want absolute lookalike host preserved", got)
	}
}

func TestConvertRESTJSONResponseStatusValidation(t *testing.T) {
	inferExtractors := false
	tests := []struct {
		name    string
		status  string
		wantErr bool
	}{
		{name: "numeric string", status: `"201"`},
		{name: "omitted", status: `null`},
		{name: "invalid string", status: `"OK"`, wantErr: true},
		{name: "fractional", status: `200.5`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := `{
				"request": {"method":"GET","url":"https://api.example.com/orders"},
				"response": {"status": ` + tt.status + `}
			}`
			result, err := Convert(ImportRequest{
				Format:  "rest_json",
				Content: content,
				Options: ConvertOptions{InferExtractors: &inferExtractors},
			})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Convert returned nil error, result=%#v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
		})
	}
}

func TestConvertCurlBasicAuthEncodesCredential(t *testing.T) {
	sanitizeSensitive := false
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -u alice:secret https://api.example.com/orders`,
		Options: ConvertOptions{SanitizeSensitive: &sanitizeSensitive, InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:secret"))
	if got := result.Script.Actions[0].Headers["Authorization"]; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

func TestConvertCurlUnsupportedOptionIsDurablyGatedAndDisclosureSafe(t *testing.T) {
	const canary = "curl-option-canary-5f84e20c"
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl --proxy-user=` + canary + ` https://api.example.com/orders`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "unsupported_curl_option" {
		t.Fatalf("execution blocker = %#v", action.Metadata["execution_blocker"])
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v", action.Metadata["import_review"])
	}
	reasons, ok := review["reason_codes"].([]string)
	if !ok || !slices.Contains(reasons, "unsupported_curl_option") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(publicJSON), canary) {
		t.Fatalf("unsupported option value was not preserved in converter JSON: %s", publicJSON)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked cURL draft validation issues = %v", issues)
	}
}

func TestConvertCurlSanitizesAndGatesInvalidMultipartMetadata(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "curl",
		Content: "curl -F 'label\r\nX-Injected: yes=value;type=text/plain\r\nX-Other: yes' " +
			"https://api.example.com/upload",
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if strings.Contains(action.Body, "\r\nX-Injected: yes") ||
		strings.Contains(action.Body, "\r\nX-Other: yes") {
		t.Fatalf("cURL multipart metadata injected part headers: %q", action.Body)
	}
	if !strings.Contains(action.Body, `name="label%0D%0AX-Injected: yes"`) {
		t.Fatalf("sanitized multipart body = %q", action.Body)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v", action.Metadata["import_review"])
	}
	reasons, ok := review["reason_codes"].([]string)
	if !ok || !slices.Contains(reasons, "invalid_curl_multipart_metadata") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked cURL multipart draft validation issues = %v", issues)
	}
}

func TestConvertCurl(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -X POST https://api.example.com/v1/orders -H 'Content-Type: application/json' -d '{"item":"widget","qty":5}'`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	if result.Script.Actions[0].Method != "POST" {
		t.Fatalf("method = %q, want POST", result.Script.Actions[0].Method)
	}
	if result.Script.Actions[0].Target != "/v1/orders" {
		t.Fatalf("target = %q, want /v1/orders", result.Script.Actions[0].Target)
	}
	if got := result.Script.Actions[0].Headers["Content-Type"]; got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	if !strings.Contains(result.Script.Actions[0].Body, `"qty":5`) {
		t.Fatalf("body = %q, want request payload preserved", result.Script.Actions[0].Body)
	}
	if result.Script.Config.BaseURL != "https://api.example.com" {
		t.Fatalf("base URL = %q, want https://api.example.com", result.Script.Config.BaseURL)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected missing correlation warning for single request sample")
	}
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("converted actions = %d, want 1", result.Stats.ConvertedActions)
	}
	if result.SourceReview == nil {
		t.Fatal("expected source review")
	}
	assertSampleSourceMapping(t, result.SourceReview.Mappings, "curl_command", "actions[0]", "converted")
}

func assertSampleSourceMapping(t *testing.T, mappings []sourceview.Mapping, function string, targetPath string, status string) {
	t.Helper()
	for _, mapping := range mappings {
		if mapping.Function == function && mapping.TargetPath == targetPath && mapping.Status == status {
			return
		}
	}
	t.Fatalf("missing source mapping function=%s target=%s status=%s in %#v", function, targetPath, status, mappings)
}

func TestConvertCurlGetMovesDataToQueryAndURLEncodes(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -G https://api.example.com/search --data-urlencode 'q=red widgets' --data-urlencode 'sort=created desc'`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Method != "GET" {
		t.Fatalf("method = %q, want GET", action.Method)
	}
	if action.Body != "" {
		t.Fatalf("body = %q, want empty body for -G", action.Body)
	}
	if action.Target != "/search?q=red+widgets&sort=created+desc" {
		t.Fatalf("target = %q", action.Target)
	}
}

func TestConvertCurlJSONRefererAndMaxTime(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl https://api.example.com/v1/orders --json '{"sku":"ABC"}' -e https://shop.example.com/cart --max-time 2.5`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" {
		t.Fatalf("method = %q, want POST", action.Method)
	}
	if action.Headers["Content-Type"] != "application/json" || action.Headers["Accept"] != "application/json" {
		t.Fatalf("headers = %#v", action.Headers)
	}
	if action.Headers["Referer"] != "https://shop.example.com/cart" {
		t.Fatalf("referer = %q", action.Headers["Referer"])
	}
	if action.TimeoutMs != 2500 {
		t.Fatalf("timeout = %d, want 2500", action.TimeoutMs)
	}
	if action.Body != `{"sku":"ABC"}` {
		t.Fatalf("body = %q", action.Body)
	}
}

func TestConvertCurlSOAP(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -X POST https://example.com/services/quotes -H 'Content-Type: text/xml; charset=utf-8' -H 'SOAPAction: urn:GetQuote' -d '<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><GetQuote xmlns="urn:quotes"><Symbol>STORM</Symbol></GetQuote></soapenv:Body></soapenv:Envelope>'`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "soap" {
		t.Fatalf("protocol = %q, want soap", result.Script.Protocol)
	}
	if result.Script.Config.SOAP.Version != "1.1" {
		t.Fatalf("soap version = %q, want 1.1", result.Script.Config.SOAP.Version)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.SOAP == nil {
		t.Fatal("expected SOAP action metadata")
	}
	if action.SOAP.Action != "urn:GetQuote" {
		t.Fatalf("soap action = %q, want urn:GetQuote", action.SOAP.Action)
	}
	if action.SOAP.BodyMode != "raw_xml" {
		t.Fatalf("body mode = %q, want raw_xml", action.SOAP.BodyMode)
	}
	if action.Target != "/services/quotes" {
		t.Fatalf("target = %q, want /services/quotes", action.Target)
	}
}

func TestConvertCurlMultipartFormReportsFileAndUnsupportedFlags(t *testing.T) {
	sanitizeSensitive := false
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl --compressed -F 'description=quarterly report' -F 'file=@/tmp/report.pdf;type=application/pdf' https://api.example.com/upload`,
		Options: ConvertOptions{SanitizeSensitive: &sanitizeSensitive, InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" {
		t.Fatalf("method = %q, want POST", action.Method)
	}
	if action.Headers["Content-Type"] != "multipart/form-data; boundary=----apexload-curl-boundary" {
		t.Fatalf("content type = %q", action.Headers["Content-Type"])
	}
	if action.Headers["Accept-Encoding"] != "gzip, deflate, br" {
		t.Fatalf("accept encoding = %q, want compressed cURL header", action.Headers["Accept-Encoding"])
	}
	for _, want := range []string{
		`name="description"`,
		"quarterly report",
		`name="file"; filename="report.pdf"`,
		"Content-Type: application/pdf",
		"{{file.file}}",
	} {
		if !strings.Contains(action.Body, want) {
			t.Fatalf("multipart body = %q, want %q", action.Body, want)
		}
	}
	if !containsSampleDiagnostic(result, "cURL multipart file") {
		t.Fatalf("diagnostics = %#v, want cURL multipart file warning", result.Unsupported)
	}
}

func TestConvertCurlMultipartUsesExistingBoundary(t *testing.T) {
	sanitizeSensitive := false
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -H 'Content-Type: multipart/form-data; boundary=----custom-boundary' -F 'file=@/tmp/report.pdf;type=application/pdf' https://api.example.com/upload`,
		Options: ConvertOptions{SanitizeSensitive: &sanitizeSensitive, InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if got := action.Headers["Content-Type"]; got != "multipart/form-data; boundary=----custom-boundary" {
		t.Fatalf("Content-Type = %q, want existing boundary preserved", got)
	}
	if !strings.Contains(action.Body, "------custom-boundary\r\n") || !strings.Contains(action.Body, "------custom-boundary--\r\n") {
		t.Fatalf("multipart body = %q, want custom boundary", action.Body)
	}
}

// TestConvertCurlMultipartSynthesizesBoundaryInHeaderList guards B1: when the
// recorded Content-Type is multipart but carries no boundary, the synthesized
// boundary must land in the ordered HeaderList (which replay prefers), not only
// in the legacy Headers map — otherwise the bare HeaderList Content-Type shadows
// the fix and the body replays as unparseable.
func TestConvertCurlMultipartSynthesizesBoundaryInHeaderList(t *testing.T) {
	sanitizeSensitive := false
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -H 'Content-Type: multipart/form-data' -F 'file=@/tmp/report.pdf;type=application/pdf' https://api.example.com/upload`,
		Options: ConvertOptions{SanitizeSensitive: &sanitizeSensitive, InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	listCT := headerListContentType(t, action.HeaderList)
	if !strings.Contains(listCT, "boundary=") {
		t.Fatalf("HeaderList Content-Type = %q, want a synthesized boundary (replay uses HeaderList)", listCT)
	}
	if listCT != action.Headers["Content-Type"] {
		t.Fatalf("HeaderList Content-Type %q out of sync with map %q", listCT, action.Headers["Content-Type"])
	}
	_, params, _ := mime.ParseMediaType(listCT)
	if b := params["boundary"]; b == "" || !strings.Contains(action.Body, "--"+b+"\r\n") {
		t.Fatalf("body does not use the HeaderList boundary %q: %q", b, action.Body)
	}
}

func TestConvertCurlDataFileAndGetContentTypeCleanup(t *testing.T) {
	inferExtractors := false
	fileResult, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl --data-binary @/tmp/payload.json https://api.example.com/upload`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert data file returned error: %v", err)
	}
	if fileResult.Script.Actions[0].Body != "{{file.payload}}" {
		t.Fatalf("data file body = %q, want file placeholder", fileResult.Script.Actions[0].Body)
	}
	if len(fileResult.Script.DataSources) != 1 {
		t.Fatalf("data sources = %#v, want one file asset", fileResult.Script.DataSources)
	}
	if got := fileResult.Script.DataSources[0]; got.Name != "payload" || got.Type != "file" || got.FileName != "payload.json" {
		t.Fatalf("file data source = %#v, want payload file asset metadata", got)
	}
	if !containsSampleDiagnostic(fileResult, "cURL data file") {
		t.Fatalf("diagnostics = %#v, want cURL data file warning", fileResult.Unsupported)
	}

	getResult, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -G -H 'Content-Type: application/json' --data 'q=widgets' https://api.example.com/search`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert -G returned error: %v", err)
	}
	action := getResult.Script.Actions[0]
	if action.Method != "GET" || action.Body != "" || action.Target != "/search?q=widgets" {
		t.Fatalf("GET action = method %q target %q body %q", action.Method, action.Target, action.Body)
	}
	if action.Headers["Content-Type"] != "" || len(headerListValues(action.HeaderList, "Content-Type")) != 0 {
		t.Fatalf("Content-Type should be stripped for -G GET: headers=%#v header_list=%#v", action.Headers, action.HeaderList)
	}
}

func TestConvertBatchRawHTTP(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "raw_http",
		Content: strings.Join([]string{
			"POST /v1/session HTTP/1.1\nHost: api.example.com\nContent-Type: application/json\n\n{\"username\":\"demo\",\"password\":\"secret\"}",
			"GET /v1/session/session-12345/orders HTTP/1.1\nHost: api.example.com\nAccept: application/json",
		}, rawHTTPRequestDelimiter),
		ResponseContent: strings.Join([]string{
			"HTTP/1.1 201 Created\nContent-Type: application/json\n\n{\"sessionId\":\"session-12345\"}",
			"HTTP/1.1 200 OK\nContent-Type: application/json\n\n{\"orders\":[]}",
		}, rawHTTPResponseDelimiter),
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	if result.Script.Actions[1].Target != "/v1/session/{{var.corr_sessionid}}/orders" {
		t.Fatalf("second target = %q, want correlated target", result.Script.Actions[1].Target)
	}
	if result.Stats.Assertions != 2 {
		t.Fatalf("assertions = %d, want 2", result.Stats.Assertions)
	}
	if result.Stats.CorrelationsDetected != 1 {
		t.Fatalf("correlations = %d, want 1", result.Stats.CorrelationsDetected)
	}
}

func TestConvertRawHTTPPreservesBinaryRequestBodyAndCaseInsensitiveScheme(t *testing.T) {
	rawBody := string([]byte{'a', 0xff, 'b'})
	result, err := Convert(ImportRequest{
		Format:  "raw_http",
		Content: "POST HTTP://example.com/upload HTTP/1.1\r\nContent-Type: application/octet-stream\r\n\r\n" + rawBody,
	})
	if err != nil {
		t.Fatalf("Convert raw binary request: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Target != "/upload" {
		t.Fatalf("uppercase absolute-form target = %q, want /upload", action.Target)
	}
	if action.BodyEncoding != "base64" {
		t.Fatalf("binary raw body encoding = %q, want base64", action.BodyEncoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(action.Body)
	if err != nil || string(decoded) != rawBody {
		t.Fatalf("binary raw body round trip = %q, %v", decoded, err)
	}
}

func TestConvertRawHTTPUsesFirstHeaderBodyBoundary(t *testing.T) {
	const body = "prefix\r\n\r\nsuffix"
	result, err := Convert(ImportRequest{
		Format:  "raw_http",
		Content: "POST https://example.test/upload HTTP/1.1\nContent-Type: text/plain\n\n" + body,
	})
	if err != nil {
		t.Fatalf("Convert raw HTTP with CRLF sequence in body: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one", result.Script.Actions)
	}
	if got := result.Script.Actions[0].Body; got != body {
		t.Fatalf("body = %q, want earliest LF header boundary to preserve %q", got, body)
	}
}

func TestConvertRawHTTPAcceptsMixedHeaderBodyLineEndings(t *testing.T) {
	for _, boundary := range []string{"\r\n\n", "\n\r\n"} {
		t.Run(fmt.Sprintf("%q", boundary), func(t *testing.T) {
			result, err := Convert(ImportRequest{
				Format:  "raw_http",
				Content: "POST https://example.test/upload HTTP/1.1\nContent-Type: text/plain" + boundary + "payload",
			})
			if err != nil {
				t.Fatalf("Convert mixed-line-ending raw HTTP: %v", err)
			}
			if got := result.Script.Actions[0].Body; got != "payload" {
				t.Fatalf("body = %q, want payload after mixed boundary", got)
			}
		})
	}
}

func TestConvertRawHTTPPreservesRepeatedResponseHeaderOrdinalForCorrelation(t *testing.T) {
	const secondToken = "raw-session-token-second-123456"
	result, err := Convert(ImportRequest{
		Format: "raw_http",
		Content: strings.Join([]string{
			"GET /bootstrap HTTP/1.1\nHost: api.example.com",
			"GET /use?echo=" + secondToken + " HTTP/1.1\nHost: api.example.com",
		}, rawHTTPRequestDelimiter),
		ResponseContent: strings.Join([]string{
			"HTTP/1.1 200 OK\nX-Session-Token: raw-session-token-first-123456\nX-Session-Token: " + secondToken + "\n\n{}",
			"HTTP/1.1 200 OK\nContent-Type: application/json\n\n{}",
		}, rawHTTPResponseDelimiter),
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Correlations) != 1 || !result.Correlations[0].PreApplied {
		t.Fatalf("correlations = %#v, want the reused second header occurrence applied", result.Correlations)
	}
	extractors := result.Script.Actions[0].Extractors
	if len(extractors) != 1 || extractors[0].Type != "regex" || !strings.Contains(extractors[0].Expression, "{1}") || extractors[0].OnMiss != "fail" {
		t.Fatalf("source extractors = %#v, want fail-closed second-occurrence header regex", extractors)
	}
	if got := result.Script.Actions[1].Target; got != "/use?echo={{var.corr_x_session_token}}" {
		t.Fatalf("consumer target = %q, want second header token correlated", got)
	}
}

func TestConvertRawHTTPUnfoldsHeadersAndDecodesChunkedBody(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "raw_http",
		Content: strings.Join([]string{
			"POST /submit HTTP/1.0",
			"Host: api.example.com",
			"X-Trace: first",
			"\tsecond",
			"Transfer-Encoding: chunked",
			"Content-Length: 999",
			"",
			"4",
			"Wiki",
			"5",
			"pedia",
			"0",
			"",
			"",
		}, "\r\n"),
		ResponseContent: strings.Join([]string{
			"HTTP/1.1 200 OK",
			"Transfer-Encoding: chunked",
			"",
			"2",
			"ok",
			"0",
			"",
			"",
		}, "\r\n"),
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Body != "Wikipedia" {
		t.Fatalf("body = %q, want decoded chunked body", action.Body)
	}
	if action.Headers["Transfer-Encoding"] != "" {
		t.Fatalf("Transfer-Encoding header should be removed after decoding: %#v", action.Headers)
	}
	if action.Headers["Content-Length"] != "" {
		t.Fatalf("stale Content-Length header should be removed after decoding: %#v", action.Headers)
	}
	if action.Headers["X-Trace"] != "first second" {
		t.Fatalf("X-Trace = %q, want unfolded value", action.Headers["X-Trace"])
	}
	if got := headerListValues(action.HeaderList, "X-Trace"); strings.Join(got, ",") != "first second" {
		t.Fatalf("header_list X-Trace = %#v, want unfolded value", got)
	}
	if action.Metadata["http_version"] != "HTTP/1.0" {
		t.Fatalf("metadata = %#v, want HTTP/1.0", action.Metadata)
	}
	if !sampleHasFindingCode(t, result.Script, "raw_http_version_review_required") {
		t.Fatalf("HTTP/1.0 request was not durably review-gated: %#v", action.Metadata)
	}
	if got := action.Assertions[0].Expected; got != 200 {
		t.Fatalf("assertion expected = %#v, want 200 from decoded response", got)
	}
}

func TestConvertRawHTTPAsteriskTargetIsDurablyBlocked(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "raw_http",
		Content: strings.Join([]string{
			"OPTIONS * HTTP/1.1",
			"Host: api.example.test",
			"",
			"",
		}, "\r\n"),
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %#v", issues)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.Method != "OPTIONS" || action.Metadata["raw_request_target"] != "*" {
		t.Fatalf("action = %#v, want preserved OPTIONS asterisk evidence", action)
	}
	if !sampleHasFindingCode(t, result.Script, "raw_http_request_target_review_required") {
		t.Fatalf("asterisk-form request was not durably review-gated: %#v", action.Metadata)
	}
}

func sampleHasFindingCode(t *testing.T, script *dsl.Script, want string) bool {
	t.Helper()
	findings, err := importreview.FindingsFromScript(script)
	if err != nil {
		t.Fatalf("FindingsFromScript: %v", err)
	}
	for _, finding := range findings {
		if finding.Code == want || finding.ReasonCode == want {
			return true
		}
	}
	return false
}

func TestConvertRawHTTPParsingIsBoundedAndFailClosed(t *testing.T) {
	inferExtractors := false
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "malformed header",
			content: "GET / HTTP/1.1\nHost: api.example.test\nnot-a-header\n\n",
		},
		{
			name:    "orphan continuation",
			content: "GET / HTTP/1.1\n continuation\n\n",
		},
		{
			name:    "extra request-line field",
			content: "GET / HTTP/1.1 unexpected\nHost: api.example.test\n\n",
		},
		{
			name: "too many header lines",
			content: "GET / HTTP/1.1\nHost: api.example.test\n" +
				strings.Repeat("X-Test: value\n", maxRawHTTPHeaderLines) + "\n",
		},
		{
			name: "too many messages",
			content: "GET / HTTP/1.1\nHost: api.example.test\n\n" +
				strings.Repeat(rawHTTPRequestDelimiter, maxRawHTTPMessages),
		},
		{
			name: "unsupported coding before chunked",
			content: "POST / HTTP/1.1\nHost: api.example.test\nTransfer-Encoding: gzip, chunked\n\n" +
				"4\r\ntest\r\n0\r\n\r\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Convert(ImportRequest{
				Format:  "raw_http",
				Content: test.content,
				Options: ConvertOptions{InferExtractors: &inferExtractors},
			}); err == nil {
				t.Fatal("Convert accepted malformed or over-budget raw HTTP input")
			}
		})
	}
}

func TestConvertRawHTTPTransferEncodingTokenAndHeaderCaseHandling(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "raw_http",
		Content: strings.Join([]string{
			"POST /upload HTTP/1.1",
			"Host: ignored.example.test",
			"host: api.example.test",
			"Transfer-Encoding: xchunked",
			"",
			"not-a-chunked-body",
		}, "\r\n"),
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert raw HTTP token/header case input: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Body != "not-a-chunked-body" {
		t.Fatalf("non-token xchunked body = %q, want unchanged", action.Body)
	}
	if action.Headers["Transfer-Encoding"] != "" {
		t.Fatalf("transport-owned Transfer-Encoding was not omitted: %#v", action.Headers)
	}
	fidelity, _ := action.Metadata["import_header_fidelity"].(map[string]any)
	reasons, _ := fidelity["reason_codes"].([]string)
	if !slices.Contains(reasons, "http_header_transport_owned_header") {
		t.Fatalf("runtime fidelity metadata = %#v", fidelity)
	}
	if result.Script.Config.BaseURL != "https://api.example.test" {
		t.Fatalf("case-variant Host summary selected nondeterministically: base URL = %q", result.Script.Config.BaseURL)
	}
	if action.Headers["Host"] != "ignored.example.test" {
		t.Fatalf("case-variant compatibility summary = %#v, want the retained first singleton occurrence", action.Headers)
	}
	if _, exists := action.Headers["host"]; exists {
		t.Fatalf("case-variant compatibility summary retained omitted duplicate: %#v", action.Headers)
	}
}

func TestConvertRawHTTPPreservesDuplicateRequestHeaders(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "raw_http",
		Content: strings.Join([]string{
			"GET /orders HTTP/1.1",
			"Host: api.example.com",
			"X-Duplicate: one",
			"X-Duplicate: two",
			"",
			"",
		}, "\n"),
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Headers["X-Duplicate"] != "two" {
		t.Fatalf("summary X-Duplicate = %q, want last value two", action.Headers["X-Duplicate"])
	}
	got := headerListValues(action.HeaderList, "X-Duplicate")
	if strings.Join(got, ",") != "one,two" {
		t.Fatalf("header_list X-Duplicate = %#v, want one/two", got)
	}
}

// Regression for the review finding that one standard 2000-record JSON page
// (paginated REST/SOQL-style, every record carrying an Id field) made
// sample.Convert fail with a correlation work-limit error: the import must
// succeed, with correlation inference bounded internally.
func TestConvertHARWithLargeRecordPageSucceeds(t *testing.T) {
	var records strings.Builder
	for index := 0; index < 2000; index++ {
		if index > 0 {
			records.WriteByte(',')
		}
		fmt.Fprintf(&records, `{\"Id\":\"001xx%011d\",\"Name\":\"Account %d\"}`, index, index)
	}
	content := `{
		"log": {
			"entries": [
				{
					"request": {"method": "GET", "url": "https://api.example.com/v1/query?q=SELECT", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"text": "{\"totalSize\":2000,\"done\":true,\"records\":[` + records.String() + `]}"}}
				},
				{
					"request": {"method": "GET", "url": "https://api.example.com/v1/orders", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"text": "{}"}}
				}
			]
		}
	}`
	result, err := Convert(ImportRequest{Format: "har", Content: content})
	if err != nil {
		t.Fatalf("Convert large record page returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
}

// Residual hard work-limit errors (e.g. observation counts beyond the
// documented analysis cap) must degrade to a conversion warning: the script
// still imports, correlations are simply best-effort.
func TestConvertDegradesCorrelationWorkLimitToWarning(t *testing.T) {
	var entries strings.Builder
	for index := 0; index < 4097; index++ {
		if index > 0 {
			entries.WriteByte(',')
		}
		fmt.Fprintf(&entries, `{"request": {"method": "GET", "url": "https://api.example.com/v1/item/%d", "headers": []}, "response": {"status": 200, "headers": [], "content": {"text": "{}"}}}`, index)
	}
	result, err := Convert(ImportRequest{
		Format:  "har",
		Content: `{"log": {"entries": [` + entries.String() + `]}}`,
	})
	if err != nil {
		t.Fatalf("Convert must degrade the work-limit error, got: %v", err)
	}
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "correlation analysis was not completed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warnings = %#v, want a correlation-analysis degradation warning", result.Warnings)
	}
	if len(result.Correlations) != 0 {
		t.Fatalf("correlations = %d, want none after degraded analysis", len(result.Correlations))
	}
}

func TestConvertHARWithThinkTime(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"startedDateTime": "2026-05-17T10:00:00.000Z",
						"time": 200,
						"request": {
							"method": "GET",
							"url": "https://api.example.com/v1/session",
							"headers": []
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {"text": "{}"}
						}
					},
					{
						"startedDateTime": "2026-05-17T10:00:03.700Z",
						"time": 150,
						"request": {
							"method": "GET",
							"url": "https://api.example.com/v1/orders",
							"headers": []
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {"text": "{}"}
						}
					}
				]
			}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	first := result.Script.Actions[0]
	if !first.ThinkTimeAfter {
		t.Fatal("expected first HAR action to preserve think time after the request")
	}
	if first.ThinkTime.Type != "fixed" || first.ThinkTime.MeanMs != 3500 {
		t.Fatalf("first action think time = %#v, want fixed 3500ms", first.ThinkTime)
	}
	if result.Script.Actions[1].ThinkTimeAfter {
		t.Fatal("expected final HAR action to omit trailing think time")
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "think time") {
		t.Fatalf("warnings = %#v, want think time guidance", result.Warnings)
	}
}

func TestConvertHARWithoutTimestampsDoesNotUseDurationAsThinkTime(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"time": 5000,
						"request": {"method": "GET", "url": "https://api.example.com/v1/session", "headers": []},
						"response": {"status": 200, "headers": [], "content": {"text": "{}"}}
					},
					{
						"time": 200,
						"request": {"method": "GET", "url": "https://api.example.com/v1/orders", "headers": []},
						"response": {"status": 200, "headers": [], "content": {"text": "{}"}}
					}
				]
			}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	if result.Script.Actions[0].ThinkTimeAfter {
		t.Fatalf("first action think time = %#v; missing HAR timestamps must not synthesize think time from response duration", result.Script.Actions[0].ThinkTime)
	}
}

func TestConvertHARDiagnosticsForMissingResponseWebSocketAndRedirect(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"request": {
							"method": "GET",
							"url": "https://api.example.com/no-response",
							"headers": []
						}
					},
					{
						"request": {
							"method": "GET",
							"url": "https://api.example.com/socket",
							"headers": [{"name":"Upgrade","value":"websocket"}]
						},
						"response": {
							"status": 101,
							"headers": [{"name":"Upgrade","value":"websocket"}],
							"content": {"text": ""}
						}
					},
					{
						"request": {
							"method": "GET",
							"url": "https://api.example.com/login",
							"headers": []
						},
						"response": {
							"status": 302,
							"headers": [{"name":"Location","value":"https://api.example.com/home"}],
							"content": {"text": ""}
						}
					}
				]
			}
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want websocket entry skipped", len(result.Script.Actions))
	}
	if !containsSampleDiagnostic(result, "HAR missing response") {
		t.Fatalf("diagnostics = %#v, want missing response warning", result.Unsupported)
	}
	if !containsSampleDiagnostic(result, "HAR websocket upgrade") {
		t.Fatalf("diagnostics = %#v, want websocket warning", result.Unsupported)
	}
	redirect := result.Script.Actions[1]
	if redirect.MaxRedirects == nil || *redirect.MaxRedirects != 0 {
		t.Fatalf("redirect max_redirects = %#v, want 0", redirect.MaxRedirects)
	}
}

func TestConvertHARDetectsWebSocketUpgradeBeforeHeaderSanitization(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{"log":{"entries":[{
			"request":{"method":"GET","url":"https://api.example.com/socket","headers":[
				{"name":"Connection","value":"Upgrade"},
				{"name":"Upgrade","value":"websocket"}
			]}
		},{
			"request":{"method":"GET","url":"https://api.example.com/orders","headers":[]},
			"response":{"status":200,"headers":[],"content":{"text":"{}"}}
		}]}}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.ConvertedActions != 1 || result.Stats.SkippedRequests != 1 {
		t.Fatalf("stats = %#v, want websocket entry skipped and normal entry converted", result.Stats)
	}
	if !containsSampleDiagnostic(result, "HAR websocket upgrade") {
		t.Fatalf("diagnostics = %#v, want websocket warning", result.Unsupported)
	}
	if containsSampleDiagnostic(result, "HAR missing response") {
		t.Fatalf("websocket upgrade should be classified before missing response: %#v", result.Unsupported)
	}
}

func TestConvertHARFiltersStaticAssetsByDefault(t *testing.T) {
	inferExtractors := false
	content := `{
		"log": {
			"entries": [
				{
					"request": {"method": "GET", "url": "https://shop.example.com/app.js", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"mimeType": "application/javascript", "text": "console.log(1)"}}
				},
				{
					"request": {"method": "GET", "url": "https://shop.example.com/api/orders", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"mimeType": "application/json", "text": "{}"}}
				}
			]
		}
	}`
	result, err := Convert(ImportRequest{
		Format:  "har",
		Content: content,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.TotalRequests != 2 || result.Stats.FilteredAssets != 1 || result.Stats.ConvertedActions != 1 {
		t.Fatalf("stats = %#v", result.Stats)
	}
	if result.Script.Actions[0].Target != "/api/orders" {
		t.Fatalf("target = %q, want API request retained", result.Script.Actions[0].Target)
	}
	if !containsSampleDiagnostic(result, "HAR static asset filter") {
		t.Fatalf("diagnostics = %#v, want static filter info", result.Unsupported)
	}

	includeStaticAssets := true
	included, err := Convert(ImportRequest{
		Format:  "har",
		Content: content,
		Options: ConvertOptions{InferExtractors: &inferExtractors, IncludeStaticAssets: &includeStaticAssets},
	})
	if err != nil {
		t.Fatalf("Convert with static assets returned error: %v", err)
	}
	if included.Stats.FilteredAssets != 0 || included.Stats.ConvertedActions != 2 {
		t.Fatalf("included stats = %#v", included.Stats)
	}
}

func TestConvertHARDecodesBase64Bodies(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"request": {
							"method": "POST",
							"url": "https://api.example.com/v1/session",
							"headers": [],
							"postData": {
								"mimeType": "application/json",
								"encoding": "base64",
								"text": "eyJ1c2VyIjoiYWxpY2UifQ=="
							}
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {
								"encoding": "base64",
								"text": "eyJvayI6dHJ1ZX0="
							}
						}
					},
					{
						"request": {
							"method": "GET",
							"url": "https://api.example.com/v1/blob",
							"headers": []
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {
								"encoding": "base64",
								"text": "//79"
							}
						}
					}
				]
			}
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Actions[0].Body != `{"user":"alice"}` {
		t.Fatalf("request body = %q", result.Script.Actions[0].Body)
	}
	if !containsSampleDiagnostic(result, "HAR base64 content") {
		t.Fatalf("diagnostics = %#v, want binary base64 info", result.Unsupported)
	}
}

func TestConvertHARDoesNotWireDecodeStoredBinaryBeforeCorrelation(t *testing.T) {
	responseBody := gzipBase64(t, []byte(`{"csrf_token":"gzip-token-abcdef123456"}`))
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"request": {
							"method": "GET",
							"url": "https://api.example.com/form",
							"headers": []
						},
						"response": {
							"status": 200,
							"headers": [{"name":"Content-Encoding","value":"gzip"}],
							"content": {
								"encoding": "base64",
								"text": "` + responseBody + `"
							}
						}
					},
					{
						"request": {
							"method": "POST",
							"url": "https://api.example.com/form",
							"headers": [{"name":"Content-Type","value":"application/x-www-form-urlencoded"}],
							"postData": {
								"mimeType": "application/x-www-form-urlencoded",
								"text": "csrf_token=gzip-token-abcdef123456"
							}
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {"text": "{}"}
						}
					}
				]
			}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.CorrelationsDetected != 0 {
		t.Fatalf("correlations = %d, want no invented response from binary resource: %#v", result.Stats.CorrelationsDetected, result.Correlations)
	}
	if strings.Contains(result.Script.Actions[1].Body, "{{var.corr_csrf_token}}") {
		t.Fatal("binary HAR resource generated an invented wire-decoded correlation")
	}
	if !containsSampleDiagnostic(result, "HAR base64 content") {
		t.Fatalf("diagnostics = %#v, want binary storage info", result.Unsupported)
	}
}

func TestConvertHARPreservesBinaryPostDataAsBase64(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"request": {
							"method": "POST",
							"url": "https://api.example.com/v1/upload",
							"headers": [{"name":"Content-Type","value":"application/octet-stream"}],
							"postData": {
								"mimeType": "application/octet-stream",
								"encoding": "base64",
								"text": "//79AA=="
							}
						},
						"response": {
							"status": 201,
							"headers": [],
							"content": {"text": "{}"}
						}
					}
				]
			}
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.BodyEncoding != "base64" {
		t.Fatalf("body encoding = %q, want base64", action.BodyEncoding)
	}
	if action.Body != "//79AA==" {
		t.Fatalf("body = %q, want preserved base64 payload", action.Body)
	}
	if strings.Contains(action.Body, "{{binary.") {
		t.Fatalf("body = %q, should not use binary placeholder", action.Body)
	}
}

func TestConvertHARDetectsJavaHTTPSerializedObject(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"request": {
							"method": "POST",
							"url": "https://api.example.com/java",
							"headers": [{"name":"Content-Type","value":"application/x-java-serialized-object"}],
							"postData": {
								"mimeType": "application/x-java-serialized-object",
								"encoding": "base64",
								"text": "rO0ABWRlbW8="
							}
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {"text": ""}
						}
					}
				]
			}
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "java_http" {
		t.Fatalf("protocol = %q, want java_http", result.Script.Protocol)
	}
	cfg := result.Script.Config.JavaHTTP
	if cfg.Codec != "java_serialized_object_base64" || cfg.HelperMode != "embedded" || cfg.BodyEncoding != "base64" {
		t.Fatalf("java_http config = %#v", cfg)
	}
	if result.Script.Actions[0].BodyEncoding != "base64" || result.Script.Actions[0].Body != "rO0ABWRlbW8=" {
		t.Fatalf("action body encoding/body = %q/%q", result.Script.Actions[0].BodyEncoding, result.Script.Actions[0].Body)
	}
}

func TestConvertHARDetectsJavaHTTPSerializedObjectMagicBytes(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"request": {
							"method": "POST",
							"url": "https://api.example.com/java",
							"headers": [],
							"postData": {
								"encoding": "base64",
								"text": "rO0ABQ=="
							}
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {"text": ""}
						}
					}
				]
			}
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "java_http" {
		t.Fatalf("protocol = %q, want java_http", result.Script.Protocol)
	}
	if result.Script.Config.JavaHTTP.ContentType != "application/x-java-serialized-object" {
		t.Fatalf("java_http content type = %q", result.Script.Config.JavaHTTP.ContentType)
	}
}

func TestConvertHARDetectsHTMLHiddenTokenCorrelation(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"request": {
							"method": "GET",
							"url": "https://shop.example.com/login",
							"headers": []
						},
						"response": {
							"status": 200,
							"headers": [{"name":"Content-Type","value":"text/html"}],
							"content": {
								"mimeType": "text/html",
								"text": "<form><input type=\"hidden\" name=\"csrf_token\" value=\"tok-9f8e7d6c5b4a\"></form>"
							}
						}
					},
					{
						"request": {
							"method": "POST",
							"url": "https://shop.example.com/login",
							"headers": [{"name":"Content-Type","value":"application/x-www-form-urlencoded"}],
							"postData": {
								"mimeType": "application/x-www-form-urlencoded",
								"text": "csrf_token=tok-9f8e7d6c5b4a&user=demo"
							}
						},
						"response": {
							"status": 302,
							"headers": [],
							"content": {"text": ""}
						}
					}
				]
			}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.CorrelationsDetected != 1 {
		t.Fatalf("correlations = %d, want 1: %#v", result.Stats.CorrelationsDetected, result.Correlations)
	}
	if len(result.Script.Actions[0].Extractors) != 1 {
		t.Fatalf("first action extractors = %#v", result.Script.Actions[0].Extractors)
	}
	ext := result.Script.Actions[0].Extractors[0]
	if ext.Name != "corr_csrf_token" || ext.Type != "regex" || ext.Source != "body" {
		t.Fatalf("unexpected extractor: %#v", ext)
	}
	if !strings.Contains(result.Script.Actions[1].Body, "{{var.corr_csrf_token}}") {
		t.Fatalf("second action body = %q, want correlated token", result.Script.Actions[1].Body)
	}
}

func TestConvertHARPreservesRepeatedResponseHeaderOrdinalForCorrelation(t *testing.T) {
	const secondToken = "session-token-second-123456"
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log":{"entries":[
				{
					"request":{"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
					"response":{"status":200,"headers":[
						{"name":"X-Session-Token","value":"session-token-first-123456"},
						{"name":"X-Session-Token","value":"` + secondToken + `"}
					],"content":{"text":"{}"}}
				},
				{
					"request":{"method":"GET","url":"https://api.example.com/use?echo=` + secondToken + `","headers":[]},
					"response":{"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Correlations) != 1 || !result.Correlations[0].PreApplied {
		t.Fatalf("correlations = %#v, want the reused second header occurrence applied", result.Correlations)
	}
	extractors := result.Script.Actions[0].Extractors
	if len(extractors) != 1 || extractors[0].Type != "regex" || !strings.Contains(extractors[0].Expression, "{1}") || extractors[0].OnMiss != "fail" {
		t.Fatalf("source extractors = %#v, want fail-closed second-occurrence header regex", extractors)
	}
	if got := result.Script.Actions[1].Target; got != "/use?echo={{var.corr_x_session_token}}" {
		t.Fatalf("consumer target = %q, want second header token correlated", got)
	}
}

func TestConvertHARDoesNotRewriteLiteralBeforeCorrelationSource(t *testing.T) {
	const token = "source-ordered-token-abcdef123456"
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {"entries": [
				{
					"request": {"method":"GET","url":"https://api.example.com/before?echo=` + token + `","headers":[]},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				},
				{
					"request": {"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
					"response": {"status":200,"headers":[],"content":{"mimeType":"application/json","text":"{\"access_token\":\"` + token + `\"}"}}
				},
				{
					"request": {"method":"GET","url":"https://api.example.com/after?access_token=` + token + `","headers":[]},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.CorrelationsDetected != 1 {
		t.Fatalf("correlations = %d, want 1: %#v", result.Stats.CorrelationsDetected, result.Correlations)
	}
	if got := result.Script.Actions[0].Target; got != "/before?echo="+token {
		t.Fatalf("pre-source target = %q, want literal preserved", got)
	}
	if len(result.Script.Actions[1].Extractors) != 1 {
		t.Fatalf("source extractors = %#v, want one", result.Script.Actions[1].Extractors)
	}
	if got := result.Script.Actions[2].Target; got != "/after?access_token={{var.corr_access_token}}" {
		t.Fatalf("post-source target = %q, want correlation variable", got)
	}
}

func TestConvertHARRewritesEncodedCorrelationValues(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"request": {
							"method": "GET",
							"url": "https://api.example.com/bootstrap",
							"headers": []
						},
						"response": {
							"status": 200,
							"headers": [{"name":"Content-Type","value":"application/json"}],
							"content": {
								"mimeType": "application/json",
								"text": "{\"auth\":{\"access_token\":\"tok abc/123456\"}}"
							}
						}
					},
					{
						"request": {
							"method": "GET",
							"url": "https://api.example.com/callback?access_token=tok+abc%2F123456",
							"headers": [{"name":"X-Access-Token","value":"tok+abc%2F123456"}]
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {"text": "{}"}
						}
					}
				]
			}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.CorrelationsDetected != 1 {
		t.Fatalf("correlations = %d, want 1: %#v", result.Stats.CorrelationsDetected, result.Correlations)
	}
	second := result.Script.Actions[1]
	if second.Target != "/callback?access_token={{var.corr_access_token}}" {
		t.Fatalf("second target = %q, want encoded token rewritten", second.Target)
	}
	if second.Headers["X-Access-Token"] != "{{var.corr_access_token}}" {
		t.Fatalf("summary header = %q, want encoded token rewritten", second.Headers["X-Access-Token"])
	}
	got := headerListValues(second.HeaderList, "X-Access-Token")
	if strings.Join(got, ",") != "{{var.corr_access_token}}" {
		t.Fatalf("header_list X-Access-Token = %#v, want encoded token rewritten", got)
	}
	ext := result.Script.Actions[0].Extractors[0]
	if ext.Expression != "$.auth.access_token" || ext.OnMiss != "fail" || ext.Convert != "url_encode" {
		t.Fatalf("extractor = %#v, want nested JSONPath, url_encode, and fail-closed", ext)
	}
}

func TestConvertHAREncodedCorrelationWithAmpersandUsesURLEncodedVariable(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {"entries": [
				{
					"request": {"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
					"response": {"status":200,"headers":[],"content":{"mimeType":"application/json","text":"{\"access_token\":\"tok&branch/123456\"}"}}
				},
				{
					"request": {"method":"GET","url":"https://api.example.com/callback?access_token=tok%26branch%2F123456","headers":[]},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions[0].Extractors) != 1 {
		t.Fatalf("source extractors = %#v", result.Script.Actions[0].Extractors)
	}
	ext := result.Script.Actions[0].Extractors[0]
	if ext.Convert != "url_encode" {
		t.Fatalf("extractor = %#v, want url_encode so '&' cannot split the replay query", ext)
	}
	if got := result.Script.Actions[1].Target; got != "/callback?access_token={{var.corr_access_token}}" {
		t.Fatalf("target = %q, want encoded literal routed through extracted variable", got)
	}
}

func TestConvertHAREncodedSourceDoesNotDoubleEncodeEncodedReuse(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {"entries": [
				{
					"request": {"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
					"response": {"status":200,"headers":[],"content":{"mimeType":"application/x-www-form-urlencoded","text":"aura.token=tok%26branch%2F123456"}}
				},
				{
					"request": {"method":"POST","url":"https://api.example.com/aura","headers":[],"postData":{"mimeType":"application/x-www-form-urlencoded","text":"aura.token=tok%26branch%2F123456"}},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions[0].Extractors) != 1 {
		t.Fatalf("source extractors = %#v", result.Script.Actions[0].Extractors)
	}
	ext := result.Script.Actions[0].Extractors[0]
	if ext.Convert != "" {
		t.Fatalf("extractor = %#v, want encoded source captured as-is for encoded reuse", ext)
	}
	if got := result.Script.Actions[1].Body; got != "aura.token={{var.corr_salesforce_aura_token}}" {
		t.Fatalf("body = %q, want encoded literal routed through captured encoded variable", got)
	}
	if len(result.Correlations) != 1 || !result.Correlations[0].PreApplied || result.Correlations[0].ReviewAction != "" {
		t.Fatalf("correlations = %#v, want executable correlation classified pre-applied", result.Correlations)
	}
}

func TestConvertHARLocationQuerySourcePreservesPlusWireSpelling(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {"entries": [
				{
					"request": {"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
					"response": {"status":302,"headers":[{"name":"Location","value":"https://api.example.com/callback?state=space+token%2F123456"}],"content":{"text":""}}
				},
				{
					"request": {"method":"GET","url":"https://api.example.com/use?state=space+token%2F123456","headers":[]},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	extractors := result.Script.Actions[0].Extractors
	if len(extractors) != 1 {
		t.Fatalf("Location source extractors = %#v, want one", extractors)
	}
	if extractor := extractors[0]; extractor.Source != "header" || extractor.Type != "regex" || extractor.Convert != "" {
		t.Fatalf("Location source extractor = %#v, want exact wire capture without a second encoding pass", extractor)
	}
	if got := result.Script.Actions[1].Target; got != "/use?state={{var.corr_location_state}}" {
		t.Fatalf("target = %q, want encoded Location value routed through its wire-spelling variable", got)
	}
	if len(result.Correlations) != 1 || result.Correlations[0].SampleValue != "space token/123456" || !result.Correlations[0].PreApplied {
		t.Fatalf("correlations = %#v, want decoded Location value safely pre-applied", result.Correlations)
	}
}

func TestConvertHARLocationQuerySourceValidatesAuthoritativeDuplicateOccurrence(t *testing.T) {
	const decodedValue = "space token/123456"
	const canonicalValue = "space+token%2F123456"
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {"entries": [
				{
					"request": {"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
					"response": {"status":302,"headers":[
						{"name":"Location","value":"https://api.example.com/callback?state=space%20token%2F123456"},
						{"name":"Location","value":"https://api.example.com/callback?state=space+token%2F123456"}
					],"content":{"text":""}}
				},
				{
					"request": {"method":"GET","url":"https://api.example.com/use?state=space+token%2F123456","headers":[]},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[0].Extractors; len(got) != 0 {
		t.Fatalf("source extractors = %#v, want no automatic extractor for the non-canonical first Location occurrence", got)
	}
	if got := result.Script.Actions[1].Target; got != "/use?state="+canonicalValue {
		t.Fatalf("consumer target = %q, want canonical literal left for review", got)
	}
	if len(result.Correlations) != 1 || result.Correlations[0].SampleValue != decodedValue ||
		result.Correlations[0].PreApplied || result.Correlations[0].ReviewAction != manualCorrelationValueReplacement {
		t.Fatalf("correlations = %#v, want the first duplicate source occurrence left for manual review", result.Correlations)
	}
}

func TestConvertHAREncodedCredentialConsumerUsesSharedProjection(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {"entries": [
				{
					"request": {"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
					"response": {"status":200,"headers":[],"content":{"mimeType":"application/x-www-form-urlencoded","text":"aura.token=tok%20branch%2F123456"}}
				},
				{
					"request": {"method":"POST","url":"https://api.example.com/aura","headers":[],"postData":{"mimeType":"application/x-www-form-urlencoded","text":"aura.token=tok+branch%2F123456"}},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[1].Body; got != "aura.token=tok+branch%2F123456" {
		t.Fatalf("body = %q, want imported form bytes", got)
	}
	if got := result.Script.Actions[0].Extractors; len(got) != 0 {
		t.Fatalf("source extractors = %#v, want none because %%20 source cannot supply '+' consumer", got)
	}
	if len(result.Correlations) != 1 || result.Correlations[0].PreApplied || result.Correlations[0].ReviewAction != "manual_value_replacement_required" {
		t.Fatalf("correlations = %#v, want manual non-pre-applied candidate", result.Correlations)
	}
}

func TestConvertHARMixedRawAndEncodedCorrelationUsesSiblingVariables(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {"entries": [
				{
					"request": {"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
					"response": {"status":200,"headers":[],"content":{"mimeType":"application/json","text":"{\"access_token\":\"tok abc/123456\"}"}}
				},
				{
					"request": {
						"method":"POST",
						"url":"https://api.example.com/callback?access_token=tok+abc%2F123456",
						"headers":[{"name":"X-Raw-Correlation","value":"tok abc/123456"}]
					},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	extractors := result.Script.Actions[0].Extractors
	if len(extractors) != 2 {
		t.Fatalf("source extractors = %#v, want raw and encoded siblings", extractors)
	}
	byName := map[string]dsl.Extractor{}
	for _, extractor := range extractors {
		byName[extractor.Name] = extractor
	}
	if byName["corr_access_token"].Convert != "" || byName["corr_access_token_encoded"].Convert != "url_encode" {
		t.Fatalf("source extractors = %#v, want raw base and url_encode sibling", extractors)
	}
	second := result.Script.Actions[1]
	if second.Target != "/callback?access_token={{var.corr_access_token_encoded}}" {
		t.Fatalf("encoded target = %q, want encoded sibling variable", second.Target)
	}
	if second.Headers["X-Raw-Correlation"] != "{{var.corr_access_token}}" {
		t.Fatalf("raw header = %q, want raw variable", second.Headers["X-Raw-Correlation"])
	}
	if result.Stats.Extractors != 2 {
		t.Fatalf("extractor stats = %d, want 2", result.Stats.Extractors)
	}
}

func TestPrepareCorrelationApplicationRejectsEncodedRawLiteralCollision(t *testing.T) {
	const rawValue = "opaque token/123456"
	encodedValue := url.QueryEscape(rawValue)
	exchanges := []exchange{
		{URL: "https://api.example.test/source-a"},
		{URL: "https://api.example.test/source-b"},
		{URL: "https://api.example.test/use?token=" + encodedValue},
	}
	correlations := []Correlation{
		{
			Name:        "corr_a",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(opaque token/123456)`,
			Value:       rawValue,
			FoundInStep: 1,
			UsedInSteps: []int{3},
		},
		{
			Name:        "corr_b",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(opaque\+token%2F123456)`,
			Value:       encodedValue,
			FoundInStep: 2,
			UsedInSteps: []int{3},
		},
	}

	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	if len(vars) != 0 || len(sourceSteps) != 0 || len(extraExtractors) != 0 {
		t.Fatalf("ambiguous collision application = vars %#v, sources %#v, extractors %#v; want no automatic rewrite", vars, sourceSteps, extraExtractors)
	}
	for _, correlation := range correlations {
		if correlation.PreApplied || correlation.ReviewAction != manualCorrelationValueReplacement {
			t.Fatalf("collision correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestPrepareCorrelationApplicationNeverRewritesEncodedRequestBodies(t *testing.T) {
	const encodedBody = "YWJjZGVm"
	exchanges := []exchange{
		{
			URL: "https://api.example.test/source",
			Response: &response{
				Body: `{"token":"` + encodedBody + `"}`,
			},
		},
		{
			URL:          "https://api.example.test/use",
			Body:         encodedBody,
			BodyEncoding: "base64",
		},
	}
	correlations, err := detectCorrelationsChecked(exchanges)
	if err != nil {
		t.Fatalf("detectCorrelationsChecked: %v", err)
	}
	if len(correlations) != 1 || len(correlations[0].UsedInSteps) != 1 {
		t.Fatalf("correlations = %#v, want the transformed-body reuse surfaced for review", correlations)
	}
	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	if len(vars) != 0 || len(sourceSteps) != 0 || len(extraExtractors) != 0 {
		t.Fatalf("encoded-body application = vars %#v sources %#v extractors %#v, want no automatic rewrite", vars, sourceSteps, extraExtractors)
	}
	if correlations[0].PreApplied || correlations[0].ReviewAction != manualCorrelationValueReplacement {
		t.Fatalf("encoded-body correlation = %#v, want manual review", correlations[0])
	}
}

func TestPrepareCorrelationApplicationMarksTransformedBodyRemainderForReview(t *testing.T) {
	const encodedBody = "YWJjZGVm"
	exchanges := []exchange{
		{
			URL: "https://api.example.test/source",
			Response: &response{
				Body: `{"token":"` + encodedBody + `"}`,
			},
		},
		{
			URL:          "https://api.example.test/use?token=" + encodedBody,
			Body:         encodedBody,
			BodyEncoding: "base64",
		},
	}
	correlations, err := detectCorrelationsChecked(exchanges)
	if err != nil {
		t.Fatalf("detectCorrelationsChecked: %v", err)
	}
	if len(correlations) != 1 {
		t.Fatalf("correlations = %#v, want one transformed-body correlation", correlations)
	}
	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	if vars[encodedBody] != "{{var.corr_token}}" || sourceSteps[encodedBody] != 1 || len(extraExtractors) != 0 {
		t.Fatalf("partial application = vars %#v sources %#v extractors %#v, want only the editable URL mapped", vars, sourceSteps, extraExtractors)
	}
	if !correlations[0].PreApplied || correlations[0].ReviewAction != manualCorrelationValueReplacement {
		t.Fatalf("partial transformed-body correlation = %#v, want pre-applied URL plus explicit manual remainder", correlations[0])
	}
}

func TestPrepareCorrelationApplicationRejectsEncodedValueBeyondRuntimeResultLimit(t *testing.T) {
	// Raw value fits the 16 KiB per-extraction ceiling, while QueryEscape
	// expands it beyond that ceiling.
	value := strings.Repeat("?", 6_000)
	encoded := url.QueryEscape(value)
	exchanges := []exchange{
		{URL: "https://api.example.test/source"},
		{URL: "https://api.example.test/use?token=" + encoded},
	}
	correlations := []Correlation{{
		Name:        "corr_token",
		Source:      "body",
		ExtractType: "regex",
		Expression:  `(token)`,
		Value:       value,
		FoundInStep: 1,
		UsedInSteps: []int{2},
	}}
	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	if len(vars) != 0 || len(sourceSteps) != 0 || len(extraExtractors) != 0 {
		t.Fatalf("oversized encoded application = vars %#v sources %#v extractors %#v, want none", vars, sourceSteps, extraExtractors)
	}
	if correlations[0].PreApplied || correlations[0].ReviewAction != manualCorrelationValueReplacement {
		t.Fatalf("oversized encoded correlation = %#v, want manual review", correlations[0])
	}
}

func TestPrepareCorrelationApplicationRespectsAggregateRuntimeResultLimit(t *testing.T) {
	exchanges := []exchange{{URL: "https://api.example.test/source"}}
	correlations := make([]Correlation, 0, 5)
	extractionLimit := 0
	for index := 0; index < 5; index++ {
		value := fmt.Sprintf("token-%d-%s", index, strings.Repeat(string(rune('a'+index)), 12))
		name := fmt.Sprintf("corr_token_%d", index)
		if index < 4 {
			entryBytes, ok := correlate.CorrelationExtractionEntryBytes(name, value)
			if !ok {
				t.Fatalf("test entry %d unexpectedly exceeds the per-entry runtime limit", index)
			}
			extractionLimit += entryBytes
		}
		exchanges = append(exchanges, exchange{URL: "https://api.example.test/use?token=" + value})
		correlations = append(correlations, Correlation{
			Name:        name,
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(token)`,
			Value:       value,
			FoundInStep: 1,
			UsedInSteps: []int{index + 2},
		})
	}

	vars, sourceSteps, extraExtractors := prepareCorrelationApplicationWithExtractionLimit(exchanges, correlations, extractionLimit, false)
	if len(vars) != 4 || len(sourceSteps) != 4 || len(extraExtractors) != 0 {
		t.Fatalf("aggregate-limited application = vars %d sources %d extras %#v, want four raw mappings", len(vars), len(sourceSteps), extraExtractors)
	}
	for index := range correlations {
		if index < 4 {
			if !correlations[index].PreApplied || correlations[index].ReviewAction != "" {
				t.Fatalf("correlation %d = %#v, want pre-applied", index, correlations[index])
			}
			continue
		}
		if correlations[index].PreApplied || correlations[index].ReviewAction != manualCorrelationValueReplacement {
			t.Fatalf("correlation %d = %#v, want aggregate-budget manual review", index, correlations[index])
		}
	}
}

func TestSampleCorrelationCollisionSurfaceLimitFailsClosed(t *testing.T) {
	exchanges := []exchange{{
		URL:     "https://example.test/use?left=TOKEN-left",
		Headers: map[string]string{"X-Right": "TOKEN-right"},
	}}
	plans := []correlationApplicationPlan{
		{index: 0, value: "TOKEN-left", usage: correlationValueUse{raw: true}},
		{index: 1, value: "TOKEN-right", usage: correlationValueUse{raw: true}},
	}
	literals, planIndexes := sampleCorrelationReplacementCollisionsWithSurfaceLimit(exchanges, plans, 1)
	if !literals["TOKEN-left"] || !literals["TOKEN-right"] || !planIndexes[0] || !planIndexes[1] {
		t.Fatalf("surface-limit collision result = literals %#v plans %#v, want every claim suppressed", literals, planIndexes)
	}
}

func TestPrepareCorrelationApplicationRejectsSubstringEncodedRawLiteralCollision(t *testing.T) {
	const rawValue = "opaque token/123456"
	encodedValue := url.QueryEscape(rawValue)
	containingRawValue := "prefix-" + encodedValue + "-suffix"
	exchanges := []exchange{
		{URL: "https://api.example.test/source-a"},
		{URL: "https://api.example.test/source-b"},
		{URL: "https://api.example.test/use?token=" + containingRawValue},
	}
	correlations := []Correlation{
		{
			Name:        "corr_a",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(opaque token/123456)`,
			Value:       rawValue,
			FoundInStep: 1,
			UsedInSteps: []int{3},
		},
		{
			Name:        "corr_b",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(prefix-opaque\+token%2F123456-suffix)`,
			Value:       containingRawValue,
			FoundInStep: 2,
			UsedInSteps: []int{3},
		},
	}

	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	if len(vars) != 0 || len(sourceSteps) != 0 || len(extraExtractors) != 0 {
		t.Fatalf("substring collision application = vars %#v, sources %#v, extractors %#v; want no automatic rewrite", vars, sourceSteps, extraExtractors)
	}
	for _, correlation := range correlations {
		if correlation.PreApplied || correlation.ReviewAction != manualCorrelationValueReplacement {
			t.Fatalf("substring collision correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func partialOverlapCorrelations() []Correlation {
	return []Correlation{
		{
			Name:        "corr_token",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(TOKENabc123def456)`,
			Value:       "TOKENabc123def456",
			FoundInStep: 1,
			UsedInSteps: []int{3},
		},
		{
			Name:        "corr_session_id",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(def456SESSIONxyz789)`,
			Value:       "def456SESSIONxyz789",
			FoundInStep: 2,
			UsedInSteps: []int{3},
		},
	}
}

func TestPrepareCorrelationApplicationRejectsPartialOverlapLiteralCollision(t *testing.T) {
	// Neither literal contains the other, but the suffix "def456" of the first
	// is the prefix of the second and the merged region occurs in the step-3
	// URL. Single-pass replacement would produce the hybrid
	// "blob={{var.corr_token}}SESSIONxyz789" while corr_session_id is reported
	// pre-applied with an OnMiss:fail extractor that rewrote nothing.
	exchanges := []exchange{
		{URL: "https://api.example.test/source-a"},
		{URL: "https://api.example.test/source-b"},
		{URL: "https://api.example.test/use?blob=TOKENabc123def456SESSIONxyz789"},
	}
	correlations := partialOverlapCorrelations()

	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	if len(vars) != 0 || len(sourceSteps) != 0 || len(extraExtractors) != 0 {
		t.Fatalf("partial-overlap collision application = vars %#v, sources %#v, extractors %#v; want no automatic rewrite", vars, sourceSteps, extraExtractors)
	}
	for _, correlation := range correlations {
		if correlation.PreApplied || correlation.ReviewAction != manualCorrelationValueReplacement {
			t.Fatalf("partial-overlap collision correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestPrepareCorrelationApplicationAllowsSuffixPrefixLiteralsUsedDisjointly(t *testing.T) {
	// The same literal pair shares the boundary substring "def456", but the
	// merged spelling never occurs on a request surface: disjoint occurrences
	// replace correctly and must stay automatic.
	exchanges := []exchange{
		{URL: "https://api.example.test/source-a"},
		{URL: "https://api.example.test/source-b"},
		{URL: "https://api.example.test/use?a=TOKENabc123def456&b=def456SESSIONxyz789"},
	}
	correlations := partialOverlapCorrelations()

	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	if len(vars) != 2 || vars["TOKENabc123def456"] != "{{var.corr_token}}" || vars["def456SESSIONxyz789"] != "{{var.corr_session_id}}" {
		t.Fatalf("disjoint suffix/prefix vars = %#v, want both automatic replacements", vars)
	}
	if len(sourceSteps) != 2 || len(extraExtractors) != 0 {
		t.Fatalf("disjoint suffix/prefix sources = %#v, extractors = %#v", sourceSteps, extraExtractors)
	}
	for _, correlation := range correlations {
		if !correlation.PreApplied || correlation.ReviewAction != "" {
			t.Fatalf("disjoint suffix/prefix correlation = %#v, want pre-applied without review action", correlation)
		}
	}
}

func TestPrepareCorrelationApplicationRejectsPartialOverlapWithEncodedSpelling(t *testing.T) {
	// corr_a's raw literal "TOKENabc" partially overlaps corr_b's QueryEscape
	// spelling "abc+SESSION" in the step-3 URL. The encoded claim must be
	// dropped for both so no orphaned url_encode sibling extractor is
	// synthesized, while corr_b's raw header reuse remains safely automatic.
	exchanges := []exchange{
		{URL: "https://api.example.test/source-a"},
		{URL: "https://api.example.test/source-b"},
		{
			URL:     "https://api.example.test/use?blob=TOKENabc+SESSION",
			Headers: map[string]string{"X-Raw-Correlation": "abc SESSION"},
		},
	}
	correlations := []Correlation{
		{
			Name:        "corr_a",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(TOKENabc)`,
			Value:       "TOKENabc",
			FoundInStep: 1,
			UsedInSteps: []int{3},
		},
		{
			Name:        "corr_b",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(abc SESSION)`,
			Value:       "abc SESSION",
			FoundInStep: 2,
			UsedInSteps: []int{3},
		},
	}

	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	if len(vars) != 1 || vars["abc SESSION"] != "{{var.corr_b}}" {
		t.Fatalf("encoded partial-overlap vars = %#v, want only the raw header replacement", vars)
	}
	if len(sourceSteps) != 1 || sourceSteps["abc SESSION"] != 2 {
		t.Fatalf("encoded partial-overlap sources = %#v, want raw literal only", sourceSteps)
	}
	if len(extraExtractors) != 0 {
		t.Fatalf("encoded partial-overlap sibling extractors = %#v, want no orphaned url_encode sibling", extraExtractors)
	}
	if correlations[0].PreApplied || correlations[0].ReviewAction != manualCorrelationValueReplacement {
		t.Fatalf("corr_a = %#v, want manual and not pre-applied", correlations[0])
	}
	// corr_b keeps PreApplied (its raw consumer was rewritten) alongside the
	// manual review action for the ambiguous encoded spelling.
	if !correlations[1].PreApplied || correlations[1].ReviewAction != manualCorrelationValueReplacement || correlations[1].Convert != "" {
		t.Fatalf("corr_b = %#v, want raw pre-applied with manual review for the encoded spelling", correlations[1])
	}
}

func TestConvertHARLeavesPartialOverlapLiteralsForManualReview(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {"entries": [
				{
					"request": {"method":"GET","url":"https://app.example.com/login","headers":[]},
					"response": {"status":200,"headers":[],"content":{"mimeType":"application/json","text":"{\"token\":\"TOKENabc123def456\"}"}}
				},
				{
					"request": {"method":"GET","url":"https://app.example.com/session","headers":[]},
					"response": {"status":200,"headers":[],"content":{"mimeType":"application/json","text":"{\"session_id\":\"def456SESSIONxyz789\"}"}}
				},
				{
					"request": {"method":"GET","url":"https://app.example.com/step3?blob=TOKENabc123def456SESSIONxyz789","headers":[]},
					"response": {"status":200,"headers":[],"content":{"text":"{}"}}
				}
			]}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[2].Target; !strings.Contains(got, "blob=TOKENabc123def456SESSIONxyz789") || strings.Contains(got, "{{var.") {
		t.Fatalf("step-3 target = %q, want recorded merged literal preserved without hybrid rewrite", got)
	}
	for index := 0; index < 2; index++ {
		if got := result.Script.Actions[index].Extractors; len(got) != 0 {
			t.Fatalf("action %d extractors = %#v, want none for ambiguous partial overlap", index, got)
		}
	}
	if len(result.Correlations) != 2 {
		t.Fatalf("correlations = %#v, want both partial-overlap candidates surfaced", result.Correlations)
	}
	for _, correlation := range result.Correlations {
		if correlation.PreApplied || correlation.ReviewAction != manualCorrelationValueReplacement {
			t.Fatalf("partial-overlap correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestPrepareCorrelationApplicationCapsSourceExtractorsIncludingEncodedSibling(t *testing.T) {
	const mixedValue = "mixed token/000000"
	correlations := make([]Correlation, 0, dsl.MaxActionExtractors)
	correlations = append(correlations, Correlation{
		Name:        "corr_mixed",
		Source:      "body",
		ExtractType: "regex",
		Expression:  `(mixed token/000000)`,
		Value:       mixedValue,
		FoundInStep: 1,
		UsedInSteps: []int{2},
	})
	rawValues := []string{mixedValue}
	for index := 1; index < dsl.MaxActionExtractors; index++ {
		value := fmt.Sprintf("value-%06d-abcdef", index)
		correlations = append(correlations, Correlation{
			Name:        fmt.Sprintf("corr_value_%06d", index),
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(` + value + `)`,
			Value:       value,
			FoundInStep: 1,
			UsedInSteps: []int{2},
		})
		rawValues = append(rawValues, value)
	}
	exchanges := []exchange{
		{URL: "https://api.example.test/source"},
		{
			URL:  "https://api.example.test/use?mixed=" + url.QueryEscape(mixedValue),
			Body: strings.Join(rawValues, "|"),
		},
	}

	vars, sourceSteps, extraExtractors := prepareCorrelationApplication(exchanges, correlations)
	preApplied := 0
	for _, correlation := range correlations {
		if correlation.PreApplied {
			preApplied++
		}
	}
	totalExtractors := preApplied + len(extraExtractors[1])
	if totalExtractors != dsl.MaxActionExtractors {
		t.Fatalf("source extractors = %d, want capped at %d", totalExtractors, dsl.MaxActionExtractors)
	}
	if len(extraExtractors[1]) != 1 || extraExtractors[1][0].Name != "corr_mixed_encoded" {
		t.Fatalf("encoded sibling extractors = %#v, want one reserved sibling", extraExtractors[1])
	}
	if len(vars) != dsl.MaxActionExtractors || len(sourceSteps) != dsl.MaxActionExtractors {
		t.Fatalf("replacement maps = %d/%d, want %d safe replacements", len(vars), len(sourceSteps), dsl.MaxActionExtractors)
	}
	overflow := correlations[len(correlations)-1]
	if overflow.PreApplied || overflow.ReviewAction != manualCorrelationValueReplacement {
		t.Fatalf("overflow correlation = %#v, want manual and not pre-applied", overflow)
	}
	if _, exists := vars[overflow.Value]; exists {
		t.Fatalf("overflow value %q was exposed as an automatic replacement", overflow.Value)
	}
}

func TestConvertHARGovernsUnsupportedEncodedCredentialSpellings(t *testing.T) {
	const rawValue = "tok +/== recorded"
	exact := url.QueryEscape(rawValue)
	tests := []struct {
		name              string
		spelling          string
		expectCorrelation bool
	}{
		{name: "path escape", spelling: url.PathEscape(rawValue)},
		{name: "percent-20 space", spelling: strings.ReplaceAll(exact, "+", "%20"), expectCorrelation: true},
		{name: "unescaped literal plus", spelling: strings.ReplaceAll(exact, "%2B", "+")},
		{name: "lowercase percent hex", spelling: strings.NewReplacer("%2B", "%2b", "%2F", "%2f", "%3D", "%3d").Replace(exact), expectCorrelation: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{
				Format: "har",
				Content: `{
					"log": {"entries": [
						{
							"request": {"method":"GET","url":"https://api.example.com/bootstrap","headers":[]},
							"response": {"status":200,"headers":[],"content":{"mimeType":"application/json","text":"{\"access_token\":\"` + rawValue + `\"}"}}
						},
						{
							"request": {"method":"GET","url":"https://api.example.com/callback?access_token=` + tt.spelling + `","headers":[]},
							"response": {"status":200,"headers":[],"content":{"text":"{}"}}
						}
					]}
				}`,
			})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			wantTarget := "/callback?access_token=" + tt.spelling
			if got := result.Script.Actions[1].Target; got != wantTarget {
				t.Fatalf("target = %q, want imported encoded credential %q", got, wantTarget)
			}
			for _, extractor := range result.Script.Actions[0].Extractors {
				if extractor.Convert == "url_encode" {
					t.Fatalf("source extractors = %#v, unsupported spelling must not select url_encode", result.Script.Actions[0].Extractors)
				}
			}
			if len(result.Script.Actions[0].Extractors) != 0 {
				t.Fatalf("source extractors = %#v, unsupported-only consumer must not add an unused extractor", result.Script.Actions[0].Extractors)
			}
			if tt.expectCorrelation {
				if len(result.Correlations) != 1 || result.Correlations[0].PreApplied || result.Correlations[0].ReviewAction != "manual_value_replacement_required" {
					t.Fatalf("correlations = %#v, want manual non-pre-applied candidate", result.Correlations)
				}
			}
		})
	}
}

func TestConvertHARPreservesFormParamsCookiesAndDuplicateHeaders(t *testing.T) {
	sanitizeSensitive := false
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"pageref": "page_1",
						"serverIPAddress": "203.0.113.10",
						"timings": {"wait": 12, "receive": 3},
						"request": {
							"method": "POST",
							"url": "https://api.example.com/search",
							"httpVersion": "HTTP/2",
							"queryString": [{"name": "q", "value": "widgets"}],
							"headers": [],
							"cookies": [
								{"name": "sid", "value": "abc"},
								{"name": "theme", "value": "dark"}
							],
							"postData": {
								"mimeType": "application/x-www-form-urlencoded",
								"params": [
									{"name": "q", "value": "widgets"},
									{"name": "page", "value": "1"}
								]
							}
						},
						"response": {
							"status": 200,
							"httpVersion": "HTTP/2",
							"headers": [
								{"name": "Set-Cookie", "value": "a=1"},
								{"name": "Set-Cookie", "value": "b=2"}
							],
							"cookies": [{"name": "a", "value": "1", "httpOnly": true}],
							"content": {"text": "{}"}
						}
					},
					{
						"request": {
							"method": "POST",
							"url": "https://api.example.com/upload",
							"headers": [],
							"postData": {
								"mimeType": "multipart/form-data",
								"params": [
									{"name": "description", "value": "quarterly report"},
									{"name": "file", "fileName": "report.pdf", "contentType": "application/pdf"}
								]
							}
						},
						"response": {"status": 201, "headers": [], "content": {"text": "{}"}}
					}
				]
			}
		}`,
		Options: ConvertOptions{SanitizeSensitive: &sanitizeSensitive, InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	first := result.Script.Actions[0]
	if first.Body != "q=widgets&page=1" {
		t.Fatalf("urlencoded body = %q", first.Body)
	}
	if first.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q", first.Headers["Content-Type"])
	}
	if first.Headers["Cookie"] != "sid=abc; theme=dark" {
		t.Fatalf("Cookie header = %q, want HAR cookies[] serialized in source order", first.Headers["Cookie"])
	}
	if got := headerListValues(first.HeaderList, "Cookie"); !slices.Equal(got, []string{"sid=abc; theme=dark"}) {
		t.Fatalf("Cookie header occurrences = %#v, want one exact serialized occurrence", got)
	}
	if first.Metadata["har.request.http_version"] != "HTTP/2" || first.Metadata["har.response.http_version"] != "HTTP/2" || first.Metadata["har.server_ip_address"] != "203.0.113.10" {
		t.Fatalf("HAR metadata = %#v, want HTTP versions and server IP", first.Metadata)
	}
	if timings, ok := first.Metadata["har.timings"].(map[string]any); !ok || timings["wait"].(float64) != 12 {
		t.Fatalf("HAR timings metadata = %#v", first.Metadata["har.timings"])
	}
	if query, ok := first.Metadata["har.request.query_string"].([]map[string]any); !ok || len(query) != 1 || query[0]["name"] != "q" {
		t.Fatalf("HAR query metadata = %#v", first.Metadata["har.request.query_string"])
	}
	if cookies, ok := first.Metadata["har.response.cookies"].([]map[string]any); !ok || len(cookies) != 1 || cookies[0]["httpOnly"] != true {
		t.Fatalf("HAR response cookie metadata = %#v", first.Metadata["har.response.cookies"])
	}
	second := result.Script.Actions[1]
	if second.Headers["Content-Type"] != "multipart/form-data; boundary=----apexload-har-boundary" {
		t.Fatalf("multipart content type = %q", second.Headers["Content-Type"])
	}
	if !strings.Contains(second.Body, "------apexload-har-boundary\r\n") || !strings.Contains(second.Body, "------apexload-har-boundary--\r\n") {
		t.Fatalf("multipart body did not use generated boundary: %s", second.Body)
	}
	if !strings.Contains(second.Body, `filename="report.pdf"`) || !strings.Contains(second.Body, "{{file.file}}") {
		t.Fatalf("multipart body did not preserve file placeholder: %s", second.Body)
	}
	if len(result.Script.DataSources) != 1 {
		t.Fatalf("data sources = %#v, want one HAR file asset", result.Script.DataSources)
	}
	if got := result.Script.DataSources[0]; got.Name != "file" || got.Type != "file" || got.FileName != "report.pdf" || got.ContentType != "application/pdf" {
		t.Fatalf("file data source = %#v, want HAR file metadata", got)
	}
	if !containsSampleDiagnostic(result, "HAR multipart file") {
		t.Fatalf("diagnostics = %#v, want multipart file warning", result.Unsupported)
	}
	if !containsSampleDiagnostic(result, "duplicate Set-Cookie headers") {
		t.Fatalf("diagnostics = %#v, want duplicate Set-Cookie info", result.Unsupported)
	}
	if !containsSampleDiagnostic(result, "HAR cookies[]") {
		t.Fatalf("diagnostics = %#v, want cookies[] info", result.Unsupported)
	}
}

func TestConvertHARExplicitCookieHeaderWinsAndSourceReviewRedacts(t *testing.T) {
	const (
		headerCanary = "har-cookie-header-canary-a46814"
		arrayCanary  = "har-cookie-array-canary-2e931f"
	)
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{"log":{"entries":[{
			"request":{
				"method":"GET",
				"url":"https://api.example.com/private",
				"headers":[{"name":"cOoKiE","value":"session=` + headerCanary + `;  theme=dark"}],
				"cookies":[{"name":"session","value":"` + arrayCanary + `"}]
			},
			"response":{"status":200,"headers":[],"content":{"text":"{}"}}
		}]}}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if got := headerListValues(action.HeaderList, "Cookie"); !slices.Equal(got, []string{"session=" + headerCanary + ";  theme=dark"}) {
		t.Fatalf("Cookie header occurrences = %#v, want exact explicit HAR header", got)
	}
	if action.Headers["cOoKiE"] != "session="+headerCanary+";  theme=dark" {
		t.Fatalf("Cookie compatibility header = %#v, want original casing and value", action.Headers)
	}
	if !containsSampleDiagnostic(result, "HAR cookies[]") {
		t.Fatalf("diagnostics = %#v, want explicit-header precedence diagnostic", result.Unsupported)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 {
		t.Fatalf("source review = %#v, want disclosure-safe source files", result.SourceReview)
	}
	for _, file := range result.SourceReview.SourceFiles {
		if !strings.Contains(file.Content, headerCanary) || !strings.Contains(file.Content, arrayCanary) {
			t.Fatalf("source review file %q lost a HAR cookie: %s", file.Path, file.Content)
		}
	}
}

func TestConvertHARFormParamsPreserveOrderDuplicatesAndEmptyText(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{"log":{"entries":[{
			"request":{
				"method":"POST",
				"url":"https://api.example.com/form",
				"headers":[],
				"postData":{
					"mimeType":"application/x-www-form-urlencoded",
					"params":[
						{"name":"z","value":"1"},
						{"name":"password","value":""},
						{"name":"z","value":"2"},
						{"name":"a","value":""}
					]
				}
			},
			"response":{"status":204,"headers":[],"content":{"text":""}}
		}]}}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got, want := result.Script.Actions[0].Body, "z=1&password=&z=2&a="; got != want {
		t.Fatalf("URL-encoded body = %q, want source-order bytes %q", got, want)
	}
	if len(result.Script.DataSources) != 0 {
		t.Fatalf("data sources = %#v, empty text fields must not become file assets", result.Script.DataSources)
	}
}

func TestConvertHARMultipartEmptyTextFieldIsNotAFile(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{"log":{"entries":[{
			"request":{
				"method":"POST",
				"url":"https://api.example.com/upload",
				"headers":[],
				"postData":{
					"mimeType":"multipart/form-data",
					"params":[
						{"name":"password","value":""},
						{"name":"note","value":""},
						{"name":"file","fileName":"report.pdf","contentType":"application/pdf"}
					]
				}
			},
			"response":{"status":201,"headers":[],"content":{"text":"{}"}}
		}]}}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	const boundary = "------apexload-har-boundary"
	for _, name := range []string{"password", "note"} {
		want := `Content-Disposition: form-data; name="` + name + "\"\r\n\r\n\r\n" + boundary
		if !strings.Contains(action.Body, want) {
			t.Fatalf("multipart body did not preserve %q as an empty text field: %q", name, action.Body)
		}
		if strings.Contains(action.Body, `name="`+name+`"; filename=`) || strings.Contains(action.Body, "{{file."+name+"}}") {
			t.Fatalf("multipart body treated empty text field %q as a file: %q", name, action.Body)
		}
	}
	if len(result.Script.DataSources) != 1 || result.Script.DataSources[0].Name != "file" {
		t.Fatalf("data sources = %#v, want only the explicitly named file part", result.Script.DataSources)
	}
}

func TestConvertPreservesWhitespaceOnlyRequestBodies(t *testing.T) {
	const body = " \t\n"
	tests := []struct {
		name    string
		format  string
		content string
	}{
		{
			name:   "HAR postData text",
			format: "har",
			content: `{"log":{"entries":[{
				"request":{
					"method":"POST",
					"url":"https://api.example.com/whitespace",
					"headers":[{"name":"Content-Type","value":"text/plain"}],
					"postData":{"mimeType":"text/plain","text":" \t\n"}
				},
				"response":{"status":204,"headers":[],"content":{"text":""}}
			}]}}`,
		},
		{
			name:    "raw HTTP body",
			format:  "raw_http",
			content: "POST /whitespace HTTP/1.1\r\nHost: api.example.com\r\nContent-Type: text/plain\r\n\r\n" + body,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inferExtractors := false
			result, err := Convert(ImportRequest{
				Format:  test.format,
				Content: test.content,
				Options: ConvertOptions{InferExtractors: &inferExtractors},
			})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if got := result.Script.Actions[0].Body; got != body {
				t.Fatalf("request body = %q, want exact whitespace bytes %q", got, body)
			}
		})
	}
}

func TestConvertHARMetadataDoesNotDuplicateQueryOrCookieCredentials(t *testing.T) {
	const queryCanary = "har-query-canary-814ac7"
	const cookieCanary = "har-cookie-canary-36d510"
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{"log":{"entries":[{
		  "request":{
		    "method":"GET",
		    "url":"https://api.example.com/orders?password=` + queryCanary + `",
		    "queryString":[{"name":"password","value":"` + queryCanary + `"}],
		    "headers":[]
		  },
		  "response":{
		    "status":200,
		    "headers":[],
		    "cookies":[{"name":"session","value":"` + cookieCanary + `","httpOnly":true}],
		    "content":{"text":"{}"}
		  }
		}]}}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	query, ok := action.Metadata["har.request.query_string"].([]map[string]any)
	if !ok || len(query) != 1 || query[0]["value"] != queryCanary {
		t.Fatalf("query metadata = %#v", action.Metadata["har.request.query_string"])
	}
	cookies, ok := action.Metadata["har.response.cookies"].([]map[string]any)
	if !ok || len(cookies) != 1 || cookies[0]["value"] != cookieCanary ||
		cookies[0]["httpOnly"] != true {
		t.Fatalf("cookie metadata = %#v", action.Metadata["har.response.cookies"])
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(action.Target, queryCanary) {
		t.Fatalf("executable target lost imported query credential: %q", action.Target)
	}
	if !strings.Contains(string(publicJSON), cookieCanary) {
		t.Fatalf("non-executable HAR cookie metadata was not preserved: %s", publicJSON)
	}
	if result.SourceReview == nil ||
		!strings.Contains(result.SourceReview.SourceFiles[0].Content, queryCanary) ||
		!strings.Contains(result.SourceReview.SourceFiles[0].Content, cookieCanary) {
		t.Fatalf("source review lost imported credential material: %#v", result.SourceReview)
	}
}

func TestConvertHARSkipsMalformedEntriesWithIndexedWarning(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{"log":{"entries":[
			{"request":{"method":"GET","url":"https://api.example.com/ok","headers":[]},"response":{"status":200,"headers":[],"content":{"text":"{}"}}},
			{"response":{"status":200,"headers":[],"content":{"text":"{}"}}}
		]}}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.TotalRequests != 2 || result.Stats.ConvertedActions != 1 || result.Stats.SkippedRequests != 1 {
		t.Fatalf("stats = %#v, want one converted and one skipped", result.Stats)
	}
	if !containsSampleDiagnostic(result, "HAR entry skipped") || !strings.Contains(strings.Join(result.Warnings, "\n"), "HAR entry 2") {
		t.Fatalf("diagnostics=%#v warnings=%v, want indexed skip warning", result.Unsupported, result.Warnings)
	}
	if containsSampleDiagnostic(result, "Sample completeness guard") {
		t.Fatalf("expected skipped entry not to trip completeness guard: %#v", result.Unsupported)
	}
}

func TestConvertHARStaticFilterIsMethodAware(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{"log":{"entries":[
			{"request":{"method":"POST","url":"https://api.example.com/avatar","headers":[]},"response":{"status":201,"headers":[],"content":{"mimeType":"image/png","text":"created"}}},
			{"request":{"method":"GET","url":"https://api.example.com/app.js","headers":[]},"response":{"status":200,"headers":[],"content":{"mimeType":"application/javascript","text":"console.log(1)"}}}
		]}}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.TotalRequests != 2 || result.Stats.FilteredAssets != 1 || result.Stats.ConvertedActions != 1 {
		t.Fatalf("stats = %#v, want POST image retained and GET JS filtered", result.Stats)
	}
	if result.Script.Actions[0].Method != "POST" {
		t.Fatalf("retained method = %q, want POST image request", result.Script.Actions[0].Method)
	}
	if containsSampleDiagnostic(result, "Sample completeness guard") {
		t.Fatalf("static filter should not trip completeness guard: %#v", result.Unsupported)
	}
}

func TestConvertHARStripsReplayHostileHeadersButPreservesCookie(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{"log":{"entries":[{
			"request":{"method":"GET","url":"https://api.example.com/orders","headers":[
				{"name":":authority","value":"api.example.com"},
				{"name":"Connection","value":"keep-alive"},
				{"name":"Cookie","value":"sid=abc"},
				{"name":"Accept","value":"application/json"}
			]},
			"response":{"status":200,"headers":[],"content":{"text":"{}"}}
		}]}}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	for _, name := range []string{":authority", "Connection"} {
		if action.Headers[name] != "" || len(headerListValues(action.HeaderList, name)) != 0 {
			t.Fatalf("%s should be stripped: headers=%#v header_list=%#v", name, action.Headers, action.HeaderList)
		}
	}
	if action.Headers["Cookie"] != "sid=abc" {
		t.Fatalf("Cookie header = %q, want exact imported value", action.Headers["Cookie"])
	}
	if got := headerListValues(action.HeaderList, "Cookie"); !slices.Equal(got, []string{"sid=abc"}) {
		t.Fatalf("Cookie header occurrences = %#v, want exact imported occurrence", got)
	}
	if action.Headers["Accept"] != "application/json" {
		t.Fatalf("Accept header = %q, want preserved", action.Headers["Accept"])
	}
}

func TestConvertHARMultipartUsesExistingBoundary(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [{
					"request": {
						"method": "POST",
						"url": "https://api.example.com/upload",
						"headers": [{"name":"Content-Type","value":"multipart/form-data; boundary=----custom-har-boundary"}],
						"postData": {
							"mimeType": "multipart/form-data",
							"params": [{"name":"file","fileName":"report.pdf","contentType":"application/pdf"}]
						}
					},
					"response": {"status": 201, "headers": [], "content": {"text": "{}"}}
				}]
			}
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if got := action.Headers["Content-Type"]; got != "multipart/form-data; boundary=----custom-har-boundary" {
		t.Fatalf("Content-Type = %q, want custom boundary preserved", got)
	}
	if !strings.Contains(action.Body, "------custom-har-boundary\r\n") || !strings.Contains(action.Body, "------custom-har-boundary--\r\n") {
		t.Fatalf("multipart body = %q, want custom boundary", action.Body)
	}
}

// TestConvertHARMultipartSynthesizesBoundaryInHeaderList guards B1 on the HAR
// path: a boundary-less multipart Content-Type must get its synthesized boundary
// reflected in the ordered HeaderList that replay consumes.
func TestConvertHARMultipartSynthesizesBoundaryInHeaderList(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [{
					"request": {
						"method": "POST",
						"url": "https://api.example.com/upload",
						"headers": [{"name":"Content-Type","value":"multipart/form-data"}],
						"postData": {
							"mimeType": "multipart/form-data",
							"params": [{"name":"file","fileName":"report.pdf","contentType":"application/pdf"}]
						}
					},
					"response": {"status": 201, "headers": [], "content": {"text": "{}"}}
				}]
			}
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	listCT := headerListContentType(t, action.HeaderList)
	if !strings.Contains(listCT, "boundary=") {
		t.Fatalf("HeaderList Content-Type = %q, want a synthesized boundary (replay uses HeaderList)", listCT)
	}
	if listCT != action.Headers["Content-Type"] {
		t.Fatalf("HeaderList Content-Type %q out of sync with map %q", listCT, action.Headers["Content-Type"])
	}
	_, params, _ := mime.ParseMediaType(listCT)
	if b := params["boundary"]; b == "" || !strings.Contains(action.Body, "--"+b+"\r\n") {
		t.Fatalf("body does not use the HeaderList boundary %q: %q", b, action.Body)
	}
}

func TestConvertHARPreservesDuplicateRequestHeaders(t *testing.T) {
	inferExtractors := false
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [{
					"request": {
						"method": "GET",
						"url": "https://api.example.com/orders",
						"headers": [
							{"name":"X-Duplicate","value":"one"},
							{"name":"X-Duplicate","value":"two"}
						]
					},
					"response": {"status": 200, "headers": [], "content": {"text": "{}"}}
				}]
			}
		}`,
		Options: ConvertOptions{InferExtractors: &inferExtractors},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Headers["X-Duplicate"] != "two" {
		t.Fatalf("summary X-Duplicate = %q, want last effective occurrence", action.Headers["X-Duplicate"])
	}
	got := headerListValues(action.HeaderList, "X-Duplicate")
	if strings.Join(got, ",") != "one,two" {
		t.Fatalf("header_list X-Duplicate = %#v, want one/two", got)
	}
}

func containsSampleDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}

func headerListValues(headers dsl.HeaderList, name string) []string {
	values := []string{}
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			values = append(values, header.Value)
		}
	}
	return values
}

func gzipBase64(t *testing.T, payload []byte) string {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("gzip write returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close returned error: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestConvertSourceReviewPreservesSecretsWhenSanitizeDisabled(t *testing.T) {
	const (
		authValue     = "Basic dXNlcjpodW50ZXIyc2VjcmV0"
		passwordValue = "hunter2-secret"
	)
	tests := []struct {
		name    string
		format  string
		content string
	}{
		{
			name:   "har",
			format: "har",
			content: `{
				"log": {
					"entries": [
						{
							"startedDateTime": "2026-01-01T00:00:00.000Z",
							"request": {
								"method": "POST",
								"url": "https://api.example.com/v1/login",
								"headers": [
									{"name": "Content-Type", "value": "application/json"},
									{"name": "Authorization", "value": "` + authValue + `"}
								],
								"postData": {
									"mimeType": "application/json",
									"text": "{\"username\":\"alice\",\"password\":\"` + passwordValue + `\"}"
								}
							},
							"response": {
								"status": 200,
								"headers": [],
								"content": {"mimeType": "application/json", "text": "{}"}
							}
						}
					]
				}
			}`,
		},
		{
			name:    "raw_http",
			format:  "raw_http",
			content: "POST /v1/login HTTP/1.1\nHost: api.example.com\nContent-Type: application/json\nAuthorization: " + authValue + "\n\n{\"username\":\"alice\",\"password\":\"" + passwordValue + "\"}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sanitizeSensitive := false
			result, err := Convert(ImportRequest{
				Format:  tc.format,
				Content: tc.content,
				Options: ConvertOptions{SanitizeSensitive: &sanitizeSensitive},
			})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if len(result.Script.Actions) != 1 {
				t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
			}
			action := result.Script.Actions[0]
			if action.Headers["Authorization"] != authValue {
				t.Fatalf("authorization header = %q, want raw value preserved when sanitize is off", action.Headers["Authorization"])
			}
			if !strings.Contains(action.Body, passwordValue) {
				t.Fatalf("body = %q, want raw password preserved when sanitize is off", action.Body)
			}
			if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 {
				t.Fatalf("source review = %#v, want files", result.SourceReview)
			}
			for _, file := range result.SourceReview.SourceFiles {
				if !strings.Contains(file.Content, "dXNlcjpodW50ZXIyc2VjcmV0") {
					t.Fatalf("source review file %s lost Authorization value: %s", file.Path, file.Content)
				}
				if !strings.Contains(file.Content, passwordValue) {
					t.Fatalf("source review file %s lost password: %s", file.Path, file.Content)
				}
				if file.Redacted {
					t.Fatalf("source review file %s was marked redacted", file.Path)
				}
			}
		})
	}
}

func TestConvertRawHTTPSourceReviewPreservesDuplicateSensitiveHeaderListValues(t *testing.T) {
	const (
		first  = "Bearer first-duplicate-secret"
		second = "Bearer second-duplicate-secret"
	)
	sanitizeSensitive := false
	content := "GET /private HTTP/1.1\nHost: api.example.com\nAuthorization: " + first +
		"\nAuthorization: " + second + "\n\n"
	result, err := Convert(ImportRequest{
		Format:  "raw_http",
		Content: content,
		Options: ConvertOptions{SanitizeSensitive: &sanitizeSensitive},
	})
	if err != nil {
		t.Fatalf("Convert duplicate sensitive headers: %v", err)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 {
		t.Fatalf("source review = %#v, want files", result.SourceReview)
	}
	for _, file := range result.SourceReview.SourceFiles {
		if !strings.Contains(file.Content, first) || !strings.Contains(file.Content, second) {
			t.Fatalf("source review %q lost duplicate sensitive header: %q", file.Path, file.Content)
		}
	}
}

func TestRedactExchangeSecretsAppliesReplacementsNonRecursively(t *testing.T) {
	exchanges := []exchange{{
		HeaderList: dsl.HeaderList{
			{Name: "Authorization", Value: "A"},
			{Name: "X-Api-Key", Value: "D"},
		},
	}}
	got, changed := redactExchangeSecrets("A D", exchanges)
	if !changed {
		t.Fatal("redactExchangeSecrets reported unchanged")
	}
	if want := "[REDACTED] [REDACTED]"; got != want {
		t.Fatalf("redactExchangeSecrets = %q, want non-recursive %q", got, want)
	}
}

func TestRedactExchangeSecretsBoundsExpandingSensitiveHeaderReplacement(t *testing.T) {
	content := strings.Repeat("x", sourceview.MaxFileChars)
	got, changed := redactExchangeSecrets(content, []exchange{{
		Headers: map[string]string{"Authorization": "x"},
	}})
	if !changed {
		t.Fatal("redactExchangeSecrets reported unchanged")
	}
	if len(got) > sourceview.MaxFileChars+1 {
		t.Fatalf("redacted source retained %d bytes, want at most %d", len(got), sourceview.MaxFileChars+1)
	}
	if strings.Contains(got, "x") {
		t.Fatal("redacted source retained raw one-byte credential")
	}
}

// One value owned by an applied and a review-only correlation stays a
// collision: the review-only owner keeps its literal, so neither is applied.
func TestPrepareCorrelationApplicationKeepsAValueAReviewOnlyOwnerShares(t *testing.T) {
	exchanges := []exchange{
		{URL: "https://api.example.test/source-a"},
		{URL: "https://api.example.test/source-b"},
		{URL: "https://api.example.test/use?token=token-123456"},
	}
	correlations := []Correlation{
		{Name: "corr_a", Source: "body", ExtractType: "regex", Expression: `(token-123456)`, Value: "token-123456", FoundInStep: 1, UsedInSteps: []int{3}},
		{Name: "corr_b", Source: "body", ExtractType: "regex", Expression: `(token-123456)`, Value: "token-123456", FoundInStep: 2, UsedInSteps: []int{3}, reviewOnly: true},
	}
	vars, _, _ := prepareCorrelationApplication(exchanges, correlations)
	if len(vars) != 0 || correlations[0].PreApplied || correlations[1].PreApplied {
		t.Fatalf("vars = %#v, correlations = %#v; want the shared value left for review", vars, correlations)
	}
}

// A review-only correlation is never rewritten automatically, so its literal
// containing, or contained in, an applied one is no collision: the applied
// value is replaced wherever it occurs, inside the longer literal too.
func TestPrepareCorrelationApplicationAppliesAroundReviewOnlyContainment(t *testing.T) {
	for _, tc := range []struct {
		name            string
		applied, review Correlation
		use             string
	}{
		{
			name:    "applied inside review-only",
			applied: Correlation{Name: "corr_cshc", Source: "body", ExtractType: "regex", Expression: `(CSHCabc123def456)`, Value: "CSHCabc123def456", FoundInStep: 1, UsedInSteps: []int{3, 4}},
			review:  Correlation{Name: "corr_location", Source: "body", ExtractType: "regex", Expression: `(/verify\?cshc=CSHCabc123def456&r=1)`, Value: "/verify?cshc=CSHCabc123def456&r=1", FoundInStep: 2, UsedInSteps: []int{3}, reviewOnly: true},
			use:     "https://api.example.test/verify?cshc=CSHCabc123def456&r=1",
		},
		{
			name:    "review-only inside applied",
			applied: Correlation{Name: "corr_token", Source: "body", ExtractType: "regex", Expression: `(00D5g000ORG!TOKENabc123)`, Value: "00D5g000ORG!TOKENabc123", FoundInStep: 1, UsedInSteps: []int{3, 4}},
			review:  Correlation{Name: "corr_orgid", Source: "body", ExtractType: "regex", Expression: `(00D5g000ORG)`, Value: "00D5g000ORG", FoundInStep: 2, UsedInSteps: []int{3}, reviewOnly: true},
			use:     "https://api.example.test/use?token=00D5g000ORG!TOKENabc123",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exchanges := []exchange{
				{URL: "https://api.example.test/source-a"},
				{URL: "https://api.example.test/source-b"},
				{URL: tc.use},
				{URL: "https://api.example.test/again?v=" + tc.applied.Value},
			}
			correlations := []Correlation{tc.applied, tc.review}
			vars, _, _ := prepareCorrelationApplication(exchanges, correlations)
			if vars[tc.applied.Value] != "{{var."+tc.applied.Name+"}}" || len(vars) != 1 {
				t.Fatalf("vars = %#v, want only %s applied", vars, tc.applied.Name)
			}
			if !correlations[0].PreApplied || correlations[1].PreApplied {
				t.Fatalf("applied = %#v, review-only = %#v", correlations[0], correlations[1])
			}
		})
	}
}
