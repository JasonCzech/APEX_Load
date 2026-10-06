# Native HAR ownership candidate review

Status: v5 independent static repeat review found no additional findings in
the five-file delta, with all 33 direct inputs revalidated. Required-mode
native acceptance and heavy API checks remain pending. This bundle does not
establish release completion or full browser evidence acceptance.

The candidate adds private never-reused HAR generations, exact context
close-once ownership, one shared bounded teardown deadline, existing-descriptor
cross-process leases, durable no-replace scrubbed publication and an
identity-bound retirement ledger. It isolates stale native-frame finalizer
errors from live sibling document receipts. See the
[scope guide](../../../guides/BROWSER_HAR_WORKING_OWNERSHIP.md).

The coherent candidate direct source inventory is
[evidence/apexload-native-har-ownership-candidate-source-v5-20261005.json](evidence/apexload-native-har-ownership-candidate-source-v5-20261005.json):
33 direct inputs (23 changed/new Go sources/tests and 10 supporting references).
The before-strict-type draft is historical and superseded. Neither inventory
is a complete transitive build or native runtime inventory.

Parent reviews v1 through v4 identified actionable findings. Their exact
receipts, prior source freezes, red attempts and failure dispositions are
retained. The candidate fixes blocked CDP detach ownership, callback Goexit
false-success receipts, incomplete retirement recovery, retention bounds,
document close ownership and stale-frame isolation, planned-batch ledger
replacement, mixed recorded/unrecorded batch mutations, duplicate completed
pre-rename discovery, teardown phase budgets, and post-wait namespace identity.
Additional red-to-green fixtures require private raw-file reservation before
SDK admission, correct joined size metadata, and actual typed JSON scalars.
Unknown or unproven writers remain preserved.

The [parent v5 static repeat review](evidence/apexload-native-har-ownership-parent-review-v5-20261005.json)
contains no additional static findings in the reviewed delta. It ran no Go
tests, and explicitly requires native/API execution and final composed gates.
The prior [v4 finding receipt](evidence/apexload-native-har-ownership-parent-review-v4-20261005.json)
remains a changes-required result, with its disposition preserved.

The current v5 scoped qualification is
[evidence/native-har-v5-focused-final-2.json](evidence/native-har-v5-focused-final-2.json)
and [evidence/native-har-v5-race-final-1.json](evidence/native-har-v5-race-final-1.json):
the selected browser ownership, capture, retention and document regressions
passed twice normally and twice under the race detector. These commands use
short mode: the new native fixtures are skipped, and adjacent API batch/error
fixtures, including completed retirement before rename, are uncompiled pending
the coordinated heavy build slot. The exact frozen v4 pre-dedup API source
and overlay are preserved to execute a later red reproduction without changing
the v5 worktree; this prepared overlay has not been executed. No broader
API or required-mode native pass is inferred from these checks.

Each check records its command, elapsed time, exit code, raw-log SHA and
point-in-time repository Go/module hash stability. The latest checks froze
4,030 repository Go/module inputs; they do not inventory all dependencies in
the module cache or native runtime. Large original inventories remain in the
explicitly named private raw receipts. Compact digests, raw test logs and
direct source inventories are retained here. A historical broad short suite
cannot qualify required-mode native acceptance: some existing tests omit
short guards, used an auto runtime gate, and lacked verbose per-test execution.

The neutral ordered frame-finalizer patch has no HAR owner/ledger dependency.
It was separately applied and qualified in the parent integration worktree;
that prerequisite's unchanged actual-native oracle is separate from this
candidate's pending HAR-owner native runtime tests.

All fixture HARs, URLs, cookies and bodies are invented loopback or example
content. The user's original HAR and derived private body/header/URL content
are absent. No original-target replay, controller/API upload, native export
or archive-transfer acceptance is performed by these tests.

The 2 GiB working admission check is point-in-time, distinct from future
aggregate active-writer or RSS enforcement. Unknown legacy/crash writers are
preserved. Linux/macOS source identity checks do not establish Linux/Windows
runtime qualification. Full BE-3, managed native archive transfer/export,
aggregate writer/RSS control and full program delivery remain open.

[Check history and failure dispositions](evidence/check-history.json).
