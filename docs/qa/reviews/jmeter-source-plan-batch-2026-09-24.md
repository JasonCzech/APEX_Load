# Source-plan execution and protected batch repair verification

This change supplies grouped-source execution and atomic repair publication. It
does not establish strict JMeter client parity or advertise
`jmeter_messaging_v1`.

## Verified implementation

- Versioned, hash-bound manifests stage all groups before one commit epoch;
  complete-population reservations, failed-stage cleanup, immutable membership,
  retry handling and capability negotiation preserve single-generator affinity.
- One `MultiGroupEngine` owns source state and lifecycle. Group sinks and secret
  resolvers remain separate; shared telemetry is emitted once. Owner failures
  release every waiter, and shared resources drain with the owner.
- Encrypted batch parents preserve original source bytes, source identities,
  scripts, scenario and reviewed overlays. Preview digests bind exact results.
  Lease-fenced publication receipts prevent competing repair/save revisions.
- Restart recovery, stale revisions, stripped identities, mixed membership,
  source/workload preservation, independent credentials and concurrent repair
  and save are covered. Payload edits reject trailing JSON and invalid pointers.
- Browser repairs replace the whole batch and refresh review/preflight evidence;
  credentials remain reference-only and strict blockers remain enforced.

## Checks

- `go test -p 2 -timeout 30m ./...`: passed (the full API suite took 698 seconds).
- Latest focused API batch/manifest tests: passed after the final payload checks.
- Race tests: engine, generator RPC, import artifact/session packages and protected
  batch API tests passed. A loaded-host iteration-count failure passed isolated
  recheck and a subsequent complete generator race run.
- Kafka and Event Hubs packages with both real-adapter build tags: passed. These
  are adapter tests, not authorized live-service conformance.
- API/protobuf artifacts were regenerated through their repository generators;
  generated-artifact checks passed in the full Go run.
- Dashboard type checking, lint, embedded build and 4,476 unit tests passed.
- Dev-admin browser profile: 137 passed, four expected skips.
- Protected browser checks passed in dev-admin and local-auth modes. The latter
  binds separate Kafka/east/west references, saves/reopens the scenario, rejects
  a stale revision and verifies blocked launch.
- Full local-auth browser profile: 56 passed, one expected skip, one environmental
  failure (`ENOSPC` while writing a dashboard-analysis screenshot). After freeing
  reproducible cache space, that exact test passed on an isolated port/output
  directory. The original `npm run test:cycle` therefore exited nonzero; its sole
  remaining failed test passed the targeted rerun.

Concurrent embedded builds interrupted earlier Go compilations; a stable source
snapshot was used for the protected API race run. Reproducible old Go build-cache
objects were pruned when local disk pressure interrupted another build.

## Remaining evidence boundary

Authorized Kafka/OAuth and Event Hubs endpoints were unavailable. No live strict
parity claim is made. PostgreSQL service-level crash/failover testing was not run
against an external database; publication uses the existing protected-store
fence and asset saves use the existing transactional persistence contract.
