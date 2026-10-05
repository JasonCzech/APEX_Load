# Kafka management reader review and qualification

Status: implementation, independent review and repository/targeted acceptance complete; final documentation check recorded below. This slice does not close the feature-completion program.

The production `kafka-offsets` observer reads exact approved topic/partition/group inventories over explicitly pinned TLS and SASL PLAIN/SCRAM identities. Numeric routes and CA trust are loaded immutably without target traffic during construction/readiness. Fixed versioned metadata, coordinator and offset requests retain exact next positions, presence and the oldest component capture time. Request and physical-connection ownership stays charged until actual cleanup is proven. Capability-gated source authoring preserves saved approval separately from unsaved edits and revision conflicts; CLI source update/delete uses compare-and-swap.

## Review, fixes and repeat review

Independent source reviews are clean after fixing:

- The signed authorization deadline can precede the scheduling deadline. Admission and actual I/O use the earliest signed, parent and operation deadline, including queue waiting.
- A context timer can be delivered after its deadline. Shared admission, Kafka I/O, observer qualification and scheduler completion now check the actual deadline, with delayed-timer queue/callback/publication and real TCP/peer-closure regressions.
- SASL session lifetime starts at the last authentication request, so a delayed response cannot extend authority. Close errors retain first-owner/proof semantics and unresolved physical charge.
- Saved approval cannot cross a different source, project, pack or evidence kind. Narrow layouts contain the immutable approval. Help text has separate accessible descriptions; the original browser label failure is retained and both corrected profiles pass.
- Highest-valid stored source revisions remain deletable; updates retain increment bounds. CLI regression counters are atomic.
- The genuine fixture counts sockets before dispatch, discharges only after physical closure, retains test process-group authority until reap and verifies each owned Docker resource and protected scratch independently. It copies only bounded protected required inputs and preserves pinned image/trust constraints.
- Cold setup probes use bounded numeric listener readiness and short provider deadlines without retrying an unproven timed-out remote command. Independent setup production uses finite per-record waits. Transaction setup retries only bounded control requests, with no production-reader retries or weakened oracle.
- Fixture reports and test logs cannot overwrite retained existing evidence, including dangling symlinks; rejected destinations perform no scratch/provider work.

## Executed checks

| Check | Result |
|---|---|
| Complete collector normal suite after final deadline fix | Pass, 9.719 seconds |
| Targeted Management/Kafka race suite after final deadline fix | Pass, 11.379 seconds |
| Complete CLI suite; revision-boundary race | Pass, 15.595 / 2.813 seconds |
| Final dashboard typecheck, lint, embedded build | Pass |
| Final focused source units | 9 passed |
| Initial full dashboard cycle | 459 files / 4,774 units passed; dev-admin 196 passed, 4 conditional skips and one Kafka label failure; local-auth 116 passed, 1 conditional skip and the same Kafka failure |
| Corrected final source workflow | dev-admin 1 passed, 2.8 seconds; local-auth 1 passed, 9.8 seconds; no retries |
| Final fixture cleanup/readiness/provenance units | 11 passed |
| Genuine Apache Kafka 3.9.2 fixture | Pass, 1.391-second Go suite / 28.692-second harness |
| Full Go repository gate | Pass; API suite 1,280.330 seconds, collector 9.601 seconds |
| Final documentation check | Pass, 107 active Markdown documents |

The first full Go attempt was interrupted before completion. The final restart uses the same task cache and serialized `-p 1` lane with `GOMAXPROCS=2` and an 8 GiB Go memory setting; the earlier 4 GiB compile profile showed GC pressure above its setting. This build/test adjustment does not qualify analytics resource controls or a native release. The final complete run exited zero.

The full dashboard command remains recorded as failed at its original snapshot; subsequent final focused execution covers the correction. Source/approval viewport screenshots from both profiles are retained and visually inspected at desktop and 375 pixels. Browser authoring uses scoped API fixtures; it does not certify a broker. Copied text logs normalize tab indentation and trim trailing whitespace; SHA-256 hashes of the raw originals are in `raw-log-hashes-final.txt`.

## Genuine broker result and limits

`actual-broker-final.json` and the matching UUID-specific test/harness logs identify the official pinned 3.9.2 index and Linux arm64 platform digest, single KRaft topology, 1 CPU / 1,536 MiB container ceiling, guarded data directory, physical test reap and verified removal of all owned resources/scratch. All three SASL profiles, denied group, wrong cluster, wrong CA, zero construction/readiness target traffic, fixed read-only frame audit and physical ownership checks pass.

The transaction visibility oracle passes: before the transaction HWM/LSO/committed-next are 4/4/1; while open they are 5/4/1; after abort they are 6/6/1. The reader never labels physical replica log end as HWM, invents an offset increment or measures lag by consuming. Failed setup/cleanup attempts remain retained; a reused-basename mismatched report/log pair is explicitly excluded from qualification, and final evidence uses a fresh UUID with overwrite protection.

This result does not certify full consume-transform-produce atomicity, producer fencing, broker restart/multi-broker failover, newer releases, hosted/protected brokers or Event Hubs Kafka endpoints. Those remain explicit program work. Service Bus/Solace readers and the full MSG-2 exit are separate slices. Only the 10 GB benchmark is waived; native 1 GiB/platform and protected provider gates remain required. Build-cache cleanup stays deferred while dependent program builds/tests still need the caches.
