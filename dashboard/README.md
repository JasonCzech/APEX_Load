# APEX Load Dashboard

React 18 + TypeScript + Vite single-page application for authoring, scheduling, executing, monitoring, and analyzing performance tests.

## Toolchain & Build Output

- **Node.js**: `22.23.3` (enforced by `engines.node`, `preinstall` / `npm run check:node`, `.nvmrc`, and CI).
- **Embedded static output**: `npm run build` typechecks `tsconfig.json` and `tsconfig.node.json`, then emits production assets directly into `../pkg/api/static/`, which `cmd/controller` and `desktop/` embed via `embed.FS`.
- **Generated OpenAPI contract**: `src/types/generated/openapi.ts` is generated from [../docs/swagger.yaml](../docs/swagger.yaml) by `cmd/openapits` (`npm run contracts:generate` or `go generate ./pkg/api`); never hand-edit it.
- **Styling & UI conventions**: Standard `.css` stylesheets co-located with components plus global design tokens in `src/index.css` (no CSS Modules or Tailwind). Sentence-case labels (`src/labelCasing.test.ts`), canonical metric headers/units (`src/metricLabels.ts`), and distinct `LoadingState` / `EmptyState` UX primitives are test-enforced; see [../AGENTS.md](../AGENTS.md).

## Scripts (`package.json`)

| Script | Command / Purpose |
| --- | --- |
| `npm run dev` | Starts Vite dev server on `:4000`, proxying `/api` and WebSocket traffic to `http://127.0.0.1:8080`. |
| `npm run build` | Runs `tsc --noEmit` across app and node tsconfigs and builds static assets into `../pkg/api/static` (`--emptyOutDir`). |
| `npm run preview` | Serves the built production bundle locally via `vite preview`. |
| `npm run typecheck` | Runs `tsc --noEmit` on `tsconfig.json` and `tsconfig.node.json` (`--max-old-space-size=4096`). |
| `npm run lint` | Runs ESLint across the workspace with `--max-warnings=0`. |
| `npm run lint:a11y` | Runs custom static accessibility checks (`scripts/lint-accessibility.mjs`). |
| `npm test` | Runs the Vitest unit and component test suite (`vitest run`). |
| `npm run contracts:generate` | Regenerates `src/types/generated/openapi.ts` from `../docs/swagger.yaml` via `cmd/openapits`. |
| `npm run contracts:check` | Verifies `src/types/generated/openapi.ts` matches `../docs/swagger.yaml` without drift. |
| `npm run bundle:check` | Verifies production bundle chunk sizes against budget limits (`scripts/run-bundle-budget.mjs`). |
| `npm run check:node` | Verifies the active Node.js runtime matches `engines.node` (`22.23.3`). |
| `npm run e2e` | Runs Playwright tests directly against an already-running controller (`playwright test`). |
| `npm run test:workflows` | Runs [../scripts/run-dashboard-workflows.sh](../scripts/run-dashboard-workflows.sh) across managed controller instances and Playwright workflow suites. |
| `npm run test:cycle` | Full local pre-merge frontend gate: `typecheck` → `lint` → `test` → `build` → `test:workflows`. |
| `npm run test:bundle-budget` / `test:node-version` / `test:e2e-preflight` | Node `--test` self-tests for the dashboard's build, version, and E2E helper scripts. |

## Playwright E2E Auth Profiles

End-to-end browser verification ([../scripts/run-e2e.sh](../scripts/run-e2e.sh) and `npm run test:workflows`) exercises two isolated controller authentication profiles:

- **`dev-admin`** (default port `:18081`): Fresh throwaway SQLite store starting in `auth.mode: "dev"` (local dev bypass with Admin/User preview support).
- **`local-auth`** (default port `:18082`): Persists `auth.mode: "local"` via `PUT /api/v1/system/settings`, restarts the controller, and verifies authenticated login, CSRF, session cookies, and RBAC flows.

Select a single profile during targeted iteration with `APEXLOAD_E2E_PROFILES=dev-admin` or `APEXLOAD_E2E_PROFILES=local-auth`:

```bash
APEXLOAD_E2E_PROFILES=dev-admin ../scripts/run-e2e.sh e2e/smoke.spec.ts
```

> Finish `npm run build` (dashboard asset generation) before starting `go build` or `go test` runs that embed `pkg/api/static`; never run them concurrently.
