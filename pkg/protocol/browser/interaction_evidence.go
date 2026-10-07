package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// Interaction evidence (E3). With browser_capture_interactions an init
// script observes trusted pointer events in every document. Before a gesture
// one page evaluation discards events earlier actions left (a download's
// click) and notes the document; after the gesture's own wait another drains
// its events with the viewport, scroll, device scale, sanitized route and a
// structural layout fingerprint. A keyboard gesture's target is the element
// its own selector resolves to (or, without one, the element focused before
// it), described by role and the SHA-256 of its sanitized label, never a
// value. Nothing is dispatched to the page and no request is made. Gestures
// inside child frames, gestures the runtime dispatches as synthetic events
// (touch emulation) and gestures that navigate are recorded without points,
// with the reason.

// browserInteractionState is a session's interactions not yet handed over.
type browserInteractionState struct {
	pending []protocol.BrowserInteraction
	// sizes are the pending interactions' encoded sizes, for splitting a
	// handoff into records within the evidence bound.
	sizes      []int
	documents  map[string]*browserInteractionDocument
	order      []string
	dropped    int64
	incomplete bool
}

// maxBrowserInteractionDocuments bounds the documents a session tracks
// bounds for; past it the least recently used one is forgotten (a persistent
// context outlives any fixed number of documents).
const maxBrowserInteractionDocuments = 1024

type browserInteractionDocument struct {
	count int
	bytes int
}

// browserInteractionObserver is the init script; name is the context's
// random global that holds the observer.
func browserInteractionObserver(name string) string {
	return fmt.Sprintf(`(() => {
 "use strict";
 const name = %q;
 if (Object.prototype.hasOwnProperty.call(globalThis, name)) return;
 const random = new Uint8Array(16); crypto.getRandomValues(random);
 const token = Array.from(random, x => x.toString(16).padStart(2, '0')).join('');
 let events = [], move = null;
 const keep = e => ({t: e.type, x: e.clientX, y: e.clientY, p: e.pointerType || ''});
 const record = e => {
  if (!e.isTrusted) return;
  if (e.type === 'pointermove') { move = keep(e); return; }
  if (events.length >= 32) events.shift();
  events.push(keep(e));
 };
 for (const type of ['pointerdown', 'pointerup', 'pointermove']) addEventListener(type, record, {capture: true, passive: true});
 const quantize = v => Math.round(v / 32);
 const hash = s => {
  let h1 = 0x811c9dc5, h2 = 0x050c5d1f;
  for (let i = 0; i < s.length; i++) { const c = s.charCodeAt(i); h1 = Math.imul(h1 ^ c, 0x01000193); h2 = Math.imul(h2 ^ c, 0x01000193); }
  return (h1 >>> 0).toString(16).padStart(8, '0') + (h2 >>> 0).toString(16).padStart(8, '0');
 };
 // Tags, safe role tokens and quantized boxes of at most 1,024 elements;
 // never text or other attribute values.
 const layout = () => {
  const parts = [], queue = document.documentElement ? [document.documentElement] : [];
  for (let n = 0; queue.length && n < 1024; n++) {
   const el = queue.shift(), r = el.getBoundingClientRect(), role = (el.getAttribute('role') || '').toLowerCase();
   parts.push(el.localName + '|' + (/^[a-z]{1,24}$/.test(role) ? role : '') + '|' + quantize(r.left + scrollX) + ',' + quantize(r.top + scrollY) + ',' + quantize(r.width) + ',' + quantize(r.height));
   for (const child of el.children) queue.push(child);
  }
  return hash(parts.join(';'));
 };
 const implicitRole = el => {
  const tag = el.localName, type = (el.getAttribute('type') || 'text').toLowerCase();
  if (tag === 'input') return ({checkbox: 'checkbox', radio: 'radio', button: 'button', submit: 'button', reset: 'button', range: 'slider', file: 'button', search: 'searchbox'})[type] || 'textbox';
  return ({textarea: 'textbox', select: 'combobox', button: 'button', a: 'link', option: 'option'})[tag] || 'generic';
 };
 const focus = () => {
  const el = document.activeElement;
  if (!el || el === document.body || el === document.documentElement) return null;
  const explicit = (el.getAttribute('role') || '').toLowerCase();
  let label = el.getAttribute('aria-label') || '';
  if (!label && el.labels && el.labels.length) label = el.labels[0].textContent || '';
  if (!label) label = el.getAttribute('placeholder') || el.getAttribute('name') || el.getAttribute('title') || '';
  return {role: /^[a-z]{1,24}$/.test(explicit) ? explicit : implicitRole(el), label: label.toLowerCase().replace(/[0-9]/g, '#').replace(/\s+/g, ' ').trim().slice(0, 64)};
 };
 Object.defineProperty(globalThis, name, {configurable: false, enumerable: false, value: {drain(light) {
  // A gesture's start reads no layout: it forces a reflow inside the
  // measured action.
  const out = {doc: token, events, move, vw: innerWidth, vh: innerHeight, sx: scrollX, sy: scrollY, dpr: devicePixelRatio, path: location.pathname, layout: light ? '' : layout(), focus: focus()};
  events = []; move = null;
  return out;
 }}});
})()`, name)
}

