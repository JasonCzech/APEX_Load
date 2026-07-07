# APEX Load Performance Review - 2026-07-06

Generated from the verified multi-agent performance review output so the findings survive cleanup of the original temporary task artifacts.

## Source

- Review summary: Multi-agent performance review of APEX Load: find, verify, and gap-check optimization opportunities
- Agent count: 134
- Original output path at generation time: `/private/tmp/claude-501/-Users-jasonczech-Documents-GitHub-Project-Stormrunner/6c766b1c-534a-41e5-bcbb-cd1924a128e6/tasks/wquvoe5uv.output`

## Review Log

- Launching 12 subsystem performance finders
- 104 raw findings → 91 after dedup; verifying each adversarially
- Verification: 86 confirmed, 5 refuted/negligible
- Critic identified 4 gap area(s): java-vuser-process-per-action, recorder-session-payload-and-poll, go-runtime-k8s-sizing, browser-protocol-per-step
- Supplemental round: 26 new findings; verifying
- DONE: 108 confirmed findings total

## Counts

- Confirmed findings: 108
- Rejected/refuted findings: 5
- Critical: 2
- High: 31
- Medium: 59
- Low: 16

## Category Breakdown

- cpu: 20
- network: 14
- allocations: 13
- algorithmic: 11
- memory: 11
- polling: 7
- database: 7
- caching: 7
- concurrency: 7
- io: 5
- rendering: 4
- bundle: 2

## Finder Breakdown

- archive-report: 9
- background-loops: 9
- metrics-pipeline: 8
- engine-vuser: 8
- importers: 8
- dashboard-render: 8
- sup:recorder-session-payload-and-poll: 8
- protocol: 8
- sup:browser-protocol-per-step: 8
- dashboard-data: 7
- distributed: 6
- api-handlers: 6
- storage-sqlite: 5
- go-crosscutting: 4
- sup:java-vuser-process-per-action: 4
- sup:go-runtime-k8s-sizing: 2

## Coverage Notes

The sweep's 12 scopes covered the dominant hot paths well: engine resolve/redaction regexes, HTTP protocol body copies, metrics fan-in O(N^2), SQLite pragmas/missing indexes/retention, archive O(n^2) re-reads and report caching, importer quadratics, distributed serial dials, and dashboard poll/render storms. Verified gaps: (1) pkg/protocol/java_vuser (596L) spawns a fresh `java` process per java_vuser_run action — default RunnerCommand is literally "java" (pkg/config/runner_env.go:39), with exec.CommandContext + temp event file + os.Environ copy per action at pkg/protocol/java_vuser/java_vuser.go:126-212; no finding touched this despite Java Vuser being a first-class import lane. (2) The recording subsystem: pkg/recorder Sessions retain up to 512KiB request + 512KiB response body per captured event in RAM (pkg/recorder/recorder.go:28,51-61), handleGetRecording re-marshals the FULL session per call (pkg/api/handlers_recorder.go:101-109), and RecordingStudio.tsx polls the full active session every 2s with fresh identity (dashboard/src/components/RecordingStudio.tsx:650-663) — same pattern class as the confirmed LiveMetrics critical, uncovered. (3) No GOMAXPROCS/GOMEMLIMIT handling anywhere (grep across pkg/cmd/controller and deployments/k8s manifests returns nothing) while generator pods carry cpu limit 4 / controller 2 (deployments/k8s/onprem/generator-statefulset.yaml:101) — classic CFS-throttling distortion for a latency-measuring generator. (4) pkg/protocol/browser (~11.4k lines) + pkg/selector produced zero findings; scrub.Value runs multi-regex + JSON-parse per observed WS frame (pkg/protocol/browser/ws_observe.go:59). Checked and adequate (no supplemental pass needed): pkg/scrub precompiles built-in patterns and documents per-call ExtraPatterns compile (pkg/scrub/scrub.go:106-125); pkg/dsl ValidateScriptIssues is linear with map dedup; http.Server timeouts/MaxHeaderBytes present (pkg/api/server.go:248-254); session auth is sha256 not bcrypt (pkg/api/auth_resolution.go:371); rewriteSQL is a dialect-gated no-op on SQLite (pkg/storage/sqlite.go:322-325; PG-only ~15 ReplaceAll passes per query judged below threshold); collectoragent/recordagent loops trivial; mcp/ (Python) and desktop/ (Wails) cold.

## Confirmed Findings

### 1. GET /tests/{id} serializes the entire 3600-point LiveMetrics history on every 2-second dashboard poll

- Location: `pkg/api/runner.go:230`
- Category: network
- Finder: metrics-pipeline
- Verified impact: critical
- Confidence: high
- Effort: M

**Description:** ManagedTest.History (`History []*LiveMetrics json:"history,omitempty"`, capped at maxManagedTestHistory=3600 at runner.go:797) is serialized in full by handleGetTest (pkg/api/handlers.go:130 writeJSON(maskManagedTest(test))) and again by handleGetMetrics (handlers.go:429). Each history point is a full LiveMetrics with Transactions/DependencyHealth/SOAPOperations/NetworkProfiles maps of TxMetrics (incl. ServerTelemetry summaries), DependencyMap, ErrorPatterns and Resources — easily 3–50KB of JSON per point. The dashboard polls api.getTest every 2s during execution (dashboard/src/components/useExecutionTestPolling.ts:8,45,57), so a mature run re-marshals and ships 10–100+ MB of JSON every 2 seconds per viewer, even though the poller only needs status/summary fields (the live chart is fed by WebSocket; history is only needed for reconnect backfill).

**Evidence:** History               []*LiveMetrics                           `json:"history,omitempty"`  // serialized by handleGetTest: writeJSON(w, http.StatusOK, maskManagedTest(test))

**Hot path:** Polled every 2s per open execution view for the entire duration of a run; history grows to 3600 points (1/sec single-node, N/sec distributed), so payload and marshal CPU grow linearly with run age and multiply by concurrent viewers.

**Verification notes:** Verified the full chain: History (runner.go:230, cap 3600 at :797) gets a full enriched LiveMetrics appended every tick (default 1s, settings.go:535, metrics.go:476); GetTest (tests.go:17) and maskManagedTest (runtime_mask.go:121) are shallow copies that keep History; handleGetTest (handlers.go:130) serializes it via writeJSON with no gzip anywhere; dashboard polls GET /tests/{id} every 2s during live runs (useExecutionTestPolling.ts:8,44 → useTestApi.ts:312, used by TestExecution.tsx:106), while the chart is WS-fed and history is only needed for seed/backfill (useExecutionChartModel.ts:31, useExecutionMetricsStream.ts:127). At realistic scale (10-20 transactions, distributed GeneratorResources per point) responses reach tens of MB uncompressed every 2s per viewer; two aggravators found: the poller has no in-flight guard so slow responses pile up overlapping marshals, and GET /tests (list) also ships full History for every test in the project via ListTestsFiltered + maskManagedTests.

**Refined fix:** Strip History in maskManagedTest by default (masked.History = nil) and gate it behind ?include=history with an optional since=<RFC3339> param so backfill fetches only the gap; Telemetry.HistoryPoints already provides the cheap count. Migrate the two consumers: useExecutionChartModel.ts:31 initial seed and useExecutionMetricsStream.ts:127 reconnect backfill do a one-shot GET /tests/{id}?include=history&since=<lastSeenTimestamp>; the 2s poller stays history-free. Also strip History from the list path (maskManagedTests / handleListTests) — no dashboard code reads history off list responses, and completed tests reloaded from persistence carry 3600 points each there. Leave handleGetMetrics's history field initially (CommandCenterPrototype.tsx:1261 consumes it) or add the same since/limit params. Optionally add an in-flight guard to useExecutionTestPolling so a slow poll can't stack requests.

**Risk:** Dashboard reconnect backfill (useExecutionMetricsStream.ts:127) and CommandCenter mapExecutionHistory read test.history — both must be migrated to the new endpoint or the opt-in param, or charts lose backfill after socket drops.

### 2. Test list/detail endpoints serialize full 3600-point metrics History on every dashboard poll

- Location: `pkg/api/handlers.go:96`
- Category: polling
- Finder: go-crosscutting
- Verified impact: critical
- Confidence: high
- Effort: M

**Description:** ManagedTest.History (pkg/api/runner.go:230, `History []*LiveMetrics json:"history,omitempty"`, capped at maxManagedTestHistory=3600) is serialized whole by GET /api/v1/tests (maskManagedTests keeps the History pointer) and GET /api/v1/tests/{id}. Each LiveMetrics point carries group maps, error patterns, telemetry and resource structs (~1-4KB serialized). loadStoredTests hydrates the full history for EVERY stored test at boot (persistence.go:171), so completed runs also carry it. The list endpoint therefore re-marshals (tests × up-to-3600 × KBs) of JSON per request, and clients re-parse it.

**Evidence:** writeJSON(w, http.StatusOK, map[string]any{
    "tests": maskManagedTests(tests),  // each ManagedTest includes History []*LiveMetrics (runner.go:230), up to 3600 points

**Hot path:** DashboardPage polls api.listTests every 3s (dashboard/src/components/DashboardPage.tsx:569), useExecutionTestPolling polls GET /tests/{id} every 2s (useExecutionTestPolling.ts:55), ResultsPage refreshes every 10s — per connected dashboard client, continuously, during and after runs.

**Verification notes:** Verified end-to-end: maskManagedTest (runtime_mask.go:121) is a shallow copy that keeps the History pointer, ListTests/GetTest (tests.go) never strip it, History grows 1 point/sec to 3600 (metrics.go:476, runner.go:797), each LiveMetrics point carries per-transaction/group/telemetry/resource maps (runner.go:326-364), and loadStoredTests hydrates full history for every stored test at boot (persistence.go:171) with no in-memory retention cap on test count. Hot path confirmed: DashboardPage polls listTests every 3s, useExecutionTestPolling polls getTest every 2s, ResultsPage every 10s — and writeJSON is uncompressed json.NewEncoder per request, so a controller with dozens of completed runs re-marshals tens of MB per poll per client indefinitely. The finding's risk note is also validated: TestHub.tsx:666-682 sparklines read run.history from LIST responses and useExecutionMetricsStream.ts:127 backfills from getTest().history on WS reconnect, so removal requires a coordinated dashboard change. Only nuance: the detail endpoint is bounded to one test and stops polling at terminal status; the list endpoints are the true critical multiplier.

**Refined fix:** Split the fix by endpoint. (1) List endpoints (the critical multiplier): zero out History in maskManagedTests so GET /api/v1/tests never carries it; keep LiveMetrics (latest point) so status cards still render. Migrate TestHub sparklines (dashboard/src/components/TestHub.tsx:666-682) — note lines 674/682 only use the LAST history point, replaceable by run.live_metrics; only the TPS series (line 666) needs real history, so either fetch it lazily from GET /tests/{id}/metrics for visible cards or attach a tiny server-built sparkline field (e.g. 60 downsampled {timestamp,tps} pairs) in maskManagedTest. (2) Detail endpoint GET /tests/{id}: gate history behind ?include=history rather than removing it, because useExecutionMetricsStream.ts:127 (WS-reconnect backfill) and the parent chart seed consume it; alternatively repoint both at GET /tests/{id}/metrics, which already returns identical filtered history. (3) Add ?since=<timestamp> incremental fetch to /tests/{id}/metrics. (4) Orthogonal cheap win: gzip middleware on /api/v1 — the highly repetitive JSON compresses ~10-20x — but do not treat it as a substitute for stripping the list payload, since marshal CPU remains.

**Risk:** Any dashboard or API client reading history from GET /tests responses breaks; needs a coordinated dashboard change or an explicit include flag for backward compatibility.

### 3. Every action result's response body is scrubbed with 6 regex passes plus per-token entropy analysis

- Location: `pkg/engine/vuser_execute.go:26`
- Category: cpu
- Finder: engine-vuser
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** recordResult unconditionally calls scrubSensitiveResult on every protocol result. scrubSensitiveTextWithConfig (redaction.go:229-250) runs 5 compiled payload regexes plus the highEntropyTokenPattern ReplaceAllStringFunc over ErrorMessage, ResponseBody, PageURL, PageTitle, HTML, console/network entries and any snapshot — even when RedactionConfig is nil and the result succeeded. Each entropy candidate (any 24+ char base64-ish token, ubiquitous in JSON APIs/JWTs) additionally allocates a map[rune]float64 in shannonEntropy. The HTTP adapter attaches the full response body to every result (pkg/protocol/http/http.go:427), so this is O(body bytes x 6 regex scans) per action.

**Evidence:** vu.mergeExtractions(result)
	scrubSensitiveResult(result, vu.RedactionConfig)
	vu.Metrics.Record(result)
... redaction.go: for _, pattern := range payloadRedactionPatterns { redacted = pattern.re.ReplaceAllString(redacted, pattern.repl) } ... redacted = highEntropyTokenPattern.ReplaceAllStringFunc(...) { ... shannonEntropy(value) ... }

**Hot path:** recordResult runs once per action per iteration per VUser (and once per http_sequence child). At thousands of VUsers x several actions/sec with multi-KB response bodies, regex-scanning every payload 6x is likely the single largest CPU sink in the engine, stealing generator CPU and distorting the latency the test is measuring.

**Verification notes:** Confirmed at pkg/engine/vuser_execute.go:26: recordResult unconditionally scrubs every result (call sites vuser.go:459 main per-action loop, vuser.go:689, vuser_execute.go:297 per http_sequence child), and scrubSensitiveTextWithConfig (redaction.go:229-250) runs 5 payload regexes + the entropy pass with no size/success/keyword gating even when RedactionConfig is nil; the HTTP adapter attaches the full decoded body (64 MiB default cap, http.go:427/523). Worse than claimed on allocations: Go's ReplaceAllString copies the whole input even with zero matches, so a clean B-byte body generates ~12B of garbage per action, and shannonEntropy allocates a map[rune]float64 + []rune per 24+-char letter+digit token (ubiquitous JWTs/IDs). Crucially, the scrubbed ResponseBody is never emitted in the common case — archive TransactionEvent (archive_writer.go:107) has no ResponseBody field, the distributed transactionBuffer sends only ErrorMessage+error snapshot, MetricsCollector.Record only channels the pointer, and the only body egresses are opt-in extended logging "data_returned" (vuser_resolve.go:136) and error-only snapshots/live evidence — so the dominant cost protects data that is discarded. I rate impact high rather than the claimed critical because the cost scales with body size (tens of µs for small JSON bodies vs ms-scale for 100KB+ bodies where it plausibly is the top CPU sink); the repo's own <50µs/transaction harness budget (harness_overhead_benchmark_test.go) measures only Metrics.Record and excludes this call entirely.

**Refined fix:** Keep scrubSensitiveResult in recordResult but make it field/egress-aware rather than moving it wholesale (safer given the secrets-in-evidence P0): (1) skip ResponseBody/HTML scrubbing unless the VUser has extended-logging "data_returned" enabled or the result failed and SnapshotOnError/live-evidence will emit it — ErrorMessage/PageURL/PageTitle/console/network/evidence must stay scrubbed since archive events persist them, and Snapshot.* must always be scrubbed before SnapshotStore. (2) In scrubSensitiveTextWithConfig, guard each ReplaceAllString with pattern.re.MatchString (stops at first match, zero allocation) so clean text costs scans but no copies, and add a single cheap prefilter pass: case-insensitive keyword scan (token/secret/password/ssn/mrn/...) gating patterns 1-3, a digits-present check gating the SSN pattern, and a manual scan for a 24+ run of [A-Za-z0-9+/=_-] gating the entropy regex. (3) Replace shannonEntropy's map[rune]float64 with a [256]int byte-count array and drop the []rune conversion — the candidate charset is ASCII by regex definition. (4) Add a scrub benchmark next to harness_overhead_benchmark_test.go with representative 1KB/50KB bodies to bring this call under the <50µs/transaction budget and prevent regression. Before landing (1), audit all ResultSink implementations (archive_writer, combinedResultSink, liveErrorEvidenceSink, generatorrpc transactionBuffer/combinedSink) to confirm no unscrubbbed body field escapes.

**Risk:** Redaction is a security control (secrets-in-evidence was a P0 in the two-week review). Any deferral must guarantee every path that writes payloads out of process (snapshots, sinks, logs, replay events) still scrubs — audit ResultSink implementations before moving the call.

### 4. Template interpolation runs two full regex passes per string and re-resolves every token for the discarded redacted variant

- Location: `pkg/engine/vuser_resolve.go:296`
- Category: cpu
- Finder: engine-vuser
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** interpolateWithSecrets executes templatePattern.ReplaceAllStringFunc twice over every string — once for the resolved value (line 280) and a second complete scan for the redacted value (line 296) that calls resolveTemplateValue again for every non-secret token. resolveAction applies this to target, selector, value, every header, body, params (recursively) and metadata for every action; there is no fast path for strings containing no '{{' at all, so large literal bodies are regex-scanned twice per action. interpolate() (line 267) computes the redacted variant only to throw it away. resolveAction then materializes []byte(body) AND []byte(redactedBody) (lines 176-178) — two full copies of the body even when no secret exists and both strings are identical.

**Evidence:** resolved := templatePattern.ReplaceAllStringFunc(s, func(token string) string { ... })
	redacted := templatePattern.ReplaceAllStringFunc(s, func(token string) string { ... if value, ok := vu.resolveTemplateValue(expr); ok { return value } ... })

**Hot path:** resolveAction runs once per action per iteration per VUser (plus once per http_sequence child); with multi-KB imported bodies and dozens of headers/params, thousands of VUsers each pay 2x regex scans + 2x body copies per action. The double resolveTemplateValue also advances each_occurrence parameters and random builtins a second time, so the redacted log value diverges from what was sent.

**Verification notes:** Verified all claims in pkg/engine/vuser_resolve.go:275-321: two full ReplaceAllStringFunc passes per string, second pass re-calls resolveTemplateValue per non-secret token, no '{{' fast path, and resolveAction (lines 176/178) makes two []byte copies of identical strings when no secret exists. Confirmed the path is hot (vuser.go:434 per action per iteration in the main VUser loop; vuser_execute.go:286 per http_sequence child; vuser.go:421/428 even re-run resolveAction a second full time for extended logging). Also confirmed the correctness side effect is real and worse than claimed: ResolveNamed for each_occurrence params calls generate() unconditionally (parameters.go:128), which mutates nextIndex/used/counter state, so the discarded redacted pass consumes one extra value per occurrence (sequential/unique params skip every other value; counters advance 2x; fn./fake. builtins draw vu.rng twice, making redacted logs diverge from sent values). No caching or mitigation exists; only the regex is precompiled.

**Refined fix:** Original fix is sound; refine as follows. (1) Fast path: if !strings.Contains(s, "{{") return s, s — Go's ReplaceAllStringFunc allocates a full copy even on zero matches, so this alone removes 2 scans + 2 copies for literal bodies. (2) Single pass via templatePattern.FindAllStringSubmatchIndex with a manual strings.Builder for each variant (ReplaceAllStringFunc cannot emit two outputs); resolve each token exactly once, appending value to resolved and value-or-[REDACTED]/[FILE:x] to redacted; keep unresolvedSecretRefs/unresolvedFileRefs appends fail-closed. This also fixes the each_occurrence double-advance and rng divergence — a behavior change that is a bugfix but requires updating redaction/interpolation tests. (3) In resolveAction, when redacted == resolved, prefer setting RedactedBody=nil/RedactedTarget="" over aliasing the same []byte between Body and RedactedBody (aliasing risks silent corruption if an executor mutates Body); actionLogAttrs (vuser.go:123-128) already falls back to Body/Target when redacted is empty, but audit other RedactedBody consumers (replay/evidence) first. (4) Adjacent win: vuser.go:421/428 call resolveAction a second full time just to build log attrs — reuse the already-resolved action from line 434 / the sequence path instead.

**Risk:** Redacted output shape changes subtly (it would now reflect the values actually sent — arguably a fix); secret/file token handling must keep failing closed via unresolvedSecretRefs. Needs the existing redaction/interpolation test suites to pass.

### 5. {{file.*}} placeholders copy the entire file asset on every resolution, twice per interpolated string

- Location: `pkg/engine/data.go:421`
- Category: allocations
- Finder: engine-vuser
- Verified impact: high
- Confidence: high
- Effort: S

**Description:** GetFileContent returns string(fileSource.body), which copies the whole asset (imports can attach multi-MB upload bodies) on every call. In interpolateWithSecrets the resolved pass copies it once, and the redacted pass (vuser_resolve.go:307) calls resolveTemplateValue again — materializing a second full copy — only to discard it (`_ = value`) and emit a "[FILE:...]" marker.

**Evidence:** func (dp *DataProvider) GetFileContent(name string) (string, bool) {
	...
	return string(fileSource.body), true
}
... vuser_resolve.go:307: if value, ok := vu.resolveTemplateValue(expr); ok { _ = value; return "[FILE:" + ... }

**Hot path:** A script that uploads a file body per iteration ({{file.x}} in an action body) triggers two full-asset copies per action per iteration; at 1000 VUsers x 1 upload/sec with a 2 MB asset that is ~4 GB/s of allocation churn feeding the GC.

**Verification notes:** Verified exactly as claimed: pkg/engine/data.go:421 returns string(fileSource.body) — a full copy of a []byte asset bounded only by fileasset.MaxBytes = 50 MiB (fileasset.go:13) — with no caching anywhere between resolveTemplateValue (vuser_resolve.go:442-447) and the map lookup. The redacted pass at vuser_resolve.go:307-309 calls resolveTemplateValue again and discards the value (`_ = value`) purely as an existence check, so every interpolation of a {{file.*}} token makes two avoidable full-asset copies (plus one inherent copy in the ReplaceAllStringFunc output). The path is hot: interpolateWithSecrets runs on action.Body at vuser_resolve.go:160 per action per iteration per VUser, which is precisely the upload-body scenario in the finding; the 1000-VUser x 2 MB arithmetic holds. Fix safety confirmed: fileDataSource.body is written once in loadFileDataSource (data.go:88) and read only at data.go:421 — nothing mutates it.

**Refined fix:** Two independent changes, both safe: (1) Change fileDataSource.body from []byte to string, converting once at load in loadFileDataSource (data.go:88, `body: string(content)`); GetFileContent then returns it with zero copy. Grep confirms body has exactly one reader and one writer, so no behavior change. (2) In the redacted pass (vuser_resolve.go:305-312), replace resolve-and-discard with an existence check: add `func (dp *DataProvider) HasFile(name string) bool` mirroring GetFileContent's semantics exactly (nil-receiver guard, strings.TrimSpace(name), nil fileSource check), and guard vu.DataProvider == nil at the call site the way resolveTemplateValue's file. branch does — the missing-file fallthrough must still return the raw token to preserve current unresolved-ref behavior. Together these cut per-request allocation for {{file.*}} bodies from ~3x asset size to the single unavoidable ReplaceAllStringFunc splice.

**Risk:** Minimal — string vs []byte storage is behavior-identical for read-only assets; verify nothing mutates fileDataSource.body.

### 6. Each VUser lazily builds its own secret resolver, causing a per-VUser Key Vault fetch herd at ramp-up

- Location: `pkg/engine/vuser_resolve.go:457`
- Category: network
- Finder: engine-vuser
- Verified impact: high
- Confidence: high
- Effort: S

**Description:** Neither Engine.Run (engine.go:199-209) nor MultiGroupEngine.spawnVUser/spawnOpenWorker (group.go:469-485, 570-583) sets VUser.SecretResolver, so secretResolver() creates a fresh newDefaultSecretResolver per VUser. The caching layer is therefore per-VUser: every VUser independently constructs an azidentity.DefaultAzureCredential, acquires its own token, and issues its own HTTPS GET to Azure Key Vault for each distinct {{secret.*}} reference. Open-workload runs are worse: each arrival is a new VUser, so the cache never amortizes.

**Evidence:** func (vu *VUser) secretResolver() SecretResolver {
	if vu.SecretResolver == nil {
		vu.SecretResolver = newDefaultSecretResolver()
	}
	return vu.SecretResolver
}

**Hot path:** 5000 VUsers x 3 secrets = 15,000 Key Vault requests plus 5000 credential/token acquisitions concentrated in the ramp window; Key Vault throttles well below that, and the secrets gate then fails actions closed — turning a config-scale issue into spurious load-test failures. Open workloads at N TPS sustain N KV fetch-sets per second for the whole run.

**Verification notes:** Verified: the lazy per-VUser resolver exists exactly at vuser_resolve.go:455-460 and no spawn site (engine.go:199/317, group.go:374/469/570) sets VUser.SecretResolver, so each VUser builds its own DefaultAzureCredential + per-instance token cache (azure_token.go:30-56) and issues its own KV GET per secret; open-workload paths in BOTH engines create a fresh VUser per arrival, so the cache never amortizes. It is worse than claimed: cachingSecretResolver caches misses permanently (secrets.go:51-65), and secrets_gate.go fails actions closed, so a single throttled/timed-out fetch during the ramp herd permanently breaks that VUser's secret for the whole run. Only mitigations: env resolver is first in the chain and the whole path is inert unless APEXLOAD_AZURE_KEY_VAULT_URL is set — but KV-backed fail-closed secrets is the intended production posture.

**Refined fix:** Sharing one resolver is necessary but not sufficient: cachingSecretResolver.Resolve drops its mutex before delegating (secrets.go:55-57), so N concurrently-cold VUsers still stampede N parallel KV GETs for the same secret name. Fix in three parts: (1) create one newDefaultSecretResolver per engine run and assign it at all five VUser construction sites (engine.go:199 and 317; group.go:374, 469, 570), plumbed through GroupConfig like rateLimiter/rendezvous; (2) add per-key coalescing inside cachingSecretResolver (golang.org/x/sync/singleflight or a per-name sync.Once/lock map) so a cold secret costs exactly one KV GET regardless of concurrency; (3) stop caching misses permanently — distinguish authoritative 404 (cacheable) from transient errors (429/timeout/5xx: retry or short TTL), because the permanent miss cache plus the fail-closed gate turns one throttled fetch into run-long failures. Optionally use RWMutex for the hit path, though after coalescing contention is trivial. Accepted semantic change: secret rotation mid-run is now frozen run-wide instead of per-VUser — same effective behavior as today.

**Risk:** The shared cachingSecretResolver mutex becomes cross-VUser; hits are in-memory map reads so contention is negligible, but consider RWMutex/sync.Map. Cached-once semantics also mean a rotated secret is not re-fetched mid-run (already true per-VUser today).

### 7. Every incoming generator batch triggers a full fleet re-aggregation, LiveMetrics rebuild, WS broadcast, and SQLite write (O(N^2) fan-in)

- Location: `pkg/api/distributed_stream.go:402`
- Category: cpu
- Finder: distributed
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** consumeMetrics calls applyRemoteMetricsBatch + tr.publishLiveMetrics for EVERY batch received from ANY generator (distributed_stream.go:254-255). Each call re-runs aggregateDistributedMetricsBatches over ALL N stored per-target batches (concatenating every target's up-to-1024-per-group raw transactions, merging TDigests per transaction name), rebuilds the full LiveMetrics (sorting all durations, building fresh compression-200 TDigests per group at distributed_metrics.go:371-376), then publishLiveMetrics JSON-marshals the entire LiveMetrics for websocket broadcast and inserts a row into test_interval_summaries (metrics.go:498 -> SQLiteStore.SaveHistory). Generators publish one batch per group per second (service.go publishInterval=1s), so a test on N generators with G groups drives N*G full-fleet re-aggregations, N*G full-payload broadcasts, and N*G SQLite inserts per second — total merge work grows O(N^2*G) with fleet size, and the history table grows N*G times faster than the local path's 1 row/sec. Even duplicate/out-of-order batches (replaced by prior at line 350-354) still trigger the full pipeline.

**Evidence:** aggregated, fleetTPS, groupTPS := aggregateDistributedMetricsBatches(test.ID, batches)  // per received batch
... (caller, line 254-255)
live, finalStatus, finalError := b.applyRemoteMetricsBatch(tr, test, target, batch)
tr.publishLiveMetrics(test, live, nil)

**Hot path:** Runs once per generator per group per second for the whole duration of every distributed run; with 30 generators x 3 groups that is ~90 full-fleet merges + 90 full-JSON websocket broadcasts + 90 SQLite inserts per second per test.

**Verification notes:** Verified every link in the chain: per-group 1s publishers on generators (service.go:448,1397) send full per-test batches, and the controller's consumeMetrics (distributed_stream.go:254-255) runs aggregateDistributedMetricsBatches over ALL N stored target batches (line 402, transaction concat + per-name TDigest merges), then publishLiveMetrics does an unthrottled SQLite INSERT (sqlite_tests.go:225) and full-payload WS broadcast per batch — no coalescing anywhere, and out-of-order batches (line 350-354) still trigger the full pipeline. Additional unclaimed cost: goal-controller evaluation and buildLiveCapacityStatus also run per batch under the global tr.mu, adding lock contention. One nuance: the raw-duration sorting/fresh-TDigest path (distributed_metrics.go:344-376) is skipped when generators send FleetMetrics aggregates (applyDistributedAggregateMetrics early return at line 232), but the O(N^2*G) merge shape holds in both modes. Rated high not critical: quadratic controller CPU during distributed runs, severe at 30+ generator fleets, but not a correctness failure at small fleets.

**Refined fix:** Decouple ingestion from aggregation: consumeMetrics stores the batch under tr.mu, marks the test dirty, and computes lifecycle transitions synchronously (that logic is already cheap and lock-scoped, separate from aggregation). A single per-test aggregator goroutine wakes at a 1s cadence (or on dirty), runs aggregateDistributedMetricsBatches once per interval, and publishes. Refinements over the original: (1) on COMPLETE/FAILED, run one final synchronous aggregate+publish before completeDistributedTestRun so the carried-forward terminal snapshot (distributed_stream.go:319-348) is observed and completion is never delayed; (2) skip the dirty-mark for out-of-order batches replaced by prior (line 350-354) — they add no new data; (3) move the goal-controller evaluation (lines 411-423) into the coalesced tick, since it consumes the aggregated LiveMetrics and currently re-evaluates scaling N*G times/sec; (4) this also collapses N*G/sec acquisitions of the global tr.mu in publishLiveMetrics (which iterates all tests for capacity status) down to 1/sec, reducing API-handler lock contention.

**Risk:** Live metrics latency becomes up to one interval; COMPLETE/FAILED lifecycle batches must still be processed synchronously so completion is not delayed or lost.

### 8. SQLite opened without WAL/synchronous pragmas or connection-pool limits

- Location: `pkg/storage/sqlite.go:161`
- Category: database
- Finder: storage-sqlite
- Verified impact: high
- Confidence: high
- Effort: S

**Description:** sqliteDSN only appends busy_timeout(5000); journal_mode is never set, so the modernc.org/sqlite database runs in default rollback-journal (DELETE) mode with synchronous=FULL. Every write does a full journal cycle with multiple fsyncs and blocks all concurrent readers for its duration; readers likewise block the writer. NewSQLiteStore also never applies StoreConnectionOptions (applyConnectionOptions is Postgres-only), so database/sql opens an unbounded number of SQLite connections that then contend for the single writer, each stalling up to the 5s busy_timeout. The comment on sqliteDSN itself documents that read/write overlap already surfaced as transient 'database is locked' failures — busy_timeout papered over a problem WAL would remove.

**Evidence:** const pragma = "_pragma=busy_timeout(5000)"  // sqliteDSN adds ONLY this
...
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))  // no journal_mode/synchronous, no SetMaxOpenConns

**Hot path:** SQLite is the default production backend. Writers hit it once per second per running test (SaveHistory from publishLiveMetrics), on every authenticated request (UpdateUserIdentitySeen), on every audit event, scheduler-lease upsert, and fleet update — all while dashboard polling reads concurrently during live tests.

**Verification notes:** Verified at pkg/storage/sqlite.go:161-191: sqliteDSN appends only busy_timeout(5000), no journal_mode/synchronous pragma exists anywhere in the repo, and NewSQLiteStore never bounds the pool — factory.go:59 even silently drops StoreConnectionOptions for the default sqlite backend while Postgres honors them. Hot paths confirmed: SaveHistory writes once/sec per running test (metrics.go:617/646 → persistence.go:683) and UpdateUserIdentitySeen is an unthrottled write on every authenticated request via the auth middleware (middleware.go:144/209 → auth_resolution.go:189), all contending in DELETE-journal mode where writers block all readers; the sqliteDSN comment itself documents prior "database is locked" failures that busy_timeout only papers over. The suggested fix uses the correct modernc DSN mechanism and is low-risk with the stated WAL caveats.

**Refined fix:** In sqliteDSN, additionally append _pragma=journal_mode(WAL) and _pragma=synchronous(NORMAL), guarding EACH pragma individually against pre-existing occurrences in the DSN (the current guard only checks busy_timeout); keep busy_timeout since WAL does not remove writer-writer contention. Wire StoreConnectionOptions through the sqlite branch of NewConfiguredStore (factory.go:59 currently discards config.Connections) and call applyConnectionOptions with a small default MaxOpenConns (e.g. 4-8) when unset, so APEXLOAD_DATABASE_MAX_OPEN_CONNS finally works for SQLite. Do NOT bundle foreign_keys(1) into this change — FK enforcement is a behavior change requiring separate verification. Document the -wal/-shm sidecar files for backup/copy tooling and note WAL is unsafe on network filesystems.

**Risk:** WAL creates -wal/-shm sidecar files (backup/copy scripts must include them) and is unsafe on some network filesystems; synchronous=NORMAL trades a marginal durability window on OS crash. Existing DSNs that already pass pragmas must not be double-appended (the helper already guards busy_timeout only).

### 9. No index on test_interval_summaries(test_id): every history read/delete is a full-table scan

- Location: `pkg/storage/sqlite_tests.go:327`
- Category: database
- Finder: storage-sqlite
- Verified impact: high
- Confidence: high
- Effort: S

**Description:** getTypedHistory queries `FROM test_interval_summaries WHERE test_id = ? ORDER BY timestamp ASC`, but schemaIndexes (sqlite_schema.go:868-897) defines no index on test_interval_summaries (or test_history) — the tables have no PK either. Every GetHistory call scans the entire table across all tests ever run, then sorts. DeleteTest's `DELETE FROM test_interval_summaries WHERE test_id = ?` full-scans too. The table grows at ~1 row/second per running test (default MetricsIntervalSeconds=1) and is never pruned, so the scan cost grows without bound.

**Evidence:** FROM test_interval_summaries WHERE test_id = ? ORDER BY timestamp ASC`, testID)
-- schema: CREATE TABLE test_interval_summaries (test_id TEXT, ... ) with no index in schemaIndexes

**Hot path:** loadStoredTests (boot) and reloadStoredTests (HA leader failover) call GetHistory once per stored test, i.e. O(#tests × total_rows) row visits: with 500 historical runs averaging 3,600 rows each (1.8M rows), startup performs ~900M row visits. Retention sweeps that DeleteTest many runs pay the same full scan per delete.

**Verification notes:** Verified line-for-line: sqlite_tests.go:327 query matches, both test_interval_summaries and test_history have no PK/index (schemaIndexes at sqlite_schema.go:868-897 covers 20+ other tables but skips these), inserts arrive at 1 row/sec per running test (metrics.go:498, MetricsIntervalSeconds default 1), and no time-based pruning exists. The hot path is real: loadStoredTests (runner.go:956, every boot) and reloadStoredTests (testexecution.go:241, HA leader failover — directly extends the no-leader window) call GetHistory for every stored test = O(#tests × total_rows) full scans with 28-column row decode; additionally, project deletion (sqlite_projects.go:484-497) runs both full-scan DELETEs per test inside one write transaction, holding SQLite's single writer lock and stalling live-run metric inserts. One correction: the archive retention sweep (archive_retention.go:157) only removes archive directories on disk and never calls DeleteTest, so the "retention sweeps" bulk-delete claim is wrong — bulk DB deletes come from user test/project deletion instead; this does not change the verdict.

**Refined fix:** Add to schemaIndexes in pkg/storage/sqlite_schema.go: {`CREATE INDEX IF NOT EXISTS idx_interval_summaries_test_ts ON test_interval_summaries(test_id, timestamp)`, "idx_interval_summaries_test_ts"} and {`CREATE INDEX IF NOT EXISTS idx_test_history_test_ts ON test_history(test_id, timestamp)`, "idx_test_history_test_ts"}. Use composite (test_id, timestamp) on BOTH tables (not just the first) so ORDER BY timestamp is satisfied without a sort in both getTypedHistory and the legacy getLegacyHistory fallback. Verified rewriteSQL passes these through untouched for Postgres (no DATETIME token, no ? placeholders — keep "datetime" out of index names since rewriteSQL does a blind lowercase "datetime"→TIMESTAMPTZ substring replace), and CREATE INDEX IF NOT EXISTS is valid on a TimescaleDB hypertable, so the APEXLOAD_TIMESCALEDB path is unaffected. One-time index build on large existing DBs happens inside init(); acceptable, matches how the audit_events indexes were retrofitted.

**Risk:** One-time index build on a large existing table at startup (seconds to minutes for multi-GB tables); slight write amplification on the per-second insert path (negligible vs. the scan savings).

### 10. GET /tests/{id} and /tests/{id}/metrics re-marshal the full 3600-point LiveMetrics history on every 2s dashboard poll

- Location: `pkg/api/handlers.go:429`
- Category: network
- Finder: api-handlers
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** handleGetMetrics returns `history: filterLiveMetricsHistoryForGroups(test.History, ...)` and handleGetTest (line 130) returns maskManagedTest(test) whose History field carries the same slice (json tag `history,omitempty` in ManagedTest, runner.go:230). History grows one point per metrics interval up to maxManagedTestHistory=3600 (runner.go:797), and each LiveMetrics point embeds per-transaction TxMetrics maps, GroupMetrics, error patterns, and telemetry. The dashboard polls GET /tests/{id} every 2 seconds during a live run (dashboard/src/components/useExecutionTestPolling.ts:8, EXECUTION_POLL_INTERVAL_MS=2000), so late in a long run the server re-encodes and ships a multi-MB, ever-growing JSON blob every 2s per open viewer, even though only the newest point changed.

**Evidence:** writeJSON(w, http.StatusOK, map[string]any{
  "test_id": id,
  "status":  test.Status,
  "metrics": filterLiveMetricsForGroups(test.LiveMetrics, groupFilter),
  "history": filterLiveMetricsHistoryForGroups(test.History, groupFilter),
})

**Hot path:** Polled every 2s per viewer for the entire duration of a live test; history reaches 3600 points x (transactions map + group maps) per point, so encoding cost and payload grow linearly with run duration and multiply by concurrent viewers.

**Verification notes:** Verified every cited line: handleGetMetrics (handlers.go:429) returns the raw History slice when no group filter is set, and handleGetTest (handlers.go:130) returns a shallow maskManagedTest copy that keeps History (runner.go:230); history grows 1 point/sec by default (settings.go:535 MetricsIntervalSeconds=1) to the 3600 cap (metrics.go:476-479), each point a fully-enriched LiveMetrics with per-transaction/group/generator maps. Hot path confirmed: TestExecution.tsx:106 polls GET /tests/{id} via useExecutionTestPolling every 2s for the whole live run, with no gzip middleware and no incremental param — so ~7-35 MB of JSON is re-marshaled and shipped per poll per active viewer late in a run. Rated high rather than critical because cost is bounded (3600-point cap), visibility-gated (hidden tabs skip polls), and per-viewer — it degrades controller CPU/GC/egress during live tests but doesn't fail or corrupt anything.

**Refined fix:** Original fix is directionally right but History cannot simply be stripped from GET /tests/{id}: the dashboard seeds initial chart history from that snapshot and the WS-reconnect backfill (useExecutionMetricsStream.ts:118-136) re-calls getTest specifically to fetch history. Refined plan: (1) add ?history_limit=N and ?since=<RFC3339 timestamp> to GET /tests/{id}/metrics (timestamps are monotonic per run, so tail-slicing test.History is trivial and avoids index bookkeeping); (2) make useExecutionTestPolling call GET /tests/{id}?exclude=history (or a new lightweight summary param) since it only needs status/summary fields — the WS stream already delivers per-interval points; (3) point initial seeding and reconnect backfill at /metrics?history_limit=300 (client cap EXECUTION_STREAM_HISTORY_LIMIT=300 means anything beyond 300 points is discarded today anyway); (4) keep full history behavior as the default on GET /tests/{id} for external API compat, opting the dashboard out explicitly. Optionally add gzip middleware for application/json responses as a cheap orthogonal win.

**Risk:** Dashboard charts currently rebuild from the full history each poll; the client must merge incremental points and handle reconnect/gap cases. External API consumers relying on history in GET /tests/{id} would need the new endpoint.

### 11. Fleet inventory dials every generator sequentially per request — including the /health endpoint

- Location: `pkg/api/distributed_inventory.go:16`
- Category: network
- Finder: api-handlers
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** distributedExecutionBackend.SystemInventory probes generators one at a time in a for-loop: fresh dial, RPC probe, close, per target, each with its own timeout. An unreachable generator costs the full b.timeout before the next target is tried, so a fleet of N generators with k dead ones costs ~k*timeout serially. This runs fresh (no read-through cache) on every call to handleGetSystemInventory, handleGetFleetAlerts, handleGetFleetCapacity, handleGetFleetLoadZones — and on every handleHealthCheck (handlers.go:773 -> liveAvailableGeneratorCount, capacity.go:322-332) when availability source is configured_plus_live, i.e. on every Kubernetes liveness/readiness probe. A cached-read helper (cachedLiveAvailableGeneratorCount, capacity.go:344) already exists but the health handler bypasses it.

**Evidence:** for _, target := range specs {
	...
	probeCtx, cancel := context.WithTimeout(ctx, b.timeout)
	client, err := b.dial(probeCtx, target.Address)
	...
	snapshot := b.probeGeneratorLoad(probeCtx, target.Address, client)
	_ = client.Close()

**Hot path:** /health is hit by K8s probes every few seconds and the dashboard polls fleet/inventory pages; with a 50-generator fleet each call performs 50 sequential dial+probe round-trips and re-establishes connections each time, and one flaky generator adds a full timeout of latency to every health probe.

**Verification notes:** Verified the serial per-target fresh-dial loop at distributed_inventory.go:16-69 (new gRPC conn per target per call, per-target b.timeout default 10s, no pooling) and that Server.systemInventory has no read-through cache (write-only at system_inventory.go:117-118). handlers.go:773 does bypass cachedLiveAvailableGeneratorCount, but only under non-default configured_plus_live (default is 'configured', settings.go:548). The finding understates: the readiness probe (/api/v1/system/readiness, 10s period / 3s timeout in all K8s manifests) unconditionally runs the full fleet probe via generatorRPCReadiness (readiness.go:229), so one blackholed generator (>=10s serial stall vs 3s probe timeout) marks the controller unready on a stock deployment; Prometheus scrapes (prometheus_exposition.go:14,339) and fleet/calendar handlers also re-probe per request.

**Refined fix:** Broaden scope beyond handleHealthCheck: introduce a single singleflight-protected, TTL-cached (reuse defaultGeneratorSnapshotTTL=30s) inventory snapshot refreshed by a background loop that probes targets concurrently (bounded errgroup, e.g. 8-16 in flight) with a per-target timeout well under the K8s probe timeout. Have ALL read paths consume it: handleHealthCheck (handlers.go:773), generatorRPCReadiness (readiness.go:229) — the unconditional K8s readiness path and the real P0 here — handlePrometheusMetrics (prometheus_exposition.go:14,339), and the fleet/inventory/run-calendar handlers. Keep an explicit force-refresh only where freshness matters (e.g. handleGetSystemInventory with ?refresh=1). Optionally pool ClientConns per target instead of dial/close per probe. Risk stays as stated: availability lags failures by <=TTL; readiness still detects sustained fleet-down via the refreshed snapshot.

**Risk:** Cached availability can lag a generator failure by the snapshot TTL, slightly delaying capacity-gate decisions; concurrent probing raises momentary connection burst on generators (bound the fan-out).

### 12. buildRunArchiveReport does an O(transactions x summaries) bucket lookup and reloads/recomputes the whole archive on every report request

- Location: `pkg/api/archive_report.go:77`
- Category: algorithmic
- Finder: api-handlers
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** For every transaction in the archive, the report builder calls archiveSummaryBucketIndex(summaries, tx.Timestamp), which is a linear scan over the interval summaries (archive_loaders.go:407-417). A 1-hour run at high TPS can have millions of transactions and ~3600 summaries, giving billions of comparisons per report build. The same quadratic helper is used per transaction in loadArchiveIntervalSummariesForFilters (archive_loaders.go:272). Additionally, buildRunArchiveReport loads ALL transactions, resources, and summaries fully into memory (readAllArchiveTransactions) and recomputes the entire aggregation on every request — the JSON/CSV/PDF report endpoints, the HTML report, and the multi-run comparison (up to 10 full report builds per request, handlers_archive.go:553) all rebuild it from scratch even though archives are immutable.

**Evidence:** if len(summaries) > 0 {
	index := archiveSummaryBucketIndex(summaries, tx.Timestamp)
	... }
// archive_loaders.go:411: for index, summary := range summaries { if !timestamp.After(summary.Timestamp) { return index } }

**Hot path:** The archive report is the primary past-run results path (and the planned Run Report page makes /archive/report the default viewer); large-run archives have millions of transaction events, and comparison requests multiply the cost by up to 10 runs.

**Verification notes:** Verified all claims against source: archiveSummaryBucketIndex (archive_loaders.go:407) is a front-to-back linear scan called per transaction from buildRunArchiveReport (archive_report.go:77), loadArchiveIntervalSummariesForFilters (archive_loaders.go:272), and load_comparison.go:364; readAllArchiveRecords slurps whole JSONL files with no cap; RecordResult archives every transaction with no sampling; summaries accrue ~1/sec (metrics.go:591) so a 1h high-TPS run yields ~N*S/2 ≈ billions of comparisons per build. No caching exists — buildRunArchiveReport is rebuilt fresh at 9+ handler sites (report/CSV/PDF, comparison x10 via maxArchiveComparisonRuns=10 at handlers_archive.go:17/553, RCA, AI assist, SLA governance, release trend, annotations), and it is the primary past-run results path (plus planned Run Report default viewer). Cost is real and compounding: quadratic bucket scan plus full JSON re-decode of millions of rows plus multi-GB transient allocations per request.

**Refined fix:** Two corrections to the suggested fix. (1) Plain sort.Search assumes sorted summaries, but distributed archives concatenate per-generator summary files unsorted (distributed_archive.go:186), and transactions are also not globally time-ordered, so the cursor variant is unsafe. Exact behavior-preserving O(log S) replacement: once per build, compute a prefix-maximum timestamp array (non-decreasing for any input order); binary-search for the minimal index whose prefix-max is >= tx.Timestamp — provably identical to the current first-not-after linear scan, including the len-1 fallback. (2) Memoizing the built *RunArchiveReport by pointer is unsafe: handlers mutate the returned report in place (report.Integrity for the audit profile, attachReleaseTrend, applyArchiveComparisonActionScope, annotations), so a shared cached instance would leak per-request mutations across requests. Either deep-copy on cache hit, or better: precompute and store report.json as an archive artifact at Finalize (regenerated on re-upload; manifest hash as validity key), which also eliminates the per-request full JSONL re-decode — aligned with the Run Report Phase 0 plan already in docs/RUN_REPORT_IMPLEMENTATION_PLAN.md.

**Risk:** Report caching must invalidate on archive re-upload (supported via test-notes re-upload flow) — keying on the manifest hash handles that; binary search must preserve the current first-bucket-not-after semantics exactly or per-interval action stats shift by one bucket.

### 13. Every report request fully re-reads and re-parses the entire archive (no caching of built reports)

- Location: `pkg/api/archive_report.go:16`
- Category: caching
- Finder: archive-report
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** buildRunArchiveReport loads and JSON-decodes ALL transactions, resource samples, and interval summaries from the archive JSONL files on every single call, and no layer caches the result. Archives are immutable after Finalize (SHA-256 hashes are recorded in the manifest), so the report is a pure function of the archive, yet it is recomputed from raw multi-GB JSONL for every JSON/CSV/PDF/HTML request, every comparison run, every annotation preview, AI-assist call, and root-cause request.

**Evidence:** func buildRunArchiveReport(record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
	...
	transactions, err := readAllArchiveTransactions(record)
	resources, err := readAllArchiveResources(record)
	summaries, err := readAllArchiveSummaries(record)

**Hot path:** Called per-request from at least 9 endpoints (handlers_archive.go:309, :587, :672; handlers_release_trend.go:47; release_sla_governance.go:187; handlers_report_annotations.go:114; ai_assistance.go:541; ai_explain_engine.go:61; root_cause_analysis.go:50; handlers_scenarios.go:100). The new Run Report dashboard page (flag apexload.featureFlags.runReport) fetches /archive/report as its primary data source. A run with 1M+ transactions means hundreds of MB of JSON decoded per page view.

**Verification notes:** Verified: buildRunArchiveReport (archive_report.go:16) fully re-reads and json.Unmarshals every JSONL line via readAllArchiveRecords (archive_loaders.go:522), including Snapshot payloads with base64 screenshots; grep confirms zero caching anywhere in archive code and no persisted report artifact at Finalize; the manifest is even re-parsed 4x per build. Hotness is understated: collectReleaseTrendRuns (handlers_release_trend.go:47) and release_sla_governance.go:187 rebuild a full report per archived run in a release, and executive report/HTML/CSV paths trigger that loop via attachReleaseTrendForProfile on top of the current run's build, so one page view can decode N+1 full archives. I downgrade critical→high: this is the results-viewing path (per page view / on-demand), not the load-generation loop, though on memory-limited K8s controllers a trend/governance view over large archives risks OOM during a live test.

**Refined fix:** Two-tier fix. (1) Persist the built report as a manifest-listed artifact (report.json + SHA-256) at archive Finalize; on read, load it directly and rebuild only when runArchiveReportSchemaVersion mismatches (then rewrite). This survives restarts, works for cloud backends, and turns O(raw events) into O(report size). (2) Add a small in-process LRU (4-8 entries, bounded by approximate bytes) keyed on (testID, manifest.json content hash or mtime+size) — note storage.RunArchiveRecord has no ManifestSHA256 field, so hash the manifest file, which test-notes re-upload rewrites, giving natural invalidation. Mutation safety is cheap: handlers only assign top-level pointer fields (Integrity, ReleaseTrend, Verdict), so return a top-level shallow copy of the cached struct. Additionally, release_sla_governance.go:187 only needs manifest SLA flags, telemetry status, and completeness — serve that loop from a manifest-only summary instead of full reports for the biggest immediate win.

**Risk:** Cached report must not be mutated in place by handlers (attachReleaseTrend/verifyRunArchiveIntegrity currently write into the report); memory bound needed for the LRU since reports of huge runs are themselves large.

### 14. attachReleaseTrend rebuilds full archive reports for EVERY run in the release on each report request

- Location: `pkg/api/handlers_release_trend.go:47`
- Category: caching
- Finder: archive-report
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** collectReleaseTrendRuns calls buildRunArchiveReport(record) for every archived sibling run in the release, each of which re-reads and re-parses that run's complete transactions/resources/summaries JSONL. The trend only consumes ~8 scalar aggregates per run (Overview.Transactions/ErrorPct/AvgResponseMs/PeakTPS, Completeness.ScorePct, SLABreached, Telemetry.Status). handleGetRunArchiveReport attaches this unconditionally on the default JSON path (handlers_archive.go:360), so one report fetch for a run in a 20-run release parses 20+ full archives. release_sla_governance.go:187 repeats the identical pattern for the governance view.

**Evidence:** for _, test := range tests {
	record, ok := s.runner.GetRunArchive(test.ID)
	...
	report, err := buildRunArchiveReport(record)   // full archive re-parse per sibling run

**Hot path:** Runs on every JSON /archive/report request (unconditional attach at handlers_archive.go:360), every executive HTML/PDF/CSV export, /releases/{id}/trend, trend CSV export, and release SLA governance. Cost is O(sum of all archive sizes in the release) per request and grows with every archived run in a release.

**Verification notes:** Verified: collectReleaseTrendRuns (handlers_release_trend.go:47) calls buildRunArchiveReport per sibling run, which decodes the run's complete transactions/resources/summaries JSONL (archive_report.go:16-35, readAllArchiveRecords) with no cap and no caching anywhere in pkg/api; trendPointFromRun consumes only ~8 scalars. Attach is unconditional on the default JSON /archive/report path (handlers_archive.go:360, the endpoint the dashboard fetches on every archive/results view), profile-gated but active for executive HTML/PDF/CSV exports, and release_sla_governance.go:187 repeats the pattern. Worse than claimed: the requested run is parsed twice per request, the manifest is parsed 4x per report, and on cloud archive backends ensureArchiveMaterialized downloads every artifact of every sibling archive. I rate impact high rather than critical: it is request-driven (fetched once per view, not polled) and never on the live metric fan-in path, but cost is O(sum of sibling archive sizes) per click, unbounded in release size, in the same process as live test orchestration.

**Refined fix:** Persist the ~8 per-run trend aggregates (transactions, errors/error pct, avg response ms, peak TPS, peak VUsers, completeness score+status, telemetry status) into the run-archive DB row (storage.RunArchiveRecord) at Finalize — NOT into the manifest, since the manifest is the tamper-evidence root of trust (verifyRunArchiveIntegrity) and backfilling old manifests would mutate audited evidence. Build the trend from those rows; for pre-existing archives lacking aggregates, lazily do one full buildRunArchiveReport and write the aggregates back to the DB row (one-time backfill). Low-risk stopgap deployable immediately: an in-process cache of the 8 scalars keyed by test ID (archives are immutable after finalize, so no invalidation needed) plus reuse of the already-built current-run report in handleGetRunArchiveReport instead of rebuilding it as its own sibling. Also fix release_sla_governance.go:187 to read SLABreached/SLADetails from loadRunArchiveManifest (small JSON) instead of the full report. Note: buildRunArchiveReleaseTrendSummary needs per-run aggregates for the WHOLE release (RunCount + first-to-last Trajectory), not just current+predecessor, so persisting per-run aggregates is the right shape — the "only needs predecessor" note in the original fix is wrong for Trajectory.

**Risk:** Old archives lack the persisted aggregates; need a fallback (one-time backfill or lazy full build) to keep trends working for pre-existing runs.

### 15. O(transactions x summaries) linear scan: archiveSummaryBucketIndex called per transaction

- Location: `pkg/api/archive_loaders.go:407`
- Category: algorithmic
- Finder: archive-report
- Verified impact: high
- Confidence: high
- Effort: S

**Description:** archiveSummaryBucketIndex linearly scans the (time-ordered) summaries slice to find the bucket for a timestamp. buildRunArchiveReport calls it once per transaction (archive_report.go:77) and loadArchiveIntervalSummariesForFilters does the same per transaction (archive_loaders.go:272). With N transactions and M interval summaries this is O(N*M): for 1M transactions and a 1-hour run at 1s intervals (3600 summaries) that is ~1.8 billion comparisons per report build, multiplied by every uncached report/comparison/trend request.

**Evidence:** func archiveSummaryBucketIndex(summaries []IntervalSummaryRecord, timestamp time.Time) int {
	...
	for index, summary := range summaries {
		if !timestamp.After(summary.Timestamp) {
			return index
		}
	}

**Hot path:** Inner loop of buildRunArchiveReport (every report/export/trend/governance request) and of filtered summary loading (handleGetRunArchiveSummaries with action/group filters, comparison action scoping). Long runs have thousands of summaries; large runs have millions of transactions.

**Verification notes:** Confirmed at archive_loaders.go:407 and all three per-transaction call sites (archive_report.go:77, archive_loaders.go:272, load_comparison.go:364); no report caching exists, readAllArchiveTransactions has no row cap (one JSONL row per protocol.Result), and summaries accrue at 1/MetricsIntervalSeconds (min 1s), so O(N*M) at the claimed scale is real — and worse than claimed, since the range loop copies a ~200-byte IntervalSummaryRecord struct per iteration. It is the request path of every archive report view/export/verdict/comparison/trend/governance/AI-assist call, multiplied by attachReleaseTrend rebuilding every sibling run's report per single report request. Not on the live load-generation path, but a user-facing request path whose cost scales with run size (seconds to minutes of CPU per request for large runs).

**Refined fix:** Sort summaries once at load (sort.SliceStable by Timestamp inside readAllArchiveSummaries — its only three callers are exactly the bucketing consumers; the paged endpoints use readJSONLPage directly and are unaffected), then replace the scan with sort.Search: idx := sort.Search(len(summaries), func(i int) bool { return !timestamp.After(summaries[i].Timestamp) }); if idx == len(summaries) { idx = len(summaries)-1 }; empty slice still returns -1. This preserves exact current semantics (first summary >= tx timestamp, clamp past-end) for single-node archives, which are already ticker-ordered. Critically, the distributed merge (distributed_archive.go:187-195, appendArchiveContent per generator bundle) concatenates per-generator summary files WITHOUT global time ordering, so the current linear scan already mis-buckets distributed runs (all transactions land in the first generator's timeline segment); the load-time sort fixes that latent correctness bug too. Avoid the suggested monotonic-cursor alternative: transactions carry result.StartTime recorded at completion under concurrency and are not guaranteed monotonic.

**Risk:** Must preserve the current tie/edge semantics (first summary with ts >= tx ts; clamp past-end to last index) and tolerate any out-of-order summary rows.

### 16. Archive transaction pagination decodes the whole JSONL file per page; sorted requests materialize every matching event

- Location: `pkg/api/archive_loaders.go:35`
- Category: memory
- Finder: archive-report
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** loadArchiveTransactions with sort_by set uses readJSONLFiltered, which decodes and retains EVERY matching TransactionEvent (including snapshot/console/network payloads) in memory, sorts them all, then returns one 100-row page. Even without sorting, readJSONLPage (line 604) keeps decoding every remaining line after the page is filled just to compute the total, although for unfiltered requests the total is already recorded in manifest.TransactionCount.

**Evidence:** if filter.SortBy != "" {
	items, total, err := readJSONLFiltered(path, predicate)  // ALL matching events in memory
	...
	sortArchiveTransactions(items, filter.SortBy, filter.SortDir)
	paged := pageArchiveItems(items, page)

**Hot path:** Backs the dashboard's archived-transactions drilldown table: each page click, filter change, or sort toggle re-runs a full-file decode of a potentially multi-GB file; a sorted view of a 1M-event run loads all 1M events (with evidence blobs) to serve 100 rows.

**Verification notes:** Confirmed both mechanisms by reading pkg/api/archive_loaders.go: sorted requests (line 34-42) call readJSONLFiltered which retains every matching TransactionEvent — a struct carrying console/network/DOM/snapshot/protocol-evidence payloads (archive.go:128-167) — and unsorted readJSONLPage (line 604) full-decodes every remaining line just to count the total, while manifest.TransactionCount already holds the unfiltered count. There is no caching anywhere (file reopened per request, no sorted-index or page cache) and parseArchivePage doesn't cap limit. It is a click-driven archive drilldown, not a per-VUser or polling hot path, but one sort click on a large run (10^5-10^7 events is normal output for this product) materializes GB-scale heap in the same controller process that runs live tests, making it an OOM vector plus O(file) CPU per page interaction.

**Refined fix:** Original fix is sound; refine it: (1) sorted path — first pass decodes only {byteOffset, sortKey, timestamp, actionName} (tiebreak fields) into a compact slice, sort, then seek+decode only the requested page; cache this index in a small LRU keyed by (testID, sortBy, filterHash) since archives are immutable after Finalize, so subsequent page clicks on the same sorted view are O(page). (2) readJSONLPage — when predicate == nil, stop scanning once offset+limit rows are collected and take total from manifest.TransactionCount (or the artifact Count entry for resources/summaries); when a predicate exists, decode the counting-phase remainder into a lightweight struct containing only the scalar filter fields (the filter never touches the heavy evidence fields). (3) Cap limit in parseArchivePage (handlers_archive.go:730) — it currently accepts any positive value. Note the dashboard always sends an action filter (useExecutionArchiveModel.ts auto-selects one), so the manifest-count shortcut alone won't help dashboard traffic — the light-struct counting decode or the parquet path is what fixes the common case.

**Risk:** Total semantics for filtered queries still require a full scan; offset-based seek needs stable files (safe — archives are immutable after Finalize).

### 17. JMX parser recounts newlines from file start for every XML element (O(elements x file size))

- Location: `pkg/convert/jmeter/converter_xml.go:29`
- Category: algorithmic
- Finder: importers
- Verified impact: high
- Confidence: high
- Effort: S

**Description:** parseXML calls sourceview.LineForOffset(content, decoder.InputOffset()) on every StartElement (line 29) and again on every EndElement (line 46). LineForOffset is strings.Count(content[:offset], "\n") — a scan of the entire file prefix. JMX is extremely element-dense (each HTTP sampler is ~30 elements: stringProp/boolProp/etc.), so a multi-MB plan triggers hundreds of thousands of prefix scans, each averaging half the file size: a 10MB / 100k-element JMX does ~2 TB of byte scanning, freezing the import for minutes.

**Evidence:** case xml.StartElement:
    node := &xmlNode{Name: t.Name.Local, Attrs: map[string]string{}, StartLine: sourceview.LineForOffset(content, int(decoder.InputOffset()))}
...
case xml.EndElement:
    stack[len(stack)-1].EndLine = sourceview.LineForOffset(content, int(decoder.InputOffset()))
// sourceview.LineForOffset: return strings.Count(content[:offset], "\n") + 1

**Hot path:** Runs once per XML element on every JMeter import; users import multi-MB JMX exports where element count is in the tens to hundreds of thousands. This is the backend cost behind large-import latency.

**Verification notes:** Verified converter_xml.go:29/:46 call sourceview.LineForOffset twice per XML element, and LineForOffset (source_review.go:105) is a strings.Count prefix scan with no index or caching — O(elements × file size) overall. The JMeter import endpoint accepts up to 50 MiB (jmeterImportMaxBodyBytes, middleware.go:435) and ConvertJMX runs parseXML synchronously in the HTTP handler, so a 10MB/100k-element plan does ~1-2 TB of byte scanning (tens of seconds to minutes) and larger plans get quadratically worse. Not per-VUser hot, but it is the dominant cost of every large JMeter import/validate — a headline migration workflow — so the hot-path claim holds under the multi-MB-import criterion.

**Refined fix:** Original fix is correct: decoder.InputOffset() is monotonic, so keep lastOffset/lastLine and add strings.Count(content[lastOffset:offset], "\n") per token — O(file) total. Prefer the shared-index variant: add a sourceview.LineIndex type (precomputed newline-offset slice built once in O(N), sort.SearchInts per lookup) and adopt it in jmeter, postman/converter.go:408, openapi/converter.go:513, wsdl/converter.go:683, k6/converter.go:347/473, sample/sourceview.go, devweb/converter.go:280, AND the duplicated copy javaLineForOffset at enterprisearchive/java_scanner.go:293. Preserve LineForOffset's clamp semantics (offset <= 0 -> line 1; offset > len(content) -> line of last byte) so existing StartLine/EndLine test expectations hold.

**Risk:** Low — pure bookkeeping change; verify off-by-one parity with existing StartLine/EndLine expectations in converter tests.

### 18. correlate.Detect is O(observations^2 x candidates) and re-decodes/re-parses each request body per membership check

- Location: `pkg/convert/correlate/correlate.go:118`
- Category: algorithmic
- Finder: importers
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** For every candidate extracted from every observation, Detect scans ALL later observations with ObservationContains. Each ObservationContains call does substring scans over URL/body/all headers, plus decodedContains (url.QueryUnescape allocating a fresh decoded copy of the URL and body every call, correlate.go:504,527) and formContainsValue (a full url.ParseQuery of the request body every call, correlate.go:509-511). With O observations averaging C candidates each, that is O*C*O/2 ObservationContains calls, each re-decoding and re-parsing the same request bodies from scratch.

**Evidence:** for _, candidate := range Candidates(observation.ResponseBody, observation.ResponseHeaders) {
    ...
    for later := index + 1; later < len(observations); later++ {
        if ObservationContains(observations[later], candidate.Value) {
// ObservationContains -> decodedContains(raw) -> url.QueryUnescape(raw)  (fresh alloc per call)
//                     -> formContainsValue(body) -> url.ParseQuery(body) (full parse per call)

**Hot path:** correlate.Detect runs on every HAR/sample import (sample/correlate_redact.go:32), every paramize correlation pass (paramize/correlations.go:19), recorder baseline conversion (pkg/recorder/converter.go:579), browser-to-HTTP conversion, and the correlation-rulesets API handler. A 1,000-entry HAR with ~5 candidates per response yields ~2.5M ObservationContains calls, each re-decoding/re-parsing multi-KB bodies — seconds to minutes of CPU plus massive GC pressure.

**Verification notes:** Verified the exact code: Detect's inner loop (correlate.go:118) calls ObservationContains O(observations^2 x candidates) times, and each call re-runs url.QueryUnescape on the request URL and body (correlate.go:504,531) and url.ParseQuery on the body (correlate.go:510) with no caching anywhere — the Observation struct has no memoized fields and the cheap Contains fast path only short-circuits on matches, so the expensive decode/parse runs on nearly every (non-matching) pair. The finding actually understates it: the seen-dedup map (line 123-126) only marks values that WERE reused, so recurring non-reused candidate values rescan all later observations every time with zero dedup. All six claimed call sites confirmed at cited lines (HAR import, paramize pass on all importers, recorder converter :579, browser-to-HTTP :466, correlation-rulesets handler :175, salesforce analyzer :1681/:1796); these are import/conversion paths in the shared controller process, and multi-MB thousand-entry imports are a documented real scenario in this repo.

**Refined fix:** Original fix is sound; two refinements. (1) ObservationContains is an exported API used by salesforce/analyzer.go, so do not change its signature — add a cached form (e.g. type observationSurfaces with lazily computed urlDecoded(RequestURL), urlDecoded(RequestBody), ParseQuery(RequestBody) flattened to key/value strings, and decoded header values) built in a parallel slice inside Detect and a ContainsCached method, preserving the exact decoded!=raw gate by caching the decoded string and comparing to raw. Refactor salesforceCandidateReuses (analyzer.go:1675, also the ID15 loop at :1699) to accept the same cached surfaces. (2) Add negative caching in Detect: track candidate values whose scan produced empty usedIn and skip them on re-occurrence — provably output-preserving, since a scan from index i covers i+1..end, so a value absent after i is absent after any j>i; this closes the seen-map gap where repeated non-reused values rescan O(n) each time. The multi-pattern (Aho-Corasick) rewrite is optional; memoization alone removes the dominant per-call alloc/parse cost.

**Risk:** Behavioral parity required: memoization must preserve the exact Contains/decoded/form-field match semantics or correlation output (and its ordering) changes.

### 19. Source-review secret redaction does full-content strings.ReplaceAll per exchange per file (O(files x exchanges x content))

- Location: `pkg/convert/sample/correlate_redact.go:175`
- Category: algorithmic
- Finder: importers
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** redactExchangeSecrets loops over every parsed exchange and, for each sensitive header value and each body whose sanitized form differs, runs replaceRawAndJSONEscaped — two strings.ReplaceAll passes over the ENTIRE preserved source file (typically the original multi-MB HAR), plus a jsonEscapeString encode of the value. It also runs paramize.SanitizeBodySecrets (2 regex scans) on every request and response body per file. Since most HAR entries repeat the same Authorization/token values, the same multi-MB content is rescanned and reallocated thousands of times.

**Evidence:** for _, ex := range exchanges {
    content = redactSensitiveHeaderValues(content, ex.Headers)   // ReplaceAll x2 over full file per sensitive header
    if ex.BodyEncoding == "" {
        content = redactBodySecrets(content, ex.Body)
    }
    if ex.Response != nil {
        content = redactSensitiveHeaderValues(content, ex.Response.Headers)
        content = redactBodySecrets(content, ex.Response.Body)
    }
}

**Hot path:** Called from sample/converter.go:209 on every HAR/raw-HTTP import, unconditionally (runs regardless of the sanitize option). A 20MB HAR with 2,000 entries carrying an Authorization header means ~4,000+ full 20MB ReplaceAll scans (~80+ GB of memory traffic), each hit allocating a new 20MB string.

**Verification notes:** Verified the code at correlate_redact.go:175 matches the evidence exactly: per-exchange full-content ReplaceAll with no dedup, called unconditionally from converter.go:209 on every HAR/sample import and browser-recorder conversion (HAR cap 50MiB, synchronous in the handler). The O(exchanges x content) scanning is real (2,000 entries x 20MB = ~40GB scanned, seconds to minutes at the 50MiB cap), but the allocation claim is overstated: strings.ReplaceAll returns the original string alloc-free once the first exchange has redacted all occurrences of a repeated token, so duplicates cost full Count scans, not 20MB copies; allocations are bounded by unique secret values/bodies. Complexity class, hot path, and fix direction all hold.

**Refined fix:** Split the fix into a risk-free core and an optional optimization. Core (behavior-identical): before the files loop in redactSourceReviewSecrets, build the deduped needle list once per import — iterate exchanges, collect each sensitive header value into an ordered set, and for each un-encoded body compute SanitizeBodySecrets(body) once (memoized by body string) and collect (body -> sanitized) only when changed; then per file run replaceRawAndJSONEscaped once per unique needle. Skipping duplicate needles is exactly equivalent to today's guaranteed-no-op ReplaceAll calls, and hoisting sanitize out of the files loop avoids re-regexing the same bodies per file. Optional: collapse to one strings.NewReplacer pass per file — but note Go's Replacer matches patterns in ARGUMENT order at each position (not longest-match), so sort needles longest-first and keep the existing final paramize.SanitizeBodySecrets(content) catch-all at line 187 as the safety net; add a test for a body needle containing an already-redacted header token.

**Risk:** Replacer applies patterns in one pass (no re-matching of replaced text) — verify overlapping-value cases (a body containing a header token) still redact identically; longest-match-first ordering preserves current behavior.

### 20. Dashboard polls GET /tests every 3s and the payload embeds full run history (up to 3600 LiveMetrics per test)

- Location: `dashboard/src/components/DashboardPage.tsx:569`
- Category: polling
- Finder: dashboard-render
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** DashboardPage re-fetches the entire test collection every 3 seconds via api.listTests and unconditionally calls setTests(testRes.tests || []) with a fresh array. The backend list endpoint (pkg/api/handlers.go:109-119) serializes each ManagedTest including its History field (pkg/api/runner.go:230, `json:"history,omitempty"`), and every stored test is hydrated with up to maxManagedTestHistory=3600 LiveMetrics entries at boot (pkg/api/persistence.go:171, 302-303) — history is never stripped by maskManagedTest (pkg/api/runtime_mask.go:121-136). So as completed runs accumulate, each 3s poll re-downloads and JSON.parses megabytes of per-second metric records, and the fresh array identity re-runs every downstream useMemo and re-renders the full page even when nothing changed. ResultsPage repeats the same full-payload poll every 10s (ResultsPage.tsx:434) and CommandCenterPrototype's run discovery every 5s (CommandCenterPrototype.tsx:1020).

**Evidence:** const [testRes, healthRes] = await Promise.all([
  api.listTests({ application_id: activeApplicationId!, project_id: activeProjectId! }),
  ...
])
setTests(testRes.tests || [])
...
const interval = setInterval(load, 3000)

**Hot path:** The dashboard landing page is open in every user's browser and polls continuously; payload size scales with (number of runs ever kept in memory) x (up to 3600 rich LiveMetrics records each, including per-group maps, error patterns, dependency health). Serialization cost also hits the Go backend on every poll from every open tab.

**Verification notes:** Verified the full chain: DashboardPage.tsx:559-569 polls listTests every 3s with unconditional setTests; handlers.go:93-120 serializes maskManagedTests, and runtime_mask.go:121-136 never strips History (runner.go:230, json:"history,omitempty", cap 3600); persistence.go:280-308 hydrates history for every stored test at boot with no query LIMIT, metrics.go:476 appends full rich LiveMetrics that is never cleared post-run, and there is no gzip middleware. Downgraded from critical to high: boot-hydrated entries are the slim IntervalSummaryRecord shape (~300-700B each, not the rich per-group/transaction maps — those only persist for runs completed in-process), and DashboardPage has a document.visibilityState guard so hidden tabs don't poll (ResultsPage's 10s poll does not). Still multi-MB uncompressed payloads at modest run accumulation, marshaled per poll per user, plus ListTests* taking the exclusive tr.mu.Lock contending with the metrics processor.

**Refined fix:** Original fix is sound; refinements: (1) strip History in the list path inside maskManagedTest (it already shallow-copies) unless ?include=history, but KEEP LiveMetrics (last sample) — DashboardPage charts use the websocket-fed metricsHistory prop, not test.history, so the landing page loses nothing; (2) TestHub's goal-baseline readers (TestHub.tsx:666/674/682) only need peak TPS and the last history point — add peak_tps to the summary or have TestHub request include=history rather than keeping full history for everyone; (3) add the fingerprint-skip (id+status+total_transactions of last sample) before setTests on all three pollers, and copy DashboardPage's visibilityState guard to ResultsPage.tsx:434 which lacks it; (4) cheap orthogonal win: gzip middleware for JSON responses. Also note maskManagedTest's shallow copy shares the History slice that metrics.go:476 appends to under tr.mu while writeJSON marshals it outside the lock — a latent data race that stripping History in the list path incidentally narrows.

**Risk:** Some UI reads test.history from list-shaped payloads (TestHub goalBaselinePeakTps uses run.history; DashboardPage falls back to live_metrics) — stripping history must be opt-in or those readers switch to archive reports.

### 21. Execution view polls full test snapshot (incl. 3600-point history) every 2s and always sets fresh identity, invalidating the whole chart pipeline

- Location: `dashboard/src/components/useExecutionTestPolling.ts:55`
- Category: polling
- Finder: dashboard-render
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** useExecutionTestPolling calls api.getTest(testId) every 2000ms and setTest(data) unconditionally. GET /tests/{id} (pkg/api/handlers.go:123-131) returns the full ManagedTest including History capped at 3600 entries (pkg/api/runner.go:797), so a long run re-downloads its entire per-second metric history every 2 seconds — even though useExecutionMetricsStream already receives the same points over WebSocket and has a reconnect backfill (useExecutionMetricsStream.ts:118-136). Because test is a new object each tick, useExecutionChartModel re-runs mapExecutionHistory over all points and rebuilds chartData (useExecutionChartModel.ts:30-46), which changes the granularBaseData identity and defeats TestExecutionCharts' per-chart derived-series cache (`cachedDerived.sourceChartData === chartData` at TestExecutionCharts.tsx:2236), forcing downsample/smoothing recompute for every visible chart every 2s — including for PAUSED runs where nothing changed. CommandCenterPrototype has its own identical inline 2s getTest poll (CommandCenterPrototype.tsx:977).

**Evidence:** const data = await api.getTest(testId)
setTest(data)
...
const interval = setInterval(load, EXECUTION_POLL_INTERVAL_MS) // 2000ms

**Hot path:** Runs for the entire duration of every watched live test (the platform's core screen); payload and remap cost grow linearly with run length up to 3600 points x per-group/dependency maps, repeated 30x/minute per open viewer.

**Verification notes:** Verified every link in the chain: the 2s unconditional getTest poll (useExecutionTestPolling.ts:44-55); GET /tests/{id} returning full History — maskManagedTest strips only RuntimeSettings and GetTest's shallow clone keeps the 3600-cap slice of fully-enriched LiveMetrics (per-transaction/group/dependency maps, metrics.go:475-479), so a mature run ships multi-MB JSON per tick per viewer; duplication with the WS stream + its reconnect backfill (useExecutionMetricsStream.ts:108-136); and the identity cascade — fresh test.history each parse re-runs mapExecutionHistory, buildExecutionChartData always concatenates+dedups backend history even with live WS data, and the new chartData identity defeats the derivedSeriesCacheRef check at TestExecutionCharts.tsx:2236, recomputing downsample/smoothing for every chart every 2s. PAUSED is not in COMPLETED_TEST_STATUSES so paused runs keep paying; CommandCenterPrototype.tsx:962-984 duplicates the poll without even the visibility guard; no exclude/since API param exists. Existing mitigations (hidden-tab skip, stop-on-terminal, per-chart cache) don't cover the hot case of a visible watched live run.

**Refined fix:** Server: add ?include_history=false (or history_since=<RFC3339>) handling in handleGetTest/GetTest so interval ticks skip serializing History; default stays full so initial chart seeding and the WS reconnect backfill (useExecutionMetricsStream.ts:124-131) are untouched. Client: in useExecutionTestPolling, request full history only on the first load; on ticks fetch without history and merge via functional setTest, carrying forward prev.history so the useExecutionChartModel memo on test?.history stays cached. Cheap interim client-only win shippable first: setTest(prev => prev && data.history?.length === prev.history?.length && lastTimestampEqual ? { ...data, history: prev.history } : data) — stabilizes identity for paused/idle ticks immediately. Apply the same treatment (or migrate to the shared hook) for the inline poll at CommandCenterPrototype.tsx:962-984, and add its missing document.visibilityState guard.

**Risk:** TestExecution seeds charts from test.history on first load and after WS gaps; the delta/exclude path must keep the initial hydration and the reconnect backfill working.

### 22. Command-center shared crosshair sets root-level state on every mousemove, re-rendering the entire 3770-line component and every Recharts chart per mouse event

- Location: `dashboard/src/components/TestExecutionCharts.tsx:965`
- Category: rendering
- Finder: dashboard-render
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** On the command-center surface, handleChartMouseMove builds a fresh cursor object on every mousemove and calls updateSharedCursor, which invokes onActiveCursorChange — wired to setActiveGraphCursor at the CommandCenterPrototype root (CommandCenterPrototype.tsx:928, passed down at 1596-1597). Every mouse movement over any chart therefore re-renders the whole CommandCenterPrototype tree and all displayed charts, because the chart wall is one inline displayedCharts.map (TestExecutionCharts.tsx:2172) with no React.memo boundary per chart card. The derived-series cache avoids data recomputation, but each of the N Recharts charts still rebuilds its element tree and re-runs layout at pointer-event frequency to move one ReferenceLine crosshair.

**Evidence:** const nextCursor = activeLabel !== undefined || typeof timestampMs === 'number'
  ? { label: activeLabel, timestampMs }
  : null
updateSharedCursor(nextCursor)  // -> setActiveGraphCursor at CommandCenterPrototype root

**Hot path:** Fires continuously while an operator moves the mouse over the command-center graph wall during a live test — dozens of events per second, each re-rendering ~6-20 Recharts charts plus hero strip, tables, and controls.

**Verification notes:** Verified every link in the chain by reading the code: handleChartMouseMove (TestExecutionCharts.tsx:954-968) creates a fresh cursor object per mousemove and updateSharedCursor routes it to setActiveGraphCursor at the CommandCenterPrototype root (line 928 via props at 1596-1597/2029/3275), so Object.is bailout never fires and the whole 3770-line tree plus all inline-mapped, unmemoized Recharts charts (map at line 2172, zero React.memo/throttle/rAF in either file) re-render per pointer event. Critically, this is not pre-existing: the parallel zoom path's reducer returns the identical state object when not dragging (testExecutionChartZoomModel.ts:82-85), so plain hover caused no re-renders before the crosshair — the crosshair setState is the sole driver. The derivedSignature cache only skips data recompute, not Recharts element-tree rebuild/layout, exactly as claimed. Hot for an operator actively monitoring a live run (60-120 events/s over a 6-20 chart wall), though it is client-side jank rather than a server-scale issue.

**Refined fix:** Layer the fix cheapest-first: (1) bail out of updateSharedCursor when the new label/timestampMs equal the current sharedCursor — consecutive mousemoves within the same downsampled bucket (the common case) become no-ops, a one-line zero-risk win; (2) coalesce cursor setState through requestAnimationFrame so at most one update lands per frame; (3) stop lifting cursor state to the CommandCenterPrototype root — omit onActiveCursorChange from CommandRuntimeGraphs and use the existing localActiveCursor fallback (TestExecutionCharts.tsx:859-866), since key={variant} at line 3250 already remounts/resets on variant switch and the root's only other use is a reset at line 1490; this confines re-renders to the charts component without any memoization risk. Only if jank persists, proceed to the finder's React.memo-per-chart-card + narrow cursor context, which requires stabilizing all inline handler props (onMouseMove closures are currently recreated every render) or the memo silently defeats itself.

**Risk:** Memoizing chart cards requires all handler props to be stable (useCallback) or the memo silently defeats itself; the crosshair must still track correctly across differently-downsampled charts.

### 23. Dashboard fires one getTestArchiveReport request per archived run in the release (unbounded N+1 burst on mount)

- Location: `dashboard/src/components/DashboardPage.tsx:668`
- Category: network
- Finder: dashboard-render
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** The performance-trend effect collects every archived completed run in the active release (archivedReleaseRuns has no cap) and issues a parallel api.getTestArchiveReport(test.id) for each one it hasn't fetched yet. Each report is a heavy backend artifact (overview, completeness, per-action stats, manifest, browser breakdowns) that may be generated on demand. On first dashboard open for a release with hundreds of archived runs this fires hundreds of simultaneous report requests, saturating the browser connection pool and hammering the report generator, only to render one summary table row per report.

**Evidence:** const missingReports = archivedReleaseRuns.filter(test => !(test.id in archiveReports))
...
Promise.all(
  missingReports.map(async test => {
    const report = await api.getTestArchiveReport(test.id)

**Hot path:** Runs on every dashboard mount and whenever a new archived run appears; request count grows linearly with release run history, which grows forever.

**Verification notes:** Confirmed at DashboardPage.tsx:661-690: uncapped Promise.all of getTestArchiveReport over all archived runs in the release (or ALL project tests when no active release, line 641), with no client/server pagination on listTests. The finding understates severity: the backend JSON path (handlers_archive.go:359-360) unconditionally runs attachReleaseTrend, which rebuilds every sibling archive report from raw transaction logs (no cache, possible cloud-bucket download via ensureArchiveMaterialized), so one dashboard mount costs ~N²+N full archive parses — and the dashboard never even reads report.ReleaseTrend. Fires on every dashboard mount since archiveReports is component state.

**Refined fix:** Priority 1 (backend, biggest win): make ReleaseTrend opt-in on the JSON report path — e.g. gate s.attachReleaseTrend behind ?include=release_trend or the existing attachReleaseTrendForProfile — turning the N² sibling rebuilds into N; audit RunReportPage/ReportComposer consumers that may rely on the field before changing the default. Priority 2 (frontend): cap the trend fetch to the most recent ~20 archived runs with bounded concurrency (4-6), update the dynamic 'Last {n} archived runs' chip, and fix the no-active-release fallback (line 641) that fans out over the whole project. Priority 3: add an in-memory LRU of built reports keyed by test ID (archives are immutable; the audit profile already re-verifies integrity separately), which also speeds the /releases/{id}/trend endpoint and the upcoming Run Report consumers; longer-term, expose the few overview fields the trend table needs in the test-list DTO or a batch endpoint.

**Risk:** The release trend stats (buildReleaseTrendStats) currently aggregate over all fetched reports; capping changes the aggregation window and should be reflected in the label.

### 24. 2s test-detail poll downloads the full 3600-point live-metrics history on every tick

- Location: `dashboard/src/components/useExecutionTestPolling.ts:44`
- Category: network
- Finder: dashboard-data
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** useExecutionTestPolling calls api.getTest(testId) every 2 seconds (EXECUTION_POLL_INTERVAL_MS = 2000). The backend handler (pkg/api/handlers.go:123 handleGetTest -> maskManagedTest, a shallow copy at pkg/api/runtime_mask.go:125) serializes the entire ManagedTest including History []*LiveMetrics (json:"history,omitempty", pkg/api/runner.go:230), which grows one point per MetricsIntervalSeconds up to maxManagedTestHistory = 3600 (pkg/api/runner.go:797). Each LiveMetrics carries per-transaction maps, per-group maps, error patterns, telemetry and resource snapshots (pkg/api/runner.go:326-364), easily 1-10KB JSON per point. Neither consumer (TestExecution.tsx:106, RunReportPage.tsx:57) ever reads test.history — charts come from the WebSocket-fed metricsHistory prop — so a payload that reaches multiple MB mid-run is fetched, marshaled server-side, downloaded, and JSON.parsed every 2 seconds for pure waste, precisely while the controller is busiest driving a live load test.

**Evidence:** const data = await api.getTest(testId)
...
const interval = setInterval(load, EXECUTION_POLL_INTERVAL_MS) // 2000ms

**Hot path:** Runs continuously for every open TestExecution or RunReportPage view during a live run; a 1-hour run at the default 1s metrics interval accumulates 3600 rich LiveMetrics points, so each 2s poll can transfer several MB and force a full server-side JSON marshal of the history while thousands of VUsers are executing.

**Verification notes:** Verified the full chain: 2s poll (useExecutionTestPolling.ts:44,55) -> GET /tests/{id} (useTestApi.ts:312) -> handleGetTest (handlers.go:123) -> maskManagedTest shallow copy keeping History (runtime_mask.go:121-136), with History capped at 3600 rich LiveMetrics points appended at >=1s intervals (metrics.go:476-479,587-590; runner.go:230,326-364) and no HTTP compression — so multi-MB marshal+download per tick mid-run is real, and CommandCenterPrototype.tsx:964-979 duplicates the same 2s poll. However the finding's central 'pure waste, nobody reads test.history' claim is FALSE: useExecutionChartModel.ts:30-32 feeds test?.history into the execution chart (TestExecution.tsx:152-157) and it is the only source of full-run data beyond the 300-point WS cap (useExecutionMetricsStream.ts:9), and RunReportPage stops polling on terminal status so it fetches once, not per-tick. The inefficiency is real (O(full-history) transfer every 2s where O(delta) suffices) but the suggested fix as written would silently truncate the live chart; impact is high, not critical.

**Refined fix:** Do NOT strip History from GET /tests/{id} or blindly switch useExecutionTestPolling to a summary endpoint — TestExecution's chart consumes the polled test.history (useExecutionChartModel.ts:30-32) and the WS series only holds the last 300 points. Instead: (1) add ?since=<timestamp> (or ?offset=) support to the existing GET /tests/{id}/metrics handler (pkg/api/handlers.go:409-431), which today also returns full history; (2) change TestExecution/useExecutionMetricsStream to seed history once (initial full fetch) and append via WS plus incremental ?since fetches (reuse the existing mergeMetricsHistory in testExecutionMetricsModel.ts); (3) then add a summary=1 mode (History/LiveMetrics deep fields omitted) to GET /tests/{id} and point useExecutionTestPolling and CommandCenterPrototype.tsx:964's own 2s getTest poll at it. Also include the list endpoints in remediation: handleListTests -> maskManagedTests serializes History for every test, including completed runs rehydrated with up to 3600 points (pkg/api/tests.go:282-327, persistence.go:291-306), which is a larger payload than the detail endpoint.

**Risk:** Must audit other getTest callers for test.history / test.live_metrics reliance before stripping fields from the shared endpoint; a new query param avoids that risk entirely.

### 25. Unpaginated /tests list (including every test's full metrics history) polled from three places

- Location: `dashboard/src/App.tsx:818`
- Category: network
- Finder: dashboard-data
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** AppShell's topbar effect polls testApi.listTests({project_id, application_id}) every 10s just to derive id/name/status for run chips (selectTopbarActiveRuns). The backend list endpoint (pkg/api/handlers.go:89-121) has no pagination and returns full ManagedTest objects; at boot loadStoredTests (pkg/api/persistence.go:171) hydrates EVERY stored test's history (up to 3600 LiveMetrics each via loadStoredTestHistory), so the response body scales as (number of runs ever kept) x (history size). DashboardPage.tsx:560 independently polls the same collection every 3s, and on desktop a second AppShell effect (App.tsx:851) polls an unfiltered global listTests() every 10s — three overlapping pollers of a payload that can reach tens of MB, each re-marshaled under the runner mutex.

**Evidence:** const response = await testApi.listTests({
  project_id: activeProject.id,
  application_id: activeApplication?.id,
})
...
const interval = window.setInterval(() => { void loadTopbarRuns() }, 10000)

**Hot path:** Fires every 3-10s per open dashboard tab for the app's entire lifetime; response size grows unboundedly with run retention because every archived run's 3600-point history rides along (ManagedTest.History json:"history,omitempty", pkg/api/runner.go:230). Multiplied across concurrent viewers during a test campaign this dominates controller CPU (JSON marshal) and bandwidth.

**Verification notes:** Confirmed all core mechanisms: App.tsx:818 (10s topbar poll, ungated, uses only id/name/status), App.tsx:851 (10s unfiltered poll, desktop shell only), components/DashboardPage.tsx:560 (3s visibility-gated poll), handleListTests has no pagination, maskManagedTest is a shallow copy leaving History intact, and persistence.go:171 hydrates up to 3600 LiveMetrics per stored test at boot (maxManagedTestHistory=3600, runner.go:797), so list payloads grow ~1-2MB per retained hour-long run. Two claims refuted: marshaling happens AFTER tr.mu is released (tests.go:282-297 → writeJSON), so this taxes marshal CPU/bandwidth, not the runner lock; and the third poller only exists on the Wails desktop surface. Also found TestHub.tsx:666/1746 as a fourth listTests consumer that genuinely reads run.history for sparklines, validating the stated risk. Downgraded critical→high: DashboardPage poll is visibility-gated, retention pruning bounds run count, and no lock contention.

**Refined fix:** Default the list endpoints to a summary projection: in maskManagedTest (or a new summarizeManagedTest used only by handleListTests), nil out History but KEEP LiveMetrics (last point — DashboardPage/TestHub row badges read live scalars) and Archive (it is small: paths+counts only, storage.go:894-909). Add an opt-in query param (?include=history or ?history_tail=60) returning a capped tail for TestHub sparklines, and migrate TestHub.tsx:1746 to it; 60 points is plenty for a sparkline. Deep-copy into the projection rather than sharing pointers — this also fixes the latent data race of marshaling shared *ManagedTest/History outside tr.mu while the runner mutates them. On the client, dedupe pollers: one shared poll store (context or query cache) feeding the topbar (App.tsx:818), the desktop badge (App.tsx:851 can derive its count from the same store instead of a second unfiltered fetch), and DashboardPage; add a visibility gate to the topbar interval like DashboardPage already has. Pagination (limit/offset) is worth adding but is secondary once history stops riding along — the per-test summary is small.

**Risk:** Some list consumers may read live_metrics for status badges (DashboardPage tables); keep live_metrics top-level scalars or gate stripping behind a query param and migrate callers incrementally.

### 26. Every result's full response body is retained and scanned by 6 redaction regexes, even on success

- Location: `pkg/engine/redaction.go:235`
- Category: cpu
- Finder: go-crosscutting
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** vuser_execute.go:26 calls scrubSensitiveResult on every recorded result. scrubSensitiveTextWithConfig runs 5 heavyweight alternation regexes plus the high-entropy token ReplaceAllStringFunc over result.ResponseBody — which pkg/protocol/http/http.go:427 unconditionally populates with the entire response body (up to 64MiB cap) on every request. On the load path ResponseBody is only ever consumed by the optional data_returned extended-log option (vuser_resolve.go:136); archives and the distributed transaction buffer never persist it, so on the default success path this multi-regex scan of the full body is pure wasted CPU.

**Evidence:** redacted := value
for _, pattern := range payloadRedactionPatterns {
    redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
}
...
redacted = highEntropyTokenPattern.ReplaceAllStringFunc(redacted, ...)

**Hot path:** Runs once per action result per VUser iteration on the generator/engine hot path — thousands of results/sec, each scanning the full response body (KBs-100s of KBs) with ~6 regexes; regex NFA throughput makes this one of the largest per-request CPU costs.

**Verification notes:** Verified the full chain: http.go:429 unconditionally retains the decoded body (64MiB cap at http.go:523), vuser_execute.go:26 scrubs every result, and redaction.go:112/235-248 runs 5 alternation regexes + entropy-gated ReplaceAllStringFunc over it; Go's ReplaceAllString also full-copies the input even with zero matches, so it's ~6 scans plus ~6 body-sized allocations per request. I traced every post-adapter consumer of top-level Result.ResponseBody: both archive writers, the distributed transactionBuffer, liveErrorEvidenceSink, and MetricsCollector copy explicit fields and never persist it — the only reader is the data_returned extended-log attr (vuser_resolve.go:136), which fires after recordResult. On the default success path the scrub is pure waste on the per-request hot path.

**Refined fix:** Gate at the engine, not the adapter — adapters consume ResponseBody internally (extractors, assertions, BuildProtocolEvidence, java_http response mapping) before returning. In VUser.recordResult, precompute once per VUser: needsBody = logCfg has the "data_returned" extended option AND (Level=="extended" OR OnErrorOnly). If !needsBody, set result.ResponseBody = "" before scrubSensitiveResult — dropping is strictly stronger than redacting for data that never leaves the process. Leave scrubSensitiveResult itself unchanged so ErrorMessage, Snapshot (failure evidence), ProtocolEvidence, and browser fields stay scrubbed on every retained/persisted surface; the extended-log path is behavior-identical since actionLogAttrs runs after recordResult and still sees the scrubbed body. Optionally also truncate the data_returned log attribute to a fixed cap (e.g. 64KB) so even the enabled path is bounded. Add a test asserting ResponseBody is empty on recorded results when data_returned is off, and scrubbed-but-present when on.

**Risk:** Redaction is a security control (prior secrets-in-evidence P0). The fix must guarantee every persisted/exposed surface is still scrubbed — gate the retention, not the scrubbing of retained data. Extended-logging behavior must be unchanged when enabled.

### 27. Run-calendar handler loads and JSON-parses every archived run's manifest per request (5s dashboard poll), and can trigger full object-store archive downloads

- Location: `pkg/api/run_calendar.go:289`
- Category: io
- Finder: background-loops
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** BuildRunCalendar calls tr.manifestForCalendarTest(test) for every occurrence (line 279) and every test (line 289) before the from/to overlap filter runs (appendEvent filters after the event — including its topology — is fully built). manifestForCalendarTest -> GetRunArchiveManifest (tests.go:411) does os.ReadFile + json.Unmarshal of the archive manifest for each archived test, and first calls ensureArchiveMaterialized (archive_backend.go:240), which on a non-filesystem backend with a cold local cache downloads the manifest AND every artifact (parquet, HAR, CSVs) of the run. The manifest is only used to render a topology summary for out-of-window and never-rendered events alike.

**Evidence:** appendEvent(runCalendarEventFromTest(test, snapshot.Scenarios, inventory, tr.manifestForCalendarTest(test), now))  // and line 279: ...tr.manifestForCalendarTest(test)... per occurrence; manifestForCalendarTest -> GetRunArchiveManifest -> ensureArchiveMaterialized + loadRunArchiveManifest (ReadFile + json.Unmarshal)

**Hot path:** MonitoringPage.tsx polls api.getRunCalendar every 5 seconds per open dashboard tab (setInterval(refresh, 5000) at MonitoringPage.tsx:458). Archived runs and occurrences grow without bound, so each poll performs O(total archived runs + total occurrences) manifest file reads + JSON parses; with an S3/GCS archive backend and a fresh controller replica, one poll can materialize every archive to local disk (potentially gigabytes).

**Verification notes:** Confirmed: run_calendar.go:279/289 load+parse every archived test's manifest (os.ReadFile + json.Unmarshal of a Provenance/settings-laden document, no cache anywhere — tr.archives caches only the record struct) before the [from,to) overlap filter and maxEvents cap run, and MonitoringPage.tsx:458 polls getRunCalendar every 5s per visible tab, so cost is O(all retained runs + occurrences) per poll and grows unboundedly. However the "one poll downloads gigabytes" sub-claim is overstated: boot restore (persistence.go:155→loadStoredTestArchive:310→runArchiveRecordAvailable→ensureArchiveMaterialized) materializes every archive at startup, and manifestForCalendarTest short-circuits on test.Archive==nil, so calendar-triggered full downloads only happen if the local copy vanishes post-boot or for runs archived after boot — hence high, not critical.

**Refined fix:** Cache the extracted topology, don't just defer it: parse the manifest once and store the small RunCalendarTopology/Provenance slice alongside the record in tr.archives at hydration time (boot loadStoredTestArchive, archive creation testexecution.go:1051, notes re-upload test_notes.go:144 — all natural invalidation points since they already replace the record). Also move manifest resolution after the appendEvent overlap/cap check as suggested, but note lazy-only is insufficient: maxEvents=2000 in-window events could still mean 2000 parses per 5s poll without the cache. Optionally drop archive topology from the list payload entirely and serve it from a per-event detail endpoint on click.

**Risk:** Calendar event topology for archived runs would be filled lazily; a cache must be invalidated when an archive is re-uploaded or pruned, or the side panel could show stale topology.

### 28. Fresh JVM process forked for every java_vuser action (full boot + classload per iteration)

- Location: `pkg/protocol/java_vuser/java_vuser.go:170`
- Category: cpu
- Finder: sup:java-vuser-process-per-action
- Verified impact: high
- Confidence: high
- Effort: L

**Description:** Execute() builds and forks a brand-new OS process via exec.CommandContext for every single java_vuser_run action, with the default RunnerCommand being 'java' (runner_env.go:39). Each action therefore pays full JVM boot (~100-800ms), full classpath scan + Class.forName of the user's Actions class, and — in the default 'full_lifecycle' mode (Harness.java:36-39) — re-runs init() and end() on every iteration even though LR semantics intend init once per VUser. Harness.main() already reads exactly one JSON command line from stdin (Harness.java:134-138) and exits, so the wire protocol is one process per action by construction. No process pool, no persistent sidecar, no classloader reuse exists anywhere in the package (java_vuser.go is the entire implementation).

**Evidence:** cmd := exec.CommandContext(runCtx, j.cfg.RunnerCommand, args...)
...
err = startRunnerCommand(ctx, cmd, j.cfg.StartupTimeout)
if err == nil {
    err = cmd.Wait()
}

**Hot path:** Runs once per action per iteration per VUser (vuser.go:445 -> vuser_execute.go:206 -> Execute). With thousands of configured Java VUsers each iterating continuously, every iteration burns ~0.5-1 CPU-core-seconds of pure JVM startup; at the 25-slot default that is up to ~25-50 cores of the generator spent on JVM boot alone, starving co-located protocol VUsers on the same pod.

**Verification notes:** Every citation verified: exec.CommandContext at java_vuser.go:170 forks a fresh `java` process per action (runner_env.go:39), Harness.main reads exactly one stdin command line (Harness.java:134-138) and exits, the importer hardcodes method=full_lifecycle (java_import.go:472) so init()/end() re-run every iteration, and no pool/reuse exists in the package; hot path pkg/engine/vuser.go:445 → vuser_execute.go:206 → Execute confirmed per-iteration-per-VUser. Downgraded from critical to high because the lane is operator-gated opt-in (APEXLOAD_JAVA_VUSER_RUNNER_ENABLED + entitlement + launch-blocked import mode), is the fallback path for non-translatable Java (first-class importer emits native HTTP/JDBC/SOAP actions), and the 25-slot processSlots cap bounds CPU to ~25 concurrent JVM boots — the failure mode is a ~25-50 actions/s throughput ceiling plus generator saturation, not an unbounded fork storm. Additional confirmed defect: result.Duration includes slot-queue wait + JVM boot and feeds transaction metrics, so reported Java-action response times are inflated by ~0.5s+ of harness overhead on a performance-measurement product.

**Refined fix:** Stage the fix instead of jumping straight to a pooled sidecar. (1) Zero-risk quick wins: add JVM startup flags (-XX:TieredStopAtLevel=1 and an AppCDS/-Xshare archive of harness+lrapi classes) to cut boot ~50%, and fix measurement fidelity now — use the harness-reported duration_ms as the action Duration and record slot-wait/JVM-boot as separate ProtocolEvidence fields so transaction metrics stop including harness overhead. (2) Lifecycle split with the existing process model: the harness already supports method=init/action/end; invoke method=init once from the protocol's per-VUser Init(), method=action per iteration, method=end from Teardown(), restoring LR init-once semantics without changing isolation. (3) Persistent warm-JVM pool (the original suggestion): loop Harness.main over stdin lines, pool keyed by classpath+source_archive_ref, explicit reset of lrapi statics (parameters/timers/transactionStatus, confirmed static ConcurrentHashMaps at lr.java:23-25) between requests, health-check + respawn on crash/hang, and keep fork-per-action as the default 'governed isolation' mode with the pool as operator opt-in (APEXLOAD_JAVA_VUSER_RUNNER_MODE=pooled) — this matches the existing gating philosophy and the importer's own 'governed runner pool' language.

**Risk:** Persistent JVMs change isolation semantics: user script static state and lrapi static maps leak across iterations unless explicitly reset; a crashed/hung JVM must be detected and replaced (health-check + respawn); memory of long-lived user code accumulates. Keep fork-per-action as a fallback mode.

### 29. MaxProcesses slot cap (default 25) silently throttles offered load and queue wait is misreported as SUT response time

- Location: `pkg/protocol/java_vuser/java_vuser.go:148`
- Category: concurrency
- Finder: sup:java-vuser-process-per-action
- Verified impact: high
- Confidence: high
- Effort: S

**Description:** acquireProcessSlot blocks on a generator-wide channel sized from APEXLOAD_JAVA_VUSER_MAX_PROCESSES (default 25, runner_env.go:43). Combined with the process-per-action model, max java_vuser action rate is ~MaxProcesses / avg-process-lifetime — at ~500ms JVM boot that is ~2N/sec, i.e. ~50 actions/sec per generator regardless of whether 100 or 10,000 VUsers are configured (same offered-load under-delivery class as the confirmed TokenBucket finding). Worse, result timing starts at line 127 (start := time.Now()) BEFORE the slot acquire at line 148, and finalizeResult sets result.Duration = time.Since(start) (line 282), so slot-queue wait is folded into the reported action duration that feeds transaction metrics (generatorrpc/service.go:324). Under saturation the platform reports multi-second 'response times' that are actually harness queuing, blaming the system under test, and no evidence key or warning distinguishes queue wait from execution.

**Evidence:** start := time.Now()          // line 127, before slot acquire
...
release, err := acquireProcessSlot(ctx)   // line 148, unbounded wait
...
result.Duration = time.Since(start)       // finalizeResult, line 282

**Hot path:** Every java_vuser action on every generator passes through this single channel; at thousands of VUsers virtually all of them sit blocked here, and every recorded metric for the protocol is skewed by the wait.

**Verification notes:** Verified all three cited lines: start:=time.Now() (java_vuser.go:127) precedes the unbounded acquireProcessSlot wait (line 148) on a package-level channel sized 25 by default (runner_env.go:43, shared across all tests on the generator), and finalizeResult sets Duration=time.Since(start) (line 282), which flows verbatim into TransactionMetric.DurationMs at pkg/distributed/generatorrpc/service.go:324 and engine metrics (vuser_execute.go:27/30). No evidence key, log, or preflight check surfaces slot-queue wait — launch preflight only gates runner enablement — so under saturation the platform silently reports harness queuing as SUT response time while capping offered load at ~MaxProcesses/process-lifetime per generator (fresh JVM per action, exec.CommandContext line 170, no pooling). Scoped to an opt-in protocol, which is the only thing keeping it below critical.

**Refined fix:** After acquireProcessSlot returns, capture queueWait := time.Since(start) and re-base BOTH the duration clock and result.StartTime to post-acquire (StartTime feeds TPS windowing via resultStartTimeUnixMs at service.go:322, so leaving it pre-queue smears throughput windows). Record runner_slot_wait_ms and the original total wall time (e.g. runner_total_wall_ms) in ProtocolEvidence to preserve trend continuity. Emit a saturation warning event when queueWait exceeds a threshold (e.g. >1s or >25% of action duration). Additionally add a launch-preflight advisory (alongside the existing java_vuser_runner_required check) when planned concurrent java_vuser VUsers per generator exceed MaxProcesses, so the cap is visible before the run rather than discovered via skewed metrics. Do not simply raise the default cap — 25 concurrent JVMs is legitimate resource governance; the fix is attribution and visibility.

**Risk:** Changing what Duration means alters historical trend comparability for existing java_vuser runs; keep the total wall time available as a separate extraction to preserve continuity.

### 30. Every 2s poll re-serializes ALL recording sessions with full request/response bodies

- Location: `pkg/api/handlers_recorder.go:113`
- Category: network
- Finder: sup:recorder-session-payload-and-poll
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** handleListRecordings calls recorder.ListSessions() and writeJSONs the result. recorder.Session serializes Requests []RecordedRequest with `json:"requests"`, and each RecordedRequest carries Body and ResponseBody strings of up to 512KiB each (defaultBodyCaptureLimitBytes, recorder.go:28). handleGetRecording (line 101-109) does the same for the active session. There is no summary view, pagination, or since-seq incremental fetch, so the entire retained recording corpus (all stopped sessions plus the live one) is JSON-marshaled and shipped on every request. Additionally, each snapshot() copy of an N-request session is taken while holding s.mu (recorder.go:343-368), contending with the proxy's recordRequestWithOptions on every poll.

**Evidence:** sessions := s.recorder.ListSessions()
...
writeJSON(w, http.StatusOK, map[string]any{
    "sessions": sessions,           // full Session incl. Requests[].Body/ResponseBody (≤512KiB each)
    "count":    len(sessions),
    "active":   s.recorder.ActiveSession(),
})

**Hot path:** RecordingStudio.tsx polls api.listRecordings + api.getRecording(active.id) on a 2-second setInterval while the studio page is open. A 30-minute recording of a busy SPA (hundreds of XHRs with captured bodies) plus a few stopped sessions means hundreds of MB of JSON marshal + network transfer + browser JSON.parse every 2 seconds, continuously, while a live recording is also writing.

**Verification notes:** Confirmed: handleListRecordings (pkg/api/handlers_recorder.go:111-128) JSON-marshals every session's full requests[] with Body/ResponseBody (≤512KiB each, base64-inflated for binary), and its "active" field is a second full ActiveSession() snapshot — combined with getRecording in the same tick, the active session is serialized 3x per poll; RecordingStudio.tsx:659-663 polls on a 2s setInterval and its live feed renders only the last 20 requests' method/path/status, so the bodies are waste. No summary view, pagination, since_seq, or per-session request cap exists. One overstatement: snapshot() (recorder.go:343-368) aliases body strings (struct-header copy only), so s.mu hold time is µs-ms and marshal happens outside the lock — proxy contention is minor; the marshal/transfer/JSON.parse cost is the real issue. Impact is high not critical: severe, growing-with-recording-length degradation (server marshal churn + browser main-thread parse jank/tab OOM on long body-heavy recordings) but confined to the open studio page, typically single-user, and never touches load generation.

**Refined fix:** Original fix is sound; refine in four parts. (1) List endpoint returns summaries only (id, name, project_id, status, request_count, base_url, started_at/stopped_at) and its "active" field should reuse the summary shape handleRecorderStatus already builds (the studio calls getRecorderStatus in the same Promise.all, so full-active there is redundant). (2) GET /recorder/{id}: add ?bodies=false (sufficient for the 2s live feed, which shows only method/path/status/time) and ?since_seq=N for incremental appends. (3) Add GET /recorder/{id}/requests/{seq} for lazy full-body fetch when a request is opened in the detail editor — a natural sibling of the existing PUT-style handleUpdateRecordedRequest route. (4) Dashboard: switch selectedSession detail table (RecordingStudio.tsx:1756) to fetch on selection, and feed the search index (line 99) from body-less request summaries or drop per-request search from the list view. Do NOT change snapshot()/locking — string aliasing already makes the copy cheap. HAR export/conversion are server-side via GetSession and unaffected.

**Risk:** The dashboard's stopped-session detail table reads sessions[].requests from the list payload, so it must be switched to fetch detail on selection; HAR export and conversion are server-side and unaffected.

### 31. RecordingStudio 2s polling loop has no in-flight guard, no incremental cursor, and replaces all state identities each tick

- Location: `dashboard/src/appRouting.ts:1`
- Category: polling
- Finder: sup:recorder-session-payload-and-poll
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** refresh() (RecordingStudio.tsx:613-657) awaits three endpoints plus the full active-session fetch and then calls setSessions/setActiveSession/setBrowserSessions with freshly parsed objects every tick, re-rendering the entire 1950-line component even when nothing changed. Unlike BrowserRecorderControlPanel (which has refreshInFlightRef), this interval has no overlap guard: when a response takes >2s — guaranteed once payloads reach tens of MB — requests pile up concurrently, multiplying backend serialization load and frontend parse/GC pressure.

**Evidence:** useEffect(() => {
  refresh()
  const interval = setInterval(refresh, 2000)
  return () => clearInterval(interval)
}, [refresh])
...
if (recorderStatus.active) {
  const full = await api.getRecording(recorderStatus.active.id)
  setActiveSession(full)
}

**Hot path:** Runs for the whole time the Recording Studio page is open, including during a live recording when the backend is proxying traffic; each tick re-downloads and re-parses the full session corpus and forces a full component re-render (live feed also copies+reverses the whole requests array per render at line 1628).

**Verification notes:** Verified every claim in source: setInterval(refresh, 2000) at RecordingStudio.tsx:659-663 has no in-flight guard (BrowserRecorderControlPanel.tsx:144-162 has the refreshInFlightRef pattern), refresh() re-fetches and replaces all state identities each tick, and line 1628 copies+reverses the full requests array per render. The finding actually understates it: handleListRecordings→ListSessions() (pkg/recorder/recorder.go:380-392) deep-copies and serializes ALL sessions' full request corpora (bodies capped at 512KB each, recorder.go:28) every 2s under the same session mutex the live proxy uses, and request_recorded WS events are already broadcast server-side (pkg/api/handlers_recorder.go:36-43) but unconsumed. Overlapping >2s responses create a self-amplifying degradation loop during live recording, so high impact is justified.

**Refined fix:** Original fix is sound; refine in priority order: (1) land the 3-line in-flight ref guard immediately (copy BrowserRecorderControlPanel.tsx:144-162 pattern). (2) No new cursor endpoint needed — the backend already broadcasts request_recorded/request_updated/recording_paused/recording_resumed with full RecordedRequest payloads via the WS hub (handlers_recorder.go:36-43, recorder.go:944-948); subscribe and append/patch incrementally, keeping a slow (15-30s) full-refresh reconcile as fallback for missed events and session restart/deletion. (3) Also fix the list side: handleListRecordings serializes every session's full request corpus each tick — add a summary projection (id/name/status/request_count/started_at) since the UI list renders only metadata; this is the larger byte-count offender when multiple past sessions are in memory. (4) Skip setActiveSession when status and requests.length are unchanged to preserve identity. (5) Replace line 1628 with activeSession.requests.slice(-20).reverse() (or useMemo) to avoid copying the whole array per render.

**Risk:** Cursor-based merging must handle session restart/deletion and request updates (excluded/step_name edits) or the feed can show stale rows; the correct anchor file is dashboard/src/components/RecordingStudio.tsx:661.

### 32. Proxy recording sessions with full bodies are retained in controller RAM forever; per-session request count is unbounded

- Location: `pkg/recorder/recorder.go:206`
- Category: memory
- Finder: sup:recorder-session-payload-and-poll
- Verified impact: high
- Confidence: high
- Effort: M

**Description:** Sessions are stored in the in-memory r.sessions map and the only removal path is an explicit DeleteSession (line 394). Stopped sessions keep their entire Requests slice — each entry holding up to 512KiB request body + 512KiB response body plus 4 header maps — until the controller restarts. There is no retention janitor (BrowserRecordingManager has a 24h PruneExpired janitor; the proxy Recorder has none), no cap on sessions, and recordRequestWithOptions appends to s.Requests (line 941) with no ceiling on request count for a session left recording.

**Evidence:** r.sessions[id] = s
...
func (r *Recorder) DeleteSession(id string) bool { ... delete(r.sessions, id) ... }  // only removal path
...
s.Requests = append(s.Requests, rec)  // unbounded

**Hot path:** Grows with every recorded request and every recording session over the controller's lifetime. A recording accidentally left running against a polling SPA, or a few weeks of accumulated stopped recordings, drives controller RSS into multiple GB — memory shared with the test-orchestration process — and makes the 2s list-poll payload (finding 1) grow without bound.

**Verification notes:** All three cited lines match exactly (sessions map insert at recorder.go:206, sole delete path at 394/404, uncapped append at 941); grep confirms zero prune/janitor/cap logic in pkg/recorder, while BrowserRecordingManager, ScriptDebugManager, and BrowserReplayManager all run PruneExpired janitors — the asymmetry is real. Conversion and stop handlers (pkg/api/handlers_recorder.go) never delete or spill sessions, so stopped sessions with full 512KiB-capped bodies live in controller RAM (recorder created in pkg/api/server.go:183) until explicit DELETE or restart, and ListSessions deep-copies every body on each poll. One understatement in the finding: only one session can record at a time (recorder.go:186), which bounds growth rate but not retention or the single-runaway-session case.

**Refined fix:** Three layers, in priority order: (1) stopped-session TTL janitor mirroring BrowserRecordingManager.PruneExpired (browser_recording_manager.go:375) with a 24h default, exposing expires_at in the list/get payloads so the UI can warn before eviction; (2) a per-session cumulative captured-bytes budget (not just request count — request count alone doesn't bound memory since bodies vary 0-512KiB) plus a max-request cap; on breach, auto-stop the session via the existing StopSession path and emit a SessionEvent (e.g. "recording_limit_reached") through the existing notify hub so the dashboard surfaces it — never silently drop; (3) make handleListRecordings return summaries (id/name/status/request_count/bytes) instead of full snapshot() copies, which also fixes the poll-payload amplification (finding 1) and removes the per-2s deep copy of all bodies. Disk spill is optional follow-up; TTL+caps+summary-list deliver most of the win at low risk since convert-to-script and HAR export already persist what users want to keep.

**Risk:** Users may rely on stopped recordings surviving until manually deleted; TTL eviction is silent data loss unless surfaced in the UI, and conversion/HAR export must handle evicted sessions gracefully.

### 33. No GOMEMLIMIT anywhere: generator/controller GC runs blind to the cgroup memory limit, so large runs get OOM-killed instead of GC-backpressured

- Location: `deployments/k8s/onprem/generator-statefulset.yaml:102`
- Category: memory
- Finder: sup:go-runtime-k8s-sizing
- Verified impact: high
- Confidence: high
- Effort: S

**Description:** Every deployment overlay sets hard memory limits (generator 8Gi onprem/aks at generator-statefulset.yaml:102 and aks:126, 2Gi local:64; controller 4Gi at controller-deployment.yaml:102) but no manifest, entrypoint, or Dockerfile sets GOMEMLIMIT or calls debug.SetMemoryLimit — grep across pkg/, cmd/, deployments/, and Dockerfile returns zero hits. With the default GOMEMLIMIT=MaxInt64 and GOGC=100, peak RSS reaches ~2x live heap plus goroutine stacks, so a generator whose live heap grows to ~4Gi (thousands of VUsers each holding response-capture buffers capped at 512KiB, 8MiB Java streams, per-second metric batches, and in-memory archiveRecorders per test — see NewService in pkg/distributed/generatorrpc/service.go:438-451) crosses the 8Gi cgroup limit and is SIGKILLed by the kernel mid-test, losing or distorting the entire run and churning the StatefulSet/KEDA. The controller has the same exposure at 4Gi during archive/report generation of large runs. Note the CPU side needs NO fix: go.mod declares go 1.26.4 and the image builds with golang:1.26.4, so Go's container-aware GOMAXPROCS (default since Go 1.25) already caps GOMAXPROCS at the cgroup CPU quota — the originally suspected GOMAXPROCS/CFS-throttling mismatch does not exist in this build; only the memory analog is missing, because Go has no automatic GOMEMLIMIT-from-cgroup default. Ironically the repo already ships the exact detection code needed: detectMemoryLimitBytes() at pkg/distributed/generatorrpc/pod_resources.go:40-52 reads /sys/fs/cgroup/memory.max (v2) and memory.limit_in_bytes (v1) — but only to report capacity to the controller, never to configure the GC.

**Evidence:** resources:
  requests:
    cpu: "1"
    memory: 2Gi
  limits:
    cpu: "4"
    memory: 8Gi
# env block (lines 58-75) has no GOMEMLIMIT; cmd/generator/main.go:22-32 does no runtime setup;
# pkg/distributed/generatorrpc/pod_resources.go:40 detectMemoryLimitBytes() exists but is never fed to debug.SetMemoryLimit

**Hot path:** The generator heap is the core product path: thousands of concurrent VUsers buffering captures/bodies for hours per run, plus per-test archive recorders and 1s-cadence metric batches. An OOM kill here doesn't degrade performance — it destroys the load test being measured. The controller path (archive writes, report generation, metrics fan-in from many generators) has the same exposure at its 4Gi limit.

**Verification notes:** Grep across all .go/.yaml/Dockerfile/.mod files returns zero hits for GOMEMLIMIT, SetMemoryLimit, GOGC, or automaxprocs — the runtime is fully blind to cgroup memory. Every cited anchor checks out: 8Gi generator limit in deployments/k8s/onprem/generator-statefulset.yaml (resources block ~line 99-103, env block lines 58-75 has no GOMEMLIMIT but does set APEXLOAD_GENERATOR_MAX_VUSERS=10000, confirming the thousands-of-VUsers scale), 4Gi controller limit at controller-deployment.yaml:102, cmd/generator/main.go does no runtime setup, go.mod declares go 1.26.4 (so the finding's CPU-side carve-out is correct), and detectMemoryLimitBytes() at pkg/distributed/generatorrpc/pod_resources.go:40 reads cgroup v1/v2 limits but flows only into podResources capacity reporting (pod_resources.go:19 → Pong → controller inventory), never into the GC. No mitigation exists: runtime.MemStats is read only for telemetry (pkg/engine/sys_metrics.go), and NewService at service.go:430-458 confirms per-test in-memory archiveWriters/archiveRecords plus 512KiB body-capture buffers (pkg/protocol/browser/runtime_context.go:15) as real heap-heavy state. The failure mode — kernel SIGKILL of a generator mid-run instead of GC backpressure — destroys the test being measured, on a product whose entire job is long large runs; prior memory-leak audits in this repo show generator memory pressure is a real, recurring concern, so 'high' impact is fair (conditional on live heap approaching ~half the limit, but that is exactly the large-run regime the product targets).

**Refined fix:** Original fix is correct and safe; two refinements. (1) The existing helper is directly reusable with no hardening needed: parseMemoryLimit at pod_resources.go:90-103 already rejects cgroup-v2 "max", non-positive values, and the cgroup-v1 unlimited sentinel (>1<<50), so exporting detectMemoryLimitBytes and calling `if os.Getenv("GOMEMLIMIT") == "" { if lim := generatorrpc.DetectMemoryLimitBytes(); lim > 0 { debug.SetMemoryLimit(lim - lim/10) } }` early in cmd/generator/main.go and cmd/controller/main.go is safe as written (the Go runtime consumes an explicit GOMEMLIMIT env itself, so the empty-check correctly defers to operator overrides). Consider moving the helper to a small shared package (e.g. pkg/config or pkg/debugserver-adjacent) rather than importing generatorrpc from the controller binary just for this. (2) Keep GOGC at default; do NOT also set GOMEMLIMIT via manifest resourceFieldRef once the in-code path exists — pick one mechanism so overlays (onprem/aks/local at 8Gi/8Gi/2Gi) never drift. For browser-VUser generator images, use a lower Go share (~60-70% of the limit) since Chromium subprocess RSS counts against the same cgroup but is invisible to Go's GC — a 90% GOMEMLIMIT there still allows kernel OOM. Log the computed limit at startup so operators can see it in inventory next to the already-reported MemoryLimitBytes.

**Risk:** A GOMEMLIMIT set too close to actual live-heap demand causes GC death-spiral (runtime spends most CPU in GC instead of OOMing); use ~90% headroom and keep GOGC at default. Browser-VUser pods need a lower Go share of the limit or Chromium can still push the cgroup over the top. No behavior change when the env var is explicitly set or no cgroup limit exists (detect returns 0).

### 34. CEL condition evaluation rebuilds the full vars/data activation maps and re-resolves every parameter per evaluation

- Location: `pkg/engine/condition_expr.go:51`
- Category: allocations
- Finder: engine-vuser
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** evalConditionExpr builds a fresh activation on every call: conditionExprVarMap iterates the entire vu.Variables map (which grows with extractions, data columns and foreach bindings), allocates nested maps for every dotted key, and calls Parameters.ResolveNamed for every script parameter; conditionExprDataMap re-iterates vu.Variables a second time; conditionExprEnvMap rebuilds the env map. ResolveNamed on each_occurrence parameters also generates (and advances) a fresh value purely to populate the activation.

**Evidence:** func (vu *VUser) conditionExprActivation() map[string]any {
	return map[string]any{
		"vars": vu.conditionExprVarMap(),
		"data": vu.conditionExprDataMap(),
		"env":  conditionExprEnvMap(),
	}
}

**Hot path:** Condition-driven control_loops evaluate the condition once per pass (default cap 1000 passes) per iteration per VUser (vuser.go:366), and control_if per occurrence. A script with 50 variables + 20 parameters pays O(V+P) map building thousands of times per second per VUser.

**Verification notes:** Every claimed mechanism checks out in condition_expr.go: line 51 matches exactly; conditionExprVarMap iterates all vu.Variables plus calls ResolveNamed for every script parameter, conditionExprDataMap re-iterates vu.Variables, and conditionExprEnvMap -> scriptVisibleEnvMap (script_env.go:66) calls os.Environ() and rebuilds the filtered map plus re-parses the allowlist on EVERY eval — the heaviest constant cost, missed by the finding. Only the CEL program is cached (sync.Map, line 12); the activation has zero mitigation, and callers confirm per-pass hotness (vuser.go:366/655 condition loops capped at 1000 passes, :328/619 per foreach row, :274/575 per control_if) with importers (e.g. trueclient/control_flow.go:204) emitting Expr conditions so real imported scripts hit this. The finding actually understates one aspect: ResolveNamed on each_occurrence params calls generate() directly (parameters.go:128-130), which advances state.nextIndex/state.used/state.counter (parameters.go:327, 359-412) — so merely evaluating a condition consumes unique-pool values, skips data rows, and increments counters for parameters the expression never references; that is a correctness hazard (premature unique exhaustion, skipped test data), not just allocations. Severity capped at medium because fixed-count loops skip condition eval (vuser.go:357), each build is single-digit-to-tens of microseconds, and loop bodies doing HTTP I/O dwarf it — but on a load generator where per-VUser goroutine GC pressure directly bounds achievable load, condition-heavy scripts pay a real tax.

**Refined fix:** Three-part fix, ordered by win/risk: (1) Cache the env map — os.Environ() plus allowlist parsing is process-immutable during a run; compute once via sync.Once (or invalidate per test run) in scriptVisibleEnvMap. Trivial, safe, and removes the largest constant cost. (2) At compile time in conditionExprProgram, extract the referenced root identifiers and vars.<name> fields from the checked AST and cache them alongside the program (extend the sync.Map value to a small struct); at eval time build only the referenced vars/data entries and call ResolveNamed only for parameters the expression actually references. This is simpler and lower-risk than a full custom interpreter.Activation with lazy traits.Mapper (nested field traversal makes lazy mappers fiddly) while preserving the dotted-key-wins determinism rule verbatim. (3) Fix the each_occurrence side effect independently as a correctness bug: ResolveNamed used for activation population must not consume values — add a peek/read-only resolution mode (return cached or a non-advancing preview) so condition evaluation never advances sequential cursors, unique pools, or counters. Item 3 deserves its own test: a condition loop alongside an each_occurrence unique parameter must not exhaust the pool.

**Risk:** CEL semantics for nested var.row.email traversal must be preserved (the deterministic dotted-key-wins rule); needs the control-flow and condition tests to stay green.

### 35. Random template builtins allocate and seed a fresh math/rand generator per occurrence in load runs

- Location: `pkg/engine/parameters.go:552`
- Category: allocations
- Finder: engine-vuser
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** builtinRand returns rand.New(rand.NewSource(now().UnixNano())) whenever vu.rng is nil — which is every load run. Every {{fn.random(...)}}, {{fn.random_string(...)}} and {{fake.email}} occurrence therefore allocates a ~5 KB rngSource and pays the full 607-word seeding loop, per placeholder per action per iteration. Two occurrences resolved in the same nanosecond also produce identical values (correlated 'random' data).

**Evidence:** func builtinRand(rng *rand.Rand, now func() time.Time) *rand.Rand {
	if rng != nil {
		return rng
	}
	return rand.New(rand.NewSource(now().UnixNano()))
}

**Hot path:** Imported scripts lean heavily on random builtins for parameterization; at thousands of VUsers x multiple placeholders per action this is a steady multi-MB/s allocation stream plus expensive reseeding on the per-action resolve path (vuser_resolve.go:449).

**Verification notes:** Verified code at pkg/engine/parameters.go:548-553 matches the claim exactly; vu.rng is set only in replay.go:328 and vuser.go:44 documents it is nil in load runs, so every fn.random/fn.random_string/fake.email occurrence allocates a ~4.9KB math/rand rngSource and pays the ~627-step seeding loop per placeholder per action (sole caller vuser_resolve.go:449, inside per-action interpolation). Importers emit these builtins (pkg/convert/tmpl/functions.go:66-105, Postman converter), no caching exists, and the same-nanosecond correlation claim is mechanically true. The repo already uses the proposed pattern elsewhere: DataProvider.randIntn (data.go:328) uses replay-seeded rng vs global rand.Intn on the load path, and go.mod is Go 1.26.4 with no rand.Seed calls, so global rand is lock-free.

**Refined fix:** Add an unexported lazily-initialized per-VUser fallback generator (e.g. vu.loadRNG, alongside the existing replay-only vu.rng) and pass it from resolveTemplateValue so builtinRand signatures stay *rand.Rand. VUsers are single-goroutine (documented on adjacent fields in vuser.go), so no locking. One correction to the original fix: do NOT seed each VUser with bare time.Now().UnixNano() — VUsers spawned in the same nanosecond would share identical sequences, recreating the correlation bug across VUsers. Seed with UnixNano() ^ (int64(vu.ID) << 32), or draw the seed from the global rand (rand.Int63()), which is lock-free in Go 1.20+ since rand.Seed is never called. Keep the vu.rng replay override untouched; add/adjust a test asserting deterministic replay still passes and that two placeholders in one action yield different values.

**Risk:** Very low; replay determinism already flows through vu.rng and is untouched. Value sequences change (they were never guaranteed).

### 36. TokenBucket and arrival scheduler lose ticks at high TPS, silently under-delivering offered load

- Location: `pkg/engine/pacing.go:222`
- Category: concurrency
- Finder: engine-vuser
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** TokenBucket.fill adds exactly one token per ticker tick, and time.Ticker (buffer 1) drops ticks when the runtime timer fires late or the goroutine is descheduled. At tps=10000 the interval is 100us — well below reliable Go timer granularity under load — so the bucket refills at whatever rate the scheduler delivers ticks, capping actual iteration starts below the configured TPS with no error. runArrivalScheduler (open_workload.go:140-149) has the same one-arrival-per-tick coalescing for open workloads.

**Evidence:** case <-ticker.C:
	select {
	case tb.tokens <- struct{}{}:
	default:
	}

**Hot path:** Every tps-paced closed workload and every open workload runs through these tickers for the whole test; the busier the generator (thousands of VUsers competing for the scheduler), the more ticks coalesce — so the throughput shortfall grows exactly when the test matters, and shows up as a bogus 'SUT missed target TPS' gap.

**Verification notes:** Confirmed at pkg/engine/pacing.go:214-228 and open_workload.go:132-161: both mint exactly one token/arrival per delivered ticker tick with no elapsed-time accounting, and time.Ticker (1-buffered channel, documented drop-on-slow-receiver) permanently loses ticks — so actual offered rate is capped at delivered-tick rate, not configured TPS. NewTokenBucket clamps interval to 1ns for large tps (TestNewTokenBucket_LargeTPS exercises 1e9-1e15), guaranteeing severe silent under-delivery at high rates plus a busy-looping fill goroutine; the arrival scheduler additionally runs dispatch (tryAcquire, wg.Add, metrics atomic, goroutine spawn, waitIfPaused) synchronously between ticks, widening the drop window. No mitigation exists: bucket capacity only stores minted tokens, and the shed-arrival warning covers SUT slowness, not ticker under-delivery, so the shortfall appears in the target-vs-achieved TPS gap mis-attributed to the SUT. Effect is small below ~1k tps/generator but systematic above, and offered-load fidelity is the product's core function.

**Refined fix:** Replace per-tick minting with elapsed-time accounting. TokenBucket: use a coarse ticker at max(1s/tps, ~5ms); on each tick compute owed = int(float64(now.Sub(lastMint)) * tps / float64(time.Second)), mint min(owed, free capacity) tokens, then advance lastMint by the minted amount's worth of intervals — but when the bucket is full, set lastMint = now so idle time cannot bank unbounded burst credit beyond capacity. (Equivalently, swap the whole type for golang.org/x/time/rate.Limiter with burst = existing capacity and Wait(ctx); that also deletes the fill goroutine and fixes the 1ns busy-loop ticker for extreme tps.) runArrivalScheduler: coarse ticker at max(interval, ~2-10ms), dispatch elapsed/interval arrivals per tick with a capped per-tick batch (e.g. one tick-window's worth) to preserve arrival smoothness — and critically, reset the elapsed baseline after waitIfPaused returns, otherwise paused wall-clock time converts into a huge arrival burst on resume (a detail the original fix omits). Add a metrics counter for ticks/tokens owed-vs-minted so pacing shortfall becomes observable instead of silent.

**Risk:** Burstier token release within a tick window (bounded by existing bucket capacity); arrival batching slightly clusters open-model arrivals — keep batch windows small (<=10ms) to preserve the Poisson-ish profile.

### 37. Every HTTP response body is fully copied 3-4 times per request

- Location: `pkg/protocol/http/http.go:403`
- Category: allocations
- Finder: protocol
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** After the body is read into a pooled buffer, Execute makes an unconditional full copy into rawResponseBody (line 403), a second full copy into respBody (line 407) even though decodeHTTPResponseContent never mutates its input and returns the same slice when there is no Content-Encoding, a third full copy inside applyDataFormattingChain (data_formatting.go:93 `formatted := append([]byte(nil), body...)`) which runs even when the formatting chain is empty and auto-detect is off, and a fourth copy converting to string at line 427 (`result.ResponseBody = string(respBody)`). The cache-store path independently deep-copies (cache.go:162), so the defensive copies are redundant. In the default configuration (no cache, no encoding, no formatting) only the single copy out of the pooled buffer is actually needed.

**Evidence:** rawResponseBody := append([]byte(nil), buf.Bytes()...)
...
respBody := append([]byte(nil), rawResponseBody...)
respBody, responseHeaders, err = decodeHTTPResponseContent(respBody, ...)
...
formattedBody, err := applyDataFormattingChain(respBody, h.dataFormattingAutoDetect, h.dataFormattingChain)
...
result.ResponseBody = string(respBody)

**Hot path:** HTTP.Execute runs once per HTTP request for every VUser; SOAP and java_http also route through it. At thousands of RPS with 100 KB-1 MB responses this is multiple GB/s of avoidable allocation traffic and directly drives GC pause/CPU on load generators.

**Verification notes:** Verified all four copies at the cited lines: http.go:403 (necessary pool escape), http.go:407 (fully redundant — decodeHTTPResponseContent at http.go:550 returns input unchanged with no Content-Encoding and makes its own copy at line 559 when decoding; cache.go:162 independently deep-copies), data_formatting.go:93 (unconditional copy even with empty chain and autoDetect off, which are the defaults per data_formatting.go:23-26), and http.go:427 string conversion (necessary). HTTP.Execute is the per-request inner loop and soap/java_http route through it, so it is unambiguously hot; the fix cuts default-path full-body copies from 4 to 2 (~50% of body-copy alloc traffic), which is meaningful at large bodies/high RPS but modest for typical few-KB API responses — hence medium, not high.

**Refined fix:** Keep the line-403 copy as the single unconditional escape from the pooled buffer (it must happen before bufPool.Put regardless of caching — do NOT gate it on h.cache != nil as the original fix suggests). Delete line 407 and pass rawResponseBody directly to decodeHTTPResponseContent (safe: it returns the input untouched or a freshly allocated decoded slice, and cache.store deep-copies on its own). Add an early return in applyDataFormattingChain: `if !autoDetect && len(chain) == 0 { return body, nil }` — the entry copy at data_formatting.go:93 can go entirely since every transform returns a new slice. Optionally also remove the redundant internal copies at http.go:559 and data_formatting.go:108 (decoders read via bytes.NewReader and never mutate input). Leave the string(respBody) at line 427 as-is; it is the irreducible second copy since respBody must stay []byte for extractors/assertions/snapshot.

**Risk:** Must preserve immutability assumptions: the snapshot/cache/extractor paths all read the body after the pooled buffer is returned, so the one escaping copy (or the string conversion) must happen before bufPool.Put. Data-formatting transforms that decode in place still need their own output slices.

### 38. HTTP extractors re-parse the full response body per extractor and recompile XPath/JMESPath per call

- Location: `pkg/protocol/http/extract.go:270`
- Category: cpu
- Finder: protocol
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** Each xpath extractor call converts the body to string (full copy), parses the entire body with xmlquery, and on failure re-parses it with htmlquery (two full parses for HTML bodies), and compiles the XPath selector via xpath.Compile on every call (lines 271-295, 326-331) with a base64+JSON config decode per call (lines 306-324). Each jsonpath/jmespath extractor re-decodes the entire JSON body per extractor (lines 352, 451), and jmespath.Search re-parses the expression AST every call. extractCSS (line 240) and extractBoundary (line 556) also make full string(body) copies. Only regex extraction is cached (compiledRegexCache). A response with k extractors pays k full parses plus k body copies.

**Evidence:** if doc, err := xmlquery.Parse(strings.NewReader(string(body))); err == nil {
    selector, err := compileXPathSelector(expr, namespaces)  // xpath.Compile per call
...
decoder := json.NewDecoder(bytes.NewReader(body))  // full JSON decode per extractor

**Hot path:** Extractors run for every action on every iteration of every VUser; correlation-heavy LoadRunner/JMeter imports commonly attach several extractors per request, so large JSON/XML bodies get parsed 3-5x per response at full request rate.

**Verification notes:** Every specific checks out in pkg/protocol/http/extract.go: extractXPath does string(body) copy + full xmlquery.Parse per call with htmlquery.Parse fallback (lines 270/290), xpath.Compile per call (271/285), base64+JSON config decode per call (306-324); extractJSONPath/extractJMESPath fully re-decode the JSON body per extractor (352/451) and jmespath v0.4.0 Search() re-parses the AST every call (verified in api.go:41-48); extractCSS (240) and extractBoundary (556) each make full body copies; only regex is cached (compiledRegexCache, whose own comment states the exact hot-path rationale that was never applied to xpath/jmespath). The extractor loop runs inside Execute at http.go:428-475 — per action, per iteration, per VUser — so this is the generator's hottest path; k structured extractors cost k full parses (2k for HTML via the xpath fallback). Medium (not high) is right: regex/boundary — the most common LR web_reg_save_param mapping — are already cheap, the path already makes 2-3 full body copies upstream (http.go:403/407/427), and the multiplier only bites on correlation-heavy JSON/XML scripts with nontrivial bodies.

**Refined fix:** Two independent layers, cheapest first. (1) Expression caches, mirroring compiledRegexCache: for xpath, key a bounded sync.Map on the ORIGINAL (possibly config-encoded) expression string and store the decoded config together with the compiled *xpath.Expr — one entry kills the per-call base64+JSON decode AND xpath.Compile; sharing cached *xpath.Expr across VUser goroutines is safe because Expr.Select() clones the internal query (verified in xpath@v1.3.6 xpath.go:129-131) and xmlquery/htmlquery QuerySelector use Select. For jmespath, cache jmespath.Compile results; treeInterpreter holds no per-Search mutable state in v0.4.0 but add a concurrency note/test since this is not documented API contract. Boundary/regex-ordinal config decodes can join the same pattern for free. (2) Parse-once-per-response: build a lazy context in the http.go:428 loop (decoded-JSON `any` with UseNumber, memoized xml-or-html doc including which parser succeeded so the HTML fallback runs once not per-extractor, and one body string — reuse the string already built for result.ResponseBody at http.go:427 instead of re-converting in extractBoundary/extractCSS) and thread it into extract/extractAll; scope it strictly to the single Execute. Also replace strings.NewReader(string(body)) with bytes.NewReader(body) where a reader is all that is needed. Extractors only read the shared decoded JSON, so aliasing is safe; assertions (assert.go) do plain string contains and need no changes.

**Risk:** Parsed-document reuse must be scoped to a single Execute (bodies differ per response). The compile caches are bounded only if expressions are static per script — same assumption compiledRegexCache already documents; use LoadOrStore and consider a size cap if expressions can be parameterized.

### 39. Kafka/JMS/EventHubs/WebSocket/java_http/jsonextract recompile extractor regexes on every message

- Location: `pkg/protocol/kafka/kafka.go:790`
- Category: caching
- Finder: protocol
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** The non-HTTP protocols compile extractor regexes per call with no memoization: kafka.go:790 (per kafka_consume/request_reply), jms/jms.go:1822 (per JMS message), eventhubs/helpers.go:166 (per event), websocket/websocket.go:618 (per ws_receive), java_http/java_http.go:223 (per response, and twice per mapping when the expression is not valid regex, via the QuoteMeta fallback at line 225), and jsonextract/jsonextract.go:98 (used by the database and network protocols per request). The HTTP package already solved exactly this with compiledRegexCache (http/extract.go:32) and documents the per-request compile as "a measurable CPU/allocation cost on the load-generator hot path", but the fix was never propagated.

**Evidence:** case "regex":
    re, err := regexp.Compile(ext.Expression)
    if err != nil {
        return "", fmt.Errorf("invalid regex: %w", err)
    }
    matches := re.FindSubmatch(rec.Value)

**Hot path:** Messaging consume/produce loops run per message at thousands of messages/sec per generator; regexp.Compile costs microseconds plus significant allocation, multiplied by every message with a regex extractor.

**Verification notes:** Verified all six cited sites compile extractor regexes per message with no memoization (kafka.go:790, jms.go:1822, eventhubs/helpers.go:166, websocket.go:618, java_http.go:223-225, jsonextract.go:98), each reached from per-message consume/response loops (kafka.go:561/622, jms.go:729/785/1088, eventhubs.go:243, websocket.go:551, java_http.go:144, database.go:420, network.go:594). The HTTP package's compileRegexCached (http/extract.go:38) is unexported and its doc comment already declares this exact cost "measurable" on the generator hot path, so the codebase has itself judged the fix worthwhile. Impact is real but conditional on scripts using regex extractors on these protocols: ~1-4 us + several KB allocation per compile, a few percent of a core plus GC pressure at thousands of msgs/sec per pattern. The stated cache-growth risk is negligible: extractor expressions are copied verbatim from the static DSL with no per-iteration interpolation (pkg/engine/vuser_resolve.go:487-503), so keys are bounded by the script exactly as in the HTTP case.

**Refined fix:** Hoist the existing helper into a shared package rather than exporting from http: pkg/protocol/jsonextract is the natural home since it already contains the sixth site and is imported by database/network (it pulls only stdlib + jmespath, a harmless extra dep for kafka/jms/eventhubs/websocket/java_http). Export CompileRegexCached there (or make jsonextract.Regex internally cached), use it at all six sites, and have http's compileRegexCached delegate to it so there is one cache. Keep the existing semantics: errors not cached, LoadOrStore for concurrency. No cap is needed — expressions are provably static (vuser_resolve.go copies them without interpolation). For java_http, key the cache on the raw expression and store the final *Regexp whichever branch produced it (valid regex or QuoteMeta fallback), so plain-literal mappings stop paying a failed Compile + MustCompile on every response; this changes no behavior since applyResponseMapping never surfaces compile errors.

**Risk:** If any expression is variable-interpolated per iteration the cache grows; use LoadOrStore with an optional cap. Behavior is otherwise identical (compile errors still surface per call).

### 40. SOAP fault detection and WS-Security injection compile regexes on every request

- Location: `pkg/protocol/soap/soap.go:670`
- Category: cpu
- Finder: protocol
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** firstSOAPTagInnerXML builds a pattern with fmt.Sprintf and calls regexp.MustCompile on every invocation. It runs at least twice per SOAP request (soapEnvelopeOverheadKB at line 168 on the request body, detectSOAPFault at line 140 on the full response body) and 4+ more times when a fault is present (faultstring/Text/faultcode/Value lookups), each compiling a fresh `(?is).*?` regex and scanning the whole body. applySecurity additionally compiles up to three prefix-derived regexes per request (lines 432, 436, 441) when WS-Security is configured. The tag set is a small fixed vocabulary and the envelope prefix set per script is tiny, so all of these are trivially cacheable.

**Evidence:** func firstSOAPTagInnerXML(body, tag string) string {
    pattern := fmt.Sprintf(`(?is)<(?:[A-Za-z_][A-Za-z0-9_.-]*:)?%s\b[^>]*>(.*?)</...`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))
    re := regexp.MustCompile(pattern)

**Hot path:** Runs on every SOAP request/response for every VUser (fault detection is unconditional), so a SOAP load test pays 2-6 regex compilations plus full-body regex scans per transaction.

**Verification notes:** Verified soap.go:668-670: firstSOAPTagInnerXML compiles a fresh regex per call with no caching, while the same file precompiles four other package-level patterns (lines 48-51). Traced callers: 2 unconditional compiles + full-body scans per SOAP transaction (soapEnvelopeOverheadKB on the request body at line 168 — always reached since shapeSOAPBody emits an Envelope; detectSOAPFault on the response at line 633 — runs even when failOnFault is false), +2-4 on fault responses (finding's "4+" is a slight overstatement), and applySecurity hits all 3 compiles (lines 432/436/441) for typical shaped bodies because they contain no Header element. SOAP.Execute is per-action/per-VUser/per-iteration, so this is genuinely hot; impact is medium (few-µs + several-KB allocs per compile → measurable generator CPU/GC at scale) rather than high because it's a modest fraction of the path's existing full-body copies and HTTP I/O.

**Refined fix:** Precompile a package-level map[string]*regexp.Regexp for the six fixed tags actually used (Body, Fault, faultstring, Text, faultcode, Value — grep confirms this is the complete set), built with the same pattern template; keep a sync.Map fallback compile for any unexpected tag. Cache the three applySecurity patterns in a sync.Map keyed by envelope prefix (bounded: one prefix per script in practice). Drop or fix the suggested strings.Contains("Fault") pre-check: as written it misses lowercase <fault> matched by (?i), and once the regex is cached the failing lazy scan is O(n) anyway, so the pre-check buys little.

**Risk:** Minimal — identical patterns, just compiled once. The Contains pre-check must be case-insensitive-safe to avoid missing lowercase <fault> variants matched by (?i).

### 41. Database protocol JSON-marshals row payload then unmarshals it up to three times per query

- Location: `pkg/protocol/database/database.go:383`
- Category: cpu
- Finder: protocol
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** query() serializes all fetched rows to a JSON string (encodeDatabasePayload, line 265) into result.ResponseBody. The same Execute then re-unmarshals that string for extractors (decodeDatabasePayload, line 383), unmarshals it a second time for assertions (line 433 `json.Unmarshal([]byte(body), &payload)` — which also copies the string to bytes), and BuildProtocolEvidence unmarshals it a third time (evidence.go:226-235 parseEvidencePayload, called unconditionally at line 158). Body-source extractors additionally do their own full unmarshal per extractor inside jsonextract.JSONPath/JMESPath. The in-memory row set existed as map[string]any moments earlier and is thrown away.

**Evidence:** payload, err := decodeDatabasePayload(result.ResponseBody)   // extractors
...
var payload map[string]any
_ = json.Unmarshal([]byte(body), &payload)                    // assertions
...
result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)  // parses ResponseBody again

**Hot path:** Runs once per db_query/db_exec action per VUser iteration; with default max_rows=100 the payload can be tens of KB, so every database transaction pays 1 marshal + 3-4 unmarshals of the same data.

**Verification notes:** Verified every hop: query() marshals rows at database.go:265; runDatabaseExtractors re-unmarshals at :383 (plus a per-extractor []byte copy at :388 and a full json.Unmarshal per body-source extractor in jsonextract.JSONPath/JMESPath); evaluateDatabaseAssertions unmarshals again at :433; and BuildProtocolEvidence->parseEvidencePayload (evidence.go:32,226-235) parses a third time unconditionally for every database action via Execute line 158. This runs once per db_query/db_exec per VUser iteration with default max_rows=100 (tens-of-KB payloads plausible), so it is real redundant CPU + allocation/GC load on the generator hot path — though confined to database-protocol scripts and dwarfed by DB round-trip latency, hence medium not high.

**Refined fix:** Decode result.ResponseBody exactly once in Execute (after the action succeeds), not the pre-encode map — the finding's own risk note is correct that pre-encode values (int64, driver types) differ from JSON-decoded forms (float64), so reusing the pre-encode map could shift assertion/extractor/evidence semantics. Thread the single decoded map[string]any into runDatabaseExtractors, evaluateDatabaseAssertions, and a new additive BuildProtocolEvidenceWithPayload(action, result, payload) (evidence.go is shared across protocols; keep the existing signature delegating with a nil payload that falls back to parsing). Add jsonextract variants that evaluate JSONPath/JMESPath against an already-decoded any so body-source extractors stop re-parsing per extractor; keep regex/boundary on raw bytes, and hoist []byte(result.ResponseBody) out of the extractor loop in runDatabaseExtractors. Net: 1 marshal + 1 unmarshal per action with byte-identical behavior.

**Risk:** Extractors with regex/boundary types still read the raw string body — keep that path. Evidence semantics must stay identical (evidenceRows picks the same keys), and normalizeSQLValue-produced values must round-trip identically to the JSON-decoded forms (e.g. int64 vs float64) or assertion comparisons could shift; safest is to reuse the decoded-from-string payload once rather than the pre-encode map.

### 42. TLS client config has no ClientSessionCache, disabling TLS session resumption

- Location: `pkg/protocol/http/config.go:80`
- Category: network
- Finder: protocol
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** buildTLSConfig creates a custom tls.Config without setting ClientSessionCache. Per crypto/tls documentation, a nil ClientSessionCache disables session resumption, and net/http does not install one into a caller-supplied TLSClientConfig. Every new HTTPS connection therefore performs a full TLS handshake (extra round trip plus asymmetric crypto) instead of an abbreviated/resumed one. This is amortized away when keep-alives reuse connections, but load tests frequently set keep_alive=false (transportConfig.disableKeepAlive) or churn connections past MaxConnsPerHost=100, at which point every request pays a full handshake.

**Evidence:** func buildTLSConfig(config map[string]any) (*tls.Config, error) {
    tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}

**Hot path:** One tls.Config per VUser transport; with keep-alive disabled or high connection churn across thousands of VUsers, full handshakes dominate generator CPU (ECDHE + certificate verification per connection) and inflate measured connect/TLS times.

**Verification notes:** Confirmed: config.go:80 builds tls.Config with no ClientSessionCache (zero repo hits), so client-side TLS resumption is disabled; net/http never installs a cache into caller-supplied configs. The keep_alive=false path is real and wired end-to-end (engine/config_protocols.go:32 -> DisableKeepAlives; LR imports set it via RTS KeepAlive and web_set_sockets_option close_keepalive_connections in pkg/convert/enterprisearchive), making full handshakes per-request at VUser scale. One protocol instance per VUser (engine.go:180-190) means a per-instance cache is naturally VUser-isolated. Impact is medium only on keep-alive-off/high-churn tests; negligible on default keep-alive runs — and the MaxConnsPerHost=100 'churn' sub-claim is wrong (excess requests queue, they don't churn connections).

**Refined fix:** In buildTLSConfig set tlsCfg.ClientSessionCache = tls.NewLRUClientSessionCache(0) (0 uses the default capacity of 64; the cache is per-VUser because Init builds one tls.Config per VUser instance, so no cross-VUser session leakage). Gate it behind an optional bool knob, e.g. config["tls_session_resumption"] defaulting to true — default-on actually improves LoadRunner parity, since LR reuses SSL sessions within a vuser across iterations even when closing keep-alive connections. Document that turning it on changes SUT-observed handshake mix (resumed vs full) and lowers reported TLS-handshake timings; tests deliberately modeling brand-new clients should set it false. Note TLS 1.3 resumption in Go still performs ECDHE (psk_dhe_ke); the savings are chiefly certificate-chain validation and one round trip on TLS 1.2.

**Risk:** Changes what the system under test observes: resumed handshakes exercise different server code paths and lower TLS timing metrics, which some LR-style tests deliberately avoid. Making it configurable (and per-VUser, never shared across VUsers) keeps realism controllable.

### 43. Assertions and content checks convert the full response body to string per check

- Location: `pkg/protocol/http/assert.go:62`
- Category: allocations
- Finder: protocol
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** evaluate() runs string(body) for every body_contains/body_not_contains assertion — a full body copy per assertion per request since Go does not elide the conversion in a function-call argument. evaluateContentChecks additionally lowercases the entire body per case-insensitive check per request (line 174 strings.ToLower(haystack) inside the loop). The same per-assertion copy pattern repeats in websocket (websocket.go:238/244 convert the whole message with []byte(result.ResponseBody) per extractor and per assertion), kafka (kafka.go:827), jms (jms.go:1857), and eventhubs (helpers.go:195).

**Evidence:** case "body_contains":
    if !strings.Contains(string(body), a.Value) {
...
haystack := bodyText
if check.CaseInsensitive {
    haystack = strings.ToLower(haystack)   // full-body copy per check

**Hot path:** Assertions run on every request/message for every VUser; multi-assertion scripts on 100 KB+ bodies duplicate the body once per assertion at full request rate, adding to the copy load from the response pipeline.

**Verification notes:** Verified all five cited sites verbatim (assert.go:62/67/166/174, websocket.go:237-244, kafka.go:827, jms.go:1857, eventhubs/helpers.go:195). string(body) as a strings.Contains argument is not elided (stack tmpbuf only covers ≤32B), and evaluate() runs per assertion per request on the core HTTP path with bodies buffered up to a 64 MiB default cap. The finding actually understates it: evaluateContentChecks is invoked unconditionally on every successful request (http.go:485) and executes bodyText := string(body) before the loop, so every successful request pays one full-body heap copy even with zero content checks configured.

**Refined fix:** Original fix is correct; refine as follows. (1) In evaluate(), use bytes.Contains(body, []byte(a.Value)) — the small needle conversion is non-escaping and stack-allocated for needles ≤32B. (2) In evaluateContentChecks, add an early `if len(checks) == 0 { return nil }` — this alone removes an unconditional per-request full-body copy on every successful request, the biggest single win. Then drop bodyText entirely: work on []byte with bytes.Contains, compute loweredBody := bytes.ToLower(body) at most once only if any non-resource check is CaseInsensitive (bytes.ToLower keeps identical Unicode semantics to strings.ToLower), and precompute lowered needles once in parseContentCheckMaps since checks are parsed once at protocol init. (3) In websocket receive, convert result.ResponseBody to []byte once and share across the extractor and assertion loops. (4) Apply the same bytes.Contains change in kafka/jms/eventhubs evaluateAssertions (lower payoff — message bodies are typically small — but zero risk).

**Risk:** Minimal — bytes.Contains is semantically identical. Case-insensitive lowering must keep current strings.ToLower Unicode semantics (don't switch to ASCII-only folding without checking expectations).

### 44. Full Kafka message body copied into Extractions map on every produce and consume

- Location: `pkg/protocol/kafka/kafka.go:716`
- Category: allocations
- Finder: protocol
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** addRecordExtractions unconditionally stores the entire record value as a string under kafka_body for every kafka_produce, kafka_consume, and kafka_request_reply — a full copy of the message payload retained on the Result (and carried into per-VUser variables and evidence metadata, which clones the Extractions map at evidence.go:221). For large messages this doubles the per-operation memory footprint whether or not any script references {{var.kafka_body}}.

**Evidence:** result.Extractions["kafka_body"] = string(rec.Value)

**Hot path:** Runs on every Kafka operation; at thousands of messages/sec with payloads up to the 1 MiB Event Hubs cap this is a sustained duplicate-allocation stream on the generator.

**Verification notes:** Confirmed at kafka.go:716 exactly as cited; addRecordExtractions runs on all four data paths (lines 434/448/542/618) with no cap or truncation anywhere (checkRecordSize only enforces max_message_bytes; redaction scrubs keys, never truncates). Beyond the claimed per-op duplicate allocation and per-VUser retention via mergeExtractions (vuser_resolve.go:462), I found an amplifier the finding missed: archive_writer.go:136+144 JSON-encodes every result's Extractions AND ProtocolEvidence (which clones extractions again at evidence.go:221) into uncompressed transactions.jsonl — the full body hits disk twice per kafka op, so kafka-heavy runs balloon archives (~36 GB body bytes for 30 min at 1k msg/s x 10 KB). Raised impact low→medium because the same one-line population drives allocation churn, VUser retention, and archive bloat; the distributed RPC path is unaffected (transactionBuffer forwards only soap_operation).

**Refined fix:** Two-layer fix: (1) at protocol Init, scan the script for references to kafka_body (templates {{kafka_body}}/{{var.kafka_body}}, assertions, control-flow conditions) and only populate when referenced — user extractors don't need it since runExtractors/evaluateAssertions operate on the raw kafkaRecord; (2) independently cap the stored value (e.g. first 8-16 KB with a truncation marker) behind an explicit config.kafka.capture_full_body opt-in, and exclude body-sized extraction keys from the evidence-metadata clone at evidence.go:221 so the archive writer stops persisting the payload twice per operation. Apply the identical treatment to jms_body (pkg/protocol/jms/jms.go:1674), which has the same pattern and feeds the same archive sink.

**Risk:** Behavior change for scripts that template {{var.kafka_body}} — the conditional population must be driven by actual reference detection or a config flag, otherwise correlations silently break.

### 45. Generator transactionBuffer copies the whole 1024-entry slice on every transaction past the cap, under the hot-path mutex

- Location: `pkg/distributed/generatorrpc/service.go:348`
- Category: allocations
- Finder: metrics-pipeline
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** transactionBuffer.RecordTransaction is the engine ResultSink on generator pods (wired at service.go:1353/1371), invoked for every business transaction from every VUser. After append, `if len(b.transactions) > 1024` re-allocates and copies the last 1024 TransactionMetric structs (~250B each, so ~256KB) into a fresh slice — and because the new slice has no spare capacity, the NEXT append reallocs and copies again. So each transaction beyond 1024 per publish window costs two O(1024) struct copies plus a 256KB allocation, all while holding b.mu, which serializes every VUser in the group.

**Evidence:** b.transactions = append(b.transactions, tx)
if len(b.transactions) > 1024 {
	b.transactions = append([]TransactionMetric(nil), b.transactions[len(b.transactions)-1024:]...)
}

**Hot path:** Per business transaction on every generator; at >1024 TPS per group (the normal case for thousands of VUsers with a 1s publish interval) this degenerates to ~0.5MB of memcpy + a large allocation per transaction, inflating GC pressure and lock hold time on the VUser result path.

**Verification notes:** Code at service.go:347-350 matches the quote exactly, and the wiring claim holds: transactionBuffer is created at :1353 and installed as the engine ResultSink at :1371 via combineSinks, whose combinedSink.RecordTransaction (:1985) delegates straight to the buffer; engine VUser goroutines call it synchronously per business transaction (vuser.go:241 auto per-iteration, :416/:450/:464 explicit). Mechanism confirmed: TransactionMetric is 200B, so the trim allocates a cap==len==1024 slice (204,800B = exactly 25 pages), meaning every subsequent append re-growslices (copy 1024, ~256KB alloc) and then trims again (copy 1024, ~205KB alloc) — two ~205KB copies plus ~460KB garbage per transaction, under b.mu which all group VUsers contend. The one overstatement is hotness: the only drainers are the publish ticker (default 1s, :448) and the final publish, so the pathological branch fires only when a single group exceeds ~1024 business transactions/sec on a single generator pod — zero cost below that; above it the cost is severe (400MB/s+ memcpy/garbage at a few thousand TPS) and distorts measured latency exactly at peak load, so impact is medium overall, escalating to high for high-TPS-per-pod runs.

**Refined fix:** Prefer the amortized in-place trim over a ring buffer (no wrap-order logic, Drain/DrainWindow unchanged): replace `if len(b.transactions) > 1024 { b.transactions = append([]TransactionMetric(nil), b.transactions[len(b.transactions)-1024:]...) }` with `if len(b.transactions) >= 2048 { n := copy(b.transactions, b.transactions[len(b.transactions)-1024:]); b.transactions = b.transactions[:n] }`. append's normal doubling leaves cap >= 2048 after the first growth, so after warmup there are no per-transaction allocations and each retained struct is copied at most once per 1024 transactions (~200B amortized per tx instead of ~410KB). Chronological order and most-recent-retention semantics are preserved; DrainWindow already nils the slice each publish window so peak memory only rises from ~205KB to ~410KB per group buffer. Optionally zero ServerTelemetry/Snapshot pointers in the discarded prefix is unnecessary since the whole array is dropped at the next drain.

**Risk:** DrainWindow must still return samples in a sane order; ring-buffer ordering differences are invisible downstream (the controller sorts/aggregates), but archive/error-evidence sampling semantics (which 1024 survive) shift from 'last 1024' only if implemented incorrectly.

### 46. Group-filtered metrics endpoint re-clones and re-merges t-digests for all 3600 history points on every request, re-triggered every live update

- Location: `pkg/api/group_metrics_filter.go:86`
- Category: caching
- Finder: metrics-pipeline
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** filterLiveMetricsHistoryForGroups runs filterLiveMetricsForGroups on every history point; each call clones the LiveMetrics and runs applySelectedGroupAggregate, which allocates a fresh engine.NewTDigest(200) (~14KB: 1000-float buffer + centroid slice) and Merges each selected group's digest snapshot (sort + compress) — per point. For a 3600-point history that is ~50MB of throwaway digest allocations plus thousands of sort/compress passes per request, followed by JSON-marshaling the whole filtered history. CommandCenterPrototype.tsx:1259 calls this endpoint inside an effect whose deps include liveMetrics?.timestamp (line 1288), so with a group filter active it refires on every live metrics update — about once per second.

**Evidence:** for _, point := range history {
	filtered = append(filtered, filterLiveMetricsForGroups(point, filter))
}  // each: mergedDigest := engine.NewTDigest(200); mergedDigest.Merge(...) per group

**Hot path:** Once per second per Command Center viewer with a group filter during a live run; cost grows linearly with run age (history length) × selected groups.

**Verification notes:** Verified every link in the chain: filterLiveMetricsHistoryForGroups (group_metrics_filter.go:86) filters all history points per request; applySelectedGroupAggregate unconditionally allocates NewTDigest(200) (~14.4KB: 1000-float buffer + 400-centroid cap per tdigest.go:44-56) and Merge→compress does a sort.Slice per group per point; history is capped at 3600 (runner.go:797) and in-memory points carry DurationDigest snapshots (metrics.go:152,476). Hot path confirmed: handleGetMetrics (handlers.go:429) is called by CommandCenterPrototype.tsx (~1259) with groups params in an effect keyed on liveMetrics?.timestamp — though polling is every 2s (EXECUTION_POLL_INTERVAL_MS=2000), not 1s as claimed. Cost is conditional on live run + active group-subset filter + long run, so medium (not high) is the honest rating: ~50MB garbage + hundreds of ms CPU per request on the controller, per filtered viewer.

**Refined fix:** Layer the fix cheapest-first: (1) lazy-allocate the merged digest in applySelectedGroupAggregate — only call engine.NewTDigest(200) when the first selected group with a non-nil DurationDigest (Count>0) is found; this alone removes the ~52MB/request allocation for weighted-fallback and persistence-rehydrated histories (DurationDigest is json:"-" so restored points never have it). (2) Single-group shortcut: when len(selected)==1, copy the group's P50/P90/P95/P99 directly — semantics are identical because those fields were computed from the same digest (metrics.go:145-152). (3) Memoize the filtered history on the managed test keyed by sorted filter-name signature, storing (filtered slice, source history length, last timestamp); since points are immutable once appended, only filter points beyond the cached length on subsequent requests. Invalidate when the cap-trim re-slices History (metrics.go:478 / persistence.go:303) or on test restart, and guard with the same lock that protects test.History. Skip the suggested `since` API parameter — it changes the response contract and the memoization alone removes the O(history) work.

**Risk:** Cache invalidation on test restart/delete; the single-group shortcut must keep identical percentile semantics to the merged-digest path.

### 47. TDigest eagerly allocates a 1000-entry buffer; computeSnapshot creates one per unique key per second across six dimensions

- Location: `pkg/engine/tdigest.go:48`
- Category: allocations
- Finder: metrics-pipeline
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** NewTDigest(200) pre-allocates a 1000-float64 buffer (8KB) plus a 400-centroid slice (~6.4KB) up front. computeSnapshot (pkg/engine/metrics.go:475-487) builds a fresh digest for the fleet plus one per unique transaction, action, dependency-target, SOAP-operation, network-profile and group name — every snapshot interval (1s), discarding them after quantile extraction. With a large imported script exposing hundreds of unique action/transaction names, this is hundreds × ~14.4KB ≈ multiple MB/s of short-lived allocation on both the generator engine and the controller merge path (mergeGeneratorMetricAggregates also news a digest per series per merge), even when a key sees only a handful of samples in the window.

**Evidence:** bufCap := int(compression) * 5
return &TDigest{
	compression: compression,
	centroids:   make([]centroid, 0, int(compression)*2),
	buffer:      make([]float64, 0, bufCap),

**Hot path:** Per snapshot interval (1/sec) per unique metric key on every generator and on the controller; allocation volume scales with script action-name cardinality × snapshot rate, contributing steady GC pressure for the whole run.

**Verification notes:** Verified tdigest.go:48 exactly matches: 8KB buffer + 6.4KB centroid cap eagerly allocated per NewTDigest(200). computeSnapshot runs at a confirmed 1s interval (engine.go:122) and news a digest per unique key seen per window across 6 dimensions with no pooling (Reset() has zero production callers). The controller path is worse than claimed: mergeGeneratorMetricAggregates (distributed_metrics.go:986) calls NewTDigestFromSnapshot per generator per series per merge, and that constructor (tdigest.go:189-203) wastes its entire 14.4KB — the buffer is never used and the 400-cap centroid slice is immediately discarded and reallocated — with the merge itself running N times/sec for N generators (distributed_stream.go:402). Minor overstatement: per-key digests are only created for keys active in the window, not all script keys, but under sustained load that distinction mostly vanishes.

**Refined fix:** Three targeted changes, in value order: (1) rewrite NewTDigestFromSnapshot to construct the TDigest struct directly (set compression/bufferCap, nil buffer, centroids sized to len(snapshot.Centroids)) instead of calling NewTDigest — its current eager 14.4KB is 100% wasted; (2) in NewTDigest, allocate buffer and centroids lazily (nil slices, keep bufferCap as the flush threshold; append growth is behavior-neutral since flushBuffer triggers on len>=bufferCap, not capacity); (3) in mergeGeneratorMetricAggregates, add a TDigest.MergeSnapshot(TDigestSnapshot) that appends centroids straight from the wire snapshot, eliminating the temp digest per generator per series entirely. Skip cross-window pooling via Reset — lazy allocation captures most of the win with zero sample-leak risk.

**Risk:** Pooling across windows must not leak samples between windows (Reset must be called reliably); lazy allocation is behavior-neutral.

### 48. ResourceCollector fully scans /proc/net/tcp and /proc/net/tcp6 every second on loaded generators

- Location: `pkg/engine/sys_metrics.go:338`
- Category: io
- Finder: metrics-pipeline
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** collectTCPDiagnostics is called from ResourceCollector.Collect on every metrics snapshot (invoked each aggregation tick at pkg/engine/metrics.go:432). It line-scans the entire /proc/net/tcp and /proc/net/tcp6 tables, calling strings.Fields (which allocates a []string per line) and hex-parsing the local port for every socket. A generator driving thousands of VUsers holds tens of thousands of sockets (plus TIME_WAIT churn), so each 1-second collect reads and tokenizes several MB of procfs text — a well-known O(sockets) pattern that modern tools avoid via netlink sock_diag — burning a measurable fraction of a core precisely on the busiest pods.

**Evidence:** for _, path := range []string{filepath.Join(procRoot, "net", "tcp"), filepath.Join(procRoot, "net", "tcp6")} {
	established, timeWait, ports := readTCPConnectionStates(path, start, end)

**Hot path:** Once per second per generator pod for the whole run; cost scales linearly with concurrent connection count, i.e. it is most expensive exactly when the generator is under the most load.

**Verification notes:** Verified end-to-end: collectTCPDiagnostics (sys_metrics.go:326-348) full-scans /proc/net/tcp+tcp6 with per-line strings.Fields, called unconditionally from ResourceCollector.Collect (sys_metrics.go:120) with zero caching/throttling, driven every 1s by MetricsCollector.aggregate (metrics.go:432, started at time.Second in engine.go:122/group.go:148) whenever VUsers are active — including on generator pods, which run one engine (and one scan loop) per concurrent assignment. /proc/net/tcp is netns-scoped so row count equals live load-test sockets plus up to ~60k TIME_WAIT rows under connection churn; kernel seq_file formatting is the classic O(sockets) cost netlink sock_diag replaced, so at 10k+ VUser scale this is tens of ms/s of CPU plus ~100k allocs/s of GC garbage inside the loaded generator process. Medium at advertised scale, low for small runs; the generatorrpc heartbeat path is only a one-shot placement probe, so it does not add a second recurring scan.

**Refined fix:** Cheaper than the suggested sockstat-only approach: (1) Established — readTCPRetransmits already parses the Tcp: line of /proc/net/snmp; pull CurrEstab from that same line (netns-scoped, covers v4+v6, zero extra IO). (2) TIME_WAIT — read the 'tw' field from /proc/net/sockstat (O(1) single small file). (3) Keep the full tcp/tcp6 table scan only for EphemeralPortUtilizationPct, sampled every 15-30s with the last value cached in ResourceCollector (it already holds lastTCP delta state, so caching fits the existing pattern; guard with a lastTCPScan timestamp). Skip the manual-field-slicing micro-opt — kernel-side formatting dominates, and it's irrelevant once the scan is off the 1s cadence. Update the fake-procRoot tests in sys_metrics tests to cover the new snmp CurrEstab/sockstat parsing and the throttle.

**Risk:** sockstat counts cover all namespaces sockets slightly differently than the per-row scan; ephemeral-port utilization becomes a slightly stale (15-30s) signal, which is acceptable for a trend gauge.

### 49. Engine metrics channel (10k, drained only on ticker tick) silently drops samples above ~10k results/sec per group engine

- Location: `pkg/engine/metrics.go:298`
- Category: concurrency
- Finder: metrics-pipeline
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** MetricsCollector.Record does a non-blocking send into a fixed 10,000-slot channel (constructed at pkg/engine/engine.go:101 and group.go:121), and the aggregate goroutine only drains the channel when the snapshot ticker fires (metrics.go:398-406). Anything produced beyond 10,000 results within one interval is dropped (droppedResults) — so a single group engine sustaining >10k TPS loses samples every window, and the surviving set is biased toward the start of each interval, skewing TPS, error rates, and percentiles at exactly the scale the platform targets. The drop is surfaced as a 'danger' collector_drops diagnostic, so at high rates the run permanently shows degraded telemetry health.

**Evidence:** select {
case mc.ch <- r:
default:
	mc.current.droppedResults.Add(1)
	// channel full — drop metric rather than blocking the hot path
}

**Hot path:** Per VUser result on the engine hot path; the ceiling binds on any single-node or per-generator group exceeding 10k results/sec — the stated scale target (thousands of VUsers × many iterations/sec).

**Verification notes:** Verified every element: non-blocking send with drop counter at metrics.go:294-301 (line 298 exact), 10k hardcoded buffer at engine.go:101 and group.go:121, and the channel's ONLY reader is the tick-time drain at metrics.go:401 inside aggregate() with a 1-second ticker (Start(time.Second) at engine.go:122/group.go:148) — so >10k results/sec per engine loses samples every window, biased to early-window survivors. Record is called per VUser action result (vuser_execute.go:27), the collector is shared across all groups of a MultiGroupEngine (ceiling is per generator process), droppedResults is cumulative/never reset and maps to a permanent 'danger' collector_drops diagnostic (pkg/api/telemetry.go:27-31), and distributed generators ship only aggregated snapshots so lost samples are unrecoverable. Impact is medium not high: only single-generator runs above ~10k results/sec are exposed, drops are operator-visible, and the platform has no verified scale benchmarks proving other engine costs don't bind first.

**Refined fix:** Simpler than the proposed mutex drainer: aggregate() is already the sole owner of batch (reused via batch[:0] at metrics.go:441), so add a third case to its existing select — `case r := <-mc.ch: batch = append(batch, r)` — draining continuously between ticks with zero new goroutines or locks; the 10k channel then only buffers the milliseconds spent inside computeSnapshot. Keep an explicit per-window batch cap (e.g. configurable, default a few hundred thousand) that increments the same droppedResults counter as a memory backstop, and consider making the buffer/cap sizable from configured workload.

**Risk:** The continuous drainer adds one mutex on the aggregation side (not the record side); memory for a window's batch becomes proportional to actual TPS, so an explicit upper cap (with the same dropped-counter) should be kept as a backstop.

### 50. Synchronous DB write (UPDATE users) on every authenticated API request

- Location: `pkg/api/auth_resolution.go:189`
- Category: database
- Finder: storage-sqlite
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** applyLocalUserBindings runs for every authenticated request in trusted-headers and OIDC modes and unconditionally executes store.UpdateUserIdentitySeen — an UPDATE on the users table — plus two SELECTs (user + role bindings). There is no throttle, unlike the login audit event which is deduped per hour. authenticatedUserFromAPIKey does the same with UpdateServiceAccountLastUsed (line 347) for every API-key request. On the default SQLite backend (rollback journal, see the pragma finding) each of these writes takes the global writer lock and fsyncs, serializing against live-metrics persistence.

**Evidence:** _ = store.UpdateUserIdentitySeen(local.ID, local.DisplayName, local.Email, now, now)  // per request
...
if err := store.UpdateServiceAccountLastUsed(account.ID, usedAt); err == nil {  // per API-key request

**Hot path:** The dashboard polls several endpoints every few seconds per open browser during live tests (useExecutionTestPolling), and CI/automation hammers the API with service-account keys; each such request currently costs 2 SELECTs + 1 UPDATE. Write throughput on SQLite is the platform's scarcest DB resource.

**Verification notes:** Verified all claims by reading the code: auth_resolution.go:189 and :347 issue unconditional UPDATEs per authenticated request via authMiddleware/projectScopedAuthMiddleware (middleware.go:144/209), GetUserBySubject really does 2 SELECTs (user + ListUserRoleBindings via getUserByQuery, sqlite_auth.go:916), the sqlite DSN sets only busy_timeout with no WAL (rollback journal, so each UPDATE is an exclusive-lock+fsync commit), and audit_auth_events.go proves the throttle pattern exists but is applied only to audit events. Aggravator: the service-account rate limiter runs AFTER resolveRequestAuth and defaults to unlimited, so hammering automation pays the UPDATE on every request including 429-destined ones. Downgraded impact from high to medium: request rates are dashboard-polling scale (roughly 1-5 req/s per browser), so this adds a handful of write transactions/sec of writer-lock/fsync contention against live-metrics persistence — real and worth fixing, but not thousands/sec; it approaches high only under heavy CI/service-account traffic or many concurrent viewers.

**Refined fix:** Simpler than the suggested in-memory map: the throttle state is already loaded by the existing SELECTs. In applyLocalUserBindings, only call UpdateUserIdentitySeen when local.LastSeenAt is nil, older than ~60s, or when upstream DisplayName/Email differ from the stored values (preserves prompt IdP-attribute sync). In authenticatedUserFromAPIKey, gate UpdateServiceAccountLastUsed on account.LastUsedAt (already fetched by GetServiceAccountByKeyHash) being nil or older than ~60s. This needs no new state, is HA-correct across replicas by construction, and leaves both SELECTs untouched so user-disable and session-epoch revocation (checked from the fresh SELECT at lines 175-183) stay instant. Do NOT add the optional AuthenticatedUser cache unless its TTL is a few seconds at most, because it would delay the P0 session-revoke/disable semantics.

**Risk:** last_seen_at/last_used_at become minute-granular instead of exact; a short auth cache delays user-disable/session-epoch revocation by the TTL, so keep the SELECTs uncached or the TTL very small if the P0 session-revoke semantics must stay instant. Multi-replica HA keeps per-replica throttles (worst case one write per replica per window).

### 51. Boot/failover hydration loads and JSON-decodes the full unbounded history of every test, then keeps only the last 3600 points

- Location: `pkg/api/persistence.go:284`
- Category: memory
- Finder: storage-sqlite
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** loadStoredTestHistory calls historyStore.GetHistory(test.ID) with no LIMIT — the storage layer materializes every interval row (28 columns plus three JSON blob columns unmarshalled per row: browser_json, browser_by_device_json, latency_json) into []any — and the caller then throws away everything except the last maxManagedTestHistory (3600) entries. This runs for every stored test at boot and again on HA leadership acquisition (reloadStoredTests), so failover time and startup memory churn scale with total historical rows, not with the 3600 points actually used.

**Evidence:** history, err := tr.historyStore.GetHistory(test.ID)  // no limit
...
if len(test.History) > maxManagedTestHistory {
	test.History = append([]*LiveMetrics(nil), test.History[len(test.History)-maxManagedTestHistory:]...)

**Hot path:** Runs per stored test on every controller boot and on every HA leader failover — exactly when fast recovery matters. A 12-hour soak test contributes 43k rows of which 39k are decoded and discarded; multiplied across hundreds of retained runs this dominates startup after the missing-index scan is fixed.

**Verification notes:** Verified all claims in code: persistence.go:284 calls GetHistory with no limit; sqlite_tests.go getTypedHistory selects all interval rows ORDER BY timestamp ASC with no LIMIT, unmarshalling 3 JSON blobs per row, and the caller discards all but the last 3600 (runner.go:797). Write cadence is 1 row/sec/test by default (settings.go:535, metrics.go:498) with no retention pruning of test_interval_summaries, so the 43k-rows-per-12h-soak arithmetic holds; hydration runs for every stored test (ListTests has no limit or status filter) at boot (persistence.go:171) and on every HA leadership acquisition (persistence.go:202 via testexecution.go:241). Not a per-request hot path, but it directly scales boot and failover recovery time with total historical rows.

**Refined fix:** Change the HistoryStore interface to GetHistory(testID string, limit int) (limit<=0 = unbounded) rather than adding a second method — persistence.go is the only production caller. In getTypedHistory use ORDER BY timestamp DESC LIMIT ? and reverse in Go; order by timestamp alone (the table has no PK and rowid is not portable to the Postgres rewriteSQL path; at 1-row/sec cadence timestamp ties are not a practical concern). Apply the same LIMIT to the legacy test_history fallback branch (sqlite_tests.go:279). Ship that as a standalone low-risk change; defer lazy hydration of terminal tests (the larger resident-memory win: 3600 *LiveMetrics per archived run stay in memory forever) to a separate change since it alters GetTest response timing for archived runs.

**Risk:** Must preserve chronological ordering after the DESC+reverse; the legacy test_history fallback path needs the same treatment; lazy hydration changes GetTest response timing for archived runs.

### 52. test_interval_summaries and test_history grow without bound — no retention path

- Location: `pkg/storage/sqlite_tests.go:225`
- Category: caching
- Finder: storage-sqlite
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** SaveHistory inserts one row per metrics interval (default 1s) per running test into test_interval_summaries, including up-to-multi-KB browser/latency JSON blobs. The only deletion path is DeleteTest (manual). The archive-retention sweep (pkg/api/archive_retention.go:171) only os.RemoveAll's archive files; audit_events and usage_accounting_snapshots both have DeleteBefore retention hooks, but interval summaries have none. The DB file therefore grows by ~86k rows per 24h-equivalent of aggregate run time forever, compounding the boot-scan and missing-index costs and inflating backup/vacuum times.

**Evidence:** _, err = s.db.Exec(`INSERT INTO test_interval_summaries (
	test_id, schema_version, timestamp, ... browser_json, browser_by_device_json, latency_json, ...`)  // 1 row/sec/test, never pruned

**Hot path:** Written once per second per running test by publishLiveMetrics → saveLiveMetricsHistory; read via full scans at boot/failover. Growth is permanent: a team running 8 test-hours/day adds ~10M rows/year.

**Verification notes:** Verified all claims in code: SaveHistory (sqlite_tests.go:225) writes 1 row/sec/running test (broadcastMetrics ticker floored to 1s, metrics.go:587-617 → saveLiveMetricsHistory), the only deletes are manual DeleteTest/project-delete, the archive sweep (archive_retention.go:171) only os.RemoveAll's files, and DeleteBefore hooks exist for audit/usage tables but not interval summaries; there is also no index on test_interval_summaries(test_id) (schema index list sqlite_schema.go:869-896). Stronger than claimed: GetHistory's sole production caller is loadStoredTestHistory, which runs once per test at boot AND on HA failover (persistence.go:155-213) and discards all but the newest 3600 rows per test — so every older row is unreachable dead weight, yet each hydration full-scans the entire unindexed table, making boot/failover O(tests × total_rows). Writes ride the per-second metrics fan-in path and the growth compounds failover time, so hot-path is a fair characterization; severity is medium because it is slow-burn DB bloat, not live-run latency.

**Refined fix:** Two-step, lossless-first: (1) Add `CREATE INDEX IF NOT EXISTS idx_interval_summaries_test_time ON test_interval_summaries(test_id, timestamp)` to the schemaIndexes list (sqlite_schema.go) — this alone fixes the O(tests × rows) boot/failover scans and is a prerequisite for any per-test DELETE. (2) Since loadStoredTestHistory is the only reader and truncates to maxManagedTestHistory (3600), prune losslessly: on run completion (and/or in the audit_retention.go sweep), DELETE rows per test older than that test's newest 3600 (e.g. `DELETE FROM test_interval_summaries WHERE test_id = ? AND timestamp < (SELECT timestamp FROM test_interval_summaries WHERE test_id = ? ORDER BY timestamp DESC LIMIT 1 OFFSET 3599)`); apply the same cap to the legacy test_history table. This sidesteps the archive-presence/compliance-class risk entirely because no current reader can see the pruned rows. A time-based DeleteIntervalSummariesBefore wired to archive retention can come later, gated on confirmed archive presence and compliance class as the finder noted. Note the Run Report redesign (Phase 0) may add new readers of interval history — coordinate the cap with that plan before landing.

**Risk:** Interval summaries are the fallback source when an archive is missing/tampered — pruning must only target runs whose archive is confirmed present, or honor compliance retention classes; requires the test_id index first or the DELETE itself full-scans.

### 53. TestRunner.GetTest takes the exclusive write lock and rebuilds the comparison baseline (O(all tests) scan + deep clone) on every read

- Location: `pkg/api/tests.go:18`
- Category: concurrency
- Finder: api-handlers
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** GetTest — the entry point for every test read (handleGetTest, handleGetMetrics, handleGetTestSLAStatus, requireRunArchive in handlers_archive.go:714, and ~15 other handlers) — acquires tr.mu.Lock() (exclusive), runs refreshPendingQueueMetadataLocked() (iterates all tests, and per PENDING test calls evaluateQuotaBlockLocked which iterates tests again), refreshes capacity advisories for queued/pending tests, and calls buildRunComparisonBaselineLocked which iterates every managed test and deep-clones the selected baseline's per-transaction TxMetrics maps (cloneTxMetricsMap incl. ServerTelemetry histograms) — all recomputed from scratch per call. This exclusive critical section contends directly with publishLiveMetrics (metrics.go:451) and evaluateGoalControllerLocked (metrics.go:630), which hold the same lock once per metrics interval per running test.

**Evidence:** func (tr *TestRunner) GetTest(id string) (*ManagedTest, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	tr.refreshPendingQueueMetadataLocked()
	...
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(t)

**Hot path:** Called every 2s per dashboard viewer during live tests plus on every archive/report/SLA request; the write lock serializes these reads against the metric fan-in path that ingests samples from all generators, so read polls and metric processing block each other as test count and viewer count grow.

**Verification notes:** Verified every claim in code: GetTest (pkg/api/tests.go:17-38) takes the exclusive lock, runs refreshPendingQueueMetadataLocked (capacity.go:607, O(N) scan mutating all tests + per-PENDING evaluateQuotaBlockLocked with multiple O(N) counting scans) and buildRunComparisonBaselineLocked (O(N) scan + deep clone of TxMetrics incl. ServerTelemetry histograms, tests.go:245-272) with zero caching; tr.tests holds the entire persisted run history (persistence.go:155-175, no retention pruning); the dashboard polls getTest every 2s per viewer and publishLiveMetrics (metrics.go:451) / evaluateGoalControllerLocked (metrics.go:630) contend on the same exclusive lock. Downgraded impact from high to medium: per-iteration cost is cheap pointer/time comparisons, so per-call cost is sub-ms to low-ms until history reaches thousands of records with several pending tests, degradation is progressive rather than a cliff, and polling stops on terminal status / hidden tabs. Aggravating detail the finding missed: requireRunArchive (handlers_archive.go:713) and handlers.go:844 pay the full baseline build + deep clone for a mere existence check, and because GetTest mutates QueuePosition/PendingReason on every test, every read is a write.

**Refined fix:** Three-part split, ordered by payoff/risk: (1) Add a lightweight existence/lookup method (RLock, no refresh, no baseline) and switch pure-existence callers (requireRunArchive at handlers_archive.go:713, handlers.go:844, notes handlers) to it — zero staleness risk, removes the deep clone from the archive/report path entirely. (2) Stop recomputing pending-queue metadata in GetTest/ListTests: ReconcilePendingTests (testexecution.go:221-231) already calls refreshPendingQueueMetadataLocked on the promotion cadence, so reads can serve the stored QueuePosition/PendingReason under RLock; capacity-advisory refresh for QUEUED/PENDING moves to the same tick. (3) Memoize ComparisonBaseline per test ID (store alongside the ManagedTest), invalidated on terminal status transitions, pin/unpin (PinComparisonBaseline already touches every scoped candidate, tests.go:178-190), delete, and reloadStoredTests — or cheaper still, compute it lazily only in the handlers that actually render comparison overlays via an opt-in flag, since most of the ~53 call sites never read ComparisonBaseline. Invalidation surface is small and enumerable; queue-position staleness is bounded by the reconcile tick.

**Risk:** Baseline memoization must be invalidated on every completion/pin path or the comparison overlay shows a stale baseline; pending queue positions would update on the scheduler tick rather than on read (slightly staler queue_position in responses).

### 54. GET /api/v1/scripts serializes every script's full DSL body and runs O(scripts x tests) usage enrichment with N+1 release lookups under the runner lock

- Location: `pkg/api/script.go:195`
- Category: database
- Finder: api-handlers
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** ListScriptsWithUsage (unfiltered GET /api/v1/scripts, handlers.go:502) iterates every script and, per script, scans all scenarios and all managed tests (enrichScriptSummaryLocked) — O(scripts x (scenarios + tests x groups)). For each test with a ReleaseID it calls releaseRefLocked, which issues a store.GetRelease DB query (persistence.go:522) with only per-script memoization, so the same release is re-fetched once per script — an N+1 pattern executed while holding tr.mu.RLock. The response then embeds StoredScript.Script (runner.go:745, json:"script") — the complete DSL — so a workspace containing a few multi-MB LoadRunner imports (100k+ line scripts) produces a giant JSON payload on every scripts-page load.

**Evidence:** for _, test := range tr.tests {
	...
	if test.ReleaseID != "" {
		releaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID) // -> store.GetRelease(id) per hit
	...
// StoredScript: Script *dsl.Script `json:"script"` — full body in list responses

**Hot path:** The scripts library page fetches this list on navigation (and after every import/save); cost grows multiplicatively with script count x test count, and payload grows with total imported-script size, while the RLock section blocks writers (script saves, test mutations).

**Verification notes:** Verified all cited code: ListScriptsWithUsage (script.go:195) holds tr.mu.RLock while enrichScriptSummaryLocked rescans all scenarios and all tests per script, and releaseRefLocked issues a real SQLite QueryRow (persistence.go:522, sqlite_projects.go:296). The N+1 is worse than claimed — there is NO memoization even per script (script.go:233/235 assign unconditionally, and fire twice per test when ScenarioID is set), and tr.tests hydrates every historical run at boot with no bound (persistence.go:155-175). StoredScript.Script (runner.go:745, no omitempty) puts the full DSL in every list response, and the project-filtered path (ListScriptsFiltered, script.go:113) has the identical mechanism, so the dashboard's usual project-scoped fetch (ScriptsPage/TestHub/ProtocolAssetImport, on navigation and after imports/saves) is affected too — not just the unfiltered path.

**Refined fix:** Split into three independently-shippable steps. (1) Zero-risk, do first: build one request-scoped release memo per list call — pass a shared map[string]ReleaseRef into enrichScriptSummaryLocked (or prefetch all referenced releases with a single WHERE id IN query before the loop) and check map existence before calling releaseRefLocked; this eliminates the N+1 with no API change and also fixes the double-call at script.go:233/235. Apply the same memo to the identical pattern in scenario.go:504. (2) Strip the body from list responses by setting copy.Script = nil in the enrich copy (the shallow copy at script.go:209 makes this trivial), gated by ?include=script for compatibility; note ScriptsPage already null-checks script.script and falls back to GET /scripts/{id} (ScriptsPage.tsx:484-493), but audit ProtocolAssetImport and TestHub, which may read metadata from list items — consider keeping script.metadata while dropping steps if so. Apply the same to ListScriptsFiltered, which has the identical payload problem. (3) Skip the incremental usage-stat precompute from the original suggestion — its consistency burden across test delete/reload/HA-failover paths outweighs the win once (1) and (2) land; the remaining in-memory O(scripts x tests) scan without DB calls is cheap until test history is very large.

**Risk:** API consumers that read script bodies from the list endpoint would break — needs a compatibility flag or ?include=script; usage counters computed incrementally must stay consistent through test deletion/reload paths.

### 55. GET /api/v1/tests returns the full metric history of every test, and all histories are held in RAM permanently

- Location: `pkg/api/handlers.go:110`
- Category: memory
- Finder: api-handlers
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** handleListTests returns maskManagedTests(s.runner.ListTests()); maskManagedTest (runtime_mask.go:125) is a shallow copy that retains the History slice, so the tests-list JSON includes up to 3600 LiveMetrics points (with per-transaction maps) for EVERY test. Histories are hydrated for all stored tests at boot (loadStoredTestHistory, persistence.go:280-308) and kept resident forever, so both the resident set and the list payload grow linearly with the number of completed runs — with ~100 archived runs the list response can reach hundreds of MB and the runner holds all of it on the heap.

**Evidence:** tests := s.runner.ListTests()
writeJSON(w, http.StatusOK, map[string]any{
	"tests": maskManagedTests(tests), // masked := *test keeps masked.History
	"count": len(tests),
})

**Hot path:** The tests/runs list page fetches this on navigation and after run-state changes; boot hydration cost and heap footprint grow with retained run count (3600 points x per-point transaction maps per test).

**Verification notes:** Confirmed all three legs: (1) maskManagedTest (runtime_mask.go:125) is a shallow copy retaining History ([]*LiveMetrics, json:"history,omitempty", cap 3600 at 1/sec), so every /api/v1/tests branch serializes full history per test; (2) loadStoredTests/reloadStoredTests hydrate up to 3600 points for every stored test at boot with no eviction or retention pruning; (3) the dashboard polls listTests every 10s (App.tsx:818 topbar, App.tsx:851 unfiltered desktop poll used only to count active runs) plus navigation fetches from TestHub/ResultsPage/DashboardPage. One correction: restart-hydrated points are lean scalars (intervalSummaryRecordToLiveMetrics fills no Transactions/GroupMetrics maps; SaveHistory persists typed summaries), so per-transaction-map bloat applies only to runs completed in the current process lifetime — payload is more like ~2-3MB per hour-long run (tens-to-hundreds of MB at ~100 runs), not uniformly map-heavy.

**Refined fix:** Add a list-specific mask (e.g. maskManagedTestForList) used only by handleListTests' three branches: nil out History, and replace LiveMetrics with the existing last point (it is already the scalar summary) — do NOT change the shared maskManagedTest, because handleGetTest (handlers.go:130) feeds useExecutionMetricsStream.ts:127 which backfills the execution chart from test.history. TestHub.tsx:666-682 sparklines read run.history from the list response, so either include a small downsampled tail (e.g. last 60 tps/avg/error points) in the list mask or move sparklines to a lazy per-test GET /tests/{id}/metrics fetch. Separately, make boot hydration lazy: loadStoredTestHistory should load only the newest interval record (to populate LiveMetrics for list rows) and full history on first GetTest/metrics access; apply the same to reloadStoredTests (leader failover has identical cost).

**Risk:** Anything reading history/live_metrics from the list response (sparkline previews, baseline pickers) must switch to the per-test endpoint; lazy history load adds a DB read on first per-test metrics view of an old run.

### 56. Archive JSONL pagination decodes the entire file for every page — O(n²) to read a large run

- Location: `pkg/api/archive_loaders.go:625`
- Category: algorithmic
- Finder: go-crosscutting
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** readJSONLPage opens transactions.jsonl and json.Unmarshal's EVERY line (even rows before page.Offset and after the limit is filled) just to compute the running total. A single deep page request costs a full-file decode; the dashboard's page-through loop (CommandCenterPrototype.tsx:1200-1211 fetches limit=1000, offset+=1000 until total) makes reading an archive of N transactions cost N²/1000 JSON decodes. With filter.SortBy set, readJSONLFiltered additionally materializes the whole file in memory per request.

**Evidence:** for scanner.Scan() {
    var item T
    if err := json.Unmarshal(scanner.Bytes(), &item); err != nil { ... }  // decodes every line, every page request
    if matched >= page.Offset && len(items) < limit { items = append(items, item) }
    matched++

**Hot path:** Archives of large runs hold 100k-millions of transaction rows; the Run Report / archive browser and CommandCenter group-scoped views page through the full file, and each page request re-scans it end-to-end on the controller.

**Verification notes:** Verified readJSONLPage (archive_loaders.go:604-642) json.Unmarshal's every line on every page request with no early exit, and readJSONLFiltered materializes the whole file whenever SortBy is set; ensureArchiveMaterialized is cached so the scan is the real per-request cost. The O(n²) page-through loop exists at CommandCenterPrototype.tsx:1195-1212 but is a conditional fallback (historical run + grafana variant + group subset + summaries endpoint not honoring group filters); the everyday archive browser (useExecutionArchiveModel.ts:235) is O(N) full-file decode per page turn — seconds of controller CPU per interaction on 100k-1M-row archives, so impact is real but medium rather than high. Also confirmed manifest.TransactionCount exists (archive.go:42) and is already loaded in loadArchiveTransactions.

**Refined fix:** The suggested fix as written would never fire for transactions: loadArchiveTransactions (archive_loaders.go:30) always passes a non-nil predicate closure even when the filter is empty. Refine: (1) add transactionEventFilter.isZero() and pass predicate=nil when the filter is empty; (2) in readJSONLPage, when predicate==nil skip json.Unmarshal for lines outside [offset, offset+limit) and accept an optional knownTotal (manifest.TransactionCount / interval-summary count, already loaded by the caller) so the scan can stop at offset+limit instead of reading to EOF; (3) keep filtered totals as matched-count (unchanged semantics), optionally caching (archiveID, filterHash)->total to avoid recounting per page; (4) the CommandCenterPrototype O(n²) fallback loop disappears more cheaply by making the summaries endpoint honor group filters (the client already probes for this at CommandCenterPrototype.tsx:1230-1240) than by optimizing the transaction sweep; (5) line-offset index or parquet-backed reads remain the right long-term answer but are not needed for the quick win.

**Risk:** Filtered queries still require decoding rows to evaluate the predicate, so only the unfiltered fast path is trivially safe; totals returned with filters must keep the same semantics (matched count, not raw count).

### 57. Per-transaction unbuffered file writes under one mutex on the live result-ingestion path

- Location: `pkg/api/archive_writer.go:34`
- Category: io
- Finder: archive-report
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** The archive writer's JSON encoders wrap *os.File directly with no bufio layer, so every RecordResult/RecordResourceSample/RecordIntervalSummary issues at least one write(2) syscall per record while holding w.mu. RecordResult (archive_writer.go:153) runs once per completed transaction result during a live test; at high throughput that is tens of thousands of syscalls per second, and the mutex hold time includes the kernel write, serializing result ingestion fanned in from all generators.

**Evidence:** open := func(name string) (*os.File, *json.Encoder, error) {
	file, err := os.Create(filepath.Join(archiveDir, name))
	...
	return file, json.NewEncoder(file), nil
}

**Hot path:** Hottest write path in archiving: every transaction result from thousands of concurrent VUsers running many iterations/sec flows through RecordResult for the duration of the run.

**Verification notes:** Confirmed: pkg/api/archive_writer.go:29-35 puts json.Encoder directly on *os.File, and RecordResult (line 107) holds w.mu across struct build + JSON encode + one write(2) per record; the call chain (pkg/engine/vuser_execute.go:30 -> combinedResultSink -> RecordResult) fires synchronously per protocol result from every VUser, with Sync only at Finalize. One correction: the controller does NOT ingest per-record results from generators in distributed mode (generator archives merge as bulk appends via appendArchiveContent, pkg/api/distributed_archive.go:187) — instead the identical unbuffered writer is duplicated on each generator at pkg/distributed/generatorrpc/archive.go:231-236/:279, so the serialization is per-node, not cross-fleet fan-in. Impact downgraded to medium: the buffered write is ~1-3us while the JSON encode held under the same mutex is comparable or larger, so bufio removes the syscall but not the dominant lock cost; it matters at tens of thousands of results/sec or when writeback throttling turns writes into ms stalls that block all VUsers.

**Refined fix:** Wrap each of the three files in bufio.NewWriterSize(file, 256<<10) and point the encoders at the bufio writer; in Finalize, Flush all three buffers before file.Sync()/Close() and before writeArchiveParquetFiles/updateRunArchiveArtifactIntegrity (hashing reads from disk). Apply the identical fix to the duplicated generator-side writer in pkg/distributed/generatorrpc/archive.go (open() at :231, Sync/close at :391) — generators are the highest-throughput nodes. Bound crash-loss by flushing in RecordIntervalSummary (already called once per interval under the same mutex). Optional larger win: encode the event to a per-call buffer outside the mutex and only append+increment under the lock, which removes the dominant encode cost from the critical section; bufio alone is the S-effort version. No mid-run readers of transactions.jsonl exist (archive reads are post-Finalize; live error evidence is in-memory), so flush-ordering risk is confined to Finalize.

**Risk:** A crash loses the buffered tail (today the OS page cache holds it anyway — no fsync per record); Finalize and any live-file reader must see flushed data, so Flush ordering matters.

### 58. Filtered interval summaries load the entire transaction archive into memory

- Location: `pkg/api/archive_loaders.go:250`
- Category: memory
- Finder: archive-report
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** loadArchiveIntervalSummariesForFilters calls readAllArchiveTransactions to re-bucket per-action/per-group interval stats, materializing every TransactionEvent with all nested evidence payloads even though the loop only reads Group, ActionName, Timestamp, DurationMs, and Success. It then performs the O(N*M) bucket-index scan per transaction.

**Evidence:** transactions, err := readAllArchiveTransactions(record)
...
for _, tx := range transactions {
	...
	index := archiveSummaryBucketIndex(summaries, tx.Timestamp)

**Hot path:** Invoked by handleGetRunArchiveSummaries whenever an action or group filter is set (dashboard per-group/per-action timeline views) and once per run by comparison action-scoping (up to 10 runs per comparison request) — each invocation re-parses the full transaction file.

**Verification notes:** Verified at pkg/api/archive_loaders.go:250/272: readAllArchiveTransactions fully materializes []TransactionEvent (heavy nested evidence fields per archive.go:128-167, written uncapped per result by archive_writer.go RecordResult) with no caching, plus a wasted snapshot-normalization pass, and archiveSummaryBucketIndex is a linear O(M) scan per transaction. Callers amplify it beyond the claim: ResultsPage.tsx:665 fetches action-filtered summaries for up to 10 runs in parallel on every filter change; CommandCenterPrototype.tsx:1223 paginates group-filtered summaries at 1000/page while the server recomputes the full filtered set (full re-parse) per page; CSV multi-comparison (maxArchiveComparisonRuns=10) adds per-run parses on top of buildRunArchiveReport's own. Not a polling loop, but a routine interactive path whose peak heap scales as archive-size × concurrent runs — a plausible controller OOM on multi-hundred-MB archives.

**Refined fix:** Add a streaming visitor (e.g. forEachArchiveRecord[T](record, pathSelector, fn) alongside readJSONLFiltered in archive_loaders.go) and decode into a trimmed 5-field struct {Timestamp, Group, ActionName, DurationMs, Success} — json.Unmarshal skips unknown fields, avoiding nested allocations and making normalizeArchiveTransactionSnapshots unnecessary for this path. Replace archiveSummaryBucketIndex's linear scan with sort.Search over summary timestamps, preserving the exact current semantics: first index where !tx.Timestamp.After(summaries[i].Timestamp), clamping to len-1 when past the end. Consider reusing the same streaming helper for the identical full-materialization pattern in archive_report.go:24, archive_analysis.go:350, and handlers_load_comparison.go:74 (separate change).

**Risk:** Low; identical outputs. Watch that snapshot URL normalization (not needed here) is not accidentally required.

### 59. Parquet export at Finalize double-materializes the whole run in memory

- Location: `pkg/api/archive_parquet.go:117`
- Category: memory
- Finder: archive-report
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** writeArchiveParquetFiles reads the entire transactions.jsonl back into a []TransactionEvent (readAllJSONLRecords), then toParquetTransactions builds a second full []parquetTransactionEvent slice (with per-row re-marshaled Extractions/ServerTelemetry/Snapshot JSON strings) before any row is written. Peak memory at run finalize is roughly 2x the decoded archive size, hitting the controller right at test completion — potentially alongside other completing tests.

**Evidence:** transactions, err := readAllJSONLRecords[TransactionEvent](filepath.Join(rootDir, manifest.TransactionsFile))
...
if err := writeParquetFile(filepath.Join(rootDir, manifest.TransactionsParquetFile), toParquetTransactions(transactions));

**Hot path:** Runs synchronously inside runArchiveWriter.Finalize for every completed test; for a large run (millions of events, GB-scale JSONL with snapshot payloads) this is a multi-GB heap spike on the controller.

**Verification notes:** Confirmed by direct read: pkg/api/archive_parquet.go:117-138 loads the full transactions.jsonl into []TransactionEvent (readAllJSONLRecords appends every line, no cap), then toParquetTransactions (line 192) builds a second full slice with freshly-allocated re-marshaled JSON (mustJSON on Extractions/ServerTelemetry/Snapshot) before any row is written; writeParquetFile then writes row-by-row anyway, proving streaming is a drop-in change. It runs unconditionally and synchronously in runArchiveWriter.Finalize (archive_writer.go:278) from the run-completion path (execution_backend.go:386, archive failure marks the run FAILED), and the archive writer deliberately streams JSONL during the run precisely to avoid this accumulation — the exporter defeats that at the worst moment. Two precision caveats: (1) the "2x" is an upper bound — plain string fields in the parquet structs share backing arrays with slice 1, so the true duplicate is struct overhead + timestamps + the re-marshaled JSON payloads (near-2x only for snapshot/telemetry-heavy runs), though non-exported heavy fields (ConsoleEntries, NetworkEntries, DOMSummary, ProtocolEvidence) also stay live in slice 1 the whole time; (2) the same pattern is copy-pasted in pkg/distributed/generatorrpc/archive.go:462 (generator-side) and invoked from pkg/api/distributed_archive.go:39 (controller merge), so the finding under-scopes the fix. Once-per-run, not per-request, but O(run size) heap on a K8s pod at finalize is a real OOM risk at the platform's target scale (millions of events).

**Refined fix:** The suggested streaming fix is correct and low-risk, but generalize and apply it in all three places: add a generic streamJSONLToParquet[S, T any](jsonlPath, parquetPath string, convert func(S) T) error that reuses the existing bufio.Scanner loop (keep the 2MB line buffer) and calls pw.Write(convert(item)) per line, then replace the read-all+convert-all pattern for transactions/resources/summaries in pkg/api/archive_parquet.go, pkg/distributed/generatorrpc/archive.go:462, and (via the shared helper) the pkg/api/distributed_archive.go merge path. Extract the per-item conversion out of toParquet* into single-item functions so both slice and stream paths share it. Memory then bounds at the parquet writer's row-group buffer (~128MB default) instead of the whole run. Optionally set pw.RowGroupSize explicitly to make the bound deliberate. Note in passing: the 2MB scanner line cap means a single snapshot-heavy event >2MB already fails the whole export today (bufio.ErrTooLong) — worth a follow-up but out of scope for this fix.

**Risk:** Error mid-stream leaves a partial parquet file (already handled today via WriteStop/Close error paths); memory drops but parquet writer row-group buffering still holds some data.

### 60. Object-store archive upload/materialization buffers whole artifacts in memory

- Location: `pkg/api/archive_backend.go:289`
- Category: memory
- Finder: archive-report
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** ensureArchiveMaterialized downloads each artifact with bucket.ReadAll into a full in-memory byte slice before writing it to disk, and uploadArchiveDirectory/uploadArchiveObjectsToBucket mirror this with os.ReadFile + bucket.WriteAll (lines 188-193, 228-234). For a large run's transactions.jsonl (multi-GB, includes screenshot/HTML snapshot payloads) this spikes controller RAM by the artifact size, per artifact, on the first report/download request after a restart (materialization) and at every run finalize (upload).

**Evidence:** data, err := bucket.ReadAll(ctx, backend.objectKey(record.TestID, artifact))
...
if err := os.WriteFile(localPath, data, 0o644); err != nil {

**Hot path:** ensureArchiveMaterialized guards every archive handler (requireRunArchive), so after a controller restart with an S3/GCS/Azure backend the first access to each big archive loads whole artifacts into heap; uploads run at every test completion.

**Verification notes:** Verified the exact code at pkg/api/archive_backend.go:289-300 (bucket.ReadAll -> os.WriteFile per artifact) and 188-193/228-234 (os.ReadFile -> bucket.WriteAll); no streaming, caps, or singleflight exist. Artifact sizes are realistically multi-GB: transactions.jsonl gets one JSON line per transaction (archive_writer.go:107-158) and each event can embed ErrorSnapshot with full request/response bodies, page HTML, and base64 screenshots (protocol.go:389-404). Callers confirmed: uploadArchiveDirectory at every run finalize (archive_writer.go:292, distributed_archive.go:50, test_manifest.go:581) and ensureArchiveMaterialized guarding ~15 handler paths, but the manifest-on-disk fast path makes downloads once-per-archive-per-restart and everything early-returns on the default filesystem backend, so it is a per-run/cold-start memory spike (controller OOM-kill risk under K8s memory limits for object-store deployments), not a sustained hot path.

**Refined fix:** Original fix is correct; refine it: (1) Downloads: bucket.NewReader(ctx, key, nil) -> io.Copy into "<localPath>.partial" then os.Rename after successful Close of both reader and file; apply to the manifest download at line 267 too, and write manifest.json LAST (after all artifacts) because the fast path at lines 244-247 only stats ManifestPath — today a crash mid-materialization leaves a manifest that makes later calls treat a half-downloaded archive as complete. (2) Uploads: os.Open -> bucket.NewWriter(ctx, key, nil) -> io.Copy, and check the writer.Close() error (gocloud surfaces the commit failure there; s3blob streams multipart with ~5MB parts so memory is constant). (3) Add a per-testID mutex/singleflight around ensureArchiveMaterialized so concurrent first requests to a cold archive do not download the same multi-GB artifacts in parallel.

**Risk:** Streaming writers need explicit Close-error handling for atomicity; consider writing to a temp file then renaming so a partial download is not mistaken for a materialized artifact.

### 61. remapSourceReviewTargets scans every pathMap key per mapping (O(mappings x actions), runs twice per LR import)

- Location: `pkg/convert/enterprisearchive/grouping.go:276`
- Category: algorithmic
- Finder: importers
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** remapSourceReviewTargets sorts all pathMap keys by length, then for each source-review mapping linearly walks the whole key list doing equality + HasPrefix checks until a match. compactSourceHTTPList inserts a pathMap entry for EVERY action — including identity entries where oldPath == newPath (grouping.go:71, :91) — so the key list is proportional to the action count, and mappings that never match (config/metadata targets) scan the entire list. It runs twice per import: from structureSourceControlFlow (control_flow_import.go:336) and compactSourceHTTPActions (grouping.go:33).

**Evidence:** sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
for index := range result.SourceReview.Mappings {
    target := result.SourceReview.Mappings[index].TargetPath
    ...
    for _, oldPath := range keys {
        newPath := pathMap[oldPath]
        if target == oldPath { ... break }
        if strings.HasPrefix(target, oldPath+".") { ... break }
    }
}

**Hot path:** Every enterprise-archive (LoadRunner) import. A 100k-line VuGen export produces tens of thousands of source-review mappings and ~10k action paths: ~10^8 string prefix comparisons per pass, seconds of CPU on the exact multi-MB-import path that already had a frontend freeze fixed.

**Verification notes:** Verified all claims in /Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/convert/enterprisearchive/grouping.go: the O(mappings x keys) scan at line 276 is real and includes an unmentioned per-iteration heap allocation (oldPath+"." concat) plus a map lookup; compactSourceHTTPList inserts identity pathMap entries for every action (lines 71, 91), appendSourceReviewMapping has no cap so mappings scale with source-function count, and config/metadata-targeted mappings never match so they scan the full key list. It runs twice on the web import path (converter.go:642-643), though the control-flow pass skips identity entries so compaction dominates. Cost is negligible for typical imports but reaches ~10^8 allocating iterations (seconds of CPU) on the large VuGen exports this project demonstrably handles, on the synchronous user-facing import request.

**Refined fix:** Keep identity entries (do NOT skip them: under longest-prefix-wins, an identity long-match currently shadows shorter non-identity ancestor matches, and while rebaseCompactionPathMap makes that state unreachable today, the argument is fragile). Instead replace the key scan entirely: (1) exact lookup `if newPath, ok := pathMap[target]; ok { ... continue }`; (2) walk target's dot-boundary prefixes from longest to shortest (iterate strings.LastIndex(target[:i], ".") downward), looking each prefix up in pathMap and rewriting on first hit. This preserves longest-prefix-wins by construction, makes identity hits harmless self-assignments, removes the sort.Slice, and drops complexity to O(mappings x segments) with zero allocations in the loop.

**Risk:** Must preserve longest-prefix-wins semantics; ancestor-walk from the longest prefix downward keeps that. Identity-entry skip changes nothing observable since remap would rewrite the same value.

### 62. sample source-location pass recounts full-content line totals and prefix newlines per exchange

- Location: `pkg/convert/sample/sourceview.go:82`
- Category: algorithmic
- Finder: importers
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** sampleSourceLocations' init loop calls maxLine(content, 1) and maxLine(responseContent, 1) — each a strings.Count over the ENTIRE content — once per exchange, even though the values are loop-invariant. Then assignURLLocations/assignDelimitedContentLocations/assignResponseLocations call sourceview.LineForOffset(content, found) per exchange, which rescans the file prefix from offset 0 each time even though matches advance monotonically. Combined cost is O(exchanges x content).

**Evidence:** for index := range exchanges {
    locations[index] = sampleSourceLocation{
        ...
        EndLine:           maxLine(content, 1),          // full strings.Count per exchange
        ...
        ResponseEndLine:   maxLine(responseContent, 1),  // full strings.Count per exchange
    }
}
...
locations[index].StartLine = sourceview.LineForOffset(content, found) // prefix rescan per exchange

**Hot path:** Runs on every HAR / raw-HTTP / rest_json sample import. A 20MB HAR with 2,000 entries does 4,000 full-content Counts (~80GB scanned) in the init loop alone, plus ~2,000 prefix scans averaging 10MB each in assignURLLocations.

**Verification notes:** Confirmed both mechanisms in pkg/convert/sample/sourceview.go: the init loop (lines 78-87) recomputes loop-invariant maxLine(content)/maxLine(responseContent) (full strings.Count scans) per exchange, and assignURLLocations/assignDelimitedContentLocations/assignResponseLocations call sourceview.LineForOffset (strings.Count over content[:offset]) per exchange despite monotonic offsets — O(exchanges x content) total, with no caching anywhere (assignCurlLocations already hoists correctly, showing the intended pattern). Reachable at scale: har.Convert routes (import/har, salesforce, browser-recorder HAR->HTTP) pass the full HAR string and are capped at 50 MiB (harImportMaxBodyBytes, middleware.go:434), so a 20MB/2000-entry HAR (~60GB scanned, ~1-5s CPU) up to ~375GB at the cap (tens of seconds) stalls the synchronous import handler; the /import/sample route is capped at 5 MiB so it is milder there. Not runtime-hot (no effect on running load tests), so impact is medium import-time UX, not critical.

**Refined fix:** Hoist `endLine := maxLine(content, 1)` and `responseEndLine := maxLine(responseContent, 1)` above the init loop in sampleSourceLocations. In assignDelimitedContentLocations and assignResponseLocations, track (lastOffset, lastLine) starting at (0, 1) and compute each line as lastLine += strings.Count(content[lastOffset:pos], "\n") — offsets there are strictly monotonic. In assignURLLocations use the same tracker but guard the backward case: the full-content fallback at line 129 (strings.Index(content, needle)) can yield found < lastOffset, so when that happens recompute via sourceview.LineForOffset(content, found) and reset the tracker to (found, thatLine). Values stay bit-identical to today; covered by existing sample source-review tests.

**Risk:** Low — line numbers must match current values exactly; covered by sample source-review tests.

### 63. Source-review Redact runs 6 regexes over full multi-MB file content before truncating to 60k chars

- Location: `pkg/convert/sourceview/source_review.go:189`
- Category: cpu
- Finder: importers
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** buildFiles calls Redact(files[name]) — six case-insensitive alternation regexes with ReplaceAllString over the ENTIRE original content — and only afterwards truncates to MaxFileChars (60,000). For preserved import sources like a 20MB original HAR, that is ~120MB+ of regex scanning (regexes with (?i) alternations run well below memchr speed in Go) per file, up to MaxFiles=20 files, to produce at most 60KB of displayed text each.

**Evidence:** for _, name := range names {
    content, redacted := Redact(files[name])   // 6 regex passes over full content
    truncated := false
    if len(content) > MaxFileChars {
        content = content[:MaxFileChars] + "\n\n[truncated for source review]"

**Hot path:** Runs for every importer that builds a source review (sample/HAR, enterprise archive, WSDL, JMeter…) on every import; preserved originals are the same multi-MB payloads users upload.

**Verification notes:** Verified at pkg/convert/sourceview/source_review.go:189 — Redact runs 6 precompiled (?i)-alternation regexes via ReplaceAllString over the full content (each pass also allocates a full string copy even with zero matches), and truncation to 60k happens only afterwards. Upstream handlers permit 50MB HAR/JMeter and 100MB enterprise-archive bodies (pkg/api/middleware.go:430-436) and every importer (jmeter converter.go:274, sample converter.go:342, wsdl/devweb/postman/openapi/asyncapi/k6, enterprisearchive) feeds full originals to Build, so worst case is ~300MB of regex scanning plus ~300MB alloc churn per file to display 60KB. Path is import-time (user-initiated, synchronous in the handler), not sustained load — real user-visible latency of seconds on large imports but not a classic hot path. Aggravator: the sample/HAR path already fully redacts upstream (correlate_redact.go:155 incl. scrub.HAR), making Build's pass a duplicate full scan there.

**Refined fix:** Truncate first to MaxFileChars+4KB margin, run Redact on that slice, then re-truncate to exactly MaxFileChars (so a straddling secret's [REDACTED] remnant doesn't extend the kept region and the flag comes from the displayed slice). Keep OriginalBytes from files[name] as today (line 199 already does). Safe because content past 60k is discarded in all cases — no caller persists Redact's full output (verified: buildFiles stores only the truncated copy; sample path ORs in its own upstream redaction flag at sample/converter.go:344-348, and enterprisearchive's redactSourceSourceContent wrapper is unused outside tests). If whole-file Redacted signaling is ever wanted, a single combined re.MatchString pre-scan (no replace, no alloc, stops at first match) is far cheaper than 6 ReplaceAllString passes. Optional follow-up outside this finding: the sample/HAR path double-redacts (redactSourceReviewSecrets already scrubs full content, O(exchanges x size)); skipping Build's Redact for pre-redacted files would save another full scan.

**Risk:** Redacted flag may change for files whose only secrets lie beyond the 60k cut; displayed content is unaffected since that region is discarded anyway — confirm no caller persists the redacted full content (buildFiles only stores the truncated copy).

### 64. No in-flight gate on goal-controller scaling: concurrent consumeMetrics goroutines can launch duplicate fleet-wide scale storms

- Location: `pkg/api/distributed_stream.go:419`
- Category: concurrency
- Finder: distributed
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** evaluateGoalController runs under tr.mu and gates re-scaling only via shouldEvaluateScale (goal_controller.go:466-471), which checks state.lastScaleAt. But lastScaleAt is written only at the END of applyGoalControllerUpdate (testexecution.go:970), after all the blocking probe/adjust RPCs complete. With N generators each delivering batches every second, several consumeMetrics goroutines can pass the 5s gate while the first scale operation is still in flight, and each then runs the full serial ScaleVUsers fan-out across all targets for all groups. The result is duplicated RPC storms (N goroutines x 2M RPCs) and VUser-count thrashing where later duplicates recompute shards from load snapshots mid-mutation.

**Evidence:** if runGoalUpdate {
    applyGoalControllerUpdate(tr, test, live, goalGroupVUsers, goalUpdate, func(groupName string, target int) error {
        return b.ScaleVUsers(tr, test, groupName, target)
    })
}

**Hot path:** Every goal-oriented distributed run whenever TPS is outside the 5% band — precisely the high-load moments when many generators are streaming batches concurrently.

**Verification notes:** Verified every link in the chain: per-target consumeMetrics goroutines (distributed_stream.go:84) evaluate the goal controller under tr.mu but release the lock before applyGoalControllerUpdate (lines 403-423, explicitly documented), lastScaleAt is written only at the end of the blocking fan-out (testexecution.go:970), and no in-flight flag or scale mutex exists anywhere in pkg/api (goalControllerState has only 4 fields; grep for scaleMu/inFlight/singleflight empty). ScaleVUsers fans out serial probe+adjust RPCs per target per group with a default 10s timeout each, and shouldEvaluateScale returns true while lastScaleAt is nil — so at goal-run start (TPS below band) all N goroutines pass the gate and launch concurrent duplicate fan-outs almost deterministically; remoteGroupShards writes are last-writer-wins so bookkeeping can desync from actual generator state. Impact is medium not high: extra RPC volume is modest for realistic fleets and the local path is single-goroutine, but VUser thrash and hold-down bypass degrade goal-run fidelity, the platform's core promise.

**Refined fix:** The suggested fix is directionally right; two refinements. (1) Stamp the gate at decision time: inside the existing tr.mu critical section in applyRemoteMetricsBatch (distributed_stream.go:411-416), when goalUpdate.ShouldScale is true, set test.goalControl.lastScaleAt = &live.Timestamp before unlocking. Note this is behavior-equivalent on failure to today's code — applyGoalControllerUpdate already stamps lastScaleAt even when a scale RPC fails (the break at testexecution.go:950 falls through to line 970) — so no retry regression is introduced. (2) A timestamp alone does not cover fan-outs lasting longer than the 5s window (each probe/adjust RPC has a 10s timeout, so a slow generator easily exceeds 5s): add a scaleInFlight bool to goalControllerState, set it in the same critical section, have shouldEvaluateScale return false while it is set, and clear it under tr.mu at the end of applyGoalControllerUpdate on all exit paths (success and failure). Apply the same claim-at-decision pattern in evaluateGoalControllerLocked (metrics.go:629) for uniformity, though the local single-ticker path is not actually exposed.

**Risk:** If the claimed scale attempt fails immediately, the next evaluation window is delayed by up to 5s; acceptable versus concurrent thrashing.

### 65. Run() placement dials, probes, and distributes multi-MB scripts to every generator serially under one shared 10s deadline

- Location: `pkg/api/distributed_execution_backend.go:534`
- Category: network
- Finder: distributed
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** The distributed Run path creates a single context with b.timeout (default 10s) and uses it for ALL placement work: serial dial + heartbeat probe per target (lines 549-557), then per group per shard a serial DistributeScript (uploading the full JSON-marshaled compiled script — multi-MB for 100k-line imports) and AssignWork (lines 588-639). Work is strictly sequential, so total start latency grows O(N_targets + shards*script_size), and every unreachable/slow target consumes up to ~3s of the shared budget (MinConnectTimeout) before its probe fails. On large fleets or large scripts the shared 10s budget can be exhausted mid-loop, failing the run with a deadline error even though every generator is healthy.

**Evidence:** ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
defer cancel()
...
for _, target := range allTargets {
    client, err := b.dial(ctx, target)
    ...
    loadByTarget[target] = b.probeGeneratorLoad(ctx, target, client)

**Hot path:** Every distributed test start; cost scales linearly with fleet size and imported-script size, and the fixed shared deadline turns scale growth into hard start failures.

**Verification notes:** Verified every claim in code: Run() at distributed_execution_backend.go:534 shares one b.timeout ctx (default 10s, APEXLOAD_GENERATOR_TIMEOUT) across serial dial+heartbeat-probe per target and serial per-shard DistributeScript (full json.Marshal of the compiled script, re-sent per target) + AssignWork; MinConnectTimeout=3s confirmed at generatorrpc/transport.go:215, and no client/probe caching exists across runs. One nuance: dial is lazy (no WithBlock), so connection-refused targets fail in ms — the ~3s budget burn applies to blackholed/unrouteable targets (realistic during dns:// autoscale-down, which SetTargets explicitly supports), where 3 such targets consume ~9s of the 10s budget and fail the run despite healthy generators. This runs on every distributed test start (the platform's core workflow, incl. scheduler-driven runs) and cost scales with fleet size and script size, but it is a control-plane start path, not a per-iteration loop — hence medium, not high.

**Refined fix:** Split the budget and parallelize: (1) probe all targets concurrently, each with its own per-target timeout of ~MinConnectTimeout+1s (or lower MinConnectTimeout for placement dials), so unreachable targets cost wall-clock max(3s) instead of sum; (2) distribute scripts concurrently with bounded fan-out (~8 in flight) and a separate deadline sized to payload (e.g. base 10s + per-MB allowance) rather than the shared placement budget — marshal each group's script once (already done) and key uploads by ContentHash; consider a hash-first "already have it?" RPC since the generator already tracks Cached/ContentHash, avoiding repeat multi-MB pushes to warm generators. Keep AssignWork per group ordered or, if parallelized, add explicit revocation on partial failure — today cleanup() only closes connections and does not retract assignments already accepted by generators, so parallel AssignWork must not widen that pre-existing gap. Also raise the generator server's MaxRecvMsgSize (default 4MB) or chunk DistributeScript, since large compiled imports can exceed it and fail regardless of timeout.

**Risk:** Parallel script pushes briefly multiply controller egress bandwidth; bound concurrency (e.g. 8 in flight) to avoid saturating the controller NIC.

### 66. Generator rebuilds the full proto batch — re-encoding every group's retained transactions — on each per-group publish tick under the service mutex

- Location: `pkg/distributed/generatorrpc/service.go:1141`
- Category: allocations
- Finder: distributed
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** publishMetricsWithAggregates holds s.mu while metricsBatchLocked concatenates the retained transaction slices of ALL groups (up to 1024 each) and metricsBatchForMetrics converts every one to a fresh pb.TransactionMetric (plus nested ServerTelemetry and snapshot JSON). Since runExecutionPublisher ticks once per second per group, a pod hosting G groups performs G full-batch rebuilds per second, each O(G*1024) proto allocations — G^2*1024 conversions/sec — and each batch redundantly re-ships every sibling group's unchanged window transactions over the stream G times. All of this happens while holding the service-wide mutex that AssignWork/AdjustVUsers/heartbeat handlers need, and it competes for CPU with the load generation itself.

**Evidence:** transactions := make([]TransactionMetric, 0)
for _, groupTransactions := range assignment.groupTransactions {
    transactions = append(transactions, groupTransactions...)
}
... (caller holds s.mu: batch := s.metricsBatchLocked(testID) at line 1037)

**Hot path:** Once per group per second on every generator for the whole run; quadratic in group count and directly steals CPU from VUser execution on the generator pod.

**Verification notes:** Verified every element in service.go: s.mu.Lock at line 1004 held through metricsBatchLocked (1037, unlock 1039); the all-groups concat at 1140-1143; fresh pb.TransactionMetric + nested ServerTelemetry + snapshotJSON json.Marshal per tx (1192-1248); 1024/group retention cap (348-349); 1s publishInterval with one publisher goroutine per group (448, 1387, 1397) — so G co-located groups yield G rebuilds/sec of a G*1024 batch, all under the service-wide write lock that AssignWork/AdjustVUsers/heartbeat contend on, plus G-fold redundant re-ship of unchanged sibling windows (controller replaces the stored batch per target at pkg/api/distributed_stream.go:353, confirming full-snapshot contract). Honest discount: with G=1 per pod there is no redundancy at all — the waste only manifests when multiple groups of one test co-locate on a generator, but that is the normal case for LR-style multi-group scenarios on small fleets (importers split scripts into per-protocol/per-channel groups), so medium is fair; it is not high because the 1024 cap bounds each batch and typical G is single-digit.

**Refined fix:** Do NOT switch to per-group delta batches — the controller replaces the stored batch per generator target (pkg/api/distributed_stream.go:353), so partial batches would erase sibling groups' data. Two safe steps instead: (1) Hoist the merge + proto conversion out of s.mu: snapshot assignment.groupTransactions map values, groupStats/groupTelemetry/groupAggregates under the lock, unlock, then build the batch — safe because each group's slice is replaced wholesale at line 1034 (never mutated in place). This alone removes the lock-contention half. (2) Eliminate the G-fold redundancy without changing the wire contract: preferably replace the G per-group publisher goroutines with a single per-test publisher that drains all of s.executions[testID] each tick and emits one full-snapshot batch per second per test (move publisher lifecycle from groupExecution ctx to the test assignment lifecycle; keep the final out-of-band flush per group in the engine-done goroutine at line 1388-1393). Alternative if lifecycle refactor is unwanted: cache each group's already-encoded []*pb.TransactionMetric next to groupTransactions, invalidated only when that group publishes, so batch assembly concatenates cached proto slices (aliasing across batches is fine — batches are only marshaled, never mutated).

**Risk:** The controller currently expects each batch to be a full test snapshot (it replaces the stored batch per target); the aggregation contract must keep merging per-group windows correctly if batches become partial.

### 67. Monitoring page fires 13 parallel API requests every 5 seconds regardless of which panels are visible

- Location: `dashboard/src/components/MonitoringPage.tsx:402`
- Category: network
- Finder: dashboard-render
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** The refresh callback awaits Promise.all over 13 endpoints — health, system inventory, restart capabilities, readiness, services, schedule health, run calendar (with capacity computation), APM profiles, monitor packs, fleet alerts, auth audit events, fleet capacity, and fleet provisioner — and setInterval(refresh, 5000) repeats the full burst every 5s (line 458), plus again on every fleet_event WS signal. Most of these (APM integration profiles, monitor packs, restart capabilities, audit events) change rarely, and the run-calendar query recomputes a full visible-range calendar with capacity on the server each tick. Thirteen new response objects also mean 13 setState calls producing a full re-render of the 2289-line page every 5s.

**Evidence:** const [healthRes, inventoryRes, restartCapsRes, readinessRes, servicesRes, scheduleHealthRes, runCalendarRes, apmProfilesRes, monitorPacksRes, fleetAlertsRes, fleetAuditRes, fleetCapacityRes, provisionerRes] = await Promise.all([ ... ])
...
const interval = setInterval(refresh, 5000)

**Hot path:** Monitoring is typically left open on ops screens during load tests; ~156 requests/minute per open tab, multiplied across viewers, landing on the same controller that is orchestrating the test.

**Verification notes:** Verified at dashboard/src/components/MonitoringPage.tsx:402/458/478 — 13-way Promise.all on a 5s interval plus fleet_event refresh, fetching all endpoints even though only the active category tab's cards render, with no client dedup/cache layer. Server side is worse than claimed: 4 of the 13 endpoints (inventory, services at system_services.go:32, run calendar at run_calendar.go:153, fleet capacity at handlers_fleet.go:214) each independently call s.systemInventory(), which has no read-side cache and sequentially gRPC-dials/probes every configured generator (distributed_inventory.go:13) plus a K8s ListPods per call — so ~4xN generator probes every 5s per visible tab on the orchestrating controller. Two corrections: a document.visibilityState guard (line 398) already stops hidden-tab polling, and React 18 batching means one re-render per tick, not 13 — but the re-render still fires every 5s on unchanged data due to fresh object identities.

**Refined fix:** Highest leverage is server-side: add a short-TTL (2-5s) read path to the existing generator-inventory cache (setCachedGeneratorInventory plumbing already exists in capacity.go) so the four endpoints that call s.systemInventory() per tick share one probe sweep, and probe generators concurrently instead of sequentially in distributedExecutionBackend.SystemInventory. Client-side: fetch per active category tab (fast 5s loop for health/readiness/services/inventory/fleet-alerts; calendar+capacity only when the Schedules tab is active or on a 30-60s slow loop; APM profiles, monitor packs, restart capabilities, and audit events on the slow loop or on tab activation), keep the fleet_event-triggered refresh scoped to the fleet endpoints, and have lastRefreshed reflect the fast loop. Also memoize or shallow-compare poll results before setState to skip the no-change re-render of the 2289-line component.

**Risk:** Panels showing slow-loop data may lag up to their interval; the lastRefreshed indicator should reflect the fast loop.

### 68. Results table renders every completed run as a DOM row with no virtualization or pagination

- Location: `dashboard/src/components/ResultsPage.tsx:1434`
- Category: rendering
- Finder: dashboard-render
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** sortedRows contains one row per completed local run plus federation remote items, and the table body maps all of them into the DOM (`{sortedRows.map(row => ...)}`) with no windowing, paging, or cap. Combined with the 10s listTests poll (line 434) that replaces the tests array identity each tick, the full row set is reconciled and re-rendered every 10 seconds, and every search keystroke re-filters and re-renders the entire set. Row count grows monotonically with run history.

**Evidence:** {sortedRows.map(row => {

**Hot path:** Results is the main archive-browsing page; teams accumulate thousands of runs, so DOM size and reconciliation cost grow without bound and re-run on every 10s poll tick.

**Verification notes:** Verified all three legs: (1) ResultsPage.tsx:1434 maps sortedRows straight into <tr> rows with no cap/windowing/pagination and no memoized row component; (2) the 10s setInterval (line 434) calls setTests with a fresh array each tick AND toggles setResultsRefreshing true/false, guaranteeing full re-renders that cascade through the sourceRows/filteredRows/sortedRows memo chain; (3) the backend (pkg/api/handlers.go:89-121 → pkg/api/tests.go ListTestsByContext) returns ALL tests for the application with no limit or retention pruning, so row count and the 10s poll payload grow unbounded. Frontend-only jank, so medium (not high) is the right severity — negligible at hundreds of runs, real at thousands.

**Refined fix:** Phase the fix cheapest-first: (1) fingerprint the listTests response (e.g., hash of id/status/archive fields) and skip setTests on no change, and stop toggling resultsRefreshing for background ticks — that toggle alone forces 2 renders per tick even with unchanged data; (2) extract a React.memo'd ResultRow with stable callback props so unchanged rows skip VDOM rebuild; (3) prefer simple client-side pagination (~100 rows/page) over @tanstack/react-virtual — table virtualization requires transform/padding-row layout hacks that fight the existing bounded-table CSS, while pagination keeps select-all/compare semantics correct by operating on filteredRows data rather than DOM; (4) long-term, add server-side pagination to GET /tests, which also bounds the growing 10s poll payload, but audit the 6+ other listTests callers (TestHub, DashboardPage, SettingsPage, CommandCenterPrototype, useScriptsLibraryModel) first.

**Risk:** Virtualization interacts with the existing bottom-bounded-table sizing and row expansion; select-all/comparison selection semantics must still cover off-screen rows.

### 69. Every metrics_update WebSocket message re-renders the entire AppShell tree

- Location: `dashboard/src/App.tsx:259`
- Category: rendering
- Finder: dashboard-data
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** handleWSMessage stores per-test metrics history in AppShell root state: setMetricsHistory creates a new top-level object per message. The backend broadcasts one metrics_update per running test every MetricsIntervalSeconds (>=1s, pkg/api/metrics.go:591-617 and :501), so with N concurrent tests in the active project the whole AppShell re-renders N times per second. None of the children are memoized (Sidebar.tsx:187, ScriptsPage.tsx:121, TestExecution.tsx:105 are plain function components), so the 661-line Sidebar, the topbar, and whichever heavy page is mounted (including the CodeMirror-backed script editor, already known to struggle with ~98k-line documents) re-render on every tick even when viewing pages that never use metricsHistory. The metricsHistory and testStatuses maps also never prune keys for finished tests, retaining 300 points per test for the session lifetime.

**Evidence:** setMetricsHistory((prev) => {
  const existing = prev[testId] || []
  const updated = [...existing, point].slice(-300)
  return { ...prev, [testId]: updated }
})

**Hot path:** 1 message/sec per running test in the project, for the entire duration of every load test; a user editing a large imported script or browsing TestHub while 3-5 runs execute gets 3-5 full-app re-renders per second for hours.

**Verification notes:** Confirmed end-to-end: metricsHistory/testStatuses are AppShell root state (App.tsx:195-196), setMetricsHistory at :259-263 matches the evidence verbatim, the backend ticks one metrics_update per running test per MetricsIntervalSeconds (metrics.go:591/501, default 1s per settings.go:535), the WS is project-filtered server-side (websocket.go:146), and Sidebar/ScriptsPage/TestExecution/DashboardPage are un-memoized plain components rendered inline in AppShell — so N running tests = N full-shell render passes/sec for the run duration, and neither map ever prunes keys. The finding overstates severity slightly: ResultsPage and TestHub ARE memo()-wrapped (ResultsPage's props are stable so it's shielded; TestHub's memo is defeated by the non-useCallback navigateToNewTestForScenario prop at App.tsx:342), CodeMirror content is ref-mounted so it doesn't re-render via React, and the most common case (one test, dashboard page mounted) re-renders a page that legitimately consumes the data. Real, continuous, hours-long UI CPU waste in the multi-run + editor/TestHub scenario, but bounded per-tick cost with no correctness or unbounded-memory blowup (history capped at 300 points/test) — honest impact is medium.

**Refined fix:** Original direction is correct; refine as: (1) move metricsHistory into a module-level external store written directly from handleWSMessage (no AppShell setState) and read via useSyncExternalStore with per-testId selectors in DashboardPage/TestExecution/CommandCenterPrototype — this removes metrics ticks from AppShell entirely; keep low-frequency testStatuses in AppShell state if simpler (status transitions are rare and the topbar needs them). (2) If a store is too invasive now, a cheap interim that captures most of the win: buffer incoming points in a ref and flush with one coalesced setMetricsHistory every ~250-500ms (caps renders at ~2-4/sec regardless of concurrent-test count), plus memo-wrap Sidebar/ScriptsPage/TestExecution — which requires first stabilizing the plain-arrow callbacks in AppShell (navigateToNewTestForScenario App.tsx:342, navigateToScriptEdit :337, navigateToScenarioBuilder :341, navigateToScheduleForDate :346); note the existing TestHub memo (TestHub.tsx:3811) is currently defeated by navigateToNewTestForScenario, so that fix pays off twice. (3) Prune metricsHistory[testId]/testStatuses[testId] on terminal status with a short grace delay (DashboardPage shows recently-finished runs, so do not delete synchronously). Do NOT gate accumulation on 'testId currently on-screen' as the finding alternatively suggests — DashboardPage graphs need history for all project runs and users expect history when navigating to a mid-flight run.

**Risk:** DashboardPage/TestExecution/CommandCenterPrototype receive metricsHistory/testStatuses as props today; refactoring to a store changes their data flow and needs re-verification of live-graph updates and status badge overrides.

### 70. Catch-all manualChunks 'vendor' pulls three.js (~600KB min) into the eager startup bundle

- Location: `dashboard/vite.config.ts:53`
- Category: bundle
- Finder: dashboard-data
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** manualChunks returns 'vendor' for every node_modules module not matching recharts/codemirror. three (package.json dependency, ~600KB minified / ~150KB gzip) is only imported by LoadImpact3DHeatmap.tsx, reached exclusively through the React.lazy TestExecution/TestExecutionCharts route — but the catch-all merges it into the same 'vendor' chunk as react/react-dom, which the entry chunk statically imports. Result: every user downloads and parses three.js at app startup even if they never open the 3D load-impact heatmap, defeating the route-level code splitting App.tsx carefully sets up.

**Evidence:** manualChunks(id) {
  if (!id.includes('node_modules')) { return undefined }
  if (id.includes('/recharts/')) { return 'vendor-recharts' }
  if (id.includes('/@codemirror/') || ...) { return 'vendor-codemirror' }
  return 'vendor'
}

**Hot path:** Paid on every cold load of the dashboard by every user (startup time-to-interactive), including the Wails desktop shell and operators opening the dashboard mid-incident to watch a live run.

**Verification notes:** Verified end-to-end against the committed production build: three is imported only by LoadImpact3DHeatmap.tsx, reachable solely through React.lazy routes (TestExecution, CommandCenterPrototype), yet the manualChunks catch-all at vite.config.ts:53 places it in vendor-CfA4hXb4.js (847KB raw / 236KB gzip) which the entry chunk statically imports and index.html modulepreloads — confirmed by WebGLRenderer signatures inside pkg/api/static/assets/vendor-CfA4hXb4.js. The finding overstates three's size (~600KB min / 150KB gzip claimed; actual three.module.min.js is 360KB / 87KB gzip, so ~a third of the eager vendor payload). Impact is medium rather than high: it is a cold-start/post-deploy TTI tax (hashed assets cache afterwards; V8 code caching mitigates repeat parse), not an ongoing runtime cost — but it hits every first load including the Wails desktop shell and defeats the route splitting App.tsx sets up.

**Refined fix:** In dashboard/vite.config.ts manualChunks, before the catch-all, add: if (id.includes('/node_modules/three/')) { return 'vendor-three' } — match the full /node_modules/three/ segment (not /three/) so future packages like three-stdlib don't get swept in. Prefer this targeted line over deleting the catch-all (smaller chunk-graph diff). Rollup will make vendor-three a dependency of only the lazy TestExecution/CommandCenterPrototype chunks, so it loads on demand. After building, regenerate the embedded copy under pkg/api/static (index.html's modulepreload list is auto-generated and will drop the three bytes from the eager set); verify no circular-chunk warnings and that vendor-three is absent from index.html modulepreloads.

**Risk:** Chunk-graph reshuffle changes file names/count; verify the Go static-asset embedding (pkg/api/static) and any preload hints still work, and that no circular chunk warnings appear.

### 71. Topbar run pollers never pause when hidden, restart on every WS status event, and double-fetch on desktop

- Location: `dashboard/src/App.tsx:839`
- Category: polling
- Finder: dashboard-data
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** Both 10s listTests pollers (topbar runs at App.tsx:808-839, desktop run-count at :841-869) lack the document.visibilityState gate that useExecutionTestPolling and DashboardPage have, so backgrounded tabs keep fetching the heavy unpaginated collection forever, even when zero runs are active. Both effects also list `testStatuses` in their deps; setTestStatuses (App.tsx:282) allocates a fresh object on every status-bearing WS event even when the value is unchanged, so each lifecycle event tears down the interval and triggers an immediate extra refetch in both effects. On desktop the second effect fetches ALL projects' tests (testApi.listTests() unfiltered) 10s apart from the project-scoped one — two full-collection fetches per cycle.

**Evidence:** void loadTopbarRuns()
const interval = window.setInterval(() => { void loadTopbarRuns() }, 10000)
...
}, [activeApplicationId?.id, activeProject?.id, testApi, testStatuses])

**Hot path:** Runs for the lifetime of every dashboard session, including hidden/backgrounded tabs; enterprise users routinely leave the dashboard open, so idle tabs collectively generate continuous full-collection load on the controller during test campaigns.

**Verification notes:** Verified all three mechanisms in App.tsx:808-869: no visibility gate (unlike useExecutionTestPolling.ts:39-66 and DashboardPage.tsx:555 which have it), testStatuses in both dep arrays with a non-bailing setter at App.tsx:282, and an unfiltered listTests() in the desktop effect (Wails shell only). The payload is heavier than claimed: handleListTests (pkg/api/handlers.go:89) is unpaginated and maskManagedTest does not strip ManagedTest.History (up to 3600 LiveMetrics points per test, hydrated for ALL persisted tests at boot via persistence.go:155-175), so each 10s poll can serialize multi-MB responses. Two softening facts: only lifecycle broadcastEvent messages carry top-level status (metrics_update does not, so interval churn is ~6-8 events per run, not per-second), and browsers throttle hidden-tab intervals to ~1/min, so 'forever at 10s' overstates the backgrounded rate — medium, not high.

**Refined fix:** Original fix is sound with two corrections. (1) Keep the visibility gate + visibilitychange catch-up (copy useExecutionTestPolling verbatim) and the setTestStatuses bail-out (return prev when prev[testId] === msg.status — pure win, also cuts App re-renders). Read testStatuses via a ref inside load() and replace the dep with an explicit statusVersion counter bumped in the WS handler only on actual transitions, preserving prompt chip refresh. (2) Do NOT blindly derive the desktop count from topbar data: the desktop effect intentionally counts ALL projects (unfiltered listTests) while the topbar fetch is project-scoped — deriving from topbar data silently narrows the count to the active project. Either accept that scope change deliberately or keep one shared global fetch on a single visibility-gated interval. Bigger win worth a follow-up: the list endpoint serializes each test's full History (up to 3600 LiveMetrics points); a summary/fields param or stripping History in maskManagedTests for list responses would cut the dominant cost for every poller, not just these two.

**Risk:** The testStatuses dep currently doubles as a 'refresh list on lifecycle change' trigger; replace it with an explicit signal for status transitions (not every message) so chips still update promptly when runs start/stop.

### 72. DashboardPage imported eagerly, forcing the recharts vendor chunk into the startup path

- Location: `dashboard/src/App.tsx:10`
- Category: bundle
- Finder: dashboard-data
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** Every routed page is code-split via React.lazy (ScriptsPage, TestExecution, ResultsPage, etc. at App.tsx:68-82) except DashboardPage, which is a static import. DashboardPage.tsx imports recharts at module top-level (AreaChart, Area, XAxis... line 3), so the entry chunk statically depends on the vendor-recharts manualChunk (~450KB min / ~140KB gzip) plus DashboardPage's own 1451 lines and its panel dependencies. Users deep-linking to #/scripts, #/test/<id>, or the recorder panel — and the chrome-less recorder popup which returns before rendering DashboardPage at all — still download and parse recharts before first paint.

**Evidence:** import DashboardPage, { type DashboardSnapshotAudience } from './components/DashboardPage'
...
const ScriptsPage = lazy(() => import('./components/ScriptsPage'))

**Hot path:** Cold-start of every session; particularly wasteful for the recorder companion window (view.page === 'recorder-panel' returns early) and script-editor deep links where charts are never rendered.

**Verification notes:** Verified App.tsx:10 eagerly imports DashboardPage while all 15 other pages are lazy (App.tsx:68-82); DashboardPage.tsx:3 imports recharts top-level (plus LoadComparisonPanel, also recharts), and vite.config.ts:41 puts recharts in a vendor-recharts manualChunk that becomes a static dependency of the entry. I confirmed exclusivity: no other eager import of App.tsx/main.tsx (Sidebar, TablePaginationManager, AuthGate, DesktopHome, etc.) reaches recharts, so the split genuinely removes ~140KB-gzip of vendor JS plus the 1451-line page from cold-start of every session, including the chrome-less recorder popup which returns at App.tsx:873 before ever rendering DashboardPage; the render site (App.tsx:1015) is already inside a Suspense boundary (line 998), making the fix low-risk.

**Refined fix:** In App.tsx: (1) replace line 10 with `import type { DashboardSnapshotAudience } from './components/DashboardPage'` — type-only imports are erased at build time, so no separate lightweight type module is needed; (2) add `const DashboardPage = lazy(() => import('./components/DashboardPage'))` next to the other lazy consts at lines 68-82. The render site at line 1015 is already wrapped by the Suspense at line 998, so no new boundary is required. Do NOT use an unconditional module-level import() preload (the finding's risk mitigation) — that re-downloads recharts on every route including the recorder popup, negating the win; if the landing-view fallback flash matters, preload conditionally after parsing the initial hash: `if (initialView.page === 'dashboard') void import('./components/DashboardPage')`.

**Risk:** Brief loading fallback flash on the default landing view; mitigate by kicking off the dynamic import eagerly (module-level import() preload) while still splitting the chunk.

### 73. Render-blocking Google Fonts stylesheet fetched from external CDN on every load

- Location: `dashboard/index.html:10`
- Category: network
- Finder: dashboard-data
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** index.html loads Inter via a render-blocking <link> to fonts.googleapis.com. Every cold load pays an extra DNS+TLS+fetch round trip to a third-party host before first paint. In the deployment modes this product targets — Wails desktop app, air-gapped/enterprise clusters serving the dashboard from pkg/api/static, SSRF-hardened production environments — the CDN is often unreachable, and the browser stalls first render until the request times out or fails before falling back to system fonts.

**Evidence:** <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&display=swap" rel="stylesheet">

**Hot path:** Every page load in every environment; worst in offline/desktop/air-gapped deployments where the external fetch hangs until timeout, delaying first paint by seconds.

**Verification notes:** Verified the render-blocking Google Fonts link at dashboard/index.html:10 and confirmed it ships in the served bundle (pkg/api/static/index.html:10) and the Wails desktop webview (desktop/main.go serves the same embedded static dir); CSP at pkg/api/middleware.go:478/480 explicitly allow-lists both fonts hosts so the fetch really goes out, and no self-hosted Inter (.woff2/@font-face/@fontsource) exists anywhere in the repo. Impact caveat: the Google Fonts CSS is client-cached ~24h, so online users pay ~100-400ms only on cold loads; multi-second first-paint stalls occur specifically when egress is silently dropped (air-gapped clusters, offline desktop) — which are explicitly targeted deployment modes, justifying medium. It is on the first-paint critical path of every dashboard session, though once per session, not per interaction.

**Refined fix:** Add `@fontsource/inter` (weights 400/500/600/700/800, latin subset) or `@fontsource-variable/inter` to dashboard/package.json and import it in src/main.tsx — Vite fingerprints and bundles the woff2 into pkg/api/static automatically (OFL license file ships inside the package). Delete lines 8-10 (both preconnects and the stylesheet link) from dashboard/index.html. ALSO tighten CSP in pkg/api/middleware.go: remove https://fonts.googleapis.com from style-src (line 478) and https://fonts.gstatic.com from font-src (line 480) — nothing else in the repo references those hosts. Verify index.css:414's 'Inter' family name matches the @fontsource family and rebuild the embedded dashboard so pkg/api/static/index.html is regenerated.

**Risk:** Minimal — verify weights 400-800 are included and licensing attribution (OFL) is retained.

### 74. Archive writers JSON-encode every result to an unbuffered os.File under one global mutex

- Location: `pkg/api/archive_writer.go:153`
- Category: io
- Finder: go-crosscutting
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** runArchiveWriter.RecordResult takes w.mu.Lock() and encodes each TransactionEvent with a json.Encoder created directly on the *os.File (archive_writer.go:29-35, no bufio) — so every result costs at least one write() syscall performed while holding the single writer mutex. All VUsers' results serialize through this one lock+syscall pair. The generator-side twin pkg/distributed/generatorrpc/archive.go:279-311 (wired into every VUser via combineSinks at service.go:1371) has the identical pattern.

**Evidence:** w.mu.Lock()
defer w.mu.Unlock()
event := TransactionEvent{...}
if err := w.txEncoder.Encode(event); err != nil {  // txEncoder wraps the raw *os.File — one syscall per result, under the global lock

**Hot path:** Called from vu.ResultSink.RecordResult (pkg/engine/vuser_execute.go:30) for every action result of every VUser — thousands of times per second on both controller-local runs and every distributed generator.

**Verification notes:** Confirmed verbatim: archive_writer.go:29-35 puts json.Encoder on the raw *os.File and RecordResult (line 153) encodes under the single w.mu — one write(2) syscall per result plus event construction (including console/network/DOM evidence clones) inside the critical section; the generatorrpc twin (archive.go:279-311) is identical, and both are wired unconditionally into every VUser's synchronous result path (vuser_execute.go:30 via execution_backend.go resultSink and service.go:1371 combineSinks). Tempering the claimed "high": writes hit page cache (no per-record fsync), so at a few-thousand results/s this costs single-digit percent of a core; it only becomes a throughput ceiling and pacing-jitter source at tens-of-thousands of results/s or browser tests with fat evidence payloads — hence medium, trending high at scale. Verified no mid-run readers of transactions.jsonl (parquet/report/GetArchiveArtifact all run post-finalize), so buffering is safe.

**Refined fix:** 1) Wrap each archive file in a bufio.Writer (64-256KB) and point the json.Encoder at it; flush on a ~1s ticker or every N records, and in Finalize call Flush BEFORE file.Sync(), parquet generation, and updateRunArchiveArtifactIntegrity hashing (generator side: before finalizeArchive hashing and GetArchiveArtifact serving). 2) Cheapest extra win the original fix misses: build the TransactionEvent (all clone* calls) BEFORE taking w.mu — JSON struct construction likely costs as much as the syscall and currently serializes all VUsers; only Encode + TransactionCount++ need the lock. 3) Defer the channel+writer-goroutine variant unless profiling shows residual contention — it changes error propagation and shutdown ordering for modest gain once 1+2 land. Apply identically to pkg/distributed/generatorrpc/archive.go.

**Risk:** Buffered tail data can be lost if the process crashes mid-run (currently each record hits the file immediately); periodic flush bounds the loss window. Finalize must flush before file.Sync() and integrity hashing or archive hashes/counts will mismatch.

### 75. Every test completion walks every archive directory on disk while holding the global runner write lock

- Location: `pkg/api/usage_accounting.go:444`
- Category: io
- Finder: background-loops
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** usageAccountingFromTests calls usageArchiveBytes(test) (line 237) for every test, and usageArchiveBytes does a full filepath.WalkDir of the run's archive directory, stat-ing every file. completeTestRun (testexecution.go:1042) calls captureUsageSnapshotForTerminalRunLocked (persistence.go:701) between tr.mu.Lock()/Unlock() (testexecution.go:1055), which runs this aggregation over ALL tests. So each terminal run walks every archived run's directory tree — N archives x M files of stat syscalls — while the single global runner mutex is held, then JSON-marshals and INSERTs the snapshot, still under the lock.

**Evidence:** _ = filepath.WalkDir(test.Archive.ArchivePath, func(path string, entry os.DirEntry, err error) error { ... total += info.Size() ... })  // called per test from usageAccountingFromTests; captureUsageSnapshotForTerminalRunLocked runs inside tr.mu.Lock() in completeTestRun (testexecution.go:1055)

**Hot path:** Runs on every run completion (execution plans complete many runs back-to-back), on the usage snapshot cadence tick, and on every GET /usage-accounting / entitlement / CSV-export request. With hundreds of archived large runs (each archive holding many parquet/HAR/CSV files), this is tens of thousands of stat calls per completion while tr.mu blocks all API reads (ListTests also takes tr.mu) and all live-test state updates.

**Verification notes:** Verified every link: usageArchiveBytes (usage_accounting.go:444) WalkDir-stats each archive with no caching; RunArchiveRecord has no stored size; completeTestRun (testexecution.go:1043-1056) holds the global tr.mu WRITE lock through captureUsageSnapshotForTerminalRunLocked (persistence.go:701), which walks ALL tests' archives + JSON-marshals + SQLite-INSERTs under the lock, while the adjacent cadence path already does the correct copy-then-release pattern; loadStoredTests hydrates full run history so N is unbounded. One overstatement: an archive dir has ~7 files (jsonl/parquet/manifest, archive_writer.go:37-69), not many, so "tens of thousands of stats" needs ~1500+ archived runs — typical installs on SSD see tens-of-ms stalls (SQLite fsync under the lock likely dominates), while large histories on K8s network storage can hit seconds, blocking all API reads (ListTests also write-locks) and live-test state updates at every completion; the per-request GET /usage-accounting, entitlements, and user-activity walks run outside the lock but repeat the full I/O per request.

**Refined fix:** Split into two independent fixes. (1) Lock fix, trivial and highest value: make completeTestRun mirror captureUsageSnapshotForCadence — copy the []*ManagedTest slice under tr.mu, then aggregate/marshal/INSERT after Unlock (e.g., delete the Locked variant and call a deferred capture after tr.mu.Unlock() at testexecution.go:1056). (2) Walk fix without schema churn: keep an in-memory map[testID]int64 archive-size cache on TestRunner, populated at archive completion (archive_writer/distributed_archive) and on loadStoredTestArchive, invalidated/refreshed on retention prune (archive_retention.go) and test-notes archive re-upload; usageArchiveBytes reads the cache and only walks on miss. If persisting the size in RunArchiveRecord instead, add the column knowing the SQLite→Postgres rewriteSQL translation requires coordinated rewrite entries (documented project gotcha).

**Risk:** Stored sizes can drift if archive contents change outside the record lifecycle (retention prune, manual deletion); prune paths must clear/refresh the cached size. Moving capture outside the lock slightly changes snapshot ordering relative to concurrent completions (benign).

### 76. Schedule-health endpoint loads the entire scenario_schedule_occurrences table under the runner read lock just to count statuses, every 5 seconds

- Location: `pkg/api/scenario.go:470`
- Category: database
- Finder: background-loops
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** ScenarioScheduleHealth calls ListAllScenarioScheduleOccurrences() — a full-table SELECT of all 14 columns with ORDER BY planned_at DESC, materializing a struct per row — only to increment health.StatusCounts[occurrence.Status]. It runs while tr.mu.RLock is held (acquired at line 445 with defer), so the DB scan extends the lock hold and delays writers.

**Evidence:** if occurrences, err := tr.scenarioStore.ListAllScenarioScheduleOccurrences(); err == nil {
    for _, occurrence := range occurrences { health.StatusCounts[occurrence.Status]++ } }

**Hot path:** GET /api/v1/schedules/health is fetched by the Monitoring dashboard in the same 5-second refresh loop (MonitoringPage.tsx:408/458). The occurrences table grows forever (one row per scheduled fire/skip/block; no retention loop deletes them), so per-poll cost grows linearly with deployment age — a 5-minute schedule accumulates ~105k rows/year, all decoded every 5s per viewer.

**Verification notes:** Verified every link: scenario.go:470 does call ListAllScenarioScheduleOccurrences (full 14-column SELECT, no WHERE/LIMIT, ORDER BY planned_at DESC with no covering index, sqlite_schedules.go:158) inside the tr.mu.RLock held from line 445; MonitoringPage.tsx polls GET /api/v1/schedules/health every 5s (setInterval at :458, fetched in Promise.all at :402) with no server-side cache; the only DELETE on scenario_schedule_occurrences is the manual per-ID API endpoint, so the table grows unboundedly. tr.mu writers include the live-metrics publish path (metrics.go:451/630) and scheduler ticks, so long RLock holds delay live-run telemetry. Rated medium rather than high because today's deployments have small tables (tens of ms per poll); it degrades linearly with age/schedule count toward the claimed severity.

**Refined fix:** As suggested: add CountScenarioScheduleOccurrencesByStatus() (map[string]int, error) running SELECT status, COUNT(*) FROM scenario_schedule_occurrences GROUP BY status (plain DML — identical on SQLite/Postgres, no rewriteSQL entries needed since those cover DDL), add it to the runner/scheduler store interfaces (pkg/api/runner.go:53 area) plus the metricsStoreSpy test stub, and call it in ScenarioScheduleHealth AFTER releasing tr.mu (restructure: take counts first or after the locked in-memory loop; no atomicity needed for a health widget). Two follow-ups worth noting but out of scope for this fix: run_calendar.go:552 and launch_preflight.go:469 do the same full-table list on colder paths, and the real long-term fix is a retention/pruning policy for the occurrences table since nothing deletes rows today.

**Risk:** None functionally — identical counts; just verify SQLite/Postgres dialect handling (rewriteSQL) for the new query.

### 77. Run-calendar snapshot loads ALL scenario-schedule occurrences ever recorded (no time filter) per request, on a growing, never-pruned table

- Location: `pkg/api/run_calendar.go:552`
- Category: database
- Finder: background-loops
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** runCalendarSnapshot calls ListAllScenarioScheduleOccurrences() and clones every row, then BuildRunCalendar discards everything outside [from,to) in memory. The requested calendar range is never pushed into the query even though the table has UNIQUE(schedule_id, planned_at) and rows are time-keyed. The same function also issues one ListExecutionPlanScheduleOccurrences query per plan schedule (line 585, N+1). scenario_schedule_occurrences has no retention path (only a manual single-row DELETE endpoint), so this scan grows without bound.

**Evidence:** if storedOccurrences, err := store.ListAllScenarioScheduleOccurrences(); err == nil {
    for _, occurrence := range storedOccurrences { ... occurrences = append(occurrences, cloneScenarioScheduleOccurrence(occurrence)) } }

**Hot path:** Executed on every GET /run-calendar, which the Monitoring dashboard polls every 5 seconds per open tab. Cost is O(total occurrences ever created) per poll: full-table read, per-row decode, per-row clone allocation — while a default request only needs a 60-day window.

**Verification notes:** Verified all claims: sqlite_schedules.go:158 is an unfiltered full-table SELECT, run_calendar.go:552 clones every row then BuildRunCalendar discards out-of-range events in memory, line 585 is a real N+1, MonitoringPage.tsx:458 polls the endpoint every 5s per visible tab, and no retention path exists (only a manual single-row DELETE; the *_retention.go loops skip this table). Downgraded from high to medium: the UNIQUE(schedule_id,planned_at) upsert bounds growth to one row per schedule per planned time, so typical cron cadences (hourly/nightly) yield only 10k-100k rows/year — low-ms per poll today, degrading gradually over months rather than a cliff.

**Refined fix:** Add ListScenarioScheduleOccurrencesInRange(from, to) and pass the clamped calendar range down, with three corrections to the original fix: (1) the UNIQUE(schedule_id, planned_at) index does not serve a pure planned_at range scan (schedule_id is the leading column) — add a dedicated planned_at index; (2) fetch with a look-back margin (planned_at >= from - maxScenarioDuration, e.g. 24-48h) because events overlap [from,to) by StartAt/EndAt and an occurrence planned before `from` can still cross into the window — this also preserves occurrenceByScheduleAt dedup for projections; (3) apply the same ranged variant to the other ListAllScenarioScheduleOccurrences callers at launch_preflight.go:469 and scenario.go:470. Batch the plan-schedule occurrences (run_calendar.go:585) with a single IN(...) or ranged query. Add a retention sweep for terminal-status occurrences older than the archive-retention horizon, mirroring audit_retention.go.

**Risk:** Projection dedup (occurrenceByScheduleAt) must still see all occurrences inside the projection window [now, to] — fetching by the same [from,to] range preserves that; off-by-one at window edges could double-show an occurrence as projected.

### 78. Cron projections recomputed from scratch, minute-by-minute, for every enabled schedule on every 5-second calendar poll

- Location: `pkg/api/run_calendar.go:732`
- Category: caching
- Finder: background-loops
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** projectRunCalendarSchedule (and projectRunCalendarPlanSchedule at line 880) walk each schedule's cron forward by repeatedly calling nextScenarioScheduleRun/nextCronRun, which evaluates candidates one minute at a time; cronFieldMatches re-splits the cron field strings (strings.Split per candidate per field) on every minute evaluated. A daily schedule over a month view evaluates ~43k candidate minutes; nothing is cached between polls even though schedules only change on explicit mutation (they carry a Revision) and the window advances by seconds.

**Evidence:** for len(events) < runCalendarMaxProjectedPerCron {
    next, err := nextScenarioScheduleRun(schedule, cursor)  // each call = minute-by-minute scan in nextCronRun with per-candidate strings.Split allocations

**Hot path:** Runs per enabled scenario schedule + per execution-plan schedule on every run-calendar request; the Monitoring dashboard polls that endpoint every 5s. With tens of schedules this is millions of candidate evaluations and string-split allocations per poll, repeated forever, multiplied by concurrent dashboard viewers.

**Verification notes:** Verified the full chain: run_calendar.go:731/879 loop per enabled schedule calling nextCronRun (scheduler.go:226), which walks candidates minute-by-minute up to a 5-year horizon, with cronFieldMatches (scheduler.go:481) doing strings.Split per field per candidate minute; no caching or precompilation exists anywhere. Hotness confirmed: MonitoringPage.tsx:458 polls every 5000ms with getRunCalendar in the batch, default month view, and BuildRunCalendar re-projects every enabled scenario+plan schedule per request; runCalendarMaxProjectedPerCron bounds occurrences, not minutes scanned. The finding even understates the worst case: the loop's last nextCronRun call scans past the window to the next fire regardless — a yearly cron scans ~260k minutes and a leap-day cron ~2.1M minutes per poll. Impact stays medium because it is control-plane CPU/GC only while a Monitoring page is open (tens of ms for a few daily schedules, hundreds of ms+ with many schedules/sparse crons/multiple viewers), not the load-generation data path.

**Refined fix:** Do it in two low-risk steps before any cache: (1) precompile each cron field into a uint64 bitmask once per nextCronRun call (fields are static within a call), making per-minute matching allocation-free integer checks — no cross-request state, no invalidation; (2) add skip-ahead scanning: on month mismatch jump to the first minute of the next local month, on day mismatch to the next local day, on hour mismatch to the next local hour — collapses a daily cron over a month view from ~43k to hundreds of evaluations and fixes the pathological sparse-cron scans (yearly ~260k, Feb-29 ~2.1M minutes per poll); the existing DST/leap-day tests in runner_test.go:5300-5800 guard the semantics, and this also speeds up the other callers (scheduler.go, launch_preflight.go:493, execution_plan_scheduler.go) for free. If a cache is still wanted afterward, key it (scheduleID, Revision, window bucket) and store raw projected times pre-dedup — the occurrenceByScheduleAt dedup is already applied per-request inside the loop, so no invalidation on materialization is needed, only on schedule mutation (Revision covers that).

**Risk:** Cache must be invalidated on schedule create/update/delete and on occurrence materialization, or the calendar briefly shows a projected event that has already materialized (already tolerated at 5s staleness).

### 79. nextCronRun scans up to 5 years minute-by-minute when the allows callback always rejects (indefinitely paused or fully-blacked-out schedules)

- Location: `pkg/api/scheduler.go:226`
- Category: algorithmic
- Finder: background-loops
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** nextCronRun iterates candidate minutes up to maxCronSearchHorizon (5*366 days ~ 2.63M iterations), calling the allows callback on every cron-matching candidate. For a schedule paused open-endedly (DisabledFrom set, DisabledUntil nil) or with a blackout window that always matches (StartTime == EndTime returns true in scheduleWindowContains), every candidate is rejected and the full horizon is scanned before erroring. upsertScenarioScheduleLocked short-circuits this at save time (line 1368), but projectRunCalendarSchedule only checks schedule.Enabled — a paused-but-enabled schedule triggers the full 2.6M-iteration scan on every calendar request, and the skip-missed-run path in the scheduler sweep can hit it under tr.mu.

**Evidence:** for candidate := from.Truncate(time.Minute).Add(time.Minute); !candidate.After(limit); candidate = candidate.Add(time.Minute) {  // limit = from + 5*366*24h; allows()==false forever -> full scan then error

**Hot path:** Reachable from the 5s-polled run-calendar projection (per request, per paused schedule) and from the 30s leader sweep. One paused per-minute schedule costs ~2.6M candidate evaluations (each with time.In conversion and several strings.Split allocations) per calendar poll — hundreds of ms of CPU and tens of MB of garbage every 5 seconds.

**Verification notes:** Verified end-to-end: nextCronRun (pkg/api/scheduler.go:226) scans ~2.63M minute candidates with a strings.Split allocation each (cronFieldMatches:482); an open-ended pause makes scheduleAllowsCandidate reject everything, and the upsert short-circuit (scheduler.go:1368) saves such schedules with NextRunAt=nil but Enabled=true, while the run-calendar caller (run_calendar.go:293, and 327/876 for plan schedules) filters only on Enabled — so every calendar request (polled every 5 s by MonitoringPage.tsx:409/458) runs one full scan per paused schedule (~0.3-1.5 s CPU + tens of MB garbage, off-lock since the snapshot RLock is released first). The sweep claim is real and slightly worse than stated: after the last pre-pause run materializes, the recompute at scheduler.go:977/1015 errors and leaves NextRunAt lapsed, so the 30 s leader sweep repeats the full scan under the exclusive tr.mu write lock indefinitely. The always-matching-blackout variant (scheduleWindowContains:821) is confirmed code-wise but cannot persist — upsert's save-time next-run computation errors and rejects the save — so only the pause variant is hot.

**Refined fix:** Two-part fix, simpler and safer than rejection-counting: (1) In the calendar projections (projectRunCalendarSchedule / projectRunCalendarPlanSchedule), pass the calendar range end `to` down as the scan limit (limit = min(from+maxCronSearchHorizon, to)) — the loops already discard !next.Before(to), so capping is behavior-identical, bounds every scan to the visible window (~44K iterations for a month view), and also fixes an unclaimed variant: healthy sparse crons (annual/leap-day) currently scan millions of minutes per poll even when un-paused. Additionally skip projection entirely when executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) in both calendar loops (run_calendar.go:293/327). (2) In the sweep recompute sites (scheduler.go:977, 1015, 1023, and applyMissedRunPolicyLocked:166), check executionPlanSchedulePausedIndefinitely first and set NextRunAt=nil instead of calling nextScenarioScheduleRun — mirroring upsert line 1368 — which kills the repeating 2.6M scan under tr.mu every 30 s sweep. The finding's stated risk (finite far-future DisabledUntil must still project) is inherently handled: pausedIndefinitely only fires when DisabledUntil==nil, and the horizon cap by `to` never suppresses an in-range run. The suggested consecutive-rejection bail-out is unnecessary complexity once the limit is range-aware.

**Risk:** A schedule paused with a finite far-future DisabledUntil must still find its post-pause run; the short-circuit must only apply to genuinely open-ended pauses/always-rejecting windows or schedules would silently stop projecting.

### 80. time.LoadLocation called per candidate evaluation inside cron window checks — tzdata re-read and re-parsed millions of times

- Location: `pkg/api/scheduler.go:804`
- Category: cpu
- Finder: background-loops
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** scheduleWindowContains resolves the window timezone with time.LoadLocation on every invocation. Go's LoadLocation does not cache: each call re-reads and re-parses the zoneinfo file. scheduleAllowsCandidate calls it once per window (blackout + maintenance + availability) for every cron-matching candidate inside nextCronRun's minute scan, so a frequent cron (every 1-5 minutes) with windows performs tens of thousands of tzdata loads per projection call.

**Evidence:** location, err := time.LoadLocation(timezone)  // inside scheduleWindowContains, invoked per window per matching candidate from scheduleAllowsCandidate

**Hot path:** Inner loop of nextCronRun, which runs per enabled schedule per run-calendar request (polled every 5s), per scheduler sweep materialization (every 30s), and in the schedule-preview endpoint. Each LoadLocation is ~10-50us plus allocations, multiplied by matching candidates x windows x schedules x polls.

**Verification notes:** Verified at pkg/api/scheduler.go:804: time.LoadLocation runs per window per cron-matching candidate via scheduleAllowsCandidate inside nextCronRun's minute scan, with no cache anywhere in the package (stdlib fast-paths only ""/"UTC"/"Local"). Hotness confirmed: GET /api/v1/run-calendar is polled every 5s by MonitoringPage (setInterval(refresh,5000), MonitoringPage.tsx:458) and projectRunCalendarSchedule calls nextScenarioScheduleRun up to 1000x per enabled schedule per request; the 30s sweep is mostly cold (due-schedules only). Impact is real but config-gated — needs non-UTC timezone + windows + frequent cron; typical cost is tens-to-hundreds of ms per poll, but a never-allowed window config (e.g., all-covering blackout) amplifies the 5-year minute scan (~2.6M candidates) into tens of seconds of controller CPU per poll. Control-plane only; VUser throughput unaffected.

**Refined fix:** Prefer the package-level cache over pre-resolving in the allows closure: windows carry their own per-window Timezone (scheduleWindowContains line 800 uses window.Timezone before the schedule fallback), so per-schedule pre-resolution misses per-window zones. Add a small sync.Map[string]{loc *time.Location, err error} keyed by trimmed zone name, used by both scheduleWindowContains (scheduler.go:804) and scheduleEvalLocation (scheduler.go:563) — this fixes the scenario, execution-plan, preview, and launch-preflight paths at once. Cache the error result too (or successes only) so scheduleWindowContains keeps returning an error on invalid zones while scheduleEvalLocation keeps its silent UTC fallback. No need to special-case "UTC"/"" — stdlib already fast-paths them, but caching them is harmless.

**Risk:** Negligible — identical semantics; a process-lifetime location cache ignores on-disk tzdata updates, which Go programs generally already do.

### 81. Latest-occurrence lookup loads a schedule's entire occurrence history to use only the newest row

- Location: `pkg/api/scheduler.go:1250`
- Category: database
- Finder: background-loops
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** attachLatestScenarioScheduleOccurrenceLocked calls ListScenarioScheduleOccurrences(schedule.ID), whose SQL (sqlite_schedules.go:150) is ORDER BY planned_at DESC with no LIMIT, then uses only occurrences[0]. ListScenarioSchedules invokes it once per schedule while holding tr.mu.RLock, so listing a scenario's schedules decodes and allocates every occurrence row that schedule ever produced.

**Evidence:** occurrences, err := tr.scenarioStore.ListScenarioScheduleOccurrences(schedule.ID)
if err != nil || len(occurrences) == 0 { return }
schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrences[0])

**Hot path:** Runs per schedule on every schedules-list request from the scheduling UI. A schedule firing every 5 minutes accumulates ~105k occurrence rows per year; each list view then decodes 105k rows per schedule to display one, under the runner read lock, growing linearly forever since occurrences are never pruned.

**Verification notes:** Verified: scheduler.go:1250 loads the full occurrence history (sqlite_schedules.go:150 has ORDER BY planned_at DESC, no LIMIT) to use only row [0], once per schedule under the global tr.mu.RLock. Growth is unbounded — every scheduler tick persists a row (materialized/skipped/blocked/failed) and no retention prunes the table, so per-minute cron reaches ~525k rows/year. The only caller is the schedules-list endpoint hit on scenario selection and schedule CRUD in NewTestPage (no polling), so it is interactive-frequency, not a hot loop — but each hit blocks runner writers (including scheduler ticks) for the scan and the cost worsens forever, justifying medium.

**Refined fix:** Add GetLatestScenarioScheduleOccurrence(scheduleID) to SQLiteStore issuing the existing SELECT with LIMIT 1 (backed by UNIQUE(schedule_id, planned_at) at sqlite_schema.go:144; LIMIT 1 is dialect-identical in SQLite/Postgres so rewriteSQL needs no change), add it to the store interfaces in pkg/api/runner.go:52 and pkg/api/scheduler.go:23, and call it from attachLatestScenarioScheduleOccurrenceLocked. Optional micro-win: early-return when the cloned schedule already carries a scheduler-maintained LastOccurrence (kept current at scheduler.go:171/970/1004/1066), leaving the DB read for the post-restart cold case. Follow-up (separate finding): the occurrences endpoint (pkg/api/scenario.go:362) returns the same unbounded history to the dashboard and needs a LIMIT/pagination.

**Risk:** None — identical result; verify LIMIT syntax passes the Postgres rewriteSQL dialect translation.

### 82. Full 8MiB stdout/stderr retained per result and scrubbed by 6 regex passes, then discarded downstream

- Location: `pkg/protocol/java_vuser/java_vuser.go:283`
- Category: cpu
- Finder: sup:java-vuser-process-per-action
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** finalizeResult stores the entire captured stdout (up to 8MiB) as result.ResponseBody (line 283) and the entire stderr as ProtocolEvidence["runner_stderr"] (line 357), after stdout.buf.String()/stderr.buf.String() at line 198 already copied both buffers into fresh strings. Every recorded result then goes through scrubSensitiveResult (vuser_execute.go:26), which runs 5 payload regexes plus the high-entropy token pass with per-candidate Shannon-entropy computation (redaction.go:112,119,235-249) over ResponseBody AND the stderr string in the evidence map. Yet neither consumer keeps the body: the RPC transaction buffer copies only scalar fields (generatorrpc/service.go:320-340) and the archive recorder's archiveTransactionEvent omits ResponseBody/ProtocolEvidence entirely (generatorrpc/archive.go:285-305). ResponseBody's only use is opt-in 'data_returned' debug logging (vuser_resolve.go:136). Additionally parseRunnerSummary calls reverseNonEmptyLines (line 476-486), which strings.Split()s the full 8MiB and materializes a slice of ALL non-empty lines just to find the last JSON summary line.

**Evidence:** result.ResponseBody = stdout
result.BytesReceived = int64(len(stdout))
...
if stderr != "" {
    result.ProtocolEvidence["runner_stderr"] = stderr
}

**Hot path:** Per action per iteration; a chatty script (System.out debug prints, payload dumps) makes each action allocate ~32MiB of transient buffers/strings and regex-scan up to 16MiB with 6 patterns — at tens of actions/sec that is hundreds of MB/s of wasted regex CPU and GC pressure on the generator, with up to MaxProcesses actions finalizing concurrently.

**Verification notes:** Verified every cited line: full 8MiB-capped stdout/stderr are stored on the Result (java_vuser.go:283,357), scrubbed unconditionally by 5 regexes + entropy pass (vuser_execute.go:26, redaction.go:112,119,235-249), and ResponseBody is dropped by both archive paths (its only consumer is opt-in data_returned logging at vuser_resolve.go:136). Two corrections: the local-run archive writer (pkg/api/archive_writer.go:144) DOES persist ProtocolEvidence, so full runner_stderr is cloned+JSON-encoded into archives (worsens cost but means stderr bounding changes archived diagnostics); and reverseNonEmptyLines shares string backing via strings.Split, so it is an O(n) scan + header-slice alloc, not an 8MiB copy. Impact downgraded to medium because the protocol spawns a JVM per action (MaxProcesses default 25, ~100-500ms startup), bounding throughput to ~50-150 actions/sec, and the scrub cost only bites when scripts emit MBs of output — real for chatty LR-imported Java scripts, negligible otherwise.

**Refined fix:** Keep the suggested cap, with ordering and archive caveats: (1) run parseRunnerSummary and parseRunnerEvents on the full captured text FIRST (the JSON summary is the last stdout line), then store only a bounded head+tail excerpt (e.g. 8KiB head + 56KiB tail) as ResponseBody and a similarly bounded stderr excerpt in ProtocolEvidence["runner_stderr"]; full sizes stay reported via runner_stdout_bytes/runner_stderr_bytes. (2) Because pkg/api/archive_writer.go:144 persists ProtocolEvidence into local-run archives, add a distinct runner_stderr_excerpted flag rather than reusing runner_stderr_truncated (which means capture-level truncation), so archived diagnostics honestly signal the excerpting. (3) Replace reverseNonEmptyLines with a backward strings.LastIndexByte('\n') scan yielding trimmed lines lazily — the win is avoiding the full forward split + slice alloc, not a data copy. (4) Optional deeper win: since scrubbing then operates on <=128KiB instead of 16MiB, no redaction changes are needed; do NOT truncate before summary/event parsing or failed runs could lose their summary line.

**Risk:** Users debugging with log level 'extended' + data_returned would see a truncated body; failed-action diagnostics lose mid-stream output (keep head+tail if that matters). Truncation flags already exist to signal it.

### 83. readRunnerEventFile reads the entire unbounded event file into memory before truncating

- Location: `pkg/protocol/java_vuser/java_vuser.go:393`
- Category: memory
- Finder: sup:java-vuser-process-per-action
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** stdout/stderr are capped writer-side by cappedBuffer, but the JSONL event file has no writer-side cap: lr.emit appends unboundedly for every lr.* call (lr.java:497). readRunnerEventFile then does os.ReadFile(path) — loading the whole file into memory — and only afterwards slices to defaultRunnerMaxCaptureBytes (line 397-400). A runaway or event-heavy script (lr.log/user_data_point in a tight loop) can produce a multi-GiB file that the generator reads fully into RAM, and up to MaxProcesses actions can hit this concurrently, risking generator OOM — the exact hazard the cappedBuffer comment (lines 183-185) was written to prevent for the other two streams.

**Evidence:** data, err := os.ReadFile(path)
if err != nil {
    return "", false
}
if len(data) <= defaultRunnerMaxCaptureBytes {
    return string(data), false
}
return string(data[:defaultRunnerMaxCaptureBytes]), true

**Hot path:** Once per java_vuser action (event file is created per action at line 175); severity scales with script event volume and MaxProcesses concurrency, and the file size is entirely under uploaded-script control.

**Verification notes:** Verified all claims: java_vuser.go:393 does os.ReadFile of the whole event file before the 8MiB slice; lr.java:497 emit() appends per lr.* call with no byte budget (~35 API methods route through it); the cappedBuffer comment at lines 183-185 explicitly names the same OOM hazard for stdout/stderr, leaving the event stream as the one uncapped channel. Worse than claimed in one respect: the wall-clock bound is script-controlled too — actionTimeout (line 506) honors an unclamped script-supplied timeout_ms param, so the emit window is arbitrary; DefaultJavaVuserMaxProcesses=25 gives ~25x concurrent worst-case peak per generator. Common case (small event files) is negligible cost, so this is a script-triggerable worst-case OOM hazard rather than a steady-state regression — medium is calibrated correctly.

**Refined fix:** In readRunnerEventFile: os.Open, then f.Stat() for size (file is stable — cmd.Wait() has returned so the child exited), allocate min(size, defaultRunnerMaxCaptureBytes), io.ReadFull, return truncated = size > cap. This avoids both the unbounded read AND the naive cap+1 approach's 8MiB allocation per action for the common tiny-file case. Companion fix (recommended, separate concern): writer-side byte budget in lr.java emit() — track cumulative bytes, stop appending past the budget and write one events_truncated marker line — since the reader-side fix alone still allows unbounded temp-dir disk growth during a long action.

**Risk:** Minimal — truncation semantics are unchanged (first 8MiB kept); only the peak memory of the read path changes.

### 84. Proxy record path holds the session mutex across gzip/brotli decompression and a full-body WebSocket broadcast marshal

- Location: `pkg/recorder/recorder.go:943`
- Category: concurrency
- Finder: sup:recorder-session-payload-and-poll
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** recordRequestWithOptions locks s.mu at entry (line 809, deferred unlock) and then, still under the lock: decompresses up to 512KiB of response body (decodeBody, line 881 — gzip/zlib/brotli), base64-encodes bodies, and finally invokes s.notify (line 943), which is the API handler's closure calling hub.BroadcastJSON — a json.Marshal of the full RecordedRequest including up to ~1MB of body strings — before releasing the lock. Every concurrent proxied request in the session serializes behind this work, and GetSession/ListSessions snapshots block on the same mutex.

**Evidence:** func (r *Recorder) recordRequestWithOptions(...) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	...
	if decoded, ok := decodeBody(respBody, resp.Header.Get("Content-Encoding"), s.Filters.BodyCaptureLimitBytes); ok {
	...
	if s.notify != nil {
		s.notify(SessionEvent{Type: "request_recorded", SessionID: s.ID, Request: &rec})
	}

**Hot path:** Runs once per recorded HTTP exchange on the proxy hot path. A busy SPA drives dozens of concurrent requests through the MITM proxy; each pays milliseconds of decompression + megabyte-scale JSON marshal inside the single session mutex, so recording throughput degrades to single-file and inflates recorded response times (ThinkTimeMs/ResponseTimeMs skew).

**Verification notes:** Verified in pkg/recorder/recorder.go:808-951: s.mu is held across decodeBody (real gzip/zlib/brotli decompress up to 512KiB, line 881), base64 body capture, and s.notify (line 943), whose closure (pkg/api/handlers_recorder.go:36-43) synchronously json.Marshals the full RecordedRequest (up to ~1MB of body strings) via hub.BroadcastJSON (websocket.go:206), plus a second envelope marshal when HA fanout is configured; per-connection proxy goroutines (recorder.go:552, tls.go:524, websocket.go:114) serialize on this mutex and RecordingStudio's getRecording polls contend via snapshot() (recorder.go:343) copying the whole Requests slice under the same lock. Two corrections: ResponseTimeMs is NOT skewed (elapsed computed and response flushed to browser before the lock, recorder.go:549/tls.go:520-520) — only ThinkTimeMs inflates second-order via keep-alive handler stalls; and this is the interactive recording path (dozens of concurrent requests per SPA page load, ms-scale work per event), not the VUser-scale engine path, so impact is medium, not high. The WS send itself is non-blocking drop-on-full, so cost is CPU under lock, not slow-client stalls.

**Refined fix:** Step 1 (low risk, most of the win): stop broadcasting bodies — grep confirms dashboard/src has NO consumer of request_recorded (RecordingStudio polls getRecording instead), so strip Body/ResponseBody/RawHeaders(Multi) from the SessionEvent (or send a slim {seq, method, url, status, response_time_ms} summary). This shrinks the under-lock marshal from ~MB to ~KB and also fixes the fanout double-marshal, making it unnecessary to move notify outside the lock (avoiding the seq-reordering risk entirely). Step 2 (optional): hoist decodeBody/capturedBody/sanitizeHeaders above s.mu.Lock() — safe because Session.Filters is only assigned in StartSessionForProject and never mutated after — but add a cheap pre-lock status/host-filter check (or accept wasted decode on paused/filtered sessions) since the current early-returns at lines 812-849 run before decode. Only seq assignment, think-time bookkeeping, BaseURL init, and the append need the lock.

**Risk:** Notifying outside the lock can reorder events relative to appends under high concurrency; consumers that rely on strict seq ordering of request_recorded events would need a per-session ordered dispatch (e.g. buffered channel).

### 85. handleProxy builds a new http.Client + http.Transport per proxied request, leaking idle upstream connections

- Location: `pkg/recorder/recorder.go:502`
- Category: network
- Finder: sup:recorder-session-payload-and-poll
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** Every plain-HTTP request through the recording proxy constructs a fresh http.Client with a fresh http.Transport, so no upstream connection is ever reused across requests — each pays a new TCP (and TLS for https targets) handshake. Worse, the custom Transport has IdleConnTimeout=0 (unlimited), and CloseIdleConnections is never called, so each abandoned transport parks its keep-alive upstream connection plus its read/write-loop goroutines until the remote server closes it. The MITM path has the same pattern per CONNECT tunnel (tls.go:342-351): connections are reused within one tunnel but the transport's idle conns are never closed when the tunnel ends, and browsers open many tunnels (≈6 per host plus workers).

**Evidence:** client := &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext:     proxySafeDialer(30 * time.Second).DialContext,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},

**Hot path:** Once per proxied request (plain HTTP) and once per CONNECT tunnel (HTTPS). Recording a chatty SPA generates hundreds to thousands of requests; each leaves behind goroutines + fds and adds full connection-setup latency to the recorded ResponseTimeMs, distorting the think-time/response-time data the converter bakes into scripts.

**Verification notes:** Confirmed at recorder.go:502 (fresh Client+Transport per plain-HTTP request, body fully read so the conn parks in the abandoned transport's idle pool with its 2 goroutines) and tls.go:342-351 (per-CONNECT-tunnel transport, idle conns orphaned at tunnel end; the 120s tunnel timeout only reclaims the client-side fd). No CloseIdleConnections call exists anywhere in pkg/recorder, and elapsed at recorder.go:549 includes per-request DNS+TCP dial, so the timing-distortion claim also holds. Downgraded high→medium: this is the single-user recording proxy, not the VUser data plane, and the leak is self-limiting (parked conns die at the upstream server's keep-alive timeout, so steady-state is hundreds of fds/goroutines during an active recording, not unbounded), with most modern traffic taking the per-tunnel MITM path where in-tunnel reuse already works.

**Refined fix:** Create one shared *http.Transport on the Recorder at construction (DialContext: proxySafeDialer(30*time.Second).DialContext, TLSClientConfig InsecureSkipVerify, MaxIdleConnsPerHost: 8, IdleConnTimeout: 90*time.Second) and reference it from both the handleProxy client and the MITM tunnel client — keep the two http.Client wrappers distinct since they intentionally differ (handleProxy has Timeout: 30s, tunnel client has none; both use ErrUseLastResponse). Call transport.CloseIdleConnections() in Recorder.Close(). Sharing is safe: proxySafeDialer is a stateless factory and proxyDialControl is a pure function, so SSRF-guard semantics are unchanged.

**Risk:** Shared transport pools connections across recorded hosts — behavior standard for proxies; per-tunnel TLS settings are identical (InsecureSkipVerify) so no config conflict. Verify SSRF-guard dialer semantics are unchanged when shared.

### 86. Each recorded WebSocket frame does an O(n) request scan and broadcasts the full request with all accumulated frames

- Location: `pkg/recorder/recorder.go:963`
- Category: algorithmic
- Finder: sup:recorder-session-payload-and-poll
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** appendWebSocketMessage linearly scans s.Requests for the owning seq under s.mu on every frame, and then notifies with `updated = s.Requests[i]` — a struct copy whose WebSocketMessages slice includes every message recorded so far — which the handler JSON-marshals and broadcasts. For a socket with M frames in a session of N requests this is O(N) mutex-held scan per frame and O(M²) total broadcast bytes, with each frame payload up to the 512KiB capture limit.

**Evidence:** for i := range s.Requests {
	if s.Requests[i].Seq != seq { continue }
	s.Requests[i].WebSocketMessages = append(s.Requests[i].WebSocketMessages, msg)
	updated := s.Requests[i]
	if s.notify != nil {
		s.notify(SessionEvent{Type: "request_updated", SessionID: s.ID, Request: &updated})
	}

**Hot path:** Runs per WebSocket frame in both directions through the tunnel (copyWebSocketFrames → recordWebSocketFrame). Chatty sockets (chat, live dashboards, trading feeds) emit many frames per second for the whole recording; late in a large session each frame pays a scan over thousands of requests plus a broadcast that re-serializes the entire message history — all while holding the session mutex that the HTTP record path needs.

**Verification notes:** Verified all three legs: (1) recorder.go:963-973 does the O(N) seq scan under s.mu and copies the request whose WebSocketMessages slice holds all M messages; (2) handlers_recorder.go:36-43 + websocket.go:206 (api) show BroadcastJSON json.Marshals the full request synchronously — inside the mutex-held region, even with no dashboard clients — giving O(M²) cumulative broadcast bytes with 512KiB/message payloads and no cap on the slice; (3) recorder/websocket.go:410-411 shows the finding UNDERSTATES it — recordWebSocketFrame runs before dst.Write, so the growing marshal also delays the proxied frames themselves, corrupting recorded timing fidelity. Hot within the recorder's per-frame data plane (chatty sockets, both directions, whole recording) though it never touches load-test execution, so medium not high.

**Refined fix:** Prioritize the notify payload over the scan: emit a new slim event (e.g. type "websocket_message_recorded" carrying session_id, seq, and only the new RecordedWebSocketMessage) and move the marshal/broadcast outside s.mu (copy the message under lock, notify after unlock); add debouncing (max one broadcast per seq per ~250ms) since chatty sockets flood the hub regardless of payload size. Keep the existing request_updated event unchanged for the metadata-edit path (recorder.go:302-326) to limit dashboard churn; update the dashboard recorder listener for the new event type. Secondary: seq→index map (safe — seq strictly increasing at recorder.go:851/941, requests never removed) and a per-request WebSocketMessages cap (count + total bytes) with a truncation flag surfaced in conversion warnings, which also fixes the unbounded memory growth per socket.

**Risk:** Dashboard listeners of request_updated must handle the slimmer event shape; a per-request frame cap loses tail frames for very long sockets (flag it in the UI/conversion warnings).

### 87. Recording detail table renders every request row unvirtualized and recordingLooksSalesforce re-scans all requests each render

- Location: `dashboard/src/components/RecordingStudio.tsx:1756`
- Category: rendering
- Finder: sup:recorder-session-payload-and-poll
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** The selected-session table maps over selectedSession.requests with no virtualization or row cap, so a large recording mounts thousands of <tr> elements with per-row handlers. Compounding it, recordingLooksSalesforce(session) (lines 96-110) is called inline in render for each stopped session card (line 1704) and flatMaps every request's host/url/path/content_type into one giant joined string — O(total requests) string building — and because the 2s poll replaces all session identities, both costs recur every tick while the studio is open.

**Evidence:** {selectedSession.requests?.map(req => (
  <tr key={req.seq} className={...} onClick={() => setSelectedRequest(req)}>
...
{recordingLooksSalesforce(session) && (  // flatMaps all requests per render

**Hot path:** Selecting a 5-10k-request recording (busy SPA, static excludes off) mounts 9-column rows for every request and re-renders them on every 2s poll tick and on every exclude-toggle; the Salesforce sniff re-concatenates megabytes of URL strings per tick per stopped session.

**Verification notes:** Verified all cited code: line 1756 maps selectedSession.requests into 9-column rows with no virtualization/slice/pagination (static+excluded rows still render); recordingLooksSalesforce (lines 96-110) flatMaps every request's host/url/path/content_type into one joined string and is called inline per stopped-session card (line 1704). Hotness confirmed: refresh() runs on a 2s setInterval (line 661) and setSessions(listRes.sessions) (line 621) replaces all session identities each tick, and the backend list endpoint (handlers_recorder.go:111 → recorder.ListSessions) serializes full unbounded Requests arrays (s.Requests append has no cap; bodies up to 512KB each are included in the payload), so session.requests is fully populated in the cards and the O(total-requests) sniff plus full-table React reconciliation recur every tick while the studio is mounted. One honesty caveat: selectedSession keeps a stale object reference across poll ticks (only replaceRequest updates it), so per-tick table cost is React element creation/diff, not DOM mutation — the acute costs are initial mount of thousands of DOM rows and the exclude-toggle full-array re-render. Impact is medium: real user-facing jank in the flagship recorder workflow, but dashboard-only, studio-open-only, and only bites on multi-thousand-request recordings.

**Refined fix:** The root fix is the list payload, not just the render: make GET /recorder return session summaries (id, name, status, timestamps, base_url, request count, plus a server-computed looks_salesforce flag — trivially derivable in ListSessions/snapshot) and fetch the full session via the existing api.getRecording(id) only on card selection. That simultaneously kills the O(total-requests) client sniff, the per-tick identity churn, and the multi-MB poll payload (list currently ships every request body). Then virtualize the detail table with @tanstack/react-virtual (confirmed already in dashboard/package.json ^3.14.3) or, as a lower-risk interim, cap rendered rows with a "show all N" toggle. If the payload change is deferred, memoize the sniff with a module-level Map keyed on `${session.id}:${session.requests?.length ?? 0}` (per-render useMemo won't help since session identities are replaced each tick), and wrap the detail table in a React.memo child receiving selectedSession — its identity is already stable across poll ticks, so memoization there is safe and replaceRequest already produces a new object on real edits (no version-bump needed).

**Risk:** Virtualized rows change scroll/selection behavior and test selectors; memoization keyed on request_count could miss content-only changes (e.g. step_name edits) unless the replaceRequest path bumps a version.

### 88. Generator advertises a flat 10,000-VUser capacity decoupled from its 4-CPU cgroup quota, letting placement CPU-saturate a measurement pod

- Location: `deployments/k8s/onprem/generator-statefulset.yaml:63`
- Category: cpu
- Finder: sup:go-runtime-k8s-sizing
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** The onprem and aks overlays set APEXLOAD_GENERATOR_MAX_VUSERS=10000 (onprem generator-statefulset.yaml:62-63, aks:67-68) on pods CPU-limited to 4 cores (2,500 VUsers/core), and the code falls back to the same 10000 when unset (pkg/distributed/generatorrpc/service.go:434-435). Advertised capacity is purely count-based — availableCapacityLocked() at service.go:931-937 computes maxVUsers minus active and never consults the detected podResources.CPULimitCores, even though pod_resources.go measures it and ships it to the controller in every stats snapshot (service.go:872). Placement then trusts that number: chooseGeneratorTargetForGroup (pkg/api/distributed_placement.go:349) puts an entire group on a single generator whenever AvailableCapacity >= group.VUsers, so a default-configured 8,000-VUser test lands on one GOMAXPROCS=4 pod even with 50 pods idle. Since container-aware GOMAXPROCS correctly pins the runtime to 4 Ps, saturation shows up as run-queue delay inside the generator: measured response times include generator-side scheduling latency and TPS silently caps out — corrupted results on a product whose entire job is measurement fidelity, with no guardrail or warning. The local overlay's own sizing (1000 VUsers on 2 CPUs, local/generator-statefulset.yaml:40-41,63) implies ~500 VUsers/core is the intended conservative ratio, 5x below what the production overlays permit.

**Evidence:** - name: APEXLOAD_GENERATOR_MAX_VUSERS
  value: "10000"        # onprem/aks manifests; pod cpu limit is "4"
// service.go:932 — capacity ignores CPU entirely:
available := s.maxVUsers - s.activeVUsersLocked()
// distributed_placement.go:349 — whole group packed onto one pod if it fits by count:
if loadByTarget[target].AvailableCapacity >= int32(group.VUsers) { return target }

**Hot path:** Evaluated on every distributed placement decision and then borne for the full duration of every large run: a single 4-core pod driving up to 10k VUsers is CPU-bound for hours while its latency samples feed the run report, SLA verdicts, and release-trend comparisons.

**Verification notes:** Every citation verified: onprem/aks set MAX_VUSERS=10000 on 4-CPU-limited pods (onprem:62-63/101, aks:67-68/125), config.go:28 + service.go:434-435 default to 10000, availableCapacityLocked (service.go:931-937) and AssignWork (service.go:508-515) are purely count-based, and default auto-mode placement (distributed_placement.go:222-226,349) puts an entire group on ONE pod whenever it fits by count — while detected CPULimitCores (pod_resources.go) is shipped in every heartbeat (service.go:872, snapshot at distributed_stream.go:512) but used only for inventory/archive display, never capacity or placement. The finding does overstate two things: a CPU-aware start gate exists (buildCapacityShortfallStartError, capacity.go:633; estimateCapacityAdvisoryWithAvailability computes CPU-dimension RecommendedGenerators) but it is fleet-count-level with configured (not detected) specs — with defaults an 8,000-VUser HTTP test recommends 1 generator (usableVUsers=8000; ~0.00025 cores/VUser → 2 cores < 3.2 usable), so the exact scenario passes the gate and still packs one pod; and post-run RCA flags generator CPU saturation at ≥80% peak process CPU (root_cause_analysis.go:115-122), so skewed results are flagged post-hoc rather than fully silent. The claimed "intended 500 VUsers/core" ratio from the local overlay is contradicted by the platform's own footprint model (~4,000+/core, capacity_planning.go:834), so severity is workload-dependent — medium (not high) is right: real measurement-fidelity risk for heavy scripts/large single-group runs under default assignment, on the measurement path for the full run duration, but lightweight think-time workloads at this density may be fine per the product's own calibration.

**Refined fix:** Three-layer fix, warning-first: (1) Generator-side: in NewService, clamp maxVUsers to detectCPULimitCores() * APEXLOAD_GENERATOR_VUSERS_PER_CORE when a cgroup limit is detected and the configured value exceeds it — but derive the default per-core ratio from the platform's own calibrated density model (capacity_planning.go implies ~2000-4000/core for protocol VUsers), NOT the local overlay's 500/core which the codebase's own estimates contradict; at minimum log a loud startup warning on env-vs-CPU mismatch before enforcing anything. (2) Placement-side (the sharper half of the bug): chooseGeneratorTargetForGroup/orderedLoadTargets should prefer targets by projected VUsers-per-CPULimitCores — the field is already populated in the controller's load snapshot (distributed_stream.go:512) — and fall through to capacityWeightedShards across multiple targets when a single group exceeds a per-pod CPU-derived density threshold, instead of packing one pod with 49 idle. (3) Cheapest immediate win: the launch preflight/capacity advisory currently can't see the mismatch because it uses configured settings.CapacityPlanning.Generator.CPUCores; feed the detected CpuLimitCores from live heartbeats into that path and emit a preflight warning when a planned single-target group exceeds per-pod CPU-derived density — converts silent skew into a pre-run warning with zero placement behavior change. Caution on the config-only mitigation: AvailableCapacity also drives AssignWork rejections and the shortfall/override gate, so lowering the manifest value flips silently-degraded runs into explicit placement failures on small fleets — acceptable but must be release-noted, and the per-core ratio must stay operator-tunable since their own envelopes span 25 (Java) to 10k (DNS) VUsers per generator.

**Risk:** Lowering advertised capacity can make existing large single-group tests fail placement ("no capacity") on small fleets until KEDA scales out or operators raise the per-core knob — a deliberate behavior change from silently-degraded to explicitly-rejected. Per-core defaults are workload-dependent (heavy scripts or browser VUsers support far fewer), so the ratio must stay operator-tunable, and the true fix for capacity ceilings is more replicas, not a bigger number in the env var.

### 89. Full scrub regex battery + JSON parse runs on every observed WebSocket frame

- Location: `pkg/protocol/browser/ws_observe.go:59`
- Category: cpu
- Finder: sup:browser-protocol-per-step
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** recordWSEvent calls scrub.Value on every frame sent or received on every WebSocket. Each call attempts a full JSON decode with UseNumber plus a recursive redaction walk and re-marshal (pkg/scrub/scrub.go:200-223), then runs 6 static regex ReplaceAll passes plus the highEntropyToken ReplaceAllStringFunc with per-candidate Shannon-entropy scoring (scrub.go:175-198) over the (up to 4KB) payload. Yet the only consumer of the ring is matchWSEvents feeding ProtocolEvidence — 255 of every 256 events are evicted unread, and nothing in the ring is ever persisted; scrub-on-write is only needed at the exposure point.

**Evidence:** text := scrub.Value(string(body), scrub.Policy{})  // per OnFrameSent/OnFrameReceived callback (lines 41-42)

**Hot path:** Runs per WS message per VUser for the entire session. A chatty app stream (Socket.io/SignalR/trading feeds at 100s of msgs/sec) pays JSON parse + 7 regex passes per message, continuously, for every browser VUser.

**Verification notes:** Confirmed: ws_observe.go:59 runs scrub.Value (conditional JSON decode/walk/re-marshal + 6 regex passes + entropy-scored token pass) on every WS frame, and grep proves the only reader of the 256-entry ring is matchWSEvents feeding ProtocolEvidence — nothing is persisted, so scrub-at-write is almost entirely wasted work. Verified an aggravator the finding missed: playwright-go invokes OnFrameSent/OnFrameReceived handlers synchronously on the driver connection's dispatch goroutine (connection.go Emit → callHandlers → reflect Call), so the scrub also delays all other driver events at high frame rates, potentially distorting measured latencies. Impact capped at medium (not high) because this is browser-protocol-only and browser VUsers are canary-scale (tens per generator, Chromium dominates cost); the compiledPolicy-hoist sub-claim is refuted — Policy{}.compile() with no ExtraPatterns compiles nothing and is free.

**Refined fix:** Store raw truncated payloads in the ring (keep the utf8-vs-base64 classification on raw bytes at write time) and scrub lazily — but NOT inside matchWSEvents, which re-runs every 50ms in dispatchWSWait's poll loop and would re-scrub repeatedly; scrub the matched slice exactly at the three result.ProtocolEvidence assignment sites (ws_observe.go:102, 107, 134), bounded to ≤256 events per action. Drop the compiledPolicy-hoist part of the fix (zero-Policy compile is a no-op). Document two things: (1) match patterns now evaluate against raw payloads instead of scrubbed ones — patterns matching a secret now match and patterns matching "[REDACTED]" no longer do (arguably more correct); (2) add a comment on wsEvents that any future persistence/export path must scrub-on-exposure. If lazy scrub is deemed too risky, the cheaper alternative that still fixes the dispatch-goroutine stall is handing frames to a single buffered-channel worker that scrubs off the Playwright dispatch path.

**Risk:** Raw payloads sit unscrubbed in the in-memory ring; every read path (currently only matchWSEvents) must scrub before exposing data, and any future persistence of wsEvents must go through scrub-on-write.

### 90. Fork/exec of `ps` over the full process table before every browser action, usually discarded

- Location: `pkg/protocol/browser/browser.go:158`
- Category: cpu
- Finder: sup:browser-protocol-per-step
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** Execute captures a browserProcessSnapshot before every action whenever clientMetricsEnabled is set — which experience canaries force on (config.go:160-165). captureBrowserProcessSnapshot execs `ps -eo pid=,ppid=,rss=,time=` (runtime_metrics.go:269), enumerating and parsing every process on the node per action. The snapshot is only ever consumed when the APEXLOAD_BROWSER_PROCESS_METRICS env flag is on (runtime_metrics.go:48-51), so with the flag off (the default) this is a pure-waste fork/exec + full-process-table parse per action; with it on it happens twice per action.

**Evidence:** if b.clientMetricsEnabled {
	processBefore, _ = b.captureBrowserProcessSnapshot(actionCtx)
}
// runtime_metrics.go:269: exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,rss=,time=").Output()

**Hot path:** Every action of every canary/client-metrics browser VUser for the whole run. On a K8s generator node with hundreds of processes (each Chromium spawns many), fork/exec + parse costs several ms and heavy allocations per action, and each Chromium child inflates the ps output further.

**Verification notes:** Confirmed end-to-end: browser.go:156-159 captures the snapshot on every Execute whenever clientMetricsEnabled (forced on by experience canaries at config.go:160-165), and processBefore's ONLY consumer is captureBrowserProcessMetrics behind envFlagEnabled("APEXLOAD_BROWSER_PROCESS_METRICS") (runtime_metrics.go:48-51,62-64), so with the flag off it is provably dead fork/exec of `ps -eo pid=,ppid=,rss=,time=` + full-table parse per action (runtime_metrics.go:269, no caching/sampling; the rootPID<=0 early-out never fires in real runs since globalPlaywright is set at pool launch). Two aggravators the finding missed: the ps exec runs after result.StartTime (browser.go:144) and before dispatch, so its cost inflates the canary's reported action Duration (measured at :179) — skewing the very experience metric canaries exist to collect; and terminal actions (:163-169) return before finalizeActionResult, discarding the snapshot even with the flag on. Downgraded impact from high to medium: browser VUsers are canaries by design (small counts, per repo ground-truth), action rates are sub-Hz to a few Hz, and in a K8s pod `ps -e` sees the pod PID namespace (tens of Chromium processes) not the whole node — so this is real per-action waste plus metric skew, not a platform-scale CPU hotspot.

**Refined fix:** Change browser.go:157 to `if b.clientMetricsEnabled && b.processMetricsAttributionEnabled() {` — exactly mirrors the after-side guard at runtime_metrics.go:48; zero behavior change either way (flag off: value provably unused; flag on: identical). Two optional follow-ups when the flag IS on: (1) capture the before-snapshot prior to setting result.StartTime (or subtract its elapsed time) so ps exec latency stops inflating measured canary action durations; (2) replace `ps -e` with a direct /proc walk of the Playwright subtree (or cache the tree PIDs and re-read only their /proc/<pid>/stat) to avoid fork/exec and full-table parses; a min-interval sample cache also works but coarsens per-action CPU attribution.

**Risk:** None when the flag is off (the snapshot is provably unused). With the flag on, sampling changes per-action CPU attribution granularity.

### 91. HAR finalize buffers the whole session HAR (with embedded bodies) and re-parses it as map[string]any

- Location: `pkg/protocol/browser/har_capture.go:118`
- Category: memory
- Finder: sup:browser-protocol-per-step
- Verified impact: medium
- Confidence: high
- Effort: M

**Description:** finalizeHARCapture reads the entire working HAR into memory with os.ReadFile, then scrub.HAR json.Unmarshals it into a generic map[string]any tree and re-marshals (scrub.go:316-350). RecordHarContent is always HarContentPolicyEmbed (har_capture.go:57), so the working file contains every response body of the entire session. For a long canary session under `always`/`on_sla_breach` policy (or an `on_error` session that errored), the file can reach hundreds of MB; interface{}-tree decoding costs roughly 5-10x the file size in transient heap, all at once at context close.

**Evidence:** data, err := os.ReadFile(workingPath)
...
scrubbed, err := scrub.HAR(data, scrub.Policy{})  // full-document json.Unmarshal into map[string]any

**Hot path:** Runs at every browser-session close (per VUser teardown, and per arrival in open workloads). The buffer size scales with the whole session's network traffic including embedded bodies, so long soak canaries produce the biggest spike exactly when the generator is already busy tearing down.

**Verification notes:** Verified the exact code: har_capture.go:118 os.ReadFile of the whole working HAR, :125 scrub.HAR, :57 unconditional HarContentPolicyEmbed with no size cap, and scrub.go:316-351 full json.Unmarshal into map[string]any + re-marshal. The path runs once per browser-session close (Browser.Teardown via runProtocolTeardown), not per iteration, and browser VUsers are low-concurrency canaries with the feature default-off — so it is not a classic hot path and the claimed 5-10x heap multiplier is overstated (~3-5x realistic since embedded bodies are single base64 strings). It is still a genuine unbounded-memory risk: a long soak canary under always/on_sla_breach accumulates a multi-hundred-MB embedded-body HAR, and all sessions tear down near-simultaneously at test end, so coincident 3-5x spikes can OOM a memory-limited generator pod exactly during result flush.

**Refined fix:** Do the cheap, safe mitigations first: (1) os.Stat the working HAR before ReadFile and enforce a configurable size budget (e.g. 256MB); over budget, discard + slog.Warn, consistent with the existing fail-closed 'discarding capture to avoid persisting secrets' behavior. (2) Add a browser_har_content knob (embed|omit) so soak/always policies can drop bodies — but do NOT offer Playwright's 'attach' mode: it writes bodies as sidecar files that scrub.HAR never sees, creating a secrets-persistence hole. (3) Optionally serialize finalizeHARCapture calls process-wide with a small semaphore so coincident teardowns don't multiply the spike. The original streaming json.Decoder scrub is the right long-term fix but needs parity tests against scrub.HAR (sensitivity annotation, unknown-field passthrough, single-entry-document handling at scrub.go:322-327), so it should land behind those tests rather than as the first step.

**Risk:** Streaming scrub must preserve scrub-on-write completeness (sensitivity annotation, unknown-field passthrough); a bug could persist secrets or corrupt artifacts, so it needs parity tests against scrub.HAR.

### 92. readDOMSummary forces full-page layout (innerText) via JS eval on every single action

- Location: `pkg/protocol/browser/page_runtime.go:228`
- Category: cpu
- Finder: sup:browser-protocol-per-step
- Verified impact: medium
- Confidence: high
- Effort: S

**Description:** finalizeActionResult → capturePageState → readDOMSummary runs after EVERY browser action, unconditionally (result_finalization.go:19, page_runtime.go:50). The evaluated expression reads document.body.innerText, which forces a synchronous style+layout recalculation of the entire page in the renderer, then splits the full text into an array of every line — all to keep only the first 10 lines for DOMSummary.VisibleText. On heavy pages (large tables, dashboards) this adds tens of ms of renderer CPU per action and skews the page's own performance profile that canaries are trying to measure.

**Evidence:** visible: (document.body?.innerText || '').split(/\n+/).map((line) => line.trim()).filter(Boolean).slice(0, 10)

**Hot path:** Every action of every browser VUser, success or failure, regardless of configuration — the single most-executed code path in the package after the action itself.

**Verification notes:** Verified the exact expression at pkg/protocol/browser/page_runtime.go:228 and the unconditional call chain Execute (browser.go:183) → finalizeActionResult (result_finalization.go:19) → capturePageState (page_runtime.go:50) → readDOMSummary; no config or runMode gate exists (runMode only gates self-heal at self_heal.go:210). innerText forcing full-page style+layout recalc is real, and the skew claim is confirmed for client metrics — captureClientMetrics runs AFTER the eval against a pre-action process snapshot (runtime_metrics.go:366), so the forced-layout CPU is attributed to the action's CPU delta, which browser_experience_canary force-enables (config.go:160-165); however, response-time Duration is NOT skewed (captured at browser.go:179 before finalize). Impact is medium not high because browser VUsers run at canary scale (small counts), though the path is the hottest in the package and the pollution hits exactly the fidelity metric the canary exists to measure.

**Refined fix:** Gate visible-text capture on run mode plus error state rather than removing it: pass includeVisible := (b.runMode != browserRunModeLoad) || forceArtifacts from capturePageState into readDOMSummary (forceArtifacts == actionErr != nil is already in the signature at page_runtime.go:49), and when false evaluate only {url, title, readyState} — no innerText, no forced layout. This preserves full VisibleText fidelity where it is actually consumed on success paths (BrowserReplayDebugger.tsx:431/478 and replay_diagnosis.go:206 are replay-mode consumers) and on error evidence/snapshots, while eliminating the per-action forced layout in load mode. The suggested TreeWalker/textContent rewrite is riskier for less gain: filtering hidden text needs per-node style checks that themselves force style/layout recalc, and unfiltered textContent leaks display:none content into redaction-scrubbed archives.

**Risk:** textContent includes visually hidden text, so VisibleText fidelity changes slightly for the diagnose UI; gating to error-only removes per-step visible-text from successful replay step results.

### 93. Failure and extended-log paths re-run full action resolution just to build log attributes

- Location: `pkg/engine/vuser.go:421`
- Category: cpu
- Finder: engine-vuser
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** When an http_sequence action fails (or extended logging is on), the log call invokes vu.resolveAction(action) inline — re-interpolating the entire action (all headers, params including the full nested requests array, body) with the double regex pass described above, purely to extract a target/body for the log line. Line 428 does the same for every successful action at extended level. Besides the cost, re-resolution draws fresh random/each_occurrence values, so the logged request does not match what was actually sent.

**Evidence:** if logCfg.OnErrorOnly || logCfg.Level == "extended" {
	logger.Error("action failed", actionLogAttrs(action.Name, vu.resolveAction(action), result, logCfg, true)...)
}

**Hot path:** Error storms are exactly when generators are already stressed: an SUT returning failures at scale makes every failed http_sequence pay a second full interpolation of its entire child-request array, per failure, per VUser.

**Verification notes:** Confirmed verbatim at pkg/engine/vuser.go:421/428: both http_sequence log branches call vu.resolveAction(action), which re-interpolates the full nested params.requests array with two regex passes per string, yet actionLogAttrs only reads Target/Body (never Params) and only when the parameter_substitution extended option is on — so the work is essentially all wasted. Verified the side effect is worse than claimed: both interpolation passes call resolveTemplateValue, so each_occurrence counters advance (parameters.go:327), unique values are consumed (line 390), and vu.rng is drawn — enabling on_error_only logging perturbs the parameter sequence of subsequent real requests, not just the logged content. Path is gated behind opt-in log config (OnErrorOnly/extended default off) but sits in the per-action VUser loop and fires per failure during error storms when enabled, so 'low' impact is fair (pure CPU alone would be negligible; the parameter-state mutation is the real reason to fix).

**Refined fix:** Original fix is correct; refine as: in executeHTTPSequenceAction (vuser_execute.go:259), capture the redacted target/body of the first failing (or last) resolved child onto the aggregate protocol.Result during execution (children are already resolved at line 286), then have vuser.go:421/428 pass result-carried fields to actionLogAttrs instead of calling vu.resolveAction(action). This both removes the wasted double interpolation and fixes the parameter-state mutation (each_occurrence counter/unique-value/RNG advancement) caused by log-path re-resolution — and improves log truthfulness, since the sequence parent's own Target/Body are typically empty today. Do NOT settle for merely gating the resolve behind the parameter_substitution option check: that reduces frequency but leaves the state-mutation bug when the option is enabled. Confirm no test asserts on the re-resolved log attrs; deterministic-replay-seed tests should only get more stable.

**Risk:** Log content changes slightly (now reflecting the actually-sent request — an improvement); confirm no test asserts on the re-resolved values.

### 94. WebSocket hub re-unmarshals every broadcast payload just to read the 3-field routing envelope

- Location: `pkg/api/websocket.go:136`
- Category: cpu
- Finder: metrics-pipeline
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** The hub's single dispatch goroutine calls json.Unmarshal on the complete broadcast message to extract test_id/project_id/broadcast_all. encoding/json still tokenizes the entire document even when only three fields are kept, so every metrics_update — which embeds a full LiveMetrics with per-transaction maps and can be hundreds of KB — is fully parsed a second time right after BroadcastJSON marshaled it. This runs on a serialized loop: parse time delays delivery of all other broadcasts and pushes the hub toward droppedBroadcasts under load.

**Evidence:** case msg := <-h.broadcast:
	...
	_ = json.Unmarshal(msg, &envelope)  // full re-parse of a payload the caller just marshaled

**Hot path:** Once per broadcast in the single hub goroutine: N generator publishes/sec × running tests × threshold alerts/latency updates; payload size scales with transaction-name cardinality.

**Verification notes:** Confirmed: websocket.go:136 fully re-unmarshals every broadcast in the single hub goroutine right after BroadcastJSON marshaled it, with no mitigation; metrics_update payloads embed full LiveMetrics (6 per-transaction maps) at 1/sec per local test and per-generator-batch in distributed runs (distributed_stream.go:255, generator publishInterval=1s), so it is genuinely per-broadcast hot. However magnitude is overstated: realistic rates (tens of broadcasts/sec, 50-300KB) cost single-digit % of one core and sub-2ms serialized delay, the 256-slot channel makes the droppedBroadcasts claim theoretical, and the redundant parse is dwarfed by the marshal/persistence/artifact work already done per broadcast. Real waste, worth the small fix, but low impact in typical deployments — approaches medium only at high generator-count x test-count x transaction-cardinality.

**Refined fix:** Change the broadcast channel to carry a typed struct {testID, projectID string; broadcastAll bool; payload []byte}. Add a scoped API (e.g. BroadcastScoped(testID, projectID, v)) used by hot callers like publishLiveMetrics; keep Broadcast([]byte)/BroadcastJSON as a fail-safe fallback that extracts the envelope once at enqueue time in the caller's goroutine (moving, not changing, the parse), so any missed call site keeps identical filtering behavior instead of breaking it. Also add test_id/project_id/broadcast_all to websocketFanoutEnvelope (pkg/api/websocket.go:71) populated by publishFanout, so the Redis HA consumer at server.go:1230 — which already unmarshals the envelope — hands typed routing to broadcastLocal without re-parsing the inner payload either.

**Risk:** All Broadcast/BroadcastJSON call sites and the Redis fanout path must populate the routing metadata; a missed site would change filtering behavior (message delivered to no one or everyone).

### 95. Latency prober re-derives endpoints from all script actions — including full inline-CSV parses per action — every 5 seconds

- Location: `pkg/api/latency.go:112`
- Category: caching
- Finder: metrics-pipeline
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** measureNetworkLatency (fired every 5s per running test from broadcastMetrics, pkg/api/metrics.go:612) calls latencyEndpointsForTest → addParameterizedLatencyEndpoints, which loops over every action in every group's script and regex-resolves `{{var.x}}` prefixes. For each matching action it calls firstScriptParameterValue → firstScriptDataSourceValue → parseInlineDataRows, which csv.ReadAll-parses the ENTIRE inline data source, trying up to 4 delimiters — with no memoization, so the same parameter's CSV is re-parsed once per referencing action. A 100k-line imported script whose base URL is parameterized from a large inline datapool turns each 5-second probe cycle into (actions × datapool bytes) of parsing, on the controller.

**Evidence:** for _, action := range script.Actions {
	...
	if endpoint, ok := resolveLatencyEndpointTemplatePrefix(raw, script); ok {  // → firstScriptDataSourceValue → parseInlineDataRows → reader.ReadAll(content)

**Hot path:** Every 5 seconds per running test for the whole run; scripts and data sources are immutable during a run, so all of this work computes the same answer every cycle. Repo memory confirms ~98k-line imports are a supported case.

**Verification notes:** Mechanism confirmed: broadcastMetrics (metrics.go:612) fires measureNetworkLatency every 5s per running test, and latencyEndpointsForTest re-scans every action with zero memoization, with each {{var.x}}-prefixed target triggering a full csv.ReadAll of the parameter's entire InlineData (up to 4 delimiter retries), verified at latency.go:105-248; latency_test.go:261-302 shows the {{var.url}}+DataSource trigger is a real-world script shape. However, the headline worst case is largely unreachable today: current LR enterprise-archive imports rewrite .prm/.dat-backed refs to {{param.name}} (enterprisearchive/parameters.go:222-289), which resolveLatencyEndpointTemplatePrefix rejects (var.-only), JMeter declares no dsl.Parameters, devweb uses {{data.*}}, and paramize's inline source is 2 rows — so the CSV-storm needs a legacy pre-rewrite import or hand-authored script with a large inline datapool behind a {{var.}} base URL. Guaranteed universal cost is just a per-action regex prefix scan every 5s (microseconds at 20k actions), hence low probability-weighted impact despite a genuinely unbounded narrow worst case.

**Refined fix:** Two-layer fix, cheapest first: (1) replace parseInlineDataRows/ReadAll in the latency path with a streaming reader that csv.Read()s only until 2 non-empty records are collected (header + first data row is all firstScriptDataSourceValueWithDelimiter ever uses) — this bounds cost by row width, not datapool size, with zero cache-invalidation risk; (2) memoize firstScriptParameterValue results per parameter name in a map created at the top of latencyEndpointsForTest and threaded through addLatencyEndpoint, so each parameter resolves at most once per 5s cycle regardless of action count. Caching the whole endpoint set on ManagedTest at first probe is also safe (scripts immutable mid-run) but touches tr.mu lock ordering around the lazy tr.scripts lookup (latency.go:79-84) for marginal extra gain — the per-cycle memo + first-rows parse already reduces the pathological case to O(actions) regex checks, which is negligible.

**Risk:** None functionally — endpoints cannot change mid-run; only cache invalidation on test restart with an edited script needs care.

### 96. BrowserResources accumulates every network entry of every transaction just to keep the top 50

- Location: `pkg/api/archive_report.go:141`
- Category: allocations
- Finder: archive-report
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** Inside the per-transaction report loop, browserResourceTimingsFromTransaction converts all NetworkEntries of each transaction into BrowserResourceTiming structs and appends them to report.BrowserResources unbounded; finalizeBrowserResourceTimings (line 193) then sorts the whole slice and truncates to 50. A browser-mode run with ~100 resources per page across tens of thousands of page transactions allocates millions of structs (each with several strings) that are immediately discarded.

**Evidence:** report.BrowserResources = append(report.BrowserResources, browserResourceTimingsFromTransaction(tx)...)
...
report.BrowserResources = finalizeBrowserResourceTimings(report.BrowserResources, 50)

**Hot path:** Executes on every buildRunArchiveReport call for browser-protocol runs — the flagship browser-recorder/TruClient workloads generate dense NetworkEntries per transaction.

**Verification notes:** Confirmed the mechanism: archive_report.go:141 appends every transaction's NetworkEntries as BrowserResourceTiming structs and archive_browser.go:171 stable-sorts the whole slice before truncating to 50; every browser action persists its entries (result_finalization.go:76, archive_writer.go:140) so N is genuinely unbounded per run. Impact is lower than claimed because buildRunArchiveReport already loads the entire transactions JSONL (with full NetworkEntries incl. header maps/bodies) into memory via readAllArchiveTransactions, so the ~176B/entry timing slice (string headers shared, not deep-copied) is a constant-fraction overhead on an already O(archive) function, and all callers (report endpoint, release-trend/SLA loops) are on-demand, not the live-run path. Worth fixing since release-trend/attachReleaseTrend rebuild reports for every run in a release per request, but it is a secondary contributor behind the full-archive load.

**Refined fix:** A size-50 min-heap keyed only on DurationMs is NOT byte-safe: the current sort.SliceStable preserves input order for entries with identical (DurationMs, URL), which a heap discards. Byte-identical bounded alternative: accumulate into a local slice; whenever it exceeds ~4x the limit, stable-sort with the existing comparator and drop only entries strictly worse than the limit-th element's (DurationMs, URL) key (retain all ties at the cutoff), then finalize as today. Memory becomes O(limit + ties) and output is unchanged. Note this only trims the timing slice; peak memory of buildRunArchiveReport remains dominated by readAllArchiveTransactions loading all NetworkEntries, so consider it a companion to (not substitute for) streaming the transactions file if archive-report memory ever becomes a real problem.

**Risk:** Tie-breaking order (DurationMs then URL) must match the current stable sort to keep report output byte-identical.

### 97. stableMappingID compiles a regex 5 times per source-review mapping in the LR import hot loop

- Location: `pkg/convert/sourceview/source_review.go:236`
- Category: allocations
- Finder: importers
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** stableMappingID calls regexp.MustCompile(`[^a-z0-9_.\[\]-]+`) inside its per-part loop — 5 fresh compiles per mapping. It is reached via NormalizeMapping from enterprisearchive's appendSourceReviewMapping/sourceReviewMappingForFunction, which run for essentially every parsed source function during Convert, so a large VuGen import compiles the identical regex tens of thousands of times, generating avoidable CPU and garbage inside the conversion loop.

**Evidence:** for index, part := range parts {
    part = strings.ToLower(strings.TrimSpace(part))
    part = regexp.MustCompile(`[^a-z0-9_.\[\]-]+`).ReplaceAllString(part, "-")
    ...
}

**Hot path:** One mapping (minimum) per source function in every enterprise-archive import; a 100k-line script yields tens of thousands of mappings → ~100k+ regex compilations per import. Also hit by every other importer that builds sourceview mappings without explicit IDs.

**Verification notes:** Confirmed at pkg/convert/sourceview/source_review.go:236: regexp.MustCompile runs inside the 5-part loop, and stableMappingID fires for essentially every mapping because no constructor sets Mapping.ID (NormalizeMapping line 99-101); ~40 appendSourceReviewMapping sites run per parsed function in enterprisearchive Convert and all other importers flow through sourceview.Build → NormalizeMappings. No cap on mapping count (only files are capped) and no precompiled var exists. Magnitude is smaller than claimed, though: it is once-per-import (not per-VUser), the double-normalize path only compiles on the first pass (5/mapping, not 10), and a worst-case 10-20k-mapping import wastes ~100-300 ms CPU and ~100-200 MB transient garbage — measurable on large VuGen imports but invisible on typical ones, so impact is low rather than medium.

**Refined fix:** Hoist to a package-level var matching the file's existing sourceRedactors convention: `var mappingIDInvalidChars = regexp.MustCompile(`[^a-z0-9_.\[\]-]+`)` next to sourceRedactors, and in stableMappingID use `part = mappingIDInvalidChars.ReplaceAllString(part, "-")`. Identical semantics, zero risk.

**Risk:** None — identical pattern, pure hoist.

### 98. Legacy C parser rescans the whole accumulated block for ");" on every line (quadratic for large inline bodies)

- Location: `pkg/convert/enterprisearchive/parser.go:206`
- Category: algorithmic
- Finder: importers
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** parseActionFileLegacy appends each line to currentBlock and then runs strings.Contains(blockStr, ");") over the ENTIRE accumulated block on every line. For a call whose block is large — a web_custom_request carrying a multi-hundred-KB "Body=" literal split across thousands of continuation lines, exactly the shape of big VuGen exports — this is O(block_size^2 / line_len): a 1MB body over ~15k lines scans ~7.5GB. The legacy scanner is the common fallback since the strict cc.Parse AST path rejects the whole file on any LR macro or vendor pragma.

**Evidence:** currentBlock.WriteString(trimmed)
currentBlock.WriteString(" ")
...
blockStr := currentBlock.String()
if strings.Contains(blockStr, ");") {
    fn, err := parseFunctionBlock(blockStr, ...)

**Hot path:** Runs per line of every action file when the AST parser fails (common for real LoadRunner scripts per the ParseActionFile comment); multi-MB imported scripts with large inlined request bodies are the stated scale case.

**Verification notes:** Confirmed: parser.go:205-206 rescans the full accumulated block with strings.Contains(blockStr, ");") on every appended line, O(lines x block_size) per block with no mitigation; the legacy scanner is the documented common fallback (parser.go:127-132) invoked once per action file at import (zip.go:228). Downgraded impact from medium to low: only a single block wrapping a large inline Body= across thousands of lines triggers it, and Go's SIMD 2-byte Contains makes the stated 1MB/15k-line case (~7.5GB scanned) roughly 1-3s of one-off import latency; multi-MB inline bodies (tens of seconds) are uncommon since VuGen externalizes large binary bodies. It is on the importer's main per-line loop for multi-MB LR imports, but it is import-time one-off, not runtime.

**Refined fix:** Simpler than proposed: replace the whole-block check with `if strings.Contains(trimmed, ");")` on the current line only. This is provably semantics-identical — lines are joined with a trailing space (parser.go:199-200), so ");" can never straddle a line boundary, and any earlier occurrence would already have terminated the block on a previous iteration. No carry-over byte tracking is needed. Separately (out of scope but adjacent): bufio.NewScanner at parser.go:159 uses the default 64KB max token size, so a single body line over 64KB silently truncates parsing — worth a scanner.Buffer bump plus scanner.Err() check.

**Risk:** Low — must handle the terminator straddling a line boundary; add a case to the existing legacy-parser tests.

### 99. Goal-driven ScaleVUsers probes and adjusts every shard target serially, blocking the metrics-consumer goroutine

- Location: `pkg/api/distributed_execution_backend.go:750`
- Category: network
- Finder: distributed
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** ScaleVUsers, invoked by the goal controller on its 5s cadence (applyRemoteMetricsBatch -> applyGoalControllerUpdate -> b.ScaleVUsers per group), first probes generator load one target at a time (probeGeneratorLoad = open heartbeat stream + send + recv, fresh context up to b.timeout=10s each) and then issues AdjustVUsers one target at a time. A single scale event on M shard targets costs 2*M sequential RPC round trips per group, multiplied by the number of groups being rescaled. Because it runs on the consumeMetrics goroutine that called applyRemoteMetricsBatch, that generator's metrics stream stops being drained for the whole sequence, backing up gRPC flow control and inflating metric lag exactly when the closed-loop controller needs fresh TPS data.

**Evidence:** for _, target := range targets {
    client := clients[target]
    ...
    ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
    snapshot := b.probeGeneratorLoad(ctx, target, client)
    cancel()

**Hot path:** Goal-oriented runs evaluate scaling every 5 seconds; each decision fans out serially across all shard targets while a metrics stream sits undrained.

**Verification notes:** Verified the full chain: consumeMetrics goroutine (distributed_stream.go:232) synchronously reaches b.ScaleVUsers via applyRemoteMetricsBatch/applyGoalControllerUpdate, which does 2xM serial RPCs per group (probe loop at distributed_execution_backend.go:750-760 with fresh 10s-default timeouts, adjust loop at 767-790), gated to a real 5s cadence by shouldEvaluateScale (goal_controller.go:470); the blocked goroutine stops draining that one generator's metrics stream. Downgrading impact to low: M is bounded small, healthy-cluster stalls are tens-to-hundreds of ms per 5s, and sibling per-target consumers keep draining and independently run the goal evaluation, so controller input only goes stale for the single blocked target. The genuinely bad case (2xMx10s stall when a generator hangs) only occurs when the fleet is already degraded. Also noted: lastScaleAt is stamped only after scaling completes, so the serial stall widens an existing race where sibling consumers can concurrently decide ShouldScale.

**Refined fix:** Prefer moving the whole scale operation off the consumeMetrics goroutine onto a single dedicated scale worker per test with a latest-wins queue (coalesce {group->target} decisions): this both unblocks metrics draining entirely and serializes scale ops, closing the sibling-consumer concurrent-ShouldScale race that lastScaleAt-after-completion leaves open. Within ScaleVUsers, additionally parallelize the probe loop (errgroup, and use a shorter probe timeout, e.g. 2s, since a placement probe should not inherit the 10s control timeout; probe failure already degrades gracefully to a default snapshot). Keep AdjustVUsers serial to preserve the existing rollback-to-previous ordering contract; parallelizing probes alone removes half the round trips with zero semantic risk.

**Risk:** Concurrent AdjustVUsers changes rollback ordering semantics on partial failure; keep the successes list mutex-protected and preserve the roll-back-to-previous contract.

### 100. Generator pod termination watcher relists all pods from the Kubernetes API every 5 seconds instead of using a watch

- Location: `pkg/api/generator_pod_watcher.go:45`
- Category: polling
- Finder: distributed
- Verified impact: low
- Confidence: high
- Effort: M

**Description:** RunGeneratorPodTerminationWatcher ticks every 5s (defaultGeneratorPodTerminationWatchInterval) and performs a full ListPods against the apiserver plus a full GeneratorDrainStates map rebuild each tick, forever, even when nothing is terminating. Separately, enrichGeneratorRestartability repeats the same ListPods on every systemInventory call (readiness, Prometheus, fleet endpoints). With large autoscaled fleets (hundreds of generator pods) this is a sizable recurring LIST load on the apiserver and repeated JSON decode work on the controller, where a shared informer/watch would deliver only deltas.

**Evidence:** pods, err := s.kubeRestarter.ListPods(ctx, s.k8sRestart.Namespace, s.k8sRestart.GeneratorSelector)
... (ticker := time.NewTicker(interval) with default 5s at line 10)

**Hot path:** Fixed 5s cadence for the controller lifetime, plus one additional full relist per inventory-driven request (readiness probes, scrapes, dashboard polling); cost scales with generator pod count.

**Verification notes:** Confirmed: generator_pod_watcher.go:45 issues an uncached ListPods (raw HTTP GET + full JSON decode, kubeops/client.go:166) every 5s for the controller lifetime, and enrichGeneratorRestartability (system_inventory.go:192) repeats the LIST on every systemInventory call from readiness probes, Prometheus scrapes, fleet handlers, and dashboard polls — the inventory cache at system_inventory.go:117 never short-circuits the next LIST. However it is gated to leader + distributed + k8sRestart.Enabled, is a control-plane loop whose cost scales with pod count (typically tens) rather than load intensity, and totals under ~0.5 LIST/s, so impact is low, not on the load-execution hot path. The suggested client-go informer contradicts the documented single-seam design (generator_pod_watcher.go:12-15) and is heavier than the problem warrants.

**Refined fix:** Skip client-go informers (they add the client-go dependency, watch RBAC verbs, and cache-sync handling, contradicting the deliberate "no second client/RBAC surface" design noted in generator_pod_watcher.go). Instead wrap the existing kubeRestarter.ListPods seam with a short-TTL (3-5s) single-flight cache keyed on namespace+selector, so the 5s watcher tick and all concurrent systemInventory-driven callers (readiness, Prometheus, fleet, dashboard) share one LIST per interval. This collapses N recurring LISTs to ~1 per 5s, preserves the ListPods test interface, and needs no new RBAC.

**Risk:** Introduces watch re-establishment/backoff handling; the existing KubeRestarter seam would need a cache-backed implementation, and tests use the ListPods interface.

### 101. TestExecutionSummary re-runs ~15 filter+sort passes over all transactions in the render body on every poll tick

- Location: `dashboard/src/components/TestExecutionSummary.tsx:25`
- Category: cpu
- Finder: dashboard-render
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** The component body computes topServerTelemetry, topHTTPDecomposition, topHTTPDependencyHealth, messagingTransactions, eight JMS/messaging profile builders, eventHubsProducers, messagingReliability, protocol-stream and SOAP aggregations — each a full filter/sort (O(n log n)) pass over the transactions array or dependency_health map — with zero useMemo, and the component itself is not wrapped in React.memo. Its parent TestExecution re-renders at least every 2s from useExecutionTestPolling plus on every WS metrics update and every unrelated UI state change, so all of these passes re-execute each time even though the rendered output only shows top-4/top-8 slices.

**Evidence:** const topServerTelemetry = transactions
  .filter((tx) => tx.server_telemetry)
  .sort((left, right) => (right.server_telemetry?.avg_server_processing_ms || 0) - (left.server_telemetry?.avg_server_processing_ms || 0))
  .slice(0, 4)

**Hot path:** Mounted for the whole duration of every watched live run; transactions holds one entry per transaction name, which for large imported scripts (100k+ line LR/JMeter imports) reaches hundreds to thousands of entries, multiplied by ~15 passes per render at >=0.5Hz.

**Verification notes:** Code is exactly as claimed: TestExecutionSummary.tsx has ~20 sort call sites (9 in the render body at lines 25-91 plus profile builders doing 4 clone+sorts each) with zero useMemo/React.memo, and the parent re-renders every 2s (useExecutionTestPolling) plus on WS metrics/latency updates and UI state changes. However, hotness is overstated: DashboardWorkspace renders only the active tab's cards and 'live-summary' sits in the non-default Diagnostics tab, so the passes run only while that tab is open; even at a pessimistic 2,000 distinct transactions, ~20 passes cost low-single-digit ms per 2s tick (<1% CPU). During a live run the inputs genuinely change every poll, so recomputation is mostly semantically required — the waste is limited to renders from unrelated UI state, and the unsliced Transaction Breakdown table (lines 964-981, all transactions x 10 columns) dominates render cost anyway, which the suggested fix does not touch.

**Refined fix:** The suggested useMemo/React.memo inside TestExecutionSummary is defeated by the parent: TestExecution.tsx line 447 rebuilds summaryTransactions via Object.values(statusMetrics.transactions) and lines 436-445 rebuild statusMetrics via a fresh spread on every render, so the memo deps get new identities every render and never hit. To make it effective: (1) in TestExecution, wrap statusMetrics and summaryTransactions in useMemo keyed on [metrics, latestMetrics]; (2) then hoist the derivations in TestExecutionSummary into useMemo (or a single-pass helper) and wrap the component in React.memo. If optimizing this component at high transaction counts, the higher-value change is capping or virtualizing the unsliced Transaction Breakdown table, which renders every transaction row per tick and dwarfs the filter/sort cost.

**Risk:** None significant — the builders are pure; only ensure the memo deps include baseline/goalConfig where used.

### 102. aggregateHistory flattens all tests' metric histories in the render body on every DashboardPage render

- Location: `dashboard/src/components/DashboardPage.tsx:1039`
- Category: allocations
- Finder: dashboard-render
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** aggregateHistory is computed directly in the render body without useMemo: Object.values(metricsHistory).flat() allocates a flattened array over every test's WS history (capped at 300 points per test in App.tsx:261, but unbounded in test count per session) before slicing the last 60. DashboardPage re-renders every 3s from its poll and on every WS metrics_update (App-level metricsHistory state changes), so this allocation and copy runs several times per second during live tests, and its fresh identity forces the aggregate AreaChart (line 1324) to reprocess data each time.

**Evidence:** const aggregateHistory = Object.values(metricsHistory).flat().slice(-60)

**Hot path:** Executes on every DashboardPage render — at least every 3s from the poll and once per WS metrics tick per live test while the dashboard tab is open.

**Verification notes:** Confirmed: DashboardPage.tsx:1039 is exactly `const aggregateHistory = Object.values(metricsHistory).flat().slice(-60)` in the render body with no useMemo; App.tsx:259-263 caps each test at 300 points but never prunes keys, so the flatten grows with every test observed in the session. DashboardPage re-renders at least every 3s from its own poll (setTests gets a fresh array identity each tick, DashboardPage.tsx:563/569) plus once per WS metrics_update via the metricsHistory prop, so the allocation and the fresh-identity Recharts reprocess (AreaChart at line 1324, PureComponent-based, so stable identity would let it bail on poll renders) run several times per second while the dashboard is open during live runs. Impact is honestly small — thousands of pointer copies per render plus a ~60-point sparkline reprocess, and during live ticks the chart must re-render anyway since data genuinely changed — so "low" is the right ceiling; the risk claim is even safer than stated because the Area already has isAnimationActive={false}, so there is no animation-continuity concern at all.

**Refined fix:** One line: `const aggregateHistory = useMemo(() => Object.values(metricsHistory).flat().slice(-60), [metricsHistory])`. Skip the suggested "flatten only the tail" refinement — at ≤300 refs/test it adds complexity for microseconds. The bigger win for long-lived monitoring sessions is upstream: prune completed tests' entries from metricsHistory in App.tsx (the map is append-only today), which also bounds the flatten. Separately note (out of scope) that Object.values key order means the last-60 slice is not time-ordered across concurrent tests, so the aggregate chart can interleave stale points; a timestamp-sorted merge would fix that if it ever matters.

**Risk:** None — pure derivation; only the chart's animation continuity could change if identity becomes stable (desirable).

### 103. ListTests takes the global WRITE lock and refreshes capacity advisories on every call, serializing 5s-polled read paths against live-test execution

- Location: `pkg/api/tests.go:283`
- Category: concurrency
- Finder: background-loops
- Verified impact: low
- Confidence: high
- Effort: M

**Description:** ListTests acquires tr.mu.Lock() (exclusive) — not RLock — because it mutates state via refreshPendingQueueMetadataLocked and refreshCapacityAdvisoryLocked for every QUEUED/PENDING test on each call. ListTests is called by the run-calendar snapshot (run_calendar.go:494), usage accounting, entitlements, and the tests-list UI. Under the Monitoring page's 5-second poll, every read pass excludes all other readers and contends with the executor goroutines updating live-test state under the same mutex.

**Evidence:** func (tr *TestRunner) ListTests() []*ManagedTest {
    tr.mu.Lock()
    defer tr.mu.Unlock()
    tr.refreshPendingQueueMetadataLocked()
    for _, t := range tr.tests { if t.Status == "QUEUED" { tr.refreshCapacityAdvisoryLocked(t) } ... }

**Hot path:** Called at least once per 5s Monitoring poll (via run-calendar) plus per tests-page poll per viewer, while thousands of concurrent VUsers drive frequent state/metric updates that need the same exclusive lock. Multiple dashboard viewers multiply exclusive-lock acquisitions on the control plane's single hottest mutex.

**Verification notes:** Confirmed at pkg/api/tests.go:282-297: ListTests (and ListTestsFiltered/ByContext) take tr.mu.Lock() exclusive and mutate queue metadata + capacity advisories per call; MonitoringPage polls at 5s (MonitoringPage.tsx:458) with one tick calling ListTests 3-5 times (run-calendar, fleet x2, restart, entitlements). However, the finding's hotness premise is wrong: VUser metric fan-in aggregates inside engine.MultiGroupEngine, not under tr.mu — the executor only takes tr.mu ~2x/sec per running test (broadcastMetrics 1s ticker), and estimateCapacityAdvisoryForAssignment is already memoized (capacity_advisory_cache.go, 5s TTL + generation). Net contention is tens of short exclusive acquisitions/sec — real architectural smell with a growing tail (O(all-tests) + O(pending × all-tests) quota scans under the lock, tests map never auto-shrinks), but not a measurable bottleneck today, so impact is low not medium.

**Refined fix:** Do not just swap to RLock — the refresh helpers mutate shared state, so that is a data race. First move refreshes to the write paths that change their inputs: status transitions (enqueue/start/complete/stop) for queue positions/PendingReason (note refreshPendingQueueMetadataLocked also RESETS QueuePosition/PendingReason on non-pending tests, so transitions out of PENDING must be covered) and settings changes for advisories (invalidateCapacityAdvisoryCacheLocked already bumps the generation there). Then ListTests can take RLock and return data as-is; staleness is acceptable because advisories are re-validated at launch by evaluateStartGatesLocked (the cache's own doc comment says so). Lower-risk interim: under RLock, check whether any QUEUED/PENDING test exists and only escalate to Lock when a refresh is actually needed — steady state (no queue) becomes purely read-locked. Also consider returning cloned ManagedTest snapshots: handlers currently serialize live pointers outside the lock while publishLiveMetrics mutates them under it, an adjacent latent race this refactor could close for free.

**Risk:** Queue positions / advisories could be one event stale in list responses; any caller relying on ListTests to opportunistically refresh advisories (preflight paths) must trigger refresh explicitly.

### 104. Browser recorder control panel polls full session snapshot (all steps, selector candidates, thumbnails) every 1.5s with deep clone under recorder mutex

- Location: `dashboard/src/components/BrowserRecorderControlPanel.tsx:186`
- Category: polling
- Finder: sup:recorder-session-payload-and-poll
- Verified impact: low
- Confidence: high
- Effort: M

**Description:** The panel polls api.getBrowserRecording every 1.5s; the handler returns fullSnapshot() (browser_recording_manager.go:1192), which for a live session calls backend.Snapshot() — a deep clone of every Step including its Values/Modifiers/FramePath/Candidates/TargetCandidates slices plus NavEvents/Markers/Annotations, taken while holding the browser recorder's mutex (browser/recorder.go:617-621, events.go:437-462). The JSON response re-ships every step each tick with no since-cursor; with capture_screenshots enabled each step also carries a base64 thumbnail, though the panel renders only the newest 25 steps.

**Evidence:** useEffect(() => {
  if (!live) return
  const interval = setInterval(refresh, 1500)
  return () => clearInterval(interval)
}, [live, refresh])
// backend: clone.Steps = visibleSteps(artifact.Steps)  // full deep clone per poll

**Hot path:** Runs for the duration of every headed-browser recording; long sessions accumulate hundreds of steps, so each 1.5s tick clones and serializes an ever-growing artifact (hundreds of KB to several MB with thumbnails) and briefly blocks the event-capture goroutines on the recorder mutex.

**Verification notes:** Verified every link in the chain: 1.5s setInterval poll (BrowserRecorderControlPanel.tsx:184-188) → handleGetBrowserRecording → Get() → fullSnapshot() → backend.Snapshot() deep clone under r.mu (recorder.go:617-621, events.go:437-462), full step list re-serialized per tick with no since-cursor, opt-in base64 thumbnails inline, panel rendering only 25 live steps, and existing browser_recording_step WS broadcasts. Mechanism is real, but scale is one human-driven interactive session (dozens-to-hundreds of steps, single poller), the mutex hold is microseconds, and payloads only reach MB range with screenshots enabled on unusually long recordings — so I downgrade medium→low; it degrades popup/server smoothness during long recordings but never touches load-test execution. The polling-over-WS choice is a documented deliberate trade-off in the code (lines 181-183), which weakens "oversight" framing but the full-snapshot-with-thumbnails-per-tick part is genuinely wasteful.

**Refined fix:** Keep the poll but shrink it: (1) exclude step thumbnails from the live poll response and serve them via a lazy per-step endpoint (note thumbnails attach asynchronously after step capture via captureThumbnailAsync, so any delta protocol must re-deliver mutated steps, not just new seqs); (2) add a ?steps=recent (summary + last 25 visible steps) mode the panel uses while live, falling back to the full snapshot when stepSearchQuery is non-empty (live search filters over all steps client-side) or when the session leaves live status; (3) skip a since_seq delta protocol unless needed — delete-last-step tombstones, exclude toggles, and late thumbnails make correct delta merging fiddlier than the payoff for a single-user panel.

**Risk:** Standalone popup panel deliberately avoids socket plumbing, so the incremental REST path must handle step updates/deletes (delete-last-step, exclude toggles) not just appends.

### 105. resourceValidators map grows unbounded — one entry per ETag'd response URL for the whole session

- Location: `pkg/protocol/browser/request_policy.go:237`
- Category: memory
- Finder: sup:browser-protocol-per-step
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** rememberResourceValidator stores rawURL→etag for every response carrying an ETag, and is called from two per-response hooks (OnResponse at runtime_context.go:167 and recordNetworkRequest at runtime_context.go:298 — double work per finished request). The map is never trimmed and close() does not clear it; it lives for the whole VUser session. It is only ever read when checkNewerVersions is true (request_policy.go:181-185), which defaults to false, so by default this is write-only memory growth. The sibling networkEntries and wsEvents buffers were explicitly ring-bounded for exactly this reason, but this map was missed.

**Evidence:** b.resourceValidators[rawURL] = etag  // never evicted; populated from OnResponse for every response

**Hot path:** Every HTTP response of every page for the session lifetime. Long-running canary VUsers hitting apps with cache-busting or per-user query strings accumulate an unbounded set of full URLs + etags (easily tens of MB per VUser over a multi-hour soak), multiplied by VUser count per generator.

**Verification notes:** Verified all mechanics: request_policy.go:237 stores rawURL→etag with no eviction; written twice per finished request (runtime_context.go:167 OnResponse + :298 recordNetworkRequest); read only behind checkNewerVersions which defaults false (config.go:40); close() (lifecycle.go) nils consoleEntries/networkEntries/wsEvents but not resourceValidators, and the map persists for the whole VUser session unless NewUserPerIter=true (vuser.go:210) since only Init recreates it. However impact is lower than claimed: growth requires distinct ETag'd URLs (typical static-asset sets plateau at hundreds of entries ~tens of KB; cache-busted URLs rarely carry ETags), and the browser protocol runs low-count canary VUsers each already costing a multi-hundred-MB Chromium — so realistic leak is KB-to-low-MB per VUser, unbounded only in adversarial URL shapes on long soaks.

**Refined fix:** 1) Early-return in rememberResourceValidator unless b.checkNewerVersions (the only consumer) — eliminates default-config growth entirely. 2) Drop the duplicate call at runtime_context.go:298; OnResponse (line 167) already sees every response. 3) Add a size cap (~4096 entries, mirroring browserNetworkEntryRingLimit) with clear-on-overflow or random-evict under the existing b.mu. 4) Nil the map in close() alongside networkEntries. Do NOT strip query strings or hash keys as originally suggested: ETags are keyed to the exact URL including query, so normalizing keys can emit a wrong If-None-Match for a different query variant and cause incorrect 304 revalidation when checkNewerVersions is on.

**Risk:** With checkNewerVersions on, evicted entries mean a lost If-None-Match revalidation for that URL — a benign cache-behavior fidelity change.

### 106. browser_wait_response deep-clones the entire network-entry window every 25ms poll tick

- Location: `pkg/protocol/browser/network_wait_actions.go:40`
- Category: allocations
- Finder: sup:browser-protocol-per-step
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** The dispatchWaitResponse polling loop calls networkEntriesSince on every 25ms tick, which under the lock deep-clones every entry since the action start — including a fresh map allocation for RequestHeaders and ResponseHeaders per entry (protocol.go:179-190) — only to scan them read-only for the first match and throw the clones away. While a busy page loads hundreds of resources, each waiting VUser performs hundreds of entry copies and map allocations 40 times per second.

**Evidence:** if match, ok := firstMatchingNetworkEntry(b.networkEntriesSince(networkStart), matcher); ok {  // inside time.NewTicker(25ms) loop

**Hot path:** browser_wait_response is a recorded-script staple (wait for the XHR after a click); the loop runs at 40Hz for up to the action timeout on every VUser, against a window that can span hundreds of entries on resource-heavy pages.

**Verification notes:** Confirmed end-to-end: dispatchWaitResponse (network_wait_actions.go:36-49) calls networkEntriesSince on every 25ms tick, which under b.mu deep-clones the whole since-action window via protocol.CloneNetworkEntries (protocol.go:179-190) — fresh slice plus two map allocations per entry — while responseEntryMatches reads only value fields (URL/Method/StatusCode/ResourceType), so the clones are discarded unread. Window is entries since action start (not full page history), ring-capped at 2048 (runtime_context.go:24); an unmatched 10s wait on a busy page burns ~400 ticks of O(window) map churn. Downgraded medium→low because browser VUsers run at canary scale in this platform (tens, not thousands — each is a full Playwright browser dwarfing this churn); main real cost is GC noise on a timing-sensitive generator plus 40Hz lock contention with recordNetworkRequest.

**Refined fix:** Original fix is correct and safe (entries are append-only under b.mu; trim only evicts from the front and the absolute-offset translation in networkEntriesSince already handles that). Two refinements: (1) the new locked helper must replicate the offset-translation/clamping logic (local := offset - b.networkEntriesDropped, clamp to 0) rather than taking a local index; (2) keep a scan cursor — after each unmatched tick, advance networkStart to the absolute offset already scanned (b.networkEntriesDropped + len(b.networkEntries) at scan time, returnable from the helper) so each tick scans only newly appended entries, turning per-tick cost from O(window) to O(new). Clone only the matched entry (add a single-entry CloneNetworkEntry) for the ProtocolEvidence payload since the caller-owned copy must not alias the live buffer maps.

**Risk:** Matching now runs under b.mu; the matcher is pure string/int comparisons so hold time is tiny, but a slow custom regex on a huge window slightly extends lock hold versus today.

### 107. browser_ws_wait recompiles the match regex per event and copies the WS ring on every 50ms tick

- Location: `pkg/protocol/browser/ws_observe.go:259`
- Category: cpu
- Finder: sup:browser-protocol-per-step
- Verified impact: low
- Confidence: high
- Effort: S

**Description:** browserWSMatcherFromAction validates the regex with regexp.Compile (line 168) but discards the compiled object, keeping only the string. The dispatchWSWait loop then calls matchWSEvents every 50ms, which copies the whole 256-event ring under the lock and, in regex mode, calls regexp.MatchString — a fresh compile — once per event per tick: up to 256 compiles × 20 ticks = ~5k regex compilations per second per waiting VUser, plus a full ring copy per tick.

**Evidence:** matched, _ := regexp.MatchString(expected, payload)  // called per event from matchWSEvents inside the 50ms ticker loop (lines 95-111)

**Hot path:** Any script using browser_ws_wait with match_mode regex (the recorded-app norm for streaming payload assertions) pays this for the full wait duration on every iteration of every VUser.

**Verification notes:** Verified the mechanism line-by-line in pkg/protocol/browser/ws_observe.go: line 168 compiles the regex for validation and discards it, line 259 calls regexp.MatchString (fresh stdlib compile, no cache) per candidate event per 50ms tick, and line 187 copies the ring under wsMu every tick — no existing mitigation. However the hotness claim is overstated: the recorder emits match_mode "contains" (pkg/recorder/browser/convert.go:721) and "contains" is the runtime default (ws_observe.go:154), so regex mode is hand-authored opt-in, not "the recorded-app norm"; the MinSeq watermark filter (line 191) runs before payload matching so most ticks scan far fewer than 256 events; and browser VUsers run at canary scale (tens of Playwright instances, per repo ground truth), where the worst-case ~0.5-3% core per waiting VUser is dwarfed by the Chromium processes themselves. Real, cheap-to-fix inefficiency, but low realistic impact.

**Refined fix:** Take the first half of the suggested fix, drop the second. Add a `regex *regexp.Regexp` field to browserWSMatcher and keep the compiled object at ws_observe.go:168 instead of discarding it; convert browserWSPayloadMatches into a method on browserWSMatcher that switches on the already-normalized MatchMode (also removing the redundant per-event strings.ToLower/TrimSpace at line 255) and calls matcher.regex.MatchString. This eliminates ~95% of the cost with zero behavior change. Do NOT move filtering under the lock as suggested: recordWSEvent takes the same wsMu from Playwright's OnFrameSent/OnFrameReceived handlers, so regex-matching up to 256 x 4KB payloads under the lock would stall frame capture; the ~28KB header-only ring copy per tick is the cheaper side of that trade. Optional micro-win: track the max Seq scanned per wait and skip the already-scanned prefix each tick, but that subtly changes match accumulation vs ring eviction and is not worth the risk for a canary-scale path.

**Risk:** Minimal — behavior-identical matching; filtering under the lock marginally extends lock hold versus copy-then-filter.

### 108. dom_stable sync polls querySelectorAll('*') plus layout-forcing innerText every 100ms

- Location: `pkg/protocol/browser/wait_actions.go:289`
- Category: cpu
- Finder: sup:browser-protocol-per-step
- Verified impact: low
- Confidence: high
- Effort: M

**Description:** waitForDOMStable computes its stability signature by evaluating document.querySelectorAll('*').length (a full DOM traversal) and body.innerText.length (a forced full-page style+layout pass) every 100ms until the page is stable. On large pages each tick costs tens of ms of renderer CPU, which both burns generator-node CPU and perturbs the very timing the step is trying to stabilize — and the polling eval itself can keep resetting layout caches.

**Evidence:** body ? body.innerText.length : 0,
document.querySelectorAll('*').length,  // evaluated per 100ms tick

**Hot path:** Every action carrying sync:{type:dom_stable} — a common TruClient-style post-step sync — runs this loop for at least stable_ms (default 500ms, i.e., >=5 full-page layout passes) per step per VUser per iteration.

**Verification notes:** Code confirmed verbatim at pkg/protocol/browser/wait_actions.go:284-292 (line 289 is the querySelectorAll('*') read; body.innerText forces a style+layout flush per 100ms tick, run via runPostActionSync after every action carrying the sync). But the hot-path claim is overstated: grep shows NO importer or recorder ever emits dom_stable — the TruClient converter (pkg/convert/trueclient) emits no sync policies at all and recorderjson/pkg/recorder emit only load/network_idle — so it fires only for hand-authored scripts, and browser-protocol VUsers are inherently low-count (canary-grade per docs), never thousands. Per-step renderer cost (~5-50ms over the 500ms default window, worse on 50k+-node pages) is dwarfed by running Chromium itself; the more genuine concern is fidelity — the forced layout can register long tasks and skew INP/CLS collected by the same page's __apexloadMetrics observers.

**Refined fix:** Skip AddInitScript churn; lazily install the observer inside the polled eval itself, matching the existing window.__apexloadHistoryPopstateCount pattern in navigation_actions.go:109-118: first tick installs window.__apexloadDomMutations = 0 plus a MutationObserver on document.documentElement with {subtree:true, childList:true, attributes:true, characterData:true} and marks that tick unstable; subsequent ticks return [readyState, __apexloadDomMutations, location.href].join('|'). Keeping readyState and location.href preserves navigation detection (a new document loses the observer, so the reinstall tick naturally resets stability). Drop innerText and querySelectorAll entirely. Effort is S, not M, and risk is minimal since characterData covers text edits.

**Risk:** MutationObserver misses some non-DOM changes the current signature caught (location.href changes, text edits without node mutation — actually characterData covers those); stability semantics shift slightly and need replay-fidelity verification.

## Refuted / Rejected Findings

### R1. foreach control_loops rebuild every row map of the data source on each loop entry and rescan all rows to snapshot restore state

- Location: `pkg/engine/data.go:343`

**Why not actionable:** The code mechanism is accurately described (GetAllRows rebuilds per-row maps with TrimSpace on every call at data.go:343-362, no caching; captureForEachRestore is O(rows x cols) at control_flow.go:173-202), but the claimed hot path does not exist: VUser.DataProvider is only set in shakeout (shakeout.go:40) and replay (replay.go:315) — both single-VUser one-off runs. The production load paths (engine.go:199, group.go:374/469/570 via testexecution.go:918 buildRunEngine and generatorrpc/service.go:1363) never wire a DataProvider, so foreachRows nil-guards out at control_flow.go:151 and GetAllRows never runs per-VUser-per-iteration. Residual cost is one table materialization per foreach entry in an admin-time shakeout/replay — negligible. Notable side discovery: this same missing wiring means foreach-over-data-source and {{data.src.col}} bindings silently no-op in real load runs, a functional gap worth its own investigation; if that gap is ever fixed, this allocation pattern would become hot as described.

### R2. rewriteSQL re-runs the full dialect-translation pipeline on every query execution (Postgres backend)

- Location: `pkg/storage/sqlite.go:322`

**Why not actionable:** Confirmed the mechanism: rewriteSQL (pkg/storage/sqlite.go:322) runs uncached on every Exec/Query/QueryRow for storeDB/storeTx and the auth tx wrapper, and the Postgres auth path really does 3 uncached statements per authenticated request (auth_resolution.go:157-190). However the cost is overstated: strings.ReplaceAll allocates nothing on no-match (the common case for 12+ of the 14 passes), leaving only the 2 ToUpper copies and the placeholder-rewrite builder as real allocations — roughly 2-4us CPU and 3-4KB per statement, dwarfed ~100x by each pgx network round trip; at hundreds of statements/sec that is ~0.1-0.2% of one core. SQLite (the default deployment) short-circuits entirely at line 323.

### R3. Route-catalog access resolution is a two-pass linear scan over ~310 rules with per-rule string splitting, executed up to 3x per request

- Location: `pkg/api/route_catalog.go:397`

**Why not actionable:** The mechanism is real and unmitigated: route_catalog.go:397-411 does two linear passes over 257 rules (finding said ~310) with per-template-rule strings.Split, and accessRequirementForRequest is genuinely resolved up to 3x per mutating service-account request (middleware.go:120, middleware.go:464 via bodyLimitMiddleware, auth_resolution.go:449). However, the finding overstates the split cost — routePathTemplateMatches early-returns on the 151 literal rules and method-mismatched rules, so worst case is ~5-15 µs and a few KB garbage per resolution. This is control-plane middleware only (dashboard polling at tens of GET req/s, automation); VUser execution and generator metric fan-in never traverse it, and each request's DB-backed session/RBAC resolution and JSON work dwarf the scan by 100x, so real-world impact is negligible.

### R4. JMX CharData accumulated with string += reallocates per entity-split chunk

- Location: `pkg/convert/jmeter/converter_xml.go:40`

**Why not actionable:** Verified the cited code at converter_xml.go:40, then checked the claimed chunking mechanism against the installed Go 1.26.4 stdlib source: encoding/xml's text() decodes &quot;/&amp;/character references inline into one buffer (xml.go:1030-1108) and rawToken emits a contiguous text run as a single CharData token terminated only by '<', EOF, or CDATA end (xml.go:571-578) — entity boundaries never split tokens (that's Java SAX behavior). A 1MB entity-heavy stringProp body therefore triggers exactly one += with one copy; the only multi-chunk accumulation is ~10-byte inter-child whitespace on container nodes, which is single-digit MB per realistic import. The path (every JMeter import) is warm, but the claimed O(chunks x size) blowup does not exist in Go.

### R5. regionForTarget clones the entire target-spec slice per lookup inside the per-batch resource-snapshot loop

- Location: `pkg/api/distributed_inventory.go:260`

**Why not actionable:** Verified the exact mechanism: regionForTarget (pkg/api/distributed_inventory.go:260) calls configuredTargetSpecs() which clones the full spec slice under RLock (distributed_execution_backend.go:116-120) per lookup, invoked N times per batch inside buildGeneratorResourceSnapshots, which runs on every applyRemoteMetricsBatch at the generators' 1s publish interval (service.go:448) — so O(N^3) shallow copies/sec as claimed. However N is the generator-pod count (realistically 2-20, ~80B/spec shallow copy), giving well under 1 MB/sec of allocation, and the same function already copies the full MetricsBatch map per event, which dwarfs this. Real inefficiency, hot path, but negligible measurable cost at any plausible fleet size.
