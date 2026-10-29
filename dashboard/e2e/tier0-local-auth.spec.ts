import { expect, test } from '@playwright/test'

test.beforeEach(() => {
  test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'local-auth', 'requires the local-auth E2E profile')
})

test('rejects invalid local credentials without opening the workspace', async ({ page }) => {
  await page.goto('/#/dashboard')
  await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password').fill('wrong-password')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
})

test('signs in as the seeded local administrator and lands in the live workspace', async ({ page }) => {
  await page.goto('/#/dashboard')
  await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password').fill('admin')
  await page.getByRole('button', { name: 'Sign in' }).click()

  await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  await expect(page.locator('.sidebar-status')).toHaveText('Live connection: Live')
  const navigation = page.locator('nav.sidebar-nav')
  await expect(navigation.getByRole('button', { name: 'Access', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: 'Settings', exact: true })).toBeVisible()

  const auth = await page.context().request.get('/api/v1/auth/me')
  expect(auth.ok()).toBeTruthy()
  await expect(auth.json()).resolves.toMatchObject({
    authenticated: true,
    enabled: true,
    mode: 'local',
    user: { subject: 'local:admin' },
  })
})

test('signs out using the account avatar on a narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 760, height: 1000 })
  await page.goto('/#/dashboard')
  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password').fill('admin')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  const avatar = page.getByRole('button', { name: /^Open account controls for / })
  await expect(avatar).toBeVisible()
  await avatar.focus()
  await page.keyboard.press('Enter')
  const signOut = page.getByRole('button', { name: 'Sign out', exact: true })
  await expect(signOut).toBeFocused()
  const collapse = page.getByRole('button', { name: 'Collapse sidebar', exact: true })
  await expect(collapse).toBeVisible()
  await collapse.focus()
  await page.keyboard.press('Enter')
  await expect(avatar).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(signOut).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
})
