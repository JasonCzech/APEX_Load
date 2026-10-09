# Browser interaction evidence

Interaction evidence records what each scripted browser gesture did, so later
analysis (repeated-gesture patterns, heatmaps) works from observed facts rather
than from the script. It is opt-in, adds no target traffic and never records
input values. This guide covers capture and transport; analysis of the
evidence is separate work.

## Enable capture

Set `config.browser.capture_interactions: true` on a browser script. A
distributed run then requires generators that advertise
`browser_interaction_evidence_v1`; older generators are not selected. Document
metric capture (`capture_document_metrics`) is a separate opt-in.

## What one interaction records

Each gesture action (`browser_click`, `browser_dblclick`, `browser_tap`,
`browser_long_press`, `browser_swipe`, `browser_hover`,
`browser_drag_and_drop`, `browser_scroll`, `browser_check`,
`browser_press_key`, `browser_fill`, `browser_select`,
`browser_set_files`) produces one record:

- **Kind, identities and provenance.** The gesture kind, an interaction ID,
  the browser evidence action ID (shared with document and resource
  evidence), the document identity and the action name. Provenance is
  `scripted`; a recorder-imported step is `recorded` only when the recorder
  import carries an explicit `interaction_consent` flag, and `unknown`
  otherwise. No importer emits that flag yet, so recorder-imported steps
  are `unknown` today.
