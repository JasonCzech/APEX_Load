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
		"future":         func(b *BrowserAuthoredPageBinding) { b.Version = 3 },
		"missing source": func(b *BrowserAuthoredPageBinding) { b.AuthoredStep = nil },
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
