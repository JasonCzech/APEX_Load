var e=`# Script Debugger Guide

The Script Editor debugger runs a DSL script through the controller replay engine and exposes a replay session that can be inspected, paused, stepped, stopped, and exported.

## Author and Debug workspace

The left outline navigates Init, Actions, Teardown, parameters/data, and import assets. Action names are searchable; expand a container to select a nested step. Bulk action operations live under **Bulk action tools** in the center Action List.

The right inspector follows the selected item. Action properties are grouped into **Request**, **Correlation & checks**, **Timing**, and **Metadata**. Large HTTP request bodies open in the center workspace, where selection-based parameterization remains available. Parameter summaries link to the single **Parameter Data** editing workspace; usage links open the correct location in full-script source.

At narrow widths, use **Outline**, **Editor**, and **Inspector** to select a pane. Browser Debug keeps its integrated evidence in **Editor**, so it offers Outline and Editor. Debug action navigation remains available whether the logs/problems dock is expanded or collapsed. Arrow keys move focus through the visible action rows; Enter selects the focused action.

**Debug options** shows temporary session overrides and their count. Review logging, think-time handling, start-paused behavior, and seed variables before starting a replay. Reset restores debugger defaults; these options do not modify saved Runtime Settings. **Checks & readiness** remains available while inspecting an item and distinguishes static checks from runtime prerequisites and recorded replay evidence.

Selecting a parameter in Debug keeps its authored definition separate from runtime receipts. **Current paused state** shows whether the engine has prepared or actually resolved the parameter, its update policy, and its last actual-use receipt. Each-occurrence values describe the last resolution; they do not predict the next value or prove request acceptance. A seed or extracted variable with the same unqualified name is not parameter evidence. Historical values require an explicit history selection, and evidence from a different draft is labeled separately.

## Investigating a run

The transport shows Run, Pause or Continue according to the session state. **Follow execution** follows new evidence; selecting an older execution turns following off. **Go to current action** returns to the current execution position. The selected evidence and the execution cursor are labeled separately. After a failure, Continue still follows the script error policy: stop-on-error ends the iteration rather than running subsequent actions.

**Problems** separates runtime failures from validation/editor issues. Select a runtime failure to open that exact action and iteration. **Timeline** groups completed executions and supports action search, failure filtering, duration, and evidence navigation. Session lifecycle messages remain available in a disclosure. **Engine logs** includes a **Raw text** view; filtered-out logs are distinguished from uncaptured logs.

**Variables** defaults to **Current paused state**. It displays actual VUser bindings, parameter runtime receipts, original source-row numbers, and active execution frames only while the engine holds the reported boundary. Repeated inspection does not consume a parameter reference, allocate a row, advance random selection, or evaluate a template. Empty arrays clear previous bindings; empty values, redacted values, omitted values, and bounded detail are distinguished.

A pause request while an action is executing remains unavailable until the engine reaches a held boundary. Continue, Step, Stop, terminal completion, lost session ownership, and cancellation revoke current values. Completed-run values remain historical. Choose **Retained history** or **Selected execution (after)** explicitly to use searchable, pinned watches and the existing variable timeline; reconstructed seed/extraction history cannot fill missing current values.

**Active runtime frames** links to the exact unchanged authored source when it can be resolved. Original row numbers are 1-based, exclude the header, and retain shard/allocation offsets. Frames identify the current phase, nested control flow, loop iteration and isolated parallel branch; they do not expose private per-frame storage. Concurrent HTTP groups capture only the parent before preparation/dispatch and after all children join. Current values are available only while that hook actually holds a pause; an after-action hold is failure-only.

## Capture and run profiles

**Evidence capture** is separate from log verbosity. Diagnostic capture requests successful HTTP/SOAP/Java HTTP message detail, bounded to 100 messages, 64 KiB per message and 4 MiB total; backend redaction and retention limits still apply. Browser diagnostic capture uses per-action screenshots. Other protocols use their native evidence support. **Failures only** reduces capture; **Use saved Runtime Settings (custom)** leaves the script's capture settings intact. These session overrides do not change the saved script.

**Effective run configuration** identifies the target, draft/saved status, capture and failure policy, plus parameter selection controls. Named run profiles store only execution controls in the browser: they exclude seed-variable values, credentials, target URLs, and captured evidence.

**Recent runs** retains up to five completed runs in the current editor session, with protocol-appropriate evidence. Pin a comparison baseline to inspect differences in outcomes, assertions, resolved requests, responses, variables and configuration. This is temporary editor history; use **Export session** before closing. The export includes the captured run controls and retained evidence; seed-variable names are exported without their original input values.

## Routes

Use \`/api/v1/replay/sessions/{session_id}\` for session reads and transport controls; it is the canonical route family. The older \`/api/v1/script-debug/sessions/{session_id}\` routes are still registered but marked deprecated in \`swagger.yaml\`. Browser replay canary sessions still use \`/api/v1/browser-replay/sessions/{session_id}\`.

Start routes (all registered; the \`replay/sessions\` routes are preferred, and the \`debug/sessions\` routes are compatibility wrappers over the same session engine):

- Saved script: \`POST /api/v1/scripts/{script_id}/replay/sessions\` (compat: \`POST /api/v1/scripts/{script_id}/debug/sessions\`)
- Draft script: \`POST /api/v1/scripts/replay/sessions\` (compat: \`POST /api/v1/scripts/debug/sessions\`)

Session controls:

- \`GET /api/v1/replay/sessions/{session_id}\`
- \`POST /api/v1/replay/sessions/{session_id}/pause\`
- \`POST /api/v1/replay/sessions/{session_id}/resume\`
- \`POST /api/v1/replay/sessions/{session_id}/step\` with optional \`{"mode":"into"|"over"|"loop_end"}\`
- \`POST /api/v1/replay/sessions/{session_id}/stop\`
- \`PATCH /api/v1/replay/sessions/{session_id}/breakpoints\`
- \`PATCH /api/v1/replay/sessions/{session_id}/options\`
- \`GET /api/v1/replay/sessions/{session_id}/export\`

## Breakpoints And Stepping

Breakpoints target a phase and action index. Optional \`request_index\` targets an \`http_sequence\` child, and optional \`step_path\` targets structured control-flow leaves such as \`loop[0]\`, \`then[0]\`, or \`requests[1]\`.

**Breakpoint controls** can disable all breakpoints without deleting them. A hit count pauses on exactly that eligible visit (1-based); leaving it empty pauses on every eligible visit. An iteration condition on a nested action targets the innermost loop, zero-based. Editing the breakpoint set resets its hit counters.

**Run to selected action** executes prerequisites from the beginning when no session is active. During a paused session it runs forward to the next matching execution. It adds a one-shot breakpoint, which is consumed when reached, while preserving ordinary breakpoints. Earlier breakpoints, failures, and the script's failure policy can stop execution before the target. It does not replay a request in isolation or reverse execution.

Step modes:

- \`into\`: run one leaf action, then pause.
- \`over\`: run through the current container or sub-request scope, then pause before the next action outside it.
- \`loop_end\`: run to the end of the nearest enclosing control loop, then pause before the next action outside it.

### Editor keys

These keys work while the script editor is open, and \`?\` lists them with the rest:

| Keys | Action |
| --- | --- |
| \`Cmd/Ctrl\` + \`S\` | Save script |
| \`F5\` | Run the debugger, or continue a paused session |
| \`Shift\` + \`F5\` | Stop the debugger |
| \`F10\` | Step (\`into\`) |
| \`F11\` | Step over the current block (\`over\`) |
| \`Shift\` + \`F11\` | Finish the current loop (\`loop_end\`) |
| \`Cmd/Ctrl\` + \`B\` | Toggle a breakpoint on the selected action |

## Break On Failure

\`break_on_failure\` pauses immediately after a failed action, before the replay engine applies the script failure policy. This lets an operator inspect request, response, variables, condition evidence, and logs before choosing resume, step, or stop.

## Session Retention

Debug/replay session managers keep sessions in memory and prune them with safe defaults:

- Terminal session TTL: 2 hours
- Running idle cancel: 30 minutes
- Maximum retained sessions per manager: 250

Environment overrides:

- \`APEXLOAD_REPLAY_SESSION_TERMINAL_TTL\` or \`APEXLOAD_REPLAY_SESSION_TERMINAL_TTL_SECONDS\`
- \`APEXLOAD_REPLAY_SESSION_IDLE_CANCEL\` or \`APEXLOAD_REPLAY_SESSION_IDLE_CANCEL_SECONDS\`
- \`APEXLOAD_REPLAY_SESSION_MAX\` or \`APEXLOAD_REPLAY_SESSION_MAX_SESSIONS\`

Setting very high values effectively disables pruning for local diagnostics, but production deployments should keep bounded retention.

## Logs And Exports

\`log_level\` accepts \`trace\`, \`debug\`, \`info\`, \`warn\`, or \`error\`. Lower-severity messages are filtered before they enter the session logs and raw output.

Exports default to JSON and include the current session state. \`?format=log\`, \`?format=txt\`, or \`?format=text\` returns raw debugger output as text.

## Operating Notes

Pause and break-on-failure waits are signal-driven: replay goroutines block until resume, step, stop, or terminal cancellation wakes them. Stop is idempotent for existing sessions, so clients can retry a stop request safely.

Full debugger responses include a session incarnation, a decimal-string state revision, and a versioned full runtime-state replacement. Unavailable state has an explicit null snapshot. The client rejects foreign, stale, malformed and conflicting updates and resynchronizes through a trusted session read. Leaf WebSocket updates carry identity, revision and status without repeating binding maps. Revision exhaustion permanently disables current-state evidence for that session.

Runtime projection applies strict redaction before display limits and omits whole values that cannot be safely inspected. It bounds the combined receipt count to 256, frames and rows to 64 each, authored text to 4 KiB per field, and the snapshot to 64 KiB, with separate private inspection/work limits. A classification failure removes all runtime detail for the run, including older retained evidence and exports. These debugger receipts establish local replay behavior; they do not qualify a physical browser or prove Salesforce session fidelity.

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

## Imported runtime settings and HTTP capture

A saved source value can be dormant: the selected think-time/pacing mode, an
explicitly disabled parent, a different active profile, or the absence of a
source operation can make it unused. Every selected runtime row retains its
value and mapping explanation. Optional \`reason\` metadata distinguishes
\`disabled_feature\`, \`unused_mode_value\`, \`superseded_setting\`,
\`protocol_mismatch\`, \`operation_absent\`, \`source_tool_metadata\`,
\`apex_managed_behavior\`, and \`unverified_semantics\`. Zero alone is not evidence
of inactivity. Unverified \`DisableAED\` and \`AedUtf8Values\` remain review-required.

For native HTTP-family scripts, \`EnableSnapshotsInReplay=1\` imports as
\`config.results.message_detail\` with \`mode: sampled\`, \`sample_rate: 1\`,
\`max_items: 128\`, \`max_item_bytes: 1048576\`, and \`max_total_bytes: 16777216\`.
Explicit zero selects \`errors\` and does not change the independent
\`config.error_handling.snapshot_on_error\` policy. \`SaveSnapshotResources\`
imports as optional \`include_resources\`; explicit false is preserved. Missing
or conflicting capture selectors do not enable capture.

This policy applies to debugger replay and load-test archives. Debugger reports
enforce the configured snapshot item and byte limits; exhausted capture budgets
omit snapshots with \`not_retained\` evidence state while execution continues.
Switching the debugger to failures-only capture preserves resource preferences
and retention limits. Load-test launches
still require the existing project \`successful_message_detail\` feature flag
for sampled/adaptive capture. It captures only
resources the replay already fetches, including cached and failed exchanges.
It does not turn on resource discovery, fetch additional URLs, or affect outcome
policies. Captured resources appear beneath the parent action snapshot.

Resources share the parent evidence envelope and operator-owned limits. Resource
capture additionally caps a parent at 80 entries and the smaller of one quarter
of its byte envelope or 256 KiB. Text bodies exceeding 32 KiB and opaque binary
bodies are omitted, rather than retaining unredacted prefixes. Headers are
bounded and credentials are redacted; runtime secret redaction also applies.
\`response_body_state\`, \`truncated\`, and \`omitted_resources\` distinguish missing
or limited evidence from empty responses. Debugger and distributed transport
limits can further reduce retained evidence. An additional 16 MiB process-wide
in-flight resource-evidence ceiling omits optional evidence on exhaustion; it
does not compete with the response-reader budget required for executing requests.

The import is partial parity: these are bounded APEX exchange diagnostics, not
LoadRunner snapshot files, browser screenshots, or complete local resource
mirrors. Existing scripts are unchanged; re-import to a new draft to obtain new
mappings. Diagnostic policies can be edited in script settings, the import
review, or scenario/group runtime settings. Scenario \`results\` overrides replace
the whole section; omission inherits the existing policy.

Regression fixture: \`pkg/convert/enterprisearchive/testdata/acc_runtime_diagnostics\`
is a sanitized runtime-profile twin of acc_wls_reg_1.zip (VuGen 24.3), with synthetic C actions. Runtime keys and selectors are preserved; proxy credentials, network identities and user agents are replaced; C source is reduced to synthetic examples of its operation families. No customer data or executable customer workflow is included.

A local Apple M4 microbenchmark of one small resource's collector (Go 1.26.5,
2026-09-23) measured approximately 16.9 µs, 2,351 allocated bytes and 40 allocations
with capture enabled; the disabled path measured about 10 ns with no allocations.
This isolates diagnostic collection and redaction, not network latency or
end-to-end load capacity. Reproduce with:

\`\`\`sh
go test ./pkg/protocol/http -run '^$' -bench BenchmarkResourceSnapshotCapture -benchmem
\`\`\`

### Validation (2026-09-23)

- \`go test ./...\`: passed.
- Resource-capture HTTP tests with \`-race\`: passed.
- Dashboard type checking, lint, 419 unit-test files / 4,449 tests, and embedded build: passed.
- Focused browser workflows: three passed in each of dev-admin and local-auth. They cover fresh import explanations, explicit-false settings persistence, and preservation of an older draft's saved classification.
- Fresh conversion of the original archive confirms sampled capture at rate 1, resource inclusion, and independent error snapshots remaining disabled. \`DisableAED=0\` stays review-required. Conversion did not execute the customer script.

The required full dashboard cycle also exercises unrelated workflows. Its dev-admin
profile reported 122 passes, four skips and nine failures. The failing tests concern
older dashboard/navigation locators, workspace creation/scoping, and source-mapping
review persistence; those test names also fail in earlier local artifacts. The
repository documentation checker additionally reports existing audit links and
unindexed documents outside this change; this guide is included in the index.

The full local-auth profile completed with 50 passes, one skip and two failures:
\`workflow-local-auth.spec.ts\` (the old “Current project” locator) and
\`workflow-script-sidebars.spec.ts\` (source-mapping review persistence). Thus
\`npm run test:cycle\` was run but is not green: 11 workflow failures across the
two profiles. The three feature-specific workflows passed in both profiles.

### Follow-up code review

Two review passes corrected five defects:

- A full error-snapshot store could substitute a previous action's snapshot in a
  debugger event. Replay now substitutes only a snapshot added for that action
  and preserves a streamed child's own failure evidence.
- Resource metadata could be shortened without a truncation marker. Metadata
  loss now sets \`truncated\`, and a removed body is marked \`not_captured\`.
- Conflicting resource selectors could erase a valid all-exchange capture
  policy. Child conflicts now clear only the child preference; unresolved parent
  policies leave dependent source rows review-required.
- Switching the debugger to failures-only capture discarded resource preferences
  and limits. The override now preserves those authored fields.
- Debugger snapshots bypassed configured message-detail retention limits. Replay
  now applies item, per-item byte, and aggregate byte budgets without suppressing
  execution or its scalar result rows.

Each fix has regression coverage. The changed converter, engine and HTTP package
suites passed, as did dashboard type checking, lint, 4,450 unit tests and the
embedded build. The new failures-only resource-capture browser test passed in
both dev-admin and local-auth profiles.

The follow-up \`go test ./...\` run completed with two failures outside these
changes: \`desktop/TestSecureWriteGrantedFileFromEnforcesLimitBeforePublish\`
and \`pkg/api/TestRunTestPanicBackstopWaitsOutASlowDurableWriteHoldingTheRunnerLock\`.
The desktop test and full desktop package passed reruns; the API test passed
three consecutive isolated runs. This does not turn the failed full run into a
passing run. The dev-admin browser profile completed with 124 passes, four skips
and the same nine previously observed workflow failures.
The local-auth profile completed with 51 passes, one skip and the same two
previously observed failures. The required dashboard cycle therefore remains
non-green with 11 existing workflow failures; all feature-specific checks passed
in both profiles.
`;export{e as default};