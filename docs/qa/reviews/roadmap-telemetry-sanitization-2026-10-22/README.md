# Telemetry text sanitization — 2026-10-22

This [TEL-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-b--policy-evolution-and-complete-consumers) covers the text telemetry evidence carries:
- generator source and target names;
- policy names and descriptions;
- counter and stage reasons;
- exclusion and render reasons;
- the verdict text built from them.

Before this slice, that text reached run reports, comparisons, the archive manifest view, safe export and CI output as recorded. Only the durable telemetry notification envelope scrubbed it.

## Contract

- **Scrubber** (`newTelemetryTextScrubber`). The rules are the runs' own: the scenario's and each group's, in both the run's and its admission (initial) provenance.
  - Each distinct rule is used once.
  - A rule that no longer compiles is skipped, and the others still apply. It does not count toward the engine's per-configuration limits.
  - Rules beyond those limits (128 rules, 256 KiB) go into further configurations. Each configuration applies only its patterns (`RedactionConfig.ApplyPatterns`), in order, so every rule runs before the shared scrubber, which runs once.
  - At most 1,024 distinct rules, valid or not, are read (`maxTelemetryRuleCandidates`), which bounds the compiling they cost. Runs that hold more have their rule-scrubbed text withheld (`telemetryTextWithheld`) instead of shown. So does a configuration the engine refuses.
  - Control characters and bidirectional formatting characters (including the byte-order mark) become spaces. Joiners and other format characters are kept.
- **Three forms of scrubbing:**

  | Form | Applied to | Scrubbing |
  | --- | --- | --- |
  | `text` | reasons, descriptions, verdict text | run rules, then `scrub.Value` |
  | `label` | policy names | run rules, then `scrub.ValueLabel`, which keeps a long name |
  | `scrubTelemetryIdentity` | generator targets, IDs and source names; keys (states, units, stage names, exclusion keys, segment reasons, the coverage algorithm, comparison capabilities, policy pin IDs, kinds and sources, the policy's mode and lag stages, criterion IDs) | `scrub.ValueLabel` alone; a real key passes unchanged |

  Identities never take run rules. Those are payload rules: one that masks addresses would make every generator the same label, and an archive imported from the safe export would then fail its source-coverage and validity joins.
- **Manifest projection** (`scrubTelemetryText`, applied by `maskRunArchiveManifest`). It returns a copy with these scrubbed:
  - in both provenances:
    - policy pins;
    - the acceptance policy's pin, description, mode and lag stages;
    - generator shard targets;
    - inventory targets and IDs;
    - the run's and each group's selected targets;
  - coverage source names, its algorithm, its observer-lost marker, and segment reasons and policy hashes;
  - the budget policy's keys (policy ID, hash, schema, capture boundary, algorithm, cadence scope, coverage rule, budget stages) and stage-digest keys (keys that scrub alike keep the first);
  - loss states and reasons;
  - stage names, units, availability, reasons and exclusion keys;
  - the render population's name, epoch, reference client and unqualified keys;
  - source-coverage targets and generator IDs;
  - host-network sources.

  Empty lists stay empty. That projection is the run report's manifest (JSON, HTML, PDF, DOCX, CSV), the manifest view, safe archive export and the browser diagnostics bundle. The report's host-network rows come from it too.
- **Generator lists.** `scrubTestRunManifestTargets` scrubs, as identities, every generator target and ID a test manifest lists, and its telemetry policy's keys:
  - selected, shard and inventory targets in its generator lists;
  - selected and shard targets in both configurations;
  - each group's and each script's selected targets;
  - the stored telemetry policy's availability, reason and keys (an imported archive's test manifest is stored as it came).

  The test manifest view and the safe export's test manifest (`maskTestRunManifest`) use it. So does the run report's generator-assignment appendix, in every report format (`reportTestManifest`). The report's completeness finding and the run calendar's topology (live and archived, `topologyForCalendar`) name generators the same way.
- **Verdicts.** They are judged on the recorded evidence. `scrubTelemetryVerdict` then scrubs their text: the policy pin, mode, criterion IDs and reasons, reasons and exclusions. Criterion IDs and the mode are keys clients match on; a real one passes unchanged. This applies in:
  - the report;
  - CI output (`newCIValidatedArchiveEvidence`, which feeds the response and the GitHub check summary);
  - the notification sent where the durable outbox is unavailable, whose run name is scrubbed as an identity.
- **Comparisons.**
  - A new comparison is decided on the recorded profiles. It is then shown with both runs' profiles scrubbed with both runs' rules. A policy is shared by a project's runs, so one run's rule hides the name in the other's profile too.
  - A comparison saved before this slice is shown with the shared scrubber, because its runs' rules are not at hand.
- **Unchanged.**
  - The archive keeps what was recorded.
  - The durable notification envelope keeps its own scrubbing and its identity. Its scrubbing is the full form, which can blank a long generator name.

## Validation

