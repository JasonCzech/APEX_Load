package engine

import (
	"log/slog"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// BrowserHARCaptureOmissionSink retains capture losses even when no artifact
// reached publication. Like artifact transfer, this is optional evidence.
type BrowserHARCaptureOmissionSink interface {
	RecordBrowserHARCaptureOmissions(map[string]int64) error
}

func takeBrowserHARCaptureOmissions(provider protocol.BrowserHARCaptureOmissionProvider) (out map[string]int64) {
	defer func() {
		if recover() != nil {
			out = map[string]int64{protocol.BrowserHAROmittedCaptureStatus: 1}
		}
	}()
	out, err := protocol.MergeBrowserHARCaptureOmissions(nil, provider.TakeBrowserHARCaptureOmissions())
	if err != nil {
		return map[string]int64{protocol.BrowserHAROmittedCaptureStatus: 1}
	}
	return out
}

func handOffBrowserHARCaptureOmissions(p protocol.Protocol, sink ResultSink) {
	provider, ok := p.(protocol.BrowserHARCaptureOmissionProvider)
	if !ok {
		return
	}
	target, ok := sink.(BrowserHARCaptureOmissionSink)
	if !ok {
		return
	}
	counts := takeBrowserHARCaptureOmissions(provider)
	if len(counts) == 0 {
		return
	}
	if err := target.RecordBrowserHARCaptureOmissions(counts); err != nil {
		slog.Warn("browser HAR capture omissions were not retained in the run archive", "err", err)
	}
}
