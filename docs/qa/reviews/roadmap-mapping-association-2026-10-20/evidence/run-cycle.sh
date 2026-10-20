#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
export APEXLOAD_E2E_OUTPUT_ROOT=/private/tmp/apex-roadmap-mapping-capability/browser
export APEX_BUILD_HASH_LOG=/private/tmp/apex-roadmap-mapping-capability/controller-hashes.txt
export APEXLOAD_E2E_PORT=18139 APEXLOAD_E2E_AUTH_PORT=18140
cd dashboard
npm run test:cycle > /private/tmp/apex-roadmap-mapping-capability/dashboard-cycle.log 2>&1