- **`TestTelemetryProjectionsScrubSentinels`** plants sentinels in every field above, in both provenances, and in a generator-assignment manifest. The manifest has them in its lists, configurations, groups and scripts. The policy also has an imported lag stage that its criterion ID carries. The sentinels include credentials and tokens in addresses, a payload a run rule matches, and secrets in reasons, keys, names and the description. It asserts none appears in:
  - the built report's JSON;
  - the engineer-profile HTML and DOCX (every part of the package), rendered with the manifest `reportTestManifest` projects;
  - the report CSV;
  - the telemetry section's PDF lines and CSV;
  - the masked manifest, the masked test manifest and the report's test manifest;
  - a new comparison whose baseline lacks the rule (JSON and line);
  - a comparison saved before scrubbing (credential sentinels);
  - the CI response built by the production constructor, and its check summary.

  What survives: revisions, counts, verdicts, a long generator name (`apexload-generator-7d9f8b6c5d-x4k2p`), and two generators that a run rule matching addresses would have made alike. The masked manifest's completeness finding still says "1 of 2" were not collected. The caller's manifests still hold the recorded text.
- **`TestTelemetryTextScrubberRulesAndShape`** covers:
  - deduplication;
  - an invalid rule leaving the others in force;
  - group rules;
  - a later JSON-shaped rule still matching JSON the shared scrubber would re-encode, including past 128 rules;
  - identities ignoring rules;
  - text withheld past the bound on distinct rules;
  - control, bidirectional and byte-order characters removed, with a joiner kept;
  - a real criterion ID kept;
  - an empty source list kept empty.
- **`TestSavedBaselineComparisonServesScrubbedTelemetry`** stores a comparison with raw telemetry text and reads it through `GET /tests/{id}/baseline-comparisons/{comparison_id}`.
- **`TestTelemetryAcceptanceWebhookScrubsSentinels`** sends the non-durable `telemetry_acceptance` webhook for the sentinel run.
- **`TestRunReportRouteScrubsGeneratorTargets`** renders `GET /tests/{id}/report` with the sentinel test manifest.
- **`TestRunCalendarTopologyScrubsGeneratorTargets`** builds the calendar's live and archived topology from the sentinel generators.
- The three route tests fail when their scrubbing is removed, as do the sentinel test's lag-stage, selected-target and pin-key checks.
- **`TestTelemetryNotificationRuntimeScrubsAndPinsRecipients`** remains the durable notification's sentinel test.
- **Inventory and existing suites.** The new tests are in the telemetry acceptance inventory's sanitization row and the fault-matrix selector. The existing telemetry, archive report, sanitize, CI, baseline, comparison, masking, export, host-network and library tests pass.

## Review

Two reviewers read scratch snapshots: one on coverage and security, one on consumers and regressions.

**Round 1** found highs and mediums, all fixed:
- `InitialProvenance` kept raw policy names.
- Long identity names were blanked by the full scrubber.
- Generator targets stayed raw in other fields of the same projection.
- Saved comparisons were served unscrubbed.
- The policy description and verdict text bypassed the scrubbed copy.
- The CI wiring was untested.

Round 1 also fixed:
- empty lists becoming null;
- an invalid rule disabling every rule;
- rules running after the shared scrubber;
- the non-atomic notification being raw;
- labels from imported manifests being unscrubbed;
- group rules being ignored;
- bidirectional characters surviving;
- comparisons using one run's rules.

**Round 2** found these, all fixed:
- Run rules rewrote generator identities, collapsing them and breaking an imported archive's joins. Identities now take the credential scrubber alone.
- Generator targets stayed raw in the report's generator-assignment appendix, the test manifest view, the safe export's test manifest and the completeness finding.
- One configuration per rule meant later rules saw re-encoded text, and nothing deduplicated or bounded the rules.
- Imported controller keys (algorithm, states, stage names, units) were raw.
- All format characters were stripped, including joiners.
- Criterion IDs were scrubbed.
- The library-bundle note was wrong.
- The saved-comparison and fallback-notification fixes had no test through their routes.

**Round 3** found these, all fixed:
- Selected generator targets stayed raw: in both provenances and their groups, and in the test manifest's configurations, their groups and its scripts. Pool runs list every member there.
- Imported policy keys stayed raw: pin IDs, kinds and sources, and the definition's mode and lag stages, which criterion IDs carry.
- One 128-rule cap on the runs' union withheld text for admitted runs whose scenario and group rules together exceed it. Invalid rules also counted toward that cap.
- Segment reasons are keys, but were scrubbed as text.
- The report wiring had no route test.

**Round 4** found these, all fixed:
- The run calendar's topology served generator targets raw.
- The test of rules past 128 did not use pure JSON, so it did not prove their order.
- A configuration the engine refused would have dropped its rules silently.
- Two statements about rule limits were wrong.

**Whole-branch review** found these, all fixed:
- The budget policy's keys, stage-digest keys and segment policy hashes from an imported manifest were raw in the projection, and so were a stored test manifest's policy keys.

## Remaining

- **Generators named as recorded:** the archive's telemetry sidecars (`telemetry_sources`, clock observations) and resource samples, and the validity findings the report builds from them. A target that carries credentials is shown scrubbed in the projections above. It no longer matches those raw names, and two targets differing only in their credentials become alike. Rejecting credential-bearing generator targets at registration would remove both cases; that is not done here.
- **Interval summaries and live views:** their telemetry condition labels are not scrubbed.
- **Library bundles:** a safe export's manifest bytes change once with this projection. A library bundle listed before the upgrade, or by a controller on the earlier version, declares the earlier manifest hash. Importing an archive exported now for it fails its hash check until the bundle is exported again.
- **Rest of TEL-B:** in-run policy segments, explicit non-default policy selection and the rest of the TEL-B consumer matrix remain open.