- **Dispatched points.** For pointer gestures, the CSS-pixel points the page
  actually received, taken from trusted `pointerdown`, `pointerup` and
  `pointermove` events: each press for clicks, taps and checks, the hover
  position, and the start and end of a drag or swipe, with the pointer type.
  The page's own synthetic events are ignored. Gestures inside a child frame
  are recorded with `frame_scope: child_frame` and no points
  (`child_frame_unobserved`); no frame transform is inferred. Touch gestures
  (taps, long presses, swipes, and clicks in a touch session) are dispatched
  by the runtime as synthetic events, which the page cannot tell from
  script, so they are recorded without points (`synthetic_dispatch`). A
  gesture that navigates takes its document's events with it: it is recorded
  for the document it started in, with that document's context (no layout
  revision) and no points (`document_changed`). Pointer input left by actions that are not gestures
  (a download's click) is discarded when the next gesture starts.
- **Keyboard targets.** For key, fill, select, set-files and check
  gestures, the role and the SHA-256 of the sanitized label (aria-label,
  associated label, placeholder, name or title, lowercased, digits replaced
  by `#`, whitespace collapsed, at most 64 characters) of the element the
  gesture's own selector resolves to, or, for a key press without one, of
  the element focused when it began. An element that cannot be resolved, including
  one the gesture removed, is `target_unresolved` at once (never waited
  for); after a gesture that navigated, a selector's target is not resolved
  in the new document (`document_changed`); inside a child frame there is no
  target, and a scroll names none. No coordinates, and never the element's
  value.
- **Context.** Viewport size, scroll offsets, device scale, the sanitized
  route (the path only, with the run's runtime secret values redacted and
  numeric, UUID, long hexadecimal and digit-heavy segments collapsed to
  `:id`; a route the run's redaction would leave without a path, or lengthen
  past 512 bytes, is recorded as none) and a layout revision: a
  fingerprint of a breadth-first skeleton of at most 1,024 elements (tag,
  a safe role token and the box quantized to 32 CSS pixels), with no text
  or other attribute values.
- **Outcome.** The declared outcome is the gesture's post-action wait
  (`sync`), the one the runtime evaluates after it, with its deadline; the
  observed result is `met` or `missed` from that wait, or `not_reached` when
  the gesture failed before it. Actions without a post-action wait record
  `not_declared`.

The page state is read by a script installed in every document of the
context: once as the gesture starts (its document, focus, viewport and
route, without the layout fingerprint; the read's own round trip, at most
25 ms, is left out of the action's measured duration, but a busy page's wait
behind it stays in), and once after it and its wait. Each read is bounded to two seconds, so a busy page never
stalls the VUser. A record the page could not supply is kept with
`page_state_unavailable`.

## Bounds and transport

A document keeps at most 1,024 interactions and 64 KiB of them (a session
tracks its 1,024 most recently used documents), and a session holds at most
4,096 before handing them over; anything beyond is counted as dropped and
marks the evidence incomplete. Sessions hand their interactions to the run
archive at every iteration boundary and at teardown, split into records of at
most 512 KiB; a record a sink still refuses is counted as dropped. A run
archive keeps them in its own `browser_interactions.jsonl` sidecar, at most
64 MiB. Optional evidence (interactions and HARs together) stays within one
allowance fixed at its first write: a quarter of the run archive limit or of
the free space above the archive volume's floor, whichever is smaller, on a
generator and on a controller running the test itself alike; a record
beyond it is dropped. A write refused before any byte (quota, free space)
loses only that record; a torn write loses the sidecar, and every record in
it is counted as dropped.

A generator declares its sidecar as transferable evidence
(`browser_interactions_artifact`). At finalization the controller fetches it
from the live generator within one 64 MiB allowance for the whole run (so the
merged sidecar stays readable; each source's whole declared sidecar is
reserved before it streams and returned if it is not kept) charged to the
transfer reservation, verifies
its size, SHA-256 and every record, and records an `interaction_evidence`
receipt. A controller running the test itself writes the sidecar
directly. It then merges all sources into
the run archive's sidecar (`browser_interactions_file`, with the record count
and inventory entry `browser_interactions_v1`) and acknowledges the
generator after publication, exactly as for
[native HARs](BROWSER_HAR_WORKING_OWNERSHIP.md#run-archive-transfer-and-receipts).
A source that cannot be transferred, or whose records differ from its
declaration, marks `browser_interactions_incomplete` (its interactions are not
counted in `browser_interactions_dropped`, since the controller never read
them), and so does one the
archive's optional-evidence allowance (shared with HAR copies; see the
[HAR guide](BROWSER_HAR_WORKING_OWNERSHIP.md#run-archive-transfer-and-receipts))
cannot hold: it is never receipted, and its interactions count as dropped.

## Qualification

Chromium and WebKit fixtures check that recorded click and double-click
points match Playwright's own element boxes within one CSS pixel, that hover
points fall inside the hovered element, that a page's synthetic pointer
event is not recorded, that the route and label are sanitized, that input
values never appear, that the layout revision changes only when the layout
does, and that an independent request counter sees no added request.
Chromium fixtures also cover met, missed and not-reached outcomes, a select's
target resolved from its selector while focus stays elsewhere, a navigating
click, and a touch session's taps.

## Patterns and heatmaps

`GET /api/v1/tests/{id}/archive/browser-interactions` reads a finalized run's
interaction evidence (`browser_interaction_patterns.v1`). Viewer access is
enough. The run report shows the result under **Browser evidence →
Interaction patterns**. A run without interaction evidence answers
`not_captured`. The response states its thresholds and coverage:
interactions, records, dropped and incomplete evidence, counts by provenance,
gestures mapped, and gestures left out by reason. Nothing is replayed or
injected.

Patterns are found per document, in capture order:

| Pattern | Rule |
| --- | --- |
| `scripted_repetition` | At least 3 pointer gestures within 2 s and 32 CSS px of the first, on one route and layout, when any of them is scripted or of unknown provenance. |
| `repeated_gesture` | The same, when every gesture was recorded with consent. It is the only pattern that can suggest human frustration. |
| `retry` | A failed gesture followed by gestures of the same action, up to the first that succeeded. Retries never count as repetition. |
| `dead_interaction` | A gesture with an authored outcome (its post-action `sync`) observed as `missed` within its deadline. An unchanged page alone is never one. |
| `unexpected_navigation` | A gesture that declared an in-page outcome (`element_visible`, `dom_stable` or a pause) but navigated away. |

A double click is one declared gesture, so it is never a repetition. Each
pattern lists its route, layout, action, count, window and up to 16
interaction IDs.

Heatmaps place each pointer gesture's first dispatched point on an abstract
64×64 grid of its viewport. One map is kept per sanitized route, layout
revision, viewport size and orientation, engine and device profile, so
incompatible layouts never merge. Engine and profile come from the gesture's
source generation in the run's document evidence, and are `unknown` without
it. Keyboard and form gestures fill a per-map table of target roles and
hashed labels, never positions.

Some gestures are counted, by reason, instead of mapped:
- those with an unavailable reason (`child_frame_unobserved`,
  `document_changed`, `synthetic_dispatch`, `page_state_unavailable`);
- those without points or without a viewport;
- those outside the viewport;
- those beyond 64 maps.

At most 200 patterns are kept, and patterns are found among the first 100,000
interactions (`interactions_truncated` beyond that). Screenshot overlays are
not produced. Incomplete journeys are E5's journey accounting
(`/archive/journeys`), not a pattern here.
