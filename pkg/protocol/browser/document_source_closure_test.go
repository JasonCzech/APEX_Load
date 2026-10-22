package browser

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserSourceNativeClosure(t *testing.T) {
	for _, tc := range []struct {
		name, engine, body string
		complete           bool
	}{
		{"main frame", "chromium", "<h1>Independent native census</h1>", true},
		{"silent child", "chromium", "<iframe src='about:blank'></iframe>", false},
		{"unavailable engine", "webkit", "<h1>Retained raw evidence</h1>", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, "<!doctype html>"+tc.body)
			}))
			defer server.Close()
			var records []*protocol.BrowserDocumentReceiptEvidence
			journal, err := protocol.NewBrowserSourceJournal("native-run", "controller", func(e *protocol.BrowserDocumentReceiptEvidence) error {
				records = append(records, e.Clone())
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := journal.Begin()
			if err != nil {
				t.Fatal(err)
			}
			b, ctx := documentActualBrowser(t, tc.engine, server.URL, lease)
			if err = journal.Phase(true, 1); err != nil {
				t.Fatal(err)
			}
			documentActualExecute(t, b, ctx, &protocol.Action{Type: "browser_navigate", Target: server.URL})
			if err = b.Teardown(ctx); err != nil {
				t.Fatal(err)
			}
			if err = lease.Finish(true); err != nil {
				t.Fatal(err)
			}
			if err = journal.Seal(true); err != nil {
				t.Fatal(err)
			}
			r := protocol.NewBrowserSourceReconciler("native-run", []string{"controller"})
			for _, e := range records {
				if err = r.Observe(e); err != nil {
					t.Fatal(err)
				}
			}
			e := b.BrowserDocumentReceiptEvidence()
			if tc.complete {
				mapped := 0
				for _, o := range b.BrowserDocumentEvidence().Observations {
					if o.State != "final" || !o.Available {
						continue
					}
					window, spans, err := r.Map(o)
					if err != nil {
						t.Fatal("native measurement map", err)
					}
					contained := false
					for _, span := range spans {
						if protocol.ContainedInBrowserSteadySpan(window, []protocol.BrowserMonotonicSpan{span.BrowserMonotonicSpan}) {
							contained = true
						}
					}
					if !contained {
						t.Fatalf("measurement crosses steady boundary: %+v %+v", window, spans)
					}
					mapped++
				}
				if mapped == 0 {
					t.Fatal("no native measurement mapped")
				}
			}

			if r.Complete() != tc.complete {
				t.Fatalf("complete=%v want=%v native=%+v docs=%+v", r.Complete(), tc.complete, e.Closure, e.Closure.Documents)
			}
		})
	}
}