func browserInteractionGlobal(generation string) string { return "__apexInteraction_" + generation }

// browserInteractionKind maps a browser action to its gesture kind.
func (b *Browser) browserInteractionKind(action *protocol.Action) (string, bool) {
	kind, ok := map[string]string{
		"browser_click": "click", "browser_dblclick": "dblclick", "browser_tap": "tap", "browser_long_press": "long_press",
		"browser_swipe": "swipe", "browser_hover": "hover", "browser_drag_and_drop": "drag", "browser_scroll": "scroll",
		"browser_check": "check", "browser_press_key": "key", "browser_fill": "fill", "browser_select": "select", "browser_set_files": "set_files",
	}[action.Type]
	if ok && kind == "click" && b.touchMode {
		kind = "tap"
	}
	return kind, ok
}

// browserInteractionProvenance says who authored a gesture: scripted unless
// it came from the recorder, which counts as recorded only with the
// recorder's explicit consent flag.
func browserInteractionProvenance(action *protocol.Action) string {
	if action.Metadata == nil || action.Metadata["recorder_step_seq"] == nil {
		return protocol.InteractionProvenanceScripted
	}
	if consent, _ := action.Metadata["interaction_consent"].(bool); consent {
		return protocol.InteractionProvenanceRecorded
	}
	return protocol.InteractionProvenanceUnknown
}

var browserRouteIDSegment = regexp.MustCompile(`^(?:[0-9]+|[0-9a-fA-F]{8,}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|.*[0-9].*[0-9].*[0-9].*)$`)

// sanitizeBrowserRoute collapses identifier-like path segments and drops
// the query and fragment.
func sanitizeBrowserRoute(path string) string {
	path, _, _ = strings.Cut(path, "?")
	path, _, _ = strings.Cut(path, "#")
	if !strings.HasPrefix(path, "/") {
		return ""
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if segment != "" && (browserRouteIDSegment.MatchString(segment) || len(segment) > 64) {
			segments[i] = ":id"
		}
	}
	route := strings.Join(segments, "/")
	if len(route) > 256 {
		route = route[:256]
	}
	return strings.ToValidUTF8(route, "")
}

type browserInteractionPage struct {
	Doc    string `json:"doc"`
	Events []struct {
		T string  `json:"t"`
		X float64 `json:"x"`
		Y float64 `json:"y"`
		P string  `json:"p"`
	} `json:"events"`
	Move *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		P string  `json:"p"`
	} `json:"move"`
	VW     int     `json:"vw"`
	VH     int     `json:"vh"`
	SX     float64 `json:"sx"`
	SY     float64 `json:"sy"`
	DPR    float64 `json:"dpr"`
	Path   string  `json:"path"`
	Layout string  `json:"layout"`
	Focus  *struct {
		Role  string `json:"role"`
		Label string `json:"label"`
	} `json:"focus"`
}

