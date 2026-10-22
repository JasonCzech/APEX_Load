package browser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestBrowserDocumentReceiptClockStepSamples(t *testing.T) {
	start := time.Now()
	for _, tc := range []struct {
		name  string
		drift time.Duration
		step  bool
	}{
		{"stable", 0, false}, {"positive threshold", 100 * time.Millisecond, false}, {"negative threshold", -100 * time.Millisecond, false},
		{"forward", 101 * time.Millisecond, true}, {"backward", -101 * time.Millisecond, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newBrowserDocumentReceiptClock(start)
			elapsed, drift := c.observeSample(start.Add(time.Second+tc.drift), time.Second)
			if elapsed != time.Second || drift != tc.drift || c.wallStepObserved != tc.step {
				t.Fatalf("sample: %v %v %+v", elapsed, drift, c)
			}
			c.observeSample(start.Add(2*time.Second), 2*time.Second)
			if c.wallStepObserved != tc.step {
				t.Fatal("observed step was cleared")
			}
		})
	}
	c := newBrowserDocumentReceiptClock(start)
	c.observeSample(start.Add(-time.Second), -time.Second)
	if !c.incomplete {
		t.Fatal("backwards monotonic sample admitted")
	}
}

func TestBrowserDocumentReceiptClockBoundsAndImmutability(t *testing.T) {
	start := time.Now()
	c := newBrowserDocumentReceiptClock(start)
	d := &browserDocumentState{documentID: "doc", lifecycleID: "life", frameID: "frame"}
	p := browserDocumentPayload{Kind: "metric", TimeOrigin: 1710000000000, Offset: 10}
	c.record(d, p, "measurement", 2, start.Add(time.Second))
	original := c.receipts["life/metric/measurement"]
	p.Offset = 20
	c.record(d, p, "measurement", 3, start.Add(2*time.Second))
	if c.receipts["life/metric/measurement"] != original {
		t.Fatal("terminal receipt revised")
	}
	for i := 1; i < maxBrowserDocumentReceipts; i++ {
		c.record(d, p, fmt.Sprintf("metric%d", i), 1, start.Add(time.Second))
	}
	c.record(d, p, "overflow", 1, start.Add(time.Second))
	if len(c.receipts) != maxBrowserDocumentReceipts || !c.incomplete {
		t.Fatal("receipt ledger exceeded bound or hid loss")
	}
	var unavailable browserDocumentReceiptClock
	unavailable.record(d, p, "metric", 1, start)
	if len(unavailable.receipts) != 0 || !unavailable.incomplete {
		t.Fatal("missing monotonic origin invented")
	}
}

type browserReceiptPage struct {
	playwright.Page
	frame playwright.Frame
}

func (p *browserReceiptPage) MainFrame() playwright.Frame { return p.frame }

func receiptTestBrowser(t *testing.T) (*Browser, *playwright.BindingSource) {
	t.Helper()
	b := &Browser{}
	b.applyDefaultConfig()
	b.documentMetricsEnabled = true
	if err := b.resetBrowserEvidence("141.0"); err != nil {
		t.Fatal(err)
	}
	frame := &browserGenerationUntouchedFrame{}
	return b, &playwright.BindingSource{Frame: frame, Page: &browserReceiptPage{frame: frame}}
}

