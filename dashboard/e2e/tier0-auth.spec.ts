import { expect, test } from '@playwright/test'

// Tier-0 journey step S1 (docs/qa/E2E_SUITES.md): authenticate in the
// explicitly selected E2E mode, land on Dashboard, and prove the live shell
// exposes the navigation granted to the default dev-admin preview.
test('lands in the explicit dev-admin workspace with a live connection', async ({ page, request }) => {
  const auth = await request.get('/api/v1/auth/me')
  expect(auth.ok()).toBeTruthy()
  await expect(auth.json()).resolves.toMatchObject({
    enabled: false,
    mode: 'dev',
  })

  await page.goto('/#/dashboard')

  await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  await expect(page.locator('.sidebar-status')).toHaveText('Live')
  await page.getByRole('button', { name: 'Preview permissions. Current mode: admin', exact: true }).click()
  await expect(page.getByRole('group', { name: 'Development dashboard view' }).getByRole('button', { name: 'Admin' })).toHaveAttribute('aria-pressed', 'true')

  const navigation = page.locator('nav.sidebar-nav')
  for (const name of ['Dashboard', 'New Test', 'Command Center', 'Results', 'Scripts', 'Scenarios', 'Repository', 'Monitoring', 'Help & documentation', 'Access', 'Settings']) {
    await expect(navigation.getByRole('button', { name, exact: true })).toBeVisible()
  }
  await expect(navigation.getByRole('button', { name: 'Dashboard', exact: true })).toHaveAttribute('aria-current', 'page')
})