// browserInteractionStart is the page as a gesture began: its document and
// the element then focused.
type browserInteractionStart struct {
	page browserInteractionPage
	read bool
}

// browserInteractionSync is the outcome of a gesture's declared post-action
// wait.
type browserInteractionSync struct {
	reached bool
	err     error
}

// interactionReadTimeout bounds each page evaluation of interaction capture.
const interactionReadTimeout = 2 * time.Second

// interactionReadOverhead is the most of a gesture's start read left out of
// its measured duration: the read's own round trip. Time the read waited for
// a busy page stays in the gesture.
const interactionReadOverhead = 25 * time.Millisecond

// beginBrowserInteraction discards events earlier actions left and notes
// the document a gesture starts in.
func (b *Browser) beginBrowserInteraction(ctx context.Context, action *protocol.Action) *browserInteractionStart {
	if _, gesture := b.browserInteractionKind(action); !gesture || !b.interactionsEnabled || ctx.Err() != nil {
		return nil
	}
	b.mu.Lock()
	generation := b.evidence.generation
	b.mu.Unlock()
	if generation == "" {
		return nil
	}
	start := &browserInteractionStart{}
	start.read = b.drainBrowserInteractionPageAs(ctx, generation, true, &start.page) == nil && start.page.Doc != ""
	return start
}

// drainBrowserInteractionPage drains the observer within the read timeout:
// a page whose main thread stays busy never stalls the VUser past it.
func (b *Browser) drainBrowserInteractionPage(ctx context.Context, generation string, page *browserInteractionPage) error {
	return b.drainBrowserInteractionPageAs(ctx, generation, false, page)
}

// drainBrowserInteractionPageAs drains the observer; a light read (at a
// gesture's start) skips the layout fingerprint.
func (b *Browser) drainBrowserInteractionPageAs(ctx context.Context, generation string, light bool, page *browserInteractionPage) error {
	readCtx, cancel := context.WithTimeout(ctx, interactionReadTimeout)
	defer cancel()
	done := make(chan error, 1)
	var read browserInteractionPage
	go func() {
		done <- b.evaluatePageExpression(readCtx, fmt.Sprintf(`(() => { const o = globalThis[%q]; return o ? o.drain(%t) : null })()`, browserInteractionGlobal(generation), light), &read)
	}()
	select {
	case err := <-done:
		if err == nil {
			*page = read
		}
		return err
	case <-readCtx.Done():
		return readCtx.Err()
	}
}

