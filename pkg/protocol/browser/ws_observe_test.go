package browser

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserWSMatcherFromAction(t *testing.T) {
	action := &protocol.Action{
		Type: "browser_ws_wait",
		Params: map[string]any{
			"url_contains": "live",
			"direction":    "received",
			"match":        `"ready"`,
			"match_mode":   "contains",
		},
	}
	matcher, err := browserWSMatcherFromAction(action)
	if err != nil {
		t.Fatalf("browserWSMatcherFromAction() error: %v", err)
	}
	if matcher.URLContains != "live" || matcher.Direction != "received" || matcher.Match != `"ready"` || matcher.MatchMode != "contains" {
		t.Fatalf("matcher = %+v", matcher)
	}

	action.Params["match_mode"] = "[broken"
	if _, err := browserWSMatcherFromAction(action); err == nil {
		t.Fatal("invalid match_mode accepted")
	}
	action.Params["match_mode"] = "regex"
	if _, err := browserWSMatcherFromAction(action); err != nil {
		t.Fatalf("valid regex mode rejected: %v", err)
	}
	action.Params["match"] = "[broken"
	if _, err := browserWSMatcherFromAction(action); err == nil {
		t.Fatal("invalid regex accepted")
	}
}

func TestBrowserMatchWSEvents(t *testing.T) {
	b := &Browser{
		wsEvents: []browserWSEvent{
			{Seq: 1, URL: "wss://shop.example/live", Direction: "open", Timestamp: time.Now()},
			{Seq: 2, URL: "wss://shop.example/live", Direction: "received", Payload: `{"type":"ready"}`, Timestamp: time.Now()},
			{Seq: 3, URL: "wss://shop.example/chat", Direction: "sent", Payload: `{"type":"ping"}`, Timestamp: time.Now()},
		},
	}

	matches := b.matchWSEvents(browserWSMatcher{
		URLContains: "live",
		Direction:   "received",
		Match:       `"ready"`,
		MatchMode:   "contains",
	})
	if len(matches) != 1 || matches[0].Seq != 2 {
		t.Fatalf("matches = %+v, want received ready frame", matches)
	}
	if !browserWSPayloadMatches(`{"type":"ready"}`, `"type":"ready"`, "contains") {
		t.Fatal("contains payload match failed")
	}
	if !browserWSPayloadMatches(`{"type":"ready"}`, `^\{"type":"ready"\}$`, "regex") {
		t.Fatal("regex payload match failed")
	}
	if browserWSPayloadMatches(`{"type":"ready"}`, `ready`, "exact") {
		t.Fatal("exact payload match accepted partial payload")
	}
}
