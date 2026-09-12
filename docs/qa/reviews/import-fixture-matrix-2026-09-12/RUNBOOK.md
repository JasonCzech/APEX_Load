# Reproducing the import fixture matrix

Run from the repository root. The [manifest](../../import-fixture-matrix.json) defines each case's source, assertions, command and prerequisites. The Python runner records hashes and distinguishes failed, skipped/partial and unavailable checks; exit 0 means no executed case failed, **not** that every provider is certified. Inspect `results.json` for blocked rows.

## Local prerequisites

Use the repository Go toolchain and install the pinned dependencies in `runners/bruno` (`pnpm install --frozen-lockfile`). Node 24.13.0 and Go 1.26.5 were used for this run. Canonical replay starts its own loopback-only runner and SOAP target and uses a temporary encrypted artifact store. Its direct-egress override applies only to those test processes.

Fetch the public source corpus with the repository's pinned fetch script. Choose a new destination; that script replaces a non-Git destination directory.

```sh
scripts/fetch-lr-scripts-corpus.sh /tmp/apex-fixture-public-corpus
export APEXLOAD_LR_SCRIPTS_CORPUS=/tmp/apex-fixture-public-corpus
```

For offline cases only:

```sh
python3 scripts/run-import-fixture-matrix.py --output /tmp/apex-fixture-evidence-offline
```

The output directory must be new. `--only CASE_ID` selects one case and can be repeated. Node/browser-independent prerequisites are evaluated from actual test results: a skipped canonical replay cannot become a passing matrix row.

## Disposable live Kafka and Solace

The included [Compose file](brokers.compose.yaml) pins the exact images used in this validation: Kafka 4.2.0 and Solace 10.26.0.8827. Its credentials are intentionally synthetic and its exposed ports bind only to loopback. It consumes up to 3 GB of container memory plus build/test resources.

```sh
docker compose -p apex-fixture-matrix -f docs/qa/reviews/import-fixture-matrix-2026-09-12/brokers.compose.yaml up -d
```

Wait for Solace's SEMP endpoint to return 200 and Kafka to report started in the container logs. Configure the disposable VPN, client and empty queue. Run these creation requests once per new broker:

```sh
curl --fail --user admin:fixture-admin --header 'Content-Type: application/json' \
  --request PATCH --data '{"authenticationBasicType":"internal"}' \
  http://127.0.0.1:18090/SEMP/v2/config/msgVpns/default
curl --fail --user admin:fixture-admin --header 'Content-Type: application/json' \
  --data '{"queueName":"apex-fixture-roundtrip","accessType":"exclusive","permission":"consume","ingressEnabled":true,"egressEnabled":true}' \
  http://127.0.0.1:18090/SEMP/v2/config/msgVpns/default/queues
curl --fail --user admin:fixture-admin --header 'Content-Type: application/json' \
  --data '{"clientUsername":"fixture-client","password":"fixture-client","enabled":true,"clientProfileName":"default","aclProfileName":"default"}' \
  http://127.0.0.1:18090/SEMP/v2/config/msgVpns/default/clientUsernames
```

Run the complete matrix. Missing cloud/customer prerequisites remain blocked:

```sh
SOLACE_AMQP_URL=amqp://127.0.0.1:15672 \
SOLACE_QUEUE=apex-fixture-roundtrip \
SOLACE_USERNAME=fixture-client SOLACE_PASSWORD=fixture-client SOLACE_VPN=default \
KAFKA_BROKERS=127.0.0.1:19092 \
python3 scripts/run-import-fixture-matrix.py --include-live --output /tmp/apex-fixture-evidence-live
```

Kafka creates a unique one-partition topic and deletes that topic in cleanup. The Solace queue must be dedicated and empty before the case; a leftover or unrelated message causes an exact-body failure instead of a false pass. Do not point these cases at shared application queues.

After testing, remove only this fixture project and its volumes:

```sh
docker compose -p apex-fixture-matrix -f docs/qa/reviews/import-fixture-matrix-2026-09-12/brokers.compose.yaml down --volumes
```

The official setup references are [Apache Kafka Docker](https://kafka.apache.org/42/getting-started/docker/) and [Solace's first broker setup](https://docs.solace.com/Get-Started/tutorial/event-broker-set-up.htm). Solace AMQP selects the VPN by [per-VPN listen port](https://docs.solace.com/Services/AMQP-on-VPN-Management.htm) or [TLS virtual hostname on a global port](https://docs.solace.com/Services/Managing-Services.htm); it does not require the adapter to append a VPN to the SASL username.

## Adding customer/provider evidence

Record source-tool/export version, sanitized fixture location and SHA-256, original-tool expected behavior, and any permitted local substitutions. Keep provenance explicit: the three attached repository SOAP assets have preserved source bytes but no independently supplied customer execution baseline. Public LR_Scripts assets are public examples, not customer evidence.

- **Service Bus:** configure a dedicated queue and `SERVICEBUS_CONNECTION_STRING` / `SERVICEBUS_QUEUE`. Topic, session and request/reply variants require separate fixture rows.
- **Event Hubs:** configure a dedicated hub and `EVENTHUBS_CONNECTION_STRING` / `EVENTHUBS_EVENT_HUB`. The existing live smoke observes an event; it does not prove that the observed body is the just-published unique marker. Strengthen that assertion before using it as fidelity evidence.
- **IBM MQ:** provide a supported real client/sidecar, queue and expected selector/acknowledgment/transaction behavior. Mock MQSC replay is not IBM MQ runtime parity.
- **Enterprise:** provide dependency-complete customer exports, original-tool expected results, and controlled application/runner targets. CloudTest fixture gates and SAP/Java runtime limits must remain in force until the corresponding evidence exists.
