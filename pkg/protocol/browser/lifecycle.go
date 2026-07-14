package browser

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
	if b.harMonitorStop != nil {
		close(b.harMonitorStop)
		b.harMonitorStop = nil
		b.harMonitorWG.Wait()
	}
	if page != nil {
		_ = page.Close()
	}
	if b.pwContext != nil {
		_ = b.pwContext.Close() // flushes the context HAR recording
		b.pwContext = nil
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
	b.lastHARArtifactPath = b.finalizeHARCapture()
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
