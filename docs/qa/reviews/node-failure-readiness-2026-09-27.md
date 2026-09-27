# Node-failure readiness: first implementation slice

Date: 2026-09-27. Branch: `codex/node-failure-readiness`.
Scope: initial fixes from [the implementation plan](../../NODE_FAILURE_READINESS_PLAN.md).
These results do not qualify combined worker/controller recovery for production.

## Changes

- Liveness reads locally held settings and cached generator inventory. It no longer
  refreshes settings from the database or performs generator RPCs. Stale inventory
  is omitted. The existing bounded durable-write bypass and runner-lock watchdog
  remain in place; an indefinitely wedged controller does not gain an unlimited
  healthy response.
- PostgreSQL archive provenance uses the exact microsecond representation produced
  by pgx. Archive identity and CI evidence checks accept older nanosecond manifests
  using that representation. Test ID, project ID, zero-timestamp compatibility,
  and SQLite's exact timestamp comparison retain their existing semantics.
- Local, EKS, on-prem, on-prem autoscaling, and Azure Monitor KEDA trigger lists now
  retain the reservation floor already present in AKS and Helm. Azure Monitor uses
  the same authentication reference as its other triggers. This protects retained
  StatefulSet ordinals; it does not reserve unused recovery pods.

## Validation

| Check | Result |
|---|---|
| `go test ./...` | Passed; API package completed in 344 seconds |
| Focused health, timestamp, HA archive, and CI archive tests | Passed |
| Focused race tests for health, timestamps, and provenance | Passed |
| Real PostgreSQL 16 archive reload | Passed, including microsecond boundaries and same-ID reuse rejection |
| Real PostgreSQL plus SeaweedFS 4.17 S3-compatible storage | Passed, including corrupt S3 manifest checksum and same-ID reuse rejection |
| KEDA reservation-floor parity tests | Passed |
| Kustomize render: local, local-kind, on-prem, on-prem HA, on-prem autoscaling, AKS, AKS HA, AKS Azure Monitor, EKS, EKS HA | Passed |
| Helm HA render with ordinary Prometheus and Azure Monitor | Passed; exactly one reservation-floor trigger in each |
| Documentation consistency and diff whitespace | Passed |

The integration tests create an isolated PostgreSQL schema and use fresh database
connections and HA server instances for the archive HTTP requests. S3 cases upload
an artifact and its SHA-256-bearing manifest, remove the writer's local directory,
and fetch through the application. They cover nanosecond fractions 135, 999, and
001 around a microsecond boundary, timezone equivalence, canonical new provenance,
older manifest compatibility, same-ID reuse, and rejection of an incorrect S3
manifest checksum. Controller construction/reload is exercised; an actual elected
leadership transfer is a separate fault-matrix gate.

Disposable PostgreSQL and SeaweedFS containers use fresh credentials and loopback
ports and are removed after each run. The S3 endpoint used a wildcard DNS name
resolving to loopback because the existing adapter uses virtual-hosted bucket URLs.
No existing Docker Compose or Kubernetes workloads were changed by these tests.

To rerun the integration cases against disposable services, set
`APEXLOAD_TEST_POSTGRES_URL`. For S3, also set `APEXLOAD_TEST_ARCHIVE_S3=1`,
`APEXLOAD_ARCHIVE_BUCKET`, `APEXLOAD_ARCHIVE_REGION`, `APEXLOAD_ARCHIVE_ENDPOINT`,
and the standard AWS test credentials. The bucket must exist and its virtual-hosted
name must resolve. Run:

```sh
go test -tags postgres_integration ./pkg/api \
  -run 'TestPostgres(S3)?ArchiveIdentityAfterReload' -count=1 -v
```

## Remaining release gates

The repository-owned node-fault harness, ten-minute dependency outage deployment
check, scoped HA autoscaling signals, reserved spare-capacity admission, combined-loss
checkpoint/fencing recovery, storage runbook, and terminal partial-result reporting
remain open. The original two recorded S3 archives have not yet been replayed through
an updated deployed controller. There has been no repeat of the multi-node fault
matrix with these binaries, and no multi-host, CSI, PostgreSQL-primary, or Redis-primary
failover qualification.

