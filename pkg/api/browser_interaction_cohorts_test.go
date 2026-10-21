package api

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestInteractionMapCohortIsolation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*protocol.BrowserInteraction)
		maps     int
		excluded string
	}{
		{"same cohort", func(*protocol.BrowserInteraction) {}, 1, ""},
		{"horizontal scroll", func(i *protocol.BrowserInteraction) { i.ScrollX = 1 }, 2, ""},
		{"fractional scroll", func(i *protocol.BrowserInteraction) { i.ScrollY = 0.25 }, 2, ""},
		{"negative scroll", func(i *protocol.BrowserInteraction) { i.ScrollX = -0.25 }, 2, ""},
		{"negative zero", func(i *protocol.BrowserInteraction) { i.ScrollX = math.Copysign(0, -1) }, 1, ""},
		{"layout", func(i *protocol.BrowserInteraction) { i.LayoutRevision = "bbbb" }, 2, ""},
		{"missing layout", func(i *protocol.BrowserInteraction) { i.LayoutRevision = "" }, 1, "layout_unknown"},
		{"child frame without reason", func(i *protocol.BrowserInteraction) { i.FrameScope = "child_frame" }, 1, protocol.InteractionUnavailableChildFrame},
		{"missing context", func(i *protocol.BrowserInteraction) { i.SourceGeneration = strings.Repeat("1", 32) }, 1, "engine_profile_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, next := e4Tap(1, 0, 10, 10), e4Tap(2, time.Second, 10, 10)
			tc.change(&next)
			next.InteractionID = strings.ReplaceAll(next.InteractionID, e4Generation, next.SourceGeneration)
			next.ActionID = strings.ReplaceAll(next.ActionID, e4Generation, next.SourceGeneration)
			next.DocumentID = strings.ReplaceAll(next.DocumentID, e4Generation, next.SourceGeneration)
			if !next.Valid() {
				t.Fatal("invalid source evidence fixture")
			}
			before, err := json.Marshal([]protocol.BrowserInteraction{first, next})
			if err != nil {
				t.Fatal(err)
			}
			got := analyzeE4(first, next)
			wantMapped := int64(2)
			if tc.excluded != "" {
				wantMapped = 1
			}
			if got.Version != "browser_interaction_patterns.v2" || len(got.Heatmaps) != tc.maps || got.Coverage.Mapped != wantMapped || (tc.excluded != "" && got.Coverage.Excluded[tc.excluded] != 1) {
				t.Fatalf("analysis %+v", got)
			}
			var exclusions int64
			for _, count := range got.Coverage.Excluded {
				exclusions += count
			}
			if got.Coverage.Mapped+exclusions != got.Coverage.Interactions {
				t.Fatalf("unreconciled coverage %+v", got.Coverage)
			}
			// Derived analysis must not rewrite retained version-1 evidence.
			after, err := json.Marshal([]protocol.BrowserInteraction{first, next})
			if err != nil || string(after) != string(before) {
				t.Fatal("raw evidence changed", err)
			}

		})
	}
}

func TestInteractionMapEngineProfileCohorts(t *testing.T) {
	for _, tc := range []struct {
		name, engine, profile string
		mapped                bool
		maps                  int
	}{
		{"same mobile", "chromium", "mobile", true, 1},
		{"other engine", "webkit", "mobile", true, 2},
		{"other profile", "chromium", "ipad", true, 2},
		{"default desktop", "chromium", "", true, 2},
		{"missing engine", "", "mobile", false, 1},
		{"unknown engine", "unknown", "mobile", false, 1},
		{"unknown profile", "chromium", "unknown", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := strings.Repeat("1", 32)
			a := newInteractionAnalyzer("run", map[string]browserGenerationCohort{e4Generation: {engine: "chromium", profile: "mobile"}, other: {engine: tc.engine, profile: tc.profile}})
			i := e4Tap(2, time.Second, 10, 10)
			i.SourceGeneration = other
			i.InteractionID = strings.ReplaceAll(i.InteractionID, e4Generation, other)
			i.ActionID = strings.ReplaceAll(i.ActionID, e4Generation, other)
			i.DocumentID = strings.ReplaceAll(i.DocumentID, e4Generation, other)
			if !i.Valid() {
				t.Fatal("invalid source evidence fixture")
			}
			a.add(&protocol.BrowserInteractionEvidence{Interactions: []protocol.BrowserInteraction{e4Tap(1, 0, 10, 10), i}})
			got := a.finish()
			want := int64(1)
			if tc.mapped {
				want = 2
			}
			if got.Coverage.Mapped != want || len(got.Heatmaps) != tc.maps {
				t.Fatalf("analysis %+v", got)
			}
		})
	}
}

func TestBrowserGenerationCohortConflictCannotRecover(t *testing.T) {
	for _, change := range []string{"engine", "profile"} {
		t.Run(change, func(t *testing.T) {
			c := browserGenerationCohorts{byGeneration: map[string]browserGenerationCohort{}, bound: 1}
			first := protocol.BrowserDocumentObservation{SourceGeneration: e4Generation, Engine: "chromium", DeviceProfile: "mobile"}
			conflicting := first
			if change == "engine" {
				conflicting.Engine = "webkit"
			} else {
				conflicting.DeviceProfile = "ipad"
			}
			for _, o := range []protocol.BrowserDocumentObservation{first, conflicting, first} {
				c.add(&protocol.BrowserDocumentEvidence{Observations: []protocol.BrowserDocumentObservation{o}})
			}
			if got := c.byGeneration[e4Generation]; got != (browserGenerationCohort{}) || len(c.byGeneration) != 1 || c.truncated {
				t.Fatalf("conflict not retained %+v", c)
			}
			a := newInteractionAnalyzer("run", c.byGeneration)
			a.add(&protocol.BrowserInteractionEvidence{Interactions: []protocol.BrowserInteraction{e4Tap(1, 0, 10, 10)}})
			if got := a.finish(); len(got.Heatmaps) != 0 || got.Coverage.Excluded["engine_profile_unknown"] != 1 {
				t.Fatalf("conflict mapped %+v", got)
			}
		})
	}
}
