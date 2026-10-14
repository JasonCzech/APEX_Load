// Package pwshutdown stops a Playwright runtime in the order Chromium needs.
package pwshutdown

import (
	"sync"
	"time"
)

// CloseThenStop closes every browser, then stops the Playwright driver.
//
// The driver owns each browser's DevTools pipe, and Chromium treats a pipe that
// closes under it as a fatal error: stopping the driver while a browser is
// still open or closing crashes the browser (a crash report, and on macOS a
// "reopen windows" prompt that crashes its next launch as well). When the
// browsers have not closed within wait, the driver is stopped anyway so that
// nothing outlives the caller; the result then reports false.
func CloseThenStop(wait time.Duration, stop func() error, closers ...func() error) bool {
	var once sync.Once
	stopDriver := func() {
		if stop != nil {
			once.Do(func() { _ = stop() })
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var group sync.WaitGroup
		for _, closeBrowser := range closers {
			if closeBrowser == nil {
				continue
			}
			group.Add(1)
			go func(closeBrowser func() error) {
				defer group.Done()
				_ = closeBrowser()
			}(closeBrowser)
		}
		group.Wait()
		stopDriver()
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		go stopDriver()
		return false
	}
}
