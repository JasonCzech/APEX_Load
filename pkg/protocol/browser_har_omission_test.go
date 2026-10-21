package protocol

import (
	"reflect"
	"testing"
)

func TestBrowserHARCaptureOmissionBounds(t *testing.T) {
	current := map[string]int64{BrowserHAROmittedTransfer: 7, BrowserHAROmittedCaptureFailed: MaxBrowserHAROmissionCount}
	before := map[string]int64{BrowserHAROmittedTransfer: 7, BrowserHAROmittedCaptureFailed: MaxBrowserHAROmissionCount}
	got, err := MergeBrowserHARCaptureOmissions(current, map[string]int64{BrowserHAROmittedCaptureFailed: 1})
	if err != nil || got[BrowserHAROmittedCaptureFailed] != MaxBrowserHAROmissionCount || got[BrowserHAROmittedCountLimit] != 1 || got[BrowserHAROmittedTransfer] != 7 || !reflect.DeepEqual(current, before) {
		t.Fatalf("got=%v current=%v err=%v", got, current, err)
	}
	for _, counts := range []map[string]int64{{"private arbitrary reason": 1}, {BrowserHAROmittedCaptureFailed: 0}, {BrowserHAROmittedCaptureFailed: -1}, {BrowserHAROmittedCaptureFailed: MaxBrowserHAROmissionCount + 1}, {BrowserHAROmittedCountLimit: 2}} {
		if _, err := MergeBrowserHARCaptureOmissions(current, counts); err == nil {
			t.Fatal("accepted invalid counts", counts)
		}
		if !reflect.DeepEqual(current, before) {
			t.Fatal("invalid input mutated existing counts")
		}
	}
	if _, err := MergeBrowserHARCaptureOmissions(nil, map[string]int64{"a": 1, "b": 1, "c": 1, "d": 1, "e": 1, "f": 1, "g": 1}); err == nil {
		t.Fatal("unbounded reason set accepted")
	}
	if got, err := MergeBrowserHARCaptureOmissions(nil, nil); err != nil || got != nil {
		t.Fatal("empty counts changed state")
	}
}
