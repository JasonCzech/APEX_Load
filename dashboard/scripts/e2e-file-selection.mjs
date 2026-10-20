import { createHash } from 'node:crypto'
import { readFileSync, realpathSync } from 'node:fs'
import { isAbsolute, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

// The installed Playwright JSON reporter owns selection; this adapter only
// partitions that selection into files and creates exact CLI regex filters.
export function selectedFiles(report, configFile, testDir) {
  if (!report || !Array.isArray(report.errors) || report.errors.length || !Array.isArray(report.suites)
    || resolve(report.config?.configFile || '.') !== resolve(configFile)) {
    throw new Error('Playwright selection failed or used an unexpected configuration')
  }
  const files = new Set()
  function visit(suites, depth = 0) {
    if (depth > 32) throw new Error('Playwright selection nesting exceeds bound')
    for (const suite of suites) {
      for (const spec of suite.specs || []) {
        if (!Array.isArray(spec.tests) || !spec.tests.length) continue
        const file = spec.file
        if (typeof file !== 'string' || !file.endsWith('.spec.ts') || /[\r\n\t]/.test(file) || file.length > 4096 || isAbsolute(file)) {
          throw new Error('Invalid selected workflow file')
        }
        const absolute = resolve(testDir, file)
        const within = relative(resolve(testDir), absolute)
        if (within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Selected workflow is outside testDir')
        files.add(absolute)
        if (files.size > 1024) throw new Error('Too many selected workflow files')
      }
      visit(suite.suites || [], depth + 1)
    }
  }
  visit(report.suites)
  if (!files.size) throw new Error('No workflow files selected; refusing an empty suite')
  return [...files].sort().map(file => ({
    file,
    id: createHash('sha256').update(relative(testDir, file)).digest('hex').slice(0, 16),
    filter: `^${file.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`,
  }))
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [, , input, configFile, testDir] = process.argv
    if (!input || !configFile || !testDir) throw new Error('Expected selection JSON, config and testDir')
    const root = realpathSync(testDir)
    const report = JSON.parse(readFileSync(input, 'utf8'))
    // macOS temporary directories can be reached through /var and /private/var.
    report.config.configFile = realpathSync(report.config.configFile)
    const rows = selectedFiles(report, realpathSync(configFile), root)
    for (const row of rows) {
      if (!realpathSync(row.file).startsWith(root + sep)) throw new Error('Selected workflow escapes testDir through a symlink')
      process.stdout.write(`${row.id}\t${row.filter}\t${relative(root, row.file)}\n`)
    }
  } catch (error) {
    console.error(error.message)
    process.exitCode = 1
  }
}
