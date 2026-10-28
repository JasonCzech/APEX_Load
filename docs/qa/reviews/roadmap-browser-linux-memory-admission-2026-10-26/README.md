# QA Review Record — Browser Linux Memory Admission (`BE-A5`)

**Date:** 2026-10-26  
**Scope:** `BE-A5` — Linux `/proc/<pid>/smaps_rollup` `Pss` process-tree sampling and real measured browser context memory admission (`measureBrowserMemoryUsage` / `measureBrowserMemoryBreakdown`).  
**Primary Packages:** `pkg/protocol/browser`, `.github/workflows/ci.yml`

---

## 1. Summary of Changes

1. **Linux `/proc/<pid>/smaps_rollup` `Pss` Preference (`pkg/protocol/browser/runtime_metrics.go`, `pkg/protocol/browser/evidence_budget.go`):**
   - Extended `processSample` with `VmRSSKB`, `PSSKB`, and `PSSKnown`.
   - Updated `readProcProcessSamples` to retain `VmRSSKB` from `/proc/<pid>/stat` and, when `/proc/<pid>/smaps_rollup` is readable and yields a positive `Pss:`, set `PSSKB`, `PSSKnown = true`, and `RSSKB = PSSKB` so `processTreeRSS` sums proportional resident bytes instead of multiplying shared Chromium Zygote pages across every child process. When `smaps_rollup` is absent or unreadable, `readProcProcessSamples` falls back to `VmRSS`.
   - Added `browserMemoryBreakdown`, `measureBrowserMemoryBreakdown`, `processTreeMemoryTotals`, and `cgroupMemoryCurrent` so `measureBrowserMemoryUsage` prefers `Pss` on Linux for both the generator process (`/proc/self/smaps_rollup`) and the Playwright driver + browser descendant tree while exposing full `VmRSS`, `Pss`, and cgroup `memory.current` / `memory.max` figures for qualification.

2. **Subprocess & `/proc` Qualification Tests (`pkg/protocol/browser/evidence_budget_test.go`, `.github/workflows/ci.yml`):**
   - Added `TestProcProcessSamplesPreferSmapsRollupPSSOverVmRSS` verifying `smaps_rollup` `Pss` preference, `VmRSS` preservation, and `VmRSS` fallback when `smaps_rollup` is absent.
   - Added `TestBrowserMemoryAdmissionActual`, which calibrates 1 live headless Chromium context's `measureBrowserMemoryBreakdown` and spawns a re-exec'd child process with `APEXLOAD_BROWSER_MEMORY_LIMIT_MB` set so Context 1 is admitted with real `measureBrowserMemoryUsage` (`ContextsUnmeasured == 0`, `MemorySampleFailures == 0`), a subsequent context is refused after refreshing the live sample (`ContextsRefusedByMemory == 1`, `Execute` fails with `browser_memory_admission` and `browser_resource_limit_browser_memory_admission`), and teardown releases all live reservations.
   - Added `TestBrowserMemoryAdmissionActual|TestProcProcessSamplesPreferSmapsRollupPSSOverVmRSS|TestProcProcessSamplesIncludeThisProcess` to the Linux browser step in `.github/workflows/ci.yml`.

---

## 2. Measured Linux Container & Host Qualification Evidence

| Environment | Context 1 Generator RSS / PSS | Context 1 Driver+Browser Tree `VmRSS` | Context 1 Driver+Browser Tree `Pss` | Tree PIDs | Cgroup `memory.current` / `memory.max` | Child `APEXLOAD_BROWSER_MEMORY_LIMIT_MB` | Refused Context | Counters (`Refused` / `Unmeasured` / `Failures`) |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| Linux (`apexload-generator:local`, `--memory=2g`) | `29 MiB` / `28 MiB` | `1,015 MiB` | `399 MiB` | `10` | `960 MiB` / `2,048 MiB` | `700 MiB` | `b4` (`683 MiB + 128 MiB > 700 MiB`) | `1` / `0` / `0` |
| macOS Host (`darwin/arm64`) | `30 MiB` / `n/a` | `420 MiB` | `n/a` (`VmRSS` fallback) | `5` | `0 MiB` / `0 MiB` | `647 MiB` | `b3` (`579 MiB + 128 MiB > 647 MiB`) | `1` / `0` / `0` |

Notice that on Linux, summing raw `VmRSS` across the 10-process Playwright driver + Chromium process tree reports `1,015 MiB` (due to shared Zygote mappings), whereas summing `/proc/<pid>/smaps_rollup` `Pss` reports the true proportional resident footprint of `399 MiB` (`427 MiB` total including the `28 MiB` generator process).

---

## 3. Verification & Mutation Testing (`go test -overlay`)

- **Host & Linux Container Suites:**
  - `APEXLOAD_BROWSER_TESTS=require go test -v ./pkg/protocol/browser -run "TestBrowserMemoryAdmissionActual|TestProcProcessSamples|TestProcessTree|TestBrowserContextAdmission|TestBrowserMemoryUsageMeasuresTheDriverTree" -count=1` -> **PASS** (`2.86s` on macOS; `4.60s` in 2 GiB Linux container).
- **`go test -overlay` Mutation Checks:**
  - `mut1_pss` (`pkg/protocol/browser/evidence_budget.go`: disabled `smaps_rollup` `Pss` branch in `readProcProcessSamples`) -> **Caught** by `TestProcProcessSamplesPreferSmapsRollupPSSOverVmRSS`.
  - `mut2_admission` (`pkg/protocol/browser/evidence_budget.go`: disabled `projected > limit` refusal in `admitBrowserContext`) -> **Caught** by `TestBrowserMemoryAdmissionActual`.
