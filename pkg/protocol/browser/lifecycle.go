package browser

func (b *Browser) close() {
	if b.pwCDP != nil {
		_ = b.pwCDP.Detach()
		b.pwCDP = nil
	}
	b.mu.Lock()
	page := b.pwPage
	b.pwPage = nil
	b.mu.Unlock()
	if page != nil {
		_ = page.Close()
	}
	if b.pwContext != nil {
		_ = b.pwContext.Close() // flushes the context HAR recording
		b.pwContext = nil
	}
	b.lastHARArtifactPath = b.finalizeHARCapture()
	b.currentURL = ""
	b.slowMo = 0
	b.userAgent = ""
	b.captureScreenshots = ""
	b.consoleEntries = nil
	b.networkEntries = nil
}
