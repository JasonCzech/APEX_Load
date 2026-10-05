package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func browserResourceV2Fixture() *protocol.BrowserResourceEvidence {
	generation := "0123456789abcdef0123456789abcdef"
	zero := float64(0)
	end := float64(12.5)
	epoch := float64(1700000000123)
	empty := int64(0)
	cached := false
	return &protocol.BrowserResourceEvidence{
		Version: protocol.BrowserEvidenceVersion, SourceGeneration: generation,
		RequestID: generation + "-request-2", FrameID: generation + "-frame-1",
		DocumentID: generation + "-document-1", NavigationID: generation + "-navigation-1",
		RedirectChainID: generation + "-chain-1", RedirectFromRequestID: generation + "-request-1", RedirectHop: 1,
		InitiatingActionID: generation + "-action-1", ObservationActionID: generation + "-action-2",
		Origin: "https://example.test", EpochStartMS: &epoch, DocumentOffsetMS: &zero,
		ObservedAt: time.Unix(1700000001, 0).UTC(), TimingSource: "playwright", State: "finished",
		Phases: []protocol.BrowserResourcePhase{{Name: "dns", UnavailableReason: "not_exposed"}, {Name: "response", StartMS: &zero, EndMS: &end}},
		Bytes:  protocol.BrowserResourceBytes{ResponseEncodedBody: &empty, UnavailableReason: "not_exposed"},
		Cache:  &cached, ServiceWorkerUnavailableReason: "not_exposed", InitiatorUnavailableReason: "not_exposed",
	}
}

func TestBrowserResourceArchiveRetainsExactIdentityWithoutReplacingLegacyClock(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		t.Run(map[bool]string{false: "transaction", true: "snapshot"}[snapshot], func(t *testing.T) {
			evidence := browserResourceV2Fixture()
			if evidence.Clone() == nil {
				t.Fatal("invalid test evidence")
			}
			entry := protocol.NetworkEntry{URL: "https://example.test/a.js", Method: "GET", DurationMs: 12.5, BrowserEvidence: evidence}
			tx := TransactionEvent{Timestamp: time.Unix(1700000009, 0).UTC(), ActionName: "completion", PageURL: "https://example.test/page"}
			if snapshot {
				tx.Snapshot = &protocol.ErrorSnapshot{NetworkEntries: []protocol.NetworkEntry{entry}}
			} else {
				tx.NetworkEntries = []protocol.NetworkEntry{entry}
			}
			rows := browserResourceTimingsFromTransaction(tx)
			top := appendTopBrowserResourceTimingsFromTransaction(nil, tx, 50)
			if len(rows) != 1 || !reflect.DeepEqual(rows, top) || !reflect.DeepEqual(rows[0].BrowserEvidence, evidence) {
				t.Fatalf("lost exact identity: rows=%+v top=%+v", rows, top)
			}
			if rows[0].Timestamp != tx.Timestamp || rows[0].BrowserEvidence.ObservedAt == tx.Timestamp || rows[0].BrowserEvidence.InitiatingActionID == rows[0].BrowserEvidence.ObservationActionID {
				t.Fatal("resource capture clock or action identity collapsed into transaction observation")
			}
			raw, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			var decoded []BrowserResourceTiming
			if err = json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rows, decoded) {
				t.Fatalf("resource roundtrip drift: %s", raw)
			}
			*evidence.Phases[1].StartMS = 99
			*evidence.Bytes.ResponseEncodedBody = 99
			*evidence.Cache = true
			if *rows[0].BrowserEvidence.Phases[1].StartMS != 0 || *rows[0].BrowserEvidence.Bytes.ResponseEncodedBody != 0 || *rows[0].BrowserEvidence.Cache {
				t.Fatal("returned resource aliases mutable source")
			}
			if rows[0].BrowserEvidence.Phases[0].StartMS != nil || rows[0].BrowserEvidence.Phases[0].EndMS != nil {
				t.Fatal("unavailable native phase became zero")
			}
		})
	}
}

func TestBrowserResourceArchiveLegacyAndMalformedEvidenceStayExplicit(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		entry := protocol.NetworkEntry{URL: "https://example.test/a.js", DurationMs: 3}
		if malformed {
			entry.BrowserEvidence = browserResourceV2Fixture()
			entry.BrowserEvidence.RequestID = "foreign-request"
		}
		rows := browserResourceTimingsFromTransaction(TransactionEvent{NetworkEntries: []protocol.NetworkEntry{entry}})
		if len(rows) != 1 || rows[0].DurationMs != 3 || rows[0].BrowserEvidence != nil {
			t.Fatalf("invented identity for historical/malformed evidence %+v", rows)
		}
	}
}

func TestBrowserResourceEvidenceCountsAgainstDiagnosticPayloadBudget(t *testing.T) {
	event := BrowserDiagnosticsEvent{NetworkEntries: []protocol.NetworkEntry{{URL: "https://example.test/a.js"}}}
	baseline := browserDiagnosticEventPayloadBytes(&event)
	event.NetworkEntries[0].BrowserEvidence = browserResourceV2Fixture()
	raw, err := json.Marshal(event.NetworkEntries[0].BrowserEvidence)
	if err != nil {
		t.Fatal(err)
	}
	actual := browserDiagnosticEventPayloadBytes(&event)
	if actual-baseline != len(raw) {
		t.Fatalf("evidence omitted from payload charge: %d want %d", actual-baseline, len(raw))
	}
	retained, bytes := retainBoundedBrowserDiagnosticEvent(nil, 0, event, actual, 1, actual-1)
	if len(retained) != 0 || bytes != 0 {
		t.Fatal("large evidence bypasses retained byte budget")
	}
}

