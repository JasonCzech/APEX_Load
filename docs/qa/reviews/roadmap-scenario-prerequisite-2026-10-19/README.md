# Scenario prerequisite handoff — 2026-10-19

AUTH-C slice from the [implementation spec](../../../ROADMAP_IMPLEMENTATION_SPEC.md#auth-c--lifecycle-edges-and-property-level-parity).

Script save notifications now tell the scenario wrapper when authoring must stay
open. Source review returns, later edits and secret setup preserve the embedded
workflow while the confirmed script is assigned to the selected group. Later
saves update the library projection without adding another group. Ordinary
finished saves retain automatic return. Explicit return uses the existing dirty
navigation guards and preserves the scenario draft.

An archive review also stays mounted when its editor becomes a saved editor with
later changes. The source return survives subsequent saves and canonical reload
failure; save callbacks remain distinct from Java approval receipts.

Protected imports still require save-before-edit. The new browser case models a
legacy plain response from synthetic source archives and uses real HTTP saves.
No customer capture or converted private payload is included.

## Validation and review

The complete dashboard cycle passed: typecheck, lint, 7,988 unit tests in 716
files, asset generation and both browser profiles. Dev-admin passed 320 tests
with 6 skips; local-auth passed 259 with 1 skip. Both exercised
the new prerequisite/late-edit/sibling test and the Help workflows. Skips remain
skips. Main was integrated through `5a9355e16` before the build.

Actual multipart HTTP import of the private HAR returned HTTP 200 with 476
request actions from 803 entries, 327 filtered assets, 479 assertions, 37
extractors, 43 correlations, four warnings and zero DSL validation issues.
All recorded structural fields match the retained baseline; the source hash is
unchanged. No private capture, converted script, traffic replay or library save
is included.

API embedded-document serving and generated lifecycle freshness passed. The
related QA dashboard spec-file count was regenerated from 89 to 90; this changes
an inventory count, not qualification status.

See the validation summary (`evidence/validation-summary.json`),
review rounds (`evidence/review.txt`), commands (`evidence/commands.txt`),
controller hashes (`evidence/browser-controller-hashes.json`),
staged source/asset hashes (`evidence/staged-source-and-asset-hashes.json`) and
HAR comparison (`evidence/external-har-comparison.json`). Compressed logs retain
failed fixture iterations and the superseded full cycle as well as the final
passing cycle. Checksums cover retained evidence.

Build caches remain available for the subsequent authorized slices. Their
cleanup is deferred until dependent session validation is complete.

## Remaining scope

This closes the named prerequisite and later-edit lifecycle edges. Durable user intent,
contextual revisions, the complete property matrix and external qualification remain open. No adapter is retired here.
