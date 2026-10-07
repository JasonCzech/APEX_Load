# Elastic provider qualification evidence

This report records local vendor interoperability checks performed on 2026-10-06 UTC. It qualifies the scenarios below against the exact images listed; it is not a blanket claim for every release, deployment flavor, permission model, or production capacity.

## Environment and images

The host ran Go 1.26.5 on macOS with Docker Engine 29.8.0 on Linux/arm64, eight Docker CPUs and 8,319,770,624 bytes of Docker memory. Each disposable vendor container had a 2 GiB memory ceiling, a two-CPU limit, a 512 MiB JVM heap, single-node discovery and two configured node processors. TLS endpoints were bound to loopback solely for the isolated qualification fixture.

| Product | Reported version | Image | Image digest |
|---|---|---|---|
| Elasticsearch | 8.14.3 | `docker.elastic.co/elasticsearch/elasticsearch:8.14.3` | `sha256:96ae58e574cb40b2caf8bdebc6dda7b789e1fae7d56346cc83c288374e18b1c2` |
| Elasticsearch | 9.1.0 | `docker.elastic.co/elasticsearch/elasticsearch:9.1.0` | `sha256:47f91984aa1065d745f0a7c827bc912ea1ac439c635854902b65b6ed77f62055` |
| OpenSearch | 2.19.0 | `opensearchproject/opensearch:2.19.0` | `sha256:1f8b88245a6af61e7aa500afe0e87d43401e4b33140bb47230a919428ce3f7cb` |
| OpenSearch | 3.3.0 | `opensearchproject/opensearch:3.3.0` | `sha256:d96afaf6cbd2a6a3695aeb2f1d48c9a16ad5c8918eb849e5cbf43475f0f8e146` |

Elasticsearch used a fixture-generated CA and server certificate with verified loopback SANs. OpenSearch used the image's public demo CA/server certificate, whose SAN includes loopback and whose validity was checked. TLS verification stayed enabled. Basic setup credentials existed only inside disposable fixtures. Collection used a separately created reader, restricted to the fixture namespace; it had read/discovery/PIT permissions and narrow `cluster:monitor/main` product-discovery permission, without cluster-health permission.

The test uses `NewClientWithHTTPClient` with a trusted, verified-TLS transport so that loopback is reachable inside the test. Public connection configuration cannot select this seam. Production DNS/private-network restrictions remain independently tested; these localhost trials do not qualify production private networking.

## Scenarios

The env-gated [vendor test](../pkg/elastic/vendor_integration_test.go) creates two disposable mapped indices and a filtered alias. It seeds 10,005 matching records with equal event timestamps and document IDs repeated across the concrete indices, plus excluded records with a different event window, run ID or service. Fixture setup writes are separate from the read-only provider; the provider never refreshes an index, changes index settings or provisions pipelines.

| Scenario | Elasticsearch 8.14.3 / 9.1.0 | OpenSearch 2.19.0 / 3.3.0 |
|---|---|---|
| Verified TLS and scoped Basic read principal | Pass | Pass |
| Product/version, selected-index and field discovery | Pass | Pass |
| Read collection while cluster health is forbidden | Pass | Pass |
| Mandatory half-open event, run-ID and service filters | Pass | Pass |
| Filtered alias and frozen concrete-index provenance | Pass | Pass |
| Exact document count: 10,005 | Pass | Pass |
| Weighted source average: 5 ms from microseconds | Pass | Pass |
| Native composite bucket with service dimension | Pass | Pass |
| Exact-window p95 and cardinality with approximation metadata | Pass | Pass |
| PIT/search-after beyond 10,000, equal timestamps and repeated IDs across indices | Pass | Pass |
| Scrubbed projected prose and ingestion timestamp | Pass | Pass |
| Synchronous ES\|QL exact-window count | Pass | Explicitly unsupported |
| Owned asynchronous ES\|QL, probed before use | Pass | Explicitly unsupported |
| Stable native tiebreaker | Timestamp + PIT-wide `_shard_doc` | 3.3: timestamp + `_index` + `_shard_doc` |
| Older sortable-ID fallback | Not needed in tested bands | 2.19: timestamp + `_index` + configured `sort_key` |

Each provider capture returned all 10,005 approved records across two bounded collection continuations without merging equal IDs from different indices. This is a provider pagination test. It does not exercise canonical storage retention, receipt durability, collector spool replay or default source-retention quotas. The default canonical source quota is 10,000 records and would intentionally truncate a 10,005-record retained capture. Provider qualification raised collection ceilings to 100 requests, 60 seconds of work and 10,000 evidence records per collection; document pages and response-body limits remained 100 records and 4 MiB.

## Commands and observed evidence

After starting an isolated fixture and providing its CA and synthetic setup credentials, the actual test command was:

```sh
APEXLOAD_ELASTIC_INTEGRATION=1 \
APEXLOAD_ELASTIC_INTEGRATION_URL=https://127.0.0.1:19420 \
APEXLOAD_ELASTIC_INTEGRATION_PRODUCT=elasticsearch \
APEXLOAD_ELASTIC_INTEGRATION_VERSION=8.14.3 \
APEXLOAD_ELASTIC_INTEGRATION_USERNAME="$APEX_FIXTURE_ADMIN_USER" \
APEXLOAD_ELASTIC_INTEGRATION_PASSWORD="$APEX_FIXTURE_ADMIN_PASSWORD" \
APEXLOAD_ELASTIC_INTEGRATION_CA="$APEX_FIXTURE_CA_FILE" \
GOCACHE="$PWD/.session-cache/go" GOMAXPROCS=4 \
go test -p 2 ./pkg/elastic -run '^TestVendorQualification$' -count=1 -v
```

