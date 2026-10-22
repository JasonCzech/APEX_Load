package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
)

func TestArchiveAuthoredNativePageProofVersionsAndConflict(t *testing.T) {
	for _, conflict := range []string{"sequence", "proof_version", "source"} {
		t.Run(conflict, func(t *testing.T) {
			b, sources := archiveAuthoredPageFixture(1)
			b.Version, b.Association, b.NavigationSequence = 2, "native_navigation_commit", 17
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := protocol.DecodeBrowserAuthoredPageBinding(raw)
			if err != nil {
				t.Fatal(err)
			}
			a := newArchiveAuthoredPageAccumulator()
			a.merge(pageBindingTransaction(decoded), sources)
			a.merge(pageBindingTransaction(decoded), sources)
			if r := a.result(); r.Incomplete || len(r.Bindings) != 1 || r.Bindings[0].NavigationSequence != 17 {
				t.Fatal(r)
			}
			bad := decoded.Clone()
			switch conflict {
			case "sequence":
				bad.NavigationSequence++
			case "proof_version":
				bad.Version, bad.Association, bad.NavigationSequence = 1, "cdp_loader_join", 0
			case "source":
				sources = nil
			}
			a.merge(pageBindingTransaction(bad), sources)
			r := a.result()
			if !r.Incomplete {
				t.Fatal("incompatible proof was silently deduplicated", r)
			}
			if conflict == "source" {
				if r.Unavailable["original_source_mismatch"] != 1 {
					t.Fatal(r)
				}
				return
			}
			a.merge(pageBindingTransaction(decoded), sources)
			r = a.result()
			if len(r.Bindings) != 0 || r.Unavailable["conflicting_binding"] != 2 {
				t.Fatal("conflicting proof readmitted", r)
			}
		})
	}
}
