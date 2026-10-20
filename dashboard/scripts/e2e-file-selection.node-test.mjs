import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, writeFileSync, symlinkSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import test from 'node:test'
import { selectedFiles } from './e2e-file-selection.mjs'

const config = resolve('/repo/dashboard/playwright.config.ts')
const root = resolve('/repo/dashboard/e2e')
const spec = file => ({ file, tests: [{}] })
const report = specs => ({ config: { configFile: config }, errors: [], suites: [{ specs }] })

test('selection deduplicates nested suites and escapes exact file filters', () => {
  const input = report([spec('a[1].spec.ts'), spec('other.spec.ts')])
  input.suites[0].suites = [{ specs: [spec('a[1].spec.ts')] }]
  const result = selectedFiles(input, config, root)
  assert.equal(result.length, 2)
  assert.notEqual(result[0].id, result[1].id)
  const filter = new RegExp(result[0].filter)
  assert.equal(filter.test(join(root, 'a[1].spec.ts')), true)
  for (const file of ['a1.spec.ts', 'a[1]Xspec.ts', 'a[1].spec.ts.bak']) assert.equal(filter.test(join(root, file)), false)
  assert.deepEqual(result, selectedFiles(input, config, root))
})

test('invalid, empty, outside and excessive selections fail closed', () => {
  for (const input of [null, { ...report([]), errors: [{}] }, report([]), { ...report([spec('a.spec.ts')]), config: {} }]) {
    assert.throws(() => selectedFiles(input, config, root))
  }
  for (const file of ['../escape.spec.ts', '/tmp/a.spec.ts', 'a\nb.spec.ts', 'a\tb.spec.ts', 'a.test.ts']) {
    assert.throws(() => selectedFiles(report([spec(file)]), config, root))
  }
  assert.throws(() => selectedFiles(report(Array.from({ length: 1025 }, (_, i) => spec(`${i}.spec.ts`))), config, root), /Too many/)
  const deep = report([spec('a.spec.ts')])
  let node = deep.suites[0]
  for (let i = 0; i < 34; i++) { node.suites = [{}]; node = node.suites[0] }
  assert.throws(() => selectedFiles(deep, config, root), /nesting/)
})

test('CLI refuses a selected file symlink outside its test directory', { skip:process.platform === 'win32' }, () => {
  const dir = mkdtempSync(join(tmpdir(), 'apex-e2e-selection-'))
  try {
    const tests = join(dir, 'e2e'); mkdirSync(tests)
    const outside = join(dir, 'outside.spec.ts'); writeFileSync(outside, '')
    symlinkSync(outside, join(tests, 'link.spec.ts'))
    const input = report([spec('link.spec.ts')]); input.config.configFile = join(dir, 'playwright.config.ts')
    writeFileSync(input.config.configFile, '')
    const json = join(dir, 'selection.json'); writeFileSync(json, JSON.stringify(input))
    const result = spawnSync(process.execPath, [new URL('./e2e-file-selection.mjs', import.meta.url).pathname, json, input.config.configFile, tests], { encoding: 'utf8' })
    assert.equal(result.status, 1)
    assert.match(result.stderr, /symlink/)
    assert.equal(result.stdout, '')
  } finally { rmSync(dir, { recursive: true, force: true }) }
})
