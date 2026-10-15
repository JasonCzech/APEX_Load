package browser

import (
	"testing"
	"time"
)

// DevTools' report of an in-page URL change marks the navigation Playwright
// records for it, whichever arrives first; a report too old to belong to it
// does not.
func TestSameDocumentReportsMarkTheirNavigation(t *testing.T) {
	r := &Recorder{session: &Session{}}
	start := time.Now()
	record := func(url string, at time.Time) NavEvent {
		r.mu.Lock()
		defer r.mu.Unlock()
		nav := NavEvent{Timestamp: at, URL: url}
		nav.SameDocument, nav.NavigationType = r.takeSameDocumentReportLocked(url, at)
		r.session.NavEvents = append(r.session.NavEvents, nav)
		return nav
	}

	// The report first.
	r.noteSameDocumentNavigation("https://x/lightning/r/Account/001/view", "historyApi", start)
	if nav := record("https://x/lightning/r/Account/001/view", start.Add(10*time.Millisecond)); !nav.SameDocument || nav.NavigationType != "historyApi" {
		t.Fatalf("navigation after its report = %+v", nav)
	}
	// The navigation first.
	record("https://x/lightning/r/Account/001/related", start.Add(time.Second))
	r.noteSameDocumentNavigation("https://x/lightning/r/Account/001/related", "historyApi", start.Add(time.Second+5*time.Millisecond))
	if nav := r.session.NavEvents[1]; !nav.SameDocument {
		t.Fatalf("navigation before its report = %+v", nav)
	}
	// A document load to a URL reported in-page long before stays a load.
	r.noteSameDocumentNavigation("https://x/home", "historyApi", start.Add(2*time.Second))
	if nav := record("https://x/home", start.Add(10*time.Second)); nav.SameDocument {
		t.Fatalf("a stale report marked a load: %+v", nav)
	}
	if len(r.pendingSameDocument) != 0 {
		t.Fatalf("pending reports = %+v", r.pendingSameDocument)
	}
	// Reports wait in a bounded queue.
	for i := range 2 * maxPendingSameDocumentReports {
		r.noteSameDocumentNavigation("https://x/other", "fragment", start.Add(20*time.Second+time.Duration(i)*time.Millisecond))
	}
	if len(r.pendingSameDocument) != maxPendingSameDocumentReports {
		t.Fatalf("pending reports = %d", len(r.pendingSameDocument))
	}
}
