package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func authoredPageFixture() *BrowserAuthoredPageBinding {
	s := NewBrowserOriginalAuthoredStep(BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}, "actions[0]")
	g := strings.Repeat("c", 32)
	return &BrowserAuthoredPageBinding{Version: 1, State: "bound", ObservedAt: time.Now().UTC(), AuthoredStep: s, PageID: BrowserAuthoredPageID(s), SourceGeneration: g, RequestID: g + "-r1", DocumentID: g + "-d1", LifecycleID: g + "-l1", Association: "cdp_loader_join"}
}
func TestBrowserAuthoredPageBindingContract(t *testing.T) {
	good := authoredPageFixture()
	if !good.Valid() {
		t.Fatal(good)
	}
	for name, mutate := range map[string]func(*BrowserAuthoredPageBinding){
		"future":               func(b *BrowserAuthoredPageBinding) { b.Version = 4 },
		"v3 on v1 association": func(b *BrowserAuthoredPageBinding) { b.Version = 3 },
		"missing source":       func(b *BrowserAuthoredPageBinding) { b.AuthoredStep = nil },
		"legacy input": func(b *BrowserAuthoredPageBinding) {
			b.AuthoredStep = NewBrowserAuthoredStep(strings.Repeat("a", 64), "actions[0]")
		},
		"wrong page":        func(b *BrowserAuthoredPageBinding) { b.PageID = strings.Repeat("d", 64) },
		"foreign document":  func(b *BrowserAuthoredPageBinding) { b.DocumentID = strings.Repeat("e", 32) + "-d1" },
		"missing lifecycle": func(b *BrowserAuthoredPageBinding) { b.LifecycleID = "" },
		"URL guess":         func(b *BrowserAuthoredPageBinding) { b.Association = "same_url" },
		"mixed unavailable": func(b *BrowserAuthoredPageBinding) { b.Reason = "navigation_join_unavailable" },
	} {
		t.Run(name, func(t *testing.T) {
			b := good.Clone()
			mutate(b)
			if b.Valid() || b.Clone() != nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
	copy := good.Clone()
	copy.AuthoredStep.StepPath = "actions[1]"
	if good.AuthoredStep.StepPath != "actions[0]" {
		t.Fatal("alias")
	}
	raw, _ := json.Marshal(good)
	if _, err := DecodeBrowserAuthoredPageBinding(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(raw, raw...), []byte(strings.Repeat(" ", 16<<10+1)), []byte(strings.Replace(string(raw), `"version":1`, `"version":1,"extra":true`, 1))} {
		if _, err := DecodeBrowserAuthoredPageBinding(bad); err == nil {
			t.Fatal("invalid wire accepted")
		}
	}
}
func TestBrowserAuthoredPageMissingnessAndStructuralIdentity(t *testing.T) {
	b := &BrowserAuthoredPageBinding{Version: 1, State: "unavailable", Reason: "original_source_unavailable", ObservedAt: time.Now().UTC()}
	if !b.Valid() {
		t.Fatal(b)
	}
	b.AuthoredStep = authoredPageFixture().AuthoredStep
	for _, reason := range []string{"navigation_response_unavailable", "navigation_join_unavailable", "document_lifecycle_unavailable", "unsupported_navigation_binding"} {
		b.Reason = reason
		if !b.Valid() {
			t.Fatal(reason)
		}
	}
	b.PageID = BrowserAuthoredPageID(b.AuthoredStep)
	if b.Valid() {
		t.Fatal("unavailable proof invented a key")
	}
	other := NewBrowserOriginalAuthoredStep(BrowserAuthoredDefinition{Version: 1, ScriptSHA256: b.AuthoredStep.ScriptSHA256, StructureSHA256: b.AuthoredStep.SourceStructureSHA256}, "actions[1]")
	if BrowserAuthoredPageID(other) == b.PageID {
		t.Fatal("distinct locations pooled")
	}
	if BrowserAuthoredPageID(nil) != "" {
		t.Fatal("missing source invented a key")
	}
}

func TestBrowserAuthoredPageBindingV3Contract(t *testing.T) {
	step := NewBrowserOriginalAuthoredStep(BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}, "actions[2]")
	gen := strings.Repeat("c", 32)
	for _, tc := range []struct {
		name        string
		association string
		requestID   string
		routeSeq    uint64
		valid       bool
	}{
		{"history with request", BrowserAuthoredPageAssociationHistoryTraversal, gen + "-r1", 0, true},
		{"history without request", BrowserAuthoredPageAssociationHistoryTraversal, "", 1, true},
		{"same document route", BrowserAuthoredPageAssociationSameDocumentRoute, "", 2, true},
		{"same document route with request rejected", BrowserAuthoredPageAssociationSameDocumentRoute, gen + "-r1", 2, false},
		{"same document route zero seq rejected", BrowserAuthoredPageAssociationSameDocumentRoute, "", 0, false},
		{"opened by action", BrowserAuthoredPageAssociationOpenedByAction, gen + "-r2", 0, true},
		{"opened by action without request", BrowserAuthoredPageAssociationOpenedByAction, "", 0, true},
		{"opened by action non-zero route_seq rejected", BrowserAuthoredPageAssociationOpenedByAction, gen + "-r2", 1, false},
		{"redirect chain", BrowserAuthoredPageAssociationRedirectChain, gen + "-r3", 0, true},
		{"redirect chain missing request rejected", BrowserAuthoredPageAssociationRedirectChain, "", 0, false},
		{"redirect chain non-zero route_seq rejected", BrowserAuthoredPageAssociationRedirectChain, gen + "-r3", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &BrowserAuthoredPageBinding{
				Version:            BrowserAuthoredPageVersion3,
				State:              "bound",
				ObservedAt:         time.Now().UTC(),
				AuthoredStep:       step.Clone(),
				PageID:             BrowserAuthoredPageID(step),
				SourceGeneration:   gen,
				RequestID:          tc.requestID,
				DocumentID:         gen + "-d1",
				LifecycleID:        gen + "-l1",
				Association:        tc.association,
				NavigationSequence: 3,
				RouteSeq:           tc.routeSeq,
			}
			if b.Valid() != tc.valid {
				t.Fatalf("Valid() = %t, want %t for %+v", b.Valid(), tc.valid, b)
			}
			if tc.valid {
				raw, err := json.Marshal(b)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := DecodeBrowserAuthoredPageBinding(raw)
				if err != nil || decoded.RouteSeq != tc.routeSeq || decoded.Association != tc.association {
					t.Fatalf("decoded=%+v err=%v", decoded, err)
				}
			}
		})
	}
	for _, reason := range []string{
		BrowserAuthoredPageUnavailableHistoryTraversal,
		BrowserAuthoredPageUnavailableSameDocumentRoute,
		BrowserAuthoredPageUnavailablePopupCommit,
		BrowserAuthoredPageUnavailableRedirectChain,
	} {
		u := &BrowserAuthoredPageBinding{
			Version:      BrowserAuthoredPageVersion3,
			State:        "unavailable",
			Reason:       reason,
			ObservedAt:   time.Now().UTC(),
			AuthoredStep: step.Clone(),
		}
		if !u.Valid() {
			t.Fatalf("v3 unavailable reason %q rejected", reason)
		}
	}
}