func TestBrowserDocumentReceiptBindingIdentityAndSnapshot(t *testing.T) {
	b, source := receiptTestBrowser(t)
	p := validBrowserDocumentPayloadMap(t)
	if !b.receiveDocumentEvidence(source, p) {
		t.Fatal("begin refused")
	}
	p["kind"], p["metric"], p["state"], p["value"], p["offset"], p["boundary"] = "metric", "cls", "final", 0.1, 10.0, "graceful_end"
	if !b.receiveDocumentEvidence(source, p) {
		t.Fatal("final refused")
	}
	evidence := b.BrowserDocumentEvidence()
	before, _ := json.Marshal(evidence)
	snapshot := b.BrowserDocumentReceiptEvidence()
	if snapshot == nil || snapshot.Incomplete || len(snapshot.Receipts) != 2 {
		t.Fatalf("snapshot %+v", snapshot)
	}
	var final protocol.BrowserDocumentObservation
	for _, o := range evidence.Observations {
		if o.Metric == "cls" {
			final = o
		}
	}
	receipt := snapshot.Receipts[1] // stable begin/metric order
	if receipt.MeasurementID != final.MeasurementID || receipt.Revision != final.Revision || receipt.ReceivedAt != final.CapturedAt || receipt.DocumentID != final.DocumentID || receipt.LifecycleID != final.LifecycleID || receipt.FrameID != final.FrameID || receipt.BrowserOffsetMS != 10 || receipt.HostElapsedNS < 0 || receipt.ReceivedAt.Location() != time.UTC {
		t.Fatalf("unbound receipt %+v / %+v", receipt, final)
	}
	snapshot.Receipts[0].Kind = "changed"
	if b.BrowserDocumentReceiptEvidence().Receipts[0].Kind != "begin" {
		t.Fatal("snapshot aliases native state")
	}
	p["value"], p["offset"] = 0.2, 20.0
	if !b.receiveDocumentEvidence(source, p) {
		t.Fatal("duplicate final acknowledgement refused")
	}
	after, _ := json.Marshal(b.BrowserDocumentEvidence())
	if string(before) != string(after) || b.BrowserDocumentReceiptEvidence().Receipts[1] != receipt {
		t.Fatal("duplicate final changed raw or receipt evidence")
	}
	p["kind"], p["metric"], p["value"] = "end", "", nil
	if !b.receiveDocumentEvidence(source, p) || len(b.BrowserDocumentReceiptEvidence().Receipts) != 3 {
		t.Fatal("end receipt missing")
	}
	old := b.BrowserDocumentReceiptEvidence()
	if err := b.resetBrowserEvidence("141.0"); err != nil {
		t.Fatal(err)
	}
	if b.receiveDocumentEvidence(source, p, old.SourceGeneration) {
		t.Fatal("stale generation admitted")
	}
	fresh := b.BrowserDocumentReceiptEvidence()
	if fresh.SourceGeneration == old.SourceGeneration || len(fresh.Receipts) != 0 {
		t.Fatal("reset reused clock domain")
	}
	b.documentMetricsEnabled = false
	if b.BrowserDocumentReceiptEvidence() != nil {
		t.Fatal("disabled capture exposed receipts")
	}
}

func TestBrowserDocumentReceiptCancellationPreservesTerminal(t *testing.T) {
	b, source := receiptTestBrowser(t)
	p := validBrowserDocumentPayloadMap(t)
	b.receiveDocumentEvidence(source, p)
	b.censorDocumentEvidence(nil, "cancelled")
	snapshot := b.BrowserDocumentReceiptEvidence()
	if !snapshot.Incomplete || len(snapshot.Receipts) != 1 || snapshot.Receipts[0].Kind != "begin" {
		t.Fatal("censorship fabricated a browser terminal receipt")
	}
}

func TestBrowserDocumentReceiptNativeAcquisition(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><title>Clock receipts</title><p>Native receipt fixture</p>`)
			}))
			defer server.Close()
			b, ctx := documentActualBrowser(t, engine, server.URL)
			documentActualExecute(t, b, ctx, &protocol.Action{Type: "browser_navigate", Target: server.URL})
			b.finalizeDocumentEvidence(ctx)
			raw := b.BrowserDocumentEvidence()
			snapshot := b.BrowserDocumentReceiptEvidence()
			if snapshot == nil || snapshot.SourceGeneration != raw.SourceGeneration || len(snapshot.Receipts) < 7 {
				t.Fatalf("native acquisition missing %+v", snapshot)
			}
			receipts := make(map[string]BrowserDocumentReceipt)
			for _, receipt := range snapshot.Receipts {
				if receipt.Kind == "metric" {
					receipts[receipt.MeasurementID] = receipt
				}
			}
			ends := make(map[string]bool)
			for _, receipt := range snapshot.Receipts {
				if receipt.Kind == "end" {
					ends[receipt.LifecycleID] = true
				}
			}
			for _, receipt := range snapshot.Receipts {
				if receipt.Kind == "begin" && !ends[receipt.LifecycleID] && !snapshot.Incomplete {
					t.Fatal("native lost terminal receipt concealed")
				}
			}
			matched := 0
			for _, o := range raw.Observations {
				if o.State == "final" {
					receipt, ok := receipts[o.MeasurementID]
					if !ok || receipt.Revision != o.Revision || !receipt.ReceivedAt.Equal(o.CapturedAt) || receipt.BrowserTimeOriginMS <= 0 || receipt.HostElapsedNS < 0 {
						t.Fatalf("native immutable measurement lacks matching receipt: %+v / %+v", o, receipt)
					}
					matched++
				}
			}
			if matched < 5 {
				t.Fatal("no complete metric lifecycle")
			}
			if !reflect.DeepEqual(raw, b.BrowserDocumentEvidence()) {
				t.Fatal("receipt read changed raw evidence")
			}
		})
	}
}
