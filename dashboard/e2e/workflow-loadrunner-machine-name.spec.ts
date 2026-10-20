import { test, expect } from './support/workflow'
import { checkUSZPickerAndReselection, openLoadRunnerImport } from './support/loadrunnerImport'

// Uploads over plain HTTP from a machine name: not a secure context, so the
// dashboard must not depend on secure-only crypto APIs. Launch options apply
// to the whole file, which is why this test has its own spec.
test.use({ launchOptions: { args: ['--host-resolver-rules=MAP apexload-upload.test 127.0.0.1'] } })

test('queues and validates USZ archives without secure-context crypto APIs', async ({ page, baseURL }) => {
  expect(baseURL).toBeTruthy()
  const origin = new URL(baseURL!)
  expect(origin.protocol).toBe('http:')
  origin.hostname = 'apexload-upload.test'
  const pageErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.message))
  await openLoadRunnerImport(page, origin.origin)
  expect(await page.evaluate(() => ({
    secure: window.isSecureContext,
    randomUUID: typeof globalThis.crypto?.randomUUID,
    getRandomValues: typeof globalThis.crypto?.getRandomValues,
  }))).toEqual({ secure: false, randomUUID: 'undefined', getRandomValues: 'function' })
  await checkUSZPickerAndReselection(page)
  expect(pageErrors).toEqual([])
})
