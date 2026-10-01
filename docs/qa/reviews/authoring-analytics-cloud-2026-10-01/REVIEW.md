# Authoring, native panels, cloud collectors and messaging — 2026-10-01

Status: implemented slices reviewed and validated; broader roadmap scope remains open. Branch: `codex/authoring-analytics-cloud`, based on `7bf711ea`. The original shared checkout is preserved.

## Delivered scope

- Script save handoffs without a returned body fetch the canonical saved script instead of reusing an unrelated draft. Scenario group reseeding retains transaction response-time SLAs.
- Project editors can create saved native run metric line, latest-value and table panels. Viewers can select saved project dashboards. Missing observations remain missing; source interval percentiles are never averaged. Unsupported dimension/filter/overlay contracts render an explicit unavailable state.
- CloudWatch and Azure Monitor collectors use signed run assignments, local operator-owned target/query approvals, bounded queries/responses and scoped cloud identities. AWS account identity is checked before metrics access. Cloud summaries retain source timestamps, units, statistic identity and missing values. Accepted points reuse durable batch acknowledgement and deduplication. These adapters remain preview pending live cloud acceptance.
- Explicit finite response-property categories propagate through action metrics, generator RPC, live tables, JSONL/Parquet artifacts, archive filtering and reports. Capability negotiation prevents old peers silently dropping requested property metrics. Promotion follows redaction and replaces adapter-provided labels.
- Kafka and Event Hubs distinguish observed zero lag from unavailable lag. Exact transaction rules can evaluate maximum consumer age and offset backlog from complete archived evidence; incomplete observations remain unscored. Event Hubs deployment defaults preserve explicit script settings.

## Review fixes

- Fixed browser test types and separated native projection helpers from React components for lint compliance.
- Updated authoring regression fixtures to verify the canonical stored script, not stale draft reuse.
- Preserved prior saved metric aliases and made unsupported native queries explicit. Visual review also added metric/unit labels to saved panels and exports.
- Added stable accessible names to native dropdowns after browser verification exposed option text in their labels.
- Connected property promotion to the canonical script runtime view, in addition to scenario overrides, after browser verification found the missing entry point.
- Kept cloud deduplication retention separate from the existing OTLP window.
- Preserved observed-zero lag availability through generator protobuf transport.
- Added CloudWatch signed-request/account-mismatch fixtures; aligned transport headers with the documented JSON API and rejected provider-incompatible unit names.
- Prevented empty or failed cloud queries from bypassing the configured collection interval.
- Preserved the first complete cloud minute when execution begins exactly at a minute boundary.

## Follow-up review cycle

- Prevented provider-null cloud buckets from entering the acknowledgement identity set, so delayed numeric values (including zero) remain collectable during the lookback window. Added collection and acknowledgement regression coverage for both cloud packs.
- Normalized Azure timestamps to UTC before duplicate detection; equivalent instants with different offsets are rejected.
- Restored WebSocket diagnostics in the shared generator telemetry clone, with measured-zero and deep-copy regression coverage.
- Re-reviewed the fixes and surrounding collection, serialization, property aggregation and messaging-rule paths; no further actionable findings remained in the reviewed scope.
- Post-fix validation passed: collector and generator RPC race tests, the full Go suite (`APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -ldflags=-w -timeout 20m -p 1 ./...`), documentation checks and `git diff --check`. Dashboard code was unchanged in this follow-up cycle; its earlier validation remains recorded below.

## Verification

- Full repository Go suite passed: `APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -ldflags=-w -timeout 20m -p 1 ./...`, including API/generated-contract, engine, generator RPC, SQLite storage and packaging tests.
- Collector, Kafka and Event Hubs race tests passed; collector tests were rerun after the final polling/window fixes. Focused property, archive, distributed and messaging regressions passed.
- Dashboard typecheck, lint and all 4,672 unit tests passed. The final build and focused lint passed after the metric-label change.
- `npm run test:cycle` exercised the full browser selection: dev-admin had 175 passes, 4 existing skips and the two newly added workflow failures; local-auth had 95 passes, 1 existing skip and the same two failures. Those findings were fixed. The complete affected analysis/preparation and script-settings files then passed all 8 tests in each profile. After the final metric-label change, all 4 analysis/preparation tests passed again in each profile. No test retries were used.
- Documentation checks and `git diff --check` passed. Native-panel screenshots were inspected and retained below.
- Final review found no additional actionable issues in this implemented scope. Live-cloud acceptance and the broader items below are not covered by these local results.

Screenshots: [dev-admin native panel](native-panel-dev-admin.png), [local-auth native panel](native-panel-local-auth.png).

## Remaining roadmap scope and acceptance limits

This is a set of bounded implementation slices, not closure of every broad roadmap item. Remaining work includes the broader authoring/scenario orchestration consolidation; native dimension/filter/monitor/imported overlay authoring; property timelines and saved comparisons; partition/subscription messaging dashboards, full processor/checkpoint ownership and provider-wide acknowledgement telemetry. Live CloudWatch/Azure Monitor acceptance requires approved cloud environments. The previously waived 10 GB import benchmark was not run.

Cloud API references: [CloudWatch GetMetricData](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricData.html), [CloudWatch API reference](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/acw-api.pdf), [Azure Monitor metrics](https://learn.microsoft.com/en-us/rest/api/monitor/metrics/list?view=rest-monitor-2023-10-01).
