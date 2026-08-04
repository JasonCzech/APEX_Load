package browser

import (
	"sync"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

type cdpTestPage struct {
	playwright.Page
	name string
}

type cdpTestContext struct {
	playwright.BrowserContext
}

type cdpTestSession struct {
	playwright.CDPSession
	mu       sync.Mutex
	detached bool
}

func (session *cdpTestSession) Detach() error {
	session.mu.Lock()
	session.detached = true
	session.mu.Unlock()
	return nil
}

func (session *cdpTestSession) wasDetached() bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.detached
}

func TestRemoveActivePageRebindsCDPToFallback(t *testing.T) {
	context := &cdpTestContext{}
	fallback := &cdpTestPage{name: "fallback"}
	popup := &cdpTestPage{name: "popup"}
	popupCDP := &cdpTestSession{}
	fallbackCDP := &cdpTestSession{}

	browser := &Browser{
		engine:               "chromium",
		clientMetricsEnabled: true,
		pwContext:            context,
		pwPage:               popup,
		pwCDP:                popupCDP,
		pwCDPPage:            popup,
		pages:                []playwright.Page{fallback, popup},
		createCDPSession: func(_ playwright.BrowserContext, page playwright.Page) (playwright.CDPSession, error) {
			if page != fallback {
				t.Fatalf("CDP rebound to %v, want fallback", page)
			}
			return fallbackCDP, nil
		},
	}

	browser.removePage(popup)

	if browser.currentPage() != fallback {
		t.Fatal("fallback page was not activated")
	}
	browser.mu.Lock()
	gotCDP, gotPage := browser.pwCDP, browser.pwCDPPage
	browser.mu.Unlock()
	if gotCDP != fallbackCDP || gotPage != fallback {
		t.Fatalf("fallback CDP = (%v, %v), want (%v, %v)", gotCDP, gotPage, fallbackCDP, fallback)
	}
	if !popupCDP.wasDetached() {
		t.Fatal("closed popup CDP was not detached")
	}
}

func TestInstallCDPSessionDoesNotCommitStaleActivation(t *testing.T) {
	context := &cdpTestContext{}
	firstPage := &cdpTestPage{name: "first"}
	secondPage := &cdpTestPage{name: "second"}
	firstCDP := &cdpTestSession{}
	secondCDP := &cdpTestSession{}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	browser := &Browser{
		engine:               "chromium",
		clientMetricsEnabled: true,
		pwContext:            context,
		pwPage:               firstPage,
		createCDPSession: func(_ playwright.BrowserContext, page playwright.Page) (playwright.CDPSession, error) {
			switch page {
			case firstPage:
				close(firstStarted)
				<-releaseFirst
				return firstCDP, nil
			case secondPage:
				return secondCDP, nil
			default:
				t.Fatalf("unexpected page %v", page)
				return nil, nil
			}
		},
	}

	firstDone := make(chan struct{})
	go func() {
		browser.installCDPSession(context, firstPage)
		close(firstDone)
	}()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first CDP installation did not start")
	}
	browser.mu.Lock()
	browser.pwPage = secondPage
	browser.mu.Unlock()
	browser.installCDPSession(context, secondPage)
	close(releaseFirst)
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("stale CDP installation did not finish")
	}

	browser.mu.Lock()
	gotCDP, gotPage := browser.pwCDP, browser.pwCDPPage
	browser.mu.Unlock()
	if gotCDP != secondCDP || gotPage != secondPage {
		t.Fatalf("active CDP = (%v, %v), want (%v, %v)", gotCDP, gotPage, secondCDP, secondPage)
	}
	if !firstCDP.wasDetached() {
		t.Fatal("stale CDP session was not detached")
	}
	if secondCDP.wasDetached() {
		t.Fatal("current CDP session was detached by stale activation")
	}
}

func TestBrowserContextReferenceIsRaceSafe(t *testing.T) {
	first := &cdpTestContext{}
	second := &cdpTestContext{}
	browser := &Browser{}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 1_000; iteration++ {
				if worker%2 == 0 {
					if iteration%2 == 0 {
						browser.setBrowserContext(first)
					} else {
						browser.setBrowserContext(second)
					}
					continue
				}
				_ = browser.currentBrowserContext()
			}
		}(worker)
	}
	close(start)
	workers.Wait()
	if context := browser.currentBrowserContext(); context != first && context != second {
		t.Fatalf("current context = %v, want one of the installed contexts", context)
	}
}
