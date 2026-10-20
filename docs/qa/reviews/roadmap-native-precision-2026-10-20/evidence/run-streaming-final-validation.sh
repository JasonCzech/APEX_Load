#!/bin/bash
set -euo pipefail
# Recorded local darwin/arm64 validation; set repository/runtime paths for reuse.
cd "${APEXLOAD_CHECKOUT:?set the reviewed checkout path}"
export GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
out="${APEXLOAD_EVIDENCE_DIR:?set evidence output directory}"
git fetch origin main
git merge-base --is-ancestor origin/main HEAD
go test -tags analytics_duckdb -json -timeout=20m ./pkg/analysis/duckdb > "$out/streaming-full-native-worker-headroom.jsonl" 2>&1
# Dashboard assets were generated and browser-verified before this backend-only correction.
git fetch origin main
git merge-base --is-ancestor origin/main HEAD
APEXLOAD_BROWSER_TESTS=require \
APEXLOAD_HAR_COMPAT_FILE="${APEXLOAD_HAR_COMPAT_FILE:?set private capture path}" \
APEXLOAD_HAR_COMPAT_SHA256=7469ebdef88a576cd3cfa5891318a4aa5afb4efe97fccb410df339fc609ddef7 \
APEXLOAD_HAR_COMPAT_PRIVATE_SCRIPT= \
APEXLOAD_HAR_COMPAT_SUMMARY="$out/private-har-streaming-summary.json" \
go test -overlay="${APEXLOAD_HAR_TEST_OVERLAY:?set private test-only overlay}" -json -timeout=20m ./... > "$out/streaming-full-go-headroom.jsonl" 2>&1
