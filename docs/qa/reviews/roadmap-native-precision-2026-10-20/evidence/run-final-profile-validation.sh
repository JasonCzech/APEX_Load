#!/bin/bash
set -euo pipefail
# Final backend correction; embedded dashboard was built and browser-verified first.
cd "${APEXLOAD_CHECKOUT:?set reviewed checkout path}"
export GOMAXPROCS=4 GOGC=100 GOFLAGS=-p=1
out="${APEXLOAD_EVIDENCE_DIR:?set evidence directory}"
git fetch origin main
git merge-base --is-ancestor origin/main HEAD
APEXLOAD_BROWSER_TESTS=require \
APEXLOAD_HAR_COMPAT_FILE="${APEXLOAD_HAR_COMPAT_FILE:?set private capture path}" \
APEXLOAD_HAR_COMPAT_SHA256=7469ebdef88a576cd3cfa5891318a4aa5afb4efe97fccb410df339fc609ddef7 \
APEXLOAD_HAR_COMPAT_PRIVATE_SCRIPT= \
APEXLOAD_HAR_COMPAT_SUMMARY="$out/private-har-final-profile-summary.json" \
go test -overlay="${APEXLOAD_HAR_TEST_OVERLAY:?set private test-only overlay}" -json -timeout=20m ./... > "$out/final-profile-full-go.jsonl" 2>&1
