package browser

import (
	"context"
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
	if matches := b.matchWSEvents(browserWSMatcher{URLContains: "live", Direction: "received", Match: `"ready"`, MatchMode: "contains", MinSeq: 2}); len(matches) != 0 {
		t.Fatalf("matches with MinSeq=2 = %+v, want no stale frame", matches)
	}
	b.advanceWSWatermark([]browserWSEvent{{Seq: 2}})
	if got := b.currentWSWatermark(); got != 2 {
		t.Fatalf("ws watermark = %d, want 2", got)
	}
	b.resetWSEvents()
	if got := b.currentWSWatermark(); got != 0 || len(b.wsEvents) != 0 || b.wsSeq != 0 {
		t.Fatalf("reset ws state = watermark %d seq %d events %d", got, b.wsSeq, len(b.wsEvents))
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

func TestBrowserWSWaitThenAssertCanReadSameFrame(t *testing.T) {
	b := &Browser{}
	b.recordWSEvent("wss://shop.example/live", "received", []byte(`{"type":"ready"}`))

	waitAction := &protocol.Action{
		Type: "browser_ws_wait",
		Params: map[string]any{
			"url_contains": "live",
			"direction":    "received",
			"match":        `"ready"`,
		},
	}
	waitResult := &protocol.Result{}
	if err, fail := b.dispatchWSWait(context.Background(), waitAction, waitResult); err != nil || fail {
		t.Fatalf("dispatchWSWait err=%v fail=%v", err, fail)
	}
	if got := b.currentWSWatermark(); got != 1 {
		t.Fatalf("watermark after wait = %d, want 1", got)
	}

	assertResult := &protocol.Result{}
	if err, fail := b.dispatchWSAssert(&protocol.Action{
		Type:   "browser_ws_assert",
		Params: waitAction.Params,
	}, assertResult); err != nil || fail {
		t.Fatalf("dispatchWSAssert err=%v fail=%v", err, fail)
	}
	if got := b.currentWSWatermark(); got != 1 {
		t.Fatalf("watermark after assert = %d, want unchanged 1", got)
	}
	matches, ok := assertResult.ProtocolEvidence["browser_ws_matches"].([]browserWSEvent)
	if !ok || len(matches) != 1 || matches[0].Seq != 1 {
		t.Fatalf("assert matches = %#v, want original wait frame", assertResult.ProtocolEvidence["browser_ws_matches"])
	}
}
