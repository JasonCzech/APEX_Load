package browser

import "github.com/jasonczech/apexload/pkg/protocol"

// BrowserIdentityEvidenceKey names, in a session's first step, the session
// identity an observer runs as: its position in config.browser.session and
// its username, never a secret.
const BrowserIdentityEvidenceKey = "browser_identity"

// applyBrowserIdentity reports the observer's identity in the result, once.
func (b *Browser) applyBrowserIdentity(result *protocol.Result) {
	b.mu.Lock()
	index, username, reported := b.identityIndex, b.identityUsername, b.identityReported
	if index > 0 {
		b.identityReported = true
	}
	b.mu.Unlock()
	if index <= 0 || reported {
		return
	}
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence[BrowserIdentityEvidenceKey] = map[string]any{"index": index, "username": username}
}
