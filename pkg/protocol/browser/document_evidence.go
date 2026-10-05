package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

const browserDocumentLimit = protocol.MaxBrowserDocumentObservations / 5
const browserFrameIdentityLimit = 256
const browserRequestIdentityLimit = 512

type browserDocumentState struct {
	frame                                          playwright.Frame
	page                                           playwright.Page
	token, lifecycleToken                          string
	documentID, lifecycleID, frameID, navigationID string
	start                                          time.Time
	timeOrigin                                     float64
	restored, ended                                bool
	cohort                                         *protocol.BrowserRuntimeCohort
	observations                                   map[string]protocol.BrowserDocumentObservation
}
type browserRequestIdentity struct {
	evidence protocol.BrowserResourceEvidence
	finished bool
}
type browserEvidenceState struct {
	lifecycleContext                                       context.Context
	generation                                             string
	sequence                                               uint64
	binding, finalizer                                     string
	currentAction                                          string
	frames                                                 map[playwright.Frame]string
	documents                                              map[string]*browserDocumentState
	requests                                               map[playwright.Request]*browserRequestIdentity
	retired                                                []playwright.Request
	cohort                                                 *protocol.BrowserRuntimeCohort
	shapedPages                                            map[playwright.Page]bool
	issued, inFlight, droppedRequests, droppedObservations int64
	incomplete, cancelled                                  bool
}
type browserDocumentPayload struct {
	Kind             string   `json:"kind"`
	DocumentToken    string   `json:"document_token"`
	LifecycleToken   string   `json:"lifecycle_token"`
	Restored         bool     `json:"restored"`
	TimeOrigin       float64  `json:"time_origin"`
	Offset           float64  `json:"offset"`
	Metric           string   `json:"metric"`
	State            string   `json:"state"`
	Boundary         string   `json:"boundary"`
	Reason           string   `json:"reason"`
	InteractionCount float64  `json:"interaction_count"`
	Value            *float64 `json:"value"`
}

func (s *browserEvidenceState) nextID(kind string) string {
	s.sequence++
	return fmt.Sprintf("%s-%s%x", s.generation, kind, s.sequence)
}
func browserDocumentToken(v string) bool { return protocol.BrowserEvidenceGeneration(v) }
func browserEvidenceTime(origin, offset float64) (time.Time, bool) {
	if math.IsNaN(origin) || math.IsInf(origin, 0) || origin <= 0 || origin > 253402300799999 || math.IsNaN(offset) || math.IsInf(offset, 0) || offset < 0 || offset > 86400000 {
		return time.Time{}, false
	}
	epochMS := origin + offset
	return time.Unix(int64(epochMS/1000), int64(math.Mod(epochMS, 1000)*1e6)).UTC(), true
}
func (b *Browser) resetBrowserEvidence(version string) error {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Errorf("browser evidence identity initialization failed")
	}
	generation := hex.EncodeToString(bytes)
	viewport := b.browserViewport()
	headless, mobile, touch, cache, content, newer := b.headless, b.mobile, b.touchMode, b.cacheEnabled, b.cacheContentURLs, b.checkNewerVersions
	// Successful context routing precedes this producer setup. Playwright's
	// routing disables HTTP cache even when an unrelated download/revalidation
	// policy requested caching: report the effective native setting.
	// https://playwright.dev/docs/api/class-browsercontext#browser-context-route
	cache = cache && !b.requestPolicyEnabled()
	cohort := &protocol.BrowserRuntimeCohort{Version: 1, BrowserVersion: version, Platform: runtime.GOOS, ViewportWidth: viewport.Width, ViewportHeight: viewport.Height, Headless: &headless, Mobile: &mobile, Touch: &touch, DeviceScaleFactor: b.deviceScaleFactor, CPUThrottleRate: b.cpuThrottleRate, NetworkLatencyMS: b.networkLatencyMS, NetworkBandwidthKbps: b.networkBandwidthKB, ShapingApplied: b.cpuThrottleRate <= 1 && !b.browserNetworkSimulationActive(), CacheEnabled: &cache, CacheContentURLs: &content, CheckNewerVersions: &newer, NewUserPolicy: b.newUserPolicy, NewUserPolicyApplied: b.newUserPolicy == "persistent_context" || b.newUserPolicy == "context_each_iteration"}
	if !cohort.NewUserPolicyApplied {
		cohort.UnavailableReason = "new_user_policy_unavailable"
	} else if !cohort.ShapingApplied {
		cohort.UnavailableReason = "shaping_unavailable"
	}
	b.mu.Lock()
	b.evidence = browserEvidenceState{generation: generation, binding: "__apexEvidence_" + generation, finalizer: "__apexFinalize_" + generation, frames: make(map[playwright.Frame]string), documents: make(map[string]*browserDocumentState), requests: make(map[playwright.Request]*browserRequestIdentity), cohort: cohort, shapedPages: make(map[playwright.Page]bool)}
	b.mu.Unlock()
	return nil
}
func (b *Browser) installDocumentEvidence(ctx playwright.BrowserContext, version string) error {
	if err := b.resetBrowserEvidence(version); err != nil {
		return err
	}
	// Resource IDs use passive native request hooks independently. New document
	// observers/bindings and lifecycle target execution require explicit opt-in.
	if !b.documentMetricsEnabled {
		return nil
	}
	b.mu.Lock()
	binding, finalizer, generation := b.evidence.binding, b.evidence.finalizer, b.evidence.generation
	b.mu.Unlock()
	if err := ctx.ExposeBinding(binding, func(source *playwright.BindingSource, args ...any) any {
		if source == nil || len(args) != 1 {
			return false
		}
		return b.receiveDocumentEvidence(source, args[0], generation)
	}); err != nil {
		return err
	}
	return ctx.AddInitScript(playwright.Script{Content: playwright.String(browserDocumentBootstrap(binding, finalizer, b.documentMetricsEnabled))})
}
func (b *Browser) frameIdentityLocked(frame playwright.Frame) string {
	if frame == nil {
		return ""
	}
	if id := b.evidence.frames[frame]; id != "" {
		return id
	}
	if len(b.evidence.frames) >= browserFrameIdentityLimit {
		b.evidence.incomplete = true
		return ""
	}
	id := b.evidence.nextID("f")
	b.evidence.frames[frame] = id
	return id
}

