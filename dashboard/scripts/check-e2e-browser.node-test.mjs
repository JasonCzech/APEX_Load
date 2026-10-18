import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, rmSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { checkE2EBrowser } from './check-e2e-browser.mjs'

test('preflight launches the default headless browser and closes it', async () => {
  let closed = false
  await checkE2EBrowser(async () => ({ launch: async options => {
    assert.deepEqual(options, { headless: true, timeout: 15_000 })
    return { close: async () => { closed = true } }
  } }))
  assert.equal(closed, true)
})

test('preflight distinguishes missing dependencies from an unusable browser', async () => {
  await assert.rejects(checkE2EBrowser(async () => { throw new Error('module missing') }), /npm ci/)
  await assert.rejects(checkE2EBrowser(async () => ({ launch: async () => { throw new Error('library missing') } })), /playwright install chromium/)
})

test('a missing pinned browser fails before an expensive controller build', () => {
  const browsers = mkdtempSync(join(tmpdir(), 'apexload-browser-preflight-'))
  try {
    const result = spawnSync(process.execPath, [new URL('./check-e2e-browser.mjs', import.meta.url).pathname], {
      encoding: 'utf8', timeout: 30_000,
      env: { ...process.env, PLAYWRIGHT_BROWSERS_PATH: browsers },
    })
    assert.equal(result.error, undefined)
    assert.equal(result.status, 1)
    assert.match(result.stderr, /playwright install chromium/)
    assert.match(result.stderr, /Executable doesn't exist/)
    const runner = readFileSync(new URL('../../scripts/run-e2e.sh', import.meta.url), 'utf8')
    assert.ok(runner.indexOf('node dashboard/scripts/check-e2e-browser.mjs') < runner.indexOf('go build -trimpath'))
  } finally {
    rmSync(browsers, { recursive: true, force: true })
  }
})
