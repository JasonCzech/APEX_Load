package generatorrpc

import (
	"errors"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func (w *archiveRecorder) RecordBrowserHARCaptureOmissions(counts map[string]int64) error {
	if w == nil {
		return errors.New("generator archive recorder is unavailable")
	}
	w.harMu.Lock()
	defer w.harMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.runRoot == nil {
		return errors.New("generator archive recorder is closed")
	}
	merged, err := protocol.MergeBrowserHARCaptureOmissions(w.manifest.BrowserHAROmitted, counts)
	if err != nil {
		return err
	}
	if len(counts) > 0 {
		w.manifest.BrowserHAROmitted = merged
		w.manifest.BrowserHARIncomplete = true
	}
	return nil
}

func (s *combinedSink) RecordBrowserHARCaptureOmissions(counts map[string]int64) error {
	if s != nil {
		for _, candidate := range s.sinks {
			if target, ok := candidate.(engine.BrowserHARCaptureOmissionSink); ok {
				return target.RecordBrowserHARCaptureOmissions(counts)
			}
		}
	}
	return errors.New("browser HAR capture omission sink is unavailable")
}
