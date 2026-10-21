package api

import (
	"errors"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Capture can fail before an artifact exists. Retain that loss through the
// same manifest used for transfer omissions without attempting a file read.
func (w *runArchiveWriter) RecordBrowserHARCaptureOmissions(counts map[string]int64) error {
	if w == nil {
		return errors.New("run archive is unavailable")
	}
	w.harMu.Lock()
	defer w.harMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.txFile == nil {
		return errors.New("run archive is closed")
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

// Capture counts follow the same single archive owner as the HAR artifacts.
func (sink combinedResultSink) RecordBrowserHARCaptureOmissions(counts map[string]int64) error {
	for _, candidate := range sink {
		if target, ok := candidate.(engine.BrowserHARCaptureOmissionSink); ok {
			return target.RecordBrowserHARCaptureOmissions(counts)
		}
	}
	return errors.New("browser HAR capture omission sink is unavailable")
}
