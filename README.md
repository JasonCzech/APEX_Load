# Project Stormrunner

Stormrunner is a Go-based load testing platform with three primary surfaces:

- A controller API and embedded dashboard for authoring/running tests
- A CLI (`stormctl`) for local script execution
- A generator process placeholder for future distributed execution

The project is organized to support both direct local runs and an eventual controller-managed distributed model.

## Core Components

- `cmd/controller`: starts the HTTP API server and serves the React dashboard
- `cmd/stormctl`: CLI for running DSL scripts directly against the engine
- `cmd/generator`: generator process scaffold for distributed mode
- `pkg/api`: REST API, websocket streaming, settings/archive/recording handlers
- `pkg/engine`: virtual-user execution engine, pacing, metrics, and runtime behavior
- `pkg/dsl`: script structures and parser for load test definitions
- `dashboard`: React + Vite frontend that compiles into `pkg/api/static`

## Requirements

- Go `1.25+`
- Node.js `18+` (recommended for dashboard builds)

## Quick Start

### 1) Start the controller API and dashboard

```bash
go run ./cmd/controller
```

By default the controller listens on `:8080`.

### 2) Open the dashboard

Navigate to `http://127.0.0.1:8080`.

### 3) Build dashboard assets (if developing frontend)

```bash
cd dashboard
npm install
npm run build
```

`npm run build` emits static assets to `pkg/api/static`, which are served by the controller.

## Environment Variables

Controller runtime configuration:

- `STORMRUNNER_ADDR`: HTTP bind address (default `:8080`)
- `STORMRUNNER_DB`: SQLite DB path (default `stormrunner.db`)

## Build and Test

Use Make targets from repo root:

```bash
make build       # builds generator/controller/stormctl into ./bin
make test        # runs Go tests with race detector
make test-cover  # race + coverage report (coverage.out / coverage.html)
make lint        # runs golangci-lint (if installed)
```

Frontend checks:

```bash
cd dashboard
npm run lint
npm run test
```

## stormctl CLI

Run a DSL script locally:

```bash
go run ./cmd/stormctl run --script path/to/script.json --vusers 50 --duration 2m --ramp-up 30s
```

Key flags:

- `--script, -s` required script path
- `--vusers, -v` virtual user count (default `10`)
- `--duration, -d` test duration (default `1m`)
- `--ramp-up, -r` ramp period (default `0s`)

## API Surface (Controller)

Representative endpoints:

- Tests: `/api/v1/tests`, `/api/v1/tests/{id}`, `/api/v1/tests/{id}/start`
- Scripts: `/api/v1/scripts`, `/api/v1/scripts/{id}`
- Recorder: `/api/v1/recorder/*`
- Health: `/api/v1/system/health`
- WebSocket streams: `/api/v1/ws`, `/api/v1/tests/{id}/stream`

The dashboard relies on these endpoints for run management, live metrics, and script workflows.

## Repository Layout

```text
cmd/            # binary entrypoints
dashboard/      # React frontend (build outputs embedded static assets)
pkg/api/        # HTTP handlers, server wiring, websocket hub, archive/settings
pkg/engine/     # load generation and metrics runtime
pkg/dsl/        # script parser and types
pkg/recorder/   # traffic recorder and conversion flows
pkg/storage/    # storage abstractions and sqlite implementation
examples/       # sample requests/scripts
```

## Current State and Direction

- Controller + dashboard + local engine path is functional.
- Generator binary currently runs as a scaffold while distributed orchestration is phased in.
- Architecture already separates controller, engine, and generator concerns to support that expansion.
