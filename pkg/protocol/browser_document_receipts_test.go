package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func receiptContractFixture() *BrowserDocumentReceiptEvidence {
	gen := strings.Repeat("a", 32)
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	return &BrowserDocumentReceiptEvidence{Version: 1, SourceGeneration: gen, StartedAt: at, Receipts: []BrowserDocumentReceipt{{DocumentID: gen + "-d1", LifecycleID: gen + "-l1", FrameID: gen + "-f1", Kind: "metric", MeasurementID: gen + "-d1/" + gen + "-l1/lcp", Revision: 2, BrowserTimeOriginMS: 1792627200000, BrowserOffsetMS: 100, ReceivedAt: at.Add(time.Second), HostElapsedNS: int64(time.Second)}}}
}

func TestBrowserDocumentReceiptContractRoundTrip(t *testing.T) {
	source := receiptContractFixture()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := DecodeBrowserDocumentReceiptEvidence(raw)
	if err != nil || !reflect.DeepEqual(owned, source) {
		t.Fatalf("roundtrip %v %+v", err, owned)
	}
	owned.Receipts[0].Kind = "changed"
	if source.Receipts[0].Kind != "metric" {
		t.Fatal("alias")
	}
	for _, rows := range [][]BrowserDocumentReceipt{nil, {}} {
		source.Receipts = rows
		raw, _ = json.Marshal(source)
		if _, err := DecodeBrowserDocumentReceiptEvidence(raw); err != nil {
			t.Fatal("explicit empty acquisition refused", err)
		}
	}
}

func TestBrowserDocumentReceiptContractRefusesMalformed(t *testing.T) {
	for name, mutate := range map[string]func(*BrowserDocumentReceiptEvidence){
		"future version":         func(e *BrowserDocumentReceiptEvidence) { e.Version = 4 },
		"missing source":         func(e *BrowserDocumentReceiptEvidence) { e.SourceGeneration = "" },
		"foreign document":       func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].DocumentID = strings.Repeat("b", 32) + "-d1" },
		"foreign frame":          func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].FrameID = "foreign" },
		"foreign lifecycle":      func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].LifecycleID = "foreign" },
		"missing origin":         func(e *BrowserDocumentReceiptEvidence) { e.StartedAt = time.Time{} },
		"missing receipt time":   func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].ReceivedAt = time.Time{} },
		"nan browser":            func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].BrowserOffsetMS = math.NaN() },
		"negative browser":       func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].BrowserOffsetMS = -1 },
		"missing browser origin": func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].BrowserTimeOriginMS = 0 },
		"negative host":          func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].HostElapsedNS = -1 },
		"invented drift":         func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].WallDriftNS = 1 },
		"hidden wall step": func(e *BrowserDocumentReceiptEvidence) {
			e.Receipts[0].WallDriftNS = int64(time.Second)
			e.Receipts[0].ReceivedAt = e.Receipts[0].ReceivedAt.Add(time.Second)
		},
		"unknown kind":      func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].Kind = "complete" },
		"unknown metric":    func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].MeasurementID += "unknown" },
		"missing revision":  func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].Revision = 0 },
		"begin with metric": func(e *BrowserDocumentReceiptEvidence) { e.Receipts[0].Kind = "begin" },
		"duplicate":         func(e *BrowserDocumentReceiptEvidence) { e.Receipts = append(e.Receipts, e.Receipts[0]) },
		"over count": func(e *BrowserDocumentReceiptEvidence) {
			e.Receipts = make([]BrowserDocumentReceipt, MaxBrowserDocumentReceipts+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := receiptContractFixture()
			mutate(e)
			if e.Clone() != nil {
				t.Fatal("malformed proof admitted")
			}
		})
	}
	e := receiptContractFixture()
	e.Receipts[0].WallDriftNS = int64(time.Second)
	e.Receipts[0].ReceivedAt = e.Receipts[0].ReceivedAt.Add(time.Second)
	e.WallStepObserved = true
	if e.Clone() == nil {
		t.Fatal("disclosed wall step should remain readable")
	}
}

func TestBrowserDocumentReceiptWireRequiresExplicitEvidence(t *testing.T) {
	raw, _ := json.Marshal(receiptContractFixture())
	for name, data := range map[string][]byte{
		"missing": []byte(`{"version":1}`), "null scalar": []byte(strings.Replace(string(raw), `"wall_step_observed":false`, `"wall_step_observed":null`, 1)),
		"omitted false":   []byte(strings.Replace(string(raw), `"wall_step_observed":false,`, "", 1)),
		"omitted drift":   []byte(strings.Replace(string(raw), `,"wall_drift_ns":0`, "", 1)),
		"duplicate field": []byte(strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1)),
		"unknown":         []byte(strings.Replace(string(raw), `"version":1`, `"version":1,"qualified":true`, 1)),
		"trailing":        append(append([]byte(nil), raw...), []byte(`{}`)...),
		"oversized":       []byte(strings.Repeat(" ", MaxBrowserDocumentReceiptBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBrowserDocumentReceiptEvidence(data); err == nil {
				t.Fatal("ambiguous wire admitted")
			}
		})
	}
}

func TestBrowserDocumentReceiptIdentityCannotChangeAcrossPartialRecords(t *testing.T) {
	for _, field := range []string{"document", "frame", "origin", "restored frame", "restored origin"} {
		t.Run(field, func(t *testing.T) {
			e := receiptContractFixture()
			end := e.Receipts[0]
			end.Kind = "end"
			end.MeasurementID = ""
			end.Revision = 0
			switch field {
			case "document":
				end.DocumentID = e.SourceGeneration + "-d2"
			case "frame":
				end.FrameID = e.SourceGeneration + "-f2"
			case "origin":
				end.BrowserTimeOriginMS++
			case "restored frame":
				end.LifecycleID = e.SourceGeneration + "-l2"
				end.FrameID = e.SourceGeneration + "-f2"
			case "restored origin":
				end.LifecycleID = e.SourceGeneration + "-l2"
				end.BrowserTimeOriginMS++
			}
			e.Receipts = append(e.Receipts, end)
			if e.Clone() != nil {
				t.Fatal("conflicting identity cloned")
			}
			raw, _ := json.Marshal(e)
			if _, err := DecodeBrowserDocumentReceiptEvidence(raw); err == nil {
				t.Fatal("conflicting identity decoded")
			}
		})
	}
	e := receiptContractFixture()
	end := e.Receipts[0]
	end.Kind = "end"
	end.MeasurementID = ""
	end.Revision = 0
	e.Receipts = append(e.Receipts, end)
	if e.Clone() == nil {
		t.Fatal("consistent partial lifecycle refused")
	}
	end.LifecycleID = e.SourceGeneration + "-l2"
	e.Receipts = append(e.Receipts, end)
	if e.Clone() == nil {
		t.Fatal("restored lifecycle with same document identity refused")
	}
}
