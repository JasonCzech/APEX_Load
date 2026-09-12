# Opt-in Test Suites

`go test ./...` (and `make test`) runs the default suite. A number of suites are
**excluded from that default** — either by a Go build tag or by an environment
variable — and must be invoked deliberately. Selected suites also have dedicated
automation: the deployment-artifact CI job provisions PostgreSQL 16 for the
scheduler-migration test, and the protected protocol-integration workflow runs
the live broker/cloud suites weekly or by trusted repository dispatch.

This page is the inventory of every such gate, what it covers, and how to run it.
It is machine-enforced: `TestOptInSuiteInventoryIsDocumented`
(`pkg/convert/enterprisearchive/optin_suite_inventory_test.go`) scans every
`*_test.go` in the repository for build-tag and environment-variable gates and
fails if one is missing from the tables below. Adding a new opt-in gate means
adding a row here.

What the scanner can see, precisely: `//go:build` constraints, and the name of an
environment variable inside a `Skip`/`Skipf`/`Skipln` call on any testing receiver
(`t`, `b`, …) — written literally in the message, or passed as a format argument
via a package-level string constant, across as many lines as the call spans. What
it cannot see is a skip whose message was assembled somewhere else and handed in as
a variable (`t.Skip(message)`); `APEXLOAD_BROWSER_TESTS` is that shape, so its row
below is maintained by hand. Keep the gate's name inside the `Skip` call and the
guard stays honest.

Everything can be run at once with:

```bash
./scripts/run-optin-suites.sh          # everything that needs no external infrastructure
./scripts/run-optin-suites.sh --all    # also suites needing live infrastructure (needs the env vars below)
./scripts/run-optin-suites.sh --list   # show the inventory and what would run
```

## Why these are opt-in

Three different reasons, and they are worth keeping straight:

1. **Needs a large external corpus** — the LR_Scripts smoke test needs a public
   LoadRunner script corpus that is not vendored into this repository.
2. **Needs real infrastructure** — the `*_integration` suites talk to a live
   PostgreSQL database, Kafka, Solace or Azure cloud endpoint.
3. **Needs a real client library compiled in** — the `*_real` build tags swap the
   in-memory mock adapters for the vendored network clients. These compile and run
   locally; they do not need a broker.

Only category 3 is cheap enough to be a candidate for the default `go test ./...`
CI step. Categories 1 and 2 need provisioning, which is why they are gated. CI
separately provisions PostgreSQL 16 for the scheduler-migration test. The
protected protocol-integration workflow runs the Kafka, Solace, Service Bus, and
Event Hubs suites against configured infrastructure; other infrastructure- or
corpus-backed suites remain manual.

## Environment-variable gates

