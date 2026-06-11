package browser

func (b *Browser) close() {
	if b.pwCDP != nil {
		_ = b.pwCDP.Detach()
		b.pwCDP = nil
	}
	if b.pwPage != nil {
		_ = b.pwPage.Close()
		b.pwPage = nil
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
