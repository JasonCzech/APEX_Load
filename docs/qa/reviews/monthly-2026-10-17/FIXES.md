# APEX Load monthly review fixes

All 13 findings from the monthly review have corresponding code changes and regression tests on `codex/monthly-review-fixes`. The two P1 fixes were implemented first. Independent reviews covered correctness, concurrency, recovery, fidelity, compatibility, and maintainability; security remained outside the review criteria.

The fixes are based on main commit `e0a373090a71b4649d731d26d32a4a0e9165ebe8`. Work is isolated in the attached monthly-code-review checkout, preserving the original checkout's existing edits.

| Finding | Result | Regression coverage |
|---|---|---|
| 1 · P1 · Rendezvous lifetime | Startup and adoption transfer coordinator ownership to the remote run lifetime. Delayed cleanup cannot remove a replacement coordinator. | Successful and failed startup/adoption; graceful cancellation; terminal cleanup; lease loss; replacement ownership. |
| 2 · P1 · Atomic draft response | Conditional draft updates return their own committed document and revision through `UPDATE … RETURNING`. | Deterministic interleaving of two real SQLite writers; the first response retains its own snapshot and its next stale save conflicts. |
| 3 · P2 · Durable Stop | Stop persists entry state with lease and revision checks before acknowledgement when recovery has not restored in-memory state. | Controller replacement with halt/skip policies and completed/interrupted children; failed persistence; lost commit response; concurrent transition. |
| 4 · P2 · DNS fallback | Cached-address dialing allocates the remaining deadline across eligible addresses. | Blackholed first address with reachable fallback for TCP, IPv4, and IPv6; parent cancellation. |
| 5 · P2 · Appium text wait | Element presence and expected text share one polling deadline, including stale-element recovery. | Loading-to-ready transition; rerender; timeout; caller cancellation. |
| 6 · P2 · Generated identifier equality | Parameter lifetime uses actual replacement multiplicity, preserving repeated values within bodies, queries, and ordered headers. | Repeated and embedded literals; template exclusion; semantic confidence; duplicate and mixed-case headers; real engine interpolation. |
| 7 · P2 · Partial re-import | A shared coordinator classifies each update as pending or already applied by the same operation. Adopted items create neither an extra version nor a compensation entry. | All four batch handlers; failed second update and rollback; repeated retry failure; fresh-controller recovery; intervening edit rejection; fresh operation rejection; stable version identity. |
| 8 · P2 · Overlapping analytics windows | Summary and histogram queries share interval admission using the longest preceding window end. | Both representations; adjacent, overlapping, nested, cross-file, lead-in, duplicate, sparse, and different-identity windows. |
| 9 · P2 · Clipped analytics windows | Admission compares each source window with the requested query end as well as its bucket boundary. | Clipped and exact query ends; partially filled final buckets; both representations. |
| 10 · P2 · Catalog field drafts | A shared field registry participates in dirty state, Save, validation, replay, and export. Invalid or orphaned drafts remain visible and block stale output. | Integer and JSON fields; focused keyboard Save; export paths; action replacement; same-resource refresh; unapplied JSON; save-in-flight behavior. |
| 11 · P2 · Explicit empty cells | Row-grid edits preserve touched empty strings and provide an explicit Set empty action for absent cells. | Existing and new rows; absent versus empty cells; keyboard focus; live-table values and expected conditions. |
| 12 · P2 · MCP conditional saves | Registered endpoint tools accept `if_match`, forward it as `If-Match`, and preserve response ETags verbatim. | Conditional read/update transport and registered-tool forwarding; documentation covers authoring reads and conflict reconciliation. |
| 13 · P2 · Reused Helm values | A single fallback layer supplies missing HTTP/3, archive staging, and network-policy defaults while preserving explicit settings. | Actual September 15 values snapshot; missing/partial sections; explicit false and empty lists; configured overrides; parity with current defaults. |

**Related refactoring**

The changes consolidate rendezvous cleanup ownership, durable Stop transitions, per-item import reconciliation, summary/histogram interval admission, catalog field draft handling, and Helm upgrade defaults. Identifier analysis separates field locations from replacement counts and builds one authoritative header index per action. These refactors address the boundaries involved in the confirmed defects. Broader proposals concerning Java execution, scenario session extraction, provider units, KEDA generation, and runner-wide locking remain separate future work.

**Review rounds**

Initial implementation reviews were followed by independent area reviews and a final integration review. Findings discovered during those rounds were fixed and re-reviewed: duplicate ordered-header counting, repeated header scans, JSON-source versus structured-field ownership, same-resource refresh preservation, selected-action export, saving-state behavior, and Set empty focus restoration. Browser verification then found a clipped Set empty button; its layout was corrected and browser focus assertions were strengthened. The final integration review and subsequent reviews of the fixture, selector, focus, and CSS changes reported no actionable findings. Fresh pre-commit reviews of orchestration/storage/imports, runtime/analytics/Helm, and dashboard/MCP also reported no actionable findings.

**Validation**

- Full dashboard type checking, lint, build, and 7,830 unit tests across 706 files passed.
- Full tagged DuckDB package suite passed in 222.898 seconds.
- Full storage and Kubernetes deployment package suites passed; draft-save tests also passed under the race detector.
- Full parameterization, HTTP, and Appium package suites passed, along with importer-to-engine regression tests.
- MCP suite passed: 37 tests; two existing live-controller integration tests skipped.
- The final focused API run passed startup/adoption rendezvous lifetime and ownership, durable Stop recovery, and partial re-import recovery through all four batch adapters. Fixtures use isolated archives and unique run IDs.
- All 150 Go test packages passed across the broad run and full API retry. The retry passed 11,947 checks with 16 skips in 802.186 seconds, including all seven new top-level API regressions and the three scheduler fixtures previously interrupted by disk-full errors.
- The broad browser campaign completed both profiles: dev-admin 298 passed / 6 skipped / 3 initial failures; local-auth 239 passed / 1 skipped / 1 failure against the older embedded CSS. Every failing case passed on a corrective rerun. Final-bundle affected workflows passed in both profiles: dev-admin 4 passed / 1 skipped; local-auth 5 passed. No browser failure remains unresolved. The original `npm run test:cycle` invocation retains exit status 1 because it includes those earlier failures; the final-bundle reruns exited 0.

The initial browser failures identified two regression-fixture corrections and a clipped Set empty button. An initial HAR download also returned 500 during disk pressure; the same URL and corrective reruns subsequently succeeded. The grid layout correction was independently reviewed and verified in both auth profiles.

The [retained validation summary](evidence/fix-validation.txt) records commands, results, and regression names.

The protocol tests use deterministic network/WebDriver fixtures. No live Appium device, PostgreSQL server, cloud deployment, or throughput benchmark was used to establish these results.

**Cleanup**

The dedicated Go build/test cache was cleared with the same `GOCACHE` configuration used for validation, reclaiming 5.53 GiB and leaving 8 KiB. Another 5.63 GiB of unused test archives had been removed during disk pressure. Session API temporary files and approximately 296 KiB of checkout-local frontend/Python caches were removed. Dependencies, generated dashboard deliverables, and evidence were preserved. All session-owned browser/controller processes are closed; ports 18081–18084 are free.

Docker build-cache pruning was attempted with `docker builder prune --all --force`, but timed out after 20 seconds. Docker cache cleanup remains unconfirmed. The original checkout’s pre-existing edits were left untouched.
