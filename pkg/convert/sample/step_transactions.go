package sample

import (
	"container/heap"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// maxStepTransactionNameBytes bounds the page label in a step transaction's
// name.
const maxStepTransactionNameBytes = 48

var stepTransactionLabelUnsafe = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// stepTransactionWindows divides a recording without marked transactions into
// user steps, one transaction each, as a tester marks them in VuGen: a step
// starts at the first request, after each recorded pause longer than the
// think-time threshold (where the user read or typed and the generated script
// also pauses), and at each forced boundary (the end of a sign-in that moves to
// init). A pause is the time since the latest request finished, counted only
// when no other request is in flight, so a page load that waits on a slow
// response stays one step; a request held open longer than
// stepLongHeldRequest (a long poll, a download) does not count as in flight,
// so it does not hold the step open across later clicks, and its end counts
// only while its own step is the current one. Redirects and the requests a page fires on
// its own stay in the step that caused them.
// Requests in skip (background requests the recorded shape folds into
// conversations) neither start nor join a step, and a forced boundary on one
// moves to the next request kept. Each step is named for the page most of its
// requests belong to (the Lightning page an Aura request names in
// aura.pageURI, or the page a navigation loaded), numbered in recording order
// (01_login, 02_home, 03_Customer_Search). A request listed after a later one
// (recorders append in completion order) joins the step it is listed, and so
// sent, in; the steps stay disjoint. member gives each exchange's window, or -1. Without windows,
// reason says why: a request has no recorded start time, or every request
// folds into the background.
func stepTransactionWindows(exchanges []exchange, threshold time.Duration, skip, forced map[int]bool) (windows []TransactionWindow, member []int, reason string) {
	if len(exchanges) == 0 {
		return nil, nil, ""
	}
	for _, ex := range exchanges {
		if !ex.HasStartedAt {
			return nil, nil, "some requests have no recorded start time, so the steps cannot be placed"
		}
	}
	member = make([]int, len(exchanges))
	var labels map[string]int
	var firstLabel string
	// inFlight holds the ends of kept requests not yet finished; pending
	// counts those that are not long-held; finished is the latest end.
	var inFlight stepEndHeap
	var finished time.Time
	pending := 0
	pendingForced := false
	closeStep := func() {
		if len(windows) == 0 {
			return
		}
		label, best := firstLabel, 0
		for candidate, count := range labels {
			if count > best || (count == best && candidate < label) {
				label, best = candidate, count
			}
		}
		number := len(windows)
		windows[number-1].Name = fmt.Sprintf("%02d_%s", number, label)
	}
	for index, ex := range exchanges {
		member[index] = -1
		if skip[index] {
			pendingForced = pendingForced || forced[index]
			continue
		}
		duration := time.Duration(ex.DurationMs * float64(time.Millisecond))
		// Requests that finished by now set the latest end; one still in
		// flight holds the step open unless it is long-held. A long-held
		// request's end counts only while its own step is the current one:
		// a long poll from an earlier step that ends during the user's pause
		// does not shorten it.
		for inFlight.Len() > 0 && !inFlight[0].end.After(ex.StartedAt) {
			done := heap.Pop(&inFlight).(stepRequestEnd)
			if done.longHeld {
				if done.window == len(windows)-1 && done.end.After(finished) {
					finished = done.end
				}
				continue
			}
			if done.end.After(finished) {
				finished = done.end
			}
			pending--
		}
		longHeld := duration > stepLongHeldRequest
		push := func() {
			heap.Push(&inFlight, stepRequestEnd{end: ex.StartedAt.Add(duration), longHeld: longHeld, window: member[index]})
			if !longHeld {
				pending++
			}
		}
		if len(windows) > 0 && !ex.StartedAt.After(windows[len(windows)-1].End) && !forced[index] && !pendingForced {
			// Listed late (recorders append in completion order): it joins the
			// step the script sends it in (stepWindowAt).
			member[index] = stepWindowAt(windows, ex.StartedAt, threshold)
			push()
			if label := stepPageLabel(ex); label != "" {
				labels[label]++
			}
			continue
		}
		boundary := len(windows) == 0 || forced[index] || pendingForced
		pendingForced = false
		if !boundary && pending == 0 && !finished.IsZero() {
			// pending counts the other requests still in flight.
			pause := ex.StartedAt.Sub(finished)
			if ex.HasRecordedThinkTime && (index == 0 || !skip[index-1]) {
				// The recorder measured the pause before this request from the
				// previous one, which the step kept.
				pause = time.Duration(ex.RecordedThinkTimeMs) * time.Millisecond
			}
			boundary = pause > threshold
		}
		if boundary {
			closeStep()
			number := len(windows) + 1
			// A forced boundary listed late starts after the previous step, so
			// the steps stay disjoint; stepMarkerStarts places its request.
			start := ex.StartedAt
			if len(windows) > 0 && !start.After(windows[len(windows)-1].End) {
				start = windows[len(windows)-1].End.Add(time.Nanosecond)
			}
			windows = append(windows, TransactionWindow{Start: start, End: start, StartOrder: 2 * number, EndOrder: 2*number + 1})
			labels = map[string]int{}
			firstLabel = stepTransactionLabel(ex.URL)
		} else {
			windows[len(windows)-1].End = ex.StartedAt
		}
		member[index] = len(windows) - 1
		push()
		if label := stepPageLabel(ex); label != "" {
			labels[label]++
		}
	}
	closeStep()
	if len(windows) == 0 {
		return nil, nil, "every request folds into a background conversation, so there is no user step to time"
	}
	return windows, member, ""
}

// stepWindowAt is the window a late-listed request that started at joins:
// the current (last) one, since the script sends requests in listed order and
// so sends it in that step. When it started in the pause before that step
// (more than threshold after the previous step's last start) it is the step's
// own first request, and the step's start moves back to it; otherwise the
// windows keep their spans, so they stay disjoint and no step times the
// user's pause.
func stepWindowAt(windows []TransactionWindow, at time.Time, threshold time.Duration) int {
	current := len(windows) - 1
	if at.Before(windows[current].Start) {
		if current == 0 || at.Sub(windows[current-1].End) > threshold {
			windows[current].Start = at
		}
	}
	return current
}

// stepMarkerStarts is, for each request that started outside the step it
// joined (listed late), a start inside that step's span, so the transaction
// markers place it within the step the script sends it in.
func stepMarkerStarts(exchanges []exchange, windows []TransactionWindow, member []int) map[int]time.Time {
	var starts map[int]time.Time
	for index, window := range member {
		if window < 0 || index >= len(exchanges) {
			continue
		}
		at := exchanges[index].StartedAt
		if at.Before(windows[window].Start) || at.After(windows[window].End) {
			if starts == nil {
				starts = map[int]time.Time{}
			}
			starts[index] = windows[window].End
		}
	}
	return starts
}

// stepRequestEnd is when a kept request ends, whether it is long-held, and
// the step it joined.
type stepRequestEnd struct {
	end      time.Time
	longHeld bool
	window   int
}

// stepEndHeap is a min-heap of request ends.
type stepEndHeap []stepRequestEnd

func (h stepEndHeap) Len() int           { return len(h) }
func (h stepEndHeap) Less(i, j int) bool { return h[i].end.Before(h[j].end) }
func (h stepEndHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *stepEndHeap) Push(value any)    { *h = append(*h, value.(stepRequestEnd)) }
func (h *stepEndHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

// stepLongHeldRequest is the longest a request may run and still hold its
// step open: a longer one (a long poll, a download) runs in the background of
// the steps after it.
const stepLongHeldRequest = 10 * time.Second

// exchangesCarryRecordedTransactions reports a recording whose requests the
// recorder already tagged with the transactions the tester marked.
func exchangesCarryRecordedTransactions(exchanges []exchange) bool {
	for _, ex := range exchanges {
		if name, _ := ex.Metadata[harSourceTransactionMeta].(string); name != "" {
			return true
		}
	}
	return false
}

// tagStepTransactions tags each action, one per exchange, with its step.
func tagStepTransactions(actions []dsl.Action, windows []TransactionWindow, member []int) {
	for index := range actions {
		if index >= len(member) || member[index] < 0 {
			continue
		}
		if actions[index].Metadata == nil {
			actions[index].Metadata = map[string]any{}
		}
		actions[index].Metadata[harSourceTransactionMeta] = windows[member[index]].Name
	}
}

// stepPageLabel is the page a request shows, as a transaction label: the
// Lightning page an Aura request names, or the page a navigation (a request
// whose response is HTML) loaded; "" for any other request.
func stepPageLabel(ex exchange) string {
	if pageURI := auraPageURI(ex); pageURI != "" {
		return stepTransactionLabel(pageURI)
	}
	if ex.Response == nil {
		return ""
	}
	for name, value := range ex.Response.Headers {
		if strings.EqualFold(name, "Content-Type") && strings.Contains(strings.ToLower(value), "text/html") {
			return stepTransactionLabel(ex.URL)
		}
	}
	return ""
}

// auraPageURI is the path of the aura.pageURI an Aura form post sends, or "".
func auraPageURI(ex exchange) string {
	if !strings.EqualFold(ex.Method, "POST") || !strings.Contains(ex.Body, "aura.pageURI=") || len(ex.Body) > 8<<20 {
		return ""
	}
	parsed, err := url.Parse(ex.URL)
	if err != nil {
		return ""
	}
	trimmed := strings.TrimRight(parsed.Path, "/")
	if trimmed != "/aura" && !strings.HasSuffix(trimmed, "/aura") {
		return ""
	}
	for _, field := range strings.Split(ex.Body, "&") {
		name, value, _ := strings.Cut(field, "=")
		if name != "aura.pageURI" {
			continue
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			return ""
		}
		pageURL, err := url.Parse(decoded)
		if err != nil {
			return ""
		}
		return pageURL.Path
	}
	return ""
}

// stepTransactionLabel makes a URL or path a transaction label: its last
// path segment that is a name rather than an identifier (a record id, a
// Salesforce identity URL's org and user ids), without an extension, safe for
// a transaction name; "root" for the site root.
func stepTransactionLabel(rawURL string) string {
	source := rawURL
	if parsed, err := url.Parse(rawURL); err == nil {
		source = parsed.Path
	}
	segments := strings.Split(strings.Trim(source, "/"), "/")
	if len(segments) == 1 && segments[0] == "" {
		return "root"
	}
	for position := len(segments) - 1; position >= 0; position-- {
		segment := segments[position]
		if extension := path.Ext(segment); extension != "" && extension != segment {
			segment = strings.TrimSuffix(segment, extension)
		}
		label := strings.Trim(stepTransactionLabelUnsafe.ReplaceAllString(segment, "_"), "_")
		if label == "" || stepIdentifierSegment(label) {
			continue
		}
		if len(label) > maxStepTransactionNameBytes {
			label = label[:maxStepTransactionNameBytes]
		}
		return label
	}
	return "step"
}

// stepIdentifierSegment reports a path segment that is a value rather than a
// name: an identifier shape carrying at least five digits (a record id,
// numeric id, UUID or hex token).
func stepIdentifierSegment(segment string) bool {
	digits := 0
	for index := 0; index < len(segment); index++ {
		if segment[index] >= '0' && segment[index] <= '9' {
			digits++
		}
	}
	return digits >= 5 && stepIdentifierShape.MatchString(segment)
}

var stepIdentifierShape = regexp.MustCompile(`^(?:[A-Za-z0-9]{15}|[A-Za-z0-9]{18}|[0-9A-Fa-f_]{16,}|[0-9]{5,})$`)
