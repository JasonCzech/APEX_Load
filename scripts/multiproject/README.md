# Multi-project concurrency suite

This suite runs load tests from several projects at the same time against one
deployment. It checks that admission, queueing, isolation and results stay
correct for each project. The unit tests in `pkg/api` cover the admission rules
with hand-set statuses; this suite covers the same rules with real runs, real
generators and real traffic.

The suite is a black-box Go test (`concurrency_test.go`, build tag
`multiproject_integration`) that uses only the REST API. `stack.py` starts a
disposable Docker stack for it. The same test runs against an existing AKS
installation by pointing it at that installation instead (see
[Running against AKS](#running-against-aks)).

## What it checks

Three tenants each get their own project, script, project secret and a
service-account key scoped to that project only. That is how separate teams'
CI pipelines would use one installation.

| Tenant | Project limit (`max_concurrent_tests`) |
|---|---|
| alpha | 1 |
| bravo | 3 |
| charlie | 3 |

Every request goes to `apexfault-target`, which counts requests per run ID.
That count does not depend on the controller, so it independently confirms
which run sent each request.

A run counts as running only when it is `RUNNING` **and** the target has
counted its requests. A distributed run reports `RUNNING` about 2.5 seconds
before its coordinated start barrier, so status alone does not mean load is
flowing. A run stopped before its barrier must have sent nothing, and the suite
checks that too.

| Phase | Passes when |
|---|---|
| Projects launch at the same moment | alpha, bravo and charlie start in the same instant, all three are `RUNNING` in one sample, and every run's target count rises in the same window. |
| Tenants cannot see or control each other's runs | alpha's run list holds only alpha's runs. alpha gets 403 or 404 when it reads, stops or aborts bravo's or charlie's runs, and those runs keep sending traffic. |
| A full project queues only its own work | alpha's second run is `PENDING` ("project test slot") while bravo's second run starts. Stopping alpha's first run promotes the queued one with no further request. |
| The global queue promotes the least-loaded project first | With the global limit at 4 and full, bravo (2 running) queues an older run and charlie (1 running) queues a newer one. Freeing alpha's slot promotes charlie's newer run; first-in-first-out would have picked bravo's. The queue then drains. |
| Two projects race for the last global slot | With the global limit at 3 and 2 slots taken, alpha and charlie start at the same instant. Exactly one is admitted and the other queues, then takes the slot when it frees. This repeats `APEXLOAD_MP_RACE_ROUNDS` times. |
| Every run's results belong to that run alone | Each run's archived request count matches the target's count for that run ID, with zero request errors. No run's results name another tenant's request. Another tenant cannot read the report. |
| Every controller replica reports the same outcomes | Both HA replicas, read directly, agree on every run's final status and project. |
| Admission limits held in every sample | A sampler polls the active-test list every 400 ms for the whole suite. No sample exceeds the global limit or any project limit. |

The two global phases lower the deployment-wide limit, so they run only with
`APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS=1`. `stack.py` always sets it, because the
stack is its own. The suite restores the original limit when it finishes.

Evidence goes to `evidence/report.json` whether the suite passes or fails. It
holds the controller version, limits, each run's status transitions, generator
placement, reported and observed counts, and the sampler's maximums and any
violations.

## Local Docker stack

```
            127.0.0.1 (ephemeral ports)
                 │
             router (HAProxy) ──── execution paths ──▶ admitted leader only
                 │                 everything else ──▶ both replicas
       ┌─────────┴─────────┐
  controller-1        controller-2      APEXLOAD_ENV=production, HA lease,
       │  └──────┬──────┘  │            trusted-header auth
       │   PostgreSQL · Redis · S3 (SeaweedFS)
       │
  dns://apexload-generator ── gRPC, mutual TLS + token ──▶ generator-1..3
                                                              │
                                                       target (apexfault-target)
```

- The router reproduces the AKS ingress split. Its execution routes are generated
  from `deployments/k8s/aks/controller-ingress.yaml`, so they stay in sync. Those
  routes reach only the controller whose `/api/v1/system/execution-readiness`
  passes, which is the job of the `apexload-controller-active` EndpointSlice on AKS.
- `stack.py` generates every credential fresh: the proxy secret, gRPC token,
  wrapping key, database and Redis passwords, S3 keys, target tokens, and a
  two-day CA. The CA key is deleted once the certificates are signed.
- Trusted-header auth is seeded into PostgreSQL before the controllers start,
  because a production controller refuses to boot in the default bypass mode.
  The admin is the pinned bootstrap subject `mp-admin`.
- Once the controllers are up, `stack.py` sets capacity availability to
  `configured_plus_live` with three configured generators, as an operator would
  for this fleet. A fresh install assumes one generator.
- Settings reads balance across both replicas, and an update reaches the other
  replica after about a second. The suite and `stack.py` therefore retry a
  settings update that returns 412, as the API instructs.

### Requirements

- Docker Desktop with about 6 GB of memory available to containers.
- At least 15 GB of free disk for the first image build (`--min-free-gib`).
- Go, Python 3 and OpenSSL on the host.
- No other heavy Docker or Go work running at the same time. The build is
  CPU-bound and the suite measures concurrency.

### Commands

Run everything (build images, start the stack, run the suite, remove the stack):

```bash
python3 scripts/multiproject/stack.py run
```

Keep a stack running while you work on the suite:

```bash
python3 scripts/multiproject/stack.py up
```

`up` prints the follow-up commands:
`stack.py test --stack <work-dir>` runs the suite against it, and
`stack.py down --stack <work-dir>` removes it.

To reuse images you already have, for example the images deployed to AKS,
instead of building them:

```bash
python3 scripts/multiproject/stack.py run \
  --controller-image <ref> --generator-image <ref> --target-image <ref>
```

The first image build compiles the dashboard and both Go binaries and takes
the longest (about 15 minutes cold). Starting the stack takes two to three
minutes, and the suite itself about one minute.

### Outputs and cleanup

The work directory (a fresh temporary directory, or `--work-dir`) keeps:

- `evidence/report.json`: the suite's evidence.
- `logs/go-test.log`, `logs/compose-services.log`, `logs/build.log`, and the
  compose up/down logs.

Removing the stack deletes its containers, network and volumes. It also deletes
the images this run built and any dependency images this run pulled
(`--keep-images` keeps them), and it deletes every credential file. It then
checks that nothing labelled with the compose project remains.

It does not prune the BuildKit cache, because that cache is shared with any
other build on the machine. Reclaim it with `docker builder prune` only when no
other build is running.

### Driver unit tests

These start no containers:

```bash
python3 -m unittest discover -s scripts/multiproject -p 'test_*.py'
```

They check the ingress-derived routes, the generated configs, the
certificates, and that the driver, `compose.yaml` and the Go suite agree on
every variable name.

## Suite settings

These are the variables the Go suite reads. When you run `go test` directly,
set them yourself to point the suite at a deployment.

`stack.py` sets them from its own stack (`--stack`) or from the JSON file
(`--external`), and it ignores any `APEXLOAD_MP_*` variable already exported
in your shell, so a leftover `suite.env` cannot redirect a run or let it change
global settings. The exceptions are the load-shape settings `APEXLOAD_MP_VUSERS`,
`APEXLOAD_MP_PACING_MS`, `APEXLOAD_MP_RACE_ROUNDS` and the two
`APEXLOAD_MP_COUNT_TOLERANCE_*` settings: those pass through from your shell,
though the external file's own values take precedence.

| Variable | Meaning |
|---|---|
| `APEXLOAD_MP_BASE_URL` | API base URL (router, ingress, or port-forward). The suite skips when this is unset. |
| `APEXLOAD_MP_CONTROLLER_URLS` | Comma-separated direct URLs of each controller replica, for the replica-consistency phase (optional). |
| `APEXLOAD_MP_ADMIN_API_KEY` | Admin service-account key (needs `system_admin`; scopes `*` or at least the ones the setup calls use). |
| `APEXLOAD_MP_PROXY_SECRET`, `APEXLOAD_MP_ADMIN_SUBJECT` | Trusted-header admin, as an alternative to the key. |
| `APEXLOAD_MP_TARGET_URL` | The target as generators reach it. |
| `APEXLOAD_MP_OBSERVER_URL` | The same target as this process reaches it. |
| `APEXLOAD_MP_TARGET_RUN_ID`, `APEXLOAD_MP_TARGET_LOAD_TOKEN`, `APEXLOAD_MP_TARGET_OBSERVER_TOKEN` | The target's `target.json` values. |
| `APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS` | `1` allows the global-limit phases. Use it only on a dedicated, otherwise idle deployment. |
| `APEXLOAD_MP_EVIDENCE_DIR` | Where `report.json` goes. |
| `APEXLOAD_MP_PREFIX` | Name prefix for created projects and keys (default `mp-<time>`). |
| `APEXLOAD_MP_VUSERS`, `APEXLOAD_MP_PACING_MS` | Load per run (default 4 VUsers, one request each per 250 ms). |
| `APEXLOAD_MP_MIN_GENERATORS` | Live generators required before starting (default 2). |
| `APEXLOAD_MP_RACE_ROUNDS` | Rounds of the last-slot race (default 3). |
| `APEXLOAD_MP_COUNT_TOLERANCE_REQUESTS`, `APEXLOAD_MP_COUNT_TOLERANCE_RATIO` | Allowed difference between reported and target counts (default the larger of 2 requests or 0.5%). |
| `APEXLOAD_MP_DELETE_PROJECTS` | `1` deletes the created runs and projects at the end. Service-account keys are always deleted. |

## Running against AKS

The suite needs nothing from Docker, so moving to AKS is a matter of
configuration. `stack.py test --external <config.json>` maps a JSON description
to the variables above and keeps the evidence in `--evidence-dir`:

```json
{
  "profile": "aks",
  "base_url": "https://apexload.example.com",
  "controller_urls": ["http://127.0.0.1:18101", "http://127.0.0.1:18102"],
  "admin_api_key": "<system_admin service-account key>",
  "target_url": "http://apexload-mp-target.apexload.svc:8090",
  "observer_url": "http://127.0.0.1:18090",
  "target_run_id": "<16 hex characters>",
  "target_load_token": "<32-128 URL-safe characters>",
  "target_observer_token": "<different 32-128 URL-safe characters>",
  "mutate_global_settings": false
}
```

```bash
python3 scripts/multiproject/stack.py test --external aks.json --evidence-dir /absolute/new/dir
```

Unknown keys are refused, so a misspelled `mutate_global_settings` cannot
silently skip or enable a phase.

What the AKS run still needs (not yet provided here):

1. **A counting target in the cluster.** Run the `cmd/apexfault-target` image
   as a Deployment and Service, with its `target.json` mounted from a Secret.
   Generators must be able to reach it. If you applied the opt-in generator
   egress policies, allow this Service.
   `scripts/fault_harness/render_target.py` renders a hardened target, but only
   for the fault-harness namespace.
2. **Observer access** to that target from where the suite runs, for example
   `kubectl port-forward svc/apexload-mp-target 18090:8090`.
3. **An admin key**: a service account with `system_admin`, created once from
   **Access**. The suite creates and deletes its own tenant keys.
4. **Per-replica URLs** for the replica-consistency phase: a port-forward to each
   controller pod. Without them that phase is skipped.
5. **A decision on the global phases.** On a shared cluster leave
   `mutate_global_settings` false. The suite then runs the project-level phases
   and checks the sampler against the deployment's own global limit. On a
   dedicated test cluster set it to true.

On a shared cluster, free global slots are checked only when each phase
starts. If other teams fill them during a phase, a run that should start can
queue instead, and the phase fails for a capacity reason rather than a defect.
The evidence shows which runs queued and why.

## Differences from AKS

| AKS | Local stack |
|---|---|
| Azure Blob archives (workload identity) | S3 archives on SeaweedFS (static keys) |
| Azure Database for PostgreSQL and Azure Cache for Redis, both over TLS | PostgreSQL and Redis containers, plaintext inside the Docker network |
| OIDC or a gateway in front of the ingress | Trusted headers sent by the suite with the proxy secret |
| Active EndpointSlice published by the leader | HAProxy health check on `/api/v1/system/execution-readiness` |
| KEDA-scaled generator StatefulSet on a dedicated node pool | Three fixed generator containers on one host |
| Key Vault for script secrets | Project secrets in PostgreSQL, wrapped by the generated master key |

Everything else matches the production manifests: production posture, two HA
controllers, `dns://` generator discovery, mutual-TLS gRPC with a token,
read-only root filesystems, and non-root users.
