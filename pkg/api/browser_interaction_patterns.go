package api

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Browser interaction patterns and heatmaps (E4, BI-3), read from a run's
// archived interaction evidence (E3). Patterns are measured, with their
// windows and thresholds: repeated gestures, retries, unexpected
// navigations and dead interactions. A scripted or unknown gesture never
// yields a human-frustration label: a script repeating a tap is
// scripted_repetition. Heatmaps count where pointer gestures were dispatched
// on an abstract grid of at most 64×64 cells, one map per sanitized route,
// layout revision, viewport and orientation, engine and device profile;
// incompatible layouts never merge. Nothing here injects interactions or
// probe traffic: it reads what the run recorded.

const (
	browserInteractionAnalysisVersion = "browser_interaction_patterns.v1"
	// A repeated gesture is at least repeatMinGestures pointer gestures
	// within repeatWindow and repeatRadiusPx CSS pixels of the first, on one
	// document, route and layout.
	repeatMinGestures = 3
	repeatWindow      = 2 * time.Second
	repeatRadiusPx    = 32.0
	// heatmapGrid is a map's cells per side.
	heatmapGrid = 64
	// Output and memory bounds.
	maxInteractionPatterns       = 200
	maxInteractionPatternMembers = 16
	maxInteractionHeatmaps       = 64
	maxHeatmapTargets            = 64
	maxRetainedInteractions      = 100000
)

// Interaction pattern kinds.
const (
	interactionPatternScriptedRepetition   = "scripted_repetition"
	interactionPatternRepeatedGesture      = "repeated_gesture"
	interactionPatternRetry                = "retry"
	interactionPatternUnexpectedNavigation = "unexpected_navigation"
	interactionPatternDeadInteraction      = "dead_interaction"
)

// BrowserInteractionAnalysis is a run's interaction patterns and heatmaps.
type BrowserInteractionAnalysis struct {
	RunID   string `json:"run_id"`
	Version string `json:"version"`
	// Status is ready, or not_captured when the run recorded no interaction
	// evidence.
	Status     string                       `json:"status"`
	Thresholds BrowserInteractionThresholds `json:"thresholds"`
	Coverage   BrowserInteractionCoverage   `json:"coverage"`
	Patterns   []BrowserInteractionPattern  `json:"patterns"`
	Heatmaps   []BrowserInteractionHeatmap  `json:"heatmaps"`
	// Notes state what the analysis does not claim.
	Notes       []string `json:"notes"`
	Limitations []string `json:"limitations,omitempty"`
}

// BrowserInteractionThresholds are the rules the patterns were found under.
type BrowserInteractionThresholds struct {
	RepeatMinGestures int     `json:"repeat_min_gestures"`
	RepeatWindowMs    int64   `json:"repeat_window_ms"`
	RepeatRadiusPx    float64 `json:"repeat_radius_px"`
	HeatmapGrid       int     `json:"heatmap_grid"`
}

// BrowserInteractionCoverage counts the evidence and what was left out.
type BrowserInteractionCoverage struct {
	Interactions int64 `json:"interactions"`
	Records      int64 `json:"records"`
	// Dropped counts interactions capture bounds left out; Incomplete says
	// some source's evidence is missing.
	Dropped      int64            `json:"dropped"`
	Incomplete   bool             `json:"incomplete"`
	ByProvenance map[string]int64 `json:"by_provenance"`
	Mapped       int64            `json:"mapped"`
	// Excluded counts interactions no heatmap holds, by reason (the
	// capture's unavailable reasons, no_points, no_viewport,
	// outside_viewport, heatmaps_truncated).
	Excluded map[string]int64 `json:"excluded,omitempty"`
}

