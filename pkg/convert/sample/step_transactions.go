package sample

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// maxStepTransactionNameBytes bounds the page label in a step transaction's
// name.
const maxStepTransactionNameBytes = 48

var stepTransactionLabelUnsafe = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// stepTransactionWindows divides a recording without marked transactions into
// user steps, one transaction each, as a tester marks them in VuGen: a step
// starts at the first request and after each recorded pause longer than the
// think-time threshold, where the user read or typed and where the generated
// script also pauses. Redirects and the requests a page fires on its own stay
// in the step that caused them. Each step is named for the page most of its
// requests belong to (the Lightning page an Aura request names in
// aura.pageURI, or the page a navigation loaded), numbered in recording order
// (01_login, 02_home, 03_Customer_Search). Requests without a recorded start
// time cannot be placed, so a recording with any returns none.
func stepTransactionWindows(exchanges []exchange, threshold time.Duration) []TransactionWindow {
	if len(exchanges) == 0 {
		return nil
	}
	for _, ex := range exchanges {
		if !ex.HasStartedAt {
			return nil
		}
	}
	var windows []TransactionWindow
	var labels map[string]int
	var firstLabel string
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
		boundary := index == 0
		if index > 0 {
			if _, paused := thinkTimeAfterThreshold(exchanges, index-1, threshold); paused {
				boundary = true
			}
		}
		if boundary {
			closeStep()
			number := len(windows) + 1
			windows = append(windows, TransactionWindow{Start: ex.StartedAt, StartOrder: 2 * number, EndOrder: 2*number + 1})
			labels = map[string]int{}
			firstLabel = stepTransactionLabel(ex.URL)
		}
		windows[len(windows)-1].End = ex.StartedAt
		if label := stepPageLabel(ex); label != "" {
			labels[label]++
		}
	}
	closeStep()
	return windows
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
// path segment without an extension, safe for a transaction name; "root" for
// the site root.
func stepTransactionLabel(rawURL string) string {
	source := rawURL
	if parsed, err := url.Parse(rawURL); err == nil {
		source = parsed.Path
	}
	trimmed := strings.TrimRight(source, "/")
	if trimmed == "" {
		return "root"
	}
	segment := path.Base(trimmed)
	if extension := path.Ext(segment); extension != "" && extension != segment {
		segment = strings.TrimSuffix(segment, extension)
	}
	label := strings.Trim(stepTransactionLabelUnsafe.ReplaceAllString(segment, "_"), "_")
	if label == "" {
		label = "step"
	}
	if len(label) > maxStepTransactionNameBytes {
		label = label[:maxStepTransactionNameBytes]
	}
	return label
}
