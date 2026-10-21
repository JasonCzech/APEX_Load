# Immutable replay artifact backend — 2026-10-21

This BE-B slice supplies a passive filesystem/object backend adapter for the existing replay artifact receipts. It does not enable durable replay publication or recovered HTTP downloads. Those next slices must reserve bytes and an exact owner/generation before staging, establish writer completion only after successful staging, publish through the storage CAS, authorize current project/receipt reads and take download admission before materializing bytes.

## Contracts

- Each generation is a fresh 64-character lowercase hexadecimal identity. HAR and report have independent exact lengths and SHA-256 hashes, bounded at 256 MiB and 16 MiB respectively. No executable source is introduced by this adapter.
- Staging creates exclusive objects; no retry overwrites a prior attempt. Both objects must write, close and verify before success. Failed or ambiguous staging neither deletes objects nor supplies writer-join proof.
- Source fingerprints bind the current backend namespace. Object staging-root changes do not change remote authority; changing a filesystem root does. Reads never follow a persisted arbitrary URI.
- Reads verify both complete objects into private disk staging before returning readers. Missing, corrupt or mismatched evidence fails explicitly. There is no synthetic replacement, large HAR heap buffer or successful partial response. Closing the result removes its private staging directory; cleanup of backend bytes does not invalidate an admitted verified reader.
- Cleanup requires a joined cleanup tombstone and removes only its two generation-specific object names. Local cleanup removes an empty generation directory; foreign extra files remain and prevent reservation release. Failed writes retain their reservation until independent recovery establishes writer completion.
- `.apexload-replays` is reserved against run-ID and Windows-alias collisions. Ordinary run archive cleanup cannot recursively delete it. Replay receipt admission owns its byte budget independently of run-output backlog accounting.

## Review

Implementation review found and fixed a collision with ordinary run archive namespaces. The dedicated reserved namespace, run-ID alias checks and deletion guards now preserve the replay owner. Additional review tightened exclusive local directory creation, exact-size output (the overflow probe is not written), empty-root refusal and handle-relative cleanup. Regression tests cover these boundaries.

Follow-up review of source authority, immutable writes, reader staging, cleanup ownership, namespace aliases, quota isolation and the regression tests found no further findings, including low severity. Final source hashes remained unchanged throughout qualification.

## Validation

Main `ceb3f9b83` was integrated without content changes as `da030fd81` before testing and remained current at completion. Actual Go 1.26.10 validation passed:

- Full `go test -timeout=20m ./...`, with the private-HAR import overlay: 35,966 passing test/subtest events and 46 conditional skips; no failed packages or tests.
- Adapter, namespace and archive deletion regressions: 42 passing events; independent controller backlog checks: 33 passing events.
- Three race-detector repetitions: 126 adapter/archive events and 99 controller backlog events, all passing.
- The supplied private HAR's stable import summary matches its retained baseline. Randomized script identity is excluded from comparison. Its requests were never replayed.
- Documentation checks pass for 318 active Markdown files; whitespace checks pass. All 311 previously tested embedded dashboard assets are unchanged. No new UI or native-browser qualification is claimed.

Raw logs: `full-go.jsonl`, `final-targeted.jsonl`, `backlog-targeted.jsonl`, `race.jsonl`, `backlog-race.jsonl`; source hashes: `tested-source-hashes.json`. Conditional skips retain their stated environment/platform gates.

## Evidence and limits

Local logs live under `/private/tmp/apex-browser-durable-backend/`. The object fixture uses Go Cloud's file-backed bucket implementation; it does not certify S3, Azure Blob or GCS behavior. The adapter is not yet wired into browser session completion or downloads, and it does not close crash/restart/HA, live cloud, real TruClient or reference-host acceptance. Existing native-versus-synthetic disclosure and the supplied private HAR import contract remain unchanged. See the [implementation specification](../../../ROADMAP_IMPLEMENTATION_SPEC.md#be-b--har-durability-and-real-importreplay-workflows).
