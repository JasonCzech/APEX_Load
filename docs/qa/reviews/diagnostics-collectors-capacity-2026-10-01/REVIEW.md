# Diagnostics, collectors and preparation review — 2026-10-01

Scope: explainable statistical findings and error-pattern delivery; dependency-to-monitor interval drilldown; authenticated OTLP and pinned SSH collection; restricted JMX/PostgreSQL helpers; immutable preparation capacity policies. Unrelated browser-recorder changes in the shared checkout were preserved.

## Findings fixed

- Advisory notifications could inherit threshold-failure wording. Advisory explanations now survive notification routing and remain separate from stop decisions.
- Dependency links selected a metric without selecting its observation interval. Both API filters and dashboard overlays now use that UTC window. Mixed-quality evidence and bounded historical scans expose incomplete coverage.
- Latest dependency evidence was fetched only once. It now refreshes every five seconds without overlapping requests; historical windows remain fixed.
- OTLP sampling could miss delayed points or replay accepted points. Collection now includes retained late arrivals, with per-run acknowledgements persisted alongside pending batches and isolated across overlapping runs.
- OTLP HTTP success preceded durable receipt. The receiver now requires spool ownership and a recovered, atomically replaced and synced journal before acknowledging ingestion.
- Canonical metadata could expand accepted OTLP exports beyond the controller request limit. Collection now splits them into bounded batches, reserves room for renewed signed leases, and drains the retained remainder without waiting for another collection interval. The regression and collector race tests pass.
- Adapter units were inconsistent. Accepted scalar units are normalized; unsupported units and metric semantics are explicitly rejected, with rejected point counts.
- Missing credentials could block polling for every job on a collector. The controller now returns a signed unavailable status for the affected job while allowing other jobs to proceed.
- Required source credentials were not checked during preparation readiness. They are now resolved without target traffic before readiness, renewal and Start; unsupported credential-bearing packs are rejected.
- Fresh wall-clock buckets could span a pause. Qualification now also checks active-runtime advancement in live and archived baselines.
- The new endpoint lacked a pinned authorization fingerprint. Its project-viewer and results-read access contract is now included in the authorization snapshot.
- Archived statistical windows lost stale-observation qualification, and the finding cap could be exceeded by a multi-metric append. JSONL/Parquet summaries now retain the observation state and the cap is enforced exactly.
- Preparation capacity selection persisted across run changes. It now resets with the run, and the new selects expose explicit accessible labels.
- Ephemeral provider reconciliation could touch standing capacity. Explicit ephemeral plans isolate burst ownership and enforce ECS spot ceilings/on-demand floors; unsupported required policies fail.
- A standalone retry could lose a run's immutable capacity policy. Run-owned requests now require a new preparation/run instead of the standalone provisioning retry path.

## Verification

- Collector and provider unit/race checks pass, including real SSH transport with rejected host-key substitution, sealed credential identity/expiry checks, durable OTLP restart/deduplication, and ECS spot allocation/standing isolation.
- Real authenticated TLS JMX and PostgreSQL fixtures pass, including credential rejection and arbitrary query-pack rejection. The JDBC fixture verifies the pinned driver checksum before use.
- SQLite storage and PostgreSQL integration/migration checks pass.
- All 442 dashboard unit files / 4,663 tests pass on the stable source tree (`--maxWorkers=2 --testTimeout=30000`). Earlier broad runs exposed load-sensitive timeouts; isolated retries passed.
- Typecheck, lint, dashboard build and documentation checks pass. Generated API artifacts and the QA contract inventory are refreshed through repository generators.
- Fresh-controller dev-admin and local-auth browser checks pass for preparation policy submission/renewal/Start, Parquet workflow, dependency interval drilldown, and gRPC/WebSocket evidence (5 tests per profile).
- Full browser cycle ran both profiles: dev-admin 172 passed / 3 failed / 4 skipped; local-auth 94 passed / 1 failed / 1 skipped. The preparation failures used an earlier embedded bundle and pass against a fresh final build in both profiles. Live-metrics and library-assignment failures pass unchanged on a fresh-controller retry (3 passed / 1 profile-specific skip). No failed browser check remains unverified.
- Full Go suite passes: `APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -ldflags=-w -timeout 20m -p 1 ./...` exited successfully, including the API authorization snapshot and generated-contract checks. The final collector batch-bound fix also passes `go test -ldflags=-w -race -p 1 ./pkg/collectoragent`.

## Acceptance limits

The helper intentionally supports fixed JVM attributes and a PostgreSQL connection-health query, not arbitrary JMX invocation or SQL. OTLP ingestion accepts bounded HTTP JSON gauges and cumulative monotonic sums; other metric semantics are explicitly rejected. Receiver retention is five minutes and 512 points per approved stream.

Ephemeral capacity requires a supported asynchronous capacity backend. Kubernetes/static targets retain existing-capacity preparation; unsupported ephemeral or required spot requests fail explicitly. Cloud-provider live acceptance across every runtime/target remains outstanding. Local deterministic tests do not establish cloud production readiness. The separately requested 10 GB Parquet benchmark remains skipped at the user's direction.

## Final review outcome

Repeated review and fix passes found no remaining actionable findings in the implemented scope. Acceptance limits above remain explicit.