// captureBrowserInteraction records one gesture after its action finished.
func (b *Browser) captureBrowserInteraction(ctx context.Context, action *protocol.Action, result *protocol.Result, actionErr error, start *browserInteractionStart, sync browserInteractionSync) {
	kind, gesture := b.browserInteractionKind(action)
	if !gesture || !b.interactionsEnabled || ctx.Err() != nil {
		return
	}
	b.mu.Lock()
	generation, actionID := b.evidence.generation, b.evidence.currentAction
	b.mu.Unlock()
	if generation == "" {
		return
	}
	interaction := protocol.BrowserInteraction{Version: protocol.BrowserInteractionVersion, SourceGeneration: generation, ActionID: actionID, ActionName: boundedInteractionText(action.Name, 256), FrameScope: "main_frame", Kind: kind,
		Provenance: browserInteractionProvenance(action), Success: actionErr == nil && result != nil && result.Success, CapturedAt: time.Now().UTC(), Outcome: protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared}}
	// The declared outcome is the gesture's post-action wait (sync), the one
	// the runtime actually evaluates after it.
	if policy := browserSyncPolicy(action); policy != nil {
		declared := strings.ToLower(strings.TrimSpace(policy.Type))
		if declared == "" {
			declared = "network_idle"
		}
		interaction.Outcome = protocol.BrowserInteractionOutcome{Declared: boundedInteractionText(declared, 64), DeadlineMS: int(b.waitTimeout(policy.TimeoutMs) / time.Millisecond), Observed: protocol.InteractionOutcomeNotReached}
		if sync.reached {
			interaction.Outcome.Observed = protocol.InteractionOutcomeMissed
			if sync.err == nil {
				interaction.Outcome.Observed = protocol.InteractionOutcomeMet
			}
		}
	}
	framed := len(b.actionFrameSelectors(action)) > 0
	if framed {
		interaction.FrameScope = "child_frame"
	}
	var page browserInteractionPage
	if err := b.drainBrowserInteractionPage(ctx, generation, &page); err != nil || page.Doc == "" {
		interaction.UnavailableReason = protocol.InteractionUnavailablePageState
		b.recordBrowserInteraction(interaction)
		return
	}
	// A gesture that navigated left its events in the document it started
	// in: the record describes that document, without points.
	navigated := start != nil && start.read && start.page.Doc != page.Doc
	state := page
	if navigated {
		state = start.page
	}
	if protocol.BrowserEvidenceGeneration(state.Doc) {
		interaction.DocumentID = generation + "-idoc-" + state.Doc
	}
	interaction.ViewportWidth, interaction.ViewportHeight, interaction.ScrollX, interaction.ScrollY, interaction.DeviceScaleFactor = state.VW, state.VH, state.SX, state.SY, state.DPR
	if len(state.Layout) == 16 {
		interaction.LayoutRevision = state.Layout
	}
	interaction.Route = sanitizeBrowserRoute(state.Path)
	if protocol.BrowserInteractionPointerKind(kind) {
		switch {
		case framed:
			interaction.UnavailableReason = protocol.InteractionUnavailableChildFrame
		case navigated:
			interaction.UnavailableReason = protocol.InteractionUnavailableDocumentChanged
		default:
			interaction.Points, interaction.PointerType = browserInteractionPoints(kind, page)
			if len(interaction.Points) == 0 && b.syntheticGesture(kind) {
				interaction.UnavailableReason = protocol.InteractionUnavailableSyntheticDispatch
			}
		}
	}
	// Keyboard-style gestures (and a check) name their target; a scroll
	// names none.
	if kind != "scroll" && !protocol.BrowserInteractionPointerKind(kind) || kind == "check" {
		switch {
		case framed:
			interaction.UnavailableReason = protocol.InteractionUnavailableChildFrame
		default:
			target, reason := b.browserInteractionTarget(ctx, action, start, navigated)
			interaction.Target = target
			if interaction.UnavailableReason == "" {
				interaction.UnavailableReason = reason
			}
		}
	}
	b.recordBrowserInteraction(interaction)
}

// syntheticGesture reports whether the runtime dispatches kind as synthetic
// (untrusted) events, which the observer ignores: every touch gesture.
func (b *Browser) syntheticGesture(kind string) bool {
	switch kind {
	case "tap", "long_press", "swipe":
		return true
	}
	return b.touchMode
}

// browserInteractionTargetScript describes an element by role and sanitized
// label, as the observer describes focus.
const browserInteractionTargetScript = `el => {
 const tag = el.localName, type = (el.getAttribute('type') || 'text').toLowerCase();
 const implicit = tag === 'input' ? (({checkbox: 'checkbox', radio: 'radio', button: 'button', submit: 'button', reset: 'button', range: 'slider', file: 'button', search: 'searchbox'})[type] || 'textbox') : (({textarea: 'textbox', select: 'combobox', button: 'button', a: 'link', option: 'option'})[tag] || 'generic');
 const explicit = (el.getAttribute('role') || '').toLowerCase();
 let label = el.getAttribute('aria-label') || '';
 if (!label && el.labels && el.labels.length) label = el.labels[0].textContent || '';
 if (!label) label = el.getAttribute('placeholder') || el.getAttribute('name') || el.getAttribute('title') || '';
 return {role: /^[a-z]{1,24}$/.test(explicit) ? explicit : implicit, label: label.toLowerCase().replace(/[0-9]/g, '#').replace(/\s+/g, ' ').trim().slice(0, 64)};
}`