func browserDocumentPayloadNumber(value any) (float64, bool) {
	// The pinned Playwright SDK materializes integral JS numbers as int and
	// fractional numbers as float64. Admit those exact transport primitives.
	var number float64
	switch v := value.(type) {
	case int:
		number = float64(v)
	case float64:
		number = v
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

// A target can call an exposed binding. Admit only the fixed flat primitive
// envelope before marshaling, so nested/cyclic/large target objects cannot
// allocate an unbounded JSON copy in the generator.
func decodeBrowserDocumentPayload(raw any) (browserDocumentPayload, bool) {
	var payload browserDocumentPayload
	fields, ok := raw.(map[string]any)
	if !ok || len(fields) != 12 {
		return payload, false
	}
	for name, value := range fields {
		switch name {
		case "kind", "document_token", "lifecycle_token", "metric", "state", "boundary", "reason":
			text, ok := value.(string)
			if !ok || len(text) > 64 {
				return payload, false
			}
		case "restored":
			if _, ok := value.(bool); !ok {
				return payload, false
			}
		case "time_origin", "offset", "interaction_count":
			_, ok := browserDocumentPayloadNumber(value)
			if !ok {
				return payload, false
			}
		case "value":
			if value == nil {
				continue
			}
			_, ok := browserDocumentPayloadNumber(value)
			if !ok {
				return payload, false
			}
		default:
			return payload, false
		}
	}
	data, err := json.Marshal(fields)
	if err != nil || len(data) > 4096 {
		return payload, false
	}
	return payload, json.Unmarshal(data, &payload) == nil
}
func (b *Browser) receiveDocumentEvidence(source *playwright.BindingSource, raw any, expectedGeneration ...string) bool {
	// The binding source supplies the actual native frame. No target-provided
	// URL, frame name, native ID or error text becomes an evidence identity.
	p, admitted := decodeBrowserDocumentPayload(raw)
	if !admitted || !browserDocumentToken(p.DocumentToken) || !browserDocumentToken(p.LifecycleToken) || source == nil || source.Frame == nil || source.Page == nil {
		return false
	}
	end, valid := browserEvidenceTime(p.TimeOrigin, p.Offset)
	if !valid || p.InteractionCount < 0 || p.InteractionCount > 1e12 || math.Trunc(p.InteractionCount) != p.InteractionCount {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.evidence
	if s.generation == "" || b.closing || len(expectedGeneration) > 0 && expectedGeneration[0] != s.generation {
		return false
	}
	key := ""
	// Establish the native frame ID before constructing the key.
	frameID := b.frameIdentityLocked(source.Frame)
	if frameID == "" {
		s.droppedObservations++
		return false
	}
	key = frameID + "/" + p.DocumentToken + "/" + p.LifecycleToken
	d := s.documents[key]
	if p.Kind == "begin" {
		if d != nil {
			return true
		}
		if len(s.documents) >= browserDocumentLimit {
			s.droppedObservations += 5
			s.incomplete = true
			return false
		}
		documentID := ""
		for _, old := range s.documents {
			if old.frame == source.Frame && old.token == p.DocumentToken {
				documentID = old.documentID
				break
			}
		}
		if documentID == "" {
			documentID = s.nextID("d")
		}
		start, _ := browserEvidenceTime(p.TimeOrigin, 0)
		if p.Restored {
			start = end
		}
		docCohort := s.cohort.Clone()
		if !docCohort.ShapingApplied && s.shapedPages[source.Page] {
			docCohort.ShapingApplied = true
			if docCohort.UnavailableReason == "shaping_unavailable" {
				docCohort.UnavailableReason = ""
			}
		}
		d = &browserDocumentState{cohort: docCohort, frame: source.Frame, page: source.Page, token: p.DocumentToken, lifecycleToken: p.LifecycleToken, documentID: documentID, lifecycleID: s.nextID("l"), frameID: frameID, navigationID: s.nextID("n"), start: start, timeOrigin: p.TimeOrigin, restored: p.Restored, observations: make(map[string]protocol.BrowserDocumentObservation)}
		s.documents[key] = d
		if b.documentMetricsEnabled {
			for _, metric := range []string{"fcp", "lcp", "cls", "inp", "ttfb"} {
				o := b.documentObservationLocked(d, metric, end)
				o.UnavailableReason = "no_observation"
				d.observations[metric] = o
			}
		}
		return true
	}
	if d == nil || d.timeOrigin != p.TimeOrigin || end.Before(d.start) || d.ended {
		return false
	}
	if p.Kind == "end" {
		d.ended = true
		return true
	}
	if p.Kind != "metric" || !b.documentMetricsEnabled {
		return false
	}
	o, exists := d.observations[p.Metric]
	if !exists {
		return false
	}
	// A metric that reached its own final boundary is immutable. Later lifecycle
	// snapshots cannot create a second sample or revise a completed observation.
	if o.State == "final" || o.State == "censored" {
		return true
	}
	o.Revision++
	o.WindowEnd = end
	o.CapturedAt = time.Now().UTC()
	o.State = p.State
	o.Boundary = p.Boundary
	o.InteractionCount = int64(p.InteractionCount)
	o.Value = p.Value
	o.Available = p.Value != nil
	o.UnavailableReason = p.Reason
	o.Cohort = d.cohort.Clone()
	o.Qualified = o.State == "final" && o.Available && o.Cohort.Eligible() && !s.cancelled
	if o.State == "final" && s.cancelled {
		o.State = "censored"
		o.Boundary = "cancelled"
		o.Value = nil
		o.Available = false
		o.Qualified = false
		o.UnavailableReason = "source_censored"
	}
	if !o.Valid() {
		s.incomplete = true
		return false
	}
	d.observations[p.Metric] = o
	return true
}
func (b *Browser) documentObservationLocked(d *browserDocumentState, metric string, end time.Time) protocol.BrowserDocumentObservation {
	s := &b.evidence
	phase := "navigation"
	if d.restored {
		phase = "bfcache_restore"
	}
	scope := "child_frame"
	if d.page.MainFrame() == d.frame {
		scope = "main_frame"
	}
	unit := "ms"
	if metric == "cls" {
		unit = "score"
	}
	return protocol.BrowserDocumentObservation{Version: protocol.BrowserEvidenceVersion, SourceGeneration: s.generation, DocumentID: d.documentID, LifecycleID: d.lifecycleID, FrameID: d.frameID, NavigationID: d.navigationID, MeasurementID: d.documentID + "/" + d.lifecycleID + "/" + metric, Revision: 1, Metric: metric, Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, Unit: unit, Engine: b.engine, DeviceProfile: b.deviceProfile, Cohort: d.cohort.Clone(), FrameScope: scope, LifecyclePhase: phase, State: "provisional", Boundary: "progress", WindowStart: d.start, WindowEnd: end, CapturedAt: time.Now().UTC(), UnavailableReason: "no_observation"}
}
func (b *Browser) BrowserDocumentEvidence() *protocol.BrowserDocumentEvidence {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.evidence
	if s.generation == "" || !b.documentMetricsEnabled {
		return nil
	}
	e := &protocol.BrowserDocumentEvidence{Version: protocol.BrowserEvidenceVersion, SourceGeneration: s.generation, DroppedObservations: s.droppedObservations, Incomplete: s.incomplete, IssuedRequests: s.issued, InFlightRequests: s.inFlight, DroppedRequestIdentities: s.droppedRequests}
	for _, d := range s.documents {
		for _, o := range d.observations {
			e.Observations = append(e.Observations, o)
		}
	}
	sort.Slice(e.Observations, func(i, j int) bool { return e.Observations[i].MeasurementID < e.Observations[j].MeasurementID })
	return protocol.PackBrowserDocumentEvidence(e)
}
func (b *Browser) beginEvidenceAction() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != "" {
		b.evidence.currentAction = b.evidence.nextID("a")
	}
}
func (b *Browser) endEvidenceAction(cancelled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evidence.currentAction = ""
	b.evidence.cancelled = b.evidence.cancelled || cancelled
}
func (b *Browser) censorDocumentEvidence(page playwright.Page, boundary string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.evidence.documents {
		if page != nil && d.page != page {
			continue
		}
		for metric, o := range d.observations {
			if o.State == "final" || o.State == "censored" {
				continue
			}
			o.Revision++
			o.State = "censored"
			o.Boundary = boundary
			o.Value = nil
			o.Available = false
			o.Qualified = false
			o.UnavailableReason = "source_censored"
			o.CapturedAt = time.Now().UTC()
			d.observations[metric] = o
		}
		d.ended = true
	}
}
func (b *Browser) finalizeDocumentEvidence(ctx context.Context) {
	b.mu.Lock()
	frames := make(map[playwright.Frame]playwright.Page)
	for _, doc := range b.evidence.documents {
		if !doc.ended {
			frames[doc.frame] = doc.page
		}
	}
	finalizer := b.evidence.finalizer
	cancelled := b.evidence.cancelled || b.evidence.lifecycleContext != nil && b.evidence.lifecycleContext.Err() != nil
	browserContext := b.pwContext
	b.mu.Unlock()
	if cancelled || ctx.Err() != nil {
		b.censorDocumentEvidence(nil, "cancelled")
		return
	}
	if finalizer == "" || browserContext == nil {
		return
	}
	// One bounded budget for the entire existing context. Cancellation closes
	// only this owned context, physically unblocking native Evaluate, and joins
	// both the worker and the close callback before returning.
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	closed := make(chan struct{})
	stopClose := context.AfterFunc(bounded, func() { defer close(closed); _ = browserContext.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		for frame, page := range frames {
			if bounded.Err() != nil {
				return
			}
			_, err := frame.Evaluate(fmt.Sprintf("async () => { const f=globalThis[%q]; if(typeof f==='function') await f('graceful_end'); }", finalizer))
			if err != nil {
				b.censorDocumentEvidence(page, "context_closed")
			}
		}
	}()
	<-done
	if !stopClose() {
		<-closed
	}
	if bounded.Err() != nil {
		b.censorDocumentEvidence(nil, "cancelled")
	} else {
		b.censorDocumentEvidence(nil, "context_closed")
	}
}

func (b *Browser) censorFrameEvidence(frame playwright.Frame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.evidence.documents {
		if d.frame != frame {
			continue
		}
		for metric, o := range d.observations {
			if o.State == "final" || o.State == "censored" {
				continue
			}
			o.Revision++
			o.State = "censored"
			o.Boundary = "frame_detached"
			o.Value = nil
			o.Available = false
			o.Qualified = false
			o.UnavailableReason = "source_censored"
			o.CapturedAt = time.Now().UTC()
			d.observations[metric] = o
		}
		d.ended = true
	}
}

func (b *Browser) browserEvidenceGenerationCurrent(generation string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.closing && (generation == "" || generation == b.evidence.generation)
}
func (b *Browser) BrowserDocumentCaptureEnabled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.documentMetricsEnabled
}
