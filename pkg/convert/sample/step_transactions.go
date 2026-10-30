package sample

import (
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
// init). A pause is measured from the end of the step's requests so far, so a
// page load that waits on a slow response stays one step; a request held open
// longer than stepLongHeldRequest (a long poll, a download) does not hold the
// step open across later clicks. Redirects and the requests a page fires on
// its own stay in the step that caused them.
// Requests in skip (background requests the recorded shape folds into
// conversations) neither start nor join a step, and a forced boundary on one
// moves to the next request kept. Each step is named for the page most of its
// requests belong to (the Lightning page an Aura request names in
// aura.pageURI, or the page a navigation loaded), numbered in recording order
// (01_login, 02_home, 03_Customer_Search), and spans its requests' earliest to
// latest start. member gives each exchange's window, or -1. Without windows,
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
	var stepEnd time.Time
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
		boundary := len(windows) == 0 || forced[index] || pendingForced
		pendingForced = false
		// A request listed late but started within the current step's span
		// (recorders append in completion order) belongs to that step.
		if !boundary && ex.StartedAt.After(windows[len(windows)-1].End) {
			pause := ex.StartedAt.Sub(stepEnd)
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
			windows = append(windows, TransactionWindow{Start: ex.StartedAt, End: ex.StartedAt, StartOrder: 2 * number, EndOrder: 2*number + 1})
			labels = map[string]int{}
			firstLabel = stepTransactionLabel(ex.URL)
			stepEnd = ex.StartedAt
		}
		if end := ex.StartedAt.Add(duration); duration <= stepLongHeldRequest && end.After(stepEnd) {
			stepEnd = end
		}
		window := &windows[len(windows)-1]
		if ex.StartedAt.Before(window.Start) {
			window.Start = ex.StartedAt
		}
		if ex.StartedAt.After(window.End) {
			window.End = ex.StartedAt
		}
		member[index] = len(windows) - 1
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
