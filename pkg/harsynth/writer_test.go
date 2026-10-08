package harsynth

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func compileHARSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile("testdata/har-1.2.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("har.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("har.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

// ValidateHAR checks raw against the HAR 1.2 test schema.
func validateHAR(t *testing.T, raw []byte) {
	t.Helper()
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("HAR is not JSON: %v", err)
	}
	if err := compileHARSchema(t).Validate(instance); err != nil {
		t.Fatalf("HAR 1.2 schema: %v", err)
	}
}

func phase(name string, start, end float64) protocol.BrowserResourcePhase {
	return protocol.BrowserResourcePhase{Name: name, StartMS: f64(start), EndMS: f64(end)}
}

// fixtureEntries covers a fully measured request, a reused connection, a
// failed request, a redirect, a legacy entry without evidence and one with no
// timing at all.
func fixtureEntries() []Entry {
	record := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	start := float64(record.Add(-2*time.Second).UnixMilli()) + 0.5
	full := &protocol.BrowserResourceEvidence{Version: 3, RequestID: "r1", EpochStartMS: f64(start), TimingSource: "playwright", State: "finished",
		Phases: []protocol.BrowserResourcePhase{phase("dns", 0, 4), phase("connect", 4, 30), phase("tls", 12, 30), phase("request_wait", 30, 80), phase("response", 80, 95)},
		Bytes:  protocol.BrowserResourceBytes{RequestHeaders: i64(410), RequestBody: i64(0), ResponseHeaders: i64(220), ResponseEncodedBody: i64(5120)}}
	reused := &protocol.BrowserResourceEvidence{Version: 2, RequestID: "r2", EpochStartMS: f64(start + 100), TimingSource: "playwright", State: "finished",
		Phases: []protocol.BrowserResourcePhase{{Name: "dns", UnavailableReason: "reused_connection"}, {Name: "connect", UnavailableReason: "reused_connection"}, {Name: "tls", UnavailableReason: "reused_connection"}, phase("request_wait", 0, 40), phase("response", 40, 41)}}
	failed := &protocol.BrowserResourceEvidence{Version: 2, RequestID: "r3", EpochStartMS: f64(start + 200), State: "failed", Phases: []protocol.BrowserResourcePhase{phase("dns", 0, 2)}}
	return []Entry{
		{RecordTime: record, ActionName: "Open home", PageURL: "https://shop.example/", BrowserEngine: "chromium", Network: protocol.NetworkEntry{BrowserEvidence: full, URL: "https://shop.example/?q=a%20b&page=2&q=c", Method: "GET", StatusCode: 200, ResourceType: "document", DurationMs: 95,
			RequestHeaders: map[string]string{"accept": "text/html", "User-Agent": "test"}, ResponseHeaders: map[string]string{"Content-Type": "text/html; charset=utf-8"}}},
		{RecordTime: record, ActionName: "Open home", BrowserEngine: "chromium", Network: protocol.NetworkEntry{BrowserEvidence: reused, URL: "https://shop.example/app.js", Method: "GET", StatusCode: 200, ResourceType: "script", DurationMs: 41}},
		{RecordTime: record, ActionName: "Checkout", BrowserEngine: "chromium", Network: protocol.NetworkEntry{BrowserEvidence: failed, URL: "https://api.shop.example/cart", Method: "POST", FailureKind: "transport", ResourceType: "fetch", RequestBody: `{"sku":"abc"}`}},
		{RecordTime: record, ActionName: "Checkout", BrowserEngine: "webkit", Network: protocol.NetworkEntry{URL: "https://shop.example/old", Method: "GET", StatusCode: 302, DurationMs: 12, ResponseHeaders: map[string]string{"Location": "https://shop.example/new"}}},
		{RecordTime: record.Add(time.Second), ActionName: "Checkout", Network: protocol.NetworkEntry{URL: "https://shop.example/new", Method: "GET", StatusCode: 200}},
	}
}

func writeHAR(t *testing.T, meta Metadata, entries []Entry) ([]byte, Summary) {
	t.Helper()
	var out bytes.Buffer
	w := NewWriter(&out, meta)
	for _, entry := range entries {
		if err := w.Add(entry); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), summary
}

type decodedHAR struct {
	Log struct {
		Version string `json:"version"`
		Entries []struct {
			StartedDateTime string             `json:"startedDateTime"`
			Time            float64            `json:"time"`
			Timings         map[string]float64 `json:"timings"`
			Request         struct {
				Method      string                   `json:"method"`
				URL         string                   `json:"url"`
				Headers     []map[string]string      `json:"headers"`
				QueryString []map[string]string      `json:"queryString"`
				PostData    map[string]any           `json:"postData"`
				HeadersSize int64                    `json:"headersSize"`
				BodySize    int64                    `json:"bodySize"`
				Cookies     []map[string]interface{} `json:"cookies"`
			} `json:"request"`
			Response struct {
				Status      int            `json:"status"`
				Content     map[string]any `json:"content"`
				RedirectURL string         `json:"redirectURL"`
				HeadersSize int64          `json:"headersSize"`
				BodySize    int64          `json:"bodySize"`
			} `json:"response"`
			Evidence map[string]any `json:"_apexload"`
		} `json:"entries"`
		Apexload map[string]any `json:"_apexload"`
	} `json:"log"`
}

func decode(t *testing.T, raw []byte) decodedHAR {
	t.Helper()
	var har decodedHAR
	if err := json.Unmarshal(raw, &har); err != nil {
		t.Fatal(err)
	}
	return har
}

func TestSyntheticHARIsValidHAR12(t *testing.T) {
	raw, summary := writeHAR(t, Metadata{CaptureMode: CaptureRetainedNetworkEntries, CreatorVersion: "test"}, fixtureEntries())
	validateHAR(t, raw)
	if summary.Entries != 5 || summary.Bytes != int64(len(raw)) || len(summary.SHA256) != 64 || summary.Completeness != CompletenessPartial {
		t.Fatalf("summary = %+v (len %d)", summary, len(raw))
	}
	har := decode(t, raw)
	block := har.Log.Apexload
	if block["fidelity"] != "synthetic" || block["capture_mode"] != CaptureRetainedNetworkEntries || block["completeness"] != CompletenessPartial || block["entry_count"] != float64(5) || block["body_policy"] != "omitted" || block["algorithm"] != Algorithm || block["algorithm_version"] != float64(1) || block["schema_version"] != "1.2" {
		t.Fatalf("_apexload = %#v", block)
	}
	if fields, _ := block["omitted_fields"].([]any); len(fields) != len(OmittedFields) {
		t.Fatalf("omitted_fields = %#v", block["omitted_fields"])
	}
	if engines, _ := block["browser_engines"].([]any); len(engines) != 2 || engines[0] != "chromium" || engines[1] != "webkit" {
		t.Fatalf("browser_engines = %#v", block["browser_engines"])
	}
}

func TestSyntheticHARNeverInventsTimingsOrContent(t *testing.T) {
	raw, _ := writeHAR(t, Metadata{CaptureMode: CaptureRetainedNetworkEntries}, fixtureEntries())
	har := decode(t, raw)
	full := har.Log.Entries[0]
	want := map[string]float64{"blocked": -1, "dns": 4, "connect": 26, "ssl": 18, "send": -1, "wait": 50, "receive": 15}
	for name, value := range want {
		if full.Timings[name] != value {
			t.Fatalf("full timings[%s] = %v, want %v (%v)", name, full.Timings[name], value, full.Timings)
		}
	}
	// ssl is inside connect: time is dns+connect+wait+receive.
	if full.Time != 95 || full.Evidence["time_source"] != TimeTimings || full.Evidence["started_source"] != StartedRequestStart || full.Evidence["wait_includes_send"] != true {
		t.Fatalf("full entry = %v %#v", full.Time, full.Evidence)
	}
	if full.StartedDateTime != "2026-10-07T11:59:58.0005Z" {
		t.Fatalf("startedDateTime = %q, want the measured request start", full.StartedDateTime)
	}
	if full.Request.HeadersSize != 410 || full.Request.BodySize != 0 || full.Response.HeadersSize != 220 || full.Response.BodySize != 5120 || full.Response.Content["size"] != float64(-1) || full.Response.Content["mimeType"] != "text/html; charset=utf-8" {
		t.Fatalf("sizes = %+v %+v", full.Request, full.Response)
	}
	if len(full.Request.QueryString) != 3 || full.Request.QueryString[0]["value"] != "a b" || full.Request.QueryString[1]["name"] != "page" || full.Request.QueryString[2]["value"] != "c" {
		t.Fatalf("queryString = %v", full.Request.QueryString)
	}
	if len(full.Request.Headers) != 2 || full.Request.Headers[0]["name"] != "User-Agent" || full.Request.Headers[1]["name"] != "accept" {
		t.Fatalf("headers = %v", full.Request.Headers)
	}
	reused := har.Log.Entries[1]
	if reused.Timings["dns"] != -1 || reused.Timings["connect"] != -1 || reused.Timings["ssl"] != -1 || reused.Timings["wait"] != 40 || reused.Timings["receive"] != 1 || reused.Time != 41 {
		t.Fatalf("reused timings = %v time %v", reused.Timings, reused.Time)
	}
	failed := har.Log.Entries[2]
	if failed.Response.Status != 0 || failed.Evidence["failure_kind"] != "transport" || failed.Evidence["state"] != "failed" || failed.Timings["wait"] != -1 || failed.Timings["receive"] != -1 || failed.Time != 2 {
		t.Fatalf("failed entry = %+v", failed)
	}
	if failed.Request.PostData != nil || bytes.Contains(raw, []byte(`"sku"`)) || bytes.Contains(raw, []byte(`"text"`)) {
		t.Fatal("a request or response body was written")
	}
	legacy := har.Log.Entries[3]
	for name, value := range legacy.Timings {
		if value != -1 {
			t.Fatalf("legacy timings[%s] = %v, want -1", name, value)
		}
	}
	if legacy.Time != 12 || legacy.Evidence["time_source"] != TimeEntryDuration || legacy.Evidence["started_source"] != StartedRecordTime || legacy.StartedDateTime != "2026-10-07T12:00:00Z" || legacy.Response.RedirectURL != "https://shop.example/new" || legacy.Request.HeadersSize != -1 || legacy.Response.BodySize != -1 {
		t.Fatalf("legacy entry = %+v", legacy)
	}
	bare := har.Log.Entries[4]
	if bare.Time != 0 || bare.Evidence["time_source"] != TimeUnavailable {
		t.Fatalf("an entry without any duration claims time %v (%v)", bare.Time, bare.Evidence)
	}
}

func TestSyntheticHAROmitsUnwritableEntriesByReason(t *testing.T) {
	entries := fixtureEntries()[:1]
	entries = append(entries,
		Entry{RecordTime: time.Now(), Network: protocol.NetworkEntry{URL: "/relative", Method: "GET"}},
		Entry{RecordTime: time.Now(), Network: protocol.NetworkEntry{URL: "https://x.example/", Method: ""}},
		Entry{Network: protocol.NetworkEntry{URL: "https://x.example/", Method: "GET"}},
	)
	var out bytes.Buffer
	w := NewWriter(&out, Metadata{CaptureMode: CaptureReplayNetworkEntries})
	for _, entry := range entries {
		if err := w.Add(entry); err != nil {
			t.Fatal(err)
		}
	}
	w.Omit(OmittedCoveredByHAR)
	summary, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	validateHAR(t, out.Bytes())
	want := map[string]int64{OmittedInvalidURL: 1, OmittedNoMethod: 1, OmittedNoStartTime: 1, OmittedCoveredByHAR: 1}
	if summary.Entries != 1 || len(summary.Omitted) != len(want) || summary.Completeness != CompletenessPartial {
		t.Fatalf("summary = %+v", summary)
	}
	for reason, count := range want {
		if summary.Omitted[reason] != count {
			t.Fatalf("omitted[%s] = %d, want %d", reason, summary.Omitted[reason], count)
		}
	}
	block := decode(t, out.Bytes()).Log.Apexload
	if block["capture_mode"] != CaptureReplayNetworkEntries || block["omitted_entries"].(map[string]any)[OmittedNoStartTime] != float64(1) {
		t.Fatalf("_apexload = %#v", block)
	}
}

func TestSyntheticHARByteLimitTruncatesAndStaysValid(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out, Metadata{CaptureMode: CaptureRetainedNetworkEntries})
	w.maxBytes = trailerReserve + 2048
	added := 0
	for i := 0; i < 50; i++ {
		if err := w.Add(fixtureEntries()[0]); err != nil {
			t.Fatal(err)
		}
		added++
	}
	summary, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	validateHAR(t, out.Bytes())
	if summary.Entries == 0 || summary.Entries+summary.Omitted[OmittedByteLimit] != int64(added) || summary.Completeness != CompletenessTruncated || int64(out.Len()) > w.maxBytes {
		t.Fatalf("summary = %+v size %d", summary, out.Len())
	}
	reasons := decode(t, out.Bytes()).Log.Apexload["completeness_reasons"].([]any)
	if len(reasons) != 2 || reasons[1] != "writer_limit" {
		t.Fatalf("completeness_reasons = %v", reasons)
	}
}

func TestSyntheticHAREntryLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 100,001 entries")
	}
	entry := Entry{RecordTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Network: protocol.NetworkEntry{URL: "https://x.example/a", Method: "GET", StatusCode: 204}}
	var out bytes.Buffer
	w := NewWriter(&out, Metadata{CaptureMode: CaptureRetainedNetworkEntries})
	for i := 0; i <= MaxEntries; i++ {
		if err := w.Add(entry); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	if summary.Entries != MaxEntries || summary.Omitted[OmittedEntryLimit] != 1 || summary.Completeness != CompletenessTruncated {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestSyntheticHARIsDeterministic(t *testing.T) {
	first, a := writeHAR(t, Metadata{CaptureMode: CaptureRetainedNetworkEntries, CreatorVersion: "v"}, fixtureEntries())
	second, b := writeHAR(t, Metadata{CaptureMode: CaptureRetainedNetworkEntries, CreatorVersion: "v"}, fixtureEntries())
	if !bytes.Equal(first, second) || a.SHA256 != b.SHA256 {
		t.Fatal("the same evidence wrote different HARs")
	}
}

func TestSyntheticHAREmptyIsValid(t *testing.T) {
	raw, summary := writeHAR(t, Metadata{CaptureMode: CaptureRetainedNetworkEntries}, nil)
	validateHAR(t, raw)
	if summary.Entries != 0 || len(decode(t, raw).Log.Entries) != 0 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestSyntheticHARWriterRefusesUseAfterClose(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out, Metadata{})
	if _, err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(fixtureEntries()[0]); err == nil {
		t.Fatal("Add after Close succeeded")
	}
	if _, err := w.Close(); err == nil {
		t.Fatal("second Close succeeded")
	}
}

// Entries pass through the native HAR scrubber: credential headers, cookies
// and token-bearing query values never reach the document.
func TestSyntheticHARScrubsEveryEntry(t *testing.T) {
	entry := Entry{RecordTime: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), ActionName: "Login", PageURL: "https://shop.example/login?access_token=pageTokenQxZr", Network: protocol.NetworkEntry{
		URL: "https://shop.example/api?access_token=urlTokenQxZrTm&page=2", Method: "GET", StatusCode: 200,
		RequestHeaders:  map[string]string{"Authorization": "Bearer headerTokenQxZrTmWp", "Cookie": "sid=cookieValueQxZrTm", "Accept": "application/json"},
		ResponseHeaders: map[string]string{"Set-Cookie": "sid=setCookieValueQxZr; Path=/", "Content-Type": "application/json"},
	}}
	raw, summary := writeHAR(t, Metadata{CaptureMode: CaptureRetainedNetworkEntries}, []Entry{entry})
	validateHAR(t, raw)
	if summary.Entries != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	for _, secret := range []string{"pageTokenQxZr", "urlTokenQxZrTm", "headerTokenQxZrTmWp", "cookieValueQxZrTm", "setCookieValueQxZr"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("synthetic HAR retains %q: %s", secret, raw)
		}
	}
	har := decode(t, raw)
	if !bytes.Contains(raw, []byte(`"_apexloadSensitivity":"scrubbed"`)) || bytes.Count(raw, []byte(`_apexloadSensitivity`)) != 1 {
		t.Fatalf("sensitivity label: %s", raw)
	}
	if got := har.Log.Entries[0].Request.QueryString; len(got) != 2 || got[1]["value"] != "2" {
		t.Fatalf("queryString = %v", got)
	}
}

func TestSyntheticHARListsSourceSpecificOmittedFields(t *testing.T) {
	raw, _ := writeHAR(t, Metadata{CaptureMode: CaptureRetainedNetworkEntries, ExtraOmittedFields: []string{"request.headers", "response.headers", "request.cookies"}}, fixtureEntries()[:1])
	fields, _ := decode(t, raw).Log.Apexload["omitted_fields"].([]any)
	if len(fields) != len(OmittedFields)+2 || fields[len(fields)-2] != "request.headers" || fields[len(fields)-1] != "response.headers" {
		t.Fatalf("omitted_fields = %v", fields)
	}
}