## Review/fix cycle

The first review found that the filesystem manifest loader ignored a persisted
checksum and that a future-dated inventory snapshot could be reported as current.
The loader now verifies a nonempty catalog checksum before decoding the manifest;
legacy records without a checksum remain readable. Health capacity observations
reject future timestamps as well as expired timestamps. The integration regression
now rejects corrupt catalog checksums for both filesystem and S3 archives.

The S3 integration test also registers cleanup before upload and bounds the upload
attempt, including failure paths. A second source review found no additional issues
in this slice. Post-review validation passed: API full suite (370 seconds), focused race tests,
PostgreSQL/filesystem/S3 integration checks, deployment tests, and documentation checks.
The full-suite inventory guard found an undocumented integration switch; its entry
was added to `docs/qa/TEST_PROGRAM.md` and the guard passed on rerun. Fleet-scoped KEDA
demand remains a separately tracked implementation batch.


## Fleet autoscaling review/fix cycle

Fleet demand now comes only from a reconciled execution leader, carries an installation
ID and expires at the earlier of 30 seconds or its lease deadline. Every shipped KEDA
query filters expiry and selects that fleet before deduplicating with `max`. Missing
observations fail the metric instead of representing zero demand. Legacy diagnostic
metrics remain available. Total demand includes queued stage/adaptive admission peaks.

Review fixed stopped/shakeout demand disappearing too early, under-reported queued
peaks, misplaced ConfigMap identity wiring, and cloud renderers that broke inherited
HA overlay paths. Renderers now expand overlays before replacing their placeholders.
A second review found no further issues in this slice. Reserved recovery capacity and
combined-loss adoption remain separate unfinished work.

Validation: focused API tests passed (1.433 seconds); focused race tests passed
(3.055 seconds); deployment tests and the complete scripts suite passed (24.693
seconds for scripts). Real Prometheus v3.12.0 `promtool test rules` passed all six
query families, duplicate HA, stale observations, independent fleets and expiry.
The later attempted repeat could not start because Docker Desktop's containerd
metadata filesystem became read-only; this is a local runtime blocker, not a passing
repeat. No Docker restart or cleanup of shared workloads was attempted.

The broad API run found one existing asynchronous-test race: the tracing test read
the exporter immediately after the start response, before execution ended its span.
It now waits for that ended span with a five-second bound, preserving all trace,
parentage and disclosure assertions. Thirty repeated runs passed (2.434 seconds).
The broad API rerun is recorded separately once complete.


## Fault-driver and storage review/fix cycle

The storage runbook was checked against the local, AKS, EKS, on-prem and Helm storage
contracts and generator epoch/supervision persistence. It distinguishes same-identity
reattachment from new-identity replacement and leaves CSI/zone RTO/RPO unqualified.

The repository fault driver now refuses unowned namespaces, mismatched kind clusters,
control-plane victims, stale/future observations, duplicate shard ownership, absent
KEDA demand and unsustained target traffic. It restores the immutable container ID
on timeout, interruption and failed recovery. Review fixed a false-pass risk by
requiring sustained traffic while the failed node remains stopped, reset the target
comparison at injection, and removed arbitrary nested assignment fields from evidence.
Fresh input generation refuses overwrite, produces a new CA and credentials, and
keeps directory/files at 0700/0600. Nine Python regressions pass, including actual
OpenSSL verification; the Go scripts test invokes these regressions as well.
A second review found no additional issues in this foundation's documented scope.

This is not a completed end-to-end harness: the application stack provisioner, real
observation adapter, complete HPA/final-archive evidence and deployed three-trial
matrix remain open. Docker's read-only metadata filesystem prevents runtime fault
validation here. The driver never force-deletes a StatefulSet identity or PVC.
