# Local Docker Monitoring

This compose stack runs the APEX Load controller and a local Prometheus server.
The controller exposes the dashboard/API on `8080` and a dedicated metrics
listener on `9091`; Prometheus scrapes `controller:9091` inside the compose
network.

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
