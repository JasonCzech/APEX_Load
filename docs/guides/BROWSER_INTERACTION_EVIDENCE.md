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
  otherwise.
- **Dispatched points.** For pointer gestures, the CSS-pixel points the page
  actually received, taken from trusted `pointerdown`, `pointerup` and
  `pointermove` events: each press for clicks, taps and checks, the hover
  position, and the start and end of a drag or swipe, with the pointer type.
  The page's own synthetic events are ignored. Gestures inside a child frame
  are recorded with `frame_scope: child_frame` and no points
  (`child_frame_unobserved`); no frame transform is inferred.
- **Keyboard targets.** For key, fill, select, set-files and check
  gestures, the focused element's role and the SHA-256 of its sanitized
  label (aria-label, associated label, placeholder, name or title,
  lowercased, digits replaced by `#`, whitespace collapsed, at most 64
  characters). No coordinates, and never the element's value.
- **Context.** Viewport size, scroll offsets, device scale, the sanitized
  route (the path only, with numeric, UUID, long hexadecimal and
  digit-heavy segments collapsed to `:id`) and a layout revision: a
  fingerprint of a breadth-first skeleton of at most 1,024 elements (tag,
  a safe role token and the box quantized to 32 CSS pixels), with no text
  or other attribute values.
- **Outcome.** The declared outcome is the action's `wait_for` condition and
  its deadline; the observed result is `met` or `missed`. Actions without
  `wait_for` record `not_declared`.

The page state is read once, after the action and its own wait, by one page
evaluation of a script installed in every document of the context. A record
the page could not supply is kept with `page_state_unavailable`.

## Bounds and transport

A document keeps at most 1,024 interactions and 64 KiB of them, and a
session holds at most 4,096 before handing them over; anything beyond is
counted as dropped and marks the evidence incomplete. Sessions hand their
interactions to the run archive at every iteration boundary and at
teardown. A run archive keeps them in its own `browser_interactions.jsonl`
sidecar, at most 64 MiB; on a generator the sidecar also never takes the
quarter of the run archive limit reserved for required evidence.

A generator declares its sidecar as transferable evidence
(`browser_interactions_artifact`). At finalization the controller fetches it
from the live generator, verifies its size, SHA-256 and every record, and
records an `interaction_evidence` receipt. It then merges all sources into
the run archive's sidecar (`browser_interactions_file`, with the record count
and inventory entry `browser_interactions_v1`) and acknowledges the
generator after publication, exactly as for
[native HARs](BROWSER_HAR_WORKING_OWNERSHIP.md#run-archive-transfer-and-receipts).
A source that cannot be transferred, or whose records differ from its
declaration, marks `browser_interactions_incomplete`.

## Qualification

Chromium and WebKit fixtures check that recorded click and double-click
points match Playwright's own element boxes within one CSS pixel, that hover
points fall inside the hovered element, that a page's synthetic pointer
event is not recorded, that the route and label are sanitized, that input
values never appear, that the layout revision changes only when the layout
does, and that an independent request counter sees no added request.
