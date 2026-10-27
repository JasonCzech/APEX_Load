package api

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Browser interaction patterns and heatmaps (E4, BI-3), read from a run's
// archived interaction evidence (E3). Patterns are measured, with their
// windows and thresholds: repeated gestures, retries, unexpected
// navigations and dead interactions. A scripted or unknown gesture never
// yields a human-frustration label: a script repeating a tap is
// scripted_repetition. Heatmaps count where pointer gestures were dispatched
// on an abstract grid of at most 64×64 cells, one map per sanitized route,
// layout revision, viewport and orientation, recorded scroll, engine and device profile;
// incompatible layouts never merge. Nothing here injects interactions or
// probe traffic: it reads what the run recorded.

const (
	browserInteractionAnalysisVersion4 = "browser_interaction_patterns.v4"
	browserInteractionAnalysisVersion3 = "browser_interaction_patterns.v3"
	browserInteractionAnalysisVersion  = "browser_interaction_patterns.v2"
	// A repeated gesture is at least repeatMinGestures clicks, taps or checks
	// within repeatWindow on one document, route and layout: within
	// repeatRadiusPx CSS pixels of the first when points were dispatched, else
	// with one action name, gesture kind and target (see repeatedGestures).
	repeatMinGestures = 3
	repeatWindow      = 2 * time.Second
	repeatRadiusPx    = 32.0
	// heatmapGrid is a map's cells per side.
	heatmapGrid = 64
)

// Output and memory bounds. Variables so unit tests can reach each bound.
var (
	maxInteractionPatterns       = 200
	maxInteractionPatternMembers = 16
	maxInteractionHeatmaps       = 64
	maxHeatmapTargets            = 64
	// Bound aggregation as well as output: at most 64 maps × 1024 target keys.
	maxRetainedHeatmapTargets = 1024
	maxRetainedInteractions   = 100000
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
	// Status is ready; not_captured when the run recorded no interaction
	// evidence; or unavailable when it captured some but none reached the
	// archive (a lost transfer, a refused or torn sidecar).
	Status        string                       `json:"status"`
	Provisional   bool                         `json:"provisional,omitempty"`
	LiveTruncated bool                         `json:"live_truncated,omitempty"`
	Thresholds    BrowserInteractionThresholds `json:"thresholds"`
	Coverage      BrowserInteractionCoverage   `json:"coverage"`
	Patterns      []BrowserInteractionPattern  `json:"patterns"`
	Heatmaps      []BrowserInteractionHeatmap  `json:"heatmaps"`
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
	Interactions         int64            `json:"interactions"`
	StreamedInteractions int64            `json:"streamed_interactions"`
	PatternRetained      int64            `json:"pattern_retained"`
	PatternTruncated     int64            `json:"pattern_truncated,omitempty"`
	PatternEligible      int64            `json:"pattern_eligible,omitempty"`
	PatternExcluded      map[string]int64 `json:"pattern_excluded,omitempty"`
	Records              int64            `json:"records"`
	// Dropped counts interactions left out by capture bounds, refused by the
	// archive's budget or a write, or lost with a torn sidecar. A failed
	// transfer, a refused receipt or an invalid artifact loses an unknown
	// number: it only sets Incomplete, which says some source's evidence is
	// missing.
	Dropped            int64            `json:"dropped"`
	Incomplete         bool             `json:"incomplete"`
	ByProvenance       map[string]int64 `json:"by_provenance"`
	Mapped             int64            `json:"mapped"`
	MappedViaTransform int64            `json:"mapped_via_transform,omitempty"`
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
	Provenance     string                        `json:"provenance"`
	Route          string                        `json:"route,omitempty"`
	LayoutRevision string                        `json:"layout_revision,omitempty"`
	AuthoredStep   *protocol.BrowserAuthoredStep `json:"authored_step,omitempty"`
	ActionID       string                        `json:"action_id,omitempty"`
	ActionName     string                        `json:"action_name,omitempty"`
	Count          int                           `json:"count"`
	Start          time.Time                     `json:"start"`
	End            time.Time                     `json:"end"`
	// Declared and DeadlineMs are a dead interaction's authored outcome.
	OutcomeKind      string   `json:"outcome_kind,omitempty"`
	OutcomeIndex     int      `json:"outcome_index,omitempty"`
	OutcomeReason    string   `json:"outcome_reason,omitempty"`
	Declared         string   `json:"declared,omitempty"`
	DeadlineMs       int      `json:"deadline_ms,omitempty"`
	Detail           string   `json:"detail"`
	Interactions     []string `json:"interactions"`
	MembersTruncated bool     `json:"members_truncated,omitempty"`
}

// BrowserInteractionHeatmap is one compatible layout's map.
type BrowserInteractionHeatmap struct {
	Observation    string `json:"observation,omitempty"`
	Key            string `json:"key"`
	Route          string `json:"route"`
	LayoutRevision string `json:"layout_revision"`
	ViewportWidth  int    `json:"viewport_width"`
	ViewportHeight int    `json:"viewport_height"`
	Orientation    string `json:"orientation"`
	Engine         string `json:"engine"`
	DeviceProfile  string `json:"device_profile"`
	// DeviceScaleFactor is the gestures' device pixel ratio.
	DeviceScaleFactor float64 `json:"device_scale_factor,omitempty"`
	// CoordinateSpace is viewport_css_px: a cell is a position in the
	// viewport. Scroll is the exact recorded offset at Observation: native
	// dispatch for v2 evidence, or post-action capture for historical v1.
	CoordinateSpace string                    `json:"coordinate_space"`
	Scroll          *BrowserInteractionScroll `json:"scroll,omitempty"`
	Columns         int                       `json:"columns"`
	Rows            int                       `json:"rows"`
	// Cells are the occupied cells as [row, column, count].
	Cells           [][3]int                        `json:"cells"`
	Points          int64                           `json:"points"`
	Targets         []BrowserInteractionTargetCount `json:"targets,omitempty"`
	ByKind          map[string]int64                `json:"by_kind"`
	Sources         map[string]int64                `json:"by_provenance"`
	Notes           []string                        `json:"notes,omitempty"`
	cells           map[[2]int]int                  `json:"-"`
	targets         map[interactionTargetKey]int64  `json:"-"`
	targetsExcluded int64
}

