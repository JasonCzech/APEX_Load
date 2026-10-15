package browser

import (
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Salesforce EPT calibration (config.browser.ept_calibration, an opt-in for
// qualification runs): the observer opens Lightning with eptVisible=1, which
// shows Salesforce's Experienced Page Time counter on the page, and reads the
// counter after each step. A step after which the counter changed reports
// its reading in protocol_evidence.salesforce_ept, and the run report sets it
// beside the step's own time per transaction. Salesforce documents no API for
// the counter, so it is read from the page's text: confirm the reading in the
// org before relying on it.

// SalesforceEPTEvidenceKey names a step's EPT reading: {"ms": n}.
const SalesforceEPTEvidenceKey = "salesforce_ept"

// MaxSalesforceEPTMs bounds a believable EPT reading.
const MaxSalesforceEPTMs = 600000

// salesforceEPTScript returns the page's EPT counter in milliseconds, or
// null. It walks text nodes rather than reading innerText, which would force
// a layout of the whole Lightning page; a reading without a unit is seconds.
const salesforceEPTScript = `() => {
  const root = document.body
  if (!root) return null
  const pattern = /\bEPT\b\s*[:=]?\s*([0-9]+(?:[.,][0-9]+)?)\s*(ms|s)?\b/i
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  let scanned = 0
  for (let node = walker.nextNode(); node && scanned < 50000; node = walker.nextNode(), scanned++) {
    const text = node.nodeValue
    if (!text || text.length > 200 || !/EPT/i.test(text)) continue
    let match = text.match(pattern)
    if (!match && node.parentElement) match = (node.parentElement.textContent || '').slice(0, 200).match(pattern)
    if (!match) continue
    const value = parseFloat(match[1].replace(',', '.'))
    return (match[2] || 's').toLowerCase() === 'ms' ? value : value * 1000
  }
  return null
}`

// salesforceEPTURL is a Lightning URL with eptVisible=1, its other query
// parameters kept in order; other URLs are unchanged.
func salesforceEPTURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	host := strings.ToLower(parsed.Hostname())
	lightning := strings.HasSuffix(host, ".lightning.force.com") || parsed.Path == "/lightning" ||
		strings.HasPrefix(parsed.Path, "/lightning/") || strings.HasPrefix(parsed.Path, "/one/one.app")
	if !lightning || parsed.Query().Get("eptVisible") == "1" {
		return raw
	}
	if parsed.RawQuery == "" {
		parsed.RawQuery = "eptVisible=1"
	} else {
		parsed.RawQuery += "&eptVisible=1"
	}
	return parsed.String()
}

// recordSalesforceEPT reads the page's EPT counter after a passed step and
// reports a reading that changed since the context's last one: an unchanged
// counter still shows an earlier page's time.
func (b *Browser) recordSalesforceEPT(result *protocol.Result) {
	if !b.eptCalibration || result == nil || !result.Success {
		return
	}
	page := b.currentPage()
	if page == nil {
		return
	}
	value, err := page.Evaluate(salesforceEPTScript)
	if err != nil {
		return
	}
	ms, ok := salesforceEPTMilliseconds(value)
	if !ok {
		return
	}
	b.mu.Lock()
	changed := ms != b.lastSalesforceEPTMs
	b.lastSalesforceEPTMs = ms
	b.mu.Unlock()
	if !changed {
		return
	}
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence[SalesforceEPTEvidenceKey] = map[string]any{"ms": ms}
}

func salesforceEPTMilliseconds(value any) (float64, bool) {
	var ms float64
	switch typed := value.(type) {
	case float64:
		ms = typed
	case int:
		ms = float64(typed)
	default:
		return 0, false
	}
	return ms, ms > 0 && ms <= MaxSalesforceEPTMs
}
