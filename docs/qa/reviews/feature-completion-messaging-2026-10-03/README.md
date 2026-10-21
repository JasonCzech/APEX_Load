# Shared messaging evidence review — 2026-10-03

Scope: optional messaging diagnostics v2, fixed bounded duration distributions, explicit operation/counter populations, Kafka/Event Hubs/JMS adapter observations and engine/RPC/archive propagation. This is the R1/MSG-1 evidence foundation, not complete broker qualification or the full messaging program.

Independent review/fix iterations resolved:

- Engine retention estimates omitted new histogram arrays, slice capacities and availability maps. Revised conservative accounting and queue-clone tests cover retained allocations.
- A claimed-v2 empty aggregate payload disappeared instead of marking missing coverage. Decoder/cohort regressions now preserve incompleteness for missing/unknown versions.
- Successful sends followed by assertion/extraction/reply failure lost accepted message/acknowledgement evidence. Internal adapter outcome markers retain genuine transport observations separately from action success.
- Actual failed sends omitted eligible v2 producer operations. Failed acknowledgements now retain unavailable reasons and scoped observed-zero/one failure counters; preflight/buffer admission and transaction flush populations remain separate.
- Observed JMS replies/deliveries followed by settlement errors lost received evidence. Explicit delivery markers preserve observations even with empty payload/metadata; they do not manufacture successful settlement.
- Go JSON decoding silently truncated oversized fixed histogram arrays and coerced null/omitted required numbers into zero. V2-specific bounded decoders reject malformed arrays, explicit null counts/statistics, absent counter counts and missing positive-observation statistics. Legacy distribution decoding is unchanged; wholly unavailable zero distributions retain compatibility.

The adapter/retention repeat review reported no additional actionable findings, including strengthened empty-body tests. The final strict-decoder repeat review also reported no additional actionable findings. Histograms merge bins, counts and availability rather than averaging percentiles. Legacy evidence stays readable and cannot manufacture v2 coverage; unsupported/malformed/oversized publication remains explicitly incomplete. Uncalibrated broker enqueue clocks remain unavailable in v2.

Validation:

- Full default protocol/Event Hubs/Kafka/JMS suites passed.
- Full real-adapter build profiles passed together: `eventhubs_real`, `kafka_real`, `jms_servicebus_real`, `jms_solace_real`. These runs compile the edited adapters and exercise deterministic/local fixtures. Live service acceptance was not run.
- Partial-delivery/empty-body regressions passed in default and combined tagged profiles.
- Focused engine/RPC bounded-retention, wire-version and mixed-peer regressions passed. Focused API messaging/native regressions passed.
- Final focused protocol/engine/RPC race suites passed after all decoder fixes (race log (`race-tests.txt`)); default repository-wide tests passed on frozen source with a dedicated temporary Go cache (repository log (`../feature-completion-catalog-2026-10-03/go-tests.txt`)).
- The final combined real-adapter build-profile repeat passed (adapter log (`adapter-tests.txt`)); no live provider qualification is inferred. Documentation and generated contract checks passed.
- The initial repository run encountered stale generated QA inventory and source changed during compilation. Inventory was regenerated and a frozen complete run passed; the interrupted decoder-follow-up run is not counted as a pass. A subsequent shared-cache run passed API tests but lost dependency cache files during remaining builds; the dedicated-cache full repeat passed and supersedes that environmental failure.

Remaining program gates include provider capability/admission matrices, management packs, user/report percentile workflows, qualified source clocks, helper semantics, restart/rebalance/provider certification and integrated acceptance. No provider support level is promoted by these component results.
