# Replay capture omission evidence — 2026-10-21

This BE-B slice retains bounded native capture omissions in final replay reports after protocol teardown. It supports omission-only providers, uses the existing closed reason/count validation, and turns malformed or panicking providers into the existing safe unavailable-status reason. Empty evidence remains absent and does not establish capture coverage. Replay success and progress-event ordering are unchanged.

Report clones own their count maps. Terminal recovery preserves the serialized counts exactly. Generated OpenAPI and TypeScript contracts and both hand-maintained replay interfaces include the optional field.

Requested native capture suppressed by resolved runtime secrets emits `browser_har_unavailable_har_runtime_secrets` once per context. Suppression opens no writer and creates no raw artifact. It is a policy decision, not a capture failure. Capture-off remains quiet. No secret values are included in the warning.

## Validation and review

The focused regressions pass, including successful and failed actions, teardown ordering, omission-only providers, malformed reasons, provider panic, empty evidence, clone independence, retained recovery and secret-suppression behavior. The affected engine, recordbridge and browser race suites pass. Targeted API recovery/generated-contract checks, dashboard typecheck and lint, and documentation checks are recorded under `/private/tmp/apex-browser-durable-backend/` with the `omission-` prefix. The full Go/private-HAR import-only gate passes: 36,261 test/package pass events, 56 skips and no failures across 168 passed packages; the API package completed in 946.783 seconds. The supplied private HAR import matches every stable prior summary field, excluding only the timestamp-sensitive script hash.

The retained Salesforce home-page/link-click recording was replayed using its existing logical selector bindings. The first navigation returned HTTP 403; its final report retained the expected safe suppression warning and no capture-failure count. This does not qualify a successful external journey or native durability. The existing editor still displays all 11 actions and the valid duplicate-name warning. Browser screenshot: `output/playwright/replay-recovery-20261021/.playwright-cli/page-2026-10-21T20-48-46-932Z.png`. The owned verification browser was closed. Private HAR targets were not replayed.

Review found and fixed the missing hand-maintained client fields. A subsequent review checked teardown, progress-versus-terminal publication, current lease fences, clone ownership, recovery serialization and generated schema propagation, with no further findings. The final review and validation round found no remaining issues, including low-severity findings. External native/crash/multi-host/cloud and reference-host qualification remain open.
