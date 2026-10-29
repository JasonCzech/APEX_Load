package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

const (
	browserWSRingLimit         = 256
	browserWSPayloadLimitBytes = 4096
	// Whole-frame UTF-8 validation is bounded. Beyond this limit the retained
	// prefix determines the encoding, so unseen binary bytes may be classified
	// as text and a prefix split inside a rune remains base64.
	browserWSTextValidationBytes = 64 << 10
	browserWSURLLimitBytes       = browserNetworkURLLimitBytes
	defaultBrowserWSWaitTimeout  = 10 * time.Second
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
	// matchPayload is a bounded private copy of the observed bytes (base64 for
	// binary frames). It must never cross the result-evidence boundary.
	matchPayload  string
	matchEncoding string
	urlTruncated  bool
	epoch         uint64
}

// Each page observer belongs to one browser context. A reset keeps its
// sockets live, while teardown retires the context generation.
func (b *Browser) observePageWebSockets(page playwright.Page, pageGeneration uint64) {
	if page == nil {
		return
	}
	page.OnWebSocket(func(ws playwright.WebSocket) {
		epoch, ok := b.wsEpochForPage(pageGeneration)
		if !ok {
			return
		}
		url := ws.URL()
		b.recordWSEventAt(epoch, pageGeneration, url, "open", nil)
		recordFrame := func(direction string, payload []byte) {
			frameEpoch, valid := b.wsEpochForPage(pageGeneration)
			if valid {
				b.recordWSEventAt(frameEpoch, pageGeneration, url, direction, payload)
			}
		}
		ws.OnFrameSent(func(payload []byte) { recordFrame("sent", payload) })
		ws.OnFrameReceived(func(payload []byte) { recordFrame("received", payload) })
	})
}

func (b *Browser) recordWSEvent(url, direction string, payload []byte) {
	b.wsMu.Lock()
	epoch, pageGeneration := b.wsEpoch, b.wsPageGeneration
	b.wsMu.Unlock()
	b.recordWSEventAt(epoch, pageGeneration, url, direction, payload)
}

func (b *Browser) wsEpochForPage(pageGeneration uint64) (uint64, bool) {
	b.wsMu.Lock()
	defer b.wsMu.Unlock()
	return b.wsEpoch, pageGeneration == b.wsPageGeneration
}

func (b *Browser) recordWSEventAt(epoch, pageGeneration uint64, url, direction string, payload []byte) {
	// Reject stale handlers before performing projection work, and recheck at
	// publication because reset/teardown can run while that work is blocked.
	b.wsMu.Lock()
	current := b.wsEpoch == epoch && b.wsPageGeneration == pageGeneration
	b.wsMu.Unlock()
	if !current {
		return
	}
	trimmedURL := strings.TrimSpace(url)
	boundedURL := boundedtext.TruncateUTF8(trimmedURL, browserWSURLLimitBytes)
	event := browserWSEvent{
		URL:          boundedURL,
		urlTruncated: boundedURL != trimmedURL,
		epoch:        epoch,
		Direction:    strings.ToLower(strings.TrimSpace(direction)),
		Timestamp:    time.Now(),
	}
	if len(payload) > 0 {
		body := payload
		event.SizeBytes = len(payload)
		if len(body) > browserWSPayloadLimitBytes {
			body = body[:browserWSPayloadLimitBytes]
			event.Truncated = true
		}
		textFrame := utf8.Valid(body)
		if len(payload) <= browserWSTextValidationBytes {
			// A valid retained prefix does not imply a valid whole frame: binary
			// bytes can occur after the retained portion.
			textFrame = utf8.Valid(payload)
		}
		if event.Truncated && textFrame && !utf8.Valid(body) {
			// A valid text frame can be cut in the middle of its last rune. Trim
			// only that incomplete tail after validating the full frame.
			prefix := body
			for removed := 0; removed < utf8.UTFMax-1 && len(prefix) > 0 && !utf8.Valid(prefix); removed++ {
				prefix = prefix[:len(prefix)-1]
			}
			if utf8.Valid(prefix) {
				body = prefix
			}
		}
		if textFrame {
			event.matchPayload = string(body)
			event.matchEncoding = "text"
		} else {
			event.matchPayload = base64.StdEncoding.EncodeToString(body)
			event.matchEncoding = "base64"
		}
	}

	b.wsMu.Lock()
	defer b.wsMu.Unlock()
	if b.wsEpoch != epoch || b.wsPageGeneration != pageGeneration {
		return
	}
	b.wsSeq++
	event.Seq = b.wsSeq
	b.wsEvents = append(b.wsEvents, event)
	if len(b.wsEvents) > browserWSRingLimit {
		copy(b.wsEvents, b.wsEvents[len(b.wsEvents)-browserWSRingLimit:])
		clear(b.wsEvents[browserWSRingLimit:])
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
	useWatermark := matcher.MinSeq == 0
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		b.wsMu.Lock()
		currentMatcher := matcher
		if useWatermark {
			currentMatcher.MinSeq = b.wsWatermark
		}
		matches := b.matchWSEventsLocked(currentMatcher)
		epoch := b.wsEpoch
		b.wsMu.Unlock()
		if len(matches) >= count {
			evidence, projection, valid := b.wsEvidenceAt(matches, epoch)
			published := false
			committed := valid && b.commitWSEvidence(epoch, projection, func() {
				if waitCtx.Err() != nil {
					return
				}
				b.wsLastWaitMinSeq, b.wsLastWaitValid = currentMatcher.MinSeq, true
				for _, match := range matches {
					if match.Seq > b.wsWatermark {
						b.wsWatermark = match.Seq
					}
				}
				result.ProtocolEvidence = map[string]any{"browser_ws_matches": evidence}
				published = true
			})
			if committed && published {
				return nil, false
			}
		}
		select {
		case <-waitCtx.Done():
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("browser_ws_wait interrupted: %w", err), false
			}
			if waitCtx.Err() == context.Canceled {
				return fmt.Errorf("browser_ws_wait canceled: %w", waitCtx.Err()), false
			}
			// A reset may have retired the snapshot while projection was in
			// progress. Never publish evidence from that earlier generation.
			evidence, projection, valid := b.wsEvidenceAt(matches, epoch)
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("browser_ws_wait interrupted: %w", err), false
			}
			if valid {
				b.commitWSEvidence(epoch, projection, func() {
					if ctx.Err() == nil {
						result.ProtocolEvidence = map[string]any{"browser_ws_matches": evidence}
					}
				})
			}
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("browser_ws_wait interrupted: %w", err), false
			}
			return fmt.Errorf("browser_ws_wait timed out after %s waiting for %d matching frame(s)", timeout, count), false
		case <-ticker.C:
		}
	}
}

