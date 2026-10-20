#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=4 GOGC=100 GOFLAGS=-p=1
out=/private/tmp/apex-roadmap-native-precision
for profile in dev-admin local-auth; do
  port=18083
  if [[ "$profile" = local-auth ]]; then port=18084; fi
  git fetch origin main
  git merge-base --is-ancestor origin/main HEAD
  APEXLOAD_E2E_PROFILE="$profile" APEXLOAD_E2E_PORT="$port" APEXLOAD_E2E_OUTPUT_ROOT="$out/browser-help-freshness" scripts/run-e2e.sh e2e/workflow-analysis-preparation.spec.ts e2e/workflow-help.spec.ts > "$out/browser-help-freshness-$profile.log" 2>&1
done
