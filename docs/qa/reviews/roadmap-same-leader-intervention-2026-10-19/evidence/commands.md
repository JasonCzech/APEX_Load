# Validation commands and cohorts

Work ran in the isolated `codex/roadmap-durable-cursors` checkout. Go used a session-local `GOCACHE`/`GOTMPDIR`, `GOMAXPROCS=2`, `GOGC=30`, and `GOFLAGS=-p=1`. The dashboard used the repository-pinned Node 22.23.3. Before explicit Go/dashboard builds and full API suites, origin/main was fetched and integrated; private wrappers stopped a build when newer main commits appeared.

- `go test -count=1 -timeout=20m -json ./...` — full standard repository run; its Go API binary predates the final held-buffer review fix.
- `go test -count=1 -timeout=20m -json ./pkg/api` — full API repeat on the final recovery production source, including the held-buffer fix.
- `go test -count=1 -json ./pkg/api -run 'TestSameLeaderEvacuation|TestInterruptedEvacuation|TestTerminalEvacuationUncertaintyStillRetriesUncommittedParticipantStop'` — final recovery tests, including the later UTF-8 diagnostic boundary case.
- `npm --prefix dashboard test -- src/components/ApiGuidePage.test.tsx src/helpContent.test.ts src/components/GuideMarkdownArticle.test.tsx` — targeted Help unit coverage.
- `npm --prefix dashboard run typecheck`, `npm --prefix dashboard run lint`, `npm --prefix dashboard run build` — initial Help checks and bundle generation.
- Both-profile `scripts/run-e2e.sh` runs scoped to `workflow-help.spec.ts` — corrected Help workflows, retained reload failure, and deterministic delayed-permission regression.
- `npm --prefix dashboard run test:cycle` — complete typecheck, lint, unit tests, embedded bundle build, dev-admin and local-auth browser workflows. The first attempt passed dev-admin and stopped at the next build because main advanced; its log is named `full-dashboard-cycle-pre-integration.log.gz`.
- After integrating main through `745d85a05`, the focused command-center/chart/Help unit run passed 187 tests, then the full dashboard cycle was restarted on that combined tree. Its final log and controller hashes are separate from the earlier cohort.
- `python3 scripts/check-docs.py` — repository Markdown targets and owning Help references.

The stopped `-trimpath` broad run is diagnostic history, not a passing gate. Neither the local recovery fixtures nor the local browser workflows constitute deployed multi-host recovery qualification. The private user HAR was imported, never replayed; its prior sanitized evidence is linked from the review README.