func (b *Browser) dispatchWSAssert(ctx context.Context, action *protocol.Action, result *protocol.Result) (error, bool) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("browser_ws_assert interrupted: %w", err), false
	}
	count := positiveActionParamInt(action.Params, "count", 1)
	matcher, err := browserWSMatcherFromAction(action)
	if err != nil {
		return nil, failBrowserAction(result, err.Error())
	}
	explicitMinSeq := matcher.MinSeq > 0
	b.wsMu.Lock()
	if matcher.MinSeq == 0 {
		matcher.MinSeq = b.wsWatermark
	}
	matches := b.matchWSEventsLocked(matcher)
	if len(matches) < count && !explicitMinSeq {
		if minSeq, ok := b.wsLastWaitMinSeq, b.wsLastWaitValid; ok && minSeq < matcher.MinSeq {
			waitMatcher := matcher
			waitMatcher.MinSeq = minSeq
			if waitMatches := b.matchWSEventsLocked(waitMatcher); len(waitMatches) >= count {
				matches = waitMatches
			}
		}
	}
	epoch := b.wsEpoch
	b.wsMu.Unlock()
	evidence, projection, valid := b.wsEvidenceAt(matches, epoch)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("browser_ws_assert interrupted: %w", err), false
	}
	published := false
	committed := valid && b.commitWSEvidence(epoch, projection, func() {
		if ctx.Err() == nil {
			result.ProtocolEvidence = map[string]any{"browser_ws_matches": evidence}
			published = true
		}
	})
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("browser_ws_assert interrupted: %w", err), false
	}
	if !committed || !published {
		return fmt.Errorf("browser_ws_assert observation invalidated by reset"), false
	}
	if len(matches) < count {
		return fmt.Errorf("browser_ws_assert found %d matching frame(s), want at least %d", len(matches), count), false
	}
	return nil, false
}

// wsEvidence creates detached JSON-shaped evidence. Private matcher payloads
// never cross the result boundary except as the explicit debugger projection.
func (b *Browser) wsEvidence(matches []browserWSEvent) any {
	if len(matches) == 0 {
		return []any{}
	}
	evidence, projection, valid := b.wsEvidenceAt(matches, matches[0].epoch)
	if !valid || !b.commitWSEvidence(matches[0].epoch, projection, func() {}) {
		return nil
	}
	return evidence
}

