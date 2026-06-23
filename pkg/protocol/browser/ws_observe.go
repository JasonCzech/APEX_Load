package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	playwright "github.com/playwright-community/playwright-go"
)

const (
	browserWSRingLimit          = 256
	browserWSPayloadLimitBytes  = 4096
	defaultBrowserWSWaitTimeout = 10 * time.Second
)

type browserWSEvent struct {
	Seq       int64     `json:"seq"`
	URL       string    `json:"url"`
	Direction string    `json:"direction"`
	Payload   string    `json:"payload,omitempty"`
	Encoding  string    `json:"encoding,omitempty"`
	SizeBytes int       `json:"size_bytes,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

func (b *Browser) observePageWebSockets(page playwright.Page) {
	if page == nil {
		return
	}
	page.OnWebSocket(func(ws playwright.WebSocket) {
		url := ws.URL()
		b.recordWSEvent(url, "open", nil)
		ws.OnFrameSent(func(payload []byte) { b.recordWSEvent(url, "sent", payload) })
		ws.OnFrameReceived(func(payload []byte) { b.recordWSEvent(url, "received", payload) })
	})
}

func (b *Browser) recordWSEvent(url, direction string, payload []byte) {
	event := browserWSEvent{
		URL:       strings.TrimSpace(url),
		Direction: strings.ToLower(strings.TrimSpace(direction)),
		Timestamp: time.Now(),
	}
	if len(payload) > 0 {
		body := payload
		event.SizeBytes = len(payload)
		if len(body) > browserWSPayloadLimitBytes {
			body = body[:browserWSPayloadLimitBytes]
			event.Truncated = true
		}
		text := scrub.Value(string(body), scrub.Policy{})
		if utf8.ValidString(text) {
			event.Payload = text
			event.Encoding = "text"
		} else {
			event.Payload = base64.StdEncoding.EncodeToString([]byte(text))
			event.Encoding = "base64"
		}
	}

	b.wsMu.Lock()
	defer b.wsMu.Unlock()
	b.wsSeq++
	event.Seq = b.wsSeq
	b.wsEvents = append(b.wsEvents, event)
	if len(b.wsEvents) > browserWSRingLimit {
		copy(b.wsEvents, b.wsEvents[len(b.wsEvents)-browserWSRingLimit:])
		b.wsEvents = b.wsEvents[:browserWSRingLimit]
	}
}

func (b *Browser) dispatchWSWait(ctx context.Context, action *protocol.Action, result *protocol.Result) (error, bool) {
	timeout := time.Duration(positiveActionParamInt(action.Params, "timeout_ms", int(defaultBrowserWSWaitTimeout/time.Millisecond))) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultBrowserWSWaitTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	count := positiveActionParamInt(action.Params, "count", 1)
	matcher, err := browserWSMatcherFromAction(action)
	if err != nil {
		return nil, failBrowserAction(result, err.Error())
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		matches := b.matchWSEvents(matcher)
		if len(matches) >= count {
			result.ProtocolEvidence = map[string]any{"browser_ws_matches": matches}
			return nil, false
		}
		select {
		case <-waitCtx.Done():
			result.ProtocolEvidence = map[string]any{"browser_ws_matches": matches}
			return fmt.Errorf("browser_ws_wait timed out after %s waiting for %d matching frame(s)", timeout, count), false
		case <-ticker.C:
		}
	}
}

func (b *Browser) dispatchWSAssert(action *protocol.Action, result *protocol.Result) (error, bool) {
	count := positiveActionParamInt(action.Params, "count", 1)
	matcher, err := browserWSMatcherFromAction(action)
	if err != nil {
		return nil, failBrowserAction(result, err.Error())
	}
	matches := b.matchWSEvents(matcher)
	result.ProtocolEvidence = map[string]any{"browser_ws_matches": matches}
	if len(matches) < count {
		return fmt.Errorf("browser_ws_assert found %d matching frame(s), want at least %d", len(matches), count), false
	}
	return nil, false
}

type browserWSMatcher struct {
	URLContains string
	Direction   string
	Match       string
	MatchMode   string
}

func browserWSMatcherFromAction(action *protocol.Action) (browserWSMatcher, error) {
	matcher := browserWSMatcher{
		URLContains: actionParamString(action.Params, "url_contains", ""),
		Direction:   strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "direction", ""))),
		Match:       actionParamString(action.Params, "match", ""),
		MatchMode:   strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "match_mode", "contains"))),
	}
	switch matcher.Direction {
	case "", "open", "sent", "received":
	default:
		return matcher, fmt.Errorf("%s params.direction must be open, sent, or received", action.Type)
	}
	switch matcher.MatchMode {
	case "", "contains", "exact", "regex":
	default:
		return matcher, fmt.Errorf("%s params.match_mode must be contains, exact, or regex", action.Type)
	}
	if matcher.MatchMode == "regex" && matcher.Match != "" {
		if _, err := regexp.Compile(matcher.Match); err != nil {
			return matcher, fmt.Errorf("%s params.match regex is invalid: %w", action.Type, err)
		}
	}
	return matcher, nil
}

func (b *Browser) matchWSEvents(matcher browserWSMatcher) []browserWSEvent {
	b.wsMu.Lock()
	events := append([]browserWSEvent(nil), b.wsEvents...)
	b.wsMu.Unlock()
	matches := make([]browserWSEvent, 0)
	for _, event := range events {
		if matcher.URLContains != "" && !strings.Contains(event.URL, matcher.URLContains) {
			continue
		}
		if matcher.Direction != "" && event.Direction != matcher.Direction {
			continue
		}
		if matcher.Match != "" && !browserWSPayloadMatches(event.Payload, matcher.Match, matcher.MatchMode) {
			continue
		}
		matches = append(matches, event)
	}
	return matches
}

func browserWSPayloadMatches(payload, expected, mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "exact":
		return payload == expected
	case "regex":
		matched, _ := regexp.MatchString(expected, payload)
		return matched
	default:
		return strings.Contains(payload, expected)
	}
}
