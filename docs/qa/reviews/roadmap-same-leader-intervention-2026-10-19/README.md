# Same-leader evacuation intervention — 2026-10-19

A REC-B slice from the [implementation spec](../../../ROADMAP_IMPLEMENTATION_SPEC.md#rec-b--bound-same-leader-terminal-cleanup-without-weakening-fences). Validation completed October 20.

A canceled current-owner evacuation with a durable journal now has a bounded
stop retry budget: the initial round plus five retry rounds. Exhaustion persists
a bounded, scrubbed intervention diagnostic before retiring the worker. Failed
persistence retries without issuing more stop rounds. The run stays STOPPING
and retains its clients and capacity while termination remains unproven.

The existing system-administrator endpoint accepts this explicitly eligible
same-leader state. Its required all-processes-stopped attestation is the proof;
controller cancellation is only eligibility. A distinct durable proof prefix
preserves truthful ownership history and old successor prefixes remain readable.
FAILED/proof persistence precedes closing transports and archive/capacity
reconciliation. Original causes and incomplete archive evidence survive. Held
joint-evidence buffers whose writer has ended are released with dropped-evidence
accounting; missing evidence is not converted into a successful archive.

The [operator procedure](../../../ENTERPRISE_PLATFORM_RUNBOOK.md#resolving-an-unproven-generator-evacuation)
is bundled in Help. Old controllers cannot use the new proof shortcut; retain
its durable record for a capable cleanup owner. Ordinary terminal/stale-runtime
cleanup still stops uncommitted participants. No global epoch is advanced and
no heartbeat, lease timeout or pod-name reuse is accepted as process death.

## Validation and review

- The full standard Go suite passed: 168 tested packages and 17,638 top-level
  tests; 36 top-level skips are retained in the log. Ten packages have no tests.
- After the evidence-buffer review fix, the full API suite passed again on the
  final production source: 6,412 top-level tests, 14 top-level skips.
- The final targeted recovery run passed all 18 top-level tests. It includes
  the subsequently added UTF-8/redaction diagnostic case, exact retry budgets,
  failed persistence, lost ownership, authorization, repeated cleanup and
  recovery after a crash immediately following durable attestation.
- Final targeted Help unit tests passed (51 tests). Type checking, lint, documentation
  links and the embedded dashboard build passed. The final full dashboard cycle passed 8,008 unit tests across 716 files,
  321 dev-admin browser workflows (6 expected skips) and 260 local-auth
  workflows (1 expected skip).

The validation summary (`evidence/validation-summary.json`), compressed logs,
source hashes (`evidence/source-hashes.json`), controller hashes, runtime metadata
and review rounds (`evidence/review.txt`) retain the tested local cohort.
[Commands and cohort notes](evidence/commands.md) distinguish each gate. The first
broad attempt used incompatible `-trimpath` paths for source fixtures and was
stopped; its log is preserved separately. It also exposed a stale generated QA
spec-file count, fixed in the preceding scenario commit. That attempt is not a
successful full-suite result. The standard broad run predates the final buffer
fix; the subsequent full API run covers that fix. Only the additional Go diagnostic
test changed after the final API binary compiled, and it passed in the targeted
run. Browser verification then exposed an administrator Help reload race: pending
permissions redirected the requested article before access resolved. The Help page
now keeps that URL pending without rendering protected content; confirmed denial
and errors still fall back, and public Help stays available. Unit and real-response
delay browser regressions cover those states. The Help bundle was rebuilt after
Go validation finished. A first complete dashboard attempt passed all units and
321 dev-admin workflows (6 expected skips), then its required main-fetch guard
stopped before building local-auth: main had advanced. The two upstream chart/history
commits were integrated without conflicts; 187 focused tests passed. The repeated
full dashboard cycle validates the final UI/bundle on that integrated tree.

Review fixed archive/topology lock ordering, stable bounded diagnostics,
terminal evidence-buffer retention and the Help permission-loading race. The
first browser attempt also contained an incorrect test assertion: repository-only
review references intentionally stay plain text in bundled Help. Its failure and
the subsequent real reload failure are retained separately. Final review found no unresolved issues.

The private ZIP's actual multipart HAR import remains documented in the
[scenario review](../roadmap-scenario-prerequisite-2026-10-19/README.md). This slice
changes no importer or source-capture payload, and no customer capture was replayed.

## Remaining scope

This implements the journaled current-owner intervention boundary. Unjournaled
cleanup, earlier-phase resumable adoption, independent deployed fault trials and
the rest of the recovery program remain separately open. Local tests do not
certify multi-host recovery. Session caches remain in use for subsequent slices.
