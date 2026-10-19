# Native HAR ownership qualification

Current composed source/test qualification: the default full Go suite, full dashboard cycle in both authentication profiles, affected API races, actual PostgreSQL cohort, SDK contract and generated-contract checks passed in the [composed review](../../../archive/qa-reviews/INTEGRATION_2026-10.md#feature-completion-composed-review-2026-10-05). That qualification is scoped; the broader feature requirements and explicitly unrun acceptance remain open. The following component checkpoint predates these composed results. Its original bytes are preserved in [the historical checkpoint](README.component-checkpoint.txt).

The v7 candidate preserves the qualified v6 production behavior and passes
the refreshed documentation, provenance and packaging gates. The scoped SDK,
browser, native HAR ownership and controller retention gates are listed below. Independent reviews
found no additional scoped findings after the recorded fixes. Composed full
repository and dashboard qualification remains the parent task's separate gate.
This bundle does not establish full browser evidence or program completion.

The change gives each HAR writer a private never-reused generation, exact
context close ownership, one bounded teardown budget and descriptor-backed
cross-process leases. Completed output is scrubbed, durably published without
replacement and discoverable by flat artifact retention. An identity-bound
retirement ledger recovers proven terminal deletions while preserving unknown
or replaced writers. See the [scope guide](../../../guides/BROWSER_HAR_WORKING_OWNERSHIP.md).

The local MIT-licensed Playwright Go subset remains pinned to v0.6100.0 and
its existing native driver and browser versions. Its optional owned HAR close
method enters the actual SDK close body once and retains that complete result.
Prior ordinary/server closes fail closed. An unfinished dispatcher callback
repeat returns an explicit error without blocking acknowledgment delivery or
poisoning the first owner. Completed repeats return the retained result. The
ordinary Close API keeps its early-nil compatibility. The upstream manifest,
patch notes, explicit dependency tests and packaging checks record the subset
and changed-file contract.

The final direct source inventory is
[candidate v7](evidence/apexload-native-har-ownership-candidate-source-v7-20261005.json).
Its 135 direct inputs cover owned sources/tests, the SDK subset, module metadata
and relevant packaging/reference files. It is not a full transitive build or
native runtime inventory. The v6 source inventory preserves the exact behavior
qualification basis; its draft and older source freezes remain historical.

The first docs check found twelve broken relative links in the pristine upstream
README, whose referenced examples were intentionally omitted. V7 preserves those
exact bytes as README.upstream.txt with an explicit original-path mapping in the
80-file upstream manifest, adds a concise local README and updates the integrity
test and patch note. The docs checker is unchanged and skips no new paths. Normal
and race packaging checks pass three times, and the docs check passes. Relative
to the 134-input v6 direct inventory, v7 retains 130 hashes, updates
four documentation/provenance/integrity-test paths and adds one exact upstream
README copy. Every SDK/APEX production Go, native and module input retains
its qualified v6 bytes. Native/API behavior gates were not repeated for this
documentation and integrity-test-only delta.

Final scoped checks are:

- SDK owned-close contracts three times normally and three times under the race
  detector: `native-har-owned-sdk-contract-final-3` and
  `native-har-owned-sdk-contract-race-final-3`.
- Affected browser ownership, capture, retirement, context and document
  finality regressions twice normally and twice under the race detector:
  `native-har-v6-focused-final-1` and `native-har-v6-race-final-1`. Short mode
  explicitly skips eight native test invocations; it is not native acceptance.
- Required-mode actual Chromium/WebKit HAR ownership fixtures three times:
  `native-har-owned-sdk-all-native-final-1`. These cover successful flush,
  deadline-held exact ownership, explicit close failure, prior close rejection
  and a synchronous OnClose repeat. The separately owned callback fixture's
  first pass is also retained.
- The unchanged actual document identity/no-interaction oracle twenty times
  per engine: `native-har-owned-sdk-document-finality-required20-final-1`.
  All forty engine subtests pass without skips and report exact target counters
  of two root and four frame requests. No oracle thresholds or extra probe
  traffic were introduced.
- Controller retention regressions twice normally and once under the race
  detector: `native-har-api-retention-final-1` and
  `native-har-api-retention-race-final-1`. SDK packaging, recording workflow
  filters and pinned native-version checks pass three times normally and under
  race after the docs correction: `native-har-owned-sdk-packaging-final-3` and
  `native-har-owned-sdk-packaging-race-final-1`. The corrected repository docs
  check is `native-har-docs-final-3`; its failed predecessor remains preserved.

Each execution retains its exact command, elapsed time, exit code, raw-log SHA
and point-in-time repository source stability. Final Go executions freeze
4,114 repository Go sources plus root module inputs, excluding complete module
cache/native dependencies. Compact receipts retain inventory digests and the
original private receipt SHA; complete direct inventories and raw logs are
included. Fourteen direct installed runtime/SDK/module input bookends and local
module-resolution receipts remain separately scoped and unchanged across the
final native cohort.

Reviews v1 through v4 and the first SDK draft found actionable issues. Their
changes-required receipts, original source/overlay inputs, failed assertions
and superseded attempts remain preserved. They include joined callback and
detach barriers, Goexit false success, deletion recovery, bounded maintenance,
ledger plan mutation and replacement refusal, duplicate pre-rename completed
discovery, namespace replacement, private raw admission, size metadata and
strict JSON fields. The original pinned SDK's prior-close native reproduction
demonstrates why ordinary Close returning nil cannot certify a writer join.
The deterministic unfinished-dispatcher repeat and recording path-filter
regressions are recorded red before their fixes. Failed compile/fixture
attempts are explicitly distinguished from production assertion failures.

The [corrected SDK independent repeat review](evidence/apexload-har-sdk-corrected-authoring-independent-review-v6-20261005.json)
bookends all 134 direct inputs and reviews 39 relevant paths on v6. The [v7 packaging repeat
review](evidence/apexload-har-sdk-readme-packaging-authoring-independent-review-v7-20261005.json)
finds no additional issues in the five-path documentation/provenance delta
and bookends all 135 final direct inputs. It runs no tests;
its scoped static result is separate from the execution receipts. Parent v5
review retains its exact narrower scope. The [parent composed HAR/document/resource
review](evidence/apexload-har-document-resource-composition-independent-review-v1-20261005.json)
finds no additional scoped issues after bookending 147 composed inputs. Its
composition deliberately preserves the integration native test’s aggregate
failure logging without changing assertions; it is separate from this worktree’s
execution inventory.
The neutral document finality correction was separately reviewed and qualified
in the integration worktree; the owned HAR tree preserves that correction.

All fixture HARs, bodies, cookies and URLs are invented loopback or example
content. The user's original capture and derived private body/header/URL
content are absent. No original-target replay, controller/API upload, native
export or archive-transfer acceptance is claimed.

The 2 GiB working admission check is point-in-time; aggregate active-writer or
RSS enforcement remains separate. Unknown legacy/crash writers are preserved.
The actual native evidence qualifies the installed macOS arm64 runtimes, not
Linux or Windows runtime delivery. Full BE-3, managed native archive
transfer/export and full program completion remain open. Shared build-cache
cleanup is deferred until the parent task's composed checks and fixes join.

[Check history and failure dispositions](evidence/check-history.json).
