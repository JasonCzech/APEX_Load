import { expect, test } from '@playwright/test'

test('serves the dashboard, an SPA route, and healthy controller endpoints', async ({ page, request }) => {
  const consoleErrors: string[] = []
  const pageErrors: string[] = []
  const failedAssets: string[] = []
  const assetResourceTypes = new Set(['font', 'image', 'script', 'stylesheet'])

  page.on('console', message => {
    if (message.type() === 'error') consoleErrors.push(message.text())
  })
  page.on('pageerror', error => {
    pageErrors.push(error.message)
  })
  page.on('requestfailed', request => {
    if (assetResourceTypes.has(request.resourceType())) {
      failedAssets.push(`${request.url()}: ${request.failure()?.errorText || 'request failed'}`)
    }
  })
  page.on('response', response => {
    if (assetResourceTypes.has(response.request().resourceType()) && !response.ok()) {
      failedAssets.push(`${response.url()}: HTTP ${response.status()} ${response.statusText()}`)
    }
  })

  const health = await request.get('/api/v1/system/health')
  expect(health.ok()).toBeTruthy()

  const readiness = await request.get('/api/v1/system/readiness')
  expect(readiness.ok()).toBeTruthy()
  expect(['ready', 'degraded']).toContain((await readiness.json()).status)

  await page.goto('/')
  await expect(page.locator('#root')).not.toBeEmpty()
  await expect(page.getByRole('main')).toBeVisible()
  await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()

  await page.goto('/#/settings')
  await expect(page.locator('#root')).not.toBeEmpty()
  await expect(page.getByRole('main')).toBeVisible()
  await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible()
  await page.waitForLoadState('networkidle')

  expect(failedAssets).toEqual([])
  expect(consoleErrors).toEqual([])
  expect(pageErrors).toEqual([])
})
