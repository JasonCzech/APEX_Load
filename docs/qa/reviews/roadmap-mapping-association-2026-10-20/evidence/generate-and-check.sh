#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -count=1 -json ./pkg/api -run '^TestGeneratedAPIContractMatrixIsFresh$' > /private/tmp/apex-roadmap-mapping-capability/applied-contract-matrix.jsonl 2>&1
(cd docs/qa && python3 ../../scripts/qa-generate-dashboard.py --root ../.. --status STATUS.md --matrix E2E_SUITES.md --api-contract evidence/M1/api-contract-matrix.json --executions execution-status.json --output DASHBOARD.md) > /private/tmp/apex-roadmap-mapping-capability/qa-dashboard-generate.log 2>&1
APEXLOAD_UPDATE_ANALYTICS_ACCEPTANCE=1 go test -count=1 -json ./docs/qa -run '^TestAnalyticsAcceptanceInventory$' > /private/tmp/apex-roadmap-mapping-capability/applied-inventory.jsonl 2>&1
go test -count=1 -timeout=5m -json ./pkg/api -run 'TestAnalysisCapabilities|TestAnalysisNegotiatedAssociationGeneratedContract|TestAccessRequirementSnapshotStable|TestGeneratedAPIContractMatrixIsFresh|TestSwaggerYAMLIsFresh|TestEndpointCatalogJSONIsFresh|TestGeneratedSwaggerIsValid|TestGeneratedEndpointCatalogIsValid' > /private/tmp/apex-roadmap-mapping-capability/applied-targeted.jsonl 2>&1
go test -tags analytics_duckdb -count=1 -timeout=10m -json ./pkg/api -run '^TestAnalysisNegotiatedAssociationPreservesMeasurementMapping$' > /private/tmp/apex-roadmap-mapping-capability/applied-native.jsonl 2>&1
go test -count=1 -timeout=5m -json ./docs/qa > /private/tmp/apex-roadmap-mapping-capability/applied-docs-qa.jsonl 2>&1