// BrowserInteractionScroll is the range of scroll offsets, in CSS pixels.
type BrowserInteractionScroll struct {
	MinX float64 `json:"min_x"`
	MaxX float64 `json:"max_x"`
	MinY float64 `json:"min_y"`
	MaxY float64 `json:"max_y"`
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

// maxInteractionGenerationCohorts bounds the source generations (one per
// browser context) whose engine and profile the analysis keeps. A variable so
// tests can reach the bound.
var maxInteractionGenerationCohorts = 1 << 16

// browserGenerationCohorts maps source generations to the engine and device
// profile their document evidence names. It reads every record, not the
// bounded measurement population documents are summarized from, so a long
// run's later contexts keep theirs; past the bound a new generation is left
// out and the analysis says so.
type browserGenerationCohorts struct {
	byGeneration   map[string]browserGenerationCohort
	bound          int
	truncated      bool
	joins          map[string]bool
	joinBound      int
	joinsTruncated bool
}

func (c *browserGenerationCohorts) add(evidence *protocol.BrowserDocumentEvidence) {
	for _, o := range evidence.Observations {
		cohort := browserGenerationCohort{engine: o.Engine, profile: o.DeviceProfile}
		if previous, ok := c.byGeneration[o.SourceGeneration]; ok {
			// A context cannot change engine/profile. Retain an unknown
			// tombstone on conflict so later records cannot restore it.
			if previous != cohort {
				c.byGeneration[o.SourceGeneration] = browserGenerationCohort{}
			} else {
				c.addDocumentJoin(o)
			}
			continue
		}
		if len(c.byGeneration) >= c.bound {
			c.truncated = true
			continue
		}
		c.byGeneration[o.SourceGeneration] = cohort
		c.addDocumentJoin(o)
	}
}

func readBrowserGenerationCohorts(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, cohorts *browserGenerationCohorts) error {
	if err := validateBrowserDocumentManifest(manifest, true); err != nil {
		return err
	}
	if manifest.BrowserDocumentsFile == "" {
		return nil
	}
	return forEachBrowserDocumentArtifact(ctx, record.ArchivePath, manifest.BrowserDocumentsFile, manifest.BrowserDocumentsSHA256, manifest.BrowserDocumentRecordCount, func(evidence *protocol.BrowserDocumentEvidence) error {
		cohorts.add(evidence)
		return ctx.Err()
	})
}

const unknownCohortNoDocuments = "the run kept no consistent engine/profile document evidence for these gestures"

// interactionAnalyzer accumulates interactions in archive order.
type interactionAnalyzer struct {
	analysis BrowserInteractionAnalysis
	cohorts  map[string]browserGenerationCohort
	maps     map[string]*BrowserInteractionHeatmap
	retained []protocol.BrowserInteraction
	// unknownCohort counts excluded gestures whose engine/profile cohort is
	// unknown; unknownReason says why.
	hasV2              bool
	hasV4              bool
	documentJoins      map[string]bool
	patternPopulations map[string]int64
	unknownCohort      int64
	unknownReason      string
}

func newInteractionAnalyzer(runID string, cohorts map[string]browserGenerationCohort) *interactionAnalyzer {
	return &interactionAnalyzer{patternPopulations: map[string]int64{}, cohorts: cohorts, unknownReason: unknownCohortNoDocuments, maps: map[string]*BrowserInteractionHeatmap{}, analysis: BrowserInteractionAnalysis{
		RunID: runID, Version: browserInteractionAnalysisVersion, Status: "ready",
		Thresholds: BrowserInteractionThresholds{RepeatMinGestures: repeatMinGestures, RepeatWindowMs: repeatWindow.Milliseconds(), RepeatRadiusPx: repeatRadiusPx, HeatmapGrid: heatmapGrid},
		Coverage:   BrowserInteractionCoverage{ByProvenance: map[string]int64{}, Excluded: map[string]int64{}},
		Patterns:   []BrowserInteractionPattern{}, Heatmaps: []BrowserInteractionHeatmap{},
		Notes: []string{
			"Patterns describe the run's recorded gestures. Scripted or unknown gestures never carry a human-frustration label; a scripted repeat is scripted_repetition.",
			"A dead interaction needs an authored expected outcome whose deadline was missed; an unchanged page alone is never one.",
			"Heatmaps show where gestures were dispatched on an abstract grid of the viewport, not production user behavior. Screenshot overlays are not produced.",
			"Maps separate exact recorded scroll offsets, layouts, viewports, engines and device profiles. In historical v1 evidence, layout, viewport and scroll are read after the gesture and its wait; they do not prove dispatch-time geometry. Child frames without an observed transform and missing or conflicting cohorts are excluded.",
			"In historical v1 evidence, only clicks, taps and checks repeat. Those without dispatched points (touch gestures are dispatched synthetically) repeat when they share an action name, gesture kind and recorded target; those with points repeat within the radius. The repeat window is measured on capture time, after each gesture's wait. Steps are told apart by action name and gesture kind, so distinct steps that share a name are one step here.",
		},
	}}
}

func (a *interactionAnalyzer) add(evidence *protocol.BrowserInteractionEvidence) {
	a.analysis.Coverage.Records++
	a.hasV2 = a.hasV2 || protocol.BrowserInteractionHasDispatch(evidence.Version)
	a.hasV4 = a.hasV4 || evidence.Version >= protocol.BrowserInteractionVersion4
	a.analysis.Coverage.Dropped += evidence.Dropped
	a.analysis.Coverage.Incomplete = a.analysis.Coverage.Incomplete || evidence.Incomplete
	for _, interaction := range evidence.Interactions {
		a.analysis.Coverage.Interactions++
		a.analysis.Coverage.StreamedInteractions++
		a.hasV2 = a.hasV2 || protocol.BrowserInteractionHasDispatch(interaction.Version)
		a.hasV4 = a.hasV4 || interaction.Version >= protocol.BrowserInteractionVersion4
		a.patternPopulations[a.patternQualificationReason(interaction)]++
		a.analysis.Coverage.ByProvenance[interaction.Provenance]++
		a.mapInteraction(interaction)
		if len(a.retained) < maxRetainedInteractions {
			a.retained = append(a.retained, interaction)
			a.analysis.Coverage.PatternRetained = int64(len(a.retained))
		} else {
			a.analysis.Coverage.PatternTruncated++
			if len(a.analysis.Limitations) == 0 || a.analysis.Limitations[len(a.analysis.Limitations)-1] != "interactions_truncated" {
				a.analysis.Limitations = append(a.analysis.Limitations, "interactions_truncated")
			}
		}
	}
}

// mapInteraction adds one interaction to its layout's heatmap: a pointer
// gesture at its first dispatched point, a keyboard gesture to the target
// table.
func (a *interactionAnalyzer) mapInteraction(i protocol.BrowserInteraction) {
	exclude := func(reason string) { a.analysis.Coverage.Excluded[reason]++ }
	if i.Version >= protocol.BrowserInteractionVersion4 {
		if i.UnavailableReason != "" {
			exclude(i.UnavailableReason)
			return
		}
		if i.GeometryProvenance == protocol.InteractionGeometryProvenancePostAction {
			exclude("post_action_geometry")
			return
		}
	}
	if protocol.BrowserInteractionHasDispatch(i.Version) && i.Dispatch == nil {
		exclude("dispatch_unavailable")
		return
	}
	if i.UnavailableReason != "" {
		exclude(i.UnavailableReason)
		return
	}
	mappedViaTransform := false
	if i.FrameScope != "main_frame" {
		var excludeReason string
		i, excludeReason = transformChildFrameInteraction(i)
		if excludeReason != "" {
			exclude(excludeReason)
			return
		}
		mappedViaTransform = true
	}
	if i.LayoutRevision == "" {
		exclude("layout_unknown")
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
	positioned := pointer && len(i.Points) > 0
	if positioned {
		if p := i.Points[0]; p.X < 0 || p.Y < 0 || p.X >= float64(i.ViewportWidth) || p.Y >= float64(i.ViewportHeight) {
			exclude("outside_viewport")
			return
		}
	}
	cohort, ok := a.cohorts[i.SourceGeneration]
	// An empty profile on a known context means the default, unprofiled
	// browser. An absent or conflicting context must not join that cohort.
	if !ok || cohort.engine == "" || cohort.engine == "unknown" || cohort.profile == "unknown" {
		exclude("engine_profile_unknown")
		a.unknownCohort++
		return
	}
	orientation := "landscape"
	if i.ViewportHeight > i.ViewportWidth {
		orientation = "portrait"
	}
	key := strings.Join([]string{i.Route, i.LayoutRevision, fmt.Sprintf("%dx%d@%g", i.ViewportWidth, i.ViewportHeight, i.DeviceScaleFactor), orientation, cohort.engine, cohort.profile, fmt.Sprintf("%g,%g", canonicalScroll(i.ScrollX), canonicalScroll(i.ScrollY))}, "\x00")
	observation := "post_action"
	if protocol.BrowserInteractionHasDispatch(i.Version) {
		observation = "native_dispatch"
		key = "native_dispatch\x00" + key
	}
	m := a.maps[key]
	if m == nil {
		if len(a.maps) >= maxInteractionHeatmaps {
			exclude("heatmaps_truncated")
			return
		}
		sum := sha256.Sum256([]byte(key))
		m = &BrowserInteractionHeatmap{Observation: observation, Key: hex.EncodeToString(sum[:8]), Route: i.Route, LayoutRevision: i.LayoutRevision, ViewportWidth: i.ViewportWidth, ViewportHeight: i.ViewportHeight, Orientation: orientation,
			Engine: cohort.engine, DeviceProfile: cohort.profile, DeviceScaleFactor: i.DeviceScaleFactor, CoordinateSpace: "viewport_css_px", Columns: heatmapGrid, Rows: heatmapGrid, Cells: [][3]int{}, ByKind: map[string]int64{}, Sources: map[string]int64{},
			cells: map[[2]int]int{}, targets: map[interactionTargetKey]int64{}}
		a.maps[key] = m
	}
	if m.Scroll == nil {
		m.Scroll = &BrowserInteractionScroll{MinX: i.ScrollX, MaxX: i.ScrollX, MinY: i.ScrollY, MaxY: i.ScrollY}
	}
	m.Scroll.MinX, m.Scroll.MaxX = min(m.Scroll.MinX, i.ScrollX), max(m.Scroll.MaxX, i.ScrollX)
	m.Scroll.MinY, m.Scroll.MaxY = min(m.Scroll.MinY, i.ScrollY), max(m.Scroll.MaxY, i.ScrollY)
	if positioned {
		p := i.Points[0]
		col := int(p.X * heatmapGrid / float64(i.ViewportWidth))
		row := int(p.Y * heatmapGrid / float64(i.ViewportHeight))
		m.cells[[2]int{row, col}]++
		m.Points++
	} else {
		label := ""
		if i.Target != nil {
			label = i.Target.LabelSHA256
			key := interactionTargetKey{role: i.Target.Role, label: label}
			if _, retained := m.targets[key]; !retained && len(m.targets) >= maxRetainedHeatmapTargets {
				m.targetsExcluded++
				exclude("keyboard_targets_truncated")
				return
			}
			m.targets[key]++
		}
	}
	m.ByKind[i.Kind]++
	m.Sources[i.Provenance]++
	a.analysis.Coverage.Mapped++
	if mappedViaTransform {
		a.analysis.Coverage.MappedViaTransform++
	}
}

// canonicalScroll treats signed zero as the same recorded CSS offset. Other
// offsets remain exact: quantizing could merge different page populations.
func canonicalScroll(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
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
		if m.targetsExcluded > 0 {
			m.Notes = append(m.Notes, fmt.Sprintf("%d keyboard gestures excluded after the first %d distinct targets; counts describe retained targets only", m.targetsExcluded, maxRetainedHeatmapTargets))
		}
		if len(m.Targets) > maxHeatmapTargets {
			m.Notes = append(m.Notes, fmt.Sprintf("kept the %d most used of %d retained keyboard targets", maxHeatmapTargets, len(m.Targets)))
			m.Targets = m.Targets[:maxHeatmapTargets]
		}
		a.analysis.Heatmaps = append(a.analysis.Heatmaps, *m)
	}
	slices.SortFunc(a.analysis.Heatmaps, func(x, y BrowserInteractionHeatmap) int {
		return cmp.Or(cmp.Compare(y.Points+sumCounts(y.Targets), x.Points+sumCounts(x.Targets)), cmp.Compare(x.Key, y.Key))
	})
	if a.hasV2 {
		if a.hasV4 {
			a.analysis.Version = browserInteractionAnalysisVersion4
			a.analysis.Notes = []string{
				"V4 patterns require an engine-authored step, logical invocation and joined native dispatch clock. Legacy or unavailable rows remain retained and are counted as pattern exclusions.",
				"Native dispatch and historical post-action maps are separate populations. Same-origin child frames with an invertible 2D transform map into the main-frame viewport; unmapped child frames, cross-origin frames, non-2D transforms, unknown cohorts and unavailable dispatch geometry remain excluded.",
				"Engine retry attempts share an invocation and never count as repetition. Separate invocations repeat only for the same authored step within the native dispatch window and compatible geometry.",
				"Every missed declared page-response outcome deadline produces its own dead interaction or unexpected navigation pattern; unchanged DOM, cancellation or navigation alone cannot. Scripted gestures never establish human frustration.",
			}
		} else {
			a.analysis.Version = browserInteractionAnalysisVersion3
			a.analysis.Notes = []string{
				"V3 patterns require an engine-authored step, logical invocation and joined native dispatch clock. Legacy or unavailable rows remain retained and are counted as pattern exclusions.",
				"Native dispatch and historical post-action maps are separate populations. Unmapped child frames, unknown cohorts and unavailable dispatch geometry remain excluded.",
				"Engine retry attempts share an invocation and never count as repetition. Separate invocations repeat only for the same authored step within the native dispatch window and compatible geometry.",
				"A missed declared page-response deadline can be a dead interaction; unchanged DOM, cancellation or navigation alone cannot. Scripted gestures never establish human frustration.",
			}
		}
		a.analysis.Coverage.PatternExcluded = map[string]int64{}
		for reason, count := range a.patternPopulations {
			if reason == "" {
				a.analysis.Coverage.PatternEligible += count
			} else {
				a.analysis.Coverage.PatternExcluded[reason] = count
			}
		}
		qualified := make([]protocol.BrowserInteraction, 0, len(a.retained))
		for _, row := range a.retained {
			if a.patternQualificationReason(row) == "" {
				qualified = append(qualified, row)
			}
		}
		a.analysis.Patterns = findInteractionPatterns(qualified)
	} else {
		a.analysis.Patterns = findInteractionPatterns(a.retained)
	}
	if a.unknownCohort > 0 {
		a.analysis.Limitations = append(a.analysis.Limitations, fmt.Sprintf("engine_profile_unknown: %d gestures excluded from maps without a consistent engine/profile cohort (%s)", a.unknownCohort, a.unknownReason))
	}
	a.analysis.Coverage.StreamedInteractions = a.analysis.Coverage.Interactions
	a.analysis.Coverage.PatternRetained = int64(len(a.retained))
	if len(a.analysis.Patterns) > maxInteractionPatterns {
		totalPatterns := len(a.analysis.Patterns)
		slices.SortStableFunc(a.analysis.Patterns, func(x, y BrowserInteractionPattern) int {
			return cmp.Or(cmp.Compare(patternTruncationPriority(x.Kind), patternTruncationPriority(y.Kind)), x.Start.Compare(y.Start), cmp.Compare(x.ID, y.ID))
		})
		a.analysis.Patterns = a.analysis.Patterns[:maxInteractionPatterns]
		slices.SortStableFunc(a.analysis.Patterns, func(x, y BrowserInteractionPattern) int {
			return cmp.Or(x.Start.Compare(y.Start), cmp.Compare(x.ID, y.ID))
		})
		a.analysis.Limitations = append(a.analysis.Limitations, fmt.Sprintf("patterns_truncated: kept %d of %d", maxInteractionPatterns, totalPatterns))
	}
	if len(a.analysis.Coverage.Excluded) == 0 {
		a.analysis.Coverage.Excluded = nil
	}
	return a.analysis
}

func patternTruncationPriority(kind string) int {
	switch kind {
	case interactionPatternDeadInteraction, interactionPatternUnexpectedNavigation, interactionPatternRetry, interactionPatternRepeatedGesture:
		return 0
	default:
		return 1
	}
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
		if protocol.BrowserInteractionHasDispatch(i.Version) && i.Dispatch != nil {
			key += "\x00" + i.Dispatch.LifecycleID
		}
		if _, ok := byDocument[key]; !ok {
			documents = append(documents, key)
		}
		byDocument[key] = append(byDocument[key], i)
	}
	patterns := []BrowserInteractionPattern{}
	for _, key := range documents {
		gestures := byDocument[key]
		slices.SortStableFunc(gestures, func(x, y protocol.BrowserInteraction) int {
			if c := interactionClock(x).Compare(interactionClock(y)); c != 0 {
				return c
			}
			if sameInvocation(x, y) {
				return cmp.Compare(x.Execution.Attempt, y.Execution.Attempt)
			}
			return 0
		})
		retried := map[string]bool{}
		// Attempts >1 are intended retries even if the prior attempt was lost.
		// Their first attempt must also stay out of repetition.
		retryInvocations := map[string]bool{}
		for _, row := range gestures {
			if protocol.BrowserInteractionHasDispatch(row.Version) && row.Execution.Valid() && row.Execution.Attempt > 1 {
				retryInvocations[interactionInvocationKey(row)] = true
			}
		}
		for _, row := range gestures {
			if retryInvocations[interactionInvocationKey(row)] {
				retried[row.InteractionID] = true
			}
		}
		retryReported := map[string]bool{}
		for index, g := range gestures {
			if g.Version >= protocol.BrowserInteractionVersion4 {
				navigated := g.UnavailableReason == protocol.InteractionUnavailableDocumentChanged
				for _, outcome := range g.DeclaredOutcomes() {
					if outcome.Kind == protocol.InteractionOutcomeKindNavigation && (outcome.Observed == protocol.InteractionOutcomeMet || outcome.Reason == "unexpected_navigation") {
						navigated = true
						break
					}
				}
				for outcomeIndex, outcome := range g.DeclaredOutcomes() {
					if outcome.Observed != protocol.InteractionOutcomeMissed {
						continue
					}
					switch {
					case (navigated && (inPageOutcome(outcome.Declared) || outcome.Kind == protocol.InteractionOutcomeKindAssertion)) || (outcome.Kind == protocol.InteractionOutcomeKindNavigation && outcome.Reason == "unexpected_navigation"):
						detail := fmt.Sprintf("the gesture navigated away and its declared %s outcome was not observed", outcome.Declared)
						if outcome.DeadlineMS > 0 {
							detail = fmt.Sprintf("the gesture navigated away and its declared %s outcome was not observed within %d ms", outcome.Declared, outcome.DeadlineMS)
						}
						patterns = append(patterns, interactionOutcomePattern(interactionPatternUnexpectedNavigation, g, outcomeIndex, outcome, detail))
					case !navigated && outcomeIsPageResponse(outcome):
						detail := fmt.Sprintf("the declared %s outcome was not observed", outcome.Declared)
						if outcome.DeadlineMS > 0 {
							detail = fmt.Sprintf("the declared %s outcome was not observed within %d ms", outcome.Declared, outcome.DeadlineMS)
						}
						patterns = append(patterns, interactionOutcomePattern(interactionPatternDeadInteraction, g, outcomeIndex, outcome, detail))
					}
				}
			} else {
				primary := g.PrimaryOutcome()
				switch {
				case g.UnavailableReason == protocol.InteractionUnavailableDocumentChanged:
					// A gesture that navigated away is never a dead interaction.
					// Navigating is normal (a recorded step waits for the next
					// page's element); it is unexpected only when the element the
					// step waited for never appeared.
					if inPageOutcome(primary.Declared) && primary.Observed == protocol.InteractionOutcomeMissed {
						patterns = append(patterns, interactionPattern(interactionPatternUnexpectedNavigation, []protocol.BrowserInteraction{g},
							fmt.Sprintf("the gesture navigated away and its declared %s outcome was not observed within %d ms", primary.Declared, primary.DeadlineMS)))
					}
				default:
					for _, outcome := range g.DeclaredOutcomes() {
						if outcome.Observed == protocol.InteractionOutcomeMissed && pageResponseOutcome(outcome.Declared) {
							// The authored outcome's deadline passed.
							patterns = append(patterns, interactionPattern(interactionPatternDeadInteraction, []protocol.BrowserInteraction{g},
								fmt.Sprintf("the declared %s outcome was not observed within %d ms", outcome.Declared, outcome.DeadlineMS)))
							patterns[len(patterns)-1].Declared, patterns[len(patterns)-1].DeadlineMs = outcome.Declared, outcome.DeadlineMS
							break
						}
					}
				}
			}
			// A failed gesture repeated at once by the same step is a retry
			// (each engine retry is a new execution with its own action ID,
			// so a step is its action name and gesture kind); the attempts
			// never count as repeated gestures.
			if !g.Success && !retryReported[g.InteractionID] && (protocol.BrowserInteractionHasDispatch(g.Version) || !retried[g.InteractionID]) {
				attempts := []protocol.BrowserInteraction{g}
				for _, next := range gestures[index+1:] {
					if protocol.BrowserInteractionHasDispatch(g.Version) {
						if !sameInvocation(g, next) {
							continue
						}
						if next.Execution.Attempt <= attempts[len(attempts)-1].Execution.Attempt {
							continue
						}
					} else if !sameInteractionStep(g, next) {
						break
					}
					attempts = append(attempts, next)
					if next.Success {
						break
					}
				}
				if len(attempts) > 1 {
					for _, attempt := range attempts {
						retried[attempt.InteractionID] = true
						retryReported[attempt.InteractionID] = true
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

// sameInteractionStep reports whether next is another attempt of g's step.
func sameInteractionStep(g, next protocol.BrowserInteraction) bool {
	if protocol.BrowserInteractionHasDispatch(g.Version) || protocol.BrowserInteractionHasDispatch(next.Version) {
		return g.Version == next.Version && g.AuthoredStep.Valid() && next.AuthoredStep.Valid() && g.AuthoredStep.StepID == next.AuthoredStep.StepID && g.Kind == next.Kind
	}
	if g.ActionName == "" {
		return g.ActionID != "" && next.ActionID == g.ActionID
	}
	return next.ActionName == g.ActionName && next.Kind == g.Kind
}

// pageResponseOutcome reports whether a declared outcome asserts that the
// page responded (an element appeared, a load completed), so missing it is a
// dead interaction. A DOM that never settled, a network that never idled or
// a pause says nothing about whether the page responded.
func pageResponseOutcome(declared string) bool {
	switch declared {
	case "element_visible", "visible", "load", "domcontentloaded", "dialog", "download":
		return true
	}
	return false
}

func outcomeIsPageResponse(outcome protocol.BrowserInteractionOutcome) bool {
	if pageResponseOutcome(outcome.Declared) {
		return true
	}
	switch outcome.Kind {
	case protocol.InteractionOutcomeKindDialog, protocol.InteractionOutcomeKindDownload, protocol.InteractionOutcomeKindAssertion, protocol.InteractionOutcomeKindNavigation:
		return true
	}
	return false
}

// inPageOutcome reports whether a declared outcome waits for an element. A
// stable DOM or a pause says nothing about which page the gesture should
// leave the browser on.
func inPageOutcome(declared string) bool {
	switch declared {
	case "element_visible", "visible":
		return true
	}
	return false
}

// repeatGestureKind reports whether a gesture kind counts toward repeated
// gestures: a click, tap or check (a double click is one declared gesture),
// not a hover, drag, swipe or long press.
func repeatGestureKind(kind string) bool {
	switch kind {
	case "click", "dblclick", "tap", "check":
		return true
	}
	return false
}

// repeatedGestures finds runs of at least repeatMinGestures gestures within
// repeatWindow on one route and layout: gestures with dispatched points
// within repeatRadiusPx of the run's first; gestures without points (touch
// gestures are dispatched synthetically) with one action name and kind, and
// target when one is recorded.
// Retries are excluded.
func repeatedGestures(gestures []protocol.BrowserInteraction, retried map[string]bool) []BrowserInteractionPattern {
	var positioned, targeted []protocol.BrowserInteraction
	for _, g := range gestures {
		if !repeatGestureKind(g.Kind) || retried[g.InteractionID] || (protocol.BrowserInteractionHasDispatch(g.Version) && g.Dispatch == nil) {
			continue
		}
		if g.FrameScope != "main_frame" {
			var excludeReason string
			g, excludeReason = transformChildFrameInteraction(g)
			if excludeReason != "" {
				continue
			}
		}
		switch {
		case len(g.Points) > 0 && g.UnavailableReason == "":
			positioned = append(positioned, g)
		case len(g.Points) == 0 && (g.UnavailableReason == "" || g.UnavailableReason == protocol.InteractionUnavailableSyntheticDispatch) && (g.ActionName != "" || g.Target != nil || g.AuthoredStep.Valid()):
			targeted = append(targeted, g)
		}
	}
	near := func(first, next protocol.BrowserInteraction) bool {
		return math.Hypot(next.Points[0].X-first.Points[0].X, next.Points[0].Y-first.Points[0].Y) <= repeatRadiusPx
	}
	patterns := repeatRuns(positioned, near, fmt.Sprintf("within %d ms and %.0f CSS px", repeatWindow.Milliseconds(), repeatRadiusPx))
	return append(patterns, repeatRuns(targeted, sameInteractionTarget, fmt.Sprintf("within %d ms with one action name, gesture kind and target (no dispatched points)", repeatWindow.Milliseconds()))...)
}

func transformChildFrameInteraction(i protocol.BrowserInteraction) (protocol.BrowserInteraction, string) {
	if i.Version < protocol.BrowserInteractionVersion4 {
		return i, protocol.InteractionUnavailableChildFrame
	}
	if len(i.FrameChain) == 0 {
		return i, "unmapped_frame"
	}
	if len(i.FrameChain) > protocol.MaxBrowserInteractionFrameDepth {
		return i, protocol.InteractionUnavailableFrameDepthExceeded
	}
	for _, hop := range i.FrameChain {
		if hop.CrossOrigin {
			return i, protocol.InteractionUnavailableCrossOriginAncestry
		}
		if hop.Non2DTransform || !hop.Invertible2D() {
			return i, protocol.InteractionUnavailableNonAxisAlignedOr3DTransform
		}
	}
	if protocol.BrowserInteractionPointerKind(i.Kind) && len(i.Points) > 0 {
		pt := i.Points[0]
		for _, hop := range i.FrameChain {
			var ok bool
			pt, ok = hop.ApplyToPoint(pt)
			if !ok {
				return i, protocol.InteractionUnavailableNonAxisAlignedOr3DTransform
			}
		}
		i.Points = append([]protocol.BrowserInteractionPoint(nil), i.Points...)
		i.Points[0] = pt
	}
	return i, ""
}

func sameInteractionFrameChain(a, b []protocol.BrowserInteractionFrameHop) bool {
	return slices.EqualFunc(a, b, func(x, y protocol.BrowserInteractionFrameHop) bool {
		return x.BoundingX == y.BoundingX &&
			x.BoundingY == y.BoundingY &&
			x.BoundingWidth == y.BoundingWidth &&
			x.BoundingHeight == y.BoundingHeight &&
			x.OffsetWidth == y.OffsetWidth &&
			x.OffsetHeight == y.OffsetHeight &&
			x.ClientLeft == y.ClientLeft &&
			x.ClientTop == y.ClientTop &&
			x.PaddingLeft == y.PaddingLeft &&
			x.PaddingTop == y.PaddingTop &&
			x.CrossOrigin == y.CrossOrigin &&
			x.Non2DTransform == y.Non2DTransform &&
			slices.Equal(x.Matrix, y.Matrix)
	})
}

func sameInteractionTarget(first, next protocol.BrowserInteraction) bool {
	if protocol.BrowserInteractionHasDispatch(first.Version) || protocol.BrowserInteractionHasDispatch(next.Version) {
		if !sameInteractionStep(first, next) || (next.Target == nil) != (first.Target == nil) {
			return false
		}
		return first.Target == nil || next.Target.Role == first.Target.Role && next.Target.LabelSHA256 == first.Target.LabelSHA256
	}
	if next.Kind != first.Kind || next.ActionName != first.ActionName || (next.Target == nil) != (first.Target == nil) {
		return false
	}
	return first.Target == nil || next.Target.Role == first.Target.Role && next.Target.LabelSHA256 == first.Target.LabelSHA256
}

func repeatRuns(candidates []protocol.BrowserInteraction, match func(first, next protocol.BrowserInteraction) bool, rule string) []BrowserInteractionPattern {
	var patterns []BrowserInteractionPattern
	for start := 0; start < len(candidates); {
		first := candidates[start]
		end := start + 1
		for end < len(candidates) {
			next := candidates[end]
			if next.Route != first.Route || next.LayoutRevision != first.LayoutRevision || interactionClock(next).Sub(interactionClock(first)) > repeatWindow || (protocol.BrowserInteractionHasDispatch(first.Version) && (!sameInteractionStep(first, next) || first.FrameScope != next.FrameScope || !sameInteractionFrameChain(first.FrameChain, next.FrameChain) || first.ScrollX != next.ScrollX || first.ScrollY != next.ScrollY || first.ViewportWidth != next.ViewportWidth || first.ViewportHeight != next.ViewportHeight || first.DeviceScaleFactor != next.DeviceScaleFactor)) || !match(first, next) {
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
			patterns = append(patterns, interactionPattern(kind, run, fmt.Sprintf("%d gestures %s", len(run), rule)))
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
		Count: len(members), AuthoredStep: first.AuthoredStep.Clone(), Start: interactionClock(first), End: interactionClock(last), Detail: detail, Interactions: ids, MembersTruncated: len(members) > maxInteractionPatternMembers}
}

func interactionOutcomePattern(kind string, g protocol.BrowserInteraction, outcomeIndex int, outcome protocol.BrowserInteractionOutcome, detail string) BrowserInteractionPattern {
	pattern := interactionPattern(kind, []protocol.BrowserInteraction{g}, detail)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", kind, g.InteractionID, outcome.Kind, outcomeIndex, outcome.Declared)))
	pattern.ID = hex.EncodeToString(sum[:8])
	pattern.OutcomeKind = outcome.Kind
	pattern.OutcomeIndex = outcomeIndex
	pattern.OutcomeReason = outcome.Reason
	pattern.Declared = outcome.Declared
	pattern.DeadlineMs = outcome.DeadlineMS
	return pattern
}

// handleGetRunArchiveBrowserInteractions returns a run's interaction
// patterns and heatmaps from its archived interaction evidence.
func (s *Server) handleGetRunArchiveBrowserInteractions(w http.ResponseWriter, r *http.Request) {
	// Materializing an object-backed archive can outlast the write timeout.
	extendArtifactWriteDeadline(w)
	format := queryFormat(r)
	if format != "" && format != "json" && format != "csv" {
		writeError(w, http.StatusBadRequest, "unsupported format")
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	analysis, err := s.runArchiveBrowserInteractions(ctx, id)
	if errors.Is(err, errInteractionArchiveNotFound) || errors.Is(err, errInteractionArchiveNotFinalized) {
		if provisional, ok := s.provisionalRunBrowserInteractions(ctx, id); ok {
			analysis = provisional
			err = nil
		}
	}
	switch {
	case err == nil:
		if wantsCSV(r) {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-archive-browser-interactions-"+sanitizeFilename(id)+".csv\"")
			if err := writeBrowserInteractionAnalysisCSV(analysis, w); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to write browser interactions csv: "+err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, analysis)
	case errors.Is(err, errInteractionRunNotFound), errors.Is(err, errInteractionArchiveNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errInteractionArchiveNotFinalized):
		writeError(w, http.StatusConflict, err.Error())
	case ctx.Err() != nil:
		// The request was cancelled or timed out while reading.
		writeError(w, http.StatusServiceUnavailable, ctx.Err().Error())
	default:
		// Unreadable evidence, or an archive that could not be materialized
		// or whose manifest could not be read, as the sibling archive routes
		// answer.
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeBrowserInteractionAnalysisCSV(analysis *BrowserInteractionAnalysis, w io.Writer) error {
	cw := csv.NewWriter(w)
	writeRow := func(section, key, field, value string) error {
		return cw.Write([]string{reportCSVSafeCell(section), reportCSVSafeCell(key), reportCSVSafeCell(field), reportCSVSafeCell(value)})
	}
	if err := cw.Write([]string{"section", "key", "field", "value"}); err != nil {
		return err
	}
	cov := analysis.Coverage
	for _, item := range [][2]string{
		{"run_id", analysis.RunID},
		{"version", analysis.Version},
		{"status", analysis.Status},
		{"interactions", strconv.FormatInt(cov.Interactions, 10)},
		{"streamed_interactions", strconv.FormatInt(cov.StreamedInteractions, 10)},
		{"pattern_retained", strconv.FormatInt(cov.PatternRetained, 10)},
		{"pattern_truncated", strconv.FormatInt(cov.PatternTruncated, 10)},
		{"pattern_eligible", strconv.FormatInt(cov.PatternEligible, 10)},
		{"records", strconv.FormatInt(cov.Records, 10)},
		{"dropped", strconv.FormatInt(cov.Dropped, 10)},
		{"incomplete", strconv.FormatBool(cov.Incomplete)},
		{"mapped", strconv.FormatInt(cov.Mapped, 10)},
		{"mapped_via_transform", strconv.FormatInt(cov.MappedViaTransform, 10)},
	} {
		if err := writeRow("coverage", "", item[0], item[1]); err != nil {
			return err
		}
	}
	writeMap := func(prefix string, m map[string]int64) error {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if err := writeRow("coverage", "", prefix+"."+k, strconv.FormatInt(m[k], 10)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeMap("by_provenance", cov.ByProvenance); err != nil {
		return err
	}
	if err := writeMap("pattern_excluded", cov.PatternExcluded); err != nil {
		return err
	}
	if err := writeMap("excluded", cov.Excluded); err != nil {
		return err
	}
	for _, p := range analysis.Patterns {
		stepID, stepPath, scriptSHA := "", "", ""
		if p.AuthoredStep.Valid() {
			stepID, stepPath, scriptSHA = p.AuthoredStep.StepID, p.AuthoredStep.StepPath, p.AuthoredStep.ScriptSHA256
		}
		for _, item := range [][2]string{
			{"kind", p.Kind},
			{"provenance", p.Provenance},
			{"route", p.Route},
			{"layout_revision", p.LayoutRevision},
			{"action_id", p.ActionID},
			{"action_name", p.ActionName},
			{"step_id", stepID},
			{"step_path", stepPath},
			{"script_sha256", scriptSHA},
			{"count", strconv.Itoa(p.Count)},
			{"members_truncated", strconv.FormatBool(p.MembersTruncated)},
			{"start", p.Start.UTC().Format(time.RFC3339Nano)},
			{"end", p.End.UTC().Format(time.RFC3339Nano)},
			{"outcome_kind", p.OutcomeKind},
			{"outcome_index", strconv.Itoa(p.OutcomeIndex)},
			{"outcome_reason", p.OutcomeReason},
			{"declared", p.Declared},
			{"deadline_ms", strconv.Itoa(p.DeadlineMs)},
			{"detail", p.Detail},
			{"interactions", strings.Join(p.Interactions, ";")},
		} {
			if err := writeRow("pattern", p.ID, item[0], item[1]); err != nil {
				return err
			}
		}
	}
	for _, m := range analysis.Heatmaps {
		kbd := sumCounts(m.Targets)
		for _, item := range [][2]string{
			{"observation", m.Observation},
			{"route", m.Route},
			{"layout_revision", m.LayoutRevision},
			{"viewport_width", strconv.Itoa(m.ViewportWidth)},
			{"viewport_height", strconv.Itoa(m.ViewportHeight)},
			{"orientation", m.Orientation},
			{"engine", m.Engine},
			{"device_profile", m.DeviceProfile},
			{"device_scale_factor", fmt.Sprintf("%g", m.DeviceScaleFactor)},
			{"coordinate_space", m.CoordinateSpace},
			{"points", strconv.FormatInt(m.Points, 10)},
			{"keyboard_gestures", strconv.FormatInt(kbd, 10)},
			{"total_gestures", strconv.FormatInt(m.Points+kbd, 10)},
			{"occupied_cells", strconv.Itoa(len(m.Cells))},
			{"targets", formatReportHeatmapTargets(m.Targets)},
		} {
			if err := writeRow("heatmap", m.Key, item[0], item[1]); err != nil {
				return err
			}
		}
		if m.Scroll != nil {
			for _, item := range [][2]string{
				{"scroll_min_x", fmt.Sprintf("%g", m.Scroll.MinX)},
				{"scroll_max_x", fmt.Sprintf("%g", m.Scroll.MaxX)},
				{"scroll_min_y", fmt.Sprintf("%g", m.Scroll.MinY)},
				{"scroll_max_y", fmt.Sprintf("%g", m.Scroll.MaxY)},
			} {
				if err := writeRow("heatmap", m.Key, item[0], item[1]); err != nil {
					return err
				}
			}
		}
		for idx, note := range m.Notes {
			if err := writeRow("heatmap_note", fmt.Sprintf("%s.%d", m.Key, idx), "note", note); err != nil {
				return err
			}
		}
	}
	for idx, lim := range analysis.Limitations {
		if err := writeRow("limitation", strconv.Itoa(idx), "limitation", lim); err != nil {
			return err
		}
	}
	for idx, note := range analysis.Notes {
		if err := writeRow("note", strconv.Itoa(idx), "note", note); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

var (
	errInteractionRunNotFound         = errors.New("test not found")
	errInteractionArchiveNotFound     = errors.New("archive not found")
	errInteractionArchiveNotFinalized = errors.New("the run archive is not finalized")
	errInteractionEvidenceUnreadable  = errors.New("the run's interaction evidence could not be read")
)

func (s *Server) runArchiveBrowserInteractions(ctx context.Context, id string) (*BrowserInteractionAnalysis, error) {
	test, ok := s.archiveDomainService().GetTestSummary(id)
	if !ok || test == nil {
		return nil, errInteractionRunNotFound
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, test.ID)
	if !ok || record == nil {
		return nil, errInteractionArchiveNotFound
	}
	if strings.TrimSpace(record.ManifestSHA256) == "" {
		return nil, errInteractionArchiveNotFinalized
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("the run archive could not be materialized")
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, errors.New("the run archive manifest is unavailable")
	}
	if manifest.BrowserInteractionsFile == "" {
		analysis := newInteractionAnalyzer(test.ID, nil).finish()
		analysis.Status = "not_captured"
		// Interactions were captured, but none reached the archive.
		if manifest.BrowserInteractionsDropped > 0 || manifest.BrowserInteractionsIncomplete {
			analysis.Status = "unavailable"
			analysis.Coverage.Dropped, analysis.Coverage.Incomplete = manifest.BrowserInteractionsDropped, true
		}
		return &analysis, nil
	}
	// Each source generation's engine and profile come from its document
	// evidence; a generation without consistent documents is excluded from maps.
	cohorts := browserGenerationCohorts{byGeneration: map[string]browserGenerationCohort{}, bound: maxInteractionGenerationCohorts}
	unknownReason, documentLimitation := unknownCohortNoDocuments, ""
	if err := readBrowserGenerationCohorts(ctx, record, manifest, &cohorts); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// An unverified partial read names nothing.
		cohorts.byGeneration = map[string]browserGenerationCohort{}
		cohorts.joins = nil
		unknownReason, documentLimitation = "the run's document evidence could not be read", "document_evidence_unreadable: engines and device profiles are unknown"
	} else if cohorts.truncated {
		// A context left out past the bound cannot be told from one that
		// kept no document evidence, so both are named.
		unknownReason = fmt.Sprintf("the run kept no consistent engine/profile document evidence for these gestures, or their browser context is past the first %d whose document evidence the analysis keeps", maxInteractionGenerationCohorts)
		documentLimitation = fmt.Sprintf("document_evidence_truncated: engines and device profiles are kept for the first %d browser contexts", maxInteractionGenerationCohorts)
	}
	analyzer := newInteractionAnalyzer(test.ID, cohorts.byGeneration)
	analyzer.unknownReason = unknownReason
	analyzer.documentJoins = cohorts.joins
	if cohorts.joinsTruncated {
		analyzer.analysis.Limitations = append(analyzer.analysis.Limitations, fmt.Sprintf("document_joins_truncated: retained at most %d source/document/lifecycle identities for qualified v3 patterns", maxInteractionDocumentJoins))
	}
	if documentLimitation != "" {
		analyzer.analysis.Limitations = append(analyzer.analysis.Limitations, documentLimitation)
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
	// The archive's own account includes drops and missing sources that no
	// record carries (a failed transfer, a refused sidecar).
	analysis.Coverage.Dropped = max(analysis.Coverage.Dropped, manifest.BrowserInteractionsDropped)
	analysis.Coverage.Incomplete = analysis.Coverage.Incomplete || manifest.BrowserInteractionsIncomplete
	return &analysis, nil
}
