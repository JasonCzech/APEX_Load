#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
out=/private/tmp/apex-roadmap-native-precision
git fetch origin main
git merge --ff-only origin/main
(cd dashboard && npm run build) > "$out/final-build.log" 2>&1
# Dashboard generation is complete before any Go validation embeds its output.
git fetch origin main
git merge-base --is-ancestor origin/main HEAD
APEXLOAD_BROWSER_TESTS=require \
APEXLOAD_HAR_COMPAT_FILE="${APEXLOAD_HAR_COMPAT_FILE:?set private capture path}" \
APEXLOAD_HAR_COMPAT_SHA256=7469ebdef88a576cd3cfa5891318a4aa5afb4efe97fccb410df339fc609ddef7 \
APEXLOAD_HAR_COMPAT_PRIVATE_SCRIPT= \
APEXLOAD_HAR_COMPAT_SUMMARY="$out/private-har-final-summary.json" \
go test -overlay=/private/tmp/apex-roadmap-mapping-capability/applied-har-overlay.json -json -timeout=20m ./... > "$out/final-full-go.jsonl" 2>&1
python3 scripts/check-docs.py > "$out/final-docs.log" 2>&1
