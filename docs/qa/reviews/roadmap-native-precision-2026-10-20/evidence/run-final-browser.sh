#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
out=/private/tmp/apex-roadmap-native-precision
for profile in dev-admin local-auth; do
  port=18083
  if [[ "$profile" = local-auth ]]; then port=18084; fi
  APEXLOAD_E2E_PROFILE="$profile" APEXLOAD_E2E_PORT="$port" APEXLOAD_E2E_OUTPUT_ROOT="$out/browser-final" scripts/run-e2e.sh e2e/workflow-analysis-preparation.spec.ts e2e/workflow-help.spec.ts > "$out/browser-final-$profile.log" 2>&1
done
