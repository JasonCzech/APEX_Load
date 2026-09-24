# Execution Results design QA

Status: Historical QA log (moved from the repository root 2026-09-24).

final result: passed

## Findings

No actionable P0/P1/P2 visual differences remain. Completed, failed, and aborted runs use the existing Results shell and its five sections. Missing archives are an evidence state within that shell. Historical execution links resolve to Results while live runs retain their execution controls.

## Visual truth and comparison evidence

Source: the existing APEX Load Results page, dark theme, `web wiki` run, Summary selected. This is the approved product visual reference, rather than a separate mockup.

Source visual truth path:
`/Users/jasonczech/.codex/visualizations/2026/09/10/01a08b79-4115-75d2-9cec-f1d6a0113bd9/execution-implementation/results-reference.png`

Implementation screenshot paths in the same directory:

- `completed-summary.png`: same run, theme, viewport and Summary state as the reference.
- `completed-execution.png`: new Execution section, including visible keyboard focus and captured load history.
- `failed-execution.png`: actual `jms-rel-run`, including its recorded controller restart cause and missing archive notice.
- `failed-summary.png`: the failed run's Summary and investigation actions.

The desktop source and implementation screenshots are 1422 × 800 pixels at the same browser content size. They were compared at matching image dimensions without density scaling. Source and implementation were opened together in a single comparison input; the matched Summary views establish shell fidelity. Execution and failed states intentionally have different content and vertical proportions, so those comparisons assess consistency rather than pixel identity.

Responsive evidence: `dashboard/test-results/dev-admin/workflow-execution-results-f9b60-vigation-without-an-archive/execution-narrow.png`, a 760 × 900 screenshot from the aborted-run workflow. The cards wrap and the five tabs remain reachable with no document overflow. This is a separate responsive check, not a density-normalized comparison against the desktop reference.

Full-view screenshots made the typography, controls, borders and spacing legible; a separate magnified crop was unnecessary. Group configuration, raw manifest disclosure, historical diagnostic timestamps and tab focus were additionally inspected in the browser.

## Required fidelity surfaces

- **Typography:** existing Results font family, title hierarchy, label weights and secondary-text treatment are retained. Long notices wrap without truncation. Dates and metrics use readable labels and explicit missing-value text.
- **Spacing/layout:** shared toolbar, panel padding, border radii and tab alignment match the source. Execution uses a compact facts row, one load timeline, one group table and an event section. Tables and raw records scroll locally; responsive cards wrap.
- **Colors/tokens:** existing surface, border, text and accent tokens are reused. Failure uses a restrained red heading and edge; missing evidence remains neutral. Zero transaction errors cannot produce a successful execution badge.
- **Image/assets:** existing APEX logo and navigation assets remain unchanged and sharp. No generated or replacement visual assets were introduced. The load chart renders recorded data with the existing Recharts library.
- **Copy/content:** configured load and duration are distinguished from observed elapsed time and peak load. The original plan is called immutable only when provenance explicitly supports that claim. Missing timestamps display “Not recorded”; retained samples are not presented as whole-run KPIs or current system health.

## Comparison and correction history

1. The audit identified three disconnected destinations and missing failure context. The implementation moved terminal execution into Results and surfaced the recorded reason above its tabs.
2. Initial browser review exposed an irrelevant metric-population selector in Execution. It is now limited to analysis sections; `completed-execution.png` shows the corrected hierarchy.
3. Browser interaction checks exposed focus loss when Results section changes remounted the route. Report section/lens changes now preserve the page instance. The final screenshot shows the Execution focus outline, and browser tests cover End-key navigation.
4. Actual failed-run inspection exposed retained events stuck loading under StrictMode. Request invalidation now resets the obsolete in-flight reference. The browser shows the retained create/start events, with a dedicated regression test.

## Verification and limits

The required dashboard cycle was run: type checking, lint, unit tests, a fresh embedded build, and both workflow profiles. All 4,051 unit tests passed. Browser expectations for old archive destinations were updated to open the Results Evidence disclosure before downloading; the final dev-admin rerun passed all 85 tests. Local-auth passed all 10 tests. Verification logs: `/tmp/execution-cycle-verified.log` and `/tmp/execution-workflows-final.log`.

Manual checks covered completed and failed legacy URLs, retained evidence, expandable configuration, diagnostic timestamps, common navigation, and keyboard focus. Automated workflows cover archive-backed analysis and COMPLETE/FAILED/ABORTED states without archives. No Go source changed.

The existing records do not establish original ramp schedules or complete telemetry for every run. The UI explicitly preserves those limitations. It does not reconstruct missing measurements or infer a successful workload from retained counters.

## Implementation checklist

- [x] Use one Results shell for terminal run states.
- [x] Surface recorded failure reasons and evidence availability.
- [x] Separate configuration, observed measurements and acceptance.
- [x] Preserve shared styling and keyboard navigation.
- [x] Inspect desktop, narrow and missing-evidence states.

No blocking open design questions or follow-up polish findings remain.

## Code review follow-up

The implementation review corrected three P2 issues:

- Missing fields in an immutable configuration no longer fall back to mutable test values. Header and facts display missing values explicitly.
- Manifest request failures no longer also claim that no lifecycle history was retained. Retry recovery is covered by a component regression test.
- The execution chart uses a numeric time axis, sorts retained samples, and rejects invalid dates and negative load values. Irregular timestamps retain their actual elapsed spacing, and an unavailable configured reference is omitted.

The corrected chart was opened in the browser at the original desktop viewport. Screenshot: `/Users/jasonczech/.codex/visualizations/2026/09/10/01a08b79-4115-75d2-9cec-f1d6a0113bd9/execution-implementation/review-completed-execution.png`. No layout regression was observed. Regression coverage includes incomplete immutable records, failed event retrieval and retry, irregular sampling, out-of-range timestamps, and input preservation.

Post-review validation: `npm run test:cycle` passed in full: type checking, lint, 4,054 unit tests, embedded build, 85 dev-admin workflows and 10 local-auth workflows. Log: `/tmp/execution-review-cycle.log`. `git diff --check` passed.