// browserInteractionTarget is the element a keyboard gesture acted on: the
// one its selector resolves to, or without a selector the element focused
// when it began. An element that cannot be resolved is unavailable at once
// (never waited for), and after a navigation the selector would describe
// another document's element, so it is not resolved.
func (b *Browser) browserInteractionTarget(ctx context.Context, action *protocol.Action, start *browserInteractionStart, navigated bool) (*protocol.BrowserInteractionTarget, string) {
	describe := func(role, label string) *protocol.BrowserInteractionTarget {
		target := &protocol.BrowserInteractionTarget{Role: role}
		if label != "" {
			sum := sha256.Sum256([]byte(label))
			target.LabelSHA256 = hex.EncodeToString(sum[:])
		}
		return target
	}
	selector := b.actionSelector(action)
	if selector == "" {
		if start != nil && start.read && start.page.Focus != nil && start.page.Focus.Role != "" {
			return describe(start.page.Focus.Role, start.page.Focus.Label), ""
		}
		return nil, protocol.InteractionUnavailableTarget
	}
	if navigated {
		return nil, protocol.InteractionUnavailableDocumentChanged
	}
	page := b.currentPage()
	if page == nil || ctx.Err() != nil {
		return nil, protocol.InteractionUnavailableTarget
	}
	readCtx, cancel := context.WithTimeout(ctx, interactionReadTimeout)
	defer cancel()
	type described struct {
		value any
		err   error
	}
	done := make(chan described, 1)
	go func() {
		// Count does not wait: a target the gesture removed (a submitted
		// form, a closed dialog) is unresolved now, not after a timeout.
		locator := page.Locator(selector)
		if count, err := locator.Count(); err != nil || count == 0 {
			done <- described{nil, errors.Join(err, errors.New("target is not attached"))}
			return
		}
		value, err := locator.First().Evaluate(browserInteractionTargetScript, nil, playwright.LocatorEvaluateOptions{Timeout: playwright.Float(float64(interactionReadTimeout / time.Millisecond))})
		done <- described{value, err}
	}()
	var out described
	select {
	case out = <-done:
	case <-readCtx.Done():
		return nil, protocol.InteractionUnavailableTarget
	}
	fields, ok := out.value.(map[string]any)
	role, _ := fields["role"].(string)
	label, _ := fields["label"].(string)
	if out.err != nil || !ok || role == "" {
		return nil, protocol.InteractionUnavailableTarget
	}
	return describe(role, label), ""
}

// browserInteractionPoints chooses the dispatched points a gesture's kind
// records: each press, the hover position, or a drag's start and end.
func browserInteractionPoints(kind string, page browserInteractionPage) ([]protocol.BrowserInteractionPoint, string) {
	var points []protocol.BrowserInteractionPoint
	pointer := ""
	add := func(x, y float64, p string) {
		if len(points) < 8 {
			points = append(points, protocol.BrowserInteractionPoint{X: x, Y: y})
			if pointer == "" {
				pointer = p
			}
		}
	}
	switch kind {
	case "hover":
		if page.Move != nil {
			add(page.Move.X, page.Move.Y, page.Move.P)
		}
	case "drag", "swipe":
		var last *protocol.BrowserInteractionPoint
		for _, e := range page.Events {
			if e.T == "pointerdown" && len(points) == 0 {
				add(e.X, e.Y, e.P)
			}
			if e.T == "pointerup" {
				last = &protocol.BrowserInteractionPoint{X: e.X, Y: e.Y}
			}
		}
		if last != nil && len(points) == 1 {
			add(last.X, last.Y, "")
		}
	default:
		for _, e := range page.Events {
			if e.T == "pointerdown" {
				add(e.X, e.Y, e.P)
			}
		}
	}
	switch pointer {
	case "mouse", "touch", "pen":
	default:
		pointer = ""
	}
	return points, pointer
}

