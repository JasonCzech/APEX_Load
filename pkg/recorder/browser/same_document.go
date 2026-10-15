package browser

import (
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	playwright "github.com/mxschmitt/playwright-go"
)

// An app that changes its URL inside the page (history.pushState, a fragment
// change: Salesforce Lightning moves between its pages this way) reaches
// Playwright as a frame navigation like any document load. Chromium's
// DevTools tells them apart with Page.navigatedWithinDocument, so each
// recorded page gets a DevTools session that marks those navigations. A
// script repeats the step that made one; loading its URL instead would reload
// the app.

// sameDocumentMatchWindow is how far apart Playwright's navigation event and
// DevTools' report of one in-page URL change may arrive, in either order.
const sameDocumentMatchWindow = 2 * time.Second

// maxPendingSameDocumentReports bounds the reports waiting for their
// navigation event.
const maxPendingSameDocumentReports = 32

// maxNavigationTypeBytes bounds Chromium's navigation type label.
const maxNavigationTypeBytes = 32

// sameDocumentReport is DevTools' report of an in-page URL change whose
// navigation event has not been recorded yet.
type sameDocumentReport struct {
	url            string
	navigationType string
	at             time.Time
}

// watchSameDocumentNavigations opens the DevTools session that reports a
// page's in-page URL changes. It runs off the page callback, since opening
// the session waits on the browser. Without one (a browser other than
// Chromium), every navigation counts as a document load, as before.
func (r *Recorder) watchSameDocumentNavigations(page playwright.Page) {
	r.mu.Lock()
	context, stopped := r.context, r.stopped
	r.mu.Unlock()
	if context == nil || stopped {
		return
	}
	session, err := context.NewCDPSession(page)
	if err != nil || session == nil {
		return
	}
	mainFrame := ""
	if tree, err := session.Send("Page.getFrameTree", map[string]any{}); err == nil {
		mainFrame = cdpFrameTreeRootID(tree)
	}
	session.On("Page.navigatedWithinDocument", func(params map[string]any) {
		// An iframe's in-page change is not the page's navigation.
		if frameID, _ := params["frameId"].(string); mainFrame != "" && frameID != mainFrame {
			return
		}
		rawURL, _ := params["url"].(string)
		navigationType, _ := params["navigationType"].(string)
		r.noteSameDocumentNavigation(rawURL, navigationType, time.Now())
	})
	if _, err := session.Send("Page.enable", map[string]any{}); err != nil {
		_ = session.Detach()
	}
}

func cdpFrameTreeRootID(tree any) string {
	result, _ := tree.(map[string]any)
	frameTree, _ := result["frameTree"].(map[string]any)
	frame, _ := frameTree["frame"].(map[string]any)
	id, _ := frame["id"].(string)
	return id
}

// noteSameDocumentNavigation marks the recorded navigation to rawURL as
// in-page, or keeps the report until its navigation event is recorded.
func (r *Recorder) noteSameDocumentNavigation(rawURL, navigationType string, at time.Time) {
	navigationType = boundedtext.TruncateUTF8(strings.TrimSpace(navigationType), maxNavigationTypeBytes)
	retainedURL := boundedtext.TruncateUTF8(rawURL, maxBrowserNativeURLBytes)
	if retainedURL == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.session == nil {
		return
	}
	for i := len(r.session.NavEvents) - 1; i >= 0; i-- {
		nav := &r.session.NavEvents[i]
		if at.Sub(nav.Timestamp) > sameDocumentMatchWindow {
			break
		}
		if !nav.SameDocument && nav.URL == retainedURL {
			nav.SameDocument, nav.NavigationType = true, navigationType
			return
		}
	}
	r.pendingSameDocument = append(pruneSameDocumentReports(r.pendingSameDocument, at), sameDocumentReport{url: retainedURL, navigationType: navigationType, at: at})
	if extra := len(r.pendingSameDocument) - maxPendingSameDocumentReports; extra > 0 {
		r.pendingSameDocument = slices.Delete(r.pendingSameDocument, 0, extra)
	}
}

// takeSameDocumentReportLocked reports whether DevTools already reported the
// navigation to url as in-page, and its navigation type.
func (r *Recorder) takeSameDocumentReportLocked(url string, at time.Time) (bool, string) {
	r.pendingSameDocument = pruneSameDocumentReports(r.pendingSameDocument, at)
	if url == "" {
		return false, ""
	}
	for i, report := range r.pendingSameDocument {
		if report.url == url {
			r.pendingSameDocument = slices.Delete(r.pendingSameDocument, i, i+1)
			return true, report.navigationType
		}
	}
	return false, ""
}

func pruneSameDocumentReports(reports []sameDocumentReport, now time.Time) []sameDocumentReport {
	kept := reports[:0]
	for _, report := range reports {
		if age := now.Sub(report.at); age <= sameDocumentMatchWindow && age >= -sameDocumentMatchWindow {
			kept = append(kept, report)
		}
	}
	return kept
}
