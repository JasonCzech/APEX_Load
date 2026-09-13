# Script Debugger Guide

The Script Editor debugger runs a DSL script through the controller replay engine and exposes a replay session that can be inspected, paused, stepped, stopped, and exported.

## Author and Debug workspace

The left outline navigates Init, Actions, Teardown, parameters/data, and import assets. Action names are searchable; expand a container to select a nested step. Bulk action operations live under **Bulk action tools** in the center Action List.

The right inspector follows the selected item. Action properties are grouped into **Request**, **Correlation & checks**, **Timing**, and **Metadata**. Large HTTP request bodies open in the center workspace, where selection-based parameterization remains available. Parameter summaries link to the single **Parameter Data** editing workspace; usage links open the correct location in full-script source.

At narrow widths, use **Outline**, **Editor**, and **Inspector** to select a pane. Browser Debug keeps its integrated evidence in **Editor**, so it offers Outline and Editor. Debug action navigation remains available whether the logs/problems dock is expanded or collapsed. Arrow keys move focus through the visible action rows; Enter selects the focused action.

**Debug options** shows temporary session overrides and their count. Review logging, think-time handling, start-paused behavior, and seed variables before starting a replay. Reset restores debugger defaults; these options do not modify saved Runtime Settings. **Checks & readiness** remains available while inspecting an item and distinguishes static checks from runtime prerequisites and recorded replay evidence.

Selecting a parameter in Debug shows its current definition and any explicitly captured parameter value for the selected event. A seed or extracted variable with the same unqualified name is not parameter evidence. Missing captures are labeled **Not captured**; values are masked until revealed, and evidence from a different draft is labeled separately.

## Routes

Use `/api/v1/replay/sessions/{session_id}` for session reads and transport controls. The older `/api/v1/script-debug/sessions/{session_id}` routes are deprecated compatibility aliases. Browser replay canary sessions still use `/api/v1/browser-replay/sessions/{session_id}`.

Start routes:

- Saved script: `POST /api/v1/scripts/{script_id}/debug/sessions`
- Draft script: `POST /api/v1/scripts/debug/sessions`

Session controls:

- `GET /api/v1/replay/sessions/{session_id}`
- `POST /api/v1/replay/sessions/{session_id}/pause`
- `POST /api/v1/replay/sessions/{session_id}/resume`
- `POST /api/v1/replay/sessions/{session_id}/step` with optional `{"mode":"into"|"over"|"loop_end"}`
- `POST /api/v1/replay/sessions/{session_id}/stop`
- `PATCH /api/v1/replay/sessions/{session_id}/breakpoints`
- `PATCH /api/v1/replay/sessions/{session_id}/options`
- `GET /api/v1/replay/sessions/{session_id}/export`

## Breakpoints And Stepping

Breakpoints target a phase and action index. Optional `request_index` targets an `http_sequence` child, and optional `step_path` targets structured control-flow leaves such as `loop[0]`, `then[0]`, or `requests[1]`.

Step modes:

- `into`: run one leaf action, then pause.
- `over`: run through the current container or sub-request scope, then pause before the next action outside it.
- `loop_end`: run to the end of the nearest enclosing control loop, then pause before the next action outside it.

## Break On Failure

`break_on_failure` pauses immediately after a failed action, before the replay engine applies the script failure policy. This lets an operator inspect request, response, variables, condition evidence, and logs before choosing resume, step, or stop.

## Session Retention

Debug/replay session managers keep sessions in memory and prune them with safe defaults:

- Terminal session TTL: 2 hours
- Running idle cancel: 30 minutes
- Maximum retained sessions per manager: 250

Environment overrides:

- `APEXLOAD_REPLAY_SESSION_TERMINAL_TTL` or `APEXLOAD_REPLAY_SESSION_TERMINAL_TTL_SECONDS`
- `APEXLOAD_REPLAY_SESSION_IDLE_CANCEL` or `APEXLOAD_REPLAY_SESSION_IDLE_CANCEL_SECONDS`
- `APEXLOAD_REPLAY_SESSION_MAX` or `APEXLOAD_REPLAY_SESSION_MAX_SESSIONS`

Setting very high values effectively disables pruning for local diagnostics, but production deployments should keep bounded retention.

## Logs And Exports

`log_level` accepts `trace`, `debug`, `info`, `warn`, or `error`. Lower-severity messages are filtered before they enter the session logs and raw output.

Exports default to JSON and include the current session state. `?format=log`, `?format=txt`, or `?format=text` returns raw debugger output as text.

## Operating Notes

Pause and break-on-failure waits are signal-driven: replay goroutines block until resume, step, stop, or terminal cancellation wakes them. Stop is idempotent for existing sessions, so clients can retry a stop request safely.

### Source Mapping review

The source and destination comparison stays at an equal 50/50 split. **Focus source**
or **Focus destination** temporarily expands one pane; **Return to comparison**
restores both. **Expand workspace** hides the surrounding sidebars.

Start with **Mapped context** to inspect the selected source span and its destination
with nearby lines. Original document line numbers remain visible. **Full document**
shows the complete captured file and destination document. Selecting a mapping again
recenters the code inside its panes. **Show mappings** opens the searchable, sortable
mapping list; arrow keys and Home/End select rows. Bracket keys move between
unacknowledged review or unsupported items when focus is in Source Mapping.

Conversion status, runtime support, and review annotations are separate. Record an
**Acknowledged** or **Needs changes** decision with a note, then save the script.
The annotation retains the current reviewer identity, time, import revision, and
destination content identity. It does not grant execution approval.

Saved mappings follow uniquely matching content through reordering. Edited, missing,
or duplicate targets are explicitly unresolved; inspect the current draft and use
**Re-link to this destination** to establish a new connection. Re-linking clears the
previous decision. Review decisions become stale when their import or destination
revision changes.

**Review baseline** is the saved reference captured when review bindings were first
established. For an older script, it is not proof of the original generated import.
**Current draft** includes current edits. The source file picker includes every
captured file, including files without mappings; omitted and truncated files are
listed separately. **DSL without source origin** opens the affected destination
items for inspection.

Copy controls distinguish mapped excerpts from whole documents. **Export filtered
mappings** exports the current result set, conversion/runtime status, current target
identity, and review annotations, including stale decisions.