func browserDocumentV2ArchiveFixture() *protocol.BrowserDocumentEvidence {
	generation := "0123456789abcdef0123456789abcdef"
	doc, life := generation+"-document-1", generation+"-lifecycle-1"
	now := time.Unix(1700000000, 0).UTC()
	return &protocol.BrowserDocumentEvidence{Version: protocol.BrowserEvidenceVersion, SourceGeneration: generation,
		Observations: []protocol.BrowserDocumentObservation{{Version: protocol.BrowserEvidenceVersion, SourceGeneration: generation,
			DocumentID: doc, LifecycleID: life, FrameID: generation + "-frame-1", NavigationID: generation + "-navigation-1", MeasurementID: doc + "/" + life + "/inp",
			Revision: 1, Metric: "inp", Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, Unit: "ms", Engine: "chromium",
			FrameScope: "main_frame", LifecyclePhase: "navigation", State: "final", Boundary: "graceful_end", WindowStart: now, WindowEnd: now, CapturedAt: now,
			UnavailableReason: "no_interaction"}},
	}
}

func TestBrowserDocumentDiagnosticCopiesAndChargesIndependentEvidence(t *testing.T) {
	evidence := browserDocumentV2ArchiveFixture()
	if evidence.Clone() == nil {
		t.Fatal("invalid document fixture")
	}
	input := &protocol.ClientMetrics{FCPMs: 12, DeviceProfile: "desktop", DocumentEvidence: evidence}
	copy := cloneBrowserDiagnosticClientMetrics(input)
	if !reflect.DeepEqual(input, copy) {
		t.Fatal("diagnostic clone dropped independent evidence")
	}
	evidence.Observations[0].Revision = 2
	if copy.DocumentEvidence.Observations[0].Revision != 1 || copy.FCPMs != 12 {
		t.Fatal("clone aliases source or replaces legacy metrics")
	}
	event := BrowserDiagnosticsEvent{ClientMetrics: &protocol.ClientMetrics{DeviceProfile: "desktop"}}
	baseline := browserDiagnosticEventPayloadBytes(&event)
	event.ClientMetrics = copy
	raw, err := json.Marshal(copy.DocumentEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if got := browserDiagnosticEventPayloadBytes(&event) - baseline; got != len(raw) {
		t.Fatalf("document bytes %d want %d", got, len(raw))
	}
	input.DocumentEvidence.Observations[0].SourceGeneration = "foreign"
	invalid := cloneBrowserDiagnosticClientMetrics(input)
	if invalid.DocumentEvidence != nil || invalid.FCPMs != 12 {
		t.Fatal("malformed document accepted or legacy record lost")
	}
	if cloneBrowserDiagnosticClientMetrics(nil) != nil {
		t.Fatal("absent legacy input becomes evidence")
	}
}

func TestBrowserRuntimeAvailabilityDiagnosticOwnershipAndBudget(t *testing.T) {
	availability := &protocol.BrowserRuntimeMetricAvailability{Version: 1, Metrics: []protocol.BrowserRuntimeMetricCapability{{Metric: "js_heap_used_mb", Source: "cdp", Scope: "active_page", UnavailableReason: "unsupported"}, {Metric: "browser_process_mem_mb", Source: "driver_process_tree", Scope: "driver_process_tree", Available: true}}}
	if availability.Clone() == nil {
		t.Fatal("invalid fixture")
	}
	input := &protocol.ClientMetrics{FCPMs: 12, RuntimeAvailability: availability}
	owned := cloneBrowserDiagnosticClientMetrics(input)
	if !reflect.DeepEqual(input, owned) {
		t.Fatal("availability lost in diagnostic clone")
	}
	owned.RuntimeAvailability.Metrics[0].UnavailableReason = "not_exposed"
	if input.RuntimeAvailability.Metrics[0].UnavailableReason != "unsupported" {
		t.Fatal("availability clone aliases source")
	}
	event := BrowserDiagnosticsEvent{ClientMetrics: &protocol.ClientMetrics{FCPMs: 12}}
	baseline := browserDiagnosticEventPayloadBytes(&event)
	event.ClientMetrics = cloneBrowserDiagnosticClientMetrics(input)
	raw, err := json.Marshal(event.ClientMetrics.RuntimeAvailability)
	if err != nil {
		t.Fatal(err)
	}
	if actual := browserDiagnosticEventPayloadBytes(&event) - baseline; actual != len(raw) {
		t.Fatal("availability bytes not charged", actual, len(raw))
	}
	size := browserDiagnosticEventPayloadBytes(&event)
	retained, bytes := retainBoundedBrowserDiagnosticEvent(nil, 0, event, size, 1, size-1)
	if len(retained) != 0 || bytes != 0 {
		t.Fatal("availability bypasses retained payload limit")
	}
	malformed := &protocol.BrowserRuntimeMetricAvailability{Version: 1, Metrics: make([]protocol.BrowserRuntimeMetricCapability, 17)}
	for i := range malformed.Metrics {
		malformed.Metrics[i] = availability.Metrics[0]
	}
	input.RuntimeAvailability = malformed
	invalid := cloneBrowserDiagnosticClientMetrics(input)
	if invalid.RuntimeAvailability != nil || invalid.FCPMs != 12 {
		t.Fatal("malformed oversized availability leaked or legacy metric lost")
	}
}
