#!/bin/bash
set -euo pipefail
cd /Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner
export PATH=/private/tmp/apex-roadmap-prerequisite/bin:/private/tmp/apexload-monthly-node-runtime/node_modules/node/bin:$PATH
export GOCACHE=/private/tmp/apex-roadmap-prerequisite/go-cache GOTMPDIR=/private/tmp/apex-roadmap-prerequisite/go-tmp GOMAXPROCS=2 GOGC=30 GOFLAGS=-p=1
out=/private/tmp/apex-roadmap-controlled-endpoints
git rev-parse HEAD > "$out/base-commit.txt"
go version > "$out/runtime.txt"
go list -m google.golang.org/grpc google.golang.org/protobuf github.com/gorilla/websocket >> "$out/runtime.txt"
go test -count=1 -timeout=5m -json ./pkg/protocol/grpc ./pkg/protocol/websocket > "$out/applied-tests.jsonl" 2>&1
go test -race -count=1 -timeout=5m -json ./pkg/protocol/grpc ./pkg/protocol/websocket > "$out/applied-race.jsonl" 2>&1
python3 /private/tmp/apex-roadmap-controlled-endpoints/prepare-review.py
python3 scripts/check-docs.py > "$out/docs.log" 2>&1
git diff --check