func (b *Browser) wsEvidenceAt(matches []browserWSEvent, epoch uint64) ([]any, wsTextProjection, bool) {
	b.wsMu.Lock()
	current := b.wsEpoch == epoch
	b.wsMu.Unlock()
	if !current {
		return nil, wsTextProjection{}, false
	}
	projection := b.wsProjectionSnapshot()
	out := make([]any, 0, len(matches))
	for _, event := range matches {
		url := event.URL
		urlOmitted := false
		if !projection.debug {
			var ok bool
			if !event.urlTruncated {
				url, ok = projection.redact(url)
			}
			if !ok {
				url = ""
				urlOmitted = true
			}
		}
		entry := map[string]any{"seq": event.Seq, "url": url, "direction": event.Direction, "timestamp": event.Timestamp.Format(time.RFC3339Nano)}
		if event.urlTruncated {
			entry["url_truncated"] = true
		}
		if urlOmitted {
			entry["url_omitted"] = true
		}
		payload, encoding := "", ""
		if projection.debug {
			payload, encoding = event.matchPayload, event.matchEncoding
		} else if event.matchEncoding != "" && !event.Truncated {
			// Re-project the original rather than the earlier redacted result:
			// secrets may become known after the frame was recorded, and an early
			// replacement could otherwise hide only part of a newly known secret.
			original := event.matchPayload
			if event.matchEncoding == "base64" {
				decoded, err := base64.StdEncoding.DecodeString(original)
				if err != nil {
					continue
				}
				original = string(decoded)
			}
			if safe, ok := projection.redact(original); ok {
				if utf8.ValidString(safe) {
					payload, encoding = safe, "text"
				} else {
					payload, encoding = base64.StdEncoding.EncodeToString([]byte(safe)), "base64"
				}
			}
		}
		if encoding != "" {
			entry["payload"] = payload
			entry["encoding"] = encoding
		} else if event.matchEncoding != "" {
			// Binary and unavailable projections are omitted rather than encoding
			// bytes that could conceal credentials from the runtime redactor.
			entry["payload_omitted"] = true
		}
		if event.SizeBytes != 0 {
			entry["size_bytes"] = event.SizeBytes
		}
		if event.Truncated {
			entry["truncated"] = true
		}
		out = append(out, entry)
	}
	b.wsMu.Lock()
	current = b.wsEpoch == epoch
	b.wsMu.Unlock()
	if !current {
		return nil, projection, false
	}
	return out, projection, true
}

// commitWSEvidence establishes the policy and generation fences at the same
// publication boundary as the wait watermark. Lock order is policy fence,
// then mu, then wsMu; no projector or policy updater runs inside the commit.
func (b *Browser) commitWSEvidence(epoch uint64, projection wsTextProjection, publish func()) (committed bool) {
	defer func() {
		if recover() != nil {
			committed = false
		}
	}()
	attempted := false
	commit := func() bool {
		if attempted {
			return false
		}
		attempted = true
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.wsProjectionRevision != projection.revision {
			return false
		}
		b.wsMu.Lock()
		defer b.wsMu.Unlock()
		if b.wsEpoch != epoch {
			return false
		}
		publish()
		committed = true
		return true
	}
	if projection.guard == nil {
		return commit()
	}
	return projection.guard(commit) && committed
}

type browserWSMatcher struct {
	URLContains string
	Direction   string
	Match       string
	MatchMode   string
	MatchRegex  *regexp.Regexp
	// interiorRegex requires one observed rune after a match. This allows the
	// regexp engine to find an interior alternative to a greedy edge match.
	interiorRegex *regexp.Regexp
	// truncatedRegexSafe identifies expressions that do not depend on the
	// character following a match at the end of a retained prefix.
	truncatedRegexSafe bool
	MinSeq             int64
}

func browserWSMatcherFromAction(action *protocol.Action) (browserWSMatcher, error) {
	matcher := browserWSMatcher{
		URLContains: actionParamString(action.Params, "url_contains", ""),
		Direction:   strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "direction", ""))),
		Match:       actionParamString(action.Params, "match", ""),
		MatchMode:   strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "match_mode", "contains"))),
		MinSeq:      bwsActionMinSeq(action),
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
		compiled, err := regexp.Compile(matcher.Match)
		if err != nil {
			return matcher, fmt.Errorf("%s params.match regex is invalid: %w", action.Type, err)
		}
		matcher.MatchRegex = compiled
		matcher.truncatedRegexSafe = browserWSRegexPrefixStable(compiled)
		if !matcher.truncatedRegexSafe {
			matcher.interiorRegex, _ = regexp.Compile("(?:" + matcher.Match + ")(?s:.)")
		}
	}
	return matcher, nil
}

func browserWSRegexPrefixStable(re *regexp.Regexp) bool {
	if re == nil {
		return false
	}
	parsed, err := syntax.Parse(re.String(), syntax.Perl)
	if err != nil {
		return false
	}
	var stable func(*syntax.Regexp) bool
	stable = func(node *syntax.Regexp) bool {
		switch node.Op {
		case syntax.OpEndLine, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
			return false
		}
		for _, child := range node.Sub {
			if !stable(child) {
				return false
			}
		}
		return true
	}
	return stable(parsed)
}

