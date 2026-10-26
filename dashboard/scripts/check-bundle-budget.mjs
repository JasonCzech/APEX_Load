import { readdir, readFile } from 'node:fs/promises'
import { extname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'

const kib = (value) => value * 1024
const mib = (value) => value * 1024 * 1024

// These limits are based on the deterministic production build. The raw JS cap
// includes modest headroom for structural scanners and other parser-heavy code;
// compressed aggregate and route-specific limits remain the transfer/startup
// regression guards. They are not an assertion that the current bundle has
// reached its long-term size target. The aggregate ceilings were recalibrated
// after the deterministic 2026-08-25 CloudTest parity, composition-timeline,
// report-export expansion, and the ART-aware production-profile sizing UI.
// Modal-only dependencies remain off the Scripts route, and the tighter
// per-asset and route budgets continue to catch loading regressions
// independently of intentionally deferred feature chunks.
// The 2026-10-05 committed HEAD baseline independently measured 6.93 MiB JS /
// 1.93 MiB gzip and 888.47 KiB CSS / 159.10 KiB gzip. The composed candidate
// includes deferred resource inspection and measures 6.99 MiB / 1.95 MiB JS
// and 890.47 KiB / 160.75 KiB CSS after the access-style split. The scoped
// composed-review QA bundle retains both builds and their exact inventories.
// Reconcile only aggregate inventory limits with modest measured headroom;
// individual assets, route budgets and forbidden startup dependencies remain
// separate strict guards. Deferred feature inventory is not startup cost.
//
// 2026-10-16 recalibration. The check had failed on main since at least
// 2026-10-06, so builds of main were measured day by day:
// - JS: 7.11 MiB on 10/06, 7.42 on 10/09, 7.78 on 10/12, 7.99 on 10/14
//   and 8.12 on 10/15.
// - ScriptEditor raw: 628, 647, 689, 761 and 766 KiB over the same days.
// - CSS: 897 KiB to 999 KiB.
// The growth is shipped features in deferred chunks: the editor's replay
// debugger, source mapping and full-source review; LoadRunner parity; native
// analytics and RCA views; and live monitoring evidence.
// Two structural regressions were fixed rather than budgeted:
// - Routes imported the shell entry. A moved navigation guard, unpinned
//   shared modules, and one unordered chunk group (whose first-visited
//   module claimed the others' dependencies) put them in the entry.
// - The execution route statically loaded the saved-archive view and the
//   report composer.
// The fixed build measures:
// - JS: 8.13 MiB / 2.32 MiB gzip, largest asset ScriptEditor at 758 KiB
//   raw / 202 KiB gzip.
// - CSS: 996 KiB / 181 KiB gzip, largest asset index.css at 225 KiB /
//   36.5 KiB gzip.
// - Initial shell 199 KiB gzip (the 10/15 desktop shell integration),
//   Scripts route 227 KiB, Test execution route 406 KiB.
// Limits keep about 4-6% headroom over those numbers. The per-asset gzip
// ceiling and every forbidden asset or entry stay as they were.
// 2026-10-20 precision-consent integration: an independent main-source build
// measures 2.40 MiB gzip; the candidate measures 2.43 MiB. The newly bundled,
// lazily loaded native analytics Help article accounts for about 24 KiB of the
// 25.5 KiB increase; the remaining growth is the deferred precision-review UI.
// Initial shell, Scripts and Test execution remain about 200/228/411 KiB gzip.
// Allow that measured inventory at 2.45 MiB; keep raw, per-asset, CSS and every
// route/startup guard unchanged. Evidence: roadmap-e2e-isolation-2026-10-20.
//
// 2026-10-24 recalibration. The aggregates were already over at 33125479c
// (PR #267 merged): 8.54 MiB / 2.46 MiB gzip JS, Test execution route
// 417.98 KiB. PR #269 (Help data model) had MarkdownRenderer statically load
// the ER diagram renderer and its parser (route 422.11 KiB); PR #270
// (telemetry policy segments) had the live status panels statically load
// the segments list and budget-change form (route 427.54 KiB). Main at
// da21eec8b measured 8.63 MiB / 2.48 MiB gzip JS and a 427.53 KiB route.
// Both route regressions were fixed rather than budgeted:
// - The status panels load the segments list only when a run has two or
//   more segments and the change form only for an editor of a running or
//   paused run (423.39 KiB).
// - MarkdownRenderer loads the ER diagram renderer only for a Mermaid block
//   that declares an erDiagram, showing its source until then (419.71 KiB).
// The fixed build measures:
// - JS: 8.63 MiB (8836 KiB) / 2.48 MiB (2541 KiB) gzip, largest asset
//   ScriptEditor at 765 KiB raw / 204 KiB gzip.
// - CSS: 1005 KiB / 183 KiB gzip, largest asset index.css at 225 KiB /
//   36.5 KiB gzip.
// - Initial shell 202 KiB gzip, Scripts route 230 KiB, Test execution route
//   420 KiB.
// The JS aggregate limits move to 9.0 MiB raw and 2.6 MiB gzip (4.3% and
// 4.8% headroom). Per-asset, CSS and every route/startup guard stay as they
// were.
//
// 2026-10-26 per-asset recalibration. Main's script debugger work
// (819563141 completion summary and retained HAR downloads, 746c34f6a HAR
// capture controls and transaction breakdowns) grows the lazily loaded
// ScriptEditor chunk from 788 KiB to 808.25 KiB raw (218.30 KiB gzip),
// past the 800 KiB per-asset limit; a build of main alone measures the same.
// The growth is the editor's own deferred debugger, not startup cost: the
// chunk stays off every route's static graph. The merged build (7ad740fde)
// measures JS 8.73 MiB / 2.52 MiB gzip, CSS 1013 KiB / 184 KiB gzip,
// Scripts route 230.59 KiB and Test execution route 420.41 KiB. The JS
// per-asset limits move to 850 KiB raw and 230 KiB gzip (5.2% and 5.4%
// headroom). Aggregates, CSS and every route/startup guard stay as they were.
export const DEFAULT_BUNDLE_BUDGETS = Object.freeze({
  '.js': Object.freeze({
    label: 'JavaScript',
    maxAssetBytes: kib(850),
    maxAssetGzipBytes: kib(230),
    maxTotalBytes: mib(9),
    maxTotalGzipBytes: mib(2.6),
  }),
  '.css': Object.freeze({
    label: 'CSS',
    maxAssetBytes: kib(236),
    maxAssetGzipBytes: kib(38),
    maxTotalBytes: kib(1040),
    maxTotalGzipBytes: kib(190),
  }),
})

export const DEFAULT_ROUTE_BUDGETS = Object.freeze([
  Object.freeze({
    id: 'initial-shell',
    label: 'Initial shell',
    entryKey: 'index.html',
    maxGzipBytes: kib(208),
    forbiddenAssets: [/codemirror/i, /vendor-three/i, /ScriptEditor/i],
  }),
  Object.freeze({
    id: 'scripts-library',
    label: 'Scripts library route',
    entryFile: /ScriptsPage-.*\.js$/,
    forbiddenEntryKeys: ['index.html'],
    maxGzipBytes: kib(238),
    forbiddenAssets: [/codemirror/i, /ScriptEditor/i],
  }),
  Object.freeze({
    id: 'test-execution',
    label: 'Test execution route',
    entryKey: 'src/components/TestExecution.tsx',
    forbiddenEntryKeys: ['index.html'],
    maxGzipBytes: kib(425),
    forbiddenAssets: [/codemirror/i, /vendor-three/i, /LoadImpact3DHeatmap/i],
  }),
])

async function collectFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = []

  for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      files.push(...(await collectFiles(path)))
    } else if (entry.isFile()) {
      files.push(path)
    }
  }
  return files
}

