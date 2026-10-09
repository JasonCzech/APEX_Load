package clienttimestamps

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestApplyReplacesClientTimestamps(t *testing.T) {
	captured := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	ms := func(offset time.Duration) string { return strconv.FormatInt(captured.Add(offset).UnixMilli(), 10) }
	sec := strconv.FormatInt(captured.Unix(), 10)
	server := ms(5 * time.Second)
	script := &dsl.Script{Actions: []dsl.Action{
		{Type: "http", Method: "GET", Target: "/data?_=" + ms(0) + "&id=42"},
		{Type: "http", Method: "GET", Target: "/poll?since=" + ms(time.Second)},
		{Type: "http", Method: "GET", Target: "/poll?since=" + ms(2*time.Second)},
		{Type: "http", Method: "POST", Target: "/log", Body: `{"ts":` + sec + `,"order":` + ms(3*time.Second) + `}`},
		{Type: "http", Method: "GET", Target: "/resume?token=" + server},
		{Type: "http", Method: "GET", Target: "/old?_=" + strconv.FormatInt(captured.Add(-48*time.Hour).UnixMilli(), 10)},
	}}
	evidence := Evidence{Server: map[string]bool{server: true}}
	evidence.Observe(captured)
	evidence.Observe(captured.Add(10 * time.Second))
	report := Apply(script, evidence)

	want := []string{
		"/data?_=" + Millis + "&id=42",
		"/poll?since=" + Millis,
		"/poll?since=" + Millis,
		"/log",
		"/resume?token=" + server,
		"/old?_=" + strconv.FormatInt(captured.Add(-48*time.Hour).UnixMilli(), 10),
	}
	for i, action := range script.Actions {
		if action.Target != want[i] {
			t.Errorf("action %d target = %q, want %q", i, action.Target, want[i])
		}
	}
	if body := script.Actions[3].Body; body != `{"ts":`+Seconds+`,"order":`+ms(3*time.Second)+`}` {
		t.Errorf("body = %q: a named timestamp key is replaced, a single order number is not", body)
	}
	if report.Replaced != 4 || strings.Join(report.Keys, ",") != "_,since,ts" {
		t.Errorf("report = %+v", report)
	}
	if !strings.Contains(report.Warning(), "4 occurrence(s)") {
		t.Errorf("warning = %q", report.Warning())
	}
}

func TestFromHAR(t *testing.T) {
	har := []byte(`{"log":{"entries":[
		{"startedDateTime":"2026-10-09T12:00:00.000Z","response":{"headers":[{"value":"exp=1791547200"}],"content":{"text":"eyJ0IjoxNzkxNTQ3MjAwMDAwfQ==","encoding":"base64"}}},
		{"startedDateTime":"2026-10-09T12:05:00.000Z","response":{"redirectURL":"/x?at=1791547300","content":{"text":"ok"}}}
	]}}`)
	evidence, err := FromHAR(har)
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range []string{"1791547200", "1791547200000", "1791547300"} {
		if !evidence.Server[number] {
			t.Errorf("server numbers miss %s: %v", number, evidence.Server)
		}
	}
	if evidence.To.Sub(evidence.From) != 5*time.Minute {
		t.Errorf("window = %v..%v", evidence.From, evidence.To)
	}
}

// A timestamp in a multipart text field is replaced like one in a body.
func TestApplyReplacesTimestampsInMultipartFields(t *testing.T) {
	captured := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	ms := func(offset time.Duration) string { return strconv.FormatInt(captured.Add(offset).UnixMilli(), 10) }
	upload := func(offset time.Duration) dsl.Action {
		return dsl.Action{Type: "http", Method: "POST", Target: "/upload", BodyMultipart: &dsl.MultipartBody{Parts: []dsl.MultipartPart{
			{Name: "meta", Value: `{"ts":` + ms(offset) + `}`},
			{Name: "file", Filename: "a.bin", Value: "{{file.upload_file}}"},
		}}}
	}
	script := &dsl.Script{Actions: []dsl.Action{upload(time.Second), upload(2 * time.Second)}}
	evidence := Evidence{Server: map[string]bool{}}
	evidence.Observe(captured)
	evidence.Observe(captured.Add(10 * time.Second))
	report := Apply(script, evidence)
	for i, action := range script.Actions {
		if got := action.BodyMultipart.Parts[0].Value; got != `{"ts":`+Millis+`}` {
			t.Errorf("action %d meta = %q", i, got)
		}
		if got := action.BodyMultipart.Parts[1].Value; got != "{{file.upload_file}}" {
			t.Errorf("action %d file part changed: %q", i, got)
		}
	}
	if report.Replaced != 2 {
		t.Errorf("report = %+v", report)
	}
}
