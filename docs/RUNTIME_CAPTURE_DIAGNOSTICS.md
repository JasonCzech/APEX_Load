# Imported runtime settings and HTTP capture

A saved source value can be dormant: the selected think-time/pacing mode, an
explicitly disabled parent, a different active profile, or the absence of a
source operation can make it unused. Every selected runtime row retains its
value and mapping explanation. Optional `reason` metadata distinguishes
`disabled_feature`, `unused_mode_value`, `superseded_setting`,
`protocol_mismatch`, `operation_absent`, `source_tool_metadata`,
`apex_managed_behavior`, and `unverified_semantics`. Zero alone is not evidence
of inactivity. Unverified `DisableAED` and `AedUtf8Values` remain review-required.

For native HTTP-family scripts, `EnableSnapshotsInReplay=1` imports as
`config.results.message_detail` with `mode: sampled`, `sample_rate: 1`,
`max_items: 128`, `max_item_bytes: 1048576`, and `max_total_bytes: 16777216`.
Explicit zero selects `errors` and does not change the independent
`config.error_handling.snapshot_on_error` policy. `SaveSnapshotResources`
imports as optional `include_resources`; explicit false is preserved. Missing
or conflicting capture selectors do not enable capture.

This policy applies to debugger replay and load-test archives. Debugger reports
enforce the configured snapshot item and byte limits; exhausted capture budgets
omit snapshots with `not_retained` evidence state while execution continues.
Switching the debugger to failures-only capture preserves resource preferences
and retention limits. Load-test launches
still require the existing project `successful_message_detail` feature flag
for sampled/adaptive capture. It captures only
resources the replay already fetches, including cached and failed exchanges.
It does not turn on resource discovery, fetch additional URLs, or affect outcome
policies. Captured resources appear beneath the parent action snapshot.

Resources share the parent evidence envelope and operator-owned limits. Resource
capture additionally caps a parent at 80 entries and the smaller of one quarter
of its byte envelope or 256 KiB. Text bodies exceeding 32 KiB and opaque binary
bodies are omitted, rather than retaining unredacted prefixes. Headers are
bounded and credentials are redacted; runtime secret redaction also applies.
`response_body_state`, `truncated`, and `omitted_resources` distinguish missing
or limited evidence from empty responses. Debugger and distributed transport
limits can further reduce retained evidence. An additional 16 MiB process-wide
in-flight resource-evidence ceiling omits optional evidence on exhaustion; it
does not compete with the response-reader budget required for executing requests.

The import is partial parity: these are bounded APEX exchange diagnostics, not
LoadRunner snapshot files, browser screenshots, or complete local resource
mirrors. Existing scripts are unchanged; re-import to a new draft to obtain new
mappings. Diagnostic policies can be edited in script settings, the import
review, or scenario/group runtime settings. Scenario `results` overrides replace
the whole section; omission inherits the existing policy.

Regression fixture: `pkg/convert/enterprisearchive/testdata/acc_runtime_diagnostics`
is a sanitized runtime-profile twin of acc_wls_reg_1.zip (VuGen 24.3), with synthetic C actions. Runtime keys and selectors are preserved; proxy credentials, network identities and user agents are replaced; C source is reduced to synthetic examples of its operation families. No customer data or executable customer workflow is included.

A local Apple M4 microbenchmark of one small resource's collector (Go 1.26.5,
2026-09-23) measured approximately 16.9 µs, 2,351 allocated bytes and 40 allocations
with capture enabled; the disabled path measured about 10 ns with no allocations.
This isolates diagnostic collection and redaction, not network latency or
end-to-end load capacity. Reproduce with:

```sh
go test ./pkg/protocol/http -run '^$' -bench BenchmarkResourceSnapshotCapture -benchmem
```

## Validation (2026-09-23)

- `go test ./...`: passed.
- Resource-capture HTTP tests with `-race`: passed.
- Dashboard type checking, lint, 419 unit-test files / 4,449 tests, and embedded build: passed.
- Focused browser workflows: three passed in each of dev-admin and local-auth. They cover fresh import explanations, explicit-false settings persistence, and preservation of an older draft's saved classification.
- Fresh conversion of the original archive confirms sampled capture at rate 1, resource inclusion, and independent error snapshots remaining disabled. `DisableAED=0` stays review-required. Conversion did not execute the customer script.

The required full dashboard cycle also exercises unrelated workflows. Its dev-admin
profile reported 122 passes, four skips and nine failures. The failing tests concern
older dashboard/navigation locators, workspace creation/scoping, and source-mapping
review persistence; those test names also fail in earlier local artifacts. The
repository documentation checker additionally reports existing audit links and
unindexed documents outside this change; this guide is included in the index.

The full local-auth profile completed with 50 passes, one skip and two failures:
`workflow-local-auth.spec.ts` (the old “Current project” locator) and
`workflow-script-sidebars.spec.ts` (source-mapping review persistence). Thus
`npm run test:cycle` was run but is not green: 11 workflow failures across the
two profiles. The three feature-specific workflows passed in both profiles.

## Follow-up code review

Two review passes corrected five defects:

- A full error-snapshot store could substitute a previous action's snapshot in a
  debugger event. Replay now substitutes only a snapshot added for that action
  and preserves a streamed child's own failure evidence.
- Resource metadata could be shortened without a truncation marker. Metadata
  loss now sets `truncated`, and a removed body is marked `not_captured`.
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

The follow-up `go test ./...` run completed with two failures outside these
changes: `desktop/TestSecureWriteGrantedFileFromEnforcesLimitBeforePublish`
and `pkg/api/TestRunTestPanicBackstopWaitsOutASlowDurableWriteHoldingTheRunnerLock`.
The desktop test and full desktop package passed reruns; the API test passed
three consecutive isolated runs. This does not turn the failed full run into a
passing run. The dev-admin browser profile completed with 124 passes, four skips
and the same nine previously observed workflow failures.
The local-auth profile completed with 51 passes, one skip and the same two
previously observed failures. The required dashboard cycle therefore remains
non-green with 11 existing workflow failures; all feature-specific checks passed
in both profiles.