func boundedInteractionText(s string, limit int) string {
	s = strings.ToValidUTF8(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s), "")
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "")
	}
	return s
}

// recordBrowserInteraction keeps an interaction within the per-document and
// per-session bounds; anything beyond them is counted as dropped.
func (b *Browser) recordBrowserInteraction(interaction protocol.BrowserInteraction) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.interactions
	interaction.InteractionID = b.evidence.nextID("i")
	encoded, err := json.Marshal(interaction)
	if err != nil || !interaction.Valid() {
		s.dropped++
		s.incomplete = true
		return
	}
	if s.documents == nil {
		s.documents = map[string]*browserInteractionDocument{}
	}
	document := s.documents[interaction.DocumentID]
	if document == nil {
		if len(s.order) >= maxBrowserInteractionDocuments {
			delete(s.documents, s.order[0])
			s.order = s.order[1:]
		}
		document = &browserInteractionDocument{}
		s.documents[interaction.DocumentID] = document
	} else {
		// Most recently used last.
		for k, id := range s.order {
			if id == interaction.DocumentID {
				s.order = append(s.order[:k], s.order[k+1:]...)
				break
			}
		}
	}
	s.order = append(s.order, interaction.DocumentID)
	if document.count >= protocol.MaxBrowserInteractionsPerDocument || document.bytes+len(encoded) > protocol.MaxBrowserInteractionBytesPerDocument || len(s.pending) >= protocol.MaxBrowserInteractionsPerEvidence {
		s.dropped++
		s.incomplete = true
		return
	}
	document.count++
	document.bytes += len(encoded)
	s.pending = append(s.pending, interaction)
	s.sizes = append(s.sizes, len(encoded))
}

// TakeBrowserInteractionEvidence hands over the interactions recorded since
// the last call: one bounded evidence record per source generation (each
// browser context has its own), the drops counted on the newest.
func (b *Browser) TakeBrowserInteractionEvidence() []*protocol.BrowserInteractionEvidence {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.interactions
	var out []*protocol.BrowserInteractionEvidence
	// One record per source generation, split so each stays within the
	// evidence byte bound (with room for its envelope).
	const envelope = 1024
	size := 0
	for k, interaction := range s.pending {
		encoded := s.sizes[k] + 1
		if len(out) == 0 || out[len(out)-1].SourceGeneration != interaction.SourceGeneration || size+encoded > protocol.MaxBrowserInteractionEvidenceBytes-envelope {
			out = append(out, &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: interaction.SourceGeneration})
			size = 0
		}
		last := out[len(out)-1]
		last.Interactions = append(last.Interactions, interaction)
		size += encoded
	}
	if s.dropped > 0 || s.incomplete {
		if len(out) == 0 && b.evidence.generation != "" {
			out = append(out, &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: b.evidence.generation, Interactions: []protocol.BrowserInteraction{}})
		}
		if len(out) > 0 {
			out[len(out)-1].Dropped, out[len(out)-1].Incomplete = s.dropped, true
		}
	}
	s.pending, s.sizes, s.dropped, s.incomplete = nil, nil, 0, false
	for i, evidence := range out {
		out[i] = evidence.Clone()
	}
	return out
}

// BrowserInteractionCaptureEnabled reports whether this session records
// interactions.
func (b *Browser) BrowserInteractionCaptureEnabled() bool { return b.interactionsEnabled }
