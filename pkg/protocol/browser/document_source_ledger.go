package browser

import "github.com/jasonczech/apexload/pkg/protocol"

func (b *Browser) SetBrowserDocumentSourceLease(l *protocol.BrowserSourceLease) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sourceLease = l
}
func (b *Browser) BrowserDocumentSourceLease() *protocol.BrowserSourceLease {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sourceLease
}
