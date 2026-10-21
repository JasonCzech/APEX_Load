# Full codebase review evidence

Snapshot: f7eff5c27801bebedafd17694bc59124e233b879. Date: October 17, 2026. Security was excluded.

At the time of the original review, the managed checkout was /Users/jasonczech/.codex/worktrees/monthly-code-review/Project_Stormrunner on codex/full-codebase-review. The earlier monthly fixes had been merged in PR #240. The original checkout's existing edits were not changed. This document records that review's baseline evidence; subsequent implementation and validation are tracked in [the fix record](../FIXES.md).

**Inventory**

`area-inventory.csv` contains source/test file and physical-line counts by area. `largest-go-functions.csv` retains the 100 longest production-classified Go function bodies, with AST statement counts. `internal-go-dependencies.csv` contains unique direct internal import edges from non-test-classified Go files.

`inventory.go.txt` is the scanner source. It lists tracked files, excludes generated/retired/vendor/docs/embedded output, reads supported extensions, and parses Go ASTs. Copy it to a scratch .go path and run:

    go run /scratch/inventory.go /absolute/repository /scratch/structure.json

The count is limited to .go, .ts, .tsx, .py, .js, .mjs, .sh, .tf, .yaml, and .yml. It includes comments and blank lines and uses test filenames/directories for classification. Java, CSS, C import fixtures, Helm helpers, package manifests, and other formats are outside these counts. They are not asserted to have zero source or zero tests. Internal import count is a dependency measure, not proof of runtime cost. Static registry/schema literals must be distinguished from procedural control flow.

**Passing package controls**

Command from the review checkout:

    GOCACHE=/tmp/apexload-full-review-gocache GOMAXPROCS=2 go test -p 2 -count=1 -timeout=4m ./pkg/monitorprovider ./pkg/analysis ./pkg/rca ./pkg/trafficprofile ./pkg/telemetry ./pkg/runtimetuning ./pkg/hostnet ./pkg/collectoragent ./deployments/k8s

All nine package suites exited successfully. `root-controls.log` includes output and a third-party macOS compiler warning. No full API suite, full repository suite, tagged DuckDB suite, Terraform deployment, or performance benchmark was run for this sweep. Main was fetched/integrated before the inventory build; it was already current at the pinned snapshot. No dashboard asset generation overlapped Go work.

**Runtime extraction probes**

Sources:

- `jms_probe_test.go.txt`: real JMS runExtractors with a quoted numeric object key, plus an array-index control.
- `jsonextract_probe_test.go.txt`: exact integer JSONPath extraction, plus a quoted-string control.

These were injected with Go's overlay mechanism at pkg/protocol/jms/full_review_probe_test.go and pkg/protocol/jsonextract/full_review_probe_test.go. The overlay Replace map pointed each absolute virtual filename at the corresponding scratch source. Run the affected packages with -overlay, -run '^TestReview', -count=1, and -p 2.

`runtime-probes.log` records two expected failures on the unmodified snapshot. The two controls passed in the same run. Kafka's existing numeric-key control was inspected, not executed. Database/network reachability was traced through production calls; this was not a live SQL/network end-to-end execution.

**Recording and device probes**

The dashboard dependencies were synchronized with the snapshot's lockfile using pinned Node 22.23.3 and npm ci. The runs used Vitest 5.0.3 and jsdom 30.1.2.

`recording-probe.test.tsx.txt` is a temporary copy of the existing RecordingStudio test harness with four added cases. It ran as src/components/full-review-recording-probe.test.tsx:

    npm test -- src/components/full-review-recording-probe.test.tsx -t fullreview --maxWorkers=1

`recording-probe.log` records two delayed-response failures and two passing ordered-response controls. Ninety unrelated harness cases were skipped by the filter. It proves stale selection and stale returned editable metadata in the actual component; it does not claim an automatic overwrite of the entire HTTP request.

`device-probe.test.tsx.txt` ran similarly as src/components/full-review-device-probe.test.tsx. `device-probe.log` records different attestation tokens in retry payloads under the same operation ID. Token issuance was substituted in this component harness. The actual controller issuance and receipt digest were traced separately. Only imports that actually receive an attestation are affected. No end-to-end HTTP 409 reproduction is claimed.

The copied test files were removed afterward. No real browser or controller was started.

**CLI cancellation probe**

`cli_cancel_poll_probe_test.go.txt` uses an httptest controller. One case cancels only after the status poll reaches the server; another returns an ordinary HTTP 500. It was injected at cmd/apexctl/full_review_probe_test.go via an overlay and run with:

    GOCACHE=/tmp/apexload-full-review-gocache GOMAXPROCS=2 go test -p 2 -count=1 -run '^TestFullReviewCICancelDuringPoll$' -v -overlay=/scratch/cli-overlay.json ./cmd/apexctl

`cli-cancel-poll-probe.log` records no abort request and STATUS_FAILED/execution_error for the cancelled poll. The ordinary transport-error control passed. The probe used current production CLI code and a local HTTP fixture; it did not launch a load generator.

**Scenario history and CLI output probes**

`scenario-history-probe.test.tsx.txt` copies the existing TestHub harness and adds failure-versus-empty cases. It ran with the same pinned frontend runtime and filter as the recording probes. `scenario-history-probe.log` records one failed case showing false empty history and no retry after collapse/reopen, plus one passing authoritative-empty control. The filter skipped 161 unrelated cases.

`cli_additional_probe_test.go.txt` ran through a Go overlay at cmd/apexctl/full_review_additional_probe_test.go. The command used -p 2, -count=1, -timeout=2m, -v, and -run '^TestFullReview(StatusSLAEvaluation|ExplainAllActionScopes)$'. `cli-additional-probe.log` records two failures for unevaluated SLA text/JSON and three failures for assertions/extractors in validated nested/init/teardown scripts; evaluated-status and top-level-script controls passed. The retained final probe does not require a particular root-versus-recursive action count. No exit-code defect is asserted.

