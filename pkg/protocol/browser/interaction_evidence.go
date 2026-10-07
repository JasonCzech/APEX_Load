package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Interaction evidence (E3). With browser_capture_interactions an init
// script observes trusted pointer events in every document; after each
// gesture's own wait one page evaluation drains them with the viewport,
// scroll, device scale, sanitized route, a structural layout fingerprint and,
// for keyboard input, the focused target's role and sanitized label (hashed
// here, never stored). Nothing is dispatched to the page and no request is
// made. Interactions inside child frames are recorded without points.

// browserInteractionState is a session's interactions not yet handed over.
type browserInteractionState struct {
	pending    []protocol.BrowserInteraction
	documents  map[string]*browserInteractionDocument
	dropped    int64
	incomplete bool
}

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
 Object.defineProperty(globalThis, name, {configurable: false, enumerable: false, value: {drain() {
  const out = {doc: token, events, move, vw: innerWidth, vh: innerHeight, sx: scrollX, sy: scrollY, dpr: devicePixelRatio, path: location.pathname, layout: layout(), focus: focus()};
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

// captureBrowserInteraction records one gesture after its action finished.
func (b *Browser) captureBrowserInteraction(ctx context.Context, action *protocol.Action, result *protocol.Result, actionErr error) {
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
	if action.WaitFor != nil && strings.TrimSpace(action.WaitFor.Type) != "" {
		interaction.Outcome = protocol.BrowserInteractionOutcome{Declared: boundedInteractionText(strings.ToLower(strings.TrimSpace(action.WaitFor.Type)), 64), DeadlineMS: action.WaitFor.TimeoutMs, Observed: protocol.InteractionOutcomeMissed}
		if interaction.Outcome.DeadlineMS <= 0 {
			interaction.Outcome.DeadlineMS = int(b.waitTimeout(0) / time.Millisecond)
		}
		if interaction.Success {
			interaction.Outcome.Observed = protocol.InteractionOutcomeMet
		}
	}
	framed := len(b.actionFrameSelectors(action)) > 0
	if framed {
		interaction.FrameScope = "child_frame"
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var page browserInteractionPage
	err := b.evaluatePageExpression(readCtx, fmt.Sprintf(`(() => { const o = globalThis[%q]; return o ? o.drain() : null })()`, browserInteractionGlobal(generation)), &page)
	switch {
	case err != nil || page.Doc == "":
		interaction.UnavailableReason = "page_state_unavailable"
	default:
		if protocol.BrowserEvidenceGeneration(page.Doc) {
			interaction.DocumentID = generation + "-idoc-" + page.Doc
		}
		interaction.ViewportWidth, interaction.ViewportHeight, interaction.ScrollX, interaction.ScrollY, interaction.DeviceScaleFactor = page.VW, page.VH, page.SX, page.SY, page.DPR
		if len(page.Layout) == 16 {
			interaction.LayoutRevision = page.Layout
		}
		interaction.Route = sanitizeBrowserRoute(page.Path)
		if protocol.BrowserInteractionPointerKind(kind) {
			if framed {
				interaction.UnavailableReason = "child_frame_unobserved"
			} else {
				interaction.Points, interaction.PointerType = browserInteractionPoints(kind, page)
			}
		}
		if !protocol.BrowserInteractionPointerKind(kind) || kind == "check" {
			if page.Focus != nil && page.Focus.Role != "" {
				interaction.Target = &protocol.BrowserInteractionTarget{Role: page.Focus.Role}
				if page.Focus.Label != "" {
					sum := sha256.Sum256([]byte(page.Focus.Label))
					interaction.Target.LabelSHA256 = hex.EncodeToString(sum[:])
				}
			}
		}
	}
	b.recordBrowserInteraction(interaction)
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
		if len(s.documents) >= 1024 {
			s.dropped++
			s.incomplete = true
			return
		}
		document = &browserInteractionDocument{}
		s.documents[interaction.DocumentID] = document
	}
	if document.count >= protocol.MaxBrowserInteractionsPerDocument || document.bytes+len(encoded) > protocol.MaxBrowserInteractionBytesPerDocument || len(s.pending) >= protocol.MaxBrowserInteractionsPerEvidence {
		s.dropped++
		s.incomplete = true
		return
	}
	document.count++
	document.bytes += len(encoded)
	s.pending = append(s.pending, interaction)
}

// TakeBrowserInteractionEvidence hands over the interactions recorded since
// the last call: one bounded evidence record per source generation (each
// browser context has its own), the drops counted on the newest.
func (b *Browser) TakeBrowserInteractionEvidence() []*protocol.BrowserInteractionEvidence {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.interactions
	var out []*protocol.BrowserInteractionEvidence
	for _, interaction := range s.pending {
		if len(out) == 0 || out[len(out)-1].SourceGeneration != interaction.SourceGeneration {
			out = append(out, &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: interaction.SourceGeneration})
		}
		last := out[len(out)-1]
		last.Interactions = append(last.Interactions, interaction)
	}
	if s.dropped > 0 || s.incomplete {
		if len(out) == 0 && b.evidence.generation != "" {
			out = append(out, &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: b.evidence.generation, Interactions: []protocol.BrowserInteraction{}})
		}
		if len(out) > 0 {
			out[len(out)-1].Dropped, out[len(out)-1].Incomplete = s.dropped, true
		}
	}
	s.pending, s.dropped, s.incomplete = nil, 0, false
	for i, evidence := range out {
		out[i] = evidence.Clone()
	}
	return out
}

// BrowserInteractionCaptureEnabled reports whether this session records
// interactions.
func (b *Browser) BrowserInteractionCaptureEnabled() bool { return b.interactionsEnabled }