| Environment variable | Suite | Package | How to run |
|---|---|---|---|
| `APEXLOAD_LR_SCRIPTS_CORPUS` | `TestPinnedLRScriptsCorpusSmoke` — runs the LoadRunner / DevWeb / JMeter importers over the pinned public LR_Scripts corpus | `pkg/convert/enterprisearchive` | **Auto-runs when the corpus is present.** `./scripts/fetch-lr-scripts-corpus.sh` then `go test ./pkg/convert/enterprisearchive`. Set the variable only to point at a checkout somewhere else — a path that does not resolve now **fails** rather than skipping. Auto-discovery admits a root only when its `Scripts` directory holds at least **20** script directories (the same bar the suite asserts), so a half-finished fetch, or an unrelated directory left at the `/tmp` default, skips with instructions instead of conscripting the package into failing. An explicitly named checkout is held to the weaker "holds any `.usr` / `.jmx` / `main.js`" test, so an operator who pointed at an incomplete corpus gets the suite's own loud assertion rather than a silent skip. |
| `LR_SCRIPTS_DEST` | Relocates the corpus for both `./scripts/fetch-lr-scripts-corpus.sh` and the auto-discovery in `TestPinnedLRScriptsCorpusSmoke` (probed first when set) | `pkg/convert/enterprisearchive` | `LR_SCRIPTS_DEST=$HOME/corpora/lr ./scripts/fetch-lr-scripts-corpus.sh`, then run the package with the same variable exported. Not a switch — it only moves the location. |
| `APEXLOAD_BROWSER_TESTS` | Decides how the Playwright browser suites treat a launch failure: `require` = always a FAILURE, `skip` = always a skip, `auto` (default) = skip only when the probe cannot find the engine's executable | `pkg/protocol/browser` | Runs by default; set `APEXLOAD_BROWSER_TESTS=require go test ./pkg/protocol/browser` anywhere the runtime is installed on purpose so a broken runtime cannot hide as a skip. Any other value is a hard failure. **`auto` is not a complete guarantee:** the probe itself boots the Playwright driver and stats the driver-resolved executable path, so it also reports "absent" — and therefore skips — when the driver fails to run or when the installed browser build drifts from the pinned driver version. CI installs chromium and webkit (`.github/workflows/ci.yml`) and pins playwright-go in `go.mod`, so an installed-but-broken runtime already fails there; `require` is the belt-and-braces against driver/browser version drift. |
| `APEXLOAD_BROWSER_BENCHMARKS` | Browser launch / action-latency / Chromium RSS benchmarks (`BenchmarkBrowser*`) — they start real browser processes | `pkg/protocol/browser` | `APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser -run '^$' -bench 'BenchmarkBrowser' -benchmem -count 5`; see `docs/MOBILE_BROWSER_BENCHMARK_RUNBOOK.md` |
| `APEXLOAD_RUN_HA_FAILOVER_SOAK` | HA execution-leader failover soak | `pkg/api` | `APEXLOAD_RUN_HA_FAILOVER_SOAK=1 go test ./pkg/api -run HAFailoverSoak -count=1` |
| `APEXLOAD_RUN_PERFORMANCE_SMOKE` | Controlled performance probes that produce the perf-evidence artifacts | `pkg/perfevidence` | `make performance-smoke`, or `APEXLOAD_RUN_PERFORMANCE_SMOKE=1 go test ./pkg/perfevidence -count=1` |
| `APEXLOAD_TEST_POSTGRES_URL` | Real PostgreSQL scheduler-lease schema migration: creates and drops a uniquely named schema, upgrades a legacy `INTEGER` generation to `BIGINT`, verifies the existing value and `int64` range survive, then reopens the store to prove restart idempotence | `pkg/storage` | Against a dedicated test database whose user may create/drop schemas: `APEXLOAD_TEST_POSTGRES_URL='postgres://...' go test -tags postgres_integration ./pkg/storage -run '^TestPostgresSchedulerLeaseGenerationMigration$' -count=1` |
| `KAFKA_BROKERS`, `KAFKA_TOPIC` | Live Kafka produce/consume integration and imported AsyncAPI payload fidelity | `pkg/protocol/kafka`, `pkg/convert/asyncapi` | `KAFKA_BROKERS=localhost:9092 go test -tags "kafka_real kafka_integration" ./pkg/protocol/kafka ./pkg/convert/asyncapi -count=1` (`KAFKA_TOPIC` optional for protocol smoke; AsyncAPI fixture creates/deletes its own topic) |
| `SOLACE_AMQP_URL`, `SOLACE_QUEUE` | Live Solace AMQP integration and imported topology payload fidelity | `pkg/protocol/jms`, `pkg/convert/brokerconfig` | `SOLACE_AMQP_URL=amqp://... SOLACE_QUEUE=q go test -tags 'jms_solace_real jms_solace_integration' ./pkg/protocol/jms -count=1` |
| `SERVICEBUS_CONNECTION_STRING` *or* `SERVICEBUS_NAMESPACE`; plus `SERVICEBUS_TOPIC`, `SERVICEBUS_SUBSCRIPTION`, `SERVICEBUS_REPLY_QUEUE` for the topic/reply cases | Live Azure Service Bus integration | `pkg/protocol/jms` | `SERVICEBUS_CONNECTION_STRING=... go test -tags 'jms_servicebus_real jms_servicebus_integration' ./pkg/protocol/jms -count=1` |
| `EVENTHUBS_CONNECTION_STRING` (plus `EVENTHUBS_EVENT_HUB`, optional `EVENTHUBS_PARTITION_ID`) | Live Azure Event Hubs round trip — Event Hubs has no GA local emulator | `pkg/protocol/eventhubs` | `EVENTHUBS_CONNECTION_STRING=... EVENTHUBS_EVENT_HUB=hub go test -tags eventhubs_real ./pkg/protocol/eventhubs -count=1` |

## Build-tag gates

| Build tag | What it swaps in | Package(s) | How to run |
|---|---|---|---|
| `kafka_real` | franz-go network client instead of the in-memory mock broker | `pkg/protocol/kafka`, `pkg/dsl` | `go test -tags kafka_real ./pkg/protocol/kafka ./pkg/dsl -count=1` |
| `kafka_integration` | Live-broker Kafka tests (also needs `KAFKA_BROKERS`) | `pkg/protocol/kafka` | see the env table above |
| `jms_solace_real` | Real AMQP adapter instead of the mock | `pkg/protocol/jms` | `make test-jms-solace-real` |
| `jms_solace_integration` | Live Solace tests (with `jms_solace_real`, also needs `SOLACE_AMQP_URL`) | `pkg/protocol/jms` | see the env table above |
| `jms_servicebus_real` | Real Azure Service Bus adapter instead of the mock | `pkg/protocol/jms`, `pkg/dsl` | `go test -tags jms_servicebus_real ./pkg/protocol/jms ./pkg/dsl -count=1` |
| `jms_servicebus_integration` | Live Service Bus tests (with `jms_servicebus_real`) | `pkg/protocol/jms` | `go test -tags 'jms_servicebus_real jms_servicebus_integration' ./pkg/protocol/jms -count=1` |
| `eventhubs_real` | Real azeventhubs/AMQP adapter instead of the mock | `pkg/protocol/eventhubs`, `pkg/dsl` | `go test -tags eventhubs_real ./pkg/protocol/eventhubs ./pkg/dsl -count=1` |
| `postgres_integration` | Real PostgreSQL catalog/DDL migration test (also needs `APEXLOAD_TEST_POSTGRES_URL`) | `pkg/storage` | see the env table above |

Note the paired `!tag` files (`kafka_unavailable_default_test.go`,
`servicebus_unavailable_test.go`, `eventhubs_unavailable_default_test.go`,
`validate_*_default_test.go`). Those run in the DEFAULT build and pin the
mock/unavailable behaviour, so the default suite is not blind to these protocols —
it just never exercises the real client.

## Not opt-in

Platform constraints (`//go:build windows`, `darwin`, `linux`, the BSD list) and
`//go:build race` / `!race` are not opt-in gates: they select per-platform or
per-`-race` implementations and one branch always runs. The inventory guard ignores
them.

## Adding a new opt-in suite

1. Add the row to the matching table above.
2. Add it to `scripts/run-optin-suites.sh` so it is actually runnable in one command.
3. Make the skip message name the gate and the command that satisfies it — a bare
   `t.Skip("skipping")` is how a suite becomes invisible.
