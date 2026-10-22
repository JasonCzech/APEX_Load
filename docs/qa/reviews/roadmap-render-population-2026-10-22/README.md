# Publication identity and render population — 2026-10-22

This [TEL-A slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-a--durable-windows-and-measured-end-to-end-age) gives every live publication an identity that reaches the browser. It defines the eligible publication population and accounts each eligible publication for one reference client. A TEL-B consumer states the result in the run report. Before this, the advisory browser-render stage sampled what a visible tab happened to render. Hidden receipts, superseded callbacks and failed reports were unaccounted gaps.

## Contract

- **Identity.** Each live point a controller installs carries `telemetry.publication` (`TelemetryPublication`): the controller's publication `epoch` for the run, a `sequence`, `eligible`, and the oldest and newest collection times it includes.
  - **Epoch:** random per publishing recorder. Re-adoption by the same recorder (`adopted`) starts a new one.
  - **Collection times:** each is on its generator's clock, qualified as `sample_age_quality` says. They are omitted when the point reports no collection time.
  - The fleet's newest collection time is chosen on the same corrected scale as its oldest.
  - Only installed points get an identity: it is assigned under the same lock and guard that install the point, so a point a guard rejects gets none.
- **Eligibility.** A publication is eligible when it carries a collection no earlier publication carried.
  - **Distributed point:** ineligible only when every generator window it carries was already published (`distributedPublicationRepeatedLocked`, decided in the install guard before `publishedWindows` advances). A generator whose collection is not identified, an older peer or a batch without telemetry, is never proved repeated.
  - **Local point:** ineligible when it repeats the previous point's collection time.
  - Replayed and lifecycle-only envelopes are therefore excluded.
- **Client outcomes, best effort.** A reporting tab is one view of the run page. It reports the named publications it receives by identity:
  - `visible`, with its measurement, when rendered;
  - `superseded`, when a newer publication rendered first (`background` if that receipt was received or pending while hidden);
  - `background`, when the publication was received or painted while hidden.

  Further rules:
  - Unmeasured outcomes are sent only after the tab measured a visible render; those received earlier are reported with it.
  - Receipts logged before the one on screen when reporting starts are dropped, not reported. The on-screen receipt and later ones are kept.
  - A re-delivered publication replaces its unresolved receipt; one re-delivered after its first copy was reported is not reported again. The server counts a publication named twice in one report as rendered if either copy rendered.
  - Visible and unnamed samples spend the viewer's sample budget, and every report spends at least one; a named publication's unmeasured outcome spends none.
  - A malformed publication name refuses the report with 400.
- **Reference client.** The first tab whose report includes a visible render of a publication the controller still holds in the current epoch. It is keyed by the viewer's subject plus the tab identifier, so another viewer cannot report as it, and is exposed only as an opaque identifier.
- **Population** (`TelemetryRenderPopulation`, live as `telemetry.render_population` and archived as `telemetry_render_population` in the manifest; never on history or interval rows). Declared as `rendered_publications`.
  - Within the reference client's reported sequence range, every eligible publication is exactly one of: `rendered`, `superseded`, `unqualified` (by reason), or `unreported`.
  - **Running counters:** the range is accounted as it grows, so each read is constant-time. Eligible publications evicted past the range's end while it is open become `unreported` once the range passes them.
  - **Retention:** the ledger holds the newest 4,096 publications, but a publication is accounted before it leaves, so eviction loses no classification. A later report of an evicted publication counts as `unknown`.
  - `published_eligible`, `ineligible` and `unknown` complete it. These are delivery outcomes, separate from accepted-record loss.
- **Stage interaction.** The reference tab's unmeasured outcomes count as `browser_render` exclusions when its ledger records them. Other tabs' do not, and publications never reported do not either. Any tab's visible render refused as stalled or implausibly timed is still excluded, as before. The advisory criterion's exclusion-aware bound is therefore often inconclusive while the reference tab is hidden or busy. It is advisory, so the verdict is unaffected.
- **Reports (TEL-B consumer).** The run report's telemetry acceptance section states the archived population in HTML and PDF. Each of its figures is in the CSV (`telemetry_render_population` rows).

## Validation

