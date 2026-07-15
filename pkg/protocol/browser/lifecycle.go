package browser

import "github.com/playwright-community/playwright-go"

func (b *Browser) close() {
	b.mu.Lock()
	b.closing = true
	cdp := b.pwCDP
	b.pwCDP = nil
	page := b.pwPage
	b.pwPage = nil
	b.mu.Unlock()
	if cdp != nil {
		_ = cdp.Detach()
	}
	if page != nil {
		_ = page.Close()
	}
	if context := b.takeBrowserContext(); context != nil {
		_ = context.Close() // flushes the context HAR recording
	}
	// Keep the limiter active through Playwright's final HAR flush.
	b.mu.Lock()
	monitorStop := b.harMonitorStop
	b.harMonitorStop = nil
	b.mu.Unlock()
	if monitorStop != nil {
		close(monitorStop)
		b.harMonitorWG.Wait()
		b.mu.Lock()
		b.harLimitExceeded = nil
		b.mu.Unlock()
	}
	b.mu.Lock()
	workerStop := b.networkWorkerStop
	b.networkWorkerStop = nil
	b.networkTasks = nil
	b.mu.Unlock()
	if workerStop != nil {
		close(workerStop)
		b.networkWorkerWG.Wait()
	}
	if artifactPath := b.finalizeHARCapture(); artifactPath != "" {
		// Retained artifacts are independently swept by the configured retention
		// policy. In particular, do not delete an earlier failing iteration when
		// new_user_per_iteration tears down a later browser session.
		b.lastHARArtifactPath = artifactPath
	}
	b.currentURL = ""
	b.slowMo = 0
	b.userAgent = ""
	b.captureScreenshots = ""
	b.consoleEntries = nil
	b.consoleDropped = 0
	b.networkEntries = nil
	b.networkEntriesDropped = 0
	b.networkBodyBytes = 0
	b.pages = nil
	b.resetWSEvents()
}

func (b *Browser) takeBrowserContext() playwright.BrowserContext {
	b.mu.Lock()
	defer b.mu.Unlock()
	context := b.pwContext
	b.pwContext = nil
	return context
}
