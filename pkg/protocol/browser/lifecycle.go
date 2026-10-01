package browser

import "github.com/mxschmitt/playwright-go"

func (b *Browser) close() {
	b.mu.Lock()
	b.closing = true
	requestPolicyStop := b.requestPolicyStop
	b.requestPolicyStop = nil
	cdp := b.pwCDP
	b.pwCDP = nil
	b.pwCDPPage = nil
	b.cdpInstallGeneration++
	page := b.pwPage
	b.pwPage = nil
	b.mu.Unlock()
	if requestPolicyStop != nil {
		close(requestPolicyStop)
	}
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
	b.mu.Lock()
	b.runtimeTextRedactor = nil
	b.runtimeSecretsActive = nil
	b.mu.Unlock()
	b.storageState = nil
	b.currentURL = ""
	b.slowMo = 0
	b.userAgent = ""
	b.captureScreenshots = ""
	b.consoleEntries = nil
	b.consoleDropped = 0
	b.networkEntries = nil
	b.networkEntriesDropped = 0
	b.networkBodyBytes = 0
	b.networkMetadataBytes = 0
	b.pages = nil
	b.resetWSEvents()
}

func (b *Browser) stopRequestPolicyWaiters() {
	b.mu.Lock()
	stop := b.requestPolicyStop
	b.requestPolicyStop = nil
	b.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (b *Browser) takeBrowserContext() playwright.BrowserContext {
	b.mu.Lock()
	defer b.mu.Unlock()
	context := b.pwContext
	b.pwContext = nil
	return context
}
