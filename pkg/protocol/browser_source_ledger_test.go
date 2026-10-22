package protocol

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func sourceJournalFixture(t *testing.T) ([]*BrowserDocumentReceiptEvidence, string) {
	t.Helper()
	var records []*BrowserDocumentReceiptEvidence
	journal, err := NewBrowserSourceJournal("run", "controller", func(e *BrowserDocumentReceiptEvidence) error { records = append(records, e.Clone()); return nil })
	if err != nil {
		t.Fatal(err)
	}
	lease, err := journal.Begin()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	g := strings.Repeat("a", 32)
	e := &BrowserDocumentReceiptEvidence{Version: 3, SourceGeneration: g, StartedAt: start.UTC(), Lifetime: &BrowserNativeLifetime{CreationBracketed: true}, Receipts: []BrowserDocumentReceipt{}}
	if err = lease.ExpectContext(e, start); err != nil {
		t.Fatal(err)
	}
	if err = journal.Phase(true, 1); err != nil {
		t.Fatal(err)
	}
	e.Lifetime.Closed = true
	e.Lifetime.EndNS = time.Since(start).Nanoseconds()
	e.Closure = &BrowserNativeSourceClosure{Method: "chromium_cdp_commits_v1", DiscoveryComplete: true, Documents: []BrowserNativeDocumentExpectation{{NativeID: strings.Repeat("b", 64), DocumentID: g + "-d1", LifecycleID: g + "-l1", Terminal: true}}}
	if err = lease.CloseContext(e, true); err != nil {
		t.Fatal(err)
	}
	if err = journal.Phase(false, 1); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(true); err != nil {
		t.Fatal(err)
	}
	if err = journal.Seal(true); err != nil {
		t.Fatal(err)
	}
	return records, g
}
func TestBrowserSourceJournalReconcilesIndependentInventory(t *testing.T) {
	records, g := sourceJournalFixture(t)
	r := NewBrowserSourceReconciler("run", []string{"controller"})
	for _, e := range records {
		if e == nil {
			t.Fatal("invalid journal record")
		}
		if err := r.Observe(e); err != nil {
			t.Fatal(err)
		}
	}
	proof, spans, ok := r.Context(g)
	if !ok || !r.Complete() || len(spans) != 1 || proof.SourceOriginNS <= 0 || proof.BeforeNS < proof.SourceOriginNS || proof.AfterNS < proof.BeforeNS {
		t.Fatalf("invalid reconciled proof: %+v %+v", proof, spans)
	}
	proof.Receipts.Closure.DiscoveryComplete = false
	if next, _, _ := r.Context(g); !next.Receipts.Closure.DiscoveryComplete {
		t.Fatal("proof aliases reconciler")
	}
}
func TestBrowserSourceJournalRefusesIncompletePopulation(t *testing.T) {
	for _, name := range []string{"missing owner", "missing context", "missing terminal", "failed context", "failed slot", "late context", "changed origin", "reordered", "duplicate", "unknown discovery", "missing document", "empty slots", "future lifetime", "document conflict"} {
		t.Run(name, func(t *testing.T) {
			records, _ := sourceJournalFixture(t)
			owners := []string{"controller"}
			switch name {
			case "missing owner":
				owners = append(owners, "unobserved-generator")
			case "missing context":
				records = append(records[:2], records[3:]...)
			case "missing terminal":
				records = records[:len(records)-1]
			case "failed context":
				records[4].Source.Successful = false
			case "failed slot":
				records[6].Source.Successful = false
			case "late context":
				records[2].Lifetime.Closed = true
			case "changed origin":
				records[4].Source.SourceOriginNS++
			case "reordered":
				records[3], records[4] = records[4], records[3]
			case "duplicate":
				records = append(records, records[len(records)-1])
			case "unknown discovery":
				records[4].Closure.DiscoveryComplete = false
			case "missing document":
				records[4].Closure.Documents[0].Terminal = false
			case "empty slots":
				records[4].Closure.Documents = nil
			case "future lifetime":
				records[4].Lifetime.EndNS = records[4].Source.AtNS - records[4].Source.SourceOriginNS + 1
			case "document conflict":
				e := records[4]
				g := e.SourceGeneration
				e.Receipts = []BrowserDocumentReceipt{{DocumentID: g + "-d2", LifecycleID: g + "-l1", FrameID: g + "-f1", Kind: "begin", BrowserTimeOriginMS: 1, ReceivedAt: e.StartedAt}}

			}
			r := NewBrowserSourceReconciler("run", owners)
			for _, e := range records {
				_ = r.Observe(e)
			}
			if r.Complete() {
				t.Fatal("incomplete source admitted")
			}
		})
	}
}
func TestBrowserSourceJournalDurabilityFailureSticky(t *testing.T) {
	for _, panicWriter := range []bool{false, true} {
		calls := 0
		j, err := NewBrowserSourceJournal("run", "controller", func(*BrowserDocumentReceiptEvidence) error {
			calls++
			if calls == 2 {
				if panicWriter {
					panic("disk")
				}
				return errors.New("disk")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if l, err := j.Begin(); err == nil || l != nil {
			t.Fatal("operation admitted without durable expectation")
		}
		if err := j.Seal(true); err == nil {
			t.Fatal("failed expectation forgotten")
		}
	}
}
func TestBrowserSourceV3Projection(t *testing.T) {
	records, _ := sourceJournalFixture(t)
	for _, version := range []int{1, 2} {
		e := BrowserDocumentReceiptProjection(records[4], version)
		if e == nil || e.Source != nil || e.Closure != nil || e.Version != version {
			t.Fatal("invalid projection", version, e)
		}
	}
}

func TestBrowserSourceWholeWindowMap(t *testing.T) {
	for _, name := range []string{"valid", "revision", "raw start", "raw end", "capture", "metric identity", "missing begin", "late lower", "restored", "censored"} {
		t.Run(name, func(t *testing.T) {
			records, g := sourceJournalFixture(t)
			source, o, _ := causalMappingFixture()
			e := records[4]
			e.Receipts = source.Receipts
			for i := range e.Receipts {
				e.Receipts[i].ReceivedAt = e.StartedAt.Add(time.Duration(e.Receipts[i].HostElapsedNS))
			}
			o.CapturedAt = e.Receipts[1].ReceivedAt
			switch name {
			case "revision":
				o.Revision++
			case "raw start":
				o.WindowStart = o.WindowStart.Add(-1)
			case "raw end":
				o.WindowEnd = o.WindowEnd.Add(1)
			case "capture":
				o.CapturedAt = o.CapturedAt.Add(1)
			case "metric identity":
				o.Metric = "fcp"
			case "missing begin":
				e.Receipts = e.Receipts[1:]
			case "late lower":
				e.Closure.Documents[0].LowerNS = 2
			case "restored":
				o.LifecyclePhase = "bfcache_restore"
			case "censored":
				o.State = "censored"
			}
			r := NewBrowserSourceReconciler("run", []string{"controller"})
			for _, e := range records {
				if err := r.Observe(e); err != nil {
					t.Fatal(err)
				}
			}
			w, _, err := r.Map(o)
			if name == "valid" {
				p, _, _ := r.Context(g)
				if err != nil || w.StartNS != p.SourceOriginNS || w.EndNS != p.SourceOriginNS+2 {
					t.Fatal("wrong causal envelope", w, err)
				}
			} else if err == nil {
				t.Fatal("invalid measurement mapped")
			}
		})
	}
}

func TestBrowserSourceUnboundSnapshotsCannotDisappear(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		for _, foreign := range []bool{false, true} {
			records, _ := sourceJournalFixture(t)
			snapshot := BrowserDocumentReceiptProjection(records[4], version)
			snapshot.Source = nil
			if foreign {
				snapshot.SourceGeneration = strings.Repeat("c", 32)
				snapshot.Closure = nil
			}
			r := NewBrowserSourceReconciler("run", []string{"controller"})
			if err := r.Observe(snapshot); err != nil {
				t.Fatal(err)
			}
			for _, e := range records {
				if err := r.Observe(e); err != nil {
					t.Fatal(err)
				}
			}
			if r.Complete() == foreign {
				t.Fatalf("version=%d foreign=%v complete=%v", version, foreign, r.Complete())
			}
		}
	}
}

func TestBrowserSourceControlEventCannotHideForeignReceipts(t *testing.T) {
	for _, index := range []int{1, 3, 6, 7} {
		for _, field := range []string{"generation", "receipt", "lifetime", "closure", "origin"} {
			records, _ := sourceJournalFixture(t)
			e := records[index]
			switch field {
			case "generation":
				e.SourceGeneration = strings.Repeat("c", 32)
			case "receipt":
				q := receiptContractFixture()
				e.SourceGeneration = q.SourceGeneration
				e.StartedAt = q.StartedAt
				e.Receipts = q.Receipts
			case "lifetime":
				e.Lifetime = &BrowserNativeLifetime{CreationBracketed: true}
			case "closure":
				e.Closure = &BrowserNativeSourceClosure{Method: "unavailable"}
			case "origin":
				e.Source.SourceOriginNS = 1
			}
			r := NewBrowserSourceReconciler("run", []string{"controller"})
			for _, record := range records {
				_ = r.Observe(record)
			}
			if r.Complete() {
				t.Fatalf("control %d concealed %s", index, field)
			}
		}
	}
}

func TestBrowserSourceCompletedLookupCacheInvalidatedByNewEvidence(t *testing.T) {
	records, g := sourceJournalFixture(t)
	r := NewBrowserSourceReconciler("run", []string{"controller"})
	for _, e := range records {
		if err := r.Observe(e); err != nil {
			t.Fatal(err)
		}
	}
	if !r.Complete() {
		t.Fatal("valid journal incomplete")
	}
	for i := 0; i < 1000; i++ {
		if _, _, ok := r.Context(g); !ok {
			t.Fatal("immutable lookup failed")
		}
	}
	extra := receiptContractFixture()
	extra.SourceGeneration = strings.Repeat("c", 32)
	extra.Receipts = nil
	if err := r.Observe(extra); err != nil {
		t.Fatal(err)
	}
	if r.Complete() {
		t.Fatal("new source reused cached closure")
	}
}
