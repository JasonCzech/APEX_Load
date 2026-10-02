# Next priority pass — 2026-10-01

Five implementation/review commits are planned. The first includes the preceding whole-pass review fixes, already verified and documented in the [previous record](../priority-pass-2026-10-01/REVIEW.md). IBM MQ remains low priority; the 10 GB benchmark remains waived. Live Azure acceptance requires an approved test environment and is separate from local implementation verification.

1. Cross-protocol SLA and regression reporting: complete. Extend exact terminal rules with population/protocol/group scope and missing measurements; add qualified messaging baseline evidence and explicit CI verdicts using existing load/version qualification.
2. Live analytics dimensions: complete. Retain bounded hierarchy/lifecycle joint evidence alongside selected network-profile intersections, without answering queries from unrelated marginals.
3. Real Kafka execution: pending. Implement real seek and transaction/consumer-offset coordination with isolation, ownership fencing and recovery verification.
4. Event Hubs consumer ownership: pending. Add processor ownership and durable checkpoint support with governed Blob access, bounded queues and restart recovery; keep the observer probe contract and zero-traffic preparation boundary.
5. Quantitative chart exports: pending. Apply the shared CSV/SVG workflow to remaining live and saved analytics charts, preserving filters, units and coverage.

Each item receives corresponding regression tests, affected Go/dashboard/browser checks and repeated review/fix passes before its commit. Full repository gates are recorded at completion.

## Item 1 review and verification

Implemented action/transaction SLA scope by exact protocol/group, P99 and complete acknowledgement observations, preserved legacy transaction behavior, messaging baseline observations, typed protocol acceptance metrics, workload/version/window-qualified verdicts, explicit CLI gating and saved dashboard/report evidence.

Review/fix round 1 corrected scoped zero scores when protocol declarations were missing and excluded incomplete current runs. Round 2 qualified dropped/missing telemetry windows, capped acceptance evidence work, guarded byte/duration overflow, classified CLI transport failures as unavailable acceptance, and corrected dashboard string replacement for the configured TypeScript target. Round 3 corrected the CLI pass-count guard. Round 4 added exact stable transaction identity selection, preserved measured-zero protocol message throughput, rejected partial diagnostic coverage, and corrected scoped report labels. A final review found no further actionable issues in this item.

The full Go suite passed before the final identity, typed-throughput and scoped-report extensions. Final affected Go tests passed afterward (`TransactionSLA`, scoped/acknowledgement, baseline, goals and contracts). Dashboard typecheck, lint, 28 affected unit tests, embedded build, generated contracts and documentation checks passed. The final baseline workspace browser test passed in both dev-admin and local-auth profiles, including exact identity selection, saved acceptance scope, qualified verdict display and reopening. The API race build was canceled after prolonged compilation under observed memory/disk pressure; it is unverified. The first commit also includes the preceding whole-pass hardening documented above. Latest-head full repository gates remain due at program completion.

## Item 2 review and verification

Retained bounded measured action/network/hierarchy/lifecycle tuples and distributions in native evidence v3, engine snapshots, optional RPC fields, source publications and SQLite/PostgreSQL history representations. Saved live panels expose exact filters and hierarchy/lifecycle grouping. Queries require merged action evidence and reject unretained combinations, incomplete peer coverage and missing grouping labels.

Review/fix rounds corrected legacy/source version recognition, rejected synthetic missing-label identities, bounded tuple labels and accounted for distribution retention memory. Final review found no further actionable issues. Engine, generator RPC, storage and API native evidence tests passed; additional query capability tests passed. Dashboard typecheck, lint, 14 relevant unit tests, embedded build, documentation checks, and the save/reopen browser workflow passed in both authentication profiles. The first commit is `bb2741dd`.
