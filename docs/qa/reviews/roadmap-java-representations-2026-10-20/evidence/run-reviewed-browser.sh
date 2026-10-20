#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
export APEXLOAD_E2E_OUTPUT_ROOT=/private/tmp/apex-roadmap-java-acquisition/browser-reviewed
export APEX_BUILD_HASH_LOG=/private/tmp/apex-roadmap-java-acquisition/controller-reviewed-hashes.txt
APEXLOAD_E2E_PROFILE=dev-admin APEXLOAD_E2E_PORT=18145 scripts/run-e2e.sh workflow-java-representation.spec.ts workflow-help.spec.ts > /private/tmp/apex-roadmap-java-acquisition/reviewed-browser-dev-admin.log 2>&1
APEXLOAD_E2E_PROFILE=local-auth APEXLOAD_E2E_PORT=18146 scripts/run-e2e.sh workflow-java-representation.spec.ts workflow-help.spec.ts > /private/tmp/apex-roadmap-java-acquisition/reviewed-browser-local-auth.log 2>&1
