package browser

import (
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

type closeCountingBrowser struct {
	playwright.Browser
	closes int
}

func (b *closeCountingBrowser) Close(...playwright.BrowserCloseOptions) error {
	b.closes++
	return nil
}

// Exiting closes every pooled browser and empties the pool, so a later
// replay starts a fresh one.
func TestShutdownPoolClosesPooledBrowsers(t *testing.T) {
	// Set aside a pool earlier tests started, and restore it afterwards.
	globalMutex.Lock()
	savedPlaywright, savedBrowsers := globalPlaywright, globalBrowsers
	first, second := &closeCountingBrowser{}, &closeCountingBrowser{}
	globalPlaywright = nil
	globalBrowsers = map[string]playwright.Browser{"fake-first": first, "fake-second": second}
	globalMutex.Unlock()
	t.Cleanup(func() {
		globalMutex.Lock()
		globalPlaywright, globalBrowsers = savedPlaywright, savedBrowsers
		globalMutex.Unlock()
	})

	if !ShutdownPool(time.Second) {
		t.Fatal("ShutdownPool reported a timeout")
	}
	if first.closes != 1 || second.closes != 1 {
		t.Fatalf("closes = %d, %d; want each pooled browser closed once", first.closes, second.closes)
	}
	globalMutex.Lock()
	defer globalMutex.Unlock()
	if len(globalBrowsers) != 0 || globalPlaywright != nil {
		t.Fatalf("pool after shutdown = %d browsers, driver %v; want empty", len(globalBrowsers), globalPlaywright)
	}
}
