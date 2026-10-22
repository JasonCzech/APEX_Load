# Replay artifact receipt storage — 2026-10-21

This storage foundation reserves immutable, versioned terminal HAR/report identities before upload. The reservation requires the exact unexpired retained session/project/owner lease and a current project. It retains backend-namespace and artifact hashes, lengths and a fresh generation; it does not store arbitrary backend URLs or credentials.

Receipt count and byte admission include staging, ready and cleanup-pending objects. Unknown writers remain reserved after expiry. Only an explicit physical-writer join permits publication or cleanup. Publication rechecks terminal ownership against the database clock. A ready receipt protects the global session namespace after process pruning removes the lease; expiry alone does not release that namespace. Cleanup removes only the exact joined generation after physical deletion is confirmed.

Project deletion keeps cleanup metadata and revokes old replay leases. Recreating the project ID cannot authorize the old owner to reserve or publish. Reads and cleanup verify the serialized identity and reserved size against the stored row. Schema release 74 adds the receipt table and indexes; older application/schema combinations remain governed by the existing schema-version refusal contract.

## Validation and review

Go 1.26.10 full suite passed: 35,925 passing tests/subtests and 38 explicit skips, with no failures. Targeted storage tests passed under the race detector across three repetitions (75 passing tests/subtests), and real PostgreSQL receipt tests passed across three repetitions (9 passes, no skips). Documentation validation passed for 314 active Markdown files.

The original private HAR import's stable compatibility fields match the prior baseline exactly. The tested source hashes, embedded dashboard asset hashes and base commit were verified unchanged after the run. This storage-only slice does not change in-app help or browser UI. Intermediate logs, schema fingerprint derivation and final evidence remain local under `/private/tmp/apex-browser-durable-replay/`. No raw QA artifacts are committed.

Review findings fixed: verify serialized receipt identity against its row before cleanup; explicitly version the payload; reject invalid UTF-8 rather than allow JSON serialization to change an identity. A second review of the final storage transitions and tests found no further issues.

## Remaining integration

This slice does **not** make browser replay HARs durable yet. Controller publication, approved-backend staging, physical-writer proof, bounded cleanup, restart/HA reads and authenticated HAR/evidence downloads remain to be wired. All controller session kinds must consult the durable namespace, including non-HA execution. The standalone bridge retains its local behavior. Receipt methods do not themselves verify backend bytes or infer that a remote writer has stopped.
