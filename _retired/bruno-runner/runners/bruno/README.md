# APEX Bruno compatibility runner

This service is the versioned execution boundary for canonical
`protocol: "bruno"` scripts. It is pinned to Bruno 3.5.2 compatibility and
uses Bruno's own parser, HTTP/GraphQL runner, JavaScript runtimes, request
libraries, assertions, and tests.

Each APEX VUser owns one persistent worker-thread session. Safe sessions use
Bruno's QuickJS runtime. Developer sessions are rejected unless the service is
running in an operator-enabled isolated worker image with
`APEXLOAD_BRUNO_DEVELOPER_WORKER=true`.

Sessions fail closed unless `APEXLOAD_BRUNO_EGRESS_BROKER_URL` is configured.
Direct egress is available only for isolated local conformance testing through
the explicit `APEXLOAD_BRUNO_ALLOW_DIRECT_EGRESS=true` override. Production
deployments must deny direct worker egress at the pod/container network
boundary as a second enforcement layer.

`APEXLOAD_BRUNO_MAX_SESSIONS` bounds concurrent persistent VUser sessions
(default 256). `APEXLOAD_BRUNO_MAX_PENDING_EVENTS` bounds the per-execution
NDJSON result backlog (default 10,000); overflow cancels the run explicitly
instead of dropping child results.

Run `pnpm run sbom` after dependency changes to refresh the checked-in
CycloneDX dependency manifest included in the runner image.
