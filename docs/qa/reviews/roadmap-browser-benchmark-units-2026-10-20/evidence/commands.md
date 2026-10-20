# Commands and evidence scope

All commands use the isolated task checkout and session cache, GOMAXPROCS=2, GOGC=30 and GOFLAGS=-p=1. Fetch/check integrated main before builds/full API suites. Complete dashboard generation before embedding Go builds/tests.

- Draft and applied `go test -count=1 -json ./pkg/protocol/browser -run '^TestBrowserBenchmark(RSS|V2)'` checks exact decimal-versus-binary boundaries, missing/zero/partial/overflow readings, artifact version, sample denominators and population disclosure.
- `go test -count=1 -json ./pkg/api -run '^TestBrowserBenchmarkEvidenceUsesDecimalByteTargets$'` checks compatibility fields, authoritative raw target, engine availability and unchanged density/repetition gates.
- The draft native smoke sets APEXLOAD_BROWSER_TESTS=require and APEXLOAD_BROWSER_BENCHMARKS=1 and runs `-bench '^BenchmarkBrowserRSSPerChromiumDeviceProfile$' -benchtime=1x`, retaining three local Chromium profiles. This validates only harness operation under current host load.
- Generate API contracts with `go generate ./pkg/api`, and code-owned browser inventory with APEXLOAD_UPDATE_BROWSER_ACCEPTANCE=1. Verify freshness.
- Applied full Go regression, relevant dashboard checks/build and both auth-profile Help workflows qualify local compatibility. The private HAR regression retains only aggregate counts/hash and does not replay traffic or save converted content.
- Documentation checks and evidence SHA256 verify links and retained evidence.

Historical artifacts and their hashes remain untouched. No external qualification is claimed.

The final integrated Go invocation permits the standard package test cache; package output identifies any reused results. Focused and race regressions are explicitly uncached. Source/asset manifests bind the applied cohort.
