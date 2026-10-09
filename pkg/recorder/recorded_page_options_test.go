package recorder

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func pageOptionsSession() *Session {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &Session{Name: "pages", BaseURL: "https://shop.test", StartedAt: started, Requests: []RecordedRequest{
		{Seq: 1, Timestamp: started, Method: "GET", URL: "https://shop.test/home", Path: "/home", Host: "shop.test", StatusCode: 200,
			ResponseHeaders: map[string]string{"Content-Type": "text/html; charset=utf-8"}, ResponseBody: "<html><title> Shop Home </title></html>"},
		{Seq: 2, Timestamp: started.Add(10 * time.Millisecond), Method: "GET", URL: "https://shop.test/logo.png", Path: "/logo.png", Host: "shop.test", StatusCode: 200, IsStatic: true,
			Headers: map[string]string{"Referer": "https://shop.test/home"}},
		{Seq: 3, Timestamp: started.Add(time.Second), Method: "GET", URL: "https://shop.test/poll?ts=" + strconv.FormatInt(started.Add(time.Second).UnixMilli(), 10), Path: "/poll", QueryString: "ts=" + strconv.FormatInt(started.Add(time.Second).UnixMilli(), 10), Host: "shop.test", StatusCode: 200,
			ResponseHeaders: map[string]string{"Content-Type": "application/json"}, ResponseBody: `{"ok":true}`},
	}}
}

func TestRecorderPageTitleChecksResourcesAndTimestamps(t *testing.T) {
	result := ConvertSessionWithOptions(pageOptionsSession(), "pages", ConvertOptions{ResourceMode: "in_step"})
	if result.Script == nil || len(result.Script.Actions) != 2 {
		t.Fatalf("script = %+v", result.Script)
	}
	home := result.Script.Actions[0]
	foundTitle := false
	for _, assertion := range home.Assertions {
		foundTitle = foundTitle || (assertion.Type == "body_contains" && assertion.Value == "Shop Home")
	}
	if !foundTitle {
		t.Fatalf("home assertions = %+v, want a title check", home.Assertions)
	}
	resources, _ := home.Params["resources"].([]any)
	if len(resources) != 1 || resources[0].(map[string]any)["url"] != "https://shop.test/logo.png" {
		t.Fatalf("home resources = %v, want the logo attached in step", home.Params["resources"])
	}
	if poll := result.Script.Actions[1]; !strings.Contains(poll.Target, "{{fn.timestamp.unix_ms}}") {
		t.Fatalf("poll target = %q, want a fresh timestamp", poll.Target)
	}

	off := false
	plain := ConvertSessionWithOptions(pageOptionsSession(), "pages", ConvertOptions{PageTitleChecks: &off})
	for _, assertion := range plain.Script.Actions[0].Assertions {
		if assertion.Type == "body_contains" {
			t.Fatalf("title checks are off, got %+v", plain.Script.Actions[0].Assertions)
		}
	}
	if _, ok := plain.Script.Actions[0].Params["resources"]; ok {
		t.Fatal("the default resource mode must not attach resources")
	}
}