// BrowserInteractionPattern is one measured pattern.
type BrowserInteractionPattern struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Provenance is scripted, recorded, unknown, or mixed.
	Provenance     string    `json:"provenance"`
	Route          string    `json:"route,omitempty"`
	LayoutRevision string    `json:"layout_revision,omitempty"`
	ActionID       string    `json:"action_id,omitempty"`
	ActionName     string    `json:"action_name,omitempty"`
	Count          int       `json:"count"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	// Declared and DeadlineMs are a dead interaction's authored outcome.
	Declared     string   `json:"declared,omitempty"`
	DeadlineMs   int      `json:"deadline_ms,omitempty"`
	Detail       string   `json:"detail"`
	Interactions []string `json:"interactions"`
}

// BrowserInteractionHeatmap is one compatible layout's map.
type BrowserInteractionHeatmap struct {
	Key            string `json:"key"`
	Route          string `json:"route"`
	LayoutRevision string `json:"layout_revision"`
	ViewportWidth  int    `json:"viewport_width"`
	ViewportHeight int    `json:"viewport_height"`
	Orientation    string `json:"orientation"`
	Engine         string `json:"engine"`
	DeviceProfile  string `json:"device_profile"`
	Columns        int    `json:"columns"`
	Rows           int    `json:"rows"`
	// Cells are the occupied cells as [row, column, count].
	Cells   [][3]int                        `json:"cells"`
	Points  int64                           `json:"points"`
	Targets []BrowserInteractionTargetCount `json:"targets,omitempty"`
	ByKind  map[string]int64                `json:"by_kind"`
	Sources map[string]int64                `json:"by_provenance"`
	Notes   []string                        `json:"notes,omitempty"`
	cells   map[[2]int]int                  `json:"-"`
	targets map[interactionTargetKey]int64  `json:"-"`
}

// BrowserInteractionTargetCount is a keyboard or form target's gestures; a
// target has a role and a hashed label, never text or a position.
type BrowserInteractionTargetCount struct {
	Role        string `json:"role"`
	LabelSHA256 string `json:"label_sha256,omitempty"`
	Count       int64  `json:"count"`
}

// interactionTargetKey identifies a keyboard target in a map.
type interactionTargetKey struct{ role, label string }

// browserGenerationCohort is a source generation's engine and profile, from
// its document evidence.
type browserGenerationCohort struct{ engine, profile string }

// interactionAnalyzer accumulates interactions in archive order.
type interactionAnalyzer struct {
	analysis BrowserInteractionAnalysis
	cohorts  map[string]browserGenerationCohort
	maps     map[string]*BrowserInteractionHeatmap
	retained []protocol.BrowserInteraction
}

func newInteractionAnalyzer(runID string, cohorts map[string]browserGenerationCohort) *interactionAnalyzer {
	return &interactionAnalyzer{cohorts: cohorts, maps: map[string]*BrowserInteractionHeatmap{}, analysis: BrowserInteractionAnalysis{
		RunID: runID, Version: browserInteractionAnalysisVersion, Status: "ready",
		Thresholds: BrowserInteractionThresholds{RepeatMinGestures: repeatMinGestures, RepeatWindowMs: repeatWindow.Milliseconds(), RepeatRadiusPx: repeatRadiusPx, HeatmapGrid: heatmapGrid},
		Coverage:   BrowserInteractionCoverage{ByProvenance: map[string]int64{}, Excluded: map[string]int64{}},
		Patterns:   []BrowserInteractionPattern{}, Heatmaps: []BrowserInteractionHeatmap{},
		Notes: []string{
			"Patterns describe the run's recorded gestures. Scripted or unknown gestures never carry a human-frustration label; a scripted repeat is scripted_repetition.",
			"A dead interaction needs an authored expected outcome whose deadline was missed; an unchanged page alone is never one.",
			"Heatmaps show where gestures were dispatched on an abstract grid of the viewport, not production user behavior. Screenshot overlays are not produced.",
		},
	}}
}

func (a *interactionAnalyzer) add(evidence *protocol.BrowserInteractionEvidence) {
	a.analysis.Coverage.Records++
	a.analysis.Coverage.Dropped += evidence.Dropped
	a.analysis.Coverage.Incomplete = a.analysis.Coverage.Incomplete || evidence.Incomplete
	for _, interaction := range evidence.Interactions {
		a.analysis.Coverage.Interactions++
		a.analysis.Coverage.ByProvenance[interaction.Provenance]++
		a.mapInteraction(interaction)
		if len(a.retained) < maxRetainedInteractions {
			a.retained = append(a.retained, interaction)
		} else if len(a.analysis.Limitations) == 0 || a.analysis.Limitations[len(a.analysis.Limitations)-1] != "interactions_truncated" {
			a.analysis.Limitations = append(a.analysis.Limitations, "interactions_truncated")
		}
	}
}

// mapInteraction adds one interaction to its layout's heatmap: a pointer
// gesture at its first dispatched point, a keyboard gesture to the target
// table.
func (a *interactionAnalyzer) mapInteraction(i protocol.BrowserInteraction) {
	exclude := func(reason string) { a.analysis.Coverage.Excluded[reason]++ }
	if i.UnavailableReason != "" {
		exclude(i.UnavailableReason)
		return
	}
	pointer := protocol.BrowserInteractionPointerKind(i.Kind)
	keyboard := i.Target != nil && (!pointer || i.Kind == "check")
	if pointer && len(i.Points) == 0 && !keyboard {
		exclude("no_points")
		return
	}
	if !pointer && !keyboard {
		// A scroll names no target and has no point.
		exclude("no_points")
		return
	}
	if i.ViewportWidth <= 0 || i.ViewportHeight <= 0 {
		exclude("no_viewport")
		return
	}
	cohort, ok := a.cohorts[i.SourceGeneration]
	if !ok {
		cohort = browserGenerationCohort{engine: "unknown", profile: "unknown"}
	}
	orientation := "landscape"
	if i.ViewportHeight > i.ViewportWidth {
		orientation = "portrait"
	}
	key := strings.Join([]string{i.Route, i.LayoutRevision, fmt.Sprintf("%dx%d", i.ViewportWidth, i.ViewportHeight), orientation, cohort.engine, cohort.profile}, "\x00")
	m := a.maps[key]
	if m == nil {
		if len(a.maps) >= maxInteractionHeatmaps {
			exclude("heatmaps_truncated")
			return
		}
		sum := sha256.Sum256([]byte(key))
		m = &BrowserInteractionHeatmap{Key: hex.EncodeToString(sum[:8]), Route: i.Route, LayoutRevision: i.LayoutRevision, ViewportWidth: i.ViewportWidth, ViewportHeight: i.ViewportHeight, Orientation: orientation,
			Engine: cohort.engine, DeviceProfile: cohort.profile, Columns: heatmapGrid, Rows: heatmapGrid, Cells: [][3]int{}, ByKind: map[string]int64{}, Sources: map[string]int64{},
			cells: map[[2]int]int{}, targets: map[interactionTargetKey]int64{}}
		if i.LayoutRevision == "" {
			m.Notes = append(m.Notes, "layout revision unavailable: gestures of different layouts on this route may share the map")
		}
		a.maps[key] = m
	}
	if pointer && len(i.Points) > 0 {
		p := i.Points[0]
		if p.X < 0 || p.Y < 0 || p.X >= float64(i.ViewportWidth) || p.Y >= float64(i.ViewportHeight) {
			exclude("outside_viewport")
			return
		}
		col := int(p.X * heatmapGrid / float64(i.ViewportWidth))
		row := int(p.Y * heatmapGrid / float64(i.ViewportHeight))
		m.cells[[2]int{row, col}]++
		m.Points++
	} else {
		label := ""
		if i.Target != nil {
			label = i.Target.LabelSHA256
			m.targets[interactionTargetKey{role: i.Target.Role, label: label}]++
		}
	}
	m.ByKind[i.Kind]++
	m.Sources[i.Provenance]++
	a.analysis.Coverage.Mapped++
}

// finish orders and bounds the heatmaps and finds the patterns.
func (a *interactionAnalyzer) finish() BrowserInteractionAnalysis {
	for _, m := range a.maps {
		for cell, count := range m.cells {
			m.Cells = append(m.Cells, [3]int{cell[0], cell[1], count})
		}
		slices.SortFunc(m.Cells, func(x, y [3]int) int { return cmp.Or(cmp.Compare(x[0], y[0]), cmp.Compare(x[1], y[1])) })
		for target, count := range m.targets {
			m.Targets = append(m.Targets, BrowserInteractionTargetCount{Role: target.role, LabelSHA256: target.label, Count: count})
		}
		slices.SortFunc(m.Targets, func(x, y BrowserInteractionTargetCount) int {
			return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.Role, y.Role), cmp.Compare(x.LabelSHA256, y.LabelSHA256))
		})
		if len(m.Targets) > maxHeatmapTargets {
			m.Notes = append(m.Notes, fmt.Sprintf("kept the %d most used of %d keyboard targets", maxHeatmapTargets, len(m.Targets)))
			m.Targets = m.Targets[:maxHeatmapTargets]
		}
		a.analysis.Heatmaps = append(a.analysis.Heatmaps, *m)
	}
	slices.SortFunc(a.analysis.Heatmaps, func(x, y BrowserInteractionHeatmap) int {
		return cmp.Or(cmp.Compare(y.Points+sumCounts(y.Targets), x.Points+sumCounts(x.Targets)), cmp.Compare(x.Key, y.Key))
	})
	a.analysis.Patterns = findInteractionPatterns(a.retained)
	if len(a.analysis.Patterns) > maxInteractionPatterns {
		a.analysis.Limitations = append(a.analysis.Limitations, fmt.Sprintf("patterns_truncated: kept %d of %d", maxInteractionPatterns, len(a.analysis.Patterns)))
		a.analysis.Patterns = a.analysis.Patterns[:maxInteractionPatterns]
	}
	if len(a.analysis.Coverage.Excluded) == 0 {
		a.analysis.Coverage.Excluded = nil
	}
	return a.analysis
}

func sumCounts(targets []BrowserInteractionTargetCount) int64 {
	var total int64
	for _, t := range targets {
		total += t.Count
	}
	return total
}

// findInteractionPatterns finds retries, dead interactions, unexpected
// navigations and repeated gestures, per document in capture order.
func findInteractionPatterns(interactions []protocol.BrowserInteraction) []BrowserInteractionPattern {
	byDocument := map[string][]protocol.BrowserInteraction{}
	var documents []string
	for _, i := range interactions {
		key := i.SourceGeneration + "\x00" + i.DocumentID
		if _, ok := byDocument[key]; !ok {
			documents = append(documents, key)
		}
		byDocument[key] = append(byDocument[key], i)
	}
	var patterns []BrowserInteractionPattern
	for _, key := range documents {
		gestures := byDocument[key]
		slices.SortStableFunc(gestures, func(x, y protocol.BrowserInteraction) int { return x.CapturedAt.Compare(y.CapturedAt) })
		retried := map[string]bool{}
		for index, g := range gestures {
			switch {
			case g.Outcome.Observed == protocol.InteractionOutcomeMissed:
				// The authored outcome's deadline passed.
				patterns = append(patterns, interactionPattern(interactionPatternDeadInteraction, []protocol.BrowserInteraction{g},
					fmt.Sprintf("the declared %s outcome was not observed within %d ms", g.Outcome.Declared, g.Outcome.DeadlineMS)))
				patterns[len(patterns)-1].Declared, patterns[len(patterns)-1].DeadlineMs = g.Outcome.Declared, g.Outcome.DeadlineMS
			case g.UnavailableReason == protocol.InteractionUnavailableDocumentChanged && inPageOutcome(g.Outcome.Declared):
				patterns = append(patterns, interactionPattern(interactionPatternUnexpectedNavigation, []protocol.BrowserInteraction{g},
					fmt.Sprintf("the gesture declared an in-page %s outcome but navigated away", g.Outcome.Declared)))
			}
			// A failed gesture repeated by the same action is a retry; the
			// attempts never count as repeated gestures.
			if !g.Success && g.ActionID != "" && !retried[g.InteractionID] {
				attempts := []protocol.BrowserInteraction{g}
				for _, next := range gestures[index+1:] {
					if next.ActionID != g.ActionID {
						continue
					}
					attempts = append(attempts, next)
					if next.Success {
						break
					}
				}
				if len(attempts) > 1 {
					for _, attempt := range attempts {
						retried[attempt.InteractionID] = true
					}
					patterns = append(patterns, interactionPattern(interactionPatternRetry, attempts, fmt.Sprintf("%d attempts of one action after a failed gesture", len(attempts))))
				}
			}
		}
		patterns = append(patterns, repeatedGestures(gestures, retried)...)
	}
	slices.SortStableFunc(patterns, func(x, y BrowserInteractionPattern) int {
		return cmp.Or(x.Start.Compare(y.Start), cmp.Compare(x.ID, y.ID))
	})
	return patterns
}

// inPageOutcome reports whether a declared outcome expects the page to stay:
// an element, a stable DOM or a pause, rather than a load.
func inPageOutcome(declared string) bool {
	switch declared {
	case "element_visible", "visible", "dom_stable", "duration", "sleep":
		return true
	}
	return false
}

// repeatedGestures finds runs of at least repeatMinGestures pointer
// gestures, within repeatWindow and repeatRadiusPx of the run's first, on
// one route and layout. A double click is one declared gesture; retries are
// excluded.
func repeatedGestures(gestures []protocol.BrowserInteraction, retried map[string]bool) []BrowserInteractionPattern {
	var candidates []protocol.BrowserInteraction
	for _, g := range gestures {
		if protocol.BrowserInteractionPointerKind(g.Kind) && g.Kind != "dblclick" && len(g.Points) > 0 && g.UnavailableReason == "" && !retried[g.InteractionID] {
			candidates = append(candidates, g)
		}
	}
	var patterns []BrowserInteractionPattern
	for start := 0; start < len(candidates); {
		first := candidates[start]
		end := start + 1
		for end < len(candidates) {
			next := candidates[end]
			if next.Route != first.Route || next.LayoutRevision != first.LayoutRevision || next.CapturedAt.Sub(first.CapturedAt) > repeatWindow ||
				math.Hypot(next.Points[0].X-first.Points[0].X, next.Points[0].Y-first.Points[0].Y) > repeatRadiusPx {
				break
			}
			end++
		}
		if end-start >= repeatMinGestures {
			run := candidates[start:end]
			kind := interactionPatternRepeatedGesture
			for _, g := range run {
				if g.Provenance != protocol.InteractionProvenanceRecorded {
					kind = interactionPatternScriptedRepetition
				}
			}
			patterns = append(patterns, interactionPattern(kind, run, fmt.Sprintf("%d gestures within %d ms and %.0f CSS px", len(run), repeatWindow.Milliseconds(), repeatRadiusPx)))
			start = end
			continue
		}
		start++
	}
	return patterns
}

func interactionPattern(kind string, members []protocol.BrowserInteraction, detail string) BrowserInteractionPattern {
	first, last := members[0], members[len(members)-1]
	provenance := first.Provenance
	ids := make([]string, 0, min(len(members), maxInteractionPatternMembers))
	for _, m := range members {
		if m.Provenance != provenance {
			provenance = "mixed"
		}
		if len(ids) < maxInteractionPatternMembers {
			ids = append(ids, m.InteractionID)
		}
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.Join(ids, "\x00")))
	return BrowserInteractionPattern{ID: hex.EncodeToString(sum[:8]), Kind: kind, Provenance: provenance, Route: first.Route, LayoutRevision: first.LayoutRevision, ActionID: first.ActionID, ActionName: first.ActionName,
		Count: len(members), Start: first.CapturedAt, End: last.CapturedAt, Detail: detail, Interactions: ids}
}

// handleGetRunArchiveBrowserInteractions returns a run's interaction
// patterns and heatmaps from its archived interaction evidence.
func (s *Server) handleGetRunArchiveBrowserInteractions(w http.ResponseWriter, r *http.Request) {
	analysis, err := s.runArchiveBrowserInteractions(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, errInteractionRunNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errInteractionArchiveNotFinalized):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errInteractionEvidenceUnreadable):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeJSON(w, http.StatusOK, analysis)
	}
}

var (
	errInteractionRunNotFound         = errors.New("test not found")
	errInteractionArchiveNotFinalized = errors.New("the run has no finalized archive")
	errInteractionEvidenceUnreadable  = errors.New("the run's interaction evidence could not be read")
)

func (s *Server) runArchiveBrowserInteractions(ctx context.Context, id string) (*BrowserInteractionAnalysis, error) {
	test, ok := s.archiveDomainService().GetTestSummary(id)
	if !ok || test == nil {
		return nil, errInteractionRunNotFound
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, test.ID)
	if !ok || record == nil {
		return nil, errInteractionArchiveNotFinalized
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, errors.New("the run archive is unavailable")
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, errors.New("the run archive manifest is unavailable")
	}
	// Each source generation's engine and profile come from its document
	// evidence; a generation without documents maps as unknown.
	cohorts := map[string]browserGenerationCohort{}
	if accumulator, _, err := readArchiveBrowserDocumentMeasurements(ctx, record, manifest); err == nil && accumulator != nil {
		for _, measurement := range accumulator.measurements {
			o := measurement.observation
			if _, ok := cohorts[o.SourceGeneration]; !ok {
				cohorts[o.SourceGeneration] = browserGenerationCohort{engine: o.Engine, profile: o.DeviceProfile}
			}
		}
	}
	analyzer := newInteractionAnalyzer(test.ID, cohorts)
	if manifest.BrowserInteractionsFile == "" {
		analysis := analyzer.finish()
		analysis.Status = "not_captured"
		return &analysis, nil
	}
	if _, err := forEachBrowserInteractionRecord(filepath.Join(record.ArchivePath, manifest.BrowserInteractionsFile), func(evidence *protocol.BrowserInteractionEvidence) error {
		analyzer.add(evidence)
		return ctx.Err()
	}); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", errInteractionEvidenceUnreadable, err)
	}
	analysis := analyzer.finish()
	return &analysis, nil
}