func browserWSTruncatedRegexMatches(event browserWSEvent, matcher browserWSMatcher) bool {
	if matcher.MatchRegex == nil {
		return false
	}
	prefix := event.matchPayload
	if event.matchEncoding == "base64" {
		// The final quartet may change when the unseen raw suffix is encoded.
		prefix = prefix[:max(0, len(prefix)-4)]
	}
	match := matcher.MatchRegex.FindStringIndex(prefix)
	if match == nil {
		return false
	}
	// A match strictly inside the prefix has its real following character, so
	// even end-of-line and word-boundary assertions have their true context.
	// At the boundary, only expressions without such assertions are safe.
	if match[1] < len(prefix) || matcher.truncatedRegexSafe {
		return true
	}
	// A greedy first match may depend on the artificial end, even though a
	// shorter match has real context. Requiring a following rune finds that
	// alternative in a single bounded RE2 search, without changing anchors.
	if matcher.interiorRegex != nil {
		return matcher.interiorRegex.MatchString(prefix)
	}
	return false
}

func bwsActionMinSeq(action *protocol.Action) int64 {
	if action == nil {
		return 0
	}
	if seq := actionParamInt(action.Params, "since_seq", 0); seq > 0 {
		return int64(seq)
	}
	return 0
}

func (b *Browser) matchWSEvents(matcher browserWSMatcher) []browserWSEvent {
	b.wsMu.Lock()
	defer b.wsMu.Unlock()
	return b.matchWSEventsLocked(matcher)
}

func (b *Browser) matchWSEventsLocked(matcher browserWSMatcher) []browserWSEvent {
	matches := make([]browserWSEvent, 0)
	for _, event := range b.wsEvents {
		if matcher.MinSeq > 0 && event.Seq <= matcher.MinSeq {
			continue
		}
		if matcher.URLContains != "" && !strings.Contains(event.URL, matcher.URLContains) {
			continue
		}
		if matcher.Direction != "" && event.Direction != matcher.Direction {
			continue
		}
		// Equality cannot be proven from a retained prefix of a larger frame.
		if matcher.Match != "" {
			if event.Truncated && matcher.MatchMode == "exact" {
				continue
			}
			if event.Truncated && matcher.MatchMode == "regex" {
				if !browserWSTruncatedRegexMatches(event, matcher) {
					continue
				}
			} else if event.Truncated && event.matchEncoding == "base64" && (matcher.MatchMode == "" || matcher.MatchMode == "contains") {
				// The final base64 quartet represents an incomplete group of raw
				// bytes and can change when the unseen suffix is appended.
				stableLength := max(0, len(event.matchPayload)-4)
				if !strings.Contains(event.matchPayload[:stableLength], matcher.Match) {
					continue
				}
			} else if !browserWSPayloadMatches(event.matchPayload, matcher.Match, matcher.MatchMode, matcher.MatchRegex) {
				continue
			}
		}
		matches = append(matches, event)
	}
	return matches
}

func (b *Browser) currentWSWatermark() int64 {
	b.wsMu.Lock()
	defer b.wsMu.Unlock()
	return b.wsWatermark
}

func (b *Browser) advanceWSWatermark(matches []browserWSEvent) {
	if len(matches) == 0 {
		return
	}
	maxSeq := int64(0)
	for _, match := range matches {
		if match.Seq > maxSeq {
			maxSeq = match.Seq
		}
	}
	b.wsMu.Lock()
	if maxSeq > b.wsWatermark {
		b.wsWatermark = maxSeq
	}
	b.wsMu.Unlock()
}

func (b *Browser) resetWSEvents() {
	b.wsMu.Lock()
	b.wsEpoch++
	b.clearWSEventsLocked()
	b.wsMu.Unlock()
}

// Teardown also retires all handlers from the closing browser context.
func (b *Browser) retireWSEvents() {
	b.wsMu.Lock()
	b.wsEpoch++
	b.wsPageGeneration++
	b.clearWSEventsLocked()
	b.wsMu.Unlock()
}

func (b *Browser) clearWSEventsLocked() {
	b.wsEvents = nil
	b.wsSeq = 0
	b.wsWatermark = 0
	b.wsLastWaitMinSeq = 0
	b.wsLastWaitValid = false
}

func browserWSPayloadMatches(payload, expected, mode string, compiled *regexp.Regexp) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "exact":
		return payload == expected
	case "regex":
		if compiled == nil {
			return false
		}
		return compiled.MatchString(payload)
	default:
		return strings.Contains(payload, expected)
	}
}
