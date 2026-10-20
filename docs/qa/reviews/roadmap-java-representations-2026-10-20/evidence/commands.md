# Java opaque representation validation

Applied checkout: `codex/roadmap-durable-cursors`, based on the commit recorded in `runtime.txt` and source hash manifests. The reviewed backend cohort is `reviewed-source-hashes.json`. Later e2e-only screenshot framing is separately hashed and tested; it does not change embedded application assets or Go source.

Tool/runtime pins are in `runtime.txt`. Validation used `GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache`, `GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp`, `GOMAXPROCS=2`, `GOGC=30`, `GOFLAGS=-p=1`. Wrappers fetched `origin/main` and required it to be an ancestor before every explicit Go/dashboard build. Each full Go run also fetched and checked main immediately before invocation. Frontend asset generation finished before backend compilation/tests embedded it.

## Installed source

1. `go generate ./pkg/api` generated OpenAPI, endpoint contracts and TypeScript.
2. `npm run test:cycle` exercised typecheck, lint, all unit tests, build, full dev-admin and local-auth browser workflows. Initial source had 8,030 unit tests; the later review added two cases. Original complete browser profiles used that initial build and the three original Java cases, with the local-auth allowlist fixed.
3. After review fixes: `npm run typecheck`, `npm run lint`, `npm test`, `npm run build`. Final unit cohort is 720 files / 8,032 tests. Refreshed assets are hashed.
4. `run-reviewed-browser.sh` and `run-reviewed-repeat-browser.sh` exercised four Java cases plus all Help cases under both profiles. The first final dev-admin run found a test-only CSV quoting expectation; corrected repetition passed. The local-auth repetition includes actual Java cases, unlike the earlier targeted local-auth run, which accidentally selected only Help.
5. `run-final-help.sh` and `run-final-baseline.sh` repeated their affected tests in both profiles after screenshot framing changes. Screenshots were visually inspected for the new evidence, counts, missing values and help limits.
6. `run-reviewed-backend.sh`: fresh targeted API/native catalog/population/report tests; generated API matrix and QA dashboard; analytics inventory and docs QA; PostgreSQL migration/round trip; domain-contract tests; race checks of protocol, HTTP, Java HTTP, engine, RPC, analysis, storage and API Java/body observation tests.
7. Storage and API race linking ran out of disk space. `run-reviewed-backend-resume.sh` reran those two packages in separate commands after obsolete build archives were reclaimed. Other six race package passes remain in the original reviewed race log. These are build-resource failures, not passing tests.
8. The resume script runs `go test -timeout=20m -json ./...` with `APEXLOAD_BROWSER_TESTS=require` and the private HAR test-only overlay. Unchanged packages may use Go's normal test cache; affected focused/race tests use `-count=1`. Original full-suite failures (Java metric units and a forbidden compatibility-barrel import) remain in `full-go-applied.jsonl.gz`; the final full-suite output is separate. The reviewed repetition later hit the real 1GiB archive-download reserve; `run-final-full.sh` repeats the full gate after reclaiming closed pre-Java API archives, preserving `reviewed-full-go.jsonl.gz` and writing `final-full-go.jsonl.gz`.
9. `python3 scripts/check-docs.py` checks repository links/anchors and help source contracts. Final diff and staged review must be clean before commit.

## Private attachment

The overlay adds only the retained compatibility harness to `pkg/api`; no product importer code is replaced. It runs the real import endpoint against the user-provided ZIP with the original SHA-256 enforced. `APEXLOAD_HAR_COMPAT_PRIVATE_SCRIPT` is empty, so no converted private script is saved. It never replays target URLs. The final aggregate comparison ignores only the randomized `script_sha256_without_id_created_at` field and compares all other keys to the preceding mapping slice. The original ZIP is rehashed after import. Only aggregate summaries, hashes and the test harness are retained here.

## Limits

One local darwin/arm64 functional cohort. Browser diagnostics use controlled API responses with real authentication/navigation and panel persistence. PostgreSQL uses the disposable local fixture. This does not establish a JVM object-footprint method, complete distributed source windows, native Linux/Windows behavior, reference-host overhead, live TruClient qualification, or deployed multi-host recovery.

## Final tracked-inventory correction

Staging new Java test files changed the git-tracked test count used by the QA dashboard generator. The third full run (`final-full-go.jsonl.gz`) found `TestGeneratedDashboardIsFresh` stale. `python3 scripts/qa-generate-dashboard.py` regenerated it after staging; `staged-docs-qa.jsonl.gz` records the fresh documentation suite. `run-final-clean-full.sh` then repeats the entire gate in `final-clean-full-go.jsonl.gz`, retaining the same private HAR environment/path and the normal test cache for unchanged source. Its final-source-hashes.json supersedes the preserved pre-qa-final-source-hashes.json; no runtime source or embedded application asset changed for this correction.
