package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestInteractionKeyboardTargetAggregationIsBounded(t *testing.T) {
	const limit = 1024
	a := newInteractionAnalyzer("run", map[string]browserGenerationCohort{e4Generation: {engine: "chromium", profile: "mobile"}})
	add := func(id int, layout string) {
		row := e4Tap(id+1, 0, 0, 0)
		row.Kind, row.Points, row.PointerType = "fill", nil, ""
		row.LayoutRevision = layout
		row.Target = &protocol.BrowserInteractionTarget{Role: "textbox", LabelSHA256: fmt.Sprintf("%064x", id)}
		if !row.Valid() {
			t.Fatal("invalid fixture")
		}
		a.add(&protocol.BrowserInteractionEvidence{Version: 1, SourceGeneration: e4Generation, Interactions: []protocol.BrowserInteraction{row}})
	}
	for i := 0; i < limit+10; i++ {
		add(i, "00000000000000aa")
	}
	for _, m := range a.maps {
		if len(m.targets) != limit {
			t.Fatalf("target aggregation has %d keys, want bounded %d", len(m.targets), limit)
		}
	}
	// Existing targets still count exactly, and the bound is local to each map.
	add(0, "00000000000000aa")
	add(limit+1, "00000000000000bb")
	// A full keyboard table does not block independently bounded pointer cells.
	pointer := e4Tap(limit+100, 0, 10, 10)
	a.add(&protocol.BrowserInteractionEvidence{Version: 1, SourceGeneration: e4Generation, Interactions: []protocol.BrowserInteraction{pointer}})
	got := a.finish()
	if got.Coverage.Excluded["keyboard_targets_truncated"] != 10 || got.Coverage.Mapped != limit+3 || got.Coverage.Interactions != limit+13 {
		t.Fatalf("coverage does not reconcile: %+v", got.Coverage)
	}
	var full *BrowserInteractionHeatmap
	for i := range got.Heatmaps {
		if got.Heatmaps[i].LayoutRevision == "00000000000000aa" {
			full = &got.Heatmaps[i]
		}
	}
	if full == nil || len(full.Targets) != maxHeatmapTargets || full.Targets[0].Count != 2 || full.Points != 1 {
		t.Fatalf("retained counts changed: %+v", full)
	}
	notes := strings.Join(full.Notes, " ")
	if !strings.Contains(notes, "10 keyboard gestures excluded") || !strings.Contains(notes, "retained targets") {
		t.Fatalf("missing scope disclosure: %s", notes)
	}
}
