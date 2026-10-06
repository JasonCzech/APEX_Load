var e=`# Telemetry budget policy receipts

Status: immutable policy-receipt slice reviewed and qualified with the full Go suite, dashboard cycle and actual workflows in both authentication profiles. This delivers the first part of [TEL-1](../FEATURE_COMPLETION_SPEC.md#tel-1-immutable-policy-and-time-coverage). Full TEL-1–TEL-4 remain open for qualified clocks, source-loss evidence, time coverage, telemetry verdicts and complete reporting.

When managed execution commits INITIALIZING, the controller stores complete initial configuration and resolved telemetry budgets in that same Start transaction before asynchronous runtime staging. Pending admission does not fabricate a receipt. Candidates stay private while SQL waits. Failed writes do not pin settings; successful retries adopt the write-once durable winner, including legacy absent-policy receipts. Later system settings do not rewrite retained run budgets.

The receipt identifies apexload.telemetry_policy.v1, fixed policy system-telemetry-budgets revision 1, semantic SHA-256, UTC capture time and legacy-mixed-diagnostic.v1. Revision 1 identifies the fixed contract, not a global settings edit counter. Capture time is provenance excluded from the digest. Five stages preserve sample age, generator processing, transport, controller fan-out and browser render budgets. Nil resolves existing defaults; explicit zero disables a stage. The existing 15-second sustained window is retained. Actual controller cadence is configured whole seconds for local execution or the existing one-second distributed coalescing gate; it does not certify generator/browser cadence.

Live status, manifests, archive analysis and report execution details show the receipt. Dashboard WebCrypto verifies exact semantic identity before displaying available evidence; pending, malformed or unverifiable receipts remain unavailable, with stale completion fenced. Zero capture time is invalid even with a matching digest; nonzero nanosecond precision is retained. A matching digest detects content mismatch but does not attest origin. Live badges describe observed age or separate controller cumulative drop observations, not exact run loss or a health verdict. Archive interval classifications are explicitly legacy mixed diagnostic; missing summary/counts are n/a, explicit zero stays zero.

\`\`\`bash
apexctl analysis telemetry-policy --id RUN_ID --mode live
apexctl analysis telemetry-policy --id RUN_ID --mode archive
\`\`\`

Live mode reads the authorized run manifest; archive mode reads its report. Optional --api and --project retain existing conventions. Unsupported filters, excessive bytes, redirects, project mismatch and malformed canonical fields/digests are refused. Output is a closed projection; raw controller errors and credential values are not printed. Old evidence has no invented policy identity.

[Review and qualification](../qa/reviews/feature-completion-telemetry-policy-2026-10-05/README.md) separates actual SQL/client checks from remaining scope. Source epochs/windows, clock uncertainty, gap-aware union coverage, exact/lower-bound loss, stage distributions, policy segments/comparisons, independent advisory/fail-CI verdicts and complete exports remain open. The under-two-second figure remains a design target pending declared hardware/workload acceptance.
`;export{e as default};