Across all targeted probe runs, 20 selected leaf cases produced 12 expected failures and eight passing controls. Nine distinct findings have a focused reproduction; two capacity findings remain source-traced. These expected failures demonstrate defects in the original snapshot, not a passing release validation.

The static MCP route comparison (`mcp-route-comparison.json`) matched all 303 declared method/path pairs. It does not establish request/response schema compatibility.

**Independent challenge**

A separate review checked the reported caller-to-effect traces and the retained probes. All original eight findings survived. It narrowed the device finding to imports that receive attestations, the recording edit consequence to editable metadata, the ACI cleanup consequence to a later sweep, and the capacity finding to an in-flight error after leadership loss. It recommended P1 for stale provider rollback and cancellation that leaves load running. The three subsequent history/SLA/explanation findings were also independently source-checked.

The report's recommendations preserve established differences between provider query loops, JSONPath dialects, importer finalization, Java process ownership, and schedule types. No new correctness defect is inferred solely from a long file or differing unit table.

## Post-review source clarifications

On October 17, 2026, the report was corrected against the same pinned snapshot in response to a further evaluation. This pass read source only; it did not rerun probes, build binaries, measure memory, or change the original result counts. Later implementation tests belong in [FIXES.md](../FIXES.md).

- **F1:** The worker receives only a request ID, reads the current attempt on entry, and later reloads the request each iteration. The original entry token governs deferred local cleanup rather than provider effects. This broadens the earlier independent challenge's description beyond an in-flight error: immutable launch-time attempt and term ownership are needed to prevent adoption of a newer attempt after same-process reacquisition as well as stale cleanup. Verified sites: capacity_operations.go lines 110, 127, 131–137, 154, 198, and 217; handlers_fleet_management.go line 1429.
- **F2:** The demonstrated admission failure is a positive-count operator request on ACI. Its Plan always assigns Launch=Desired (instances.go line 123), whereas the ECS/ACA plans calculate standing capacity from the warm floor (aws/backend.go line 244 and azure/backend.go line 188). The general warm-versus-burst admission rule remains appropriate; the evidence does not establish identical plan behavior across providers.
- **F8:** The original probe covers JSONPath rounding above the exact float64 integer range. The shared Stringify uses fmt.Sprint for float64 (jsonextract.go lines 218–219), which can also render exactly representable seven-digit identifiers in exponent notation. JMESPath reaches this formatter at line 136. Formatting must therefore be tested separately from the JMESPath library's float64 arithmetic and numeric function behavior. This added scope is source-traced; it is not an additional original failing probe.
- **Priorities:** The expanded F8 scope warrants P1 for database identifier correlation because ordinary seven-digit IDs are affected. F9/F10 move to P3 because the demonstrated impact is client reporting/recovery, with no server-data deletion or exit-code/gate defect established. The report preserves their original P2 labels and original evidence rather than silently changing the review baseline.
- **R3:** `lock-held-durable-write-sites.csv` records all 18 explicit tr.beginLockHeldDurableWrite calls in production API files at the snapshot, with the containing function. These are section-opening sites, including compatibility/lifecycle paths; they are not a count of leaf store writes or defects. The source describes the deliberate gauge at persistence.go lines 1999–2045; TestEveryLockHeldDurableWriteIsCounted (lock_held_durable_write_guard_test.go line 34) and TestLockHeldDurableWriteSectionsAreBalanced (line 69) enforce it. The inventory should guide migration while retaining accounting for any I/O still performed under tr.mu. Ordinary storeScriptDurable already snapshots and releases tr.mu before storage (script.go lines 596–621).
- **R15:** `cli-server-imports.csv` records nine non-test CLI files importing pkg/api. The API embeds static assets at server.go line 36. The macOS job in desktop.yml line 640 describes the API compile as multi-gigabyte and runs it separately from native analytics at lines 663–666. This verifies an existing build-resource concern, but the retained evidence contains no CI peak-RSS artifact supporting the reported approximately 9 GB value. The report does not claim that peak as a measurement.

Both CSV inventories were generated from git show/git grep at f7eff5c27801bebedafd17694bc59124e233b879, not from the changing implementation checkout. The import enumeration is reproducible with:

    git grep -n 'github.com/jasonczech/apexload/pkg/api' f7eff5c27801bebedafd17694bc59124e233b879 -- cmd/apexctl ':!**/*_test.go'

The section-opening enumeration is reproducible with:

    git grep -n 'tr.beginLockHeldDurableWrite()' f7eff5c27801bebedafd17694bc59124e233b879 -- pkg/api ':!**/*_test.go'

The revised delivery sequence explicitly includes all R1–R18 recommendations, prioritizes R5 then R15 then R3/R14 after the correctness fixes, and defers R6/R10/R18 until concrete work warrants their migration risk.

**Cleanup**

All review agents and probes finished before cleanup. The dedicated Go build/test cache was cleared with the same GOCACHE setting used for the tests, reducing allocated size from 1,800,260 KiB to 8 KiB and reclaiming approximately 1.72 GiB. Session Vite caches were removed and verified absent. Dependencies and source/deliverable outputs were preserved. No browser sessions or controller processes were opened for this sweep.

Docker build-cache pruning was attempted with docker builder prune --all --force, but timed out after 20 seconds; Docker cleanup is unconfirmed. No separate Buildx builder was used. Cache cleanup (`cache-cleanup.json`) and Docker cleanup (`docker-cleanup.json`) retain the results.

Probe source copies in this evidence directory use .txt suffixes and do not participate in ordinary test discovery.
