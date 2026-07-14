import { spawn } from 'node:child_process'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { DEFAULT_BUNDLE_BUDGETS, DEFAULT_ROUTE_BUDGETS, formatBundleReport, inspectBundle } from './check-bundle-budget.mjs'

const dashboardDirectory = dirname(dirname(fileURLToPath(import.meta.url)))
const viteEntry = join(dashboardDirectory, 'node_modules', 'vite', 'bin', 'vite.js')
const outputDirectory = await mkdtemp(join(tmpdir(), 'apexload-dashboard-bundle-'))

function runViteBuild() {
  return new Promise((resolve, reject) => {
    const child = spawn(
      process.execPath,
      [viteEntry, 'build', '--manifest', '--outDir', outputDirectory, '--emptyOutDir'],
      {
        cwd: dashboardDirectory,
        env: {
          ...process.env,
          APEXLOAD_UX_BUILD_TIME:
            process.env.APEXLOAD_UX_BUILD_TIME || '1970-01-01T00:00:00.000Z',
          APEXLOAD_VITE_BUILD_TIME:
            process.env.APEXLOAD_VITE_BUILD_TIME || '1970-01-01T00:00:00.000Z',
        },
        stdio: 'inherit',
      },
    )
    child.on('error', reject)
    child.on('exit', (code, signal) => {
      if (code === 0) resolve()
      else reject(new Error(`Vite build failed (${signal ? `signal ${signal}` : `exit ${code}`})`))
    })
  })
}

try {
  await runViteBuild()
  const result = await inspectBundle(outputDirectory, DEFAULT_BUNDLE_BUDGETS, DEFAULT_ROUTE_BUDGETS)
  process.stdout.write(formatBundleReport(result))
  if (result.violations.length > 0) process.exitCode = 1
} catch (error) {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`)
  process.exitCode = 1
} finally {
  await rm(outputDirectory, { recursive: true, force: true })
}
