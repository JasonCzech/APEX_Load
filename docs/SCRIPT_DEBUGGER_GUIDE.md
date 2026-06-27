# Script Debugger Guide

The Script Editor debugger runs a DSL script through the controller replay engine and exposes a replay session that can be inspected, paused, stepped, stopped, and exported.

## Routes

Use `/api/v1/replay/sessions/{session_id}` for session reads and transport controls. The older `/api/v1/script-debug/sessions/{session_id}` and `/api/v1/browser-replay/sessions/{session_id}` routes remain compatibility/debug surfaces.

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