export function formatBytes(bytes) {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(2)} MiB`
  return `${(bytes / 1024).toFixed(2)} KiB`
}

function findRouteEntry(manifest, budget) {
  if (budget.entryKey && manifest[budget.entryKey]) return budget.entryKey
  return Object.entries(manifest).find(([, entry]) => (
    entry.isDynamicEntry && budget.entryFile?.test(entry.file)
  ))?.[0]
}

function collectStaticRouteEntries(manifest, entryKey, collected = new Set()) {
  if (!entryKey || collected.has(entryKey) || !manifest[entryKey]) return collected
  collected.add(entryKey)
  for (const importedKey of manifest[entryKey].imports || []) {
    collectStaticRouteEntries(manifest, importedKey, collected)
  }
  return collected
}

export async function inspectRouteBudgets(root, routeBudgets = DEFAULT_ROUTE_BUDGETS) {
  const manifestPath = join(root, '.vite', 'manifest.json')
  let manifest
  try {
    manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
  } catch (error) {
    return {
      routes: [],
      violations: [`Route budgets: unable to read ${relative(root, manifestPath)} (${error instanceof Error ? error.message : String(error)})`],
    }
  }

  const routes = []
  const violations = []
  for (const budget of routeBudgets) {
    const entryKey = findRouteEntry(manifest, budget)
    if (!entryKey) {
      violations.push(`${budget.label}: route entry was not found in the Vite manifest`)
      continue
    }
    const entryKeys = Array.from(collectStaticRouteEntries(manifest, entryKey))
    const assets = []
    for (const key of entryKeys) {
      const file = manifest[key]?.file
      if (!file || extname(file) !== '.js') continue
      const contents = await readFile(join(root, file))
      assets.push({ path: file, gzipBytes: gzipSync(contents, { level: 9 }).byteLength })
    }
    const totalGzipBytes = assets.reduce((total, asset) => total + asset.gzipBytes, 0)
    routes.push({ ...budget, entryKey, assets, totalGzipBytes })
    if (totalGzipBytes > budget.maxGzipBytes) {
      violations.push(
        `${budget.label} static JavaScript is ${formatBytes(totalGzipBytes)} gzip ` +
          `(limit ${formatBytes(budget.maxGzipBytes)})`,
      )
    }
    for (const forbiddenEntry of budget.forbiddenEntryKeys || []) {
      if (entryKeys.includes(forbiddenEntry)) {
        violations.push(`${budget.label} unexpectedly imports the ${forbiddenEntry} entry`)
      }
    }
    for (const asset of assets) {
      const forbidden = budget.forbiddenAssets?.find(pattern => pattern.test(asset.path))
      if (forbidden) violations.push(`${budget.label} unexpectedly includes ${asset.path}`)
    }
  }
  return { routes, violations }
}

export async function inspectBundle(directory, budgets = DEFAULT_BUNDLE_BUDGETS, routeBudgets = null) {
  const root = resolve(directory)
  const files = await collectFiles(root)
  const groups = {}
  const violations = []

  for (const [extension, budget] of Object.entries(budgets)) {
    const assets = []
    for (const path of files.filter((file) => extname(file) === extension)) {
      const contents = await readFile(path)
      assets.push({
        path: relative(root, path),
        bytes: contents.byteLength,
        gzipBytes: gzipSync(contents, { level: 9 }).byteLength,
      })
    }

    assets.sort((left, right) => left.path.localeCompare(right.path))
    const totalBytes = assets.reduce((total, asset) => total + asset.bytes, 0)
    const totalGzipBytes = assets.reduce((total, asset) => total + asset.gzipBytes, 0)
    groups[extension] = { ...budget, assets, totalBytes, totalGzipBytes }

    if (assets.length === 0) {
      violations.push(`${budget.label}: no ${extension} assets found`)
      continue
    }
    for (const asset of assets) {
      if (asset.bytes > budget.maxAssetBytes) {
        violations.push(
          `${budget.label} asset ${asset.path} is ${formatBytes(asset.bytes)} ` +
            `(limit ${formatBytes(budget.maxAssetBytes)})`,
        )
      }
      if (asset.gzipBytes > budget.maxAssetGzipBytes) {
        violations.push(
          `${budget.label} asset ${asset.path} is ${formatBytes(asset.gzipBytes)} gzip ` +
            `(limit ${formatBytes(budget.maxAssetGzipBytes)})`,
        )
      }
    }
    if (totalBytes > budget.maxTotalBytes) {
      violations.push(
        `${budget.label} total is ${formatBytes(totalBytes)} ` +
          `(limit ${formatBytes(budget.maxTotalBytes)})`,
      )
    }
    if (totalGzipBytes > budget.maxTotalGzipBytes) {
      violations.push(
        `${budget.label} total is ${formatBytes(totalGzipBytes)} gzip ` +
          `(limit ${formatBytes(budget.maxTotalGzipBytes)})`,
      )
    }
  }

  let routes = []
  if (routeBudgets) {
    const routeResult = await inspectRouteBudgets(root, routeBudgets)
    routes = routeResult.routes
    violations.push(...routeResult.violations)
  }

  return { root, groups, routes, violations }
}

export function formatBundleReport(result) {
  const lines = [`Bundle budget report: ${result.root}`]
  for (const [extension, group] of Object.entries(result.groups)) {
    const largest = [...group.assets].sort((left, right) => right.gzipBytes - left.gzipBytes)[0]
    lines.push(
      `${group.label}: ${group.assets.length} ${extension} files, ` +
        `${formatBytes(group.totalBytes)} raw / ${formatBytes(group.totalGzipBytes)} gzip`,
    )
    if (largest) {
      lines.push(
        `  largest gzip asset: ${largest.path} ` +
          `(${formatBytes(largest.bytes)} raw / ${formatBytes(largest.gzipBytes)} gzip)`,
      )
    }
  }
  for (const route of result.routes || []) {
    lines.push(
      `${route.label}: ${route.assets.length} static .js files, ` +
        `${formatBytes(route.totalGzipBytes)} gzip ` +
        `(limit ${formatBytes(route.maxGzipBytes)})`,
    )
  }
  if (result.violations.length === 0) {
    lines.push('Bundle is within the configured regression budgets.')
  } else {
    lines.push('Bundle budget violations:')
    lines.push(...result.violations.map((violation) => `  - ${violation}`))
  }
  return `${lines.join('\n')}\n`
}

async function main() {
  const directory = process.argv[2]
  if (!directory) throw new Error('Usage: node scripts/check-bundle-budget.mjs <build-directory>')
  const result = await inspectBundle(directory, DEFAULT_BUNDLE_BUDGETS, DEFAULT_ROUTE_BUDGETS)
  process.stdout.write(formatBundleReport(result))
  process.exitCode = result.violations.length > 0 ? 1 : 0
}

const invokedPath = process.argv[1] ? resolve(process.argv[1]) : ''
if (invokedPath === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`)
    process.exitCode = 1
  })
}
