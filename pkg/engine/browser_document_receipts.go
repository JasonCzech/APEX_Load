package engine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func readBrowserReceipts(provider protocol.BrowserDocumentReceiptEvidenceProvider) (out *protocol.BrowserDocumentReceiptEvidence) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return provider.BrowserDocumentReceiptEvidence().Clone()
}

func (r *browserDocumentRegistration) publishReceipts() {
	if r == nil {
		return
	}
	for {
		r.mu.Lock()
		if !r.receiptPublishing {
			break
		}
		done := r.receiptDone
		r.mu.Unlock()
		<-done
	}
	p, ok := r.provider.(protocol.BrowserDocumentReceiptEvidenceProvider)
	wiring, wired := r.provider.(interface{ BrowserDocumentReceiptTransportVersion() int })
	if !ok || !wired {
		r.mu.Unlock()
		return
	}
	version, valid := receiptTransportVersion(wiring)
	if !valid {
		r.mu.Unlock()
		r.receiptFailed()
		return
	}
	if version != 1 && version != 2 {
		r.mu.Unlock()
		return
	}
	evidence := protocol.BrowserDocumentReceiptProjection(readBrowserReceipts(p), version)
	if evidence == nil {
		r.mu.Unlock()
		r.receiptFailed()
		return
	}
	encoded, err := json.Marshal(evidence)
	if err != nil || len(encoded) > protocol.MaxBrowserDocumentReceiptBytes {
		r.mu.Unlock()
		r.receiptFailed()
		return
	}
	digest := sha256.Sum256(encoded)
	if r.receiptPublished && r.receiptDigest == digest {
		r.mu.Unlock()
		return
	}
	sink := r.sink
	r.receiptPublishing = true
	r.receiptDone = make(chan struct{})
	r.mu.Unlock()
	target, ok := sink.(BrowserDocumentReceiptSink)
	if !ok {
		err = fmt.Errorf("browser receipt sink unavailable")
	} else {
		err = writeBrowserReceipts(target, evidence)
	}
	r.mu.Lock()
	r.receiptPublishing = false
	close(r.receiptDone)
	if err == nil {
		r.receiptPublished = true
		r.receiptDigest = digest
	}
	r.mu.Unlock()
	if err != nil {
		r.receiptFailed()
	}
}
func (r *browserDocumentRegistration) receiptFailed() {
	r.mu.Lock()
	sink := r.sink
	r.mu.Unlock()
	if marker, ok := sink.(interface{ MarkBrowserDocumentReceiptIncomplete() }); ok {
		func() { defer func() { _ = recover() }(); marker.MarkBrowserDocumentReceiptIncomplete() }()
	}
	r.registry.mu.Lock()
	r.registry.receiptIncomplete = true
	r.registry.mu.Unlock()
}

func writeBrowserReceipts(sink BrowserDocumentReceiptSink, evidence *protocol.BrowserDocumentReceiptEvidence) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("browser receipt sink panicked")
		}
	}()
	return sink.RecordBrowserDocumentReceiptEvidence(evidence)
}

// BrowserDocumentReceiptsIncomplete reports sticky receipt publication loss even
// after providers retire; it is separate from raw document completeness.
func (mc *MetricsCollector) BrowserDocumentReceiptsIncomplete() bool {
	if mc == nil {
		return true
	}
	mc.browserDocuments.mu.Lock()
	defer mc.browserDocuments.mu.Unlock()
	return mc.browserDocuments.receiptIncomplete
}
func receiptTransportVersion(p interface{ BrowserDocumentReceiptTransportVersion() int }) (version int, valid bool) {
	defer func() {
		if recover() != nil {
			version = 0
			valid = false
		}
	}()
	return p.BrowserDocumentReceiptTransportVersion(), true
}
