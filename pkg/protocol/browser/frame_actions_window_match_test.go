package browser

import (
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

type urlOnlyPage struct {
	playwright.Page
	url string
}

func (p *urlOnlyPage) URL() string { return p.url }

const helpPage = "https://app.example.test/help"

// A new window still loading (about:blank) may be the one meant: nothing is
// picked until it loads, unless the switch has timed out.
func TestNewestWindowShowingPageWaitsForALoadingNewWindow(t *testing.T) {
	opener := &urlOnlyPage{url: "https://app.example.test/home"}
	olderPopup := &urlOnlyPage{url: "https://app.example.test/help?topic=1"}
	loading := &urlOnlyPage{url: "about:blank"}
	pages := []playwright.Page{opener, olderPopup, loading}
	existing := map[playwright.Page]bool{opener: true, olderPopup: true}
	if got := newestWindowShowingPage(pages, helpPage, opener, windowMatchScope{existing: existing, allowOlder: true}); got != nil {
		t.Fatalf("matched %v while a new window is still loading", got.URL())
	}
	if got := newestWindowShowingPage(pages, helpPage, opener, windowMatchScope{existing: existing, allowOlder: true, ignoreLoading: true}); got != olderPopup {
		t.Fatalf("at the timeout got %v, want the older window", got)
	}
	loading.url = "https://app.example.test/help?topic=2"
	if got := newestWindowShowingPage(pages, helpPage, opener, windowMatchScope{existing: existing}); got != loading {
		t.Fatalf("got %v, want the new window once it loaded", got)
	}
}

// An activated popup is current but still new: it is picked at once.
func TestNewestWindowShowingPagePicksACurrentNewWindow(t *testing.T) {
	opener := &urlOnlyPage{url: "https://app.example.test/help?view=main"}
	popup := &urlOnlyPage{url: "https://app.example.test/help?view=print&run=live"}
	pages := []playwright.Page{opener, popup}
	got := newestWindowShowingPage(pages, helpPage, popup, windowMatchScope{existing: map[playwright.Page]bool{opener: true}})
	if got != popup {
		t.Fatalf("got %v, want the activated popup", got)
	}
}

// The current window, open at the last switch, is picked only for a switch
// back to it; otherwise a switch whose window has not opened finds nothing.
func TestNewestWindowShowingPageTakesTheCurrentWindowOnlyOnAReturn(t *testing.T) {
	opener := &urlOnlyPage{url: "https://app.example.test/help?view=main"}
	pages := []playwright.Page{opener}
	existing := map[playwright.Page]bool{opener: true}
	if got := newestWindowShowingPage(pages, helpPage, opener, windowMatchScope{existing: existing, allowOlder: true}); got != nil {
		t.Fatalf("got %v, want no match while the popup has not opened", got)
	}
	if got := newestWindowShowingPage(pages, helpPage, opener, windowMatchScope{existing: existing, allowCurrent: true}); got != opener {
		t.Fatalf("got %v, want the opener on a return", got)
	}
}

// A new window that stays blank holds a match up only until the grace ends.
func TestNewestWindowShowingPageStopsWaitingOnABlankHelperAfterTheGrace(t *testing.T) {
	opener := &urlOnlyPage{url: "https://app.example.test/home"}
	popup := &urlOnlyPage{url: "https://app.example.test/help?run=2"}
	helper := &urlOnlyPage{url: "about:blank"}
	pages := []playwright.Page{opener, popup, helper}
	existing := map[playwright.Page]bool{opener: true}
	if got := newestWindowShowingPage(pages, helpPage, opener, windowMatchScope{existing: existing}); got != nil {
		t.Fatalf("within the grace got %v, want a wait for the newer blank window", got.URL())
	}
	if got := newestWindowShowingPage(pages, helpPage, opener, windowMatchScope{existing: existing, allowOlder: true}); got != popup {
		t.Fatalf("after the grace got %v, want the loaded popup", got)
	}
}
