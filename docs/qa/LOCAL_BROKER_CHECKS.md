# Manual local broker verification

Run the real Kafka and Solace adapters without GitHub Actions or cloud accounts:

```sh
python3 scripts/run-local-broker-integration.py --output /tmp/apexload-brokers-$(date +%s)
```

This is a manual command; it does not install a schedule. Docker Compose and Go
are required. Fetch and integrate current `main` before running it, following the
repository testing policy. The output directory must be new. Use `--profiles
kafka` or `--profiles solace` to run one broker.

The runner uses the digest-pinned images in
[brokers.compose.yaml](reviews/import-fixture-matrix-2026-09-12/brokers.compose.yaml).
It owns a unique Compose project and refuses occupied host ports 19092 (Kafka),
18090 (Solace management) and 15672 (Solace AMQP). The Kafka override adds a
container-only administration listener so both host tests and topic provisioning
can reach the advertised broker. The Solace fixture uses synthetic credentials
and an explicitly provisioned queue.

Each package runs with its real adapter and integration build tags. A pass
requires the live produce/consume test for Kafka or live publish test for Solace
to execute successfully; a skipped test is not a pass. The runner retains JSON
test output, fixture logs and `receipt.json`, then removes only its own containers
and network. Images and unrelated Docker resources are preserved. A teardown
failure makes the command fail and leaves its project name in the fixture log.

These checks establish basic adapter interoperability with the pinned local
brokers. They do not qualify Kafka's separate transaction/seek suites, other
broker versions, high availability, or Azure Service Bus/Event Hubs. Those lanes
need their own fixtures and receipts. Record a successful run in
[execution-status.json](execution-status.json) with a dated evidence link, then
regenerate the [QA dashboard](DASHBOARD.md):

```sh
python3 scripts/qa-generate-dashboard.py
```

The dashboard separates static test coverage from actual executions and marks
old successes as stale. Missing accounts, failed runs and unexecuted lanes must
remain explicit.
