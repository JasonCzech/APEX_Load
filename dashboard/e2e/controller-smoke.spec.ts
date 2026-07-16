import { expect, test } from '@playwright/test'

test('serves the dashboard, an SPA route, and healthy controller endpoints', async ({ page, request }) => {
  const consoleErrors: string[] = []
  const failedAssets: string[] = []

  page.on('console', message => {
    if (message.type() === 'error') consoleErrors.push(message.text())
  })
  page.on('requestfailed', request => {
    const url = new URL(request.url())
    if (url.pathname.startsWith('/assets/')) {
      failedAssets.push(`${url.pathname}: ${request.failure()?.errorText || 'request failed'}`)
    }
  })

  const health = await request.get('/api/v1/system/health')
  expect(health.ok()).toBeTruthy()

  const readiness = await request.get('/api/v1/system/readiness')
  expect(readiness.ok()).toBeTruthy()
  expect(['ready', 'degraded']).toContain((await readiness.json()).status)

  await page.goto('/')
  await expect(page.locator('#root')).toBeAttached()
  await expect(page.locator('body')).not.toBeEmpty()

  await page.goto('/#/settings')
  await expect(page.locator('#root')).toBeAttached()
  await page.waitForLoadState('networkidle')

  expect(failedAssets).toEqual([])
  expect(consoleErrors).toEqual([])
})
