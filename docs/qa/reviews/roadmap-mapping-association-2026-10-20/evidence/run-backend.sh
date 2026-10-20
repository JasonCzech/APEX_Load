#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
go test -race -tags analytics_duckdb -count=1 -timeout=10m -json ./pkg/api -run '^TestAnalysisNegotiatedAssociationPreservesMeasurementMapping$' > /private/tmp/apex-roadmap-mapping-capability/applied-race.jsonl 2>&1
git fetch origin main
git merge-base --is-ancestor origin/main HEAD
export APEXLOAD_BROWSER_TESTS=require
APEXLOAD_HAR_COMPAT_FILE='/Users/jasonczech/Downloads/AIA attone--perfhyper2 (2).zip' \
APEXLOAD_HAR_COMPAT_SHA256=7469ebdef88a576cd3cfa5891318a4aa5afb4efe97fccb410df339fc609ddef7 \
APEXLOAD_HAR_COMPAT_PRIVATE_SCRIPT='' \
APEXLOAD_HAR_COMPAT_SUMMARY=/private/tmp/apex-roadmap-mapping-capability/har-import-applied-summary.json \
go test -overlay=/private/tmp/apex-roadmap-mapping-capability/applied-har-overlay.json -timeout=20m -json ./... > /private/tmp/apex-roadmap-mapping-capability/full-go-applied.jsonl 2>&1