The 9.1.0 trial substituted its expected version. OpenSearch trials used `product=opensearch`, versions 2.19.0/3.3.0 and their public fixture CA files. All four fixtures ran sequentially on port 19420. The test asserts the reported version, so accidentally testing another tag fails.

Successful output includes `document_count=10005 collections=2`, `equal_timestamp_and_cross_index_identity=true`, `weighted_average_ms=5`, and `health=false` for the restricted reader. Elasticsearch reported `esql=true async_esql=true`; OpenSearch 2.19 reported `native_tiebreaker=false`, while 3.3 reported `native_tiebreaker=true`. The final provider race suite ran with `GOMAXPROCS=2 GOCACHE="$PWD/.session-cache/go" go test -race -p 1 -parallel 1 ./pkg/elastic -count=1` and passed in 10.074 seconds.

The [PIT ownership regressions](../pkg/elastic/pit_renewal_test.go) separately exercise rejected renewed pages, malformed/truncated response prefixes, failed body reads, failed cleanup and replacement, native capability probes, and pre-dispatch backpressure. They preserve private ownership without accepting rejected pages or advancing coverage. The [source-limit regressions](../pkg/elastic/validation_limits_test.go) check the twenty-metric total across native and advanced metric definitions; record queries remain separately bounded. These synthetic failures do not qualify a vendor outage or production network.

All four trials completed successfully on the final reviewed source after cleanup ownership, renewal/restart, byte reservation, preview and source-limit fixes. The completed vendor-test durations and UTC trial windows were:

| Product/version | Vendor test duration | Fixture trial start / finish (UTC) |
|---|---|---|
| Elasticsearch 8.14.3 | 19.14 seconds | 20:21:43 / 20:22:24 |
| Elasticsearch 9.1.0 | 18.37 seconds | 20:22:43 / 20:23:42 |
| OpenSearch 2.19.0 | 20.19 seconds | 20:23:51 / 20:24:40 |
| OpenSearch 3.3.0 | 24.16 seconds | 20:24:47 / 20:26:03 |

These durations identify observed tests and are not throughput benchmarks. Fixture startup and teardown are included in the trial windows, but excluded from the vendor-test duration. Interrupted earlier review runs are not used as evidence for this snapshot.

The tested `vendor_integration_test.go` SHA-256 was `1536e71be54d26ec03314c44079cc40ba1495101350ff90b8f8c51ec55989340`. The SHA-256 of the sorted provider source manifest was `7ec441dcf28839ad56dfd765b9d431895b5015c489380e86368a8ae735b6b5ba`; the manifest consists of sorted `SHA256  relative-path` lines for every `pkg/elastic/*.go` file, including the qualification test. All four trials used this identical snapshot, with a hash check before and after each test.

## Compatibility corrections found by real trials

Elasticsearch 8.14 rejects `allow_partial_search_results` on PIT creation. The adapter omits this later-version creation parameter for that band while retaining strict per-page search/shard validation. Field discovery sends `include_unmapped` as a query parameter; putting it in a field-caps JSON body is rejected. These differences were confirmed against the [8.14 PIT API](https://www.elastic.co/guide/en/elasticsearch/reference/8.14/point-in-time-api.html) and [field capabilities API](https://www.elastic.co/guide/en/elasticsearch/reference/8.14/search-field-caps.html).

OpenSearch 3.3 accepts `_shard_doc`, but equal shard/document values can occur in different concrete indices. Its adapter includes `_index` in the sort vector; Elasticsearch retains its separate PIT-wide ordering. The exact 10,005-record cross-index trial exposed this difference, which is consistent with the vendor's [shard/document tiebreaker implementation](https://github.com/opensearch-project/OpenSearch/pull/18924). The [OpenSearch PIT security model](https://docs.opensearch.org/2.19/search-plugins/searching-data/point-in-time/#security-model) also requires explicit create/delete permissions where the deployed read role does not already include them.

The fresh fixture setup initially encountered a strict Python readiness certificate-profile error and an OpenSearch certificate extraction attempted before its demo installer ran. Both setup issues were corrected before the reported tests. TLS trust, validity and loopback host checks stayed enabled, including in the Go integration client. Failed or interrupted fixture setup is not passing interoperability evidence.

## Limits and remaining qualification

Hosted Elastic, Serverless, Azure Native Elastic, managed OpenSearch, AKS/on-premises networking, AWS SigV4, vendor API-key/Bearer authentication and vendor mTLS were not exercised in these local trials. AWS SigV4 is outside the release scope. Serverless capabilities must still be probed rather than inferred from a version string.

Actual collector-to-vendor deployment, HA failover, data-stream rollover, vendor outages, delayed upstream indexing, PIT expiration under a real multi-node outage, upstream trace sampling and Kibana/Logstash deployed health APIs require additional environment qualification. Unit/integration fixtures separately exercise normalization, missing values, cursor cleanup, expiration/restart, alias/data-stream scope, quota accounting and collector contracts. Fixture evidence does not replace real deployment qualification.

These runs demonstrate interoperability and bounded pagination behavior for the stated inputs. They do not establish APEX's unbenchmarked throughput, memory, storage, shutdown or platform performance targets. The owned disposable containers and their anonymous data volumes are removed after testing; unrelated containers, images, dependencies and application data are preserved.