Go:
- `TestTelemetryRenderLedgerAccountsTheReferenceClient` covers:
  - pinning, including no pin for an unheld publication;
  - non-reference tabs and first-outcome-wins;
  - a publication named twice;
  - in-range late reports;
  - ineligible and unknown reports;
  - the exactly-once identity.
- `TestTelemetryRenderLedgerAccountsPastEviction` covers pending evicted publications, a whole evicted range, unplaceable reports and re-adoption.
- `TestLivePublicationsCarryIdentityAndEligibility` covers sequences, eligibility, collection bounds and no fabricated bounds.
- `TestDistributedPublicationRepeatedOnlyWhenEveryCollectionWasPublished` covers the repeat check, including a batch without telemetry.
- `TestTelemetryCollectionReplayDoesNotReageTerminalMetrics` covers the install-guard wiring through a real distributed revision, under legacy and sequence identities: a new collection's publication is eligible, and a terminal replay's is ineligible with the next sequence. `TestLivePublicationsCarryIdentityAndEligibility` also shows that a point a guard rejects takes no sequence.
- `TestClientRenderReportAccountsPublications` covers the report handler, the live projection, the manifest and the reference-only exclusions.
- `TestClientRenderReportGuardsTheReferenceAndTheBudget` covers malformed names, unknown fields, a spoofed tab identifier, the budget and archived rows.
- `TestTelemetryAcceptanceReportStatesTheRenderPopulation` covers the HTML, PDF and CSV report output.

Dashboard:
- the reporter: superseded, rendered and background outcomes; batching into one render with no double reports; nothing reported from before activation or before a visible render;
- the receipt log: order, validation, bound, per-run resolution, replacement of re-delivered publications, and discarding.

End to end: `workflow-telemetry-stages.spec.ts` asserts the mixed report and the live and archived population in both authentication profiles.

The telemetry acceptance inventory has a render-population row. Its Go tests are in the telemetry fault matrix selector. The generated API contracts (both swagger copies and `openapi.ts`) are regenerated and verified.

## Review

Separate controller and dashboard reviewers read scratch snapshots.

- Rounds 3 and 4 found no high, medium or low issues. Their nits were fixed: discard ordering by log position; a re-delivered copy resolving its twin; a test for the hidden-before-paint gate; the wiring and guard tests and their listing; a guide parenthetical.
- Round 2 found two mediums, both introduced by the round-1 budget fix: unnamed non-visible samples skipped the budget, and a non-reference tab's refused visible renders left the stage. It also found lows: the newest bound was clamped on raw clocks; a re-delivered publication could be measured twice; a stale `truncated` type; a hidden-before-paint outcome bypassed the measurement gate; the guide's `unknown` definition; the undeclared reference tab. All fixed.
- Round 1 found:
  - **Highs:** the generated contracts were not regenerated.
  - **Mediums:**
    - outcome-only entries spent the viewer budget, and hidden non-reference tabs reported every publication;
    - eviction discarded final accounting;
    - the reference tab identifier could be reused by another viewer;
    - receipts logged while reporting was off were reported superseded;
    - an end-to-end assertion expected visible samples only.
  - **Lows and nits:**
    - malformed names were silently dropped;
    - re-adoption kept the epoch;
    - archived rows kept the population;
    - a re-broadcast could show a rendered publication as superseded;
    - bounds were fabricated without a collection time;
    - batches without telemetry escaped the repeat check;
    - labels depended on batching, and the docs overclaimed;
    - the StrictMode reset dropped an outcome;
    - the newest collection time was on an inconsistent scale, and comments were stale.
  - All of these were fixed.

## Remaining

- **Window starts.** The bounds are collection times. The oldest included observation is up to one window earlier, and per-window starts are not carried yet.
- **Durability.** The accounting lives in the publishing controller's memory.
- **Declared reference.** The reference tab is chosen (the first to report a render), not declared, and is not guaranteed to stay in the foreground. A declared foreground reference client remains open.
- **Clocks and presentation.** It does not qualify browser clocks, prove visible presentation (the callback is still a paint opportunity) or establish the capture-to-visible-render distribution. Authenticated generator→controller→browser clock mappings and the exclusion-aware end-to-end bound remain open in TEL-A.
