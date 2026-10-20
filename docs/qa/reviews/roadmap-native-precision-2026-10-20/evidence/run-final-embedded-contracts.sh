#!/bin/bash
set -euo pipefail
cd "${APEXLOAD_CHECKOUT:?set reviewed checkout path}"
export GOMAXPROCS=4 GOGC=100 GOFLAGS=-p=1
go test -json -timeout=20m ./pkg/api -run '^(TestStaticRoutes.*|TestDashboardStaticReady.*|TestSwaggerYAMLIsFresh|TestEndpointCatalogJSONIsFresh|TestOpenAPIAnalysisPrecisionReviewResponseRetainsTypedRisks|TestOpenAPIClosedAnalyticalRequestsRetainTypedIdentityAndDefinition)$' > "${APEXLOAD_EVIDENCE_DIR:?set evidence directory}/final-embedded-contracts.jsonl" 2>&1
