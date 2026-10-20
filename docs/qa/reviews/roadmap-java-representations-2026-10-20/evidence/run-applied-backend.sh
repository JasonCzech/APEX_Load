#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
out=/private/tmp/apex-roadmap-java-acquisition
go test -count=1 -timeout=10m -json ./pkg/api -run 'TestJavaHTTP|Test.*Native.*Catalog' > "$out/applied-api.jsonl" 2>&1
APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -count=1 -json ./pkg/api -run '^TestGeneratedAPIContractMatrixIsFresh$' > "$out/applied-contract-matrix.jsonl" 2>&1
(cd docs/qa && python3 ../../scripts/qa-generate-dashboard.py --root ../.. --status STATUS.md --matrix E2E_SUITES.md --api-contract evidence/M1/api-contract-matrix.json --executions execution-status.json --output DASHBOARD.md) > "$out/applied-qa-dashboard.log" 2>&1
APEXLOAD_UPDATE_ANALYTICS_ACCEPTANCE=1 go test -count=1 -json ./docs/qa -run '^TestAnalyticsAcceptanceInventory$' > "$out/applied-inventory.jsonl" 2>&1
go test -count=1 -timeout=10m -json ./docs/qa > "$out/applied-docs-qa.jsonl" 2>&1
APEXLOAD_TEST_POSTGRES_URL=postgres://postgres:local-outbox-fixture@127.0.0.1:50055/outbox go test -tags postgres_integration -count=1 -timeout=10m -json ./pkg/storage -run '^TestPostgresJavaHTTPHistoryMigrationAndRoundTrip$' > "$out/applied-postgres.jsonl" 2>&1
go test -race -count=1 -timeout=10m -json ./pkg/protocol ./pkg/protocol/http ./pkg/protocol/java_http ./pkg/engine ./pkg/distributed/generatorrpc ./pkg/analysis ./pkg/storage -run 'Test.*(JavaHTTP|HTTPBody)' > "$out/applied-race.jsonl" 2>&1
git fetch origin main
git merge-base --is-ancestor origin/main HEAD
export APEXLOAD_BROWSER_TESTS=require
APEXLOAD_HAR_COMPAT_FILE='/Users/jasonczech/Downloads/AIA attone--perfhyper2 (2).zip' \
APEXLOAD_HAR_COMPAT_SHA256=7469ebdef88a576cd3cfa5891318a4aa5afb4efe97fccb410df339fc609ddef7 \
APEXLOAD_HAR_COMPAT_PRIVATE_SCRIPT= \
APEXLOAD_HAR_COMPAT_SUMMARY="$out/har-import-applied-summary.json" \
go test -overlay=/private/tmp/apex-roadmap-mapping-capability/applied-har-overlay.json -timeout=20m -json ./... > "$out/full-go-applied.jsonl" 2>&1
python3 scripts/check-docs.py > "$out/applied-docs-final.log" 2>&1
