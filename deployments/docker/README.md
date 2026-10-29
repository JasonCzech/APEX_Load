# Local Docker Monitoring

This Compose stack (`compose.yaml`) runs:

- `controller` (`apexload-controller:local`): exposes the dashboard/API on `127.0.0.1:8080` and a dedicated metrics listener on `127.0.0.1:9091`, discovering the local load generator via `APEXLOAD_GENERATORS=dns://generator:9090`.
- `generator` (`apexload-generator:local`, ID `apexload-generator-local`): listens on internal gRPC port `9090` (`APEXLOAD_GENERATOR_MAX_VUSERS=1000`) and persists fencing state and terminal evidence in the `generator-data` volume.
- `bruno-artifacts-init`: one-shot initializer setting `65532:65532` (`0700`) ownership on the `bruno-artifacts` volume before the controller starts.
- `prometheus` (`prom/prometheus:v3.12.0` on `127.0.0.1:9090`): scrapes `controller:9091` inside the Compose network.
- `java-vuser-runner` (opt-in `--profile java-vuser`): credential-minimized loopback sidecar sharing the generator's network namespace (`network_mode: service:generator`, `127.0.0.1:8098`).

## Start

```bash
docker compose up --build -d
```

Or use the Make target:

```bash
make docker-local-up
```

Open:

- APEX Load dashboard: `http://127.0.0.1:8080`
- Controller metrics endpoint: `http://127.0.0.1:9091/metrics`
- Prometheus UI: `http://127.0.0.1:9090`
- Prometheus targets: `http://127.0.0.1:9090/targets?search=apexload`

Useful starter PromQL:

```promql
up{job="apexload-controller"}
apexload_up
apexload_test_count
apexload_scaling_vusers
```

## Stop

```bash
docker compose down
```

Use `docker compose down -v` when you also want to remove the local SQLite,
archive, and Prometheus data volumes.

## Configuration

Prometheus reads `deployments/docker/prometheus/prometheus.yml`.

The compose stack stores controller state in the `controller-data` Docker volume:

- SQLite DB: `/data/apexload.db`
- Run archives: `/data/archives`
- Encrypted Project Secrets: SQLite records wrapped by
  `APEXLOAD_WRAPPING_MASTER_KEY`

