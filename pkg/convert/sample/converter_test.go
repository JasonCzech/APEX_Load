package sample

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"mime"
	"strings"
	"testing"

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
	if got := result.Script.Actions[0].Headers["Authorization"]; !strings.Contains(got, "{{secret.authorization}}") {
		t.Fatalf("expected authorization header to be sanitized, got %q", got)
	}
	if got := result.Script.Actions[0].Body; !strings.Contains(got, "{{secret.redacted}}") {
		t.Fatalf("expected password to be sanitized in body, got %q", got)
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
	if strings.Contains(result.SourceReview.SourceFiles[0].Content, "top-secret") || strings.Contains(result.SourceReview.SourceFiles[0].Content, "secret\"") {
		t.Fatalf("source review leaked sensitive content: %s", result.SourceReview.SourceFiles[0].Content)
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
	if action.Headers["X-Trace"] != "first second" {
		t.Fatalf("X-Trace = %q, want unfolded value", action.Headers["X-Trace"])
	}
	if got := headerListValues(action.HeaderList, "X-Trace"); strings.Join(got, ",") != "first second" {
		t.Fatalf("header_list X-Trace = %#v, want unfolded value", got)
	}
	if action.Metadata["http_version"] != "HTTP/1.0" {
		t.Fatalf("metadata = %#v, want HTTP/1.0", action.Metadata)
	}
	if got := action.Assertions[0].Expected; got != 200 {
		t.Fatalf("assertion expected = %#v, want 200 from decoded response", got)
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

func TestConvertHARDecompressesResponseBeforeCorrelation(t *testing.T) {
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
	if result.Stats.CorrelationsDetected != 1 {
		t.Fatalf("correlations = %d, want 1: %#v", result.Stats.CorrelationsDetected, result.Correlations)
	}
	if !strings.Contains(result.Script.Actions[1].Body, "{{var.corr_csrf_token}}") {
		t.Fatalf("second action body = %q, want token from decompressed response", result.Script.Actions[1].Body)
	}
	if !containsSampleDiagnostic(result, "HAR content-encoding") {
		t.Fatalf("diagnostics = %#v, want content-encoding info", result.Unsupported)
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
	if ext.Expression != "$.auth.access_token" || ext.OnMiss != "fail" {
		t.Fatalf("extractor = %#v, want nested JSONPath and fail-closed", ext)
	}
}

func TestConvertHARPreservesFormParamsStripsCookiesAndDuplicateHeaders(t *testing.T) {
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
	if first.Body != "page=1&q=widgets" {
		t.Fatalf("urlencoded body = %q", first.Body)
	}
	if first.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q", first.Headers["Content-Type"])
	}
	if first.Headers["Cookie"] != "" || len(headerListValues(first.HeaderList, "Cookie")) != 0 {
		t.Fatalf("recorded cookies should be stripped for jar replay: headers=%#v header_list=%#v", first.Headers, first.HeaderList)
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

func TestConvertHARStripsReplayHostileHeaders(t *testing.T) {
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
	for _, name := range []string{":authority", "Connection", "Cookie"} {
		if action.Headers[name] != "" || len(headerListValues(action.HeaderList, name)) != 0 {
			t.Fatalf("%s should be stripped: headers=%#v header_list=%#v", name, action.Headers, action.HeaderList)
		}
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
	if action.Headers["X-Duplicate"] != "one, two" {
		t.Fatalf("summary X-Duplicate = %q, want folded value", action.Headers["X-Duplicate"])
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

func TestConvertSourceReviewRedactsSecretsWhenSanitizeDisabled(t *testing.T) {
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
			redactedFiles := 0
			for _, file := range result.SourceReview.SourceFiles {
				if strings.Contains(file.Content, "dXNlcjpodW50ZXIyc2VjcmV0") {
					t.Fatalf("source review file %s leaked Authorization value: %s", file.Path, file.Content)
				}
				if strings.Contains(file.Content, passwordValue) {
					t.Fatalf("source review file %s leaked password: %s", file.Path, file.Content)
				}
				if file.Redacted {
					redactedFiles++
				}
			}
			if redactedFiles == 0 {
				t.Fatal("expected at least one source review file to be flagged as redacted")
			}
		})
	}
}
