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
// September usability verification: baseline is 5.28 MiB JS / 1.42 MiB gzip
// and 699.63 KiB CSS. Guided setup, outcome evidence and responsive fixes add
// about 0.01 MiB JS and 2.74 KiB CSS. Reconcile aggregate ceilings to that
// measured inventory; per-asset and route startup/transfer limits stay fixed.
export const DEFAULT_BUNDLE_BUDGETS = Object.freeze({
  '.js': Object.freeze({
    label: 'JavaScript',
    maxAssetBytes: kib(650),
    maxAssetGzipBytes: kib(220),
    maxTotalBytes: mib(5.30),
    maxTotalGzipBytes: mib(1.425),
  }),
  '.css': Object.freeze({
    label: 'CSS',
    maxAssetBytes: kib(210),
    maxAssetGzipBytes: kib(34),
    maxTotalBytes: kib(704),
    maxTotalGzipBytes: kib(121),
  }),
})

export const DEFAULT_ROUTE_BUDGETS = Object.freeze([
  Object.freeze({
    id: 'initial-shell',
    label: 'Initial shell',
    entryKey: 'index.html',
    maxGzipBytes: kib(185),
    forbiddenAssets: [/codemirror/i, /vendor-three/i, /ScriptEditor/i],
  }),
  Object.freeze({
    id: 'scripts-library',
    label: 'Scripts library route',
    entryFile: /ScriptsPage-.*\.js$/,
    maxGzipBytes: kib(225),
    forbiddenAssets: [/codemirror/i, /ScriptEditor/i],
  }),
  Object.freeze({
    id: 'test-execution',
    label: 'Test execution route',
    entryKey: 'src/components/TestExecution.tsx',
    maxGzipBytes: kib(380),
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