Generator fencing state and terminal evidence are retained in the
`generator-data` volume. The local stack uses the same mandatory 60-second
run-supervision TTL, 10-second renewal, and 5-second abandonment drain as the
production manifests. See [Generator run supervision](../../docs/ENTERPRISE_FLEET_AND_CAPACITY.md#generator-run-supervision)
for the failure drill and why Compose relies on process exit/restart rather than
a controller-connectivity healthcheck.

Prometheus time-series data is stored in the `prometheus-data` Docker volume.

The compose default wrapping key is intentionally local-development-only.
Override it with a stable value before saving secrets you need to retain:

```bash
export APEXLOAD_WRAPPING_MASTER_KEY="$(openssl rand -base64 32)"
docker compose up --build -d
```

Keep the same value while the `controller-data` volume exists. The local
controller/generator channel is plaintext, so secret-bearing distributed runs
fail closed; use the production Helm/on-prem/AKS TLS wiring when validating
distributed Project Secrets.

## Opt-in Java Vuser Runner Profile

Starting the `--profile java-vuser` service attaches `java-vuser-runner` to the
`generator` network namespace on `127.0.0.1:8098` and mounts precompiled
classes/JARs read-only from `${APEXLOAD_JAVA_VUSER_ASSETS_DIR:-./runners/java_vuser/assets}`.
Profile selection alone intentionally leaves Java execution disabled; pass both
`APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true` and a shared loopback bearer token:

```bash
export APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true
export APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN="$(openssl rand -hex 32)"
docker compose --profile java-vuser up --build -d
```

Because the stock non-privileged container lacks writable cgroup v2 delegation,
it advertises legacy `full_lifecycle` execution only (see
[Java Vuser execution](../../docs/PROTOCOL_SUPPORT.md#java-vuser-execution)).

## Opt-in PostgreSQL and Redis

The data-platform override adds a dedicated PostgreSQL 16 metadata store and
Redis 7 cache/coordination service without changing the default SQLite and
in-memory-cache workflow. The services are reachable only inside this Compose
project; they do not publish host ports and do not conflict with other local
PostgreSQL or Redis containers.

Start the opt-in stack with:

```bash
make docker-local-data-up
```

This is equivalent to:

```bash
docker compose \
  -f compose.yaml \
  -f deployments/docker/compose.data-platform.yaml \
  up --build -d
```

The first start pulls `postgres:16-alpine` and `redis:7-alpine`. PostgreSQL
schema initialization is performed automatically by the controller. The
`postgres-data` and `redis-data` named volumes preserve service data across
ordinary container restarts and `make docker-local-data-down`.

The checked-in passwords are local-development defaults and must not be used in
production. Override them in the repository's ignored `.env` file when needed;
use URL-safe values because the passwords are embedded in connection URLs:

```dotenv
APEXLOAD_LOCAL_POSTGRES_PASSWORD=replace-with-a-url-safe-local-password
APEXLOAD_LOCAL_REDIS_PASSWORD=replace-with-a-url-safe-local-password
```

For Redis, change the password in `.env` and rerun
`make docker-local-data-up`. Compose recreates Redis and the controller with the
new password, and the existing `redis-data` volume remains usable; do not reset
that volume just to change the password.

For PostgreSQL, `POSTGRES_PASSWORD` is applied only when a new database volume
is initialized. If `postgres-data` already exists, either restore the password
that initialized it or reset only the PostgreSQL development volume. The
following procedure stops the merged stack, resolves the project-scoped volume
name from Compose, removes only PostgreSQL data, and starts a fresh database:

```bash
make docker-local-data-down
docker volume rm "$(
  docker compose -f compose.yaml -f deployments/docker/compose.data-platform.yaml \
    config --format json | jq -r '.volumes["postgres-data"].name'
)"
make docker-local-data-up
```

This targeted reset permanently deletes the PostgreSQL metadata store. It does
not delete the retained SQLite/archive, Redis, Bruno, or Prometheus volumes.

Verify the selected backends:

```bash
curl -fsS http://127.0.0.1:8080/api/v1/system/readiness | jq \
  '.checks[] | select(.name == "metadata_store" or .name == "cache_backend")'

docker compose -f compose.yaml -f deployments/docker/compose.data-platform.yaml \
  exec postgres sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" psql -h postgres \
    -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "select current_database();"'

docker compose -f compose.yaml -f deployments/docker/compose.data-platform.yaml \
  exec redis sh -c 'redis-cli -h redis --no-auth-warning -a "$REDIS_PASSWORD" ping'
```

Readiness should report `store_type: postgres` and `cache_backend: redis`.
Follow service logs with `make docker-local-data-logs` and stop the opt-in stack
with `make docker-local-data-down`.

To discard every volume declared by the merged stack and start completely
fresh, use the following only when deletion of the retained SQLite/archive,
PostgreSQL, Redis, Bruno, and Prometheus data is intentional:

```bash
docker compose -f compose.yaml -f deployments/docker/compose.data-platform.yaml down -v
```

To roll back without deleting anything, stop the opt-in stack and start the
base stack. The controller resumes using the preserved SQLite database and
in-memory cache:

```bash
make docker-local-data-down
make docker-local-up
```

## Running Prometheus Against a Host Controller

If you run `go run ./cmd/controller` outside Docker and only want Prometheus in a
container, start the controller with a dedicated metrics listener:

```bash
APEXLOAD_METRICS_ADDR=:9091 go run ./cmd/controller
```

Then change the target in `deployments/docker/prometheus/prometheus.yml` from
`controller:9091` to `host.docker.internal:9091` and run only the Prometheus
service:

```bash
docker compose up -d prometheus
```
